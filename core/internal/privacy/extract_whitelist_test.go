package privacy

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Whitelist fixtures mark every string by what extraction must do with it:
// READ is content and always extracted, DECL is a tool declaration extracted
// only when declarations are inspected, and anything else (NEVER marks the
// protocol identifiers, unknown fields and unknown block or item types) is
// never extracted.
type whitelistFixture struct {
	name     string
	protocol contract.ProtocolID
	body     string
}

func whitelistFixtures() []whitelistFixture {
	return []whitelistFixture{
		{"anthropic", contract.ProtocolAnthropicMessages, `{"model":"NEVER","metadata":{"user_id":"NEVER"},
 "mcp_servers":[{"type":"url","name":"NEVER","url":"NEVER","authorization_token":"NEVER"}],
 "system":[{"type":"text","text":"READ","cache_control":{"type":"ephemeral"}}],
 "messages":[
  {"role":"user","unknown":"NEVER","content":[
   {"type":"text","text":"READ","extra":"NEVER","citations":[{"type":"char_location","cited_text":"READ","document_title":"READ","document_index":0}]},
   {"type":"image","source":{"type":"base64","media_type":"image/png","data":"NEVER"}},
   {"type":"document","title":"READ","context":"READ","source":{"type":"text","media_type":"text/plain","data":"READ"},"citations":{"enabled":true}},
   {"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"NEVER"}},
   {"type":"document","source":{"type":"url","url":"NEVER"}},
   {"type":"document","source":{"type":"file","file_id":"NEVER"}},
   {"type":"document","source":{"type":"content","content":[{"type":"text","text":"READ"},{"type":"image","source":{"type":"url","url":"NEVER"}}]}},
   {"type":"search_result","source":"READ","title":"READ","content":[{"type":"text","text":"READ"}]},
   {"type":"future_block","text":"NEVER","input":{"q":"NEVER"},"content":"NEVER"},
   {"type":"tool_result","tool_use_id":"NEVER","is_error":false,"content":[
    {"type":"text","text":"READ","nested":{"type":"tool_reference","tool_name":"NEVER"}},
    {"type":"tool_reference","tool_name":"NEVER"},
    {"type":"image","source":{"type":"base64","media_type":"image/png","data":"NEVER"}},
    {"type":"search_result","source":"READ","title":"READ","content":[{"type":"text","text":"READ"}]},
    {"type":"future_block","text":"NEVER"}
   ]},
   {"type":"tool_result","tool_use_id":"NEVER","content":"READ"},
   {"type":"mcp_tool_result","tool_use_id":"NEVER","content":[{"type":"text","text":"READ"}]},
   {"type":"container_upload","file_id":"NEVER"}
  ]},
  {"role":"assistant","content":[
   {"type":"thinking","thinking":"NEVER","signature":"NEVER"},
   {"type":"thinking","thinking":"READ"},
   {"type":"redacted_thinking","data":"NEVER"},
   {"type":"text","text":"READ","citations":[{"type":"web_search_result_location","url":"READ","title":"READ","encrypted_index":"NEVER","cited_text":"READ"}]},
   {"type":"tool_use","id":"NEVER","name":"NEVER","input":{"query":"READ","tool_name":"READ","id":"READ"},"caller":{"type":"direct","tool_id":"NEVER"}},
   {"type":"server_tool_use","id":"NEVER","name":"NEVER","input":{"query":"READ"}},
   {"type":"web_search_tool_result","tool_use_id":"NEVER","content":[{"type":"web_search_result","url":"READ","title":"READ","encrypted_content":"NEVER","page_age":"NEVER"}]},
   {"type":"web_fetch_tool_result","tool_use_id":"NEVER","content":{"type":"web_fetch_result","url":"READ","retrieved_at":"NEVER",
    "content":{"type":"document","title":"READ","source":{"type":"text","media_type":"text/plain","data":"READ"}}}},
   {"type":"code_execution_tool_result","tool_use_id":"NEVER","content":{"type":"code_execution_result","stdout":"READ","stderr":"READ","return_code":0,
    "content":[{"type":"code_execution_output","file_id":"NEVER"}]}},
   {"type":"bash_code_execution_tool_result","tool_use_id":"NEVER","content":{"type":"bash_code_execution_result","stdout":"READ","stderr":"READ","content":[]}},
   {"type":"text_editor_code_execution_tool_result","tool_use_id":"NEVER","content":{"type":"text_editor_code_execution_view_result","file_type":"NEVER","content":"READ"}},
   {"type":"text_editor_code_execution_tool_result","tool_use_id":"NEVER","content":{"type":"text_editor_code_execution_str_replace_result","lines":["READ"]}},
   {"type":"tool_search_tool_result","tool_use_id":"NEVER","content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"NEVER"}]}},
   {"type":"mcp_tool_use","id":"NEVER","name":"NEVER","server_name":"NEVER","input":{"q":"READ"}}
  ]}
 ],
 "tools":[
  {"name":"NEVER","description":"DECL","input_schema":{"type":"object","$schema":"NEVER","required":["NEVER"],
   "properties":{"q":{"type":"string","description":"DECL","enum":["NEVER"],"format":"NEVER","pattern":"NEVER","default":"DECL"}}},
   "input_examples":[{"q":"DECL"}],"allowed_callers":["NEVER"],"defer_loading":true},
  {"type":"mcp_toolset","mcp_server_name":"NEVER"},
  {"type":"web_search_20250305","name":"NEVER","allowed_domains":["NEVER"]}
 ]}`},
		{"chat", contract.ProtocolOpenAIChat, `{"model":"NEVER","user":"NEVER","messages":[
  {"role":"system","name":"NEVER","content":"READ"},
  {"role":"developer","content":[{"type":"text","text":"READ"}]},
  {"role":"user","name":"NEVER","unknown":"NEVER","content":[
   {"type":"text","text":"READ","extra":"NEVER"},
   {"type":"image_url","image_url":{"url":"NEVER","detail":"NEVER"}},
   {"type":"input_audio","input_audio":{"data":"NEVER","format":"NEVER"}},
   {"type":"file","file":{"file_id":"NEVER","file_data":"NEVER","filename":"READ"}},
   {"type":"future_part","text":"NEVER"},
   {"text":"NEVER"}
  ]},
  {"role":"assistant","name":"NEVER",
   "content":[{"type":"text","text":"READ"},{"type":"refusal","refusal":"READ"},{"type":"thinking","thinking":"NEVER","signature":"NEVER"},{"type":"thinking","thinking":"READ"}],
   "refusal":"READ","reasoning_content":"READ","reasoning":"READ",
   "reasoning_details":[{"type":"reasoning.text","text":"NEVER","signature":"NEVER"},{"type":"reasoning.text","text":"READ","id":"NEVER"},
    {"type":"reasoning.summary","summary":"READ"},{"type":"reasoning.encrypted","data":"NEVER","id":"NEVER"}],
   "thinking_blocks":[{"type":"thinking","thinking":"NEVER","signature":"NEVER"},{"type":"thinking","thinking":"READ"},{"type":"redacted_thinking","data":"NEVER"}],
   "annotations":[{"type":"url_citation","url_citation":{"title":"READ","url":"READ","start_index":0,"end_index":1}}],
   "audio":{"id":"NEVER"},
   "function_call":{"name":"NEVER","arguments":"READ"},
   "tool_calls":[
    {"id":"NEVER","type":"function","function":{"name":"NEVER","arguments":"READ","provider_specific_fields":{"thought_signature":"NEVER"}},
     "extra_content":{"google":{"thought_signature":"NEVER"}},"provider_specific_fields":{"thought_signature":"NEVER"}},
    {"id":"NEVER","type":"custom","custom":{"name":"NEVER","input":"READ"}}
   ]},
  {"role":"tool","tool_call_id":"NEVER","content":"READ"},
  {"role":"tool","tool_call_id":"NEVER","content":[{"type":"text","text":"READ"},{"type":"image_url","image_url":{"url":"NEVER"}}]},
  {"role":"function","name":"NEVER","content":"READ"}
 ],
 "tools":[
  {"type":"function","function":{"name":"NEVER","description":"DECL","strict":true,
   "parameters":{"type":"object","properties":{"q":{"type":"string","title":"DECL","examples":["DECL"],"const":"NEVER"}}}}},
  {"type":"custom","custom":{"name":"NEVER","description":"DECL","format":{"type":"grammar","grammar":{"syntax":"NEVER","definition":"NEVER"}}}}
 ],
 "functions":[{"name":"NEVER","description":"DECL","parameters":{"type":"object"}}],
 "tool_choice":{"type":"function","function":{"name":"NEVER"}},
 "prediction":{"type":"content","content":[{"type":"text","text":"READ"},{"type":"future_part","text":"NEVER"}]}}`},
		{"chat/prediction_string", contract.ProtocolOpenAIChat, `{"messages":[{"role":"user","content":"READ"}],"prediction":{"type":"content","content":"READ"}}`},
		{"responses", contract.ProtocolOpenAIResponses, `{"model":"NEVER","previous_response_id":"NEVER","instructions":"READ",
 "prompt":{"id":"NEVER","version":"NEVER","variables":{"customer":"READ","file_id":"READ",
  "note":{"type":"input_text","text":"READ"},"doc":{"type":"input_file","file_id":"NEVER","filename":"READ"},"pic":{"type":"input_image","image_url":"NEVER"}}},
 "input":[
  {"role":"user","content":"READ"},
  {"type":"message","role":"user","id":"NEVER","status":"NEVER","content":[
   {"type":"input_text","text":"READ","extra":"NEVER"},
   {"type":"input_image","image_url":"NEVER","file_id":"NEVER","detail":"NEVER"},
   {"type":"input_file","file_id":"NEVER","file_data":"NEVER","file_url":"NEVER","filename":"READ"},
   {"type":"future_part","text":"NEVER"}
  ]},
  {"type":"message","role":"assistant","id":"NEVER","content":[
   {"type":"output_text","text":"READ","logprobs":[{"token":"NEVER"}],"annotations":[
    {"type":"url_citation","url":"READ","title":"READ","start_index":0,"end_index":1},
    {"type":"file_citation","file_id":"NEVER","filename":"READ","index":0},
    {"type":"container_file_citation","container_id":"NEVER","file_id":"NEVER","filename":"READ"}]},
   {"type":"refusal","refusal":"READ"}
  ]},
  {"type":"reasoning","id":"NEVER","encrypted_content":"NEVER","summary":[{"type":"summary_text","text":"READ"}],"content":[{"type":"reasoning_text","text":"READ"}]},
  {"type":"compaction","id":"NEVER","encrypted_content":"NEVER"},
  {"type":"item_reference","id":"NEVER"},
  {"type":"future_item","text":"NEVER","content":"NEVER","arguments":"NEVER"},
  {"type":"function_call","id":"NEVER","call_id":"NEVER","name":"NEVER","namespace":"NEVER","arguments":"READ","status":"NEVER"},
  {"type":"function_call_output","call_id":"NEVER","output":"READ"},
  {"type":"function_call_output","call_id":"NEVER","output":[{"type":"input_text","text":"READ"},{"type":"input_image","image_url":"NEVER","file_id":"NEVER"},
   {"type":"input_file","file_id":"NEVER","filename":"READ"},{"type":"encrypted_content","encrypted_content":"NEVER"}]},
  {"type":"custom_tool_call","call_id":"NEVER","name":"NEVER","input":"READ"},
  {"type":"custom_tool_call_output","call_id":"NEVER","output":"READ"},
  {"type":"tool_search_call","call_id":"NEVER","execution":"NEVER","arguments":{"query":"READ"}},
  {"type":"tool_search_output","call_id":"NEVER","tools":[{"type":"namespace","name":"NEVER","description":"DECL","tools":[
   {"type":"function","name":"NEVER","description":"DECL","parameters":{"type":"object","properties":{"q":{"description":"DECL"}}}}]}]},
  {"type":"mcp_list_tools","id":"NEVER","server_label":"NEVER","tools":[{"name":"NEVER","description":"DECL","input_schema":{"type":"object"}}],"error":"READ"},
  {"type":"mcp_call","id":"NEVER","server_label":"NEVER","name":"NEVER","arguments":"READ","output":"READ","error":"READ","approval_request_id":"NEVER"},
  {"type":"mcp_approval_request","id":"NEVER","server_label":"NEVER","name":"NEVER","arguments":"READ"},
  {"type":"mcp_approval_response","approval_request_id":"NEVER","approve":false,"reason":"READ"},
  {"type":"web_search_call","id":"NEVER","status":"NEVER","action":{"type":"search","query":"READ","queries":["READ"],"sources":[{"type":"url","url":"READ"},{"type":"api","name":"NEVER"}]}},
  {"type":"web_search_call","id":"NEVER","action":{"type":"find_in_page","url":"READ","pattern":"READ"}},
  {"type":"file_search_call","id":"NEVER","queries":["READ"],"results":[{"file_id":"NEVER","filename":"READ","text":"READ","attributes":{"k":"NEVER"}}]},
  {"type":"code_interpreter_call","id":"NEVER","container_id":"NEVER","code":"READ","outputs":[{"type":"logs","logs":"READ"},{"type":"image","url":"NEVER"}]},
  {"type":"local_shell_call","id":"NEVER","call_id":"NEVER","action":{"type":"exec","command":["READ"],"env":{"K":"READ"},"working_directory":"READ","user":"NEVER"}},
  {"type":"local_shell_call_output","id":"NEVER","output":"READ"},
  {"type":"shell_call","call_id":"NEVER","action":{"commands":["READ"],"timeout_ms":1}},
  {"type":"shell_call_output","call_id":"NEVER","output":[{"stdout":"READ","stderr":"READ","outcome":{"type":"exit"}}]},
  {"type":"apply_patch_call","call_id":"NEVER","operation":{"type":"update_file","path":"READ","diff":"READ"}},
  {"type":"apply_patch_call_output","call_id":"NEVER","output":"READ"},
  {"type":"computer_call","call_id":"NEVER","action":{"type":"type","text":"READ"},"pending_safety_checks":[{"id":"NEVER","code":"NEVER","message":"NEVER"}]},
  {"type":"computer_call_output","call_id":"NEVER","output":{"type":"computer_screenshot","image_url":"NEVER","file_id":"NEVER"}},
  {"type":"image_generation_call","id":"NEVER","result":"NEVER","revised_prompt":"READ"},
  {"type":"additional_tools","role":"developer","tools":[{"type":"function","name":"NEVER","description":"READ"}]}
 ],
 "tools":[
  {"type":"function","name":"NEVER","description":"DECL","parameters":{"type":"object","properties":{"q":{"type":"string","description":"DECL"}}}},
  {"type":"custom","name":"NEVER","description":"DECL","format":{"type":"grammar","definition":"NEVER"}},
  {"type":"namespace","name":"NEVER","description":"DECL","tools":[{"type":"function","name":"NEVER","description":"DECL"}]},
  {"type":"mcp","server_label":"NEVER","server_url":"NEVER","server_description":"DECL","connector_id":"NEVER","authorization":"NEVER",
   "allowed_tools":["NEVER"],"require_approval":{"never":{"tool_names":["NEVER"]}}},
  {"type":"file_search","vector_store_ids":["NEVER"]}
 ]}`},
		{"gemini", contract.ProtocolGoogleGenerateContent, `{"cachedContent":"NEVER",
 "systemInstruction":{"role":"NEVER","parts":[{"text":"READ"}]},
 "system_instruction":{"parts":[{"text":"READ"}]},
 "contents":[
  {"role":"user","parts":[{"text":"READ"},{"inlineData":{"mimeType":"image/png","data":"NEVER"}},{"fileData":{"mimeType":"application/pdf","fileUri":"NEVER"}},
   {"futurePart":{"text":"NEVER"}},
   {"functionResponse":{"id":"NEVER","name":"NEVER","response":{"name":"READ","output":"READ"}}},
   {"function_response":{"name":"NEVER","response":{"result":"READ"}}}]},
  {"role":"model","parts":[{"text":"READ","thought":true,"thoughtSignature":"NEVER"},
   {"functionCall":{"id":"NEVER","name":"NEVER","args":{"id":"READ"}},"thoughtSignature":"NEVER"},
   {"function_call":{"name":"NEVER","args":{"q":"READ"}},"thought_signature":"NEVER"},
   {"executableCode":{"language":"NEVER","code":"READ"}},{"codeExecutionResult":{"outcome":"NEVER","output":"READ"}},
   {"videoMetadata":{"startOffset":"NEVER"}}]}
 ],
 "tools":[{"functionDeclarations":[{"name":"NEVER","description":"DECL",
   "parameters":{"type":"NEVER","properties":{"q":{"type":"NEVER","description":"DECL","enum":["NEVER"]}},"required":["NEVER"]},
   "response":{"type":"NEVER","description":"DECL"}}]},{"googleSearch":{}}],
 "generationConfig":{"responseSchema":{"description":"NEVER"}}}`},
		{"completions", contract.ProtocolOpenAICompletions, `{"model":"NEVER","prompt":["READ","READ"],"suffix":"READ","user":"NEVER","stop":["NEVER"]}`},
		{"embeddings", contract.ProtocolOpenAIEmbeddings, `{"model":"NEVER","input":["READ"],"user":"NEVER","encoding_format":"NEVER"}`},
		{"google embeddings", contract.ProtocolGoogleEmbeddings, `{"model":"NEVER","title":"READ","taskType":"NEVER",
 "content":{"role":"NEVER","parts":[{"text":"READ"},{"inlineData":{"mimeType":"image/png","data":"NEVER"}}]},
 "requests":[{"model":"NEVER","title":"READ","taskType":"NEVER","content":{"parts":[{"text":"READ"}]}}]}`},
		{"rerank", contract.ProtocolRerank, `{"model":"NEVER","query":"READ","documents":["READ",{"text":"READ","title":"READ"}],"return_documents":true}`},
		{"images", contract.ProtocolOpenAIImages, `{"model":"NEVER","prompt":"READ","size":"NEVER","user":"NEVER","image":"NEVER"}`},
		{"search", contract.ProtocolOpenAISearch, `{"id":"NEVER","model":"NEVER",
 "commands":{"search_query":[{"q":"READ","domains":["READ"]}],"find":[{"ref_id":"READ","pattern":"READ"}]},
 "settings":{"filters":{"allowed_domains":["NEVER"]}},
 "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"READ"}]},{"type":"reasoning","encrypted_content":"NEVER"}]}`},
	}
}

