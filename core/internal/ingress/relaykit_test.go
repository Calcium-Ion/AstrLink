package ingress

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestRelayKitOpenAIChatToClaude(t *testing.T) {
	engine := relaykitbridge.NewEngine()
	upstream := validEndpoint(contract.ProtocolAnthropicMessages, true)
	var sawPath, sawModel, sawVersion string
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: upstream, PlanType: contract.PlanTypeRelayKit,
			UpstreamProtocol: contract.ProtocolAnthropicMessages, UpstreamModel: "claude-upstream",
		}}},
		ConversionEngine: engine,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			sawPath = request.URL.Path
			sawVersion = request.Header.Get("anthropic-version")
			body, _ := io.ReadAll(request.Body)
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("upstream body: %v", err)
			}
			sawModel, _ = payload["model"].(string)
			return jsonResponse(http.StatusOK, `{
				"id":"msg_1","type":"message","role":"assistant","model":"claude-upstream",
				"content":[{"type":"text","text":"hello from claude"}],
				"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":5}
			}`), nil
		})),
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"public-chat","messages":[{"role":"user","content":"hi"}]}`,
	))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.HasSuffix(sawPath, "/v1/messages") || sawVersion != "2023-06-01" || sawModel != "claude-upstream" {
		t.Fatalf("upstream path/model/version = %q %q %q", sawPath, sawModel, sawVersion)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("client body: %v", err)
	}
	// Conversion reshapes the response but keeps the upstream's model name.
	if body["model"] != "claude-upstream" {
		t.Fatalf("client model = %#v", body["model"])
	}
	choices, _ := body["choices"].([]any)
	if len(choices) == 0 {
		t.Fatalf("missing choices: %s", response.Body.String())
	}
}

func TestRelayKitClaudeToOpenAIChat(t *testing.T) {
	engine := relaykitbridge.NewEngine()
	upstream := validEndpoint(contract.ProtocolOpenAIChat, true)
	var sawPath string
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: upstream, PlanType: contract.PlanTypeRelayKit,
			UpstreamProtocol: contract.ProtocolOpenAIChat, UpstreamModel: "gpt-upstream",
		}}},
		ConversionEngine: engine,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			sawPath = request.URL.Path
			return jsonResponse(http.StatusOK, `{
				"id":"chatcmpl_1","object":"chat.completion","model":"gpt-upstream",
				"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
			}`), nil
		})),
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(
		`{"model":"public-claude","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`,
	))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.HasSuffix(sawPath, "/v1/chat/completions") {
		t.Fatalf("status=%d path=%q body=%s", response.Code, sawPath, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "gpt-upstream" {
		t.Fatalf("client model = %#v", body["model"])
	}
}

func TestRelayKitGeminiToOpenAIChat(t *testing.T) {
	engine := relaykitbridge.NewEngine()
	upstream := validEndpoint(contract.ProtocolOpenAIChat, true)
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: upstream, PlanType: contract.PlanTypeRelayKit,
			UpstreamProtocol: contract.ProtocolOpenAIChat, UpstreamModel: "gpt-upstream",
		}}},
		ConversionEngine: engine,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(request.URL.Path, "/v1/chat/completions") {
				t.Fatalf("path = %q", request.URL.Path)
			}
			return jsonResponse(http.StatusOK, `{
				"id":"chatcmpl_1","object":"chat.completion","model":"gpt-upstream",
				"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
			}`), nil
		})),
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini-public:generateContent",
		strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	candidates, _ := body["candidates"].([]any)
	if len(candidates) == 0 {
		t.Fatalf("missing gemini candidates: %s", response.Body.String())
	}
}

