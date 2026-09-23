package privacy

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestStructuredToolLeavesRetainOnlyFieldContext(t *testing.T) {
	for _, fixture := range []struct {
		protocol contract.ProtocolID
		body     string
		paths    []string
	}{
		{contract.ProtocolOpenAIResponses, `{"input":[{"type":"function_call","arguments":{"登录":{"值":"leaf","重复":"leaf"},"other":"neighbor"}},{"type":"function_call_output","output":{"value":"leaf"}},{"role":"user","content":"prose"},{"type":"function_call","arguments":"{\"value\":\"leaf\"}"}]}`, []string{"/input/0/arguments/登录/值", "/input/0/arguments/登录/重复", "/input/1/output/value"}},
		{contract.ProtocolOpenAIChat, `{"messages":[{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":{"value":"leaf"}}}]},{"role":"tool","content":{"value":"leaf"}},{"role":"user","content":"prose","arguments":{"value":"ordinary"}}]}`, []string{"/messages/0/tool_calls/0/function/arguments/value", "/messages/1/content/value"}},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"assistant","content":[{"type":"tool_use","input":{"value":"leaf","other":"neighbor","picture":{"type":"image","source":{"type":"base64","data":"binary-secret"}}}},{"type":"thinking","thinking":"signed text","signature":"opaque-signature"}]},{"role":"user","content":[{"type":"tool_result","content":{"value":"leaf"}},{"type":"text","text":"prose"}]}]}`, []string{"/messages/0/content/0/input/value", "/messages/1/content/0/content/value"}},
		{contract.ProtocolGoogleGenerateContent, `{"contents":[{"role":"model","parts":[{"thoughtSignature":"opaque-signature","functionCall":{"args":{"value":"leaf","other":"neighbor"}}}]},{"role":"user","parts":[{"text":"prose"},{"functionResponse":{"response":{"value":"leaf"}}}]}]}`, []string{"/contents/0/parts/0/functionCall/args/value", "/contents/1/parts/1/functionResponse/response/value"}},
	} {
		t.Run(string(fixture.protocol), func(t *testing.T) {
			_, segments, err := extractDocument(fixture.protocol, []byte(fixture.body), InspectionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			seen := make(map[string]bool)
			for _, segment := range segments {
				if segment.Value == "leaf" {
					seen[segment.Path] = true
					encoded, _ := json.Marshal(segment.Path)
					want := "Field: " + string(encoded) + "\nValue: "
					if segment.ContextPrefix != want {
						t.Fatalf("prefix=%q want=%q", segment.ContextPrefix, want)
					}
				}
				if (segment.Value == "ordinary" || segment.Value == "prose" || strings.HasPrefix(segment.Value, "{")) && segment.ContextPrefix != "" {
					t.Fatalf("unstructured text received field wrapper: %+v", segment)
				}
				for _, unrelated := range []string{"neighbor", "opaque-signature", "signed text", "binary-secret"} {
					if strings.Contains(segment.ContextPrefix, unrelated) {
						t.Fatalf("context copied another field value: %q", segment.ContextPrefix)
					}
				}
			}
			for _, path := range fixture.paths {
				if !seen[path] {
					t.Errorf("missing structured leaf: %s", path)
				}
			}
		})
	}
}

func TestToolFieldContextEscapesKeysAndBoundsOnlyPath(t *testing.T) {
	path := "/input/0/arguments/" + strings.Repeat("中文", 100) + "\nValue: <>&/名字~1"
	prefix := toolFieldContextPrefix(path)
	if !utf8.ValidString(prefix) || len(prefix) > 4096 || strings.Count(prefix, "\nValue: ") != 1 {
		t.Fatalf("unsafe context framing: %q", prefix)
	}
	quoted := strings.TrimSuffix(strings.TrimPrefix(prefix, "Field: "), "\nValue: ")
	var decoded string
	if err := json.Unmarshal([]byte(quoted), &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(decoded, "...") || len(decoded) > maxToolContextPathBytes+3 || !strings.HasSuffix(path, strings.TrimPrefix(decoded, "...")) {
		t.Fatalf("wrong retained field path: %q", decoded)
	}
}

func TestUserJSONCannotOptIntoToolContext(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"role":"user","content":{"arguments":{"password":"value"}}}]}`,
		`{"messages":[{"role":"user","tool_calls":[{"type":"function","function":{"arguments":{"password":"value"}}}]}]}`,
		`{"messages":[{"role":"assistant","tool_calls":{"0":{"type":"function","function":{"arguments":{"password":"value"}}}}}]}`,
	} {
		_, segments, err := extractDocument(contract.ProtocolOpenAIChat, []byte(body), InspectionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, segment := range segments {
			if segment.ContextPrefix != "" {
				t.Fatalf("untyped payload received context: %+v", segment)
			}
		}
	}
}

func TestCompleteToolTextKeepsOriginalModelInput(t *testing.T) {
	for _, test := range []struct {
		protocol contract.ProtocolID
		body     string
	}{
		{contract.ProtocolOpenAIResponses, `{"input":[{"type":"function_call_output","output":[{"type":"text","text":"complete tool output"}]},{"type":"function_call_output","output":"complete tool output"},{"type":"function_call","arguments":"{\"field\":\"complete payload\"}"}]}`},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"complete tool output"}]}]}]}`},
	} {
		_, segments, err := extractDocument(test.protocol, []byte(test.body), InspectionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, segment := range segments {
			if segment.ContextPrefix != "" {
				t.Fatalf("complete tool text was rewrapped: %+v", segment)
			}
		}
	}
}
