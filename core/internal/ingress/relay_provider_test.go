package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/tidwall/gjson"
)

// vendorRelayPlugin is a new-api plugin with only a provider part: its own
// models go to its OpenAI-style path with its own key header, every other
// model is converted to Claude Messages, and prepareRequest and
// signRequest adjust each request.
const vendorRelayPlugin = `export const meta = {
  apiVersion: 1, key: "vendor", name: "Vendor", version: "1.0.0", author: { name: "Test" }, baseUrl: "https://api.vendor.example",
  models: ["vendor-large", "claude-x"],
  channelConfig: { type: "object", properties: { region: { type: "string", title: "Region", default: "us" } } },
  relay: [
    { incoming_path: "/v1/chat/completions", upstream_path: "/openai/{model}/chat", models: ["re:^vendor-"], auth: { type: "header", name: "X-Vendor-Key", value: "Key {api_key}" } },
    { incoming_path: "/v1/chat/completions", upstream_path: "/claude/v1/messages", converter: "openai_chat_completions_to_anthropic_messages", auth: { type: "header", name: "x-api-key", value: "{api_key}" } },
    { incoming_path: "/v1/models", upstream_path: "/openai/models", auth: { type: "query", name: "key", value: "{api_key}" } },
  ],
};
export function prepareRequest(ctx, request) {
  if (request.body.temperature === 9) return { effects: [{ type: "json.patch", ops: [{ op: "replace", path: "/model", value: "cheap" }] }] };
  return { effects: [
    { type: "json.patch", ops: [{ op: "add", path: "/region", value: ctx.config.region }, ...(request.body.metadata ? [{ op: "remove", path: "/metadata" }] : [])] },
    { type: "header.patch", set: { "X-Vendor-Beta": "2025-01" } },
  ] };
}
export function signRequest(ctx, request) {
  return { headers: { "X-Signature": request.bodySha256.slice(0, 8) + ":" + ctx.apiKey + ":" + request.method } };
}
`

type relayUpstream struct {
	server   *httptest.Server
	mutex    sync.Mutex
	requests []*http.Request
	bodies   []string
}

