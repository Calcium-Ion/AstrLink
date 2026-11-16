// AppImages exist on Linux only, and these tests check the Unix file modes
// an update keeps; Windows has no such modes.

//go:build !windows

package updates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// oldAppImage is the content of the AppImage an update replaces.
var oldAppImage = []byte("AppImage of 1.0.0")

// runAsAppImage makes the test's app an AppImage of the given mode: a file
// in a directory of its own, which $APPIMAGE names through a symlink, as a
// desktop entry may. It returns the file.
func runAsAppImage(t *testing.T, mode os.FileMode) string {
	t.Helper()
	system := appImageOS
	appImageOS = runtime.GOOS
	t.Cleanup(func() { appImageOS = system })
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "AstrLink_1.0.0_amd64.AppImage")
	if err := os.WriteFile(file, oldAppImage, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, mode); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "AstrLink.AppImage")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPIMAGE", link)
	return file
}

// assertUntouched fails unless file still holds the old AppImage with
// mode and nothing else was left in its directory.
func assertUntouched(t *testing.T, file string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(data, oldAppImage) {
		t.Fatalf("the AppImage changed: %q %v", data, err)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != mode {
		t.Fatalf("the AppImage's mode changed to %v", info.Mode())
	}
	assertAlone(t, file)
}

// assertAlone fails when the directory of file holds anything else.
func assertAlone(t *testing.T, file string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{filepath.Base(file)}) {
		t.Fatalf("the AppImage's directory holds %q", names)
	}
}

// publishAppImage adds the AppImage of a published version and its
// manifest, which edit may change, to the release.
func (f *fakeReleases) publishAppImage(version string, edit func(m *manifest, image []byte)) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	tag := "v" + version
	image := []byte("AppImage of " + version + strings.Repeat("!", 4096))
	name := "AstrLink_" + version + "_amd64.AppImage"
	download := "https://github.com/Calcium-Ion/AstrLink/releases/download/" + tag + "/"
	m := manifest{
		Version:   version,
		Notes:     "notes of " + version,
		Date:      "2026-11-01T00:00:00Z",
		URL:       download + name,
		Size:      int64(len(image)),
		Signature: f.key.sign(image),
	}
	if edit != nil {
		edit(&m, image)
	}
	body, _ := json.Marshal(m)
	manifestName := appImageManifestName(testTarget)
	for _, prefix := range []string{repositoryPath, downloadSitePath} {
		f.files[prefix+tag+"/"+name] = image
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
	return image
}

// relaunches records what RelaunchAppImage was asked to start.
type relaunches struct {
	mu    sync.Mutex
	files []string
	args  [][]string
}

func (r *relaunches) record(file string, args []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files = append(r.files, file)
	r.args = append(r.args, args)
}

func newAppImageHarness(t *testing.T, r *relaunches) *harness {
	return newHarness(t, "1.0.0", func(c *Config) { c.RelaunchAppImage = r.record })
}

func TestAnAppImageReadsTheManifestOfItsAppImage(t *testing.T) {
	for name, values := range map[string]prefs.UpdatePreferences{
		"stable":  {Channel: prefs.ChannelStable},
		"preview": {Channel: prefs.ChannelPreview},
		"mirror":  {Channel: prefs.ChannelStable, OfficialMirror: true},
	} {
		t.Run(name, func(t *testing.T) {
			runAsAppImage(t, 0o755)
			h := newAppImageHarness(t, &relaunches{})
			h.setUpdates(func(u *prefs.UpdatePreferences) { *u = values })
			h.releases.publish("1.1.0", false)
			h.releases.publishAppImage("1.1.0", nil)
			if s := h.check(); s.Phase != PhaseAvailable || s.Release.Version != "1.1.0" {
				t.Fatalf("check: %+v", s)
			}
			if got := h.m.pending.file.URL; !strings.HasSuffix(got, "/v1.1.0/AstrLink_1.1.0_amd64.AppImage") {
				t.Fatalf("the update installs %s", got)
			}
			if values.OfficialMirror && (h.releases.count("github.com") != 0 || h.releases.count("api.github.com") != 0) {
				t.Fatalf("the mirror reached GitHub: %v", h.releases.requests)
			}
		})
	}
	// $APPIMAGE that names no file is not an AppImage: the app reads its
	// own platform's manifest, as before.
	t.Run("missing file", func(t *testing.T) {
		file := runAsAppImage(t, 0o755)
		t.Setenv("APPIMAGE", file+".gone")
		h := newHarness(t, "1.0.0", nil)
		h.releases.publish("1.1.0", false)
		h.releases.publishAppImage("1.1.0", nil)
		h.check()
		if got := h.m.pending.file.URL; !strings.HasSuffix(got, ".tar.gz") {
			t.Fatalf("the update installs %s", got)
		}
	})
}

