package claudeofficial

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/claudecli"
)

const (
	defaultMaxProcesses = 6
	defaultIdleTimeout  = 5 * time.Minute
	defaultToolTimeout  = 30 * time.Minute
	defaultResumeWindow = 30 * time.Minute
	maxRequestBody      = 64 << 20
	gracefulStop        = 5 * time.Second
)

// Options configures a Runtime.
type Options struct {
	Accounts claudecli.Accounts
	// MaxProcesses caps live CLI processes across all accounts.
	MaxProcesses int
	// IdleTimeout stops a process that has answered and heard nothing since.
	IdleTimeout time.Duration
	// ToolTimeout stops a process whose tool calls the caller never answers.
	ToolTimeout time.Duration
	// ResumeWindow keeps a stopped conversation's transcript so it resumes
	// without replaying its history.
	ResumeWindow time.Duration
	// SignedOut is called when an account's CLI turns out to be signed out.
	SignedOut func(contract.ServiceID)
	// Usage receives the response headers of an account's model calls,
	// which report its plan usage.
	Usage func(context.Context, contract.ServiceID, http.Header)
	Now   func() time.Time
}

type state int

const (
	stateBusy state = iota
	stateParked
	stateIdle
	// stateDormant has no process; its transcript is kept for resume.
	stateDormant
)

// conversation is one caller conversation served by one account's CLI.
type conversation struct {
	service    contract.ServiceID
	configDir  string
	configKey  string
	sessionKey string
	session    string
	process    *process
	state      state
	// seen are the hashes of the history the CLI holds, ending with its own
	// reply; replyTools are the tool calls in that reply.
	seen       []string
	replyTools []string
	pending    []string
	call       Call
	used       time.Time
	timer      *time.Timer
	generation int
}

// Runtime serves official Claude accounts through their CLI processes. It is
// the RoundTripper for those accounts' outer requests.
type Runtime struct {
	options Options

	mu            sync.Mutex
	baseURL       string
	conversations map[*conversation]struct{}
	secrets       map[[32]byte]*conversation
	starting      int
	// cleaned lists the config directories whose leftover transcripts this
	// run has removed.
	cleaned map[string]bool
	closed  bool
}

