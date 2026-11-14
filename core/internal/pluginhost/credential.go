package pluginhost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// A provider of a plugin that exports login holds, in place of a key, the
// credential the plugin's login returned: data only the plugin's hooks read,
// the headers and query each request sends, when it expires, and the
// account it is. The gateway keeps it where a key is kept, refreshes it
// through the plugin's refresh before it expires or when the upstream
// rejects it, and has the plugin revoke it when the provider is deleted.

// credentialPrefix marks a provider's secret that is a plugin credential.
const credentialPrefix = "astrlink-plugin-credential:"

// PluginCredential is a credential a plugin's login or refresh returned.
type PluginCredential struct {
	Type       string            `json:"type"`
	Plugin     string            `json:"plugin"`
	Data       json.RawMessage   `json:"data"`
	Headers    map[string]string `json:"headers,omitempty"`
	Query      map[string]string `json:"query,omitempty"`
	ExpiresAt  int64             `json:"expires_at"`
	AccountID  string            `json:"account_id,omitempty"`
	Label      string            `json:"label,omitempty"`
	NeedsLogin bool              `json:"needs_login,omitempty"`
}

// CredentialStore reads and writes the secret of a provider; the gateway's
// service store satisfies it.
type CredentialStore interface {
	ReadServiceSecret(ctx context.Context, service contract.Service) ([]byte, error)
	WriteServiceSecret(ctx context.Context, id contract.ServiceID, secret []byte) error
}

// encodeCredential is the secret a provider keeps for a credential.
func encodeCredential(credential PluginCredential) ([]byte, error) {
	credential.Type = "plugin_credential"
	encoded, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 64<<10 {
		return nil, errors.New("the credential is larger than 64 KiB")
	}
	return []byte(credentialPrefix + base64.RawURLEncoding.EncodeToString(encoded)), nil
}

// decodeCredential reads a provider's secret as a credential.
func decodeCredential(secret string) (PluginCredential, bool) {
	text, ok := strings.CutPrefix(secret, credentialPrefix)
	if !ok {
		return PluginCredential{}, false
	}
	data, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil {
		return PluginCredential{}, false
	}
	var credential PluginCredential
	if json.Unmarshal(data, &credential) != nil || credential.Type != "plugin_credential" {
		return PluginCredential{}, false
	}
	return credential, true
}

// CredentialView is what the provider page shows of a credential: never
// its data, headers or query.
type CredentialView struct {
	LoggedIn   bool   `json:"logged_in"`
	AccountID  string `json:"account_id,omitempty"`
	Label      string `json:"label,omitempty"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
	NeedsLogin bool   `json:"needs_login,omitempty"`
}

// errNeedsLogin is a provider whose account only a new login restores.
var errNeedsLogin = errors.New("the provider's account needs a new login")

// credentialRefreshMargin refreshes a credential this long before it
// expires.
const credentialRefreshMargin = 30 * time.Second

// credentialLocks keeps one refresh of a provider's credential at a time,
// so a rotated refresh token is sent once.
type credentialLocks struct {
	mutex sync.Mutex
	locks map[contract.ServiceID]*sync.Mutex
}

func (locks *credentialLocks) of(id contract.ServiceID) *sync.Mutex {
	locks.mutex.Lock()
	defer locks.mutex.Unlock()
	if locks.locks == nil {
		locks.locks = map[contract.ServiceID]*sync.Mutex{}
	}
	lock, ok := locks.locks[id]
	if !ok {
		lock = &sync.Mutex{}
		locks.locks[id] = lock
	}
	return lock
}

// liveCredential is a provider's credential, refreshed first when it
// expires soon or force is set and the plugin exports refresh.
func (host *Host) liveCredential(ctx context.Context, service contract.Service, relay *relayTarget, baseURL string, credential PluginCredential, force bool) (PluginCredential, error) {
	if credential.NeedsLogin {
		return credential, errNeedsLogin
	}
	expiring := credential.ExpiresAt > 0 && time.Until(time.Unix(credential.ExpiresAt, 0)) < credentialRefreshMargin
	if (!expiring && !force) || !relay.plugin.HasTaskHook("refresh") {
		return credential, nil
	}
	lock := host.credentialLocks.of(service.ID)
	lock.Lock()
	defer lock.Unlock()
	// Another request may have refreshed it while this one waited.
	if host.credentials != nil && !force {
		if secret, err := host.credentials.ReadServiceSecret(ctx, service); err == nil {
			if stored, ok := decodeCredential(string(secret)); ok && stored.ExpiresAt > credential.ExpiresAt && time.Until(time.Unix(stored.ExpiresAt, 0)) > credentialRefreshMargin {
				return stored, nil
			}
		}
	}
	call, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	view := map[string]any{"baseUrl": baseURL, "credential": jsonOrNull(credential.Data), "apiKey": ""}
	raw, _, err := relay.plugin.CallTask(call, "refresh", []any{view}, credentialCallOptions(relay.meta, baseURL))
	if err != nil {
		// A temporary failure: the credential goes on until it expires.
		host.logf("plugin %s: refresh of the credential of %s failed", relay.plugin.Key, service.ID)
		if credential.ExpiresAt > 0 && time.Now().After(time.Unix(credential.ExpiresAt, 0)) {
			return credential, fmt.Errorf("the provider's credential expired and could not be refreshed")
		}
		return credential, nil
	}
	var result struct {
		Relogin    bool              `json:"relogin"`
		Credential json.RawMessage   `json:"credential"`
		Headers    map[string]string `json:"headers"`
		Query      map[string]string `json:"query"`
		ExpiresAt  int64             `json:"expiresAt"`
		AccountID  string            `json:"accountId"`
		Label      string            `json:"label"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return credential, errors.New("refresh returned neither a credential nor relogin")
	}
	if result.Relogin {
		credential.NeedsLogin = true
		host.saveCredential(ctx, service.ID, credential)
		return credential, errNeedsLogin
	}
	if result.AccountID != "" && credential.AccountID != "" && result.AccountID != credential.AccountID {
		return credential, errors.New("refresh returned another account")
	}
	refreshed := credential
	refreshed.Data, refreshed.Headers, refreshed.Query, refreshed.ExpiresAt = result.Credential, result.Headers, result.Query, result.ExpiresAt
	if result.AccountID != "" {
		refreshed.AccountID = result.AccountID
	}
	if result.Label != "" {
		refreshed.Label = result.Label
	}
	host.saveCredential(ctx, service.ID, refreshed)
	return refreshed, nil
}

