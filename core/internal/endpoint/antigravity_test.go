package endpoint

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/providerapi"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestAntigravityAuthorizedForwarding(t *testing.T) {
	const payload = `{"contents":[{"role":"user","parts":[{"text":"Explain AstrLink"}]}]}`
	const gemini = `{"candidates":[{"content":{"parts":[{"text":"reply"}]}}]}`
	const catalog = `{"models":{"gemini-test":{"displayName":"Gemini"},"chat_20706":{},"tab_flash_lite_preview":{},"gemini-2.5-flash-thinking":{},"gemini-2.5-pro":{}}}`
	for _, tt := range []struct {
		name, method, path, upstreamPath, response, contentType, want string
		status                                                        int
	}{
		{"json", "POST", "/v1beta/models/gemini-test:generateContent", "/v1internal:generateContent",
			`{"response":` + gemini + `}`, "application/json", gemini, 200},
		{"stream", "POST", "/v1beta/models/gemini-test:streamGenerateContent?alt=sse", "/v1internal:streamGenerateContent",
			"data: {\"response\":" + gemini + "}\n\ndata: [DONE]\n\n", "text/event-stream", "data: " + gemini + "\n\n", 200},
		{"openai models", "GET", "/v1/models", "/v1internal:fetchAvailableModels",
			catalog, "application/json", `"id":"gemini-test"`, 200},
		{"google models", "GET", "/v1beta/models", "/v1internal:fetchAvailableModels",
			catalog, "application/json", `"name":"models/gemini-test"`, 200},
		{"upstream failure", "POST", "/v1beta/models/gemini-test:generateContent", "/v1internal:generateContent",
			`{"error":{"code":429}}`, "application/json", `{"error":{"code":429}}`, 429},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != tt.upstreamPath {
					t.Errorf("unexpected upstream method/path: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-access" ||
					r.UserAgent() != accountauth.AntigravityUserAgent() {
					t.Error("missing Antigravity authentication or identity")
				}
				for key := range r.Header {
					if strings.HasPrefix(strings.ToLower(key), "x-astrlink-") {
						t.Errorf("local header reached upstream: %s", key)
					}
				}
				for _, key := range []string{"Originator", "Via", "X-Powered-By", "X-Goog-Api-Key", "Cookie"} {
					if r.Header.Get(key) != "" {
						t.Errorf("unexpected forwarded header: %s", key)
					}
				}
				var body struct {
					Project   string          `json:"project"`
					Model     string          `json:"model"`
					Request   json.RawMessage `json:"request"`
					RequestID string          `json:"requestId"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Project != "test-project" {
					t.Error("missing trusted project")
				}
				if tt.method == "POST" {
					if body.Model != "gemini-test" || !strings.Contains(string(body.Request), "Explain AstrLink") ||
						!strings.HasPrefix(body.RequestID, "agent-") {
						t.Error("incorrect envelope or lost caller content")
					}
				}
				if tt.name == "stream" && r.URL.Query().Get("alt") != "sse" {
					t.Error("missing SSE query")
				}
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.response)
			}))
			defer upstream.Close()
			service := contract.Service{ID: "service_antigravity", Kind: contract.ServiceKindAntigravitySubscription}
			authorizer := NewServiceAuthorizer(nil, &fakeSubscriptionTokenSource{tokens: accountauth.AccountTokens{
				AccessToken: "test-access", ProjectID: "test-project",
			}})
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(payload))
			for _, key := range []string{"Authorization", "Originator", "Via", "X-Powered-By", "X-Goog-Api-Key", "Cookie", providerapi.AntigravityProjectHeader} {
				request.Header.Set(key, "untrusted")
			}
			headers, err := authorizer.Headers(context.Background(), contract.Endpoint{
				ID: service.ID, Kind: service.Kind,
			}, request.Header)
			if err != nil {
				t.Fatal(err)
			}
			baseURL, _ := url.Parse(upstream.URL)
			response, err := transport.New(upstream.Client().Transport).RoundTrip(request, transport.Target{
				Service: service, BaseURL: baseURL, RequestHeaders: headers,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != tt.status || !strings.Contains(string(body), tt.want) {
				t.Fatalf("response status=%d body=%s err=%v", response.StatusCode, body, err)
			}
			if tt.method == "GET" {
				for _, hidden := range []string{"chat_20706", "tab_flash_lite_preview"} {
					if strings.Contains(string(body), hidden) {
						t.Errorf("model list includes non-chat model %q", hidden)
					}
				}
				for _, model := range []string{"gemini-2.5-flash-thinking", "gemini-2.5-pro"} {
					if !strings.Contains(string(body), model) {
						t.Errorf("model list dropped %q", model)
					}
				}
			}
		})
	}
}
