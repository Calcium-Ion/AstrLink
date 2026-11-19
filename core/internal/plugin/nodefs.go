package plugin

import (
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
)

// checkCounter numbers the temporary directories of checks.
var checkCounter atomic.Int64

// maxOpenFiles bounds the files one runtime has open at once.
const maxOpenFiles = 256

// nodeFile is a file the plugin opened through node:fs.
type nodeFile struct {
	file     *os.File
	path     string
	writable bool
}

// Types of directory entries, as libuv numbers them.
const (
	direntUnknown = iota
	direntFile
	direntDir
	direntLink
	direntFIFO
	direntSocket
	direntChar
	direntBlock
)

// direntType is the libuv type of a file mode.
func direntType(mode fs.FileMode) int {
	switch {
	case mode.IsRegular():
		return direntFile
	case mode.IsDir():
		return direntDir
	case mode&fs.ModeSymlink != 0:
		return direntLink
	case mode&fs.ModeNamedPipe != 0:
		return direntFIFO
	case mode&fs.ModeSocket != 0:
		return direntSocket
	case mode&fs.ModeCharDevice != 0:
		return direntChar
	case mode&fs.ModeDevice != 0:
		return direntBlock
	}
	return direntUnknown
}

// modeBits is st_mode for a file mode: its type and permission bits.
func modeBits(mode fs.FileMode) int64 {
	bits := int64(mode.Perm())
	switch direntType(mode) {
	case direntFile:
		bits |= 0o100000
	case direntDir:
		bits |= 0o040000
	case direntLink:
		bits |= 0o120000
	case direntFIFO:
		bits |= 0o010000
	case direntSocket:
		bits |= 0o140000
	case direntChar:
		bits |= 0o020000
	case direntBlock:
		bits |= 0o060000
	}
	if mode&fs.ModeSetuid != 0 {
		bits |= 0o4000
	}
	if mode&fs.ModeSetgid != 0 {
		bits |= 0o2000
	}
	if mode&fs.ModeSticky != 0 {
		bits |= 0o1000
	}
	return bits
}

// milliseconds is t as Unix milliseconds.
func milliseconds(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e6
}

// fsError maps what a file system operation failed with to its code of
// Node and the explanation the message adds.
func fsError(err error) (code, detail string) {
	switch {
	case errors.Is(err, errOutside):
		return "EACCES", errOutside.Error()
	case errors.Is(err, errReadOnly):
		return "EROFS", errReadOnly.Error()
	case errors.Is(err, errQuota):
		return "ENOSPC", "the plugin's data directory is full"
	case strings.Contains(err.Error(), "path escapes from parent"):
		// os.Root refuses a symbolic link out of the directory.
		return "EACCES", errOutside.Error()
	}
	if code := platformErrno(err); code != "" {
		return code, ""
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "ENOENT", ""
	case errors.Is(err, fs.ErrExist):
		return "EEXIST", ""
	case errors.Is(err, fs.ErrPermission):
		return "EACCES", ""
	case errors.Is(err, fs.ErrClosed):
		return "EBADF", ""
	}
	return "EIO", err.Error()
}

