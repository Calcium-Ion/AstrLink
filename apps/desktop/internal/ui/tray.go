package ui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"log"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/core"
	"github.com/QuantumNous/astrlink/desktop/internal/darwin"
	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
	"github.com/QuantumNous/astrlink/desktop/internal/raw"
	"github.com/QuantumNous/astrlink/desktop/internal/widget"
)

// PopoverLabel is the tray popover's label (TRAY_POPOVER_LABEL in the
// frontend).
const PopoverLabel = "tray-popover"

const (
	schedulerTick         = 5 * time.Second
	localRefreshHidden    = 30 * time.Second
	planRefreshVisible    = 120 * time.Second
	planRefreshBackground = 300 * time.Second
	clickRefreshDebounce  = 10 * time.Second
	// digestTimeout bounds one collection; the panel keeps its numbers
	// past it.
	digestTimeout = 45 * time.Second
	copiedFlash   = 1500 * time.Millisecond
	// popoverReopenGuard: the press on the tray icon that closed the
	// popover must not open it again with the click that follows.
	popoverReopenGuard = 350 * time.Millisecond
	// popoverHandoffTimeout is how long a dismissal waits for the app it
	// hands the foreground to before it hides the popover anyway.
	popoverHandoffTimeout = 400 * time.Millisecond
	// menuRelease is how long the panel waits to open from the Linux tray
	// menu. The desktop's panel draws that menu and holds the keyboard
	// while it shows, and tells the click before it lets go: a panel shown
	// at once does not get the keyboard, and closes only as it loses it.
	menuRelease = 200 * time.Millisecond
	popoverGap  = 2
)

// The panel opens from the tray menu on Linux, whose tray reports no
// clicks; MyGo places a tray flyout in the middle of the primary display's
// work area there.
const linuxMenu = runtime.GOOS == "linux"

//go:embed icons/*.png
var icons embed.FS

// iconFor is the image of a state, and whether macOS tints it as a
// template. Running is the template glyph; idle is a fixed black glyph and
// watched a light one with a red badge, which a template would lose.
// Windows and Linux get the color mark.
func iconFor(state TrayIconState) ([]byte, bool) {
	name := "icons/color-" + string(state) + ".png"
	if mac {
		name = "icons/mac-" + string(state) + ".png"
	}
	png, err := icons.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return png, mac && state == TrayReady
}

// planRefresh is how a refresh treats the upstream plan quota lookups.
type planRefresh struct {
	kind   planKind
	maxAge time.Duration
}

type planKind int

const (
	// planReuse reuses whatever is cached, however old; never upstream.
	planReuse planKind = iota
	// planIfOlder calls upstream unless the cache is younger than maxAge.
	planIfOlder
	// planForce calls upstream now, and has Core skip its own snapshot.
	planForce
)

func ifOlderThan(age time.Duration) planRefresh { return planRefresh{kind: planIfOlder, maxAge: age} }

// trayController keeps the tray icon, its popover and the usage digest
// the popover shows.
type trayController struct {
	a   *App
	now func() time.Time

	mu       sync.Mutex
	tray     *mygo.Tray
	popover  *mygo.Window
	model    *TrayModel
	menuKey  string
	digest   *UsageDigest
	digestAt time.Time
	// subscriptionsAt is when the cached plans were read, zero for none.
	subscriptionsAt  time.Time
	lastClickRefresh time.Time
	refreshing       bool
	// popoverSize is the content size the page last asked for, in DIPs.
	popoverWidth, popoverHeight float64
	popoverHiddenAt             time.Time
	// returnTo is the app the popover took the foreground from (macOS).
	returnTo int
	// handoff: a dismissal waits for that app to take the foreground.
	handoff bool
	// navigation is the page last asked of the main window, until it
	// takes it.
	navigation  *string
	copiedUntil time.Time
	// widget feeds the macOS desktop widget; nil without one.
	widget *widget.Feed
	// widgetPlans is the last plan read the widget can show, read at
	// widgetReadAt (zero for none). It outlives the digest: a stopped
	// gateway leaves the widget its last numbers, which it marks as old.
	widgetPlans  []SubscriptionDigest
	widgetReadAt time.Time
}

