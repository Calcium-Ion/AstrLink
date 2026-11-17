package host

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/core"
	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// ---- Core ----

// Core starts and stops the Core process and reports its state.
type Core struct{ h *Host }

// AppSnapshot is the app's version with Core's state.
type AppSnapshot struct {
	AppVersion string `json:"app_version"`
	core.Snapshot
}

func (c Core) capture() AppSnapshot {
	return AppSnapshot{AppVersion: c.h.appVersion(), Snapshot: c.h.core.Snapshot()}
}

// CoreStatus returns the app version and Core's state.
func (c Core) CoreStatus() AppSnapshot { return c.capture() }

// StartCore starts Core, which may wait on a keychain prompt.
func (c Core) StartCore() (AppSnapshot, error) {
	if err := c.h.core.Start(); err != nil {
		return AppSnapshot{}, err
	}
	return c.capture(), nil
}

// StopCore stops Core and waits for it to end.
func (c Core) StopCore(ctx context.Context) (AppSnapshot, error) {
	if err := c.h.core.StopAndWait(ctx); err != nil {
		return AppSnapshot{}, err
	}
	return c.capture(), nil
}

// RestartCore stops Core, waiting for it, and starts it again.
func (c Core) RestartCore(ctx context.Context) (AppSnapshot, error) {
	if err := c.h.core.Restart(ctx); err != nil {
		return AppSnapshot{}, err
	}
	return c.capture(), nil
}

// ---- Preferences ----

// Preferences are the desktop's own settings.
type Preferences struct{ h *Host }

// SettingsSnapshot is what the settings page shows: the preferences, the
// login item as the system reports it, the data backups and where Core's
// local key lives.
type SettingsSnapshot struct {
	prefs.Snapshot
	AutostartActual *bool                 `json:"autostart_actual"`
	AutostartError  *string               `json:"autostart_error"`
	DataBackups     []core.DataBackupFile `json:"data_backups"`
	// LocalKeyStorage is nil before the first Core start.
	LocalKeyStorage *keystore.LocalKeyStorage `json:"local_key_storage"`
}

// GetPreferences returns the settings snapshot.
func (p Preferences) GetPreferences() SettingsSnapshot { return p.h.settingsSnapshot() }

// UpdatePreferences validates and saves the preferences, turns the login
// item on or off to match, and applies them to Core and every window.
func (p Preferences) UpdatePreferences(input json.RawMessage) (SettingsSnapshot, error) {
	values, err := prefs.ParseInput(input)
	if err != nil {
		return SettingsSnapshot{}, err
	}
	if err := p.h.updatePreferences(values); err != nil {
		return SettingsSnapshot{}, err
	}
	return p.h.settingsSnapshot(), nil
}

// ---- Shell ----

// Shell opens links in the system browser.
type Shell struct{ h *Host }

// OpenExternalURL opens an https link in the system browser. A file the
// gateway generated, such as an image, opens there too.
func (s Shell) OpenExternalURL(url string) error {
	if err := checkExternalURL(url); err != nil && !s.h.IsGatewayFile(url) {
		return err
	}
	if err := s.h.openURL(url); err != nil {
		return errors.New("unable to open URL in the system browser: " + err.Error())
	}
	return nil
}

func checkExternalURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return errors.New("invalid external URL")
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" {
		return errors.New("external URL must use https")
	}
	return nil
}

// OpenAuthorizationURL opens an authorization provider's page.
func (s Shell) OpenAuthorizationURL(url string) error {
	if err := control.ValidateAuthorizationURL(url); err != nil {
		return err
	}
	if err := s.h.openURL(url); err != nil {
		return errors.New("unable to open authorization URL in the system browser: " + err.Error())
	}
	return nil
}

// ---- Window ----

// Window is the windows' chrome and placement.
type Window struct{ h *Host }

// WindowChrome is what the frontend draws its own title bar from.
type WindowChrome struct {
	Platform string `json:"platform"`
	// DecorationLayout is GTK's gtk-decoration-layout on Linux, nil
	// elsewhere.
	DecorationLayout *string `json:"decoration_layout"`
}

// WindowChromePreferences returns the platform and, on Linux, where the
// desktop puts the window buttons.
func (Window) WindowChromePreferences() WindowChrome {
	return WindowChrome{Platform: PlatformName(), DecorationLayout: decorationLayout()}
}

// WindowState is what a custom title bar draws from.
type WindowState struct {
	Focused    bool `json:"focused"`
	FullScreen bool `json:"full_screen"`
	Maximized  bool `json:"maximized"`
}

// WindowState returns the state of the window the call came from.
func (Window) WindowState(ctx context.Context) (WindowState, error) {
	w := mygo.CallerWindow(ctx)
	if w == nil {
		return WindowState{}, errors.New("no window made this call")
	}
	return WindowState{Focused: w.IsFocused(), FullScreen: w.IsFullScreen(), Maximized: w.IsMaximized()}, nil
}

// WindowService returns the window service, for registerWindowCommands.
func (h *Host) WindowService() *Window { return &Window{h} }

// Services returns the services of Core's lifecycle, the preferences and
// the shell, for registerCoreCommands to bind.
func (h *Host) Services() (Core, Preferences, Shell) {
	return Core{h}, Preferences{h}, Shell{h}
}

// CallerLabel is the label of the window a call came from, "" when the
// caller is no window.
func CallerLabel(ctx context.Context) string {
	return Label(mygo.CallerWindow(ctx))
}
