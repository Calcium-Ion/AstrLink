package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestProxyPersistsAcrossRestartAndLegacyWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "proxy.db")
	store := openTestStore(t, path)
	service := contract.ServiceFromEndpoint(testEndpoint("service_proxy"))
	service.Proxy = &contract.ServiceProxy{Mode: "custom", URL: "socks5://proxy.example:1080"}
	record, err := store.CreateService(ctx, service, storage.CredentialMutation{ProxyPresent: true, Proxy: &contract.ProxyCredential{Username: "proxy-user", Password: "proxy-password"}})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store = openTestStore(t, path)
	defer store.Close()
	record, err = store.GetService(ctx, service.ID)
	if err != nil || record.Service.Proxy == nil || record.Service.Proxy.URL != service.Proxy.URL {
		t.Fatal("proxy did not survive restart", err)
	}
	value, err := store.Get(ctx, secretstore.Ref(record.Service.Proxy.CredentialRef))
	if err != nil || !strings.Contains(string(value), "proxy-password") {
		t.Fatal("proxy secret did not survive restart", err)
	}
	var document string
	if err := store.db.QueryRow(`SELECT document_json FROM services WHERE id=?`, service.ID).Scan(&document); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document, "proxy-password") || strings.Contains(document, "proxy-user") {
		t.Fatal("proxy authentication entered public document")
	}
	legacy, _ := record.Service.EndpointView()
	legacy.Name = "renamed by compatibility API"
	if _, err := store.UpdateEndpoint(ctx, legacy, storage.CredentialMutation{}, record.ETag); err != nil {
		t.Fatal(err)
	}
	record, _ = store.GetService(ctx, service.ID)
	if record.Service.Proxy == nil || record.Service.Proxy.CredentialRef == "" {
		t.Fatal("legacy write removed proxy")
	}
	if err := store.DeleteService(ctx, service.ID, record.ETag); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, secretstore.Ref("local://service-proxy/"+string(service.ID))); err == nil {
		t.Fatal("deleted proxy credential survived")
	}
}
