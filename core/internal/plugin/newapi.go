package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/MoeForge/moejs"
	"github.com/QuantumNous/new-api/pluginkit"
)

// A plugin written for New API's plugin API v1 runs on pluginkit, New API's
// own implementation of the contract, so that it behaves as it does there.
// The gateway gives it what pluginkit leaves to the host: utils.fetch's
// network, the files of a request, the paths it keeps for itself, and the
// call statistics and journal of the plugin page.

// newAPIReservedPaths are the paths of the inference port, with what lies
// below them, that no native route may take: the gateway's own pages and
// plugin endpoints, and the task API. A route on another path the gateway
// answers, such as /v1/chat/completions, is still declared; it is served
// under /{key} alone.
var newAPIReservedPaths = []string{"/v1/tasks", "/assets", "/astrlink", "/console", "/control", "/plugins", "/tools"}

// hostReservedKeys are the keys a New API plugin may not have here beside
// the first segments of newAPIReservedPaths, as its routes are also served
// under /{key}: the first segments of the gateway's other paths, and api,
// which New API keeps.
var hostReservedKeys = []string{"api", "v1", "v1beta"}

func init() {
	// The other members keep the contract's defaults; utils.paramOverride
	// is not available on this host.
	pluginkit.Configure(pluginkit.Host{
		ReservedRouteNamespaces: newAPIReservedPaths,
		ReservedKeys:            hostReservedKeys,
	})
}

// isTaskModule reports whether a compiled module is a task plugin: one that
// exports buildSubmitRequest.
func isTaskModule(module *moejs.Module) bool {
	return slices.Contains(module.Exports(), "buildSubmitRequest")
}

// isNewAPIMeta reports whether the manifest a plugin's top level exported
// is New API's: one with meta.relay or meta.author, which AstrLink's own
// manifest does not have. A New API plugin with only a provider or request
// processing part exports no task hook to tell it by.
func isNewAPIMeta(runtime *moejs.Runtime) bool {
	value, ok := runtime.Export("meta")
	if !ok {
		return false
	}
	var members map[string]json.RawMessage
	if err := runtime.UnmarshalWith(value, &members, moejs.DecodeOptions{MaxBytes: 1 << 20, MaxNodes: 1 << 16}); err != nil {
		return false
	}
	_, relay := members["relay"]
	_, author := members["author"]
	return relay || author
}

// ReadTaskSource reads a New API plugin from a source directory: a
// plugin.js with no package.json to build it from. Such a plugin installs
// and runs as it is, never built; whether it is one is decided when it
// loads.
func ReadTaskSource(dir string) (string, bool) {
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		return "", false
	}
	text, err := os.ReadFile(filepath.Join(dir, "plugin.js"))
	if err != nil || len(text) > MaxSourceBytes {
		return "", false
	}
	return string(text), true
}

// newAPILog is where a New API plugin's console goes: the journal of the
// plugin it is loaded as, once a manager publishes it.
type newAPILog struct {
	mutex   sync.Mutex
	counter *counter
}

func (sink *newAPILog) attach(count *counter) {
	sink.mutex.Lock()
	sink.counter = count
	sink.mutex.Unlock()
}

func (sink *newAPILog) write(message string) {
	sink.mutex.Lock()
	count := sink.counter
	sink.mutex.Unlock()
	if count == nil {
		return
	}
	// pluginkit names the plugin in front of what it wrote.
	if strings.HasPrefix(message, "[plugin:") {
		if _, rest, found := strings.Cut(message, "] "); found {
			message = rest
		}
	}
	message = cleanText(message, maxLogBytes, "")
	for len(message) > maxLogBytes {
		_, size := utf8.DecodeLastRuneInString(message)
		message = message[:len(message)-size]
	}
	count.journal.mutex.Lock()
	count.journal.append(JournalEntry{Time: time.Now(), Hook: "console", Level: "info", Message: message})
	count.journal.mutex.Unlock()
}

// loadNewAPI compiles a plugin written for New API's plugin API v1 with
// pluginkit, which checks it as New API does.
func loadNewAPI(source Source, dirs *pluginDirs) (*program, error) {
	sink := &newAPILog{}
	compiled, err := pluginkit.CompilePlugin(source.Text, pluginkit.Options{Key: source.Key, Log: sink.write})
	if err != nil {
		return nil, newAPILoadError(source.File, err)
	}
	if source.Key != "" && compiled.Meta.Key != source.Key {
		return nil, &LoadError{Code: CodeInvalidMeta, File: source.File, Message: fmt.Sprintf("meta.key %q does not match the plugin directory %q", compiled.Meta.Key, source.Key)}
	}
	loaded := &program{key: source.Key, file: source.File, hooks: map[string]moejs.Hook{}, dirs: dirs, newAPI: &NewAPIPlugin{LoadedPlugin: compiled, log: sink}}
	loaded.meta = Meta{APIVersion: 1, Key: compiled.Meta.Key, Name: compiled.Meta.Name, Version: compiled.Meta.Version}
	return loaded, nil
}

