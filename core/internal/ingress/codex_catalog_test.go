package ingress

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestCodexCatalogRequestNegotiation(t *testing.T) {
	for _, test := range []struct {
		name, path, header, value string
		protocol                  contract.ProtocolID
		want                      bool
	}{
		{"version query", "/v1/models?client_version=0.158.0", "", "", contract.ProtocolOpenAIModels, true},
		{"CLI", "/v1/models", "User-Agent", "codex_cli_rs/0.158.0 (Mac OS; arm64)", contract.ProtocolOpenAIModels, true},
		{"desktop", "/v1/models", "Originator", "codex_app", contract.ProtocolOpenAIModels, true},
		{"ordinary client", "/v1/models", "User-Agent", "openai-python/2.0", contract.ProtocolOpenAIModels, false},
		{"empty version", "/v1/models?client_version=%20", "", "", contract.ProtocolOpenAIModels, false},
		{"not a Codex product", "/v1/models", "User-Agent", "not-codex_cli_rs/1", contract.ProtocolOpenAIModels, false},
		{"Gemini stays Gemini", "/v1beta/models?client_version=0.158.0", "Originator", "codex_app", contract.ProtocolGoogleModels, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.header != "" {
				request.Header.Set(test.header, test.value)
			}
			if got := wantsCodexModelCatalog(request, test.protocol); got != test.want {
				t.Fatalf("catalog negotiation = %t, want %t", got, test.want)
			}
		})
	}
}

func codexCatalogTestHandler(t *testing.T) *Handler {
	t.Helper()
	codex := codexVersionCandidate("https://codex.example/backend-api/codex")
	codex.Service.Models = []string{"gpt-model", "codex-auto-review"}
	codex.Service.ModelRedirects = []contract.ModelRedirect{enabledRedirect("service-alias", "gpt-model")}
	kimi := discoveryEndpoint("kimi", contract.ProtocolOpenAIModels, contract.CapabilityModeNative)
	kimi.Models = []string{"kimi-for-coding", "manual-model", "gpt-model", contract.AstrLinkAutoModelID}
	return NewWithDependencies(Dependencies{
		Resolver:   candidateResolver{candidates: []endpoint.Resolved{codex, {Endpoint: kimi}}},
		Authorizer: endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
		RequestRecords: newRedirectSettingsStore(
			enabledRedirect("coding-alias", "gpt-model"),
			enabledRedirect("kimi-alias", "kimi-for-coding"),
			enabledRedirect("missing-alias", "missing-model"),
		),
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("X-AstrLink-Debug") != "" || request.Header.Get(contract.CodexUpstreamCatalogHeader) != "" {
				t.Error("gateway-owned header reached upstream")
			}
			body := `{"data":[{"id":"kimi-for-coding"},{"id":"gpt-model"},{"id":"blocked-model"},{"id":"astrlink/auto"}]}`
			if request.URL.Host == "codex.example" {
				body = `{"models":[{
					"slug":"gpt-model","display_name":"Upstream GPT","description":"Upstream description",
					"visibility":"list","priority":7,"supported_in_api":false,
					"default_reasoning_level":"high",
					"supported_reasoning_levels":[{"effort":"high","description":"Think carefully"}],
					"base_instructions":"Keep the original instructions mentioning AstrLink.",
					"context_window":64000,"input_modalities":["text","image"],
					"supports_parallel_tool_calls":true,"supports_reasoning_summaries":true,
					"model_messages":{"instructions_template":"Original template"},
					"future_capability":{"enabled":true}
				},{"slug":"hidden-model","visibility":"hide"},{"slug":"blocked-model","visibility":"list"}]}`
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(body)),
			}, nil
		})),
	})
}

