package contract

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestServicePackageForGroupsByProviderAndPlan(t *testing.T) {
	claude := Service{ID: "svc_claude", Kind: ServiceKindClaudeSubscription}
	codex := Service{ID: "svc_codex", Kind: ServiceKindCodexSubscription}
	for _, test := range []struct {
		name     string
		service  Service
		planType string
		want     ServicePackage
	}{
		{"claude plan", claude, "max_20x", ServicePackage{
			ID: "subscription:claude:max_20x", Kind: ServicePackageKindSubscription,
			Provider: SubscriptionProviderClaudeCode, PlanType: "max_20x",
		}},
		{"plan key is normalized", codex, "  Plus ", ServicePackage{
			ID: "subscription:codex:plus", Kind: ServicePackageKindSubscription,
			Provider: SubscriptionProviderOpenAICodex, PlanType: "plus",
		}},
		{"unknown plan is never guessed", codex, " ", ServicePackage{
			ID: "subscription:codex:unknown", Kind: ServicePackageKindSubscription,
			Provider: SubscriptionProviderOpenAICodex,
		}},
		{"other services stand alone", Service{ID: "svc_http", Kind: ServiceKindOpenAI}, "plus", ServicePackage{
			ID: "service:svc_http", Kind: ServicePackageKindService,
		}},
	} {
		got := ServicePackageFor(test.service, test.planType)
		if got.ID != test.want.ID || got.Kind != test.want.Kind || got.Provider != test.want.Provider || got.PlanType != test.want.PlanType {
			t.Errorf("%s: ServicePackageFor() = %#v, want %#v", test.name, got, test.want)
		}
		if !ValidServicePackageID(got.ID) {
			t.Errorf("%s: ValidServicePackageID(%q) = false", test.name, got.ID)
		}
	}
	for _, kind := range []ServiceKind{
		ServiceKindCodexSubscription, ServiceKindClaudeSubscription, ServiceKindGrokSubscription,
		ServiceKindAntigravitySubscription, ServiceKindCopilotSubscription, ServiceKindDroidSubscription,
	} {
		if id := ServicePackageFor(Service{ID: "svc_any", Kind: kind}, "pro").ID; !ValidServicePackageID(id) {
			t.Errorf("%s: package ID %q is not valid", kind, id)
		}
	}
}

func TestValidServicePackageIDRejectsOtherShapes(t *testing.T) {
	for _, id := range []string{
		"", "plus", "service:", "service:Bad ID", "subscription:codex", "subscription:codex:",
		"subscription:codex:Plus", "subscription:openai_codex:plus", "subscription:other:plus",
		"subscription:codex: plus", "subscription:codex:" + strings.Repeat("p", 65),
	} {
		if ValidServicePackageID(id) {
			t.Errorf("ValidServicePackageID(%q) = true", id)
		}
	}
}

func TestStoredPlanTypeIsValidated(t *testing.T) {
	connection := SubscriptionConnection{Provider: SubscriptionProviderOpenAICodex, Status: SubscriptionStatusDisconnected}
	for _, plan := range []string{"", "plus", "Max 20x", strings.Repeat("p", 64)} {
		connection.PlanType = plan
		if err := connection.Validate("svc_codex"); err != nil {
			t.Errorf("plan %q: Validate() = %v", plan, err)
		}
	}
	for _, plan := range []string{
		strings.Repeat("p", 65), "owner@example.com", "access_token=abcdefgh12345678", "Bearer abcdefghijklmnop",
	} {
		connection.PlanType = plan
		if connection.Validate("svc_codex") == nil {
			t.Errorf("plan %q: Validate() accepted it", plan)
		}
		if ValidatePlanType(plan) == nil {
			t.Errorf("ValidatePlanType(%q) accepted it", plan)
		}
	}
	account := SubscriptionAccount{PlanType: "pro"}
	service := ServiceFromSubscriptionAccount(account)
	if service.Subscription.PlanType != "pro" {
		t.Fatalf("ServiceFromSubscriptionAccount() plan = %q", service.Subscription.PlanType)
	}
	service.Subscription.Provider = SubscriptionProviderOpenAICodex
	service.Kind = ServiceKindCodexSubscription
	if view, _ := service.SubscriptionAccountView(); view.PlanType != "pro" {
		t.Fatalf("SubscriptionAccountView() plan = %q", view.PlanType)
	}
}

func TestRoutingSettingsPackageStrategies(t *testing.T) {
	settings := DefaultRoutingSettings()
	if got := settings.StrategyForPackage("subscription:codex:plus"); got != PackageStrategyAuto {
		t.Fatalf("default strategy = %q", got)
	}
	encoded, err := json.Marshal(settings)
	if err != nil || strings.Contains(string(encoded), "package_strategies") {
		t.Fatalf("default settings encode package_strategies: %s, %v", encoded, err)
	}
	settings.PackageStrategies = map[string]PackageStrategy{
		"subscription:codex:plus": PackageStrategyMostRemaining,
		"service:svc_http":        PackageStrategySoonestReset,
	}
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := settings.StrategyForPackage("subscription:codex:plus"); got != PackageStrategyMostRemaining {
		t.Fatalf("stored strategy = %q", got)
	}
	encoded, _ = json.Marshal(settings)
	var decoded RoutingSettings
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.StrategyForPackage("service:svc_http") != PackageStrategySoonestReset {
		t.Fatalf("round trip = %#v, %v", decoded.PackageStrategies, err)
	}
	for name, strategies := range map[string]map[string]PackageStrategy{
		"bad key":   {"codex:plus": PackageStrategyAuto},
		"bad value": {"subscription:codex:plus": "fastest"},
	} {
		settings.PackageStrategies = strategies
		if settings.Validate() == nil {
			t.Errorf("%s: Validate() accepted %v", name, strategies)
		}
	}
	tooMany := make(map[string]PackageStrategy, MaxPackageStrategies+1)
	for index := range MaxPackageStrategies + 1 {
		tooMany[fmt.Sprintf("service:svc_%03d", index)] = PackageStrategyAuto
	}
	settings.PackageStrategies = tooMany
	if settings.Validate() == nil {
		t.Fatalf("Validate() accepted %d strategies", len(tooMany))
	}
}
