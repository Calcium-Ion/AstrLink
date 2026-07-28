package contract

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

func TestAlphaProtocolDescriptorsAreFrozen(t *testing.T) {
	descriptors := AlphaProtocolDescriptors()
	got := make([]ProtocolID, 0, len(descriptors))
	for _, descriptor := range descriptors {
		got = append(got, descriptor.ID)
		if descriptor.Phase != ProtocolPhaseAlpha {
			t.Fatalf("protocol %q has phase %q", descriptor.ID, descriptor.Phase)
		}
	}
	want := []ProtocolID{
		ProtocolOpenAIResponses,
		ProtocolOpenAIResponsesCompact,
		ProtocolAnthropicMessages,
		ProtocolGoogleGenerateContent,
		ProtocolOpenAIChat,
		ProtocolOpenAICompletions,
		ProtocolOpenAIModels,
		ProtocolGoogleModels,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Alpha protocol IDs = %#v, want %#v", got, want)
	}
}

func TestProtocolDescriptorsReturnsCopy(t *testing.T) {
	first := ProtocolDescriptors()
	first[0].ID = "changed"
	second := ProtocolDescriptors()
	if second[0].ID != ProtocolOpenAIResponses {
		t.Fatalf("registry was mutated through returned slice: %q", second[0].ID)
	}
}

func TestLookupProtocolDescriptor(t *testing.T) {
	descriptor, ok := LookupProtocolDescriptor(ProtocolOpenAIResponsesCompact)
	if !ok || descriptor.ID != ProtocolOpenAIResponsesCompact || descriptor.Streaming {
		t.Fatalf("descriptor = %#v, found=%t", descriptor, ok)
	}
	if _, ok := LookupProtocolDescriptor("vendor.custom_protocol"); ok {
		t.Fatal("well-formed extension unexpectedly has a built-in descriptor")
	}
}

func TestServiceCapabilitySchemaMatchesNonStreamingRegistry(t *testing.T) {
	data, err := os.ReadFile("../../contracts/protocol-capabilities.schema.json")
	if err != nil {
		t.Fatalf("read capability schema: %v", err)
	}
	var document struct {
		Definitions struct {
			ServiceCapability struct {
				AllOf []struct {
					If struct {
						Properties struct {
							Protocol struct {
								Enum []string `json:"enum"`
							} `json:"protocol"`
						} `json:"properties"`
					} `json:"if"`
				} `json:"allOf"`
			} `json:"ServiceCapability"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode capability schema: %v", err)
	}
	if len(document.Definitions.ServiceCapability.AllOf) != 1 {
		t.Fatalf("ServiceCapability allOf count = %d, want 1 registry condition", len(document.Definitions.ServiceCapability.AllOf))
	}

	got := append([]string(nil), document.Definitions.ServiceCapability.AllOf[0].If.Properties.Protocol.Enum...)
	want := make([]string, 0)
	for _, descriptor := range ProtocolDescriptors() {
		if !descriptor.Streaming {
			want = append(want, string(descriptor.ID))
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema non-streaming protocols = %#v, want registry %#v", got, want)
	}
}

func TestReservedPostAlphaProtocolIDsAreValid(t *testing.T) {
	reserved := []ProtocolID{
		ProtocolOpenAIRealtime,
		ProtocolOpenAIEmbeddings,
		ProtocolGoogleEmbeddings,
		ProtocolOpenAIImages,
		ProtocolOpenAIAudio,
		ProtocolRerank,
		ProtocolOpenAIVideos,
	}
	for _, id := range reserved {
		if err := id.Validate(); err != nil {
			t.Errorf("reserved protocol %q is invalid: %v", id, err)
		}
	}
}

func TestWellFormedUnknownProtocolIDCanRoundTrip(t *testing.T) {
	id := ProtocolID("vendor.custom_protocol")
	if err := id.Validate(); err != nil {
		t.Fatalf("well-formed extension protocol rejected: %v", err)
	}
	if id.Known() {
		t.Fatalf("extension protocol %q unexpectedly marked built-in", id)
	}
	for _, id := range []ProtocolID{"X.invalid", "a", "openai..chat", "openai/chat"} {
		if err := id.Validate(); err == nil {
			t.Errorf("invalid protocol %q accepted", id)
		}
	}
}
