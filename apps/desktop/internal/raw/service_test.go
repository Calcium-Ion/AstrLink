package raw

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
)

// fakeCore answers the raw routes from an httptest listener through the
// real control client and records what the commands told it.
type fakeCore struct {
	server *httptest.Server
	client *control.Client
	pins   *fakePins

	mu        sync.Mutex
	requests  []string
	bodies    []string
	heldPins  []bool
	status    int
	body      string
	directory string
	replaced  []bool
	polls     int
	// moved is what NoteRawKeyReplaced reports.
	moved bool
}

func newFakeCore(t *testing.T, pins *fakePins) *fakeCore {
	f := &fakeCore{client: control.NewClient(), pins: pins, status: http.StatusOK, directory: "/data/astrlink"}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
		f.bodies = append(f.bodies, string(body))
		f.heldPins = append(f.heldPins, pins.held())
		status, answer := f.status, f.body
		f.mu.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeCore) Call(ctx context.Context, req control.Request) (control.Response, error) {
	return f.client.Do(ctx, f.server.URL, "operator-token", req)
}

func (f *fakeCore) DataDirectory() string { return f.directory }

func (f *fakeCore) NoteRawKeyReplaced(replaced bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replaced = append(f.replaced, replaced)
	return f.moved
}

func (f *fakeCore) PollObservers(context.Context) error {
	f.mu.Lock()
	f.polls++
	f.mu.Unlock()
	return nil
}

func (f *fakeCore) answer(status int, body string) {
	f.mu.Lock()
	f.status, f.body = status, body
	f.requests, f.bodies, f.heldPins, f.replaced, f.polls = nil, nil, nil, nil, 0
	f.mu.Unlock()
}

type pinCall struct {
	account string
	action  keystore.PinAction
}

type fakePins struct {
	mu       sync.Mutex
	locked   bool
	calls    []pinCall
	replaced bool
}

func (p *fakePins) Lock() {
	p.mu.Lock()
	p.locked = true
	p.mu.Unlock()
}

func (p *fakePins) Unlock() {
	p.mu.Lock()
	p.locked = false
	p.mu.Unlock()
}

func (p *fakePins) held() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.locked
}

func (p *fakePins) Apply(account string, _ json.RawMessage, action keystore.PinAction) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, pinCall{account, action})
	return p.replaced
}

type rawBridge struct {
	core    *fakeCore
	pins    *fakePins
	raw     Raw
	changes int
}

func newRawBridge(t *testing.T) *rawBridge {
	b := &rawBridge{pins: &fakePins{}}
	b.core = newFakeCore(t, b.pins)
	b.raw = Raw{Core: b.core, Pins: b.pins, Changed: func() { b.changes++ }}
	return b
}

const sealedStatus = `{"raw_available":true,"configured":true,"unlocked":false}`

func passwordArg(value string) ProofArg { return ProofArg{Kind: "password", Password: &value} }

var biometricArg = ProofArg{Kind: "biometric"}

func sameJSON(t *testing.T, got []byte, literal string) bool {
	t.Helper()
	return reflect.DeepEqual(decoded(t, got), want(t, literal))
}

