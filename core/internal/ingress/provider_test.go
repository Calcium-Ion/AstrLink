package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/tidwall/gjson"
)

// acmePlugin adapts the Acme API, which takes a prompt and answers text or
// a stream of deltas, to Chat Completions.
const acmePlugin = `export const meta = {
  apiVersion: 2, key: "acme", name: "Acme", version: "1.0.0", capabilities: ["provider"],
  providers: [{ id: "acme", name: "Acme AI", baseUrl: "https://acme.example/api", protocols: { "openai.chat": { stream: true } } }],
};
export async function buildRequest(ctx) {
  if (ctx.purpose === "models") return { method: "GET", url: "/v2/models" };
  const prompt = ctx.body.messages.map((message) => message.content).join("\n");
  return {
    url: ctx.model === "acme-fail" ? "/v2/fail" : "/v2/generate",
    headers: { "X-Acme-Tenant": ctx.settings.tenant, Authorization: "Bearer stolen", "X-AstrLink-Debug": "1" },
    body: { prompt, model: ctx.model, stream: ctx.stream, version: ctx.headers["anthropic-version"] ?? null },
  };
}
export async function parseResponse(ctx, res) {
  if (ctx.purpose === "models") return { models: res.json.data.map((model) => model.name) };
  return { json: {
    id: "acme-1", object: "chat.completion", model: ctx.model,
    choices: [{ index: 0, message: { role: "assistant", content: res.json.output }, finish_reason: "stop" }],
    usage: { prompt_tokens: res.json.usage.in, completion_tokens: res.json.usage.out, total_tokens: res.json.usage.in + res.json.usage.out },
  } };
}
export async function parseStreamEvent(ctx, event, state) {
  const seen = (state ?? 0) + 1;
  if (event.json.end) return { events: [{ data: { id: "acme-1", object: "chat.completion.chunk", choices: [{ index: 0, delta: {}, finish_reason: "stop" }] } }], state: seen, done: true };
  return { events: [{ data: { id: "acme-1", object: "chat.completion.chunk", choices: [{ index: 0, delta: { content: event.json.delta } }] } }], state: seen };
}
export async function finishStream(ctx, state) {
  return { events: [{ data: { id: "acme-1", object: "chat.completion.chunk", choices: [], seen: state } }, { data: "[DONE]" }] };
}
export async function parseError(ctx, res) {
  return { status: res.status, code: "acme_busy", message: res.json.err, retryable: true };
}
`

// acmeUpstream is the Acme API: what it received, and its answers.
type acmeUpstream struct {
	server *httptest.Server
	mutex  sync.Mutex
	paths  []string
	bodies []string
	header []http.Header
}

func newAcmeUpstream(t *testing.T) *acmeUpstream {
	upstream := &acmeUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		upstream.mutex.Lock()
		upstream.paths = append(upstream.paths, request.Method+" "+request.URL.Path)
		upstream.bodies = append(upstream.bodies, string(body))
		upstream.header = append(upstream.header, request.Header.Clone())
		upstream.mutex.Unlock()
		switch {
		case request.URL.Path == "/api/v2/models":
			_, _ = io.WriteString(writer, `{"data":[{"name":"acme-1"},{"name":"acme-2"}]}`)
		case request.URL.Path == "/api/v2/fail":
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(writer, `{"err":"slow down"}`)
		case gjson.GetBytes(body, "stream").Bool():
			writer.Header().Set("Content-Type", "text/event-stream")
			for _, event := range []string{`{"delta":"he"}`, `{"delta":"llo"}`, `{"end":true}`} {
				_, _ = io.WriteString(writer, "data: "+event+"\n\n")
				writer.(http.Flusher).Flush()
			}
		default:
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"output":"hello","usage":{"in":3,"out":2}}`)
		}
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

type providerGateway struct {
	t        *testing.T
	handler  *Handler
	host     *pluginhost.Host
	store    *memoryRequestRecordStore
	upstream *acmeUpstream
	service  contract.Service
	// fallback counts the requests the second provider served.
	fallback int
}

func newProviderGateway(t *testing.T, enabled bool) *providerGateway {
	t.Helper()
	upstream := newAcmeUpstream(t)
	settings := pluginSettings{}
	if enabled {
		settings["acme"] = plugin.Settings{Enabled: true, Capabilities: []string{plugin.CapabilityProvider}, Config: json.RawMessage(`{"tenant":"t9"}`)}
	}
	manager := plugin.NewManager(plugin.ManagerOptions{Builtin: fstest.MapFS{"acme/plugin.js": &fstest.MapFile{Data: []byte(acmePlugin)}}, Settings: settings})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loaded := manager.Current().Plugin("acme"); !loaded.Loaded() {
		t.Fatalf("acme did not load: %v", loaded.Err)
	}
	host := pluginhost.New(pluginhost.Options{Plugins: manager, Logf: t.Logf})
	t.Cleanup(host.Close)
	service := contract.Service{
		ID: "service_acme", Name: "Acme", Kind: contract.ServiceKindPlugin, Enabled: true, Models: []string{"acme-1", "acme-fail"},
		HTTP:         &contract.HTTPConnection{BaseURL: upstream.server.URL + "/api", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}},
		Plugin:       &contract.ServicePlugin{Key: "acme", Provider: "acme"},
		Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true}},
	}
	fallback := validEndpoint(contract.ProtocolOpenAIChat, true)
	fallback.ID, fallback.Name = "endpoint_fallback", "Fallback"
	gateway := &providerGateway{t: t, host: host, store: &memoryRequestRecordStore{}, upstream: upstream, service: service}
	gateway.handler = NewWithDependencies(Dependencies{
		Resolver:       candidateResolver{candidates: []endpoint.Resolved{{Service: service, BaseURL: service.HTTP.BaseURL}, {Endpoint: fallback}}},
		RequestRecords: gateway.store,
		AuditBlobs:     &memoryAuditBlobs{},
		Plugins:        host,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, target transport.Target) error {
			gateway.fallback++
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err := io.WriteString(writer, `{"id":"f","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"from the fallback"},"finish_reason":"stop"}]}`)
			return err
		}),
	})
	return gateway
}

