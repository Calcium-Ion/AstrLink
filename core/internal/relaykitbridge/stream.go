package relaykitbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
)

type responseStream struct {
	from, to  contract.ProtocolID
	state     *relayconvert.ResponseStreamState
	meta      *convmeta.Values
	finalized bool
	closed    bool
	mu        sync.Mutex
}

var _ ResponseStream = (*responseStream)(nil)

func (e *Engine) NewResponseStream(_ context.Context, options StreamOptions) (ResponseStream, error) {
	from, err := relayFormat(options.From)
	if err != nil {
		return nil, err
	}
	to, err := relayFormat(options.To)
	if err != nil {
		return nil, err
	}
	upstream := firstNonEmpty(options.UpstreamModel, options.PublicModel)
	state, err := relayconvert.NewResponseStreamState(from, to, relayconvert.ResponseStreamOptions{
		ID: options.ID, Model: upstream, Created: options.Created, IncludeUsage: options.IncludeUsage,
		// sequence_number is required by the current Responses SSE contract;
		// RelayKit keeps it opt-in so legacy hosts are not changed by upgrades.
		EmitSequenceNumber: true,
	})
	if err != nil {
		return nil, err
	}
	return &responseStream{
		from: options.From, to: options.To, state: state,
		meta: newMeta(options.PublicModel, upstream, options.UpstreamModel != "", true),
	}, nil
}

func (s *responseStream) Convert(ctx context.Context, event ResponseEvent) ([]ResponseEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("response stream is closed")
	}
	if s.finalized {
		return nil, errors.New("response stream is finalized")
	}
	if event.Type == "done" || bytes.Equal(bytes.TrimSpace(event.Data), []byte("[DONE]")) {
		return nil, nil // finalization owns terminal conversion; [DONE] has no DTO.
	}
	response, err := decodeResponse(s.from, event.Data, true)
	if err != nil {
		return nil, err
	}
	applyStreamEventType(response, event.Type)
	results, err := relayconvert.ConvertStreamResponseChunk(ctx, s.meta, s.state, response)
	if err != nil {
		return nil, fmt.Errorf("convert stream response %s to %s: %w", s.from, s.to, err)
	}
	return responseEvents(s.to, results)
}

func applyStreamEventType(response any, eventType string) {
	if eventType == "" || eventType == "data" {
		return
	}
	switch response := response.(type) {
	case *dto.ResponsesStreamResponse:
		if response.Type == "" {
			response.Type = eventType
		}
	case *dto.ClaudeResponse:
		if response.Type == "" {
			response.Type = eventType
		}
	}
}

func (s *responseStream) Finalize(ctx context.Context) ([]ResponseEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("response stream is closed")
	}
	if s.finalized {
		return nil, nil
	}
	s.finalized = true
	results, err := relayconvert.FinalizeStreamResponse(ctx, s.meta, s.state)
	if err != nil {
		return nil, err
	}
	events, err := responseEvents(s.to, results)
	if err != nil {
		return nil, err
	}
	if s.to == contract.ProtocolOpenAIChat {
		events = append(events, ResponseEvent{Type: "done", Data: []byte("[DONE]")})
	}
	return events, nil
}

// Close deliberately does not finalize: an interrupted upstream must not be
// represented as a completed client response.
func (s *responseStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func decodeResponse(protocol contract.ProtocolID, body []byte, stream bool) (any, error) {
	var response any
	switch protocol {
	case contract.ProtocolOpenAIChat:
		if stream {
			response = &dto.ChatCompletionsStreamResponse{}
		} else {
			response = &dto.OpenAITextResponse{}
		}
	case contract.ProtocolOpenAIResponses:
		if stream {
			response = &dto.ResponsesStreamResponse{}
		} else {
			response = &dto.OpenAIResponsesResponse{}
		}
	case contract.ProtocolAnthropicMessages:
		response = &dto.ClaudeResponse{}
	case contract.ProtocolGoogleGenerateContent:
		response = &dto.GeminiChatResponse{}
	default:
		return nil, fmt.Errorf("unsupported RelayKit protocol %q", protocol)
	}
	if err := json.Unmarshal(body, response); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", protocol, err)
	}
	return response, nil
}

func responseEvents(protocol contract.ProtocolID, results []relayconvert.ResponseResult) ([]ResponseEvent, error) {
	events := make([]ResponseEvent, 0, len(results))
	for _, result := range results {
		for _, item := range flattenResponseValue(result.Value) {
			body, err := json.Marshal(item.value)
			if err != nil {
				return nil, fmt.Errorf("marshal stream response: %w", err)
			}
			eventType := item.eventType
			switch value := item.value.(type) {
			case *dto.ResponsesStreamResponse:
				if value.Type != "" {
					eventType = value.Type
				}
			case *dto.ClaudeResponse:
				eventType = value.Type
			}
			if protocol == contract.ProtocolOpenAIChat || protocol == contract.ProtocolGoogleGenerateContent {
				eventType = "data"
			}
			events = append(events, ResponseEvent{Type: eventType, Data: body})
		}
	}
	return events, nil
}

// streamValue is one wire payload extracted from a RelayKit stream result.
type streamValue struct {
	value     any
	eventType string
}

// flattenResponseValue normalises the shapes RelayKit stream converters
// return: slices of Claude events, Chat-to-Responses event wrappers (whose
// Payload is the actual Responses SSE body and whose Type is the SSE event
// name), and DTOs passed by value instead of by pointer.
func flattenResponseValue(value any) []streamValue {
	switch items := value.(type) {
	case nil:
		return nil
	case []*dto.ClaudeResponse:
		values := make([]streamValue, 0, len(items))
		for _, item := range items {
			if item != nil {
				values = append(values, streamValue{value: item})
			}
		}
		return values
	case relayconvert.ChatToResponsesStreamEvent:
		payload := items.Payload
		return []streamValue{{value: &payload, eventType: items.Type}}
	case *relayconvert.ChatToResponsesStreamEvent:
		if items == nil {
			return nil
		}
		payload := items.Payload
		return []streamValue{{value: &payload, eventType: items.Type}}
	case []relayconvert.ChatToResponsesStreamEvent:
		values := make([]streamValue, 0, len(items))
		for _, item := range items {
			payload := item.Payload
			values = append(values, streamValue{value: &payload, eventType: item.Type})
		}
		return values
	case dto.ChatCompletionsStreamResponse:
		return []streamValue{{value: &items}}
	case dto.ResponsesStreamResponse:
		return []streamValue{{value: &items}}
	case dto.ClaudeResponse:
		return []streamValue{{value: &items}}
	case dto.GeminiChatResponse:
		return []streamValue{{value: &items}}
	default:
		return []streamValue{{value: value}}
	}
}
