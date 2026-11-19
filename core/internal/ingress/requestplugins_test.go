package ingress

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/tidwall/gjson"
)

// requestPluginGateway runs new-api's built-in request processing plugins,
// as they ship in new-api, in front of a provider that records what it got.
type requestPluginGateway struct {
	t       *testing.T
	handler *Handler
	host    *pluginhost.Host
	store   *memoryRequestRecordStore
	sent    []string
	reply   string
	// replyType is the answer's content type, JSON when empty.
	replyType string
}

func newRequestPluginGateway(t *testing.T, instances ...contract.RequestPluginInstance) *requestPluginGateway {
	return newRequestPluginGatewayWith(t, nil, instances...)
}

// newRequestPluginGatewayWith runs new-api's plugins and the extra ones,
// by key.
func newRequestPluginGatewayWith(t *testing.T, extra map[string]string, instances ...contract.RequestPluginInstance) *requestPluginGateway {
	t.Helper()
	host := newRequestPluginHost(t, extra, instances...)
	gateway := &requestPluginGateway{t: t, host: host, store: &memoryRequestRecordStore{},
		reply: `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`}
	provider := validEndpoint(contract.ProtocolOpenAIChat, true)
	gateway.handler = NewWithDependencies(Dependencies{
		Resolver:       candidateResolver{candidates: []endpoint.Resolved{{Endpoint: provider}}},
		RequestRecords: gateway.store,
		AuditBlobs:     &memoryAuditBlobs{},
		Plugins:        host,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			body, _ := io.ReadAll(request.Body)
			gateway.sent = append(gateway.sent, string(body))
			contentType := gateway.replyType
			if contentType == "" {
				contentType = "application/json"
			}
			writer.Header().Set("Content-Type", contentType)
			writer.WriteHeader(http.StatusOK)
			_, err := io.WriteString(writer, gateway.reply)
			return err
		}),
	})
	return gateway
}

// newRequestPluginHost runs new-api's request processing plugins and the
// extra ones, by key, with instances of them.
func newRequestPluginHost(t *testing.T, extra map[string]string, instances ...contract.RequestPluginInstance) *pluginhost.Host {
	t.Helper()
	files := fstest.MapFS{}
	settings := pluginSettings{}
	for _, name := range []string{"request-redact", "request-filter"} {
		text, err := os.ReadFile("testdata/newapi/" + name + "/plugin.js")
		if err != nil {
			t.Fatal(err)
		}
		files[name+"/plugin.js"] = &fstest.MapFile{Data: text}
		settings[name] = plugin.Settings{Enabled: true}
	}
	for name, text := range extra {
		files[name+"/plugin.js"] = &fstest.MapFile{Data: []byte(text)}
		settings[name] = plugin.Settings{Enabled: true}
	}
	manager := plugin.NewManager(plugin.ManagerOptions{Builtin: files, Settings: settings})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"request-redact", "request-filter"} {
		if loaded := manager.Current().Plugin(name); !loaded.Loaded() || !loaded.NewAPI().HasRequestPart() {
			t.Fatalf("%s did not load: %v", name, loaded.Err)
		}
	}
	host := pluginhost.New(pluginhost.Options{Plugins: manager, Logf: t.Logf})
	t.Cleanup(host.Close)
	if _, err := host.UpdateSettings(context.Background(), contract.PluginHostSettingsUpdate{RequestPlugins: &instances}); err != nil {
		t.Fatal(err)
	}
	return host
}

func (gateway *requestPluginGateway) chat(body string) *httptest.ResponseRecorder {
	gateway.t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	gateway.handler.ServeHTTP(response, request)
	return response
}

func (gateway *requestPluginGateway) records() []contract.RequestPluginRecord {
	gateway.t.Helper()
	records := gateway.store.records
	if len(records) == 0 {
		gateway.t.Fatal("no request record")
	}
	return records[len(records)-1].PluginRecords
}

