package subscription

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

var usageCredentialLeakPattern = regexp.MustCompile(
	`(?i)(Bearer\s+[A-Za-z0-9._~+/=-]{12,}|` +
		`(access_token|refresh_token|id_token|device_auth_id|code_verifier|authorization_code)["']?\s*[:=]\s*["']?[A-Za-z0-9._~+/=-]{8,}|` +
		`eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,})`,
)

type rawUsagePayload struct {
	PlanType              json.RawMessage      `json:"plan_type"`
	RateLimit             *rawRateLimitDetails `json:"rate_limit"`
	AdditionalRateLimits  []rawAdditionalLimit `json:"additional_rate_limits"`
	Credits               *rawCredits          `json:"credits"`
	RateLimitResetCredits *rawResetCredits     `json:"rate_limit_reset_credits"`
}

type rawRateLimitDetails struct {
	Allowed         *bool      `json:"allowed"`
	LimitReached    *bool      `json:"limit_reached"`
	PrimaryWindow   *rawWindow `json:"primary_window"`
	SecondaryWindow *rawWindow `json:"secondary_window"`
}

type rawAdditionalLimit struct {
	LimitName      string               `json:"limit_name"`
	MeteredFeature string               `json:"metered_feature"`
	RateLimit      *rawRateLimitDetails `json:"rate_limit"`
}

type rawWindow struct {
	UsedPercent        json.Number `json:"used_percent"`
	LimitWindowSeconds *int64      `json:"limit_window_seconds"`
	ResetAfterSeconds  *int64      `json:"reset_after_seconds"`
	ResetAt            *int64      `json:"reset_at"`
}

type rawCredits struct {
	HasCredits bool            `json:"has_credits"`
	Unlimited  bool            `json:"unlimited"`
	Balance    json.RawMessage `json:"balance"`
}

type rawResetCredits struct {
	AvailableCount *int `json:"available_count"`
}

// DecodeCodexUsage maps the official openai/codex `/wham/usage` payload onto
// the public control snapshot. email, user_id, and account_id are dropped.
func DecodeCodexUsage(body []byte) (contract.SubscriptionUsage, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	var payload rawUsagePayload
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	usage := contract.SubscriptionUsage{
		PlanType: decodePlanType(payload.PlanType),
	}
	if payload.RateLimit != nil {
		usage.Allowed = payload.RateLimit.Allowed
		usage.LimitReached = payload.RateLimit.LimitReached
		usage.Primary = decodeWindow(payload.RateLimit.PrimaryWindow)
		usage.Secondary = decodeWindow(payload.RateLimit.SecondaryWindow)
	}
	if extras := decodeAdditionalLimits(payload.AdditionalRateLimits); len(extras) > 0 {
		usage.AdditionalRateLimits = extras
	}
	if credits := decodeCredits(payload.Credits); credits != nil {
		usage.Credits = credits
	}
	if payload.RateLimitResetCredits != nil && payload.RateLimitResetCredits.AvailableCount != nil {
		count := *payload.RateLimitResetCredits.AvailableCount
		if count >= 0 && count <= 1000 {
			usage.RateLimitResetCredits = &contract.RateLimitResetCredits{AvailableCount: count}
		}
	}
	return usage, nil
}

func decodePlanType(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "@") || usageCredentialLeakPattern.MatchString(value) {
		return ""
	}
	if len([]rune(value)) > 64 {
		return ""
	}
	return value
}

func decodeWindow(raw *rawWindow) *contract.RateLimitWindow {
	if raw == nil {
		return nil
	}
	used, err := raw.UsedPercent.Float64()
	if err != nil {
		return nil
	}
	if used < 0 || used > 1000 {
		return nil
	}
	if raw.LimitWindowSeconds != nil && *raw.LimitWindowSeconds <= 0 {
		return nil
	}
	window := &contract.RateLimitWindow{UsedPercent: used}
	if raw.LimitWindowSeconds != nil && *raw.LimitWindowSeconds > 0 {
		seconds := *raw.LimitWindowSeconds
		window.LimitWindowSeconds = &seconds
	}
	if raw.ResetAfterSeconds != nil && *raw.ResetAfterSeconds >= 0 {
		seconds := *raw.ResetAfterSeconds
		window.ResetAfterSeconds = &seconds
	}
	if raw.ResetAt != nil && *raw.ResetAt > 0 {
		resetAt := time.Unix(*raw.ResetAt, 0).UTC()
		window.ResetAt = &resetAt
	}
	return window
}

