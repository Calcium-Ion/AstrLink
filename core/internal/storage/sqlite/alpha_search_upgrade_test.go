package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestAlphaSearchUpgradePreservesConfiguredServicesAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alpha-search.db")
	writeSchemaFixture(t, path, 49)
	database, err := sql.Open(driverName, sqliteFileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	stamp := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	alpha := contract.Capability{Protocol: contract.ProtocolOpenAIAlphaSearch, Mode: contract.CapabilityModeNative}
	webSocketEnabled := true
	codex := contract.Service{
		ID: "service_codex_upgrade", Name: "Configured Codex", Kind: contract.ServiceKindCodexSubscription,
		Enabled: false, Models: []string{"custom-codex-model"},
		Capabilities: []contract.Capability{
			{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
			{Protocol: contract.ProtocolOpenAIResponsesCompact, Mode: contract.CapabilityModeNative},
			{Protocol: contract.ProtocolOpenAIModels, Mode: contract.CapabilityModeNative},
			{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true, ConvertTo: contract.ProtocolOpenAIResponses},
		},
		Subscription: &contract.SubscriptionConnection{
			Provider: contract.SubscriptionProviderOpenAICodex, Status: contract.SubscriptionStatusConnected,
			AccountHint: "configured-account", ProviderAccountID: "provider-account-42",
			CredentialRef: "keyring://subscription/service_codex_upgrade", TokenExpiresAt: &stamp,
		},
		ResponsesWebSocketEnabled: &webSocketEnabled,
		CreatedAt:                 stamp, UpdatedAt: stamp,
	}
	gateway := contract.ServiceFromEndpoint(testEndpoint("service_newapi_upgrade"))
	gateway.Kind = contract.ServiceKindNewAPI
	gateway.HTTP.CredentialRef = "local://service/" + string(gateway.ID)
	gateway.CreatedAt, gateway.UpdatedAt = stamp, stamp
	gateway.Models = []string{"configured-model-a", "configured-model-b"}
	gateway.Capabilities = append(gateway.Capabilities, contract.Capability{
		Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true,
	})
	already := codex
	already.ID, already.Name = "service_codex_already", "Already configured"
	already.Capabilities = append(slices.Clone(codex.Capabilities), alpha)
	unrelated := contract.ServiceFromEndpoint(testEndpoint("service_openai_unchanged"))
	unrelated.CreatedAt, unrelated.UpdatedAt = stamp, stamp
	services := []contract.Service{codex, gateway, already, unrelated}
	want := make(map[contract.ServiceID]contract.Service)
	for _, service := range services {
		// Marshal directly: the old Codex document intentionally lacks the
		// native capability required by the current contract.
		document, err := json.Marshal(service)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO services (id, document_json, created_at, updated_at, sort_position) VALUES (?, ?, ?, ?, 5)`,
			service.ID, string(document), stamp.Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		if service.ID == codex.ID {
			service.Capabilities = append(slices.Clone(service.Capabilities), alpha)
		}
		want[service.ID] = service
	}
	const credential = "fixture-existing-newapi-key"
	if _, err := database.Exec(`INSERT INTO service_credentials (service_id, credential_value, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		gateway.ID, []byte(credential), stamp.Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	localKey := testLocalKey(t, 0x52)
	etags := make(map[contract.ServiceID]string)
	for attempt := 0; attempt < 2; attempt++ {
		store := openWithKey(t, path, localKey, nil)
		func() {
			defer store.Close()
			page, err := store.ListServices(ctx, storagecontract.ServiceListOptions{})
			if err != nil || len(page.Items) != len(services) {
				t.Fatalf("reopen %d: list services = %#v, err = %v", attempt, page, err)
			}
			for _, item := range page.Items {
				record, err := store.GetService(ctx, item.Service.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(record.Service, want[item.Service.ID]) || !reflect.DeepEqual(item.Service, record.Service) {
					t.Fatalf("reopen %d: service %s changed unexpectedly:\ngot  %#v\nwant %#v", attempt, item.Service.ID, record.Service, want[item.Service.ID])
				}
				if attempt > 0 && record.ETag != etags[item.Service.ID] {
					t.Fatalf("reopen rewrote %s", item.Service.ID)
				}
				etags[item.Service.ID] = record.ETag
			}
			accounts, err := store.ListSubscriptionAccounts(ctx)
			if err != nil || len(accounts) != 2 {
				t.Fatalf("subscription views = %#v, err = %v", accounts, err)
			}
			secret, err := store.Get(ctx, secretstore.Ref(localRef(gateway.ID)))
			if err != nil || string(secret) != credential {
				t.Fatalf("gateway credential was not preserved: %v", err)
			}
		}()
	}
}

func TestAlphaSearchUpgradeFromLegacySubscriptionAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-subscriptions.db")
	// Use v11's real schema. v12 imports connected/needs_reauth accounts
	// into services and drops subscription_accounts; do not invent columns.
	writeSchemaFixture(t, path, 11)
	database, err := sql.Open(driverName, sqliteFileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	stamp := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	accounts := []contract.SubscriptionAccount{}
	for _, status := range []contract.SubscriptionStatus{contract.SubscriptionStatusConnected, contract.SubscriptionStatusNeedsReauth} {
		account := contract.SubscriptionAccount{
			ID: contract.ServiceID("legacy_" + string(status)), Provider: contract.SubscriptionProviderOpenAICodex,
			Status: status, DisplayName: "Legacy Codex", AccountHint: "legacy-account", ProviderAccountID: "legacy-provider-id",
			CredentialRef: "keyring://subscription/legacy_" + string(status),
			Capabilities: []contract.Capability{
				{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
				{Protocol: contract.ProtocolOpenAIResponsesCompact, Mode: contract.CapabilityModeNative},
			},
			CreatedAt: stamp, UpdatedAt: stamp,
		}
		accounts = append(accounts, account)
		document, err := json.Marshal(account)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO subscription_accounts (id, document_json, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			account.ID, string(document), stamp.Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store := openWithKey(t, path, testLocalKey(t, 0x53), nil)
	defer store.Close()
	if hasTable(t, store, "subscription_accounts") {
		t.Fatal("legacy subscription table still exists")
	}
	for _, before := range accounts {
		after, err := store.GetSubscriptionAccount(context.Background(), before.ID)
		if err != nil {
			t.Fatal(err)
		}
		wantCapabilities := contract.DefaultOpenAICodexCapabilities()
		if len(after.Capabilities) != len(wantCapabilities) {
			t.Fatalf("legacy account capabilities = %#v", after.Capabilities)
		}
		for _, capability := range wantCapabilities {
			if !slices.Contains(after.Capabilities, capability) {
				t.Fatalf("legacy account missing %#v", capability)
			}
		}
		after.Capabilities = before.Capabilities
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("legacy account configuration changed:\ngot  %#v\nwant %#v", after, before)
		}
	}
}
