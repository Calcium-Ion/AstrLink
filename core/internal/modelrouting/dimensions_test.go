package modelrouting

import (
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

const sentimentDimension = "sentiment"

// TestDimensionFactsAppliesTheSingleThresholdToEveryDimension is the core of
// the threshold ruling: one configured value governs every dimension, and an
// answer below it leaves the dimension unknown exactly like a missing one.
func TestDimensionFactsAppliesTheSingleThresholdToEveryDimension(t *testing.T) {
	outcome := TaskTypeOutcome{
		LowConfidenceThreshold: 0.5,
		Dimensions: map[string]DimensionScore{
			"confident":  {Label: "negative", Score: 0.7},
			"borderline": {Label: "negative", Score: 0.5},
			"weak":       {Label: "negative", Score: 0.3},
			"unanswered": {Label: "", Score: 0},
		},
	}
	facts := DimensionFacts(outcome)
	if len(facts) != 4 {
		t.Fatalf("facts = %v, want one entry per reported dimension", facts)
	}
	for name, wantKnown := range map[string]bool{
		"confident":  true,
		"borderline": true, // the threshold is inclusive: >= passes
		"weak":       false,
		"unanswered": false,
	} {
		fact, ok := facts[name]
		if !ok {
			t.Fatalf("dimension %q missing from %v", name, facts)
		}
		if fact.Produced != true {
			t.Errorf("%s: the provider reported this dimension, so Produced must be true", name)
		}
		if fact.Known != wantKnown {
			t.Errorf("%s: Known = %v, want %v", name, fact.Known, wantKnown)
		}
	}
	if facts["confident"].Value != "negative" {
		t.Errorf("a known dimension must carry its value, got %q", facts["confident"].Value)
	}
}

// TestDimensionFactsFallbackProducesNothing mirrors the task_type rule: a
// fallback is not a measurement, so no dimension may claim a value from it.
func TestDimensionFactsFallbackProducesNothing(t *testing.T) {
	outcome := TaskTypeOutcome{
		Code: "classifier_busy",
		Dimensions: map[string]DimensionScore{
			sentimentDimension: {Label: "negative", Score: 0.9},
		},
	}
	if facts := DimensionFacts(outcome); facts != nil {
		t.Fatalf("a fallback attempt must contribute no dimensions, got %v", facts)
	}
}

// The two tests below are the 5d-3 / 5d-4 pair. They exist because the
// original criteria covered the threshold comparison but not the guard in
// front of it: low confidence was treated as a fallback, so a weak task_type
// discarded every dimension in the same reply.

// TestDimensionFactsLowConfidenceTaskTypeKeepsDimensions (5d-3) pins the
// distinction the code is named for. A task_type below the threshold is a
// completed measurement that lost its own selection, not a failed attempt:
// the dimensions answered by the same reply keep their values and their own
// threshold outcomes.
func TestDimensionFactsLowConfidenceTaskTypeKeepsDimensions(t *testing.T) {
	outcome := TaskTypeOutcome{
		// This is what both producers record when task_type alone is weak:
		// ingress model_routing.go (existing-model and cloud paths) and the
		// controlapi dry-run all set this exact code.
		Code:                   CodeClassifierLowConfidence,
		Confidence:             0.2,
		LowConfidenceThreshold: 0.5,
		Dimensions: map[string]DimensionScore{
			"strong": {Label: "negative", Score: 0.95},
			"weak":   {Label: "negative", Score: 0.3},
		},
	}
	facts := DimensionFacts(outcome)
	if len(facts) != 2 {
		t.Fatalf("facts = %v, want both dimensions kept despite the weak task_type", facts)
	}
	if !facts["strong"].Known {
		t.Errorf("a dimension measured at 0.95 must stay Known when only task_type was weak; got %+v", facts["strong"])
	}
	if facts["strong"].Value != "negative" {
		t.Errorf("Value = %q, want the provider's answer preserved", facts["strong"].Value)
	}
	// Each dimension is still judged on its own score, so the weak one is
	// unknown — kept and reportable, but not selectable.
	if facts["weak"].Known {
		t.Errorf("the below-threshold dimension must stay unknown, got %+v", facts["weak"])
	}
	if !facts["weak"].Produced {
		t.Errorf("a dimension the provider answered is Produced even when weak, got %+v", facts["weak"])
	}
}

// TestDimensionFactsRealFallbackStillProducesNothing (5d-4) guards the fix
// from the other side: only the low-confidence code is exempt. A genuine
// fallback measured nothing, so it must still contribute no dimensions.
func TestDimensionFactsRealFallbackStillProducesNothing(t *testing.T) {
	for _, code := range []string{
		"classifier_busy",
		"classifier_timeout",
		"classifier_unavailable",
		"classifier_invalid_output",
		"classifier_refusal",
	} {
		outcome := TaskTypeOutcome{
			Code: code,
			Dimensions: map[string]DimensionScore{
				sentimentDimension: {Label: "negative", Score: 0.9},
			},
		}
		if facts := DimensionFacts(outcome); facts != nil {
			t.Errorf("code %q is a fallback and must contribute no dimensions, got %v", code, facts)
		}
	}
}

// TestDimensionFactsSingleDimensionStaysEmpty is the P1 invariant on the fact
// side: with no dimensions asked for, the map is nil and nothing downstream
// changes.
func TestDimensionFactsSingleDimensionStaysEmpty(t *testing.T) {
	if facts := DimensionFacts(TaskTypeOutcome{Value: "coding", Confidence: 0.9}); facts != nil {
		t.Fatalf("single-dimension facts must stay empty, got %v", facts)
	}
}

// TestEvaluateMatchesOnCustomDimension is the capability itself: a rule that
// tests a custom dimension selects its member when the dimension is known.
func TestEvaluateMatchesOnCustomDimension(t *testing.T) {
	group := evaluationGroup([]contract.ModelRoutingRule{{
		ID: "by-sentiment", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: sentimentDimension, In: []string{"negative"}},
		TargetMemberID: "early",
	}})
	facts := contract.ModelRoutingFacts{
		Dimensions: map[string]contract.DimensionFact{
			sentimentDimension: {Value: "negative", Known: true, Produced: true},
		},
	}
	selection := Evaluate(group, facts)
	if selection.Member.ID != "early" {
		t.Fatalf("member = %q, want early", selection.Member.ID)
	}
	if selection.RuleID != "by-sentiment" {
		t.Errorf("rule = %q, want by-sentiment", selection.RuleID)
	}
}

