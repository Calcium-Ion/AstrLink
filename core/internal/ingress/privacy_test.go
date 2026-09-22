package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

var emailPlaceholderPattern = regexp.MustCompile(`<PRIVATE_EMAIL_[0-9a-f]{16}>`)

func TestDisabledPrivacyPolicyDoesNotReadOrReplaceOriginalGeminiBody(t *testing.T) {
	const original = " {\n \"contents\":[{\"parts\":[{\"text\":\"alice@example.com\"}]}]\n} "
	tracked := &trackingRequestBody{reader: strings.NewReader(original)}
	filter := testPrivacyEngine(t, privacy.Policy{}, nil)
	upstream := validEndpoint(contract.ProtocolGoogleGenerateContent, false)
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			if request.Model != "gemini-2.5-pro" {
				t.Fatalf("resolved model = %q", request.Model)
			}
			return endpoint.Resolved{Endpoint: upstream}, nil
		}),
		PrivacyFilter: filter,
		Forwarder: forwarderFunc(func(_ http.ResponseWriter, request *http.Request, _ transport.Target) error {
			// Session linking may buffer the body for inspection, but a
			// disabled policy must never rewrite it: the upstream sees the
			// exact original bytes, whitespace included, and no privacy
			// residency buffer replaces the stream.
			if _, rewritten := request.Body.(*metadataPermitBody); rewritten {
				t.Fatalf("disabled policy replaced body with %T", request.Body)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil || string(body) != original {
				t.Fatalf("forwarded body = %q, %v", body, err)
			}
			return nil
		}),
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini-2.5-pro:generateContent",
		nil,
	)
	request.Body = tracked
	request.ContentLength = int64(len(original))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPrivacyNoMatchAndWarnPreserveExactBytesAndWarningStaysLocal(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		action     privacy.Action
		wantHeader bool
	}{
		{
			name:   "no match",
			body:   " {\n \"model\":\"gpt-5\", \"input\":\"ordinary text\"\n} ",
			action: privacy.ActionRedact,
		},
		{
			name:       "warn",
			body:       " {\n \"model\":\"gpt-5\", \"input\":\"alice@example.com\"\n} ",
			action:     privacy.ActionWarn,
			wantHeader: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var warningReports []string
			filter := testPrivacyEngine(t, privacy.Policy{
				Enabled: true, Mode: privacy.ModeRegex, Action: test.action,
			}, nil)
			upstream := validEndpoint(contract.ProtocolOpenAIResponses, false)
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
					return endpoint.Resolved{Endpoint: upstream}, nil
				}),
				PrivacyFilter: filter,
				PolicyWarningReporter: PolicyWarningReporterFunc(
					func(_ contract.ProtocolID, _ contract.ServiceID, summary string) {
						warningReports = append(warningReports, summary)
					},
				),
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					if request.Header.Get(PolicyWarningHeader) != "" {
						t.Fatalf("privacy warning leaked upstream: %#v", request.Header)
					}
					body, err := io.ReadAll(request.Body)
					if err != nil || string(body) != test.body {
						t.Fatalf("forwarded body = %q, %v", body, err)
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
					}, nil
				})),
			})
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(PolicyWarningHeader, "spoofed=999")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK || response.Body.String() != `{"ok":true}` {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
			header := response.Header().Get(PolicyWarningHeader)
			if test.wantHeader && header != "email=1" {
				t.Fatalf("warning header = %q", header)
			}
			if !test.wantHeader && header != "" {
				t.Fatalf("unexpected warning header = %q", header)
			}
			if test.wantHeader {
				if len(warningReports) != 1 || warningReports[0] != "email=1" {
					t.Fatalf("warning reports = %#v", warningReports)
				}
				if strings.Contains(warningReports[0], "alice@example.com") {
					t.Fatalf("warning report leaked plaintext: %q", warningReports[0])
				}
			} else if len(warningReports) != 0 {
				t.Fatalf("unexpected warning reports = %#v", warningReports)
			}
		})
	}
}

