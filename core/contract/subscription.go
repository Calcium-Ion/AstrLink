package contract

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	subscriptionErrorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
	credentialLeakPattern        = regexp.MustCompile(
		`(?i)(Bearer\s+[A-Za-z0-9._~+/=-]{12,}|` +
			`(access_token|refresh_token|id_token|device_auth_id|code_verifier|authorization_code)["']?\s*[:=]\s*["']?[A-Za-z0-9._~+/=-]{8,}|` +
			`eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,})`,
	)
)

func containsCredentialLeak(value string) bool {
	return credentialLeakPattern.MatchString(value)
}

// SubscriptionAccountID remains a compatibility view over the canonical
// ServiceID. Subscription accounts are Service instances, not a second
// identity namespace.
type SubscriptionAccountID = ServiceID

// SubscriptionProvider enumerates supported subscription providers.
// Alpha ships only openai_codex; Claude/Gemini remain Post-Alpha.
type SubscriptionProvider string

const (
	SubscriptionProviderOpenAICodex SubscriptionProvider = "openai_codex"
)

func (provider SubscriptionProvider) Valid() bool {
	return provider == SubscriptionProviderOpenAICodex
}

// SubscriptionStatus is the non-sensitive authorization lifecycle state.
type SubscriptionStatus string

const (
	SubscriptionStatusDisconnected SubscriptionStatus = "disconnected"
	SubscriptionStatusAuthorizing  SubscriptionStatus = "authorizing"
	SubscriptionStatusConnected    SubscriptionStatus = "connected"
	SubscriptionStatusNeedsReauth  SubscriptionStatus = "needs_reauth"
	SubscriptionStatusError        SubscriptionStatus = "error"
)

func (status SubscriptionStatus) Valid() bool {
	switch status {
	case SubscriptionStatusDisconnected, SubscriptionStatusAuthorizing,
		SubscriptionStatusConnected, SubscriptionStatusNeedsReauth,
		SubscriptionStatusError:
		return true
	default:
		return false
	}
}

// SubscriptionError is a sanitized, non-secret failure summary.
type SubscriptionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (err SubscriptionError) Validate() error {
	if strings.TrimSpace(err.Code) == "" || len(err.Code) > 64 {
		return fmt.Errorf("subscription error code is required and must be at most 64 bytes")
	}
	if !subscriptionErrorCodePattern.MatchString(err.Code) {
		return fmt.Errorf("subscription error code %q is invalid", err.Code)
	}
	if strings.TrimSpace(err.Message) == "" || utf8.RuneCountInString(err.Message) > 240 {
		return fmt.Errorf("subscription error message is required and must be at most 240 characters")
	}
	if containsCredentialLeak(err.Message) {
		return fmt.Errorf("subscription error message must not contain credential material")
	}
	return nil
}

// SubscriptionAccount is the public, non-secret subscription connection state.
// Access/refresh tokens never appear on this document.
type SubscriptionAccount struct {
	ID                SubscriptionAccountID `json:"id"`
	Provider          SubscriptionProvider  `json:"provider"`
	Status            SubscriptionStatus    `json:"status"`
	DisplayName       string                `json:"display_name"`
	AccountHint       string                `json:"account_hint,omitempty"`
	ProviderAccountID string                `json:"provider_account_id,omitempty"`
	CredentialRef     string                `json:"credential_ref,omitempty"`
	Capabilities      []Capability          `json:"capabilities"`
	// AuthorizationBoundary explains provider-controlled limits that affect
	// whether interactive login can proceed. It never contains secrets.
	AuthorizationBoundary string             `json:"authorization_boundary,omitempty"`
	TokenExpiresAt        *time.Time         `json:"token_expires_at,omitempty"`
	LastRefreshAt         *time.Time         `json:"last_refresh_at,omitempty"`
	LastError             *SubscriptionError `json:"last_error,omitempty"`
	CreatedAt             time.Time          `json:"created_at"`
	UpdatedAt             time.Time          `json:"updated_at"`
}

