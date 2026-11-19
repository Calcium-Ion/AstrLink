package ingress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestRequestRecordsKeepTheRequestMetadata(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := validEndpoint(contract.ProtocolOpenAIChat, false)
	service.ID, service.Name, service.BaseURL, service.Models = "service_meta", "meta", "https://meta.example", []string{"public"}
	if _, err := store.CreateEndpoint(ctx, service, storage.CredentialMutation{Present: true, Secret: []byte("test-secret")}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateMetadataAttribute(ctx, contract.MetadataAttribute{Key: "env", Label: "环境", Pattern: "^(dev|prod)$", Owner: contract.MetadataOwnerUser}); err != nil {
		t.Fatal(err)
	}
	registry := metadata.NewRegistry(store)
	if err := registry.RegisterPlugin(ctx, "powercontext", []contract.MetadataAttribute{{Key: "project", Label: "项目", Pattern: "^[a-z0-9][a-z0-9._-]{0,63}$", Marker: "memory-project"}}); err != nil {
		t.Fatal(err)
	}
	resolver, err := endpoint.NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	var forwarded http.Header
	handler := NewWithDependencies(Dependencies{
		Resolver: resolver, Authorizer: endpoint.NewSecretAuthorizer(store), RequestRecords: store, AuditBlobs: store, Metadata: registry,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			forwarded = request.Header.Clone()
			return jsonResponse(200, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
		})),
	})
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("X-AstrLink-Meta-Env", "prod")
	request.Header.Set("X-AstrLink-Meta-Project", "Not A Project")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	for name := range forwarded {
		if strings.HasPrefix(strings.ToLower(name), "x-astrlink-") {
			t.Fatalf("forwarded %s upstream", name)
		}
	}
	page, err := store.ListRequestRecords(ctx, storage.RequestRecordListOptions{Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("records %+v, %v", page, err)
	}
	recorded := page.Items[0].Metadata
	if recorded == nil || recorded.Values["env"] != (contract.MetadataValue{Value: "prod", Source: contract.MetadataSourceHeader}) {
		t.Fatalf("metadata = %+v", recorded)
	}
	if _, set := recorded.Values["project"]; set || len(recorded.Events) != 1 || recorded.Events[0].Key != "project" || recorded.Events[0].Reason != contract.MetadataIgnoredInvalidValue {
		t.Fatalf("an invalid header value = %+v", recorded)
	}
}
