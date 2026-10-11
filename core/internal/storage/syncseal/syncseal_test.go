package syncseal

import (
	"bytes"
	"errors"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/storage/rawseal"
)

func TestSealOpenRoundTrip(t *testing.T) {
	passphrase := []byte("correct horse battery")
	plaintext := []byte(`{"services":[],"policies":[]}`)
	pkg, err := Seal(passphrase, plaintext, DefaultKDF)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if pkg.Version != Version {
		t.Fatalf("version = %d, want %d", pkg.Version, Version)
	}
	if bytes.Contains(pkg.Ciphertext, plaintext) {
		t.Fatal("ciphertext contains plaintext")
	}
	opened, err := Open(pkg, passphrase)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened = %q, want %q", opened, plaintext)
	}
}

func TestOpenWrongPassphrase(t *testing.T) {
	pkg, err := Seal([]byte("correct horse battery"), []byte("payload"), DefaultKDF)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := Open(pkg, []byte("correct horse batteries")); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("wrong passphrase err = %v, want ErrPassphrase", err)
	}
}

func TestPassphrasePolicy(t *testing.T) {
	short := make([]byte, MinPassphraseRunes-1)
	for i := range short {
		short[i] = 'a'
	}
	if err := ValidatePassphrase(short); !errors.Is(err, ErrPassphrasePolicy) {
		t.Fatalf("short err = %v, want ErrPassphrasePolicy", err)
	}
	if err := ValidatePassphrase([]byte("ab\xff")); !errors.Is(err, ErrPassphrasePolicy) {
		t.Fatalf("invalid utf-8 err = %v, want ErrPassphrasePolicy", err)
	}
	ok := make([]byte, MaxPassphraseRunes+1)
	for i := range ok {
		ok[i] = 'a'
	}
	if err := ValidatePassphrase(ok); !errors.Is(err, ErrPassphrasePolicy) {
		t.Fatalf("long err = %v, want ErrPassphrasePolicy", err)
	}
}

// A package sealed here must not open with a raw-sealing envelope and the
// reverse: the two domains derive independent keys and use distinct AAD.
func TestDomainSeparationFromRawSealing(t *testing.T) {
	passphrase := []byte("correct horse battery")
	private, public, err := rawseal.GenerateKeyPair()
	if err != nil {
		t.Fatalf("key pair: %v", err)
	}
	rawEnvelope, err := rawseal.WrapPassword(private, passphrase, rawseal.DefaultKDF, 1, public)
	if err != nil {
		t.Fatalf("wrap password: %v", err)
	}
	// A raw envelope's fields are not a sync package.
	if _, err := OpenWithKey(Package{
		Version:    Version,
		KDF:        rawEnvelope.KDF,
		Salt:       rawEnvelope.Salt,
		Nonce:      rawEnvelope.Nonce,
		Ciphertext: rawEnvelope.Wrapped,
	}, passphraseKey(t, passphrase)); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("raw envelope opened as sync package: %v", err)
	}
	// A package must not open under rawseal either.
	pkg, err := Seal(passphrase, []byte("payload"), DefaultKDF)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := rawseal.UnwrapPassword(rawseal.PasswordEnvelope{
		KDF:     pkg.KDF,
		Salt:    pkg.Salt,
		Nonce:   pkg.Nonce,
		Wrapped: pkg.Ciphertext,
	}, passphrase, 1, public); err == nil {
		t.Fatal("sync package opened as raw envelope")
	}
}

func passphraseKey(t *testing.T, passphrase []byte) []byte {
	t.Helper()
	salt, err := NewSalt()
	if err != nil {
		t.Fatalf("salt: %v", err)
	}
	key, err := DeriveKey(passphrase, salt, DefaultKDF)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	return key
}

func TestMarshalParseRoundTrip(t *testing.T) {
	pkg, err := Seal([]byte("correct horse battery"), []byte("payload"), DefaultKDF)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	encoded, err := Marshal(pkg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := Parse(encoded)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !bytes.Equal(parsed.Ciphertext, pkg.Ciphertext) || !bytes.Equal(parsed.Salt, pkg.Salt) {
		t.Fatal("parsed package differs")
	}
	opened, err := Open(parsed, []byte("correct horse battery"))
	if err != nil || !bytes.Equal(opened, []byte("payload")) {
		t.Fatalf("open parsed: %v %q", err, opened)
	}
}

func TestParseRejectsUnknownFieldAndVersion(t *testing.T) {
	pkg, err := Seal([]byte("correct horse battery"), []byte("payload"), DefaultKDF)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	encoded, err := Marshal(pkg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	extended := append(bytes.TrimSuffix(encoded, []byte("}")), []byte(`,"extra":1}`)...)
	if _, err := Parse(extended); !errors.Is(err, ErrPackage) {
		t.Fatalf("unknown field err = %v, want ErrPackage", err)
	}
	pkg.Version = Version + 1
	if _, err := Marshal(pkg); !errors.Is(err, ErrPackage) {
		t.Fatalf("marshal future version err = %v, want ErrPackage", err)
	}
}

// A cached key is checked against a package without using the passphrase.
func TestKeyMatches(t *testing.T) {
	passphrase := []byte("correct horse battery")
	salt, err := NewSalt()
	if err != nil {
		t.Fatalf("salt: %v", err)
	}
	key, err := DeriveKey(passphrase, salt, DefaultKDF)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	pkg, err := SealWithKey(key, []byte("payload"), salt, DefaultKDF)
	if err != nil {
		t.Fatalf("seal with key: %v", err)
	}
	if !KeyMatches(pkg, key) {
		t.Fatal("cached key did not match its own package")
	}
	other, err := DeriveKey([]byte("another passphrase"), salt, DefaultKDF)
	if err != nil {
		t.Fatalf("derive other: %v", err)
	}
	if KeyMatches(pkg, other) {
		t.Fatal("unrelated key matched the package")
	}
}