// newAPILoadError is why pluginkit refused a plugin, with the code of the
// check that failed.
func newAPILoadError(file string, err error) *LoadError {
	message := err.Error()
	code := CodeInvalidMeta
	switch {
	case strings.HasPrefix(message, "unsupported plugin syntax"):
		code = CodeSyntax
		switch {
		case strings.Contains(message, `"async"`):
			code = CodeAsync
		case strings.Contains(message, `"await"`):
			code = CodeAwait
		case strings.Contains(message, `"import"`):
			code = CodeImport
		case strings.Contains(message, "re-export"):
			code = CodeReexport
		}
	case strings.HasPrefix(message, "compile plugin"):
		code = CodeSyntax
	case strings.HasPrefix(message, "evaluate plugin"), strings.HasPrefix(message, "initialize plugin"), strings.HasPrefix(message, "inject plugin utils"):
		code = CodeLoadFailed
	case strings.Contains(message, "is not a function"):
		code = CodeNotFunction
	case strings.Contains(message, `export "meta" not found`):
		code = CodeMissingMeta
	}
	return &LoadError{Code: code, File: file, Message: cleanText(message, maxMessageRunes, "")}
}

// NewAPIPlugin is a loaded New API plugin: pluginkit's, with what the
// gateway reads of it.
type NewAPIPlugin struct {
	*pluginkit.LoadedPlugin
	log *newAPILog
	// paths caches whether a dotted hook path, native.render, is callable.
	paths sync.Map
	// configs caches InstanceConfig by the stored config.
	configs sync.Map
}

type instanceConfig struct {
	config json.RawMessage
	err    error
}

// InstanceConfig is a request processing instance's stored config as its
// hooks read it: checked against meta.configSchema, with its defaults, and
// passed through the plugin's validateConfig. The result is kept per stored
// config, so a request runs no validateConfig.
func (loaded *NewAPIPlugin) InstanceConfig(stored json.RawMessage) (json.RawMessage, error) {
	if cached, ok := loaded.configs.Load(string(stored)); ok {
		entry := cached.(instanceConfig)
		return entry.config, entry.err
	}
	config, err := loaded.ValidateConfig(stored)
	loaded.configs.Store(string(stored), instanceConfig{config: config, err: err})
	return config, err
}

// NameIn is the plugin's name in language: its English name without
// translations.
func (loaded *NewAPIPlugin) NameIn(language string) string {
	if text := loaded.Meta.NameI18n.Text(language); text != "" {
		return text
	}
	return loaded.Meta.Name
}

// HasTaskPart reports whether the plugin has a task part.
func (loaded *NewAPIPlugin) HasTaskPart() bool { return loaded != nil && loaded.Meta.HasTaskPart() }

// HasRequestPart reports whether the plugin has a request processing part.
func (loaded *NewAPIPlugin) HasRequestPart() bool {
	return loaded != nil && loaded.Meta.HasTransformPart()
}

// HasStage reports whether the plugin exports the request processing hook.
func (loaded *NewAPIPlugin) HasStage(hook string) bool {
	return loaded != nil && slices.Contains(loaded.TransformHooks(), hook)
}

// InScope reports whether the plugin's scope holds a request for model, the
// model the client asked for, matched without its @ modifiers as an
// instance's models are: true without a scope or with one that lists no
// models. The gateway has no channel types; it matches on the models alone.
func (loaded *NewAPIPlugin) InScope(model string) bool {
	if loaded == nil || loaded.Meta.Scope == nil || len(loaded.Meta.Scope.Models) == 0 {
		return true
	}
	base, _, _ := strings.Cut(model, "@")
	return slices.Contains(loaded.Meta.Scope.Models, base)
}

// Claim returns the plugin's claim on protocol, nil when it makes none.
func (loaded *NewAPIPlugin) Claim(protocol string) *pluginkit.ProtocolClaim {
	for index := range loaded.Meta.Protocols {
		if loaded.Meta.Protocols[index].Name == protocol {
			return &loaded.Meta.Protocols[index]
		}
	}
	return nil
}

