package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

// droidCatalog is the model list Factory publishes for Droid
// (docs.factory.com/models), less the Gemini models Factory serves on a
// generateContent wire of its own and the models it has deprecated. Factory
// has no model endpoint, so the list is fixed per release and the user may
// add newer ids by hand.
var droidCatalog = []string{
	// Anthropic
	"claude-fable-5.1", "claude-fable-5",
	"claude-opus-5-5", "claude-opus-5-5-fast", "claude-opus-5", "claude-opus-5-fast",
	"claude-opus-4-8", "claude-opus-4-8-fast", "claude-opus-4-7", "claude-opus-4-6", "claude-opus-4-5-20251101",
	"claude-sonnet-5-5", "claude-sonnet-5", "claude-sonnet-4-6", "claude-sonnet-4-5-20250929",
	"claude-haiku-4-5-20251001",
	// OpenAI
	"gpt-6.1-sol", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna",
	"gpt-5.6-sol", "gpt-5.6-sol-fast", "gpt-5.6-terra", "gpt-5.6-luna",
	"gpt-5.5", "gpt-5.5-fast", "gpt-5.5-pro",
	"gpt-5.4", "gpt-5.4-fast", "gpt-5.4-mini", "gpt-5.4-mini-fast",
	"gpt-5.3-codex", "gpt-5.3-codex-fast", "gpt-5.2",
	// xAI
	"grok-4.7", "grok-4.6", "grok-4.5",
	// Droid Core (open models)
	"inkling", "mistral-large-4", "mistral-medium-3.5",
	"glm-5.3-flash", "glm-5.3", "glm-5.2", "glm-5.2-fast",
	"kimi-k3", "qwen3.8-max", "nemotron-3-ultra", "deepseek-v4.1-flash", "minimax-m3",
}

// DroidModels lists the models a Factory plan offers Droid, sorted.
func DroidModels() []string {
	models := append([]string{}, droidCatalog...)
	sort.Strings(models)
	return models
}

// droidUsage reads the account's rate-limit windows the way Droid's /limits
// does: GET {api}/api/billing/limits with the account's identity.
func (manager *Manager) droidUsage(ctx context.Context, tokens accountauth.AccountTokens) (contract.SubscriptionUsage, error) {
	endpoint := accountauth.DroidAPIBaseURL(manager.droidConfig.APIBaseURL, tokens) + "/api/billing/limits"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: %w", ErrUsageUnavailable, err)
	}
	accountauth.ApplyDroidAPIHeaders(request.Header, tokens)
	request.Header.Set("Accept", "application/json")
	response, err := manager.droidConfig.HTTPClient.Do(request)
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
	return DecodeDroidUsage(body, manager.now().UTC())
}

type droidLimitWindow struct {
	UsedPercent      *float64 `json:"usedPercent"`
	WindowEnd        string   `json:"windowEnd"`
	SecondsRemaining *float64 `json:"secondsRemaining"`
}

type droidLimitTier struct {
	FiveHour *droidLimitWindow `json:"fiveHour"`
	Weekly   *droidLimitWindow `json:"weekly"`
	Monthly  *droidLimitWindow `json:"monthly"`
}

type droidLimitsResponse struct {
	Limits *struct {
		Standard *droidLimitTier `json:"standard"`
		Core     *droidLimitTier `json:"core"`
	} `json:"limits"`
	PlanType               string   `json:"planType"`
	ExtraUsageBalanceCents *float64 `json:"extraUsageBalanceCents"`
}

const (
	droidFiveHourSeconds int64 = 5 * 3600
	droidWeeklySeconds   int64 = 7 * 24 * 3600
	droidMonthlySeconds  int64 = 30 * 24 * 3600
)

