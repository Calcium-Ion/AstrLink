package core

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

func loopbackLaunch(port uint16) launchSettings {
	s := defaultState()
	launch := s.launchSettings()
	launch.inference.port = port
	return launch
}

func TestSidecarArgsCarryPIDAndDataPathButNoToken(t *testing.T) {
	launch := loopbackLaunch(8317)
	launch.responseStartTimeoutSeconds = 0
	got := sidecarArgs("linux", 4242, "/tmp/astrlink-data", launch)
	want := []string{
		"--parent-pid", "4242",
		"--data-dir", "/tmp/astrlink-data",
		"--inference-listen", "127.0.0.1:8317",
		"--inference-port-fallback",
		"--control-listen", "127.0.0.1:0",
		"--control-token-stdin",
		"--observer-token-stdin",
		"--max-concurrent-inspections", "16",
		"--response-start-timeout-seconds", "0",
		"--max-request-body-mib", "0",
		"--outbound-proxy=system",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}

	launch.maxRequestBodyMiB = 64
	launch.useSystemProxy = false
	direct := sidecarArgs("linux", 4242, "/tmp/astrlink-data", launch)
	if !hasPair(direct, "--max-request-body-mib", "64") || !slices.Contains(direct, "--outbound-proxy=direct") || slices.Contains(direct, "--outbound-proxy=system") {
		t.Fatalf("direct launch arguments: %q", direct)
	}
}

func TestExposedSidecarBindsEveryInterfaceWithoutAPortFallback(t *testing.T) {
	launch := loopbackLaunch(8317)
	launch.inference.listen = prefs.ListenAllInterfaces
	args := sidecarArgs("linux", 4242, "/tmp/astrlink-data", launch)
	if !hasPair(args, "--inference-listen", "0.0.0.0:8317") || slices.Contains(args, "--inference-port-fallback") {
		t.Fatalf("exposed launch arguments: %q", args)
	}
	// The control plane stays on this machine whatever the inference scope.
	if !hasPair(args, "--control-listen", "127.0.0.1:0") {
		t.Fatalf("control listener moved: %q", args)
	}
}

// Core keeps a raw unlock open until the session locks only where the
// desktop sees that lock; Linux keeps Core's idle timeout.
func TestSidecarArgsFollowTheSessionLockOnMacOSAndWindows(t *testing.T) {
	launch := loopbackLaunch(8317)
	for goos, want := range map[string]bool{"darwin": true, "windows": true, "linux": false} {
		args := sidecarArgs(goos, 4242, "/tmp/astrlink-data", launch)
		if got := slices.Contains(args, "--raw-unlock-until-lock"); got != want {
			t.Errorf("%s: --raw-unlock-until-lock present = %v, want %v (%q)", goos, got, want, args)
		}
		if followsSessionLock(goos) != want {
			t.Errorf("followsSessionLock(%q) = %v, want %v", goos, !want, want)
		}
	}
}

func hasPair(args []string, name, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestRecoveryBackoffIsBoundedAndExponential(t *testing.T) {
	for attempt, want := range map[uint8]time.Duration{
		0: time.Second, 1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second,
		4: 8 * time.Second, 5: 16 * time.Second, 99: 16 * time.Second,
	} {
		if got := recoveryDelay(attempt); got != want {
			t.Errorf("recoveryDelay(%d) = %s, want %s", attempt, got, want)
		}
	}
}

func TestLinuxONNXRuntimeUsesTheResourceDirectory(t *testing.T) {
	got := linuxONNXRuntimePath(filepath.Join("res", "linux-amd64"))
	want := filepath.Join("res", "linux-amd64", "onnxruntime", "libonnxruntime.so.1.23.2")
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestSidecarStdinPlacesTokensOnTheirFixedLines(t *testing.T) {
	for _, key := range []string{"", strings.Repeat("ab", 32)} {
		payload := string(sidecarStdin("control", key, "observer"))
		if !strings.HasSuffix(payload, "\n") {
			t.Fatalf("payload %q lacks a final newline", payload)
		}
		lines := strings.Split(strings.TrimSuffix(payload, "\n"), "\n")
		if !slices.Equal(lines, []string{"control", key, "observer"}) {
			t.Fatalf("got lines %q", lines)
		}
	}
}

func TestLocalKeyReachesCoreOnStdinAndNeverInArguments(t *testing.T) {
	key := strings.Repeat("cd", 32)
	tests := []struct {
		name     string
		resolved keystore.ResolvedKey
		want     []string
	}{
		{"keychain", keystore.ResolvedKey{Storage: keystore.StorageKeychain, StdinHex: key}, []string{"--kek-stdin"}},
		{"fallback file", keystore.ResolvedKey{Storage: keystore.StorageKeychainUnavailable, KeyFile: "/tmp/astrlink-data/local.fallback.key"}, []string{"--kek-file=/tmp/astrlink-data/local.fallback.key"}},
		{"core default", keystore.ResolvedKey{Storage: keystore.StorageFile}, nil},
	}
	for _, tt := range tests {
		got := localKeyArgs(tt.resolved)
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
		for _, arg := range got {
			if strings.Contains(arg, key) {
				t.Errorf("%s: the key reached the arguments", tt.name)
			}
		}
	}
}

func TestControlTokenIsRandomAndArgumentSafe(t *testing.T) {
	first, err := generateControlToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateControlToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || strings.Trim(first, "0123456789abcdef") != "" || first == second {
		t.Fatalf("tokens %q and %q", first, second)
	}
}
