package contract

// ClientType is a best-effort label derived from inbound client headers. It is
// display metadata, never an authenticated identity or an upstream instruction.
type ClientType string

const (
	ClientUnknown    ClientType = "unknown"
	ClientCodex      ClientType = "codex"
	ClientClaudeCode ClientType = "claude_code"
	ClientCursor     ClientType = "cursor"
	ClientGrokCLI    ClientType = "grok_cli"
	ClientGeminiCLI  ClientType = "gemini_cli"
	ClientOpenCode   ClientType = "opencode"
	ClientOpenClaw   ClientType = "openclaw"
	ClientCline      ClientType = "cline"
	ClientPi         ClientType = "pi"
)

func (client ClientType) Valid() bool {
	switch client {
	case ClientUnknown, ClientCodex, ClientClaudeCode, ClientCursor, ClientGrokCLI,
		ClientGeminiCLI, ClientOpenCode, ClientOpenClaw, ClientCline, ClientPi:
		return true
	default:
		return false
	}
}
