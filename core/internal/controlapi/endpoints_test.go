package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

const testControlToken = "test-control-token-0123456789"

func TestEndpointRoutesAreAbsentWithoutAuthenticatedDependencies(t *testing.T) {
	handler := New(contract.DefaultVersionResponse("", ""))
	response := serve(t, handler, http.MethodGet, EndpointsPath)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestEndpointControlAPICRUDKeepsCredentialWriteOnly(t *testing.T) {
	store, handler := newEndpointHandler(t)
	ctx := context.Background()

	createBody := `{
        "name":"primary",
        "kind":"openai",
        "base_url":"https://api.example/v1",
        "auth":{"scheme":"bearer"},
        "credential":{"secret":"first-provider-secret"},
        "capabilities":[{"protocol":"openai.responses","mode":"native","streaming":true}]
    }`
	response := endpointRequest(t, handler, http.MethodPost, EndpointsPath, "application/json", createBody, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", response.Code, response.Body.String())
	}
	createdETag := response.Header().Get("ETag")
	if createdETag == "" || response.Header().Get("Location") != EndpointsPath+"/endpoint_test" {
		t.Fatalf("create headers = %#v", response.Header())
	}
	if strings.Contains(response.Body.String(), "first-provider-secret") {
		t.Fatal("create response exposed credential")
	}
	var created contract.Endpoint
	decode(t, response, &created)
	if created.ID != "endpoint_test" || created.CredentialRef != "local://endpoint/endpoint_test" {
		t.Fatalf("created endpoint = %#v", created)
	}
	secret, err := store.Get(ctx, secretstore.Ref(created.CredentialRef))
	if err != nil || string(secret) != "first-provider-secret" {
		t.Fatalf("stored credential = %q, %v", secret, err)
	}

	response = endpointRequest(t, handler, http.MethodGet, EndpointsPath+"/endpoint_test", "", "", "")
	if response.Code != http.StatusOK || response.Header().Get("ETag") != createdETag {
		t.Fatalf("get status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}
	if strings.Contains(response.Body.String(), "first-provider-secret") {
		t.Fatal("get response exposed credential")
	}

	response = endpointRequest(t, handler, http.MethodGet, EndpointsPath+"?limit=1&enabled=true&kind=openai", "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var page endpointPageResponse
	decode(t, response, &page)
	if len(page.Items) != 1 || page.Items[0].ID != created.ID || page.NextCursor != nil {
		t.Fatalf("endpoint page = %#v", page)
	}

	patchBody := `{"name":"rotated","credential":{"secret":"second-provider-secret"}}`
	response = endpointRequest(t, handler, http.MethodPatch, EndpointsPath+"/endpoint_test", "application/merge-patch+json", patchBody, `"stale"`)
	if response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale patch status=%d body=%s", response.Code, response.Body.String())
	}
	response = endpointRequest(t, handler, http.MethodPatch, EndpointsPath+"/endpoint_test", "application/merge-patch+json", patchBody, createdETag)
	if response.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", response.Code, response.Body.String())
	}
	updatedETag := response.Header().Get("ETag")
	var updated contract.Endpoint
	decode(t, response, &updated)
	if updated.Name != "rotated" || updatedETag == "" || updatedETag == createdETag {
		t.Fatalf("updated endpoint=%#v etag=%q", updated, updatedETag)
	}
	secret, err = store.Get(ctx, secretstore.Ref(updated.CredentialRef))
	if err != nil || string(secret) != "second-provider-secret" {
		t.Fatalf("rotated credential = %q, %v", secret, err)
	}

	response = endpointRequest(t, handler, http.MethodDelete, EndpointsPath+"/endpoint_test", "", "", createdETag)
	if response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale delete status=%d body=%s", response.Code, response.Body.String())
	}
	response = endpointRequest(t, handler, http.MethodDelete, EndpointsPath+"/endpoint_test", "", "", updatedETag)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := store.Get(ctx, secretstore.Ref(updated.CredentialRef)); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("credential survived endpoint delete: %v", err)
	}
}

