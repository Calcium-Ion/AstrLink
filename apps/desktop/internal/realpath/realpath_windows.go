package realpath

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// finalPath is the path Windows opens for path, through every symbolic
// link and junction, without the \\?\ prefix.
func finalPath(path string) (string, bool) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	name, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return "", false
	}
	// No access is needed to read the path, and backup semantics opens
	// directories too.
	handle, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(handle)
	// FILE_NAME_NORMALIZED|VOLUME_NAME_DOS: a drive-letter path with every
	// link resolved.
	const flags = 0
	buffer := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), flags)
		if err != nil || n == 0 {
			return "", false
		}
		if int(n) < len(buffer) {
			return trimVerbatim(windows.UTF16ToString(buffer[:n])), true
		}
		buffer = make([]uint16, n)
	}
}

// trimVerbatim drops the \\?\ prefix GetFinalPathNameByHandle adds:
// \\?\C:\x becomes C:\x and \\?\UNC\server\share\x \\server\share\x.
func trimVerbatim(path string) string {
	if rest, ok := strings.CutPrefix(path, `\\?\UNC\`); ok {
		return `\\` + rest
	}
	return strings.TrimPrefix(path, `\\?\`)
}
