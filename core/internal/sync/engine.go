// This file is the engine: the part that reads this device's configuration,
// brings it together with what the remote holds, and writes back.
//
// One run is one compare-and-swap cycle. The engine takes the remote's single
// write lock, reads the stored package, merges it with what this device has,
// writes the result, and only then records that the run succeeded. A run that
// loses the lock or fails to write changes nothing about the last success, so
// the next run retries from the same state.
//
// The package is sealed under the sync passphrase, and the passphrase's derived
// key is cached sealed under the local key hierarchy, so automatic sync works
// without the passphrase while a copy of the database alone decrypts nothing.

package sync

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/rawseal"
	"github.com/QuantumNous/astrlink/core/internal/storage/syncseal"
	"github.com/QuantumNous/astrlink/core/internal/sync/remote"
	"github.com/QuantumNous/astrlink/core/internal/sync/schedule"
)

var (
	// ErrNotConfigured means sync is off, has no remote, or the remote settings
	// are incomplete, so there is nothing to run.
	ErrNotConfigured = errors.New("sync is not configured")
	// ErrPassphraseRequired means no sync passphrase has been set on this
	// device, or the cached key was cleared, so the engine cannot open or
	// write a package.
	ErrPassphraseRequired = errors.New("sync passphrase is required")
	// ErrPassphraseMismatch means the stored package was sealed with a
	// different passphrase than the one this device holds. It is never
	// overwritten automatically: that would destroy the other device's
	// configuration.
	ErrPassphraseMismatch = errors.New("the stored package was sealed with a different sync passphrase")
	// ErrBusy means another device holds the remote lock. It is a normal
	// outcome of a run, not a failure.
	ErrBusy = remote.ErrLocked
	// ErrRemoteChanged means the remote at the configured location is not the
	// one this device last confirmed. A package is never exchanged with it.
	ErrRemoteChanged = errors.New("the remote is not the one this device last synced with")
)

// Debounce bounds and the poll interval the scheduler wakes on.
const (
	defaultDebounceSeconds = 30
	// pollInterval is how often the scheduler looks for work. It is shorter
	// than the shortest interval and the shortest debounce, so a due run starts
	// close to when it became due.
	pollInterval = 5 * time.Second
	// runTimeout bounds one sync, including the remote round trips.
	runTimeout = 5 * time.Minute
	// maxWriteAttempts bounds the read-merge-write retries when another writer
	// changes the package between the read and the write.
	maxWriteAttempts = 3
	// noticeLimit is how many merge decisions are kept for the status surface.
	noticeLimit = 20
)

// Dependencies are what the engine needs from the process around it.
type Dependencies struct {
	Store  storage.SyncStore
	Client *http.Client
	// Logf reports a failed run. It is never called for a cancelled context.
	Logf func(string, ...any)
	// Now is the clock, injectable for tests.
	Now func() time.Time
	// LeaseTTL is how long the remote lock is held. Zero uses the default.
	LeaseTTL time.Duration
	// Hostname is how a device with no configured name names itself, defaulting
	// to the machine's own host name.
	Hostname func() (string, error)
}

// Engine runs the configuration sync for one device.
type Engine struct {
	store    storage.SyncStore
	client   *http.Client
	logf     func(string, ...any)
	now      func() time.Time
	leaseTTL time.Duration
	hostname func() (string, error)

	// runMu serializes runs in this process, so a scheduled run and a
	// user-initiated one never overlap. The remote lock covers other devices;
	// this covers this one.
	runMu   sync.Mutex
	running bool

	noticeMu sync.Mutex
	notices  []contract.SyncConflictNotice
	// lastAttempt is when a run was last started, which is how a failed run
	// backs off to the next interval rather than retrying in a tight loop.
	lastAttempt time.Time
}

