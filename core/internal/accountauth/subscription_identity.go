package accountauth

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// recognizedClientIdentity accepts only a versioned provider client UA. Unknown
// clients fall back to the provider default even when enforcement is disabled.
func recognizedClientIdentity(headers http.Header, product string) (ua, version string) {
	ua = headers.Get("User-Agent")
	if len(ua) > 1024 {
		return "", ""
	}
	for _, r := range ua {
		if r < 0x20 || r > 0x7e {
			return "", ""
		}
	}
	ua = strings.TrimSpace(ua)
	name, rest, found := strings.Cut(ua, "/")
	if !found || name != product {
		return "", ""
	}
	version, _, _ = strings.Cut(rest, " ")
	if !contract.ValidClientVersion(version) {
		return "", ""
	}
	return ua, version
}

// ApplyClaudeForwardHeaders authenticates a forwarded Claude request with
// identity, the resolved Claude Code identity. Without enforcement a
// recognized Claude Code client keeps its own User-Agent.
func ApplyClaudeForwardHeaders(header http.Header, tokens AccountTokens, clientHeaders http.Header, identity ClientIdentity, enforce bool) {
	ApplyClaudeAPIHeaders(header, tokens, identity)
	if header == nil {
		return
	}
	clientUA := ""
	if !enforce {
		clientUA, _ = recognizedClientIdentity(clientHeaders, "claude-cli")
	}
	if clientUA != "" {
		header.Set("User-Agent", clientUA)
	} else {
		// The default identity carries the SDK fingerprint of the same
		// Claude Code release; a recognized client keeps its own.
		applyClaudeCodeClientHeaders(header, clientHeaders, identity)
	}
	// Feature betas are independent of identity enforcement. Keep all client
	// values, with required OAuth betas added exactly once.
	values := append([]string{header.Get("Anthropic-Beta")}, clientHeaders.Values("Anthropic-Beta")...)
	seen := make(map[string]bool)
	var betas []string
	for _, value := range values {
		for _, beta := range strings.Split(value, ",") {
			beta = strings.TrimSpace(beta)
			if beta != "" && !seen[beta] {
				seen[beta] = true
				betas = append(betas, beta)
			}
		}
	}
	header.Set("Anthropic-Beta", strings.Join(betas, ","))
}

// clearClientHeaderPrefix deletes matching client headers through nil overlay
// values.
func clearClientHeaderPrefix(header, clientHeaders http.Header, prefix string) {
	for name := range clientHeaders {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			header[http.CanonicalHeaderKey(name)] = nil
		}
	}
}

// ApplyClaudeOfficialForwardHeaders authenticates a recognized Claude Code
// request without replacing its identity: it sets the OAuth Authorization, adds
// Anthropic-Version only when the client omitted it, and appends the required
// OAuth betas after the client's own list without reordering it. The client's
// User-Agent and X-Stainless-* fingerprint pass through untouched.
func ApplyClaudeOfficialForwardHeaders(header http.Header, tokens AccountTokens, clientHeaders http.Header) {
	if header == nil {
		return
	}
	header.Set("Authorization", "Bearer "+tokens.AccessToken)
	if strings.TrimSpace(clientHeaders.Get("Anthropic-Version")) == "" {
		header.Set("Anthropic-Version", "2023-06-01")
	}
	betas := splitAnthropicBetas(clientHeaders.Values("Anthropic-Beta"))
	seen := make(map[string]bool, len(betas))
	for _, beta := range betas {
		seen[beta] = true
	}
	added := false
	for _, required := range []string{"claude-code-20250219", "oauth-2025-04-20"} {
		if !seen[required] {
			betas = append(betas, required)
			seen[required] = true
			added = true
		}
	}
	// Only rewrite the header when a beta was missing, so a compliant client's
	// exact value passes through.
	if added {
		header.Set("Anthropic-Beta", strings.Join(betas, ","))
	}
}

// grokIdentityFromHeaders recognizes the shell's product even behind a host
// product (for example Lody), and the older workspace tool client. The explicit
// version header wins over the embedded shell version.
func grokIdentityFromHeaders(header http.Header) (ClientIdentity, bool) {
	ua := header.Get("User-Agent")
	if len(header.Values("User-Agent")) != 1 || len(ua) > maxLearnedUserAgent || !learnedText(ua) {
		return ClientIdentity{}, false
	}
	fields := strings.Fields(ua)
	for _, product := range []string{grokUserAgentProduct, "xai-grok-workspace"} {
		for index, token := range fields {
			name, version, found := strings.Cut(token, "/")
			if !found || name != product || !contract.ValidClientVersion(version) {
				continue
			}
			if explicit := grokHeaderVersion(header); explicit != "" {
				version = explicit
				fields[index] = product + "/" + version
			}
			return ClientIdentity{UserAgent: strings.Join(fields, " "), Version: version}, true
		}
	}
	return ClientIdentity{}, false
}

func grokHeaderVersion(header http.Header) string {
	if len(header.Values("X-Grok-Client-Version")) == 1 {
		version := strings.TrimSpace(header.Get("X-Grok-Client-Version"))
		if contract.ValidClientVersion(version) && learnedText(version) {
			return version
		}
	}
	return ""
}

func ApplyGrokForwardHeaders(header http.Header, tokens AccountTokens, clientHeaders http.Header, identity ClientIdentity, enforce bool) {
	if header == nil {
		return
	}
	identity = grokIdentityAt(identity.Version)
	client, recognized := grokIdentityFromHeaders(clientHeaders)
	version := grokHeaderVersion(clientHeaders)
	if version == "" && recognized {
		version = client.Version
	}
	identity = withVersionFloor(identity, version)
	ApplyGrokAPIHeaders(header, tokens, identity.Version)
	if !enforce && recognized {
		header.Set("User-Agent", client.UserAgent)
		header.Set("X-Grok-Client-Version", client.Version)
		identifier := strings.TrimSpace(clientHeaders.Get("X-Grok-Client-Identifier"))
		if len(clientHeaders.Values("X-Grok-Client-Identifier")) == 1 && len(identifier) <= maxLearnedValue && identifier != "" && learnedText(identifier) {
			header.Set("X-Grok-Client-Identifier", identifier)
		}
	}
}