func newTray(a *App) *trayController {
	return &trayController{a: a, now: time.Now, popoverWidth: PopoverWidth, popoverHeight: popoverInitialHeight, widget: widget.Default()}
}

func (t *trayController) view() core.View {
	if manager := t.a.h.CoreManager(); manager != nil {
		view, _ := manager.View()
		return view
	}
	return core.View{Phase: core.PhaseStopped}
}

func (t *trayController) currentModel() TrayModel {
	values := t.a.prefs()
	t.mu.Lock()
	digest := t.digest
	t.mu.Unlock()
	return trayModel(t.view(), values.Tray, digest, values.Locale, values.QuotaDisplayMode, t.a.display(), mac)
}

// start builds the tray icon and starts following Core and the usage
// ticker, until ctx ends. Main thread.
func (t *trayController) start(ctx context.Context) {
	model := t.currentModel()
	png, template := iconFor(model.Icon)
	options := mygo.TrayOptions{ID: "main", Icon: png, IconIsTemplate: template, ToolTip: model.Tooltip}
	if mac && model.Title != nil {
		options.Title = *model.Title
	}
	var menuKey string
	if linuxMenu {
		options.Menu, menuKey = t.menu(model)
	}
	tray, err := mygo.NewTray(options)
	if err != nil {
		log.Printf("astrlink: unable to show the tray icon: %v", err)
		return
	}
	t.mu.Lock()
	t.tray, t.model, t.menuKey = tray, &model, menuKey
	t.mu.Unlock()
	if !linuxMenu {
		// Any click, whichever button, toggles the popover.
		tray.OnClick(t.toggle)
		tray.OnRightClick(t.toggle)
	}
	go t.watchCore(ctx)
	go t.schedule(ctx)
}

// menu is the Linux tray menu: the status as a disabled row, gateway
// controls for the current phase, the panel and the window actions. Its
// key changes whenever anything it shows does.
func (t *trayController) menu(model TrayModel) (*mygo.Menu, string) {
	values := t.a.prefs()
	locale := values.Locale
	phase := t.view().Phase
	item := func(key string, click func()) *mygo.MenuItem {
		return &mygo.MenuItem{ID: key, Label: prefs.T(locale, key), Click: func(*mygo.MenuItem, *mygo.Window) { click() }}
	}
	items := []*mygo.MenuItem{{Label: model.Tooltip, Disabled: true}}
	if values.Tray.GatewayControls {
		switch phase {
		case core.PhaseReady:
			items = append(items, mygo.Separator(),
				item("host.tray.core.restart", func() { t.runCore(CoreRestart) }),
				item("host.tray.core.stop", func() { t.runCore(CoreStop) }))
		case core.PhaseStopped, core.PhaseExited, core.PhaseError:
			items = append(items, mygo.Separator(),
				item("host.tray.core.start", func() { t.runCore(CoreStart) }))
		}
	}
	items = append(items, mygo.Separator(),
		item("host.tray.panel", func() { time.AfterFunc(menuRelease, t.open) }),
		item("host.tray.show", t.a.ShowMain),
		item("host.tray.settings", func() { t.navigate("settings") }),
		item("host.tray.quit", mygo.App.Quit))
	key := string(locale) + "\x00" + model.Tooltip + "\x00" + string(phase)
	if values.Tray.GatewayControls {
		key += "\x00controls"
	}
	return mygo.NewMenu(items), key
}

// refresh derives the icon again, shows it when it changed, and hands an
// open popover a fresh snapshot.
func (t *trayController) refresh() {
	model := t.currentModel()
	var menu *mygo.Menu
	var menuKey string
	if linuxMenu {
		menu, menuKey = t.menu(model)
	}
	t.mu.Lock()
	tray := t.tray
	changed := t.model == nil || !t.model.equal(model)
	menuChanged := linuxMenu && menuKey != t.menuKey
	copied := t.now().Before(t.copiedUntil)
	t.model = &model
	t.menuKey = menuKey
	popover := t.popover
	plans, readAt := t.widgetPlans, t.widgetReadAt
	t.mu.Unlock()
	if !readAt.IsZero() {
		values := t.a.prefs()
		t.widget.Publish(widget.NewSnapshot(widgetPlans(plans), readAt, values.Locale, values.QuotaDisplayMode))
	}
	if tray != nil && (changed || menuChanged) {
		t.render(tray, model, menu, copied)
	}
	if popover != nil && !popover.IsDestroyed() {
		_ = host.TrayState.Emit(popover, t.snapshot(nil))
	}
}

