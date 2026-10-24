package contract

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// ModelProtocol pins the protocol a provider is called in for one of its
// models. A request entering on any model format is converted to it, or
// passes through when it already uses it; the protocol needs no capability
// row of its own.
type ModelProtocol struct {
	Model    string     `json:"model"`
	Protocol ProtocolID `json:"protocol"`
}

// modelFormats are the inference protocols a model can be pinned to.
var modelFormats = []ProtocolID{
	ProtocolOpenAIResponses, ProtocolAnthropicMessages, ProtocolOpenAIChat, ProtocolGoogleGenerateContent,
}

// IsModelFormat reports whether protocol is one of the inference protocols a
// model's upstream format can name. Only requests entering on these follow
// the format; every other protocol keeps its own capability row.
func (protocol ProtocolID) IsModelFormat() bool {
	return slices.Contains(modelFormats, protocol)
}

func (entry *ModelProtocol) UnmarshalJSON(data []byte) error {
	type document ModelProtocol
	var value document
	if err := decodePolicy(data, &value, []string{"model", "protocol"}, nil); err != nil {
		return err
	}
	*entry = ModelProtocol(value)
	return nil
}

// ModelFormats lists the formats a service of kind can pin a model to. A
// subscription speaks a fixed wire format, so its conversion targets bound it.
func (kind ServiceKind) ModelFormats() []ProtocolID {
	if kind == ServiceKindVertex {
		return []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIChat, ProtocolGoogleGenerateContent}
	}
	if !kind.IsSubscription() {
		return slices.Clone(modelFormats)
	}
	var formats []ProtocolID
	for _, target := range kind.SubscriptionProvider().ConversionTargets() {
		if target.IsModelFormat() {
			formats = append(formats, target)
		}
	}
	return formats
}

// ValidateModelProtocols checks a service's own table: each model appears
// once and names a format its kind can be called in.
func ValidateModelProtocols(kind ServiceKind, entries []ModelProtocol) error {
	if len(entries) > MaxServiceModels {
		return fmt.Errorf("model_protocols must contain at most %d entries", MaxServiceModels)
	}
	formats := kind.ModelFormats()
	seen := make(map[string]struct{}, len(entries))
	for index, entry := range entries {
		if entry.Model == "" || utf8.RuneCountInString(entry.Model) > 256 {
			return fmt.Errorf("model_protocols[%d].model must contain 1 to 256 characters", index)
		}
		if _, duplicate := seen[entry.Model]; duplicate {
			return fmt.Errorf("model_protocols[%d] duplicates model %q", index, entry.Model)
		}
		seen[entry.Model] = struct{}{}
		if !slices.Contains(formats, entry.Protocol) {
			names := make([]string, 0, len(formats))
			for _, format := range formats {
				names = append(names, string(format))
			}
			return fmt.Errorf("model_protocols[%d].protocol must be one of %s", index, strings.Join(names, ", "))
		}
	}
	return nil
}

// NormalizeModelProtocols returns the table sorted by model, or nil when it
// is empty, after validating it for kind.
func NormalizeModelProtocols(kind ServiceKind, entries []ModelProtocol) ([]ModelProtocol, error) {
	if err := ValidateModelProtocols(kind, entries); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b ModelProtocol) int { return strings.Compare(a.Model, b.Model) })
	return sorted, nil
}
