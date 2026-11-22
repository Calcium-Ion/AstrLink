package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const relayKitCustomToolPatch = "*** Begin Patch\n*** Add File: hello.txt\n+say \"hi\"\n*** End Patch\n"

// relayKitCustomToolRequest is a Codex-shaped Responses request with the
// freeform apply_patch tool.
func relayKitCustomToolRequest(stream bool) string {
	return `{"model":"public-responses","stream":` + map[bool]string{true: "true", false: "false"}[stream] +
		`,"input":"add hello.txt","tools":[{"type":"custom","name":"apply_patch","description":"Apply a patch",` +
		`"format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"}}]}`
}

func relayKitJSONString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// relayKitCustomToolUpstream answers with an apply_patch call carrying input,
// encoded the way each upstream protocol represents a function call.
func relayKitCustomToolUpstream(t *testing.T, protocol contract.ProtocolID, stream bool, input string) *http.Response {
	t.Helper()
	patch := relayKitJSONString(t, input)
	arguments := relayKitJSONString(t, `{"input":`+patch+`}`)
	if !stream {
		switch protocol {
		case contract.ProtocolOpenAIChat:
			return jsonResponse(http.StatusOK, `{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-upstream","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_patch","type":"function","function":{"name":"apply_patch","arguments":`+arguments+`}}]}}]}`)
		case contract.ProtocolAnthropicMessages:
			return jsonResponse(http.StatusOK, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-upstream","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_patch","name":"apply_patch","input":{"input":`+patch+`}}],"usage":{"input_tokens":4,"output_tokens":2}}`)
		case contract.ProtocolGoogleGenerateContent:
			return jsonResponse(http.StatusOK, `{"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"functionCall":{"name":"apply_patch","args":{"input":`+patch+`}}}]}}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":6}}`)
		}
		t.Fatalf("unsupported protocol %s", protocol)
	}
	var frames string
	switch protocol {
	case contract.ProtocolOpenAIChat:
		frames = `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-upstream","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_patch","type":"function","function":{"name":"apply_patch","arguments":""}}]},"finish_reason":null}]}` + "\n\n" +
			`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":` + arguments + `}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
			"data: [DONE]\n\n"
	case contract.ProtocolAnthropicMessages:
		frames = "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-upstream","content":[],"usage":{"input_tokens":4,"output_tokens":0}}}` + "\n\n" +
			"event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_patch","name":"apply_patch","input":{}}}` + "\n\n" +
			"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + arguments + `}}` + "\n\n" +
			"event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}` + "\n\n" +
			"event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":2}}` + "\n\n" +
			"event: message_stop\ndata: " + `{"type":"message_stop"}` + "\n\n"
	case contract.ProtocolGoogleGenerateContent:
		frames = "data: " + `{"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"functionCall":{"name":"apply_patch","args":{"input":` + patch + `}}}]}}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":6}}` + "\n\n"
	default:
		t.Fatalf("unsupported protocol %s", protocol)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(frames)),
	}
}

func relayKitCandidate(id contract.ServiceID, protocol contract.ProtocolID, model string) endpoint.Resolved {
	service := validEndpoint(protocol, true)
	service.ID = id
	service.BaseURL = "https://" + string(id) + ".example"
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 0
	failover := contract.DefaultFailoverPolicy()
	failover.Strategy = contract.FailoverOnly
	return endpoint.Resolved{
		Endpoint: service, PlanType: contract.PlanTypeRelayKit, UpstreamProtocol: protocol, UpstreamModel: model,
		FailurePolicy: &policy, Failover: &failover,
	}
}

func serveRelayKitResponses(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	return response
}

// assertCustomToolResponse checks that the client received apply_patch as a
// Responses custom tool call with its original freeform input.
func assertCustomToolResponse(t *testing.T, response *httptest.ResponseRecorder, stream bool) {
	t.Helper()
	if !stream {
		var body struct {
			Output []map[string]any `json:"output"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, item := range body.Output {
			if item["name"] == "apply_patch" {
				if item["type"] != "custom_tool_call" || item["input"] != relayKitCustomToolPatch {
					t.Fatalf("apply_patch item = %#v", item)
				}
				return
			}
		}
		t.Fatalf("no apply_patch item: %s", response.Body.String())
	}
	var input strings.Builder
	var doneInput any
	var item map[string]any
	for _, frame := range strings.Split(strings.TrimSpace(response.Body.String()), "\n\n") {
		var eventType, data string
		for _, line := range strings.Split(frame, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				eventType = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatalf("frame %q: %v", frame, err)
		}
		switch eventType {
		case "response.function_call_arguments.delta", "response.function_call_arguments.done":
			t.Fatalf("apply_patch streamed as a function call: %s", data)
		case "response.custom_tool_call_input.delta":
			delta, _ := payload["delta"].(string)
			input.WriteString(delta)
		case "response.custom_tool_call_input.done":
			doneInput = payload["input"]
		case "response.output_item.done":
			if output, _ := payload["item"].(map[string]any); output["name"] == "apply_patch" {
				item = output
			}
		}
	}
	if input.String() != relayKitCustomToolPatch || doneInput != relayKitCustomToolPatch {
		t.Fatalf("custom tool input deltas=%q done=%#v\n%s", input.String(), doneInput, response.Body.String())
	}
	if item == nil || item["type"] != "custom_tool_call" || item["input"] != relayKitCustomToolPatch {
		t.Fatalf("apply_patch output item = %#v", item)
	}
}