func (t *trayController) render(tray *mygo.Tray, model TrayModel, menu *mygo.Menu, copied bool) {
	png, template := iconFor(model.Icon)
	if err := tray.SetIcon(png, template); err != nil {
		log.Printf("astrlink: unable to update the tray icon: %v", err)
	}
	tray.SetToolTip(model.Tooltip)
	// An empty title clears the previous choice's text.
	if mac && !copied {
		tray.SetTitle(deref(model.Title))
	}
	if menu != nil {
		tray.SetMenu(menu)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// snapshot is what the popover renders, for the stored tray preferences
// or a draft of them.
func (t *trayController) snapshot(draft *prefs.TrayPreferences) TrayStateSnapshot {
	values := t.a.prefs()
	tray := values.Tray
	if draft != nil {
		tray = *draft
	}
	t.mu.Lock()
	digest := t.digest
	var age *uint64
	if !t.digestAt.IsZero() {
		ms := uint64(max(t.now().Sub(t.digestAt).Milliseconds(), 0))
		age = &ms
	}
	icon := t.tray
	t.mu.Unlock()
	if digest != nil {
		copied := *digest
		digest = &copied
	}
	return TrayStateSnapshot{
		AppVersion:   mygo.App.Version(),
		Platform:     host.PlatformName(),
		View:         t.view(),
		Digest:       digest,
		DigestAgeMS:  age,
		Tray:         tray,
		PopoverBelow: popoverBelow(icon),
	}
}

// popoverBelow is whether the popover hangs below the tray icon (a menu
// bar) or rises above it (a taskbar at the bottom).
func popoverBelow(tray *mygo.Tray) bool {
	if tray == nil || linuxMenu {
		return true
	}
	bounds := tray.Bounds()
	if bounds.Width == 0 && bounds.Height == 0 {
		return true
	}
	work := mygo.Screen.DisplayMatching(bounds).WorkArea
	if work.Width == 0 {
		return true
	}
	return anchorBelow(box(bounds), box(work))
}

func box(r mygo.Rectangle) Box {
	return Box{X: float64(r.X), Y: float64(r.Y), Width: float64(r.Width), Height: float64(r.Height)}
}

// ---- The popover ----

func (t *trayController) popoverWindow() *mygo.Window {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.popover == nil || t.popover.IsDestroyed() {
		return nil
	}
	return t.popover
}

func (t *trayController) popoverVisible() bool {
	w := t.popoverWindow()
	return w != nil && w.IsVisible()
}

// toggle answers a click on the tray icon.
func (t *trayController) toggle() {
	if t.popoverVisible() {
		t.dismiss()
		return
	}
	t.mu.Lock()
	closedByThisClick := !t.popoverHiddenAt.IsZero() && t.now().Sub(t.popoverHiddenAt) < popoverReopenGuard
	t.mu.Unlock()
	if !closedByThisClick {
		t.open()
	}
}

// open shows the popover, building it when the hidden one was released.
func (t *trayController) open() {
	t.mu.Lock()
	tray := t.tray
	t.mu.Unlock()
	if tray == nil {
		t.a.ShowMain()
		return
	}
	switch t.a.reclaim.Show(PopoverLabel) {
	case ShowExisting:
		t.present()
	case ShowBuild:
		w := t.buildPopover(tray)
		t.mu.Lock()
		t.popover = w
		t.mu.Unlock()
		t.a.reclaim.Built(PopoverLabel)
		t.present()
	case ShowPending:
	}
}

// reopen opens the popover a click asked for while it was released.
func (t *trayController) reopen() { t.open() }

func (t *trayController) buildPopover(tray *mygo.Tray) *mygo.Window {
	t.mu.Lock()
	width, height := t.popoverWidth, t.popoverHeight
	t.mu.Unlock()
	w := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:      tray,
		Placement: mygo.PlacementBottom,
		Gap:       popoverGap,
		Width:     int(math.Ceil(width)),
		Height:    int(math.Ceil(height)),
		// It takes the keyboard, and closes as a menu does: on a press
		// outside it, Escape, or when another app takes the foreground.
		Focusable: true,
		Hidden:    true,
		URL:       "/",
		Page:      mygo.PageOptions{PreloadScript: t.a.h.PreloadScript(PopoverLabel)},
	})
	host.Register(w, PopoverLabel)
	w.OnClose(func(e *mygo.CloseEvent) {
		if t.a.quitting.Load() {
			return
		}
		// Kept for the next click; Reclaim releases it later.
		e.PreventDefault()
		t.closing()
	})
	w.OnClosed(func() {
		t.mu.Lock()
		if t.popover == w {
			t.popover = nil
		}
		t.mu.Unlock()
		t.a.windowClosed(PopoverLabel)
	})
	return w
}

