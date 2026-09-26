package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/gorilla/websocket"
)

// redirectSettingsStore is a request-record store that also serves routing
// settings, matching the sqlite store the handler type-asserts in production.
type redirectSettingsStore struct {
	mu       sync.Mutex
	records  memoryRequestRecordStore
	settings contract.RoutingSettings
	reads    int
	readErr  error
}

func newRedirectSettingsStore(redirects ...contract.ModelRedirect) *redirectSettingsStore {
	settings := contract.DefaultRoutingSettings()
	settings.ModelRedirects = redirects
	return &redirectSettingsStore{settings: settings}
}

func (store *redirectSettingsStore) InsertRequestRecord(ctx context.Context, record contract.RequestRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.records.InsertRequestRecord(ctx, record)
}

func (store *redirectSettingsStore) UpsertRequestRecord(ctx context.Context, record contract.RequestRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.records.UpsertRequestRecord(ctx, record)
}

func (store *redirectSettingsStore) FindSessionLink(ctx context.Context, kind contract.SessionCursorKind, values []string, scope storage.SessionCursorScope) (storage.SessionLinkMatch, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.records.FindSessionLink(ctx, kind, values, scope)
}

func (store *redirectSettingsStore) GetRoutingSettings(context.Context) (contract.RoutingSettings, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reads++
	if store.readErr != nil {
		return contract.RoutingSettings{}, store.readErr
	}
	settings := store.settings
	settings.ModelRedirects = append([]contract.ModelRedirect(nil), store.settings.ModelRedirects...)
	return settings, nil
}

func (store *redirectSettingsStore) UpdateRoutingSettings(_ context.Context, settings contract.RoutingSettings) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.settings = settings
	return nil
}

func (store *redirectSettingsStore) setRedirects(redirects ...contract.ModelRedirect) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.settings.ModelRedirects = redirects
}

func (store *redirectSettingsStore) snapshot() []contract.RequestRecord {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]contract.RequestRecord(nil), store.records.records...)
}

func (store *redirectSettingsStore) roots(t *testing.T) []contract.RequestRecord {
	t.Helper()
	var roots []contract.RequestRecord
	for _, record := range store.snapshot() {
		if record.ParentRequestID == nil {
			roots = append(roots, record)
		}
	}
	return roots
}

func (store *redirectSettingsStore) root(t *testing.T) contract.RequestRecord {
	t.Helper()
	roots := store.roots(t)
	if len(roots) != 1 {
		t.Fatalf("root records = %#v", roots)
	}
	return roots[0]
}

func enabledRedirect(from, to string) contract.ModelRedirect {
	return contract.ModelRedirect{From: from, To: to, Enabled: true}
}

func eventKinds(events []contract.RequestEvent) []string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, string(event.Kind))
	}
	return kinds
}

// assertNoGatewayIdentity checks the final outgoing request: the redirect may
// change only the model, never add AstrLink identity headers.
func assertNoGatewayIdentity(t *testing.T, request *http.Request, clientUserAgent string) {
	t.Helper()
	for name, values := range request.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-astrlink") {
			t.Errorf("reserved header forwarded upstream: %s", name)
		}
		for _, value := range values {
			if strings.Contains(strings.ToLower(value), "astrlink") {
				t.Errorf("upstream header %s carries gateway identity: %q", name, value)
			}
		}
	}
	for _, name := range []string{"Via", "X-Powered-By", "Originator"} {
		if request.Header.Get(name) != "" {
			t.Errorf("upstream header %s = %q", name, request.Header.Get(name))
		}
	}
	if request.Header.Get("User-Agent") != clientUserAgent {
		t.Errorf("User-Agent = %q, want client %q", request.Header.Get("User-Agent"), clientUserAgent)
	}
	if strings.Contains(strings.ToLower(request.URL.String()), "astrlink") {
		t.Errorf("upstream URL carries gateway identity: %s", request.URL)
	}
}

