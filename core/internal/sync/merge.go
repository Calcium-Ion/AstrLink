// Package sync moves a device's configuration to and from a remote store. The
// description here is the package format and the merge rule; the local and
// remote halves live in their own files.
//
// A package is a JSON document sealed by syncseal, so the remote holding it
// learns nothing beyond its size and when it changed. Inside, every entity is
// named by the id this device stores it under, because that id is the identity
// a user recognises across devices. The merge is three-way: the package this
// device last synced is the base, and a difference from the base on both sides
// is a conflict.
//
// Entities carry updated_at, so a conflict is resolved by taking the later
// change and recording a notice rather than dropping one side silently. A
// deletion counts as a change at deleted_at. A device that holds no base at all
// — one that has never synced with this remote — has nothing to compare
// against, so it adopts the remote's copy of every entity it did not change
// itself instead of publishing the configuration its installation started from.
package sync

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"
)

// PackageVersion is the newest payload version this build writes and reads.
// Version 2 protects access tokens from version 1 writers, which drop unknown
// entity kinds. The encrypted envelope's format is unchanged.
const PackageVersion = 2

const legacyPackageVersion = 1

// Entity kinds a package carries.
//
// KindServiceCredential, KindServiceProxyCredential, and
// KindBuiltinToolCredential name sealed values rather than documents: their
// entity id is the owning provider's (or the tool's), and their Entity carries
// Secret instead of Document. Credentials are their own kinds, so whether they
// travel is a separate decision from whether the provider row does.
//
// KindAccessToken is the one kind that carries both: the value is a secret a
// client presents, and the name beside it is what a user recognises the token
// by. The value has to travel for the token to work on another device, which is
// what the group is for, and the metadata has to travel with it because the
// digest an authentication reads is derived from the value on the device that
// stores it.
//
// Subscription credentials are deliberately absent: a subscription login is
// performed on each device, so its OAuth token never enters a package.
const (
	KindService                = "service"
	KindServiceCredential      = "service_credential"
	KindServiceProxyCredential = "service_proxy_credential"
	KindBuiltinToolCredential  = "builtin_tool_credential"
	KindPolicy                 = "policy"
	KindPricingConfig          = "pricing_config"
	KindAccessToken            = "access_token"
)

// Kinds is every kind a package carries, in a stable order.
func Kinds() []string {
	return []string{
		KindService,
		KindServiceCredential,
		KindServiceProxyCredential,
		KindBuiltinToolCredential,
		KindPolicy,
		KindPricingConfig,
		KindAccessToken,
	}
}

// CredentialKinds are the kinds whose Entity carries Secret.
func CredentialKinds() []string {
	return []string{KindServiceCredential, KindServiceProxyCredential, KindBuiltinToolCredential}
}

// IsCredentialKind reports whether a kind carries a sealed value rather than a
// document.
func IsCredentialKind(kind string) bool {
	for _, credential := range CredentialKinds() {
		if kind == credential {
			return true
		}
	}
	return false
}

// Entity is one named configuration document.
type Entity struct {
	// Revision counts local writes to this entity; it is informational and is
	// not compared across devices, which have unrelated counters.
	Revision int64 `json:"revision"`
	// UpdatedAt is when this side last changed the entity.
	UpdatedAt time.Time `json:"updated_at"`
	// Deleted marks a tombstone inside the package, so a device that still
	// holds the entity deletes it.
	Deleted bool `json:"deleted"`
	// Document is the entity's JSON. It is empty when Deleted or when the
	// entity is a credential.
	Document json.RawMessage `json:"document,omitempty"`
	// Secret is a base64 credential value, present only for a credential kind.
	// It is re-sealed on the importing device.
	Secret string `json:"secret,omitempty"`
}

// Document is the whole package payload, before sealing.
//
// A package is a complete view of the configuration, not a diff: a device that
// leaves a kind out of its own settings still carries the kind's merged state
// through, so nothing a second device removed is resurrected and nothing it
// added is dropped. What a device writes into its own database is a decision
// made after the merge, from its own settings.
type Document struct {
	Version   int                          `json:"version"`
	DeviceID  string                       `json:"device_id"`
	Revision  int64                        `json:"revision"`
	Generated time.Time                    `json:"generated_at"`
	Entities  map[string]map[string]Entity `json:"entities"`
	// Order preserves the provider priority list, as entity ids.
	Order []string `json:"order,omitempty"`
}

// NewDocument returns an empty package for a device.
func NewDocument(deviceID string, revision int64, generated time.Time) Document {
	return Document{
		Version:   PackageVersion,
		DeviceID:  deviceID,
		Revision:  revision,
		Generated: generated,
		Entities:  map[string]map[string]Entity{},
	}
}

// Put records an entity in the package.
func (document *Document) Put(kind, id string, entity Entity) {
	if document.Entities == nil {
		document.Entities = map[string]map[string]Entity{}
	}
	if document.Entities[kind] == nil {
		document.Entities[kind] = map[string]Entity{}
	}
	document.Entities[kind][id] = entity
}