func (account SubscriptionAccount) Validate() error {
	if err := account.ID.Validate(); err != nil {
		return err
	}
	if !account.Provider.Valid() {
		return fmt.Errorf("unknown subscription provider %q", account.Provider)
	}
	if !account.Status.Valid() {
		return fmt.Errorf("unknown subscription status %q", account.Status)
	}
	if strings.TrimSpace(account.DisplayName) == "" || utf8.RuneCountInString(account.DisplayName) > 64 {
		return fmt.Errorf("display_name is required and must be at most 64 characters")
	}
	if account.AccountHint != "" {
		if utf8.RuneCountInString(account.AccountHint) > 128 {
			return fmt.Errorf("account_hint must be at most 128 characters")
		}
		if containsCredentialLeak(account.AccountHint) {
			return fmt.Errorf("account_hint must not contain credential material")
		}
	}
	if utf8.RuneCountInString(account.ProviderAccountID) > 256 {
		return fmt.Errorf("provider_account_id must be at most 256 characters")
	}
	if containsCredentialLeak(account.ProviderAccountID) {
		return fmt.Errorf("provider_account_id must not contain credential material")
	}
	switch account.Status {
	case SubscriptionStatusConnected, SubscriptionStatusNeedsReauth, SubscriptionStatusAuthorizing:
		if account.Status == SubscriptionStatusAuthorizing && account.CredentialRef == "" {
			break
		}
		if err := ValidateCredentialRef(account.CredentialRef); err != nil {
			return fmt.Errorf("connected subscription requires credential_ref: %w", err)
		}
		if !strings.HasPrefix(account.CredentialRef, "keyring://") {
			return fmt.Errorf("subscription credential_ref must use keyring://")
		}
	default:
		if account.CredentialRef != "" {
			return fmt.Errorf("credential_ref is only valid for connected or needs_reauth accounts")
		}
	}
	if len(account.Capabilities) == 0 {
		return fmt.Errorf("subscription capabilities are required")
	}
	for index, capability := range account.Capabilities {
		if err := capability.Validate(); err != nil {
			return fmt.Errorf("capabilities[%d]: %w", index, err)
		}
	}
	if account.AuthorizationBoundary != "" {
		if utf8.RuneCountInString(account.AuthorizationBoundary) > 240 {
			return fmt.Errorf("authorization_boundary must be at most 240 characters")
		}
		if containsCredentialLeak(account.AuthorizationBoundary) {
			return fmt.Errorf("authorization_boundary must not contain credential material")
		}
	}
	if account.LastError != nil {
		if err := account.LastError.Validate(); err != nil {
			return err
		}
	}
	if account.CreatedAt.IsZero() || account.UpdatedAt.IsZero() {
		return fmt.Errorf("created_at and updated_at are required")
	}
	if account.UpdatedAt.Before(account.CreatedAt) {
		return fmt.Errorf("updated_at must not precede created_at")
	}
	return nil
}

// DefaultOpenAICodexCapabilities is the fixed native capability set for Codex.
func DefaultOpenAICodexCapabilities() []Capability {
	return []Capability{
		{
			Protocol:  ProtocolOpenAIResponses,
			Mode:      CapabilityModeNative,
			Streaming: true,
		},
		{
			Protocol:  ProtocolOpenAIResponsesCompact,
			Mode:      CapabilityModeNative,
			Streaming: false,
		},
		{
			Protocol:  ProtocolOpenAIModels,
			Mode:      CapabilityModeNative,
			Streaming: false,
		},
	}
}

// AuthorizationSessionID identifies a short-lived interactive login attempt.
type AuthorizationSessionID string

func (id AuthorizationSessionID) Validate() error {
	return validateResourceID("authorization_session", string(id))
}

// AuthorizationSessionStatus is the public view of an in-flight login.
type AuthorizationSessionStatus string

const (
	AuthorizationSessionStatusPending   AuthorizationSessionStatus = "pending"
	AuthorizationSessionStatusCompleted AuthorizationSessionStatus = "completed"
	AuthorizationSessionStatusCancelled AuthorizationSessionStatus = "cancelled"
	AuthorizationSessionStatusExpired   AuthorizationSessionStatus = "expired"
	AuthorizationSessionStatusFailed    AuthorizationSessionStatus = "failed"
)

func (status AuthorizationSessionStatus) Valid() bool {
	switch status {
	case AuthorizationSessionStatusPending, AuthorizationSessionStatusCompleted,
		AuthorizationSessionStatusCancelled, AuthorizationSessionStatusExpired,
		AuthorizationSessionStatusFailed:
		return true
	default:
		return false
	}
}

// AuthorizationFlow selects the interactive login transport for a Codex
// subscription service.
type AuthorizationFlow string

