package pluginhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// convertingPlugin converts image edits and chats into a vendor's API: a
// form's files in a JSON body, a form or the whole body; a chat over an
// AWS event stream, over a WebSocket session, or failing before its first
// event; and a token the vendor rejects once.
const convertingPlugin = `export const meta = {
  apiVersion: 1, key: "vendor", name: "Vendor", version: "1.0.0", author: { name: "Test" }, baseUrl: "https://api.vendor.example",
  models: ["acme-1"],
  relay: [
    { incoming_path: "/v1/images/edits", upstream_path: "/edit", converter: "plugin" },
    { incoming_path: "/v1/chat/completions", upstream_path: "/chat", converter: "plugin" },
  ],
};
export function exchangeKey(ctx) { return { token: { value: utils.uuid() } }; }
export function signRequest(ctx) { return { headers: { "X-Token": ctx.token.value } }; }
const chunk = (content, finish) => ({ id: "c1", object: "chat.completion.chunk", choices: [{ index: 0, delta: content ? { content } : {}, finish_reason: finish || null }] });
const reply = (text) => ({ id: "c1", object: "chat.completion", choices: [{ index: 0, message: { role: "assistant", content: text }, finish_reason: "stop" }] });
export function convertRequest(ctx, request) {
  if (request.form) {
    const prompt = request.form.fields.prompt;
    if (request.form.files.length !== 2 || request.form.files[0].size !== 3) throw new Error("files " + JSON.stringify(request.form.files));
    if (prompt === "as json") return { body: { prompt, model: request.model, image: { "$file": "image", encoding: "dataUrl" }, mask: { "$file": "image#1" } } };
    if (prompt === "as file") return { file: "image", path: "/upload" };
    if (prompt === "missing") return { file: "nothing" };
    return { form: { fields: { text: prompt, model: request.model }, files: [{ field: "picture", from: "image", filename: "p.png" }] } };
  }
  const prompt = request.body.messages[0].content;
  const state = { stream: request.stream, text: "", socket: prompt === "socket" };
  if (prompt === "socket") return { websocket: { path: "/socket?model=" + request.model, headers: { "X-Session": "s1" }, protocols: ["vendor.v1"], messages: [JSON.stringify({ type: "start", text: prompt })] }, state };
  return { body: { prompt }, path: "/" + prompt, stream: prompt !== "token", state };
}
export function convertResponse(ctx, response) {
  if (response.body.includes("expired")) return { error: { status: 401, message: "expired" } };
  if (ctx.path === "/v1/images/edits") return { body: { created: 1, data: [{ b64_json: "aGk=" }] } };
  return { body: reply(JSON.parse(response.body).text) };
}
export function convertStreamEvent(ctx, input) {
  const state = ctx.state;
  if (input.done) return state.stream ? { events: [{ data: "[DONE]" }] } : {};
  if (input.event === "quota" || input.data === "quota") return { error: { status: 429, message: "quota used up" } };
  if (input.data === "audio") return { chunks: [{ bodyBase64: "aGk=" }], contentType: "audio/mpeg" };
  const event = JSON.parse(input.data);
  if (event.end) {
    if (!state.stream) return { body: reply(state.text), send: [JSON.stringify({ type: "bye" })] };
    return { events: [{ data: chunk("", "stop") }], close: state.socket };
  }
  if (!state.stream) return { state: { ...state, text: state.text + event.delta } };
  return { events: [{ data: chunk(event.delta) }] };
}
`

type convertingUpstream struct {
	server *httptest.Server
	mutex  sync.Mutex
	seen   []*http.Request
	bodies [][]byte
	socket []string
	tokens []string
}

func (upstream *convertingUpstream) last(t *testing.T) (*http.Request, []byte) {
	t.Helper()
	upstream.mutex.Lock()
	defer upstream.mutex.Unlock()
	if len(upstream.seen) == 0 {
		t.Fatal("the upstream got no request")
	}
	return upstream.seen[len(upstream.seen)-1], upstream.bodies[len(upstream.bodies)-1]
}