// present shows the existing popover, seeds it with the current state and
// asks for fresh numbers.
func (t *trayController) present() {
	w := t.popoverWindow()
	if w == nil {
		return
	}
	var returnTo int
	mygo.RunOnMain(func() {
		// Read before showing: showing activates AstrLink.
		returnTo = darwin.FrontmostOtherApp()
		w.Show()
	})
	t.mu.Lock()
	t.returnTo, t.handoff = returnTo, false
	t.mu.Unlock()
	_ = host.TrayState.Emit(w, t.snapshot(nil))
	t.requestRefresh(false, ifOlderThan(planRefreshVisible))
}

// hide hides the popover and starts its release countdown.
func (t *trayController) hide() {
	w := t.popoverWindow()
	if w == nil || !w.IsVisible() {
		return
	}
	w.Hide()
	t.a.reclaim.Hidden(PopoverLabel)
	t.mu.Lock()
	t.popoverHiddenAt, t.handoff = t.now(), false
	t.mu.Unlock()
}

// dismiss closes the popover at the operator's request: Escape, its close
// button, a click beside the panel or on the tray icon. Hiding the key
// window of the active app makes macOS raise the app's next window, so
// the main window would jump in front of what the operator was using.
// When the popover took the foreground from another app, that app gets it
// back first, and the popover hides as AstrLink deactivates.
func (t *trayController) dismiss() {
	if !t.popoverVisible() {
		return
	}
	t.mu.Lock()
	if t.handoff {
		t.mu.Unlock()
		return
	}
	returnTo := t.returnTo
	t.returnTo = 0
	t.mu.Unlock()
	handed := false
	if returnTo > 0 {
		mygo.RunOnMain(func() { handed = darwin.ReturnForeground(returnTo) })
	}
	if !handed {
		t.hide()
		return
	}
	t.mu.Lock()
	t.handoff = true
	t.mu.Unlock()
	time.AfterFunc(popoverHandoffTimeout, func() {
		t.mu.Lock()
		pending := t.handoff
		t.mu.Unlock()
		if pending {
			t.hide()
		}
	})
}

// closing answers the popover closing as a menu does. A dismissal waiting
// for the other app to take the foreground ends here; otherwise it is
// dismissed.
func (t *trayController) closing() {
	t.mu.Lock()
	handoff := t.handoff
	t.mu.Unlock()
	if handoff {
		t.hide()
		return
	}
	t.dismiss()
}

// resize follows the content height the popover reports, height CSS
// pixels drawn at devicePixelRatio.
func (t *trayController) resize(height, devicePixelRatio float64) error {
	if math.IsNaN(height) || math.IsInf(height, 0) {
		return errors.New("popover height must be finite")
	}
	height = math.Ceil(math.Min(math.Max(height, popoverMinHeight), popoverMaxHeight))
	w := t.popoverWindow()
	scale := devicePixelRatio
	if w != nil {
		if s := displayOf(w).ScaleFactor; positive(s) {
			scale = s
		}
	}
	width, height := popoverSize(height, devicePixelRatio, scale)
	t.mu.Lock()
	t.popoverWidth, t.popoverHeight = width, height
	t.mu.Unlock()
	if w != nil {
		// A flyout placed again keeps to its icon and the work area.
		w.SetContentSize(int(math.Ceil(width)), int(math.Ceil(height)))
	}
	return nil
}

