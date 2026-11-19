// Package host holds what the desktop app's parts share: the instance it
// is, its directories, preferences, the Core it supervises and the main
// window. Services of every area are built on a *Host.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/core"
	"github.com/QuantumNous/astrlink/desktop/internal/instance"
	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
	"github.com/QuantumNous/astrlink/desktop/internal/raw"
)

// MainLabel is the main window's label, as the frontend reads it from
// getCurrentWindow().label.
const MainLabel = "main"

// observerPollInterval spaces the observer and privacy warning polls.
// Agent-side reads are short bursts; a tight local poll keeps the "being
// watched" state honest without any push channel from Core.
const observerPollInterval = 4 * time.Second

// Autostart is the login item (mygo.App.SetOpenAtLogin by default).
type Autostart interface {
	Enabled() (bool, error)
	SetEnabled(enabled bool) error
}

type loginItem struct{}

func (loginItem) Enabled() (bool, error)        { return mygo.App.OpenAtLogin(), nil }
func (loginItem) SetEnabled(enabled bool) error { return mygo.App.SetOpenAtLogin(enabled) }

// Host is the running desktop app. New only allocates it; Start, called
// once the app is ready, loads the preferences, starts Core and opens the
// main window.
type Host struct {
	instance  instance.Instance
	dirs      instance.Directories
	prefs     *prefs.Store
	core      *core.Manager
	pins      *keystore.RawKeyPins
	autostart Autostart
	// openURL opens a link in the system browser.
	openURL func(url string) error
	// writeClipboard puts text on the system clipboard (mygo.Clipboard.Write).
	writeClipboard func(text string) error
	// preferredLanguages are the user's languages, most preferred first
	// (mygo.App.PreferredLanguages): macOS and Windows keep them in their
	// settings, not in LANG.
	preferredLanguages func() []string
	// applyTheme sets the appearance of native UI (mygo.Theme.SetSource).
	applyTheme func(theme prefs.ThemePreference)
	// stop ends the background polls; stopped is closed once they ended.
	stop    context.CancelFunc
	stopped chan struct{}
	// mainOpened takes over each main window as it is built
	// (OnMainWindowOpened).
	mainOpened func(w *mygo.Window)
	// mainPreload adds to the main window's preload script
	// (OnMainWindowPreload).
	mainPreload func() string
	// preload adds to every window's preload script (OnPreload).
	preload func() string

	mu   sync.Mutex
	main *mygo.Window
	// started runs once the host is up, for areas that start their own work
	// after Core's (the tray, updates).
	started            []func()
	recordsChanged     []func()
	preferencesChanged []func(previous, current prefs.Preferences)
	privacyPolicy      []func()
	privacyReleases    []func()
}

// New returns a host that has not started.
func New() *Host {
	return &Host{
		autostart: loginItem{},
		openURL:   mygo.Shell.OpenExternal,
		writeClipboard: func(text string) error {
			return mygo.Clipboard.Write(transfer.TextData(text))
		},
		applyTheme:         func(theme prefs.ThemePreference) { mygo.Theme.SetSource(mygo.ThemeSource(theme)) },
		preferredLanguages: mygo.App.PreferredLanguages,
		stopped:            make(chan struct{}),
	}
}

// OnStarted registers fn to run on the main thread once Start finished.
// Areas register in their register*Commands function.
func (h *Host) OnStarted(fn func()) {
	h.mu.Lock()
	h.started = append(h.started, fn)
	h.mu.Unlock()
}

// OnRecordsChanged registers fn to run after request records were deleted,
// so the tray counts again.
func (h *Host) OnRecordsChanged(fn func()) {
	h.mu.Lock()
	h.recordsChanged = append(h.recordsChanged, fn)
	h.mu.Unlock()
}

// OnPreferencesChanged registers fn to run after the preferences were
// saved, for the tray, the display currency and the theme of native UI.
func (h *Host) OnPreferencesChanged(fn func(previous, current prefs.Preferences)) {
	h.mu.Lock()
	h.preferencesChanged = append(h.preferencesChanged, fn)
	h.mu.Unlock()
}

