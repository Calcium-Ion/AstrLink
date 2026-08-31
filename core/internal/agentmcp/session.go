package agentmcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	sessionSchemaVersion = 1
	sessionFileName      = "control-session.json"
)

// SessionFile is the 0600 locator written by the desktop when Core is ready.
// Unix deployments set ControlSocket and omit the token. Windows may set
// ControlURL plus ControlToken because it has no local control socket.
type SessionFile struct {
	SchemaVersion int    `json:"schema_version"`
	ControlSocket string `json:"control_socket,omitempty"`
	ControlURL    string `json:"control_url,omitempty"`
	ControlToken  string `json:"control_token,omitempty"`
	PID           int    `json:"pid,omitempty"`
}

func DefaultSessionPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ASTRLINK_CONTROL_SESSION")); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".astrlink", sessionFileName), nil
}

func LoadSessionFile(path string) (SessionFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return SessionFile{}, fmt.Errorf("read control session: %w", err)
	}
	var session SessionFile
	if err := json.Unmarshal(raw, &session); err != nil {
		return SessionFile{}, fmt.Errorf("parse control session: %w", err)
	}
	if session.SchemaVersion != sessionSchemaVersion {
		return SessionFile{}, fmt.Errorf("unsupported control session schema %d", session.SchemaVersion)
	}
	if strings.TrimSpace(session.ControlSocket) == "" && strings.TrimSpace(session.ControlURL) == "" {
		return SessionFile{}, fmt.Errorf("control session has neither control_socket nor control_url")
	}
	return session, nil
}
