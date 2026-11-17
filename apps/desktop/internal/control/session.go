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
	// GatewayURL is the instance's client inference address, which
	// `astrlink instances` shows. It is not a credential.
	GatewayURL string `json:"gateway_url,omitempty"`
}

// Locator is what a published session tells agent tools about one Core.
type Locator struct {
	DataDirectory string
	ControlURL    string
	// ObserverToken is the observer-role token; the operator token never
	// reaches a session file.
	ObserverToken string
	GatewayURL    string
	PID           int
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

const (
	// SessionFileName is the default instance's session file.
	SessionFileName = "control-session.json"
	// DevSessionFileName is the development (isolated) instance's, beside
	// the default one, so both can run at once.
	DevSessionFileName = "control-session-dev.json"
)

// SessionPath is the default instance's control-session.json of home.
func SessionPath(home string) string { return InstanceSessionPath(home, false) }

// InstanceSessionPath is the session file of the default or the isolated
// instance in home.
func InstanceSessionPath(home string, isolated bool) string {
	name := SessionFileName
	if isolated {
		name = DevSessionFileName
	}
	return filepath.Join(home, ".astrlink", name)
}

// ControlSocketPath is the control socket of a data directory.
func ControlSocketPath(dataDirectory string) string {
	return filepath.Join(dataDirectory, ControlSocketFileName)
}

// PublishSession writes the locator agent tools use to reach Core to path
// (see InstanceSessionPath).
func PublishSession(path string, locator Locator) error {
	return writePrivateJSON(path, sessionFile(runtime.GOOS == "windows", locator))
}

func sessionFile(windows bool, locator Locator) SessionFile {
	session := SessionFile{
		SchemaVersion: SessionSchemaVersion,
		ControlSocket: ControlSocketPath(locator.DataDirectory),
		PID:           locator.PID,
		DesktopPID:    os.Getpid(),
		GatewayURL:    locator.GatewayURL,
	}
	if windows {
		session.ControlSocket = ""
		session.ControlURL, session.ControlToken = locator.ControlURL, locator.ObserverToken
	}
	return session
}

// ClearSession removes the session file at path unless another desktop
// process published it. During a development restart the old instance shuts
// down after the new one has already announced itself; deleting here would
// leave the agent CLI without a locator until the next full launch.
func ClearSession(path string) error { return clearSessionFor(path, os.Getpid()) }

func clearSessionFor(path string, currentPID int) error {
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
