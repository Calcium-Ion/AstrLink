package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const invalidReasoningCipher = `{"error":{"code":"invalid_encrypted_content","type":"invalid_request_error","message":"The encrypted content could not be verified."}}`
const invalidFunctionOutputCipher = `{"error":{"code":"invalid_encrypted_content","type":"invalid_request_error","message":"Encrypted function output content could not be decrypted or decoded."}}`
const namedReasoningCipher = `{"error":{"code":"invalid_encrypted_content","type":"invalid_request_error","message":"The encrypted content for item rs_bad could not be verified. Reason: Encrypted content could not be decrypted or parsed."}}`
const encryptedFunctionOutputRequest = `{"model":"public","store":false,"include":["reasoning.encrypted_content"],"input":[{"type":"message","role":"user","content":"hello"},{"type":"reasoning","id":"rs_bad","encrypted_content":"invalid","summary":[{"type":"summary_text","text":"look up"}]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{}"},{"type":"function_call_output","id":"fco_1","call_id":"call_1","output":[{"type":"encrypted_content","encrypted_content":"gAAAAA"},{"type":"text","text":"visible"}]},{"type":"function_call_output","id":"fco_2","call_id":"call_2","output":[{"type":"encrypted_content","encrypted_content":"gBBBBB"}]},{"type":"custom_tool_call_output","id":"cto_1","call_id":"call_3","output":[{"type":"encrypted_content","encrypted_content":"gCCCCC"}]}]}`
const codexReasoningRequest = `{"model":"public","store":false,"reasoning":{"effort":"high"},"include":["reasoning.encrypted_content"],"input":[{"type":"message","role":"user","content":"hello"},{"type":"reasoning","id":"rs_bad","encrypted_content":"invalid","content":null,"summary":[{"type":"summary_text","text":"look up the result"}],"nonce":9007199254740993},{"type":"reasoning","id":"rs_empty","encrypted_content":"invalid2","summary":[]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"n\":9007199254740993}"},{"type":"function_call_output","call_id":"call_1","output":"result"},{"type":"message","role":"assistant","id":"msg_1","phase":"final_answer","content":[{"type":"output_text","text":"done"}]}]}`

func reasoningErrorJSON(code, message, param string) []byte {
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": message, "param": param}})
	return body
}

