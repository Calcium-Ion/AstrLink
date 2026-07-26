//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package parentwatch

import "os"

func currentParentPID() (int, bool) {
	return os.Getppid(), true
}
