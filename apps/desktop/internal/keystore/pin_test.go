package keystore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	pinAccount = "0123456789abcdef"
	keyA       = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	keyB       = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type fakePins struct {
	entries  map[string]string
	failLoad bool
}

func (f *fakePins) Load(account string) (string, error) {
	if f.failLoad {
		return "", errors.New("OSStatus -25308")
	}
	return f.entries[account], nil
}

func (f *fakePins) Save(account, pin string) error {
	f.entries[account] = pin
	return nil
}

func newPins() (*RawKeyPins, *fakePins) {
	store := &fakePins{entries: map[string]string{}}
	return NewRawKeyPins(store), store
}

func statusJSON(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func noPasswordStatus(t *testing.T) json.RawMessage {
	return statusJSON(t, map[string]any{"configured": true, "password_set": false, "key_fingerprint": keyA})
}

func keyed(t *testing.T, fingerprint string) json.RawMessage {
	return statusJSON(t, map[string]any{"configured": true, "password_set": true, "key_fingerprint": fingerprint})
}

func check(p *RawKeyPins, status json.RawMessage) bool { return p.Apply(pinAccount, status, PinCheck) }

func TestAKeyThatPredatesPinningIsAdoptedOnce(t *testing.T) {
	p, store := newPins()
	if check(p, keyed(t, keyA)) || store.entries[pinAccount] != keyA || check(p, keyed(t, keyA)) {
		t.Fatal("the first key was not adopted")
	}
	if !check(p, keyed(t, keyB)) {
		t.Fatal("a replaced key passed")
	}
	// The warning stays until the operator acts on it.
	if store.entries[pinAccount] != keyA || !check(p, keyed(t, keyB)) {
		t.Fatal("the warning cleared itself")
	}
}

func TestAPasswordSetOutsideTheDesktopIsAReplacement(t *testing.T) {
	p, store := newPins()
	if check(p, noPasswordStatus(t)) || store.entries[pinAccount] != noPassword {
		t.Fatalf("pin %q", store.entries[pinAccount])
	}
	// The key is the same one; its new password is not the operator's.
	if !check(p, keyed(t, keyA)) {
		t.Fatal("a password set outside passed")
	}
}

func TestDesktopSetAndResetRepinWhileAChangeOnlyChecks(t *testing.T) {
	for action, want := range map[string]PinAction{"set": PinRecord, "reset": PinRecord, "change": PinCheck} {
		if got := PinActionAfterPassword(action); got != want {
			t.Errorf("PinActionAfterPassword(%q) = %v", action, got)
		}
	}
	p, store := newPins()
	steps := []struct {
		status json.RawMessage
		action PinAction
	}{
		{noPasswordStatus(t), PinCheck},
		{keyed(t, keyA), PinActionAfterPassword("set")},
		{keyed(t, keyA), PinCheck},
		{keyed(t, keyA), PinActionAfterPassword("change")},
		{keyed(t, keyB), PinActionAfterPassword("reset")},
		{keyed(t, keyB), PinCheck},
	}
	for i, step := range steps {
		if p.Apply(pinAccount, step.status, step.action) {
			t.Fatalf("step %d reported a replacement", i)
		}
	}
	if store.entries[pinAccount] != keyB {
		t.Fatalf("pin %q", store.entries[pinAccount])
	}
}

func TestAChangeDoesNotClearAReplacement(t *testing.T) {
	p, _ := newPins()
	check(p, keyed(t, keyA))
	if !p.Apply(pinAccount, keyed(t, keyB), PinActionAfterPassword("change")) || !check(p, keyed(t, keyB)) {
		t.Fatal("a change cleared the replacement")
	}
}

func TestAcknowledgingAReplacedKeyRepinsIt(t *testing.T) {
	p, store := newPins()
	check(p, keyed(t, keyA))
	if !check(p, keyed(t, keyB)) || p.Apply(pinAccount, keyed(t, keyB), PinRecord) {
		t.Fatal("unexpected verdict")
	}
	if store.entries[pinAccount] != keyB || check(p, keyed(t, keyB)) {
		t.Fatal("the key was not repinned")
	}
}

func TestAStatusWithoutAPasswordKeepsThePin(t *testing.T) {
	p, store := newPins()
	check(p, keyed(t, keyA))
	if check(p, noPasswordStatus(t)) || store.entries[pinAccount] != keyA || !check(p, keyed(t, keyB)) {
		t.Fatal("a status without a password moved the pin")
	}
}

func TestStatusesWithoutAVerdictLeaveThePinAlone(t *testing.T) {
	p, store := newPins()
	for _, status := range []map[string]any{
		{"configured": true, "password_set": true},
		{"configured": true, "password_set": true, "key_fingerprint": ""},
		{"configured": true, "password_set": true, "key_fingerprint": strings.ToUpper(keyA)},
		{"configured": true, "password_set": true, "key_fingerprint": keyA[1:]},
		{"configured": true, "password_set": "yes"},
		{"configured": true},
	} {
		s := statusJSON(t, status)
		if check(p, s) || p.Apply(pinAccount, s, PinRecord) {
			t.Fatalf("%s gave a verdict", s)
		}
	}
	if _, ok := store.entries[pinAccount]; ok {
		t.Fatal("a status without a verdict was pinned")
	}
}

func TestAnUnreadableOrInvalidPinRaisesNoWarning(t *testing.T) {
	store := &fakePins{entries: map[string]string{}, failLoad: true}
	if check(NewRawKeyPins(store), keyed(t, keyB)) || len(store.entries) != 0 {
		t.Fatal("an unreadable pin warned or was replaced")
	}
	p, valid := newPins()
	valid.entries[pinAccount] = "not-a-pin"
	if check(p, keyed(t, keyB)) || valid.entries[pinAccount] != "not-a-pin" {
		t.Fatal("an invalid pin warned or was replaced")
	}
}

func TestDataDirectoriesKeepSeparatePins(t *testing.T) {
	p, _ := newPins()
	if p.Apply("1111111111111111", keyed(t, keyA), PinCheck) || p.Apply("2222222222222222", keyed(t, keyB), PinCheck) {
		t.Fatal("first keys warned")
	}
	if !p.Apply("1111111111111111", keyed(t, keyB), PinCheck) {
		t.Fatal("the pins were shared")
	}
}

func TestThePinFileIsOwnerOnlyAndKeepsOtherAccounts(t *testing.T) {
	path := PinFile(filepath.Join(t.TempDir(), "config"))
	store := &FilePins{Path: path}
	if pin, err := store.Load(pinAccount); err != nil || pin != "" {
		t.Fatalf("Load = %q, %v", pin, err)
	}
	for _, save := range [][2]string{{pinAccount, keyA}, {"fedcba9876543210", noPassword}, {pinAccount, keyB}} {
		if err := store.Save(save[0], save[1]); err != nil {
			t.Fatal(err)
		}
	}
	if pin, _ := store.Load(pinAccount); pin != keyB {
		t.Fatalf("pin %q", pin)
	}
	if pin, _ := store.Load("fedcba9876543210"); pin != noPassword {
		t.Fatalf("other pin %q", pin)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, %v", info.Mode(), err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 || entries[0].Name() != PinFileName {
		t.Fatalf("leftovers %v", entries)
	}
	p := NewRawKeyPins(&FilePins{Path: path})
	if check(p, keyed(t, keyB)) || !check(p, keyed(t, keyA)) {
		t.Fatal("the file's pin was not used")
	}
}

func TestADamagedPinFileIsNeitherTrustedNorReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), PinFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &FilePins{Path: path}
	if _, err := store.Load(pinAccount); err == nil {
		t.Fatal("a damaged file loaded")
	}
	if err := store.Save(pinAccount, keyA); err == nil {
		t.Fatal("a damaged file was replaced")
	}
	if check(NewRawKeyPins(&FilePins{Path: path}), keyed(t, keyA)) {
		t.Fatal("a damaged file warned")
	}
	if b, _ := os.ReadFile(path); string(b) != "{not json" {
		t.Fatalf("file %q", b)
	}
}
