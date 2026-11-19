package contract

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	pluginKeyPattern            = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	nativePluginKeyPattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	pluginCredentialNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// ValidPluginKey reports whether key can name a plugin: a lowercase letter
// or digit, then up to 63 lowercase letters, digits, hyphens and
// underscores, as new-api's keys are; a community plugin's key starts with
// its author's GitHub name and an underscore, like alice_foo. It is also
// the name of the plugin's directory.
func ValidPluginKey(key string) bool { return pluginKeyPattern.MatchString(key) }

// ValidNativePluginKey reports whether key can name a plugin written for
// AstrLink's own plugin API: a lowercase letter, then up to 31 lowercase
// letters, digits and hyphens.
func ValidNativePluginKey(key string) bool { return nativePluginKeyPattern.MatchString(key) }

// ValidPluginCredentialName reports whether name can name a credential slot
// of a plugin: a lowercase letter, then up to 31 lowercase letters, digits
// and underscores.
func ValidPluginCredentialName(name string) bool {
	return pluginCredentialNamePattern.MatchString(name)
}

const pluginCredentialRefPrefix = "local://plugin/"

// PluginCredentialRef is where the credential store keeps a plugin's
// credential.
func PluginCredentialRef(plugin, name string) string {
	return pluginCredentialRefPrefix + plugin + "/" + name
}

func validPluginCredentialRef(value string) bool {
	plugin, name, ok := strings.Cut(strings.TrimPrefix(value, pluginCredentialRefPrefix), "/")
	return strings.HasPrefix(value, pluginCredentialRefPrefix) && ok && ValidPluginKey(plugin) && ValidPluginCredentialName(name)
}

// Limits of the settings the plugin page saves.
const (
	MaxPluginConfigBytes  = 64 << 10
	MaxPluginOrigins      = 16
	MaxPluginSecretBytes  = 16384
	MaxPluginOriginLength = 512
)

// Bounds of how long a request waits for a plugin's prepare hooks, the
// turn's privacy check included.
const (
	DefaultPluginTurnWaitMs = 2500
	MinPluginTurnWaitMs     = 1000
	MaxPluginTurnWaitMs     = 5000
)

// Bounds of what all plugins together inject into one request, in bytes.
const (
	DefaultPluginInjectionBudgetBytes = 16 << 10
	MinPluginInjectionBudgetBytes     = 4 << 10
	MaxPluginInjectionBudgetBytes     = 64 << 10
)

// Interface languages, which what plugins inject follows. They are the
// desktop's locales.
const (
	PluginLanguageEnglish  = "en"
	PluginLanguageChinese  = "zh-CN"
	PluginLanguageJapanese = "ja"
	// DefaultPluginLanguage is the desktop's default locale, for a Core the
	// desktop has not told its language yet.
	DefaultPluginLanguage = PluginLanguageEnglish
)

// ValidPluginLanguage reports whether language is an interface language.
func ValidPluginLanguage(language string) bool {
	switch language {
	case PluginLanguageEnglish, PluginLanguageChinese, PluginLanguageJapanese:
		return true
	}
	return false
}

// PluginHostSettings are the settings of every plugin at once.
type PluginHostSettings struct {
	// Paused keeps every plugin out of requests, which then go out as they
	// came.
	Paused bool `json:"paused"`
	// InjectionBudgetBytes bounds what all plugins inject into one request
	// together. Past it, the blocks of the plugins later in key order stay
	// out.
	InjectionBudgetBytes int `json:"injection_budget_bytes"`
	// Language is the interface language, en, zh-CN or ja. A session takes
	// it when it starts and keeps it.
	Language string `json:"language"`
	// Order lists plugin keys in the order their extension points run:
	// each plugin gets what the one before it returned. Plugins it does not
	// list come after, in key order.
	Order []string `json:"order,omitempty"`
	// WebUI lets the web console load the interface code of plugins, which
	// it does not by default: it has no confirmation window of its own.
	WebUI bool `json:"web_ui,omitempty"`
	// NpmRegistry is where an install from source fetches the packages a
	// lock file pins, "" for the npm registry; npmmirror is tried when it
	// fails. The lock file's hashes check whatever it returns.
	NpmRegistry string `json:"npm_registry,omitempty"`
	// RequestPlugins are the instances of new-api request processing
	// plugins, in the order they run.
	RequestPlugins []RequestPluginInstance `json:"request_plugins,omitempty"`
}

