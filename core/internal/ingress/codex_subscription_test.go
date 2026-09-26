package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
)

func prepareCodexBody(t *testing.T, protocol contract.ProtocolID, body string) (map[string]json.RawMessage, []byte, bool) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	forced, err := prepareCodexSubscriptionRequest(request, protocol, codexRequestOptions{normalize: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	return fields, raw, forced
}

func TestPrepareCodexSubscriptionRequestNormalizesStructureOnly(t *testing.T) {
	const input = `{"model":"gpt-6-astra","input":[{"role":"user","content":"hello"}],"store":true,
		"reasoning":{"effort":"high"},"include":["file_search_call.results"],"stream":false,
		"previous_response_id":"resp_prev","service_tier":"priority","prompt_cache_key":"cache-1",
		"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],
		"max_output_tokens":10,"max_completion_tokens":10,"temperature":0.2,"top_p":0.9,
		"frequency_penalty":0,"presence_penalty":0,"truncation":"auto","prompt_cache_retention":"24h",
		"safety_identifier":"s","user":"u","metadata":{"a":"b"},"stream_options":{"include_usage":true}}`
	fields, _, forced := prepareCodexBody(t, contract.ProtocolOpenAIResponses, input)
	if !forced {
		t.Fatal("stream:false was not forced upstream")
	}
	for name, want := range map[string]string{
		"store": `false`, "instructions": `""`, "stream": `true`,
		"include":              `["file_search_call.results","reasoning.encrypted_content"]`,
		"previous_response_id": `"resp_prev"`, "service_tier": `"priority"`, "prompt_cache_key": `"cache-1"`,
		"input": `[{"role":"user","content":"hello"}]`,
		"tools": `[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`,
	} {
		if got := string(fields[name]); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
	for _, name := range codexSubscriptionUnsupportedFields {
		if _, ok := fields[name]; ok {
			t.Errorf("unsupported field %s was forwarded", name)
		}
	}

	// Normalizing an already normalized body is a byte-identical no-op.
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	_, again, forcedAgain := prepareCodexBody(t, contract.ProtocolOpenAIResponses, string(encoded))
	if forcedAgain || !bytes.Equal(again, encoded) {
		t.Fatalf("second pass changed the body (forced=%t):\n%s\n%s", forcedAgain, encoded, again)
	}
}

func TestPrepareCodexSubscriptionRequestKeepsCompliantBytes(t *testing.T) {
	const compliant = "{ \"model\": \"gpt-6-astra\",\n  \"instructions\": \"be brief\", \"input\": \"hello\",\n" +
		"  \"store\": false, \"stream\": true, \"reasoning\": {\"effort\": \"low\"},\n" +
		"  \"include\": [\"reasoning.encrypted_content\"] }"
	_, raw, forced := prepareCodexBody(t, contract.ProtocolOpenAIResponses, compliant)
	if forced || string(raw) != compliant {
		t.Fatalf("compliant body rewritten (forced=%t): %s", forced, raw)
	}
}

func TestPrepareCodexSubscriptionRequestEdgeCases(t *testing.T) {
	for _, test := range []struct {
		name, body string
		protocol   contract.ProtocolID
		check      func(t *testing.T, fields map[string]json.RawMessage, forced bool)
	}{
		{
			name: "compact only drops unsupported fields", protocol: contract.ProtocolOpenAIResponsesCompact,
			body: `{"model":"gpt-6-astra","input":"hello","instructions":null,"reasoning":{"effort":"low"},"temperature":1}`,
			check: func(t *testing.T, fields map[string]json.RawMessage, forced bool) {
				if forced || string(fields["instructions"]) != `""` || fields["temperature"] != nil {
					t.Fatalf("compact body = %v forced=%t", fields, forced)
				}
				for _, name := range []string{"store", "stream", "include"} {
					if _, ok := fields[name]; ok {
						t.Fatalf("compact body gained %s: %v", name, fields)
					}
				}
			},
		},
		{
			name: "null reasoning adds no include", protocol: contract.ProtocolOpenAIResponses,
			body: `{"model":"gpt-6-astra","input":"hello","reasoning":null,"stream":true}`,
			check: func(t *testing.T, fields map[string]json.RawMessage, forced bool) {
				if _, ok := fields["include"]; ok || forced {
					t.Fatalf("body = %v forced=%t", fields, forced)
				}
			},
		},
		{
			name: "malformed include is left for upstream", protocol: contract.ProtocolOpenAIResponses,
			body: `{"model":"gpt-6-astra","input":"hello","reasoning":{},"include":"reasoning","stream":true}`,
			check: func(t *testing.T, fields map[string]json.RawMessage, _ bool) {
				if string(fields["include"]) != `"reasoning"` {
					t.Fatalf("include = %s", fields["include"])
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields, _, forced := prepareCodexBody(t, test.protocol, test.body)
			test.check(t, fields, forced)
		})
	}

	for _, invalid := range []string{`[]`, `null`, `{"model":`} {
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(invalid))
		if _, err := prepareCodexSubscriptionRequest(request, contract.ProtocolOpenAIResponses, codexRequestOptions{normalize: true}); err == nil {
			t.Errorf("prepareCodexSubscriptionRequest(%s) succeeded", invalid)
		}
	}
}

type codexSSEUpstream struct {
	hits   atomic.Int32
	bodies chan []byte
	accept chan string
	url    string
}

// newCodexSSEUpstream answers attempt N with streams[min(N, len-1)].
func newCodexSSEUpstream(t *testing.T, streams ...string) *codexSSEUpstream {
	t.Helper()
	upstream := &codexSSEUpstream{bodies: make(chan []byte, 8), accept: make(chan string, 8)}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hit := int(upstream.hits.Add(1)) - 1
		body, _ := io.ReadAll(request.Body)
		upstream.bodies <- body
		upstream.accept <- request.Header.Get("Accept")
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, streams[min(hit, len(streams)-1)])
	}))
	t.Cleanup(server.Close)
	upstream.url = server.URL
	return upstream
}

