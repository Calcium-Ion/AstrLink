package convo

import "testing"

func TestVisibleTextUnwrapsWrappers(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "  审核完先别改，给我结论  ", "审核完先别改，给我结论"},
		{"empty", "  \n ", ""},

		// The one named rule: <system-reminder> is dropped, never unwrapped.
		{"reminder only", "<system-reminder>\nToday is Monday.\n</system-reminder>", ""},
		{"text then trailing reminder", "fix main.go\n\n<system-reminder>\nnote\n</system-reminder>", "fix main.go"},
		{"reminder then text", "<system-reminder>x</system-reminder>\nhello", "hello"},
		{"reminder inside wrapper", "<user_query>hi <system-reminder>x</system-reminder></user_query>", "hi"},
		{"unterminated reminder kept", "<system-reminder>\nno closing tag", "<system-reminder>\nno closing tag"},

		// Structural unwrapping: text outside blocks wins.
		{"skill then text", "<skill name=\"paseo-advisor\">\nbody\n</skill>\n\n审核完先别改，给我结论", "审核完先别改，给我结论"},
		{"two blocks then text", "<system-reminder>\nnote\n</system-reminder>\n<skill name=\"x\">s</skill>\nhello", "hello"},
		{"text then pasted block", "看看这段\n<pre>\ncode\n</pre>", "看看这段"},
		{"text between blocks", "<a>x</a> middle <b>y</b>", "middle"},

		// Wholly wrapped: open the last block.
		{"skill only", "<skill name=\"paseo-advisor\" location=\"/x/SKILL.md\">\nbody\n</skill>", "body"},
		{"cursor user_query", "<user_query>\nwhat is a goroutine\n</user_query>", "what is a goroutine"},
		{"cursor context then query", "<additional_data>\n<attached_files>f</attached_files>\n</additional_data>\n\n<user_query>\nfix it\n</user_query>", "fix it"},
		{"nested wrappers", "<outer><inner>deep</inner></outer>", "deep"},
		{"nested same name", "<summary><summary>a</summary> b</summary>", "b"},
		{"compaction summary keeps lead sentence", "The conversation history before this point was compacted into the following summary:\n\n<summary>…</summary>", "The conversation history before this point was compacted into the following summary:"},

		// Not blocks.
		{"unterminated block kept", "<skill name=\"x\">\nno closing tag", "<skill name=\"x\">\nno closing tag"},
		{"self-closing is not a wrapper", "<br/> hello", "<br/> hello"},
		{"less-than is not a tag", "<3 you", "<3 you"},
		{"mismatched close kept", "<a>x</b>", "<a>x</b>"},
		{"similar tag not confused", "<skillful>hi</skillful> yes", "yes"},
		{"markdown code untouched", "```html\n<div>x</div>\n```", "```html\n<div>x</div>\n```"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := visibleText(tc.in); got != tc.want {
				t.Fatalf("visibleText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestVisibleTextDepthIsBounded(t *testing.T) {
	deep := "x"
	for range maxUnwrapDepth + 2 {
		deep = "<w>" + deep + "</w>"
	}
	if got := visibleText(deep); got == "x" {
		t.Fatal("unwrapping must stop at maxUnwrapDepth")
	} else if got == "" {
		t.Fatal("bounded unwrapping must still return text")
	}
}
