package keystore

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// KeychainService is the keychain service of every local key entry.
const KeychainService = "com.astrlink.desktop.local-key"

const (
	// keyFileName is Core's default key file inside the data directory.
	keyFileName = "local.key"
	// fallbackKeyFileName is the key file Core uses while the keychain
	// cannot be read. It stays apart from local.key so a key made during an
	// outage is never mistaken for one that arrived with a copied directory
	// and moved into the keychain.
	fallbackKeyFileName = "local.fallback.key"
	keyBytes            = 32
	// maxKeyFileBytes bounds a key file read: 64 hex digits plus line
	// endings, as Core allows.
	maxKeyFileBytes = 256
)

// LocalKeyStorage is where Core's local key lives, shown as one status line
// in Settings.
type LocalKeyStorage string

const (
	// StorageKeychain is the login keychain; the desktop injects the key on
	// stdin.
	StorageKeychain LocalKeyStorage = "keychain"
	// StorageFile is a key file Core manages, as on every build that does
	// not use the keychain.
	StorageFile LocalKeyStorage = "file"
	// StorageKeychainUnavailable is a signed build that could not read or
	// write the keychain and fell back to a key file.
	StorageKeychainUnavailable LocalKeyStorage = "keychain_unavailable"
)

// ErrNotFound is what a Vault's Get returns when there is no entry.
var ErrNotFound = errors.New("no keychain entry")

// Vault is the secret storage the key moves through, so tests never touch
// the real keychain.
type Vault interface {
	// Get returns the stored value, or ErrNotFound.
	Get(account string) ([]byte, error)
	// Set creates or replaces the entry.
	Set(account string, value []byte) error
}

// ResolvedKey is how the next Core start receives its local key.
type ResolvedKey struct {
	Storage LocalKeyStorage
	// StdinHex is the key as 64 lowercase hex digits for Core's stdin, or
	// empty.
	StdinHex string
	// KeyFile is an explicit key file for Core; empty keeps Core's default.
	KeyFile string
}

func coreDefault(storage LocalKeyStorage) ResolvedKey { return ResolvedKey{Storage: storage} }

func keychainKey(key []byte) ResolvedKey {
	return ResolvedKey{Storage: StorageKeychain, StdinHex: hex.EncodeToString(key)}
}

// fallbackFile is a Core that keeps the fallback file an outage start gave it.
func fallbackFile(path string) ResolvedKey {
	return ResolvedKey{Storage: StorageKeychainUnavailable, KeyFile: path}
}

var resolving sync.Mutex

// ResolveForStart resolves the key for a Core start. Only signed macOS
// builds use the keychain; resolutions are serialized so two starts never
// both generate one.
func ResolveForStart(dataDirectory string, log func(string)) ResolvedKey {
	resolving.Lock()
	defer resolving.Unlock()
	if runtime.GOOS == "darwin" && KeychainEnabled() {
		return Resolve(systemVault{service: KeychainService}, dataDirectory, log)
	}
	return coreDefault(StorageFile)
}

// KeychainAccount is the keychain account of a data directory: the first 16
// hex digits of the SHA-256 of its absolute path, so several data
// directories on one Mac keep separate entries.
func KeychainAccount(dataDirectory string) string {
	sum := sha256.Sum256([]byte(dataDirectory))
	return hex.EncodeToString(sum[:8])
}

// Resolve applies the key flow against vault. It never deletes or
// overwrites a key that might still open data: a failure at any step leaves
// the keychain and the key files as they were and lets Core use a key file.
func Resolve(vault Vault, dataDirectory string, log func(string)) ResolvedKey {
	account := KeychainAccount(dataDirectory)
	keyFile := filepath.Join(dataDirectory, keyFileName)
	fallback := filepath.Join(dataDirectory, fallbackKeyFileName)
	var stored []byte
	switch value, err := vault.Get(account); {
	case err == nil:
		if stored = parseKey(value); stored == nil {
			log("AstrLink local key: the keychain entry is not a valid key; using a key file")
			return keychainUnavailable(dataDirectory, keyFile)
		}
	case errors.Is(err, ErrNotFound):
	default:
		log(fmt.Sprintf("AstrLink local key: the keychain cannot be read (%v); using a key file", err))
		return keychainUnavailable(dataDirectory, keyFile)
	}
	fileKey, err := readKeyFile(keyFile)
	if err != nil {
		if stored == nil {
			// Core reports the unreadable file; nothing moves.
			log(fmt.Sprintf("AstrLink local key: %s cannot be read (%v); leaving it for Core", keyFileName, err))
			return coreDefault(StorageFile)
		}
		log(fmt.Sprintf("AstrLink local key: ignoring %s (%v); using the keychain", keyFileName, err))
		fileKey = nil
	}

	switch {
	case stored != nil && fileKey == nil:
		settleFallback(stored, fallback, log)
		return keychainKey(stored)
	case stored != nil && bytes.Equal(stored, fileKey):
		// A migration stopped after the keychain write; finish it.
		removeKeyFile(keyFile, log)
		settleFallback(stored, fallback, log)
		return keychainKey(stored)
	case stored != nil:
		// A keychain hit is used as is: a file anyone who can write the data
		// directory could have placed never replaces it. The file stays for
		// the user to recover a copied directory with.
		log(fmt.Sprintf("AstrLink local key: %s differs from the keychain entry; using the keychain and leaving the file", keyFileName))
		settleFallback(stored, fallback, log)
		return keychainKey(stored)
	case fileKey != nil:
		// Core used local.key over the fallback file whenever both were
		// there, so local.key holds the current key.
		resolved := storeFileKey(vault, account, fileKey, keyFile, coreDefault(StorageKeychainUnavailable), log)
		if resolved.Storage == StorageKeychain {
			settleFallback(fileKey, fallback, log)
		}
		return resolved
	}
	switch outage, err := readKeyFile(fallback); {
	case err != nil:
		log(fmt.Sprintf("AstrLink local key: %s cannot be read (%v); leaving it for Core", fallbackKeyFileName, err))
		return fallbackFile(fallback)
	case outage != nil:
		// An outage start left its key in the fallback file and Core wrapped
		// the data keys under it: that key moves in, never a new one that
		// would orphan them.
		return storeFileKey(vault, account, outage, fallback, fallbackFile(fallback), log)
	}
	key := make([]byte, keyBytes)
	if _, err := rand.Read(key); err != nil {
		log(fmt.Sprintf("AstrLink local key: cannot generate a key (%v); Core will create %s", err, keyFileName))
		return coreDefault(StorageKeychainUnavailable)
	}
	if err := writeVerified(vault, account, key); err != nil {
		log(fmt.Sprintf("AstrLink local key: cannot write the keychain (%v); Core will create %s", err, keyFileName))
		return coreDefault(StorageKeychainUnavailable)
	}
	return keychainKey(key)
}