// The npm registries an install from source fetches packages from.
const (
	DefaultNpmRegistry  = "https://registry.npmjs.org"
	FallbackNpmRegistry = "https://registry.npmmirror.com"
)

// MaxPluginOrder bounds the plugins Order lists.
const MaxPluginOrder = 64

// DefaultPluginHostSettings are the settings of a Core nobody changed them in.
func DefaultPluginHostSettings() PluginHostSettings {
	return PluginHostSettings{InjectionBudgetBytes: DefaultPluginInjectionBudgetBytes, Language: DefaultPluginLanguage}
}

func (settings PluginHostSettings) Validate() error {
	if settings.InjectionBudgetBytes < MinPluginInjectionBudgetBytes || settings.InjectionBudgetBytes > MaxPluginInjectionBudgetBytes {
		return fmt.Errorf("injection_budget_bytes must be %d to %d", MinPluginInjectionBudgetBytes, MaxPluginInjectionBudgetBytes)
	}
	if !ValidPluginLanguage(settings.Language) {
		return fmt.Errorf("language must be en, zh-CN or ja")
	}
	if len(settings.Order) > MaxPluginOrder {
		return fmt.Errorf("order lists at most %d plugins", MaxPluginOrder)
	}
	seen := make(map[string]bool, len(settings.Order))
	for _, key := range settings.Order {
		if !ValidPluginKey(key) || seen[key] {
			return fmt.Errorf("order must list distinct plugin keys")
		}
		seen[key] = true
	}
	if settings.NpmRegistry != "" && !validRegistry(settings.NpmRegistry) {
		return fmt.Errorf("npm_registry must be an https address without credentials, query or fragment")
	}
	if len(settings.RequestPlugins) > MaxRequestPluginInstances {
		return fmt.Errorf("request_plugins lists at most %d instances", MaxRequestPluginInstances)
	}
	ids := map[string]bool{}
	for _, instance := range settings.RequestPlugins {
		if err := instance.Validate(); err != nil {
			return err
		}
		if ids[instance.ID] {
			return fmt.Errorf("request_plugins lists the instance %s twice", instance.ID)
		}
		ids[instance.ID] = true
	}
	return nil
}

// validRegistry reports whether text can name an npm registry: https, a
// host, no credentials, query or fragment.
func validRegistry(text string) bool {
	if len(text) > 256 {
		return false
	}
	parsed, err := url.Parse(text)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil &&
		parsed.RawQuery == "" && parsed.Fragment == "" && !parsed.ForceQuery
}

// PluginHostSettingsUpdate changes what is set and keeps what is absent.
type PluginHostSettingsUpdate struct {
	Paused               *bool     `json:"paused,omitempty"`
	InjectionBudgetBytes *int      `json:"injection_budget_bytes,omitempty"`
	Language             *string   `json:"language,omitempty"`
	Order                *[]string `json:"order,omitempty"`
	WebUI                *bool     `json:"web_ui,omitempty"`
	NpmRegistry          *string   `json:"npm_registry,omitempty"`
	// RequestPlugins replaces every request processing instance; one
	// without an id is new and gets one.
	RequestPlugins *[]RequestPluginInstance `json:"request_plugins,omitempty"`
}

// Extension points of apiVersion 2 plugins, by the hook that serves them.
const (
	PluginHookOnRequest       = "onRequest"
	PluginHookRoute           = "route"
	PluginHookBeforeForward   = "beforeForward"
	PluginHookBeforeUpstream  = "beforeUpstream"
	PluginHookRewriteResponse = "rewriteResponse"
	PluginHookOnStreamEvent   = "onStreamEvent"
	PluginHookOnResponse      = "onResponse"
	PluginHookObserveTurn     = "observeTurn"
	PluginHookOnEvent         = "onEvent"
	PluginHookOnSchedule      = "onSchedule"
	PluginHookOnHTTP          = "onHttp"
	PluginHookOnUICall        = "onUiCall"
	PluginHookProvider        = "provider"
	PluginHookProviderEvent   = "providerEvent"
)

