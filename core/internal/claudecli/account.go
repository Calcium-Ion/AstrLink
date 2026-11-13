package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Status is what `claude auth status --json` reports about one config
// directory. It never contains a token.
type Status struct {
	LoggedIn         bool
	AuthMethod       string
	Email            string
	OrganizationID   string
	OrganizationName string
	SubscriptionType string
}

// AccountID is the stable identity of the signed-in Claude account.
func (status Status) AccountID() string {
	if status.Email == "" {
		return status.OrganizationID
	}
	if status.OrganizationID == "" {
		return status.Email
	}
	return status.OrganizationID + ":" + status.Email
}

// Accounts runs the account commands of the CLI for one config directory.
type Accounts struct {
	Locator *Locator
	Layout  Layout
	// Environ is the parent environment; nil reads os.Environ.
	Environ []string
	// Network routes each claude process's own traffic through its account's
	// proxy; nil uses the gateway's route.
	Network Network
}

// Tunnel opens the proxy tunnel of one claude process of account id.
func (accounts Accounts) Tunnel(id contract.ServiceID) (*Tunnel, error) {
	return StartTunnel(accounts.Network, id)
}

// command prepares an account command; stop closes its tunnel once the
// command has ended.
func (accounts Accounts) command(ctx context.Context, id contract.ServiceID, configDir string, args ...string) (*exec.Cmd, func(), error) {
	binary, err := accounts.Locator.Find(ctx)
	if err != nil {
		return nil, nil, err
	}
	tunnel, err := accounts.Tunnel(id)
	if err != nil {
		return nil, nil, err
	}
	command := exec.CommandContext(ctx, binary.Path, args...)
	env := withEnv(LoginEnv(accounts.Environ, accounts.Layout.Home(), configDir), tunnel.Env())
	command.Env = AddPathDir(env, dirOf(binary.Path))
	command.Dir = configDir
	return command, func() { _ = tunnel.Close() }, nil
}

// Status reads the sign-in state of an account directory.
func (accounts Accounts) Status(ctx context.Context, id contract.ServiceID, configDir string) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command, stop, err := accounts.command(ctx, id, configDir, "auth", "status", "--json")
	if err != nil {
		return Status{}, err
	}
	defer stop()
	var stdout bytes.Buffer
	command.Stdout = &stdout
	runErr := command.Run()
	status, parseErr := ParseStatus(stdout.Bytes())
	if parseErr != nil {
		if runErr != nil {
			return Status{}, fmt.Errorf("claude auth status: %w", runErr)
		}
		return Status{}, parseErr
	}
	// Signed out is reported with exit status 1 and a valid document.
	return status, nil
}

// Logout signs the account directory out through the CLI, which removes the
// login from the Keychain or its config directory.
func (accounts Accounts) Logout(ctx context.Context, id contract.ServiceID, configDir string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command, stop, err := accounts.command(ctx, id, configDir, "auth", "logout")
	if err != nil {
		return err
	}
	defer stop()
	if err := command.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Logging out a signed-out directory fails harmlessly.
			return nil
		}
		return fmt.Errorf("claude auth logout: %w", err)
	}
	return nil
}

// ParseStatus reads the JSON document of `claude auth status --json`.
func ParseStatus(raw []byte) (Status, error) {
	var document map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &document); err != nil || document == nil {
		return Status{}, fmt.Errorf("unrecognized claude auth status output")
	}
	text := func(names ...string) string {
		for _, name := range names {
			if value, ok := document[name].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	loggedIn, _ := document["loggedIn"].(bool)
	status := Status{
		LoggedIn:         loggedIn,
		AuthMethod:       text("authMethod"),
		Email:            text("email", "emailAddress"),
		OrganizationID:   text("orgId", "organizationId", "organizationUuid"),
		OrganizationName: text("orgName", "organizationName"),
		SubscriptionType: text("subscriptionType"),
	}
	return status, nil
}

func dirOf(path string) string {
	index := strings.LastIndexAny(path, `/\`)
	if index <= 0 {
		return "."
	}
	return path[:index]
}
