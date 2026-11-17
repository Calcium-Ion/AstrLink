package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const (
	maxModelRedirects       = 200
	maxRedirectModelChars   = 256
	astrlinkAutoModelID     = "astrlink/auto"
	maxSystemPromptChars    = 20000
	maxProxyURLBytes        = 2048
	maxProxyCredentialBytes = 255
)

// validateProxy checks a service proxy. input is a proxy the frontend sends,
// with its credential; otherwise it is one Core returned for serviceID,
// whose credential is only a reference.
func validateProxy(value any, serviceID string, input bool) error {
	if input && value == nil {
		return nil
	}
	o, ok := asObject(value)
	if !ok {
		return errors.New("invalid service proxy")
	}
	credentialKey := "credential_ref"
	if input {
		credentialKey = "credential"
	}
	for key := range o {
		if key != "mode" && key != "url" && key != credentialKey {
			return errors.New("unexpected proxy field")
		}
	}
	mode, ok := stringField(o, "mode")
	if !ok {
		return errors.New("proxy mode is required")
	}
	if mode != "inherit" && mode != "direct" && mode != "custom" {
		return errors.New("invalid proxy mode")
	}
	credential, hasCredential := o[credentialKey]
	if mode != "custom" {
		if _, ok := o["url"]; ok || (hasCredential && (!input || credential != nil)) {
			return errors.New("only custom proxy may specify an address or authentication")
		}
		return nil
	}
	raw, ok := stringField(o, "url")
	if !ok {
		return errors.New("proxy URL is required")
	}
	if !validProxyURL(raw) {
		return errors.New("invalid proxy URL")
	}
	if !hasCredential {
		return nil
	}
	if !input {
		if serviceID == "" {
			return errors.New("missing service id")
		}
		if reference, _ := asString(credential); reference != "local://service-proxy/"+serviceID {
			return errors.New("invalid proxy credential reference")
		}
		return nil
	}
	if credential == nil {
		return nil
	}
	auth, ok := asObject(credential)
	_, hasUsername := auth["username"]
	_, hasPassword := auth["password"]
	if !ok || len(auth) != 2 || !hasUsername || !hasPassword {
		return errors.New("invalid proxy authentication")
	}
	username, ok := stringField(auth, "username")
	if !ok {
		return errors.New("invalid proxy username")
	}
	password, ok := stringField(auth, "password")
	if !ok {
		return errors.New("invalid proxy password")
	}
	if username == "" || len(username) > maxProxyCredentialBytes || len(password) > maxProxyCredentialBytes ||
		strings.ContainsAny(username, ":\r\n\x00") || strings.ContainsAny(password, "\r\n\x00") {
		return errors.New("invalid proxy authentication")
	}
	return nil
}

func validProxyURL(raw string) bool {
	if len(raw) > maxProxyURLBytes || strings.TrimSpace(raw) != raw || strings.HasSuffix(raw, ":") ||
		hasControl(raw) || strings.ContainsAny(raw, "@?#") {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	switch parsed.Scheme {
	case "http", "https", "socks5":
	default:
		return false
	}
	if parsed.Hostname() == "" || (parsed.Path != "" && parsed.Path != "/") {
		return false
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return false
		}
	}
	return true
}

var failureActions = []string{"stop", "retry", "failover", "retry_and_failover"}