func TestEndpointPatchOnlyDeletesCredentialWhenExplicit(t *testing.T) {
	store, handler := newEndpointHandler(t)
	ctx := context.Background()

	createBody := `{
        "name":"legacy",
        "kind":"custom",
        "base_url":"https://gateway.example/v1",
        "auth":{"scheme":"bearer"},
        "credential":{"secret":"provider-secret"},
        "capabilities":[{"protocol":"openai.responses","mode":"delegated","streaming":true}]
    }`
	response := endpointRequest(t, handler, http.MethodPost, EndpointsPath, "application/json", createBody, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var created contract.Endpoint
	decode(t, response, &created)
	credentialRef := created.CredentialRef

	response = endpointRequest(
		t,
		handler,
		http.MethodPatch,
		EndpointsPath+"/endpoint_test",
		"application/merge-patch+json",
		`{"auth":{"scheme":"none"}}`,
		response.Header().Get("ETag"),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("auth patch status=%d body=%s", response.Code, response.Body.String())
	}
	var noAuth contract.Endpoint
	decode(t, response, &noAuth)
	if noAuth.Auth.Scheme != contract.AuthSchemeNone || noAuth.CredentialRef != credentialRef {
		t.Fatalf("implicit credential mutation: %#v", noAuth)
	}
	secret, err := store.Get(ctx, secretstore.Ref(credentialRef))
	if err != nil || string(secret) != "provider-secret" {
		t.Fatalf("credential after auth-only patch=%q, %v", secret, err)
	}
	noAuthETag := response.Header().Get("ETag")

	for _, body := range []string{
		`{"credential":{}}`,
		`{"credential":{"secret":""}}`,
		`{"credential":{"secret":"replacement-secret"},"capabilities":[{"protocol":"openai.responses","mode":"delegated","streaming":true,"unexpected":true}]}`,
		`{"credential":{"secret":"replacement-secret"},"capabilities":[{"protocol":"openai.responses","mode":"delegated","streaming":true,"models":null}]}`,
		`{"credential":{"secret":"replacement-secret"},"capabilities":[{"protocol":"openai.responses","mode":"delegated"}]}`,
	} {
		response = endpointRequest(
			t,
			handler,
			http.MethodPatch,
			EndpointsPath+"/endpoint_test",
			"application/merge-patch+json",
			body,
			noAuthETag,
		)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("malformed credential patch status=%d body=%s", response.Code, response.Body.String())
		}
		secret, err = store.Get(ctx, secretstore.Ref(credentialRef))
		if err != nil || string(secret) != "provider-secret" {
			t.Fatalf("credential after rejected patch=%q, %v", secret, err)
		}
	}
	response = endpointRequest(t, handler, http.MethodGet, EndpointsPath+"/endpoint_test", "", "", "")
	if response.Code != http.StatusOK || response.Header().Get("ETag") != noAuthETag {
		t.Fatalf("endpoint changed after rejected patch: status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}

	response = endpointRequest(
		t,
		handler,
		http.MethodPatch,
		EndpointsPath+"/endpoint_test",
		"application/merge-patch+json",
		`{"credential":null}`,
		noAuthETag,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("credential clear status=%d body=%s", response.Code, response.Body.String())
	}
	var cleared contract.Endpoint
	decode(t, response, &cleared)
	if cleared.CredentialRef != "" {
		t.Fatalf("credential_ref after explicit clear=%q", cleared.CredentialRef)
	}
	if _, err := store.Get(ctx, secretstore.Ref(credentialRef)); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("credential survived explicit clear: %v", err)
	}
}

func TestEndpointCreateDistinguishesOmittedCredentialFromNull(t *testing.T) {
	const bodyWithoutCredential = `{
        "name":"no-auth",
        "kind":"custom",
        "base_url":"https://gateway.example/v1",
        "auth":{"scheme":"none"},
        "capabilities":[{"protocol":"openai.responses","mode":"delegated","streaming":true}]
    }`

	t.Run("omitted credential", func(t *testing.T) {
		_, handler := newEndpointHandler(t)
		response := endpointRequest(t, handler, http.MethodPost, EndpointsPath, "application/json", bodyWithoutCredential, "")
		if response.Code != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
		}
		var endpoint contract.Endpoint
		decode(t, response, &endpoint)
		if endpoint.CredentialRef != "" {
			t.Fatalf("credential_ref=%q", endpoint.CredentialRef)
		}
	})

	t.Run("explicit null credential", func(t *testing.T) {
		_, handler := newEndpointHandler(t)
		body := strings.Replace(bodyWithoutCredential, `"auth":{"scheme":"none"},`, `"auth":{"scheme":"none"},"credential":null,`, 1)
		response := endpointRequest(t, handler, http.MethodPost, EndpointsPath, "application/json", body, "")
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func TestEndpointAuthPatchDecodesFreshStrictValue(t *testing.T) {
	_, handler := newEndpointHandler(t)
	createBody := `{
        "name":"custom-auth",
        "kind":"custom",
        "base_url":"https://gateway.example/v1",
        "auth":{"scheme":"custom_header","header_name":"X-Subscription-Key"},
        "credential":{"secret":"provider-secret"},
        "capabilities":[{"protocol":"openai.responses","mode":"delegated","streaming":true}]
    }`
	response := endpointRequest(t, handler, http.MethodPost, EndpointsPath, "application/json", createBody, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	credentialRef := ""
	var created contract.Endpoint
	decode(t, response, &created)
	credentialRef = created.CredentialRef

	response = endpointRequest(
		t,
		handler,
		http.MethodPatch,
		EndpointsPath+"/endpoint_test",
		"application/merge-patch+json",
		`{"auth":{"scheme":"bearer"}}`,
		response.Header().Get("ETag"),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("auth transition status=%d body=%s", response.Code, response.Body.String())
	}
	var updated contract.Endpoint
	decode(t, response, &updated)
	if updated.Auth.Scheme != contract.AuthSchemeBearer || updated.Auth.HeaderName != "" || updated.CredentialRef != credentialRef {
		t.Fatalf("auth transition endpoint=%#v", updated)
	}
	updatedETag := response.Header().Get("ETag")

	for _, body := range []string{
		`{"auth":{"scheme":"bearer","header_name":null}}`,
		`{"auth":{"scheme":"custom_header"}}`,
	} {
		response = endpointRequest(
			t,
			handler,
			http.MethodPatch,
			EndpointsPath+"/endpoint_test",
			"application/merge-patch+json",
			body,
			updatedETag,
		)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid auth patch status=%d body=%s", response.Code, response.Body.String())
		}
	}
}

type snapshotMismatchStore struct {
	record       storage.EndpointRecord
	updateCalled bool
}

func (store *snapshotMismatchStore) CreateEndpoint(
	context.Context,
	contract.Endpoint,
	storage.CredentialMutation,
) (storage.EndpointRecord, error) {
	return storage.EndpointRecord{}, errors.New("unexpected create")
}

func (store *snapshotMismatchStore) GetEndpoint(
	context.Context,
	contract.EndpointID,
) (storage.EndpointRecord, error) {
	return store.record, nil
}

func (store *snapshotMismatchStore) ListEndpoints(
	context.Context,
	storage.EndpointListOptions,
) (storage.EndpointPage, error) {
	return storage.EndpointPage{}, errors.New("unexpected list")
}

func (store *snapshotMismatchStore) UpdateEndpoint(
	_ context.Context,
	endpoint contract.Endpoint,
	_ storage.CredentialMutation,
	_ string,
) (storage.EndpointRecord, error) {
	store.updateCalled = true
	return storage.EndpointRecord{Endpoint: endpoint, ETag: `"sha256:updated"`}, nil
}

func (store *snapshotMismatchStore) DeleteEndpoint(
	context.Context,
	contract.EndpointID,
	string,
) error {
	return errors.New("unexpected delete")
}

func TestEndpointPatchRejectsStaleSnapshotBeforeBuildingReplacement(t *testing.T) {
	store := &snapshotMismatchStore{
		record: storage.EndpointRecord{
			Endpoint: contract.Endpoint{
				ID:      "endpoint_test",
				Name:    "newer state",
				Kind:    contract.EndpointKindCustom,
				BaseURL: "https://gateway.example/v1",
				Auth:    contract.EndpointAuth{Scheme: contract.AuthSchemeNone},
				Enabled: true,
				Capabilities: []contract.Capability{{
					Protocol:  "openai.responses",
					Mode:      contract.CapabilityModeDelegated,
					Streaming: true,
				}},
			},
			ETag: `"sha256:newer"`,
		},
	}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		EndpointStore: store,
		ControlToken:  testControlToken,
	})
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	response := endpointRequest(
		t,
		handler,
		http.MethodPatch,
		EndpointsPath+"/endpoint_test",
		"application/merge-patch+json",
		`{"name":"client update"}`,
		`"sha256:older"`,
	)
	if response.Code != http.StatusPreconditionFailed {
		t.Fatalf("patch status=%d body=%s", response.Code, response.Body.String())
	}
	if store.updateCalled {
		t.Fatal("stale snapshot reached UpdateEndpoint")
	}
}

func TestEndpointControlAPIRequiresAuthenticationAndStrictContracts(t *testing.T) {
	_, handler := newEndpointHandler(t)

	request := httptest.NewRequest(http.MethodGet, EndpointsPath, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthorized response status=%d headers=%#v", response.Code, response.Header())
	}

	invalidCases := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
	}{
		{name: "media type", contentType: "text/plain", body: `{}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "unknown field", contentType: "application/json", body: `{"unexpected":true}`, wantStatus: http.StatusBadRequest},
		{name: "missing required", contentType: "application/json", body: `{"name":"incomplete"}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "invalid endpoint", contentType: "application/json", body: `{"name":"unsafe","kind":"openai","base_url":"https://user:secret@example.com","auth":{"scheme":"none"},"capabilities":[]}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "missing credential secret", contentType: "application/json", body: `{"name":"empty","kind":"custom","base_url":"https://gateway.example","auth":{"scheme":"bearer"},"credential":{},"capabilities":[{"protocol":"openai.responses","mode":"delegated","streaming":true}]}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "empty credential secret", contentType: "application/json", body: `{"name":"empty","kind":"custom","base_url":"https://gateway.example","auth":{"scheme":"bearer"},"credential":{"secret":""},"capabilities":[{"protocol":"openai.responses","mode":"delegated","streaming":true}]}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "null enabled", contentType: "application/json", body: `{"name":"empty","kind":"custom","base_url":"https://gateway.example","enabled":null,"auth":{"scheme":"none"},"capabilities":[]}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "unknown capability field", contentType: "application/json", body: `{"name":"invalid","kind":"custom","base_url":"https://gateway.example","auth":{"scheme":"none"},"capabilities":[{"protocol":"openai.responses","mode":"delegated","streaming":true,"unexpected":true}]}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "null capability models", contentType: "application/json", body: `{"name":"invalid","kind":"custom","base_url":"https://gateway.example","auth":{"scheme":"none"},"capabilities":[{"protocol":"openai.responses","mode":"delegated","streaming":true,"models":null}]}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "missing capability streaming", contentType: "application/json", body: `{"name":"invalid","kind":"custom","base_url":"https://gateway.example","auth":{"scheme":"none"},"capabilities":[{"protocol":"openai.responses","mode":"delegated"}]}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "null auth header", contentType: "application/json", body: `{"name":"invalid","kind":"custom","base_url":"https://gateway.example","auth":{"scheme":"bearer","header_name":null},"capabilities":[]}`, wantStatus: http.StatusUnprocessableEntity},
	}
	for _, test := range invalidCases {
		t.Run(test.name, func(t *testing.T) {
			response := endpointRequest(t, handler, http.MethodPost, EndpointsPath, test.contentType, test.body, "")
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}

	response = endpointRequest(t, handler, http.MethodGet, EndpointsPath+"?unknown=true", "", "", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown query status=%d", response.Code)
	}
	response = endpointRequest(t, handler, http.MethodPatch, EndpointsPath+"/endpoint_test", "application/merge-patch+json", `{"name":"x"}`, "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing If-Match status=%d", response.Code)
	}
}

func TestNewWithDependenciesRejectsMissingStoreOrWeakToken(t *testing.T) {
	version := contract.DefaultVersionResponse("", "")
	if _, err := NewWithDependencies(version, Dependencies{}); err == nil {
		t.Fatal("missing endpoint store was accepted")
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := NewWithDependencies(version, Dependencies{EndpointStore: store, ControlToken: "short"}); err == nil {
		t.Fatal("weak control token was accepted")
	}
}

func newEndpointHandler(t *testing.T) (*sqlite.Store, *Handler) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		EndpointStore: store,
		ControlToken:  testControlToken,
		NewEndpointID: func() (contract.EndpointID, error) { return "endpoint_test", nil },
	})
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	return store, handler
}

func endpointRequest(t *testing.T, handler http.Handler, method, path, contentType, body, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	return response
}

func TestEndpointResponsesRemainValidJSON(t *testing.T) {
	_, handler := newEndpointHandler(t)
	response := endpointRequest(t, handler, http.MethodGet, EndpointsPath, "", "", "")
	if !json.Valid(response.Body.Bytes()) {
		t.Fatalf("invalid JSON response: %s", response.Body.String())
	}
}
