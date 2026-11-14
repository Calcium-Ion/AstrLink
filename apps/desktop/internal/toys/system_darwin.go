//go:build darwin

package toys

import (
	"log"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

func platformAvailable() bool { return true }

// platformMinTop is the menu bar's height at least, where the pet does not
// go.
const platformMinTop = 24.0

// petLevel is above the Dock (20), under the menu bar (24) and its menus.
const petLevel = 21

// She floats where she was left, over full-screen apps too.
const petCollection = 1<<0 | // CanJoinAllSpaces
	1<<4 | // Stationary
	1<<6 | // IgnoresCycle
	1<<8 // FullScreenAuxiliary

func screenOf(bounds, work rect) screen { return screen{frame: bounds, visible: work, scale: 1} }

func composited() bool { return true }

type cgRect struct{ x, y, w, h float64 }

var cg struct {
	once         sync.Once
	ok           bool
	buttonState  func(state int32, button uint32) bool
	secondsSince func(state int32, kind uint32) float64
	windowList   func(option, relativeTo uint32) uintptr
	rectFromDict func(dict uintptr, out *cgRect) bool
	arrayCount   func(array uintptr) int
	arrayAt      func(array uintptr, index int) uintptr
	dictGet      func(dict, key uintptr) uintptr
	typeOf       func(value uintptr) uint
	numberType   func() uint
	stringType   func() uint
	numberValue  func(number uintptr, kind int, out *float64) bool
	stringValue  func(text uintptr, buffer *byte, size int, encoding uint32) bool
	release      func(value uintptr)
	dock         func(out *cgRect)
	layer        uintptr
	bounds       uintptr
	pid          uintptr
	owner        uintptr
	number       uintptr
	alpha        uintptr
}

// symbolValue reads the CFStringRef a framework exports under name.
func symbolValue(lib uintptr, name string) uintptr {
	address, err := purego.Dlsym(lib, name)
	if err != nil {
		return 0
	}
	return **(**uintptr)(unsafe.Pointer(&address))
}

func loadCG() bool {
	cg.once.Do(func() {
		graphics, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		foundation, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		for name, fn := range map[string]any{
			"CGEventSourceButtonState":               &cg.buttonState,
			"CGEventSourceSecondsSinceLastEventType": &cg.secondsSince,
			"CGWindowListCopyWindowInfo":             &cg.windowList,
			"CGRectMakeWithDictionaryRepresentation": &cg.rectFromDict,
		} {
			symbol, err := purego.Dlsym(graphics, name)
			if err != nil {
				return
			}
			purego.RegisterFunc(fn, symbol)
		}
		for name, fn := range map[string]any{
			"CFArrayGetCount":        &cg.arrayCount,
			"CFArrayGetValueAtIndex": &cg.arrayAt,
			"CFDictionaryGetValue":   &cg.dictGet,
			"CFGetTypeID":            &cg.typeOf,
			"CFNumberGetTypeID":      &cg.numberType,
			"CFStringGetTypeID":      &cg.stringType,
			"CFNumberGetValue":       &cg.numberValue,
			"CFStringGetCString":     &cg.stringValue,
			"CFRelease":              &cg.release,
		} {
			symbol, err := purego.Dlsym(foundation, name)
			if err != nil {
				return
			}
			purego.RegisterFunc(fn, symbol)
		}
		cg.layer = symbolValue(graphics, "kCGWindowLayer")
		cg.bounds = symbolValue(graphics, "kCGWindowBounds")
		cg.pid = symbolValue(graphics, "kCGWindowOwnerPID")
		cg.owner = symbolValue(graphics, "kCGWindowOwnerName")
		cg.number = symbolValue(graphics, "kCGWindowNumber")
		cg.alpha = symbolValue(graphics, "kCGWindowAlpha")
		// The Dock's frame from the Dock itself: its width follows every
		// icon, kept or running, which no count of its preferences gets
		// right. Private API, looked up at run time so a macOS without it
		// only loses the Dock as a place to stand.
		if symbol, err := purego.Dlsym(purego.RTLD_DEFAULT, "CoreDockGetRect"); err == nil {
			purego.RegisterFunc(&cg.dock, symbol)
		} else {
			log.Printf("astrlink: desktop toys: no CoreDockGetRect; pets will not stand on the Dock")
		}
		cg.ok = cg.layer != 0 && cg.bounds != 0 && cg.pid != 0 && cg.owner != 0 && cg.number != 0 && cg.alpha != 0
	})
	return cg.ok
}

// dockRect is the Dock's frame in global top-left points.
func dockRect() *rect {
	if !loadCG() || cg.dock == nil {
		return nil
	}
	var r cgRect
	cg.dock(&r)
	if r.w <= 0 || r.h <= 0 {
		return nil
	}
	return &rect{r.x, r.y, r.w, r.h}
}

// styleWindow puts the pet's window above the Dock, on every Space and over
// full-screen apps, out of the window cycle.
func styleWindow(w *mygo.Window) {
	mygo.RunOnMain(func() {
		ns := objc.ID(w.NativeHandle())
		if ns == 0 {
			return
		}
		ns.Send(objc.RegisterName("setLevel:"), petLevel)
		ns.Send(objc.RegisterName("setCollectionBehavior:"), uint(petCollection))
	})
}

// preventActivation keeps the keyboard where it is, too: the panel becomes
// the key window only for a control that needs typing. That the panel
// never activates AstrLink comes from its creation (WindowOptions.Panel).
func preventActivation(w *mygo.Window) {
	mygo.RunOnMain(func() {
		ns := objc.ID(w.NativeHandle())
		selector := objc.RegisterName("setBecomesKeyOnlyIfNeeded:")
		if ns == 0 || !respondsTo(ns, selector) {
			log.Printf("astrlink: desktop toys: the pet's window is no panel; a click on her may activate AstrLink")
			return
		}
		ns.Send(selector, true)
	})
}

func respondsTo(object objc.ID, selector objc.SEL) bool {
	return object.Send(objc.RegisterName("respondsToSelector:"), selector) != 0
}

// frontRegardless orders the pet's window in front though AstrLink is not
// the active app.
func frontRegardless(w *mygo.Window) {
	mygo.RunOnMain(func() {
		if ns := objc.ID(w.NativeHandle()); ns != 0 {
			ns.Send(objc.RegisterName("orderFrontRegardless"))
		}
	})
}

type osProbe struct{}

func openOSProbe() osProbe { return osProbe{} }

const (
	combinedState = 0
	hidState      = 1
	anyInput      = ^uint32(0)
	onScreenOnly  = 1
	noDesktop     = 16
	utf8Encoding  = 0x08000100
	doubleNumber  = 13
)

func (osProbe) buttonDown() bool { return loadCG() && cg.buttonState(combinedState, 0) }

func (osProbe) idleSeconds() (float64, bool) {
	if !loadCG() {
		return 0, false
	}
	// No permission is needed for the HID idle time.
	return cg.secondsSince(hidState, anyInput), true
}

func number(dict, key uintptr) (float64, bool) {
	value := cg.dictGet(dict, key)
	if value == 0 || cg.typeOf(value) != cg.numberType() {
		return 0, false
	}
	var out float64
	return out, cg.numberValue(value, doubleNumber, &out)
}

func text(dict, key uintptr) string {
	value := cg.dictGet(dict, key)
	if value == 0 || cg.typeOf(value) != cg.stringType() {
		return ""
	}
	buffer := make([]byte, 256)
	if !cg.stringValue(value, &buffer[0], len(buffer), utf8Encoding) {
		return ""
	}
	for i, b := range buffer {
		if b == 0 {
			return string(buffer[:i])
		}
	}
	return string(buffer)
}

// otherWindows are other apps' windows on every screen, front to back.
func (osProbe) otherWindows() []otherWindow {
	if !loadCG() {
		return nil
	}
	list := cg.windowList(onScreenOnly|noDesktop, 0)
	if list == 0 {
		return nil
	}
	defer cg.release(list)
	own := float64(osPID)
	var others []otherWindow
	for i := range cg.arrayCount(list) {
		dict := cg.arrayAt(list, i)
		if layer, ok := number(dict, cg.layer); !ok || layer != 0 {
			continue
		}
		if pid, ok := number(dict, cg.pid); ok && pid == own {
			continue
		}
		if alpha, ok := number(dict, cg.alpha); ok && alpha < 0.1 {
			continue
		}
		bounds := cg.dictGet(dict, cg.bounds)
		var r cgRect
		if bounds == 0 || !cg.rectFromDict(bounds, &r) {
			continue
		}
		id, _ := number(dict, cg.number)
		others = append(others, otherWindow{id: int64(id), kind: kindOf(text(dict, cg.owner)), frame: rect{r.x, r.y, r.w, r.h}})
	}
	return others
}

func (osProbe) close() {}
