package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const signatureFailure = `{"type":"error","error":{"type":"invalid_request_error","message":"Invalid signature in thinking block"}}`
const signedThinkingRequest = `{"model":"public","max_tokens":4096,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"thinking","thinking":"reasoning","signature":"invalid"},{"type":"tool_use","id":"call_1","name":"lookup","input":{"n":9007199254740993,"thinking":"keep","signature":"keep"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"result"}]}]}`

func TestThinkingSignatureErrorMatcher(t *testing.T) {
	for _, tt := range []struct {
		message string
		want    bool
	}{
		{"Invalid `signature` in `thinking` block", true},
		{"messages.1.content.0.signature: Field required", true},
		{"Expected `thinking` or `redacted_thinking`, but found `text`", true},
		{"thinking or redacted_thinking blocks cannot be modified", true},
		{"each thinking block must contain thinking", true},
		{"Invalid AWS request signature", false},
		{"max_tokens: expected a value greater than thinking.budget_tokens", false},
		{"The content[].thinking must be passed back to the API", false},
		{"all messages must have non-empty content", false},
	} {
		encoded, _ := json.Marshal(map[string]any{"error": map[string]any{"message": tt.message}})
		if got := isThinkingSignatureError(encoded); got != tt.want {
			t.Errorf("%s => %v", tt.message, got)
		}
	}
	if isThinkingSignatureError([]byte(`{"error":{"message":"other"},"echo":"thinking signature"}`)) || isThinkingSignatureError([]byte("invalid thinking signature")) {
		t.Fatal("matched content outside a structured error message")
	}
}

func TestThinkingRecoveryRequiresNativeAnthropicSemantics(t *testing.T) {
	for _, protocol := range []contract.ProtocolID{contract.ProtocolAnthropicMessages, contract.ProtocolGoogleGenerateContent, contract.ProtocolOpenAIChat} {
		for _, planType := range []contract.PlanType{contract.PlanTypeNative, contract.PlanTypeDelegated, contract.PlanTypeRelayKit} {
			plan := contract.ExecutionPlan{Type: planType, InputProtocol: protocol, UpstreamProtocol: protocol}
			want := protocol == contract.ProtocolAnthropicMessages && planType != contract.PlanTypeRelayKit
			if supportsThinkingSignatureRecovery(plan, "anthropic/claude-sonnet-4-6") != want {
				t.Fatalf("unexpected support for %+v", plan)
			}
		}
	}
}

func TestThinkingRecoveryIncompleteErrorDoesNotBecomeNetworkRetry(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		t.Run(fmt.Sprint(stalled), func(t *testing.T) {
			policy := contract.DefaultFailurePolicy()
			policy.InitialDelayMS = 0
			trips := 0
			handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: thinkingCandidates(policy, 6, "claude-sonnet-4-6")}, Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
				trips++
				var body io.ReadCloser = io.NopCloser(io.MultiReader(strings.NewReader(`{"error":{"message":"thinking signature`), failingReader{err: io.ErrUnexpectedEOF}))
				if stalled {
					reader, writer := io.Pipe()
					t.Cleanup(func() { _ = writer.Close() })
					body = reader
				}
				return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}, nil
			}))})
			request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(signedThinkingRequest))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if trips != 1 || response.Code != 400 || !json.Valid(response.Body.Bytes()) {
				t.Fatalf("attempts=%d status=%d body=%s", trips, response.Code, response.Body.String())
			}
		})
	}
}

func TestThinkingRecoveryCancellationDoesNotInventAttempt(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 5000
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: thinkingCandidates(policy, 6, "claude-sonnet-4-6")}, RequestRecords: store, Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
		cancel()
		return jsonResponse(400, signatureFailure), nil
	}))})
	request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(signedThinkingRequest)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if len(store.records) != 1 || store.records[0].AttemptIndex != 1 || store.records[0].ChildCount != 0 || store.records[0].Status != contract.RequestStatusCancelled {
		t.Fatalf("phantom retry after cancellation: %+v", store.records)
	}
}

