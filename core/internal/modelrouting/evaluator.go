package modelrouting

import (
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/astrlink/core/contract"
)

type Selection struct {
	Member          contract.ModelRoutingMember
	SelectionSource contract.ModelRoutingSelectionSource
	RuleID          string
	RulePriority    *int
	UsedDefault     bool
	Fields          []contract.ModelRoutingFieldObservation
	FallbackReason  string
	// TaskTypeCandidateIndex is the adopted candidate's rank when the matched
	// rule went through scored-fallback substitution; nil otherwise. It is
	// diagnostics for the observation side: candidate scores never re-enter
	// selection.
	TaskTypeCandidateIndex *int
}

// Evaluate performs only deterministic, bounded local evaluation. Unknown
// facts never block a request; only a true rule is eligible to select a member.
func Evaluate(group contract.ModelRoutingGroup, facts contract.ModelRoutingFacts) Selection {
	selection, _ := selectRule(group, facts)
	return selection
}

// selectRule is the one selection pass: it returns the winning selection and the
// winning rule's index in orderedRules, or -1 when no rule selected a member.
// Evaluate and Explain both derive from it so the preview cannot disagree with
// the live path about which row won.
func selectRule(group contract.ModelRoutingGroup, facts contract.ModelRoutingFacts) (Selection, int) {
	members := memberIndex(group)
	rules := orderedRules(group)
	for index := range rules {
		rule := &rules[index]
		result, observations, adoptedCandidate := evaluateRow(rule.When, facts)
		if result != triTrue {
			continue
		}
		member, ok := members[rule.TargetMemberID]
		if !ok {
			break
		}
		priority := rule.Priority
		selection := Selection{Member: member, SelectionSource: contract.ModelRoutingSelectionRule, RuleID: rule.ID, RulePriority: &priority, Fields: observations}
		if adoptedCandidate != nil {
			selection.TaskTypeCandidateIndex = adoptedCandidate
		}
		return selection, index
	}
	member := members[group.DefaultMemberID]
	return Selection{Member: member, SelectionSource: contract.ModelRoutingSelectionDefaultMember, UsedDefault: true, Fields: []contract.ModelRoutingFieldObservation{}}, -1
}

// orderedRules is the one rule order every evaluation path uses: ascending
// priority, ID as the tie-break. The wire contract requires unique priorities,
// but keeping the tie-break here makes runtime behavior deterministic for an
// unvalidated document too.
//
// The slice is copied before sorting: callers may reuse the settings snapshot
// and the evaluator must not rewrite its rule order.
func orderedRules(group contract.ModelRoutingGroup) []contract.ModelRoutingRule {
	rules := append([]contract.ModelRoutingRule(nil), group.Rules...)
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority < rules[j].Priority
		}
		return rules[i].ID < rules[j].ID
	})
	return rules
}

// memberIndex maps a group-local member ID to its definition. A rule naming a
// member that is not in the group cannot select anything, which is why the
// caller has to distinguish "no rule matched" from "a rule matched but pointed
// at nothing" — the two need different fixes.
func memberIndex(group contract.ModelRoutingGroup) map[string]contract.ModelRoutingMember {
	members := make(map[string]contract.ModelRoutingMember, len(group.Members))
	for _, member := range group.Members {
		members[member.ID] = member
	}
	return members
}

