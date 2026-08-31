package autotext

import (
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestExtractLastUserTextCurrentUserV3(t *testing.T) {
	tests := []struct {
		name     string
		protocol contract.ProtocolID
		body     string
		want     string
	}{
		{
			name:     "responses last user only",
			protocol: contract.ProtocolOpenAIResponses,
			body:     `{"instructions":"ignore","input":[{"role":"user","content":"first"},{"role":"assistant","content":"no"},{"role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_text","text":"world"}]}]}`,
			want:     "hello\nworld",
		},
		{
			name:     "chat skips system and tools",
			protocol: contract.ProtocolOpenAIChat,
			body:     `{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"old"},{"role":"user","content":[{"type":"text","text":"keep"},{"type":"image_url","image_url":{"url":"x"}},{"type":"text","text":"  "}]}]}`,
			want:     "keep",
		},
		{
			name:     "anthropic excludes top-level system",
			protocol: contract.ProtocolAnthropicMessages,
			body:     `{"system":"sys","messages":[{"role":"user","content":[{"type":"text","text":"plain"}]}]}`,
			want:     "plain",
		},
		{
			name:     "gemini skips inline data",
			protocol: contract.ProtocolGoogleGenerateContent,
			body:     `{"systemInstruction":{"parts":[{"text":"sys"}]},"contents":[{"role":"user","parts":[{"text":"ask"},{"inline_data":{"mime_type":"image/png"}}]}]}`,
			want:     "ask",
		},
		{
			name:     "empty user is fallback",
			protocol: contract.ProtocolOpenAIChat,
			body:     `{"messages":[{"role":"user","content":"   "}]}`,
			want:     "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ExtractLastUserText(test.protocol, []byte(test.body))
			if got != test.want {
				t.Fatalf("got %q want %q", got, test.want)
			}
		})
	}
}
