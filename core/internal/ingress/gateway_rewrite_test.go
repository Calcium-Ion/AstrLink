package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/providerapi"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/tidwall/gjson"
)

const (
	testPromptPrefix = "Answer in plain English."
	testPromptSuffix = "Explain the plan before changing code."
)

func rewriteSettings(mode contract.ReasoningMode, level contract.ReasoningLevel) contract.RoutingSettings {
	settings := contract.DefaultRoutingSettings()
	settings.SystemPromptEnabled = true
	settings.SystemPromptPrefix = testPromptPrefix
	settings.SystemPromptSuffix = testPromptSuffix
	settings.ReasoningMode = mode
	settings.ReasoningLevel = level
	return settings
}

// promptSettings turns on the global prompt and leaves reasoning to clients.
func promptSettings() contract.RoutingSettings {
	return rewriteSettings(contract.ReasoningModePassthrough, contract.ReasoningLevelMedium)
}

// reasoningSettings sets reasoning and leaves the global prompt off.
func reasoningSettings(mode contract.ReasoningMode, level contract.ReasoningLevel) contract.RoutingSettings {
	settings := contract.DefaultRoutingSettings()
	settings.ReasoningMode = mode
	settings.ReasoningLevel = level
	return settings
}

// adaptiveSettings sets the adaptive mode with a preset and leaves the global
// prompt off. low and high are the custom range.
func adaptiveSettings(preset contract.AdaptivePreset, low, high contract.ReasoningLevel) contract.RoutingSettings {
	settings := reasoningSettings(contract.ReasoningModeAdaptive, contract.ReasoningLevelMedium)
	settings.AdaptivePreset, settings.AdaptiveMin, settings.AdaptiveMax = preset, low, high
	return settings
}

// jsonEscape spells the JSON escape of a UTF-16 code unit.
func jsonEscape(hex string) string {
	return "\\" + "u" + hex
}

// sequenceUpstream answers each call with the next queued status, then with
// 200 and its response, and keeps every request it received.
type sequenceUpstream struct {
	mu          sync.Mutex
	statuses    []int
	errors      []string
	contentType string
	response    string
	bodies      []string
	paths       []string
	url         string
}

func newSequenceUpstream(t *testing.T, contentType, response string, statuses ...int) *sequenceUpstream {
	t.Helper()
	upstream := &sequenceUpstream{statuses: statuses, contentType: contentType, response: response}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		upstream.mu.Lock()
		upstream.bodies = append(upstream.bodies, string(body))
		upstream.paths = append(upstream.paths, request.URL.Path)
		status, message := http.StatusOK, ""
		if len(upstream.statuses) > 0 {
			status, upstream.statuses = upstream.statuses[0], upstream.statuses[1:]
			if len(upstream.errors) > 0 {
				message, upstream.errors = upstream.errors[0], upstream.errors[1:]
			}
		}
		upstream.mu.Unlock()
		if status != http.StatusOK {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(status)
			_, _ = fmt.Fprintf(writer, `{"type":"error","error":{"type":"invalid_request_error","message":%q}}`, message)
			return
		}
		writer.Header().Set("Content-Type", upstream.contentType)
		_, _ = io.WriteString(writer, upstream.response)
	}))
	t.Cleanup(server.Close)
	upstream.url = server.URL
	return upstream
}

func (upstream *sequenceUpstream) requests() []string {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	return append([]string(nil), upstream.bodies...)
}

func apiCandidate(id contract.ServiceID, kind contract.ServiceKind, baseURL string, protocol contract.ProtocolID, capabilities ...contract.Capability) endpoint.Resolved {
	auth := contract.AuthSchemeBearer
	if kind == contract.ServiceKindGemini {
		auth = contract.AuthSchemeGoogleAPIKey
	}
	service := contract.Service{
		ID: id, Name: string(id), Kind: kind, Enabled: true, Models: []string{"model-test"},
		Capabilities: append([]contract.Capability{{
			Protocol: protocol, Mode: contract.CapabilityModeNative, Streaming: protocol != contract.ProtocolOpenAIResponsesCompact,
		}}, capabilities...),
		HTTP: &contract.HTTPConnection{BaseURL: baseURL, Auth: contract.ServiceAuth{Scheme: auth}, CredentialRef: "local://service/" + string(id)},
	}
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS, policy.MaxDelayMS = 0, 0
	return endpoint.Resolved{Service: service, BaseURL: baseURL, UpstreamProtocol: protocol, FailurePolicy: &policy}
}

func serveRewrite(t *testing.T, settings contract.RoutingSettings, candidates []endpoint.Resolved, path, body string, header http.Header) (*httptest.ResponseRecorder, *redirectSettingsStore) {
	t.Helper()
	store := newRedirectSettingsStore()
	store.settings = settings
	handler := NewWithDependencies(Dependencies{
		Resolver:         candidateResolver{candidates: candidates},
		Authorizer:       endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}).WithRoutingSettings(store),
		RequestRecords:   store,
		ConversionEngine: relaykitbridge.NewEngine(),
	})
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	for name, values := range header {
		request.Header[name] = values
	}
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response, store
}

