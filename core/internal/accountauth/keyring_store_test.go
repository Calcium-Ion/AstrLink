package accountauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/zalando/go-keyring"
)

func TestKeyringCredentialStoreAvailabilityFailsClosed(t *testing.T) {
	t.Parallel()
	backendErr := errors.New("credential backend unavailable")

	t.Run("write probe", func(t *testing.T) {
		t.Parallel()
		getCalled := false
		store := &KeyringCredentialStore{
			service: "astrlink.test",
			set: func(_, _, _ string) error {
				return backendErr
			},
			get: func(_, _ string) (string, error) {
				getCalled = true
				return "", nil
			},
			delete: func(_, _ string) error {
				return nil
			},
		}

		err := store.Available(context.Background())
		if !errors.Is(err, ErrCredentialStoreUnavailable) {
			t.Fatalf("Available() = %v, want ErrCredentialStoreUnavailable", err)
		}
		if getCalled {
			t.Fatal("probe read continued after failed probe write")
		}
	})

	t.Run("read probe", func(t *testing.T) {
		t.Parallel()
		deleteCalled := false
		store := &KeyringCredentialStore{
			service: "astrlink.test",
			set: func(_, _, _ string) error {
				return nil
			},
			get: func(_, _ string) (string, error) {
				return "", backendErr
			},
			delete: func(_, _ string) error {
				deleteCalled = true
				return nil
			},
		}

		err := store.Available(context.Background())
		if !errors.Is(err, ErrCredentialStoreUnavailable) {
			t.Fatalf("Available() = %v, want ErrCredentialStoreUnavailable", err)
		}
		if !deleteCalled {
			t.Fatal("probe cleanup was not attempted after a failed probe read")
		}
	})

	t.Run("delete probe", func(t *testing.T) {
		t.Parallel()
		store := &KeyringCredentialStore{
			service: "astrlink.test",
			set: func(_, _, _ string) error {
				return nil
			},
			get: func(_, _ string) (string, error) {
				return "probe", nil
			},
			delete: func(_, _ string) error {
				return backendErr
			},
		}

		err := store.Available(context.Background())
		if !errors.Is(err, ErrCredentialStoreUnavailable) {
			t.Fatalf("Available() = %v, want ErrCredentialStoreUnavailable", err)
		}
	})
}

func TestKeyringCredentialStoreOperationsFailClosed(t *testing.T) {
	t.Parallel()
	const (
		accountID    contract.SubscriptionAccountID = "service_keyring_test"
		accessToken                                 = "access-private-marker"
		refreshToken                                = "refresh-private-marker"
	)
	backendErr := errors.New(
		"credential backend unavailable: " + accessToken + " " + refreshToken,
	)
	tokens := AccountTokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
	}

	t.Run("get", func(t *testing.T) {
		t.Parallel()
		store := &KeyringCredentialStore{
			service: "astrlink.test",
			get: func(_, _ string) (string, error) {
				return "", backendErr
			},
		}
		got, err := store.Get(context.Background(), accountID)
		if !errors.Is(err, ErrCredentialStoreUnavailable) {
			t.Fatalf("Get() = %#v, %v; want ErrCredentialStoreUnavailable", got, err)
		}
		if got != (AccountTokens{}) {
			t.Fatalf("Get() returned tokens on backend failure: %#v", got)
		}
		assertCredentialMarkersAbsent(t, err, accessToken, refreshToken)
	})

	t.Run("put", func(t *testing.T) {
		t.Parallel()
		store := &KeyringCredentialStore{
			service: "astrlink.test",
			set: func(_, _, _ string) error {
				return backendErr
			},
		}
		err := store.Put(context.Background(), accountID, tokens)
		if !errors.Is(err, ErrCredentialStoreUnavailable) {
			t.Fatalf("Put() = %v, want ErrCredentialStoreUnavailable", err)
		}
		assertCredentialMarkersAbsent(t, err, accessToken, refreshToken)
	})

	t.Run("delete", func(t *testing.T) {
		t.Parallel()
		store := &KeyringCredentialStore{
			service: "astrlink.test",
			delete: func(_, _ string) error {
				return backendErr
			},
		}
		err := store.Delete(context.Background(), accountID)
		if !errors.Is(err, ErrCredentialStoreUnavailable) {
			t.Fatalf("Delete() = %v, want ErrCredentialStoreUnavailable", err)
		}
		assertCredentialMarkersAbsent(t, err, accessToken, refreshToken)
	})
}

func TestKeyringCredentialStoreNotFoundSemantics(t *testing.T) {
	t.Parallel()
	const accountID contract.SubscriptionAccountID = "service_keyring_missing"

	store := &KeyringCredentialStore{
		service: "astrlink.test",
		get: func(_, _ string) (string, error) {
			return "", keyring.ErrNotFound
		},
		delete: func(_, _ string) error {
			return keyring.ErrNotFound
		},
	}
	if _, err := store.Get(context.Background(), accountID); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("Get() = %v, want ErrCredentialNotFound", err)
	}
	if err := store.Delete(context.Background(), accountID); err != nil {
		t.Fatalf("Delete() missing credential = %v, want nil", err)
	}
}

func assertCredentialMarkersAbsent(t *testing.T, err error, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(err.Error(), marker) {
			t.Fatalf("credential error leaked %q: %v", marker, err)
		}
	}
}
