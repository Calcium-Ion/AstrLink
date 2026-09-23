package privacy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

type structuredTestSpan struct {
	path string
	text string
	kind Kind
}

// structuredSpanDetector reports each span at its first occurrence in the
// segment at path, the way a model reports tokenizer-aligned offsets.
func structuredSpanDetector(t *testing.T, spans ...structuredTestSpan) Detector {
	t.Helper()
	return DetectorFunc(func(_ context.Context, input DetectInput) ([]Finding, error) {
		findings := make([]Finding, 0, len(spans))
		for _, span := range spans {
			found := false
			for index, segment := range input.Segments {
				if segment.Path != span.path {
					continue
				}
				start := strings.Index(segment.Value, span.text)
				if start < 0 {
					t.Fatalf("span %q not in %s: %q", span.text, span.path, segment.Value)
				}
				findings = append(findings, Finding{
					Segment: index, Start: start, End: start + len(span.text),
					Kind: span.kind, Confidence: 0.99,
				})
				found = true
			}
			if !found {
				t.Fatalf("segment %s was not extracted", span.path)
			}
		}
		return findings, nil
	})
}

func inspectStructuredSpans(t *testing.T, action Action, body string, spans ...structuredTestSpan) Result {
	t.Helper()
	engine := newTestEngine(t, structuredSpanDetector(t, spans...))
	result, err := engine.Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID, Action: action,
	}, contract.ProtocolOpenAIResponses, []byte(body))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	return result
}

func redactStructuredSpans(t *testing.T, body string, spans ...structuredTestSpan) Result {
	t.Helper()
	result := inspectStructuredSpans(t, ActionRedact, body, spans...)
	if result.Decision != DecisionRedact {
		t.Fatalf("decision = %q", result.Decision)
	}
	return result
}