// New builds an engine.
func New(dependencies Dependencies) (*Engine, error) {
	if dependencies.Store == nil {
		return nil, fmt.Errorf("sync store is required")
	}
	now := dependencies.Now
	if now == nil {
		now = time.Now
	}
	logf := dependencies.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	client := dependencies.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	hostname := dependencies.Hostname
	if hostname == nil {
		hostname = os.Hostname
	}
	return &Engine{
		store:    dependencies.Store,
		client:   client,
		logf:     logf,
		now:      now,
		leaseTTL: dependencies.LeaseTTL,
		hostname: hostname,
	}, nil
}

// deviceName is what this device calls itself: the configured name when there is
// one, otherwise the host's. The identity the remote lock uses stays separate,
// so renaming a device never disturbs a held lock.
func (engine *Engine) deviceName(config contract.SyncConfig) string {
	if name := strings.TrimSpace(config.DeviceName); name != "" {
		return name
	}
	return engine.hostName()
}

// hostName is the machine's own name, which is what a blank device name falls
// back to and what the settings show as its placeholder.
func (engine *Engine) hostName() string {
	host, err := engine.hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(host)
}

// Configure validates and stores the user's sync settings.
//
// A configuration that points somewhere else is a different package, so the
// merge base and the pinned remote are dropped: without that, every entity on
// the new remote would look like a remote edit and the two configurations would
// be merged into one.
func (engine *Engine) Configure(ctx context.Context, config contract.SyncConfig) error {
	if err := contract.ValidateSyncConfig(config); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	if config.Trigger.Mode == contract.SyncTriggerCron {
		if _, err := schedule.Parse(config.Trigger.Cron); err != nil {
			return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
		}
	}
	before, err := engine.store.SyncState(ctx)
	if err != nil {
		return err
	}
	if err := engine.store.UpdateSyncConfig(ctx, config); err != nil {
		return err
	}
	if before.Config.Remote != config.Remote {
		return engine.store.ResetBaseline(ctx)
	}
	return nil
}

// SetPassphrase derives the package key from the user's passphrase, caches it
// sealed under the local key hierarchy, and verifies it against the stored
// package when one exists. A passphrase that does not open the stored package
// is reported rather than accepted, because accepting it would seal the next
// package with a key the other device cannot open.
//
// The salt is a function of the passphrase, so every device that knows the same
// passphrase derives the same key and no device needs anything else to join an
// existing remote.
func (engine *Engine) SetPassphrase(ctx context.Context, passphrase []byte) error {
	if err := syncseal.ValidatePassphrase(passphrase); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	if err := engine.verifyPassphrase(ctx, passphrase); err != nil {
		return err
	}
	salt := syncseal.PassphraseSalt(passphrase)
	params := syncseal.DefaultKDF
	key, err := syncseal.DeriveKey(passphrase, salt, params)
	if err != nil {
		return err
	}
	defer clear(key)
	encoded, err := marshalKDF(params)
	if err != nil {
		return err
	}
	return engine.store.PutSyncKey(ctx, key, salt, encoded)
}

// verifyPassphrase checks a candidate passphrase against the stored package. A
// remote that does not answer leaves the check to the next run: refusing a
// passphrase because a server is down would lock the user out of their own
// settings.
func (engine *Engine) verifyPassphrase(ctx context.Context, passphrase []byte) error {
	state, err := engine.store.SyncState(ctx)
	if err != nil {
		return err
	}
	if !state.Configured {
		return nil
	}
	target, err := engine.remote(state)
	if err != nil {
		return nil
	}
	payload, _, exists, err := target.Read(ctx)
	if err != nil || !exists {
		return nil
	}
	pkg, err := syncseal.Parse(payload)
	if err != nil {
		return fmt.Errorf("%w: %v", syncseal.ErrPackage, err)
	}
	key, err := syncseal.DeriveKey(passphrase, pkg.Salt, pkg.KDF)
	if err != nil {
		return nil
	}
	defer clear(key)
	if !syncseal.KeyMatches(pkg, key) {
		return ErrPassphraseMismatch
	}
	return nil
}

