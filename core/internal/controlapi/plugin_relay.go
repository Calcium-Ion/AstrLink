package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/new-api/pluginkit"
)

// relayProvider is the provider a new-api plugin's provider part offers on
// the providers page, named in language.
func (handler *Handler) relayProvider(meta *plugin.NewAPIPlugin, language string) contract.PluginProvider {
	provider := contract.PluginProvider{
		ID: contract.PluginProviderRelay, Name: meta.NameIn(language), BaseURL: meta.Meta.BaseURL, Auth: relayServiceAuth(meta),
		Models: slices.Clone(meta.Meta.Models), Protocols: []contract.PluginProviderProtocol{},
		ConfigSchema: meta.Meta.ChannelConfigSchemaJSON(), KeyHint: meta.Meta.KeyHint.Text(language),
	}
	if target := handler.plugins.Current().Plugin(meta.Meta.Key); target != nil {
		provider.Login = target.HasTaskHook("login")
		provider.Balance = target.HasTaskHook("buildBalanceRequest")
	}
	for _, capability := range handler.relayCapabilities(meta) {
		if capability.ConvertTo == "" {
			provider.Protocols = append(provider.Protocols, contract.PluginProviderProtocol{Protocol: capability.Protocol, Stream: capability.Streaming, Sync: true})
		}
	}
	return provider
}

// relayServiceAuth is how a relay provider keeps its key: as a bearer
// token, which each route then sends as it says, or none when no route
// sends one.
func relayServiceAuth(meta *plugin.NewAPIPlugin) contract.ServiceAuth {
	for _, route := range meta.Meta.Relay {
		if route.Auth == nil || route.Auth.Type != "none" {
			return contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}
		}
	}
	return contract.ServiceAuth{Scheme: contract.AuthSchemeNone}
}

// relayCapabilities are what routing reads of a relay provider: one per
// protocol of the routes the gateway answers, native when a route sends it
// as it came, else converted as the first route's relaykit converter
// does, when the gateway has it. An attempt then goes the way the route of
// its model says. Routes the plugin's own hooks convert, and those of
// paths the gateway does not answer, are left out.
func (handler *Handler) relayCapabilities(meta *plugin.NewAPIPlugin) []contract.Capability {
	var capabilities []contract.Capability
	add := func(capability contract.Capability) {
		for index, have := range capabilities {
			if have.Protocol != capability.Protocol {
				continue
			}
			if have.ConvertTo != "" && capability.ConvertTo == "" {
				capabilities[index] = capability
			} else if have.ConvertTo == capability.ConvertTo {
				capabilities[index].Streaming = have.Streaming || capability.Streaming
			}
			return
		}
		capabilities = append(capabilities, capability)
	}
	for _, route := range meta.Meta.Relay {
		protocol := plugin.RelayProtocol(route.IncomingPath)
		if protocol == "" || protocol == contract.ProtocolOpenAIModels {
			continue
		}
		descriptor, known := contract.LookupProtocolDescriptor(protocol)
		streaming := known && descriptor.Streaming
		upstream := plugin.RelayUpstreamProtocol(route)
		if upstream == protocol {
			if plugin.RelayConverter(route) == "" {
				add(contract.Capability{Protocol: protocol, Mode: contract.CapabilityModeNative, Streaming: streaming})
			}
			continue
		}
		edge, ok := handler.conversionEdge(protocol, upstream)
		if !ok {
			continue
		}
		add(contract.Capability{Protocol: protocol, Mode: contract.CapabilityModeNative, Streaming: streaming && edge.Streaming, ConvertTo: upstream})
	}
	return capabilities
}

// conversionEdge is the gateway's conversion from one protocol to another,
// when it has one.
func (handler *Handler) conversionEdge(from, to contract.ProtocolID) (contract.ConversionEdge, bool) {
	engine := handler.capabilities.ConversionEngine
	if !engine.Available {
		return contract.ConversionEdge{}, false
	}
	for _, edge := range engine.Edges {
		if edge.From == from && edge.To == to {
			return edge, true
		}
	}
	return contract.ConversionEdge{}, false
}

// applyRelayProvider fills in what a provider of a new-api plugin's
// provider part takes from the plugin: its capabilities, how it keeps its
// key, its address when the user named none, and its model list path.
func (handler *Handler) applyRelayProvider(service *contract.Service) error {
	if handler.plugins == nil {
		return errors.New("plugins are unavailable")
	}
	target := handler.plugins.Current().Plugin(service.Plugin.Key)
	if target == nil || target.NewAPI() == nil || len(target.NewAPI().Meta.Relay) == 0 {
		return fmt.Errorf("plugin %q is not an installed plugin with a provider part", service.Plugin.Key)
	}
	meta := target.NewAPI()
	if service.Plugin.Upstream != "" {
		return errors.New("plugin.upstream is for the provider of a task plugin")
	}
	service.Capabilities = handler.relayCapabilities(meta)
	if len(service.Capabilities) == 0 {
		return fmt.Errorf("none of the routes of %s is one AstrLink serves yet", meta.Meta.Key)
	}
	settings, err := relaySettings(meta, service.Plugin.Settings)
	if err != nil {
		return err
	}
	service.Plugin.Settings = settings
	if service.HTTP != nil {
		if service.HTTP.BaseURL == "" {
			service.HTTP.BaseURL = meta.Meta.BaseURL
		}
		service.HTTP.Auth = relayServiceAuth(meta)
		service.HTTP.ModelListPath = ""
	}
	return nil
}

