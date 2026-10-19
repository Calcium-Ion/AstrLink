package cloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
)

func testInput(serverURL string) Input {
	return Input{
		Protocol:  ProtocolJev,
		Endpoint:  serverURL,
		APIKey:    []byte("test-api-key"),
		Model:     "jev-latest",
		Prompt:    "Pick from: general, coding",
		Labels:    []string{"general", "coding"},
		Text:      "write a go test",
		TimeoutMS: 2000,
	}
}

// captureServer records the outbound request and answers with a fixed body.
func captureServer(t *testing.T, status int, body string, capture *http.Request, capturedBody *[]byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		data, _ := io.ReadAll(request.Body)
		if capturedBody != nil {
			*capturedBody = data
		}
		if capture != nil {
			*capture = *request
		}
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestClassifyJevRequestShapeAndResponse(t *testing.T) {
	var captured http.Request
	var body []byte
	server := captureServer(t, 200, `{"answers":{"task_type":{"choice":"coding","confidence":0.92}}}`, &captured, &body)
	outcome := Classify(context.Background(), server.Client(), testInput(server.URL))
	if !outcome.OK() {
		t.Fatalf("outcome = %+v, want success", outcome)
	}
	if outcome.Category != "coding" || outcome.Confidence != 0.92 {
		t.Fatalf("category/confidence = %q/%v", outcome.Category, outcome.Confidence)
	}
	if outcome.ClassifierID != "cloud:jev" {
		t.Fatalf("classifier id = %q", outcome.ClassifierID)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if request["state"] != "write a go test" || request["model"] != "jev-latest" {
		t.Fatalf("jev request envelope = %v", request)
	}
	questions, ok := request["questions"].(map[string]any)
	if !ok {
		t.Fatalf("jev questions must be a map: %v", request["questions"])
	}
	question, ok := questions["task_type"].(map[string]any)
	if !ok {
		t.Fatalf("jev task_type question missing: %v", questions)
	}
	criteria, ok := question["criteria"].(map[string]any)
	if !ok {
		t.Fatalf("jev criteria must be a map: %v", question["criteria"])
	}
	if _, exists := criteria["coding"]; !exists {
		t.Fatalf("jev criteria missing label key: %v", criteria)
	}
	if _, exists := criteria["not-a-label"]; exists {
		t.Fatalf("jev criteria carried an unknown label: %v", criteria)
	}
}

func TestClassifyJevChoiceOutsideLabelsIsInvalidOutput(t *testing.T) {
	server := captureServer(t, 200, `{"answers":{"task_type":{"choice":"surprise","confidence":0.9}}}`, nil, nil)
	outcome := Classify(context.Background(), server.Client(), testInput(server.URL))
	if outcome.OK() || outcome.FallbackReason != FallbackInvalidOutput {
		t.Fatalf("outcome = %+v, want invalid output fallback", outcome)
	}
}

func TestClassifyOpenAIDecisionsRequestShapeAndResponse(t *testing.T) {
	var captured http.Request
	var body []byte
	server := captureServer(t, 200, `{"answers":[{"type":"choice","name":"task_type","choice":"general","confidence":0.75}]}`, &captured, &body)
	input := testInput(server.URL)
	input.Protocol = ProtocolOpenAIDecisions
	outcome := Classify(context.Background(), server.Client(), input)
	if !outcome.OK() || outcome.Category != "general" || outcome.Confidence != 0.75 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.ClassifierID != "cloud:openai_decisions" {
		t.Fatalf("classifier id = %q", outcome.ClassifierID)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if request["input"] != "write a go test" || request["model"] != "jev-latest" {
		t.Fatalf("openai request envelope = %v", request)
	}
	questions, ok := request["questions"].([]any)
	if !ok || len(questions) != 1 {
		t.Fatalf("openai questions must be an array of one: %v", request["questions"])
	}
	question := questions[0].(map[string]any)
	if question["name"] != "task_type" || question["instructions"] != "Pick from: general, coding" {
		t.Fatalf("openai question = %v", question)
	}
	choices, ok := question["choices"].([]any)
	if !ok || len(choices) != 2 {
		t.Fatalf("openai choices must be an array: %v", question["choices"])
	}
	first := choices[0].(map[string]any)
	if first["value"] != "general" {
		t.Fatalf("openai choices order = %v", choices)
	}
}

func TestClassifyOpenAIDecisionsRefusalFailsOpen(t *testing.T) {
	server := captureServer(t, 200, `{"answers":[{"type":"refusal","name":"task_type"}]}`, nil, nil)
	input := testInput(server.URL)
	input.Protocol = ProtocolOpenAIDecisions
	outcome := Classify(context.Background(), server.Client(), input)
	if outcome.OK() || outcome.FallbackReason != FallbackRefusal {
		t.Fatalf("outcome = %+v, want refusal fallback", outcome)
	}
}

func TestClassifyCustomLabelsAreSentAndAccepted(t *testing.T) {
	var body []byte
	server := captureServer(t, 200, `{"answers":{"task_type":{"choice":"storyboard","confidence":0.8}}}`, nil, &body)
	input := testInput(server.URL)
	input.Labels = []string{"alpha", "beta", "storyboard"}
	outcome := Classify(context.Background(), server.Client(), input)
	if !outcome.OK() || outcome.Category != "storyboard" {
		t.Fatalf("outcome = %+v", outcome)
	}
	var request struct {
		Questions struct {
			TaskType struct {
				Criteria map[string]string `json:"criteria"`
			} `json:"task_type"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Questions.TaskType.Criteria) != 3 {
		t.Fatalf("custom labels missing from criteria: %v", request.Questions.TaskType.Criteria)
	}
}

func TestClassifyMissingOrInvalidConfidenceIsInvalidOutput(t *testing.T) {
	for name, body := range map[string]string{
		"missing":      `{"answers":{"task_type":{"choice":"coding"}}}`,
		"non-finite":   `{"answers":{"task_type":{"choice":"coding","confidence":"high"}}}`,
		"out-of-range": `{"answers":{"task_type":{"choice":"coding","confidence":1.5}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := captureServer(t, 200, body, nil, nil)
			outcome := Classify(context.Background(), server.Client(), testInput(server.URL))
			if outcome.OK() || outcome.FallbackReason != FallbackInvalidOutput {
				t.Fatalf("outcome = %+v, want invalid output", outcome)
			}
		})
	}
}

// The outbound header set is a fixed whitelist: the provider's authentication
// and JSON content negotiation, nothing else. No gateway identity may attach.
func TestClassifyOutboundHeaderWhitelist(t *testing.T) {
	var captured http.Request
	server := captureServer(t, 200, `{"answers":{"task_type":{"choice":"coding","confidence":0.9}}}`, &captured, nil)
	Classify(context.Background(), server.Client(), testInput(server.URL))
	if got := captured.Header.Get("Authorization"); got != "Bearer test-api-key" {
		t.Fatalf("authorization = %q", got)
	}
	if got := captured.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q", got)
	}
	if got := captured.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("accept = %q", got)
	}
	for name := range captured.Header {
		lower := strings.ToLower(name)
		switch lower {
		case "authorization", "content-type", "accept", "content-length", "host":
			continue
		}
		// Accept-Encoding comes from the test client's transport negotiation,
		// not from this package; the shared client disables compression.
		if lower == "accept-encoding" {
			continue
		}
		if strings.HasPrefix(lower, "x-astrlink") || lower == "via" || lower == "x-powered-by" ||
			lower == "originator" || lower == "user-agent" {
			t.Fatalf("unexpected outbound header %q: %v", name, captured.Header.Get(name))
		}
	}
	if agent := captured.Header.Get("User-Agent"); agent != "" {
		t.Fatalf("user-agent = %q, want explicitly empty", agent)
	}
}

// Every failure mode maps to an Outcome fallback: the classifier seam must
// never surface an error that could block the caller's request.
func TestClassifyFailureMatrixFailsOpen(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			time.Sleep(300 * time.Millisecond)
			writer.WriteHeader(200)
		}))
		defer server.Close()
		input := testInput(server.URL)
		input.TimeoutMS = 50
		outcome := Classify(context.Background(), server.Client(), input)
		if outcome.OK() || outcome.FallbackReason != autoclassifier.FallbackTimeout {
			t.Fatalf("outcome = %+v, want timeout fallback", outcome)
		}
	})
	for name, status := range map[string]int{"rate limited": 429, "overloaded": 529, "bad gateway": 502} {
		t.Run(name, func(t *testing.T) {
			server := captureServer(t, status, `{"error":"no"}`, nil, nil)
			outcome := Classify(context.Background(), server.Client(), testInput(server.URL))
			if outcome.OK() || outcome.FallbackReason != autoclassifier.FallbackBusy {
				t.Fatalf("outcome = %+v, want busy fallback", outcome)
			}
		})
	}
	t.Run("network error", func(t *testing.T) {
		input := testInput("http://127.0.0.1:1")
		outcome := Classify(context.Background(), nil, input)
		if outcome.OK() || outcome.FallbackReason != autoclassifier.FallbackUnavailable {
			t.Fatalf("outcome = %+v, want unavailable fallback", outcome)
		}
	})
	t.Run("malformed json", func(t *testing.T) {
		server := captureServer(t, 200, `not json at all`, nil, nil)
		outcome := Classify(context.Background(), server.Client(), testInput(server.URL))
		if outcome.OK() || outcome.FallbackReason != FallbackInvalidOutput {
			t.Fatalf("outcome = %+v, want invalid output fallback", outcome)
		}
	})
	t.Run("missing api key", func(t *testing.T) {
		server := captureServer(t, 200, `{}`, nil, nil)
		input := testInput(server.URL)
		input.APIKey = nil
		outcome := Classify(context.Background(), server.Client(), input)
		if outcome.OK() || outcome.FallbackReason != autoclassifier.FallbackUnavailable {
			t.Fatalf("outcome = %+v, want unavailable fallback", outcome)
		}
	})
}

