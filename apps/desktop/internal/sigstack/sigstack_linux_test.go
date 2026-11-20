//go:build linux && (amd64 || arm64)

package sigstack

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"

	"github.com/ebitengine/purego"
)

func TestKeepPutsAHandlerWithoutTheSignalStackBackOnIt(t *testing.T) {
	// A user signal gets Go's handler, which a library then reinstalls
	// without SA_ONSTACK, as GLib does with SIGCHLD. JavaScriptCore may hold
	// the other one.
	sig := syscall.SIGUSR2
	if suspendSignal() == int(sig) {
		sig = syscall.SIGUSR1
	}
	signal.Notify(make(chan os.Signal, 1), sig)
	defer signal.Reset(sig)
	var installed action
	if errno := sigaction(int(sig), nil, &installed); errno != 0 {
		t.Fatal(errno)
	}
	if installed.handler <= 1 || installed.flags&saOnStack == 0 {
		t.Fatalf("Go's handler = %+v", installed)
	}
	stripped := installed
	stripped.flags &^= saOnStack
	if errno := sigaction(int(sig), &stripped, nil); errno != 0 {
		t.Fatal(errno)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Keep(ctx)

	var fixed action
	if errno := sigaction(int(sig), nil, &fixed); errno != 0 {
		t.Fatal(errno)
	}
	if fixed != installed {
		t.Fatalf("handler = %+v, want %+v", fixed, installed)
	}
}

func TestKeepLeavesTheSuspendHandlerOffTheSignalStack(t *testing.T) {
	// JavaScriptCore installs its suspend handler without SA_ONSTACK; Go's
	// handler, or JavaScriptCore's own, stripped of it stands in for it.
	sig := syscall.Signal(suspendSignal())
	var original action
	if errno := sigaction(int(sig), nil, &original); errno != 0 {
		t.Fatal(errno)
	}
	t.Cleanup(func() { sigaction(int(sig), &original, nil) })
	signal.Notify(make(chan os.Signal, 1), sig)
	defer signal.Reset(sig)
	var installed action
	if errno := sigaction(int(sig), nil, &installed); errno != 0 {
		t.Fatal(errno)
	}
	installed.flags &^= saOnStack
	if errno := sigaction(int(sig), &installed, nil); errno != 0 {
		t.Fatal(errno)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Keep(ctx)

	var kept action
	if errno := sigaction(int(sig), nil, &kept); errno != 0 {
		t.Fatal(errno)
	}
	if kept != installed {
		t.Fatalf("suspend handler = %+v, want %+v", kept, installed)
	}
}

func TestTheSuspendSignalIsReadAsJavaScriptCoreReadsIt(t *testing.T) {
	// JavaScriptCore reads JSC_SIGNAL_FOR_GC with sscanf's %d and keeps
	// SIGUSR1 when no number leads it.
	for value, want := range map[string]int{
		"":        int(syscall.SIGUSR1),
		"12":      12,
		" 12":     12,
		"12x":     12,
		"SIGUSR2": int(syscall.SIGUSR1),
	} {
		t.Setenv("JSC_SIGNAL_FOR_GC", value)
		if got := suspendSignal(); got != want {
			t.Errorf("JSC_SIGNAL_FOR_GC=%q: signal %d, want %d", value, got, want)
		}
	}
}

func TestAHeldChildWatchKeepsGLibsHandlerOnTheSignalStack(t *testing.T) {
	glib, err := purego.Dlopen("libglib-2.0.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Skipf("no GLib: %v", err)
	}
	var watch func(pid int32) uintptr
	var unref func(source uintptr)
	purego.RegisterLibFunc(&watch, glib, "g_child_watch_source_new")
	purego.RegisterLibFunc(&unref, glib, "g_source_unref")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Keep(ctx)
	handler := func() action {
		var current action
		if errno := sigaction(int(syscall.SIGCHLD), nil, &current); errno != 0 {
			t.Fatal(errno)
		}
		return current
	}
	held := handler()
	if held.handler <= 1 || held.flags&saOnStack == 0 {
		t.Fatalf("SIGCHLD handler after Keep = %+v", held)
	}
	// A WebKit subprocess comes and goes; GLib neither reinstalls its
	// handler nor removes it.
	child := exec.Command("true")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	source := watch(int32(child.Process.Pid))
	unref(source)
	if got := handler(); got != held {
		t.Fatalf("SIGCHLD handler = %+v, want %+v", got, held)
	}
}
