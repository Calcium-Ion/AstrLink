//go:build windows

package privacymodel

import "golang.org/x/sys/windows"

// windowsBuildNumber returns the build of the running system. RtlGetVersion,
// unlike GetVersionEx, reports it whatever the executable's manifest says.
func windowsBuildNumber() uint32 {
	return windows.RtlGetVersion().BuildNumber
}