func TestRequestRedactionHidesPersonalDataBeforeItIsSent(t *testing.T) {
	gateway := newRequestPluginGateway(t, contract.RequestPluginInstance{
		ID: "rpi_redact001", Name: "脱敏", Plugin: "request-redact", Enabled: true, Models: []string{},
	})
	response := gateway.chat(`{"model":"gpt-test","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"Mail jane.doe@example.com or call +14155550123, key sk-abcdefghijklmnopqrstuvwxyz012345"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body)
	}
	if len(gateway.sent) != 1 {
		t.Fatalf("sent %d requests", len(gateway.sent))
	}
	content := gjson.Get(gateway.sent[0], "messages.1.content").String()
	if strings.Contains(content, "jane.doe@example.com") || strings.Contains(content, "4155550123") || strings.Contains(content, "sk-abcdef") || !strings.Contains(content, "[REDACTED]") {
		t.Fatalf("sent content = %q", content)
	}
	if gjson.Get(gateway.sent[0], "model").String() != "gpt-test" || gjson.Get(gateway.sent[0], "messages.0.content").String() != "be brief" {
		t.Fatalf("sent = %s", gateway.sent[0])
	}
	records := gateway.records()
	if len(records) == 0 || records[0].Plugin != "request-redact" || records[0].Hook != "ingress" || records[0].Title != "脱敏" ||
		!strings.Contains(records[0].Summary, "email") || len(records[0].Effects) == 0 || records[0].Effects[0] != contract.PluginEffectBody {
		t.Fatalf("records = %+v", records)
	}
}

func TestRequestFilterRejectsAKeywordAndModelsLimitAnInstance(t *testing.T) {
	config, _ := json.Marshal(map[string]any{"keywords": "forbidden word\nsecret plan", "message": "不能发送这个内容"})
	gateway := newRequestPluginGateway(t,
		contract.RequestPluginInstance{ID: "rpi_filter001", Name: "过滤", Plugin: "request-filter", Enabled: true, Models: []string{"gpt-test"}, Config: config},
	)
	response := gateway.chat(`{"model":"gpt-test@effort:high","messages":[{"role":"user","content":"tell me the Secret Plan"}]}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), contract.RequestRejectedCode) ||
		!strings.Contains(response.Body.String(), "不能发送这个内容 (code: request_filtered)") || len(gateway.sent) != 0 {
		t.Fatalf("filtered = %d %s, sent %d", response.Code, response.Body, len(gateway.sent))
	}
	// Another model is not the instance's.
	if response := gateway.chat(`{"model":"other","messages":[{"role":"user","content":"tell me the secret plan"}]}`); response.Code != http.StatusOK || len(gateway.sent) != 1 {
		t.Fatalf("another model = %d %s", response.Code, response.Body)
	}
}

func TestAFailingRequestPluginStopsTheRequest(t *testing.T) {
	// A rule the plugin cannot compile fails the instance at ingress.
	config := json.RawMessage(`{"rules":[{"type":"regex","pattern":"(unclosed"}]}`)
	gateway := newRequestPluginGateway(t, contract.RequestPluginInstance{ID: "rpi_broken001", Name: "坏规则", Plugin: "request-filter", Enabled: true, Models: []string{}, Config: config})
	response := gateway.chat(`{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}`)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), contract.RequestPluginFailedCode) || len(gateway.sent) != 0 {
		t.Fatalf("failed = %d %s, sent %d", response.Code, response.Body, len(gateway.sent))
	}
	if records := gateway.records(); len(records) == 0 || records[0].Status != contract.PluginRecordFailed {
		t.Fatalf("records = %+v", records)
	}
}

