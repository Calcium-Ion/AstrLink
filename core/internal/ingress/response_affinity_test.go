package ingress

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseAffinityFollowsActualBackupAndIsolatesPrincipals(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 0
	policy.MaxRetries = 0
	candidates := recoveryCandidates(2, policy, contract.DefaultFailoverPolicy())
	for i := range candidates {
		candidates[i].Endpoint.Capabilities = []contract.Capability{{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative}}
		candidates[i].UpstreamModel = []string{"model-a", "model-b"}[i]
		candidates[i].UpstreamProtocol = contract.ProtocolOpenAIResponses
	}
	var attempts []string
	handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: candidates}, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts = append(attempts, request.URL.Host)
		if request.URL.Host == "A.example" {
			return jsonResponse(503, `{"error":{"message":"unavailable"}}`), nil
		}
		return jsonResponse(200, `{"id":"resp_actual_b","model":"model-b","output":[]}`), nil
	}))})
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"public","input":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != 200 || strings.Join(attempts, ",") != "A.example,B.example" {
		t.Fatalf("%d %v %s", response.Code, attempts, response.Body.String())
	}
	response = httptest.NewRecorder()
	request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"public","input":"continue","previous_response_id":"resp_actual_b"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != 200 || strings.Join(attempts, ",") != "A.example,B.example,B.example" {
		t.Fatalf("continuation %d %v", response.Code, attempts)
	}
	ctx := context.WithValue(context.Background(), accessTokenIDContextKey{}, contract.AccessTokenID("access_other"))
	continuation := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "public", PreviousResponseID: "resp_actual_b"}
	if _, err := handler.bindResponseAffinity(ctx, continuation, candidates); err == nil {
		t.Fatal("cross-principal affinity accepted")
	}
	if _, err := handler.bindResponseAffinity(context.Background(), continuation, candidates[:1]); err == nil {
		t.Fatal("unavailable target silently switched")
	}
	continuation.PreviousResponseID = "unknown"
	if _, err := handler.bindResponseAffinity(context.Background(), continuation, candidates[:1]); err == nil {
		t.Fatal("unknown previous response accepted")
	}
}
