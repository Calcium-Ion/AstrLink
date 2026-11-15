package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// CCSwitchInstalled reports whether the OS has an app registered for CC
// Switch's links, so a write can warn that switching providers there
// overwrites it.
func CCSwitchInstalled() bool { return opensLinks("ccswitch") }

// OpensLinks reports whether the OS has an app registered for `scheme:`
// links, as the client import links need.
func OpensLinks(scheme string) bool { return opensLinks(scheme) }

// ConfigDirs returns the configuration directories CC Switch's settings
// move each app to, so agent installs and client configs reach the same
// directories, inside WSL included. Missing or unreadable settings move
// nothing.
func ConfigDirs(home string) ToolDirs {
	var settings map[string]any
	if raw, err := os.ReadFile(filepath.Join(home, ".cc-switch", "settings.json")); err == nil {
		_ = json.Unmarshal(raw, &settings)
	}
	dir := func(key string) string {
		raw, _ := settings[key].(string)
		return resolveDir(home, raw)
	}
	return ToolDirs{
		Claude:   dir("claudeConfigDir"),
		Codex:    dir("codexConfigDir"),
		Grok:     dir("grokConfigDir"),
		Gemini:   dir("geminiConfigDir"),
		OpenCode: dir("opencodeConfigDir"),
		OpenClaw: dir("openclawConfigDir"),
		Pi:       dir("piConfigDir"),
	}
}

// resolveDir resolves a directory setting the way CC Switch does: `~` is
// the home, and anything still relative is ignored.
func resolveDir(home, raw string) string {
	raw = strings.TrimSpace(raw)
	path := raw
	switch {
	case raw == "":
		return ""
	case raw == "~":
		path = home
	case strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`):
		parts := strings.FieldsFunc(raw[2:], func(c rune) bool { return c == '/' || c == '\\' })
		path = filepath.Join(append([]string{home}, parts...)...)
	}
	if !filepath.IsAbs(path) {
		return ""
	}
	return path
}
