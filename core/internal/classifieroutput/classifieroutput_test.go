package classifieroutput

import (
	"errors"
	"strings"
	"testing"
)

var defaultLabels = []string{"general", "research", "coding", "architect"}

func TestParseObjectAcceptsOneStrictObject(t *testing.T) {
	cases := []string{
		`{"label":"coding","score":0.81}`,
		"\n\t {\"label\":\"coding\"} \n",
		`{"label":"coding","score":0.81,"candidates":[{"label":"research","score":0.64}]}`,
	}
	for _, body := range cases {
		if _, err := ParseObject(body); err != nil {
			t.Errorf("ParseObject(%q) rejected a strict object: %v", body, err)
		}
	}
}

func TestParseObjectRejectsNonObjects(t *testing.T) {
	cases := []string{
		``,
		`   `,
		`"coding"`,
		`42`,
		`null`,
		`true`,
		`["coding","research"]`,
		`[{"label":"coding"}]`,
	}
	for _, body := range cases {
		if _, err := ParseObject(body); !errors.Is(err, ErrInvalidJSON) {
			t.Errorf("ParseObject(%q) accepted a non-object (err=%v)", body, err)
		}
	}
}

func TestParseObjectRejectsTrailingContent(t *testing.T) {
	cases := []string{
		`{"label":"coding"} {"label":"research"}`,
		`{"label":"coding"} here is why`,
		`{"label":"coding"}{"label":"research"}`,
	}
	for _, body := range cases {
		if _, err := ParseObject(body); !errors.Is(err, ErrInvalidJSON) {
			t.Errorf("ParseObject(%q) accepted trailing content (err=%v)", body, err)
		}
	}
}

func TestParseObjectRejectsFences(t *testing.T) {
	cases := []string{
		"```json\n{\"label\":\"coding\"}\n```",
		"```\n{\"label\":\"coding\"}\n```",
		"`{\"label\":\"coding\"}`",
	}
	for _, body := range cases {
		if _, err := ParseObject(body); !errors.Is(err, ErrInvalidJSON) {
			t.Errorf("ParseObject(%q) accepted a fenced block (err=%v)", body, err)
		}
	}
}

func TestParseObjectRejectsDuplicateKeysAtAnyDepth(t *testing.T) {
	cases := []string{
		`{"label":"coding","label":"research"}`,
		`{"candidates":[{"label":"a","label":"b"}]}`,
		`{"a":{"b":1,"b":2},"label":"coding"}`,
	}
	for _, body := range cases {
		if _, err := ParseObject(body); !errors.Is(err, ErrInvalidJSON) {
			t.Errorf("ParseObject(%q) accepted duplicate keys (err=%v)", body, err)
		}
	}
}

func TestParseObjectRejectsBrokenJSON(t *testing.T) {
	cases := []string{
		`{"label":}`,
		`{"label":"coding"`,
		`{label:"coding"}`,
		`{'label':'coding'}`,
	}
	for _, body := range cases {
		if _, err := ParseObject(body); !errors.Is(err, ErrInvalidJSON) {
			t.Errorf("ParseObject(%q) accepted broken JSON (err=%v)", body, err)
		}
	}
}

func TestParseObjectRejectsOverrun(t *testing.T) {
	wide := "{" + strings.TrimSuffix(strings.Repeat(`"k0":1,`, MaxObjectKeys+1), ",") + "}"
	if _, err := ParseObject(wide); !errors.Is(err, ErrInvalidJSON) {
		t.Errorf("ParseObject accepted more than %d keys", MaxObjectKeys)
	}
	deep := strings.Repeat("[", MaxDepth+1) + strings.Repeat("]", MaxDepth+1)
	if _, err := ParseObject(`{"d":` + deep + `}`); !errors.Is(err, ErrInvalidJSON) {
		t.Errorf("ParseObject accepted nesting deeper than %d", MaxDepth)
	}
	if _, err := ParseObject(`{"d":` + strings.Repeat("[", MaxDepth) + strings.Repeat("]", MaxDepth) + `}`); err != nil {
		t.Errorf("ParseObject rejected nesting at the limit: %v", err)
	}
}

