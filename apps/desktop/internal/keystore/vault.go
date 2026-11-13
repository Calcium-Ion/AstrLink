package keystore

import (
	"errors"

	"github.com/egoist/mygo"
)

// systemVault keeps entries in the system credential store under one
// service: generic passwords of the login keychain on macOS.
type systemVault struct{ service string }

func (v systemVault) Get(account string) ([]byte, error) {
	value, err := mygo.Secrets.Service(v.service).Get(account)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return nil, ErrNotFound
	}
	return value, err
}

func (v systemVault) Set(account string, value []byte) error {
	return mygo.Secrets.Service(v.service).Set(account, value)
}

func (v systemVault) Delete(account string) error {
	return mygo.Secrets.Service(v.service).Delete(account)
}