func TestCodexCatalogAggregatesCapabilitiesAndRedirects(t *testing.T) {
	handler := codexCatalogTestHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.158.0", nil)
	request.Header.Set("X-AstrLink-Debug", "local-only")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]map[string]json.RawMessage)
	var ids []string
	for _, model := range catalog.Models {
		var id string
		if err := json.Unmarshal(model["slug"], &id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		byID[id] = model
	}
	wantIDs := []string{"codex-auto-review", "coding-alias", "gpt-model", "kimi-alias", "kimi-for-coding", "manual-model"}
	if !slices.Equal(ids, wantIDs) {
		t.Fatalf("catalog IDs = %v, want %v", ids, wantIDs)
	}
	for key, want := range map[string]string{
		"display_name": `"Upstream GPT"`, "context_window": "64000", "priority": "7",
		"supported_in_api": "false", "supports_parallel_tool_calls": "true",
		"base_instructions": `"Keep the original instructions mentioning AstrLink."`,
		"input_modalities":  `["text","image"]`, "future_capability": `{"enabled":true}`,
		"model_messages": `{"instructions_template":"Original template"}`,
	} {
		if got := string(byID["gpt-model"][key]); got != want {
			t.Errorf("upstream %s = %s, want %s", key, got, want)
		}
		if key != "display_name" && string(byID["coding-alias"][key]) != want {
			t.Errorf("redirect did not inherit %s: %s", key, byID["coding-alias"][key])
		}
	}
	if string(byID["coding-alias"]["display_name"]) != `"coding-alias"` {
		t.Error("redirect source did not keep its public display name")
	}
	for _, id := range []string{"kimi-for-coding", "kimi-alias", "manual-model"} {
		for key, want := range map[string]string{
			"visibility": `"list"`, "supported_in_api": "true", "input_modalities": `["text"]`,
			"supports_parallel_tool_calls": "false", "supports_reasoning_summaries": "false",
			"default_reasoning_level": "null", "supported_reasoning_levels": "[]",
			"experimental_supported_tools": "[]", "apply_patch_tool_type": "null",
		} {
			if got := string(byID[id][key]); got != want {
				t.Errorf("%s %s = %s, want %s", id, key, got, want)
			}
		}
		if _, guessed := byID[id]["context_window"]; guessed {
			t.Errorf("guessed context limit for %s", id)
		}
	}
	if string(byID["codex-auto-review"]["visibility"]) != `"hide"` {
		t.Error("synthetic internal auto-review model must not appear in the picker")
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Error("model catalog must not be cached by HTTP intermediaries")
	}
	repeated := httptest.NewRecorder()
	handler.ServeHTTP(repeated, request)
	if repeated.Body.String() != response.Body.String() {
		t.Error("catalog changed without any upstream change")
	}
	ordinary := httptest.NewRecorder()
	handler.ServeHTTP(ordinary, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	var standard map[string]json.RawMessage
	if err := json.Unmarshal(ordinary.Body.Bytes(), &standard); err != nil {
		t.Fatal(err)
	}
	if standard["data"] == nil || standard["models"] != nil || standard["object"] == nil {
		t.Fatalf("ordinary client received a different protocol: %s", ordinary.Body.String())
	}
	if strings.Contains(ordinary.Body.String(), "base_instructions") {
		t.Fatal("Codex capability metadata leaked into the standard model list")
	}
}

func TestCodexCatalogFromProxyAndEmptyCatalog(t *testing.T) {
	entries, err := parseDiscoveryEntries(contract.ProtocolOpenAIModels, []byte(`{"models":[{"slug":"proxy-model","visibility":"list","future_field":42}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := encodeCodexModelCatalog(entries)
	if err != nil || !strings.Contains(string(body), `"future_field":42`) || strings.Contains(string(body), "codex-auto-review") {
		t.Fatalf("proxy catalog = %s, error = %v", body, err)
	}
	for _, invalid := range []string{`{"models":null}`, `{"models":{}}`, `{"models":[{"slug":1}]}`} {
		if _, err := parseDiscoveryEntries(contract.ProtocolOpenAIModels, []byte(invalid)); err == nil {
			t.Errorf("accepted malformed catalog %s", invalid)
		}
	}
	for _, input := range []string{`{"models":[]}`, `{"data":[]}`} {
		entries, err := parseDiscoveryEntries(contract.ProtocolOpenAIModels, []byte(input))
		if err != nil {
			t.Fatal(err)
		}
		body, err := encodeCodexModelCatalog(entries)
		if err != nil || string(body) != `{"models":[]}` {
			t.Fatalf("empty catalog = %s, error = %v", body, err)
		}
	}
}

func TestCodexCatalogMetadataFollowsFirstRoutingCandidate(t *testing.T) {
	first := discoveryEntry{id: "shared", raw: []byte(`{"id":"shared"}`)}
	second := discoveryEntry{id: "shared", raw: []byte(`{"id":"shared"}`), codexCatalog: []byte(`{"slug":"shared","context_window":64000}`)}
	for _, entries := range [][]discoveryEntry{{first, second}, {second, first}} {
		merged, _ := mergeDiscoveryEntries([]discoveryResult{
			{outcome: discoveryOutcomeFetched, entries: entries[:1]},
			{outcome: discoveryOutcomeFetched, entries: entries[1:]},
		})
		if len(merged) != 1 || !reflect.DeepEqual(merged[0], entries[0]) {
			t.Fatalf("capabilities came from a different provider than routing: %+v", merged)
		}
	}
}

func TestDesktopCatalogContainsOnlyTokenRouteDeclarations(t *testing.T) {
	handler := codexCatalogTestHandler(t)
	r := httptest.NewRequest(http.MethodGet, "/v1/models?client_version="+contract.MinCodexClientVersion, nil)
	r.Header.Set(contract.CodexUpstreamCatalogHeader, "1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var declarations map[string]struct{ Slug string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &declarations) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, id := range []string{"gpt-model", "coding-alias", "service-alias"} {
		if declarations[id].Slug != "gpt-model" {
			t.Fatalf("wrong source for %s: %+v", id, declarations)
		}
	}
	for _, id := range []string{"manual-model", "kimi-alias", "blocked-model"} {
		if _, ok := declarations[id]; ok {
			t.Fatalf("unexpected native declaration for %s", id)
		}
	}
}
