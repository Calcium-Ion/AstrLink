package endpoint

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

var packageNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// packageUsage serves fixed usage snapshots. Usage is the read that reaches
// the provider, which the subscription manager also has; routing must never
// call it.
type packageUsage struct {
	t         *testing.T
	snapshots map[contract.ServiceID]contract.SubscriptionUsage
}

func (usage packageUsage) CachedUsage(id contract.ServiceID) (contract.SubscriptionUsage, bool) {
	snapshot, ok := usage.snapshots[id]
	return snapshot, ok
}

func (usage packageUsage) Usage(context.Context, contract.ServiceID) (contract.SubscriptionUsage, error) {
	usage.t.Error("routing read usage from the provider")
	return contract.SubscriptionUsage{}, errors.New("no provider in tests")
}

type packageSettings struct{ settings contract.RoutingSettings }

func (store *packageSettings) GetRoutingSettings(context.Context) (contract.RoutingSettings, error) {
	return store.settings, nil
}

func (store *packageSettings) UpdateRoutingSettings(_ context.Context, settings contract.RoutingSettings) error {
	store.settings = settings
	return nil
}

func packageAccount(id contract.ServiceID, kind contract.ServiceKind) contract.Service {
	return contract.Service{
		ID: id, Name: string(id), Kind: kind, Enabled: true, Models: []string{"gpt-5"},
		Capabilities: kind.SubscriptionProvider().Capabilities(),
		Subscription: &contract.SubscriptionConnection{
			Provider: kind.SubscriptionProvider(), Status: contract.SubscriptionStatusConnected,
			CredentialRef: "keyring://subscription/" + string(id),
		},
	}
}

func packageHTTPService(id contract.ServiceID) contract.Service {
	return contract.ServiceFromEndpoint(resolverEndpoint(id, contract.CapabilityModeNative, true, nil))
}

// planUsage is a snapshot of plan whose 5-hour window is used percent full
// and resets after resetIn.
func planUsage(plan string, used float64, resetIn time.Duration) contract.SubscriptionUsage {
	reset := packageNow.Add(resetIn)
	return contract.SubscriptionUsage{
		FetchedAt: packageNow, PlanType: plan,
		Primary: &contract.RateLimitWindow{UsedPercent: used, ResetAt: &reset},
	}
}

func newPackageResolver(
	t *testing.T,
	services []contract.Service,
	order []contract.ServiceID,
	snapshots map[contract.ServiceID]contract.SubscriptionUsage,
	settings contract.RoutingSettings,
) *StoreResolver {
	t.Helper()
	resolver, err := NewStoreResolver(rankingStore{services: services, order: order})
	if err != nil {
		t.Fatal(err)
	}
	resolver.routingSettings = &packageSettings{settings: settings}
	resolver.clock = func() time.Time { return packageNow }
	return resolver.WithUsageCache(packageUsage{t: t, snapshots: snapshots})
}

var packageRequest = ResolveRequest{
	Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true, AllCandidates: true,
}

func resolvedIDs(t *testing.T, resolver *StoreResolver, request ResolveRequest) []contract.ServiceID {
	t.Helper()
	candidates, _, err := resolver.ResolveRankedCandidates(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]contract.ServiceID, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.CanonicalService().ID)
	}
	return ids
}

func rankedIDs(ranking []RankedService) []contract.ServiceID {
	ids := make([]contract.ServiceID, 0, len(ranking))
	for _, ranked := range ranking {
		ids = append(ids, ranked.ServiceID)
	}
	return ids
}