// ServesProtocol reports whether the plugin serves model on protocol.
func (loaded *NewAPIPlugin) ServesProtocol(protocol, model string) bool {
	claim := loaded.Claim(protocol)
	return claim != nil && slices.Contains(loaded.Meta.Models, model) && (len(claim.Models) == 0 || slices.Contains(claim.Models, model))
}

// Has reports whether the plugin requires the host capability.
func (loaded *NewAPIPlugin) Has(capability string) bool {
	return slices.Contains(loaded.Meta.RequiredCapabilities, capability)
}

// SubmitsSSE reports whether the plugin may read a submission as SSE.
func (loaded *NewAPIPlugin) SubmitsSSE() bool {
	return slices.Contains(loaded.Meta.SubmitResponseTypes, "sse")
}

// Batch reports whether the plugin polls its tasks in batches.
func (loaded *NewAPIPlugin) Batch() bool { return loaded.Meta.FetchMode == "batch" }

// AuthKind is how the plugin's provider authenticates: api_key, the default,
// oauth2_jwt or none.
func (loaded *NewAPIPlugin) AuthKind() string {
	if loaded.Meta.Auth.Type == "" {
		return "api_key"
	}
	return loaded.Meta.Auth.Type
}

// HasHook reports whether the plugin exports the hook at path: a top-level
// export, or a member below one, such as native.render or
// protocols.openai_video.render.
func (loaded *NewAPIPlugin) HasHook(path string) bool {
	export, rest, nested := strings.Cut(path, ".")
	if !loaded.Engine.HasExport(export) {
		return false
	}
	if !nested {
		return true
	}
	if known, ok := loaded.paths.Load(path); ok {
		return known.(bool)
	}
	callable, err := loaded.Engine.HasCallablePath(context.Background(), export, strings.Split(rest, ".")...)
	if err != nil {
		return false
	}
	loaded.paths.Store(path, callable)
	return callable
}

// exports are the hooks the plugin exports, by their paths.
func (loaded *NewAPIPlugin) exports() []string {
	var hooks []string
	top := slices.Concat(newAPITaskHooks, pluginkit.ProviderHooks, pluginkit.TransformStages, []string{pluginkit.ReplyHook, pluginkit.ReportHook, "validateConfig"})
	for _, hook := range top {
		if loaded.Engine.HasExport(hook) && !slices.Contains(hooks, hook) {
			hooks = append(hooks, hook)
		}
	}
	for _, route := range loaded.Meta.Routes {
		for _, member := range []string{route.Decode, route.Render} {
			if member != "" && !slices.Contains(hooks, "native."+member) {
				hooks = append(hooks, "native."+member)
			}
		}
	}
	if loaded.HasHook("native.error") {
		hooks = append(hooks, "native.error")
	}
	for _, claim := range loaded.Meta.Protocols {
		definition, _ := pluginkit.HostProtocol(claim.Name)
		for _, operation := range definition.Operations {
			members := slices.Clone(operation.RequiredProtocolMembers)
			for _, mode := range operation.Modes {
				members = append(members, mode.Hook)
			}
			for _, member := range members {
				path := "protocols." + claim.Name + "." + member
				if !slices.Contains(hooks, path) && loaded.HasHook(path) {
					hooks = append(hooks, path)
				}
			}
		}
	}
	slices.Sort(hooks)
	return hooks
}

// The host protocols a task plugin may claim, and the forms of request
// openai_responses takes, as the gateway names them.
const (
	TaskProtocolResponses = "openai_responses"
	TaskProtocolVideo     = "openai_video"
	TaskProtocolImage     = "openai_image"

	TaskModeStream     = "stream"
	TaskModeSync       = "sync"
	TaskModeBackground = "background"
)

// newAPITaskHooks are the functions of a task part the host calls, beside
// the members of its native and protocols exports.
var newAPITaskHooks = []string{
	"buildSubmitRequest", "parseSubmitResponse", "parseSubmitEvent", "parseSubmitEventDelta",
	"buildQueryRequest", "buildBatchQueryRequest", "parseBatchResult", "parseTaskResult",
	"extractUsage", "extractUsageOnSubmit", "extractUsageOnComplete", "listArtifacts", "buildContentRequest",
}

// Task returns a plugin with a task part, nil for any other plugin and for
// one that did not load.
func (plugin *Plugin) Task() *NewAPIPlugin {
	if loaded := plugin.NewAPI(); loaded.HasTaskPart() {
		return loaded
	}
	return nil
}

