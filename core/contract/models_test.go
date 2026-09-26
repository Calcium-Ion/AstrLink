package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestEndpointContractStoresReferenceNotSecret(t *testing.T) {
	endpoint := Endpoint{
		ID:            "endpoint_01",
		Name:          "new-api",
		Kind:          EndpointKindNewAPI,
		BaseURL:       "https://gateway.example/v1",
		Auth:          EndpointAuth{Scheme: AuthSchemeBearer},
		CredentialRef: "local://endpoint/endpoint_01",
		Enabled:       true,
		Capabilities: []Capability{{
			Protocol: ProtocolOpenAIResponses, Mode: CapabilityModeDelegated, Streaming: true,
		}},
	}
	if err := endpoint.Validate(); err != nil {
		t.Fatalf("valid endpoint rejected: %v", err)
	}
	encoded, err := json.Marshal(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "api_key") || !strings.Contains(string(encoded), "credential_ref") {
		t.Fatalf("unexpected endpoint JSON: %s", encoded)
	}
}

func TestEndpointRejectsCredentialsInBaseURL(t *testing.T) {
	endpoint := Endpoint{
		ID: "endpoint_01", Name: "unsafe", Kind: EndpointKindCustom,
		BaseURL: "https://user:secret@example.com", Auth: EndpointAuth{Scheme: AuthSchemeNone}, Enabled: true,
		Capabilities: []Capability{},
	}
	if err := endpoint.Validate(); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("validation error = %v, want credentials error", err)
	}
}

func TestEndpointAuthRoundTripsCustomHeaderWithoutSecret(t *testing.T) {
	endpoint := Endpoint{
		ID: "endpoint_custom", Name: "custom", Kind: EndpointKindCustom,
		BaseURL: "https://gateway.example", Auth: EndpointAuth{
			Scheme: AuthSchemeCustomHeader, HeaderName: "X-AstrLink-Key",
		},
		CredentialRef: "local://endpoint/endpoint_custom", Enabled: true,
		Capabilities: []Capability{},
	}
	if err := endpoint.Validate(); err != nil {
		t.Fatalf("valid custom auth rejected: %v", err)
	}
	encoded, err := json.Marshal(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"auth":{"scheme":"custom_header","header_name":"X-AstrLink-Key"}`) {
		t.Fatalf("custom auth was not preserved: %s", encoded)
	}
}

func TestEndpointAuthRejectsReservedCustomHeaders(t *testing.T) {
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
			auth := EndpointAuth{Scheme: AuthSchemeCustomHeader, HeaderName: headerName}
			if err := auth.Validate(); err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestEndpointRejectsAmbiguousCapabilitiesAndCredentialRefs(t *testing.T) {
	endpoint := Endpoint{
		ID: "endpoint_01", Name: "duplicate", Kind: EndpointKindNewAPI,
		BaseURL: "https://gateway.example", Auth: EndpointAuth{Scheme: AuthSchemeBearer}, Enabled: true,
		CredentialRef: "keyring://endpoint/",
		Capabilities: []Capability{
			{Protocol: ProtocolOpenAIResponses, Mode: CapabilityModeNative},
			{Protocol: ProtocolOpenAIResponses, Mode: CapabilityModeNative, Streaming: true},
		},
	}
	if err := endpoint.Validate(); err == nil || !strings.Contains(err.Error(), "credential_ref") {
		t.Fatalf("credential validation error = %v", err)
	}
	endpoint.CredentialRef = "local://endpoint/endpoint_01"
	if err := endpoint.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate capability error = %v", err)
	}
	endpoint.Capabilities = nil
	if err := endpoint.Validate(); err == nil || !strings.Contains(err.Error(), "non-null") {
		t.Fatalf("nil capability error = %v", err)
	}
}

func TestCredentialRefFixtureMatchesGoValidator(t *testing.T) {
	data, err := os.ReadFile("../../contracts/examples/credential-refs.v1.json")
	if err != nil {
		t.Fatalf("read credential reference fixture: %v", err)
	}
	var fixture struct {
		ControlAPIVersion       string   `json:"control_api_version"`
		ProtocolContractVersion string   `json:"protocol_contract_version"`
		Accepted                []string `json:"accepted"`
		Rejected                []string `json:"rejected"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode credential reference fixture: %v", err)
	}
	if fixture.ControlAPIVersion != ControlAPIVersion || fixture.ProtocolContractVersion != ProtocolContractVersion {
		t.Fatalf("credential reference change must preserve v1 versions: %#v", fixture)
	}
	for _, value := range fixture.Accepted {
		if err := ValidateCredentialRef(value); err != nil {
			t.Errorf("accepted credential reference %q rejected: %v", value, err)
		}
	}
	for _, value := range fixture.Rejected {
		if err := ValidateCredentialRef(value); err == nil {
			t.Errorf("rejected credential reference %q accepted", value)
		}
	}
}

