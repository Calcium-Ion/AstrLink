package ingress

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Codex's subscription Responses endpoint rejects server-side storage, a
// missing instructions field and max_output_tokens. Codex clients already
// comply, so only bodies produced by local conversion are normalized.
func prepareCodexConvertedRequest(request *http.Request) error {
	raw, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	if err != nil {
		return fmt.Errorf("Codex subscription request could not be prepared")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return fmt.Errorf("invalid Codex subscription request")
	}
	body["store"] = json.RawMessage("false")
	if instructions, ok := body["instructions"]; !ok || string(instructions) == "null" {
		body["instructions"] = json.RawMessage(`""`)
	}
	delete(body, "max_output_tokens")
	updated, err := json.Marshal(body)
	if err != nil {
		return err
	}
	replaceRecoveryRequestBody(request, updated)
	return nil
}
