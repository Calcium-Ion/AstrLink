package agentcli

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// stillActive is GetExitCodeProcess's code for a running process.
const stillActive = 259

// pidAlive reports whether a process with pid is running.
func pidAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(handle)
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == stillActive
}

// refused reports a connection nothing was listening for.
func refused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, syscall.ECONNREFUSED)
}
