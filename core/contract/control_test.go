package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultCapabilitiesExposeAlphaSemanticsWithoutConversion(t *testing.T) {
	capabilities := DefaultCapabilitiesResponse()
	if len(capabilities.Protocols) != 8 {
		t.Fatalf("protocol count = %d, want 8", len(capabilities.Protocols))
	}
	if capabilities.ConversionEngine.Name != "relaykit" || capabilities.ConversionEngine.Version != nil || capabilities.ConversionEngine.Available {
		t.Fatalf("unexpected conversion engine descriptor: %#v", capabilities.ConversionEngine)
	}
	if capabilities.ConversionEngine.Edges == nil || len(capabilities.ConversionEngine.Edges) != 0 {
		t.Fatalf("conversion edges = %#v, want non-nil empty slice", capabilities.ConversionEngine.Edges)
	}

	want := map[PlanType]struct {
		available  bool
		conversion bool
	}{
		PlanTypeNative:    {available: true, conversion: false},
		PlanTypeDelegated: {available: true, conversion: false},
		PlanTypeRelayKit:  {available: false, conversion: true},
	}
	for _, descriptor := range capabilities.PlanTypes {
		expected, ok := want[descriptor.ID]
		if !ok {
			t.Fatalf("unexpected plan type %q", descriptor.ID)
		}
		if descriptor.AvailableInAlpha != expected.available || descriptor.UsesLocalConversion != expected.conversion {
			t.Errorf("plan descriptor %#v, want available=%t conversion=%t", descriptor, expected.available, expected.conversion)
		}
		delete(want, descriptor.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing plan descriptors: %#v", want)
	}

	encoded, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	engine := raw["conversion_engine"].(map[string]any)
	if value, exists := engine["version"]; !exists || value != nil {
		t.Fatalf("conversion_engine.version = %#v (exists=%t), want explicit null", value, exists)
	}
}

func TestVersionResponseValidateRejectsInvalidFields(t *testing.T) {
	valid := DefaultVersionResponse("0.1.0-dev", "0123456")
	tests := []struct {
		name   string
		mutate func(*VersionResponse)
		want   string
	}{
		{name: "empty core version", mutate: func(value *VersionResponse) { value.CoreVersion = "" }, want: "core_version"},
		{name: "long core version", mutate: func(value *VersionResponse) { value.CoreVersion = strings.Repeat("a", 65) }, want: "core_version"},
		{name: "invalid control version", mutate: func(value *VersionResponse) { value.ControlAPIVersion = "bad/version" }, want: "control_api_version"},
		{name: "long protocol version", mutate: func(value *VersionResponse) { value.ProtocolContractVersion = strings.Repeat("a", 65) }, want: "protocol_contract_version"},
		{name: "uppercase commit", mutate: func(value *VersionResponse) { value.BuildCommit = "ABCDEF0" }, want: "build_commit"},
		{name: "short commit", mutate: func(value *VersionResponse) { value.BuildCommit = "abcdef" }, want: "build_commit"},
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

func validReadyEvent() ReadyEvent {
	return ReadyEvent{
		Event:                   "ready",
		CoreVersion:             "0.1.0-dev",
		ControlAPIVersion:       ControlAPIVersion,
		ProtocolContractVersion: ProtocolContractVersion,
		InferenceURL:            "http://127.0.0.1:8317",
		ControlURL:              "http://127.0.0.1:49152",
	}
}

func TestReadyEventValidateAcceptsPortBoundaries(t *testing.T) {
	for _, port := range []string{"1", "65535"} {
		event := validReadyEvent()
		event.InferenceURL = "http://127.0.0.1:" + port
		event.ControlURL = "http://127.0.0.1:" + port
		if err := event.Validate(); err != nil {
			t.Errorf("port %s rejected: %v", port, err)
		}
	}
}

func TestReadyEventValidateRejectsInvalidAnnouncements(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReadyEvent)
	}{
		{name: "event", mutate: func(value *ReadyEvent) { value.Event = "started" }},
		{name: "core version", mutate: func(value *ReadyEvent) { value.CoreVersion = "" }},
		{name: "control version", mutate: func(value *ReadyEvent) { value.ControlAPIVersion = "bad/version" }},
		{name: "protocol version", mutate: func(value *ReadyEvent) { value.ProtocolContractVersion = "bad/version" }},
		{name: "zero", mutate: func(value *ReadyEvent) { value.ControlURL = "http://127.0.0.1:0" }},
		{name: "leading zero", mutate: func(value *ReadyEvent) { value.ControlURL = "http://127.0.0.1:080" }},
		{name: "out of range 65536", mutate: func(value *ReadyEvent) { value.ControlURL = "http://127.0.0.1:65536" }},
		{name: "out of range 99999", mutate: func(value *ReadyEvent) { value.ControlURL = "http://127.0.0.1:99999" }},
		{name: "localhost", mutate: func(value *ReadyEvent) { value.ControlURL = "http://localhost:8317" }},
		{name: "ipv6", mutate: func(value *ReadyEvent) { value.ControlURL = "http://[::1]:8317" }},
		{name: "credentials", mutate: func(value *ReadyEvent) { value.ControlURL = "http://user@127.0.0.1:8317" }},
		{name: "path", mutate: func(value *ReadyEvent) { value.ControlURL = "http://127.0.0.1:8317/" }},
		{name: "query", mutate: func(value *ReadyEvent) { value.ControlURL = "http://127.0.0.1:8317?x=1" }},
		{name: "fragment", mutate: func(value *ReadyEvent) { value.ControlURL = "http://127.0.0.1:8317#x" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validReadyEvent()
			test.mutate(&event)
			if err := event.Validate(); err == nil {
				t.Fatal("invalid ready event was accepted")
			}
		})
	}
}
