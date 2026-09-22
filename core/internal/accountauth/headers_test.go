package accountauth

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestCodexForwardIdentityPolicy(t *testing.T) {
	const clientUA = "codex_cli_rs/0.156.0 (Mac OS; arm64) Terminal/1.0"
	for _, test := range []struct {
		name, userAgent, version, configuredVersion string
		disabled                                    bool
		wantUA, wantOriginator, wantVersion         string
	}{
		{name: "zero value defaults to enforced identity", userAgent: clientUA, version: "0.100.0"},
		{name: "missing client identity"},
		{name: "canonical configured version", configuredVersion: "0.157.0", wantVersion: "0.157.0"},
		{name: "invalid configured version", configuredVersion: "0.157.0 astrlink"},
		{name: "old configured version", configuredVersion: "0.100.0"},
		{name: "disabled preserves full UA and pairs fields", disabled: true, userAgent: clientUA, version: "0.100.0", wantUA: clientUA, wantOriginator: "codex_cli_rs", wantVersion: "0.156.0"},
		{name: "disabled preserves TUI", disabled: true, userAgent: "codex-tui/0.156.0", wantUA: "codex-tui/0.156.0", wantOriginator: "codex-tui", wantVersion: "0.156.0"},
		{name: "disabled preserves IDE", disabled: true, userAgent: "codex_vscode/0.156.0 (Mac OS; arm64)", wantUA: "codex_vscode/0.156.0 (Mac OS; arm64)", wantOriginator: "codex_vscode", wantVersion: "0.156.0"},
		{name: "disabled unknown client falls back", disabled: true, userAgent: "astrlink/0.156.0", version: "0.156.0"},
		{name: "disabled old client falls back", disabled: true, userAgent: "codex_cli_rs/0.99.0"},
		{name: "disabled malformed version falls back", disabled: true, userAgent: "codex_cli_rs/0.156.0/other"},
		{name: "disabled prefix lookalike falls back", disabled: true, userAgent: "codex_cli_rs_other/0.156.0"},
		{name: "disabled control bytes fall back", disabled: true, userAgent: clientUA + "\r\nOriginator: astrlink"},
		{name: "disabled oversized UA falls back", disabled: true, userAgent: clientUA + strings.Repeat("x", 1024)},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := make(http.Header)
			client.Set("User-Agent", test.userAgent)
			client.Set("version", test.version)
			client.Set("originator", "astrlink")
			original := client.Clone()
			headers := make(http.Header)
			ApplyCodexForwardHeaders(headers, AccountTokens{AccessToken: "access", AccountID: "acct_1"}, client, CodexIdentityPolicy{
				DisableEnforcement: test.disabled, ClientVersion: test.configuredVersion,
			})
			version := test.wantVersion
			if version == "" {
				version = DefaultCodexModelsClientVersion
			}
			ua, originator := test.wantUA, test.wantOriginator
			if ua == "" {
				ua = "codex-tui/" + version + " (Ubuntu 22.4.0; x86_64) xterm-256color"
				originator = "codex-tui"
			}
			for name, want := range map[string]string{
				"User-Agent": ua, "originator": originator, "version": version,
				"Authorization": "Bearer access", "ChatGPT-Account-ID": "acct_1",
				"OAI-Product-Sku": "codex", "Accept": "",
			} {
				if got := headers.Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			if !reflect.DeepEqual(client, original) {
				t.Fatal("client headers were mutated")
			}
		})
	}
}

func TestCodexAuthPreservesAccept(t *testing.T) {
	for _, accept := range [][]string{nil, {"application/json"}, {"text/event-stream"}, {"text/event-stream", "application/json;q=0.5"}} {
		for name, apply := range map[string]func(http.Header){
			"API": func(headers http.Header) {
				ApplyCodexAPIHeaders(headers, AccountTokens{AccessToken: "access"}, "")
			},
			"forward": func(headers http.Header) {
				ApplyCodexForwardHeaders(headers, AccountTokens{AccessToken: "access"}, nil, CodexIdentityPolicy{})
			},
		} {
			t.Run(name+"/"+strings.Join(accept, ","), func(t *testing.T) {
				headers := make(http.Header)
				for _, value := range accept {
					headers.Add("Accept", value)
				}
				apply(headers)
				if got := headers.Values("Accept"); !reflect.DeepEqual(got, accept) {
					t.Fatalf("Accept = %v, want %v", got, accept)
				}
			})
		}
	}
}

