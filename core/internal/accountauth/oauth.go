package accountauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const (
	DefaultIssuer                = "https://auth.openai.com"
	DefaultCodexAPIBaseURL       = "https://chatgpt.com/backend-api/codex"
	DefaultCodexOAuthClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultAuthorizationTTL      = 10 * time.Minute
	DefaultDeviceCodeTTL         = 15 * time.Minute
	DefaultRefreshSkew           = 5 * time.Minute
	DefaultCallbackPort          = 1455
	DefaultFallbackCallbackPort  = 1457
	DefaultDevicePollMinInterval = time.Second
	DefaultDevicePollMaxInterval = 30 * time.Second

	// ObservedCodexCLIOAuthClientID remains as a source-compatibility alias for
	// tests and older integrations. The public Codex client is intentionally
	// shared by clients implementing the official login protocol.
	ObservedCodexCLIOAuthClientID = DefaultCodexOAuthClientID

	ErrCodeStateMismatch         = "oauth_state_mismatch"
	ErrCodeSessionExpired        = "oauth_session_expired"
	ErrCodeSessionCancelled      = "oauth_session_cancelled"
	ErrCodeSessionInterrupted    = "oauth_session_interrupted"
	ErrCodeInvalidGrant          = "oauth_invalid_grant"
	ErrCodeCallbackFailed        = "oauth_callback_failed"
	ErrCodeCallbackPortsBusy     = "oauth_callback_ports_unavailable"
	ErrCodeDeviceCodeUnavailable = "oauth_device_code_unavailable"
	ErrCodeDeviceCodeRequest     = "oauth_device_code_request_failed"
	ErrCodeDeviceCodePoll        = "oauth_device_code_poll_failed"
	ErrCodeStoreUnavailable      = "account_credential_store_unavailable"
)

var (
	ErrInvalidGrant             = errors.New("oauth refresh token is no longer valid")
	ErrStateMismatch            = errors.New("oauth state mismatch")
	ErrSessionNotFound          = errors.New("authorization session not found")
	ErrSessionNotPending        = errors.New("authorization session is not pending")
	ErrCallbackPortsUnavailable = errors.New("codex oauth callback ports are unavailable")
	ErrDeviceCodeUnavailable    = errors.New("codex device code login is unavailable")
	ErrDeviceCodeRequestFailed  = errors.New("codex device code request failed")
)

// OAuthConfig configures the Codex subscription authorization adapter.
// It defaults to the official public Codex OAuth client and registered
// localhost callback ports.
type OAuthConfig struct {
	Provider              contract.SubscriptionProvider
	AuthorizeURL          string
	TokenURL              string
	CodeRedirectURI       string
	ClientID              string
	Issuer                string
	APIBaseURL            string
	Scopes                []string
	RedirectPath          string
	PreferredPort         int
	FallbackPort          int
	ResolveProxy          func(context.Context, contract.ServiceID) (context.Context, error)
	HTTPClient            *http.Client
	Now                   func() time.Time
	SessionTTL            time.Duration
	DeviceCodeTTL         time.Duration
	DevicePollMinInterval time.Duration
	DevicePollMaxInterval time.Duration
	RefreshSkew           time.Duration
	ExtraAuthQuery        url.Values
	ModelsClientVersion   string
	// Identities resolves the client identity of Codex authorization
	// requests and of the provider's gateway-initiated requests; nil uses the
	// baseline.
	Identities *IdentityRegistry
}

func (config OAuthConfig) Normalize() OAuthConfig {
	return config.normalized()
}

