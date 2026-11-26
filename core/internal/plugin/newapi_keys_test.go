package plugin

import (
	"strings"
	"testing"
)

func TestNewAPIKeyProblemNamesWhatNewAPIRefuses(t *testing.T) {
	for _, key := range []string{"api", "v1", "mj", "dashboard", "ali", "suno", strings.Repeat("a", NewAPIKeyLength+1)} {
		if NewAPIKeyProblem(key) == "" {
			t.Errorf("%s passed", key)
		}
	}
	for _, key := range []string{"request-filter", "alibaba", "sunoapi", "claude-code-headers", strings.Repeat("a", NewAPIKeyLength)} {
		if problem := NewAPIKeyProblem(key); problem != "" {
			t.Errorf("%s: %s", key, problem)
		}
	}
}
