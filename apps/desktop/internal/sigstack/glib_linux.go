//go:build linux && (amd64 || arm64)

package sigstack

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/ebitengine/purego"
)

// holdChildHandler keeps GLib's SIGCHLD handler installed for the life of
// the app. GLib before 2.76 installs it, without SA_ONSTACK, whenever its
// first child watch is created and removes it when the last one goes, so
// every WebKit subprocess could put it back after a fix. A child watch that
// is never freed installs it now, once, for fix to mark. The watched child
// exits at once and GLib reaps it; newer GLib watches children through a
// pidfd and installs no handler at all.
func holdChildHandler() error {
	glib, err := purego.Dlopen("libglib-2.0.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	var watch func(pid int32) uintptr
	purego.RegisterLibFunc(&watch, glib, "g_child_watch_source_new")
	path, err := exec.LookPath("true")
	if err != nil {
		return err
	}
	child, err := os.StartProcess(path, []string{"true"}, &os.ProcAttr{})
	if err != nil {
		return err
	}
	if watch(int32(child.Pid)) == 0 {
		return fmt.Errorf("g_child_watch_source_new(%d) failed", child.Pid)
	}
	// GLib reaps the child, so Go must not wait for it.
	return child.Release()
}
