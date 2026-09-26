package accountauth

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// CodexIdentityPolicy defaults to a single upstream identity. Disabling
// enforcement preserves recognized client UAs, but still pairs originator and
// version with that UA. Arbitrary client originators are never forwarded.
// ClientVersion is the configured baseline release; Identity, when set, is
// the resolved identity that replaces that baseline.
type CodexIdentityPolicy struct {
	DisableEnforcement bool
	ClientVersion      string
	Identity           ClientIdentity
}

func (policy CodexIdentityPolicy) identity() ClientIdentity {
	if policy.Identity.UserAgent != "" {
		return policy.Identity
	}
	return codexIdentityAt(policy.ClientVersion)
}

// validCodexVersion uses sub2api's compatibility floor (0.144.0); older or
// invalid identities fall back as a complete tuple instead of mixing a new
// version with an old UA.
func validCodexVersion(version string) bool {
	return contract.ValidCodexClientVersion(version)
}

func codexVersionOrDefault(version string) string {
	version = strings.TrimSpace(version)
	if validCodexVersion(version) {
		return version
	}
	return DefaultCodexModelsClientVersion
}

func CodexUserAgent(version string) string {
	return codexIdentityAt(version).UserAgent
}

// ApplyCodexAuthIdentity is for token/device authorization requests. The
// inference-only version header is deliberately not sent to the auth service.
// The zero identity is the baseline.
func ApplyCodexAuthIdentity(header http.Header, identity ClientIdentity) {
	if header == nil {
		return
	}
	identity = codexIdentityOrDefault(identity)
	header.Set("originator", codexOriginator(identity))
	header.Set("User-Agent", identity.UserAgent)
}

func codexIdentityOrDefault(identity ClientIdentity) ClientIdentity {
	if identity.UserAgent == "" {
		return DefaultCodexIdentity()
	}
	return identity
}

func codexOriginator(identity ClientIdentity) string {
	if originator := identity.Headers["originator"]; originator != "" {
		return originator
	}
	return DefaultCodexOriginator
}

// ApplyCodexForwardHeaders shares credential and identity construction across
// HTTP inference, model discovery, and WebSocket handshakes.
func ApplyCodexForwardHeaders(header http.Header, tokens AccountTokens, clientHeaders http.Header, policy CodexIdentityPolicy) {
	ApplyCodexAPIHeaders(header, tokens, policy.identity())
	if header == nil {
		return
	}
	if policy.DisableEnforcement {
		if ua, name, version, ok := recognizedCodexClient(clientHeaders); ok {
			header.Set("User-Agent", ua)
			header.Set("originator", name)
			header.Set("version", version)
			return
		}
	}
	// Codex clients send no Stainless SDK headers, so another SDK's fingerprint
	// must not accompany the default identity.
	clearClientHeaderPrefix(header, clientHeaders, "x-stainless-")
}

// ApplyCodexOfficialForwardHeaders authenticates a recognized Codex CLI request
// without replacing its identity: it sets the OAuth Authorization, account
// binding and product sku, and keeps the client's own User-Agent, originator
// and version.
func ApplyCodexOfficialForwardHeaders(header http.Header, tokens AccountTokens) {
	if header == nil {
		return
	}
	header.Set("Authorization", "Bearer "+tokens.AccessToken)
	header.Del("ChatGPT-Account-ID")
	if tokens.AccountID != "" {
		header.Set("ChatGPT-Account-ID", tokens.AccountID)
	}
	header.Set("OAI-Product-Sku", "codex")
}

// recognizedCodexClient reports whether the caller's User-Agent is an official
// Codex client with a supported version, returning its UA, originator and version.
func recognizedCodexClient(clientHeaders http.Header) (ua, name, version string, ok bool) {
	ua = clientHeaders.Get("User-Agent")
	if len(ua) > 1024 {
		return "", "", "", false
	}
	for _, r := range ua {
		if r < 0x20 || r > 0x7e {
			return "", "", "", false
		}
	}
	ua = strings.TrimSpace(ua)
	name, rest, found := strings.Cut(ua, "/")
	if !found {
		return "", "", "", false
	}
	switch name {
	case "codex-tui", "codex_cli_rs", "codex_vscode", "codex_vscode_copilot",
		"codex_app", "codex_chatgpt_desktop", "codex_atlas", "codex_exec", "codex_sdk_ts":
	default:
		return "", "", "", false
	}
	version, _, _ = strings.Cut(rest, " ")
	if !validCodexVersion(version) {
		return "", "", "", false
	}
	return ua, name, version, true
}
