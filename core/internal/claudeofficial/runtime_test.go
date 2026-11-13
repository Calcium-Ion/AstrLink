package claudeofficial

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/claudecli"
	"github.com/QuantumNous/astrlink/core/internal/claudecli/fakeclaude"
)

func TestMain(m *testing.M) {
	fakeclaude.Main()
	os.Exit(m.Run())
}

// upstreamCall is one model request the fake CLI made.
type upstreamCall struct {
	header http.Header
	body   map[string]any
}

// fakeUpstream stands in for the loopback endpoint; reply scripts each call.
type fakeUpstream struct {
	server *httptest.Server
	reply  func(index int, body map[string]any, writer http.ResponseWriter)

	mu    sync.Mutex
	calls []upstreamCall
}

func newFakeUpstream(t *testing.T, reply func(int, map[string]any, http.ResponseWriter)) *fakeUpstream {
	t.Helper()
	upstream := &fakeUpstream{reply: reply}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		upstream.mu.Lock()
		index := len(upstream.calls)
		upstream.calls = append(upstream.calls, upstreamCall{header: request.Header.Clone(), body: body})
		upstream.mu.Unlock()
		upstream.reply(index, body, writer)
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (upstream *fakeUpstream) call(t *testing.T, index int) upstreamCall {
	t.Helper()
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if index >= len(upstream.calls) {
		t.Fatalf("upstream saw %d calls, want call %d", len(upstream.calls), index)
	}
	return upstream.calls[index]
}

func (upstream *fakeUpstream) secrets() []string {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	var secrets []string
	for _, call := range upstream.calls {
		if secret := call.header.Get(ProcessHeader); !slices.Contains(secrets, secret) {
			secrets = append(secrets, secret)
		}
	}
	return secrets
}

func writeSSE(writer http.ResponseWriter, events ...map[string]any) {
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	for _, event := range events {
		raw, _ := json.Marshal(event)
		fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event["type"], raw)
	}
}

func textReply(writer http.ResponseWriter, id, text string) {
	writeSSE(writer,
		map[string]any{"type": "message_start", "message": map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": "claude-sonnet-5-5", "content": []any{},
			"usage": map[string]any{"input_tokens": 10, "cache_read_input_tokens": 4, "output_tokens": 1},
		}},
		map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}},
		map[string]any{"type": "content_block_stop", "index": 0},
		map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 7}},
		map[string]any{"type": "message_stop"},
	)
}

func toolReply(writer http.ResponseWriter, id, toolID, name, input string) {
	writeSSE(writer,
		map[string]any{"type": "message_start", "message": map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": "claude-sonnet-5-5", "content": []any{},
			"usage": map[string]any{"input_tokens": 20, "output_tokens": 1},
		}},
		map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Checking."}},
		map[string]any{"type": "content_block_stop", "index": 0},
		map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{
			"type": "tool_use", "id": toolID, "name": name, "input": map[string]any{},
		}},
		map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": input}},
		map[string]any{"type": "content_block_stop", "index": 1},
		map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"output_tokens": 12}},
		map[string]any{"type": "message_stop"},
	)
}

type testEnv struct {
	runtime   *Runtime
	upstream  *fakeUpstream
	call      Call
	configDir string
	signedOut chan contract.ServiceID
}

