package privacy

import (
	"context"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// All identifiers below are fictional. The detector flags every one of them
// wherever it is asked to look, so only the extraction whitelist keeps them
// intact.
const (
	fixtureToolUseID     = "toolu_01FictionalPair00000000001"
	fixtureServerToolID  = "srvtoolu_01FictionalSearch000001"
	fixtureMCPToolID     = "mcptoolu_01FictionalLookup00001"
	fixtureUploadFileID  = "file_011FictionalUpload00000001"
	fixtureCitationIndex = "EqQBCioIAhgBIiQ0FictionalIndex01"
	fixtureResponseID    = "resp_0fictional00000000000000001"
	fixtureItemRefID     = "msg_0fictional000000000000000002"
	fixtureCallID        = "call_fictional00000000000000003"
	fixtureApprovalID    = "mcpr_0fictional0000000000000004"
	fixtureContainerID   = "cntr_0fictional0000000000000006"
	fixtureInputFileID   = "file-Fictional0000000000000007"
	fixtureImageFileID   = "file-Fictional0000000000000008"
	fixtureContainerFile = "cfile_0fictional000000000000009"
	fixtureChatFileID    = "file-FictionalChat000000000001"
	fixtureChatCallID    = "call_fictionalchat0000000000002"
	fixtureGeminiCallID  = "fc_fictional0000000000000000001"
	// Addendum 1: typed references inside tool results.
	fixtureOutputFileID   = "file-FictionalToolOutput0000001"
	fixtureOutputImageID  = "file-FictionalToolOutput0000002"
	fixtureCustomFileID   = "file-FictionalToolOutput0000003"
	fixtureScreenshotID   = "file-FictionalScreenshot0000001"
	fixtureCodeOutputID   = "file_011FictionalCodeOutput00001"
	fixtureBashOutputID   = "file_011FictionalBashOutput00001"
	fixtureSearchContent  = "EqwBCioIAhgBIiQ0FictionalPageContent01"
	fixturePromptFileID   = "file-FictionalPromptVariable0001"
	fixtureComputerCallID = "call_fictionalcomputer000000004"
)

var fixtureProtocolIDs = []string{
	fixtureToolUseID, fixtureServerToolID, fixtureMCPToolID, fixtureUploadFileID,
	fixtureCitationIndex, fixtureResponseID, fixtureItemRefID, fixtureCallID,
	fixtureApprovalID, fixtureContainerID, fixtureInputFileID, fixtureImageFileID,
	fixtureContainerFile, fixtureChatFileID, fixtureChatCallID, fixtureGeminiCallID,
	fixtureOutputFileID, fixtureOutputImageID, fixtureCustomFileID, fixtureScreenshotID,
	fixtureCodeOutputID, fixtureBashOutputID, fixtureSearchContent, fixturePromptFileID,
	fixtureComputerCallID,
}

type protocolIDFixture struct {
	name     string
	protocol contract.ProtocolID
	// EMAIL_A/EMAIL_B mark ordinary content that must still be inspected.
	body string
}

func protocolIDFixtures() []protocolIDFixture {
	return []protocolIDFixture{
		{"anthropic/tool_use_pairs", contract.ProtocolAnthropicMessages, `{"model":"claude-test","messages":[
 {"role":"assistant","content":[{"type":"tool_use","id":"` + fixtureToolUseID + `","name":"read_file","input":{"path":"notes.txt"}}]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"` + fixtureToolUseID + `","content":[{"type":"text","text":"owner EMAIL_A"}]}]},
 {"role":"assistant","content":[
  {"type":"server_tool_use","id":"` + fixtureServerToolID + `","name":"web_search","input":{"query":"weather"}},
  {"type":"web_search_tool_result","tool_use_id":"` + fixtureServerToolID + `","content":[]},
  {"type":"mcp_tool_use","id":"` + fixtureMCPToolID + `","name":"lookup","server_name":"crm","input":{}},
  {"type":"text","text":"done","citations":[{"type":"web_search_result_location","url":"https://example.org/","title":"t","encrypted_index":"` + fixtureCitationIndex + `","cited_text":"c"}]}
 ]},
 {"role":"user","content":[
  {"type":"mcp_tool_result","tool_use_id":"` + fixtureMCPToolID + `","content":[{"type":"text","text":"crm row"}]},
  {"type":"container_upload","file_id":"` + fixtureUploadFileID + `"},
  {"type":"text","text":"call EMAIL_B"}
 ]}
]}`},
		{"responses/round_trip_items", contract.ProtocolOpenAIResponses, `{"model":"gpt-test","previous_response_id":"` + fixtureResponseID + `","input":[
 {"type":"item_reference","id":"` + fixtureItemRefID + `"},
 {"type":"function_call","call_id":"` + fixtureCallID + `","name":"lookup","arguments":"{\"q\":\"x\"}"},
 {"type":"function_call_output","call_id":"` + fixtureCallID + `","output":"owner EMAIL_A"},
 {"type":"mcp_approval_request","id":"` + fixtureApprovalID + `","name":"lookup","server_label":"crm","arguments":"{}"},
 {"type":"mcp_approval_response","approval_request_id":"` + fixtureApprovalID + `","approve":true},
 {"type":"code_interpreter_call","id":"ci_0fictional","container_id":"` + fixtureContainerID + `","code":"print(1)","outputs":[],"status":"completed"},
 {"role":"user","content":[
  {"type":"input_file","file_id":"` + fixtureInputFileID + `"},
  {"type":"input_image","file_id":"` + fixtureImageFileID + `","detail":"auto"},
  {"type":"input_text","text":"mail EMAIL_B"}
 ]},
 {"role":"assistant","type":"message","content":[{"type":"output_text","text":"ok","annotations":[
  {"type":"file_citation","file_id":"` + fixtureInputFileID + `","index":0,"filename":"notes.txt"},
  {"type":"container_file_citation","container_id":"` + fixtureContainerID + `","file_id":"` + fixtureContainerFile + `","start_index":0,"end_index":2,"filename":"out.csv"}
 ]}]}
]}`},
		{"chat/file_and_tool_call", contract.ProtocolOpenAIChat, `{"model":"gpt-test","messages":[
 {"role":"user","content":[{"type":"file","file":{"file_id":"` + fixtureChatFileID + `","filename":"a.pdf"}},{"type":"text","text":"from EMAIL_A"}]},
 {"role":"assistant","content":null,"tool_calls":[{"id":"` + fixtureChatCallID + `","type":"function","function":{"name":"lookup","arguments":"{}"}}]},
 {"role":"tool","tool_call_id":"` + fixtureChatCallID + `","content":"owner EMAIL_B"}
]}`},
		{"gemini/function_pair", contract.ProtocolGoogleGenerateContent, `{"contents":[
 {"role":"model","parts":[{"functionCall":{"id":"` + fixtureGeminiCallID + `","name":"lookup","args":{"q":"EMAIL_A"}}}]},
 {"role":"user","parts":[{"functionResponse":{"id":"` + fixtureGeminiCallID + `","name":"lookup","response":{"owner":"EMAIL_B"}}}]}
]}`},
		// Addendum 1: references a tool result carries inside its payload are
		// kept by their typed position; the rest of the payload stays inspected.
		{"responses/tool_output_files", contract.ProtocolOpenAIResponses, `{"model":"gpt-test","input":[
 {"type":"function_call","call_id":"` + fixtureCallID + `","name":"render","arguments":"{}"},
 {"type":"function_call_output","call_id":"` + fixtureCallID + `","output":[
  {"type":"input_text","text":"owner EMAIL_A"},
  {"type":"input_file","file_id":"` + fixtureOutputFileID + `"},
  {"type":"input_image","file_id":"` + fixtureOutputImageID + `","detail":"auto"}
 ]},
 {"type":"custom_tool_call_output","call_id":"` + fixtureCallID + `","output":[{"type":"input_file","file_id":"` + fixtureCustomFileID + `"}]},
 {"type":"computer_call","id":"cu_0fictional","call_id":"` + fixtureComputerCallID + `","action":{"type":"screenshot"},"pending_safety_checks":[],"status":"completed"},
 {"type":"computer_call_output","call_id":"` + fixtureComputerCallID + `","output":{"type":"computer_screenshot","file_id":"` + fixtureScreenshotID + `"}},
 {"role":"user","content":[{"type":"input_text","text":"mail EMAIL_B"}]}
]}`},
		{"responses/prompt_variable_file", contract.ProtocolOpenAIResponses, `{"model":"gpt-test","prompt":{"id":"pmpt_0fictional","variables":{
 "doc":{"type":"input_file","file_id":"` + fixturePromptFileID + `"},
 "owner":"EMAIL_A"
}},"input":"reply to EMAIL_B"}`},
		{"anthropic/server_tool_results", contract.ProtocolAnthropicMessages, `{"model":"claude-test","messages":[
 {"role":"user","content":"chart the csv"},
 {"role":"assistant","content":[
  {"type":"server_tool_use","id":"` + fixtureServerToolID + `","name":"code_execution","input":{"code":"print(1)"}},
  {"type":"code_execution_tool_result","tool_use_id":"` + fixtureServerToolID + `","content":{"type":"code_execution_result","stdout":"owner EMAIL_A","stderr":"","return_code":0,"content":[
   {"type":"code_execution_output","file_id":"` + fixtureCodeOutputID + `"}
  ]}},
  {"type":"server_tool_use","id":"` + fixtureMCPToolID + `","name":"bash_code_execution","input":{"command":"ls"}},
  {"type":"bash_code_execution_tool_result","tool_use_id":"` + fixtureMCPToolID + `","content":{"type":"bash_code_execution_result","stdout":"","stderr":"","return_code":0,"content":[
   {"type":"bash_code_execution_output","file_id":"` + fixtureBashOutputID + `"}
  ]}},
  {"type":"server_tool_use","id":"` + fixtureToolUseID + `","name":"web_search","input":{"query":"weather"}},
  {"type":"web_search_tool_result","tool_use_id":"` + fixtureToolUseID + `","content":[
   {"type":"web_search_result","url":"https://example.org/","title":"t","encrypted_content":"` + fixtureSearchContent + `","page_age":null}
  ]},
  {"type":"text","text":"mailed EMAIL_B"}
 ]}
]}`},
	}
}

// flaggingDetector reports every occurrence of the given values, as a local
// model that mistakes opaque ids for secrets would, and records what it saw.
func flaggingDetector(values []string, seen *[]Segment) Detector {
	return DetectorFunc(func(_ context.Context, input DetectInput) ([]Finding, error) {
		*seen = append(*seen, input.Segments...)
		var findings []Finding
		for index, segment := range input.Segments {
			for _, value := range values {
				kind := KindCommonSecret
				if strings.Contains(value, "@") {
					kind = KindEmail
				}
				for offset := 0; ; {
					start := strings.Index(segment.Value[offset:], value)
					if start < 0 {
						break
					}
					start += offset
					findings = append(findings, Finding{
						Segment: index, Start: start, End: start + len(value), Kind: kind, Confidence: 0.99,
					})
					offset = start + len(value)
				}
			}
		}
		return findings, nil
	})
}

func TestProtocolRoundTripIDsSurviveRedaction(t *testing.T) {
	flagged := append([]string{"alice@example.com", "bob@example.com"}, fixtureProtocolIDs...)
	for _, fixture := range protocolIDFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			body := strings.NewReplacer("EMAIL_A", "alice@example.com", "EMAIL_B", "bob@example.com").Replace(fixture.body)
			var seen []Segment
			policy := Policy{Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID, Action: ActionRedact}
			result, err := newTestEngine(t, flaggingDetector(flagged, &seen)).Inspect(t.Context(), policy, fixture.protocol, []byte(body))
			if err != nil || result.Decision != DecisionRedact {
				t.Fatalf("decision=%s err=%v", result.Decision, err)
			}
			for _, segment := range seen {
				for _, id := range fixtureProtocolIDs {
					if strings.Contains(segment.Value, id) {
						t.Fatalf("protocol id reached the detector at %s", segment.Path)
					}
				}
			}
			for _, id := range fixtureProtocolIDs {
				if strings.Count(string(result.Body), id) != strings.Count(body, id) {
					t.Fatalf("protocol id %s changed:\n%s", id, result.Body)
				}
			}
			// Tool results, tool arguments and message text stay inspected.
			for _, email := range []string{"alice@example.com", "bob@example.com"} {
				if strings.Contains(string(result.Body), email) {
					t.Fatalf("ordinary content escaped redaction: %s", result.Body)
				}
			}
			assertOnlyRedactionsChanged(t, body, result)
		})
	}
}

