// The Debian package installs on Linux; these tests stand in for it on Unix
// systems, with the file modes apt reads the package by.

//go:build !windows

package updates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// aptRuns records what the stand-in for pkexec was asked to run, and the
// package it was handed, read while it ran.
type aptRuns struct {
	mu       sync.Mutex
	args     [][]string
	packages [][]byte
	modes    []os.FileMode
	// fail is the error the run returns, nil for an install.
	fail error
	// output is what the run prints.
	output string
}

// runAsDeb makes the test's app the one the Debian package installed: the
// package's executable is the test binary, which dpkg's list names. It
// returns the runs of pkexec.
func runAsDeb(t *testing.T) *aptRuns {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	list := filepath.Join(t.TempDir(), "astrlink.list")
	if err := os.WriteFile(list, []byte("/.\n/opt\n/opt/astrlink\n"+exe+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runs := &aptRuns{}
	system, executable, info, lookup, run := debOS, debExecutable, dpkgInfo, pkexecPath, runPrivileged
	t.Cleanup(func() {
		debOS, debExecutable, dpkgInfo, pkexecPath, runPrivileged = system, executable, info, lookup, run
	})
	debOS, debExecutable, dpkgInfo = runtime.GOOS, exe, list
	pkexecPath = func() (string, error) { return "/usr/bin/pkexec", nil }
	runPrivileged = func(_ context.Context, pkexec string, args ...string) ([]byte, error) {
		runs.mu.Lock()
		defer runs.mu.Unlock()
		runs.args = append(runs.args, append([]string{pkexec}, args...))
		file := args[len(args)-1]
		data, _ := os.ReadFile(file)
		runs.packages = append(runs.packages, data)
		if info, err := os.Stat(file); err == nil {
			runs.modes = append(runs.modes, info.Mode().Perm())
		}
		return []byte(runs.output), runs.fail
	}
	return runs
}

// publishDeb adds the Debian package of a published version and its
// manifest, which edit may change, to the release.
func (f *fakeReleases) publishDeb(version string, edit func(m *manifest, deb []byte)) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	tag := "v" + version
	deb := []byte("Debian package of " + version + strings.Repeat("!", 4096))
	name := "astrlink_" + version + "_amd64.deb"
	download := "https://github.com/Calcium-Ion/AstrLink/releases/download/" + tag + "/"
	m := manifest{
		Version:   version,
		Date:      "2026-11-01T00:00:00Z",
		URL:       download + name,
		Size:      int64(len(deb)),
		Signature: f.key.sign(deb),
	}
	if edit != nil {
		edit(&m, deb)
	}
	body, _ := json.Marshal(m)
	manifestName := debManifestName(testTarget)
	for _, prefix := range []string{repositoryPath, downloadSitePath} {
		f.files[prefix+tag+"/"+name] = deb
		f.files[prefix+tag+"/"+manifestName] = body
	}
	assets := []releaseAsset{
		{Name: manifestName, BrowserDownloadURL: download + manifestName},
		{Name: name, BrowserDownloadURL: download + name},
	}
	for _, list := range [][]githubRelease{f.apiReleases, f.mirror} {
		for i := range list {
			if list[i].TagName == tag {
				list[i].Assets = append(list[i].Assets, assets...)
			}
		}
	}
	return deb
}

func TestADebInstallReadsTheManifestOfItsPackage(t *testing.T) {
	for name, values := range map[string]prefs.UpdatePreferences{
		"stable":  {Channel: prefs.ChannelStable},
		"preview": {Channel: prefs.ChannelPreview},
		"mirror":  {Channel: prefs.ChannelStable, OfficialMirror: true},
	} {
		t.Run(name, func(t *testing.T) {
			runAsDeb(t)
			h := newHarness(t, "1.0.0", nil)
			h.setUpdates(func(u *prefs.UpdatePreferences) { *u = values })
			h.releases.publish("1.1.0", false)
			h.releases.publishDeb("1.1.0", nil)
			if s := h.check(); s.Phase != PhaseAvailable || s.Release.Version != "1.1.0" {
				t.Fatalf("check: %+v", s)
			}
			if got := h.m.pending.file.URL; !strings.HasSuffix(got, "/v1.1.0/astrlink_1.1.0_amd64.deb") {
				t.Fatalf("the update installs %s", got)
			}
		})
	}
	// An executable dpkg does not list is no package's: the app reads its
	// own platform's manifest, as before.
	t.Run("not the package's", func(t *testing.T) {
		runAsDeb(t)
		if err := os.WriteFile(dpkgInfo, []byte("/opt/astrlink/other\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		h := newHarness(t, "1.0.0", nil)
		h.releases.publish("1.1.0", false)
		h.releases.publishDeb("1.1.0", nil)
		h.check()
		if got := h.m.pending.file.URL; !strings.HasSuffix(got, ".tar.gz") {
			t.Fatalf("the update installs %s", got)
		}
	})
}

func TestADebReleaseWithoutItsManifestIsReported(t *testing.T) {
	runAsDeb(t)
	h := newHarness(t, "1.0.0", nil)
	h.setUpdates(func(u *prefs.UpdatePreferences) { u.Channel = prefs.ChannelPreview })
	h.releases.publish("1.1.0", false)
	if s := h.check(); deref(s.ErrorCode) != "missing_artifact" || !strings.Contains(deref(s.ErrorDetail), "update-linux-amd64-deb.json") {
		t.Fatalf("check: %+v", s)
	}
}

func TestADebUpdateInstallsThePackageThroughApt(t *testing.T) {
	runs := runAsDeb(t)
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	deb := h.releases.publishDeb("1.1.0", nil)
	h.check()
	if s, err := h.m.Download(context.Background()); err != nil || s.Phase != PhaseReady {
		t.Fatalf("download: %+v %v", s, err)
	}
	s, err := h.m.Install(context.Background())
	if err != nil || s.Phase != PhaseInstalling || s.ErrorCode != nil {
		t.Fatalf("install: %+v %v", s, err)
	}
	if len(runs.args) != 1 {
		t.Fatalf("pkexec ran %q", runs.args)
	}
	args := runs.args[0]
	if want := []string{"/usr/bin/pkexec", "/usr/bin/apt-get", "install", "-y"}; !slices.Equal(args[:4], want) || len(args) != 5 {
		t.Fatalf("pkexec ran %q", args)
	}
	// apt takes a path ending in .deb for a package file, read as _apt.
	if file := args[4]; !filepath.IsAbs(file) || !strings.HasSuffix(file, ".deb") {
		t.Fatalf("apt installs %q", file)
	}
	if !bytes.Equal(runs.packages[0], deb) || runs.modes[0] != 0o644 {
		t.Fatalf("apt read %d bytes of mode %v", len(runs.packages[0]), runs.modes[0])
	}
	if _, err := os.Stat(args[4]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the package stayed behind: %v", err)
	}
	if h.relaunched != 1 || h.updater.installed != nil {
		t.Fatalf("relaunched %d, MyGo installed %v", h.relaunched, h.updater.installed)
	}
	if want := []string{"stop", "wait 10s"}; h.core.starts != 0 || !slices.Equal(h.core.events, want) {
		t.Fatalf("core events %q starts %d", h.core.events, h.core.starts)
	}
}

func TestADismissedDebInstallStaysReady(t *testing.T) {
	runs := runAsDeb(t)
	// pkexec exits 126 when the user dismisses its dialog.
	runs.fail = exitError(t, 126)
	h := newHarness(t, "1.0.0", nil)
	h.releases.publish("1.1.0", false)
	h.releases.publishDeb("1.1.0", nil)
	h.check()
	_, _ = h.m.Download(context.Background())
	s, err := h.m.Install(context.Background())
	if err != nil || s.Phase != PhaseReady || s.ErrorCode != nil {
		t.Fatalf("install: %+v %v", s, err)
	}
	if h.relaunched != 0 || h.core.stops != h.core.starts {
		t.Fatalf("relaunched %d, core stops %d starts %d", h.relaunched, h.core.stops, h.core.starts)
	}
	// Asked again, it installs.
	runs.fail = nil
	if s, err := h.m.Install(context.Background()); err != nil || s.Phase != PhaseInstalling {
		t.Fatalf("second install: %+v %v", s, err)
	}
}

func TestAFailedDebUpdateReportsAptAndRestartsCore(t *testing.T) {
	other := newTestKey(t)
	for name, tc := range map[string]struct {
		edit   func(m *manifest, deb []byte)
		fail   error
		output string
		code   string
		detail string
	}{
		"bad signature": {edit: func(m *manifest, deb []byte) { m.Signature = other.sign(deb) }, code: "verification"},
		"wrong size":    {edit: func(m *manifest, _ []byte) { m.Size++ }, code: "verification"},
		"apt failed": {fail: errors.New("exit status 100"), output: "Reading package lists...\nE: Unable to locate package",
			code: "install", detail: "E: Unable to locate package"},
	} {
		t.Run(name, func(t *testing.T) {
			runs := runAsDeb(t)
			runs.fail, runs.output = tc.fail, tc.output
			h := newHarness(t, "1.0.0", nil)
			h.releases.publish("1.1.0", false)
			h.releases.publishDeb("1.1.0", tc.edit)
			h.check()
			_, _ = h.m.Download(context.Background())
			_, _ = h.m.Install(context.Background())
			s := h.m.Snapshot()
			if s.Phase != PhaseError || deref(s.ErrorCode) != tc.code || !strings.Contains(deref(s.ErrorDetail), tc.detail) {
				t.Fatalf("%+v: %s", s, deref(s.ErrorDetail))
			}
			if h.relaunched != 0 || h.core.stops != h.core.starts {
				t.Fatalf("relaunched %d, core stops %d starts %d", h.relaunched, h.core.stops, h.core.starts)
			}
		})
	}
}

func TestADebInstallNeedsPkexec(t *testing.T) {
	runAsDeb(t)
	if !InstallSupported() {
		t.Fatal("the package cannot install with pkexec")
	}
	pkexecPath = func() (string, error) { return "", exec.ErrNotFound }
	if InstallSupported() {
		t.Fatal("the package installs without pkexec")
	}
	h := newHarness(t, "1.0.0", func(c *Config) { c.InstallSupported = InstallSupported() })
	h.releases.publish("1.1.0", false)
	h.releases.publishDeb("1.1.0", nil)
	if s := h.check(); s.Phase != PhaseManual {
		t.Fatalf("check: %+v", s)
	}
}

// exitError is the error of a command that exited with code.
func exitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	if err == nil {
		t.Fatal("the command did not fail")
	}
	return err
}
