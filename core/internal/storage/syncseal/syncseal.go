// Package syncseal seals a configuration package under a key derived from a
// sync passphrase the user sets in the sync settings, so the encrypted package
// can be stored on a remote (WebDAV/S3) that is not trusted.
//
//	passphrase ─Argon2id─▶ K_sync ─AES-256-GCM─▶ package (version‖kdf‖salt‖nonce‖ciphertext)
//
// It is deliberately a separate domain from rawseal: the additional data
// prefix differs, so a ciphertext produced here never opens under the raw
// sealing key and the reverse, and cracking one derived key does not yield the
// other. Both packages derive keys with the same audited Argon2id parameters
// (rawseal.DefaultKDF) so hardening stays in one place.
//
// This package is symmetric: it never generates or stores a key pair. The
// derived key is cached by the caller sealed under the local key hierarchy, so
// automatic sync survives a Core restart without keeping the passphrase, and a
// copy of the database alone yields nothing.
package syncseal

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/rawseal"
)

const (
	// Version is the on-disk package format this build writes and reads.
	Version = 1

	// SaltBytes is the Argon2id salt size, matching rawseal.
	SaltBytes = rawseal.SaltBytes

	// MinPassphraseRunes and MaxPassphraseRunes bound a sync passphrase. The
	// minimum is the recommended raw length rather than the raw minimum: the
	// sync passphrase protects a package that leaves the machine, so it starts
	// where rawseal's advice ends.
	MinPassphraseRunes = rawseal.RecommendedPasswordRunes
	MaxPassphraseRunes = rawseal.MaxPasswordRunes

	// payloadAADPrefix is the additional-data prefix that separates this
	// domain from rawseal's passwordAADPrefix.
	payloadAADPrefix = "astrlink/sync-payload/v1"
	// saltPrefix separates the salt derivation from every other use of the
	// passphrase.
	saltPrefix = "astrlink/sync-salt/v1"
)

var (
	// ErrPassphrasePolicy means a new passphrase is too short, too long, or
	// not UTF-8 text.
	ErrPassphrasePolicy = errors.New("sync passphrase does not meet the length policy")
	// ErrPassphrase means the passphrase did not open the package.
	ErrPassphrase = errors.New("sync passphrase is incorrect")
	// ErrPackage means a package is malformed or its version is unsupported.
	ErrPackage = errors.New("sync package is not readable")
)

// KDFParams names Argon2id parameters beside a package. It is rawseal's type so
// the hardening bounds and the JSON shape stay shared.
type KDFParams = rawseal.KDFParams

// DefaultKDF is the Argon2id setting every new package is written with.
var DefaultKDF = rawseal.DefaultKDF

// Package is the sealed form of one configuration payload.
type Package struct {
	Version    int
	KDF        KDFParams
	Salt       []byte
	Nonce      []byte
	Ciphertext []byte
}

type packageJSON struct {
	Version    int `json:"version"`
	KDF        KDFParams
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// ValidatePassphrase applies the length policy to a new sync passphrase.
// Length is counted in characters, as NIST SP 800-63B does.
func ValidatePassphrase(passphrase []byte) error {
	if !utf8.Valid(passphrase) {
		return fmt.Errorf("%w: not UTF-8 text", ErrPassphrasePolicy)
	}
	runes := utf8.RuneCount(passphrase)
	if runes < MinPassphraseRunes || runes > MaxPassphraseRunes {
		return fmt.Errorf("%w: %d to %d characters", ErrPassphrasePolicy, MinPassphraseRunes, MaxPassphraseRunes)
	}
	return nil
}

// DeriveKey returns the AES-256-GCM key a passphrase produces under salt and
// params. The caller owns the key and clears it. Open calls it internally;
// callers use it to cache the key sealed under the local key hierarchy.
func DeriveKey(passphrase, salt []byte, params KDFParams) ([]byte, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if len(salt) != SaltBytes {
		return nil, fmt.Errorf("%w: salt length", ErrPackage)
	}
	return argon2.IDKey(passphrase, salt, params.Time, params.MemoryKiB, params.Threads, storage.AuditKeyBytes), nil
}

// PassphraseSalt derives the Argon2id salt a passphrase uses on every device.
//
// The passphrase is the only shared secret two devices have, so the salt has to
// come from it: a random salt would make each device derive a different key and
// a device that already synced could not open the package a new one wrote.
// There is one package per remote, so a per-remote salt is not needed, and this
// keeps a device that joins an existing remote from needing anything beyond the
// passphrase. It is a domain-separated hash of the passphrase, not a password
// hash: Argon2id then hardens it.
func PassphraseSalt(passphrase []byte) []byte {
	sum := sha256.Sum256(append([]byte(saltPrefix+"\x00"), passphrase...))
	return sum[:SaltBytes]
}

// NewSalt returns a fresh random salt for a new package.
func NewSalt() ([]byte, error) {
	salt := make([]byte, SaltBytes)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate sync salt: %w", err)
	}
	return salt, nil
}