func TestDecideRawAccessSendsOneProofAndReadsTheQueueAgain(t *testing.T) {
	b := newRawBridge(t)
	ctx := t.Context()
	decided := `{"grant_id":"` + grant + `","status":"approved"}`
	b.core.answer(http.StatusOK, decided)
	arg := passwordArg("hunter2")
	outcome, err := b.raw.DecideRawAccess(ctx, grant, "once", &arg)
	if err != nil || outcome.Outcome != OutcomeDecided || string(outcome.Grant) != decided {
		t.Fatalf("decide = %+v, %v", outcome, err)
	}
	path := DecisionPath(grant)
	if !reflect.DeepEqual(b.core.requests, []string{"POST " + path}) ||
		!sameJSON(t, []byte(b.core.bodies[0]), `{"decision":"once","proof":{"password":"hunter2"}}`) {
		t.Errorf("request = %v %v", b.core.requests, b.core.bodies)
	}
	if control.Timeout(http.MethodPost, path) != control.ProofTimeout || !control.IsProofRequest(http.MethodPost, path) {
		t.Error("a decision is not a single-attempt proof request")
	}
	if b.core.polls != 1 {
		t.Errorf("polls = %d", b.core.polls)
	}

	b.core.answer(http.StatusOK, decided)
	if _, err := b.raw.DecideRawAccess(ctx, grant, "deny", &arg); err != nil {
		t.Fatal(err)
	}
	if !sameJSON(t, []byte(b.core.bodies[0]), `{"decision":"deny"}`) {
		t.Errorf("a denial carried %s", b.core.bodies[0])
	}

	b.core.answer(http.StatusForbidden, `{"error":{"code":"raw_password_invalid"}}`)
	if outcome, err := b.raw.DecideRawAccess(ctx, grant, "once", &arg); err != nil || outcome.Outcome != OutcomePasswordInvalid {
		t.Errorf("wrong password = %+v, %v", outcome, err)
	}

	refusal := `{"error":{"code":"raw_access_unavailable"}}`
	b.core.answer(http.StatusConflict, refusal)
	_, err = b.raw.DecideRawAccess(ctx, grant, "once", &arg)
	if err == nil || err.Error() != "POST "+path+" returned 409 Conflict: "+refusal {
		t.Errorf("refusal error = %v", err)
	}
	if b.core.polls != 1 {
		t.Errorf("a failed decision did not read the queue again: %d polls", b.core.polls)
	}

	b.core.answer(http.StatusOK, decided)
	if outcome, err := b.raw.DecideRawAccess(ctx, grant, "once", &biometricArg); err != nil || outcome.Outcome != OutcomeBiometricUnavailable {
		t.Errorf("touch id = %+v, %v", outcome, err)
	}
	if _, err := b.raw.DecideRawAccess(ctx, "rawgrant_bad", "once", &arg); err == nil {
		t.Error("a malformed grant ID was accepted")
	}
	if len(b.core.requests) != 0 {
		t.Errorf("sent %v", b.core.requests)
	}
}

func TestListAndRevokeRawAccess(t *testing.T) {
	b := newRawBridge(t)
	ctx := t.Context()
	b.core.answer(http.StatusOK, `{"items":[],"active":[],"approval_requires_proof":true}`)
	if list, err := b.raw.ListRawAccess(ctx); err != nil || !strings.Contains(string(list), "approval_requires_proof") {
		t.Fatalf("list = %s, %v", list, err)
	}
	if !reflect.DeepEqual(b.core.requests, []string{"GET " + control.RawAccessPath}) {
		t.Errorf("list request = %v", b.core.requests)
	}
	b.core.answer(http.StatusOK, `{"items":[]}`)
	if _, err := b.raw.ListRawAccess(ctx); err == nil {
		t.Error("a list without active was accepted")
	}

	b.core.answer(http.StatusOK, `{"grant_id":"`+grant+`","status":"revoked"}`)
	if _, err := b.raw.RevokeRawGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.core.requests, []string{"DELETE " + control.RawAccessPath + "/" + grant}) {
		t.Errorf("revoke request = %v", b.core.requests)
	}
	b.core.answer(http.StatusOK, "not json")
	if _, err := b.raw.RevokeRawGrant(ctx, grant); err == nil || !strings.Contains(err.Error(), "raw grant revoke returned invalid JSON") {
		t.Errorf("invalid answer = %v", err)
	}
	b.core.answer(http.StatusNotFound, `{"error":{"code":"not_found"}}`)
	if _, err := b.raw.RevokeRawGrant(ctx, grant); err == nil || !strings.Contains(err.Error(), "returned 404 Not Found") {
		t.Errorf("missing grant = %v", err)
	}
	b.core.answer(http.StatusOK, `{}`)
	if _, err := b.raw.RevokeRawGrant(ctx, "../x"); err == nil || len(b.core.requests) != 0 {
		t.Errorf("a malformed grant ID was sent: %v", b.core.requests)
	}
}

