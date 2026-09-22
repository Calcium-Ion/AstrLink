package contract

import (
	"fmt"
	"time"
)

// ChannelStickiness applies only to ordinary session preference. Stateful
// Responses continuation and established WebSocket connections remain strict.
type ChannelStickiness struct {
	Enabled    bool `json:"enabled"`
	TTLSeconds int  `json:"ttl_seconds"`
}

func (settings ChannelStickiness) Validate() error {
	if settings.TTLSeconds < 60 || settings.TTLSeconds > 86400 {
		return fmt.Errorf("ttl_seconds must be between 60 and 86400")
	}
	return nil
}

func (settings *ChannelStickiness) UnmarshalJSON(data []byte) error {
	type document ChannelStickiness
	var value document
	if err := decodePolicy(data, &value, []string{"enabled", "ttl_seconds"}, nil); err != nil {
		return err
	}
	if err := ChannelStickiness(value).Validate(); err != nil {
		return err
	}
	*settings = ChannelStickiness(value)
	return nil
}

type ChannelBindingScope struct {
	SessionID SessionID  `json:"session_id"`
	Principal string     `json:"local_access_token_id"`
	Protocol  ProtocolID `json:"protocol"`
	Model     string     `json:"model"`
}

type ChannelBinding struct {
	ChannelBindingScope
	ServiceID ServiceID `json:"service_id"`
	Source    string    `json:"source"`
	RequestID RequestID `json:"request_id"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Events contain local identities and reason codes, never conversation text or
// raw protocol cursors. An empty scope on released means the whole session.
type ChannelBindingEvent struct {
	ID int64 `json:"id"`
	ChannelBindingScope
	At                time.Time `json:"at"`
	Action            string    `json:"action"`
	Reason            string    `json:"reason"`
	Source            string    `json:"source"`
	ServiceID         ServiceID `json:"service_id"`
	PreviousServiceID ServiceID `json:"previous_service_id"`
	RequestID         RequestID `json:"request_id"`
}

type ChannelBindingAudit struct {
	Enabled  bool                  `json:"enabled"`
	Bindings []ChannelBinding      `json:"bindings"`
	Events   []ChannelBindingEvent `json:"events"`
	HasMore  bool                  `json:"has_more"`
}
