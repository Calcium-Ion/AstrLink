package relaykitbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Chat Completions defines image_url as an object with a url field, while a
// Responses input_image carries image_url as a string. Regression for the
// Responses-to-Chat edge: a string image_url must reach the Chat upstream
// wrapped as {"url": ...}, not as a bare string strict upstreams reject.
func TestResponsesToChatWrapsStringImageURLAsObject(t *testing.T) {
	body := `{
		"model": "vision-model",
		"stream": true,
		"input": [
			{
				"type": "message",
				"role": "user",
				"content": [
					{"type": "input_text", "text": "describe this image"},
					{"type": "input_image", "image_url": "data:image/png;base64,iVBORw0KGgo="}
				]
			}
		]
	}`
	engine := NewEngine()
	out, err := engine.ConvertRequest(context.Background(), ConvertRequestInput{
		From: contract.ProtocolOpenAIResponses, To: contract.ProtocolOpenAIChat,
		ContentType: "application/json", Body: []byte(body),
		PublicModel: "vision-model", UpstreamModel: "vision-model", Streaming: true,
	})
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}

	var converted struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out.Body, &converted); err != nil {
		t.Fatalf("converted body is not JSON: %v", err)
	}
	if len(converted.Messages) == 0 {
		t.Fatalf("converted body has no messages: %s", out.Body)
	}

	var found bool
	var rawParts []map[string]any
	if err := json.Unmarshal(converted.Messages[0].Content, &rawParts); err == nil {
		for _, part := range rawParts {
			if strings.TrimSpace(part["type"].(string)) != "image_url" {
				continue
			}
			found = true
			imageURL, ok := part["image_url"].(map[string]any)
			if !ok {
				t.Fatalf("image_url should be an object, got %T: %v", part["image_url"], part["image_url"])
			}
			if url, _ := imageURL["url"].(string); !strings.HasPrefix(url, "data:image/png;base64,") {
				t.Fatalf("image_url.url should keep the data URL, got %q", url)
			}
		}
	}
	if !found {
		t.Fatalf("converted chat body carries no image part: %s", out.Body)
	}
}
