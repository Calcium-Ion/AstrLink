package ingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const claudeCodeBanner = "You are Claude Code, Anthropic's official CLI for Claude."

// Claude's subscription endpoint expects this compatibility banner. Existing
// Claude Code requests keep their exact body; other system blocks remain intact.
func prepareClaudeSubscriptionRequest(request *http.Request) error {
	raw, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	if err != nil {
		return fmt.Errorf("Claude subscription request could not be prepared")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return fmt.Errorf("invalid Claude subscription request")
	}
	var blocks []json.RawMessage
	if system := body["system"]; len(system) > 0 && !bytes.Equal(system, []byte("null")) {
		var text string
		if json.Unmarshal(system, &text) == nil {
			if text == claudeCodeBanner {
				replaceRecoveryRequestBody(request, raw)
				return nil
			}
			block, _ := json.Marshal(map[string]string{"type": "text", "text": text})
			blocks = append(blocks, block)
		} else if json.Unmarshal(system, &blocks) != nil {
			return fmt.Errorf("invalid Claude subscription system prompt")
		}
	}
	for _, block := range blocks {
		var text struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(block, &text) == nil && text.Type == "text" && text.Text == claudeCodeBanner {
			replaceRecoveryRequestBody(request, raw)
			return nil
		}
	}
	banner, _ := json.Marshal(map[string]string{"type": "text", "text": claudeCodeBanner})
	body["system"], err = json.Marshal(append([]json.RawMessage{banner}, blocks...))
	if err != nil {
		return err
	}
	updated, err := json.Marshal(body)
	if err != nil {
		return err
	}
	replaceRecoveryRequestBody(request, updated)
	return nil
}
