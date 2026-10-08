package privacy

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestAlphaSearchInspectsInputAndCommandText(t *testing.T) {
	const sensitive = "alice@example.com"
	for _, fixture := range []struct {
		name   string
		fields string
		paths  []string
	}{
		{"string input", `"input":"alice@example.com"`, []string{"/input"}},
		{"input items", `"input":[{"role":"user","content":[{"type":"input_text","text":"alice@example.com"}]}]`, []string{"/input/0/content/0/text"}},
		{"commands", `"commands":{
 "search_query":[{"q":"alice@example.com","recency":1,"domains":["example.org"]}],
 "image_query":[{"q":"alice@example.com"}],
 "find":[{"ref_id":"opaque@example.com","pattern":"alice@example.com"}],
 "weather":[{"location":"alice@example.com"}]
}`, []string{"/commands/search_query/0/q", "/commands/image_query/0/q", "/commands/find/0/pattern", "/commands/weather/0/location"}},
	} {
		for _, action := range []Action{ActionRedact, ActionBlock, ActionWarn} {
			t.Run(fixture.name+"/"+string(action), func(t *testing.T) {
				// Opaque metadata and unknown fields must keep their original bytes.
				body := ` {"id":"opaque@example.com","settings":{"locale":"en"},"future_field":{"value":1},` + fixture.fields + `} `
				original := []byte(body)
				policy := Policy{Enabled: true, Mode: ModeRegex, Action: action, PlaceholderNotice: true}
				result, err := newTestEngine(t, nil).Inspect(t.Context(), policy, contract.ProtocolOpenAIAlphaSearch, original)
				if err != nil || result.Decision != Decision(action) || len(result.Findings) != len(fixture.paths) {
					t.Fatalf("decision=%s findings=%d err=%v", result.Decision, len(result.Findings), err)
				}
				locations, err := LocateFindings(contract.ProtocolOpenAIAlphaSearch, original, result.Findings, policy.InspectionOptions())
				if err != nil {
					t.Fatal(err)
				}
				var paths []string
				for _, finding := range locations {
					paths = append(paths, finding.Path)
				}
				slices.Sort(paths)
				wantPaths := slices.Clone(fixture.paths)
				slices.Sort(wantPaths)
				if !slices.Equal(paths, wantPaths) {
					t.Fatalf("finding paths=%v want=%v", paths, wantPaths)
				}
				encoded, err := json.Marshal(locations)
				if err != nil || strings.Contains(string(encoded), sensitive) {
					t.Fatalf("dry-run findings leaked plaintext: %s err=%v", encoded, err)
				}
				if action != ActionWarn && !slices.Contains(result.Protected, sensitive) {
					t.Fatal("blocked/redacted text missing from preview protection")
				}
				if action == ActionRedact {
					if len(result.Redactions) != 1 || result.Redactions[0].Value != sensitive {
						t.Fatalf("unexpected redactions: %#v", result.Redactions)
					}
					want := strings.ReplaceAll(body, sensitive, result.Redactions[0].Placeholder)
					if string(result.Body) != want || result.NoticeInjected {
						t.Fatal("redaction changed unrelated bytes or injected instructions")
					}
				} else if result.Body != nil {
					t.Fatal("block/warn rewrote the request")
				}
				if string(original) != body {
					t.Fatal("inspection mutated the original request")
				}
			})
		}
	}
}

func TestAlphaSearchOpaqueReferencesDoNotTriggerPrivacy(t *testing.T) {
	// This synthetic value would be flagged wherever it reached the detector.
	const body = `{
 "id":"opaque@example.com",
 "input":[
  {"type":"reasoning","encrypted_content":"opaque@example.com"},
  {"type":"function_call_output","call_id":"opaque@example.com","output":[
   {"type":"input_file","file_id":"opaque@example.com"},
   {"type":"input_image","file_id":"opaque@example.com"},
   {"type":"encrypted_content","encrypted_content":"opaque@example.com"}
  ]},
  {"type":"custom_tool_call_output","call_id":"opaque@example.com","output":[{"type":"input_file","file_id":"opaque@example.com"}]},
  {"type":"computer_call_output","call_id":"opaque@example.com","output":{"type":"computer_screenshot","file_id":"opaque@example.com"}}
 ],
 "commands":{
  "open":[{"ref_id":"opaque@example.com"}],
  "click":[{"ref_id":"opaque@example.com","id":1}],
  "find":[{"ref_id":"opaque@example.com","pattern":"ordinary text"}],
  "screenshot":[{"ref_id":"opaque@example.com","pageno":1}]
 },
 "encrypted_output":"opaque@example.com",
 "results":[{"opaque":"opaque@example.com"}]
}`
	for _, action := range []Action{ActionRedact, ActionBlock, ActionWarn} {
		t.Run(string(action), func(t *testing.T) {
			var seen []Segment
			policy := Policy{Enabled: true, Mode: ModeLocalModel, LocalModelID: testLocalModelID, Action: action}
			result, err := newTestEngine(t, flaggingDetector([]string{"opaque@example.com"}, &seen)).Inspect(
				t.Context(), policy, contract.ProtocolOpenAIAlphaSearch, []byte(body),
			)
			if err != nil || result.Decision != DecisionAllow || result.Body != nil || len(result.Findings) != 0 {
				t.Fatalf("opaque data triggered privacy: decision=%s findings=%d err=%v", result.Decision, len(result.Findings), err)
			}
			for _, segment := range seen {
				if strings.Contains(segment.Value, "opaque@example.com") {
					t.Fatalf("opaque data reached detector at %s", segment.Path)
				}
			}
		})
	}
}

