package plugininstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/QuantumNous/astrlink/core/contract"
)

// lockfile is a lock file read: which package the code of each package, or
// of the project, gets for a name it imports.
type lockfile interface {
	// dependency is the key of the package the code of from gets for
	// name; from is "" for the project's own code. ok is false when the
	// lock file has none.
	dependency(from, name string) (key string, ok bool)
	// resolve describes the package key names.
	resolve(key string) (lockedPackage, error)
}

// lockedPackage is a package a lock file pins.
type lockedPackage struct {
	// Name is the package's name in the registry, which an alias imports
	// under another.
	Name    string
	Version string
	// Path is the archive's path on a registry: /name/-/file.tgz.
	Path      string
	Integrity string
}

// lockFiles are the lock files read, in the order looked for.
var lockFiles = []string{"package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "bun.lock"}

// maxLockBytes bounds a lock file.
const maxLockBytes = 32 << 20

// readLock reads the lock file of the project in dir: nil when it has none.
func readLock(dir string) (lockfile, string, []byte, error) {
	for _, name := range []string{"bun.lockb", "yarn.lock"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil && !hasAnyLock(dir) {
			return nil, name, nil, fail(contract.PluginInstallLockfileUnsupported, name)
		}
	}
	for _, name := range lockFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, name, nil, err
		}
		if len(data) > maxLockBytes {
			return nil, name, nil, fail(contract.PluginInstallTooLarge, name)
		}
		var lock lockfile
		switch name {
		case "pnpm-lock.yaml":
			lock, err = parsePnpmLock(data)
		case "bun.lock":
			lock, err = parseBunLock(data)
		default:
			lock, err = parseNpmLock(data)
		}
		if err != nil {
			return nil, name, nil, err
		}
		return lock, name, data, nil
	}
	return nil, "", nil, nil
}

func hasAnyLock(dir string) bool {
	for _, name := range lockFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// unsupportedLock says a lock file's format is not one the installer reads.
func unsupportedLock(name, why string) error {
	return fail(contract.PluginInstallLockfileUnsupported, name+": "+why)
}

// unsupportedPackage says a package does not come from an npm registry.
func unsupportedPackage(name, from string) error {
	return fail(contract.PluginInstallDependencyUnsupported, name+" ("+from+")")
}

// archivePath is where a registry serves name@version, given where the
// lock file says it came from: the registry it came from does not matter,
// for the lock file's hash checks what any registry returns.
func archivePath(name, version, resolved string) (string, error) {
	if resolved == "" {
		base := name
		if at := strings.LastIndex(name, "/"); at >= 0 {
			base = name[at+1:]
		}
		return "/" + name + "/-/" + base + "-" + version + ".tgz", nil
	}
	parsed, err := url.Parse(resolved)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return "", unsupportedPackage(name+"@"+version, resolved)
	}
	at := strings.Index(parsed.Path, "/"+name+"/-/")
	if at < 0 || !strings.HasSuffix(parsed.Path, ".tgz") {
		return "", unsupportedPackage(name+"@"+version, resolved)
	}
	return parsed.Path[at:], nil
}

// npmLock is package-lock.json or npm-shrinkwrap.json of lockfileVersion 2
// or 3: the node_modules tree npm would lay out.
type npmLock struct {
	packages map[string]npmEntry
}

type npmEntry struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Resolved  string `json:"resolved"`
	Integrity string `json:"integrity"`
	Link      bool   `json:"link"`
	InBundle  bool   `json:"inBundle"`
}

