package contract

import "strings"

// ModelNativeProtocol follows OpenCode's published endpoint tables. The
// catalog is fetched live; these rules select its wire protocol, not access.
func (kind ServiceKind) ModelNativeProtocol(model string) ProtocolID {
	if kind == ServiceKindCopilotSubscription {
		return copilotModelProtocol(strings.ToLower(model))
	}
	if kind == ServiceKindDroidSubscription {
		return droidModelProtocol(strings.ToLower(model))
	}
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

// droidModelProtocol follows the wire the Droid CLI's model registry sends
// each model on: Claude and MiniMax M2 on Messages (/api/llm/a), GPT and
// Grok on Responses, every other open model on Chat Completions (/api/llm/o).
func droidModelProtocol(model string) ProtocolID {
	switch {
	case strings.HasPrefix(model, "claude-"), strings.HasPrefix(model, "minimax-m2"):
		return ProtocolAnthropicMessages
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "grok-"):
		return ProtocolOpenAIResponses
	default:
		return ProtocolOpenAIChat
	}
}

// copilotModelProtocol mirrors OpenCode's choice among the endpoints a Copilot
// model lists: Messages when offered (Claude), then Responses (GPT-5, o-series
// and Codex models), otherwise Chat Completions.
func copilotModelProtocol(model string) ProtocolID {
	switch {
	case strings.HasPrefix(model, "claude-"):
		return ProtocolAnthropicMessages
	case strings.HasPrefix(model, "gpt-5"), strings.Contains(model, "codex"),
		strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"), strings.HasPrefix(model, "o4"):
		return ProtocolOpenAIResponses
	default:
		return ProtocolOpenAIChat
	}
}