// PluginHookLimit bounds how long a request waits for one call of a hook:
// the default and the most a plugin or the user may set, in milliseconds.
type PluginHookLimit struct {
	DefaultMs int `json:"default_ms"`
	MaxMs     int `json:"max_ms"`
}

// PluginHookLimits are the time limits of the extension points. A stream
// event's limit is each event's; providerEvent is each event of a
// provider's stream.
var PluginHookLimits = map[string]PluginHookLimit{
	PluginHookOnRequest:       {DefaultMs: 1000, MaxMs: 5000},
	PluginHookRoute:           {DefaultMs: 1000, MaxMs: 5000},
	PluginHookBeforeForward:   {DefaultMs: 2500, MaxMs: 5000},
	PluginHookBeforeUpstream:  {DefaultMs: 1000, MaxMs: 5000},
	PluginHookRewriteResponse: {DefaultMs: 1000, MaxMs: 5000},
	PluginHookOnStreamEvent:   {DefaultMs: 50, MaxMs: 1000},
	PluginHookOnResponse:      {DefaultMs: 30000, MaxMs: 60000},
	PluginHookObserveTurn:     {DefaultMs: 30000, MaxMs: 60000},
	PluginHookOnEvent:         {DefaultMs: 30000, MaxMs: 60000},
	PluginHookOnSchedule:      {DefaultMs: 60000, MaxMs: 300000},
	PluginHookOnHTTP:          {DefaultMs: 10000, MaxMs: 30000},
	PluginHookOnUICall:        {DefaultMs: 10000, MaxMs: 30000},
	PluginHookProvider:        {DefaultMs: 1000, MaxMs: 5000},
	PluginHookProviderEvent:   {DefaultMs: 50, MaxMs: 1000},
}

// PluginHookLimitSetting is one hook's time limit on the plugin page: the
// bounds, what the plugin asks for, and what the user set (0 for none).
type PluginHookLimitSetting struct {
	Hook      string `json:"hook"`
	DefaultMs int    `json:"default_ms"`
	MaxMs     int    `json:"max_ms"`
	// PluginMs is what the plugin's manifest asks for, 0 for the default.
	PluginMs int `json:"plugin_ms,omitempty"`
	// Ms is what the user set, 0 to follow the plugin.
	Ms int `json:"ms,omitempty"`
	// EffectiveMs is the limit in effect.
	EffectiveMs int `json:"effective_ms"`
}

// PluginApprovalHeader marks a change of a plugin's settings the user
// confirmed where nothing else can click for them: the desktop's
// confirmation window, or the web console's own dialog. A change that grants
// a plugin more is refused without it.
const PluginApprovalHeader = "X-AstrLink-Plugin-Approval"

// PluginApproval describes what a change grants a plugin, for the user to
// confirm: enabling it, capabilities it did not have, addresses it could not
// reach (plain http among them), the privacy check skipped, and failing a
// request when its entry hook fails.
type PluginApproval struct {
	Plugin            string   `json:"plugin"`
	Name              string   `json:"name"`
	Enable            bool     `json:"enable,omitempty"`
	AddedCapabilities []string `json:"added_capabilities,omitempty"`
	AddedOrigins      []string `json:"added_origins,omitempty"`
	Plaintext         []string `json:"plaintext,omitempty"`
	Trusted           bool     `json:"trusted,omitempty"`
	FailClosed        bool     `json:"fail_closed,omitempty"`
	// Install is the review of the code an install, an update or a
	// rollback puts in place; Rollback is set for a rollback.
	Install  *PluginInstallReview `json:"install,omitempty"`
	Rollback bool                 `json:"rollback,omitempty"`
}