// markedPaths lists the JSON pointers of the strings in body equal to one of
// the markers.
func markedPaths(t *testing.T, body string, markers ...string) []string {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatal(err)
	}
	var paths []string
	var walk func(any, string)
	walk = func(value any, path string) {
		switch typed := value.(type) {
		case string:
			if slices.Contains(markers, typed) {
				paths = append(paths, path)
			}
		case []any:
			for index, item := range typed {
				walk(item, path+"/"+jsonIndex(index))
			}
		case map[string]any:
			for key, item := range typed {
				walk(item, path+"/"+escapeJSONPointer(key))
			}
		}
	}
	walk(value, "")
	slices.Sort(paths)
	return paths
}

// Every content position of each protocol is extracted, and nothing else:
// no identifier, unknown field or unknown block or item type, whatever the
// detector would think of it.
func TestExtractionIsAWhitelist(t *testing.T) {
	for _, fixture := range whitelistFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			for _, options := range []InspectionOptions{{}, {InspectToolDeclarations: true}} {
				markers := []string{"READ"}
				if options.InspectToolDeclarations {
					markers = append(markers, "DECL")
				}
				want := markedPaths(t, fixture.body, markers...)
				if len(want) == 0 {
					t.Fatal("fixture marks no content")
				}
				got := slices.Sorted(slices.Values(extractedPaths(t, fixture.protocol, fixture.body, options)))
				if !slices.Equal(got, want) {
					for _, path := range got {
						if !slices.Contains(want, path) {
							t.Errorf("declarations=%t: extracted a non-content string at %s", options.InspectToolDeclarations, path)
						}
					}
					for _, path := range want {
						if !slices.Contains(got, path) {
							t.Errorf("declarations=%t: content at %s was not extracted", options.InspectToolDeclarations, path)
						}
					}
				}
			}
		})
	}
}

// Redacting every extracted string leaves every identifier byte for byte and
// the request valid.
func TestWhitelistRedactionKeepsIdentifiers(t *testing.T) {
	for _, fixture := range whitelistFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			var seen []Segment
			policy := Policy{Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID,
				Action: ActionRedact, InspectToolDeclarations: true}
			detector := flaggingDetector([]string{"READ", "DECL", "NEVER"}, &seen)
			result, err := newTestEngine(t, detector).Inspect(t.Context(), policy, fixture.protocol, []byte(fixture.body))
			if err != nil || result.Decision != DecisionRedact {
				t.Fatalf("decision=%s err=%v", result.Decision, err)
			}
			for _, segment := range seen {
				if segment.Value == "NEVER" {
					t.Fatalf("identifier reached the detector at %s", segment.Path)
				}
			}
			if got, want := markedPaths(t, string(result.Body), "NEVER"), markedPaths(t, fixture.body, "NEVER"); !slices.Equal(got, want) {
				t.Fatalf("identifiers changed: got %v want %v", got, want)
			}
			if remaining := markedPaths(t, string(result.Body), "READ", "DECL"); len(remaining) != 0 {
				t.Fatalf("content escaped redaction at %v", remaining)
			}
		})
	}
}
