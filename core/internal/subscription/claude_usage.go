package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

const (
	claudeSessionWindowSeconds int64 = 5 * 3600
	claudeWeeklyWindowSeconds  int64 = 7 * 24 * 3600
	claudeMonthlyWindowSeconds int64 = 30 * 24 * 3600
	maxClaudeAdditionalLimits        = 16
)

// claudeUsage reads the same snapshot Claude Code shows for /usage:
// GET {api}/api/oauth/usage with the subscription OAuth token. The request
// carries the Claude CLI User-Agent because api.anthropic.com puts unknown
// agents into an aggressively rate-limited bucket that answers 429 for hours.
func (manager *Manager) claudeUsage(ctx context.Context, tokens accountauth.AccountTokens) (contract.SubscriptionUsage, error) {
	endpoint := strings.TrimRight(manager.claudeConfig.APIBaseURL, "/") + "/api/oauth/usage"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: %w", ErrUsageUnavailable, err)
	}
	accountauth.ApplyClaudeAPIHeaders(request.Header, tokens)
	request.Header.Set("User-Agent", accountauth.DefaultClaudeUserAgent)
	request.Header.Set("Accept", "application/json")
	response, err := manager.claudeConfig.HTTPClient.Do(request)
	if err != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: %w", ErrUsageUnavailable, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: %w", ErrUsageUnavailable, err)
	}
	if response.StatusCode != http.StatusOK {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: status %d", ErrUsageUnavailable, response.StatusCode)
	}
	return DecodeClaudeUsage(body)
}

// claudeUsageWindow is one flat window object such as five_hour or seven_day.
// utilization is already a percentage (0-100). Newer payloads add nullable
// dollar fields that are ignored here.
type claudeUsageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

