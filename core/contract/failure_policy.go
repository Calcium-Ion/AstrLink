package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// FailureAction describes recovery before a response reaches the client.
type FailureAction string

const (
	FailureStop             FailureAction = "stop"
	FailureRetry            FailureAction = "retry"
	FailureFailover         FailureAction = "failover"
	FailureRetryAndFailover FailureAction = "retry_and_failover"
)

func (action FailureAction) Valid() bool {
	return action == FailureStop || action == FailureRetry || action == FailureFailover || action == FailureRetryAndFailover
}

func (action FailureAction) AllowsRetry() bool {
	return action == FailureRetry || action == FailureRetryAndFailover
}
func (action FailureAction) AllowsFailover() bool {
	return action == FailureFailover || action == FailureRetryAndFailover
}

// FailurePolicy is a complete policy, not a merge patch. A nil policy inherits
// the service policy (on a route), or the persisted global defaults (on a service).
type FailurePolicy struct {
	MaxRetries                   int                      `json:"max_retries"`
	InitialDelayMS               int                      `json:"initial_delay_ms"`
	MaxDelayMS                   int                      `json:"max_delay_ms"`
	ResponseStartTimeoutSeconds  *int                     `json:"response_start_timeout_seconds,omitempty"`
	ThinkingSignatureRecovery    *bool                    `json:"thinking_signature_recovery,omitempty"`
	OpenAIReasoningRecovery      *bool                    `json:"openai_reasoning_recovery,omitempty"`
	OpenAIFunctionOutputRecovery *bool                    `json:"openai_function_output_recovery,omitempty"`
	NetworkError                 FailureAction            `json:"network_error"`
	ResponseTimeout              FailureAction            `json:"response_timeout"`
	HTTPStatus                   map[string]FailureAction `json:"http_status"`
}

func DefaultFailurePolicy() FailurePolicy {
	return FailurePolicy{
		MaxRetries: 1, InitialDelayMS: 500, MaxDelayMS: 5000,
		NetworkError: FailureRetryAndFailover, ResponseTimeout: FailureRetryAndFailover,
		HTTPStatus: map[string]FailureAction{
			"408": FailureRetryAndFailover, "429": FailureRetryAndFailover,
			"500": FailureRetryAndFailover, "502": FailureRetryAndFailover,
			"503": FailureRetryAndFailover, "504": FailureRetryAndFailover,
			"529": FailureRetryAndFailover, "401": FailureFailover, "403": FailureFailover,
		},
	}
}

func (policy FailurePolicy) Validate() error {
	if policy.MaxRetries < 0 || policy.MaxRetries > 5 {
		return fmt.Errorf("max_retries must be between 0 and 5")
	}
	if policy.InitialDelayMS < 0 || policy.InitialDelayMS > 60000 {
		return fmt.Errorf("initial_delay_ms must be between 0 and 60000")
	}
	if policy.MaxDelayMS < policy.InitialDelayMS || policy.MaxDelayMS > 60000 {
		return fmt.Errorf("max_delay_ms must be between initial_delay_ms and 60000")
	}
	if n := policy.ResponseStartTimeoutSeconds; n != nil && (*n < 0 || *n > 86400) {
		return fmt.Errorf("response_start_timeout_seconds must be between 0 and 86400")
	}
	if !policy.NetworkError.Valid() || !policy.ResponseTimeout.Valid() {
		return fmt.Errorf("network_error and response_timeout must be valid failure actions")
	}
	if policy.HTTPStatus == nil {
		return fmt.Errorf("http_status must be an object")
	}
	for code, action := range policy.HTTPStatus {
		n, err := strconv.Atoi(code)
		if err != nil || n < 400 || n > 599 || strconv.Itoa(n) != code || !action.Valid() {
			return fmt.Errorf("invalid http_status rule %q", code)
		}
	}
	return nil
}

func (policy FailurePolicy) ActionForStatus(status int) FailureAction {
	if action, ok := policy.HTTPStatus[strconv.Itoa(status)]; ok {
		return action
	}
	return FailureStop
}

// Omission enables targeted repair for older policy documents. Repair switches
// are independent of ordinary HTTP error rules, retry quotas and backoff.
func (policy FailurePolicy) AllowsThinkingSignatureRecovery() bool {
	return policy.ThinkingSignatureRecovery == nil || *policy.ThinkingSignatureRecovery
}

func (policy FailurePolicy) AllowsOpenAIReasoningRecovery() bool {
	return policy.OpenAIReasoningRecovery == nil || *policy.OpenAIReasoningRecovery
}

// Omission leaves function-output ciphertext intact. True enables one same-target
// repair of encrypted function / custom-tool outputs, independently of error rules.
func (policy FailurePolicy) AllowsOpenAIFunctionOutputRecovery() bool {
	return policy.OpenAIFunctionOutputRecovery != nil && *policy.OpenAIFunctionOutputRecovery
}