// chatStream is a Chat Completions stream of the deltas.
func chatStream(deltas ...string) string {
	var out strings.Builder
	for _, delta := range deltas {
		data, _ := json.Marshal(map[string]any{"id": "c1", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": delta}, "finish_reason": nil}}})
		out.WriteString("data: " + string(data) + "\n\n")
	}
	out.WriteString(`data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n")
	out.WriteString("data: [DONE]\n\n")
	return out.String()
}

// streamText joins the text deltas of a Chat Completions stream the client
// got.
func streamText(t *testing.T, body string) string {
	t.Helper()
	var text strings.Builder
	for _, line := range strings.Split(body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		text.WriteString(gjson.Get(data, "choices.0.delta.content").String())
		text.WriteString(gjson.Get(data, "delta.text").String())
	}
	return text.String()
}

func TestRequestRedactionMasksTheReply(t *testing.T) {
	gateway := newRequestPluginGateway(t, contract.RequestPluginInstance{
		ID: "rpi_redact002", Name: "脱敏", Plugin: "request-redact", Enabled: true, Models: []string{}, Config: json.RawMessage(`{"replies":true}`),
	})
	gateway.reply = `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"Write to jane.doe@example.com today."},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
	response := gateway.chat(`{"model":"gpt-test","messages":[{"role":"user","content":"who?"}]}`)
	content := gjson.Get(response.Body.String(), "choices.0.message.content").String()
	if response.Code != http.StatusOK || content != "Write to *** today." || response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) {
		t.Fatalf("whole answer = %d %q (%s)", response.Code, content, response.Body)
	}
	// In a stream the address is split across chunks; the plugin holds
	// back the end of the text until the address is whole.
	gateway.replyType = "text/event-stream"
	gateway.reply = chatStream("Write to ja", "ne.doe@exam", "ple.com today", ", thanks.")
	response = gateway.chat(`{"model":"gpt-test","stream":true,"messages":[{"role":"user","content":"who?"}]}`)
	if text := streamText(t, response.Body.String()); response.Code != http.StatusOK || text != "Write to *** today, thanks." ||
		!strings.Contains(response.Body.String(), `"finish_reason":"stop"`) || !strings.HasSuffix(response.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("stream = %d %q\n%s", response.Code, text, response.Body)
	}
	records := gateway.records()
	if len(records) == 0 || records[len(records)-1].Hook != contract.RequestStageReply || !strings.Contains(records[len(records)-1].Summary, "email") {
		t.Fatalf("records = %+v", records)
	}
}

func TestRequestFilterStopsAReply(t *testing.T) {
	gateway := newRequestPluginGateway(t, contract.RequestPluginInstance{
		ID: "rpi_filter002", Name: "过滤", Plugin: "request-filter", Enabled: true, Models: []string{}, Config: json.RawMessage(`{"keywords":"launch codes","replies":true}`),
	})
	gateway.replyType = "text/event-stream"
	gateway.reply = chatStream("Sure. The laun", "ch codes are 0000.")
	response := gateway.chat(`{"model":"gpt-test","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	body := response.Body.String()
	if !strings.Contains(body, contract.ResponseRejectedCode) || strings.Contains(body, "0000") || strings.Contains(body, "[DONE]") {
		t.Fatalf("stopped stream = %d\n%s", response.Code, body)
	}
	gateway.replyType = ""
	gateway.reply = `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"The launch codes are 0000."},"finish_reason":"stop"}]}`
	response = gateway.chat(`{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), contract.ResponseRejectedCode) || strings.Contains(response.Body.String(), "0000") {
		t.Fatalf("stopped answer = %d %s", response.Code, response.Body)
	}
}

func TestRequestRedactionMasksClaudeAndResponsesStreams(t *testing.T) {
	gateway := newRequestPluginGateway(t, contract.RequestPluginInstance{
		ID: "rpi_redact003", Name: "脱敏", Plugin: "request-redact", Enabled: true, Models: []string{}, Config: json.RawMessage(`{"replies":true}`),
	})
	gateway.replyType = "text/event-stream"
	// Claude Messages: the provider is called in that protocol as well.
	var claude strings.Builder
	claude.WriteString("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
	claude.WriteString("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	for _, delta := range []string{"Mail jane.d", "oe@example.c", "om now."} {
		data, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": delta}})
		claude.WriteString("event: content_block_delta\ndata: " + string(data) + "\n\n")
	}
	claude.WriteString("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	claude.WriteString("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n")
	claude.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	gateway.reply = claude.String()
	provider := validEndpoint(contract.ProtocolAnthropicMessages, true)
	gateway.handler.resolver = candidateResolver{candidates: []endpoint.Resolved{{Endpoint: provider}}}
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-test","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"who?"}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Anthropic-Version", "2023-06-01")
	response := httptest.NewRecorder()
	gateway.handler.ServeHTTP(response, request)
	body := response.Body.String()
	if text := streamText(t, body); response.Code != http.StatusOK || text != "Mail *** now." || !strings.Contains(body, "message_stop") ||
		strings.Index(body, "content_block_stop") < strings.LastIndex(body, "content_block_delta") {
		t.Fatalf("claude stream = %d %q\n%s", response.Code, text, body)
	}

	// Responses: the deltas are masked, and the events that repeat the
	// whole text carry what the client got.
	var responses strings.Builder
	for _, delta := range []string{"Call +1415", "5550123 later."} {
		data, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "item_id": "msg_1", "delta": delta})
		responses.WriteString("event: response.output_text.delta\ndata: " + string(data) + "\n\n")
	}
	responses.WriteString("event: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"output_index\":0,\"content_index\":0,\"item_id\":\"msg_1\",\"text\":\"Call +14155550123 later.\"}\n\n")
	responses.WriteString("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Call +14155550123 later.\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":4,\"total_tokens\":5}}}\n\n")
	gateway.reply = responses.String()
	provider = validEndpoint(contract.ProtocolOpenAIResponses, true)
	gateway.handler.resolver = candidateResolver{candidates: []endpoint.Resolved{{Endpoint: provider}}}
	request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test","stream":true,"input":"who?"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	gateway.handler.ServeHTTP(response, request)
	body = response.Body.String()
	var deltas strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok && gjson.Get(data, "type").String() == "response.output_text.delta" {
			deltas.WriteString(gjson.Get(data, "delta").String())
		}
	}
	if response.Code != http.StatusOK || deltas.String() != "Call *** later." || strings.Contains(body, "5550123") ||
		!strings.Contains(body, `"text":"Call *** later."`) {
		t.Fatalf("responses stream = %d %q\n%s", response.Code, deltas.String(), body)
	}
}