// ---- Actions ----

// perform runs an action the popover asks for.
func (t *trayController) perform(action TrayAction) error {
	switch action.Kind {
	case TrayOpen:
		t.hide()
		t.a.ShowMain()
	case TrayNavigate:
		if err := checkNavigation(action.Page); err != nil {
			return err
		}
		t.hide()
		t.navigate(action.Page)
	case TrayCopyAddress:
		return t.copyAddress()
	case TrayCore:
		t.runCore(action.Op)
	case TrayRefresh:
		// An explicit refresh means now, plans included.
		t.requestRefresh(true, planRefresh{kind: planForce})
	case TrayRawAccess:
		t.hide()
		t.a.openApproval(true)
	case TrayQuit:
		mygo.App.Quit()
	}
	return nil
}

// navigate keeps the page for the main window to take: a window built
// again listens only once it loaded.
func (t *trayController) navigate(page string) {
	t.mu.Lock()
	t.navigation = &page
	t.mu.Unlock()
	t.a.ShowMain()
	if w := t.a.h.MainWindow(); w != nil {
		_ = host.TrayNavigate.Emit(w, struct{}{})
	}
}

func (t *trayController) takeNavigation() *string {
	t.mu.Lock()
	defer t.mu.Unlock()
	page := t.navigation
	t.navigation = nil
	return page
}

func (t *trayController) runCore(op CoreOp) {
	manager := t.a.h.CoreManager()
	if manager == nil {
		return
	}
	go func() {
		var err error
		switch op {
		case CoreStart:
			// It may wait on a keychain prompt.
			err = manager.Start()
		case CoreStop:
			err = manager.StopAndWait(context.Background())
		case CoreRestart:
			err = manager.Restart(context.Background())
		}
		if err != nil {
			log.Printf("astrlink: unable to %s astrlink-core from the tray: %v", op, err)
		}
	}()
}

func (t *trayController) copyAddress() error {
	view := t.view()
	locale := t.a.locale()
	if view.InferenceURL == nil {
		return errors.New(prefs.T(locale, "host.sidecar.notReady"))
	}
	mygo.Clipboard.WriteText(*view.InferenceURL)
	// The status item is the one place still seen once the popover
	// closes. Other platforms stay silent rather than raise a
	// notification.
	if !mac {
		return nil
	}
	t.mu.Lock()
	tray := t.tray
	t.copiedUntil = t.now().Add(copiedFlash)
	t.mu.Unlock()
	if tray == nil {
		return nil
	}
	tray.SetTitle(prefs.T(locale, "host.tray.copied"))
	time.AfterFunc(copiedFlash, func() {
		t.mu.Lock()
		var title string
		if t.model != nil {
			title = deref(t.model.Title)
		}
		t.mu.Unlock()
		tray.SetTitle(title)
	})
	return nil
}

// ---- The usage digest ----

func (t *trayController) recordsChanged() {
	// Today's figures count the records; plans are unaffected.
	t.requestRefresh(true, planRefresh{kind: planReuse})
}

func (t *trayController) preferencesChanged() {
	t.refresh()
	t.requestRefresh(true, planRefresh{kind: planReuse})
}

// requestRefresh schedules a usage refresh. A click is debounced; a state
// change is not.
func (t *trayController) requestRefresh(immediate bool, plans planRefresh) {
	if !immediate {
		t.mu.Lock()
		now := t.now()
		if !t.lastClickRefresh.IsZero() && now.Sub(t.lastClickRefresh) < clickRefreshDebounce {
			t.mu.Unlock()
			return
		}
		t.lastClickRefresh = now
		t.mu.Unlock()
	}
	go t.refreshUsage(context.Background(), plans)
}