func TestServicePackagesSitWhereTheirEarliestMemberIsStored(t *testing.T) {
	codex := contract.ServiceKindCodexSubscription
	// The earliest Plus account cannot be scheduled; its package still sits
	// where that account is stored.
	plusPaused := packageAccount("plus_paused", codex)
	plusPaused.Enabled = false
	services := []contract.Service{
		packageHTTPService("http_new"), packageAccount("unknown_b", codex),
		packageAccount("claude_max", contract.ServiceKindClaudeSubscription), packageAccount("unknown_a", codex),
		packageAccount("plus_b", codex), packageAccount("pro_a", codex), plusPaused, packageHTTPService("http_a"),
	}
	order := []contract.ServiceID{"http_a", "plus_paused", "pro_a", "plus_b", "unknown_a", "claude_max", "unknown_b"}
	snapshots := map[contract.ServiceID]contract.SubscriptionUsage{
		"plus_paused": planUsage(" Plus", 10, time.Hour),
		"plus_b":      planUsage("plus", 10, time.Hour),
		"pro_a":       planUsage("pro", 10, time.Hour),
		// A snapshot without a plan is as unknown as no snapshot.
		"unknown_b":  {FetchedAt: packageNow},
		"claude_max": planUsage("max_20x", 10, time.Hour),
	}
	settings := contract.DefaultRoutingSettings()
	settings.PackageStrategies = map[string]contract.PackageStrategy{
		"subscription:codex:plus": contract.PackageStrategyMostRemaining,
	}
	resolver := newPackageResolver(t, services, order, snapshots, settings)

	packages, err := resolver.ServicePackages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	auto, subscription, codexProvider := contract.PackageStrategyAuto, contract.ServicePackageKindSubscription, contract.SubscriptionProviderOpenAICodex
	want := []contract.ServicePackage{
		{ID: "service:http_a", Kind: contract.ServicePackageKindService, ServiceIDs: []contract.ServiceID{"http_a"}, Strategy: auto},
		{
			ID: "subscription:codex:plus", Kind: subscription, Provider: codexProvider, PlanType: "plus",
			ServiceIDs: []contract.ServiceID{"plus_paused", "plus_b"}, Strategy: contract.PackageStrategyMostRemaining,
		},
		{
			ID: "subscription:codex:pro", Kind: subscription, Provider: codexProvider, PlanType: "pro",
			ServiceIDs: []contract.ServiceID{"pro_a"}, Strategy: auto,
		},
		{
			ID: "subscription:codex:unknown", Kind: subscription, Provider: codexProvider,
			ServiceIDs: []contract.ServiceID{"unknown_a", "unknown_b"}, Strategy: auto,
		},
		{
			ID: "subscription:claude:max_20x", Kind: subscription, Provider: contract.SubscriptionProviderClaudeCode,
			PlanType: "max_20x", ServiceIDs: []contract.ServiceID{"claude_max"}, Strategy: auto,
		},
		// A service missing from the stored order comes last.
		{ID: "service:http_new", Kind: contract.ServicePackageKindService, ServiceIDs: []contract.ServiceID{"http_new"}, Strategy: auto},
	}
	if !reflect.DeepEqual(packages, want) {
		t.Fatalf("packages = %#v\nwant %#v", packages, want)
	}

	candidates, ranking, err := resolver.ResolveRankedCandidates(context.Background(), packageRequest)
	if err != nil {
		t.Fatal(err)
	}
	// plus_b goes before pro_a, which is stored first, because its package does.
	wantCandidates := []contract.ServiceID{"http_a", "plus_b", "pro_a", "unknown_a", "unknown_b", "http_new"}
	got := make([]contract.ServiceID, 0, len(candidates))
	for _, candidate := range candidates {
		got = append(got, candidate.CanonicalService().ID)
	}
	if !reflect.DeepEqual(got, wantCandidates) {
		t.Fatalf("candidates = %v, want %v", got, wantCandidates)
	}
	wantRanking := []contract.ServiceID{"http_a", "plus_paused", "plus_b", "pro_a", "unknown_a", "unknown_b", "claude_max", "http_new"}
	if !reflect.DeepEqual(rankedIDs(ranking), wantRanking) || ranking[1].Skip != contract.RoutingSkipDisabled || ranking[6].Skip == "" {
		t.Fatalf("ranking = %#v, want order %v", ranking, wantRanking)
	}
}

