package contract

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestDefaultPrivacyPolicyIsFrozenAndValid(t *testing.T) {
	policy := DefaultPrivacyPolicy()
	if err := ValidatePrivacyDefault(policy); err != nil {
		t.Fatalf("ValidatePrivacyDefault: %v", err)
	}
	if policy.ID != "policy_privacy_default" || policy.Name != "隐私保护" ||
		policy.Enabled || policy.Priority != 0 || policy.Detector != PolicyDetectorRegex ||
		policy.MinConfidence != DefaultPrivacyMinConfidence ||
		policy.RequestAction != PolicyActionRedact || policy.ResponseAction != PolicyActionAllow ||
		!policy.ResponseRestore {
		t.Fatalf("default privacy policy drifted: %#v", policy)
	}
	if len(policy.Match.Protocols) != 0 || len(policy.Match.Models) != 0 || len(policy.Match.ServiceIDs) != 0 {
		t.Fatalf("default match is not global: %#v", policy.Match)
	}
	document, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(document), `"match":{}`) ||
		!strings.Contains(string(document), `"detector":"regex"`) ||
		!strings.Contains(string(document), `"min_confidence":0.8`) ||
		!strings.Contains(string(document), `"request_action":"redact"`) ||
		!strings.Contains(string(document), `"response_restore":true`) {
		t.Fatalf("default policy wire shape = %s", document)
	}
}

func TestPolicyMinConfidenceValidation(t *testing.T) {
	for _, value := range []float64{0, 0.8, 1} {
		policy := DefaultPrivacyPolicy()
		policy.MinConfidence = value
		if err := policy.Validate(); err != nil {
			t.Fatalf("min_confidence %v rejected: %v", value, err)
		}
	}
	for _, value := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1)} {
		policy := DefaultPrivacyPolicy()
		policy.MinConfidence = value
		if err := policy.Validate(); err == nil ||
			!strings.Contains(err.Error(), "min_confidence") {
			t.Fatalf("min_confidence %v error = %v", value, err)
		}
	}
}

func TestPolicyDetectorAndRedactActionValidation(t *testing.T) {
	policy := DefaultPrivacyPolicy()
	policy.Detector = PolicyDetectorOpenAIPrivacyFilter
	modelID := LegacyOpenAIPrivacyFilterInstallationID
	policy.LocalModelID = &modelID
	policy.RequestAction = PolicyActionRedact
	if err := policy.Validate(); err != nil {
		t.Fatalf("model/redact policy rejected: %v", err)
	}
	policy.Detector = "remote_service"
	if err := policy.Validate(); err == nil || !strings.Contains(err.Error(), "detector") {
		t.Fatalf("invalid detector error = %v", err)
	}
	policy = DefaultPrivacyPolicy()
	policy.RequestAction = "drop"
	if err := policy.Validate(); err == nil || !strings.Contains(err.Error(), "request action") {
		t.Fatalf("invalid action error = %v", err)
	}
}

func TestPrivacyDefaultRejectsMutableIdentityScopeAndResponseAction(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Policy)
	}{
		{name: "id", mutate: func(policy *Policy) { policy.ID = "policy_other" }},
		{name: "name", mutate: func(policy *Policy) { policy.Name = "other" }},
		{name: "priority", mutate: func(policy *Policy) { policy.Priority = 1 }},
		{name: "response action", mutate: func(policy *Policy) {
			policy.ResponseAction = PolicyActionWarn
		}},
		{name: "protocol scope", mutate: func(policy *Policy) {
			policy.Match.Protocols = []ProtocolID{ProtocolOpenAIResponses}
		}},
		{name: "model scope", mutate: func(policy *Policy) { policy.Match.Models = []string{"gpt-5"} }},
		{name: "endpoint scope", mutate: func(policy *Policy) {
			policy.Match.ServiceIDs = []ServiceID{"endpoint_01"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultPrivacyPolicy()
			test.mutate(&policy)
			if err := ValidatePrivacyDefault(policy); err == nil {
				t.Fatal("mutable default policy was accepted")
			}
		})
	}
}
