package servicetest

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
	"strings"
)

func (tester *Tester) TestIntelligence(ctx context.Context, service contract.Service, input contract.IntelligenceRequest) contract.ServiceTestResult {
	if err := input.Validate(service); err != nil {
		return contract.ServiceTestResult{ServiceID: service.ID, Model: input.Model, ErrorCode: "invalid_test", Message: err.Error()}
	}
	return tester.test(ctx, service, input.ServiceTestRequest, &input)
}

func intelligencePayload(kind contract.ServiceKind, input contract.IntelligenceRequest) (string, map[string]any) {
	path, body := testPayload(kind, input.ServiceTestRequest)
	delete(body, "instructions")
	// Discard the small connection-test budgets. Let the provider choose its
	// output limit for evaluation requests, including reasoning tokens.
	delete(body, "max_tokens")
	delete(body, "max_completion_tokens")
	delete(body, "max_output_tokens")
	delete(body, "generationConfig")
	switch input.Protocol {
	case contract.ProtocolOpenAIResponses:
		content := []any{map[string]string{"type": "input_text", "text": input.Prompt}}
		for _, img := range input.Images {
			content = append(content, map[string]string{"type": "input_image", "image_url": img})
		}
		body["input"] = []any{map[string]any{"role": "user", "content": content}}
		if kind == contract.ServiceKindCodexSubscription {
			body["instructions"] = "Answer the user's question."
		}
	case contract.ProtocolOpenAIChat:
		content := []any{map[string]string{"type": "text", "text": input.Prompt}}
		for _, img := range input.Images {
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": img}})
		}
		body["messages"] = []any{map[string]any{"role": "user", "content": content}}
	case contract.ProtocolAnthropicMessages:
		content := []any{map[string]string{"type": "text", "text": input.Prompt}}
		for _, img := range input.Images {
			content = append(content, map[string]any{"type": "image", "source": map[string]string{"type": "base64", "media_type": "image/png", "data": strings.TrimPrefix(img, "data:image/png;base64,")}})
		}
		body["messages"] = []any{map[string]any{"role": "user", "content": content}}
		// Anthropic Messages requires max_tokens; preserve its existing budget.
		body["max_tokens"] = 16384
	case contract.ProtocolGoogleGenerateContent:
		parts := []any{map[string]string{"text": input.Prompt}}
		for _, img := range input.Images {
			parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": "image/png", "data": strings.TrimPrefix(img, "data:image/png;base64,")}})
		}
		body["contents"] = []any{map[string]any{"role": "user", "parts": parts}}
	}
	return path, body
}
