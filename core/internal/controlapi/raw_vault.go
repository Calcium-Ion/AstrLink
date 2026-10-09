package controlapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/rawseal"
)

const (
	// rawUnlockIdle ends the operator's unlock session after this long
	// without a raw read (D13).
	rawUnlockIdle = 15 * time.Minute
	// rawBackoffFreeFailures wrong proofs in a row are answered at once;
	// the next ones wait 2^(n-3) s, at most rawBackoffCap (§5.11.9.6)
	// unless RawVaultOptions.BackoffCap raises it.
	rawBackoffFreeFailures = 2
	rawBackoffCap          = 30 * time.Second
	// rawResealBatch parts are committed per reseal transaction.
	rawResealBatch = 100
)

var (
	// ErrRawPasswordAlreadySet means set was asked for while a password
	// envelope exists; change replaces it.
	ErrRawPasswordAlreadySet = errors.New("a raw password is already set")
	// ErrRawPasswordNotSet means change was asked for without a password.
	ErrRawPasswordNotSet = errors.New("no raw password is set")
	// ErrRawPasswordRequired means an action needs a new password that was
	// not given.
	ErrRawPasswordRequired = errors.New("a new raw password is required")
	// ErrRawPasswordPolicy means a new password is outside the length policy.
	ErrRawPasswordPolicy = rawseal.ErrPasswordPolicy
)

// RawPasswordAction is one operation on the raw password.
type RawPasswordAction string

const (
	// RawPasswordSet adds the first password envelope, creating the key pair
	// when none exists yet.
	RawPasswordSet RawPasswordAction = "set"
	// RawPasswordChange rewraps the private key under a new password.
	RawPasswordChange RawPasswordAction = "change"
	// RawPasswordReset replaces the key pair and discards every part sealed
	// to the old one.
	RawPasswordReset RawPasswordAction = "reset"
)

// RawPasswordOutcome reports a password action. Reset is set only for a
// reset.
type RawPasswordOutcome struct {
	Status RawVaultStatus
	Reset  *storage.RawResetResult
}

// RawVaultController is the operator side of the vault. A RawVault that does
// not implement it cannot be unlocked or given a password.
type RawVaultController interface {
	// Unlock proves access and keeps the private key for the idle window.
	Unlock(context.Context, RawProof) (RawVaultStatus, error)
	// Verify checks a proof like Unlock, under the same backoff, but zeroes
	// the private key at once and leaves the unlock session as it was.
	Verify(context.Context, RawProof) (RawVaultStatus, error)
	// Lock ends the unlock session at once.
	Lock()
	// ChangePassword applies action. password is the new password and
	// belongs to the caller; proof opens the existing key where needed.
	ChangePassword(ctx context.Context, action RawPasswordAction, password []byte, proof RawProof) (RawPasswordOutcome, error)
}

// RawVaultOptions configures NewRawVault.
type RawVaultOptions struct {
	// KDF overrides the Argon2id parameters; zero means rawseal.DefaultKDF.
	KDF rawseal.KDFParams
	// Logf receives state changes; it never sees key material or passwords.
	Logf func(string, ...any)
	// ReadOnly is for a store opened read-only: a proof never repairs the
	// public key MAC.
	ReadOnly bool
	// Now is for tests; nil uses time.Now.
	Now func() time.Time
	// BackoffCap bounds the wait after wrong proofs; zero keeps
	// rawBackoffCap. The server edition, whose raw password also signs in
	// to a console other machines reach, raises it. The backoff is kept in
	// memory only, so a restart clears it.
	BackoffCap time.Duration
}

// Vault guards the raw sealing private key inside Core (§5.11.9.4). The
// private key is in memory only while a proof is checked, and while the
// operator's unlock session lasts. Argon2id runs one at a time and never
// inside a database transaction.
type Vault struct {
	store    storage.RawSealingStore
	readOnly bool
	kdf      rawseal.KDFParams
	logf     func(string, ...any)
	now      func() time.Time
	// slot admits one key opening or password action at a time, so parallel
	// guesses queue behind the backoff instead of racing past it.
	slot   chan struct{}
	reseal chan struct{}

	mu          sync.Mutex
	stateLoaded bool
	configured  bool
	state       storage.RawSealingState

	session      []byte
	sessionKeyID int64
	sessionUntil time.Time
	sessionTimer *time.Timer

	failures     int
	blockedUntil time.Time
	backoffCap   time.Duration

	// privateCleared, when set by tests, sees each proof-opened private key
	// right after the vault zeroes it.
	privateCleared func([]byte)
}

