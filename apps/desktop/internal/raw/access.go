// Package raw is the desktop side of raw audit content (plan §5.11.6,
// §5.11.9). The operator decides an agent's pending request in the approval
// window, revokes the lasting grants still running, unlocks raw parts for
// their own reading, and manages the raw password and Touch ID here. Every
// proof is forwarded once and never kept.
package raw

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
)

// BiometricKeyBytes is the size of the key the keychain keeps behind Touch
// ID, as Core expects.
const BiometricKeyBytes = 32

// maxPasswordChars is the longest raw password Core accepts, so a longer
// one cannot be correct.
const maxPasswordChars = 128

const grantPrefix = "rawgrant_"

// Outcome kinds of a proof-carrying call. Refusals the dialog can recover
// from are outcomes rather than errors, so it stays open for another
// attempt.
const (
	OutcomeDecided         = "decided"
	OutcomeSealing         = "sealing"
	OutcomeProofRequired   = "proof_required"
	OutcomePasswordInvalid = "password_invalid"
	OutcomeBackoff         = "backoff"
	OutcomeNotPending      = "not_pending"
	// OutcomeBiometricCancelled is a dismissed Touch ID prompt.
	OutcomeBiometricCancelled = "biometric_cancelled"
	// OutcomeBiometricUnavailable is Touch ID that cannot stand in for the
	// password right now: none on this Mac, none set up for raw content, or
	// its key no longer opens it.
	OutcomeBiometricUnavailable = "biometric_unavailable"
)

// ProofOutcome is what a proof-carrying call ended in.
type ProofOutcome struct {
	Outcome string `json:"outcome"`
	// Grant is the decided grant of OutcomeDecided.
	Grant json.RawMessage `json:"grant,omitempty"`
	// Status is the new raw sealing state of OutcomeSealing.
	Status json.RawMessage `json:"status,omitempty"`
	// RetryAfterSeconds is how long OutcomeBackoff waits. It is omitzero:
	// the IPC encodes with encoding/json/v2, whose omitempty keeps zeros.
	RetryAfterSeconds uint64 `json:"retry_after_seconds,omitzero"`
}

// ProofArg is the proof the dialog collected: a password, or a request for
// Touch ID, so no key passes the webview.
type ProofArg struct {
	Kind     string  `json:"kind"`
	Password *string `json:"password,omitempty"`
}

func (p *ProofArg) UnmarshalJSON(data []byte) error {
	type plain ProofArg
	var v plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&v); err != nil {
		return err
	}
	switch v.Kind {
	case "password":
		if v.Password == nil {
			return errors.New("a password proof omitted password")
		}
	case "biometric":
		if v.Password != nil {
			return errors.New("a biometric proof carries no password")
		}
	default:
		return fmt.Errorf("unknown proof kind %q", v.Kind)
	}
	*p = ProofArg(v)
	return nil
}

// Proof is a proof ready for Core: a password, or the key the keychain
// released after Touch ID.
type Proof struct {
	Password  string
	Biometric []byte
}

// ValidateGrantID checks the grant ID format Core issues.
func ValidateGrantID(id string) error {
	hex, ok := strings.CutPrefix(id, grantPrefix)
	if ok && len(hex) == 16 {
		valid := true
		for i := 0; i < len(hex); i++ {
			c := hex[i]
			valid = valid && (c >= '0' && c <= '9' || c >= 'a' && c <= 'f')
		}
		if valid {
			return nil
		}
	}
	return errors.New("raw access grant id is invalid")
}

// DecisionPath is where a grant's decision is posted.
func DecisionPath(grantID string) string {
	return control.RawAccessPath + "/" + grantID + "/decision"
}

// GrantPath is where DELETE revokes one running lasting grant.
func GrantPath(grantID string) (string, error) {
	if err := ValidateGrantID(grantID); err != nil {
		return "", err
	}
	return control.RawAccessPath + "/" + grantID, nil
}

type proofBody struct {
	Password  *string `json:"password,omitempty"`
	Biometric *string `json:"biometric,omitempty"`
}

func (p Proof) body() (*proofBody, error) {
	if p.Biometric != nil {
		key, err := encodeKey(p.Biometric)
		if err != nil {
			return nil, err
		}
		return &proofBody{Biometric: &key}, nil
	}
	password, err := checkedPassword(p.Password)
	if err != nil {
		return nil, err
	}
	return &proofBody{Password: &password}, nil
}

func encodeKey(key []byte) (string, error) {
	if len(key) != BiometricKeyBytes {
		return "", errors.New("the Touch ID key is invalid")
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

func checkedPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("the raw password is required")
	}
	if utf8.RuneCountInString(password) > maxPasswordChars {
		return "", errors.New("raw password is incorrect")
	}
	return password, nil
}

// DecisionBody encodes one decision. A denial never carries a proof; Core
// refuses an approval without one, unlocked or not.
func DecisionBody(grantID, decision string, proof *Proof) ([]byte, error) {
	if err := ValidateGrantID(grantID); err != nil {
		return nil, err
	}
	switch decision {
	case "deny":
		proof = nil
	case "once", "until_lock", "window_1h":
	default:
		return nil, errors.New("raw access decision must be once, until_lock, window_1h, or deny")
	}
	body := struct {
		Decision string     `json:"decision"`
		Proof    *proofBody `json:"proof,omitempty"`
	}{Decision: decision}
	if proof != nil {
		var err error
		if body.Proof, err = proof.body(); err != nil {
			return nil, err
		}
	}
	return encodeSecretJSON(body)
}

// UnlockBody encodes an unlock or verify call.
func UnlockBody(proof Proof) ([]byte, error) {
	encoded, err := proof.body()
	if err != nil {
		return nil, err
	}
	return encodeSecretJSON(struct {
		Proof *proofBody `json:"proof"`
	}{encoded})
}

