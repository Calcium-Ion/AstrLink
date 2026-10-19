package modelrouting

import (
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestEvaluateSupportsNewValueOperators(t *testing.T) {
	tests := []struct {
		name    string
		when    contract.ModelRoutingCondition
		facts   contract.ModelRoutingFacts
		want    triState
		wantObs contract.ModelRoutingFieldObservation
	}{
		{
			name:  "not_in matches when value is absent",
			when:  contract.ModelRoutingCondition{Field: "task_type", NotIn: []string{"research"}},
			facts: contract.ModelRoutingFacts{TaskType: "coding"},
			want:  triTrue,
		},
		{
			name:  "not_in rejects a listed value",
			when:  contract.ModelRoutingCondition{Field: "task_type", NotIn: []string{"coding"}},
			facts: contract.ModelRoutingFacts{TaskType: "coding"},
			want:  triFalse,
		},
		{
			name:  "not_equals matches a different value",
			when:  contract.ModelRoutingCondition{Field: "task_type", NotEquals: "research"},
			facts: contract.ModelRoutingFacts{TaskType: "coding"},
			want:  triTrue,
		},
		{
			name:  "not_equals rejects the equal value",
			when:  contract.ModelRoutingCondition{Field: "task_type", NotEquals: "coding"},
			facts: contract.ModelRoutingFacts{TaskType: "coding"},
			want:  triFalse,
		},
		{
			name:  "not_contains_any matches normalized text without keywords",
			when:  contract.ModelRoutingCondition{Field: "literal_keyword", NotContainsAny: []string{"urgent", "secret"}},
			facts: contract.ModelRoutingFacts{LiteralText: "ordinary request"},
			want:  triTrue,
		},
		{
			name:  "not_contains_any rejects normalized keyword",
			when:  contract.ModelRoutingCondition{Field: "literal_keyword", NotContainsAny: []string{"URGENT"}},
			facts: contract.ModelRoutingFacts{LiteralText: "This is urgent"},
			want:  triFalse,
		},
		{
			name:  "gt matches above the bound",
			when:  contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Gt: intRef(10)},
			facts: contract.ModelRoutingFacts{EstimatedInputContextTokens: intRef(11)},
			want:  triTrue,
		},
		{
			name:  "gt rejects equality",
			when:  contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Gt: intRef(10)},
			facts: contract.ModelRoutingFacts{EstimatedInputContextTokens: intRef(10)},
			want:  triFalse,
		},
		{
			name:  "gte matches equality",
			when:  contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Gte: intRef(10)},
			facts: contract.ModelRoutingFacts{EstimatedInputContextTokens: intRef(10)},
			want:  triTrue,
		},
		{
			name:  "gte rejects a value below the bound",
			when:  contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Gte: intRef(10)},
			facts: contract.ModelRoutingFacts{EstimatedInputContextTokens: intRef(9)},
			want:  triFalse,
		},
		{
			name:  "lt matches below the bound",
			when:  contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Lt: intRef(10)},
			facts: contract.ModelRoutingFacts{EstimatedInputContextTokens: intRef(9)},
			want:  triTrue,
		},
		{
			name:  "lt rejects equality",
			when:  contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Lt: intRef(10)},
			facts: contract.ModelRoutingFacts{EstimatedInputContextTokens: intRef(10)},
			want:  triFalse,
		},
		{
			name:  "lte matches equality",
			when:  contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Lte: intRef(10)},
			facts: contract.ModelRoutingFacts{EstimatedInputContextTokens: intRef(10)},
			want:  triTrue,
		},
		{
			name:  "lte rejects a value above the bound",
			when:  contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Lte: intRef(10)},
			facts: contract.ModelRoutingFacts{EstimatedInputContextTokens: intRef(11)},
			want:  triFalse,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, observations := evaluateCondition(test.when, test.facts)
			if got != test.want {
				t.Fatalf("state = %v, want %v; observations = %+v", got, test.want, observations)
			}
			if !reflect.DeepEqual(test.wantObs, contract.ModelRoutingFieldObservation{}) && !reflect.DeepEqual(observations[0], test.wantObs) {
				t.Fatalf("observations = %+v, want %+v", observations, test.wantObs)
			}
		})
	}
}

