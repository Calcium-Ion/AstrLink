// Package pluginhost runs the plugins' part in inference requests: the views
// a plugin reads, the privacy check of what leaves for a plugin, the
// prepareSession and prepareTurn hooks within the turn's budget, the blocks
// they return, the session bindings marker lines, answers and workspaces
// make, and the observeTurn queue. It also keeps what the user controls of
// that part: the pause of every plugin, the clients a plugin is off for, the
// sessions it is paused in, the budget of what plugins inject, and the
// refresh of a session's resident block.
//
// Plugins never hold a request up for long and never fail one: whatever goes
// wrong on their side costs the request its injection or its capture, and the
// request record says why. Privacy is the exception: when the check of the
// turn's text cannot run, no text leaves for a plugin the user does not trust.
package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/injection"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Budgets of a request's plugins. A request waits for each plugin as long
// as the plugin's settings say, counted from the moment it starts the
// plugins; the privacy check of the turn's text counts against it.
const (
	// sessionHookTimeout bounds prepareSession. A request stops waiting at
	// the plugin's wait, but the hook runs on: the block it computes serves
	// the session's later calls.
	sessionHookTimeout = 10 * time.Second
	// turnHookSlack is how long prepareTurn may run past the wait; its
	// result is no use by then.
	turnHookSlack = 500 * time.Millisecond
	// turnNetworkMargin is the part of the wait prepareTurn keeps after its
	// requests end, to build its result.
	turnNetworkMargin = 100 * time.Millisecond
	// inspectTimeout bounds the privacy check. Past the budget it still
	// decides what observeTurn may capture.
	inspectTimeout = 30 * time.Second
	// bindingTimeout bounds each read and write of session bindings.
	bindingTimeout = 500 * time.Millisecond
	// recordAppendTimeout bounds adding a capture's record to its request.
	recordAppendTimeout = 5 * time.Second
	// refreshInterval is how often the automatic refresh checks a session's
	// resident block for newer data, and how often a compacted history may
	// refresh it: a client's subagents can look like compactions as they
	// come and go.
	refreshInterval = 10 * time.Minute
)

// ErrBlocked is what Input.Inspect returns when the privacy policy blocks
// the text.
var ErrBlocked = errors.New("the privacy policy blocks the text")

// Options configure a Host.
type Options struct {
	Plugins  *plugin.Manager
	Metadata *metadata.Registry
	// Bindings keeps the sessions' metadata values and the workspaces'.
	Bindings storage.MetadataStore
	// Store keeps the plugins' part in each session and the settings of
	// every plugin.
	Store PluginStore
	// Records adds what captures report to the records of their requests.
	Records RecordAppender
	// WorkspaceKey keys the fingerprints of workspaces. Without it a new
	// session never takes the value its workspace had.
	WorkspaceKey []byte
	Logf         func(string, ...any)
	// Credentials reads and writes the secret of a provider, for the
	// credentials plugins log providers in with.
	Credentials CredentialStore
	// Send sends the requests plugins make for their providers outside a
	// request, such as a balance; ProxyCredentials are the providers'
	// proxy passwords.
	Send             RoundTrip
	ProxyCredentials secretstore.SecretStore
}

// PluginStore keeps what the host stores for plugins; *sqlite.Store
// satisfies it.
type PluginStore interface {
	PluginHostSettings(context.Context) (contract.PluginHostSettings, error)
	PutPluginHostSettings(context.Context, contract.PluginHostSettings) error
	PluginSession(ctx context.Context, plugin, session string) (storage.PluginSessionRecord, bool, error)
	StartPluginSession(ctx context.Context, plugin, session string, turn int) (storage.PluginSessionRecord, error)
	PausePluginSession(ctx context.Context, plugin, session string, paused bool, turn int) (storage.PluginSessionRecord, error)
}

// RecordAppender adds plugin records to a request record already written.
type RecordAppender interface {
	AppendRequestPluginRecords(ctx context.Context, id contract.RequestID, records []contract.RequestPluginRecord) error
}

