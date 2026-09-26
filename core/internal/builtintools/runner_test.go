package builtintools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

type testSecrets struct{}

func (testSecrets) Get(context.Context, secretstore.Ref) ([]byte, error) {
	return []byte("test-key"), nil
}
func (testSecrets) Put(context.Context, secretstore.Ref, []byte) error { return nil }
func (testSecrets) Delete(context.Context, secretstore.Ref) error      { return nil }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testClient(t *testing.T, handle func(*http.Request) Object) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("missing tool credential")
		}
		for name, values := range r.Header {
			if strings.Contains(strings.ToLower(name), "astrlink") || strings.Contains(strings.ToLower(strings.Join(values, " ")), "astrlink") {
				t.Fatal("gateway identity leaked")
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(Text(handle(r))))}, nil
	})}
}
func toolConfig() contract.BuiltinTool {
	return contract.BuiltinTool{Enabled: true, Backend: "external", BaseURL: "https://tools.example/v1", Model: "image-model"}
}
func modelResponse(output ...any) Object {
	return Object{"status": "completed", "output": output, "usage": Object{"input_tokens": float64(3), "output_tokens": float64(2), "total_tokens": float64(5)}}
}
func message(text string) Object {
	return Object{"type": "message", "id": ID("msg_"), "role": "assistant", "status": "completed", "content": []any{Object{"type": "output_text", "text": text, "annotations": []any{}}}}
}
func generatedCall(body Object, args Object) Object {
	return Object{"type": "function_call", "id": ID("fc_"), "call_id": ID("call_"), "name": Map(Array(body["tools"])[0])["name"], "arguments": Text(args), "status": "completed"}
}
func decode(t *testing.T, raw string) Object {
	t.Helper()
	var body Object
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err, raw)
	}
	return body
}

func TestSearchLoopAndContinuation(t *testing.T) {
	settings := &contract.BuiltinTools{WebSearch: toolConfig()}
	var requests int
	executor := Executor{Secrets: testSecrets{}, Client: testClient(t, func(r *http.Request) Object {
		requests++
		if !strings.HasSuffix(r.URL.Path, "/search") {
			t.Fatal(r.URL.Path)
		}
		return Object{"results": []any{Object{"title": "Source", "url": "https://example.com/fact", "content": "Verified fact"}}, "usage": Object{"credits": 1}}
	})}
	store := &Store{}
	runner := Runner{Store: store, Executor: executor}
	rounds := 0
	runner.Model = func(_ context.Context, body Object) (Object, error) {
		rounds++
		if rounds == 1 {
			return modelResponse(generatedCall(body, Object{"action": "search", "query": "test"})), nil
		}
		if !strings.Contains(Text(body["input"]), "Verified fact") {
			t.Fatal("tool output not returned to model")
		}
		return modelResponse(message("See [Source](https://example.com/fact).")), nil
	}
	input := Object{"model": "main", "input": "Search for a fact", "tools": []any{Object{"type": "web_search"}}}
	w := httptest.NewRecorder()
	if err := runner.Run(context.Background(), w, "principal", input, settings); err != nil {
		t.Fatal(err)
	}
	response := decode(t, w.Body.String())
	output := Array(response["output"])
	if requests != 1 || rounds != 2 || len(output) != 2 || Map(output[0])["type"] != "web_search_call" {
		t.Fatal(response, requests, rounds)
	}
	annotations := Array(Map(Array(Map(output[1])["content"])[0])["annotations"])
	if len(annotations) != 1 {
		t.Fatal("missing citations", response)
	}
	if Map(response["usage"])["total_tokens"] != float64(10) {
		t.Fatal("missing accumulated model usage")
	}
	input["previous_response_id"] = response["id"]
	input["input"] = "Explain that result"
	w = httptest.NewRecorder()
	if err := runner.Run(context.Background(), w, "principal", input, settings); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatal("replayed search")
	}
	if err := runner.Run(context.Background(), httptest.NewRecorder(), "different-principal", input, settings); err == nil {
		t.Fatal("state crossed token boundary")
	}
	// Full replay expands a public hosted-tool item back into its private call/output pair.
	delete(input, "previous_response_id")
	input["input"] = append(output, Object{"role": "user", "content": "Continue"})
	if err := runner.Run(context.Background(), httptest.NewRecorder(), "principal", input, settings); err != nil {
		t.Fatal(err)
	}
}

