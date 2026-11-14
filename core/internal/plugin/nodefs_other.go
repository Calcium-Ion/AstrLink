//go:build !linux && !darwin

package plugin

import "io/fs"

// statValues are the fields of fs.Stats in Node's order: dev, mode, nlink,
// uid, gid, rdev, blksize, ino, size, blocks, then the access, modify,
// change and birth times in milliseconds.
func statValues(info fs.FileInfo) []any { return genericStatValues(info) }