// OnPrivacyPolicyChanged registers fn to run after the privacy policy
// changed, so the model update reminder settles at once.
func (h *Host) OnPrivacyPolicyChanged(fn func()) {
	h.mu.Lock()
	h.privacyPolicy = append(h.privacyPolicy, fn)
	h.mu.Unlock()
}

// OnPrivacyReleasesRead registers fn to run after the page read the privacy
// model releases, which the reminder may not have seen.
func (h *Host) OnPrivacyReleasesRead(fn func()) {
	h.mu.Lock()
	h.privacyReleases = append(h.privacyReleases, fn)
	h.mu.Unlock()
}

// SetAutostart replaces the login item, for the area that owns it.
func (h *Host) SetAutostart(autostart Autostart) { h.autostart = autostart }

// OnMainWindowOpened hands each main window, hidden as it is built, to fn,
// which places and shows it: at Start, and again in OpenMainWindow. Without
// it the window shows once its page is ready. Call it before Start.
func (h *Host) OnMainWindowOpened(fn func(w *mygo.Window)) { h.mainOpened = fn }

// OnMainWindowPreload appends what fn returns to the preload script of each
// main window as it is built. Call it before Start.
func (h *Host) OnMainWindowPreload(fn func() string) { h.mainPreload = fn }

// OnPreload adds fn's script to every window's preload script. Call it
// before Start.
func (h *Host) OnPreload(fn func() string) { h.preload = fn }

