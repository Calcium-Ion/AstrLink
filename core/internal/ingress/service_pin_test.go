package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/tidwall/gjson"
)

// A request naming a provider in X-AstrLink-Service is served by that
// provider alone: it skips higher-priority ones, never fails over, leaves no
// session binding behind, and the header stays on this side of the gateway.
func TestNamedServiceServesTheRequestAlone(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "service-pin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, service := range []struct {
		id      string
		enabled bool
		model   string
	}{
		{"service_backup", true, "public"},
		{"service_newapi", true, "public"},
		{"service_codex", false, "public"},
		{"service_mly", true, "other"},
	} {
		ep := validEndpoint(contract.ProtocolOpenAIChat, false)
		ep.ID, ep.Name, ep.Enabled, ep.Models = contract.ServiceID(service.id), "Name of "+service.id, service.enabled, []string{service.model}
		ep.BaseURL = "https://" + strings.TrimPrefix(service.id, "service_") + ".example"
		ep.Capabilities = append(ep.Capabilities, contract.Capability{Protocol: contract.ProtocolOpenAIModels, Mode: contract.CapabilityModeNative})
		if _, err := store.CreateEndpoint(ctx, ep, storage.CredentialMutation{Present: true, Secret: []byte("test-secret")}); err != nil {
			t.Fatal(err)
		}
	}
	order, err := store.GetServiceOrder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateServiceOrder(ctx, contract.ServiceOrder{ServiceIDs: []contract.ServiceID{
		"service_backup", "service_newapi", "service_codex", "service_mly",
	}}, order.ETag); err != nil {
		t.Fatal(err)
	}
	resolver, err := endpoint.NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	failNewAPI := false
	var hosts []string
	answer := "The pinned answer is long enough to be recognized through the conversation fingerprint."
	handler := NewWithDependencies(Dependencies{Resolver: resolver, Authorizer: endpoint.NewSecretAuthorizer(store), RequestRecords: store, AuditBlobs: store, Forwarder: transport.New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get(ServiceHeader) != "" {
			t.Errorf("%s reached %s", ServiceHeader, r.URL.Host)
		}
		hosts = append(hosts, r.URL.Host)
		if strings.HasSuffix(r.URL.Path, "/models") {
			model := "public"
			if r.URL.Host == "mly.example" {
				model = "other"
			}
			return jsonResponse(200, `{"object":"list","data":[{"id":"`+model+`","object":"model"}]}`), nil
		}
		if r.URL.Host == "newapi.example" && failNewAPI {
			return jsonResponse(503, `{"error":{"message":"unavailable"}}`), nil
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": answer}}}})
		return jsonResponse(200, string(raw)), nil
	}))})
	serve := func(method, path, pin, body string) *httptest.ResponseRecorder {
		t.Helper()
		hosts = nil
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if pin != "" {
			request.Header.Set(ServiceHeader, pin)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	latest := func() contract.RequestRecord {
		t.Helper()
		page, err := store.ListRequestRecords(ctx, storage.RequestRecordListOptions{Limit: 1})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("records %+v %v", page, err)
		}
		return page.Items[0]
	}
	opening := `{"model":"public","messages":[{"role":"user","content":"hello"}]}`

	response := serve("POST", "/v1/chat/completions", "service_newapi", opening)
	record := latest()
	if response.Code != 200 || strings.Join(hosts, ",") != "newapi.example" {
		t.Fatalf("pinned: status %d hosts %v body %s", response.Code, hosts, response.Body)
	}
	// Providers ranked ahead were never considered, so none is listed.
	want := &contract.RequestRoutingDecision{Selected: contract.RoutingSelectionClientPin, Skipped: []contract.RoutingSkip{}}
	if record.ServiceID == nil || *record.ServiceID != "service_newapi" || !reflect.DeepEqual(record.RoutingDecision, want) {
		t.Fatalf("pinned record served by %v, decision %+v", record.ServiceID, record.RoutingDecision)
	}

	// Without the header the same conversation follows priority again: the
	// named provider left no binding.
	follow := `{"model":"public","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"` + answer + `"},{"role":"user","content":"continue"}]}`
	if response := serve("POST", "/v1/chat/completions", "", follow); response.Code != 200 || strings.Join(hosts, ",") != "backup.example" {
		t.Fatalf("unpinned follow-up: status %d hosts %v", response.Code, hosts)
	}

	// A failing named provider exhausts its retries before returning the error.
	failNewAPI = true
	if response := serve("POST", "/v1/chat/completions", "service_newapi", opening); response.Code != 503 || !reflect.DeepEqual(hosts, []string{"newapi.example", "newapi.example"}) {
		t.Fatalf("failed pin: status %d hosts %v body %s", response.Code, hosts, response.Body)
	}

	for _, test := range []struct {
		pin     string
		status  int
		message string
	}{
		{"service_codex", 422, `API provider "Name of service_codex" is turned off`},
		{"service_mly", 422, `API provider "Name of service_mly" does not serve model "public"`},
		{"service_gone", 422, `API provider "service_gone" named in X-AstrLink-Service does not exist`},
		{"Not an ID", 400, "X-AstrLink-Service must hold one API provider ID"},
	} {
		response := serve("POST", "/v1/chat/completions", test.pin, opening)
		message := gjson.Get(response.Body.String(), "error.message").String()
		if response.Code != test.status || message != test.message || len(hosts) != 0 {
			t.Fatalf("%s: %d %q, hosts %v", test.pin, response.Code, message, hosts)
		}
		if record := latest(); record.Status != contract.RequestStatusFailed || record.ServiceID != nil {
			t.Fatalf("%s: record %s served by %v", test.pin, record.Status, record.ServiceID)
		}
	}

	// Discovery lists the named provider's models only.
	response = serve("GET", "/v1/models", "service_mly", "")
	if response.Code != 200 || strings.Join(hosts, ",") != "mly.example" {
		t.Fatalf("pinned discovery: status %d hosts %v body %s", response.Code, hosts, response.Body)
	}
	if ids := gjson.Get(response.Body.String(), "data.#.id").String(); ids != `["other"]` {
		t.Fatalf("pinned discovery listed %s", ids)
	}
}

