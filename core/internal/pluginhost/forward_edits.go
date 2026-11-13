package pluginhost

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/jsonedit"
	"github.com/tidwall/gjson"
)

// paramFields names the generation parameters beforeForward sets in each
// protocol: temperature, top_p, top_k, max_tokens, stop, seed and the
// penalties. A parameter a protocol has no field for is left out.
var paramFields = map[contract.ProtocolID]map[string]string{
	contract.ProtocolOpenAIChat: {
		"temperature": "temperature", "top_p": "top_p", "max_tokens": "max_tokens", "stop": "stop",
		"seed": "seed", "presence_penalty": "presence_penalty", "frequency_penalty": "frequency_penalty",
	},
	contract.ProtocolOpenAIResponses: {
		"temperature": "temperature", "top_p": "top_p", "max_tokens": "max_output_tokens",
	},
	contract.ProtocolAnthropicMessages: {
		"temperature": "temperature", "top_p": "top_p", "top_k": "top_k", "max_tokens": "max_tokens", "stop": "stop_sequences",
	},
	contract.ProtocolGoogleGenerateContent: {
		"temperature": "generationConfig.temperature", "top_p": "generationConfig.topP", "top_k": "generationConfig.topK",
		"max_tokens": "generationConfig.maxOutputTokens", "stop": "generationConfig.stopSequences", "seed": "generationConfig.seed",
		"presence_penalty": "generationConfig.presencePenalty", "frequency_penalty": "generationConfig.frequencyPenalty",
	},
}

// applyParams sets the generation parameters of a body in its protocol's
// fields; null removes one. OpenAI Chat's max_tokens goes to
// max_completion_tokens when the body uses that.
func applyParams(protocol contract.ProtocolID, body []byte, params map[string]json.RawMessage) ([]byte, error) {
	fields, ok := paramFields[protocol]
	if !ok {
		return body, fmt.Errorf("protocol %s takes no parameters", protocol)
	}
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		field, known := fields[name]
		if !known {
			continue
		}
		if protocol == contract.ProtocolOpenAIChat && name == "max_tokens" && gjson.GetBytes(body, "max_completion_tokens").Exists() {
			field = "max_completion_tokens"
		}
		value := params[name]
		var err error
		if string(value) == "null" {
			if gjson.GetBytes(body, field).Exists() {
				body, err = jsonedit.Delete(body, field)
			}
		} else {
			body, err = jsonedit.SetRaw(body, field, value)
		}
		if err != nil {
			return body, fmt.Errorf("set %s: %w", field, err)
		}
	}
	return body, nil
}

// applyTools adds tools to a body and removes others by name, in the
// protocol's own shape of a function tool.
func applyTools(protocol contract.ProtocolID, body []byte, add []ToolSpec, remove []string) ([]byte, error) {
	for _, spec := range add {
		if err := spec.check(); err != nil {
			return body, err
		}
	}
	var err error
	if len(remove) > 0 {
		if body, err = removeTools(protocol, body, remove); err != nil {
			return body, err
		}
	}
	for _, spec := range add {
		parameters := spec.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		var tool any
		switch protocol {
		case contract.ProtocolOpenAIChat:
			tool = map[string]any{"type": "function", "function": map[string]any{"name": spec.Name, "description": spec.Description, "parameters": parameters}}
		case contract.ProtocolOpenAIResponses:
			tool = map[string]any{"type": "function", "name": spec.Name, "description": spec.Description, "parameters": parameters}
		case contract.ProtocolAnthropicMessages:
			tool = map[string]any{"name": spec.Name, "description": spec.Description, "input_schema": parameters}
		case contract.ProtocolGoogleGenerateContent:
			declaration := map[string]any{"name": spec.Name, "description": spec.Description, "parameters": parameters}
			index := -1
			for at, entry := range gjson.GetBytes(body, "tools").Array() {
				if entry.Get("functionDeclarations").IsArray() {
					index = at
					break
				}
			}
			if index < 0 {
				tool = map[string]any{"functionDeclarations": []any{declaration}}
				break
			}
			path := fmt.Sprintf("tools.%d.functionDeclarations.-1", index)
			if body, err = jsonedit.Set(body, path, declaration); err != nil {
				return body, err
			}
			continue
		default:
			return body, fmt.Errorf("protocol %s takes no tools", protocol)
		}
		if body, err = jsonedit.Set(body, "tools.-1", tool); err != nil {
			return body, err
		}
	}
	return body, nil
}

// removeTools removes the function tools named in remove.
func removeTools(protocol contract.ProtocolID, body []byte, remove []string) ([]byte, error) {
	name := func(tool gjson.Result) string {
		switch protocol {
		case contract.ProtocolOpenAIChat:
			return tool.Get("function.name").Str
		default:
			return tool.Get("name").Str
		}
	}
	var err error
	if protocol == contract.ProtocolGoogleGenerateContent {
		tools := gjson.GetBytes(body, "tools").Array()
		for at := len(tools) - 1; at >= 0; at-- {
			declarations := tools[at].Get("functionDeclarations").Array()
			for index := len(declarations) - 1; index >= 0; index-- {
				if slices.Contains(remove, declarations[index].Get("name").Str) {
					if body, err = jsonedit.Delete(body, fmt.Sprintf("tools.%d.functionDeclarations.%d", at, index)); err != nil {
						return body, err
					}
				}
			}
		}
		return body, nil
	}
	tools := gjson.GetBytes(body, "tools").Array()
	for index := len(tools) - 1; index >= 0; index-- {
		if slices.Contains(remove, name(tools[index])) {
			if body, err = jsonedit.Delete(body, fmt.Sprintf("tools.%d", index)); err != nil {
				return body, err
			}
		}
	}
	return body, nil
}
