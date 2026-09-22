//go:build !windows

package privacymodel

import (
	"os"
	"testing"
)

// makeFileUnreadable clears every permission bit so os.Open fails until the
// test finishes.
func makeFileUnreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
}
