package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

type settingsMap map[string]Settings

func (settings settingsMap) PluginSettings(context.Context) (map[string]Settings, error) {
	return settings, nil
}

type fetcherFunc func(ctx context.Context, plugin *Plugin, request HTTPRequest) HTTPResponse

func (fetch fetcherFunc) Fetch(ctx context.Context, plugin *Plugin, request HTTPRequest) HTTPResponse {
	return fetch(ctx, plugin, request)
}

type memoryStore struct {
	mutex  sync.Mutex
	values map[string]json.RawMessage
}

func (store *memoryStore) Get(_ context.Context, plugin, key string) (json.RawMessage, bool, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	value, found := store.values[plugin+"/"+key]
	return value, found, nil
}

func (store *memoryStore) Set(_ context.Context, plugin, key string, value json.RawMessage) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.values == nil {
		store.values = make(map[string]json.RawMessage)
	}
	store.values[plugin+"/"+key] = value
	return nil
}

func (store *memoryStore) Delete(_ context.Context, plugin, key string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.values, plugin+"/"+key)
	return nil
}

func (store *memoryStore) Keys(_ context.Context, plugin, prefix string) ([]string, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	var keys []string
	for key := range store.values {
		if name, ok := strings.CutPrefix(key, plugin+"/"); ok && strings.HasPrefix(name, prefix) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// pluginSource is a plugin of key asking for capabilities, followed by body.
func pluginSource(key string, capabilities []string, body string) string {
	extra := ""
	if slices.Contains(capabilities, CapabilityNetwork) {
		extra = `, network: { purpose: "test" }, credentials: [{ name: "token", label: "Token", header: "Authorization", scheme: "Bearer" }]`
	}
	names, _ := json.Marshal(capabilities)
	return fmt.Sprintf("export const meta = { apiVersion: 1, key: %q, name: \"Test\", version: \"1.0.0\", capabilities: %s%s };\n%s", key, names, extra, body)
}

func enabled(capabilities ...string) Settings {
	return Settings{Enabled: true, Capabilities: capabilities}
}

func newTestManager(t *testing.T, sources map[string]string, settings settingsMap, services Services, limits Limits) *Manager {
	t.Helper()
	builtin := fstest.MapFS{}
	for key, text := range sources {
		builtin[key+"/plugin.js"] = &fstest.MapFile{Data: []byte(text)}
	}
	manager := NewManager(ManagerOptions{Builtin: builtin, Settings: settings, Services: services, Limits: limits})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	return manager
}

func loadedPlugin(t *testing.T, manager *Manager, key string) *Plugin {
	t.Helper()
	plugin := manager.Current().Plugin(key)
	if plugin == nil || !plugin.Loaded() {
		t.Fatalf("plugin %s = %+v, want it loaded", key, plugin)
	}
	return plugin
}

type callOutcome struct {
	result json.RawMessage
	info   CallInfo
	err    error
}

// callWithin calls hook and fails the test when the call does not return in
// time, so that a missed interrupt fails at once.
func callWithin(t *testing.T, ctx context.Context, plugin *Plugin, hook string, arg any, options CallOptions) callOutcome {
	t.Helper()
	done := make(chan callOutcome, 1)
	go func() {
		result, info, err := plugin.Call(ctx, hook, arg, options)
		done <- callOutcome{result, info, err}
	}()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return", hook)
		return callOutcome{}
	}
}

func callError(t *testing.T, outcome callOutcome, reason, message string) {
	t.Helper()
	var failure *CallError
	if !errors.As(outcome.err, &failure) || failure.Reason != reason || !strings.Contains(failure.Message, message) {
		t.Fatalf("Call = %s, %v; want %s mentioning %q", outcome.result, outcome.err, reason, message)
	}
}

func TestCallReturnsTheHookResult(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", []string{CapabilityInject}, `
export function prepareTurn(turn) {
  return { context: { items: [turn.text.trim()] }, skip: undefined };
}
export function describeStatus() {}
`),
	}, settingsMap{"demo": enabled(CapabilityInject)}, Services{}, Limits{})
	plugin := loadedPlugin(t, manager, "demo")
	if !plugin.Enabled() || !plugin.Has(HookPrepareTurn) || plugin.Has(HookObserveTurn) {
		t.Fatalf("plugin enabled %v, prepareTurn %v, observeTurn %v", plugin.Enabled(), plugin.Has(HookPrepareTurn), plugin.Has(HookObserveTurn))
	}
	outcome := callWithin(t, context.Background(), plugin, HookPrepareTurn, map[string]any{"text": "  hello  "}, CallOptions{})
	if outcome.err != nil || string(outcome.result) != `{"context":{"items":["hello"]}}` {
		t.Fatalf("Call = %s, %v", outcome.result, outcome.err)
	}
	if outcome = callWithin(t, context.Background(), plugin, HookDescribeStatus, nil, CallOptions{}); outcome.err != nil || outcome.result != nil {
		t.Fatalf("describeStatus = %s, %v; want no result", outcome.result, outcome.err)
	}
	outcome = callWithin(t, context.Background(), plugin, HookObserveTurn, nil, CallOptions{})
	callError(t, outcome, ReasonUnavailable, "does not export observeTurn")
}

func TestCallAcceptsTypeScript(t *testing.T) {
	builtin := fstest.MapFS{"demo/plugin.ts": {Data: []byte(`
interface Turn { text: string }
type Block = { open?: string; items: string[] };
export const meta = { apiVersion: 1, key: "demo", name: "Demo", version: "1.0.0", capabilities: ["context.inject"] };
export function prepareTurn(turn: Turn): { context: Block } {
  const items: string[] = [turn.text!.toUpperCase()];
  return { context: { items } satisfies Block };
}
`)}}
	manager := NewManager(ManagerOptions{Builtin: builtin, Settings: settingsMap{"demo": enabled(CapabilityInject)}})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, map[string]any{"text": "ts"}, CallOptions{})
	if outcome.err != nil || string(outcome.result) != `{"context":{"items":["TS"]}}` {
		t.Fatalf("Call = %s, %v", outcome.result, outcome.err)
	}
}

func TestCallStopsAnEndlessLoopAndDropsItsRuntime(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", nil, `
let calls = 0;
export function prepareTurn() { calls++; for (;;) {} }
export function describeStatus() { return calls; }
`),
	}, settingsMap{"demo": enabled()}, Services{}, Limits{ScriptTime: 50 * time.Millisecond, Concurrency: 1})
	plugin := loadedPlugin(t, manager, "demo")
	outcome := callWithin(t, context.Background(), plugin, HookPrepareTurn, nil, CallOptions{})
	callError(t, outcome, ReasonTimeout, "longer than 50ms")
	if outcome.info.Script < 50*time.Millisecond {
		t.Fatalf("script time = %s, want at least the limit", outcome.info.Script)
	}
	// The interrupted runtime is gone: a fresh one has counted no call.
	outcome = callWithin(t, context.Background(), plugin, HookDescribeStatus, nil, CallOptions{})
	if outcome.err != nil || string(outcome.result) != "0" {
		t.Fatalf("describeStatus after the timeout = %s, %v; want a fresh runtime", outcome.result, outcome.err)
	}
}

func TestCallStopsAMemoryHog(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", nil, `
export function prepareTurn() {
  const items = [];
  for (;;) items.push({ index: items.length, text: "item " + items.length });
}
`),
	}, settingsMap{"demo": enabled()}, Services{}, Limits{Memory: 8 << 20, ScriptTime: 5 * time.Second})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{})
	callError(t, outcome, ReasonMemory, "more than 8 MiB")
}

func TestContextDeadlineInterruptsTheCall(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", nil, `export function prepareTurn() { for (;;) {} }`),
	}, settingsMap{"demo": enabled()}, Services{}, Limits{ScriptTime: time.Minute})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	outcome := callWithin(t, ctx, loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{})
	callError(t, outcome, ReasonTimeout, "ran out of time")
}

func TestPluginsGetNodeAndNoOtherHost(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", nil, `
export function prepareTurn() {
  return [typeof require, typeof process, typeof Buffer, typeof XMLHttpRequest, typeof WebSocket, typeof Deno, typeof Bun, typeof __astrlinkHost, typeof __astrlinkNode];
}
export function describeStatus() { return eval("1 + 1"); }
export function testConnection() { return new Function("return 1")(); }
export function observeTurn() { Array.prototype.injected = 1; return [].injected; }
`),
		"other": pluginSource("other", nil, `export function observeTurn() { return [typeof [].injected]; }`),
	}, settingsMap{"demo": enabled(), "other": enabled()}, Services{}, Limits{})
	plugin := loadedPlugin(t, manager, "demo")
	outcome := callWithin(t, context.Background(), plugin, HookPrepareTurn, nil, CallOptions{})
	if outcome.err != nil || string(outcome.result) != `["function","object","function","undefined","undefined","undefined","undefined","undefined","undefined"]` {
		t.Fatalf("globals = %s, %v", outcome.result, outcome.err)
	}
	// Plugins are trusted code, as Node's packages are: eval works, and a
	// plugin may change its own builtins, which no other runtime shares.
	if outcome := callWithin(t, context.Background(), plugin, HookDescribeStatus, nil, CallOptions{}); outcome.err != nil || string(outcome.result) != "2" {
		t.Fatalf("eval = %s, %v", outcome.result, outcome.err)
	}
	if outcome := callWithin(t, context.Background(), plugin, HookTestConnection, nil, CallOptions{}); outcome.err != nil || string(outcome.result) != "1" {
		t.Fatalf("new Function = %s, %v", outcome.result, outcome.err)
	}
	if outcome := callWithin(t, context.Background(), plugin, HookObserveTurn, nil, CallOptions{}); outcome.err != nil || string(outcome.result) != "1" {
		t.Fatalf("a changed builtin = %s, %v", outcome.result, outcome.err)
	}
	if outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "other"), HookObserveTurn, nil, CallOptions{}); outcome.err != nil || string(outcome.result) != `["undefined"]` {
		t.Fatalf("another plugin's builtin = %s, %v", outcome.result, outcome.err)
	}
}

func TestHostFunctionsNeedTheirCapability(t *testing.T) {
	// A plugin has the capabilities it declares, all of them once enabled;
	// this one declares none.
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", nil, `
export function prepareTurn() {
  const messages = [];
  for (const use of [
    () => astrlink.store.get("key"),
    () => astrlink.store.keys(),
    () => astrlink.http.fetch({ method: "GET", url: "https://memory.test/" }),
    () => astrlink.request.info(),
  ]) {
    try { use(); messages.push("allowed"); } catch (error) { messages.push(error.name + ": " + error.message); }
  }
  return messages;
}
`),
	}, settingsMap{"demo": enabled()}, Services{Store: &memoryStore{}, Fetcher: fetcherFunc(func(context.Context, *Plugin, HTTPRequest) HTTPResponse {
		return HTTPResponse{Status: 200}
	})}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: time.Second})
	var messages []string
	if outcome.err != nil || json.Unmarshal(outcome.result, &messages) != nil || len(messages) != 4 {
		t.Fatalf("Call = %s, %v", outcome.result, outcome.err)
	}
	for index, capability := range []string{CapabilityStorage, CapabilityStorage, CapabilityNetwork, CapabilityRequest} {
		if !strings.HasPrefix(messages[index], "TypeError: ") || !strings.Contains(messages[index], "needs the "+capability+" capability") {
			t.Fatalf("message %d = %q, want a TypeError naming %s", index, messages[index], capability)
		}
	}
}

func TestHostFunctionsWorkOnlyDuringAHook(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", nil, `
const started = astrlink.clock.now();
export function prepareTurn() { return started; }
`),
	}, nil, Services{}, Limits{})
	plugin := manager.Current().Plugin("demo")
	if plugin == nil || plugin.Loaded() || plugin.Err == nil || plugin.Err.Code != CodeLoadFailed || plugin.Err.Line != 3 || !strings.Contains(plugin.Err.Message, "works only while the host runs a hook") {
		t.Fatalf("plugin = %+v, want a load failure on line 3", plugin)
	}
}

func TestLoadChecksTheModule(t *testing.T) {
	original := loadTime
	loadTime = 100 * time.Millisecond
	defer func() { loadTime = original }()
	meta := `export const meta = { apiVersion: 1, key: "demo", name: "Demo", version: "1.0.0", capabilities: [] };` + "\n"
	for _, test := range []struct {
		name, source, code, message string
		line                        int
	}{
		{"no manifest", "export function prepareTurn() {}", CodeMissingMeta, "does not export meta", 0},
		{"misspelt field", `export const meta = { apiVersion: 1, key: "demo", name: "Demo", version: "1.0.0", capabilites: [] };`, CodeInvalidMeta, `unknown field "capabilites"`, 0},
		{"manifest of another plugin", `export const meta = { apiVersion: 1, key: "other", name: "Demo", version: "1.0.0", capabilities: [] };`, CodeInvalidMeta, "does not match", 0},
		{"unknown export", meta + "export function helper() {}", CodeUnknownExport, `"helper"`, 0},
		{"default export", meta + "export default function () {}", CodeUnknownExport, `"default"`, 0},
		{"hook that is not a function", meta + "export const prepareTurn = 1;", CodeNotFunction, `"prepareTurn"`, 0},
		{"top level that throws", meta + "\nthrow new RangeError(\"boom\");", CodeLoadFailed, "threw RangeError: boom", 3},
		{"top level that never ends", meta + "for (;;) {}", CodeLoadFailed, "ran longer than 100ms", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := newTestManager(t, map[string]string{"demo": test.source}, nil, Services{}, Limits{})
			plugin := manager.Current().Plugin("demo")
			if plugin == nil || plugin.Loaded() || plugin.Err == nil {
				t.Fatalf("plugin = %+v, want a load error", plugin)
			}
			if plugin.Err.Code != test.code || !strings.Contains(plugin.Err.Message, test.message) || plugin.Err.Line != test.line || plugin.Err.File != "plugin.js" {
				t.Fatalf("load error = %+v, want %s mentioning %q on line %d", plugin.Err, test.code, test.message, test.line)
			}
		})
	}
}

func TestCallErrorsAreSafeToShow(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", nil, `
export function prepareTurn() {
  throw new Error("boom\u001b[31m" + "x".repeat(2000));
}
export function describeStatus() { return "x".repeat(70000); }
`),
	}, settingsMap{"demo": enabled()}, Services{}, Limits{})
	plugin := loadedPlugin(t, manager, "demo")
	outcome := callWithin(t, context.Background(), plugin, HookPrepareTurn, nil, CallOptions{})
	callError(t, outcome, ReasonPluginError, "threw Error: boom[31mxxx")
	if message := outcome.err.(*CallError).Message; len([]rune(message)) > maxMessageRunes+1 || strings.ContainsRune(message, 0x1b) {
		t.Fatalf("message of %d runes = %q", len([]rune(message)), message)
	}
	callError(t, callWithin(t, context.Background(), plugin, HookDescribeStatus, nil, CallOptions{}), ReasonInvalidResult, "larger than 64 KiB")
}

func TestThrownErrorsNameTheirPosition(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", nil, "export function prepareTurn() {\n  return missing.value;\n}\nexport function describeStatus() {\n  return astrlink.store.get(\"x\");\n}\n"),
	}, settingsMap{"demo": enabled()}, Services{}, Limits{})
	plugin := loadedPlugin(t, manager, "demo")
	callError(t, callWithin(t, context.Background(), plugin, HookPrepareTurn, nil, CallOptions{}), ReasonPluginError, "plugin.js:3:")
	// The host function that threw sits above the plugin's own frame.
	callError(t, callWithin(t, context.Background(), plugin, HookDescribeStatus, nil, CallOptions{}), ReasonPluginError, "plugin.js:6:")
}

func TestWaitingOnTheHostDoesNotCountAsScriptTime(t *testing.T) {
	var seen []HTTPRequest
	var mutex sync.Mutex
	fetcher := fetcherFunc(func(ctx context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
		mutex.Lock()
		seen = append(seen, request)
		mutex.Unlock()
		select {
		case <-time.After(300 * time.Millisecond):
			return HTTPResponse{Status: 200, Header: map[string]string{"content-type": "application/json"}, Body: []byte(`{"package_version":"1.3.0"}`), Took: 300 * time.Millisecond}
		case <-ctx.Done():
			return HTTPResponse{Error: FetchTimeout}
		}
	})
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", []string{CapabilityNetwork}, `
export function prepareTurn() {
  const response = astrlink.http.fetch({ method: "GET", url: "https://memory.test/v1/server-info", credential: "token", timeoutMs: 1000 });
  return { status: response.status, version: response.json.package_version, took: response.took_ms, type: response.headers["content-type"] };
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcher}, Limits{ScriptTime: 100 * time.Millisecond})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: 2 * time.Second})
	if outcome.err != nil || string(outcome.result) != `{"status":200,"version":"1.3.0","took":300,"type":"application/json"}` {
		t.Fatalf("Call = %s, %v", outcome.result, outcome.err)
	}
	if outcome.info.Script >= 100*time.Millisecond || outcome.info.Network.Wait < 300*time.Millisecond || outcome.info.Network.Requests != 1 {
		t.Fatalf("CallInfo = %+v, want the wait outside script time", outcome.info)
	}
	if len(seen) != 1 || seen[0].Credential != "token" || seen[0].TimeoutMS != 1000 {
		t.Fatalf("fetched %+v", seen)
	}
}

