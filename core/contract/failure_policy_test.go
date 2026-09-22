package contract

import (
	"encoding/json"
	"testing"
)

func TestFailurePolicyDefaultsAndStrictDocuments(t *testing.T) {
	policy := DefaultFailurePolicy()
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if policy.MaxRetries != 1 || policy.InitialDelayMS != 500 || policy.MaxDelayMS != 5000 || policy.ActionForStatus(401) != FailureFailover || policy.ActionForStatus(400) != FailureStop {
		t.Fatalf("%+v", policy)
	}
	for _, code := range []int{408, 429, 500, 502, 503, 504, 529} {
		if policy.ActionForStatus(code) != FailureRetryAndFailover {
			t.Fatalf("status %d", code)
		}
	}
	encoded, _ := json.Marshal(policy)
	for _, key := range []string{"max_retries", "initial_delay_ms", "max_delay_ms", "network_error", "response_timeout", "http_status"} {
		var doc map[string]any
		_ = json.Unmarshal(encoded, &doc)
		delete(doc, key)
		data, _ := json.Marshal(doc)
		if json.Unmarshal(data, &FailurePolicy{}) == nil {
			t.Fatalf("accepted omitted %s", key)
		}
	}
	for _, key := range []string{"max_retries", "initial_delay_ms", "response_start_timeout_seconds", "thinking_signature_recovery", "openai_reasoning_recovery", "openai_function_output_recovery"} {
		var doc map[string]any
		_ = json.Unmarshal(encoded, &doc)
		doc[key] = nil
		data, _ := json.Marshal(doc)
		if json.Unmarshal(data, &FailurePolicy{}) == nil {
			t.Fatalf("accepted null %s", key)
		}
	}
	if json.Unmarshal([]byte(`{"strategy":"retry_first","max_attempts":6}`), &FailoverPolicy{}) == nil {
		t.Fatal("accepted omitted enabled")
	}
}

func TestOpenAIReasoningPolicyCompatibilityAndIndependence(t *testing.T) {
	policy := DefaultFailurePolicy()
	if !policy.AllowsOpenAIReasoningRecovery() {
		t.Fatal("omitted switch must enable recovery")
	}
	disabled := false
	policy.ThinkingSignatureRecovery = &disabled
	for _, enabled := range []bool{false, true} {
		policy.OpenAIReasoningRecovery = &enabled
		data, _ := json.Marshal(policy)
		var decoded FailurePolicy
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.AllowsOpenAIReasoningRecovery() != enabled || decoded.AllowsThinkingSignatureRecovery() {
			t.Fatal("repair switches are not independent")
		}
	}
	for action, want := range map[FailureAction]bool{FailureStop: true, FailureFailover: true, FailureRetry: true, FailureRetryAndFailover: true} {
		policy.HTTPStatus["400"] = action
		if policy.AllowsOpenAIReasoningRecovery() != want {
			t.Fatalf("ordinary rule %s changed repair availability", action)
		}
	}
}

func TestOpenAIFunctionOutputPolicyIsOptInAndIndependent(t *testing.T) {
	policy := DefaultFailurePolicy()
	if policy.AllowsOpenAIFunctionOutputRecovery() {
		t.Fatal("omitted switch must leave function output intact")
	}
	enabled := true
	disabled := false
	policy.OpenAIReasoningRecovery = &disabled
	policy.OpenAIFunctionOutputRecovery = &enabled
	data, _ := json.Marshal(policy)
	var decoded FailurePolicy
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.AllowsOpenAIFunctionOutputRecovery() || decoded.AllowsOpenAIReasoningRecovery() {
		t.Fatal("function-output switch is not independent")
	}
	for action, want := range map[FailureAction]bool{FailureStop: true, FailureFailover: true, FailureRetry: true, FailureRetryAndFailover: true} {
		policy.HTTPStatus["400"] = action
		if policy.AllowsOpenAIFunctionOutputRecovery() != want {
			t.Fatalf("ordinary rule %s changed repair availability", action)
		}
	}
}

func TestThinkingSignaturePolicyCompatibilityAndIndependentRules(t *testing.T) {
	policy := DefaultFailurePolicy()
	if !policy.AllowsThinkingSignatureRecovery() {
		t.Fatal("omitted setting must enable repair")
	}
	for _, enabled := range []bool{false, true} {
		policy.ThinkingSignatureRecovery = &enabled
		data, _ := json.Marshal(policy)
		var decoded FailurePolicy
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.AllowsThinkingSignatureRecovery() != enabled {
			t.Fatal("lost explicit switch")
		}
	}
	for action, want := range map[FailureAction]bool{FailureStop: true, FailureFailover: true, FailureRetry: true, FailureRetryAndFailover: true} {
		policy.HTTPStatus["400"] = action
		if policy.AllowsThinkingSignatureRecovery() != want {
			t.Fatalf("ordinary rule %s changed repair availability", action)
		}
	}
}