func TestModelRedirectSendsTargetUpstreamAndForwardsResponseUnchanged(t *testing.T) {
	const clientUA = "client-sdk/1.2.3"
	tests := []struct {
		name         string
		path         string
		body         string
		protocol     contract.ProtocolID
		streaming    bool
		target       string
		wantURL      string
		wantBody     string
		upstreamType string
		upstream     string
	}{
		{
			name: "chat completions json", path: "/v1/chat/completions", protocol: contract.ProtocolOpenAIChat,
			body:     `{"model":"client-model","messages":[{"role":"user","content":"mentions astrlink"}]}`,
			target:   "target-model",
			wantURL:  "https://upstream.example/prefix/v1/chat/completions",
			wantBody: `{"model":"target-model","messages":[{"role":"user","content":"mentions astrlink"}]}`,
			upstream: `{"id":"chatcmpl_1","model":"target-model","choices":[]}`, upstreamType: "application/json",
		},
		{
			name: "responses json", path: "/v1/responses", protocol: contract.ProtocolOpenAIResponses,
			body:     `{"model":"client-model","input":"hello"}`,
			target:   "target-model",
			wantURL:  "https://upstream.example/prefix/v1/responses",
			wantBody: `{"model":"target-model","input":"hello"}`,
			upstream: `{"id":"resp_1","model":"target-model","output":[]}`, upstreamType: "application/json",
		},
		{
			name: "responses sse", path: "/v1/responses", protocol: contract.ProtocolOpenAIResponses, streaming: true,
			body:         `{"model":"client-model","stream":true,"input":"hello"}`,
			target:       "target-model",
			wantURL:      "https://upstream.example/prefix/v1/responses",
			wantBody:     `{"model":"target-model","stream":true,"input":"hello"}`,
			upstream:     "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"target-model\"}}\n\n",
			upstreamType: "text/event-stream",
		},
		{
			name: "anthropic messages json", path: "/v1/messages", protocol: contract.ProtocolAnthropicMessages,
			body:     `{"model":"client-model","max_tokens":8,"messages":[]}`,
			target:   "target-model",
			wantURL:  "https://upstream.example/prefix/v1/messages",
			wantBody: `{"model":"target-model","max_tokens":8,"messages":[]}`,
			upstream: `{"id":"msg_1","type":"message","model":"target-model","content":[]}`, upstreamType: "application/json",
		},
		{
			name: "anthropic messages sse", path: "/v1/messages", protocol: contract.ProtocolAnthropicMessages, streaming: true,
			body:         `{"model":"client-model","stream":true,"messages":[]}`,
			target:       "target-model",
			wantURL:      "https://upstream.example/prefix/v1/messages",
			wantBody:     `{"model":"target-model","stream":true,"messages":[]}`,
			upstream:     "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"target-model\"}}\n\n",
			upstreamType: "text/event-stream",
		},
		{
			name: "gemini path escapes target once", path: "/v1beta/models/client-model:generateContent", protocol: contract.ProtocolGoogleGenerateContent,
			body:     `{"contents":[{"parts":[{"text":"hello"}]}]}`,
			target:   "tuned/gemini flash",
			wantURL:  "https://upstream.example/prefix/v1beta/models/tuned%2Fgemini%20flash:generateContent",
			wantBody: `{"contents":[{"parts":[{"text":"hello"}]}]}`,
			upstream: `{"candidates":[],"modelVersion":"tuned/gemini flash"}`, upstreamType: "application/json",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newRedirectSettingsStore(enabledRedirect("client-model", test.target))
			var resolved []string
			handler := NewWithDependencies(Dependencies{
				// A Resolve-only resolver leaves UpstreamModel empty; the
				// handler must still send the redirect target.
				Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
					resolved = append(resolved, request.Model)
					return endpoint.Resolved{Endpoint: validEndpoint(test.protocol, test.streaming)}, nil
				}),
				RequestRecords: store,
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					if request.URL.String() != test.wantURL {
						t.Errorf("upstream URL = %q, want %q", request.URL.String(), test.wantURL)
					}
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Fatal(err)
					}
					if string(body) != test.wantBody {
						t.Errorf("upstream body = %q, want %q", body, test.wantBody)
					}
					assertNoGatewayIdentity(t, request, clientUA)
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {test.upstreamType}},
						Body:       io.NopCloser(strings.NewReader(test.upstream)),
					}, nil
				})),
			})
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("User-Agent", clientUA)
			request.Header.Set("X-AstrLink-Trace", "local-only")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			// The redirect only touches the request; the client sees the
			// upstream's own model name.
			if response.Code != http.StatusOK || response.Body.String() != test.upstream {
				t.Fatalf("client response = %d %q, want upstream bytes %q", response.Code, response.Body.String(), test.upstream)
			}
			if strings.Join(resolved, ",") != test.target {
				t.Fatalf("resolved models = %v, want target %q", resolved, test.target)
			}
			record := store.root(t)
			if record.RequestedModel == nil || *record.RequestedModel != "client-model" {
				t.Fatalf("requested model = %v", record.RequestedModel)
			}
			if record.ModelRedirect == nil || *record.ModelRedirect != (contract.RequestModelRedirect{From: "client-model", To: test.target}) {
				t.Fatalf("model redirect = %#v", record.ModelRedirect)
			}
			if record.Recovery == nil || record.Recovery.UpstreamModel != test.target {
				t.Fatalf("recovery = %#v", record.Recovery)
			}
		})
	}
}

