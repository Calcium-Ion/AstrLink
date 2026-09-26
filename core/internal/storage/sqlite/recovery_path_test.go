package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func pathTestService(id contract.ServiceID) contract.Service {
	return contract.Service{ID: id, Name: string(id), Kind: contract.ServiceKindOpenAI, Enabled: true, Models: []string{"public", "model_a", "model_b"}, HTTP: &contract.HTTPConnection{BaseURL: "https://example.test", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true}}}
}

// Retired route and recovery path rows stay in their tables and settings keep
// their stored default path IDs, but nothing executes or enforces them.
func TestRetiredRoutesAndPathsRemainStoredButInactive(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "paths.db"))
	defer store.Close()
	for _, id := range []contract.ServiceID{"service_a", "service_b"} {
		if _, err := store.CreateService(ctx, pathTestService(id), storage.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}
	const pathDocument = `{"id":"path_old","name":"Old","protocol":"openai.responses","mode":"automatic","targets":[{"id":"node_b","service_id":"service_b","upstream_model":"model_b","upstream_protocol":"openai.responses","plan_type":"native"}]}`
	if _, err := store.db.ExecContext(ctx, `INSERT INTO recovery_paths (id, document_json) VALUES (?, ?)`, "path_old", pathDocument); err != nil {
		t.Fatal(err)
	}
	const routeDocument = `{"recovery_path_id":"path_old","id":"route_old","name":"Old","enabled":true,"priority":0,"match":{"protocol":"openai.responses","model":"public"}}`
	if _, err := store.db.ExecContext(ctx, `INSERT INTO routes (id, document_json, created_at, updated_at) VALUES (?, ?, ?, ?)`, "route_old", routeDocument, "now", "now"); err != nil {
		t.Fatal(err)
	}
	settings := contract.DefaultRoutingSettings()
	settings.DefaultRecoveryPaths = map[contract.ProtocolID]contract.RecoveryPathID{contract.ProtocolOpenAIResponses: "path_old"}
	if err := store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetRoutingSettings(ctx)
	if err != nil || len(stored.DefaultRecoveryPaths) != 1 || stored.DefaultRecoveryPaths[contract.ProtocolOpenAIResponses] != "path_old" {
		t.Fatalf("default recovery paths = %v, %v", stored.DefaultRecoveryPaths, err)
	}
	resolver, _ := endpoint.NewStoreResolver(store)
	result, err := resolver.ResolveCandidates(ctx, endpoint.ResolveRequest{Protocol: contract.ProtocolOpenAIResponses, Model: "public"})
	if err != nil || len(result) != 2 || result[0].CanonicalService().ID != "service_a" || result[1].CanonicalService().ID != "service_b" {
		t.Fatalf("retired routing still executed: %v %v", result, err)
	}
	for _, candidate := range result {
		if candidate.UpstreamModel != "public" {
			t.Fatalf("retired routing still executed: %+v", candidate)
		}
	}
	referenced, _ := store.GetService(ctx, "service_b")
	if err := store.DeleteService(ctx, "service_b", referenced.ETag); err != nil {
		t.Fatalf("retired reference blocked deletion: %v", err)
	}
	for _, table := range []string{"routes", "recovery_paths"} {
		var rows int
		if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("%s rows = %d, %v", table, rows, err)
		}
	}
}
