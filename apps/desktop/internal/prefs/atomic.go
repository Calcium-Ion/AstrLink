package prefs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
)

var temporarySequence atomic.Uint64

// TemporaryPath returns a file beside path for a write that replaces it,
// unique to this process and call.
func TemporaryPath(path string) string {
	ext := filepath.Ext(path)
	return fmt.Sprintf("%s.tmp-%d-%d", path[:len(path)-len(ext)], os.Getpid(), temporarySequence.Add(1)-1)
}

// WriteFileAtomic replaces path with data, owner-only, so a reader sees the
// old file or the new one and a crash never leaves half of it. The parent
// directory is created and, on Unix, synced after the rename.
func WriteFileAtomic(path string, data []byte) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	temporary := TemporaryPath(path)
	err := func() error {
		file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := file.Write(data); err != nil {
			file.Close()
			return err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if err := AtomicReplace(temporary, path); err != nil {
			return err
		}
		return syncDirectory(parent)
	}()
	if err != nil {
		os.Remove(temporary)
	}
	return err
}

// AtomicReplace moves source over destination in one step. On Windows,
// os.Rename is MoveFileEx with MOVEFILE_REPLACE_EXISTING, as the Tauri
// shell's own replace was.
func AtomicReplace(source, destination string) error {
	return os.Rename(source, destination)
}

func syncDirectory(directory string) error {
	// Windows cannot open a directory as a file to sync it.
	if runtime.GOOS == "windows" {
		return nil
	}
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
