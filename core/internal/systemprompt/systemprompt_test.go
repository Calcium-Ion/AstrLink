package systemprompt

import (
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/providerapi"
)

const (
	billing = "x-anthropic-billing-header: cc_version=2.1.300.abc; cc_entrypoint=cli; cch=00000;"
	droid   = providerapi.DroidSystemPromptLine
)

// addBoth adds the prefix and the suffix the way the gateway does.
func addBoth(t *testing.T, protocol contract.ProtocolID, body string) string {
	t.Helper()
	updated, _, err := Prepend(protocol, []byte(body), "PRE")
	if err != nil {
		t.Fatal(err)
	}
	updated, _, err = Append(protocol, updated, "SUF")
	if err != nil {
		t.Fatal(err)
	}
	return string(updated)
}

func TestPrefixAndSuffixPlacement(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		body     string
		want     string
	}{
		{
			"responses string", contract.ProtocolOpenAIResponses,
			`{"model":"m", "instructions" : "base", "input":"hi"}`,
			`{"model":"m", "instructions" : "PRE\n\nbase\n\nSUF", "input":"hi"}`,
		},
		{
			"responses absent", contract.ProtocolOpenAIResponses,
			`{"model":"m","input":"hi"}`,
			`{"model":"m","input":"hi","instructions":"PRE\n\nSUF"}`,
		},
		{
			"compact null", contract.ProtocolOpenAIResponsesCompact,
			`{"instructions":null,"input":[]}`,
			`{"instructions":"PRE\n\nSUF","input":[]}`,
		},
		{
			"responses Droid line", contract.ProtocolOpenAIResponses,
			`{"instructions":"` + droid + `\nbase"}`,
			`{"instructions":"` + droid + `\n\nPRE\nbase\n\nSUF"}`,
		},
		{
			"anthropic string", contract.ProtocolAnthropicMessages,
			`{"system":"base","messages":[]}`,
			`{"system":"PRE\n\nbase\n\nSUF","messages":[]}`,
		},
		{
			"anthropic absent", contract.ProtocolAnthropicMessages,
			`{"messages":[{"role":"user","content":"hi"}]}`,
			`{"messages":[{"role":"user","content":"hi"}],"system":"PRE\n\nSUF"}`,
		},
		{
			"anthropic banner string", contract.ProtocolAnthropicMessages,
			`{"system":"` + ClaudeCodeBanner + `"}`,
			`{"system":"` + ClaudeCodeBanner + `\n\nPRE\n\nSUF"}`,
		},
		{
			"anthropic blocks", contract.ProtocolAnthropicMessages,
			`{"system":[ {"type":"text","text":"base","cache_control":{"type":"ephemeral"}} ]}`,
			`{"system":[{"text":"PRE","type":"text"}, {"type":"text","text":"base","cache_control":{"type":"ephemeral"}},{"text":"SUF","type":"text"} ]}`,
		},
		{
			"anthropic Claude Code identity", contract.ProtocolAnthropicMessages,
			`{"system":[{"type":"text","text":"` + billing + `"},{"type":"text","text":"` + ClaudeCodeBanner + `","cache_control":{"type":"ephemeral"}},{"type":"text","text":"main"}]}`,
			`{"system":[{"type":"text","text":"` + billing + `"},{"type":"text","text":"` + ClaudeCodeBanner + `","cache_control":{"type":"ephemeral"}},{"text":"PRE","type":"text"},{"type":"text","text":"main"},{"text":"SUF","type":"text"}]}`,
		},
		{
			"anthropic Droid block with prompt", contract.ProtocolAnthropicMessages,
			`{"system":[{"type":"text","text":"` + droid + `\nmain"}]}`,
			`{"system":[{"type":"text","text":"` + droid + `\n\nPRE\nmain"},{"text":"SUF","type":"text"}]}`,
		},
		{
			"chat string", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"system","content":"base"},{"role":"user","content":"hi"}]}`,
			`{"messages":[{"role":"system","content":"PRE\n\nbase\n\nSUF"},{"role":"user","content":"hi"}]}`,
		},
		{
			"chat leading run", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"developer","content":"one"},{"role":"system","content":[{"type":"text","text":"two"}]},{"role":"user","content":"hi"},{"role":"system","content":"later"}]}`,
			`{"messages":[{"role":"developer","content":"PRE\n\none"},{"role":"system","content":[{"type":"text","text":"two"},{"text":"SUF","type":"text"}]},{"role":"user","content":"hi"},{"role":"system","content":"later"}]}`,
		},
		{
			"chat without system", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"user","content":"hi"},{"role":"system","content":"later"}]}`,
			`{"messages":[{"content":"PRE\n\nSUF","role":"system"},{"role":"user","content":"hi"},{"role":"system","content":"later"}]}`,
		},
		{
			"chat null system content", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"system","content":null},{"role":"user","content":"hi"}]}`,
			`{"messages":[{"content":"PRE","role":"system"},{"role":"system","content":null},{"content":"SUF","role":"system"},{"role":"user","content":"hi"}]}`,
		},
		{
			"chat Droid line", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"system","content":"` + droid + `"},{"role":"user","content":"hi"}]}`,
			`{"messages":[{"role":"system","content":"` + droid + `\n\nPRE\n\nSUF"},{"role":"user","content":"hi"}]}`,
		},
		{
			"gemini parts", contract.ProtocolGoogleGenerateContent,
			`{"systemInstruction":{"role":"system","parts":[ {"text":"base"} ]},"contents":[]}`,
			`{"systemInstruction":{"role":"system","parts":[{"text":"PRE"}, {"text":"base"},{"text":"SUF"} ]},"contents":[]}`,
		},
		{
			"gemini absent", contract.ProtocolGoogleGenerateContent,
			`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
			`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"systemInstruction":{"parts":[{"text":"PRE"},{"text":"SUF"}]}}`,
		},
		{
			"gemini snake case", contract.ProtocolGoogleGenerateContent,
			`{"system_instruction":{"parts":[{"text":"base"}]},"contents":[]}`,
			`{"system_instruction":{"parts":[{"text":"PRE"},{"text":"base"},{"text":"SUF"}]},"contents":[]}`,
		},
		{
			"gemini cached content", contract.ProtocolGoogleGenerateContent,
			`{"cachedContent":"cachedContents/abc","contents":[]}`,
			`{"cachedContent":"cachedContents/abc","contents":[]}`,
		},
		{
			"completions", contract.ProtocolOpenAICompletions,
			`{"prompt":"continue"}`,
			`{"prompt":"continue"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := addBoth(t, test.protocol, test.body); got != test.want {
				t.Fatalf("got  %s\nwant %s", got, test.want)
			}
		})
	}
}

func TestUnexpectedChannelsAreLeftAlone(t *testing.T) {
	for _, test := range []struct {
		protocol contract.ProtocolID
		body     string
	}{
		{contract.ProtocolOpenAIResponses, `{"instructions":[{"role":"developer","content":"x"}]}`},
		{contract.ProtocolAnthropicMessages, `{"system":42}`},
		{contract.ProtocolOpenAIChat, `{"messages":"hi"}`},
		{contract.ProtocolGoogleGenerateContent, `{"systemInstruction":"text"}`},
	} {
		updated, changed, err := Prepend(test.protocol, []byte(test.body), "PRE")
		if err != nil || changed || string(updated) != test.body {
			t.Fatalf("%s: %s changed=%t err=%v", test.protocol, updated, changed, err)
		}
	}
}

// The privacy notice is added to the client's body before the gateway adds
// its prefix and suffix, so the prefix lands between the identity and the
// notice.
func TestPrefixGoesBeforeAnEarlierNotice(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		body     string
		want     string
	}{
		{
			"anthropic blocks", contract.ProtocolAnthropicMessages,
			`{"system":[{"type":"text","text":"` + billing + `"},{"type":"text","text":"` + ClaudeCodeBanner + `"},{"type":"text","text":"base"}]}`,
			`{"system":[{"type":"text","text":"` + billing + `"},{"type":"text","text":"` + ClaudeCodeBanner + `"},` +
				`{"text":"PRE","type":"text"},{"text":"NOTICE","type":"text"},{"type":"text","text":"base"},{"text":"SUF","type":"text"}]}`,
		},
		{
			"anthropic string", contract.ProtocolAnthropicMessages,
			`{"system":"` + ClaudeCodeBanner + `\n\nbase"}`,
			`{"system":"` + ClaudeCodeBanner + `\n\nPRE\n\nNOTICE\n\nbase\n\nSUF"}`,
		},
		{
			"chat", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"user","content":"hi"}]}`,
			`{"messages":[{"content":"PRE\n\nNOTICE\n\nSUF","role":"system"},{"role":"user","content":"hi"}]}`,
		},
	} {
		noticed, _, err := Prepend(test.protocol, []byte(test.body), "NOTICE")
		if err != nil {
			t.Fatal(err)
		}
		if got := addBoth(t, test.protocol, string(noticed)); got != test.want {
			t.Errorf("%s:\n got %s\nwant %s", test.name, got, test.want)
		}
	}
}

