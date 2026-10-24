package modelrouting

import (
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/classifieroutput"
)

// A ranked sequence that omits the primary answer is completed, not trusted:
// the primary merges in with its own score and lands at its score position,
// instead of being displaced by the mere presence of candidates.
func TestTaskTypeCandidateScoresMergesPrimaryAbsentFromRanked(t *testing.T) {
	group := contract.ModelRoutingGroup{}
	outcome := TaskTypeOutcome{
		Value:      "coding",
		Confidence: 0.95,
		Candidates: []autoclassifier.Candidate{
			{Label: "research", Score: 0.9},
			{Label: "architect", Score: 0.8},
		},
	}
	want := []autoclassifier.Candidate{
		{Label: "coding", Score: 0.95},
		{Label: "research", Score: 0.9},
		{Label: "architect", Score: 0.8},
	}
	if got := TaskTypeCandidateScores(outcome, group); !reflect.DeepEqual(got, want) {
		t.Fatalf("scores = %+v, want the primary merged at its score position: %+v", got, want)
	}
	if labels := TaskTypeCandidateLabels(outcome, group); !reflect.DeepEqual(labels, []string{"coding", "research", "architect"}) {
		t.Fatalf("labels = %v, want the primary present ahead of the ranked tail", labels)
	}
	// The merge must not touch the producer's original slice.
	original := []autoclassifier.Candidate{{Label: "research", Score: 0.9}, {Label: "architect", Score: 0.8}}
	if !reflect.DeepEqual(outcome.Candidates, original) {
		t.Fatalf("input candidates mutated: %+v", outcome.Candidates)
	}
}

// End to end over the real strict parser: the existing_model path copies
// parsed.Label and parsed.Candidates onto separate Outcome fields, and the
// conversion must still surface the parsed primary.
func TestTaskTypeCandidateLabelsSurviveParseTaskType(t *testing.T) {
	labels := contract.EffectiveModelRoutingTaskTypeLabels(nil)
	parsed, err := classifieroutput.ParseTaskType(`{`+
		`"label":"coding","score":0.95,`+
		`"candidates":[{"label":"research","score":0.9},{"label":"architect","score":0.8}]}`, labels)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Label != "coding" {
		t.Fatalf("parsed label = %q, want coding", parsed.Label)
	}
	converted := make([]autoclassifier.Candidate, 0, len(parsed.Candidates))
	for _, candidate := range parsed.Candidates {
		converted = append(converted, autoclassifier.Candidate{Label: candidate.Label, Score: candidate.Score})
	}
	outcome := TaskTypeOutcome{Value: parsed.Label, Confidence: parsed.Score, Candidates: converted}
	if labels := TaskTypeCandidateLabels(outcome, contract.ModelRoutingGroup{}); !reflect.DeepEqual(labels, []string{"coding", "research", "architect"}) {
		t.Fatalf("labels = %v, want the parsed primary present", labels)
	}
}

// Idempotence: a primary already inside the ranked sequence is a no-op. The
// filtered sequence keeps the producer's order exactly — no re-sort — so the
// cloud-shaped outcomes stay byte-identical to the pre-merge behavior, and
// single-value sources keep their synthesized one-entry form.
func TestTaskTypeCandidateScoresPrimaryPresentStaysProducerOrder(t *testing.T) {
	group := contract.ModelRoutingGroup{}
	// Deliberately not score-ordered: the idempotent path must not sort it.
	ranked := []autoclassifier.Candidate{
		{Label: "general", Score: 0.7},
		{Label: "coding", Score: 0.9},
	}
	outcome := TaskTypeOutcome{Value: "coding", Confidence: 0.9, Candidates: ranked}
	if got := TaskTypeCandidateScores(outcome, group); !reflect.DeepEqual(got, ranked) {
		t.Fatalf("scores = %+v, want the producer order untouched: %+v", got, ranked)
	}
	single := TaskTypeOutcome{Value: "coding", Confidence: 0.9}
	if got := TaskTypeCandidateScores(single, group); !reflect.DeepEqual(got, []autoclassifier.Candidate{{Label: "coding", Score: 0.9}}) {
		t.Fatalf("single-value scores = %+v", got)
	}
}

