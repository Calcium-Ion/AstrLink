package cloud

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

// TestJevSingleDimensionRequestIsUnchanged is the wire-compatibility guard on
// the Jev side: with no dimensions declared the request is the one-question
// map the single-dimension path always sent.
func TestJevSingleDimensionRequestIsUnchanged(t *testing.T) {
	_, body, err := buildJevRequest(Input{Model: "jev-latest", Prompt: "classify", Labels: []string{"coding", "research"}, Text: "state"})
	if err != nil {
		t.Fatal(err)
	}
	var request jevRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Questions) != 1 {
		t.Fatalf("questions = %v, want exactly task_type", request.Questions)
	}
	if _, ok := request.Questions["task_type"]; !ok {
		t.Errorf("task_type question missing from %v", request.Questions)
	}
}

// TestJevMultiDimensionRequestAsksEveryDimension: Jev keys questions by name,
// so each dimension becomes its own key with its own criteria map.
func TestJevMultiDimensionRequestAsksEveryDimension(t *testing.T) {
	_, body, err := buildJevRequest(Input{
		Model: "jev-latest", Prompt: "classify", Labels: []string{"coding", "research"},
		Text: "the user pasted this sentence", Dimensions: dimensionFixtures,
	})
	if err != nil {
		t.Fatal(err)
	}
	var request jevRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Questions) != 3 {
		t.Fatalf("questions = %v, want task_type plus 2 dimensions", request.Questions)
	}
	sentiment, ok := request.Questions["sentiment"]
	if !ok {
		t.Fatalf("sentiment question missing from %v", request.Questions)
	}
	if len(sentiment.Criteria) != 2 {
		t.Errorf("sentiment criteria = %v, want its own 2 options", sentiment.Criteria)
	}
	if sentiment.Criteria["positive"] != "Content or pleased" {
		t.Errorf("option description was not carried: %v", sentiment.Criteria)
	}
	// The instruction must mention the dimension so the model knows what it is
	// deciding, and must not splice the user text in.
	if sentiment.Instructions == "" || !strings.Contains(sentiment.Instructions, "sentiment") {
		t.Errorf("sentiment instructions do not name the dimension: %q", sentiment.Instructions)
	}
	if strings.Contains(sentiment.Instructions, "the user pasted this sentence") {
		t.Errorf("the user text must travel as state, not inside the instruction: %q", sentiment.Instructions)
	}
	taskType := request.Questions["task_type"]
	if len(taskType.Criteria) != 2 {
		t.Errorf("task_type criteria = %v, want the label set", taskType.Criteria)
	}
}

// TestJevParsesDimensionAnswers: answers come back keyed by dimension name, so
// 回收 needs no ordering contract on this provider.
func TestJevParsesDimensionAnswers(t *testing.T) {
	in := Input{Labels: []string{"coding", "research"}, Dimensions: dimensionFixtures}
	payload := []byte(`{"answers":{
		"task_type":{"type":"choice","choice":"coding","confidence":0.9},
		"sentiment":{"type":"choice","choice":"negative","confidence":0.8},
		"domain":{"type":"choice","choice":"backend","confidence":0.7}}}`)
	answers, err := parseJevDimensions(payload, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 2 {
		t.Fatalf("answers = %v, want the 2 custom dimensions only", answers)
	}
	if _, ok := answers["task_type"]; ok {
		t.Error("task_type must never appear in the dimensions map")
	}
	if answers["sentiment"].Label != "negative" || answers["sentiment"].Score != 0.8 {
		t.Errorf("sentiment answer = %+v, want negative/0.8", answers["sentiment"])
	}
}

// TestJevUnansweredDimensionIsNotAnError: an omission is the "not produced"
// case, which the caller turns into an unknown dimension — never a failure.
func TestJevUnansweredDimensionIsNotAnError(t *testing.T) {
	in := Input{Labels: []string{"coding"}, Dimensions: dimensionFixtures}
	payload := []byte(`{"answers":{"task_type":{"type":"choice","choice":"coding","confidence":0.9}}}`)
	answers, err := parseJevDimensions(payload, in)
	if err != nil {
		t.Fatalf("an unanswered dimension must not fail the reply: %v", err)
	}
	if len(answers) != 0 {
		t.Errorf("answers = %v, want none", answers)
	}
}

// TestJevDimensionChoiceOutsideOwnOptionsIsInvalidOutput keeps the
// whole-document rule: a label valid elsewhere is not valid here.
func TestJevDimensionChoiceOutsideOwnOptionsIsInvalidOutput(t *testing.T) {
	in := Input{Labels: []string{"coding"}, Dimensions: dimensionFixtures}
	payload := []byte(`{"answers":{"sentiment":{"type":"choice","choice":"coding","confidence":0.9}}}`)
	if _, err := parseJevDimensions(payload, in); err == nil {
		t.Fatal("a choice outside the dimension's options must be rejected")
	}
}

// TestOpenAISingleDimensionRequestIsUnchanged: same guard as the Jev one, on
// the provider whose answers are order-aligned.
func TestOpenAISingleDimensionRequestIsUnchanged(t *testing.T) {
	_, body, err := buildOpenAIDecisionsRequest(Input{Model: "gpt-6-luna", Prompt: "classify", Labels: []string{"coding", "research"}, Text: "state"})
	if err != nil {
		t.Fatal(err)
	}
	var request openAIDecisionsRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Questions) != 1 || request.Questions[0].Name != "task_type" {
		t.Fatalf("questions = %+v, want exactly task_type", request.Questions)
	}
}

