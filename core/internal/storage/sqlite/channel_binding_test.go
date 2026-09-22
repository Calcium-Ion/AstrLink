package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestChannelBindingReleaseFencesInflightAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bindings.db")
	store := openTestStore(t, path)
	ctx := context.Background()
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	scope := contract.ChannelBindingScope{SessionID: "session_one", Principal: "token_one", Protocol: contract.ProtocolOpenAIChat, Model: "model"}
	binding := contract.ChannelBinding{ChannelBindingScope: scope, ServiceID: "service_a", Source: "fingerprint", RequestID: "request_one", UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := store.RememberChannelBinding(ctx, binding, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	other := scope
	other.Principal = "token_two"
	if _, found, err := store.GetChannelBinding(ctx, other); err != nil || found {
		t.Fatalf("cross-principal binding: %v %v", found, err)
	}
	if err := store.ReleaseChannelBindings(ctx, scope.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.RememberChannelBinding(ctx, binding, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.GetChannelBinding(ctx, scope); found {
		t.Fatal("in-flight request resurrected released binding")
	}
	store.Close()
	store = openTestStore(t, path)
	defer store.Close()
	if err := store.RememberChannelBinding(ctx, binding, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.GetChannelBinding(ctx, scope); found {
		t.Fatal("release fence lost on reopen")
	}
	binding.ServiceID = "service_b"
	if err := store.RememberChannelBinding(ctx, binding, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// Old completion must not overwrite a newer successful preference.
	binding.ServiceID = "service_a"
	if err := store.RememberChannelBinding(ctx, binding, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.GetChannelBinding(ctx, scope)
	if err != nil || !found || got.ServiceID != "service_b" {
		t.Fatalf("stale write won: %+v %v", got, err)
	}
	audit, err := store.GetChannelBindingAudit(ctx, scope.SessionID, 0)
	if err != nil || len(audit.Events) != 3 || audit.Events[1].Action != "released" {
		t.Fatalf("audit lost: %+v %v", audit, err)
	}
	if audit.Events[2].Source != "fingerprint" {
		t.Fatal("convo match evidence missing")
	}
}

func TestChannelBindingBusyWriterHonorsCancellation(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "busy.db"))
	defer store.Close()
	store.channelBindingsMu <- struct{}{}
	defer func() { <-store.channelBindingsMu }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, write := range []func() error{
		func() error { return store.RecordChannelBindingEvent(ctx, contract.ChannelBindingEvent{}) },
		func() error { return store.RememberChannelBinding(ctx, contract.ChannelBinding{}, time.Now()) },
		func() error { return store.ReleaseChannelBindings(ctx, "session_one") },
	} {
		if err := write(); !errors.Is(err, context.Canceled) {
			t.Fatalf("busy writer ignored cancellation: %v", err)
		}
	}
}

func TestChannelBindingAuditExpiryAndLimit(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "bindings.db"))
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	scope := contract.ChannelBindingScope{SessionID: "session_one", Protocol: contract.ProtocolOpenAIChat, Model: "model"}
	binding := contract.ChannelBinding{ChannelBindingScope: scope, ServiceID: "service_a", UpdatedAt: now, ExpiresAt: now.Add(time.Second)}
	if err := store.RememberChannelBinding(ctx, binding, now); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	for i := 0; i < 101; i++ {
		if err := store.RecordChannelBindingEvent(ctx, contract.ChannelBindingEvent{ChannelBindingScope: scope, Action: "hit", Reason: "session_match"}); err != nil {
			t.Fatal(err)
		}
	}
	audit, err := store.GetChannelBindingAudit(ctx, scope.SessionID, 0)
	if err != nil || len(audit.Bindings) != 0 || len(audit.Events) != 100 || !audit.HasMore {
		t.Fatalf("expiry/limit: %+v %v", audit, err)
	}
	if audit.Events[0].ID <= audit.Events[1].ID {
		t.Fatal("audit order is not newest first")
	}
	older, err := store.GetChannelBindingAudit(ctx, scope.SessionID, audit.Events[len(audit.Events)-1].ID)
	if err != nil || len(older.Events) != 2 || older.HasMore || older.Events[0].ID >= audit.Events[99].ID {
		t.Fatalf("older page: %+v %v", older, err)
	}

}
