package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	_ "github.com/QuantumNous/astrlink/desktop/internal/clientconfig/clients"
	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/core"
	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/instance"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
	"github.com/QuantumNous/astrlink/desktop/internal/proxycheck"
	"github.com/QuantumNous/astrlink/desktop/internal/wsl"
)

// registerClientCommands binds the services that write client configs and
// check the client proxy (src/native-host/commands-clients.ts), and keeps
// the configs this gateway wrote on its current address.
func registerClientCommands(h *host.Host) {
	api := &control.API{Caller: h}
	gateway := clientGateway{host: h, tokens: control.Tokens{API: api}, pricing: control.Pricing{API: api}}
	mygo.Bind(Clients{host: h, gateway: gateway, writeClipboard: h.WriteClipboard})
	h.OnStarted(func() {
		if syncsClientConfigsAtStartup(h.Instance()) {
			go syncClientConfigs(h, gateway)
		}
	})
}

// syncsClientConfigsAtStartup says whether the instance keeps its client
// configs on its current address on its own. The development instance
// shares the user's client configs with the other AstrLink but never
// repoints them: any rebuild restarts it, and the token check alone has let
// it take the other AstrLink's config. It changes a client only when one is
// set up from its own window.
func syncsClientConfigsAtStartup(i instance.Instance) bool { return !i.Isolated }

// clientGateway is the running gateway client configs are written for:
// Core's session and its control API.
type clientGateway struct {
	host    *host.Host
	tokens  control.Tokens
	pricing control.Pricing
}

func (g clientGateway) Session() (string, string, bool) {
	manager := g.host.CoreManager()
	if manager == nil {
		return "", "", false
	}
	snapshot := manager.Snapshot()
	if snapshot.Phase != core.PhaseReady || snapshot.Ready == nil {
		return "", "", false
	}
	pid := -1
	if snapshot.PID != nil {
		pid = *snapshot.PID
	}
	// Another process, or another ready announcement, is another session.
	identity, _ := json.Marshal(struct {
		PID   int
		Ready core.Ready
	}{pid, *snapshot.Ready})
	return snapshot.Ready.ClientInferenceURL, string(identity), true
}

func (g clientGateway) ListAccessTokens(ctx context.Context) (json.RawMessage, error) {
	return g.tokens.ListAccessTokens(ctx)
}

func (g clientGateway) RevealAccessToken(ctx context.Context, tokenID string) (string, error) {
	return control.RevealAccessToken(ctx, g.host, tokenID)
}

func (g clientGateway) ModelMetadata(ctx context.Context, model string) (json.RawMessage, error) {
	input, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return nil, err
	}
	return g.pricing.Pricing(ctx, "model", nil, input)
}

// Clients writes and removes the config files of the coding clients,
// previews and copies their setups, and checks what reaches the gateway.
type Clients struct {
	host    *host.Host
	gateway cc.Gateway
	// writeClipboard puts text on the system clipboard (Host.WriteClipboard).
	writeClipboard func(text string) error
}

// ClientCatalog is every client the dialog lists, in its order: the ones
// AstrLink writes, then the ones it explains.
func (Clients) ClientCatalog() cc.ClientCatalog { return cc.CatalogOf() }

// ClientConfigStatus is the client configs in the desktop user's home and
// in running WSL distributions. A WSL home that cannot be read is reported
// unchecked.
func (c Clients) ClientConfigStatus(ctx context.Context, inferenceURL *string) (cc.ClientOverview, error) {
	userHome, err := control.UserHome()
	if err != nil {
		return cc.ClientOverview{}, err
	}
	url := ""
	var tokens cc.Tokens
	if inferenceURL != nil {
		url = *inferenceURL
		// Unknown tokens leave AstrLink's configs unattributed.
		tokens, _ = cc.TokensOf(ctx, c.gateway)
	}
	homes, err := agents.FindHomes(userHome, wsl.Running())
	if err != nil {
		return cc.ClientOverview{}, err
	}
	overview := cc.ClientOverview{
		Clients:      []cc.ClientStatus{},
		WSLUnchecked: append([]string{}, homes.Unchecked...),
		WSLLocalhost: wsl.LocalhostShared(homes.Host),
		Installed:    cc.Installed(agents.OpensLinks),
	}
	for _, home := range homes.All() {
		statuses, err := cc.Status(home, url, tokens)
		if err != nil {
			distribution := home.Distribution()
			if distribution == "" {
				return cc.ClientOverview{}, err
			}
			fmt.Fprintf(os.Stderr, "unable to read client configs in WSL %s: %v\n", distribution, err)
			overview.WSLUnchecked = append(overview.WSLUnchecked, distribution)
			continue
		}
		overview.Clients = append(overview.Clients, statuses...)
	}
	return overview, nil
}

