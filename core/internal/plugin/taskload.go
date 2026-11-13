package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/syntax"
)

// taskHooks are the functions of a task plugin the host calls, beside the
// members of its native and protocols exports.
var taskHooks = []string{
	"buildSubmitRequest", "parseSubmitResponse", "parseSubmitEvent", "parseSubmitEventDelta",
	"buildQueryRequest", "buildBatchQueryRequest", "parseBatchResult", "parseTaskResult",
	"extractUsage", "extractUsageOnSubmit", "extractUsageOnComplete", "listArtifacts", "buildContentRequest",
}

// isTaskModule reports whether a compiled module is a task plugin: one that
// exports buildSubmitRequest.
func isTaskModule(module *moejs.Module) bool {
	return slices.Contains(module.Exports(), "buildSubmitRequest")
}

// isNewAPIMeta reports whether the manifest a plugin's top level exported
// is new-api's: one with meta.relay or meta.author, which AstrLink's own
// manifest does not have. A new-api plugin with only a provider part
// exports no task hook to tell it by.
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

// taskLimits are the limits of a task plugin's hooks: conversions of whole
// media requests and responses take more time, memory and result than the
// hooks of a turn.
func taskLimits(limits Limits) Limits {
	limits.ScriptTime = max(limits.ScriptTime, 5*time.Second)
	limits.Memory = max(limits.Memory, 256<<20)
	limits.ResultBytes = max(limits.ResultBytes, 64<<20)
	limits.ResultNodes = max(limits.ResultNodes, 1<<20)
	return limits
}

// loadTask loads a task plugin: its manifest is new-api's, and its hooks
// are the driver's functions and the members of native and of each protocol
// of protocols, by their paths: native.createJob,
// protocols.openai_video.render.
func loadTask(source Source, module *moejs.Module, parsed *syntax.Module, limits Limits, dirs *pluginDirs) (*program, error) {
	if err := checkSynchronous(source.File, parsed); err != nil {
		return nil, err
	}
	loaded := &program{key: source.Key, file: source.File, module: module, hooks: make(map[string]moejs.Hook), sources: parseSourceMap(source.SourceMap, source.File), dirs: dirs, task: &TaskMeta{}}
	instance, err := newInstance(loaded, taskLimits(limits))
	if err != nil {
		return nil, loadFailure(source.File, err)
	}
	defer instance.close()
	stop := interruptAfter(instance.interrupt, loadTime, errLoadTime)
	defer stop()
	meta, err := readTaskMeta(instance.runtime, source.File)
	if err != nil {
		return nil, err
	}
	runtime := instance.runtime
	exported := map[string]bool{}
	callable := func(hook moejs.Hook) bool {
		ok, err := runtime.Has(hook)
		return err == nil && ok
	}
	members := func(value moejs.Value) []string {
		if !value.IsObject() {
			return nil
		}
		keys, err := runtime.Realm().EnumerableOwnKeys(value.AsObject())
		if err != nil {
			return nil
		}
		names := make([]string, 0, len(keys))
		for _, key := range keys {
			if text := key.String(); text != nil {
				names = append(names, text.GoString())
			}
		}
		return names
	}
	for _, name := range module.Exports() {
		switch {
		case name == "meta":
		case name == "native":
			value, _ := runtime.Export(name)
			for _, member := range members(value) {
				hook, err := module.Hook(name, member)
				if err == nil && callable(hook) {
					loaded.hooks[name+"."+member] = hook
					exported[name+"."+member] = true
				}
			}
		case name == "protocols":
			value, _ := runtime.Export(name)
			for _, protocol := range members(value) {
				claim, err := runtime.Get(value, protocol)
				if err != nil {
					continue
				}
				for _, member := range members(claim) {
					hook, err := module.Hook(name, protocol, member)
					if err == nil && callable(hook) {
						path := name + "." + protocol + "." + member
						loaded.hooks[path] = hook
						exported[path] = true
					}
				}
			}
		case slices.Contains(taskHooks, name) || slices.Contains(relayHooks, name) || slices.Contains(requestHooks, name):
			hook, err := module.Hook(name)
			if err != nil || !callable(hook) {
				return nil, &LoadError{Code: CodeNotFunction, File: source.File, Message: fmt.Sprintf("the export %q is not a function", name)}
			}
			loaded.hooks[name] = hook
			exported[name] = true
		default:
			exported[name] = true
		}
	}
	key := source.Key
	if key == "" {
		key = meta.Key
	}
	if err := validateTaskMeta(meta, key, exported); err != nil {
		err.(*LoadError).File = source.File
		return nil, err
	}
	loaded.task = meta
	loaded.meta = Meta{APIVersion: 1, Key: meta.Key, Name: meta.Name.In("en"), Version: meta.Version}
	return loaded, nil
}

