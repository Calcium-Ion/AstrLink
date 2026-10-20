// This file builds the local half of a package and describes what the scope
// selects. It is the reading side of a sync: everything here turns this
// device's rows into the entities a merge works on.

package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/syncseal"
)

// marshalKDF encodes Argon2id parameters for the key_params_json column. The
// shape is rawseal's, so the cached key's derivation is described the same way
// a password envelope's is.
func marshalKDF(params syncseal.KDFParams) ([]byte, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode kdf parameters: %w", err)
	}
	return encoded, nil
}

// inScope reports which kinds this device's settings select. A group is kept
// whole or left out whole, because its kinds name each other by id: a
// credential without its provider, or a price binding without its provider,
// would land on another device as a dangling reference.
func inScope(scope contract.SyncScope) map[string]bool {
	kinds := map[string]bool{}
	if scope.Services {
		kinds[KindService] = true
	}
	if scope.Services && scope.Credentials {
		kinds[KindServiceCredential] = true
		kinds[KindServiceProxyCredential] = true
		kinds[KindBuiltinToolCredential] = true
	}
	if scope.Policies {
		kinds[KindPolicy] = true
	}
	if scope.Services && scope.Pricing {
		kinds[KindPricingConfig] = true
	}
	if scope.Tokens {
		kinds[KindAccessToken] = true
	}
	return kinds
}

// localDocument is this device's package payload, as it stands now.
//
// It carries the kinds the settings select, and the tombstones for entities
// this device no longer holds as deleted entities. A deletion therefore travels
// with a time, which is what lets a later edit on another device beat it rather
// than being lost to it.
//
// snapshotAt is the moment the rows were read. The change log is only reconciled
// up to it, so a write that landed after the read is left in the log for the
// next run rather than being mistaken for a deletion of a row this snapshot did
// not see.
func (engine *Engine) localDocument(ctx context.Context, state storage.SyncStateRecord, base Document, snapshotAt time.Time) (Document, error) {
	document := NewDocument(state.DeviceID, base.Revision, snapshotAt)
	kinds := inScope(state.Config.Scope)
	for _, kind := range Kinds() {
		if !kinds[kind] {
			continue
		}
		entities, err := engine.store.SyncEntities(ctx, kind)
		if err != nil {
			return Document{}, err
		}
		for _, entity := range entities {
			record := Entity{Revision: base.Revision, UpdatedAt: entity.UpdatedAt}
			switch {
			case IsCredentialKind(kind):
				if len(entity.Secret) == 0 {
					continue
				}
				record.Secret = encodeSecret(entity.Secret)
			case kind == KindAccessToken:
				// A token must travel whole. An incomplete snapshot would turn
				// a read failure into a deletion on every other device.
				if len(entity.Secret) == 0 || len(entity.Document) == 0 {
					return Document{}, fmt.Errorf("%w: incomplete access token %s", storage.ErrInvalidRecord, entity.LocalID)
				}
				record.Document = append(json.RawMessage(nil), entity.Document...)
				record.Secret = encodeSecret(entity.Secret)
			default:
				record.Document = append(json.RawMessage(nil), entity.Document...)
			}
			document.Put(kind, entity.LocalID, record)
		}
	}
	tombstones, err := engine.store.Tombstones(ctx, time.Time{})
	if err != nil {
		return Document{}, err
	}
	for _, tombstone := range tombstones {
		if !kinds[tombstone.Kind] {
			continue
		}
		if _, held := document.Get(tombstone.Kind, tombstone.SyncID); held {
			// The entity is back on this device; its tombstone is stale and the
			// apply step drops it.
			continue
		}
		document.Put(tombstone.Kind, tombstone.SyncID, Entity{Deleted: true, UpdatedAt: tombstone.DeletedAt})
	}
	order, err := engine.store.SyncServiceOrder(ctx)
	if err != nil {
		return Document{}, err
	}
	for _, id := range order {
		document.Order = append(document.Order, string(id))
	}
	if err := engine.reconcileDeletions(ctx, document, kinds, snapshotAt); err != nil {
		return Document{}, err
	}
	return document, nil
}

