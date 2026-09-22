package accountauth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
)

const (
	DefaultClaudeAPIBaseURL   = "https://api.anthropic.com"
	DefaultClaudeClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	DefaultClaudeAuthorizeURL = "https://claude.com/cai/oauth/authorize"
	DefaultClaudeTokenURL     = "https://platform.claude.com/v1/oauth/token"
	DefaultClaudeRedirectURI  = "https://platform.claude.com/oauth/code/callback"
	// DefaultClaudeUserAgent mirrors the Claude Code CLI. api.anthropic.com
	// routes unknown agents (including Go's default) into a far stricter
	// rate-limit bucket on the OAuth usage and models endpoints.
	DefaultClaudeUserAgent = "claude-cli/2.1.258 (external, cli)"
	ClaudeUserAgentPrefix  = "claude-cli/"
)

func normalizeClaudeConfig(config OAuthConfig) OAuthConfig {
	if config.ClientID == "" {
		config.ClientID = DefaultClaudeClientID
	}
	if config.APIBaseURL == "" {
		config.APIBaseURL = DefaultClaudeAPIBaseURL
	}
	if config.AuthorizeURL == "" {
		config.AuthorizeURL = DefaultClaudeAuthorizeURL
	}
	if config.TokenURL == "" {
		config.TokenURL = DefaultClaudeTokenURL
	}
	if config.CodeRedirectURI == "" {
		config.CodeRedirectURI = DefaultClaudeRedirectURI
	}
	if len(config.Scopes) == 0 {
		config.Scopes = []string{"user:profile", "user:inference", "user:sessions:claude_code", "user:mcp_servers", "user:file_upload"}
	}
	return config
}

func ApplyClaudeAPIHeaders(header http.Header, tokens AccountTokens) {
	if header == nil {
		return
	}
	header.Set("User-Agent", DefaultClaudeUserAgent)
	header.Set("Authorization", "Bearer "+tokens.AccessToken)
	header.Set("Anthropic-Version", "2023-06-01")
	header.Set("Anthropic-Beta", "claude-code-20250219,oauth-2025-04-20")
}

func (manager *SessionManager) beginCodeAuthorization(ctx context.Context, serviceID contract.ServiceID) (contract.AuthorizationSession, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := manager.canStartLocked(serviceID); err != nil {
		return contract.AuthorizationSession{}, err
	}
	state, err := generateState()
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	pkce, err := generatePKCE()
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	authURL, err := manager.buildAuthorizeURL(manager.config.CodeRedirectURI, state, pkce.Challenge)
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	id, err := manager.newID()
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	now := manager.config.Now().UTC()
	public := contract.AuthorizationSession{
		ID: id, Provider: manager.config.Provider, Status: contract.AuthorizationSessionStatusPending,
		Flow: contract.AuthorizationFlowCode, AuthorizationURL: authURL, ServiceID: serviceID,
		ExpiresAt: now.Add(manager.config.SessionTTL), CreatedAt: now, UpdatedAt: now,
	}
	ctx, cancel := context.WithCancel(networkproxy.Copy(context.Background(), ctx))
	manager.sessions[id] = &trackedSession{public: public, secrets: &sessionSecrets{
		proxyContext: ctx, state: state, pkce: pkce, redirectURI: manager.config.CodeRedirectURI, cancel: cancel,
	}}
	manager.byService[serviceID] = id
	go manager.expireAfter(ctx, id, manager.config.SessionTTL)
	return public, nil
}

// CompleteCode accepts only the code#state displayed by Claude's registered
// callback. It is never included in session documents or provider error text.
func (manager *SessionManager) CompleteCode(ctx context.Context, serviceID contract.ServiceID, sessionID contract.AuthorizationSessionID, code string) (contract.AuthorizationSession, error) {
	manager.mu.Lock()
	manager.expirePendingLocked()
	session := manager.sessions[sessionID]
	if session == nil || manager.byService[serviceID] != sessionID || session.public.ServiceID != serviceID ||
		session.public.Status != contract.AuthorizationSessionStatusPending || session.completing || session.exchanging ||
		session.public.Flow != contract.AuthorizationFlowCode || session.secrets == nil {
		manager.mu.Unlock()
		return contract.AuthorizationSession{}, ErrSessionNotPending
	}
	parts := strings.SplitN(strings.TrimSpace(code), "#", 2)
	if len(code) > 8192 || len(parts) != 2 || parts[0] == "" ||
		subtle.ConstantTimeCompare([]byte(parts[1]), []byte(session.secrets.state)) != 1 {
		manager.mu.Unlock()
		return contract.AuthorizationSession{}, ErrStateMismatch
	}
	verifier, redirect := session.secrets.pkce.Verifier, session.secrets.redirectURI
	ctx = networkproxy.Copy(ctx, session.secrets.proxyContext)
	session.exchanging = true
	manager.mu.Unlock()
	defer func() {
		manager.mu.Lock()
		session.exchanging = false
		manager.mu.Unlock()
	}()
	tokens, err := manager.tokens.ExchangeCode(ctx, strings.TrimSpace(code), verifier, redirect)
	if err != nil {
		manager.failSession(sessionID, &contract.SubscriptionError{Code: ErrCodeCallbackFailed, Message: "authorization code exchange failed"})
		return contract.AuthorizationSession{}, fmt.Errorf("authorization code exchange failed")
	}
	if err := manager.completeSession(ctx, sessionID, tokens); err != nil {
		manager.failSession(sessionID, &contract.SubscriptionError{Code: ErrCodeStoreUnavailable, Message: "failed to persist connected account"})
		return contract.AuthorizationSession{}, fmt.Errorf("failed to persist connected account")
	}
	public, _ := manager.Get(serviceID)
	return public, nil
}