func (config OAuthConfig) normalized() OAuthConfig {
	if config.Provider == "" {
		config.Provider = contract.SubscriptionProviderOpenAICodex
	}
	if config.Provider == contract.SubscriptionProviderClaudeCode {
		config = normalizeClaudeConfig(config)
	}
	if config.Provider == contract.SubscriptionProviderXAIGrok {
		config = normalizeGrokConfig(config)
	}
	if strings.TrimSpace(config.ClientID) == "" {
		config.ClientID = DefaultCodexOAuthClientID
	}
	if config.Issuer == "" {
		config.Issuer = DefaultIssuer
	}
	if config.APIBaseURL == "" {
		config.APIBaseURL = DefaultCodexAPIBaseURL
	}
	if len(config.Scopes) == 0 {
		config.Scopes = []string{
			"openid", "profile", "email", "offline_access",
			"api.connectors.read", "api.connectors.invoke",
		}
	}
	if config.RedirectPath == "" {
		config.RedirectPath = "/auth/callback"
	}
	if config.PreferredPort <= 0 {
		config.PreferredPort = DefaultCallbackPort
	}
	if config.FallbackPort <= 0 {
		config.FallbackPort = DefaultFallbackCallbackPort
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = DefaultAuthorizationTTL
	}
	if config.DeviceCodeTTL <= 0 {
		config.DeviceCodeTTL = DefaultDeviceCodeTTL
	}
	if config.DevicePollMinInterval <= 0 {
		config.DevicePollMinInterval = DefaultDevicePollMinInterval
	}
	if config.DevicePollMaxInterval <= 0 {
		config.DevicePollMaxInterval = DefaultDevicePollMaxInterval
	}
	if config.DevicePollMaxInterval < config.DevicePollMinInterval {
		config.DevicePollMaxInterval = config.DevicePollMinInterval
	}
	if config.RefreshSkew <= 0 {
		config.RefreshSkew = DefaultRefreshSkew
	}
	if strings.TrimSpace(config.ModelsClientVersion) == "" {
		config.ModelsClientVersion = DefaultCodexModelsClientVersion
	}
	if config.Provider == contract.SubscriptionProviderOpenAICodex {
		config.ModelsClientVersion = codexVersionOrDefault(config.ModelsClientVersion)
	}
	config.HTTPClient = networkproxy.WrapClient(config.HTTPClient)
	return config
}

// ApplyCodexAPIHeaders uses one matched Codex identity for gateway-initiated
// backend requests. Forwarded requests use the same identity by default.
// Accept belongs to the request so authentication overlays cannot change its
// response format (for example, an SSE inference stream).
// The zero identity is the baseline.
func ApplyCodexAPIHeaders(header http.Header, tokens AccountTokens, identity ClientIdentity) {
	if header == nil {
		return
	}
	identity = codexIdentityOrDefault(identity)
	header.Set("Authorization", "Bearer "+tokens.AccessToken)
	header.Del("ChatGPT-Account-ID")
	if tokens.AccountID != "" {
		header.Set("ChatGPT-Account-ID", tokens.AccountID)
	}
	header.Set("OAI-Product-Sku", "codex")
	ApplyCodexAuthIdentity(header, identity)
	header.Set("version", identity.Version)
}

