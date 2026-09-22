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
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

// grokUsage reads the Grok Build credits snapshot the Grok CLI shows in its
// credit bar: GET {proxy}/v1/billing?format=credits. Only the aggregate
// percentage, the current period and the prepaid balance are kept.
func (manager *Manager) grokUsage(ctx context.Context, tokens accountauth.AccountTokens) (contract.SubscriptionUsage, error) {
	endpoint := strings.TrimRight(manager.grokConfig.APIBaseURL, "/") + "/v1/billing?format=credits"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: %w", ErrUsageUnavailable, err)
	}
	accountauth.ApplyGrokAPIHeaders(request.Header, tokens, manager.grokConfig.ModelsClientVersion)
	request.Header.Set("Accept", "application/json")
	response, err := manager.grokConfig.HTTPClient.Do(request)
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
	return DecodeGrokUsage(body, manager.now().UTC())
}

type grokCents struct {
	Val int64 `json:"val"`
}

type grokUsagePeriod struct {
	Type  string `json:"type"`
	Start string `json:"start"`
	End   string `json:"end"`
}

type grokBillingConfig struct {
	CreditUsagePercent *float64         `json:"creditUsagePercent"`
	CurrentPeriod      *grokUsagePeriod `json:"currentPeriod"`
	MonthlyLimit       *grokCents       `json:"monthlyLimit"`
	Used               *grokCents       `json:"used"`
	PrepaidBalance     *grokCents       `json:"prepaidBalance"`
	BillingPeriodStart string           `json:"billingPeriodStart"`
	BillingPeriodEnd   string           `json:"billingPeriodEnd"`
}

type grokBillingResponse struct {
	Config           *grokBillingConfig `json:"config"`
	SubscriptionTier string             `json:"subscriptionTier"`
}

// DecodeGrokUsage maps the credits config onto the public usage snapshot. The
// newer creditUsagePercent/currentPeriod fields win; the deprecated
// monthlyLimit/used pair is the fallback. Nothing personal is retained.
func DecodeGrokUsage(body []byte, now time.Time) (contract.SubscriptionUsage, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	var payload grokBillingResponse
	if err := json.Unmarshal(trimmed, &payload); err != nil || payload.Config == nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	config := payload.Config
	usage := contract.SubscriptionUsage{PlanType: decodePlanType(json.RawMessage(quoteJSON(payload.SubscriptionTier)))}

	var used *float64
	if config.CreditUsagePercent != nil && !math.IsNaN(*config.CreditUsagePercent) && !math.IsInf(*config.CreditUsagePercent, 0) {
		value := *config.CreditUsagePercent
		used = &value
	} else if config.MonthlyLimit != nil && config.MonthlyLimit.Val > 0 && config.Used != nil {
		value := float64(config.Used.Val) / float64(config.MonthlyLimit.Val) * 100
		used = &value
	}
	start, end := "", ""
	periodType := ""
	if config.CurrentPeriod != nil {
		start, end, periodType = config.CurrentPeriod.Start, config.CurrentPeriod.End, config.CurrentPeriod.Type
	}
	if start == "" && end == "" {
		start, end = config.BillingPeriodStart, config.BillingPeriodEnd
	}
	if used != nil {
		value := math.Round(*used*100) / 100
		if value < 0 {
			value = 0
		}
		if value > 1000 {
			value = 1000
		}
		window := &contract.RateLimitWindow{UsedPercent: value}
		if seconds := grokWindowSeconds(periodType, start, end); seconds > 0 {
			window.LimitWindowSeconds = &seconds
		}
		if reset, ok := parseRFC3339(end); ok && reset.After(now) {
			window.ResetAt = &reset
		}
		usage.Primary = window
		limitReached := value >= 100
		usage.LimitReached = &limitReached
	}
	if config.PrepaidBalance != nil && config.PrepaidBalance.Val > 0 {
		usage.Credits = &contract.UsageCredits{
			HasCredits: true,
			Balance:    fmt.Sprintf("$%d.%02d", config.PrepaidBalance.Val/100, config.PrepaidBalance.Val%100),
		}
	}
	if usage.Primary == nil && usage.Credits == nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: no usage windows", ErrUsageUnavailable)
	}
	return usage, nil
}

func grokWindowSeconds(periodType, start, end string) int64 {
	if from, ok := parseRFC3339(start); ok {
		if to, ok := parseRFC3339(end); ok && to.After(from) {
			seconds := int64(to.Sub(from) / time.Second)
			if seconds >= 1 && seconds <= 366*24*3600 {
				return seconds
			}
		}
	}
	switch strings.ToUpper(strings.TrimSpace(periodType)) {
	case "USAGE_PERIOD_TYPE_WEEKLY", "WEEKLY":
		return 7 * 24 * 3600
	case "USAGE_PERIOD_TYPE_MONTHLY", "MONTHLY":
		return 30 * 24 * 3600
	default:
		return 0
	}
}

func parseRFC3339(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func quoteJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}