func reasoningInput(t *testing.T, body []byte) []map[string]json.RawMessage {
	t.Helper()
	var doc struct {
		Input []map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Input
}

func TestOpenAIReasoningCipherRepairPreservesVisibleHistory(t *testing.T) {
	original := []byte(codexReasoningRequest)
	repaired, changed := rectifyOpenAIReasoning(original, []byte(invalidReasoningCipher))
	if !changed || string(original) != codexReasoningRequest {
		t.Fatal("missing repair or mutated original")
	}
	items := reasoningInput(t, repaired)
	if len(items) != 5 {
		t.Fatalf("items=%s", repaired)
	}
	if len(items[1]["encrypted_content"]) > 0 || len(items[1]["id"]) > 0 || len(items[1]["content"]) > 0 || !bytes.Contains(items[1]["summary"], []byte("look up the result")) || string(items[1]["nonce"]) != "9007199254740993" {
		t.Fatalf("bad reasoning repair: %s", repaired)
	}
	for i, originalIndex := range []int{0, 3, 4, 5} {
		repairedIndex := i
		if i > 0 {
			repairedIndex++
		}
		before, _ := json.Marshal(reasoningInput(t, original)[originalIndex])
		after, _ := json.Marshal(items[repairedIndex])
		if !bytes.Equal(before, after) {
			t.Fatalf("changed non-reasoning item: %s => %s", before, after)
		}
	}
	for _, text := range []string{`"reasoning":{"effort":"high"}`, `"include":["reasoning.encrypted_content"]`, `"store":false`} {
		if !bytes.Contains(repaired, []byte(text)) {
			t.Fatalf("changed request option %s", text)
		}
	}
	if _, changed := rectifyOpenAIReasoning(repaired, []byte(invalidReasoningCipher)); changed {
		t.Fatal("repaired twice")
	}
}

func TestOpenAIReasoningSpecificErrorRepairs(t *testing.T) {
	for _, tt := range []struct {
		name, body, code, message, param string
		wantItems                        int
		absentField                      string
		item                             int
	}{
		{"indexed ciphertext", codexReasoningRequest, "invalid_encrypted_content", "bad ciphertext", "input[1].encrypted_content", 6, "encrypted_content", 1},
		{"dotted ciphertext", codexReasoningRequest, "invalid_encrypted_content", "bad ciphertext", "input.1.encrypted_content", 6, "encrypted_content", 1},
		{"null content", codexReasoningRequest, "invalid_type", "Invalid type for 'input[1].content': expected an array, but got null.", "input[1].content", 6, "content", 1},
		{"unsupported status", `{"input":[{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"keep","status":"completed"},{"role":"user","content":"continue"}]}`, "unknown_parameter", "Unknown parameter: 'input[0].status'.", "", 2, "status", 0},
		{"unsupported content", `{"input":[{"type":"reasoning","summary":[],"encrypted_content":"keep","content":[{"type":"reasoning_text","text":"text"}]},{"role":"user","content":"continue"}]}`, "array_above_max_length", "Invalid 'input[0].content': array too long. Expected an array with maximum length 0, but got an array with length 1 instead.", "input[0].content", 2, "content", 0},
		{"named ciphertext", `{"input":[{"type":"compaction","id":"cmp_1","encrypted_content":"only-history"},{"type":"reasoning","id":"rs_bad","encrypted_content":"invalid","summary":[]},{"type":"message","role":"assistant","id":"msg_1","phase":"final_answer","content":[{"type":"output_text","text":"done"}]},{"type":"reasoning","id":"rs_keep","encrypted_content":"keep-me","summary":[{"type":"summary_text","text":"other"}]}]}`, "invalid_encrypted_content", "The encrypted content for item rs_bad could not be verified. Reason: Encrypted content could not be decrypted or parsed.", "", 3, "id", 1},
		{"orphan reasoning", codexReasoningRequest, "invalid_request_error", "Item 'rs_empty' of type 'reasoning' was provided without its required following item.", "", 5, "", 0},
		{"missing predecessor", codexReasoningRequest, "invalid_request_error", "Item 'msg_1' of type 'message' was provided without its required 'reasoning' item: 'rs_missing'.", "", 6, "id", 5},
		{"tool missing predecessor", codexReasoningRequest, "invalid_request_error", "Item 'fc_1' of type 'function_call' was provided without its required 'reasoning' item: 'rs_missing'.", "", 6, "id", 3},
		{"missing stored reasoning", codexReasoningRequest, "item_not_found", "Item with id 'rs_bad' not found. Items are not persisted when store is set to false.", "", 6, "id", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repaired, changed := rectifyOpenAIReasoning([]byte(tt.body), reasoningErrorJSON(tt.code, tt.message, tt.param))
			if !changed {
				t.Fatal("no repair")
			}
			items := reasoningInput(t, repaired)
			if len(items) != tt.wantItems || (tt.absentField != "" && len(items[tt.item][tt.absentField]) > 0) {
				t.Fatalf("unexpected repair %s", repaired)
			}
			if tt.name == "tool missing predecessor" && reasoningString(items[3]["call_id"]) != "call_1" {
				t.Fatal("lost tool pairing")
			}
			if tt.name == "missing predecessor" && reasoningString(items[5]["phase"]) != "final_answer" {
				t.Fatal("lost assistant phase")
			}
			if tt.name == "named ciphertext" {
				if reasoningString(items[0]["encrypted_content"]) != "only-history" || len(items[2]["encrypted_content"]) > 0 || len(items[2]["id"]) > 0 || !bytes.Contains(items[2]["summary"], []byte("other")) || reasoningString(items[1]["phase"]) != "final_answer" || !bytes.Contains(items[1]["content"], []byte("done")) {
					t.Fatalf("named repair changed unrelated items: %s", repaired)
				}
			}
			if tt.name == "null content" && reasoningString(items[1]["encrypted_content"]) != "invalid" {
				t.Fatal("field repair discarded ciphertext")
			}
			if tt.name == "indexed ciphertext" && reasoningString(items[2]["encrypted_content"]) != "invalid2" {
				t.Fatal("repaired an unreported item")
			}
		})
	}
}

