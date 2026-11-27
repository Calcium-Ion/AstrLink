package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/QuantumNous/new-api/pluginkit"
)

// A plugin may read the balance of its providers' accounts through
// buildBalanceRequest and parseBalance, and keep them up with scheduled,
// which runs now and then for each provider while the gateway runs. The
// provider page shows the balance; a provider a plugin says cannot serve
// now rests until it says it can, and keeps the user's own switch as it
// is.

// Bounds of the balance and the maintenance of providers.
const (
	maxBalanceBody      = 1 << 20
	scheduledRetry      = 15 * time.Minute
	scheduledFirstSpan  = 10 * time.Minute
	scheduledSlots      = 4
	maintenanceInterval = time.Minute
)

// Balance is a provider account's balance, as the plugin read it.
type Balance struct {
	Amount    *float64        `json:"amount,omitempty"`
	Currency  string          `json:"currency,omitempty"`
	Unlimited bool            `json:"unlimited,omitempty"`
	Windows   []BalanceWindow `json:"windows,omitempty"`
}

// BalanceWindow is a quota window: what is used of the limit, and when it
// resets.
type BalanceWindow struct {
	Name    pluginkit.LocalizedText `json:"name"`
	Used    float64                 `json:"used"`
	Limit   float64                 `json:"limit"`
	Unit    string                  `json:"unit,omitempty"`
	ResetAt int64                   `json:"reset_at,omitempty"`
}

// balanceOf is a balance pluginkit read as the provider page keeps it.
func balanceOf(result pluginkit.BalanceResult) *Balance {
	balance := &Balance{Amount: result.Amount, Currency: result.Currency, Unlimited: result.Unlimited}
	for _, window := range result.Windows {
		balance.Windows = append(balance.Windows, BalanceWindow{Name: window.Name, Used: window.Used, Limit: window.Limit, Unit: window.Unit, ResetAt: window.ResetAt})
	}
	return balance
}

