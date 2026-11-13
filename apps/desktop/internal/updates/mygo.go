package updates

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
)

// publicKey is updates.publicKey of mygo.config.ts, which the desktop build
// links in with -X beside MyGo's own copy (scripts/mygo-build.mjs), so a
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
// elsewhere. AppImages and the packages a package manager installed, such
// as a .deb in /opt, cannot.
func InstallSupported() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
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
	probe, err := os.MkdirTemp(filepath.Dir(target), ".mygo-update-")
	if err != nil {
		return false
	}
	os.Remove(probe)
	return true
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