// Config returns the settings as stored, so the settings surface shows what
// the engine will actually use rather than what a page last held.
func (engine *Engine) Config(ctx context.Context) (contract.SyncConfig, error) {
	state, err := engine.store.SyncState(ctx)
	if err != nil {
		return contract.SyncConfig{}, err
	}
	return state.Config, nil
}

// ClearPassphrase forgets the cached key. The remote settings stay, so entering
// the passphrase again resumes sync.
func (engine *Engine) ClearPassphrase(ctx context.Context) error {
	return engine.store.ClearSyncKey(ctx)
}

// Status reports what the settings surface shows.
func (engine *Engine) Status(ctx context.Context) (contract.SyncStatus, error) {
	state, err := engine.store.SyncState(ctx)
	if err != nil {
		return contract.SyncStatus{}, err
	}
	pending, err := engine.store.PendingChanges(ctx)
	if err != nil {
		return contract.SyncStatus{}, err
	}
	status := contract.SyncStatus{
		Enabled:          state.Config.Enabled,
		PassphraseSet:    state.PassphraseState == "set",
		RemoteConfigured: state.Configured,
		Configured:       state.Configured && state.PassphraseState == "set",
		DeviceID:         state.DeviceID,
		DeviceName:       engine.deviceName(state.Config),
		HostName:         engine.hostName(),
		PendingChanges:   pending,
		LastSyncedAt:     state.LastSyncedAt,
		LastError:        state.LastError,
		Conflicts:        engine.recentNotices(),
	}
	engine.runMu.Lock()
	status.Running = engine.running
	lastAttempt := engine.lastAttempt
	engine.runMu.Unlock()
	status.NextRunAt = nextRunAt(state, lastAttempt, engine.now())
	return status, nil
}

// nextRunAt is when the scheduler will next start a run. A manual trigger has
// no next run, and a device that is not fully configured reports none either,
// so the settings surface never promises a run that cannot happen.
func nextRunAt(state storage.SyncStateRecord, lastAttempt time.Time, now time.Time) *time.Time {
	if !state.Config.Enabled || !state.Configured || state.PassphraseState != "set" {
		return nil
	}
	from := now
	if state.LastSyncedAt != nil && state.LastSyncedAt.After(from) {
		from = *state.LastSyncedAt
	}
	if lastAttempt.After(from) {
		from = lastAttempt
	}
	switch state.Config.Trigger.Mode {
	case contract.SyncTriggerInterval:
		minutes := state.Config.Trigger.IntervalMinutes
		if minutes < contract.MinSyncIntervalMinutes {
			minutes = contract.MinSyncIntervalMinutes
		}
		next := from.Add(time.Duration(minutes) * time.Minute)
		return &next
	case contract.SyncTriggerCron:
		expression, err := schedule.Parse(state.Config.Trigger.Cron)
		if err != nil {
			return nil
		}
		next, err := expression.Next(from)
		if err != nil {
			return nil
		}
		return &next
	default:
		return nil
	}
}

// Run serves until the context is cancelled, starting a sync whenever the
// settings say one is due. It reports failures through the log and the status
// row, never by stopping: a remote that is down must not end the loop.
func (engine *Engine) Run(ctx context.Context, logf func(string, ...any)) {
	if logf != nil {
		engine.logf = logf
	}
	for {
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}
		due, err := engine.due(ctx)
		if err != nil {
			if ctx.Err() == nil {
				engine.logf("sync schedule: %v", err)
			}
			continue
		}
		if !due {
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, runTimeout)
		_, err = engine.Sync(runCtx)
		cancel()
		if err != nil && ctx.Err() == nil && !errors.Is(err, ErrBusy) {
			engine.logf("sync: %v", err)
		}
	}
}

