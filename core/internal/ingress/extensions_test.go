package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/tidwall/gjson"
)

// extensionPlugin is an apiVersion 2 plugin of key asking for capabilities.
func extensionPlugin(key string, capabilities []string, extra, body string) string {
	names, _ := json.Marshal(capabilities)
	return `export const meta = { apiVersion: 2, key: "` + key + `", name: "` + strings.ToUpper(key[:1]) + key[1:] + `", version: "1.0.0", capabilities: ` + string(names) + extra + ` };
` + body
}

type sentAttempt struct {
	provider string
	body     []byte
	header   http.Header
}

type extensionGateway struct {
	t       *testing.T
	handler *Handler
	manager *plugin.Manager
	store   *memoryRequestRecordStore
	mutex   sync.Mutex
	sent    []sentAttempt
	reply   string
}

func twoProviders() []endpoint.Resolved {
	alpha := validEndpoint(contract.ProtocolOpenAIChat, true)
	alpha.ID, alpha.Name = "endpoint_alpha", "Alpha"
	beta := validEndpoint(contract.ProtocolOpenAIChat, true)
	beta.ID, beta.Name = "endpoint_beta", "Beta"
	return []endpoint.Resolved{{Endpoint: alpha}, {Endpoint: beta}}
}

func newExtensionGateway(t *testing.T, sources map[string]string, settings pluginSettings, order ...string) *extensionGateway {
	t.Helper()
	files := fstest.MapFS{}
	for key, source := range sources {
		files[key+"/plugin.js"] = &fstest.MapFile{Data: []byte(source)}
	}
	manager := plugin.NewManager(plugin.ManagerOptions{Builtin: files, Settings: settings})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	for key := range sources {
		if loaded := manager.Current().Plugin(key); !loaded.Loaded() {
			t.Fatalf("plugin %s did not load: %v", key, loaded.Err)
		}
	}
	host := pluginhost.New(pluginhost.Options{Plugins: manager, Logf: t.Logf})
	t.Cleanup(host.Close)
	if len(order) > 0 {
		if _, err := host.UpdateSettings(context.Background(), contract.PluginHostSettingsUpdate{Order: &order}); err != nil {
			t.Fatal(err)
		}
	}
	gateway := &extensionGateway{t: t, manager: manager, store: &memoryRequestRecordStore{}, reply: `{"id":"r","choices":[{"message":{"role":"assistant","content":"ok"}}]}`}
	gateway.handler = NewWithDependencies(Dependencies{
		Resolver:       candidateResolver{candidates: twoProviders()},
		RequestRecords: gateway.store,
		AuditBlobs:     &memoryAuditBlobs{},
		Plugins:        host,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, target transport.Target) error {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if request.ContentLength != int64(len(body)) {
				t.Errorf("the request declares %d bytes and carries %d", request.ContentLength, len(body))
			}
			gateway.mutex.Lock()
			gateway.sent = append(gateway.sent, sentAttempt{provider: target.Service.Name, body: body, header: request.Header.Clone()})
			reply := gateway.reply
			gateway.mutex.Unlock()
			contentType := "application/json"
			if strings.HasPrefix(reply, "data:") || strings.HasPrefix(reply, "event:") {
				contentType = "text/event-stream"
			}
			writer.Header().Set("Content-Type", contentType)
			writer.WriteHeader(http.StatusOK)
			_, err = writer.Write([]byte(reply))
			return err
		}),
	})
	return gateway
}

// send sends a Chat Completions request and returns the client's response
// and the request's record.
func (gateway *extensionGateway) send(body string) (*httptest.ResponseRecorder, contract.RequestRecord) {
	gateway.t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	gateway.handler.ServeHTTP(recorder, request)
	record := latestRoot(gateway.t, gateway.store)
	if err := record.Validate(); err != nil {
		gateway.t.Fatalf("the record does not validate: %v", err)
	}
	return recorder, record
}

func (gateway *extensionGateway) last() sentAttempt {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	if len(gateway.sent) == 0 {
		gateway.t.Fatal("nothing went upstream")
	}
	return gateway.sent[len(gateway.sent)-1]
}

func hookRecord(t *testing.T, record contract.RequestRecord, key, hook string) contract.RequestPluginRecord {
	t.Helper()
	for _, entry := range record.PluginRecords {
		if entry.Plugin == key && entry.Hook == hook {
			return entry
		}
	}
	t.Fatalf("no record of %s %s in %+v", key, hook, record.PluginRecords)
	return contract.RequestPluginRecord{}
}

