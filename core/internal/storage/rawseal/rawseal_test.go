package rawseal

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// testKDF keeps tests fast; DefaultKDF is exercised once below.
var testKDF = KDFParams{Algorithm: "argon2id", Version: argon2idVersion, Time: 1, MemoryKiB: 64, Threads: 1}

func keyPair(t *testing.T) (private, public []byte) {
	t.Helper()
	private, public, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if len(private) != PrivateKeyBytes || len(public) != PublicKeyBytes {
		t.Fatalf("key sizes %d/%d", len(private), len(public))
	}
	return private, public
}

func TestBlobKeyRoundTripIsBoundToItsPart(t *testing.T) {
	private, public := keyPair(t)
	blobKey, err := NewBlobKey()
	if err != nil {
		t.Fatal(err)
	}
	info := BlobKeyInfo("request_1", "request")
	wrapped, err := SealBlobKey(public, info, blobKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(wrapped) != WrappedBlobKeyBytes {
		t.Fatalf("wrapped key is %d bytes, want %d", len(wrapped), WrappedBlobKeyBytes)
	}
	opened, err := OpenBlobKey(private, info, wrapped)
	if err != nil || !bytes.Equal(opened, blobKey) {
		t.Fatalf("OpenBlobKey: %v", err)
	}
	for _, other := range [][]byte{
		BlobKeyInfo("request_1", "upstream_request"),
		BlobKeyInfo("request_2", "request"),
		BlobKeyInfo("request_1r", "equest"),
	} {
		if _, err := OpenBlobKey(private, other, wrapped); !errors.Is(err, ErrBlobKey) {
			t.Fatalf("a wrapped key opened for another part (%q): %v", other, err)
		}
	}
	otherPrivate, _ := keyPair(t)
	if _, err := OpenBlobKey(otherPrivate, info, wrapped); !errors.Is(err, ErrBlobKey) {
		t.Fatalf("another private key opened the part key: %v", err)
	}
	tampered := append([]byte(nil), wrapped...)
	tampered[len(tampered)-1] ^= 1
	if _, err := OpenBlobKey(private, info, tampered); !errors.Is(err, ErrBlobKey) {
		t.Fatalf("a tampered part key opened: %v", err)
	}
	second, err := SealBlobKey(public, info, blobKey)
	if err != nil || bytes.Equal(second, wrapped) {
		t.Fatal("sealing is not randomized")
	}
}

func TestPasswordEnvelopeOpensOnlyWithItsPasswordAndBinding(t *testing.T) {
	private, public := keyPair(t)
	password := []byte("correct horse battery")
	envelope, err := WrapPassword(private, password, testKDF, 7, public)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.Salt) != SaltBytes || bytes.Contains(envelope.Wrapped, private) {
		t.Fatal("envelope is malformed or holds the key in the clear")
	}
	opened, err := UnwrapPassword(envelope, password, 7, public)
	if err != nil || !bytes.Equal(opened, private) {
		t.Fatalf("UnwrapPassword: %v", err)
	}
	if _, err := UnwrapPassword(envelope, []byte("wrong horse battery"), 7, public); !errors.Is(err, ErrPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := UnwrapPassword(envelope, password, 8, public); !errors.Is(err, ErrPassword) {
		t.Fatalf("envelope opened for another key id: %v", err)
	}
	_, otherPublic := keyPair(t)
	if _, err := UnwrapPassword(envelope, password, 7, otherPublic); !errors.Is(err, ErrPassword) {
		t.Fatalf("envelope opened for another public key: %v", err)
	}
	again, err := WrapPassword(private, password, testKDF, 7, public)
	if err != nil || bytes.Equal(again.Salt, envelope.Salt) || bytes.Equal(again.Wrapped, envelope.Wrapped) {
		t.Fatal("password wrapping reuses its salt")
	}
}

func TestPasswordPolicyCountsCharacters(t *testing.T) {
	for _, testCase := range []struct {
		password string
		ok       bool
	}{
		{"1234567", false},
		{"12345678", true},
		{"密码密码密码密码", true},
		{"密码密码密码密", false},
		{strings.Repeat("a", MaxPasswordRunes), true},
		{strings.Repeat("a", MaxPasswordRunes+1), false},
		{"\xff\xfe12345678", false},
	} {
		err := ValidatePassword([]byte(testCase.password))
		if (err == nil) != testCase.ok {
			t.Fatalf("ValidatePassword(%q) = %v", testCase.password, err)
		}
		if err != nil && !errors.Is(err, ErrPasswordPolicy) {
			t.Fatalf("policy error does not wrap ErrPasswordPolicy: %v", err)
		}
	}
	private, public := keyPair(t)
	if _, err := WrapPassword(private, []byte("short"), testKDF, 1, public); !errors.Is(err, ErrPasswordPolicy) {
		t.Fatalf("WrapPassword accepted a short password: %v", err)
	}
}

func TestDefaultKDFIsRFC9106SecondRecommendation(t *testing.T) {
	if DefaultKDF.Time != 3 || DefaultKDF.MemoryKiB != 65536 || DefaultKDF.Threads != 4 || DefaultKDF.Validate() != nil {
		t.Fatalf("DefaultKDF = %+v", DefaultKDF)
	}
	private, public := keyPair(t)
	envelope, err := WrapPassword(private, []byte("a long enough phrase"), DefaultKDF, 1, public)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := envelope.KDFJSON()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseKDFJSON(encoded)
	if err != nil || parsed != DefaultKDF {
		t.Fatalf("kdf_json round trip = %+v, %v", parsed, err)
	}
	envelope.KDF = parsed
	if _, err := UnwrapPassword(envelope, []byte("a long enough phrase"), 1, public); err != nil {
		t.Fatal(err)
	}
}

func TestStoredKDFParametersAreBounded(t *testing.T) {
	for _, text := range []string{
		`{"alg":"argon2id","v":19,"t":3,"m":4194304,"p":4}`,
		`{"alg":"argon2id","v":19,"t":3,"m":262145,"p":4}`,
		`{"alg":"argon2id","v":19,"t":3,"m":1048576,"p":4}`,
		`{"alg":"argon2id","v":19,"t":9,"m":65536,"p":4}`,
		`{"alg":"argon2id","v":19,"t":100,"m":65536,"p":4}`,
		`{"alg":"argon2i","v":19,"t":3,"m":65536,"p":4}`,
		`{"alg":"argon2id","v":16,"t":3,"m":65536,"p":4}`,
		`{"alg":"argon2id","v":19,"t":3,"m":65536,"p":0}`,
		`{"alg":"argon2id","v":19,"t":3,"m":65536,"p":4,"extra":1}`,
		`not json`,
	} {
		if _, err := ParseKDFJSON(text); !errors.Is(err, ErrEnvelope) {
			t.Fatalf("ParseKDFJSON(%s) = %v", text, err)
		}
	}
	// The edges stay usable for a stronger setting than DefaultKDF.
	for _, text := range []string{
		`{"alg":"argon2id","v":19,"t":8,"m":262144,"p":4}`,
		`{"alg":"argon2id","v":19,"t":1,"m":8,"p":1}`,
	} {
		if _, err := ParseKDFJSON(text); err != nil {
			t.Fatalf("ParseKDFJSON(%s) = %v", text, err)
		}
	}
}

func TestPairChecksAndPublicKeyMAC(t *testing.T) {
	private, public := keyPair(t)
	derived, err := DerivePublic(private)
	if err != nil || !bytes.Equal(derived, public) {
		t.Fatalf("DerivePublic: %v", err)
	}
	_, otherPublic := keyPair(t)
	if err := CheckPair(private, otherPublic); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("CheckPair accepted a foreign public key: %v", err)
	}
	// A matching envelope for a different public key is refused too.
	envelope, err := WrapPassword(private, []byte("12345678"), testKDF, 1, otherPublic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnwrapPassword(envelope, []byte("12345678"), 1, otherPublic); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("a mismatched pair opened: %v", err)
	}
	auditKey := bytes.Repeat([]byte{1}, 32)
	mac := PublicKeyMAC(auditKey, public)
	if len(mac) != 32 || bytes.Equal(mac, PublicKeyMAC(auditKey, otherPublic)) ||
		bytes.Equal(mac, PublicKeyMAC(bytes.Repeat([]byte{2}, 32), public)) {
		t.Fatal("public key MAC does not bind the key and the audit key")
	}
}