func TestPackageStrategiesOrderAccountsByCachedUsage(t *testing.T) {
	codex := contract.ServiceKindCodexSubscription
	blocked := false
	snapshots := map[contract.ServiceID]contract.SubscriptionUsage{
		"low":  planUsage("plus", 95, time.Hour),
		"edge": planUsage("plus", 90, 30*time.Minute),
		// half_a, half_b, tie_x and tie_y tie; the stored order, then the
		// ID, decides.
		"half_a": planUsage("plus", 50, 3*time.Hour),
		"half_b": planUsage("plus", 50, 3*time.Hour),
		"tie_x":  planUsage("plus", 50, 3*time.Hour),
		"tie_y":  planUsage("plus", 50, 3*time.Hour),
		"fresh":  planUsage("plus", 20, 4*time.Hour),
		"full":   planUsage("plus", 100, 2*time.Hour),
		// The plan without a window to rank by.
		"cold_a": {FetchedAt: packageNow, PlanType: "plus"},
		"cold_b": {FetchedAt: packageNow, PlanType: "plus"},
	}
	refused := planUsage("plus", 60, 90*time.Minute)
	refused.Allowed = &blocked
	snapshots["refused"] = refused
	var services []contract.Service
	for _, id := range []contract.ServiceID{"tie_y", "tie_x", "cold_b", "edge", "half_b", "refused", "fresh", "low", "half_a", "full", "cold_a"} {
		services = append(services, packageAccount(id, codex))
	}
	// tie_x and tie_y are missing from the stored order.
	order := []contract.ServiceID{"cold_a", "full", "half_a", "low", "fresh", "refused", "half_b", "edge", "cold_b"}
	for _, test := range []struct {
		strategy contract.PackageStrategy
		want     []contract.ServiceID
	}{
		{contract.PackageStrategySoonestReset, []contract.ServiceID{
			"edge", "low", "half_a", "half_b", "tie_x", "tie_y", "fresh", "refused", "full", "cold_a", "cold_b",
		}},
		{contract.PackageStrategyMostRemaining, []contract.ServiceID{
			"fresh", "half_a", "half_b", "tie_x", "tie_y", "edge", "low", "refused", "full", "cold_a", "cold_b",
		}},
		// Above 10% left by soonest reset, then the rest by most left.
		{contract.PackageStrategyAuto, []contract.ServiceID{
			"half_a", "half_b", "tie_x", "tie_y", "fresh", "edge", "low", "refused", "full", "cold_a", "cold_b",
		}},
	} {
		settings := contract.DefaultRoutingSettings()
		if test.strategy != contract.PackageStrategyAuto {
			settings.PackageStrategies = map[string]contract.PackageStrategy{"subscription:codex:plus": test.strategy}
		}
		resolver := newPackageResolver(t, services, order, snapshots, settings)
		if got := resolvedIDs(t, resolver, packageRequest); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: candidates = %v\nwant %v", test.strategy, got, test.want)
		}
	}
}