// BiometricBody encodes a Touch ID enrollment: the raw password opens the
// key, which is then wrapped under key.
func BiometricBody(password string, key []byte) ([]byte, error) {
	checked, err := checkedPassword(password)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeKey(key)
	if err != nil {
		return nil, err
	}
	return encodeSecretJSON(struct {
		Proof proofBody `json:"proof"`
		Key   string    `json:"key"`
	}{proofBody{Password: &checked}, encoded})
}

// PasswordBody encodes a raw password action. password is the new
// password; proof opens the existing key where Core needs it.
func PasswordBody(action string, password *string, proof *Proof) ([]byte, error) {
	switch action {
	case "set", "change", "reset":
	default:
		return nil, errors.New("raw password action must be set, change, or reset")
	}
	if password != nil && *password == "" {
		password = nil
	}
	if password != nil && utf8.RuneCountInString(*password) > maxPasswordChars {
		return nil, fmt.Errorf("the raw password must contain at most %d characters", maxPasswordChars)
	}
	body := struct {
		Action   string     `json:"action"`
		Password *string    `json:"password,omitempty"`
		Proof    *proofBody `json:"proof,omitempty"`
	}{Action: action, Password: password}
	if proof != nil {
		var err error
		if body.Proof, err = proof.body(); err != nil {
			return nil, err
		}
	}
	return encodeSecretJSON(body)
}

// encodeSecretJSON encodes without HTML escaping, as the Tauri shell did.
// The caller clears the buffer once it is sent.
func encodeSecretJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("unable to encode control request: %v", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}

func errorCode(body []byte) (code string, retryAfter uint64, hasRetry bool, ok bool) {
	var envelope struct {
		Error *struct {
			Code    *string           `json:"code"`
			Details []json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Error == nil || envelope.Error.Code == nil {
		return "", 0, false, false
	}
	for _, detail := range envelope.Error.Details {
		var d struct {
			RetryAfterSeconds *uint64 `json:"retry_after_seconds"`
		}
		if json.Unmarshal(detail, &d) == nil && d.RetryAfterSeconds != nil {
			retryAfter, hasRetry = *d.RetryAfterSeconds, true
			break
		}
	}
	return *envelope.Error.Code, retryAfter, hasRetry, true
}

// proofRefusal maps the refusals shared by every proof-carrying call.
func proofRefusal(status int, body []byte) *ProofOutcome {
	code, retryAfter, hasRetry, ok := errorCode(body)
	if !ok {
		return nil
	}
	switch {
	case status == http.StatusForbidden && code == "raw_password_invalid":
		return &ProofOutcome{Outcome: OutcomePasswordInvalid}
	case status == http.StatusTooManyRequests && code == "raw_password_backoff":
		if !hasRetry {
			retryAfter = 1
		}
		return &ProofOutcome{Outcome: OutcomeBackoff, RetryAfterSeconds: max(retryAfter, 1)}
	case status == http.StatusForbidden && code == "raw_biometric_invalid",
		status == http.StatusConflict && code == "raw_biometric_unavailable":
		return &ProofOutcome{Outcome: OutcomeBiometricUnavailable}
	}
	return nil
}

// DecisionOutcome maps a decision response onto an outcome, or nil for a
// failure the caller reports as an error.
func DecisionOutcome(status int, body []byte) *ProofOutcome {
	if status == http.StatusOK {
		var grant map[string]json.RawMessage
		if json.Unmarshal(body, &grant) != nil {
			return nil
		}
		// A JSON null decodes into a string too, so the kind is checked.
		if id := bytes.TrimSpace(grant["grant_id"]); len(id) == 0 || id[0] != '"' {
			return nil
		}
		return &ProofOutcome{Outcome: OutcomeDecided, Grant: body}
	}
	if refusal := proofRefusal(status, body); refusal != nil {
		return refusal
	}
	code, _, _, ok := errorCode(body)
	if !ok {
		return nil
	}
	switch {
	case status == http.StatusConflict && code == "raw_access_not_pending",
		status == http.StatusNotFound && code == "not_found":
		return &ProofOutcome{Outcome: OutcomeNotPending}
	case status == http.StatusUnprocessableEntity && code == "raw_proof_required":
		return &ProofOutcome{Outcome: OutcomeProofRequired}
	}
	return nil
}

// SealingOutcome maps an unlock or password response onto an outcome, or
// nil for a failure the caller reports as an error.
func SealingOutcome(status int, body []byte) *ProofOutcome {
	if status == http.StatusOK {
		if _, err := ParseSealingStatus(body); err != nil {
			return nil
		}
		return &ProofOutcome{Outcome: OutcomeSealing, Status: body}
	}
	return proofRefusal(status, body)
}

// ParseSealingStatus checks a raw sealing state and returns it as an
// object the host can annotate.
func ParseSealingStatus(body []byte) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("raw sealing state returned invalid JSON: %v", err)
	}
	if configured := string(bytes.TrimSpace(value["configured"])); configured != "true" && configured != "false" {
		return nil, errors.New("raw sealing state omitted configured")
	}
	return value, nil
}

// ParseList checks the pending requests and the lasting grants still
// running.
func ParseList(body []byte) (json.RawMessage, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("raw access list returned invalid JSON: %v", err)
	}
	for _, key := range []string{"items", "active"} {
		var items []json.RawMessage
		if raw, ok := value[key]; !ok || json.Unmarshal(raw, &items) != nil || items == nil {
			return nil, fmt.Errorf("raw access list omitted %s", key)
		}
	}
	return body, nil
}
