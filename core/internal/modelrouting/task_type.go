package modelrouting

import (
	"sort"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
)

// TaskTypeOutcome is one classification attempt's contribution to routing
// facts. Candidates is the classifier's ranked output (or nil for
// single-value sources); Value and Candidates may both be set — structured
// sources report the primary label and the ranked list on separate fields —
// and the conversion below reconciles the two instead of trusting one. The
// conversion into Facts.TaskTypeCandidates happens in
// TaskTypeCandidateLabels, the single place that orders, filters, and
// deduplicates. The inference path (ingress) and the control-plane dry-run
// (controlapi) both build this carrier, so both run through the same
// conversion below instead of each deriving its own candidate sequence.
type TaskTypeOutcome struct {
	Value                  string
	Code                   string
	DurationMS             int
	ClassifierID           string
	Confidence             float64
	Candidates             []autoclassifier.Candidate
	LowConfidenceThreshold float64
	// Dimensions carries the custom dimensions' raw answers, keyed by
	// dimension name. A dimension the provider omitted is absent, which is the
	// "not produced" case DimensionFacts turns into an unknown. Empty for a
	// single-dimension configuration, where nothing downstream changes.
	Dimensions map[string]DimensionScore
}

// DimensionScore is one custom dimension's raw provider answer: the chosen
// option name and the confidence reported for it. It is the input side of
// DimensionFact; the threshold comparison happens in DimensionFacts, the one
// place that sees both the answer and the threshold.
type DimensionScore struct {
	Label string
	Score float64
}

// TaskTypeCandidateScores is the one Outcome→sequence conversion both fact
// building and observation building consume (W5/W6): it turns the
// classifier's ranked output into the ordered candidate sequence the
// evaluator substitutes, keeping each label's score. Ranked candidates are
// threshold-filtered and label-checked; single-value sources contribute
// their one confident label; fallback paths (any recorded code, including
// fallback=general) contribute nothing, because a fallback is not a
// measurement. A ranked sequence that omits the primary answer is completed,
// not trusted: the primary merges in with its own score before the shared
// filters run, so the presence of a ranked field can never displace a valid
// primary from selection; when the merged primary survives, the sequence
// re-sorts by score descending with the group's effective label order
// breaking ties, keeping ranks consistent with the substituted sequence. A
// primary already inside the sequence stands as-is — the merge is
// idempotent. Scores ride along for the observation side only — facts stay
// label-only so scores can never re-enter selection.
func TaskTypeCandidateScores(outcome TaskTypeOutcome, group contract.ModelRoutingGroup) []autoclassifier.Candidate {
	if outcome.Code != "" {
		return nil
	}
	candidates := outcome.Candidates
	mergedPrimary := false
	if len(candidates) == 0 {
		if outcome.Value == "" {
			return nil
		}
		candidates = []autoclassifier.Candidate{{Label: outcome.Value, Score: outcome.Confidence}}
	} else if outcome.Value != "" && !taskTypeCandidatesContainLabel(candidates, outcome.Value) {
		// The primary is a label absent from the sequence, so uniqueness and
		// the keeps-first-entry rule both hold after the merge. Copy before
		// appending: the producer owns the original slice.
		merged := make([]autoclassifier.Candidate, 0, len(candidates)+1)
		merged = append(merged, candidates...)
		candidates = append(merged, autoclassifier.Candidate{Label: outcome.Value, Score: outcome.Confidence})
		mergedPrimary = true
	}
	scored := make([]autoclassifier.Candidate, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.Score < outcome.LowConfidenceThreshold {
			continue
		}
		if !group.ValidTaskTypeLabel(candidate.Label) {
			continue
		}
		if _, ok := seen[candidate.Label]; ok {
			continue
		}
		seen[candidate.Label] = struct{}{}
		scored = append(scored, candidate)
	}
	if mergedPrimary {
		if _, ok := seen[outcome.Value]; ok {
			sortTaskTypeCandidates(scored, group)
		}
	}
	return scored
}

// CodeClassifierLowConfidence is the recorded code for a classification that
// completed but whose task_type fell below LowConfidenceThreshold. It is
// deliberately distinct from the fallback codes: the provider answered, the
// answer was simply too weak to select on. Every producer of a task_type
// outcome (ingress for both classifier sources, controlapi for the dry-run)
// names it through this constant so the three cannot drift apart.
const CodeClassifierLowConfidence = "classifier_low_confidence"

