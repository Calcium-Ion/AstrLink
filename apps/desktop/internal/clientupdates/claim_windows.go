package clientupdates

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// openClaim opens file so that nobody else can open it until the handle
// closes, which deletes it; nil while another update holds it.
func openClaim(file string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return nil, err
	}
	for wait := 0; ; wait++ {
		handle, err := windows.CreateFile(name, windows.DELETE|windows.GENERIC_WRITE, 0, nil,
			windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_DELETE_ON_CLOSE, 0)
		switch {
		case err == nil:
			return os.NewFile(uintptr(handle), file), nil
		case errors.Is(err, windows.ERROR_SHARING_VIOLATION):
			// Another update holds it.
			return nil, nil
		// ERROR_ACCESS_DENIED also while the previous holder's file is
		// being deleted; a lasting one is a permission problem.
		case errors.Is(err, windows.ERROR_ACCESS_DENIED) && wait < 10:
			time.Sleep(50 * time.Millisecond)
		case errors.Is(err, windows.ERROR_PATH_NOT_FOUND) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
			return nil, &os.PathError{Op: "open", Path: file, Err: os.ErrNotExist}
		default:
			return nil, err
		}
	}
}

// releaseClaim closes the claim; Windows deletes the file then.
func releaseClaim(handle *os.File, _ string) { _ = handle.Close() }

// renameBusy reports a rename that antivirus or the search indexer may
// let through shortly.
func renameBusy(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
