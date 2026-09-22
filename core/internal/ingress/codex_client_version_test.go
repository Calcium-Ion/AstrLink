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

func TestCodexForwardingPrefersClientVersion(t *testing.T) {
	for _, identity := range []struct {
		name, version, userAgent, want string
	}{
		{"explicit older version", "0.100.0", "codex_cli_rs/0.156.0", "0.100.0"},
		{"Rust CLI version", "", "codex_cli_rs/0.156.0 (Mac OS; arm64)", "0.156.0"},
		{"older CLI version", "", "codex_cli_rs/0.99.0", "0.99.0"},
		{"fallback", "", "OpenAI/Python 1.0", accountauth.DefaultCodexModelsClientVersion},
	} {
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
					if strings.HasPrefix(route.path, "/v1/models") {
						want := identity.want
						if strings.Contains(route.path, "?") {
							want = "0.103.0"
						}
						if got := r.URL.Query().Get("client_version"); got != want {
							t.Errorf("models client_version = %q, want %q", got, want)
						}
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
					Authorizer: endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}),
				})
				body := fmt.Sprintf(`{"model":"gpt-6-astra","input":"hello","stream":%t}`, route.stream)
				request := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(body))
				if strings.HasPrefix(route.path, "/v1/models") {
					request = httptest.NewRequest(http.MethodGet, route.path, nil)
				}
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer local-token")
				request.Header.Set("version", identity.version)
				request.Header.Set("User-Agent", identity.userAgent)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("response = %d %s", response.Code, response.Body.String())
				}
				select {
				case headers := <-sawHeaders:
					for name, want := range map[string]string{
						"version": identity.want, "User-Agent": "codex-cli/" + identity.want,
						"Authorization": "Bearer subscription-token", "originator": "astrlink",
					} {
						if got := headers.Get(name); got != want {
							t.Errorf("%s = %q, want %q", name, got, want)
						}
					}
				default:
					t.Fatal("no upstream request")
				}
			})
		}
	}
}

func TestCodexWebSocketPrefersClientVersion(t *testing.T) {
	for _, explicit := range []string{"", "0.100.0"} {
		t.Run("version="+explicit, func(t *testing.T) {
			want := explicit
			if want == "" {
				want = "0.156.0"
			}
			upstream := wsUpstream(t, func(conn *websocket.Conn, request *http.Request) {
				if request.URL.Path != "/backend-api/codex/responses" ||
					request.Header.Get("version") != want || request.UserAgent() != "codex-cli/"+want {
					t.Errorf("upstream path=%q version=%q User-Agent=%q", request.URL.Path, request.Header.Get("version"), request.UserAgent())
				}
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
				Authorizer: endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}),
			})
			headers := make(http.Header)
			headers.Set("version", explicit)
			headers.Set("User-Agent", "codex_cli_rs/0.156.0 (Mac OS; arm64)")
			client := dialResponses(t, handler, headers)
			sendWS(t, client, `{"type":"response.create","model":"gpt-6-astra","input":"hello"}`)
			if event := readWS(t, client); event["type"] != "response.completed" {
				t.Fatalf("event = %#v", event)
			}
		})
	}
}