func TestModelRedirectRecordsClosedEventAfterAccepted(t *testing.T) {
	store := newRedirectSettingsStore(enabledRedirect("client-model", "target-model"))
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			// Settings are read after the pending row is written, so the
			// redirect must be persisted before any upstream attempt.
			pending := store.root(t)
			if pending.Status != contract.RequestStatusPending || pending.ModelRedirect == nil || pending.ModelRedirect.To != "target-model" {
				t.Errorf("pending record before routing = %#v", pending)
			}
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		RequestRecords: store,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
			writer.Header().Set("Content-Type", "application/json")
			_, err := writer.Write([]byte(`{"id":"resp_1","model":"target-model"}`))
			return err
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"client-model","input":"hello"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	record := store.root(t)
	if err := record.Validate(); err != nil {
		t.Fatalf("record invalid: %v", err)
	}
	// The redirect comes right after accepted, before privacy and routing.
	kinds := eventKinds(record.Events)
	want := []string{"accepted", "model_redirect", "privacy", "routed", "upstream", "completed"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("event kinds = %v, want %v", kinds, want)
	}
	accepted, redirect := record.Events[0], record.Events[1]
	if !strings.HasPrefix(accepted.Summary, "client-model · ") {
		t.Fatalf("accepted summary = %q, want client model", accepted.Summary)
	}
	if redirect.Status != contract.RequestStatusSucceeded || redirect.Summary != "client-model → target-model" ||
		redirect.EndedAt == nil || !redirect.EndedAt.Equal(redirect.StartedAt) || redirect.AttemptIndex != 0 {
		t.Fatalf("redirect event = %#v", redirect)
	}
}

func TestModelRedirectRetryChildrenInheritRedirect(t *testing.T) {
	first := validEndpoint(contract.ProtocolOpenAIResponses, false)
	first.ID, first.BaseURL = "endpoint_first", "https://first.example"
	second := validEndpoint(contract.ProtocolOpenAIResponses, false)
	second.ID, second.BaseURL = "endpoint_second", "https://second.example"
	store := newRedirectSettingsStore(enabledRedirect("client-model", "target-model"))
	var bodies []string
	handler := NewWithDependencies(Dependencies{
		Resolver:       candidateResolver{candidates: []endpoint.Resolved{{Endpoint: first}, {Endpoint: second}}},
		RequestRecords: store,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(request.Body)
			bodies = append(bodies, string(body))
			if request.URL.Host == "first.example" {
				return nil, errors.New("dial failed before response start")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"model":"target-model"}`)),
			}, nil
		})),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"client-model","input":"hi"}`)))
	if response.Code != http.StatusOK || response.Body.String() != `{"model":"target-model"}` {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	want := `{"model":"target-model","input":"hi"}`
	if len(bodies) != 2 || bodies[0] != want || bodies[1] != want {
		t.Fatalf("upstream bodies = %v", bodies)
	}
	var children int
	for _, record := range store.snapshot() {
		if record.ModelRedirect == nil || *record.ModelRedirect != (contract.RequestModelRedirect{From: "client-model", To: "target-model"}) {
			t.Fatalf("record %s model redirect = %#v", record.ID, record.ModelRedirect)
		}
		if record.ParentRequestID == nil {
			continue
		}
		children++
		for _, event := range record.Events {
			if event.Kind == contract.RequestEventModelRedirect {
				t.Fatalf("child copied the root-only redirect event: %v", eventKinds(record.Events))
			}
		}
		if record.Recovery == nil || record.Recovery.UpstreamModel != "target-model" {
			t.Fatalf("child recovery = %#v", record.Recovery)
		}
	}
	if children != 1 {
		t.Fatalf("children = %d", children)
	}
	if kinds := eventKinds(store.root(t).Events); len(kinds) < 2 || kinds[1] != "model_redirect" {
		t.Fatalf("root event kinds = %v", kinds)
	}
}

