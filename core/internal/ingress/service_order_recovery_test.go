package ingress

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestFailoverOnlyNeverRevisitsFailedServices(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.MaxRetries = 5
	policy.InitialDelayMS = 0
	var attempts []string
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: recoveryCandidates(3, policy, contract.FailoverPolicy{Enabled: true, Strategy: contract.FailoverOnly, MaxAttempts: 20})},
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts = append(attempts, request.URL.Host)
			return jsonResponse(503, `{"error":{"message":"unavailable"}}`), nil
		})),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","messages":[{"role":"user","content":"hello"}]}`)))
	if response.Code != 503 || !reflect.DeepEqual(attempts, []string{"A.example", "B.example", "C.example"}) {
		t.Fatalf("status=%d attempts=%v body=%s", response.Code, attempts, response.Body.String())
	}
}

func TestFailoverOnlyRepairsOnceBeforeApplyingErrorRules(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.MaxRetries = 0
	policy.HTTPStatus["400"] = contract.FailureFailover
	candidates := thinkingCandidates(policy, 20, "claude-sonnet-4-6", "claude-opus-4-6")
	candidates[0].Failover.Strategy = contract.FailoverOnly
	trips := 0
	var attempts []string
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: candidates},
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			trips++
			attempts = append(attempts, request.URL.Host)
			body, _ := io.ReadAll(request.Body)
			if strings.Contains(string(body), `"signature":"invalid"`) != (trips%2 == 1) {
				t.Fatal("each target must receive the original request, then its own repair")
			}
			return jsonResponse(400, signatureFailure), nil
		})),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(signedThinkingRequest)))
	if !reflect.DeepEqual(attempts, []string{"A.example", "A.example", "B.example", "B.example"}) || response.Code != 400 {
		t.Fatalf("attempts=%v response=%d", attempts, response.Code)
	}
}
