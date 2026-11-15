package prefs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
)

const (
	// DefaultInferencePort is the inference port of a new install.
	DefaultInferencePort uint16 = 18317
	// legacyInferencePort is the port of installs that predate the setting.
	legacyInferencePort             uint16 = 8317
	DefaultMaxConcurrentInspections uint16 = 16
	MinMaxConcurrentInspections     uint16 = 4
	MaxMaxConcurrentInspections     uint16 = 128
	DefaultResponseStartTimeoutSecs uint32 = 600
	MaxResponseStartTimeoutSeconds  uint32 = 86400
	fileName                               = "desktop-preferences.json"
	autoCurrency                           = "auto"
)

// InferenceListen is which network interfaces the inference port answers.
type InferenceListen string

const (
	// ListenLoopback lets only programs on this computer reach the gateway.
	ListenLoopback InferenceListen = "loopback"
	// ListenAllInterfaces lets other devices reach the gateway with an
	// access token.
	ListenAllInterfaces InferenceListen = "all_interfaces"
)

func (l *InferenceListen) UnmarshalJSON(data []byte) error {
	return unmarshalChoice(data, (*string)(l), "inference_listen", ListenLoopback, ListenAllInterfaces)
}

// Host is the host Core binds for this scope.
func (l InferenceListen) Host() string {
	if l == ListenAllInterfaces {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// CloseBehavior is what closing the main window does.
type CloseBehavior string

const (
	CloseHideToTray CloseBehavior = "hide_to_tray"
	CloseQuit       CloseBehavior = "quit"
)

func (c *CloseBehavior) UnmarshalJSON(data []byte) error {
	return unmarshalChoice(data, (*string)(c), "close_behavior", CloseHideToTray, CloseQuit)
}

// ThemePreference is the theme the user chose.
type ThemePreference string

const (
	ThemeSystem ThemePreference = "system"
	ThemeLight  ThemePreference = "light"
	ThemeDark   ThemePreference = "dark"
)

func (t *ThemePreference) UnmarshalJSON(data []byte) error {
	return unmarshalChoice(data, (*string)(t), "theme", ThemeSystem, ThemeLight, ThemeDark)
}

// QuotaDisplayMode is whether quotas show what remains or what is used.
type QuotaDisplayMode string

const (
	QuotaRemaining QuotaDisplayMode = "remaining"
	QuotaUsed      QuotaDisplayMode = "used"
)

func (q *QuotaDisplayMode) UnmarshalJSON(data []byte) error {
	return unmarshalChoice(data, (*string)(q), "quota_display_mode", QuotaRemaining, QuotaUsed)
}

// Percent is the share of a quota to show when usedPercent of it is used.
func (q QuotaDisplayMode) Percent(usedPercent float64) float64 {
	used := min(max(usedPercent, 0), 100)
	if usedPercent != usedPercent { // NaN
		used = 0
	}
	if q == QuotaUsed {
		return used
	}
	return 100 - used
}

// TrayMenubarText is what the macOS status item shows next to the tray
// icon. Other platforms have no title slot, so the same text goes into the
// tooltip instead.
type TrayMenubarText string

const (
	MenubarNone         TrayMenubarText = "none"
	MenubarRequests     TrayMenubarText = "requests"
	MenubarTokens       TrayMenubarText = "tokens"
	MenubarCost         TrayMenubarText = "cost"
	MenubarSubscription TrayMenubarText = "subscription"
	MenubarAlertOnly    TrayMenubarText = "alert_only"
)

func (t *TrayMenubarText) UnmarshalJSON(data []byte) error {
	return unmarshalChoice(data, (*string)(t), "menubar_text", MenubarNone, MenubarRequests, MenubarTokens, MenubarCost, MenubarSubscription, MenubarAlertOnly)
}

// TrayPage is a page the tray popover can jump to. Overview and settings are
// always reachable through the fixed "open" and "settings" actions, so they
// are not listed. The popover renders the enabled subset in this order.
type TrayPage string

const (
	TrayRecords    TrayPage = "records"
	TrayServices   TrayPage = "services"
	TrayTokens     TrayPage = "tokens"
	TraySafety     TrayPage = "safety"
	TrayRouting    TrayPage = "routing"
	TrayAgentTools TrayPage = "agent_tools"
)

func (t *TrayPage) UnmarshalJSON(data []byte) error {
	return unmarshalChoice(data, (*string)(t), "tray page", TrayRecords, TrayServices, TrayTokens, TraySafety, TrayRouting, TrayAgentTools)
}

// TrayUsagePreferences are the usage lines the tray menu can show. Every
// line is optional so the menu stays short by default; the operator enables
// the numbers they like.
type TrayUsagePreferences struct {
	Today bool `json:"today"`
	Cost  bool `json:"cost"`
	// Privacy is what the privacy policy replaced, restored and blocked
	// today.
	Privacy             bool `json:"privacy"`
	SubscriptionWindows bool `json:"subscription_windows"`
	TopModel            bool `json:"top_model"`
	CompareYesterday    bool `json:"compare_yesterday"`
	CacheHit            bool `json:"cache_hit"`
	TopClient           bool `json:"top_client"`
	LastRequest         bool `json:"last_request"`
	MonthTotal          bool `json:"month_total"`
}

func defaultTrayUsage() TrayUsagePreferences {
	return TrayUsagePreferences{Today: true, Cost: true, Privacy: true, SubscriptionWindows: true, TopModel: true}
}

func (u *TrayUsagePreferences) UnmarshalJSON(data []byte) error {
	type plain TrayUsagePreferences
	v := plain(defaultTrayUsage())
	if err := decodeStrict(data, &v); err != nil {
		return err
	}
	*u = TrayUsagePreferences(v)
	return nil
}

// NeedsUsageSummary reports whether any line needs a usage summary call.
func (u TrayUsagePreferences) NeedsUsageSummary() bool {
	return u.Today || u.TopModel || u.CompareYesterday || u.CacheHit
}

// NeedsAnything reports whether any line is shown.
func (u TrayUsagePreferences) NeedsAnything() bool {
	return u.NeedsUsageSummary() || u.Cost || u.Privacy || u.SubscriptionWindows || u.TopClient || u.LastRequest || u.MonthTotal
}

// TrayPreferences configure the tray and its popover.
type TrayPreferences struct {
	MenubarText     TrayMenubarText      `json:"menubar_text"`
	CopyAddress     bool                 `json:"copy_address"`
	GatewayControls bool                 `json:"gateway_controls"`
	Usage           TrayUsagePreferences `json:"usage"`
	// Pages are the quick-jump pages in fixed navigation order.
	Pages []TrayPage `json:"pages"`
}

// DefaultTrayPreferences are the tray settings of a new install.
func DefaultTrayPreferences() TrayPreferences {
	return TrayPreferences{
		MenubarText:     MenubarNone,
		CopyAddress:     true,
		GatewayControls: true,
		Usage:           defaultTrayUsage(),
		Pages:           []TrayPage{TrayRecords, TrayServices, TrayTokens},
	}
}

func (t *TrayPreferences) UnmarshalJSON(data []byte) error {
	type plain TrayPreferences
	v := plain(DefaultTrayPreferences())
	if err := decodeStrict(data, &v); err != nil {
		return err
	}
	if v.Pages == nil {
		v.Pages = []TrayPage{}
	}
	*t = TrayPreferences(v)
	return nil
}

// Validate rejects a page listed twice.
func (t TrayPreferences) Validate(locale Locale) error {
	seen := map[TrayPage]bool{}
	for _, page := range t.Pages {
		if seen[page] {
			return errors.New(T(locale, "host.preferences.trayPagesDuplicate"))
		}
		seen[page] = true
	}
	return nil
}

// UpdateChannel is which releases updates offer.
type UpdateChannel string

const (
	ChannelStable  UpdateChannel = "stable"
	ChannelPreview UpdateChannel = "preview"
)

func (c *UpdateChannel) UnmarshalJSON(data []byte) error {
	return unmarshalChoice(data, (*string)(c), "channel", ChannelStable, ChannelPreview)
}

// UpdatePreferences are the update settings of About, saved apart from the
// general settings.
type UpdatePreferences struct {
	AutoCheck    bool          `json:"auto_check"`
	AutoDownload bool          `json:"auto_download"`
	Channel      UpdateChannel `json:"channel"`
	// OfficialMirror checks and downloads updates through the project
	// download site instead of GitHub.
	OfficialMirror bool `json:"official_mirror"`
}

// DefaultUpdatePreferences are the update settings of a new install.
func DefaultUpdatePreferences() UpdatePreferences {
	return UpdatePreferences{AutoCheck: true, AutoDownload: true, Channel: ChannelStable}
}

func (u *UpdatePreferences) UnmarshalJSON(data []byte) error {
	type plain UpdatePreferences
	v := plain(DefaultUpdatePreferences())
	if err := decodeStrict(data, &v); err != nil {
		return err
	}
	*u = UpdatePreferences(v)
	return nil
}

// Preferences are the desktop's saved settings.
type Preferences struct {
	CloseBehavior   CloseBehavior `json:"close_behavior"`
	Autostart       bool          `json:"autostart"`
	AutostartHidden bool          `json:"autostart_hidden"`
	CoreAutoStart   bool          `json:"core_auto_start"`
	CoreAutoRecover bool          `json:"core_auto_recover"`
	UseSystemProxy  bool          `json:"use_system_proxy"`
	// InferencePort of preferences saved without it is the original
	// default.
	InferencePort uint16 `json:"inference_port"`
	// InferenceListen of preferences saved before it existed is loopback.
	InferenceListen             InferenceListen `json:"inference_listen"`
	MaxConcurrentInspections    uint16          `json:"max_concurrent_inspections"`
	ResponseStartTimeoutSeconds uint32          `json:"response_start_timeout_seconds"`
	// MaxRequestBodyMiB is the largest inference request body in MiB; zero
	// means unlimited.
	MaxRequestBodyMiB uint32           `json:"max_request_body_mib"`
	Locale            Locale           `json:"locale"`
	Theme             ThemePreference  `json:"theme"`
	QuotaDisplayMode  QuotaDisplayMode `json:"quota_display_mode"`
	// DisplayCurrency is what amounts estimated at official API prices are
	// shown in: auto for the system region's currency, or a currency code.
	DisplayCurrency string            `json:"display_currency"`
	Tray            TrayPreferences   `json:"tray"`
	Updates         UpdatePreferences `json:"updates"`
	// DesktopToys is saved on its own, like Updates, so the settings page
	// and the toys never overwrite each other's changes.
	DesktopToys DesktopToysPreferences `json:"desktop_toys"`
}

// Default returns the preferences of a new install whose default inference
// port is port (instance.DefaultInferencePort).
func Default(port uint16) Preferences {
	return Preferences{
		CloseBehavior:               CloseHideToTray,
		CoreAutoStart:               true,
		CoreAutoRecover:             true,
		UseSystemProxy:              true,
		InferencePort:               port,
		InferenceListen:             ListenLoopback,
		MaxConcurrentInspections:    DefaultMaxConcurrentInspections,
		ResponseStartTimeoutSeconds: DefaultResponseStartTimeoutSecs,
		Locale:                      LocaleEn,
		Theme:                       ThemeSystem,
		QuotaDisplayMode:            QuotaRemaining,
		DisplayCurrency:             autoCurrency,
		Tray:                        DefaultTrayPreferences(),
		Updates:                     DefaultUpdatePreferences(),
		DesktopToys:                 DefaultDesktopToys(),
	}
}

// decodePreferences reads a saved file: missing fields take the defaults of
// Default(port), but the inference port, whose saved files without it used
// the original default.
func decodePreferences(data []byte, port uint16) (Preferences, error) {
	v := Default(port)
	v.InferencePort = legacyInferencePort
	type plain Preferences
	p := plain(v)
	if err := decodeStrict(data, &p); err != nil {
		return Preferences{}, err
	}
	return Preferences(p), nil
}

// optionalInputFields are the fields the settings page may leave out; they
// take their defaults.
var optionalInputFields = []string{"autostart_hidden", "inference_listen", "tray", "updates", "desktop_toys"}

// ParseInput reads the preferences the settings page saves. Every field
// but optionalInputFields is required and unknown fields are refused.
func ParseInput(data []byte) (Preferences, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Preferences{}, err
	}
	for _, name := range preferenceFields() {
		if _, ok := fields[name]; !ok && !slices.Contains(optionalInputFields, name) {
			return Preferences{}, fmt.Errorf("missing field `%s`", name)
		}
	}
	defaults := Default(DefaultInferencePort)
	p := Preferences{
		InferenceListen: defaults.InferenceListen,
		Tray:            defaults.Tray,
		Updates:         defaults.Updates,
		DesktopToys:     defaults.DesktopToys,
	}
	type plain Preferences
	v := plain(p)
	if err := decodeStrict(data, &v); err != nil {
		return Preferences{}, err
	}
	return Preferences(v), nil
}

// preferenceFields lists the JSON names of the Preferences fields.
func preferenceFields() []string {
	t := reflect.TypeFor[Preferences]()
	names := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		names = append(names, name)
	}
	return names
}