// Host runs the plugins' part in requests. It is safe for concurrent use.
type Host struct {
	plugins      *plugin.Manager
	metadata     *metadata.Registry
	bindings     storage.MetadataStore
	store        PluginStore
	ledger       *injection.Ledger
	sessions     *sessions
	lookups      *lookups
	observe      *observeQueue
	after        *afterQueue
	held         *heldCaptures
	records      RecordAppender
	workspaceKey []byte
	logf         func(string, ...any)
	// reports runs the report hooks of request processing instances.
	reports reportQueue
	// relayTokens keeps the exchangeKey tokens of relay providers.
	relayTokens relayTokens
	// credentials keeps the credentials plugins log providers in with;
	// logins are the logins in progress, whose vendors send the browser
	// back to loginBase.
	credentials     CredentialStore
	credentialLocks credentialLocks
	logins          pluginLogins
	gatewayAddress  atomic.Value
	// providers keeps the balance and rest of each plugin's provider; send
	// sends the balance requests, through each provider's proxy.
	providers        providerStates
	send             RoundTrip
	proxyCredentials secretstore.SecretStore
	// settings are the settings of every plugin, as requests read them.
	settings atomic.Pointer[contract.PluginHostSettings]
	// breakers keep a plugin's extension point out of requests after it
	// failed too often, by plugin/hook.
	breakers sync.Map
	// forwardTurns remembers the turn each plugin's beforeForward last
	// settled its turn block in, by session and plugin.
	forwardTurns *forwardTurns
	// budget, when set, stands for every plugin's wait; tests shorten it.
	budget time.Duration
	// now is the host's clock; tests move it.
	now func() time.Time
}

// New returns a Host with the default settings of every plugin until
// LoadSettings reads the stored ones. Close stops it.
func New(options Options) *Host {
	logf := options.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	host := &Host{
		plugins:          options.Plugins,
		metadata:         options.Metadata,
		bindings:         options.Bindings,
		store:            options.Store,
		ledger:           injection.NewLedger(),
		sessions:         newSessions(),
		lookups:          newLookups(),
		observe:          newObserveQueue(logf),
		after:            newAfterQueue(),
		held:             newHeldCaptures(),
		forwardTurns:     newForwardTurns(),
		records:          options.Records,
		workspaceKey:     options.WorkspaceKey,
		logf:             logf,
		credentials:      options.Credentials,
		send:             options.Send,
		proxyCredentials: options.ProxyCredentials,
		now:              time.Now,
	}
	defaults := contract.DefaultPluginHostSettings()
	host.settings.Store(&defaults)
	return host
}

// turnWait is how long a request waits for candidate.
func (host *Host) turnWait(candidate *plugin.Plugin) time.Duration {
	if host.budget > 0 {
		return host.budget
	}
	return candidate.Settings.Wait()
}

// appendRecords adds records to the written record of request id.
func (host *Host) appendRecords(id contract.RequestID, records []contract.RequestPluginRecord) {
	if host.records == nil || id == "" || len(records) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), recordAppendTimeout)
	defer cancel()
	if err := host.records.AppendRequestPluginRecords(ctx, id, records); err != nil {
		host.logf("keep a capture's record with its request: %v", err)
	}
}

// Close stops the observeTurn queue and the calls that follow requests.
// What is still waiting is dropped.
func (host *Host) Close() {
	if host != nil {
		host.observe.close()
		host.after.close()
	}
}

// Input is a request that may run plugins.
type Input struct {
	// RequestID names the request's record, which captures that end after
	// it is written add their records to.
	RequestID contract.RequestID
	Protocol  contract.ProtocolID
	// Model is the model the client asked for, before any redirect.
	Model  string
	Client string
	Stream bool
	Token  contract.AccessTokenID
	Header http.Header
	// Session and Turn place the request in its conversation. Turn is 0
	// when the request has no user turn.
	Session string
	Turn    int
	// Body is the client's body as it came.
	Body []byte
	// Inspect checks texts against the privacy policy before they go to a
	// plugin the user does not trust, and returns them as an upstream would
	// see them; ErrBlocked when the policy blocks them, another error when
	// the check failed. nil when no policy applies.
	Inspect func(ctx context.Context, texts []string) ([]string, error)
	// Redacts reports whether the privacy policy puts placeholders in place
	// of what it finds: then what a plugin the user does not trust keeps
	// holds placeholders too. nil when no policy applies.
	Redacts func(ctx context.Context) bool
	// LookupBase is the address the client reached the gateway at, such as
	// http://127.0.0.1:18317, when the client runs on this machine: only
	// then can the model run a lookup. "" otherwise.
	LookupBase string
}

// Run is the plugins' part in one request.
type Run struct {
	host  *Host
	input Input
	began time.Time
	turn  turn
	// settings are the settings of every plugin when the request began.
	settings contract.PluginHostSettings
	// system and first fingerprint the request's system prompt and what its
	// history opens with, to tell a compacted history.
	system, first string
	// place is the workspace the request names.
	place workspace
	// workspaceKeys are the attributes whose plugins bind a new session to
	// the value its workspace had.
	workspaceKeys map[string]bool
	// known are the values of each attribute the session was offered or
	// other sessions are bound to, read once a marker needs them.
	known map[string][]string
	// values are the session's bindings; nil when they could not be read.
	values   map[string]storage.MetadataValueRecord
	metadata contract.RequestMetadata
	// bindsOnly is set when the turn's opening message only binds the
	// session: one marker line, or an answer naming an offered value. The
	// turn gets no turn block and nothing of it is captured.
	bindsOnly bool
	// shell is set when the request offers the model a shell to run
	// lookups with; stamp is what the request tells the session's lookup
	// credentials.
	shell bool
	stamp lookupStamp
	// turnKey names the request's turn: its index and its opening message.
	// The index alone is not enough: a session linked through an older
	// record can give two turns the same index.
	turnKey string
	parts   []*part
	// ready closes once the turn's text passed the privacy check and the
	// prepareTurn calls started.
	ready chan struct{}

	mutex    sync.Mutex
	reported bool
	records  []contract.RequestPluginRecord
	events   []contract.RequestEvent
	// observed are the records of captures that ended before the request
	// record was written; persisted is set once it is.
	observed  []contract.RequestPluginRecord
	persisted bool
}

