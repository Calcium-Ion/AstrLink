package plugin

import (
	"time"

	"golang.org/x/sys/unix"
)

// systemMemory reads the memory, load and kernel of Linux.
func systemMemory() memoryInfo {
	info := memoryInfo{load: []float64{0, 0, 0}, uptime: time.Since(bootTime).Seconds()}
	var sys unix.Sysinfo_t
	if unix.Sysinfo(&sys) == nil {
		unit := uint64(sys.Unit)
		if unit == 0 {
			unit = 1
		}
		info.total, info.free = uint64(sys.Totalram)*unit, uint64(sys.Freeram)*unit
		info.uptime = float64(sys.Uptime)
		for index := range 3 {
			info.load[index] = float64(sys.Loads[index]) / 65536
		}
	}
	var name unix.Utsname
	if unix.Uname(&name) == nil {
		info.release, info.version = unix.ByteSliceToString(name.Release[:]), unix.ByteSliceToString(name.Version[:])
	}
	return info
}
