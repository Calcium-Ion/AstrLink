package relaykitbridge

import (
	"runtime/debug"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

const relayKitFallbackVersion = "v0.2.1"

func relayKitVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dependency := range info.Deps {
			if dependency.Path == "github.com/QuantumNous/new-api/relaykit" && strings.TrimSpace(dependency.Version) != "" {
				return dependency.Version
			}
		}
	}
	return relayKitFallbackVersion
}

// Descriptor exposes conversion capability without leaking RelayKit types into
// the public contract.
func Descriptor(engine ConversionEngine) contract.ConversionEngineDescriptor {
	descriptor := contract.ConversionEngineDescriptor{Name: "relaykit", Edges: []contract.ConversionEdge{}}
	switch engine.(type) {
	case *Engine:
		version := engine.Version()
		descriptor.Version = &version
		descriptor.Available = true
		descriptor.Edges = engine.Edges()
	}
	return descriptor
}
