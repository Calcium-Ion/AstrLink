package controlapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/rawseal"
)

const (
	// RawSealingPath reports raw sealing state: operators see the key and the
	// unlock session, observers only whether agents may ask for raw parts.
	RawSealingPath = "/control/v1/audit/raw-sealing"
	// RawPasswordPath sets, changes or resets the raw password.
	RawPasswordPath = "/control/v1/audit/raw-password"
	// RawUnlockPath and RawLockPath start and end the operator's unlock
	// session. A lock also revokes every agent grant. An approval needs its
	// own proof either way.
	RawUnlockPath = "/control/v1/audit/raw-unlock"
	RawLockPath   = "/control/v1/audit/raw-lock"
	// RawVerifyPath checks a proof without starting or touching the unlock
	// session, for desktop actions that only need to know it holds.
	RawVerifyPath = "/control/v1/audit/raw-verify"
	// RawBiometricPath adds (POST) or takes out (DELETE) the envelope a key
	// the desktop keeps behind Touch ID opens. Operators only.
	RawBiometricPath = "/control/v1/audit/raw-biometric"
)

// RawSealingSummary is the observer view of raw sealing.
type RawSealingSummary struct {
	RawAvailable bool `json:"raw_available"`
}

// RawSealingStatus is the operator view of raw sealing. It never carries key
// material.
type RawSealingStatus struct {
	RawAvailable bool `json:"raw_available"`
	Configured   bool `json:"configured"`
	PasswordSet  bool `json:"password_set"`
	// PasswordRequired is true until a raw password is set: no raw content
	// is kept or read before then, on any platform.
	PasswordRequired bool `json:"password_required"`
	// Envelopes lists the stored private key envelopes.
	Envelopes []string `json:"envelopes"`
	// KeyVerified is false while raw captures are not kept until a proof
	// confirms the stored public key.
	KeyVerified       bool       `json:"key_verified"`
	Unlocked          bool       `json:"unlocked"`
	UnlockExpiresAt   *time.Time `json:"unlock_expires_at"`
	UnlockIdleSeconds int        `json:"unlock_idle_seconds"`
	RetryAfterSeconds int        `json:"retry_after_seconds"`
	PasswordMinRunes  int        `json:"password_min_length"`
	PasswordMaxRunes  int        `json:"password_max_length"`
	// KeyFingerprint identifies the public key so the desktop can notice a
	// key replaced while it was not looking. It is not key material.
	KeyFingerprint string `json:"key_fingerprint"`
}

// RawPasswordResponse answers a raw password action.
type RawPasswordResponse struct {
	RawSealingStatus
	// Reset reports what a reset discarded; absent for set and change.
	Reset *RawResetSummary `json:"reset,omitempty"`
}

// RawResetSummary counts the raw parts a reset discarded.
type RawResetSummary struct {
	DeletedParts    int `json:"deleted_parts"`
	AffectedRecords int `json:"affected_records"`
}

func (handler *Handler) registerRawSealingRoutes() {
	handler.mux.HandleFunc(RawSealingPath, handler.authenticated(handler.getRawSealing, RoleObserver))
	handler.mux.HandleFunc(RawPasswordPath, handler.authenticated(handler.postRawPassword, RoleOperator))
	handler.mux.HandleFunc(RawUnlockPath, handler.authenticated(handler.postRawUnlock, RoleOperator))
	handler.mux.HandleFunc(RawLockPath, handler.authenticated(handler.postRawLock, RoleOperator))
	handler.mux.HandleFunc(RawVerifyPath, handler.authenticated(handler.postRawVerify, RoleOperator))
	handler.mux.HandleFunc(RawBiometricPath, handler.authenticated(handler.rawBiometric, RoleOperator))
}

func (handler *Handler) rawVaultController() RawVaultController {
	controller, _ := handler.rawVault.(RawVaultController)
	return controller
}

func retryAfterSeconds(remaining time.Duration) int {
	if remaining <= 0 {
		return 0
	}
	return max(int((remaining+time.Second-1)/time.Second), 1)
}

