package plugin

import (
	"fmt"
	"slices"
)

// NewAPIKeyLength is the longest key new-api takes.
const NewAPIKeyLength = 30

// NewAPIKeyProblem says why new-api would refuse a plugin of key that
// AstrLink takes, "" when it would not: a key longer than new-api allows,
// or one of new-api's own paths, whose /{key} address would not reach the
// plugin there. AstrLink still runs such a plugin; a plugin meant for both
// gateways changes its key.
func NewAPIKeyProblem(key string) string {
	switch {
	case len(key) > NewAPIKeyLength:
		return fmt.Sprintf("New API takes keys of at most %d characters", NewAPIKeyLength)
	case slices.Contains(newAPIReservedKeys, key):
		return fmt.Sprintf("/%s is a path of New API, which refuses the key", key)
	}
	return ""
}