func codexSSE(events ...string) string {
	var builder strings.Builder
	for _, event := range events {
		var header struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(event), &header)
		builder.WriteString("event: " + header.Type + "\r\ndata: " + event + "\r\n\r\n")
	}
	return builder.String()
}

const codexCompletedWithoutOutput = `{"type":"response.completed","response":{"id":"resp_forced","object":"response","status":"completed","output":[],"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}}}`

func codexOutputItemDone(index int, text string) string {
	return `{"type":"response.output_item.done","output_index":` + strconv.Itoa(index) +
		`,"item":{"id":"msg_` + text + `","type":"message","role":"assistant","content":[{"type":"output_text","text":"` + text + `"}]}}`
}

func serveCodexForcedStream(t *testing.T, candidate endpoint.Resolved, records *memoryRequestRecordStore) *httptest.ResponseRecorder {
	t.Helper()
	dependencies := Dependencies{
		Resolver:   candidateResolver{candidates: []endpoint.Resolved{candidate}},
		Authorizer: endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
	}
	if records != nil {
		dependencies.RequestRecords = records
	}
	handler := NewWithDependencies(dependencies)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses",
		strings.NewReader(`{"model":"gpt-6-astra","input":"hello","stream":false}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "third-party-sdk/1.0")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestCodexSubscriptionAggregatesForcedStream(t *testing.T) {
	t.Parallel()
	upstream := newCodexSSEUpstream(t, codexSSE(
		`{"type":"response.created","response":{"id":"resp_forced","status":"in_progress","output":[]}}`,
		codexOutputItemDone(1, "second"),
		codexOutputItemDone(0, "first"),
		codexCompletedWithoutOutput,
	))
	records := &memoryRequestRecordStore{}
	response := serveCodexForcedStream(t, riskCodexCandidate("service_codex", upstream.url), records)
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("response = %d %q %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var aggregated struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Output []struct {
			ID string `json:"id"`
		} `json:"output"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &aggregated); err != nil {
		t.Fatalf("aggregated body is not JSON: %v %s", err, response.Body.String())
	}
	if aggregated.ID != "resp_forced" || aggregated.Status != "completed" || len(aggregated.Output) != 2 ||
		aggregated.Output[0].ID != "msg_first" || aggregated.Output[1].ID != "msg_second" {
		t.Fatalf("aggregated response = %+v", aggregated)
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(<-upstream.bodies, &sent); err != nil {
		t.Fatal(err)
	}
	if string(sent["stream"]) != "true" || string(sent["store"]) != "false" || <-upstream.accept != "text/event-stream" {
		t.Fatalf("upstream request was not forced to stream: %v", sent)
	}
	if len(records.records) != 1 || records.records[0].Usage == nil ||
		records.records[0].Usage.InputTokens != 11 || records.records[0].Usage.OutputTokens != 4 ||
		records.records[0].Usage.BillingIncomplete {
		t.Fatalf("usage from forced stream = %+v", records.records)
	}
}

func TestCodexSubscriptionForcedStreamFailureStatus(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, event, code string
		status            int
	}{
		{"rate limit", `{"type":"response.failed","response":{"id":"resp_failed","error":{"code":"rate_limit_exceeded","message":"slow down"}}}`, "rate_limit_exceeded", http.StatusTooManyRequests},
		{"context length", `{"type":"error","error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"too long"}}`, "context_length_exceeded", http.StatusBadRequest},
		{"flat error", `{"type":"error","code":"invalid_prompt","message":"bad prompt"}`, "invalid_prompt", http.StatusBadRequest},
		{"server error", `{"type":"response.failed","response":{"id":"resp_failed","error":{"code":"server_error","message":"oops"}}}`, "server_error", http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			upstream := newCodexSSEUpstream(t, codexSSE(test.event))
			response := serveCodexForcedStream(t, riskCodexCandidate("service_codex", upstream.url), nil)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if response.Code != test.status || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Error.Code != test.code {
				t.Fatalf("response = %d %s, want %d %s", response.Code, response.Body.String(), test.status, test.code)
			}
			if hits := upstream.hits.Load(); hits != 1 {
				t.Fatalf("failed stream was retried %d times", hits)
			}
		})
	}
}

