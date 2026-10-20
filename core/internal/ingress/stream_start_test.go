package ingress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const (
	overloadedCodexStream = "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_busy\",\"status\":\"in_progress\",\"output\":[]}}\n\n" +
		"event: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"sequence_number\":1,\"response\":{\"id\":\"resp_busy\",\"status\":\"in_progress\"}}\n\n" +
		"event: error\ndata: {\"type\":\"error\",\"sequence_number\":2,\"error\":{\"type\":\"service_unavailable_error\",\"code\":\"server_is_overloaded\",\"message\":\"Our servers are currently overloaded. Please try again later.\",\"param\":null}}\n\n" +
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"sequence_number\":3,\"response\":{\"id\":\"resp_busy\",\"status\":\"failed\",\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"Our servers are currently overloaded. Please try again later.\"}}}\n\n"
	completedCodexStream = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_ok\",\"status\":\"in_progress\",\"output\":[]}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n"
)

func sseResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func streamCandidates(protocol contract.ProtocolID, count int, failover contract.FailoverStrategy) []endpoint.Resolved {
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 0
	candidates := make([]endpoint.Resolved, count)
	for index := range candidates {
		service := validEndpoint(protocol, true)
		service.ID = contract.ServiceID("endpoint_" + string(rune('a'+index)))
		service.BaseURL = "https://" + string(rune('A'+index)) + ".example"
		candidates[index] = endpoint.Resolved{Endpoint: service, FailurePolicy: &policy,
			Failover: &contract.FailoverPolicy{Enabled: true, Strategy: failover, MaxAttempts: 6}}
	}
	return candidates
}

func serveStream(t *testing.T, candidates []endpoint.Resolved, records RequestRecordStore, path, body string, streams map[string]string) (*httptest.ResponseRecorder, []string) {
	t.Helper()
	var attempts []string
	handler := NewWithDependencies(Dependencies{
		Resolver:       candidateResolver{candidates: candidates},
		RequestRecords: records,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts = append(attempts, request.URL.Host)
			if _, err := io.ReadAll(request.Body); err != nil {
				return nil, err
			}
			return sseResponse(streams[request.URL.Host]), nil
		})),
	})
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response, attempts
}

// splitRecords returns the finished root record and its failed attempts.
func splitRecords(t *testing.T, store *memoryRequestRecordStore) (contract.RequestRecord, []contract.RequestRecord) {
	t.Helper()
	var root *contract.RequestRecord
	var children []contract.RequestRecord
	for _, record := range store.records {
		if record.ParentRequestID != nil {
			children = append(children, record)
			continue
		}
		copy := record
		root = &copy
	}
	if root == nil {
		t.Fatalf("no root record among %+v", store.records)
	}
	return *root, children
}

// Issue #77: the Codex backend answers HTTP 200 and refuses the request in the
// stream. The refusal stands for a 503, so under failover_only the next
// account serves the request and the client never sees the refusal.
func TestStreamRefusalFailsOverBeforeTheClientSeesIt(t *testing.T) {
	t.Parallel()
	busy := newRiskUpstream(t, http.StatusOK, "text/event-stream", overloadedCodexStream, nil)
	healthy := newRiskUpstream(t, http.StatusOK, "text/event-stream", completedCodexStream, nil)
	first := riskCodexCandidate("service_codex_busy", busy.url)
	second := riskCodexCandidate("service_codex_healthy", healthy.url)
	first.Failover = &contract.FailoverPolicy{Enabled: true, Strategy: contract.FailoverOnly, MaxAttempts: 6}
	store := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{
		Resolver:       candidateResolver{candidates: []endpoint.Resolved{first, second}},
		Authorizer:     endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
		RequestRecords: store,
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-6-astra","input":"hello","stream":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != completedCodexStream {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if busy.hits.Load() != 1 || healthy.hits.Load() != 1 {
		t.Fatalf("hits busy=%d healthy=%d, want one each", busy.hits.Load(), healthy.hits.Load())
	}
	root, children := splitRecords(t, store)
	if root.Status != contract.RequestStatusSucceeded || root.ServiceID == nil || *root.ServiceID != second.Service.ID {
		t.Fatalf("root = %s on %v", root.Status, root.ServiceID)
	}
	if len(children) != 1 {
		t.Fatalf("children = %+v", children)
	}
	child := children[0]
	if child.Status != contract.RequestStatusFailed || child.Error == nil || child.Error.Code != "upstream_stream_failed" ||
		!child.Error.Retryable || !strings.Contains(child.Error.Message, "currently overloaded") {
		t.Fatalf("failed attempt = %s %+v", child.Status, child.Error)
	}
	if child.Recovery == nil || child.Recovery.Action != "failover" || child.Recovery.Reason != "stream_503" {
		t.Fatalf("failed attempt recovery = %+v", child.Recovery)
	}
}

