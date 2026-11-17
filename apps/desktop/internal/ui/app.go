package ui

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/core"
	"github.com/QuantumNous/astrlink/desktop/internal/currency"
	"github.com/QuantumNous/astrlink/desktop/internal/darwin"
	"github.com/QuantumNous/astrlink/desktop/internal/devreload"
	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
	"github.com/QuantumNous/astrlink/desktop/internal/raw"
	"github.com/QuantumNous/astrlink/desktop/internal/toys"
)

const mac = runtime.GOOS == "darwin"

// App runs the windows around the main one and the app's place in the
// system: it places the main window and hides it to the tray, keeps the
// tray and its popover, the trajectory inspectors and the raw access
// approval window, and answers a second launch, links and the session
// lock.
type App struct {
	h          *host.Host
	startup    Startup
	reclaim    *Reclaim
	tray       *trayController
	inspectors inspectorRegistry
	rates      atomic.Pointer[currency.Rates]
	// toys is the desktop pet, once the host started.
	toys atomic.Pointer[toys.Runtime]

	// quitting is set once a quit started, so closing a window no longer
	// hides it.
	quitting atomic.Bool
	// builds counts the main windows built; the first is the startup one.
	builds atomic.Int32
	// notices are the notices shown once.
	notices notices
	// stop ends the background work at quit.
	stopOnce sync.Once
	stop     context.CancelFunc

	// legacyStorage is the main window's script handing the pages what
	// the Tauri shell's webview stored, read once.
	legacyStorage func() string
}

// New returns the app's window runtime and registers its lifecycle hooks.
// Call it before App.Run; it starts once the host started.
func New(h *host.Host) *App {
	a := &App{h: h}
	current.Store(a)
	a.reclaim = NewReclaim(a.lookup)
	a.tray = newTray(a)
	a.legacyStorage = sync.OnceValue(func() string { return legacyStorageScript(readLegacyStorage(h.Instance().Identifier())) })
	h.OnMainWindowPreload(a.legacyStorage)
	h.OnPreload(toysPreload)
	h.OnMainWindowOpened(a.mainOpened)
	h.OnStarted(a.start)
	h.OnRecordsChanged(a.tray.recordsChanged)
	h.OnPreferencesChanged(a.preferencesChanged)

	app := mygo.App
	app.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting.Store(true) })
	app.OnQuitCanceled(func() { a.quitting.Store(false) })
	app.OnWillQuit(func(*mygo.QuitEvent) { a.end() })
	// Releasing hidden windows can close the last one; AstrLink stays in
	// the tray until it is told to quit.
	app.OnWindowAllClosed(func() {})
	// The Dock icon (macOS).
	app.OnActivate(func(bool) {
		if !a.quitting.Load() {
			a.ShowMain()
		}
	})
	app.OnSecondInstance(a.secondInstance)
	app.OnOpenURL(a.openURL)

	// The raw unlock and every agent grant end when the operator leaves.
	lock := a.sessionLock()
	mygo.Power.OnSuspend(func() { lock.Lock("sleep") })
	mygo.Power.OnLockScreen(func() { lock.Lock("screen lock") })
	return a
}

// Services are the bound services of windows, the tray, inspectors, files,
// the display currency and the desktop pet
// (src/native-host/commands-window.ts).
func (a *App) Services() []any {
	return []any{MainWindow{a}, Files{}, Currency{a}, Tray{a}, TrajectoryInspector{a}, Toys{a}}
}

func (a *App) prefs() prefs.Preferences {
	if store := a.h.PreferencesStore(); store != nil {
		return store.Values()
	}
	return prefs.Preferences{}
}

func (a *App) locale() prefs.Locale { return a.prefs().Locale }

// start runs on the main thread once the host started: Core, the
// preferences and the main window are there.
func (a *App) start() {
	ctx, stop := context.WithCancel(context.Background())
	a.stop = stop
	go a.removeLegacyAutostart()
	a.startCurrency(ctx)
	a.tray.start(ctx)
	a.startToys(ctx)
	lock := a.sessionLock()
	go lock.Watch(ctx)
	if mac {
		darwin.OnSessionResignActive(func() { lock.Lock("a user switch") })
	}
	if devURL := os.Getenv("MYGO_DEV_URL"); mygo.IsDev() && devURL != "" {
		go devreload.Run(ctx, devURL, reloadPages)
	}
}

// end stops the background work as the app quits.
func (a *App) end() {
	a.stopOnce.Do(func() {
		if a.stop != nil {
			a.stop()
		}
	})
}

func (a *App) sessionLock() raw.SessionLock {
	return raw.SessionLock{
		Raw: a.h.Raw(),
		Ready: func() bool {
			manager := a.h.CoreManager()
			if manager == nil {
				return false
			}
			view, _ := manager.View()
			return view.Phase == core.PhaseReady
		},
	}
}