type tokenResponse struct {
	Account struct {
		UUID string `json:"uuid"`
	} `json:"account"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

type TokenClient struct {
	config OAuthConfig
}

func NewTokenClient(config OAuthConfig) *TokenClient {
	return &TokenClient{config: config.normalized()}
}

func (client *TokenClient) ExchangeCode(ctx context.Context, code, verifier, redirectURI string) (AccountTokens, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("redirect_uri", redirectURI)
	values.Set("client_id", client.config.ClientID)
	values.Set("code_verifier", verifier)
	if client.config.Provider == contract.SubscriptionProviderClaudeCode {
		parts := strings.SplitN(code, "#", 2)
		values.Set("code", parts[0])
		if len(parts) == 2 {
			values.Set("state", parts[1])
		}
	}
	return client.requestToken(ctx, values)
}

func (client *TokenClient) Refresh(ctx context.Context, refreshToken string) (AccountTokens, error) {
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", refreshToken)
	values.Set("client_id", client.config.ClientID)
	return client.requestToken(ctx, values)
}

func (client *TokenClient) requestToken(ctx context.Context, values url.Values) (AccountTokens, error) {
	endpoint := strings.TrimRight(client.config.Issuer, "/") + "/oauth/token"
	if client.config.TokenURL != "" {
		endpoint = client.config.TokenURL
	}
	var bodyReader io.Reader = strings.NewReader(values.Encode())
	contentType := "application/x-www-form-urlencoded"
	if client.config.Provider == contract.SubscriptionProviderClaudeCode {
		payload := make(map[string]string, len(values))
		for key := range values {
			payload[key] = values.Get(key)
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return AccountTokens{}, err
		}
		bodyReader = bytes.NewReader(body)
		contentType = "application/json"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bodyReader)
	if err != nil {
		return AccountTokens{}, err
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	if client.config.Provider == contract.SubscriptionProviderXAIGrok {
		applyGrokOAuthHeaders(request.Header, client.config.ModelsClientVersion)
	} else if client.config.Provider == contract.SubscriptionProviderOpenAICodex {
		ApplyCodexAuthIdentity(request.Header, client.config.Identities.CodexIdentityFor(ctx, client.config.ModelsClientVersion))
	}
	request.Header.Set("Accept-Encoding", transport.SupportedResponseEncodings)
	response, err := client.config.HTTPClient.Do(request)
	if err != nil {
		return AccountTokens{}, err
	}
	defer response.Body.Close()
	body, err := transport.ReadResponseBody(response, 1<<20)
	if err != nil {
		return AccountTokens{}, err
	}
	return client.parseTokenResponse(body, response.StatusCode, values.Get("refresh_token"))
}

// parseTokenResponse maps a provider token response onto AccountTokens.
// previousRefresh is reused when the provider does not rotate refresh tokens.
func (client *TokenClient) parseTokenResponse(body []byte, status int, previousRefresh string) (AccountTokens, error) {
	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return AccountTokens{}, fmt.Errorf("decode token response: %w", err)
	}
	if parsed.Error == "invalid_grant" {
		return AccountTokens{}, ErrInvalidGrant
	}
	if status < 200 || status >= 300 || parsed.AccessToken == "" {
		return AccountTokens{}, fmt.Errorf("%s: token endpoint returned status %d", ErrCodeCallbackFailed, status)
	}
	expiresIn := parsed.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	refresh := parsed.RefreshToken
	if refresh == "" {
		refresh = previousRefresh
	}
	if refresh == "" {
		return AccountTokens{}, fmt.Errorf("token response missing refresh_token")
	}
	var accountID string
	switch client.config.Provider {
	case contract.SubscriptionProviderClaudeCode:
		accountID = parsed.Account.UUID
	case contract.SubscriptionProviderXAIGrok:
		// The xAI id_token subject identifies the user; the access token carries
		// the consented principal when the user picked a team or organization.
		accountID = jwtStringClaim(parsed.IDToken, "sub")
		if accountID == "" {
			accountID = jwtStringClaim(parsed.AccessToken, "principal_id", "principalId", "sub")
		}
	default:
		accountID = accountIDFromIDToken(parsed.IDToken)
	}
	return AccountTokens{
		AccessToken:  parsed.AccessToken,
		RefreshToken: refresh,
		TokenType:    parsed.TokenType,
		Scope:        parsed.Scope,
		AccountID:    accountID,
		ExpiresAt:    client.config.Now().UTC().Add(time.Duration(expiresIn) * time.Second),
	}, nil
}

func accountIDFromIDToken(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	for _, key := range []string{
		"https://api.openai.com/auth.chatgpt_account_id",
		"chatgpt_account_id",
		"account_id",
	} {
		if value, ok := claims[key].(string); ok && value != "" {
			return value
		}
	}
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if value, ok := auth["chatgpt_account_id"].(string); ok {
			return value
		}
	}
	return ""
}

func decodeJWTSegment(segment string) ([]byte, error) {
	return decodeRawURLBase64(segment)
}

// jwtStringClaim returns the first non-empty string claim among keys from an
// unverified JWT payload. Tokens arrive over direct HTTPS from the issuer and
// are only used for display and de-duplication, never for authorization.
func jwtStringClaim(token string, keys ...string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := claims[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
