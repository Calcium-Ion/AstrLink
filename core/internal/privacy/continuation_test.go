package privacy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

type continuationFixture struct {
	name      string
	protocol  contract.ProtocolID
	body      string
	protected []string
}

func continuationFixtures() []continuationFixture {
	return []continuationFixture{
		{
			"responses", contract.ProtocolOpenAIResponses,
			`{"input":[{"type":"reasoning","encrypted_content":"HOLD","summary":[{"type":"summary_text","text":"SCAN"}]},{"type":"compaction","encrypted_content":"HOLD"},{"type":"compaction_summary","encrypted_content":"HOLD"},{"type":"function_call_output","output":[{"type":"encrypted_content","encrypted_content":"HOLD"},{"type":"text","text":"SCAN"}]},{"type":"custom_tool_call_output","output":[{"type":"encrypted_content","encrypted_content":"HOLD"}]},{"role":"user","content":"SCAN"}]}`,
			[]string{"/input/0/encrypted_content", "/input/1/encrypted_content", "/input/2/encrypted_content", "/input/3/output/0/encrypted_content", "/input/4/output/0/encrypted_content"},
		},
		{
			"anthropic", contract.ProtocolAnthropicMessages,
			`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"HOLD","signature":"HOLD"},{"type":"redacted_thinking","data":"HOLD"},{"type":"text","text":"SCAN"},{"type":"tool_use","name":"lookup","input":{"signature":"SCAN","data":"SCAN"}}]},{"role":"user","content":[{"type":"tool_result","content":"SCAN"}]}]}`,
			[]string{"/messages/0/content/0/signature", "/messages/0/content/0/thinking", "/messages/0/content/1/data"},
		},
		{
			"gemini", contract.ProtocolGoogleGenerateContent,
			`{"contents":[{"role":"model","parts":[{"text":"SCAN","thoughtSignature":"HOLD"},{"thought_signature":"HOLD","functionCall":{"name":"lookup","args":{"thoughtSignature":"SCAN"}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"thought_signature":"SCAN"}}},{"text":"SCAN"}]}]}`,
			[]string{"/contents/0/parts/0/thoughtSignature", "/contents/0/parts/1/thought_signature"},
		},
		{
			"chat-gemini", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"assistant","content":"SCAN","tool_calls":[{"type":"function","id":"call_1","function":{"name":"lookup","arguments":{"thought_signature":"SCAN"},"provider_specific_fields":{"thought_signature":"HOLD","other":"SCAN"}},"extra_content":{"google":{"thought_signature":"HOLD","other":"SCAN"}},"provider_specific_fields":{"thought_signature":"HOLD","other":"SCAN"}}]},{"role":"tool","tool_call_id":"call_1","content":"SCAN"}]}`,
			[]string{"/messages/0/tool_calls/0/function/provider_specific_fields/thought_signature", "/messages/0/tool_calls/0/extra_content/google/thought_signature", "/messages/0/tool_calls/0/provider_specific_fields/thought_signature"},
		},
		{
			"chat-anthropic", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"assistant","thinking_blocks":[{"type":"thinking","thinking":"HOLD","signature":"HOLD"},{"type":"redacted_thinking","data":"HOLD"}],"content":[{"type":"thinking","thinking":"HOLD","signature":"HOLD"},{"type":"text","text":"SCAN"}]}]}`,
			[]string{"/messages/0/thinking_blocks/0/thinking", "/messages/0/thinking_blocks/0/signature", "/messages/0/thinking_blocks/1/data", "/messages/0/content/0/thinking", "/messages/0/content/0/signature"},
		},
		{
			"chat-openrouter", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"assistant","reasoning_details":[{"type":"reasoning.encrypted","data":"HOLD"},{"type":"reasoning.text","text":"HOLD","signature":"HOLD"},{"type":"reasoning.text","text":"SCAN"},{"type":"reasoning.summary","summary":"SCAN"}],"reasoning_content":"SCAN","content":"SCAN"}]}`,
			[]string{"/messages/0/reasoning_details/0/data", "/messages/0/reasoning_details/1/text", "/messages/0/reasoning_details/1/signature"},
		},
	}
}

func TestContinuationPathsAreTypedAndProtocolScoped(t *testing.T) {
	for _, fixture := range continuationFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal([]byte(fixture.body), &root); err != nil {
				t.Fatal(err)
			}
			paths := continuationPaths(fixture.protocol, root)
			if len(paths) != len(fixture.protected) {
				t.Fatalf("protected=%v want=%v", paths, fixture.protected)
			}
			for _, path := range fixture.protected {
				if _, ok := paths[path]; !ok {
					t.Errorf("missing protection: %s", path)
				}
			}
		})
	}
}

// Same bytes appear both in replay metadata and ordinary text. No detector,
// confidence level, or global value allowlist may conflate those occurrences.
func TestContinuationExtractionAndRewritePreserveMetadata(t *testing.T) {
	const cipher = "sk-proj-abcdefghijklmnopqrstuvwxyz123456"
	for _, fixture := range continuationFixtures() {
		for _, mode := range []Mode{ModeRegex, ModeLocalModel} {
			t.Run(fixture.name+"/"+string(mode), func(t *testing.T) {
				protected := make(map[string]bool)
				for _, path := range fixture.protected {
					protected[path] = true
				}
				body := strings.NewReplacer("HOLD", cipher+`\u003d`, "SCAN", cipher).Replace(fixture.body)
				_, segments, err := extractDocument(fixture.protocol, []byte(body))
				if err != nil {
					t.Fatal(err)
				}
				for _, segment := range segments {
					if protected[segment.Path] {
						t.Fatalf("metadata reached extraction: %s", segment.Path)
					}
				}
				model := DetectorFunc(func(ctx context.Context, input DetectInput) ([]Finding, error) {
					for _, segment := range input.Segments {
						if protected[segment.Path] {
							t.Fatalf("metadata reached model: %s", segment.Path)
						}
					}
					return NewRegexDetector().Detect(ctx, input)
				})
				policy := Policy{Enabled: true, Mode: mode, LocalModelID: testLocalModelID, Action: ActionRedact}
				result, err := newTestEngine(t, model).Inspect(t.Context(), policy, fixture.protocol, []byte(body))
				if err != nil || result.Decision != DecisionRedact || len(result.Redactions) != 1 {
					t.Fatalf("decision=%s redactions=%d err=%v", result.Decision, len(result.Redactions), err)
				}
				want := strings.NewReplacer("HOLD", cipher+`\u003d`, "SCAN", result.Redactions[0].Placeholder).Replace(fixture.body)
				if string(result.Body) != want {
					t.Fatalf("metadata changed or ordinary content unfiltered:\ngot=%s\nwant=%s", result.Body, want)
				}
			})
		}
	}
}

func TestContinuationMetadataDoesNotTriggerPrivacyActions(t *testing.T) {
	for _, fixture := range continuationFixtures() {
		for _, action := range []Action{ActionRedact, ActionWarn, ActionBlock} {
			t.Run(fixture.name+"/"+string(action), func(t *testing.T) {
				body := strings.NewReplacer("HOLD", "sk-proj-abcdefghijklmnopqrstuvwxyz123456", "SCAN", "ordinary text").Replace(fixture.body)
				result, err := newTestEngine(t, nil).Inspect(t.Context(), Policy{
					Enabled: true, Mode: ModeRegex, Action: action,
				}, fixture.protocol, []byte(body))
				if err != nil || result.Decision != DecisionAllow || len(result.Findings) != 0 || result.Body != nil {
					t.Fatalf("metadata triggered privacy: decision=%s findings=%d err=%v", result.Decision, len(result.Findings), err)
				}
			})
		}
	}
}

func TestContinuationNamesCannotBypassOrdinaryContent(t *testing.T) {
	for _, fixture := range []struct {
		protocol contract.ProtocolID
		body     string
	}{
		{contract.ProtocolOpenAIResponses, `{"input":{"0":{"type":"reasoning","encrypted_content":"%s"}}}`},
		{contract.ProtocolOpenAIResponses, `{"input":[{"role":"user","type":"reasoning","encrypted_content":"%s"}]}`},
		{contract.ProtocolOpenAIResponses, `{"input":[{"type":"message","output":[{"type":"encrypted_content","encrypted_content":"%s"}]}]}`},
		{contract.ProtocolOpenAIResponses, `{"input":[{"type":"function_call_output","output":{"0":{"type":"encrypted_content","encrypted_content":"%s"}}}]}`},
		{contract.ProtocolOpenAIResponses, `{"input":[{"role":"user","content":"{\"input\":[{\"type\":\"reasoning\",\"encrypted_content\":\"%s\"}]}"}]}`},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":[{"type":"thinking","signature":"%s"}]}]}`},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"%s"}]}]}`},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"assistant","content":[{"type":"tool_use","input":{"type":"thinking","signature":"%s"}}]}]}`},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"redacted_thinking","data":"%s"}]}]}]}`},
		{contract.ProtocolGoogleGenerateContent, `{"contents":[{"role":"user","parts":[{"thoughtSignature":"%s"}]}]}`},
		{contract.ProtocolGoogleGenerateContent, `{"contents":[{"role":"model","parts":{"0":{"thought_signature":"%s"}}}]}`},
		{contract.ProtocolGoogleGenerateContent, `{"contents":[{"role":"model","parts":[{"functionCall":{"args":{"thoughtSignature":"%s"}}}]}]}`},
		{contract.ProtocolOpenAIChat, `{"messages":[{"role":"user","reasoning_details":[{"type":"reasoning.encrypted","data":"%s"}]}]}`},
		{contract.ProtocolOpenAIChat, `{"messages":[{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":{"extra_content":{"google":{"thought_signature":"%s"}}}}}]}]}`},
		{contract.ProtocolOpenAIChat, `{"messages":[{"role":"user","content":"{\"role\":\"assistant\",\"thinking_blocks\":[{\"type\":\"thinking\",\"signature\":\"%s\"}]}"}]}`},
	} {
		t.Run(fmt.Sprintf("%s/%s", fixture.protocol, fixture.body), func(t *testing.T) {
			const secret = "sk-proj-abcdefghijklmnopqrstuvwxyz123456"
			body := fmt.Sprintf(fixture.body, secret)
			var root map[string]any
			if err := json.Unmarshal([]byte(body), &root); err != nil {
				t.Fatal(err)
			}
			if paths := continuationPaths(fixture.protocol, root); len(paths) != 0 {
				t.Fatalf("ordinary content was exempted: %v", paths)
			}
			result, err := newTestEngine(t, nil).Inspect(t.Context(), Policy{
				Enabled: true, Mode: ModeRegex, Action: ActionRedact,
			}, fixture.protocol, []byte(body))
			if err != nil || result.Decision != DecisionRedact || strings.Contains(string(result.Body), secret) {
				t.Fatalf("ordinary content escaped redaction: decision=%s err=%v", result.Decision, err)
			}
		})
	}
}
