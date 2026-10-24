package contract

import "testing"

func TestVertexConnectionAndModelFormats(t *testing.T) {
	enabled := true
	if (Service{Kind: ServiceKindVertex, ResponsesWebSocketEnabled: &enabled}).ResponsesWebSocket() {
		t.Fatal("Vertex exposed native Responses WebSockets")
	}
	for location, host := range map[string]string{"global": "aiplatform.googleapis.com", "us": "aiplatform.us.rep.googleapis.com", "eu": "aiplatform.eu.rep.googleapis.com", "us-central1": "us-central1-aiplatform.googleapis.com"} {
		vertex := VertexConnection{ProjectID: "vertex-test", Location: location}
		if err := vertex.Validate(); err != nil || vertex.BaseURL() != "https://"+host {
			t.Fatalf("%s: %v %s", location, err, vertex.BaseURL())
		}
		connection := HTTPConnection{BaseURL: vertex.BaseURL(), Auth: ServiceAuth{Scheme: AuthSchemeGoogleServiceAccount}, Vertex: &vertex}
		if err := connection.Validate("service_vertex"); err != nil {
			t.Fatal(err)
		}
		connection.BaseURL = "https://attacker.example"
		if connection.Validate("service_vertex") == nil {
			t.Fatal("accepted custom Vertex host")
		}
	}
	for _, location := range []string{"", "GLOBAL", "../global", "us-east1.example", "global/path"} {
		if (VertexConnection{ProjectID: "vertex-test", Location: location}).Validate() == nil {
			t.Fatalf("accepted %q", location)
		}
	}
	for _, model := range []string{"gemini-test", "claude-test@20250101", "meta/llama-test-maas"} {
		if got := ServiceKindVertex.BuiltinModelProtocol(model); got != "" {
			t.Fatalf("%s=%s", model, got)
		}
	}
	if ValidateModelProtocols(ServiceKindVertex, []ModelProtocol{{Model: "test", Protocol: ProtocolOpenAIResponses}}) == nil {
		t.Fatal("Vertex accepted Responses upstream")
	}
}

func TestVertexEntryCapabilities(t *testing.T) {
	vertex := &VertexConnection{ProjectID: "vertex-test", Location: "global"}
	service := Service{ID: "service_vertex", Name: "Vertex", Kind: ServiceKindVertex,
		HTTP: &HTTPConnection{BaseURL: vertex.BaseURL(), Auth: ServiceAuth{Scheme: AuthSchemeGoogleServiceAccount}, Vertex: vertex}}
	for _, tc := range []struct {
		entry, target ProtocolID
		valid         bool
	}{
		{ProtocolGoogleGenerateContent, "", true},
		{ProtocolAnthropicMessages, "", true},
		{ProtocolOpenAIChat, "", true},
		{ProtocolOpenAIResponses, ProtocolGoogleGenerateContent, true},
		{ProtocolOpenAIResponses, ProtocolAnthropicMessages, true},
		{ProtocolOpenAIResponses, ProtocolOpenAIChat, true},
		{ProtocolOpenAIResponses, "", false},
		{ProtocolOpenAIChat, ProtocolOpenAIResponses, false},
		{ProtocolOpenAIEmbeddings, "", false},
	} {
		t.Run(string(tc.entry)+"/"+string(tc.target), func(t *testing.T) {
			service.Capabilities = []Capability{{Protocol: tc.entry, Mode: CapabilityModeNative, ConvertTo: tc.target}}
			if err := service.Validate(); (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
}
