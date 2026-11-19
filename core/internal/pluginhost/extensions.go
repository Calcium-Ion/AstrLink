package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// extensionHooks are the hooks of the extension points that run for a
// request. A plugin that exports none of them stays out of Extend.
var extensionHooks = []string{
	plugin.HookOnRequest,
	plugin.HookRoute,
	plugin.HookBeforeForward,
	plugin.HookBeforeUpstream,
	plugin.HookRewriteResponse,
	plugin.HookOnStreamEvent,
	plugin.HookOnStreamEnd,
	plugin.HookOnResponse,
	plugin.HookOnEvent,
}

// ExtensionInput is what the extension points of a request see of it.
type ExtensionInput struct {
	RequestID contract.RequestID
	Protocol  contract.ProtocolID
	// Model is the model the client asked for.
	Model  string
	Client string
	Stream bool
	// TokenName is the name of the access token the request came with, ""
	// for none.
	TokenName string
	Session   string
	Turn      int
	Meta      map[string]plugin.MetaValue
	BodyBytes int
	// View returns a body, in protocol, as a plugin the user does not
	// trust may see it: as the privacy check of an upstream nobody trusts
	// leaves it. nil when no privacy policy can apply.
	View func(ctx context.Context, protocol contract.ProtocolID, body []byte) (BodyView, error)
}

// BodyView is a body as a plugin may see it. Restore puts back what the
// privacy check replaced, in a body the plugin made from it; nil when
// nothing was replaced. Blocked is set when the policy blocks the body: no
// plugin the user does not trust sees it.
type BodyView struct {
	Body    []byte
	Restore func([]byte) []byte
	Blocked bool
}

// Extensions runs the extension points of apiVersion 2 plugins for one
// request. The plugins of a point run one after the other in the user's
// order, each with what the one before returned; each call has its own time
// limit. Whatever goes wrong costs the request that plugin's part, unless
// the plugin fails closed at the entry, and the request record says why.
type Extensions struct {
	host    *Host
	input   ExtensionInput
	plugins []*plugin.Plugin
	model   string

	mutex   sync.Mutex
	tags    []string
	records []contract.RequestPluginRecord
	// views keeps a body's view for plugins the user does not trust, by
	// the body it was made of.
	views map[string]BodyView
	// persisted is set once the request record is written.
	persisted bool
}

// Extend starts the extension points of a request, or returns nil when no
// enabled plugin serves one.
func (host *Host) Extend(input ExtensionInput) *Extensions {
	if host == nil || host.plugins == nil {
		return nil
	}
	settings := host.Settings()
	if settings.Paused {
		return nil
	}
	var plugins []*plugin.Plugin
	for _, candidate := range host.plugins.Current().Plugins() {
		if !candidate.Enabled() || candidate.Settings.Excludes(input.Client) {
			continue
		}
		if slices.ContainsFunc(extensionHooks, candidate.Has) {
			plugins = append(plugins, candidate)
		}
	}
	if len(plugins) == 0 {
		return nil
	}
	return &Extensions{host: host, input: input, plugins: ordered(plugins, settings.Order), model: input.Model, views: map[string]BodyView{}}
}

// ordered puts plugins in the user's order: those order lists first, in
// its order, then the rest in key order.
func ordered(plugins []*plugin.Plugin, order []string) []*plugin.Plugin {
	sorted := slices.Clone(plugins)
	rank := func(key string) int {
		if index := slices.Index(order, key); index >= 0 {
			return index
		}
		return len(order)
	}
	slices.SortStableFunc(sorted, func(a, b *plugin.Plugin) int {
		if ra, rb := rank(a.Key), rank(b.Key); ra != rb {
			return ra - rb
		}
		return strings.Compare(a.Key, b.Key)
	})
	return sorted
}

// Ordered returns the enabled plugins in the user's order.
func (host *Host) Ordered() []*plugin.Plugin {
	return ordered(host.plugins.Current().Plugins(), host.Settings().Order)
}

// serving returns the plugins that export hook and hold capability, ""
// for none, in order.
func (ext *Extensions) serving(hook string, capabilities ...string) []*plugin.Plugin {
	if ext == nil {
		return nil
	}
	var plugins []*plugin.Plugin
	for _, candidate := range ext.plugins {
		if !candidate.Has(hook) {
			continue
		}
		if len(capabilities) > 0 && !slices.ContainsFunc(capabilities, candidate.Can) {
			continue
		}
		plugins = append(plugins, candidate)
	}
	return plugins
}