func TestIdentityLead(t *testing.T) {
	for _, test := range []struct {
		text string
		want int
	}{
		{"", 0},
		{"You are Claude Code", 0},
		{ClaudeCodeBanner, len(ClaudeCodeBanner)},
		{ClaudeCodeBanner + "X", 0},
		{ClaudeCodeBanner + "\n\nrest", len(ClaudeCodeBanner)},
		{billing + "\n" + ClaudeCodeBanner + "\nrest", len(billing) + 1 + len(ClaudeCodeBanner)},
		{droid + " rest", len(droid)},
		{"rest " + ClaudeCodeBanner, 0},
	} {
		if got := identityLead(test.text); got != test.want {
			t.Errorf("identityLead(%q) = %d, want %d", test.text, got, test.want)
		}
	}
}

func TestClientPromptBytesSurvive(t *testing.T) {
	// The client's own escapes stay byte for byte; only the added text is
	// encoded.
	prompt := "Use " + u("003c") + "tags" + u("003e") + `, \"quotes\", caf` + u("00e9") + ", " + u("d83d") + u("de00") +
		` and \/slashes.\nLine two` + u("2028") + "."
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		body     string
	}{
		{"responses", contract.ProtocolOpenAIResponses, `{"instructions":"` + prompt + `"}`},
		{"anthropic", contract.ProtocolAnthropicMessages, `{"system":"` + prompt + `"}`},
		{"chat", contract.ProtocolOpenAIChat, `{"messages":[{"role":"system","content":"` + prompt + `"}]}`},
		{"Droid line", contract.ProtocolAnthropicMessages, `{"system":"` + droid + `\n` + prompt + `"}`},
		{"Droid block", contract.ProtocolAnthropicMessages, `{"system":[{"type":"text","text":"` + droid + `\n` + prompt + `"}]}`},
		{"banner with escaped apostrophe", contract.ProtocolAnthropicMessages,
			`{"system":"You are Claude Code, Anthropic` + u("0027") + `s official CLI for Claude.\n` + prompt + `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := addBoth(t, test.protocol, test.body)
			start := strings.Index(got, "PRE")
			end := strings.LastIndex(got, "SUF")
			if start < 0 || end < start || !strings.Contains(got[start:end], prompt) {
				t.Fatalf("client prompt bytes changed or moved:\n%s", got)
			}
			if !ojson.Valid(got) {
				t.Fatalf("invalid JSON: %s", got)
			}
		})
	}
	// The escaped banner is still found as the identity lead.
	banner := "You are Claude Code, Anthropic" + u("0027") + "s official CLI for Claude."
	got := addBoth(t, contract.ProtocolAnthropicMessages, `{"system":"`+banner+`\nMain"}`)
	if want := `{"system":"` + banner + `\n\nPRE\nMain\n\nSUF"}`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// u spells the JSON escape of a UTF-16 code unit.
func u(hex string) string {
	return "\\" + "u" + hex
}

func TestRawOffset(t *testing.T) {
	for _, test := range []struct {
		content string
		decoded int
		want    int
		ok      bool
	}{
		{"abc", 2, 2, true},
		{u("0027") + "b", 1, 6, true},
		{`\nb`, 1, 2, true},
		{u("d83d") + u("de00") + "b", 4, 12, true},
		{u("d83d") + u("de00") + "b", 2, 0, false},
		{u("d800") + "b", 3, 6, true},
		{"caf\u00e9!", 5, 5, true},
	} {
		got, ok := rawOffset(test.content, test.decoded)
		if got != test.want || ok != test.ok {
			t.Errorf("rawOffset(%q, %d) = %d %t, want %d %t", test.content, test.decoded, got, ok, test.want, test.ok)
		}
	}
}
