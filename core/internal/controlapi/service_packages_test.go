package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

// fixedUsage serves usage snapshots the way the subscription manager's cache
// does.
type fixedUsage map[contract.ServiceID]contract.SubscriptionUsage

func (usage fixedUsage) CachedUsage(id contract.ServiceID) (contract.SubscriptionUsage, bool) {
	snapshot, ok := usage[id]
	return snapshot, ok
}

func newServicePackagesHandler(t *testing.T) (*sqlite.Store, *Handler) {
	t.Helper()
	ctx := context.Background()
	store, err := openControlTestStore(t, ctx, t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	codex := func(id contract.ServiceID) contract.Service {
		return contract.Service{
			ID: id, Name: string(id), Kind: contract.ServiceKindCodexSubscription, Enabled: true, Models: []string{},
			Capabilities: contract.DefaultOpenAICodexCapabilities(),
			Subscription: &contract.SubscriptionConnection{Provider: contract.SubscriptionProviderOpenAICodex, Status: contract.SubscriptionStatusDisconnected},
		}
	}
	plusB := codex("service_plus_b")
	plusB.Enabled = false
	// Creation order is the stored order.
	for _, service := range []contract.Service{
		{
			ID: "service_http", Name: "HTTP", Kind: contract.ServiceKindOpenAI, Enabled: true, Models: []string{"public"},
			HTTP:         &contract.HTTPConnection{BaseURL: "https://example.test", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}},
			Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true}},
		},
		codex("service_plus_a"), codex("service_codex_new"), plusB,
	} {
		if _, err := store.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	resolver, err := endpoint.NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	resolver.WithUsageCache(fixedUsage{
		"service_plus_a": {ServiceID: "service_plus_a", FetchedAt: now, PlanType: "Plus"},
		"service_plus_b": {ServiceID: "service_plus_b", FetchedAt: now, PlanType: "plus"},
	})
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore: store, ServicePackages: resolver, ControlToken: testControlToken,
	})
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	return store, handler
}

