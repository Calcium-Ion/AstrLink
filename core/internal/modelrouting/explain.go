package modelrouting

import "github.com/QuantumNous/astrlink/core/contract"

// RuleVerdict is one rule's own outcome under a single fact set.
//
// Evaluate stops at the first true rule, which is correct at runtime and
// useless as an answer to "why did my other rule never fire?". The registry
// records only the winning row, so a rule that was never reached and a rule
// that was reached and failed look identical from the outside. Explain walks
// the whole table and reports each row for itself.
type RuleVerdict struct {
	RuleID       string
	RulePriority *int
	// TargetMemberID is what the rule names — reported even when that member is
	// not in the group, because a typo'd target is exactly the kind of defect
	// this pass exists to surface.
	TargetMemberID string
	State          triState
	// Decided is true when the row reached a conclusion without the walk being
	// cut short. A row that never evaluated because an earlier row won is
	// neither true nor false; State is triUnknown and Decided is false.
	Decided bool
	// Selected marks the row that actually picked the member, equivalent to
	// Evaluate's rule-selection return.
	Selected bool
	// TargetMissing marks a row that came out true but names a member the group
	// does not define. At runtime this aborts selection and falls through to
	// the default member; treating it as an ordinary false would hide the bug.
	TargetMissing bool
	// Fields carries this rule's observations, already stripped of the
	// classifier provenance, which belongs to the run rather than to one row.
	Fields []contract.ModelRoutingFieldObservation
}

// Explain returns the group's rules in evaluation order with each row's own
// verdict, plus the selection those facts produced. Evaluation order is
// decisive — first true rule wins — so the order here is the answer to
// "which rule beat mine?", not a cosmetic sort.
func Explain(group contract.ModelRoutingGroup, facts contract.ModelRoutingFacts) (Selection, []RuleVerdict) {
	members := memberIndex(group)
	rules := orderedRules(group)
	verdicts := make([]RuleVerdict, 0, len(rules))
	selected := false
	for index := range rules {
		rule := rules[index]
		verdict := RuleVerdict{
			RuleID:         rule.ID,
			TargetMemberID: rule.TargetMemberID,
			State:          triUnknown,
		}
		priority := rule.Priority
		verdict.RulePriority = &priority
		if selected {
			// Rows below the winner never ran. Reporting them as false would
			// claim the facts were tested when they were not.
			verdicts = append(verdicts, verdict)
			continue
		}
		state, observations, _ := evaluateRow(rule.When, facts)
		verdict.State = state
		verdict.Decided = true
		verdict.Fields = plainObservations(observations)
		if state != triTrue {
			verdicts = append(verdicts, verdict)
			continue
		}
		if _, ok := members[rule.TargetMemberID]; !ok {
			// Mirrors selectRule: a true row with an unknown target ends the
			// walk. Rows below it are unreachable and are reported as such.
			verdict.TargetMissing = true
			selected = true
			verdicts = append(verdicts, verdict)
			continue
		}
		verdict.Selected = true
		selected = true
		verdicts = append(verdicts, verdict)
	}
	selection, _ := selectRule(group, facts)
	return selection, verdicts
}

// plainObservations drops classifier provenance from a verdict's fields.
//
// Provenance (classifier id, confidence, candidate ranking) describes the run
// that produced the facts, not the row that read them: the same classifier id
// would be copied onto every row's every field, and on the wire that is most of
// 128 rules x 21 fields. The run-level carrier already reports it once, and the
// candidate `chosen` flag is a property of the adopted value rather than of a
// single rule, so it is not a per-row signal either.
func plainObservations(observations []contract.ModelRoutingFieldObservation) []contract.ModelRoutingFieldObservation {
	if len(observations) == 0 {
		return nil
	}
	plain := make([]contract.ModelRoutingFieldObservation, len(observations))
	for index, observation := range observations {
		observation.ClassifierID = ""
		observation.ClassifierConfidence = 0
		observation.Candidates = nil
		plain[index] = observation
	}
	return plain
}

// RuleVerdicts renders the per-rule walk in the shape the dry-run preview and
// the request record share. The member model is resolved here rather than in
// the evaluator: naming a member's model is a property of the group document,
// not of the evaluation.
func RuleVerdicts(verdicts []RuleVerdict, group contract.ModelRoutingGroup) []contract.ModelRoutingRuleVerdict {
	if len(verdicts) == 0 {
		return nil
	}
	models := make(map[string]string, len(group.Members))
	for _, member := range group.Members {
		models[member.ID] = member.Model
	}
	rendered := make([]contract.ModelRoutingRuleVerdict, 0, len(verdicts))
	for _, verdict := range verdicts {
		priority := 0
		if verdict.RulePriority != nil {
			priority = *verdict.RulePriority
		}
		state := verdict.State.String()
		if !verdict.Decided {
			// "never ran" and "ran but a field was unknown" both carry triUnknown.
			// Reaching the second requires the facts to have been read, so only
			// the first is reported as unevaluated — a row that was skipped
			// because an earlier one won cannot be improved by editing it.
			state = contract.ModelRoutingRuleNotEvaluated
		}
		rendered = append(rendered, contract.ModelRoutingRuleVerdict{
			RuleID:         verdict.RuleID,
			Priority:       priority,
			TargetMemberID: verdict.TargetMemberID,
			TargetModel:    models[verdict.TargetMemberID],
			State:          state,
			Selected:       verdict.Selected,
			TargetMissing:  verdict.TargetMissing,
			Fields:         verdict.Fields,
		})
	}
	return rendered
}