func TestRelayKitRestoresCustomToolCallsThroughHandler(t *testing.T) {
	for _, target := range []contract.ProtocolID{
		contract.ProtocolOpenAIChat, contract.ProtocolAnthropicMessages, contract.ProtocolGoogleGenerateContent,
	} {
		for _, stream := range []bool{false, true} {
			name := string(target) + map[bool]string{true: "/stream", false: "/json"}[stream]
			t.Run(name, func(t *testing.T) {
				handler := NewWithDependencies(Dependencies{
					Resolver: candidateResolver{candidates: []endpoint.Resolved{
						relayKitCandidate("endpoint_custom", target, "upstream-model"),
					}},
					ConversionEngine: relaykitbridge.NewEngine(),
					Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
						body, _ := io.ReadAll(request.Body)
						if !strings.Contains(string(body), "apply_patch") || strings.Contains(string(body), `"custom"`) {
							t.Fatalf("upstream did not receive apply_patch as a function: %s", body)
						}
						return relayKitCustomToolUpstream(t, target, stream, relayKitCustomToolPatch), nil
					})),
				})
				assertCustomToolResponse(t, serveRelayKitResponses(t, handler, relayKitCustomToolRequest(stream)), stream)
			})
		}
	}
}

// recordingConversionEngine remembers the state every request conversion
// returned and every response conversion received.
type recordingConversionEngine struct {
	relaykitbridge.ConversionEngine
	mu        sync.Mutex
	requests  []relaykitbridge.ConversionState
	responses []relaykitbridge.ConversionState
}

func (engine *recordingConversionEngine) ConvertRequest(ctx context.Context, in relaykitbridge.ConvertRequestInput) (relaykitbridge.ConvertRequestOutput, error) {
	output, err := engine.ConversionEngine.ConvertRequest(ctx, in)
	engine.mu.Lock()
	engine.requests = append(engine.requests, output.State)
	engine.mu.Unlock()
	return output, err
}

func (engine *recordingConversionEngine) ConvertResponse(ctx context.Context, in relaykitbridge.ConvertResponseInput) (relaykitbridge.ConvertResponseOutput, error) {
	engine.mu.Lock()
	engine.responses = append(engine.responses, in.State)
	engine.mu.Unlock()
	return engine.ConversionEngine.ConvertResponse(ctx, in)
}

func (engine *recordingConversionEngine) NewResponseStream(ctx context.Context, options relaykitbridge.StreamOptions) (relaykitbridge.ResponseStream, error) {
	engine.mu.Lock()
	engine.responses = append(engine.responses, options.State)
	engine.mu.Unlock()
	return engine.ConversionEngine.NewResponseStream(ctx, options)
}

