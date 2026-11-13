package clientupdates

import (
	"log"
	"path/filepath"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/wsl"
)

// Clients in WSL distributions, checked from Windows. Their paths stay
// Linux paths. Commands, file checks, and link resolution run in the
// distribution through wsl.exe, because Windows cannot follow Linux links
// through the \\wsl$ share. Manifests and settings files are read through
// the share. Manifests lie under resolved paths; a settings file that is
// itself a link reads as missing, and its default applies.

// wsl.exe receives these scripts through a Windows command line. They
// contain no double quotes, so its unquoting cannot change them. An empty
// IFS and `set -f` keep unquoted paths whole.
const (
	// wslProbe prints the user's home and PATH. The login shell's PATH
	// comes last; an interactive shell also sets up version managers such
	// as nvm.
	wslProbe = "echo __ASTRLINK_PATH__; printenv PATH; " +
		"${SHELL:-/bin/sh} -ilc 'echo __ASTRLINK_PATH__; printenv PATH' </dev/null; " +
		"echo __ASTRLINK_HOME__; printenv HOME"
	// wslLookup prints each executable file among the arguments, a tab,
	// and the file it resolves to.
	wslLookup     = `IFS=; set -f; for f do if [ -f $f ] && [ -x $f ]; then printf '%s\t%s\n' $f $(readlink -f $f); fi; done`
	wslExecutable = "IFS=; set -f; [ -f $1 ] && [ -x $1 ]"
	wslFile       = "IFS=; set -f; [ -f $1 ]"
)

// wslTarget is a distribution and how this computer reaches it.
type wslTarget struct {
	distribution string
	// launcher is wsl.exe, which runs commands in the distribution.
	launcher string
	// share is the distribution's root as Windows reads files in it.
	share string
	// mountRoot is where the distribution mounts Windows drives: /mnt by
	// default.
	mountRoot string
}

// newWSL reaches a registered distribution through wsl.exe and its share.
func newWSL(distribution string) (*wslTarget, *clientError) {
	launcher, err := wsl.Program()
	if err != nil {
		return nil, newError("command", err.Error())
	}
	return &wslTarget{
		distribution: distribution,
		launcher:     launcher,
		share:        wsl.ShareRoot(distribution),
		mountRoot:    wsl.MountRoot(distribution),
	}, nil
}

// shared is a Linux path as Windows reads it through the share.
func (w *wslTarget) shared(path string) (string, bool) {
	if filepath.VolumeName(path) != "" {
		return "", false
	}
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		return "", false
	}
	shared := []string{w.share}
	for _, part := range strings.Split(slashed, "/") {
		switch part {
		case "", ".":
		case "..":
			return "", false
		default:
			shared = append(shared, part)
		}
	}
	return filepath.Join(shared...), true
}

