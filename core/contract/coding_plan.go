package contract

import "strings"

// ModelNativeProtocol follows OpenCode's published endpoint tables. The
// catalog is fetched live; these rules select its wire protocol, not access.
func (kind ServiceKind) ModelNativeProtocol(model string) ProtocolID {
	if kind != ServiceKindOpenCodeGo && kind != ServiceKindOpenCodeZen {
		return ""
	}
	model = strings.ToLower(model)
	if strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "grok-") || strings.HasPrefix(model, "muse-spark-") {
		return ProtocolOpenAIResponses
	}
	if strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "qwen") || model == "union-alpha" ||
		(kind == ServiceKindOpenCodeGo && strings.HasPrefix(model, "minimax-")) {
		return ProtocolAnthropicMessages
	}
	return ProtocolOpenAIChat
}