func TestGlobalPromptReachesEveryInferenceProtocol(t *testing.T) {
	const prefix, suffix = testPromptPrefix, testPromptSuffix
	geminiResponse := `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`
	for _, test := range []struct {
		name     string
		kind     contract.ServiceKind
		base     string
		protocol contract.ProtocolID
		path     string
		body     string
		want     string
	}{
		{
			"chat", contract.ServiceKindMagpie, "", contract.ProtocolOpenAIChat, "/v1/chat/completions",
			`{"model":"model-test","messages":[{"role":"system","content":"Keep my instructions"},{"role":"user","content":"hello"}]}`,
			`{"model":"model-test","messages":[{"role":"system","content":"` + prefix + `\n\nKeep my instructions\n\n` + suffix + `"},{"role":"user","content":"hello"}]}`,
		},
		{
			"chat without system", contract.ServiceKindMagpie, "", contract.ProtocolOpenAIChat, "/v1/chat/completions",
			`{"model":"model-test","messages":[{"role":"user","content":"hello"}]}`,
			`{"model":"model-test","messages":[{"content":"` + prefix + `\n\n` + suffix + `","role":"system"},{"role":"user","content":"hello"}]}`,
		},
		{
			"responses", contract.ServiceKindMagpie, "", contract.ProtocolOpenAIResponses, "/v1/responses",
			`{"model":"model-test","instructions":"Keep my instructions","input":"hello"}`,
			`{"model":"model-test","instructions":"` + prefix + `\n\nKeep my instructions\n\n` + suffix + `","input":"hello"}`,
		},
		{
			"responses compact", contract.ServiceKindXAI, "/v1", contract.ProtocolOpenAIResponsesCompact, "/v1/responses/compact",
			`{"model":"model-test","input":"hello"}`,
			`{"model":"model-test","input":"hello","instructions":"` + prefix + `\n\n` + suffix + `"}`,
		},
		{
			"anthropic string", contract.ServiceKindMagpie, "", contract.ProtocolAnthropicMessages, "/v1/messages",
			`{"model":"model-test","max_tokens":64,"system":"Keep my instructions","messages":[{"role":"user","content":"hello"}]}`,
			`{"model":"model-test","max_tokens":64,"system":"` + prefix + `\n\nKeep my instructions\n\n` + suffix + `","messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			"anthropic blocks", contract.ServiceKindMagpie, "", contract.ProtocolAnthropicMessages, "/v1/messages",
			`{"model":"model-test","max_tokens":64,"system":[{"type":"text","text":"Keep my instructions","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"}]}`,
			`{"model":"model-test","max_tokens":64,"system":[{"text":"` + prefix + `","type":"text"},{"type":"text","text":"Keep my instructions","cache_control":{"type":"ephemeral"}},{"text":"` + suffix + `","type":"text"}],"messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			"anthropic without system", contract.ServiceKindMagpie, "", contract.ProtocolAnthropicMessages, "/v1/messages",
			`{"model":"model-test","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
			`{"model":"model-test","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"system":"` + prefix + `\n\n` + suffix + `"}`,
		},
		{
			"gemini", contract.ServiceKindGemini, "", contract.ProtocolGoogleGenerateContent, "/v1beta/models/model-test:generateContent",
			`{"systemInstruction":{"parts":[{"text":"Keep my instructions"}]},"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			`{"systemInstruction":{"parts":[{"text":"` + prefix + `"},{"text":"Keep my instructions"},{"text":"` + suffix + `"}]},"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
		},
		{
			"gemini without system", contract.ServiceKindGemini, "", contract.ProtocolGoogleGenerateContent, "/v1beta/models/model-test:generateContent",
			`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"systemInstruction":{"parts":[{"text":"` + prefix + `"},{"text":"` + suffix + `"}]}}`,
		},
		{
			"gemini with cached content untouched", contract.ServiceKindGemini, "", contract.ProtocolGoogleGenerateContent, "/v1beta/models/model-test:generateContent",
			`{"cachedContent":"cachedContents/abc","contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			`{"cachedContent":"cachedContents/abc","contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
		},
		{
			"completions untouched", contract.ServiceKindXAI, "/v1", contract.ProtocolOpenAICompletions, "/v1/completions",
			`{"model":"model-test","prompt":"hello"}`,
			`{"model":"model-test","prompt":"hello"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, response := nativeProviderExchange(test.protocol, false)
			if test.protocol == contract.ProtocolGoogleGenerateContent {
				response = geminiResponse
			}
			upstream := newSequenceUpstream(t, "application/json", response)
			candidate := apiCandidate("service_api", test.kind, upstream.url+test.base, test.protocol)
			got, _ := serveRewrite(t, promptSettings(), []endpoint.Resolved{candidate}, test.path, test.body, nil)
			if got.Code != http.StatusOK {
				t.Fatalf("response = %d %s", got.Code, got.Body.String())
			}
			if bodies := upstream.requests(); len(bodies) != 1 || bodies[0] != test.want {
				t.Fatalf("upstream bodies = %q\nwant %s", bodies, test.want)
			}
		})
	}
}

func TestGlobalPromptIsAddedOncePerAttempt(t *testing.T) {
	chat := `{"model":"model-test","messages":[{"role":"system","content":"Keep my instructions"},{"role":"user","content":"hello"}]}`
	_, _, chatResponse := nativeProviderExchange(contract.ProtocolOpenAIChat, false)
	first := newSequenceUpstream(t, "application/json", chatResponse, http.StatusInternalServerError, http.StatusInternalServerError)
	second := newSequenceUpstream(t, "application/json", chatResponse)
	candidates := []endpoint.Resolved{
		apiCandidate("service_a", contract.ServiceKindMagpie, first.url, contract.ProtocolOpenAIChat),
		apiCandidate("service_b", contract.ServiceKindMagpie, second.url, contract.ProtocolOpenAIChat),
	}
	candidates[0].Failover = &contract.FailoverPolicy{Enabled: true, Strategy: contract.RetryFirst, MaxAttempts: 6}
	response, _ := serveRewrite(t, promptSettings(), candidates, "/v1/chat/completions", chat, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	attempts := append(first.requests(), second.requests()...)
	if len(first.requests()) < 2 || len(second.requests()) != 1 {
		t.Fatalf("expected a retry and a failover, got %d and %d attempts", len(first.requests()), len(second.requests()))
	}
	for index, body := range attempts {
		if strings.Count(body, testPromptPrefix) != 1 || strings.Count(body, testPromptSuffix) != 1 {
			t.Fatalf("attempt %d carries the prompt more or less than once: %s", index, body)
		}
	}

	// A repaired retry is rebuilt from the client's body and gets the prompt
	// and the reasoning level once more. Its history no longer holds thinking
	// blocks; the last assistant turn has no tool call, so thinking comes back
	// on without breaking it.
	messages := `{"model":"claude-sonnet-4-5","max_tokens":32000,"thinking":{"type":"enabled","budget_tokens":4000},` +
		`"system":"Keep my instructions","messages":[{"role":"user","content":"hello"},` +
		`{"role":"assistant","content":[{"type":"thinking","thinking":"plan","signature":"stale"},{"type":"text","text":"ok"}]},` +
		`{"role":"user","content":"continue"}]}`
	_, _, messagesResponse := nativeProviderExchange(contract.ProtocolAnthropicMessages, false)
	anthropic := newSequenceUpstream(t, "application/json", messagesResponse, http.StatusBadRequest)
	anthropic.errors = []string{"messages.1.content.0: Invalid `signature` in `thinking` block"}
	candidate := apiCandidate("service_claude", contract.ServiceKindMagpie, anthropic.url, contract.ProtocolAnthropicMessages)
	response, _ = serveRewrite(t, rewriteSettings(contract.ReasoningModeFixed, contract.ReasoningLevelHigh), []endpoint.Resolved{candidate}, "/v1/messages", messages, nil)
	bodies := anthropic.requests()
	if response.Code != http.StatusOK || len(bodies) != 2 {
		t.Fatalf("response = %d %s after %d attempts", response.Code, response.Body.String(), len(bodies))
	}
	for index, body := range bodies {
		if strings.Count(body, testPromptPrefix) != 1 || strings.Count(body, testPromptSuffix) != 1 {
			t.Fatalf("attempt %d carries the prompt more or less than once: %s", index, body)
		}
	}
	if !strings.Contains(bodies[0], `"budget_tokens":31999`) {
		t.Fatalf("first attempt did not get the override: %s", bodies[0])
	}
	if strings.Contains(bodies[1], `"signature"`) || !strings.Contains(bodies[1], `"thinking":{"type":"enabled","budget_tokens":31999}`) {
		t.Fatalf("repaired retry = %s", bodies[1])
	}
}

func TestGatewayRewritesAreRecorded(t *testing.T) {
	notes := func(record contract.RequestRecord) []string {
		var paths []string
		for _, diagnostic := range record.ConversionDiagnostics {
			if diagnostic.Code == contract.GatewayRewriteDiagnostic {
				paths = append(paths, diagnostic.Path)
			}
		}
		return paths
	}
	_, _, chatResponse := nativeProviderExchange(contract.ProtocolOpenAIChat, false)
	upstream := newSequenceUpstream(t, "application/json", chatResponse)
	candidate := apiCandidate("service_api", contract.ServiceKindMagpie, upstream.url, contract.ProtocolOpenAIChat)
	response, store := serveRewrite(t, promptSettings(), []endpoint.Resolved{candidate}, "/v1/chat/completions",
		`{"model":"model-test","messages":[{"role":"user","content":"hello"}]}`, nil)
	if got := notes(store.root(t)); response.Code != http.StatusOK || !slices.Equal(got, []string{"global_prompt"}) {
		t.Fatalf("API key provider: response = %d, rewrites = %q", response.Code, got)
	}

	// Droid always presents its own client, so every call there carries the
	// subscription identity.
	droid := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, identityClaudeResponse)
	}))
	defer droid.Close()
	kind := contract.ServiceKindDroidSubscription
	service := contract.Service{
		ID: "service_droid", Name: "Droid", Kind: kind, Enabled: true, Models: []string{"claude-opus-4-6"},
		Capabilities: kind.SubscriptionProvider().Capabilities(),
		Subscription: &contract.SubscriptionConnection{Provider: kind.SubscriptionProvider(), Status: contract.SubscriptionStatusConnected, CredentialRef: "keyring://subscription/service_droid"},
	}
	response, store = serveRewrite(t, reasoningSettings(contract.ReasoningModePassthrough, contract.ReasoningLevelMedium),
		[]endpoint.Resolved{{Service: service, BaseURL: droid.URL, UpstreamProtocol: contract.ProtocolAnthropicMessages}}, "/v1/messages",
		`{"model":"claude-opus-4-6","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`, nil)
	if got := notes(store.root(t)); response.Code != http.StatusOK || !slices.Equal(got, []string{"client_identity"}) {
		t.Fatalf("Droid: response = %d, rewrites = %q", response.Code, got)
	}
}

func TestGlobalPromptSkipsConnectionTests(t *testing.T) {
	_, _, response := nativeProviderExchange(contract.ProtocolOpenAIChat, false)
	upstream := newSequenceUpstream(t, "application/json", response)
	candidate := apiCandidate("service_api", contract.ServiceKindMagpie, upstream.url, contract.ProtocolOpenAIChat)
	store := newRedirectSettingsStore()
	store.settings = rewriteSettings(contract.ReasoningModeFixed, contract.ReasoningLevelMax)
	handler := NewWithDependencies(Dependencies{
		Resolver:       candidateResolver{candidates: []endpoint.Resolved{candidate}},
		Authorizer:     endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}),
		RequestRecords: store,
	})
	body := `{"model":"model-test","reasoning_effort":"low","messages":[{"role":"user","content":"hello"}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	input := contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: "model-test"}
	if err := handler.ServeServiceTest(recorder, request, candidate.Service, input, candidate.BaseURL, ServiceTestObserver{}); err != nil {
		t.Fatal(err)
	}
	if bodies := upstream.requests(); recorder.Code != http.StatusOK || len(bodies) != 1 || bodies[0] != body {
		t.Fatalf("connection test = %d, upstream bodies = %q", recorder.Code, bodies)
	}
}

func TestGlobalPromptKeepsOfficialIdentityFirst(t *testing.T) {
	const billing = "x-anthropic-billing-header: cc_version=2.1.300.abc; cc_entrypoint=cli; cch=00000;"
	systemTexts := func(t *testing.T, body []byte) []string {
		t.Helper()
		var payload struct {
			System json.RawMessage `json:"system"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		var text string
		if json.Unmarshal(payload.System, &text) == nil {
			return []string{text}
		}
		var blocks []struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(payload.System, &blocks); err != nil {
			t.Fatalf("system = %s", payload.System)
		}
		texts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			texts = append(texts, block.Text)
		}
		return texts
	}
	equal := func(t *testing.T, got []string, want ...string) {
		t.Helper()
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("system = %q\nwant   %q", got, want)
		}
	}

	t.Run("official Claude Code", func(t *testing.T) {
		upstream := newCapturedUpstream(t, http.StatusOK, "application/json", identityClaudeResponse)
		harness := newIdentityHarness(t, promptSettings(), nil, claudeConversionCandidate(upstream.url))
		body := strings.Replace(officialClaudeBody, `"messages":`,
			`"system":[{"type":"text","text":"`+billing+`"},{"type":"text","text":"`+claudeCodeBanner+`","cache_control":{"type":"ephemeral"}},{"type":"text","text":"Main prompt"}],`+
				`"tools":[{"name":"Read","input_schema":{"type":"object"}}],"messages":`, 1)
		harness.serve(t, "/v1/messages", body, officialClaudeHeaders(learnedClaudeUA))
		header, forwarded := upstream.request(t)
		if header.Get("User-Agent") != learnedClaudeUA {
			t.Fatalf("official request lost its identity: %v", header)
		}
		equal(t, systemTexts(t, forwarded), billing, claudeCodeBanner, testPromptPrefix, "Main prompt", testPromptSuffix)
	})

	t.Run("third-party banner string", func(t *testing.T) {
		upstream := newCapturedUpstream(t, http.StatusOK, "application/json", identityClaudeResponse)
		harness := newIdentityHarness(t, promptSettings(), nil, claudeConversionCandidate(upstream.url))
		harness.serve(t, "/v1/messages",
			`{"model":"claude-sonnet-4-5","max_tokens":64,"system":"`+claudeCodeBanner+`","messages":[{"role":"user","content":"hello"}]}`, nil)
		_, forwarded := upstream.request(t)
		equal(t, systemTexts(t, forwarded), claudeCodeBanner, testPromptPrefix+"\n\n"+testPromptSuffix)
	})

	t.Run("converted to Claude subscription", func(t *testing.T) {
		for _, client := range []struct{ path, body string }{
			{"/v1/chat/completions", `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"system","content":"Main prompt"},{"role":"user","content":"hello"}]}`},
			{"/v1/responses", `{"model":"claude-sonnet-4-5","max_output_tokens":64,"instructions":"Main prompt","input":"hello"}`},
		} {
			upstream := newCapturedUpstream(t, http.StatusOK, "application/json", identityClaudeResponse)
			candidate := claudeConversionCandidate(upstream.url)
			candidate.Service.Capabilities = append(candidate.Service.Capabilities, contract.Capability{
				Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true,
				ConvertTo: contract.ProtocolAnthropicMessages,
			})
			harness := newIdentityHarness(t, promptSettings(), nil, candidate)
			harness.serve(t, client.path, client.body, nil)
			_, forwarded := upstream.request(t)
			texts := systemTexts(t, forwarded)
			joined := strings.Join(texts, "\n\n")
			if texts[0] != claudeCodeBanner || strings.Count(joined, claudeCodeBanner) != 1 ||
				!strings.HasPrefix(joined, claudeCodeBanner+"\n\n"+testPromptPrefix+"\n\nMain prompt") || !strings.HasSuffix(joined, testPromptSuffix) {
				t.Fatalf("%s: system = %q", client.path, texts)
			}
		}
	})

	t.Run("Droid", func(t *testing.T) {
		const line = providerapi.DroidSystemPromptLine
		for _, test := range []struct {
			system string
			want   []string
		}{
			{`"Main prompt"`, []string{line + "\n" + testPromptPrefix + "\n\nMain prompt\n\n" + testPromptSuffix}},
			{`"` + line + `\nMain prompt"`, []string{line + "\n\n" + testPromptPrefix + "\nMain prompt\n\n" + testPromptSuffix}},
			{`[{"type":"text","text":"` + line + `"},{"type":"text","text":"Main prompt"}]`, []string{line, testPromptPrefix, "Main prompt", testPromptSuffix}},
		} {
			var forwarded []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				forwarded, _ = io.ReadAll(request.Body)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, identityClaudeResponse)
			}))
			kind := contract.ServiceKindDroidSubscription
			service := contract.Service{
				ID: "service_droid", Name: "Droid", Kind: kind, Enabled: true, Models: []string{"claude-opus-4-6"},
				Capabilities: kind.SubscriptionProvider().Capabilities(),
				Subscription: &contract.SubscriptionConnection{Provider: kind.SubscriptionProvider(), Status: contract.SubscriptionStatusConnected, CredentialRef: "keyring://subscription/service_droid"},
			}
			candidate := endpoint.Resolved{Service: service, BaseURL: upstream.URL, UpstreamProtocol: contract.ProtocolAnthropicMessages}
			response, _ := serveRewrite(t, promptSettings(), []endpoint.Resolved{candidate}, "/v1/messages",
				`{"model":"claude-opus-4-6","max_tokens":64,"system":`+test.system+`,"messages":[{"role":"user","content":"hello"}]}`, nil)
			upstream.Close()
			if response.Code != http.StatusOK {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			equal(t, systemTexts(t, forwarded), test.want...)
		}
	})

	t.Run("official Codex", func(t *testing.T) {
		upstream := newCapturedUpstream(t, http.StatusOK, "text/event-stream",
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[]}}\n\n")
		harness := newIdentityHarness(t, rewriteSettings(contract.ReasoningModeFixed, contract.ReasoningLevelHigh), nil, codexConversionCandidate(upstream.url))
		harness.serve(t, "/v1/responses",
			`{"model":"gpt-6-astra","instructions":"Codex base","reasoning":{"effort":"low","summary":"auto"},"input":"hello",`+
				`"tools":[{"type":"function","name":"shell"}],"store":false,"stream":true}`,
			officialCodexHeaders(learnedCodexUA, "codex_cli_rs"))
		_, forwarded := upstream.request(t)
		want := `{"model":"gpt-6-astra","instructions":"` + testPromptPrefix + `\n\nCodex base\n\n` + testPromptSuffix +
			`","reasoning":{"effort":"high","summary":"auto"},"input":"hello","tools":[{"type":"function","name":"shell"}],"store":false,"stream":true}`
		if string(forwarded) != want {
			t.Fatalf("official Codex body = %s\nwant %s", forwarded, want)
		}
	})
}

