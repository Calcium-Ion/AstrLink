// Package rawseal seals the per-part keys of raw audit content to an X25519
// public key whose private half exists only inside an envelope wrapped under
// a key derived from the raw password (plan §5.11.9).
//
//	k_blob ─HPKE(pk, info=request‖direction)─▶ wrapped_key (80 B)
//	password ─Argon2id─▶ K_wrap ─AES-256-GCM─▶ private key ("password")
//	keychain key ─HKDF─▶ K_wrap ─AES-256-GCM─▶ private key ("biometric")
//
// The biometric envelope's key is 32 random bytes the desktop keeps in the
// keychain and releases only after Touch ID; Core never stores it.
//
// Capture needs only the public key, so the request path never runs a KDF.
// Go cannot zero the copies crypto/ecdh keeps internally; every byte slice
// this package returns belongs to the caller, which clears it.
package rawseal

import (
	"bytes"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const (
	// PublicKeyBytes and PrivateKeyBytes are X25519 key sizes.
	PublicKeyBytes  = 32
	PrivateKeyBytes = 32
	// BlobKeyBytes is the size of one part's random content key.
	BlobKeyBytes = storage.AuditKeyBytes
	// WrappedBlobKeyBytes is the HPKE encapsulated key plus the sealed
	// part key and its tag.
	WrappedBlobKeyBytes = 32 + BlobKeyBytes + 16
	// SaltBytes is the Argon2id salt size.
	SaltBytes = 16

	// MinPasswordRunes and MaxPasswordRunes bound the raw password (D16);
	// RecommendedPasswordRunes is only a hint.
	MinPasswordRunes         = 8
	RecommendedPasswordRunes = 12
	MaxPasswordRunes         = 128

	// KindPassword names the private key envelope the raw password opens.
	KindPassword = "password"
	// KindBiometric names the envelope a key the desktop releases after
	// Touch ID opens.
	KindBiometric = "biometric"
	// BiometricKeyBytes is the size of that key.
	BiometricKeyBytes = 32
	// BiometricKDFJSON fills the kdf_json column of a biometric envelope:
	// its key is random, so HKDF only binds it to the salt.
	BiometricKDFJSON = `{"alg":"hkdf-sha256"}`

	blobKeyInfoPrefix  = "astrlink/raw-key/v1"
	passwordAADPrefix  = "raw-envelope/password"
	biometricAADPrefix = "raw-envelope/biometric"
	biometricKeyInfo   = "astrlink/raw-envelope/biometric/v1"
	publicKeyMACLabel  = "raw-sealing/public-key"
	argon2idVersion    = argon2.Version
)

var (
	// ErrPasswordPolicy means a new password is too short, too long, or not
	// UTF-8 text.
	ErrPasswordPolicy = errors.New("raw password does not meet the length policy")
	// ErrPassword means the password did not open the password envelope.
	ErrPassword = errors.New("raw password is incorrect")
	// ErrBiometric means the key offered did not open the biometric
	// envelope.
	ErrBiometric = errors.New("biometric key is incorrect")
	// ErrEnvelope means a private key envelope is malformed or does not
	// open with the key offered.
	ErrEnvelope = errors.New("raw key envelope cannot be opened")
	// ErrBlobKey means a wrapped part key does not open under this private
	// key or was stored for another part.
	ErrBlobKey = errors.New("raw part key cannot be opened")
	// ErrKeyMismatch means a private key does not derive the stored public
	// key.
	ErrKeyMismatch = errors.New("raw private key does not match its public key")
)

// KDFParams are the Argon2id parameters stored beside a password envelope.
type KDFParams struct {
	Algorithm string `json:"alg"`
	Version   int    `json:"v"`
	Time      uint32 `json:"t"`
	MemoryKiB uint32 `json:"m"`
	Threads   uint8  `json:"p"`
}

// DefaultKDF is RFC 9106's second recommended Argon2id setting (D16).
var DefaultKDF = KDFParams{Algorithm: "argon2id", Version: argon2idVersion, Time: 3, MemoryKiB: 64 * 1024, Threads: 4}

// Bounds on stored Argon2id parameters: four times DefaultKDF's memory and
// under three times its passes, so a stronger setting still fits.
const (
	maxKDFTime      = 8
	maxKDFMemoryKiB = 256 * 1024
)

// Validate bounds stored parameters so a damaged or planted row cannot make
// one unlock allocate more than 256 MiB or run more than eight passes.
func (params KDFParams) Validate() error {
	if params.Algorithm != "argon2id" || params.Version != argon2idVersion ||
		params.Time < 1 || params.Time > maxKDFTime ||
		params.Threads < 1 || params.Threads > 16 ||
		params.MemoryKiB < 8*uint32(params.Threads) || params.MemoryKiB > maxKDFMemoryKiB {
		return fmt.Errorf("%w: unsupported kdf parameters", ErrEnvelope)
	}
	return nil
}

// PasswordEnvelope is the stored form of the password-wrapped private key.
type PasswordEnvelope struct {
	KDF     KDFParams
	Salt    []byte
	Nonce   []byte
	Wrapped []byte
}

// KDFJSON encodes the parameters for the kdf_json column.
func (envelope PasswordEnvelope) KDFJSON() (string, error) {
	encoded, err := json.Marshal(envelope.KDF)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// ParseKDFJSON decodes and validates a kdf_json column.
func ParseKDFJSON(text string) (KDFParams, error) {
	var params KDFParams
	decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return KDFParams{}, fmt.Errorf("%w: kdf parameters", ErrEnvelope)
	}
	if err := params.Validate(); err != nil {
		return KDFParams{}, err
	}
	return params, nil
}

// ValidatePassword applies the length policy to a new password. Length is
// counted in characters, as NIST SP 800-63B does.
func ValidatePassword(password []byte) error {
	if !utf8.Valid(password) {
		return fmt.Errorf("%w: not UTF-8 text", ErrPasswordPolicy)
	}
	runes := utf8.RuneCount(password)
	if runes < MinPasswordRunes || runes > MaxPasswordRunes {
		return fmt.Errorf("%w: %d to %d characters", ErrPasswordPolicy, MinPasswordRunes, MaxPasswordRunes)
	}
	return nil
}

// GenerateKeyPair returns a fresh X25519 key pair.
func GenerateKeyPair() (private, public []byte, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate raw sealing key: %w", err)
	}
	return key.Bytes(), key.PublicKey().Bytes(), nil
}