func TestAlphaSearchReferenceProtectionIsPositionSpecific(t *testing.T) {
	const body = `{"commands":{
 "find":[{"ref_id":"opaque@example.com","pattern":"alice@example.com","extra":{"ref_id":"alice@example.com"}}],
 "search_query":[{"q":"ordinary text","ref_id":"alice@example.com"}]
},"input":[
 {"type":"function_call","arguments":{"ref_id":"alice@example.com","encrypted_content":"alice@example.com"}},
 {"role":"user","type":"reasoning","encrypted_content":"alice@example.com"}
]}`
	policy := Policy{Enabled: true, Mode: ModeRegex, Action: ActionRedact}
	result, err := newTestEngine(t, nil).Inspect(t.Context(), policy, contract.ProtocolOpenAIAlphaSearch, []byte(body))
	if err != nil || result.Decision != DecisionRedact || len(result.Findings) != 6 || len(result.Redactions) != 1 {
		t.Fatalf("decision=%s findings=%d redactions=%d err=%v", result.Decision, len(result.Findings), len(result.Redactions), err)
	}
	if string(result.Body) != strings.ReplaceAll(body, "alice@example.com", result.Redactions[0].Placeholder) {
		t.Fatal("reference protection exempted ordinary content or changed an opaque ref_id")
	}
	_, segments, err := extractDocument(contract.ProtocolOpenAIAlphaSearch, []byte(body), InspectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, segment := range segments {
		if strings.HasPrefix(segment.Path, "/input/0/arguments/") && segment.ContextPrefix != toolFieldContextPrefix(segment.Path) {
			t.Fatalf("Responses tool field context was lost at %s", segment.Path)
		}
	}
}

func TestAlphaSearchDryRunSample(t *testing.T) {
	const sample = "alice@example.com"
	if !SupportsInspection(contract.ProtocolOpenAIAlphaSearch) {
		t.Fatal("Alpha Search is not inspectable")
	}
	body, err := WrapSampleText(contract.ProtocolOpenAIAlphaSearch, sample)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"commands":{"search_query":[{"q":"alice@example.com"}]}}` {
		t.Fatalf("unexpected sample shape: %s", body)
	}
	policy := Policy{Enabled: true, Mode: ModeRegex, Action: ActionRedact}
	result, err := newTestEngine(t, nil).Inspect(t.Context(), policy, contract.ProtocolOpenAIAlphaSearch, body)
	if err != nil || result.Decision != DecisionRedact {
		t.Fatalf("decision=%s err=%v", result.Decision, err)
	}
	locations, err := LocateFindings(contract.ProtocolOpenAIAlphaSearch, body, result.Findings, policy.InspectionOptions())
	if err != nil || len(locations) != 1 || locations[0].Path != "/commands/search_query/0/q" {
		t.Fatalf("locations=%#v err=%v", locations, err)
	}
}

func TestAlphaSearchRejectsUnsafeInput(t *testing.T) {
	for _, body := range []string{
		`{"commands":`,
		`{"commands":{"search_query":[{"q":"first","q":"second"}]}}`,
		`{"input":"first"} {"input":"second"}`,
	} {
		_, err := newTestEngine(t, nil).Inspect(t.Context(), Policy{
			Enabled: true, Mode: ModeRegex, Action: ActionBlock,
		}, contract.ProtocolOpenAIAlphaSearch, []byte(body))
		if !errors.Is(err, ErrUnsafeInput) {
			t.Fatalf("unsafe input accepted: %s err=%v", body, err)
		}
	}
}