const extensionChatBody = `{"model":"gpt-test","messages":[{"role":"system","content":"Be nice."},{"role":"user","content":"%s"}],"tools":[{"type":"function","function":{"name":"old","parameters":{"type":"object"}}}]}`

func chat(text string) string { return strings.Replace(extensionChatBody, "%s", text, 1) }

func TestEntryHooksTagAndRefuseRequests(t *testing.T) {
	gate := extensionPlugin("gate", []string{plugin.CapabilityRequestGate, plugin.CapabilityRequest}, "", `
export async function onRequest(ctx) {
  const said = ctx.body.messages.at(-1).content;
  if (said === "forbidden") return { action: "deny", status: 451, code: "policy_word", message: "Not that word." };
  return { action: "allow", tags: ["checked", ctx.request.protocol] };
}
`)
	gateway := newExtensionGateway(t, map[string]string{"gate": gate}, pluginSettings{
		"gate": {Enabled: true, Capabilities: []string{plugin.CapabilityRequestGate, plugin.CapabilityRequest}},
	})
	response, record := gateway.send(chat("forbidden"))
	if response.Code != 451 || gjson.Get(response.Body.String(), "error.code").Str != "policy_word" || gjson.Get(response.Body.String(), "error.message").Str != "Not that word." {
		t.Fatalf("refused = %d %s", response.Code, response.Body.String())
	}
	if len(gateway.sent) != 0 {
		t.Fatal("a refused request went upstream")
	}
	if entry := hookRecord(t, record, "gate", plugin.HookOnRequest); !slices.Equal(entry.Effects, []string{contract.PluginEffectDeny}) || entry.Summary != "Not that word." {
		t.Fatalf("record = %+v", entry)
	}
	response, record = gateway.send(chat("hello"))
	if response.Code != http.StatusOK {
		t.Fatalf("allowed = %d %s", response.Code, response.Body.String())
	}
	if entry := hookRecord(t, record, "gate", plugin.HookOnRequest); !slices.Equal(entry.Tags, []string{"checked", "openai.chat"}) || entry.Status != contract.PluginRecordOK {
		t.Fatalf("record = %+v", entry)
	}
}

func TestEntryHooksThatFailLetRequestsInUnlessTheyFailClosed(t *testing.T) {
	broken := extensionPlugin("broken", []string{plugin.CapabilityRequestGate}, `, failClosed: true`, `
export async function onRequest() { throw new Error("database down"); }
`)
	for _, closed := range []bool{false, true} {
		gateway := newExtensionGateway(t, map[string]string{"broken": broken}, pluginSettings{
			"broken": {Enabled: true, Capabilities: []string{plugin.CapabilityRequestGate}, FailClosed: closed},
		})
		response, record := gateway.send(chat("hello"))
		entry := hookRecord(t, record, "broken", plugin.HookOnRequest)
		if entry.Status != contract.PluginRecordFailed || !strings.Contains(entry.Summary, "database down") {
			t.Fatalf("record = %+v", entry)
		}
		want := http.StatusOK
		if closed {
			want = http.StatusServiceUnavailable
		}
		if response.Code != want {
			t.Fatalf("fail closed %v: status %d %s", closed, response.Code, response.Body.String())
		}
	}
}

func TestBreakerKeepsAFailingHookOut(t *testing.T) {
	broken := extensionPlugin("broken", nil, "", `
export async function onRequest() { throw new Error("always"); }
`)
	gateway := newExtensionGateway(t, map[string]string{"broken": broken}, pluginSettings{"broken": {Enabled: true}})
	for range 5 {
		gateway.send(chat("hello"))
	}
	_, record := gateway.send(chat("hello"))
	if entry := hookRecord(t, record, "broken", plugin.HookOnRequest); entry.Status != contract.PluginRecordSkipped || entry.SkippedReason != "circuit_open" {
		t.Fatalf("record after five failures = %+v", entry)
	}
}

