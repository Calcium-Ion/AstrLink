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
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

var (
	ErrCredentialRequired = errors.New("endpoint credential is required")
	ErrInvalidCredential  = errors.New("endpoint credential cannot be used in an HTTP header")
)

// Authorizer resolves an Endpoint's opaque credential reference for a single
// request. Implementations return only the headers that must override inbound
// client authentication and supply backend identity. Client headers are
// read-only and may be nil for gateway-initiated requests. Callers must not
// persist or log the returned values.
type Authorizer interface {
	Headers(context.Context, contract.Endpoint, http.Header) (http.Header, error)
}

type SecretAuthorizer struct {
	store secretstore.SecretStore
}

type SubscriptionTokenSource interface {
	AccessToken(context.Context, contract.ServiceID) (accountauth.AccountTokens, error)
}

type ServiceAuthorizer struct {
	http            *SecretAuthorizer
	subscriptions   SubscriptionTokenSource
	codexIdentity   accountauth.CodexIdentityPolicy
	routingSettings storage.RoutingSettingsStore
	identities      *accountauth.IdentityRegistry
}

func NewSecretAuthorizer(store secretstore.SecretStore) *SecretAuthorizer {
	return &SecretAuthorizer{store: store}
}

func NewServiceAuthorizer(store secretstore.SecretStore, subscriptions SubscriptionTokenSource, identity ...accountauth.CodexIdentityPolicy) *ServiceAuthorizer {
	authorizer := &ServiceAuthorizer{http: NewSecretAuthorizer(store), subscriptions: subscriptions}
	if len(identity) > 0 {
		authorizer.codexIdentity = identity[0]
	}
	return authorizer
}

// WithRoutingSettings reads the persisted identity choice for each new request,
// so changes in the desktop settings apply without restarting the gateway.
func (authorizer *ServiceAuthorizer) WithRoutingSettings(settings storage.RoutingSettingsStore) *ServiceAuthorizer {
	authorizer.routingSettings = settings
	return authorizer
}

// WithIdentities resolves the learned client identities and version floors
// that replace a non-official client's identity.
func (authorizer *ServiceAuthorizer) WithIdentities(identities *accountauth.IdentityRegistry) *ServiceAuthorizer {
	authorizer.identities = identities
	return authorizer
}

func (authorizer *ServiceAuthorizer) Headers(ctx context.Context, endpoint contract.Endpoint, clientHeaders http.Header) (http.Header, error) {
	if endpoint.Kind.IsHTTP() {
		return authorizer.http.Headers(ctx, endpoint, clientHeaders)
	}
	if endpoint.Kind.IsSubscription() {
		if authorizer == nil || authorizer.subscriptions == nil {
			return nil, secretstore.ErrUnavailable
		}
		tokens, err := authorizer.subscriptions.AccessToken(ctx, endpoint.ID)
		if err != nil {
			return nil, fmt.Errorf("load subscription credential: %w", err)
		}
		settings := contract.DefaultRoutingSettings()
		settings.CodexIdentityEnforcement = !authorizer.codexIdentity.DisableEnforcement
		if authorizer.routingSettings != nil {
			settings, err = authorizer.routingSettings.GetRoutingSettings(ctx)
			if err != nil {
				return nil, fmt.Errorf("load forwarding identity settings: %w", err)
			}
		}
		headers := make(http.Header)
		// An official Claude Code or Codex CLI request is forwarded with its own
		// identity intact; every other class keeps the enforced default.
		official := settings.OfficialClientPassthrough &&
			accountauth.ClientClassFrom(ctx) == accountauth.ClientClassOfficial
		switch endpoint.Kind {
		case contract.ServiceKindClaudeSubscription:
			if official {
				accountauth.ApplyClaudeOfficialForwardHeaders(headers, tokens, clientHeaders)
			} else {
				accountauth.ApplyClaudeForwardHeaders(headers, tokens, clientHeaders, authorizer.identities.ClaudeIdentity(settings), settings.ClaudeIdentityEnforcement)
			}
		case contract.ServiceKindGrokSubscription:
			accountauth.ApplyGrokForwardHeaders(headers, tokens, clientHeaders, settings.GrokIdentityEnforcement)
		default:
			if official {
				accountauth.ApplyCodexOfficialForwardHeaders(headers, tokens)
			} else {
				identity := authorizer.codexIdentity
				identity.DisableEnforcement = !settings.CodexIdentityEnforcement
				identity.Identity = authorizer.identities.CodexIdentity(settings, identity.ClientVersion)
				accountauth.ApplyCodexForwardHeaders(headers, tokens, clientHeaders, identity)
			}
			if tokens.AccountID == "" {
				// An empty overlay deletes any client-supplied account binding.
				headers[http.CanonicalHeaderKey("ChatGPT-Account-ID")] = nil
			}
		}
		return headers, nil
	}
	return nil, fmt.Errorf("unsupported service kind %q", endpoint.Kind)
}

func (authorizer *SecretAuthorizer) Headers(ctx context.Context, endpoint contract.Endpoint, _ http.Header) (http.Header, error) {
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
