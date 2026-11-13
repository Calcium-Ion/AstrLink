//go:build unix

package plugin

import (
	"errors"
	"io/fs"
	"syscall"
)

// unixErrnos are the codes of Node for the errors of the system.
var unixErrnos = map[syscall.Errno]string{
	syscall.EPERM: "EPERM", syscall.ENOENT: "ENOENT", syscall.EIO: "EIO", syscall.EBADF: "EBADF",
	syscall.EAGAIN: "EAGAIN", syscall.EACCES: "EACCES", syscall.EBUSY: "EBUSY", syscall.EEXIST: "EEXIST",
	syscall.EXDEV: "EXDEV", syscall.ENOTDIR: "ENOTDIR", syscall.EISDIR: "EISDIR", syscall.EINVAL: "EINVAL",
	syscall.ENFILE: "ENFILE", syscall.EMFILE: "EMFILE", syscall.ETXTBSY: "ETXTBSY", syscall.EFBIG: "EFBIG",
	syscall.ENOSPC: "ENOSPC", syscall.EROFS: "EROFS", syscall.EMLINK: "EMLINK",
	syscall.ENAMETOOLONG: "ENAMETOOLONG", syscall.ENOSYS: "ENOSYS", syscall.ENOTEMPTY: "ENOTEMPTY",
	syscall.ELOOP: "ELOOP", syscall.ENOTSUP: "ENOTSUP",
}

// platformErrno is the code of Node for an error of the system, "" for any
// other error.
func platformErrno(err error) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return ""
	}
	return unixErrnos[errno]
}

// executable reports whether the owner may execute the file.
func executable(info fs.FileInfo) bool { return info.Mode().Perm()&0o111 != 0 }

// platformOpenFlags are the flags of fs.constants beyond the portable ones.
func platformOpenFlags() map[string]any {
	return map[string]any{
		"O_NOCTTY": syscall.O_NOCTTY, "O_DIRECTORY": syscall.O_DIRECTORY, "O_NOFOLLOW": syscall.O_NOFOLLOW,
		"O_NONBLOCK": syscall.O_NONBLOCK, "O_DSYNC": syscall.O_DSYNC,
	}
}
