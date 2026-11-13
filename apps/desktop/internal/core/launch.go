package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

const (
	linuxONNXRuntimePathEnv  = "ASTRLINK_ONNX_RUNTIME_PATH"
	linuxONNXRuntimeResource = "onnxruntime/libonnxruntime.so.1.23.2"
	// binaryName is the Core executable staged beside the desktop.
	binaryName = "astrlink-core"
)

// inferenceBind is where the inference plane listens: the saved scope and
// port together.
type inferenceBind struct {
	listen prefs.InferenceListen
	port   uint16
}

func (b inferenceBind) address() string {
	return b.listen.Host() + ":" + strconv.Itoa(int(b.port))
}

// launchSettings are the saved preferences Core reads from its command
// line. Changing any of them takes a gateway restart.
type launchSettings struct {
	inference                   inferenceBind
	maxConcurrentInspections    uint16
	responseStartTimeoutSeconds uint32
	maxRequestBodyMiB           uint32
	useSystemProxy              bool
}

// followsSessionLock says whether goos reports the session locking reliably
// enough for Core to drop its raw unlock idle timeout. The desktop ends the
// raw unlock when the screen locks, the computer sleeps, or the user switches
// away, so on macOS and Windows Core keeps an unlock open until that lock
// with --raw-unlock-until-lock. Linux follows logind where it runs, but Core
// keeps its idle timeout there too, since a desktop without logind sends no
// signal at all.
func followsSessionLock(goos string) bool {
	return goos == "darwin" || goos == "windows"
}

// sidecarArgs are Core's command-line arguments when the desktop runs on
// goos.
func sidecarArgs(goos string, parentPID int, dataDirectory string, launch launchSettings) []string {
	arguments := []string{
		"--parent-pid", strconv.Itoa(parentPID),
		"--data-dir", dataDirectory,
		"--inference-listen", launch.inference.address(),
	}
	// Other devices are configured with the saved port, so an exposed
	// gateway never moves to a random one; Core reports the occupied port.
	if launch.inference.listen == prefs.ListenLoopback {
		arguments = append(arguments, "--inference-port-fallback")
	}
	outbound := "direct"
	if launch.useSystemProxy {
		outbound = "system"
	}
	arguments = append(arguments,
		"--control-listen", "127.0.0.1:0",
		"--control-token-stdin",
		"--observer-token-stdin",
		"--max-concurrent-inspections", strconv.Itoa(int(launch.maxConcurrentInspections)),
		"--response-start-timeout-seconds", strconv.FormatUint(uint64(launch.responseStartTimeoutSeconds), 10),
		"--max-request-body-mib", strconv.FormatUint(uint64(launch.maxRequestBodyMiB), 10),
		"--outbound-proxy="+outbound,
	)
	if followsSessionLock(goos) {
		arguments = append(arguments, "--raw-unlock-until-lock")
	}
	return arguments
}

// localKeyArgs are the Core arguments that say where its local key comes
// from. They name a flag or a path, never the key.
func localKeyArgs(key keystore.ResolvedKey) []string {
	var arguments []string
	if key.StdinHex != "" {
		arguments = append(arguments, "--kek-stdin")
	}
	if key.KeyFile != "" {
		arguments = append(arguments, "--kek-file="+key.KeyFile)
	}
	return arguments
}

// sidecarStdin is one secret per line in the fixed order Core's
// readTokenLines mirrors: the operator control token, the local key from the
// keychain (empty when Core uses a key file), and the observer token handed
// to agent tools.
func sidecarStdin(controlToken, localKeyHex, observerToken string) []byte {
	payload := make([]byte, 0, len(controlToken)+len(localKeyHex)+len(observerToken)+3)
	for _, line := range []string{controlToken, localKeyHex, observerToken} {
		payload = append(payload, line...)
		payload = append(payload, '\n')
	}
	return payload
}

func generateControlToken() (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("unable to generate local control token: %w", err)
	}
	return hex.EncodeToString(random[:]), nil
}

func recoveryDelay(attempt uint8) time.Duration {
	shift := min(max(int(attempt)-1, 0), 4)
	return time.Duration(1<<shift) * time.Second
}

func linuxONNXRuntimePath(resourceDirectory string) string {
	return filepath.Join(resourceDirectory, filepath.FromSlash(linuxONNXRuntimeResource))
}

// Locate finds the Core executable and the resource root it ships in.
// `make desktop-sidecar` stages Core, its workers and the Linux ONNX Runtime
// under resources/<os>-<arch>, which MyGo merges into the app's resource
// directory as bin/ and onnxruntime/; a `go run` reads them where they were
// staged.
func Locate(resourceDirectory string) (binary, root string, err error) {
	name := binaryName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, root := range []string{
		resourceDirectory,
		filepath.Join(resourceDirectory, runtime.GOOS+"-"+runtime.GOARCH),
	} {
		candidate := filepath.Join(root, "bin", name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, root, nil
		}
	}
	return "", "", fmt.Errorf("unable to resolve astrlink-core sidecar: bin/%s is missing from the app resources", name)
}