// Serves reports whether any plugin of the request serves hook.
func (ext *Extensions) Serves(hook string, capabilities ...string) bool {
	return len(ext.serving(hook, capabilities...)) > 0
}

// errHookLimit ends a call that ran past its extension point's limit.
var errHookLimit = errors.New("the extension point's time limit passed")

// call runs one hook of target within its limit. skip is why it did not
// run: the breaker of the plugin's hook is open.
func (ext *Extensions) call(ctx context.Context, target *plugin.Plugin, hook string, args ...any) (result json.RawMessage, info plugin.CallInfo, skip string, err error) {
	limited := limitHook(hook)
	breaker := ext.host.breaker(target.Key, limited)
	if !breaker.allow(time.Now()) {
		return nil, plugin.CallInfo{}, "circuit_open", nil
	}
	limit := target.Limit(limited)
	ctx, cancel := context.WithTimeoutCause(ctx, limit, errHookLimit)
	defer cancel()
	options := plugin.CallOptions{Request: ext.requestInfo(target)}
	if target.Can(plugin.CapabilityNetwork) {
		options.NetworkBudget = limit
	}
	result, info, err = target.CallArgs(ctx, hook, args, options)
	breaker.record(err != nil, time.Now())
	return result, info, "", err
}

// limitHook is the hook whose time limit a call of hook keeps to.
func limitHook(hook string) string {
	if hook == plugin.HookOnStreamEnd {
		return plugin.HookOnStreamEvent
	}
	return hook
}

// requestInfo is what astrlink.request.info returns in the request's
// extension points, for a plugin granted request.read.
func (ext *Extensions) requestInfo(target *plugin.Plugin) *plugin.RequestInfo {
	if !target.Can(plugin.CapabilityRequest) {
		return nil
	}
	info := &plugin.RequestInfo{Protocol: string(ext.input.Protocol), Model: ext.model, Client: ext.input.Client, Stream: ext.input.Stream, Meta: ext.input.Meta}
	if info.Meta == nil {
		info.Meta = map[string]plugin.MetaValue{}
	}
	if ext.input.Session != "" {
		info.Session = &plugin.SessionRef{ID: ext.input.Session, Turn: ext.input.Turn}
	}
	return info
}

// requestContext is the request part of what every extension point hands
// a plugin.
func (ext *Extensions) requestContext() map[string]any {
	ext.mutex.Lock()
	tags := slices.Clone(ext.tags)
	ext.mutex.Unlock()
	if tags == nil {
		tags = []string{}
	}
	meta := ext.input.Meta
	if meta == nil {
		meta = map[string]plugin.MetaValue{}
	}
	request := map[string]any{
		"id": string(ext.input.RequestID), "protocol": string(ext.input.Protocol), "model": ext.model,
		"client": ext.input.Client, "stream": ext.input.Stream, "meta": meta, "tags": tags,
		"language": ext.host.Settings().Language, "bytes": ext.input.BodyBytes,
	}
	if ext.input.TokenName != "" {
		request["token"] = ext.input.TokenName
	}
	if ext.input.Session != "" {
		request["session"] = map[string]any{"id": ext.input.Session, "turn": ext.input.Turn}
	}
	return request
}

// view returns body as target may see it: as it is for a plugin the user
// trusts, else as the privacy check leaves it. ok is false when the plugin
// may not see it at all.
func (ext *Extensions) view(ctx context.Context, target *plugin.Plugin, protocol contract.ProtocolID, body []byte) (BodyView, bool) {
	if target.Settings.Trusted || ext.input.View == nil {
		return BodyView{Body: body}, true
	}
	key := string(protocol) + "\x00" + string(body)
	ext.mutex.Lock()
	view, found := ext.views[key]
	ext.mutex.Unlock()
	if !found {
		var err error
		view, err = ext.input.View(ctx, protocol, body)
		if err != nil {
			return BodyView{}, false
		}
		ext.mutex.Lock()
		ext.views[key] = view
		ext.mutex.Unlock()
	}
	if view.Blocked {
		return BodyView{}, false
	}
	return view, true
}

// record is one call's line in the request record.
type record struct {
	status  contract.PluginRecordStatus
	summary string
	skipped string
	effects []string
	tags    []string
	events  int
	failed  int
}

