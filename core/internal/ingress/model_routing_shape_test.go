package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func shapeOf(t *testing.T, protocol contract.ProtocolID, body string) requestShape {
	t.Helper()
	if !json.Valid([]byte(body)) {
		t.Fatal("fixture is not JSON")
	}
	return inspectRequestShape(protocol, []byte(body))
}

type shapeCounts struct {
	toolDeclarations, toolCalls, toolMessages, assistantMessages, mediaParts int
	mediaKind                                                                string
}

func countsOf(shape requestShape) shapeCounts {
	return shapeCounts{shape.toolDeclarations, shape.toolCalls, shape.toolMessages, shape.assistantMessages, shape.mediaParts, shape.mediaKind()}
}

// One conversation per protocol: a user turn with media, an assistant turn
// that calls tools, and the tool results.
func TestInspectRequestShapeReadsEveryProtocol(t *testing.T) {
	tests := []struct {
		name     string
		protocol contract.ProtocolID
		body     string
		want     shapeCounts
	}{
		{
			name:     "chat",
			protocol: contract.ProtocolOpenAIChat,
			body: `{"model":"astrlink/x","tools":[{"type":"function","function":{"name":"read"}},{"type":"function","function":{"name":"write"}}],"messages":[
				{"role":"system","content":"be brief"},
				{"role":"user","content":[{"type":"text","text":"what is this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},
				{"role":"assistant","content":null,"tool_calls":[{"id":"a","type":"function","function":{"name":"read","arguments":"{}"}},{"id":"b","type":"function","function":{"name":"read","arguments":"{}"}}]},
				{"role":"tool","tool_call_id":"a","content":"one"},
				{"role":"tool","tool_call_id":"b","content":"two"},
				{"role":"assistant","content":"done"},
				{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"AAAA","format":"wav"}}]}]}`,
			want: shapeCounts{toolDeclarations: 2, toolCalls: 2, toolMessages: 2, assistantMessages: 2, mediaParts: 2, mediaKind: "mixed"},
		},
		{
			name:     "chat legacy functions",
			protocol: contract.ProtocolOpenAIChat,
			body:     `{"model":"astrlink/x","functions":[{"name":"read"}],"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":null,"function_call":{"name":"read","arguments":"{}"}},{"role":"function","name":"read","content":"ok"}]}`,
			want:     shapeCounts{toolDeclarations: 1, toolCalls: 1, toolMessages: 1, assistantMessages: 1, mediaKind: "none"},
		},
		{
			name:     "responses",
			protocol: contract.ProtocolOpenAIResponses,
			body: `{"model":"astrlink/x","tools":[{"type":"function","name":"shell"},{"type":"web_search"},{"type":"custom","name":"apply_patch"}],"input":[
				{"role":"user","content":[{"type":"input_text","text":"fix it"},{"type":"input_image","image_url":"https://example.test/a.png"},{"type":"input_file","file_id":"file_1"}]},
				{"type":"reasoning","summary":[]},
				{"type":"function_call","call_id":"c1","name":"shell","arguments":"{}"},
				{"type":"function_call_output","call_id":"c1","output":"ok"},
				{"type":"custom_tool_call","call_id":"c2","name":"apply_patch","input":"*** Begin Patch"},
				{"type":"custom_tool_call_output","call_id":"c2","output":"applied"},
				{"type":"web_search_call","id":"ws1","status":"completed"},
				{"type":"computer_call_output","call_id":"c3","output":{"type":"computer_screenshot","image_url":"data:image/png;base64,AAAA"}},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"patched"}]}]}`,
			// Four model turns: reasoning with the shell call, the patch, the web
			// search, and the closing message; each tool result ends one.
			want: shapeCounts{toolDeclarations: 3, toolCalls: 3, toolMessages: 3, assistantMessages: 4, mediaParts: 2, mediaKind: "image"},
		},
		{
			name:     "responses string input",
			protocol: contract.ProtocolOpenAIResponsesCompact,
			body:     `{"model":"astrlink/x","input":"hello"}`,
			want:     shapeCounts{mediaKind: "none"},
		},
		{
			name:     "anthropic",
			protocol: contract.ProtocolAnthropicMessages,
			body: `{"model":"astrlink/x","system":"be brief","tools":[{"name":"read","input_schema":{"type":"object"}},{"type":"web_search_20250305","name":"web_search"}],"messages":[
				{"role":"user","content":[{"type":"text","text":"look"},{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AAAA"}}]},
				{"role":"assistant","content":[{"type":"thinking","thinking":"hm","signature":"sig"},{"type":"tool_use","id":"t1","name":"read","input":{}},{"type":"tool_use","id":"t2","name":"read","input":{}},{"type":"server_tool_use","id":"s1","name":"web_search","input":{}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"text"},{"type":"tool_result","tool_use_id":"t2","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}]}`,
			want: shapeCounts{toolDeclarations: 2, toolCalls: 3, toolMessages: 2, assistantMessages: 1, mediaParts: 1, mediaKind: "image"},
		},
	}
	for _, test := range tests {
		shape := shapeOf(t, test.protocol, test.body)
		if !shape.known || shape.inputBytes != len(test.body) {
			t.Fatalf("%s: known=%v inputBytes=%d, want %d", test.name, shape.known, shape.inputBytes, len(test.body))
		}
		if got := countsOf(shape); got != test.want {
			t.Fatalf("%s: counts = %+v, want %+v", test.name, got, test.want)
		}
	}
}

