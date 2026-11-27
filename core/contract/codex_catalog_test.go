package contract

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestCodexReasoningUsesOnlyTheModelsDeclaredChoices(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		want   []string
		chosen string
	}{
		{"", []string{"low", "max"}, ""},
		{`{"supported_reasoning_levels":[],"default_reasoning_level":"low"}`, []string{}, ""},
		{`{"supported_reasoning_levels":[{"effort":"high"},{"effort":"high"},{"effort":"turbo"}],"default_reasoning_level":"high"}`, []string{"high"}, "high"},
		{`{"default_reasoning_level":"medium"}`, []string{"low", "max"}, ""},
	} {
		got := ModelReasoning(json.RawMessage(tc.raw), []string{"low", "turbo", "max", "low"})
		var levels []string
		for _, level := range got.Supported {
			levels = append(levels, level.Effort)
		}
		if got.Supported == nil || !slices.Equal(levels, tc.want) || tc.chosen == "" && got.Default != nil || tc.chosen != "" && (got.Default == nil || *got.Default != tc.chosen) {
			t.Errorf("reasoning for %s = %+v", tc.raw, got)
		}
	}
}
