//go:build !linux && !darwin && !windows

package plugin

import "time"

// systemMemory is what a system without a reader here reports: nothing.
func systemMemory() memoryInfo {
	return memoryInfo{load: []float64{0, 0, 0}, uptime: time.Since(bootTime).Seconds()}
}
