package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientupdates"
	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/imports"
	"github.com/QuantumNous/astrlink/desktop/internal/instance"
	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
	"github.com/QuantumNous/astrlink/desktop/internal/realpath"
	"github.com/QuantumNous/astrlink/desktop/internal/ui"
	"github.com/QuantumNous/astrlink/desktop/internal/wsl"
)

// registerAgentCommands binds the services of agent tools, local and link
// imports, local client updates and service terminals
// (src/native-host/commands-agents.ts). Once the host started, astrlink://
// links reach the provider import, and the instance that is not isolated
// refreshes the agent tools AstrLink installed.
func registerAgentCommands(h *host.Host) {
	api := &control.API{Caller: h}
	services := control.Services{API: api}
	links := imports.NewProviderImports(instance.FromEnvironment().LinkScheme())
	clients := clientupdates.NewManager(
		func() bool { return preferences(h).UseSystemProxy },
		func(snapshot clientupdates.ClientSnapshot) {
			if err := host.LocalClientStatus.Broadcast(snapshot); err != nil {
				log.Printf("astrlink: unable to publish the local client status: %v", err)
			}
		})
	gateway := clientGateway{host: h, tokens: control.Tokens{API: api}, pricing: control.Pricing{API: api}}
	mygo.Bind(
		AgentTools{host: h},
		ProviderImport{imports: links, services: services},
		LocalImport{host: h, imports: imports.NewLocalImports(), services: services},
		LocalClients{manager: clients},
		Terminal{host: h, gateway: gateway},
	)
	h.OnStarted(func() {
		// The main window is up, so a link, even the launch one, shows its
		// confirmation there.
		ui.ReceiveLinks(func(link string) { receiveProviderLink(h, links, link) })
		// The development instance shares the user's agent tools with the
		// other AstrLink. Only that one refreshes them at startup, so a
		// development instance's skills and CLI do not replace its copies.
		if !h.Instance().Isolated {
			go syncAgentInstalls(h)
		}
	})
}

func preferences(h *host.Host) prefs.Preferences {
	if store := h.PreferencesStore(); store != nil {
		return store.Values()
	}
	return prefs.Preferences{}
}

// ---- Agent tools ----

// AgentTools installs AstrLink's skills, CLI access and guards for the
// agent tools in the desktop user's home and in WSL homes.
type AgentTools struct{ host *host.Host }

// AgentDebugStatus is what is installed for every tool. Stopped WSL
// distributions are listed unchecked; checking never starts one.
func (a AgentTools) AgentDebugStatus() (agents.AgentInstallStatus, error) {
	installs, unchecked, err := agentInstallContext(a.host, wsl.Running())
	if err != nil {
		return agents.AgentInstallStatus{}, err
	}
	status := agents.Status(installs)
	status.WSLUnchecked = append([]string{}, unchecked...)
	return status, nil
}

// InstallAgentDebug installs skillIDs for tools.
func (a AgentTools) InstallAgentDebug(skillIDs []agents.AgentSkillID, tools []agents.AgentToolTarget) (agents.InstallReceipt, error) {
	var distributions []string
	for _, tool := range tools {
		if tool.WSL != nil {
			distributions = append(distributions, *tool.WSL)
		}
	}
	installs, _, err := agentInstallContext(a.host, wsl.Named(distributions))
	if err != nil {
		return agents.InstallReceipt{}, err
	}
	return agents.Install(installs, skillIDs, tools)
}

// UninstallAgentDebug removes every install. It also reaches recorded WSL
// distributions that are stopped, starting them, so no skill is left
// pointing at the removed CLI.
func (a AgentTools) UninstallAgentDebug() error {
	home, err := control.UserHome()
	if err != nil {
		return err
	}
	installs, _, err := agentInstallContext(a.host, wsl.RunningAnd(agents.RecordedWSL(home)))
	if err != nil {
		return err
	}
	return agents.Uninstall(installs)
}

