package contract

import (
	"fmt"
	"strings"
	"time"
)

// Sync triggers. A device may sync on a fixed interval, on a cron expression,
// or only when the user asks. Every mode is throttled by the debounce so a
// burst of saves becomes one sync.
const (
	SyncTriggerInterval = "interval"
	SyncTriggerCron     = "cron"
	SyncTriggerManual   = "manual"

	// MinSyncIntervalMinutes is the shortest selectable interval.
	MinSyncIntervalMinutes = 5
	// MinSyncDebounceSeconds is the shortest wait after a save.
	MinSyncDebounceSeconds = 5
	// MaxSyncDebounceSeconds is the longest wait after a save.
	MaxSyncDebounceSeconds = 3600
	// MaxSyncDeviceNameLength bounds what a device calls itself, so a name fits
	// the surfaces that show it.
	MaxSyncDeviceNameLength = 40
)

// SyncRemoteKind names a supported remote store.
const (
	SyncRemoteWebDAV = "webdav"
	SyncRemoteS3     = "s3"
)

// SyncDefaultFolder is the folder a package is written under when a WebDAV
// remote names none. A package at the server root is easy to lose among other
// files and easy to write over, so the settings carry a folder by default.
const SyncDefaultFolder = "astrlink"

// SyncS3Addressing names how an S3 request addresses its bucket. The path style
// keeps the bucket in the URL path and is served by every S3-compatible store;
// the suffix style puts it in the host name, which some stores and the CDNs in
// front of them require. An empty value is the path style.
const (
	SyncS3AddressingPath   = "path"
	SyncS3AddressingSuffix = "suffix"
)

// SyncTriggerSettings says when a device starts a sync.
type SyncTriggerSettings struct {
	Mode            string `json:"mode"`
	IntervalMinutes int    `json:"interval_minutes,omitempty"`
	// Cron is a five- or six-field expression when Mode is SyncTriggerCron.
	Cron string `json:"cron,omitempty"`
	// DebounceSeconds is how long a save waits before a sync, so a burst of
	// edits becomes one package.
	DebounceSeconds int `json:"debounce_seconds,omitempty"`
}

// SyncScope selects which configuration is packaged.
//
// A group is kept whole or left out whole, because its kinds name each other
// by id: a service without its credential, or a price binding without its
// service, would land on a device as a broken reference. Credentials is
// therefore not independent of Services, and Pricing is not independent of
// Services; the engine intersects the two devices' scopes and drops a group
// when either side left out any part of it.
//
// Privacy models are deliberately absent: their files live outside the
// database, so a device installs them itself rather than receiving a row that
// points at a file it does not have.
type SyncScope struct {
	// Services carries each provider: its name, kind, endpoint, models,
	// redirects, and failure policy.
	Services bool `json:"services"`
	// Credentials adds each provider's API key and proxy credentials to the
	// package, sealed under the sync passphrase.
	Credentials bool `json:"credentials"`
	// Policies carries the privacy policy document.
	Policies bool `json:"policies"`
	// Pricing carries each provider's price bindings, budget, and billing day.
	Pricing bool `json:"pricing"`
}

