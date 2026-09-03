package transport

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestDefaultForwarderDisablesTransparentCompression(t *testing.T) {
	forwarder := New(nil)
	configured, ok := forwarder.roundTripper.(*http.Transport)
	if !ok || !configured.DisableCompression || configured.ResponseHeaderTimeout != 0 {
		t.Fatalf("default transport = %#v, want compression disabled and unbounded response headers", forwarder.roundTripper)
	}
}

func TestForwarderHonorsConfiguredResponseHeaderTimeout(t *testing.T) {
	forwarder := NewWithResponseHeaderTimeout(nil, 30*time.Second)
	configured, ok := forwarder.roundTripper.(*http.Transport)
	if !ok || configured.ResponseHeaderTimeout != 30*time.Second || !configured.DisableCompression {
		t.Fatalf("configured transport = %#v, want 30s response headers and compression disabled", forwarder.roundTripper)
	}
}

func TestJoinTargetURLNormalizesOneDuplicateProtocolVersionSegment(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		incoming string
		want     string
	}{
		{name: "OpenAI SDK base", base: "https://upstream.example/v1", incoming: "http://localhost/v1/responses", want: "https://upstream.example/v1/responses"},
		{name: "proxy OpenAI base", base: "https://upstream.example/proxy/v1/", incoming: "http://localhost/v1/models?limit=2", want: "https://upstream.example/proxy/v1/models?limit=2"},
		{name: "Gemini SDK base", base: "https://upstream.example/v1beta", incoming: "http://localhost/v1beta/models/gemini:generateContent", want: "https://upstream.example/v1beta/models/gemini:generateContent"},
		{name: "ordinary prefix", base: "https://upstream.example/api", incoming: "http://localhost/v1/responses", want: "https://upstream.example/api/v1/responses"},
		{name: "different version", base: "https://upstream.example/v1", incoming: "http://localhost/v1beta/models", want: "https://upstream.example/v1/v1beta/models"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := mustParseURL(t, test.base)
			incoming := mustParseURL(t, test.incoming)
			if got := joinTargetURL(base, incoming).String(); got != test.want {
				t.Fatalf("joined URL = %q, want %q", got, test.want)
			}
		})
	}
}

func TestForwardPreservesNativeRequestAndFiltersHopByHopHeaders(t *testing.T) {
	baseURL := mustParseURL(t, "https://upstream.example/api")
	forwarder := New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", request.Method)
		}
		if request.URL.String() != "https://upstream.example/api/v1/items/a%2Fb?limit=2&raw=%2F" {
			t.Errorf("URL = %q", request.URL.String())
		}
		if request.Host != "" {
			t.Errorf("Host override = %q, want empty", request.Host)
		}
		if request.RequestURI != "" {
			t.Errorf("RequestURI = %q, want empty", request.RequestURI)
		}
		if request.Header.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Errorf("Content-Type = %q", request.Header.Get("Content-Type"))
		}
		if request.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		for _, name := range []string{"Cookie", "X-Api-Key", "X-Goog-Api-Key", localPolicyWarningHeader} {
			if value := request.Header.Get(name); value != "" {
				t.Errorf("inbound credential header %s = %q", name, value)
			}
		}
		for _, name := range []string{"Connection", "Keep-Alive", "X-Request-Hop"} {
			if value := request.Header.Get(name); value != "" {
				t.Errorf("hop-by-hop request header %s = %q", name, value)
			}
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(body) != `{"model":"native"}` {
			t.Errorf("body = %q", body)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header: http.Header{
				"Content-Type":                {"application/json"},
				"Connection":                  {"X-Response-Hop"},
				"X-Response-Hop":              {"remove-me"},
				"Keep-Alive":                  {"timeout=5"},
				"X-Upstream":                  {"kept"},
				"Access-Control-Allow-Origin": {"*"},
				"Timing-Allow-Origin":         {"*"},
				localPolicyWarningHeader:      {"spoofed=999"},
			},
			Body: io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	}))

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/items/a%2Fb?limit=2&raw=%2F", strings.NewReader(`{"model":"native"}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Authorization", "Bearer client-value")
	request.Header.Set("Cookie", "local_session=secret")
	request.Header.Set("X-Api-Key", "client-anthropic-key")
	request.Header.Set("X-Goog-Api-Key", "client-google-key")
	request.Header.Set(localPolicyWarningHeader, "spoofed=999")
	request.Header.Set("Connection", "X-Request-Hop")
	request.Header.Set("X-Request-Hop", "remove-me")
	request.Header.Set("Keep-Alive", "timeout=5")
	response := httptest.NewRecorder()

	err := forwarder.Forward(response, request, Target{
		BaseURL: baseURL,
		RequestHeaders: http.Header{
			"Authorization":       {"Bearer upstream-secret"},
			"Connection":          {"X-Unsafe-Target-Hop"},
			"X-Unsafe-Target-Hop": {"remove-me"},
		},
	})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", response.Code)
	}
	if response.Body.String() != `{"ok":true}` {
		t.Errorf("body = %q", response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Errorf("response Content-Type = %q", response.Header().Get("Content-Type"))
	}
	if response.Header().Get("X-Upstream") != "kept" {
		t.Errorf("X-Upstream = %q", response.Header().Get("X-Upstream"))
	}
	for _, name := range []string{"Connection", "Keep-Alive", "X-Response-Hop"} {
		if value := response.Header().Get(name); value != "" {
			t.Errorf("hop-by-hop response header %s = %q", name, value)
		}
	}
	for _, name := range []string{"Access-Control-Allow-Origin", "Timing-Allow-Origin"} {
		if value := response.Header().Get(name); value != "" {
			t.Errorf("unsafe browser response header %s = %q", name, value)
		}
	}
	if value := response.Header().Get(localPolicyWarningHeader); value != "" {
		t.Errorf("spoofed local policy response header = %q", value)
	}
}

