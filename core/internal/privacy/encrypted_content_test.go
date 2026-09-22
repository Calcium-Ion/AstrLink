package privacy

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestEnginePreservesResponsesEncryptedContent(t *testing.T) {
	const template = ` {
 "model":"gpt-5", "store":false, "include":["reasoning.encrypted_content"],
 "input":[
  {"type":"reasoning","id":"rs_1","encrypted_content" : "OPAQUE","summary":[{"type":"summary_text","text":"alice@example.com"}]},
  {"type":"compaction","id":"cmp_1","encrypted_content":"OPAQUE"},
  {"type":"compaction_summary","encrypted_content":"OPAQUE"},
  {"type":"function_call_output","call_id":"call_1","output":[{"type":"encrypted_content","encrypted_content":"OPAQUE"},{"type":"text","text":"SENSITIVE"}]},
  {"type":"custom_tool_call_output","call_id":"call_2","output":[{"type":"encrypted_content","encrypted_content":"OPAQUE"}]},
  {"type":"message","role":"user","content":"SENSITIVE"},
  {"type":"function_call","call_id":"call_3","name":"lookup","arguments":{"type":"reasoning","encrypted_content":"SENSITIVE"}},
  {"type":"function_call_output","call_id":"call_3","output":{"type":"reasoning","encrypted_content":"SENSITIVE"}}
 ]
} `
	for _, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact} {
		for _, mode := range []string{"builtin", "custom", "model"} {
			t.Run(string(protocol)+"/"+mode, func(t *testing.T) {
				cipher := "gAAAAA" + strings.Repeat("Ab09_-", 335) + "=="
				policy := Policy{Enabled: true, Mode: ModeRegex, Action: ActionRedact}
				rules := []contract.PolicyRegexRule{
					{Kind: string(KindCommonSecret), Pattern: `gAAAAA[A-Za-z0-9_=-]+`},
					{Kind: string(KindEmail), Pattern: `alice@example\.com`},
				}
				var model Detector
				switch mode {
				case "builtin":
					cipher = "sk-proj-abcdefghijklmnopqrstuvwxyz123456"
				case "custom":
					policy.RegexSource = contract.PolicyRegexSourceCustom
					policy.CustomRegexRules = rules
				case "model":
					policy.Mode = ModeLocalModel
					policy.LocalModelID = testLocalModelID
					detector, err := NewCustomRegexDetector(rules)
					if err != nil {
						t.Fatal(err)
					}
					model = DetectorFunc(func(ctx context.Context, input DetectInput) ([]Finding, error) {
						for _, segment := range input.Segments {
							switch segment.Path {
							case "/input/0/encrypted_content", "/input/1/encrypted_content", "/input/2/encrypted_content",
								"/input/3/output/0/encrypted_content", "/input/4/output/0/encrypted_content":
								t.Fatalf("protocol ciphertext reached model detector: %s", segment.Path)
							}
						}
						return detector.Detect(ctx, input)
					})
				}
				// Escapes in opaque strings must survive even when adjacent text changes.
				opaque := strings.ReplaceAll(cipher, "=", `\u003d`)
				body := strings.NewReplacer("OPAQUE", opaque, "SENSITIVE", cipher).Replace(template)
				original := []byte(body)
				result, err := newTestEngine(t, model).Inspect(t.Context(), policy, protocol, original)
				if err != nil || result.Decision != DecisionRedact || len(result.Redactions) != 2 {
					t.Fatalf("decision=%s redactions=%d err=%v", result.Decision, len(result.Redactions), err)
				}
				want := strings.ReplaceAll(template, "OPAQUE", opaque)
				for _, redaction := range result.Redactions {
					switch redaction.Value {
					case cipher:
						want = strings.ReplaceAll(want, "SENSITIVE", redaction.Placeholder)
					case "alice@example.com":
						want = strings.ReplaceAll(want, redaction.Value, redaction.Placeholder)
					default:
						t.Fatalf("unexpected redaction kind: %s", redaction.Kind)
					}
				}
				if string(result.Body) != want {
					t.Fatal("ciphertext bytes changed or conversation/tool text escaped redaction")
				}
				if string(original) != body {
					t.Fatal("inspection mutated the original request")
				}
			})
		}
	}
}

func TestEncryptedResponsesHistoryDoesNotTriggerPrivacyActions(t *testing.T) {
	for _, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact} {
		for _, action := range []Action{ActionRedact, ActionBlock, ActionWarn} {
			t.Run(string(protocol)+"/"+string(action), func(t *testing.T) {
				policy := Policy{
					Enabled: true, Mode: ModeRegex, Action: action,
					RegexSource:      contract.PolicyRegexSourceCustom,
					CustomRegexRules: []contract.PolicyRegexRule{{Kind: string(KindCommonSecret), Pattern: `gAAAAA[A-Za-z0-9_=-]+`}},
				}
				body := []byte(`{"store":false,"input":[{"type":"reasoning","encrypted_content":"gAAAAAcipher==","summary":[]},{"type":"compaction","encrypted_content":"gAAAAAhistory=="}]}`)
				result, err := newTestEngine(t, nil).Inspect(t.Context(), policy, protocol, body)
				if err != nil || result.Decision != DecisionAllow || result.Body != nil || len(result.Findings) != 0 {
					t.Fatalf("encrypted history triggered privacy: decision=%s findings=%d err=%v", result.Decision, len(result.Findings), err)
				}
			})
		}
	}
}

func TestEncryptedContentExemptionIsLimitedToProtocolFields(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		fields   string
	}{
		{"message field", contract.ProtocolOpenAIResponses, `"input":[{"type":"message","role":"user","encrypted_content":"%s"}]`},
		{"nested content", contract.ProtocolOpenAIResponses, `"input":[{"role":"user","content":[{"type":"reasoning","encrypted_content":"%s"}]}]`},
		{"nested tool output", contract.ProtocolOpenAIResponses, `"input":[{"type":"function_call_output","output":{"data":{"type":"encrypted_content","encrypted_content":"%s"}}}]`},
		{"object tool output", contract.ProtocolOpenAIResponses, `"input":[{"type":"function_call_output","output":{"type":"encrypted_content","encrypted_content":"%s"}}]`},
		{"string tool output", contract.ProtocolOpenAIResponses, `"input":[{"type":"function_call_output","output":"{\"type\":\"reasoning\",\"encrypted_content\":\"%s\"}"}]`},
		{"chat", contract.ProtocolOpenAIChat, `"messages":[{"role":"user","content":[{"type":"reasoning","encrypted_content":"%s"}]}]`},
		{"anthropic", contract.ProtocolAnthropicMessages, `"messages":[{"role":"user","content":[{"type":"reasoning","encrypted_content":"%s"}]}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			const secret = "sk-proj-abcdefghijklmnopqrstuvwxyz123456"
			body := "{" + fmt.Sprintf(test.fields, secret) + "}"
			result, err := newTestEngine(t, nil).Inspect(t.Context(), Policy{
				Enabled: true, Mode: ModeRegex, Action: ActionRedact,
			}, test.protocol, []byte(body))
			if err != nil || result.Decision != DecisionRedact || strings.Contains(string(result.Body), secret) {
				t.Fatalf("ordinary text bypassed privacy: decision=%s err=%v", result.Decision, err)
			}
		})
	}
}
