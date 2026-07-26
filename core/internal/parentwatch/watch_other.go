//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris

package parentwatch

func currentParentPID() (int, bool) {
	return 0, false
}