// tickPlan is what a scheduler tick does: false skips it, otherwise the
// local numbers are read and plans follow the policy.
func (t *trayController) tickPlan(visible bool, menubar prefs.TrayMenubarText) (planRefresh, bool) {
	if visible {
		return ifOlderThan(planRefreshVisible), true
	}
	t.mu.Lock()
	now := t.now()
	localDue := t.digestAt.IsZero() || now.Sub(t.digestAt) >= localRefreshHidden
	plansDue := t.subscriptionsAt.IsZero() || now.Sub(t.subscriptionsAt) >= planRefreshBackground
	t.mu.Unlock()
	if !localDue {
		return planRefresh{}, false
	}
	// The menu bar title and a placed desktop widget show plan figures,
	// so they get a background cadence, and nothing faster.
	if (menubar == prefs.MenubarSubscription || t.widget.Placed()) && plansDue {
		return ifOlderThan(planRefreshBackground), true
	}
	return planRefresh{kind: planReuse}, true
}

func (t *trayController) schedule(ctx context.Context) {
	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if plans, ok := t.tickPlan(t.popoverVisible(), t.a.prefs().Tray.MenubarText); ok {
			t.refreshUsage(ctx, plans)
		}
	}
}

// refreshUsage collects the digest while the gateway is ready and shows
// it. One runs at a time.
func (t *trayController) refreshUsage(ctx context.Context, plans planRefresh) {
	if t.view().Phase != core.PhaseReady {
		return
	}
	usage := collectedUsage(t.a.prefs().Tray, t.widget.Placed())
	t.mu.Lock()
	if t.refreshing {
		t.mu.Unlock()
		return
	}
	t.refreshing = true
	var previous []SubscriptionDigest
	previousAt := t.subscriptionsAt
	if t.digest != nil {
		previous = t.digest.Subscriptions
	}
	t.mu.Unlock()
	// Whatever happens below, the next refresh must be allowed to run.
	defer func() {
		t.mu.Lock()
		t.refreshing = false
		t.mu.Unlock()
	}()

	var digest *UsageDigest
	var subscriptionsAt time.Time
	if usage.NeedsAnything() {
		collectCtx, cancel := context.WithTimeout(ctx, digestTimeout)
		collected, at := collectDigest(collectCtx, &control.API{Caller: t.a.h}, usage, previous, previousAt, plans, t.now())
		timedOut := errors.Is(collectCtx.Err(), context.DeadlineExceeded)
		cancel()
		if timedOut {
			log.Printf("astrlink: the tray's usage digest took over %s; keeping the previous numbers", digestTimeout)
			return
		}
		if ctx.Err() != nil {
			return
		}
		digest, subscriptionsAt = &collected, at
	}
	t.mu.Lock()
	if digest != nil {
		if plans, ok := widgetRead(*digest, previousAt, subscriptionsAt); ok {
			t.widgetPlans, t.widgetReadAt = plans, t.now()
		}
	}
	t.digest, t.digestAt, t.subscriptionsAt = digest, t.now(), subscriptionsAt
	t.mu.Unlock()
	t.refresh()
}

// widgetRead is the plans a refresh just read for the desktop widget: a
// new read, or a fresh service list with no plans in it. A cached read
// changes nothing, and an empty read with plans listed is a failed lookup,
// after which the widget keeps its last numbers.
func widgetRead(digest UsageDigest, previousAt, subscriptionsAt time.Time) ([]SubscriptionDigest, bool) {
	if !subscriptionsAt.IsZero() && !subscriptionsAt.Equal(previousAt) {
		return digest.Subscriptions, true
	}
	if digest.subscriptionServices != nil && *digest.subscriptionServices == 0 {
		return []SubscriptionDigest{}, true
	}
	return nil, false
}

// widgetPlans is the digest's plans as the widget snapshot lists them.
func widgetPlans(plans []SubscriptionDigest) []widget.Plan {
	out := make([]widget.Plan, 0, len(plans))
	for _, plan := range plans {
		windows := make([]widget.Window, 0, len(plan.Windows))
		for _, w := range plan.Windows {
			windows = append(windows, widget.Window{
				Label: w.Label, LimitWindowSeconds: w.LimitWindowSeconds, Secondary: w.Secondary,
				UsedPercent: w.UsedPercent, ResetAt: w.ResetAt,
			})
		}
		out = append(out, widget.Plan{ID: plan.ID, Name: plan.Name, Kind: plan.Kind, Windows: windows})
	}
	return out
}

