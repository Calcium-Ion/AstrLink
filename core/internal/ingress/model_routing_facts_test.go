package ingress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// routeLive sends one client request through ServeHTTP to group and returns
// the routing decision the request record keeps, with the response status.
func routeLive(t *testing.T, group contract.ModelRoutingGroup, path, body string, configure ...func(*Handler)) (*contract.ModelRoutingDecision, int) {
	t.Helper()
	store := newRedirectSettingsStore()
	store.settings.ModelRoutingGroups = []contract.ModelRoutingGroup{group}
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(request.Protocol, false), UpstreamModel: request.Model}, nil
		}),
		RequestRecords: store,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
			_, _ = writer.Write([]byte(`{}`))
			return nil
		}),
	})
	for _, apply := range configure {
		apply(handler)
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	decision := store.root(t).ModelRouting
	if decision == nil {
		t.Fatalf("no routing decision; status=%d body=%s", response.Code, response.Body.String())
	}
	return decision, response.Code
}

// factsGroup routes to "matched" when any rule holds and to "fallback"
// otherwise; each rule's ID names the case it stands for.
func factsGroup(rules ...contract.ModelRoutingRule) contract.ModelRoutingGroup {
	return contract.ModelRoutingGroup{
		ID: "facts", Enabled: true,
		Members: []contract.ModelRoutingMember{
			{ID: "fallback", ServiceID: "endpoint_test", Model: "fallback-model"},
			{ID: "matched", ServiceID: "endpoint_test", Model: "matched-model"},
		},
		DefaultMemberID: "fallback",
		Rules:           rules,
	}
}

func factsRule(id string, priority int, when contract.ModelRoutingCondition) contract.ModelRoutingRule {
	return contract.ModelRoutingRule{ID: id, Priority: priority, When: when, TargetMemberID: "matched"}
}

func observedValue(decision *contract.ModelRoutingDecision, field string) (string, bool) {
	for _, rule := range decision.Rules {
		for _, observation := range rule.Fields {
			if observation.Field == field {
				return observation.Value, observation.Known
			}
		}
	}
	return "", false
}

// The character count covers the whole latest user message, not the prefix
// conversation linking keeps, so a long-message rule can fire.
func TestModelRoutingCountsTheWholeUserMessage(t *testing.T) {
	group := factsGroup(factsRule("long", 1, contract.ModelRoutingCondition{Field: "input_text_runes", Gte: intRef(5000)}))
	for _, test := range []struct {
		name, text, runes string
		matched           bool
	}{
		{name: "ascii", text: strings.Repeat("a", 10000), runes: "10000", matched: true},
		{name: "cjk", text: strings.Repeat("汉", 6000), runes: "6000", matched: true},
		{name: "short", text: "hello", runes: "5", matched: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `{"model":"astrlink/facts","messages":[{"role":"user","content":"` + test.text + `"}]}`
			decision, code := routeLive(t, group, "/v1/chat/completions", body)
			if code != http.StatusOK || (decision.RuleID == "long") != test.matched {
				t.Fatalf("status=%d decision=%+v, want matched=%v", code, decision, test.matched)
			}
			if value, known := observedValue(decision, "input_text_runes"); !known || value != test.runes {
				t.Fatalf("input_text_runes = %q (known=%v), want %s", value, known, test.runes)
			}
		})
	}
}

