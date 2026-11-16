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
	// SIGUSR2 gets Go's handler, which a library then reinstalls without
	// SA_ONSTACK, as WebKit does with SIGUSR1.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGUSR2)
	defer signal.Reset(syscall.SIGUSR2)
	var installed action
	if errno := sigaction(int(syscall.SIGUSR2), nil, &installed); errno != 0 {
		t.Fatal(errno)
	}
	if installed.handler <= 1 || installed.flags&saOnStack == 0 {
		t.Fatalf("Go's handler = %+v", installed)
	}
	stripped := installed
	stripped.flags &^= saOnStack
	if errno := sigaction(int(syscall.SIGUSR2), &stripped, nil); errno != 0 {
		t.Fatal(errno)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Keep(ctx)

	var fixed action
	if errno := sigaction(int(syscall.SIGUSR2), nil, &fixed); errno != 0 {
		t.Fatal(errno)
	}
	if fixed != installed {
		t.Fatalf("handler = %+v, want %+v", fixed, installed)
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