func TestModelRedirectDisabledRuleIsNoOp(t *testing.T) {
	store := newRedirectSettingsStore(contract.ModelRedirect{From: "client-model", To: "target-model", Enabled: false})
	var resolved string
	var upstreamBody string
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			resolved = request.Model
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIChat, false)}, nil
		}),
		RequestRecords: store,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(request.Body)
			upstreamBody = string(body)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		})),
	})
	const body = `{"model":"client-model","messages":[]}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if response.Code != http.StatusOK || resolved != "client-model" || upstreamBody != body {
		t.Fatalf("status=%d resolved=%q upstream=%q", response.Code, resolved, upstreamBody)
	}
	record := store.root(t)
	if record.ModelRedirect != nil {
		t.Fatalf("disabled rule recorded redirect %#v", record.ModelRedirect)
	}
	for _, event := range record.Events {
		if event.Kind == contract.RequestEventModelRedirect {
			t.Fatalf("disabled rule emitted event: %v", eventKinds(record.Events))
		}
	}
}

func TestModelRedirectFromRetiredAutoModel(t *testing.T) {
	newHandler := func(store *redirectSettingsStore, called *bool) *Handler {
		return NewWithDependencies(Dependencies{
			Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
				*called = true
				if request.Model != "gpt-4.1" {
					t.Errorf("resolved model = %q", request.Model)
				}
				return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
			}),
			RequestRecords: store,
			Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(request.Body)
				if string(body) != `{"model":"gpt-4.1","input":"hi"}` {
					t.Errorf("upstream body = %q", body)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"model":"gpt-4.1"}`))}, nil
			})),
		})
	}
	const body = `{"model":"astrlink/auto","input":"hi"}`

	t.Run("redirected", func(t *testing.T) {
		called := false
		store := newRedirectSettingsStore(enabledRedirect(contract.AstrLinkAutoModelID, "gpt-4.1"))
		response := httptest.NewRecorder()
		newHandler(store, &called).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
		if response.Code != http.StatusOK || !called || response.Body.String() != `{"model":"gpt-4.1"}` {
			t.Fatalf("response = %d %q called=%t", response.Code, response.Body.String(), called)
		}
		if redirect := store.root(t).ModelRedirect; redirect == nil || redirect.From != contract.AstrLinkAutoModelID || redirect.To != "gpt-4.1" {
			t.Fatalf("model redirect = %#v", redirect)
		}
	})
	t.Run("without rule stays retired", func(t *testing.T) {
		called := false
		store := newRedirectSettingsStore(contract.ModelRedirect{From: contract.AstrLinkAutoModelID, To: "gpt-4.1", Enabled: false})
		response := httptest.NewRecorder()
		newHandler(store, &called).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
		assertInferenceError(t, response, http.StatusGone, "routing_feature_retired")
		if called {
			t.Fatal("retired auto model reached the resolver")
		}
	})
}