// Keyword conditions read the user's latest message as text: not the JSON
// around it, not the system prompt, and not only the first 64 KiB of a long
// conversation.
func TestModelRoutingKeywordsReadTheLatestUserMessage(t *testing.T) {
	history := strings.Repeat(`{"role":"user","content":"`+strings.Repeat("background ", 100)+`"},{"role":"assistant","content":"noted"},`, 80)
	for _, test := range []struct {
		name, keyword, body string
		matched             bool
	}{
		{name: "escaped CJK text", keyword: "翻译",
			body: `{"model":"astrlink/facts","messages":[{"role":"user","content":"请翻译这段"}]}`, matched: true},
		{name: "JSON field name", keyword: "role",
			body: `{"model":"astrlink/facts","messages":[{"role":"user","content":"hello"}]}`},
		{name: "image part type", keyword: "image",
			body: `{"model":"astrlink/facts","messages":[{"role":"user","content":[{"type":"text","text":"what is this"},{"type":"image_url","image_url":{"url":"https://example.test/a.png"}}]}]}`},
		{name: "system prompt only", keyword: "translate",
			body: `{"model":"astrlink/facts","messages":[{"role":"system","content":"translate everything"},{"role":"user","content":"hello"}]}`},
		{name: "after long history", keyword: "urgent",
			body: `{"model":"astrlink/facts","messages":[` + history + `{"role":"user","content":"urgent: fix the build"}]}`, matched: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			group := factsGroup(factsRule("keyword", 1, contract.ModelRoutingCondition{Field: "literal_keyword", ContainsAny: []string{test.keyword}}))
			decision, code := routeLive(t, group, "/v1/chat/completions", test.body)
			if code != http.StatusOK || (decision.RuleID == "keyword") != test.matched {
				t.Fatalf("status=%d rule=%q, want matched=%v", code, decision.RuleID, test.matched)
			}
		})
	}
}