// evaluateRow applies the scored-fallback substitution: when the classifier
// produced a ranked candidate sequence, a row referencing task_type tries
// each candidate in order and adopts the first that makes the row true.
// Rows without a task_type reference, and rows without candidates, keep the
// original single-value path unchanged. Row order stays the outer order:
// substitution happens inside one row and can never let a later row leapfrog
// an earlier matchable row.
func evaluateRow(condition contract.ModelRoutingCondition, facts contract.ModelRoutingFacts) (triState, []contract.ModelRoutingFieldObservation, *int) {
	if !conditionReferencesTaskType(condition) || len(facts.TaskTypeCandidates) == 0 {
		state, observations := evaluateCondition(condition, facts)
		return state, observations, nil
	}
	var firstObservations []contract.ModelRoutingFieldObservation
	aggregate := triFalse
	for candidateIndex, label := range facts.TaskTypeCandidates {
		probe := facts
		probe.TaskType = label
		state, observations := evaluateCondition(condition, probe)
		if candidateIndex == 0 {
			firstObservations = observations
		}
		if state == triTrue {
			adopted := candidateIndex
			return triTrue, observations, &adopted
		}
		if state == triUnknown {
			aggregate = triUnknown
		}
	}
	// No candidate made the row true. Report the top-ranked candidate's
	// observations so the record still shows why the field did not match.
	return aggregate, firstObservations, nil
}

func conditionReferencesTaskType(condition contract.ModelRoutingCondition) bool {
	if condition.Field == "task_type" {
		return true
	}
	if condition.Not != nil && conditionReferencesTaskType(*condition.Not) {
		return true
	}
	for _, child := range condition.All {
		if conditionReferencesTaskType(child) {
			return true
		}
	}
	for _, child := range condition.Any {
		if conditionReferencesTaskType(child) {
			return true
		}
	}
	return false
}

type triState uint8

const (
	triUnknown triState = iota
	triFalse
	triTrue
)

// String is the wire spelling. It exists so the control API never has to know
// the internal numbering: a preview that reported "1" instead of "false" would
// be a mapping the UI has to reverse, and reversing it is where the two sides
// drift apart.
func (state triState) String() string {
	switch state {
	case triTrue:
		return "true"
	case triFalse:
		return "false"
	default:
		return "unknown"
	}
}

func evaluateCondition(condition contract.ModelRoutingCondition, facts contract.ModelRoutingFacts) (triState, []contract.ModelRoutingFieldObservation) {
	if condition.All != nil {
		state := triTrue
		var observations []contract.ModelRoutingFieldObservation
		for _, child := range condition.All {
			childState, childObservations := evaluateCondition(child, facts)
			observations = append(observations, childObservations...)
			if childState == triFalse {
				return triFalse, observations
			}
			if childState == triUnknown {
				state = triUnknown
			}
		}
		return state, observations
	}
	if condition.Any != nil {
		state := triFalse
		var observations []contract.ModelRoutingFieldObservation
		for _, child := range condition.Any {
			childState, childObservations := evaluateCondition(child, facts)
			observations = append(observations, childObservations...)
			if childState == triTrue {
				return triTrue, observations
			}
			if childState == triUnknown {
				state = triUnknown
			}
		}
		return state, observations
	}
	if condition.Not != nil {
		state, observations := evaluateCondition(*condition.Not, facts)
		switch state {
		case triTrue:
			return triFalse, observations
		case triFalse:
			return triTrue, observations
		default:
			return triUnknown, observations
		}
	}
	return evaluateLeaf(condition, facts)
}

func evaluateLeaf(condition contract.ModelRoutingCondition, facts contract.ModelRoutingFacts) (triState, []contract.ModelRoutingFieldObservation) {
	known, value := factValue(condition.Field, facts)
	state, observations := evaluateLeafValue(condition, facts, known, value)
	if !known && len(observations) == 1 && observations[0].SkipReason == "unknown" {
		if reason, ok := dimensionSkipReason(condition.Field, facts); ok {
			observations[0].SkipReason = reason
		}
	}
	return state, observations
}

// dimensionSkipReason separates the two ways a custom dimension can be
// unknown. Neither changes selection — an unknown fact never matches a rule —
// but only the first is a provider-side gap, and the record is where an
// operator finds out that the classifier stopped answering a dimension.
func dimensionSkipReason(field string, facts contract.ModelRoutingFacts) (string, bool) {
	fact, ok := facts.Dimensions[field]
	if !ok {
		return "", false
	}
	if !fact.Produced {
		// Either the provider omitted the dimension entirely, or its answer was
		// unusable (outside the declared options, malformed, or below the
		// threshold). Both are "no value this call", which is what the
		// provider-side gap report needs to say.
		return "dimension_not_produced", true
	}
	return "", false
}