func TestPrivacyRedactPreservesUnmodifiedBytesAndUpdatesBodyLength(t *testing.T) {
	const original = " {\n \"z\":1.2300e+04, \"model\":\"model@example.com\", \"messages\":[{\"role\":\"user\",\"content\":\"alice@example.com\",\"a\":null}], \"a\":false\n} "
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, ResponseRestore: true,
	}, nil)
	upstream := validEndpoint(contract.ProtocolOpenAIChat, false)
	forwarded := false
	var upstreamPlaceholder string
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: upstream}, nil
		}),
		PrivacyFilter: filter,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			forwarded = true
			if request.Header.Get("Accept-Encoding") != "" {
				t.Fatalf("Accept-Encoding must be stripped when restoring, got %q", request.Header.Get("Accept-Encoding"))
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(body) || strings.Contains(string(body), "alice@example.com") ||
				!strings.Contains(string(body), "model@example.com") {
				t.Fatalf("redacted body = %s", body)
			}
			upstreamPlaceholder = emailPlaceholderFromBody(t, body)
			if want := strings.Replace(original, "alice@example.com", upstreamPlaceholder, 1); string(body) != want {
				t.Fatalf("unmodified bytes or field order changed: got %s want %s", body, want)
			}
			replay, err := request.GetBody()
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := io.ReadAll(replay)
			_ = replay.Close()
			if err != nil || string(replayed) != string(body) {
				t.Fatalf("replay differs from filtered body: %v", err)
			}
			if request.ContentLength != int64(len(body)) ||
				request.Header.Get("Content-Length") != strconv.Itoa(len(body)) {
				t.Fatalf(
					"content length field=%d header=%q body=%d",
					request.ContentLength,
					request.Header.Get("Content-Length"),
					len(body),
				)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.Header().Set("Content-Length", "8")
			writer.WriteHeader(http.StatusOK)
			_, err = writer.Write([]byte(
				`{"choices":[{"index":0,"message":{"content":"` +
					upstreamPlaceholder +
					`"}}]}`,
			))
			return err
		}),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(original))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if !forwarded || response.Code != http.StatusOK {
		t.Fatalf("forwarded=%t response=%d %s", forwarded, response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "alice@example.com") ||
		strings.Contains(response.Body.String(), upstreamPlaceholder) {
		t.Fatalf("response was not restored: %s", response.Body.String())
	}
	if response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) {
		t.Fatalf("restored content-length=%q body=%d", response.Header().Get("Content-Length"), response.Body.Len())
	}
}

func TestStreamingResponseRestorePreservesPlaceholderSplitAcrossTransportFlushes(t *testing.T) {
	const original = `{"model":"gpt-5","stream":true,"input":"alice@example.com"}`
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled:         true,
		Mode:            privacy.ModeRegex,
		Action:          privacy.ActionRedact,
		ResponseRestore: true,
	}, nil)
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{
				Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, true),
			}, nil
		}),
		PrivacyFilter: filter,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "alice@example.com") {
				t.Fatalf("request was not redacted: %s", body)
			}
			placeholder := emailPlaceholderFromBody(t, body)
			split := len(placeholder) / 2
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body: &chunkReadCloser{chunks: [][]byte{
					[]byte(
						`data: {"type":"response.output_text.delta","item_id":"item_1","content_index":0,"delta":"` +
							placeholder[:split] +
							`"}` + "\n\n",
					),
					[]byte(
						`data: {"type":"response.output_text.delta","item_id":"item_1","content_index":0,"delta":"` +
							placeholder[split:] +
							`"}` + "\n\n",
					),
				}},
			}, nil
		})),
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(original)),
	)

	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "alice@example.com") ||
		strings.Contains(response.Body.String(), "<PRIVATE_") {
		t.Fatalf("split placeholder was not restored: %q", response.Body.String())
	}
}

