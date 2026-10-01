package intelligence

import (
	"github.com/QuantumNous/astrlink/core/contract"
	"testing"
)

func TestChannelModelSelectionDoesNotSilentlyChooseFirstModel(t *testing.T) {
	service := contract.Service{Models: []string{"deepseek-v4.1-flash"}}
	cfg := DefaultSettings()
	q := cfg.Questions[1]
	if _, err := ResolveModel(service, ChannelConfig{UseDefault: true}, cfg, q); err == nil {
		t.Fatal("missing Astra should require an explicit channel model")
	}
	model, err := ResolveModel(service, ChannelConfig{Model: "deepseek-v4.1-flash"}, cfg, q)
	if err != nil || model != "deepseek-v4.1-flash" {
		t.Fatalf("override: %q %v", model, err)
	}
	if _, err := ResolveModel(service, ChannelConfig{}, cfg, q); err == nil {
		t.Fatal("unset model silently fell back")
	}
}

func TestGradeFinalAnswerNotReasoningOrSubstring(t *testing.T) {
	for _, tc := range []struct{ kind, expected, output, status string }{
		{"number", "21", "121", "failed"},
		{"number", "21", "思考中有21，但最终答案是29。", "failed"},
		{"number", "21", "**答案：21颗**", "passed"},
		{"number", "21", "推理过程有21，最终答案：\\[\n\\boxed{29\\text{个}}\n\\]", "failed"},
		{"number", "21", "最终答案：\\boxed{21}", "passed"},
		{"number", "21", "可能21，也可能29", "ungraded"},
		{"text", "yes", "Yes.", "passed"},
		{"text", "yes", "Yesterday", "failed"},
		{"text", "yes", "no", "failed"},
	} {
		status, _ := Grade(Question{Kind: tc.kind, Answer: tc.expected}, tc.output)
		if status != tc.status {
			t.Errorf("%q: got %s want %s", tc.output, status, tc.status)
		}
	}
}

func TestChannelQuestionsAndRandomWithoutReplacement(t *testing.T) {
	cfg := DefaultSettings()
	selected, err := SelectQuestions(cfg, ChannelConfig{QuestionIDs: []string{"candy", "thibault"}}, "all", 2)
	if err != nil || len(selected) != 2 || selected[0].ID != "candy" {
		t.Fatalf("channel selection: %v %v", selected, err)
	}
	selected, err = SelectQuestions(cfg, ChannelConfig{}, "random", 3)
	if err != nil || len(selected) != 3 {
		t.Fatalf("random: %v %v", selected, err)
	}
	seen := map[string]bool{}
	for _, q := range selected {
		if seen[q.ID] {
			t.Fatal("duplicate random question")
		}
		seen[q.ID] = true
	}
}