// Empty reports whether the change grants nothing.
func (approval PluginApproval) Empty() bool {
	return !approval.Enable && len(approval.AddedCapabilities) == 0 && len(approval.AddedOrigins) == 0 &&
		len(approval.Plaintext) == 0 && !approval.Trusted && !approval.FailClosed && approval.Install == nil
}

// States of a plugin in the plugin list.
const (
	PluginStateLoaded = "loaded"
	PluginStateFailed = "failed"
)

// Sources of a plugin: built into Core, from the plugin directory, or from
// the plugin directory in place of the builtin of the same key.
const (
	PluginSourceBuiltin   = "builtin"
	PluginSourceDirectory = "directory"
	PluginSourceOverride  = "override"
)

// Statuses of a connection test: what the plugin reported, or failed when
// the call itself failed.
const (
	PluginTestOK     = "ok"
	PluginTestError  = "error"
	PluginTestFailed = "failed"
)

// PluginSummary is one plugin of the plugin list.
type PluginSummary struct {
	Key string `json:"key"`
	// Name and Version are the manifest's; for a plugin that did not load,
	// Name is its key and Version is empty.
	Name    string `json:"name"`
	Version string `json:"version"`
	// Source is builtin, directory, or override for a directory plugin
	// that replaces the builtin of its key.
	Source string `json:"source"`
	// File is the source's name, plugin.ts or plugin.js.
	File string `json:"file,omitempty"`
	// Icon is the icon beside the source as a data URL of a PNG or SVG
	// image, absent for none.
	Icon    string `json:"icon,omitempty"`
	Enabled bool   `json:"enabled"`
	// State is loaded or failed; Error says why a plugin failed.
	State                 string           `json:"state"`
	Error                 *PluginLoadError `json:"error,omitempty"`
	RequestedCapabilities []string         `json:"requested_capabilities"`
	GrantedCapabilities   []string         `json:"granted_capabilities"`
	NeedsNetwork          bool             `json:"needs_network"`
	// APIVersion is the plugin contract the manifest names, 0 for a plugin
	// that did not load.
	APIVersion int `json:"api_version,omitempty"`
	// Development is set for a plugin astrlink plugin dev keeps built from
	// a project on this computer.
	Development *PluginDevelopment `json:"development,omitempty"`
	// Installed is set for a plugin installed from source: where it came
	// from.
	Installed *PluginInstallSource `json:"installed,omitempty"`
	// UIVersion is set when the plugin's interface code loads: enabled,
	// granted ui.code, and its interface not turned off. It changes with
	// the code.
	UIVersion string `json:"ui_version,omitempty"`
	// Providers are the kinds of provider the plugin adapts, while it is
	// enabled with provider granted; a task plugin's one, while it is
	// enabled.
	Providers []PluginProvider `json:"providers,omitempty"`
	// Kind is extension, or task for a task plugin written for new-api's
	// task plugin API, which Task describes. A plugin that did not load
	// has none.
	Kind string      `json:"kind,omitempty"`
	Task *PluginTask `json:"task,omitempty"`
	// Request describes a new-api plugin's request processing part: the
	// hooks it exports and the schema of an instance's config.
	Request *PluginRequestPart `json:"request,omitempty"`
	// Calls and Failures count the plugin's hook calls since Core started;
	// Tasks and RunningTasks a task plugin's tasks, and those not yet
	// finished.
	Calls        int `json:"calls,omitempty"`
	Failures     int `json:"failures,omitempty"`
	Tasks        int `json:"tasks,omitempty"`
	RunningTasks int `json:"running_tasks,omitempty"`
}

// PluginUICallRequest is a call of a plugin's interface code to its
// backend part, the onUiCall hook.
type PluginUICallRequest struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

// PluginUICallResult is what onUiCall returned.
type PluginUICallResult struct {
	Result json.RawMessage `json:"result"`
}

// PluginDevelopment is the project a development build comes from.
type PluginDevelopment struct {
	Project string `json:"project"`
	BuiltAt string `json:"built_at,omitempty"`
}