func TestBufferedResponseRestoreDiscardsInterruptedAttemptBeforeFallback(t *testing.T) {
	const original = `{"model":"gpt-5","input":"alice@example.com"}`
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled:         true,
		Mode:            privacy.ModeRegex,
		Action:          privacy.ActionRedact,
		ResponseRestore: true,
	}, nil)
	first := validEndpoint(contract.ProtocolOpenAIResponses, false)
	first.ID = "endpoint_first"
	first.BaseURL = "https://first.example"
	second := first
	second.ID = "endpoint_second"
	second.BaseURL = "https://second.example"
	attempts := 0
	resolver := &healthTrackingCandidateResolver{
		candidateResolver: candidateResolver{candidates: []endpoint.Resolved{
			{Endpoint: first},
			{Endpoint: second},
		}},
	}
	handler := NewWithDependencies(Dependencies{
		Resolver:      resolver,
		PrivacyFilter: filter,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			body, err := io.ReadAll(request.Body)
			if err != nil || strings.Contains(string(body), "alice@example.com") {
				t.Fatalf("redacted attempt body = %q, %v", body, err)
			}
			placeholder := emailPlaceholderFromBody(t, body)
			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
			}
			if request.URL.Host == "first.example" {
				split := len(placeholder) / 2
				response.Body = io.NopCloser(io.MultiReader(
					strings.NewReader(
						`{"output":[{"type":"message","content":[{"type":"output_text","text":"`+
							placeholder[:split],
					),
					failingReader{err: errors.New("upstream body interrupted")},
				))
				return response, nil
			}
			response.Body = io.NopCloser(strings.NewReader(
				`{"output":[{"type":"message","content":[{"type":"output_text","text":"` +
					placeholder +
					`"}]}]}`,
			))
			return response, nil
		})),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodPost,
			"/v1/responses",
			strings.NewReader(original),
		),
	)

	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "alice@example.com") ||
		strings.Contains(response.Body.String(), "<PRIVATE_") {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "<PRIVATE_") {
		t.Fatalf("partial first response leaked: %q", response.Body.String())
	}
	if len(resolver.failures) != 1 || resolver.failures[0] != "endpoint_first" ||
		len(resolver.successes) != 1 || resolver.successes[0] != "endpoint_second" ||
		len(resolver.abandons) != 0 {
		t.Fatalf(
			"health outcomes failures=%v successes=%v abandons=%v",
			resolver.failures,
			resolver.successes,
			resolver.abandons,
		)
	}
}

func TestPrivacyRedactSkipsResponseRestoreWhenDisabled(t *testing.T) {
	const original = `{"model":"gpt-5","messages":[{"role":"user","content":"alice@example.com"}]}`
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, ResponseRestore: false,
	}, nil)
	var upstreamPlaceholder string
	records := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIChat, false)}, nil
		}),
		PrivacyFilter:  filter,
		RequestRecords: records,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			if request.Header.Get("Accept-Encoding") != "gzip" {
				t.Fatalf("Accept-Encoding should remain when restore is off, got %q", request.Header.Get("Accept-Encoding"))
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "alice@example.com") {
				t.Fatalf("request should still be redacted: %s", body)
			}
			upstreamPlaceholder = emailPlaceholderFromBody(t, body)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err = writer.Write([]byte(`{"echo":"` + upstreamPlaceholder + `"}`))
			return err
		}),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(original))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), upstreamPlaceholder) ||
		strings.Contains(response.Body.String(), "alice@example.com") {
		t.Fatalf("restore should stay off: %d %s", response.Code, response.Body.String())
	}
	if len(records.records) != 1 ||
		records.records[0].PrivacyRestore == nil ||
		records.records[0].PrivacyRestore.Enabled ||
		records.records[0].PrivacyRestore.MappingCount != 1 ||
		records.records[0].PrivacyRestore.RestoredCount != 0 {
		t.Fatalf("privacy diagnostics=%#v", records.records)
	}
	hits := records.records[0].PrivacyRestore.Hits
	if len(hits) != 1 || hits[0].Kind != contract.CanonicalKindEmail || hits[0].Count != 1 {
		t.Fatalf("privacy hits=%#v", hits)
	}
}