// collectDigest reads only what the enabled cards need. Local numbers are
// always read; plan windows follow plans against the cached ones.
func collectDigest(ctx context.Context, api *control.API, usage prefs.TrayUsagePreferences,
	previous []SubscriptionDigest, previousAt time.Time, plans planRefresh, now time.Time) (UsageDigest, time.Time) {
	digest := newDigest()
	windows := localWindowsAt(now, localTimeZone())
	summaries := control.Usage{API: api}
	read := func(raw json.RawMessage, err error) (object, bool) {
		if err != nil {
			return nil, false
		}
		return objectOf(raw)
	}

	if usage.NeedsUsageSummary() {
		if summary, ok := read(summaries.GetUsageSummary(ctx, windows.todayFrom, windows.todayTo, windows.timeZone, "hour")); ok {
			digest.Today = totalsFrom(summary)
			digest.HourlyTokens = hourlyFrom(summary)
			if usage.TopModel {
				var total int64
				if digest.Today != nil {
					total = digest.Today.TotalTokens
				}
				digest.TopModel = topGroup(summary, total)
			}
		}
		if usage.CompareYesterday {
			if summary, ok := read(summaries.GetUsageSummary(ctx, windows.yesterdayFrom, windows.todayFrom, windows.timeZone, "day")); ok {
				if totals := totalsFrom(summary); totals != nil {
					digest.YesterdayTokens = &totals.TotalTokens
				}
			}
		}
	}

	if usage.Cost {
		input, _ := json.Marshal(map[string]string{"from": windows.todayFrom, "to": windows.todayTo})
		if summary, ok := read(control.Pricing{API: api}.Pricing(ctx, "summary", nil, input)); ok {
			digest.CostToday = costFrom(summary)
		}
	}

	if usage.Privacy {
		if summary, ok := read(summaries.GetPrivacySummary(ctx, windows.todayFrom, windows.todayTo, windows.timeZone, "day")); ok {
			// Read beside the counts, so the card can say protection is
			// off instead of showing zeros that look like a clean day.
			var enabled *bool
			if record, err := (control.Privacy{API: api}).GetPrivacyPolicy(ctx); err == nil {
				if policy, ok := objectOf(record.Policy); ok {
					if value, ok := field[bool](policy, "enabled"); ok {
						enabled = &value
					}
				}
			}
			digest.Privacy = privacyFrom(summary, enabled)
		}
	}

	if usage.TopClient {
		tokens, tokensOK := read(control.Tokens{API: api}.ListAccessTokens(ctx))
		tokenUsage, usageOK := read(summaries.ListAccessTokenUsage(ctx, windows.todayFrom))
		if tokensOK && usageOK {
			digest.TopClient = topClientFrom(tokens, tokenUsage)
		}
	}

	if usage.LastRequest {
		if page, ok := read(control.Records{API: api}.ListRequestRecords(ctx, json.RawMessage(`{"limit":1}`))); ok {
			digest.LastRequest = lastRequestFrom(page)
		}
	}

	if usage.MonthTotal {
		if summary, ok := read(summaries.GetUsageSummary(ctx, windows.monthFrom, windows.todayTo, windows.timeZone, "day")); ok {
			if totals := totalsFrom(summary); totals != nil {
				digest.MonthTokens = &totals.TotalTokens
			}
		}
	}

	var subscriptionsAt time.Time
	if usage.SubscriptionWindows {
		// Only a non-empty result counts as cached: an empty one usually
		// means the lookups failed, as right after start-up, before
		// upstream auth settles.
		cached := len(previous) > 0 && !previousAt.IsZero()
		reuse := false
		switch plans.kind {
		case planReuse:
			reuse = cached
		case planIfOlder:
			reuse = cached && now.Sub(previousAt) < plans.maxAge
		}
		switch {
		case reuse:
			digest.Subscriptions, subscriptionsAt = previous, previousAt
		case plans.kind == planReuse:
		default:
			subscriptions, listed := collectSubscriptions(ctx, control.Services{API: api}, plans.kind == planForce)
			digest.Subscriptions, digest.subscriptionServices = subscriptions, listed
			if len(subscriptions) > 0 {
				subscriptionsAt = now
			}
		}
	}
	return digest, subscriptionsAt
}