// due reports whether the settings now call for a run.
//
// An interval or cron trigger runs when its moment arrives, and also when
// configuration was saved and has been left alone for the debounce, so a save
// brings the next sync forward instead of waiting out the interval. A manual
// trigger never runs on its own.
func (engine *Engine) due(ctx context.Context) (bool, error) {
	state, err := engine.store.SyncState(ctx)
	if err != nil {
		return false, err
	}
	if !state.Config.Enabled || !state.Configured || state.PassphraseState != "set" {
		return false, nil
	}
	engine.runMu.Lock()
	lastAttempt := engine.lastAttempt
	engine.runMu.Unlock()
	now := engine.now()
	if next := nextRunAt(state, lastAttempt, now); next != nil && !now.Before(*next) {
		return true, nil
	}
	if state.Config.Trigger.Mode == contract.SyncTriggerManual {
		return false, nil
	}
	pending, err := engine.store.PendingChanges(ctx)
	if err != nil {
		return false, err
	}
	if pending == 0 {
		return false, nil
	}
	changes, err := engine.store.ChangesSince(ctx, time.Time{})
	if err != nil {
		return false, err
	}
	debounce := time.Duration(debounceSeconds(state.Config)) * time.Second
	for _, change := range changes {
		if now.Sub(change.ChangedAt) >= debounce {
			return true, nil
		}
	}
	return false, nil
}

func debounceSeconds(config contract.SyncConfig) int {
	if config.Trigger.DebounceSeconds >= contract.MinSyncDebounceSeconds {
		return config.Trigger.DebounceSeconds
	}
	return defaultDebounceSeconds
}

// Sync runs one sync now, whatever the schedule says. It is what the control
// API's manual run and the desktop's "sync now" call.
func (engine *Engine) Sync(ctx context.Context) (contract.SyncStatus, error) {
	engine.runMu.Lock()
	if engine.running {
		engine.runMu.Unlock()
		return contract.SyncStatus{}, fmt.Errorf("%w: a sync is already running", storage.ErrConflict)
	}
	engine.running = true
	engine.lastAttempt = engine.now()
	engine.runMu.Unlock()
	defer func() {
		engine.runMu.Lock()
		engine.running = false
		engine.runMu.Unlock()
	}()

	err := engine.syncOnce(ctx)
	// A failed run must still leave the status readable, so the error is
	// recorded and also returned.
	if err != nil {
		engine.recordFailure(ctx, err)
	}
	status, statusErr := engine.Status(ctx)
	if statusErr != nil {
		return contract.SyncStatus{}, statusErr
	}
	return status, err
}

func (engine *Engine) recordFailure(ctx context.Context, err error) {
	if errors.Is(err, ErrBusy) {
		// Another device is syncing. That is not this device's failure.
		return
	}
	if ctx.Err() != nil {
		return
	}
	_ = engine.store.RecordSyncResult(ctx, contract.RemoteIdentity{}, nil, err.Error())
}

