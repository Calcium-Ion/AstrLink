package agents

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
	"github.com/QuantumNous/astrlink/desktop/internal/realpath"
	"github.com/QuantumNous/astrlink/desktop/internal/wsl"
)

// Edits to files other programs own: agent configs, skills, and client
// connection settings in the user's home directory.

var (
	hostFiles         sync.Mutex
	temporarySequence atomic.Uint64
)

// LockHostFiles serializes this process's edits to host files: agent
// installs and client configuration both rewrite `~/.claude/settings.json`.
// It returns the unlock.
func LockHostFiles() func() {
	hostFiles.Lock()
	return hostFiles.Unlock
}

// ReadOptional reads a file; false for a missing one.
func ReadOptional(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("unable to read %s: %v", path, ioError(err))
	}
	return string(data), true, nil
}

// TemporarySibling is a unique sibling for staging a replacement of name
// in parent.
func TemporarySibling(parent, name string) string {
	return filepath.Join(parent, fmt.Sprintf(".%s.tmp-%d-%d", name, os.Getpid(), temporarySequence.Add(1)-1))
}

// WriteOptions are how WriteText writes.
type WriteOptions struct {
	// Secret tightens the file to owner-only access (Unix, and WSL homes).
	Secret bool
	// Expected is the contents the edit was computed from; the write is
	// abandoned when the file no longer holds them. Nil checks nothing.
	Expected *Expected
}

// Expected is what a file held when an edit was computed: Exists is false
// for a missing file.
type Expected struct {
	Text   string
	Exists bool
}

// ExpectText expects a file to hold text, or to be missing when exists is
// false.
func ExpectText(text string, exists bool) *Expected { return &Expected{Text: text, Exists: exists} }

var errChanged = errors.New("the file changed while it was being edited; try again")

// WriteText replaces a user's file through a temporary sibling, so a crash
// or a full disk leaves either the old contents or the new ones. A
// symlinked file is replaced at its target, and an existing file keeps its
// permissions.
func WriteText(path, contents string, options WriteOptions) error {
	target := path
	if info, err := os.Lstat(path); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		resolved, err := realpath.Resolve(path)
		if err != nil {
			return fmt.Errorf("unable to resolve %s: %v", path, ioError(err))
		}
		target = resolved
	}
	parent, name := filepath.Dir(target), filepath.Base(target)
	if name == "." || name == string(filepath.Separator) {
		return fmt.Errorf("unable to write %s: no parent directory", path)
	}
	if err := os.MkdirAll(parent, 0o777); err != nil {
		return fmt.Errorf("unable to create %s: %v", parent, ioError(err))
	}
	var mode fs.FileMode
	existed := false
	if info, err := os.Stat(target); err == nil {
		mode, existed = info.Mode().Perm(), true
	}
	temporary := TemporarySibling(parent, name)
	err := func() error {
		file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if err != nil {
			return err
		}
		defer file.Close()
		// Access is settled before the contents land, so they never sit in
		// a file others can read.
		shared, err := wsl.SettleShareMode(temporary, target, options.Secret)
		if err != nil {
			return err
		}
		if !shared {
			if existed {
				if err := file.Chmod(mode); err != nil && runtime.GOOS != "windows" {
					return err
				}
			}
			if options.Secret && runtime.GOOS != "windows" {
				if err := file.Chmod(0o600); err != nil {
					return err
				}
			}
		}
		if _, err := file.WriteString(contents); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if expected := options.Expected; expected != nil {
			current, err := os.ReadFile(target)
			exists := err == nil
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if exists != expected.Exists || exists && string(current) != expected.Text {
				return errChanged
			}
		}
		return prefs.AtomicReplace(temporary, target)
	}()
	if err != nil {
		os.Remove(temporary)
		return fmt.Errorf("unable to write %s: %v", path, ioError(err))
	}
	return nil
}

// RemovePath removes a file, or a directory and everything in it; a
// missing one is fine.
func RemovePath(path string) error {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("unable to inspect %s: %v", path, ioError(err))
	case info.IsDir():
		err = os.RemoveAll(path)
	default:
		if err = os.Remove(path); err != nil {
			err = os.RemoveAll(path)
		}
	}
	if err != nil {
		return fmt.Errorf("unable to remove %s: %v", path, ioError(err))
	}
	return nil
}

// ioError drops the path a *fs.PathError repeats.
func ioError(err error) error {
	var pathError *fs.PathError
	if errors.As(err, &pathError) {
		return pathError.Err
	}
	return err
}
