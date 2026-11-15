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

func TestDisplayedValuesMaskCredentialsAndTrimAddresses(t *testing.T) {
	long := strings.Repeat("m", 210)
	for _, tc := range []struct {
		path, value string
		want        *string
	}{
		{"model", "gpt-5", Some("gpt-5")},
		{"security.auth.selectedType", "gemini-api-key", Some("gemini-api-key")},
		{"env.ANTHROPIC_AUTH_TOKEN", "sk-relay-0123456789", Some("sk-r…789")},
		{"apiKey", "secret", Some("…et")},
		{"env.ANTHROPIC_BASE_URL", "https://me:pw@relay.example/v1?key=abc#x", Some("https://relay.example/v1")},
		{"env.ANTHROPIC_BASE_URL", "https://me@relay.example", Some("https://relay.example/")},
		{"env.ANTHROPIC_BASE_URL", "http://127.0.0.1:1", Some("http://127.0.0.1:1")},
		{"env.ANTHROPIC_CUSTOM_HEADERS", `{"x": 1}`, nil},
		{"list", "[1]", nil},
		{"multi", "a\nb", nil},
		{"empty", "  ", nil},
		{"model", long, Some(strings.Repeat("m", 200) + "…")},
	} {
		got := DisplayedValue(tc.path, tc.value)
		if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
			t.Errorf("%s %q: %v", tc.path, tc.value, got)
		}
	}
}