func newRelayUpstream(t *testing.T) *relayUpstream {
	upstream := &relayUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		upstream.mutex.Lock()
		upstream.requests = append(upstream.requests, request.Clone(context.Background()))
		upstream.bodies = append(upstream.bodies, string(body))
		upstream.mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/base/openai/models":
			_, _ = io.WriteString(writer, `{"object":"list","data":[{"id":"vendor-large"},{"id":"vendor-mini"}]}`)
		case "/base/openai/vendor-large/chat":
			_, _ = io.WriteString(writer, `{"id":"c1","object":"chat.completion","model":"vendor-large","choices":[{"index":0,"message":{"role":"assistant","content":"native"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		case "/base/claude/v1/messages":
			_, _ = io.WriteString(writer, `{"id":"m1","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":"converted"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (upstream *relayUpstream) last(t *testing.T) (*http.Request, string) {
	t.Helper()
	upstream.mutex.Lock()
	defer upstream.mutex.Unlock()
	if len(upstream.requests) == 0 {
		t.Fatal("the upstream got no request")
	}
	return upstream.requests[len(upstream.requests)-1], upstream.bodies[len(upstream.bodies)-1]
}

func newRelayGateway(t *testing.T) (*Handler, *pluginhost.Host, contract.Service, *relayUpstream) {
	return newRelayGatewayOf(t, vendorRelayPlugin, newRelayUpstream(t))
}

func newRelayGatewayOf(t *testing.T, source string, upstream *relayUpstream) (*Handler, *pluginhost.Host, contract.Service, *relayUpstream) {
	return newRelayGatewayWithSecrets(t, source, upstream, nil)
}

// relaySecrets keeps a provider's secret in memory: the key, or the
// credential a plugin logged it in with.
type relaySecrets struct {
	mutex  sync.Mutex
	secret []byte
}

func (secrets *relaySecrets) ReadServiceSecret(context.Context, contract.Service) ([]byte, error) {
	secrets.mutex.Lock()
	defer secrets.mutex.Unlock()
	if secrets.secret == nil {
		return nil, errors.New("no secret")
	}
	return slices.Clone(secrets.secret), nil
}

func (secrets *relaySecrets) WriteServiceSecret(_ context.Context, _ contract.ServiceID, secret []byte) error {
	secrets.mutex.Lock()
	defer secrets.mutex.Unlock()
	secrets.secret = slices.Clone(secret)
	return nil
}

func (secrets *relaySecrets) Get(ctx context.Context, _ secretstore.Ref) ([]byte, error) {
	return secrets.ReadServiceSecret(ctx, contract.Service{})
}
func (secrets *relaySecrets) Put(context.Context, secretstore.Ref, []byte) error { return nil }
func (secrets *relaySecrets) Delete(context.Context, secretstore.Ref) error      { return nil }

func newRelayGatewayWithSecrets(t *testing.T, source string, upstream *relayUpstream, secrets *relaySecrets) (*Handler, *pluginhost.Host, contract.Service, *relayUpstream) {
	t.Helper()
	manager := plugin.NewManager(plugin.ManagerOptions{
		Builtin:  fstest.MapFS{"vendor/plugin.js": &fstest.MapFile{Data: []byte(source)}},
		Settings: pluginSettings{"vendor": plugin.Settings{Enabled: true}},
	})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loaded := manager.Current().Plugin("vendor"); !loaded.Loaded() || loaded.NewAPI() == nil {
		t.Fatalf("vendor did not load: %v", loaded.Err)
	}
	options := pluginhost.Options{Plugins: manager, Logf: t.Logf}
	var keys secretstore.SecretStore = codingPlanCredentials{}
	if secrets != nil {
		options.Credentials, keys = secrets, secrets
	}
	host := pluginhost.New(options)
	t.Cleanup(host.Close)
	service := contract.Service{
		ID: "service_vendor", Name: "Vendor", Kind: contract.ServiceKindPlugin, Enabled: true, Models: []string{"vendor-large", "claude-x", "acme-1", "acme-job"},
		HTTP:         &contract.HTTPConnection{BaseURL: upstream.server.URL + "/base", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}, CredentialRef: "local://service/service_vendor"},
		Plugin:       &contract.ServicePlugin{Key: "vendor", Provider: contract.PluginProviderRelay, Settings: json.RawMessage(`{"region":"eu","stray":1}`)},
		Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true}},
	}
	if err := service.Validate(); err != nil {
		t.Fatal(err)
	}
	handler := NewWithDependencies(Dependencies{
		Resolver:         candidateResolver{candidates: []endpoint.Resolved{{Service: service, BaseURL: service.HTTP.BaseURL}}},
		Authorizer:       endpoint.NewServiceAuthorizer(keys, nil),
		RequestRecords:   &memoryRequestRecordStore{},
		AuditBlobs:       &memoryAuditBlobs{},
		Plugins:          host,
		ConversionEngine: relaykitbridge.NewEngine(),
	})
	return handler, host, service, upstream
}

