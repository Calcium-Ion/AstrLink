package claudecli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// InstallScript is Anthropic's official installer, run only when the user asks
// for it in the server edition.
const InstallScript = "curl -fsSL https://claude.ai/install.sh | bash"

const installTimeout = 10 * time.Minute

// ErrInstallUnavailable means this edition does not install the CLI itself.
var ErrInstallUnavailable = errors.New("installing Claude Code is only available in the server edition")

// ErrInstallRunning means an install is already in progress.
var ErrInstallRunning = errors.New("Claude Code is already being installed")

// InstallState is the progress of the last install.
type InstallState struct {
	Installing bool
	Error      string
}

// Installer runs the official installer with HOME set to the install home, so
// the CLI lands in the data volume and survives image updates.
type Installer struct {
	Home string
	// Environ is the parent environment; nil reads os.Environ.
	Environ []string
	// Shell and Script run the installer; empty uses bash and InstallScript.
	Shell  string
	Script string
	// Done is called after every install attempt.
	Done func()

	mu    sync.Mutex
	state InstallState
}

// Available reports whether this edition installs the CLI.
func (installer *Installer) Available() bool {
	return installer != nil && installer.Home != ""
}

// State returns the progress of the last install.
func (installer *Installer) State() InstallState {
	if installer == nil {
		return InstallState{}
	}
	installer.mu.Lock()
	defer installer.mu.Unlock()
	return installer.state
}

// Start runs the installer in the background.
func (installer *Installer) Start() error {
	if !installer.Available() {
		return ErrInstallUnavailable
	}
	installer.mu.Lock()
	if installer.state.Installing {
		installer.mu.Unlock()
		return ErrInstallRunning
	}
	installer.state = InstallState{Installing: true}
	installer.mu.Unlock()
	go installer.run()
	return nil
}

func (installer *Installer) run() {
	err := installer.install()
	installer.mu.Lock()
	installer.state = InstallState{}
	if err != nil {
		installer.state.Error = err.Error()
	}
	installer.mu.Unlock()
	if installer.Done != nil {
		installer.Done()
	}
}

func (installer *Installer) install() error {
	if err := os.MkdirAll(installer.Home, 0o700); err != nil {
		return errors.New("could not create the install directory")
	}
	shell, script := installer.Shell, installer.Script
	if shell == "" {
		shell = "bash"
	}
	if script == "" {
		script = InstallScript
	}
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, shell, "-c", script)
	command.Env = installEnv(installer.Environ, installer.Home)
	command.Dir = installer.Home
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		if line := lastLine(output.String()); line != "" {
			return errors.New("install failed: " + clampMessage(line))
		}
		return errors.New("install failed")
	}
	return nil
}

func lastLine(text string) string {
	lines := strings.Split(escapeSequence.ReplaceAllString(text, ""), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return ""
}

// Tool is the CLI as the control surface reports it: where it is and whether
// this edition can install it.
type Tool struct {
	Locator   *Locator
	Installer *Installer
}

// Status reports the located CLI and the install progress.
func (tool Tool) Status(ctx context.Context) contract.ClaudeCLIStatus {
	status := contract.ClaudeCLIStatus{MinimumVersion: MinimumVersion}
	if tool.Locator != nil {
		binary, err := tool.Locator.Find(ctx)
		switch {
		case err == nil:
			status.Installed, status.Supported = true, true
			status.Path, status.Version = binary.Path, binary.Version
		case errors.Is(err, ErrTooOld):
			status.Installed = true
			status.Path, status.Version = binary.Path, binary.Version
		}
	}
	if tool.Installer.Available() {
		state := tool.Installer.State()
		status.InstallAvailable = true
		status.Installing = state.Installing
		status.InstallError = state.Error
	}
	return status
}

// Install starts the official installer.
func (tool Tool) Install() error {
	return tool.Installer.Start()
}