func TestRelayKitEachAttemptConvertsWithItsOwnState(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{true: "stream", false: "json"}[stream], func(t *testing.T) {
			engine := &recordingConversionEngine{ConversionEngine: relaykitbridge.NewEngine()}
			var attempts atomic.Int32
			handler := NewWithDependencies(Dependencies{
				Resolver: candidateResolver{candidates: []endpoint.Resolved{
					relayKitCandidate("endpoint_chat", contract.ProtocolOpenAIChat, "gpt-upstream"),
					relayKitCandidate("endpoint_claude", contract.ProtocolAnthropicMessages, "claude-upstream"),
				}},
				ConversionEngine: engine,
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					_, _ = io.ReadAll(request.Body)
					if attempts.Add(1) == 1 {
						if stream {
							return jsonResponse(http.StatusServiceUnavailable, `{"error":{"message":"busy"}}`), nil
						}
						// The response is converted with the first attempt's state, then fails.
						return jsonResponse(http.StatusOK, `{`), nil
					}
					return relayKitCustomToolUpstream(t, contract.ProtocolAnthropicMessages, stream, relayKitCustomToolPatch), nil
				})),
			})
			assertCustomToolResponse(t, serveRelayKitResponses(t, handler, relayKitCustomToolRequest(stream)), stream)

			if attempts.Load() != 2 || len(engine.requests) != 2 {
				t.Fatalf("attempts=%d request conversions=%d", attempts.Load(), len(engine.requests))
			}
			first, second := engine.requests[0], engine.requests[1]
			if first == (relaykitbridge.ConversionState{}) || second == (relaykitbridge.ConversionState{}) || first == second {
				t.Fatal("each attempt must record its own custom tool state")
			}
			if len(engine.responses) != 2 || engine.responses[0] != first || engine.responses[1] != second {
				t.Fatalf("response conversions used %d states, not each attempt's own", len(engine.responses))
			}
		})
	}
}

func TestRelayKitResponsesEffortReachesGemini4AsThinkingLevel(t *testing.T) {
	for _, test := range []struct{ model, effort, want string }{
		{"gemini-4-neon", "max", "xhigh"},
		{"gemini-4-neon", "medium", "medium"},
		{"gemini-4-argon-contributor", "max", "high"},
	} {
		t.Run(test.model+" "+test.effort, func(t *testing.T) {
			var sent []byte
			handler := NewWithDependencies(Dependencies{
				Resolver:         candidateResolver{candidates: []endpoint.Resolved{relayKitCandidate("endpoint_gemini", contract.ProtocolGoogleGenerateContent, test.model)}},
				ConversionEngine: relaykitbridge.NewEngine(),
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					sent, _ = io.ReadAll(request.Body)
					return jsonResponse(http.StatusOK, `{"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"ok"}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`), nil
				})),
			})
			serveRelayKitResponses(t, handler, `{"model":"public-responses","input":"hello","reasoning":{"effort":"`+test.effort+`"}}`)
			var body struct {
				GenerationConfig struct {
					ThinkingConfig map[string]any `json:"thinkingConfig"`
				} `json:"generationConfig"`
			}
			if err := json.Unmarshal(sent, &body); err != nil {
				t.Fatalf("upstream body %s: %v", sent, err)
			}
			if level := body.GenerationConfig.ThinkingConfig["thinkingLevel"]; level != test.want {
				t.Fatalf("thinkingLevel = %v, want %q; upstream body %s", level, test.want, sent)
			}
		})
	}
}