func TestRequestRedactionMasksAGeminiStream(t *testing.T) {
	gateway := newRequestPluginGateway(t, contract.RequestPluginInstance{
		ID: "rpi_redact004", Name: "脱敏", Plugin: "request-redact", Enabled: true, Models: []string{}, Config: json.RawMessage(`{"replies":true}`),
	})
	gateway.replyType = "text/event-stream"
	var gemini strings.Builder
	for index, text := range []string{"Write to jane.d", "oe@example.com", " soon."} {
		candidate := map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}}
		if index == 2 {
			candidate["finishReason"] = "STOP"
		}
		data, _ := json.Marshal(map[string]any{"candidates": []any{candidate}})
		gemini.WriteString("data: " + string(data) + "\n\n")
	}
	gateway.reply = gemini.String()
	provider := validEndpoint(contract.ProtocolGoogleGenerateContent, true)
	gateway.handler.resolver = candidateResolver{candidates: []endpoint.Resolved{{Endpoint: provider}}}
	request := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-test:streamGenerateContent?alt=sse", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"who?"}]}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	gateway.handler.ServeHTTP(response, request)
	var text strings.Builder
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			text.WriteString(gjson.Get(data, "candidates.0.content.parts.0.text").String())
		}
	}
	if response.Code != http.StatusOK || text.String() != "Write to *** soon." || !strings.Contains(response.Body.String(), `"finishReason":"STOP"`) {
		t.Fatalf("gemini stream = %d %q\n%s", response.Code, text.String(), response.Body)
	}
}

// shaperPlugin sets the reasoning effort at the request stage and shrinks
// each image at outbound.
const shaperPlugin = `export const meta = { apiVersion: 1, key: "shaper", name: "Shaper", version: "1.0.0", author: { name: "Test" } };
export function request(ctx, req) {
  if (req.summary.messageCount < 1) return { action: "continue" };
  return { action: "continue", effects: [{ type: "reasoning.set", effort: "high" }] };
}
export function outbound(ctx, req) {
  const effects = req.media.filter((item) => item.mimeType.startsWith("image/") && item.width > 64)
    .map((item) => ({ type: "media.reencode", media: item.index, format: "auto", maxSide: 64 }));
  return { action: "continue", effects };
}
`