func TestServicePackagesListAndStoreStrategies(t *testing.T) {
	store, handler := newServicePackagesHandler(t)
	ctx := context.Background()
	response := controlRequest(t, handler, http.MethodGet, ServicePackagesPath, "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", response.Code, response.Body.String())
	}
	var wire struct {
		Packages []map[string]json.RawMessage `json:"packages"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil || len(wire.Packages) != 3 {
		t.Fatalf("GET body = %s (%v)", response.Body.String(), err)
	}
	// An unknown plan is left out rather than guessed.
	unknownKeys := slices.Sorted(func(yield func(string) bool) {
		for key := range wire.Packages[2] {
			if !yield(key) {
				return
			}
		}
	})
	if want := []string{"id", "kind", "provider", "service_ids", "strategy"}; !reflect.DeepEqual(unknownKeys, want) {
		t.Fatalf("unknown plan package keys = %v, want %v", unknownKeys, want)
	}
	var listed contract.ServicePackageList
	decode(t, response, &listed)
	auto, codex := contract.PackageStrategyAuto, contract.SubscriptionProviderOpenAICodex
	want := []contract.ServicePackage{
		{ID: "service:service_http", Kind: contract.ServicePackageKindService, ServiceIDs: []contract.ServiceID{"service_http"}, Strategy: auto},
		{
			ID: "subscription:codex:plus", Kind: contract.ServicePackageKindSubscription, Provider: codex, PlanType: "plus",
			ServiceIDs: []contract.ServiceID{"service_plus_a", "service_plus_b"}, Strategy: auto,
		},
		{
			ID: "subscription:codex:unknown", Kind: contract.ServicePackageKindSubscription, Provider: codex,
			ServiceIDs: []contract.ServiceID{"service_codex_new"}, Strategy: auto,
		},
	}
	if !reflect.DeepEqual(listed.Packages, want) {
		t.Fatalf("packages = %#v\nwant %#v", listed.Packages, want)
	}

	// The desktop escapes the colons; Go clients leave them as they are.
	for _, path := range []string{ServicePackagesPath + "/subscription%3Acodex%3Aplus", ServicePackagesPath + "/subscription:codex:plus"} {
		response = controlRequest(t, handler, http.MethodPatch, path, "application/merge-patch+json", `{"strategy":"most_remaining"}`, "")
		if response.Code != http.StatusOK {
			t.Fatalf("PATCH %s: %d %s", path, response.Code, response.Body.String())
		}
		var updated contract.ServicePackage
		decode(t, response, &updated)
		wantUpdated := want[1]
		wantUpdated.Strategy = contract.PackageStrategyMostRemaining
		if !reflect.DeepEqual(updated, wantUpdated) {
			t.Fatalf("PATCH %s = %#v", path, updated)
		}
	}
	strategies := func() map[string]contract.PackageStrategy {
		t.Helper()
		settings, err := store.GetRoutingSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return settings.PackageStrategies
	}
	if got := strategies(); !reflect.DeepEqual(got, map[string]contract.PackageStrategy{"subscription:codex:plus": contract.PackageStrategyMostRemaining}) {
		t.Fatalf("stored strategies = %v", got)
	}
	listed = contract.ServicePackageList{}
	decode(t, controlRequest(t, handler, http.MethodGet, ServicePackagesPath, "", "", ""), &listed)
	if listed.Packages[1].Strategy != contract.PackageStrategyMostRemaining {
		t.Fatalf("listed strategy = %q", listed.Packages[1].Strategy)
	}

	// Routing settings keep the strategies but neither show nor take them.
	response = controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "package_strategies") {
		t.Fatalf("GET routing settings: %d %s", response.Code, response.Body.String())
	}
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", `{"max_attempts":7}`, "")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "package_strategies") || len(strategies()) != 1 {
		t.Fatalf("PATCH routing settings: %d %s, strategies %v", response.Code, response.Body.String(), strategies())
	}
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", `{"package_strategies":{}}`, "")
	if response.Code != http.StatusUnprocessableEntity || len(strategies()) != 1 {
		t.Fatalf("PATCH routing settings package_strategies: %d %s", response.Code, response.Body.String())
	}

	// auto is the default, so choosing it drops the entry.
	response = controlRequest(t, handler, http.MethodPatch, ServicePackagesPath+"/subscription:codex:plus", "application/merge-patch+json", `{"strategy":"auto"}`, "")
	if response.Code != http.StatusOK || strategies() != nil {
		t.Fatalf("PATCH auto: %d %s, strategies %v", response.Code, response.Body.String(), strategies())
	}
}

// TestServicePackageUpdatePrunesOnlyVanishedPackages checks that saving a
// strategy drops the strategies of packages no service forms any more, and
// keeps a package whose account has only a plan stored by an earlier usage
// read, as after a restart.
func TestServicePackageUpdatePrunesOnlyVanishedPackages(t *testing.T) {
	store, handler := newServicePackagesHandler(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	if err := store.PutSubscriptionAccount(ctx, contract.SubscriptionAccount{
		ID: "service_pro_stored", DisplayName: "Pro", Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.SubscriptionStatusConnected, ProviderAccountID: "acct_pro",
		CredentialRef: "local://subscription/service_pro_stored", Capabilities: contract.DefaultOpenAICodexCapabilities(),
		TokenExpiresAt: &expires, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if wrote, err := store.ObserveSubscriptionPlan(ctx, "service_pro_stored", "acct_pro", "pro"); err != nil || !wrote {
		t.Fatalf("ObserveSubscriptionPlan() = %v, %v", wrote, err)
	}
	settings, err := store.GetRoutingSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.PackageStrategies = map[string]contract.PackageStrategy{
		"subscription:codex:plus": contract.PackageStrategyMostRemaining,
		"subscription:codex:pro":  contract.PackageStrategySoonestReset,
		"subscription:codex:team": contract.PackageStrategyMostRemaining,
		"service:service_gone":    contract.PackageStrategySoonestReset,
	}
	if err := store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}

	response := controlRequest(t, handler, http.MethodPatch, ServicePackagesPath+"/service:service_http", "application/merge-patch+json", `{"strategy":"soonest_reset"}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", response.Code, response.Body.String())
	}
	settings, err = store.GetRoutingSettings(ctx)
	want := map[string]contract.PackageStrategy{
		"service:service_http":    contract.PackageStrategySoonestReset,
		"subscription:codex:plus": contract.PackageStrategyMostRemaining,
		"subscription:codex:pro":  contract.PackageStrategySoonestReset,
	}
	if err != nil || !reflect.DeepEqual(settings.PackageStrategies, want) {
		t.Fatalf("stored strategies = %v (%v), want %v", settings.PackageStrategies, err, want)
	}
	var listed contract.ServicePackageList
	decode(t, controlRequest(t, handler, http.MethodGet, ServicePackagesPath, "", "", ""), &listed)
	index := slices.IndexFunc(listed.Packages, func(pkg contract.ServicePackage) bool { return pkg.ID == "subscription:codex:pro" })
	if index < 0 || !reflect.DeepEqual(listed.Packages[index].ServiceIDs, []contract.ServiceID{"service_pro_stored"}) ||
		listed.Packages[index].Strategy != contract.PackageStrategySoonestReset {
		t.Fatalf("packages = %#v, want the stored-plan package with its strategy", listed.Packages)
	}
}