// TestOpenAIMultiDimensionRequestAsksEveryDimension keeps task_type first, so
// the documented order and the index-alignment fallback agree.
func TestOpenAIMultiDimensionRequestAsksEveryDimension(t *testing.T) {
	_, body, err := buildOpenAIDecisionsRequest(Input{
		Model: "gpt-6-luna", Prompt: "classify", Labels: []string{"coding"},
		Text: "state", Dimensions: dimensionFixtures,
	})
	if err != nil {
		t.Fatal(err)
	}
	var request openAIDecisionsRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	want := []string{"task_type", "sentiment", "domain"}
	if len(request.Questions) != len(want) {
		t.Fatalf("questions = %+v, want %v", request.Questions, want)
	}
	for index, name := range want {
		if request.Questions[index].Name != name {
			t.Errorf("question %d = %q, want %q", index, request.Questions[index].Name, name)
		}
	}
	if len(request.Questions[1].Choices) != 2 {
		t.Errorf("sentiment choices = %+v, want its own 2 options", request.Questions[1].Choices)
	}
	if request.Questions[1].Choices[0].Description != "Content or pleased" {
		t.Errorf("option description was not carried: %+v", request.Questions[1].Choices[0])
	}
}

// TestOpenAINamedAnswersResolveByStrictName is the multi-question contract:
// each question's answer is located by the name it carries, even when the
// provider returns them out of order.
func TestOpenAINamedAnswersResolveByStrictName(t *testing.T) {
	payload := []byte(`{"answers":[
		{"type":"choice","name":"domain","choice":"backend","confidence":0.7},
		{"type":"choice","name":"task_type","choice":"coding","confidence":0.9},
		{"type":"choice","name":"sentiment","choice":"negative","confidence":0.8}]}`)

	category, confidence, _, err := parseOpenAIDecisionsResponse(payload, []string{"coding", "research"})
	if err != nil {
		t.Fatal(err)
	}
	if category != "coding" || confidence != 0.9 {
		t.Fatalf("task_type = %q/%v, want coding/0.9", category, confidence)
	}

	in := Input{Labels: []string{"coding", "research"}, Dimensions: dimensionFixtures}
	answers, err := parseOpenAIDecisionsDimensions(payload, in)
	if err != nil {
		t.Fatal(err)
	}
	if answers["sentiment"].Label != "negative" || answers["domain"].Label != "backend" {
		t.Fatalf("answers = %+v, want sentiment=negative and domain=backend", answers)
	}
}

// TestOpenAIUnnamedAnswerIsNeverMistakenForTaskType is the regression the
// multi-question work exposed: the provider's name field is optional, and the
// previous matcher treated a nameless answer as task_type. With several
// questions in flight that would silently route on the wrong answer.
func TestOpenAIUnnamedAnswerIsNeverMistakenForTaskType(t *testing.T) {
	// The reply names its answers but omits task_type: task_type went
	// unanswered, so it must not inherit some other question's answer.
	payload := []byte(`{"answers":[
		{"type":"choice","name":"sentiment","choice":"negative","confidence":0.9},
		{"type":"choice","name":"domain","choice":"backend","confidence":0.8}]}`)
	if _, _, _, err := parseOpenAIDecisionsResponse(payload, []string{"coding", "research"}); err == nil {
		t.Fatal("a reply that never answered task_type must not be read as answering it")
	}

	// A wholly nameless reply is the one case where the documented ordering
	// applies, and task_type is asked first so position 0 is its answer.
	unnamed := []byte(`{"answers":[
		{"type":"choice","choice":"coding","confidence":0.9},
		{"type":"choice","choice":"negative","confidence":0.8}]}`)
	category, _, _, err := parseOpenAIDecisionsResponse(unnamed, []string{"coding", "research"})
	if err != nil {
		t.Fatalf("a fully unnamed reply falls back to index alignment: %v", err)
	}
	if category != "coding" {
		t.Fatalf("task_type = %q, want coding from index 0", category)
	}
}

// TestOpenAIRefusedDimensionLeavesOthersUsable: the provider's documented
// behaviour is that one question's refusal does not affect the others, so a
// refused dimension is simply unknown.
func TestOpenAIRefusedDimensionLeavesOthersUsable(t *testing.T) {
	payload := []byte(`{"answers":[
		{"type":"choice","name":"task_type","choice":"coding","confidence":0.9},
		{"type":"refusal","name":"sentiment"},
		{"type":"choice","name":"domain","choice":"backend","confidence":0.8}]}`)
	in := Input{Labels: []string{"coding", "research"}, Dimensions: dimensionFixtures}
	answers, err := parseOpenAIDecisionsDimensions(payload, in)
	if err != nil {
		t.Fatalf("a refused dimension must not fail the reply: %v", err)
	}
	if _, ok := answers["sentiment"]; ok {
		t.Errorf("a refused dimension must be absent, got %+v", answers["sentiment"])
	}
	if answers["domain"].Label != "backend" {
		t.Errorf("the other dimensions must survive a refusal: %+v", answers)
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