func newTestEnv(t *testing.T, options Options, reply func(int, map[string]any, http.ResponseWriter)) *testEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is linked by symlink")
	}
	root := t.TempDir()
	binary := fakeclaude.Install(t, t.TempDir(), fakeclaude.Settings{})
	layout := claudecli.Layout{Root: filepath.Join(root, "accounts"), WorkDir: filepath.Join(root, "work")}
	service := contract.Service{
		ID: "service_claude_one", Kind: contract.ServiceKindClaudeSubscription,
		Subscription: &contract.SubscriptionConnection{Provider: contract.SubscriptionProviderClaudeCode, Mode: contract.SubscriptionModeOfficial},
	}
	configDir, err := layout.EnsureConfigDir(service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fakeclaude.AccountFile(configDir), []byte(`{"email":"person@example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := &testEnv{configDir: configDir, signedOut: make(chan contract.ServiceID, 4)}
	options.Accounts = claudecli.Accounts{
		Locator: &claudecli.Locator{Configured: binary}, Layout: layout,
		Environ: []string{"PATH=/usr/bin:/bin", "HOME=" + root, "HTTPS_PROXY=http://proxy.invalid:1", "ANTHROPIC_API_KEY=parent-key"},
	}
	options.SignedOut = func(id contract.ServiceID) { env.signedOut <- id }
	env.runtime = New(options)
	env.upstream = newFakeUpstream(t, reply)
	env.runtime.SetBaseURL(env.upstream.server.URL)
	env.call = Call{Service: service, BaseURL: "https://api.anthropic.com", RecordID: "request_outer_1"}
	t.Cleanup(env.runtime.Close)
	return env
}

func (env *testEnv) send(t *testing.T, ctx context.Context, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(WithCall(ctx, env.call), http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response, err := env.runtime.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readAll(t *testing.T, response *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

type sseEvent struct {
	name string
	data map[string]any
}

func parseSSE(t *testing.T, text string) []sseEvent {
	t.Helper()
	var events []sseEvent
	var name string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			name = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			var data map[string]any
			if err := json.Unmarshal([]byte(value), &data); err != nil {
				t.Fatalf("bad SSE data %q", value)
			}
			events = append(events, sseEvent{name: name, data: data})
		}
	}
	return events
}

func messageText(message map[string]any) string {
	switch content := message["content"].(type) {
	case string:
		return content
	case []any:
		var parts []string
		for _, item := range content {
			entry, _ := item.(map[string]any)
			if text, ok := entry["text"].(string); ok {
				parts = append(parts, text)
			}
			if entry["type"] == "tool_result" {
				parts = append(parts, "tool_result:"+fmt.Sprint(entry["tool_use_id"])+"="+toolResultText(entry["content"]))
			}
		}
		return strings.Join(parts, "|")
	}
	return ""
}

func upstreamMessages(call upstreamCall) []string {
	items, _ := call.body["messages"].([]any)
	var texts []string
	for _, item := range items {
		message, _ := item.(map[string]any)
		texts = append(texts, fmt.Sprint(message["role"])+":"+messageText(message))
	}
	return texts
}

func TestStreamsATextReplyAndCallsBackThroughLoopback(t *testing.T) {
	env := newTestEnv(t, Options{}, func(index int, body map[string]any, writer http.ResponseWriter) {
		textReply(writer, "msg_1", "Hello there.")
	})
	response := env.send(t, context.Background(), `{"model":"claude-sonnet-5-5","stream":true,"max_tokens":1024,"temperature":0.2,
		"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=1"},{"type":"text","text":"Be brief."}],
		"messages":[{"role":"user","content":"Hi"}]}`)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("response = %d %v", response.StatusCode, response.Header)
	}
	events := parseSSE(t, readAll(t, response))
	var names []string
	for _, event := range events {
		names = append(names, event.name)
	}
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if !slices.Equal(names, want) {
		t.Fatalf("events = %v", names)
	}
	delta := events[4].data
	usage, _ := delta["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) || usage["cache_read_input_tokens"] != float64(4) || usage["output_tokens"] != float64(7) {
		t.Fatalf("final usage = %v", usage)
	}
	if reason := delta["delta"].(map[string]any)["stop_reason"]; reason != "end_turn" {
		t.Fatalf("stop_reason = %v", reason)
	}

	call := env.upstream.call(t, 0)
	if call.header.Get("Authorization") != "Bearer "+fakeclaude.FakeToken {
		t.Fatalf("Authorization = %q", call.header.Get("Authorization"))
	}
	secret := call.header.Get(ProcessHeader)
	loopback, ok := env.runtime.Loopback(secret)
	if !ok || loopback.RecordID != env.call.RecordID || loopback.Service.ID != env.call.Service.ID {
		t.Fatalf("Loopback(%q) = %+v %v", secret, loopback, ok)
	}
	if _, ok := env.runtime.Loopback("not-a-secret"); ok {
		t.Fatal("an unknown secret resolved")
	}
	system := fmt.Sprint(call.body["system"])
	if !strings.Contains(system, "Be brief.") || strings.Contains(system, "cc_version=1") {
		t.Fatalf("system = %s", system)
	}
	if got := upstreamMessages(call); !slices.Equal(got, []string{"user:Hi"}) {
		t.Fatalf("messages = %v", got)
	}

	args, err := os.ReadFile(fakeclaude.ServeArgsFile(env.configDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--tools=", "--setting-sources=", "--strict-mcp-config", "dontAsk", "--thinking=disabled", `{"disableAllHooks":true}`} {
		if !strings.Contains(string(args), flag) {
			t.Fatalf("args lack %s:\n%s", flag, args)
		}
	}
	if strings.Contains(string(args), "--bare") || strings.Contains(string(args), "-p\n") {
		t.Fatalf("args = %s", args)
	}
	environ, err := os.ReadFile(fakeclaude.EnvFile(env.configDir, "serve"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"CLAUDE_CODE_DISABLE_CLAUDE_MDS=1", "DISABLE_AUTO_COMPACT=1", "ENABLE_TOOL_SEARCH=false",
		"ANTHROPIC_BASE_URL=" + env.upstream.server.URL, "CLAUDE_CONFIG_DIR=" + env.configDir} {
		if !strings.Contains(string(environ), entry+"\n") {
			t.Fatalf("environment lacks %s:\n%s", entry, environ)
		}
	}
	for _, leaked := range []string{"ANTHROPIC_API_KEY", "astrlink", "AstrLink-Claude", "proxy.invalid"} {
		if strings.Contains(strings.ToLower(string(environ)), strings.ToLower(leaked)+"=") || strings.Contains(string(environ), "proxy.invalid") {
			t.Fatalf("environment carries %s:\n%s", leaked, environ)
		}
	}
	// The CLI's own traffic goes through its tunnel; model calls stay direct.
	if !regexp.MustCompile(`(?m)^HTTPS_PROXY=http://claude:[0-9a-f]{48}@127\.0\.0\.1:\d+$`).Match(environ) ||
		!strings.Contains(string(environ), "\nNO_PROXY=127.0.0.1,localhost,::1\n") {
		t.Fatalf("environment lacks the tunnel:\n%s", environ)
	}
}

