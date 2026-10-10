package accountauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const (
	standardDeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
	deviceSlowDownIncrement = 5 * time.Second
)

// polledDeviceAuthorization is one RFC 8628 device code awaiting approval.
type polledDeviceAuthorization struct {
	VerificationURL string
	UserCode        string
	DeviceCode      string
	PollInterval    time.Duration
	ExpiresIn       time.Duration
}

// beginPolledDeviceAuthorization runs an RFC 8628 device login: request a
// code, then poll the token endpoint in the background until the user
// approves, the provider ends the grant, or the session expires.
func (manager *SessionManager) beginPolledDeviceAuthorization(
	ctx context.Context,
	serviceID contract.ServiceID,
	request func(context.Context) (polledDeviceAuthorization, error),
	poll devicePollFunc,
	issuerName string,
) (contract.AuthorizationSession, error) {
	manager.mu.Lock()
	if err := manager.canStartLocked(serviceID); err != nil {
		manager.mu.Unlock()
		return contract.AuthorizationSession{}, err
	}
	manager.mu.Unlock()

	device, err := request(ctx)
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
		ID: sessionID, Provider: manager.config.Provider,
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
	go manager.pollPolledDeviceAuthorization(sessionContext, sessionID, device, poll, issuerName)
	return cloneAuthorizationSession(public), nil
}

// devicePollFunc exchanges a device code once. It returns pending=true while
// the user has not approved yet, slowDown=true when the provider asked for a
// longer interval, and a terminal error otherwise.
type devicePollFunc func(context.Context, polledDeviceAuthorization) (AccountTokens, bool, bool, error)

