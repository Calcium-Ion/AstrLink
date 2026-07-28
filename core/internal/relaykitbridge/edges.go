package relaykitbridge

import "github.com/QuantumNous/astrlink/core/contract"

var supportedEdges = []contract.ConversionEdge{
	{From: contract.ProtocolOpenAIChat, To: contract.ProtocolOpenAIResponses, Quality: contract.ConversionQualityGood, Streaming: true},
	{From: contract.ProtocolOpenAIResponses, To: contract.ProtocolOpenAIChat, Quality: contract.ConversionQualityGood, Streaming: true},
	{From: contract.ProtocolAnthropicMessages, To: contract.ProtocolGoogleGenerateContent, Quality: contract.ConversionQualityDiscouraged, Streaming: true},
	{From: contract.ProtocolGoogleGenerateContent, To: contract.ProtocolAnthropicMessages, Quality: contract.ConversionQualityDiscouraged, Streaming: true},
	{From: contract.ProtocolOpenAIChat, To: contract.ProtocolAnthropicMessages, Quality: contract.ConversionQualityFair, Streaming: true},
	{From: contract.ProtocolAnthropicMessages, To: contract.ProtocolOpenAIChat, Quality: contract.ConversionQualityFair, Streaming: true},
	{From: contract.ProtocolOpenAIChat, To: contract.ProtocolGoogleGenerateContent, Quality: contract.ConversionQualityFair, Streaming: true},
	{From: contract.ProtocolGoogleGenerateContent, To: contract.ProtocolOpenAIChat, Quality: contract.ConversionQualityFair, Streaming: true},
	{From: contract.ProtocolOpenAIResponses, To: contract.ProtocolAnthropicMessages, Quality: contract.ConversionQualityFair, Streaming: true},
	{From: contract.ProtocolAnthropicMessages, To: contract.ProtocolOpenAIResponses, Quality: contract.ConversionQualityFair, Streaming: true},
	{From: contract.ProtocolOpenAIResponses, To: contract.ProtocolGoogleGenerateContent, Quality: contract.ConversionQualityFair, Streaming: true},
	{From: contract.ProtocolGoogleGenerateContent, To: contract.ProtocolOpenAIResponses, Quality: contract.ConversionQualityFair, Streaming: true},
}

func copyEdges() []contract.ConversionEdge {
	return append([]contract.ConversionEdge(nil), supportedEdges...)
}