var (
	_ RawVault           = (*Vault)(nil)
	_ RawVaultController = (*Vault)(nil)
)

// NewRawVault returns a vault over store. Call Run to reseal in the
// background.
func NewRawVault(store storage.RawSealingStore, options RawVaultOptions) *Vault {
	kdf := options.KDF
	if kdf == (rawseal.KDFParams{}) {
		kdf = rawseal.DefaultKDF
	}
	logf := options.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	vault := &Vault{
		store:    store,
		readOnly: options.ReadOnly,
		kdf:      kdf,
		logf:     logf,
		now:      now,
		slot:     make(chan struct{}, 1),
		reseal:   make(chan struct{}, 1),
	}
	vault.backoffCap = options.BackoffCap
	if vault.backoffCap <= 0 {
		vault.backoffCap = rawBackoffCap
	}
	// The first pass picks up parts left under the audit key by an earlier
	// run that stopped mid-reseal.
	vault.reseal <- struct{}{}
	return vault
}

// Run reseals raw parts onto the raw sealing key whenever one appears or the
// store reports a settled part it could not move itself, until ctx ends.
func (vault *Vault) Run(ctx context.Context) {
	vault.store.OnResealDeferred(vault.requestReseal)
	defer vault.store.OnResealDeferred(nil)
	for {
		select {
		case <-ctx.Done():
			return
		case <-vault.reseal:
		}
		result, err := vault.store.ResealRawParts(ctx, rawResealBatch)
		if err != nil && ctx.Err() == nil {
			vault.logf("raw sealing: reseal stopped: %v", err)
		}
		if result.Dropped > 0 {
			vault.logf("raw sealing: discarded %d raw part(s) captured while no raw password was set", result.Dropped)
		}
		if result.Resealed > 0 {
			vault.logf("raw sealing: moved %d captured part(s) onto the raw sealing key", result.Resealed)
		}
	}
}

func (vault *Vault) requestReseal() {
	select {
	case vault.reseal <- struct{}{}:
	default:
	}
}