func TestOpenAIReasoningRecoveryRejectsUnrelatedOrUnrecoverableErrors(t *testing.T) {
	compacted := `{"input":[{"type":"compaction","id":"cmp_1","encrypted_content":"only-history"},{"type":"reasoning","id":"rs_1","encrypted_content":"bad","summary":[]},{"role":"user","content":"continue"}]}`
	for _, tt := range []struct{ name, body, code, message, param string }{
		{"ordinary parameter", codexReasoningRequest, "invalid_request_error", "Unsupported reasoning.effort value", "reasoning.effort"},
		{"auth", codexReasoningRequest, "invalid_api_key", "invalid_encrypted_content", ""},
		{"untrusted echo", codexReasoningRequest, "rate_limit_exceeded", "Item 'rs_empty' of type 'reasoning' was provided without its required following item.", ""},
		{"not a reasoning field", codexReasoningRequest, "unknown_parameter", "Unknown parameter: 'input[3].status'", "input[3].status"},
		{"conflicting field", codexReasoningRequest, "invalid_type", "Invalid type for 'input[1].content': got null", "input[0].content"},
		{"conflicting reasoning indexes", `{"input":[{"type":"reasoning","content":null},{"type":"reasoning","content":null}]}`, "invalid_type", "Invalid type for 'input[1].content': got null", "input[0].content"},
		{"dependent reference", `{"input":[{"type":"reasoning","id":"rs_bad","encrypted_content":"bad","summary":[]},{"type":"item_reference","id":"rs_bad"},{"role":"user","content":"continue"}]}`, "invalid_encrypted_content", "invalid", ""},
		{"bad index", codexReasoningRequest, "invalid_encrypted_content", "invalid", "input[999].encrypted_content"},
		{"unrelated ciphertext", codexReasoningRequest, "invalid_encrypted_content", "invalid", "input[3].encrypted_content"},
		{"compaction ambiguous", compacted, "invalid_encrypted_content", "invalid", ""},
		{"compaction named", compacted, "invalid_encrypted_content", "invalid", "input[0].encrypted_content"},
		{"named compaction id", `{"input":[{"type":"compaction","id":"rs_bad","encrypted_content":"only-history"},{"type":"reasoning","id":"rs_ok","encrypted_content":"bad","summary":[]}]}`, "invalid_encrypted_content", "The encrypted content for item rs_bad could not be verified.", ""},
		{"named unknown item", codexReasoningRequest, "invalid_encrypted_content", "The encrypted content for item rs_unknown could not be verified.", ""},
		{"no ciphertext", `{"input":[{"role":"user","content":"hello"}]}`, "invalid_encrypted_content", "invalid", ""},
		{"empty resulting request", `{"input":[{"type":"reasoning","encrypted_content":"bad","summary":[]}]}`, "invalid_encrypted_content", "invalid", ""},
		{"function output ciphertext", encryptedFunctionOutputRequest, "invalid_encrypted_content", "Encrypted function output content could not be decrypted or decoded.", ""},
		{"opaque item reference", `{"input":[{"type":"item_reference","id":"rs_bad"},{"role":"user","content":"continue"}]}`, "item_not_found", "Item with id 'rs_bad' not found", ""},
		{"server-only reasoning", `{"previous_response_id":"resp_1","input":"continue"}`, "item_not_found", "Item with id 'rs_bad' not found", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if repaired, changed := rectifyOpenAIReasoning([]byte(tt.body), reasoningErrorJSON(tt.code, tt.message, tt.param)); changed {
				t.Fatalf("unsafe repair %s", repaired)
			}
		})
	}
	if repaired, changed := rectifyOpenAIReasoning([]byte(compacted), reasoningErrorJSON("invalid_encrypted_content", "invalid", "input[1].encrypted_content")); !changed || !bytes.Contains(repaired, []byte("only-history")) {
		t.Fatalf("lost unaffected compaction %s", repaired)
	}
	wrapped := reasoningErrorJSON("", invalidReasoningCipher+" (traceid: example)", "")
	if _, changed := rectifyOpenAIReasoning([]byte(codexReasoningRequest), wrapped); !changed {
		t.Fatal("wrapped error was not repaired")
	}
	sse := []byte("event: error\ndata: " + string(reasoningErrorJSON("invalid_encrypted_content", "The encrypted content for item rs_bad could not be verified.", "")) + "\n\n")
	if _, changed := rectifyOpenAIReasoning([]byte(codexReasoningRequest), sse); !changed {
		t.Fatal("sse error was not repaired")
	}
}

