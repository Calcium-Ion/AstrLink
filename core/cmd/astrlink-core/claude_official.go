package main

import (
	"context"
	"net"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/claudecli"
	"github.com/QuantumNous/astrlink/core/internal/claudeofficial"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

// officialClaude is the claude CLI runner shared by sign-in and serving.
type officialClaude struct {
	accounts claudecli.Accounts
	tool     claudecli.Tool
	runtime  *claudeofficial.Runtime
}

// enableOfficialClaude lets official Claude accounts sign in through the
// user's claude CLI and serve through it. The server edition also installs
// the CLI on request into its data directory. Without a usable layout
// official accounts stay unavailable and the rest of Core runs on. network
// resolves each account's proxy, which the CLI's own traffic goes through.
func enableOfficialClaude(manager *subscription.Manager, options persistentOptions, network claudecli.Network) (*officialClaude, error) {
	layout, err := claudecli.NewLayout(options.dataDirectory, options.installClaudeCLI)
	if err != nil {
		return nil, err
	}
	locator := claudecli.NewLocator(layout.InstallHome)
	official := &officialClaude{
		accounts: claudecli.Accounts{Locator: locator, Layout: layout, Network: network},
		tool:     claudecli.Tool{Locator: locator},
	}
	if layout.InstallHome != "" {
		official.tool.Installer = &claudecli.Installer{Home: layout.InstallHome}
	}
	official.runtime = claudeofficial.New(claudeofficial.Options{
		Accounts: official.accounts,
		SignedOut: func(id contract.ServiceID) {
			if err := manager.MarkOfficialSignedOut(context.Background(), id); err != nil {
				options.logf("mark official Claude account %s signed out: %v", id, err)
			}
		},
		Usage: manager.ObserveOfficialUsage,
	})
	manager.EnableOfficialClaude(official.accounts)
	manager.SetOfficialStopper(official.runtime.StopAccount)
	return official, nil
}

func (official *officialClaude) controlCLI() controlapi.ClaudeCLI {
	if official == nil {
		return nil
	}
	return official.tool
}

func (official *officialClaude) serving() ingress.ClaudeOfficial {
	if official == nil {
		return nil
	}
	return official.runtime
}

// models lists the models of official accounts; nil when they are
// unavailable.
func (official *officialClaude) models() func(context.Context, contract.ServiceID) ([]string, error) {
	if official == nil {
		return nil
	}
	return official.runtime.Models
}

// listening points the CLI's model calls at the inference listener. A
// listener on every interface, or on loopback, is reached over 127.0.0.1.
func (official *officialClaude) listening(address string) {
	if official == nil {
		return
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return
	}
	if ip := net.ParseIP(host); host == "" || ip == nil || ip.IsUnspecified() || ip.IsLoopback() && ip.To4() != nil {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	official.runtime.SetBaseURL("http://" + host + ":" + port)
}

func (official *officialClaude) close() {
	if official != nil {
		official.runtime.Close()
	}
}
