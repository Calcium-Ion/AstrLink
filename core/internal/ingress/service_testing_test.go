package ingress

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestServiceTestDoesNotResendRepairableRequests(t *testing.T) {
	for _, test := range []struct {
		name, body, failure, path, model string
		protocol                         contract.ProtocolID
	}{
		{"thinking", signedThinkingRequest, signatureFailure, "/v1/messages", "claude-sonnet-4-6", contract.ProtocolAnthropicMessages},
		{"reasoning", codexReasoningRequest, invalidReasoningCipher, "/v1/responses", "gpt-5.3-codex", contract.ProtocolOpenAIResponses},
		{"function output", encryptedFunctionOutputRequest, invalidFunctionOutputCipher, "/v1/responses", "gpt-5.3-codex", contract.ProtocolOpenAIResponses},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := contract.ServiceFromEndpoint(validEndpoint(test.protocol, false))
			policy := contract.DefaultFailurePolicy()
			enabled := true
			policy.OpenAIFunctionOutputRecovery = &enabled
			service.FailurePolicy = &policy
			service.Enabled = false
			body := strings.Replace(test.body, `"model":"public"`, `"model":"`+test.model+`"`, 1)
			calls := 0
			records := &memoryRequestRecordStore{}
			handler := NewWithDependencies(Dependencies{
				RequestRecords: records,
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					calls++
					sent, _ := io.ReadAll(request.Body)
					if string(sent) != body {
						t.Error("test request was repaired or rewritten")
					}
					return jsonResponse(400, test.failure), nil
				})),
			})
			request := httptest.NewRequest("POST", test.path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			err := handler.ServeServiceTest(response, request, service, contract.ServiceTestRequest{Protocol: test.protocol, Model: test.model}, "", ServiceTestObserver{})
			if err != nil || calls != 1 || response.Code != 400 || response.Body.String() != test.failure {
				t.Fatalf("calls=%d status=%d body=%s err=%v", calls, response.Code, response.Body, err)
			}
			if len(records.records) != 1 || records.records[0].AttemptIndex != 1 || records.records[0].ChildCount != 0 || records.records[0].Status != contract.RequestStatusFailed {
				t.Fatalf("repair records=%+v", records.records)
			}
		})
	}
}
