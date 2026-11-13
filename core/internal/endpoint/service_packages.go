package endpoint

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// autoLowRemainingPercent splits the auto strategy: accounts with more than
// this percent of their binding window left are used soonest reset first,
// and the ones at or below it follow, most remaining first, so a nearly
// exhausted account is not drained just because it resets soon.
const autoLowRemainingPercent = 10.0

// UsageCache reads the last usage snapshot of a subscription service without
// contacting its provider.
type UsageCache interface {
	CachedUsage(contract.ServiceID) (contract.SubscriptionUsage, bool)
}

// WithUsageCache supplies the plans and quotas service packages are built
// from. Without it accounts are grouped by the plan stored from their last
// usage read, and every quota is unknown.
func (resolver *StoreResolver) WithUsageCache(cache UsageCache) *StoreResolver {
	if resolver != nil && cache != nil {
		resolver.usage = cache
	}
	return resolver
}

// ServicePackages lists every configured service, disabled ones included, as
// packages in routing order; the services of a package keep the stored order.
func (resolver *StoreResolver) ServicePackages(ctx context.Context) ([]contract.ServicePackage, error) {
	if resolver == nil || resolver.reader == nil {
		return nil, ErrUnavailable
	}
	settings := contract.DefaultRoutingSettings()
	if resolver.routingSettings != nil {
		var err error
		settings, err = resolver.routingSettings.GetRoutingSettings(ctx)
		if err != nil {
			return nil, fmt.Errorf("read routing settings: %w", err)
		}
	}
	services := make(map[contract.ServiceID]contract.Service)
	err := resolver.eachService(ctx, func(service contract.Service) error {
		if err := service.Validate(); err != nil {
			return fmt.Errorf("persisted endpoint failed validation: %w", err)
		}
		services[service.ID] = service
		return nil
	})
	if err != nil {
		return nil, err
	}
	stored, err := resolver.storedOrder(ctx, "")
	if err != nil {
		return nil, err
	}
	groups := resolver.groupPackages(services, stored, resolver.now())
	packages := make([]contract.ServicePackage, 0, len(groups))
	for _, group := range groups {
		pkg := group.ServicePackage
		pkg.Strategy = settings.StrategyForPackage(pkg.ID)
		pkg.ServiceIDs = make([]contract.ServiceID, 0, len(group.members))
		for _, member := range group.members {
			pkg.ServiceIDs = append(pkg.ServiceIDs, member.id)
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}

type packageGroup struct {
	contract.ServicePackage
	members []packageMember
}

type packageMember struct {
	id    contract.ServiceID
	quota accountQuota
}

// groupPackages puts services into packages in routing order: a package sits
// where its earliest member is stored, whether or not that member can be
// scheduled, and its members keep the stored order.
func (resolver *StoreResolver) groupPackages(services map[contract.ServiceID]contract.Service, stored func(a, b contract.ServiceID) bool, now time.Time) []packageGroup {
	ordered := make([]contract.Service, 0, len(services))
	for _, service := range services {
		ordered = append(ordered, service)
	}
	sort.Slice(ordered, func(left, right int) bool { return stored(ordered[left].ID, ordered[right].ID) })
	groups := make([]packageGroup, 0, len(ordered))
	index := make(map[string]int, len(ordered))
	for _, service := range ordered {
		var usage contract.SubscriptionUsage
		cached := false
		if service.Kind.IsSubscription() && resolver.usage != nil {
			usage, cached = resolver.usage.CachedUsage(service.ID)
		}
		pkg := contract.ServicePackageFor(service, packagePlan(service, usage, cached))
		at, found := index[pkg.ID]
		if !found {
			at = len(groups)
			index[pkg.ID] = at
			groups = append(groups, packageGroup{ServicePackage: pkg})
		}
		groups[at].members = append(groups[at].members, packageMember{id: service.ID, quota: quotaOf(usage, cached, now)})
	}
	return groups
}

// packagePlan is the plan a service is grouped by: the one its cached usage
// snapshot reports, else the one stored from an earlier read, so a restart
// keeps every account in its package.
func packagePlan(service contract.Service, usage contract.SubscriptionUsage, cached bool) string {
	if cached && strings.TrimSpace(usage.PlanType) != "" {
		return usage.PlanType
	}
	if service.Subscription != nil {
		return service.Subscription.PlanType
	}
	return ""
}

// packageOrder is the routing order of services: packages where their
// earliest member is stored, and the members of each package in the order its
// strategy gives. Ties keep the stored order.
func (resolver *StoreResolver) packageOrder(services map[contract.ServiceID]contract.Service, stored func(a, b contract.ServiceID) bool, settings contract.RoutingSettings) func(a, b contract.ServiceID) bool {
	position := make(map[contract.ServiceID]int, len(services))
	for _, group := range resolver.groupPackages(services, stored, resolver.now()) {
		strategy := settings.StrategyForPackage(group.ID)
		members := group.members
		sort.SliceStable(members, func(left, right int) bool {
			return members[left].quota.before(members[right].quota, strategy)
		})
		for _, member := range members {
			position[member.id] = len(position)
		}
	}
	return func(a, b contract.ServiceID) bool { return position[a] < position[b] }
}

// accountQuota is what an account's cached usage says about its quota.
type accountQuota struct {
	// known is set when the snapshot has a window to rank the account by.
	known bool
	// exhausted is set when the snapshot says the account is at its limit.
	exhausted bool
	// remaining is the percent left in the binding window.
	remaining float64
	// reset is when the binding window resets; zero when unknown.
	reset time.Time
}

// quotaOf reads the binding window of a snapshot: the 5-hour or weekly window
// with less left. On a tie the later known reset binds, since quota only
// returns once both windows reset. A window whose reset has passed since the
// snapshot counts as unused, with its next reset unknown.
func quotaOf(usage contract.SubscriptionUsage, cached bool, now time.Time) accountQuota {
	if !cached {
		return accountQuota{}
	}
	var quota accountQuota
	rolled := false
	for _, window := range []*contract.RateLimitWindow{usage.Primary, usage.Secondary} {
		if window == nil {
			continue
		}
		used, reset := window.UsedPercent, windowReset(window, usage.FetchedAt)
		if !reset.IsZero() && !reset.After(now) {
			used, reset, rolled = 0, time.Time{}, true
		}
		remaining := min(max(100-used, 0), 100)
		if !quota.known || remaining < quota.remaining ||
			(remaining == quota.remaining && !reset.IsZero() && reset.After(quota.reset)) {
			quota.known, quota.remaining, quota.reset = true, remaining, reset
		}
	}
	// The limit flags describe the snapshot; after a reset the windows tell.
	flagged := (usage.LimitReached != nil && *usage.LimitReached) || (usage.Allowed != nil && !*usage.Allowed)
	quota.exhausted = (flagged && !rolled) || (quota.known && quota.remaining <= 0)
	return quota
}

// tier groups accounts before their strategy orders them: quota left, then
// (auto only) at most autoLowRemainingPercent left, then at the limit, then
// no window to rank by, which keeps the stored order.
func (quota accountQuota) tier(strategy contract.PackageStrategy) int {
	switch {
	case quota.exhausted:
		return 2
	case !quota.known:
		return 3
	case strategy == contract.PackageStrategyAuto && quota.remaining <= autoLowRemainingPercent:
		return 1
	default:
		return 0
	}
}

// before orders two accounts of one package. Accounts at the limit go by
// reset, earliest first, since the first to reset is the first to work again.
func (quota accountQuota) before(other accountQuota, strategy contract.PackageStrategy) bool {
	tier, otherTier := quota.tier(strategy), other.tier(strategy)
	if tier != otherTier {
		return tier < otherTier
	}
	switch {
	case tier == 1, tier == 0 && strategy == contract.PackageStrategyMostRemaining:
		return quota.remaining > other.remaining
	case tier == 0, tier == 2:
		return resetsBefore(quota.reset, other.reset)
	default:
		return false
	}
}

// windowReset is the window's reset time, or the snapshot time plus the
// seconds it had left; zero when the provider reported neither.
func windowReset(window *contract.RateLimitWindow, fetchedAt time.Time) time.Time {
	switch {
	case window.ResetAt != nil:
		return *window.ResetAt
	case window.ResetAfterSeconds != nil && !fetchedAt.IsZero():
		return fetchedAt.Add(time.Duration(*window.ResetAfterSeconds) * time.Second)
	default:
		return time.Time{}
	}
}

// resetsBefore orders known reset times earliest first, ahead of unknown ones.
func resetsBefore(reset, other time.Time) bool {
	if reset.IsZero() || other.IsZero() {
		return !reset.IsZero() && other.IsZero()
	}
	return reset.Before(other)
}
