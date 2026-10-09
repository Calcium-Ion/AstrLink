package ingress

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/builtintools"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

const (
	codexTestTurn    = "turn-1"
	codexTestSession = "session-1"
	codexTestUA      = "codex_cli_rs/0.162.0 (Mac OS 27.0.0; arm64) dumb (codex_cli_rs; 0.162.0)"
)

// codexToolResolver serves routing and ID lookups from the same providers.
type codexToolResolver struct {
	candidateResolver
}

func (resolver codexToolResolver) ResolveService(_ context.Context, id contract.ServiceID) (endpoint.Resolved, error) {
	for _, candidate := range resolver.candidates {
		if candidate.CanonicalService().ID == id {
			return candidate, nil
		}
	}
	return endpoint.Resolved{}, endpoint.ErrNoEndpoint
}

type codexUpstreamCall struct {
	path   string
	header http.Header
	body   builtintools.Object
}

type codexToolUpstream struct {
	t           *testing.T
	mu          sync.Mutex
	calls       []codexUpstreamCall
	imageStatus int
	searchBody  string
}

func (upstream *codexToolUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body builtintools.Object
	_ = json.Unmarshal(raw, &body)
	upstream.mu.Lock()
	upstream.calls = append(upstream.calls, codexUpstreamCall{path: r.URL.Path, header: r.Header.Clone(), body: body})
	imageStatus, searchBody := upstream.imageStatus, upstream.searchBody
	upstream.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/responses"):
		writeResponsesSSE(w, builtintools.String(body["model"]), builtinMessage("ok"))
	case strings.HasSuffix(r.URL.Path, "/images/generations"):
		if imageStatus != 0 {
			w.WriteHeader(imageStatus)
			_, _ = io.WriteString(w, `{"error":{"message":"upstream image failure"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"aW1hZ2U=","generation_id":"gen_1"}]}`)
	case strings.HasSuffix(r.URL.Path, "/alpha/search"):
		w.Header().Set("Content-Type", "application/json")
		if searchBody == "" {
			searchBody = `{"encrypted_output":null,"output":"native search result","results":[{"type":"page"}]}`
		}
		_, _ = io.WriteString(w, searchBody)
	default:
		upstream.t.Errorf("unexpected upstream path %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (upstream *codexToolUpstream) last(t *testing.T, suffix string) codexUpstreamCall {
	t.Helper()
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	var found []codexUpstreamCall
	for _, call := range upstream.calls {
		if strings.HasSuffix(call.path, suffix) {
			found = append(found, call)
		}
	}
	if len(found) != 1 {
		t.Fatalf("upstream %s calls = %d", suffix, len(found))
	}
	return found[0]
}

func codexToolSubscription(baseURL string) endpoint.Resolved {
	candidate := codexVersionCandidate(baseURL + "/backend-api/codex")
	candidate.Service.Models = []string{"gpt-5.6-sol", "gpt-image-2", "gpt-image-2-hd"}
	return candidate
}

func codexClientRequest(path, body string, official bool) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Openai-Actor-Authorization", "codex-imagegen")
	if official {
		request.Header.Set("User-Agent", codexTestUA)
		request.Header.Set("originator", "codex_cli_rs")
	}
	return request
}

// codexTurn sends one conversation turn the way Codex does over HTTP.
func codexTurn(t *testing.T, handler http.Handler, official bool) {
	t.Helper()
	request := codexClientRequest("/v1/responses", `{"model":"gpt-5.6-sol","input":"draw a fox","stream":true,"client_metadata":{"turn_id":"`+codexTestTurn+`","session_id":"`+codexTestSession+`"}}`, official)
	request.Header.Set("Accept", "text/event-stream")
	if official {
		request.Header.Set("Session-Id", codexTestSession)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("turn = %d %s", response.Code, response.Body.String())
	}
}

func TestCodexToolRequestsReachTheTurnsSubscription(t *testing.T) {
	for _, official := range []bool{true, false} {
		name := "third-party"
		if official {
			name = "official"
		}
		t.Run(name, func(t *testing.T) {
			upstream := &codexToolUpstream{t: t}
			server := httptest.NewServer(upstream)
			defer server.Close()
			store := newRedirectSettingsStore(contract.ModelRedirect{From: "gpt-image-2", To: "gpt-image-2-hd", Enabled: true})
			store.settings.BuiltinTools = &contract.BuiltinTools{}
			handler := NewWithDependencies(Dependencies{
				Resolver:       codexToolResolver{candidateResolver{candidates: []endpoint.Resolved{codexToolSubscription(server.URL)}}},
				Authorizer:     endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
				RequestRecords: store,
			})
			codexTurn(t, handler, official)

			image := codexClientRequest("/v1/images/generations", `{"prompt":"a red fox","background":"opaque","model":"gpt-image-2","quality":"auto","size":"auto"}`, official)
			image.Header.Set("X-Codex-Image-Turn-Id", codexTestTurn)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, image)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"generation_id":"gen_1"`) {
				t.Fatalf("image = %d %s", response.Code, response.Body.String())
			}
			call := upstream.last(t, "/images/generations")
			if call.path != "/backend-api/codex/images/generations" || call.body["model"] != "gpt-image-2-hd" || call.body["prompt"] != "a red fox" {
				t.Fatalf("image upstream = %s %v", call.path, call.body)
			}
			search := codexClientRequest("/v1/alpha/search", `{"id":"`+codexTestSession+`","model":"gpt-5.6-sol","input":[],"commands":{"search_query":[{"q":"fox"}]}}`, official)
			search.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+codexTestSession+`","turn_id":"`+codexTestTurn+`"}`)
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, search)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "native search result") {
				t.Fatalf("search = %d %s", response.Code, response.Body.String())
			}
			searchCall := upstream.last(t, "/alpha/search")
			if searchCall.path != "/backend-api/codex/alpha/search" || searchCall.body["model"] != "gpt-5.6-sol" {
				t.Fatalf("search upstream = %s %v", searchCall.path, searchCall.body)
			}
			// The search names the session as the turn's account saw it.
			if id := builtintools.String(searchCall.body["id"]); id == codexTestSession || id == "" {
				t.Fatalf("search session id was not scoped to the account: %q", id)
			}
			turn := upstream.last(t, "/responses")
			for _, call := range []codexUpstreamCall{call, searchCall} {
				if call.header.Get("Authorization") != "Bearer subscription-token" {
					t.Fatalf("tool request Authorization = %q", call.header.Get("Authorization"))
				}
				for name := range call.header {
					if strings.EqualFold(name, "X-Openai-Actor-Authorization") || strings.Contains(strings.ToLower(name), "astrlink") {
						t.Fatalf("tool request carried %s", name)
					}
				}
				// Tool requests present the identity their turn was sent with.
				for _, name := range []string{"User-Agent", "originator"} {
					if call.header.Get(name) != turn.header.Get(name) {
						t.Fatalf("%s = %q, turn sent %q", name, call.header.Get(name), turn.header.Get(name))
					}
				}
			}
			if official && turn.header.Get("User-Agent") != codexTestUA {
				t.Fatalf("official turn User-Agent = %q", turn.header.Get("User-Agent"))
			}
			if !official && turn.header.Get("User-Agent") == codexTestUA {
				t.Fatal("third-party turn kept the client's User-Agent")
			}
			protocols := map[contract.ProtocolID]contract.RequestStatus{}
			for _, record := range store.snapshot() {
				protocols[record.InputProtocol] = record.Status
				if record.InputProtocol == contract.ProtocolOpenAIImages && (record.ServiceID == nil || *record.ServiceID != "service_codex") {
					t.Fatalf("image record service = %v", record.ServiceID)
				}
			}
			if protocols[contract.ProtocolOpenAIImages] != contract.RequestStatusSucceeded || protocols[contract.ProtocolOpenAISearch] != contract.RequestStatusSucceeded {
				t.Fatalf("records = %v", protocols)
			}
		})
	}
}

