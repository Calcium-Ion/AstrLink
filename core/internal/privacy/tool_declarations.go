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