func TestFetchRunsBatchesInParallelWithinItsBudgets(t *testing.T) {
	var sentHeaders sync.Map
	fetcher := fetcherFunc(func(ctx context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
		sentHeaders.Store(request.URL, request.Headers)
		delay := 200 * time.Millisecond
		if strings.HasSuffix(request.URL, "/slow") {
			delay = time.Minute
		}
		select {
		case <-time.After(delay):
			return HTTPResponse{Status: 200, Body: []byte(strings.TrimPrefix(request.URL, "https://memory.test/"))}
		case <-ctx.Done():
			return HTTPResponse{Error: FetchTimeout}
		}
	})
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", []string{CapabilityNetwork}, `
const get = (path, extra) => ({ method: "GET", url: "https://memory.test/" + path, ...extra });
export function prepareTurn() {
  const batch = astrlink.http.fetch(["a", "b", "c", "d", "e", "f", "g", "h"].map((path) => get(path)));
  const own = astrlink.http.fetch(get("own", { headers: { Authorization: "Bearer own", Host: "elsewhere.test", "X-AstrLink-Meta-Project": "secret" } }));
  const more = astrlink.http.fetch(["i", "j", "k", "l", "m", "n", "o", "p"].map((path) => get(path)));
  const over = astrlink.http.fetch(get("q"));
  return { batch: batch.map((response) => response.text), own: own.text, more: more.map((response) => response.text), over: over.error };
}
export function observeTurn() {
  const slow = astrlink.http.fetch(get("slow"));
  const after = astrlink.http.fetch(get("after"));
  return [slow.error, after.error];
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcher}, Limits{})
	plugin := loadedPlugin(t, manager, "demo")
	started := time.Now()
	outcome := callWithin(t, context.Background(), plugin, HookPrepareTurn, nil, CallOptions{NetworkBudget: 10 * time.Second})
	if outcome.err != nil {
		t.Fatalf("Call: %v", outcome.err)
	}
	if elapsed := time.Since(started); elapsed >= 1500*time.Millisecond {
		t.Fatalf("two batches of eight 200 ms requests took %s, want them parallel", elapsed)
	}
	// A call sends at most 16 requests; the seventeenth answers budget.
	want := `{"batch":["a","b","c","d","e","f","g","h"],"own":"own","more":["i","j","k","l","m","n","o",null],"over":"budget"}`
	if string(outcome.result) != want {
		t.Fatalf("Call = %s\nwant %s", outcome.result, want)
	}
	// A plugin sets its own Authorization; the host keeps the transport's
	// headers and its own.
	headers, _ := sentHeaders.Load("https://memory.test/own")
	if got := headers.(map[string]string); got["Authorization"] != "Bearer own" || got["Host"] != "" || got["X-AstrLink-Meta-Project"] != "" {
		t.Fatalf("headers sent = %v", got)
	}
	if network := outcome.info.Network; network.Requests != 18 || network.Denied != 0 || network.Errors != 2 {
		t.Fatalf("NetworkStats = %+v", network)
	}
	outcome = callWithin(t, context.Background(), plugin, HookObserveTurn, nil, CallOptions{NetworkBudget: 300 * time.Millisecond})
	if outcome.err != nil || string(outcome.result) != `["budget","budget"]` {
		t.Fatalf("observeTurn past the network budget = %s, %v", outcome.result, outcome.err)
	}
}

