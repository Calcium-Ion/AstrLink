package sqlite

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestRecoverySettingsRecordsAndAffinitySurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.db")
	store := openTestStore(t, path)
	ctx := context.Background()
	settings := contract.DefaultRoutingSettings()
	// Explicit opt-outs must survive storage and reopening despite the defaults.
	settings.AllowUnmatchedFailover = false
	settings.ChannelStickiness = &contract.ChannelStickiness{Enabled: false, TTLSeconds: 120}
	settings.DefaultFailurePolicy.MaxRetries = 4
	if err := store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := contract.RequestRecord{ID: "request_recovery", StartedAt: now, CompletedAt: &now, Status: contract.RequestStatusFailed, InputProtocol: contract.ProtocolOpenAIChat, Audit: contract.NotCapturedAuditSummary(), Recovery: &contract.RequestRecovery{UpstreamModel: "actual", Action: "retry", Reason: "http_429", DelayMS: 500, StopReason: "attempt_limit"}}
	if err := store.InsertRequestRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	binding := storage.ResponseAffinity{ServiceID: "service_backup", UpstreamModel: "actual", UpstreamProtocol: contract.ProtocolOpenAIResponses, PlanType: contract.PlanTypeNative}
	if err := store.PutResponseAffinity(ctx, "token_a", "resp_one", binding); err != nil {
		t.Fatal(err)
	}
	key, err := store.GetOrCreateAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, direction := range []storage.AuditDirection{storage.AuditDirectionRequest, storage.AuditDirectionUpstreamRequest, storage.AuditDirectionUpstreamResponse} {
		nonce, ciphertext, err := storage.SealAuditBlob(key, []byte("content"))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.InsertAuditBlob(ctx, storage.AuditBlob{RequestID: record.ID, Direction: direction, MediaType: "application/json", Nonce: nonce, Ciphertext: ciphertext, CapturedBytes: 7, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteUpstreamAuditBlobs(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.GetAuditBlobsByRequest(ctx, record.ID)
	if err != nil || len(remaining) != 1 || remaining[0].Direction != storage.AuditDirectionRequest {
		t.Fatalf("attempt reset removed client audit or retained stale upstream audit: %+v %v", remaining, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openTestStore(t, path)
	defer store.Close()
	got, err := store.GetRoutingSettings(ctx)
	if err != nil || !reflect.DeepEqual(got, settings) {
		t.Fatalf("settings %v %+v", err, got)
	}
	saved, err := store.GetRequestRecord(ctx, record.ID)
	if err != nil || !reflect.DeepEqual(saved.Recovery, record.Recovery) {
		t.Fatalf("recovery %v %+v", err, saved)
	}
	stored, found, err := store.GetResponseAffinity(ctx, "token_a", "resp_one")
	if err != nil || !found || stored != binding {
		t.Fatalf("affinity %+v %v %v", stored, found, err)
	}
	if _, found, err := store.GetResponseAffinity(ctx, "token_b", "resp_one"); err != nil || found {
		t.Fatalf("principal isolation %v %v", found, err)
	}
	store.now = func() time.Time { return now.Add(25 * time.Hour) }
	if _, found, err := store.GetResponseAffinity(ctx, "token_a", "resp_one"); err != nil || found {
		t.Fatalf("expiry %v %v", found, err)
	}
	// An older settings row without the new field reads global built-in defaults.
	if _, err := store.db.ExecContext(ctx, `UPDATE routing_settings SET document_json = '{"allow_unmatched_failover":false,"strategy":"retry_first","max_attempts":6}'`); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetRoutingSettings(ctx)
	if err != nil || got.DefaultFailurePolicy.MaxRetries != 1 || got.AllowUnmatchedFailover || got.ChannelStickiness == nil || !got.ChannelStickiness.Enabled || got.ChannelStickiness.TTLSeconds != 3600 {
		t.Fatalf("legacy settings %v %+v", err, got)
	}
}
