package raw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// Core is what the raw commands need of the running Core (core.Manager).
type Core interface {
	control.Caller
	// DataDirectory is the data directory of the last Core start, "" before
	// the first.
	DataDirectory() string
	// NoteRawKeyReplaced hands the tray the desktop's verdict on the raw
	// key and reports whether it changed.
	NoteRawKeyReplaced(replaced bool) bool
	PollObservers(ctx context.Context) error
}

// Pins keeps the raw key the desktop accepted for each data directory
// (keystore.RawKeyPins).
type Pins interface {
	Lock()
	Unlock()
	Apply(account string, status json.RawMessage, action keystore.PinAction) bool
}

// Raw is the operator's raw content: agent requests and grants, the unlock
// and the raw password.
type Raw struct {
	Core Core
	Pins Pins
	// Changed tells every window that the raw unlock or key may have
	// changed, so one showing raw parts, such as a pinned inspector, reads
	// the state again.
	Changed func()
	// Biometrics is Touch ID, the system's when nil.
	Biometrics Biometrics
	// Locale is the language of the Touch ID prompt, the system's when nil.
	Locale func() prefs.Locale
}

func (r Raw) biometrics() Biometrics {
	if r.Biometrics != nil {
		return r.Biometrics
	}
	return systemBiometrics{}
}

func (r Raw) locale() prefs.Locale {
	if r.Locale != nil {
		return r.Locale()
	}
	return prefs.SystemLocale()
}

func (r Raw) changed() {
	if r.Changed != nil {
		r.Changed()
	}
}

// The prompts name what Touch ID is asked for.
const (
	unlockReason  = "host.rawBiometric.unlockReason"
	approveReason = "host.rawBiometric.approveReason"
)

// proof turns what the dialog sent into a proof for Core, or the outcome
// the dialog shows instead. A Touch ID request shows the prompt here, in
// the app, with the reason of reasonKey; the key it releases goes to Core
// and never to the webview.
func (r Raw) proof(arg ProofArg, reasonKey string) (*Proof, *ProofOutcome) {
	if arg.Kind == "password" && arg.Password != nil {
		return &Proof{Password: *arg.Password}, nil
	}
	directory := r.Core.DataDirectory()
	if directory == "" {
		return nil, &ProofOutcome{Outcome: OutcomeBiometricUnavailable}
	}
	key, outcome := r.biometrics().Release(keystore.KeychainAccount(directory), prefs.T(r.locale(), reasonKey))
	if key == nil {
		return nil, &ProofOutcome{Outcome: outcome}
	}
	return &Proof{Biometric: key}, nil
}

// ListRawAccess lists agent requests awaiting the operator's decision, the
// timed grants still running, and whether an approval needs the raw
// password.
func (r Raw) ListRawAccess(ctx context.Context) (json.RawMessage, error) {
	response, err := control.Send(ctx, r.Core, control.Request{Method: http.MethodGet, Path: control.RawAccessPath})
	if err != nil {
		return nil, err
	}
	return ParseList(response.Body)
}

// DecideRawAccess decides one agent request. An approval carries the proof
// the dialog collected, sent once; refusals the dialog recovers from come
// back as outcomes.
func (r Raw) DecideRawAccess(ctx context.Context, grantID, decision string, arg *ProofArg) (ProofOutcome, error) {
	var p *Proof
	if arg != nil && decision != "deny" {
		var refusal *ProofOutcome
		if p, refusal = r.proof(*arg, approveReason); refusal != nil {
			return *refusal, nil
		}
	}
	outcome, err := r.decide(ctx, grantID, decision, p)
	if p != nil {
		clear(p.Biometric)
	}
	// Read what still waits now, so the approval window stops floating once
	// the last request is decided rather than at the next observer poll.
	_ = r.Core.PollObservers(ctx)
	return outcome, err
}