// TestSealingCommandsCheckTheKeyPin covers the commands that read or move
// the raw unlock: the pin action each applies under the pins lock, the
// verdict handed to the tray and the fields added for the dialogs.
func TestSealingCommandsCheckTheKeyPin(t *testing.T) {
	account := keystore.KeychainAccount("/data/astrlink")
	ctx := t.Context()
	for _, tc := range []struct {
		name    string
		call    func(r Raw) (json.RawMessage, error)
		request string
		body    string
		action  keystore.PinAction
		changes int
	}{
		{"raw_sealing_status", func(r Raw) (json.RawMessage, error) { return r.RawSealingStatus(ctx) },
			"GET " + control.RawSealingPath, "", keystore.PinCheck, 0},
		{"lock_raw", func(r Raw) (json.RawMessage, error) { return r.LockRaw(ctx) },
			"POST " + control.RawLockPath, "", keystore.PinCheck, 1},
		{"unlock_raw", func(r Raw) (json.RawMessage, error) {
			outcome, err := r.UnlockRaw(ctx, passwordArg("pw"))
			return outcome.Status, err
		}, "POST " + control.RawUnlockPath, `{"proof":{"password":"pw"}}`, keystore.PinCheck, 1},
		{"acknowledge_raw_key", func(r Raw) (json.RawMessage, error) {
			outcome, err := r.AcknowledgeRawKey(ctx, passwordArg("pw"))
			return outcome.Status, err
		}, "POST " + control.RawVerifyPath, `{"proof":{"password":"pw"}}`, keystore.PinRecord, 1},
		{"set_raw_password set", func(r Raw) (json.RawMessage, error) {
			outcome, err := r.SetRawPassword(ctx, "set", new("new-pw"), nil)
			return outcome.Status, err
		}, "POST " + control.RawPasswordPath, `{"action":"set","password":"new-pw"}`, keystore.PinRecord, 1},
		{"set_raw_password change", func(r Raw) (json.RawMessage, error) {
			arg := passwordArg("old-pw")
			outcome, err := r.SetRawPassword(ctx, "change", new("new-pw"), &arg)
			return outcome.Status, err
		}, "POST " + control.RawPasswordPath, `{"action":"change","password":"new-pw","proof":{"password":"old-pw"}}`, keystore.PinCheck, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newRawBridge(t)
			b.pins.replaced = true
			b.core.answer(http.StatusOK, sealedStatus)
			status, err := tc.call(b.raw)
			if err != nil {
				t.Fatal(err)
			}
			if !sameJSON(t, status, `{"raw_available":true,"configured":true,"unlocked":false,"key_replaced":true,"biometric_available":false}`) {
				t.Errorf("status = %s", status)
			}
			if !reflect.DeepEqual(b.core.requests, []string{tc.request}) || !b.core.heldPins[0] {
				t.Errorf("requests = %v, pins held %v", b.core.requests, b.core.heldPins)
			}
			if tc.body == "" && b.core.bodies[0] != "" || tc.body != "" && !sameJSON(t, []byte(b.core.bodies[0]), tc.body) {
				t.Errorf("body = %s, want %s", b.core.bodies[0], tc.body)
			}
			if !reflect.DeepEqual(b.pins.calls, []pinCall{{account, tc.action}}) {
				t.Errorf("pin calls = %+v", b.pins.calls)
			}
			if !reflect.DeepEqual(b.core.replaced, []bool{true}) {
				t.Errorf("tray verdicts = %v", b.core.replaced)
			}
			if b.changes != tc.changes {
				t.Errorf("changes = %d, want %d", b.changes, tc.changes)
			}
			if b.pins.held() {
				t.Error("the pins stayed locked")
			}

			b.core.answer(http.StatusServiceUnavailable, `{"error":{"code":"unavailable"}}`)
			if _, err := tc.call(b.raw); err == nil || !strings.Contains(err.Error(), "returned 503 Service Unavailable") {
				t.Errorf("refusal = %v", err)
			}
		})
	}
}