const (
	AuthorizationFlowBrowser    AuthorizationFlow = "browser"
	AuthorizationFlowDeviceCode AuthorizationFlow = "device_code"
)

func (flow AuthorizationFlow) Valid() bool {
	return flow == AuthorizationFlowBrowser || flow == AuthorizationFlowDeviceCode
}

// AuthorizationDeviceCode contains the non-secret information a user needs to
// finish a device-code login. The provider's device_auth_id is never exposed.
type AuthorizationDeviceCode struct {
	VerificationURL string `json:"verification_url"`
	UserCode        string `json:"user_code"`
}

func (device AuthorizationDeviceCode) Validate() error {
	if err := validateAuthorizationURL(device.VerificationURL, "verification_url"); err != nil {
		return err
	}
	code := strings.TrimSpace(device.UserCode)
	if code == "" || utf8.RuneCountInString(code) > 128 {
		return fmt.Errorf("user_code is required and must be at most 128 characters")
	}
	if strings.ContainsAny(code, "\r\n\x00") {
		return fmt.Errorf("user_code contains invalid characters")
	}
	return nil
}

// AuthorizationSession is returned by control operations. It never includes
// tokens, authorization codes, verifiers, device_auth_id, or raw provider
// callbacks. A short-lived user_code is intentionally returned only while a
// device-code session is pending.
type AuthorizationSession struct {
	ID               AuthorizationSessionID     `json:"id"`
	Provider         SubscriptionProvider       `json:"provider"`
	Status           AuthorizationSessionStatus `json:"status"`
	Flow             AuthorizationFlow          `json:"flow"`
	AuthorizationURL string                     `json:"authorization_url,omitempty"`
	DeviceCode       *AuthorizationDeviceCode   `json:"device_code,omitempty"`
	ServiceID        ServiceID                  `json:"service_id"`
	ExpiresAt        time.Time                  `json:"expires_at"`
	Error            *SubscriptionError         `json:"error,omitempty"`
	CreatedAt        time.Time                  `json:"created_at"`
	UpdatedAt        time.Time                  `json:"updated_at"`
}

func (session AuthorizationSession) Validate() error {
	if err := session.ID.Validate(); err != nil {
		return err
	}
	if !session.Provider.Valid() {
		return fmt.Errorf("unknown subscription provider %q", session.Provider)
	}
	if !session.Status.Valid() {
		return fmt.Errorf("unknown authorization session status %q", session.Status)
	}
	if !session.Flow.Valid() {
		return fmt.Errorf("unknown authorization flow %q", session.Flow)
	}
	if session.Status == AuthorizationSessionStatusPending {
		switch session.Flow {
		case AuthorizationFlowBrowser:
			if session.DeviceCode != nil {
				return fmt.Errorf("browser authorization session must not include device_code")
			}
			if err := validateAuthorizationURL(session.AuthorizationURL, "authorization_url"); err != nil {
				return err
			}
		case AuthorizationFlowDeviceCode:
			if session.AuthorizationURL != "" {
				return fmt.Errorf("device-code authorization session must not include authorization_url")
			}
			if session.DeviceCode == nil {
				return fmt.Errorf("pending device-code authorization session requires device_code")
			}
			if err := session.DeviceCode.Validate(); err != nil {
				return err
			}
		}
	} else if session.AuthorizationURL != "" || session.DeviceCode != nil {
		return fmt.Errorf("terminal authorization session must not include login instructions")
	}
	if err := session.ServiceID.Validate(); err != nil {
		return fmt.Errorf("service_id: %w", err)
	}
	if session.Error != nil {
		if err := session.Error.Validate(); err != nil {
			return err
		}
	}
	if session.CreatedAt.IsZero() || session.UpdatedAt.IsZero() || session.ExpiresAt.IsZero() {
		return fmt.Errorf("authorization session timestamps are required")
	}
	return nil
}

func validateAuthorizationURL(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s is invalid", field)
	}
	switch parsed.Scheme {
	case "https":
		if parsed.Host == "" {
			return fmt.Errorf("%s must be an absolute https URL", field)
		}
		return nil
	case "http":
		host := strings.ToLower(parsed.Hostname())
		if host != "127.0.0.1" && host != "localhost" {
			return fmt.Errorf("%s http is only allowed on loopback test issuers", field)
		}
		return nil
	default:
		return fmt.Errorf("%s must use https", field)
	}
}