func TestGlobalPromptKeepsBuiltInPromptsByteForByte(t *testing.T) {
	const billing = "x-anthropic-billing-header: cc_version=2.1.300.abc; cc_entrypoint=cli; cch=00000;"
	// Raw JSON string contents with the escapes the clients sent.
	builtIn := `Follow the user's instructions.\n\n# Tools\n- Wrap code in ` + jsonEscape("003c") + `code` + jsonEscape("003e") +
		`, keep \"quotes\" and paths like C:\\work, caf` + jsonEscape("00e9") + ` ` + jsonEscape("d83d") + jsonEscape("de00") + ` \/ done.`
	prefix, suffix := testPromptPrefix, testPromptSuffix

	t.Run("Claude Code", func(t *testing.T) {
		upstream := newCapturedUpstream(t, http.StatusOK, "application/json", identityClaudeResponse)
		harness := newIdentityHarness(t, promptSettings(), nil, claudeConversionCandidate(upstream.url))
		mainBlock := `{"type":"text","text":"` + builtIn + `","cache_control":{"type":"ephemeral"}}`
		body := strings.Replace(officialClaudeBody, `"messages":`,
			`"system":[{"type":"text","text":"`+billing+`"},{"type":"text","text":"`+claudeCodeBanner+`","cache_control":{"type":"ephemeral"}},`+mainBlock+`],`+
				`"tools":[{"name":"Read","input_schema":{"type":"object"}}],"messages":`, 1)
		harness.serve(t, "/v1/messages", body, officialClaudeHeaders(learnedClaudeUA))
		_, forwarded := upstream.request(t)
		want := `"system":[{"type":"text","text":"` + billing + `"},{"type":"text","text":"` + claudeCodeBanner + `","cache_control":{"type":"ephemeral"}},` +
			`{"text":"` + prefix + `","type":"text"},` + mainBlock + `,{"text":"` + suffix + `","type":"text"}]`
		if !strings.Contains(string(forwarded), want) {
			t.Fatalf("Claude Code system =\n%s\nwant it to contain\n%s", forwarded, want)
		}
	})

	t.Run("Codex", func(t *testing.T) {
		upstream := newCapturedUpstream(t, http.StatusOK, "text/event-stream",
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[]}}\n\n")
		harness := newIdentityHarness(t, promptSettings(), nil, codexConversionCandidate(upstream.url))
		harness.serve(t, "/v1/responses",
			`{"model":"gpt-6-astra","instructions":"`+builtIn+`","input":"hello","tools":[{"type":"function","name":"shell"}],"store":false,"stream":true}`,
			officialCodexHeaders(learnedCodexUA, "codex_cli_rs"))
		_, forwarded := upstream.request(t)
		want := `"instructions":"` + prefix + `\n\n` + builtIn + `\n\n` + suffix + `"`
		if !strings.Contains(string(forwarded), want) {
			t.Fatalf("Codex instructions =\n%s\nwant it to contain\n%s", forwarded, want)
		}
	})

	t.Run("Droid", func(t *testing.T) {
		var forwarded []byte
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			forwarded, _ = io.ReadAll(request.Body)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, identityClaudeResponse)
		}))
		defer upstream.Close()
		kind := contract.ServiceKindDroidSubscription
		service := contract.Service{
			ID: "service_droid", Name: "Droid", Kind: kind, Enabled: true, Models: []string{"claude-opus-4-6"},
			Capabilities: kind.SubscriptionProvider().Capabilities(),
			Subscription: &contract.SubscriptionConnection{Provider: kind.SubscriptionProvider(), Status: contract.SubscriptionStatusConnected, CredentialRef: "keyring://subscription/service_droid"},
		}
		candidate := endpoint.Resolved{Service: service, BaseURL: upstream.URL, UpstreamProtocol: contract.ProtocolAnthropicMessages}
		line := providerapi.DroidSystemPromptLine
		response, _ := serveRewrite(t, promptSettings(), []endpoint.Resolved{candidate}, "/v1/messages",
			`{"model":"claude-opus-4-6","max_tokens":64,"system":"`+line+`\n`+builtIn+`","tools":[{"name":"Execute","input_schema":{"type":"object"}}],`+
				`"messages":[{"role":"user","content":"hello"}]}`,
			http.Header{"User-Agent": {"factory-cli/0.30.0"}})
		if response.Code != http.StatusOK {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
		want := `"system":"` + line + `\n\n` + prefix + `\n` + builtIn + `\n\n` + suffix + `"`
		if !strings.Contains(string(forwarded), want) {
			t.Fatalf("Droid system =\n%s\nwant it to contain\n%s", forwarded, want)
		}
	})
}

