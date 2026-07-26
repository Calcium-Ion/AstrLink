package privacy

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

// SupportsInspection reports whether the protocol has extractable request-body
// roots for privacy evaluation.
func SupportsInspection(protocol contract.ProtocolID) bool {
	_, ok := protocolRoots(protocol)
	return ok
}

// WrapSampleText builds a minimal inspectable request body for dry-run.
func WrapSampleText(protocol contract.ProtocolID, sample string) ([]byte, error) {
	if !SupportsInspection(protocol) {
		return nil, fmt.Errorf("protocol %q does not support privacy inspection", protocol)
	}
	if sample == "" {
		return nil, fmt.Errorf("sample_text is required")
	}
	if len(sample) > contract.MaxPolicyDryRunSampleBytes {
		return nil, fmt.Errorf("sample_text exceeds %d bytes", contract.MaxPolicyDryRunSampleBytes)
	}
	if !utf8.ValidString(sample) {
		return nil, ErrUnsafeInput
	}
	var payload any
	switch protocol {
	case contract.ProtocolOpenAIChat, contract.ProtocolAnthropicMessages:
		payload = map[string]any{
			"messages": []map[string]string{
				{"role": "user", "content": sample},
			},
		}
	case contract.ProtocolOpenAICompletions:
		payload = map[string]any{"prompt": sample}
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		payload = map[string]any{"input": sample}
	case contract.ProtocolGoogleGenerateContent:
		payload = map[string]any{
			"contents": []map[string]any{
				{"parts": []map[string]string{{"text": sample}}},
			},
		}
	default:
		return nil, fmt.Errorf("protocol %q does not support privacy inspection", protocol)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode sample request body: %w", err)
	}
	return body, nil
}

// LocateFindings maps detector findings onto JSON paths without retaining
// matched plaintext.
func LocateFindings(protocol contract.ProtocolID, body []byte, findings []Finding) ([]contract.PolicyDryRunFinding, error) {
	_, extracted, err := extractDocument(protocol, body)
	if err != nil {
		return nil, err
	}
	locations := make([]contract.PolicyDryRunFinding, 0, len(findings))
	for _, finding := range findings {
		if finding.Segment < 0 || finding.Segment >= len(extracted) || !validKind(finding.Kind) {
			return nil, ErrDetectorUnavailable
		}
		locations = append(locations, contract.PolicyDryRunFinding{
			Kind:  string(finding.Kind),
			Path:  extracted[finding.Segment].Path,
			Start: finding.Start,
			End:   finding.End,
		})
	}
	return locations, nil
}