// PluginJournal is what the development tab of the plugin page shows: the
// log lines and failures of the plugin's recent calls after a sequence
// number, and how long each hook took. Latest is the sequence number to ask
// after next time.
type PluginJournal struct {
	Entries []PluginJournalEntry `json:"entries"`
	Timings []PluginHookTiming   `json:"timings"`
	Latest  uint64               `json:"latest"`
	// Breakers are the extension points kept out of requests for now,
	// after failing too often, and until when.
	Breakers []PluginBreaker `json:"breakers"`
}

// PluginBreaker is an extension point kept out of requests until Until, in
// RFC 3339.
type PluginBreaker struct {
	Hook  string `json:"hook"`
	Until string `json:"until"`
}

// PluginJournalEntry is one log line or failed call. Reason and Stack are
// set for a failed call; DryRun marks the lines of a call run by hand.
type PluginJournalEntry struct {
	Seq     uint64 `json:"seq"`
	Time    string `json:"time"`
	Hook    string `json:"hook"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Reason  string `json:"reason,omitempty"`
	Stack   string `json:"stack,omitempty"`
	DryRun  bool   `json:"dry_run,omitempty"`
}

// PluginHookTiming is how long a hook's recent calls took, in
// milliseconds.
type PluginHookTiming struct {
	Hook     string  `json:"hook"`
	Calls    int     `json:"calls"`
	Failures int     `json:"failures"`
	P50Ms    float64 `json:"p50_ms"`
	P95Ms    float64 `json:"p95_ms"`
	MaxMs    float64 `json:"max_ms"`
	ScriptMs float64 `json:"script_ms"`
}

// PluginDryRunRequest runs one hook of a plugin by hand, without the
// network: Args are the hook's arguments.
type PluginDryRunRequest struct {
	Hook string            `json:"hook"`
	Args []json.RawMessage `json:"args,omitempty"`
}

// MaxPluginDryRunBytes bounds a dry run's request.
const MaxPluginDryRunBytes = 256 << 10

// PluginDryRunResult is what a dry run returned, or why it failed.
type PluginDryRunResult struct {
	Result    json.RawMessage      `json:"result,omitempty"`
	Error     *PluginCallFailure   `json:"error,omitempty"`
	ElapsedMs float64              `json:"elapsed_ms"`
	ScriptMs  float64              `json:"script_ms"`
	Logs      []PluginJournalEntry `json:"logs"`
}

// PluginCallFailure is why a hook call produced no result.
type PluginCallFailure struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
}

// PluginLoadError is why a plugin did not load. Line and Column are 0 when
// the cause has no position in the source.
type PluginLoadError struct {
	Code    string `json:"code"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
}