// checkRelayKey runs the checkKey hook of a relay provider's plugin on a
// key the save gives, and on its settings: the plugin checks the key's form,
// such as the parts of AccessKey|SecretKey. The message of a key it refuses
// fails the save.
func (handler *Handler) checkRelayKey(ctx context.Context, service contract.Service, credential storage.CredentialMutation) error {
	if service.Plugin == nil || service.Plugin.Provider != contract.PluginProviderRelay || !credential.Present || len(credential.Secret) == 0 || handler.plugins == nil {
		return nil
	}
	target := handler.plugins.Current().Plugin(service.Plugin.Key)
	if target == nil || target.NewAPI() == nil || !target.HasTaskHook("checkKey") {
		return nil
	}
	baseURL := ""
	if service.HTTP != nil {
		baseURL = strings.TrimRight(service.HTTP.BaseURL, "/")
	}
	config := pluginhost.RelayConfig(target.NewAPI(), service.Plugin.Settings)
	call, cancel := context.WithTimeout(ctx, validateConfigTime)
	defer cancel()
	refused, _, err := plugin.CallNewAPI(call, target, "checkKey", plugin.CallOptions{}, func(ctx context.Context, loaded *plugin.NewAPIPlugin) (pluginkit.LocalizedText, error) {
		return loaded.CallCheckKey(ctx, baseURL, config, string(credential.Secret))
	})
	if err != nil {
		return fmt.Errorf("the plugin refused the key: %s", validateConfigMessage(err))
	}
	if len(refused) > 0 {
		return fmt.Errorf("%s", refused.Text(handler.pluginPageView().Language))
	}
	return nil
}

// relaySettings checks a relay provider's settings against its plugin's
// channelConfig: members the schema does not declare are dropped, and a
// value it refuses fails the save, naming the field. Without a schema the
// provider keeps none.
func relaySettings(meta *plugin.NewAPIPlugin, settings json.RawMessage) (json.RawMessage, error) {
	if len(settings) == 0 || meta.Meta.ChannelConfig == nil {
		return nil, nil
	}
	kept, err := meta.ValidateChannelConfig(settings)
	if err != nil {
		if strings.Contains(err.Error(), "must be a JSON object") {
			return nil, errors.New("plugin.settings must be a JSON object")
		}
		return nil, fmt.Errorf("the provider's settings: %v", err)
	}
	if string(kept) == "{}" {
		return nil, nil
	}
	return kept, nil
}

// pluginLoginInput drives the login of a provider of the plugin: start,
// state, submit (with what a paste or fields step asks), cancel, or the
// status of the credential it keeps.
type pluginLoginInput struct {
	ServiceID contract.ServiceID `json:"service_id"`
	Action    string             `json:"action"`
	pluginhost.LoginInput
}

// pluginLoginAnswer is a login's state and the provider's credential.
type pluginLoginAnswer struct {
	Login      *pluginhost.LoginView     `json:"login,omitempty"`
	Credential pluginhost.CredentialView `json:"credential"`
}

// pluginLogin serves the logins of the plugin's providers.
func (handler *Handler) pluginLogin(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin) {
	if handler.pluginHost == nil || handler.serviceStore == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
		return
	}
	var input pluginLoginInput
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	record, err := handler.serviceStore.GetService(request.Context(), input.ServiceID)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	service := record.Service
	if service.Plugin == nil || service.Plugin.Key != target.Key || service.Plugin.Provider != contract.PluginProviderRelay {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_login", "the provider is not one of this plugin's")
		return
	}
	ctx := request.Context()
	answer := pluginLoginAnswer{}
	switch input.Action {
	case "start":
		view, err := handler.pluginHost.StartLogin(ctx, service)
		if err != nil {
			writeError(writer, http.StatusUnprocessableEntity, "login_failed", err.Error())
			return
		}
		answer.Login = &view
	case "state":
		if view, ok := handler.pluginHost.Login(ctx, service.ID); ok {
			answer.Login = &view
		}
	case "submit":
		view, err := handler.pluginHost.SubmitLogin(ctx, service.ID, input.LoginInput)
		if err != nil {
			writeError(writer, http.StatusUnprocessableEntity, "login_failed", err.Error())
			return
		}
		answer.Login = &view
	case "cancel":
		handler.pluginHost.CancelLogin(service.ID)
	case "status":
	default:
		writeError(writer, http.StatusUnprocessableEntity, "invalid_login", "action is start, state, submit, cancel or status")
		return
	}
	if record, err := handler.serviceStore.GetService(ctx, service.ID); err == nil {
		answer.Credential = handler.pluginHost.PluginCredentialOf(ctx, record.Service)
	}
	writeJSON(writer, http.StatusOK, answer)
}

// providerBalanceAge reads a provider's balance again when the one kept is
// older.
const providerBalanceAge = time.Hour

// pluginProviderState answers the balance of a provider of the plugin, read
// again when refresh is set or the one kept is older than an hour, and
// whether the plugin rests it.
func (handler *Handler) pluginProviderState(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin) {
	if handler.pluginHost == nil || handler.serviceStore == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
		return
	}
	var input struct {
		ServiceID contract.ServiceID `json:"service_id"`
		Refresh   bool               `json:"refresh"`
	}
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	record, err := handler.serviceStore.GetService(request.Context(), input.ServiceID)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	service := record.Service
	if service.Plugin == nil || service.Plugin.Key != target.Key || service.Plugin.Provider != contract.PluginProviderRelay {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_provider", "the provider is not one of this plugin's")
		return
	}
	state := handler.pluginHost.ProviderStateOf(service.ID)
	if target.HasTaskHook("buildBalanceRequest") && (input.Refresh || state.UpdatedAt.IsZero() || time.Since(state.UpdatedAt) > providerBalanceAge) {
		state, _ = handler.pluginHost.ProviderBalance(request.Context(), service)
	}
	writeJSON(writer, http.StatusOK, state)
}