// saveCredential keeps a provider's credential where its key is kept.
func (host *Host) saveCredential(ctx context.Context, id contract.ServiceID, credential PluginCredential) {
	if host.credentials == nil {
		return
	}
	secret, err := encodeCredential(credential)
	if err == nil {
		err = host.credentials.WriteServiceSecret(context.WithoutCancel(ctx), id, secret)
	}
	if err != nil {
		host.logf("plugin %s: could not keep the credential of %s: %v", credential.Plugin, id, err)
	}
}

// credentialCallOptions let the credential hooks POST to the provider's
// address and the plugin's allowed hosts.
func credentialCallOptions(meta *plugin.TaskMeta, baseURL string) plugin.CallOptions {
	allowed := slices.Clone(meta.AllowedHosts)
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Host != "" {
		allowed = append(allowed, strings.ToLower(parsed.Host))
	}
	return plugin.CallOptions{
		NetworkBudget: time.Minute,
		TaskFetch: &plugin.TaskFetch{
			Allowed: func(target *url.URL) bool { return slices.Contains(allowed, strings.ToLower(target.Host)) },
			Client:  &http.Client{}, Post: true,
		},
	}
}

// PluginCredentialOf describes the credential a provider keeps, for the
// provider page.
func (host *Host) PluginCredentialOf(ctx context.Context, service contract.Service) CredentialView {
	if host == nil || host.credentials == nil {
		return CredentialView{}
	}
	secret, err := host.credentials.ReadServiceSecret(ctx, service)
	if err != nil {
		return CredentialView{}
	}
	credential, ok := decodeCredential(string(secret))
	if !ok {
		return CredentialView{}
	}
	return CredentialView{LoggedIn: true, AccountID: credential.AccountID, Label: credential.Label, ExpiresAt: credential.ExpiresAt, NeedsLogin: credential.NeedsLogin}
}

// RevokePluginCredential has the plugin revoke the credential a provider
// that is going away keeps, in the background; its result is ignored.
func (host *Host) RevokePluginCredential(ctx context.Context, service contract.Service) {
	if host == nil || host.credentials == nil || service.Plugin == nil || service.Plugin.Provider != contract.PluginProviderRelay {
		return
	}
	target := host.plugins.Current().Plugin(service.Plugin.Key)
	if target == nil || target.NewAPI() == nil || !target.HasTaskHook("revoke") {
		return
	}
	secret, err := host.credentials.ReadServiceSecret(ctx, service)
	if err != nil {
		return
	}
	credential, ok := decodeCredential(string(secret))
	if !ok {
		return
	}
	baseURL := ""
	if service.HTTP != nil {
		baseURL = strings.TrimRight(service.HTTP.BaseURL, "/")
	}
	meta := target.NewAPI()
	go func() {
		call, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		view := map[string]any{"baseUrl": baseURL, "credential": jsonOrNull(credential.Data), "apiKey": ""}
		if _, _, err := target.CallTask(call, "revoke", []any{view}, credentialCallOptions(meta, baseURL)); err != nil {
			host.logf("plugin %s: revoke of the credential of %s failed", target.Key, service.ID)
		}
	}()
}
