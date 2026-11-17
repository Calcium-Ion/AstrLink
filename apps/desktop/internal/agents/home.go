package agents

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/wsl"
)

// ToolDirs holds the configuration directories that moved away from their
// default place in a home, as CC Switch's directory settings record them;
// "" keeps the default. Cursor has none.
type ToolDirs struct {
	Claude string
	Codex  string
	Grok   string
	// Gemini is Gemini CLI's directory, `~/.gemini` by default.
	Gemini string
	// OpenCode is OpenCode's config directory, `~/.config/opencode` by
	// default.
	OpenCode string
	// OpenClaw is OpenClaw's directory, `~/.openclaw` by default.
	OpenClaw string
	// Pi is Pi's agent directory, `~/.pi/agent` by default.
	Pi string
}

// fields lists the directories, for code that treats each the same.
func (d *ToolDirs) fields() []*string {
	return []*string{&d.Claude, &d.Codex, &d.Grok, &d.Gemini, &d.OpenCode, &d.OpenClaw, &d.Pi}
}

// WSLHome is a home in a WSL distribution, with the tool directories CC
// Switch points into it.
type WSLHome struct {
	wsl.Home
	Dirs ToolDirs
}

// wslDirs is the tool directories that point into one WSL distribution.
type wslDirs struct {
	distribution string
	// home is the home the first directory inside a home sits in, "" when
	// every directory lies outside one.
	home string
	dirs ToolDirs
}

// splitDirs splits tool directories into the desktop user's and those
// inside WSL distributions.
func splitDirs(dirs ToolDirs) (ToolDirs, []wslDirs) {
	var local ToolDirs
	var inWSL []wslDirs
	for i, dir := range dirs.fields() {
		if *dir == "" {
			continue
		}
		distribution, home, ok := wsl.ShareLocation(*dir)
		if !ok {
			*local.fields()[i] = *dir
			continue
		}
		index := -1
		for j, entry := range inWSL {
			if strings.EqualFold(entry.distribution, distribution) {
				index = j
				break
			}
		}
		if index < 0 {
			inWSL = append(inWSL, wslDirs{distribution: distribution})
			index = len(inWSL) - 1
		}
		entry := &inWSL[index]
		if entry.home == "" {
			entry.home = home
		}
		*entry.dirs.fields()[i] = *dir
	}
	return local, inWSL
}

// Homes is the desktop user's home and the WSL homes found beside it.
type Homes struct {
	Host string
	// Dirs is where the desktop user's tools keep their configuration.
	Dirs ToolDirs
	WSL  []WSLHome
	// Unchecked names the registered WSL distributions that were not
	// checked.
	Unchecked []string
}

// FindHomes finds the WSL homes reach selects beside host. Tool
// directories follow CC Switch's settings, which also locate a WSL home
// without asking WSL.
func FindHomes(host string, reach wsl.Reach) (*Homes, error) {
	dirs, inWSL := splitDirs(ConfigDirs(host))
	configured := func(distribution string) *wslDirs {
		for i := range inWSL {
			if strings.EqualFold(inWSL[i].distribution, distribution) {
				return &inWSL[i]
			}
		}
		return nil
	}
	found, unchecked, err := wsl.Homes(func(distribution string) string {
		if entry := configured(distribution); entry != nil {
			return entry.home
		}
		return ""
	}, reach)
	if err != nil {
		return nil, err
	}
	homes := &Homes{Host: host, Dirs: dirs, Unchecked: unchecked}
	for _, home := range found {
		entry := WSLHome{Home: home}
		if configured := configured(home.Distribution); configured != nil {
			entry.Dirs = configured.dirs
		}
		homes.WSL = append(homes.WSL, entry)
	}
	return homes, nil
}

// All returns the desktop user's home, then each WSL home.
func (h *Homes) All() []Home {
	all := []Home{{Host: h.Host, Dirs: h.Dirs}}
	for i := range h.WSL {
		all = append(all, InWSL(h.Host, &h.WSL[i]))
	}
	return all
}

