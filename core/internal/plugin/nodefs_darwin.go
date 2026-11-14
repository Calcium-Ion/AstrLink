package plugin

import (
	"io/fs"
	"syscall"
)

// statValues are the fields of fs.Stats in Node's order: dev, mode, nlink,
// uid, gid, rdev, blksize, ino, size, blocks, then the access, modify,
// change and birth times in milliseconds.
func statValues(info fs.FileInfo) []any {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return genericStatValues(info)
	}
	timespec := func(spec syscall.Timespec) float64 { return float64(spec.Sec)*1e3 + float64(spec.Nsec)/1e6 }
	return []any{stat.Dev, stat.Mode, stat.Nlink, stat.Uid, stat.Gid, stat.Rdev, stat.Blksize, stat.Ino, stat.Size, stat.Blocks,
		timespec(stat.Atimespec), timespec(stat.Mtimespec), timespec(stat.Ctimespec), timespec(stat.Birthtimespec)}
}