func TestCodexSubscriptionTruncatedForcedStreamIsRetried(t *testing.T) {
	t.Parallel()
	upstream := newCodexSSEUpstream(t,
		codexSSE(`{"type":"response.created","response":{"id":"resp_cut","output":[]}}`),
		codexSSE(codexOutputItemDone(0, "first"), codexCompletedWithoutOutput),
	)
	response := serveCodexForcedStream(t, riskCodexCandidate("service_codex", upstream.url), nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"msg_first"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if hits := upstream.hits.Load(); hits != 2 {
		t.Fatalf("upstream hits = %d, want a retry after the truncated stream", hits)
	}
}

func TestCodexSubscriptionConvertedNonStreamingRequestAggregates(t *testing.T) {
	t.Parallel()
	upstream := newCodexSSEUpstream(t, codexSSE(
		codexOutputItemDone(0, "converted"),
		`{"type":"response.completed","response":{"id":"resp_converted","object":"response","model":"gpt-6-astra","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`,
	))
	candidate := codexVersionCandidate(upstream.url + "/backend-api/codex")
	candidate.Service.Capabilities = append(candidate.Service.Capabilities, contract.Capability{
		Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true,
		ConvertTo: contract.ProtocolOpenAIResponses,
	})
	candidate.UpstreamProtocol = contract.ProtocolOpenAIResponses
	handler := NewWithDependencies(Dependencies{
		Resolver:         candidateResolver{candidates: []endpoint.Resolved{candidate}},
		Authorizer:       endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
		ConversionEngine: relaykitbridge.NewEngine(),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(
		`{"model":"gpt-6-astra","max_tokens":64,"temperature":0.3,"messages":[{"role":"user","content":"hello"}],"stream":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var message struct {
		Type    string `json:"type"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &message) != nil ||
		message.Type != "message" || len(message.Content) == 0 || message.Content[0].Text != "converted" {
		t.Fatalf("converted response = %d %s", response.Code, response.Body.String())
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(<-upstream.bodies, &sent); err != nil {
		t.Fatal(err)
	}
	if string(sent["stream"]) != "true" || sent["temperature"] != nil || sent["max_output_tokens"] != nil {
		t.Fatalf("converted upstream body = %v", sent)
	}
}

func TestCodexSubscriptionForwardsCompliantNativeBodyUnchanged(t *testing.T) {
	t.Parallel()
	const compliant = `{"model":"gpt-6-astra","instructions":"be brief","input":"hello","store":false,"stream":true}`
	upstream := newCodexSSEUpstream(t, codexSSE(codexCompletedWithoutOutput))
	handler := NewWithDependencies(Dependencies{
		Resolver:   candidateResolver{candidates: []endpoint.Resolved{riskCodexCandidate("service_codex", upstream.url)}},
		Authorizer: endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(compliant))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("User-Agent", "codex_cli_rs/0.156.0 (Mac OS; arm64)")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: response.completed") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if sent := <-upstream.bodies; string(sent) != compliant {
		t.Fatalf("compliant body changed:\n got %s\nwant %s", sent, compliant)
	}
}