// reloadPages reloads every page, for the dev reload supervisor.
func reloadPages() bool {
	reloaded := false
	for _, w := range mygo.Windows() {
		if !w.IsDestroyed() {
			w.Page().Reload()
			reloaded = true
		}
	}
	return reloaded
}

func (a *App) removeLegacyAutostart() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	legacy := legacyAutostart{goos: runtime.GOOS, home: home, name: a.h.Instance().Name(), run: systemLegacyRunEntries()}
	removed, err := legacy.Remove()
	for _, entry := range removed {
		log.Printf("astrlink: removed the login entry of the previous release: %s", entry)
	}
	if err != nil {
		log.Printf("astrlink: unable to remove the login entry of the previous release: %v", err)
	}
}

// ---- The main window ----

// startHidden reports whether this launch keeps the main window in the
// tray: a login launch with both autostart preferences on.
func startHidden(values prefs.Preferences, args []string, openedAtLogin bool) bool {
	login := openedAtLogin || slices.Contains(args, LegacyAutostartArg)
	return values.Autostart && values.AutostartHidden && login
}

// mainOpened places each main window as it is built, hidden, and shows it.
func (a *App) mainOpened(w *mygo.Window) {
	w.OnClose(func(e *mygo.CloseEvent) { a.mainClosing(w, e) })
	w.OnClosed(func() { a.windowClosed(host.MainLabel) })
	if a.builds.Add(1) > 1 {
		// Built again after the hidden one was released: where it was.
		a.startup.Restore(w)
		w.OnReadyToShow(func() { bringToFront(w) })
		return
	}
	if err := a.startup.FitToDisplay(w); err != nil {
		log.Printf("astrlink: unable to size the main window for the current display: %v", err)
	}
	if startHidden(a.prefs(), os.Args[1:], mygo.App.WasOpenedAtLogin()) {
		a.reclaim.Hidden(host.MainLabel)
		if mac {
			mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
		}
		return
	}
	w.OnReadyToShow(w.Show)
}

func (a *App) mainClosing(w *mygo.Window, e *mygo.CloseEvent) {
	if a.quitting.Load() {
		return
	}
	e.PreventDefault()
	if a.prefs().CloseBehavior == prefs.CloseHideToTray {
		a.hideMainToTray(w)
		return
	}
	// Closing this window alone ends nothing while the tray popover or a
	// pinned inspector is still open.
	mygo.App.Quit()
}

