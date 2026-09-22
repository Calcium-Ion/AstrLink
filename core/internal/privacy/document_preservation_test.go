package privacy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Exact byte comparison also checks field order at every depth, whitespace,
// unknown fields, number spelling, untouched string escapes and array order.
func TestRedactionPreservesUnmodifiedJSONBytes(t *testing.T) {
	for _, test := range []struct {
		protocol contract.ProtocolID
		fields   string
	}{
		{contract.ProtocolOpenAIChat, `"messages" : [
  { "z":null, "content":"alice@example.com", "role":"system", "a":false },
  { "content":[ {"z":7, "text":"bob@example.com", "type":"text", "a":"\u4f60\u597d"} ], "role":"user" },
  { "content":null, "role":"system" }
 ]`},
		{contract.ProtocolOpenAIResponses, `"instructions":"alice@example.com", "input":[
 { "z":null, "content":[{"text":"bob@example.com", "type":"input_text"}], "role":"user" }
 ]`},
		{contract.ProtocolOpenAIResponsesCompact, `"input":"alice@example.com", "instructions":"bob@example.com"`},
		{contract.ProtocolOpenAICompletions, `"suffix":"alice@example.com", "prompt":["keep", "bob@example.com", null]`},
		{contract.ProtocolAnthropicMessages, `"system":[{"text":"alice@example.com", "type":"text"}], "messages":[
 { "content":[{"input":{"z":true,"email":"bob@example.com","a":null},"type":"tool_use"}], "role":"assistant" }
 ]`},
		{contract.ProtocolGoogleGenerateContent, `"systemInstruction":{"parts":[{"text":"alice@example.com"}],"role":"system"}, "contents":[
 { "parts":[{"functionCall":{"args":{"z":null,"email":"bob@example.com","a":false},"name":"lookup"}}], "role":"model" }
 ]`},
	} {
		for _, style := range []string{"token", "natural"} {
			t.Run(string(test.protocol)+"/"+style, func(t *testing.T) {
				body := " \r\n{\n" + ` "z_extension" : {"z":9007199254740993,"n":1.2300e+04,"a":"\u003ckeep\u003e\/path"},` +
					"\n " + test.fields + ",\n " + `"model":"untouched@example.net", "a_extension":null, "tools":[{"z":"docs@example.net","a":{}}]` + "\n}\t\r\n"
				original := []byte(body)
				policy := tokenPolicy()
				if style == "natural" {
					policy = naturalPolicy()
				}
				result, err := mustTestEngine(t).Inspect(t.Context(), policy, test.protocol, original)
				if err != nil || result.Decision != DecisionRedact || len(result.Redactions) != 2 {
					t.Fatalf("inspect: decision=%s redactions=%d err=%v", result.Decision, len(result.Redactions), err)
				}
				if string(original) != body {
					t.Fatal("inspection mutated the source bytes used for retries")
				}
				assertOnlyRedactionsChanged(t, body, result)
			})
		}
	}
}

func TestRedactionTreatsToolPayloadKeysLiterally(t *testing.T) {
	for _, key := range []string{"", "0", "-1", "a.b", `a\b`, ":name", "*", "a?b", "#", "@this", "a|b", "[x]", "{x}", "!true", "#(x)", "a/b~c", "中文", `quote"key`} {
		t.Run(key, func(t *testing.T) {
			encodedKey, err := json.Marshal(key)
			if err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(` { "z":1, "messages":[{"content":[{"input":{
 "z_keep":"\u0061", %s:{"0":[{"email":"alice@example.com","a":false}]}, "a_keep":null
 },"type":"tool_use"}],"role":"assistant"}], "a":2 } `, encodedKey)
			result, err := mustTestEngine(t).Inspect(t.Context(), tokenPolicy(), contract.ProtocolOpenAIChat, []byte(body))
			if err != nil || result.Decision != DecisionRedact || len(result.Redactions) != 1 {
				t.Fatalf("inspect: decision=%s redactions=%d err=%v", result.Decision, len(result.Redactions), err)
			}
			assertOnlyRedactionsChanged(t, body, result)
		})
	}
}

