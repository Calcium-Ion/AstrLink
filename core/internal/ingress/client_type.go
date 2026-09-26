package ingress

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// detectClientType only reads the inbound envelope. Explicit application names
// take precedence over User-Agent because some clients use provider SDK UAs.
// Neither raw headers nor versions are persisted, and generic SDKs stay unknown.
func detectClientType(headers http.Header) contract.ClientType {
	for _, header := range []string{"X-Title", "X-OpenRouter-Title", "X-OpenCode-Client", "X-Billing-Invoke-Origin", "Originator"} {
		if client := clientProduct(normalizedClientHeader(headers.Get(header))); client != "" {
			return client
		}
	}
	ua := normalizedClientHeader(headers.Get("User-Agent"))
	for _, token := range strings.FieldsFunc(ua, func(r rune) bool {
		return r == ' ' || r == '(' || r == ')' || r == ';'
	}) {
		product, _, _ := strings.Cut(token, "/")
		if client := clientProduct(product); client != "" {
			return client
		}
	}
	return contract.ClientUnknown
}

func normalizedClientHeader(value string) string {
	if len(value) > 1024 {
		return ""
	}
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return ""
		}
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func clientProduct(product string) contract.ClientType {
	switch product {
	case "codex", "codex-tui", "codex_cli_rs", "codex_vscode", "codex_vscode_copilot",
		"codex_app", "codex_chatgpt_desktop", "codex_atlas", "codex_exec", "codex_sdk_ts":
		return contract.ClientCodex
	case "claude-code", "claude code", "claude-cli":
		return contract.ClientClaudeCode
	case "cursor", "cursor-agent":
		return contract.ClientCursor
	case "grok-cli", "xai-grok-workspace":
		return contract.ClientGrokCLI
	case "gemini-cli", "geminicli", "geminicli-a2a-server":
		return contract.ClientGeminiCLI
	case "opencode":
		return contract.ClientOpenCode
	case "openclaw":
		return contract.ClientOpenClaw
	case "cline":
		return contract.ClientCline
	case "pi", "pi-coding-agent":
		return contract.ClientPi
	default:
		return ""
	}
}
