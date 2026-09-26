package ingress

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/builtintools"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestBuiltinToolTestOwnsOneFinishedRecord(t *testing.T) {
	for _, outcome := range []string{"success", "missing-tool", "cancelled", "rate-limited"} {
		for _, auditEnabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/audit=%v", outcome, auditEnabled), func(t *testing.T) {
				var calls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					if outcome == "cancelled" {
						<-r.Context().Done()
						return
					}
					// Longer than the former desktop control deadline.
					if outcome == "success" {
						time.Sleep(2100 * time.Millisecond)
					}
					output := []any{builtinMessage("done")}
					if outcome == "success" {
						output = append(output, builtintools.Object{"id": "ws_test", "type": "web_search_call", "status": "completed", "action": builtintools.Object{"type": "search", "query": "OpenAI", "sources": []any{}}})
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if outcome == "rate-limited" {
						_, _ = io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"private provider context\"}}\n\n")
						return
					}
					_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", builtintools.Text(builtintools.Object{"type": "response.completed", "response": builtintools.Object{"id": "resp_test", "status": "completed", "model": "main", "output": output}}))
				}))
				defer upstream.Close()
				candidate := wsCandidate(upstream.URL)
				store := newRedirectSettingsStore()
				handler := NewWithDependencies(Dependencies{
					Resolver:       candidateResolver{candidates: []endpoint.Resolved{candidate}},
					RequestRecords: store,
					AuditSettings: &memoryAuditSettings{settings: contract.AuditSettings{
						RequestBodyEnabled: auditEnabled, ResponseContentEnabled: auditEnabled, HTTPMetaEnabled: auditEnabled,
						RequestBodyMaxBytes: 64 << 10, ResponseContentMaxBytes: 64 << 10,
						MetadataRetentionDays: 30, ContentRetentionDays: 7,
					}},
					AuditBlobs: &memoryAuditBlobs{},
				})
				ctx := context.Background()
				if outcome == "cancelled" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
					defer cancel()
				}
				_, err := handler.TestBuiltinTool(ctx, "web_search", contract.BuiltinTool{Enabled: true, Backend: "upstream", ServiceID: candidate.Service.ID, Model: "main"})
				if (err == nil) != (outcome == "success") {
					t.Fatal(err)
				}
				if outcome == "rate-limited" && (!strings.Contains(err.Error(), "rate_limit_exceeded") || strings.Contains(err.Error(), "private provider context")) {
					t.Fatal("upstream error code was lost or private context exposed", err)
				}
				records := store.snapshot()
				if len(records) != 1 || calls.Load() != 1 {
					t.Fatalf("records=%d upstream calls=%d", len(records), calls.Load())
				}
				if records[0].CompletedAt == nil || records[0].Status == contract.RequestStatusPending {
					t.Fatal("test record left pending", records[0])
				}
				want := contract.RequestStatusSucceeded
				if outcome == "missing-tool" || outcome == "rate-limited" {
					want = contract.RequestStatusFailed
				}
				if outcome == "cancelled" {
					want = contract.RequestStatusCancelled
				}
				if records[0].Status != want {
					t.Fatal(records[0].Status, want)
				}
			})
		}
	}

}

