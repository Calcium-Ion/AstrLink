package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

const (
	// SessionSchemaVersion is the control-session.json schema agent tools
	// read (core/internal/agentcli).
	SessionSchemaVersion = 1
	// ControlSocketFileName is Core's control socket in its data directory.
	ControlSocketFileName = "control.sock"
)

// SessionFile is the locator agent tools use to reach Core.
type SessionFile struct {
	SchemaVersion int    `json:"schema_version"`
	ControlSocket string `json:"control_socket,omitempty"`
	ControlURL    string `json:"control_url,omitempty"`
	// ControlToken is the observer-role bearer token for agent tools on
	// Windows. The field keeps its historical name; it never carries the
	// desktop's operator token.
	ControlToken string `json:"control_token,omitempty"`
	// PID is Core's process ID, for readers that check liveness.
	PID int `json:"pid,omitempty"`
	// DesktopPID is the desktop process that wrote the file. Clearing is
	// skipped when another live desktop owns it, so a restart cannot delete
	// its successor's file.
	DesktopPID int `json:"desktop_pid,omitempty"`
}

// UserHome is the home whose agent tools and client configs AstrLink
// manages. Every AstrLink on this computer shares it.
func UserHome() (string, error) {
	if value := os.Getenv("ASTRLINK_AGENT_HOME"); value != "" {
		return value, nil
	}
	for _, name := range []string{"HOME", "USERPROFILE"} {
		if value, ok := os.LookupEnv(name); ok {
			return value, nil
		}
	}
	return "", errors.New("unable to resolve the user home directory")
}

// SessionPath is the control-session.json of home.
func SessionPath(home string) string {
	return filepath.Join(home, ".astrlink", "control-session.json")
}

// ControlSocketPath is the control socket of a data directory.
func ControlSocketPath(dataDirectory string) string {
	return filepath.Join(dataDirectory, ControlSocketFileName)
}

// PublishSession writes the locator agent tools use to reach Core. Only the
// observer token is accepted here, so the operator token cannot reach the
// file.
func PublishSession(home, dataDirectory, controlURL, observerToken string, pid int) error {
	return writePrivateJSON(SessionPath(home), sessionFile(runtime.GOOS == "windows", dataDirectory, controlURL, observerToken, pid))
}

func sessionFile(windows bool, dataDirectory, controlURL, observerToken string, pid int) SessionFile {
	if windows {
		return SessionFile{
			SchemaVersion: SessionSchemaVersion,
			ControlURL:    controlURL,
			ControlToken:  observerToken,
			PID:           pid,
			DesktopPID:    os.Getpid(),
		}
	}
	return SessionFile{
		SchemaVersion: SessionSchemaVersion,
		ControlSocket: ControlSocketPath(dataDirectory),
		PID:           pid,
		DesktopPID:    os.Getpid(),
	}
}

// ClearSession removes the session file unless another desktop process
// published it. During a development restart the old instance shuts down
// after the new one has already announced itself; deleting here would leave
// the agent CLI without a locator until the next full launch.
func ClearSession(home string) error { return clearSessionFor(home, os.Getpid()) }

func clearSessionFor(home string, currentPID int) error {
	path := SessionPath(home)
	if raw, err := os.ReadFile(path); err == nil {
		var session SessionFile
		if json.Unmarshal(raw, &session) == nil && session.DesktopPID != 0 && session.DesktopPID != currentPID {
			return nil
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("unable to remove control session: %w", err)
	}
	return nil
}

func writePrivateJSON(path string, value any) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("unable to create %s: %w", parent, err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(parent, 0o700)
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("unable to serialize control session: %w", err)
	}
	temporary := path + ".tmp"
	if ext := filepath.Ext(path); ext != "" {
		temporary = path[:len(path)-len(ext)] + ".json.tmp"
	}
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("unable to write control session: %w", err)
	}
	_, err = file.Write(append(payload, '\n'))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("unable to write control session: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("unable to replace control session: %w", err)
	}
	return nil
}
