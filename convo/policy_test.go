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
	if decision.Turn == nil || decision.Turn.Index != 1 || decision.Turn.UserMessages != 2 || decision.Turn.LastUserFingerprint != "" {
		t.Fatalf("Turn = %+v, want index 1 with 2 user messages (no user digest to fingerprint)", decision.Turn)
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
		KindExplicit: {SessionID: "s1", Turn: &TurnState{Index: 3, UserMessages: 2}},
		KindEchoID:   {SessionID: "s2"},
	}), now)
	if err != nil || !decision.Matched || decision.Match.SessionID != "s1" || decision.Match.Kind != KindExplicit {
		t.Fatalf("decision = %+v err = %v", decision, err)
	}
	if decision.Turn == nil || decision.Turn.Index != 3 {
		t.Fatalf("Turn = %+v, want the matched turn 3 (same user-message count, no digest)", decision.Turn)
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
	if err != nil || decision.Matched || decision.Turn == nil || decision.Turn.Index != 1 || len(decision.Inbound) != 1 {
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

func TestNextTurn(t *testing.T) {
	policy := DefaultPolicy()
	replay := func(users int) RequestSummary {
		return RequestSummary{UserTurnCount: users, HasUserMessage: users > 0}
	}
	stateful := func(users int) RequestSummary {
		return RequestSummary{Stateful: true, UserTurnCount: users, HasUserMessage: users > 0}
	}
	matched := func(turn TurnState) *Match { return &Match{Kind: KindEchoID, Turn: &turn} }
	cases := []struct {
		name        string
		summary     RequestSummary
		matched     *Match
		fingerprint string
		want        *TurnState
	}{
		// The first record is turn 1 no matter how much history it replays:
		// harness notes, skill text, or a conversation started elsewhere.
		{"first request is turn 1", replay(3), nil, "fp_a", &TurnState{1, 3, "fp_a"}},
		{"first request without fingerprinter", replay(3), nil, "", &TurnState{1, 3, ""}},
		{"no user turns", replay(0), nil, "", nil},
		{"matched record without turn restarts", replay(3), &Match{Kind: KindEchoID}, "fp_a", &TurnState{1, 3, "fp_a"}},
		// Agent loop: the next call appends assistant + tool items only.
		{"same count same text keeps turn", replay(3), matched(TurnState{4, 3, "fp_a"}), "fp_a", &TurnState{4, 3, "fp_a"}},
		{"same count without fingerprints keeps turn", replay(3), matched(TurnState{4, 3, ""}), "", &TurnState{4, 3, ""}},
		{"same count, one side unfingerprinted, keeps turn", replay(3), matched(TurnState{4, 3, ""}), "fp_b", &TurnState{4, 3, "fp_b"}},
		// A person typed again.
		{"more user messages starts a turn", replay(4), matched(TurnState{4, 3, "fp_a"}), "fp_b", &TurnState{5, 4, "fp_b"}},
		// Compaction: fewer messages but a new prompt at the end.
		{"fewer messages with new text starts a turn", replay(2), matched(TurnState{4, 3, "fp_a"}), "fp_b", &TurnState{5, 2, "fp_b"}},
		{"fewer messages same text keeps turn", replay(2), matched(TurnState{4, 3, "fp_a"}), "fp_a", &TurnState{4, 2, "fp_a"}},
		// Stateful Responses chain: the body is a delta.
		{"stateful user delta increments", stateful(1), &Match{Kind: KindExplicit, Turn: &TurnState{4, 6, "fp_a"}}, "fp_b", &TurnState{5, 7, "fp_b"}},
		{"stateful tool output keeps turn", stateful(0), &Match{Kind: KindExplicit, Turn: &TurnState{4, 6, "fp_a"}}, "", &TurnState{4, 6, "fp_a"}},
		{"stateful without matched turn restarts", stateful(1), &Match{Kind: KindExplicit}, "fp_a", &TurnState{1, 1, "fp_a"}},
		{"stateful tool output without match", stateful(0), nil, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := policy.NextTurn(tc.summary, tc.matched, tc.fingerprint)
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("NextTurn = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveFingerprintsLastUserText(t *testing.T) {
	policy := DefaultPolicy()
	fp := NewFingerprinter([]byte("k"))
	prompt := RequestSummary{UserTurnCount: 1, HasUserMessage: true, LastUserDigest: DigestText("list TODOs", false)}
	decision, err := policy.Resolve(context.Background(), prompt, fp, nil, time.Now())
	if err != nil || decision.Turn == nil || decision.Turn.LastUserFingerprint != fp.Fingerprint(prompt.LastUserDigest) {
		t.Fatalf("decision = %+v err = %v", decision, err)
	}
	// A harness compacts the history down to one message that is a new prompt:
	// the count did not grow, but the text did change.
	edited := RequestSummary{UserTurnCount: 1, HasUserMessage: true, LastUserDigest: DigestText("now fix them", false)}
	lookup := func(context.Context, Kind, []string, Scope) (Match, bool, error) {
		return Match{SessionID: "s1", Turn: decision.Turn}, true, nil
	}
	edited.EchoIDs = []string{"call_7f3a9c2e1b4d4e8fa1c2"}
	next, err := policy.Resolve(context.Background(), edited, fp, lookup, time.Now())
	if err != nil || !next.Matched || next.Turn == nil || next.Turn.Index != 2 {
		t.Fatalf("changed user text at equal count must start a turn: %+v err = %v", next, err)
	}
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