// TestEvaluateUnknownCustomDimensionNeverMatches is the fail-open rule: a
// dimension with no usable value leaves every rule that tests it unmatched, so
// the request still routes through the default member rather than failing.
func TestEvaluateUnknownCustomDimensionNeverMatches(t *testing.T) {
	group := evaluationGroup([]contract.ModelRoutingRule{{
		ID: "by-sentiment", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: sentimentDimension, Equals: "negative"},
		TargetMemberID: "early",
	}})
	for name, fact := range map[string]contract.DimensionFact{
		"below threshold": {Value: "negative", Score: 0.1, Known: false, Produced: true},
		"not produced":    {Known: false, Produced: false},
		"absent":          {},
	} {
		selection := Evaluate(group, contract.ModelRoutingFacts{
			Dimensions: map[string]contract.DimensionFact{sentimentDimension: fact},
		})
		if selection.Member.ID != "fast" || !selection.UsedDefault {
			t.Errorf("%s: a rule testing an unknown dimension must not match (got member %q)", name, selection.Member.ID)
		}
	}
}

// TestEvaluateUnknownDimensionAlsoFailsNegativeOperators: "the provider did not
// answer this" is not the same as "the provider answered something else", so a
// not_equals leaf must stay unknown rather than becoming true.
func TestEvaluateUnknownDimensionAlsoFailsNegativeOperators(t *testing.T) {
	group := evaluationGroup([]contract.ModelRoutingRule{{
		ID: "not-negative", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: sentimentDimension, NotEquals: "negative"},
		TargetMemberID: "early",
	}})
	selection := Evaluate(group, contract.ModelRoutingFacts{
		Dimensions: map[string]contract.DimensionFact{sentimentDimension: {Known: false, Produced: true}},
	})
	if selection.Member.ID != "fast" {
		t.Fatalf("member = %q, want the default fast member", selection.Member.ID)
	}
}

// TestEvaluateReportsWhyADimensionWasSkipped separates the two ways a
// dimension can be unknown: a provider-side gap is worth surfacing, a
// threshold rejection is a measurement that was simply too weak.
func TestEvaluateReportsWhyADimensionWasSkipped(t *testing.T) {
	field := sentimentDimension
	selection := Evaluate(evaluationGroup(nil), contract.ModelRoutingFacts{})
	if len(selection.Fields) != 0 {
		t.Fatalf("a default-member selection carries no field observations, got %v", selection.Fields)
	}
	_, observations := evaluateLeaf(
		contract.ModelRoutingCondition{Field: field, Equals: "negative"},
		contract.ModelRoutingFacts{Dimensions: map[string]contract.DimensionFact{field: {Known: false, Produced: false}}},
	)
	if len(observations) != 1 {
		t.Fatalf("observations = %v, want one", observations)
	}
	if observations[0].SkipReason != "dimension_not_produced" {
		t.Errorf("skip reason = %q, want dimension_not_produced", observations[0].SkipReason)
	}
	if observations[0].Source != "classifier" {
		t.Errorf("source = %q, want classifier", observations[0].Source)
	}

	_, weak := evaluateLeaf(
		contract.ModelRoutingCondition{Field: field, Equals: "negative"},
		contract.ModelRoutingFacts{Dimensions: map[string]contract.DimensionFact{field: {Value: "negative", Score: 0.1, Known: false, Produced: true}}},
	)
	if len(weak) != 1 {
		t.Fatalf("observations = %v, want one", weak)
	}
	if weak[0].SkipReason != "unknown" {
		t.Errorf("skip reason = %q, want unknown for a below-threshold answer", weak[0].SkipReason)
	}
}

// TestEvaluateDimensionComposesWithBuiltInFields checks a dimension is
// evaluated as an ordinary leaf inside a boolean tree, not as a special case.
func TestEvaluateDimensionComposesWithBuiltInFields(t *testing.T) {
	group := evaluationGroup([]contract.ModelRoutingRule{{
		ID: "combined", Priority: 1,
		When: contract.ModelRoutingCondition{All: []contract.ModelRoutingCondition{
			{Field: sentimentDimension, Equals: "negative"},
			{Field: "streaming", EqualsBool: boolRef(true)},
		}},
		TargetMemberID: "strong",
	}})
	selection := Evaluate(group, contract.ModelRoutingFacts{
		Streaming: boolRef(true),
		Dimensions: map[string]contract.DimensionFact{
			sentimentDimension: {Value: "negative", Known: true, Produced: true},
		},
	})
	if selection.Member.ID != "strong" {
		t.Fatalf("member = %q, want strong", selection.Member.ID)
	}
}
