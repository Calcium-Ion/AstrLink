package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ModelRoutingModelPrefix          = "astrlink/"
	MaxModelRoutingGroups            = 64
	MaxModelRoutingMembersPerGroup   = 32
	MaxModelRoutingRulesPerGroup     = 128
	MaxModelRoutingConditionDepth    = 4
	MaxModelRoutingConditionNodes    = 64
	MaxModelRoutingListItems         = 16
	MaxModelRoutingKeywords          = 32
	MaxModelRoutingTaskTypeLabels    = MaxModelRoutingListItems
	MaxModelRoutingValueRunes        = 128
	MaxModelRoutingRuleIDRunes       = 64
	MaxModelRoutingMemberIDRunes     = 64
	MaxModelRoutingPriority          = 1_000_000_000
	MaxModelRoutingFactsTextRunes    = 64 * 1024
	MaxModelRoutingObservationFields = 64
)

// DefaultModelRoutingTaskTypeLabels preserves the task_type domain of groups
// created before configurable labels were added. A non-empty group label list
// is authoritative and intentionally replaces this built-in set.
var DefaultModelRoutingTaskTypeLabels = []string{"general", "research", "coding", "architect"}

func EffectiveModelRoutingTaskTypeLabels(labels []string) []string {
	if len(labels) == 0 {
		return append([]string(nil), DefaultModelRoutingTaskTypeLabels...)
	}
	return append([]string(nil), labels...)
}

type ModelRoutingSelectionSource string

const (
	ModelRoutingSelectionRule             ModelRoutingSelectionSource = "rule"
	ModelRoutingSelectionDefaultMember    ModelRoutingSelectionSource = "default_member"
	ModelRoutingSelectionGroupBinding     ModelRoutingSelectionSource = "group_binding"
	ModelRoutingSelectionResponseAffinity ModelRoutingSelectionSource = "response_affinity"
	ModelRoutingSelectionWebSocket        ModelRoutingSelectionSource = "websocket_connection"
)

func (source ModelRoutingSelectionSource) Valid() bool {
	switch source {
	case ModelRoutingSelectionRule, ModelRoutingSelectionDefaultMember,
		ModelRoutingSelectionGroupBinding, ModelRoutingSelectionResponseAffinity,
		ModelRoutingSelectionWebSocket:
		return true
	default:
		return false
	}
}

type ModelRoutingExecutionStatus string

const (
	ModelRoutingExecutionPending   ModelRoutingExecutionStatus = "pending"
	ModelRoutingExecutionSelected  ModelRoutingExecutionStatus = "selected"
	ModelRoutingExecutionSucceeded ModelRoutingExecutionStatus = "succeeded"
	ModelRoutingExecutionFailed    ModelRoutingExecutionStatus = "failed"
	ModelRoutingExecutionCancelled ModelRoutingExecutionStatus = "cancelled"
)

func (status ModelRoutingExecutionStatus) Valid() bool {
	switch status {
	case ModelRoutingExecutionPending, ModelRoutingExecutionSelected,
		ModelRoutingExecutionSucceeded, ModelRoutingExecutionFailed,
		ModelRoutingExecutionCancelled:
		return true
	default:
		return false
	}
}

type ModelRoutingPrivacyDecision string

const (
	ModelRoutingPrivacyNone    ModelRoutingPrivacyDecision = "none"
	ModelRoutingPrivacyAllow   ModelRoutingPrivacyDecision = "allow"
	ModelRoutingPrivacyReject  ModelRoutingPrivacyDecision = "reject"
	ModelRoutingPrivacyUnknown ModelRoutingPrivacyDecision = "unknown"
)

func (decision ModelRoutingPrivacyDecision) Valid() bool {
	switch decision {
	case ModelRoutingPrivacyNone, ModelRoutingPrivacyAllow, ModelRoutingPrivacyReject, ModelRoutingPrivacyUnknown:
		return true
	default:
		return false
	}
}

// ModelRoutingCondition is a bounded, structural condition tree. Optional
// scalar pointers distinguish an absent bound from zero.
type ModelRoutingCondition struct {
	All            []ModelRoutingCondition `json:"all,omitempty"`
	Any            []ModelRoutingCondition `json:"any,omitempty"`
	Not            *ModelRoutingCondition  `json:"not,omitempty"`
	Field          string                  `json:"field,omitempty"`
	In             []string                `json:"in,omitempty"`
	NotIn          []string                `json:"not_in,omitempty"`
	Equals         string                  `json:"equals,omitempty"`
	NotEquals      string                  `json:"not_equals,omitempty"`
	EqualsBool     *bool                   `json:"equals_bool,omitempty"`
	Min            *int                    `json:"min,omitempty"`
	Max            *int                    `json:"max,omitempty"`
	Gt             *int                    `json:"gt,omitempty"`
	Gte            *int                    `json:"gte,omitempty"`
	Lt             *int                    `json:"lt,omitempty"`
	Lte            *int                    `json:"lte,omitempty"`
	ContainsAny    []string                `json:"contains_any,omitempty"`
	NotContainsAny []string                `json:"not_contains_any,omitempty"`
	// Within is the time leaf: exactly two HH:mm values, a half-open
	// [start, end) window on system_time only. Exclusion composes through
	// the not leaf instead of a set operator.
	Within []string `json:"within,omitempty"`
}

