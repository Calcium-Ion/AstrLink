package privacy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// TestDisabledKindsAreSuppressedWithAReason keeps the per-kind switch on the
// same path as every other suppression, so a dry-run can say why a visible
// match was left alone instead of showing an unexplained gap.
func TestDisabledKindsAreSuppressedWithAReason(t *testing.T) {
	policy := naturalPolicy()
	policy.KindRules[KindEmail] = KindRule{
		Enabled: false, Style: contract.PlaceholderStyleNatural,
	}
	engine := mustTestEngine(t)
	result, err := engine.Inspect(
		t.Context(),
		policy,
		contract.ProtocolOpenAIChat,
		[]byte(`{"messages":[{"role":"user","content":"mail alice@example.com card 4242 4242 4242 4242"}]}`),
	)
	if err != nil || result.Decision != DecisionRedact {
		t.Fatalf("inspect: %#v %v", result, err)
	}
	if !strings.Contains(string(result.Body), "alice@example.com") {
		t.Fatalf("disabled kind was still redacted: %s", result.Body)
	}
	found := false
	for _, finding := range result.SuppressedFindings {
		if finding.Kind != KindEmail {
			continue
		}
		found = true
		if finding.Suppression != SuppressionKindDisabled {
			t.Fatalf("suppression reason = %q", finding.Suppression)
		}
	}
	if !found {
		t.Fatalf("disabled email was not reported as suppressed: %#v", result.SuppressedFindings)
	}
}

// TestAllowlistExemptsHostsAcrossValueShapes covers the reason host matching
// resolves the value first: one domain_suffix rule has to cover a bare host, a
// URL on that host, and an address at that domain, or an operator would have to
// enumerate every form a link can take.
func TestAllowlistExemptsHostsAcrossValueShapes(t *testing.T) {
	policy := naturalPolicy()
	policy.Allowlist = []contract.PolicyAllowlistRule{
		{Type: contract.PolicyAllowlistTypeDomainSuffix, Value: "github.com"},
		{Type: contract.PolicyAllowlistTypeCIDR, Value: "127.0.0.0/8"},
		{Type: contract.PolicyAllowlistTypeLiteral, Value: "ops@astrlink.test"},
	}
	engine := mustTestEngine(t)
	body := []byte(`{"messages":[{"role":"user","content":` +
		`"see https://api.github.com/repos/x mail bot@github.com or ops@astrlink.test ` +
		`local http://127.0.0.1:8317/v1 and mail alice@example.com"}]}`)
	result, err := engine.Inspect(t.Context(), policy, contract.ProtocolOpenAIChat, body)
	if err != nil {
		t.Fatal(err)
	}
	redacted := string(result.Body)
	for _, kept := range []string{
		"https://api.github.com/repos/x",
		"bot@github.com",
		"ops@astrlink.test",
		"http://127.0.0.1:8317/v1",
	} {
		if !strings.Contains(redacted, kept) {
			t.Fatalf("allowlisted %q was redacted: %s", kept, redacted)
		}
	}
	if strings.Contains(redacted, "alice@example.com") {
		t.Fatalf("non-allowlisted address survived: %s", redacted)
	}
	for _, finding := range result.SuppressedFindings {
		if finding.Suppression != SuppressionAllowlisted &&
			finding.Suppression != SuppressionKindDisabled {
			t.Fatalf("unexpected suppression reason %q", finding.Suppression)
		}
	}
}

// TestToolDefinitionsAreOutOfScope pins the scope reduction. A harness ships
// dozens of tool descriptions full of documentation links and example
// addresses, none of which is user data, and redacting them both wrecked the
// schema the model reads and inflated the hit count past any useful signal.
func TestToolDefinitionsAreOutOfScope(t *testing.T) {
	engine := mustTestEngine(t)
	body := []byte(`{
		"messages":[{"role":"user","content":"contact alice@example.com"}],
		"tools":[{"type":"function","function":{
			"name":"lookup",
			"description":"Look up a customer, see https://docs.example.com/api or mail dev@example.com",
			"parameters":{"type":"object","properties":{"email":{"type":"string","description":"like bob@example.com"}}}
		}}]
	}`)
	result, err := engine.Inspect(t.Context(), tokenPolicy(), contract.ProtocolOpenAIChat, body)
	if err != nil || result.Decision != DecisionRedact {
		t.Fatalf("inspect: %#v %v", result, err)
	}
	redacted := string(result.Body)
	for _, kept := range []string{
		"https://docs.example.com/api", "dev@example.com", "bob@example.com",
	} {
		if !strings.Contains(redacted, kept) {
			t.Fatalf("tool definition text %q was redacted: %s", kept, redacted)
		}
	}
	if strings.Contains(redacted, "alice@example.com") {
		t.Fatalf("conversation content was not redacted: %s", redacted)
	}
	if len(result.Redactions) != 1 {
		t.Fatalf("redactions = %#v", result.Redactions)
	}
}

