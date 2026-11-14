package toys

import (
	"log"
	"math"
	"runtime"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/host"
)

// mygoSystem builds the pet's window with MyGo, in its DIPs: global
// top-left points on macOS, the screen's own DIPs elsewhere, so every
// screen is at one unit to the window's point.
type mygoSystem struct {
	// preload is the script the pet's page runs first.
	preload func(label string) string
	// closed is told when a toy window closes on its own.
	closed func(label string)
}

func (mygoSystem) available() bool { return platformAvailable() }

func (mygoSystem) minTop() float64 { return platformMinTop }

func rectOf(r mygo.Rectangle) rect {
	return rect{float64(r.X), float64(r.Y), float64(r.Width), float64(r.Height)}
}

func rectangle(r rect) mygo.Rectangle {
	return mygo.Rectangle{X: int(math.Round(r.x)), Y: int(math.Round(r.y)), Width: int(math.Round(r.w)), Height: int(math.Round(r.h))}
}

func (mygoSystem) geometry() (geometry, bool) {
	displays := mygo.Screen.Displays()
	if len(displays) == 0 {
		return geometry{}, false
	}
	g := geometry{dock: dockRect()}
	for _, d := range displays {
		g.screens = append(g.screens, screenOf(rectOf(d.Bounds), rectOf(d.WorkArea)))
	}
	g.main = g.screens[0].frame
	return g, true
}

// mygoWindow is a toy window and its label.
type mygoWindow struct {
	w     *mygo.Window
	label string
}

var events = map[string]*mygo.Event[any]{
	FeedEvent:    host.ToysFeed,
	PrefsEvent:   host.ToysPrefs,
	PointerEvent: host.ToysPointer,
	FramesEvent:  host.ToysFrames,
	IdleEvent:    host.ToysIdle,
	ScreenEvent:  host.ToysScreen,
}

func (m mygoWindow) SetIgnoreMouseEvents(ignore bool) { m.w.SetIgnoreMouseEvents(ignore) }

func (m mygoWindow) Emit(event string, payload any) {
	if e := events[event]; e != nil && !m.w.IsDestroyed() {
		if err := e.Emit(m.w, payload); err != nil {
			log.Printf("astrlink: unable to update the desktop toy %s: %v", m.label, err)
		}
	}
}

func (m mygoWindow) Destroy() { m.w.Destroy() }

// build opens the pet's window: hidden, the mouse let through, on her
// screen and shown without focus once her page is ready.
func (s mygoSystem) build(label string, _ geometry, frame rect) (window, bool) {
	if !composited() {
		// A see-through window needs a compositor; without one she would
		// be a black sheet over the screen.
		log.Printf("astrlink: desktop toys: no compositor is running; the pet stays in")
		return nil, false
	}
	preload := ""
	if s.preload != nil {
		preload = s.preload(label)
	}
	b := rectangle(frame)
	w := mygo.NewWindow(petWindowOptions(runtime.GOOS, b, preload))
	host.Register(w, label)
	w.OnClosed(func() {
		if s.closed != nil {
			s.closed(label)
		}
	})
	w.SetIgnoreMouseEvents(true)
	w.SetVisibleOnAllWorkspaces(true)
	w.SetBounds(b)
	// A click on her never activates AstrLink: activation would bring the
	// main window forward with it.
	preventActivation(w)
	styleWindow(w)
	w.OnReadyToShow(func() {
		// Placed again: a window built at the origin is centered.
		w.SetBounds(b)
		w.ShowInactive()
		styleWindow(w)
		frontRegardless(w)
	})
	return mygoWindow{w, label}, true
}

// petWindowOptions are the pet window's: frameless, see-through, without a
// shadow, on top and out of the taskbar, hidden until her page is ready.
// On macOS she is a panel, which the window server knows from its
// creation never activates AstrLink, so a click on her leaves the app in
// front where it is and the main window where it was.
func petWindowOptions(goos string, b mygo.Rectangle, preload string) mygo.WindowOptions {
	return mygo.WindowOptions{
		Title:  "AstrLink",
		URL:    "/",
		X:      b.X,
		Y:      b.Y,
		Width:  b.Width,
		Height: b.Height,
		// A window at the origin would be centered.
		UseContentSize:    true,
		Hidden:            true,
		Frameless:         true,
		Transparent:       true,
		DisableShadow:     true,
		AlwaysOnTop:       true,
		SkipTaskbar:       true,
		DisableResize:     true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableClose:      true,
		DisableFullScreen: true,
		Panel:             goos == "darwin",
		Page: mygo.PageOptions{
			PreloadScript:        preload,
			BackgroundThrottling: mygo.ThrottlingOff,
		},
	}
}

func (mygoSystem) setFrame(w window, frame rect) {
	if m, ok := w.(mygoWindow); ok && !m.w.IsDestroyed() {
		m.w.SetBounds(rectangle(frame))
	}
}

func (mygoSystem) origin(w window) (rect, float64, bool) {
	m, ok := w.(mygoWindow)
	if !ok || m.w.IsDestroyed() {
		return rect{}, 0, false
	}
	return rectOf(m.w.Bounds()), 1, true
}

// mygoProbe reads the mouse through MyGo and the button, the idle time
// and the other windows from the system.
type mygoProbe struct{ os osProbe }

func (mygoSystem) openProbe() probe { return mygoProbe{openOSProbe()} }

func (p mygoProbe) pointer() (point, bool, bool) {
	at := mygo.Screen.CursorScreenPoint()
	return point{float64(at.X), float64(at.Y)}, true, p.os.buttonDown()
}

// idleSeconds is the system's own reading, else MyGo's, which is 0 where
// the system does not tell: never away.
func (p mygoProbe) idleSeconds() (float64, bool) {
	if seconds, ok := p.os.idleSeconds(); ok {
		return seconds, true
	}
	return mygo.Power.IdleTime().Seconds(), true
}

func (p mygoProbe) otherWindows() []otherWindow { return p.os.otherWindows() }

func (p mygoProbe) close() { p.os.close() }