func TestFetchChecksTheRequestShape(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", []string{CapabilityNetwork}, `
const base = { method: "POST", url: "https://memory.test/v1/sources/content" };
export function prepareTurn() {
  const messages = [];
  for (const request of [
    { ...base, method: "TRACE" },
    { ...base, url: "file:///etc/passwd" },
    { ...base, url: "https://user:secret@memory.test/" },
    { ...base, json: { a: 1 }, body: "a" },
    { ...base, method: "GET", json: {} },
    { ...base, credential: "undeclared" },
    { ...base, timeoutMs: 60000 },
    { ...base, headers: { "Bad Header": "x" } },
    { ...base, extra: true },
    { ...base, body: "x".repeat(1100000) },
    { ...base, get url() { return "https://memory.test/"; } },
    Array.from({ length: 9 }, () => base),
  ]) {
    try { astrlink.http.fetch(request); messages.push("sent"); } catch (error) { messages.push(error.name); }
  }
  return messages;
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcherFunc(func(context.Context, *Plugin, HTTPRequest) HTTPResponse {
		return HTTPResponse{Status: 200}
	})}, Limits{Memory: 64 << 20, ScriptTime: 10 * time.Second}) // builds a 1 MiB body; the shape is what is under test
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: time.Second})
	want := `["TypeError","TypeError","TypeError","TypeError","TypeError","TypeError","TypeError","TypeError","TypeError","TypeError","TypeError","RangeError"]`
	if outcome.err != nil || string(outcome.result) != want {
		t.Fatalf("Call = %s, %v\nwant %s", outcome.result, outcome.err, want)
	}
}

