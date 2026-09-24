package sqlite

import (
	"context"
	"errors"
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
	settings.CodexIdentityEnforcement = false
	settings.ClaudeIdentityEnforcement = false
	settings.GrokIdentityEnforcement = false
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
	if err != nil || !got.CodexIdentityEnforcement || !got.ClaudeIdentityEnforcement || !got.GrokIdentityEnforcement || got.DefaultFailurePolicy.MaxRetries != 1 || got.AllowUnmatchedFailover || got.ChannelStickiness == nil || !got.ChannelStickiness.Enabled || got.ChannelStickiness.TTLSeconds != 3600 {
		t.Fatalf("legacy settings %v %+v", err, got)
	}
	if got.ModelRedirects == nil || len(got.ModelRedirects) != 0 {
		t.Fatalf("legacy document redirects = %#v", got.ModelRedirects)
	}
}

func TestRoutingSettingsModelRedirectsLoadFromRawDocuments(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "redirects.db"))
	defer store.Close()
	ctx := context.Background()
	got, err := store.GetRoutingSettings(ctx)
	if err != nil || got.ModelRedirects == nil || len(got.ModelRedirects) != 0 {
		t.Fatalf("fresh database redirects = %#v %v", got.ModelRedirects, err)
	}
	setDocument := func(document string) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, `UPDATE routing_settings SET document_json = ? WHERE id = 1`, document); err != nil {
			t.Fatal(err)
		}
	}
	for _, document := range []string{
		`{"allow_unmatched_failover":true,"strategy":"retry_first","max_attempts":6}`,
		`{"model_redirects":null,"allow_unmatched_failover":true,"strategy":"retry_first","max_attempts":6}`,
		`{"model_redirects":[],"allow_unmatched_failover":true,"strategy":"retry_first","max_attempts":6}`,
	} {
		setDocument(document)
		got, err := store.GetRoutingSettings(ctx)
		if err != nil || got.ModelRedirects == nil || len(got.ModelRedirects) != 0 {
			t.Fatalf("%s => %#v %v", document, got.ModelRedirects, err)
		}
	}
	setDocument(`{"model_redirects":[{"from":"gpt-4o","to":"gpt-4.1","enabled":true},{"from":"astrlink/auto","to":"gpt-4.1-mini","enabled":false}],"allow_unmatched_failover":true,"strategy":"retry_first","max_attempts":6}`)
	got, err = store.GetRoutingSettings(ctx)
	want := []contract.ModelRedirect{
		{From: "gpt-4o", To: "gpt-4.1", Enabled: true},
		{From: contract.AstrLinkAutoModelID, To: "gpt-4.1-mini", Enabled: false},
	}
	if err != nil || !reflect.DeepEqual(got.ModelRedirects, want) {
		t.Fatalf("stored redirects = %#v %v", got.ModelRedirects, err)
	}
	// Stored tables are validated with the same rules as writes.
	for _, redirects := range []string{
		`[{"from":"a","to":"b","enabled":true},{"from":"b","to":"c","enabled":true}]`,
		`[{"from":"a","to":"b","enabled":true},{"from":"a","to":"c","enabled":true}]`,
		`[{"from":"a","to":"a","enabled":true}]`,
		`[{"from":"a","to":"astrlink/auto","enabled":true}]`,
		`[{"from":"a","to":"b"}]`,
		`[{"from":"a","to":"b","enabled":true,"note":"x"}]`,
		`{"from":"a","to":"b","enabled":true}`,
	} {
		setDocument(`{"model_redirects":` + redirects + `,"allow_unmatched_failover":true,"strategy":"retry_first","max_attempts":6}`)
		if _, err := store.GetRoutingSettings(ctx); !errors.Is(err, storage.ErrInvalidRecord) {
			t.Fatalf("%s loaded with err=%v", redirects, err)
		}
	}
	settings := contract.DefaultRoutingSettings()
	settings.ModelRedirects = []contract.ModelRedirect{{From: "a", To: "b", Enabled: true}, {From: "b", To: "c", Enabled: true}}
	if err := store.UpdateRoutingSettings(ctx, settings); !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("chained update err=%v", err)
	}
	settings.ModelRedirects = nil
	if err := store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	var document string
	if err := store.db.QueryRowContext(ctx, `SELECT document_json FROM routing_settings WHERE id = 1`).Scan(&document); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetRoutingSettings(ctx)
	if err != nil || got.ModelRedirects == nil || len(got.ModelRedirects) != 0 {
		t.Fatalf("nil table saved as %s and loaded as %#v %v", document, got.ModelRedirects, err)
	}
}