func TestPackageQuotaReadsTheBindingWindow(t *testing.T) {
	at := func(offset time.Duration) *time.Time {
		value := packageNow.Add(offset)
		return &value
	}
	halfHour := int64(1800)
	reached, refused := true, false
	week := 72 * time.Hour
	for _, test := range []struct {
		name  string
		usage contract.SubscriptionUsage
		want  accountQuota
	}{
		{"the window with less left binds", contract.SubscriptionUsage{
			Primary:   &contract.RateLimitWindow{UsedPercent: 20, ResetAt: at(time.Hour)},
			Secondary: &contract.RateLimitWindow{UsedPercent: 60, ResetAt: at(week)},
		}, accountQuota{known: true, remaining: 40, reset: *at(week)}},
		{"equal windows bind on the later reset", contract.SubscriptionUsage{
			Primary:   &contract.RateLimitWindow{UsedPercent: 50, ResetAt: at(week)},
			Secondary: &contract.RateLimitWindow{UsedPercent: 50, ResetAt: at(time.Hour)},
		}, accountQuota{known: true, remaining: 50, reset: *at(week)}},
		{"reset after seconds counts from the snapshot", contract.SubscriptionUsage{
			FetchedAt: packageNow.Add(-10 * time.Minute),
			Primary:   &contract.RateLimitWindow{UsedPercent: 30, ResetAfterSeconds: &halfHour},
		}, accountQuota{known: true, remaining: 70, reset: *at(20 * time.Minute)}},
		{"a window reset since the snapshot is unused", contract.SubscriptionUsage{
			LimitReached: &reached,
			Primary:      &contract.RateLimitWindow{UsedPercent: 100, ResetAt: at(-time.Minute)},
			Secondary:    &contract.RateLimitWindow{UsedPercent: 40, ResetAt: at(week)},
		}, accountQuota{known: true, remaining: 60, reset: *at(week)}},
		{"limit flags without windows", contract.SubscriptionUsage{LimitReached: &reached}, accountQuota{exhausted: true}},
		{"a refused account is at its limit", contract.SubscriptionUsage{
			Allowed: &refused, Primary: &contract.RateLimitWindow{UsedPercent: 30, ResetAt: at(time.Hour)},
		}, accountQuota{known: true, exhausted: true, remaining: 70, reset: *at(time.Hour)}},
		{"a full window is at its limit", contract.SubscriptionUsage{
			Primary: &contract.RateLimitWindow{UsedPercent: 100, ResetAt: at(time.Hour)},
		}, accountQuota{known: true, exhausted: true, reset: *at(time.Hour)}},
	} {
		if got := quotaOf(test.usage, true, packageNow); got != test.want {
			t.Errorf("%s: quotaOf() = %+v, want %+v", test.name, got, test.want)
		}
	}
	if got := quotaOf(contract.SubscriptionUsage{Primary: &contract.RateLimitWindow{UsedPercent: 10}}, false, packageNow); got != (accountQuota{}) {
		t.Errorf("quotaOf() without a cached snapshot = %+v", got)
	}
}

func TestPackageOrderKeepsCooldownsPinsAndDiscovery(t *testing.T) {
	codex := contract.ServiceKindCodexSubscription
	services := []contract.Service{
		packageAccount("plus_a", codex), packageAccount("plus_b", codex), packageAccount("plus_c", codex),
		packageHTTPService("http_x"),
	}
	order := []contract.ServiceID{"plus_a", "http_x", "plus_b", "plus_c"}
	snapshots := map[contract.ServiceID]contract.SubscriptionUsage{
		"plus_a": planUsage("plus", 50, 3*time.Hour),
		"plus_b": planUsage("plus", 50, time.Hour),
		"plus_c": planUsage("plus", 50, 2*time.Hour),
	}
	settings := contract.DefaultRoutingSettings()
	settings.AllowUnmatchedFailover = false
	resolver := newPackageResolver(t, services, order, snapshots, settings)
	ctx := context.Background()

	// Failover tries the rest of the package before the next package.
	candidates, _, err := resolver.ResolveRankedCandidates(ctx, packageRequest)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resolvedIDs(t, resolver, packageRequest), []contract.ServiceID{"plus_b", "plus_c", "plus_a", "http_x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}

	// A cooling account is skipped and the next one of its package leads.
	resolver.RecordRateLimit(candidates[0], time.Minute)
	_, ranking, err := resolver.ResolveRankedCandidates(ctx, packageRequest)
	if err != nil {
		t.Fatal(err)
	}
	wantRanking := []RankedService{
		{ServiceID: "plus_b", Skip: contract.RoutingSkipRateLimited}, {ServiceID: "plus_c"}, {ServiceID: "plus_a"}, {ServiceID: "http_x"},
	}
	if !reflect.DeepEqual(ranking, wantRanking) {
		t.Fatalf("ranking = %#v\nwant %#v", ranking, wantRanking)
	}
	for _, test := range []struct {
		name    string
		request ResolveRequest
		want    []contract.ServiceID
	}{
		{"all candidates", packageRequest, []contract.ServiceID{"plus_c", "plus_a", "http_x"}},
		{"without failover only the first", ResolveRequest{
			Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true,
		}, []contract.ServiceID{"plus_c"}},
		// Affinity then picks the conversation's own account among them.
		{"a continuation keeps every candidate", ResolveRequest{
			Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true, Continuation: true,
		}, []contract.ServiceID{"plus_c", "plus_a", "http_x"}},
		{"a pinned request resolves only its service", ResolveRequest{
			Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true, AllCandidates: true, Service: "plus_a",
		}, []contract.ServiceID{"plus_a"}},
		// Discovery merges every account's models, so it keeps the stored order.
		{"model discovery", ResolveRequest{Protocol: contract.ProtocolOpenAIModels}, []contract.ServiceID{"plus_a", "plus_b", "plus_c"}},
	} {
		if got := resolvedIDs(t, resolver, test.request); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: candidates = %v, want %v", test.name, got, test.want)
		}
	}
}