// Validate checks the values the settings page can set.
func (p Preferences) Validate() error {
	if p.InferencePort < 1024 {
		return errors.New(T(p.Locale, "host.preferences.portRange"))
	}
	if err := p.Tray.Validate(p.Locale); err != nil {
		return err
	}
	if !IsCurrencyChoice(p.DisplayCurrency) {
		return errors.New(T(p.Locale, "host.preferences.currencyUnknown"))
	}
	if p.MaxConcurrentInspections < MinMaxConcurrentInspections || p.MaxConcurrentInspections > MaxMaxConcurrentInspections {
		return errors.New(T(p.Locale, "host.preferences.concurrencyRange"))
	}
	if p.ResponseStartTimeoutSeconds > MaxResponseStartTimeoutSeconds {
		return errors.New(T(p.Locale, "host.preferences.responseStartTimeoutRange"))
	}
	return nil
}

// Currencies the setting offers, with the symbol and decimals shown.
// Mirrors DISPLAY_CURRENCIES in src/currency-display.ts.
var Currencies = []struct {
	Code, Symbol string
	Decimals     int
}{
	{"USD", "$", 2}, {"CNY", "¥", 2}, {"HKD", "HK$", 2}, {"TWD", "NT$", 2},
	{"JPY", "¥", 0}, {"KRW", "₩", 0}, {"SGD", "S$", 2}, {"EUR", "€", 2},
	{"GBP", "£", 2}, {"CHF", "CHF ", 2}, {"CAD", "CA$", 2}, {"AUD", "A$", 2},
	{"INR", "₹", 2}, {"RUB", "₽", 2}, {"BRL", "R$", 2},
}