// DerivePublic returns the public key of private.
func DerivePublic(private []byte) ([]byte, error) {
	key, err := ecdh.X25519().NewPrivateKey(private)
	if err != nil {
		return nil, fmt.Errorf("%w: private key", ErrEnvelope)
	}
	return key.PublicKey().Bytes(), nil
}

// CheckPair reports ErrKeyMismatch unless private derives public.
func CheckPair(private, public []byte) error {
	derived, err := DerivePublic(private)
	if err != nil {
		return err
	}
	if !hmac.Equal(derived, public) {
		return ErrKeyMismatch
	}
	return nil
}

// PublicKeyMAC authenticates the stored public key under the audit key, so
// a casually replaced public key is noticed at start (§5.11.9.2).
func PublicKeyMAC(auditKey, public []byte) []byte {
	mac := hmac.New(sha256.New, auditKey)
	_, _ = mac.Write([]byte(publicKeyMACLabel))
	_, _ = mac.Write(public)
	return mac.Sum(nil)
}

// BlobKeyInfo binds a wrapped part key to its request and direction, so a
// wrapped key copied onto another part does not open.
func BlobKeyInfo(requestID, direction string) []byte {
	info := make([]byte, 0, len(blobKeyInfoPrefix)+len(requestID)+len(direction)+1)
	info = append(info, blobKeyInfoPrefix...)
	info = append(info, requestID...)
	info = append(info, 0)
	return append(info, direction...)
}

