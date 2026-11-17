package ui

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The Tauri shell started at login through its own entries, named after
// the product ("AstrLink", "AstrLink Dev" for the development instance)
// and passing LegacyAutostartArg. MyGo's login item is named after the
// bundle identifier, so an old entry left behind would start the app a
// second time, or at all once autostart is turned off. They are removed
// at startup; only one that passes the argument is the shell's.

// LegacyAutostartArg is the argument the Tauri shell's login entries
// passed. A launch that has it is a login launch.
const LegacyAutostartArg = "--autostart"

// legacyAutostart finds the shell's login entries of one instance.
type legacyAutostart struct {
	goos string
	home string
	name string
	// run removes the Windows Run entry; nil elsewhere.
	run legacyRunEntries
}

// legacyRunEntries are the values of the user's Run key and Task Manager's
// StartupApproved\Run key (Windows).
type legacyRunEntries interface {
	Value(name string) (string, bool, error)
	Delete(name string) error
}

// files are the shell's entry files: a launch agent on macOS (the plugin
// used launch agents, not login items) and an autostart desktop entry
// under ~/.config on Linux, whatever XDG_CONFIG_HOME says.
func (l legacyAutostart) files() []string {
	switch l.goos {
	case "darwin":
		return []string{filepath.Join(l.home, "Library", "LaunchAgents", l.name+".plist")}
	case "linux", "freebsd", "openbsd", "netbsd":
		return []string{filepath.Join(l.home, ".config", "autostart", l.name+".desktop")}
	}
	return nil
}

// isLegacyEntry reports whether an entry file is the shell's: it starts
// the app with the autostart argument.
func isLegacyEntry(goos string, data []byte) bool {
	text := string(data)
	if goos == "darwin" {
		return strings.Contains(text, "<string>"+LegacyAutostartArg+"</string>")
	}
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if exec, ok := strings.CutPrefix(line, "Exec="); ok && isLegacyCommand(exec) {
			return true
		}
	}
	return false
}

// isLegacyCommand reports whether a login command line passes the
// autostart argument.
func isLegacyCommand(command string) bool {
	return strings.HasSuffix(strings.TrimSpace(command), " "+LegacyAutostartArg)
}

// Remove deletes the shell's entries and reports those it removed.
func (l legacyAutostart) Remove() ([]string, error) {
	var removed []string
	var errs []error
	for _, path := range l.files() {
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !isLegacyEntry(l.goos, data) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, path)
	}
	if l.run != nil {
		command, ok, err := l.run.Value(l.name)
		switch {
		case err != nil:
			errs = append(errs, err)
		case ok && isLegacyCommand(command):
			if err := l.run.Delete(l.name); err != nil {
				errs = append(errs, err)
			} else {
				removed = append(removed, `HKCU\`+legacyRunKey+`\`+l.name)
			}
		}
	}
	return removed, errors.Join(errs...)
}

const (
	legacyRunKey      = `Software\Microsoft\Windows\CurrentVersion\Run`
	legacyApprovedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`
)