func TestNoticePreservesExistingJSONBytes(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		before   string
		after    string
		injected bool
	}{
		{
			"chat string", contract.ProtocolOpenAIChat,
			`"messages" : [{"z":null,"content":"be brief","role":"developer","a":false},{"content":"alice@example.com","role":"user"}]`,
			`"messages" : [{"z":null,"content":"__NOTICE__\n\nbe brief","role":"developer","a":false},{"content":"alice@example.com","role":"user"}]`, true,
		},
		{
			"chat blocks", contract.ProtocolOpenAIChat,
			`"messages":[{"content":[ {"z":1e+02,"text":"\u0061","type":"text","a":null} ],"role":"system"},{"role":"user","content":"alice@example.com"}]`,
			`"messages":[{"content":[{"text":"__NOTICE__","type":"text"}, {"z":1e+02,"text":"\u0061","type":"text","a":null} ],"role":"system"},{"role":"user","content":"alice@example.com"}]`, true,
		},
		{
			"chat new system", contract.ProtocolOpenAIChat,
			`"messages" : [ {"z":1,"content":"alice@example.com","role":"user","a":null} ]`,
			`"messages" : [{"content":"__NOTICE__","role":"system"}, {"z":1,"content":"alice@example.com","role":"user","a":null} ]`, true,
		},
		{
			"chat null system", contract.ProtocolOpenAIChat,
			`"messages" : [ {"content":null,"role":"system"}, {"role":"user","content":"alice@example.com"} ]`,
			`"messages" : [{"content":"__NOTICE__","role":"system"}, {"content":null,"role":"system"}, {"role":"user","content":"alice@example.com"} ]`, true,
		},
		{
			"responses string", contract.ProtocolOpenAIResponses,
			`"instructions" : "alice@example.com", "input":"hello"`,
			`"instructions" : "__NOTICE__\n\nalice@example.com", "input":"hello"`, true,
		},
		{
			"responses null", contract.ProtocolOpenAIResponses,
			`"instructions" : null, "input":"alice@example.com"`,
			`"instructions" : "__NOTICE__", "input":"alice@example.com"`, true,
		},
		{
			"compact string", contract.ProtocolOpenAIResponsesCompact,
			`"input":"alice@example.com", "instructions":"be brief"`,
			`"input":"alice@example.com", "instructions":"__NOTICE__\n\nbe brief"`, true,
		},
		{
			"anthropic string", contract.ProtocolAnthropicMessages,
			`"system":"be brief", "messages":[{"content":"alice@example.com","role":"user"}]`,
			`"system":"__NOTICE__\n\nbe brief", "messages":[{"content":"alice@example.com","role":"user"}]`, true,
		},
		{
			"anthropic blocks", contract.ProtocolAnthropicMessages,
			`"system" : [ {"cache_control":{"z":null,"type":"ephemeral"},"text":"alice@example.com","type":"text"} ], "messages":[]`,
			`"system" : [{"text":"__NOTICE__","type":"text"}, {"cache_control":{"z":null,"type":"ephemeral"},"text":"alice@example.com","type":"text"} ], "messages":[]`, true,
		},
		{
			"anthropic empty blocks", contract.ProtocolAnthropicMessages,
			`"system" : [  ], "messages":[{"content":"alice@example.com","role":"user"}]`,
			`"system" : [{"text":"__NOTICE__","type":"text"}  ], "messages":[{"content":"alice@example.com","role":"user"}]`, true,
		},
		{
			"gemini blocks", contract.ProtocolGoogleGenerateContent,
			`"systemInstruction":{"z":null,"parts":[ {"z":1.00,"text":"alice@example.com","a":"\u0061"} ],"role":"system"}, "contents":[]`,
			`"systemInstruction":{"z":null,"parts":[{"text":"__NOTICE__"}, {"z":1.00,"text":"alice@example.com","a":"\u0061"} ],"role":"system"}, "contents":[]`, true,
		},
		{
			"gemini missing parts", contract.ProtocolGoogleGenerateContent,
			`"systemInstruction":{"z":null,"role":"system"}, "contents":[{"parts":[{"text":"alice@example.com"}]}]`,
			`"systemInstruction":{"z":null,"role":"system","parts":[{"text":"__NOTICE__"}]}, "contents":[{"parts":[{"text":"alice@example.com"}]}]`, true,
		},
		{
			"completions have no notice", contract.ProtocolOpenAICompletions,
			`"prompt":"alice@example.com", "suffix":"continue"`,
			`"prompt":"alice@example.com", "suffix":"continue"`, false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			wrap := func(fields string) string { return " \n{ \"z\":1.00, " + fields + ", \"a\":null }\r\n" }
			policy := tokenPolicy()
			policy.PlaceholderNotice = true
			body := []byte(wrap(test.before))
			result, err := mustTestEngine(t).Inspect(t.Context(), policy, test.protocol, body)
			if err != nil || result.Decision != DecisionRedact || result.NoticeInjected != test.injected {
				t.Fatalf("inspect: decision=%s notice=%t err=%v", result.Decision, result.NoticeInjected, err)
			}
			if !bytes.Equal(body, []byte(wrap(test.before))) {
				t.Fatal("notice insertion mutated the source bytes used for retries")
			}
			assertOnlyRedactionsChanged(t, strings.ReplaceAll(wrap(test.after), "__NOTICE__", placeholderNotice), result)
		})
	}
}