func TestModelRedirectResolveErrorsNameClientModel(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "no endpoint", err: endpoint.ErrNoEndpoint},
		{name: "capability", err: &endpoint.CapabilityUnavailableError{
			Protocol: contract.ProtocolOpenAIResponses, Model: "target-model",
			Modes: []contract.CapabilityMode{contract.CapabilityModeNative}, Streaming: false,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newRedirectSettingsStore(enabledRedirect("client-model", "target-model"))
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
					if request.Model != "target-model" {
						t.Errorf("resolved model = %q", request.Model)
					}
					return endpoint.Resolved{}, test.err
				}),
				RequestRecords: store,
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"client-model","input":"hi"}`)))
			envelope := assertInferenceError(t, response, http.StatusUnprocessableEntity, "missing_protocol_capability")
			if !strings.Contains(envelope.Error.Message, `model "client-model"`) || strings.Contains(response.Body.String(), "target-model") {
				t.Fatalf("error = %s", response.Body.String())
			}
			if redirect := store.root(t).ModelRedirect; redirect == nil || redirect.To != "target-model" {
				t.Fatalf("failed record lost redirect: %#v", redirect)
			}
		})
	}
}

// The resolver names the routed model on every candidate. Without a redirect
// the request and a compressed response must pass through byte for byte; with
// one, only the request's model changes.
func TestModelRedirectLeavesOtherRequestAndResponseBytesAlone(t *testing.T) {
	// An escaped model would lose its escape if AstrLink re-encoded it.
	const body = `{"messages":[],"model":"client\u002dmodel"}`
	compressed := gzipBytes(t, []byte(`{"id":"chatcmpl_1","model":"provider-reported","choices":[]}`))
	for _, test := range []struct {
		name      string
		redirects []contract.ModelRedirect
		wantBody  string
	}{
		{name: "no redirect", wantBody: body},
		{name: "redirect", redirects: []contract.ModelRedirect{enabledRedirect("client-model", "target-model")}, wantBody: `{"messages":[],"model":"target-model"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newRedirectSettingsStore(test.redirects...)
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
					return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIChat, false), UpstreamModel: request.Model}, nil
				}),
				RequestRecords: store,
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					got, err := io.ReadAll(request.Body)
					if err != nil {
						t.Fatal(err)
					}
					if string(got) != test.wantBody {
						t.Errorf("upstream body = %q, want %q", got, test.wantBody)
					}
					if encoding := request.Header.Get("Accept-Encoding"); encoding != "gzip" {
						t.Errorf("upstream Accept-Encoding = %q, want client value", encoding)
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}},
						Body:       io.NopCloser(bytes.NewReader(compressed)),
					}, nil
				})),
			})
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept-Encoding", "gzip")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), compressed) ||
				response.Header().Get("Content-Encoding") != "gzip" {
				t.Fatalf("client response = %d %q encoding=%q", response.Code, response.Body.Bytes(), response.Header().Get("Content-Encoding"))
			}
		})
	}
}

func TestModelRedirectResponsesWebSocketPinsRoutingModel(t *testing.T) {
	models := make(chan string, 3)
	var connections sync.WaitGroup
	connections.Add(1)
	var dialed int
	var dialMu sync.Mutex
	upstream := wsUpstream(t, func(conn *websocket.Conn, _ *http.Request) {
		dialMu.Lock()
		dialed++
		dialMu.Unlock()
		for turn := 0; turn < 2; turn++ {
			var event map[string]any
			if err := conn.ReadJSON(&event); err != nil {
				t.Error(err)
				return
			}
			model, _ := event["model"].(string)
			models <- model
			_ = conn.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_" + string(rune('a'+turn)), "model": model, "status": "completed", "output": []any{}}})
		}
	})
	store := newRedirectSettingsStore(enabledRedirect("client-model", "target-a"))
	var resolvedMu sync.Mutex
	var resolved []string
	candidate := wsCandidate(upstream.URL + "/prefix/v1")
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			resolvedMu.Lock()
			resolved = append(resolved, request.Model)
			resolvedMu.Unlock()
			return candidate, nil
		}),
		RequestRecords: store,
	})
	client := dialResponses(t, handler, nil)

	sendWS(t, client, `{"type":"response.create","model":"client-model","input":"one"}`)
	if completed := readWS(t, client); completed["response"].(map[string]any)["model"] != "target-a" {
		t.Fatalf("first completed = %#v", completed)
	}
	// A rule edit must not move a socket that is already bound upstream.
	store.setRedirects(enabledRedirect("client-model", "target-b"))
	sendWS(t, client, `{"type":"response.create","model":"client-model","input":"two"}`)
	if completed := readWS(t, client); completed["response"].(map[string]any)["model"] != "target-a" {
		t.Fatalf("second completed = %#v", completed)
	}
	if first, second := <-models, <-models; first != "target-a" || second != "target-a" {
		t.Fatalf("upstream models = %q, %q", first, second)
	}
	resolvedMu.Lock()
	if strings.Join(resolved, ",") != "target-a,target-a" {
		t.Fatalf("resolved = %v", resolved)
	}
	resolvedMu.Unlock()
	dialMu.Lock()
	if dialed != 1 {
		t.Fatalf("upstream connections = %d", dialed)
	}
	dialMu.Unlock()
	roots := store.roots(t)
	if len(roots) != 2 {
		t.Fatalf("records = %d", len(roots))
	}
	for _, record := range roots {
		if record.ModelRedirect == nil || *record.ModelRedirect != (contract.RequestModelRedirect{From: "client-model", To: "target-a"}) {
			t.Fatalf("turn record redirect = %#v", record.ModelRedirect)
		}
	}
}

