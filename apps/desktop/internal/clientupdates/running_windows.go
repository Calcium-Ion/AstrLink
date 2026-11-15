package clientupdates

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// findRunning lists the processes whose program, or for Node.js and Bun a
// loaded module, lies in roots: files, or directories with everything
// below them.
func findRunning(roots []string) []runningProcess {
	keys := newPathRoots(roots)
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	type entry struct {
		pid  uint32
		name string
	}
	var processes []entry
	var process windows.ProcessEntry32
	process.Size = uint32(unsafe.Sizeof(process))
	for err = windows.Process32First(snapshot, &process); err == nil; err = windows.Process32Next(snapshot, &process) {
		processes = append(processes, entry{process.ProcessID, windows.UTF16ToString(process.ExeFile[:])})
	}
	windows.CloseHandle(snapshot)
	own := uint32(os.Getpid())
	var found []runningProcess
	for _, p := range processes {
		if p.pid == own || p.pid == 0 {
			continue
		}
		path, ok := processImage(p.pid)
		if !ok {
			continue
		}
		if keys.contains(path) {
			found = append(found, runningProcess{p.pid, path})
			continue
		}
		// Script clients run inside Node.js or Bun; their native addons
		// are loaded from the installation.
		stem := strings.ToLower(strings.TrimSuffix(p.name, filepath.Ext(p.name)))
		if stem != "node" && stem != "bun" {
			continue
		}
		for _, module := range processModules(p.pid) {
			if keys.contains(module) {
				found = append(found, runningProcess{p.pid, path})
				break
			}
		}
	}
	return found
}

func processImage(pid uint32) (string, bool) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32_768)
	size := uint32(len(buffer))
	if windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size) != nil {
		return "", false
	}
	return windows.UTF16ToString(buffer[:size]), true
}

func processModules(pid uint32) []string {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPMODULE, pid)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)
	var found []string
	var module windows.ModuleEntry32
	module.Size = uint32(unsafe.Sizeof(module))
	for err = windows.Module32First(snapshot, &module); err == nil; err = windows.Module32Next(snapshot, &module) {
		found = append(found, windows.UTF16ToString(module.ExePath[:]))
	}
	return found
}
