package plugin

import (
	"errors"
	"io/fs"
	"syscall"

	"golang.org/x/sys/windows"
)

// windowsErrnos are the codes of Node for the errors of Windows, as libuv
// maps them.
var windowsErrnos = map[syscall.Errno]string{
	windows.ERROR_FILE_NOT_FOUND: "ENOENT", windows.ERROR_PATH_NOT_FOUND: "ENOENT",
	windows.ERROR_INVALID_NAME: "ENOENT", windows.ERROR_ACCESS_DENIED: "EPERM",
	windows.ERROR_ALREADY_EXISTS: "EEXIST", windows.ERROR_FILE_EXISTS: "EEXIST",
	windows.ERROR_DIR_NOT_EMPTY: "ENOTEMPTY", windows.ERROR_SHARING_VIOLATION: "EBUSY",
	windows.ERROR_LOCK_VIOLATION: "EBUSY", windows.ERROR_DIRECTORY: "ENOTDIR",
	windows.ERROR_DISK_FULL: "ENOSPC", windows.ERROR_HANDLE_DISK_FULL: "ENOSPC",
	windows.ERROR_NOT_SAME_DEVICE: "EXDEV", windows.ERROR_FILENAME_EXCED_RANGE: "ENAMETOOLONG",
	windows.ERROR_INVALID_HANDLE: "EBADF", windows.ERROR_TOO_MANY_OPEN_FILES: "EMFILE",
	windows.ERROR_INVALID_PARAMETER: "EINVAL", windows.ERROR_NOT_SUPPORTED: "ENOTSUP",
	windows.ERROR_CANT_RESOLVE_FILENAME: "ELOOP",
}

// platformErrno is the code of Node for an error of the system, "" for any
// other error.
func platformErrno(err error) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return ""
	}
	return windowsErrnos[errno]
}

// executable reports whether the file runs: Windows has no execute bit.
func executable(fs.FileInfo) bool { return true }

// platformOpenFlags are the flags of fs.constants beyond the portable ones.
func platformOpenFlags() map[string]any { return map[string]any{} }
