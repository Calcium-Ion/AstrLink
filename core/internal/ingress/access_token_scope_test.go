package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// A token limited to selected providers reaches only them: routing skips the
// rest, a model only they serve is refused with 403, a provider named in
// X-AstrLink-Service outside the scope is refused, and model discovery lists
// only what the token can use.
func TestAccessTokenScopeLimitsRouting(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "token-scope.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, service := range []struct {
		id     string
		models []string
	}{
		{"service_li", []string{"public", "li-only"}},
		{"service_zhang", []string{"public"}},
	} {
		ep := validEndpoint(contract.ProtocolOpenAIChat, false)
		ep.ID, ep.Name, ep.Enabled, ep.Models = contract.ServiceID(service.id), "Name of "+service.id, true, service.models
		ep.BaseURL = "https://" + strings.TrimPrefix(service.id, "service_") + ".example"
		ep.Capabilities = append(ep.Capabilities, contract.Capability{Protocol: contract.ProtocolOpenAIModels, Mode: contract.CapabilityModeNative})
		if _, err := store.CreateEndpoint(ctx, ep, storage.CredentialMutation{Present: true, Secret: []byte("test-secret")}); err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := endpoint.NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	scopes := map[string]contract.AccessTokenServices{
		"astr_zhang": {Scope: contract.AccessTokenServicesSelected, ServiceIDs: []contract.ServiceID{"service_zhang"}},
		"astr_all":   contract.AllAccessTokenServices(),
	}
	var hosts []string
	handler := NewWithDependencies(Dependencies{
		Resolver: resolver, Authorizer: endpoint.NewSecretAuthorizer(store), RequestRecords: store, AuditBlobs: store,
		AccessTokenAuthenticator: ScopedAccessTokenAuthenticatorFunc(func(_ context.Context, raw string) (contract.AccessTokenID, contract.AccessTokenServices, error) {
			scope, ok := scopes[raw]
			if !ok {
				return "", contract.AccessTokenServices{}, errInvalidTestToken
			}
			return contract.AccessTokenID("access_token_" + strings.TrimPrefix(raw, "astr_")), scope, nil
		}),
		Forwarder: transport.New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			hosts = append(hosts, r.URL.Host)
			if strings.HasSuffix(r.URL.Path, "/models") {
				ids := `{"id":"public","object":"model"}`
				if r.URL.Host == "li.example" {
					ids += `,{"id":"li-only","object":"model"}`
				}
				return jsonResponse(200, `{"object":"list","data":[`+ids+`]}`), nil
			}
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "hi"}}}})
			return jsonResponse(200, string(raw)), nil
		})),
	})
	serve := func(token, method, path, pin, body string) *httptest.ResponseRecorder {
		t.Helper()
		hosts = nil
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		if pin != "" {
			request.Header.Set(ServiceHeader, pin)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	chat := func(model string) string {
		return `{"model":"` + model + `","messages":[{"role":"user","content":"hello"}]}`
	}

	// service_li ranks first, but the scoped token never reaches it.
	if response := serve("astr_zhang", "POST", "/v1/chat/completions", "", chat("public")); response.Code != 200 || strings.Join(hosts, ",") != "zhang.example" {
		t.Fatalf("scoped: status %d hosts %v body %s", response.Code, hosts, response.Body)
	}
	if response := serve("astr_all", "POST", "/v1/chat/completions", "", chat("li-only")); response.Code != 200 || strings.Join(hosts, ",") != "li.example" {
		t.Fatalf("unscoped: status %d hosts %v body %s", response.Code, hosts, response.Body)
	}

	for _, test := range []struct {
		name, pin, model, message string
	}{
		{"model outside scope", "", "li-only", `none of the API providers this access token may use serve model "li-only"`},
		{"pinned outside scope", "service_li", "public", "this access token may not use the API provider named in X-AstrLink-Service"},
	} {
		response := serve("astr_zhang", "POST", "/v1/chat/completions", test.pin, chat(test.model))
		body := response.Body.String()
		if response.Code != http.StatusForbidden || ojson.GetBytes(response.Body.Bytes(), "error.code").String() != "access_token_scope" ||
			ojson.GetBytes(response.Body.Bytes(), "error.message").String() != test.message || len(hosts) != 0 {
			t.Fatalf("%s: status %d hosts %v body %s", test.name, response.Code, hosts, body)
		}
		if strings.Contains(body, "Name of service_li") {
			t.Fatalf("%s: refusal names a provider outside the scope: %s", test.name, body)
		}
	}

	response := serve("astr_zhang", "GET", "/v1/models", "", "")
	if response.Code != 200 || strings.Contains(response.Body.String(), "li-only") || !strings.Contains(response.Body.String(), "public") {
		t.Fatalf("discovery: status %d hosts %v body %s", response.Code, hosts, response.Body)
	}
	for _, host := range hosts {
		if host != "zhang.example" {
			t.Fatalf("discovery asked %s for the scoped token", host)
		}
	}
}

type invalidTestTokenError struct{}

func (invalidTestTokenError) Error() string { return "invalid token" }

var errInvalidTestToken error = invalidTestTokenError{}