func TestStoreKeepsJSONValues(t *testing.T) {
	store := &memoryStore{}
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", []string{CapabilityStorage}, `
export function prepareTurn() {
  astrlink.store.set("scope:astrlink", "scp_1");
  astrlink.store.set("scope:paseo", "scp_2");
  astrlink.store.set("server", { version: "1.3.0", at: 1 });
  const before = astrlink.store.keys("scope:");
  astrlink.store.delete("scope:paseo");
  const errors = [];
  for (const misuse of [() => astrlink.store.set("big", "x".repeat(70000)), () => astrlink.store.set("nothing"), () => astrlink.store.get("")]) {
    try { misuse(); errors.push("allowed"); } catch (error) { errors.push(error.name); }
  }
  return { before, after: astrlink.store.keys(), server: astrlink.store.get("server"), missing: astrlink.store.get("missing"), errors };
}
`),
	}, settingsMap{"demo": enabled(CapabilityStorage)}, Services{Store: store}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{})
	// Stored objects come back with their keys sorted, as JSON keeps them.
	want := `{"before":["scope:astrlink","scope:paseo"],"after":["scope:astrlink","server"],"server":{"at":1,"version":"1.3.0"},"missing":null,"errors":["RangeError","TypeError","RangeError"]}`
	if outcome.err != nil || string(outcome.result) != want {
		t.Fatalf("Call = %s, %v\nwant %s", outcome.result, outcome.err, want)
	}
	if value, _, _ := store.Get(context.Background(), "demo", "server"); string(value) != `{"at":1,"version":"1.3.0"}` {
		t.Fatalf("stored %s", value)
	}
}

// valueStats answers astrlink.meta.values with two values and keeps the
// limits it was asked for.
type valueStats struct{ limits []int }

func (stats *valueStats) ValueStats(_ context.Context, key string, limit int) ([]ValueStat, error) {
	stats.limits = append(stats.limits, limit)
	return []ValueStat{{Value: key + "-a", Sessions: 3, LastUsedAt: 1760000000000}, {Value: key + "-b", Sessions: 1}}, nil
}

func TestMetaValuesReadOnlyThePluginsOwnAttributes(t *testing.T) {
	stats := &valueStats{}
	source := `export const meta = { apiVersion: 1, key: "demo", name: "Test", version: "1.0.0", capabilities: ["request.read"],
  metadata: [{ key: "project", label: "Project", pattern: "^[a-z-]+$" }] };
export function prepareTurn() {
  const errors = [];
  for (const misuse of [() => astrlink.meta.values("env"), () => astrlink.meta.values("project", 0), () => astrlink.meta.values("project", 201), () => astrlink.meta.values()]) {
    try { misuse(); errors.push("allowed"); } catch (error) { errors.push(error.name); }
  }
  return { all: astrlink.meta.values("project"), few: astrlink.meta.values("project", 5).length, errors };
}
`
	manager := newTestManager(t, map[string]string{"demo": source}, settingsMap{"demo": enabled(CapabilityRequest)}, Services{Metadata: stats}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{})
	want := `{"all":[{"value":"project-a","sessions":3,"lastUsedAt":1760000000000},{"value":"project-b","sessions":1,"lastUsedAt":0}],"few":2,"errors":["RangeError","RangeError","RangeError","TypeError"]}`
	if outcome.err != nil || string(outcome.result) != want {
		t.Fatalf("Call = %s, %v\nwant %s", outcome.result, outcome.err, want)
	}
	if len(stats.limits) != 2 || stats.limits[0] != defaultMetaValues || stats.limits[1] != 5 {
		t.Fatalf("limits = %v", stats.limits)
	}
	// Without request.read declared the values stay out of reach.
	manager = newTestManager(t, map[string]string{"demo": strings.Replace(source, `capabilities: ["request.read"]`, `capabilities: []`, 1)}, settingsMap{"demo": enabled()}, Services{Metadata: stats}, Limits{})
	outcome = callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{})
	callError(t, outcome, ReasonPluginError, "needs the request.read capability")
}

func TestConfigIsAReadOnlyCopy(t *testing.T) {
	settings := enabled()
	settings.Config = json.RawMessage(`{"serverUrl":"http://127.0.0.1:9090","quotas":{"skill":2}}`)
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", nil, `
export function prepareTurn() {
  const config = astrlink.config.get();
  config.quotas.skill = 99;
  return [config.serverUrl, astrlink.config.get().quotas.skill, typeof astrlink.config.set];
}
`),
	}, settingsMap{"demo": settings}, Services{}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{})
	if outcome.err != nil || string(outcome.result) != `["http://127.0.0.1:9090",2,"undefined"]` {
		t.Fatalf("Call = %s, %v", outcome.result, outcome.err)
	}
}

func TestLogKeepsBoundedCleanLines(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", []string{CapabilityRequest}, `
export function prepareTurn() {
  astrlink.log("warn", "red\u001b[31m text");
  astrlink.log("info", "长".repeat(600));
  for (let index = 0; index < 23; index++) astrlink.log("debug", "line " + index);
  try { astrlink.log("error", "no such level"); } catch (error) { return [error.name, astrlink.request.info()]; }
}
`),
	}, settingsMap{"demo": enabled(CapabilityRequest)}, Services{}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{})
	if outcome.err != nil || string(outcome.result) != `["TypeError",null]` {
		t.Fatalf("Call = %s, %v", outcome.result, outcome.err)
	}
	logs := outcome.info.Logs
	if len(logs) != maxLogEntries || outcome.info.DroppedLogs != 5 {
		t.Fatalf("kept %d logs, dropped %d", len(logs), outcome.info.DroppedLogs)
	}
	if logs[0].Level != "warn" || logs[0].Message != "red[31m text" || len(logs[1].Message) > maxLogBytes || logs[2].Message != "line 0" {
		t.Fatalf("logs = %q / %d bytes / %q", logs[0].Message, len(logs[1].Message), logs[2].Message)
	}
}

func TestRequestInfoGivesTheRequestWithoutItsBody(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", []string{CapabilityRequest}, `export function prepareTurn() { return astrlink.request.info(); }`),
	}, settingsMap{"demo": enabled(CapabilityRequest)}, Services{}, Limits{})
	info := &RequestInfo{Protocol: "anthropic.messages", Model: "claude-opus-4-5", Client: "claude_code", Stream: true,
		Meta: map[string]MetaValue{"project": {Value: "astrlink", Source: "session"}}, Session: &SessionRef{ID: "sess_1", Turn: 3}}
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{Request: info})
	want := `{"protocol":"anthropic.messages","model":"claude-opus-4-5","client":"claude_code","stream":true,"meta":{"project":{"value":"astrlink","source":"session"}},"session":{"id":"sess_1","turn":3}}`
	if outcome.err != nil || string(outcome.result) != want {
		t.Fatalf("Call = %s, %v", outcome.result, outcome.err)
	}
}

func TestRefreshKeepsWhatDidNotChange(t *testing.T) {
	settings := settingsMap{"alpha": enabled(), "beta": enabled()}
	sources := map[string]string{
		"alpha": pluginSource("alpha", nil, "export function prepareTurn() { return 1; }"),
		"beta":  pluginSource("beta", nil, "export function prepareTurn() { return 2; }"),
	}
	manager := newTestManager(t, sources, settings, Services{}, Limits{})
	alpha, beta := manager.Current().Plugin("alpha"), manager.Current().Plugin("beta")
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.Current().Plugin("alpha") != alpha || manager.Current().Plugin("beta") != beta {
		t.Fatal("a reload without changes replaced the plugins")
	}
	settings["beta"] = Settings{Enabled: false}
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	current := manager.Current()
	if current.Plugin("alpha") != alpha || current.Plugin("beta") == beta || current.Plugin("beta").Enabled() {
		t.Fatal("Refresh did not replace only the plugin whose settings changed")
	}
	if current.Plugin("beta").program != beta.program {
		t.Fatal("Refresh recompiled an unchanged source")
	}
	if enabled := current.Enabled(HookPrepareTurn); len(enabled) != 1 || enabled[0].Key != "alpha" {
		t.Fatalf("Enabled = %v", enabled)
	}
}

func TestACallInFlightFinishesOnItsGeneration(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	fetcher := fetcherFunc(func(ctx context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
		if strings.HasSuffix(request.URL, "/block") {
			close(entered)
			<-release
		}
		return HTTPResponse{Status: 200}
	})
	settings := enabled(CapabilityNetwork)
	settings.Config = json.RawMessage(`{"mark":"old"}`)
	all := settingsMap{"demo": settings}
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", []string{CapabilityNetwork}, `
export function prepareTurn(path) {
  astrlink.http.fetch({ method: "GET", url: "https://memory.test/" + path });
  return astrlink.config.get().mark;
}
`),
	}, all, Services{Fetcher: fetcher}, Limits{})
	old := loadedPlugin(t, manager, "demo")
	done := make(chan callOutcome, 1)
	go func() {
		result, info, err := old.Call(context.Background(), HookPrepareTurn, "block", CallOptions{NetworkBudget: time.Minute})
		done <- callOutcome{result, info, err}
	}()
	select {
	case <-entered:
	case outcome := <-done:
		t.Fatalf("the call returned before it fetched: %s, %v", outcome.result, outcome.err)
	case <-time.After(5 * time.Second):
		t.Fatal("the call never fetched")
	}
	updated := enabled(CapabilityNetwork)
	updated.Config = json.RawMessage(`{"mark":"new"}`)
	all["demo"] = updated
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, "free", CallOptions{NetworkBudget: time.Minute})
	if outcome.err != nil || string(outcome.result) != `"new"` {
		t.Fatalf("call on the new generation = %s, %v", outcome.result, outcome.err)
	}
	close(release)
	select {
	case outcome := <-done:
		if outcome.err != nil || string(outcome.result) != `"old"` {
			t.Fatalf("call in flight = %s, %v; want the settings it started with", outcome.result, outcome.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call in flight did not finish")
	}
}

func TestConcurrentCallsShareThePool(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSource("demo", []string{CapabilityStorage}, `
let calls = 0;
export function prepareTurn(index) {
  calls++;
  astrlink.store.set("call:" + index, index);
  let total = 0;
  for (let step = 0; step < 10000; step++) total += step;
  return { index, total, stored: astrlink.store.get("call:" + index) };
}
`),
	}, settingsMap{"demo": enabled(CapabilityStorage)}, Services{Store: &memoryStore{}}, Limits{Concurrency: 2})
	plugin := loadedPlugin(t, manager, "demo")
	var group sync.WaitGroup
	failures := make(chan string, 64)
	for index := range 64 {
		group.Go(func() {
			result, _, err := plugin.Call(context.Background(), HookPrepareTurn, index, CallOptions{})
			if want := fmt.Sprintf(`{"index":%d,"total":49995000,"stored":%d}`, index, index); err != nil || string(result) != want {
				failures <- fmt.Sprintf("call %d = %s, %v", index, result, err)
			}
		})
	}
	group.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if idle := len(plugin.pool.idle); idle > 2 {
		t.Fatalf("%d idle runtimes kept, want at most the concurrency", idle)
	}
}
