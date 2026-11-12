package privacy

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/astrlink/core/contract"
)

// anthropicToolNamePattern is the pattern Anthropic enforces on tool names,
// including tool_reference.tool_name inside a tool result.
var anthropicToolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,256}$`)

// The secret is assembled so the source holds no vendor-shaped literal.
var fixtureToolSearchSecret = "sk-" + "proj-" + "Fict1onalToolSearchKey000000000"

const (
	fixtureCreateIssueTool = "mcp__tracker__create_issue"
	fixtureListIssuesTool  = "mcp__tracker__list_issues"
	fixtureToolSearchUseID = "toolu_01FictionalToolSearch00001"
)

// combinedDetector reports what the regex catalog finds plus every occurrence
// of the flagged values, as a local model that mistakes tool names for
// secrets would, and records the segments it saw.
func combinedDetector(flagged []string, seen *[]Segment) Detector {
	flagging := flaggingDetector(flagged, seen)
	regex := NewRegexDetector()
	return DetectorFunc(func(ctx context.Context, input DetectInput) ([]Finding, error) {
		findings, err := regex.Detect(ctx, input)
		if err != nil {
			return nil, err
		}
		more, err := flagging.Detect(ctx, input)
		return append(findings, more...), err
	})
}

// Claude Code's ToolSearch loads deferred MCP tools by returning tool_reference
// blocks in its tool result. A placeholder in tool_name made Anthropic reject
// the request: tool_reference.tool_name: String should match pattern.
func TestToolSearchReferencesSurviveRedaction(t *testing.T) {
	body := `{"model":"claude-test","tools":[
 {"name":"ToolSearch","description":"Load deferred tools","input_schema":{"type":"object","properties":{"query":{"type":"string"}}}},
 {"name":"` + fixtureCreateIssueTool + `","description":"Create an issue","input_schema":{"type":"object"},"defer_loading":true},
 {"name":"` + fixtureListIssuesTool + `","description":"List issues","input_schema":{"type":"object"},"defer_loading":true}
],"messages":[
 {"role":"user","content":"open an issue"},
 {"role":"assistant","content":"Loading the tracker tools."},
 {"role":"assistant","content":[{"type":"tool_use","id":"` + fixtureToolSearchUseID + `","name":"ToolSearch","input":{"query":"select:` + fixtureCreateIssueTool + `,` + fixtureListIssuesTool + `","max_results":5}}]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"` + fixtureToolSearchUseID + `","content":[
  {"type":"text","text":"Loaded 2 tools. config api_key=` + fixtureToolSearchSecret + `"},
  {"type":"tool_reference","tool_name":"` + fixtureCreateIssueTool + `"},
  {"type":"tool_reference","tool_name":"` + fixtureListIssuesTool + `"}
 ]}]}
]}`
	const query = "select:" + fixtureCreateIssueTool + "," + fixtureListIssuesTool
	for _, inspectDeclarations := range []bool{false, true} {
		name := "declarations skipped"
		if inspectDeclarations {
			name = "declarations inspected"
		}
		t.Run(name, func(t *testing.T) {
			var seen []Segment
			policy := Policy{Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID,
				Action: ActionRedact, InspectToolDeclarations: inspectDeclarations}
			detector := combinedDetector([]string{fixtureCreateIssueTool, fixtureListIssuesTool}, &seen)
			result, err := newTestEngine(t, detector).Inspect(t.Context(), policy, contract.ProtocolAnthropicMessages, []byte(body))
			if err != nil || result.Decision != DecisionRedact {
				t.Fatalf("decision=%s err=%v", result.Decision, err)
			}
			for path, want := range map[string]string{
				"messages.3.content.0.content.1.tool_name": fixtureCreateIssueTool,
				"messages.3.content.0.content.2.tool_name": fixtureListIssuesTool,
				"tools.1.name": fixtureCreateIssueTool,
				"tools.2.name": fixtureListIssuesTool,
			} {
				got := ojson.GetBytes(result.Body, path).Str
				if got != want || !anthropicToolNamePattern.MatchString(got) {
					t.Errorf("%s = %q, want %q", path, got, want)
				}
			}
			for path, want := range map[string]string{
				"messages.2.content.0.id":          fixtureToolSearchUseID,
				"messages.2.content.0.name":        "ToolSearch",
				"messages.3.content.0.tool_use_id": fixtureToolSearchUseID,
			} {
				if got := ojson.GetBytes(result.Body, path).Str; got != want {
					t.Errorf("%s = %q, want %q", path, got, want)
				}
			}
			for _, segment := range seen {
				if strings.HasSuffix(segment.Path, "/tool_name") {
					t.Errorf("tool reference reached the detector at %s", segment.Path)
				}
			}
			// The secret in the same tool result is still replaced.
			if strings.Contains(string(result.Body), fixtureToolSearchSecret) {
				t.Fatalf("secret escaped redaction: %s", result.Body)
			}
			// The ToolSearch query is a tool argument and stays inspected. Its
			// placeholders are restored when the model repeats them.
			redactedQuery := ojson.GetBytes(result.Body, "messages.2.content.0.input.query").Str
			if redactedQuery == query {
				t.Fatalf("tool argument escaped inspection: %s", result.Body)
			}
			response := `{"type":"tool_use","input":{"query":"` + redactedQuery + `"}}`
			restored := RestorePlaceholders([]byte(response), result.Redactions)
			if got := ojson.GetBytes(restored, "input.query").Str; got != query {
				t.Fatalf("restored query = %q, want %q", got, query)
			}
			// Only the query and the secret changed, byte for byte.
			placeholders := make(map[string]string, len(result.Redactions))
			for _, redaction := range result.Redactions {
				placeholders[redaction.Value] = redaction.Placeholder
			}
			want := strings.NewReplacer(
				`"query":"`+query+`"`,
				`"query":"select:`+placeholders[fixtureCreateIssueTool]+`,`+placeholders[fixtureListIssuesTool]+`"`,
				fixtureToolSearchSecret, placeholders[fixtureToolSearchSecret],
			).Replace(body)
			if string(result.Body) != want {
				t.Fatalf("unexpected rewrite:\n got: %s\nwant: %s", result.Body, want)
			}
		})
	}
}

// One case per call identifier position that a key-based skip missed. The
// fixture identifiers appear only at protocol positions; EMAIL_A and EMAIL_B
// mark ordinary text, tool arguments and tool results that must still be
// inspected.
func TestCallIdentitiesSurviveRedaction(t *testing.T) {
	const (
		toolName     = "mcp__fixture__lookup_record"
		serverName   = "crm_fictional_server"
		serverToolID = "srvtoolu_01FictionalCaller000001"
		namespace    = "mcp__fixture_notes"
		function     = "fixture_find_note"
		customTool   = "fixture_apply_patch"
		imageResult  = "iVBORw0KGgoFictionalImageResult0001"
		geminiName   = "fixture_list_records"
		geminiCallID = "fc_fictionalnoargs000000000001"
		vectorStore  = "vs_fictionalstore000000000001"
		callerTool   = "code_execution_20250825"
		// Protocol type tags and error codes inside tool results.
		resultBlock = "search_result"
		outputPart  = "input_text"
		errorCode   = "max_uses_exceeded"
	)
	identifiers := []string{toolName, serverName, serverToolID, namespace, function,
		customTool, imageResult, geminiName, geminiCallID, vectorStore, callerTool,
		resultBlock, outputPart, errorCode}
	for _, fixture := range []struct {
		protocolIDFixture
		inspectDeclarations bool
	}{
		{protocolIDFixture{"anthropic/server_tool_search", contract.ProtocolAnthropicMessages, `{"messages":[
 {"role":"user","content":"find a tool for EMAIL_A"},
 {"role":"assistant","content":[
  {"type":"server_tool_use","id":"srvtoolu_01FictionalSearch000002","name":"tool_search_tool_regex","input":{"query":"EMAIL_A"}},
  {"type":"tool_search_tool_result","tool_use_id":"srvtoolu_01FictionalSearch000002","content":{"type":"tool_search_tool_search_result","tool_references":[
   {"type":"tool_reference","tool_name":"` + toolName + `"}
  ]}},
  {"type":"text","text":"found it for EMAIL_B"}
 ]}
]}`}, false},
		{protocolIDFixture{"anthropic/mcp_server_name", contract.ProtocolAnthropicMessages, `{"messages":[
 {"role":"assistant","content":[{"type":"mcp_tool_use","id":"mcptoolu_01FictionalServer00001","name":"lookup","server_name":"` + serverName + `","input":{"q":"EMAIL_A"}}]},
 {"role":"user","content":[{"type":"mcp_tool_result","tool_use_id":"mcptoolu_01FictionalServer00001","content":[{"type":"text","text":"owner EMAIL_B"}]}]}
]}`}, false},
		{protocolIDFixture{"anthropic/caller_tool_id", contract.ProtocolAnthropicMessages, `{"messages":[
 {"role":"assistant","content":[
  {"type":"server_tool_use","id":"` + serverToolID + `","name":"code_execution","input":{"code":"lookup('EMAIL_A')"}},
  {"type":"tool_use","id":"toolu_01FictionalCalled00000001","name":"lookup","input":{"q":"x"},"caller":{"type":"code_execution_20250825","tool_id":"` + serverToolID + `"}}
 ]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01FictionalCalled00000001","content":"owner EMAIL_B"}]}
]}`}, false},
		{protocolIDFixture{"responses/mcp_server_label", contract.ProtocolOpenAIResponses, `{"input":[
 {"type":"mcp_list_tools","id":"mcpl_0fictional","server_label":"` + serverName + `","tools":[{"name":"lookup","description":"Look up a record","input_schema":{"type":"object"}}]},
 {"type":"mcp_approval_request","id":"mcpr_0fictional","server_label":"` + serverName + `","name":"lookup","arguments":"{\"q\":\"EMAIL_A\"}"},
 {"type":"mcp_call","id":"mcp_0fictional","server_label":"` + serverName + `","name":"lookup","arguments":"{}","output":"owner EMAIL_B"}
]}`}, false},
		{protocolIDFixture{"responses/mcp_call_without_arguments", contract.ProtocolOpenAIResponses, `{"input":[
 {"role":"user","content":"look up EMAIL_A"},
 {"type":"mcp_call","id":"mcp_0fictional","server_label":"` + serverName + `","name":"` + toolName + `","output":"owner EMAIL_B"}
]}`}, false},
		{protocolIDFixture{"anthropic/tool_result_block_types", contract.ProtocolAnthropicMessages, `{"messages":[
 {"role":"assistant","content":[
  {"type":"tool_use","id":"toolu_01FictionalBlocks000000001","name":"search_docs","input":{"q":"x"}},
  {"type":"server_tool_use","id":"srvtoolu_01FictionalBlocks00001","name":"web_search","input":{"query":"y"}},
  {"type":"web_search_tool_result","tool_use_id":"srvtoolu_01FictionalBlocks00001","content":{"type":"web_search_tool_result_error","error_code":"` + errorCode + `"}}
 ]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01FictionalBlocks000000001","content":[
  {"type":"` + resultBlock + `","source":"https://docs.example.org/a","title":"Doc","content":[{"type":"text","text":"owner EMAIL_A"}]},
  {"type":"text","text":"and EMAIL_B"}
 ]}]}
]}`}, false},
		{protocolIDFixture{"responses/function_output_parts", contract.ProtocolOpenAIResponses, `{"input":[
 {"type":"function_call","call_id":"call_fictionalparts0000000001","name":"lookup","arguments":"{}"},
 {"type":"function_call_output","call_id":"call_fictionalparts0000000001","output":[{"type":"` + outputPart + `","text":"owner EMAIL_A"}]},
 {"role":"user","content":"and EMAIL_B"}
]}`}, false},
		{protocolIDFixture{"responses/tool_search_output", contract.ProtocolOpenAIResponses, `{"input":[
 {"type":"tool_search_call","call_id":"call_fictionalsearch0000000001","execution":"client","arguments":{"query":"EMAIL_A"},"status":"completed"},
 {"type":"tool_search_output","call_id":"call_fictionalsearch0000000001","execution":"client","status":"completed","tools":[
  {"type":"namespace","name":"` + namespace + `","description":"Notes","tools":[
   {"type":"function","name":"` + function + `","description":"Find a note","parameters":{"type":"object"}},
   {"type":"custom","name":"` + customTool + `","description":"Patch a note"}
  ]}
 ]},
 {"type":"function_call","namespace":"` + namespace + `","name":"` + function + `","call_id":"call_fictionalnote00000000001","arguments":"{\"q\":\"EMAIL_B\"}"}
]}`}, true},
		{protocolIDFixture{"responses/image_generation_result", contract.ProtocolOpenAIResponses, `{"input":[
 {"role":"user","content":"draw a card for EMAIL_A"},
 {"type":"image_generation_call","id":"ig_0fictional","status":"completed","result":"` + imageResult + `"},
 {"role":"user","content":"make it bluer for EMAIL_B"}
]}`}, false},
		{protocolIDFixture{"chat/custom_tool_call", contract.ProtocolOpenAIChat, `{"messages":[
 {"role":"assistant","content":null,"tool_calls":[{"id":"call_fictionalcustom000000001","type":"custom","custom":{"name":"` + customTool + `","input":"notify EMAIL_A"}}]},
 {"role":"tool","tool_call_id":"call_fictionalcustom000000001","content":"owner EMAIL_B"}
]}`}, false},
		{protocolIDFixture{"gemini/function_call_without_args", contract.ProtocolGoogleGenerateContent, `{"contents":[
 {"role":"user","parts":[{"text":"list records for EMAIL_A"}]},
 {"role":"model","parts":[{"functionCall":{"id":"` + geminiCallID + `","name":"` + geminiName + `"}}]},
 {"role":"user","parts":[{"functionResponse":{"id":"` + geminiCallID + `","name":"` + geminiName + `"}},{"text":"and EMAIL_B"}]}
]}`}, false},
		// Declarations, inspected on request.
		{protocolIDFixture{"anthropic/declarations", contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":"ask EMAIL_A"}],
 "mcp_servers":[{"type":"url","name":"` + serverName + `","url":"https://mcp.example.org/"}],
 "tools":[{"type":"mcp_toolset","mcp_server_name":"` + serverName + `"},{"name":"` + toolName + `","description":"see EMAIL_B","defer_loading":true}]}`}, true},
		{protocolIDFixture{"chat/declarations", contract.ProtocolOpenAIChat, `{"messages":[{"role":"user","content":"ask EMAIL_A"}],
 "tools":[{"type":"custom","custom":{"name":"` + customTool + `","description":"see EMAIL_B"}},{"type":"function","function":{"name":"` + function + `"}}]}`}, true},
		{protocolIDFixture{"gemini/declarations", contract.ProtocolGoogleGenerateContent, `{"contents":[{"role":"user","parts":[{"text":"ask EMAIL_A"}]}],
 "tools":[{"functionDeclarations":[{"name":"` + geminiName + `","description":"see EMAIL_B"}]}]}`}, true},
		{protocolIDFixture{"anthropic/allowed_callers", contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":"ask EMAIL_A"}],
 "tools":[{"name":"` + toolName + `","description":"see EMAIL_B","allowed_callers":["` + callerTool + `"]}]}`}, true},
		{protocolIDFixture{"responses/mcp_tool_filters", contract.ProtocolOpenAIResponses, `{"input":"ask EMAIL_A",
 "tools":[
  {"type":"mcp","server_label":"` + serverName + `","server_url":"https://mcp.example.org/","server_description":"see EMAIL_B",
   "allowed_tools":{"tool_names":["` + toolName + `"]},"require_approval":{"never":{"tool_names":["` + toolName + `"]},"always":{"tool_names":["` + function + `"]}}},
  {"type":"mcp","server_label":"` + namespace + `","server_url":"https://mcp.example.org/","allowed_tools":["` + function + `"]},
  {"type":"file_search","vector_store_ids":["` + vectorStore + `"]}
 ]}`}, true},
		{protocolIDFixture{"responses/declarations", contract.ProtocolOpenAIResponses, `{"input":"ask EMAIL_A",
 "tools":[{"type":"custom","name":"` + customTool + `","description":"see EMAIL_B"},{"type":"mcp","server_label":"` + serverName + `","server_url":"https://mcp.example.org/"}]}`}, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			body := strings.NewReplacer("EMAIL_A", "alice@example.com", "EMAIL_B", "bob@example.com").Replace(fixture.body)
			var seen []Segment
			flagged := append([]string{"alice@example.com", "bob@example.com"}, identifiers...)
			policy := Policy{Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID,
				Action: ActionRedact, InspectToolDeclarations: fixture.inspectDeclarations}
			result, err := newTestEngine(t, flaggingDetector(flagged, &seen)).Inspect(t.Context(), policy, fixture.protocol, []byte(body))
			if err != nil || result.Decision != DecisionRedact {
				t.Fatalf("decision=%s err=%v", result.Decision, err)
			}
			for _, segment := range seen {
				for _, identifier := range identifiers {
					if strings.Contains(segment.Value, identifier) {
						t.Fatalf("identifier %s reached the detector at %s", identifier, segment.Path)
					}
				}
			}
			for _, identifier := range identifiers {
				if strings.Count(string(result.Body), identifier) != strings.Count(body, identifier) {
					t.Fatalf("identifier %s changed:\n%s", identifier, result.Body)
				}
			}
			for _, email := range []string{"alice@example.com", "bob@example.com"} {
				if strings.Contains(string(result.Body), email) {
					t.Fatalf("ordinary content escaped redaction: %s", result.Body)
				}
			}
			assertOnlyRedactionsChanged(t, body, result)
		})
	}
}

// Responses tool search outputs and MCP tool listings are declarations: like
// the tools array, they are left out unless the policy inspects declarations.
func TestResponsesListedToolsFollowDeclarationPolicy(t *testing.T) {
	const body = `{"input":[
 {"type":"tool_search_output","call_id":"call_fictionalsearch0000000002","tools":[{"type":"function","name":"find","description":"docs@example.net","parameters":{}}]},
 {"type":"mcp_list_tools","id":"mcpl_0fictional","server_label":"crm","tools":[{"name":"lookup","description":"docs@example.net","input_schema":{}}]},
 {"role":"user","content":"hi"}
]}`
	skipped := extractedPaths(t, contract.ProtocolOpenAIResponses, body, InspectionOptions{})
	for _, path := range skipped {
		if strings.HasPrefix(path, "/input/0/tools") || strings.HasPrefix(path, "/input/1/tools") {
			t.Errorf("declaration extracted without opt-in: %s", path)
		}
	}
	inspected := extractedPaths(t, contract.ProtocolOpenAIResponses, body, InspectionOptions{InspectToolDeclarations: true})
	for _, want := range []string{"/input/0/tools/0/description", "/input/1/tools/0/description"} {
		if !containsString(inspected, want) {
			t.Errorf("declaration description %s not inspected on opt-in: %v", want, inspected)
		}
	}
	for _, path := range inspected {
		if strings.HasSuffix(path, "/name") {
			t.Errorf("declared tool name extracted: %s", path)
		}
	}
}

// Identifiers are left out by protocol position: the same keys in tool
// arguments and tool results stay inspected.
func TestCallIdentityNamesDoNotExemptPayloads(t *testing.T) {
	const secret = "tok_fictional_payload_value_0003"
	for _, fixture := range []struct {
		protocol contract.ProtocolID
		body     string
	}{
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_keep","name":"t","input":{"tool_name":"%s"}}]}]}`},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_keep","name":"t","input":{"type":"tool_reference","tool_name":"%s"}}]}]}`},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_keep","content":"{\"type\":\"tool_reference\",\"tool_name\":\"%s\"}"}]}]}`},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"assistant","content":[{"type":"mcp_tool_use","id":"mcptoolu_keep","name":"t","server_name":"crm","input":{"server_name":"%s"}}]}]}`},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_keep","name":"t","input":{"caller":{"tool_id":"%s"}}}]}]}`},
		{contract.ProtocolOpenAIResponses, `{"input":[{"type":"function_call","call_id":"call_keep","name":"t","arguments":"{\"server_label\":\"%s\"}"}]}`},
		{contract.ProtocolOpenAIResponses, `{"input":[{"type":"function_call_output","call_id":"call_keep","output":{"type":"mcp_call","server_label":"%s"}}]}`},
		{contract.ProtocolOpenAIResponses, `{"input":[{"type":"function_call_output","call_id":"call_keep","output":{"type":"tool_search_output","tools":[{"type":"custom","name":"%s"}]}}]}`},
		{contract.ProtocolGoogleGenerateContent, `{"contents":[{"role":"model","parts":[{"functionCall":{"id":"fc_keep","name":"t","args":{"name":"%s"}}}]}]}`},
	} {
		body := strings.Replace(fixture.body, "%s", secret, 1)
		t.Run(string(fixture.protocol)+"/"+fixture.body, func(t *testing.T) {
			var seen []Segment
			policy := Policy{Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID, Action: ActionRedact}
			result, err := newTestEngine(t, flaggingDetector([]string{secret}, &seen)).Inspect(t.Context(), policy, fixture.protocol, []byte(body))
			if err != nil || result.Decision != DecisionRedact || strings.Contains(string(result.Body), secret) {
				t.Fatalf("payload value escaped inspection: decision=%s err=%v body=%s", result.Decision, err, result.Body)
			}
		})
	}
}