// rawSealingStatus builds the operator view from a vault status.
func (handler *Handler) rawSealingStatus(request *http.Request, status RawVaultStatus) (RawSealingStatus, error) {
	view := RawSealingStatus{
		Configured:        status.Configured,
		PasswordSet:       status.PasswordSet,
		PasswordRequired:  !status.PasswordSet,
		Envelopes:         []string{},
		KeyVerified:       status.KeyVerified,
		Unlocked:          status.Unlocked,
		UnlockExpiresAt:   status.UnlockExpiresAt,
		UnlockIdleSeconds: int(status.UnlockIdle / time.Second),
		RetryAfterSeconds: retryAfterSeconds(status.RetryAfter),
		PasswordMinRunes:  rawseal.MinPasswordRunes,
		PasswordMaxRunes:  rawseal.MaxPasswordRunes,
		KeyFingerprint:    status.KeyFingerprint,
	}
	if status.PasswordSet {
		view.Envelopes = append(view.Envelopes, rawseal.KindPassword)
	}
	if status.BiometricSet {
		view.Envelopes = append(view.Envelopes, rawseal.KindBiometric)
	}
	if status.PasswordSet {
		enabled, err := handler.agentRawAccessEnabled(request.Context())
		if err != nil {
			return RawSealingStatus{}, err
		}
		view.RawAvailable = enabled
	}
	return view, nil
}