// awsMessage is one message of an AWS event stream.
func awsMessage(eventType, payload string) []byte {
	var headers bytes.Buffer
	for name, value := range map[string]string{":event-type": eventType, ":message-type": "event"} {
		headers.WriteByte(byte(len(name)))
		headers.WriteString(name)
		headers.WriteByte(7)
		_ = binary.Write(&headers, binary.BigEndian, uint16(len(value)))
		headers.WriteString(value)
	}
	total := 16 + headers.Len() + len(payload)
	message := make([]byte, 0, total)
	message = binary.BigEndian.AppendUint32(message, uint32(total))
	message = binary.BigEndian.AppendUint32(message, uint32(headers.Len()))
	message = binary.BigEndian.AppendUint32(message, crc32.ChecksumIEEE(message[:8]))
	message = append(message, headers.Bytes()...)
	message = append(message, payload...)
	return binary.BigEndian.AppendUint32(message, crc32.ChecksumIEEE(message))
}

func newConvertingUpstream(t *testing.T) *convertingUpstream {
	upstream := &convertingUpstream{}
	upgrader := websocket.Upgrader{Subprotocols: []string{"vendor.v1"}}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/base/socket" {
			if request.Header.Get("X-Session") != "s1" || request.URL.Query().Get("model") != "acme-1" {
				http.Error(writer, "bad handshake", http.StatusBadRequest)
				return
			}
			conn, err := upgrader.Upgrade(writer, request, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			_, start, _ := conn.ReadMessage()
			upstream.mutex.Lock()
			upstream.socket = append(upstream.socket, conn.Subprotocol(), string(start))
			upstream.mutex.Unlock()
			for _, frame := range []string{`{"delta":"he"}`, `{"delta":"llo"}`, `{"end":true}`} {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(frame))
			}
			// The client says bye or closes the session.
			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					return
				}
				upstream.mutex.Lock()
				upstream.socket = append(upstream.socket, string(message))
				upstream.mutex.Unlock()
			}
		}
		body, _ := io.ReadAll(request.Body)
		upstream.mutex.Lock()
		upstream.seen = append(upstream.seen, request.Clone(context.Background()))
		upstream.bodies = append(upstream.bodies, body)
		upstream.mutex.Unlock()
		switch request.URL.Path {
		case "/base/aws":
			writer.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
			_, _ = writer.Write(awsMessage("delta", `{"delta":"he"}`))
			_, _ = writer.Write(awsMessage("delta", `{"delta":"llo"}`))
			_, _ = writer.Write(awsMessage("delta", `{"end":true}`))
		case "/base/quota":
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, "event: quota\ndata: {}\n\n")
		case "/base/speech":
			writer.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(writer, "audio\n\naudio\n")
		case "/base/token":
			writer.Header().Set("Content-Type", "application/json")
			upstream.mutex.Lock()
			upstream.tokens = append(upstream.tokens, request.Header.Get("X-Token"))
			first := len(upstream.tokens) == 1
			upstream.mutex.Unlock()
			if first {
				_, _ = io.WriteString(writer, `{"error":"token expired"}`)
				return
			}
			_, _ = io.WriteString(writer, `{"text":"fresh"}`)
		default:
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"ok":true}`)
		}
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

// converting runs one request through a provider of convertingPlugin.
type converting struct {
	host    *Host
	service contract.Service
	target  transport.Target
}

func newConverting(t *testing.T, upstream *convertingUpstream) *converting {
	t.Helper()
	manager := plugin.NewManager(plugin.ManagerOptions{
		Builtin:  fstest.MapFS{"vendor/plugin.js": &fstest.MapFile{Data: []byte(convertingPlugin)}},
		Settings: &settingsBox{settings: settingsSource{"vendor": plugin.Settings{Enabled: true}}},
	})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loaded := manager.Current().Plugin("vendor"); !loaded.Loaded() || loaded.NewAPI() == nil {
		t.Fatalf("vendor did not load: %v", loaded.Err)
	}
	host := New(Options{Plugins: manager, Logf: t.Logf})
	t.Cleanup(host.Close)
	service := contract.Service{
		ID: "service_vendor", Name: "Vendor", Kind: contract.ServiceKindPlugin, Enabled: true, Models: []string{"acme-1"},
		HTTP:   &contract.HTTPConnection{BaseURL: upstream.server.URL + "/base", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}},
		Plugin: &contract.ServicePlugin{Key: "vendor", Provider: contract.PluginProviderRelay},
	}
	base, _ := url.Parse(upstream.server.URL + "/base")
	return &converting{host: host, service: service, target: transport.Target{
		Service: service, BaseURL: base, RequestHeaders: http.Header{"Authorization": {"Bearer sk-test"}},
	}}
}

func (converting *converting) send(t *testing.T, path, contentType string, body []byte, stream bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	protocol := contract.ProtocolOpenAIChat
	if path == "/v1/images/edits" {
		protocol = contract.ProtocolOpenAIImages
	}
	attempt := ProviderAttempt{Protocol: protocol, Model: "acme-1", Stream: stream, ClientPath: path}
	if err := converting.host.ForwardProvider(recorder, request, converting.target, attempt, transport.New(nil).RoundTrip); err != nil {
		var upstream *transport.UpstreamError
		if errorsAs(err, &upstream) {
			recorder.Code = http.StatusBadGateway
			recorder.Body = bytes.NewBufferString(err.Error())
			return recorder
		}
		t.Fatalf("forward: %v", err)
	}
	return recorder
}

func errorsAs(err error, target **transport.UpstreamError) bool {
	upstream, ok := err.(*transport.UpstreamError)
	if ok {
		*target = upstream
	}
	return ok
}

func imageForm(t *testing.T, prompt string) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.WriteField("prompt", prompt)
	_ = writer.WriteField("model", "client-model")
	for _, content := range []string{"one", "two"} {
		header := map[string][]string{
			"Content-Disposition": {`form-data; name="image"; filename="` + content + `.png"`},
			"Content-Type":        {"image/png"},
		}
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(part, content)
	}
	_ = writer.Close()
	return buffer.Bytes(), writer.FormDataContentType()
}

func chat(prompt string) []byte {
	data, _ := json.Marshal(map[string]any{"model": "acme-1", "messages": []map[string]string{{"role": "user", "content": prompt}}})
	return data
}

func TestAConvertedRouteSendsTheClientsFormFiles(t *testing.T) {
	upstream := newConvertingUpstream(t)
	converting := newConverting(t, upstream)

	body, contentType := imageForm(t, "as json")
	if response := converting.send(t, "/v1/images/edits", contentType, body, false); response.Code != http.StatusOK {
		t.Fatalf("as json = %d %s", response.Code, response.Body)
	}
	sent, sentBody := upstream.last(t)
	if gjson.GetBytes(sentBody, "image").String() != "data:image/png;base64,"+base64.StdEncoding.EncodeToString([]byte("one")) ||
		gjson.GetBytes(sentBody, "mask").String() != base64.StdEncoding.EncodeToString([]byte("two")) ||
		gjson.GetBytes(sentBody, "model").String() != "acme-1" || sent.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("as json sent %s %s", sent.Header.Get("Content-Type"), sentBody)
	}

	body, contentType = imageForm(t, "as file")
	if response := converting.send(t, "/v1/images/edits", contentType, body, false); response.Code != http.StatusOK {
		t.Fatalf("as file = %d %s", response.Code, response.Body)
	}
	if sent, sentBody := upstream.last(t); sent.URL.Path != "/base/upload" || string(sentBody) != "one" || sent.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("as file sent %s %s %q", sent.URL.Path, sent.Header.Get("Content-Type"), sentBody)
	}

	body, contentType = imageForm(t, "as form")
	if response := converting.send(t, "/v1/images/edits", contentType, body, false); response.Code != http.StatusOK {
		t.Fatalf("as form = %d %s", response.Code, response.Body)
	}
	sent, sentBody = upstream.last(t)
	_, params, _ := mime.ParseMediaType(sent.Header.Get("Content-Type"))
	reader := multipart.NewReader(bytes.NewReader(sentBody), params["boundary"])
	parts := map[string]string{}
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(part)
		parts[part.FormName()+"|"+part.FileName()+"|"+part.Header.Get("Content-Type")] = string(data)
	}
	if parts["text||"] != "as form" || parts["model||"] != "acme-1" || parts["picture|p.png|image/png"] != "one" {
		t.Fatalf("as form sent %v", parts)
	}

	// A file the client did not send fails the conversion: nothing is sent.
	before := len(upstream.seen)
	body, contentType = imageForm(t, "missing")
	if response := converting.send(t, "/v1/images/edits", contentType, body, false); response.Code != http.StatusBadGateway || len(upstream.seen) != before {
		t.Fatalf("a missing file = %d %s", response.Code, response.Body)
	}
}

func TestAConvertedRouteReadsStreamsAndSessions(t *testing.T) {
	upstream := newConvertingUpstream(t)
	converting := newConverting(t, upstream)

	// An AWS event stream comes message by message.
	response := converting.send(t, "/v1/chat/completions", "application/json", chat("aws"), true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"he"`) || !strings.Contains(response.Body.String(), `"content":"llo"`) ||
		!strings.Contains(response.Body.String(), "data: [DONE]") {
		t.Fatalf("aws = %d %s", response.Code, response.Body)
	}

	// An error before the first event answers as an error, with its status.
	response = converting.send(t, "/v1/chat/completions", "application/json", chat("quota"), true)
	if response.Code != http.StatusTooManyRequests || gjson.Get(response.Body.String(), "error.message").String() != "quota used up" {
		t.Fatalf("quota = %d %s", response.Code, response.Body)
	}

	// A WebSocket session: the frames become events until the hook closes it.
	response = converting.send(t, "/v1/chat/completions", "application/json", chat("socket"), true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"llo"`) || !strings.Contains(response.Body.String(), `"finish_reason":"stop"`) {
		t.Fatalf("socket = %d %s", response.Code, response.Body)
	}
	// A client that does not stream gets the body, after which the session
	// closes; the hook's last message goes first.
	response = converting.send(t, "/v1/chat/completions", "application/json", chat("socket"), false)
	if response.Code != http.StatusOK || gjson.Get(response.Body.String(), "choices.0.message.content").String() != "hello" {
		t.Fatalf("socket whole = %d %s", response.Code, response.Body)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		upstream.mutex.Lock()
		got := strings.Join(upstream.socket, " ")
		upstream.mutex.Unlock()
		if strings.Contains(got, `{"type":"bye"}`) {
			if !strings.HasPrefix(got, `vendor.v1 {"type":"start","text":"socket"}`) {
				t.Fatalf("the session = %s", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session = %s", got)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A client that does not stream takes binary chunks.
	response = converting.send(t, "/v1/chat/completions", "application/json", chat("speech"), false)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "audio/mpeg" || response.Body.String() != "hihi" {
		t.Fatalf("speech = %d %s %q", response.Code, response.Header().Get("Content-Type"), response.Body)
	}
}

func TestAConvertedRouteRenewsATokenTheHooksSayIsRejected(t *testing.T) {
	upstream := newConvertingUpstream(t)
	converting := newConverting(t, upstream)
	response := converting.send(t, "/v1/chat/completions", "application/json", chat("token"), false)
	if response.Code != http.StatusOK || gjson.Get(response.Body.String(), "choices.0.message.content").String() != "fresh" {
		t.Fatalf("answer = %d %s", response.Code, response.Body)
	}
	// The hook said the first token was rejected: the key was exchanged
	// again and the request sent once more with the new token.
	upstream.mutex.Lock()
	defer upstream.mutex.Unlock()
	if len(upstream.tokens) != 2 || upstream.tokens[0] == "" || upstream.tokens[0] == upstream.tokens[1] {
		t.Fatalf("tokens = %v", upstream.tokens)
	}
}

func TestAnAWSEventStreamFailsOnABadCRC(t *testing.T) {
	message := awsMessage("delta", `{"delta":"x"}`)
	events := 0
	if err := readAWSEvents(bytes.NewReader(message), func(map[string]any) (bool, error) { events++; return false, nil }); err != nil || events != 1 {
		t.Fatalf("a good message = %d %v", events, err)
	}
	message[len(message)-6] ^= 0xff
	if err := readAWSEvents(bytes.NewReader(message), func(map[string]any) (bool, error) { return false, nil }); err == nil {
		t.Fatal("a bad CRC was read")
	}
	if err := readAWSEvents(bytes.NewReader(awsMessage("delta", "x")[:20]), func(map[string]any) (bool, error) { return false, nil }); err == nil {
		t.Fatal("a truncated message was read")
	}
}