func (r Raw) decide(ctx context.Context, grantID, decision string, p *Proof) (ProofOutcome, error) {
	body, err := DecisionBody(grantID, decision, p)
	if err != nil {
		return ProofOutcome{}, err
	}
	path := DecisionPath(grantID)
	response, err := r.Core.Call(ctx, control.Request{Method: http.MethodPost, Path: path, Body: json.RawMessage(body)})
	clear(body)
	if err != nil {
		return ProofOutcome{}, err
	}
	outcome := DecisionOutcome(response.Status, response.Body)
	if outcome == nil {
		return ProofOutcome{}, control.StatusError(http.MethodPost, path, response.Status, response.Body)
	}
	return *outcome, nil
}

// RevokeRawGrant ends a running lasting agent grant before it expires.
func (r Raw) RevokeRawGrant(ctx context.Context, grantID string) (json.RawMessage, error) {
	path, err := GrantPath(grantID)
	if err != nil {
		return nil, err
	}
	response, err := control.Send(ctx, r.Core, control.Request{Method: http.MethodDelete, Path: path})
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(response.Body, &value); err != nil {
		return nil, fmt.Errorf("raw grant revoke returned invalid JSON: %v", err)
	}
	return response.Body, nil
}

// RawSealingStatus is the raw sealing state plus whether the raw key was
// replaced outside the desktop.
func (r Raw) RawSealingStatus(ctx context.Context) (json.RawMessage, error) {
	r.Pins.Lock()
	defer r.Pins.Unlock()
	status, err := r.status(ctx, http.MethodGet, control.RawSealingPath)
	if err != nil {
		return nil, err
	}
	r.noteRawKey(status, keystore.PinCheck)
	return encodeStatus(status), nil
}

func (r Raw) status(ctx context.Context, method, path string) (map[string]json.RawMessage, error) {
	response, err := control.Send(ctx, r.Core, control.Request{Method: method, Path: path})
	if err != nil {
		return nil, err
	}
	return ParseSealingStatus(response.Body)
}

// noteRawKey adds key_replaced to a sealing status after applying action to
// the pin of the running data directory, hands the verdict to the tray,
// and adds biometric_available. Callers hold the pins.
func (r Raw) noteRawKey(status map[string]json.RawMessage, action keystore.PinAction) {
	replaced := false
	if directory := r.Core.DataDirectory(); directory != "" {
		replaced = r.Pins.Apply(keystore.KeychainAccount(directory), encodeStatus(status), action)
	}
	r.Core.NoteRawKeyReplaced(replaced)
	status["key_replaced"] = jsonBool(replaced)
	r.noteBiometric(status)
}

// noteBiometric adds biometric_available: whether Touch ID can stand in
// for the raw password on this Mac and build. Whether it is set up for raw
// content is Core's envelopes.
func (r Raw) noteBiometric(status map[string]json.RawMessage) {
	status["biometric_available"] = jsonBool(r.biometrics().Available())
}

func jsonBool(value bool) json.RawMessage {
	if value {
		return json.RawMessage("true")
	}
	return json.RawMessage("false")
}

func encodeStatus(status map[string]json.RawMessage) json.RawMessage {
	encoded, err := json.Marshal(status)
	if err != nil {
		return json.RawMessage("{}")
	}
	return encoded
}

// sealingProof sends a proof-carrying unlock, verify or password call and
// clears the body once it is sent.
func (r Raw) sealingProof(ctx context.Context, path string, body []byte) (ProofOutcome, error) {
	response, err := r.Core.Call(ctx, control.Request{Method: http.MethodPost, Path: path, Body: json.RawMessage(body)})
	clear(body)
	if err != nil {
		return ProofOutcome{}, err
	}
	outcome := SealingOutcome(response.Status, response.Body)
	if outcome == nil {
		return ProofOutcome{}, control.StatusError(http.MethodPost, path, response.Status, response.Body)
	}
	return *outcome, nil
}

// noteRawKeyOutcome applies action to the status of a successful proof
// outcome; refusals pass through unchanged. Callers hold the pins.
func (r Raw) noteRawKeyOutcome(outcome ProofOutcome, action keystore.PinAction) ProofOutcome {
	if outcome.Outcome != OutcomeSealing {
		return outcome
	}
	status, err := ParseSealingStatus(outcome.Status)
	if err != nil {
		return outcome
	}
	r.noteRawKey(status, action)
	outcome.Status = encodeStatus(status)
	return outcome
}

