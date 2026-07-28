package accountauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PkceCodes holds an RFC 7636 S256 verifier/challenge pair.
type PkceCodes struct {
	Verifier  string
	Challenge string
}

func generatePKCE() (PkceCodes, error) {
	var raw [64]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return PkceCodes{}, fmt.Errorf("generate pkce verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(verifier))
	return PkceCodes{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

func generateState() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate oauth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