// NewBlobKey returns a random part key.
func NewBlobKey() ([]byte, error) {
	key := make([]byte, BlobKeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate raw part key: %w", err)
	}
	return key, nil
}

func suite() (hpke.KEM, hpke.KDF, hpke.AEAD) {
	return hpke.DHKEM(ecdh.X25519()), hpke.HKDFSHA256(), hpke.AES256GCM()
}

// SealBlobKey wraps a part key to the public key (single-shot HPKE base
// mode: DHKEM(X25519, HKDF-SHA256), HKDF-SHA256, AES-256-GCM).
func SealBlobKey(public, info, blobKey []byte) ([]byte, error) {
	if len(public) != PublicKeyBytes || len(blobKey) != BlobKeyBytes {
		return nil, fmt.Errorf("%w: raw part key fields", storage.ErrInvalidArgument)
	}
	kem, kdf, aead := suite()
	recipient, err := kem.NewPublicKey(public)
	if err != nil {
		return nil, fmt.Errorf("%w: raw sealing public key", storage.ErrInvalidArgument)
	}
	wrapped, err := hpke.Seal(recipient, kdf, aead, info, blobKey)
	if err != nil {
		return nil, fmt.Errorf("seal raw part key: %w", err)
	}
	return wrapped, nil
}

// OpenBlobKey reverses SealBlobKey.
func OpenBlobKey(private, info, wrapped []byte) ([]byte, error) {
	if len(private) != PrivateKeyBytes || len(wrapped) != WrappedBlobKeyBytes {
		return nil, fmt.Errorf("%w: length", ErrBlobKey)
	}
	kem, kdf, aead := suite()
	key, err := kem.NewPrivateKey(private)
	if err != nil {
		return nil, fmt.Errorf("%w: private key", ErrBlobKey)
	}
	blobKey, err := hpke.Open(key, kdf, aead, info, wrapped)
	if err != nil {
		return nil, ErrBlobKey
	}
	if len(blobKey) != BlobKeyBytes {
		clear(blobKey)
		return nil, fmt.Errorf("%w: length", ErrBlobKey)
	}
	return blobKey, nil
}

func envelopeAAD(prefix string, keyID int64, public []byte) []byte {
	aad := make([]byte, 0, len(prefix)+8+len(public))
	aad = append(aad, prefix...)
	aad = binary.BigEndian.AppendUint64(aad, uint64(keyID))
	return append(aad, public...)
}

func deriveWrapKey(password, salt []byte, params KDFParams) []byte {
	return argon2.IDKey(password, salt, params.Time, params.MemoryKiB, params.Threads, storage.AuditKeyBytes)
}

// WrapPassword wraps the private key under a key derived from password.
// The GCM tag doubles as the password check; no verifier is stored.
func WrapPassword(private, password []byte, params KDFParams, keyID int64, public []byte) (PasswordEnvelope, error) {
	if len(private) != PrivateKeyBytes || len(public) != PublicKeyBytes {
		return PasswordEnvelope{}, fmt.Errorf("%w: raw key envelope fields", storage.ErrInvalidArgument)
	}
	if err := ValidatePassword(password); err != nil {
		return PasswordEnvelope{}, err
	}
	if err := params.Validate(); err != nil {
		return PasswordEnvelope{}, err
	}
	salt := make([]byte, SaltBytes)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return PasswordEnvelope{}, fmt.Errorf("generate raw password salt: %w", err)
	}
	wrapKey := deriveWrapKey(password, salt, params)
	defer clear(wrapKey)
	nonce, wrapped, err := storage.SealAES256GCM(wrapKey, private, envelopeAAD(passwordAADPrefix, keyID, public))
	if err != nil {
		return PasswordEnvelope{}, err
	}
	return PasswordEnvelope{KDF: params, Salt: salt, Nonce: nonce, Wrapped: wrapped}, nil
}