// PluginDetail is a plugin with its settings, for the plugin page.
type PluginDetail struct {
	PluginSummary
	NetworkPurpose string `json:"network_purpose,omitempty"`
	// Notice is shown as written in the dialog that enables the plugin.
	Notice         string                `json:"notice,omitempty"`
	Hooks          []string              `json:"hooks"`
	Config         json.RawMessage       `json:"config"`
	NetworkOrigins []PluginNetworkOrigin `json:"network_origins"`
	PrivacyTrusted bool                  `json:"privacy_trusted"`
	// TurnWaitMs is how long a request waits for the plugin's prepare hooks.
	TurnWaitMs int `json:"turn_wait_ms"`
	// ExcludedClients are the client types the plugin is off for; it takes
	// part in the requests of every other client, unknown ones included.
	ExcludedClients []string `json:"excluded_clients"`
	// WorkspaceBinding binds a new session to the value of the plugin's
	// attributes its workspace had before.
	WorkspaceBinding bool `json:"workspace_binding"`
	// AutoRefresh checks a session's resident block for newer data now and
	// then, and swaps it when it changed.
	AutoRefresh bool `json:"auto_refresh"`
	// LookupEnabled lets the model run the plugin's queries through the
	// lookup outlet, once the user granted model.tools.
	LookupEnabled bool `json:"lookup_enabled"`
	// Limits are the time limits of the extension points the plugin
	// serves.
	Limits []PluginHookLimitSetting `json:"limits"`
	// FailClosedRequested is set when the plugin asks to fail a request
	// its entry hook cannot decide; FailClosed when the user agreed.
	FailClosedRequested bool `json:"fail_closed_requested,omitempty"`
	FailClosed          bool `json:"fail_closed,omitempty"`
	// Client is set for a plugin with interface code; UIEnabled when the
	// user lets it load.
	Client    bool `json:"client,omitempty"`
	UIEnabled bool `json:"ui_enabled"`
	// Tools are the queries the plugin offers the model.
	Tools       []PluginTool              `json:"tools"`
	Credentials []PluginCredentialStatus  `json:"credentials"`
	Metadata    []PluginMetadataAttribute `json:"metadata"`
	// Settings are the fields of the plugin's settings form, in order.
	Settings []PluginSetting `json:"settings"`
	// Credits are the open-source projects the plugin credits.
	Credits []PluginCredit `json:"credits"`
	// Storage is what the plugin keeps, as its manifest declares it, and
	// HostStorage what the host keeps for it.
	Storage     []PluginStorageItem `json:"storage"`
	HostStorage []PluginHostStorage `json:"host_storage"`
	// Versions are the versions of a plugin installed from source that
	// later installs replaced, the newest first.
	Versions []PluginInstalledVersion `json:"versions,omitempty"`
}

// PluginTool is a query a plugin offers the model, or a report it takes:
// its name and what it says of itself in the interface language.
type PluginTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Unbound marks a query a session may run before it is bound.
	Unbound bool `json:"unbound,omitempty"`
	// Writes marks a report: the model hands the plugin something to keep.
	Writes bool `json:"writes,omitempty"`
}

// PluginStorageItem is something a plugin says it keeps in its own store.
type PluginStorageItem struct {
	Label string `json:"label"`
	// Content tells whether it holds conversation text.
	Content   bool   `json:"content"`
	Retention string `json:"retention"`
}

// What the host keeps for a plugin. The plugin page names each.
const (
	// Session bindings of the plugin's attributes: value, how and in which
	// turn it was set.
	PluginStoredSessionBindings = "session_bindings"
	// Workspaces bound to a value: a keyed fingerprint of the path and its
	// last element.
	PluginStoredWorkspaceBindings = "workspace_bindings"
	// PluginStoredLookupCredentials are the credentials sessions run the
	// plugin's lookups with: in memory only, as hashes.
	PluginStoredLookupCredentials = "lookup_credentials"
	// The plugin's records in request records: counts, sizes, times and
	// reasons.
	PluginStoredRecords = "request_records"
	// The plugin's part in each session: the turn it started in and
	// whether the user paused it.
	PluginStoredSessions = "plugin_sessions"
	// The plugin's own values in astrlink.store.
	PluginStoredValues = "plugin_values"
	// The resident block a session carries, in memory only.
	PluginStoredResidentBlock = "resident_block"
	// The files of the plugin's own data directory, which its node:fs
	// reads and writes; they stay when the plugin is removed.
	PluginStoredFiles = "plugin_files"
)

// PluginHostStorage is one kind of thing the host keeps for a plugin, and
// how many it keeps now when it counts them; Bytes is their size where the
// host measures it.
type PluginHostStorage struct {
	Kind  string `json:"kind"`
	Count *int   `json:"count,omitempty"`
	Bytes *int64 `json:"bytes,omitempty"`
}

// PluginSetting is a field of a plugin's settings form, as its manifest
// declares it. A key starting with $ places one of the host's settings.
type PluginSetting struct {
	Key         string                `json:"key"`
	Label       string                `json:"label,omitempty"`
	Type        string                `json:"type,omitempty"`
	Group       string                `json:"group,omitempty"`
	Row         string                `json:"row,omitempty"`
	Help        string                `json:"help,omitempty"`
	Default     json.RawMessage       `json:"default,omitempty"`
	Required    bool                  `json:"required,omitempty"`
	Advanced    bool                  `json:"advanced,omitempty"`
	Min         *float64              `json:"min,omitempty"`
	Max         *float64              `json:"max,omitempty"`
	Step        *float64              `json:"step,omitempty"`
	Unit        string                `json:"unit,omitempty"`
	Placeholder string                `json:"placeholder,omitempty"`
	Options     []PluginSettingOption `json:"options,omitempty"`
	Credential  string                `json:"credential,omitempty"`
}