func relayChat(t *testing.T, handler *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer gateway-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestARelayProviderSendsEachModelOverItsRoute(t *testing.T) {
	handler, _, _, upstream := newRelayGateway(t)

	// The plugin's own model goes to its path with its key header, as the
	// client sent it, adjusted by prepareRequest and signed.
	response := relayChat(t, handler, `{"model":"vendor-large","messages":[{"role":"user","content":"hi"}],"metadata":{"user":"u1"}}`)
	if response.Code != http.StatusOK || gjson.Get(response.Body.String(), "choices.0.message.content").String() != "native" {
		t.Fatalf("native = %d %s", response.Code, response.Body)
	}
	sent, body := upstream.last(t)
	switch {
	case sent.URL.Path != "/base/openai/vendor-large/chat":
		t.Fatalf("native path = %s", sent.URL.Path)
	case sent.Header.Get("X-Vendor-Key") != "Key plan-key" || sent.Header.Get("Authorization") != "":
		t.Fatalf("native key headers = %v", sent.Header)
	case sent.Header.Get("X-Vendor-Beta") != "2025-01" || !strings.HasSuffix(sent.Header.Get("X-Signature"), ":plan-key:POST"):
		t.Fatalf("prepared and signed headers = %v", sent.Header)
	case gjson.Get(body, "region").String() != "eu" || gjson.Get(body, "metadata").Exists() || gjson.Get(body, "model").String() != "vendor-large":
		t.Fatalf("native body = %s", body)
	}
	// Every other model is converted into Claude Messages and sent with
	// the key as x-api-key; the answer comes back as a chat completion.
	response = relayChat(t, handler, `{"model":"claude-x","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK || gjson.Get(response.Body.String(), "choices.0.message.content").String() != "converted" {
		t.Fatalf("converted = %d %s", response.Code, response.Body)
	}
	sent, body = upstream.last(t)
	switch {
	case sent.URL.Path != "/base/claude/v1/messages":
		t.Fatalf("converted path = %s", sent.URL.Path)
	case sent.Header.Get("X-Api-Key") != "plan-key" || sent.Header.Get("X-Vendor-Key") != "" || sent.Header.Get("Authorization") != "":
		t.Fatalf("converted key headers = %v", sent.Header)
	case gjson.Get(body, "messages.0.role").String() != "user" || gjson.Get(body, "region").String() != "eu" || gjson.Get(body, "max_tokens").Int() == 0:
		t.Fatalf("converted body = %s", body)
	}
	// A change to what the gateway bills by is refused, and not retried.
	before := len(upstream.requests)
	response = relayChat(t, handler, `{"model":"vendor-large","temperature":9,"messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "model") || len(upstream.requests) != before {
		t.Fatalf("a billing change = %d %s, upstream requests %d → %d", response.Code, response.Body, before, len(upstream.requests))
	}
}

func TestARelayProviderListsItsModelsOverItsRoute(t *testing.T) {
	_, host, service, upstream := newRelayGateway(t)
	target := transport.Target{Service: service, RequestHeaders: http.Header{"Authorization": {"Bearer plan-key"}}}
	models, listed, err := host.ProviderModels(context.Background(), service, target, transport.New(nil).RoundTrip)
	if err != nil || !listed || strings.Join(models, ",") != "vendor-large,vendor-mini" {
		t.Fatalf("models = %v %v %v", models, listed, err)
	}
	sent, _ := upstream.last(t)
	if sent.URL.Path != "/base/openai/models" || sent.URL.Query().Get("key") != "plan-key" || sent.Header.Get("Authorization") != "" ||
		!strings.HasSuffix(sent.Header.Get("X-Signature"), ":plan-key:GET") {
		t.Fatalf("model list request = %s %v", sent.URL, sent.Header)
	}
	if upstream, ok := host.RelayUpstreamProtocol(service, "/v1/chat/completions", "claude-x"); !ok || upstream != contract.ProtocolAnthropicMessages {
		t.Fatalf("the route of claude-x sends %s %v", upstream, ok)
	}
}

// tokenVendorPlugin exchanges the key for a short-lived token, sends it
// signed, and takes a 403 that says expired for a rejected token.
const tokenVendorPlugin = `export const meta = {
  apiVersion: 1, key: "vendor", name: "Vendor", version: "1.0.0", author: { name: "Test" }, baseUrl: "https://api.vendor.example",
  models: ["vendor-large"],
  relay: [{ incoming_path: "/v1/chat/completions", upstream_path: "/token/chat", auth: { type: "none" } }],
};
export function exchangeKey(ctx) { return { token: { value: ctx.apiKey + ":" + utils.uuid() } }; }
export function signRequest(ctx, request) { return { headers: { "X-Token": ctx.token.value } }; }
export function credentialRejected(ctx, response) { return response.status === 403 && response.body.includes("expired"); }
export function checkKey(ctx, key) { return key.includes("|") ? undefined : { error: { en: "Write it as AccessKey|SecretKey" } }; }
`

func TestARelayProviderExchangesItsKeyAgainWhenTheTokenIsRejected(t *testing.T) {
	upstream := &relayUpstream{}
	tokens := []string{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstream.mutex.Lock()
		tokens = append(tokens, request.Header.Get("X-Token"))
		first := len(tokens) == 1
		upstream.mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if first {
			writer.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(writer, `{"error":"token expired"}`)
			return
		}
		_, _ = io.WriteString(writer, `{"id":"c1","object":"chat.completion","model":"vendor-large","choices":[{"index":0,"message":{"role":"assistant","content":"fresh"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(upstream.server.Close)
	handler, _, _, _ := newRelayGatewayOf(t, tokenVendorPlugin, upstream)
	response := relayChat(t, handler, `{"model":"vendor-large","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK || gjson.Get(response.Body.String(), "choices.0.message.content").String() != "fresh" {
		t.Fatalf("answer = %d %s", response.Code, response.Body)
	}
	if len(tokens) != 2 || tokens[0] == tokens[1] || !strings.HasPrefix(tokens[0], "plan-key:") || !strings.HasPrefix(tokens[1], "plan-key:") {
		t.Fatalf("tokens = %v", tokens)
	}
	// A later request keeps the token the second exchange gave.
	_ = relayChat(t, handler, `{"model":"vendor-large","messages":[{"role":"user","content":"again"}]}`)
	if len(tokens) != 3 || tokens[2] != tokens[1] {
		t.Fatalf("tokens = %v", tokens)
	}
}

// convertingVendorPlugin converts Chat Completions into the vendor's own
// API: a prompt in, text or a stream of deltas out, and a job API that
// takes two requests.
const convertingVendorPlugin = `export const meta = {
  apiVersion: 1, key: "vendor", name: "Vendor", version: "1.0.0", author: { name: "Test" }, baseUrl: "https://api.vendor.example",
  models: ["acme-1", "acme-job"],
  relay: [{ incoming_path: "/v1/chat/completions", upstream_path: "/v2/generate", converter: "plugin" }],
};
const reply = (model, text) => ({ id: "c1", object: "chat.completion", model, choices: [{ index: 0, message: { role: "assistant", content: text }, finish_reason: "stop" }], usage: { prompt_tokens: 3, completion_tokens: 2, total_tokens: 5 } });
export function convertRequest(ctx, request) {
  const prompt = request.body.messages.map((message) => message.content).join("\n");
  if (request.model === "acme-job") return { body: { prompt }, path: "/v2/jobs", state: { step: "create" } };
  return { body: { prompt, model: request.model, stream: request.stream } };
}
export function convertResponse(ctx, response) {
  const json = JSON.parse(response.body);
  if (response.status >= 400) return { error: { status: response.status, message: json.err } };
  if (ctx.state && ctx.state.step === "create") return { next: { method: "GET", path: "/v2/jobs/" + json.id }, state: { step: "poll" } };
  return { body: reply("acme-1", json.output) };
}
export function convertStreamEvent(ctx, input) {
  if (input.done) return { events: [{ data: "[DONE]" }] };
  const json = JSON.parse(input.data);
  const choice = json.end ? { index: 0, delta: {}, finish_reason: "stop" } : { index: 0, delta: { content: json.delta } };
  return { events: [{ data: { id: "c1", object: "chat.completion.chunk", choices: [choice] } }] };
}
`

func TestARelayProviderConvertsThroughThePluginsHooks(t *testing.T) {
	upstream := &relayUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		upstream.mutex.Lock()
		upstream.requests = append(upstream.requests, request.Clone(context.Background()))
		upstream.bodies = append(upstream.bodies, string(body))
		upstream.mutex.Unlock()
		switch {
		case request.URL.Path == "/base/v2/jobs" && request.Method == http.MethodPost:
			_, _ = io.WriteString(writer, `{"id":"j1"}`)
		case request.URL.Path == "/base/v2/jobs/j1" && request.Method == http.MethodGet:
			_, _ = io.WriteString(writer, `{"output":"job done"}`)
		case gjson.Get(string(body), "model").String() == "acme-fail":
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(writer, `{"err":"slow down"}`)
		case gjson.Get(string(body), "stream").Bool():
			writer.Header().Set("Content-Type", "text/event-stream")
			for _, event := range []string{`{"delta":"he"}`, `{"delta":"llo"}`, `{"end":true}`} {
				_, _ = io.WriteString(writer, "data: "+event+"\n\n")
			}
		default:
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"output":"hello"}`)
		}
	}))
	t.Cleanup(upstream.server.Close)
	handler, _, _, _ := newRelayGatewayOf(t, convertingVendorPlugin, upstream)

	response := relayChat(t, handler, `{"model":"acme-1","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK || gjson.Get(response.Body.String(), "choices.0.message.content").String() != "hello" {
		t.Fatalf("whole = %d %s", response.Code, response.Body)
	}
	if sent, body := upstream.last(t); sent.URL.Path != "/base/v2/generate" || gjson.Get(body, "prompt").String() != "hi" {
		t.Fatalf("sent %s %s", sent.URL.Path, body)
	}
	response = relayChat(t, handler, `{"model":"acme-1","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if text := streamText(t, response.Body.String()); response.Code != http.StatusOK || text != "hello" || !strings.Contains(response.Body.String(), "[DONE]") {
		t.Fatalf("stream = %d %q\n%s", response.Code, text, response.Body)
	}
	// A job takes two requests: the hooks chain them.
	response = relayChat(t, handler, `{"model":"acme-job","messages":[{"role":"user","content":"make it"}]}`)
	if response.Code != http.StatusOK || gjson.Get(response.Body.String(), "choices.0.message.content").String() != "job done" {
		t.Fatalf("job = %d %s", response.Code, response.Body)
	}
	if sent, _ := upstream.last(t); sent.Method != http.MethodGet || sent.URL.Path != "/base/v2/jobs/j1" {
		t.Fatalf("the chain's last request = %s %s", sent.Method, sent.URL.Path)
	}
}

// loginVendorPlugin logs a provider in: with a token typed into a form, or
// through the vendor's page, which sends the browser back with a code. Its
// credential sends a bearer header, and refresh gives a new one.
const loginVendorPlugin = `export const meta = {
  apiVersion: 1, key: "vendor", name: "Vendor", version: "1.0.0", author: { name: "Test" }, baseUrl: "https://api.vendor.example",
  models: ["vendor-large"],
  relay: [{ incoming_path: "/v1/chat/completions", upstream_path: "/login/chat" }],
};
export function login(ctx, input) {
  if (input === null && ctx.login.session === null) return { step: "redirect", url: "https://vendor.example/auth?state=" + ctx.login.state + "&challenge=" + ctx.login.codeChallenge + "&redirect=" + encodeURIComponent(ctx.login.redirectUri), session: { started: true } };
  if (input && input.callback) {
    if (input.callback.code !== "abc") throw new Error("bad code");
    return { step: "fields", fields: [{ name: "token", label: { en: "Token", "zh-CN": "令牌" }, type: "password", required: true }] };
  }
  if (input && input.fields) return { step: "done", credential: { n: 1, verifier: ctx.login.codeVerifier.length }, headers: { Authorization: "Bearer " + input.fields.token }, expiresAt: Math.floor(Date.now() / 1000) + 3600, accountId: "acct-1", label: "Test account" };
  throw new Error("unexpected input");
}
export function refresh(ctx) {
  return { credential: { n: ctx.credential.n + 1 }, headers: { Authorization: "Bearer refreshed-" + (ctx.credential.n + 1) }, expiresAt: Math.floor(Date.now() / 1000) + 3600 };
}
`

func TestAPluginLogsAProviderInAndRefreshesItsCredential(t *testing.T) {
	upstream := &relayUpstream{}
	var seen []string
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstream.mutex.Lock()
		seen = append(seen, request.Header.Get("Authorization"))
		upstream.mutex.Unlock()
		if request.Header.Get("Authorization") == "Bearer typed-token" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"c1","object":"chat.completion","model":"vendor-large","choices":[{"index":0,"message":{"role":"assistant","content":"in"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(upstream.server.Close)
	secrets := &relaySecrets{}
	handler, host, service, _ := newRelayGatewayWithSecrets(t, loginVendorPlugin, upstream, secrets)
	host.UseGatewayAddress("0.0.0.0:17891")
	ctx := context.Background()

	view, err := host.StartLogin(ctx, service)
	if err != nil || view.Step != "redirect" || !strings.Contains(view.URL, "redirect=http%3A%2F%2F127.0.0.1%3A17891%2Foauth%2Fplugin%2Fvendor%2Fcallback") {
		t.Fatalf("start = %+v %v", view, err)
	}
	state := strings.TrimPrefix(strings.Split(view.URL, "&")[0], "https://vendor.example/auth?state=")
	// A callback for another login is refused; the right one moves on.
	callback := func(query string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		host.ServeLoginCallback(response, httptest.NewRequest(http.MethodGet, "/oauth/plugin/vendor/callback?"+query, nil))
		return response
	}
	if response := callback("state=wrong&code=abc"); response.Code != http.StatusNotFound {
		t.Fatalf("a callback of another state = %d", response.Code)
	}
	if response := callback("state=" + state + "&code=abc"); response.Code != http.StatusOK {
		t.Fatalf("callback = %d %s", response.Code, response.Body)
	}
	view, _ = host.Login(ctx, service.ID)
	if view.Step != "fields" || len(view.Fields) != 1 || view.Fields[0].Label != "Token" {
		t.Fatalf("after the callback = %+v", view)
	}
	if _, err := host.SubmitLogin(ctx, service.ID, pluginhost.LoginInput{Fields: map[string]string{"token": ""}}); err == nil {
		t.Fatal("a required field left empty was taken")
	}
	view, err = host.SubmitLogin(ctx, service.ID, pluginhost.LoginInput{Fields: map[string]string{"token": "typed-token"}})
	if err != nil || view.Step != "done" || view.Account != "acct-1" || view.Label != "Test account" {
		t.Fatalf("done = %+v %v", view, err)
	}
	if credential := host.PluginCredentialOf(ctx, service); !credential.LoggedIn || credential.AccountID != "acct-1" || credential.NeedsLogin {
		t.Fatalf("credential = %+v", credential)
	}
	// The upstream refuses the logged-in token: the credential is
	// refreshed, kept, and the request sent again.
	response := relayChat(t, handler, `{"model":"vendor-large","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK || strings.Join(seen, ",") != "Bearer typed-token,Bearer refreshed-2" {
		t.Fatalf("answer = %d %s, sent %v", response.Code, response.Body, seen)
	}
	_ = relayChat(t, handler, `{"model":"vendor-large","messages":[{"role":"user","content":"again"}]}`)
	if seen[len(seen)-1] != "Bearer refreshed-2" {
		t.Fatalf("a later request sent %v", seen)
	}
}

// accountVendorPlugin reads its account's balance and rests the provider
// while its quota is used up.
const accountVendorPlugin = `export const meta = {
  apiVersion: 1, key: "vendor", name: "Vendor", version: "1.0.0", author: { name: "Test" }, baseUrl: "https://api.vendor.example",
  models: ["vendor-large"],
  relay: [{ incoming_path: "/v1/chat/completions", upstream_path: "/account/chat" }],
};
export function buildBalanceRequest(ctx) { return { path: "/account/balance", headers: { "X-Key": ctx.apiKey } }; }
export function parseBalance(ctx, response) {
  const json = JSON.parse(response.body);
  if (response.status !== 200) return { error: "the account answered " + response.status };
  return { amount: json.left, currency: "USD", windows: [{ name: { en: "Daily", "zh-CN": "每日" }, used: json.used, limit: 10, unit: "requests" }] };
}
export function scheduled(ctx) {
  const response = utils.fetch({ url: ctx.baseUrl + "/account/balance", headers: { "X-Key": ctx.apiKey } });
  if (response.body.used >= 10) return { next: 120, keyStatus: "auto_disabled", notify: { en: "Daily quota used up", "zh-CN": "今天的额度用完了" } };
  return { next: 120, keyStatus: "enabled", balance: { amount: response.body.left, currency: "USD" } };
}
`

func TestAPluginReadsTheBalanceAndRestsAProvider(t *testing.T) {
	upstream := &relayUpstream{}
	used := 3
	chats := 0
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/base/account/balance":
			if request.Header.Get("X-Key") != "sk-test" {
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(writer, `{}`)
				return
			}
			_, _ = io.WriteString(writer, fmt.Sprintf(`{"left":12.5,"used":%d}`, used))
		default:
			chats++
			_, _ = io.WriteString(writer, `{"id":"c1","object":"chat.completion","model":"vendor-large","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		}
	}))
	t.Cleanup(upstream.server.Close)
	secrets := &relaySecrets{secret: []byte("sk-test")}
	handler, host, service, _ := newRelayGatewayWithSecrets(t, accountVendorPlugin, upstream, secrets)
	ctx := context.Background()
	state, err := host.ProviderBalance(ctx, service)
	if err != nil || state.Balance == nil || state.Balance.Amount == nil || *state.Balance.Amount != 12.5 || state.Balance.Currency != "USD" ||
		len(state.Balance.Windows) != 1 || state.Balance.Windows[0].Name.In("zh-CN") != "每日" || state.Balance.Windows[0].Used != 3 {
		t.Fatalf("balance = %+v %v", state, err)
	}
	if next := host.RunScheduled(ctx, service); next != 2*time.Minute || host.ProviderStateOf(service.ID).Resting {
		t.Fatalf("scheduled with quota left = %v %+v", next, host.ProviderStateOf(service.ID))
	}
	// The quota is used up: the plugin rests the provider, which serves no
	// request until the plugin says it can.
	used = 10
	host.RunScheduled(ctx, service)
	if state := host.ProviderStateOf(service.ID); !state.Resting || state.Notice != "Daily quota used up" {
		t.Fatalf("a used-up quota = %+v", state)
	}
	if response := relayChat(t, handler, `{"model":"vendor-large","messages":[{"role":"user","content":"hi"}]}`); response.Code == http.StatusOK || chats != 0 {
		t.Fatalf("a resting provider served = %d, chats %d", response.Code, chats)
	}
	used = 0
	host.RunScheduled(ctx, service)
	if response := relayChat(t, handler, `{"model":"vendor-large","messages":[{"role":"user","content":"hi"}]}`); response.Code != http.StatusOK || chats != 1 {
		t.Fatalf("after the quota came back = %d, chats %d", response.Code, chats)
	}
}