func TestEvaluateNegativeListOperatorsDoNotMatchEmptyLists(t *testing.T) {
	cases := []contract.ModelRoutingCondition{
		{Field: "task_type", NotIn: []string{}},
		{Field: "literal_keyword", NotContainsAny: []string{}},
	}
	for _, condition := range cases {
		got, _ := evaluateCondition(condition, contract.ModelRoutingFacts{TaskType: "coding", LiteralText: "ordinary"})
		if got != triFalse {
			t.Errorf("condition %+v = %v, want triFalse", condition, got)
		}
	}
}

func TestEvaluateNumericBoundsSupportSingleAndDoubleSidedRanges(t *testing.T) {
	tests := []struct {
		name  string
		when  contract.ModelRoutingCondition
		value int
		want  triState
	}{
		{name: "lower only", when: contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Gte: intRef(10)}, value: 10, want: triTrue},
		{name: "upper only", when: contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Lt: intRef(20)}, value: 19, want: triTrue},
		{name: "both bounds match", when: contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Gte: intRef(10), Lt: intRef(20)}, value: 19, want: triTrue},
		{name: "both bounds fail lower", when: contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Gte: intRef(10), Lt: intRef(20)}, value: 9, want: triFalse},
		{name: "both bounds fail upper", when: contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Gte: intRef(10), Lt: intRef(20)}, value: 20, want: triFalse},
		{name: "inclusive upper", when: contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Gte: intRef(10), Lte: intRef(20)}, value: 20, want: triTrue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, _ := evaluateCondition(test.when, contract.ModelRoutingFacts{EstimatedInputContextTokens: intRef(test.value)})
			if got != test.want {
				t.Fatalf("state = %v, want %v", got, test.want)
			}
		})
	}
}

func TestEvaluateUnknownFactsRemainUnknownThroughNot(t *testing.T) {
	condition := contract.ModelRoutingCondition{Not: &contract.ModelRoutingCondition{
		Field:     "task_type",
		NotEquals: "coding",
	}}
	state, observations := evaluateCondition(condition, contract.ModelRoutingFacts{})
	if state != triUnknown {
		t.Fatalf("state = %v, want triUnknown", state)
	}
	if len(observations) != 1 || observations[0].Known || observations[0].SkipReason != "unknown" {
		t.Fatalf("observations = %+v, want one unknown observation", observations)
	}
}

func TestEvaluateInvalidNumericFactRemainsUnknown(t *testing.T) {
	condition := contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Gte: intRef(10)}
	state, observations := evaluateLeafValue(condition, contract.ModelRoutingFacts{}, true, "not-a-number")
	if state != triUnknown {
		t.Fatalf("state = %v, want triUnknown", state)
	}
	if len(observations) != 1 || observations[0].Known || observations[0].SkipReason != "invalid" {
		t.Fatalf("observations = %+v, want one invalid observation", observations)
	}
}

func TestEvaluateLegacyMinMaxMatchesNewInclusiveBounds(t *testing.T) {
	values := []int{9, 10, 20, 21}
	legacy := contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Min: intRef(10), Max: intRef(20)}
	modern := contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Gte: intRef(10), Lte: intRef(20)}
	for _, value := range values {
		facts := contract.ModelRoutingFacts{EstimatedInputContextTokens: intRef(value)}
		legacyState, _ := evaluateCondition(legacy, facts)
		modernState, _ := evaluateCondition(modern, facts)
		if legacyState != modernState {
			t.Errorf("value %d: legacy = %v, modern = %v", value, legacyState, modernState)
		}
	}
}

