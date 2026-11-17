package raw

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/darwin"
	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// fakeBiometrics is Touch ID with an in-memory keychain.
type fakeBiometrics struct {
	mu        sync.Mutex
	available bool
	// refusal is the outcome of the next prompt, "" to release the key.
	refusal  string
	keys     map[string][]byte
	reasons  []string
	storeErr error
	forgets  []string
}

func newFakeBiometrics() *fakeBiometrics {
	return &fakeBiometrics{available: true, keys: map[string][]byte{}}
}

func (f *fakeBiometrics) Available() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.available
}

func (f *fakeBiometrics) Release(account, reason string) ([]byte, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reasons = append(f.reasons, reason)
	if f.refusal != "" {
		return nil, f.refusal
	}
	key, ok := f.keys[account]
	if !ok {
		return nil, OutcomeBiometricUnavailable
	}
	return bytes.Clone(key), ""
}

func (f *fakeBiometrics) Store(account string, key []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.storeErr != nil {
		return f.storeErr
	}
	f.keys[account] = bytes.Clone(key)
	return nil
}

func (f *fakeBiometrics) Forget(account string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgets = append(f.forgets, account)
	delete(f.keys, account)
}

func newTouchIDBridge(t *testing.T) (*rawBridge, *fakeBiometrics) {
	b := newRawBridge(t)
	touch := newFakeBiometrics()
	b.raw.Biometrics = touch
	b.raw.Locale = func() prefs.Locale { return prefs.LocaleZhCN }
	return b, touch
}

