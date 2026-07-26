package contract

import (
	"fmt"
	"regexp"
)

// ProtocolID is the stable identifier persisted by AstrLink and exposed by the
// control API. It deliberately does not reuse new-api or RelayKit identifiers.
type ProtocolID string

const (
	ProtocolOpenAIResponses        ProtocolID = "openai.responses"
	ProtocolOpenAIResponsesCompact ProtocolID = "openai.responses.compact"
	ProtocolAnthropicMessages      ProtocolID = "anthropic.messages"
	ProtocolGoogleGenerateContent  ProtocolID = "google.generate_content"
	ProtocolOpenAIChat             ProtocolID = "openai.chat"
	ProtocolOpenAICompletions      ProtocolID = "openai.completions"
	ProtocolOpenAIModels           ProtocolID = "openai.models"
	ProtocolGoogleModels           ProtocolID = "google.models"

	// Post-Alpha IDs are reserved now so enabling them later does not require a
	// persistent identifier migration.
	ProtocolOpenAIRealtime   ProtocolID = "openai.realtime"
	ProtocolOpenAIEmbeddings ProtocolID = "openai.embeddings"
	ProtocolGoogleEmbeddings ProtocolID = "google.embeddings"
	ProtocolOpenAIImages     ProtocolID = "openai.images"
	ProtocolOpenAIAudio      ProtocolID = "openai.audio"
	ProtocolRerank           ProtocolID = "rerank"
	ProtocolOpenAIVideos     ProtocolID = "openai.videos"
)

type ProtocolPhase string

const (
	ProtocolPhaseAlpha     ProtocolPhase = "alpha"
	ProtocolPhasePostAlpha ProtocolPhase = "post_alpha"
)

type ProtocolDescriptor struct {
	ID        ProtocolID    `json:"id"`
	Phase     ProtocolPhase `json:"phase"`
	Primary   bool          `json:"primary"`
	Streaming bool          `json:"streaming"`
}

var descriptors = [...]ProtocolDescriptor{
	{ID: ProtocolOpenAIResponses, Phase: ProtocolPhaseAlpha, Primary: true, Streaming: true},
	{ID: ProtocolOpenAIResponsesCompact, Phase: ProtocolPhaseAlpha, Streaming: false},
	{ID: ProtocolAnthropicMessages, Phase: ProtocolPhaseAlpha, Streaming: true},
	{ID: ProtocolGoogleGenerateContent, Phase: ProtocolPhaseAlpha, Streaming: true},
	{ID: ProtocolOpenAIChat, Phase: ProtocolPhaseAlpha, Streaming: true},
	{ID: ProtocolOpenAICompletions, Phase: ProtocolPhaseAlpha, Streaming: true},
	{ID: ProtocolOpenAIModels, Phase: ProtocolPhaseAlpha, Streaming: false},
	{ID: ProtocolGoogleModels, Phase: ProtocolPhaseAlpha, Streaming: false},
	{ID: ProtocolOpenAIRealtime, Phase: ProtocolPhasePostAlpha, Streaming: true},
	{ID: ProtocolOpenAIEmbeddings, Phase: ProtocolPhasePostAlpha, Streaming: false},
	{ID: ProtocolGoogleEmbeddings, Phase: ProtocolPhasePostAlpha, Streaming: false},
	{ID: ProtocolOpenAIImages, Phase: ProtocolPhasePostAlpha, Streaming: false},
	{ID: ProtocolOpenAIAudio, Phase: ProtocolPhasePostAlpha, Streaming: true},
	{ID: ProtocolRerank, Phase: ProtocolPhasePostAlpha, Streaming: false},
	{ID: ProtocolOpenAIVideos, Phase: ProtocolPhasePostAlpha, Streaming: false},
}

var protocolIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

// ProtocolDescriptors returns a copy of the complete stable registry.
func ProtocolDescriptors() []ProtocolDescriptor {
	result := make([]ProtocolDescriptor, len(descriptors))
	copy(result, descriptors[:])
	return result
}

// LookupProtocolDescriptor returns the immutable built-in descriptor for id.
// Well-formed extension IDs intentionally have no descriptor and remain open
// for round-tripping by newer implementations.
func LookupProtocolDescriptor(id ProtocolID) (ProtocolDescriptor, bool) {
	for _, descriptor := range descriptors {
		if descriptor.ID == id {
			return descriptor, true
		}
	}
	return ProtocolDescriptor{}, false
}

// AlphaProtocolDescriptors returns the capabilities published by the Alpha
// control contract. Model discovery has its own IDs and is never treated as a
// conversion edge.
func AlphaProtocolDescriptors() []ProtocolDescriptor {
	result := make([]ProtocolDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if descriptor.Phase == ProtocolPhaseAlpha {
			result = append(result, descriptor)
		}
	}
	return result
}

func (id ProtocolID) Valid() bool {
	return len(id) >= 3 && len(id) <= 96 && protocolIDPattern.MatchString(string(id))
}

// Known reports whether id is part of this Core's built-in registry. Valid
// unknown IDs remain round-trippable so extensions do not require migrations.
func (id ProtocolID) Known() bool {
	_, ok := LookupProtocolDescriptor(id)
	return ok
}

// AvailableInAlpha reports whether id is one of the explicitly published
// Alpha protocols. Well-formed extension and post-Alpha IDs remain
// round-trippable, but the Alpha planner must not execute them.
func (id ProtocolID) AvailableInAlpha() bool {
	descriptor, ok := LookupProtocolDescriptor(id)
	return ok && descriptor.Phase == ProtocolPhaseAlpha
}

func (id ProtocolID) IsModelDiscovery() bool {
	return id == ProtocolOpenAIModels || id == ProtocolGoogleModels
}

func (id ProtocolID) Validate() error {
	if !id.Valid() {
		return fmt.Errorf("invalid protocol id %q", id)
	}
	return nil
}
