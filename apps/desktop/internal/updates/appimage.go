package updates

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// appImageOS is the system AppImages run on. Tests on other systems stand
// in for it.
var appImageOS = "linux"

// createTemp creates the file an AppImage update is written to. Tests make
// writing it fail.
var createTemp = os.CreateTemp

// appImageFile is the AppImage file the app runs from, with symlinks
// resolved, or false when the app is not an AppImage: $APPIMAGE, which the
// AppImage runtime sets, must name an existing regular file. The running
// executable is in the AppImage's mount, which an update cannot replace.
func appImageFile() (string, bool) {
	if runtime.GOOS != appImageOS {
		return "", false
	}
	file := os.Getenv("APPIMAGE")
	if file == "" {
		return "", false
	}
	file, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", false
	}
	if file, err = filepath.Abs(file); err != nil {
		return "", false
	}
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return file, true
}

// appImageManifestName is the manifest of the AppImage update for target,
// such as update-linux-amd64-appimage.json, which a release publishes
// beside MyGo's update-<target>.json.
func appImageManifestName(target string) string { return "update-" + target + "-appimage.json" }

// appImageInstaller replaces the AppImage file target with the release's
// AppImage, which open serves and key must have signed.
type appImageInstaller struct {
	target string
	file   packageFile
	key    ed25519.PublicKey
	open   Opener
}

// Install writes the new AppImage beside the old one, checks its size and
// signature, then renames it over the old file. The old file stays as it
// was until the rename; a failed install removes what it wrote.
func (i appImageInstaller) Install(ctx context.Context, progress func(downloaded, total int64)) (err error) {
	info, err := os.Stat(i.target)
	if err != nil {
		return err
	}
	body, err := i.open(ctx, i.file.URL)
	if err != nil {
		return err
	}
	defer body.Close()
	dir := filepath.Dir(i.target)
	out, err := createTemp(dir, "."+filepath.Base(i.target)+".update-*")
	if err != nil {
		return err
	}
	temp := out.Name()
	defer func() {
		if err != nil {
			_ = out.Close()
			_ = os.Remove(temp)
		}
	}()
	sum := sha256.New()
	var w io.Writer = io.MultiWriter(out, sum)
	if progress != nil {
		var downloaded int64
		w = io.MultiWriter(w, progressWriter(func(n int) {
			downloaded += int64(n)
			progress(downloaded, i.file.Size)
		}))
	}
	n, err := io.Copy(w, io.LimitReader(body, i.file.Size+1))
	if err != nil {
		return err
	}
	if n != i.file.Size {
		return fmt.Errorf("the AppImage has %d bytes, want %d", n, i.file.Size)
	}
	if err := verifyDigest(i.key, sum.Sum(nil), i.file.Signature); err != nil {
		return err
	}
	// Executable for everyone, as the AppImage was when downloaded; bits
	// the old file had beyond that stay.
	if err := out.Chmod(info.Mode().Perm() | 0o755); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, i.target); err != nil {
		return err
	}
	// The rename is done; a directory that cannot be synced only risks it
	// on a power loss.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