// With a redacting privacy policy, an agent's tool loop still classifies the
// user's latest message, redacted: a tool result is not the user's words, and
// the redacted view must find the same message the plain one does.
func TestModelRoutingPrivacyKeepsTheUserMessageInToolLoops(t *testing.T) {
	group := factsGroup(
		factsRule("coding", 1, contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}}),
	)
	for _, test := range []struct{ name, path, body string }{
		{name: "anthropic", path: "/v1/messages",
			body: `{"model":"astrlink/facts","max_tokens":64,"messages":[{"role":"user","content":"write go code for alice@example.com"},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"package main"}]}]}`},
		{name: "responses", path: "/v1/responses",
			body: `{"model":"astrlink/facts","input":[{"role":"user","content":"write go code for alice@example.com"},{"type":"function_call","call_id":"c1","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"package main"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var texts []string
			decision, code := routeLive(t, group, test.path, test.body, func(handler *Handler) {
				handler.privacyFilter = testPrivacyEngine(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact}, nil)
				handler.classifier = ClassifierFunc(func(_ context.Context, text string) autoclassifier.Outcome {
					mu.Lock()
					texts = append(texts, text)
					mu.Unlock()
					return autoclassifier.Outcome{Category: "coding"}
				})
			})
			if code != http.StatusOK || decision.RuleID != "coding" {
				t.Fatalf("status=%d decision=%+v, want the coding rule", code, decision)
			}
			if len(texts) != 1 || !strings.Contains(texts[0], "write go code for") || strings.Contains(texts[0], "alice@example.com") {
				t.Fatalf("classifier texts = %q, want the redacted user message", texts)
			}
		})
	}
}

// Text the policy blocks is withheld from routing: a keyword rule cannot be
// judged and does not fire, while the character count, which reveals none of
// the text, still reads the message as sent.
func TestModelRoutingPrivacyBlockWithholdsTextNotCounts(t *testing.T) {
	group := factsGroup(
		factsRule("keyword", 1, contract.ModelRoutingCondition{Field: "literal_keyword", ContainsAny: []string{"mail"}}),
		factsRule("short", 2, contract.ModelRoutingCondition{Field: "input_text_runes", Lte: intRef(100)}),
	)
	body := `{"model":"astrlink/facts","messages":[{"role":"user","content":"mail alice@example.com"}]}`
	decision, code := routeLive(t, group, "/v1/chat/completions", body, func(handler *Handler) {
		handler.privacyFilter = testPrivacyEngine(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionBlock}, nil)
	})
	if code != http.StatusForbidden {
		t.Fatalf("status=%d, want the block", code)
	}
	if decision.RuleID != "short" {
		t.Fatalf("decision = %+v, want the character-count rule", decision)
	}
	if _, known := observedValue(decision, "literal_keyword"); known {
		t.Fatalf("the keyword rule read withheld text: %+v", decision.Rules)
	}
	if value, known := observedValue(decision, "input_text_runes"); !known || value != "22" {
		t.Fatalf("input_text_runes = %q (known=%v), want 22", value, known)
	}
}

// Time windows read the gateway's clock in its own zone, include the start
// minute, exclude the end, and may cross midnight.
func TestModelRoutingTimeWindowsRouteLiveRequests(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	group := factsGroup(factsRule("night", 1, contract.ModelRoutingCondition{Field: "system_time", Within: []string{"22:00", "02:00"}}))
	body := `{"model":"astrlink/facts","messages":[{"role":"user","content":"hello"}]}`
	for _, test := range []struct {
		clock   string
		matched bool
	}{
		{clock: "21:59", matched: false},
		{clock: "22:00", matched: true},
		{clock: "23:30", matched: true},
		{clock: "01:59", matched: true},
		{clock: "02:00", matched: false},
		{clock: "12:00", matched: false},
	} {
		t.Run(test.clock, func(t *testing.T) {
			at, err := time.ParseInLocation("2006-01-02 15:04:05", "2026-10-19 "+test.clock+":30", zone)
			if err != nil {
				t.Fatal(err)
			}
			decision, code := routeLive(t, group, "/v1/chat/completions", body, func(handler *Handler) {
				handler.clock = func() time.Time { return at }
			})
			if code != http.StatusOK || (decision.RuleID == "night") != test.matched {
				t.Fatalf("status=%d rule=%q at %s, want matched=%v", code, decision.RuleID, at, test.matched)
			}
			if value, _ := observedValue(decision, "system_time"); value != test.clock {
				t.Fatalf("system_time = %q, want %s", value, test.clock)
			}
		})
	}
}

// Media rules see images, audio and video however each format carries them,
// including in history and inside tool results; documents are not media.
func TestModelRoutingMediaRulesSeeEveryFormat(t *testing.T) {
	group := factsGroup(
		factsRule("mixed", 1, contract.ModelRoutingCondition{Field: "media_kind", In: []string{"mixed"}}),
		factsRule("image", 2, contract.ModelRoutingCondition{Field: "has_image", EqualsBool: boolRef(true)}),
		factsRule("audio", 3, contract.ModelRoutingCondition{Field: "has_audio", EqualsBool: boolRef(true)}),
		factsRule("video", 4, contract.ModelRoutingCondition{Field: "has_video", EqualsBool: boolRef(true)}),
	)
	for _, test := range []struct{ name, path, body, rule string }{
		{name: "chat image in history", path: "/v1/chat/completions", rule: "image",
			body: `{"model":"astrlink/facts","messages":[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},{"role":"assistant","content":"a cat"},{"role":"user","content":"and now?"}]}`},
		{name: "chat audio", path: "/v1/chat/completions", rule: "audio",
			body: `{"model":"astrlink/facts","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"AAAA","format":"wav"}}]}]}`},
		{name: "chat video", path: "/v1/chat/completions", rule: "video",
			body: `{"model":"astrlink/facts","messages":[{"role":"user","content":[{"type":"video_url","video_url":{"url":"https://example.test/a.mp4"}}]}]}`},
		{name: "chat image and audio", path: "/v1/chat/completions", rule: "mixed",
			body: `{"model":"astrlink/facts","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/a.png"}},{"type":"input_audio","input_audio":{"data":"AAAA","format":"wav"}}]}]}`},
		{name: "chat pdf file", path: "/v1/chat/completions", rule: "",
			body: `{"model":"astrlink/facts","messages":[{"role":"user","content":[{"type":"text","text":"summarize"},{"type":"file","file":{"filename":"a.pdf","file_data":"data:application/pdf;base64,JVBERi0="}}]}]}`},
		{name: "responses image url", path: "/v1/responses", rule: "image",
			body: `{"model":"astrlink/facts","input":[{"role":"user","content":[{"type":"input_text","text":"look"},{"type":"input_image","image_url":"https://example.test/a.png"}]}]}`},
		{name: "responses image file id", path: "/v1/responses", rule: "image",
			body: `{"model":"astrlink/facts","input":[{"role":"user","content":[{"type":"input_image","file_id":"file_1"}]}]}`},
		{name: "responses input file", path: "/v1/responses", rule: "",
			body: `{"model":"astrlink/facts","input":[{"role":"user","content":[{"type":"input_text","text":"read"},{"type":"input_file","file_id":"file_2"}]}]}`},
		{name: "anthropic base64 image", path: "/v1/messages", rule: "image",
			body: `{"model":"astrlink/facts","max_tokens":64,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}},{"type":"text","text":"what"}]}]}`},
		{name: "anthropic url image", path: "/v1/messages", rule: "image",
			body: `{"model":"astrlink/facts","max_tokens":64,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.test/a.png"}}]}]}`},
		{name: "anthropic image in tool result", path: "/v1/messages", rule: "image",
			body: `{"model":"astrlink/facts","max_tokens":64,"messages":[{"role":"user","content":"take a screenshot"},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"screenshot","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}]}`},
		{name: "anthropic document", path: "/v1/messages", rule: "",
			body: `{"model":"astrlink/facts","max_tokens":64,"messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0="}},{"type":"text","text":"summarize"}]}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision, code := routeLive(t, group, test.path, test.body)
			if code != http.StatusOK || decision.RuleID != test.rule || decision.UsedDefault != (test.rule == "") {
				t.Fatalf("status=%d rule=%q default=%v, want rule %q", code, decision.RuleID, decision.UsedDefault, test.rule)
			}
		})
	}
}

// One conversation counts the same in every format: two user turns, two
// assistant turns, one tool call with its result, and one declared tool.
func TestModelRoutingCountsAgreeAcrossFormats(t *testing.T) {
	for _, test := range []struct{ name, path, body string }{
		{name: "chat", path: "/v1/chat/completions",
			body: `{"model":"astrlink/facts","tools":[{"type":"function","function":{"name":"read"}}],"messages":[{"role":"user","content":"read the file"},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"ok"},{"role":"assistant","content":"done"},{"role":"user","content":"thanks"}]}`},
		{name: "responses", path: "/v1/responses",
			body: `{"model":"astrlink/facts","tools":[{"type":"function","name":"read"}],"input":[{"role":"user","content":"read the file"},{"type":"reasoning","summary":[]},{"type":"function_call","call_id":"c1","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"ok"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]},{"role":"user","content":"thanks"}]}`},
		{name: "anthropic", path: "/v1/messages",
			body: `{"model":"astrlink/facts","max_tokens":64,"tools":[{"name":"read","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"read the file"},{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"ok"}]},{"role":"assistant","content":"done"},{"role":"user","content":"thanks"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			classified, err := classify(httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body)), 0)
			if err != nil {
				t.Fatal(err)
			}
			facts := modelRoutingFacts(classified, nil, time.Now())
			got := map[string]int{
				"user":       *facts.UserMessageCount,
				"assistant":  *facts.AssistantMessageCount,
				"tool calls": *facts.ToolCallCount,
				"tool msgs":  *facts.ToolMessageCount,
				"tools":      *facts.ToolDeclarationCount,
			}
			want := map[string]int{"user": 2, "assistant": 2, "tool calls": 1, "tool msgs": 1, "tools": 1}
			for key, value := range want {
				if got[key] != value {
					t.Fatalf("%s = %d, want %d (all: %v)", key, got[key], value, got)
				}
			}
		})
	}
}