// The merged primary passes the same threshold filter as the ranked entries:
// a below-threshold primary drops without reordering the survivors.
func TestTaskTypeCandidateScoresMergedPrimaryFilteredByThreshold(t *testing.T) {
	group := contract.ModelRoutingGroup{}
	outcome := TaskTypeOutcome{
		Value:                  "coding",
		Confidence:             0.2,
		LowConfidenceThreshold: 0.5,
		Candidates: []autoclassifier.Candidate{
			{Label: "research", Score: 0.9},
			{Label: "architect", Score: 0.8},
		},
	}
	want := []autoclassifier.Candidate{
		{Label: "research", Score: 0.9},
		{Label: "architect", Score: 0.8},
	}
	if got := TaskTypeCandidateScores(outcome, group); !reflect.DeepEqual(got, want) {
		t.Fatalf("scores = %+v, want the below-threshold primary dropped: %+v", got, want)
	}
}

// Ordering contract: when the merged primary scores below the ranked entries
// it ranks last, the observation ranks line up with the substituted
// sequence, and the adopted index marks exactly that one row as chosen.
func TestTaskTypeCandidateScoresPrimaryLowestRanksLastAndChosenAlone(t *testing.T) {
	group := evaluationGroup([]contract.ModelRoutingRule{{
		ID: "architect-row", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"architect"}},
		TargetMemberID: "strong",
	}})
	outcome := TaskTypeOutcome{
		Value:      "architect",
		Confidence: 0.3,
		Candidates: []autoclassifier.Candidate{
			{Label: "coding", Score: 0.9},
			{Label: "research", Score: 0.8},
		},
	}
	want := []autoclassifier.Candidate{
		{Label: "coding", Score: 0.9},
		{Label: "research", Score: 0.8},
		{Label: "architect", Score: 0.3},
	}
	if got := TaskTypeCandidateScores(outcome, group); !reflect.DeepEqual(got, want) {
		t.Fatalf("scores = %+v, want the primary ranked last: %+v", got, want)
	}
	facts := contract.ModelRoutingFacts{TaskTypeCandidates: TaskTypeCandidateLabels(outcome, group)}
	selection := Evaluate(group, facts)
	if selection.RuleID != "architect-row" || selection.TaskTypeCandidateIndex == nil || *selection.TaskTypeCandidateIndex != 2 {
		t.Fatalf("selection = %+v, want architect-row adopting rank 2", selection)
	}
	observations := TaskTypeCandidateObservations(outcome, group, selection.TaskTypeCandidateIndex)
	wantObservations := []contract.ModelRoutingCandidateObservation{
		{Value: "coding", Score: 0.9, Rank: 0, Chosen: false},
		{Value: "research", Score: 0.8, Rank: 1, Chosen: false},
		{Value: "architect", Score: 0.3, Rank: 2, Chosen: true},
	}
	if !reflect.DeepEqual(observations, wantObservations) {
		t.Fatalf("observations = %+v, want exactly rank 2 chosen: %+v", observations, wantObservations)
	}
}

// Ties break by the group's effective label order — the same order the
// classification request listed — never by primary status.
func TestTaskTypeCandidateScoresTieBreaksByEffectiveLabelOrder(t *testing.T) {
	group := contract.ModelRoutingGroup{TaskTypeLabels: []string{"coding", "research", "architect"}}
	outcome := TaskTypeOutcome{
		Value:      "research",
		Confidence: 0.8,
		Candidates: []autoclassifier.Candidate{{Label: "coding", Score: 0.8}},
	}
	want := []autoclassifier.Candidate{
		{Label: "coding", Score: 0.8},
		{Label: "research", Score: 0.8},
	}
	if got := TaskTypeCandidateScores(outcome, group); !reflect.DeepEqual(got, want) {
		t.Fatalf("scores = %+v, want the effective label order to break the tie: %+v", got, want)
	}
}