func TestProtocolRoundTripIDsDoNotTriggerPrivacyActions(t *testing.T) {
	for _, fixture := range protocolIDFixtures() {
		for _, action := range []Action{ActionRedact, ActionWarn, ActionBlock} {
			t.Run(fixture.name+"/"+string(action), func(t *testing.T) {
				body := strings.NewReplacer("EMAIL_A", "ordinary text", "EMAIL_B", "more text").Replace(fixture.body)
				var seen []Segment
				policy := Policy{Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID, Action: action}
				result, err := newTestEngine(t, flaggingDetector(fixtureProtocolIDs, &seen)).Inspect(t.Context(), policy, fixture.protocol, []byte(body))
				if err != nil || result.Decision != DecisionAllow || len(result.Findings) != 0 || result.Body != nil {
					t.Fatalf("protocol ids triggered privacy: decision=%s findings=%d err=%v", result.Decision, len(result.Findings), err)
				}
			})
		}
	}
}

// Identifiers are left out by protocol position, not by name: the same keys
// inside a tool payload or a caller-named prompt variable stay inspected.
func TestProtocolRoundTripIDNamesDoNotExemptPayloads(t *testing.T) {
	const secret = "tok_fictional_payload_value_0001"
	for _, fixture := range []struct {
		protocol contract.ProtocolID
		body     string
	}{
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_keep","name":"t","input":{"tool_use_id":"%s"}}]}]}`},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"assistant","content":[{"type":"mcp_tool_use","id":"mcptoolu_keep","name":"t","input":{"nested":{"approval_request_id":"%s"}}}]}]}`},
		{contract.ProtocolOpenAIResponses, `{"input":[{"type":"custom_tool_call","call_id":"call_keep","name":"t","input":{"previous_response_id":"%s"}}]}`},
		{contract.ProtocolGoogleGenerateContent, `{"contents":[{"role":"model","parts":[{"functionCall":{"id":"fc_keep","name":"t","args":{"item_id":"%s"}}}]}]}`},
		{contract.ProtocolGoogleGenerateContent, `{"contents":[{"role":"user","parts":[{"functionResponse":{"id":"fc_keep","name":"t","response":{"container_id":"%s"}}}]}]}`},
		{contract.ProtocolOpenAIResponses, `{"prompt":{"id":"pmpt_keep","variables":{"customer_id":"%s"}}}`},
		// A tool argument shaped like a typed tool result is still payload.
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_keep","name":"t","input":{"type":"bash_code_execution_tool_result","content":{"type":"bash_code_execution_result","content":[{"type":"bash_code_execution_output","file_id":"%s"}]}}}]}]}`},
		{contract.ProtocolOpenAIResponses, `{"prompt":{"id":"pmpt_keep","variables":{"file_id":"%s"}}}`},
	} {
		body := strings.Replace(fixture.body, "%s", secret, 1)
		t.Run(string(fixture.protocol)+"/"+fixture.body, func(t *testing.T) {
			var seen []Segment
			policy := Policy{Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID, Action: ActionRedact}
			result, err := newTestEngine(t, flaggingDetector([]string{secret}, &seen)).Inspect(t.Context(), policy, fixture.protocol, []byte(body))
			if err != nil || result.Decision != DecisionRedact || strings.Contains(string(result.Body), secret) {
				t.Fatalf("payload value escaped inspection: decision=%s err=%v body=%s", result.Decision, err, result.Body)
			}
			for _, kept := range []string{"toolu_keep", "mcptoolu_keep", "call_keep", "fc_keep", "pmpt_keep"} {
				if strings.Count(string(result.Body), kept) != strings.Count(body, kept) {
					t.Fatalf("protocol id %s changed: %s", kept, result.Body)
				}
			}
		})
	}
}

