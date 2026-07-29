package accountauth

import (
	"context"
	"errors"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/zalando/go-keyring"
)

const keyringService = "astrlink.subscription"

// KeyringCredentialStore stores OAuth tokens in the OS credential store via
// zalando/go-keyring (Keychain / Credential Manager / Secret Service).
type KeyringCredentialStore struct {
	service string
	set     func(service, user, password string) error
	get     func(service, user string) (string, error)
	delete  func(service, user string) error
}

func NewKeyringCredentialStore() *KeyringCredentialStore {
	return &KeyringCredentialStore{
		service: keyringService,
		set:     keyring.Set,
		get:     keyring.Get,
		delete:  keyring.Delete,
	}
}

func (store *KeyringCredentialStore) Available(context.Context) error {
	probeUser := "astrlink.availability.probe"
	if err := store.set(store.service, probeUser, "probe"); err != nil {
		return ErrCredentialStoreUnavailable
	}
	if _, err := store.get(store.service, probeUser); err != nil {
		_ = store.delete(store.service, probeUser)
		return ErrCredentialStoreUnavailable
	}
	if err := store.delete(store.service, probeUser); err != nil && !errorsIsNotFound(err) {
		return ErrCredentialStoreUnavailable
	}
	return nil
}

func (store *KeyringCredentialStore) Get(_ context.Context, id contract.SubscriptionAccountID) (AccountTokens, error) {
	if err := id.Validate(); err != nil {
		return AccountTokens{}, err
	}
	raw, err := store.get(store.service, string(id))
	if err != nil {
		if errorsIsNotFound(err) {
			return AccountTokens{}, ErrCredentialNotFound
		}
		return AccountTokens{}, ErrCredentialStoreUnavailable
	}
	return UnmarshalAccountTokens([]byte(raw))
}

func (store *KeyringCredentialStore) Put(_ context.Context, id contract.SubscriptionAccountID, tokens AccountTokens) error {
	if err := id.Validate(); err != nil {
		return err
	}
	raw, err := tokens.MarshalSecret()
	if err != nil {
		return err
	}
	if err := store.set(store.service, string(id), string(raw)); err != nil {
		return ErrCredentialStoreUnavailable
	}
	return nil
}

func (store *KeyringCredentialStore) Delete(_ context.Context, id contract.SubscriptionAccountID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	if err := store.delete(store.service, string(id)); err != nil {
		if errorsIsNotFound(err) {
			return nil
		}
		return ErrCredentialStoreUnavailable
	}
	return nil
}

func errorsIsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}