// part is one plugin's part in a run.
type part struct {
	plugin *plugin.Plugin
	info   plugin.RequestInfo
	view   SessionView
	// off says why the plugin stays out of the request: every plugin
	// paused, its client excluded or the session paused. "" when it takes
	// part.
	off string
	// deadline is when the request stops waiting for the plugin.
	deadline    time.Time
	residentKey string
	metaKey     string
	// sessionCall is the prepareSession call the request waits for, nil
	// when the kept block serves, and trigger why it runs. A background
	// call, the automatic refresh, is not waited for.
	sessionCall *hookCall
	trigger     string
	background  bool
	// starts is set when the plugin takes part in the session for the
	// first time.
	starts bool
	// bound is set when the session has a value of each of the plugin's
	// attributes, none excluded; usage is the usage of its lookups the
	// system prompt carries, "" for none.
	bound bool
	usage string
	// settle is set on the call that settles the plugin's block for the
	// turn; turnCall is its prepareTurn call, skip why there is none.
	settle   bool
	turnCall *hookCall
	skip     string
	// observe is what became of the turn's capture; awaited is the view
	// a capture that waits for the reply runs with.
	observe string
	awaited *TurnView
}

// hookCall is a hook call running in the background. text is the block a
// prepareSession call rendered.
type hookCall struct {
	done   chan struct{}
	result hookResult
	info   plugin.CallInfo
	err    error
	text   string
}

// Begin starts the plugins' part in a request, or returns nil when no
// plugin takes part. ctx bounds the reads of the session's bindings; the
// hooks run apart from the request.
func (host *Host) Begin(ctx context.Context, input Input) *Run {
	if host == nil || host.plugins == nil || input.Session == "" {
		return nil
	}
	if _, ok := convoProtocols[input.Protocol]; !ok {
		return nil
	}
	var plugins []*plugin.Plugin
	for _, candidate := range host.plugins.Current().Plugins() {
		if candidate.Enabled() && takesPart(candidate) {
			plugins = append(plugins, candidate)
		}
	}
	if len(plugins) == 0 {
		return nil
	}
	run := &Run{
		host: host, input: input, began: time.Now(), settings: host.Settings(),
		turn: readTurn(input.Protocol, input.Body), ready: make(chan struct{}),
	}
	if opening, ok := host.ledger.Opening(input.Protocol, input.Body); ok && input.Turn > 0 {
		run.turnKey = strconv.Itoa(input.Turn) + ":" + opening
	}
	if run.settings.Paused {
		// Every plugin stays out and the request goes out as it came; the
		// turn's first call says why.
		close(run.ready)
		run.plan(plugins)
		return run
	}
	run.shell = input.LookupBase != "" && declaresShell(input.Protocol, input.Body)
	run.system = host.ledger.Fingerprint(systemText(input.Protocol, input.Body))
	if run.turn.first != "" {
		run.first = host.ledger.Fingerprint(run.turn.first)
	}
	run.locate()
	events := run.bind(ctx, plugins)
	if host.metadata != nil {
		run.metadata = host.metadata.Resolve(input.Header, input.Token, run.values)
	}
	run.metadata.Events = clampEvents(append(events, run.metadata.Events...))
	run.load(ctx, plugins)
	run.plan(plugins)
	run.start(ctx)
	// A session bound since its turns were held, in this request or in
	// between, has them captured now.
	for _, current := range run.parts {
		if current.off == "" && current.plugin.Has(plugin.HookObserveTurn) && current.plugin.Can(plugin.CapabilityRequest) {
			run.release(current, current.view.Meta)
		}
	}
	go run.work()
	return run
}

// takesPart reports whether a plugin has a part in requests: a hook that
// runs in them, or lookups whose usage goes with them.
func takesPart(candidate *plugin.Plugin) bool {
	return candidate.Has(plugin.HookPrepareSession) || candidate.Has(plugin.HookPrepareTurn) || candidate.Has(plugin.HookObserveTurn) ||
		(candidate.Has(plugin.HookCallTool) && len(candidate.Meta.Tools) > 0)
}

