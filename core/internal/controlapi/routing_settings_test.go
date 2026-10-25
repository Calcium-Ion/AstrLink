package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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

func TestRoutingSettingsModelRedirectsReplaceAndPersist(t *testing.T) {
	store, handler := newRouteHandler(t)
	ctx := context.Background()
	response := controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"model_redirects":[]`) {
		t.Fatalf("GET defaults: %d %s", response.Code, response.Body.String())
	}
	patch := `{"model_redirects":[{"from":"gpt-4o","to":"gpt-4.1","enabled":true},{"from":"astrlink/auto","to":"gpt-4.1-mini","enabled":false}]}`
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", patch, "")
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", response.Code, response.Body.String())
	}
	want := []contract.ModelRedirect{
		{From: "gpt-4o", To: "gpt-4.1", Enabled: true},
		{From: contract.AstrLinkAutoModelID, To: "gpt-4.1-mini", Enabled: false},
	}
	var patched contract.RoutingSettings
	decode(t, response, &patched)
	if !reflect.DeepEqual(patched.ModelRedirects, want) || patched.MaxAttempts != 6 {
		t.Fatalf("PATCH response = %+v", patched)
	}
	stored, err := store.GetRoutingSettings(ctx)
	if err != nil || !reflect.DeepEqual(stored.ModelRedirects, want) {
		t.Fatalf("stored = %+v %v", stored.ModelRedirects, err)
	}
	// A merge patch replaces the array instead of appending to it.
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", `{"model_redirects":[{"from":"claude-old","to":"claude-new","enabled":true}]}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", response.Code, response.Body.String())
	}
	response = controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
	var fetched contract.RoutingSettings
	decode(t, response, &fetched)
	if !reflect.DeepEqual(fetched.ModelRedirects, []contract.ModelRedirect{{From: "claude-old", To: "claude-new", Enabled: true}}) {
		t.Fatalf("replaced = %+v", fetched.ModelRedirects)
	}
	// Other keys leave the table untouched.
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", `{"max_attempts":3}`, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"model_redirects":[{"from":"claude-old","to":"claude-new","enabled":true}]`) {
		t.Fatalf("unrelated PATCH: %d %s", response.Code, response.Body.String())
	}
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", `{"model_redirects":[]}`, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"model_redirects":[]`) {
		t.Fatalf("clear: %d %s", response.Code, response.Body.String())
	}
	stored, err = store.GetRoutingSettings(ctx)
	if err != nil || stored.ModelRedirects == nil || len(stored.ModelRedirects) != 0 || stored.MaxAttempts != 3 {
		t.Fatalf("cleared = %+v %v", stored, err)
	}
}

func TestRoutingSettingsRejectInvalidModelRedirects(t *testing.T) {
	store, handler := newRouteHandler(t)
	valid := `{"model_redirects":[{"from":"gpt-4o","to":"gpt-4.1","enabled":true}]}`
	if response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", valid, ""); response.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", response.Code, response.Body.String())
	}
	for _, body := range []string{
		`{"model_redirects":null}`,
		`{"model_redirects":{}}`,
		`{"model_redirects":"gpt-4o"}`,
		`{"model_redirects":[null]}`,
		`{"model_redirects":[{"from":"a","to":"b","enabled":true},{"from":"a","to":"c","enabled":false}]}`,
		`{"model_redirects":[{"from":"a","to":"b","enabled":true},{"from":"b","to":"c","enabled":true}]}`,
		`{"model_redirects":[{"from":"same","to":"same","enabled":true}]}`,
		`{"model_redirects":[{"from":"client","to":"astrlink/auto","enabled":true}]}`,
		`{"model_redirects":[{"from":"client","to":"target","enabled":true,"id":"rule_1"}]}`,
		`{"model_redirects":[{"from":"client","to":"target"}]}`,
		`{"model_redirects":[{"from":"","to":"target","enabled":true}]}`,
		`{"model_redirects":[{"from":"client ","to":"target","enabled":true}]}`,
	} {
		response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", body, "")
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s => %d %s", body, response.Code, response.Body.String())
		}
	}
	rules := make([]contract.ModelRedirect, contract.MaxModelRedirects+1)
	for i := range rules {
		rules[i] = contract.ModelRedirect{From: fmt.Sprintf("client-%03d", i), To: "target", Enabled: true}
	}
	encoded, _ := json.Marshal(map[string]any{"model_redirects": rules})
	if response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", string(encoded), ""); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over limit => %d %s", response.Code, response.Body.String())
	}
	stored, err := store.GetRoutingSettings(context.Background())
	if err != nil || !reflect.DeepEqual(stored.ModelRedirects, []contract.ModelRedirect{{From: "gpt-4o", To: "gpt-4.1", Enabled: true}}) {
		t.Fatalf("rejected patches changed the table: %+v %v", stored.ModelRedirects, err)
	}
}

