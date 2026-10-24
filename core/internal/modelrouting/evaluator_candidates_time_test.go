package modelrouting

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func clock(t *testing.T, hour, minute int) *time.Time {
	t.Helper()
	value := time.Date(2026, 10, 13, hour, minute, 0, 0, time.Local)
	return &value
}

// The half-open [start, end) semantics are asserted here at runtime, not
// derived: every boundary minute below goes through Evaluate end to end.
func TestEvaluateSystemTimeWithinHalfOpenBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		within []string
		at     *time.Time
		want   triState
	}{
		// Cross-midnight ["22:00","02:00") must hit through the night.
		{name: "cross-midnight start inclusive", within: []string{"22:00", "02:00"}, at: clock(t, 22, 0), want: triTrue},
		{name: "cross-midnight late evening", within: []string{"22:00", "02:00"}, at: clock(t, 23, 30), want: triTrue},
		{name: "cross-midnight after midnight", within: []string{"22:00", "02:00"}, at: clock(t, 0, 30), want: triTrue},
		{name: "cross-midnight minute before end", within: []string{"22:00", "02:00"}, at: clock(t, 1, 59), want: triTrue},
		{name: "cross-midnight end exclusive", within: []string{"22:00", "02:00"}, at: clock(t, 2, 0), want: triFalse},
		{name: "cross-midnight after end", within: []string{"22:00", "02:00"}, at: clock(t, 3, 0), want: triFalse},
		{name: "cross-midnight minute before start", within: []string{"22:00", "02:00"}, at: clock(t, 21, 59), want: triFalse},
		// Same-day window: start inclusive, end exclusive.
		{name: "same-day start inclusive", within: []string{"09:00", "12:00"}, at: clock(t, 9, 0), want: triTrue},
		{name: "same-day minute before end", within: []string{"09:00", "12:00"}, at: clock(t, 11, 59), want: triTrue},
		{name: "same-day end exclusive", within: []string{"09:00", "12:00"}, at: clock(t, 12, 0), want: triFalse},
		{name: "same-day before start", within: []string{"09:00", "12:00"}, at: clock(t, 8, 59), want: triFalse},
		// Equal endpoints name one single instant: exactly that minute hits,
		// the minutes on either side do not.
		{name: "single instant hits", within: []string{"09:00", "09:00"}, at: clock(t, 9, 0), want: triTrue},
		{name: "single instant before misses", within: []string{"09:00", "09:00"}, at: clock(t, 8, 59), want: triFalse},
		{name: "single instant after misses", within: []string{"09:00", "09:00"}, at: clock(t, 9, 1), want: triFalse},
		// The second frozen contract shape, cross-midnight ["22:00","06:00"].
		{name: "contract shape overnight hit", within: []string{"22:00", "06:00"}, at: clock(t, 5, 59), want: triTrue},
		{name: "contract shape morning end exclusive", within: []string{"22:00", "06:00"}, at: clock(t, 6, 0), want: triFalse},
		{name: "contract shape evening start inclusive", within: []string{"22:00", "06:00"}, at: clock(t, 22, 0), want: triTrue},
	}
	for _, testCase := range tests {
		condition := contract.ModelRoutingCondition{Field: "system_time", Within: testCase.within}
		if err := condition.Validate(); err != nil {
			t.Fatalf("%s: condition rejected by contract validation: %v", testCase.name, err)
		}
		state, observations := evaluateCondition(condition, contract.ModelRoutingFacts{SystemTime: testCase.at})
		if state != testCase.want {
			t.Errorf("%s: state=%v want=%v", testCase.name, state, testCase.want)
		}
		if len(observations) != 1 || observations[0].Field != "system_time" || !observations[0].Known {
			t.Errorf("%s: observations=%+v", testCase.name, observations)
		}
	}
}

// A missing clock sample keeps the leaf triUnknown: without a fact the row is
// skipped, never matched, and the default member wins.
func TestEvaluateSystemTimeUnknownWithoutClockSample(t *testing.T) {
	condition := contract.ModelRoutingCondition{Field: "system_time", Within: []string{"09:00", "12:00"}}
	state, observations := evaluateCondition(condition, contract.ModelRoutingFacts{})
	if state != triUnknown {
		t.Fatalf("state=%v want triUnknown", state)
	}
	if len(observations) != 1 || observations[0].SkipReason != "unknown" {
		t.Fatalf("observations=%+v", observations)
	}
	group := evaluationGroup([]contract.ModelRoutingRule{{
		ID: "morning", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: "system_time", Within: []string{"00:00", "23:59"}},
		TargetMemberID: "strong",
	}})
	selection := Evaluate(group, contract.ModelRoutingFacts{})
	if !selection.UsedDefault || selection.Member.ID != "fast" {
		t.Fatalf("selection = %+v, want default fast member", selection)
	}
}

