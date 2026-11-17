package format

import (
	"testing"
)

var geminiEnvGroup = []string{"GEMINI_API_KEY", "GEMINI_MODEL", "GOOGLE_GEMINI_BASE_URL"}

func mustParseEnv(t *testing.T, text string) *EnvFile {
	t.Helper()
	env, err := ParseEnv(text, geminiEnvGroup)
	if err != nil {
		t.Fatalf("ParseEnv(%q): %v", text, err)
	}
	return env
}

func TestEnvFilesKeepLineEndingsAndDropShadowedNames(t *testing.T) {
	env := mustParseEnv(t, "A=1\r\nGEMINI_API_KEY=first\r\n  GEMINI_API_KEY: second # note\r\n")
	if value, _ := env.Get("GEMINI_API_KEY"); value != "second" {
		t.Fatalf("GEMINI_API_KEY = %q", value)
	}
	if err := env.Set("GEMINI_API_KEY", "with space"); err != nil {
		t.Fatal(err)
	}
	if err := env.Set("GOOGLE_GEMINI_BASE_URL", "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	want := "A=1\r\n  GEMINI_API_KEY=\"with space\"\r\nGOOGLE_GEMINI_BASE_URL=http://127.0.0.1:1\r\n"
	if got := env.Render(); got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
	env.Remove("A")
	env.Remove("GEMINI_API_KEY")
	env.Remove("GOOGLE_GEMINI_BASE_URL")
	if !env.IsEmpty() {
		t.Fatalf("expected an empty file, got %q", env.Render())
	}

	// A quoted value may span lines, but not one AstrLink writes.
	other := mustParseEnv(t, "NOTE=\"one\ntwo\"\nGEMINI_MODEL='m'\n")
	if value, _ := other.Get("NOTE"); value != "one\ntwo" {
		t.Fatalf("NOTE = %q", value)
	}
	if value, _ := other.Get("GEMINI_MODEL"); value != "m" {
		t.Fatalf("GEMINI_MODEL = %q", value)
	}
	if got := other.Render(); got != "NOTE=\"one\ntwo\"\nGEMINI_MODEL='m'\n" {
		t.Fatalf("Render() = %q", got)
	}
	_, err := ParseEnv("# a\nGEMINI_API_KEY=\"one\ntwo\"\n", geminiEnvGroup)
	invalid, ok := err.(*Invalid)
	if !ok || invalid.Line != 2 {
		t.Fatalf("err = %#v, want an Invalid at line 2", err)
	}
	if _, err := EncodeEnv("a\"b'\nc"); err == nil {
		t.Fatal("EncodeEnv accepted a value no quoting holds")
	}
}

func TestEnvLinesChangeInPlace(t *testing.T) {
	text := "# keys\nexport GEMINI_API_KEY=\"old-key-123456\"\nHTTP_PROXY=http://proxy # mine\nGEMINI_MODEL=gemini-2.5-pro\n"
	env := mustParseEnv(t, text)
	if value, _ := env.Get("GEMINI_API_KEY"); value != "old-key-123456" {
		t.Fatalf("GEMINI_API_KEY = %q", value)
	}
	if value, _ := env.Get("HTTP_PROXY"); value != "http://proxy" {
		t.Fatalf("HTTP_PROXY = %q", value)
	}
	for _, set := range [][2]string{
		{"GEMINI_API_KEY", "astr_token"},
		{"GEMINI_MODEL", "gemini-route"},
		{"GOOGLE_GEMINI_BASE_URL", "http://127.0.0.1:18317"},
	} {
		if err := env.Set(set[0], set[1]); err != nil {
			t.Fatal(err)
		}
	}
	want := "# keys\nexport GEMINI_API_KEY=astr_token\nHTTP_PROXY=http://proxy # mine\nGEMINI_MODEL=gemini-route\nGOOGLE_GEMINI_BASE_URL=http://127.0.0.1:18317\n"
	if got := env.Render(); got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
	if got := env.Names(); len(got) != 4 || got[0] != "GEMINI_API_KEY" || got[3] != "GOOGLE_GEMINI_BASE_URL" {
		t.Fatalf("Names() = %q", got)
	}
}

func TestEnvAssignments(t *testing.T) {
	for _, tc := range []struct {
		line, name, value string
		ok                bool
	}{
		{"A=1", "A", "1", true},
		{"  export  B = two words # c", "B", "two words", true},
		{"C: x", "C", "x", true},
		{"C:x", "", "", false},
		{"export=1", "export", "1", true},
		{"exportD=1", "exportD", "1", true},
		{"# A=1", "", "", false},
		{"=1", "", "", false},
		{"E='a # b'", "E", "a # b", true},
		{"F=\"a\\nb\"", "F", "a\nb", true},
		{"G=`x`", "G", "x", true},
		{"H=\"open", "H", "\"open", true},
		{"I=\"a\\\"b\"", "I", "a\\\"b", true},
	} {
		env, err := ParseEnv(tc.line+"\n", nil)
		if err != nil {
			t.Fatalf("%q: %v", tc.line, err)
		}
		names := env.Names()
		if !tc.ok {
			if len(names) != 0 {
				t.Errorf("%q: read as %q", tc.line, names)
			}
			continue
		}
		value, found := env.Get(tc.name)
		if !found || value != tc.value {
			t.Errorf("%q: %s = %q (%v), want %q", tc.line, tc.name, value, found, tc.value)
		}
		if env.Render() != tc.line+"\n" {
			t.Errorf("%q: rendered %q", tc.line, env.Render())
		}
	}
}

func TestEnvEncoding(t *testing.T) {
	for value, want := range map[string]string{
		"astr_AAAA":         "astr_AAAA",
		"http://x:1/v1":     "http://x:1/v1",
		"":                  `""`,
		"two words":         `"two words"`,
		"a#b":               `"a#b"`,
		`say "hi"`:          `'say "hi"'`,
		`back\slash`:        `'back\slash'`,
		"模型":                `"模型"`,
		"key=[1,2]%~@+.,:-": "key=[1,2]%~@+.,:-",
	} {
		got, err := EncodeEnv(value)
		if err != nil || got != want {
			t.Errorf("EncodeEnv(%q) = %q, %v; want %q", value, got, err, want)
		}
	}
	if _, err := EncodeEnv("a'b\"c"); err == nil {
		t.Error("EncodeEnv accepted both quotes")
	}
}

func TestClosingQuote(t *testing.T) {
	for _, tc := range []struct {
		text  string
		quote rune
		want  int
	}{
		{`"abc"`, '"', 4},
		{`"a\"b"`, '"', 5},
		{`"a\\"b"`, '"', 4},
		{`'abc`, '\'', -1},
		{`"é"`, '"', 3},
	} {
		if got := ClosingQuote(tc.text, tc.quote); got != tc.want {
			t.Errorf("ClosingQuote(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}
