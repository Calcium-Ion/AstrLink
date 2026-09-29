package subscription

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/providerapi"
)

func (manager *Manager) antigravityCatalog(ctx context.Context, tokens accountauth.AccountTokens) (providerapi.AntigravityCatalog, error) {
	var catalog providerapi.AntigravityCatalog
	if tokens.ProjectID == "" {
		return catalog, fmt.Errorf("Antigravity project is missing; sign in again")
	}
	err := accountauth.AntigravityCall(ctx, manager.antigravityConfig.HTTPClient, manager.antigravityConfig.APIBaseURL, "fetchAvailableModels", tokens, map[string]string{"project": tokens.ProjectID}, &catalog)
	if err == nil && catalog.Models == nil {
		err = fmt.Errorf("Antigravity catalog omitted models")
	}
	return catalog, err
}

func (manager *Manager) AntigravityModels(ctx context.Context, tokens accountauth.AccountTokens) ([]string, error) {
	catalog, err := manager.antigravityCatalog(ctx, tokens)
	if err != nil {
		return nil, err
	}
	return catalog.IDs(), nil
}

func (manager *Manager) antigravityUsage(ctx context.Context, tokens accountauth.AccountTokens) (contract.SubscriptionUsage, error) {
	catalog, err := manager.antigravityCatalog(ctx, tokens)
	if err != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: %w", ErrUsageUnavailable, err)
	}
	usage := contract.SubscriptionUsage{PlanType: tokens.PlanType}
	for _, id := range catalog.IDs() {
		quota := catalog.Models[id].QuotaInfo
		if quota == nil {
			continue
		}
		remaining := quota.RemainingFraction
		if math.IsNaN(remaining) || math.IsInf(remaining, 0) || remaining < 0 || remaining > 1 {
			return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid quota fraction", ErrUsageUnavailable)
		}
		window := &contract.RateLimitWindow{UsedPercent: math.Round((1-remaining)*10000) / 100}
		if reset, err := time.Parse(time.RFC3339Nano, quota.ResetTime); err == nil {
			reset = reset.UTC()
			window.ResetAt = &reset
		}
		usage.AdditionalRateLimits = append(usage.AdditionalRateLimits, contract.AdditionalRateLimit{LimitName: id, MeteredFeature: id, Primary: window})
	}
	if len(usage.AdditionalRateLimits) == 0 {
		return usage, fmt.Errorf("%w: no model quota available", ErrUsageUnavailable)
	}
	return usage, nil
}
