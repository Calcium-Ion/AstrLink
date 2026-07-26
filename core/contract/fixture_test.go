package contract

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestDefaultCapabilitiesMatchesFrozenFixture(t *testing.T) {
	expectedJSON, err := os.ReadFile("../../contracts/examples/capabilities.alpha.json")
	if err != nil {
		t.Fatalf("read frozen capability fixture: %v", err)
	}
	var expected any
	if err := json.Unmarshal(expectedJSON, &expected); err != nil {
		t.Fatalf("decode frozen capability fixture: %v", err)
	}
	actualJSON, err := json.Marshal(DefaultCapabilitiesResponse())
	if err != nil {
		t.Fatalf("encode Go capability response: %v", err)
	}
	var actual any
	if err := json.Unmarshal(actualJSON, &actual); err != nil {
		t.Fatalf("decode Go capability response: %v", err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Go capability response drifted from contracts fixture\nactual: %s\nexpected: %s", actualJSON, expectedJSON)
	}
}