// claudeUsageLimit is one entry of the newer limits[] array. Anthropic moved
// model-scoped weekly caps here and now returns the legacy seven_day_<model>
// keys as null for many accounts.
type claudeUsageLimit struct {
	Kind     string   `json:"kind"`
	Group    string   `json:"group"`
	Percent  *float64 `json:"percent"`
	ResetsAt *string  `json:"resets_at"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

type claudeExtraUsage struct {
	IsEnabled   bool     `json:"is_enabled"`
	Utilization *float64 `json:"utilization"`
}

// claudeLegacyLimitKeys are the flat per-feature weekly windows in the order
// Claude Code lists them. The public limit_name is the display name; the raw
// key is preserved as metered_feature.
var claudeLegacyLimitKeys = []struct{ key, name string }{
	{"seven_day_sonnet", "Sonnet"},
	{"seven_day_opus", "Opus"},
	{"seven_day_oauth_apps", "OAuth apps"},
	{"seven_day_cowork", "Cowork"},
}

// DecodeClaudeUsage maps the Claude Code /api/oauth/usage payload onto the
// public snapshot. Unknown keys, nulls and the newer limits[] array are all
// tolerated: flat five_hour / seven_day windows win, limits[] fills whatever
// they leave empty, and model-scoped weekly caps become additional limits.
// No account, email or token material is retained.
func DecodeClaudeUsage(body []byte) (contract.SubscriptionUsage, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &document); err != nil || document == nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	usage := contract.SubscriptionUsage{
		Primary:   decodeClaudeWindow(document["five_hour"], claudeSessionWindowSeconds),
		Secondary: decodeClaudeWindow(document["seven_day"], claudeWeeklyWindowSeconds),
	}
	seen := map[string]bool{}
	addLimit := func(limit contract.AdditionalRateLimit) {
		key := strings.ToLower(strings.TrimSpace(limit.LimitName))
		if key == "" || seen[key] || len(usage.AdditionalRateLimits) >= maxClaudeAdditionalLimits {
			return
		}
		seen[key] = true
		usage.AdditionalRateLimits = append(usage.AdditionalRateLimits, limit)
	}
	// limits[] is the newer, authoritative source for per-model caps; the flat
	// seven_day_<model> keys only fill in models it does not mention.
	for _, limit := range decodeClaudeLimits(document["limits"]) {
		window := claudeLimitWindow(limit)
		if window == nil {
			continue
		}
		switch limit.Kind {
		case "session":
			if usage.Primary == nil {
				usage.Primary = window
			}
		case "weekly_all":
			if usage.Secondary == nil {
				usage.Secondary = window
			}
		default:
			addLimit(contract.AdditionalRateLimit{
				LimitName:      claudeLimitName(limit),
				MeteredFeature: sanitizeClaudeLabel(limit.Kind),
				Primary:        window,
			})
		}
	}
	for _, legacy := range claudeLegacyLimitKeys {
		if window := decodeClaudeWindow(document[legacy.key], claudeWeeklyWindowSeconds); window != nil {
			addLimit(contract.AdditionalRateLimit{LimitName: legacy.name, MeteredFeature: legacy.key, Primary: window})
		}
	}
	if extra := decodeClaudeExtraUsage(document["extra_usage"]); extra != nil {
		addLimit(contract.AdditionalRateLimit{LimitName: "Extra usage", MeteredFeature: "extra_usage", Secondary: extra})
	}
	if usage.Primary == nil && usage.Secondary == nil && len(usage.AdditionalRateLimits) == 0 {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: no usage windows", ErrUsageUnavailable)
	}
	if usage.Primary != nil || usage.Secondary != nil {
		reached := (usage.Primary != nil && usage.Primary.UsedPercent >= 100) ||
			(usage.Secondary != nil && usage.Secondary.UsedPercent >= 100)
		usage.LimitReached = &reached
	}
	return usage, nil
}

func decodeClaudeWindow(raw json.RawMessage, seconds int64) *contract.RateLimitWindow {
	if !jsonObject(raw) {
		return nil
	}
	var window claudeUsageWindow
	if json.Unmarshal(raw, &window) != nil || window.Utilization == nil {
		return nil
	}
	return newClaudeWindow(*window.Utilization, window.ResetsAt, seconds)
}

func newClaudeWindow(percent float64, resetsAt *string, seconds int64) *contract.RateLimitWindow {
	if math.IsNaN(percent) || math.IsInf(percent, 0) || percent > 1000 {
		return nil
	}
	if percent < 0 {
		percent = 0
	}
	window := &contract.RateLimitWindow{UsedPercent: math.Round(percent*100) / 100}
	if seconds > 0 {
		limit := seconds
		window.LimitWindowSeconds = &limit
	}
	if resetsAt != nil {
		if reset, ok := parseRFC3339(*resetsAt); ok {
			window.ResetAt = &reset
		}
	}
	return window
}

func decodeClaudeLimits(raw json.RawMessage) []claudeUsageLimit {
	if !jsonArray(raw) {
		return nil
	}
	var limits []claudeUsageLimit
	if json.Unmarshal(raw, &limits) != nil {
		return nil
	}
	return limits
}

func claudeLimitWindow(limit claudeUsageLimit) *contract.RateLimitWindow {
	if limit.Percent == nil {
		return nil
	}
	var seconds int64
	switch {
	case limit.Kind == "session" || limit.Group == "session":
		seconds = claudeSessionWindowSeconds
	case strings.HasPrefix(limit.Kind, "weekly") || limit.Group == "weekly":
		seconds = claudeWeeklyWindowSeconds
	case strings.HasPrefix(limit.Kind, "monthly") || limit.Group == "monthly":
		seconds = claudeMonthlyWindowSeconds
	}
	return newClaudeWindow(*limit.Percent, limit.ResetsAt, seconds)
}

// claudeLimitName prefers the model display name Anthropic attaches to scoped
// caps ("Fable", "Sonnet") and otherwise humanizes the kind.
func claudeLimitName(limit claudeUsageLimit) string {
	if limit.Scope != nil && limit.Scope.Model != nil {
		if name := sanitizeClaudeLabel(limit.Scope.Model.DisplayName); name != "" {
			return name
		}
	}
	kind := sanitizeClaudeLabel(limit.Kind)
	if kind == "" {
		return ""
	}
	words := strings.Fields(strings.ReplaceAll(kind, "_", " "))
	if len(words) == 0 {
		return ""
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
}

func decodeClaudeExtraUsage(raw json.RawMessage) *contract.RateLimitWindow {
	if !jsonObject(raw) {
		return nil
	}
	var extra claudeExtraUsage
	if json.Unmarshal(raw, &extra) != nil || !extra.IsEnabled || extra.Utilization == nil {
		return nil
	}
	return newClaudeWindow(*extra.Utilization, nil, 0)
}

func sanitizeClaudeLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "@") || usageCredentialLeakPattern.MatchString(value) {
		return ""
	}
	if runes := []rune(value); len(runes) > 128 {
		value = string(runes[:128])
	}
	return value
}

func jsonObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}
