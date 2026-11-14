// Package pluginbuild builds a plugin project into the files the gateway
// loads: plugin.js and its source map for the plugin's backend part, and
// client.js for its interface. A project is a directory with a package.json
// whose astrlink field names the entries, or a single plugin.ts or
// plugin.js. The build runs in Core and in the astrlink command, with the
// bundler compiled in, so neither needs Node.
package pluginbuild

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Manifest is what a project's package.json tells the build and the
// installer.
type Manifest struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Engines     map[string]string `json:"engines"`
	AstrLink    *Entries          `json:"astrlink"`
	// Dependencies are the packages the build bundles, which a lock file
	// pins.
	Dependencies map[string]string `json:"dependencies"`
	// Author is a name, "Name <email> (url)", or an object with a name.
	Author json.RawMessage `json:"author,omitempty"`
}

// AuthorName is the author's name, "" when the manifest names none.
func (manifest *Manifest) AuthorName() string {
	if manifest == nil || len(manifest.Author) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(manifest.Author, &text) == nil {
		name, _, _ := strings.Cut(text, "<")
		name, _, _ = strings.Cut(name, "(")
		return strings.TrimSpace(name)
	}
	var object struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(manifest.Author, &object) == nil {
		return strings.TrimSpace(object.Name)
	}
	return ""
}

// Entries name a project's entry files, relative to its root.
type Entries struct {
	// Plugin is the backend part, which the gateway runs in moejs.
	Plugin string `json:"plugin"`
	// Client is the interface part, which the desktop runs; "" for none.
	Client string `json:"client,omitempty"`
	// Files are the files and directories the plugin reads at run time
	// beside its code, which the install puts in its install directory,
	// import.meta.dirname.
	Files []string `json:"files,omitempty"`
}

// MaxManifestBytes bounds package.json.
const MaxManifestBytes = 256 << 10

// defaultPluginEntries are where the backend entry is looked for when
// package.json does not name it.
var defaultPluginEntries = []string{"src/plugin.ts", "src/plugin.js", "plugin.ts", "plugin.js"}

// defaultClientEntries are where the interface entry is looked for.
var defaultClientEntries = []string{"src/client.tsx", "src/client.ts", "client.tsx", "client.ts"}

// ReadManifest reads the package.json of the project in dir, nil when it
// has none.
func ReadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read package.json: %w", err)
	}
	if len(data) > MaxManifestBytes {
		return nil, fmt.Errorf("package.json is larger than %d KiB", MaxManifestBytes>>10)
	}
	var manifest Manifest
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &manifest); err != nil {
		return nil, fmt.Errorf("package.json: %w", err)
	}
	return &manifest, nil
}

// ResolveEntries finds the entries of the project in dir: those its
// package.json names, else the conventional files.
func ResolveEntries(dir string, manifest *Manifest) (Entries, error) {
	var entries Entries
	if manifest != nil && manifest.AstrLink != nil {
		entries = *manifest.AstrLink
	}
	if entries.Plugin == "" {
		entries.Plugin = firstExisting(dir, defaultPluginEntries)
	}
	if entries.Plugin == "" {
		return Entries{}, errors.New(`the project has no plugin entry: name it in package.json as "astrlink": { "plugin": "src/plugin.ts" }`)
	}
	if entries.Client == "" && (manifest == nil || manifest.AstrLink == nil || manifest.AstrLink.Client == "") {
		entries.Client = firstExisting(dir, defaultClientEntries)
	}
	for _, entry := range []string{entries.Plugin, entries.Client} {
		if entry == "" {
			continue
		}
		if err := checkEntry(dir, entry); err != nil {
			return Entries{}, err
		}
	}
	for _, file := range entries.Files {
		if _, err := insideProject(dir, file); err != nil {
			return Entries{}, err
		}
	}
	return entries, nil
}

// insideProject returns the path of name, relative to the project in dir,
// when it is inside the project.
func insideProject(dir, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if name == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the file %q is outside the project", name)
	}
	if _, err := os.Stat(filepath.Join(dir, clean)); err != nil {
		return "", fmt.Errorf("the file %q is not in the project", name)
	}
	return clean, nil
}

// checkEntry checks that an entry is a file inside the project.
func checkEntry(dir, entry string) error {
	clean := filepath.Clean(filepath.FromSlash(entry))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("the entry %q is outside the project", entry)
	}
	info, err := os.Stat(filepath.Join(dir, clean))
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("the entry %q is not a file of the project", entry)
	}
	return nil
}

func firstExisting(dir string, names []string) string {
	for _, name := range names {
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err == nil && info.Mode().IsRegular() {
			return name
		}
	}
	return ""
}

// EngineRange is the range of AstrLink versions the project declares it
// works with, engines.astrlink; "" when it declares none.
func (manifest *Manifest) EngineRange() string {
	if manifest == nil {
		return ""
	}
	return strings.TrimSpace(manifest.Engines["astrlink"])
}