func TestTouchIDReleasesTheKeychainKeyToCoreOnly(t *testing.T) {
	b, touch := newTouchIDBridge(t)
	ctx := t.Context()
	account := keystore.KeychainAccount(b.core.directory)
	key := bytes.Repeat([]byte{7}, BiometricKeyBytes)
	touch.keys[account] = key
	b.core.answer(http.StatusOK, sealedStatus)
	outcome, err := b.raw.UnlockRaw(ctx, biometricArg)
	if err != nil || outcome.Outcome != OutcomeSealing {
		t.Fatalf("unlock = %+v, %v", outcome, err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if !sameJSON(t, []byte(b.core.bodies[0]), `{"proof":{"biometric":"`+encoded+`"}}`) {
		t.Errorf("body %s", b.core.bodies[0])
	}
	if !sameJSON(t, outcome.Status, `{"raw_available":true,"configured":true,"unlocked":false,"key_replaced":false,"biometric_available":true}`) {
		t.Errorf("status %s", outcome.Status)
	}
	// The prompt names the request in the operator's language.
	if touch.reasons[0] != prefs.T(prefs.LocaleZhCN, unlockReason) || touch.reasons[0] == unlockReason {
		t.Errorf("reason %q", touch.reasons[0])
	}
	grantBody := `{"grant_id":"` + grant + `","status":"approved"}`
	b.core.answer(http.StatusOK, grantBody)
	if outcome, err := b.raw.DecideRawAccess(ctx, grant, "once", &biometricArg); err != nil || outcome.Outcome != OutcomeDecided {
		t.Fatalf("decide = %+v, %v", outcome, err)
	}
	if touch.reasons[1] != prefs.T(prefs.LocaleZhCN, approveReason) {
		t.Errorf("approve reason %q", touch.reasons[1])
	}
}

func TestTouchIDRefusalsComeBackAsOutcomesWithoutReachingCore(t *testing.T) {
	b, touch := newTouchIDBridge(t)
	ctx := t.Context()
	b.core.answer(http.StatusOK, sealedStatus)
	for _, refusal := range []string{OutcomeBiometricCancelled, OutcomeBiometricUnavailable} {
		touch.refusal = refusal
		if outcome, err := b.raw.UnlockRaw(ctx, biometricArg); err != nil || outcome.Outcome != refusal {
			t.Errorf("%s = %+v, %v", refusal, outcome, err)
		}
	}
	touch.refusal = ""
	// No key in the keychain.
	if outcome, _ := b.raw.UnlockRaw(ctx, biometricArg); outcome.Outcome != OutcomeBiometricUnavailable {
		t.Errorf("without a key = %+v", outcome)
	}
	b.core.directory = ""
	if outcome, _ := b.raw.UnlockRaw(ctx, biometricArg); outcome.Outcome != OutcomeBiometricUnavailable {
		t.Errorf("before Core = %+v", outcome)
	}
	if len(b.core.requests) != 0 {
		t.Errorf("sent %v", b.core.requests)
	}
}

func TestEnrollStoresAKeyCoreWrapsAndForgetsItOnRefusal(t *testing.T) {
	b, touch := newTouchIDBridge(t)
	ctx := t.Context()
	account := keystore.KeychainAccount(b.core.directory)
	b.core.answer(http.StatusOK, `{"raw_available":true,"configured":true,"unlocked":false,"envelopes":["password","biometric"]}`)
	outcome, err := b.raw.EnrollRawBiometric(ctx, "hunter2")
	if err != nil || outcome.Outcome != OutcomeSealing || b.changes != 1 {
		t.Fatalf("enroll = %+v, %v, %d changes", outcome, err, b.changes)
	}
	key, ok := touch.keys[account]
	if !ok || len(key) != BiometricKeyBytes {
		t.Fatal("no key in the keychain")
	}
	if !reflect.DeepEqual(b.core.requests, []string{"POST " + control.RawBiometricPath}) ||
		!sameJSON(t, []byte(b.core.bodies[0]), `{"proof":{"password":"hunter2"},"key":"`+base64.StdEncoding.EncodeToString(key)+`"}`) {
		t.Errorf("request %v %v", b.core.requests, b.core.bodies)
	}
	if !sameJSON(t, outcome.Status, `{"raw_available":true,"configured":true,"unlocked":false,"envelopes":["password","biometric"],"biometric_available":true}`) {
		t.Errorf("status %s", outcome.Status)
	}
	// A wrong password leaves nothing in the keychain.
	b.core.answer(http.StatusForbidden, `{"error":{"code":"raw_password_invalid"}}`)
	if outcome, err := b.raw.EnrollRawBiometric(ctx, "wrong"); err != nil || outcome.Outcome != OutcomePasswordInvalid {
		t.Fatalf("wrong password = %+v, %v", outcome, err)
	}
	if _, ok := touch.keys[account]; ok {
		t.Error("the refused key stayed in the keychain")
	}
	// Without Touch ID nothing is stored or sent.
	touch.available = false
	b.core.answer(http.StatusOK, sealedStatus)
	if outcome, _ := b.raw.EnrollRawBiometric(ctx, "hunter2"); outcome.Outcome != OutcomeBiometricUnavailable || len(b.core.requests) != 0 {
		t.Errorf("unavailable = %+v, sent %v", outcome, b.core.requests)
	}
}

func TestRemoveTakesTheKeyOutOfTheKeychainToo(t *testing.T) {
	b, touch := newTouchIDBridge(t)
	account := keystore.KeychainAccount(b.core.directory)
	touch.keys[account] = make([]byte, BiometricKeyBytes)
	b.core.answer(http.StatusOK, sealedStatus)
	status, err := b.raw.RemoveRawBiometric(t.Context())
	if err != nil || !sameJSON(t, status, `{"raw_available":true,"configured":true,"unlocked":false,"biometric_available":true}`) {
		t.Fatalf("remove = %s, %v", status, err)
	}
	if _, ok := touch.keys[account]; ok || len(touch.forgets) != 1 {
		t.Error("the key stayed in the keychain")
	}
}

func TestAChangedPasswordEnrollsANewTouchIDKey(t *testing.T) {
	b, touch := newTouchIDBridge(t)
	ctx := t.Context()
	account := keystore.KeychainAccount(b.core.directory)
	old := bytes.Repeat([]byte{1}, BiometricKeyBytes)
	touch.keys[account] = old
	b.pins.replaced = true
	b.core.answer(http.StatusOK, `{"raw_available":true,"configured":true,"unlocked":false,"envelopes":["password","biometric"]}`)
	outcome, err := b.raw.SetRawPassword(ctx, "change", new("new password"), &biometricArg)
	if err != nil || outcome.Outcome != OutcomeSealing {
		t.Fatalf("change = %+v, %v", outcome, err)
	}
	if !reflect.DeepEqual(b.core.requests, []string{"POST " + control.RawPasswordPath, "POST " + control.RawBiometricPath}) {
		t.Fatalf("requests %v", b.core.requests)
	}
	renewed := touch.keys[account]
	if len(renewed) != BiometricKeyBytes || bytes.Equal(renewed, old) {
		t.Fatal("the key was not renewed")
	}
	// The host's verdict on the key stays.
	if !sameJSON(t, outcome.Status, `{"raw_available":true,"configured":true,"unlocked":false,"envelopes":["password","biometric"],"key_replaced":true,"biometric_available":true}`) {
		t.Errorf("status %s", outcome.Status)
	}
	// Without Touch ID the old key just goes.
	touch.available = false
	touch.keys[account] = old
	b.core.answer(http.StatusOK, sealedStatus)
	if _, err := b.raw.SetRawPassword(ctx, "change", new("again"), &ProofArg{Kind: "password", Password: new("new password")}); err != nil {
		t.Fatal(err)
	}
	if _, ok := touch.keys[account]; ok || len(b.core.requests) != 1 {
		t.Errorf("key kept %v, requests %v", ok, b.core.requests)
	}
}

func TestPromptFailuresSplitIntoCancelledAndUnavailable(t *testing.T) {
	for code, want := range map[int]string{
		darwin.LAErrorUserCancel:           OutcomeBiometricCancelled,
		darwin.LAErrorSystemCancel:         OutcomeBiometricCancelled,
		darwin.LAErrorUserFallback:         OutcomeBiometricCancelled,
		darwin.LAErrorAuthenticationFailed: OutcomeBiometricCancelled,
		darwin.LAErrorBiometryNotEnrolled:  OutcomeBiometricUnavailable,
		darwin.LAErrorBiometryLockout:      OutcomeBiometricUnavailable,
		darwin.LAErrorBiometryNotAvailable: OutcomeBiometricUnavailable,
		darwin.LAErrorNotInteractive:       OutcomeBiometricUnavailable,
	} {
		if got := biometricOutcome(darwin.BiometryOutcome{Code: code}); got != want {
			t.Errorf("code %d = %s, want %s", code, got, want)
		}
	}
	if got := biometricOutcome(darwin.BiometryOutcome{TimedOut: true}); got != OutcomeBiometricCancelled {
		t.Errorf("timed out = %s", got)
	}
}

func TestBuildsWithoutTouchIDNeverOfferIt(t *testing.T) {
	// Tests are unsigned builds, which keep the local key in a file.
	var system systemBiometrics
	if system.Available() {
		t.Fatal("an unsigned build offers Touch ID")
	}
	if key, outcome := system.Release("account", "unlock"); key != nil || outcome != OutcomeBiometricUnavailable {
		t.Fatal("an unsigned build released a key")
	}
	if system.Store("account", make([]byte, BiometricKeyBytes)) == nil {
		t.Fatal("an unsigned build stored a key")
	}
	first, _ := NewBiometricKey()
	second, _ := NewBiometricKey()
	if len(first) != BiometricKeyBytes || bytes.Equal(first, second) {
		t.Fatal("keys are not random")
	}
}
