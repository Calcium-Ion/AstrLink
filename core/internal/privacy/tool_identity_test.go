package privacy

import (
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/tidwall/gjson"
)

func TestResponsesToolIdentitiesDoNotTriggerPrivacyActions(t *testing.T) {
	fixtures := map[string]string{
		"function call": `{"input":[{"type":"function_call","namespace":"mcp__cua_repl","name":"js","call_id":"call_fixture","arguments":"{\"code\":\"cua.getState()\"}"}]}`,
		"custom call":   `{"input":[{"type":"custom_tool_call","namespace":"mcp__cua_repl","name":"js","call_id":"call_fixture","input":"cua.getState()"}]}`,
		"declaration":   `{"input":"hello","tools":[{"type":"namespace","name":"mcp__cua_repl","description":"Browser tools","tools":[{"type":"function","name":"js","parameters":{}}]}]}`,
		"additional tools": `{"input":[{"type":"additional_tools","role":"developer","tools":[
			{"type":"namespace","name":"mcp__cua_repl","tools":[{"type":"function","name":"js"}]}]}]}`,
		"nested declaration": `{"input":"hello","tools":[{"type":"namespace","name":"outer","tools":[
			{"type":"namespace","name":"mcp__cua_repl","tools":[{"type":"function","name":"js"}]}]}]}`,
	}
	for _, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact} {
		for name, body := range fixtures {
			for _, action := range []Action{ActionRedact, ActionWarn, ActionBlock} {
				t.Run(string(protocol)+"/"+name+"/"+string(action), func(t *testing.T) {
					var seen []Segment
					policy := Policy{Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID,
						Action: action, InspectToolDeclarations: true}
					identities := []string{"mcp__cua_repl", "js", "call_fixture", "outer"}
					result, err := newTestEngine(t, flaggingDetector(identities, &seen)).Inspect(t.Context(), policy, protocol, []byte(body))
					if err != nil || result.Decision != DecisionAllow || len(result.Findings) != 0 || result.Body != nil {
						t.Fatalf("tool identity triggered privacy: decision=%s findings=%v err=%v", result.Decision, result.Findings, err)
					}
				})
			}
		}
	}
}

func TestResponsesToolNamespaceProtectionKeepsPayloadsInspectable(t *testing.T) {
	const namespace = "mcp__cua_repl"
	const body = `{"input":[
		{"type":"function_call","namespace":"mcp__cua_repl","name":"js","call_id":"call_fixture","arguments":"{\"namespace\":\"mcp__cua_repl\"}"},
		{"type":"function_call_output","call_id":"call_fixture","output":{"type":"function_call","namespace":"mcp__cua_repl","name":"mcp__cua_repl"}},
		{"type":"custom_tool_call","namespace":"mcp__cua_repl","name":"js","call_id":"call_custom","input":"mcp__cua_repl"},
		{"role":"user","content":"mcp__cua_repl"}
	],"tools":[{"type":"namespace","name":"mcp__cua_repl","description":"mcp__cua_repl","tools":[
		{"type":"function","name":"js","parameters":{"type":"object","properties":{"namespace":{"type":"string","description":"mcp__cua_repl"}}}}
	]}]}`
	for _, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact} {
		t.Run(string(protocol), func(t *testing.T) {
			var seen []Segment
			policy := Policy{Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID,
				Action: ActionRedact, InspectToolDeclarations: true}
			result, err := newTestEngine(t, flaggingDetector([]string{namespace}, &seen)).Inspect(t.Context(), policy, protocol, []byte(body))
			if err != nil || result.Decision != DecisionRedact || len(result.Findings) != 7 {
				t.Fatalf("payload inspection: decision=%s findings=%v err=%v", result.Decision, result.Findings, err)
			}
			for _, path := range []string{"input.0.namespace", "input.2.namespace", "tools.0.name"} {
				if got := gjson.GetBytes(result.Body, path).Str; got != namespace {
					t.Errorf("%s = %q, want %q", path, got, namespace)
				}
			}
			if strings.Count(string(result.Body), namespace) != 3 {
				t.Fatalf("sensitive content escaped redaction: %s", result.Body)
			}
		})
	}
}

func TestToolIdentityProtectionRequiresProtocolPosition(t *testing.T) {
	fixtures := map[string]string{
		"message namespace": `{"input":[{"role":"user","content":[{"type":"input_text","namespace":"mcp__cua_repl"}]}]}`,
		"user call":         `{"input":[{"role":"user","type":"function_call","namespace":"mcp__cua_repl","arguments":"{}"}]}`,
		"nested call":       `{"input":[{"role":"user","content":{"type":"function_call","namespace":"mcp__cua_repl","arguments":"{}"}}]}`,
		"prompt variable":   `{"prompt":{"variables":{"namespace":"mcp__cua_repl"}}}`,
		"schema property":   `{"tools":[{"type":"function","name":"js","parameters":{"type":"object","properties":{"namespace":{"type":"string","description":"mcp__cua_repl"}}}}]}`,
		"user declaration":  `{"input":[{"type":"additional_tools","role":"user","tools":[{"type":"namespace","name":"mcp__cua_repl","tools":[]}]}]}`,
		"output declaration": `{"input":[{"type":"function_call_output","call_id":"call_fixture","output":{
			"type":"namespace","name":"mcp__cua_repl","tools":[]}}]}`,
	}
	for name, body := range fixtures {
		t.Run(name, func(t *testing.T) {
			var seen []Segment
			policy := Policy{Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID,
				Action: ActionRedact, InspectToolDeclarations: true}
			result, err := newTestEngine(t, flaggingDetector([]string{"mcp__cua_repl"}, &seen)).Inspect(t.Context(), policy, contract.ProtocolOpenAIResponses, []byte(body))
			if err != nil || result.Decision != DecisionRedact || strings.Contains(string(result.Body), "mcp__cua_repl") {
				t.Fatalf("payload escaped inspection: decision=%s body=%s err=%v", result.Decision, result.Body, err)
			}
		})
	}
}
