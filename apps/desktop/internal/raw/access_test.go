package raw

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
)

const grant = "rawgrant_0123456789abcdef"

func password(value string) *Proof { return &Proof{Password: value} }

func decoded(t *testing.T, body []byte) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	return value
}

func want(t *testing.T, literal string) any {
	t.Helper()
	return decoded(t, []byte(literal))
}

func TestGrantIDsMustMatchTheCoreFormat(t *testing.T) {
	if err := ValidateGrantID(grant); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		"",
		"rawgrant_",
		"rawgrant_0123456789ABCDEF",
		"rawgrant_0123456789abcde",
		"rawgrant_0123456789abcdef0",
		"rawgrant_0123456789abcdef/../x",
		"grant_0123456789abcdef",
	} {
		if ValidateGrantID(invalid) == nil {
			t.Errorf("%q accepted", invalid)
		}
	}
}

func TestDecisionsCarryAProofOnlyWhenApproving(t *testing.T) {
	key := bytes.Repeat([]byte{0xfb}, BiometricKeyBytes)
	encodedKey := base64.StdEncoding.EncodeToString(key)
	for _, tc := range []struct {
		decision string
		proof    *Proof
		want     string
	}{
		{"once", password(`pa"ss`), `{"decision":"once","proof":{"password":"pa\"ss"}}`},
		{"window_1h", password("secret"), `{"decision":"window_1h","proof":{"password":"secret"}}`},
		{"deny", password("ignored"), `{"decision":"deny"}`},
		{"until_lock", &Proof{Biometric: key}, `{"decision":"until_lock","proof":{"biometric":"` + encodedKey + `"}}`},
	} {
		body, err := DecisionBody(grant, tc.decision, tc.proof)
		if err != nil {
			t.Fatalf("%s: %v", tc.decision, err)
		}
		if got := decoded(t, body); !reflect.DeepEqual(got, want(t, tc.want)) {
			t.Errorf("%s: got %s", tc.decision, body)
		}
	}
	for _, tc := range []struct {
		grant, decision string
		proof           *Proof
	}{
		{grant, "window_5m", password("secret")},
		{grant, "until_lock", password("")},
		{grant, "once", &Proof{Biometric: make([]byte, 16)}},
		{grant, "always", password("secret")},
		{"rawgrant_bad", "deny", nil},
		{grant, "once", password(strings.Repeat("x", maxPasswordChars+1))},
	} {
		if _, err := DecisionBody(tc.grant, tc.decision, tc.proof); err == nil {
			t.Errorf("%s %s accepted", tc.grant, tc.decision)
		}
	}
}