func TestRouteHooksReorderExcludeAndChangeTheModel(t *testing.T) {
	router := extensionPlugin("router", []string{plugin.CapabilityRouting}, "", `
export async function route(ctx) {
  const said = ctx.request.model;
  const beta = ctx.candidates.find((candidate) => candidate.name === "Beta");
  if (said === "exclude-all") return { exclude: ctx.candidates.map((candidate) => candidate.id) };
  if (said === "switch") return { model: "cheaper-model", reason: "after hours" };
  return { order: [beta.id], reason: "Beta is cheaper" };
}
`)
	gateway := newExtensionGateway(t, map[string]string{"router": router}, pluginSettings{
		"router": {Enabled: true, Capabilities: []string{plugin.CapabilityRouting}},
	})
	_, record := gateway.send(chat("hello"))
	if gateway.last().provider != "Beta" {
		t.Fatalf("went to %s", gateway.last().provider)
	}
	if entry := hookRecord(t, record, "router", plugin.HookRoute); !slices.Equal(entry.Effects, []string{contract.PluginEffectOrder}) || entry.Summary != "Beta is cheaper" {
		t.Fatalf("record = %+v", entry)
	}
	_, record = gateway.send(strings.Replace(chat("hello"), "gpt-test", "exclude-all", 1))
	if gateway.last().provider != "Alpha" || hookRecord(t, record, "router", plugin.HookRoute).SkippedReason != "would_exclude_all" {
		t.Fatalf("excluding every provider went to %s", gateway.last().provider)
	}
	_, record = gateway.send(strings.Replace(chat("hello"), "gpt-test", "switch", 1))
	if model := gjson.GetBytes(gateway.last().body, "model").Str; model != "cheaper-model" {
		t.Fatalf("the upstream got model %q", model)
	}
	if entry := hookRecord(t, record, "router", plugin.HookRoute); !slices.Contains(entry.Effects, contract.PluginEffectModel) {
		t.Fatalf("record = %+v", entry)
	}
	// Without routing declared, route does not run.
	undeclared := strings.Replace(router, `capabilities: ["routing"]`, `capabilities: []`, 1)
	gateway = newExtensionGateway(t, map[string]string{"router": undeclared}, pluginSettings{"router": {Enabled: true}})
	if _, record := gateway.send(chat("hello")); gateway.last().provider != "Alpha" || len(record.PluginRecords) != 0 {
		t.Fatalf("an undeclared route ran: %+v", record.PluginRecords)
	}
}

func TestBeforeForwardEditsTheRequestInOrder(t *testing.T) {
	editor := extensionPlugin("editor", []string{plugin.CapabilityRequestWrite}, "", `
export async function beforeForward() {
  return {
    system: { append: "Answer briefly." },
    turn: { prepend: "[context]" },
    params: { temperature: 0.1, max_tokens: 64, unknown: 1 },
    tools: { add: [{ name: "lookup", description: "Look a fact up", parameters: { type: "object", properties: { q: { type: "string" } } } }], remove: ["old"] },
  };
}
`)
	raw := extensionPlugin("raw", []string{plugin.CapabilityRequestWriteRaw, plugin.CapabilityRequest}, "", `
export async function beforeForward(ctx) {
  return { body: { ...ctx.body, metadata: { edited: "yes", saw: ctx.body.messages.length } } };
}
`)
	gateway := newExtensionGateway(t, map[string]string{"editor": editor, "raw": raw}, pluginSettings{
		"editor": {Enabled: true, Capabilities: []string{plugin.CapabilityRequestWrite}},
		"raw":    {Enabled: true, Capabilities: []string{plugin.CapabilityRequestWriteRaw, plugin.CapabilityRequest}},
	}, "raw", "editor")
	_, record := gateway.send(chat("hello"))
	sent := gateway.last().body
	checks := map[string]string{
		"messages.0.content":    "Be nice.\n\nAnswer briefly.",
		"temperature":           "0.1",
		"max_tokens":            "64",
		"tools.#":               "1",
		"tools.0.function.name": "lookup",
		"metadata.edited":       "yes",
		"metadata.saw":          "2",
		"tools.0.function.parameters.properties.q.type": "string",
	}
	for path, want := range checks {
		if got := gjson.GetBytes(sent, path).String(); got != want {
			t.Errorf("%s = %q, want %q in %s", path, got, want, sent)
		}
	}
	if user := gjson.GetBytes(sent, "messages.1.content").String(); !strings.HasPrefix(user, "[context]") || !strings.HasSuffix(user, "hello") {
		t.Errorf("the turn = %q", user)
	}
	if entry := hookRecord(t, record, "editor", plugin.HookBeforeForward); !slices.Equal(entry.Effects, []string{"system", "turn", "params", "tools"}) {
		t.Fatalf("editor record = %+v", entry)
	}
	if entry := hookRecord(t, record, "raw", plugin.HookBeforeForward); !slices.Equal(entry.Effects, []string{"body"}) {
		t.Fatalf("raw record = %+v", entry)
	}
	// A raw body without request.write.raw declared is left out.
	undeclared := strings.Replace(raw, `capabilities: ["request.write.raw","request.read"]`, `capabilities: ["request.read"]`, 1)
	gateway = newExtensionGateway(t, map[string]string{"raw": undeclared}, pluginSettings{"raw": {Enabled: true}})
	if _, record := gateway.send(chat("hello")); len(record.PluginRecords) != 0 || gjson.GetBytes(gateway.last().body, "metadata").Exists() {
		t.Fatalf("a raw edit without the capability: %s %+v", gateway.last().body, record.PluginRecords)
	}
}