// A provider may attach a ranked list next to its primary choice. The source
// honors the Outcome contract: survivors sort by score descending with request
// order breaking ties, out-of-catalog and invalid-score entries drop, and each
// label keeps only its best entry. The low-confidence threshold is NOT applied
// here — that filter belongs to the shared conversion in modelrouting.
func TestClassifyJevRankedCandidatesNormalizeAtSource(t *testing.T) {
	server := captureServer(t, 200,
		`{"answers":{"task_type":{"choice":"coding","confidence":0.95,"ranked":[`+
			`{"choice":"research","confidence":0.4},`+
			`{"choice":"coding","confidence":0.95},`+
			`{"choice":"surprise","confidence":0.9},`+
			`{"choice":"general","confidence":1.5},`+
			`{"choice":"general","confidence":0.64},`+
			`{"choice":"coding","confidence":0.7}]}}}`, nil, nil)
	outcome := Classify(context.Background(), server.Client(), testInput(server.URL))
	if !outcome.OK() || outcome.Category != "coding" || outcome.Confidence != 0.95 {
		t.Fatalf("outcome = %+v, want the primary choice preserved", outcome)
	}
	// coding keeps its best entry (0.95 over the duplicate 0.7), general
	// recovers from its invalid 1.5 via the valid 0.64, research and
	// surprise are outside the requested catalog, and the survivors sort
	// by score descending.
	want := []autoclassifier.Candidate{
		{Label: "coding", Score: 0.95},
		{Label: "general", Score: 0.64},
	}
	if !reflect.DeepEqual(outcome.Candidates, want) {
		t.Fatalf("candidates = %+v, want %+v", outcome.Candidates, want)
	}
}