func packageMembers(t *testing.T, resolver *StoreResolver) []string {
	t.Helper()
	packages, err := resolver.ServicePackages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	members := make([]string, 0, len(packages))
	for _, pkg := range packages {
		ids := make([]string, 0, len(pkg.ServiceIDs))
		for _, id := range pkg.ServiceIDs {
			ids = append(ids, string(id))
		}
		members = append(members, pkg.ID+"="+strings.Join(ids, ","))
	}
	return members
}

func TestStoredPlansKeepPackagesAcrossARestart(t *testing.T) {
	order := []contract.ServiceID{"plus_a", "pro_a", "plus_b"}
	services := func(plans ...string) []contract.Service {
		services := make([]contract.Service, 0, len(order))
		for index, id := range order {
			service := packageAccount(id, contract.ServiceKindCodexSubscription)
			service.Subscription.PlanType = plans[index]
			services = append(services, service)
		}
		return services
	}
	settings := contract.DefaultRoutingSettings()
	stored := services("plus", "pro", "plus")
	running := newPackageResolver(t, stored, order, map[contract.ServiceID]contract.SubscriptionUsage{
		"plus_a": planUsage("Plus", 50, time.Hour),
		"pro_a":  planUsage("pro", 50, time.Hour),
		"plus_b": planUsage("plus", 50, time.Hour),
	}, settings)
	// A restart starts with an empty usage cache.
	restarted := newPackageResolver(t, stored, order, nil, settings)
	wantMembers := []string{"subscription:codex:plus=plus_a,plus_b", "subscription:codex:pro=pro_a"}
	wantCandidates := []contract.ServiceID{"plus_a", "plus_b", "pro_a"}
	for name, resolver := range map[string]*StoreResolver{"running": running, "restarted": restarted} {
		if got := packageMembers(t, resolver); !reflect.DeepEqual(got, wantMembers) {
			t.Errorf("%s: packages = %v, want %v", name, got, wantMembers)
		}
		if got := resolvedIDs(t, resolver, packageRequest); !reflect.DeepEqual(got, wantCandidates) {
			t.Errorf("%s: candidates = %v, want %v", name, got, wantCandidates)
		}
	}

	// Without stored plans a restart puts every account in the unknown package.
	forgotten := newPackageResolver(t, services("", "", ""), order, nil, settings)
	if got, want := packageMembers(t, forgotten), []string{"subscription:codex:unknown=plus_a,pro_a,plus_b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("without stored plans: packages = %v, want %v", got, want)
	}

	// A cached plan wins over the stored one; a cached snapshot without a plan
	// keeps the stored one.
	moved := newPackageResolver(t, stored, order, map[contract.ServiceID]contract.SubscriptionUsage{
		"plus_b": planUsage("pro", 50, time.Hour),
		"pro_a":  {FetchedAt: packageNow},
	}, settings)
	if got, want := packageMembers(t, moved), []string{"subscription:codex:plus=plus_a", "subscription:codex:pro=pro_a,plus_b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("cached plans: packages = %v, want %v", got, want)
	}
}