func TestCodexToolRequestsWithoutAProviderForTheirTurn(t *testing.T) {
	upstream := &codexToolUpstream{t: t, imageStatus: http.StatusBadGateway}
	server := httptest.NewServer(upstream)
	defer server.Close()
	// An API key provider without an Images API serves the turn.
	plain := wsCandidate(server.URL)
	plain.Service.ID = "service_plain"
	plain.Service.Kind = contract.ServiceKindAnthropic
	plain.Service.ResponsesWebSocketEnabled = nil
	plain.Service.Models = []string{"gpt-5.6-sol"}
	store := newRedirectSettingsStore()
	store.settings.BuiltinTools = &contract.BuiltinTools{}
	handler := NewWithDependencies(Dependencies{
		Resolver:       codexToolResolver{candidateResolver{candidates: []endpoint.Resolved{plain, codexToolSubscription(server.URL)}}},
		Authorizer:     endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
		RequestRecords: store,
	})
	codexTurn(t, handler, true)
	for _, path := range []string{"/v1/images/generations", "/v1/alpha/search"} {
		request := codexClientRequest(path, `{"id":"`+codexTestSession+`","prompt":"fox","model":"gpt-image-2","commands":{"search_query":[{"q":"fox"}]}}`, true)
		request.Header.Set("X-Codex-Image-Turn-Id", codexTestTurn)
		request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+codexTestSession+`","turn_id":"`+codexTestTurn+`"}`)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if path == "/v1/alpha/search" {
			if output := searchOutput(t, response); !strings.Contains(output, "Web search is turned off") {
				t.Fatalf("search output = %q", output)
			}
		} else {
			assertInferenceError(t, response, http.StatusUnprocessableEntity, "image_generation_disabled")
		}
	}
	// Neither request reached the subscription that did not serve the turn.
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	for _, call := range upstream.calls {
		if !strings.HasSuffix(call.path, "/responses") {
			t.Fatalf("tool request reached %s", call.path)
		}
	}
}

