package ingress

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

const protectionAccountUUID = "4f3c2b1a-0000-4000-8000-000000000001"

func protectionSettings(enabled bool) contract.RoutingSettings {
	settings := contract.DefaultRoutingSettings()
	settings.SubscriptionRiskProtection = enabled
	settings.CodexRequestNormalization = enabled
	settings.ClaudeRequestNormalization = enabled
	settings.SubscriptionSessionIsolation = enabled
	return settings
}

type capturedUpstream struct {
	mu     sync.Mutex
	header http.Header
	body   []byte
	url    string
}

func newCapturedUpstream(t *testing.T, status int, contentType, response string) *capturedUpstream {
	t.Helper()
	upstream := &capturedUpstream{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		upstream.mu.Lock()
		upstream.header, upstream.body = request.Header.Clone(), body
		upstream.mu.Unlock()
		writer.Header().Set("Content-Type", contentType)
		writer.WriteHeader(status)
		_, _ = io.WriteString(writer, response)
	}))
	t.Cleanup(server.Close)
	upstream.url = server.URL
	return upstream
}

func (upstream *capturedUpstream) request(t *testing.T) (http.Header, []byte) {
	t.Helper()
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if upstream.header == nil {
		t.Fatal("upstream was not called")
	}
	for name, values := range upstream.header {
		if strings.Contains(strings.ToLower(name+strings.Join(values, ",")), "astrlink") {
			t.Fatalf("forwarded header carries gateway branding: %s: %v", name, values)
		}
	}
	if strings.Contains(strings.ToLower(string(upstream.body)), "astrlink") {
		t.Fatalf("forwarded body carries gateway branding: %s", upstream.body)
	}
	return upstream.header, upstream.body
}

func protectionClaudeCandidate(id contract.ServiceID, baseURL string) endpoint.Resolved {
	kind := contract.ServiceKindClaudeSubscription
	return endpoint.Resolved{BaseURL: baseURL, UpstreamProtocol: contract.ProtocolAnthropicMessages, Service: contract.Service{
		ID: id, Name: "Claude", Kind: kind, Enabled: true, Models: []string{"claude-sonnet-4-5"},
		Capabilities: kind.SubscriptionProvider().Capabilities(),
		Subscription: &contract.SubscriptionConnection{
			Provider: kind.SubscriptionProvider(), Status: contract.SubscriptionStatusConnected,
			CredentialRef: accountauth.CredentialRefFor(id), ProviderAccountID: protectionAccountUUID,
		},
	}}
}

func serveProtectedRequest(
	t *testing.T,
	settings contract.RoutingSettings,
	reporter SubscriptionRiskReporter,
	candidate endpoint.Resolved,
	path, body string,
	header http.Header,
) *httptest.ResponseRecorder {
	t.Helper()
	store := newRedirectSettingsStore()
	store.settings = settings
	handler := NewWithDependencies(Dependencies{
		Resolver:         candidateResolver{candidates: []endpoint.Resolved{candidate}},
		Authorizer:       endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}).WithRoutingSettings(store),
		RequestRecords:   store,
		SubscriptionRisk: reporter,
	})
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	for name, values := range header {
		request.Header[name] = values
	}
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestCodexProtectionSwitches(t *testing.T) {
	const body = `{"model":"gpt-6-astra","input":"hello","store":true,"temperature":0.2,"stream":true,"prompt_cache_key":"client-key"}`
	for _, enabled := range []bool{true, false} {
		upstream := newCapturedUpstream(t, http.StatusOK, "text/event-stream", "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[]}}\n\n")
		candidate := riskCodexCandidate("service_codex_a", upstream.url)
		header := http.Header{"Session_id": {"client-session"}, "X-Stainless-Lang": {"js"}}
		response := serveProtectedRequest(t, protectionSettings(enabled), nil, candidate, "/v1/responses", body, header)
		if response.Code != http.StatusOK {
			t.Fatalf("enabled=%t response = %d %s", enabled, response.Code, response.Body.String())
		}
		forwarded, raw := upstream.request(t)
		if !enabled {
			if string(raw) != body || forwarded.Get("Session_id") != "client-session" {
				t.Fatalf("disabled protections changed the request: %s %v", raw, forwarded)
			}
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["store"]) != "false" || fields["temperature"] != nil {
			t.Fatalf("Codex body not normalized: %s", raw)
		}
		if key, _ := claudeString(fields["prompt_cache_key"]); key != accountauth.ScopedSessionID("service_codex_a", "client-key") {
			t.Fatalf("prompt_cache_key = %s", fields["prompt_cache_key"])
		}
		if got := forwarded.Get("Session_id"); got != accountauth.ScopedSessionID("service_codex_a", "client-session") {
			t.Fatalf("session_id = %q", got)
		}
		if forwarded.Get("Conversation_id") != "" || forwarded.Get("X-Stainless-Lang") != "" {
			t.Fatalf("unexpected forwarded headers: %v", forwarded)
		}
	}
}

