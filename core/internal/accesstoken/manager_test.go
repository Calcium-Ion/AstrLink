package accesstoken

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type fakeAccessTokenStore struct {
	created      storage.NewAccessToken
	defaulted    storage.NewAccessToken
	listed       []storage.AccessTokenMetadata
	revealed     string
	findRecord   storage.AccessTokenMetadata
	findErr      error
	findHash     storage.AccessTokenHash
	createCalls  int
	defaultCalls int
	findCalls    int
	deletedID    contract.AccessTokenID
}

func (store *fakeAccessTokenStore) EnsureDefaultAccessToken(_ context.Context, candidate storage.NewAccessToken) (storage.AccessTokenMetadata, bool, error) {
	store.defaultCalls++
	store.defaulted = candidate
	return metadataFromCandidate(candidate), true, nil
}

func (store *fakeAccessTokenStore) CreateAccessToken(_ context.Context, candidate storage.NewAccessToken) (storage.AccessTokenMetadata, error) {
	store.createCalls++
	store.created = candidate
	return metadataFromCandidate(candidate), nil
}

func (store *fakeAccessTokenStore) ListAccessTokens(context.Context) ([]storage.AccessTokenMetadata, error) {
	return append([]storage.AccessTokenMetadata(nil), store.listed...), nil
}

func (store *fakeAccessTokenStore) RevealAccessToken(context.Context, contract.AccessTokenID) (string, error) {
	return store.revealed, nil
}

func (store *fakeAccessTokenStore) DeleteAccessToken(_ context.Context, id contract.AccessTokenID) error {
	store.deletedID = id
	return nil
}

func (store *fakeAccessTokenStore) FindAccessTokenByHash(_ context.Context, hash storage.AccessTokenHash) (storage.AccessTokenMetadata, error) {
	store.findCalls++
	store.findHash = hash
	return store.findRecord, store.findErr
}

func metadataFromCandidate(candidate storage.NewAccessToken) storage.AccessTokenMetadata {
	return storage.AccessTokenMetadata{
		ID: candidate.ID, Name: candidate.Name, Hint: candidate.Hint,
		CreatedAt: time.Date(2026, 7, 24, 1, 2, 3, 0, time.UTC),
	}
}

func TestCreateGeneratesCanonicalTokenAndSafeMetadata(t *testing.T) {
	store := &fakeAccessTokenStore{}
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	randomBytes := append(bytes.Repeat([]byte{0x11}, idRandomBytes), bytes.Repeat([]byte{0x22}, tokenRandomBytes)...)
	manager.random = bytes.NewReader(randomBytes)

	created, err := manager.Create(context.Background(), "  Build Agent  ")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	expectedRaw := tokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, tokenRandomBytes))
	if created.Value != expectedRaw || !validRawToken(created.Value) {
		t.Fatalf("created raw token = %q", created.Value)
	}
	if created.Token.Name != "Build Agent" {
		t.Fatalf("created metadata = %#v", created.Token)
	}
	if store.created.NameKey != "BUILD AGENT" || store.created.Value != expectedRaw {
		t.Fatalf("stored candidate = %#v", store.created)
	}
	if store.created.ID != "access_token_11111111111111111111111111111111" {
		t.Fatalf("generated id = %q", store.created.ID)
	}
	sum := sha256.Sum256([]byte(expectedRaw))
	if store.created.Hash != storage.AccessTokenHash(sum) || store.created.Hint != tokenHint(expectedRaw) {
		t.Fatal("created token hash/hint do not match raw token")
	}

	encoded, err := json.Marshal(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(expectedRaw)) || bytes.Contains(encoded, []byte(base64.RawURLEncoding.EncodeToString(sum[:]))) {
		t.Fatalf("safe metadata leaked secret/hash: %s", encoded)
	}
}

func TestEnsureDefaultCreatesNamedTokenOnce(t *testing.T) {
	store := &fakeAccessTokenStore{}
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	manager.random = bytes.NewReader(make([]byte, idRandomBytes+tokenRandomBytes))
	token, created, err := manager.EnsureDefault(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !created || token.Name != DefaultTokenName {
		t.Fatalf("default token = %#v, created=%t", token, created)
	}
	if store.defaulted.Name != DefaultTokenName {
		t.Fatalf("defaulted candidate = %#v", store.defaulted)
	}
}

func TestCreateRejectsInvalidNamesBeforeGeneratingOrStoring(t *testing.T) {
	for _, name := range []string{"", " \t ", "line\nbreak", strings.Repeat("界", 65)} {
		store := &fakeAccessTokenStore{}
		manager, err := NewManager(store)
		if err != nil {
			t.Fatal(err)
		}
		manager.random = bytes.NewReader(nil)
		if _, err := manager.Create(context.Background(), name); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("Create(%q) error = %v", name, err)
		}
		if store.createCalls != 0 {
			t.Fatalf("Create(%q) reached store", name)
		}
	}
}

func TestAuthenticateHashesCanonicalValueAndCollapsesInvalidRecords(t *testing.T) {
	raw := tokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, tokenRandomBytes))
	expectedHash := sha256.Sum256([]byte(raw))
	for _, test := range []struct {
		name      string
		raw       string
		findErr   error
		wantError bool
		wantCalls int
	}{
		{name: "valid", raw: raw, wantCalls: 1},
		{name: "malformed", raw: "astr_not-base64!", wantError: true},
		{name: "missing", raw: raw, findErr: storage.ErrNotFound, wantError: true, wantCalls: 1},
		{name: "corrupt", raw: raw, findErr: storage.ErrInvalidRecord, wantError: true, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeAccessTokenStore{
				findErr: test.findErr,
				findRecord: storage.AccessTokenMetadata{
					ID: "access_token_stable",
				},
			}
			manager, err := NewManager(store)
			if err != nil {
				t.Fatal(err)
			}
			id, err := manager.Authenticate(context.Background(), test.raw)
			if test.wantError {
				if !errors.Is(err, ErrInvalidToken) || id != "" {
					t.Fatalf("Authenticate error = %v, id=%q", err, id)
				}
			} else if err != nil || id != "access_token_stable" {
				t.Fatalf("Authenticate error = %v, id=%q", err, id)
			}
			if store.findCalls != test.wantCalls {
				t.Fatalf("Find calls = %d, want %d", store.findCalls, test.wantCalls)
			}
			if test.wantCalls == 1 && store.findHash != storage.AccessTokenHash(expectedHash) {
				t.Fatal("Authenticate did not query the SHA-256 token hash")
			}
		})
	}
}

func TestNewManagerRequiresStore(t *testing.T) {
	if _, err := NewManager(nil); err == nil {
		t.Fatal("NewManager accepted nil store")
	}
}