// isFallbackOutcome reports whether an attempt failed to produce a
// measurement at all, as opposed to producing one that was too weak.
//
// The distinction is what CodeClassifierLowConfidence exists to carry. A
// fallback code means no reply arrived (unavailable, busy, timeout) or the
// reply was unusable (invalid output, refusal): nothing was measured, so the
// custom dimensions are absent for the same reason task_type is. Low
// confidence means the reply arrived and every dimension in it was measured —
// only task_type's own score was weak, and that is a per-value judgement, not
// a per-attempt one.
func isFallbackOutcome(outcome TaskTypeOutcome) bool {
	return outcome.Code != "" && outcome.Code != CodeClassifierLowConfidence
}

// DimensionFacts converts one attempt's raw dimension answers into the
// evaluator's dimension facts, applying the same single threshold task_type
// uses. There is exactly one LowConfidenceThreshold in the whole system and
// every dimension is compared against it — the threshold is configured once
// and applies everywhere, not once per dimension.
//
// A dimension lands in the result in one of three states:
//
//   - answered above the threshold: Known, the rule that tests it can match;
//   - answered at or below it, or answered outside its option list: Known is
//     false, so every rule testing it stays unknown and unmatched;
//   - not answered at all: absent from the map, which reads as unknown with
//     Produced false.
//
// Nothing here can fail a request. A dimension with no usable answer lowers
// what the rules can match; it never returns an error, because the classifier
// seam is fail-open and an unusable dimension is not a reason to refuse
// service.
//
// A fallback attempt produces nothing: a fallback is not a measurement, which
// is the same rule TaskTypeCandidateScores applies to task_type. But only a
// real fallback counts. CodeClassifierLowConfidence is a completed
// measurement whose task_type merely fell below the threshold, and the
// dimensions it carried are as usable as any other reply's — dropping them
// here would let a weak task_type silently erase a dimension the provider
// answered at 0.95, which is exactly what the one-threshold-per-dimension rule
// forbids.
func DimensionFacts(outcome TaskTypeOutcome) map[string]contract.DimensionFact {
	if isFallbackOutcome(outcome) || len(outcome.Dimensions) == 0 {
		return nil
	}
	facts := make(map[string]contract.DimensionFact, len(outcome.Dimensions))
	for name, answer := range outcome.Dimensions {
		if answer.Label == "" {
			// No value to select on. It stays in the map as a produced-but
			// -unknown dimension so the record can still distinguish it from a
			// dimension the provider never mentioned.
			facts[name] = contract.DimensionFact{Score: answer.Score, Produced: true}
			continue
		}
		facts[name] = contract.DimensionFact{
			Value:    answer.Label,
			Score:    answer.Score,
			Known:    answer.Score >= outcome.LowConfidenceThreshold,
			Produced: true,
		}
	}
	return facts
}

// ClassifierAnswers is what one classification said, in the shape the
// request record keeps: the task type when it is usable, and one entry per
// configured custom dimension — including a dimension the classifier skipped,
// which is the case an operator most needs to see. facts are the dimension
// facts the evaluator read, so a dimension reported known is one a rule could
// match on.
func ClassifierAnswers(outcome TaskTypeOutcome, facts map[string]contract.DimensionFact, configured []contract.ClassifierDimension) *contract.ModelRoutingClassifierAnswers {
	answers := &contract.ModelRoutingClassifierAnswers{}
	if outcome.Value != "" {
		answers.TaskType = outcome.Value
		answers.Confidence = outcome.Confidence
	}
	for _, dimension := range configured {
		if dimension.Name == contract.BuiltInTaskTypeDimension {
			continue
		}
		fact := facts[dimension.Name]
		answers.Dimensions = append(answers.Dimensions, contract.ModelRoutingDimensionAnswer{
			Name:     dimension.Name,
			Label:    fact.Value,
			Score:    fact.Score,
			Known:    fact.Known,
			Produced: fact.Produced,
		})
	}
	return answers
}

// taskTypeCandidatesContainLabel reports whether the ranked sequence already
// describes the label.
func taskTypeCandidatesContainLabel(candidates []autoclassifier.Candidate, label string) bool {
	for _, candidate := range candidates {
		if candidate.Label == label {
			return true
		}
	}
	return false
}