func TestCodexLegacyPreparationWithoutNormalization(t *testing.T) {
	const converted = `{"model":"gpt-6-astra","input":"hi","store":true,"max_output_tokens":8,"temperature":0.2,"stream":false}`
	for _, test := range []struct {
		name      string
		protocol  contract.ProtocolID
		converted bool
		body      string
		want      string
	}{
		{"native is untouched", contract.ProtocolOpenAIResponses, false, "not json", "not json"},
		{"converted compact is untouched", contract.ProtocolOpenAIResponsesCompact, true, converted, converted},
		{"converted Responses keeps the minimal fixes", contract.ProtocolOpenAIResponses, true, converted,
			`{"input":"hi","instructions":"","model":"gpt-6-astra","store":false,"stream":false,"temperature":0.2}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(test.body))
			forced, err := prepareCodexSubscriptionRequest(request, test.protocol, codexRequestOptions{converted: test.converted})
			if err != nil || forced {
				t.Fatalf("forced=%t err=%v", forced, err)
			}
			if got, _ := io.ReadAll(request.Body); string(got) != test.want {
				t.Fatalf("body = %s, want %s", got, test.want)
			}
		})
	}
}

func TestCodexSessionScopeMapsOnlyPresentIdentifiers(t *testing.T) {
	prepare := func(scope contract.ServiceID, body string, header http.Header) (http.Header, string) {
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
		request.Header = header.Clone()
		if _, err := prepareCodexSubscriptionRequest(request, contract.ProtocolOpenAIResponses, codexRequestOptions{sessionScope: scope}); err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(request.Body)
		return request.Header, string(raw)
	}
	header := http.Header{"Conversation_id": {"conversation-1"}}
	first, _ := prepare("service_a", `{"prompt_cache_key":""}`, header)
	again, _ := prepare("service_a", `{}`, header)
	other, _ := prepare("service_b", `{}`, header)
	if first.Get("Conversation_id") != again.Get("Conversation_id") || first.Get("Conversation_id") == other.Get("Conversation_id") ||
		first.Get("Conversation_id") == "conversation-1" || first.Get("Session_id") != "" {
		t.Fatalf("conversation mapping: %v %v %v", first, again, other)
	}
	if _, body := prepare("service_a", `{"prompt_cache_key":""}`, nil); body != `{"prompt_cache_key":""}` {
		t.Fatalf("empty prompt_cache_key was rewritten: %s", body)
	}
	if _, body := prepare("service_a", "not json", nil); body != "not json" {
		t.Fatalf("session scoping rejected or rewrote a body: %s", body)
	}
}

func TestClaudeProtectionSwitches(t *testing.T) {
	const body = `{"model":"claude-sonnet-4-5","max_tokens":4096,"top_k":5,` +
		`"thinking":{"type":"enabled","budget_tokens":2048},"metadata":{"user_id":"client-user","tier":"keep"},` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"text","text":" "}]}]}`
	for _, enabled := range []bool{true, false} {
		upstream := newCapturedUpstream(t, http.StatusOK, "application/json", `{"id":"msg_1","type":"message","content":[]}`)
		candidate := protectionClaudeCandidate("service_claude_a", upstream.url)
		header := http.Header{"X-Stainless-Lang": {"python"}}
		response := serveProtectedRequest(t, protectionSettings(enabled), nil, candidate, "/v1/messages", body, header)
		if response.Code != http.StatusOK {
			t.Fatalf("enabled=%t response = %d %s", enabled, response.Code, response.Body.String())
		}
		forwarded, raw := upstream.request(t)
		var payload struct {
			TopK     *int `json:"top_k"`
			Messages []struct {
				Content []json.RawMessage `json:"content"`
			} `json:"messages"`
			Metadata map[string]string `json:"metadata"`
			System   []struct {
				Text string `json:"text"`
			} `json:"system"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.System) == 0 || payload.System[0].Text != claudeCodeBanner || payload.Metadata["tier"] != "keep" {
			t.Fatalf("enabled=%t body = %s", enabled, raw)
		}
		// The default identity always carries the Claude Code SDK fingerprint.
		if forwarded.Get("X-Stainless-Lang") != "js" {
			t.Fatalf("X-Stainless-Lang = %q", forwarded.Get("X-Stainless-Lang"))
		}
		if !enabled {
			if payload.TopK == nil || len(payload.Messages[0].Content) != 2 || payload.Metadata["user_id"] != "client-user" ||
				forwarded.Get(accountauth.ClaudeCodeSessionHeader) != "" {
				t.Fatalf("disabled protections changed the request: %s %v", raw, forwarded)
			}
			continue
		}
		if payload.TopK != nil || len(payload.Messages[0].Content) != 1 {
			t.Fatalf("Claude body not normalized: %s", raw)
		}
		session := accountauth.ScopedSessionID("service_claude_a", "client-user")
		if payload.Metadata["user_id"] != accountauth.ClaudeMetadataUserID("service_claude_a", protectionAccountUUID, session, false) ||
			forwarded.Get(accountauth.ClaudeCodeSessionHeader) != session {
			t.Fatalf("user_id = %s, session header = %q", payload.Metadata["user_id"], forwarded.Get(accountauth.ClaudeCodeSessionHeader))
		}
	}
}

func TestClaudeSessionScope(t *testing.T) {
	legacyUserID := "user_" + strings.Repeat("a", 64) + "_account__session_client-session"
	prepare := func(scope claudeSessionScope, body string, header http.Header) (http.Header, map[string]json.RawMessage) {
		request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
		request.Header = header.Clone()
		if request.Header == nil {
			request.Header = http.Header{}
		}
		if err := prepareClaudeSubscriptionRequest(request, claudeRequestOptions{session: &scope}); err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(request.Body)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		var metadata map[string]json.RawMessage
		_ = json.Unmarshal(fields["metadata"], &metadata)
		return request.Header, metadata
	}
	userID := func(metadata map[string]json.RawMessage) string {
		value, _ := claudeString(metadata["user_id"])
		return value
	}
	scopeA := claudeSessionScope{serviceID: "service_a", accountUUID: protectionAccountUUID}
	scopeB := claudeSessionScope{serviceID: "service_b", accountUUID: protectionAccountUUID}
	mapped := accountauth.ScopedSessionID("service_a", "client-session")

	t.Run("legacy client keeps its format without enforced identity", func(t *testing.T) {
		header, metadata := prepare(scopeA, `{"metadata":{"user_id":"`+legacyUserID+`"},"messages":[]}`, nil)
		if want := accountauth.ClaudeMetadataUserID("service_a", protectionAccountUUID, mapped, true); userID(metadata) != want {
			t.Fatalf("user_id = %s, want %s", userID(metadata), want)
		}
		if header.Get(accountauth.ClaudeCodeSessionHeader) != "" {
			t.Fatal("session header invented for a client that sent none")
		}
	})
	t.Run("enforced identity uses the JSON form and session header", func(t *testing.T) {
		scope := scopeA
		scope.identity = true
		header, metadata := prepare(scope, `{"metadata":{"user_id":"`+legacyUserID+`"},"messages":[]}`, nil)
		if userID(metadata) != accountauth.ClaudeMetadataUserID("service_a", protectionAccountUUID, mapped, false) ||
			header.Get(accountauth.ClaudeCodeSessionHeader) != mapped {
			t.Fatalf("user_id = %s header = %v", userID(metadata), header)
		}
	})
	t.Run("session header seeds the session", func(t *testing.T) {
		header, metadata := prepare(scopeA, `{"messages":[]}`, http.Header{"X-Claude-Code-Session-Id": {"client-session"}})
		if header.Get(accountauth.ClaudeCodeSessionHeader) != mapped || accountauth.ClaudeUserIDSession(userID(metadata)) != mapped {
			t.Fatalf("user_id = %s header = %v", userID(metadata), header)
		}
	})
	t.Run("first user message seeds a stable session per account", func(t *testing.T) {
		const body = `{"messages":[{"role":"user","content":"hello"}]}`
		_, first := prepare(scopeA, body, nil)
		_, again := prepare(scopeA, body, nil)
		_, other := prepare(scopeB, body, nil)
		if userID(first) == "" || userID(first) != userID(again) || userID(first) == userID(other) {
			t.Fatalf("user_ids = %s %s %s", userID(first), userID(again), userID(other))
		}
	})
	t.Run("malformed metadata is left alone", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"metadata":"client","messages":[]}`))
		if err := prepareClaudeSubscriptionRequest(request, claudeRequestOptions{session: &scopeA}); err != nil {
			t.Fatal(err)
		}
		if raw, _ := io.ReadAll(request.Body); !strings.Contains(string(raw), `"metadata":"client"`) {
			t.Fatalf("metadata rewritten: %s", raw)
		}
	})
}

func TestRiskProtectionSwitchSkipsAccountSignals(t *testing.T) {
	body := `{"error":{"code":"deactivated_workspace","message":"Workspace deactivated"}}`
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"suspension", http.StatusPaymentRequired, body},
		{"rejected token", http.StatusUnauthorized, `{"error":{"code":"token_invalidated"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := newCapturedUpstream(t, test.status, "application/json", test.body)
			reporter := newRecordingSubscriptionRisk()
			response := serveProtectedRequest(t, protectionSettings(false), reporter,
				riskCodexCandidate("service_codex_off", upstream.url), "/v1/responses", `{"model":"gpt-6-astra","input":"hello"}`, nil)
			if response.Code != test.status || response.Body.String() != test.body {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
			if reports, _ := reporter.snapshot(); len(reports) != 0 {
				t.Fatalf("disabled risk protection reported %+v", reports)
			}
			select {
			case refreshed := <-reporter.refreshed:
				t.Fatalf("disabled risk protection refreshed %s", refreshed)
			default:
			}
		})
	}
}