func TestPrivacyRedactRecordsHitKindsAndKeepsThemAfterPolicyChange(t *testing.T) {
	const original = `{"model":"gpt-5","input":"alice@example.com bob@example.com https://example.com/docs"}`
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, ResponseRestore: true,
	}, nil)
	records := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		PrivacyFilter:  filter,
		RequestRecords: records,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return err
			}
			if strings.Contains(string(body), "alice@example.com") ||
				strings.Contains(string(body), "https://example.com") {
				t.Fatalf("request should be redacted: %s", body)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err = writer.Write([]byte(`{"id":"resp_1"}`))
			return err
		}),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(original))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(records.records) != 1 || records.records[0].PrivacyRestore == nil {
		t.Fatalf("record=%#v", records.records)
	}
	got := records.records[0].PrivacyRestore.Hits
	want := []contract.PrivacyHitCount{
		{Kind: contract.CanonicalKindEmail, Count: 2},
		{Kind: contract.CanonicalKindURL, Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hits=%#v want=%#v", got, want)
	}

	allow := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionAllow,
	}, nil)
	_ = NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		PrivacyFilter:  allow,
		RequestRecords: records,
	})
	if !reflect.DeepEqual(records.records[0].PrivacyRestore.Hits, want) {
		t.Fatalf("hits changed after policy swap: %#v", records.records[0].PrivacyRestore.Hits)
	}
}

func TestPrivacyBlockRunsBeforeCredentialLoadingAndDoesNotLeakMatch(t *testing.T) {
	const body = `{"model":"gpt-5","input":"alice@example.com"}`
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionBlock,
	}, nil)
	authorized := false
	forwarded := false
	records := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			authorized = true
			return nil, nil
		}),
		PrivacyFilter:  filter,
		RequestRecords: records,
		Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
			forwarded = true
			return nil
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))

	envelope := assertInferenceError(t, response, http.StatusForbidden, "policy_blocked")
	assertPrivacyErrorRecord(t, records, response.Code, contract.RequestStatusBlocked, "privacy", "block", envelope.Error)
	if authorized || forwarded || strings.Contains(response.Body.String(), "alice@example.com") {
		t.Fatalf("unsafe block result: authorized=%t forwarded=%t body=%s", authorized, forwarded, response.Body.String())
	}
}

func TestPrivacyUnsafeRewriteStopsBeforeCredentialLoadingAndForwarding(t *testing.T) {
	// A detector can mistakenly include JSON structure in a sensitive span.
	// The original request is valid; only the redaction attempt fails.
	const body = `{"model":"gpt-5","input":[{"type":"function_call","name":"lookup","arguments":"{\"email\":\"alice@example.com\"}"}]}`
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeLocalModel, Action: privacy.ActionRedact,
		LocalModelID: "model_00000000000000000000000000000001",
	}, privacy.DetectorFunc(func(context.Context, privacy.DetectInput) ([]privacy.Finding, error) {
		return []privacy.Finding{{Segment: 0, Start: 0, End: 1, Kind: privacy.KindEmail, Confidence: 1}}, nil
	}))
	authorized := false
	forwarded := false
	records := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			authorized = true
			return nil, nil
		}),
		PrivacyFilter:  filter,
		RequestRecords: records,
		Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
			forwarded = true
			return nil
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)),
	)

	envelope := assertInferenceError(t, response, http.StatusUnprocessableEntity, "privacy_redaction_failed")
	if !strings.Contains(envelope.Error.Message, "redaction failed") || envelope.Error.Retryable {
		t.Fatalf("redaction failure = %#v", envelope.Error)
	}
	assertPrivacyErrorRecord(t, records, response.Code, contract.RequestStatusFailed, "privacy", "privacy_redaction_failed", envelope.Error)
	if authorized || forwarded {
		t.Fatalf("unsafe rewrite escaped privacy boundary: authorized=%t forwarded=%t", authorized, forwarded)
	}
}

