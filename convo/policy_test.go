package convo

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type lookupCall struct {
	kind   Kind
	values []string
	scope  Scope
}

func recordingLookup(calls *[]lookupCall, hits map[Kind]Match) Lookup {
	return func(_ context.Context, kind Kind, values []string, scope Scope) (Match, bool, error) {
		*calls = append(*calls, lookupCall{kind: kind, values: values, scope: scope})
		match, ok := hits[kind]
		return match, ok, nil
	}
}

func TestResolveLayerOrderAndScopes(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	policy := DefaultPolicy()
	fp := NewFingerprinter([]byte("k"))
	digest := DigestText(goroutineAnswerZH, true)
	summary := RequestSummary{
		ExplicitCursors: []string{"conv_1"},
		EchoIDs:         []string{"call_7f3a9c2e1b4d4e8fa1c2"},
		AssistantDigest: digest,
		UserTurnCount:   2,
		HasUserMessage:  true,
	}

	var calls []lookupCall
	decision, err := policy.Resolve(context.Background(), summary, fp, recordingLookup(&calls, map[Kind]Match{}), now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Matched {
		t.Fatal("no hits must not match")
	}
	if len(calls) != 3 {
		t.Fatalf("expected 3 lookups, got %d", len(calls))
	}
	if calls[0].kind != KindExplicit || calls[0].scope != (Scope{}) {
		t.Fatalf("explicit layer scope = %+v", calls[0])
	}
	wantScope := Scope{SamePrincipal: true, NotBefore: now.Add(-policy.Window)}
	if calls[1].kind != KindEchoID || calls[1].scope != wantScope {
		t.Fatalf("echo layer = %+v", calls[1])
	}
	if calls[2].kind != KindFingerprint || calls[2].scope != wantScope || calls[2].values[0] != fp.Fingerprint(digest) {
		t.Fatalf("fingerprint layer = %+v", calls[2])
	}
	if got := *decision.TurnIndex; got != 2 {
		t.Fatalf("TurnIndex = %d, want 2", got)
	}
	wantInbound := []Cursor{
		{KindExplicit, DirectionIn, "conv_1"},
		{KindEchoID, DirectionIn, "call_7f3a9c2e1b4d4e8fa1c2"},
		{KindFingerprint, DirectionIn, fp.Fingerprint(digest)},
	}
	if !reflect.DeepEqual(decision.Inbound, wantInbound) {
		t.Fatalf("Inbound = %+v", decision.Inbound)
	}
	if got := decision.PersistentInbound(); !reflect.DeepEqual(got, wantInbound[:1]) {
		t.Fatalf("PersistentInbound = %+v", got)
	}

	// Explicit hit stops the cascade.
	calls = nil
	decision, err = policy.Resolve(context.Background(), summary, fp, recordingLookup(&calls, map[Kind]Match{
		KindExplicit: {SessionID: "s1", TurnIndex: 3, HasTurnIndex: true},
		KindEchoID:   {SessionID: "s2"},
	}), now)
	if err != nil || !decision.Matched || decision.Match.SessionID != "s1" || decision.Match.Kind != KindExplicit {
		t.Fatalf("decision = %+v err = %v", decision, err)
	}
	if len(calls) != 1 {
		t.Fatalf("explicit hit must stop lookups, got %d calls", len(calls))
	}
	if len(decision.Inbound) != 3 {
		t.Fatal("Inbound must still list every layer")
	}

	// Echo hit when explicit misses.
	calls = nil
	decision, _ = policy.Resolve(context.Background(), summary, fp, recordingLookup(&calls, map[Kind]Match{KindEchoID: {SessionID: "s2"}}), now)
	if decision.Match.SessionID != "s2" || decision.Match.Kind != KindEchoID || len(calls) != 2 {
		t.Fatalf("decision = %+v calls = %d", decision, len(calls))
	}

	// Fingerprint layer is skipped without a fingerprinter.
	calls = nil
	policy.Resolve(context.Background(), summary, nil, recordingLookup(&calls, nil), now)
	if len(calls) != 2 {
		t.Fatalf("nil fingerprinter must skip the fingerprint layer, got %d calls", len(calls))
	}

	// Zero window disables NotBefore.
	unbounded := policy
	unbounded.Window = 0
	calls = nil
	unbounded.Resolve(context.Background(), summary, nil, recordingLookup(&calls, nil), now)
	if !calls[1].scope.NotBefore.IsZero() || !calls[1].scope.SamePrincipal {
		t.Fatalf("zero window scope = %+v", calls[1].scope)
	}
}

func TestResolveErrorsAndNilLookup(t *testing.T) {
	policy := DefaultPolicy()
	summary := RequestSummary{ExplicitCursors: []string{"x"}, UserTurnCount: 1, HasUserMessage: true}
	boom := errors.New("db down")
	_, err := policy.Resolve(context.Background(), summary, nil, func(context.Context, Kind, []string, Scope) (Match, bool, error) {
		return Match{}, false, boom
	}, time.Now())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	decision, err := policy.Resolve(context.Background(), summary, nil, nil, time.Now())
	if err != nil || decision.Matched || *decision.TurnIndex != 1 || len(decision.Inbound) != 1 {
		t.Fatalf("nil lookup decision = %+v err = %v", decision, err)
	}
	// A lookup that says ok but returns an empty session id is ignored.
	decision, _ = policy.Resolve(context.Background(), summary, nil, func(context.Context, Kind, []string, Scope) (Match, bool, error) {
		return Match{}, true, nil
	}, time.Now())
	if decision.Matched {
		t.Fatal("empty session id must not count as a match")
	}
}

func TestNextTurnIndex(t *testing.T) {
	policy := DefaultPolicy()
	ptr := func(n int) *int { return &n }
	cases := []struct {
		name    string
		summary RequestSummary
		matched *Match
		want    *int
	}{
		{"replayed history counts user messages", RequestSummary{UserTurnCount: 3, HasUserMessage: true}, nil, ptr(3)},
		{"replayed history ignores match", RequestSummary{UserTurnCount: 3, HasUserMessage: true}, &Match{Kind: KindEchoID, TurnIndex: 7, HasTurnIndex: true}, ptr(3)},
		{"stateful with user message increments", RequestSummary{Stateful: true, UserTurnCount: 1, HasUserMessage: true}, &Match{Kind: KindExplicit, TurnIndex: 4, HasTurnIndex: true}, ptr(5)},
		{"stateful tool output keeps turn", RequestSummary{Stateful: true}, &Match{Kind: KindExplicit, TurnIndex: 4, HasTurnIndex: true}, ptr(4)},
		{"stateful without matched turn falls back", RequestSummary{Stateful: true, UserTurnCount: 1, HasUserMessage: true}, &Match{Kind: KindExplicit}, ptr(1)},
		{"stateful matched via echo falls back", RequestSummary{Stateful: true, UserTurnCount: 1, HasUserMessage: true}, &Match{Kind: KindEchoID, TurnIndex: 4, HasTurnIndex: true}, ptr(1)},
		{"no user turns", RequestSummary{}, nil, nil},
		{"stateful tool output without match", RequestSummary{Stateful: true}, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := policy.NextTurnIndex(tc.summary, tc.matched)
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("NextTurnIndex = %v, want %v", deref(got), deref(tc.want))
			}
		})
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestOutputCursors(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxOutputEchoIDs = 2
	fp := NewFingerprinter([]byte("k"))
	digest := DigestText(goroutineAnswerZH, true)
	summary := ResponseSummary{
		OutputID:        " resp_abc ",
		EchoIDs:         []string{"call_aaaabbbbccccdddd1", "bad\nvalue", "call_aaaabbbbccccdddd2", "call_aaaabbbbccccdddd3"},
		AssistantDigest: digest,
	}
	got := policy.OutputCursors(summary, fp)
	want := []Cursor{
		{KindExplicit, DirectionOut, "resp_abc"},
		{KindEchoID, DirectionOut, "call_aaaabbbbccccdddd1"},
		{KindEchoID, DirectionOut, "call_aaaabbbbccccdddd2"},
		{KindFingerprint, DirectionOut, fp.Fingerprint(digest)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OutputCursors = %+v, want %+v", got, want)
	}
	for _, cursor := range got {
		if err := cursor.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if got := policy.OutputCursors(ResponseSummary{}, fp); len(got) != 0 {
		t.Fatalf("empty summary produced %+v", got)
	}
	if got := policy.OutputCursors(summary, nil); got[len(got)-1].Kind == KindFingerprint {
		t.Fatal("nil fingerprinter must not emit fingerprints")
	}
}

func TestCursorValidate(t *testing.T) {
	bad := []Cursor{
		{Kind: "weird", Direction: DirectionIn, Value: "abc"},
		{Kind: KindEchoID, Direction: "sideways", Value: "abc"},
		{Kind: KindEchoID, Direction: DirectionIn, Value: ""},
		{Kind: KindEchoID, Direction: DirectionIn, Value: "has\ncontrol"},
		{Kind: KindEchoID, Direction: DirectionIn, Value: string(make([]byte, 300))},
	}
	for _, cursor := range bad {
		if cursor.Validate() == nil {
			t.Fatalf("expected %+v to be invalid", cursor)
		}
	}
	if err := (Cursor{KindExplicit, DirectionOut, "resp_1"}).Validate(); err != nil {
		t.Fatal(err)
	}
}