func (handler *Handler) writeRawSealingStatus(writer http.ResponseWriter, request *http.Request, status RawVaultStatus) {
	view, err := handler.rawSealingStatus(request, status)
	if err != nil {
		handler.writeAuditSettingsStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (handler *Handler) getRawSealing(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	status, err := handler.rawVaultStatus(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "raw_vault_unavailable", "raw sealing state is unavailable")
		return
	}
	view, err := handler.rawSealingStatus(request, status)
	if err != nil {
		handler.writeAuditSettingsStoreError(writer, err)
		return
	}
	if requestRole(request) < RoleOperator {
		writeJSON(writer, http.StatusOK, RawSealingSummary{RawAvailable: view.RawAvailable})
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

// requireRawController answers 409 when this Core has no raw vault, which
// is the case outside persistent mode.
func (handler *Handler) requireRawController(writer http.ResponseWriter, request *http.Request) (RawVaultController, bool) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return nil, false
	}
	controller := handler.rawVaultController()
	if controller == nil {
		writeError(writer, http.StatusConflict, "raw_sealing_unavailable", "raw content sealing is not available in this Core")
		return nil, false
	}
	return controller, true
}

func (handler *Handler) postRawPassword(writer http.ResponseWriter, request *http.Request) {
	controller, ok := handler.requireRawController(writer, request)
	if !ok {
		return
	}
	members, release, ok := decodeSecretJSON(writer, request, "action", "password", "proof")
	if !ok {
		return
	}
	defer release()
	var action RawPasswordAction
	if raw, present := members["action"]; !present || strictUnmarshal(raw, &action) != nil {
		action = ""
	}
	switch action {
	case RawPasswordSet, RawPasswordChange, RawPasswordReset:
	default:
		writeValidationFailed(writer, "action is invalid", []errorDetail{{
			Field: "action", Reason: "must be set, change, or reset",
		}})
		return
	}
	if handler.consoleSessions != nil && action != RawPasswordChange {
		writeError(writer, http.StatusForbidden, "raw_password_action_unavailable",
			"the server edition sets the raw password in first-run setup and resets it from its deploy configuration; only change is available here")
		return
	}
	var password []byte
	if raw, present := members["password"]; present && !isJSONNull(raw) {
		decoded, err := decodeJSONStringBytes(raw)
		if err != nil {
			writeValidationFailed(writer, "password is invalid", []errorDetail{{
				Field: "password", Reason: "must be a string",
			}})
			return
		}
		password = decoded
	}
	defer clear(password)
	proof, err := parseRawProof(members["proof"])
	if err != nil {
		writeValidationFailed(writer, "proof is invalid", []errorDetail{{
			Field: "proof", Reason: "must be {password}",
		}})
		return
	}
	defer proof.clear()
	outcome, err := controller.ChangePassword(request.Context(), action, password, proof.RawProof)
	if err == nil || outcome.Committed {
		// Revocation follows the durable write, including when later cleanup or
		// cache refresh failed. A rejected/uncommitted change preserves sessions.
		handler.rawGrants.revokeAll()
		if handler.consoleSessions != nil {
			handler.consoleSessions.EndOtherSessions(request)
		}
		handler.observers.noteRawEvent(rawPasswordEvents[action], RawAccessGrant{ClientName: classifyObserver(request)})
	}
	if err != nil {
		if errors.Is(err, ErrRawPasswordInvalid) {
			handler.observers.noteRawEvent(RawAccessEventPasswordInvalid, RawAccessGrant{})
		}
		handler.writeRawPasswordError(writer, err)
		return
	}
	view, err := handler.rawSealingStatus(request, outcome.Status)
	if err != nil {
		handler.writeAuditSettingsStoreError(writer, err)
		return
	}
	response := RawPasswordResponse{RawSealingStatus: view}
	if outcome.Reset != nil {
		response.Reset = &RawResetSummary{
			DeletedParts: outcome.Reset.DeletedParts, AffectedRecords: outcome.Reset.AffectedRecords,
		}
	}
	writeJSON(writer, http.StatusOK, response)
}

// rawPasswordEvents names the observer event each raw password action leaves.
var rawPasswordEvents = map[RawPasswordAction]RawAccessEventKind{
	RawPasswordSet:    RawAccessEventPasswordSet,
	RawPasswordChange: RawAccessEventPasswordChanged,
	RawPasswordReset:  RawAccessEventKeyReset,
}

func (handler *Handler) writeRawPasswordError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRawPasswordAlreadySet):
		writeError(writer, http.StatusConflict, "raw_password_already_set", "a raw password is already set; change it instead")
	case errors.Is(err, ErrRawPasswordNotSet):
		writeError(writer, http.StatusConflict, "raw_password_not_set", "no raw password is set")
	case errors.Is(err, ErrRawPasswordRequired), errors.Is(err, ErrRawPasswordPolicy):
		writeValidationFailed(writer, "password is invalid", []errorDetail{{
			Field:  "password",
			Reason: fmt.Sprintf("must contain %d to %d characters", rawseal.MinPasswordRunes, rawseal.MaxPasswordRunes),
		}})
	case errors.Is(err, storage.ErrConflict):
		writeError(writer, http.StatusConflict, "raw_sealing_changed", "raw sealing changed concurrently; reload and retry")
	default:
		handler.writeRawProofError(writer, err)
	}
}

func (handler *Handler) postRawUnlock(writer http.ResponseWriter, request *http.Request) {
	handler.postRawProof(writer, request, "unlocking", RawVaultController.Unlock)
}

// postRawVerify takes the password only: it confirms a key replaced outside
// the desktop, and only that key's own password shows the operator set it.
func (handler *Handler) postRawVerify(writer http.ResponseWriter, request *http.Request) {
	handler.postRawProof(writer, request, "verifying", func(controller RawVaultController, ctx context.Context, proof RawProof) (RawVaultStatus, error) {
		if len(proof.Password) == 0 {
			return RawVaultStatus{}, ErrRawProofRequired
		}
		return controller.Verify(ctx, proof)
	})
}