// nilRedirectRoutingStore simulates a store that returns a nil table.
type nilRedirectRoutingStore struct {
	storage.RoutingSettingsStore
}

func (store nilRedirectRoutingStore) GetRoutingSettings(ctx context.Context) (contract.RoutingSettings, error) {
	settings, err := store.RoutingSettingsStore.GetRoutingSettings(ctx)
	settings.ModelRedirects = nil
	return settings, err
}

func TestRoutingSettingsNeverEmitNullModelRedirects(t *testing.T) {
	store, handler := newRouteHandler(t)
	handler.routingSettings = nilRedirectRoutingStore{store}
	response := controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"model_redirects":[]`) {
		t.Fatalf("GET: %d %s", response.Code, response.Body.String())
	}
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", `{"max_attempts":4}`, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"model_redirects":[]`) {
		t.Fatalf("PATCH: %d %s", response.Code, response.Body.String())
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
		{"grok_identity_enforcement", contract.ServiceKindGrokSubscription, "xai-grok-workspace/0.2.102", accountauth.DefaultGrokIdentity().UserAgent},
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

func TestSubscriptionProtectionSettingsDefaultOnAndPatchIndependently(t *testing.T) {
	_, handler := newRouteHandler(t)
	keys := []string{"subscription_risk_protection", "codex_request_normalization", "claude_request_normalization", "subscription_session_isolation"}
	read := func() map[string]any {
		t.Helper()
		response := controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
		var settings map[string]any
		decode(t, response, &settings)
		return settings
	}
	defaults := read()
	for _, key := range keys {
		if defaults[key] != true {
			t.Fatalf("%s defaults to %v", key, defaults[key])
		}
	}
	for _, changed := range keys {
		for _, invalid := range []string{"null", `"false"`, "0"} {
			response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", fmt.Sprintf(`{%q:%s}`, changed, invalid), "")
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("invalid %s accepted", changed)
			}
		}
		for _, enabled := range []bool{false, true} {
			response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", fmt.Sprintf(`{%q:%t}`, changed, enabled), "")
			if response.Code != http.StatusOK {
				t.Fatalf("PATCH %s: %d %s", changed, response.Code, response.Body.String())
			}
			settings := read()
			for _, key := range keys {
				if want := key != changed || enabled; settings[key] != want {
					t.Fatalf("changing %s=%t left %s=%v", changed, enabled, key, settings[key])
				}
			}
		}
	}
}