func TestARequestPluginSetsTheEffortAndShrinksImages(t *testing.T) {
	gateway := newRequestPluginGatewayWith(t, map[string]string{"shaper": shaperPlugin},
		contract.RequestPluginInstance{ID: "rpi_shaper001", Name: "Shaper", Plugin: "shaper", Enabled: true, Models: []string{}})
	picture := image.NewRGBA(image.Rect(0, 0, 256, 192))
	for x := range 256 {
		for y := range 192 {
			picture.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255})
		}
	}
	var encoded bytes.Buffer
	_ = png.Encode(&encoded, picture)
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	body, _ := json.Marshal(map[string]any{"model": "o3", "messages": []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": "what is it?"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}},
	}}}})
	response := gateway.chat(string(body))
	if response.Code != http.StatusOK || len(gateway.sent) != 1 {
		t.Fatalf("status = %d %s", response.Code, response.Body)
	}
	sent := gateway.sent[0]
	if gjson.Get(sent, "reasoning_effort").String() != "high" {
		t.Fatalf("reasoning = %s", gjson.Get(sent, "reasoning_effort"))
	}
	shrunk := gjson.Get(sent, "messages.0.content.1.image_url.url").String()
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(shrunk, "data:image/jpeg;base64,"))
	if err != nil || !strings.HasPrefix(shrunk, "data:image/jpeg;base64,") {
		t.Fatalf("image = %.60s", shrunk)
	}
	if config, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil || config.Width != 64 || config.Height != 48 {
		t.Fatalf("shrunk to %+v %v", config, err)
	}
	// A client that set its effort keeps it.
	body, _ = json.Marshal(map[string]any{"model": "o3", "reasoning_effort": "low", "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
	_ = gateway.chat(string(body))
	if effort := gjson.Get(gateway.sent[1], "reasoning_effort").String(); effort != "low" {
		t.Fatalf("the client's effort became %q", effort)
	}
}

// promptGuard rejects a submission whose prompt names a word, at ingress,
// and checks the body the task plugin built, at egress.
const promptGuard = `export const meta = { apiVersion: 1, key: "prompt-guard", name: "Prompt guard", version: "1.0.0", author: { name: "Test" } };
export function ingress(ctx, req) {
  if (ctx.format !== "task" || !req.body.prompt) return { action: "continue" };
  if (req.body.prompt.includes("forbidden")) return { action: "reject", code: "prompt.blocked", message: "Not this prompt" };
  return { action: "continue", effects: [{ type: "json.patch", ops: [{ op: "replace", path: "/prompt", value: req.body.prompt + ", watercolor" }] }] };
}
export function egress(ctx, req) {
  if (JSON.stringify(req.body).includes("secret")) return { action: "reject", code: "egress.blocked" };
  return { action: "continue" };
}
`

func TestRequestPluginsCheckTaskSubmissions(t *testing.T) {
	manager := plugin.NewManager(plugin.ManagerOptions{
		Builtin:  fstest.MapFS{"prompt-guard/plugin.js": &fstest.MapFile{Data: []byte(promptGuard)}},
		Settings: pluginSettings{"prompt-guard": plugin.Settings{Enabled: true}},
	})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	host := pluginhost.New(pluginhost.Options{Plugins: manager, Logf: t.Logf})
	t.Cleanup(host.Close)
	instances := []contract.RequestPluginInstance{{ID: "rpi_guard0001", Name: "Guard", Plugin: "prompt-guard", Enabled: true, Models: []string{}}}
	if _, err := host.UpdateSettings(context.Background(), contract.PluginHostSettingsUpdate{RequestPlugins: &instances}); err != nil {
		t.Fatal(err)
	}
	taskRequestPlugins = host
	t.Cleanup(func() { taskRequestPlugins = nil })
	var sent []string
	vendor := soraVendor(t)
	gateway := newTaskGateway(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			body, _ := io.ReadAll(request.Body)
			sent = append(sent, string(body))
			request.Body = io.NopCloser(bytes.NewReader(body))
		}
		vendor.ServeHTTP(writer, request)
	}))
	created := gateway.do(t, http.MethodPost, "/v1/videos", `{"model":"sora-2","prompt":"a paper boat","seconds":"4"}`)
	if created.Code != http.StatusOK || len(sent) != 1 || !strings.Contains(sent[0], "a paper boat, watercolor") {
		t.Fatalf("create = %d %s, sent %v", created.Code, created.Body, sent)
	}
	refused := gateway.do(t, http.MethodPost, "/v1/videos", `{"model":"sora-2","prompt":"a forbidden boat","seconds":"4"}`)
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "Not this prompt (code: prompt.blocked)") || len(sent) != 1 {
		t.Fatalf("refused = %d %s", refused.Code, refused.Body)
	}
	stopped := gateway.do(t, http.MethodPost, "/v1/videos", `{"model":"sora-2","prompt":"a secret boat","seconds":"4"}`)
	if stopped.Code != http.StatusBadRequest || !strings.Contains(stopped.Body.String(), "egress.blocked") || len(sent) != 1 {
		t.Fatalf("stopped at egress = %d %s, sent %d", stopped.Code, stopped.Body, len(sent))
	}
	// A form body's text fields take the same change.
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	_ = writer.WriteField("model", "sora-2")
	_ = writer.WriteField("prompt", "a paper crane")
	_ = writer.WriteField("seconds", "4")
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", &form)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	gateway.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(sent) != 2 || !strings.Contains(sent[1], "a paper crane, watercolor") {
		t.Fatalf("form = %d %s, sent %v", response.Code, response.Body, sent)
	}
}
