// Package wsl reaches the WSL distributions whose agent tools the Windows
// desktop configures. Their homes are edited through the `\\wsl$` share, and
// their tools run the desktop's Windows CLI through WSL interop. A home comes
// from CC Switch's directory settings when they point into the
// distribution, and from its default user otherwise.
package wsl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Home is one home in a WSL distribution.
type Home struct {
	// Distribution is the distribution's name, as `wsl.exe --distribution`
	// takes it.
	Distribution string
	// Share is the home as Windows reaches it, through the distribution's
	// share.
	Share string
	// MountRoot is where the distribution mounts Windows drives: `/mnt` by
	// default, empty when they mount at `/`.
	MountRoot string
}

// LinuxPath returns a Windows drive path as the distribution sees it; false
// for a path that is not on a drive letter.
func (h Home) LinuxPath(path string) (string, bool) {
	path = strings.TrimPrefix(path, `\\?\`)
	if len(path) < 2 || !isASCIIAlpha(path[0]) || path[1] != ':' {
		return "", false
	}
	rest := strings.ReplaceAll(path[2:], `\`, "/")
	if rest != "" && !strings.HasPrefix(rest, "/") {
		return "", false
	}
	return h.MountRoot + "/" + strings.ToLower(path[:1]) + strings.TrimRight(rest, "/"), true
}

func isASCIIAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// ShareLocation returns the distribution a `\\wsl$` or `\\wsl.localhost`
// path lies in, and the home it lies in: `/home/<user>` or `/root`, reached
// through the same share, or "" outside one.
func ShareLocation(path string) (distribution, home string, ok bool) {
	server, distribution, parts, ok := shareParts(path)
	if !ok {
		return "", "", false
	}
	switch {
	case len(parts) >= 2 && parts[0] == "home":
		home = `\\` + server + `\` + distribution + `\home\` + parts[1]
	case len(parts) >= 1 && parts[0] == "root":
		home = `\\` + server + `\` + distribution + `\root`
	}
	return distribution, home, true
}

// insideShare returns the distribution a `\\wsl$` or `\\wsl.localhost` path
// lies in, and the path as the distribution sees it.
func insideShare(path string) (distribution, linux string, ok bool) {
	_, distribution, parts, ok := shareParts(path)
	if !ok {
		return "", "", false
	}
	return distribution, "/" + strings.Join(parts, "/"), true
}

// shareParts returns a share path's server, its distribution, and its
// segments inside the distribution.
func shareParts(path string) (server, distribution string, parts []string, ok bool) {
	raw := strings.ReplaceAll(path, "/", `\`)
	if rest, found := strings.CutPrefix(raw, `\\?\UNC\`); found {
		raw = `\\` + rest
	}
	rest, found := strings.CutPrefix(raw, `\\`)
	if !found {
		return "", "", nil, false
	}
	var segments []string
	for _, part := range strings.Split(rest, `\`) {
		if part != "" && part != "." {
			segments = append(segments, part)
		}
	}
	if len(segments) < 2 || !strings.EqualFold(segments[0], "wsl$") && !strings.EqualFold(segments[0], "wsl.localhost") {
		return "", "", nil, false
	}
	parts = segments[2:]
	for _, part := range parts {
		if part == ".." {
			return "", "", nil, false
		}
	}
	return segments[0], segments[1], parts, true
}

// SettleShareMode gives temporary, staged in a WSL share to replace target,
// the Linux mode the write should leave, before its contents land:
// owner-only for a secret, else the mode of the file it replaces. Windows
// cannot set Linux modes through the share, so the distribution's default
// user, who owns what the share creates, sets them. It reports whether the
// files lie in a share.
func SettleShareMode(temporary, target string, secret bool) (bool, error) {
	return settleShareMode(temporary, target, secret)
}

// fileMode returns the permission bits `stat -c %a` prints, as `chmod`
// takes them.
func fileMode(output string) (string, bool) {
	mode := strings.TrimSpace(output)
	if len(mode) < 1 || len(mode) > 4 {
		return "", false
	}
	for _, c := range mode {
		if c < '0' || c > '7' {
			return "", false
		}
	}
	return mode, true
}

// Distribution is a registered distribution.
type Distribution struct {
	Name    string
	Running bool
}

// Distributions lists the registered distributions, apart from Docker
// Desktop's internal ones. Listing them starts none.
func Distributions() []Distribution { return distributions() }

type reachKind int

const (
	reachRunning reachKind = iota
	reachRunningAnd
	reachNamed
	reachRunningAmong
)

// Reach is which distributions to reach.
type Reach struct {
	kind  reachKind
	names []string
}

// Running reaches the running distributions; a check never starts one.
func Running() Reach { return Reach{kind: reachRunning} }

// RunningAnd reaches the running distributions and the named ones, which
// start if stopped.
func RunningAnd(names []string) Reach { return Reach{reachRunningAnd, names} }

// Named reaches only the named distributions, which start if stopped.
func Named(names []string) Reach { return Reach{reachNamed, names} }

// RunningAmong reaches the named distributions that are running.
func RunningAmong(names []string) Reach { return Reach{reachRunningAmong, names} }

func (r Reach) named(name string) bool {
	for _, n := range r.names {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// selects returns whether reach reaches a distribution, and whether it is
// one to start.
func (r Reach) selects(d Distribution) (reached, start bool) {
	switch r.kind {
	case reachRunningAnd:
		start = r.named(d.Name)
		return d.Running || start, start
	case reachNamed:
		return r.named(d.Name), r.named(d.Name)
	case reachRunningAmong:
		return d.Running && r.named(d.Name), false
	}
	return d.Running, false
}

// Homes returns the homes of the distributions reach selects, and the
// names of the registered ones left unchecked. configured returns the home
// a distribution's tool directories sit in, or "". Only a distribution
// named for starting fails the call when its home cannot be read.
func Homes(configured func(distribution string) string, reach Reach) ([]Home, []string, error) {
	if (reach.kind == reachNamed || reach.kind == reachRunningAmong) && len(reach.names) == 0 {
		return nil, nil, nil
	}
	return homes(distributions(), configured, reach, home)
}

func homes(all []Distribution, configured func(string) string, reach Reach, read func(string, string) (Home, error)) ([]Home, []string, error) {
	var found []Home
	var unchecked []string
	for _, d := range all {
		reached, start := reach.selects(d)
		if !reached {
			unchecked = append(unchecked, d.Name)
			continue
		}
		home, err := read(d.Name, configured(d.Name))
		switch {
		case err == nil:
			found = append(found, home)
		case start:
			return nil, nil, err
		default:
			fmt.Fprintf(os.Stderr, "unable to check WSL distribution %s: %v\n", d.Name, err)
			unchecked = append(unchecked, d.Name)
		}
	}
	return found, unchecked, nil
}

func home(distribution, share string) (Home, error) {
	if share == "" {
		var err error
		if share, err = defaultHome(distribution); err != nil {
			return Home{}, err
		}
	}
	return Home{Distribution: distribution, Share: share, MountRoot: MountRoot(distribution)}, nil
}

// ShareRoot is the distribution's root as Windows reaches it. Reaching it
// starts a stopped distribution.
func ShareRoot(distribution string) string { return `\\wsl$\` + distribution }

// MountRoot is where the distribution mounts Windows drives, read from its
// `/etc/wsl.conf`.
func MountRoot(distribution string) string {
	conf, _ := os.ReadFile(filepath.Join(ShareRoot(distribution), "etc", "wsl.conf"))
	return parseMountRoot(string(conf))
}

// Program returns wsl.exe, which runs commands in a distribution.
func Program() (string, error) { return program() }

// iniSections calls visit with each `key = value` line of an INI file, its
// section lowercased, until visit returns true.
func iniSections(conf string, visit func(section, key, value string) bool) bool {
	section := ""
	for _, line := range strings.Split(conf, "\n") {
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(line)
		if name, ok := strings.CutPrefix(line, "["); ok {
			if name, ok := strings.CutSuffix(name, "]"); ok {
				section = strings.ToLower(strings.TrimSpace(name))
				continue
			}
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && visit(section, strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"`)) {
			return true
		}
	}
	return false
}

