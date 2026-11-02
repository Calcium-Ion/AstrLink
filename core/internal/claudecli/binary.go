// Package claudecli runs the user's own Claude Code CLI for official Claude
// subscription accounts: it finds the binary, keeps one config directory per
// account, signs in through `claude auth login` and reads `claude auth status`.
// It never reads the CLI's credentials; the CLI keeps them in the Keychain or
// in its config directory.
package claudecli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MinimumVersion is the oldest CLI that honours client_composed user messages
// (no slash commands, no file mentions, no per-turn attachments).
const MinimumVersion = "2.1.248"

// BinaryEnv names an explicit claude executable. It replaces the search.
const BinaryEnv = "ASTRLINK_CLAUDE_BIN"

var (
	// ErrNotInstalled means no claude executable was found.
	ErrNotInstalled = errors.New("Claude Code is not installed")
	// ErrTooOld means the claude executable predates MinimumVersion.
	ErrTooOld = errors.New("Claude Code is too old")
)

var versionPattern = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)

// Binary is a located claude executable.
type Binary struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

// Locator finds the claude executable. The first match wins: the explicit
// path, then PATH, then the official installer's locations, then the server
// edition's install home.
type Locator struct {
	// Configured is an explicit executable; when set, nothing else is tried.
	Configured string
	// InstallHome is the HOME the server edition installs the CLI into.
	InstallHome string
	// Home is the user's home directory; empty uses os.UserHomeDir.
	Home string
	// LookPath searches PATH; nil uses exec.LookPath.
	LookPath func(string) (string, error)
	// Version runs `<path> --version`; nil runs the executable.
	Version func(ctx context.Context, path string) (string, error)

	mu    sync.Mutex
	cache map[string]cachedVersion
}

type cachedVersion struct {
	modified time.Time
	size     int64
	version  string
}

// NewLocator reads BinaryEnv and uses installHome for the server edition.
func NewLocator(installHome string) *Locator {
	return &Locator{Configured: strings.TrimSpace(os.Getenv(BinaryEnv)), InstallHome: installHome}
}

func executableName() string {
	if runtime.GOOS == "windows" {
		return "claude.exe"
	}
	return "claude"
}

// candidates lists the paths to try, in order.
func (locator *Locator) candidates() []string {
	if locator.Configured != "" {
		return []string{locator.Configured}
	}
	var paths []string
	lookPath := locator.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if found, err := lookPath("claude"); err == nil {
		paths = append(paths, found)
	}
	home := locator.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	name := executableName()
	if home != "" {
		// The native installer links ~/.local/bin/claude; older npm-local
		// installs live in ~/.claude/local.
		paths = append(paths,
			filepath.Join(home, ".local", "bin", name),
			filepath.Join(home, ".claude", "local", name),
		)
	}
	if runtime.GOOS != "windows" {
		paths = append(paths, "/opt/homebrew/bin/claude", "/usr/local/bin/claude")
	}
	if locator.InstallHome != "" {
		paths = append(paths, filepath.Join(locator.InstallHome, ".local", "bin", name))
	}
	return paths
}

// Find returns the first usable claude executable and its version.
func (locator *Locator) Find(ctx context.Context) (Binary, error) {
	var tooOld *Binary
	for _, path := range locator.candidates() {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		version, err := locator.version(ctx, path, info)
		if err != nil {
			continue
		}
		binary := Binary{Path: path, Version: version}
		if !VersionAtLeast(version, MinimumVersion) {
			if tooOld == nil {
				tooOld = &binary
			}
			continue
		}
		return binary, nil
	}
	if tooOld != nil {
		return *tooOld, fmt.Errorf("%w: %s is older than %s; run `claude update`", ErrTooOld, tooOld.Version, MinimumVersion)
	}
	return Binary{}, ErrNotInstalled
}

func (locator *Locator) version(ctx context.Context, path string, info os.FileInfo) (string, error) {
	locator.mu.Lock()
	if cached, ok := locator.cache[path]; ok && cached.modified.Equal(info.ModTime()) && cached.size == info.Size() {
		locator.mu.Unlock()
		return cached.version, nil
	}
	locator.mu.Unlock()
	read := locator.Version
	if read == nil {
		read = runVersion
	}
	output, err := read(ctx, path)
	if err != nil {
		return "", err
	}
	match := versionPattern.FindString(output)
	if match == "" {
		return "", fmt.Errorf("unrecognized claude version output")
	}
	locator.mu.Lock()
	if locator.cache == nil {
		locator.cache = make(map[string]cachedVersion)
	}
	locator.cache[path] = cachedVersion{modified: info.ModTime(), size: info.Size(), version: match}
	locator.mu.Unlock()
	return match, nil
}

func runVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--version")
	command.Env = BaseEnv(nil)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// VersionAtLeast compares dotted numeric versions; unparsable versions fail.
func VersionAtLeast(version, minimum string) bool {
	have, ok := parseVersion(version)
	if !ok {
		return false
	}
	want, ok := parseVersion(minimum)
	if !ok {
		return false
	}
	for index := range have {
		if have[index] != want[index] {
			return have[index] > want[index]
		}
	}
	return true
}

func parseVersion(value string) ([3]int, bool) {
	var parts [3]int
	match := versionPattern.FindStringSubmatch(value)
	if match == nil {
		return parts, false
	}
	for index := range parts {
		number, err := strconv.Atoi(match[index+1])
		if err != nil {
			return parts, false
		}
		parts[index] = number
	}
	return parts, true
}