// New returns a runtime. SetBaseURL must be called before it serves.
func New(options Options) *Runtime {
	if options.MaxProcesses <= 0 {
		options.MaxProcesses = defaultMaxProcesses
	}
	if options.IdleTimeout <= 0 {
		options.IdleTimeout = defaultIdleTimeout
	}
	if options.ToolTimeout <= 0 {
		options.ToolTimeout = defaultToolTimeout
	}
	if options.ResumeWindow <= 0 {
		options.ResumeWindow = defaultResumeWindow
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Runtime{
		options: options, conversations: map[*conversation]struct{}{},
		secrets: map[[32]byte]*conversation{}, cleaned: map[string]bool{},
	}
}

// SetBaseURL sets the loopback address the CLI's model calls go to.
func (runtime *Runtime) SetBaseURL(baseURL string) {
	runtime.mu.Lock()
	runtime.baseURL = strings.TrimRight(baseURL, "/")
	runtime.mu.Unlock()
}

// Loopback returns the outer call a process secret belongs to.
func (runtime *Runtime) Loopback(secret string) (Call, bool) {
	if secret == "" {
		return Call{}, false
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	current := runtime.secrets[sha256.Sum256([]byte(secret))]
	if current == nil {
		return Call{}, false
	}
	return current.call, true
}

// ObserveUsage passes the headers of an answer to one of account id's model
// calls on to Options.Usage.
func (runtime *Runtime) ObserveUsage(ctx context.Context, id contract.ServiceID, header http.Header) {
	if runtime.options.Usage != nil {
		runtime.options.Usage(ctx, id, header)
	}
}

// Models lists the models account id's CLI offers, as its SDK initialize
// reply reports them. The CLI sends no model call for that; a stray one would
// carry a secret no process holds, so the loopback refuses it.
func (runtime *Runtime) Models(ctx context.Context, id contract.ServiceID) ([]string, error) {
	accounts := runtime.options.Accounts
	binary, err := accounts.Locator.Find(ctx)
	if err != nil {
		return nil, err
	}
	configDir, err := accounts.Layout.ConfigDir(id)
	if err == nil {
		_, err = os.Stat(configDir)
	}
	if err != nil {
		return nil, runtime.modelsSignedOut(id)
	}
	workDir, err := accounts.Layout.EnsureWorkDir()
	if err != nil {
		return nil, err
	}
	runtime.mu.Lock()
	baseURL, closed := runtime.baseURL, runtime.closed
	runtime.mu.Unlock()
	if closed || baseURL == "" {
		return nil, errors.New("Claude Code serving is not ready")
	}
	session, err := newSessionID()
	if err != nil {
		return nil, err
	}
	environ := accounts.Environ
	if environ == nil {
		environ = os.Environ()
	}
	tunnel, err := accounts.Tunnel(id)
	if err != nil {
		return nil, err
	}
	target, err := startProcess(ctx, spawnSpec{
		Binary: binary, ConfigDir: configDir, WorkDir: workDir, Home: accounts.Layout.Home(), Environ: environ,
		BaseURL: baseURL, Tunnel: tunnel, Session: session,
	}, nil)
	if errors.Is(err, ErrSignedOut) {
		return nil, runtime.modelsSignedOut(id)
	}
	if err != nil {
		return nil, err
	}
	target.stopGracefully()
	<-target.exited
	return target.models, nil
}

func (runtime *Runtime) modelsSignedOut(id contract.ServiceID) error {
	if runtime.options.SignedOut != nil {
		runtime.options.SignedOut(id)
	}
	return ErrSignedOut
}

type plan int

const (
	planFresh plan = iota
	planContinue
	planResume
	planTools
)

// RoundTrip serves one Claude Messages request through the account's CLI.
func (runtime *Runtime) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		defer request.Body.Close()
	}
	call, ok := CallFrom(request.Context())
	if !ok || !Official(call.Service) {
		return nil, errors.New("claude official runtime: request has no official account")
	}
	if !strings.HasSuffix(strings.TrimRight(request.URL.Path, "/"), "/v1/messages") {
		return newFailure(http.StatusNotFound, "Claude Code only serves the Messages API").response(request), nil
	}
	if request.Body == nil {
		return newFailure(http.StatusBadRequest, "request body is required").response(request), nil
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxRequestBody+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxRequestBody {
		return newFailure(http.StatusRequestEntityTooLarge, "request body is too large").response(request), nil
	}
	parsed, err := parseRequest(raw)
	if err != nil {
		return newFailure(http.StatusBadRequest, err.Error()).response(request), nil
	}
	return runtime.serve(request, call, parsed)
}

func (runtime *Runtime) serve(request *http.Request, call Call, parsed request) (*http.Response, error) {
	ctx := request.Context()
	accounts := runtime.options.Accounts
	binary, err := accounts.Locator.Find(ctx)
	if err != nil {
		message := "Claude Code is not installed"
		if errors.Is(err, claudecli.ErrTooOld) {
			message = "Claude Code is older than " + claudecli.MinimumVersion + "; update it"
		}
		return newFailure(http.StatusServiceUnavailable, message).response(request), nil
	}
	configDir, err := accounts.Layout.ConfigDir(call.Service.ID)
	if err == nil {
		_, err = os.Stat(configDir)
	}
	if err != nil {
		return runtime.signedOut(request, call.Service.ID), nil
	}
	groups := groupMessages(parsed.Messages)
	hashes := hashGroups(groups)
	last := groups[len(groups)-1]

	runtime.mu.Lock()
	baseURL := runtime.baseURL
	if runtime.closed || baseURL == "" {
		runtime.mu.Unlock()
		return newFailure(http.StatusServiceUnavailable, "Claude Code serving is not ready").response(request), nil
	}
	current, chosen := runtime.match(call, parsed.configKey(), groups, hashes)
	var target *process
	if current != nil {
		runtime.claim(current, call)
		target = current.process
	}
	if chosen == planFresh || chosen == planResume {
		if !runtime.reserve() {
			if current != nil {
				runtime.release(current)
			}
			runtime.mu.Unlock()
			return busyFailure().response(request), nil
		}
	}
	runtime.mu.Unlock()

	names := toolUseNames(groups)
	var send func(*process) error
	switch chosen {
	case planTools:
		results, extra := splitResults(last.Blocks, current.pending)
		send = func(target *process) error { return target.answerTools(ctx, results, extra) }
	case planContinue, planResume:
		input := userInput{History: groups[len(current.seen) : len(groups)-1], Blocks: last.Blocks}
		content := input.content(names)
		send = func(target *process) error { return target.sendUser(content) }
	}
	if chosen == planResume {
		started, err := runtime.start(ctx, call.Service.ID, binary, configDir, baseURL, parsed, current.session, true)
		if err == nil {
			target = started
			runtime.attachProcess(current, target)
		} else {
			// The transcript is gone or unreadable: start over with the full
			// history.
			runtime.mu.Lock()
			runtime.drop(current)
			reserved := runtime.reserve()
			runtime.mu.Unlock()
			if errors.Is(err, ErrSignedOut) || !reserved {
				if reserved {
					runtime.unreserve()
				}
				if errors.Is(err, ErrSignedOut) {
					return runtime.signedOut(request, call.Service.ID), nil
				}
				return busyFailure().response(request), nil
			}
			current, chosen = nil, planFresh
		}
	}
	if chosen == planFresh {
		session, err := newSessionID()
		if err != nil {
			runtime.unreserve()
			return nil, err
		}
		input := userInput{History: groups[:len(groups)-1], Blocks: last.Blocks}
		content := input.content(names)
		send = func(target *process) error { return target.sendUser(content) }
		started, err := runtime.start(ctx, call.Service.ID, binary, configDir, baseURL, parsed, session, false)
		if err != nil {
			go removeTranscript(configDir, session)
			if errors.Is(err, ErrSignedOut) {
				return runtime.signedOut(request, call.Service.ID), nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return newFailure(http.StatusBadGateway, "Claude Code could not start: "+err.Error()).response(request), nil
		}
		current = &conversation{
			service: call.Service.ID, configDir: configDir, configKey: parsed.configKey(), sessionKey: call.SessionKey,
			session: session, state: stateBusy, call: call, used: runtime.options.Now(),
		}
		runtime.mu.Lock()
		runtime.conversations[current] = struct{}{}
		runtime.mu.Unlock()
		target = started
		runtime.attachProcess(current, target)
	}

	active := newTurn(parsed, request, runtime.options.Now, func(result turnResult) {
		runtime.turnDone(current, target, hashes, result)
	})
	if !target.attach(active, call) {
		active.fail(newFailure(http.StatusBadGateway, target.failure(errExited).Error()))
	} else if err := send(target); err != nil {
		active.fail(newFailure(http.StatusBadGateway, "Claude Code did not take the request: "+err.Error()))
	}
	context.AfterFunc(ctx, active.abort)
	select {
	case response := <-active.head:
		return response, nil
	case <-ctx.Done():
		active.abort()
		return nil, ctx.Err()
	}
}

func busyFailure() httpFailure {
	failure := newFailure(http.StatusTooManyRequests, "too many Claude Code conversations are in progress")
	failure.retryAfter = 5 * time.Second
	return failure
}

func (runtime *Runtime) signedOut(request *http.Request, id contract.ServiceID) *http.Response {
	if runtime.options.SignedOut != nil {
		runtime.options.SignedOut(id)
	}
	return newFailure(http.StatusUnauthorized, "Claude Code is signed out of this account; sign in again").response(request)
}

// match finds the conversation the caller's history continues. Callers hold
// runtime.mu.
func (runtime *Runtime) match(call Call, configKey string, groups []group, hashes []string) (*conversation, plan) {
	var best *conversation
	bestPlan := planFresh
	for current := range runtime.conversations {
		if current.service != call.Service.ID || current.configKey != configKey || current.state == stateBusy {
			continue
		}
		if current.sessionKey != "" && call.SessionKey != "" && current.sessionKey != call.SessionKey {
			continue
		}
		seen := len(current.seen)
		if seen == 0 || seen >= len(groups) || !current.continuedBy(groups, hashes) {
			continue
		}
		candidate := planContinue
		switch current.state {
		case stateParked:
			if seen != len(groups)-1 || !answers(groups[len(groups)-1], current.pending) {
				continue
			}
			candidate = planTools
		case stateDormant:
			candidate = planResume
		}
		if best == nil || seen > len(best.seen) || (seen == len(best.seen) && current.used.After(best.used)) {
			best, bestPlan = current, candidate
		}
	}
	return best, bestPlan
}

// continuedBy reports whether the caller's history starts with what the CLI
// holds. The CLI's own reply also matches by its tool call ids: converters
// may rewrite how a tool call's input is spelled.
func (current *conversation) continuedBy(groups []group, hashes []string) bool {
	last := len(current.seen) - 1
	for index := 0; index < last; index++ {
		if hashes[index] != current.seen[index] {
			return false
		}
	}
	if hashes[last] == current.seen[last] {
		return true
	}
	return groups[last].Role == "assistant" && len(current.replyTools) > 0 &&
		slices.Equal(toolUseIDs(groups[last]), current.replyTools)
}

func toolUseIDs(item group) []string {
	var ids []string
	for _, content := range item.Blocks {
		if content["type"] == "tool_use" {
			id, _ := content["id"].(string)
			ids = append(ids, id)
		}
	}
	return ids
}

// answers reports whether a user turn holds a result for every pending call.
func answers(item group, pending []string) bool {
	if item.Role != "user" || len(pending) == 0 {
		return false
	}
	results := map[string]bool{}
	for _, content := range item.Blocks {
		if content["type"] == "tool_result" {
			id, _ := content["tool_use_id"].(string)
			results[id] = true
		}
	}
	for _, id := range pending {
		if !results[id] {
			return false
		}
	}
	return true
}

// splitResults orders the results of the pending calls; the rest of the
// user turn rides along with the last result.
func splitResults(blocks []block, pending []string) ([]block, []block) {
	byID := map[string]block{}
	var extra []block
	for _, content := range blocks {
		id, _ := content["tool_use_id"].(string)
		if content["type"] == "tool_result" && slices.Contains(pending, id) && byID[id] == nil {
			byID[id] = content
			continue
		}
		extra = append(extra, content)
	}
	results := make([]block, 0, len(pending))
	for _, id := range pending {
		results = append(results, byID[id])
	}
	return results, extra
}

// claim marks a conversation busy for a call. Callers hold runtime.mu.
func (runtime *Runtime) claim(current *conversation, call Call) {
	current.state = stateBusy
	current.call = call
	current.generation++
	if current.timer != nil {
		current.timer.Stop()
	}
}

// release returns a claimed conversation that was not served. Callers hold
// runtime.mu.
func (runtime *Runtime) release(current *conversation) {
	switch {
	case current.process == nil:
		runtime.setState(current, stateDormant)
	case len(current.pending) > 0:
		runtime.setState(current, stateParked)
	default:
		runtime.setState(current, stateIdle)
	}
}

// live counts processes running or starting. Callers hold runtime.mu.
func (runtime *Runtime) live() int {
	count := runtime.starting
	for current := range runtime.conversations {
		if current.process != nil {
			count++
		}
	}
	return count
}

// reserve makes room for one more process, stopping the least recently used
// idle process, then the least recently used one waiting for tool results.
// Callers hold runtime.mu.
func (runtime *Runtime) reserve() bool {
	for runtime.live() >= runtime.options.MaxProcesses {
		var victim *conversation
		for _, wanted := range []state{stateIdle, stateParked} {
			for current := range runtime.conversations {
				if current.state == wanted && current.process != nil && (victim == nil || current.used.Before(victim.used)) {
					victim = current
				}
			}
			if victim != nil {
				break
			}
		}
		if victim == nil {
			return false
		}
		if victim.state == stateIdle {
			runtime.sleep(victim)
		} else {
			runtime.drop(victim)
		}
	}
	runtime.starting++
	return true
}

func (runtime *Runtime) unreserve() {
	runtime.mu.Lock()
	runtime.starting--
	runtime.mu.Unlock()
}

// start runs a process for a slot reserve made; the slot is released
// either way.
func (runtime *Runtime) start(ctx context.Context, id contract.ServiceID, binary claudecli.Binary, configDir, baseURL string, parsed request, session string, resume bool) (*process, error) {
	defer runtime.unreserve()
	accounts := runtime.options.Accounts
	runtime.mu.Lock()
	clean := !resume && !runtime.cleaned[configDir]
	runtime.mu.Unlock()
	if clean {
		// Transcripts left by an earlier run are never resumed.
		_ = os.RemoveAll(filepath.Join(configDir, "projects"))
	}
	workDir, err := accounts.Layout.EnsureWorkDir()
	if err != nil {
		return nil, err
	}
	environ := accounts.Environ
	if environ == nil {
		environ = os.Environ()
	}
	tunnel, err := accounts.Tunnel(id)
	if err != nil {
		return nil, err
	}
	target, err := startProcess(ctx, spawnSpec{
		Binary: binary, ConfigDir: configDir, WorkDir: workDir, Home: accounts.Layout.Home(), Environ: environ,
		BaseURL: baseURL, Tunnel: tunnel, Request: parsed, Session: session, Resume: resume,
	}, runtime.exited)
	if err == nil && clean {
		runtime.mu.Lock()
		runtime.cleaned[configDir] = true
		runtime.mu.Unlock()
	}
	return target, err
}

// attachProcess gives a conversation its new process.
func (runtime *Runtime) attachProcess(current *conversation, target *process) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	current.process = target
	runtime.secrets[sha256.Sum256([]byte(target.secret))] = current
	if runtime.closed {
		runtime.drop(current)
	}
}

// turnDone records how a turn left its conversation.
func (runtime *Runtime) turnDone(current *conversation, target *process, hashes []string, result turnResult) {
	runtime.mu.Lock()
	if current.process != target {
		runtime.mu.Unlock()
		return
	}
	current.used = runtime.options.Now()
	switch result.outcome {
	case outcomeFailed:
		runtime.drop(current)
	default:
		current.seen = append(slices.Clone(hashes), result.reply.hash())
		current.replyTools = toolUseIDs(result.reply)
		current.pending = result.pending
		if result.outcome == outcomeParked {
			runtime.setState(current, stateParked)
		} else {
			runtime.setState(current, stateIdle)
		}
	}
	runtime.mu.Unlock()
}

// setState moves a conversation and arms the timer that ends that state.
// Callers hold runtime.mu.
func (runtime *Runtime) setState(current *conversation, next state) {
	current.state = next
	current.generation++
	if current.timer != nil {
		current.timer.Stop()
		current.timer = nil
	}
	var wait time.Duration
	switch next {
	case stateParked:
		wait = runtime.options.ToolTimeout
	case stateIdle:
		wait = runtime.options.IdleTimeout
	case stateDormant:
		wait = runtime.options.ResumeWindow
	default:
		return
	}
	generation := current.generation
	current.timer = time.AfterFunc(wait, func() { runtime.expire(current, generation) })
}

func (runtime *Runtime) expire(current *conversation, generation int) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if current.generation != generation {
		return
	}
	if _, ok := runtime.conversations[current]; !ok {
		return
	}
	if current.state == stateIdle {
		runtime.sleep(current)
		return
	}
	runtime.drop(current)
}