// fsFunctions are the host's side of node:fs. Each runs at once and returns
// its result or throws the error of Node; the callback and promise forms
// are node/fs.js's.
func (bound *instance) fsFunctions() map[string]any {
	dirs := bound.program.dirs
	fail := func(realm *moejs.Realm, err error, syscall, path, dest string) error {
		code, detail := fsError(err)
		return nodeError(realm, code, syscall, path, dest, detail)
	}
	pathArg := func(realm *moejs.Realm, args []moejs.Value, index int) (string, error) {
		return stringArg(realm, args, index, "path")
	}
	number := func(realm *moejs.Realm, args []moejs.Value, index int) (float64, error) {
		value := moejs.Arg(args, index)
		if value.IsUndefined() || value.IsNull() {
			return -1, nil
		}
		return realm.ToNumber(value)
	}
	// open resolves name for an operation that writes when write is set:
	// a path of the install directory is then errReadOnly. The caller
	// closes what it returns.
	open := func(name string, write bool) (fsLocation, error) {
		at, err := dirs.resolve(name)
		if err == nil && write && !at.writable {
			err = errReadOnly
		}
		return at, err
	}
	file := func(realm *moejs.Realm, args []moejs.Value, syscall string) (*nodeFile, error) {
		fd, err := number(realm, args, 0)
		if err != nil {
			return nil, err
		}
		found := bound.resources.files[int(fd)]
		if found == nil {
			return nil, nodeError(realm, "EBADF", syscall, "", "", "")
		}
		return found, nil
	}
	timeArg := func(realm *moejs.Realm, args []moejs.Value, index int) (time.Time, error) {
		seconds, err := number(realm, args, index)
		if err != nil {
			return time.Time{}, err
		}
		whole, fraction := math.Modf(seconds)
		return time.Unix(int64(whole), int64(fraction*1e9)), nil
	}
	stats := func(realm *moejs.Realm, info fs.FileInfo) (moejs.Value, error) {
		return realm.FromGo(statValues(info))
	}
	var functions map[string]any
	functions = map[string]any{
		"constants": openConstants(),
		"stat": bound.function("stat", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			follow := !engine.ToBoolean(moejs.Arg(args, 1))
			syscall := "stat"
			if !follow {
				syscall = "lstat"
			}
			at, err := dirs.resolve(name)
			defer at.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, syscall, name, "")
			}
			var info fs.FileInfo
			bound.blocking(func() {
				if follow {
					info, err = at.root.Stat(at.rel)
				} else {
					info, err = at.root.Lstat(at.rel)
				}
			})
			if err != nil {
				return moejs.Undefined(), fail(realm, err, syscall, name, "")
			}
			return stats(realm, info)
		}),
		"exists": bound.function("exists", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Bool(false), nil
			}
			at, err := dirs.resolve(name)
			defer at.close()
			if err != nil {
				return moejs.Bool(false), nil
			}
			_, err = at.root.Stat(at.rel)
			return moejs.Bool(err == nil), nil
		}),
		"access": bound.function("access", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			mode, err := number(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			at, err := dirs.resolve(name)
			defer at.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "access", name, "")
			}
			info, err := at.root.Stat(at.rel)
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "access", name, "")
			}
			want := int(mode)
			if want < 0 {
				want = 0
			}
			perm := info.Mode().Perm()
			switch {
			case want&2 != 0 && (!at.writable || perm&0o200 == 0):
				return moejs.Undefined(), nodeError(realm, "EACCES", "access", name, "", "")
			case want&4 != 0 && perm&0o400 == 0:
				return moejs.Undefined(), nodeError(realm, "EACCES", "access", name, "", "")
			case want&1 != 0 && !executable(info):
				return moejs.Undefined(), nodeError(realm, "EACCES", "access", name, "", "")
			}
			return moejs.Undefined(), nil
		}),
		"readFile": bound.function("readFile", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			at, err := dirs.resolve(name)
			defer at.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "open", name, "")
			}
			var data []byte
			bound.blocking(func() {
				var info fs.FileInfo
				if info, err = at.root.Stat(at.rel); err == nil && info.IsDir() {
					err = errIsDir
					return
				}
				data, err = at.root.ReadFile(at.rel)
			})
			if errors.Is(err, errIsDir) {
				return moejs.Undefined(), nodeError(realm, "EISDIR", "read", "", "", "")
			}
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "open", name, "")
			}
			return bytesValue(realm, data)
		}),
		// reserve counts a write of n bytes that moejs's node:fs is about to
		// make in the data directory against the quota.
		"reserve": bound.function("reserve", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			size, err := number(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			name, err := pathArg(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			if err := dirs.reserve(int64(size)); err != nil {
				return moejs.Undefined(), fail(realm, err, "open", name, "")
			}
			return moejs.Undefined(), nil
		}),
		"writeFile": bound.function("writeFile", 4, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			data, ok := bytesOf(moejs.Arg(args, 1))
			if !ok {
				return moejs.Undefined(), realm.TypeError("the data must be bytes")
			}
			flags, err := number(realm, args, 2)
			if err != nil {
				return moejs.Undefined(), err
			}
			mode, err := number(realm, args, 3)
			if err != nil {
				return moejs.Undefined(), err
			}
			if mode < 0 {
				mode = 0o666
			}
			at, err := open(name, true)
			defer at.close()
			if err == nil {
				err = dirs.reserve(int64(len(data)))
			}
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "open", name, "")
			}
			bound.blocking(func() {
				var file *os.File
				if file, err = at.root.OpenFile(at.rel, int(flags), fs.FileMode(mode)&fs.ModePerm); err != nil {
					return
				}
				_, err = file.Write(data)
				if closed := file.Close(); err == nil {
					err = closed
				}
			})
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "open", name, "")
			}
			return moejs.Undefined(), nil
		}),
		"open": bound.function("open", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			flags, err := number(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			mode, err := number(realm, args, 2)
			if err != nil {
				return moejs.Undefined(), err
			}
			if mode < 0 {
				mode = 0o666
			}
			if len(bound.resources.files) >= maxOpenFiles {
				return moejs.Undefined(), nodeError(realm, "EMFILE", "open", name, "", "")
			}
			write := int(flags)&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
			at, err := open(name, write)
			defer at.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "open", name, "")
			}
			var opened *os.File
			bound.blocking(func() {
				opened, err = at.root.OpenFile(at.rel, int(flags), fs.FileMode(mode)&fs.ModePerm)
			})
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "open", name, "")
			}
			if bound.resources.files == nil {
				bound.resources.files = make(map[int]*nodeFile)
				bound.resources.nextFD = 20
			}
			fd := bound.resources.nextFD
			bound.resources.nextFD++
			bound.resources.files[fd] = &nodeFile{file: opened, path: at.abs, writable: write}
			return moejs.Int(int64(fd)), nil
		}),
		"close": bound.function("close", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, err := file(realm, args, "close")
			if err != nil {
				return moejs.Undefined(), err
			}
			fd, _ := number(realm, args, 0)
			delete(bound.resources.files, int(fd))
			if err := found.file.Close(); err != nil {
				return moejs.Undefined(), fail(realm, err, "close", "", "")
			}
			return moejs.Undefined(), nil
		}),
		"read": bound.function("read", 5, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, err := file(realm, args, "read")
			if err != nil {
				return moejs.Undefined(), err
			}
			target := moejs.Arg(args, 1)
			if !target.IsObject() {
				return moejs.Undefined(), realm.TypeError("the buffer must be a typed array")
			}
			buffer, ok := target.AsObject().BufferData()
			if !ok {
				return moejs.Undefined(), realm.TypeError("the buffer must be a typed array")
			}
			offset, err := number(realm, args, 2)
			if err != nil {
				return moejs.Undefined(), err
			}
			length, err := number(realm, args, 3)
			if err != nil {
				return moejs.Undefined(), err
			}
			position, err := number(realm, args, 4)
			if err != nil {
				return moejs.Undefined(), err
			}
			if offset < 0 || length < 0 || offset+length > float64(len(buffer)) {
				return moejs.Undefined(), realm.RangeError("the offset and length are outside the buffer")
			}
			window := buffer[int(offset) : int(offset)+int(length)]
			read := 0
			bound.blocking(func() {
				for read < len(window) {
					var n int
					if position >= 0 {
						n, err = found.file.ReadAt(window[read:], int64(position)+int64(read))
					} else {
						n, err = found.file.Read(window[read:])
					}
					read += n
					if err != nil || n == 0 {
						break
					}
				}
			})
			if err != nil && !errors.Is(err, io.EOF) {
				if code, _ := fsError(err); code == "EIO" && strings.Contains(err.Error(), "is a directory") {
					return moejs.Undefined(), nodeError(realm, "EISDIR", "read", "", "", "")
				}
				return moejs.Undefined(), fail(realm, err, "read", "", "")
			}
			return moejs.Int(int64(read)), nil
		}),
		"write": bound.function("write", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, err := file(realm, args, "write")
			if err != nil {
				return moejs.Undefined(), err
			}
			data, ok := bytesOf(moejs.Arg(args, 1))
			if !ok {
				return moejs.Undefined(), realm.TypeError("the data must be bytes")
			}
			position, err := number(realm, args, 2)
			if err != nil {
				return moejs.Undefined(), err
			}
			if !found.writable {
				return moejs.Undefined(), nodeError(realm, "EBADF", "write", "", "", "")
			}
			if err := dirs.reserve(int64(len(data))); err != nil {
				return moejs.Undefined(), fail(realm, err, "write", "", "")
			}
			written := 0
			bound.blocking(func() {
				if position >= 0 {
					written, err = found.file.WriteAt(data, int64(position))
				} else {
					written, err = found.file.Write(data)
				}
			})
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "write", "", "")
			}
			return moejs.Int(int64(written)), nil
		}),
		"fstat": bound.function("fstat", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, err := file(realm, args, "fstat")
			if err != nil {
				return moejs.Undefined(), err
			}
			info, err := found.file.Stat()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "fstat", "", "")
			}
			return stats(realm, info)
		}),
		"ftruncate": bound.function("ftruncate", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, err := file(realm, args, "ftruncate")
			if err != nil {
				return moejs.Undefined(), err
			}
			length, err := number(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			if length < 0 {
				length = 0
			}
			if !found.writable {
				return moejs.Undefined(), nodeError(realm, "EINVAL", "ftruncate", "", "", "")
			}
			if info, err := found.file.Stat(); err == nil && int64(length) > info.Size() {
				if err := dirs.reserve(int64(length) - info.Size()); err != nil {
					return moejs.Undefined(), fail(realm, err, "ftruncate", "", "")
				}
			} else {
				dirs.released()
			}
			if err := found.file.Truncate(int64(length)); err != nil {
				return moejs.Undefined(), fail(realm, err, "ftruncate", "", "")
			}
			return moejs.Undefined(), nil
		}),
		"fsync": bound.function("fsync", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, err := file(realm, args, "fsync")
			if err != nil {
				return moejs.Undefined(), err
			}
			bound.blocking(func() { err = found.file.Sync() })
			if err != nil && found.writable {
				return moejs.Undefined(), fail(realm, err, "fsync", "", "")
			}
			return moejs.Undefined(), nil
		}),
		"fchmod": bound.function("fchmod", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, err := file(realm, args, "fchmod")
			if err != nil {
				return moejs.Undefined(), err
			}
			mode, err := number(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			if !found.writable {
				return moejs.Undefined(), nodeError(realm, "EROFS", "fchmod", "", "", errReadOnly.Error())
			}
			if err := found.file.Chmod(fs.FileMode(int(mode)) & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)); err != nil {
				return moejs.Undefined(), fail(realm, err, "fchmod", "", "")
			}
			return moejs.Undefined(), nil
		}),
		"futimes": bound.function("futimes", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, err := file(realm, args, "futime")
			if err != nil {
				return moejs.Undefined(), err
			}
			atime, err := timeArg(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			mtime, err := timeArg(realm, args, 2)
			if err != nil {
				return moejs.Undefined(), err
			}
			at, err := open(found.path, true)
			defer at.close()
			if err == nil {
				err = at.root.Chtimes(at.rel, atime, mtime)
			}
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "futime", "", "")
			}
			return moejs.Undefined(), nil
		}),
		"readdir": bound.function("readdir", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			withTypes := engine.ToBoolean(moejs.Arg(args, 1))
			recursive := engine.ToBoolean(moejs.Arg(args, 2))
			at, err := dirs.resolve(name)
			defer at.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "scandir", name, "")
			}
			var entries []any
			bound.blocking(func() {
				entries, err = readDirectory(at, recursive, withTypes)
			})
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "scandir", name, "")
			}
			return realm.FromGo(entries)
		}),
		"mkdir": bound.function("mkdir", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			recursive := engine.ToBoolean(moejs.Arg(args, 1))
			mode, err := number(realm, args, 2)
			if err != nil {
				return moejs.Undefined(), err
			}
			if mode < 0 {
				mode = 0o777
			}
			at, err := open(name, true)
			defer at.close()
			if err != nil {
				if errors.Is(err, errReadOnly) && recursive {
					if info, statErr := at.root.Stat(at.rel); statErr == nil && info.IsDir() {
						return moejs.Undefined(), nil
					}
				}
				return moejs.Undefined(), fail(realm, err, "mkdir", name, "")
			}
			perm := fs.FileMode(mode) & fs.ModePerm
			if !recursive {
				if err := at.root.Mkdir(at.rel, perm); err != nil {
					return moejs.Undefined(), fail(realm, err, "mkdir", name, "")
				}
				return moejs.Undefined(), nil
			}
			// The first directory made, which mkdir returns, is the
			// highest one missing.
			first := ""
			for rel := at.rel; rel != "." && rel != ""; rel = filepath.Dir(rel) {
				info, err := at.root.Stat(rel)
				if err == nil {
					if !info.IsDir() {
						return moejs.Undefined(), nodeError(realm, "ENOTDIR", "mkdir", name, "", "")
					}
					break
				}
				first = rel
			}
			if err := at.root.MkdirAll(at.rel, perm); err != nil {
				return moejs.Undefined(), fail(realm, err, "mkdir", name, "")
			}
			if first == "" {
				return moejs.Undefined(), nil
			}
			return moejs.String(filepath.Join(dirs.data, first)), nil
		}),
		"mkdtemp": bound.function("mkdtemp", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			prefix, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
			for range 100 {
				var suffix [6]byte
				_, _ = rand.Read(suffix[:])
				for index := range suffix {
					suffix[index] = letters[int(suffix[index])%len(letters)]
				}
				name := prefix + string(suffix[:])
				at, err := open(name, true)
				defer at.close()
				if err != nil {
					return moejs.Undefined(), fail(realm, err, "mkdtemp", prefix+"XXXXXX", "")
				}
				err = at.root.Mkdir(at.rel, 0o700)
				if errors.Is(err, fs.ErrExist) {
					continue
				}
				if err != nil {
					return moejs.Undefined(), fail(realm, err, "mkdtemp", prefix+"XXXXXX", "")
				}
				return moejs.String(at.abs), nil
			}
			return moejs.Undefined(), nodeError(realm, "EEXIST", "mkdtemp", prefix+"XXXXXX", "", "")
		}),
		"rm": bound.function("rm", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			recursive := engine.ToBoolean(moejs.Arg(args, 1))
			force := engine.ToBoolean(moejs.Arg(args, 2))
			at, err := open(name, true)
			defer at.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "rm", name, "")
			}
			info, err := at.root.Lstat(at.rel)
			if err != nil {
				if force && errors.Is(err, fs.ErrNotExist) {
					return moejs.Undefined(), nil
				}
				return moejs.Undefined(), fail(realm, err, "lstat", name, "")
			}
			if at.rel == "." {
				return moejs.Undefined(), nodeError(realm, "EPERM", "rm", name, "", "a plugin cannot remove its data directory itself")
			}
			if info.IsDir() && !recursive {
				return moejs.Undefined(), nodeError(realm, "EISDIR", "rm", name, "", "")
			}
			bound.blocking(func() {
				if info.IsDir() {
					err = at.root.RemoveAll(at.rel)
				} else {
					err = at.root.Remove(at.rel)
				}
			})
			dirs.released()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "rm", name, "")
			}
			return moejs.Undefined(), nil
		}),
		"rmdir": bound.function("rmdir", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			recursive := engine.ToBoolean(moejs.Arg(args, 1))
			at, err := open(name, true)
			defer at.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "rmdir", name, "")
			}
			info, err := at.root.Lstat(at.rel)
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "rmdir", name, "")
			}
			if !info.IsDir() {
				return moejs.Undefined(), nodeError(realm, "ENOTDIR", "rmdir", name, "", "")
			}
			if at.rel == "." {
				return moejs.Undefined(), nodeError(realm, "EPERM", "rmdir", name, "", "a plugin cannot remove its data directory itself")
			}
			bound.blocking(func() {
				if recursive {
					err = at.root.RemoveAll(at.rel)
				} else {
					err = at.root.Remove(at.rel)
				}
			})
			dirs.released()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "rmdir", name, "")
			}
			return moejs.Undefined(), nil
		}),
		"unlink": bound.function("unlink", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			at, err := open(name, true)
			defer at.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "unlink", name, "")
			}
			info, err := at.root.Lstat(at.rel)
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "unlink", name, "")
			}
			if info.IsDir() {
				return moejs.Undefined(), nodeError(realm, "EISDIR", "unlink", name, "", "")
			}
			if err := at.root.Remove(at.rel); err != nil {
				return moejs.Undefined(), fail(realm, err, "unlink", name, "")
			}
			dirs.released()
			return moejs.Undefined(), nil
		}),
		"rename": bound.function("rename", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			from, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			to, err := pathArg(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			source, err := open(from, true)
			defer source.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "rename", from, to)
			}
			target, err := open(to, true)
			defer target.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "rename", from, to)
			}
			if target.rel == "." || source.rel == "." {
				return moejs.Undefined(), nodeError(realm, "EPERM", "rename", from, to, "")
			}
			if err := source.root.Rename(source.rel, target.rel); err != nil {
				return moejs.Undefined(), fail(realm, err, "rename", from, to)
			}
			return moejs.Undefined(), nil
		}),
		"copyFile": bound.function("copyFile", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			from, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			to, err := pathArg(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			mode, err := number(realm, args, 2)
			if err != nil {
				return moejs.Undefined(), err
			}
			source, err := dirs.resolve(from)
			defer source.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "copyfile", from, to)
			}
			target, err := open(to, true)
			defer target.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "copyfile", from, to)
			}
			exclusive := mode > 0 && int(mode)&1 != 0
			bound.blocking(func() { err = copyFile(dirs, source, target, exclusive) })
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "copyfile", from, to)
			}
			return moejs.Undefined(), nil
		}),
		"cp": bound.function("cp", 5, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			from, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			to, err := pathArg(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			recursive := engine.ToBoolean(moejs.Arg(args, 2))
			force := engine.ToBoolean(moejs.Arg(args, 3))
			errorOnExist := engine.ToBoolean(moejs.Arg(args, 4))
			source, err := dirs.resolve(from)
			defer source.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "cp", from, to)
			}
			target, err := open(to, true)
			defer target.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "cp", from, to)
			}
			info, err := source.root.Stat(source.rel)
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "cp", from, to)
			}
			if info.IsDir() && !recursive {
				return moejs.Undefined(), nodeError(realm, "EISDIR", "cp", from, to, "copy a directory with recursive: true")
			}
			bound.blocking(func() { err = copyTree(dirs, source, target, force, errorOnExist) })
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "cp", from, to)
			}
			return moejs.Undefined(), nil
		}),
		"link": bound.function("link", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			from, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			to, err := pathArg(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			source, err := open(from, true)
			defer source.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "link", from, to)
			}
			target, err := open(to, true)
			defer target.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "link", from, to)
			}
			if err := source.root.Link(source.rel, target.rel); err != nil {
				return moejs.Undefined(), fail(realm, err, "link", from, to)
			}
			return moejs.Undefined(), nil
		}),
		"symlink": bound.function("symlink", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			targetText, err := stringArg(realm, args, 0, "target")
			if err != nil {
				return moejs.Undefined(), err
			}
			name, err := pathArg(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			at, err := open(name, true)
			defer at.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "symlink", targetText, name)
			}
			// A link may only point into the directory it is in, which
			// os.Root then follows.
			pointed := targetText
			if !filepath.IsAbs(pointed) {
				pointed = filepath.Join(filepath.Dir(at.abs), pointed)
			}
			if _, inside := within(dirs.data, filepath.Clean(pointed)); !inside || filepath.IsAbs(targetText) {
				return moejs.Undefined(), nodeError(realm, "EACCES", "symlink", targetText, name, "a plugin's symbolic links point into its data directory, by a relative path")
			}
			if err := at.root.Symlink(targetText, at.rel); err != nil {
				return moejs.Undefined(), fail(realm, err, "symlink", targetText, name)
			}
			return moejs.Undefined(), nil
		}),
		"readlink": bound.function("readlink", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			at, err := dirs.resolve(name)
			defer at.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "readlink", name, "")
			}
			target, err := at.root.Readlink(at.rel)
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "readlink", name, "")
			}
			return moejs.String(target), nil
		}),
		"realpath": bound.function("realpath", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			at, err := dirs.resolve(name)
			defer at.close()
			if err == nil {
				_, err = at.root.Stat(at.rel)
			}
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "realpath", name, "")
			}
			resolved, err := filepath.EvalSymlinks(at.abs)
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "realpath", name, "")
			}
			check, err := dirs.resolve(resolved)
			check.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "realpath", name, "")
			}
			return moejs.String(resolved), nil
		}),
		"truncate": bound.function("truncate", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			length, err := number(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			if length < 0 {
				length = 0
			}
			at, err := open(name, true)
			defer at.close()
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "open", name, "")
			}
			bound.blocking(func() {
				var file *os.File
				if file, err = at.root.OpenFile(at.rel, os.O_WRONLY, 0); err != nil {
					return
				}
				defer file.Close()
				if info, statErr := file.Stat(); statErr == nil && int64(length) > info.Size() {
					if err = dirs.reserve(int64(length) - info.Size()); err != nil {
						return
					}
				} else {
					dirs.released()
				}
				err = file.Truncate(int64(length))
			})
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "open", name, "")
			}
			return moejs.Undefined(), nil
		}),
		"utimes": bound.function("utimes", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			atime, err := timeArg(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			mtime, err := timeArg(realm, args, 2)
			if err != nil {
				return moejs.Undefined(), err
			}
			at, err := open(name, true)
			defer at.close()
			if err == nil {
				err = at.root.Chtimes(at.rel, atime, mtime)
			}
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "utime", name, "")
			}
			return moejs.Undefined(), nil
		}),
		"chmod": bound.function("chmod", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			mode, err := number(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			at, err := open(name, true)
			defer at.close()
			if err == nil {
				err = at.root.Chmod(at.rel, fs.FileMode(int(mode))&(fs.ModePerm|fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky))
			}
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "chmod", name, "")
			}
			return moejs.Undefined(), nil
		}),
		"chown": bound.function("chown", 4, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			uid, err := number(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			gid, err := number(realm, args, 2)
			if err != nil {
				return moejs.Undefined(), err
			}
			follow := !engine.ToBoolean(moejs.Arg(args, 3))
			at, err := open(name, true)
			defer at.close()
			if err == nil {
				if follow {
					err = at.root.Chown(at.rel, int(uid), int(gid))
				} else {
					err = at.root.Lchown(at.rel, int(uid), int(gid))
				}
			}
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "chown", name, "")
			}
			return moejs.Undefined(), nil
		}),
		"statfs": bound.function("statfs", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := pathArg(realm, args, 0)
			if err != nil {
				return moejs.Undefined(), err
			}
			at, err := dirs.resolve(name)
			defer at.close()
			if err == nil {
				_, err = at.root.Stat(at.rel)
			}
			if err != nil {
				return moejs.Undefined(), fail(realm, err, "statfs", name, "")
			}
			const block = 4096
			free := dirs.free()
			return realm.FromGo([]any{0, block, dirs.quota / block, free / block, free / block, 1 << 20, 1 << 20})
		}),
	}
	return functions
}