func TestBiometricEnvelopeOpensOnlyWithItsKeyForItsKeyPair(t *testing.T) {
	private, public, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x5a}, BiometricKeyBytes)
	const keyID = 41
	envelope, err := WrapBiometric(private, key, keyID, public)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.Salt) != SaltBytes || bytes.Contains(envelope.Wrapped, private) {
		t.Fatalf("envelope=%#v", envelope)
	}
	opened, err := UnwrapBiometric(envelope, key, keyID, public)
	if err != nil || !bytes.Equal(opened, private) {
		t.Fatalf("unwrap = %x, %v", opened, err)
	}
	wrongKey := bytes.Repeat([]byte{0x5b}, BiometricKeyBytes)
	if _, err := UnwrapBiometric(envelope, wrongKey, keyID, public); !errors.Is(err, ErrBiometric) {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := UnwrapBiometric(envelope, key[:16], keyID, public); !errors.Is(err, ErrBiometric) {
		t.Fatalf("short key: %v", err)
	}
	// The envelope is bound to its key pair, like the password one.
	if _, err := UnwrapBiometric(envelope, key, keyID+1, public); !errors.Is(err, ErrBiometric) {
		t.Fatalf("other key id: %v", err)
	}
	_, otherPublic, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnwrapBiometric(envelope, key, keyID, otherPublic); !errors.Is(err, ErrBiometric) {
		t.Fatalf("other public key: %v", err)
	}
	if _, err := WrapBiometric(private, key[:31], keyID, public); err == nil {
		t.Fatal("a short wrapping key was accepted")
	}
}