// UnlockRaw unlocks raw parts for the operator's own reading.
func (r Raw) UnlockRaw(ctx context.Context, arg ProofArg) (ProofOutcome, error) {
	p, refusal := r.proof(arg, unlockReason)
	if refusal != nil {
		return *refusal, nil
	}
	body, err := UnlockBody(*p)
	clear(p.Biometric)
	if err != nil {
		return ProofOutcome{}, err
	}
	r.Pins.Lock()
	defer r.Pins.Unlock()
	outcome, err := r.sealingProof(ctx, control.RawUnlockPath, body)
	if err != nil {
		return ProofOutcome{}, err
	}
	outcome = r.noteRawKeyOutcome(outcome, keystore.PinCheck)
	r.changed()
	return outcome, nil
}

// LockRaw is the operator's own lock, which also revokes every agent
// grant, as the session lock does.
func (r Raw) LockRaw(ctx context.Context) (json.RawMessage, error) {
	r.Pins.Lock()
	defer r.Pins.Unlock()
	status, err := r.status(ctx, http.MethodPost, control.RawLockPath)
	if err != nil {
		return nil, err
	}
	r.noteRawKey(status, keystore.PinCheck)
	r.changed()
	return encodeStatus(status), nil
}

// AcknowledgeRawKey accepts a raw key replaced outside the desktop, such as
// by the operator's own `astrlink-core raw-password`. Only the key's own
// password accepts it: that shows the operator chose the key. Core's verify
// route checks the proof without starting an unlock session, and the key
// its answer names is pinned.
func (r Raw) AcknowledgeRawKey(ctx context.Context, arg ProofArg) (ProofOutcome, error) {
	// Only the replaced key's own password confirms it; Core refuses Touch
	// ID.
	p, refusal := r.proof(arg, unlockReason)
	if refusal != nil {
		return *refusal, nil
	}
	body, err := UnlockBody(*p)
	clear(p.Biometric)
	if err != nil {
		return ProofOutcome{}, err
	}
	r.Pins.Lock()
	defer r.Pins.Unlock()
	outcome, err := r.sealingProof(ctx, control.RawVerifyPath, body)
	if err != nil {
		return ProofOutcome{}, err
	}
	outcome = r.noteRawKeyOutcome(outcome, keystore.PinRecord)
	r.changed()
	return outcome, nil
}

// SetRawPassword sets, changes, or resets the raw password. password is the
// new one; proof opens the existing key where Core needs it. Core takes
// any Touch ID envelope out with the old password; where Touch ID is
// available the new password sets it up again at once.
func (r Raw) SetRawPassword(ctx context.Context, action string, password *string, arg *ProofArg) (ProofOutcome, error) {
	var p *Proof
	if arg != nil {
		var refusal *ProofOutcome
		if p, refusal = r.proof(*arg, unlockReason); refusal != nil {
			return *refusal, nil
		}
	}
	body, err := PasswordBody(action, password, p)
	if p != nil {
		clear(p.Biometric)
	}
	if err != nil {
		return ProofOutcome{}, err
	}
	r.Pins.Lock()
	defer r.Pins.Unlock()
	outcome, err := r.sealingProof(ctx, control.RawPasswordPath, body)
	if err != nil {
		return ProofOutcome{}, err
	}
	outcome = r.noteRawKeyOutcome(outcome, keystore.PinActionAfterPassword(action))
	if password != nil && outcome.Outcome == OutcomeSealing {
		r.renewBiometric(ctx, *password, &outcome)
	}
	r.changed()
	return outcome, nil
}

