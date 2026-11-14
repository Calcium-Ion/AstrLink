// Package clients holds the clients AstrLink writes config files for, one
// file each, all implementing clientconfig.Spec and their files
// clientconfig.File. The shared code (records, confirmation, backup and
// restore, previews, batches, port sync) reaches clients only through
// these two.
//
// To add a client, write `<id>.go` with an exported Client for its Spec,
// and add it to the list below, whose order is the dialog's.
package clients

import cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"

func init() {
	cc.Register(
		Claude,
		Codex,
		Gemini,
		OpenCode,
		OpenClaw,
		Pi,
		WorkBuddy,
		CodeBuddy,
		DeepSeekHarness,
		QwenCode,
		KimiCode,
		GrokBuild,
		Droid,
		CopilotCLI,
		VSCodeCopilot,
		KiloCode,
		Crush,
		Cline,
		Zed,
		Goose,
		MistralVibe,
		CodeWhale,
		Reasonix,
		DimAgent,
		ZCode,
		MiniMaxCode,
		StepCode,
		Aider,
		Chatbox,
		QoderCLI,
		TraeCLI,
	)
}