func TestBeforeUpstreamEditsEachAttempt(t *testing.T) {
	upstream := extensionPlugin("upstream", []string{plugin.CapabilityRequestWriteRaw}, "", `
export async function beforeUpstream(ctx) {
  return {
    body: { ...ctx.body, user: "u-" + ctx.attempt.provider + "-" + ctx.attempt.protocol },
    headers: { "X-Trace": "t1", "Authorization": "Bearer stolen" },
  };
}
`)
	gateway := newExtensionGateway(t, map[string]string{"upstream": upstream}, pluginSettings{
		"upstream": {Enabled: true, Capabilities: []string{plugin.CapabilityRequestWriteRaw}},
	})
	_, record := gateway.send(chat("hello"))
	sent := gateway.last()
	if user := gjson.GetBytes(sent.body, "user").Str; user != "u-Alpha-openai.chat" {
		t.Fatalf("user = %q in %s", user, sent.body)
	}
	if sent.header.Get("X-Trace") != "t1" || sent.header.Get("Authorization") == "Bearer stolen" {
		t.Fatalf("headers = %v", sent.header)
	}
	if entry := hookRecord(t, record, "upstream", plugin.HookBeforeUpstream); entry.SkippedReason != "header_denied" || !slices.Equal(entry.Effects, []string{"body", "headers"}) {
		t.Fatalf("record = %+v", entry)
	}
}

func TestRewriteResponseChangesAWholeResponse(t *testing.T) {
	polish := extensionPlugin("polish", []string{plugin.CapabilityResponseWrite}, "", `
export async function rewriteResponse(ctx) {
  const body = structuredClone(ctx.response.body);
  body.choices[0].message.content = body.choices[0].message.content.toUpperCase() + "!";
  return { body };
}
`)
	gateway := newExtensionGateway(t, map[string]string{"polish": polish}, pluginSettings{
		"polish": {Enabled: true, Capabilities: []string{plugin.CapabilityResponseWrite}},
	})
	response, record := gateway.send(chat("hello"))
	if got := gjson.Get(response.Body.String(), "choices.0.message.content").Str; got != "OK!" || response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) {
		t.Fatalf("response = %s (%s)", response.Body.String(), response.Header().Get("Content-Length"))
	}
	if entry := hookRecord(t, record, "polish", plugin.HookRewriteResponse); !slices.Equal(entry.Effects, []string{contract.PluginEffectRewrite}) {
		t.Fatalf("record = %+v", entry)
	}
}

const streamReply = "data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n" +
	": keep-alive\n\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"llo\"}}]}\n\n" +
	"data: [DONE]\n\n"

func TestStreamHooksRewriteEventsAndAddTheLastOnes(t *testing.T) {
	shout := extensionPlugin("shout", []string{plugin.CapabilityResponseWrite}, "", `
export async function onStreamEvent(ctx, event, state) {
  const seen = (state ?? 0) + 1;
  if (event.data === "[DONE]") return { events: [], state: seen };
  const data = structuredClone(event.data);
  data.choices[0].delta.content = data.choices[0].delta.content.toUpperCase();
  return { events: [{ data }], state: seen };
}
export async function onStreamEnd(ctx, state) {
  return { events: [{ data: { choices: [{ delta: { content: " (" + state + " events)" } }] } }, { data: "[DONE]" }] };
}
`)
	gateway := newExtensionGateway(t, map[string]string{"shout": shout}, pluginSettings{
		"shout": {Enabled: true, Capabilities: []string{plugin.CapabilityResponseWrite}},
	})
	gateway.reply = streamReply
	response, record := gateway.send(strings.Replace(chat("hello"), `"model":"gpt-test"`, `"model":"gpt-test","stream":true`, 1))
	body := response.Body.String()
	want := "data: {\"choices\":[{\"delta\":{\"content\":\"HE\"}}]}\n\n" +
		": keep-alive\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"LLO\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" (3 events)\"}}]}\n\n" +
		"data: [DONE]\n\n"
	if body != want {
		t.Fatalf("stream =\n%q\nwant\n%q", body, want)
	}
	if entry := hookRecord(t, record, "shout", plugin.HookOnStreamEvent); entry.Events != 3 || entry.Failures != 0 || !slices.Equal(entry.Effects, []string{contract.PluginEffectStream}) {
		t.Fatalf("record = %+v", entry)
	}
}

