package contract

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// PackageStrategy orders the accounts of one service package. Routing reads
// only cached usage for it; see the endpoint resolver for the exact order.
type PackageStrategy string

const (
	// PackageStrategyAuto uses accounts with plenty left by soonest reset,
	// then the nearly exhausted ones by most remaining. It is the default.
	PackageStrategyAuto PackageStrategy = "auto"
	// PackageStrategySoonestReset uses the account whose binding window
	// resets first, so quota about to reset is spent before it expires.
	PackageStrategySoonestReset PackageStrategy = "soonest_reset"
	// PackageStrategyMostRemaining uses the account with the most left first.
	PackageStrategyMostRemaining PackageStrategy = "most_remaining"
)

func (strategy PackageStrategy) Valid() bool {
	switch strategy {
	case PackageStrategyAuto, PackageStrategySoonestReset, PackageStrategyMostRemaining:
		return true
	default:
		return false
	}
}

// ServicePackageKind tells subscription plan packages from single services.
type ServicePackageKind string

const (
	ServicePackageKindSubscription ServicePackageKind = "subscription"
	ServicePackageKindService      ServicePackageKind = "service"
)

const (
	// UnknownPlanType is the plan key of accounts whose plan is not known yet.
	UnknownPlanType = "unknown"
	// MaxPackageStrategies bounds the strategies kept in routing settings.
	MaxPackageStrategies = 256

	servicePackagePrefix      = "service:"
	subscriptionPackagePrefix = "subscription:"
)

// packageProviderNames are the provider segments of subscription package IDs.
var packageProviderNames = map[SubscriptionProvider]string{
	SubscriptionProviderOpenAICodex:   "codex",
	SubscriptionProviderClaudeCode:    "claude",
	SubscriptionProviderXAIGrok:       "grok",
	SubscriptionProviderAntigravity:   "antigravity",
	SubscriptionProviderGitHubCopilot: "copilot",
	SubscriptionProviderFactoryDroid:  "droid",
}

// ServicePackage is a group of services routing treats as one: every
// subscription account with the same provider and plan, or one other service.
// Membership is derived on every read and never stored.
type ServicePackage struct {
	ID         string               `json:"id"`
	Kind       ServicePackageKind   `json:"kind"`
	Provider   SubscriptionProvider `json:"provider,omitempty"`
	PlanType   string               `json:"plan_type,omitempty"`
	ServiceIDs []ServiceID          `json:"service_ids"`
	Strategy   PackageStrategy      `json:"strategy"`
}

// ServicePackageList is every package in routing order.
type ServicePackageList struct {
	Packages []ServicePackage `json:"packages"`
}

// NormalizePlanType turns a reported plan into its package key. An empty plan
// is unknown; no plan is ever guessed.
func NormalizePlanType(planType string) string {
	key := strings.ToLower(strings.TrimSpace(planType))
	if key == "" {
		return UnknownPlanType
	}
	return key
}

// ServicePackageFor names the package service belongs to, without members or
// strategy. planType is the plan the service's usage last reported, cached or
// stored.
func ServicePackageFor(service Service, planType string) ServicePackage {
	if !service.Kind.IsSubscription() {
		return ServicePackage{ID: servicePackagePrefix + string(service.ID), Kind: ServicePackageKindService}
	}
	provider := service.Kind.SubscriptionProvider()
	name, ok := packageProviderNames[provider]
	if !ok {
		name = string(provider)
	}
	key := NormalizePlanType(planType)
	pkg := ServicePackage{
		ID:   subscriptionPackagePrefix + name + ":" + key,
		Kind: ServicePackageKindSubscription, Provider: provider,
	}
	if key != UnknownPlanType {
		pkg.PlanType = key
	}
	return pkg
}

// ValidServicePackageID reports whether id has the form ServicePackageFor
// produces: service:<service id> or subscription:<provider>:<plan key>.
func ValidServicePackageID(id string) bool {
	if serviceID, ok := strings.CutPrefix(id, servicePackagePrefix); ok {
		return ServiceID(serviceID).Validate() == nil
	}
	rest, ok := strings.CutPrefix(id, subscriptionPackagePrefix)
	if !ok {
		return false
	}
	name, key, ok := strings.Cut(rest, ":")
	if !ok || key == "" || key != NormalizePlanType(key) || utf8.RuneCountInString(key) > maxUsagePlanTypeLength {
		return false
	}
	for _, known := range packageProviderNames {
		if name == known {
			return true
		}
	}
	return false
}

// StrategyForPackage returns how routing orders the accounts of package id.
func (settings RoutingSettings) StrategyForPackage(id string) PackageStrategy {
	if strategy, ok := settings.PackageStrategies[id]; ok {
		return strategy
	}
	return PackageStrategyAuto
}

func validatePackageStrategies(strategies map[string]PackageStrategy) error {
	if len(strategies) > MaxPackageStrategies {
		return fmt.Errorf("package_strategies must contain at most %d packages", MaxPackageStrategies)
	}
	for id, strategy := range strategies {
		if !ValidServicePackageID(id) {
			return fmt.Errorf("package_strategies key %q is not a package ID", id)
		}
		if !strategy.Valid() {
			return fmt.Errorf("package_strategies[%q] must be auto, soonest_reset or most_remaining", id)
		}
	}
	return nil
}