func (h *Host) run(hooks *[]func()) {
	h.mu.Lock()
	fns := append([]func(){}, *hooks...)
	h.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// RecordsChanged runs the OnRecordsChanged hooks.
func (h *Host) RecordsChanged() { h.run(&h.recordsChanged) }

// PrivacyPolicyChanged runs the OnPrivacyPolicyChanged hooks.
func (h *Host) PrivacyPolicyChanged() { h.run(&h.privacyPolicy) }

// PrivacyReleasesRead runs the OnPrivacyReleasesRead hooks.
func (h *Host) PrivacyReleasesRead() { h.run(&h.privacyReleases) }

// RawSealingChanged tells every window that the raw unlock or key may have
// changed.
func (h *Host) RawSealingChanged() {
	RawSealingChanged.Broadcast(struct{}{})
}

// Instance is the instance this app runs as.
func (h *Host) Instance() instance.Instance { return h.instance }

// Directories are the instance's data and config directories.
func (h *Host) Directories() instance.Directories { return h.dirs }

// PreferencesStore is the preferences, loaded at Start.
func (h *Host) PreferencesStore() *prefs.Store { return h.prefs }

// CoreManager is the Core supervisor, created at Start.
func (h *Host) CoreManager() *core.Manager { return h.core }

// Call sends a control request to the running Core, for the services.
func (h *Host) Call(ctx context.Context, req control.Request) (control.Response, error) {
	if h.core == nil {
		return control.Response{}, errors.New("AstrLink is not running")
	}
	return h.core.Call(ctx, req)
}

// Raw returns the raw content service.
func (h *Host) Raw() raw.Raw {
	return raw.Raw{Core: rawCore{h}, Pins: rawPins{h}, Changed: h.RawSealingChanged, Locale: h.locale}
}

// locale is the language native prompts use.
func (h *Host) locale() prefs.Locale {
	if h.prefs == nil {
		return prefs.SystemLocale()
	}
	return h.prefs.Values().Locale
}

// rawCore and rawPins reach the Core and the pins Start creates.
type rawCore struct{ h *Host }

func (c rawCore) Call(ctx context.Context, req control.Request) (control.Response, error) {
	return c.h.Call(ctx, req)
}

func (c rawCore) DataDirectory() string {
	if c.h.core == nil {
		return ""
	}
	return c.h.core.DataDirectory()
}

func (c rawCore) NoteRawKeyReplaced(replaced bool) bool {
	return c.h.core != nil && c.h.core.NoteRawKeyReplaced(replaced)
}

func (c rawCore) PollObservers(ctx context.Context) error {
	if c.h.core == nil {
		return errors.New("AstrLink is not running")
	}
	return c.h.core.PollObservers(ctx)
}

type rawPins struct{ h *Host }

func (p rawPins) Lock() {
	if p.h.pins != nil {
		p.h.pins.Lock()
	}
}

func (p rawPins) Unlock() {
	if p.h.pins != nil {
		p.h.pins.Unlock()
	}
}

func (p rawPins) Apply(account string, status json.RawMessage, action keystore.PinAction) bool {
	return p.h.pins != nil && p.h.pins.Apply(account, status, action)
}

// RegisterServiceTest and CancelServiceTest keep the connection tests
// windows started (control.TestRegistry).
func (h *Host) RegisterServiceTest(owner, serviceID, testID string, cancel func()) (func(), error) {
	if h.core == nil {
		return nil, errors.New("AstrLink is not running")
	}
	return h.core.RegisterServiceTest(owner, serviceID, testID, cancel)
}

func (h *Host) CancelServiceTest(owner, serviceID, testID string) bool {
	return h.core != nil && h.core.CancelServiceTest(owner, serviceID, testID)
}

// GatewayFile is the link of a file the running gateway serves for a task,
// such as a generated image or video, parsed; false for any other link.
func (h *Host) GatewayFile(raw string) (*url.URL, bool) {
	if h.core == nil {
		return nil, false
	}
	return h.core.GatewayFileURL(raw)
}

// IsGatewayFile reports a link of a file the running gateway serves.
func (h *Host) IsGatewayFile(raw string) bool {
	_, ok := h.GatewayFile(raw)
	return ok
}

// OpenURL opens a link in the system browser.
func (h *Host) OpenURL(url string) error { return h.openURL(url) }

// WriteClipboard puts text on the system clipboard, or says why it could
// not.
func (h *Host) WriteClipboard(text string) error { return h.writeClipboard(text) }

func (h *Host) appVersion() string { return mygo.App.Version() }

// followSystemLanguages makes the language of a first run, and of native
// prompts before the preferences load, the system's: the languages of
// System Settings on macOS and the display languages on Windows. LANG
// alone, which apps started from the Dock or Finder do not get, left them
// in English.
func (h *Host) followSystemLanguages() {
	if h.preferredLanguages == nil {
		return
	}
	languages := h.preferredLanguages
	prefs.SetSystemLanguages(func() []string { return languages() })
}

// Start loads the preferences, starts Core and opens the main window. It
// runs on the main thread once the app is ready (App.WhenReady).
func (h *Host) Start() {
	h.instance = instance.FromEnvironment()
	h.followSystemLanguages()
	dirs, err := h.instance.Dirs()
	if err != nil {
		log.Printf("astrlink: unable to resolve the app directories: %v", err)
	}
	h.dirs = dirs
	h.prefs = prefs.Load(dirs.Config, dirs.Data, h.instance.DefaultInferencePort())
	h.pins = keystore.SystemRawKeyPins(dirs.Config)
	resources, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		log.Printf("astrlink: unable to resolve the resource directory: %v", err)
	}
	h.core = core.New(core.Config{DataDirectory: dirs.Data, ResourceDirectory: resources, Isolated: h.instance.Isolated})
	values := h.prefs.Values()
	h.core.Configure(values)
	h.reconcileAutostart(values)
	h.applyTheme(values.Theme)

	ctx, stop := context.WithCancel(context.Background())
	h.stop = stop
	go h.pollObservers(ctx)
	if values.CoreAutoStart {
		// The start may wait on a keychain prompt; keep it off the main
		// thread.
		go func() {
			if err := h.core.Start(); err != nil {
				log.Printf("astrlink: unable to start astrlink-core: %v", err)
			}
		}()
	}
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { h.shutdown() })

	h.OpenMainWindow()
	h.mu.Lock()
	started := h.started
	h.mu.Unlock()
	for _, fn := range started {
		fn()
	}
}

// OpenMainWindow builds the main window: at Start, and again after the
// hidden one was released.
func (h *Host) OpenMainWindow() *mygo.Window {
	w := h.openMainWindow()
	h.mu.Lock()
	h.main = w
	h.mu.Unlock()
	return w
}

// reconcileAutostart makes the login item match the preference, which a
// system settings change or an older release may have moved.
func (h *Host) reconcileAutostart(values prefs.Preferences) {
	actual, err := h.autostart.Enabled()
	if err == nil && actual != values.Autostart {
		err = h.autostart.SetEnabled(values.Autostart)
	}
	if err != nil {
		h.prefs.ReportWarning(prefs.T(values.Locale, "host.autostart.startupCheckFailed", "error", err.Error()))
	}
}