func TestCodexIdentityRejectsInvalidAndOldVersions(t *testing.T) {
	for _, version := range []string{"", "0.143.99", "0.144.0-alpha.1", "0.155", "0.155.1/other", "0.155.1\r\n", "999999999999999999999.0.0"} {
		if validCodexVersion(version) {
			t.Errorf("accepted version %q", version)
		}
	}
	for _, version := range []string{"0.144.0", "0.144.1-alpha.1", "0.155.1", "1.0.0", "0.156.0-alpha.1+build.2"} {
		if !validCodexVersion(version) {
			t.Errorf("rejected version %q", version)
		}
	}
}

func TestApplyCodexAPIHeadersClearsStaleAccountID(t *testing.T) {
	headers := make(http.Header)
	headers.Set("ChatGPT-Account-ID", "stale-account")
	ApplyCodexAPIHeaders(headers, AccountTokens{AccessToken: "access"}, "0.156.0")
	if got := headers.Get("ChatGPT-Account-ID"); got != "" {
		t.Fatal("stale account ID was retained")
	}
	ApplyCodexAPIHeaders(nil, AccountTokens{}, "")
	ApplyCodexForwardHeaders(nil, AccountTokens{}, nil, CodexIdentityPolicy{DisableEnforcement: true})
}

func TestOtherSubscriptionForwardIdentity(t *testing.T) {
	for _, provider := range []struct {
		name, product, version, defaultUA string
		apply                             func(http.Header, AccountTokens, http.Header, bool)
	}{
		{"claude", "claude-cli", "2.2.0", DefaultClaudeUserAgent, ApplyClaudeForwardHeaders},
		{"grok", "xai-grok-workspace", "0.2.102", "xai-grok-workspace/" + DefaultGrokCLIClientVersion, ApplyGrokForwardHeaders},
	} {
		t.Run(provider.name, func(t *testing.T) {
			validUA := provider.product + "/" + provider.version + " (Mac OS; arm64)"
			for _, ua := range []string{validUA, "", "astrlink/1.0.0", provider.product + "/bad", provider.product + "/1.0.0\r\nInjected: value", strings.Repeat("x", 1025)} {
				for _, enforce := range []bool{true, false} {
					client := make(http.Header)
					client.Set("User-Agent", ua)
					client.Set("Authorization", "Bearer client-secret")
					client.Set("X-Grok-Client-Version", "0.0.1")
					client.Set("Anthropic-Version", "invalid")
					client.Add("Anthropic-Beta", "client-feature,oauth-2025-04-20")
					client.Add("Anthropic-Beta", "another-feature, client-feature")
					before := client.Clone()
					headers := make(http.Header)
					provider.apply(headers, AccountTokens{AccessToken: "upstream-token"}, client, enforce)
					wantUA := provider.defaultUA
					if !enforce && ua == validUA {
						wantUA = validUA
					}
					if headers.Get("User-Agent") != wantUA || headers.Get("Authorization") != "Bearer upstream-token" {
						t.Fatalf("enforce=%t ua=%q: unexpected identity or credential", enforce, ua)
					}
					if provider.name == "grok" {
						wantVersion := DefaultGrokCLIClientVersion
						if wantUA == validUA {
							wantVersion = provider.version
						}
						if headers.Get("X-Grok-Client-Version") != wantVersion || headers.Get("X-XAI-Token-Auth") != "xai-grok-cli" {
							t.Fatal("Grok identity is not paired")
						}
					} else {
						if headers.Get("Anthropic-Version") != "2023-06-01" || headers.Get("Anthropic-Beta") != "claude-code-20250219,oauth-2025-04-20,client-feature,another-feature" {
							t.Fatalf("lost or duplicated Claude protocol headers: %v", headers)
						}
					}
					if !reflect.DeepEqual(client, before) {
						t.Fatal("mutated client headers")
					}
				}
			}
			provider.apply(nil, AccountTokens{}, nil, false)
		})
	}
}