func (a *App) hideMainToTray(w *mygo.Window) {
	a.startup.Remember(w)
	w.Hide()
	a.reclaim.Hidden(host.MainLabel)
	// Hiding a window leaves a regular macOS app in the Dock; an accessory
	// keeps the tray and pinned inspectors.
	if mac {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
	// An inspector following the list must not stay on screen showing a
	// request the operator can no longer reach. Pinned ones stay.
	a.closeUnpinnedInspectors()
	// An in-window toast is not seen once the window hides. A notification
	// says where the window went the first time only: from then on, closing
	// to the tray is what the operator expects of it.
	path := a.noticesPath()
	if !a.notices.Seen(path, noticeHiddenToTray) {
		a.notifyHidden(path)
	}
}

// notifyHidden shows hiddenNotification, which brings the window back when
// clicked, and keeps that it was shown.
func (a *App) notifyHidden(path string) {
	n := mygo.NewNotification(hiddenNotification(a.locale()))
	n.OnClick(a.ShowMain)
	if err := n.Show(); err != nil {
		log.Printf("astrlink: unable to show a notification: %v", err)
		return
	}
	if err := a.notices.Record(path, noticeHiddenToTray); err != nil {
		log.Printf("astrlink: unable to keep that the tray notice was shown: %v", err)
	}
}

// noticesPath is the file of the notices shown, none when the config
// directory is unknown.
func (a *App) noticesPath() string {
	if config := a.h.Directories().Config; config != "" {
		return filepath.Join(config, noticesFile)
	}
	return ""
}

// ShowMain brings the main window up, building it again when the hidden
// one was released.
func (a *App) ShowMain() {
	if mac {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	}
	switch a.reclaim.Show(host.MainLabel) {
	case ShowExisting:
		if w := a.h.MainWindow(); w != nil {
			bringToFront(w)
		}
	case ShowBuild:
		a.h.OpenMainWindow()
		a.reclaim.Built(host.MainLabel)
	case ShowPending:
	}
}

func bringToFront(w *mygo.Window) {
	w.Show()
	if w.IsMinimized() {
		w.Restore()
	}
	w.Focus()
}

// lookup finds a live window by label, for Reclaim.
func (a *App) lookup(label string) ReclaimedWindow {
	var w *mygo.Window
	switch label {
	case host.MainLabel:
		w = a.h.MainWindow()
	case PopoverLabel:
		w = a.tray.popoverWindow()
	default:
		w = windowByLabel(label)
	}
	if w == nil || w.IsDestroyed() {
		return nil
	}
	return w
}

func windowByLabel(label string) *mygo.Window {
	for _, w := range mygo.Windows() {
		if host.Label(w) == label && !w.IsDestroyed() {
			return w
		}
	}
	return nil
}

// windowClosed forgets a window that closed and builds it again when a
// show arrived while it was released.
func (a *App) windowClosed(label string) {
	if strings.HasPrefix(label, InspectorLabelPrefix) {
		a.inspectors.mu.Lock()
		a.inspectors.remove(label)
		a.inspectors.mu.Unlock()
	}
	if !a.reclaim.Destroyed(label) || a.quitting.Load() {
		return
	}
	switch label {
	case host.MainLabel:
		go a.ShowMain()
	case PopoverLabel:
		go a.tray.reopen()
	}
}

// ---- Launches and links ----

// secondInstance answers a second launch: it shows the main window unless
// it is a login launch that stays in the tray. Its links reach openURL.
func (a *App) secondInstance(args []string, _ string) {
	if startHidden(a.prefs(), args, false) {
		return
	}
	a.ShowMain()
}

// openURL takes the instance's own links; others are not AstrLink's.
func (a *App) openURL(link string) {
	scheme, _, ok := strings.Cut(link, ":")
	if !ok || !strings.EqualFold(scheme, a.h.Instance().LinkScheme()) {
		return
	}
	if !links.deliver(link) {
		// Nothing imports links yet; the window at least comes up.
		a.ShowMain()
	}
}

// linkInbox holds the last link until something takes links.
type linkInbox struct {
	mu      sync.Mutex
	receive func(link string)
	pending string
}

var links linkInbox

// current is the App New returned, which ShowMainWindow brings up.
var current atomic.Pointer[App]

// ShowMainWindow brings the main window up, building it again when the
// hidden one was released, for code that takes links outside the window
// runtime. It does nothing before New.
func ShowMainWindow() {
	if a := current.Load(); a != nil {
		a.ShowMain()
	}
}

// ReceiveLinks hands every link of the instance's scheme
// (astrlink://, astrlink-isolated:// in development) to fn, starting with
// one that arrived before, such as the link the app was launched with. fn
// runs on the main thread and shows the main window itself
// (ShowMainWindow); one link is confirmed at a time, so only the last one
// waiting is kept.
func ReceiveLinks(fn func(link string)) {
	links.mu.Lock()
	links.receive = fn
	pending := links.pending
	links.pending = ""
	links.mu.Unlock()
	if pending != "" {
		mygo.RunOnMain(func() { fn(pending) })
	}
}

// deliver hands link over, or keeps it; it reports whether it was handed.
func (l *linkInbox) deliver(link string) bool {
	l.mu.Lock()
	receive := l.receive
	if receive == nil {
		l.pending = link
	}
	l.mu.Unlock()
	if receive == nil {
		return false
	}
	receive(link)
	return true
}

// ---- Preferences and the currency ----

func (a *App) preferencesChanged(previous, current prefs.Preferences) {
	if previous.DisplayCurrency != current.DisplayCurrency || previous.UseSystemProxy != current.UseSystemProxy {
		if rates := a.rates.Load(); rates != nil {
			rates.Wake()
		}
		a.currencyChanged()
	}
	if previous.Locale != current.Locale {
		a.inspectorLocaleChanged(current.Locale)
		// The pet says things.
		if runtime := a.toys.Load(); runtime != nil {
			runtime.PrefsChanged()
		}
	}
	a.tray.preferencesChanged()
}

func (a *App) startCurrency(ctx context.Context) {
	rates := currency.Load(a.h.Directories().Config)
	rates.Choice = func() string { return a.prefs().DisplayCurrency }
	rates.UseSystemProxy = func() bool { return a.prefs().UseSystemProxy }
	system := currency.SystemCurrency(slices.Concat(
		[]string{mygo.App.Region().Locale, currency.PosixLocale(os.Getenv)},
		mygo.App.PreferredLanguages()))
	rates.System = func() string { return system }
	rates.Changed = a.currencyChanged
	a.rates.Store(rates)
	go rates.Run(ctx)
}

// display is the currency amounts show in, USD before the rates load.
func (a *App) display() currency.Display {
	if rates := a.rates.Load(); rates != nil {
		return rates.Display()
	}
	return currency.USD()
}

func (a *App) currencyChanged() {
	_ = host.DisplayCurrencyChanged.Broadcast(a.display())
	a.tray.refresh()
}

// notify shows a system notification; a failure is only logged, so it
// never holds up what raised it.
func notify(title, body string) {
	if err := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body}).Show(); err != nil {
		log.Printf("astrlink: unable to show a notification: %v", err)
	}
}