func TestServicePackagesRejectBadRequests(t *testing.T) {
	store, handler := newServicePackagesHandler(t)
	plus := ServicePackagesPath + "/subscription:codex:plus"
	for _, test := range []struct {
		method, path, contentType, body string
		code                            int
		errorCode, allow                string
	}{
		{http.MethodPatch, ServicePackagesPath + "/service:service_missing", "application/merge-patch+json", `{"strategy":"auto"}`, http.StatusNotFound, "not_found", ""},
		{http.MethodPatch, ServicePackagesPath + "/subscription:claude:pro", "application/merge-patch+json", `{"strategy":"auto"}`, http.StatusNotFound, "not_found", ""},
		{http.MethodPatch, ServicePackagesPath + "/service:service_http/x", "application/merge-patch+json", `{"strategy":"auto"}`, http.StatusNotFound, "not_found", ""},
		{http.MethodPatch, ServicePackagesPath + "/service:service%2Fhttp", "application/merge-patch+json", `{"strategy":"auto"}`, http.StatusNotFound, "not_found", ""},
		{http.MethodPatch, plus, "application/merge-patch+json", `{"strategy":"fastest"}`, http.StatusUnprocessableEntity, "invalid_package_strategy", ""},
		{http.MethodPatch, plus, "application/merge-patch+json", `{"strategy":null}`, http.StatusUnprocessableEntity, "invalid_package_strategy", ""},
		{http.MethodPatch, plus, "application/merge-patch+json", `{}`, http.StatusUnprocessableEntity, "invalid_package_strategy", ""},
		{http.MethodPatch, plus, "application/merge-patch+json", `{"strategy":"auto","service_ids":[]}`, http.StatusBadRequest, "invalid_json", ""},
		{http.MethodPatch, plus, "application/json", `{"strategy":"auto"}`, http.StatusUnsupportedMediaType, "unsupported_media_type", ""},
		{http.MethodPost, ServicePackagesPath, "application/json", `{}`, http.StatusMethodNotAllowed, "method_not_allowed", http.MethodGet},
		{http.MethodGet, plus, "", "", http.StatusMethodNotAllowed, "method_not_allowed", http.MethodPatch},
	} {
		response := controlRequest(t, handler, test.method, test.path, test.contentType, test.body, "")
		var envelope errorEnvelope
		decode(t, response, &envelope)
		if response.Code != test.code || envelope.Error.Code != test.errorCode || response.Header().Get("Allow") != test.allow {
			t.Errorf("%s %s %s: %d %+v Allow=%q, want %d %s", test.method, test.path, test.body, response.Code, envelope.Error, response.Header().Get("Allow"), test.code, test.errorCode)
		}
	}
	settings, err := store.GetRoutingSettings(context.Background())
	if err != nil || settings.PackageStrategies != nil {
		t.Fatalf("rejected requests stored strategies %v (%v)", settings.PackageStrategies, err)
	}

	// Without a resolver the surface answers 503.
	_, bare := newRouteHandler(t)
	for _, test := range []struct{ method, path, contentType, body string }{
		{http.MethodGet, ServicePackagesPath, "", ""},
		{http.MethodPatch, plus, "application/merge-patch+json", `{"strategy":"auto"}`},
	} {
		response := controlRequest(t, bare, test.method, test.path, test.contentType, test.body, "")
		var envelope errorEnvelope
		decode(t, response, &envelope)
		if response.Code != http.StatusServiceUnavailable || envelope.Error.Code != "service_packages_unavailable" {
			t.Errorf("%s %s without a resolver: %d %+v", test.method, test.path, response.Code, envelope.Error)
		}
	}
}
