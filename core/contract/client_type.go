package contract

// ClientType is a best-effort label derived from inbound client headers. It is
// display metadata, never an authenticated identity or an upstream instruction.
type ClientType string

const (
	ClientUnknown         ClientType = "unknown"
	ClientCodex           ClientType = "codex"
	ClientClaudeCode      ClientType = "claude_code"
	ClientCursor          ClientType = "cursor"
	ClientGrokCLI         ClientType = "grok_cli"
	ClientGeminiCLI       ClientType = "gemini_cli"
	ClientOpenCode        ClientType = "opencode"
	ClientOpenClaw        ClientType = "openclaw"
	ClientCline           ClientType = "cline"
	ClientPi              ClientType = "pi"
	ClientDeepSeekHarness ClientType = "deepseek_harness"
	ClientCodewhale       ClientType = "codewhale"
	ClientReasonix        ClientType = "reasonix"
	ClientQwenCode        ClientType = "qwen_code"
	ClientKimiCode        ClientType = "kimi_code"
	ClientCodeBuddy       ClientType = "codebuddy"
	ClientCopilot         ClientType = "copilot"
	ClientDroid           ClientType = "droid"
	ClientCrush           ClientType = "crush"
	ClientKiloCode        ClientType = "kilo_code"
	ClientRooCode         ClientType = "roo_code"
	ClientMistralVibe     ClientType = "mistral_vibe"
	ClientZed             ClientType = "zed"
	ClientCherryStudio    ClientType = "cherry_studio"
)

func (client ClientType) Valid() bool {
	switch client {
	case ClientUnknown, ClientCodex, ClientClaudeCode, ClientCursor, ClientGrokCLI,
		ClientGeminiCLI, ClientOpenCode, ClientOpenClaw, ClientCline, ClientPi,
		ClientDeepSeekHarness, ClientCodewhale, ClientReasonix, ClientQwenCode,
		ClientKimiCode, ClientCodeBuddy, ClientCopilot, ClientDroid, ClientCrush,
		ClientKiloCode, ClientRooCode, ClientMistralVibe, ClientZed, ClientCherryStudio:
		return true
	default:
		return false
	}
}