// Issue #86: empty output containers are still announcements. None of these
// events should commit the response before the subsequent overload arrives.
func TestResponsesMetadataBeforeOverloadFailsOver(t *testing.T) {
	t.Parallel()
	for _, metadata := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","summary":[],"encrypted_content":null}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","summary":[],"encrypted_content":""}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}`,
		`{"type":"response.output_text.delta","delta":""}`,
		`{"type":"response.reasoning_summary_text.delta","delta":""}`,
	} {
		t.Run(metadata, func(t *testing.T) {
			store := &memoryRequestRecordStore{}
			refused := strings.Replace(overloadedCodexStream, "event: error", "data: "+metadata+"\n\nevent: error", 1)
			response, attempts := serveStream(t, streamCandidates(contract.ProtocolOpenAIResponses, 2, contract.FailoverOnly), store,
				"/v1/responses", `{"model":"m","input":"hi","stream":true}`,
				map[string]string{"A.example": refused, "B.example": completedCodexStream})
			if response.Code != http.StatusOK || response.Body.String() != completedCodexStream ||
				!reflect.DeepEqual(attempts, []string{"A.example", "B.example"}) {
				t.Fatalf("response = %d %q after %v", response.Code, response.Body.String(), attempts)
			}
			root, children := splitRecords(t, store)
			if root.Status != contract.RequestStatusSucceeded || len(children) != 1 ||
				children[0].FirstTokenMs != nil || children[0].Recovery == nil || children[0].Recovery.Reason != "stream_503" {
				t.Fatalf("root = %s, children = %+v", root.Status, children)
			}
		})
	}
}

// Content and unknown event types must remain committed, including tool calls
// and encrypted reasoning which do not necessarily have a text delta.
func TestResponsesOutputBeforeOverloadIsNotRetried(t *testing.T) {
	t.Parallel()
	for _, output := range []string{
		`{"type":"response.output_text.delta","delta":"partial"}`,
		`{"type":"response.reasoning_summary_text.delta","delta":"Thinking"}`,
		`{"type":"response.output_item.added","item":{"type":"message","content":[{"type":"output_text","text":"partial"}]}}`,
		`{"type":"response.output_item.added","item":{"type":"reasoning","summary":[{"type":"summary_text","text":"Thinking"}]}}`,
		`{"type":"response.output_item.added","item":{"type":"reasoning","summary":[],"encrypted_content":"ciphertext"}}`,
		`{"type":"response.content_part.added","part":{"type":"output_text","text":"partial"}}`,
		`{"type":"response.output_item.added","item":{"type":"function_call","name":"run_command","arguments":""}}`,
		`{"type":"response.output_item.added","item":{"type":"web_search_call","status":"in_progress"}}`,
		`{"type":"response.image_generation_call.partial_image","partial_image_b64":"image"}`,
		`{"type":"future.event","content":"opaque"}`,
	} {
		t.Run(output, func(t *testing.T) {
			broken := strings.Replace(overloadedCodexStream, "event: error", "data: "+output+"\n\nevent: error", 1)
			response, attempts := serveStream(t, streamCandidates(contract.ProtocolOpenAIResponses, 2, contract.FailoverOnly), nil,
				"/v1/responses", `{"model":"m","input":"hi","stream":true}`, map[string]string{"A.example": broken})
			if response.Code != http.StatusOK || response.Body.String() != broken || !reflect.DeepEqual(attempts, []string{"A.example"}) {
				t.Fatalf("response = %d %q after %v", response.Code, response.Body.String(), attempts)
			}
		})
	}
}

func TestStreamOverloadCoolsOnlyItsRouteForLaterRequests(t *testing.T) {
	t.Parallel()
	const output = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
	recovered := strings.Split(overloadedCodexStream, "event: response.failed")[0] + completedCodexStream
	generic := strings.NewReplacer("server_is_overloaded", "server_error", "service_unavailable_error", "server_error").Replace(overloadedCodexStream)
	withoutCode := strings.ReplaceAll(overloadedCodexStream, `"code":"server_is_overloaded",`, "")
	for _, test := range []struct {
		name, body, retryAfter string
		failover, cooldown     bool
	}{
		{"held refusal", overloadedCodexStream, "", true, true},
		{"held refusal without code", withoutCode, "", true, true},
		{"held refusal with Retry-After", overloadedCodexStream, "120", true, true},
		{"committed failure", output + overloadedCodexStream, "120", false, true},
		{"recovered error", recovered, "120", false, false},
		{"generic failure", generic, "", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidates := streamCandidates(contract.ProtocolOpenAIResponses, 2, contract.FailoverOnly)
			services := redirectServices{}
			for _, candidate := range candidates {
				service := candidate.CanonicalService()
				service.Models = []string{"m", "other"}
				policy := *candidate.FailurePolicy
				policy.MaxRetries = 0
				service.FailurePolicy = &policy
				services = append(services, service)
			}
			resolver, err := endpoint.NewStoreResolver(services)
			if err != nil {
				t.Fatal(err)
			}
			var attempts []string
			first := true
			handler := NewWithDependencies(Dependencies{
				Resolver: resolver,
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					attempts = append(attempts, request.URL.Host)
					if first && request.URL.Host == "A.example" {
						first = false
						response := sseResponse(test.body)
						if test.retryAfter != "" {
							response.Header.Set("Retry-After", test.retryAfter)
						}
						return response, nil
					}
					return sseResponse(completedCodexStream), nil
				})),
			})
			serve := func(model string) {
				t.Helper()
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses",
					strings.NewReader(`{"model":"`+model+`","input":"hi","stream":true}`)))
				if response.Code != http.StatusOK {
					t.Fatalf("response = %d %s", response.Code, response.Body.String())
				}
			}
			before := time.Now()
			serve("m")
			want := []string{"A.example", "B.example"}
			if !test.failover {
				want = want[:1]
			}
			if !reflect.DeepEqual(attempts, want) {
				t.Fatalf("first request attempts = %v, want %v", attempts, want)
			}
			// Fresh sessions must skip A for m, but can still use its other model.
			request := endpoint.ResolveRequest{Protocol: contract.ProtocolOpenAIResponses, Model: "m", Streaming: true}
			resolved, _, err := resolver.ResolveRankedCandidates(context.Background(), request)
			count, firstIndex := 2, 0
			if test.cooldown {
				count, firstIndex = 1, 1
			}
			if err != nil || len(resolved) != count || resolved[0].CanonicalService().ID != candidates[firstIndex].CanonicalService().ID {
				t.Fatalf("cooled candidates = %+v, %v", resolved, err)
			}
			limited := candidates[0]
			limited.UpstreamProtocol, limited.UpstreamModel = request.Protocol, request.Model
			until := resolver.RateLimitedUntil(limited)
			if test.cooldown {
				minimum := max(30*time.Second, parseRetryAfter(test.retryAfter, before))
				if until.Before(before.Add(minimum)) {
					t.Fatalf("cooldown = %v, want at least %s", until, minimum)
				}
			} else if !until.IsZero() {
				t.Fatalf("non-overload response established cooldown until %v", until)
			}
			attempts = nil
			serve("m")
			serve("other")
			resolver.ReleaseRateLimits(candidates[0].CanonicalService().ID)
			serve("m")
			want = []string{"A.example", "A.example", "A.example"}
			if test.cooldown {
				want[0] = "B.example"
			}
			if !reflect.DeepEqual(attempts, want) {
				t.Fatalf("later attempts = %v", attempts)
			}
		})
	}
}

func TestEveryStreamRouteCoolingReportsOverloadWithoutAnotherAttempt(t *testing.T) {
	t.Parallel()
	service := streamCandidates(contract.ProtocolOpenAIResponses, 1, contract.FailoverOnly)[0].CanonicalService()
	service.Models = []string{"m"}
	resolver, err := endpoint.NewStoreResolver(redirectServices{service})
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	handler := NewWithDependencies(Dependencies{
		Resolver: resolver,
		Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			return sseResponse(overloadedCodexStream), nil
		})),
	})
	serve := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses",
			strings.NewReader(`{"model":"m","input":"hi","stream":true}`)))
		return response
	}
	if first := serve(); first.Code != http.StatusOK || first.Body.String() != overloadedCodexStream {
		t.Fatalf("first response = %d %q", first.Code, first.Body.String())
	}
	response := serve()
	envelope := assertInferenceError(t, response, http.StatusTooManyRequests, "upstream_rate_limited")
	if attempts != 1 || !strings.Contains(envelope.Error.Message, "overload") ||
		strings.Contains(envelope.Error.Message, "answered HTTP 429") || parseRetryAfter(response.Header().Get("Retry-After"), time.Now()) <= 0 {
		t.Fatalf("cooling response = %s, headers = %v, attempts = %d", response.Body, response.Header(), attempts)
	}
}

func TestAnthropicStreamOverloadFailsOver(t *testing.T) {
	t.Parallel()
	const overloaded = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_busy\"}}\n\n" +
		"event: ping\ndata: {\"type\":\"ping\"}\n\n" +
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
	const completed = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_ok\"}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	response, attempts := serveStream(t, streamCandidates(contract.ProtocolAnthropicMessages, 2, contract.FailoverOnly), nil,
		"/v1/messages", `{"model":"claude-opus-5-5","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{"A.example": overloaded, "B.example": completed})
	if response.Code != http.StatusOK || response.Body.String() != completed ||
		!reflect.DeepEqual(attempts, []string{"A.example", "B.example"}) {
		t.Fatalf("response = %d %q after %v", response.Code, response.Body.String(), attempts)
	}
}