func TestBuiltinToolsGatewayHTTPAndWebSocket(t *testing.T) {
	for _, stream := range []string{"http", "sse", "websocket"} {
		t.Run(stream, func(t *testing.T) {
			var mainCalls, nativeCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Error("tool path attempted upstream websocket")
					w.WriteHeader(400)
					return
				}
				for name, values := range r.Header {
					if strings.Contains(strings.ToLower(name), "astrlink") || strings.Contains(strings.ToLower(strings.Join(values, " ")), "astrlink") {
						t.Error("gateway identity escaped", name)
					}
				}
				var body builtintools.Object
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid body")
					return
				}
				var output []any
				if body["model"] == "search" {
					nativeCalls.Add(1)
					if builtintools.Map(builtintools.Array(body["tools"])[0])["type"] != "web_search" {
						t.Error("native tool got rewritten recursively")
					}
					output = []any{builtintools.Object{"type": "web_search_call", "id": "ws_native", "status": "completed", "action": builtintools.Object{"type": "search", "query": "fact", "sources": []any{builtintools.Object{"type": "url", "url": "https://example.com", "title": "Example"}}}}, builtinMessage("The fact is 42.")}
				} else {
					mainCalls.Add(1)
					if strings.Contains(builtintools.Text(body["input"]), "function_call_output") {
						output = []any{builtinMessage("[Example](https://example.com) says 42.")}
					} else {
						tool := builtintools.Map(builtintools.Array(body["tools"])[0])
						if tool["type"] != "function" {
							t.Error("main model received unsupported hosted tool")
						}
						output = []any{builtintools.Object{"type": "function_call", "id": "fc_main", "name": tool["name"], "call_id": "call_main", "arguments": `{"action":"search","query":"fact"}`, "status": "completed"}}
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				response := builtintools.Object{"id": builtintools.ID("resp_"), "object": "response", "model": body["model"], "status": "completed", "output": output, "usage": builtintools.Object{"input_tokens": 2, "output_tokens": 1, "total_tokens": 3}}
				_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", builtintools.Text(builtintools.Object{"type": "response.completed", "response": response}))
			}))
			defer upstream.Close()
			main := wsCandidate(upstream.URL)
			main.Service.ID = "service_main"
			main.Service.Models = []string{"main"}
			main.Service.ResponsesWebSocketEnabled = nil
			native := wsCandidate(upstream.URL)
			native.Service.ID = "service_search"
			native.Service.Models = []string{"search"}
			store := newRedirectSettingsStore()
			store.settings.BuiltinTools = &contract.BuiltinTools{WebSearch: contract.BuiltinTool{Enabled: true, Backend: "upstream", ServiceID: "service_search", Model: "search"}}
			handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: []endpoint.Resolved{main, native}}, RequestRecords: store})
			input := builtintools.Object{"model": "main", "input": "Find a fact", "tools": []any{builtintools.Object{"type": "web_search"}}, "stream": stream != "http"}
			var previous string
			if stream == "websocket" {
				client := dialResponses(t, handler, nil)
				for turn := 0; turn < 2; turn++ {
					input["type"] = "response.create"
					if previous != "" {
						input["previous_response_id"] = previous
						input["input"] = "Explain"
					}
					sendWS(t, client, builtintools.Text(input))
					for {
						event := readWS(t, client)
						if event["type"] == "error" || event["type"] == "response.failed" {
							t.Fatal(event)
						}
						if event["type"] == "response.completed" {
							previous = builtintools.String(builtintools.Map(event["response"])["id"])
							break
						}
					}
				}
			} else {
				w := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(builtintools.Text(input)))
				request.Header.Set("Content-Type", "application/json")
				handler.ServeHTTP(w, request)
				if w.Code != 200 || !strings.Contains(w.Body.String(), "web_search_call") || strings.Contains(w.Body.String(), `"name":"tool_`) {
					t.Fatal(w.Code, w.Body.String())
				}
				if stream == "sse" && strings.Count(w.Body.String(), "event: response.completed\n") != 1 {
					t.Fatal(w.Body.String())
				}
			}
			want := int32(2)
			if stream == "websocket" {
				want = 3
			}
			if mainCalls.Load() != want || nativeCalls.Load() != 1 {
				t.Fatal(mainCalls.Load(), nativeCalls.Load())
			}
		})
	}
}

func builtinMessage(text string) builtintools.Object {
	return builtintools.Object{"type": "message", "id": builtintools.ID("msg_"), "role": "assistant", "status": "completed", "content": []any{builtintools.Object{"type": "output_text", "text": text, "annotations": []any{}}}}
}

