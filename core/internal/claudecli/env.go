package claudecli

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// inheritedEnv are the variables a claude child keeps from Core's own
// environment: what the OS, the Keychain and the browser opener need. Every
// ANTHROPIC_* and CLAUDE_* variable is dropped, so the parent's API settings
// can never reach the child.
var inheritedEnv = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LANGUAGE", "TZ", "TMPDIR",
	"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "DBUS_SESSION_BUS_ADDRESS",
	"DISPLAY", "WAYLAND_DISPLAY",
	// Windows needs these to start processes and find the profile.
	"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "TEMP", "TMP", "USERPROFILE", "APPDATA",
	"LOCALAPPDATA", "PROGRAMDATA", "PROGRAMFILES", "HOMEDRIVE", "HOMEPATH", "USERNAME",
}

// proxyEnv are the standard proxy variables. Account commands never inherit
// them: their traffic goes through the account's Tunnel, and model calls go
// to AstrLink's loopback. Only the installer keeps the user's own.
var proxyEnv = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"}

// BaseEnv returns the inherited part of a child environment. A nil source
// reads os.Environ.
func BaseEnv(source []string) []string {
	if source == nil {
		source = os.Environ()
	}
	keep := make([]string, 0, len(source))
	for _, entry := range source {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(name)
		if slices.Contains(inheritedEnv, upper) || strings.HasPrefix(upper, "LC_") {
			keep = append(keep, entry)
		}
	}
	return keep
}

// LoginEnv is the environment of `claude auth …` for one account: the base
// and the account's config directory. The caller adds the account's tunnel.
func LoginEnv(source []string, home, configDir string) []string {
	env := BaseEnv(source)
	env = setEnv(env, "CLAUDE_CONFIG_DIR", configDir)
	if home != "" {
		env = setEnv(env, "HOME", home)
	}
	return env
}

// ServeEnv is the environment of a serving child. extra holds the loopback
// base URL, correlation header and isolation switches.
func ServeEnv(source []string, home, configDir string, extra map[string]string) []string {
	env := BaseEnv(source)
	env = setEnv(env, "CLAUDE_CONFIG_DIR", configDir)
	if home != "" {
		env = setEnv(env, "HOME", home)
	}
	return withEnv(env, extra)
}

// installEnv is the environment of the official installer: the base and the
// user's own proxy settings.
func installEnv(source []string, home string) []string {
	if source == nil {
		source = os.Environ()
	}
	env := BaseEnv(source)
	for _, entry := range source {
		name, _, _ := strings.Cut(entry, "=")
		if slices.Contains(proxyEnv, strings.ToUpper(name)) {
			env = append(env, entry)
		}
	}
	if home != "" {
		env = setEnv(env, "HOME", home)
	}
	return env
}

// withEnv sets each variable, in a stable order.
func withEnv(env []string, values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		env = setEnv(env, name, values[name])
	}
	return env
}

func setEnv(env []string, name, value string) []string {
	for index, entry := range env {
		existing, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(existing, name) && (runtime.GOOS == "windows" || existing == name) {
			env[index] = name + "=" + value
			return env
		}
	}
	return append(env, name+"="+value)
}

// AddPathDir puts dir first on PATH, so a CLI found outside PATH can still
// find its siblings.
func AddPathDir(env []string, dir string) []string {
	for index, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, "PATH") {
			for _, existing := range filepath.SplitList(value) {
				if existing == dir {
					return env
				}
			}
			env[index] = name + "=" + dir + string(os.PathListSeparator) + value
			return env
		}
	}
	return append(env, "PATH="+dir)
}