func TestOpenAIReasoningCipherRepairKeepsPlainContentWithNullSummary(t *testing.T) {
	body := []byte(`{"input":[{"type":"reasoning","id":"rs_bad","encrypted_content":"bad","summary":null,"content":[{"type":"reasoning_text","text":"visible"}]},{"role":"user","content":"continue"}]}`)
	repaired, changed := rectifyOpenAIReasoning(body, []byte(invalidReasoningCipher))
	if !changed {
		t.Fatal("no repair")
	}
	items := reasoningInput(t, repaired)
	if string(items[0]["summary"]) != "[]" || !bytes.Contains(items[0]["content"], []byte("visible")) {
		t.Fatalf("invalid repaired summary or missing content: %s", repaired)
	}
}

func TestOpenAIReasoningRepairRebuildsEachTargetFromOriginal(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 0
	policy.MaxRetries = 2
	trips := 0
	handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: openAIRecoveryCandidates(policy, 6, contract.ProtocolOpenAIResponses, "gpt-5.3-codex", "gpt-5")}, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		trips++
		data, _ := io.ReadAll(request.Body)
		items := reasoningInput(t, data)
		switch trips {
		case 1:
			return jsonResponse(400, invalidReasoningCipher), nil
		case 2:
			if request.URL.Host != "A.example" || len(items[1]["encrypted_content"]) > 0 {
				t.Fatalf("A was not repaired: %s", data)
			}
			return jsonResponse(503, `{"error":{"message":"unavailable"}}`), nil
		case 3:
			if request.URL.Host != "B.example" || reasoningString(items[1]["encrypted_content"]) != "invalid" || string(items[1]["content"]) != "null" {
				t.Fatalf("B inherited A's repair: %s", data)
			}
			return jsonResponse(400, string(reasoningErrorJSON("invalid_type", "Invalid type for 'input[1].content': expected array, got null", "input[1].content"))), nil
		case 4:
			if request.URL.Host != "B.example" || reasoningString(items[1]["encrypted_content"]) != "invalid" || len(items[1]["content"]) > 0 {
				t.Fatalf("B's own repair was not applied: %s", data)
			}
			return jsonResponse(200, `{"id":"resp_b","output":[]}`), nil
		default:
			t.Fatal("extra attempt")
			return nil, io.ErrUnexpectedEOF
		}
	}))})
	request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(codexReasoningRequest))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if trips != 4 || response.Code != 200 {
		t.Fatalf("attempts=%d status=%d body=%s", trips, response.Code, response.Body.String())
	}
}

func openAIRecoveryCandidates(policy contract.FailurePolicy, limit int, protocol contract.ProtocolID, models ...string) []endpoint.Resolved {
	candidates := recoveryCandidates(len(models), policy, contract.FailoverPolicy{Enabled: true, Strategy: contract.FailoverFirst, MaxAttempts: limit})
	for i, model := range models {
		candidates[i].Endpoint.Capabilities = []contract.Capability{{Protocol: protocol, Mode: contract.CapabilityModeNative, Streaming: protocol != contract.ProtocolOpenAIResponsesCompact}}
		candidates[i].UpstreamModel = model
		candidates[i].UpstreamProtocol = protocol
	}
	return candidates
}