func decodeAdditionalLimits(raw []rawAdditionalLimit) []contract.AdditionalRateLimit {
	if len(raw) == 0 {
		return nil
	}
	limits := make([]contract.AdditionalRateLimit, 0, len(raw))
	for _, item := range raw {
		name := strings.TrimSpace(item.LimitName)
		if name == "" {
			name = strings.TrimSpace(item.MeteredFeature)
		}
		if name == "" || strings.Contains(name, "@") || usageCredentialLeakPattern.MatchString(name) {
			continue
		}
		if len([]rune(name)) > 128 {
			name = string([]rune(name)[:128])
		}
		feature := strings.TrimSpace(item.MeteredFeature)
		if feature != "" && (strings.Contains(feature, "@") || usageCredentialLeakPattern.MatchString(feature) || len([]rune(feature)) > 128) {
			feature = ""
		}
		limit := contract.AdditionalRateLimit{LimitName: name, MeteredFeature: feature}
		if item.RateLimit != nil {
			limit.Primary = decodeWindow(item.RateLimit.PrimaryWindow)
			limit.Secondary = decodeWindow(item.RateLimit.SecondaryWindow)
		}
		if limit.Primary == nil && limit.Secondary == nil {
			continue
		}
		limits = append(limits, limit)
		if len(limits) == 16 {
			break
		}
	}
	return limits
}

func decodeCredits(raw *rawCredits) *contract.UsageCredits {
	if raw == nil {
		return nil
	}
	credits := &contract.UsageCredits{HasCredits: raw.HasCredits, Unlimited: raw.Unlimited}
	if balance := decodeBalance(raw.Balance); balance != "" {
		credits.Balance = balance
	}
	return credits
}

func decodeBalance(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		text = strings.TrimSpace(text)
		if text == "" || usageCredentialLeakPattern.MatchString(text) || len([]rune(text)) > 32 {
			return ""
		}
		return text
	}
	var number json.Number
	if json.Unmarshal(trimmed, &number) != nil {
		return ""
	}
	formatted := number.String()
	if formatted == "" || len(formatted) > 32 {
		return ""
	}
	if _, err := strconv.ParseFloat(formatted, 64); err != nil {
		return ""
	}
	return formatted
}

type rawConsumeResponse struct {
	Code         string `json:"code"`
	WindowsReset *int64 `json:"windows_reset"`
}

// DecodeCodexConsumeReset maps the official consume response. The `credit`
// object is dropped.
func DecodeCodexConsumeReset(body []byte) (contract.SubscriptionUsageReset, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return contract.SubscriptionUsageReset{}, fmt.Errorf("%w: invalid payload", ErrResetUnavailable)
	}
	var payload rawConsumeResponse
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return contract.SubscriptionUsageReset{}, fmt.Errorf("%w: invalid payload", ErrResetUnavailable)
	}
	outcome := strings.TrimSpace(payload.Code)
	result := contract.SubscriptionUsageReset{Outcome: outcome}
	if payload.WindowsReset != nil && *payload.WindowsReset >= 0 {
		windows := *payload.WindowsReset
		result.WindowsReset = &windows
	}
	switch outcome {
	case contract.UsageResetOutcomeReset, contract.UsageResetOutcomeAlreadyRedeemed:
		return result, nil
	case contract.UsageResetOutcomeNothingToReset:
		return result, ErrNothingToReset
	case contract.UsageResetOutcomeNoCredit:
		return result, ErrNoResetCredit
	default:
		return contract.SubscriptionUsageReset{}, fmt.Errorf("%w: unexpected code", ErrResetUnavailable)
	}
}