func (condition *ModelRoutingCondition) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("routing condition must be an object")
	}
	allowed := map[string]bool{
		"all": true, "any": true, "not": true, "field": true,
		"in": true, "not_in": true, "equals": true, "not_equals": true,
		"equals_bool": true,
		"min":         true, "max": true, "gt": true, "gte": true, "lt": true, "lte": true,
		"contains_any": true, "not_contains_any": true, "within": true,
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unknown routing condition field %q", key)
		}
	}
	var value struct {
		All            []ModelRoutingCondition `json:"all"`
		Any            []ModelRoutingCondition `json:"any"`
		Not            *ModelRoutingCondition  `json:"not"`
		Field          string                  `json:"field"`
		In             []string                `json:"in"`
		NotIn          []string                `json:"not_in"`
		Equals         string                  `json:"equals"`
		NotEquals      string                  `json:"not_equals"`
		EqualsBool     *bool                   `json:"equals_bool"`
		Min            *int                    `json:"min"`
		Max            *int                    `json:"max"`
		Gt             *int                    `json:"gt"`
		Gte            *int                    `json:"gte"`
		Lt             *int                    `json:"lt"`
		Lte            *int                    `json:"lte"`
		ContainsAny    []string                `json:"contains_any"`
		NotContainsAny []string                `json:"not_contains_any"`
		Within         []string                `json:"within"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*condition = ModelRoutingCondition(value)
	return nil
}

type ModelRoutingMember struct {
	ID        string    `json:"id"`
	ServiceID ServiceID `json:"service_id"`
	Model     string    `json:"model"`
}

type ModelRoutingRule struct {
	ID             string                `json:"id"`
	Priority       int                   `json:"priority"`
	When           ModelRoutingCondition `json:"when"`
	TargetMemberID string                `json:"target_member_id"`
}

type ModelRoutingGroup struct {
	ID              string               `json:"id"`
	Enabled         bool                 `json:"enabled"`
	Members         []ModelRoutingMember `json:"members"`
	DefaultMemberID string               `json:"default_member_id"`
	Rules           []ModelRoutingRule   `json:"rules"`
	// TaskTypeLabels is the task_type domain for this group. Empty preserves the
	// built-in four-label domain for backward compatibility; non-empty replaces
	// it, including whether built-in labels remain allowed.
	TaskTypeLabels []string `json:"task_type_labels,omitempty"`
}

func (member *ModelRoutingMember) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	type plain ModelRoutingMember
	var value plain
	if err := decodePolicy(data, &value, []string{"id", "service_id", "model"}, nil); err != nil {
		return err
	}
	*member = ModelRoutingMember(value)
	return nil
}

func (rule *ModelRoutingRule) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	type plain ModelRoutingRule
	var value plain
	if err := decodePolicy(data, &value, []string{"id", "priority", "when", "target_member_id"}, nil); err != nil {
		return err
	}
	*rule = ModelRoutingRule(value)
	return nil
}

func (group *ModelRoutingGroup) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	type plain ModelRoutingGroup
	var value plain
	if err := decodePolicy(data, &value, []string{"id", "enabled", "members", "default_member_id", "rules"}, []string{"task_type_labels"}); err != nil {
		return err
	}
	*group = ModelRoutingGroup(value)
	return nil
}

func (group ModelRoutingGroup) PublicModelID() string { return ModelRoutingModelPrefix + group.ID }

// HasMember reports whether the group still defines this exact member: the
// same ID naming the same API provider and model.
func (group ModelRoutingGroup) HasMember(id string, serviceID ServiceID, model string) bool {
	for _, member := range group.Members {
		if member.ID == id {
			return member.ServiceID == serviceID && member.Model == model
		}
	}
	return false
}

func (group ModelRoutingGroup) Validate() error {
	return group.validate(nil)
}

// ValidateWithDimensions is Validate against a caller-supplied dimension set,
// which lets a rule name a user-defined dimension. The group itself never
// stores the dimensions — they are global classifier settings, not group
// configuration — so a caller that has them passes them here. With no
// dimensions this is identical to Validate.
func (group ModelRoutingGroup) ValidateWithDimensions(dimensions []ClassifierDimension) error {
	return group.validate(dimensions)
}

func (group ModelRoutingGroup) validate(dimensions []ClassifierDimension) error {
	if err := validateModelRoutingSlug("group id", group.ID, 64); err != nil {
		return err
	}
	if group.ID == "auto" || group.ID == "text-v1" {
		return fmt.Errorf("group id %q is reserved", group.ID)
	}
	if err := validateModelRoutingTaskTypeLabels(group.TaskTypeLabels); err != nil {
		return err
	}
	taskTypeLabels := EffectiveModelRoutingTaskTypeLabels(group.TaskTypeLabels)
	if len(group.Members) == 0 || len(group.Members) > MaxModelRoutingMembersPerGroup {
		return fmt.Errorf("model routing group %q must contain 1 to %d members", group.ID, MaxModelRoutingMembersPerGroup)
	}
	members := make(map[string]ModelRoutingMember, len(group.Members))
	for index, member := range group.Members {
		if err := validateModelRoutingSlug(fmt.Sprintf("members[%d].id", index), member.ID, MaxModelRoutingMemberIDRunes); err != nil {
			return err
		}
		if _, ok := members[member.ID]; ok {
			return fmt.Errorf("model routing member %q is duplicated", member.ID)
		}
		members[member.ID] = member
		if err := member.ServiceID.Validate(); err != nil {
			return fmt.Errorf("members[%d].service_id: %w", index, err)
		}
		if err := validateModelRoutingModel(fmt.Sprintf("members[%d].model", index), member.Model); err != nil {
			return err
		}
	}
	if _, ok := members[group.DefaultMemberID]; !ok {
		return fmt.Errorf("default_member_id %q does not name a member", group.DefaultMemberID)
	}
	if len(group.Rules) > MaxModelRoutingRulesPerGroup {
		return fmt.Errorf("model routing group %q must contain at most %d rules", group.ID, MaxModelRoutingRulesPerGroup)
	}
	seenIDs := make(map[string]struct{}, len(group.Rules))
	seenPriorities := make(map[int]struct{}, len(group.Rules))
	for index, rule := range group.Rules {
		if err := validateModelRoutingSlug(fmt.Sprintf("rules[%d].id", index), rule.ID, MaxModelRoutingRuleIDRunes); err != nil {
			return err
		}
		if _, ok := seenIDs[rule.ID]; ok {
			return fmt.Errorf("model routing rule %q is duplicated", rule.ID)
		}
		seenIDs[rule.ID] = struct{}{}
		if rule.Priority < 0 || rule.Priority > MaxModelRoutingPriority {
			return fmt.Errorf("rules[%d].priority must be between 0 and %d", index, MaxModelRoutingPriority)
		}
		if _, ok := seenPriorities[rule.Priority]; ok {
			return fmt.Errorf("routing rule priority %d is duplicated", rule.Priority)
		}
		seenPriorities[rule.Priority] = struct{}{}
		if _, ok := members[rule.TargetMemberID]; !ok {
			return fmt.Errorf("rules[%d].target_member_id %q does not name a member", index, rule.TargetMemberID)
		}
		if err := rule.When.validateWithTaskTypeLabels(taskTypeLabels, dimensions); err != nil {
			return fmt.Errorf("rules[%d].when: %w", index, err)
		}
	}
	return nil
}

func validateModelRoutingSlug(field, value string, max int) error {
	if value == "" || utf8.RuneCountInString(value) > max {
		return fmt.Errorf("%s must contain 1 to %d characters", field, max)
	}
	for index, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return fmt.Errorf("%s must contain only lowercase ASCII letters, digits and hyphens", field)
		}
		if (index == 0 || index == len(value)-1) && r == '-' {
			return fmt.Errorf("%s must not start or end with a hyphen", field)
		}
	}
	return nil
}

func validateModelRoutingModel(field, value string) error {
	if value == "" || utf8.RuneCountInString(value) > 256 {
		return fmt.Errorf("%s must contain 1 to 256 characters", field)
	}
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\t") {
		return fmt.Errorf("%s must not contain surrounding whitespace or controls", field)
	}
	if value == AstrLinkTextClassificationV1 || value == "text-v1" || strings.HasPrefix(value, ModelRoutingModelPrefix) || strings.Contains(value, "://") {
		return fmt.Errorf("%s must name a concrete model, not a reserved group or URL", field)
	}
	if parsed, err := url.Parse(value); err == nil && (parsed.User != nil || parsed.Host != "") {
		return fmt.Errorf("%s must not contain credentials or a URL", field)
	}
	return nil
}

func (group ModelRoutingGroup) ValidTaskTypeLabel(value string) bool {
	return containsModelRoutingTaskTypeLabel(EffectiveModelRoutingTaskTypeLabels(group.TaskTypeLabels), value)
}

// ReferencesField reports whether any rule of the group names field.
func (group ModelRoutingGroup) ReferencesField(field string) bool {
	for _, rule := range group.Rules {
		if rule.When.ReferencesField(field) {
			return true
		}
	}
	return false
}

func validateModelRoutingTaskTypeLabels(labels []string) error {
	if len(labels) > MaxModelRoutingTaskTypeLabels {
		return fmt.Errorf("task_type_labels must contain at most %d labels", MaxModelRoutingTaskTypeLabels)
	}
	seen := make(map[string]struct{}, len(labels))
	for index, label := range labels {
		if err := validateModelRoutingValue(fmt.Sprintf("task_type_labels[%d]", index), label); err != nil {
			return err
		}
		if _, ok := seen[label]; ok {
			return fmt.Errorf("task_type_labels contains duplicate label %q", label)
		}
		seen[label] = struct{}{}
	}
	return nil
}

func containsModelRoutingTaskTypeLabel(labels []string, value string) bool {
	return containsString(labels, value)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

var modelRoutingFields = map[string]struct {
	kind string
}{
	"protocol": {kind: "string"}, "media_kind": {kind: "string"}, "task_type": {kind: "string"},
	"streaming": {kind: "bool"}, "continuation": {kind: "bool"}, "has_tools": {kind: "bool"}, "has_media": {kind: "bool"}, "has_image": {kind: "bool"}, "has_audio": {kind: "bool"}, "has_video": {kind: "bool"},
	"turn_count": {kind: "number"}, "user_message_count": {kind: "number"}, "assistant_message_count": {kind: "number"}, "tool_message_count": {kind: "number"}, "tool_declaration_count": {kind: "number"}, "tool_call_count": {kind: "number"}, "input_bytes": {kind: "number"}, "input_text_runes": {kind: "number"}, "estimated_input_context_tokens": {kind: "number"},
	"literal_keyword": {kind: "keyword"},
	"system_time":     {kind: "time"},
}

// modelRoutingFieldKind resolves a routing field against the built-in registry
// and then the user-defined dimensions. A custom dimension is always string
// kind: its options are free-form names, so it supports the same operators as
// any other string field. With no dimensions this is exactly
// modelRoutingFields[field].
func modelRoutingFieldKind(field string, dimensions []ClassifierDimension) (string, bool) {
	if entry, ok := modelRoutingFields[field]; ok {
		return entry.kind, true
	}
	for _, dimension := range dimensions {
		if dimension.Name == field {
			return "string", true
		}
	}
	return "", false
}

func (condition ModelRoutingCondition) Validate() error {
	return condition.validateWithTaskTypeLabels(DefaultModelRoutingTaskTypeLabels, nil)
}

// ReferencesField reports whether field appears in any leaf of the tree,
// including leaves under not.
func (condition ModelRoutingCondition) ReferencesField(field string) bool {
	if condition.Field == field {
		return true
	}
	if condition.Not != nil && condition.Not.ReferencesField(field) {
		return true
	}
	for _, child := range condition.All {
		if child.ReferencesField(field) {
			return true
		}
	}
	for _, child := range condition.Any {
		if child.ReferencesField(field) {
			return true
		}
	}
	return false
}

func (condition ModelRoutingCondition) validateWithTaskTypeLabels(taskTypeLabels []string, dimensions []ClassifierDimension) error {
	return validateModelRoutingCondition(condition, 1, new(int), taskTypeLabels, dimensions)
}

// ValidateWithDimensions is Validate against a caller-supplied dimension set.
// Rules may name a user-defined dimension only through this entry point: the
// zero-argument Validate cannot see the global dimension list and therefore
// accepts built-in fields alone. Passing no dimensions makes this identical to
// Validate.
func (condition ModelRoutingCondition) ValidateWithDimensions(taskTypeLabels []string, dimensions []ClassifierDimension) error {
	return validateModelRoutingCondition(condition, 1, new(int), taskTypeLabels, dimensions)
}

func validateModelRoutingCondition(condition ModelRoutingCondition, depth int, nodes *int, taskTypeLabels []string, dimensions []ClassifierDimension) error {
	if depth > MaxModelRoutingConditionDepth {
		return fmt.Errorf("condition depth exceeds %d", MaxModelRoutingConditionDepth)
	}
	*nodes++
	if *nodes > MaxModelRoutingConditionNodes {
		return fmt.Errorf("condition nodes exceed %d", MaxModelRoutingConditionNodes)
	}
	shapes := 0
	if condition.All != nil {
		shapes++
	}
	if condition.Any != nil {
		shapes++
	}
	if condition.Not != nil {
		shapes++
	}
	leaf := condition.Field != "" || condition.In != nil || condition.NotIn != nil ||
		condition.Equals != "" || condition.NotEquals != "" || condition.EqualsBool != nil ||
		condition.Min != nil || condition.Max != nil ||
		condition.Gt != nil || condition.Gte != nil || condition.Lt != nil || condition.Lte != nil ||
		condition.ContainsAny != nil || condition.NotContainsAny != nil || condition.Within != nil
	if leaf {
		shapes++
	}
	if shapes != 1 {
		return fmt.Errorf("condition must contain exactly one logical or leaf shape")
	}
	if condition.All != nil || condition.Any != nil {
		children := condition.All
		name := "all"
		if condition.Any != nil {
			children, name = condition.Any, "any"
		}
		if len(children) == 0 || len(children) > MaxModelRoutingListItems {
			return fmt.Errorf("%s must contain 1 to %d conditions", name, MaxModelRoutingListItems)
		}
		for index, child := range children {
			if err := validateModelRoutingCondition(child, depth+1, nodes, taskTypeLabels, dimensions); err != nil {
				return fmt.Errorf("%s[%d]: %w", name, index, err)
			}
		}
		return nil
	}
	if condition.Not != nil {
		return validateModelRoutingCondition(*condition.Not, depth+1, nodes, taskTypeLabels, dimensions)
	}
	kind, ok := modelRoutingFieldKind(condition.Field, dimensions)
	if !ok {
		return fmt.Errorf("unsupported_routing_field: %s", condition.Field)
	}
	operators := 0
	if condition.In != nil {
		operators++
	}
	if condition.NotIn != nil {
		operators++
	}
	if condition.Equals != "" {
		operators++
	}
	if condition.NotEquals != "" {
		operators++
	}
	if condition.EqualsBool != nil {
		operators++
	}
	// min, max and the gt/gte/lt/lte bounds form one operator slot: a number
	// leaf describes a single range, built from at most one lower and one
	// upper bound.
	if condition.Min != nil || condition.Max != nil ||
		condition.Gt != nil || condition.Gte != nil || condition.Lt != nil || condition.Lte != nil {
		operators++
	}
	if condition.ContainsAny != nil {
		operators++
	}
	if condition.NotContainsAny != nil {
		operators++
	}
	if condition.Within != nil {
		operators++
	}
	if operators != 1 {
		return fmt.Errorf("field %q must contain exactly one supported operator", condition.Field)
	}
	if condition.Within != nil && kind != "time" {
		return fmt.Errorf("field %q does not support within", condition.Field)
	}
	switch kind {
	case "string":
		if condition.EqualsBool != nil || condition.Min != nil || condition.Max != nil ||
			condition.Gt != nil || condition.Gte != nil || condition.Lt != nil || condition.Lte != nil ||
			condition.ContainsAny != nil || condition.NotContainsAny != nil {
			return fmt.Errorf("field %q does not support that operator", condition.Field)
		}
		for _, list := range []struct {
			name   string
			values []string
		}{{"in", condition.In}, {"not_in", condition.NotIn}} {
			if list.values == nil {
				continue
			}
			if len(list.values) == 0 || len(list.values) > MaxModelRoutingListItems {
				return fmt.Errorf("field %q %s must contain 1 to %d values", condition.Field, list.name, MaxModelRoutingListItems)
			}
			for _, value := range list.values {
				if err := validateModelRoutingValue(condition.Field, value); err != nil {
					return err
				}
			}
		}
		for _, scalar := range []string{condition.Equals, condition.NotEquals} {
			if scalar != "" {
				if err := validateModelRoutingValue(condition.Field, scalar); err != nil {
					return err
				}
			}
		}
		values := append([]string{}, condition.In...)
		values = append(values, condition.NotIn...)
		if condition.Equals != "" {
			values = append(values, condition.Equals)
		}
		if condition.NotEquals != "" {
			values = append(values, condition.NotEquals)
		}
		for _, value := range values {
			switch condition.Field {
			case "protocol":
				if value != "" && !ProtocolID(value).Valid() {
					return fmt.Errorf("protocol %q is invalid", value)
				}
			case "task_type":
				if !containsString(taskTypeLabels, value) {
					return fmt.Errorf("task_type %q is invalid; allowed labels: %s", value, strings.Join(taskTypeLabels, ", "))
				}
			case "media_kind":
				if value != "none" && value != "image" && value != "audio" && value != "video" && value != "mixed" && value != "unknown" {
					return fmt.Errorf("media_kind %q is invalid", value)
				}
			}
		}
	case "bool":
		if condition.EqualsBool == nil {
			return fmt.Errorf("field %q requires equals_bool", condition.Field)
		}
	case "number":
		// A number leaf is a single range: at most one lower bound (min, gte or
		// gt, in that equivalence) and at most one upper bound (max, lte or lt),
		// and at least one of the two. min is an inclusive lower bound and gte
		// its rename, as max is to lte, so mixing the two spellings of one side
		// is ambiguous and refused.
		lowerBounds := []*int{condition.Min, condition.Gte, condition.Gt}
		upperBounds := []*int{condition.Max, condition.Lte, condition.Lt}
		lowerCount, upperCount := 0, 0
		for _, bound := range lowerBounds {
			if bound != nil {
				lowerCount++
			}
		}
		for _, bound := range upperBounds {
			if bound != nil {
				upperCount++
			}
		}
		if lowerCount == 0 && upperCount == 0 {
			return fmt.Errorf("field %q requires a numeric bound", condition.Field)
		}
		if lowerCount > 1 {
			return fmt.Errorf("field %q must not carry more than one lower bound", condition.Field)
		}
		if upperCount > 1 {
			return fmt.Errorf("field %q must not carry more than one upper bound", condition.Field)
		}
		var lower, upper *int
		for _, bound := range lowerBounds {
			if bound != nil {
				lower = bound
			}
		}
		for _, bound := range upperBounds {
			if bound != nil {
				upper = bound
			}
		}
		if condition.Min != nil && *condition.Min < 0 || condition.Max != nil && *condition.Max < 0 ||
			condition.Gt != nil && *condition.Gt < 0 || condition.Gte != nil && *condition.Gte < 0 ||
			condition.Lt != nil && *condition.Lt < 0 || condition.Lte != nil && *condition.Lte < 0 {
			return fmt.Errorf("field %q bounds must be non-negative", condition.Field)
		}
		if lower != nil && upper != nil && *lower > *upper {
			return fmt.Errorf("field %q lower bound must not exceed upper bound", condition.Field)
		}
	case "keyword":
		if condition.ContainsAny == nil && condition.NotContainsAny == nil {
			return fmt.Errorf("literal_keyword must contain 1 to %d values", MaxModelRoutingKeywords)
		}
		lists := []struct {
			name   string
			values []string
		}{{"contains_any", condition.ContainsAny}, {"not_contains_any", condition.NotContainsAny}}
		for _, list := range lists {
			if list.values == nil {
				continue
			}
			if len(list.values) == 0 || len(list.values) > MaxModelRoutingKeywords {
				return fmt.Errorf("literal_keyword %s must contain 1 to %d values", list.name, MaxModelRoutingKeywords)
			}
			for _, value := range list.values {
				if err := validateModelRoutingValue(condition.Field, value); err != nil {
					return err
				}
			}
		}
	case "time":
		// A time leaf is one half-open [start, end) window: exactly two
		// HH:mm values. Equal endpoints name a single instant; exclusion
		// composes through the not leaf, not a set operator.
		if condition.Within == nil {
			return fmt.Errorf("field %q requires within", condition.Field)
		}
		if len(condition.Within) != 2 {
			return fmt.Errorf("field %q within must contain exactly 2 values", condition.Field)
		}
		for _, value := range condition.Within {
			if !validRoutingTimeOfDay(value) {
				return fmt.Errorf("field %q within value %q must be HH:mm", condition.Field, value)
			}
		}
	}
	return nil
}

// validRoutingTimeOfDay accepts HH:mm with a 00-23 hour and a 00-59 minute;
// 24:00 is not a wall-clock time and is refused.
func validRoutingTimeOfDay(value string) bool {
	if len(value) != 5 || value[2] != ':' {
		return false
	}
	for _, digit := range []byte{value[0], value[1], value[3], value[4]} {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	hour := int(value[0]-'0')*10 + int(value[1]-'0')
	minute := int(value[3]-'0')*10 + int(value[4]-'0')
	return hour <= 23 && minute <= 59
}

func validateModelRoutingValue(field, value string) error {
	if value == "" || utf8.RuneCountInString(value) > MaxModelRoutingValueRunes {
		return fmt.Errorf("%s values must contain 1 to %d characters", field, MaxModelRoutingValueRunes)
	}
	return nil
}

func ValidateModelRoutingGroups(groups []ModelRoutingGroup, redirects []ModelRedirect) error {
	return ValidateModelRoutingGroupsWithDimensions(groups, redirects, nil)
}

// ValidateModelRoutingGroupsWithDimensions is ValidateModelRoutingGroups
// against a caller-supplied dimension set, so a group whose rules name a
// user-defined dimension validates. The dimensions are global classifier
// settings rather than group configuration, so a caller that holds them passes
// them in; with none this is identical to ValidateModelRoutingGroups.
//
// The save path (controlapi) and the inference path (ingress) are the two
// production callers: without them a dimension-aware rule could be written by
// the API's own test surface and rejected by every real request, which is
// exactly the state this entry point exists to end.
func ValidateModelRoutingGroupsWithDimensions(groups []ModelRoutingGroup, redirects []ModelRedirect, dimensions []ClassifierDimension) error {
	if len(groups) > MaxModelRoutingGroups {
		return fmt.Errorf("model_routing_groups must contain at most %d groups", MaxModelRoutingGroups)
	}
	ids := make(map[string]struct{}, len(groups))
	occupied := make(map[string]string)
	for _, group := range groups {
		if err := group.ValidateWithDimensions(dimensions); err != nil {
			return err
		}
		if _, ok := ids[group.ID]; ok {
			return fmt.Errorf("model routing group %q is duplicated", group.ID)
		}
		ids[group.ID] = struct{}{}
		occupied[group.PublicModelID()] = "group"
		for _, member := range group.Members {
			occupied[member.Model] = "member"
		}
	}
	for _, redirect := range redirects {
		if _, ok := occupied[redirect.From]; ok {
			return fmt.Errorf("model routing redirect conflicts with %q", redirect.From)
		}
		if _, ok := occupied[redirect.To]; ok {
			return fmt.Errorf("model routing redirect conflicts with %q", redirect.To)
		}
	}
	return nil
}

// ModelRoutingCandidateObservation is one candidate a classifier produced for
// a field: the observed output shape only, never an input to selection.
// Selection stays row-order first-match; nothing may read this for scoring.
type ModelRoutingCandidateObservation struct {
	Value  string  `json:"value"`
	Score  float64 `json:"score"`
	Rank   int     `json:"rank"`
	Chosen bool    `json:"chosen"`
}

type ModelRoutingFieldObservation struct {
	Field      string `json:"field"`
	Source     string `json:"source"`
	Known      bool   `json:"known"`
	MatchCount int    `json:"match_count"`
	// Value is the fact the condition read, in the evaluator's own spelling
	// ("true", "12", "openai.chat", "14:20"). It stays empty for an unknown
	// field and for literal_keyword, whose fact is the request text itself:
	// MatchCount is all a record may say about it.
	Value string `json:"value,omitempty"`
	// ClassifierConfidence is the upstream-reported score passed through
	// verbatim; zero for the bundled worker and every fallback path.
	ClassifierConfidence float64 `json:"classifier_confidence,omitempty"`
	ClassifierID         string  `json:"classifier_id,omitempty"`
	SkipReason           string  `json:"skip_reason,omitempty"`
	ErrorCode            string  `json:"error_code,omitempty"`
	// Candidates is the classifier's full ranked output for this field.
	// Empty means the record predates candidates or the field never went
	// through scored fallback; it renders as "no candidate detail", not as
	// an empty table. Chosen marks at most one candidate per field.
	Candidates []ModelRoutingCandidateObservation `json:"candidates,omitempty"`
}

type ModelRoutingExecution struct {
	Status       ModelRoutingExecutionStatus `json:"status"`
	AttemptCount int                         `json:"attempt_count"`
	ErrorCode    string                      `json:"error_code,omitempty"`
}

type ModelRoutingPrivacyAttempt struct {
	AttemptIndex int                         `json:"attempt_index"`
	ServiceID    ServiceID                   `json:"service_id"`
	Decision     ModelRoutingPrivacyDecision `json:"decision"`
	FindingCount int                         `json:"finding_count"`
	DurationMS   int                         `json:"duration_ms"`
	ErrorCode    string                      `json:"error_code,omitempty"`
}

// Rule verdict states. A row that came out false and one that never ran both
// leave the member unselected, but only the first is something the operator
// can change by rewriting the rule, so the two are never collapsed.
const (
	ModelRoutingRuleTrue         = "true"
	ModelRoutingRuleFalse        = "false"
	ModelRoutingRuleUnknown      = "unknown"
	ModelRoutingRuleNotEvaluated = "not_evaluated"
)

// ModelRoutingRuleVerdict is one rule's own outcome in an evaluation. The
// dry-run preview and the request record carry the same shape, so a live
// request explains itself exactly the way the preview does.
type ModelRoutingRuleVerdict struct {
	RuleID         string                         `json:"rule_id"`
	Priority       int                            `json:"priority"`
	TargetMemberID string                         `json:"target_member_id"`
	TargetModel    string                         `json:"target_model,omitempty"`
	State          string                         `json:"state"`
	Selected       bool                           `json:"selected"`
	TargetMissing  bool                           `json:"target_missing,omitempty"`
	Fields         []ModelRoutingFieldObservation `json:"fields,omitempty"`
}

// ModelRoutingClassifierAnswers is what the classifier said for one request,
// recorded once per run rather than on every rule that read it. TaskType is
// empty when the answer was unusable; the decision's fallback_reason says why.
type ModelRoutingClassifierAnswers struct {
	TaskType   string                        `json:"task_type,omitempty"`
	Confidence float64                       `json:"confidence,omitempty"`
	Dimensions []ModelRoutingDimensionAnswer `json:"dimensions,omitempty"`
}

// ModelRoutingDimensionAnswer is one configured custom dimension. Known is the
// threshold verdict, so a known dimension is one a rule could match on;
// Produced separates "not answered" from "answered below the threshold",
// which need different fixes.
type ModelRoutingDimensionAnswer struct {
	Name     string  `json:"name"`
	Label    string  `json:"label,omitempty"`
	Score    float64 `json:"score,omitempty"`
	Known    bool    `json:"known"`
	Produced bool    `json:"produced"`
}

// ModelRoutingBindingMemberRemoved is the one reason a session binding is
// dropped while its group still serves requests: the member it names is no
// longer in the group, so the rules are judged again.
const ModelRoutingBindingMemberRemoved = "member_removed"

// ModelRoutingBindingReset names the session binding a request could not reuse.
type ModelRoutingBindingReset struct {
	Reason    string    `json:"reason"`
	MemberID  string    `json:"member_id"`
	ServiceID ServiceID `json:"service_id"`
	Model     string    `json:"model"`
}

type ModelRoutingDecision struct {
	SchemaVersion         int    `json:"schema_version"`
	VirtualModelID        string `json:"virtual_model_id"`
	GroupID               string `json:"group_id"`
	ConfigSnapshotVersion string `json:"config_snapshot_version"`
	// ConfigVersion is the group's saved configuration version the request
	// was routed under; zero for records written before versions existed.
	ConfigVersion   int                            `json:"config_version,omitempty"`
	SelectionSource ModelRoutingSelectionSource    `json:"selection_source"`
	RuleID          string                         `json:"rule_id,omitempty"`
	RulePriority    *int                           `json:"rule_priority,omitempty"`
	UsedDefault     bool                           `json:"used_default"`
	Fields          []ModelRoutingFieldObservation `json:"fields"`
	// Rules is every rule's own verdict in evaluation order. It is empty when
	// no rule was judged: a reused session binding, a strict protocol pin, or
	// a record written before verdicts were kept.
	Rules      []ModelRoutingRuleVerdict      `json:"rules,omitempty"`
	Classifier *ModelRoutingClassifierAnswers `json:"classifier,omitempty"`
	// BindingRequestID is the request whose rule evaluation chose the member a
	// reused session binding names; BindingExpiresAt is when that binding
	// lapses after this request.
	BindingRequestID     string                       `json:"binding_request_id,omitempty"`
	BindingExpiresAt     string                       `json:"binding_expires_at,omitempty"`
	BindingReset         *ModelRoutingBindingReset    `json:"binding_reset,omitempty"`
	MemberID             string                       `json:"member_id"`
	MemberServiceID      ServiceID                    `json:"member_service_id"`
	MemberModel          string                       `json:"member_model"`
	FallbackReason       string                       `json:"fallback_reason,omitempty"`
	Execution            ModelRoutingExecution        `json:"execution"`
	PrivacyAttempts      []ModelRoutingPrivacyAttempt `json:"privacy_attempts"`
	DurationMS           int                          `json:"duration_ms"`
	ClassifierDurationMS int                          `json:"classifier_duration_ms,omitempty"`
	ErrorCode            string                       `json:"error_code,omitempty"`
}

func (decision ModelRoutingDecision) Validate() error {
	if decision.SchemaVersion != 1 {
		return fmt.Errorf("model_routing schema_version must be 1")
	}
	if !strings.HasPrefix(decision.VirtualModelID, ModelRoutingModelPrefix) || len(decision.VirtualModelID) > len(ModelRoutingModelPrefix)+64 {
		return fmt.Errorf("virtual_model_id must be an astrlink group id")
	}
	if err := validateModelRoutingSlug("group_id", decision.GroupID, 64); err != nil {
		return err
	}
	if decision.VirtualModelID != ModelRoutingModelPrefix+decision.GroupID {
		return fmt.Errorf("virtual_model_id does not match group_id")
	}
	if len(decision.ConfigSnapshotVersion) != 64 || strings.ToLower(decision.ConfigSnapshotVersion) != decision.ConfigSnapshotVersion {
		return fmt.Errorf("config_snapshot_version must be a lowercase sha256")
	}
	if _, err := hex.DecodeString(decision.ConfigSnapshotVersion); err != nil {
		return fmt.Errorf("config_snapshot_version must be a lowercase sha256")
	}
	if !decision.SelectionSource.Valid() {
		return fmt.Errorf("unknown model routing selection_source %q", decision.SelectionSource)
	}
	if decision.RulePriority != nil && (*decision.RulePriority < 0 || *decision.RulePriority > MaxModelRoutingPriority) {
		return fmt.Errorf("rule_priority is invalid")
	}
	if err := validateModelRoutingSlug("member_id", decision.MemberID, MaxModelRoutingMemberIDRunes); err != nil {
		return err
	}
	if err := decision.MemberServiceID.Validate(); err != nil {
		return err
	}
	if err := validateModelRoutingModel("member_model", decision.MemberModel); err != nil {
		return err
	}
	if decision.Fields == nil {
		return fmt.Errorf("model routing fields must be an array")
	}
	if len(decision.Fields) > MaxModelRoutingObservationFields {
		return fmt.Errorf("model routing fields exceed %d", MaxModelRoutingObservationFields)
	}
	for _, field := range decision.Fields {
		if err := validateModelRoutingValue("field", field.Field); err != nil {
			return err
		}
		if field.MatchCount < 0 {
			return fmt.Errorf("field match_count must be non-negative")
		}
		chosen := 0
		for _, candidate := range field.Candidates {
			if err := validateModelRoutingValue("candidates.value", candidate.Value); err != nil {
				return err
			}
			if math.IsNaN(candidate.Score) || math.IsInf(candidate.Score, 0) || candidate.Score < 0 || candidate.Score > 1 {
				return fmt.Errorf("field %q candidate %q score must be a finite value in [0,1]", field.Field, candidate.Value)
			}
			if candidate.Rank < 0 {
				return fmt.Errorf("field %q candidate %q rank must be non-negative", field.Field, candidate.Value)
			}
			if candidate.Chosen {
				chosen++
			}
		}
		if chosen > 1 {
			return fmt.Errorf("field %q must not mark more than one chosen candidate", field.Field)
		}
	}
	if decision.ConfigVersion < 0 {
		return fmt.Errorf("config_version must be non-negative")
	}
	if len(decision.Rules) > MaxModelRoutingRulesPerGroup {
		return fmt.Errorf("model routing rules exceed %d", MaxModelRoutingRulesPerGroup)
	}
	for _, verdict := range decision.Rules {
		if err := verdict.validate(); err != nil {
			return err
		}
	}
	if decision.Classifier != nil {
		if err := decision.Classifier.validate(); err != nil {
			return err
		}
	}
	if decision.BindingRequestID != "" {
		if err := RequestID(decision.BindingRequestID).Validate(); err != nil {
			return fmt.Errorf("binding_request_id: %w", err)
		}
	}
	if decision.BindingExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, decision.BindingExpiresAt); err != nil {
			return fmt.Errorf("binding_expires_at must be an RFC 3339 time")
		}
	}
	if reset := decision.BindingReset; reset != nil {
		if reset.Reason != ModelRoutingBindingMemberRemoved {
			return fmt.Errorf("unknown binding_reset reason %q", reset.Reason)
		}
		if err := validateModelRoutingSlug("binding_reset member_id", reset.MemberID, MaxModelRoutingMemberIDRunes); err != nil {
			return err
		}
		if err := reset.ServiceID.Validate(); err != nil {
			return err
		}
		if err := validateModelRoutingModel("binding_reset model", reset.Model); err != nil {
			return err
		}
	}
	if !decision.Execution.Status.Valid() || decision.Execution.AttemptCount < 0 {
		return fmt.Errorf("invalid model routing execution")
	}
	if decision.DurationMS < 0 || decision.ClassifierDurationMS < 0 {
		return fmt.Errorf("model routing durations must be non-negative")
	}
	if decision.PrivacyAttempts == nil {
		return fmt.Errorf("model routing privacy_attempts must be an array")
	}
	if len(decision.PrivacyAttempts) > 64 {
		return fmt.Errorf("model routing privacy attempts exceed 64")
	}
	for _, attempt := range decision.PrivacyAttempts {
		if attempt.AttemptIndex < 0 || attempt.FindingCount < 0 || attempt.DurationMS < 0 {
			return fmt.Errorf("invalid model routing privacy attempt")
		}
		if err := attempt.ServiceID.Validate(); err != nil {
			return err
		}
		if !attempt.Decision.Valid() {
			return fmt.Errorf("unknown model routing privacy decision %q", attempt.Decision)
		}
	}
	return nil
}

func (verdict ModelRoutingRuleVerdict) validate() error {
	if utf8.RuneCountInString(verdict.RuleID) > MaxModelRoutingRuleIDRunes || utf8.RuneCountInString(verdict.TargetMemberID) > MaxModelRoutingMemberIDRunes {
		return fmt.Errorf("rule verdict ids are too long")
	}
	if verdict.Priority < 0 || verdict.Priority > MaxModelRoutingPriority {
		return fmt.Errorf("rule verdict priority is invalid")
	}
	if verdict.TargetModel != "" {
		if err := validateModelRoutingModel("rule verdict target_model", verdict.TargetModel); err != nil {
			return err
		}
	}
	switch verdict.State {
	case ModelRoutingRuleTrue, ModelRoutingRuleFalse, ModelRoutingRuleUnknown, ModelRoutingRuleNotEvaluated:
	default:
		return fmt.Errorf("unknown rule verdict state %q", verdict.State)
	}
	if len(verdict.Fields) > MaxModelRoutingObservationFields {
		return fmt.Errorf("rule verdict fields exceed %d", MaxModelRoutingObservationFields)
	}
	for _, field := range verdict.Fields {
		if err := validateModelRoutingValue("rule verdict field", field.Field); err != nil {
			return err
		}
		if field.MatchCount < 0 || utf8.RuneCountInString(field.Value) > MaxModelRoutingValueRunes {
			return fmt.Errorf("rule verdict field %q is invalid", field.Field)
		}
	}
	return nil
}

func (answers ModelRoutingClassifierAnswers) validate() error {
	if utf8.RuneCountInString(answers.TaskType) > MaxModelRoutingValueRunes || !unitScore(answers.Confidence) {
		return fmt.Errorf("classifier task_type answer is invalid")
	}
	if len(answers.Dimensions) > MaxModelRoutingObservationFields {
		return fmt.Errorf("classifier dimensions exceed %d", MaxModelRoutingObservationFields)
	}
	for _, dimension := range answers.Dimensions {
		if err := validateModelRoutingValue("classifier dimension", dimension.Name); err != nil {
			return err
		}
		if utf8.RuneCountInString(dimension.Label) > MaxModelRoutingValueRunes || !unitScore(dimension.Score) {
			return fmt.Errorf("classifier dimension %q answer is invalid", dimension.Name)
		}
	}
	return nil
}

func unitScore(score float64) bool {
	return !math.IsNaN(score) && !math.IsInf(score, 0) && score >= 0 && score <= 1
}

func (decision *ModelRoutingDecision) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	type plain ModelRoutingDecision
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := ModelRoutingDecision(value).Validate(); err != nil {
		return err
	}
	*decision = ModelRoutingDecision(value)
	return nil
}

// ModelRoutingFacts contains only bounded, typed inputs to the evaluator.
type ModelRoutingFacts struct {
	Protocol  string
	MediaKind string
	TaskType  string
	// TaskTypeCandidates is the classifier's ordered label sequence: ranked,
	// deduplicated and threshold-filtered; empty means the field is unknown.
	// It deliberately carries no scores — scores are diagnostics and stay on
	// the Outcome/observation side, so candidates can never feed scoring.
	TaskTypeCandidates                                                                                                                                                 []string
	Streaming, Continuation, HasTools, HasMedia, HasImage, HasAudio, HasVideo                                                                                          *bool
	TurnCount, UserMessageCount, AssistantMessageCount, ToolMessageCount, ToolDeclarationCount, ToolCallCount, InputBytes, InputTextRunes, EstimatedInputContextTokens *int
	LiteralText                                                                                                                                                        string
	// Dimensions carries the user-defined dimensions' evaluated values. It
	// never contains task_type, whose value stays in the TaskType fields
	// above: those carry the candidate-replacement mechanism that task_type
	// alone needs. Empty for a single-dimension configuration.
	Dimensions map[string]DimensionFact
	// SystemTime is the one local clock sample the request was evaluated
	// under; nil means the time fact is unknown and system_time leaves stay
	// triUnknown. It is evaluator input only, never persisted.
	SystemTime *time.Time
}

// DimensionFact is one custom dimension's evaluated input. Known is false when
// the dimension has no usable value, which leaves every rule that tests it
// unknown and therefore unmatched — the classifier seam never blocks a request
// over a missing dimension. Produced separates "the provider did not answer
// this dimension" from "the provider answered with a value outside the
// dimension's options": both stay unknown for selection, but only the former is
// a provider-side gap worth reporting as such.
//
// Score is the provider-reported confidence for this dimension's answer. It is
// compared against the same single LowConfidenceThreshold task_type uses — one
// threshold for every dimension, not one per dimension — and an answer below it
// leaves the dimension unknown exactly like a missing one. The field exists so
// that comparison can happen in the fact builder, which is the only layer that
// sees the threshold; the evaluator receives a dimension that is already
// resolved to known-or-unknown and never ranks dimensions itself.
type DimensionFact struct {
	Value    string
	Score    float64
	Known    bool
	Produced bool
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSONForDuplicateKeys(decoder); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}

func walkJSONForDuplicateKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key must be a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := walkJSONForDuplicateKeys(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := walkJSONForDuplicateKeys(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return nil
	}
}

func ModelRoutingSnapshotVersion(groups []ModelRoutingGroup) (string, error) {
	return ModelRoutingSnapshotVersionWithDimensions(groups, nil)
}

// ModelRoutingSnapshotVersionWithDimensions is ModelRoutingSnapshotVersion
// against a caller-supplied dimension set. The version covers every group, so
// one group whose rules name a user-defined dimension would fail the
// built-in-only validation and take every other group's requests down with
// it; the inference path therefore passes the dimensions it validated the
// requested group against. The dimensions are not hashed: they are classifier
// settings, and the version identifies the routing configuration alone.
func ModelRoutingSnapshotVersionWithDimensions(groups []ModelRoutingGroup, dimensions []ClassifierDimension) (string, error) {
	if err := ValidateModelRoutingGroupsWithDimensions(groups, nil, dimensions); err != nil {
		return "", err
	}
	encodedGroups, err := json.Marshal(groups)
	if err != nil {
		return "", err
	}
	var copyGroups []ModelRoutingGroup
	if err := json.Unmarshal(encodedGroups, &copyGroups); err != nil {
		return "", err
	}
	for _, group := range copyGroups {
		if err := group.ValidateWithDimensions(dimensions); err != nil {
			return "", err
		}
	}
	sort.Slice(copyGroups, func(i, j int) bool { return copyGroups[i].ID < copyGroups[j].ID })
	for i := range copyGroups {
		canonicalizeModelRoutingGroup(&copyGroups[i])
	}
	encoded, err := json.Marshal(copyGroups)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// canonicalizeModelRoutingGroup puts one group into the order every content
// hash reads it in: members by ID, rules in evaluation order, condition lists
// sorted. Two saves that differ only in that order are the same configuration.
func canonicalizeModelRoutingGroup(group *ModelRoutingGroup) {
	sort.Slice(group.Members, func(a, b int) bool { return group.Members[a].ID < group.Members[b].ID })
	sort.Slice(group.Rules, func(a, b int) bool {
		if group.Rules[a].Priority != group.Rules[b].Priority {
			return group.Rules[a].Priority < group.Rules[b].Priority
		}
		return group.Rules[a].ID < group.Rules[b].ID
	})
	for j := range group.Rules {
		canonicalizeCondition(&group.Rules[j].When)
	}
}

func canonicalizeCondition(condition *ModelRoutingCondition) {
	if condition == nil {
		return
	}
	for i := range condition.All {
		canonicalizeCondition(&condition.All[i])
	}
	for i := range condition.Any {
		canonicalizeCondition(&condition.Any[i])
	}
	sort.Slice(condition.All, func(i, j int) bool {
		a, _ := json.Marshal(condition.All[i])
		b, _ := json.Marshal(condition.All[j])
		return string(a) < string(b)
	})
	sort.Slice(condition.Any, func(i, j int) bool {
		a, _ := json.Marshal(condition.Any[i])
		b, _ := json.Marshal(condition.Any[j])
		return string(a) < string(b)
	})
	canonicalizeCondition(condition.Not)
}

type ModelRoutingBinding struct {
	Principal             string     `json:"principal"`
	SessionID             SessionID  `json:"session_id"`
	Protocol              ProtocolID `json:"protocol"`
	VirtualModelID        string     `json:"virtual_model_id"`
	GroupID               string     `json:"group_id"`
	MemberID              string     `json:"member_id"`
	ServiceID             ServiceID  `json:"service_id"`
	Model                 string     `json:"model"`
	ConfigSnapshotVersion string     `json:"config_snapshot_version"`
	// RequestID is the request whose rule evaluation chose this member. Later
	// requests that reuse the binding keep it, so each of them can point back
	// at the judgment it inherited.
	RequestID string `json:"request_id,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	ExpiresAt string `json:"expires_at"`
}