// ProviderState is what the provider page shows of a plugin's provider
// beside its settings: its balance, when it was read, the last error, and
// whether the plugin rests it and why.
type ProviderState struct {
	Balance   *Balance  `json:"balance,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	Error     string    `json:"error,omitempty"`
	Resting   bool      `json:"resting,omitempty"`
	Notice    string    `json:"notice,omitempty"`
}

// providerStates keeps the state of each plugin's provider in memory.
type providerStates struct {
	mutex  sync.Mutex
	states map[contract.ServiceID]*ProviderState
	next   map[contract.ServiceID]time.Time
	busy   map[contract.ServiceID]bool
}

func (states *providerStates) update(id contract.ServiceID, change func(*ProviderState)) {
	states.mutex.Lock()
	defer states.mutex.Unlock()
	if states.states == nil {
		states.states = map[contract.ServiceID]*ProviderState{}
	}
	state, ok := states.states[id]
	if !ok {
		state = &ProviderState{}
		states.states[id] = state
	}
	change(state)
}

func (states *providerStates) get(id contract.ServiceID) ProviderState {
	states.mutex.Lock()
	defer states.mutex.Unlock()
	if state, ok := states.states[id]; ok {
		copied := *state
		return copied
	}
	return ProviderState{}
}

// resting reports whether a plugin rests the provider, and why.
func (host *Host) resting(id contract.ServiceID) (bool, string) {
	state := host.providers.get(id)
	return state.Resting, state.Notice
}

// providerCall is what the balance and maintenance hooks of one provider
// read.
type providerCall struct {
	service contract.Service
	target  *plugin.Plugin
	meta    *plugin.NewAPIPlugin
	relay   *relayTarget
	baseURL string
	base    *url.URL
	key     string
}

// providerCallFor readies the hooks of a plugin's provider: its plugin,
// address, settings and key or credential.
func (host *Host) providerCallFor(ctx context.Context, service contract.Service) (*providerCall, error) {
	if host == nil || host.plugins == nil || service.Plugin == nil || service.Plugin.Provider != contract.PluginProviderRelay {
		return nil, errProviderUnavailable
	}
	target := host.plugins.Current().Plugin(service.Plugin.Key)
	if target == nil || !target.Enabled() || target.NewAPI() == nil {
		return nil, errProviderUnavailable
	}
	meta := target.NewAPI()
	call := &providerCall{service: service, target: target, meta: meta,
		relay: &relayTarget{plugin: target, meta: meta, config: RelayConfig(meta, service.Plugin.Settings)}}
	if service.HTTP != nil {
		call.baseURL = strings.TrimRight(service.HTTP.BaseURL, "/")
	}
	if call.baseURL == "" {
		call.baseURL = meta.Meta.BaseURL
	}
	call.base, _ = url.Parse(call.baseURL)
	if host.credentials != nil {
		if secret, err := host.credentials.ReadServiceSecret(ctx, service); err == nil {
			call.key = string(secret)
		}
	}
	key, err := host.useCredential(ctx, service, call.relay, call.baseURL, call.key, false)
	if err != nil {
		return nil, err
	}
	call.key = key
	if failed, err := host.exchange(ctx, service, call.relay, call.base, key, false); failed != nil || err != nil {
		if err == nil {
			_ = failed.Body.Close()
			err = fmt.Errorf("the plugin refused the key with %d", failed.StatusCode)
		}
		return nil, err
	}
	return call, nil
}

// credentialContext is ctx of the balance and maintenance hooks.
func (call *providerCall) credentialContext() pluginkit.CredentialContext {
	return call.relay.credentialContext(call.baseURL, call.key)
}

// ProviderBalance reads a provider's balance through its plugin and keeps
// it for the provider page.
func (host *Host) ProviderBalance(ctx context.Context, service contract.Service) (ProviderState, error) {
	call, err := host.providerCallFor(ctx, service)
	if err != nil {
		return host.providers.get(service.ID), err
	}
	if !call.target.HasTaskHook("buildBalanceRequest") {
		return host.providers.get(service.ID), errors.New("the provider's plugin reads no balance")
	}
	balance, err := host.readBalance(ctx, call)
	host.providers.update(service.ID, func(state *ProviderState) {
		state.UpdatedAt = time.Now()
		if err != nil {
			state.Error = truncateRunes(redact(err.Error(), call), 512)
			return
		}
		state.Balance, state.Error = balance, ""
	})
	return host.providers.get(service.ID), err
}

// ProviderStateOf is what the provider page shows of a plugin's provider.
func (host *Host) ProviderStateOf(id contract.ServiceID) ProviderState {
	if host == nil {
		return ProviderState{}
	}
	return host.providers.get(id)
}

func (host *Host) readBalance(ctx context.Context, call *providerCall) (*Balance, error) {
	hookCtx := call.credentialContext()
	request, err := callProvider(ctx, call.relay, "buildBalanceRequest", func(ctx context.Context, loaded *plugin.NewAPIPlugin) (pluginkit.ConvertedRequest, error) {
		return loaded.CallBuildBalanceRequest(ctx, hookCtx)
	})
	if err != nil {
		return nil, fmt.Errorf("buildBalanceRequest: %w", err)
	}
	state := request.State
	for sent := 0; sent <= maxConvertedRequests; sent++ {
		response, err := host.sendBalance(ctx, call, request)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, maxBalanceBody))
		_ = response.Body.Close()
		if err != nil {
			return nil, err
		}
		upstream := pluginkit.UpstreamResponse{Status: response.StatusCode, Headers: firstHeaders(response.Header), Body: body}
		step, err := callProvider(ctx, call.relay, "parseBalance", func(ctx context.Context, loaded *plugin.NewAPIPlugin) (pluginkit.BalanceStep, error) {
			return loaded.CallParseBalance(ctx, hookCtx, state, upstream)
		})
		if err != nil {
			return nil, fmt.Errorf("parseBalance: %w", err)
		}
		if step.State != nil {
			state = step.State
		}
		if step.Next != nil {
			request = *step.Next
			if request.Delay > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(request.Delay):
				}
			}
			continue
		}
		return balanceOf(*step.Balance), nil
	}
	return nil, fmt.Errorf("the balance took more than %d requests", maxConvertedRequests)
}

// sendBalance sends one balance request: a path on the provider's address,
// or an address on its host or the plugin's allowed hosts, with the
// credential and signRequest.
func (host *Host) sendBalance(ctx context.Context, call *providerCall, request pluginkit.ConvertedRequest) (*http.Response, error) {
	if call.base == nil || call.base.Host == "" {
		return nil, errors.New("the provider has no address")
	}
	address := call.base
	switch {
	case request.URL != "":
		parsed, err := url.Parse(request.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil ||
			(!strings.EqualFold(parsed.Host, call.base.Host) && !containsFold(call.meta.Meta.AllowedHosts, parsed.Host)) {
			return nil, errors.New("the balance request addresses a host the plugin may not reach")
		}
		address = parsed
	case request.Path != "":
		joined, err := joinPath(call.base, request.Path)
		if err != nil {
			return nil, err
		}
		address = joined
	}
	body := request.Body
	method := request.Method
	if method == "" {
		method = http.MethodGet
		if len(body) > 0 {
			method = http.MethodPost
		}
	}
	query := address.Query()
	credentials, _ := call.relay.authFor("", query)
	header := http.Header{}
	for name, value := range request.Headers {
		if validHeader(name, value) && http.CanonicalHeaderKey(name) != "Host" {
			header.Set(name, value)
		}
	}
	if len(body) > 0 {
		contentType := request.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		header.Set("Content-Type", contentType)
	}
	final := *address
	final.RawQuery = query.Encode()
	if call.target.HasTaskHook("signRequest") {
		signing := header.Clone()
		for name, values := range credentials {
			signing[name] = values
		}
		headers, extra, err := call.relay.sign(ctx, call.baseURL, call.key, method, &final, signing, body)
		if err != nil {
			return nil, err
		}
		for name, value := range headers {
			credentials.Set(name, value)
		}
		for name, value := range extra {
			query.Set(name, value)
		}
		final.RawQuery = query.Encode()
	}
	outbound, err := http.NewRequestWithContext(ctx, method, (&url.URL{Path: final.Path, RawQuery: final.RawQuery}).String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	outbound.Header = header
	target := transport.Target{Service: call.service, ProxyCredentials: host.proxyCredentials, BaseURL: &url.URL{Scheme: final.Scheme, Host: final.Host}, RequestHeaders: credentials}
	send := host.send
	if send == nil {
		send = transport.New(nil).RoundTrip
	}
	return send(outbound, target)
}

func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

// redact takes the key and the credential's values out of a message.
func redact(message string, call *providerCall) string {
	if call.key != "" && len(call.key) >= 4 {
		message = strings.ReplaceAll(message, call.key, "***")
	}
	if call.relay.credential != nil {
		for _, value := range call.relay.credential.Headers {
			if len(value) >= 4 {
				message = strings.ReplaceAll(message, value, "***")
			}
		}
	}
	return message
}

// ServiceLister lists the gateway's providers, for maintenance to find
// those of plugins that export scheduled.
type ServiceLister func(ctx context.Context) ([]contract.Service, error)

// StartMaintenance runs the plugins' scheduled hook for each of their
// providers while the gateway runs: a new provider's first run comes at a
// random time within 10 minutes, the next when the hook says, an hour by
// default, and 15 minutes after a failure.
func (host *Host) StartMaintenance(ctx context.Context, services ServiceLister) {
	if host == nil || services == nil {
		return
	}
	slots := make(chan struct{}, scheduledSlots)
	go func() {
		ticker := time.NewTicker(maintenanceInterval)
		defer ticker.Stop()
		for {
			host.maintain(ctx, services, slots)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// maintain runs the scheduled hooks that are due.
func (host *Host) maintain(ctx context.Context, services ServiceLister, slots chan struct{}) {
	list, err := services(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	for _, service := range list {
		if !service.Enabled || service.Plugin == nil || service.Plugin.Provider != contract.PluginProviderRelay {
			continue
		}
		target := host.plugins.Current().Plugin(service.Plugin.Key)
		if target == nil || !target.Enabled() || !target.HasTaskHook("scheduled") {
			continue
		}
		host.providers.mutex.Lock()
		if host.providers.next == nil {
			host.providers.next = map[contract.ServiceID]time.Time{}
			host.providers.busy = map[contract.ServiceID]bool{}
		}
		due, known := host.providers.next[service.ID]
		if !known {
			host.providers.next[service.ID] = now.Add(time.Duration(rand.Int64N(int64(scheduledFirstSpan))))
		}
		run := known && !now.Before(due) && !host.providers.busy[service.ID]
		if run {
			host.providers.busy[service.ID] = true
		}
		host.providers.mutex.Unlock()
		if !run {
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			host.providers.mutex.Lock()
			host.providers.busy[service.ID] = false
			host.providers.mutex.Unlock()
			continue
		}
		go func(service contract.Service) {
			defer func() { <-slots }()
			next := host.RunScheduled(ctx, service)
			host.providers.mutex.Lock()
			host.providers.next[service.ID] = time.Now().Add(next)
			host.providers.busy[service.ID] = false
			host.providers.mutex.Unlock()
		}(service)
	}
}

// RunScheduled runs a provider's scheduled hook once and returns when it
// runs next.
func (host *Host) RunScheduled(ctx context.Context, service contract.Service) time.Duration {
	call, err := host.providerCallFor(ctx, service)
	if err != nil {
		return scheduledRetry
	}
	resting, _ := host.resting(service.ID)
	scheduled := pluginkit.ScheduledContext{CredentialContext: call.credentialContext(), Reason: "timer", KeyStatus: "enabled"}
	if resting {
		scheduled.KeyStatus = "auto_disabled"
	}
	result, err := callCredential(ctx, call.target, "scheduled", call.baseURL, func(ctx context.Context, loaded *plugin.NewAPIPlugin) (pluginkit.ScheduledResult, error) {
		return loaded.CallScheduled(ctx, scheduled)
	})
	if err != nil {
		host.logf("plugin %s: scheduled for %s failed", call.target.Key, service.ID)
		return scheduledRetry
	}
	if result.Credential != nil && call.relay.credential != nil {
		updated := *call.relay.credential
		updated.Data = result.Credential
		host.saveCredential(ctx, service.ID, updated)
	}
	var balance *Balance
	if result.Balance != nil {
		balance = balanceOf(*result.Balance)
	}
	notice := localizedIn(result.Notify, host.Settings().Language)
	host.providers.update(service.ID, func(state *ProviderState) {
		if balance != nil {
			state.Balance, state.UpdatedAt, state.Error = balance, time.Now(), ""
		}
		switch result.KeyStatus {
		case "auto_disabled":
			state.Resting = true
		case "enabled":
			state.Resting = false
		}
		if notice != "" {
			state.Notice = notice
		} else if !state.Resting {
			state.Notice = ""
		}
	})
	if notice != "" {
		host.logf("plugin %s about %s: %s", call.target.Key, service.Name, notice)
	}
	next := result.Next
	return next
}
