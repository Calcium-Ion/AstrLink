package contracts_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const capabilitySchemaURL = "https://astrlink.dev/contracts/protocol-capabilities.schema.json"

type credentialRefFixture struct {
	ControlAPIVersion       string   `json:"control_api_version"`
	ProtocolContractVersion string   `json:"protocol_contract_version"`
	Accepted                []string `json:"accepted"`
	Rejected                []string `json:"rejected"`
}

func loadSchemaDocument(t *testing.T) any {
	t.Helper()
	data, err := os.ReadFile("protocol-capabilities.schema.json")
	if err != nil {
		t.Fatalf("read capability schema: %v", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode capability schema: %v", err)
	}
	return document
}

func compileSchema(t *testing.T, location string) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(capabilitySchemaURL, loadSchemaDocument(t)); err != nil {
		t.Fatalf("add capability schema: %v", err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatalf("compile %s: %v", location, err)
	}
	return schema
}

func loadFixture(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("examples/capabilities.alpha.json")
	if err != nil {
		t.Fatalf("read capability fixture: %v", err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode capability fixture: %v", err)
	}
	object, ok := instance.(map[string]any)
	if !ok {
		t.Fatalf("capability fixture has type %T, want object", instance)
	}
	return object
}

func loadCredentialRefFixture(t *testing.T) credentialRefFixture {
	t.Helper()
	data, err := os.ReadFile("examples/credential-refs.v1.json")
	if err != nil {
		t.Fatalf("read credential reference fixture: %v", err)
	}
	var fixture credentialRefFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode credential reference fixture: %v", err)
	}
	return fixture
}

func cloneObject(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode test instance: %v", err)
	}
	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatalf("decode test instance: %v", err)
	}
	return clone
}

func TestFrozenCapabilityFixtureValidatesAgainstRootSchema(t *testing.T) {
	if err := compileSchema(t, capabilitySchemaURL).Validate(loadFixture(t)); err != nil {
		t.Fatalf("frozen capability fixture failed schema validation: %v", err)
	}
}

func TestCredentialRefFixtureValidatesAgainstSharedDefinition(t *testing.T) {
	fixture := loadCredentialRefFixture(t)
	if fixture.ControlAPIVersion != "v1" || fixture.ProtocolContractVersion != "v1" {
		t.Fatalf("credential reference change must preserve v1 versions: %#v", fixture)
	}
	schema := compileSchema(t, capabilitySchemaURL+"#/$defs/CredentialRef")
	for _, value := range fixture.Accepted {
		if err := schema.Validate(value); err != nil {
			t.Errorf("accepted credential reference %q rejected: %v", value, err)
		}
	}
	for _, value := range fixture.Rejected {
		if err := schema.Validate(value); err == nil {
			t.Errorf("rejected credential reference %q accepted", value)
		}
	}
}

func TestRootCapabilitySchemaRejectsContractDrift(t *testing.T) {
	schema := compileSchema(t, capabilitySchemaURL)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "below minimum protocol count",
			mutate: func(instance map[string]any) {
				protocols := instance["protocols"].([]any)
				instance["protocols"] = protocols[:len(protocols)-1]
			},
		},
		{
			name: "missing required protocol at valid count",
			mutate: func(instance map[string]any) {
				protocols := instance["protocols"].([]any)
				replacement := cloneObject(t, protocols[len(protocols)-1].(map[string]any))
				replacement["id"] = "vendor.custom_models"
				protocols[len(protocols)-1] = replacement
			},
		},
		{
			name: "illegal unavailable RelayKit state",
			mutate: func(instance map[string]any) {
				engine := instance["conversion_engine"].(map[string]any)
				engine["version"] = "0.1.0"
			},
		},
		{
			name: "unexpected root field",
			mutate: func(instance map[string]any) {
				instance["unexpected"] = true
			},
		},
	}
	fixture := loadFixture(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance := cloneObject(t, fixture)
			test.mutate(instance)
			if err := schema.Validate(instance); err == nil {
				t.Fatal("invalid capability instance passed schema validation")
			}
		})
	}
}

func TestServiceCapabilityDefinitionEnforcesStreamingRegistry(t *testing.T) {
	// ServiceCapability is a reusable $def that the root capability response
	// does not reference. Compile it directly so this invariant is exercised.
	schema := compileSchema(t, capabilitySchemaURL+"#/$defs/ServiceCapability")
	nonStreaming := []string{
		"openai.responses.compact",
		"openai.models",
		"google.models",
		"openai.embeddings",
		"google.embeddings",
		"openai.images",
		"rerank",
		"openai.videos",
	}
	for _, protocol := range nonStreaming {
		t.Run(protocol, func(t *testing.T) {
			invalid := map[string]any{"protocol": protocol, "mode": "native", "streaming": true}
			if err := schema.Validate(invalid); err == nil {
				t.Fatal("non-streaming built-in protocol accepted streaming=true")
			}
			invalid["streaming"] = false
			if err := schema.Validate(invalid); err != nil {
				t.Fatalf("streaming=false rejected: %v", err)
			}
		})
	}

	valid := []map[string]any{
		{"protocol": "openai.responses", "mode": "native", "streaming": true},
		{"protocol": "openai.responses", "mode": "native", "streaming": false},
		{"protocol": "vendor.custom_protocol", "mode": "delegated", "streaming": true},
	}
	for _, instance := range valid {
		if err := schema.Validate(instance); err != nil {
			t.Errorf("valid service capability %#v rejected: %v", instance, err)
		}
	}
}

func TestServiceAuthDefinitionRejectsReservedCustomHeaders(t *testing.T) {
	schema := compileSchema(t, capabilitySchemaURL+"#/$defs/ServiceAuth")
	for _, headerName := range []string{
		"Host",
		"content-length",
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"proxy-authorization",
		"Proxy-Connection",
		"TE",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	} {
		t.Run(headerName, func(t *testing.T) {
			instance := map[string]any{"scheme": "custom_header", "header_name": headerName}
			if err := schema.Validate(instance); err == nil {
				t.Fatal("reserved custom auth header passed schema validation")
			}
		})
	}

	for _, headerName := range []string{"Authorization", "X-Api-Key", "api-key"} {
		instance := map[string]any{"scheme": "custom_header", "header_name": headerName}
		if err := schema.Validate(instance); err != nil {
			t.Errorf("usable custom auth header %q rejected: %v", headerName, err)
		}
	}
}
