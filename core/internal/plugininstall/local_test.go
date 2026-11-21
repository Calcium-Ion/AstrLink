package plugininstall

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// writeProject writes files under dir.
func writeProject(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A local folder is built like a GitHub commit, its dependencies by its
// lock file and not from the node_modules beside it, and the folder is
// left as it was.
func TestInstallFromALocalFolder(t *testing.T) {
	fixture := newFixture(t)
	installer := fixture.installer()
	folder := filepath.Join(t.TempDir(), "hello-plugin")
	files := pluginFiles("1.0.0", "", "package-lock.json", fixture.npmLockFile())
	files["node_modules/tiny-lib/package.json"] = `{"name":"tiny-lib","version":"2.0.0","main":"index.js"}`
	files["node_modules/tiny-lib/index.js"] = `exports.greet = () => "<installed by hand>";`
	writeProject(t, folder, files)

	review, err := installer.Preview(context.Background(), "  "+folder+"  ", func(string) *Existing { return nil })
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if source := review.Source; source.Kind != contract.PluginSourceLocal || source.URL != folder || source.Repository != "hello-plugin" || source.RefKind != "" || source.Commit != "" {
		t.Fatalf("source = %+v", source)
	}
	if review.Key != "hello" || review.Version != "1.0.0" || review.Lockfile != "package-lock.json" || len(review.Dependencies) != 2 {
		t.Fatalf("review = %+v", review)
	}
	for name := range files {
		if _, err := os.Stat(filepath.Join(folder, filepath.FromSlash(name))); err != nil {
			t.Fatalf("the folder lost %s: %v", name, err)
		}
	}
	if _, err := installer.Install(review.Token); err != nil {
		t.Fatalf("Install: %v", err)
	}
	built, err := os.ReadFile(filepath.Join(fixture.dir, "hello", "plugin.js"))
	if err != nil || !strings.Contains(string(built), "<two>") || strings.Contains(string(built), "installed by hand") {
		t.Fatalf("plugin.js = %s, %v", built, err)
	}
	sources, err := plugin.Discover(nil, fixture.dir)
	if err != nil || len(sources) != 1 || sources[0].Installed == nil || sources[0].Installed.Source.Kind != contract.PluginSourceLocal {
		t.Fatalf("Discover = %+v, %v", sources, err)
	}
	record := *sources[0].Installed
	if check, err := installer.CheckUpdate(context.Background(), record); err != nil || !check.Pinned || check.Available {
		t.Fatalf("CheckUpdate = %+v, %v", check, err)
	}

	// Installing from the folder again builds what it holds now.
	writeProject(t, folder, pluginFiles("1.1.0", `"storage"`, "package-lock.json", fixture.npmLockFile()))
	current := &Existing{Origin: string(plugin.OriginDirectory), Loaded: true, Name: "Hello", Version: "1.0.0", Capabilities: []string{}, Installed: &record}
	again, err := installer.Preview(context.Background(), record.Source.URL, func(string) *Existing { return current })
	if err != nil {
		t.Fatalf("Preview again: %v", err)
	}
	if again.Version != "1.1.0" || again.Replaces == nil || again.Replaces.Origin != contract.PluginReplacesSource || again.Replaces.Repository != "hello-plugin" {
		t.Fatalf("again = %+v / %+v", again, again.Replaces)
	}
}

func TestALocalFolderMustBeAnAbsolutePathToAFolder(t *testing.T) {
	installer := newFixture(t).installer()
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := installer.Preview(context.Background(), missing, nil); !hasReason(err, contract.PluginInstallFolderNotFound) {
		t.Fatalf("Preview of a missing folder = %v", err)
	}
	file := filepath.Join(t.TempDir(), "plugin.ts")
	writeProject(t, filepath.Dir(file), map[string]string{"plugin.ts": "export const meta = {};"})
	if _, err := installer.Preview(context.Background(), file, nil); !hasReason(err, contract.PluginInstallFolderNotFound) {
		t.Fatalf("Preview of a file = %v", err)
	}
	if _, err := installer.Preview(context.Background(), "plugins/hello/x", nil); !hasReason(err, contract.PluginInstallInvalidLink) {
		t.Fatalf("Preview of a relative path = %v", err)
	}
	empty := t.TempDir()
	if _, err := installer.Preview(context.Background(), empty, nil); !hasReason(err, contract.PluginInstallNoPlugin) {
		t.Fatalf("Preview of a folder without a plugin = %v", err)
	}
}