// agentInstallContext is the homes agent installs work on: the desktop
// user's, and the WSL distributions reach selects. Tool directories follow
// CC Switch's settings. It also returns the distributions left unchecked.
func agentInstallContext(h *host.Host, reach wsl.Reach) (*agents.InstallContext, []string, error) {
	home, err := control.UserHome()
	if err != nil {
		return nil, nil, err
	}
	homes, err := agents.FindHomes(home, reach)
	if err != nil {
		return nil, nil, err
	}
	// Install reports a missing sidecar itself; status and uninstall do not
	// need it.
	resources, _ := mygo.App.Path(mygo.PathResources)
	cli, _ := agents.ResolveSidecar(resources, "astrlink-cli")
	data, pins := guardedPaths(h.Directories())
	return &agents.InstallContext{
		Home:            homes.Host,
		CLISource:       cli,
		DataDirectories: data,
		RawKeyPins:      pins,
		Dirs:            homes.Dirs,
		WSL:             homes.WSL,
	}, homes.Unchecked, nil
}

// guardedPaths is what agent guards keep agents out of: the data directory
// and raw key pin file of every AstrLink instance, so either one writes and
// checks the same rules in the agent tools they share.
func guardedPaths(dirs instance.Directories) (data, pins []string) {
	if dirs.Data != "" {
		data = instance.EveryInstanceDirectory(dirs.Data)
	}
	if dirs.Config != "" {
		for _, directory := range instance.EveryInstanceDirectory(dirs.Config) {
			pins = append(pins, keystore.PinFile(directory))
		}
	}
	return data, pins
}

// syncAgentInstalls keeps installed agent skills, guards and the CLI
// current. Recorded WSL distributions sync after the desktop user's home,
// and only while running; starting AstrLink never starts a distribution.
func syncAgentInstalls(h *host.Host) {
	syncHomes := func(installs *agents.InstallContext, wslOnly bool) {
		for _, target := range installs.Homes() {
			distribution := target.Distribution()
			if wslOnly && distribution == "" {
				continue
			}
			place := ""
			if distribution != "" {
				place = " in WSL " + distribution
			}
			if err := agents.SyncInstalledSkills(target); err != nil {
				log.Printf("astrlink: failed to sync AstrLink agent skills%s: %v", place, err)
			}
			if err := agents.SyncInstalledHostGuards(target, installs.DataDirectories, installs.RawKeyPins); err != nil {
				log.Printf("astrlink: failed to sync AstrLink agent host guards%s: %v", place, err)
			}
		}
	}
	installs, _, err := agentInstallContext(h, wsl.Named(nil))
	if err != nil {
		log.Printf("astrlink: failed to sync AstrLink agent installs: %v", err)
		return
	}
	syncHomes(installs, false)
	// Runs without a sidecar too, so the MCP migration still happens.
	if err := agents.SyncInstalledCLI(installs); err != nil {
		log.Printf("astrlink: failed to sync AstrLink agent CLI: %v", err)
	}
	distributions := agents.RecordedWSL(installs.Home)
	if len(distributions) == 0 {
		return
	}
	installs, _, err = agentInstallContext(h, wsl.RunningAmong(distributions))
	if err != nil {
		log.Printf("astrlink: failed to reach WSL for AstrLink agent installs: %v", err)
		return
	}
	// The desktop user's home synced above.
	syncHomes(installs, true)
}

// ---- Provider import links ----

// ProviderImport confirms the provider an astrlink:// link describes.
type ProviderImport struct {
	imports  *imports.ProviderImports
	services control.Services
}

// PendingProviderImport is the link waiting for the user, if any.
func (p ProviderImport) PendingProviderImport() *imports.Notice { return p.imports.Pending() }

// DismissProviderImport drops the link id and its key.
func (p ProviderImport) DismissProviderImport(id string) { p.imports.Dismiss(id) }

// ConfirmProviderImport creates the linked provider from what the dialog
// filled in.
func (p ProviderImport) ConfirmProviderImport(ctx context.Context, id string, input json.RawMessage) (control.ServiceRecord, error) {
	return p.imports.Confirm(ctx, p.services, id, input)
}

// receiveProviderLink accepts a link the OS opened and brings the
// confirmation to the front.
func receiveProviderLink(h *host.Host, links *imports.ProviderImports, link string) {
	notice, err := links.Accept(link)
	if err != nil {
		log.Printf("astrlink: unable to accept provider import link: %v", err)
		return
	}
	ui.ShowMainWindow()
	// A main window built again asks for the pending link once it loads.
	if w := h.MainWindow(); w != nil {
		if err := host.ProviderImport.Emit(w, notice); err != nil {
			log.Printf("astrlink: unable to show provider import link: %v", err)
		}
	}
}