func TestEvaluateRulesByPriorityWithoutMutatingInput(t *testing.T) {
	rules := []contract.ModelRoutingRule{
		{ID: "late", Priority: 20, When: contract.ModelRoutingCondition{Field: "streaming", EqualsBool: boolRef(true)}, TargetMemberID: "late"},
		{ID: "early", Priority: 10, When: contract.ModelRoutingCondition{Field: "streaming", EqualsBool: boolRef(true)}, TargetMemberID: "early"},
	}
	group := evaluationGroup(rules)
	before := append([]contract.ModelRoutingRule(nil), group.Rules...)
	selection := Evaluate(group, contract.ModelRoutingFacts{Streaming: boolRef(true)})
	if selection.RuleID != "early" || selection.Member.ID != "early" {
		t.Fatalf("selection = %+v, want early rule", selection)
	}
	if !reflect.DeepEqual(group.Rules, before) {
		t.Fatalf("Evaluate mutated input rules: got %+v, before %+v", group.Rules, before)
	}
}

func TestEvaluateRuleOrderIsIndependentOfArrayOrder(t *testing.T) {
	ruleA := contract.ModelRoutingRule{ID: "a", Priority: 2, When: contract.ModelRoutingCondition{Field: "streaming", EqualsBool: boolRef(true)}, TargetMemberID: "a"}
	ruleB := contract.ModelRoutingRule{ID: "b", Priority: 1, When: contract.ModelRoutingCondition{Field: "streaming", EqualsBool: boolRef(true)}, TargetMemberID: "b"}
	facts := contract.ModelRoutingFacts{Streaming: boolRef(true)}
	first := Evaluate(evaluationGroup([]contract.ModelRoutingRule{ruleA, ruleB}), facts)
	second := Evaluate(evaluationGroup([]contract.ModelRoutingRule{ruleB, ruleA}), facts)
	if first.RuleID != "b" || second.RuleID != "b" || first.Member.ID != second.Member.ID {
		t.Fatalf("array order changed selection: first = %+v, second = %+v", first, second)
	}
}

func TestEvaluateSamePriorityUsesRuleIDTieBreak(t *testing.T) {
	ruleA := contract.ModelRoutingRule{ID: "a", Priority: 1, When: contract.ModelRoutingCondition{Field: "streaming", EqualsBool: boolRef(true)}, TargetMemberID: "a"}
	ruleB := contract.ModelRoutingRule{ID: "b", Priority: 1, When: contract.ModelRoutingCondition{Field: "streaming", EqualsBool: boolRef(true)}, TargetMemberID: "b"}
	selection := Evaluate(evaluationGroup([]contract.ModelRoutingRule{ruleB, ruleA}), contract.ModelRoutingFacts{Streaming: boolRef(true)})
	if selection.RuleID != "a" || selection.Member.ID != "a" {
		t.Fatalf("selection = %+v, want rule a", selection)
	}
}

func TestEvaluateDefaultMemberFallbackIsStable(t *testing.T) {
	group := evaluationGroup([]contract.ModelRoutingRule{
		{ID: "z", Priority: 2, When: contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}}, TargetMemberID: "strong"},
		{ID: "a", Priority: 1, When: contract.ModelRoutingCondition{Field: "streaming", EqualsBool: boolRef(true)}, TargetMemberID: "strong"},
	})
	selection := Evaluate(group, contract.ModelRoutingFacts{})
	if !selection.UsedDefault || selection.RuleID != "" || selection.Member.ID != "fast" {
		t.Fatalf("selection = %+v, want default fast member", selection)
	}
}

func evaluationGroup(rules []contract.ModelRoutingRule) contract.ModelRoutingGroup {
	return contract.ModelRoutingGroup{
		ID:              "group",
		Enabled:         true,
		Members:         []contract.ModelRoutingMember{{ID: "fast", Model: "fast"}, {ID: "early", Model: "early"}, {ID: "late", Model: "late"}, {ID: "a", Model: "a"}, {ID: "b", Model: "b"}, {ID: "strong", Model: "strong"}},
		DefaultMemberID: "fast",
		Rules:           rules,
	}
}

func intRef(value int) *int    { return &value }
func boolRef(value bool) *bool { return &value }