// Facts read from the body stay unknown for a request whose shape was never
// read, rather than reading as zero tools and no media.
func TestModelRoutingFactsLeaveUnreadShapeUnknown(t *testing.T) {
	facts := modelRoutingFacts(Request{Protocol: contract.ProtocolOpenAIChat}, nil, time.Now())
	if facts.MediaKind != "" || facts.HasImage != nil || facts.HasTools != nil || facts.ToolCallCount != nil || facts.EstimatedInputContextTokens != nil {
		t.Fatalf("facts = %+v, want the body facts unknown", facts)
	}
	facts = modelRoutingFacts(Request{Protocol: contract.ProtocolOpenAIChat, shape: shapeOf(t, contract.ProtocolOpenAIChat, `{"messages":[{"role":"user","content":"hi"}]}`)}, nil, time.Now())
	if facts.MediaKind != "none" || facts.HasImage == nil || *facts.HasImage || facts.HasTools == nil || *facts.HasTools {
		t.Fatalf("facts = %+v, want known and empty", facts)
	}
}

// Escapes are read as the characters they stand for: an escaped Han
// character is one token, and a payload whose slashes are escaped is still a
// payload.
func TestEstimateTokensReadsEscapes(t *testing.T) {
	escapedChinese := strings.Repeat(`\u4f60\u597d`, 500)
	payload := strings.Repeat(`iVBORw0KGgo\/AAAA`, 100)
	shape := shapeOf(t, contract.ProtocolOpenAIChat, `{"messages":[{"role":"user","content":"`+escapedChinese+`"},{"role":"user","content":"`+payload+`"}]}`)
	if shape.estimatedTokens < 1000 || shape.estimatedTokens > 1020 {
		t.Fatalf("estimate = %d, want about 1000", shape.estimatedTokens)
	}
}

// Text counts by script, inlined payloads count nothing, and each media part
// adds its flat cost, so one screenshot cannot read as a long context.
func TestEstimateTokensSkipsPayloadsAndCountsMediaFlat(t *testing.T) {
	english := strings.Repeat("word ", 800)
	chinese := strings.Repeat("你好世界", 250)
	payload := strings.Repeat("iVBORw0KGgo", 100000)
	text := shapeOf(t, contract.ProtocolOpenAIChat, `{"messages":[{"role":"user","content":"`+english+`"},{"role":"user","content":"`+chinese+`"}]}`)
	if text.estimatedTokens < 2000 || text.estimatedTokens > 2050 {
		t.Fatalf("text estimate = %d, want about 1000 + 1000", text.estimatedTokens)
	}
	image := shapeOf(t, contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+payload+`"}},{"type":"image","source":{"type":"url","url":"data:image/png;base64,`+payload+`"}}]}]}`)
	if image.estimatedTokens < 2*estimatedTokensPerMediaPart || image.estimatedTokens > 2*estimatedTokensPerMediaPart+50 {
		t.Fatalf("image estimate = %d, want two flat media costs", image.estimatedTokens)
	}
	if image.inputBytes < 2*len(payload) {
		t.Fatalf("input bytes = %d, want the whole body", image.inputBytes)
	}
}

// The live path must hand these facts to the evaluator: rules on images,
// tools, context size and media kind route real requests.
func TestModelRoutingRequestShapeFactsRouteLiveRequests(t *testing.T) {
	longText := strings.Repeat("context ", 40000)
	group := contract.ModelRoutingGroup{
		ID: "shapes", Enabled: true,
		Members: []contract.ModelRoutingMember{
			{ID: "fast", ServiceID: "endpoint_test", Model: "fast-model"},
			{ID: "vision", ServiceID: "endpoint_test", Model: "vision-model"},
			{ID: "agent", ServiceID: "endpoint_test", Model: "agent-model"},
			{ID: "long", ServiceID: "endpoint_test", Model: "long-model"},
			{ID: "plain", ServiceID: "endpoint_test", Model: "plain-model"},
		},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{
			{ID: "image", Priority: 10, When: contract.ModelRoutingCondition{Field: "has_image", EqualsBool: boolRef(true)}, TargetMemberID: "vision"},
			{ID: "tools", Priority: 20, When: contract.ModelRoutingCondition{All: []contract.ModelRoutingCondition{
				{Field: "has_tools", EqualsBool: boolRef(true)},
				{Field: "tool_call_count", Gte: intRef(1)},
			}}, TargetMemberID: "agent"},
			{ID: "long", Priority: 30, When: contract.ModelRoutingCondition{Field: "estimated_input_context_tokens", Gte: intRef(64000)}, TargetMemberID: "long"},
			{ID: "plain", Priority: 40, When: contract.ModelRoutingCondition{Field: "media_kind", In: []string{"none"}}, TargetMemberID: "plain"},
		},
	}
	tests := []struct {
		name, path, body, rule string
	}{
		{name: "image", path: "/v1/chat/completions", rule: "image",
			body: `{"model":"astrlink/shapes","messages":[{"role":"user","content":[{"type":"text","text":"what is this"},{"type":"image_url","image_url":{"url":"https://example.test/cat.png"}}]}]}`},
		{name: "tools", path: "/v1/messages", rule: "tools",
			body: `{"model":"astrlink/shapes","max_tokens":64,"tools":[{"name":"read","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"read it"},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]}`},
		{name: "long", path: "/v1/responses", rule: "long",
			body: `{"model":"astrlink/shapes","input":"` + longText + `"}`},
		{name: "plain", path: "/v1/chat/completions", rule: "plain",
			body: `{"model":"astrlink/shapes","messages":[{"role":"user","content":"hello"}]}`},
	}
	for _, test := range tests {
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
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", test.name, response.Code, response.Body.String())
		}
		decision := store.root(t).ModelRouting
		if decision == nil || decision.RuleID != test.rule || decision.UsedDefault != (test.rule == "") {
			t.Fatalf("%s: decision = %+v, want rule %q", test.name, decision, test.rule)
		}
	}
}

func intRef(value int) *int { return &value }
