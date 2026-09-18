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
)

const (
	DefaultIssuer             = "https://auth.openai.com"
	DefaultCodexAPIBaseURL    = "https://chatgpt.com/backend-api/codex"
	DefaultCodexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	// DefaultCodexModelsClientVersion is the observed openai/codex ModelsClient
	// query (public CLI 0.150.0). The backend may hide models below a
	// catalog minimum; do not send a non-semver placeholder.
	DefaultCodexModelsClientVersion = "0.150.0"
	DefaultAuthorizationTTL         = 10 * time.Minute
	DefaultDeviceCodeTTL            = 15 * time.Minute
	DefaultRefreshSkew              = 5 * time.Minute
	DefaultCallbackPort             = 1455
	DefaultFallbackCallbackPort     = 1457
	DefaultDevicePollMinInterval    = time.Second
	DefaultDevicePollMaxInterval    = 30 * time.Second

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
	HTTPClient            *http.Client
	Now                   func() time.Time
	SessionTTL            time.Duration
	DeviceCodeTTL         time.Duration
	DevicePollMinInterval time.Duration
	DevicePollMaxInterval time.Duration
	RefreshSkew           time.Duration
	Originator            string
	ExtraAuthQuery        url.Values
	ModelsClientVersion   string
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
	if config.Originator == "" {
		config.Originator = "astrlink"
	}
	if strings.TrimSpace(config.ModelsClientVersion) == "" {
		config.ModelsClientVersion = DefaultCodexModelsClientVersion
	}
	return config
}

// ApplyCodexAPIHeaders writes the observed ChatGPT Codex backend request
// headers. Official openai/codex clients always send originator plus a
// Codex-style User-Agent; chatgpt.com otherwise treats Go's default
// User-Agent as bot traffic. User-Agent follows the public new-api/Codex
// CLI shape `codex-cli/{client_version}`. originator stays AstrLink's own
// identity.
func ApplyCodexAPIHeaders(header http.Header, tokens AccountTokens, originator, clientVersion string) {
	if header == nil {
		return
	}
	if strings.TrimSpace(originator) == "" {
		originator = "astrlink"
	}
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = DefaultCodexModelsClientVersion
	}
	header.Set("Authorization", "Bearer "+tokens.AccessToken)
	if tokens.AccountID != "" {
		header.Set("ChatGPT-Account-ID", tokens.AccountID)
	}
	header.Set("OAI-Product-Sku", "codex")
	header.Set("Accept", "application/json")
	header.Set("originator", originator)
	header.Set("version", clientVersion)
	header.Set("User-Agent", "codex-cli/"+clientVersion)
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
	response, err := client.config.HTTPClient.Do(request)
	if err != nil {
		return AccountTokens{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return AccountTokens{}, err
	}
	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return AccountTokens{}, fmt.Errorf("decode token response: %w", err)
	}
	if parsed.Error == "invalid_grant" {
		return AccountTokens{}, ErrInvalidGrant
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || parsed.AccessToken == "" {
		return AccountTokens{}, fmt.Errorf("%s: token endpoint returned status %d", ErrCodeCallbackFailed, response.StatusCode)
	}
	expiresIn := parsed.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	refresh := parsed.RefreshToken
	if refresh == "" {
		refresh = values.Get("refresh_token")
	}
	if refresh == "" {
		return AccountTokens{}, fmt.Errorf("token response missing refresh_token")
	}
	accountID := accountIDFromIDToken(parsed.IDToken)
	if client.config.Provider == contract.SubscriptionProviderClaudeCode {
		accountID = parsed.Account.UUID
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
