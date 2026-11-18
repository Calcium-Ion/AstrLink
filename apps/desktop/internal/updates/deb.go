package updates

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// debOS is the system Debian packages install on. Tests on other systems
// stand in for it.
var debOS = "linux"

// debPackage is the package `mygo build` names after the app's slug, which
// installs the app in /opt/<package>.
const debPackage = "astrlink"

// dpkgInfo is the file of dpkg's database that lists the files a package
// installed. Tests point it elsewhere.
var dpkgInfo = "/var/lib/dpkg/info/" + debPackage + ".list"

// debExecutable is the path the package installs the app at.
var debExecutable = "/opt/" + debPackage + "/" + debPackage

// pkexecPath finds pkexec, which asks the user to authorize installing the
// package. Tests stand in for it.
var pkexecPath = func() (string, error) { return exec.LookPath("pkexec") }

// aptGet installs the package; pkexec runs it by its path.
const aptGet = "/usr/bin/apt-get"

// runPrivileged runs name with args through pkexec. Tests stand in for it.
var runPrivileged = func(ctx context.Context, pkexec string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, pkexec, args...).CombinedOutput()
}

// debInstalled reports whether the running app is the one the Debian package
// installed: its executable is the package's, which dpkg lists as a file of
// the package.
func debInstalled() bool {
	if runtime.GOOS != debOS {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if exe != debExecutable {
		return false
	}
	return packageLists(dpkgInfo, debExecutable)
}

// packageLists reports whether the dpkg file list at list names file.
func packageLists(list, file string) bool {
	f, err := os.Open(list)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if scanner.Text() == file {
			return true
		}
	}
	return false
}

// debManifestName is the manifest of the Debian package update for target,
// such as update-linux-amd64-deb.json, which a release publishes beside
// MyGo's update-<target>.json.
func debManifestName(target string) string { return "update-" + target + "-deb.json" }

// debInstallSupported reports whether a Debian package install can update
// itself: pkexec can ask the user to authorize apt.
func debInstallSupported() bool {
	_, err := pkexecPath()
	return err == nil
}

// debInstaller installs the release's Debian package over the installed
// one, through apt with the user's authorization.
type debInstaller struct {
	// dir keeps the package while apt installs it, under a name apt takes
	// for a package file; the file is readable by all, as apt's _apt user
	// reads it.
	dir  string
	file packageFile
	key  ed25519.PublicKey
	open Opener
}

// Install downloads the package, checks its size and signature, and has
// apt install it, which asks the user to authorize it. The installed app
// stays as it was until apt replaces it.
func (i debInstaller) Install(ctx context.Context, progress func(downloaded, total int64)) (err error) {
	pkexec, err := pkexecPath()
	if err != nil {
		return errors.New("pkexec is not installed, so AstrLink cannot ask to install the update")
	}
	body, err := i.open(ctx, i.file.URL)
	if err != nil {
		return err
	}
	defer body.Close()
	if err := os.MkdirAll(i.dir, 0o755); err != nil {
		return err
	}
	out, err := os.CreateTemp(i.dir, "astrlink-update-*.deb")
	if err != nil {
		return err
	}
	package_ := out.Name()
	defer func() {
		_ = out.Close()
		_ = os.Remove(package_)
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
		return fmt.Errorf("the package has %d bytes, want %d", n, i.file.Size)
	}
	if err := verifyDigest(i.key, sum.Sum(nil), i.file.Signature); err != nil {
		return err
	}
	if err := out.Chmod(0o644); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	output, err := runPrivileged(ctx, pkexec, aptGet, "install", "-y", package_)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 126 {
			// pkexec: the user dismissed the authorization dialog.
			return errDebNotAuthorized
		}
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, lastLines(detail, 5))
	}
	return nil
}

// errDebNotAuthorized is an install the user dismissed the authorization
// dialog of.
var errDebNotAuthorized = errors.New("the install was not authorized")

// lastLines is the last n lines of text.
func lastLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