// IsCurrencyChoice reports whether value is a display currency setting.
func IsCurrencyChoice(value string) bool {
	return value == autoCurrency || slices.ContainsFunc(Currencies, func(c struct {
		Code, Symbol string
		Decimals     int
	}) bool {
		return c.Code == value
	})
}

// Snapshot is the saved preferences and what went wrong loading them.
type Snapshot struct {
	Values      Preferences `json:"values"`
	LoadWarning *string     `json:"load_warning"`
}

// Store keeps desktop-preferences.json in the config directory.
type Store struct {
	path        string
	mu          sync.Mutex
	values      Preferences
	loadWarning string
}

// Load reads the preferences of an instance whose default inference port is
// port. When none are usable (first run, unreadable or unparseable file)
// the interface starts in the system language.
func Load(configDirectory, dataDirectory string, port uint16) *Store {
	return LoadWithFallbackLocale(configDirectory, dataDirectory, port, SystemLocale())
}

// LoadWithFallbackLocale is Load with the language of a first run given.
func LoadWithFallbackLocale(configDirectory, dataDirectory string, port uint16, fallbackLocale Locale) *Store {
	path := filepath.Join(configDirectory, fileName)
	tightenExistingMode(path)
	// Older installations may have run without ever saving preferences.
	_, statErr := os.Stat(filepath.Join(dataDirectory, "astrlink.db"))
	hasExistingData := statErr == nil
	fallback := func(locale Locale) Preferences {
		p := Default(port)
		p.Locale = locale
		if hasExistingData {
			p.InferencePort = legacyInferencePort
		}
		return p
	}
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		values, err := decodePreferences(data, port)
		if err != nil {
			s.values = fallback(fallbackLocale)
			s.loadWarning = T(fallbackLocale, "host.preferences.unparseable", "error", err.Error())
			break
		}
		if err := values.Validate(); err != nil {
			s.values = fallback(values.Locale)
			s.loadWarning = T(values.Locale, "host.preferences.invalidSettings", "error", err.Error())
			break
		}
		s.values = values
	case errors.Is(err, fs.ErrNotExist):
		s.values = fallback(fallbackLocale)
		// Save new-install defaults before Core creates its database, so
		// subsequent starts keep the same port. Do not migrate old installs.
		if !hasExistingData {
			if err := persist(path, s.values); err != nil {
				s.loadWarning = err.Error()
			}
		}
	default:
		s.values = fallback(fallbackLocale)
		s.loadWarning = T(fallbackLocale, "host.preferences.unreadable", "error", err.Error())
	}
	return s
}

