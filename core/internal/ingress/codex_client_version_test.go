package ingress

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/gorilla/websocket"
)

func codexVersionCandidate(baseURL string) endpoint.Resolved {
	return endpoint.Resolved{
		BaseURL: baseURL,
		Service: contract.Service{
			ID: "service_codex", Name: "Codex", Kind: contract.ServiceKindCodexSubscription,
			Enabled: true, Models: []string{"gpt-6-astra"}, Capabilities: contract.DefaultOpenAICodexCapabilities(),
			Subscription: &contract.SubscriptionConnection{
				Provider: contract.SubscriptionProviderOpenAICodex, Status: contract.SubscriptionStatusConnected,
				CredentialRef: accountauth.CredentialRefFor("service_codex"),
			},
		},
	}
}

type codexIdentityTestCase struct {
	name, userAgent, version            string
	policy                              accountauth.CodexIdentityPolicy
	wantUA, wantOriginator, wantVersion string
}

func codexIdentityCases() []codexIdentityTestCase {
	const clientUA = "codex_cli_rs/0.156.0 (Mac OS; arm64)"
	return []codexIdentityTestCase{
		{name: "default enforced", userAgent: clientUA, version: "0.100.0", wantUA: accountauth.CodexUserAgent(""), wantOriginator: "codex-tui", wantVersion: accountauth.DefaultCodexModelsClientVersion},
		{name: "configured version", userAgent: clientUA, policy: accountauth.CodexIdentityPolicy{ClientVersion: "0.157.0"}, wantUA: accountauth.CodexUserAgent("0.157.0"), wantOriginator: "codex-tui", wantVersion: "0.157.0"},
		{name: "disabled paired client", userAgent: clientUA, version: "0.100.0", policy: accountauth.CodexIdentityPolicy{DisableEnforcement: true}, wantUA: clientUA, wantOriginator: "codex_cli_rs", wantVersion: "0.156.0"},
		{name: "disabled old client fallback", userAgent: "codex_cli_rs/0.99.0", policy: accountauth.CodexIdentityPolicy{DisableEnforcement: true}, wantUA: accountauth.CodexUserAgent(""), wantOriginator: "codex-tui", wantVersion: accountauth.DefaultCodexModelsClientVersion},
		{name: "disabled unrelated client fallback", userAgent: "astrlink/0.1", policy: accountauth.CodexIdentityPolicy{DisableEnforcement: true}, wantUA: accountauth.CodexUserAgent(""), wantOriginator: "codex-tui", wantVersion: accountauth.DefaultCodexModelsClientVersion},
	}
}

func (test codexIdentityTestCase) clientHeaders() http.Header {
	headers := make(http.Header)
	headers.Set("User-Agent", test.userAgent)
	headers.Set("version", test.version)
	headers.Set("originator", "astrlink")
	headers.Set("Authorization", "Bearer local-token")
	headers.Set("ChatGPT-Account-ID", "client-account")
	headers.Set("X-Api-Key", "local-key")
	headers.Set("Cookie", "session=local-secret")
	headers.Set("X-Client-Feature", "preserved")
	headers.Set("X-AstrLink-Debug", "local-only")
	return headers
}

