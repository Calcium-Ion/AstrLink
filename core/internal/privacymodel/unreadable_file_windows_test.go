//go:build windows

package privacymodel

import (
	"syscall"
	"testing"
)

// makeFileUnreadable holds a handle that denies all sharing, so every other
// open of the file (including os.Open) fails with a sharing violation until
// the test finishes. Chmod cannot deny reads on Windows; it only toggles the
// read-only attribute. Stat still works because it does not open the file.
func makeFileUnreadable(t *testing.T, path string) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(
		name,
		syscall.GENERIC_READ,
		0, // no FILE_SHARE_* flags: reject concurrent opens
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.CloseHandle(handle) })
}
