//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package parentwatch

import (
	"os"
	"testing"
)

func TestCurrentParentPIDIsSupportedOnUnix(t *testing.T) {
	pid, supported := currentParentPID()
	if !supported || pid != os.Getppid() {
		t.Fatalf("currentParentPID = (%d, %t), want (%d, true)", pid, supported, os.Getppid())
	}
}