func (vault *Vault) acquire(ctx context.Context) error {
	select {
	case vault.slot <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (vault *Vault) release() { <-vault.slot }

// loadLocked reads the stored key once; mutations call reloadLocked.
func (vault *Vault) loadLocked(ctx context.Context) error {
	if vault.stateLoaded {
		return nil
	}
	return vault.reloadLocked(ctx)
}

func (vault *Vault) reloadLocked(ctx context.Context) error {
	state, err := vault.store.LoadRawSealing(ctx)
	if errors.Is(err, storage.ErrNotFound) {
		vault.stateLoaded, vault.configured = true, false
		vault.state = storage.RawSealingState{}
		return nil
	}
	if err != nil {
		return err
	}
	vault.stateLoaded, vault.configured, vault.state = true, true, state
	if vault.session != nil && vault.sessionKeyID != state.KeyID {
		vault.clearSessionLocked()
	}
	return nil
}

func (vault *Vault) reload(ctx context.Context) error {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	return vault.reloadLocked(ctx)
}

// snapshot returns the stored key and whether one exists.
func (vault *Vault) snapshot(ctx context.Context) (storage.RawSealingState, bool, error) {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	if err := vault.loadLocked(ctx); err != nil {
		return storage.RawSealingState{}, false, err
	}
	return vault.state, vault.configured, nil
}

// Status implements RawVault.
func (vault *Vault) Status(ctx context.Context) (RawVaultStatus, error) {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	if err := vault.loadLocked(ctx); err != nil {
		return RawVaultStatus{}, err
	}
	now := vault.now()
	status := RawVaultStatus{
		Configured:  vault.configured,
		PasswordSet: vault.configured && vault.state.Password != nil,
		KeyVerified: vault.configured && vault.state.MACValid,
	}
	if vault.configured {
		sum := sha256.Sum256(vault.state.PublicKey)
		status.KeyFingerprint = hex.EncodeToString(sum[:])
	}
	if vault.sessionActiveLocked(now) {
		until := vault.sessionUntil.UTC()
		status.Unlocked, status.UnlockExpiresAt = true, &until
	}
	if remaining := vault.blockedUntil.Sub(now); remaining > 0 {
		status.RetryAfter = remaining
	}
	return status, nil
}

func (vault *Vault) sessionActiveLocked(now time.Time) bool {
	if vault.session == nil {
		return false
	}
	if now.Before(vault.sessionUntil) {
		return true
	}
	vault.clearSessionLocked()
	return false
}

func (vault *Vault) clearSessionLocked() {
	clear(vault.session)
	vault.session, vault.sessionKeyID, vault.sessionUntil = nil, 0, time.Time{}
	if vault.sessionTimer != nil {
		vault.sessionTimer.Stop()
		vault.sessionTimer = nil
	}
}

// expireSession zeroes the session key once its idle deadline passes, so an
// abandoned unlock does not leave the key in memory until the next read.
func (vault *Vault) expireSession() {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	if vault.session == nil {
		return
	}
	remaining := vault.sessionUntil.Sub(vault.now())
	if remaining <= 0 {
		vault.clearSessionLocked()
		return
	}
	vault.sessionTimer = time.AfterFunc(remaining, vault.expireSession)
}

// UnlockedOpener implements RawVault. Each call is a raw read and restarts
// the idle window.
func (vault *Vault) UnlockedOpener() (RawKeyOpener, bool) {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	now := vault.now()
	if !vault.sessionActiveLocked(now) {
		return nil, false
	}
	vault.sessionUntil = now.Add(rawUnlockIdle)
	return sessionOpener{vault: vault}, true
}

type sessionOpener struct{ vault *Vault }

func (opener sessionOpener) OpenBlobKey(blob storage.AuditBlob) ([]byte, error) {
	vault := opener.vault
	vault.mu.Lock()
	defer vault.mu.Unlock()
	if !vault.sessionActiveLocked(vault.now()) {
		return nil, fmt.Errorf("%w: the unlock session ended", rawseal.ErrBlobKey)
	}
	return openRawBlobKey(vault.session, vault.sessionKeyID, blob)
}

// proofOpener lends a private key to one callback, or to a holder until
// Close.
type proofOpener struct {
	mu      sync.Mutex
	private []byte
	keyID   int64
	cleared func([]byte)
}

func (opener *proofOpener) OpenBlobKey(blob storage.AuditBlob) ([]byte, error) {
	opener.mu.Lock()
	defer opener.mu.Unlock()
	if opener.private == nil {
		return nil, fmt.Errorf("%w: the proof was already used", rawseal.ErrBlobKey)
	}
	return openRawBlobKey(opener.private, opener.keyID, blob)
}

// Close zeroes the private key; later opens fail. It implements
// RawKeyHolder and may be called more than once.
func (opener *proofOpener) Close() {
	opener.mu.Lock()
	defer opener.mu.Unlock()
	private := opener.private
	if private == nil {
		return
	}
	clear(private)
	opener.private = nil
	if opener.cleared != nil {
		opener.cleared(private)
	}
}

func openRawBlobKey(private []byte, keyID int64, blob storage.AuditBlob) ([]byte, error) {
	if blob.Sealing != storage.AuditSealingRawV1 || blob.RawKeyID != keyID {
		return nil, fmt.Errorf("%w: part is not sealed to this key", rawseal.ErrBlobKey)
	}
	return rawseal.OpenBlobKey(private, rawseal.BlobKeyInfo(string(blob.RequestID), string(blob.Direction)), blob.WrappedKey)
}

// WithProof implements RawVault. The private key is zeroed before it
// returns; the unlock session is neither used nor extended.
func (vault *Vault) WithProof(ctx context.Context, proof RawProof, use func(RawKeyOpener) error) error {
	private, keyID, err := vault.openWithProof(ctx, proof)
	if err != nil {
		return err
	}
	opener := &proofOpener{private: private, keyID: keyID, cleared: vault.privateCleared}
	defer opener.Close()
	return use(opener)
}

// HoldKey implements RawVault. An empty proof copies the unlock session's
// key without extending its idle window, so holding a key is not a raw
// read by the operator.
func (vault *Vault) HoldKey(ctx context.Context, proof RawProof) (RawKeyHolder, error) {
	if proof.Empty() {
		vault.mu.Lock()
		defer vault.mu.Unlock()
		if !vault.sessionActiveLocked(vault.now()) {
			return nil, ErrRawProofRequired
		}
		return &proofOpener{
			private: append([]byte(nil), vault.session...), keyID: vault.sessionKeyID, cleared: vault.privateCleared,
		}, nil
	}
	private, keyID, err := vault.openWithProof(ctx, proof)
	if err != nil {
		return nil, err
	}
	return &proofOpener{private: private, keyID: keyID, cleared: vault.privateCleared}, nil
}

// Unlock implements RawVaultController.
func (vault *Vault) Unlock(ctx context.Context, proof RawProof) (RawVaultStatus, error) {
	private, keyID, err := vault.openWithProof(ctx, proof)
	if err != nil {
		return RawVaultStatus{}, err
	}
	vault.mu.Lock()
	vault.clearSessionLocked()
	vault.session, vault.sessionKeyID = private, keyID
	vault.sessionUntil = vault.now().Add(rawUnlockIdle)
	vault.sessionTimer = time.AfterFunc(rawUnlockIdle, vault.expireSession)
	vault.mu.Unlock()
	return vault.Status(ctx)
}

// Verify implements RawVaultController. The status is read before the slot
// is released, so it names the key the proof opened even when a password
// action waits behind it.
func (vault *Vault) Verify(ctx context.Context, proof RawProof) (RawVaultStatus, error) {
	if proof.Empty() {
		return RawVaultStatus{}, ErrRawProofRequired
	}
	if err := vault.acquire(ctx); err != nil {
		return RawVaultStatus{}, err
	}
	defer vault.release()
	private, _, err := vault.openHeldWithProof(ctx, proof)
	if err != nil {
		return RawVaultStatus{}, err
	}
	clear(private)
	if vault.privateCleared != nil {
		vault.privateCleared(private)
	}
	return vault.Status(ctx)
}

// Lock implements RawVaultController.
func (vault *Vault) Lock() {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	vault.clearSessionLocked()
}

// backoffLocked returns how long proofs are still refused.
func (vault *Vault) backoffLocked(now time.Time) time.Duration {
	if remaining := vault.blockedUntil.Sub(now); remaining > 0 {
		return remaining
	}
	return 0
}

func (vault *Vault) checkBackoff() error {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	if remaining := vault.backoffLocked(vault.now()); remaining > 0 {
		return &RawBackoffError{Remaining: remaining}
	}
	return nil
}

func (vault *Vault) noteWrongProof() {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	vault.failures++
	if vault.failures <= rawBackoffFreeFailures {
		return
	}
	// Doubling stops at the cap; the shift bound only keeps it from
	// overflowing.
	delay := vault.backoffCap
	if shift := vault.failures - rawBackoffFreeFailures - 1; shift < 30 {
		delay = min(time.Second<<shift, vault.backoffCap)
	}
	vault.blockedUntil = vault.now().Add(delay)
}

func (vault *Vault) noteRightProof() {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	vault.failures, vault.blockedUntil = 0, time.Time{}
}

// openWithProof checks a proof and returns the private key, which the
// caller zeroes.
func (vault *Vault) openWithProof(ctx context.Context, proof RawProof) ([]byte, int64, error) {
	if proof.Empty() {
		return nil, 0, ErrRawProofRequired
	}
	if err := vault.acquire(ctx); err != nil {
		return nil, 0, err
	}
	defer vault.release()
	return vault.openHeldWithProof(ctx, proof)
}

// openHeldWithProof is openWithProof for a caller that holds the slot.
func (vault *Vault) openHeldWithProof(ctx context.Context, proof RawProof) ([]byte, int64, error) {
	if err := vault.checkBackoff(); err != nil {
		return nil, 0, err
	}
	state, configured, err := vault.snapshot(ctx)
	if err != nil {
		return nil, 0, err
	}
	if !configured || state.Password == nil {
		return nil, 0, ErrRawNotConfigured
	}
	private, err := vault.openStateLocked(ctx, state, proof)
	if err != nil {
		return nil, 0, err
	}
	return private, state.KeyID, nil
}

// openStateLocked opens state's private key with proof. The caller holds
// the slot.
func (vault *Vault) openStateLocked(ctx context.Context, state storage.RawSealingState, proof RawProof) ([]byte, error) {
	if len(proof.Password) == 0 {
		return nil, ErrRawProofRequired
	}
	private, err := vault.openPassword(state, proof.Password)
	if err != nil {
		return nil, err
	}
	vault.repairAfterProof(ctx, state)
	return private, nil
}

func (vault *Vault) openPassword(state storage.RawSealingState, password []byte) ([]byte, error) {
	if state.Password == nil {
		return nil, ErrRawProofRequired
	}
	envelope, err := passwordEnvelope(*state.Password)
	if err != nil {
		return nil, err
	}
	private, err := rawseal.UnwrapPassword(envelope, password, state.KeyID, state.PublicKey)
	if errors.Is(err, rawseal.ErrPassword) {
		vault.noteWrongProof()
		return nil, ErrRawPasswordInvalid
	}
	if err != nil {
		return nil, err
	}
	vault.noteRightProof()
	return private, nil
}

// repairAfterProof re-authenticates a public key whose MAC no longer
// verifies, once a proof showed the pair is intact (§5.11.9.3).
func (vault *Vault) repairAfterProof(ctx context.Context, state storage.RawSealingState) {
	if vault.readOnly || state.MACValid {
		return
	}
	if err := vault.store.RefreshRawSealingMAC(ctx, state.KeyID); err != nil {
		vault.logf("raw sealing: re-authenticate the public key: %v", err)
		return
	}
	vault.logf("raw sealing: a proof confirmed the sealing key; raw captures are sealed to it again")
	vault.requestReseal()
	if err := vault.reload(ctx); err != nil {
		vault.logf("raw sealing: reload the sealing key: %v", err)
	}
}

func passwordEnvelope(stored storage.RawKeyEnvelope) (rawseal.PasswordEnvelope, error) {
	params, err := rawseal.ParseKDFJSON(stored.KDFJSON)
	if err != nil {
		return rawseal.PasswordEnvelope{}, err
	}
	return rawseal.PasswordEnvelope{KDF: params, Salt: stored.Salt, Nonce: stored.Nonce, Wrapped: stored.Wrapped}, nil
}

func storedPasswordEnvelope(envelope rawseal.PasswordEnvelope) (storage.RawKeyEnvelope, error) {
	kdfJSON, err := envelope.KDFJSON()
	if err != nil {
		return storage.RawKeyEnvelope{}, err
	}
	return storage.RawKeyEnvelope{
		Kind: rawseal.KindPassword, KDFJSON: kdfJSON,
		Salt: envelope.Salt, Nonce: envelope.Nonce, Wrapped: envelope.Wrapped,
	}, nil
}

// newRawKeyID picks a random positive id, so a key restored elsewhere never
// collides with a local one by counting.
func newRawKeyID() (int64, error) {
	var buffer [8]byte
	for {
		if _, err := rand.Read(buffer[:]); err != nil {
			return 0, err
		}
		if id := int64(binary.BigEndian.Uint64(buffer[:]) >> 1); id > 0 {
			return id, nil
		}
	}
}

// newKey generates a key pair wrapped under password.
func (vault *Vault) newKey(password []byte) (storage.NewRawSealingKey, error) {
	private, public, err := rawseal.GenerateKeyPair()
	if err != nil {
		return storage.NewRawSealingKey{}, err
	}
	defer clear(private)
	keyID, err := newRawKeyID()
	if err != nil {
		return storage.NewRawSealingKey{}, err
	}
	wrapped, err := rawseal.WrapPassword(private, password, vault.kdf, keyID, public)
	if err != nil {
		return storage.NewRawSealingKey{}, err
	}
	envelope, err := storedPasswordEnvelope(wrapped)
	if err != nil {
		return storage.NewRawSealingKey{}, err
	}
	return storage.NewRawSealingKey{KeyID: keyID, PublicKey: public, Envelopes: []storage.RawKeyEnvelope{envelope}}, nil
}

// ChangePassword implements RawVaultController.
func (vault *Vault) ChangePassword(
	ctx context.Context,
	action RawPasswordAction,
	password []byte,
	proof RawProof,
) (RawPasswordOutcome, error) {
	switch action {
	case RawPasswordSet, RawPasswordChange, RawPasswordReset:
	default:
		return RawPasswordOutcome{}, fmt.Errorf("%w: unknown raw password action", storage.ErrInvalidArgument)
	}
	if err := vault.acquire(ctx); err != nil {
		return RawPasswordOutcome{}, err
	}
	defer vault.release()
	state, configured, err := vault.snapshot(ctx)
	if err != nil {
		return RawPasswordOutcome{}, err
	}
	var outcome RawPasswordOutcome
	switch action {
	case RawPasswordSet:
		if configured {
			return RawPasswordOutcome{}, ErrRawPasswordAlreadySet
		}
		if err := requireNewPassword(password); err != nil {
			return RawPasswordOutcome{}, err
		}
		err = vault.createWithPassword(ctx, password)
	case RawPasswordChange:
		if !configured || state.Password == nil {
			return RawPasswordOutcome{}, ErrRawPasswordNotSet
		}
		if err := requireNewPassword(password); err != nil {
			return RawPasswordOutcome{}, err
		}
		err = vault.rewrap(ctx, state, password, proof)
	case RawPasswordReset:
		if !configured {
			return RawPasswordOutcome{}, ErrRawNotConfigured
		}
		// The new key needs a password too: without one nothing raw would
		// be kept after the reset. A reset is how a forgotten password is
		// recovered from, so it takes no proof.
		if err := requireNewPassword(password); err != nil {
			return RawPasswordOutcome{}, err
		}
		outcome.Reset, err = vault.replace(ctx, password)
	}
	if err != nil {
		return RawPasswordOutcome{}, err
	}
	if err := vault.reload(ctx); err != nil {
		return RawPasswordOutcome{}, err
	}
	outcome.Status, err = vault.Status(ctx)
	return outcome, err
}

func requireNewPassword(password []byte) error {
	if len(password) == 0 {
		return ErrRawPasswordRequired
	}
	return rawseal.ValidatePassword(password)
}

func (vault *Vault) createWithPassword(ctx context.Context, password []byte) error {
	key, err := vault.newKey(password)
	if err != nil {
		return err
	}
	if err := vault.store.CreateRawSealingKey(ctx, key); err != nil {
		return err
	}
	vault.logf("raw sealing: created the raw sealing key")
	vault.requestReseal()
	return nil
}

// rewrap opens the existing key with proof and stores the new password
// envelope under a fresh salt.
func (vault *Vault) rewrap(ctx context.Context, state storage.RawSealingState, password []byte, proof RawProof) error {
	private, err := vault.proveLocked(ctx, state, proof)
	if err != nil {
		return err
	}
	defer clear(private)
	wrapped, err := rawseal.WrapPassword(private, password, vault.kdf, state.KeyID, state.PublicKey)
	if err != nil {
		return err
	}
	envelope, err := storedPasswordEnvelope(wrapped)
	if err != nil {
		return err
	}
	if err := vault.store.PutRawKeyEnvelope(ctx, state.KeyID, envelope); err != nil {
		return err
	}
	vault.logf("raw sealing: the raw password was changed")
	return nil
}

// proveLocked opens state's key with proof for a caller that holds the
// slot; the caller zeroes the key.
func (vault *Vault) proveLocked(ctx context.Context, state storage.RawSealingState, proof RawProof) ([]byte, error) {
	if proof.Empty() {
		return nil, ErrRawProofRequired
	}
	if err := vault.checkBackoff(); err != nil {
		return nil, err
	}
	return vault.openStateLocked(ctx, state, proof)
}

// replace discards the key pair and every part sealed to it. It is how a
// forgotten password is recovered from, at the cost of the raw captures
// still in retention.
func (vault *Vault) replace(ctx context.Context, password []byte) (*storage.RawResetResult, error) {
	key, err := vault.newKey(password)
	if err != nil {
		return nil, err
	}
	result, err := vault.store.ReplaceRawSealingKey(ctx, key)
	if err != nil {
		return nil, err
	}
	vault.mu.Lock()
	vault.clearSessionLocked()
	vault.mu.Unlock()
	vault.logf("raw sealing: replaced the raw sealing key; discarded %d raw part(s) of %d request(s)",
		result.DeletedParts, result.AffectedRecords)
	vault.requestReseal()
	return &result, nil
}

// ClearPassword discards the key pair, every part sealed to it and with
// them the raw password, so none is set until set runs again. It needs no
// proof: the server edition runs it at startup for the reset switch in its
// deploy configuration, which only whoever controls the deployment can set.
func (vault *Vault) ClearPassword(ctx context.Context) (storage.RawResetResult, error) {
	if err := vault.acquire(ctx); err != nil {
		return storage.RawResetResult{}, err
	}
	defer vault.release()
	result, err := vault.store.ClearRawSealingKey(ctx)
	if err != nil {
		return storage.RawResetResult{}, err
	}
	vault.mu.Lock()
	vault.clearSessionLocked()
	vault.mu.Unlock()
	if err := vault.reload(ctx); err != nil {
		return storage.RawResetResult{}, err
	}
	vault.logf("raw sealing: cleared the raw password; discarded %d raw part(s) of %d request(s)",
		result.DeletedParts, result.AffectedRecords)
	vault.requestReseal()
	return result, nil
}