// Get returns an entity, and whether it was present.
func (document Document) Get(kind, id string) (Entity, bool) {
	byID, ok := document.Entities[kind]
	if !ok {
		return Entity{}, false
	}
	entity, ok := byID[id]
	return entity, ok
}

// Encode serializes the package payload.
func (document Document) Encode() ([]byte, error) {
	return json.Marshal(document)
}

// Decode reads a payload written by Encode. A package whose format version this
// build does not recognise is refused, so a future format is not read with this
// build's rules.
func Decode(encoded []byte) (Document, error) {
	var document Document
	if err := json.Unmarshal(encoded, &document); err != nil {
		return Document{}, fmt.Errorf("sync package is not readable: %w", err)
	}
	if document.Version != legacyPackageVersion && document.Version != PackageVersion {
		return Document{}, fmt.Errorf("sync package version %d is unsupported", document.Version)
	}
	if document.Entities == nil {
		document.Entities = map[string]map[string]Entity{}
	}
	return document, nil
}

// Notice is one merge decision the user may want to know about.
type Notice struct {
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Resolution string    `json:"resolution"`
	At         time.Time `json:"at"`
}

// Resolutions a conflict notice can carry.
const (
	// ResolutionLocalWon means this device's change was kept.
	ResolutionLocalWon = "local_kept"
	// ResolutionRemoteWon means the remote change was kept.
	ResolutionRemoteWon = "remote_kept"
	// ResolutionRemoved means a deletion beat a change on one side.
	ResolutionRemoved = "deleted"
	// ResolutionTieKeptLocal means both sides changed at the same instant and
	// this device's copy was kept.
	ResolutionTieKeptLocal = "tie_kept_local"
	// ResolutionRemoteAdopted means this device met the remote's configuration
	// for the first time and took the remote's copy of an entity it had not
	// changed itself.
	ResolutionRemoteAdopted = "remote_adopted"
	// ResolutionForcePushed means the remote was overwritten with this device's
	// configuration rather than merged with it.
	ResolutionForcePushed = "force_pushed"
)

// LocallyChanged names the entities this device changed itself, by kind and id.
// The change log is what fills it, and a device that has never synced has never
// cleared that log, so on a first contact it holds everything the user has
// configured here.
type LocallyChanged map[string]map[string]bool

// NewLocallyChanged returns an empty set.
func NewLocallyChanged() LocallyChanged {
	return LocallyChanged{}
}

// Add records one entity this device changed.
func (changed LocallyChanged) Add(kind, id string) {
	if changed[kind] == nil {
		changed[kind] = map[string]bool{}
	}
	changed[kind][id] = true
}

// Has reports whether this device changed one entity.
func (changed LocallyChanged) Has(kind, id string) bool {
	return changed[kind][id]
}

// MergeOptions tells a merge what it cannot read from the three documents.
type MergeOptions struct {
	// FirstContact reports that this device holds no package it last synced with
	// this remote, so an entity has no common ancestor to be compared against.
	FirstContact bool
	// LocallyChanged names the entities this device changed itself, which is how
	// a first contact tells the user's own work from a row the installation
	// seeded.
	LocallyChanged LocallyChanged
}

// Merge combines the base (last synced), local, and remote packages into the
// package this device should hold, and lists the decisions worth reporting.
//
// The rule per entity, keyed by kind and id:
//   - only one side differs from the base: that side wins.
//   - both sides differ: the later updated_at wins. A tie keeps the local copy
//     and records the conflict, so nothing is lost silently.
//   - a deletion is a change at its deleted_at, so a delete and an edit are
//     compared by time rather than one always beating the other.
//   - under options.FirstContact there is no base at all, and an entity this
//     device never changed comes from the remote: see the rule at mergeEntity.
func Merge(base, local, remote Document, options MergeOptions) (Document, []Notice, error) {
	merged := NewDocument(local.DeviceID, local.Revision+1, time.Now().UTC())
	merged.Entities = map[string]map[string]Entity{}
	var notices []Notice

	kinds := map[string]bool{}
	for kind := range base.Entities {
		kinds[kind] = true
	}
	for kind := range local.Entities {
		kinds[kind] = true
	}
	for kind := range remote.Entities {
		kinds[kind] = true
	}
	for kind := range kinds {
		ids := map[string]bool{}
		for id := range base.Entities[kind] {
			ids[id] = true
		}
		for id := range local.Entities[kind] {
			ids[id] = true
		}
		for id := range remote.Entities[kind] {
			ids[id] = true
		}
		for id := range ids {
			baseEntity := lookup(base, kind, id)
			localEntity := lookup(local, kind, id)
			remoteEntity := lookup(remote, kind, id)
			chosen, notice := mergeEntity(kind, id, baseEntity, localEntity, remoteEntity, options)
			if notice != nil {
				notices = append(notices, *notice)
			}
			if chosen == nil || chosen.Deleted {
				continue
			}
			merged.Put(kind, id, *chosen)
		}
	}
	merged.Order = mergeOrder(base.Order, local.Order, remote.Order)
	sort.Slice(notices, func(i, j int) bool {
		if notices[i].Kind != notices[j].Kind {
			return notices[i].Kind < notices[j].Kind
		}
		return notices[i].Name < notices[j].Name
	})
	return merged, notices, nil
}