func TestCodexSubscriptionImageFailureIsNotRetryable(t *testing.T) {
	upstream := &codexToolUpstream{t: t, imageStatus: http.StatusServiceUnavailable, searchBody: `<html>maintenance</html>`}
	server := httptest.NewServer(upstream)
	defer server.Close()
	store := newRedirectSettingsStore()
	store.settings.BuiltinTools = &contract.BuiltinTools{}
	handler := NewWithDependencies(Dependencies{
		Resolver:       codexToolResolver{candidateResolver{candidates: []endpoint.Resolved{codexToolSubscription(server.URL)}}},
		Authorizer:     endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
		RequestRecords: store,
	})
	codexTurn(t, handler, true)
	image := codexClientRequest("/v1/images/generations", `{"prompt":"fox","model":"gpt-image-2"}`, true)
	image.Header.Set("X-Codex-Image-Turn-Id", codexTestTurn)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, image)
	// Codex retries any 5xx, and each retry can bill another image.
	assertInferenceError(t, response, http.StatusFailedDependency, "image_generation_failed")
	upstream.last(t, "/images/generations")

	search := codexClientRequest("/v1/alpha/search", `{"id":"`+codexTestSession+`","model":"gpt-5.6-sol","commands":{"search_query":[{"q":"fox"}]}}`, true)
	search.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+codexTestSession+`"}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, search)
	// An answer Codex cannot parse would end its turn.
	if output := searchOutput(t, response); !strings.Contains(output, "invalid search answer") {
		t.Fatalf("search output = %q", output)
	}
	for _, record := range store.snapshot() {
		if record.InputProtocol != contract.ProtocolOpenAIResponses && record.Status != contract.RequestStatusFailed {
			t.Fatalf("%s record = %s", record.InputProtocol, record.Status)
		}
	}
}

func TestCodexImageRequestsReachTheTurnsImagesAPI(t *testing.T) {
	jpegImage := encodedTestImage(t, "jpeg")
	var mu sync.Mutex
	var imageBodies []builtintools.Object
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body builtintools.Object
		_ = json.Unmarshal(raw, &body)
		switch r.URL.Path {
		case "/v1/responses":
			writeResponsesSSE(w, builtintools.String(body["model"]), builtinMessage("ok"))
		case "/v1/images/generations":
			mu.Lock()
			imageBodies = append(imageBodies, body)
			mu.Unlock()
			if r.Header.Get("Authorization") != "Bearer plan-key" || r.Header.Get("X-Openai-Actor-Authorization") != "" {
				t.Errorf("image headers = %v", r.Header)
			}
			_, _ = io.WriteString(w, builtintools.Text(builtintools.Object{"data": []any{builtintools.Object{"b64_json": jpegImage}}}))
		default:
			t.Errorf("unexpected upstream path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	openai := wsCandidate(server.URL)
	openai.Service.ID = "service_openai"
	openai.Service.ResponsesWebSocketEnabled = nil
	openai.Service.Models = []string{"gpt-5.6-sol"}
	openai.Service.HTTP.Auth = contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}
	openai.Service.HTTP.CredentialRef = "local://service/service_openai"
	store := newRedirectSettingsStore()
	store.settings.BuiltinTools = &contract.BuiltinTools{}
	handler := NewWithDependencies(Dependencies{
		Resolver:       codexToolResolver{candidateResolver{candidates: []endpoint.Resolved{openai}}},
		Authorizer:     endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}),
		RequestRecords: store,
	})
	codexTurn(t, handler, true)
	image := codexClientRequest("/v1/images/generations", `{"prompt":"a red fox","model":"gpt-image-2","quality":"auto","size":"auto"}`, true)
	image.Header.Set("X-Codex-Image-Turn-Id", codexTestTurn)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, image)
	var answer struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil || len(answer.Data) != 1 {
		t.Fatalf("image = %d %s", response.Code, response.Body.String())
	}
	// The provider's JPEG reaches Codex as the PNG it saves.
	if data, err := base64.StdEncoding.DecodeString(answer.Data[0].B64JSON); err != nil || !bytes.HasPrefix(data, pngSignature) {
		t.Fatalf("image answer is not PNG: %v", err)
	}
	if len(imageBodies) != 1 || imageBodies[0]["model"] != "gpt-image-2" || imageBodies[0]["prompt"] != "a red fox" {
		t.Fatalf("image upstream bodies = %v", imageBodies)
	}
	// An API key provider has no Codex search endpoint.
	search := codexClientRequest("/v1/alpha/search", `{"id":"`+codexTestSession+`","model":"gpt-5.6-sol","commands":{"search_query":[{"q":"fox"}]}}`, true)
	search.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+codexTestSession+`","turn_id":"`+codexTestTurn+`"}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, search)
	if output := searchOutput(t, response); !strings.Contains(output, "Web search is turned off") {
		t.Fatalf("search output = %q", output)
	}
}