// DecodeDroidUsage maps the billing-limits snapshot onto the public usage
// snapshot: the Standard pool's 5-hour and weekly windows are the primary
// and secondary ones, its monthly window and the Droid Core pool's windows
// are additional limits, and the Extra Usage balance is the credit
// remainder. A pool without a live window (Droid Core on a plan without
// one) is left out so it never looks like available quota.
func DecodeDroidUsage(body []byte, now time.Time) (contract.SubscriptionUsage, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	var payload droidLimitsResponse
	if err := json.Unmarshal(trimmed, &payload); err != nil || (payload.Limits == nil && payload.PlanType == "" && payload.ExtraUsageBalanceCents == nil) {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	usage := contract.SubscriptionUsage{PlanType: decodePlanType(json.RawMessage(quoteJSON(payload.PlanType)))}
	var standard, core droidLimitTier
	if payload.Limits != nil {
		if payload.Limits.Standard != nil {
			standard = *payload.Limits.Standard
		}
		if payload.Limits.Core != nil {
			core = *payload.Limits.Core
		}
	}
	usage.Primary = droidWindow(standard.FiveHour, droidFiveHourSeconds, now)
	usage.Secondary = droidWindow(standard.Weekly, droidWeeklySeconds, now)
	if usage.Primary != nil {
		limitReached := usage.Primary.UsedPercent >= 100
		usage.LimitReached = &limitReached
	}
	if monthly := droidWindow(standard.Monthly, droidMonthlySeconds, now); monthly != nil {
		usage.AdditionalRateLimits = append(usage.AdditionalRateLimits, contract.AdditionalRateLimit{
			LimitName: "Monthly", MeteredFeature: "standard_monthly", Primary: monthly,
		})
	}
	coreFiveHour := droidWindow(core.FiveHour, droidFiveHourSeconds, now)
	coreWeekly := droidWindow(core.Weekly, droidWeeklySeconds, now)
	if coreFiveHour != nil || coreWeekly != nil {
		if coreFiveHour == nil {
			coreFiveHour, coreWeekly = coreWeekly, nil
		}
		usage.AdditionalRateLimits = append(usage.AdditionalRateLimits, contract.AdditionalRateLimit{
			LimitName: "Droid Core", MeteredFeature: "droid_core", Primary: coreFiveHour, Secondary: coreWeekly,
		})
	}
	if monthly := droidWindow(core.Monthly, droidMonthlySeconds, now); monthly != nil {
		usage.AdditionalRateLimits = append(usage.AdditionalRateLimits, contract.AdditionalRateLimit{
			LimitName: "Droid Core Monthly", MeteredFeature: "droid_core_monthly", Primary: monthly,
		})
	}
	if payload.ExtraUsageBalanceCents != nil && !math.IsNaN(*payload.ExtraUsageBalanceCents) && !math.IsInf(*payload.ExtraUsageBalanceCents, 0) {
		if cents := int64(math.Round(*payload.ExtraUsageBalanceCents)); cents > 0 {
			usage.Credits = &contract.UsageCredits{
				HasCredits: true,
				Balance:    fmt.Sprintf("$%d.%02d", cents/100, cents%100),
			}
		}
	}
	if usage.Primary == nil && usage.Secondary == nil && len(usage.AdditionalRateLimits) == 0 && usage.Credits == nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: no usage windows", ErrUsageUnavailable)
	}
	return usage, nil
}

// droidWindow maps one bucket onto a rate-limit window. The absolute window
// end wins; the relative seconds are the fallback. A bucket whose window is
// over and that reports no remaining seconds has no live window.
func droidWindow(bucket *droidLimitWindow, seconds int64, now time.Time) *contract.RateLimitWindow {
	if bucket == nil || bucket.UsedPercent == nil || math.IsNaN(*bucket.UsedPercent) || math.IsInf(*bucket.UsedPercent, 0) {
		return nil
	}
	used := math.Round(*bucket.UsedPercent*100) / 100
	used = math.Min(math.Max(used, 0), 1000)
	window := &contract.RateLimitWindow{UsedPercent: used}
	window.LimitWindowSeconds = &seconds
	if reset, ok := parseRFC3339(strings.TrimSpace(bucket.WindowEnd)); ok && reset.After(now) {
		window.ResetAt = &reset
		return window
	}
	if bucket.SecondsRemaining != nil && !math.IsNaN(*bucket.SecondsRemaining) && !math.IsInf(*bucket.SecondsRemaining, 0) && *bucket.SecondsRemaining >= 0 {
		remaining := int64(math.Round(*bucket.SecondsRemaining))
		if remaining > 366*24*3600 {
			remaining = 366 * 24 * 3600
		}
		window.ResetAfterSeconds = &remaining
		reset := now.Add(time.Duration(remaining) * time.Second)
		window.ResetAt = &reset
		return window
	}
	return nil
}