// Seal encrypts plaintext under a key derived from passphrase. The salt is
// derived from the passphrase itself, so every device that knows the passphrase
// derives the same key. The GCM tag doubles as the passphrase check; no
// verifier is stored.
func Seal(passphrase, plaintext []byte, params KDFParams) (Package, error) {
	if err := ValidatePassphrase(passphrase); err != nil {
		return Package{}, err
	}
	if err := params.Validate(); err != nil {
		return Package{}, err
	}
	salt := PassphraseSalt(passphrase)
	key, err := DeriveKey(passphrase, salt, params)
	if err != nil {
		return Package{}, err
	}
	defer clear(key)
	return SealWithKey(key, plaintext, salt, params)
}

// SealWithKey encrypts plaintext under an already-derived key. It is the form
// the engine uses once the key is cached, so the passphrase is not needed on
// every sync.
func SealWithKey(key, plaintext, salt []byte, params KDFParams) (Package, error) {
	if len(key) != storage.AuditKeyBytes {
		return Package{}, fmt.Errorf("%w: key length", storage.ErrInvalidArgument)
	}
	if len(salt) != SaltBytes {
		return Package{}, fmt.Errorf("%w: salt length", ErrPackage)
	}
	if err := params.Validate(); err != nil {
		return Package{}, err
	}
	nonce, ciphertext, err := storage.SealAES256GCM(key, plaintext, payloadAAD(Version))
	if err != nil {
		return Package{}, err
	}
	return Package{Version: Version, KDF: params, Salt: append([]byte(nil), salt...), Nonce: nonce, Ciphertext: ciphertext}, nil
}

// Open decrypts a package with passphrase. A wrong passphrase reports
// ErrPassphrase.
func Open(pkg Package, passphrase []byte) ([]byte, error) {
	if err := ValidatePassphrase(passphrase); err != nil {
		// Still report a policy error, but do not leak the length of an
		// unrelated passphrase into the package-open path.
		return nil, ErrPassphrase
	}
	key, err := DeriveKey(passphrase, pkg.Salt, pkg.KDF)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	return OpenWithKey(pkg, key)
}

// OpenWithKey decrypts a package with an already-derived key. The key must be
// the one this package's salt and parameters produced.
func OpenWithKey(pkg Package, key []byte) ([]byte, error) {
	if len(key) != storage.AuditKeyBytes {
		return nil, fmt.Errorf("%w: key length", storage.ErrInvalidArgument)
	}
	if err := validate(pkg); err != nil {
		return nil, err
	}
	plaintext, err := storage.OpenAES256GCM(key, pkg.Nonce, pkg.Ciphertext, payloadAAD(pkg.Version))
	if err != nil {
		return nil, ErrPassphrase
	}
	return plaintext, nil
}

// KeyMatches reports whether key opens pkg, without keeping the plaintext. It
// is how a cached key is checked against a package before it is trusted.
func KeyMatches(pkg Package, key []byte) bool {
	plaintext, err := OpenWithKey(pkg, key)
	if err != nil {
		return false
	}
	clear(plaintext)
	return true
}

func validate(pkg Package) error {
	if pkg.Version != Version {
		return fmt.Errorf("%w: unsupported version %d", ErrPackage, pkg.Version)
	}
	if err := pkg.KDF.Validate(); err != nil {
		return err
	}
	if len(pkg.Salt) != SaltBytes || len(pkg.Nonce) != storage.AuditNonceBytes || len(pkg.Ciphertext) < 16 {
		return fmt.Errorf("%w: field lengths", ErrPackage)
	}
	return nil
}

func payloadAAD(version int) []byte {
	aad := make([]byte, 0, len(payloadAADPrefix)+1)
	aad = append(aad, payloadAADPrefix...)
	return append(aad, byte(version))
}

// Marshal encodes a package for storage. Only the plaintext header is visible.
func Marshal(pkg Package) ([]byte, error) {
	if err := validate(pkg); err != nil {
		return nil, err
	}
	return json.Marshal(packageJSON{
		Version:    pkg.Version,
		KDF:        pkg.KDF,
		Salt:       base64.StdEncoding.EncodeToString(pkg.Salt),
		Nonce:      base64.StdEncoding.EncodeToString(pkg.Nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(pkg.Ciphertext),
	})
}

// Parse decodes a package written by Marshal. Unknown fields are refused so a
// future format cannot be read with this build's rules.
func Parse(encoded []byte) (Package, error) {
	var raw packageJSON
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return Package{}, fmt.Errorf("%w: json", ErrPackage)
	}
	salt, err := base64.StdEncoding.DecodeString(raw.Salt)
	if err != nil {
		return Package{}, fmt.Errorf("%w: salt", ErrPackage)
	}
	nonce, err := base64.StdEncoding.DecodeString(raw.Nonce)
	if err != nil {
		return Package{}, fmt.Errorf("%w: nonce", ErrPackage)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(raw.Ciphertext)
	if err != nil {
		return Package{}, fmt.Errorf("%w: ciphertext", ErrPackage)
	}
	pkg := Package{Version: raw.Version, KDF: raw.KDF, Salt: salt, Nonce: nonce, Ciphertext: ciphertext}
	if err := validate(pkg); err != nil {
		return Package{}, err
	}
	return pkg, nil
}
