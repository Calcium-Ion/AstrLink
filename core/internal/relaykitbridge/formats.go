package relaykitbridge

import (
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/new-api/relaykit/types"
)

func relayFormat(protocol contract.ProtocolID) (types.RelayFormat, error) {
	switch protocol {
	case contract.ProtocolOpenAIChat:
		return types.RelayFormatOpenAI, nil
	case contract.ProtocolOpenAIResponses:
		return types.RelayFormatOpenAIResponses, nil
	case contract.ProtocolAnthropicMessages:
		return types.RelayFormatClaude, nil
	case contract.ProtocolGoogleGenerateContent:
		return types.RelayFormatGemini, nil
	default:
		return "", fmt.Errorf("unsupported RelayKit protocol %q", protocol)
	}
}