// Scored fallback: a row referencing task_type tries each ranked candidate in
// order and adopts the first that makes the whole row true.
func TestEvaluateTaskTypeCandidatesSubstitutionAdoptsFirstMatchingRank(t *testing.T) {
	group := evaluationGroup([]contract.ModelRoutingRule{{
		ID: "research", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"research", "architect"}},
		TargetMemberID: "strong",
	}})
	facts := contract.ModelRoutingFacts{TaskTypeCandidates: []string{"coding", "research", "general"}}
	selection := Evaluate(group, facts)
	if selection.RuleID != "research" || selection.Member.ID != "strong" {
		t.Fatalf("selection = %+v, want research rule via rank 1", selection)
	}
	if selection.TaskTypeCandidateIndex == nil || *selection.TaskTypeCandidateIndex != 1 {
		t.Fatalf("adopted candidate index = %v, want 1", selection.TaskTypeCandidateIndex)
	}
}

// H3 core regression: row order is the outer order. An earlier row that only
// matches a lower-ranked candidate must win over a later row matching the
// top-ranked (higher-scoring) candidate; candidates never leapfrog rows.
func TestEvaluateHighScoringCandidateDoesNotLeapfrogEarlierRow(t *testing.T) {
	group := evaluationGroup([]contract.ModelRoutingRule{
		{ID: "research-row", Priority: 10, When: contract.ModelRoutingCondition{Field: "task_type", In: []string{"research"}}, TargetMemberID: "late"},
		{ID: "coding-row", Priority: 20, When: contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}}, TargetMemberID: "strong"},
	})
	// coding ranks first (score 0.9), research second (0.6): the coding row is
	// later in row order, so the research row must still win.
	facts := contract.ModelRoutingFacts{TaskTypeCandidates: []string{"coding", "research"}}
	selection := Evaluate(group, facts)
	if selection.RuleID != "research-row" || selection.Member.ID != "late" {
		t.Fatalf("selection = %+v, want research-row via rank 1", selection)
	}
	if selection.TaskTypeCandidateIndex == nil || *selection.TaskTypeCandidateIndex != 1 {
		t.Fatalf("adopted candidate index = %v, want 1", selection.TaskTypeCandidateIndex)
	}
}

// Substitution respects the rest of the row: a candidate only adopts when the
// whole row becomes true, and other leaves keep their tri-state semantics.
func TestEvaluateTaskTypeCandidatesWithConjunction(t *testing.T) {
	group := evaluationGroup([]contract.ModelRoutingRule{{
		ID: "night-coding", Priority: 1,
		When: contract.ModelRoutingCondition{All: []contract.ModelRoutingCondition{
			{Field: "task_type", In: []string{"coding"}},
			{Field: "system_time", Within: []string{"22:00", "06:00"}},
		}},
		TargetMemberID: "strong",
	}})
	facts := contract.ModelRoutingFacts{
		TaskTypeCandidates: []string{"research", "coding"},
		SystemTime:         clock(t, 23, 0),
	}
	selection := Evaluate(group, facts)
	if selection.RuleID != "night-coding" || selection.Member.ID != "strong" {
		t.Fatalf("selection = %+v, want night-coding via rank 1", selection)
	}
	// Same candidates during the day: no rank makes the row true, the row is
	// skipped and the default member wins.
	facts.SystemTime = clock(t, 12, 0)
	selection = Evaluate(group, facts)
	if !selection.UsedDefault || selection.Member.ID != "fast" {
		t.Fatalf("daytime selection = %+v, want default fast member", selection)
	}
}

