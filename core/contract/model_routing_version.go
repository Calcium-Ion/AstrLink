package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ModelRoutingGroupVersion is one saved configuration of a routing group.
// Versions count per group and grow only when the routing content changes, so
// a request record can name the configuration it was routed under with a
// number a person can read, and the configuration itself stays out of every
// record.
type ModelRoutingGroupVersion struct {
	GroupID string `json:"group_id"`
	Version int    `json:"version"`
	// LatestVersion is the group's newest version when this one was read, so
	// a reader can tell an old configuration from the current one.
	LatestVersion int               `json:"latest_version"`
	CreatedAt     string            `json:"created_at"`
	Group         ModelRoutingGroup `json:"group"`
}

// ModelRoutingGroupConfigHash is the content identity versions are keyed on:
// the group's canonical form with Enabled left out. Switching a group off and
// on again changes which requests it serves, not how it routes them, so it
// does not mint a version.
func ModelRoutingGroupConfigHash(group ModelRoutingGroup) (string, error) {
	encoded, err := json.Marshal(group)
	if err != nil {
		return "", err
	}
	// The round trip is a deep copy: canonicalizing sorts nested lists in
	// place, and the caller's group is usually a live settings snapshot.
	var copied ModelRoutingGroup
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return "", fmt.Errorf("model routing group %q: %w", group.ID, err)
	}
	copied.Enabled = true
	canonicalizeModelRoutingGroup(&copied)
	encoded, err = json.Marshal(copied)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
