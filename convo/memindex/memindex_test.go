package memindex

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/convo"
)

func TestLookupScopesAndDirections(t *testing.T) {
	base := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	index := New(Options{})
	index.Put(Record{
		SessionID: "s-old", Principal: "tok-a", At: base.Add(-2 * time.Hour), Turn: &convo.TurnState{Index: 1, UserMessages: 1},
		Cursors: []convo.Cursor{{Kind: convo.KindEchoID, Direction: convo.DirectionOut, Value: "call_old"}},
	})
	index.Put(Record{
		SessionID: "s-new", Principal: "tok-a", At: base.Add(-1 * time.Hour), Turn: &convo.TurnState{Index: 2, UserMessages: 2, LastUserFingerprint: "fp1_user"},
		Cursors: []convo.Cursor{
			{Kind: convo.KindEchoID, Direction: convo.DirectionOut, Value: "call_new"},
			{Kind: convo.KindExplicit, Direction: convo.DirectionIn, Value: "conv_1"},
			{Kind: convo.KindFingerprint, Direction: convo.DirectionIn, Value: "fp1_inbound"},
		},
	})
	ctx := context.Background()

	match, ok, err := index.Lookup(ctx, "tok-a", convo.KindEchoID, []string{"call_old", "call_new"}, convo.Scope{SamePrincipal: true})
	if err != nil || !ok || match.SessionID != "s-new" || match.Value != "call_new" {
		t.Fatalf("most recent record must win: %+v %v %v", match, ok, err)
	}
	if match.Turn == nil || *match.Turn != (convo.TurnState{Index: 2, UserMessages: 2, LastUserFingerprint: "fp1_user"}) {
		t.Fatalf("Turn = %+v, want the stored state", match.Turn)
	}
	if _, ok, _ := index.Lookup(ctx, "tok-b", convo.KindEchoID, []string{"call_new"}, convo.Scope{SamePrincipal: true}); ok {
		t.Fatal("other principal must not match")
	}
	if _, ok, _ := index.Lookup(ctx, "tok-b", convo.KindEchoID, []string{"call_new"}, convo.Scope{}); !ok {
		t.Fatal("unscoped lookup must ignore principal")
	}
	if _, ok, _ := index.Lookup(ctx, "tok-a", convo.KindEchoID, []string{"call_new"}, convo.Scope{NotBefore: base.Add(-30 * time.Minute)}); ok {
		t.Fatal("NotBefore must exclude older records")
	}
	if _, ok, _ := index.Lookup(ctx, "", convo.KindExplicit, []string{"conv_1"}, convo.Scope{}); !ok {
		t.Fatal("explicit cursors must match in the inbound direction")
	}
	if _, ok, _ := index.Lookup(ctx, "tok-a", convo.KindFingerprint, []string{"fp1_inbound"}, convo.Scope{SamePrincipal: true}); ok {
		t.Fatal("non-explicit inbound cursors must not match")
	}
	if _, ok, _ := index.Lookup(ctx, "tok-a", convo.KindExplicit, []string{"call_new"}, convo.Scope{}); ok {
		t.Fatal("kind must match")
	}

	// A newer record that consumed resp_1 must not outrank the record that
	// produced it: the continuation builds on the producer's turn.
	index.Put(Record{
		SessionID: "s-producer", Principal: "tok-a", At: base.Add(-50 * time.Minute), Turn: &convo.TurnState{Index: 1, UserMessages: 1},
		Cursors: []convo.Cursor{{Kind: convo.KindExplicit, Direction: convo.DirectionOut, Value: "resp_1"}},
	})
	index.Put(Record{
		SessionID: "s-consumer", Principal: "tok-a", At: base.Add(-40 * time.Minute), Turn: &convo.TurnState{Index: 7, UserMessages: 7},
		Cursors: []convo.Cursor{{Kind: convo.KindExplicit, Direction: convo.DirectionIn, Value: "resp_1"}},
	})
	match, ok, _ = index.Lookup(ctx, "tok-a", convo.KindExplicit, []string{"resp_1"}, convo.Scope{})
	if !ok || match.SessionID != "s-producer" || match.Turn == nil || match.Turn.Index != 1 {
		t.Fatalf("producer must outrank newer consumer: %+v", match)
	}
}

func TestEviction(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	index := New(Options{TTL: time.Hour, MaxRecords: 2, Now: func() time.Time { return now }})
	put := func(id string, age time.Duration) {
		index.Put(Record{SessionID: id, At: now.Add(-age), Cursors: []convo.Cursor{{Kind: convo.KindExplicit, Direction: convo.DirectionOut, Value: id}}})
	}
	put("a", 90*time.Minute)
	put("b", 10*time.Minute)
	put("c", 5*time.Minute)
	if index.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (a expired)", index.Len())
	}
	put("d", time.Minute)
	if index.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (b evicted by size)", index.Len())
	}
	if _, ok, _ := index.Lookup(context.Background(), "", convo.KindExplicit, []string{"b"}, convo.Scope{}); ok {
		t.Fatal("evicted record must be gone from the value index")
	}
	if _, ok, _ := index.Lookup(context.Background(), "", convo.KindExplicit, []string{"d"}, convo.Scope{}); !ok {
		t.Fatal("newest record must remain")
	}
}
