package claudecli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Layout places the per-account config directories and the shared working
// directory. The working directory reaches the model (Claude Code reports it
// in its environment message), so neither path may name the gateway.
type Layout struct {
	// Root holds one CLAUDE_CONFIG_DIR per official account.
	Root string
	// WorkDir is the empty working directory every serving child runs in.
	WorkDir string
	// InstallHome is the HOME the server edition installs the CLI into and
	// runs it with; empty on the desktop, where the user's own HOME is used.
	InstallHome string
}

// NewLayout keeps account directories under dataDir unless its path names the
// gateway (the desktop's app-data directory does), in which case they live in
// the user's config directory.
func NewLayout(dataDir string, serverEdition bool) (Layout, error) {
	if dataDir == "" {
		return Layout{}, fmt.Errorf("data directory is required")
	}
	root := filepath.Join(dataDir, "claude-accounts")
	if namesGateway(dataDir) {
		base, err := os.UserConfigDir()
		if err != nil {
			return Layout{}, fmt.Errorf("locate user config directory: %w", err)
		}
		root = filepath.Join(base, "claude-accounts")
	}
	layout := Layout{Root: root, WorkDir: neutralWorkDir()}
	if serverEdition {
		layout.InstallHome = filepath.Join(dataDir, "claude-cli")
	}
	return layout, nil
}

func namesGateway(path string) bool {
	return strings.Contains(strings.ToLower(path), "astrlink")
}

func neutralWorkDir() string {
	base := os.TempDir()
	if namesGateway(base) {
		base = string(filepath.Separator) + "tmp"
	}
	return filepath.Join(base, "claude-work")
}

// ConfigDir is the CLAUDE_CONFIG_DIR of one account.
func (layout Layout) ConfigDir(id contract.ServiceID) (string, error) {
	if err := id.Validate(); err != nil {
		return "", err
	}
	return filepath.Join(layout.Root, string(id)), nil
}

// EnsureConfigDir creates the account directory, private to the user.
func (layout Layout) EnsureConfigDir(id contract.ServiceID) (string, error) {
	dir, err := layout.ConfigDir(id)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create account directory: %w", err)
	}
	return dir, nil
}

// EnsureWorkDir creates the shared empty working directory.
func (layout Layout) EnsureWorkDir() (string, error) {
	if err := os.MkdirAll(layout.WorkDir, 0o700); err != nil {
		return "", fmt.Errorf("create working directory: %w", err)
	}
	return layout.WorkDir, nil
}

// RemoveConfigDir deletes an account's directory after sign-out.
func (layout Layout) RemoveConfigDir(id contract.ServiceID) error {
	dir, err := layout.ConfigDir(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove account directory: %w", err)
	}
	return nil
}

// Home is the HOME a child runs with: the install home on the server edition,
// otherwise the user's own (empty keeps the inherited value).
func (layout Layout) Home() string {
	return layout.InstallHome
}