func TestGlobalPromptNeverBecomesTheTitle(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "titles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings, err := store.GetRoutingSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.SystemPromptEnabled, settings.SystemPromptPrefix, settings.SystemPromptSuffix = true, testPromptPrefix, testPromptSuffix
	if err := store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		protocol contract.ProtocolID
		path     string
		body     string
		response string
		user     string
	}{
		{contract.ProtocolOpenAIChat, "/v1/chat/completions",
			`{"model":"public","messages":[{"role":"user","content":"修好登录页的报错"}]}`,
			`{"id":"chat_1","choices":[{"index":0,"message":{"role":"assistant","content":"好"},"finish_reason":"stop"}]}`, "修好登录页的报错"},
		{contract.ProtocolOpenAIResponses, "/v1/responses",
			`{"model":"public","input":"Rename the config loader"}`,
			`{"id":"resp_1","output":[]}`, "Rename the config loader"},
	} {
		var forwarded string
		handler := NewWithDependencies(Dependencies{
			Resolver:       candidateResolver{candidates: []endpoint.Resolved{{Endpoint: validEndpoint(test.protocol, false)}}},
			RequestRecords: store,
			Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(request.Body)
				forwarded = string(body)
				return jsonResponse(http.StatusOK, test.response), nil
			})),
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body)))
		if response.Code != http.StatusOK || !strings.Contains(forwarded, testPromptPrefix) {
			t.Fatalf("%s: response = %d, forwarded = %s", test.protocol, response.Code, forwarded)
		}
	}
	records, err := store.ListRequestRecords(ctx, storage.RequestRecordListOptions{Limit: 10})
	if err != nil || len(records.Items) != 2 {
		t.Fatalf("records = %+v, err = %v", records, err)
	}
	sessions, err := store.ListRequestSessions(ctx, storage.RequestSessionListOptions{})
	if err != nil || len(sessions.Items) != 2 {
		t.Fatalf("sessions = %+v, err = %v", sessions, err)
	}
	titles := map[string]bool{}
	for _, session := range sessions.Items {
		titles[session.Title] = true
	}
	for _, record := range records.Items {
		if record.InputPreview == nil || strings.Contains(*record.InputPreview, testPromptPrefix) {
			t.Fatalf("preview = %v", record.InputPreview)
		}
	}
	if !titles["修好登录页的报错"] || !titles["Rename the config loader"] {
		t.Fatalf("session titles = %v", titles)
	}
}

const geminiTestResponse = `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP","index":0}],` +
	`"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`

// conversation is a Messages or Chat history of text turns with messages
// turns in all, ending with the user's last.
func conversation(messages int, last string) string {
	turns := make([]string, 0, messages)
	for index := 0; index < messages-1; index++ {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		turns = append(turns, fmt.Sprintf(`{"role":%q,"content":"turn %d"}`, role, index))
	}
	return "[" + strings.Join(append(turns, `{"role":"user","content":`+last+`}`), ",") + "]"
}

