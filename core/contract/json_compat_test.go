package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLegacyEndpointIdentifiersDecodeAndMarshalAsServices(t *testing.T) {
	t.Parallel()

	var route Route
	if err := json.Unmarshal([]byte(`{
		"id":"route_legacy","name":"Legacy","enabled":true,"priority":0,
		"match":{"protocol":"openai_responses"},
		"targets":[{"endpoint_id":"endpoint_legacy","plan_type":"native","upstream_protocol":"openai_responses","priority":0}]
	}`), &route); err != nil {
		t.Fatalf("decode legacy route: %v", err)
	}
	if got := route.Targets[0].ServiceID; got != "endpoint_legacy" {
		t.Fatalf("service id = %q", got)
	}

	var policy PolicyMatch
	if err := json.Unmarshal([]byte(`{"endpoint_ids":["endpoint_legacy"]}`), &policy); err != nil {
		t.Fatalf("decode legacy policy match: %v", err)
	}
	if len(policy.ServiceIDs) != 1 || policy.ServiceIDs[0] != "endpoint_legacy" {
		t.Fatalf("service ids = %#v", policy.ServiceIDs)
	}

	var plan ExecutionPlan
	if err := json.Unmarshal([]byte(`{
		"plan_type":"native","endpoint_id":"endpoint_legacy",
		"input_protocol":"openai_responses","upstream_protocol":"openai_responses",
		"conversion_path":[],"streaming":false
	}`), &plan); err != nil {
		t.Fatalf("decode legacy plan: %v", err)
	}
	if plan.ServiceID != "endpoint_legacy" {
		t.Fatalf("service id = %q", plan.ServiceID)
	}

	for name, value := range map[string]any{"route": route, "policy": policy, "plan": plan} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if strings.Contains(string(encoded), "endpoint_id") {
			t.Fatalf("%s retained legacy key: %s", name, encoded)
		}
		if !strings.Contains(string(encoded), "service_id") {
			t.Fatalf("%s omitted canonical key: %s", name, encoded)
		}
	}
}

func TestLegacyAndCanonicalIdentifiersCannotBeMixed(t *testing.T) {
	t.Parallel()
	var target RouteTarget
	err := json.Unmarshal([]byte(`{
		"service_id":"service_one","endpoint_id":"endpoint_two",
		"plan_type":"native","upstream_protocol":"openai_responses","priority":0
	}`), &target)
	if err == nil {
		t.Fatal("expected mixed identifier fields to be rejected")
	}
}