func TestClientIdentityLearningSettingsPatchAndApplyImmediately(t *testing.T) {
	store, handler := newRouteHandler(t)
	authorizer := endpoint.NewServiceAuthorizer(nil, identityTestTokens{}).
		WithRoutingSettings(store).WithIdentities(accountauth.NewIdentityRegistry(store, nil))
	read := func() map[string]any {
		t.Helper()
		response := controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
		var settings map[string]any
		decode(t, response, &settings)
		return settings
	}
	patch := func(body string) int {
		t.Helper()
		return controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", body, "").Code
	}
	outgoing := func(kind contract.ServiceKind) http.Header {
		t.Helper()
		headers, err := authorizer.Headers(context.Background(), contract.Endpoint{ID: "service_identity", Kind: kind}, http.Header{})
		if err != nil {
			t.Fatal(err)
		}
		return headers
	}

	defaults := read()
	for _, key := range []string{"claude_identity_auto_learn", "codex_identity_auto_learn", "grok_identity_auto_learn"} {
		if defaults[key] != true {
			t.Fatalf("%s defaults to %v", key, defaults[key])
		}
	}
	for _, key := range []string{"claude_identity_version", "codex_identity_version", "grok_identity_version"} {
		if _, present := defaults[key]; present {
			t.Fatalf("%s present without an override: %v", key, defaults[key])
		}
	}

	learnKeys := []string{"claude_identity_auto_learn", "codex_identity_auto_learn", "grok_identity_auto_learn"}
	for _, changed := range learnKeys {
		for _, invalid := range []string{"null", `"false"`, "0"} {
			if code := patch(fmt.Sprintf(`{%q:%s}`, changed, invalid)); code != http.StatusUnprocessableEntity {
				t.Fatalf("invalid %s=%s returned %d", changed, invalid, code)
			}
		}
		for _, enabled := range []bool{false, true} {
			if code := patch(fmt.Sprintf(`{%q:%t}`, changed, enabled)); code != http.StatusOK {
				t.Fatalf("PATCH %s=%t returned %d", changed, enabled, code)
			}
			settings := read()
			for _, key := range learnKeys {
				if want := key != changed || enabled; settings[key] != want {
					t.Fatalf("changing %s=%t left %s=%v", changed, enabled, key, settings[key])
				}
			}
		}
	}

	for _, test := range []struct {
		key, invalid string
	}{
		{"claude_identity_version", "null"},
		{"claude_identity_version", "2"},
		{"claude_identity_version", `"2.1"`},
		{"claude_identity_version", `"v2.1.400"`},
		{"claude_identity_version", `"claude-cli/2.1.400"`},
		{"codex_identity_version", "null"},
		{"grok_identity_version", "null"},
		{"grok_identity_version", `"1.0"`},
		{"codex_identity_version", `"0.143.9"`},
		{"codex_identity_version", `"0.170"`},
	} {
		if code := patch(fmt.Sprintf(`{%q:%s}`, test.key, test.invalid)); code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid %s=%s returned %d", test.key, test.invalid, code)
		}
	}
	if code := patch(`{"claude_identity_version":"2.1.400","codex_identity_version":"0.170.0","grok_identity_version":"1.0.50"}`); code != http.StatusOK {
		t.Fatalf("PATCH versions returned %d", code)
	}
	settings := read()
	if settings["claude_identity_version"] != "2.1.400" || settings["codex_identity_version"] != "0.170.0" {
		t.Fatalf("versions = %v, %v", settings["claude_identity_version"], settings["codex_identity_version"])
	}
	if got := outgoing(contract.ServiceKindClaudeSubscription).Get("User-Agent"); got != "claude-cli/2.1.400 (external, cli)" {
		t.Fatalf("Claude User-Agent = %q", got)
	}
	grok := outgoing(contract.ServiceKindGrokSubscription)
	if !strings.HasPrefix(grok.Get("User-Agent"), "grok-shell/1.0.50 (") || grok.Get("X-Grok-Client-Version") != "1.0.50" {
		t.Fatalf("Grok identity = %q, version %q", grok.Get("User-Agent"), grok.Get("X-Grok-Client-Version"))
	}
	codex := outgoing(contract.ServiceKindCodexSubscription)
	if !strings.HasPrefix(codex.Get("User-Agent"), "codex-tui/0.170.0 ") || codex.Get("version") != "0.170.0" {
		t.Fatalf("Codex identity = %q, version %q", codex.Get("User-Agent"), codex.Get("version"))
	}

	// A rejected patch leaves the override in place; an empty string clears it.
	if code := patch(`{"claude_identity_version":"2.1.500","codex_identity_version":"0.1.0"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("partially invalid PATCH returned %d", code)
	}
	if settings := read(); settings["claude_identity_version"] != "2.1.400" {
		t.Fatalf("rejected PATCH changed claude_identity_version to %v", settings["claude_identity_version"])
	}
	if code := patch(`{"claude_identity_version":"","codex_identity_version":"","grok_identity_version":""}`); code != http.StatusOK {
		t.Fatalf("clearing versions returned %d", code)
	}
	settings = read()
	for _, key := range []string{"claude_identity_version", "codex_identity_version", "grok_identity_version"} {
		if _, present := settings[key]; present {
			t.Fatalf("%s not cleared: %v", key, settings[key])
		}
	}
	if got := outgoing(contract.ServiceKindClaudeSubscription).Get("User-Agent"); got != accountauth.DefaultClaudeUserAgent {
		t.Fatalf("Claude User-Agent after clearing = %q", got)
	}
	if got := outgoing(contract.ServiceKindCodexSubscription).Get("version"); got != accountauth.DefaultCodexModelsClientVersion {
		t.Fatalf("Codex version after clearing = %q", got)
	}
}

func TestRoutingSettingsPatchAcceptsDesktopModelRoutingGroupShape(t *testing.T) {
	store, handler := newRouteHandler(t)
	service := contract.Service{
		ID:      "service_01",
		Name:    "Model routing test service",
		Kind:    contract.ServiceKindOpenAI,
		Enabled: true,
		Models:  []string{"claude-haiku", "claude-sonnet"},
		HTTP: &contract.HTTPConnection{
			BaseURL: "https://example.test",
			Auth:    contract.ServiceAuth{Scheme: contract.AuthSchemeNone},
		},
		Capabilities: []contract.Capability{{
			Protocol:  contract.ProtocolOpenAIChat,
			Mode:      contract.CapabilityModeNative,
			Streaming: true,
		}},
	}
	if _, err := store.CreateService(context.Background(), service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	body := `{"model_routing_groups":[{"id":"superclaude","enabled":true,"members":[{"id":"fast","service_id":"service_01","model":"claude-haiku"},{"id":"strong","service_id":"service_01","model":"claude-sonnet"}],"default_member_id":"fast","rules":[{"id":"coding-streaming","priority":100,"when":{"all":[{"field":"task_type","in":["coding"]},{"field":"streaming","equals_bool":true}]},"target_member_id":"strong"}]}]}`
	response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", body, "")
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH model_routing_groups: %d %s", response.Code, response.Body.String())
	}
	var settings contract.RoutingSettings
	decode(t, response, &settings)
	if err := settings.Validate(); err != nil {
		t.Fatalf("PATCH response failed RoutingSettings.Validate: %v", err)
	}
	if len(settings.ModelRoutingGroups) != 1 || settings.ModelRoutingGroups[0].Rules[0].When.All == nil || len(settings.ModelRoutingGroups[0].Rules[0].When.All) != 2 {
		t.Fatalf("saved model_routing_groups = %+v", settings.ModelRoutingGroups)
	}

	getResponse := controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET after model_routing_groups PATCH: %d %s", getResponse.Code, getResponse.Body.String())
	}
	var persisted contract.RoutingSettings
	decode(t, getResponse, &persisted)
	if got := persisted.ModelRoutingGroups[0].DefaultMemberID; got != "fast" {
		t.Fatalf("persisted default_member_id = %q", got)
	}

	customBody := `{"model_routing_groups":[{"id":"superclaude","enabled":true,"task_type_labels":["general","custom-review"],"members":[{"id":"fast","service_id":"service_01","model":"claude-haiku"},{"id":"strong","service_id":"service_01","model":"claude-sonnet"}],"default_member_id":"fast","rules":[{"id":"custom-review","priority":100,"when":{"field":"task_type","in":["custom-review"]},"target_member_id":"strong"}]}]}`
	customResponse := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", customBody, "")
	if customResponse.Code != http.StatusOK {
		t.Fatalf("PATCH custom task_type_labels: %d %s", customResponse.Code, customResponse.Body.String())
	}
	getResponse = controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET after custom task_type_labels PATCH: %d %s", getResponse.Code, getResponse.Body.String())
	}
	decode(t, getResponse, &persisted)
	if got := persisted.ModelRoutingGroups[0].TaskTypeLabels; !reflect.DeepEqual(got, []string{"general", "custom-review"}) {
		t.Fatalf("persisted task_type_labels = %v", got)
	}

	invalidBody := strings.Replace(customBody, `"task_type_labels":["general","custom-review"]`, `"task_type_labels":["general"]`, 1)
	invalidResponse := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", invalidBody, "")
	if invalidResponse.Code != http.StatusUnprocessableEntity || !strings.Contains(invalidResponse.Body.String(), "allowed labels") {
		t.Fatalf("invalid custom task_type label response: %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestRoutingSettingsThinkingAndPromptRoundTrip(t *testing.T) {
	store, handler := newRouteHandler(t)
	response := controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"system_prompt_enabled":false,"system_prompt_prefix":"","system_prompt_suffix":"",`+
			`"reasoning_mode":"passthrough","reasoning_level":"medium","adaptive_preset":"everyday","adaptive_min":"low","adaptive_max":"max"`) {
		t.Fatalf("GET defaults: %d %s", response.Code, response.Body.String())
	}
	prefix := "Answer in English.\n\n  Keep it short.  "
	patch, _ := json.Marshal(map[string]any{
		"system_prompt_enabled": true, "system_prompt_prefix": prefix, "system_prompt_suffix": "Check twice.",
		"reasoning_mode": "adaptive", "reasoning_level": "max",
	})
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", string(patch), "")
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", response.Code, response.Body.String())
	}
	stored, err := store.GetRoutingSettings(context.Background())
	if err != nil || !stored.SystemPromptEnabled || stored.SystemPromptPrefix != prefix ||
		stored.SystemPromptSuffix != "Check twice." || stored.ReasoningMode != contract.ReasoningModeAdaptive ||
		stored.ReasoningLevel != contract.ReasoningLevelMax {
		t.Fatalf("stored = %+v %v", stored, err)
	}
	// Other keys leave the prompt and level as they are.
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", `{"reasoning_mode":"fixed"}`, "")
	var patched contract.RoutingSettings
	decode(t, response, &patched)
	if response.Code != http.StatusOK || patched.ReasoningMode != contract.ReasoningModeFixed || patched.ReasoningLevel != contract.ReasoningLevelMax ||
		patched.SystemPromptPrefix != prefix || !patched.SystemPromptEnabled {
		t.Fatalf("fixed mode: %d %+v", response.Code, patched)
	}
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", `{"reasoning_level":"off"}`, "")
	decode(t, response, &patched)
	if response.Code != http.StatusOK || patched.ReasoningMode != contract.ReasoningModeFixed || patched.ReasoningLevel != contract.ReasoningLevelOff {
		t.Fatalf("level off: %d %+v", response.Code, patched)
	}
	for _, invalid := range []string{
		`{"reasoning_override":"max"}`,
		`{"reasoning_mode":""}`,
		// The modes were renamed before release.
		`{"reasoning_mode":"follow"}`,
		`{"reasoning_mode":"auto"}`,
		`{"reasoning_mode":null}`,
		`{"reasoning_level":"xhigh"}`,
		`{"reasoning_level":""}`,
		`{"reasoning_level":"none"}`,
		`{"reasoning_level":null}`,
		`{"system_prompt_enabled":"yes"}`,
		`{"system_prompt_prefix":` + string(must(json.Marshal(strings.Repeat("字", contract.MaxSystemPromptRunes+1)))) + `}`,
		`{"adaptive_preset":"daily"}`,
		`{"adaptive_preset":""}`,
		`{"adaptive_preset":null}`,
		`{"adaptive_preset":"Everyday"}`,
		`{"adaptive_min":"xhigh"}`,
		`{"adaptive_min":""}`,
		`{"adaptive_min":null}`,
		`{"adaptive_max":"none"}`,
		`{"adaptive_max":1}`,
	} {
		response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", invalid, "")
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%.60s accepted: %d %s", invalid, response.Code, response.Body.String())
		}
	}
	atLimit, _ := json.Marshal(map[string]string{"system_prompt_suffix": strings.Repeat("字", contract.MaxSystemPromptRunes)})
	if response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", string(atLimit), ""); response.Code != http.StatusOK {
		t.Fatalf("prompt at the limit: %d %s", response.Code, response.Body.String())
	}
}

