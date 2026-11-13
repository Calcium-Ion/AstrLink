package clientconfig

import (
	"reflect"
	"strings"
	"testing"
)

func TestPreviewsMaskSecretsAndShowChangesWithContext(t *testing.T) {
	for _, tc := range [][2]string{
		{"GEMINI_API_KEY=sk-1234567890abcdef", "GEMINI_API_KEY=sk-1…def"},
		{`experimental_bearer_token = "abcdefghijklmnop"`, `experimental_bearer_token = "abcd…nop"`},
		{`    "apiKey": "astr_…AAAAA0",`, `    "apiKey": "astr_…AAAAA0",`},
		{`  "ANTHROPIC_BASE_URL": "http://127.0.0.1:1",`, `  "ANTHROPIC_BASE_URL": "http://127.0.0.1:1",`},
		{`  "auth": {`, `  "auth": {`},
		{`      "selectedType": "gemini-api-key"`, `      "selectedType": "gemini-api-key"`},
		{`{ apiKey: 'sk-abcdefghijkl', api: 'x' }`, `{ apiKey: 'sk-a…jkl', api: 'x' }`},
	} {
		if got := maskValues(tc[0], false); got != tc[1] {
			t.Errorf("%q: %q, want %q", tc[0], got, tc[1])
		}
	}

	var before strings.Builder
	for n := 1; n <= 10; n++ {
		before.WriteString("l" + string(rune('0'+n%10)) + "\n")
	}
	text := strings.ReplaceAll(before.String(), "l0\n", "l10\n")
	after := strings.NewReplacer("l2\n", "L2\n", "l9\n", "L9\n").Replace(text)
	var kinds []string
	for _, line := range diffLines(text, after) {
		switch line.Kind {
		case LineContext:
			kinds = append(kinds, " "+line.Text)
		case LineAdd:
			kinds = append(kinds, "+"+line.Text)
		case LineRemove:
			kinds = append(kinds, "-"+line.Text)
		case LineGap:
			kinds = append(kinds, "…")
		}
	}
	want := []string{" l1", "-l2", "+L2", " l3", " l4", "…", " l7", " l8", "-l9", "+L9", " l10"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("%q", kinds)
	}
}

func TestMaskKeepsItsEnds(t *testing.T) {
	for in, want := range map[string]string{"": "", "abcd": "…", "abcde": "…de", "abcdefghijkl": "abcd…jkl", "x…y": "x…y"} {
		if got := mask(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestShownValuesAreModelChoicesOnly(t *testing.T) {
	for _, tc := range []struct {
		path, value string
		shown       bool
	}{
		{"model", "gpt-5", true},
		{"env.ANTHROPIC_MODEL", "claude", true},
		{"security.auth.selectedType", "gemini-api-key", true},
		{"model_provider", "astrlink", true},
		{"env.ANTHROPIC_AUTH_TOKEN", "astr_x", false},
		{"env.ANTHROPIC_BASE_URL", "http://127.0.0.1:1", false},
		{"model_auth_key", "x", false},
	} {
		if got := ShownValue(tc.path, tc.value); (got != nil) != tc.shown {
			t.Errorf("%s: %v", tc.path, got)
		}
	}
}