// detach takes a conversation's process away. Callers hold runtime.mu.
func (runtime *Runtime) detach(current *conversation) *process {
	target := current.process
	if target != nil {
		delete(runtime.secrets, sha256.Sum256([]byte(target.secret)))
		current.process = nil
	}
	return target
}

// sleep stops an idle conversation's process and keeps its transcript for
// resume. Callers hold runtime.mu.
func (runtime *Runtime) sleep(current *conversation) {
	if target := runtime.detach(current); target != nil {
		go target.stopGracefully()
	}
	runtime.setState(current, stateDormant)
}

// drop ends a conversation and deletes its transcript. Callers hold
// runtime.mu.
func (runtime *Runtime) drop(current *conversation) {
	target := runtime.detach(current)
	delete(runtime.conversations, current)
	current.generation++
	if current.timer != nil {
		current.timer.Stop()
		current.timer = nil
	}
	configDir, session := current.configDir, current.session
	go func() {
		if target != nil {
			target.stop()
			<-target.exited
		}
		removeTranscript(configDir, session)
	}()
}

func removeTranscript(configDir, session string) {
	if configDir == "" || session == "" {
		return
	}
	projects := filepath.Join(configDir, "projects")
	entries, err := os.ReadDir(projects)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		_ = os.Remove(filepath.Join(projects, entry.Name(), session+".jsonl"))
		_ = os.RemoveAll(filepath.Join(projects, entry.Name(), session))
	}
}

// exited handles a process that ended on its own.
func (runtime *Runtime) exited(target *process) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	for current := range runtime.conversations {
		if current.process != target {
			continue
		}
		switch current.state {
		case stateIdle:
			runtime.sleep(current)
		case stateParked:
			runtime.drop(current)
		}
		// A busy conversation is ended by its turn.
		return
	}
}

// StopAccount ends every conversation of an account, before it signs out or
// is removed.
func (runtime *Runtime) StopAccount(id contract.ServiceID) {
	runtime.mu.Lock()
	var done []<-chan struct{}
	for current := range runtime.conversations {
		if current.service != id {
			continue
		}
		if current.process != nil {
			done = append(done, current.process.exited)
		}
		runtime.drop(current)
	}
	runtime.mu.Unlock()
	for _, exited := range done {
		<-exited
	}
}

// Close stops every process.
func (runtime *Runtime) Close() {
	runtime.mu.Lock()
	runtime.closed = true
	var done []<-chan struct{}
	for current := range runtime.conversations {
		if current.process != nil {
			done = append(done, current.process.exited)
		}
		runtime.drop(current)
	}
	runtime.mu.Unlock()
	for _, exited := range done {
		<-exited
	}
}