func TestRectifyThinkingPreservesToolsAndUnknownFields(t *testing.T) {
	repaired, changed := rectifyThinkingSignature([]byte(signedThinkingRequest))
	if !changed {
		t.Fatal("did not repair history")
	}
	var doc map[string]json.RawMessage
	_ = json.Unmarshal(repaired, &doc)
	if _, ok := doc["thinking"]; ok {
		t.Fatal("thinking still enabled")
	}
	for _, kept := range []string{`"n":9007199254740993`, `"thinking":"keep"`, `"signature":"keep"`, `"tool_use_id":"call_1"`, `"type":"tool_use"`, `"type":"tool_result"`, `"text":"reasoning"`} {
		if !strings.Contains(string(repaired), kept) {
			t.Errorf("missing %s in %s", kept, repaired)
		}
	}
	if _, changed := rectifyThinkingSignature(repaired); changed {
		t.Fatal("repair is not idempotent")
	}
	for _, body := range []string{`null`, `[]`, `{`, `{"thinking":{},"messages":[]}`, `{"messages":[{"role":"user","content":[{"type":"thinking","signature":"keep"}]}]}`} {
		if _, changed := rectifyThinkingSignature([]byte(body)); changed {
			t.Errorf("unexpected rewrite: %s", body)
		}
	}
	for _, settings := range []string{
		`"context_management":{"edits":[{"type":"clear_thinking_20251015"}]},`,
		`"context_management":{"edits":[{"type":"clear_thinking_20251015"},{"type":"clear_tool_uses_20250919"}]},`,
	} {
		body := `{` + settings + `"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"encrypted"}]}]}`
		repaired, changed := rectifyThinkingSignature([]byte(body))
		if !changed || strings.Contains(string(repaired), "encrypted") || strings.Contains(string(repaired), "clear_thinking_") || !strings.Contains(string(repaired), "(thinking omitted)") {
			t.Fatalf("bad repair %s", repaired)
		}
		if strings.Contains(settings, "clear_tool_uses") && !strings.Contains(string(repaired), "clear_tool_uses") {
			t.Fatal("removed unrelated context strategy")
		}
	}
}

func thinkingCandidates(policy contract.FailurePolicy, limit int, models ...string) []endpoint.Resolved {
	candidates := recoveryCandidates(len(models), policy, contract.FailoverPolicy{Enabled: true, Strategy: contract.FailoverFirst, MaxAttempts: limit})
	for i, model := range models {
		service := validEndpoint(contract.ProtocolAnthropicMessages, true)
		service.ID = candidates[i].Endpoint.ID
		service.BaseURL = candidates[i].Endpoint.BaseURL
		candidates[i].Endpoint = service
		candidates[i].UpstreamModel = model
	}
	return candidates
}

func TestThinkingSignatureRecoveryBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name, model, errorBody       string
		retries, limit               int
		disabled, stream, stillFails bool
		rule                         contract.FailureAction
		want                         int
	}{
		{name: "repairs on same target before failover", model: "claude-sonnet-4-6", retries: 1, limit: 6, want: 2},
		{name: "streaming request", model: "claude-sonnet-4-6", retries: 1, limit: 6, stream: true, want: 2},
		{name: "does not rectify twice", model: "claude-sonnet-4-6", retries: 5, limit: 6, stillFails: true, want: 2},
		{name: "switch disabled", model: "claude-sonnet-4-6", retries: 1, limit: 6, disabled: true, want: 1},
		{name: "no retry budget", model: "claude-sonnet-4-6", retries: 0, limit: 6, want: 2},
		{name: "total budget", model: "claude-sonnet-4-6", retries: 1, limit: 1, want: 1},
		{name: "explicit stop", model: "claude-sonnet-4-6", retries: 1, limit: 6, rule: contract.FailureStop, want: 2},
		{name: "mapped DeepSeek", model: "deepseek-v4-pro", retries: 1, limit: 6, want: 1},
		{name: "mapped Kimi", model: "kimi-k2.5", retries: 1, limit: 6, want: 1},
		{name: "unknown model", model: "my-model", retries: 1, limit: 6, want: 1},
		{name: "auth signature", model: "claude-sonnet-4-6", errorBody: `{"error":{"message":"invalid request signature"}}`, retries: 1, limit: 6, want: 1},
		{name: "ordinary 400", model: "claude-sonnet-4-6", errorBody: `{"error":{"message":"max_tokens too large"}}`, retries: 1, limit: 6, want: 1},
		{name: "oversized error", model: "claude-sonnet-4-6", errorBody: `{"error":{"message":"Invalid thinking signature ` + strings.Repeat("x", 65536) + `"}}`, retries: 1, limit: 6, want: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := contract.DefaultFailurePolicy()
			policy.InitialDelayMS, policy.MaxRetries = 0, tt.retries
			if tt.disabled {
				value := false
				policy.ThinkingSignatureRecovery = &value
			}
			if tt.rule != "" {
				policy.HTTPStatus["400"] = tt.rule
			}
			failure := tt.errorBody
			if failure == "" {
				failure = signatureFailure
			}
			var sent []string
			store := &memoryRequestRecordStore{}
			handler := NewWithDependencies(Dependencies{
				Resolver: candidateResolver{candidates: thinkingCandidates(policy, tt.limit, tt.model, "claude-opus-4-6")}, RequestRecords: store,
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					data, _ := io.ReadAll(request.Body)
					sent = append(sent, string(data))
					if request.URL.Host != "A.example" {
						t.Fatalf("switched target for signature repair: %s", request.URL.Host)
					}
					if request.ContentLength != int64(len(data)) {
						t.Fatal("incorrect Content-Length")
					}
					if len(sent) == 1 || tt.stillFails {
						response := jsonResponse(400, failure)
						response.Header.Set("X-Failed-Attempt", "1")
						return response, nil
					}
					if tt.stream {
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))}, nil
					}
					return jsonResponse(200, `{"type":"message","model":"`+tt.model+`","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`), nil
				})),
			})
			input := signedThinkingRequest
			if tt.stream {
				input = strings.Replace(input, `"max_tokens":`, `"stream":true,"max_tokens":`, 1)
			}
			request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(input))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if len(sent) != tt.want {
				t.Fatalf("got %d attempts, want %d; status=%d body=%s", len(sent), tt.want, response.Code, response.Body.String())
			}
			if !strings.Contains(sent[0], `"signature":"invalid"`) {
				t.Fatal("initial request was rectified preemptively")
			}
			if len(sent) > 1 && (strings.Contains(sent[1], `"signature":"invalid"`) || !strings.Contains(sent[1], `"type":"tool_use"`)) {
				t.Fatalf("bad retry body %s", sent[1])
			}
			if tt.want == 1 || tt.stillFails {
				if response.Code != 400 || response.Body.String() != failure {
					t.Fatalf("final error changed: %d %s", response.Code, response.Body.String())
				}
			} else if response.Code != 200 || response.Header().Get("X-Failed-Attempt") != "" || strings.Contains(response.Body.String(), "Invalid signature") {
				t.Fatalf("intermediate error leaked: %d %s", response.Code, response.Body.String())
			}
			children := 0
			for _, record := range store.records {
				if record.ParentRequestID != nil {
					children++
					if record.Recovery == nil || record.Recovery.Reason != thinkingSignatureRecoveryReason || record.Recovery.Action != "retry" {
						t.Fatalf("missing repair record: %+v", record.Recovery)
					}
				}
			}
			if children != tt.want-1 {
				t.Fatalf("children=%d", children)
			}
		})
	}
}

