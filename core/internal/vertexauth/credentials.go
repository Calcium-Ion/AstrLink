// Package vertexauth exchanges explicit Google service accounts for short-lived tokens.
package vertexauth

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"

	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
)

const tokenURL = "https://oauth2.googleapis.com/token"
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

var ErrCredential = errors.New("invalid Google service account JSON: check type, project_id, client_email, and RSA private_key")

// Compact validates before storage without weakening API-key header validation.
func Compact(secret []byte) ([]byte, error) {
	if _, err := config(secret); err != nil {
		return nil, err
	}
	var compact bytes.Buffer
	if json.Compact(&compact, secret) != nil {
		return nil, ErrCredential
	}
	return compact.Bytes(), nil
}

func config(secret []byte) (*jwt.Config, error) {
	if len(secret) == 0 || len(secret) > 16_384 {
		return nil, ErrCredential
	}
	var account struct {
		Type           string `json:"type"`
		ProjectID      string `json:"project_id"`
		ClientEmail    string `json:"client_email"`
		TokenURI       string `json:"token_uri"`
		UniverseDomain string `json:"universe_domain"`
	}
	if json.Unmarshal(secret, &account) != nil || account.Type != "service_account" || account.ProjectID == "" || account.ClientEmail == "" ||
		(account.TokenURI != "" && account.TokenURI != tokenURL) ||
		(account.UniverseDomain != "" && account.UniverseDomain != "googleapis.com") {
		return nil, ErrCredential
	}
	configuration, err := google.JWTConfigFromJSON(secret, cloudPlatformScope)
	if err != nil {
		return nil, ErrCredential
	}
	block, rest := pem.Decode(configuration.PrivateKey)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrCredential
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if err != nil || !ok || rsaKey.N.BitLen() < 2048 || rsaKey.Validate() != nil {
		return nil, ErrCredential
	}
	// Credential documents cannot redirect signed assertions to another server.
	configuration.TokenURL = tokenURL
	return configuration, nil
}