// validateFailurePolicy mirrors the desktop's strict reading of a failure
// policy: every field known, actions from the list, counts and delays in
// range.
func validateFailurePolicy(value any) error {
	o, ok := asObject(value)
	if !ok {
		return errors.New("invalid failure policy: expected an object")
	}
	for _, key := range []string{"response_start_timeout_seconds", "thinking_signature_recovery", "openai_reasoning_recovery", "openai_function_output_recovery"} {
		if v, ok := o[key]; ok && v == nil {
			return fmt.Errorf("%s must not be null", key)
		}
	}
	var policy struct {
		MaxRetries                   *uint32            `json:"max_retries"`
		InitialDelayMS               *uint32            `json:"initial_delay_ms"`
		MaxDelayMS                   *uint32            `json:"max_delay_ms"`
		ResponseStartTimeoutSeconds  *uint32            `json:"response_start_timeout_seconds"`
		ThinkingSignatureRecovery    *bool              `json:"thinking_signature_recovery"`
		OpenAIReasoningRecovery      *bool              `json:"openai_reasoning_recovery"`
		OpenAIFunctionOutputRecovery *bool              `json:"openai_function_output_recovery"`
		NetworkError                 *string            `json:"network_error"`
		ResponseTimeout              *string            `json:"response_timeout"`
		HTTPStatus                   *map[string]string `json:"http_status"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encodeValue(o)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return fmt.Errorf("invalid failure policy: %v", err)
	}
	for name, present := range map[string]bool{
		"max_retries": policy.MaxRetries != nil, "initial_delay_ms": policy.InitialDelayMS != nil,
		"max_delay_ms": policy.MaxDelayMS != nil, "network_error": policy.NetworkError != nil,
		"response_timeout": policy.ResponseTimeout != nil, "http_status": policy.HTTPStatus != nil,
	} {
		if !present {
			return fmt.Errorf("invalid failure policy: missing field `%s`", name)
		}
	}
	actions := []string{*policy.NetworkError, *policy.ResponseTimeout}
	for _, action := range *policy.HTTPStatus {
		actions = append(actions, action)
	}
	for _, action := range actions {
		if !slices.Contains(failureActions, action) {
			return fmt.Errorf("invalid failure policy: unknown variant `%s`", action)
		}
	}
	if *policy.MaxRetries > 5 || *policy.InitialDelayMS > 60000 || *policy.MaxDelayMS < *policy.InitialDelayMS ||
		*policy.MaxDelayMS > 60000 || (policy.ResponseStartTimeoutSeconds != nil && *policy.ResponseStartTimeoutSeconds > 86400) {
		return errors.New("failure policy count or timeout is out of range")
	}
	for code := range *policy.HTTPStatus {
		status, err := strconv.Atoi(code)
		if len(code) != 3 || err != nil || status < 400 || status > 599 {
			return errors.New("failure policy HTTP status must be 400 through 599")
		}
	}
	return nil
}

// recoveryPathID checks a recovery path or service ID: 3 to 96 lowercase
// letters, digits, underscores and hyphens, starting with a letter.
func recoveryPathID(value any) error {
	text, ok := asString(value)
	if !ok || runeCount(text) > 96 {
		return errors.New("invalid path text")
	}
	if len(text) < 3 || text[0] < 'a' || text[0] > 'z' || strings.Trim(text, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return errors.New("invalid path ID")
	}
	return nil
}

func recoveryPathProtocol(value any) error {
	text, ok := asString(value)
	if !ok || runeCount(text) > 96 {
		return errors.New("invalid path text")
	}
	if len(text) < 3 || text[0] < 'a' || text[0] > 'z' || strings.Trim(text, "abcdefghijklmnopqrstuvwxyz0123456789_.-") != "" {
		return errors.New("invalid path protocol")
	}
	return nil
}

var reasoningLevels = []string{"off", "low", "medium", "high", "max"}

// validateRoutingSettings checks routing settings Core returned, or with
// patch a partial update the frontend sends.
func validateRoutingSettings(value any, patch bool) error {
	o, ok := asObject(value)
	if !ok {
		return errors.New("routing settings must be an object")
	}
	if len(o) == 0 {
		return errors.New("missing routing settings fields")
	}
	if !patch {
		for _, key := range []string{"default_failure_policy", "allow_unmatched_failover", "strategy", "max_attempts"} {
			if _, ok := o[key]; !ok {
				return errors.New("missing routing settings fields")
			}
		}
	}
	for _, key := range sortedKeys(o) {
		value := o[key]
		var err error
		switch key {
		case "default_recovery_paths":
			paths, ok := asObject(value)
			if !ok {
				return errors.New("invalid default paths")
			}
			for _, protocol := range sortedKeys(paths) {
				if err := recoveryPathProtocol(protocol); err != nil {
					return err
				}
				if err := recoveryPathID(paths[protocol]); err != nil {
					return err
				}
			}
		case "channel_stickiness":
			settings, ok := asObject(value)
			_, enabled := asBool(settings["enabled"])
			ttl, ttlOK := asU64(settings["ttl_seconds"])
			if !ok || len(settings) != 2 || !enabled || !ttlOK || ttl < 60 || ttl > 86400 {
				return errors.New("invalid API provider reuse settings")
			}
		case "builtin_tools":
			err = validateBuiltinTools(value)
		case "image_compression":
			err = validateImageCompression(value)
		case "model_redirects":
			err = validateModelRedirects(value)
		case "default_failure_policy":
			err = validateFailurePolicy(value)
		case "allow_unmatched_failover", "codex_identity_enforcement", "claude_identity_enforcement",
			"grok_identity_enforcement", "official_client_passthrough", "subscription_risk_protection",
			"codex_request_normalization", "claude_request_normalization", "subscription_session_isolation",
			"claude_identity_auto_learn", "codex_identity_auto_learn", "grok_identity_auto_learn", "system_prompt_enabled":
			if _, ok := asBool(value); !ok {
				return errors.New("invalid routing settings field")
			}
		case "claude_identity_version", "codex_identity_version", "grok_identity_version":
			err = validateIdentityVersion(value)
		case "system_prompt_prefix", "system_prompt_suffix":
			if text, ok := asString(value); !ok || runeCount(text) > maxSystemPromptChars {
				return errors.New("global prompt text must be at most 20000 characters")
			}
		case "reasoning_mode":
			if text, _ := asString(value); text != "passthrough" && text != "fixed" && text != "adaptive" {
				return errors.New("invalid reasoning mode")
			}
		case "reasoning_level", "adaptive_min", "adaptive_max":
			if text, ok := asString(value); !ok || !slices.Contains(reasoningLevels, text) {
				return errors.New("invalid reasoning level")
			}
		case "adaptive_preset":
			if text, _ := asString(value); text != "everyday" && text != "research" && text != "custom" {
				return errors.New("invalid adaptive preset")
			}
		case "strategy":
			if text, _ := asString(value); text != "retry_first" && text != "failover_first" && text != "failover_only" {
				return errors.New("invalid failure handling order")
			}
		case "max_attempts":
			if count, ok := asU64(value); !ok || count < 1 || count > 20 {
				return errors.New("max_attempts must be 1 through 20")
			}
		default:
			return errors.New("invalid routing settings field")
		}
		if err != nil {
			return err
		}
	}
	// A patch with one end of the custom range is checked by Core against
	// the stored other end.
	low, lowOK := stringField(o, "adaptive_min")
	high, highOK := stringField(o, "adaptive_max")
	if lowOK && highOK && slices.Index(reasoningLevels, low) > slices.Index(reasoningLevels, high) {
		return errors.New("the adaptive range must start at or below its upper end")
	}
	return nil
}

// validateIdentityVersion checks the shape of a client version override;
// an empty string clears it. Core applies the full version rules.
func validateIdentityVersion(value any) error {
	version, ok := asString(value)
	if !ok {
		return errors.New("invalid client identity version")
	}
	if version == "" {
		return nil
	}
	if len(version) > 64 || version[0] < '0' || version[0] > '9' ||
		strings.Trim(version, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-+") != "" {
		return errors.New("invalid client identity version")
	}
	return nil
}

// validateImageCompression mirrors contract.ImageCompression: every field
// is required.
func validateImageCompression(value any) error {
	settings, ok := asObject(value)
	_, enabled := asBool(settings["enabled"])
	mode, _ := stringField(settings, "mode")
	quality, qualityOK := asU64(settings["quality"])
	if !ok || len(settings) != 3 || !enabled || (mode != "lossless" && mode != "lossy") || !qualityOK || quality < 1 || quality > 100 {
		return errors.New("invalid image compression settings")
	}
	return nil
}

// validateModelRedirects mirrors contract.ValidateModelRedirects: sources
// are unique and no target is another rule's source, so every redirect is
// a single hop.
func validateModelRedirects(value any) error {
	rules, ok := asArray(value)
	if !ok || len(rules) > maxModelRedirects {
		return errors.New("model_redirects must be an array of at most 200 rules")
	}
	sources := map[string]bool{}
	var targets []string
	for _, rule := range rules {
		fields, ok := asObject(rule)
		if !ok {
			return errors.New("model redirect must be an object")
		}
		if _, enabled := asBool(fields["enabled"]); len(fields) != 3 || !enabled {
			return errors.New("invalid model redirect fields")
		}
		from, err := redirectModel(fields["from"])
		if err != nil {
			return err
		}
		to, err := redirectModel(fields["to"])
		if err != nil {
			return err
		}
		if from == to || to == astrlinkAutoModelID {
			return errors.New("invalid model redirect target")
		}
		if sources[from] {
			return errors.New("model redirect source is duplicated")
		}
		sources[from] = true
		targets = append(targets, to)
	}
	for _, to := range targets {
		if sources[to] {
			return errors.New("model redirect target must not be another rule's source")
		}
	}
	return nil
}

func redirectModel(value any) (string, error) {
	model, ok := asString(value)
	n := runeCount(model)
	if !ok || n < 1 || n > maxRedirectModelChars || strings.TrimSpace(model) != model {
		return "", errors.New("invalid model redirect model")
	}
	for _, r := range model {
		if r < ' ' && r != '\t' {
			return "", errors.New("invalid model redirect model")
		}
	}
	return model, nil
}

func validateBuiltinTools(value any) error {
	tools, ok := asObject(value)
	_, search := tools["web_search"]
	_, images := tools["image_generation"]
	if !ok {
		return errors.New("invalid builtin tools")
	}
	if len(tools) != 2 || !search || !images {
		return errors.New("invalid builtin tool fields")
	}
	for _, kind := range sortedKeys(tools) {
		if err := validateBuiltinTool(kind, tools[kind]); err != nil {
			return err
		}
	}
	return nil
}

func validateBuiltinTool(kind string, value any) error {
	if kind != "web_search" && kind != "image_generation" {
		return errors.New("invalid builtin tool kind")
	}
	config, ok := asObject(value)
	if !ok {
		return errors.New("invalid builtin tool")
	}
	enabled, ok := asBool(config["enabled"])
	if !ok {
		return errors.New("enabled must be a boolean")
	}
	for key, value := range config {
		if key == "enabled" {
			continue
		}
		if _, ok := asString(value); !ok || (key != "backend" && key != "service_id" && key != "model" && key != "base_url") {
			return errors.New("invalid builtin tool field")
		}
	}
	text := func(key string) string {
		value, _ := stringField(config, key)
		return value
	}
	if _, ok := config["backend"]; !ok {
		return errors.New("backend is required")
	}
	if text("service_id") != "" {
		if err := recoveryPathID(text("service_id")); err != nil {
			return err
		}
	}
	backend := text("backend")
	switch {
	case !slices.Contains([]string{"", "upstream", "external", "service_images", "service_search"}, backend),
		backend == "service_images" && kind != "image_generation",
		backend == "service_search" && kind != "web_search":
		return errors.New("invalid builtin backend")
	}
	if runeCount(text("model")) > 256 || hasControl(text("model")) {
		return errors.New("invalid tool model")
	}
	if baseURL := text("base_url"); baseURL != "" {
		parsed, err := url.Parse(baseURL)
		if err != nil || len(baseURL) > 2048 || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.HasSuffix(baseURL, "?") {
			return errors.New("invalid tool API URL")
		}
	}
	if enabled && (backend == "" ||
		((backend == "upstream" || backend == "service_images" || backend == "service_search") && (text("service_id") == "" || text("model") == "")) ||
		(backend == "external" && (text("base_url") == "" || (kind == "image_generation" && text("model") == "")))) {
		return errors.New("tool configuration is incomplete")
	}
	return nil
}
