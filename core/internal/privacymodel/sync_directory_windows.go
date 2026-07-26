//go:build windows

package privacymodel

// Windows does not provide the same portable directory fsync operation as
// Unix. Individual asset files and the ready marker are flushed before the
// atomic directory rename.
func syncDirectory(string) error {
	return nil
}