// TestNoticeIsInjectedOnlyWhenATokenPlaceholderIsEmitted keeps the cacheable
// prefix stable in the common case. A natural stand-in needs no explanation
// because it is already a well-formed value of its type, so injecting the note
// anyway would perturb the prefix for nothing.
func TestNoticeIsInjectedOnlyWhenATokenPlaceholderIsEmitted(t *testing.T) {
	engine := mustTestEngine(t)
	natural := naturalPolicy()
	natural.PlaceholderNotice = true

	result, err := engine.Inspect(
		t.Context(),
		natural,
		contract.ProtocolOpenAIChat,
		[]byte(`{"messages":[{"role":"user","content":"mail alice@example.com"}]}`),
	)
	if err != nil || result.Decision != DecisionRedact {
		t.Fatalf("natural inspect: %#v %v", result, err)
	}
	if result.NoticeInjected {
		t.Fatalf("notice injected for natural stand-ins: %s", result.Body)
	}

	// common_secret is locked to the token shape, so a request carrying one
	// always needs the convention explained.
	result, err = engine.Inspect(
		t.Context(),
		natural,
		contract.ProtocolOpenAIChat,
		[]byte(`{"messages":[{"role":"system","content":"api_key=abcdefghijklmnop123456"}]}`),
	)
	if err != nil || result.Decision != DecisionRedact {
		t.Fatalf("secret inspect: %#v %v", result, err)
	}
	if !result.NoticeInjected {
		t.Fatalf("notice missing for a token placeholder: %s", result.Body)
	}
	if !strings.Contains(string(result.Body), "redaction markers of the form") {
		t.Fatalf("notice text absent from the body: %s", result.Body)
	}

	disabled := natural
	disabled.PlaceholderNotice = false
	result, err = engine.Inspect(
		t.Context(),
		disabled,
		contract.ProtocolOpenAIChat,
		[]byte(`{"messages":[{"role":"system","content":"api_key=abcdefghijklmnop123456"}]}`),
	)
	if err != nil || result.Decision != DecisionRedact {
		t.Fatalf("disabled-notice inspect: %#v %v", result, err)
	}
	if result.NoticeInjected ||
		strings.Contains(string(result.Body), "redaction markers of the form") {
		t.Fatalf("notice injected while disabled: %s", result.Body)
	}
}

// TestNoticeIsNotInjectedTwiceAcrossTurns matters because the client resends the
// whole conversation: a note appended each turn would grow without bound and
// invalidate the prefix cache every time.
func TestNoticeIsNotInjectedTwiceAcrossTurns(t *testing.T) {
	engine := mustTestEngine(t)
	policy := tokenPolicy()
	policy.PlaceholderNotice = true
	body := []byte(`{"messages":[{"role":"system","content":"be brief"},` +
		`{"role":"user","content":"mail alice@example.com"}]}`)
	first, err := engine.Inspect(t.Context(), policy, contract.ProtocolOpenAIChat, body)
	if err != nil || !first.NoticeInjected {
		t.Fatalf("first inspect: %#v %v", first, err)
	}
	// The next turn replays the annotated history and adds a fresh address, so
	// the request is redacted again and the notice path runs a second time.
	second, err := engine.Inspect(
		t.Context(),
		policy,
		contract.ProtocolOpenAIChat,
		appendChatMessage(t, first.Body, "and bob@example.com"),
	)
	if err != nil || second.Decision != DecisionRedact {
		t.Fatalf("second inspect: %#v %v", second, err)
	}
	if second.NoticeInjected {
		t.Fatalf("notice injected a second time: %s", second.Body)
	}
	if strings.Count(string(second.Body), "redaction markers of the form") != 1 {
		t.Fatalf("notice duplicated: %s", second.Body)
	}
}

