package ui

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/darwin"
	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// inspectorResize is how long a widening inspector takes to stretch
// (macOS).
const inspectorResize = 320 * time.Millisecond

func inspectorTitle(locale prefs.Locale, pinned bool) string {
	if pinned {
		return prefs.T(locale, "host.window.trajectoryInspectorPinned")
	}
	return prefs.T(locale, "host.window.trajectoryInspector")
}

// showInspector routes a selection to the newest unpinned inspector, or
// opens a new one beside the main window, and reports its label.
func (a *App) showInspector(selection json.RawMessage) (string, error) {
	// One lock for the whole decision, so a window created alongside this
	// call cannot end up holding a phase routed elsewhere.
	r := &a.inspectors
	r.mu.Lock()
	label, existing := r.target()
	cascade := 0
	if !existing {
		label = r.insert()
		cascade = len(r.entries) - 1
	}
	r.store(label, selection)
	r.mu.Unlock()

	if !existing {
		a.buildInspector(label, cascade)
		// Leaving the conversation can cancel a window before it exists.
		r.mu.Lock()
		open := r.find(label) != nil
		r.mu.Unlock()
		if !open {
			if w := windowByLabel(label); w != nil {
				w.Close()
			}
		}
		// The page pulls the stored selection once it mounts.
		return label, nil
	}
	if w := windowByLabel(label); w != nil {
		// Reveal a hidden or minimized inspector, but leave the keyboard
		// where it is: the operator is clicking rows in the main window.
		w.ShowInactive()
		if w.IsMinimized() {
			w.Restore()
		}
		if err := host.TrajectoryInspectorSelect.Emit(w, selection); err != nil {
			return "", err
		}
	}
	return label, nil
}

// updateInspector refreshes the inspector already following the list and
// never opens one: a poll that replaces the record must not bring back a
// window the operator closed.
func (a *App) updateInspector(selection json.RawMessage) error {
	r := &a.inspectors
	r.mu.Lock()
	label, ok := r.target()
	if ok {
		r.store(label, selection)
	}
	r.mu.Unlock()
	if !ok {
		return nil
	}
	if w := windowByLabel(label); w != nil {
		return host.TrajectoryInspectorSelect.Emit(w, selection)
	}
	return nil
}

func (a *App) buildInspector(label string, cascade int) {
	options := mygo.WindowOptions{
		Title:           inspectorTitle(a.locale(), false),
		URL:             "/",
		Width:           inspectorWidth,
		Height:          inspectorHeight,
		UseContentSize:  true,
		MinWidth:        inspectorMinWidth,
		MinHeight:       inspectorMinHeight,
		Hidden:          true,
		BackgroundColor: windowBackground,
		Page:            mygo.PageOptions{PreloadScript: a.h.PreloadScript(label)},
	}
	chrome(&options)
	// Frameless or with the title bar over the page, so the content size
	// is the whole frame.
	if main := a.h.MainWindow(); main != nil {
		if work := displayOf(main).WorkArea; work.Width > 0 {
			frame := inspectorOpeningFrame(box(main.Bounds()), box(work), cascade)
			options.X, options.Y = int(math.Round(frame.X)), int(math.Round(frame.Y))
			options.Width, options.Height = int(math.Round(frame.Width)), int(math.Round(frame.Height))
		}
	}
	w := mygo.NewWindow(options)
	host.Register(w, label)
	w.OnClosed(func() { a.windowClosed(label) })
	w.OnReadyToShow(w.Show)
}

// callerInspector is the inspector window a call came from.
func callerInspector(ctx context.Context) (*mygo.Window, string, error) {
	w := mygo.CallerWindow(ctx)
	label := host.Label(w)
	if w == nil || !strings.HasPrefix(label, InspectorLabelPrefix) {
		return nil, "", errors.New("only a trajectory inspector window can do this")
	}
	return w, label, nil
}