// SyncRemote is where a package is stored. Its credentials are part of the
// configuration and are stripped from every package before it is sealed, so a
// remote never learns where it lives.
type SyncRemote struct {
	Kind      string `json:"kind"`
	Endpoint  string `json:"endpoint,omitempty"`
	Path      string `json:"path,omitempty"`
	Region    string `json:"region,omitempty"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	// Addressing selects how an S3 request names its bucket. It is empty or
	// SyncS3AddressingPath for the path style, and SyncS3AddressingSuffix for
	// the virtual-host style. WebDAV ignores it.
	Addressing string `json:"addressing,omitempty"`
}

// SyncConfig is the user's sync configuration, stored as one row.
type SyncConfig struct {
	Enabled bool                `json:"enabled"`
	Trigger SyncTriggerSettings `json:"trigger"`
	Scope   SyncScope           `json:"scope"`
	Remote  SyncRemote          `json:"remote"`
	// DeviceName is what this device calls itself, shown wherever a device is
	// named. An empty value leaves the name to the host.
	DeviceName string `json:"device_name,omitempty"`
}

// DefaultSyncConfig returns the configuration a fresh device starts with:
// sync off, a one-hour interval, and everything in scope.
func DefaultSyncConfig() SyncConfig {
	return SyncConfig{
		Enabled: false,
		Trigger: SyncTriggerSettings{Mode: SyncTriggerInterval, IntervalMinutes: 60, DebounceSeconds: 30},
		Scope:   SyncScope{Services: true, Credentials: true, Policies: true, Pricing: true},
		Remote:  SyncRemote{Path: SyncDefaultFolder, Addressing: SyncS3AddressingPath},
	}
}

// ValidateSyncConfig refuses a configuration the engine cannot honour.
func ValidateSyncConfig(config SyncConfig) error {
	switch config.Trigger.Mode {
	case SyncTriggerInterval:
		if config.Trigger.IntervalMinutes < MinSyncIntervalMinutes {
			return fmt.Errorf("sync interval must be at least %d minutes", MinSyncIntervalMinutes)
		}
	case SyncTriggerCron:
		if config.Trigger.Cron == "" {
			return fmt.Errorf("sync cron expression is required")
		}
	case SyncTriggerManual:
	default:
		return fmt.Errorf("sync trigger mode %q is unsupported", config.Trigger.Mode)
	}
	debounce := config.Trigger.DebounceSeconds
	if debounce != 0 && (debounce < MinSyncDebounceSeconds || debounce > MaxSyncDebounceSeconds) {
		return fmt.Errorf("sync debounce must be between %d and %d seconds", MinSyncDebounceSeconds, MaxSyncDebounceSeconds)
	}
	switch config.Remote.Kind {
	case SyncRemoteWebDAV:
		if config.Remote.Endpoint == "" {
			return fmt.Errorf("a WebDAV remote needs an endpoint")
		}
		if strings.TrimSpace(config.Remote.Path) == "" {
			return fmt.Errorf("a WebDAV remote needs a folder")
		}
	case SyncRemoteS3:
		if config.Remote.Endpoint == "" || config.Remote.Path == "" {
			return fmt.Errorf("an S3 remote needs an endpoint and a bucket")
		}
		switch config.Remote.Addressing {
		case "", SyncS3AddressingPath, SyncS3AddressingSuffix:
		default:
			return fmt.Errorf("sync S3 addressing %q is unsupported", config.Remote.Addressing)
		}
	case "":
		if config.Enabled {
			return fmt.Errorf("sync is enabled without a remote")
		}
	default:
		return fmt.Errorf("sync remote kind %q is unsupported", config.Remote.Kind)
	}
	if len([]rune(config.DeviceName)) > MaxSyncDeviceNameLength {
		return fmt.Errorf("sync device name must be at most %d characters", MaxSyncDeviceNameLength)
	}
	return nil
}

// SyncConflictNotice describes one entity two devices changed, kept so the UI
// can show what was merged and offer the losing side back.
type SyncConflictNotice struct {
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Resolution string    `json:"resolution"`
	At         time.Time `json:"at"`
}

// SyncStatus is the read model the desktop shows for sync.
type SyncStatus struct {
	Enabled          bool `json:"enabled"`
	PassphraseSet    bool `json:"passphrase_set"`
	RemoteConfigured bool `json:"remote_configured"`
	Configured       bool `json:"configured"`
	Running          bool `json:"running"`
	// DeviceID is this device's identity in the remote lock. It is opaque and
	// stays out of the interface, which shows DeviceName.
	DeviceID string `json:"device_id"`
	// DeviceName is what this device calls itself, which is the configured name
	// or the host's.
	DeviceName string `json:"device_name"`
	// HostName is the host's own name, shown as the default a blank device name
	// falls back to.
	HostName string `json:"host_name"`
	// PendingChanges counts configuration writes not yet packaged.
	PendingChanges int        `json:"pending_changes"`
	LastSyncedAt   *time.Time `json:"last_synced_at,omitempty"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	// Conflicts lists recent merges of concurrent edits.
	Conflicts []SyncConflictNotice `json:"conflicts"`
}

// RemoteIdentity is what a remote returns when a package is stored there: the
// store's identity, the entity tag of the stored object, and the revision it
// carries. The engine pins the identity it confirmed, so a response claiming a
// different store is refused and remote_etag alone is never trusted.
type RemoteIdentity struct {
	Identity string
	ETag     string
	Revision int64
}