// Snapshot returns the preferences and the load warning.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}

func (s *Store) snapshot() Snapshot {
	snapshot := Snapshot{Values: s.values}
	snapshot.Values.Tray.Pages = slices.Clone(s.values.Tray.Pages)
	if s.loadWarning != "" {
		w := s.loadWarning
		snapshot.LoadWarning = &w
	}
	return snapshot
}

// Values returns the preferences.
func (s *Store) Values() Preferences { return s.Snapshot().Values }

// Replace validates and saves values. The update and desktop toy settings
// are kept, so the general settings never overwrite those saved on their
// own.
func (s *Store) Replace(values Preferences) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values.Updates = s.values.Updates
	values.DesktopToys = s.values.DesktopToys
	if err := values.Validate(); err != nil {
		return Snapshot{}, err
	}
	if err := persist(s.path, values); err != nil {
		return Snapshot{}, err
	}
	s.values = values
	s.loadWarning = ""
	return s.snapshot(), nil
}

// ReplaceUpdates saves the update settings alone.
func (s *Store) ReplaceUpdates(updates UpdatePreferences) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := s.values
	values.Updates = updates
	if err := persist(s.path, values); err != nil {
		return err
	}
	s.values = values
	return nil
}

// ReplaceDesktopToys saves the desktop toy settings alone.
func (s *Store) ReplaceDesktopToys(toys DesktopToysPreferences) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := s.values
	values.DesktopToys = toys
	if err := persist(s.path, values); err != nil {
		return Snapshot{}, err
	}
	s.values = values
	return s.snapshot(), nil
}