func TestNoticeDeduplicatesAcrossSystemMessages(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		body     string
	}{
		{"later system", contract.ProtocolOpenAIChat, `{"messages":[{"role":"system","content":"new prefix"},{"content":"__NOTICE__","role":"system"},{"role":"user","content":"alice@example.com"}]}`},
		{"later developer blocks", contract.ProtocolOpenAIChat, `{"messages":[{"role":"system","content":"new prefix"},{"content":[{"z":null,"text":"__NOTICE__","type":"text"}],"role":"developer"},{"role":"user","content":"alice@example.com"}]}`},
		{"null prefix", contract.ProtocolOpenAIChat, `{"messages":[{"content":null,"role":"system"},{"role":"developer","content":"__NOTICE__"},{"role":"user","content":"alice@example.com"}]}`},
		{"empty blocks prefix", contract.ProtocolOpenAIChat, `{"messages":[{"role":"developer","content":[]},{"role":"system","content":"__NOTICE__"},{"role":"user","content":"alice@example.com"}]}`},
		{"after user message", contract.ProtocolOpenAIChat, `{"messages":[{"role":"user","content":"alice@example.com"},{"role":"system","content":"__NOTICE__"}]}`},
		{"responses input", contract.ProtocolOpenAIResponses, `{"instructions":"new prefix","input":[{"role":"developer","content":[{"text":"__NOTICE__","type":"input_text"}]},{"role":"user","content":"alice@example.com"}]}`},
		{"compact input", contract.ProtocolOpenAIResponsesCompact, `{"input":[{"role":"system","content":"__NOTICE__"},{"role":"user","content":"alice@example.com"}]}`},
		{"anthropic later block", contract.ProtocolAnthropicMessages, `{"system":[{"type":"text","text":"new prefix"},{"text":"__NOTICE__","type":"text"}],"messages":[{"role":"user","content":"alice@example.com"}]}`},
		{"gemini later part", contract.ProtocolGoogleGenerateContent, `{"systemInstruction":{"parts":[{"text":"new prefix"},{"text":"__NOTICE__"}]},"contents":[{"parts":[{"text":"alice@example.com"}]}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := tokenPolicy()
			policy.PlaceholderNotice = true
			body := strings.ReplaceAll(test.body, "__NOTICE__", placeholderNotice)
			result, err := mustTestEngine(t).Inspect(t.Context(), policy, test.protocol, []byte(body))
			if err != nil || result.Decision != DecisionRedact || result.NoticeInjected {
				t.Fatalf("inspect: decision=%s notice=%t err=%v", result.Decision, result.NoticeInjected, err)
			}
			if count := strings.Count(string(result.Body), "redaction markers of the form"); count != 1 {
				t.Fatalf("notice count=%d, want 1", count)
			}
			assertOnlyRedactionsChanged(t, body, result)
		})
	}
}

func TestUserAndAssistantNoticeQuotesDoNotSuppressSystemNotice(t *testing.T) {
	for _, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIChat, contract.ProtocolOpenAIResponses} {
		for _, role := range []string{"user", "assistant"} {
			t.Run(string(protocol)+"/"+role, func(t *testing.T) {
				body := `{"messages":[{"role":"system","content":"be brief"},{"role":"` + role + `","content":"` + placeholderNotice + `"},{"role":"user","content":"alice@example.com"}]}`
				if protocol == contract.ProtocolOpenAIResponses {
					body = strings.Replace(body, `"messages":`, `"input":`, 1)
				}
				policy := tokenPolicy()
				policy.PlaceholderNotice = true
				result, err := mustTestEngine(t).Inspect(t.Context(), policy, protocol, []byte(body))
				if err != nil || !result.NoticeInjected {
					t.Fatalf("notice wrongly suppressed by %s quote: %v", role, err)
				}
			})
		}
	}
}

func appendChatMessage(t *testing.T, body []byte, text string) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	messages, _ := document["messages"].([]any)
	document["messages"] = append(
		messages,
		map[string]any{"role": "user", "content": text},
	)
	next, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

// TestNoticeReachesTheSystemChannelOfEveryProtocol guards the per-protocol
// wiring: a note written to the wrong field is silently ignored by the upstream
// and the model goes back to paraphrasing markers.
func TestNoticeReachesTheSystemChannelOfEveryProtocol(t *testing.T) {
	policy := tokenPolicy()
	policy.PlaceholderNotice = true
	for _, test := range []struct {
		protocol contract.ProtocolID
		body     string
		injected bool
	}{
		{
			contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"user","content":"mail alice@example.com"}]}`,
			true,
		},
		{
			contract.ProtocolOpenAIResponses,
			`{"instructions":"be brief","input":"mail alice@example.com"}`,
			true,
		},
		{
			contract.ProtocolAnthropicMessages,
			`{"system":"be brief","messages":[{"role":"user","content":"mail alice@example.com"}]}`,
			true,
		},
		{
			contract.ProtocolGoogleGenerateContent,
			`{"contents":[{"role":"user","parts":[{"text":"mail alice@example.com"}]}]}`,
			true,
		},
		{
			// A raw completion has no system channel, and prefixing the prompt
			// would change what the model is asked to continue.
			contract.ProtocolOpenAICompletions,
			`{"prompt":"mail alice@example.com"}`,
			false,
		},
	} {
		t.Run(string(test.protocol), func(t *testing.T) {
			engine := mustTestEngine(t)
			result, err := engine.Inspect(
				t.Context(), policy, test.protocol, []byte(test.body),
			)
			if err != nil || result.Decision != DecisionRedact {
				t.Fatalf("inspect: %#v %v", result, err)
			}
			if result.NoticeInjected != test.injected {
				t.Fatalf("notice injected = %v, want %v: %s",
					result.NoticeInjected, test.injected, result.Body)
			}
			present := strings.Contains(string(result.Body), "redaction markers of the form")
			if present != test.injected {
				t.Fatalf("notice text present = %v: %s", present, result.Body)
			}
		})
	}
}
