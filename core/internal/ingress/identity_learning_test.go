package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
)

const (
	learnedClaudeUA    = "claude-cli/2.1.300 (external, sdk-cli)"
	learnedCodexUA     = "codex_cli_rs/0.160.0 (Mac OS 26.0.0; arm64) iTerm.app/3.6.1"
	officialClaudeBody = `{"model":"claude-sonnet-4-5","max_tokens":64,"top_k":5,` +
		`"metadata":{"user_id":"{\"device_id\":\"device-1\",\"account_uuid\":\"\",\"session_id\":\"session-1\"}"},` +
		`"messages":[{"role":"user","content":"hello"}]}`
	officialCodexBody      = `{"model":"gpt-6-astra","input":"hello","store":true,"stream":true}`
	identityClaudeResponse = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5",` +
		`"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
)

type memoryLearnedIdentities struct {
	mu        sync.Mutex
	documents map[contract.SubscriptionProvider][]byte
}

func (store *memoryLearnedIdentities) ListLearnedIdentities(context.Context) (map[contract.SubscriptionProvider][]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	documents := make(map[contract.SubscriptionProvider][]byte, len(store.documents))
	for provider, document := range store.documents {
		documents[provider] = append([]byte(nil), document...)
	}
	return documents, nil
}

func (store *memoryLearnedIdentities) PutLearnedIdentity(_ context.Context, provider contract.SubscriptionProvider, document []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.documents == nil {
		store.documents = make(map[contract.SubscriptionProvider][]byte)
	}
	store.documents[provider] = append([]byte(nil), document...)
	return nil
}

type identityHarness struct {
	settings *redirectSettingsStore
	registry *accountauth.IdentityRegistry
	handler  http.Handler
}

func newIdentityHarness(t *testing.T, settings contract.RoutingSettings, learned accountauth.LearnedIdentityStore, candidate endpoint.Resolved) *identityHarness {
	t.Helper()
	store := newRedirectSettingsStore()
	store.settings = settings
	registry := accountauth.NewIdentityRegistry(store, learned)
	if err := registry.Hydrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &identityHarness{settings: store, registry: registry, handler: NewWithDependencies(Dependencies{
		Resolver:         candidateResolver{candidates: []endpoint.Resolved{candidate}},
		Authorizer:       endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}).WithRoutingSettings(store).WithIdentities(registry),
		RequestRecords:   store,
		ConversionEngine: relaykitbridge.NewEngine(),
		Identities:       registry,
	})}
}

func (harness *identityHarness) serve(t *testing.T, path, body string, header http.Header) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	for name, values := range header {
		request.Header[name] = values
	}
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("%s response = %d %s", path, response.Code, response.Body.String())
	}
}

func officialClaudeHeaders(userAgent string) http.Header {
	return http.Header{
		"User-Agent":                  {userAgent},
		"X-App":                       {"cli"},
		"Anthropic-Beta":              {"claude-code-20250219,oauth-2025-04-20"},
		"Anthropic-Version":           {"2023-06-01"},
		"X-Claude-Code-Session-Id":    {"session-1"},
		"X-Stainless-Lang":            {"js"},
		"X-Stainless-Package-Version": {"0.95.1"},
		"X-Stainless-Os":              {"Linux"},
		"X-Stainless-Arch":            {"arm64"},
		"X-Stainless-Runtime":         {"node"},
		"X-Stainless-Runtime-Version": {"v24.9.0"},
		"X-Stainless-Retry-Count":     {"2"},
		"X-Stainless-Helper-Method":   {"stream"},
		"X-Astrlink-Debug":            {"local-only"},
	}
}

func officialCodexHeaders(userAgent, originator string) http.Header {
	return http.Header{
		"User-Agent": {userAgent},
		"Originator": {originator},
		"Session-Id": {"session-1"},
		"Accept":     {"text/event-stream"},
	}
}

// brandedClientHeaders is a third-party caller whose identity headers name the
// gateway; the subscription identity replaces all of them upstream.
func brandedClientHeaders(provider contract.SubscriptionProvider) http.Header {
	header := http.Header{
		"User-Agent":       {"AstrLink-Test/1.0"},
		"X-Astrlink-Debug": {"local-only"},
	}
	if provider == contract.SubscriptionProviderOpenAICodex {
		header.Set("Originator", "astrlink")
		header.Set("Version", "0.100.0-astrlink")
	} else {
		header.Set("X-Stainless-Lang", "python")
		header.Set("X-Stainless-Package-Version", "0.1.0-astrlink")
	}
	return header
}

func claudeConversionCandidate(baseURL string) endpoint.Resolved {
	candidate := protectionClaudeCandidate("service_claude_identity", baseURL)
	candidate.Service.Capabilities = append(candidate.Service.Capabilities, contract.Capability{
		Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true,
		ConvertTo: contract.ProtocolAnthropicMessages,
	})
	return candidate
}

func codexConversionCandidate(baseURL string) endpoint.Resolved {
	candidate := riskCodexCandidate("service_codex_identity", baseURL)
	candidate.Service.Capabilities = append(candidate.Service.Capabilities, contract.Capability{
		Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true,
		ConvertTo: contract.ProtocolOpenAIResponses,
	})
	candidate.UpstreamProtocol = contract.ProtocolOpenAIResponses
	return candidate
}

func passthroughSettings(passthrough, autoLearn bool) contract.RoutingSettings {
	settings := contract.DefaultRoutingSettings()
	settings.OfficialClientPassthrough = passthrough
	settings.ClaudeIdentityAutoLearn, settings.CodexIdentityAutoLearn = autoLearn, autoLearn
	return settings
}

func TestOfficialClaudeClientTeachesIdentityForConvertedRequests(t *testing.T) {
	upstream := newCapturedUpstream(t, http.StatusOK, "application/json", identityClaudeResponse)
	harness := newIdentityHarness(t, passthroughSettings(true, true), nil, claudeConversionCandidate(upstream.url))

	harness.serve(t, "/v1/messages", officialClaudeBody, officialClaudeHeaders(learnedClaudeUA))
	official, officialBody := upstream.request(t)
	if string(officialBody) != officialClaudeBody || official.Get("User-Agent") != learnedClaudeUA ||
		official.Get("X-Stainless-Retry-Count") != "2" || official.Get("X-Stainless-Helper-Method") != "stream" {
		t.Fatalf("official request was rewritten: %s %v", officialBody, official)
	}
	learned := harness.registry.ClaudeIdentityFor(context.Background())
	if learned.UserAgent != learnedClaudeUA || learned.Headers["X-Stainless-Runtime-Version"] != "v24.9.0" {
		t.Fatalf("learned identity = %+v", learned)
	}

	// A converted request carries the learned identity, never the caller's
	// branding, in its final outgoing headers and body.
	harness.serve(t, "/v1/chat/completions",
		`{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`, brandedClientHeaders(contract.SubscriptionProviderClaudeCode))
	converted, _ := upstream.request(t)
	if converted.Get("User-Agent") != learnedClaudeUA {
		t.Fatalf("converted User-Agent = %q", converted.Get("User-Agent"))
	}
	for name, value := range learned.Headers {
		if got := converted.Get(name); got != value {
			t.Fatalf("converted %s = %q, want %q", name, got, value)
		}
	}
	if converted.Get("Authorization") != "Bearer subscription-token" || converted.Get("X-Stainless-Helper-Method") != "" {
		t.Fatalf("converted headers = %v", converted)
	}

	// A converted request never teaches an identity, even one that looks
	// official and newer.
	newer := officialClaudeHeaders("claude-cli/2.1.400 (external, cli)")
	harness.serve(t, "/v1/chat/completions",
		`{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`, newer)
	if got := harness.registry.ClaudeIdentityFor(context.Background()); got.UserAgent != learnedClaudeUA {
		t.Fatalf("converted request taught %q", got.UserAgent)
	}
}

func TestOfficialCodexClientTeachesIdentityForConvertedRequests(t *testing.T) {
	upstream := newCapturedUpstream(t, http.StatusOK, "text/event-stream", codexSSE(
		codexOutputItemDone(0, "converted"),
		`{"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-6-astra","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`,
	))
	harness := newIdentityHarness(t, passthroughSettings(true, true), nil, codexConversionCandidate(upstream.url))

	harness.serve(t, "/v1/responses", officialCodexBody, officialCodexHeaders(learnedCodexUA, "codex_cli_rs"))
	official, officialBody := upstream.request(t)
	if string(officialBody) != officialCodexBody || official.Get("User-Agent") != learnedCodexUA || official.Get("Session-Id") != "session-1" {
		t.Fatalf("official request was rewritten: %s %v", officialBody, official)
	}

	for _, request := range []struct{ path, body string }{
		{"/v1/responses", `{"model":"gpt-6-astra","input":"hello","stream":true}`},
		{"/v1/messages", `{"model":"gpt-6-astra","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"stream":false}`},
	} {
		harness.serve(t, request.path, request.body, brandedClientHeaders(contract.SubscriptionProviderOpenAICodex))
		forwarded, _ := upstream.request(t)
		for name, want := range map[string]string{
			"User-Agent": learnedCodexUA, "Originator": "codex_cli_rs", "Version": "0.160.0",
			"Authorization": "Bearer subscription-token",
		} {
			if got := forwarded.Get(name); got != want {
				t.Fatalf("%s %s = %q, want %q", request.path, name, got, want)
			}
		}
	}

	harness.serve(t, "/v1/messages",
		`{"model":"gpt-6-astra","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"stream":false}`,
		officialCodexHeaders("codex_cli_rs/0.170.0 (Mac OS 26.0.0; arm64)", "codex_cli_rs"))
	if got := harness.registry.CodexIdentityFor(context.Background(), ""); got.UserAgent != learnedCodexUA {
		t.Fatalf("converted request taught %q", got.UserAgent)
	}
}

// An official passthrough request is forwarded exactly as it would be without
// any learned identity or version override.
func TestOfficialPassthroughIgnoresLearnedIdentity(t *testing.T) {
	for _, test := range []struct {
		name, path, body string
		header           http.Header
		candidate        func(string) endpoint.Resolved
		response         string
		teach            http.Header
	}{
		{
			name: "claude", path: "/v1/messages", body: officialClaudeBody,
			header: officialClaudeHeaders("claude-cli/2.1.200 (external, cli)"), candidate: claudeConversionCandidate,
			response: identityClaudeResponse, teach: officialClaudeHeaders(learnedClaudeUA),
		},
		{
			name: "codex", path: "/v1/responses", body: officialCodexBody,
			header: officialCodexHeaders("codex_cli_rs/0.150.0 (Mac OS 26.0.0; arm64)", "codex_cli_rs"), candidate: codexConversionCandidate,
			response: codexSSE(codexCompletedWithoutOutput), teach: officialCodexHeaders(learnedCodexUA, "codex_cli_rs"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			forward := func(settings contract.RoutingSettings, learn bool) (http.Header, []byte) {
				t.Helper()
				upstream := newCapturedUpstream(t, http.StatusOK, "application/json", test.response)
				if test.name == "codex" {
					upstream = newCapturedUpstream(t, http.StatusOK, "text/event-stream", test.response)
				}
				harness := newIdentityHarness(t, settings, nil, test.candidate(upstream.url))
				if learn {
					learnIdentity := harness.registry.LearnCodex
					if test.name == "claude" {
						learnIdentity = harness.registry.LearnClaude
					}
					if changed, err := learnIdentity(context.Background(), test.teach); !changed || err != nil {
						t.Fatalf("learn = %t, %v", changed, err)
					}
				}
				harness.serve(t, test.path, test.body, test.header.Clone())
				header, body := upstream.request(t)
				return header, body
			}
			plainHeader, plainBody := forward(passthroughSettings(true, false), false)
			learnedSettings := passthroughSettings(true, true)
			learnedSettings.ClaudeIdentityVersion, learnedSettings.CodexIdentityVersion = "2.1.500", "0.180.0"
			learnedHeader, learnedBody := forward(learnedSettings, true)
			if string(plainBody) != test.body || string(learnedBody) != test.body {
				t.Fatalf("official body changed:\nplain   %s\nlearned %s", plainBody, learnedBody)
			}
			if !reflect.DeepEqual(plainHeader, learnedHeader) {
				t.Fatalf("learning changed official headers:\nplain   %v\nlearned %v", plainHeader, learnedHeader)
			}
			if got, want := learnedHeader.Get("User-Agent"), test.header.Get("User-Agent"); got != want {
				t.Fatalf("official User-Agent = %q, want %q", got, want)
			}
		})
	}
}

func TestIdentityLearningFollowsSettings(t *testing.T) {
	for _, test := range []struct {
		name                   string
		passthrough, autoLearn bool
		wantLearned            bool
	}{
		{"learning without passthrough", false, true, true},
		{"passthrough without learning", true, false, false},
		{"both off", false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			claudeUpstream := newCapturedUpstream(t, http.StatusOK, "application/json", identityClaudeResponse)
			claude := newIdentityHarness(t, passthroughSettings(test.passthrough, test.autoLearn), nil, claudeConversionCandidate(claudeUpstream.url))
			claude.serve(t, "/v1/messages", officialClaudeBody, officialClaudeHeaders(learnedClaudeUA))
			forwarded, body := claudeUpstream.request(t)
			if got := claude.registry.ClaudeIdentity(contract.DefaultRoutingSettings()).UserAgent; (got == learnedClaudeUA) != test.wantLearned {
				t.Fatalf("Claude identity = %q, want learned %t", got, test.wantLearned)
			}
			// Without passthrough the recognized client is still rewritten.
			if rewritten := string(body) != officialClaudeBody; rewritten == test.passthrough {
				t.Fatalf("passthrough=%t forwarded body %s", test.passthrough, body)
			}
			if !test.passthrough && forwarded.Get("X-Stainless-Retry-Count") != "0" {
				t.Fatalf("non-passthrough headers = %v", forwarded)
			}

			codexUpstream := newCapturedUpstream(t, http.StatusOK, "text/event-stream", codexSSE(codexCompletedWithoutOutput))
			codex := newIdentityHarness(t, passthroughSettings(test.passthrough, test.autoLearn), nil, codexConversionCandidate(codexUpstream.url))
			codex.serve(t, "/v1/responses", officialCodexBody, officialCodexHeaders(learnedCodexUA, "codex_cli_rs"))
			_, codexBody := codexUpstream.request(t)
			if got := codex.registry.CodexIdentity(contract.DefaultRoutingSettings(), "").UserAgent; (got == learnedCodexUA) != test.wantLearned {
				t.Fatalf("Codex identity = %q, want learned %t", got, test.wantLearned)
			}
			if rewritten := string(codexBody) != officialCodexBody; rewritten == test.passthrough {
				t.Fatalf("passthrough=%t forwarded body %s", test.passthrough, codexBody)
			}
		})
	}
}

