package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
)

// ServicePlugin names the plugin that adapts a provider, and which of the
// kinds of provider it declares.
type ServicePlugin struct {
	Key      string `json:"key"`
	Provider string `json:"provider"`
	// Upstream is new_api for a task plugin's provider that is another
	// new-api gateway with the plugin installed, "" for the vendor.
	Upstream string `json:"upstream,omitempty"`
	// Settings are what a task plugin reads as the provider's own
	// settings, such as vertexRegion: a JSON object.
	Settings json.RawMessage `json:"settings,omitempty"`
}

// MaxServicePluginSettingsBytes bounds a provider's plugin settings.
const MaxServicePluginSettingsBytes = 16 << 10

var pluginProviderPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// validate requires a plugin for, and only for, a provider of kind plugin.
func (plugin *ServicePlugin) validate(kind ServiceKind) error {
	switch {
	case kind != ServiceKindPlugin && plugin != nil:
		return fmt.Errorf("plugin is only valid for a provider of kind plugin")
	case kind == ServiceKindPlugin && plugin == nil:
		return fmt.Errorf("a provider of kind plugin requires plugin")
	case plugin == nil:
		return nil
	case !ValidPluginKey(plugin.Key):
		return fmt.Errorf("plugin.key %q is not a plugin key", plugin.Key)
	case !pluginProviderPattern.MatchString(plugin.Provider):
		return fmt.Errorf("plugin.provider %q is not a provider id", plugin.Provider)
	case plugin.Upstream != "" && (plugin.Upstream != PluginUpstreamNewAPI || plugin.Provider != PluginProviderTask):
		return fmt.Errorf("plugin.upstream may only be new_api, for the provider of a task plugin")
	case len(plugin.Settings) > MaxServicePluginSettingsBytes:
		return fmt.Errorf("plugin.settings is larger than %d KiB", MaxServicePluginSettingsBytes>>10)
	case len(plugin.Settings) > 0 && (!json.Valid(plugin.Settings) || !bytes.HasPrefix(bytes.TrimSpace(plugin.Settings), []byte("{"))):
		return fmt.Errorf("plugin.settings must be a JSON object")
	}
	return nil
}

// PluginProvider is a kind of provider a plugin adapts, as the providers
// page offers it.
type PluginProvider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url,omitempty"`
	// Auth is how the gateway sends the provider's key.
	Auth ServiceAuth `json:"auth"`
	// Protocols are the protocols the plugin serves, the first the one
	// others convert to when Convert is set.
	Protocols []PluginProviderProtocol `json:"protocols"`
	Convert   bool                     `json:"convert,omitempty"`
	Models    []string                 `json:"models,omitempty"`
	// ConfigSchema is the schema of the provider's settings, as the plugin
	// wrote it; KeyHint is shown with its key. Login says the plugin logs
	// in to an account instead of taking a key, and Balance that it reads
	// the account's balance.
	ConfigSchema json.RawMessage `json:"config_schema,omitempty"`
	KeyHint      string          `json:"key_hint,omitempty"`
	Login        bool            `json:"login,omitempty"`
	Balance      bool            `json:"balance,omitempty"`
}

// PluginProviderProtocol is what a plugin serves of one protocol.
type PluginProviderProtocol struct {
	Protocol ProtocolID `json:"protocol"`
	Stream   bool       `json:"stream"`
	Sync     bool       `json:"sync"`
}