// shutdown stops the polls and Core before the app quits.
func (h *Host) shutdown() {
	if h.stop != nil {
		h.stop()
		<-h.stopped
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.core.StopAndWait(ctx); err != nil {
		log.Printf("astrlink: unable to stop astrlink-core: %v", err)
	}
}

// pollObservers reads what agents do and the privacy warnings while Core
// is ready, until ctx ends.
func (h *Host) pollObservers(ctx context.Context) {
	defer close(h.stopped)
	var cursor core.WarningCursor
	ticker := time.NewTicker(observerPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if view, _ := h.core.View(); view.Phase != core.PhaseReady {
			continue
		}
		// Errors are transient (Core restarting); the next tick retries.
		_ = h.core.PollObservers(ctx)
		core.PollPrivacyWarnings(ctx, h.core, &cursor, h.prefs.Values().Locale, notify)
	}
}

func notify(title, body string) {
	if err := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body}).Show(); err != nil {
		log.Printf("astrlink: unable to show a notification: %v", err)
	}
}

// PlatformName is the OS as the frontend names it
// (window.__ASTRLINK_DESKTOP_PLATFORM__): macos, linux or windows.
func PlatformName() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return runtime.GOOS
}

// PreloadScript is what every window's page runs first: its label, the
// platform and the instance it belongs to.
func (h *Host) PreloadScript(label string) string {
	quote := func(s string) string {
		encoded, _ := json.Marshal(s)
		return string(encoded)
	}
	script := "window.__ASTRLINK_WINDOW_LABEL__ = " + quote(label) + ";\n" +
		"window.__ASTRLINK_DESKTOP_PLATFORM__ = " + quote(PlatformName()) + ";\n" +
		h.instance.InitializationScript() + "\n"
	if h.preload != nil {
		script += h.preload() + "\n"
	}
	return script
}

// mainBackground is the main window's background before the page paints,
// following the theme.
const mainBackground = "light-dark(#ffffff, #111827)"

// openMainWindow opens the main window, hidden until its page is ready.
// macOS keeps the traffic lights over the page; elsewhere the page draws
// its own title bar.
func (h *Host) openMainWindow() *mygo.Window {
	preload := h.PreloadScript(MainLabel)
	if h.mainPreload != nil {
		preload += h.mainPreload()
	}
	options := mygo.WindowOptions{
		Title:           h.instance.Name(),
		URL:             "/",
		Width:           950,
		Height:          620,
		MinWidth:        760,
		MinHeight:       600,
		Hidden:          true,
		BackgroundColor: mainBackground,
		Page:            mygo.PageOptions{PreloadScript: preload},
	}
	if runtime.GOOS == "darwin" {
		options.TitleBarStyle = mygo.TitleBarHidden
	} else {
		options.Frameless = true
	}
	w := mygo.NewWindow(options)
	Register(w, MainLabel)
	if h.mainOpened != nil {
		h.mainOpened(w)
	} else {
		w.OnReadyToShow(w.Show)
	}
	return w
}

// MainWindow returns the main window, or nil before Start or once it closed.
func (h *Host) MainWindow() *mygo.Window {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.main == nil || h.main.IsDestroyed() {
		return nil
	}
	return h.main
}

var labels sync.Map // *mygo.Window → string

// Register names a window for the frontend and the commands that scope
// state per window, and tells its page when its size or state changes.
// Every window the app opens is registered.
func Register(w *mygo.Window, label string) {
	labels.Store(w, label)
	w.OnClosed(func() { labels.Delete(w) })
	// WebKit ends a page's process when it runs out of memory, and a crash
	// ends it too; either leaves the window blank until the page loads
	// again.
	var revive reviver
	w.Page().OnRenderProcessGone(func(reason string) {
		if !revive.allow(time.Now()) {
			log.Printf("astrlink: the %s window's page stopped again (%s); not reloading it", label, reason)
			return
		}
		log.Printf("astrlink: the %s window's page stopped (%s); reloading it", label, reason)
		w.Page().Reload()
	})
	resized := func() { _ = WindowResized.Emit(w, struct{}{}) }
	w.OnResize(resized)
	w.OnMaximize(resized)
	w.OnUnmaximize(resized)
	w.OnEnterFullScreen(resized)
	w.OnLeaveFullScreen(resized)
}