// CheckClientProxy reports whether the system proxy keeps Codex from
// reaching the gateway. It only reads, and never changes proxy settings.
func (Clients) CheckClientProxy(ctx context.Context, inferenceURL string) (proxycheck.ProxyCheck, error) {
	return proxycheck.Check(ctx, inferenceURL)
}

// namedHomes is the desktop user's home and the named WSL homes, started
// if stopped.
func namedHomes(names []string) (*agents.Homes, error) {
	userHome, err := control.UserHome()
	if err != nil {
		return nil, err
	}
	return agents.FindHomes(userHome, wsl.Named(names))
}

// clientHome is the home a client config command works on. Naming a WSL
// distribution starts it if it is stopped.
func clientHome(distribution *string) (agents.Home, error) {
	var names []string
	if distribution != nil {
		names = []string{*distribution}
	}
	homes, err := namedHomes(names)
	if err != nil {
		return agents.Home{}, err
	}
	home := cc.HomeOf(homes, distribution)
	if home == nil {
		name := ""
		if distribution != nil {
			name = *distribution
		}
		return agents.Home{}, fmt.Errorf("WSL distribution %s is no longer available", name)
	}
	return *home, nil
}

func validClient(client cc.Client) error {
	if !client.Valid() {
		return errors.New("unknown client")
	}
	return nil
}

// ApplyClientConfigs writes and removes several clients' configs with one
// token. Without confirmed, nothing is written while any of them needs
// confirmation.
func (c Clients) ApplyClientConfigs(ctx context.Context, tokenID *string, inferenceURL string, items []cc.ApplyItem, confirmed bool) (cc.BatchOutcome, error) {
	var names []string
	for _, item := range items {
		if err := validClient(item.Client); err != nil {
			return cc.BatchOutcome{}, err
		}
		if item.WSL != nil {
			names = append(names, *item.WSL)
		}
	}
	slices.Sort(names)
	homes, err := namedHomes(slices.Compact(names))
	if err != nil {
		return cc.BatchOutcome{}, err
	}
	return cc.Apply(ctx, c.gateway, homes, tokenID, inferenceURL, items, confirmed)
}

// PreviewClientConfigChanges is what writing or removing one client's
// config would change, with the token shown as its hint.
func (c Clients) PreviewClientConfigChanges(ctx context.Context, tokenID *string, client cc.Client, target, distribution *string, action cc.Action, models *cc.Models, inferenceURL string) (*cc.Preview, error) {
	if err := validClient(client); err != nil {
		return nil, err
	}
	place, err := cc.NewPlace(client, target)
	if err != nil {
		return nil, err
	}
	var names []string
	if distribution != nil {
		names = []string{*distribution}
	}
	homes, err := namedHomes(names)
	if err != nil {
		return nil, err
	}
	return cc.PreviewChanges(ctx, c.gateway, homes, tokenID, place, distribution, action, models, inferenceURL)
}

// RemoveClientConfig removes AstrLink's config from one client, putting
// back what it replaced.
func (Clients) RemoveClientConfig(client cc.Client, target, distribution *string) error {
	if err := validClient(client); err != nil {
		return err
	}
	place, err := cc.NewPlace(client, target)
	if err != nil {
		return err
	}
	home, err := clientHome(distribution)
	if err != nil {
		return err
	}
	if failed := cc.Remove(home, place); failed != nil {
		return failed
	}
	return nil
}