// Addendum 1: the JSON Schema dialect URI in a tool declaration is protocol
// metadata, not a URL the user wrote.
const fixtureSchemaDialect = "https://json-schema.org/draft/2020-12/schema"

func schemaDialectFixtures() []protocolIDFixture {
	schema := `{"$schema":"` + fixtureSchemaDialect + `","type":"object","required":["q"],` +
		`"properties":{"q":{"type":"string","description":"ask EMAIL_A"}}}`
	return []protocolIDFixture{
		{"responses/parameters", contract.ProtocolOpenAIResponses,
			`{"input":"hi","tools":[{"type":"function","name":"lookup","description":"see EMAIL_B","parameters":` + schema + `}]}`},
		{"chat/tools_and_functions", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"user","content":"hi"}],` +
				`"tools":[{"type":"function","function":{"name":"lookup","description":"see EMAIL_B","parameters":` + schema + `}}],` +
				`"functions":[{"name":"legacy","parameters":` + schema + `}]}`},
		{"anthropic/input_schema", contract.ProtocolAnthropicMessages,
			`{"messages":[{"role":"user","content":"hi"}],` +
				`"tools":[{"name":"lookup","description":"see EMAIL_B","input_schema":` + schema + `}]}`},
		{"gemini/parameters_json_schema", contract.ProtocolGoogleGenerateContent,
			`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],` +
				`"tools":[{"functionDeclarations":[{"name":"lookup","description":"see EMAIL_B","parametersJsonSchema":` + schema + `}]}]}`},
	}
}

func TestJSONSchemaDialectSurvivesToolDeclarationInspection(t *testing.T) {
	emails := strings.NewReplacer("EMAIL_A", "alice@example.com", "EMAIL_B", "bob@example.com")
	for _, fixture := range schemaDialectFixtures() {
		body := emails.Replace(fixture.body)
		check := func(t *testing.T, result Result, err error) {
			t.Helper()
			if err != nil || result.Decision != DecisionRedact {
				t.Fatalf("decision=%s err=%v", result.Decision, err)
			}
			if strings.Count(string(result.Body), fixtureSchemaDialect) != strings.Count(body, fixtureSchemaDialect) {
				t.Fatalf("schema dialect changed:\n%s", result.Body)
			}
			// Descriptions in the same declaration stay inspected.
			for _, email := range []string{"alice@example.com", "bob@example.com"} {
				if strings.Contains(string(result.Body), email) {
					t.Fatalf("declaration text escaped redaction: %s", result.Body)
				}
			}
			assertOnlyRedactionsChanged(t, body, result)
		}
		t.Run(fixture.name+"/model", func(t *testing.T) {
			var seen []Segment
			flagged := []string{fixtureSchemaDialect, "alice@example.com", "bob@example.com"}
			policy := Policy{Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID,
				Action: ActionRedact, InspectToolDeclarations: true}
			result, err := newTestEngine(t, flaggingDetector(flagged, &seen)).Inspect(t.Context(), policy, fixture.protocol, []byte(body))
			check(t, result, err)
			for _, segment := range seen {
				if strings.Contains(segment.Value, fixtureSchemaDialect) {
					t.Fatalf("schema dialect reached the detector at %s", segment.Path)
				}
			}
		})
		t.Run(fixture.name+"/regex", func(t *testing.T) {
			policy := tokenPolicy()
			policy.InspectToolDeclarations = true
			result, err := mustTestEngine(t).Inspect(t.Context(), policy, fixture.protocol, []byte(body))
			check(t, result, err)
		})
	}
}

// $schema in a tool payload, in message content, or as a schema property name
// keeps today's behaviour and is inspected.
func TestJSONSchemaDialectKeyOutsideSchemasIsInspected(t *testing.T) {
	const secret = "tok_fictional_payload_value_0002"
	for _, fixture := range []struct {
		protocol contract.ProtocolID
		body     string
		value    string
	}{
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_keep","name":"t","input":{"$schema":"%s"}}]}]}`, fixtureSchemaDialect},
		{contract.ProtocolOpenAIResponses, `{"input":[{"type":"function_call_output","call_id":"call_keep","output":"{\"$schema\":\"%s\"}"}]}`, fixtureSchemaDialect},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"t","input_schema":{"type":"object","properties":{"$schema":{"type":"string","description":"%s"}}}}]}`, secret},
	} {
		body := strings.Replace(fixture.body, "%s", fixture.value, 1)
		t.Run(string(fixture.protocol)+"/"+fixture.body, func(t *testing.T) {
			var seen []Segment
			policy := Policy{Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID,
				Action: ActionRedact, InspectToolDeclarations: true}
			result, err := newTestEngine(t, flaggingDetector([]string{fixture.value}, &seen)).Inspect(t.Context(), policy, fixture.protocol, []byte(body))
			if err != nil || result.Decision != DecisionRedact || strings.Contains(string(result.Body), fixture.value) {
				t.Fatalf("$schema value escaped inspection: decision=%s err=%v body=%s", result.Decision, err, result.Body)
			}
		})
	}
}