// syncOnce is one compare-and-swap cycle against the remote.
//
// The whole cycle repeats when the conditional write is refused, because that
// means another device wrote the package between this device's read and write.
// The merge is idempotent, so repeating it with the newer package is safe.
func (engine *Engine) syncOnce(ctx context.Context) error {
	state, err := engine.store.SyncState(ctx)
	if err != nil {
		return err
	}
	if !state.Config.Enabled {
		return ErrNotConfigured
	}
	if !state.Configured {
		return fmt.Errorf("%w: no remote is set", ErrNotConfigured)
	}
	if err := contract.ValidateSyncConfig(state.Config); err != nil {
		return fmt.Errorf("%w: %v", ErrNotConfigured, err)
	}
	key, salt, err := engine.store.SyncKey(ctx)
	if err != nil {
		if errors.Is(err, secretstore.ErrNotFound) {
			return ErrPassphraseRequired
		}
		return err
	}
	defer clear(key)
	params, err := rawseal.ParseKDFJSON(string(state.KeyParams))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPassphraseRequired, err)
	}
	target, err := engine.remote(state)
	if err != nil {
		return err
	}
	identity, err := target.Identity(ctx)
	if err != nil {
		return err
	}
	if state.RemoteIdentity != "" && state.RemoteIdentity != identity {
		// The configuration points somewhere else than the package this device
		// last agreed with. Merging the two would import unrelated settings, so
		// the merge base is dropped, the new remote is pinned, and the change is
		// reported once.
		if err := engine.store.ResetBaseline(ctx); err != nil {
			return err
		}
		if err := engine.store.RecordSyncResult(ctx, contract.RemoteIdentity{Identity: identity}, nil,
			ErrRemoteChanged.Error()); err != nil {
			return err
		}
		return ErrRemoteChanged
	}

	lease, err := remote.Acquire(ctx, target, state.DeviceID, engine.leaseTTL, engine.now)
	if err != nil {
		return err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := lease.Release(releaseCtx); err != nil {
			engine.logf("sync lock release: %v", err)
		}
	}()

	base, err := engine.baseline(ctx, state)
	if err != nil {
		return err
	}
	scope := state.Config.Scope
	kinds := inScope(scope)
	// snapshotAt is the moment this device's rows are read. The change log is
	// cleared only through it, so a save that lands during the run stays in the
	// log and is packaged by the next one instead of being marked as synced.
	snapshotAt := engine.now().UTC()
	for attempt := 0; attempt < maxWriteAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		remoteDoc, etag, exists, err := engine.readRemote(ctx, target, key)
		if err != nil {
			return err
		}
		local, err := engine.localDocument(ctx, state, base, snapshotAt)
		if err != nil {
			return err
		}
		// Only the kinds this device's settings select take part in the merge.
		// The rest travel through unchanged, so a setting turned off here never
		// reads as a deletion of the other device's data.
		merged, notices, err := Merge(restrict(base, kinds), local, restrict(remoteDoc, kinds))
		if err != nil {
			return err
		}
		carryThrough(merged, kinds, remoteDoc, base)
		pruneOrphans(&merged)
		if err := engine.apply(ctx, merged, scope); err != nil {
			return err
		}
		encoded, err := merged.Encode()
		if err != nil {
			return fmt.Errorf("encode sync package: %w", err)
		}
		pkg, err := syncseal.SealWithKey(key, encoded, salt, params)
		if err != nil {
			return err
		}
		payload, err := syncseal.Marshal(pkg)
		if err != nil {
			return err
		}
		expected := etag
		if !exists {
			expected = ""
		}
		storedTag, err := target.Write(ctx, payload, expected)
		if errors.Is(err, remote.ErrPrecondition) {
			// Another device wrote the package first. Read it again and redo the
			// merge against it rather than overwriting its change.
			continue
		}
		if err != nil {
			return err
		}
		through := snapshotAt
		syncedAt := engine.now().UTC()
		if err := engine.store.PutBaseline(ctx, encoded, merged.Revision); err != nil {
			return err
		}
		if err := engine.store.RecordSyncResult(ctx, contract.RemoteIdentity{
			Identity: identity, ETag: storedTag, Revision: merged.Revision,
		}, &syncedAt, ""); err != nil {
			return err
		}
		if err := engine.store.ClearChangesThrough(ctx, through); err != nil {
			return err
		}
		if err := engine.store.RetainTombstonesThrough(ctx, through); err != nil {
			return err
		}
		if err := engine.store.DeleteTombstonesBefore(ctx, through.Add(-tombstoneRetention)); err != nil {
			return err
		}
		engine.rememberNotices(notices)
		return nil
	}
	return fmt.Errorf("%w: the remote package kept changing", storage.ErrConflict)
}

// tombstoneRetention is how long a deletion is remembered. It bounds how long
// a device that was offline can come back and still learn about it.
const tombstoneRetention = 90 * 24 * time.Hour