// Metadata is the request's metadata: its values, with the session's
// bindings, and what the turn's opening message changed.
func (run *Run) Metadata() contract.RequestMetadata {
	if run == nil {
		return contract.RequestMetadata{}
	}
	return run.metadata
}

// locate finds the workspace the request names, once for each system prompt
// and opening of the session: a session's requests repeat them.
func (run *Run) locate() {
	host := run.host
	key := run.system + "/" + run.first
	host.sessions.mutex.Lock()
	session := host.sessions.session(run.input.Session)
	if session.workspaceFor == key {
		run.place = session.workspace
		host.sessions.mutex.Unlock()
		return
	}
	host.sessions.mutex.Unlock()
	place := readWorkspace(host.workspaceKey, run.input.Protocol, run.input.Body, run.turn.messages)
	host.sessions.mutex.Lock()
	session = host.sessions.session(run.input.Session)
	session.workspace, session.workspaceFor = place, key
	host.sessions.mutex.Unlock()
	run.place = place
}

// load reads the plugins' part in the session from storage: the turn each
// first took part in and whether the user paused it. Each is read once while
// the host keeps the session.
func (run *Run) load(ctx context.Context, plugins []*plugin.Plugin) {
	host := run.host
	host.sessions.mutex.Lock()
	var missing []string
	for _, candidate := range plugins {
		state := host.sessions.plugin(run.input.Session, candidate.Key)
		if host.store == nil {
			state.loaded = true
		}
		if !state.loaded {
			missing = append(missing, candidate.Key)
		}
	}
	host.sessions.mutex.Unlock()
	if len(missing) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, bindingTimeout)
	defer cancel()
	for _, key := range missing {
		record, found, err := host.store.PluginSession(ctx, key, run.input.Session)
		if err != nil {
			host.logf("read the plugin's part in the session: %v", err)
			continue
		}
		host.sessions.mutex.Lock()
		state := host.sessions.plugin(run.input.Session, key)
		if !state.loaded {
			state.loaded = true
			if found {
				state.firstTurn, state.paused = record.FirstTurn, record.Paused
			}
		}
		host.sessions.mutex.Unlock()
	}
}

// start keeps the turn each plugin took part in the session for the first
// time. Storage keeps the first turn it was told; a different one corrects
// what the host assumed.
func (run *Run) start(ctx context.Context) {
	host := run.host
	if host.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindingTimeout)
	defer cancel()
	for _, current := range run.parts {
		if !current.starts {
			continue
		}
		record, err := host.store.StartPluginSession(ctx, current.plugin.Key, run.input.Session, run.input.Turn)
		if err != nil {
			host.logf("keep the plugin's part in the session: %v", err)
			continue
		}
		if record.FirstTurn != run.input.Turn {
			host.sessions.mutex.Lock()
			host.sessions.plugin(run.input.Session, current.plugin.Key).firstTurn = record.FirstTurn
			host.sessions.mutex.Unlock()
		}
	}
}