func TestFallbackReevaluatesEndpointScopedPrivacyAgainstOriginalBody(t *testing.T) {
	const original = `{"model":"gpt-5","input":"alice@example.com"}`
	var scopes []contract.ServiceID
	filter, err := privacy.New(
		privacy.PolicyProviderFunc(func(_ context.Context, scope privacy.Scope) (privacy.Policy, error) {
			scopes = append(scopes, scope.ServiceID)
			if scope.ServiceID == "endpoint_second" {
				return privacy.Policy{
					Enabled: true,
					Mode:    privacy.ModeRegex,
					Action:  privacy.ActionBlock,
				}, nil
			}
			return privacy.Policy{}, nil
		}),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	first := validEndpoint(contract.ProtocolOpenAIResponses, false)
	first.ID = "endpoint_first"
	first.BaseURL = "https://first.example"
	second := first
	second.ID = "endpoint_second"
	second.BaseURL = "https://second.example"
	var forwarded []contract.ServiceID
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{
			{Endpoint: first},
			{Endpoint: second},
		}},
		PrivacyFilter: filter,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil || string(body) != original {
				t.Fatalf("forwarded original body = %q, %v", body, readErr)
			}
			switch request.URL.Host {
			case "first.example":
				forwarded = append(forwarded, "endpoint_first")
				return nil, errors.New("dial failed")
			case "second.example":
				forwarded = append(forwarded, "endpoint_second")
				return nil, errors.New("second endpoint must be blocked before forwarding")
			default:
				t.Fatalf("unexpected host %q", request.URL.Host)
				return nil, nil
			}
		})),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodPost,
			"/v1/responses",
			strings.NewReader(original),
		),
	)

	assertInferenceError(t, response, http.StatusForbidden, "policy_blocked")
	if len(scopes) != 2 ||
		strings.Join([]string{string(scopes[0]), string(scopes[1])}, ",") !=
			"endpoint_first,endpoint_second" {
		t.Fatalf("privacy scopes = %v", scopes)
	}
	if len(forwarded) != 1 || forwarded[0] != "endpoint_first" {
		t.Fatalf("forwarded endpoints = %v", forwarded)
	}
}