// geminiConversation is conversation in Gemini contents.
func geminiConversation(messages int) string {
	turns := make([]string, 0, messages)
	for index := 0; index < messages; index++ {
		role := "user"
		if index%2 == 1 {
			role = "model"
		}
		turns = append(turns, fmt.Sprintf(`{"role":%q,"parts":[{"text":"turn %d"}]}`, role, index))
	}
	return "[" + strings.Join(turns, ",") + "]"
}

// claudeTools is a Messages tools array of count tools.
func claudeTools(count int) string {
	tools := make([]string, count)
	for index := range tools {
		tools[index] = fmt.Sprintf(`{"name":"tool_%d","input_schema":{"type":"object"}}`, index)
	}
	return "[" + strings.Join(tools, ",") + "]"
}

func functionTools(count int) string {
	tools := make([]string, count)
	for index := range tools {
		tools[index] = fmt.Sprintf(`{"type":"function","name":"tool_%d","parameters":{"type":"object"}}`, index)
	}
	return "[" + strings.Join(tools, ",") + "]"
}

// serveReasoning sends body to one API-key upstream of protocol and returns
// the body the upstream received and the request's record.
func serveReasoning(t *testing.T, settings contract.RoutingSettings, protocol contract.ProtocolID, models []string, path, body string, header http.Header) (string, contract.RequestRecord) {
	t.Helper()
	kind := contract.ServiceKindMagpie
	_, _, response := nativeProviderExchange(protocol, false)
	if protocol == contract.ProtocolGoogleGenerateContent {
		kind, response = contract.ServiceKindGemini, geminiTestResponse
	}
	upstream := newSequenceUpstream(t, "application/json", response)
	candidate := apiCandidate("service_api", kind, upstream.url, protocol)
	candidate.Service.Models = models
	got, store := serveRewrite(t, settings, []endpoint.Resolved{candidate}, path, body, header)
	if got.Code != http.StatusOK || len(upstream.requests()) != 1 {
		t.Fatalf("response = %d %s", got.Code, got.Body.String())
	}
	return upstream.requests()[0], store.root(t)
}

// gatewayDiagnostic is the record's gateway_reasoning message, empty when it
// has none.
func gatewayDiagnostic(record contract.RequestRecord) string {
	for _, diagnostic := range record.ConversionDiagnostics {
		if diagnostic.Code == gatewayReasoningDiagnostic {
			return diagnostic.Message
		}
	}
	return ""
}

func recordedEffort(record contract.RequestRecord) string {
	if record.ReasoningEffort == nil {
		return ""
	}
	return *record.ReasoningEffort
}