// NewAPI returns a plugin written for New API's plugin API v1, whatever its
// parts, nil for any other plugin and for one that did not load.
func (plugin *Plugin) NewAPI() *NewAPIPlugin {
	if plugin.program == nil {
		return nil
	}
	return plugin.program.newAPI
}

// HasTaskHook reports whether a New API plugin exports the hook at path.
func (plugin *Plugin) HasTaskHook(path string) bool {
	loaded := plugin.NewAPI()
	return loaded != nil && loaded.HasHook(path)
}

// Fetch is what utils.fetch of a New API plugin's hook call reaches: the
// hosts Allowed takes, through Client. Without one, utils.fetch throws.
type Fetch struct {
	Allowed func(target *url.URL) bool
	Client  *http.Client
}

// fetcher sends utils.fetch requests as fetch allows, counting them in
// stats. A redirect reaches the plugin as it is.
func (fetch *Fetch) fetcher(stats *NetworkStats, mutex *sync.Mutex) pluginkit.Fetcher {
	return func(ctx context.Context, request pluginkit.FetchRequest) (pluginkit.FetchResponse, error) {
		count := func(denied, failed bool) {
			mutex.Lock()
			defer mutex.Unlock()
			stats.Requests++
			if denied {
				stats.Denied++
			}
			if failed {
				stats.Errors++
			}
		}
		target, err := url.Parse(request.URL)
		if err != nil || !fetch.Allowed(target) {
			count(true, false)
			host := request.URL
			if err == nil {
				host = target.Hostname()
			}
			return pluginkit.FetchResponse{}, fmt.Errorf("the host %s is not allowed", host)
		}
		var body io.Reader
		if request.Method == http.MethodPost {
			body = strings.NewReader(request.Body)
			if request.BodyStream != nil {
				body = request.BodyStream
			}
		}
		outgoing, err := http.NewRequestWithContext(ctx, request.Method, request.URL, body)
		if err != nil {
			count(false, true)
			return pluginkit.FetchResponse{}, err
		}
		if request.Method == http.MethodPost && request.BodyStream != nil {
			outgoing.ContentLength = request.BodySize
		}
		for name, value := range request.Headers {
			outgoing.Header.Set(name, value)
		}
		if outgoing.Header.Get("User-Agent") == "" {
			// Go's default would name the client.
			outgoing.Header.Set("User-Agent", "")
		}
		client := *fetch.Client
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		started := time.Now()
		response, err := client.Do(outgoing)
		mutex.Lock()
		stats.Wait += time.Since(started)
		mutex.Unlock()
		if err != nil {
			count(false, true)
			return pluginkit.FetchResponse{}, err
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, request.ResponseBodyLimit()+1))
		count(false, err != nil)
		if err != nil {
			return pluginkit.FetchResponse{Body: data}, err
		}
		headers := make(map[string]string, len(response.Header))
		for name, values := range response.Header {
			if len(values) > 0 {
				headers[name] = values[0]
			}
		}
		return pluginkit.FetchResponse{Status: response.StatusCode, Headers: headers, Body: data}, nil
	}
}

// CallNewAPI runs call, one call of hook into a New API plugin's pluginkit
// engine, as the plugin page counts and journals calls. options.Fetch, when
// set, is what utils.fetch reaches. A failure is a *CallError; the CallInfo
// is filled either way.
func CallNewAPI[T any](ctx context.Context, plugin *Plugin, hook string, options CallOptions, call func(ctx context.Context, loaded *NewAPIPlugin) (T, error)) (T, CallInfo, error) {
	var zero T
	loaded := plugin.NewAPI()
	if loaded == nil {
		return zero, CallInfo{}, &CallError{Reason: ReasonUnavailable, Message: "the plugin is not a New API plugin that loaded"}
	}
	var stats NetworkStats
	var mutex sync.Mutex
	if options.Fetch != nil {
		ctx = pluginkit.WithFetcher(ctx, options.Fetch.fetcher(&stats, &mutex))
	} else {
		ctx = pluginkit.WithFetcher(ctx, nil)
	}
	started := time.Now()
	result, err := call(ctx, loaded)
	mutex.Lock()
	info := CallInfo{Elapsed: time.Since(started), Network: stats}
	mutex.Unlock()
	if err != nil {
		failure := newAPIFailure(err)
		plugin.counter.record(info, failure)
		plugin.note(hook, info, failure, options.DryRun)
		return zero, info, failure
	}
	plugin.counter.record(info, nil)
	plugin.note(hook, info, nil, options.DryRun)
	return result, info, nil
}

