package accountauth

import (
	"net/http"
	"strconv"
	"strings"
)

const (
	DefaultCodexOriginator = "codex-tui"
	// Keep the canonical UA shape and version in one place, as in sub2api.
	codexUserAgentSuffix = " (Ubuntu 22.4.0; x86_64) xterm-256color"
)

// CodexIdentityPolicy defaults to a single upstream identity. Disabling
// enforcement preserves recognized client UAs, but still pairs originator and
// version with that UA. Arbitrary client originators are never forwarded.
type CodexIdentityPolicy struct {
	DisableEnforcement bool
	ClientVersion      string
}

func validCodexVersion(version string) bool {
	if len(version) > 64 || !clientVersionPattern.MatchString(version) {
		return false
	}
	base := strings.FieldsFunc(version, func(r rune) bool { return r == '-' || r == '+' })[0]
	parts := strings.Split(base, ".")
	var numbers [3]uint64
	for i, part := range parts {
		number, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return false
		}
		numbers[i] = number
	}
	// Use sub2api's compatibility floor (0.144.0); older or invalid identities
	// fall back as a complete tuple instead of mixing a new version with an old UA.
	return numbers[0] > 0 || numbers[1] > 144 ||
		(numbers[1] == 144 && (numbers[2] > 0 || !strings.Contains(strings.SplitN(version, "+", 2)[0], "-")))
}

func codexVersionOrDefault(version string) string {
	version = strings.TrimSpace(version)
	if validCodexVersion(version) {
		return version
	}
	return DefaultCodexModelsClientVersion
}

func CodexUserAgent(version string) string {
	return DefaultCodexOriginator + "/" + codexVersionOrDefault(version) + codexUserAgentSuffix
}

// ApplyCodexAuthIdentity is for token/device authorization requests. The
// inference-only version header is deliberately not sent to the auth service.
func ApplyCodexAuthIdentity(header http.Header, version string) {
	if header == nil {
		return
	}
	header.Set("originator", DefaultCodexOriginator)
	header.Set("User-Agent", CodexUserAgent(version))
}

// ApplyCodexForwardHeaders shares credential and identity construction across
// HTTP inference, model discovery, and WebSocket handshakes.
func ApplyCodexForwardHeaders(header http.Header, tokens AccountTokens, clientHeaders http.Header, policy CodexIdentityPolicy) {
	ApplyCodexAPIHeaders(header, tokens, policy.ClientVersion)
	if !policy.DisableEnforcement || header == nil {
		return
	}
	ua := clientHeaders.Get("User-Agent")
	if len(ua) > 1024 {
		return
	}
	for _, r := range ua {
		if r < 0x20 || r > 0x7e {
			return
		}
	}
	ua = strings.TrimSpace(ua)
	name, rest, found := strings.Cut(ua, "/")
	if !found {
		return
	}
	switch name {
	case "codex-tui", "codex_cli_rs", "codex_vscode", "codex_vscode_copilot",
		"codex_app", "codex_chatgpt_desktop", "codex_atlas", "codex_exec", "codex_sdk_ts":
	default:
		return
	}
	version, _, _ := strings.Cut(rest, " ")
	if !validCodexVersion(version) {
		return
	}
	header.Set("User-Agent", ua)
	header.Set("originator", name)
	header.Set("version", version)
}
