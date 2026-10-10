//go:build darwin

package privacyworker

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// topCores counts the fastest cluster on Apple Silicon, and physical cores on
// Intel, whose logical count includes Hyper-Threading siblings.
func topCores(logicalCPUs int) int {
	name := "hw.physicalcpu"
	if runtime.GOARCH == "arm64" {
		name = "hw.perflevel0.logicalcpu"
	}
	count, err := unix.SysctlUint32(name)
	if err != nil || count == 0 {
		return logicalCPUs / 2
	}
	return int(count)
}
