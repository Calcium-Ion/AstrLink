package ingress

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAutoModelIsRetiredBeforeClassificationOrForwarding(t *testing.T) {
	for _, tc := range []struct{ path, body string }{{"/v1/responses", `{"model":"astrlink/auto","input":"hello"}`}, {"/v1beta/models/astrlink/auto:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`}} {
		handler := NewWithDependencies(Dependencies{
			Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
				t.Fatal("retired model reached resolver")
				return endpoint.Resolved{}, nil
			}),
			Classifier: ClassifierFunc(func(context.Context, string) autoclassifier.Outcome {
				t.Fatal("retired model reached classifier")
				return autoclassifier.Outcome{}
			}),
			Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
				t.Fatal("retired model reached upstream")
				return nil
			}),
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
		if response.Code != http.StatusGone || !strings.Contains(response.Body.String(), "routing_feature_retired") {
			t.Fatalf("%s: %d %s", tc.path, response.Code, response.Body.String())
		}
	}
}

func TestExplicitModelDoesNotCallClassifier(t *testing.T) {
	called := false
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		Classifier: ClassifierFunc(func(context.Context, string) autoclassifier.Outcome {
			called = true
			return autoclassifier.Outcome{Category: "coding"}
		}),
		Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
			return nil
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5","input":"fix the rust compile error"}`),
	))
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if called {
		t.Fatal("explicit model must bypass the classifier")
	}
}