func parseNpmLock(data []byte) (lockfile, error) {
	var file struct {
		LockfileVersion int                 `json:"lockfileVersion"`
		Packages        map[string]npmEntry `json:"packages"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &file); err != nil {
		return nil, unsupportedLock("package-lock.json", "not JSON")
	}
	if file.LockfileVersion < 2 || file.Packages == nil {
		return nil, unsupportedLock("package-lock.json", fmt.Sprintf("lockfileVersion %d", file.LockfileVersion))
	}
	return &npmLock{packages: file.Packages}, nil
}

// dependency finds name as Node would from the package at from: in its
// own node_modules, then in each one above it.
func (lock *npmLock) dependency(from, name string) (string, bool) {
	for {
		candidate := "node_modules/" + name
		if from != "" {
			candidate = from + "/" + candidate
		}
		if _, ok := lock.packages[candidate]; ok {
			return candidate, true
		}
		if from == "" {
			return "", false
		}
		if at := strings.LastIndex(from, "/node_modules/"); at >= 0 {
			from = from[:at]
		} else {
			from = ""
		}
	}
}

func (lock *npmLock) resolve(key string) (lockedPackage, error) {
	entry := lock.packages[key]
	name := entry.Name
	if name == "" {
		name = key[strings.LastIndex(key, "node_modules/")+len("node_modules/"):]
	}
	switch {
	case entry.Link:
		return lockedPackage{}, unsupportedPackage(name, "workspace "+entry.Resolved)
	case entry.InBundle:
		return lockedPackage{}, unsupportedPackage(name, "bundled")
	case entry.Version == "":
		return lockedPackage{}, unsupportedPackage(name, "no version")
	}
	path, err := archivePath(name, entry.Version, entry.Resolved)
	if err != nil {
		return lockedPackage{}, err
	}
	return lockedPackage{Name: name, Version: entry.Version, Path: path, Integrity: entry.Integrity}, nil
}

// pnpmLock is pnpm-lock.yaml of lockfileVersion 6 or 9: each package's own
// dependencies, by version.
type pnpmLock struct {
	// importer maps the project's dependencies to their versions.
	importer map[string]string
	packages map[string]pnpmPackage
	// snapshots are the dependencies of each package with its peers
	// (lockfileVersion 9); nil before.
	snapshots map[string]map[string]string
}

type pnpmDependency struct {
	Version string `yaml:"version"`
}

type pnpmImporter struct {
	Dependencies         map[string]pnpmDependency `yaml:"dependencies"`
	DevDependencies      map[string]pnpmDependency `yaml:"devDependencies"`
	OptionalDependencies map[string]pnpmDependency `yaml:"optionalDependencies"`
}

type pnpmPackage struct {
	Resolution struct {
		Integrity string `yaml:"integrity"`
		Tarball   string `yaml:"tarball"`
		Type      string `yaml:"type"`
	} `yaml:"resolution"`
	Dependencies         map[string]string `yaml:"dependencies"`
	OptionalDependencies map[string]string `yaml:"optionalDependencies"`
}

type pnpmSnapshot struct {
	Dependencies         map[string]string `yaml:"dependencies"`
	OptionalDependencies map[string]string `yaml:"optionalDependencies"`
}

func parsePnpmLock(data []byte) (lockfile, error) {
	var file struct {
		LockfileVersion string                  `yaml:"lockfileVersion"`
		Importers       map[string]pnpmImporter `yaml:"importers"`
		pnpmImporter    `yaml:",inline"`
		Packages        map[string]pnpmPackage  `yaml:"packages"`
		Snapshots       map[string]pnpmSnapshot `yaml:"snapshots"`
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, unsupportedLock("pnpm-lock.yaml", "not YAML")
	}
	major, _, _ := strings.Cut(file.LockfileVersion, ".")
	if major != "6" && major != "9" {
		return nil, unsupportedLock("pnpm-lock.yaml", "lockfileVersion "+file.LockfileVersion)
	}
	root := file.pnpmImporter
	if importer, ok := file.Importers["."]; ok {
		root = importer
	}
	lock := &pnpmLock{importer: map[string]string{}, packages: map[string]pnpmPackage{}}
	for _, group := range []map[string]pnpmDependency{root.DevDependencies, root.OptionalDependencies, root.Dependencies} {
		for name, dependency := range group {
			lock.importer[name] = dependency.Version
		}
	}
	for key, entry := range file.Packages {
		lock.packages[strings.TrimPrefix(key, "/")] = entry
	}
	if major == "9" {
		lock.snapshots = map[string]map[string]string{}
		for key, snapshot := range file.Snapshots {
			dependencies := map[string]string{}
			for _, group := range []map[string]string{snapshot.OptionalDependencies, snapshot.Dependencies} {
				for name, version := range group {
					dependencies[name] = version
				}
			}
			lock.snapshots[key] = dependencies
		}
	}
	return lock, nil
}

func (lock *pnpmLock) dependency(from, name string) (string, bool) {
	var version string
	var ok bool
	switch {
	case from == "":
		version, ok = lock.importer[name]
	case lock.snapshots != nil:
		version, ok = lock.snapshots[from][name]
	default:
		entry := lock.packages[from]
		if version, ok = entry.Dependencies[name]; !ok {
			version, ok = entry.OptionalDependencies[name]
		}
	}
	if !ok || version == "" {
		return "", false
	}
	return pnpmKey(name, version), true
}

// pnpmKey is the key of the package a dependency's version names: an
// alias names another package, as /other@1.0.0 or other@1.0.0.
func pnpmKey(name, version string) string {
	if strings.HasPrefix(version, "/") {
		return version[1:]
	}
	base, _, _ := strings.Cut(version, "(")
	if len(base) > 1 && strings.Contains(base[1:], "@") {
		return version
	}
	return name + "@" + version
}

func (lock *pnpmLock) resolve(key string) (lockedPackage, error) {
	base, _, _ := strings.Cut(key, "(")
	name, version, ok := splitNameVersion(base)
	if !ok {
		return lockedPackage{}, unsupportedPackage(key, "pnpm-lock.yaml")
	}
	if strings.HasPrefix(version, "link:") || strings.HasPrefix(version, "file:") {
		return lockedPackage{}, unsupportedPackage(name, version)
	}
	entry, found := lock.packages[base]
	if lock.snapshots == nil {
		entry, found = lock.packages[key]
	}
	if !found {
		return lockedPackage{}, unsupportedPackage(key, "not in pnpm-lock.yaml")
	}
	if entry.Resolution.Type != "" {
		return lockedPackage{}, unsupportedPackage(key, entry.Resolution.Type)
	}
	path, err := archivePath(name, version, entry.Resolution.Tarball)
	if err != nil {
		return lockedPackage{}, err
	}
	return lockedPackage{Name: name, Version: version, Path: path, Integrity: entry.Resolution.Integrity}, nil
}

// splitNameVersion splits name@version, a scoped name's @ aside.
func splitNameVersion(text string) (string, string, bool) {
	at := strings.LastIndex(text, "@")
	if at <= 0 || at == len(text)-1 {
		return "", "", false
	}
	return text[:at], text[at+1:], true
}

// bunLock is bun.lock: the node_modules tree bun would lay out, its keys
// the paths of nested packages.
type bunLock struct {
	packages map[string][]json.RawMessage
}

func parseBunLock(data []byte) (lockfile, error) {
	var file struct {
		LockfileVersion *int                         `json:"lockfileVersion"`
		Packages        map[string][]json.RawMessage `json:"packages"`
	}
	if err := json.Unmarshal(stripJSONC(data), &file); err != nil || file.LockfileVersion == nil {
		return nil, unsupportedLock("bun.lock", "not a text lock file")
	}
	if *file.LockfileVersion > 1 {
		return nil, unsupportedLock("bun.lock", fmt.Sprintf("lockfileVersion %d", *file.LockfileVersion))
	}
	return &bunLock{packages: file.Packages}, nil
}

func (lock *bunLock) dependency(from, name string) (string, bool) {
	for {
		candidate := name
		if from != "" {
			candidate = from + "/" + name
		}
		if _, ok := lock.packages[candidate]; ok {
			return candidate, true
		}
		if from == "" {
			return "", false
		}
		from = bunParent(from)
	}
}

// bunParent is the key of the package a nested key sits in: its path
// without the last name, a scope and its name counting as one.
func bunParent(key string) string {
	segments := strings.Split(key, "/")
	var names []string
	for index := 0; index < len(segments); index++ {
		if strings.HasPrefix(segments[index], "@") && index+1 < len(segments) {
			names = append(names, segments[index]+"/"+segments[index+1])
			index++
			continue
		}
		names = append(names, segments[index])
	}
	return strings.Join(names[:len(names)-1], "/")
}

func (lock *bunLock) resolve(key string) (lockedPackage, error) {
	entry := lock.packages[key]
	var spec, registry, integrity string
	if len(entry) < 1 || json.Unmarshal(entry[0], &spec) != nil {
		return lockedPackage{}, unsupportedPackage(key, "bun.lock")
	}
	name, version, ok := splitNameVersion(spec)
	if !ok {
		return lockedPackage{}, unsupportedPackage(key, "bun.lock")
	}
	if len(entry) != 4 || json.Unmarshal(entry[1], &registry) != nil || json.Unmarshal(entry[3], &integrity) != nil {
		// Workspaces, links, git and tarballs have other shapes.
		return lockedPackage{}, unsupportedPackage(name, version)
	}
	resolved := ""
	if strings.HasSuffix(registry, ".tgz") {
		resolved = registry
	}
	path, err := archivePath(name, version, resolved)
	if err != nil {
		return lockedPackage{}, err
	}
	return lockedPackage{Name: name, Version: version, Path: path, Integrity: integrity}, nil
}

// stripJSONC turns JSON with comments and trailing commas, as bun writes
// its lock file, into JSON.
func stripJSONC(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	for index := 0; index < len(data); index++ {
		char := data[index]
		if inString {
			out = append(out, char)
			switch char {
			case '\\':
				if index+1 < len(data) {
					index++
					out = append(out, data[index])
				}
			case '"':
				inString = false
			}
			continue
		}
		switch {
		case char == '"':
			inString = true
			out = append(out, char)
		case char == '/' && index+1 < len(data) && data[index+1] == '/':
			for index < len(data) && data[index] != '\n' {
				index++
			}
			index--
		case char == '/' && index+1 < len(data) && data[index+1] == '*':
			end := bytes.Index(data[index+2:], []byte("*/"))
			if end < 0 {
				index = len(data)
			} else {
				index += end + 3
			}
		case char == ']' || char == '}':
			// A comma before the closing bracket goes.
			trimmed := bytes.TrimRight(out, " \t\r\n")
			if len(trimmed) > 0 && trimmed[len(trimmed)-1] == ',' {
				out = append(trimmed[:len(trimmed)-1], out[len(trimmed):]...)
			}
			out = append(out, char)
		default:
			out = append(out, char)
		}
	}
	return out
}
