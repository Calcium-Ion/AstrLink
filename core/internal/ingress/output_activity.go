package ingress

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/tidwall/gjson"
)

// outputPart is what one piece of a response says about its output: the
// part it belongs to and the tool calls it starts, in order. kind is empty
// when it carries no output, such as metadata, heartbeats or usage.
type outputPart struct {
	kind  contract.OutputKind
	tools []string
}

func (part *outputPart) note(kind contract.OutputKind, present bool) {
	if present {
		part.kind = kind
	}
}

func (part *outputPart) tool(name string) {
	part.kind = contract.OutputKindTool
	if name != "" {
		part.tools = append(part.tools, name)
	}
}

// classifyStreamPart reads one decoded stream event. Several parts in one
// event leave the last one as the event's part.
func classifyStreamPart(protocol contract.ProtocolID, payload []byte) outputPart {
	doc := gjson.ParseBytes(payload)
	nonempty := func(value gjson.Result) bool { return value.Type == gjson.String && value.Str != "" }
	var part outputPart
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		switch eventType := doc.Get("type").Str; {
		case eventType == "response.output_item.added":
			responsesItemPart(&part, doc.Get("item"))
		case eventType == "response.output_text.delta", eventType == "response.refusal.delta":
			part.note(contract.OutputKindText, nonempty(doc.Get("delta")))
		case eventType == "response.function_call_arguments.delta", eventType == "response.custom_tool_call_input.delta":
			part.note(contract.OutputKindTool, nonempty(doc.Get("delta")))
		case strings.HasPrefix(eventType, "response.reasoning") && strings.HasSuffix(eventType, ".delta"):
			part.note(contract.OutputKindReasoning, nonempty(doc.Get("delta")))
		}
	case contract.ProtocolOpenAIChat, contract.ProtocolOpenAICompletions:
		for _, choice := range doc.Get("choices").Array() {
			part.note(contract.OutputKindReasoning, nonempty(choice.Get("delta.reasoning_content")) || nonempty(choice.Get("delta.reasoning")))
			part.note(contract.OutputKindText, nonempty(choice.Get("delta.content")) || nonempty(choice.Get("delta.refusal")) || nonempty(choice.Get("text")))
			if name := choice.Get("delta.function_call.name").Str; name != "" {
				part.tool(name)
			}
			for _, call := range choice.Get("delta.tool_calls").Array() {
				if name := call.Get("function.name").Str; name != "" {
					part.tool(name)
				} else {
					part.note(contract.OutputKindTool, nonempty(call.Get("function.arguments")))
				}
			}
		}
	case contract.ProtocolAnthropicMessages:
		switch doc.Get("type").Str {
		case "content_block_start":
			anthropicBlockPart(&part, doc.Get("content_block"))
		case "content_block_delta":
			switch doc.Get("delta.type").Str {
			case "thinking_delta":
				part.note(contract.OutputKindReasoning, true)
			case "text_delta":
				part.note(contract.OutputKindText, true)
			case "input_json_delta":
				part.note(contract.OutputKindTool, true)
			}
		}
	case contract.ProtocolGoogleGenerateContent:
		for _, candidate := range doc.Get("candidates").Array() {
			geminiParts(&part, candidate.Get("content.parts"))
		}
	}
	return part
}

// outputFromBody reads a whole non-streaming response. Nil when it holds no
// output this gateway recognizes.
func outputFromBody(protocol contract.ProtocolID, body []byte) *contract.OutputActivity {
	doc := gjson.ParseBytes(body)
	var part outputPart
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		for _, item := range doc.Get("output").Array() {
			responsesItemPart(&part, item)
		}
	case contract.ProtocolOpenAIChat, contract.ProtocolOpenAICompletions:
		choice := doc.Get("choices.0")
		message := choice.Get("message")
		part.note(contract.OutputKindReasoning, message.Get("reasoning_content").Str != "" || message.Get("reasoning").Str != "")
		part.note(contract.OutputKindText, message.Get("content").Str != "" || message.Get("refusal").Str != "" || choice.Get("text").Str != "")
		if name := message.Get("function_call.name").Str; name != "" {
			part.tool(name)
		}
		for _, call := range message.Get("tool_calls").Array() {
			part.tool(call.Get("function.name").Str)
		}
	case contract.ProtocolAnthropicMessages:
		for _, block := range doc.Get("content").Array() {
			anthropicBlockPart(&part, block)
		}
	case contract.ProtocolGoogleGenerateContent:
		geminiParts(&part, doc.Get("candidates.0.content.parts"))
	}
	if part.kind == "" {
		return nil
	}
	output := &contract.OutputActivity{Kind: part.kind}
	for _, name := range part.tools {
		output.AddTool(name)
	}
	return output
}