func TestOpenAIReasoningRecoveryEndToEnd(t *testing.T) {
	for _, tt := range []struct {
		name                                        string
		disabled, stream, compact, stillFails, stop bool
		retries, limit, want                        int
		model                                       string
	}{
		{name: "Codex", retries: 1, limit: 6, want: 2, model: "gpt-5.3-codex"},
		{name: "stream", stream: true, retries: 1, limit: 6, want: 2, model: "gpt-5.3-codex"},
		{name: "compact", compact: true, retries: 1, limit: 6, want: 2, model: "gpt-5.3-codex"},
		{name: "o-series", retries: 1, limit: 6, want: 2, model: "o3"},
		{name: "disabled", disabled: true, retries: 1, limit: 6, want: 1, model: "gpt-5.3-codex"},
		{name: "no retry budget", retries: 0, limit: 6, want: 2, model: "gpt-5.3-codex"},
		{name: "total limit", retries: 1, limit: 1, want: 1, model: "gpt-5.3-codex"},
		{name: "explicit stop", stop: true, retries: 1, limit: 6, want: 2, model: "gpt-5.3-codex"},
		{name: "only one repair", stillFails: true, retries: 5, limit: 6, want: 2, model: "gpt-5.3-codex"},
		{name: "unknown model", retries: 1, limit: 6, want: 1, model: "other"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := contract.DefaultFailurePolicy()
			policy.InitialDelayMS = 0
			policy.MaxRetries = tt.retries
			claudeOff := false
			policy.ThinkingSignatureRecovery = &claudeOff
			if tt.disabled {
				policy.OpenAIReasoningRecovery = &claudeOff
			}
			if tt.stop {
				policy.HTTPStatus["400"] = contract.FailureStop
			}
			protocol, path := contract.ProtocolOpenAIResponses, "/v1/responses"
			if tt.compact {
				protocol, path = contract.ProtocolOpenAIResponsesCompact, "/v1/responses/compact"
			}
			var sent [][]byte
			store := &memoryRequestRecordStore{}
			handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: openAIRecoveryCandidates(policy, tt.limit, protocol, tt.model, "gpt-5")}, RequestRecords: store, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				data, _ := io.ReadAll(request.Body)
				sent = append(sent, data)
				if request.URL.Host != "A.example" || request.URL.Path != path || request.ContentLength != int64(len(data)) {
					t.Fatalf("wrong repair target/request %s", request.URL)
				}
				if len(sent) == 1 || tt.stillFails {
					response := jsonResponse(400, invalidReasoningCipher)
					response.Header.Set("X-Failed-Attempt", "yes")
					return response, nil
				}
				if tt.stream {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"output\":[]}}\n\n"))}, nil
				}
				return jsonResponse(200, `{"id":"resp_ok","object":"response","output":[]}`), nil
			}))})
			input := codexReasoningRequest
			if tt.stream {
				input = strings.Replace(input, `"store":false`, `"store":false,"stream":true`, 1)
			}
			request := httptest.NewRequest("POST", path, strings.NewReader(input))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if len(sent) != tt.want {
				t.Fatalf("attempts=%d expected=%d status=%d body=%s", len(sent), tt.want, response.Code, response.Body.String())
			}
			if !bytes.Contains(sent[0], []byte(`"encrypted_content":"invalid"`)) {
				t.Fatal("preemptive repair")
			}
			if len(sent) > 1 && (bytes.Contains(sent[1], []byte(`"encrypted_content":"invalid"`)) || !bytes.Contains(sent[1], []byte(`"call_id":"call_1"`))) {
				t.Fatalf("bad repair %s", sent[1])
			}
			if tt.want == 1 || tt.stillFails {
				if response.Code != 400 || response.Body.String() != invalidReasoningCipher {
					t.Fatalf("lost error %d %s", response.Code, response.Body.String())
				}
			} else if response.Code != 200 || response.Header().Get("X-Failed-Attempt") != "" {
				t.Fatalf("intermediate error leaked %d %s", response.Code, response.Body.String())
			}
			children := 0
			for _, record := range store.records {
				if record.ParentRequestID != nil {
					children++
					if record.Recovery == nil || record.Recovery.Reason != openAIReasoningRecoveryReason {
						t.Fatalf("missing repair reason %+v", record.Recovery)
					}
				}
			}
			if children != tt.want-1 {
				t.Fatalf("children=%d", children)
			}
		})
	}
}

