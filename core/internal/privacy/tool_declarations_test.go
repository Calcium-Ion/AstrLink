package privacy

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func extractedPaths(t *testing.T, protocol contract.ProtocolID, body string, options InspectionOptions) []string {
	t.Helper()
	_, extracted, err := extractDocument(protocol, []byte(body), options)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(extracted))
	for index, segment := range extracted {
		paths[index] = segment.Path
	}
	return paths
}

func TestExtractDocumentToolDeclarationRoots(t *testing.T) {
	const schema = `{"type":"object","required":["q"],"properties":{"q":{"type":"string","format":"email","description":"query"}}}`
	for _, test := range []struct {
		protocol contract.ProtocolID
		body     string
		content  []string
		tools    []string
	}{
		{
			contract.ProtocolOpenAIResponses,
			`{"input":"hi","tools":[{"type":"function","name":"lookup","description":"docs@example.net","parameters":` + schema + `}]}`,
			[]string{"/input"},
			[]string{"/tools/0/description", "/tools/0/parameters/properties/q/description"},
		},
		{
			contract.ProtocolOpenAIResponsesCompact,
			`{"input":"hi","tools":[{"type":"function","name":"lookup","description":"docs@example.net","parameters":` + schema + `}]}`,
			[]string{"/input"},
			[]string{"/tools/0/description", "/tools/0/parameters/properties/q/description"},
		},
		{
			contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"user","content":"hi"}],` +
				`"tools":[{"type":"function","function":{"name":"lookup","description":"docs@example.net","parameters":` + schema + `}}],` +
				`"functions":[{"name":"legacy","description":"old@example.net","parameters":{}}]}`,
			[]string{"/messages/0/content"},
			[]string{
				"/tools/0/function/description", "/tools/0/function/parameters/properties/q/description",
				"/functions/0/description",
			},
		},
		{
			contract.ProtocolAnthropicMessages,
			`{"messages":[{"role":"user","content":"hi"}],` +
				`"tools":[{"name":"lookup","description":"docs@example.net","input_schema":` + schema + `}]}`,
			[]string{"/messages/0/content"},
			[]string{"/tools/0/description", "/tools/0/input_schema/properties/q/description"},
		},
		{
			contract.ProtocolGoogleGenerateContent,
			`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],` +
				`"tools":[{"functionDeclarations":[{"name":"lookup","description":"docs@example.net","parameters":` + schema + `}]}]}`,
			[]string{"/contents/0/parts/0/text"},
			[]string{
				"/tools/0/functionDeclarations/0/description",
				"/tools/0/functionDeclarations/0/parameters/properties/q/description",
			},
		},
		{
			contract.ProtocolOpenAICompletions,
			`{"prompt":"hi","tools":[{"type":"function","name":"lookup","description":"docs@example.net"}]}`,
			[]string{"/prompt"},
			nil,
		},
	} {
		t.Run(string(test.protocol), func(t *testing.T) {
			if got := extractedPaths(t, test.protocol, test.body, InspectionOptions{}); !reflect.DeepEqual(got, test.content) {
				t.Fatalf("skipped declarations: paths = %q, want %q", got, test.content)
			}
			want := append(slices.Clone(test.content), test.tools...)
			got := extractedPaths(t, test.protocol, test.body, InspectionOptions{InspectToolDeclarations: true})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("inspected declarations: paths = %q, want %q", got, want)
			}
		})
	}
}

const additionalToolsItem = `{"type":"additional_tools","role":"developer","id":"at_1","tools":[` +
	`{"type":"namespace","name":"mcp","description":"ns docs@example.net","tools":[` +
	`{"type":"function","name":"f","description":"fn docs@example.net"}]}]}`

func TestExtractDocumentSkipsAdditionalToolsForFilteredClient(t *testing.T) {
	message := `{"type":"message","role":"user","content":[{"type":"input_text","text":"alice@example.com"}]}`
	body := `{"model":"m","input":[` + additionalToolsItem + `,` + message + `]}`
	declarations := []string{
		"/input/0/tools/0/description", "/input/0/tools/0/name", "/input/0/tools/0/tools/0/description",
	}
	content := []string{"/input/1/content/0/text"}
	for _, protocol := range []contract.ProtocolID{
		contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact,
	} {
		skip := InspectionOptions{SkipAdditionalTools: true}
		if got := extractedPaths(t, protocol, body, skip); !reflect.DeepEqual(got, content) {
			t.Fatalf("%s skipped: paths = %q, want %q", protocol, got, content)
		}
		want := append(slices.Clone(declarations), content...)
		if got := extractedPaths(t, protocol, body, InspectionOptions{}); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s inspected: paths = %q, want %q", protocol, got, want)
		}
		// additional_tools is not a top-level declaration, so inspecting those
		// leaves the per-client skip in place.
		both := InspectionOptions{InspectToolDeclarations: true, SkipAdditionalTools: true}
		if got := extractedPaths(t, protocol, body, both); !reflect.DeepEqual(got, content) {
			t.Fatalf("%s independent skip: paths = %q, want %q", protocol, got, content)
		}
	}

	for name, item := range map[string]string{
		"user role":      strings.Replace(additionalToolsItem, `"developer"`, `"user"`, 1),
		"tools object":   `{"type":"additional_tools","role":"developer","tools":{"description":"docs@example.net"}}`,
		"other type":     strings.Replace(additionalToolsItem, `"additional_tools"`, `"message"`, 1),
		"content beside": `{"type":"additional_tools","role":"developer","tools":[],"content":"alice@example.com"}`,
	} {
		got := extractedPaths(t, contract.ProtocolOpenAIResponses, `{"input":[`+item+`]}`,
			InspectionOptions{SkipAdditionalTools: true})
		if len(got) == 0 {
			t.Fatalf("%s: nothing was inspected", name)
		}
	}
}

func TestRedactPreservesSkippedAdditionalToolsBytes(t *testing.T) {
	body := ` {"model":"m", "input":[ ` + additionalToolsItem + ` ,
 {"type":"message","role":"user","content":[{"type":"input_text","text":"alice@example.com"}]} ]} `
	policy := tokenPolicy()
	policy.SkipAdditionalTools = true
	result, err := mustTestEngine(t).Inspect(t.Context(), policy, contract.ProtocolOpenAIResponses, []byte(body))
	if err != nil || result.Decision != DecisionRedact || len(result.Redactions) != 1 ||
		result.Redactions[0].Value != "alice@example.com" {
		t.Fatalf("inspect: decision=%s redactions=%+v err=%v", result.Decision, result.Redactions, err)
	}
	assertOnlyRedactionsChanged(t, body, result)
	if !strings.Contains(string(result.Body), additionalToolsItem) {
		t.Fatal("the skipped additional_tools item changed")
	}

	policy.SkipAdditionalTools = false
	result, err = mustTestEngine(t).Inspect(t.Context(), policy, contract.ProtocolOpenAIResponses, []byte(body))
	if err != nil || len(result.Redactions) != 2 {
		t.Fatalf("inspected additional_tools: redactions=%+v err=%v", result.Redactions, err)
	}
}

func TestLocateFindingsUsesInspectionOptions(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hi"}],` +
		`"tools":[{"type":"function","function":{"name":"f","description":"docs@example.net","parameters":{}}}]}`)
	options := InspectionOptions{InspectToolDeclarations: true}
	findings := []Finding{{Segment: 1, Start: 0, End: len("docs@example.net"), Kind: KindEmail, Confidence: 1}}
	locations, err := LocateFindings(contract.ProtocolOpenAIChat, body, findings, options)
	if err != nil || len(locations) != 1 || locations[0].Path != "/tools/0/function/description" {
		t.Fatalf("locations=%+v err=%v", locations, err)
	}
	// Findings from an inspection that reached the tools do not fit an
	// extraction that skipped them.
	if _, err := LocateFindings(contract.ProtocolOpenAIChat, body, findings, InspectionOptions{}); !errors.Is(
		err, ErrDetectorUnavailable,
	) {
		t.Fatalf("mismatched options error = %v", err)
	}
}

func TestFromContractPolicyResolvesToolDeclarationSwitches(t *testing.T) {
	policy := contract.DefaultPrivacyPolicy()
	for _, test := range []struct {
		skipTools, inspectAdd bool
		want                  InspectionOptions
	}{
		{false, false, InspectionOptions{InspectToolDeclarations: true, SkipAdditionalTools: true}},
		{true, false, InspectionOptions{SkipAdditionalTools: true}},
		{false, true, InspectionOptions{InspectToolDeclarations: true}},
		{true, true, InspectionOptions{}},
	} {
		policy.SkipToolDeclarations, policy.InspectAdditionalTools = test.skipTools, test.inspectAdd
		provider, err := NewStorePolicyProvider(&fakePolicyStore{record: storage.PolicyRecord{Policy: policy}})
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := provider.RequestPolicy(context.Background(), Scope{})
		if err != nil {
			t.Fatal(err)
		}
		if got := resolved.InspectionOptions(); got != test.want {
			t.Errorf("skip=%t inspect additional=%t: options = %+v, want %+v",
				test.skipTools, test.inspectAdd, got, test.want)
		}
	}
}
