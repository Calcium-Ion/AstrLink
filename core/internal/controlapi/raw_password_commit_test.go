package controlapi

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func TestRawPasswordCommittedActionsFinishAfterRequestCancellation(t *testing.T) {
	for _, action := range []RawPasswordAction{RawPasswordSet, RawPasswordChange, RawPasswordReset} {
		t.Run(string(action), func(t *testing.T) {
			store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "raw-password-test.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			key, err := store.GetOrCreateAuditKey(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			clear(key)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			armed, committed := false, false
			vault := NewRawVault(store, RawVaultOptions{
				KDF: rawVaultTestKDF,
				Logf: func(format string, _ ...any) {
					if !armed {
						return
					}
					switch format {
					case "raw sealing: created the raw sealing key", "raw sealing: the raw password was changed",
						"raw sealing: replaced the raw sealing key; discarded %d raw part(s) of %d request(s)":
						// These messages follow successful storage writes. Cancel at
						// that boundary, before refresh or biometric cleanup.
						committed = true
						cancel()
					}
				},
			})
			if action != RawPasswordSet {
				mustSetRawPassword(t, vault, rawTestPassword, RawProof{})
				if _, err := vault.EnrollBiometric(t.Context(), passwordProof(rawTestPassword), bytes.Repeat([]byte{7}, 32)); err != nil {
					t.Fatal(err)
				}
			}
			armed = true
			outcome, err := vault.ChangePassword(ctx, action, []byte(rawVaultNewPassword), passwordProof(rawTestPassword))
			if !committed || !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("request was not cancelled after its storage write")
			}
			if err != nil || !outcome.Committed || !outcome.Status.PasswordSet {
				t.Fatalf("committed=%t password_set=%t error=%v", outcome.Committed, outcome.Status.PasswordSet, err)
			}
			state, err := store.LoadRawSealing(t.Context())
			if err != nil || state.Password == nil || state.Biometric != nil {
				t.Fatalf("persisted password=%t biometric=%t error=%v", state.Password != nil, state.Biometric != nil, err)
			}
			if _, err := vault.Verify(t.Context(), passwordProof(rawVaultNewPassword)); err != nil {
				t.Fatalf("new password cannot open the cached state: %v", err)
			}
			if _, err := vault.Verify(t.Context(), passwordProof(rawTestPassword)); !errors.Is(err, ErrRawPasswordInvalid) {
				t.Fatalf("old password error=%v, want invalid password", err)
			}
		})
	}
}