// A not leaf inverts per candidate: the first candidate that makes the
// negation true is the adopted one.
func TestEvaluateTaskTypeCandidatesThroughNotLeaf(t *testing.T) {
	inner := contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}}
	group := evaluationGroup([]contract.ModelRoutingRule{{
		ID: "not-coding", Priority: 1,
		When:           contract.ModelRoutingCondition{Not: &inner},
		TargetMemberID: "strong",
	}})
	facts := contract.ModelRoutingFacts{TaskTypeCandidates: []string{"coding", "research"}}
	selection := Evaluate(group, facts)
	if selection.RuleID != "not-coding" || selection.Member.ID != "strong" {
		t.Fatalf("selection = %+v, want not-coding via rank 1", selection)
	}
	if selection.TaskTypeCandidateIndex == nil || *selection.TaskTypeCandidateIndex != 1 {
		t.Fatalf("adopted candidate index = %v, want 1", selection.TaskTypeCandidateIndex)
	}
}

// Unknown sibling leaves stay unknown per probe: a candidate row that is
// unknown rather than false is skipped, not selected.
func TestEvaluateTaskTypeCandidatesPreserveUnknownSiblings(t *testing.T) {
	group := evaluationGroup([]contract.ModelRoutingRule{{
		ID: "needs-unknown", Priority: 1,
		When: contract.ModelRoutingCondition{All: []contract.ModelRoutingCondition{
			{Field: "task_type", In: []string{"coding"}},
			{Field: "has_tools", EqualsBool: boolRef(true)},
		}},
		TargetMemberID: "strong",
	}})
	facts := contract.ModelRoutingFacts{TaskTypeCandidates: []string{"coding"}}
	selection := Evaluate(group, facts)
	if !selection.UsedDefault || selection.Member.ID != "fast" {
		t.Fatalf("selection = %+v, want default fast member for an unknown row", selection)
	}
	if selection.TaskTypeCandidateIndex != nil {
		t.Fatalf("unknown row reported an adopted candidate: %+v", selection.TaskTypeCandidateIndex)
	}
}

// Rows without a task_type reference never enter the substitution loop, with
// or without candidates present.
func TestEvaluateCandidatesIgnoredForRowsWithoutTaskType(t *testing.T) {
	group := evaluationGroup([]contract.ModelRoutingRule{{
		ID: "streaming", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: "streaming", EqualsBool: boolRef(false)},
		TargetMemberID: "strong",
	}})
	selection := Evaluate(group, contract.ModelRoutingFacts{TaskTypeCandidates: []string{"coding"}, Streaming: boolRef(false)})
	if selection.RuleID != "streaming" || selection.Member.ID != "strong" || selection.TaskTypeCandidateIndex != nil {
		t.Fatalf("selection = %+v, want streaming rule without candidate adoption", selection)
	}
}

// Empty TaskTypeCandidates keeps the single-value path byte-identical: the
// serialized selection equals the golden computed from pre-candidates
// semantics for the same facts.
func TestEvaluateEmptyCandidatesStaysByteIdentical(t *testing.T) {
	group := evaluationGroup([]contract.ModelRoutingRule{
		{ID: "coding", Priority: 1, When: contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}}, TargetMemberID: "strong"},
		{ID: "tools", Priority: 2, When: contract.ModelRoutingCondition{Field: "has_tools", EqualsBool: boolRef(true)}, TargetMemberID: "late"},
	})
	facts := contract.ModelRoutingFacts{TaskType: "coding", HasTools: boolRef(false)}
	golden := Evaluate(group, facts)
	goldenBytes, err := json.Marshal(golden)
	if err != nil {
		t.Fatal(err)
	}
	// The same facts with an explicit empty candidate sequence must produce
	// the identical bytes: the guard keeps the original path.
	withEmpty := facts
	withEmpty.TaskTypeCandidates = []string{}
	selection := Evaluate(group, withEmpty)
	selectionBytes, err := json.Marshal(selection)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(golden, selection) || string(goldenBytes) != string(selectionBytes) {
		t.Fatalf("empty candidates changed evaluation:\ngolden=%s\nactual=%s", goldenBytes, selectionBytes)
	}
	if golden.TaskTypeCandidateIndex != nil {
		t.Fatalf("single-value path reported a candidate index: %+v", golden.TaskTypeCandidateIndex)
	}
	// And the selected member is the same one the single value picks.
	if golden.RuleID != "coding" || golden.Member.ID != "strong" {
		t.Fatalf("golden selection = %+v, want coding rule on strong", golden)
	}
}
