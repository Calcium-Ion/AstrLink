package accountauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
)

// Grok subscription login follows the public Grok CLI (grok-build) client:
// RFC 8628 device authorization against auth.x.ai, then Bearer requests to the
// CLI chat proxy with the CLI identity headers. AstrLink never embeds a client
// secret; the client ID below is the public one shipped in the open-source CLI.
const (
	DefaultGrokIssuer        = "https://auth.x.ai"
	DefaultGrokClientID      = "b1a00492-073a-47ea-816f-4c329264a828"
	DefaultGrokAPIBaseURL    = "https://cli-chat-proxy.grok.com"
	DefaultGrokDeviceCodeTTL = 15 * time.Minute

	grokDeviceGrantType    = "urn:ietf:params:oauth:grant-type:device_code"
	grokSlowDownIncrement  = 5 * time.Second
	grokTokenAuthHeader    = "xai-grok-cli"
	grokClientSurfaceValue = "ui"
)

// DefaultGrokScopes is the minimal scope set that authorizes CLI proxy calls
// (`grok-cli:access`, `api:access`) plus identity and refresh tokens.
func DefaultGrokScopes() []string {
	return []string{"openid", "profile", "email", "offline_access", "grok-cli:access", "api:access"}
}

func normalizeGrokConfig(config OAuthConfig) OAuthConfig {
	if strings.TrimSpace(config.ClientID) == "" {
		config.ClientID = DefaultGrokClientID
	}
	if config.Issuer == "" {
		config.Issuer = DefaultGrokIssuer
	}
	if config.TokenURL == "" {
		config.TokenURL = strings.TrimRight(config.Issuer, "/") + "/oauth2/token"
	}
	if config.APIBaseURL == "" {
		config.APIBaseURL = DefaultGrokAPIBaseURL
	}
	if len(config.Scopes) == 0 {
		config.Scopes = DefaultGrokScopes()
	}
	if config.DeviceCodeTTL <= 0 {
		config.DeviceCodeTTL = DefaultGrokDeviceCodeTTL
	}
	if strings.TrimSpace(config.ModelsClientVersion) == "" {
		config.ModelsClientVersion = DefaultGrokCLIClientVersion
	}
	return config
}

// ApplyGrokAPIHeaders writes the Grok CLI identity used by cli-chat-proxy:
// Bearer token, the CLI token-auth marker, the client version and a CLI-style
// User-Agent. Go's default User-Agent is otherwise treated as bot traffic.
func ApplyGrokAPIHeaders(header http.Header, tokens AccountTokens, clientVersion string) {
	if header == nil {
		return
	}
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = DefaultGrokCLIClientVersion
	}
	header.Set("Authorization", "Bearer "+tokens.AccessToken)
	header.Set("X-XAI-Token-Auth", grokTokenAuthHeader)
	header.Set("X-Grok-Client-Version", clientVersion)
	header.Set("User-Agent", grokUserAgentProduct+"/"+clientVersion)
}

func applyGrokOAuthHeaders(header http.Header, clientVersion string) {
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = DefaultGrokCLIClientVersion
	}
	header.Set("X-Grok-Client-Version", clientVersion)
	header.Set("X-Grok-Client-Surface", grokClientSurfaceValue)
	header.Set("User-Agent", grokUserAgentProduct+"/"+clientVersion)
}

type grokDeviceCodeResponse struct {
	DeviceCode              string          `json:"device_code"`
	UserCode                string          `json:"user_code"`
	VerificationURI         string          `json:"verification_uri"`
	VerificationURIComplete string          `json:"verification_uri_complete"`
	ExpiresIn               int64           `json:"expires_in"`
	Interval                json.RawMessage `json:"interval"`
}

type grokDeviceAuthorization struct {
	VerificationURL string
	UserCode        string
	DeviceCode      string
	PollInterval    time.Duration
	ExpiresIn       time.Duration
}

type grokTokenErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (manager *SessionManager) beginGrokDeviceAuthorization(
	ctx context.Context,
	serviceID contract.ServiceID,
) (contract.AuthorizationSession, error) {
	manager.mu.Lock()
	if err := manager.canStartLocked(serviceID); err != nil {
		manager.mu.Unlock()
		return contract.AuthorizationSession{}, err
	}
	manager.mu.Unlock()

	device, err := manager.requestGrokDeviceAuthorization(ctx)
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	sessionID, err := manager.newID()
	if err != nil {
		return contract.AuthorizationSession{}, err
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := manager.canStartLocked(serviceID); err != nil {
		return contract.AuthorizationSession{}, err
	}
	ttl := manager.config.DeviceCodeTTL
	if device.ExpiresIn > 0 && device.ExpiresIn < ttl {
		ttl = device.ExpiresIn
	}
	now := manager.config.Now().UTC()
	public := contract.AuthorizationSession{
		ID: sessionID, Provider: contract.SubscriptionProviderXAIGrok,
		Status: contract.AuthorizationSessionStatusPending,
		Flow:   contract.AuthorizationFlowDeviceCode,
		DeviceCode: &contract.AuthorizationDeviceCode{
			VerificationURL: device.VerificationURL,
			UserCode:        device.UserCode,
		},
		ServiceID: serviceID, ExpiresAt: now.Add(ttl),
		CreatedAt: now, UpdatedAt: now,
	}
	sessionContext, cancel := context.WithCancel(networkproxy.Copy(context.Background(), ctx))
	manager.sessions[sessionID] = &trackedSession{
		public:  public,
		secrets: &sessionSecrets{cancel: cancel},
	}
	manager.byService[serviceID] = sessionID
	go manager.expireAfter(sessionContext, sessionID, ttl)
	go manager.pollGrokDeviceAuthorization(sessionContext, sessionID, device)
	return cloneAuthorizationSession(public), nil
}

func (manager *SessionManager) requestGrokDeviceAuthorization(ctx context.Context) (grokDeviceAuthorization, error) {
	form := url.Values{}
	form.Set("client_id", manager.config.ClientID)
	form.Set("scope", strings.Join(manager.config.Scopes, " "))
	endpoint := strings.TrimRight(manager.config.Issuer, "/") + "/oauth2/device/code"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return grokDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	applyGrokOAuthHeaders(request.Header, manager.config.ModelsClientVersion)
	response, err := manager.config.HTTPClient.Do(request)
	if err != nil {
		return grokDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return grokDeviceAuthorization{}, ErrDeviceCodeUnavailable
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return grokDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return grokDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	var parsed grokDeviceCodeResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return grokDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	if strings.TrimSpace(parsed.DeviceCode) == "" || len(parsed.DeviceCode) > 4096 ||
		!validGrokUserCode(parsed.UserCode) {
		return grokDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	// Prefer the pre-filled URL so the user only confirms the code they see.
	verification := strings.TrimSpace(parsed.VerificationURIComplete)
	if verification == "" {
		verification = strings.TrimSpace(parsed.VerificationURI)
	}
	public := contract.AuthorizationDeviceCode{VerificationURL: verification, UserCode: parsed.UserCode}
	if len(verification) > 4096 || public.Validate() != nil {
		return grokDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	interval, err := parseDevicePollInterval(parsed.Interval)
	if err != nil {
		return grokDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	device := grokDeviceAuthorization{
		VerificationURL: verification,
		UserCode:        parsed.UserCode,
		DeviceCode:      parsed.DeviceCode,
		PollInterval:    manager.clampDevicePollInterval(interval),
	}
	if parsed.ExpiresIn > 0 {
		device.ExpiresIn = time.Duration(parsed.ExpiresIn) * time.Second
	}
	return device, nil
}

func validGrokUserCode(code string) bool {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 128 {
		return false
	}
	for _, r := range code {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

func (manager *SessionManager) pollGrokDeviceAuthorization(
	ctx context.Context,
	sessionID contract.AuthorizationSessionID,
	device grokDeviceAuthorization,
) {
	interval := device.PollInterval
	for {
		// Sleep first: an immediate poll on a fresh code only yields authorization_pending.
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
		tokens, pending, slowDown, err := manager.pollGrokDeviceAuthorizationOnce(ctx, device)
		if err != nil {
			if ctx.Err() == nil {
				manager.failSession(sessionID, &contract.SubscriptionError{
					Code:    ErrCodeDeviceCodePoll,
					Message: "Device Code login failed while waiting for xAI",
				})
			}
			return
		}
		if slowDown {
			interval = manager.clampDevicePollInterval(interval + grokSlowDownIncrement)
		}
		if pending {
			continue
		}
		if err := manager.completeSession(ctx, sessionID, tokens); err != nil && ctx.Err() == nil {
			manager.failSession(sessionID, &contract.SubscriptionError{
				Code:    ErrCodeStoreUnavailable,
				Message: "failed to persist connected account",
			})
		}
		return
	}
}

// pollGrokDeviceAuthorizationOnce exchanges the device code once. It returns
// pending=true while the user has not approved yet, slowDown=true when the
// provider asked for a longer interval, and a terminal error otherwise.
func (manager *SessionManager) pollGrokDeviceAuthorizationOnce(
	ctx context.Context,
	device grokDeviceAuthorization,
) (AccountTokens, bool, bool, error) {
	form := url.Values{}
	form.Set("grant_type", grokDeviceGrantType)
	form.Set("device_code", device.DeviceCode)
	form.Set("client_id", manager.config.ClientID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, manager.tokens.config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return AccountTokens{}, false, false, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	applyGrokOAuthHeaders(request.Header, manager.config.ModelsClientVersion)
	response, err := manager.config.HTTPClient.Do(request)
	if err != nil {
		return AccountTokens{}, false, false, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return AccountTokens{}, false, false, err
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		tokens, err := manager.tokens.parseTokenResponse(body, response.StatusCode, "")
		if err != nil {
			return AccountTokens{}, false, false, err
		}
		return tokens, false, false, nil
	}
	var failure grokTokenErrorResponse
	if json.Unmarshal(bytes.TrimSpace(body), &failure) != nil || failure.Error == "" {
		return AccountTokens{}, false, false, fmt.Errorf("device token endpoint returned status %d", response.StatusCode)
	}
	switch failure.Error {
	case "authorization_pending":
		return AccountTokens{}, true, false, nil
	case "slow_down":
		return AccountTokens{}, true, true, nil
	default:
		// access_denied, expired_token, invalid_grant and unknown codes end the session.
		return AccountTokens{}, false, false, fmt.Errorf("device authorization ended: %s", failure.Error)
	}
}