func TestCodexTurnStoreKeepsTurnsPerTokenAndExpires(t *testing.T) {
	var store codexTurnStore
	now := time.Now()
	ref := codexTurnRef{turnID: "turn", sessionID: "session"}
	store.note("token_a", ref, codexTurnBinding{service: "service_a", at: now})
	if binding, ok := store.lookup("token_a", codexTurnRef{turnID: "turn"}, now); !ok || binding.service != "service_a" {
		t.Fatalf("turn lookup = %v %v", binding, ok)
	}
	if binding, ok := store.lookup("token_a", codexTurnRef{sessionID: "session"}, now); !ok || binding.service != "service_a" {
		t.Fatalf("session lookup = %v %v", binding, ok)
	}
	if _, ok := store.lookup("token_b", ref, now); ok {
		t.Fatal("another access token found the turn")
	}
	if _, ok := store.lookup("token_a", ref, now.Add(codexTurnTTL)); ok {
		t.Fatal("an expired turn was found")
	}
	for index := range codexTurnLimit + 10 {
		store.note("token_a", codexTurnRef{turnID: fmt.Sprint(index)}, codexTurnBinding{service: "service_a", at: now.Add(time.Duration(index) * time.Millisecond)})
	}
	if len(store.entries) > codexTurnLimit {
		t.Fatalf("store holds %d entries", len(store.entries))
	}
	if _, ok := store.lookup("token_a", codexTurnRef{turnID: fmt.Sprint(codexTurnLimit + 9)}, now); !ok {
		t.Fatal("the newest turn was evicted")
	}
}

// A turn whose hosted web_search the gateway maps is served by the mapping's
// own model calls; the image request still finds the subscription.
func TestCodexImageFindsATurnServedThroughToolMapping(t *testing.T) {
	upstream := &codexToolUpstream{t: t}
	server := httptest.NewServer(upstream)
	defer server.Close()
	tavily := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	defer tavily.Close()
	store := newRedirectSettingsStore()
	store.settings.BuiltinTools = &contract.BuiltinTools{WebSearch: contract.BuiltinTool{Enabled: true, Backend: "external", BaseURL: tavily.URL}}
	handler := NewWithDependencies(Dependencies{
		Resolver:         codexToolResolver{candidateResolver{candidates: []endpoint.Resolved{codexToolSubscription(server.URL)}}},
		Authorizer:       endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
		RequestRecords:   store,
		ProxyCredentials: builtinToolSecrets{},
	})
	turn := codexClientRequest("/v1/responses", `{"model":"gpt-5.6-sol","input":"draw a fox","stream":true,"tools":[{"type":"web_search"}],"client_metadata":{"turn_id":"`+codexTestTurn+`","session_id":"`+codexTestSession+`"}}`, true)
	turn.Header.Set("Session-Id", codexTestSession)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, turn)
	if response.Code != http.StatusOK {
		t.Fatalf("turn = %d %s", response.Code, response.Body.String())
	}
	if call := upstream.last(t, "/responses"); builtintools.String(builtintools.Map(builtintools.Array(call.body["tools"])[0])["type"]) != "function" {
		t.Fatalf("the hosted search was not mapped: %v", call.body["tools"])
	}
	image := codexClientRequest("/v1/images/generations", `{"prompt":"a red fox","model":"gpt-image-2"}`, true)
	image.Header.Set("X-Codex-Image-Turn-Id", codexTestTurn)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, image)
	if response.Code != http.StatusOK {
		t.Fatalf("image = %d %s", response.Code, response.Body.String())
	}
	upstream.last(t, "/images/generations")
}
