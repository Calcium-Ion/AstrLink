package ingress

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestAutoModelClassifiesLastUserTextThenResolvesCategory(t *testing.T) {
	var resolved endpoint.ResolveRequest
	var classifiedText string
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			resolved = request
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		Classifier: ClassifierFunc(func(_ context.Context, text string) autoclassifier.Outcome {
			classifiedText = text
			return autoclassifier.Outcome{Category: "coding", Logits: []float32{0, 0, 1, 0}}
		}),
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "fix the rust compile error") {
				t.Fatalf("forwarded body lost user text: %s", body)
			}
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte(`{"id":"resp_auto"}`))
			return nil
		}),
	})

	body := `{"model":"astrlink/auto","input":[{"role":"user","content":[{"type":"input_text","text":"fix the rust compile error"}]}]}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if classifiedText != "fix the rust compile error" {
		t.Fatalf("classified text = %q", classifiedText)
	}
	if resolved.Model != contract.AstrLinkAutoModelID || resolved.Category != "coding" {
		t.Fatalf("resolve request = %#v", resolved)
	}
}

func TestAutoModelFailOpenWhenClassifierTimesOut(t *testing.T) {
	var resolved endpoint.ResolveRequest
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			resolved = request
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		Classifier: ClassifierFunc(func(context.Context, string) autoclassifier.Outcome {
			return autoclassifier.Outcome{FallbackReason: autoclassifier.FallbackTimeout}
		}),
		Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
			return nil
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"astrlink/auto","input":"hello"}`),
	))
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if resolved.Category != "" {
		t.Fatalf("timeout must fail-open with empty category, got %#v", resolved)
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

func TestGeminiAutoPathClassifiesContents(t *testing.T) {
	var resolved endpoint.ResolveRequest
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			resolved = request
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolGoogleGenerateContent, false)}, nil
		}),
		Classifier: ClassifierFunc(func(_ context.Context, text string) autoclassifier.Outcome {
			if text != "design the service boundary" {
				t.Fatalf("classified text = %q", text)
			}
			return autoclassifier.Outcome{Category: "architect", Logits: []float32{0, 0, 0, 1}}
		}),
		Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
			return nil
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/astrlink/auto:generateContent",
		strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"design the service boundary"}]}]}`),
	))
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if resolved.Model != contract.AstrLinkAutoModelID || resolved.Category != "architect" {
		t.Fatalf("resolve request = %#v", resolved)
	}
}
