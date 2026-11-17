package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	supportedControlAPIVersion       = "v1"
	supportedProtocolContractVersion = "v1"
)

// Ready is the line Core prints on stdout once both listeners are bound.
type Ready = contract.ReadyEvent

// Health, Version and Capabilities are what the handshake reads.
type (
	Health       = contract.HealthResponse
	Version      = contract.VersionResponse
	Capabilities = contract.CapabilitiesResponse
)

// parseReady reads Core's ready line, rejecting one this desktop cannot
// talk to.
func parseReady(line string) (Ready, error) {
	var ready Ready
	if err := json.Unmarshal([]byte(line), &ready); err != nil {
		return Ready{}, fmt.Errorf("invalid astrlink-core ready signal: %w", err)
	}
	if ready.Event != "ready" {
		return Ready{}, fmt.Errorf("unexpected astrlink-core event %q; expected \"ready\"", ready.Event)
	}
	for _, field := range []struct{ name, value string }{
		{"core_version", ready.CoreVersion},
		{"control_api_version", ready.ControlAPIVersion},
		{"protocol_contract_version", ready.ProtocolContractVersion},
	} {
		if strings.TrimSpace(field.value) == "" {
			return Ready{}, fmt.Errorf("astrlink-core ready signal has empty %s", field.name)
		}
	}
	if err := ready.Validate(); err != nil {
		return Ready{}, fmt.Errorf("invalid astrlink-core ready signal: %w", err)
	}
	return ready, nil
}

func verifyContract(ready Ready, version Version, capabilities Capabilities) error {
	if ready.ControlAPIVersion != supportedControlAPIVersion {
		return fmt.Errorf("unsupported Core control API version %q; desktop supports %q", ready.ControlAPIVersion, supportedControlAPIVersion)
	}
	if ready.ProtocolContractVersion != supportedProtocolContractVersion {
		return fmt.Errorf("unsupported Core protocol contract version %q; desktop supports %q", ready.ProtocolContractVersion, supportedProtocolContractVersion)
	}
	if ready.CoreVersion != version.CoreVersion {
		return errors.New("Core version changed between ready and version handshake")
	}
	if ready.ControlAPIVersion != version.ControlAPIVersion {
		return errors.New("control API version changed between ready and version handshake")
	}
	if ready.ProtocolContractVersion != version.ProtocolContractVersion ||
		ready.ProtocolContractVersion != capabilities.ProtocolContractVersion {
		return errors.New("protocol contract version mismatch during Core handshake")
	}
	return verifyCapabilities(capabilities)
}

func verifyCapabilities(capabilities Capabilities) error {
	engine := capabilities.ConversionEngine
	if engine.Name != "relaykit" {
		return errors.New("Core returned an unsupported local conversion engine")
	}
	if engine.Available {
		if engine.Version == nil || strings.TrimSpace(*engine.Version) == "" || len(*engine.Version) > 128 {
			return errors.New("available RelayKit capability requires a non-empty version")
		}
		for _, edge := range engine.Edges {
			if strings.TrimSpace(string(edge.From)) == "" || strings.TrimSpace(string(edge.To)) == "" || edge.From == edge.To {
				return errors.New("Core returned an invalid RelayKit conversion edge")
			}
			switch edge.Quality {
			case contract.ConversionQualityGood, contract.ConversionQualityFair, contract.ConversionQualityDiscouraged:
			default:
				return errors.New("Core returned an invalid RelayKit conversion edge")
			}
		}
	} else if engine.Version != nil || len(engine.Edges) != 0 {
		return errors.New("unavailable RelayKit capability must not advertise a version or conversion edges")
	}

	expectedPlans := []struct {
		id              string
		available       bool
		localConversion bool
	}{
		{"native", true, false},
		{"delegated", true, false},
		{"relaykit", false, true},
	}
	if len(capabilities.PlanTypes) != len(expectedPlans) {
		return errors.New("Core returned an unexpected Alpha plan registry")
	}
	for _, want := range expectedPlans {
		var matches []contract.PlanTypeDescriptor
		for _, plan := range capabilities.PlanTypes {
			if string(plan.ID) == want.id {
				matches = append(matches, plan)
			}
		}
		if len(matches) != 1 || matches[0].AvailableInAlpha != want.available || matches[0].UsesLocalConversion != want.localConversion {
			return fmt.Errorf("Core returned invalid Alpha plan semantics for %s", want.id)
		}
	}

	requiredProtocols := []struct {
		id                 string
		primary, streaming bool
	}{
		{"openai.responses", true, true},
		{"openai.responses.compact", false, false},
		{"anthropic.messages", false, true},
		{"google.generate_content", false, true},
		{"openai.chat", false, true},
		{"openai.completions", false, true},
		{"openai.models", false, false},
		{"google.models", false, false},
		{"openai.embeddings", false, false},
		{"google.embeddings", false, false},
		{"rerank", false, false},
	}
	for _, want := range requiredProtocols {
		var matches []contract.ProtocolDescriptor
		for _, protocol := range capabilities.Protocols {
			if string(protocol.ID) == want.id {
				matches = append(matches, protocol)
			}
		}
		if len(matches) != 1 || matches[0].Phase != "alpha" || matches[0].Primary != want.primary || matches[0].Streaming != want.streaming {
			return fmt.Errorf("Core returned invalid Alpha protocol semantics for %s", want.id)
		}
	}
	return nil
}