func TestThinkingRepairIsTargetLocalAndReappliesPrivacy(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 0
	policy.MaxRetries = 2
	var sent []string
	handler := NewWithDependencies(Dependencies{
		Resolver:      candidateResolver{candidates: thinkingCandidates(policy, 6, "claude-sonnet-4-6", "kimi-k2.5")},
		PrivacyFilter: testPrivacyEngine(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact}, nil),
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			data, _ := io.ReadAll(request.Body)
			sent = append(sent, string(data))
			switch len(sent) {
			case 1:
				return jsonResponse(400, signatureFailure), nil
			case 2:
				if request.URL.Host != "A.example" || strings.Contains(string(data), "alice@example.com") || !emailPlaceholderPattern.Match(data) {
					t.Fatalf("repair bypassed privacy: %s", data)
				}
				return jsonResponse(503, `{"error":{"message":"unavailable"}}`), nil
			default:
				if request.URL.Host != "B.example" || !strings.Contains(string(data), `"signature":"invalid"`) {
					t.Fatalf("repair escaped original target: %s", data)
				}
				return jsonResponse(200, `{"type":"message","content":[{"type":"text","text":"ok"}]}`), nil
			}
		})),
	})
	request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.Replace(signedThinkingRequest, "reasoning", "alice@example.com", 1)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if len(sent) != 3 || response.Code != 200 {
		t.Fatalf("attempts=%d status=%d body=%s", len(sent), response.Code, response.Body.String())
	}
}

func TestThinkingRecoveryRepairsWithinCurrentManualStep(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		t.Run(fmt.Sprint(repeat), func(t *testing.T) {
			policy := contract.DefaultFailurePolicy()
			policy.InitialDelayMS = 0
			policy.MaxRetries = 0
			candidates := thinkingCandidates(policy, 6, "claude-sonnet-4-6", "claude-opus-4-6")
			if repeat {
				candidates = append(candidates, candidates[0])
			}
			for i := range candidates {
				candidates[i].Path = &endpoint.RecoveryPathSnapshot{ID: "path_test", Mode: "steps", StepID: fmt.Sprintf("step_%d", i)}
			}
			trips := 0
			handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: candidates}, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				trips++
				data, _ := io.ReadAll(request.Body)
				if request.URL.Host != "A.example" {
					t.Fatal("manual path switched during repair")
				}
				if trips == 1 {
					return jsonResponse(400, signatureFailure), nil
				}
				if strings.Contains(string(data), `"signature":"invalid"`) {
					t.Fatal("manual repeat was not repaired")
				}
				return jsonResponse(200, `{"type":"message","content":[]}`), nil
			}))})
			request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(signedThinkingRequest))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if trips != 2 || response.Code != 200 {
				t.Fatalf("got %d attempts, status %d; want repaired success in 2 attempts", trips, response.Code)
			}
		})
	}
}