// UnwrapPassword opens a password envelope. A wrong password reports
// ErrPassword; the returned key is checked against public.
func UnwrapPassword(envelope PasswordEnvelope, password []byte, keyID int64, public []byte) ([]byte, error) {
	if err := envelope.KDF.Validate(); err != nil {
		return nil, err
	}
	if len(envelope.Salt) != SaltBytes || len(public) != PublicKeyBytes {
		return nil, fmt.Errorf("%w: password envelope fields", ErrEnvelope)
	}
	if len(password) == 0 || len(password) > 4*MaxPasswordRunes {
		return nil, ErrPassword
	}
	wrapKey := deriveWrapKey(password, envelope.Salt, envelope.KDF)
	defer clear(wrapKey)
	private, err := storage.OpenAES256GCM(wrapKey, envelope.Nonce, envelope.Wrapped, envelopeAAD(passwordAADPrefix, keyID, public))
	if err != nil {
		return nil, ErrPassword
	}
	return checkedPrivate(private, public)
}

func checkedPrivate(private, public []byte) ([]byte, error) {
	if len(private) != PrivateKeyBytes {
		clear(private)
		return nil, fmt.Errorf("%w: private key length", ErrEnvelope)
	}
	if err := CheckPair(private, public); err != nil {
		clear(private)
		return nil, err
	}
	return private, nil
}

// BiometricEnvelope is the stored form of the private key wrapped under the
// key the desktop keeps behind Touch ID.
type BiometricEnvelope struct {
	Salt    []byte
	Nonce   []byte
	Wrapped []byte
}

func deriveBiometricWrapKey(key, salt []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, key, salt, biometricKeyInfo, storage.AuditKeyBytes)
}

// WrapBiometric wraps the private key under key, which must be
// BiometricKeyBytes of random data the caller keeps elsewhere.
func WrapBiometric(private, key []byte, keyID int64, public []byte) (BiometricEnvelope, error) {
	if len(private) != PrivateKeyBytes || len(public) != PublicKeyBytes || len(key) != BiometricKeyBytes {
		return BiometricEnvelope{}, fmt.Errorf("%w: biometric envelope fields", storage.ErrInvalidArgument)
	}
	salt := make([]byte, SaltBytes)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return BiometricEnvelope{}, fmt.Errorf("generate biometric envelope salt: %w", err)
	}
	wrapKey, err := deriveBiometricWrapKey(key, salt)
	if err != nil {
		return BiometricEnvelope{}, err
	}
	defer clear(wrapKey)
	nonce, wrapped, err := storage.SealAES256GCM(wrapKey, private, envelopeAAD(biometricAADPrefix, keyID, public))
	if err != nil {
		return BiometricEnvelope{}, err
	}
	return BiometricEnvelope{Salt: salt, Nonce: nonce, Wrapped: wrapped}, nil
}

// UnwrapBiometric opens a biometric envelope. A wrong key reports
// ErrBiometric; the returned key is checked against public.
func UnwrapBiometric(envelope BiometricEnvelope, key []byte, keyID int64, public []byte) ([]byte, error) {
	if len(envelope.Salt) != SaltBytes || len(public) != PublicKeyBytes {
		return nil, fmt.Errorf("%w: biometric envelope fields", ErrEnvelope)
	}
	if len(key) != BiometricKeyBytes {
		return nil, ErrBiometric
	}
	wrapKey, err := deriveBiometricWrapKey(key, envelope.Salt)
	if err != nil {
		return nil, err
	}
	defer clear(wrapKey)
	private, err := storage.OpenAES256GCM(wrapKey, envelope.Nonce, envelope.Wrapped, envelopeAAD(biometricAADPrefix, keyID, public))
	if err != nil {
		return nil, ErrBiometric
	}
	return checkedPrivate(private, public)
}