// PluginSettingOption is one answer of a choice.
type PluginSettingOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// PluginCreditStars holds the GitHub stars of the repositories a plugin
// credits, by their address; one GitHub has not answered for is absent.
type PluginCreditStars struct {
	Stars map[string]int `json:"stars"`
}

// PluginCredit is an open-source project a plugin credits.
type PluginCredit struct {
	Name    string `json:"name"`
	Owner   string `json:"owner,omitempty"`
	License string `json:"license,omitempty"`
	URL     string `json:"url"`
}

// PluginNetworkOrigin is an address a plugin may reach.
type PluginNetworkOrigin struct {
	Origin string `json:"origin"`
	// AllowPlaintext is the user's consent to plain http beyond this
	// machine; Plaintext tells the page that the origin needs it.
	AllowPlaintext bool `json:"allow_plaintext"`
	Plaintext      bool `json:"plaintext"`
	// Setting names the server setting the origin comes from; the user's
	// own origins have none.
	Setting string `json:"setting,omitempty"`
}

// PluginCredentialStatus is a credential slot the plugin declares and what
// is stored in it, never the value.
type PluginCredentialStatus struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	Configured bool   `json:"configured"`
	Hint       string `json:"hint,omitempty"`
	Origin     string `json:"origin,omitempty"`
}

// PluginMetadataAttribute is a request attribute the plugin registers.
type PluginMetadataAttribute struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Pattern string `json:"pattern"`
	Marker  string `json:"marker,omitempty"`
}

// PluginSettingsUpdate changes what is set and keeps what is absent.
type PluginSettingsUpdate struct {
	Enabled             *bool                       `json:"enabled,omitempty"`
	GrantedCapabilities *[]string                   `json:"granted_capabilities,omitempty"`
	NetworkOrigins      *[]PluginNetworkOriginInput `json:"network_origins,omitempty"`
	PrivacyTrusted      *bool                       `json:"privacy_trusted,omitempty"`
	TurnWaitMs          *int                        `json:"turn_wait_ms,omitempty"`
	ExcludedClients     *[]string                   `json:"excluded_clients,omitempty"`
	WorkspaceBinding    *bool                       `json:"workspace_binding,omitempty"`
	AutoRefresh         *bool                       `json:"auto_refresh,omitempty"`
	LookupEnabled       *bool                       `json:"lookup_enabled,omitempty"`
	Config              json.RawMessage             `json:"config,omitempty"`
	// HookLimitsMs sets the time limits of extension points, by hook; 0
	// follows the plugin again.
	HookLimitsMs map[string]int `json:"hook_limits_ms,omitempty"`
	FailClosed   *bool          `json:"fail_closed,omitempty"`
	UIEnabled    *bool          `json:"ui_enabled,omitempty"`
	// PlaintextConsent lists the plain http origins of servers the user
	// agrees to reach in the clear.
	PlaintextConsent []string `json:"plaintext_consent,omitempty"`
}

type PluginNetworkOriginInput struct {
	Origin         string `json:"origin"`
	AllowPlaintext bool   `json:"allow_plaintext"`
}

// PluginCredentialInput stores a credential for the one origin the host
// injects it for.
type PluginCredentialInput struct {
	Secret string `json:"secret"`
	Origin string `json:"origin"`
}