// plan settles what each plugin does in the request and starts the
// prepareSession calls, which read no user text.
func (run *Run) plan(plugins []*plugin.Plugin) {
	host := run.host
	meta := map[string]plugin.MetaValue{}
	for key, value := range run.metadata.Values {
		meta[key] = plugin.MetaValue{Value: value.Value, Source: value.Source}
	}
	now := host.now()
	host.sessions.mutex.Lock()
	defer host.sessions.mutex.Unlock()
	session := host.sessions.session(run.input.Session)
	if session.language == "" {
		session.language = run.settings.Language
	}
	compacted := !run.settings.Paused && session.compacted(run.system, run.first)
	run.stamp = lookupStamp{
		seen: now, request: run.input.RequestID, turn: run.input.Turn, turnKey: run.turnKey,
		scope:  LookupScope{Protocol: run.input.Protocol, Model: run.input.Model, Token: run.input.Token},
		client: run.input.Client, language: session.language, meta: meta,
	}
	defer host.lookups.touch(run.input.Session, run.stamp)
	for _, candidate := range plugins {
		read := candidate.Can(plugin.CapabilityRequest)
		view := SessionView{
			Protocol: string(run.input.Protocol), Model: run.input.Model, Client: run.input.Client,
			Meta: map[string]plugin.MetaValue{}, Session: plugin.SessionRef{ID: run.input.Session, Turn: run.input.Turn},
			Language: session.language,
		}
		if read {
			view.Meta = meta
			if run.place.known() {
				view.Workspace = &WorkspaceView{Name: run.place.name}
			}
		}
		state := host.sessions.plugin(run.input.Session, candidate.Key)
		current := &part{plugin: candidate, deadline: run.began.Add(host.turnWait(candidate))}
		current.settle = run.turn.opened && run.turnKey != "" && state.settled != run.turnKey
		switch {
		case run.settings.Paused:
			current.off = contract.PluginSkipPausedAll
		case candidate.Settings.Excludes(run.input.Client):
			current.off = contract.PluginSkipClientDisabled
		case state.paused:
			current.off = contract.PluginSkipSessionPaused
		}
		run.parts = append(run.parts, current)
		if current.off != "" {
			// The turn's first call says why the plugin stays out; its
			// other calls say nothing.
			current.view = view
			if current.settle {
				state.settled = run.turnKey
			}
			continue
		}
		if current.settle && state.firstTurn == 0 && run.input.Turn > 0 {
			state.firstTurn, current.starts = run.input.Turn, true
		}
		view.Session.First = state.firstTurn != 0 && state.firstTurn == run.input.Turn
		ref := view.Session
		current.view = view
		current.info = plugin.RequestInfo{
			Protocol: view.Protocol, Model: view.Model, Client: view.Client, Stream: run.input.Stream,
			Meta: view.Meta, Session: &ref,
		}
		current.metaKey = metaKey(view.Meta)
		current.residentKey = residentKey(candidate, view.Language, current.metaKey)
		current.bound = true
		for _, attribute := range candidate.Meta.Metadata {
			if value, found := run.metadata.Values[attribute.Key]; !found || value.Value == contract.MetadataNone {
				current.bound = false
			}
		}
		if current.bound && run.lookupOpen(current) {
			token := host.lookups.issue(lookupKey{run.input.Session, candidate.Key}, candidate, run.stamp)
			current.usage = lookupUsage(run.input.LookupBase, token, candidate, view.Language)
		}
		if candidate.Can(plugin.CapabilityInject) && candidate.Has(plugin.HookPrepareSession) {
			run.planResident(current, state, compacted, now)
		}
		if !current.settle || run.bindsOnly {
			continue
		}
		unbound := false
		for _, attribute := range candidate.Meta.Metadata {
			if _, found := run.metadata.Values[attribute.Key]; !found {
				unbound = true
			}
		}
		if candidate.Has(plugin.HookObserveTurn) && read {
			switch {
			case state.bindingTurn != "" && state.bindingTurn == run.turnKey:
				current.observe = observeSkippedBindingTurn
			case unbound:
				// The reply may bind the session: the capture waits for it
				// and runs with what it binds.
				current.observe = observeAwaiting
			default:
				current.observe = "pending"
			}
		}
	}
}

// planResident settles whether the request computes the plugin's resident
// block and why: the session's first request since Core started, a new
// binding, new settings, the user's refresh, a compacted history, or the
// automatic check for newer data. A block is computed once and kept, so its
// bytes, and the prompt cache, hold from one request to the next. The caller
// holds the sessions' mutex.
func (run *Run) planResident(current *part, state *pluginState, compacted bool, now time.Time) {
	trigger := ""
	switch {
	case state.pending != nil && state.pendingKey == current.residentKey:
		// An earlier request's call still computes the block this one
		// needs: the request waits for it as for its own.
		current.sessionCall, current.trigger = state.pending, state.pendingTrigger
		return
	case !state.computed && state.residentKey == "":
		trigger = contract.PluginTriggerStart
	case state.residentKey != current.residentKey:
		trigger = contract.PluginTriggerConfig
		if state.metaKey != current.metaKey {
			trigger = contract.PluginTriggerBound
		}
	case state.refresh != "":
		trigger = state.refresh
	case compacted && now.Sub(state.compactedAt) >= refreshInterval:
		trigger = contract.PluginTriggerCompact
	case current.plugin.Settings.AutoRefresh && current.settle && now.Sub(state.checkedAt) >= refreshInterval:
		trigger = contract.PluginTriggerAuto
	}
	if trigger == "" {
		return
	}
	state.checkedAt = now
	switch trigger {
	case contract.PluginTriggerCompact:
		state.compactedAt = now
	case contract.PluginTriggerManual:
		state.refresh = ""
	case contract.PluginTriggerAuto:
		current.background = true
	}
	current.sessionCall, current.trigger = run.startSession(current, trigger), trigger
	// A compacted history's block is kept only if it comes in time; any
	// other is kept when it comes, and later requests wait for it.
	if trigger != contract.PluginTriggerCompact {
		state.pending, state.pendingKey, state.pendingTrigger = current.sessionCall, current.residentKey, trigger
	}
}

