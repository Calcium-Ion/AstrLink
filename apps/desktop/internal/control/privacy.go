package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// privacyKinds is every kind a detector may emit. The Regex detector emits
// the first seven and the local model adds the rest.
var privacyKinds = []string{
	"common_secret",
	"payment_card",
	"account",
	"email",
	"phone",
	"url",
	"ip_address",
	"private_person",
	"private_address",
	"private_date",
}

// placeholderStyleLockedKinds are kinds whose placeholder shape is a safety
// property rather than a preference: a credential dressed up as a
// usable-looking key invites the model to call an API with it, and the
// person, address, and date kinds have no reserved namespace to draw a
// stand-in from that cannot collide with a real one.
var placeholderStyleLockedKinds = []string{
	"common_secret",
	"private_person",
	"private_address",
	"private_date",
}

// customPrivacyKindPattern is Core's contract.IsCustomPrivacyKind: a prefix
// no built-in kind uses, then lowercase words joined by single underscores.
var customPrivacyKindPattern = regexp.MustCompile(`^custom_[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)

const (
	maxPrivacyCustomKinds         = 32
	maxPrivacyCustomKindBytes     = 40
	maxPrivacyCustomKindLabelRune = 32
)

func isCustomPrivacyKind(kind string) bool {
	return len(kind) <= maxPrivacyCustomKindBytes && customPrivacyKindPattern.MatchString(kind)
}

const (
	maxPrivacyAllowlistRules      = 128
	maxPrivacyAllowlistValueChars = 256
	maxDryRunSampleBytes          = 256 * 1024
	maxSafeInteger                = 9_007_199_254_740_991
)

// PolicyRecord is a privacy policy and the ETag its update must quote.
type PolicyRecord struct {
	Policy json.RawMessage `json:"policy"`
	ETag   string          `json:"etag"`
}

// ServiceRecord is a service and the ETag its update must quote.
type ServiceRecord struct {
	Service json.RawMessage `json:"service"`
	ETag    string          `json:"etag"`
}

// allowedKeys rejects a key outside allowed and a missing required key.
func allowedKeys(o object, allowed, required []string, context string) error {
	for _, key := range sortedKeys(o) {
		if !slices.Contains(allowed, key) {
			return fmt.Errorf("%s contains unexpected field %s", context, key)
		}
	}
	for _, key := range required {
		if _, ok := o[key]; !ok {
			return fmt.Errorf("%s omitted %s", context, key)
		}
	}
	return nil
}

// exactKeys requires value to be an object with exactly the expected keys.
func exactKeys(value any, expected []string, context string) (object, error) {
	o, ok := asObject(value)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", context)
	}
	if len(o) != len(expected) {
		return nil, fmt.Errorf("%s has missing or unexpected fields", context)
	}
	for _, key := range expected {
		if _, ok := o[key]; !ok {
			return nil, fmt.Errorf("%s omitted %s", context, key)
		}
	}
	return o, nil
}

func asF64(value any) (float64, bool) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

func isBool(value any) bool {
	_, ok := value.(bool)
	return ok
}

func isString(value any) bool {
	_, ok := value.(string)
	return ok
}

func safeJSONInteger(value any, label string) (uint64, error) {
	parsed, ok := asU64(value)
	if !ok {
		return 0, fmt.Errorf("%s must be a non-negative integer", label)
	}
	if parsed > maxSafeInteger {
		return 0, fmt.Errorf("%s exceeds the IPC safe integer range", label)
	}
	return parsed, nil
}

func validateMetadataString(value any, minimum, maximum int, label string) (string, error) {
	text, err := validateString(value, minimum, maximum, label)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) != text || hasControl(text) {
		return "", fmt.Errorf("%s must be trimmed and contain no control characters", label)
	}
	return text, nil
}

func validateStringArray(value any, maximumItems int, label string) error {
	values, ok := asArray(value)
	if !ok {
		return fmt.Errorf("%s must be an array", label)
	}
	if len(values) > maximumItems {
		return fmt.Errorf("%s contains too many values", label)
	}
	seen := map[string]bool{}
	for _, value := range values {
		parsed, err := validateMetadataString(value, 1, 64, label)
		if err != nil {
			return err
		}
		if seen[parsed] {
			return fmt.Errorf("%s contains duplicate values", label)
		}
		seen[parsed] = true
	}
	return nil
}

// PolicyRecordFrom checks a policy response and its ETag.
func PolicyRecordFrom(etag string, body []byte) (PolicyRecord, error) {
	if etag == "" {
		return PolicyRecord{}, errors.New("Core policy response omitted ETag")
	}
	if err := validateStrongETag(etag); err != nil {
		return PolicyRecord{}, err
	}
	policy, err := parsePrivacyPolicy(body)
	if err != nil {
		return PolicyRecord{}, err
	}
	return PolicyRecord{Policy: policy, ETag: etag}, nil
}

// ServiceRecordFrom checks a service response and its ETag.
func ServiceRecordFrom(etag string, body []byte) (ServiceRecord, error) {
	if etag == "" {
		return ServiceRecord{}, errors.New("Core service response omitted ETag")
	}
	if err := validateETag(etag); err != nil {
		return ServiceRecord{}, err
	}
	service, err := decodeValue(body)
	if err != nil {
		return ServiceRecord{}, fmt.Errorf("service response returned invalid JSON: %v", err)
	}
	if o, ok := asObject(service); ok {
		if proxy, ok := o["proxy"]; ok {
			id, _ := stringField(o, "id")
			if err := validateProxy(proxy, id, false); err != nil {
				return ServiceRecord{}, err
			}
		}
		if policy, ok := o["failure_policy"]; ok {
			if err := validateFailurePolicy(policy); err != nil {
				return ServiceRecord{}, err
			}
		}
	}
	return ServiceRecord{Service: body, ETag: etag}, nil
}

// ServiceOrder is the order services are tried in.
type ServiceOrder struct {
	ServiceIDs []string `json:"service_ids"`
	ETag       string   `json:"etag"`
}

// ServiceOrderFrom checks a service order response and its ETag.
func ServiceOrderFrom(etag string, body []byte) (ServiceOrder, error) {
	if etag == "" {
		return ServiceOrder{}, errors.New("Core service order omitted ETag")
	}
	if err := validateStrongETag(etag); err != nil {
		return ServiceOrder{}, err
	}
	order, err := decodeValue(body)
	if err != nil {
		return ServiceOrder{}, fmt.Errorf("invalid service order: %v", err)
	}
	o, _ := asObject(order)
	ids, ok := asArray(o["service_ids"])
	if !ok {
		return ServiceOrder{}, errors.New("service order omitted service_ids")
	}
	result := ServiceOrder{ServiceIDs: make([]string, 0, len(ids)), ETag: etag}
	seen := map[string]bool{}
	for _, value := range ids {
		id, ok := asString(value)
		if !ok {
			return ServiceOrder{}, errors.New("invalid service ID")
		}
		if err := ValidateResourceID(id); err != nil {
			return ServiceOrder{}, err
		}
		if seen[id] {
			return ServiceOrder{}, errors.New("duplicate service ID")
		}
		seen[id] = true
		result.ServiceIDs = append(result.ServiceIDs, id)
	}
	return result, nil
}

func parsePrivacyPolicyPage(body []byte) (json.RawMessage, error) {
	page, err := decodeValue(body)
	if err != nil {
		return nil, fmt.Errorf("policy list returned invalid JSON: %v", err)
	}
	o, err := exactKeys(page, []string{"items", "next_cursor"}, "policy list")
	if err != nil {
		return nil, err
	}
	if o["next_cursor"] != nil {
		return nil, errors.New("policy list next_cursor must be null")
	}
	items, ok := asArray(o["items"])
	if !ok {
		return nil, errors.New("policy list items must be an array")
	}
	if len(items) != 1 {
		return nil, errors.New("policy list must contain the singleton privacy policy")
	}
	for _, item := range items {
		if err := normalizePrivacyPolicyDefaults(item); err != nil {
			return nil, err
		}
		if err := validatePrivacyPolicyValue(item); err != nil {
			return nil, err
		}
	}
	return encodeValue(page), nil
}

func parsePrivacyPolicy(body []byte) (json.RawMessage, error) {
	policy, err := decodeValue(body)
	if err != nil {
		return nil, fmt.Errorf("policy response returned invalid JSON: %v", err)
	}
	if err := normalizePrivacyPolicyDefaults(policy); err != nil {
		return nil, err
	}
	if err := validatePrivacyPolicyValue(policy); err != nil {
		return nil, err
	}
	return encodeValue(policy), nil
}

// normalizePrivacyPolicyDefaults fills the two regex fields a Core that
// predates them omits.
func normalizePrivacyPolicyDefaults(policy any) error {
	o, ok := asObject(policy)
	if !ok {
		return errors.New("privacy policy must be an object")
	}
	if _, ok := o["regex_source"]; !ok {
		o["regex_source"] = "builtin"
	}
	if _, ok := o["custom_regex_rules"]; !ok {
		o["custom_regex_rules"] = []any{}
	}
	return nil
}

func validatePrivacyPolicyValue(policy any) error {
	o, ok := asObject(policy)
	if !ok {
		return errors.New("privacy policy must be an object")
	}
	// The per-kind, allowlist, and restore-scope fields are optional on the
	// wire so a Core predating them still parses; the interface falls back
	// to the same defaults Core would have applied.
	if err := allowedKeys(o, []string{
		"id", "name", "enabled", "priority", "detector", "local_model_id",
		"min_confidence", "regex_source", "custom_regex_rules", "kind_rules",
		"allowlist_rules", "match", "request_action", "response_action",
		"response_restore", "restore_tool_arguments", "placeholder_notice",
		"skip_tool_declarations", "inspect_additional_tools",
	}, []string{
		"id", "name", "enabled", "priority", "detector", "local_model_id",
		"min_confidence", "regex_source", "custom_regex_rules", "match",
		"request_action", "response_action", "response_restore",
	}, "privacy policy"); err != nil {
		return err
	}
	id, _ := stringField(o, "id")
	priority, isInteger := asI64(o["priority"])
	if id != "policy_privacy_default" || !isInteger || priority != 0 {
		return errors.New("Core returned an unexpected privacy policy singleton")
	}
	if name, _ := stringField(o, "name"); name == "" {
		return errors.New("privacy policy name must be a non-empty string")
	}
	if !isBool(o["enabled"]) {
		return errors.New("privacy policy enabled must be a boolean")
	}
	if !isBool(o["response_restore"]) {
		return errors.New("privacy policy response_restore must be a boolean")
	}
	if err := validatePrivacyConfidence(o["min_confidence"], "privacy policy min_confidence"); err != nil {
		return err
	}
	if err := validatePrivacyDetector(o["detector"]); err != nil {
		return err
	}
	var hasLocalModel bool
	switch localModelID := o["local_model_id"].(type) {
	case string:
		if err := validatePrivacyModelID(localModelID); err != nil {
			return err
		}
		hasLocalModel = true
	case nil:
	default:
		return errors.New("privacy policy local_model_id must be an installation ID or null")
	}
	detector, _ := stringField(o, "detector")
	if !(detector == "regex" && !hasLocalModel || detector == "local_model" && hasLocalModel) {
		return errors.New("privacy policy detector and local_model_id are inconsistent")
	}
	if err := validatePrivacyRegexSource(o["regex_source"]); err != nil {
		return err
	}
	if err := validatePrivacyCustomRegexRules(o["custom_regex_rules"]); err != nil {
		return err
	}
	if err := validateDeclaredCustomKinds(o["custom_regex_rules"], o["kind_rules"]); err != nil {
		return err
	}
	rules, _ := asArray(o["custom_regex_rules"])
	if source, _ := stringField(o, "regex_source"); detector == "regex" && source == "custom" && len(rules) == 0 {
		return errors.New("privacy policy custom_regex_rules must contain at least one rule when regex_source is custom")
	}
	if err := validatePrivacyAction(o["request_action"]); err != nil {
		return err
	}
	if err := validatePrivacyAction(o["response_action"]); err != nil {
		return err
	}
	if action, _ := stringField(o, "response_action"); action != "allow" {
		return errors.New("privacy policy response_action must remain allow")
	}
	if kindRules, ok := o["kind_rules"]; ok {
		if err := validatePrivacyKindRules(kindRules); err != nil {
			return err
		}
	}
	if allowlistRules, ok := o["allowlist_rules"]; ok {
		if err := validatePrivacyAllowlistRules(allowlistRules); err != nil {
			return err
		}
	}
	for _, key := range []string{"restore_tool_arguments", "placeholder_notice", "skip_tool_declarations", "inspect_additional_tools"} {
		if value, ok := o[key]; ok && !isBool(value) {
			return fmt.Errorf("privacy policy %s must be a boolean", key)
		}
	}
	_, err := exactKeys(o["match"], nil, "privacy policy match")
	return err
}

func validatePrivacyKindRules(value any) error {
	rules, ok := asArray(value)
	if !ok {
		return errors.New("privacy policy kind_rules must be an array")
	}
	if len(rules) > len(privacyKinds)+maxPrivacyCustomKinds {
		return errors.New("privacy policy kind_rules must hold at most one rule per kind")
	}
	kinds := map[string]bool{}
	labels := map[string]bool{}
	for index, rule := range rules {
		context := fmt.Sprintf("privacy policy kind rule %d", index)
		o, ok := asObject(rule)
		if !ok {
			return fmt.Errorf("%s must be an object", context)
		}
		// notify is optional so a Core predating it still parses; label
		// names a custom kind only.
		if err := allowedKeys(o, []string{"kind", "enabled", "style", "notify", "label"}, []string{"kind", "enabled", "style"}, context); err != nil {
			return err
		}
		kind, ok := stringField(o, "kind")
		if !ok {
			return fmt.Errorf("%s kind must be a string", context)
		}
		custom := isCustomPrivacyKind(kind)
		if !custom && !slices.Contains(privacyKinds, kind) {
			return fmt.Errorf("%s kind is unknown", context)
		}
		if kinds[kind] {
			return fmt.Errorf("%s repeats kind %s", context, kind)
		}
		kinds[kind] = true
		label, hasLabel := o["label"]
		if !custom && hasLabel {
			return fmt.Errorf("%s label is only for custom kinds", context)
		}
		if custom {
			text, ok := asString(label)
			if !ok || !validCustomKindLabel(text) {
				return fmt.Errorf("%s label must contain 1 to %d characters without surrounding spaces", context, maxPrivacyCustomKindLabelRune)
			}
			folded := strings.ToLower(text)
			if labels[folded] {
				return fmt.Errorf("%s repeats label %s", context, text)
			}
			labels[folded] = true
			if len(labels) > maxPrivacyCustomKinds {
				return fmt.Errorf("privacy policy kind_rules must declare at most %d custom kinds", maxPrivacyCustomKinds)
			}
		}
		if !isBool(o["enabled"]) {
			return fmt.Errorf("%s enabled must be a boolean", context)
		}
		if notify, ok := o["notify"]; ok && !isBool(notify) {
			return fmt.Errorf("%s notify must be a boolean", context)
		}
		style, ok := stringField(o, "style")
		if !ok {
			return fmt.Errorf("%s style must be a string", context)
		}
		if style != "natural" && style != "token" {
			return fmt.Errorf("%s style must be natural or token", context)
		}
		if (custom || slices.Contains(placeholderStyleLockedKinds, kind)) && style != "token" {
			return fmt.Errorf("%s must keep the token placeholder style", context)
		}
	}
	return nil
}

func validCustomKindLabel(label string) bool {
	n := runeCount(label)
	return n >= 1 && n <= maxPrivacyCustomKindLabelRune &&
		strings.TrimSpace(label) == label &&
		strings.IndexFunc(label, unicode.IsControl) < 0
}

// validateDeclaredCustomKinds rejects a custom regex rule whose kind no kind
// rule declares, as Core does.
func validateDeclaredCustomKinds(regexRules, kindRules any) error {
	rules, _ := asArray(regexRules)
	declared := map[string]bool{}
	if list, ok := asArray(kindRules); ok {
		for _, rule := range list {
			if o, ok := asObject(rule); ok {
				if kind, ok := stringField(o, "kind"); ok {
					declared[kind] = true
				}
			}
		}
	}
	for index, rule := range rules {
		o, _ := asObject(rule)
		kind, _ := stringField(o, "kind")
		if isCustomPrivacyKind(kind) && !declared[kind] {
			return fmt.Errorf("custom regex rule %d names an undeclared custom kind", index)
		}
	}
	return nil
}

func validatePrivacyAllowlistRules(value any) error {
	rules, ok := asArray(value)
	if !ok {
		return errors.New("privacy policy allowlist_rules must be an array")
	}
	if len(rules) > maxPrivacyAllowlistRules {
		return fmt.Errorf("privacy policy allowlist_rules must contain at most %d rules", maxPrivacyAllowlistRules)
	}
	for index, rule := range rules {
		context := fmt.Sprintf("privacy policy allowlist rule %d", index)
		o, err := exactKeys(rule, []string{"type", "value"}, context)
		if err != nil {
			return err
		}
		ruleType, ok := stringField(o, "type")
		if !ok {
			return fmt.Errorf("%s type must be a string", context)
		}
		switch ruleType {
		// The grammar of a CIDR block is Core's to enforce, so the interface
		// has one definition of a valid range rather than two that can
		// disagree about which addresses an operator exempted.
		case "literal", "domain_suffix", "cidr":
		default:
			return fmt.Errorf("%s type is unknown", context)
		}
		text, ok := stringField(o, "value")
		if !ok {
			return fmt.Errorf("%s value must be a string", context)
		}
		if n := runeCount(text); n < 1 || n > maxPrivacyAllowlistValueChars {
			return fmt.Errorf("%s value must contain 1 to %d characters", context, maxPrivacyAllowlistValueChars)
		}
	}
	return nil
}

func validatePrivacyPolicyPatch(patch any) error {
	o, ok := asObject(patch)
	if !ok {
		return errors.New("privacy policy patch must be an object")
	}
	if len(o) == 0 {
		return errors.New("privacy policy patch must change at least one field")
	}
	for _, key := range sortedKeys(o) {
		value := o[key]
		var err error
		switch key {
		case "enabled", "response_restore", "restore_tool_arguments", "placeholder_notice",
			"skip_tool_declarations", "inspect_additional_tools":
			if !isBool(value) {
				err = fmt.Errorf("privacy policy %s patch must be a boolean", key)
			}
		case "detector":
			err = validatePrivacyDetector(value)
		case "local_model_id":
			switch id := value.(type) {
			case nil:
			case string:
				err = validatePrivacyModelID(id)
			default:
				err = errors.New("privacy policy local_model_id patch must be an installation ID or null")
			}
		case "min_confidence":
			err = validatePrivacyConfidence(value, "privacy policy min_confidence patch")
		case "regex_source":
			err = validatePrivacyRegexSource(value)
		case "custom_regex_rules":
			err = validatePrivacyCustomRegexRules(value)
		case "kind_rules":
			err = validatePrivacyKindRules(value)
		case "allowlist_rules":
			err = validatePrivacyAllowlistRules(value)
		case "request_action":
			err = validatePrivacyAction(value)
		default:
			err = fmt.Errorf("privacy policy patch contains unexpected field %s", key)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func validatePrivacyRegexSource(value any) error {
	if source, _ := asString(value); source == "builtin" || source == "custom" {
		return nil
	}
	return errors.New("privacy policy regex_source must be builtin or custom")
}

func validatePrivacyCustomRegexRules(value any) error {
	rules, ok := asArray(value)
	if !ok {
		return errors.New("privacy policy custom_regex_rules must be an array")
	}
	if len(rules) > 64 {
		return errors.New("privacy policy custom_regex_rules must contain at most 64 rules")
	}
	for index, rule := range rules {
		o, err := exactKeys(rule, []string{"kind", "pattern"}, fmt.Sprintf("custom regex rule %d", index))
		if err != nil {
			return err
		}
		kind, ok := stringField(o, "kind")
		if !ok {
			return fmt.Errorf("custom regex rule %d kind must be a string", index)
		}
		switch kind {
		case "email", "phone", "account", "payment_card", "ip_address", "url", "common_secret":
		default:
			if !isCustomPrivacyKind(kind) {
				return fmt.Errorf("custom regex rule %d kind is not a Regex detector kind", index)
			}
		}
		pattern, ok := stringField(o, "pattern")
		if !ok {
			return fmt.Errorf("custom regex rule %d pattern must be a string", index)
		}
		if n := runeCount(pattern); n < 1 || n > 512 {
			return fmt.Errorf("custom regex rule %d pattern must contain 1 to 512 characters", index)
		}
	}
	return nil
}

func parsePrivacyRegexBuiltinRules(body []byte) (json.RawMessage, error) {
	response, err := decodeValue(body)
	if err != nil {
		return nil, fmt.Errorf("regex builtin rules returned invalid JSON: %v", err)
	}
	o, err := exactKeys(response, []string{"rules"}, "regex builtin rules")
	if err != nil {
		return nil, err
	}
	if err := validatePrivacyCustomRegexRules(o["rules"]); err != nil {
		return nil, err
	}
	return body, nil
}

var dryRunProtocols = []string{
	"openai.chat",
	"openai.completions",
	"openai.responses",
	"openai.responses.compact",
	"anthropic.messages",
	"google.generate_content",
}

func validatePrivacyDryRunInput(input any) error {
	o, ok := asObject(input)
	if !ok {
		return errors.New("privacy policy dry-run must be an object")
	}
	for _, key := range sortedKeys(o) {
		if key != "protocol" && key != "sample_text" && key != "policy" {
			return fmt.Errorf("privacy policy dry-run contains unexpected field %s", key)
		}
	}
	for _, key := range []string{"protocol", "sample_text"} {
		if _, ok := o[key]; !ok {
			return fmt.Errorf("privacy policy dry-run omitted %s", key)
		}
	}
	protocol, ok := stringField(o, "protocol")
	if !ok {
		return errors.New("privacy policy dry-run protocol must be a string")
	}
	if !slices.Contains(dryRunProtocols, protocol) {
		return errors.New("privacy policy dry-run protocol is unsupported")
	}
	sample, ok := stringField(o, "sample_text")
	if !ok {
		return errors.New("privacy policy dry-run sample_text must be a string")
	}
	if sample == "" || len(sample) > maxDryRunSampleBytes {
		return fmt.Errorf("privacy policy dry-run sample_text must contain 1 to %d bytes", maxDryRunSampleBytes)
	}
	if policy, ok := o["policy"]; ok {
		return validatePrivacyPolicyPatch(policy)
	}
	return nil
}

func parsePrivacyDryRunResult(body []byte) (json.RawMessage, error) {
	result, err := decodeValue(body)
	if err != nil {
		return nil, fmt.Errorf("policy dry-run returned invalid JSON: %v", err)
	}
	o, ok := asObject(result)
	if !ok {
		return nil, errors.New("privacy policy dry-run result must be an object")
	}
	for _, key := range sortedKeys(o) {
		switch key {
		case "decision", "findings_summary", "findings", "inspected_body", "redacted_body", "redactions", "suppressed_findings":
		default:
			return nil, fmt.Errorf("privacy policy dry-run result contains unexpected field %s", key)
		}
	}
	for _, key := range []string{"decision", "findings_summary", "findings", "suppressed_findings", "inspected_body"} {
		if _, ok := o[key]; !ok {
			return nil, fmt.Errorf("privacy policy dry-run result omitted %s", key)
		}
	}
	if err := validatePrivacyAction(o["decision"]); err != nil {
		return nil, err
	}
	if !isString(o["findings_summary"]) {
		return nil, errors.New("privacy policy dry-run findings_summary must be a string")
	}
	if !isString(o["inspected_body"]) {
		return nil, errors.New("privacy policy dry-run inspected_body must be a string")
	}
	if redacted, ok := o["redacted_body"]; ok && !isString(redacted) {
		return nil, errors.New("privacy policy dry-run redacted_body must be a string")
	}
	if value, ok := o["redactions"]; ok {
		redactions, ok := asArray(value)
		if !ok {
			return nil, errors.New("privacy policy dry-run redactions must be an array")
		}
		if len(redactions) > 4096 {
			return nil, errors.New("privacy policy dry-run redactions exceed the limit")
		}
		for _, value := range redactions {
			redaction, ok := asObject(value)
			if !ok {
				return nil, errors.New("privacy policy dry-run redaction must be an object")
			}
			if err := allowedKeys(redaction, []string{"placeholder", "kind", "value", "style"}, []string{"placeholder", "kind", "value"}, "privacy policy dry-run redaction"); err != nil {
				return nil, err
			}
			if style, ok := redaction["style"]; ok {
				if err := validatePrivacyPlaceholderStyle(style); err != nil {
					return nil, err
				}
			}
			if placeholder, _ := stringField(redaction, "placeholder"); placeholder == "" {
				return nil, errors.New("privacy policy dry-run redaction placeholder must be a non-empty string")
			}
			kind, ok := stringField(redaction, "kind")
			if !ok {
				return nil, errors.New("privacy policy dry-run redaction kind must be a string")
			}
			if err := validatePrivacyDryRunKind(kind); err != nil {
				return nil, err
			}
			if !isString(redaction["value"]) {
				return nil, errors.New("privacy policy dry-run redaction value must be a string")
			}
		}
	}
	for _, key := range []string{"findings", "suppressed_findings"} {
		findings, ok := asArray(o[key])
		if !ok {
			return nil, fmt.Errorf("privacy policy dry-run %s must be an array", key)
		}
		if len(findings) > 4096 {
			return nil, fmt.Errorf("privacy policy dry-run %s exceed the limit", key)
		}
		for _, value := range findings {
			finding, ok := asObject(value)
			if !ok {
				return nil, errors.New("privacy policy dry-run finding must be an object")
			}
			if err := allowedKeys(finding, []string{"kind", "path", "start", "end", "confidence", "reason"}, []string{"kind", "path", "start", "end", "confidence"}, "privacy policy dry-run finding"); err != nil {
				return nil, err
			}
			if reason, ok := finding["reason"]; ok {
				if err := validatePrivacySuppressionReason(reason); err != nil {
					return nil, err
				}
			}
			kind, ok := stringField(finding, "kind")
			if !ok {
				return nil, errors.New("privacy policy dry-run finding kind must be a string")
			}
			if err := validatePrivacyDryRunKind(kind); err != nil {
				return nil, err
			}
			if path, _ := stringField(finding, "path"); path == "" {
				return nil, errors.New("privacy policy dry-run finding path must be a non-empty string")
			}
			start, ok := asU64(finding["start"])
			if !ok {
				return nil, errors.New("privacy policy dry-run finding start must be an integer")
			}
			end, ok := asU64(finding["end"])
			if !ok {
				return nil, errors.New("privacy policy dry-run finding end must be an integer")
			}
			if end <= start {
				return nil, errors.New("privacy policy dry-run finding end must be greater than start")
			}
			if err := validatePrivacyConfidence(finding["confidence"], "privacy policy dry-run finding confidence"); err != nil {
				return nil, err
			}
		}
	}
	return body, nil
}

func validatePrivacyDryRunKind(kind string) error {
	if slices.Contains(privacyKinds, kind) || isCustomPrivacyKind(kind) {
		return nil
	}
	return errors.New("privacy policy dry-run kind is unknown")
}

func validatePrivacySuppressionReason(value any) error {
	switch reason, _ := asString(value); reason {
	case "low_confidence", "kind_disabled", "allowlisted", "placeholder", "unrepresentable", "astrlink_identifier", "example_value":
		return nil
	}
	return errors.New("privacy policy dry-run finding reason is unknown")
}

func validatePrivacyPlaceholderStyle(value any) error {
	if style, _ := asString(value); style == "natural" || style == "token" {
		return nil
	}
	return errors.New("privacy policy placeholder style must be natural or token")
}

func validatePrivacyConfidence(value any, label string) error {
	confidence, ok := asF64(value)
	if !ok {
		return fmt.Errorf("%s must be a number", label)
	}
	if !(confidence >= 0 && confidence <= 1) {
		return fmt.Errorf("%s must be between 0 and 1", label)
	}
	return nil
}

func validatePrivacyDetector(value any) error {
	if detector, _ := asString(value); detector == "regex" || detector == "local_model" {
		return nil
	}
	return errors.New("privacy policy detector is invalid")
}

func validatePrivacyAction(value any) error {
	switch action, _ := asString(value); action {
	case "allow", "warn", "block", "redact":
		return nil
	}
	return errors.New("privacy policy action is invalid")
}

func parsePrivacyModelCatalog(body []byte) (json.RawMessage, error) {
	catalog, err := decodeValue(body)
	if err != nil {
		return nil, fmt.Errorf("privacy model catalog returned invalid JSON: %v", err)
	}
	o, err := exactKeys(catalog, []string{"items"}, "privacy model catalog")
	if err != nil {
		return nil, err
	}
	items, ok := asArray(o["items"])
	if !ok {
		return nil, errors.New("privacy model catalog items must be an array")
	}
	if len(items) > 100 {
		return nil, errors.New("privacy model catalog contains too many items")
	}
	ids := map[string]bool{}
	for _, item := range items {
		if err := validatePrivacyCatalogModel(item); err != nil {
			return nil, err
		}
		id, _ := stringField(item.(object), "id")
		if ids[id] {
			return nil, errors.New("privacy model catalog contains duplicate IDs")
		}
		ids[id] = true
	}
	return body, nil
}

func parsePrivacyModelProbe(body []byte) (json.RawMessage, error) {
	probe, err := decodeValue(body)
	if err != nil {
		return nil, fmt.Errorf("privacy model probe returned invalid JSON: %v", err)
	}
	o, err := exactKeys(probe, []string{
		"repo_id", "requested_revision", "revision", "name", "license", "languages",
		"adapter", "variants", "labels", "requires_label_mapping",
	}, "privacy model probe")
	if err != nil {
		return nil, err
	}
	if err := validateRepoID(o["repo_id"], "privacy model probe repo_id"); err != nil {
		return nil, err
	}
	if err := validateRequestedRevision(o["requested_revision"], "privacy model probe requested_revision"); err != nil {
		return nil, err
	}
	if err := validateCommit(o["revision"], "privacy model probe revision"); err != nil {
		return nil, err
	}
	if _, err := validateMetadataString(o["name"], 1, 128, "privacy model probe name"); err != nil {
		return nil, err
	}
	if o["license"] != nil {
		if _, err := validateMetadataString(o["license"], 1, 64, "privacy model probe license"); err != nil {
			return nil, err
		}
	}
	if err := validateStringArray(o["languages"], 32, "privacy model probe languages"); err != nil {
		return nil, err
	}
	if err := validatePrivacyModelAdapter(o["adapter"]); err != nil {
		return nil, err
	}
	if err := validatePrivacyModelVariants(o["variants"]); err != nil {
		return nil, err
	}
	labels, ok := asArray(o["labels"])
	if !ok {
		return nil, errors.New("privacy model probe labels must be an array")
	}
	if len(labels) == 0 || len(labels) > 256 {
		return nil, errors.New("privacy model probe must contain 1 to 256 labels")
	}
	seen := map[string]bool{}
	requiresLabelMapping := false
	for _, value := range labels {
		label, ok := asObject(value)
		if !ok {
			return nil, errors.New("privacy model probe label must be an object")
		}
		if err := allowedKeys(label, []string{"label", "suggested_kind", "suggested_ignore"}, []string{"label", "suggested_kind"}, "privacy model probe label"); err != nil {
			return nil, err
		}
		source, err := validateModelLabel(label["label"], "privacy model probe label name")
		if err != nil {
			return nil, err
		}
		if seen[source] {
			return nil, errors.New("privacy model probe contains duplicate labels")
		}
		seen[source] = true
		suggestedIgnore := false
		if value, ok := label["suggested_ignore"]; ok {
			if suggestedIgnore, ok = asBool(value); !ok {
				return nil, errors.New("privacy model probe label suggested_ignore must be boolean")
			}
		}
		if label["suggested_kind"] == nil {
			requiresLabelMapping = requiresLabelMapping || !suggestedIgnore
		} else {
			if suggestedIgnore {
				return nil, errors.New("privacy model probe label suggestion cannot map and ignore")
			}
			if err := validateCanonicalPrivacyKind(label["suggested_kind"]); err != nil {
				return nil, err
			}
		}
	}
	reported, ok := asBool(o["requires_label_mapping"])
	if !ok {
		return nil, errors.New("privacy model probe requires_label_mapping must be boolean")
	}
	if reported != requiresLabelMapping {
		return nil, errors.New("privacy model probe requires_label_mapping is inconsistent")
	}
	return body, nil
}

func parsePrivacyModelInstallationList(body []byte) (json.RawMessage, error) {
	list, err := decodeValue(body)
	if err != nil {
		return nil, fmt.Errorf("privacy model installation list returned invalid JSON: %v", err)
	}
	o, err := exactKeys(list, []string{"items"}, "privacy model installation list")
	if err != nil {
		return nil, err
	}
	items, ok := asArray(o["items"])
	if !ok {
		return nil, errors.New("privacy model installation items must be an array")
	}
	if len(items) > 100 {
		return nil, errors.New("privacy model installation list contains too many items")
	}
	ids := map[string]bool{}
	for _, item := range items {
		if err := validatePrivacyModelInstallation(item); err != nil {
			return nil, err
		}
		id, _ := stringField(item.(object), "id")
		if ids[id] {
			return nil, errors.New("privacy model installation list contains duplicate IDs")
		}
		ids[id] = true
	}
	return body, nil
}

func parsePrivacyModelInstallation(body []byte) (json.RawMessage, error) {
	installation, err := decodeValue(body)
	if err != nil {
		return nil, fmt.Errorf("privacy model installation returned invalid JSON: %v", err)
	}
	if err := validatePrivacyModelInstallation(installation); err != nil {
		return nil, err
	}
	return body, nil
}

func validatePrivacyModelProbeInput(input any) error {
	o, err := exactKeys(input, []string{"repo_id", "revision"}, "privacy model probe input")
	if err != nil {
		return err
	}
	if err := validateRepoID(o["repo_id"], "privacy model probe repo_id"); err != nil {
		return err
	}
	return validateRequestedRevision(o["revision"], "privacy model probe revision")
}

func validateLocalPrivacyModelProbeInput(input any) error {
	o, err := exactKeys(input, []string{"path"}, "local privacy model probe input")
	if err != nil {
		return err
	}
	path, err := validateString(o["path"], 1, 4096, "local privacy model path")
	if err != nil {
		return err
	}
	if hasControl(path) {
		return errors.New("local privacy model path must contain no control characters")
	}
	if looksLikeURI(path) || !filepath.IsAbs(path) {
		return errors.New("local privacy model path must be an absolute filesystem path")
	}
	return nil
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isASCIIAlnum(c byte) bool { return isASCIILetter(c) || c >= '0' && c <= '9' }

func isLowerHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' }

// looksLikeURI reports a scheme prefix, but not a Windows drive letter.
func looksLikeURI(value string) bool {
	separator := strings.IndexByte(value, ':')
	if separator < 0 {
		return false
	}
	scheme := value[:separator]
	if len(scheme) == 1 && isASCIILetter(scheme[0]) && separator+1 < len(value) &&
		(value[separator+1] == '/' || value[separator+1] == '\\') {
		return false
	}
	if scheme == "" || !isASCIILetter(scheme[0]) {
		return false
	}
	for i := 0; i < len(scheme); i++ {
		c := scheme[i]
		if !isASCIIAlnum(c) && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

func validatePrivacyModelInstallInput(input any) error {
	o, err := exactKeys(input, []string{"repo_id", "revision", "variant_id", "label_mapping"}, "privacy model install input")
	if err != nil {
		return err
	}
	if err := validateRepoID(o["repo_id"], "privacy model install repo_id"); err != nil {
		return err
	}
	if err := validateCommit(o["revision"], "privacy model install revision"); err != nil {
		return err
	}
	if _, err := validateVariantID(o["variant_id"], "privacy model install variant_id"); err != nil {
		return err
	}
	return validateLabelMapping(o["label_mapping"])
}

func validatePrivacyCatalogModel(model any) error {
	o, err := exactKeys(model, []string{
		"id", "name", "summary", "source", "repo_id", "revision", "license",
		"languages", "adapter", "variants", "version", "recommended",
	}, "privacy catalog model")
	if err != nil {
		return err
	}
	id, ok := stringField(o, "id")
	if !ok {
		return errors.New("privacy catalog model id must be a string")
	}
	if err := validatePrivacyCatalogID(id); err != nil {
		return err
	}
	if _, err := validateMetadataString(o["name"], 1, 128, "privacy catalog model name"); err != nil {
		return err
	}
	if _, err := validateMetadataString(o["summary"], 1, 512, "privacy catalog model summary"); err != nil {
		return err
	}
	if source, _ := stringField(o, "source"); source != "official" && source != "community" {
		return errors.New("privacy catalog model source is invalid")
	}
	if err := validateRepoID(o["repo_id"], "privacy catalog model repo_id"); err != nil {
		return err
	}
	if err := validateCommit(o["revision"], "privacy catalog model revision"); err != nil {
		return err
	}
	if _, err := validateMetadataString(o["license"], 1, 64, "privacy catalog model license"); err != nil {
		return err
	}
	if err := validateStringArray(o["languages"], 32, "privacy catalog model languages"); err != nil {
		return err
	}
	if err := validatePrivacyModelAdapter(o["adapter"]); err != nil {
		return err
	}
	if o["version"] != nil && !isReleaseVersion(o["version"]) {
		return errors.New("privacy catalog model version is invalid")
	}
	if !isBool(o["recommended"]) {
		return errors.New("privacy catalog model recommended must be boolean")
	}
	return validatePrivacyModelVariants(o["variants"])
}

func isReleaseVersion(value any) bool {
	version, ok := asString(value)
	if !ok {
		return false
	}
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if len(part) < 1 || len(part) > 6 || (part != "0" && part[0] == '0') {
			return false
		}
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return false
			}
		}
	}
	return true
}

func validatePrivacyModelVariants(value any) error {
	variants, ok := asArray(value)
	if !ok {
		return errors.New("privacy model variants must be an array")
	}
	if len(variants) == 0 || len(variants) > 32 {
		return errors.New("privacy model must contain 1 to 32 variants")
	}
	ids := map[string]bool{}
	for _, variant := range variants {
		o, err := exactKeys(variant, []string{
			"id", "name", "quantization", "bytes_total", "estimated_ram_bytes",
			"recommended", "supported", "unsupported_reason",
		}, "privacy model variant")
		if err != nil {
			return err
		}
		id, err := validateVariantID(o["id"], "privacy model variant id")
		if err != nil {
			return err
		}
		if ids[id] {
			return errors.New("privacy model contains duplicate variant IDs")
		}
		ids[id] = true
		if _, err := validateMetadataString(o["name"], 1, 64, "privacy model variant name"); err != nil {
			return err
		}
		if _, err := validateMetadataString(o["quantization"], 1, 32, "privacy model variant quantization"); err != nil {
			return err
		}
		bytesTotal, err := safeJSONInteger(o["bytes_total"], "privacy model variant bytes_total")
		if err != nil {
			return err
		}
		if _, err := safeJSONInteger(o["estimated_ram_bytes"], "privacy model variant estimated_ram_bytes"); err != nil {
			return err
		}
		supported, ok := asBool(o["supported"])
		if !isBool(o["recommended"]) || !ok {
			return errors.New("privacy model variant flags must be boolean")
		}
		var hasReason bool
		switch reason := o["unsupported_reason"].(type) {
		case nil:
		case string:
			if reason != "cpu_only" && reason != "requires_macos_26" {
				return errors.New("privacy model variant unsupported_reason is invalid")
			}
			hasReason = true
		default:
			return errors.New("privacy model variant unsupported_reason is invalid")
		}
		if supported == hasReason {
			return errors.New("privacy model variant support fields are inconsistent")
		}
		if supported && bytesTotal == 0 {
			return errors.New("supported privacy model variant must have content")
		}
	}
	return nil
}

func validatePrivacyModelInstallation(installation any) error {
	o, err := exactKeys(installation, []string{
		"id", "source", "catalog_id", "catalog_source", "name", "license",
		"languages", "repo_id", "revision", "variant_id", "variant_name",
		"quantization", "adapter", "status", "bytes_downloaded", "bytes_total",
		"estimated_ram_bytes", "error", "label_mapping", "installed_at", "preparation",
	}, "privacy model installation")
	if err != nil {
		return err
	}
	id, ok := stringField(o, "id")
	if !ok {
		return errors.New("privacy model installation id must be a string")
	}
	if err := validatePrivacyModelID(id); err != nil {
		return err
	}
	source, ok := stringField(o, "source")
	if !ok {
		return errors.New("privacy model installation source must be a string")
	}
	repoID, ok := stringField(o, "repo_id")
	if !ok {
		return errors.New("privacy model installation repo_id must be a string")
	}
	catalogID, hasCatalogID := stringField(o, "catalog_id")
	catalogSource, _ := stringField(o, "catalog_source")
	inconsistent := errors.New("privacy model installation catalog provenance is inconsistent")
	switch {
	case source == "catalog" && hasCatalogID && (catalogSource == "official" || catalogSource == "community"):
		if err := validatePrivacyCatalogID(catalogID); err != nil {
			return err
		}
	case (source == "custom" || source == "local") && o["catalog_id"] == nil && o["catalog_source"] == nil:
	default:
		return inconsistent
	}
	localRepoID := false
	if digest, ok := strings.CutPrefix(repoID, "local/model-"); ok && len(digest) == 12 {
		localRepoID = true
		for i := 0; i < len(digest); i++ {
			localRepoID = localRepoID && isLowerHex(digest[i])
		}
	}
	if (source == "local") != localRepoID {
		return errors.New("privacy model installation local provenance is inconsistent")
	}
	if _, err := validateMetadataString(o["name"], 1, 128, "privacy model installation name"); err != nil {
		return err
	}
	if o["license"] != nil {
		if _, err := validateMetadataString(o["license"], 1, 64, "privacy model installation license"); err != nil {
			return err
		}
	}
	if err := validateStringArray(o["languages"], 32, "privacy model installation languages"); err != nil {
		return err
	}
	if err := validateRepoID(o["repo_id"], "privacy model installation repo_id"); err != nil {
		return err
	}
	if err := validateCommit(o["revision"], "privacy model installation revision"); err != nil {
		return err
	}
	if _, err := validateVariantID(o["variant_id"], "privacy model installation variant_id"); err != nil {
		return err
	}
	if _, err := validateMetadataString(o["variant_name"], 1, 64, "privacy model installation variant_name"); err != nil {
		return err
	}
	if _, err := validateMetadataString(o["quantization"], 1, 32, "privacy model installation quantization"); err != nil {
		return err
	}
	if err := validatePrivacyModelAdapter(o["adapter"]); err != nil {
		return err
	}
	status, ok := stringField(o, "status")
	if !ok {
		return errors.New("privacy model installation status must be a string")
	}
	switch status {
	case "downloading", "paused", "ready", "error":
	default:
		return errors.New("privacy model installation status is invalid")
	}
	downloaded, err := safeJSONInteger(o["bytes_downloaded"], "privacy model installation bytes_downloaded")
	if err != nil {
		return err
	}
	total, err := safeJSONInteger(o["bytes_total"], "privacy model installation bytes_total")
	if err != nil {
		return err
	}
	if _, err := safeJSONInteger(o["estimated_ram_bytes"], "privacy model installation estimated_ram_bytes"); err != nil {
		return err
	}
	if downloaded > total {
		return errors.New("privacy model installation resource fields are inconsistent")
	}
	var hasError bool
	switch value := o["error"].(type) {
	case nil:
	case string:
		switch value {
		case "download_failed", "integrity_failed", "incompatible_model":
			hasError = true
		default:
			return errors.New("privacy model installation error is invalid")
		}
	default:
		return errors.New("privacy model installation error is invalid")
	}
	var installed bool
	switch o["installed_at"].(type) {
	case nil:
	case string:
		if err := validateRFC3339(o["installed_at"], "privacy model installation installed_at"); err != nil {
			return err
		}
		installed = true
	default:
		return errors.New("privacy model installation installed_at is invalid")
	}
	consistent := false
	switch status {
	case "downloading", "paused":
		consistent = !hasError && !installed
	case "ready":
		consistent = !hasError && installed && total > 0 && downloaded == total
	case "error":
		consistent = hasError && !installed
	}
	if !consistent {
		return errors.New("privacy model installation lifecycle fields are inconsistent")
	}
	if status != "ready" && o["preparation"] != nil {
		return errors.New("privacy model installation preparation needs a ready installation")
	}
	if err := validatePrivacyModelPreparation(o["preparation"]); err != nil {
		return err
	}
	return validateLabelMapping(o["label_mapping"])
}

// validatePrivacyModelPreparation checks the Core ML model Core prepares
// ahead of requests; fallback names the previous detector that inspects
// requests until it is ready.
func validatePrivacyModelPreparation(value any) error {
	if value == nil {
		return nil
	}
	o, err := exactKeys(value, []string{"state", "fallback"}, "privacy model preparation")
	if err != nil {
		return err
	}
	var hasFallback bool
	switch fallback := o["fallback"].(type) {
	case nil:
	case string:
		if fallback != "local_model" && fallback != "regex" {
			return errors.New("privacy model preparation fallback is invalid")
		}
		hasFallback = true
	default:
		return errors.New("privacy model preparation fallback is invalid")
	}
	switch state, _ := stringField(o, "state"); {
	case state == "preparing" || state == "failed", state == "ready" && !hasFallback:
		return nil
	}
	return errors.New("privacy model preparation state is invalid")
}

func validateLabelMapping(value any) error {
	mapping, ok := asObject(value)
	if !ok {
		return errors.New("privacy model label_mapping must be an object")
	}
	if len(mapping) > 256 {
		return errors.New("privacy model label_mapping contains too many entries")
	}
	for _, label := range sortedKeys(mapping) {
		if !validModelLabel(label) {
			return errors.New("privacy model label_mapping contains an invalid label")
		}
		if kind := mapping[label]; kind != nil {
			if err := validateCanonicalPrivacyKind(kind); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateCanonicalPrivacyKind(value any) error {
	switch kind, _ := asString(value); kind {
	case "email", "phone", "account", "payment_card", "ip_address", "url", "common_secret",
		"private_address", "private_date", "private_person":
		return nil
	}
	return errors.New("privacy model canonical label kind is invalid")
}

func validatePrivacyModelAdapter(value any) error {
	switch adapter, _ := asString(value); adapter {
	case "openai_bioes_viterbi", "hf_token_classification", "pplx_bioes_viterbi", "astrlink_sensitive_guard":
		return nil
	}
	return errors.New("privacy model adapter is invalid")
}

func validateRepoID(value any, label string) error {
	repoID, err := validateString(value, 3, 193, label)
	if err != nil {
		return err
	}
	parts := strings.Split(repoID, "/")
	owner, repository := parts[0], ""
	if len(parts) > 1 {
		repository = parts[1]
	}
	if len(parts) > 2 || strings.Contains(repoID, "..") || !validHuggingFaceName(owner) || !validHuggingFaceName(repository) {
		return fmt.Errorf("%s is invalid", label)
	}
	return nil
}

func validHuggingFaceName(value string) bool {
	if value == "" || len(value) > 96 || !isASCIIAlnum(value[0]) {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !isASCIIAlnum(c) && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func validateCommit(value any, label string) error {
	revision, err := validateString(value, 40, 40, label)
	if err != nil {
		return err
	}
	for i := 0; i < len(revision); i++ {
		if !isLowerHex(revision[i]) {
			return fmt.Errorf("%s must be a lowercase 40-character commit", label)
		}
	}
	return nil
}

func validateRequestedRevision(value any, label string) error {
	revision, err := validateString(value, 1, 128, label)
	if err != nil {
		return err
	}
	valid := true
	for i := 0; i < len(revision); i++ {
		c := revision[i]
		valid = valid && (isASCIIAlnum(c) || i > 0 && (c == '.' || c == '_' || c == '/' || c == '-'))
	}
	if !valid || strings.Contains(revision, "..") || strings.Contains(revision, "//") || strings.HasSuffix(revision, "/") {
		return fmt.Errorf("%s is invalid", label)
	}
	return nil
}

// ValidatePrivacyModelID checks a privacy model installation ID.
func ValidatePrivacyModelID(value string) error { return validatePrivacyModelID(value) }

func validatePrivacyModelID(value string) error {
	suffix, ok := strings.CutPrefix(value, "model_")
	if !ok || len(suffix) != 32 {
		return errors.New("privacy model installation id is invalid")
	}
	for i := 0; i < len(suffix); i++ {
		if !isLowerHex(suffix[i]) {
			return errors.New("privacy model installation id is invalid")
		}
	}
	return nil
}

func validatePrivacyCatalogID(value string) error {
	suffix, ok := strings.CutPrefix(value, "catalog_")
	if !ok || len(suffix) < 3 || len(suffix) > 80 {
		return errors.New("privacy model catalog id is invalid")
	}
	for i := 0; i < len(suffix); i++ {
		c := suffix[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return errors.New("privacy model catalog id is invalid")
		}
	}
	return nil
}

func validateVariantID(value any, label string) (string, error) {
	variant, err := validateString(value, 2, 64, label)
	if err != nil {
		return "", err
	}
	if variant[0] < 'a' || variant[0] > 'z' {
		return "", fmt.Errorf("%s is invalid", label)
	}
	for i := 0; i < len(variant); i++ {
		c := variant[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return "", fmt.Errorf("%s is invalid", label)
		}
	}
	return variant, nil
}

func validateModelLabel(value any, label string) (string, error) {
	text, err := validateString(value, 1, 128, label)
	if err != nil {
		return "", err
	}
	if !validModelLabel(text) {
		return "", fmt.Errorf("%s is invalid", label)
	}
	return text, nil
}

func validModelLabel(value string) bool {
	if value == "" || !isASCIIAlnum(value[0]) {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !isASCIIAlnum(c) && c != '_' && c != '.' && c != '-' {
			return false
		}
	}
	return true
}
