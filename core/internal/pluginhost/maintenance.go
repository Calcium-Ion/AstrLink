package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/transport"
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
	maxBalanceWindows   = 32
	scheduledDefault    = time.Hour
	scheduledMin        = time.Minute
	scheduledMax        = 24 * time.Hour
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
	Name    plugin.LocalizedText `json:"name"`
	Used    float64              `json:"used"`
	Limit   float64              `json:"limit"`
	Unit    string               `json:"unit,omitempty"`
	ResetAt int64                `json:"reset_at,omitempty"`
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
	meta    *plugin.TaskMeta
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
		call.baseURL = meta.BaseURL
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

// view is ctx of the balance and maintenance hooks.
func (call *providerCall) view(state json.RawMessage) map[string]any {
	view := map[string]any{"baseUrl": call.baseURL, "apiKey": call.key, "config": call.relay.config, "credential": nil}
	if call.relay.credential != nil {
		view["credential"] = jsonOrNull(call.relay.credential.Data)
	}
	if call.relay.token != nil {
		view["token"] = call.relay.token
	}
	if state != nil {
		view["state"] = jsonOrNull(state)
	}
	return view
}

// balanceRequest is what buildBalanceRequest or a next returns.
type balanceRequest struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	Body        json.RawMessage   `json:"body"`
	ContentType string            `json:"contentType"`
	State       json.RawMessage   `json:"state"`
	DelayMs     int               `json:"delayMs"`
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
	raw, err := call.relay.call(ctx, "buildBalanceRequest", call.view(json.RawMessage("null")))
	if err != nil {
		return nil, fmt.Errorf("buildBalanceRequest: %w", err)
	}
	var request balanceRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, errors.New("buildBalanceRequest returned no request")
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
		input := map[string]any{"status": response.StatusCode, "headers": firstHeaders(response.Header), "body": strings.ToValidUTF8(string(body), "�")}
		raw, err := call.relay.call(ctx, "parseBalance", call.view(state), input)
		if err != nil {
			return nil, fmt.Errorf("parseBalance: %w", err)
		}
		var result struct {
			Balance
			Next  *balanceRequest `json:"next"`
			State json.RawMessage `json:"state"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, errors.New("parseBalance returned no balance")
		}
		if len(result.State) > 0 {
			state = result.State
		}
		switch {
		case len(result.Error) > 0:
			var text string
			if json.Unmarshal(result.Error, &text) != nil {
				var object struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal(result.Error, &object)
				text = object.Message
			}
			return nil, errors.New(text)
		case result.Next != nil:
			request = *result.Next
			if delay := time.Duration(min(max(request.DelayMs, 0), 10000)) * time.Millisecond; delay > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(delay):
				}
			}
			continue
		}
		return checkBalance(result.Balance)
	}
	return nil, fmt.Errorf("the balance took more than %d requests", maxConvertedRequests)
}

// checkBalance refuses a balance the contract does not allow.
func checkBalance(balance Balance) (*Balance, error) {
	switch {
	case balance.Amount == nil && !balance.Unlimited && len(balance.Windows) == 0:
		return nil, errors.New("the balance has no amount, unlimited or windows")
	case balance.Amount != nil && (math.IsNaN(*balance.Amount) || math.IsInf(*balance.Amount, 0) || len(balance.Currency) != 3):
		return nil, errors.New("an amount is a finite number with a three-letter currency")
	case len(balance.Windows) > maxBalanceWindows:
		return nil, fmt.Errorf("a balance has at most %d windows", maxBalanceWindows)
	}
	for index, window := range balance.Windows {
		if math.IsNaN(window.Used) || math.IsInf(window.Used, 0) || math.IsNaN(window.Limit) || math.IsInf(window.Limit, 0) || len(window.Unit) > 32 {
			return nil, fmt.Errorf("window %d is not finite numbers with a unit of at most 32 characters", index)
		}
	}
	return &balance, nil
}

// UnmarshalJSON reads a window as the plugin writes it.
func (window *BalanceWindow) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name    plugin.LocalizedText `json:"name"`
		Used    float64              `json:"used"`
		Limit   float64              `json:"limit"`
		Unit    string               `json:"unit"`
		ResetAt int64                `json:"resetAt"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*window = BalanceWindow{Name: raw.Name, Used: raw.Used, Limit: raw.Limit, Unit: raw.Unit, ResetAt: raw.ResetAt}
	return nil
}

// sendBalance sends one balance request: a path on the provider's address,
// or an address on its host or the plugin's allowed hosts, with the
// credential and signRequest.
func (host *Host) sendBalance(ctx context.Context, call *providerCall, request balanceRequest) (*http.Response, error) {
	if call.base == nil || call.base.Host == "" {
		return nil, errors.New("the provider has no address")
	}
	address := call.base
	switch {
	case request.URL != "":
		parsed, err := url.Parse(request.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil ||
			(!strings.EqualFold(parsed.Host, call.base.Host) && !containsFold(call.meta.AllowedHosts, parsed.Host)) {
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
	body := convertedBody(request.Body)
	method := strings.ToUpper(request.Method)
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
	view := call.view(nil)
	view["reason"] = "timer"
	view["keyStatus"] = "enabled"
	if resting {
		view["keyStatus"] = "auto_disabled"
	}
	hookCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	raw, _, err := call.target.CallTask(hookCtx, "scheduled", []any{view}, credentialCallOptions(call.meta, call.baseURL))
	if err != nil {
		host.logf("plugin %s: scheduled for %s failed", call.target.Key, service.ID)
		return scheduledRetry
	}
	var result struct {
		Next       *float64        `json:"next"`
		Credential json.RawMessage `json:"credential"`
		Balance    *Balance        `json:"balance"`
		KeyStatus  string          `json:"keyStatus"`
		Notify     json.RawMessage `json:"notify"`
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if err := json.Unmarshal(raw, &result); err != nil {
			return scheduledRetry
		}
	}
	if len(result.Credential) > 0 && call.relay.credential != nil {
		updated := *call.relay.credential
		updated.Data = result.Credential
		host.saveCredential(ctx, service.ID, updated)
	}
	var balance *Balance
	if result.Balance != nil {
		balance, _ = checkBalance(*result.Balance)
	}
	notice := localizedOf(result.Notify, host.Settings().Language)
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
	next := scheduledDefault
	if result.Next != nil && !math.IsNaN(*result.Next) && !math.IsInf(*result.Next, 0) {
		next = time.Duration(*result.Next * float64(time.Second))
	}
	return min(max(next, scheduledMin), scheduledMax)
}
