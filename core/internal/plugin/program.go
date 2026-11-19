package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/eventloop"
	"github.com/MoeForge/moejs/node"
	"github.com/MoeForge/moejs/webapi"
)

// loadTime bounds a module's top level, and the reads of its manifest and
// hooks, in a new runtime. An 8 MiB plugin loads in about 100 ms. Tests
// shorten it.
var loadTime = 2 * time.Second

var errLoadTime = errors.New("plugin load time limit exceeded")

// program is a compiled plugin that loaded and passed its checks: the
// module, the manifest and the hooks it exports. It is immutable, and every
// generation that runs the same source shares it.
type program struct {
	key    string
	file   string
	module *moejs.Module
	meta   Meta
	hooks  map[string]moejs.Hook
	// sources maps positions in a built plugin back to the files it was
	// built from; nil without a source map.
	sources *sourceMap
	// dirs are the directories the plugin reaches through node:fs.
	dirs *pluginDirs
	// importer serves import() and import.meta in the program's runtimes.
	importer *moejs.Importer
	// task is the manifest of a task plugin, nil for any other plugin.
	task *TaskMeta
}

// async reports whether the plugin's hooks may return promises: those of
// apiVersion 2, which run on a loop of their own.
func (loaded *program) async() bool { return loaded.meta.APIVersion >= 2 }

// mapStack rewrites the positions of a stack trace into the plugin's own
// sources, when it came with a source map.
func (loaded *program) mapStack(stack string) string {
	if loaded == nil || loaded.sources == nil {
		return stack
	}
	return loaded.sources.rewrite(stack)
}

// load compiles source and loads it once in a runtime of its own, to check
// that its top level runs, that it exports a valid manifest, and that every
// other export is a hook the host calls. dirs are the directories its
// node:fs reaches.
func load(source Source, limits Limits, dirs *pluginDirs) (*program, error) {
	if source.Err != nil {
		return nil, source.Err
	}
	module, parsed, err := compile(source.File, source.Text)
	if err != nil {
		return nil, err
	}
	if isTaskModule(module) {
		return loadTask(source, module, parsed, limits, dirs)
	}
	loaded := &program{key: source.Key, file: source.File, module: module, hooks: make(map[string]moejs.Hook), sources: parseSourceMap(source.SourceMap, source.File), dirs: dirs}
	loaded.importer = &moejs.Importer{Resolve: resolveNode, Meta: loaded.importMeta}
	instance, err := newInstance(loaded, limits)
	if err != nil {
		return nil, loadFailure(source.File, err)
	}
	defer instance.close()
	stop := interruptAfter(instance.interrupt, loadTime, errLoadTime)
	defer stop()
	if isNewAPIMeta(instance.runtime) {
		return loadTask(source, module, parsed, limits, dirs)
	}
	meta, err := readMeta(instance.runtime, source.File)
	if err != nil {
		return nil, err
	}
	key := source.Key
	if key == "" {
		// A plugin checked before it is installed goes by its own key.
		key = meta.Key
	}
	if err := validateMeta(meta, key); err != nil {
		err.(*LoadError).File = source.File
		return nil, err
	}
	if meta.APIVersion < 2 {
		if err := checkSynchronous(source.File, parsed); err != nil {
			return nil, err
		}
	}
	loaded.meta = meta
	for _, name := range module.Exports() {
		if name == "meta" {
			continue
		}
		if !slices.Contains(knownHooks, name) {
			return nil, &LoadError{Code: CodeUnknownExport, File: source.File, Message: fmt.Sprintf("exports %q, which is neither meta nor a hook the host calls (%s)", name, strings.Join(knownHooks, ", "))}
		}
		if meta.APIVersion < 2 && !slices.Contains(versionOneHooks, name) {
			return nil, &LoadError{Code: CodeUnknownExport, File: source.File, Message: fmt.Sprintf("exports %q, a hook of apiVersion 2", name)}
		}
		hook, err := module.Hook(name)
		if err != nil {
			return nil, &LoadError{Code: CodeNotFunction, File: source.File, Message: fmt.Sprintf("the export %q is not a function", name)}
		}
		if callable, err := instance.runtime.Has(hook); err != nil || !callable {
			return nil, &LoadError{Code: CodeNotFunction, File: source.File, Message: fmt.Sprintf("the export %q is not a function", name)}
		}
		loaded.hooks[name] = hook
	}
	exported := make(map[string]bool, len(loaded.hooks))
	for name := range loaded.hooks {
		exported[name] = true
	}
	if err := checkProviderHooks(meta, exported); err != nil {
		return nil, &LoadError{Code: CodeInvalidMeta, File: source.File, Message: err.Error()}
	}
	return loaded, nil
}