// reconcileDeletions turns the change log into tombstones. A device deletes a
// provider through the ordinary API, which records a change but leaves no row
// behind to carry the deletion: the log entry is the only memory of it, and a
// successful sync clears the log. Recording a tombstone here is what makes the
// deletion outlive the log entry, so a device that was offline and lost its own
// merge base still learns the provider is gone instead of uploading it back.
func (engine *Engine) reconcileDeletions(ctx context.Context, document Document, kinds map[string]bool, through time.Time) error {
	changes, err := engine.store.ChangesSince(ctx, time.Time{})
	if err != nil {
		return err
	}
	for _, change := range changes {
		if !kinds[change.Kind] {
			continue
		}
		if !change.ChangedAt.IsZero() && change.ChangedAt.After(through) {
			// Outside the snapshot the document was built from.
			continue
		}
		if _, held := document.Get(change.Kind, change.EntityID); held {
			continue
		}
		at := change.ChangedAt
		if at.IsZero() {
			at = through
		}
		if err := engine.store.PutTombstone(ctx, change.Kind, change.EntityID, at); err != nil {
			return err
		}
		document.Put(change.Kind, change.EntityID, Entity{Deleted: true, UpdatedAt: at})
	}
	return nil
}

// restrict returns a copy of a package holding only the kinds a scope selects.
// It is how the merge is kept from reading "this device does not carry that
// kind" as "this device deleted it": a kind out of scope never reaches the
// merge on either side.
func restrict(document Document, kinds map[string]bool) Document {
	restricted := document
	restricted.Entities = map[string]map[string]Entity{}
	for kind, entities := range document.Entities {
		if !kinds[kind] {
			continue
		}
		restricted.Entities[kind] = entities
	}
	return restricted
}

// carryThrough preserves the kinds the merge never saw. A kind this device does
// not write is still part of the remote's configuration, so it travels through
// unchanged; dropping it would let one device's narrower settings delete the
// other device's data.
//
// Later sources win, so the caller passes the stale merge base first and the
// remote's current package last: an entity this device does not manage must
// come from the remote, or a device that turned a kind off would seal its own
// old copy back over a change the other device has already published.
func carryThrough(merged Document, kinds map[string]bool, sources ...Document) {
	for _, kind := range Kinds() {
		if kinds[kind] {
			continue
		}
		for _, source := range sources {
			entities, ok := source.Entities[kind]
			if !ok || len(entities) == 0 {
				continue
			}
			for id, entity := range entities {
				merged.Put(kind, id, entity)
			}
		}
	}
}

// pruneOrphans drops a credential or price binding whose provider is not in the
// package. The merge resolves a provider and its dependants separately, so a
// provider deleted by an older change can briefly leave them behind.
func pruneOrphans(document *Document) {
	for _, kind := range []string{KindPricingConfig, KindServiceCredential, KindServiceProxyCredential} {
		entities := document.Entities[kind]
		for id := range entities {
			if _, ok := document.Get(KindService, id); !ok {
				delete(entities, id)
			}
		}
	}
}

// apply writes the merged package into this device's store: the entities the
// merge chose, the providers it dropped, the priority order, and the
// device-local credential references.
//
// Only the kinds this device's settings select are written. A kind the settings
// exclude is carried by the package but left alone here, so leaving a setting
// off never deletes data on the device that kept it on.
func (engine *Engine) apply(ctx context.Context, merged Document, scope contract.SyncScope) error {
	kinds := inScope(scope)
	local, err := engine.currentEntities(ctx, kinds)
	if err != nil {
		return err
	}
	if err := engine.writeEntities(ctx, merged, local, kinds); err != nil {
		return err
	}
	if err := engine.deleteEntities(ctx, merged, local, kinds); err != nil {
		return err
	}
	if kinds[KindService] {
		order := make([]contract.ServiceID, 0, len(merged.Order))
		for _, id := range merged.Order {
			order = append(order, contract.ServiceID(id))
		}
		if err := engine.store.SyncWriteOrder(ctx, order); err != nil {
			return err
		}
	}
	// The references are bound last, once every credential row the merge decided
	// on is in place, so a reference never names a row that is not there.
	return engine.store.SyncBindCredentialRefs(ctx)
}