func TestParseTaskTypeStrictShape(t *testing.T) {
	if result, err := ParseTaskType(`{"label":"coding","score":0.81}`, defaultLabels); err != nil {
		t.Fatalf("valid output rejected: %v", err)
	} else if result.Label != "coding" || result.Score != 0.81 {
		t.Fatalf("parsed %+v", result)
	}
	if _, err := ParseTaskType(`{"label":"coding","confidence":0.81}`, defaultLabels); !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("unknown key accepted (err=%v)", err)
	}
	if _, err := ParseTaskType(`{"label":"astronomy","score":0.9}`, defaultLabels); !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("out-of-catalog label accepted (err=%v)", err)
	}
	if _, err := ParseTaskType(`{"label":"coding","score":1.5}`, defaultLabels); !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("out-of-range score accepted (err=%v)", err)
	}
	if _, err := ParseTaskType(`{"label":"coding","score":0.5,"candidates":[{"label":"nope","score":0.1}]}`, defaultLabels); !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("out-of-catalog candidate accepted (err=%v)", err)
	}
	if _, err := ParseTaskType(`{}`, defaultLabels); !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("empty object accepted (err=%v)", err)
	}
	if _, err := ParseTaskType(`{"score":0.5}`, defaultLabels); !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("missing label accepted (err=%v)", err)
	}
}

func TestParseTaskTypeNormalizesCandidates(t *testing.T) {
	result, err := ParseTaskType(
		`{"label":"coding","score":0.81,"candidates":[`+
			`{"label":"research","score":0.64},`+
			`{"label":"coding","score":0.81},`+
			`{"label":"coding","score":0.4},`+
			`{"label":"architect","score":0.64}]}`,
		defaultLabels)
	if err != nil {
		t.Fatalf("valid candidates rejected: %v", err)
	}
	want := []Candidate{
		{Label: "coding", Score: 0.81},
		{Label: "research", Score: 0.64}, // catalog order breaks the tie
		{Label: "architect", Score: 0.64},
	}
	if len(result.Candidates) != len(want) {
		t.Fatalf("candidates %+v want %+v", result.Candidates, want)
	}
	for index, candidate := range result.Candidates {
		if candidate.Label != want[index].Label || candidate.Score != want[index].Score {
			t.Fatalf("candidates %+v want %+v", result.Candidates, want)
		}
	}
}

func TestParseTaskTypeAdoptsTopCandidateWithoutLabel(t *testing.T) {
	result, err := ParseTaskType(`{"candidates":[{"label":"research","score":0.4},{"label":"coding","score":0.9}]}`, defaultLabels)
	if err != nil {
		t.Fatalf("candidates-only output rejected: %v", err)
	}
	if result.Label != "coding" || result.Score != 0.9 {
		t.Fatalf("top candidate not adopted: %+v", result)
	}
}

func TestAssistantTextUnpacksResponsesObject(t *testing.T) {
	body := []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed",` +
		`"content":[{"type":"reasoning","summary":[]},{"type":"output_text","text":"  {\"label\":\"coding\"}  "}]}]}`)
	text, err := AssistantText(body)
	if err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}
	if text != `{"label":"coding"}` {
		t.Fatalf("text=%q", text)
	}
}

func TestAssistantTextRefusesNonAnswers(t *testing.T) {
	cases := []struct {
		name string
		body string
		want error
	}{
		{"tool only", `{"output":[{"type":"function_call","name":"f"}]}`, ErrNoAssistantText},
		{"empty content", `{"output":[{"type":"message","content":[]}]}`, ErrNoAssistantText},
		{"refusal", `{"output":[{"type":"message","content":[{"type":"refusal","refusal":"no"}]}]}`, ErrRefusal},
		{"incomplete", `{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`, ErrIncompleteOutput},
		{"incomplete details", `{"incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`, ErrIncompleteOutput},
		{"two texts", `{"output":[{"type":"message","content":[{"type":"output_text","text":"a"},{"type":"output_text","text":"b"}]}]}`, ErrAmbiguousText},
	}
	for _, testCase := range cases {
		if _, err := AssistantText([]byte(testCase.body)); !errors.Is(err, testCase.want) {
			t.Errorf("%s: got err=%v want=%v", testCase.name, err, testCase.want)
		}
	}
	if _, err := AssistantText([]byte(`not json`)); err == nil {
		t.Errorf("broken response body accepted")
	}
	if _, err := AssistantText([]byte(`{"output":[{"type":"message","status":"in_progress","content":[{"type":"output_text","text":"x"}]}]}`)); !errors.Is(err, ErrNoAssistantText) {
		t.Errorf("in-progress message accepted")
	}
}