func TestBuiltinToolsDisabledPreservesBody(t *testing.T) {
	store := newRedirectSettingsStore()
	store.settings.BuiltinTools = &contract.BuiltinTools{}
	input := `{"model":"main","input":"Hello","tools":[{"type":"web_search"}],"custom":"keep"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(input))
	handler := NewWithDependencies(Dependencies{})
	if handler.tryBuiltinTools(httptest.NewRecorder(), request, Request{Protocol: contract.ProtocolOpenAIResponses}, store.settings) {
		t.Fatal("disabled tool intercepted")
	}
	var value builtintools.Object
	if json.NewDecoder(request.Body).Decode(&value) != nil || value["custom"] != "keep" {
		t.Fatal("disabled body changed")
	}
}

func TestBuiltinToolCaptureFailure(t *testing.T) {
	c := &builtinCapture{header: make(http.Header)}
	c.header.Set("Content-Type", "text/event-stream")
	_, err := c.Write([]byte("event: response.failed\ndata: {\"type\":\"response.failed\"}\n\n"))
	if err == nil {
		t.Fatal("failed stream accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewWithDependencies(Dependencies{}).builtinModel(ctx, httptest.NewRequest(http.MethodPost, "/v1/responses", nil), builtintools.Object{"model": "main", "input": "a"}, nil); err == nil {
		t.Fatal("cancelled model call succeeded")
	}
}

type serviceImagesResolver struct {
	candidateResolver
	service endpoint.Resolved
}

func (resolver serviceImagesResolver) ResolveService(_ context.Context, id contract.ServiceID) (endpoint.Resolved, error) {
	if id != resolver.service.Service.ID {
		return endpoint.Resolved{}, endpoint.ErrNoEndpoint
	}
	return resolver.service, nil
}

func TestBuiltinServiceImagesCallsOnlyTheSelectedProvider(t *testing.T) {
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nimage"))
	for _, test := range []struct {
		name    string
		kind    contract.ServiceKind
		service contract.ServiceID
		wantErr string
	}{
		{name: "provider", kind: contract.ServiceKindNewAPI, service: "endpoint_test"},
		{name: "unknown provider", kind: contract.ServiceKindNewAPI, service: "endpoint_other", wantErr: "unavailable or disabled"},
		{name: "no images API", kind: contract.ServiceKindAnthropic, service: "endpoint_test", wantErr: "does not offer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			service := contract.ServiceFromEndpoint(validEndpoint(contract.ProtocolOpenAIResponses, false))
			service.Kind = test.kind
			service.HTTP.BaseURL = "https://images.example/v1"
			forwarder := transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				if request.Method != http.MethodPost || request.URL.String() != "https://images.example/v1/images/generations" {
					t.Errorf("upstream request = %s %s", request.Method, request.URL)
				}
				if request.Header.Get("Authorization") != "Bearer provider-secret" {
					t.Errorf("upstream Authorization = %q", request.Header.Get("Authorization"))
				}
				for name, values := range request.Header {
					if strings.Contains(strings.ToLower(name+strings.Join(values, " ")), "astrlink") {
						t.Errorf("upstream header identifies the gateway: %s: %v", name, values)
					}
				}
				if request.Header.Get("User-Agent") != "" {
					t.Errorf("upstream User-Agent = %q", request.Header.Get("User-Agent"))
				}
				var body builtintools.Object
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body["model"] != "gpt-image-1" || builtintools.String(body["prompt"]) == "" {
					t.Errorf("upstream body = %v (%v)", body, err)
				}
				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(builtintools.Text(builtintools.Object{"data": []any{builtintools.Object{"b64_json": png}}}))),
				}, nil
			}))
			store := newRedirectSettingsStore()
			handler := NewWithDependencies(Dependencies{
				Resolver: serviceImagesResolver{service: endpoint.Resolved{Service: service, BaseURL: service.HTTP.BaseURL}},
				Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
					return http.Header{"Authorization": {"Bearer provider-secret"}}, nil
				}),
				Forwarder:      forwarder,
				RequestRecords: store,
			})
			result, err := handler.TestBuiltinTool(context.Background(), "image_generation", contract.BuiltinTool{
				Enabled: true, Backend: "service_images", ServiceID: test.service, Model: "gpt-image-1",
			})
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) || calls.Load() != 0 {
					t.Fatalf("err=%v upstream calls=%d", err, calls.Load())
				}
				return
			}
			if err != nil || result["result_count"] != 1 || calls.Load() != 1 {
				t.Fatalf("result=%v err=%v upstream calls=%d", result, err, calls.Load())
			}
			records := store.snapshot()
			if len(records) != 1 || records[0].Status != contract.RequestStatusSucceeded {
				t.Fatalf("records = %+v", records)
			}
			var summaries []string
			for _, event := range records[0].Events {
				summaries = append(summaries, event.Summary)
			}
			if !strings.Contains(strings.Join(summaries, "\n"), "image_generation · service_images") {
				t.Fatalf("record events do not name the provider Images path: %q", summaries)
			}
		})
	}
}