// readTaskMeta reads a task plugin's manifest as strict JSON. The manifests
// of media plugins list many models and usage profiles, so they may be up to
// 1 MiB.
func readTaskMeta(runtime *moejs.Runtime, file string) (*TaskMeta, error) {
	value, ok := runtime.Export("meta")
	if !ok {
		return nil, &LoadError{Code: CodeMissingMeta, File: file, Message: "the plugin does not export meta"}
	}
	var raw json.RawMessage
	if err := runtime.UnmarshalWith(value, &raw, moejs.DecodeOptions{MaxBytes: 1 << 20, MaxNodes: 1 << 16}); err != nil || len(raw) == 0 {
		return nil, &LoadError{Code: CodeInvalidMeta, File: file, Message: "meta must be plain data of at most 1 MiB"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var meta TaskMeta
	if err := decoder.Decode(&meta); err != nil {
		return nil, &LoadError{Code: CodeInvalidMeta, File: file, Message: cleanText("meta: "+strings.TrimPrefix(err.Error(), "json: "), maxMessageRunes, "")}
	}
	return &meta, nil
}

// newAPISourcePattern finds what a new-api plugin's source has: the task
// part's buildSubmitRequest, meta.relay, or a request processing hook.
var newAPISourcePattern = regexp.MustCompile(`\bbuildSubmitRequest\b|\brelay\s*:|export\s+(?:async\s+)?function\s+(?:ingress|request|outbound|egress|reply|report)\b`)

// ReadTaskSource reads a new-api plugin from a source directory: a
// plugin.js that exports buildSubmitRequest or a request processing hook,
// or lists meta.relay, with no package.json to build it from. Such a plugin
// installs and runs as it is, never built.
func ReadTaskSource(dir string) (string, bool) {
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		return "", false
	}
	text, err := os.ReadFile(filepath.Join(dir, "plugin.js"))
	if err != nil || len(text) > MaxSourceBytes || !newAPISourcePattern.Match(text) {
		return "", false
	}
	return string(text), true
}

// Task returns the manifest of a plugin with a task part, nil for any
// other plugin and for one that did not load.
func (plugin *Plugin) Task() *TaskMeta {
	if plugin.program == nil || plugin.program.task == nil || !plugin.program.task.taskPart {
		return nil
	}
	return plugin.program.task
}

// NewAPI returns the manifest of a plugin written for new-api's plugin API
// v1, whatever its parts, nil for any other plugin and for one that did not
// load.
func (plugin *Plugin) NewAPI() *TaskMeta {
	if plugin.program == nil {
		return nil
	}
	return plugin.program.task
}

// HasTaskPart reports whether a new-api plugin's manifest has a task part.
func (meta *TaskMeta) HasTaskPart() bool { return meta != nil && meta.taskPart }

// HasTaskHook reports whether a task plugin exports the hook at path.
func (plugin *Plugin) HasTaskHook(path string) bool {
	if plugin.program == nil || plugin.program.task == nil {
		return false
	}
	_, ok := plugin.program.hooks[path]
	return ok
}

// CallTask runs the hook of a task plugin at path with args, Go values that
// pass to it as JSON (a json.RawMessage as the value its text holds), and
// returns its result as JSON text, nil for undefined.
func (plugin *Plugin) CallTask(ctx context.Context, path string, args []any, options CallOptions) (json.RawMessage, CallInfo, error) {
	if plugin.program == nil || plugin.program.task == nil {
		return nil, CallInfo{}, &CallError{Reason: ReasonUnavailable, Message: "the plugin is not a task plugin that loaded"}
	}
	hook, ok := plugin.program.hooks[path]
	if !ok {
		return nil, CallInfo{}, &CallError{Reason: ReasonUnavailable, Message: fmt.Sprintf("the plugin does not export %s", path)}
	}
	return plugin.invoke(ctx, path, hook, args, options)
}