func TestMixedClientToolAndSSE(t *testing.T) {
	rounds := 0
	executorCalls := 0
	runner := Runner{Store: &Store{}, Executor: Executor{Native: func(context.Context, contract.BuiltinTool, Object) (Object, error) {
		executorCalls++
		return modelResponse(Object{"type": "web_search_call", "id": "ws_remote", "status": "completed", "action": Object{"type": "search", "query": "hello", "sources": []any{}}}, message("Found result")), nil
	}}}
	runner.Model = func(_ context.Context, body Object) (Object, error) {
		rounds++
		return modelResponse(generatedCall(body, Object{"action": "search", "query": "hello"}), Object{"type": "function_call", "id": "fc_client", "call_id": "call_client", "name": "read_file", "arguments": `{"path":"a"}`, "status": "completed"}), nil
	}
	settings := &contract.BuiltinTools{WebSearch: contract.BuiltinTool{Enabled: true, Backend: "upstream", ServiceID: "service_test", Model: "search-model"}}
	input := Object{"model": "main", "input": "Search and read", "stream": true, "tools": []any{Object{"type": "web_search"}, Object{"type": "function", "name": "read_file", "parameters": Object{"type": "object"}}}}
	w := httptest.NewRecorder()
	if err := runner.Run(context.Background(), w, "p", input, settings); err != nil {
		t.Fatal(err)
	}
	if rounds != 1 || executorCalls != 1 || strings.Count(w.Body.String(), "event: response.completed\n") != 1 {
		t.Fatal(w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "read_file") || strings.Contains(w.Body.String(), `"name":"tool_`) {
		t.Fatal("private function leaked or client function disappeared")
	}
	previous := -1
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		event := decode(t, strings.TrimPrefix(line, "data: "))
		n := int(event["sequence_number"].(float64))
		if n != previous+1 {
			t.Fatal("event sequence", n, previous)
		}
		previous = n
	}
}

func TestToolFailureLimitAndCancellation(t *testing.T) {
	settings := &contract.BuiltinTools{WebSearch: toolConfig()}
	count := 0
	runner := Runner{Store: &Store{}, Executor: Executor{Secrets: testSecrets{}, Client: testClient(t, func(*http.Request) Object { count++; return Object{"results": []any{}} })}, Model: func(_ context.Context, body Object) (Object, error) {
		return modelResponse(generatedCall(body, Object{"action": "search", "query": "again"})), nil
	}}
	input := Object{"model": "main", "input": "Search", "stream": true, "tools": []any{Object{"type": "web_search"}}}
	w := httptest.NewRecorder()
	err := runner.Run(context.Background(), w, "p", input, settings)
	if err == nil || count != 8 || !strings.Contains(w.Body.String(), "event: response.failed") || strings.Contains(w.Body.String(), "event: response.completed") {
		t.Fatal(count, err, w.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := count
	if err := runner.Run(ctx, httptest.NewRecorder(), "p", input, settings); err == nil || count != before {
		t.Fatal("cancelled request executed tool")
	}
}

func pngData(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestImageGenerationEditingAndNoRetry(t *testing.T) {
	encoded := pngData(t)
	requests := []string{}
	executor := Executor{Secrets: testSecrets{}, Client: testClient(t, func(r *http.Request) Object {
		requests = append(requests, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/edits") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			if len(r.MultipartForm.File["image[]"]) != 1 {
				t.Fatal("missing reference image")
			}
		}
		return Object{"data": []any{Object{"b64_json": encoded}}}
	})}
	settings := &contract.BuiltinTools{ImageGeneration: toolConfig()}
	round := 0
	runner := Runner{Store: &Store{}, Executor: executor}
	runner.Model = func(_ context.Context, body Object) (Object, error) {
		round++
		if round%2 == 1 {
			return modelResponse(generatedCall(body, Object{"prompt": "Draw a circle"})), nil
		}
		return modelResponse(message("Here is the image.")), nil
	}
	input := Object{"model": "main", "input": "Draw", "tools": []any{Object{"type": "image_generation"}}}
	w := httptest.NewRecorder()
	if err := runner.Run(context.Background(), w, "p", input, settings); err != nil {
		t.Fatal(err)
	}
	response := decode(t, w.Body.String())
	input["previous_response_id"] = response["id"]
	input["input"] = "Make it blue"
	if err := runner.Run(context.Background(), httptest.NewRecorder(), "p", input, settings); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(requests) != "[/v1/images/generations /v1/images/edits]" {
		t.Fatal(requests)
	}
	calls := 0
	executor.Client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("lost response") })}
	if _, err := executor.Execute(context.Background(), toolConfig(), Invocation{Kind: "image_generation", Arguments: Object{"prompt": "draw"}, Options: Object{"type": "image_generation"}}); err == nil || calls != 1 {
		t.Fatal("image request retried", calls, err)
	}
}