// ---- Local import ----

// LocalImport imports the providers the agent tools on this computer
// already use.
type LocalImport struct {
	host     *host.Host
	imports  *imports.LocalImports
	services control.Services
}

// LocalImportSources locates the tools' config files without opening any
// of them, and starts a new import that replaces any earlier one.
func (l LocalImport) LocalImportSources() (imports.Opened, error) {
	home, err := control.UserHome()
	if err != nil {
		return imports.Opened{}, err
	}
	homes, err := agents.FindHomes(home, wsl.Running())
	if err != nil {
		return imports.Opened{}, err
	}
	return l.imports.Open(homes)
}

// ReadLocalImport reads the checked sources. The result names keys only by
// hint.
func (l LocalImport) ReadLocalImport(session string, sources []string) (imports.ReadResult, error) {
	var ports []uint16
	if manager := l.host.CoreManager(); manager != nil {
		if ready := manager.Snapshot().Ready; ready != nil {
			ports = imports.GatewayPorts(ready.InferenceURL, ready.ClientInferenceURL)
		}
	}
	return l.imports.Read(session, sources, ports)
}

// ProbeLocalImportModels is the one model-list request a found provider
// gets before it is created.
func (l LocalImport) ProbeLocalImportModels(ctx context.Context, session, candidate string, input json.RawMessage) (json.RawMessage, error) {
	return l.imports.Probe(ctx, l.services, session, candidate, input)
}

// ConfirmLocalImport creates a found provider with the key the read found.
func (l LocalImport) ConfirmLocalImport(ctx context.Context, session, candidate string, input json.RawMessage) (control.ServiceRecord, error) {
	return l.imports.Confirm(ctx, l.services, session, candidate, input)
}

// DismissLocalImport ends the import and drops every key it read.
func (l LocalImport) DismissLocalImport(session string) { l.imports.Dismiss(session) }

// ---- Local client updates ----

// LocalClients checks and updates the AI clients installed on this
// computer and in WSL.
type LocalClients struct{ manager *clientupdates.Manager }

// LocalClientStatus is the latest status.
func (l LocalClients) LocalClientStatus() clientupdates.ClientSnapshot { return l.manager.Snapshot() }

// RefreshLocalClients checks every client for updates in the background.
func (l LocalClients) RefreshLocalClients() (clientupdates.ClientSnapshot, error) {
	return l.manager.Refresh()
}

// UpdateLocalClients updates targets in the background.
func (l LocalClients) UpdateLocalClients(targets []clientupdates.ClientTarget) (clientupdates.ClientSnapshot, error) {
	return l.manager.Update(targets)
}

// RepairLocalClients puts back or reinstalls targets whose installation no
// longer starts.
func (l LocalClients) RepairLocalClients(targets []clientupdates.ClientTarget) (clientupdates.ClientSnapshot, error) {
	return l.manager.Repair(targets)
}

// ---- Service terminal ----

// Terminal opens a terminal running Claude Code or Codex through one
// provider.
type Terminal struct {
	host    *host.Host
	gateway clientGateway
}

// ServiceTerminalRequest is the terminal the service page asks for.
type ServiceTerminalRequest struct {
	ServiceID    string                `json:"service_id"`
	ServiceName  string                `json:"service_name"`
	Client       agents.TerminalClient `json:"client"`
	Directory    string                `json:"directory"`
	Model        *string               `json:"model"`
	Terminal     agents.TerminalApp    `json:"terminal"`
	InferenceURL string                `json:"inference_url"`
}

// ListTerminalApps lists the terminals installed here.
func (Terminal) ListTerminalApps() []agents.TerminalChoice { return agents.InstalledTerminals() }

// PickTerminalDirectory asks for the folder a terminal opens in, starting
// at start when it is one. It returns nil when the operator canceled.
func (Terminal) PickTerminalDirectory(start *string) (*string, error) { return pickFolder(start) }