// renewBiometric replaces the Touch ID key after the raw password changed:
// the old key's envelope is gone, so the keychain item goes too, and where
// Touch ID is available a new key is enrolled with the new password. The
// outcome then reports the new status. A failure leaves the password
// working alone.
func (r Raw) renewBiometric(ctx context.Context, password string, outcome *ProofOutcome) {
	directory := r.Core.DataDirectory()
	if directory == "" {
		return
	}
	account := keystore.KeychainAccount(directory)
	biometrics := r.biometrics()
	biometrics.Forget(account)
	if !biometrics.Available() {
		return
	}
	enrolled, err := r.enroll(ctx, account, password)
	if err != nil {
		log.Printf("astrlink: unable to set up Touch ID for raw content: %v", err)
		return
	}
	fresh, freshErr := ParseSealingStatus(enrolled.Status)
	status, statusErr := ParseSealingStatus(outcome.Status)
	if enrolled.Outcome != OutcomeSealing || freshErr != nil || statusErr != nil {
		log.Print("astrlink: Core did not set up Touch ID for raw content")
		return
	}
	// The host's verdict on the key stays; Core's view is the newer one.
	if replaced, ok := status["key_replaced"]; ok {
		fresh["key_replaced"] = replaced
	}
	r.noteBiometric(fresh)
	outcome.Status = encodeStatus(fresh)
}

// enroll generates a key, keeps it in the keychain and has Core wrap the
// raw key under it, opened with password. It returns Core's outcome; the
// keychain item is removed again unless Core set the envelope up.
func (r Raw) enroll(ctx context.Context, account, password string) (ProofOutcome, error) {
	key, err := NewBiometricKey()
	if err != nil {
		return ProofOutcome{}, err
	}
	defer clear(key)
	biometrics := r.biometrics()
	if err := biometrics.Store(account, key); err != nil {
		return ProofOutcome{}, err
	}
	body, err := BiometricBody(password, key)
	if err != nil {
		biometrics.Forget(account)
		return ProofOutcome{}, err
	}
	outcome, err := r.sealingProof(ctx, control.RawBiometricPath, body)
	if err != nil || outcome.Outcome != OutcomeSealing {
		biometrics.Forget(account)
	}
	return outcome, err
}

// EnrollRawBiometric sets up Touch ID for raw content with the raw
// password, for a password set before Touch ID was offered or after it was
// turned off. Refusals come back as outcomes, so the dialog can ask again.
func (r Raw) EnrollRawBiometric(ctx context.Context, password string) (ProofOutcome, error) {
	directory := r.Core.DataDirectory()
	if directory == "" {
		return ProofOutcome{}, errors.New("AstrLink is not running")
	}
	if !r.biometrics().Available() {
		return ProofOutcome{Outcome: OutcomeBiometricUnavailable}, nil
	}
	outcome, err := r.enroll(ctx, keystore.KeychainAccount(directory), password)
	if err != nil {
		return ProofOutcome{}, err
	}
	if outcome.Outcome == OutcomeSealing {
		if status, err := ParseSealingStatus(outcome.Status); err == nil {
			r.noteBiometric(status)
			outcome.Status = encodeStatus(status)
		}
	}
	r.changed()
	return outcome, nil
}

// RemoveRawBiometric turns Touch ID off for raw content: Core drops the
// envelope and the keychain item goes with it. The raw password keeps
// working.
func (r Raw) RemoveRawBiometric(ctx context.Context) (json.RawMessage, error) {
	status, err := r.status(ctx, http.MethodDelete, control.RawBiometricPath)
	if directory := r.Core.DataDirectory(); directory != "" {
		r.biometrics().Forget(keystore.KeychainAccount(directory))
	}
	if err != nil {
		return nil, err
	}
	r.noteBiometric(status)
	r.changed()
	return encodeStatus(status), nil
}

// CheckKey checks the raw key pin without any window, when Core becomes
// ready or records a raw key change, so the tray can warn about a key
// replaced while the app was closed. Windows read the state again when the
// verdict moves. Errors are transient (Core restarting): the next ready
// checks again.
func CheckKey(ctx context.Context, r Raw) {
	r.Pins.Lock()
	defer r.Pins.Unlock()
	status, err := r.status(ctx, http.MethodGet, control.RawSealingPath)
	if err != nil {
		return
	}
	replaced := false
	if directory := r.Core.DataDirectory(); directory != "" {
		replaced = r.Pins.Apply(keystore.KeychainAccount(directory), encodeStatus(status), keystore.PinCheck)
	}
	if r.Core.NoteRawKeyReplaced(replaced) {
		r.changed()
	}
}