// collectSubscriptions reads the plans of the plan-backed services side by
// side, in the order Core lists them.
func collectSubscriptions(ctx context.Context, services control.Services, fresh bool) ([]SubscriptionDigest, *int) {
	page, err := services.ListServices(ctx)
	if err != nil {
		return []SubscriptionDigest{}, nil
	}
	listed := subscriptionServices(page.Items)
	count := len(listed)
	results := make([]*SubscriptionDigest, len(listed))
	var wg sync.WaitGroup
	for i, service := range listed {
		wg.Go(func() {
			usage, err := services.GetServiceUsage(ctx, service.id, &fresh)
			if err != nil {
				// The panel omits the plan until a refresh succeeds.
				log.Printf("astrlink: subscription usage of %s unavailable: %v", service.id, err)
				return
			}
			if parsed, ok := objectOf(usage); ok {
				if digest := subscriptionFrom(service.name, service.kind, parsed); digest != nil {
					digest.ID = service.id
					results[i] = digest
				}
			}
		})
	}
	wg.Wait()
	digests := []SubscriptionDigest{}
	for _, digest := range results {
		if digest != nil {
			digests = append(digests, *digest)
		}
	}
	return digests, &count
}

// ---- Core ----

// watchCore follows Core's view until ctx ends: it opens the approval
// window for a new raw access request, checks the raw key pin, warms and
// clears the digest, and shows the state in the tray.
func (t *trayController) watchCore(ctx context.Context) {
	manager := t.a.h.CoreManager()
	if manager == nil {
		return
	}
	var watcher coreWatcher
	for {
		view, changed := manager.View()
		step := watcher.observe(view)
		if step.openApproval {
			// Without focus, so no keystroke meant for another window
			// approves anything; a notification in case the operator
			// looks away.
			t.a.openApproval(false)
			locale := t.a.locale()
			notify(prefs.T(locale, "host.rawAccess.notificationTitle"), prefs.T(locale, "host.rawAccess.notificationBody"))
		}
		if step.floating != nil {
			t.a.setApprovalFloating(*step.floating)
		}
		if step.checkKey {
			// A key replaced while the app was closed, or through another
			// client of this Core, shows only against the pin.
			go raw.CheckKey(ctx, t.a.h.Raw())
		}
		if step.warm {
			// Warm the panel once so the first open is not blank.
			t.requestRefresh(true, ifOlderThan(planRefreshVisible))
		}
		if step.clear {
			// Numbers from a gateway that is gone would be stale on
			// return.
			t.mu.Lock()
			t.digest, t.digestAt, t.subscriptionsAt = nil, time.Time{}, time.Time{}
			t.mu.Unlock()
		}
		t.refresh()
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}

// coreWatcher turns Core views into what the tray does about them.
type coreWatcher struct {
	wasReady    bool
	keyEvent    *core.RawKeyEvent
	pendingRaws uint32
}

type coreStep struct {
	openApproval bool
	// floating is set when the approval window's level changes: it floats
	// only while a request waits.
	floating *bool
	checkKey bool
	warm     bool
	clear    bool
}

func (c *coreWatcher) observe(view core.View) coreStep {
	var step coreStep
	ready := view.Phase == core.PhaseReady
	step.openApproval = view.PendingRawAccess > c.pendingRaws
	if waiting := view.PendingRawAccess > 0; waiting != (c.pendingRaws > 0) {
		step.floating = &waiting
	}
	c.pendingRaws = view.PendingRawAccess
	step.checkKey = ready && (!c.wasReady || !sameKeyEvent(view.RawKeyEvent, c.keyEvent))
	c.keyEvent = view.RawKeyEvent
	step.warm = ready && !c.wasReady
	step.clear = !ready && c.wasReady
	c.wasReady = ready
	return step
}

func sameKeyEvent(a, b *core.RawKeyEvent) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}
