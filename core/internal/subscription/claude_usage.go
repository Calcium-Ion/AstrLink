package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

func (manager *Manager) claudeUsage(ctx context.Context, tokens accountauth.AccountTokens) (contract.SubscriptionUsage, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(manager.claudeConfig.APIBaseURL, "/")+"/api/oauth/usage", nil)
	if err != nil {
		return contract.SubscriptionUsage{}, ErrUsageUnavailable
	}
	accountauth.ApplyClaudeAPIHeaders(request.Header, tokens)
	response, err := manager.claudeConfig.HTTPClient.Do(request)
	if err != nil {
		return contract.SubscriptionUsage{}, ErrUsageUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: status %d", ErrUsageUnavailable, response.StatusCode)
	}
	var windows map[string]*struct {
		Utilization float64    `json:"utilization"`
		ResetsAt    *time.Time `json:"resets_at"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&windows); err != nil {
		return contract.SubscriptionUsage{}, ErrUsageUnavailable
	}
	window := func(key string, seconds int64) *contract.RateLimitWindow {
		value := windows[key]
		if value == nil {
			return nil
		}
		return &contract.RateLimitWindow{UsedPercent: value.Utilization, LimitWindowSeconds: &seconds, ResetAt: value.ResetsAt}
	}
	usage := contract.SubscriptionUsage{Primary: window("five_hour", 5*3600), Secondary: window("seven_day", 7*24*3600)}
	for _, key := range []string{"seven_day_sonnet", "seven_day_opus", "seven_day_oauth_apps"} {
		if limit := window(key, 7*24*3600); limit != nil {
			usage.AdditionalRateLimits = append(usage.AdditionalRateLimits, contract.AdditionalRateLimit{LimitName: key, Primary: limit})
		}
	}
	if usage.Primary == nil && usage.Secondary == nil && len(usage.AdditionalRateLimits) == 0 {
		return contract.SubscriptionUsage{}, ErrUsageUnavailable
	}
	return usage, nil
}
