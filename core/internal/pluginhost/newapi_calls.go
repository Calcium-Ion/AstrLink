package pluginhost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/new-api/pluginkit"
)

// The provider hooks of a New API plugin run through pluginkit's typed
// calls, which check what each returns as New API does; the host keeps
// the counts and journal of the plugin page and the network the
// credential hooks reach.

// callProvider runs call, a call of hook into the relay's plugin, with the
// time a relay hook has.
func callProvider[T any](ctx context.Context, relay *relayTarget, hook string, call func(context.Context, *plugin.NewAPIPlugin) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, relayHookTime)
	defer cancel()
	result, _, err := plugin.CallNewAPI(ctx, relay.plugin, hook, plugin.CallOptions{}, call)
	return result, err
}

// callCredential runs call, a call of one of the plugin's credential hooks,
// whose utils.fetch may POST to the provider's address and the plugin's
// allowed hosts, for a minute.
func callCredential[T any](ctx context.Context, target *plugin.Plugin, hook, baseURL string, call func(context.Context, *plugin.NewAPIPlugin) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	result, _, err := plugin.CallNewAPI(ctx, target, hook, credentialCallOptions(target.NewAPI(), baseURL), call)
	return result, err
}

// credentialCallOptions let the credential hooks reach the provider's
// address and the plugin's allowed hosts.
func credentialCallOptions(meta *plugin.NewAPIPlugin, baseURL string) plugin.CallOptions {
	var allowed []string
	if meta != nil {
		allowed = slices.Clone(meta.Meta.AllowedHosts)
	}
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Host != "" {
		allowed = append(allowed, strings.ToLower(parsed.Host))
	}
	return plugin.CallOptions{
		Fetch: &plugin.Fetch{
			Allowed: func(target *url.URL) bool { return slices.Contains(allowed, strings.ToLower(target.Host)) },
			Client:  &http.Client{},
		},
	}
}

// credentialContext is ctx of the hooks that read the provider's
// credential or key.
func (relay *relayTarget) credentialContext(baseURL, key string) pluginkit.CredentialContext {
	hookCtx := pluginkit.CredentialContext{BaseURL: baseURL, APIKey: key, Config: relay.config, Token: relay.token}
	if relay.credential != nil {
		hookCtx.Credential, hookCtx.APIKey = relay.credential.Data, ""
	}
	return hookCtx
}

// hookContext is ctx of the relay hooks of the route at clientPath.
func (relay *relayTarget) hookContext(baseURL, clientPath string, state json.RawMessage) pluginkit.HookContext {
	return pluginkit.HookContext{BaseURL: baseURL, Path: clientPath, Config: relay.config, Token: relay.token, State: state}
}