func TestToolCallsRoundTripThroughTheSameProcess(t *testing.T) {
	env := newTestEnv(t, Options{}, func(index int, body map[string]any, writer http.ResponseWriter) {
		if index == 0 {
			toolReply(writer, "msg_1", "toolu_1", toolPrefix+mcpToolName("get.weather"), `{"city":"Paris"}`)
			return
		}
		textReply(writer, "msg_2", "Sunny in Paris.")
	})
	tools := `"tools":[{"name":"get.weather","description":"Weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}]`
	first := env.send(t, context.Background(), `{"model":"claude-sonnet-5-5",`+tools+`,"messages":[{"role":"user","content":"Weather in Paris?"}]}`)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first = %d %s", first.StatusCode, readAll(t, first))
	}
	var reply map[string]any
	if err := json.Unmarshal([]byte(readAll(t, first)), &reply); err != nil {
		t.Fatal(err)
	}
	content, _ := reply["content"].([]any)
	if len(content) != 2 || reply["stop_reason"] != "tool_use" {
		t.Fatalf("reply = %v", reply)
	}
	use := content[1].(map[string]any)
	if use["name"] != "get.weather" || use["id"] != "toolu_1" || fmt.Sprint(use["input"]) != "map[city:Paris]" {
		t.Fatalf("tool_use = %v", use)
	}
	if usage := reply["usage"].(map[string]any); usage["input_tokens"] != float64(20) || usage["output_tokens"] != float64(12) {
		t.Fatalf("usage = %v", usage)
	}

	second := env.send(t, context.Background(), `{"model":"claude-sonnet-5-5",`+tools+`,"stream":true,"messages":[
		{"role":"user","content":"Weather in Paris?"},
		{"role":"assistant","content":[{"type":"text","text":"Checking."},{"type":"tool_use","id":"toolu_1","name":"get.weather","input":{"city":"Paris"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"22C and sunny"}]}]}`)
	text := readAll(t, second)
	if second.StatusCode != http.StatusOK || !strings.Contains(text, "Sunny in Paris.") {
		t.Fatalf("second = %d %s", second.StatusCode, text)
	}
	if secrets := env.upstream.secrets(); len(secrets) != 1 {
		t.Fatalf("processes = %d, want the parked one reused", len(secrets))
	}
	resumed := env.upstream.call(t, 1)
	messages := upstreamMessages(resumed)
	if len(messages) != 3 || messages[2] != "user:tool_result:toolu_1=22C and sunny" {
		t.Fatalf("messages after the tool result = %v", messages)
	}
}