func TestRelayKitResponsesToClaude(t *testing.T) {
	engine := relaykitbridge.NewEngine()
	upstream := validEndpoint(contract.ProtocolAnthropicMessages, true)
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: upstream, PlanType: contract.PlanTypeRelayKit,
			UpstreamProtocol: contract.ProtocolAnthropicMessages, UpstreamModel: "claude-upstream",
		}}},
		ConversionEngine: engine,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(request.URL.Path, "/v1/messages") {
				t.Fatalf("path = %q", request.URL.Path)
			}
			return jsonResponse(http.StatusOK, `{
				"id":"msg_1","type":"message","role":"assistant","model":"claude-upstream",
				"content":[{"type":"text","text":"done"}],
				"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2}
			}`), nil
		})),
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		`{"model":"public-responses","input":"hello"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "claude-upstream" {
		t.Fatalf("client model = %#v", body["model"])
	}
}

func TestRelayKitStreamsOpenAIChatUpstreamAsResponsesSSE(t *testing.T) {
	engine := relaykitbridge.NewEngine()
	upstream := validEndpoint(contract.ProtocolOpenAIChat, true)
	var sawStream bool
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: upstream, PlanType: contract.PlanTypeRelayKit,
			UpstreamProtocol: contract.ProtocolOpenAIChat, UpstreamModel: "gpt-upstream",
		}}},
		ConversionEngine: engine,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(request.Body)
			var payload struct {
				Stream bool `json:"stream"`
			}
			_ = json.Unmarshal(body, &payload)
			sawStream = payload.Stream
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(
					`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-upstream","choices":[{"index":0,"delta":{"role":"assistant","content":"hel"},"finish_reason":null}]}` + "\n\n" +
						`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-upstream","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}` + "\n\n" +
						"data: [DONE]\n\n",
				)),
			}, nil
		})),
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		`{"model":"public-responses","input":"hi","stream":true}`,
	))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !sawStream {
		t.Fatalf("status=%d stream=%v body=%s", response.Code, sawStream, response.Body.String())
	}
	raw := response.Body.String()
	if strings.Contains(raw, `"Payload"`) || strings.Contains(raw, "[DONE]") {
		t.Fatalf("responses SSE carries RelayKit wrapper or chat terminator: %s", raw)
	}
	var (
		eventTypes []string
		text       strings.Builder
	)
	for _, frame := range strings.Split(strings.TrimSpace(raw), "\n\n") {
		var eventType, data string
		for _, line := range strings.Split(frame, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				eventType = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		var payload struct {
			Type           string `json:"type"`
			SequenceNumber *int   `json:"sequence_number"`
			Delta          string `json:"delta"`
			Response       *struct {
				Model string `json:"model"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatalf("frame %q: %v", frame, err)
		}
		if eventType == "" || payload.Type != eventType {
			t.Fatalf("frame event %q does not match payload type %q: %s", eventType, payload.Type, data)
		}
		if payload.SequenceNumber == nil || *payload.SequenceNumber != len(eventTypes) {
			t.Fatalf("frame %d has sequence_number %v: %s", len(eventTypes), payload.SequenceNumber, data)
		}
		if payload.Response != nil && payload.Response.Model != "gpt-upstream" {
			t.Fatalf("response model = %q, want gpt-upstream: %s", payload.Response.Model, data)
		}
		if payload.Type == "response.output_text.delta" {
			text.WriteString(payload.Delta)
		}
		eventTypes = append(eventTypes, eventType)
	}
	if eventTypes[0] != "response.created" || eventTypes[len(eventTypes)-1] != "response.completed" {
		t.Fatalf("event sequence = %v", eventTypes)
	}
	if text.String() != "hello" {
		t.Fatalf("streamed text = %q, want hello (events %v)", text.String(), eventTypes)
	}
}

func TestRelayKitPrivacyRedactRestoreSurvivesConversion(t *testing.T) {
	engine := relaykitbridge.NewEngine()
	upstream := validEndpoint(contract.ProtocolAnthropicMessages, true)
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, ResponseRestore: true,
	}, nil)
	var upstreamBody string
	var placeholder string
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: upstream, PlanType: contract.PlanTypeRelayKit,
			UpstreamProtocol: contract.ProtocolAnthropicMessages, UpstreamModel: "claude-upstream",
		}}},
		ConversionEngine: engine,
		PrivacyFilter:    filter,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(request.Body)
			upstreamBody = string(raw)
			if strings.Contains(upstreamBody, "alice@example.com") {
				t.Fatalf("email leaked to upstream: %s", upstreamBody)
			}
			var converted struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(raw, &converted); err != nil ||
				len(converted.Messages) == 0 {
				t.Fatalf("converted upstream request=%s err=%v", raw, err)
			}
			placeholder = emailPlaceholderPattern.FindString(converted.Messages[0].Content)
			if placeholder == "" {
				t.Fatalf("converted request has no email placeholder: %s", raw)
			}
			return jsonResponse(http.StatusOK, `{
				"id":"msg_1","type":"message","role":"assistant","model":"claude-upstream",
				"content":[{"type":"text","text":"redacted echo `+placeholder+`"}],
				"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}
			}`), nil
		})),
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"public-chat","messages":[{"role":"user","content":"mail alice@example.com please"}]}`,
	))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if upstreamBody == "" || strings.Contains(upstreamBody, "alice@example.com") {
		t.Fatalf("unexpected upstream body %q", upstreamBody)
	}
	if placeholder == "" ||
		!strings.Contains(response.Body.String(), "redacted echo alice@example.com") ||
		strings.Contains(response.Body.String(), placeholder) {
		t.Fatalf("converted response was not restored: %s", response.Body.String())
	}
}

func TestRelayKitResponseConversionFailureRetriesNextCandidate(t *testing.T) {
	engine := relaykitbridge.NewEngine()
	bad := validEndpoint(contract.ProtocolAnthropicMessages, true)
	bad.ID = "endpoint_bad"
	good := validEndpoint(contract.ProtocolAnthropicMessages, true)
	good.ID = "endpoint_good"
	var attempts atomic.Int32
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{
			{Endpoint: bad, PlanType: contract.PlanTypeRelayKit, UpstreamProtocol: contract.ProtocolAnthropicMessages, UpstreamModel: "claude-bad"},
			{Endpoint: good, PlanType: contract.PlanTypeRelayKit, UpstreamProtocol: contract.ProtocolAnthropicMessages, UpstreamModel: "claude-good"},
		}},
		ConversionEngine: engine,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			n := attempts.Add(1)
			body, _ := io.ReadAll(request.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			model, _ := payload["model"].(string)
			if n == 1 {
				return jsonResponse(http.StatusOK, `{`), nil
			}
			if model != "claude-good" {
				t.Fatalf("second attempt model = %q", model)
			}
			return jsonResponse(http.StatusOK, `{
				"id":"msg_1","type":"message","role":"assistant","model":"claude-good",
				"content":[{"type":"text","text":"ok"}],
				"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}
			}`), nil
		})),
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"public-chat","messages":[{"role":"user","content":"hi"}]}`,
	))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

func TestNoopConversionEngineLeavesNativeBytesUntouched(t *testing.T) {
	const original = `{"model":"gpt-5","input":"native-bytes"}`
	upstream := validEndpoint(contract.ProtocolOpenAIResponses, false)
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: upstream, PlanType: contract.PlanTypeNative,
			UpstreamProtocol: contract.ProtocolOpenAIResponses,
		}}},
		ConversionEngine: relaykitbridge.NoopEngine{},
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(request.Body)
			if string(body) != original {
				t.Fatalf("native body rewritten: %s", body)
			}
			if !strings.HasSuffix(request.URL.Path, "/v1/responses") {
				t.Fatalf("path = %q", request.URL.Path)
			}
			return jsonResponse(http.StatusOK, `{"id":"resp_1","model":"gpt-5","output":[]}`), nil
		})),
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(original))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestHTTPRecoveryUsesSameLoopForEveryExecutionPlan(t *testing.T) {
	for _, plan := range []contract.PlanType{contract.PlanTypeNative, contract.PlanTypeDelegated, contract.PlanTypeRelayKit} {
		t.Run(string(plan), func(t *testing.T) {
			policy := contract.DefaultFailurePolicy()
			policy.InitialDelayMS = 0
			candidates := recoveryCandidates(1, policy, contract.DefaultFailoverPolicy())
			candidates[0].PlanType = plan
			candidates[0].UpstreamModel = "actual"
			if plan == contract.PlanTypeDelegated {
				candidates[0].Mode = contract.CapabilityModeDelegated
				candidates[0].Endpoint.Capabilities[0].Mode = contract.CapabilityModeDelegated
			}
			if plan == contract.PlanTypeRelayKit {
				candidates[0].Endpoint.Capabilities = []contract.Capability{{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative}}
				candidates[0].UpstreamProtocol = contract.ProtocolAnthropicMessages
			}
			attempts := 0
			handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: candidates}, ConversionEngine: relaykitbridge.NewEngine(), Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				attempts++
				body, _ := io.ReadAll(request.Body)
				if !strings.Contains(string(body), `"model":"actual"`) {
					t.Fatalf("attempt %d lost model mapping: %s", attempts, body)
				}
				if attempts == 1 {
					return jsonResponse(503, `{"error":{"message":"retry"}}`), nil
				}
				if plan == contract.PlanTypeRelayKit {
					return jsonResponse(200, `{"id":"msg_done","type":"message","role":"assistant","model":"actual","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`), nil
				}
				return jsonResponse(200, `{"id":"chat_done","model":"actual","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`), nil
			}))})
			response := httptest.NewRecorder()
			request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","messages":[{"role":"user","content":"hello"}]}`))
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(response, request)
			if attempts != 2 || response.Code != 200 || strings.Contains(response.Body.String(), "retry") || !strings.Contains(response.Body.String(), `"model":"actual"`) {
				t.Fatalf("attempts=%d status=%d body=%s", attempts, response.Code, response.Body.String())
			}
		})
	}
}