func (a *App) inspectorState(ctx context.Context) InspectorState {
	label := host.CallerLabel(ctx)
	a.inspectors.mu.Lock()
	defer a.inspectors.mu.Unlock()
	return a.inspectors.state(label)
}

// pinInspector freezes the caller on its phase, which the router then
// skips, and floats it so it can be read while the list moves on. The
// window level moves first, and the registry records only what took
// effect.
func (a *App) pinInspector(ctx context.Context, pinned bool) (bool, error) {
	w, label, err := callerInspector(ctx)
	if err != nil {
		return false, err
	}
	w.SetAlwaysOnTop(pinned)
	a.inspectors.mu.Lock()
	a.inspectors.setPinned(label, pinned)
	a.inspectors.mu.Unlock()
	w.SetTitle(inspectorTitle(a.locale(), pinned))
	return pinned, nil
}

// widenInspector widens the caller for long bodies, or puts it back. Only
// the width and the horizontal position change, so the window keeps its
// height and stays where the operator left it vertically. It reports the
// state that took effect.
func (a *App) widenInspector(ctx context.Context, wide bool) (bool, error) {
	w, label, err := callerInspector(ctx)
	if err != nil {
		return false, err
	}
	if w.IsFullScreen() {
		return false, errors.New("a full-screen inspector window cannot change its width")
	}
	a.inspectors.mu.Lock()
	var narrow *narrowFrame
	if entry := a.inspectors.find(label); entry != nil && entry.narrow != nil {
		saved := *entry.narrow
		narrow = &saved
	}
	a.inspectors.mu.Unlock()
	if wide == (narrow != nil) {
		return wide, nil
	}
	current := box(w.Bounds())
	work := box(displayOf(w).WorkArea)
	if work.Width <= 0 {
		return false, errors.New("the inspector window has no display")
	}
	var target Box
	var next *narrowFrame
	if wide {
		frame, back := inspectorWideFrame(current, work)
		target, next = frame, &back
	} else {
		target = inspectorNarrowFrame(current, work, *narrow)
	}
	moveFrame(w, current, target)
	a.inspectors.mu.Lock()
	if entry := a.inspectors.find(label); entry != nil {
		entry.narrow = next
	}
	a.inspectors.mu.Unlock()
	return wide, nil
}

// moveFrame moves w to target: on macOS with AppKit's own animation, so the
// window is seen to stretch rather than jump.
func moveFrame(w *mygo.Window, current, target Box) {
	animated := false
	if mac {
		mygo.RunOnMain(func() {
			animated = darwin.AnimateFrame(w.NativeHandle(), target.X-current.X, target.Width, inspectorResize)
		})
	}
	if !animated {
		w.SetBounds(mygo.Rectangle{
			X: int(math.Round(target.X)), Y: int(math.Round(target.Y)),
			Width: int(math.Round(target.Width)), Height: int(math.Round(target.Height)),
		})
	}
}

// closeUnpinnedInspectors closes the inspectors following the list and
// leaves the pinned ones: pinning is the operator keeping that phase on
// screen.
func (a *App) closeUnpinnedInspectors() {
	a.inspectors.mu.Lock()
	labels := a.inspectors.removeUnpinned()
	a.inspectors.mu.Unlock()
	for _, label := range labels {
		if w := windowByLabel(label); w != nil {
			w.Close()
		}
	}
}

// inspectorLocaleChanged retitles the open inspectors and the approval
// window in the new language.
func (a *App) inspectorLocaleChanged(locale prefs.Locale) {
	a.inspectors.mu.Lock()
	titles := map[string]string{}
	for _, entry := range a.inspectors.entries {
		titles[entry.label] = inspectorTitle(locale, entry.pinned)
	}
	a.inspectors.mu.Unlock()
	titles[ApprovalLabel] = prefs.T(locale, "host.window.rawAccessApproval")
	for label, title := range titles {
		if w := windowByLabel(label); w != nil {
			w.SetTitle(title)
		}
	}
}