// Without a ranked list nothing changes: the Outcome stays single-value and
// callers fill the single-candidate form themselves.
func TestClassifyJevWithoutRankedHasNoCandidates(t *testing.T) {
	server := captureServer(t, 200, `{"answers":{"task_type":{"choice":"coding","confidence":0.92}}}`, nil, nil)
	outcome := Classify(context.Background(), server.Client(), testInput(server.URL))
	if !outcome.OK() || outcome.Candidates != nil {
		t.Fatalf("outcome = %+v, want success without candidates", outcome)
	}
}

// An entirely invalid ranked list is diagnostics loss only: the classification
// itself stays valid through its primary choice.
func TestClassifyJevFullyInvalidRankedYieldsNoCandidates(t *testing.T) {
	server := captureServer(t, 200,
		`{"answers":{"task_type":{"choice":"coding","confidence":0.9,"ranked":[`+
			`{"choice":"surprise","confidence":0.9},{"confidence":0.5}]}}}`, nil, nil)
	outcome := Classify(context.Background(), server.Client(), testInput(server.URL))
	if !outcome.OK() || outcome.Category != "coding" || outcome.Candidates != nil {
		t.Fatalf("outcome = %+v, want success without candidates", outcome)
	}
}

// The OpenAI Decisions adapter accepts the same optional ranked list.
func TestClassifyOpenAIDecisionsRankedCandidates(t *testing.T) {
	server := captureServer(t, 200,
		`{"answers":[{"type":"choice","name":"task_type","choice":"general","confidence":0.75,`+
			`"ranked":[{"choice":"general","confidence":0.75},{"choice":"coding","confidence":0.6}]}]}`, nil, nil)
	input := testInput(server.URL)
	input.Protocol = ProtocolOpenAIDecisions
	outcome := Classify(context.Background(), server.Client(), input)
	if !outcome.OK() || outcome.Category != "general" {
		t.Fatalf("outcome = %+v", outcome)
	}
	want := []autoclassifier.Candidate{
		{Label: "general", Score: 0.75},
		{Label: "coding", Score: 0.6},
	}
	if !reflect.DeepEqual(outcome.Candidates, want) {
		t.Fatalf("candidates = %+v, want %+v", outcome.Candidates, want)
	}
}