func TestLearnedIdentitySurvivesRestartForConvertedRequests(t *testing.T) {
	learned := &memoryLearnedIdentities{}
	upstream := newCapturedUpstream(t, http.StatusOK, "application/json", identityClaudeResponse)
	first := newIdentityHarness(t, passthroughSettings(true, true), learned, claudeConversionCandidate(upstream.url))
	first.serve(t, "/v1/messages", officialClaudeBody, officialClaudeHeaders(learnedClaudeUA))
	var document accountauth.ClientIdentity
	if err := json.Unmarshal(learned.documents[contract.SubscriptionProviderClaudeCode], &document); err != nil || document.UserAgent != learnedClaudeUA {
		t.Fatalf("persisted identity = %s, %v", learned.documents[contract.SubscriptionProviderClaudeCode], err)
	}

	restarted := newIdentityHarness(t, passthroughSettings(true, true), learned, claudeConversionCandidate(upstream.url))
	restarted.serve(t, "/v1/chat/completions",
		`{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`, brandedClientHeaders(contract.SubscriptionProviderClaudeCode))
	converted, _ := upstream.request(t)
	if converted.Get("User-Agent") != learnedClaudeUA || converted.Get("X-Stainless-Runtime-Version") != "v24.9.0" {
		t.Fatalf("restarted converted headers = %v", converted)
	}

	// With learning turned off the restarted gateway uses the baseline again.
	restarted.settings.mu.Lock()
	restarted.settings.settings.ClaudeIdentityAutoLearn = false
	restarted.settings.mu.Unlock()
	restarted.serve(t, "/v1/chat/completions",
		`{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`, brandedClientHeaders(contract.SubscriptionProviderClaudeCode))
	baseline, _ := upstream.request(t)
	if baseline.Get("User-Agent") != accountauth.DefaultClaudeUserAgent {
		t.Fatalf("User-Agent with learning off = %q", baseline.Get("User-Agent"))
	}
}