// A turn that cannot read the rules fails without binding the socket, so the
// next turn still redirects instead of pinning the client's model.
func TestModelRedirectResponsesWebSocketSettingsFailureDoesNotPin(t *testing.T) {
	models := make(chan string, 2)
	upstream := wsUpstream(t, func(conn *websocket.Conn, _ *http.Request) {
		var event map[string]any
		if err := conn.ReadJSON(&event); err != nil {
			t.Error(err)
			return
		}
		model, _ := event["model"].(string)
		models <- model
		_ = conn.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_a", "model": model, "status": "completed", "output": []any{}}})
	})
	store := newRedirectSettingsStore(enabledRedirect("client-model", "target-a"))
	store.readErr = errors.New("database is locked")
	candidate := wsCandidate(upstream.URL + "/prefix/v1")
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return candidate, nil
		}),
		RequestRecords: store,
		RecordLogger:   func(string, ...any) {},
	})
	client := dialResponses(t, handler, nil)

	sendWS(t, client, `{"type":"response.create","model":"client-model","input":"one"}`)
	if failed := readWS(t, client); failed["type"] != "error" {
		t.Fatalf("first turn = %#v", failed)
	}
	store.mu.Lock()
	store.readErr = nil
	store.mu.Unlock()
	sendWS(t, client, `{"type":"response.create","model":"client-model","input":"two"}`)
	if completed := readWS(t, client); completed["response"].(map[string]any)["model"] != "target-a" {
		t.Fatalf("second completed = %#v", completed)
	}
	if model := <-models; model != "target-a" {
		t.Fatalf("upstream model = %q", model)
	}
	select {
	case model := <-models:
		t.Fatalf("failed turn reached upstream with %q", model)
	default:
	}
}

func TestModelRedirectResponsesWebSocketFilterUsesRoutingModel(t *testing.T) {
	candidate := wsCandidate("https://upstream.example")
	session := &responsesWSSession{}
	if got := session.filterCandidates("client-model", "target-model", []endpoint.Resolved{candidate}); len(got) != 1 {
		t.Fatalf("unbound socket candidates = %d", len(got))
	}
	session.serviceID, session.model, session.upstreamModel, session.routingModel = candidate.CanonicalService().ID, "client-model", "target-model", "target-model"
	if got := session.filterCandidates("client-model", "target-model", []endpoint.Resolved{candidate}); len(got) != 1 {
		t.Fatalf("bound socket rejected its routing model")
	}
	if got := session.filterCandidates("client-model", "other-target", []endpoint.Resolved{candidate}); len(got) != 0 {
		t.Fatalf("bound socket accepted a different upstream model")
	}
	if got := session.filterCandidates("other-client", "target-model", []endpoint.Resolved{candidate}); len(got) != 0 {
		t.Fatalf("bound socket accepted a different client model")
	}
}

