package plugin

import (
	"os"
	"os/user"
	goruntime "runtime"
	"strconv"
	"time"

	"github.com/MoeForge/moejs"
)

// osFunctions are the host's side of node:os: what the system it runs on
// reports. homedir and tmpdir are the plugin's own directories.
func (bound *instance) osFunctions() map[string]any {
	dirs := bound.program.dirs
	return map[string]any{
		"info": bound.function("info", 0, func(realm *moejs.Realm, _ moejs.Value, _ []moejs.Value) (moejs.Value, error) {
			hostname, _ := os.Hostname()
			memory := systemMemory()
			info := map[string]any{
				"platform": nodePlatform(),
				"arch":     nodeArch(),
				"machine":  machineName(),
				"type":     systemType(),
				"release":  memory.release,
				"version":  memory.version,
				"hostname": hostname,
				"homedir":  dirs.data,
				"tmpdir":   dirs.tmp(),
				"cpus":     goruntime.NumCPU(),
				"totalmem": memory.total,
				"freemem":  memory.free,
				"uptime":   memory.uptime,
				"loadavg":  memory.load,
				"username": "",
				"uid":      -1,
				"gid":      -1,
				"shell":    nil,
			}
			if current, err := user.Current(); err == nil {
				info["username"] = current.Username
				if uid, err := strconv.Atoi(current.Uid); err == nil {
					info["uid"] = uid
				}
				if gid, err := strconv.Atoi(current.Gid); err == nil {
					info["gid"] = gid
				}
			}
			return realm.FromGo(info)
		}),
	}
}

// systemType is os.type().
func systemType() string {
	switch goruntime.GOOS {
	case "windows":
		return "Windows_NT"
	case "darwin":
		return "Darwin"
	case "linux":
		return "Linux"
	case "freebsd":
		return "FreeBSD"
	}
	return goruntime.GOOS
}

// machineName is os.machine().
func machineName() string {
	switch goruntime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		if goruntime.GOOS == "linux" {
			return "aarch64"
		}
		return "arm64"
	case "386":
		return "i686"
	}
	return goruntime.GOARCH
}

// memoryInfo is what the system reports of its memory, load and kernel.
type memoryInfo struct {
	total, free      uint64
	uptime           float64
	load             []float64
	release, version string
}

// bootTime approximates when the system started, for os.uptime() where the
// system does not say.
var bootTime = time.Now()