func TestAnAppImageReleaseWithoutItsManifestIsReported(t *testing.T) {
	runAsAppImage(t, 0o755)
	h := newAppImageHarness(t, &relaunches{})
	h.setUpdates(func(u *prefs.UpdatePreferences) { u.Channel = prefs.ChannelPreview })
	h.releases.publish("1.1.0", false)
	if s := h.check(); deref(s.ErrorCode) != "missing_artifact" || !strings.Contains(deref(s.ErrorDetail), "update-linux-amd64-appimage.json") {
		t.Fatalf("check: %+v", s)
	}
}

func TestAnAppImageUpdateReplacesTheFileAndStartsIt(t *testing.T) {
	for _, tc := range []struct{ before, after os.FileMode }{{0o700, 0o755}, {0o775, 0o775}} {
		t.Run(tc.before.String(), func(t *testing.T) {
			file := runAsAppImage(t, tc.before)
			link := os.Getenv("APPIMAGE")
			r := &relaunches{}
			h := newAppImageHarness(t, r)
			h.releases.publish("1.1.0", false)
			image := h.releases.publishAppImage("1.1.0", nil)
			h.check()
			if s, err := h.m.Download(context.Background()); err != nil || s.Phase != PhaseReady {
				t.Fatalf("download: %+v %v", s, err)
			}
			s, err := h.m.Install(context.Background())
			if err != nil || s.Phase != PhaseInstalling || s.ErrorCode != nil {
				t.Fatalf("install: %+v %v", s, err)
			}
			data, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(data, image) {
				t.Fatalf("the AppImage holds %d bytes, %v", len(data), err)
			}
			if info, _ := os.Stat(file); info.Mode().Perm() != tc.after {
				t.Fatalf("mode %v, want %v", info.Mode().Perm(), tc.after)
			}
			if target, err := os.Readlink(link); err != nil || target != file {
				t.Fatalf("the symlink now names %q, %v", target, err)
			}
			assertAlone(t, file)
			if !slices.Equal(r.files, []string{file}) || len(r.args) != 1 || !slices.Equal(r.args[0], os.Args[1:]) {
				t.Fatalf("relaunched %q with %q", r.files, r.args)
			}
			if h.relaunched != 0 || h.updater.installed != nil {
				t.Fatal("the AppImage went through MyGo's updater")
			}
			if want := []string{"stop", "wait 10s"}; h.core.starts != 0 || !slices.Equal(h.core.events, want) {
				t.Fatalf("core events %q starts %d", h.core.events, h.core.starts)
			}
		})
	}
}