// pickFolder asks for a folder, starting at start when it is one. It
// returns nil when the operator canceled.
func pickFolder(start *string) (*string, error) {
	options := mygo.OpenDialogOptions{Directory: true}
	if start != nil {
		if info, err := os.Stat(*start); err == nil && info.IsDir() {
			options.DefaultPath = *start
		}
	}
	paths, err := mygo.Dialog.Open(options)
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	return &paths[0], nil
}

// OpenServiceTerminal opens request.Terminal in request.Directory running
// the client through one provider. The session uses the token the client
// is set up with here, or the first token when AstrLink has not set the
// client up.
func (t Terminal) OpenServiceTerminal(ctx context.Context, request ServiceTerminalRequest) error {
	session, err := t.session(ctx, request)
	if err != nil {
		return err
	}
	locale := preferences(t.host).Locale
	session.Banner, session.Exited = terminalMessages(locale, request)
	return terminalError(locale, request.Terminal, agents.OpenTerminal(request.Terminal, session))
}

// terminalError explains that macOS refused Apple events to a terminal the
// user has not allowed AstrLink to control.
func terminalError(locale prefs.Locale, terminal agents.TerminalApp, err error) error {
	if err != nil && strings.Contains(err.Error(), "(-1743)") {
		return errors.New(prefs.T(locale, "host.terminal.automationDenied", "terminal", terminal.Name()))
	}
	return err
}

func terminalMessages(locale prefs.Locale, request ServiceTerminalRequest) (banner, exited string) {
	vars := []string{"client", request.Client.Label(), "service", request.ServiceName}
	return prefs.T(locale, "host.terminal.banner", vars...), prefs.T(locale, "host.terminal.exited", vars...)
}

// session checks request and reveals the token the terminal's client
// sends.
func (t Terminal) session(ctx context.Context, request ServiceTerminalRequest) (*agents.TerminalSession, error) {
	if !agents.ValidServiceID(request.ServiceID) {
		return nil, errors.New("invalid API provider ID")
	}
	client, ok := cc.Find(string(request.Client))
	if !ok || request.Client.Label() == "" {
		return nil, errors.New("unknown client")
	}
	model, err := agents.ChosenModel(request.Model)
	if err != nil {
		return nil, err
	}
	origin, err := cc.LocalOrigin(request.InferenceURL)
	if err != nil {
		return nil, err
	}
	directory, ok := folder(request.Directory)
	if !ok {
		return nil, fmt.Errorf("%s is not a folder", request.Directory)
	}
	// This AstrLink's own home, where it records the clients it set up.
	home, err := control.UserHome()
	if err != nil {
		return nil, err
	}
	configured, err := cc.ConfiguredToken(agents.Local(home), cc.Place{Client: client})
	if err != nil {
		return nil, err
	}
	page, err := t.gateway.ListAccessTokens(ctx)
	if err != nil {
		return nil, err
	}
	tokenID, err := terminalTokenID(page, configured)
	if err != nil {
		return nil, err
	}
	token, err := cc.RevealAccessToken(ctx, t.gateway, tokenID, request.InferenceURL)
	if err != nil {
		return nil, err
	}
	if !agents.ValidTerminalToken(token) {
		return nil, errors.New("access token is unavailable")
	}
	return &agents.TerminalSession{
		Client:    request.Client,
		Origin:    origin,
		Token:     token,
		ServiceID: request.ServiceID,
		Model:     model,
		Directory: directory,
	}, nil
}

// folder is path made absolute with its links resolved, when it is a
// directory.
func folder(path string) (string, bool) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	resolved, err := realpath.Resolve(absolute)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(resolved)
	return resolved, err == nil && info.IsDir()
}

// terminalTokenID is the configured token when the page still lists it,
// otherwise the page's first.
func terminalTokenID(page json.RawMessage, configured *string) (string, error) {
	var tokens struct {
		Items any `json:"items"`
	}
	_ = json.Unmarshal(page, &tokens)
	items, _ := tokens.Items.([]any)
	var ids []string
	for _, item := range items {
		object, _ := item.(map[string]any)
		if id, ok := object["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	switch {
	case configured != nil && slices.Contains(ids, *configured):
		return *configured, nil
	case len(ids) > 0:
		return ids[0], nil
	}
	return "", errors.New("create an access token first")
}