// readMeta reads the manifest as strict JSON, so that a misspelt field is an
// error rather than a capability that silently stays off.
func readMeta(runtime *moejs.Runtime, file string) (Meta, error) {
	value, ok := runtime.Export("meta")
	if !ok {
		return Meta{}, &LoadError{Code: CodeMissingMeta, File: file, Message: "the plugin does not export meta"}
	}
	var raw json.RawMessage
	if err := runtime.UnmarshalWith(value, &raw, moejs.DecodeOptions{MaxBytes: 64 << 10, MaxNodes: 4096}); err != nil || len(raw) == 0 {
		return Meta{}, &LoadError{Code: CodeInvalidMeta, File: file, Message: "meta must be plain data of at most 64 KiB"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var meta Meta
	if err := decoder.Decode(&meta); err != nil {
		return Meta{}, &LoadError{Code: CodeInvalidMeta, File: file, Message: cleanText("meta: "+strings.TrimPrefix(err.Error(), "json: "), maxMessageRunes, "")}
	}
	return meta, nil
}

// loadFailure explains a top level that did not finish.
func loadFailure(file string, err error) error {
	failure := &LoadError{Code: CodeLoadFailed, File: file}
	var exception *moejs.Exception
	switch {
	case errors.Is(err, errLoadTime):
		failure.Message = fmt.Sprintf("the top level ran longer than %s", loadTime)
	case errors.Is(err, moejs.ErrMemoryLimit):
		failure.Message = "the top level kept more memory than a plugin may"
	case errors.As(err, &exception):
		failure.Line, failure.Column = stackPosition(stackOf(err), file)
		failure.Message = cleanText("the top level threw "+exceptionText(exception), maxMessageRunes, "")
	default:
		failure.Message = cleanText(err.Error(), maxMessageRunes, "")
	}
	return failure
}

// instance is one runtime that loaded a program, with the call its host
// functions serve while a hook runs.
type instance struct {
	runtime *moejs.Runtime
	current *call
	program *program
	node    nodeState
	// resources are the files and databases the runtime has open; a
	// cleanup closes them once the runtime is gone.
	resources *nodeResources

	// loop runs the runtime's callbacks (async.go); nil for a task
	// plugin, whose hooks are synchronous and which has no web APIs.
	loop *eventloop.Loop
	// web are the web APIs of moejs and files its node:fs and Buffer.
	web   *webapi.API
	files *node.API
	// fetches is the call whose requests the runtime's fetch sends.
	fetches fetchCalls
	// then is Promise.prototype.then as the runtime started, and stop the
	// reaction that stops the loop once a hook's promise settled.
	then, stop moejs.Value
	timers     loopTimers
	// kept are the values only Go holds for a while (keep).
	kept     map[uint64][]moejs.Value
	lastKept uint64
}

// newInstance loads the program in a new runtime. Plugins are trusted
// code, as Node's packages are: each runtime has builtins of its own that
// the plugin may change, and eval and new Function work.
func newInstance(loaded *program, limits Limits) (*instance, error) {
	runtime := moejs.NewRuntime(moejs.Options{
		MemoryLimit:       limits.Memory,
		MutableIntrinsics: true,
		TimeZone:          time.UTC,
		Importer:          loaded.importer,
	})
	created := &instance{runtime: runtime, program: loaded, resources: &nodeResources{}}
	goruntime.AddCleanup(created, (*nodeResources).close, created.resources)
	if loaded.task != nil {
		// A task plugin gets new-api's utils and a console, as its
		// contract says, and nothing of AstrLink's or Node's.
		if err := created.installTask(); err != nil {
			return nil, err
		}
	} else {
		if err := created.newLoop(); err != nil {
			return nil, err
		}
		if err := created.install(); err != nil {
			created.close()
			return nil, err
		}
		if err := created.installNode(); err != nil {
			created.close()
			return nil, err
		}
	}
	stop := interruptAfter(created.interrupt, loadTime, errLoadTime)
	var err error
	if created.loop == nil {
		err = runtime.Load(loaded.module)
	} else {
		// The loop runs until what the top level started settled, the
		// promises of node:fs among them; its timers never fire.
		err = created.loop.Run(func(runtime *moejs.Runtime) error { return runtime.Load(loaded.module) })
	}
	stop()
	created.resources.endCall()
	if err != nil {
		err = withStack(runtime, err)
		created.close()
		return nil, err
	}
	runtime.ClearInterrupt()
	return created, nil
}

// thrown is an exception with the stack trace that only the runtime it was
// thrown in can render.
type thrown struct {
	err   error
	stack string
}

func (failure *thrown) Error() string { return failure.err.Error() }

func (failure *thrown) Unwrap() error { return failure.err }

func withStack(runtime *moejs.Runtime, err error) error {
	var exception *moejs.Exception
	if !errors.As(err, &exception) {
		return err
	}
	return &thrown{err: err, stack: runtime.StackTrace(exception)}
}

func stackOf(err error) string {
	var failure *thrown
	if errors.As(err, &failure) {
		return failure.stack
	}
	return ""
}

// interruptAfter interrupts a runtime with cause after d. Once the
// returned function returns, no interrupt from it is pending or still to
// come; one that came stays pending for the caller to clear.
func interruptAfter(interrupt func(error), d time.Duration, cause error) func() {
	var mutex sync.Mutex
	stopped := false
	timer := time.AfterFunc(d, func() {
		mutex.Lock()
		defer mutex.Unlock()
		if !stopped {
			interrupt(cause)
		}
	})
	return func() {
		mutex.Lock()
		stopped = true
		mutex.Unlock()
		timer.Stop()
	}
}

// pool keeps the runtimes that loaded one program for one generation of its
// settings. Runtimes never move to another pool, so nothing a plugin keeps
// in module state outlives a change of its settings.
type pool struct {
	program *program
	limits  Limits
	idle    chan *instance
	slots   chan struct{}
}

func newPool(loaded *program, limits Limits) *pool {
	return &pool{
		program: loaded,
		limits:  limits,
		idle:    make(chan *instance, limits.Concurrency),
		slots:   make(chan struct{}, limits.Concurrency),
	}
}

func (pool *pool) acquire(ctx context.Context) error {
	select {
	case pool.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (pool *pool) release() { <-pool.slots }

func (pool *pool) take() (*instance, error) {
	select {
	case taken := <-pool.idle:
		return taken, nil
	default:
	}
	return newInstance(pool.program, pool.limits)
}

func (pool *pool) put(returned *instance) {
	select {
	case pool.idle <- returned:
	default:
		returned.close()
	}
}

// run calls hook with args on a runtime of the pool, with c bound for its
// host functions, and returns the JSON text of the result, nil for
// undefined. The hook of an apiVersion 2 plugin may return a promise: the
// runtime's loop then runs until it settles. Once the call ended, what it
// left behind is canceled and comes to rest on the loop. A runtime whose
// call failed, or whose loop did not come to rest, is dropped: the failure
// may have left its module state half updated.
func (pool *pool) run(ctx context.Context, c *call, hook moejs.Hook, args []any) (json.RawMessage, error) {
	if err := pool.acquire(ctx); err != nil {
		return nil, err
	}
	c.slot = true
	defer func() {
		if c.slot {
			pool.release()
			c.slot = false
		}
	}()
	taken, err := pool.take()
	if err != nil {
		return nil, err
	}
	runtime := taken.runtime
	c.runtime, c.instance = runtime, taken
	c.watchdog = &watchdog{interrupt: taken.interrupt, budget: pool.limits.ScriptTime}
	taken.begin(c)
	interrupted := make(chan struct{})
	stopContext := context.AfterFunc(ctx, func() {
		taken.interrupt(context.Cause(ctx))
		close(interrupted)
	})
	result, err := taken.invoke(c, hook, args, pool.limits)
	c.leave()
	if !stopContext() {
		<-interrupted
	}
	// What the call started on other goroutines, its requests and host
	// work, ends with it.
	c.end(errCallEnded)
	taken.finish(c)
	rested := err == nil && taken.rest(c)
	taken.resources.endCall()
	if err != nil {
		err = withStack(runtime, err)
		taken.close()
		return nil, err
	}
	if !rested {
		taken.close()
		return result, nil
	}
	runtime.ClearInterrupt()
	runtime.ReleaseCallData()
	pool.put(taken)
	return result, nil
}

// invoke calls hook with args and reads its result as JSON.
func (bound *instance) invoke(c *call, hook moejs.Hook, args []any, limits Limits) (json.RawMessage, error) {
	runtime := bound.runtime
	if err := c.enter(); err != nil {
		return nil, err
	}
	values := make([]moejs.Value, 0, len(args))
	for _, arg := range args {
		value, err := runtime.FromGo(arg)
		if err != nil {
			return nil, fmt.Errorf("pass the hook's argument: %w", err)
		}
		values = append(values, value)
	}
	value, err := bound.callHook(c, hook, values)
	if err != nil {
		return nil, err
	}
	if value.IsUndefined() {
		return nil, nil
	}
	// Writing the result can run its toJSON methods.
	if err := c.enter(); err != nil {
		return nil, err
	}
	var result json.RawMessage
	if err := runtime.UnmarshalWith(value, &result, moejs.DecodeOptions{MaxBytes: limits.ResultBytes, MaxNodes: limits.ResultNodes}); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		// A function or symbol has no JSON text: no result.
		return nil, nil
	}
	return result, nil
}

// watchdog interrupts a call whose JavaScript runs longer than budget.
// Waits on the host pause it.
type watchdog struct {
	interrupt func(error)
	budget    time.Duration
	mutex     sync.Mutex
	spent     time.Duration
	since     time.Time // zero while paused
	// timer fires once what is left of the budget passed; it is made the
	// first time the dog starts, and stopped while it is paused.
	timer *time.Timer
}

func (dog *watchdog) start() {
	dog.mutex.Lock()
	defer dog.mutex.Unlock()
	if !dog.since.IsZero() {
		return
	}
	dog.since = time.Now()
	left := dog.budget - dog.spent
	if left <= 0 {
		dog.interrupt(errScriptTime)
		return
	}
	if dog.timer == nil {
		dog.timer = time.AfterFunc(left, dog.expire)
		return
	}
	dog.timer.Reset(left)
}

func (dog *watchdog) pause() {
	dog.mutex.Lock()
	defer dog.mutex.Unlock()
	if dog.since.IsZero() {
		return
	}
	dog.spent += time.Since(dog.since)
	dog.since = time.Time{}
	if dog.timer != nil {
		dog.timer.Stop()
	}
}

func (dog *watchdog) expire() {
	dog.mutex.Lock()
	defer dog.mutex.Unlock()
	// A timer that fired while a pause stopped it finds the dog paused, or
	// running again on a newer timer with time left.
	if dog.since.IsZero() || dog.spent+time.Since(dog.since) < dog.budget {
		return
	}
	dog.interrupt(errScriptTime)
}

// running reports whether the dog counts script time now.
func (dog *watchdog) running() bool {
	dog.mutex.Lock()
	defer dog.mutex.Unlock()
	return !dog.since.IsZero()
}

func (dog *watchdog) used() time.Duration {
	dog.mutex.Lock()
	defer dog.mutex.Unlock()
	if dog.since.IsZero() {
		return dog.spent
	}
	return dog.spent + time.Since(dog.since)
}

// classify turns what stopped a call into a CallError.
func classify(err error, limits Limits) *CallError {
	var existing *CallError
	if errors.As(err, &existing) {
		return existing
	}
	failure := &CallError{Reason: ReasonPluginError, Err: err}
	var exception *moejs.Exception
	var internal *moejs.InternalError
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	switch {
	case errors.Is(err, moejs.ErrMemoryLimit):
		failure.Reason, failure.Message = ReasonMemory, fmt.Sprintf("the plugin kept more than %d MiB of memory", limits.Memory>>20)
	case errors.Is(err, errScriptTime):
		failure.Reason, failure.Message = ReasonTimeout, fmt.Sprintf("the plugin ran JavaScript for longer than %s", limits.ScriptTime)
	case errors.Is(err, context.DeadlineExceeded):
		failure.Reason, failure.Message = ReasonTimeout, "the call ran out of time"
	case errors.Is(err, context.Canceled):
		failure.Reason, failure.Message = ReasonCanceled, "the request ended before the call"
	case errors.Is(err, moejs.ErrTooLarge):
		failure.Reason, failure.Message = ReasonInvalidResult, fmt.Sprintf("the result is larger than %d KiB or %d values", limits.ResultBytes>>10, limits.ResultNodes)
	case errors.As(err, &exception):
		failure.Message = cleanText("threw "+exceptionText(exception)+stackSuffix(stackOf(err)), maxMessageRunes, "")
		failure.Thrown = cleanText(exception.Message(), maxMessageRunes, "")
		if failure.Thrown == "" {
			failure.Thrown = cleanText(exceptionText(exception), maxMessageRunes, "")
		}
	case errors.As(err, &internal):
		failure.Message = "the call failed inside the host"
	case errors.As(err, &syntaxError), errors.As(err, &typeError):
		failure.Reason, failure.Message = ReasonInvalidResult, "the result is not plain data"
	default:
		failure.Message = cleanText(err.Error(), maxMessageRunes, "")
	}
	return failure
}

func exceptionText(exception *moejs.Exception) string {
	name, message := exception.Name(), exception.Message()
	switch {
	case name == "":
		return message
	case message == "":
		return name
	}
	return name + ": " + message
}

// stackFrame matches a V8 stack line's location: "at f (plugin.ts:12:5)" or
// "at plugin.ts:12:5".
var stackFrame = regexp.MustCompile(`\(?([^\s()]+):(\d+):(\d+)\)?\s*$`)

// stackPosition finds the first frame of a stack trace that is in file.
func stackPosition(stack, file string) (line, column int) {
	for _, text := range strings.Split(stack, "\n") {
		match := stackFrame.FindStringSubmatch(strings.TrimSpace(text))
		if match == nil || match[1] != file {
			continue
		}
		line, _ = strconv.Atoi(match[2])
		column, _ = strconv.Atoi(match[3])
		return line, column
	}
	return 0, 0
}

// stackSuffix names the first frame with a source position: a host function
// that threw sits above the plugin's own frame without one. The frames of
// the host's prelude are not the plugin's and are skipped.
func stackSuffix(stack string) string {
	first := ""
	for _, text := range strings.Split(stack, "\n") {
		text = strings.TrimSpace(text)
		if !strings.HasPrefix(text, "at ") || strings.Contains(text, preludeName) {
			continue
		}
		if stackFrame.MatchString(text) {
			return " (" + text + ")"
		}
		if first == "" {
			first = text
		}
	}
	if first == "" {
		return ""
	}
	return " (" + first + ")"
}

// withoutPrelude drops the frames of the host's prelude from a stack trace.
func withoutPrelude(stack string) string {
	if !strings.Contains(stack, preludeName) {
		return stack
	}
	lines := strings.Split(stack, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.Contains(line, preludeName) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}