func TestOpenAIReasoningRepairKeepsContinuationAffinity(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 0
	candidates := openAIRecoveryCandidates(policy, 6, contract.ProtocolOpenAIResponses, "gpt-5.3-codex")
	trips := 0
	handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: candidates}, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		trips++
		data, _ := io.ReadAll(request.Body)
		if !bytes.Contains(data, []byte(`"previous_response_id":"resp_previous"`)) || !bytes.Contains(data, []byte(`"call_id":"call_1"`)) {
			t.Fatalf("lost continuation %s", data)
		}
		if trips == 1 {
			return jsonResponse(400, invalidReasoningCipher), nil
		}
		return jsonResponse(200, `{"id":"resp_repaired","output":[]}`), nil
	}))})
	// Seed the exact continuation binding a previous response would have left.
	handler.affinities.entries = map[affinityKey]affinityEntry{{responseID: "resp_previous"}: {
		binding: storage.ResponseAffinity{
			ServiceID:        candidates[0].CanonicalService().ID,
			UpstreamModel:    "gpt-5.3-codex",
			UpstreamProtocol: contract.ProtocolOpenAIResponses,
			PlanType:         contract.PlanTypeNative,
		},
		at: time.Now(),
	}}
	input := strings.Replace(codexReasoningRequest, `"store":false`, `"store":false,"previous_response_id":"resp_previous"`, 1)
	request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(input))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if trips != 2 || response.Code != 200 {
		t.Fatalf("trips=%d status=%d body=%s", trips, response.Code, response.Body.String())
	}
	bound, err := handler.bindResponseAffinity(context.Background(), Request{Protocol: contract.ProtocolOpenAIResponses, Model: "public", PreviousResponseID: "resp_repaired"}, candidates)
	if err != nil || len(bound) != 1 || bound[0].Failover.Enabled {
		t.Fatalf("lost repaired response affinity %v %+v", err, bound)
	}
}

func TestOpenAIReasoningRecoveryRepairsNamedCipherOnStreaming400(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 0
	input := `{"model":"public","store":false,"stream":true,"input":[{"type":"message","role":"user","content":"hello"},{"type":"reasoning","id":"rs_bad","encrypted_content":"invalid","content":null,"summary":[]},{"type":"message","role":"assistant","id":"msg_1","phase":"final_answer","content":[{"type":"output_text","text":"done"}]},{"type":"reasoning","id":"rs_keep","encrypted_content":"keep-me","summary":[{"type":"summary_text","text":"other"}]}]}`
	failure := `{"error":{"message":"The encrypted content for item rs_bad could not be verified. Reason: Encrypted content could not be decrypted or parsed.","type":"invalid_request_error","param":"","code":"invalid_encrypted_content"}}`
	trips := 0
	store := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: openAIRecoveryCandidates(policy, 6, contract.ProtocolOpenAIResponses, "gpt-6-astra")}, RequestRecords: store, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(request.Body)
		trips++
		if trips == 1 {
			return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(failure))}, nil
		}
		items := reasoningInput(t, data)
		if len(items) != 3 || reasoningString(items[1]["id"]) != "" || len(items[2]["encrypted_content"]) > 0 || len(items[2]["id"]) > 0 || !bytes.Contains(items[2]["summary"], []byte("other")) || bytes.Contains(data, []byte("rs_bad")) {
			t.Fatalf("bad named streaming repair: %s", data)
		}
		return jsonResponse(200, `{"id":"resp_ok","output":[]}`), nil
	}))})
	request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(input))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if trips != 2 || response.Code != 200 {
		t.Fatalf("trips=%d status=%d body=%s", trips, response.Code, response.Body.String())
	}
	children := 0
	for _, record := range store.records {
		if record.ParentRequestID != nil {
			children++
			if record.Recovery == nil || record.Recovery.Reason != openAIReasoningRecoveryReason {
				t.Fatalf("missing repair reason %+v", record.Recovery)
			}
		}
	}
	if children != 1 {
		t.Fatalf("children=%d", children)
	}
}