func TestProofCommandsReturnRefusalsAsOutcomes(t *testing.T) {
	b := newRawBridge(t)
	ctx := t.Context()
	b.core.answer(http.StatusTooManyRequests, `{"error":{"code":"raw_password_backoff","details":[{"retry_after_seconds":8}]}}`)
	outcome, err := b.raw.UnlockRaw(ctx, passwordArg("pw"))
	if err != nil || !reflect.DeepEqual(outcome, ProofOutcome{Outcome: OutcomeBackoff, RetryAfterSeconds: 8}) {
		t.Fatalf("backoff = %+v, %v", outcome, err)
	}
	if len(b.pins.calls) != 0 {
		t.Errorf("a refusal touched the pin: %+v", b.pins.calls)
	}
	b.core.answer(http.StatusOK, sealedStatus)
	for name, call := range map[string]func() (ProofOutcome, error){
		"unlock":      func() (ProofOutcome, error) { return b.raw.UnlockRaw(ctx, biometricArg) },
		"acknowledge": func() (ProofOutcome, error) { return b.raw.AcknowledgeRawKey(ctx, biometricArg) },
		"password":    func() (ProofOutcome, error) { return b.raw.SetRawPassword(ctx, "reset", nil, &biometricArg) },
		"enroll":      func() (ProofOutcome, error) { return b.raw.EnrollRawBiometric(ctx, "pw") },
	} {
		if outcome, err := call(); err != nil || outcome.Outcome != OutcomeBiometricUnavailable {
			t.Errorf("%s = %+v, %v", name, outcome, err)
		}
	}
	if _, err := b.raw.SetRawPassword(ctx, "rotate", new("pw"), nil); err == nil {
		t.Error("an unknown password action was accepted")
	}
	if len(b.core.requests) != 0 {
		t.Errorf("sent %v", b.core.requests)
	}
	b.core.directory = ""
	if _, err := b.raw.EnrollRawBiometric(ctx, "pw"); err == nil || err.Error() != "AstrLink is not running" {
		t.Errorf("enroll before Core = %v", err)
	}
}

func TestRemoveRawBiometricKeepsThePin(t *testing.T) {
	b := newRawBridge(t)
	b.core.answer(http.StatusOK, sealedStatus)
	status, err := b.raw.RemoveRawBiometric(t.Context())
	if err != nil || !sameJSON(t, status, `{"raw_available":true,"configured":true,"unlocked":false,"biometric_available":false}`) {
		t.Fatalf("remove = %s, %v", status, err)
	}
	if !reflect.DeepEqual(b.core.requests, []string{"DELETE " + control.RawBiometricPath}) || len(b.pins.calls) != 0 || b.changes != 1 {
		t.Errorf("requests %v, pin calls %+v, changes %d", b.core.requests, b.pins.calls, b.changes)
	}
}

func TestCheckKeyTellsWindowsOnlyWhenTheVerdictMoves(t *testing.T) {
	b := newRawBridge(t)
	b.core.answer(http.StatusOK, sealedStatus)
	CheckKey(t.Context(), b.raw)
	if b.changes != 0 || len(b.pins.calls) != 1 {
		t.Errorf("unchanged verdict: changes %d, pin calls %+v", b.changes, b.pins.calls)
	}
	b.core.moved = true
	CheckKey(t.Context(), b.raw)
	if b.changes != 1 {
		t.Errorf("moved verdict: changes %d", b.changes)
	}
	b.core.answer(http.StatusServiceUnavailable, `{}`)
	CheckKey(t.Context(), b.raw)
	if b.changes != 1 || len(b.core.replaced) != 0 {
		t.Errorf("a failed read changed the verdict: %v", b.core.replaced)
	}
}
