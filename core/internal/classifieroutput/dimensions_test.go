package classifieroutput

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

var dimensionFixtures = []contract.ClassifierDimension{
	{
		Name:        "sentiment",
		Description: "How the user feels about the task.",
		Options: []contract.ClassifierDimensionOption{
			{Name: "positive", Description: "Content or pleased"},
			{Name: "neutral", Description: "No strong feeling"},
			{Name: "negative", Description: "Frustrated or blocked"},
		},
	},
	{
		Name: "domain",
		Options: []contract.ClassifierDimensionOption{
			{Name: "frontend"},
			{Name: "backend"},
		},
	},
}

// TestDimensionKeysAlwaysLeadsWithTaskType pins the key order the multi
// -dimension envelope is built and read in. task_type is built in and always
// addressed, so index 0 is stable regardless of what the user declared.
func TestDimensionKeysAlwaysLeadsWithTaskType(t *testing.T) {
	got := DimensionKeys(dimensionFixtures)
	want := []string{"task_type", "sentiment", "domain"}
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("keys = %v, want %v", got, want)
		}
	}
}

// TestDimensionKeysSingleDimensionIsTaskTypeOnly is the P1 invariant: with no
// custom dimensions declared there is exactly one key, which is what keeps the
// single-dimension request and reply byte-identical to the pre-dimensions
// behaviour.
func TestDimensionKeysSingleDimensionIsTaskTypeOnly(t *testing.T) {
	got := DimensionKeys(nil)
	if len(got) != 1 || got[0] != "task_type" {
		t.Fatalf("keys = %v, want [task_type]", got)
	}
}

// TestDimensionKeysSkipsReservedName: a document that slipped past the save
// path with a dimension literally named task_type must not produce duplicate
// keys.
func TestDimensionKeysSkipsReservedName(t *testing.T) {
	got := DimensionKeys([]contract.ClassifierDimension{
		{Name: "task_type", Options: []contract.ClassifierDimensionOption{{Name: "x"}}},
		{Name: "domain", Options: []contract.ClassifierDimensionOption{{Name: "frontend"}}},
	})
	want := []string{"task_type", "domain"}
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("keys = %v, want %v", got, want)
		}
	}
}

// TestParseDimensionsReadsEveryAddressedDimension is the happy path: every
// declared dimension is present in the envelope and comes back with its own
// label and score.
func TestParseDimensionsReadsEveryAddressedDimension(t *testing.T) {
	reply := `{"task_type":{"label":"coding","score":0.9},` +
		`"sentiment":{"label":"negative","score":0.7},` +
		`"domain":{"label":"backend","score":0.8}}`
	got, err := ParseDimensions(reply, defaultLabels, dimensionFixtures)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("dimensions = %v, want 3 entries", got)
	}
	for name, wantLabel := range map[string]string{"task_type": "coding", "sentiment": "negative", "domain": "backend"} {
		dimension, ok := got[name]
		if !ok {
			t.Fatalf("dimension %q missing from %v", name, got)
		}
		if dimension.Label != wantLabel {
			t.Errorf("dimension %q label = %q, want %q", name, dimension.Label, wantLabel)
		}
	}
	if got["sentiment"].Score != 0.7 {
		t.Errorf("sentiment score = %v, want 0.7", got["sentiment"].Score)
	}
}

// TestParseDimensionsAllowsUnansweredDimension: a dimension the provider did
// not answer is absent from the result rather than an error. The caller turns
// that into an unknown dimension, and an unknown dimension never matches a
// rule — the classifier seam is fail-open.
func TestParseDimensionsAllowsUnansweredDimension(t *testing.T) {
	reply := `{"task_type":{"label":"coding","score":0.9},"domain":{"label":"backend","score":0.8}}`
	got, err := ParseDimensions(reply, defaultLabels, dimensionFixtures)
	if err != nil {
		t.Fatalf("an unanswered dimension must not fail the reply: %v", err)
	}
	if _, ok := got["sentiment"]; ok {
		t.Errorf("sentiment should be absent when unanswered, got %v", got["sentiment"])
	}
	if got["domain"].Label != "backend" {
		t.Errorf("the answered dimension must survive: %v", got)
	}
}