func TestOpenAIReasoningRecoveryDoesNotRetryCommittedSSE(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 0
	trips := 0
	events := fmt.Sprintf("event: error\ndata: %s\n\n", invalidReasoningCipher)
	handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: openAIRecoveryCandidates(policy, 6, contract.ProtocolOpenAIResponses, "gpt-5.3-codex")}, Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
		trips++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(events))}, nil
	}))})
	input := strings.Replace(codexReasoningRequest, `"store":false`, `"store":false,"stream":true`, 1)
	request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(input))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if trips != 1 || response.Body.String() != events {
		t.Fatalf("retried or changed SSE: %d %s", trips, response.Body.String())
	}
}

func TestOpenAIFunctionOutputCipherRepairIsOptIn(t *testing.T) {
	if repaired, changed := rectifyOpenAIReasoning([]byte(encryptedFunctionOutputRequest), []byte(invalidFunctionOutputCipher)); changed {
		t.Fatalf("reasoning-only repair touched function output: %s", repaired)
	}
	repaired, reason, ok := rectifyOpenAIRequest([]byte(encryptedFunctionOutputRequest), []byte(invalidFunctionOutputCipher), openAIRepairScope{functionOutput: true})
	if !ok || reason != openAIFunctionOutputRecoveryReason {
		t.Fatalf("missing function-output repair %q %s", reason, repaired)
	}
	items := reasoningInput(t, repaired)
	if len(items) != 6 || reasoningString(items[1]["encrypted_content"]) != "invalid" || reasoningString(items[1]["id"]) != "rs_bad" {
		t.Fatalf("stripped reasoning: %s", repaired)
	}
	if len(items[3]["id"]) > 0 || !bytes.Contains(items[3]["output"], []byte(`"text":"visible"`)) || bytes.Contains(items[3]["output"], []byte("gAAAAA")) {
		t.Fatalf("kept ciphertext or lost plaintext: %s", items[3]["output"])
	}
	if len(items[4]["id"]) > 0 || string(items[4]["output"]) != `""` || reasoningString(items[4]["call_id"]) != "call_2" {
		t.Fatalf("empty encrypted output: %s", items[4]["output"])
	}
	if len(items[5]["id"]) > 0 || string(items[5]["output"]) != `""` || reasoningString(items[5]["call_id"]) != "call_3" {
		t.Fatalf("custom tool output: %s", items[5]["output"])
	}
	if _, _, ok := rectifyOpenAIRequest(repaired, []byte(invalidFunctionOutputCipher), openAIRepairScope{functionOutput: true}); ok {
		t.Fatal("repaired twice")
	}
}

