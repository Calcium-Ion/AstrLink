package relaykitbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// Engine adapts AstrLink's protocol-neutral boundary to RelayKit.
type Engine struct{}

var _ ConversionEngine = (*Engine)(nil)

func NewEngine() *Engine {
	InstallHostHooks()
	return &Engine{}
}

func (e *Engine) Version() string { return relayKitVersion() }

func (e *Engine) Edges() []contract.ConversionEdge { return copyEdges() }

func (e *Engine) ConvertRequest(ctx context.Context, in ConvertRequestInput) (ConvertRequestOutput, error) {
	target, err := relayFormat(in.To)
	if err != nil {
		return ConvertRequestOutput{}, err
	}
	request, model, streamed, err := decodeRequest(in.From, in.Body)
	if err != nil {
		return ConvertRequestOutput{}, err
	}
	publicModel := firstNonEmpty(in.PublicModel, model)
	upstreamModel := firstNonEmpty(in.UpstreamModel, publicModel)
	upstreamModel, reasoningState, err := splitReasoningSuffix(target, upstreamModel)
	if err != nil {
		return ConvertRequestOutput{}, fmt.Errorf("convert request %s to %s: %w", in.From, in.To, err)
	}
	setRequestModel(request, upstreamModel)
	meta := newMeta(publicModel, upstreamModel, in.UpstreamModel != "", in.Streaming || streamed)
	meta.ReasoningConversion = reasoningState
	result, err := relayconvert.ConvertRequest(ctx, meta, target, request)
	if err != nil {
		return ConvertRequestOutput{}, fmt.Errorf("convert request %s to %s: %w", in.From, in.To, err)
	}
	body, err := json.Marshal(result.Value)
	if err != nil {
		return ConvertRequestOutput{}, fmt.Errorf("marshal converted request: %w", err)
	}
	return ConvertRequestOutput{ContentType: "application/json", Body: body}, nil
}

func (e *Engine) ConvertResponse(ctx context.Context, in ConvertResponseInput) (ConvertResponseOutput, error) {
	target, err := relayFormat(in.To)
	if err != nil {
		return ConvertResponseOutput{}, err
	}
	response, err := decodeResponse(in.From, in.Body, false)
	if err != nil {
		return ConvertResponseOutput{}, err
	}
	meta := newMeta(in.PublicModel, in.PublicModel, false, false)
	result, err := relayconvert.ConvertResponse(ctx, meta, target, response)
	if err != nil {
		return ConvertResponseOutput{}, fmt.Errorf("convert response %s to %s: %w", in.From, in.To, err)
	}
	restoreResponseModel(result.Value, in.PublicModel)
	body, err := json.Marshal(result.Value)
	if err != nil {
		return ConvertResponseOutput{}, fmt.Errorf("marshal converted response: %w", err)
	}
	status := in.StatusCode
	if status == 0 {
		status = 200
	}
	return ConvertResponseOutput{StatusCode: status, ContentType: "application/json", Body: body}, nil
}

func newMeta(publicModel, upstreamModel string, override, streaming bool) *convmeta.Values {
	return &convmeta.Values{
		OriginModelName:     publicModel,
		UpstreamModelName:   upstreamModel,
		ChannelMetaAttached: override,
		IsStream:            streaming,
		Options: &convmeta.Options{Claude: convmeta.ClaudeOptions{
			DefaultMaxTokens: func(string) int { return 8192 },
		}},
	}
}

// splitReasoningSuffix keeps the reasoning-suffix behaviour RelayKit applied
// itself before v0.2.0: a "-thinking" / "-nothinking" / "-thinking-<budget>" /
// effort tail ("-high", "-low", ...) on a known Claude or Gemini upstream model
// name is turned into reasoning intent and the base name is sent upstream.
// RelayKit now leaves suffix parsing to the host, so the intent is handed over
// through convmeta.Values.ReasoningConversion. Other targets keep the model
// name untouched: OpenAI-compatible proxies define their own suffix vocabulary
// and must see the configured name as-is.
func splitReasoningSuffix(target types.RelayFormat, model string) (string, *dto.ReasoningConversionState, error) {
	if target != types.RelayFormatClaude && target != types.RelayFormatGemini {
		return model, nil, nil
	}
	baseModel, intent, ok, err := reasoning.ParseKnownProviderModelSuffix(model, true)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return model, nil, nil
	}
	return baseModel, reasoning.StateFromIntent(intent), nil
}

func decodeRequest(protocol contract.ProtocolID, body []byte) (any, string, bool, error) {
	var request any
	switch protocol {
	case contract.ProtocolOpenAIChat:
		request = &dto.GeneralOpenAIRequest{}
	case contract.ProtocolOpenAIResponses:
		request = &dto.OpenAIResponsesRequest{}
	case contract.ProtocolAnthropicMessages:
		request = &dto.ClaudeRequest{}
	case contract.ProtocolGoogleGenerateContent:
		request = &dto.GeminiChatRequest{}
	default:
		return nil, "", false, fmt.Errorf("unsupported RelayKit protocol %q", protocol)
	}
	if err := json.Unmarshal(body, request); err != nil {
		return nil, "", false, fmt.Errorf("decode %s request: %w", protocol, err)
	}
	switch r := request.(type) {
	case *dto.GeneralOpenAIRequest:
		return r, r.Model, r.Stream != nil && *r.Stream, nil
	case *dto.OpenAIResponsesRequest:
		return r, r.Model, r.Stream != nil && *r.Stream, nil
	case *dto.ClaudeRequest:
		return r, r.Model, r.Stream != nil && *r.Stream, nil
	case *dto.GeminiChatRequest:
		return r, "", false, nil
	default:
		panic("unreachable")
	}
}

func setRequestModel(request any, model string) {
	if model == "" {
		return
	}
	switch r := request.(type) {
	case *dto.GeneralOpenAIRequest:
		r.Model = model
	case *dto.OpenAIResponsesRequest:
		r.Model = model
	case *dto.ClaudeRequest:
		r.Model = model
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func restoreResponseModel(value any, model string) {
	if model == "" {
		return
	}
	switch response := value.(type) {
	case *dto.OpenAITextResponse:
		response.Model = model
	case *dto.ChatCompletionsStreamResponse:
		response.Model = model
	case *dto.OpenAIResponsesResponse:
		response.Model = model
	case *dto.ResponsesStreamResponse:
		if response.Response != nil {
			response.Response.Model = model
		}
	case *dto.ClaudeResponse:
		response.Model = model
		if response.Message != nil {
			response.Message.Model = model
		}
	case []*dto.ClaudeResponse:
		for _, item := range response {
			item.Model = model
			if item.Message != nil {
				item.Message.Model = model
			}
		}
	}
}