// note keeps a call's record with the request.
func (ext *Extensions) note(target *plugin.Plugin, hook string, info plugin.CallInfo, line record) {
	entry := contract.RequestPluginRecord{
		Plugin: target.Key, PluginVersion: target.Meta.Version, Hook: hook,
		Title: clip(target.Meta.Name, contract.MaxPluginRecordTitleRunes), Status: line.status,
		Summary: clip(line.summary, contract.MaxPluginRecordSummaryRunes), SkippedReason: line.skipped,
		ElapsedMs: int(info.Elapsed.Milliseconds()), Network: networkCount(info.Network),
		Effects: line.effects, Tags: line.tags, Events: line.events, Failures: line.failed,
	}
	ext.mutex.Lock()
	defer ext.mutex.Unlock()
	if len(ext.records) < contract.MaxRequestPluginRecords {
		ext.records = append(ext.records, entry)
	}
}

// failure is the record of a call that failed.
func failure(err error) record {
	line := record{status: contract.PluginRecordFailed, summary: err.Error()}
	var callErr *plugin.CallError
	if errors.As(err, &callErr) {
		line.skipped = callErr.Reason
		line.summary = callErr.Message
	}
	return line
}

// skipped is the record of a call that did not run.
func skipped(reason string) record {
	return record{status: contract.PluginRecordSkipped, skipped: reason}
}

// Records returns the records of the request's extension points so far,
// and forgets them, so each is kept once.
func (ext *Extensions) Records() []contract.RequestPluginRecord {
	if ext == nil {
		return nil
	}
	ext.mutex.Lock()
	defer ext.mutex.Unlock()
	records := ext.records
	ext.records = nil
	return records
}

// Tags returns the tags onRequest gave the request.
func (ext *Extensions) Tags() []string {
	if ext == nil {
		return nil
	}
	ext.mutex.Lock()
	defer ext.mutex.Unlock()
	return slices.Clone(ext.tags)
}

func networkCount(stats plugin.NetworkStats) contract.PluginNetworkCount {
	return contract.PluginNetworkCount{Requests: stats.Requests, Denied: stats.Denied, Errors: stats.Errors, WaitMs: int(stats.Wait.Milliseconds())}
}

// clip cuts text to max runes, without control characters.
func clip(text string, max int) string {
	text = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(text, "�"))
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	runes := []rune(text)
	return string(runes[:max-1]) + "…"
}

// decode reads a hook's result strictly into into. A result of undefined
// leaves into as it is.
func decode(result json.RawMessage, into any) error {
	if len(result) == 0 || string(result) == "null" {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(result)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(into)
}

// breaker keeps a plugin's hook out of requests for a while after it failed
// too often: breakerFailures failures within breakerWindow open it for
// breakerOpen.
type breaker struct {
	mutex    sync.Mutex
	failures []time.Time
	until    time.Time
}

const (
	breakerFailures = 5
	breakerWindow   = time.Minute
	breakerOpen     = time.Minute
)

func (host *Host) breaker(plugin, hook string) *breaker {
	key := plugin + "/" + hook
	if found, ok := host.breakers.Load(key); ok {
		return found.(*breaker)
	}
	created, _ := host.breakers.LoadOrStore(key, &breaker{})
	return created.(*breaker)
}

func (b *breaker) allow(now time.Time) bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return !now.Before(b.until)
}

func (b *breaker) record(failed bool, now time.Time) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if !failed {
		b.failures = b.failures[:0]
		return
	}
	kept := b.failures[:0]
	for _, at := range b.failures {
		if now.Sub(at) < breakerWindow {
			kept = append(kept, at)
		}
	}
	b.failures = append(kept, now)
	if len(b.failures) >= breakerFailures {
		b.until = now.Add(breakerOpen)
		b.failures = b.failures[:0]
	}
}

// Breakers returns the hooks of plugin kept out of requests for now, and
// until when.
func (host *Host) Breakers(plugin string) map[string]time.Time {
	open := map[string]time.Time{}
	now := time.Now()
	host.breakers.Range(func(key, value any) bool {
		owner, hook, _ := strings.Cut(key.(string), "/")
		if owner != plugin {
			return true
		}
		b := value.(*breaker)
		b.mutex.Lock()
		if now.Before(b.until) {
			open[hook] = b.until
		}
		b.mutex.Unlock()
		return true
	})
	return open
}