// startSession calls prepareSession in the background. The block it computes
// for a compacted history goes in only if Inject finds it in time; any other
// serves the session from when it comes, and the automatic refresh's only
// when it changed.
func (run *Run) startSession(current *part, trigger string) *hookCall {
	call := &hookCall{done: make(chan struct{})}
	host, session := run.host, run.input.Session
	view := current.view
	view.Reason = trigger
	key, meta := current.residentKey, current.metaKey
	redacts := run.input.Redacts
	go func() {
		defer close(call.done)
		ctx, cancel := context.WithTimeout(context.Background(), sessionHookTimeout)
		defer cancel()
		if redacts != nil && !current.plugin.Settings.Trusted {
			view.Redacted = redacts(ctx)
		}
		raw, info, err := current.plugin.Call(ctx, plugin.HookPrepareSession, view, plugin.CallOptions{
			Request: &current.info, NetworkBudget: sessionHookTimeout,
		})
		call.info, call.err = info, err
		if err == nil {
			call.result = parseResult(raw, current.plugin, host.metadata)
			if !call.result.invalid {
				call.text, call.result.dropped = call.result.system.Render(injection.MaxBlockBytes)
			}
		}
		host.sessions.mutex.Lock()
		defer host.sessions.mutex.Unlock()
		state := host.sessions.plugin(session, current.plugin.Key)
		superseded := state.pending != nil && state.pending != call && state.pendingKey != key
		if state.pending == call {
			state.pending, state.pendingKey, state.pendingTrigger = nil, "", ""
		}
		// A block for what the session was before a newer call started, for
		// another binding, would only be computed over again.
		if err != nil || call.result.invalid || trigger == contract.PluginTriggerCompact || superseded {
			return
		}
		if trigger == contract.PluginTriggerAuto && (state.residentKey != key || state.resident == call.text) {
			return
		}
		state.keep(call, trigger, key, meta, view.Language, host.now())
	}()
	return call
}

// keep makes what call computed the session's resident block.
func (state *pluginState) keep(call *hookCall, trigger, key, meta, language string, at time.Time) {
	state.computed, state.residentKey, state.metaKey, state.resident = true, key, meta, call.text
	state.record = residentRecord{
		trigger: trigger, at: at, language: language,
		given: call.result.record, hasGiven: call.result.hasRecord, dropped: call.result.dropped,
		info: callSummary{elapsedMs: int(call.info.Elapsed.Milliseconds()), network: networkOf(call.info)},
	}
	state.fresh = true
}

// work checks the turn's text against the privacy policy, then starts the
// prepareTurn calls and queues the captures.
func (run *Run) work() {
	defer close(run.ready)
	needsText := false
	for _, current := range run.parts {
		if current.off == "" && current.settle && !run.bindsOnly && (run.prepares(current) || current.observe == "pending" || current.observe == observeAwaiting) {
			needsText = true
		}
	}
	if !needsText {
		return
	}
	checked, skip := run.inspect()
	run.mutex.Lock()
	defer run.mutex.Unlock()
	now := time.Now()
	for _, current := range run.parts {
		if current.off != "" || !current.settle || run.bindsOnly {
			continue
		}
		user := run.turn.user
		reason := ""
		if !current.plugin.Settings.Trusted {
			user, reason = checked, skip
		}
		turnView := TurnView{SessionView: current.view, Stream: run.input.Stream}
		if current.plugin.Can(plugin.CapabilityRequest) {
			turnView.User = user
		}
		if run.prepares(current) {
			switch {
			case reason != "":
				current.skip = reason
			case !now.Before(current.deadline):
				current.skip = contract.PluginSkipTimeout
			default:
				current.turnCall = run.startTurn(current, turnView)
			}
		}
		switch {
		case current.observe != "pending" && current.observe != observeAwaiting:
		case reason != "":
			current.observe = observeSkippedPrivacy
		case current.observe == observeAwaiting:
			current.awaited = &turnView
		default:
			current.observe = run.queue(current, turnView)
		}
	}
}

// queue queues the plugin's capture of the turn, after the turns held for
// it, and returns what became of it. The caller holds run.mutex.
func (run *Run) queue(current *part, view TurnView) string {
	run.release(current, view.Meta)
	job := observeJob{plugin: current.plugin, view: view, info: current.info, deliver: run.deliverObserve}
	if run.host.observe.push(job) {
		return observeQueued
	}
	return observeQueueFull
}

// deliverObserve keeps what a capture reported with its request: in the
// request record while it is still to be written, added to the written
// record otherwise.
func (run *Run) deliverObserve(record contract.RequestPluginRecord) {
	run.mutex.Lock()
	if !run.persisted {
		run.observed = append(run.observed, record)
		run.mutex.Unlock()
		return
	}
	run.mutex.Unlock()
	run.host.appendRecords(run.input.RequestID, []contract.RequestPluginRecord{record})
}