func TestProviderImagesBackendUsesOnlyTheProviderHook(t *testing.T) {
	encoded := pngData(t)
	config := contract.BuiltinTool{Enabled: true, Backend: "service_images", ServiceID: "newapi_main", Model: "gpt-image-1"}
	for _, invalid := range []struct {
		kind   string
		config contract.BuiltinTool
	}{
		{"web_search", contract.BuiltinTool{Enabled: true, Backend: "service_images", ServiceID: "newapi_main", Model: "gpt-image-1"}},
		{"image_generation", contract.BuiltinTool{Enabled: true, Backend: "service_images", ServiceID: "newapi_main"}},
		{"image_generation", contract.BuiltinTool{Enabled: true, Backend: "service_images", Model: "gpt-image-1"}},
	} {
		if invalid.config.Validate(invalid.kind) == nil {
			t.Fatalf("%s accepted %+v", invalid.kind, invalid.config)
		}
	}
	requests := []string{}
	executor := Executor{
		Client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			t.Fatal("provider Images backend used the external tool client")
			return nil, nil
		})},
		Images: func(_ context.Context, got contract.BuiltinTool, path, contentType string, body io.Reader) (Object, error) {
			if got.ServiceID != config.ServiceID {
				t.Fatalf("provider = %q", got.ServiceID)
			}
			data, _ := io.ReadAll(body)
			if !strings.Contains(string(data), "gpt-image-1") {
				t.Fatalf("image model missing from %s body", path)
			}
			requests = append(requests, path+" "+strings.Split(contentType, ";")[0])
			return Object{"data": []any{Object{"b64_json": encoded}}}, nil
		},
	}
	for _, images := range [][]string{nil, {"data:image/png;base64," + encoded}} {
		result, err := executor.Execute(context.Background(), config, Invocation{Kind: "image_generation", Arguments: Object{"prompt": "draw"}, Options: Object{"type": "image_generation"}, Images: images})
		if err != nil || len(result.Items) != 1 {
			t.Fatal(result, err)
		}
	}
	if fmt.Sprint(requests) != "[/images/generations application/json /images/edits multipart/form-data]" {
		t.Fatal(requests)
	}
}

func TestStateExpiryAndBudget(t *testing.T) {
	now := time.Now()
	store := &Store{Now: func() time.Time { return now }, Limit: 500}
	state := State{History: []any{Object{"role": "user", "content": "hello"}}, Replacements: map[string][]any{"ws_tool_test": {Object{"type": "function_call"}}}, Images: map[string][]string{}}
	if err := store.Put("a", "resp_tool_a", state); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("b", "ws_tool_test"); err == nil {
		t.Fatal("cross-token item access")
	}
	now = now.Add(StateTTL)
	if _, err := store.Get("a", "resp_tool_a"); err == nil {
		t.Fatal("expired state accessible")
	}
	state.History = []any{strings.Repeat("x", 1000)}
	if store.Put("a", "large", state) == nil {
		t.Fatal("oversized state retained")
	}
}

func TestTextStreamsBeforeModelFinishes(t *testing.T) {
	w := httptest.NewRecorder()
	item := message("Hello")
	runner := Runner{Store: &Store{}, StreamModel: func(_ context.Context, _ Object, emit func(Object) error) (Object, error) {
		for _, event := range []Object{
			{"type": "response.output_item.added", "output_index": float64(0), "item": Object{"id": item["id"], "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}},
			{"type": "response.output_text.delta", "output_index": float64(0), "item_id": item["id"], "content_index": float64(0), "delta": "Hello"},
			{"type": "response.output_item.done", "output_index": float64(0), "item": item},
		} {
			if err := emit(event); err != nil {
				return nil, err
			}
		}
		if !strings.Contains(w.Body.String(), `"delta":"Hello"`) {
			t.Fatal("text buffered until final response")
		}
		return modelResponse(item), nil
	}}
	if err := runner.Run(context.Background(), w, "p", Object{"model": "main", "input": "Hi", "stream": true}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Count(w.Body.String(), `"delta":"Hello"`) != 1 || strings.Count(w.Body.String(), "event: response.output_item.done\n") != 1 {
		t.Fatal("duplicate stream items", w.Body.String())
	}
}