// Get returns the desktop user's home for "", or the named distribution's.
func (h *Homes) Get(distribution string) (Home, bool) {
	if distribution == "" {
		return Home{Host: h.Host, Dirs: h.Dirs}, true
	}
	for i := range h.WSL {
		if strings.EqualFold(h.WSL[i].Distribution, distribution) {
			return InWSL(h.Host, &h.WSL[i]), true
		}
	}
	return Home{}, false
}

// Home is one home: the desktop user's, or one in WSL.
type Home struct {
	// Host is the desktop user's home, which holds the CLI tools in every
	// home run.
	Host string
	// WSL is the WSL home this is, nil for the desktop user's.
	WSL  *WSLHome
	Dirs ToolDirs
}

// Local is a home on this computer with every tool in its default place.
func Local(host string) Home { return Home{Host: host} }

// InWSL is a home in WSL, reached from the desktop user's host home.
func InWSL(host string, home *WSLHome) Home {
	return Home{Host: host, WSL: home, Dirs: home.Dirs}
}

// Dir is the directory AstrLink edits: the WSL home's share, or the host.
func (h Home) Dir() string {
	if h.WSL != nil {
		return h.WSL.Share
	}
	return h.Host
}

// Join joins elements onto the home's directory.
func (h Home) Join(elem ...string) string {
	return filepath.Join(append([]string{h.Dir()}, elem...)...)
}

// Distribution names the WSL distribution, "" for the desktop user's home.
func (h Home) Distribution() string {
	if h.WSL != nil {
		return h.WSL.Distribution
	}
	return ""
}

// Seen returns a desktop path as this home's tools address it.
func (h Home) Seen(path string) (string, bool) {
	if h.WSL == nil {
		return path, true
	}
	return h.WSL.LinuxPath(path)
}

// Shown is a desktop path as this home's tools address it, or as it is.
func (h Home) Shown(path string) string {
	if seen, ok := h.Seen(path); ok {
		return seen
	}
	return path
}

// Windows reports whether this home's tools take Windows paths and
// quoting.
func (h Home) Windows() bool { return h.WSL == nil && runtime.GOOS == "windows" }

func (h Home) dir(moved string, elem ...string) string {
	if moved != "" {
		return moved
	}
	return h.Join(elem...)
}

func (h Home) ClaudeDir() string { return h.dir(h.Dirs.Claude, ".claude") }

// ClaudeJSON is where Claude Code keeps `.claude.json`: beside its default
// directory, and inside a moved one.
func (h Home) ClaudeJSON() string {
	if h.Dirs.Claude != "" && filepath.Clean(h.Dirs.Claude) != h.Join(".claude") {
		return filepath.Join(h.Dirs.Claude, ".claude.json")
	}
	return h.Join(".claude.json")
}

func (h Home) ClaudeDetected() bool { return isDir(h.ClaudeDir()) || isFile(h.ClaudeJSON()) }

func (h Home) CodexDir() string { return h.dir(h.Dirs.Codex, ".codex") }

func (h Home) GrokDir() string { return h.dir(h.Dirs.Grok, ".grok") }

func (h Home) GeminiDir() string { return h.dir(h.Dirs.Gemini, ".gemini") }

func (h Home) OpenCodeDir() string { return h.dir(h.Dirs.OpenCode, ".config", "opencode") }

// OpenCodeDetected reports OpenCode: it makes its config directory only
// once it writes a config, and its data directory marks a run.
func (h Home) OpenCodeDetected() bool {
	return isDir(h.OpenCodeDir()) || isDir(h.Join(".local", "share", "opencode"))
}

func (h Home) OpenClawDir() string { return h.dir(h.Dirs.OpenClaw, ".openclaw") }

func (h Home) PiAgentDir() string { return h.dir(h.Dirs.Pi, ".pi", "agent") }

// PiDir is the directory that marks Pi as installed: its moved agent
// directory, or `~/.pi`.
func (h Home) PiDir() string { return h.dir(h.Dirs.Pi, ".pi") }

func (h Home) PiDetected() bool { return isDir(h.PiDir()) }

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