func TestRoutingSettingsImageCompressionRoundTrip(t *testing.T) {
	store, handler := newRouteHandler(t)
	response := controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"image_compression":{"enabled":false,"mode":"lossless","quality":85}`) {
		t.Fatalf("GET defaults: %d %s", response.Code, response.Body.String())
	}
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json",
		`{"image_compression":{"enabled":true,"mode":"lossy","quality":70}}`, "")
	want := contract.ImageCompression{Enabled: true, Mode: contract.ImageCompressionLossy, Quality: 70}
	var patched contract.RoutingSettings
	decode(t, response, &patched)
	if response.Code != http.StatusOK || patched.ImageCompression != want {
		t.Fatalf("PATCH: %d %+v", response.Code, patched.ImageCompression)
	}
	for _, invalid := range []string{
		`{"image_compression":null}`,
		`{"image_compression":true}`,
		`{"image_compression":{"enabled":true}}`,
		`{"image_compression":{"enabled":true,"mode":"lossless"}}`,
		`{"image_compression":{"enabled":true,"mode":"lossless","quality":85,"max_side":2048}}`,
		`{"image_compression":{"enabled":null,"mode":"lossless","quality":85}}`,
		`{"image_compression":{"enabled":true,"mode":"webp","quality":85}}`,
		`{"image_compression":{"enabled":true,"mode":"","quality":85}}`,
		`{"image_compression":{"enabled":true,"mode":"lossy","quality":0}}`,
		`{"image_compression":{"enabled":true,"mode":"lossy","quality":101}}`,
		`{"image_compression":{"enabled":true,"mode":"lossy","quality":"80"}}`,
		`{"image_compression":{"enabled":true,"mode":"lossy","quality":80.5}}`,
	} {
		response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", invalid, "")
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s accepted: %d %s", invalid, response.Code, response.Body.String())
		}
	}
	if stored, err := store.GetRoutingSettings(context.Background()); err != nil || stored.ImageCompression != want {
		t.Fatalf("stored after rejected patches = %+v %v", stored.ImageCompression, err)
	}
}

func TestRoutingSettingsAdaptiveRangeRoundTrip(t *testing.T) {
	store, handler := newRouteHandler(t)
	patch := func(body string) (*httptest.ResponseRecorder, contract.RoutingSettings) {
		t.Helper()
		response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", body, "")
		var patched contract.RoutingSettings
		if response.Code == http.StatusOK {
			decode(t, response, &patched)
		}
		return response, patched
	}
	response, patched := patch(`{"reasoning_mode":"adaptive","adaptive_preset":"custom","adaptive_min":"medium","adaptive_max":"high"}`)
	if response.Code != http.StatusOK || patched.ReasoningMode != contract.ReasoningModeAdaptive || patched.AdaptivePreset != contract.AdaptivePresetCustom ||
		patched.AdaptiveMin != contract.ReasoningLevelMedium || patched.AdaptiveMax != contract.ReasoningLevelHigh {
		t.Fatalf("custom range: %d %+v", response.Code, patched)
	}
	// Each end is checked against the stored other end, and a rejected
	// patch changes nothing.
	for _, invalid := range []string{
		`{"adaptive_min":"max"}`,
		`{"adaptive_max":"low"}`,
		`{"adaptive_min":"high","adaptive_max":"low"}`,
		`{"adaptive_max":"off","reasoning_mode":"fixed"}`,
		// The range is kept, and checked, under the other presets too.
		`{"adaptive_preset":"everyday","adaptive_min":"max","adaptive_max":"low"}`,
	} {
		if response, _ := patch(invalid); response.Code != http.StatusUnprocessableEntity ||
			!strings.Contains(response.Body.String(), "adaptive_min must be at most adaptive_max") {
			t.Fatalf("%s accepted: %d %s", invalid, response.Code, response.Body.String())
		}
	}
	if stored, err := store.GetRoutingSettings(context.Background()); err != nil || stored.ReasoningMode != contract.ReasoningModeAdaptive ||
		stored.AdaptiveMin != contract.ReasoningLevelMedium || stored.AdaptiveMax != contract.ReasoningLevelHigh {
		t.Fatalf("stored after rejected patches = %+v %v", stored, err)
	}
	// Moving both ends in one patch passes through an order the ends alone
	// would reject.
	if response, patched := patch(`{"adaptive_min":"max","adaptive_max":"max"}`); response.Code != http.StatusOK ||
		patched.AdaptiveMin != contract.ReasoningLevelMax || patched.AdaptiveMax != contract.ReasoningLevelMax {
		t.Fatalf("one-level range: %d %+v", response.Code, patched)
	}
	// The preset and the range survive a switch to fixed and back.
	if response, patched := patch(`{"reasoning_mode":"fixed","reasoning_level":"low"}`); response.Code != http.StatusOK ||
		patched.AdaptivePreset != contract.AdaptivePresetCustom || patched.AdaptiveMin != contract.ReasoningLevelMax {
		t.Fatalf("fixed: %d %+v", response.Code, patched)
	}
	if response, patched := patch(`{"reasoning_mode":"adaptive"}`); response.Code != http.StatusOK || patched.ReasoningLevel != contract.ReasoningLevelLow ||
		patched.AdaptivePreset != contract.AdaptivePresetCustom || patched.AdaptiveMin != contract.ReasoningLevelMax || patched.AdaptiveMax != contract.ReasoningLevelMax {
		t.Fatalf("back to adaptive: %d %+v", response.Code, patched)
	}
	if response, patched := patch(`{"adaptive_preset":"research"}`); response.Code != http.StatusOK || patched.AdaptivePreset != contract.AdaptivePresetResearch ||
		patched.AdaptiveMin != contract.ReasoningLevelMax {
		t.Fatalf("research: %d %+v", response.Code, patched)
	}
}

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}