// postRawProof hands the request's proof to use and answers with the raw
// sealing state; verb names the action in a missing-proof error.
func (handler *Handler) postRawProof(
	writer http.ResponseWriter,
	request *http.Request,
	verb string,
	use func(RawVaultController, context.Context, RawProof) (RawVaultStatus, error),
) {
	controller, ok := handler.requireRawController(writer, request)
	if !ok {
		return
	}
	proofRequired := verb + " requires the raw password or Touch ID"
	if request.ContentLength == 0 {
		writeError(writer, http.StatusUnprocessableEntity, "raw_proof_required", proofRequired)
		return
	}
	members, release, ok := decodeSecretJSON(writer, request, "proof")
	if !ok {
		return
	}
	defer release()
	proof, err := parseRawProof(members["proof"])
	if err != nil {
		writeValidationFailed(writer, "proof is invalid", []errorDetail{{
			Field: "proof", Reason: "must be {password} or {biometric}",
		}})
		return
	}
	defer proof.clear()
	if proof.Empty() {
		writeError(writer, http.StatusUnprocessableEntity, "raw_proof_required", proofRequired)
		return
	}
	status, err := use(controller, request.Context(), proof.RawProof)
	if err != nil {
		if errors.Is(err, ErrRawPasswordInvalid) {
			handler.observers.noteRawEvent(RawAccessEventPasswordInvalid, RawAccessGrant{})
		}
		handler.writeRawProofError(writer, err)
		return
	}
	handler.writeRawSealingStatus(writer, request, status)
}

// postRawLock ends the unlock session and every agent grant. The desktop
// sends it when the screen locks, the computer sleeps, or the operator
// locks.
func (handler *Handler) postRawLock(writer http.ResponseWriter, request *http.Request) {
	controller, ok := handler.requireRawController(writer, request)
	if !ok {
		return
	}
	if request.ContentLength != 0 {
		var body struct{}
		if !requireMediaType(writer, request, "application/json") || !decodeControlJSON(writer, request, &body) {
			return
		}
	}
	controller.Lock()
	handler.rawGrants.revokeAll()
	status, err := handler.rawVaultStatus(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "raw_vault_unavailable", "raw sealing state is unavailable")
		return
	}
	handler.writeRawSealingStatus(writer, request, status)
}

// rawBiometric adds or takes out the Touch ID envelope. Adding needs the raw
// password and the key the desktop generated and keeps in the keychain;
// taking it out needs nothing, since it only gives access up. The server
// edition has no keychain to keep a key in.
func (handler *Handler) rawBiometric(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost && request.Method != http.MethodDelete {
		writer.Header().Set("Allow", "POST, DELETE")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST and DELETE are allowed")
		return
	}
	controller := handler.rawVaultController()
	if controller == nil {
		writeError(writer, http.StatusConflict, "raw_sealing_unavailable", "raw content sealing is not available in this Core")
		return
	}
	if handler.consoleSessions != nil {
		writeError(writer, http.StatusForbidden, "raw_biometric_unavailable", "the server edition unlocks raw content with the raw password only")
		return
	}
	var (
		status RawVaultStatus
		err    error
	)
	if request.Method == http.MethodDelete {
		status, err = controller.RemoveBiometric(request.Context())
	} else {
		members, release, ok := decodeSecretJSON(writer, request, "proof", "key")
		if !ok {
			return
		}
		defer release()
		proof, parseErr := parseRawProof(members["proof"])
		if parseErr != nil || len(proof.Password) == 0 {
			proof.clear()
			writeValidationFailed(writer, "proof is invalid", []errorDetail{{
				Field: "proof", Reason: "must be {password}",
			}})
			return
		}
		defer proof.clear()
		key, keyErr := decodeBase64Secret(members["key"])
		if keyErr != nil || len(key) != rawseal.BiometricKeyBytes {
			clear(key)
			writeValidationFailed(writer, "key is invalid", []errorDetail{{
				Field: "key", Reason: fmt.Sprintf("must be %d bytes of standard base64", rawseal.BiometricKeyBytes),
			}})
			return
		}
		defer clear(key)
		status, err = controller.EnrollBiometric(request.Context(), proof.RawProof, key)
		if errors.Is(err, ErrRawPasswordInvalid) {
			handler.observers.noteRawEvent(RawAccessEventPasswordInvalid, RawAccessGrant{})
		}
	}
	if err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeError(writer, http.StatusConflict, "raw_sealing_changed", "raw sealing changed concurrently; reload and retry")
			return
		}
		handler.writeRawProofError(writer, err)
		return
	}
	view, err := handler.rawSealingStatus(request, status)
	if err != nil {
		handler.writeAuditSettingsStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}