func TestReasoningSettingsEndToEnd(t *testing.T) {
	const (
		chat   = contract.ProtocolOpenAIChat
		claude = contract.ProtocolAnthropicMessages
		gemini = contract.ProtocolGoogleGenerateContent
	)
	fixed := func(level contract.ReasoningLevel) contract.RoutingSettings {
		return reasoningSettings(contract.ReasoningModeFixed, level)
	}
	check := func(t *testing.T, got string, record contract.RequestRecord, want, wantEffort, wantDiagnostic string) {
		t.Helper()
		if got != want {
			t.Fatalf("upstream body = %s\nwant          %s", got, want)
		}
		if effort := recordedEffort(record); effort != wantEffort {
			t.Fatalf("recorded reasoning effort = %q, want %q", effort, wantEffort)
		}
		if diagnostic := gatewayDiagnostic(record); diagnostic != wantDiagnostic {
			t.Fatalf("diagnostic = %q\nwant         %q", diagnostic, wantDiagnostic)
		}
	}

	t.Run("passthrough leaves the body byte for byte", func(t *testing.T) {
		body := `{"model":"gpt-5.4", "reasoning_effort" : "low","temperature":0.3,"messages":[{"role":"user","content":"hello"}]}`
		got, record := serveReasoning(t, reasoningSettings(contract.ReasoningModePassthrough, contract.ReasoningLevelMax), chat,
			[]string{"gpt-5.4"}, "/v1/chat/completions", body, nil)
		check(t, got, record, body, "low", "")
	})
	t.Run("fixed adds reasoning to a known model", func(t *testing.T) {
		got, record := serveReasoning(t, fixed(contract.ReasoningLevelHigh), chat, []string{"gpt-5.4"}, "/v1/chat/completions",
			`{"model":"gpt-5.4","temperature":0.3,"messages":[{"role":"user","content":"hello"}]}`, nil)
		check(t, got, record, `{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`,
			"high", "fixed: sent high, client asked nothing")
	})
	t.Run("fixed off on every model family", func(t *testing.T) {
		for _, test := range []struct {
			name, model, path string
			protocol          contract.ProtocolID
			body, want        string
			effort            string
		}{
			{"Opus 5.5 cannot turn off", "claude-opus-5-5", "/v1/messages", claude,
				`{"model":"claude-opus-5-5","max_tokens":2000,"messages":[{"role":"user","content":"hello"}]}`,
				`{"model":"claude-opus-5-5","max_tokens":2000,"messages":[{"role":"user","content":"hello"}],"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}}`, "low"},
			{"Sonnet 5.5 turns off between tools", "claude-sonnet-5-5", "/v1/messages", claude,
				`{"model":"claude-sonnet-5-5","max_tokens":2000,"messages":[{"role":"user","content":"hello"}]}`,
				`{"model":"claude-sonnet-5-5","max_tokens":2000,"messages":[{"role":"user","content":"hello"}],"thinking":{"type":"between_tools"}}`, "none"},
			{"GPT-5.4 sends none", "gpt-5.4", "/v1/chat/completions", chat,
				`{"model":"gpt-5.4","reasoning_effort":"high","messages":[{"role":"user","content":"hello"}]}`,
				`{"model":"gpt-5.4","reasoning_effort":"none","messages":[{"role":"user","content":"hello"}]}`, "none"},
			{"Gemini 2.5 sends 0", "gemini-2.5-flash", "/v1beta/models/gemini-2.5-flash:generateContent", gemini,
				`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
				`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":0}}}`, "none"},
		} {
			got, record := serveReasoning(t, fixed(contract.ReasoningLevelOff), test.protocol, []string{test.model}, test.path, test.body, nil)
			if got != test.want || recordedEffort(record) != test.effort {
				t.Fatalf("%s: upstream body = %s, recorded %q\nwant %s, %q", test.name, got, recordedEffort(record), test.want, test.effort)
			}
		}
	})
	t.Run("fixed max without a catalog is the model's highest effort unless the client asked for more", func(t *testing.T) {
		got, record := serveReasoning(t, fixed(contract.ReasoningLevelMax), chat, []string{"gpt-5.4"}, "/v1/chat/completions",
			`{"model":"gpt-5.4","reasoning_effort":"low","messages":[{"role":"user","content":"hello"}]}`, nil)
		check(t, got, record, `{"model":"gpt-5.4","reasoning_effort":"xhigh","messages":[{"role":"user","content":"hello"}]}`,
			"xhigh", "fixed: sent xhigh (model top), client asked low")
		got, record = serveReasoning(t, fixed(contract.ReasoningLevelMax), chat, []string{"gpt-5"}, "/v1/chat/completions",
			`{"model":"gpt-5","reasoning_effort":"low","messages":[{"role":"user","content":"hello"}]}`, nil)
		check(t, got, record, `{"model":"gpt-5","reasoning_effort":"high","messages":[{"role":"user","content":"hello"}]}`,
			"high", "fixed: sent high (model top), client asked low")
		body := `{"model":"gpt-5","reasoning_effort":"xhigh","messages":[{"role":"user","content":"hello"}]}`
		got, record = serveReasoning(t, fixed(contract.ReasoningLevelMax), chat, []string{"gpt-5"}, "/v1/chat/completions", body, nil)
		check(t, got, record, body, "xhigh", "fixed: sent xhigh (model top), client asked xhigh")
	})
	t.Run("fixed keeps a tool loop that has no thinking", func(t *testing.T) {
		body := `{"model":"claude-opus-4-7","max_tokens":2000,"messages":[{"role":"user","content":"fix"},` +
			`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}]},` +
			`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]}`
		got, record := serveReasoning(t, fixed(contract.ReasoningLevelHigh), claude, []string{"claude-opus-4-7"}, "/v1/messages", body, nil)
		check(t, got, record, body, "", "fixed: wanted high, kept client thinking: tool loop without thinking block, client asked nothing")
	})

	_, _, messagesResponse := nativeProviderExchange(contract.ProtocolAnthropicMessages, false)
	converted := func(t *testing.T, settings contract.RoutingSettings, upstreamModel, body string) (map[string]json.RawMessage, string) {
		t.Helper()
		upstream := newSequenceUpstream(t, "application/json", messagesResponse)
		candidate := apiCandidate("service_claude", contract.ServiceKindMagpie, upstream.url, contract.ProtocolAnthropicMessages, contract.Capability{
			Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true, ConvertTo: contract.ProtocolAnthropicMessages,
		})
		candidate.Service.Models = []string{"claude-model"}
		candidate.UpstreamModel = upstreamModel
		candidate.PlanType = contract.PlanTypeRelayKit
		response, store := serveRewrite(t, settings, []endpoint.Resolved{candidate}, "/v1/chat/completions", body, nil)
		if response.Code != http.StatusOK || len(upstream.requests()) != 1 {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(upstream.requests()[0]), &payload); err != nil {
			t.Fatal(err)
		}
		return payload, recordedEffort(store.root(t))
	}
	t.Run("fixed wins over a model-name suffix", func(t *testing.T) {
		payload, effort := converted(t, fixed(contract.ReasoningLevelMax), "claude-opus-4-7-low",
			`{"model":"claude-model","max_tokens":2000,"messages":[{"role":"user","content":"hello"}]}`)
		if string(payload["model"]) != `"claude-opus-4-7"` || gjson.GetBytes(payload["thinking"], "type").Str != "adaptive" ||
			string(payload["output_config"]) != `{"effort":"max"}` || effort != "max" {
			t.Fatalf("converted body: model %s thinking %s output_config %s, recorded = %q",
				payload["model"], payload["thinking"], payload["output_config"], effort)
		}
	})
	t.Run("fixed turns on thinking a converted client turned off", func(t *testing.T) {
		payload, effort := converted(t, fixed(contract.ReasoningLevelHigh), "claude-opus-4-7",
			`{"model":"claude-model","max_tokens":2000,"reasoning_effort":"none","messages":[{"role":"user","content":"hello"}]}`)
		if gjson.GetBytes(payload["thinking"], "type").Str != "adaptive" || string(payload["output_config"]) != `{"effort":"high"}` || effort != "high" {
			t.Fatalf("converted body: thinking %s output_config %s, recorded = %q", payload["thinking"], payload["output_config"], effort)
		}
	})
}

func TestAdaptiveEverydayFollowsTheConversation(t *testing.T) {
	_, _, messagesResponse := nativeProviderExchange(contract.ProtocolAnthropicMessages, false)
	upstream := newSequenceUpstream(t, "application/json", messagesResponse)
	candidate := apiCandidate("service_claude", contract.ServiceKindMagpie, upstream.url, contract.ProtocolAnthropicMessages)
	candidate.Service.Models = []string{"claude-opus-4-7"}
	store := newRedirectSettingsStore()
	// The custom range plays no part in the everyday preset.
	store.settings = adaptiveSettings(contract.AdaptivePresetEveryday, contract.ReasoningLevelHigh, contract.ReasoningLevelHigh)
	handler := NewWithDependencies(Dependencies{
		Resolver:       candidateResolver{candidates: []endpoint.Resolved{candidate}},
		Authorizer:     endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}),
		RequestRecords: store,
	})
	send := func(body string) string {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "claude-cli/2.1.300 (external, cli)")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
		requests := upstream.requests()
		return requests[len(requests)-1]
	}
	long := `"` + strings.Repeat("long ", 500) + `"`
	for round, test := range []struct {
		body, effort string
	}{
		{`{"model":"claude-opus-4-7","max_tokens":32000,"messages":` + conversation(1, `"fix the login bug"`) + `}`, "low"},
		{`{"model":"claude-opus-4-7","max_tokens":32000,"thinking":{"type":"adaptive"},"messages":` + conversation(11, `"and the tests"`) + `}`, "medium"},
		{`{"model":"claude-opus-4-7","max_tokens":32000,"thinking":{"type":"adaptive"},"tools":` + claudeTools(5) + `,"messages":` + conversation(11, `"and the tests"`) + `}`, "high"},
		{`{"model":"claude-opus-4-7","max_tokens":32000,"thinking":{"type":"adaptive"},"tools":` + claudeTools(5) + `,"messages":` + conversation(11, long) + `}`, "max"},
	} {
		got := send(test.body)
		if gjson.Get(got, "thinking").Raw != `{"type":"adaptive"}` || gjson.Get(got, "output_config").Raw != `{"effort":"`+test.effort+`"}` {
			t.Fatalf("request %d = %s", round+1, got)
		}
	}
	roots := store.roots(t)
	const cache = "; on the turn the level changes, Claude caches the whole conversation again"
	want := []struct{ effort, diagnostic string }{
		{"low", "adaptive everyday low..max: messages 1, tools 0, last 17 chars, 0 signals, sent low" + cache},
		{"medium", "adaptive everyday low..max: messages 11, tools 0, last 13 chars, 1 signal, sent medium" + cache},
		{"high", "adaptive everyday low..max: messages 11, tools 5, last 13 chars, 2 signals, sent high" + cache},
		{"max", "adaptive everyday low..max: messages 11, tools 5, last 2500 chars, 3 signals, sent max (upper bound)" + cache},
	}
	if len(roots) != len(want) {
		t.Fatalf("records = %+v", roots)
	}
	for index, expected := range want {
		if recordedEffort(roots[index]) != expected.effort || gatewayDiagnostic(roots[index]) != expected.diagnostic {
			t.Errorf("record %d: effort %q, diagnostic %q\nwant %q, %q", index+1,
				recordedEffort(roots[index]), gatewayDiagnostic(roots[index]), expected.effort, expected.diagnostic)
		}
	}
}

func TestAdaptiveResearchReachesEachModelsTop(t *testing.T) {
	research := adaptiveSettings(contract.AdaptivePresetResearch, contract.ReasoningLevelOff, contract.ReasoningLevelLow)
	const (
		chat   = contract.ProtocolOpenAIChat
		claude = contract.ProtocolAnthropicMessages
		gemini = contract.ProtocolGoogleGenerateContent
	)
	chatBody := func(model string, messages, tools int) string {
		body := `{"model":"` + model + `","messages":` + conversation(messages, `"go on"`)
		if tools > 0 {
			body += `,"tools":` + functionTools(tools)
		}
		return body + `}`
	}
	for _, test := range []struct {
		name, model, path string
		protocol          contract.ProtocolID
		body, field, want string
		effort            string
	}{
		{"a short chat thinks at medium", "gpt-5.4", "/v1/chat/completions", chat,
			chatBody("gpt-5.4", 1, 0), "reasoning_effort", `"medium"`, "medium"},
		{"one signal moves to high", "gpt-5.4", "/v1/chat/completions", chat,
			chatBody("gpt-5.4", 11, 0), "reasoning_effort", `"high"`, "high"},
		{"two signals reach xhigh on GPT-5.4", "gpt-5.4", "/v1/chat/completions", chat,
			chatBody("gpt-5.4", 11, 5), "reasoning_effort", `"xhigh"`, "xhigh"},
		{"two signals stop at high on GPT-5", "gpt-5", "/v1/chat/completions", chat,
			chatBody("gpt-5", 11, 5), "reasoning_effort", `"high"`, "high"},
		{"two signals stop at high on Gemini 3", "gemini-3-flash-preview", "/v1beta/models/gemini-3-flash-preview:generateContent", gemini,
			`{"contents":` + geminiConversation(11) + `,"tools":[{"functionDeclarations":[{"name":"a"},{"name":"b"},{"name":"c"},{"name":"d"}]}]}`,
			"generationConfig.thinkingConfig", `{"thinkingLevel":"high"}`, "high"},
		{"two signals take Gemini 2.5 Flash's largest budget", "gemini-2.5-flash", "/v1beta/models/gemini-2.5-flash:generateContent", gemini,
			`{"contents":` + geminiConversation(11) + `,"tools":[{"functionDeclarations":[{"name":"a"},{"name":"b"},{"name":"c"},{"name":"d"}]}]}`,
			"generationConfig.thinkingConfig", `{"thinkingBudget":24576}`, "high"},
		{"two signals keep Haiku 4.5's budget below max_tokens", "claude-haiku-4-5", "/v1/messages", claude,
			`{"model":"claude-haiku-4-5","max_tokens":32000,"tools":` + claudeTools(5) + `,"messages":` + conversation(11, `"go on"`) + `}`,
			"thinking", `{"type":"enabled","budget_tokens":31999}`, "high"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, record := serveReasoning(t, research, test.protocol, []string{test.model}, test.path, test.body, nil)
			if gjson.Get(got, test.field).Raw != test.want || recordedEffort(record) != test.effort {
				t.Fatalf("upstream body = %s, recorded %q", got, recordedEffort(record))
			}
			if diagnostic := gatewayDiagnostic(record); !strings.HasPrefix(diagnostic, "adaptive research medium..max: messages ") {
				t.Fatalf("diagnostic = %q", diagnostic)
			}
		})
	}
	_, record := serveReasoning(t, research, chat, []string{"gpt-5.4"}, "/v1/chat/completions", chatBody("gpt-5.4", 11, 5), nil)
	if diagnostic := gatewayDiagnostic(record); diagnostic != "adaptive research medium..max: messages 11, tools 5, last 5 chars, 2 signals, sent xhigh (model top)" {
		t.Fatalf("diagnostic = %q", diagnostic)
	}
}