func TestOpenAINamedReasoningRepairClearsFunctionOutputWhenEnabled(t *testing.T) {
	repaired, reason, ok := rectifyOpenAIRequest([]byte(encryptedFunctionOutputRequest), []byte(namedReasoningCipher), openAIRepairScope{reasoning: true, functionOutput: true})
	if !ok || reason != openAIReasoningRecoveryReason {
		t.Fatalf("expected reasoning reason, got %q %s", reason, repaired)
	}
	items := reasoningInput(t, repaired)
	if len(items) != 6 || len(items[1]["encrypted_content"]) > 0 || reasoningString(items[1]["id"]) != "" || !bytes.Contains(items[1]["summary"], []byte("look up")) {
		t.Fatalf("bad reasoning repair: %s", repaired)
	}
	if bytes.Contains(repaired, []byte("gAAAAA")) || bytes.Contains(repaired, []byte("gBBBBB")) || bytes.Contains(repaired, []byte("gCCCCC")) {
		t.Fatalf("left function-output ciphertext: %s", repaired)
	}
	if !bytes.Contains(items[3]["output"], []byte(`"text":"visible"`)) || reasoningString(items[3]["call_id"]) != "call_1" {
		t.Fatalf("lost visible tool result: %s", items[3]["output"])
	}
	repaired, _, ok = rectifyOpenAIRequest([]byte(encryptedFunctionOutputRequest), []byte(namedReasoningCipher), openAIRepairScope{reasoning: true})
	if !ok || !bytes.Contains(repaired, []byte("gAAAAA")) {
		t.Fatalf("reasoning-only repair should keep function output: %s", repaired)
	}
}

func TestOpenAIFunctionOutputRecoveryEndToEnd(t *testing.T) {
	for _, tt := range []struct {
		name     string
		enabled  bool
		want     int
		firstErr string
		reason   string
	}{
		{name: "disabled", want: 1, firstErr: invalidFunctionOutputCipher},
		{name: "function output", enabled: true, want: 2, firstErr: invalidFunctionOutputCipher, reason: openAIFunctionOutputRecoveryReason},
		{name: "same attempt as reasoning", enabled: true, want: 2, firstErr: namedReasoningCipher, reason: openAIReasoningRecoveryReason},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := contract.DefaultFailurePolicy()
			policy.InitialDelayMS = 0
			off := false
			policy.ThinkingSignatureRecovery = &off
			if !tt.enabled {
				policy.OpenAIReasoningRecovery = &off
			} else {
				on := true
				policy.OpenAIFunctionOutputRecovery = &on
			}
			var sent [][]byte
			store := &memoryRequestRecordStore{}
			handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: openAIRecoveryCandidates(policy, 6, contract.ProtocolOpenAIResponses, "gpt-6-astra")}, RequestRecords: store, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				data, _ := io.ReadAll(request.Body)
				sent = append(sent, data)
				if len(sent) == 1 {
					return jsonResponse(400, tt.firstErr), nil
				}
				if bytes.Contains(data, []byte("gAAAAA")) || bytes.Contains(data, []byte("gBBBBB")) || bytes.Contains(data, []byte("gCCCCC")) {
					t.Fatalf("retry still had function-output ciphertext: %s", data)
				}
				if tt.reason == openAIReasoningRecoveryReason && bytes.Contains(data, []byte(`"encrypted_content":"invalid"`)) {
					t.Fatalf("retry still had reasoning ciphertext: %s", data)
				}
				if !bytes.Contains(data, []byte(`"call_id":"call_1"`)) || !bytes.Contains(data, []byte("visible")) {
					t.Fatalf("lost tool pair: %s", data)
				}
				return jsonResponse(200, `{"id":"resp_ok","output":[]}`), nil
			}))})
			request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(encryptedFunctionOutputRequest))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if len(sent) != tt.want {
				t.Fatalf("attempts=%d expected=%d status=%d body=%s", len(sent), tt.want, response.Code, response.Body.String())
			}
			if tt.want == 1 {
				if response.Code != 400 || response.Body.String() != tt.firstErr {
					t.Fatalf("lost error %d %s", response.Code, response.Body.String())
				}
				return
			}
			if response.Code != 200 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			found := false
			for _, record := range store.records {
				if record.ParentRequestID != nil {
					found = true
					if record.Recovery == nil || record.Recovery.Reason != tt.reason {
						t.Fatalf("reason=%+v want %s", record.Recovery, tt.reason)
					}
				}
			}
			if !found {
				t.Fatal("missing child repair record")
			}
		})
	}
}
