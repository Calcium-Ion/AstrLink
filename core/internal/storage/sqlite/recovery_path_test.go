package sqlite

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"path/filepath"
	"reflect"
	"testing"
)

func pathTestService(id contract.ServiceID) contract.Service {
	return contract.Service{ID: id, Name: string(id), Kind: contract.ServiceKindOpenAI, Enabled: true, Models: []string{"public", "model_a", "model_b"}, HTTP: &contract.HTTPConnection{BaseURL: "https://example.test", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true}}}
}
func TestRetiredRoutesAndPathsRemainReadableButInactive(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "paths.db"))
	defer store.Close()
	for _, id := range []contract.ServiceID{"service_a", "service_b"} {
		if _, err := store.CreateService(ctx, pathTestService(id), storage.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}
	path := contract.RecoveryPath{ID: "path_old", Name: "Old", Protocol: contract.ProtocolOpenAIResponses, Mode: "automatic", Targets: []contract.RecoveryPathNode{{ID: "node_b", ServiceID: "service_b", PlanType: contract.PlanTypeNative, UpstreamProtocol: contract.ProtocolOpenAIResponses, UpstreamModel: "model_b"}}}
	if _, err := store.CreateRecoveryPath(ctx, path); err != nil {
		t.Fatal(err)
	}
	route := contract.Route{ID: "route_old", Name: "Old", Enabled: true, Match: contract.RouteMatch{Protocol: path.Protocol, Model: "public"}, RecoveryPathID: path.ID}
	if _, err := store.CreateRoute(ctx, route); err != nil {
		t.Fatal(err)
	}
	settings := contract.DefaultRoutingSettings()
	settings.DefaultRecoveryPaths = map[contract.ProtocolID]contract.RecoveryPathID{path.Protocol: path.ID}
	if err := store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	unused := path
	unused.ID, unused.Name = "path_unused", "Unused"
	if _, err := store.CreateRecoveryPath(ctx, unused); err != nil {
		t.Fatal(err)
	}
	paths, err := store.ListRecoveryPaths(ctx)
	if err != nil || len(paths) != 2 {
		t.Fatalf("paths=%+v err=%v", paths, err)
	}
	for _, listed := range paths {
		individual, err := store.GetRecoveryPath(ctx, listed.Path.ID)
		if err != nil || !reflect.DeepEqual(listed, individual) {
			t.Fatalf("listed=%+v individual=%+v err=%v", listed, individual, err)
		}
	}
	if len(paths[0].References) != 2 || paths[1].References == nil || len(paths[1].References) != 0 {
		t.Fatalf("references=%+v", paths)
	}
	resolver, _ := endpoint.NewStoreResolver(store)
	result, err := resolver.ResolveCandidates(ctx, endpoint.ResolveRequest{Protocol: path.Protocol, Model: "public"})
	if err != nil || len(result) != 2 || result[0].CanonicalService().ID != "service_a" || result[1].CanonicalService().ID != "service_b" {
		t.Fatalf("retired routing still executed: %v %v", result, err)
	}
	for _, candidate := range result {
		if candidate.Path != nil || candidate.RouteID != "" || candidate.UpstreamModel != "public" {
			t.Fatalf("retired routing still executed: %+v", candidate)
		}
	}
	aliases, err := resolver.ListAliasModels(ctx, contract.ProtocolOpenAIModels)
	if err != nil || len(aliases) != 0 {
		t.Fatalf("retired aliases: %v %v", aliases, err)
	}
	referenced, _ := store.GetService(ctx, "service_b")
	if err := store.DeleteService(ctx, "service_b", referenced.ETag); err != nil {
		t.Fatalf("retired reference blocked deletion: %v", err)
	}
	if _, err := store.GetRoute(ctx, route.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRecoveryPath(ctx, path.ID); err != nil {
		t.Fatal(err)
	}
}