// A refusal whose rule says stop reaches the client unchanged, and the record
// fails instead of claiming success.
func TestStreamRefusalPassesThroughWhenTheRuleStops(t *testing.T) {
	t.Parallel()
	const refused = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_bad\"}}\n\n" +
		"event: error\ndata: {\"type\":\"error\",\"code\":\"invalid_prompt\",\"message\":\"Invalid prompt.\"}\n\n"
	store := &memoryRequestRecordStore{}
	response, attempts := serveStream(t, streamCandidates(contract.ProtocolOpenAIResponses, 2, contract.FailoverOnly), store,
		"/v1/responses", `{"model":"m","input":"hi","stream":true}`, map[string]string{"A.example": refused})
	if response.Code != http.StatusOK || response.Body.String() != refused || len(attempts) != 1 {
		t.Fatalf("response = %d %q after %v", response.Code, response.Body.String(), attempts)
	}
	root, children := splitRecords(t, store)
	if root.Status != contract.RequestStatusFailed || root.Error == nil || root.Error.Code != "upstream_stream_failed" ||
		root.Error.Retryable || root.Error.Message != "invalid_prompt: Invalid prompt." || len(children) != 0 {
		t.Fatalf("record = %s %+v, children %d", root.Status, root.Error, len(children))
	}
	if root.Recovery == nil || root.Recovery.StopReason != "error_rule" {
		t.Fatalf("recovery = %+v", root.Recovery)
	}
}