func TestAdaptiveCustomRanges(t *testing.T) {
	custom := func(low, high contract.ReasoningLevel) contract.RoutingSettings {
		return adaptiveSettings(contract.AdaptivePresetCustom, low, high)
	}
	long := conversation(11, `"`+strings.Repeat("long ", 500)+`"`)
	t.Run("low to high stops at high with every signal", func(t *testing.T) {
		got, record := serveReasoning(t, custom(contract.ReasoningLevelLow, contract.ReasoningLevelHigh), contract.ProtocolOpenAIChat,
			[]string{"gpt-5.4"}, "/v1/chat/completions", `{"model":"gpt-5.4","tools":`+functionTools(5)+`,"messages":`+long+`}`, nil)
		if gjson.Get(got, "reasoning_effort").Str != "high" || recordedEffort(record) != "high" ||
			gatewayDiagnostic(record) != "adaptive custom low..high: messages 11, tools 5, last 2500 chars, 3 signals, sent high (upper bound)" {
			t.Fatalf("upstream body = %s, record %q %q", got, recordedEffort(record), gatewayDiagnostic(record))
		}
	})
	t.Run("off to max turns thinking off for a simple request", func(t *testing.T) {
		short := conversation(1, `"hello"`)
		for _, test := range []struct {
			name, model, path string
			protocol          contract.ProtocolID
			body, field, want string
			effort            string
		}{
			{"Opus 4.7", "claude-opus-4-7", "/v1/messages", contract.ProtocolAnthropicMessages,
				`{"model":"claude-opus-4-7","max_tokens":32000,"thinking":{"type":"adaptive"},"messages":` + short + `}`, "thinking", `{"type":"disabled"}`, "none"},
			{"Opus 5.5 cannot turn off", "claude-opus-5-5", "/v1/messages", contract.ProtocolAnthropicMessages,
				`{"model":"claude-opus-5-5","max_tokens":32000,"messages":` + short + `}`, "output_config", `{"effort":"low"}`, "low"},
			{"Haiku 4.5", "claude-haiku-4-5", "/v1/messages", contract.ProtocolAnthropicMessages,
				`{"model":"claude-haiku-4-5","max_tokens":32000,"thinking":{"type":"enabled","budget_tokens":4000},"messages":` + short + `}`, "thinking", `{"type":"disabled"}`, "none"},
			{"GPT-5.4", "gpt-5.4", "/v1/chat/completions", contract.ProtocolOpenAIChat,
				`{"model":"gpt-5.4","reasoning_effort":"high","messages":` + short + `}`, "reasoning_effort", `"none"`, "none"},
			{"Gemini 2.5 Flash", "gemini-2.5-flash", "/v1beta/models/gemini-2.5-flash:generateContent", contract.ProtocolGoogleGenerateContent,
				`{"contents":` + geminiConversation(1) + `}`, "generationConfig.thinkingConfig", `{"thinkingBudget":0}`, "none"},
		} {
			got, record := serveReasoning(t, custom(contract.ReasoningLevelOff, contract.ReasoningLevelMax), test.protocol, []string{test.model}, test.path, test.body, nil)
			if gjson.Get(got, test.field).Raw != test.want || recordedEffort(record) != test.effort {
				t.Fatalf("%s: upstream body = %s, recorded %q", test.name, got, recordedEffort(record))
			}
			if test.model == "claude-opus-5-5" && gjson.Get(got, "thinking").Raw != `{"type":"adaptive"}` {
				t.Fatalf("Opus 5.5 thinking = %s", gjson.Get(got, "thinking").Raw)
			}
		}
	})
	t.Run("medium to medium always sends medium", func(t *testing.T) {
		for _, messages := range []string{conversation(1, `"hello"`), long} {
			got, record := serveReasoning(t, custom(contract.ReasoningLevelMedium, contract.ReasoningLevelMedium), contract.ProtocolOpenAIChat,
				[]string{"gpt-5.4"}, "/v1/chat/completions", `{"model":"gpt-5.4","tools":`+functionTools(5)+`,"messages":`+messages+`}`, nil)
			if gjson.Get(got, "reasoning_effort").Str != "medium" || recordedEffort(record) != "medium" {
				t.Fatalf("upstream body = %s, recorded %q", got, recordedEffort(record))
			}
		}
	})
}

func TestAdaptiveCodexUsesTheCatalogsTopLevel(t *testing.T) {
	// Research with two signals: five tools and a long input.
	body := `{"model":"gpt-6-astra","instructions":"Codex base","reasoning":{"effort":"medium","summary":"auto"},"input":"` + strings.Repeat("x", 2001) + `",` +
		`"tools":` + functionTools(5) + `,"store":false,"stream":true}`
	for _, test := range []struct {
		name    string
		catalog string
		want    string
	}{
		{"catalog with xhigh", `[{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"}]`, "xhigh"},
		{"catalog without xhigh", `[{"effort":"low"},{"effort":"high"}]`, "high"},
		{"no catalog", ``, "xhigh"},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := newCapturedUpstream(t, http.StatusOK, "text/event-stream",
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[]}}\n\n")
			harness := newIdentityHarness(t, adaptiveSettings(contract.AdaptivePresetResearch, contract.ReasoningLevelLow, contract.ReasoningLevelMax), nil, codexConversionCandidate(upstream.url))
			if test.catalog != "" {
				harness.handler.(*Handler).codexModels.remember([]discoveryEntry{{
					id: "gpt-6-astra", codexCatalog: []byte(`{"slug":"gpt-6-astra","supported_reasoning_levels":` + test.catalog + `}`),
				}})
			}
			harness.serve(t, "/v1/responses", body, officialCodexHeaders(learnedCodexUA, "codex_cli_rs"))
			_, forwarded := upstream.request(t)
			want := strings.Replace(body, `"effort":"medium"`, `"effort":"`+test.want+`"`, 1)
			if string(forwarded) != want {
				t.Fatalf("forwarded = %.300s\nwant      %.300s", forwarded, want)
			}
		})
	}
}