func (gateway *providerGateway) send(body string) *httptest.ResponseRecorder {
	gateway.t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Anthropic-Version", "2023-06-01")
	recorder := httptest.NewRecorder()
	gateway.handler.ServeHTTP(recorder, request)
	return recorder
}

func TestAPluginAdaptsAProviderWithWholeResponses(t *testing.T) {
	gateway := newProviderGateway(t, true)
	response := gateway.send(`{"model":"acme-1","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK || gjson.Get(response.Body.String(), "choices.0.message.content").String() != "hello" ||
		gjson.Get(response.Body.String(), "usage.total_tokens").Int() != 5 {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	upstream := gateway.upstream
	if len(upstream.paths) != 1 || upstream.paths[0] != "POST /api/v2/generate" {
		t.Fatalf("upstream got %v", upstream.paths)
	}
	sent := upstream.bodies[0]
	if gjson.Get(sent, "prompt").String() != "hi" || gjson.Get(sent, "model").String() != "acme-1" || gjson.Get(sent, "stream").Bool() ||
		gjson.Get(sent, "version").String() != "2023-06-01" {
		t.Fatalf("upstream body = %s", sent)
	}
	header := upstream.header[0]
	// The plugin's own header goes through; a credential or a gateway header
	// it sets does not.
	if header.Get("X-Acme-Tenant") != "t9" || header.Get("Authorization") != "" || header.Get("X-Astrlink-Debug") != "" {
		t.Fatalf("upstream header = %v", header)
	}
	if gateway.fallback != 0 {
		t.Fatalf("the fallback served %d requests", gateway.fallback)
	}
	record := latestRoot(t, gateway.store)
	if record.Status != contract.RequestStatusSucceeded {
		t.Fatalf("record = %+v", record)
	}
}

func TestAPluginAdaptsAProvidersStream(t *testing.T) {
	gateway := newProviderGateway(t, true)
	response := gateway.send(`{"model":"acme-1","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("response = %d %v %s", response.Code, response.Header(), body)
	}
	var text strings.Builder
	var frames []string
	for frame := range strings.SplitSeq(strings.TrimSpace(body), "\n\n") {
		data := strings.TrimPrefix(frame, "data: ")
		frames = append(frames, data)
		text.WriteString(gjson.Get(data, "choices.0.delta.content").String())
	}
	if text.String() != "hello" || frames[len(frames)-1] != "[DONE]" || gjson.Get(frames[len(frames)-2], "seen").Int() != 3 {
		t.Fatalf("frames = %q", frames)
	}
	if !gjson.Get(gateway.upstream.bodies[0], "stream").Bool() {
		t.Fatalf("the upstream was not asked for a stream: %s", gateway.upstream.bodies[0])
	}
}

func TestAProviderErrorFailsOverAsTheAdapterSays(t *testing.T) {
	gateway := newProviderGateway(t, true)
	response := gateway.send(`{"model":"acme-fail","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK || gjson.Get(response.Body.String(), "choices.0.message.content").String() != "from the fallback" || gateway.fallback != 1 {
		t.Fatalf("response = %d %s, fallback %d", response.Code, response.Body.String(), gateway.fallback)
	}
}

func TestAProviderWhosePluginIsOffIsPassedOver(t *testing.T) {
	gateway := newProviderGateway(t, false)
	response := gateway.send(`{"model":"acme-1","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK || gateway.fallback != 1 || len(gateway.upstream.paths) != 0 {
		t.Fatalf("response = %d %s, fallback %d, upstream %v", response.Code, response.Body.String(), gateway.fallback, gateway.upstream.paths)
	}
}

func TestAPluginListsAProvidersModels(t *testing.T) {
	gateway := newProviderGateway(t, true)
	forwarder := transport.New(nil)
	base := gateway.upstream.server.URL
	target := transport.Target{Service: gateway.service}
	models, ok, err := gateway.host.ProviderModels(context.Background(), gateway.service, target, forwarder.RoundTrip)
	if err != nil || !ok || strings.Join(models, ",") != "acme-1,acme-2" {
		t.Fatalf("ProviderModels = %v, %v, %v (upstream %s)", models, ok, err, base)
	}
}
