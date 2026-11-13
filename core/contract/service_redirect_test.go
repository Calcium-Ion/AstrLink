package contract_test

import (
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestUndatedClaudeModelDropsOnlyAClaudeReleaseDate(t *testing.T) {
	t.Parallel()
	for model, want := range map[string]string{
		"claude-haiku-4-5-20251001": "claude-haiku-4-5",
		"claude-sonnet-4-6":         "claude-sonnet-4-6",
		"claude-sonnet-4.6":         "claude-sonnet-4.6",
		"gpt-5-20250807":            "gpt-5-20250807",
	} {
		if got := contract.UndatedClaudeModel(model); got != want {
			t.Errorf("UndatedClaudeModel(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestServiceModelRedirectsServeListedTargetsAndAreValidated(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	kind := contract.ServiceKindCopilotSubscription
	service := contract.Service{
		ID: "service_copilot", Name: "Copilot", Kind: kind, Enabled: true,
		Models: []string{"claude-haiku-4.5", "claude-haiku-4-5-20251001-preview"}, Capabilities: kind.SubscriptionProvider().Capabilities(),
		Subscription: &contract.SubscriptionConnection{Provider: kind.SubscriptionProvider(), Status: contract.SubscriptionStatusDisconnected},
		ModelRedirects: []contract.ModelRedirect{
			{From: "claude-haiku-4-5", To: "claude-haiku-4.5", Enabled: true},
			{From: "claude-opus-4-6", To: "claude-opus-4.6", Enabled: true},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	for requested, want := range map[string]string{
		"claude-haiku-4-5":                  "claude-haiku-4.5",
		"claude-haiku-4-5-20251001":         "claude-haiku-4.5",
		"claude-haiku-4.5":                  "claude-haiku-4.5",
		"claude-haiku-4-5-20251001-preview": "claude-haiku-4-5-20251001-preview",
	} {
		if got, ok := service.UpstreamModelFor(requested); !ok || got != want {
			t.Errorf("UpstreamModelFor(%q) = %q, %t; want %q", requested, got, ok, want)
		}
	}
	for _, unserved := range []string{"", "claude-opus-4-6", "claude-opus-4-6-20260101", "claude-sonnet-4-6", "claude-sonnet-5-5"} {
		if got, ok := service.UpstreamModelFor(unserved); ok {
			t.Errorf("UpstreamModelFor(%q) = %q, want unserved", unserved, got)
		}
	}
	if err := service.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	service.ModelRedirects = append(service.ModelRedirects, contract.ModelRedirect{From: "claude-haiku-4.5", To: "x", Enabled: true})
	if err := service.Validate(); err == nil {
		t.Fatal("Validate() accepted a chained redirect table")
	}
}

func TestCopilotBuiltinRedirectsReachTheNewestClaudeModels(t *testing.T) {
	t.Parallel()
	service := contract.Service{
		Kind:   contract.ServiceKindCopilotSubscription,
		Models: []string{"claude-fable-5.2", "claude-haiku-5.5", "claude-opus-4.8", "claude-opus-5"},
	}
	for requested, want := range map[string]string{
		"claude-fable-5-2": "claude-fable-5.2",
		"claude-haiku-5-5": "claude-haiku-5.5",
		"claude-opus-4-8":  "claude-opus-4.8",
		"claude-opus-5":    "claude-opus-5",
	} {
		if got, ok := service.UpstreamModelFor(requested); !ok || got != want {
			t.Errorf("UpstreamModelFor(%q) = %q, %t; want %q", requested, got, ok, want)
		}
	}
	if first := contract.ServiceKindCopilotSubscription.BuiltinModelRedirects()[0]; first.From != "claude-fable-5-2" {
		t.Errorf("built-in rules should list the newest model first, got %#v", first)
	}
}

func TestCopilotBuiltinRedirectsYieldToStoredRules(t *testing.T) {
	t.Parallel()
	service := contract.Service{
		Kind:   contract.ServiceKindCopilotSubscription,
		Models: []string{"claude-sonnet-4.6", "claude-opus-5.5"},
		ModelRedirects: []contract.ModelRedirect{
			{From: "claude-opus-5-5", To: "claude-opus-5.5", Enabled: false},
		},
	}
	if got, ok := service.UpstreamModelFor("claude-sonnet-4-6"); !ok || got != "claude-sonnet-4.6" {
		t.Fatalf("built-in rule = %q, %t", got, ok)
	}
	if got, ok := service.UpstreamModelFor("claude-opus-5-5"); ok {
		t.Fatalf("a stored disabled rule left the built-in one on: %q", got)
	}
	effective := service.EffectiveModelRedirects()
	if effective[0].From != "claude-opus-5-5" || effective[0].Enabled {
		t.Fatalf("stored rule must come first: %#v", effective[0])
	}
	for _, redirect := range effective[1:] {
		if redirect.From == "claude-opus-5-5" {
			t.Fatal("built-in rule kept beside the stored one")
		}
	}
	if err := contract.ValidateModelRedirects(contract.ServiceKindCopilotSubscription.BuiltinModelRedirects()); err != nil {
		t.Fatalf("built-in rules are invalid: %v", err)
	}
	if contract.ServiceKindClaudeSubscription.BuiltinModelRedirects() != nil {
		t.Fatal("only Copilot has built-in rules")
	}
}