func TestEditedHistoryStartsFreshWithACheckpoint(t *testing.T) {
	env := newTestEnv(t, Options{}, func(index int, body map[string]any, writer http.ResponseWriter) {
		textReply(writer, fmt.Sprintf("msg_%d", index), fmt.Sprintf("Reply %d.", index))
	})
	readAll(t, env.send(t, context.Background(), `{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"One"}]}`))
	readAll(t, env.send(t, context.Background(), `{"model":"claude-sonnet-5-5","messages":[
		{"role":"user","content":"One"},{"role":"assistant","content":"Reply 0."},{"role":"user","content":"Two"}]}`))
	if secrets := env.upstream.secrets(); len(secrets) != 1 {
		t.Fatalf("a continued conversation used %d processes", len(secrets))
	}
	if got := upstreamMessages(env.upstream.call(t, 1)); !slices.Equal(got, []string{"user:One", "assistant:Reply 0.", "user:Two"}) {
		t.Fatalf("continued messages = %v", got)
	}

	readAll(t, env.send(t, context.Background(), `{"model":"claude-sonnet-5-5","messages":[
		{"role":"user","content":"Uno"},{"role":"assistant","content":"Reply 0."},{"role":"user","content":"Two"}]}`))
	if secrets := env.upstream.secrets(); len(secrets) != 2 {
		t.Fatalf("an edited history used %d processes", len(secrets))
	}
	fresh := upstreamMessages(env.upstream.call(t, 2))
	if len(fresh) != 1 || !strings.Contains(fresh[0], "<conversation-checkpoint>") ||
		!strings.Contains(fresh[0], "Uno") || !strings.HasSuffix(fresh[0], "|Two") {
		t.Fatalf("fresh messages = %v", fresh)
	}
	if strings.Contains(strings.ToLower(fresh[0]), "astrlink") {
		t.Fatalf("checkpoint names the gateway: %s", fresh[0])
	}
}

// stopIdle stops the idle conversation's process as the idle timer would.
func stopIdle(t *testing.T, env *testEnv) {
	t.Helper()
	env.runtime.mu.Lock()
	var exited <-chan struct{}
	for current := range env.runtime.conversations {
		if current.state == stateIdle {
			exited = current.process.exited
			env.runtime.sleep(current)
		}
	}
	env.runtime.mu.Unlock()
	if exited == nil {
		t.Fatal("no idle conversation")
	}
	<-exited
}

func TestStoppedConversationResumesFromItsTranscript(t *testing.T) {
	env := newTestEnv(t, Options{}, func(index int, body map[string]any, writer http.ResponseWriter) {
		textReply(writer, fmt.Sprintf("msg_%d", index), fmt.Sprintf("Reply %d.", index))
	})
	readAll(t, env.send(t, context.Background(), `{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"One"}]}`))
	stopIdle(t, env)
	readAll(t, env.send(t, context.Background(), `{"model":"claude-sonnet-5-5","messages":[
		{"role":"user","content":"One"},{"role":"assistant","content":"Reply 0."},{"role":"user","content":"Two"}]}`))
	args, err := os.ReadFile(fakeclaude.ServeArgsFile(env.configDir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--resume\n") {
		t.Fatalf("second process did not resume:\n%s", args)
	}
	if got := upstreamMessages(env.upstream.call(t, 1)); !slices.Equal(got, []string{"user:One", "assistant:Reply 0.", "user:Two"}) {
		t.Fatalf("resumed messages = %v", got)
	}

	// Without the transcript the history is replayed in a fresh process.
	stopIdle(t, env)
	if err := os.RemoveAll(filepath.Join(env.configDir, "projects")); err != nil {
		t.Fatal(err)
	}
	readAll(t, env.send(t, context.Background(), `{"model":"claude-sonnet-5-5","messages":[
		{"role":"user","content":"One"},{"role":"assistant","content":"Reply 0."},{"role":"user","content":"Two"},
		{"role":"assistant","content":"Reply 1."},{"role":"user","content":"Three"}]}`))
	fresh := upstreamMessages(env.upstream.call(t, 2))
	if len(fresh) != 1 || !strings.Contains(fresh[0], "Reply 1.") || !strings.HasSuffix(fresh[0], "|Three") {
		t.Fatalf("replayed messages = %v", fresh)
	}
}

func TestRateLimitBecomesA429WithRetryAfter(t *testing.T) {
	reset := time.Now().Add(90 * time.Second).Unix()
	env := newTestEnv(t, Options{}, func(index int, body map[string]any, writer http.ResponseWriter) {
		writer.Header().Set("Anthropic-Ratelimit-Unified-Status", "rejected")
		writer.Header().Set("Anthropic-Ratelimit-Unified-Reset", fmt.Sprint(reset))
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"limit"}}`))
	})
	response := env.send(t, context.Background(), `{"model":"claude-sonnet-5-5","stream":true,"messages":[{"role":"user","content":"Hi"}]}`)
	body := readAll(t, response)
	if response.StatusCode != http.StatusTooManyRequests || !strings.Contains(body, "rate_limit_error") {
		t.Fatalf("response = %d %s", response.StatusCode, body)
	}
	if retry := response.Header.Get("Retry-After"); retry == "" || retry == "0" {
		t.Fatalf("Retry-After = %q", retry)
	}
}

func TestSignedOutAccountReportsIt(t *testing.T) {
	env := newTestEnv(t, Options{}, func(int, map[string]any, http.ResponseWriter) {
		t.Error("a signed-out CLI reached the upstream")
	})
	if err := os.Remove(fakeclaude.AccountFile(env.configDir)); err != nil {
		t.Fatal(err)
	}
	response := env.send(t, context.Background(), `{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"Hi"}]}`)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d %s", response.StatusCode, readAll(t, response))
	}
	if id := <-env.signedOut; id != env.call.Service.ID {
		t.Fatalf("signed out %s", id)
	}
}

func TestCallerLeavingStopsTheProcess(t *testing.T) {
	release := make(chan struct{})
	env := newTestEnv(t, Options{}, func(index int, body map[string]any, writer http.ResponseWriter) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		fmt.Fprint(writer, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"m\",\"usage\":{\"input_tokens\":1}}}\n\n")
		writer.(http.Flusher).Flush()
		<-release
	})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	response := env.send(t, ctx, `{"model":"claude-sonnet-5-5","stream":true,"messages":[{"role":"user","content":"Hi"}]}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	env.runtime.mu.Lock()
	var exited <-chan struct{}
	for current := range env.runtime.conversations {
		exited = current.process.exited
	}
	env.runtime.mu.Unlock()
	cancel()
	<-exited
	env.runtime.mu.Lock()
	remaining := len(env.runtime.conversations)
	env.runtime.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d conversations remain", remaining)
	}
}

