package clientupdates

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/realpath"
)

// Finding running processes started from a client's installation, to name
// them when an update cannot move the client's files.

// refuseRunning fails with `running` when a process from roots is running.
func refuseRunning(roots []string) *clientError {
	found := findRunning(roots)
	if len(found) == 0 {
		return nil
	}
	lines := make([]string, len(found))
	for i, process := range found {
		lines[i] = fmt.Sprintf("%d %s", process.pid, process.path)
	}
	return newError("running", strings.Join(lines, "\n"))
}

type runningProcess struct {
	pid  uint32
	path string
}

// pathRoots are paths compared as Windows compares them:
// case-insensitively, with either separator, both as given and with links
// and short names resolved.
type pathRoots []string

func newPathRoots(roots []string) pathRoots {
	var keys []string
	for _, root := range roots {
		keys = append(keys, windowsKey(root))
		if resolved, err := realpath.Resolve(root); err == nil {
			if absolute, err := filepath.Abs(resolved); err == nil {
				keys = append(keys, windowsKey(absolute))
			}
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// contains reports whether path is one of the roots or lies below one.
func (r pathRoots) contains(path string) bool {
	key := windowsKey(path)
	for _, root := range r {
		if key == root {
			return true
		}
		if rest, ok := strings.CutPrefix(key, root); ok && strings.HasPrefix(rest, `\`) {
			return true
		}
	}
	return false
}

func windowsKey(path string) string {
	path = strings.TrimPrefix(path, `\\?\`)
	return strings.TrimRight(strings.ToLower(strings.ReplaceAll(path, "/", `\`)), `\`)
}
