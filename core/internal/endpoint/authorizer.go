package endpoint

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

var (
	ErrCredentialRequired = errors.New("endpoint credential is required")
	ErrInvalidCredential  = errors.New("endpoint credential cannot be used in an HTTP header")
)

// Authorizer resolves an Endpoint's opaque credential reference for a single
// request. Implementations return only the headers that must override inbound
// client authentication; callers must not persist or log the returned values.
type Authorizer interface {
	Headers(context.Context, contract.Endpoint) (http.Header, error)
}

type SecretAuthorizer struct {
	store secretstore.SecretStore
}

type SubscriptionTokenSource interface {
	AccessToken(context.Context, contract.ServiceID) (accountauth.AccountTokens, error)
}

type ServiceAuthorizer struct {
	http          *SecretAuthorizer
	subscriptions SubscriptionTokenSource
}

func NewSecretAuthorizer(store secretstore.SecretStore) *SecretAuthorizer {
	return &SecretAuthorizer{store: store}
}

func NewServiceAuthorizer(store secretstore.SecretStore, subscriptions SubscriptionTokenSource) *ServiceAuthorizer {
	return &ServiceAuthorizer{http: NewSecretAuthorizer(store), subscriptions: subscriptions}
}

func (authorizer *ServiceAuthorizer) Headers(ctx context.Context, endpoint contract.Endpoint) (http.Header, error) {
	if endpoint.Kind.IsHTTP() {
		return authorizer.http.Headers(ctx, endpoint)
	}
	if endpoint.Kind.IsSubscription() {
		if authorizer == nil || authorizer.subscriptions == nil {
			return nil, secretstore.ErrUnavailable
		}
		tokens, err := authorizer.subscriptions.AccessToken(ctx, endpoint.ID)
		if err != nil {
			return nil, fmt.Errorf("load subscription credential: %w", err)
		}
		headers := make(http.Header)
		accountauth.ApplyCodexAPIHeaders(headers, tokens, "", "")
		return headers, nil
	}
	return nil, fmt.Errorf("unsupported service kind %q", endpoint.Kind)
}

func (authorizer *SecretAuthorizer) Headers(ctx context.Context, endpoint contract.Endpoint) (http.Header, error) {
	if err := endpoint.Auth.Validate(); err != nil {
		return nil, fmt.Errorf("validate endpoint authentication: %w", err)
	}
	if endpoint.Auth.Scheme == contract.AuthSchemeNone {
		return make(http.Header), nil
	}
	if endpoint.CredentialRef == "" {
		return nil, ErrCredentialRequired
	}
	if authorizer == nil || authorizer.store == nil {
		return nil, secretstore.ErrUnavailable
	}
	ref, err := secretstore.ParseRef(endpoint.CredentialRef)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint credential reference: %w", err)
	}
	secret, err := authorizer.store.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("load endpoint credential: %w", err)
	}
	defer clear(secret)
	if !validHeaderSecret(secret) {
		return nil, ErrInvalidCredential
	}

	value := string(secret)
	headers := make(http.Header)
	switch endpoint.Auth.Scheme {
	case contract.AuthSchemeBearer:
		headers.Set("Authorization", "Bearer "+value)
	case contract.AuthSchemeAnthropicAPIKey:
		headers.Set("X-Api-Key", value)
	case contract.AuthSchemeGoogleAPIKey:
		headers.Set("X-Goog-Api-Key", value)
	case contract.AuthSchemeCustomHeader:
		headers.Set(endpoint.Auth.HeaderName, value)
	default:
		return nil, fmt.Errorf("unsupported endpoint authentication scheme %q", endpoint.Auth.Scheme)
	}
	return headers, nil
}

func validHeaderSecret(secret []byte) bool {
	if len(secret) == 0 || len(secret) > 16_384 {
		return false
	}
	for _, value := range secret {
		if value < 0x20 || value == 0x7f {
			return false
		}
	}
	return strings.TrimSpace(string(secret)) != ""
}