// Persisted tells the run that its request record is written: the records
// of captures that end later are added to it.
func (run *Run) Persisted() {
	if run == nil {
		return
	}
	run.mutex.Lock()
	run.persisted = true
	late := run.observed
	run.observed = nil
	run.mutex.Unlock()
	run.host.appendRecords(run.input.RequestID, late)
}

// inspect returns the turn's text as a plugin the user does not trust may
// read it, or why it may read none.
func (run *Run) inspect() (UserText, string) {
	user := run.turn.user
	if run.input.Inspect == nil {
		return user, ""
	}
	trusted := true
	for _, current := range run.parts {
		trusted = trusted && (current.off != "" || current.plugin.Settings.Trusted)
	}
	if trusted {
		return user, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), inspectTimeout)
	defer cancel()
	texts := []string{user.Text}
	if user.Previous != "" {
		texts = append(texts, user.Previous)
	}
	checked, err := run.input.Inspect(ctx, texts)
	if errors.Is(err, ErrBlocked) {
		return UserText{}, contract.PluginSkipPrivacyBlocked
	}
	if err != nil || len(checked) != len(texts) {
		return UserText{}, contract.PluginSkipPrivacyUnavailable
	}
	if checked[0] != user.Text {
		user.Text, user.Typed = checked[0], typedOf(run.input.Protocol, checked[0])
	}
	if len(checked) > 1 {
		user.Previous = checked[1]
	}
	return user, ""
}

func (run *Run) startTurn(current *part, view TurnView) *hookCall {
	call := &hookCall{done: make(chan struct{})}
	go func() {
		defer close(call.done)
		ctx, cancel := context.WithDeadline(context.Background(), current.deadline.Add(turnHookSlack))
		defer cancel()
		raw, info, err := current.plugin.Call(ctx, plugin.HookPrepareTurn, view, plugin.CallOptions{
			Request: &current.info, NetworkBudget: max(time.Until(current.deadline)-turnNetworkMargin, 0),
		})
		call.info, call.err = info, err
		if err == nil {
			call.result = parseResult(raw, current.plugin, run.host.metadata)
		}
	}()
	return call
}

// Inject puts the plugins' blocks into body, the request's body in the
// client's protocol, after waiting for the plugins until the turn's budget
// runs out. Together the blocks stay within the injection budget: past it,
// the blocks of the plugins later in key order stay out. It returns the body
// to send: body itself when nothing goes in or the injection fails.
func (run *Run) Inject(body []byte) []byte {
	if run == nil {
		return body
	}
	run.wait()
	run.mutex.Lock()
	defer run.mutex.Unlock()
	host := run.host
	request := injection.Request{Protocol: run.input.Protocol, Session: run.input.Session, Body: body}
	results := make([]injected, len(run.parts))
	var entries []int
	used := 0
	now := host.now()
	host.sessions.mutex.Lock()
	for index, current := range run.parts {
		if current.off != "" {
			continue
		}
		state := host.sessions.plugin(run.input.Session, current.plugin.Key)
		result := &results[index]
		if current.trigger == contract.PluginTriggerCompact {
			// The block of a compacted history goes in now or never: the
			// next request finds the cache written with the kept one.
			if called, ok := current.sessionCall.finished(); ok && !called.invalid {
				state.keep(current.sessionCall, current.trigger, current.residentKey, current.metaKey, current.view.Language, now)
			}
		}
		entry := injection.Plugin{Key: current.plugin.Key}
		if current.plugin.Can(plugin.CapabilityInject) && state.computed {
			entry.Resident = state.resident
			result.record, result.kept = state.record, true
			result.fresh, state.fresh = state.fresh, false
		}
		// The usage of the plugin's lookups follows its block, in a block
		// of the gateway's own.
		if current.usage != "" {
			entry.Resident = strings.TrimPrefix(entry.Resident+"\n\n"+current.usage, "\n\n")
		}
		state.usage = current.usage
		result.turnTitle = state.turnTitle
		turnBytes := state.turnBytes
		var choices map[string][]string
		if current.settle {
			entry.Settled = true
			result.turnTitle, turnBytes = "", 0
			if called, ok := current.turnCall.finished(); ok && !called.invalid {
				block := called.context
				block.Items = withCommands(block.Items, func(name string) (string, bool) {
					line, ok := run.lookupCommand(current, name)
					if ok && !slices.Contains(result.commands, name) {
						result.commands = append(result.commands, name)
					}
					return line, ok
				})
				entry.Turn, result.dropped = block.Render(injection.MaxBlockBytes)
				result.turnTitle, turnBytes, choices = called.record.Title, len(entry.Turn), called.choices
			}
		}
		if size := len(entry.Resident) + turnBytes; size > 0 && used+size > run.settings.InjectionBudgetBytes {
			entry.Resident, entry.Turn, turnBytes, choices = "", "", 0, nil
			result.overBudget = true
		} else {
			used += size
		}
		if current.settle {
			state.settled, state.turnTitle, state.turnBytes = run.turnKey, result.turnTitle, turnBytes
			if entry.Turn != "" && len(choices) > 0 {
				state.choices = choices
			}
		}
		request.Plugins = append(request.Plugins, entry)
		entries = append(entries, index)
	}
	host.sessions.mutex.Unlock()
	applied, err := host.ledger.Apply(request)
	if err != nil {
		host.logf("plugin injection failed, the request goes out as it came: %v", err)
		run.report(results, true)
		return body
	}
	for at, index := range entries {
		results[index].outcome = applied.Outcomes[at]
	}
	run.report(results, false)
	return applied.Body
}

