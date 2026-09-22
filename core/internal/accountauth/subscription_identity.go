package accountauth

import (
	"net/http"
	"regexp"
	"strings"
)

var clientVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

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
	if len(version) > 64 || !clientVersionPattern.MatchString(version) {
		return "", ""
	}
	return ua, version
}

func ApplyClaudeForwardHeaders(header http.Header, tokens AccountTokens, clientHeaders http.Header, enforce bool) {
	ApplyClaudeAPIHeaders(header, tokens)
	if header == nil {
		return
	}
	if !enforce {
		if ua, _ := recognizedClientIdentity(clientHeaders, "claude-cli"); ua != "" {
			header.Set("User-Agent", ua)
		}
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

func ApplyGrokForwardHeaders(header http.Header, tokens AccountTokens, clientHeaders http.Header, enforce bool) {
	ApplyGrokAPIHeaders(header, tokens, "")
	if header == nil || enforce {
		return
	}
	if ua, version := recognizedClientIdentity(clientHeaders, "xai-grok-workspace"); ua != "" {
		header.Set("User-Agent", ua)
		header.Set("X-Grok-Client-Version", version)
	}
}