func TestRelayKitConversionDiagnosticsAreRecordedPerAttempt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "records.db")
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{
			relayKitCandidate("endpoint_chat", contract.ProtocolOpenAIChat, "gpt-upstream"),
			relayKitCandidate("endpoint_gemini", contract.ProtocolGoogleGenerateContent, "gemini-upstream"),
		}},
		ConversionEngine: relaykitbridge.NewEngine(),
		RequestRecords:   store,
		RecordLogger:     t.Logf,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(request.Body)
			if strings.Contains(string(body), "local_shell") {
				t.Fatalf("dropped tool reached upstream: %s", body)
			}
			if attempts.Add(1) == 1 {
				return jsonResponse(http.StatusServiceUnavailable, `{"error":{"message":"busy"}}`), nil
			}
			return jsonResponse(http.StatusOK, `{"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"ok"}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`), nil
		})),
	})
	// The lossy conversion still succeeds; the dropped tool is only reported.
	serveRelayKitResponses(t, handler, `{"model":"public-responses","input":"list files","tools":[{"type":"local_shell"},`+
		`{"type":"function","name":"read_file","parameters":{"type":"object"}}]}`)
	if attempts.Load() != 2 {
		t.Fatalf("attempts=%d, want 2", attempts.Load())
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen the database and read the records the way the desktop does.
	store, err = sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	control, err := controlapi.NewWithDependencies(contract.VersionResponse{
		CoreVersion: "0.0.0-test", ControlAPIVersion: "v1", ProtocolContractVersion: "v1",
	}, controlapi.Dependencies{ServiceStore: store, RequestRecords: store, ControlToken: "control-token-123456"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.ListRequestRecords(ctx, storage.RequestRecordListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	wantCode := map[contract.ServiceID]string{
		"endpoint_chat":   "unsupported_hosted_tool",
		"endpoint_gemini": "unsupported_opaque_tool",
	}
	seen := 0
	for _, summary := range page.Items {
		for _, id := range append([]contract.RequestID{summary.ID}, childRecordIDs(t, store, summary.ID)...) {
			request := httptest.NewRequest(http.MethodGet, controlapi.RequestsPath+"/"+string(id), nil)
			request.Header.Set("Authorization", "Bearer control-token-123456")
			response := httptest.NewRecorder()
			control.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("get %s status=%d body=%s", id, response.Code, response.Body.String())
			}
			var record contract.RequestRecord
			if err := json.Unmarshal(response.Body.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record.ServiceID == nil {
				t.Fatalf("record %s has no service", id)
			}
			diagnostics := record.ConversionDiagnostics
			if len(diagnostics) != 1 || diagnostics[0].Phase != contract.ConversionDiagnosticPhaseRequest ||
				diagnostics[0].Code != wantCode[*record.ServiceID] || diagnostics[0].Path != "tools[0]" ||
				diagnostics[0].Message == "" {
				t.Fatalf("record %s (%s) diagnostics=%#v", id, *record.ServiceID, diagnostics)
			}
			seen++
		}
	}
	if seen != 2 {
		t.Fatalf("checked %d attempt records, want 2", seen)
	}
}

func childRecordIDs(t *testing.T, store *sqlite.Store, parent contract.RequestID) []contract.RequestID {
	t.Helper()
	children, err := store.ListRequestRecordChildren(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]contract.RequestID, 0, len(children))
	for _, child := range children {
		ids = append(ids, child.ID)
	}
	return ids
}

func TestRelayKitRestoresRedactedValuesInsideCustomToolInput(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{true: "stream", false: "json"}[stream], func(t *testing.T) {
			filter := testPrivacyEngine(t, privacy.Policy{
				Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact,
				ResponseRestore: true, RestoreToolArguments: true,
			}, nil)
			handler := NewWithDependencies(Dependencies{
				Resolver: candidateResolver{candidates: []endpoint.Resolved{
					relayKitCandidate("endpoint_claude", contract.ProtocolAnthropicMessages, "claude-upstream"),
				}},
				ConversionEngine: relaykitbridge.NewEngine(),
				PrivacyFilter:    filter,
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					body, _ := io.ReadAll(request.Body)
					if strings.Contains(string(body), "alice@example.com") {
						t.Fatalf("email leaked to upstream: %s", body)
					}
					// Converted bodies escape the placeholder's angle brackets.
					unescaped := strings.NewReplacer(`\u003c`, "<", `\u003e`, ">").Replace(string(body))
					placeholder := emailPlaceholderPattern.FindString(unescaped)
					if placeholder == "" {
						t.Fatalf("converted request has no email placeholder: %s", body)
					}
					// The model writes the placeholder into the patch it sends back.
					patch := strings.ReplaceAll(relayKitCustomToolPatch, `say "hi"`, placeholder)
					return relayKitCustomToolUpstream(t, contract.ProtocolAnthropicMessages, stream, patch), nil
				})),
			})
			request := strings.Replace(relayKitCustomToolRequest(stream), `"input":"add hello.txt"`,
				`"input":"write alice@example.com into hello.txt"`, 1)
			response := serveRelayKitResponses(t, handler, request)
			if emailPlaceholderPattern.MatchString(response.Body.String()) {
				t.Fatalf("placeholder reached the client: %s", response.Body.String())
			}
			restored := strings.ReplaceAll(relayKitCustomToolPatch, `say "hi"`, "alice@example.com")
			if !strings.Contains(response.Body.String(), relayKitJSONString(t, restored)) {
				t.Fatalf("custom tool input was not restored: %s", response.Body.String())
			}
		})
	}
}
