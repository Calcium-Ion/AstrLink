package endpoint

import (
	"context"
	"errors"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

type fakeSecretStore struct {
	secret []byte
	err    error
	ref    secretstore.Ref
}

func (store *fakeSecretStore) Get(_ context.Context, ref secretstore.Ref) ([]byte, error) {
	store.ref = ref
	if store.err != nil {
		return nil, store.err
	}
	return append([]byte(nil), store.secret...), nil
}

func (*fakeSecretStore) Put(context.Context, secretstore.Ref, []byte) error { return nil }
func (*fakeSecretStore) Delete(context.Context, secretstore.Ref) error      { return nil }

type fakeSubscriptionTokenSource struct {
	tokens accountauth.AccountTokens
	err    error
	id     contract.ServiceID
}

func (source *fakeSubscriptionTokenSource) AccessToken(
	_ context.Context,
	id contract.ServiceID,
) (accountauth.AccountTokens, error) {
	source.id = id
	if source.err != nil {
		return accountauth.AccountTokens{}, source.err
	}
	return source.tokens, nil
}

func TestSecretAuthorizerBuildsVendorAuthenticationHeaders(t *testing.T) {
	tests := []struct {
		name       string
		auth       contract.EndpointAuth
		headerName string
		want       string
	}{
		{name: "bearer", auth: contract.EndpointAuth{Scheme: contract.AuthSchemeBearer}, headerName: "Authorization", want: "Bearer upstream-secret"},
		{name: "anthropic", auth: contract.EndpointAuth{Scheme: contract.AuthSchemeAnthropicAPIKey}, headerName: "X-Api-Key", want: "upstream-secret"},
		{name: "google", auth: contract.EndpointAuth{Scheme: contract.AuthSchemeGoogleAPIKey}, headerName: "X-Goog-Api-Key", want: "upstream-secret"},
		{name: "custom", auth: contract.EndpointAuth{Scheme: contract.AuthSchemeCustomHeader, HeaderName: "X-Custom-Token"}, headerName: "X-Custom-Token", want: "upstream-secret"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeSecretStore{secret: []byte("upstream-secret")}
			authorizer := NewSecretAuthorizer(store)
			endpoint := contract.Endpoint{
				Auth:          test.auth,
				CredentialRef: "local://endpoint/endpoint_01",
			}

			headers, err := authorizer.Headers(context.Background(), endpoint)
			if err != nil {
				t.Fatalf("Headers: %v", err)
			}
			if got := headers.Get(test.headerName); got != test.want {
				t.Fatalf("%s = %q, want %q", test.headerName, got, test.want)
			}
			if store.ref != "local://endpoint/endpoint_01" {
				t.Fatalf("secret ref = %q", store.ref)
			}
		})
	}
}

func TestSecretAuthorizerFailsClosedWithoutCredentialStore(t *testing.T) {
	endpoint := contract.Endpoint{
		Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeBearer},
	}
	authorizer := NewSecretAuthorizer(nil)

	if _, err := authorizer.Headers(context.Background(), endpoint); !errors.Is(err, ErrCredentialRequired) {
		t.Fatalf("missing ref error = %v", err)
	}
	endpoint.CredentialRef = "local://endpoint/endpoint_01"
	if _, err := authorizer.Headers(context.Background(), endpoint); !errors.Is(err, secretstore.ErrUnavailable) {
		t.Fatalf("missing store error = %v", err)
	}
}

