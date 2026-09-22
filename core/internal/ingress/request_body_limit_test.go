package ingress

import (
	"context"
	"encoding/json"
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

func TestRequestBodyLimitDefaultAndConfigured(t *testing.T) {
	for _, route := range []struct {
		path     string
		protocol contract.ProtocolID
	}{
		{"/v1/responses", contract.ProtocolOpenAIResponses},
		{"/v1/messages", contract.ProtocolAnthropicMessages},
		{"/v1beta/models/gemini:generateContent", contract.ProtocolGoogleGenerateContent},
	} {
		for _, tc := range []struct {
			name  string
			limit uint32
			size  int
			want  int
		}{
			{"default unlimited above old limit", 0, 9 << 20, http.StatusOK},
			{"custom limit above old limit", 16, 9 << 20, http.StatusOK},
			{"exact limit", 1, 1 << 20, http.StatusOK},
			{"one byte over", 1, (1 << 20) + 1, http.StatusRequestEntityTooLarge},
		} {
			t.Run(route.path+"/"+tc.name, func(t *testing.T) {
				const prefix = `{"model":"test-model","padding":"`
				body := prefix + strings.Repeat("x", tc.size-len(prefix)-2) + `"}`
				forwarded := false
				handler := NewWithDependencies(Dependencies{
					MaxRequestBodyMiB: tc.limit,
					Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
						return endpoint.Resolved{Endpoint: validEndpoint(route.protocol, false)}, nil
					}),
					Forwarder: forwarderFunc(func(w http.ResponseWriter, request *http.Request, _ transport.Target) error {
						forwarded = true
						got, err := io.ReadAll(request.Body)
						if err != nil || string(got) != body {
							t.Fatalf("body not preserved: length=%d error=%v", len(got), err)
						}
						w.WriteHeader(http.StatusOK)
						return nil
					}),
				})
				// No Content-Length and no GetBody: exercise actual reads and replay, as with chunked requests.
				request := httptest.NewRequest(http.MethodPost, route.path, io.NopCloser(strings.NewReader(body)))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != tc.want || forwarded != (tc.want == http.StatusOK) {
					t.Fatalf("status=%d forwarded=%v response=%s", response.Code, forwarded, response.Body.String())
				}
				if tc.want == http.StatusRequestEntityTooLarge {
					assertInferenceError(t, response, tc.want, "request_too_large")
				}
			})
		}
	}
}

func TestUnlimitedLargeRequestWithPrivacyRedaction(t *testing.T) {
	padding := strings.Repeat("x", 8<<20)
	body := `{"model":"test-model","input":"alice@example.com","padding":"` + padding + `"}`
	filter := testPrivacyEngine(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact}, nil)
	forwarded := false
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		PrivacyFilter: filter,
		Forwarder: forwarderFunc(func(w http.ResponseWriter, request *http.Request, _ transport.Target) error {
			forwarded = true
			got, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(got, &fields) != nil || !emailPlaceholderPattern.Match(fields["input"]) || strings.Contains(string(fields["input"]), "alice@example.com") {
				t.Fatal("large request did not retain privacy redaction")
			}
			var actualPadding string
			if json.Unmarshal(fields["padding"], &actualPadding) != nil || actualPadding != padding {
				t.Fatal("large padding changed")
			}
			w.WriteHeader(http.StatusOK)
			return nil
		}),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !forwarded {
		t.Fatalf("status=%d response=%s", response.Code, response.Body.String())
	}
}

func TestRedactedRequestExceedingBodyLimitIsReportedAsTooLarge(t *testing.T) {
	const prefix = `{"model":"test-model","input":"alice@example.com","padding":"`
	// The original fits exactly; its longer privacy placeholder exceeds the limit.
	body := prefix + strings.Repeat("x", (1<<20)-len(prefix)-2) + `"}`
	filter := testPrivacyEngine(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact}, nil)
	records := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{
		MaxRequestBodyMiB: 1,
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		PrivacyFilter:  filter,
		RequestRecords: records,
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			t.Fatal("oversized redacted request must not load credentials")
			return nil, nil
		}),
		Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
			t.Fatal("oversized redacted request must not forward")
			return nil
		}),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	envelope := assertInferenceError(t, response, http.StatusRequestEntityTooLarge, "request_too_large")
	if envelope.Error.Retryable {
		t.Fatal("oversized request must not be retried")
	}
	assertPrivacyErrorRecord(t, records, response.Code, contract.RequestStatusFailed, "gateway", "request_too_large", envelope.Error)
}

func TestUnlimitedLargeRequestReplayAndRecovery(t *testing.T) {
	body := strings.Replace(signedThinkingRequest, `"hello"`, `"`+strings.Repeat("x", 8<<20)+`"`, 1)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", io.NopCloser(strings.NewReader(body)))
	source, err := captureRequestBody(request, true)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if !source.Replayable() {
		t.Fatal("large request cannot be retried")
	}
	for range 2 {
		replay, ok, err := source.Next(context.Background())
		if err != nil || !ok {
			t.Fatalf("next request: %v, %v", ok, err)
		}
		got, err := io.ReadAll(replay.Body)
		replay.Body.Close()
		if err != nil || string(got) != body {
			t.Fatal("replayed large request changed")
		}
	}
	repaired := prepareReasoningRecovery(source, rectifyThinkingSignature)
	if len(repaired) <= 8<<20 || strings.Contains(string(repaired), `"signature":"invalid"`) {
		t.Fatal("large request recovery was skipped")
	}
}

func TestClaudeSubscriptionPreparesLargeRequest(t *testing.T) {
	body := `{"model":"test-model","messages":[],"padding":"` + strings.Repeat("x", 8<<20) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	if err := prepareClaudeSubscriptionRequest(request); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(request.Body)
	if err != nil || len(got) <= 8<<20 || !strings.Contains(string(got), claudeCodeBanner) {
		t.Fatal("large subscription request was not prepared")
	}
}
