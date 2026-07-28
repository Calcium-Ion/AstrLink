package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
			if request.Body != tracked || tracked.reads != 0 {
				t.Fatalf("disabled policy touched body: body=%T reads=%d", request.Body, tracked.reads)
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

func TestPrivacyRedactReassemblesJSONAndUpdatesBodyLength(t *testing.T) {
	const original = " {\n \"model\":\"model@example.com\", \"messages\":[{\"role\":\"user\",\"content\":\"alice@example.com\"}]\n} "
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, ResponseRestore: true,
	}, nil)
	upstream := validEndpoint(contract.ProtocolOpenAIChat, false)
	forwarded := false
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
				!strings.Contains(string(body), "model@example.com") ||
				!strings.Contains(string(body), "<PRIVATE_EMAIL>") {
				t.Fatalf("redacted body = %s", body)
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
			_, err = writer.Write([]byte(`{"echo":"<PRIVATE_EMAIL>"}`))
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
		strings.Contains(response.Body.String(), "<PRIVATE_EMAIL>") {
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
			if strings.Contains(string(body), "alice@example.com") ||
				!strings.Contains(string(body), "<PRIVATE_EMAIL>") {
				t.Fatalf("request was not redacted: %s", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body: &chunkReadCloser{chunks: [][]byte{
					[]byte("data: <PRIVATE_"),
					[]byte("EMAIL>\n\n"),
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
	if response.Body.String() != "data: alice@example.com\n\n" {
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
			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
			}
			if request.URL.Host == "first.example" {
				response.Body = io.NopCloser(io.MultiReader(
					strings.NewReader(`{"echo":"<PRIVATE_`),
					failingReader{err: errors.New("upstream body interrupted")},
				))
				return response, nil
			}
			response.Body = io.NopCloser(strings.NewReader(`{"echo":"<PRIVATE_EMAIL>"}`))
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
		response.Body.String() != `{"echo":"alice@example.com"}` {
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
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIChat, false)}, nil
		}),
		PrivacyFilter: filter,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			if request.Header.Get("Accept-Encoding") != "gzip" {
				t.Fatalf("Accept-Encoding should remain when restore is off, got %q", request.Header.Get("Accept-Encoding"))
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "alice@example.com") || !strings.Contains(string(body), "<PRIVATE_EMAIL>") {
				t.Fatalf("request should still be redacted: %s", body)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err = writer.Write([]byte(`{"echo":"<PRIVATE_EMAIL>"}`))
			return err
		}),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(original))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "<PRIVATE_EMAIL>") ||
		strings.Contains(response.Body.String(), "alice@example.com") {
		t.Fatalf("restore should stay off: %d %s", response.Code, response.Body.String())
	}
}

func TestPrivacyBlockRunsBeforeCredentialLoadingAndDoesNotLeakMatch(t *testing.T) {
	const body = `{"model":"gpt-5","input":"alice@example.com"}`
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionBlock,
	}, nil)
	authorized := false
	forwarded := false
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			authorized = true
			return nil, nil
		}),
		PrivacyFilter: filter,
		Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
			forwarded = true
			return nil
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))

	assertInferenceError(t, response, http.StatusForbidden, "policy_blocked")
	if authorized || forwarded || strings.Contains(response.Body.String(), "alice@example.com") {
		t.Fatalf("unsafe block result: authorized=%t forwarded=%t body=%s", authorized, forwarded, response.Body.String())
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

func TestInspectedRetryBodyBorrowsOnePrivacyResidencyPermit(t *testing.T) {
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true,
		Mode:    privacy.ModeRegex,
		Action:  privacy.ActionWarn,
	}, nil)
	entered := make(chan struct{}, maxConcurrentMetadataInspections+1)
	release := make(chan struct{})
	done := make(chan struct{}, maxConcurrentMetadataInspections+1)
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

	for range maxConcurrentMetadataInspections {
		start()
	}
	for range maxConcurrentMetadataInspections {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("privacy inspection deadlocked while borrowing the retry-body permit")
		}
	}

	start()
	select {
	case <-entered:
		t.Fatal("fifth retry buffer escaped the four-request residency bound")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("released retry permit did not admit the fifth request")
	}
	for range maxConcurrentMetadataInspections + 1 {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("privacy request did not finish")
		}
	}
}

func TestPrivacyEnabledFailsClosedForMalformedGeminiJSON(t *testing.T) {
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionWarn,
	}, nil)
	forwarded := false
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolGoogleGenerateContent, false)}, nil
		}),
		PrivacyFilter: filter,
		Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
			forwarded = true
			return nil
		}),
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini:generateContent",
		strings.NewReader(`{"contents":[`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertInferenceError(t, response, http.StatusForbidden, "policy_blocked")
	if forwarded {
		t.Fatal("malformed Gemini body was forwarded")
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
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
					return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
				}),
				PrivacyFilter: filter,
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(
				response,
				httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"alice@example.com"}`)),
			)
			assertInferenceError(t, response, test.status, test.code)
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
	entered := make(chan struct{}, maxConcurrentMetadataInspections+1)
	releaseForwarders := make(chan struct{})
	done := make(chan struct{}, maxConcurrentMetadataInspections+1)
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
	for range maxConcurrentMetadataInspections {
		start()
	}
	for range maxConcurrentMetadataInspections {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("privacy-buffered request did not reach forwarder")
		}
	}
	start()
	select {
	case <-entered:
		t.Fatal("fifth privacy buffer bypassed the four-buffer bound")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(releaseForwarders) })
	for range maxConcurrentMetadataInspections + 1 {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("privacy-buffered request did not finish")
		}
	}
}

func TestPrivacyUsesExistingEightMiBBodyLimit(t *testing.T) {
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionWarn,
	}, nil)
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolGoogleGenerateContent, false)}, nil
		}),
		PrivacyFilter: filter,
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini:generateContent",
		strings.NewReader(strings.Repeat("x", maxMetadataBytes+1)),
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
