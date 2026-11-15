package keystore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// Pins of the raw public key a data directory uses, so a raw key replaced
// outside the desktop is noticed. `astrlink-core raw-password` run while the
// app is closed needs no proof to reset the key, and whoever sets a key's
// password can read the raw content captured under it. The pin lives in the
// login keychain on signed macOS builds and in an owner-only file in the
// config directory everywhere else.

const (
	// pinKeychainService is the keychain service of every raw key pin.
	pinKeychainService = "com.astrlink.desktop.raw-key-pin"
	// PinFileName is the pin file in the config directory when the keychain
	// is not used.
	PinFileName = "raw-key-pins.json"
	// maxPinFileBytes bounds a pin file read; each entry is under 100 bytes.
	maxPinFileBytes = 1 << 20
	// noPassword is the pin of a data directory whose raw key has no
	// password yet.
	noPassword = "none"
)

// observed is what a raw sealing status says about the key, in pinned form:
// noPassword, or the fingerprint of a key that has a password. It is ""
// when the status gives no usable verdict, such as from a Core without key
// fingerprints.
func observed(status json.RawMessage) string {
	var s struct {
		PasswordSet    any `json:"password_set"`
		KeyFingerprint any `json:"key_fingerprint"`
	}
	if json.Unmarshal(status, &s) != nil {
		return ""
	}
	set, ok := s.PasswordSet.(bool)
	if !ok {
		return ""
	}
	if !set {
		return noPassword
	}
	if fingerprint, ok := s.KeyFingerprint.(string); ok && isFingerprint(fingerprint) {
		return fingerprint
	}
	return ""
}

func decodePin(pin string) string {
	if pin == noPassword || isFingerprint(pin) {
		return pin
	}
	return ""
}

func isFingerprint(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range []byte(value) {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// PinStore is where pins are kept, so tests never touch the real keychain
// or config.
type PinStore interface {
	// Load returns the pin of account, "" when there is none.
	Load(account string) (string, error)
	// Save creates or replaces the pin of account.
	Save(account, pin string) error
}

// PinFile is the pin file of an instance whose config directory is
// configDirectory and keeps its pins in a file.
func PinFile(configDirectory string) string { return filepath.Join(configDirectory, PinFileName) }

// PinAction is what the desktop does with the status a password action
// returns.
type PinAction int

const (
	// PinCheck compares the status with the pin.
	PinCheck PinAction = iota
	// PinRecord records the key in the status as the pinned one.
	PinRecord
)

// PinActionAfterPassword is the pin action after a password action. A
// desktop set or reset gives the key a password the operator just chose, so
// it becomes the pinned key. A change keeps the key and its fingerprint, so
// it only checks: it must not clear the warning for a key replaced outside
// the desktop, whose old password still opens a copy.
func PinActionAfterPassword(action string) PinAction {
	switch action {
	case "set", "reset":
		return PinRecord
	}
	return PinCheck
}

// RawKeyPins are the pins of every data directory plus the lock that keeps
// a status read and its check, or a password action and its pin, from
// interleaving.
type RawKeyPins struct {
	store PinStore
	mu    sync.Mutex
}

// NewRawKeyPins keeps pins in store.
func NewRawKeyPins(store PinStore) *RawKeyPins { return &RawKeyPins{store: store} }

// SystemRawKeyPins keeps pins in the keychain on signed macOS builds and in
// the config directory elsewhere.
func SystemRawKeyPins(configDirectory string) *RawKeyPins {
	if runtime.GOOS == "darwin" && KeychainEnabled() {
		return NewRawKeyPins(keychainPins{systemVault{service: pinKeychainService}})
	}
	return NewRawKeyPins(&FilePins{Path: PinFile(configDirectory)})
}

// Lock holds the pins for a status read and its check; Unlock releases them.
func (p *RawKeyPins) Lock()   { p.mu.Lock() }
func (p *RawKeyPins) Unlock() { p.mu.Unlock() }

// Apply applies action to the key in status and returns whether it was
// replaced outside the desktop. It blocks on the keychain.
func (p *RawKeyPins) Apply(account string, status json.RawMessage, action PinAction) bool {
	if action == PinRecord {
		if seen := observed(status); seen != "" {
			p.save(account, seen)
		}
		return false
	}
	return p.replaced(account, status)
}

// replaced reports whether the key in status differs from the pinned one.
// The first status seen for a data directory is adopted, so a key that
// predates pinning raises no warning. A status without a password keeps the
// pin: a key that loses its password, as with a wiped database, is not an
// attack, and a password set on it later is still compared.
func (p *RawKeyPins) replaced(account string, status json.RawMessage) bool {
	seen := observed(status)
	if seen == "" {
		return false
	}
	pin, err := p.store.Load(account)
	switch {
	case err != nil:
		log.Printf("unable to read the raw key pin: %v", err)
		return false
	case pin == "":
		p.save(account, seen)
		return false
	}
	pinned := decodePin(pin)
	if pinned == "" {
		log.Printf("ignoring an invalid raw key pin")
		return false
	}
	return pinned != seen && seen != noPassword
}

func (p *RawKeyPins) save(account, pin string) {
	if err := p.store.Save(account, pin); err != nil {
		log.Printf("unable to save the raw key pin: %v", err)
	}
}

type keychainPins struct{ vault systemVault }

func (k keychainPins) Load(account string) (string, error) {
	value, err := k.vault.Get(account)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	return string(value), err
}

func (k keychainPins) Save(account, pin string) error { return k.vault.Set(account, []byte(pin)) }

// FilePins keeps pins as one owner-only JSON object from account to pin.
// Only the desktop reads or writes it.
type FilePins struct{ Path string }

func (f *FilePins) read() (map[string]string, error) {
	file, err := os.Open(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxPinFileBytes))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Path, err)
	}
	// An unreadable file is reported rather than replaced, so a damaged pin
	// never turns into a fresh adoption of whatever key is there.
	pins := map[string]string{}
	if err := json.Unmarshal(data, &pins); err != nil {
		return nil, fmt.Errorf("%s: %w", f.Path, err)
	}
	return pins, nil
}

func (f *FilePins) Load(account string) (string, error) {
	pins, err := f.read()
	return pins[account], err
}

func (f *FilePins) Save(account, pin string) error {
	pins, err := f.read()
	if err != nil {
		return err
	}
	pins[account] = pin
	data, err := json.MarshalIndent(pins, "", "  ")
	if err != nil {
		return err
	}
	if err := prefs.WriteFileAtomic(f.Path, append(data, '\n')); err != nil {
		return fmt.Errorf("%s: %w", f.Path, err)
	}
	return nil
}
