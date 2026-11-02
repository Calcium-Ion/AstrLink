package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestPrivacyStandInsKeepTheirFirstOwnerAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stand-ins.db")
	store := openTestStore(t, path)
	ctx := context.Background()
	session := contract.SessionID("session_one")
	// .1 then .12 of a second: RFC3339Nano text would sort the second first.
	issued := time.Date(2026, 11, 2, 18, 0, 0, 100_000_000, time.UTC)
	for index, batch := range [][]storagecontract.PrivacyStandIn{
		{{StandIn: "198.51.100.91", Owner: "owner_a"}, {StandIn: "203.0.113.4", Owner: "owner_b"}},
		{{StandIn: "198.51.100.91", Owner: "owner_c"}, {StandIn: "192.0.2.17", Owner: "owner_a"}},
	} {
		at := issued.Add(time.Duration(index) * 20 * time.Millisecond)
		store.now = func() time.Time { return at }
		if err := store.RememberPrivacyStandIns(ctx, session, batch); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RememberPrivacyStandIns(ctx, "", []storagecontract.PrivacyStandIn{{StandIn: "192.0.2.1", Owner: "owner_a"}}); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("unnamed conversation error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openTestStore(t, path)
	defer store.Close()
	got, err := store.PrivacyStandIns(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	want := []storagecontract.PrivacyStandIn{
		{StandIn: "198.51.100.91", Owner: "owner_a"},
		{StandIn: "203.0.113.4", Owner: "owner_b"},
		{StandIn: "192.0.2.17", Owner: "owner_a"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("stand-ins = %#v, want %#v", got, want)
	}
	if other, err := store.PrivacyStandIns(ctx, "session_two"); err != nil || len(other) != 0 {
		t.Fatalf("another conversation's stand-ins = %#v err=%v", other, err)
	}
}

func TestPrivacyStandInsGoWithTheLastRecordOfTheirConversation(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "stand-ins-purge.db"))
	defer store.Close()
	ctx := context.Background()
	now := store.now().UTC()
	record := func(id string, session contract.SessionID, started time.Time) contract.RequestRecord {
		return contract.RequestRecord{
			ID: contract.RequestID(id), SessionID: &session, StartedAt: started, CompletedAt: ptrTime(started.Add(time.Second)),
			Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
			Audit: contract.NotCapturedAuditSummary(),
		}
	}
	for _, item := range []contract.RequestRecord{
		record("request_aged", "session_aged", now.AddDate(0, 0, -60)),
		record("request_aged_resumed", "session_resumed", now.AddDate(0, 0, -60)),
		record("request_resumed", "session_resumed", now),
		record("request_purged", "session_purged", now),
		record("request_kept", "session_kept", now),
	} {
		if err := store.InsertRequestRecord(ctx, item); err != nil {
			t.Fatalf("InsertRequestRecord(%s): %v", item.ID, err)
		}
	}
	for _, session := range []contract.SessionID{"session_aged", "session_resumed", "session_purged", "session_kept"} {
		if err := store.RememberPrivacyStandIns(ctx, session, []storagecontract.PrivacyStandIn{{StandIn: "192.0.2.1", Owner: "owner"}}); err != nil {
			t.Fatal(err)
		}
	}
	remaining := func() []contract.SessionID {
		t.Helper()
		rows, err := store.db.QueryContext(ctx, `SELECT session_id FROM privacy_stand_ins ORDER BY session_id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var sessions []contract.SessionID
		for rows.Next() {
			var session contract.SessionID
			if err := rows.Scan(&session); err != nil {
				t.Fatal(err)
			}
			sessions = append(sessions, session)
		}
		return sessions
	}

	settings := contract.DefaultAuditSettings()
	settings.MetadataRetentionDays = 30
	if err := store.UpdateAuditSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SweepExpiredAuditData(ctx); err != nil {
		t.Fatal(err)
	}
	if got := remaining(); !slices.Equal(got, []contract.SessionID{"session_kept", "session_purged", "session_resumed"}) {
		t.Fatalf("after the retention sweep stand-ins remain for %v", got)
	}
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{
		Scope: contract.PurgeScopeSessions, Confirm: true, SessionIDs: []contract.SessionID{"session_purged"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := remaining(); !slices.Equal(got, []contract.SessionID{"session_kept", "session_resumed"}) {
		t.Fatalf("after purging a conversation stand-ins remain for %v", got)
	}
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{Scope: contract.PurgeScopeAll, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if got := remaining(); len(got) != 0 {
		t.Fatalf("after purging every record stand-ins remain for %v", got)
	}
}
