package privacy

import "github.com/QuantumNous/astrlink/core/contract"

// InspectionOptions selects which tool declarations extraction reaches. The
// same options must be used to extract a body and to locate its findings, or
// segment indexes no longer line up.
type InspectionOptions struct {
	InspectToolDeclarations bool
	SkipAdditionalTools     bool
}

func (policy Policy) InspectionOptions() InspectionOptions {
	return InspectionOptions{
		InspectToolDeclarations: policy.InspectToolDeclarations,
		SkipAdditionalTools:     policy.SkipAdditionalTools,
	}
}

// toolDeclarationRoots lists the request fields that declare tools. They are
// inspected only on request; see protocolRoots for why they are left out.
func toolDeclarationRoots(protocol contract.ProtocolID) []string {
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact,
		contract.ProtocolAnthropicMessages, contract.ProtocolGoogleGenerateContent:
		return []string{"tools"}
	case contract.ProtocolOpenAIChat:
		return []string{"tools", "functions"}
	default:
		return nil
	}
}

// addToolIdentityPaths protects Responses namespaces at their protocol
// positions. Calls must keep matching their declarations even when a detector
// mistakes the identifier for a secret. The same values and keys in arguments,
// outputs, descriptions and prompt variables remain inspectable.
func addToolIdentityPaths(protocol contract.ProtocolID, root map[string]any, paths map[string]struct{}) {
	if protocol != contract.ProtocolOpenAIResponses && protocol != contract.ProtocolOpenAIResponsesCompact {
		return
	}
	protect := func(record map[string]any, path, key string) {
		if _, ok := record[key].(string); ok {
			paths[path+"/"+key] = struct{}{}
		}
	}
	var declarations func(any, string, int)
	declarations = func(value any, path string, depth int) {
		if depth > maxJSONDepth {
			return
		}
		items, _ := value.([]any)
		for index, value := range items {
			item, _ := value.(map[string]any)
			if item["type"] != "namespace" {
				continue
			}
			itemPath := path + "/" + jsonIndex(index)
			protect(item, itemPath, "name")
			declarations(item["tools"], itemPath+"/tools", depth+2)
		}
	}
	declarations(root["tools"], "/tools", 1)
	items, _ := root["input"].([]any)
	for index, value := range items {
		item, _ := value.(map[string]any)
		path := "/input/" + jsonIndex(index)
		switch item["type"] {
		case "function_call", "custom_tool_call":
			if role, exists := item["role"]; !exists || role == "assistant" {
				protect(item, path, "namespace")
			}
		case "additional_tools":
			if item["role"] == "developer" {
				declarations(item["tools"], path+"/tools", 3)
			}
		}
	}
}

// addAdditionalToolsPaths protects the tool declarations Codex responses-lite
// carries in developer input items. Only the tools array is protected, so an
// item that merely claims the type still has its content inspected.
func addAdditionalToolsPaths(protocol contract.ProtocolID, root map[string]any, paths map[string]struct{}) {
	if protocol != contract.ProtocolOpenAIResponses && protocol != contract.ProtocolOpenAIResponsesCompact {
		return
	}
	items, _ := root["input"].([]any)
	for index, value := range items {
		item, _ := value.(map[string]any)
		if item["type"] != "additional_tools" || item["role"] != "developer" {
			continue
		}
		if _, isArray := item["tools"].([]any); isArray {
			paths["/input/"+jsonIndex(index)+"/tools"] = struct{}{}
		}
	}
}