func TestStreamRefusalFromEveryProviderReachesTheClient(t *testing.T) {
	t.Parallel()
	store := &memoryRequestRecordStore{}
	response, attempts := serveStream(t, streamCandidates(contract.ProtocolOpenAIResponses, 2, contract.FailoverOnly), store,
		"/v1/responses", `{"model":"m","input":"hi","stream":true}`,
		map[string]string{"A.example": overloadedCodexStream, "B.example": overloadedCodexStream})
	if response.Code != http.StatusOK || response.Body.String() != overloadedCodexStream ||
		!reflect.DeepEqual(attempts, []string{"A.example", "B.example"}) {
		t.Fatalf("response = %d %q after %v", response.Code, response.Body.String(), attempts)
	}
	root, children := splitRecords(t, store)
	if root.Status != contract.RequestStatusFailed || root.Error == nil || root.Error.Code != "upstream_stream_failed" ||
		len(children) != 1 || children[0].Error == nil || children[0].Error.Code != "upstream_stream_failed" {
		t.Fatalf("root = %s %+v, children = %+v", root.Status, root.Error, children)
	}
	if root.Recovery == nil || root.Recovery.StopReason != "targets_exhausted" {
		t.Fatalf("recovery = %+v", root.Recovery)
	}
}

// Once the stream carried output, the client owns it: a later failure event
// is not retried, but the record still fails.
func TestStreamFailureAfterOutputIsRecordedWithoutRetry(t *testing.T) {
	t.Parallel()
	const broken = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_cut\"}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_cut\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"The server had an error.\"}}}\n\n"
	store := &memoryRequestRecordStore{}
	response, attempts := serveStream(t, streamCandidates(contract.ProtocolOpenAIResponses, 2, contract.FailoverOnly), store,
		"/v1/responses", `{"model":"m","input":"hi","stream":true}`, map[string]string{"A.example": broken})
	if response.Code != http.StatusOK || response.Body.String() != broken || len(attempts) != 1 {
		t.Fatalf("response = %d %q after %v", response.Code, response.Body.String(), attempts)
	}
	root, _ := splitRecords(t, store)
	if root.Status != contract.RequestStatusFailed || root.Error == nil || root.Error.Code != "upstream_stream_failed" ||
		root.Error.Message != "server_error: The server had an error." {
		t.Fatalf("record = %s %+v", root.Status, root.Error)
	}
	if root.Recovery == nil || root.Recovery.StopReason != "response_committed" {
		t.Fatalf("recovery = %+v", root.Recovery)
	}
}