func TestSecretAuthorizerAllowsExplicitNoAuthentication(t *testing.T) {
	headers, err := NewSecretAuthorizer(nil).Headers(context.Background(), contract.Endpoint{
		Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeNone},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != 0 {
		t.Fatalf("headers = %#v, want empty", headers)
	}
}

func TestSecretAuthorizerRejectsUnsafeHeaderCredentials(t *testing.T) {
	for _, secret := range [][]byte{
		{},
		[]byte("   "),
		[]byte("secret\r\nX-Injected: value"),
		append([]byte("secret"), 0),
	} {
		store := &fakeSecretStore{secret: secret}
		_, err := NewSecretAuthorizer(store).Headers(context.Background(), contract.Endpoint{
			Auth:          contract.EndpointAuth{Scheme: contract.AuthSchemeBearer},
			CredentialRef: "local://endpoint/endpoint_01",
		})
		if !errors.Is(err, ErrInvalidCredential) {
			t.Fatalf("credential %q error = %v", secret, err)
		}
	}
}

func TestSecretAuthorizerPreservesStoreErrorsWithoutSecretMaterial(t *testing.T) {
	storeErr := errors.New("keyring locked")
	store := &fakeSecretStore{err: storeErr}
	_, err := NewSecretAuthorizer(store).Headers(context.Background(), contract.Endpoint{
		Auth:          contract.EndpointAuth{Scheme: contract.AuthSchemeGoogleAPIKey},
		CredentialRef: "local://endpoint/endpoint_01",
	})
	if !errors.Is(err, storeErr) {
		t.Fatalf("error = %v", err)
	}
}

func TestServiceAuthorizerFailsClosedWithoutSubscriptionTokenSource(t *testing.T) {
	endpoint := contract.Endpoint{
		ID:   "service_subscription",
		Kind: contract.ServiceKindCodexSubscription,
	}

	for name, authorizer := range map[string]*ServiceAuthorizer{
		"nil authorizer": nil,
		"nil source":     NewServiceAuthorizer(nil, nil),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := authorizer.Headers(context.Background(), endpoint); !errors.Is(err, secretstore.ErrUnavailable) {
				t.Fatalf("Headers() error = %v, want credential source unavailable", err)
			}
		})
	}
}

func TestServiceAuthorizerWrapsSubscriptionTokenErrors(t *testing.T) {
	tokenErr := errors.New("subscription needs reauthorization")
	source := &fakeSubscriptionTokenSource{err: tokenErr}
	_, err := NewServiceAuthorizer(nil, source).Headers(context.Background(), contract.Endpoint{
		ID:   "service_subscription",
		Kind: contract.ServiceKindCodexSubscription,
	})
	if !errors.Is(err, tokenErr) {
		t.Fatalf("Headers() error = %v, want wrapped token error", err)
	}
	if got := err.Error(); got != "load subscription credential: subscription needs reauthorization" {
		t.Fatalf("Headers() error = %q", got)
	}
}

func TestServiceAuthorizerBuildsSubscriptionHeaders(t *testing.T) {
	const serviceID = contract.ServiceID("service_subscription")
	source := &fakeSubscriptionTokenSource{tokens: accountauth.AccountTokens{
		AccessToken: "test-access-token",
		AccountID:   "account_01",
	}}
	headers, err := NewServiceAuthorizer(nil, source).Headers(context.Background(), contract.Endpoint{
		ID:   serviceID,
		Kind: contract.ServiceKindCodexSubscription,
	})
	if err != nil {
		t.Fatalf("Headers() error = %v", err)
	}
	if source.id != serviceID {
		t.Fatalf("AccessToken() service ID = %q, want %q", source.id, serviceID)
	}
	if len(headers) != 3 {
		t.Fatalf("Headers() count = %d, want 3", len(headers))
	}
	if got := headers.Get("Authorization"); got != "Bearer test-access-token" {
		t.Fatal("Headers() did not inject the subscription bearer credential")
	}
	if got := headers.Get("ChatGPT-Account-ID"); got != "account_01" {
		t.Fatalf("ChatGPT-Account-ID = %q, want account_01", got)
	}
	if got := headers.Get("OAI-Product-Sku"); got != "codex" {
		t.Fatalf("OAI-Product-Sku = %q, want codex", got)
	}
}

func TestServiceAuthorizerOmitsEmptySubscriptionAccountID(t *testing.T) {
	source := &fakeSubscriptionTokenSource{tokens: accountauth.AccountTokens{
		AccessToken: "test-access-token",
	}}
	headers, err := NewServiceAuthorizer(nil, source).Headers(context.Background(), contract.Endpoint{
		ID:   "service_subscription",
		Kind: contract.ServiceKindCodexSubscription,
	})
	if err != nil {
		t.Fatalf("Headers() error = %v", err)
	}
	if _, present := headers["Chatgpt-Account-Id"]; present {
		t.Fatal("Headers() included ChatGPT-Account-ID for an empty account ID")
	}
	if got := headers.Get("Authorization"); got != "Bearer test-access-token" {
		t.Fatal("Headers() did not inject the subscription bearer credential")
	}
	if got := headers.Get("OAI-Product-Sku"); got != "codex" {
		t.Fatalf("OAI-Product-Sku header = %q", got)
	}
}
