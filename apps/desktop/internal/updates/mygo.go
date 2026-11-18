package updates

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/realpath"
)

// publicKey is updates.publicKey of mygo.config.ts, which the desktop build
// links in with -X beside MyGo's own copy (scripts/mygo.mjs), so a
// download is verified before it is offered for installing. Builds without
// updates leave it empty.
var publicKey string

// PublicKey is the key updates must be signed with, "" for a build without
// updates.
func PublicKey() string { return strings.TrimSpace(publicKey) }

// Target is this platform as update manifests name it, such as
// darwin-arm64.
func Target() string { return runtime.GOOS + "-" + runtime.GOARCH }

// Arch is the architecture as About names it: aarch64 or x86_64.
func Arch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "aarch64"
	case "amd64":
		return "x86_64"
	}
	return runtime.GOARCH
}

// InstallSupported reports whether the app can replace itself where it is
// installed, as mygo.Updater.Enabled decides it apart from the build's
// configuration: an app bundle on macOS, a directory the user can write to
// elsewhere. An AppImage replaces its file, in a directory the user can
// write to. The Debian package installs the new package through apt, when
// pkexec can ask the user to authorize it; other packages a package
// manager installed cannot.
func InstallSupported() bool {
	if debInstalled() {
		return debInstallSupported()
	}
	if file, ok := appImageFile(); ok {
		return writable(filepath.Dir(file))
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if real, err := realpath.Resolve(exe); err == nil {
		exe = real
	}
	target := filepath.Dir(exe)
	if runtime.GOOS == "darwin" {
		target = filepath.Dir(filepath.Dir(target))
		if !strings.HasSuffix(target, ".app") {
			return false
		}
	}
	if os.Getenv("APPIMAGE") != "" {
		return false
	}
	return writable(filepath.Dir(target))
}

// writable reports whether the user can create files in dir.
func writable(dir string) bool {
	probe, err := os.MkdirTemp(dir, ".mygo-update-")
	if err != nil {
		return false
	}
	os.Remove(probe)
	return true
}

// startDir is the working directory the app started in, where
// mygo.App.Relaunch starts it again.
var startDir, _ = os.Getwd()

// relaunchAppImage quits the app and starts the AppImage file with args.
// mygo.App.Relaunch would start the executable in the AppImage's mount,
// which goes with this process. The file starts from OnQuit, after the
// listener that mygo.App.RequestSingleInstanceLock added at launch has
// released the lock, as Relaunch starts the app: started earlier, the new
// instance would hand its launch to this one and exit. A canceled quit
// starts nothing, as with Relaunch.
func relaunchAppImage(file string, args []string) {
	var mu sync.Mutex
	var offQuit, offCanceled func()
	mu.Lock()
	offQuit = mygo.App.OnQuit(func() { startAppImage(file, args) })
	offCanceled = mygo.App.OnQuitCanceled(func() {
		mu.Lock()
		defer mu.Unlock()
		offQuit()
		offCanceled()
	})
	mu.Unlock()
	mygo.App.Quit()
}

// startAppImage starts file with args, detached from this process, which
// is about to exit.
func startAppImage(file string, args []string) {
	cmd := exec.Command(file, args...)
	cmd.Dir = startDir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("astrlink: unable to start the updated AppImage: %v", err)
		return
	}
	_ = cmd.Process.Release()
}

// MyGo is the app's updater, mygo.Updater, reading the manifest a check
// read through the source it is given.
type MyGo struct{}

// fixedSource serves one manifest and the files it names.
type fixedSource struct {
	manifest []byte
	open     Opener
}

func (s fixedSource) Manifest(context.Context, bool) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.manifest)), nil
}

func (s fixedSource) Open(ctx context.Context, url string) (io.ReadCloser, error) {
	return s.open(ctx, url)
}

// Check hands mygo.Updater the manifest; the update it returns keeps the
// source for its files.
func (MyGo) Check(ctx context.Context, manifest []byte, open Opener) (Installer, error) {
	mygo.Updater.SetSource(fixedSource{manifest: manifest, open: open})
	update, err := mygo.Updater.Check(ctx)
	if err != nil || update == nil {
		return nil, err
	}
	return update, nil
}
