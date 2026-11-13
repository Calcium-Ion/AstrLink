package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Limits of a plugin's own values (astrlink.store).
const (
	MaxPluginValueBytes = 64 << 10
	MaxPluginValuesSize = 1 << 20
)

// PluginSettingsRecord is the user's choices for one plugin.
type PluginSettingsRecord struct {
	Key     string
	Enabled bool
	// Capabilities are the ones the user granted.
	Capabilities []string
	// Origins are the addresses the plugin may reach over the network.
	Origins []PluginOrigin
	// Trusted skips the privacy check of what the host gives the plugin.
	Trusted bool
	// TurnWaitMs is how long a request waits for the plugin's prepare
	// hooks; 0 stores the default.
	TurnWaitMs int
	Config     json.RawMessage
	// ExcludedClients are the client types the plugin is off for.
	ExcludedClients []string
	// WorkspaceBinding binds a new session to the value its workspace had.
	WorkspaceBinding bool
	// AutoRefresh swaps a session's resident block when newer data
	// changes it.
	AutoRefresh bool
	// LookupEnabled lets the model run the plugin's queries.
	LookupEnabled bool
	// HookLimitsMs are the user's time limits of extension points, by hook.
	HookLimitsMs map[string]int
	// FailClosed is the user's consent that a request the plugin's entry
	// hook cannot decide is refused.
	FailClosed bool
	// UIEnabled lets the plugin's interface code load.
	UIEnabled bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewPluginSettingsRecord returns the settings of a plugin the user changed
// nothing for yet.
func NewPluginSettingsRecord(key string) PluginSettingsRecord {
	return PluginSettingsRecord{Key: key, WorkspaceBinding: true, LookupEnabled: true, UIEnabled: true}
}

// PluginActivity is what the plugins' statistics read of a request record:
// when it started, its session, its token usage, its metadata and the
// plugins' records. A part the record lacks or holds in a shape it cannot
// read is nil.
type PluginActivity struct {
	StartedAt time.Time
	SessionID string
	Usage     *contract.Usage
	Metadata  *contract.RequestMetadata
	Records   []contract.RequestPluginRecord
}

// PluginSessionRecord is a plugin's part in one session: the turn it first
// took part in and whether the user paused it there.
type PluginSessionRecord struct {
	Plugin    string
	Session   string
	FirstTurn int
	Paused    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PluginOrigin is an address a plugin may reach: scheme, host and port.
type PluginOrigin struct {
	Origin string `json:"origin"`
	// AllowPlaintext allows plain http to a host other than this machine.
	AllowPlaintext bool `json:"allow_plaintext,omitempty"`
	// Setting names the server setting the origin comes from.
	Setting string `json:"setting,omitempty"`
}

// PluginCredentialRecord describes a stored credential without its value.
type PluginCredentialRecord struct {
	Plugin string
	Name   string
	// Origin is the one address the host injects the credential for.
	Origin string
	// Hint is the end of the value, for the user to recognize it.
	Hint      string
	UpdatedAt time.Time
}

type PluginStore interface {
	// ListPluginSettings returns the plugins the user changed something
	// for, by key.
	ListPluginSettings(context.Context) ([]PluginSettingsRecord, error)
	// GetPluginSettings returns ErrNotFound for a plugin without settings.
	GetPluginSettings(ctx context.Context, plugin string) (PluginSettingsRecord, error)
	PutPluginSettings(context.Context, PluginSettingsRecord) error

	ListPluginCredentials(ctx context.Context, plugin string) ([]PluginCredentialRecord, error)
	// PutPluginCredential stores secret, sealed, bound to origin. It creates
	// the plugin's settings when it has none.
	PutPluginCredential(ctx context.Context, plugin, name, origin string, secret []byte) error
	// BindPluginCredential sends a stored credential to another origin;
	// ErrNotFound when nothing is stored.
	BindPluginCredential(ctx context.Context, plugin, name, origin string) error
	// DeletePluginCredential returns ErrNotFound when nothing was stored.
	DeletePluginCredential(ctx context.Context, plugin, name string) error
	// PluginCredential returns the secret and its origin, ErrNotFound when
	// none is stored. The caller clears the secret after use.
	PluginCredential(ctx context.Context, plugin, name string) (secret []byte, origin string, err error)

	// PluginHostSettings returns the settings of every plugin at once, the
	// defaults when nobody changed them.
	PluginHostSettings(context.Context) (contract.PluginHostSettings, error)
	PutPluginHostSettings(context.Context, contract.PluginHostSettings) error

	// PluginSession returns found false for a session the plugin has not
	// taken part in.
	PluginSession(ctx context.Context, plugin, session string) (record PluginSessionRecord, found bool, err error)
	// StartPluginSession records that the plugin took part in the session
	// from turn on, and returns the record as kept: one kept before stays.
	StartPluginSession(ctx context.Context, plugin, session string, turn int) (PluginSessionRecord, error)
	// PausePluginSession pauses or resumes the plugin in the session. A
	// session the plugin has not taken part in starts at turn.
	PausePluginSession(ctx context.Context, plugin, session string, paused bool, turn int) (PluginSessionRecord, error)
	// CountPluginSessions counts the sessions the plugin took part in.
	CountPluginSessions(ctx context.Context, plugin string) (int, error)
	// ScanPluginActivity visits the root request records started since,
	// oldest first, with what the plugins' statistics read of them.
	ScanPluginActivity(ctx context.Context, since time.Time, visit func(PluginActivity) error) error

	// GetPluginValue returns found false for a key without a value.
	GetPluginValue(ctx context.Context, plugin, key string) (value json.RawMessage, found bool, err error)
	// PutPluginValue returns ErrLimitReached when the value or the plugin's
	// values would pass their limits.
	PutPluginValue(ctx context.Context, plugin, key string, value json.RawMessage) error
	DeletePluginValue(ctx context.Context, plugin, key string) error
	PluginValueKeys(ctx context.Context, plugin, prefix string) ([]string, error)
	// DeletePluginValues removes every value of the plugin and returns how
	// many it removed.
	DeletePluginValues(ctx context.Context, plugin string) (int, error)
}