func (binding ModelRoutingBinding) Validate() error {
	if binding.Principal == "" || utf8.RuneCountInString(binding.Principal) > 256 {
		return fmt.Errorf("binding principal is invalid")
	}
	if err := binding.SessionID.Validate(); err != nil {
		return err
	}
	if err := binding.Protocol.Validate(); err != nil {
		return err
	}
	if !strings.HasPrefix(binding.VirtualModelID, ModelRoutingModelPrefix) {
		return fmt.Errorf("binding virtual_model_id is invalid")
	}
	if err := validateModelRoutingSlug("binding group_id", binding.GroupID, 64); err != nil {
		return err
	}
	if binding.VirtualModelID != ModelRoutingModelPrefix+binding.GroupID {
		return fmt.Errorf("binding virtual_model_id does not match group")
	}
	if err := validateModelRoutingSlug("binding member_id", binding.MemberID, MaxModelRoutingMemberIDRunes); err != nil {
		return err
	}
	if err := binding.ServiceID.Validate(); err != nil {
		return err
	}
	if binding.RequestID != "" {
		if err := RequestID(binding.RequestID).Validate(); err != nil {
			return fmt.Errorf("binding request_id: %w", err)
		}
	}
	return validateModelRoutingModel("binding model", binding.Model)
}

// ModelRoutingBindingCurrent reports whether a session binding may still be
// reused under the given configuration: its group is enabled and still has the
// member the binding names. Any other binding is dropped by the next request,
// which judges the rules again.
func ModelRoutingBindingCurrent(binding ModelRoutingBinding, groups []ModelRoutingGroup) bool {
	for _, group := range groups {
		if group.ID == binding.GroupID {
			return group.Enabled && group.HasMember(binding.MemberID, binding.ServiceID, binding.Model)
		}
	}
	return false
}
