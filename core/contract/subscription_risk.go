package contract

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// SubscriptionRiskState describes how an upstream risk signal affects
// scheduling. Suspended accounts stay paused until the user restores them;
// cooling accounts resume automatically at PausedUntil.
type SubscriptionRiskState string

const (
	SubscriptionRiskSuspended SubscriptionRiskState = "suspended"
	SubscriptionRiskCooling   SubscriptionRiskState = "cooling"
)

func (state SubscriptionRiskState) Valid() bool {
	return state == SubscriptionRiskSuspended || state == SubscriptionRiskCooling
}

// Risk codes recorded for subscription accounts. Desktop localizes by code.
const (
	RiskCodeOrganizationDisabled         = "organization_disabled"
	RiskCodeOAuthNotAllowed              = "oauth_not_allowed"
	RiskCodeIdentityVerificationRequired = "identity_verification_required"
	RiskCodeCreditBalanceLow             = "credit_balance_low"
	RiskCodeAccountDeactivated           = "account_deactivated"
	RiskCodeRepeatedForbidden            = "repeated_forbidden"
	RiskCodeForbidden                    = "forbidden"
	RiskCodeClientIdentityRejected       = "client_identity_rejected"
	RiskCodeRateLimit5h                  = "rate_limit_5h"
	RiskCodeRateLimit7d                  = "rate_limit_7d"
	RiskCodeUsageLimitReached            = "usage_limit_reached"
)

// SubscriptionRisk is the current, non-secret upstream risk state of a
// subscription account. It is independent from the authorization Status so
// pausing never discards credentials.
type SubscriptionRisk struct {
	State       SubscriptionRiskState `json:"state"`
	Code        string                `json:"code"`
	Message     string                `json:"message,omitempty"`
	HTTPStatus  int                   `json:"http_status,omitempty"`
	ObservedAt  time.Time             `json:"observed_at"`
	PausedUntil *time.Time            `json:"paused_until,omitempty"`
	Occurrences int                   `json:"occurrences,omitempty"`
}

func (risk SubscriptionRisk) Validate() error {
	if !risk.State.Valid() {
		return fmt.Errorf("unknown subscription risk state %q", risk.State)
	}
	if err := validateRiskText(risk.Code, risk.Message); err != nil {
		return err
	}
	if risk.HTTPStatus != 0 && (risk.HTTPStatus < 100 || risk.HTTPStatus > 599) {
		return fmt.Errorf("subscription risk http_status must be an HTTP status")
	}
	if risk.ObservedAt.IsZero() {
		return fmt.Errorf("subscription risk observed_at is required")
	}
	switch risk.State {
	case SubscriptionRiskCooling:
		if risk.PausedUntil == nil || risk.PausedUntil.IsZero() {
			return fmt.Errorf("cooling subscription risk requires paused_until")
		}
	case SubscriptionRiskSuspended:
		if risk.PausedUntil != nil {
			return fmt.Errorf("suspended subscription risk must not set paused_until")
		}
	}
	if risk.Occurrences < 0 {
		return fmt.Errorf("subscription risk occurrences must not be negative")
	}
	return nil
}

// Blocks reports whether the account must be kept out of scheduling at now.
func (risk *SubscriptionRisk) Blocks(now time.Time) bool {
	if risk == nil {
		return false
	}
	if risk.State == SubscriptionRiskSuspended {
		return true
	}
	return risk.PausedUntil != nil && now.Before(*risk.PausedUntil)
}

// Expired reports whether a cooling risk has passed its pause window.
func (risk *SubscriptionRisk) Expired(now time.Time) bool {
	return risk != nil && risk.State == SubscriptionRiskCooling && !risk.Blocks(now)
}

// SubscriptionRiskObservation is one classified upstream signal reported by the
// inference plane. Forbidden marks 403 responses that count toward escalation.
type SubscriptionRiskObservation struct {
	State       SubscriptionRiskState
	Code        string
	Message     string
	HTTPStatus  int
	PausedUntil *time.Time
	Forbidden   bool
}

// SubscriptionRiskEventKind records what happened to an account's risk state.
type SubscriptionRiskEventKind string

const (
	SubscriptionRiskEventSuspended SubscriptionRiskEventKind = "suspended"
	SubscriptionRiskEventCooling   SubscriptionRiskEventKind = "cooling"
	SubscriptionRiskEventCleared   SubscriptionRiskEventKind = "cleared"
)

func (kind SubscriptionRiskEventKind) Valid() bool {
	switch kind {
	case SubscriptionRiskEventSuspended, SubscriptionRiskEventCooling, SubscriptionRiskEventCleared:
		return true
	default:
		return false
	}
}

// SubscriptionRiskEvent is one persisted entry of an account's risk history.
type SubscriptionRiskEvent struct {
	ID          int64                     `json:"id"`
	ServiceID   ServiceID                 `json:"service_id"`
	Kind        SubscriptionRiskEventKind `json:"kind"`
	Code        string                    `json:"code,omitempty"`
	Message     string                    `json:"message,omitempty"`
	HTTPStatus  int                       `json:"http_status,omitempty"`
	ObservedAt  time.Time                 `json:"observed_at"`
	PausedUntil *time.Time                `json:"paused_until,omitempty"`
}

func (event SubscriptionRiskEvent) Validate() error {
	if err := event.ServiceID.Validate(); err != nil {
		return err
	}
	if !event.Kind.Valid() {
		return fmt.Errorf("unknown subscription risk event kind %q", event.Kind)
	}
	if event.Kind != SubscriptionRiskEventCleared || event.Code != "" {
		if err := validateRiskText(event.Code, event.Message); err != nil {
			return err
		}
	}
	if event.HTTPStatus != 0 && (event.HTTPStatus < 100 || event.HTTPStatus > 599) {
		return fmt.Errorf("subscription risk event http_status must be an HTTP status")
	}
	if event.ObservedAt.IsZero() {
		return fmt.Errorf("subscription risk event observed_at is required")
	}
	return nil
}

func validateRiskText(code, message string) error {
	if !subscriptionErrorCodePattern.MatchString(code) {
		return fmt.Errorf("subscription risk code %q is invalid", code)
	}
	if utf8.RuneCountInString(message) > 240 {
		return fmt.Errorf("subscription risk message must be at most 240 characters")
	}
	if containsCredentialLeak(message) {
		return fmt.Errorf("subscription risk message must not contain credential material")
	}
	return nil
}

// SanitizeSubscriptionRiskMessage turns an upstream error excerpt into a
// single-line message that fits SubscriptionRisk. Anything resembling
// credential material is dropped entirely.
func SanitizeSubscriptionRiskMessage(value string) string {
	value = strings.Join(strings.Fields(strings.ToValidUTF8(value, "")), " ")
	if containsCredentialLeak(value) {
		return ""
	}
	if utf8.RuneCountInString(value) > 240 {
		runes := []rune(value)
		value = strings.TrimSpace(string(runes[:239])) + "…"
	}
	return value
}