// TestParseDimensionsRejectsUnknownEnvelopeKey: the envelope's key set is
// closed. An extra key means the reply is not the document that was asked for.
func TestParseDimensionsRejectsUnknownEnvelopeKey(t *testing.T) {
	reply := `{"task_type":{"label":"coding","score":0.9},"unexpected":{"label":"x","score":0.5}}`
	if _, err := ParseDimensions(reply, defaultLabels, dimensionFixtures); err == nil {
		t.Fatal("an unknown envelope key must be rejected")
	}
}

// TestParseDimensionsRejectsLabelOutsideOwnOptionList is the per-dimension
// catalog check: a label is only valid in the dimension that declares it, so a
// task_type label offered as a sentiment value is invalid output.
func TestParseDimensionsRejectsLabelOutsideOwnOptionList(t *testing.T) {
	reply := `{"task_type":{"label":"coding","score":0.9},"sentiment":{"label":"coding","score":0.8}}`
	_, err := ParseDimensions(reply, defaultLabels, dimensionFixtures)
	if err == nil {
		t.Fatal("a label outside the dimension's own options must be rejected")
	}
	if !strings.Contains(err.Error(), "sentiment") {
		t.Errorf("the error must name the offending dimension, got %v", err)
	}
}

// TestParseDimensionsRejectsMalformedDimension keeps the whole-document
// contract: one malformed member makes the reply unusable rather than
// partially usable, which matches ParseTaskType's existing behaviour.
func TestParseDimensionsRejectsMalformedDimension(t *testing.T) {
	for name, reply := range map[string]string{
		"non-finite score":   `{"task_type":{"label":"coding","score":0.9},"domain":{"label":"backend","score":5}}`,
		"unknown inner key":  `{"task_type":{"label":"coding","score":0.9},"domain":{"label":"backend","score":0.5,"extra":1}}`,
		"score not a number": `{"task_type":{"label":"coding","score":0.9},"domain":{"label":"backend","score":"high"}}`,
	} {
		if _, err := ParseDimensions(reply, defaultLabels, dimensionFixtures); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
}

// TestParseDimensionsRejectsEmptyEnvelope: a reply that addresses nothing is
// not a usable answer.
func TestParseDimensionsRejectsEmptyEnvelope(t *testing.T) {
	if _, err := ParseDimensions(`{}`, defaultLabels, dimensionFixtures); err == nil {
		t.Fatal("an envelope with no dimension present must be rejected")
	}
}

// TestPlanSingleDimensionSchemaIsByteIdenticalToLegacy is the wire-compatibility
// guard: deploying dimension support must not change the request a
// single-dimension deployment already sends.
func TestPlanSingleDimensionSchemaIsByteIdenticalToLegacy(t *testing.T) {
	legacy, err := Plan(contract.ProtocolOpenAIResponses, nil, defaultLabels)
	if err != nil {
		t.Fatal(err)
	}
	withDimensions, err := PlanWithDimensions(contract.ProtocolOpenAIResponses, nil, defaultLabels, nil)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.SystemPrompt() != withDimensions.SystemPrompt() {
		t.Error("single-dimension prompt changed")
	}
	if legacy.SchemaName() != withDimensions.SchemaName() {
		t.Errorf("schema names differ: %q vs %q", legacy.SchemaName(), withDimensions.SchemaName())
	}
	if legacy.SchemaName() != "classifier_task_type" {
		t.Errorf("single-dimension schema name = %q, want classifier_task_type", legacy.SchemaName())
	}
}

// The existing-model instruction opens with the classifier prompt as written
// and describes, after it, each dimension the prompt does not write out —
// name, description, options and their descriptions — so none is answered
// without its meaning. It always asks for a score with every label.
func TestSystemPromptDescribesTheDimensionsThePromptLeavesOut(t *testing.T) {
	constraint, err := PlanWithDimensions(contract.ProtocolOpenAIResponses, nil, defaultLabels, dimensionFixtures)
	if err != nil {
		t.Fatal(err)
	}
	prompt := constraint.WithPrompt("Route requests for the support desk.", nil).SystemPrompt()
	if !strings.HasPrefix(prompt, "Route requests for the support desk.\n\nAlso answer these dimensions:\n") {
		t.Fatalf("instruction does not open with the prompt and the missing dimensions:\n%s", prompt)
	}
	for _, want := range []string{
		"- task_type: " + contract.BuiltInTaskTypeDescription + "\n  Options: general | research | coding | architect\n",
		"- sentiment: How the user feels about the task.\n  Options: positive (Content or pleased) | neutral (No strong feeling) | negative (Frustrated or blocked)\n",
		"- domain\n  Options: frontend | backend\n",
		"For every dimension, give the option you choose as the label and a score: your confidence from 0 to 1 that it is right.",
		"one key per dimension, each with a label and a score",
		"Return exactly one JSON object",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("instruction is missing %q:\n%s", want, prompt)
		}
	}

	// A prompt that writes out task_type and sentiment leaves only domain.
	written := constraint.WithPrompt("Classify.", []string{"task_type", "sentiment"}).SystemPrompt()
	if !strings.HasPrefix(written, "Classify.\n\nAlso answer these dimensions:\n- domain\n  Options: frontend | backend\n\nFor every dimension") {
		t.Fatalf("instruction for a prompt that writes out two dimensions:\n%s", written)
	}
	// One that writes out all three adds no description at all.
	all := constraint.WithPrompt("Classify.", []string{"task_type", "sentiment", "domain"}).SystemPrompt()
	if !strings.HasPrefix(all, "Classify.\n\nFor every dimension") || strings.Contains(all, "Also answer") {
		t.Fatalf("instruction for a prompt that writes out every dimension:\n%s", all)
	}

	var schema struct {
		Properties map[string]struct {
			Required []string `json:"required"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(promptSchemaOf(t, constraint)), &schema); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"task_type", "sentiment", "domain"} {
		if required := strings.Join(schema.Properties[name].Required, ","); required != "label,score" {
			t.Errorf("%s requires %q, want label and score", name, required)
		}
	}
}

// With task_type alone and no prompt, the instruction describes it and asks
// for its score.
func TestSystemPromptDescribesTaskTypeAlone(t *testing.T) {
	constraint, err := PlanWithDimensions(contract.ProtocolOpenAIResponses, nil, defaultLabels, nil)
	if err != nil {
		t.Fatal(err)
	}
	prompt := constraint.SystemPrompt()
	if !strings.HasPrefix(prompt, "Dimensions:\n- task_type: "+contract.BuiltInTaskTypeDescription+"\n") {
		t.Fatalf("instruction opens with %q", prompt[:min(len(prompt), 80)])
	}
	if !strings.Contains(prompt, "Give the option you choose as the label and a score") || !strings.Contains(prompt, "with a label and a score") {
		t.Fatalf("single-dimension instruction:\n%s", prompt)
	}
}

// TestPlanMultiDimensionSchemaNamesEveryDimension: the multi-dimension
// envelope's properties are exactly the addressed dimensions, so the prompt
// never asks for a value the parser would then reject as an unknown key.
func TestPlanMultiDimensionSchemaNamesEveryDimension(t *testing.T) {
	constraint, err := PlanWithDimensions(contract.ProtocolOpenAIResponses, nil, defaultLabels, dimensionFixtures)
	if err != nil {
		t.Fatal(err)
	}
	if constraint.SchemaName() != "classifier_dimensions" {
		t.Errorf("schema name = %q, want classifier_dimensions", constraint.SchemaName())
	}
	object, err := ParseObject(promptSchemaOf(t, constraint))
	if err != nil {
		t.Fatalf("prompt schema is not a JSON object: %v", err)
	}
	properties := object["properties"]
	var names map[string]json.RawMessage
	if err := json.Unmarshal(properties, &names); err != nil {
		t.Fatalf("properties is not an object: %v", err)
	}
	for _, want := range []string{"task_type", "sentiment", "domain"} {
		if _, ok := names[want]; !ok {
			t.Errorf("property %q missing from the multi-dimension schema", want)
		}
	}
	if len(names) != 3 {
		t.Errorf("properties = %v, want exactly the 3 addressed dimensions", names)
	}
}

// TestPlanMultiDimensionStrictSchemaIsSatisfiableLocally checks the same
// promise the single-dimension schema makes: a reply that honors the strict
// native constraint must pass local validation.
func TestPlanMultiDimensionStrictSchemaIsSatisfiableLocally(t *testing.T) {
	constraint, err := PlanWithDimensions(contract.ProtocolOpenAIChat,
		CapabilitySet{contract.ProtocolOpenAIChat: NativeVerified}, defaultLabels, dimensionFixtures)
	if err != nil {
		t.Fatal(err)
	}
	var responseFormat struct {
		JSONSchema struct {
			Name   string          `json:"name"`
			Strict bool            `json:"strict"`
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	}
	raw := constraint.ChatResponseFormat()
	if err := json.Unmarshal(raw, &responseFormat); err != nil {
		t.Fatalf("chat response_format is not JSON: %v", err)
	}
	if responseFormat.JSONSchema.Name != "classifier_dimensions" {
		t.Errorf("native schema name = %q, want classifier_dimensions", responseFormat.JSONSchema.Name)
	}
	var strict struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(responseFormat.JSONSchema.Schema, &strict); err != nil {
		t.Fatalf("strict schema is not JSON: %v", err)
	}
	for _, want := range []string{"task_type", "sentiment", "domain"} {
		found := false
		for _, name := range strict.Required {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("strict schema must require %q, got %v", want, strict.Required)
		}
	}
}

// promptSchemaOf extracts the schema JSON the prompt carries, which is the part
// after the "Schema:\n" marker.
func promptSchemaOf(t *testing.T, constraint OutputConstraint) string {
	t.Helper()
	prompt := constraint.SystemPrompt()
	_, schema, found := strings.Cut(prompt, "Schema:\n")
	if !found {
		t.Fatalf("prompt carries no schema section: %s", prompt)
	}
	return schema
}

// The ranked candidate sequence must survive the multi-dimension envelope.
// The evaluator's scored-fallback substitution reads task_type's sequence, so
// dropping it here silently disabled substitution for every group as soon as
// one custom dimension was configured. Ties must also break in declared option
// order, not map iteration order, or equal-scored candidates sort differently
// from run to run.
func TestParseDimensionsKeepsCandidatesInDeclaredOrder(t *testing.T) {
	labels := []string{"coding", "writing", "research"}
	text := `{"task_type":{"label":"coding","score":0.6,"candidates":[{"label":"research","score":0.4},{"label":"writing","score":0.4},{"label":"coding","score":0.6}]},` +
		`"sentiment":{"label":"neutral","score":0.9,"candidates":[{"label":"negative","score":0.1},{"label":"positive","score":0.1}]}}`
	for run := 0; run < 20; run++ {
		got, err := ParseDimensions(text, labels, dimensionFixtures)
		if err != nil {
			t.Fatal(err)
		}
		taskType := got["task_type"].Candidates
		if len(taskType) != 3 || taskType[0].Label != "coding" || taskType[1].Label != "writing" || taskType[2].Label != "research" {
			t.Fatalf("run %d task_type candidates = %+v", run, taskType)
		}
		sentiment := got["sentiment"].Candidates
		if len(sentiment) != 2 || sentiment[0].Label != "positive" || sentiment[1].Label != "negative" {
			t.Fatalf("run %d sentiment candidates = %+v", run, sentiment)
		}
	}
}