func TestCapabilityStreamingMatchesBuiltInRegistry(t *testing.T) {
	nonStreaming := []ProtocolID{
		ProtocolOpenAIResponsesCompact,
		ProtocolOpenAIModels,
		ProtocolGoogleModels,
		ProtocolOpenAIEmbeddings,
		ProtocolGoogleEmbeddings,
		ProtocolOpenAIImages,
		ProtocolRerank,
		ProtocolOpenAIVideos,
	}
	for _, protocol := range nonStreaming {
		t.Run(string(protocol), func(t *testing.T) {
			capability := Capability{Protocol: protocol, Mode: CapabilityModeNative, Streaming: true}
			if err := capability.Validate(); err == nil || !strings.Contains(err.Error(), "streaming") {
				t.Fatalf("Validate() error = %v, want streaming rejection", err)
			}
			capability.Streaming = false
			if err := capability.Validate(); err != nil {
				t.Fatalf("non-streaming declaration rejected: %v", err)
			}
		})
	}

	for _, protocol := range []ProtocolID{ProtocolOpenAIResponses, "vendor.custom_protocol"} {
		capability := Capability{Protocol: protocol, Mode: CapabilityModeNative, Streaming: true}
		if err := capability.Validate(); err != nil {
			t.Errorf("streaming declaration for %q rejected: %v", protocol, err)
		}
	}
	for _, descriptor := range ProtocolDescriptors() {
		if !descriptor.Streaming {
			continue
		}
		capability := Capability{Protocol: descriptor.ID, Mode: CapabilityModeNative, Streaming: false}
		if err := capability.Validate(); err != nil {
			t.Errorf("streaming-capable protocol %q could not downgrade to streaming=false: %v", descriptor.ID, err)
		}
	}
}

func TestCapabilityRejectsInvalidMode(t *testing.T) {
	valid := Capability{Protocol: ProtocolOpenAIResponses, Mode: CapabilityModeNative}
	tests := []struct {
		name   string
		mutate func(*Capability)
		want   string
	}{
		{name: "mode", mutate: func(value *Capability) { value.Mode = "relaykit" }, want: "mode"},
		{name: "same convert_to", mutate: func(value *Capability) { value.ConvertTo = ProtocolOpenAIResponses }, want: "convert_to"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			test.mutate(&value)
			if err := value.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestNormalizeServiceModelsValidatesDeduplicatesAndSorts(t *testing.T) {
	empty, err := NormalizeServiceModels(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("NormalizeServiceModels(nil) = %#v, %v", empty, err)
	}
	models, err := NormalizeServiceModels([]string{"zeta", "alpha", "zeta"})
	if err != nil {
		t.Fatalf("NormalizeServiceModels() error = %v", err)
	}
	if got, want := strings.Join(models, ","), "alpha,zeta"; got != want {
		t.Fatalf("models = %q, want %q", got, want)
	}
	for _, input := range [][]string{{""}, {strings.Repeat("m", 257)}} {
		if _, err := NormalizeServiceModels(input); err == nil {
			t.Fatalf("NormalizeServiceModels(%q) succeeded", input)
		}
	}
	tooMany := make([]string, MaxServiceModels+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("model-%04d", index)
	}
	if _, err := NormalizeServiceModels(tooMany); err == nil {
		t.Fatalf("NormalizeServiceModels() accepted %d unique models", len(tooMany))
	}
}

func TestPolicyRejectsDuplicateProtocolScope(t *testing.T) {
	policy := Policy{
		ID: "policy_01", Name: "sensitive content", Enabled: true,
		Detector:      PolicyDetectorRegex,
		Match:         PolicyMatch{Protocols: []ProtocolID{ProtocolOpenAIResponses, ProtocolOpenAIResponses}},
		RequestAction: PolicyActionBlock, ResponseAction: PolicyActionWarn,
	}
	if err := policy.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("validation error = %v, want duplicate error", err)
	}
}
