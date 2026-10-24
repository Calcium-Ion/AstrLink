package ingress

import (
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// TestModelRoutingNeedsClassificationFiresForCustomDimensionOnly is the gap
// this test exists for: before dimensions, the gate only looked for task_type,
// so a group whose rules read nothing but a custom dimension never classified.
// The dimension would then be unknown forever and the configuration would
// silently do nothing.
func TestModelRoutingNeedsClassificationFiresForCustomDimensionOnly(t *testing.T) {
	group := contract.ModelRoutingGroup{
		Rules: []contract.ModelRoutingRule{{
			ID: "by-sentiment",
			When: contract.ModelRoutingCondition{
				Field: "sentiment", Equals: "negative",
			},
			TargetMemberID: "fast",
		}},
	}
	dimensions := []contract.ClassifierDimension{{Name: "sentiment", Options: []contract.ClassifierDimensionOption{{Name: "negative"}}}}
	if !modelRoutingNeedsClassification(group, dimensions) {
		t.Fatal("a rule reading a custom dimension must trigger classification")
	}
}

// TestModelRoutingNeedsClassificationSingleDimensionGateIsUnchanged: with no
// custom dimensions declared, the gate reduces to the task_type-only check the
// routing path has always used — a group with unrelated rules still skips
// classification entirely.
func TestModelRoutingNeedsClassificationSingleDimensionGateIsUnchanged(t *testing.T) {
	unrelated := contract.ModelRoutingGroup{
		Rules: []contract.ModelRoutingRule{{
			ID: "by-protocol",
			When: contract.ModelRoutingCondition{
				Field: "protocol", Equals: "openai.chat",
			},
			TargetMemberID: "fast",
		}},
	}
	if modelRoutingNeedsClassification(unrelated, nil) {
		t.Error("a group that reads no classifier field must not classify")
	}

	byTaskType := contract.ModelRoutingGroup{
		Rules: []contract.ModelRoutingRule{{
			ID: "by-task-type",
			When: contract.ModelRoutingCondition{
				Field: "task_type", Equals: "coding",
			},
			TargetMemberID: "fast",
		}},
	}
	if !modelRoutingNeedsClassification(byTaskType, nil) {
		t.Error("a rule reading task_type must trigger classification")
	}
}

// TestModelRoutingNeedsClassificationFindsNestedDimensionReferences: the
// reference may sit anywhere in the boolean tree, not just at the top level.
func TestModelRoutingNeedsClassificationFindsNestedDimensionReferences(t *testing.T) {
	group := contract.ModelRoutingGroup{
		Rules: []contract.ModelRoutingRule{{
			ID: "nested",
			When: contract.ModelRoutingCondition{
				Any: []contract.ModelRoutingCondition{
					{Field: "protocol", Equals: "openai.chat"},
					{Not: &contract.ModelRoutingCondition{Field: "sentiment", Equals: "positive"}},
				},
			},
			TargetMemberID: "fast",
		}},
	}
	dimensions := []contract.ClassifierDimension{{Name: "sentiment", Options: []contract.ClassifierDimensionOption{{Name: "positive"}}}}
	if !modelRoutingNeedsClassification(group, dimensions) {
		t.Fatal("a nested dimension reference must trigger classification")
	}
}

// TestModelRoutingNeedsClassificationIgnoresUndeclaredDimension: a rule that
// references a name the settings do not declare is not a classifier trigger —
// nothing would be asked for it, so classifying could not produce a value.
func TestModelRoutingNeedsClassificationIgnoresUndeclaredDimension(t *testing.T) {
	group := contract.ModelRoutingGroup{
		Rules: []contract.ModelRoutingRule{{
			ID:             "by-undeclared",
			When:           contract.ModelRoutingCondition{Field: "sentiment", Equals: "negative"},
			TargetMemberID: "fast",
		}},
	}
	if modelRoutingNeedsClassification(group, nil) {
		t.Error("a rule naming a dimension that is not declared must not trigger classification")
	}
}
