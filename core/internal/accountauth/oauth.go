package accountauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
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
}

func (config OAuthConfig) Normalize() OAuthConfig {
	return config.normalized()
}

func (config OAuthConfig) normalized() OAuthConfig {
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
	return config
}

type tokenResponse struct {
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
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return AccountTokens{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
