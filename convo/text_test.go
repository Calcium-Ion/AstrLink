package convo

import "testing"

func TestVisibleTextStripsHarnessBlocks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "  审核完先别改，给我结论  ", "审核完先别改，给我结论"},
		{"skill only", `<skill name="paseo-advisor" location="/x/SKILL.md">
body
</skill>`, ""},
		{"skill then text", `<skill name="paseo-advisor">
body
</skill>

审核完先别改，给我结论`, "审核完先别改，给我结论"},
		{"two blocks then text", "<system-reminder>\nnote\n</system-reminder>\n<skill name=\"x\">s</skill>\nhello", "hello"},
		{"text then trailing reminder", "fix main.go\n\n<system-reminder>\nnote\n</system-reminder>", "fix main.go"},
		{"reminder only", "<system-reminder>\nToday is Monday.\n</system-reminder>", ""},
		{"unterminated block kept", "<skill name=\"x\">\nno closing tag", "<skill name=\"x\">\nno closing tag"},
		{"similar tag not stripped", "<skillful>hi</skillful> yes", "<skillful>hi</skillful> yes"},
		{"user tags untouched", "<user_query>what is a goroutine</user_query>", "<user_query>what is a goroutine</user_query>"},
		{"pi compaction summary", "The conversation history before this point was compacted into the following summary:\n\n<summary>…</summary>", ""},
		{"claude compaction summary", "This session is being continued from a previous conversation that ran out of context. The conversation is summarized below:\n…", ""},
		{"compaction after block", "<system-reminder>x</system-reminder>\nThe conversation history before this point was compacted", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := visibleText(tc.in); got != tc.want {
				t.Fatalf("visibleText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