func (manager *SessionManager) pollPolledDeviceAuthorization(
	ctx context.Context,
	sessionID contract.AuthorizationSessionID,
	device polledDeviceAuthorization,
	poll devicePollFunc,
	issuerName string,
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
		tokens, pending, slowDown, err := poll(ctx, device)
		if err != nil {
			if ctx.Err() == nil {
				manager.failSession(sessionID, devicePollFailure(err, issuerName))
			}
			return
		}
		if slowDown {
			interval = manager.clampDevicePollInterval(interval + deviceSlowDownIncrement)
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

func devicePollFailure(err error, issuerName string) *contract.SubscriptionError {
	if errors.Is(err, ErrCopilotNotEntitled) {
		return &contract.SubscriptionError{
			Code:    ErrCodeCopilotNotEntitled,
			Message: "This GitHub account has no active Copilot plan",
		}
	}
	if errors.Is(err, ErrDroidNoOrganization) {
		return &contract.SubscriptionError{
			Code:    ErrCodeDroidNoOrganization,
			Message: "This Factory account belongs to no organization",
		}
	}
	return &contract.SubscriptionError{
		Code:    ErrCodeDeviceCodePoll,
		Message: "Device Code login failed while waiting for " + issuerName,
	}
}

type deviceAuthorization struct {
	VerificationURL string
	UserCode        string
	DeviceAuthID    string
	PollInterval    time.Duration
}

type deviceUserCodeResponse struct {
	DeviceAuthID string          `json:"device_auth_id"`
	UserCode     string          `json:"user_code"`
	UserCodeAlt  string          `json:"usercode"`
	Interval     json.RawMessage `json:"interval"`
}

type deviceTokenResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeChallenge     string `json:"code_challenge"`
	CodeVerifier      string `json:"code_verifier"`
}

func (manager *SessionManager) requestDeviceAuthorization(
	ctx context.Context,
) (deviceAuthorization, error) {
	body, err := json.Marshal(map[string]string{"client_id": manager.config.ClientID})
	if err != nil {
		return deviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	endpoint := strings.TrimRight(manager.config.Issuer, "/") + "/api/accounts/deviceauth/usercode"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return deviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	ApplyCodexAuthIdentity(request.Header, manager.config.Identities.CodexIdentityFor(ctx, manager.config.ModelsClientVersion))
	request.Header.Set("Accept-Encoding", transport.SupportedResponseEncodings)
	response, err := manager.config.HTTPClient.Do(request)
	if err != nil {
		return deviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
		return deviceAuthorization{}, ErrDeviceCodeUnavailable
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return deviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	raw, err := transport.ReadResponseBody(response, 1<<20)
	if err != nil {
		return deviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	var parsed deviceUserCodeResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return deviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	if parsed.UserCode == "" {
		parsed.UserCode = parsed.UserCodeAlt
	}
	if strings.TrimSpace(parsed.DeviceAuthID) == "" ||
		strings.TrimSpace(parsed.UserCode) == "" ||
		len(parsed.DeviceAuthID) > 4096 ||
		len(parsed.UserCode) > 128 {
		return deviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	interval, err := parseDevicePollInterval(parsed.Interval)
	if err != nil {
		return deviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	interval = manager.clampDevicePollInterval(interval)
	return deviceAuthorization{
		VerificationURL: strings.TrimRight(manager.config.Issuer, "/") + "/codex/device",
		UserCode:        parsed.UserCode,
		DeviceAuthID:    parsed.DeviceAuthID,
		PollInterval:    interval,
	}, nil
}

// validDeviceUserCode accepts the short alphanumeric RFC 8628 user codes
// (ABCD-1234) that are shown to the user.
func validDeviceUserCode(code string) bool {
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

func parseDevicePollInterval(raw json.RawMessage) (time.Duration, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		seconds, parseErr := strconv.ParseUint(strings.TrimSpace(text), 10, 32)
		if parseErr != nil {
			return 0, parseErr
		}
		return time.Duration(seconds) * time.Second, nil
	}
	var seconds uint64
	if err := json.Unmarshal(raw, &seconds); err != nil {
		return 0, err
	}
	return time.Duration(seconds) * time.Second, nil
}

func (manager *SessionManager) clampDevicePollInterval(interval time.Duration) time.Duration {
	if interval < manager.config.DevicePollMinInterval {
		return manager.config.DevicePollMinInterval
	}
	if interval > manager.config.DevicePollMaxInterval {
		return manager.config.DevicePollMaxInterval
	}
	return interval
}

func (manager *SessionManager) pollDeviceAuthorization(
	ctx context.Context,
	sessionID contract.AuthorizationSessionID,
	device deviceAuthorization,
) {
	for {
		result, pending, err := manager.pollDeviceAuthorizationOnce(ctx, device)
		if err != nil {
			if ctx.Err() == nil {
				manager.failSession(sessionID, &contract.SubscriptionError{
					Code:    ErrCodeDeviceCodePoll,
					Message: "Device Code login failed while waiting for OpenAI",
				})
			}
			return
		}
		if !pending {
			redirectURI := strings.TrimRight(manager.config.Issuer, "/") + "/deviceauth/callback"
			tokens, exchangeErr := manager.tokens.ExchangeCode(
				ctx,
				result.AuthorizationCode,
				result.CodeVerifier,
				redirectURI,
			)
			if exchangeErr != nil {
				if ctx.Err() == nil {
					manager.failSession(sessionID, &contract.SubscriptionError{
						Code:    ErrCodeDeviceCodePoll,
						Message: "Device Code authorization exchange failed",
					})
				}
				return
			}
			if err := manager.completeSession(ctx, sessionID, tokens); err != nil && ctx.Err() == nil {
				manager.failSession(sessionID, &contract.SubscriptionError{
					Code:    ErrCodeStoreUnavailable,
					Message: "failed to persist connected account",
				})
			}
			return
		}
		timer := time.NewTimer(device.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (manager *SessionManager) pollDeviceAuthorizationOnce(
	ctx context.Context,
	device deviceAuthorization,
) (deviceTokenResponse, bool, error) {
	body, err := json.Marshal(map[string]string{
		"device_auth_id": device.DeviceAuthID,
		"user_code":      device.UserCode,
	})
	if err != nil {
		return deviceTokenResponse{}, false, err
	}
	endpoint := strings.TrimRight(manager.config.Issuer, "/") + "/api/accounts/deviceauth/token"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return deviceTokenResponse{}, false, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	ApplyCodexAuthIdentity(request.Header, manager.config.Identities.CodexIdentityFor(ctx, manager.config.ModelsClientVersion))
	request.Header.Set("Accept-Encoding", transport.SupportedResponseEncodings)
	response, err := manager.config.HTTPClient.Do(request)
	if err != nil {
		return deviceTokenResponse{}, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
		return deviceTokenResponse{}, true, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return deviceTokenResponse{}, false, fmt.Errorf("device token endpoint returned status %d", response.StatusCode)
	}
	raw, err := transport.ReadResponseBody(response, 1<<20)
	if err != nil {
		return deviceTokenResponse{}, false, err
	}
	var parsed deviceTokenResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return deviceTokenResponse{}, false, err
	}
	if parsed.AuthorizationCode == "" || parsed.CodeChallenge == "" || parsed.CodeVerifier == "" ||
		len(parsed.AuthorizationCode) > 16_384 ||
		len(parsed.CodeChallenge) > 4096 ||
		len(parsed.CodeVerifier) > 4096 {
		return deviceTokenResponse{}, false, fmt.Errorf("device token response is incomplete")
	}
	return parsed, false, nil
}
