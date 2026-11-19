package plugin

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	globalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	getTickCount64       = kernel32.NewProc("GetTickCount64")
)

// memoryStatus is MEMORYSTATUSEX.
type memoryStatus struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// systemMemory reads the memory and version of Windows.
func systemMemory() memoryInfo {
	info := memoryInfo{load: []float64{0, 0, 0}, uptime: time.Since(bootTime).Seconds()}
	status := memoryStatus{length: uint32(unsafe.Sizeof(memoryStatus{}))}
	if ok, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status))); ok != 0 {
		info.total, info.free = status.totalPhys, status.availPhys
	}
	if ticks, _, _ := getTickCount64.Call(); ticks != 0 {
		info.uptime = float64(ticks) / 1000
	}
	version := windows.RtlGetVersion()
	info.release = fmt.Sprintf("%d.%d.%d", version.MajorVersion, version.MinorVersion, version.BuildNumber)
	info.version = "Windows"
	return info
}
