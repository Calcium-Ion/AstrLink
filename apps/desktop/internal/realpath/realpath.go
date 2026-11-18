// Package realpath resolves the links in a path, including the directory
// junctions Windows installers create.
package realpath

import "path/filepath"

// Resolve is path with every link resolved. On Windows the file system
// resolves it, through symbolic links and directory junctions alike, as
// Rust's canonicalize does; elsewhere it is filepath.EvalSymlinks. The
// path must exist.
//
// Since Go 1.23 filepath.EvalSymlinks does not treat a junction as a link
// on Windows: it fails on a path through two of them, as the standalone
// Codex installer puts on PATH, and returns a path ending in one as is.
func Resolve(path string) (string, error) {
	if final, ok := finalPath(path); ok {
		return final, nil
	}
	return filepath.EvalSymlinks(path)
}