func TestClaudeCodeHaikuRequestsStayUntouched(t *testing.T) {
	const billing = "x-anthropic-billing-header: cc_version=2.1.300.abc; cc_entrypoint=cli; cch=00000;"
	settings := rewriteSettings(contract.ReasoningModeAdaptive, contract.ReasoningLevelHigh)
	header := officialClaudeHeaders(learnedClaudeUA)
	body := func(model string) string {
		return `{"model":"` + model + `","max_tokens":512,` +
			`"system":[{"type":"text","text":"` + billing + `"},{"type":"text","text":"` + claudeCodeBanner + `"},` +
			`{"type":"text","text":"Summarize this coding conversation in under 50 characters."}],` +
			`"messages":` + conversation(11, `"fix the login bug"`) + `}`
	}
	_, _, messagesResponse := nativeProviderExchange(contract.ProtocolAnthropicMessages, false)
	serve := func(t *testing.T, settings contract.RoutingSettings, model string, header http.Header) (string, contract.RequestRecord) {
		t.Helper()
		upstream := newSequenceUpstream(t, "application/json", messagesResponse)
		candidate := apiCandidate("service_claude", contract.ServiceKindMagpie, upstream.url, contract.ProtocolAnthropicMessages)
		candidate.Service.Models = []string{"claude-haiku-4-5-20251001", "claude-sonnet-4-5"}
		response, store := serveRewrite(t, settings, []endpoint.Resolved{candidate}, "/v1/messages", body(model), header)
		if response.Code != http.StatusOK || len(upstream.requests()) != 1 {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
		return upstream.requests()[0], store.root(t)
	}

	t.Run("byte for byte", func(t *testing.T) {
		for _, model := range []string{"claude-haiku-4-5-20251001", "claude-3-5-haiku-20241022"} {
			got, record := serve(t, settings, model, header)
			if got != body(model) {
				t.Fatalf("%s changed:\n%s", model, got)
			}
			if gatewayDiagnostic(record) != "background request (Claude Code haiku): unchanged" || record.ReasoningEffort != nil {
				t.Fatalf("%s record = %+v", model, record)
			}
		}
	})
	t.Run("the rule reads the model the client asked for", func(t *testing.T) {
		redirected := settings
		redirected.ModelRedirects = []contract.ModelRedirect{enabledRedirect("claude-haiku-4-5-20251001", "claude-sonnet-4-5")}
		got, _ := serve(t, redirected, "claude-haiku-4-5-20251001", header)
		want := strings.Replace(body("claude-haiku-4-5-20251001"), `"model":"claude-haiku-4-5-20251001"`, `"model":"claude-sonnet-4-5"`, 1)
		if got != want {
			t.Fatalf("redirected Haiku request changed beyond its model:\n%s", got)
		}
		redirected.ModelRedirects = []contract.ModelRedirect{enabledRedirect("claude-sonnet-4-5", "claude-haiku-4-5-20251001")}
		got, _ = serve(t, redirected, "claude-sonnet-4-5", header)
		if !strings.Contains(got, testPromptPrefix) || gjson.Get(got, "thinking.type").Str != "enabled" {
			t.Fatalf("a request for another model redirected to Haiku is the main conversation:\n%s", got)
		}
	})
	t.Run("other clients", func(t *testing.T) {
		got, record := serve(t, settings, "claude-haiku-4-5-20251001", http.Header{"User-Agent": {"factory-cli/0.30.0"}})
		if !strings.Contains(got, testPromptPrefix) || !strings.Contains(got, testPromptSuffix) ||
			gjson.Get(got, "thinking.type").Str != "enabled" || recordedEffort(record) == "" {
			t.Fatalf("Droid's Haiku request is its main conversation:\n%s", got)
		}
	})
}

func TestReasoningRecordsArePersisted(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "reasoning.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings, err := store.GetRoutingSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.ReasoningMode, settings.AdaptivePreset = contract.ReasoningModeAdaptive, contract.AdaptivePresetResearch
	if err := store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	serve := func(protocol contract.ProtocolID, model, path, body string) string {
		t.Helper()
		_, _, response := nativeProviderExchange(protocol, false)
		upstream := newSequenceUpstream(t, "application/json", response)
		candidate := apiCandidate(contract.ServiceID("service_"+strings.ReplaceAll(string(protocol), ".", "_")), contract.ServiceKindMagpie, upstream.url, protocol)
		candidate.Service.Models = []string{model}
		handler := NewWithDependencies(Dependencies{
			Resolver:         candidateResolver{candidates: []endpoint.Resolved{candidate}},
			Authorizer:       endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}).WithRoutingSettings(store),
			RequestRecords:   store,
			ConversionEngine: relaykitbridge.NewEngine(),
		})
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || len(upstream.requests()) != 1 {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
		return upstream.requests()[0]
	}
	codex := serve(contract.ProtocolOpenAIResponses, "gpt-5.4", "/v1/responses",
		`{"model":"gpt-5.4","reasoning":{"effort":"low"},"input":"hello","tools":`+functionTools(5)+`}`)
	if gjson.Get(codex, "reasoning.effort").Str != "high" {
		t.Fatalf("Responses body = %s", codex)
	}
	toolResult := `[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]`
	loop := `{"model":"claude-opus-4-7","max_tokens":2000,"messages":[{"role":"user","content":"fix"},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}]},{"role":"user","content":` + toolResult + `}]}`
	if forwarded := serve(contract.ProtocolAnthropicMessages, "claude-opus-4-7", "/v1/messages", loop); forwarded != loop {
		t.Fatalf("guarded body changed: %s", forwarded)
	}

	records, err := store.ListRequestRecords(ctx, storage.RequestRecordListOptions{Limit: 10})
	if err != nil || len(records.Items) != 2 {
		t.Fatalf("records = %+v, err = %v", records, err)
	}
	byModel := map[string]contract.RequestRecord{}
	for _, record := range records.Items {
		if record.RequestedModel != nil {
			byModel[*record.RequestedModel] = record
		}
	}
	want := map[string]struct{ effort, message string }{
		"gpt-5.4": {"high", "adaptive research medium..max: messages 1, tools 5, last 5 chars, 1 signal, sent high"},
		"claude-opus-4-7": {"", fmt.Sprintf("adaptive research medium..max: messages 3, tools 0, last %d chars, 0 signals, wanted medium, kept client thinking: tool loop without thinking block",
			len(toolResult))},
	}
	for model, expected := range want {
		record := byModel[model]
		diagnostics := []contract.ConversionDiagnostic{{
			Phase: contract.ConversionDiagnosticPhaseRequest, Severity: contract.ConversionDiagnosticWarning,
			Code: gatewayReasoningDiagnostic, Path: "reasoning", Message: expected.message,
		}}
		// A changed level is also noted as a rewrite, ahead of the explanation.
		if expected.effort != "" {
			diagnostics = append([]contract.ConversionDiagnostic{{
				Phase: contract.ConversionDiagnosticPhaseRequest, Severity: contract.ConversionDiagnosticWarning,
				Code: contract.GatewayRewriteDiagnostic, Path: string(contract.GatewayRewriteReasoning), Message: "sent reasoning " + expected.effort,
			}}, diagnostics...)
		}
		if recordedEffort(record) != expected.effort || !slices.Equal(record.ConversionDiagnostics, diagnostics) {
			t.Fatalf("%s record: effort %q, diagnostics %+v", model, recordedEffort(record), record.ConversionDiagnostics)
		}
	}
}

func TestModelDiscoveryRemembersCodexCatalog(t *testing.T) {
	handler := codexCatalogTestHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.158.0", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	model := handler.codexModels.lookup("gpt-model")
	if strings.Join(model.levels, ",") != "high" {
		t.Fatalf("remembered catalog = %+v", model)
	}
	edit := overrideReasoning(contract.ProtocolOpenAIResponses, []byte(`{"reasoning":{"effort":"low"}}`),
		"gpt-model", levelTarget("max", 131072), model)
	if edit.err != nil || edit.sent != "high" || string(edit.body) != `{"reasoning":{"effort":"high"}}` {
		t.Fatalf("override = %s %q %v", edit.body, edit.sent, edit.err)
	}
}