// reviveGap is how long a page must have run since it was last reloaded
// after its process stopped before it is reloaded again, so a page that
// stops as it loads is not reloaded over and over.
const reviveGap = 30 * time.Second

// reviver decides whether a page whose process stopped is reloaded.
type reviver struct{ last time.Time }

func (r *reviver) allow(now time.Time) bool {
	if !r.last.IsZero() && now.Sub(r.last) < reviveGap {
		return false
	}
	r.last = now
	return true
}

// Label is a window's label, "" for none.
func Label(w *mygo.Window) string {
	if w == nil {
		return ""
	}
	label, _ := labels.Load(w)
	text, _ := label.(string)
	return text
}

// SettingsSnapshot is what the settings page shows, for the services that
// save a part of the preferences on their own.
func (h *Host) SettingsSnapshot() SettingsSnapshot { return h.settingsSnapshot() }

// settingsSnapshot is what the settings page shows. The backups are
// rescanned every time, so a backup the user deletes stops showing.
func (h *Host) settingsSnapshot() SettingsSnapshot {
	snapshot := SettingsSnapshot{
		Snapshot:    h.prefs.Snapshot(),
		DataBackups: core.ScanBackupFiles(h.dirs.Data),
	}
	if actual, err := h.autostart.Enabled(); err != nil {
		message := prefs.T(snapshot.Values.Locale, "host.autostart.readFailed", "error", err.Error())
		snapshot.AutostartError = &message
	} else {
		snapshot.AutostartActual = &actual
	}
	if storage := h.core.LocalKeyStorage(); storage != "" {
		snapshot.LocalKeyStorage = &storage
	}
	return snapshot
}

// updatePreferences saves values, first moving the login item to match and
// moving it back when the save fails, then applies them.
func (h *Host) updatePreferences(values prefs.Preferences) error {
	if err := values.Validate(); err != nil {
		return err
	}
	locale := values.Locale
	previous := h.prefs.Values()
	actual, err := h.autostart.Enabled()
	if err != nil {
		return errors.New(prefs.T(locale, "host.autostart.readFailedUnsaved", "error", err.Error()))
	}
	if values.Autostart != actual {
		if err := h.autostart.SetEnabled(values.Autostart); err != nil {
			key := "host.autostart.disableFailed"
			if values.Autostart {
				key = "host.autostart.enableFailed"
			}
			return errors.New(prefs.T(locale, key, "error", err.Error()))
		}
		reconciled, err := h.autostart.Enabled()
		if err != nil {
			return errors.New(prefs.T(locale, "host.autostart.verifyFailed", "error", err.Error()))
		}
		if reconciled != values.Autostart {
			return errors.New(prefs.T(locale, "host.autostart.mismatch"))
		}
	}
	if _, persistErr := h.prefs.Replace(values); persistErr != nil {
		if values.Autostart == actual {
			return persistErr
		}
		if err := h.autostart.SetEnabled(actual); err != nil {
			return errors.New(prefs.T(locale, "host.autostart.rollbackFailed",
				"persist_error", persistErr.Error(), "rollback_error", err.Error()))
		}
		return errors.New(prefs.T(locale, "host.autostart.rollbackOk", "persist_error", persistErr.Error()))
	}
	h.core.Configure(values)
	h.mu.Lock()
	hooks := append([]func(previous, current prefs.Preferences){}, h.preferencesChanged...)
	h.mu.Unlock()
	for _, fn := range hooks {
		fn(previous, values)
	}
	QuotaDisplayModeChanged.Broadcast(string(values.QuotaDisplayMode))
	h.applyTheme(values.Theme)
	ThemePreferenceChanged.Broadcast(string(values.Theme))
	return nil
}

// decorationLayout is GTK's button layout, as gtk-decoration-layout spells
// it, on Linux.
func decorationLayout() *string {
	if runtime.GOOS != "linux" {
		return nil
	}
	buttons := mygo.Theme.WindowButtons()
	layout := strings.Join(buttons.Start, ",") + ":" + strings.Join(buttons.End, ",")
	return &layout
}