// PluginTestResult is the outcome of a plugin's testConnection hook: the
// status and summary it returned, or error and why the call failed.
type PluginTestResult struct {
	Status    string            `json:"status"`
	Summary   string            `json:"summary"`
	View      []PluginViewBlock `json:"view,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	// Reason is set when the call failed: timeout, memory_limit and so on.
	Reason    string             `json:"reason,omitempty"`
	ElapsedMs int                `json:"elapsed_ms"`
	Network   PluginNetworkCount `json:"network"`
}

// PluginNetworkCount counts http.fetch requests: made, denied, failed
// otherwise, and the time spent waiting.
type PluginNetworkCount struct {
	Requests int `json:"requests"`
	Denied   int `json:"denied"`
	Errors   int `json:"errors"`
	WaitMs   int `json:"wait_ms"`
}

// PluginStatus is what the host counted for a plugin since Core started,
// with the view its describeStatus hook returned.
type PluginStatus struct {
	Calls        int                `json:"calls"`
	Failures     map[string]int     `json:"failures"`
	MemoryLimits int                `json:"memory_limits"`
	Timeouts     int                `json:"timeouts"`
	Network      PluginNetworkCount `json:"network"`
	View         []PluginViewBlock  `json:"view,omitempty"`
	Truncated    bool               `json:"truncated,omitempty"`
	// ViewError is why describeStatus gave no view.
	ViewError string `json:"view_error,omitempty"`
	// LastTest is the last connection test since Core started.
	LastTest *PluginLastTest `json:"last_test,omitempty"`
	// Bindings are the values sessions are bound to, for each attribute the
	// plugin registers.
	Bindings []PluginBindings `json:"bindings,omitempty"`
}

// PluginLastTest is a connection test and when it ran.
type PluginLastTest struct {
	PluginTestResult
	TestedAt time.Time `json:"tested_at"`
}

// PluginBindings are the values sessions are bound to for one attribute,
// the most recently used first.
type PluginBindings struct {
	Key    string               `json:"key"`
	Label  string               `json:"label"`
	Values []PluginBindingValue `json:"values"`
}

// PluginBindingValue is a value, how many sessions are bound to it and when
// one of them last made a request.
type PluginBindingValue struct {
	Value      string     `json:"value"`
	Sessions   int        `json:"sessions"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// MaxPluginExcludedClients bounds the clients a plugin is off for.
const MaxPluginExcludedClients = 64

// SessionPlugin is a plugin's part in one session, for the session's memory
// page.
type SessionPlugin struct {
	Plugin string `json:"plugin"`
	Name   string `json:"name"`
	// Paused is set when the user paused the plugin in the session.
	Paused bool `json:"paused"`
	// FirstTurn is the turn the plugin first took part in, 0 for none yet.
	FirstTurn int `json:"first_turn,omitempty"`
	// Block is the resident block the session carries. It lives in memory
	// only: nil after a restart until the session's next request.
	Block *SessionPluginBlock `json:"block,omitempty"`
	// RefreshPending is set while a refresh waits for the next request.
	RefreshPending bool `json:"refresh_pending,omitempty"`
	// Lookup is the usage of the plugin's lookups the session's last
	// request carried, its credential masked; "" for none.
	Lookup string `json:"lookup,omitempty"`
}

// SessionPluginBlock is a resident block as it goes into the session's
// requests.
type SessionPluginBlock struct {
	Text       string    `json:"text"`
	Bytes      int       `json:"bytes"`
	Trigger    string    `json:"trigger"`
	Language   string    `json:"language"`
	ComputedAt time.Time `json:"computed_at"`
}

// SessionPluginInput pauses or resumes a plugin in a session.
type SessionPluginInput struct {
	Paused bool `json:"paused"`
}

// PluginActionInput is a click on a button of a plugin's record.
type PluginActionInput struct {
	Action  string `json:"action"`
	Session string `json:"session"`
}

// PluginActionResult is what came of a click: a message from the plugin,
// and whether the session's next request computes its block again.
type PluginActionResult struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Refresh bool   `json:"refresh"`
	// Reason is set when the call failed: timeout, plugin_error and so on.
	Reason string `json:"reason,omitempty"`
}

// PluginClearResult says how many things a clearing removed.
type PluginClearResult struct {
	Removed int `json:"removed"`
}