// errIsDir is the error of reading a directory as a file.
var errIsDir = errors.New("is a directory")

// readDirectory lists the directory at, as names relative to it, or as
// [name, type, parent path] with withTypes; recursive lists what is under
// its subdirectories too.
func readDirectory(at fsLocation, recursive, withTypes bool) ([]any, error) {
	var entries []any
	var walk func(rel string) error
	walk = func(rel string) error {
		directory, err := at.root.Open(rel)
		if err != nil {
			return err
		}
		listed, err := directory.ReadDir(-1)
		directory.Close()
		if err != nil {
			return err
		}
		parent := filepath.Join(filepath.Dir(at.abs), filepath.Base(at.abs))
		if rel != at.rel {
			inner, _ := filepath.Rel(at.rel, rel)
			parent = filepath.Join(at.abs, inner)
		}
		for _, entry := range listed {
			name := entry.Name()
			if rel != at.rel {
				inner, _ := filepath.Rel(at.rel, filepath.Join(rel, name))
				name = inner
			}
			if withTypes {
				entries = append(entries, []any{entry.Name(), direntType(entry.Type()), parent})
			} else {
				entries = append(entries, name)
			}
			if recursive && entry.IsDir() {
				if err := walk(filepath.Join(rel, entry.Name())); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(at.rel); err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []any{}
	}
	return entries, nil
}

// copyFile copies the file source to target, which must not exist when
// exclusive is set.
func copyFile(dirs *pluginDirs, source, target fsLocation, exclusive bool) error {
	input, err := source.root.Open(source.rel)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return &fs.PathError{Op: "copyfile", Path: source.abs, Err: errIsDir}
	}
	if err := dirs.reserve(info.Size()); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if exclusive {
		flags |= os.O_EXCL
	}
	output, err := target.root.OpenFile(target.rel, flags, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

// copyTree copies source, a file or a directory with what is under it, to
// target. An existing file is replaced when force is set, else skipped, or
// an error with errorOnExist.
func copyTree(dirs *pluginDirs, source, target fsLocation, force, errorOnExist bool) error {
	info, err := source.root.Lstat(source.rel)
	if err != nil {
		return err
	}
	switch {
	case info.IsDir():
		if err := target.root.MkdirAll(target.rel, info.Mode().Perm()|0o700); err != nil {
			return err
		}
		directory, err := source.root.Open(source.rel)
		if err != nil {
			return err
		}
		entries, err := directory.ReadDir(-1)
		directory.Close()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			from := fsLocation{root: source.root, rel: filepath.Join(source.rel, entry.Name()), abs: filepath.Join(source.abs, entry.Name())}
			to := fsLocation{root: target.root, rel: filepath.Join(target.rel, entry.Name()), abs: filepath.Join(target.abs, entry.Name()), writable: true}
			if err := copyTree(dirs, from, to, force, errorOnExist); err != nil {
				return err
			}
		}
		return nil
	case info.Mode()&fs.ModeSymlink != 0:
		link, err := source.root.Readlink(source.rel)
		if err != nil {
			return err
		}
		if filepath.IsAbs(link) {
			return &fs.PathError{Op: "cp", Path: source.abs, Err: errOutside}
		}
		return target.root.Symlink(link, target.rel)
	}
	if _, err := target.root.Lstat(target.rel); err == nil {
		switch {
		case errorOnExist && !force:
			return &fs.PathError{Op: "cp", Path: target.abs, Err: fs.ErrExist}
		case !force:
			return nil
		}
	}
	return copyFile(dirs, source, target, false)
}

// bytesOf returns the bytes a typed array, a DataView or an ArrayBuffer
// holds; JavaScript must not run while they are used.
func bytesOf(value moejs.Value) ([]byte, bool) {
	if !value.IsObject() {
		return nil, false
	}
	return value.AsObject().BufferData()
}

// bytesValue is an ArrayBuffer of data, which JavaScript then owns.
func bytesValue(realm *moejs.Realm, data []byte) (moejs.Value, error) {
	buffer, err := realm.NewArrayBuffer(data)
	if err != nil {
		return moejs.Undefined(), err
	}
	return engine.ObjectValue(buffer), nil
}

// openConstants are the flags and modes of fs.constants on this system.
func openConstants() map[string]any {
	constants := map[string]any{
		"O_RDONLY": os.O_RDONLY, "O_WRONLY": os.O_WRONLY, "O_RDWR": os.O_RDWR,
		"O_CREAT": os.O_CREATE, "O_EXCL": os.O_EXCL, "O_TRUNC": os.O_TRUNC,
		"O_APPEND": os.O_APPEND, "O_SYNC": os.O_SYNC,
		"S_IFMT": 0o170000, "S_IFREG": 0o100000, "S_IFDIR": 0o040000, "S_IFCHR": 0o020000,
		"S_IFBLK": 0o060000, "S_IFIFO": 0o010000, "S_IFLNK": 0o120000, "S_IFSOCK": 0o140000,
		"S_IRWXU": 0o700, "S_IRUSR": 0o400, "S_IWUSR": 0o200, "S_IXUSR": 0o100,
		"S_IRWXG": 0o70, "S_IRGRP": 0o40, "S_IWGRP": 0o20, "S_IXGRP": 0o10,
		"S_IRWXO": 0o7, "S_IROTH": 0o4, "S_IWOTH": 0o2, "S_IXOTH": 0o1,
		"F_OK": 0, "R_OK": 4, "W_OK": 2, "X_OK": 1,
		"UV_FS_COPYFILE_EXCL": 1, "COPYFILE_EXCL": 1,
		"UV_FS_COPYFILE_FICLONE": 2, "COPYFILE_FICLONE": 2,
		"UV_FS_COPYFILE_FICLONE_FORCE": 4, "COPYFILE_FICLONE_FORCE": 4,
		"UV_DIRENT_UNKNOWN": direntUnknown, "UV_DIRENT_FILE": direntFile, "UV_DIRENT_DIR": direntDir,
		"UV_DIRENT_LINK": direntLink, "UV_DIRENT_FIFO": direntFIFO, "UV_DIRENT_SOCKET": direntSocket,
		"UV_DIRENT_CHAR": direntChar, "UV_DIRENT_BLOCK": direntBlock,
	}
	for name, value := range platformOpenFlags() {
		constants[name] = value
	}
	return constants
}

// genericStatValues are the fields of fs.Stats from what every system
// reports.
func genericStatValues(info fs.FileInfo) []any {
	modified := milliseconds(info.ModTime())
	return []any{0, modeBits(info.Mode()), 1, 0, 0, 0, 4096, 0, info.Size(), (info.Size() + 511) / 512,
		modified, modified, modified, modified}
}