// linux is how Linux spells a path in the distribution. On Windows,
// joining one adds backslashes.
func linux(path string) string {
	path = path[len(filepath.VolumeName(path)):]
	slashed := filepath.ToSlash(path)
	var parts []string
	for _, part := range strings.Split(slashed, "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	joined := strings.Join(parts, "/")
	if strings.HasPrefix(slashed, "/") {
		return "/" + joined
	}
	return joined
}

// wslCommand runs spec in the distribution, from the user's home, with
// the environment changes a local command gets.
func wslCommand(env *Environment, w *wslTarget, spec commandSpec) commandSpec {
	args := []string{"--distribution", w.distribution, "--cd", "~", "--exec", "env"}
	removed, set := changes(env)
	for _, name := range removed {
		args = append(args, "-u", name)
	}
	if env.path != "" {
		args = append(args, "PATH="+env.path)
	}
	for _, variable := range set {
		args = append(args, variable.name+"="+variable.value)
	}
	args = append(args, linux(spec.program))
	args = append(args, spec.args...)
	return commandSpec{program: w.launcher, args: args}
}

// wslEnvironment is the distribution's environment for client checks: its
// user's home, and the login shell's PATH with the usual install
// directories added.
func wslEnvironment(w *wslTarget, useSystemProxy bool) (*Environment, *clientError) {
	env := &Environment{useSystemProxy: useSystemProxy, wsl: w}
	output, failure := run(env, command("/bin/sh", "-c", wslProbe), startTimeout)
	if failure != nil {
		return nil, failure
	}
	home, path, ok := parseProbe(output)
	if !ok {
		return nil, newError("command", "WSL did not report the user's home and PATH\n"+output)
	}
	env.home = home
	directories := strings.Split(path, ":")
	for _, directory := range usualDirectories(home) {
		if directory = linux(directory); !slices.Contains(directories, directory) {
			directories = append(directories, directory)
		}
	}
	env.path = strings.Join(directories, ":")
	return env, nil
}

// parseProbe finds the home and PATH in the probe's output. Shell start-up
// files may print around them; the last PATH wins.
func parseProbe(output string) (home, path string, ok bool) {
	all := lines(output)
	for i := range all {
		all[i] = strings.TrimSpace(all[i])
	}
	value := func(marker string) (string, bool) {
		for i := len(all) - 2; i >= 0; i-- {
			if all[i] == marker && strings.HasPrefix(all[i+1], "/") {
				return all[i+1], true
			}
		}
		return "", false
	}
	if home, ok = value("__ASTRLINK_HOME__"); !ok {
		return "", "", false
	}
	if path, ok = value("__ASTRLINK_PATH__"); !ok {
		return "", "", false
	}
	return home, path, true
}

// wslPrograms are the programs named name on PATH, apart from those on
// Windows drives. WSL appends the Windows PATH, and this computer's own
// check covers it.
func wslPrograms(env *Environment, w *wslTarget, name string) []program {
	return lookup(env, w, name, candidates(env.path, w.mountRoot, name))
}

// wslProgram is path and the file it resolves to, if it is an executable
// file.
func wslProgram(env *Environment, w *wslTarget, path string) (program, bool) {
	path = linux(path)
	found := lookup(env, w, path, []string{path})
	if len(found) == 0 {
		return program{}, false
	}
	return found[0], true
}

// lookup lists the executable files among candidates, in order, each
// target once.
func lookup(env *Environment, w *wslTarget, name string, candidates []string) []program {
	if len(candidates) == 0 {
		return nil
	}
	output, failure := run(env, command("/bin/sh", append([]string{"-c", wslLookup, "sh"}, candidates...)...), probeTimeout)
	if failure != nil {
		log.Printf("astrlink: unable to look up %s in WSL %s: %s", name, w.distribution, failure.detail)
		return nil
	}
	var found []program
	seen := map[string]bool{}
	for _, line := range lines(output) {
		path, resolved, ok := strings.Cut(line, "\t")
		if !ok || !slices.Contains(candidates, path) || !strings.HasPrefix(resolved, "/") || seen[resolved] {
			continue
		}
		seen[resolved] = true
		found = append(found, program{path: path, resolved: resolved})
	}
	return found
}

// candidates are the files named name in a Linux PATH, in order, apart
// from directories on Windows drives.
func candidates(path, mountRoot, name string) []string {
	var files []string
	seen := map[string]bool{}
	for _, directory := range strings.Split(path, ":") {
		if !strings.HasPrefix(directory, "/") || onWindowsDrive(directory, mountRoot) {
			continue
		}
		file := strings.TrimRight(directory, "/") + "/" + name
		if !seen[file] {
			seen[file] = true
			files = append(files, file)
		}
	}
	return files
}

// onWindowsDrive reports whether a Linux directory lies on a Windows drive
// the distribution mounts.
func onWindowsDrive(directory, mountRoot string) bool {
	rest, ok := strings.CutPrefix(directory, mountRoot)
	if !ok {
		return false
	}
	if rest, ok = strings.CutPrefix(rest, "/"); !ok || rest == "" {
		return false
	}
	c := rest[0]
	return (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') && (len(rest) == 1 || rest[1] == '/')
}

// wslIsFile reports whether path in the distribution is a file, and with
// executable set, one the user may run. Links resolve in the distribution.
func wslIsFile(env *Environment, path string, executable bool) bool {
	test := wslFile
	if executable {
		test = wslExecutable
	}
	_, failure := run(env, command("/bin/sh", "-c", test, "sh", linux(path)), probeTimeout)
	return failure == nil
}
