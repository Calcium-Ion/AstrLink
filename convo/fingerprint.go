package convo

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// fingerprintDomain separates fingerprint MACs from any other use of the same
// key material.
const fingerprintDomain = "convo/fingerprint/v1"

// fingerprintHexLength keeps stored fingerprints short; 128 bits is plenty to
// avoid accidental collisions while staying well under MaxCursorRunes.
const fingerprintHexLength = 32

// Fingerprinter turns text digests into keyed, versioned cursor values. A nil
// *Fingerprinter disables the fingerprint layer everywhere it is passed.
type Fingerprinter struct {
	key []byte
}

// NewFingerprinter returns a fingerprinter over a copy of key, or nil when key
// is empty so callers can pass the result straight through.
func NewFingerprinter(key []byte) *Fingerprinter {
	if len(key) == 0 {
		return nil
	}
	return &Fingerprinter{key: append([]byte(nil), key...)}
}

// DeriveFingerprintKey derives a 32-byte fingerprint key from a master secret
// with HKDF-SHA256. info should name the deployment and purpose, for example
// "my-gateway/session-fingerprint/v1", so rotating it invalidates old
// fingerprints without touching the master key.
func DeriveFingerprintKey(master []byte, info string) []byte {
	if len(master) == 0 {
		return nil
	}
	key, err := hkdf.Key(sha256.New, master, nil, info, 32)
	if err != nil {
		return nil
	}
	return key
}

// Fingerprint returns the cursor value for a TextNormalizer digest, or "" when
// digest is nil. The value is "fp<NormalizationVersion>_<hex>" so a stored
// fingerprint never matches one produced under different normalization rules.
func (fingerprinter *Fingerprinter) Fingerprint(digest []byte) string {
	if fingerprinter == nil || len(digest) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, fingerprinter.key)
	_, _ = mac.Write([]byte(fingerprintDomain))
	_, _ = mac.Write(digest)
	sum := hex.EncodeToString(mac.Sum(nil))
	return "fp" + strconv.Itoa(NormalizationVersion) + "_" + sum[:fingerprintHexLength]
}