// CallTask runs the hook of a New API plugin at path with args, Go values
// that pass to it as JSON (a json.RawMessage as the value its text holds),
// and returns its result as JSON text, nil for undefined or null. The typed
// calls of pluginkit check what each hook of the contract returns; this one
// serves the hooks a person runs by hand.
func (plugin *Plugin) CallTask(ctx context.Context, path string, args []any, options CallOptions) (json.RawMessage, CallInfo, error) {
	return CallNewAPI(ctx, plugin, path, options, func(ctx context.Context, loaded *NewAPIPlugin) (json.RawMessage, error) {
		if !loaded.HasHook(path) {
			return nil, &CallError{Reason: ReasonUnavailable, Message: fmt.Sprintf("the plugin does not export %s", path)}
		}
		values := make([]any, len(args))
		for index, arg := range args {
			values[index] = NewAPIValue(arg)
		}
		export, rest, nested := strings.Cut(path, ".")
		var members []string
		if nested {
			members = strings.Split(rest, ".")
		}
		// JSON.stringify's text keeps the member order the plugin wrote.
		var text json.RawMessage
		if err := loaded.Engine.CallPathJSONInto(ctx, &text, export, members, values...); err != nil || len(text) == 0 || string(text) == "null" {
			return nil, err
		}
		return text, nil
	})
}

// newAPIFailure is a failed call into pluginkit as a CallError.
func newAPIFailure(err error) *CallError {
	var existing *CallError
	if errors.As(err, &existing) {
		return existing
	}
	failure := &CallError{Reason: ReasonPluginError, Err: err, Message: cleanText(err.Error(), maxMessageRunes, "")}
	var hookErr *pluginkit.HookError
	var resultErr *pluginkit.ResultError
	var transformErr *pluginkit.TransformError
	switch {
	case errors.As(err, &transformErr) && transformErr.Class != pluginkit.TransformErrorThrow:
		switch transformErr.Class {
		case pluginkit.TransformErrorTimeout:
			failure.Reason = ReasonTimeout
		case pluginkit.TransformErrorMemory:
			failure.Reason = ReasonMemory
		case pluginkit.TransformErrorCanceled:
			failure.Reason = ReasonCanceled
		case pluginkit.TransformErrorResult:
			failure.Reason = ReasonInvalidResult
		}
	case errors.As(err, &hookErr):
		failure.Message = cleanText("threw "+hookErr.Message, maxMessageRunes, "")
		failure.Thrown = cleanText(hookErr.Message, maxMessageRunes, "")
	case errors.Is(err, pluginkit.ErrMemoryLimit):
		failure.Reason, failure.Message = ReasonMemory, "the plugin kept more memory than a call may"
	case errors.Is(err, pluginkit.ErrCallTimeout), errors.Is(err, pluginkit.ErrCallAdmissionTimeout), errors.Is(err, context.DeadlineExceeded):
		failure.Reason, failure.Message = ReasonTimeout, "the call ran out of time"
	case errors.Is(err, context.Canceled):
		failure.Reason, failure.Message = ReasonCanceled, "the request ended before the call"
	case errors.As(err, &resultErr):
		failure.Reason = ReasonInvalidResult
	}
	return failure
}

// NewAPIValue is a Go value as a hook of a New API plugin receives it: JSON
// text, a json.RawMessage, as the value it holds, members in their order;
// maps and slices member by member; any other value as its encoding/json
// form, struct fields in their order.
func NewAPIValue(value any) any {
	switch typed := value.(type) {
	case nil, bool, string, int, int64, float64, json.Number, pluginkit.RawJSON:
		return value
	case json.RawMessage:
		if len(bytes.TrimSpace(typed)) == 0 {
			return nil
		}
		return pluginkit.RawJSON(typed)
	case map[string]any:
		converted := make(map[string]any, len(typed))
		for name, member := range typed {
			converted[name] = NewAPIValue(member)
		}
		return converted
	case []any:
		converted := make([]any, len(typed))
		for index, item := range typed {
			converted[index] = NewAPIValue(item)
		}
		return converted
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	return pluginkit.RawJSON(encoded)
}

// NewAPIObject is NewAPIValue of a value that encodes as a JSON object, as
// the typed calls of pluginkit take a hook's context: its members each
// keep their order, though not the order among them.
func NewAPIObject(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return NewAPIValue(object).(map[string]any)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(encoded, &members) != nil {
		return nil
	}
	object := make(map[string]any, len(members))
	for name, member := range members {
		object[name] = pluginkit.RawJSON(member)
	}
	return object
}