// A routing group still answers to X-AstrLink-Service: a member on the named
// provider serves the request, and a member on another provider fails it
// without reaching any provider, since the client excluded every other one.
func TestNamedServiceBoundsModelRoutingGroups(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "service-pin-routing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"service_alpha", "service_beta"} {
		ep := validEndpoint(contract.ProtocolOpenAIChat, false)
		ep.ID, ep.Name, ep.Enabled, ep.Models = contract.ServiceID(id), "Name of "+id, true, []string{"shared-model"}
		ep.BaseURL = "https://" + strings.TrimPrefix(id, "service_") + ".example"
		if _, err := store.CreateEndpoint(ctx, ep, storage.CredentialMutation{Present: true, Secret: []byte("test-secret")}); err != nil {
			t.Fatal(err)
		}
	}
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = []contract.ModelRoutingGroup{{
		ID: "pinned", Enabled: true,
		Members:         []contract.ModelRoutingMember{{ID: "alpha", ServiceID: "service_alpha", Model: "shared-model"}},
		DefaultMemberID: "alpha",
		Rules:           []contract.ModelRoutingRule{},
	}}
	if err := store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	resolver, err := endpoint.NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	var hosts []string
	handler := NewWithDependencies(Dependencies{Resolver: resolver, Authorizer: endpoint.NewSecretAuthorizer(store), RequestRecords: store, AuditBlobs: store, Forwarder: transport.New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		return jsonResponse(200, `{"choices":[{"message":{"role":"assistant","content":"routed"}}]}`), nil
	}))})
	serve := func(pin string) *httptest.ResponseRecorder {
		t.Helper()
		hosts = nil
		request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"astrlink/pinned","messages":[{"role":"user","content":"hello"}]}`))
		request.Header.Set(ServiceHeader, pin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	if response := serve("service_alpha"); response.Code != 200 || strings.Join(hosts, ",") != "alpha.example" {
		t.Fatalf("member on the named provider: status %d hosts %v body %s", response.Code, hosts, response.Body)
	}
	response := serve("service_beta")
	if response.Code != http.StatusUnprocessableEntity || len(hosts) != 0 {
		t.Fatalf("member on another provider: status %d hosts %v body %s", response.Code, hosts, response.Body)
	}
	if code := gjson.Get(response.Body.String(), "error.code").String(); code != "model_routing_member_unavailable" {
		t.Fatalf("error code %q body %s", code, response.Body)
	}
	if message := gjson.Get(response.Body.String(), "error.message").String(); !strings.Contains(message, ServiceHeader) {
		t.Fatalf("error message %q does not name %s", message, ServiceHeader)
	}
}