func (test codexIdentityTestCase) checkUpstream(t *testing.T, headers http.Header) {
	t.Helper()
	for name, want := range map[string]string{
		"version": test.wantVersion, "User-Agent": test.wantUA, "originator": test.wantOriginator,
		"Authorization": "Bearer subscription-token", "ChatGPT-Account-ID": "",
		"Cookie": "", "X-Api-Key": "", "X-Client-Feature": "preserved",
		"X-AstrLink-Debug": "",
	} {
		if got := headers.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestCodexForwardingIdentityAcrossHTTPPaths(t *testing.T) {
	for _, identity := range codexIdentityCases() {
		for _, route := range []struct {
			path, response string
			stream         bool
		}{
			{"/v1/responses", `{"id":"resp_test","object":"response","output":[]}`, false},
			{"/v1/responses", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"output\":[]}}\n\n", true},
			{"/v1/responses/compact", `{"id":"resp_test","object":"response.compaction","output":[]}`, false},
			{"/v1/models", `{"models":[{"slug":"gpt-6-astra","visibility":"list"}]}`, false},
			{"/v1/models?client_version=0.103.0", `{"models":[{"slug":"gpt-6-astra","visibility":"list"}]}`, false},
		} {
			t.Run(fmt.Sprintf("%s/%s/stream=%t", identity.name, route.path, route.stream), func(t *testing.T) {
				sawHeaders := make(chan http.Header, 1)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sawHeaders <- r.Header.Clone()
					wantPath := "/backend-api/codex" + strings.TrimPrefix(strings.Split(route.path, "?")[0], "/v1")
					if r.URL.Path != wantPath {
						t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
					}
					if strings.HasPrefix(route.path, "/v1/models") && r.URL.Query().Get("client_version") != identity.wantVersion {
						t.Errorf("catalog version does not match identity: %q", r.URL.Query().Get("client_version"))
					}
					w.Header().Set("Content-Type", "application/json")
					if route.stream {
						w.Header().Set("Content-Type", "text/event-stream")
					}
					_, _ = w.Write([]byte(route.response))
				}))
				defer upstream.Close()
				handler := NewWithDependencies(Dependencies{
					Resolver:   candidateResolver{candidates: []endpoint.Resolved{codexVersionCandidate(upstream.URL + "/backend-api/codex")}},
					Authorizer: endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, identity.policy),
				})
				body := fmt.Sprintf(`{"model":"gpt-6-astra","input":"hello","stream":%t}`, route.stream)
				request := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(body))
				if strings.HasPrefix(route.path, "/v1/models") {
					request = httptest.NewRequest(http.MethodGet, route.path, nil)
				}
				request.Header = identity.clientHeaders()
				request.Header.Set("Content-Type", "application/json")
				accept := "application/json"
				if route.stream {
					accept = "text/event-stream"
				}
				request.Header.Set("Accept", accept)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("response = %d %s", response.Code, response.Body.String())
				}
				select {
				case headers := <-sawHeaders:
					identity.checkUpstream(t, headers)
					// HTTP Responses always stream upstream on a subscription.
					wantAccept := accept
					if route.path == "/v1/responses" {
						wantAccept = "text/event-stream"
					}
					if got := headers.Get("Accept"); got != wantAccept {
						t.Errorf("Accept = %q, want %q", got, wantAccept)
					}
				default:
					t.Fatal("no upstream request")
				}
			})
		}
	}
}

func TestCodexWebSocketIdentityPolicy(t *testing.T) {
	for _, identity := range codexIdentityCases() {
		t.Run(identity.name, func(t *testing.T) {
			upstream := wsUpstream(t, func(conn *websocket.Conn, request *http.Request) {
				if request.URL.Path != "/backend-api/codex/responses" {
					t.Errorf("upstream path = %q", request.URL.Path)
				}
				identity.checkUpstream(t, request.Header)
				var event map[string]any
				if err := conn.ReadJSON(&event); err != nil {
					t.Error(err)
					return
				}
				_ = conn.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_test", "status": "completed", "output": []any{}}})
			})
			candidate := codexVersionCandidate(upstream.URL + "/backend-api/codex")
			enabled := true
			candidate.Service.ResponsesWebSocketEnabled = &enabled
			handler := NewWithDependencies(Dependencies{
				Resolver:   candidateResolver{candidates: []endpoint.Resolved{candidate}},
				Authorizer: endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, identity.policy),
			})
			client := dialResponses(t, handler, identity.clientHeaders())
			sendWS(t, client, `{"type":"response.create","model":"gpt-6-astra","input":"hello"}`)
			if event := readWS(t, client); event["type"] != "response.completed" {
				t.Fatalf("event = %#v", event)
			}
		})
	}
}
