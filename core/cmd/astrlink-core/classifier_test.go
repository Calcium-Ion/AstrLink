package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
)

// stubRegistryProvider reports no ready installation; construction never
// consults the provider, so any deterministic stub is acceptable.
func stubRegistryProvider() autoclassifier.ReadyInstallationProvider {
	return autoclassifier.ReadyInstallationProviderFunc(
		func() (contract.ReadyAutoClassifierInstallation, bool) {
			return contract.ReadyAutoClassifierInstallation{}, false
		},
	)
}

func captureClassifierLogs(lines *[]string) func(string, ...any) {
	return func(format string, args ...any) {
		*lines = append(*lines, fmt.Sprintf(format, args...))
	}
}

func TestBuildClassifierDependencyWiresBundledWorker(t *testing.T) {
	classifier, closer := buildClassifierDependency(classifierOptions{
		workerPath:    filepath.Join(t.TempDir(), "classifier-worker"),
		dataDirectory: t.TempDir(),
		newRegistry: func(string) (autoclassifier.ReadyInstallationProvider, error) {
			return stubRegistryProvider(), nil
		},
		logf: t.Logf,
	})
	if classifier == nil {
		t.Fatal("classifier dependency = nil, want a wired client")
	}
	if closer == nil {
		t.Fatal("classifier closer = nil, want the client's Close")
	}
	closer()
	closer() // Close is idempotent.
}

func TestBuildClassifierDependencyFallsBackToSiblingExecutable(t *testing.T) {
	// With an empty worker path the only route to a wired client is the
	// sibling fallback: a missing fallback would leave the path empty and
	// fail construction instead.
	classifier, closer := buildClassifierDependency(classifierOptions{
		siblingPath: func() (string, error) {
			return filepath.Join(t.TempDir(), "astrlink-classifier-worker"), nil
		},
		dataDirectory: t.TempDir(),
		newRegistry: func(string) (autoclassifier.ReadyInstallationProvider, error) {
			return stubRegistryProvider(), nil
		},
		logf: t.Logf,
	})
	if classifier == nil {
		t.Fatal("classifier dependency = nil, want the sibling fallback to wire a client")
	}
	if closer == nil {
		t.Fatal("classifier closer = nil")
	}
	closer()
}

func TestBuildClassifierDependencyFailsOpenWhenUnavailable(t *testing.T) {
	for name, options := range map[string]classifierOptions{
		"sibling lookup fails": {
			siblingPath:   func() (string, error) { return "", errors.New("resolve core executable: missing") },
			dataDirectory: t.TempDir(),
			newRegistry: func(string) (autoclassifier.ReadyInstallationProvider, error) {
				return stubRegistryProvider(), nil
			},
		},
		"registry construction fails": {
			siblingPath: func() (string, error) {
				return filepath.Join(t.TempDir(), "astrlink-classifier-worker"), nil
			},
			dataDirectory: t.TempDir(),
			newRegistry: func(string) (autoclassifier.ReadyInstallationProvider, error) {
				return nil, errors.New("mkdir installations: read-only")
			},
		},
		"client construction fails": {
			// An empty resolved path trips autoclassifier.New's required
			// executable path check.
			siblingPath:   func() (string, error) { return "", nil },
			dataDirectory: t.TempDir(),
			newRegistry: func(string) (autoclassifier.ReadyInstallationProvider, error) {
				return stubRegistryProvider(), nil
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var lines []string
			options.logf = captureClassifierLogs(&lines)
			classifier, closer := buildClassifierDependency(options)
			if classifier != nil || closer != nil {
				t.Fatalf("fail-open broken: classifier=%T closer-set=%v", classifier, closer != nil)
			}
			if len(lines) == 0 {
				t.Fatal("construction failure was not logged")
			}
		})
	}
}
