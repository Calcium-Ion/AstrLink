package contract

import (
	"strings"
	"testing"
)

func TestNativeAndDelegatedPlansPreserveProtocol(t *testing.T) {
	for _, planType := range []PlanType{PlanTypeNative, PlanTypeDelegated} {
		plan := ExecutionPlan{
			Type:             planType,
			ServiceID:        "endpoint_01",
			InputProtocol:    ProtocolOpenAIResponses,
			UpstreamProtocol: ProtocolOpenAIResponses,
			ConversionPath:   []ConversionEdge{},
			Streaming:        true,
		}
		if err := plan.ValidateForAlpha(); err != nil {
			t.Errorf("%s plan should be valid in Alpha: %v", planType, err)
		}

		plan.UpstreamProtocol = ProtocolOpenAIChat
		if err := plan.ValidateForAlpha(); err == nil || !strings.Contains(err.Error(), "preserve") {
			t.Errorf("%s cross-protocol plan error = %v, want preserve error", planType, err)
		}
	}
}

func TestRelayKitPlanIsRepresentableButUnavailableInAlpha(t *testing.T) {
	plan := ExecutionPlan{
		Type:             PlanTypeRelayKit,
		ServiceID:        "endpoint_01",
		InputProtocol:    ProtocolOpenAIResponses,
		UpstreamProtocol: ProtocolAnthropicMessages,
		ConversionPath: []ConversionEdge{
			{
				From:      ProtocolOpenAIResponses,
				To:        ProtocolAnthropicMessages,
				Quality:   ConversionQualityGood,
				Streaming: true,
			},
		},
		ConversionQuality: ConversionQualityGood,
		Streaming:         true,
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("relaykit plan representation is invalid: %v", err)
	}
	if err := plan.ValidateForAlpha(); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("relaykit Alpha validation error = %v, want unavailable", err)
	}
}

func TestStreamingRelayKitPlanRejectsNonStreamingEdge(t *testing.T) {
	plan := ExecutionPlan{
		Type:              PlanTypeRelayKit,
		ServiceID:         "endpoint_01",
		InputProtocol:     ProtocolOpenAIResponses,
		UpstreamProtocol:  ProtocolOpenAIChat,
		ConversionQuality: ConversionQualityFair,
		Streaming:         true,
		ConversionPath: []ConversionEdge{{
			From: ProtocolOpenAIResponses, To: ProtocolOpenAIChat,
			Quality: ConversionQualityFair, Streaming: false,
		}},
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "streaming") {
		t.Fatalf("validation error = %v, want streaming error", err)
	}
}

func TestAlphaPlanRejectsPostAlphaProtocolAndNullPath(t *testing.T) {
	plan := ExecutionPlan{
		Type: PlanTypeNative, ServiceID: "endpoint_01",
		InputProtocol: ProtocolOpenAIRealtime, UpstreamProtocol: ProtocolOpenAIRealtime,
		ConversionPath: []ConversionEdge{}, Streaming: true,
	}
	if err := plan.ValidateForAlpha(); err == nil || !strings.Contains(err.Error(), "not available in Alpha") {
		t.Fatalf("post-Alpha plan error = %v", err)
	}
	plan.InputProtocol = ProtocolOpenAIResponses
	plan.UpstreamProtocol = ProtocolOpenAIResponses
	plan.ConversionPath = nil
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "non-null") {
		t.Fatalf("nil conversion path error = %v", err)
	}
}

func validRelayKitPlanForValidation() ExecutionPlan {
	return ExecutionPlan{
		Type:              PlanTypeRelayKit,
		ServiceID:         "endpoint_01",
		InputProtocol:     ProtocolOpenAIResponses,
		UpstreamProtocol:  ProtocolAnthropicMessages,
		ConversionQuality: ConversionQualityGood,
		Streaming:         false,
		ConversionPath: []ConversionEdge{{
			From: ProtocolOpenAIResponses, To: ProtocolOpenAIChat,
			Quality: ConversionQualityGood,
		}, {
			From: ProtocolOpenAIChat, To: ProtocolAnthropicMessages,
			Quality: ConversionQualityFair,
		}},
	}
}

func TestExecutionPlanRejectsBrokenConversionInvariants(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ExecutionPlan)
		want   string
	}{
		{
			name: "protocol preserving plan contains conversion",
			mutate: func(plan *ExecutionPlan) {
				plan.Type = PlanTypeNative
				plan.UpstreamProtocol = plan.InputProtocol
			},
			want: "must not contain",
		},
		{name: "empty relaykit chain", mutate: func(plan *ExecutionPlan) { plan.ConversionPath = []ConversionEdge{} }, want: "requires a conversion path"},
		{name: "invalid quality", mutate: func(plan *ExecutionPlan) { plan.ConversionQuality = "lossless" }, want: "valid conversion quality"},
		{
			name:   "discontinuous chain",
			mutate: func(plan *ExecutionPlan) { plan.ConversionPath[1].From = ProtocolGoogleGenerateContent },
			want:   "does not continue",
		},
		{
			name:   "endpoint protocol mismatch",
			mutate: func(plan *ExecutionPlan) { plan.UpstreamProtocol = ProtocolGoogleGenerateContent },
			want:   "ends at",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validRelayKitPlanForValidation()
			test.mutate(&plan)
			if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}