func TestAFailedAppImageUpdateLeavesTheFileAsItWas(t *testing.T) {
	other := newTestKey(t)
	for name, tc := range map[string]struct {
		edit       func(m *manifest, image []byte)
		failWrites bool
		code       string
	}{
		"bad signature": {edit: func(m *manifest, image []byte) { m.Signature = other.sign(image) }, code: "verification"},
		"wrong size":    {edit: func(m *manifest, _ []byte) { m.Size++ }, code: "verification"},
		"wrong version": {edit: func(m *manifest, _ []byte) { m.Version = "1.0.9" }, code: "manifest"},
		"unofficial url": {edit: func(m *manifest, _ []byte) {
			m.URL = "https://example.com/Calcium-Ion/AstrLink/releases/download/v1.1.0/AstrLink_1.1.0_amd64.AppImage"
		}, code: "manifest"},
		"write failure": {failWrites: true, code: "install"},
	} {
		t.Run(name, func(t *testing.T) {
			file := runAsAppImage(t, 0o755)
			r := &relaunches{}
			h := newAppImageHarness(t, r)
			h.setUpdates(func(u *prefs.UpdatePreferences) { u.Channel = prefs.ChannelPreview })
			h.releases.publish("1.1.0", false)
			h.releases.publishAppImage("1.1.0", tc.edit)
			if tc.failWrites {
				create := createTemp
				t.Cleanup(func() { createTemp = create })
				createTemp = readOnlyTemp
			}
			h.check()
			_, _ = h.m.Download(context.Background())
			_, _ = h.m.Install(context.Background())
			s := h.m.Snapshot()
			if s.Phase != PhaseError || deref(s.ErrorCode) != tc.code {
				t.Fatalf("%+v: %s", s, deref(s.ErrorDetail))
			}
			assertUntouched(t, file, 0o755)
			if len(r.files) != 0 {
				t.Fatalf("relaunched %q", r.files)
			}
			// An install that stopped Core starts it again.
			if h.core.stops != h.core.starts {
				t.Fatalf("core stops %d starts %d", h.core.stops, h.core.starts)
			}
		})
	}
}

// readOnlyTemp creates the temp file like os.CreateTemp but hands it out
// read-only, so writing it fails.
func readOnlyTemp(dir, pattern string) (*os.File, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	name := f.Name()
	f.Close()
	return os.Open(name)
}

func TestTheAppImageInstallerChecksWhatItWrites(t *testing.T) {
	key := newTestKey(t)
	public, err := parsePublicKey(key.public)
	if err != nil {
		t.Fatal(err)
	}
	image := []byte("AppImage of 1.1.0" + strings.Repeat("!", 64))
	file := packageFile{URL: "https://github.com/Calcium-Ion/AstrLink/releases/download/v1.1.0/AstrLink_1.1.0_amd64.AppImage", Size: int64(len(image)), Signature: key.sign(image)}
	tampered := bytes.Clone(image)
	tampered[3] ^= 1
	for name, served := range map[string][]byte{
		"tampered":  tampered,
		"truncated": image[:len(image)-1],
		"oversized": append(bytes.Clone(image), '!'),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "AstrLink.AppImage")
			if err := os.WriteFile(target, oldAppImage, 0o755); err != nil {
				t.Fatal(err)
			}
			open := func(_ context.Context, url string) (io.ReadCloser, error) {
				if url != file.URL {
					return nil, errors.New("unexpected file " + url)
				}
				return io.NopCloser(bytes.NewReader(served)), nil
			}
			err := appImageInstaller{target: target, file: file, key: public, open: open}.Install(context.Background(), nil)
			if err == nil {
				t.Fatal("the installer accepted a bad AppImage")
			}
			assertUntouched(t, target, 0o755)
		})
	}
}

func TestInstallSupportedNeedsAWritableAppImageDirectory(t *testing.T) {
	file := runAsAppImage(t, 0o755)
	if !InstallSupported() {
		t.Fatal("an AppImage in a writable directory cannot install")
	}
	// A directory named as the AppImage is not one: $APPIMAGE is set, so
	// the app does not install, as before.
	t.Setenv("APPIMAGE", filepath.Dir(file))
	if InstallSupported() {
		t.Fatal("a directory installs as an AppImage")
	}
	t.Setenv("APPIMAGE", file)
	if os.Geteuid() == 0 {
		t.Skip("root writes to any directory")
	}
	dir := filepath.Dir(file)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if InstallSupported() {
		t.Fatal("an AppImage in a read-only directory can install")
	}
}
