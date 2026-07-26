package controlapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

type fakeAccessTokenManager struct {
	tokens       []accesstoken.Token
	created      accesstoken.CreatedToken
	revealed     map[contract.AccessTokenID]string
	deleted      []contract.AccessTokenID
	listErr      error
	createErr    error
	revealErr    error
	deleteErr    error
	receivedName string
}

func (manager *fakeAccessTokenManager) List(context.Context) ([]accesstoken.Token, error) {
	return manager.tokens, manager.listErr
}

func (manager *fakeAccessTokenManager) Create(_ context.Context, name string) (accesstoken.CreatedToken, error) {
	manager.receivedName = name
	return manager.created, manager.createErr
}

func (manager *fakeAccessTokenManager) Reveal(_ context.Context, id contract.AccessTokenID) (string, error) {
	if manager.revealErr != nil {
		return "", manager.revealErr
	}
	value, ok := manager.revealed[id]
	if !ok {
		return "", storage.ErrNotFound
	}
	return value, nil
}

func (manager *fakeAccessTokenManager) Delete(_ context.Context, id contract.AccessTokenID) error {
	if manager.deleteErr != nil {
		return manager.deleteErr
	}
	manager.deleted = append(manager.deleted, id)
	return nil
}

func TestAccessTokenControlAPICRUDSeparatesMetadataFromExplicitSecrets(t *testing.T) {
	const raw = "astr_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	createdAt := time.Date(2026, time.July, 24, 12, 30, 0, 0, time.UTC)
	token := accesstoken.Token{
		ID: "token_primary", Name: "Primary", Hint: "astr_…CDEF",
		Source: accesstoken.SourceUser, CreatedAt: createdAt,
	}
	manager := &fakeAccessTokenManager{
		tokens:   []accesstoken.Token{token},
		created:  accesstoken.CreatedToken{Token: token, Value: raw},
		revealed: map[contract.AccessTokenID]string{token.ID: raw},
	}
	handler := newAccessTokenHandler(t, manager)

	response := accessTokenRequest(t, handler, http.MethodGet, AccessTokensPath, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{raw, `"access_token"`, `"hash"`, `"token_hash"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("list leaked secret material %q: %s", forbidden, body)
		}
	}
	var listed accessTokenListResponse
	decode(t, response, &listed)
	if len(listed.Items) != 1 || listed.Items[0].ID != token.ID ||
		listed.Items[0].Name != token.Name || listed.Items[0].Hint != token.Hint ||
		listed.Items[0].Source != string(accesstoken.SourceUser) ||
		!listed.Items[0].CreatedAt.Equal(createdAt) || listed.NextCursor != nil {
		t.Fatalf("list = %#v", listed)
	}

	response = accessTokenRequest(t, handler, http.MethodPost, AccessTokensPath, "application/json", `{"name":" Primary "}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	if manager.receivedName != " Primary " {
		t.Fatalf("manager name=%q", manager.receivedName)
	}
	if response.Header().Get("Location") != AccessTokensPath+"/token_primary" {
		t.Fatalf("Location=%q", response.Header().Get("Location"))
	}
	var created accessTokenSecretResponse
	decode(t, response, &created)
	if created.Token.ID != token.ID || created.AccessToken != raw {
		t.Fatalf("create response=%#v", created)
	}

	response = accessTokenRequest(t, handler, http.MethodGet, AccessTokensPath+"/token_primary/secret", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("reveal status=%d body=%s", response.Code, response.Body.String())
	}
	var revealed accessTokenRevealResponse
	decode(t, response, &revealed)
	if revealed.AccessToken != raw {
		t.Fatalf("revealed=%q", revealed.AccessToken)
	}

	response = accessTokenRequest(t, handler, http.MethodDelete, AccessTokensPath+"/token_primary", "", "")
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	if len(manager.deleted) != 1 || manager.deleted[0] != token.ID {
		t.Fatalf("deleted=%v", manager.deleted)
	}
}

func TestAccessTokenControlAPIRequiresAuthenticationAndStrictContracts(t *testing.T) {
	handler := newAccessTokenHandler(t, &fakeAccessTokenManager{})

	request := httptest.NewRequest(http.MethodGet, AccessTokensPath, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthorized status=%d headers=%#v", response.Code, response.Header())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unauthorized Cache-Control=%q", response.Header().Get("Cache-Control"))
	}

	tests := []struct {
		name        string
		method      string
		path        string
		contentType string
		body        string
		status      int
		allow       string
	}{
		{name: "collection method", method: http.MethodPut, path: AccessTokensPath, status: http.StatusMethodNotAllowed, allow: "GET, POST"},
		{name: "create media type", method: http.MethodPost, path: AccessTokensPath, contentType: "text/plain", body: `{}`, status: http.StatusUnsupportedMediaType},
		{name: "create unknown field", method: http.MethodPost, path: AccessTokensPath, contentType: "application/json", body: `{"name":"x","secret":"forbidden"}`, status: http.StatusBadRequest},
		{name: "unexpected query", method: http.MethodGet, path: AccessTokensPath + "?limit=1", status: http.StatusBadRequest},
		{name: "item method", method: http.MethodGet, path: AccessTokensPath + "/token_primary", status: http.StatusMethodNotAllowed, allow: http.MethodDelete},
		{name: "secret method", method: http.MethodDelete, path: AccessTokensPath + "/token_primary/secret", status: http.StatusMethodNotAllowed, allow: http.MethodGet},
		{name: "invalid id", method: http.MethodDelete, path: AccessTokensPath + "/!", status: http.StatusBadRequest},
		{name: "extra path", method: http.MethodGet, path: AccessTokensPath + "/token_primary/secret/extra", status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := accessTokenRequest(t, handler, test.method, test.path, test.contentType, test.body)
			if response.Code != test.status || response.Header().Get("Allow") != test.allow {
				t.Fatalf("status=%d allow=%q body=%s", response.Code, response.Header().Get("Allow"), response.Body.String())
			}
		})
	}
}

func TestAccessTokenControlAPISanitizesManagerErrors(t *testing.T) {
	const sensitive = "astr_secret_value hash=abcdef"
	manager := &fakeAccessTokenManager{listErr: errors.New(sensitive)}
	handler := newAccessTokenHandler(t, manager)

	response := accessTokenRequest(t, handler, http.MethodGet, AccessTokensPath, "", "")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), sensitive) || strings.Contains(response.Body.String(), "abcdef") {
		t.Fatalf("manager error leaked: %s", response.Body.String())
	}

	manager.listErr = nil
	manager.createErr = accesstoken.ErrInvalidName
	response = accessTokenRequest(t, handler, http.MethodPost, AccessTokensPath, "application/json", `{"name":""}`)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid name status=%d body=%s", response.Code, response.Body.String())
	}
	manager.createErr = accesstoken.ErrTokenLimit
	response = accessTokenRequest(t, handler, http.MethodPost, AccessTokensPath, "application/json", `{"name":"over limit"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("limit status=%d body=%s", response.Code, response.Body.String())
	}
	manager.createErr = nil
	manager.revealErr = storage.ErrNotFound
	response = accessTokenRequest(t, handler, http.MethodGet, AccessTokensPath+"/token_missing/secret", "", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing reveal status=%d body=%s", response.Code, response.Body.String())
	}
}

func newAccessTokenHandler(t *testing.T, manager AccessTokenManager) *Handler {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		EndpointStore: store, AccessTokenManager: manager, ControlToken: testControlToken,
	})
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	return handler
}

func accessTokenRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	contentType string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", response.Header().Get("Cache-Control"))
	}
	return response
}
