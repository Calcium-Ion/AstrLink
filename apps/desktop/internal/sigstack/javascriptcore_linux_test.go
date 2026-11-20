//go:build linux && (amd64 || arm64)

package sigstack

import (
	"context"
	"runtime"
	"testing"

	"github.com/ebitengine/purego"
)

// The web view's JavaScriptCore runs JavaScript on the UI thread, a Go thread
// with a signal stack, and its collector pauses that thread to scan it.
func TestJavaScriptCoreCanPauseAGoThreadAfterKeep(t *testing.T) {
	jsc, err := purego.Dlopen("libjavascriptcoregtk-4.1.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Skipf("no JavaScriptCore: %v", err)
	}
	var (
		createContext  func(class uintptr) uintptr
		releaseContext func(ctx uintptr)
		createString   func(s string) uintptr
		releaseString  func(s uintptr)
		evaluate       func(ctx, script, this, url uintptr, line int32, exception uintptr) uintptr
		collect        func(ctx uintptr)
	)
	purego.RegisterLibFunc(&createContext, jsc, "JSGlobalContextCreate")
	purego.RegisterLibFunc(&releaseContext, jsc, "JSGlobalContextRelease")
	purego.RegisterLibFunc(&createString, jsc, "JSStringCreateWithUTF8CString")
	purego.RegisterLibFunc(&releaseString, jsc, "JSStringRelease")
	purego.RegisterLibFunc(&evaluate, jsc, "JSEvaluateScript")
	purego.RegisterLibFunc(&collect, jsc, "JSSynchronousGarbageCollectForDebugging")

	// The UI thread: it runs JavaScript, then waits, as the GTK main loop
	// does. Its goroutine ends locked, so Go ends the thread and
	// JavaScriptCore stops pausing it.
	ready := make(chan uintptr)
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		runtime.LockOSThread()
		ctx := createContext(0)
		script := createString("Array.from({length: 10000}, (_, i) => ({i})).length")
		evaluate(ctx, script, 0, 0, 1, 0)
		releaseString(script)
		ready <- ctx
		<-done
		releaseContext(ctx)
	}()
	ctx := <-ready
	defer func() { close(done); <-exited }()

	keep, cancel := context.WithCancel(context.Background())
	defer cancel()
	Keep(keep)
	var suspend action
	if errno := sigaction(suspendSignal(), nil, &suspend); errno != 0 {
		t.Fatal(errno)
	}
	if suspend.handler <= 1 || suspend.flags&saOnStack != 0 {
		t.Fatalf("suspend handler after Keep = %+v, want JavaScriptCore's off the signal stack", suspend)
	}

	// A full collection from another thread pauses the UI thread.
	collected := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		collect(ctx)
		close(collected)
	}()
	<-collected
}