// Deviation A regression: a ranked list that survives normalization while
// omitting the primary must not displace the primary from the candidate
// sequence. The primary is merged back in with its own score and the whole
// sequence re-sorted, so the rule evaluator still sees the classifier's main
// answer.
func TestClassifyJevRankedMissingPrimaryMergesPrimary(t *testing.T) {
	server := captureServer(t, 200,
		`{"answers":{"task_type":{"choice":"coding","confidence":0.95,"ranked":[`+
			`{"choice":"research","confidence":0.9},`+
			`{"choice":"architect","confidence":0.8}]}}}`, nil, nil)
	input := testInput(server.URL)
	input.Labels = []string{"general", "research", "coding", "architect"}
	outcome := Classify(context.Background(), server.Client(), input)
	if !outcome.OK() || outcome.Category != "coding" || outcome.Confidence != 0.95 {
		t.Fatalf("outcome = %+v, want the primary choice preserved", outcome)
	}
	// coding joins at its own 0.95 and takes first place on the re-sort,
	// ahead of research 0.9 and architect 0.8.
	want := []autoclassifier.Candidate{
		{Label: "coding", Score: 0.95},
		{Label: "research", Score: 0.9},
		{Label: "architect", Score: 0.8},
	}
	if !reflect.DeepEqual(outcome.Candidates, want) {
		t.Fatalf("candidates = %+v, want %+v", outcome.Candidates, want)
	}
}

// When the ranked list already describes the primary, ranks win: the primary's
// own confidence never overwrites the ranked score, whatever the direction of
// the difference.
func TestClassifyJevRankedDescribingPrimaryKeepsRankedScore(t *testing.T) {
	for name, body := range map[string]string{
		// The lead's scenario verbatim: primary confidence equals the ranked
		// score, and architect stays on top.
		"equal scores": `{"answers":{"task_type":{"choice":"coding","confidence":0.60,"ranked":[` +
			`{"choice":"architect","confidence":0.99},` +
			`{"choice":"coding","confidence":0.60}]}}}`,
		// The discriminating direction: the primary's confidence is higher,
		// yet the ranked 0.60 must stand.
		"higher primary confidence": `{"answers":{"task_type":{"choice":"coding","confidence":0.95,"ranked":[` +
			`{"choice":"architect","confidence":0.99},` +
			`{"choice":"coding","confidence":0.60}]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := captureServer(t, 200, body, nil, nil)
			input := testInput(server.URL)
			input.Labels = []string{"general", "coding", "architect"}
			outcome := Classify(context.Background(), server.Client(), input)
			if !outcome.OK() || outcome.Category != "coding" {
				t.Fatalf("outcome = %+v, want the primary choice preserved", outcome)
			}
			want := []autoclassifier.Candidate{
				{Label: "architect", Score: 0.99},
				{Label: "coding", Score: 0.60},
			}
			if !reflect.DeepEqual(outcome.Candidates, want) {
				t.Fatalf("candidates = %+v, want %+v", outcome.Candidates, want)
			}
		})
	}
}

// The same completion applies through the OpenAI Decisions adapter, since both
// adapters funnel through validatedChoiceWithRanked.
func TestClassifyOpenAIDecisionsRankedMissingPrimaryMergesPrimary(t *testing.T) {
	server := captureServer(t, 200,
		`{"answers":[{"type":"choice","name":"task_type","choice":"coding","confidence":0.95,`+
			`"ranked":[{"choice":"research","confidence":0.9},{"choice":"architect","confidence":0.8}]}]}`, nil, nil)
	input := testInput(server.URL)
	input.Protocol = ProtocolOpenAIDecisions
	input.Labels = []string{"general", "research", "coding", "architect"}
	outcome := Classify(context.Background(), server.Client(), input)
	if !outcome.OK() || outcome.Category != "coding" {
		t.Fatalf("outcome = %+v", outcome)
	}
	want := []autoclassifier.Candidate{
		{Label: "coding", Score: 0.95},
		{Label: "research", Score: 0.9},
		{Label: "architect", Score: 0.8},
	}
	if !reflect.DeepEqual(outcome.Candidates, want) {
		t.Fatalf("candidates = %+v, want %+v", outcome.Candidates, want)
	}
}
