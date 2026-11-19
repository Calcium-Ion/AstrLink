package plugin

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// systemMemory reads the memory, load and kernel of macOS.
func systemMemory() memoryInfo {
	info := memoryInfo{load: []float64{0, 0, 0}, uptime: time.Since(bootTime).Seconds()}
	if total, err := unix.SysctlUint64("hw.memsize"); err == nil {
		info.total = total
	}
	if free, err := unix.SysctlUint32("vm.page_free_count"); err == nil {
		info.free = uint64(free) * uint64(os.Getpagesize())
	}
	if boot, err := unix.SysctlTimeval("kern.boottime"); err == nil {
		info.uptime = time.Since(time.Unix(boot.Unix())).Seconds()
	}
	var name unix.Utsname
	if unix.Uname(&name) == nil {
		info.release, info.version = unix.ByteSliceToString(name.Release[:]), unix.ByteSliceToString(name.Version[:])
	}
	return info
}