func TestProcessCapStopsTheLeastRecentlyUsedIdleProcess(t *testing.T) {
	env := newTestEnv(t, Options{MaxProcesses: 1}, func(index int, body map[string]any, writer http.ResponseWriter) {
		textReply(writer, fmt.Sprintf("msg_%d", index), fmt.Sprintf("Reply %d.", index))
	})
	readAll(t, env.send(t, context.Background(), `{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"First"}]}`))
	readAll(t, env.send(t, context.Background(), `{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"Second"}]}`))
	env.runtime.mu.Lock()
	defer env.runtime.mu.Unlock()
	states := map[state]int{}
	for current := range env.runtime.conversations {
		states[current.state]++
	}
	if env.runtime.live() != 1 || states[stateIdle] != 1 || states[stateDormant] != 1 {
		t.Fatalf("live=%d states=%v", env.runtime.live(), states)
	}
}

func TestUnsupportedFieldsAreReported(t *testing.T) {
	diagnostics := Unsupported([]byte(`{"model":"m","max_tokens":10,"temperature":1,"stop_sequences":["x"],
		"tool_choice":{"type":"any"},"tools":[{"type":"web_search_20250305","name":"web_search"}],"foo":1,
		"messages":[{"role":"user","content":"Hi"}]}`))
	got := map[string]string{}
	for _, diagnostic := range diagnostics {
		got[diagnostic.Path] = diagnostic.Severity
	}
	want := map[string]string{
		"max_tokens": "warning", "temperature": "warning", "stop_sequences": "error", "tool_choice": "error",
		"tools[0]": "error", "foo": "warning",
	}
	for path, severity := range want {
		if got[path] != severity {
			t.Fatalf("diagnostics = %v", got)
		}
	}
}

func TestModelsComeFromTheCLIWithoutAModelCall(t *testing.T) {
	env := newTestEnv(t, Options{}, func(int, map[string]any, http.ResponseWriter) {
		t.Error("listing models reached the upstream")
	})
	models, err := env.runtime.Models(context.Background(), env.call.Service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(models, fakeclaude.OfferedModels) {
		t.Fatalf("models = %v, want %v", models, fakeclaude.OfferedModels)
	}
	args, err := os.ReadFile(fakeclaude.ServeArgsFile(env.configDir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "--model") {
		t.Fatalf("listing models picked a model: %s", args)
	}
	env.runtime.mu.Lock()
	live, secrets := env.runtime.live(), len(env.runtime.secrets)
	env.runtime.mu.Unlock()
	if live != 0 || secrets != 0 {
		t.Fatalf("listing models left %d processes and %d secrets", live, secrets)
	}

	if err := os.Remove(fakeclaude.AccountFile(env.configDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := env.runtime.Models(context.Background(), env.call.Service.ID); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("signed-out models error = %v", err)
	}
	if id := <-env.signedOut; id != env.call.Service.ID {
		t.Fatalf("signed out %s", id)
	}
}