// PreviewClientConfigSnippet is the files ApplyClientConfigs would write
// for a client that has none, with the token shown as its hint.
func (c Clients) PreviewClientConfigSnippet(ctx context.Context, tokenID string, client cc.Client, models cc.Models, inferenceURL string) ([]cc.SnippetFile, error) {
	return c.snippet(ctx, tokenID, client, &models, inferenceURL, false)
}

// CopyClientConfigSnippet copies one file of the real snippet straight to
// the clipboard, like CopyAccessToken: the token never reaches the webview.
// false means the clipboard refused it.
func (c Clients) CopyClientConfigSnippet(ctx context.Context, tokenID string, client cc.Client, models cc.Models, inferenceURL string, file int) (bool, error) {
	files, err := c.snippet(ctx, tokenID, client, &models, inferenceURL, true)
	if err != nil {
		return false, err
	}
	if file < 0 || file >= len(files) {
		return false, errors.New("invalid snippet file")
	}
	if err := c.writeClipboard(files[file].Contents); err != nil {
		fmt.Fprintf(os.Stderr, "unable to copy the client config: %v\n", err)
		return false, nil
	}
	return true, nil
}

func (c Clients) snippet(ctx context.Context, tokenID string, client cc.Client, models *cc.Models, inferenceURL string, reveal bool) ([]cc.SnippetFile, error) {
	if err := validClient(client); err != nil {
		return nil, err
	}
	userHome, err := control.UserHome()
	if err != nil {
		return nil, err
	}
	return cc.SnippetFiles(ctx, c.gateway, userHome, tokenID, client, models, inferenceURL, reveal)
}

// OpenClientImportLink opens a client's import link with a token, which
// never reaches the webview. The link carries it, so neither it nor the
// opener's error is logged.
func (c Clients) OpenClientImportLink(ctx context.Context, tokenID, client, protocol, inferenceURL string) error {
	origin, err := cc.LocalOrigin(inferenceURL)
	if err != nil {
		return err
	}
	token, err := cc.RevealAccessToken(ctx, c.gateway, tokenID, inferenceURL)
	if err != nil {
		return err
	}
	link, err := cc.ImportLink(client, protocol, origin, token)
	if err != nil {
		return err
	}
	if c.host.OpenURL(link) != nil {
		return fmt.Errorf("unable to open the %s import link", client)
	}
	return nil
}

// CCSwitchInstalled reports whether CC Switch is installed, whose provider
// switches overwrite the configs AstrLink writes.
func (Clients) CCSwitchInstalled() bool { return agents.CCSwitchInstalled() }

// syncClientConfigs keeps client configs this gateway wrote pointed at its
// current address, including a fallback port picked at startup. Reading
// its tokens, which tell its configs from another AstrLink's, needs a ready
// Core. WSL distributions are synced only while running.
func syncClientConfigs(h *host.Host, gateway clientGateway) {
	manager := h.CoreManager()
	if manager == nil {
		return
	}
	synced := ""
	for {
		view, changed := manager.View()
		if view.Phase == core.PhaseReady && view.InferenceURL != nil && *view.InferenceURL != synced {
			synced = *view.InferenceURL
			if err := syncHomes(gateway, synced); err != nil {
				fmt.Fprintf(os.Stderr, "failed to update client configs for the gateway address: %v\n", err)
				if store := h.PreferencesStore(); store != nil {
					store.ReportWarning(prefs.T(store.Values().Locale, "host.clientConfig.syncFailed", "error", err.Error()))
				}
			}
		}
		<-changed
	}
}

func syncHomes(gateway clientGateway, inferenceURL string) error {
	tokens, err := cc.TokensOf(context.Background(), gateway)
	if err != nil {
		return err
	}
	userHome, err := control.UserHome()
	if err != nil {
		return err
	}
	homes, err := agents.FindHomes(userHome, wsl.Running())
	if err != nil {
		return err
	}
	var failures []string
	for _, home := range homes.All() {
		if err := cc.Sync(home, inferenceURL, tokens); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}
