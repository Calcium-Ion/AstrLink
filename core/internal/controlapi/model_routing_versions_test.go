package controlapi

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func versionTestHandler(t *testing.T) (*Handler, *sqlite.Store) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "versions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service := contract.Service{
		ID: "service_a", Name: "Service A", Kind: contract.ServiceKindOpenAI, Enabled: true, Models: []string{"model_a", "model_b"},
		HTTP:         &contract.HTTPConnection{BaseURL: "https://example.test", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}},
		Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true}},
	}
	if _, err := store.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithDependencies(contract.VersionResponse{CoreVersion: "test", ControlAPIVersion: "v1", ProtocolContractVersion: "v1"}, Dependencies{ServiceStore: store, RequestRecords: store, ControlToken: "control-token-123456"})
	if err != nil {
		t.Fatal(err)
	}
	return handler, store
}

const versionTestGroups = `{"model_routing_groups":[{"id":"program","enabled":true,"members":[{"id":"fast","service_id":"service_a","model":"model_a"},{"id":"strong","service_id":"service_a","model":"model_b"}],"default_member_id":"fast","rules":[{"id":"streamed","priority":1,"when":{"field":"streaming","equals_bool":true},"target_member_id":"strong"}]}]}`

func TestModelRoutingGroupVersionEndpoint(t *testing.T) {
	handler, _ := versionTestHandler(t)
	response := requestRecordHTTP(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", versionTestGroups)
	if response.Code != http.StatusOK {
		t.Fatalf("save groups %d %s", response.Code, response.Body.String())
	}
	response = requestRecordHTTP(t, handler, http.MethodGet, ModelRoutingVersionsPath+"/program/1", "", "")
	var version contract.ModelRoutingGroupVersion
	decode(t, response, &version)
	if response.Code != http.StatusOK || version.Version != 1 || version.LatestVersion != 1 || len(version.Group.Rules) != 1 || version.Group.Rules[0].TargetMemberID != "strong" {
		t.Fatalf("version 1 = %d %+v", response.Code, version)
	}
	for path, want := range map[string]int{
		ModelRoutingVersionsPath + "/program/2": http.StatusNotFound,
		ModelRoutingVersionsPath + "/program/0": http.StatusBadRequest,
		ModelRoutingVersionsPath + "/program":   http.StatusBadRequest,
	} {
		if got := requestRecordHTTP(t, handler, http.MethodGet, path, "", "").Code; got != want {
			t.Fatalf("GET %s = %d, want %d", path, got, want)
		}
	}
}

// The session details list routing-group bindings next to API provider
// bindings, and releasing the session clears both.
func TestSessionBindingsIncludeRoutingGroupBindings(t *testing.T) {
	ctx := context.Background()
	handler, store := versionTestHandler(t)
	if response := requestRecordHTTP(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", versionTestGroups); response.Code != http.StatusOK {
		t.Fatalf("save groups %d %s", response.Code, response.Body.String())
	}
	now := time.Now().UTC()
	id := contract.SessionID("session_one")
	record := contract.RequestRecord{ID: "request_one", SessionID: &id, StartedAt: now, CompletedAt: &now, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary()}
	if err := store.InsertRequestRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	stamp := now.Format("2006-01-02T15:04:05.000000000Z07:00")
	binding := contract.ModelRoutingBinding{
		Principal: "token_one", SessionID: id, Protocol: contract.ProtocolOpenAIResponses, VirtualModelID: "astrlink/program", GroupID: "program",
		MemberID: "strong", ServiceID: "service_a", Model: "model_b", ConfigSnapshotVersion: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		RequestID: string(record.ID), CreatedAt: stamp, UpdatedAt: stamp, ExpiresAt: now.Add(time.Hour).Format("2006-01-02T15:04:05.000000000Z07:00"),
	}
	if err := store.UpsertModelRoutingBinding(ctx, binding, now); err != nil {
		t.Fatal(err)
	}
	path := RequestSessionsPath + "/session_one/channel-bindings"
	var audit contract.ChannelBindingAudit
	response := requestRecordHTTP(t, handler, http.MethodGet, path, "", "")
	decode(t, response, &audit)
	if response.Code != http.StatusOK || len(audit.ModelRoutingBindings) != 1 || audit.ModelRoutingBindings[0].Model != "model_b" || audit.ModelRoutingBindings[0].RequestID != record.ID {
		t.Fatalf("audit %d %+v", response.Code, audit)
	}
	response = requestRecordHTTP(t, handler, http.MethodDelete, path, "", "")
	decode(t, response, &audit)
	if response.Code != http.StatusOK || len(audit.ModelRoutingBindings) != 0 {
		t.Fatalf("release %d %+v", response.Code, audit)
	}
}