// ReportWarning adds warning to the load warning Settings shows.
func (s *Store) ReportWarning(warning string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadWarning != "" {
		s.loadWarning += "; " + warning
	} else {
		s.loadWarning = warning
	}
}

// tightenExistingMode narrows a file earlier releases wrote 0644. Saving
// rewrites it 0600, but a user who never saves again would keep the old
// mode, so load narrows it too.
func tightenExistingMode(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o077 != 0 {
		os.Chmod(path, 0o600)
	}
}

func persist(path string, values Preferences) error {
	locale := values.Locale
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return errors.New(T(locale, "host.preferences.createDirFailed", "error", err.Error()))
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return errors.New(T(locale, "host.preferences.serializeFailed", "error", err.Error()))
	}
	temporary := TemporaryPath(path)
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New(T(locale, "host.preferences.createTempFailed", "error", err.Error()))
	}
	fail := func(key string, err error) error {
		file.Close()
		os.Remove(temporary)
		return errors.New(T(locale, key, "error", err.Error()))
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fail("host.preferences.writeFailed", err)
	}
	if err := file.Sync(); err != nil {
		return fail("host.preferences.writeFailed", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(temporary)
		return errors.New(T(locale, "host.preferences.writeFailed", "error", err.Error()))
	}
	if err := AtomicReplace(temporary, path); err != nil {
		os.Remove(temporary)
		return errors.New(T(locale, "host.preferences.replaceFailed", "error", err.Error()))
	}
	if err := syncDirectory(parent); err != nil {
		return errors.New(T(locale, "host.preferences.syncDirFailed", "error", err.Error()))
	}
	return nil
}

// decodeStrict decodes data into v, rejecting fields v lacks.
func decodeStrict(data []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("trailing data after the preferences")
	}
	return nil
}

// unmarshalChoice decodes a string that must be one of choices.
func unmarshalChoice[T ~string](data []byte, out *string, what string, choices ...T) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	for _, c := range choices {
		if string(c) == s {
			*out = s
			return nil
		}
	}
	names := make([]string, len(choices))
	for i, c := range choices {
		names[i] = fmt.Sprintf("%q", string(c))
	}
	return fmt.Errorf("unknown %s %q, expected one of %s", what, s, strings.Join(names, ", "))
}