func TestPrivacyRetryDoesNotAccumulateNotice(t *testing.T) {
	const original = ` { "model":"gpt-5", "messages":[{"content":"be brief","role":"system"},{"role":"user","content":"alice@example.com"}] } `
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, PlaceholderNotice: true,
	}, nil)
	first := validEndpoint(contract.ProtocolOpenAIChat, false)
	first.ID, first.BaseURL = "endpoint_first", "https://first.example"
	second := first
	second.ID, second.BaseURL = "endpoint_second", "https://second.example"
	var attempts [][]byte
	handler := NewWithDependencies(Dependencies{
		Resolver:      candidateResolver{candidates: []endpoint.Resolved{{Endpoint: first}, {Endpoint: second}}},
		PrivacyFilter: filter,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if count := strings.Count(string(body), "redaction markers of the form"); count != 1 {
				t.Fatalf("attempt %d notice count=%d, want 1", len(attempts)+1, count)
			}
			if strings.Contains(string(body), "alice@example.com") {
				t.Fatal("attempt forwarded an unredacted address")
			}
			attempts = append(attempts, body)
			if request.URL.Host == "first.example" {
				return nil, errors.New("dial failed")
			}
			return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`), nil
		})),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(original))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(attempts) != 2 {
		t.Fatalf("status=%d attempts=%d", response.Code, len(attempts))
	}
	if string(attempts[0]) != string(attempts[1]) {
		t.Fatal("retry changed the filtered request body")
	}
}

func TestInspectedRetryBodyBorrowsOnePrivacyResidencyPermit(t *testing.T) {
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true,
		Mode:    privacy.ModeRegex,
		Action:  privacy.ActionWarn,
	}, nil)
	entered := make(chan struct{}, DefaultMaxConcurrentInspections+1)
	release := make(chan struct{})
	done := make(chan struct{}, DefaultMaxConcurrentInspections+1)
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{
				Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false),
			}, nil
		}),
		PrivacyFilter: filter,
		Forwarder: forwarderFunc(func(
			_ http.ResponseWriter,
			request *http.Request,
			_ transport.Target,
		) error {
			if _, err := io.Copy(io.Discard, request.Body); err != nil {
				return err
			}
			entered <- struct{}{}
			<-release
			return nil
		}),
	})
	start := func() {
		go func() {
			defer func() { done <- struct{}{} }()
			handler.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(
					http.MethodPost,
					"/v1/responses",
					strings.NewReader(`{"model":"gpt-5","input":"ordinary text"}`),
				),
			)
		}()
	}

	for range DefaultMaxConcurrentInspections {
		start()
	}
	for range DefaultMaxConcurrentInspections {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("privacy inspection deadlocked while borrowing the retry-body permit")
		}
	}

	start()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("extra concurrent request stayed queued behind in-flight streams")
	}
	releaseOnce.Do(func() { close(release) })
	for range DefaultMaxConcurrentInspections + 1 {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("privacy request did not finish")
		}
	}
}

func TestPrivacyInvalidInputIsReportedAsInspectionFailure(t *testing.T) {
	for _, body := range []string{
		`{"contents":[`,
		`{"contents":[{"parts":[{"text":"alice@example.com","text":"safe"}]}]}`,
	} {
		for _, action := range []privacy.Action{privacy.ActionWarn, privacy.ActionRedact} {
			t.Run(string(action)+"/"+body, func(t *testing.T) {
				filter := testPrivacyEngine(t, privacy.Policy{
					Enabled: true, Mode: privacy.ModeRegex, Action: action,
				}, nil)
				records := &memoryRequestRecordStore{}
				authorized := false
				forwarded := false
				handler := NewWithDependencies(Dependencies{
					Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
						return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolGoogleGenerateContent, false)}, nil
					}),
					Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
						authorized = true
						return nil, nil
					}),
					PrivacyFilter:  filter,
					RequestRecords: records,
					Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
						forwarded = true
						return nil
					}),
				})
				request := httptest.NewRequest(
					http.MethodPost,
					"/v1beta/models/gemini:generateContent",
					strings.NewReader(body),
				)
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)

				envelope := assertInferenceError(t, response, http.StatusUnprocessableEntity, "privacy_inspection_failed")
				if !strings.Contains(envelope.Error.Message, "inspection failed") || envelope.Error.Retryable {
					t.Fatalf("inspection failure = %#v", envelope.Error)
				}
				assertPrivacyErrorRecord(t, records, response.Code, contract.RequestStatusFailed, "privacy", "privacy_inspection_failed", envelope.Error)
				if authorized || forwarded {
					t.Fatalf("unsafe input escaped privacy boundary: authorized=%t forwarded=%t", authorized, forwarded)
				}
			})
		}
	}
}

func TestPrivacyDetectorAndPolicyFailuresUseSanitizedStatusMapping(t *testing.T) {
	tests := []struct {
		name        string
		providerErr error
		detectorErr error
		status      int
		code        string
	}{
		{name: "policy", providerErr: errors.New("private policy detail alice@example.com"), status: http.StatusServiceUnavailable, code: "privacy_policy_unavailable"},
		{name: "unavailable", detectorErr: errors.New("private model detail alice@example.com"), status: http.StatusServiceUnavailable, code: "safety_engine_unavailable"},
		{name: "limit", detectorErr: privacy.ErrDetectorLimit, status: http.StatusServiceUnavailable, code: "safety_engine_unavailable"},
		{name: "timeout", detectorErr: privacy.ErrDetectorTimeout, status: http.StatusServiceUnavailable, code: "safety_engine_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := privacy.PolicyProviderFunc(func(context.Context, privacy.Scope) (privacy.Policy, error) {
				if test.providerErr != nil {
					return privacy.Policy{}, test.providerErr
				}
				return privacy.Policy{
					Enabled: true, Mode: privacy.ModeModel,
					LocalModelID: "model_00000000000000000000000000000001",
					Action:       privacy.ActionBlock,
				}, nil
			})
			model := privacy.DetectorFunc(func(context.Context, privacy.DetectInput) ([]privacy.Finding, error) {
				return nil, test.detectorErr
			})
			filter, err := privacy.New(provider, model)
			if err != nil {
				t.Fatal(err)
			}
			records := &memoryRequestRecordStore{}
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
					return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
				}),
				PrivacyFilter:  filter,
				RequestRecords: records,
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(
				response,
				httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"alice@example.com"}`)),
			)
			envelope := assertInferenceError(t, response, test.status, test.code)
			assertPrivacyErrorRecord(t, records, response.Code, contract.RequestStatusFailed, "privacy", test.code, envelope.Error)
			if strings.Contains(response.Body.String(), "alice@example.com") ||
				strings.Contains(response.Body.String(), "private") {
				t.Fatalf("private detector detail leaked: %s", response.Body.String())
			}
		})
	}
}

