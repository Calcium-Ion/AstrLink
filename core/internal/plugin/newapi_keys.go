package plugin

import (
	"fmt"
	"slices"

	"github.com/QuantumNous/new-api/pluginkit"
)

// NewAPIKeyProblem says why new-api would refuse a plugin of key that
// AstrLink takes, "" when it would not: a key longer than new-api allows,
// or one of new-api's own paths, whose /{key} address would not reach the
// plugin there. AstrLink still runs such a plugin; a plugin meant for both
// gateways changes its key.
func NewAPIKeyProblem(key string) string {
	switch {
	case len(key) > pluginkit.MaxPluginKeyLength:
		return fmt.Sprintf("New API takes keys of at most %d characters", pluginkit.MaxPluginKeyLength)
	case slices.Contains(pluginkit.NewAPIReservedKeys(), key):
		return fmt.Sprintf("/%s is a path of New API, which refuses the key", key)
	}
	return ""
}