// parseMountRoot returns the drive mount root `[automount] root` sets in
// `/etc/wsl.conf`, `/mnt` when it is unset.
func parseMountRoot(conf string) string {
	root := "/mnt"
	iniSections(conf, func(section, key, value string) bool {
		if section == "automount" && strings.EqualFold(key, "root") && strings.HasPrefix(value, "/") {
			root = strings.TrimRight(value, "/")
			return true
		}
		return false
	})
	return root
}

// LocalhostShared reports whether clients in WSL reach the desktop's
// `localhost`: WSL 2 shares it only with mirrored networking, set in
// `%UserProfile%\.wslconfig`.
func LocalhostShared(host string) bool {
	conf, _ := os.ReadFile(filepath.Join(host, ".wslconfig"))
	return iniSections(string(conf), func(section, key, value string) bool {
		// Early WSL 2 releases read the setting from [experimental].
		return (section == "wsl2" || section == "experimental") &&
			strings.EqualFold(key, "networkingMode") && strings.EqualFold(value, "mirrored")
	})
}

// decode reads wsl.exe's output: its own in UTF-16 unless it honours
// `WSL_UTF8`, the Linux commands it runs in UTF-8.
func decode(data []byte) string {
	var text string
	if strings.IndexByte(string(data), 0) >= 0 {
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
		}
		text = string(utf16.Decode(units))
	} else {
		text = strings.ToValidUTF8(string(data), string(utf8.RuneError))
	}
	return strings.TrimLeft(text, "\ufeff")
}

// parseNames reads `wsl.exe --list --quiet`, skipping Docker Desktop's
// internal distributions and any name that cannot be a share path segment.
func parseNames(output string) []string {
	var names []string
	for _, line := range strings.Split(output, "\n") {
		name := strings.TrimFunc(line, func(c rune) bool { return unicode.IsSpace(c) || c == 0 })
		if name == "" || name == "." || name == ".." || strings.HasPrefix(name, "docker-desktop") ||
			strings.IndexFunc(name, func(c rune) bool { return unicode.IsControl(c) || strings.ContainsRune(`\/:*?"<>|`, c) }) >= 0 {
			continue
		}
		names = append(names, name)
	}
	return names
}

// sharePath is a home the distribution reports, through its share.
func sharePath(distribution, home string) (string, bool) {
	home = strings.TrimRight(strings.TrimSpace(home), "/")
	if !strings.HasPrefix(home, "/") {
		return "", false
	}
	share := ShareRoot(distribution)
	for _, part := range strings.Split(home, "/") {
		if part == ".." {
			return "", false
		}
		if part != "" {
			share += `\` + part
		}
	}
	return share, true
}
