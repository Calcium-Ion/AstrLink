//go:build darwin

package widget

import (
	"log"
	"path/filepath"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

// bridgeLibrary is the WidgetKit bridge the macOS build compiles from
// macos-widget/HostBridge into the app's resources.
const bridgeLibrary = "libastrlink_widget_bridge.dylib"

func systemFeed() *Feed {
	directory := appGroupContainer()
	if directory == "" {
		return nil
	}
	return NewFeed(directory, loadBridge())
}

// dylibBridge calls the bridge's C entry points.
type dylibBridge struct {
	reload, queryPlaced func()
	placed              func() int32
}

func (b dylibBridge) Reload()       { b.reload() }
func (b dylibBridge) QueryPlaced()  { b.queryPlaced() }
func (b dylibBridge) Placed() int32 { return b.placed() }

// loadBridge opens the WidgetKit bridge; without it, snapshots are still
// written and the widget redraws on its own timeline.
func loadBridge() Bridge {
	resources, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return nil
	}
	lib, err := purego.Dlopen(filepath.Join(resources, bridgeLibrary), purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		log.Printf("astrlink: the widget bridge is not available; widgets redraw on their own schedule: %v", err)
		return nil
	}
	var b dylibBridge
	for name, fn := range map[string]any{
		"astrlink_widget_reload":       &b.reload,
		"astrlink_widget_query_placed": &b.queryPlaced,
		"astrlink_widget_placed":       &b.placed,
	} {
		symbol, err := purego.Dlsym(lib, name)
		if err != nil {
			log.Printf("astrlink: the widget bridge lacks %s", name)
			return nil
		}
		purego.RegisterFunc(fn, symbol)
	}
	return b
}

var security struct {
	poolPush, poolPop uintptr
	taskFromSelf      func(allocator uintptr) uintptr
	copyEntitlement   func(task, entitlement objc.ID, err uintptr) objc.ID
	release           func(object uintptr)
	stringUTF8        func(class objc.ID, sel objc.SEL, text *byte) objc.ID
	utf8String        func(object objc.ID, sel objc.SEL) *byte
	isKind            func(object objc.ID, sel objc.SEL, class objc.ID) bool
	count             func(object objc.ID, sel objc.SEL) uint
}

func loadSecurity() bool {
	lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return false
	}
	frameworks := map[string]uintptr{}
	for _, name := range []string{"CoreFoundation", "Foundation", "Security"} {
		handle, err := purego.Dlopen("/System/Library/Frameworks/"+name+".framework/"+name, purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			return false
		}
		frameworks[name] = handle
	}
	send, err := purego.Dlsym(lib, "objc_msgSend")
	if err != nil {
		return false
	}
	symbols := []struct {
		lib  uintptr
		name string
		fn   any
	}{
		{frameworks["Security"], "SecTaskCreateFromSelf", &security.taskFromSelf},
		{frameworks["Security"], "SecTaskCopyValueForEntitlement", &security.copyEntitlement},
		{frameworks["CoreFoundation"], "CFRelease", &security.release},
	}
	for _, s := range symbols {
		symbol, err := purego.Dlsym(s.lib, s.name)
		if err != nil {
			return false
		}
		purego.RegisterFunc(s.fn, symbol)
	}
	if security.poolPush, err = purego.Dlsym(lib, "objc_autoreleasePoolPush"); err != nil {
		return false
	}
	if security.poolPop, err = purego.Dlsym(lib, "objc_autoreleasePoolPop"); err != nil {
		return false
	}
	purego.RegisterFunc(&security.stringUTF8, send)
	purego.RegisterFunc(&security.utf8String, send)
	purego.RegisterFunc(&security.isKind, send)
	purego.RegisterFunc(&security.count, send)
	return true
}

func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

func sel(name string) objc.SEL { return objc.RegisterName(name) }

func goString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}

// appGroupContainer is the App Group container this process is entitled
// to, "" when the build carries no App Group (an unbundled development
// run), so nothing is written for a widget.
func appGroupContainer() string {
	if !loadSecurity() {
		return ""
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool, _, _ := purego.SyscallN(security.poolPush)
	defer purego.SyscallN(security.poolPop, pool)

	task := security.taskFromSelf(0)
	if task == 0 {
		return ""
	}
	defer security.release(task)
	key := []byte("com.apple.security.application-groups\x00")
	entitlement := security.stringUTF8(class("NSString"), sel("stringWithUTF8String:"), &key[0])
	value := security.copyEntitlement(objc.ID(task), entitlement, 0)
	if value == 0 {
		return ""
	}
	defer security.release(uintptr(value))
	if !security.isKind(value, sel("isKindOfClass:"), class("NSArray")) || security.count(value, sel("count")) == 0 {
		return ""
	}
	group := value.Send(sel("firstObject"))
	if group == 0 || !security.isKind(group, sel("isKindOfClass:"), class("NSString")) {
		return ""
	}
	manager := class("NSFileManager").Send(sel("defaultManager"))
	url := manager.Send(sel("containerURLForSecurityApplicationGroupIdentifier:"), group)
	if url == 0 {
		return ""
	}
	path := url.Send(sel("path"))
	if path == 0 {
		return ""
	}
	return goString(security.utf8String(path, sel("UTF8String")))
}