// lookup returns an entity from a document, or nil for absent.
func lookup(document Document, kind, id string) *Entity {
	entity, ok := document.Get(kind, id)
	if !ok {
		return nil
	}
	return &entity
}

// mergeEntity applies the rule to one entity and returns the chosen copy, or
// nil for "absent". A notice is returned only for a genuine conflict.
func mergeEntity(kind, id string, base, local, remote *Entity, options MergeOptions) (*Entity, *Notice) {
	localDiffers := differs(base, local)
	remoteDiffers := differs(base, remote)
	switch {
	case !localDiffers && !remoteDiffers:
		return local, nil
	case localDiffers && !remoteDiffers:
		return local, nil
	case !localDiffers && remoteDiffers:
		return remote, nil
	}
	// Both sides hold a copy and there is no ancestor to compare them against:
	// this device is meeting the remote's configuration for the first time. The
	// remote's copy is the one the other devices already share, so it wins
	// unless this device changed the entity itself. Without that rule a device
	// that joins an existing remote would publish the rows its own installation
	// seeded — a default policy stamped when the device was installed is newer
	// than the policy the user edited on the other device, so comparing times
	// alone would revert the remote.
	if options.FirstContact && base == nil && !options.LocallyChanged.Has(kind, id) {
		return remote, adoptNotice(kind, id, remote)
	}
	// Both sides changed. Compare the two changes by time.
	localAt := eventTime(local)
	remoteAt := eventTime(remote)
	notice := func(entity *Entity, resolution string) *Notice {
		if entity == nil || entity.Deleted {
			return &Notice{Kind: kind, Name: id, Resolution: ResolutionRemoved, At: noticeTime(entity, localAt, remoteAt)}
		}
		return &Notice{Kind: kind, Name: id, Resolution: resolution, At: entity.UpdatedAt}
	}
	switch {
	case localAt.After(remoteAt):
		return local, notice(local, ResolutionLocalWon)
	case remoteAt.After(localAt):
		return remote, notice(remote, ResolutionRemoteWon)
	default:
		// The same instant: keep this device's copy so a merge is stable, and
		// report it, because the two sides genuinely disagree.
		if local != nil && !local.Deleted {
			return local, &Notice{Kind: kind, Name: id, Resolution: ResolutionTieKeptLocal, At: local.UpdatedAt}
		}
		if remote != nil && !remote.Deleted {
			return remote, &Notice{Kind: kind, Name: id, Resolution: ResolutionRemoteWon, At: remote.UpdatedAt}
		}
		return local, notice(local, ResolutionRemoved)
	}
}

// adoptNotice reports the remote's copy being taken over this device's own, so
// the user can see why a setting they never touched here came back changed.
func adoptNotice(kind, id string, remote *Entity) *Notice {
	if remote == nil || remote.Deleted {
		return &Notice{Kind: kind, Name: id, Resolution: ResolutionRemoved, At: eventTime(remote)}
	}
	return &Notice{Kind: kind, Name: id, Resolution: ResolutionRemoteAdopted, At: remote.UpdatedAt}
}

// differs reports whether an entity is not the base's copy. A nil side and an
// absent base are the same thing.
func differs(base, side *Entity) bool {
	if base == nil && side == nil {
		return false
	}
	if base == nil || side == nil {
		return true
	}
	if base.Deleted != side.Deleted || base.Secret != side.Secret {
		return true
	}
	return !jsonEqual(base.Document, side.Document)
}

func jsonEqual(left, right json.RawMessage) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return string(left) == string(right)
	}
	return reflect.DeepEqual(a, b)
}

func eventTime(entity *Entity) time.Time {
	if entity == nil {
		return time.Time{}
	}
	return entity.UpdatedAt
}

func noticeTime(entity *Entity, localAt, remoteAt time.Time) time.Time {
	if entity != nil && !entity.UpdatedAt.IsZero() {
		return entity.UpdatedAt
	}
	if localAt.After(remoteAt) {
		return localAt
	}
	return remoteAt
}

// mergeOrder resolves the provider priority list. When both sides changed the
// order, the remote wins and the local-only ids are appended, so no provider
// drops out of the list.
func mergeOrder(base, local, remote []string) []string {
	if equalStrings(local, remote) {
		return local
	}
	if equalStrings(local, base) {
		return remote
	}
	if equalStrings(remote, base) {
		return local
	}
	seen := map[string]bool{}
	merged := make([]string, 0, len(remote)+len(local))
	for _, id := range remote {
		if !seen[id] {
			seen[id] = true
			merged = append(merged, id)
		}
	}
	for _, id := range local {
		if !seen[id] {
			seen[id] = true
			merged = append(merged, id)
		}
	}
	return merged
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
