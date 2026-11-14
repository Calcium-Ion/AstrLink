//go:build windows

package toys

import (
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