func evaluateLeafValue(condition contract.ModelRoutingCondition, facts contract.ModelRoutingFacts, known bool, value string) (triState, []contract.ModelRoutingFieldObservation) {
	observation := contract.ModelRoutingFieldObservation{Field: condition.Field, Source: factSource(condition.Field, facts), Known: known}
	if !known {
		observation.SkipReason = "unknown"
		return triUnknown, []contract.ModelRoutingFieldObservation{observation}
	}
	// The keyword fact is the request text itself; a record keeps only how
	// many keywords it contained, never the text.
	if condition.Field != "literal_keyword" {
		observation.Value = value
	}

	var matched bool
	switch {
	case condition.In != nil:
		for _, candidate := range condition.In {
			if value == candidate {
				matched = true
				break
			}
		}
	case condition.NotIn != nil:
		// Empty negative lists are incomplete conditions, not tautologies.
		matched = len(condition.NotIn) > 0
		for _, candidate := range condition.NotIn {
			if value == candidate {
				matched = false
				break
			}
		}
	case condition.Equals != "":
		matched = value == condition.Equals
	case condition.NotEquals != "":
		matched = value != condition.NotEquals
	case condition.EqualsBool != nil:
		want := "false"
		if *condition.EqualsBool {
			want = "true"
		}
		matched = value == want
	case hasNumericBound(condition):
		number, err := strconv.Atoi(value)
		if err != nil {
			observation.Known = false
			observation.Value = ""
			observation.SkipReason = "invalid"
			return triUnknown, []contract.ModelRoutingFieldObservation{observation}
		}
		matched = numericBoundsMatch(number, condition)
	case condition.ContainsAny != nil:
		matched, observation.MatchCount = keywordMatch(facts.LiteralText, condition.ContainsAny)
	case condition.NotContainsAny != nil:
		if len(condition.NotContainsAny) == 0 {
			return triFalse, []contract.ModelRoutingFieldObservation{observation}
		}
		var count int
		matched, count = keywordMatch(facts.LiteralText, condition.NotContainsAny)
		observation.MatchCount = count
		matched = len(condition.NotContainsAny) > 0 && !matched
	case condition.Within != nil:
		matched = withinWindowMatch(value, condition.Within)
	}
	if matched {
		return triTrue, []contract.ModelRoutingFieldObservation{observation}
	}
	return triFalse, []contract.ModelRoutingFieldObservation{observation}
}

func hasNumericBound(condition contract.ModelRoutingCondition) bool {
	return condition.Min != nil || condition.Max != nil || condition.Gt != nil || condition.Gte != nil || condition.Lt != nil || condition.Lte != nil
}

func numericBoundsMatch(number int, condition contract.ModelRoutingCondition) bool {
	if condition.Min != nil && number < *condition.Min {
		return false
	}
	if condition.Max != nil && number > *condition.Max {
		return false
	}
	if condition.Gt != nil && number <= *condition.Gt {
		return false
	}
	if condition.Gte != nil && number < *condition.Gte {
		return false
	}
	if condition.Lt != nil && number >= *condition.Lt {
		return false
	}
	if condition.Lte != nil && number > *condition.Lte {
		return false
	}
	return true
}

func keywordMatch(text string, keywords []string) (bool, int) {
	text = normalizeText(text)
	matched := false
	count := 0
	for _, keyword := range keywords {
		if strings.Contains(text, normalizeText(keyword)) {
			matched = true
			count++
		}
	}
	return matched, count
}

