//go:build windows

package toys

import (
	"log"

	"golang.org/x/sys/windows"

	"github.com/egoist/mygo"
)

func platformAvailable() bool { return true }

// platformMinTop: no menu bar to keep under.
const platformMinTop = 0.0

func screenOf(bounds, work rect) screen {
	area := petArea(bounds, work)
	return screen{frame: area, visible: area, scale: 1}
}

func composited() bool { return true }

func dockRect() *rect { return nil }

func styleWindow(*mygo.Window) {}

var (
	comctl32         = windows.NewLazySystemDLL("comctl32.dll")
	setWindowSubcls  = comctl32.NewProc("SetWindowSubclass")
	defSubclassProc  = comctl32.NewProc("DefSubclassProc")
	getWindowLongPtr = user32.NewProc("GetWindowLongPtrW")
	setWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	// noActivateProc answers WM_MOUSEACTIVATE for the pet's window.
	noActivateProc = windows.NewCallback(func(hwnd, msg, wParam, lParam, _, _ uintptr) uintptr {
		if r, ok := mouseActivate(uint32(msg)); ok {
			return r
		}
		r, _, _ := defSubclassProc.Call(hwnd, msg, wParam, lParam)
		return r
	})
)

const (
	gwlExStyle      = ^uintptr(19) // GWL_EXSTYLE, -20
	wsExNoActivate  = 0x08000000
	wmMouseActivate = 0x0021
	maNoActivate    = 3
	// subclassID tells the pet's subclass apart from any other.
	subclassID = 0x70657421
)

// mouseActivate is the answer to a message the pet's window handles
// itself: a click does not activate it.
func mouseActivate(msg uint32) (uintptr, bool) {
	if msg == wmMouseActivate {
		return maNoActivate, true
	}
	return 0, false
}

// noActivateStyle is an extended window style that never becomes the
// foreground window by a click.
func noActivateStyle(ex uintptr) uintptr { return ex | wsExNoActivate }

// preventActivation keeps a click on the pet from activating AstrLink,
// which would bring the main window forward: WS_EX_NOACTIVATE, as MyGo
// gives a flyout that takes no focus, and MA_NOACTIVATE for the click
// itself. On the UI thread, which owns the window.
func preventActivation(w *mygo.Window) {
	mygo.RunOnMain(func() {
		hwnd := w.NativeHandle()
		if hwnd == 0 {
			return
		}
		ex, _, _ := getWindowLongPtr.Call(hwnd, gwlExStyle)
		setWindowLongPtr.Call(hwnd, gwlExStyle, noActivateStyle(ex))
		if ok, _, err := setWindowSubcls.Call(hwnd, noActivateProc, subclassID, 0); ok == 0 {
			log.Printf("astrlink: desktop toys: a click on the pet may activate AstrLink: %v", err)
		}
	})
}

func frontRegardless(*mygo.Window) {}

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	getAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	getSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	vkLButton    = 0x01
	vkRButton    = 0x02
	smSwapButton = 23
)

type osProbe struct{}

func openOSProbe() osProbe { return osProbe{} }

// buttonDown is the main button, the right one for a left-handed mouse.
func (osProbe) buttonDown() bool {
	main := uintptr(vkLButton)
	if swapped, _, _ := getSystemMetrics.Call(smSwapButton); swapped != 0 {
		main = vkRButton
	}
	state, _, _ := getAsyncKeyState.Call(main)
	return uint16(state)&0x8000 != 0
}

// idleSeconds leaves the reading to MyGo's GetLastInputInfo.
func (osProbe) idleSeconds() (float64, bool) { return 0, false }

func (osProbe) otherWindows() []otherWindow { return nil }

func (osProbe) close() {}