// sortTaskTypeCandidates applies the merged-sequence ordering contract:
// score descending, the group's effective label order breaking ties. That
// order is exactly what the classification request listed (the call plan
// resolves its labels from the same group), so it matches the comparator the
// producing sources run (autoclassifier/cloud's sortRankedCandidates) without
// importing it: that one is package-private and keyed by the per-request
// label slice, while this one derives the key from the group's catalog.
func sortTaskTypeCandidates(candidates []autoclassifier.Candidate, group contract.ModelRoutingGroup) {
	labels := contract.EffectiveModelRoutingTaskTypeLabels(group.TaskTypeLabels)
	requestOrder := make(map[string]int, len(labels))
	for index, label := range labels {
		requestOrder[label] = index
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return requestOrder[candidates[i].Label] < requestOrder[candidates[j].Label]
	})
}

// TaskTypeCandidateLabels feeds the evaluator: the filtered sequence from
// TaskTypeCandidateScores reduced to ordered labels only.
func TaskTypeCandidateLabels(outcome TaskTypeOutcome, group contract.ModelRoutingGroup) []string {
	scored := TaskTypeCandidateScores(outcome, group)
	if len(scored) == 0 {
		return nil
	}
	labels := make([]string, 0, len(scored))
	for _, candidate := range scored {
		labels = append(labels, candidate.Label)
	}
	return labels
}

// TaskTypeCandidateObservations renders the filtered candidate sequence into
// the persisted observation shape. Rank is the 0-based position in the very
// sequence the evaluator substituted, so it always lines up with
// Selection.TaskTypeCandidateIndex; adopted (nil when no candidate was
// substituted) marks at most that one entry as chosen. A field that never
// went through scored fallback gets no candidates at all, never a list of
// all-false rows.
func TaskTypeCandidateObservations(outcome TaskTypeOutcome, group contract.ModelRoutingGroup, adopted *int) []contract.ModelRoutingCandidateObservation {
	scored := TaskTypeCandidateScores(outcome, group)
	if len(scored) == 0 {
		return nil
	}
	observations := make([]contract.ModelRoutingCandidateObservation, 0, len(scored))
	for index, candidate := range scored {
		observations = append(observations, contract.ModelRoutingCandidateObservation{
			Value:  candidate.Label,
			Score:  candidate.Score,
			Rank:   index,
			Chosen: adopted != nil && *adopted == index,
		})
	}
	return observations
}

// StampClassifierProvenance writes one classification attempt's provenance
// onto the field observations the evaluator produced. The inference path and
// the control-plane dry-run both call it, so a record and a preview cannot
// describe the same attempt differently.
//
// One call answers every question it was asked, so the attempt's classifier id
// belongs on each classifier-sourced observation. The ranked candidate
// sequence does not. It is task_type's own output: copying it onto a custom
// dimension reports the task_type ranking as that dimension's answer, which is
// exactly how a group configured only on a custom field came to render
// task_type labels under the dimension's name. A dimension carries its own
// score instead — a single-choice answer has no ranked sequence to report, and
// Candidates stays nil so the observation reads as "no candidate detail"
// rather than as a list this dimension never produced.
//
// An attempt that measured nothing stamps nothing beyond the reason it already
// carries: a fallback leaves every classifier field unknown, and inventing a
// classifier id for it would claim a measurement that never happened.
func StampClassifierProvenance(fields []contract.ModelRoutingFieldObservation, outcome TaskTypeOutcome, group contract.ModelRoutingGroup, adopted *int) {
	candidates := TaskTypeCandidateObservations(outcome, group, adopted)
	if outcome.ClassifierID == "" && outcome.Confidence == 0 && candidates == nil && len(outcome.Dimensions) == 0 {
		return
	}
	for index := range fields {
		if fields[index].Source != "classifier" {
			continue
		}
		fields[index].ClassifierID = outcome.ClassifierID
		if fields[index].Field == contract.BuiltInTaskTypeDimension {
			fields[index].ClassifierConfidence = outcome.Confidence
			fields[index].Candidates = candidates
			continue
		}
		if dimension, ok := outcome.Dimensions[fields[index].Field]; ok {
			fields[index].ClassifierConfidence = dimension.Score
		}
	}
}