func outputText(t *testing.T, body []byte, item, block int) string {
	t.Helper()
	var document struct {
		Input []struct {
			Output []struct {
				Text string `json:"text"`
			} `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("outer JSON is invalid: %v: %s", err, body)
	}
	return document.Input[item].Output[block].Text
}

func redactedValues(result Result) map[string]Kind {
	values := make(map[string]Kind, len(result.Redactions))
	for _, redaction := range result.Redactions {
		values[redaction.Value] = redaction.Kind
	}
	return values
}

func placeholderFor(t *testing.T, result Result, value string) string {
	t.Helper()
	for _, redaction := range result.Redactions {
		if redaction.Value == value {
			return redaction.Placeholder
		}
	}
	t.Fatalf("no redaction for %q in %#v", value, result.Redactions)
	return ""
}

// A Codex exec result is a JSON envelope inside an input_text block. PII-Tracer
// labelled a JSON number as a date and let a URL span start at the `":"` before
// the path; replacing either verbatim broke the envelope, and because every
// later turn resends it, the whole thread failed with privacy_redaction_failed.
func TestStructuredToolOutputSpansAcrossJSONSyntaxAreAligned(t *testing.T) {
	const body = `{"model":"gpt","input":[
		{"type":"custom_tool_call","call_id":"call_1","name":"exec","input":"text(await tools.exec_command({cmd:\"pwd\"}));\n","status":"completed"},
		{"type":"custom_tool_call_output","call_id":"call_1","output":[
			{"type":"input_text","text":"Script completed\nOutput:\n"},
			{"type":"input_text","text":"{\"chunk_id\":\"b109dc\",\"wall_time_seconds\":0.000017625,\"exit_code\":1,\"output\":\"/Users/raymond/Desktop/codex-astrlink/AstrLink测试\\n\"}"}
		]}
	]}`
	const path = "/input/1/output/1/text"
	result := redactStructuredSpans(t, body,
		structuredTestSpan{path: path, text: "b109dc", kind: KindAccount},
		structuredTestSpan{path: path, text: "0.000017625", kind: KindDate},
		structuredTestSpan{path: path, text: `":"/Users/raymond/Desktop/codex-astrlink/AstrLink测试`, kind: KindURL},
	)

	text := outputText(t, result.Body, 1, 1)
	var envelope map[string]any
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("tool output envelope is no longer JSON: %v: %s", err, text)
	}
	if _, isString := envelope["wall_time_seconds"].(string); !isString {
		t.Fatalf("redacted number was not quoted: %s", text)
	}
	output, _ := envelope["output"].(string)
	if !strings.HasSuffix(output, "\n") || strings.Contains(output, "raymond") {
		t.Fatalf("output value = %q", output)
	}
	if envelope["exit_code"] != float64(1) {
		t.Fatalf("unflagged number changed: %s", text)
	}

	values := redactedValues(result)
	for value, kind := range map[string]Kind{
		"b109dc":      KindAccount,
		"0.000017625": KindDate,
		"/Users/raymond/Desktop/codex-astrlink/AstrLink测试": KindURL,
	} {
		if values[value] != kind {
			t.Fatalf("redaction for %q = %q, all = %#v", value, values[value], values)
		}
	}
	if len(values) != 3 {
		t.Fatalf("redactions = %#v", values)
	}
}

func TestStructuredToolArgumentSpansNeverSplitEscapes(t *testing.T) {
	// The inner arguments are {"note":"say \"alice\"\nbye","path":"C:\\Users\\bob\\x"}.
	const body = `{"input":[{"type":"function_call","name":"lookup","call_id":"c","arguments":"{\"note\":\"say \\\"alice\\\"\\nbye\",\"path\":\"C:\\\\Users\\\\bob\\\\x\"}"}]}`
	const path = "/input/0/arguments"
	result := redactStructuredSpans(t, body,
		// Starts after the backslash of \" and ends after the backslash of \n.
		structuredTestSpan{path: path, text: `"alice\"\`, kind: KindPerson},
		// Ends between the two bytes of \\.
		structuredTestSpan{path: path, text: `bob\`, kind: KindPerson},
	)
	var document struct {
		Input []struct {
			Arguments string `json:"arguments"`
		} `json:"input"`
	}
	if err := json.Unmarshal(result.Body, &document); err != nil {
		t.Fatal(err)
	}
	var arguments map[string]string
	if err := json.Unmarshal([]byte(document.Input[0].Arguments), &arguments); err != nil {
		t.Fatalf("arguments are no longer JSON: %v: %s", err, document.Input[0].Arguments)
	}
	if want := "say " + placeholderFor(t, result, `\"alice\"\n`) + "bye"; arguments["note"] != want {
		t.Fatalf("note = %q, want %q", arguments["note"], want)
	}
	if want := `C:\Users\` + placeholderFor(t, result, `bob\\`) + "x"; arguments["path"] != want {
		t.Fatalf("path = %q, want %q", arguments["path"], want)
	}
}

func TestStructuredToolArgumentKeysAreOnlyRedactedOnTheirOwn(t *testing.T) {
	const body = `{"input":[{"type":"function_call","name":"lookup","call_id":"c","arguments":"{\"email\":\"alice@example.com\",\"bob@example.com\":{\"role\":\"admin\"}}"}]}`
	const path = "/input/0/arguments"
	result := redactStructuredSpans(t, body,
		// A span that swallows the key keeps the key and redacts the value.
		structuredTestSpan{path: path, text: `email":"alice@example.com`, kind: KindEmail},
		// A span confined to a key is the private value itself.
		structuredTestSpan{path: path, text: `bob@example.com`, kind: KindEmail},
	)
	var document struct {
		Input []struct {
			Arguments string `json:"arguments"`
		} `json:"input"`
	}
	if err := json.Unmarshal(result.Body, &document); err != nil {
		t.Fatal(err)
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(document.Input[0].Arguments), &arguments); err != nil {
		t.Fatalf("arguments are no longer JSON: %v: %s", err, document.Input[0].Arguments)
	}
	if _, kept := arguments["email"]; !kept || len(arguments) != 2 {
		t.Fatalf("arguments = %#v", arguments)
	}
	values := redactedValues(result)
	if len(values) != 2 || values["alice@example.com"] != KindEmail || values["bob@example.com"] != KindEmail {
		t.Fatalf("redactions = %#v", values)
	}
}

func TestStructuredToolOutputSpanOnSyntaxAloneIsDiscarded(t *testing.T) {
	const body = `{"input":[{"type":"function_call_output","call_id":"c","output":"{\"ok\":true,\"items\":[]}"}]}`
	for _, action := range []Action{ActionRedact, ActionWarn, ActionBlock} {
		result := inspectStructuredSpans(t, action, body,
			// Quotes, a colon, a keyword and a comma: no literal contents.
			structuredTestSpan{path: "/input/0/output", text: `":true,"`, kind: KindCommonSecret},
		)
		if result.Decision != DecisionAllow || len(result.Findings) != 0 || result.Body != nil {
			t.Fatalf("%s: syntax-only span was acted on: %#v", action, result)
		}
	}
}

func TestStructuredToolOutputBareNumberIsQuoted(t *testing.T) {
	const body = `{"input":[{"type":"function_call_output","call_id":"c","output":"13800138000"}]}`
	result := redactStructuredSpans(t, body,
		structuredTestSpan{path: "/input/0/output", text: "13800138000", kind: KindPhone},
	)
	var document struct {
		Input []struct {
			Output string `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(result.Body, &document); err != nil {
		t.Fatal(err)
	}
	var output string
	if err := json.Unmarshal([]byte(document.Input[0].Output), &output); err != nil ||
		len(result.Redactions) != 1 || output != result.Redactions[0].Placeholder {
		t.Fatalf("output = %q, redactions = %#v, error = %v", document.Input[0].Output, result.Redactions, err)
	}
}

func TestScanStructuredJSONLiterals(t *testing.T) {
	const value = ` {"k\u00e9y": [-1.5e3, "a\"b", true, null], "x":""} `
	scanned := scanStructuredJSONLiterals(value)
	type literal struct {
		text   string
		number bool
		key    bool
	}
	got := make([]literal, 0, len(scanned.literals))
	for _, item := range scanned.literals {
		got = append(got, literal{text: value[item.start:item.end], number: item.number, key: item.key})
	}
	want := []literal{
		{text: `k\u00e9y`, key: true},
		{text: `-1.5e3`, number: true},
		{text: `a\"b`},
		{text: `x`, key: true},
		{text: ``},
	}
	if len(got) != len(want) {
		t.Fatalf("literals = %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("literal %d = %#v, want %#v", index, got[index], want[index])
		}
	}
	if len(scanned.escapes) != 2 ||
		value[scanned.escapes[0][0]:scanned.escapes[0][1]] != `\u00e9` ||
		value[scanned.escapes[1][0]:scanned.escapes[1][1]] != `\"` {
		t.Fatalf("escapes = %#v", scanned.escapes)
	}
}

func TestStructuredToolOutputAllowlistMatchesTheAlignedValue(t *testing.T) {
	const body = `{"input":[{"type":"function_call_output","call_id":"c","output":"{\"cwd\":\"/Users/raymond/project\",\"exit_code\":0}"}]}`
	engine := newTestEngine(t, structuredSpanDetector(t,
		structuredTestSpan{path: "/input/0/output", text: `":"/Users/raymond/project`, kind: KindURL},
	))
	result, err := engine.Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID, Action: ActionRedact,
		Allowlist: []contract.PolicyAllowlistRule{{
			Type: contract.PolicyAllowlistTypeLiteral, Value: "/Users/raymond/project",
		}},
	}, contract.ProtocolOpenAIResponses, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != DecisionAllow || len(result.SuppressedFindings) != 1 ||
		result.SuppressedFindings[0].Suppression != SuppressionAllowlisted {
		t.Fatalf("allowlisted path was not suppressed: %#v", result)
	}
}

func TestStructuredToolOutputSpanCannotFanOutPastTheDetectorLimit(t *testing.T) {
	numbers := make([]string, maxDetectorFindings+1)
	for index := range numbers {
		numbers[index] = "1"
	}
	encoded, err := json.Marshal("[" + strings.Join(numbers, ",") + "]")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"input":[{"type":"function_call_output","call_id":"c","output":` + string(encoded) + `}]}`
	engine := newTestEngine(t, DetectorFunc(func(_ context.Context, input DetectInput) ([]Finding, error) {
		return []Finding{{Segment: 0, Start: 0, End: len(input.Segments[0].Value), Kind: KindPhone, Confidence: 1}}, nil
	}))
	_, err = engine.Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID, Action: ActionRedact,
	}, contract.ProtocolOpenAIResponses, []byte(body))
	if !errors.Is(err, ErrDetectorLimit) {
		t.Fatalf("error = %v", err)
	}
}
