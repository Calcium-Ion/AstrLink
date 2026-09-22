package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestRoutingSettingsGlobalInheritanceAndOverrides(t *testing.T) {
	store, handler := newRouteHandler(t)
	ctx := context.Background()
	response := controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
	if response.Code != 200 {
		t.Fatalf("GET: %d %s", response.Code, response.Body.String())
	}
	var settings contract.RoutingSettings
	decode(t, response, &settings)
	if !settings.CodexIdentityEnforcement || !settings.ClaudeIdentityEnforcement || !settings.GrokIdentityEnforcement || !settings.AllowUnmatchedFailover || settings.DefaultFailurePolicy.MaxRetries != 1 || settings.MaxAttempts != 6 {
		t.Fatalf("defaults=%+v", settings)
	}
	if settings.ChannelStickiness == nil || !settings.ChannelStickiness.Enabled || settings.ChannelStickiness.TTLSeconds != 3600 {
		t.Fatalf("stickiness defaults=%+v", settings.ChannelStickiness)
	}
	// A single global change applies to dozens of existing services.
	for i := 0; i < 32; i++ {
		service := contract.Service{ID: contract.ServiceID(fmt.Sprintf("service_%02d", i)), Name: "Inherited", Kind: contract.ServiceKindOpenAI, Enabled: true, Models: []string{"public"}, HTTP: &contract.HTTPConnection{BaseURL: "https://example.test", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true}}}
		if _, err := store.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := endpoint.NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	request := endpoint.ResolveRequest{Protocol: contract.ProtocolOpenAIChat, Model: "public"}
	original, err := resolver.ResolveCandidates(ctx, request)
	if err != nil || len(original) != 32 {
		t.Fatalf("default unmatched candidates=%d %v", len(original), err)
	}
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", `{"allow_unmatched_failover":false}`, "")
	if response.Code != 200 {
		t.Fatalf("disable failover: %d %s", response.Code, response.Body.String())
	}
	disabled, err := resolver.ResolveCandidates(ctx, request)
	if err != nil || len(disabled) != 1 {
		t.Fatalf("disabled failover candidates=%d %v", len(disabled), err)
	}
	settings.AllowUnmatchedFailover = true
	settings.Strategy = contract.FailoverOnly
	settings.MaxAttempts = 12
	settings.DefaultFailurePolicy.MaxRetries = 3
	encoded, _ := json.Marshal(settings)
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", string(encoded), "")
	if response.Code != 200 {
		t.Fatalf("PATCH: %d %s", response.Code, response.Body.String())
	}
	candidates, err := resolver.ResolveCandidates(ctx, request)
	if err != nil || len(candidates) != 32 {
		t.Fatalf("new candidates=%d %v", len(candidates), err)
	}
	for _, candidate := range candidates {
		if candidate.FailurePolicy.MaxRetries != 3 || candidate.Failover.MaxAttempts != 12 || candidate.Failover.Strategy != contract.FailoverOnly {
			t.Fatalf("not inherited: %+v", candidate)
		}
	}
	if original[0].FailurePolicy.MaxRetries != 1 || original[0].Failover.MaxAttempts != 6 {
		t.Fatal("in-flight snapshot changed")
	}
	record, err := store.GetService(ctx, "service_00")
	if err != nil {
		t.Fatal(err)
	}
	if record.Service.FailurePolicy != nil {
		t.Fatal("global change rewrote service")
	}
	exception := contract.DefaultFailurePolicy()
	exception.MaxRetries = 0
	body, _ := json.Marshal(map[string]any{"failure_policy": exception})
	response = controlRequest(t, handler, http.MethodPatch, ServicesPath+"/service_00", "application/merge-patch+json", string(body), record.ETag)
	if response.Code != 200 {
		t.Fatalf("service override: %d %s", response.Code, response.Body.String())
	}
	candidates, err = resolver.ResolveCandidates(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if candidates[0].CanonicalService().FailurePolicy.MaxRetries != 0 {
		t.Fatal("service exception lost")
	}
	response = controlRequest(t, handler, http.MethodPatch, ServicesPath+"/service_00", "application/merge-patch+json", `{"failure_policy":null}`, response.Header().Get("ETag"))
	if response.Code != 200 {
		t.Fatalf("clear service: %d %s", response.Code, response.Body.String())
	}
	candidates, err = resolver.ResolveCandidates(ctx, request)
	if err != nil || candidates[0].FailurePolicy.MaxRetries != 3 {
		t.Fatal("service did not resume global inheritance")
	}

}

func TestRoutingSettingsRejectInvalidPolicy(t *testing.T) {
	_, handler := newRouteHandler(t)
	for _, body := range []string{`{}`, `{"codex_identity_enforcement":null}`, `{"codex_identity_enforcement":"false"}`, `{"max_attempts":0}`, `{"max_attempts":21}`, `{"allow_unmatched_failover":null}`, `{"strategy":"random"}`, `{"default_failure_policy":{"max_retries":2}}`, `{"unknown":true}`} {
		response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", body, "")
		if response.Code != 422 {
			t.Fatalf("%s => %d %s", body, response.Code, response.Body.String())
		}
	}
}

type identityTestTokens struct{}

func (identityTestTokens) AccessToken(context.Context, contract.ServiceID) (accountauth.AccountTokens, error) {
	return accountauth.AccountTokens{AccessToken: "upstream-test-token"}, nil
}

func TestCodexIdentitySettingAppliesToExistingAuthorizerImmediately(t *testing.T) {
	store, handler := newRouteHandler(t)
	authorizer := endpoint.NewServiceAuthorizer(nil, identityTestTokens{}).WithRoutingSettings(store)
	client := make(http.Header)
	client.Set("User-Agent", "codex_cli_rs/0.156.0 (Mac OS; arm64)")
	client.Set("originator", "astrlink")
	client.Set("version", "0.100.0")
	check := func(enforced bool) {
		t.Helper()
		headers, err := authorizer.Headers(context.Background(), contract.Endpoint{ID: "service_codex", Kind: contract.ServiceKindCodexSubscription}, client)
		if err != nil {
			t.Fatal(err)
		}
		wantUA, wantOrigin, wantVersion := accountauth.CodexUserAgent(""), accountauth.DefaultCodexOriginator, accountauth.DefaultCodexModelsClientVersion
		if !enforced {
			wantUA, wantOrigin, wantVersion = client.Get("User-Agent"), "codex_cli_rs", "0.156.0"
		}
		if headers.Get("User-Agent") != wantUA || headers.Get("originator") != wantOrigin || headers.Get("version") != wantVersion {
			t.Fatalf("identity setting was not applied: %q %q %q", headers.Get("User-Agent"), headers.Get("originator"), headers.Get("version"))
		}
	}
	check(true)
	for _, enabled := range []bool{false, true} {
		response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", fmt.Sprintf(`{"codex_identity_enforcement":%t}`, enabled), "")
		if response.Code != http.StatusOK {
			t.Fatalf("save identity setting: %d %s", response.Code, response.Body.String())
		}
		check(enabled)
	}
}

func TestSubscriptionIdentitySettingsAreIndependentAndApplyImmediately(t *testing.T) {
	store, handler := newRouteHandler(t)
	authorizer := endpoint.NewServiceAuthorizer(nil, identityTestTokens{}).WithRoutingSettings(store)
	providers := []struct {
		key                 string
		kind                contract.ServiceKind
		clientUA, defaultUA string
	}{
		{"codex_identity_enforcement", contract.ServiceKindCodexSubscription, "codex_cli_rs/0.156.0", accountauth.CodexUserAgent("")},
		{"claude_identity_enforcement", contract.ServiceKindClaudeSubscription, "claude-cli/2.2.0 (external, cli)", accountauth.DefaultClaudeUserAgent},
		{"grok_identity_enforcement", contract.ServiceKindGrokSubscription, "xai-grok-workspace/0.2.102", "xai-grok-workspace/" + accountauth.DefaultGrokCLIClientVersion},
	}
	for _, changed := range providers {
		for _, invalid := range []string{"null", `"false"`, "0"} {
			response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", fmt.Sprintf(`{%q:%s}`, changed.key, invalid), "")
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("invalid %s accepted", changed.key)
			}
		}
		for _, enabled := range []bool{false, true} {
			response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", fmt.Sprintf(`{%q:%t}`, changed.key, enabled), "")
			if response.Code != http.StatusOK {
				t.Fatalf("PATCH %s: %d %s", changed.key, response.Code, response.Body.String())
			}
			for _, provider := range providers {
				client := make(http.Header)
				client.Set("User-Agent", provider.clientUA)
				headers, err := authorizer.Headers(context.Background(), contract.Endpoint{ID: "service_identity", Kind: provider.kind}, client)
				if err != nil {
					t.Fatal(err)
				}
				want := provider.defaultUA
				if changed.key == provider.key && !enabled {
					want = provider.clientUA
				}
				if headers.Get("User-Agent") != want {
					t.Fatalf("changing %s=%t affected %s: got %q want %q", changed.key, enabled, provider.key, headers.Get("User-Agent"), want)
				}
			}
		}
	}
}
