//go:build darwin

package darwin

import (
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// nsRect mirrors CGRect on 64-bit macOS.
type nsRect struct {
	X, Y, Width, Height float64
}

var (
	load sync.Once
	// Typed objc_msgSend variants for the arguments objc.ID.Send cannot
	// pass: structs, doubles and C integers.
	msgFrame       func(obj objc.ID, sel objc.SEL) nsRect
	msgSetFrame    func(obj objc.ID, sel objc.SEL, frame nsRect, display bool)
	msgSetDouble   func(obj objc.ID, sel objc.SEL, value float64)
	msgPID         func(obj objc.ID, sel objc.SEL) int32
	msgBool        func(obj objc.ID, sel objc.SEL) bool
	msgAppForPID   func(cls objc.ID, sel objc.SEL, pid int32) objc.ID
	msgActivate    func(obj objc.ID, sel objc.SEL, options uint) bool
	msgStringUTF8  func(cls objc.ID, sel objc.SEL, text unsafe.Pointer) objc.ID
	msgAddObserver func(center objc.ID, sel objc.SEL, name, object, queue objc.ID, block objc.Block) objc.ID
)

func loaded() bool {
	load.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			return
		}
		// AppKit is loaded already in an app; this only finds it.
		if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
			return
		}
		send, err := purego.Dlsym(lib, "objc_msgSend")
		if err != nil {
			return
		}
		purego.RegisterFunc(&msgFrame, send)
		if stret, err := purego.Dlsym(lib, "objc_msgSend_stret"); err == nil {
			// x86_64 returns a struct larger than 16 bytes through memory.
			purego.RegisterFunc(&msgFrame, stret)
		}
		purego.RegisterFunc(&msgSetFrame, send)
		purego.RegisterFunc(&msgSetDouble, send)
		purego.RegisterFunc(&msgPID, send)
		purego.RegisterFunc(&msgBool, send)
		purego.RegisterFunc(&msgAppForPID, send)
		purego.RegisterFunc(&msgActivate, send)
		purego.RegisterFunc(&msgStringUTF8, send)
		purego.RegisterFunc(&msgAddObserver, send)
	})
	return msgAddObserver != nil
}

func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

func sel(name string) objc.SEL { return objc.RegisterName(name) }

func nsString(text string) objc.ID {
	b := append([]byte(text), 0)
	return msgStringUTF8(class("NSString"), sel("stringWithUTF8String:"), unsafe.Pointer(&b[0]))
}

func workspace() objc.ID { return class("NSWorkspace").Send(sel("sharedWorkspace")) }

// FrontmostOtherApp is the pid of the frontmost app when that is not this
// one, 0 otherwise. Read it before showing a window that takes the
// keyboard: showing it activates this app. Main thread only.
func FrontmostOtherApp() int {
	if !loaded() {
		return 0
	}
	app := workspace().Send(sel("frontmostApplication"))
	if app == 0 {
		return 0
	}
	pid := int(msgPID(app, sel("processIdentifier")))
	if pid <= 0 || pid == os.Getpid() {
		return 0
	}
	return pid
}

// ReturnForeground hands the foreground from this app back to pid's. It
// reports whether this app is about to deactivate as a result: hiding the
// key window of the active app makes AppKit raise its next window, so a
// panel closing would bring the main window up in front of the app the
// operator was using. Main thread only.
func ReturnForeground(pid int) bool {
	if pid <= 0 || !loaded() {
		return false
	}
	current := class("NSRunningApplication").Send(sel("currentApplication"))
	if current == 0 || !msgBool(current, sel("isActive")) {
		return false
	}
	other := msgAppForPID(class("NSRunningApplication"), sel("runningApplicationWithProcessIdentifier:"), int32(pid))
	if other == 0 || msgBool(other, sel("isTerminated")) {
		return false
	}
	return msgActivate(other, sel("activateWithOptions:"), 0)
}

// OnSessionResignActive calls fn when the user switches to another account
// (fast user switching), which the screen lock notification does not
// cover. The observer stays for the life of the app. Main thread only.
func OnSessionResignActive(fn func()) {
	if !loaded() {
		return
	}
	center := workspace().Send(sel("notificationCenter"))
	block := objc.NewBlock(func(objc.Block, objc.ID) { fn() })
	// No queue: the block runs on the posting thread, the main one.
	msgAddObserver(center, sel("addObserverForName:object:queue:usingBlock:"),
		nsString("NSWorkspaceSessionDidResignActiveNotification"), 0, 0, block)
}

// AnimateFrame moves the NSWindow nsWindow by shiftX points and gives it
// width, with AppKit's own animation over duration, so the window is seen
// to stretch rather than jump. Only x and width change, and both run left
// to right in points on either side, so the Cocoa frame needs no flip. It
// reports false when it could not. Main thread only.
func AnimateFrame(nsWindow uintptr, shiftX, width float64, duration time.Duration) bool {
	if nsWindow == 0 || !loaded() {
		return false
	}
	window := objc.ID(nsWindow)
	frame := msgFrame(window, sel("frame"))
	frame.X += shiftX
	frame.Width = width
	context := class("NSAnimationContext")
	context.Send(sel("beginGrouping"))
	msgSetDouble(context.Send(sel("currentContext")), sel("setDuration:"), duration.Seconds())
	msgSetFrame(window.Send(sel("animator")), sel("setFrame:display:"), frame, true)
	context.Send(sel("endGrouping"))
	return true
}