func TestNoticeAppendsMissingFieldsWithoutReorderingExistingFields(t *testing.T) {
	for _, test := range []struct {
		protocol contract.ProtocolID
		fields   string
		added    string
	}{
		{contract.ProtocolOpenAIResponses, `"input":"alice@example.com"`, `"instructions":"__NOTICE__"`},
		{contract.ProtocolOpenAIResponsesCompact, `"input":"alice@example.com"`, `"instructions":"__NOTICE__"`},
		{contract.ProtocolAnthropicMessages, `"messages":[{"content":"alice@example.com","role":"user"}]`, `"system":"__NOTICE__"`},
		{contract.ProtocolGoogleGenerateContent, `"contents":[{"parts":[{"text":"alice@example.com"}]}]`, `"systemInstruction":{"parts":[{"text":"__NOTICE__"}]}`},
	} {
		t.Run(string(test.protocol), func(t *testing.T) {
			prefix := " \r\n{ \"z\":1.00, " + test.fields + ", \"a\":null\n "
			body := prefix + "}\t\n"
			policy := tokenPolicy()
			policy.PlaceholderNotice = true
			result, err := mustTestEngine(t).Inspect(t.Context(), policy, test.protocol, []byte(body))
			if err != nil || result.Decision != DecisionRedact || !result.NoticeInjected {
				t.Fatalf("inspect: decision=%s notice=%t err=%v", result.Decision, result.NoticeInjected, err)
			}
			want := prefix + "," + strings.ReplaceAll(test.added, "__NOTICE__", placeholderNotice) + "}\t\n"
			assertOnlyRedactionsChanged(t, want, result)
		})
	}
}

func assertOnlyRedactionsChanged(t *testing.T, original string, result Result) {
	t.Helper()
	want := original
	for _, redaction := range result.Redactions {
		want = strings.ReplaceAll(want, redaction.Value, redaction.Placeholder)
	}
	if string(result.Body) != want {
		t.Fatalf("unmodified JSON bytes or field order changed:\n got: %s\nwant: %s", result.Body, want)
	}
	if !json.Valid(result.Body) {
		t.Fatal("redacted body is not valid JSON")
	}
}
