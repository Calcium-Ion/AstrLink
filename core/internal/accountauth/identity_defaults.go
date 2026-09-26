package accountauth

import "runtime"

// Baseline subscription client identities, used when a request must carry a
// client identity that the caller did not supply and none has been learned.
// Each provider's values describe one release: update them together, never a
// single field, or a request declares a version that the rest of its
// fingerprint never shipped with.
const (
	ClaudeUserAgentPrefix = "claude-cli/"
	claudeCLIVersion      = "2.1.258"
	// DefaultClaudeUserAgent mirrors the Claude Code CLI. api.anthropic.com
	// routes unknown agents (including Go's default) into a far stricter
	// rate-limit bucket on the OAuth usage and models endpoints.
	DefaultClaudeUserAgent = ClaudeUserAgentPrefix + claudeCLIVersion + " (external, cli)"

	DefaultCodexOriginator = "codex-tui"
	// DefaultCodexModelsClientVersion is the observed openai/codex ModelsClient
	// query (public CLI 0.155.1). GPT-6 Astra support landed in CLI 0.154.0.
	// The backend may hide models below a catalog minimum; keep this aligned
	// with stable releases: https://learn.chatgpt.com/docs/changelog
	DefaultCodexModelsClientVersion = "0.155.1"
	codexUserAgentSuffix            = " (Ubuntu 22.4.0; x86_64) xterm-256color"

	// DefaultGrokCLIClientVersion is the Grok CLI build reported to auth.x.ai
	// and the chat proxy. It matches the version new-api ships for the same
	// upstream and is only an identity hint, not a compatibility gate.
	DefaultGrokCLIClientVersion = "0.2.101"
	grokUserAgentProduct        = "xai-grok-workspace"
)

// ClientIdentity is one subscription client's upstream identity: its
// User-Agent, the version that User-Agent declares, and the headers the client
// sends alongside it.
type ClientIdentity struct {
	UserAgent string            `json:"user_agent"`
	Version   string            `json:"version"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// DefaultClaudeIdentity is Claude Code with the SDK headers of the same
// release. The operating system and architecture follow this host, like a
// Claude Code process running on it.
func DefaultClaudeIdentity() ClientIdentity {
	return ClientIdentity{
		UserAgent: DefaultClaudeUserAgent,
		Version:   claudeCLIVersion,
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
}

// DefaultCodexIdentity is the Codex TUI of DefaultCodexModelsClientVersion.
func DefaultCodexIdentity() ClientIdentity {
	return codexIdentityAt(DefaultCodexModelsClientVersion)
}

// codexIdentityAt is the baseline Codex TUI identity of one release; an
// invalid or unsupported version falls back to the default release.
func codexIdentityAt(version string) ClientIdentity {
	version = codexVersionOrDefault(version)
	return ClientIdentity{
		UserAgent: DefaultCodexOriginator + "/" + version + codexUserAgentSuffix,
		Version:   version,
		Headers:   map[string]string{"originator": DefaultCodexOriginator},
	}
}

func stainlessOS(goos string) string {
	switch goos {
	case "darwin":
		return "MacOS"
	case "windows":
		return "Windows"
	case "freebsd":
		return "FreeBSD"
	default:
		return "Linux"
	}
}

func stainlessArch(goarch string) string {
	switch goarch {
	case "arm64":
		return "arm64"
	case "arm":
		return "arm"
	case "386":
		return "x32"
	default:
		return "x64"
	}
}