func TestModelDiscoveryListsRedirectSources(t *testing.T) {
	redirects := []contract.ModelRedirect{
		enabledRedirect("client-model", "target-model"),
		{From: "disabled-source", To: "target-model", Enabled: false},
		enabledRedirect("orphan-source", "missing-target"),
		enabledRedirect("other-model", "target-model"),
		enabledRedirect(contract.AstrLinkAutoModelID, "target-model"),
		enabledRedirect("vendor/source", "gemini-target"),
		enabledRedirect("gemini-client", "gemini-target"),
	}
	tests := []struct {
		name     string
		path     string
		protocol contract.ProtocolID
		upstream string
		wantBody string
	}{
		{
			name: "openai", path: "/v1/models", protocol: contract.ProtocolOpenAIModels,
			upstream: `{"object":"list","data":[{"id":"target-model","owned_by":"vendor"},{"id":"other-model"},{"id":"gemini-target"}]}`,
			wantBody: `{"object":"list","data":[` +
				`{"id":"client-model","object":"model","created":0,"owned_by":"system"},` +
				`{"id":"gemini-client","object":"model","created":0,"owned_by":"system"},` +
				`{"id":"gemini-target"},{"id":"other-model"},{"id":"target-model","owned_by":"vendor"},` +
				`{"id":"vendor/source","object":"model","created":0,"owned_by":"system"}` +
				`],"first_id":"client-model","has_more":false,"last_id":"vendor/source"}`,
		},
		{
			name: "gemini", path: "/v1beta/models", protocol: contract.ProtocolGoogleModels,
			upstream: `{"models":[{"name":"models/gemini-target"}]}`,
			wantBody: `{"models":[{"name":"models/gemini-client","displayName":"gemini-client"},{"name":"models/gemini-target"}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newRedirectSettingsStore(redirects...)
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
					if request.Model != "" {
						t.Errorf("discovery resolved model %q", request.Model)
					}
					return endpoint.Resolved{Endpoint: fixtureEndpoint(test.protocol, false, "https://upstream.example")}, nil
				}),
				RequestRecords: store,
				Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(test.upstream))}, nil
				})),
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK || response.Body.String() != test.wantBody {
				t.Fatalf("response = %d %s\nwant %s", response.Code, response.Body.String(), test.wantBody)
			}
			if !json.Valid(response.Body.Bytes()) {
				t.Fatal("discovery body is not JSON")
			}
			if record := store.root(t); record.ModelRedirect != nil {
				t.Fatalf("discovery recorded a redirect: %#v", record.ModelRedirect)
			}
		})
	}
}

func TestModelRedirectSharesOneRoutingSettingsRead(t *testing.T) {
	store := newRedirectSettingsStore(enabledRedirect("client-model", "target-model"))
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		RequestRecords: store,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
			writer.Header().Set("Content-Type", "application/json")
			_, err := writer.Write([]byte(`{}`))
			return err
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"client-model"}`)))
	store.mu.Lock()
	reads := store.reads
	store.mu.Unlock()
	if response.Code != http.StatusOK || reads != 1 {
		t.Fatalf("status=%d routing settings reads=%d", response.Code, reads)
	}
}

func TestModelRedirectSettingsReadFailureFailsClosed(t *testing.T) {
	t.Run("inference", func(t *testing.T) {
		store := newRedirectSettingsStore(enabledRedirect("client-model", "target-model"))
		store.readErr = errors.New("database is locked")
		var logs []string
		resolved, forwarded := false, false
		handler := NewWithDependencies(Dependencies{
			Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
				resolved = true
				return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
			}),
			RequestRecords: store,
			RecordLogger: func(format string, args ...any) {
				logs = append(logs, fmt.Sprintf(format, args...))
			},
			Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
				forwarded = true
				return nil
			}),
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"client-model"}`)))
		assertInferenceError(t, response, http.StatusServiceUnavailable, "endpoint_resolver_unavailable")
		if resolved || forwarded {
			t.Fatalf("unreadable redirects still routed: resolved=%t forwarded=%t", resolved, forwarded)
		}
		record := store.root(t)
		if record.Status != contract.RequestStatusFailed || record.ModelRedirect != nil || record.RequestedModel == nil || *record.RequestedModel != "client-model" {
			t.Fatalf("record status=%s redirect=%#v model=%v", record.Status, record.ModelRedirect, record.RequestedModel)
		}
		if len(logs) == 0 || logs[0] != "request record routing_settings_lookup failed: database is locked" {
			t.Fatalf("logs = %q", logs)
		}
	})
	t.Run("discovery", func(t *testing.T) {
		store := newRedirectSettingsStore(enabledRedirect("client-model", "target-model"))
		store.readErr = errors.New("database is locked")
		const upstream = `{"object":"list","data":[{"id":"target-model"}]}`
		handler := NewWithDependencies(Dependencies{
			Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
				return endpoint.Resolved{Endpoint: fixtureEndpoint(contract.ProtocolOpenAIModels, false, "https://upstream.example")}, nil
			}),
			RequestRecords: store,
			RecordLogger:   func(string, ...any) {},
			Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(upstream))}, nil
			})),
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "client-model") || !strings.Contains(response.Body.String(), "target-model") {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
	})
}