// baseline returns the package this device last synced with, or an empty one.
// A base this build cannot read is dropped rather than blocking sync.
func (engine *Engine) baseline(ctx context.Context, state storage.SyncStateRecord) (Document, error) {
	encoded, _, err := engine.store.Baseline(ctx)
	if err != nil {
		if errors.Is(err, secretstore.ErrNotFound) {
			return NewDocument(state.DeviceID, 0, time.Time{}), nil
		}
		return Document{}, err
	}
	decoded, err := Decode(encoded)
	if err != nil {
		engine.logf("sync baseline reset: %v", err)
		if err := engine.store.ResetBaseline(ctx); err != nil {
			return Document{}, err
		}
		return NewDocument(state.DeviceID, 0, time.Time{}), nil
	}
	return decoded, nil
}

// readRemote returns the package stored at the remote, opened with this
// device's cached key. A package that does not open is reported as a passphrase
// mismatch rather than replaced: overwriting it would destroy the other
// device's configuration.
func (engine *Engine) readRemote(ctx context.Context, target remote.Remote, key []byte) (Document, string, bool, error) {
	payload, etag, exists, err := target.Read(ctx)
	if err != nil {
		return Document{}, "", false, err
	}
	if !exists {
		return NewDocument("", 0, time.Time{}), etag, false, nil
	}
	pkg, err := syncseal.Parse(payload)
	if err != nil {
		return Document{}, "", false, fmt.Errorf("%w: %v", syncseal.ErrPackage, err)
	}
	plaintext, err := syncseal.OpenWithKey(pkg, key)
	if err != nil {
		return Document{}, "", false, ErrPassphraseMismatch
	}
	defer clear(plaintext)
	decoded, err := Decode(plaintext)
	if err != nil {
		return Document{}, "", false, err
	}
	return decoded, etag, true, nil
}

func (engine *Engine) remote(state storage.SyncStateRecord) (remote.Remote, error) {
	return remote.New(remote.Config{
		Kind:       state.Config.Remote.Kind,
		Endpoint:   state.Config.Remote.Endpoint,
		Path:       state.Config.Remote.Path,
		Region:     state.Config.Remote.Region,
		Username:   state.Config.Remote.Username,
		Password:   state.Config.Remote.Password,
		AccessKey:  state.Config.Remote.AccessKey,
		SecretKey:  state.Config.Remote.SecretKey,
		Addressing: state.Config.Remote.Addressing,
	}, engine.client, engine.now)
}

// rememberNotices keeps the most recent merge decisions for the status surface.
func (engine *Engine) rememberNotices(notices []Notice) {
	if len(notices) == 0 {
		return
	}
	engine.noticeMu.Lock()
	defer engine.noticeMu.Unlock()
	for _, notice := range notices {
		engine.notices = append(engine.notices, contract.SyncConflictNotice{
			Kind:       notice.Kind,
			Name:       notice.Name,
			Resolution: notice.Resolution,
			At:         notice.At,
		})
	}
	if len(engine.notices) > noticeLimit {
		engine.notices = engine.notices[len(engine.notices)-noticeLimit:]
	}
}

// recentNotices returns the kept merge decisions, newest last.
func (engine *Engine) recentNotices() []contract.SyncConflictNotice {
	engine.noticeMu.Lock()
	defer engine.noticeMu.Unlock()
	if len(engine.notices) == 0 {
		return nil
	}
	return append([]contract.SyncConflictNotice(nil), engine.notices...)
}

// encodeSecret is how a credential travels inside a package: base64 of the
// plaintext, which the sealing layer then protects.
func encodeSecret(plaintext []byte) string {
	return base64.StdEncoding.EncodeToString(plaintext)
}

func decodeSecret(encoded string) ([]byte, error) {
	plaintext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: credential is not base64", syncseal.ErrPackage)
	}
	return plaintext, nil
}