func TestUnlockAndPasswordBodiesMatchTheContract(t *testing.T) {
	newPassword, empty := "new phrase", ""
	sevens := bytes.Repeat([]byte{7}, BiometricKeyBytes)
	nines := bytes.Repeat([]byte{9}, BiometricKeyBytes)
	encode := base64.StdEncoding.EncodeToString
	for _, tc := range []struct {
		name string
		body func() ([]byte, error)
		want string
	}{
		{"unlock password", func() ([]byte, error) { return UnlockBody(*password("open sesame")) },
			`{"proof":{"password":"open sesame"}}`},
		{"unlock biometric", func() ([]byte, error) { return UnlockBody(Proof{Biometric: sevens}) },
			`{"proof":{"biometric":"` + encode(sevens) + `"}}`},
		{"enroll", func() ([]byte, error) { return BiometricBody("open sesame", nines) },
			`{"proof":{"password":"open sesame"},"key":"` + encode(nines) + `"}`},
		{"set", func() ([]byte, error) { return PasswordBody("set", &newPassword, nil) },
			`{"action":"set","password":"new phrase"}`},
		{"change", func() ([]byte, error) { return PasswordBody("change", &newPassword, password("old phrase")) },
			`{"action":"change","password":"new phrase","proof":{"password":"old phrase"}}`},
		{"reset", func() ([]byte, error) { return PasswordBody("reset", &empty, nil) },
			`{"action":"reset"}`},
	} {
		body, err := tc.body()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := decoded(t, body); !reflect.DeepEqual(got, want(t, tc.want)) {
			t.Errorf("%s: got %s", tc.name, body)
		}
	}
	long := strings.Repeat("x", maxPasswordChars+1)
	for name, body := range map[string]func() ([]byte, error){
		"empty unlock":   func() ([]byte, error) { return UnlockBody(*password("")) },
		"empty enroll":   func() ([]byte, error) { return BiometricBody("", nines) },
		"short key":      func() ([]byte, error) { return BiometricBody("open sesame", nines[:31]) },
		"unknown action": func() ([]byte, error) { return PasswordBody("rotate", &newPassword, nil) },
		"long password":  func() ([]byte, error) { return PasswordBody("set", &long, nil) },
	} {
		if _, err := body(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestProofArgumentsTakeAPasswordOrAskForTouchID(t *testing.T) {
	var arg ProofArg
	if err := json.Unmarshal([]byte(`{"kind":"password","password":"open sesame"}`), &arg); err != nil {
		t.Fatal(err)
	}
	if arg.Kind != "password" || arg.Password == nil || *arg.Password != "open sesame" {
		t.Fatalf("password proof = %+v", arg)
	}
	// The webview only asks for Touch ID; it never carries the key.
	arg = ProofArg{}
	if err := json.Unmarshal([]byte(`{"kind":"biometric"}`), &arg); err != nil || arg.Kind != "biometric" {
		t.Fatalf("biometric proof = %+v, %v", arg, err)
	}
	for _, invalid := range []string{
		`{"password":"open sesame"}`,
		`{"kind":"password","password":"open sesame","extra":"x"}`,
		`{"kind":"password"}`,
		`{"kind":"biometric","biometric":"AAAA"}`,
		`{"kind":"token"}`,
	} {
		var arg ProofArg
		if json.Unmarshal([]byte(invalid), &arg) == nil {
			t.Errorf("%s accepted", invalid)
		}
	}
}

func TestSealingResponsesMapOntoOutcomes(t *testing.T) {
	status := []byte(`{"raw_available":true,"configured":true,"unlocked":true}`)
	if got := SealingOutcome(http.StatusOK, status); got == nil || got.Outcome != OutcomeSealing {
		t.Fatalf("sealing outcome = %+v", got)
	}
	for _, tc := range []struct {
		status int
		body   string
		want   *ProofOutcome
	}{
		{http.StatusOK, `{"raw_available":true}`, nil},
		{http.StatusOK, `{"configured":null}`, nil},
		{http.StatusForbidden, `{"error":{"code":"raw_password_invalid"}}`, &ProofOutcome{Outcome: OutcomePasswordInvalid}},
		{http.StatusUnprocessableEntity, `{"error":{"code":"raw_proof_required"}}`, nil},
	} {
		if got := SealingOutcome(tc.status, []byte(tc.body)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%d %s = %+v", tc.status, tc.body, got)
		}
	}
}

func TestDecisionResponsesMapOntoOutcomes(t *testing.T) {
	body := []byte(`{"grant_id":"rawgrant_0123456789abcdef","status":"approved"}`)
	if got := DecisionOutcome(http.StatusOK, body); got == nil || got.Outcome != OutcomeDecided || string(got.Grant) != string(body) {
		t.Fatalf("decided outcome = %+v", got)
	}
	for _, tc := range []struct {
		status int
		body   string
		want   *ProofOutcome
	}{
		{http.StatusForbidden, `{"error":{"code":"raw_password_invalid","message":"x"}}`, &ProofOutcome{Outcome: OutcomePasswordInvalid}},
		{http.StatusTooManyRequests, `{"error":{"code":"raw_password_backoff","details":[{"reason":"retry_after","retry_after_seconds":8}]}}`, &ProofOutcome{Outcome: OutcomeBackoff, RetryAfterSeconds: 8}},
		{http.StatusTooManyRequests, `{"error":{"code":"raw_password_backoff","details":[{"retry_after_seconds":0}]}}`, &ProofOutcome{Outcome: OutcomeBackoff, RetryAfterSeconds: 1}},
		{http.StatusTooManyRequests, `{"error":{"code":"raw_password_backoff"}}`, &ProofOutcome{Outcome: OutcomeBackoff, RetryAfterSeconds: 1}},
		{http.StatusConflict, `{"error":{"code":"raw_access_not_pending"}}`, &ProofOutcome{Outcome: OutcomeNotPending}},
		{http.StatusNotFound, `{"error":{"code":"not_found"}}`, &ProofOutcome{Outcome: OutcomeNotPending}},
		{http.StatusUnprocessableEntity, `{"error":{"code":"raw_proof_required"}}`, &ProofOutcome{Outcome: OutcomeProofRequired}},
		{http.StatusConflict, `{"error":{"code":"raw_access_unavailable"}}`, nil},
		{http.StatusForbidden, `{"error":{"code":"raw_biometric_invalid"}}`, &ProofOutcome{Outcome: OutcomeBiometricUnavailable}},
		{http.StatusConflict, `{"error":{"code":"raw_biometric_unavailable"}}`, &ProofOutcome{Outcome: OutcomeBiometricUnavailable}},
		{http.StatusOK, `{}`, nil},
		{http.StatusOK, `{"grant_id":null}`, nil},
	} {
		if got := DecisionOutcome(tc.status, []byte(tc.body)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%d %s = %+v", tc.status, tc.body, got)
		}
	}
	encoded, err := json.Marshal(ProofOutcome{Outcome: OutcomeBackoff, RetryAfterSeconds: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded(t, encoded); !reflect.DeepEqual(got, want(t, `{"outcome":"backoff","retry_after_seconds":2}`)) {
		t.Fatalf("backoff encodes as %s", encoded)
	}
	encoded, _ = json.Marshal(ProofOutcome{Outcome: OutcomeNotPending})
	if string(encoded) != `{"outcome":"not_pending"}` {
		t.Fatalf("not pending encodes as %s", encoded)
	}
}

func TestOnlyProofCallsAreSingleAttemptRequests(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, DecisionPath(grant), true},
		{http.MethodPost, control.RawUnlockPath, true},
		{http.MethodPost, control.RawVerifyPath, true},
		{http.MethodPost, control.RawPasswordPath, true},
		{http.MethodPost, control.RawBiometricPath, true},
		{http.MethodDelete, control.RawBiometricPath, false},
		{http.MethodPost, control.RawLockPath, false},
		{http.MethodGet, control.RawSealingPath, false},
		{http.MethodGet, DecisionPath(grant), false},
		{http.MethodGet, control.RawAccessPath, false},
		{http.MethodPost, "/control/v1/requests/request_1/audit/raw-access", false},
	} {
		if got := control.IsProofRequest(tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s = %v", tc.method, tc.path, got)
		}
	}
}

func TestListsMustCarryPendingAndActive(t *testing.T) {
	if _, err := ParseList([]byte(`{"items":[],"active":[]}`)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`{"items":[]}`, `{}`, `{"items":[],"active":null}`, "not json"} {
		if _, err := ParseList([]byte(invalid)); err == nil {
			t.Errorf("%s accepted", invalid)
		}
	}
}

func TestGrantPathsTakeValidIDsOnly(t *testing.T) {
	path, err := GrantPath(grant)
	if err != nil || path != control.RawAccessPath+"/"+grant {
		t.Fatalf("grant path = %q, %v", path, err)
	}
	if _, err := GrantPath("rawgrant_bad/../x"); err == nil {
		t.Fatal("traversal accepted")
	}
}
