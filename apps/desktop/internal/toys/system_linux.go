//go:build linux

package toys

import (
	"log"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
)

// platformAvailable: an X11 session, where the pet's window may be placed
// and kept on top. Wayland allows neither, nor reading the mouse outside a
// window.
func platformAvailable() bool {
	return os.Getenv("DISPLAY") != "" && os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("XDG_SESSION_TYPE") != "wayland"
}

// platformMinTop: no menu bar to keep under; panels are outside the work
// area.
const platformMinTop = 0.0

func screenOf(bounds, work rect) screen {
	area := petArea(bounds, work)
	return screen{frame: area, visible: area, scale: 1}
}

func dockRect() *rect { return nil }

func styleWindow(*mygo.Window) {}

var gtkFocus struct {
	once        sync.Once
	acceptFocus func(window uintptr, accept bool)
	focusOnMap  func(window uintptr, focus bool)
	ok          bool
}

// preventActivation keeps a click on the pet from giving AstrLink the
// keyboard: her window takes no focus, as it maps or as it is clicked,
// which the window manager learns from its input hint.
func preventActivation(w *mygo.Window) {
	gtkFocus.once.Do(func() {
		lib, err := purego.Dlopen("libgtk-3.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		gtkFocus.ok = err == nil && register(lib, map[string]any{
			"gtk_window_set_accept_focus": &gtkFocus.acceptFocus,
			"gtk_window_set_focus_on_map": &gtkFocus.focusOnMap,
		})
	})
	if !gtkFocus.ok {
		log.Printf("astrlink: desktop toys: no GTK focus hints; a click on the pet may take the keyboard")
		return
	}
	mygo.RunOnMain(func() {
		if window := w.NativeHandle(); window != 0 {
			gtkFocus.acceptFocus(window, false)
			gtkFocus.focusOnMap(window, false)
		}
	})
}

func frontRegardless(*mygo.Window) {}

// composited reports whether a compositor runs, which a see-through window
// needs. GDK is loaded already; it is asked on the main thread.
func composited() bool {
	lib, err := purego.Dlopen("libgdk-3.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return false
	}
	var defaultScreen func() uintptr
	var isComposited func(screen uintptr) bool
	for name, fn := range map[string]any{"gdk_screen_get_default": &defaultScreen, "gdk_screen_is_composited": &isComposited} {
		symbol, err := purego.Dlsym(lib, name)
		if err != nil {
			return false
		}
		purego.RegisterFunc(fn, symbol)
	}
	var on bool
	mygo.RunOnMain(func() {
		if screen := defaultScreen(); screen != 0 {
			on = isComposited(screen)
		}
	})
	return on
}

// screenSaverInfo is XScreenSaverInfo.
type screenSaverInfo struct {
	window     uint64
	state      int32
	kind       int32
	tilOrSince uint64
	idle       uint64
	eventMask  uint64
}

var xlib struct {
	once         sync.Once
	open         func(name *byte) uintptr
	close        func(display uintptr) int32
	root         func(display uintptr) uint64
	query        func(display uintptr, window uint64, root, child *uint64, rootX, rootY, x, y *int32, mask *uint32) int32
	free         func(data unsafe.Pointer) int32
	allocSaver   func() unsafe.Pointer
	querySaver   func(display uintptr, drawable uint64, info unsafe.Pointer) int32
	loaded, idle bool
}

func openLibrary(names ...string) uintptr {
	for _, name := range names {
		if lib, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_LOCAL); err == nil {
			return lib
		}
	}
	return 0
}

func register(lib uintptr, fns map[string]any) bool {
	if lib == 0 {
		return false
	}
	for name, fn := range fns {
		symbol, err := purego.Dlsym(lib, name)
		if err != nil {
			return false
		}
		purego.RegisterFunc(fn, symbol)
	}
	return true
}

func loadXlib() {
	xlib.once.Do(func() {
		xlib.loaded = register(openLibrary("libX11.so.6", "libX11.so"), map[string]any{
			"XOpenDisplay": &xlib.open, "XCloseDisplay": &xlib.close, "XDefaultRootWindow": &xlib.root,
			"XQueryPointer": &xlib.query, "XFree": &xlib.free,
		})
		xlib.idle = xlib.loaded && register(openLibrary("libXss.so.1", "libXss.so"), map[string]any{
			"XScreenSaverAllocInfo": &xlib.allocSaver, "XScreenSaverQueryInfo": &xlib.querySaver,
		})
		if xlib.loaded && !xlib.idle {
			log.Printf("astrlink: desktop toys: no libXss; the pet will not notice you stepping away")
		}
	})
}

// osProbe reads the button and the idle time from the X server, on the
// poll's own connection.
type osProbe struct{ display uintptr }

func openOSProbe() osProbe {
	loadXlib()
	if !xlib.loaded {
		return osProbe{}
	}
	display := xlib.open(nil)
	if display == 0 {
		log.Printf("astrlink: desktop toys: unable to reach the X server; the pet cannot see the mouse button")
	}
	return osProbe{display}
}

const button1Mask = 1 << 8

func (p osProbe) buttonDown() bool {
	if p.display == 0 {
		return false
	}
	var root, child uint64
	var rootX, rootY, x, y int32
	var mask uint32
	if xlib.query(p.display, xlib.root(p.display), &root, &child, &rootX, &rootY, &x, &y, &mask) == 0 {
		return false
	}
	return mask&button1Mask != 0
}

func (p osProbe) idleSeconds() (float64, bool) {
	if p.display == 0 || !xlib.idle {
		return 0, false
	}
	info := xlib.allocSaver()
	if info == nil {
		return 0, false
	}
	defer xlib.free(info)
	if xlib.querySaver(p.display, xlib.root(p.display), info) == 0 {
		return 0, false
	}
	return float64((*screenSaverInfo)(info).idle) / 1000, true
}

func (osProbe) otherWindows() []otherWindow { return nil }

func (p osProbe) close() {
	if p.display != 0 {
		xlib.close(p.display)
	}
}