func TestForwardFlushesSSEChunksBeforeCompletion(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, "data: first\n\n")
		writer.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(writer, "data: second\n\n")
	}))
	defer upstream.Close()

	baseURL := mustParseURL(t, upstream.URL)
	forwarder := New(http.DefaultTransport)
	gateway := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = forwarder.Forward(writer, request, Target{BaseURL: baseURL})
	}))
	defer gateway.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(gateway.URL + "/v1/responses")
	if err != nil {
		t.Fatalf("GET gateway: %v", err)
	}
	defer response.Body.Close()
	defer close(release)

	firstChunk := make(chan string, 1)
	go func() {
		line, readErr := bufio.NewReader(response.Body).ReadString('\n')
		if readErr != nil {
			firstChunk <- "read error: " + readErr.Error()
			return
		}
		firstChunk <- line
	}()

	select {
	case line := <-firstChunk:
		if line != "data: first\n" {
			t.Fatalf("first SSE line = %q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("first SSE chunk was buffered until upstream completion")
	}
}

func TestForwardPropagatesCancellationToUpstream(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		close(started)
		<-request.Context().Done()
		close(cancelled)
	}))
	defer upstream.Close()

	baseURL := mustParseURL(t, upstream.URL)
	forwarder := New(http.DefaultTransport)
	gateway := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = forwarder.Forward(writer, request, Target{BaseURL: baseURL})
	}))
	defer gateway.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer response.Body.Close()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream request did not start")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream context was not cancelled")
	}
}

func TestDefaultForwarderReusesUpstreamConnections(t *testing.T) {
	remoteAddresses := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		remoteAddresses <- request.RemoteAddr
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{}`)
	}))
	defer upstream.Close()

	forwarder := New(nil)
	target := Target{BaseURL: mustParseURL(t, upstream.URL)}
	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		response := httptest.NewRecorder()
		if err := forwarder.Forward(response, request, target); err != nil {
			t.Fatalf("Forward: %v", err)
		}
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d", response.Code)
		}
	}

	first := <-remoteAddresses
	second := <-remoteAddresses
	if first != second {
		t.Fatalf("upstream connections were not reused: %q then %q", first, second)
	}
}

func TestForwardReturnsTypedErrorBeforeResponseStarts(t *testing.T) {
	sentinel := errors.New("dial failed")
	forwarder := New(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, sentinel
	}))
	writer := &trackingWriter{header: make(http.Header)}
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	err := forwarder.Forward(writer, request, Target{BaseURL: mustParseURL(t, "https://upstream.example")})
	var upstreamError *UpstreamError
	if !errors.As(err, &upstreamError) {
		t.Fatalf("error = %T %v, want *UpstreamError", err, err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error does not wrap RoundTrip failure")
	}
	if writer.started {
		t.Fatal("response started before RoundTrip failure was returned")
	}
}

func TestForwardRejectsNilResponseBodyBeforeStartingClientResponse(t *testing.T) {
	forwarder := New(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK}, nil
	}))
	writer := &trackingWriter{header: make(http.Header)}
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	err := forwarder.Forward(writer, request, Target{BaseURL: mustParseURL(t, "https://upstream.example")})
	var upstreamError *UpstreamError
	if !errors.As(err, &upstreamError) || writer.started {
		t.Fatalf("error = %T %v, started=%t", err, err, writer.started)
	}
}

func TestForwardDoesNotRetryAfterResponseStarts(t *testing.T) {
	sentinel := errors.New("stream interrupted")
	var roundTrips atomic.Int32
	forwarder := New(roundTripFunc(func(*http.Request) (*http.Response, error) {
		roundTrips.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       io.NopCloser(io.MultiReader(strings.NewReader("data: partial\n\n"), errorReader{err: sentinel})),
		}, nil
	}))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))

	err := forwarder.Forward(response, request, Target{BaseURL: mustParseURL(t, "https://upstream.example")})
	var responseError *ResponseError
	if !errors.As(err, &responseError) {
		t.Fatalf("error = %T %v, want *ResponseError", err, err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error does not wrap stream failure")
	}
	if got := roundTrips.Load(); got != 1 {
		t.Fatalf("round trips = %d, want 1", got)
	}
	if response.Body.String() != "data: partial\n\n" {
		t.Fatalf("partial body = %q", response.Body.String())
	}
}

type trackingWriter struct {
	header  http.Header
	started bool
}

func (writer *trackingWriter) Header() http.Header {
	return writer.header
}

func (writer *trackingWriter) WriteHeader(int) {
	writer.started = true
}

func (writer *trackingWriter) Write([]byte) (int, error) {
	writer.started = true
	return 0, nil
}

type errorReader struct {
	err error
}

func (reader errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}

func mustParseURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	return parsed
}