func responsesItemPart(part *outputPart, item gjson.Result) {
	switch itemType := item.Get("type").Str; {
	case itemType == "reasoning":
		part.note(contract.OutputKindReasoning, true)
	case itemType == "message":
		part.note(contract.OutputKindText, true)
	case itemType == "function_call", itemType == "custom_tool_call":
		part.tool(item.Get("name").Str)
	case strings.HasSuffix(itemType, "_call"):
		// Hosted tools run at the provider; their kind is their name.
		name := item.Get("name").Str
		if name == "" {
			name = strings.TrimSuffix(itemType, "_call")
		}
		part.tool(name)
	}
}

func anthropicBlockPart(part *outputPart, block gjson.Result) {
	switch block.Get("type").Str {
	case "thinking", "redacted_thinking":
		part.note(contract.OutputKindReasoning, true)
	case "text":
		part.note(contract.OutputKindText, true)
	case "tool_use", "server_tool_use", "mcp_tool_use":
		part.tool(block.Get("name").Str)
	}
}

func geminiParts(part *outputPart, parts gjson.Result) {
	for _, item := range parts.Array() {
		if item.Get("text").Str != "" {
			if item.Get("thought").Bool() {
				part.note(contract.OutputKindReasoning, true)
			} else {
				part.note(contract.OutputKindText, true)
			}
		}
		if name := item.Get("functionCall.name").Str; name != "" {
			part.tool(name)
		}
	}
}

// liveOutputInterval bounds how often one stream rewrites its call's output.
const liveOutputInterval = 500 * time.Millisecond

// requestOutputStore is implemented by stores that refresh a running call's
// output on its own, without rewriting the rest of the row.
type requestOutputStore interface {
	UpdateRequestOutput(context.Context, contract.RequestID, contract.OutputActivity) error
}

// flushLiveOutput stores what the stream produces once it has changed, at
// most every liveOutputInterval. Only the scanner's own state and fields set
// before the stream began are read, so it is safe on the goroutine that reads
// the upstream body.
func (session *recordSession) flushLiveOutput() {
	scanner := session.upstreamScanner
	if scanner == nil || !scanner.outputChanged {
		return
	}
	store, ok := session.persistStore.(requestOutputStore)
	if !ok {
		return
	}
	now := time.Now()
	if now.Sub(session.outputFlushedAt) < liveOutputInterval {
		return
	}
	output := scanner.outputActivity()
	if output == nil {
		return
	}
	scanner.outputChanged = false
	session.outputFlushedAt = now
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := store.UpdateRequestOutput(ctx, session.id, *output); err != nil {
		logRequestRecordFailure(session.persistLogf, "live_output_update", err)
	}
}

// noteStreamPart folds one event's part into the response's output.
func (scanner *usageScanner) noteStreamPart(part outputPart) {
	if part.kind == "" {
		return
	}
	if scanner.output.Kind != part.kind {
		scanner.output.Kind = part.kind
		scanner.outputChanged = true
	}
	for _, name := range part.tools {
		if scanner.output.AddTool(name) {
			scanner.outputChanged = true
		}
	}
}

// outputActivity is a copy of what the response has produced so far; nil
// before any output.
func (scanner *usageScanner) outputActivity() *contract.OutputActivity {
	if scanner == nil || scanner.output.Kind == "" {
		return nil
	}
	return &contract.OutputActivity{Kind: scanner.output.Kind, Tools: slices.Clone(scanner.output.Tools)}
}