func TestStreamHooksThatKeepFailingAreLeftOut(t *testing.T) {
	broken := extensionPlugin("broken", []string{plugin.CapabilityResponseWrite}, "", `
export async function onStreamEvent() { throw new Error("cannot parse"); }
`)
	gateway := newExtensionGateway(t, map[string]string{"broken": broken}, pluginSettings{
		"broken": {Enabled: true, Capabilities: []string{plugin.CapabilityResponseWrite}},
	})
	gateway.reply = streamReply + streamReply
	response, record := gateway.send(strings.Replace(chat("hello"), `"model":"gpt-test"`, `"model":"gpt-test","stream":true`, 1))
	if response.Body.String() != streamReply+streamReply {
		t.Fatalf("the stream changed: %q", response.Body.String())
	}
	if entry := hookRecord(t, record, "broken", plugin.HookOnStreamEvent); entry.Status != contract.PluginRecordFailed || entry.SkippedReason != "stream_stopped" || entry.Events != 3 || entry.Failures != 3 {
		t.Fatalf("record = %+v", entry)
	}
}

func TestOnResponseSeesTheReplyAfterTheRequest(t *testing.T) {
	watcher := extensionPlugin("watcher", []string{plugin.CapabilityResponseRead, plugin.CapabilityEvents}, "", `
export async function onResponse(ctx) {
  console.info("reply", ctx.response.status, ctx.response.provider, JSON.stringify(ctx.response.text));
}
export async function onEvent(event) {
  console.info("event", event.type, event.response.text === undefined);
}
`)
	gateway := newExtensionGateway(t, map[string]string{"watcher": watcher}, pluginSettings{
		"watcher": {Enabled: true, Capabilities: []string{plugin.CapabilityResponseRead, plugin.CapabilityEvents}},
	})
	gateway.reply = streamReply
	response, _ := gateway.send(strings.Replace(chat("hello"), `"model":"gpt-test"`, `"model":"gpt-test","stream":true`, 1))
	if response.Body.String() != streamReply {
		t.Fatalf("an observed stream changed: %q", response.Body.String())
	}
	loaded := gateway.manager.Current().Plugin("watcher")
	deadline := time.Now().Add(5 * time.Second)
	var messages []string
	for time.Now().Before(deadline) {
		entries, _ := loaded.Journal(0)
		messages = messages[:0]
		for _, entry := range entries {
			messages = append(messages, entry.Message)
		}
		if len(messages) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	slices.Sort(messages)
	if len(messages) != 2 || messages[0] != "event request.finished true" || messages[1] != `reply succeeded Alpha "hello"` {
		t.Fatalf("journal = %q", messages)
	}
}

func TestPluginHTTPIsServedOnTheInferenceListener(t *testing.T) {
	web := extensionPlugin("web", []string{plugin.CapabilityHTTP}, "", `
export async function onHttp(request) {
  return { json: { path: request.path, method: request.method, auth: request.headers.authorization ?? null } };
}
`)
	gateway := newExtensionGateway(t, map[string]string{"web": web}, pluginSettings{
		"web": {Enabled: true, Capabilities: []string{plugin.CapabilityHTTP}},
	})
	request := httptest.NewRequest(http.MethodGet, "/plugins/web/status/now?x=1", nil)
	request.Header.Set("Authorization", "Bearer local-secret")
	recorder := httptest.NewRecorder()
	gateway.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"path":"/status/now","method":"GET","auth":null}` || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("plugin endpoint = %d %s", recorder.Code, recorder.Body.String())
	}
	for _, path := range []string{"/plugins/nope/x", "/plugins/Bad/x"} {
		recorder := httptest.NewRecorder()
		gateway.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s = %d", path, recorder.Code)
		}
	}
}