type FailoverStrategy string

const (
	FailoverOnly  FailoverStrategy = "failover_only"
	RetryFirst    FailoverStrategy = "retry_first"
	FailoverFirst FailoverStrategy = "failover_first"
)

type FailoverPolicy struct {
	Enabled     bool             `json:"enabled"`
	Strategy    FailoverStrategy `json:"strategy"`
	MaxAttempts int              `json:"max_attempts"`
}

func DefaultFailoverPolicy() FailoverPolicy {
	return FailoverPolicy{Enabled: true, Strategy: RetryFirst, MaxAttempts: 6}
}

func (policy FailoverPolicy) Validate() error {
	if policy.Strategy != RetryFirst && policy.Strategy != FailoverFirst && policy.Strategy != FailoverOnly {
		return fmt.Errorf("strategy must be retry_first, failover_first or failover_only")
	}
	if policy.MaxAttempts < 1 || policy.MaxAttempts > 20 {
		return fmt.Errorf("max_attempts must be between 1 and 20")
	}
	return nil
}

type RoutingSettings struct {
	CodexIdentityEnforcement  bool `json:"codex_identity_enforcement"`
	ClaudeIdentityEnforcement bool `json:"claude_identity_enforcement"`
	GrokIdentityEnforcement   bool `json:"grok_identity_enforcement"`
	// ModelRedirects is always emitted; nil documents load as an empty table.
	ModelRedirects         []ModelRedirect               `json:"model_redirects"`
	ChannelStickiness      *ChannelStickiness            `json:"channel_stickiness,omitempty"`
	DefaultRecoveryPaths   map[ProtocolID]RecoveryPathID `json:"default_recovery_paths,omitempty"`
	DefaultFailurePolicy   FailurePolicy                 `json:"default_failure_policy"`
	AllowUnmatchedFailover bool                          `json:"allow_unmatched_failover"`
	Strategy               FailoverStrategy              `json:"strategy"`
	MaxAttempts            int                           `json:"max_attempts"`
}

func DefaultRoutingSettings() RoutingSettings {
	return RoutingSettings{
		CodexIdentityEnforcement:  true,
		ClaudeIdentityEnforcement: true,
		GrokIdentityEnforcement:   true,
		ModelRedirects:            []ModelRedirect{},
		ChannelStickiness:         &ChannelStickiness{Enabled: true, TTLSeconds: 3600},
		DefaultFailurePolicy:      DefaultFailurePolicy(),
		AllowUnmatchedFailover:    true,
		Strategy:                  FailoverOnly,
		MaxAttempts:               6,
	}
}
func (settings RoutingSettings) FailoverPolicy() FailoverPolicy {
	return FailoverPolicy{Enabled: settings.AllowUnmatchedFailover, Strategy: settings.Strategy, MaxAttempts: settings.MaxAttempts}
}
func (settings RoutingSettings) Validate() error {
	if err := ValidateModelRedirects(settings.ModelRedirects); err != nil {
		return err
	}
	if settings.ChannelStickiness != nil {
		if err := settings.ChannelStickiness.Validate(); err != nil {
			return err
		}
	}
	for protocol, id := range settings.DefaultRecoveryPaths {
		if err := protocol.Validate(); err != nil {
			return err
		}
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if err := settings.DefaultFailurePolicy.Validate(); err != nil {
		return err
	}
	return settings.FailoverPolicy().Validate()
}

// Complete policy documents are replacement values across Go, IPC and desktop.
// Reject omitted, null and unknown fields rather than silently merging maps.
func decodePolicy(data []byte, target any, required, optional []string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range required {
		value, exists := fields[key]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s is required", key)
		}
	}
	for _, key := range optional {
		if value, exists := fields[key]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s must not be null", key)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
func (policy *FailurePolicy) UnmarshalJSON(data []byte) error {
	type document FailurePolicy
	var value document
	if err := decodePolicy(data, &value, []string{"max_retries", "initial_delay_ms", "max_delay_ms", "network_error", "response_timeout", "http_status"}, []string{"response_start_timeout_seconds", "thinking_signature_recovery", "openai_reasoning_recovery", "openai_function_output_recovery"}); err != nil {
		return err
	}
	if err := FailurePolicy(value).Validate(); err != nil {
		return err
	}
	*policy = FailurePolicy(value)
	return nil
}
func (policy *FailoverPolicy) UnmarshalJSON(data []byte) error {
	type document FailoverPolicy
	var value document
	if err := decodePolicy(data, &value, []string{"enabled", "strategy", "max_attempts"}, nil); err != nil {
		return err
	}
	if err := FailoverPolicy(value).Validate(); err != nil {
		return err
	}
	*policy = FailoverPolicy(value)
	return nil
}