// storeFileKey moves a key file into the keychain and deletes the file once
// the entry reads back. While the keychain refuses, Core keeps ifRefused.
func storeFileKey(vault Vault, account string, key []byte, keyFile string, ifRefused ResolvedKey, log func(string)) ResolvedKey {
	if err := writeVerified(vault, account, key); err != nil {
		log(fmt.Sprintf("AstrLink local key: cannot move %s into the keychain (%v); using the file", filepath.Base(keyFile), err))
		return ifRefused
	}
	removeKeyFile(keyFile, log)
	return keychainKey(key)
}

// settleFallback settles a fallback file beside the key the keychain holds.
// The same key means a move stopped after the keychain write, so the file
// goes; another key never replaces the entry and the file stays for
// recovery.
func settleFallback(key []byte, fallback string, log func(string)) {
	switch outage, err := readKeyFile(fallback); {
	case err != nil:
		log(fmt.Sprintf("AstrLink local key: ignoring %s (%v); using the keychain", fallbackKeyFileName, err))
	case outage == nil:
	case bytes.Equal(outage, key):
		removeKeyFile(fallback, log)
	default:
		log(fmt.Sprintf("AstrLink local key: %s differs from the keychain entry; using the keychain and leaving the file", fallbackKeyFileName))
	}
}

func writeVerified(vault Vault, account string, key []byte) error {
	if err := vault.Set(account, []byte(hex.EncodeToString(key))); err != nil {
		return err
	}
	value, err := vault.Get(account)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if stored := parseKey(value); stored == nil || !bytes.Equal(stored, key) {
		return errors.New("the entry did not read back")
	}
	return nil
}

// keychainUnavailable keeps Core on a key file while the keychain cannot be
// read. An existing local.key is still the best guess; otherwise an
// existing data directory gets the separate fallback file so the outage
// never replaces the entry. The first start that finds no entry moves the
// fallback key in.
func keychainUnavailable(dataDirectory, keyFile string) ResolvedKey {
	resolved := coreDefault(StorageKeychainUnavailable)
	if _, err := os.Stat(keyFile); err != nil {
		if info, err := os.Stat(dataDirectory); err == nil && info.IsDir() {
			resolved.KeyFile = filepath.Join(dataDirectory, fallbackKeyFileName)
		}
	}
	return resolved
}

// readKeyFile returns the key in path, nil when there is no file.
func readKeyFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxKeyFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxKeyFileBytes {
		return nil, errors.New("file too large")
	}
	key := parseKey(content)
	if key == nil {
		return nil, errors.New("not 64 hex digits")
	}
	return key, nil
}

func removeKeyFile(path string, log func(string)) {
	if err := os.Remove(path); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log(fmt.Sprintf("AstrLink local key: cannot delete %s after moving it into the keychain (%v)", filepath.Base(path), err))
		}
		return
	}
	// Windows cannot open a directory as a file to sync it.
	if runtime.GOOS == "windows" {
		return
	}
	directory, err := os.Open(filepath.Dir(path))
	if err == nil {
		err = directory.Sync()
		directory.Close()
	}
	if err != nil {
		log(fmt.Sprintf("AstrLink local key: cannot sync the data directory (%v)", err))
	}
}

// parseKey decodes 64 hex digits, ignoring surrounding whitespace as Core
// does. It returns nil for anything else.
func parseKey(text []byte) []byte {
	text = bytes.TrimSpace(text)
	if len(text) != keyBytes*2 {
		return nil
	}
	key := make([]byte, keyBytes)
	if _, err := hex.Decode(key, text); err != nil {
		return nil
	}
	return key
}