// wait waits for each plugin until its wait runs out. The automatic refresh
// runs on without the request.
func (run *Run) wait() {
	last := run.began
	for _, current := range run.parts {
		last = later(last, current.deadline)
	}
	if !waitUntil(run.ready, last) {
		return
	}
	for _, current := range run.parts {
		if current.off != "" {
			continue
		}
		if current.sessionCall != nil && !current.background {
			waitUntil(current.sessionCall.done, current.deadline)
		}
		if current.turnCall != nil {
			waitUntil(current.turnCall.done, current.deadline)
		}
	}
}

// waitUntil waits for done until deadline and reports whether it came.
func waitUntil(done <-chan struct{}, deadline time.Time) bool {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// finished returns the call's result when it came back without error.
func (call *hookCall) finished() (hookResult, bool) {
	if call == nil {
		return hookResult{}, false
	}
	select {
	case <-call.done:
		return call.result, call.err == nil
	default:
		return hookResult{}, false
	}
}

// Report returns the plugins' records and their events for the request
// record. The first call after Inject returns what Inject did; without
// Inject, what the plugins did so far. Later calls return nothing.
func (run *Run) Report() ([]contract.RequestPluginRecord, []contract.RequestEvent) {
	if run == nil {
		return nil, nil
	}
	run.mutex.Lock()
	defer run.mutex.Unlock()
	if !run.reported {
		run.report(make([]injected, len(run.parts)), false)
	}
	records, events := append(run.records, run.observed...), run.events
	run.records, run.events, run.observed = nil, nil, nil
	return records, events
}

// metaKey names the metadata a resident block is computed from.
func metaKey(meta map[string]plugin.MetaValue) string {
	hash := sha256.New()
	keys := make([]string, 0, len(meta))
	for key := range meta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		hash.Write([]byte(key + "=" + meta[key].Value + "\x01" + meta[key].Source + "\x00"))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// residentKey names what a resident block is computed from: the plugin's
// version and configuration, the session's language, and the request's
// metadata.
func residentKey(candidate *plugin.Plugin, language, meta string) string {
	hash := sha256.New()
	hash.Write([]byte(candidate.Meta.Version + "\x00" + language + "\x00" + meta + "\x00"))
	hash.Write(candidate.Settings.Config)
	return hex.EncodeToString(hash.Sum(nil))
}

func networkOf(info plugin.CallInfo) contract.PluginNetworkCount {
	return contract.PluginNetworkCount{
		Requests: info.Network.Requests, Denied: info.Network.Denied,
		Errors: info.Network.Errors, WaitMs: int(info.Network.Wait.Milliseconds()),
	}
}

func clampEvents(events []contract.MetadataEvent) []contract.MetadataEvent {
	if len(events) > contract.MaxRequestMetadataEvents {
		events = events[:contract.MaxRequestMetadataEvents]
	}
	return slices.Clip(events)
}

// lookupOpen reports whether the model may run the plugin's lookups in this
// request: the user turned them on and granted them, and the client, on
// this machine, offers the model a shell.
func (run *Run) lookupOpen(current *part) bool {
	candidate := current.plugin
	return run.shell && current.off == "" && candidate.Can(plugin.CapabilityTools) &&
		candidate.Settings.LookupEnabled && candidate.Has(plugin.HookCallTool) && len(candidate.Meta.Tools) > 0
}

// lookupCommand renders the command of one of the plugin's queries for its
// blocks, false when the session cannot run it.
func (run *Run) lookupCommand(current *part, name string) (string, bool) {
	tool, declared := current.plugin.Meta.Tool(name)
	if !declared || !run.lookupOpen(current) || !current.plugin.Offers(tool) || (!tool.Unbound && !current.bound) {
		return "", false
	}
	token := run.host.lookups.issue(lookupKey{run.input.Session, current.plugin.Key}, current.plugin, run.stamp)
	return lookupCommand(run.input.LookupBase, token, current.plugin.Key, tool, current.view.Language), true
}