// currentEntities is what this device holds now, keyed by kind and id, so the
// apply step can tell a write from a deletion.
func (engine *Engine) currentEntities(ctx context.Context, kinds map[string]bool) (map[string]map[string]bool, error) {
	held := map[string]map[string]bool{}
	for _, kind := range Kinds() {
		if !kinds[kind] {
			continue
		}
		entities, err := engine.store.SyncEntities(ctx, kind)
		if err != nil {
			return nil, err
		}
		held[kind] = map[string]bool{}
		for _, entity := range entities {
			held[kind][entity.LocalID] = true
		}
	}
	return held, nil
}

func (engine *Engine) writeEntities(ctx context.Context, merged Document, held map[string]map[string]bool, kinds map[string]bool) error {
	// Providers first: every other kind references them, and a credential or a
	// price binding for a provider that is not written yet has nowhere to go.
	order := []string{KindService, KindPolicy, KindPricingConfig, KindAccessToken}
	order = append(order, CredentialKinds()...)
	for _, kind := range order {
		if !kinds[kind] {
			continue
		}
		for id, entity := range merged.Entities[kind] {
			if entity.Deleted {
				continue
			}
			if err := engine.writeEntity(ctx, kind, id, entity); err != nil {
				return err
			}
		}
	}
	return nil
}

func (engine *Engine) writeEntity(ctx context.Context, kind, id string, entity Entity) error {
	if kind == KindAccessToken {
		return engine.writeAccessToken(ctx, id, entity)
	}
	if IsCredentialKind(kind) {
		plaintext, err := decodeSecret(entity.Secret)
		if err != nil {
			return err
		}
		defer clear(plaintext)
		if err := engine.store.SyncImportCredential(ctx, kind, id, plaintext); err != nil {
			return err
		}
		return engine.store.DeleteTombstone(ctx, kind, id)
	}
	if len(entity.Document) == 0 {
		return fmt.Errorf("%w: %s %s has no document", syncseal.ErrPackage, kind, id)
	}
	if err := engine.store.SyncWriteEntity(ctx, kind, id, entity.Document); err != nil {
		if errors.Is(err, storage.ErrInvalidArgument) {
			// A document this build refuses is not stored, but it must not stop
			// the rest of the merge: the other device still holds it and the
			// package still carries it.
			engine.logf("sync skipped %s %s: %v", kind, id, err)
			return nil
		}
		if errors.Is(err, secretstore.ErrNotFound) {
			// The provider this row belongs to is not on this device.
			return nil
		}
		return err
	}
	return engine.store.DeleteTombstone(ctx, kind, id)
}

// writeAccessToken stores one access token a merge chose. It is written whole,
// because the store derives the digest and the hint an authentication reads from
// the value: metadata stored on its own would name a token nobody can present.
func (engine *Engine) writeAccessToken(ctx context.Context, id string, entity Entity) error {
	if len(entity.Document) == 0 {
		return fmt.Errorf("%w: %s %s has no document", syncseal.ErrPackage, KindAccessToken, id)
	}
	value, err := decodeSecret(entity.Secret)
	if err != nil {
		return err
	}
	defer clear(value)
	if err := engine.store.SyncWriteAccessToken(ctx, id, entity.Document, value); err != nil {
		if errors.Is(err, storage.ErrInvalidArgument) {
			// A token this build refuses — a value that is not a canonical
			// token, or a name it cannot use — is not stored, but it must not
			// stop the rest of the merge: the other device still holds it and
			// the package still carries it.
			engine.logf("sync skipped %s %s: %v", KindAccessToken, id, err)
			return nil
		}
		return err
	}
	return engine.store.DeleteTombstone(ctx, KindAccessToken, id)
}

// deleteEntities removes what this device holds and the merge did not keep. A
// deletion is also recorded as a tombstone, so a device that still has the
// entity learns it is gone instead of uploading it again.
func (engine *Engine) deleteEntities(ctx context.Context, merged Document, held map[string]map[string]bool, kinds map[string]bool) error {
	for _, kind := range Kinds() {
		if !kinds[kind] {
			continue
		}
		for id := range held[kind] {
			if _, kept := merged.Get(kind, id); kept {
				continue
			}
			if IsCredentialKind(kind) {
				if err := engine.store.SyncDeleteCredential(ctx, kind, id); err != nil {
					return err
				}
			} else if err := engine.store.SyncDeleteEntity(ctx, kind, id); err != nil {
				return err
			}
			if err := engine.store.PutTombstone(ctx, kind, id, engine.now().UTC()); err != nil {
				return err
			}
		}
	}
	return nil
}