// withinWindowMatch evaluates one half-open [start, end) HH:mm window at
// minute granularity. start == end names that single instant rather than an
// empty interval; start > end spans midnight. Malformed values cannot occur
// in a validated document and count as no match at runtime.
func withinWindowMatch(value string, window []string) bool {
	if len(window) != 2 {
		return false
	}
	current, ok := minutesOfDay(value)
	if !ok {
		return false
	}
	start, startOK := minutesOfDay(window[0])
	end, endOK := minutesOfDay(window[1])
	if !startOK || !endOK {
		return false
	}
	switch {
	case start == end:
		return current == start
	case start < end:
		return current >= start && current < end
	default:
		return current >= start || current < end
	}
}

func minutesOfDay(value string) (int, bool) {
	if len(value) != 5 || value[2] != ':' {
		return 0, false
	}
	for _, digit := range []byte{value[0], value[1], value[3], value[4]} {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	hour := int(value[0]-'0')*10 + int(value[1]-'0')
	minute := int(value[3]-'0')*10 + int(value[4]-'0')
	if hour > 23 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func factValue(field string, facts contract.ModelRoutingFacts) (bool, string) {
	switch field {
	case "protocol":
		return facts.Protocol != "", facts.Protocol
	case "media_kind":
		return facts.MediaKind != "", facts.MediaKind
	case "task_type":
		return facts.TaskType != "", facts.TaskType
	case "literal_keyword":
		return facts.LiteralText != "", facts.LiteralText
	case "system_time":
		if facts.SystemTime == nil {
			return false, ""
		}
		return true, facts.SystemTime.Format("15:04")
	}
	if fact, ok := facts.Dimensions[field]; ok {
		// A dimension that is not Known reads as unknown for every operator,
		// including != and not_in: "the provider did not answer this" is not
		// the same as "the provider answered something else", and only the
		// second can satisfy a negative test. Known is already false when the
		// answer fell below the threshold, so the comparison lives entirely in
		// the fact builder.
		return fact.Known, fact.Value
	}
	boolValue := func(value *bool) (bool, string) {
		if value == nil {
			return false, ""
		}
		if *value {
			return true, "true"
		}
		return true, "false"
	}
	intValue := func(value *int) (bool, string) {
		if value == nil {
			return false, ""
		}
		return true, strconv.Itoa(*value)
	}
	switch field {
	case "streaming":
		return boolValue(facts.Streaming)
	case "continuation":
		return boolValue(facts.Continuation)
	case "has_tools":
		return boolValue(facts.HasTools)
	case "has_media":
		return boolValue(facts.HasMedia)
	case "has_image":
		return boolValue(facts.HasImage)
	case "has_audio":
		return boolValue(facts.HasAudio)
	case "has_video":
		return boolValue(facts.HasVideo)
	case "turn_count":
		return intValue(facts.TurnCount)
	case "user_message_count":
		return intValue(facts.UserMessageCount)
	case "assistant_message_count":
		return intValue(facts.AssistantMessageCount)
	case "tool_message_count":
		return intValue(facts.ToolMessageCount)
	case "tool_declaration_count":
		return intValue(facts.ToolDeclarationCount)
	case "tool_call_count":
		return intValue(facts.ToolCallCount)
	case "input_bytes":
		return intValue(facts.InputBytes)
	case "input_text_runes":
		return intValue(facts.InputTextRunes)
	case "estimated_input_context_tokens":
		return intValue(facts.EstimatedInputContextTokens)
	default:
		return false, ""
	}
}

// factSource names where a field's value came from. A custom dimension is
// classified, exactly like task_type: both are provider answers, and an
// operator reading a record needs to tell "the classifier said this" apart
// from "the request said this".
func factSource(field string, facts contract.ModelRoutingFacts) string {
	if field == "task_type" {
		return "classifier"
	}
	if _, ok := facts.Dimensions[field]; ok {
		return "classifier"
	}
	if field == "literal_keyword" {
		return "local_text"
	}
	return "request"
}

func normalizeText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, value)
}