// The Codex backend can send an error event and still complete. Output after
// the error shows the upstream went on, so nothing is retried and the record
// succeeds.
func TestStreamErrorFollowedByOutputPassesThrough(t *testing.T) {
	t.Parallel()
	const recovered = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_on\"}}\n\n" +
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"code\":\"slow_down\",\"message\":\"busy\"}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_on\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n"
	store := &memoryRequestRecordStore{}
	response, attempts := serveStream(t, streamCandidates(contract.ProtocolOpenAIResponses, 2, contract.FailoverOnly), store,
		"/v1/responses", `{"model":"m","input":"hi","stream":true}`, map[string]string{"A.example": recovered})
	if response.Code != http.StatusOK || response.Body.String() != recovered || len(attempts) != 1 {
		t.Fatalf("response = %d %q after %v", response.Code, response.Body.String(), attempts)
	}
	if root, _ := splitRecords(t, store); root.Status != contract.RequestStatusSucceeded || root.Error != nil {
		t.Fatalf("record = %s %+v", root.Status, root.Error)
	}
}

// A stream that breaks while only its announcements were held failed like a
// dropped connection: nothing reached the client, so the next provider serves.
func TestStreamBrokenDuringHoldFailsOver(t *testing.T) {
	t.Parallel()
	var attempts []string
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: streamCandidates(contract.ProtocolOpenAIResponses, 2, contract.FailoverOnly)},
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts = append(attempts, request.URL.Host)
			if request.URL.Host == "A.example" {
				response := sseResponse("")
				response.Body = io.NopCloser(io.MultiReader(
					strings.NewReader("event: response.created\ndata: {\"type\":\"response.created\"}\n\n"),
					failingReader{err: errors.New("connection reset")},
				))
				return response, nil
			}
			return sseResponse(completedCodexStream), nil
		})),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"m","input":"hi","stream":true}`)))
	if response.Code != http.StatusOK || response.Body.String() != completedCodexStream ||
		!reflect.DeepEqual(attempts, []string{"A.example", "B.example"}) {
		t.Fatalf("response = %d %q after %v", response.Code, response.Body.String(), attempts)
	}
}

// gatedReader returns each chunk sent to it and ends when the channel closes.
type gatedReader struct {
	chunks chan string
}

func (reader gatedReader) Read(buffer []byte) (int, error) {
	chunk, ok := <-reader.chunks
	if !ok {
		return 0, io.EOF
	}
	return copy(buffer, chunk), nil
}

func (gatedReader) Close() error { return nil }

func TestHoldStreamStartLetsSlowAnnouncementsThrough(t *testing.T) {
	t.Parallel()
	const created = "event: response.created\ndata: {\"type\":\"response.created\"}\n\n"
	const rest = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"late\"}\n\n"
	upstream := gatedReader{chunks: make(chan string, 1)}
	upstream.chunks <- created
	// The next read waits for the test, so only the limit can end the hold.
	body, failure, err := holdStreamStart(upstream, contract.ProtocolOpenAIResponses, 1)
	if failure != nil || err != nil {
		t.Fatalf("failure = %+v, err = %v", failure, err)
	}
	head := make([]byte, len(created))
	if _, err := io.ReadFull(body, head); err != nil || string(head) != created {
		t.Fatalf("head = %q, %v", head, err)
	}
	upstream.chunks <- rest
	close(upstream.chunks)
	if tail, err := io.ReadAll(body); err != nil || string(tail) != rest {
		t.Fatalf("tail = %q, %v", tail, err)
	}
}

func TestHoldStreamStartReadsRefusalsAcrossChunks(t *testing.T) {
	t.Parallel()
	// The event name and a CRLF line end are split across reads.
	chunks := []string{
		"event: message_start\r\ndata: {\"type\":\"message_start\"}\r\n\r\n: keep-alive\r\n\r\nevent: err",
		"or\r\ndata: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"message\":\"slow down\"}}\r",
		"\n\r\n",
	}
	stream := strings.Join(chunks, "")
	upstream := gatedReader{chunks: make(chan string, len(chunks))}
	for _, chunk := range chunks {
		upstream.chunks <- chunk
	}
	close(upstream.chunks)
	body, failure, err := holdStreamStart(upstream, contract.ProtocolAnthropicMessages, streamStartHoldLimit)
	if err != nil || failure == nil || failure.status != http.StatusTooManyRequests ||
		!bytes.Contains(failure.event, []byte("rate_limit_error")) {
		t.Fatalf("failure = %+v, err = %v", failure, err)
	}
	if replay, err := io.ReadAll(body); err != nil || string(replay) != stream {
		t.Fatalf("replay = %q, %v", replay, err)
	}
}

func TestHoldStreamStartEndings(t *testing.T) {
	t.Parallel()
	const created = "event: response.created\ndata: {\"type\":\"response.created\"}\n\n"
	broken := errors.New("connection reset")
	_, failure, err := holdStreamStart(io.NopCloser(io.MultiReader(strings.NewReader(created), failingReader{err: broken})),
		contract.ProtocolOpenAIResponses, streamStartHoldLimit)
	if failure != nil || !errors.Is(err, broken) {
		t.Fatalf("broken stream: failure = %+v, err = %v", failure, err)
	}
	body, failure, err := holdStreamStart(io.NopCloser(strings.NewReader(created)), contract.ProtocolOpenAIResponses, streamStartHoldLimit)
	if failure != nil || err != nil {
		t.Fatalf("short stream: failure = %+v, err = %v", failure, err)
	}
	if replay, err := io.ReadAll(body); err != nil || string(replay) != created {
		t.Fatalf("short stream replay = %q, %v", replay, err)
	}
	// The last event may lack its closing blank line.
	refused := created + "event: error\ndata: {\"type\":\"error\",\"code\":\"rate_limit_exceeded\",\"message\":\"slow down\"}"
	_, failure, err = holdStreamStart(io.NopCloser(strings.NewReader(refused)), contract.ProtocolOpenAIResponses, streamStartHoldLimit)
	if err != nil || failure == nil || failure.status != http.StatusTooManyRequests {
		t.Fatalf("unterminated refusal: failure = %+v, err = %v", failure, err)
	}
}

func TestStreamFailureStatus(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		protocol contract.ProtocolID
		event    string
		status   int
	}{
		{contract.ProtocolOpenAIResponses, `{"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded"}}`, http.StatusServiceUnavailable},
		{contract.ProtocolOpenAIResponses, `{"type":"error","error":{"type":"service_unavailable_error","message":"Our servers are currently overloaded"}}`, http.StatusServiceUnavailable},
		{contract.ProtocolOpenAIResponses, `{"type":"response.failed","response":{"error":{"code":"slow_down"}}}`, http.StatusServiceUnavailable},
		{contract.ProtocolOpenAIResponses, `{"type":"response.failed","response":{"error":{"code":"usage_limit_reached"}}}`, http.StatusTooManyRequests},
		{contract.ProtocolOpenAIResponses, `{"type":"error","code":"rate_limit_exceeded","message":"slow down"}`, http.StatusTooManyRequests},
		{contract.ProtocolOpenAIResponses, `{"type":"response.failed","response":{"error":{"code":"cyber_policy"}}}`, http.StatusBadRequest},
		{contract.ProtocolOpenAIResponses, `{"type":"error","error":{"type":"invalid_request_error","code":"invalid_encrypted_content"}}`, http.StatusBadRequest},
		{contract.ProtocolOpenAIResponses, `{"type":"response.failed","response":{"error":{"code":"server_error"}}}`, http.StatusBadGateway},
		{contract.ProtocolOpenAIResponses, `{"type":"error"}`, http.StatusBadGateway},
		{contract.ProtocolAnthropicMessages, `{"type":"error","error":{"type":"overloaded_error"}}`, 529},
		{contract.ProtocolAnthropicMessages, `{"type":"error","error":{"type":"rate_limit_error"}}`, http.StatusTooManyRequests},
		{contract.ProtocolAnthropicMessages, `{"type":"error","error":{"type":"invalid_request_error"}}`, http.StatusBadRequest},
		{contract.ProtocolAnthropicMessages, `{"type":"error","error":{"type":"api_error"}}`, http.StatusInternalServerError},
	} {
		if status := streamFailureStatus(test.protocol, []byte(test.event)); status != test.status {
			t.Errorf("%s %s: status = %d, want %d", test.protocol, test.event, status, test.status)
		}
	}
}

func TestStreamFailureSummaryUsesTheProvidersWords(t *testing.T) {
	t.Parallel()
	summary := streamFailureSummary(contract.ProtocolAnthropicMessages, []byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
	if summary.Code != "upstream_stream_failed" || summary.Category != "upstream" || !summary.Retryable ||
		summary.Message != "overloaded_error: Overloaded" || summary.Upstream != nil {
		t.Fatalf("summary = %+v", summary)
	}
	if err := summary.Validate(); err != nil {
		t.Fatal(err)
	}
	summary = streamFailureSummary(contract.ProtocolOpenAIResponses, []byte(`{"type":"response.failed","response":{"error":null}}`))
	if summary.Message != "upstream reported an error in its stream" || !summary.Retryable {
		t.Fatalf("summary without details = %+v", summary)
	}
}