func TestPrivacyReusesFourBufferedBodyPermitsForGemini(t *testing.T) {
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionWarn,
	}, nil)
	entered := make(chan struct{}, DefaultMaxConcurrentInspections+1)
	releaseForwarders := make(chan struct{})
	done := make(chan struct{}, DefaultMaxConcurrentInspections+1)
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseForwarders) })

	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolGoogleGenerateContent, false)}, nil
		}),
		PrivacyFilter: filter,
		Forwarder: forwarderFunc(func(_ http.ResponseWriter, request *http.Request, _ transport.Target) error {
			entered <- struct{}{}
			<-releaseForwarders
			_, err := io.Copy(io.Discard, request.Body)
			return err
		}),
	})
	start := func() {
		go func() {
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1beta/models/gemini:generateContent",
				strings.NewReader(`{"contents":[{"parts":[{"text":"ordinary"}]}]}`),
			)
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(httptest.NewRecorder(), request)
			done <- struct{}{}
		}()
	}
	for range DefaultMaxConcurrentInspections {
		start()
	}
	for range DefaultMaxConcurrentInspections {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("privacy-buffered request did not reach forwarder")
		}
	}
	start()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("extra privacy-buffered request stayed queued behind in-flight streams")
	}
	releaseOnce.Do(func() { close(releaseForwarders) })
	for range DefaultMaxConcurrentInspections + 1 {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("privacy-buffered request did not finish")
		}
	}
}

func TestPrivacyUsesConfiguredBodyLimit(t *testing.T) {
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionWarn,
	}, nil)
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolGoogleGenerateContent, false)}, nil
		}),
		PrivacyFilter:     filter,
		MaxRequestBodyMiB: 8,
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini:generateContent",
		strings.NewReader(strings.Repeat("x", (8<<20)+1)),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertInferenceError(t, response, http.StatusRequestEntityTooLarge, "request_too_large")
}

func testPrivacyEngine(t *testing.T, policy privacy.Policy, model privacy.Detector) *privacy.Engine {
	t.Helper()
	engine, err := privacy.New(privacy.PolicyProviderFunc(func(context.Context, privacy.Scope) (privacy.Policy, error) {
		return policy, nil
	}), model)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func emailPlaceholderFromBody(t *testing.T, body []byte) string {
	t.Helper()
	matches := emailPlaceholderPattern.FindAll(body, -1)
	if len(matches) != 1 {
		t.Fatalf("email placeholders = %q", body)
	}
	return string(matches[0])
}

func assertPrivacyErrorRecord(t *testing.T, records *memoryRequestRecordStore, httpStatus int, status contract.RequestStatus, category, eventSummary string, failure inferenceError) {
	t.Helper()
	if len(records.records) != 1 {
		t.Fatalf("records = %#v", records.records)
	}
	record := records.records[0]
	if record.Status != status || record.HTTPStatus == nil || *record.HTTPStatus != httpStatus || record.AttemptIndex != 0 {
		t.Fatalf("request failure metadata = %#v", record)
	}
	want := contract.ErrorSummary{Category: category, Code: failure.Code, Message: failure.Message, Retryable: failure.Retryable}
	if record.Error == nil || *record.Error != want {
		t.Fatalf("recorded error = %#v, want %#v", record.Error, want)
	}
	var privacyEvents []contract.RequestEvent
	for _, event := range record.Events {
		if event.Kind == contract.RequestEventPrivacy {
			privacyEvents = append(privacyEvents, event)
		}
	}
	if len(privacyEvents) != 1 || privacyEvents[0].Status != status || privacyEvents[0].Summary != eventSummary {
		t.Fatalf("privacy events = %#v", privacyEvents)
	}
	if strings.Contains(failure.Message, "alice@example.com") {
		t.Fatalf("error leaked sensitive input: %q", failure.Message)
	}
}

type trackingRequestBody struct {
	reader io.Reader
	reads  int
	closed bool
}

func (body *trackingRequestBody) Read(buffer []byte) (int, error) {
	body.reads++
	return body.reader.Read(buffer)
}

func (body *trackingRequestBody) Close() error {
	body.closed = true
	return nil
}

type chunkReadCloser struct {
	chunks [][]byte
}

func (reader *chunkReadCloser) Read(buffer []byte) (int, error) {
	if len(reader.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := reader.chunks[0]
	reader.chunks = reader.chunks[1:]
	if len(chunk) > len(buffer) {
		panic("test chunk exceeds transport read buffer")
	}
	return copy(buffer, chunk), nil
}

func (*chunkReadCloser) Close() error {
	return nil
}
