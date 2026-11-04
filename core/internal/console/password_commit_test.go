package console

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type passwordCommitStore struct {
	storage.RawSealingStore
	beforeWrite func(context.Context) error
	afterWrite  func()
	loadError   error
}

func (store *passwordCommitStore) PutRawKeyEnvelope(ctx context.Context, keyID int64, envelope storage.RawKeyEnvelope) error {
	if store.beforeWrite != nil {
		if err := store.beforeWrite(ctx); err != nil {
			return err
		}
	}
	if err := store.RawSealingStore.PutRawKeyEnvelope(ctx, keyID, envelope); err != nil {
		return err
	}
	store.afterWrite()
	return nil
}

func (store *passwordCommitStore) LoadRawSealing(ctx context.Context) (storage.RawSealingState, error) {
	if store.loadError != nil {
		err := store.loadError
		store.loadError = nil
		return storage.RawSealingState{}, err
	}
	return store.RawSealingStore.LoadRawSealing(ctx)
}

func TestConsolePasswordCommitInvalidatesSessionsDespiteCancellationOrReloadFailure(t *testing.T) {
	for _, mode := range []string{"cancel after commit", "reload fails after commit", "cancel before write", "write fails"} {
		t.Run(mode, func(t *testing.T) {
			var store *passwordCommitStore
			f := newFixtureWithVaultStore(t, testPassword, false, func(inner storage.RawSealingStore) storage.RawSealingStore {
				store = &passwordCommitStore{RawSealingStore: inner}
				return store
			})
			changer := sessionFrom(t, login(f.console, testPassword))
			other := sessionFrom(t, login(f.console, testPassword))
			gate := &pausedConsolePassword{RawPassword: f.vault, verified: make(chan struct{}), resume: make(chan struct{})}
			f.console.rawPassword = gate
			loginCtx, cancelLogin := context.WithCancel(t.Context())
			pending := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader(passwordBody(testPassword))).WithContext(loginCtx)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(RequestHeader, RequestHeaderValue)
			done := make(chan struct{})
			go func() {
				defer close(done)
				f.console.ServeHTTP(pending, request)
			}()
			t.Cleanup(func() { cancelLogin(); <-done })
			select {
			case <-gate.verified:
			case <-done:
				t.Fatalf("login ended before verification: %d", pending.Code)
			case <-loginCtx.Done():
				t.Fatal("login cancelled before verification")
			}

			changeCtx, cancelChange := context.WithCancel(t.Context())
			defer cancelChange()
			committed := false
			store.afterWrite = func() {
				committed = true
				if mode == "cancel after commit" {
					cancelChange()
				} else if mode == "reload fails after commit" {
					store.loadError = errors.New("injected read failure after commit")
				}
			}
			store.beforeWrite = func(ctx context.Context) error {
				switch mode {
				case "cancel before write":
					cancelChange()
					return ctx.Err()
				case "write fails":
					return errors.New("injected write failure")
				}
				return nil
			}
			const newPassword = "a newly committed password"
			change := httptest.NewRequest(http.MethodPost, controlapi.RawPasswordPath,
				strings.NewReader(`{"action":"change","password":"`+newPassword+`","proof":{"password":"`+testPassword+`"}}`)).WithContext(changeCtx)
			change.Header.Set("Content-Type", "application/json")
			change.Header.Set(RequestHeader, RequestHeaderValue)
			change.AddCookie(changer)
			response := httptest.NewRecorder()
			f.console.ServeHTTP(response, change)
			close(gate.resume)
			select {
			case <-done:
			case <-loginCtx.Done():
				t.Fatal("pending login did not finish")
			}

			wantCommitted := mode == "cancel after commit" || mode == "reload fails after commit"
			if committed != wantCommitted {
				t.Fatalf("committed=%t, want %t", committed, wantCommitted)
			}
			wantLogin, persistedPassword := http.StatusOK, testPassword
			if committed {
				wantLogin, persistedPassword = http.StatusUnauthorized, newPassword
			}
			fresh := controlapi.NewRawVault(f.store, controlapi.RawVaultOptions{KDF: testKDF})
			if _, err := fresh.Verify(t.Context(), controlapi.RawProof{Password: []byte(persistedPassword)}); err != nil {
				t.Fatalf("persisted password is wrong: %v", err)
			}
			if pending.Code != wantLogin {
				t.Errorf("pending login=%d, want %d (change HTTP %d)", pending.Code, wantLogin, response.Code)
			}
			if committed && len(pending.Result().Cookies()) != 0 {
				t.Error("pending old-password login received a cookie")
			}
			if access := (call{method: http.MethodGet, path: controlapi.AccessTokensPath, cookie: other}).do(f.console); access.Code != wantLogin {
				t.Errorf("old session access=%d, want %d", access.Code, wantLogin)
			}
			if login := login(f.console, persistedPassword); login.Code != http.StatusOK {
				t.Errorf("persisted password login=%d, want 200", login.Code)
			}
			if oldLogin := login(f.console, testPassword); oldLogin.Code != wantLogin {
				t.Errorf("fresh old-password login=%d, want %d", oldLogin.Code, wantLogin)
			}
			if !statusOf(t, f.console, changer).SignedIn {
				t.Error("password changer lost its own session")
			}
		})
	}
}
