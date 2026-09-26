package accountauth

import (
	"reflect"
	"runtime"
	"testing"
)

// The baseline identities are released values. Changing one is a deliberate
// identity update of the whole tuple, never an incidental refactor.
func TestBaselineIdentitySnapshot(t *testing.T) {
	claude := DefaultClaudeIdentity()
	wantClaude := ClientIdentity{
		UserAgent: "claude-cli/2.1.258 (external, cli)",
		Version:   "2.1.258",
		Headers: map[string]string{
			"X-Stainless-Lang":                          "js",
			"X-Stainless-Package-Version":               "0.94.0",
			"X-Stainless-Os":                            stainlessOS(runtime.GOOS),
			"X-Stainless-Arch":                          stainlessArch(runtime.GOARCH),
			"X-Stainless-Runtime":                       "node",
			"X-Stainless-Runtime-Version":               "v24.3.0",
			"X-Stainless-Retry-Count":                   "0",
			"X-Stainless-Timeout":                       "600",
			"X-App":                                     "cli",
			"Anthropic-Dangerous-Direct-Browser-Access": "true",
		},
	}
	if !reflect.DeepEqual(claude, wantClaude) {
		t.Fatalf("Claude baseline = %#v, want %#v", claude, wantClaude)
	}
	if DefaultClaudeUserAgent != wantClaude.UserAgent {
		t.Fatalf("DefaultClaudeUserAgent = %q", DefaultClaudeUserAgent)
	}

	codex := DefaultCodexIdentity()
	wantCodex := ClientIdentity{
		UserAgent: "codex-tui/0.155.1 (Ubuntu 22.4.0; x86_64) xterm-256color",
		Version:   "0.155.1",
		Headers:   map[string]string{"originator": "codex-tui"},
	}
	if !reflect.DeepEqual(codex, wantCodex) {
		t.Fatalf("Codex baseline = %#v, want %#v", codex, wantCodex)
	}
	if DefaultCodexOriginator != "codex-tui" || DefaultCodexModelsClientVersion != "0.155.1" ||
		CodexUserAgent("") != wantCodex.UserAgent {
		t.Fatalf("Codex baseline constants drifted: %q %q %q", DefaultCodexOriginator, DefaultCodexModelsClientVersion, CodexUserAgent(""))
	}
	if DefaultGrokCLIClientVersion != "0.2.101" || grokUserAgentProduct != "xai-grok-workspace" {
		t.Fatalf("Grok baseline = %q/%q", grokUserAgentProduct, DefaultGrokCLIClientVersion)
	}
}

func TestStainlessPlatformMapping(t *testing.T) {
	for goos, want := range map[string]string{"darwin": "MacOS", "windows": "Windows", "freebsd": "FreeBSD", "linux": "Linux", "plan9": "Linux"} {
		if got := stainlessOS(goos); got != want {
			t.Errorf("stainlessOS(%q) = %q, want %q", goos, got, want)
		}
	}
	for goarch, want := range map[string]string{"arm64": "arm64", "arm": "arm", "386": "x32", "amd64": "x64"} {
		if got := stainlessArch(goarch); got != want {
			t.Errorf("stainlessArch(%q) = %q, want %q", goarch, got, want)
		}
	}
}
