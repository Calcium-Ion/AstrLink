package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/plugin/view"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/taskplugin"
)

const PluginsPath = "/control/v1/plugins"

// pluginPageBudget bounds a plugin page hook: its run and its network wait.
const pluginPageBudget = 10 * time.Second

// maxPluginBindingValues bounds the values of one attribute the overview
// lists.
const maxPluginBindingValues = 50

// PluginRegistry is the plugin registry; *plugin.Manager satisfies it.
type PluginRegistry interface {
	Current() *plugin.Generation
	Dir() string
	Reload(context.Context) error
	Refresh(context.Context) error
}

// pluginsRequiredRole lets observers read the list and a plugin's settings,
// and keeps the routes that run plugin code, as every write, to operators.
func pluginsRequiredRole(request *http.Request) Role {
	if isSafeMethod(request.Method) && !strings.HasSuffix(strings.TrimSuffix(request.URL.Path, "/"), "/status") {
		return RoleObserver
	}
	return RoleOperator
}

func (handler *Handler) pluginsResource(writer http.ResponseWriter, request *http.Request) {
	if handler.plugins == nil || handler.pluginStore == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(request.URL.Path, PluginsPath), "/")
	if rest == "" {
		if !allowMethods(writer, request, http.MethodGet) {
			return
		}
		handler.writePluginList(writer)
		return
	}
	key, action, _ := strings.Cut(rest, "/")
	if key == "sources" && (action == "preview" || action == "install") && request.Method == http.MethodPost {
		handler.pluginSources(writer, request, action)
		return
	}
	if key == "market" && (action == "" || action == "doc") {
		if !allowMethods(writer, request, http.MethodGet) {
			return
		}
		if action == "doc" {
			handler.pluginMarketDoc(writer, request)
		} else {
			handler.pluginMarket(writer, request)
		}
		return
	}
	if key == "reload" && action == "" && request.Method == http.MethodPost {
		if err := handler.plugins.Reload(request.Context()); err != nil {
			writeError(writer, http.StatusInternalServerError, "plugin_reload_failed", err.Error())
			return
		}
		handler.registerEnabledPluginAttributes(request.Context())
		handler.syncTaskProviders(request.Context())
		handler.writePluginList(writer)
		return
	}
	if !contract.ValidPluginKey(key) {
		writeError(writer, http.StatusNotFound, "not_found", "plugin not found")
		return
	}
	target := handler.plugins.Current().Plugin(key)
	if target == nil {
		writeError(writer, http.StatusNotFound, "not_found", "plugin not found")
		return
	}
	switch {
	case action == "":
		switch request.Method {
		case http.MethodGet:
			handler.writePluginDetail(writer, request.Context(), target)
		case http.MethodPatch:
			handler.updatePluginSettings(writer, request, target)
		default:
			writer.Header().Set("Allow", "GET, PATCH")
			writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PATCH")
		}
	case action == "test":
		if allowMethods(writer, request, http.MethodPost) {
			handler.testPlugin(writer, request.Context(), target)
		}
	case action == "status":
		if allowMethods(writer, request, http.MethodGet) {
			handler.pluginStatus(writer, request.Context(), target)
		}
	case strings.HasPrefix(action, "credentials/"):
		handler.pluginCredential(writer, request, target, strings.TrimPrefix(action, "credentials/"))
	case action == "actions":
		if allowMethods(writer, request, http.MethodPost) {
			handler.pluginAction(writer, request, target)
		}
	case action == "workspaces" || action == "values" || action == "lookup-credentials" || action == "files":
		handler.clearPluginData(writer, request, target, action)
	case action == "stats":
		if allowMethods(writer, request, http.MethodGet) {
			handler.pluginStats(writer, request, target)
		}
	case action == "credits":
		if allowMethods(writer, request, http.MethodGet) {
			handler.pluginCredits(writer, request.Context(), target)
		}
	case action == "journal":
		if allowMethods(writer, request, http.MethodGet) {
			handler.pluginJournal(writer, request, target)
		}
	case action == "dry-run":
		if allowMethods(writer, request, http.MethodPost) {
			handler.pluginDryRun(writer, request, target)
		}
	case action == "client.js" || action == "client.js.map":
		if allowMethods(writer, request, http.MethodGet) {
			handler.pluginClient(writer, target, action)
		}
	case action == "ui-call":
		if allowMethods(writer, request, http.MethodPost) {
			handler.pluginUICall(writer, request, target)
		}
	case action == "tasks":
		handler.pluginTasks(writer, request, target)
	case action == "provider-state":
		if allowMethods(writer, request, http.MethodPost) {
			handler.pluginProviderState(writer, request, target)
		}
	case action == "login":
		if allowMethods(writer, request, http.MethodPost) {
			handler.pluginLogin(writer, request, target)
		}
	case action == "request-dry-run":
		if allowMethods(writer, request, http.MethodPost) {
			handler.requestPluginDryRun(writer, request, target)
		}
	case action == "source" || strings.HasPrefix(action, "source/"):
		handler.pluginSource(writer, request, target, strings.TrimPrefix(strings.TrimPrefix(action, "source"), "/"))
	default:
		writeError(writer, http.StatusNotFound, "not_found", "plugin endpoint not found")
	}
}

func allowMethods(writer http.ResponseWriter, request *http.Request, method string) bool {
	if request.Method == method {
		return true
	}
	writer.Header().Set("Allow", method)
	writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use "+method)
	return false
}

func (handler *Handler) writePluginList(writer http.ResponseWriter) {
	plugins := handler.plugins.Current().Plugins()
	summaries := make([]contract.PluginSummary, 0, len(plugins))
	language := handler.pluginPageView().Language
	for _, target := range plugins {
		summary := handler.pluginSummary(target, language)
		handler.countTasks(context.Background(), target, &summary)
		summaries = append(summaries, summary)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"directory": handler.plugins.Dir(), "plugins": summaries})
}

func (handler *Handler) pluginSummary(target *plugin.Plugin, language string) contract.PluginSummary {
	summary := contract.PluginSummary{
		Key:                   target.Key,
		Name:                  target.Key,
		Source:                string(target.Origin),
		File:                  target.File,
		Icon:                  target.Icon,
		Enabled:               target.Enabled(),
		State:                 contract.PluginStateLoaded,
		RequestedCapabilities: []string{},
		GrantedCapabilities:   []string{},
	}
	if development := target.Development; development != nil {
		summary.Development = &contract.PluginDevelopment{Project: development.Project, BuiltAt: development.BuiltAt}
	}
	if installed := target.Installed; installed != nil {
		source := installed.Source
		summary.Installed = &source
	}
	if target.UILoads() {
		summary.UIVersion = target.ClientHash
	}
	if target.Enabled() && target.Can(plugin.CapabilityProvider) {
		for _, provider := range target.Meta.Providers {
			offered := contract.PluginProvider{
				ID: provider.ID, Name: provider.Name, BaseURL: provider.BaseURL, Auth: provider.Auth.Service(),
				Convert: provider.Convert, Models: slices.Clone(provider.Models), Protocols: []contract.PluginProviderProtocol{},
			}
			for _, protocol := range provider.ProtocolOrder() {
				modes := provider.Protocols[protocol]
				offered.Protocols = append(offered.Protocols, contract.PluginProviderProtocol{Protocol: protocol, Stream: modes.Stream, Sync: modes.Synchronous()})
			}
			summary.Providers = append(summary.Providers, offered)
		}
	}
	if meta := target.Task(); meta != nil {
		summary.Kind, summary.Task = contract.PluginKindTask, taskplugin.Summary(meta, language)
		if target.Enabled() {
			summary.Providers = append(summary.Providers, taskProvider(meta, language))
		}
	} else if meta := target.NewAPI(); meta != nil {
		summary.Kind = contract.PluginKindProvider
		if len(meta.Relay) == 0 && meta.HasRequestPart() {
			summary.Kind = contract.PluginKindRequest
		}
	} else if target.Loaded() {
		summary.Kind = contract.PluginKindExtension
	}
	if meta := target.NewAPI(); meta != nil && len(meta.Relay) > 0 && target.Enabled() {
		summary.Providers = append(summary.Providers, handler.relayProvider(meta, language))
	}
	if meta := target.NewAPI(); meta.HasRequestPart() {
		summary.Request = &contract.PluginRequestPart{
			Stages: slices.Clone(meta.Stages()), ConfigSchema: meta.ConfigSchema, Optional: meta.Optional, Description: meta.Description.In(language),
			Samples: meta.Samples,
		}
	}
	if !target.Loaded() {
		summary.State = contract.PluginStateFailed
		if target.Err != nil {
			summary.Error = &contract.PluginLoadError{Code: target.Err.Code, File: target.Err.File, Line: target.Err.Line, Column: target.Err.Column, Message: target.Err.Message}
		}
		return summary
	}
	summary.Name, summary.Version = target.Meta.Name, target.Meta.Version
	summary.APIVersion = target.Meta.APIVersion
	summary.RequestedCapabilities = append(summary.RequestedCapabilities, target.Meta.Capabilities...)
	summary.GrantedCapabilities = append(summary.GrantedCapabilities, target.Granted()...)
	summary.NeedsNetwork = target.Meta.NeedsNetwork()
	stats := target.Stats()
	summary.Calls = stats.Calls
	for _, count := range stats.Failures {
		summary.Failures += count
	}
	return summary
}

// countTasks adds how many tasks a task plugin has to its summary.
func (handler *Handler) countTasks(ctx context.Context, target *plugin.Plugin, summary *contract.PluginSummary) {
	if handler.pluginTaskEngine == nil || target.Task() == nil {
		return
	}
	total, running, err := handler.pluginTaskEngine.Store().CountPluginTasks(ctx, target.Key)
	if err == nil {
		summary.Tasks, summary.RunningTasks = total, running
	}
}

// hookLimits lists the time limits of the extension points the plugin
// serves, with the user's settings.
func hookLimits(target *plugin.Plugin, set map[string]int) []contract.PluginHookLimitSetting {
	limits := []contract.PluginHookLimitSetting{}
	for _, hook := range limitedHooks(target) {
		bounds := contract.PluginHookLimits[hook]
		limits = append(limits, contract.PluginHookLimitSetting{
			Hook: hook, DefaultMs: bounds.DefaultMs, MaxMs: bounds.MaxMs,
			PluginMs: target.Meta.Limits[hook], Ms: set[hook], EffectiveMs: int(target.Limit(hook).Milliseconds()),
		})
	}
	return limits
}

// limitedHooks are the extension points with a time limit the plugin
// serves, in the order a request meets them.
func limitedHooks(target *plugin.Plugin) []string {
	var hooks []string
	for _, hook := range []string{
		contract.PluginHookOnRequest, contract.PluginHookRoute, contract.PluginHookBeforeForward, contract.PluginHookBeforeUpstream,
		contract.PluginHookRewriteResponse, contract.PluginHookOnStreamEvent, contract.PluginHookOnResponse, contract.PluginHookOnEvent,
		contract.PluginHookOnSchedule, contract.PluginHookOnHTTP, contract.PluginHookOnUICall,
	} {
		if target.Has(hook) {
			hooks = append(hooks, hook)
		}
	}
	return hooks
}

func (handler *Handler) writePluginDetail(writer http.ResponseWriter, ctx context.Context, target *plugin.Plugin) {
	detail, err := handler.pluginDetail(ctx, target)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not read the plugin's settings")
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (handler *Handler) pluginDetail(ctx context.Context, target *plugin.Plugin) (contract.PluginDetail, error) {
	summary := handler.pluginSummary(target, handler.pluginPageView().Language)
	handler.countTasks(ctx, target, &summary)
	detail := contract.PluginDetail{
		PluginSummary:  summary,
		Notice:         target.Meta.Notice,
		Hooks:          []string{},
		Config:         json.RawMessage("{}"),
		NetworkOrigins: []contract.PluginNetworkOrigin{},
		Credentials:    []contract.PluginCredentialStatus{},
		Metadata:       []contract.PluginMetadataAttribute{},
	}
	if target.Meta.Network != nil {
		detail.NetworkPurpose = target.Meta.Network.Purpose
	}
	detail.Hooks = append(detail.Hooks, target.Hooks()...)
	settings, err := handler.pluginStore.GetPluginSettings(ctx, target.Key)
	if errors.Is(err, storage.ErrNotFound) {
		settings, err = storage.NewPluginSettingsRecord(target.Key), nil
	}
	if err != nil {
		return contract.PluginDetail{}, err
	}
	if len(settings.Config) > 0 {
		detail.Config = settings.Config
	}
	detail.PrivacyTrusted = settings.Trusted
	detail.Limits = hookLimits(target, settings.HookLimitsMs)
	detail.FailClosedRequested = target.Meta.FailClosed
	detail.FailClosed = settings.FailClosed && target.Meta.FailClosed
	detail.Client = target.Client
	detail.UIEnabled = settings.UIEnabled
	if handler.pluginInstaller != nil && target.Origin != plugin.OriginBuiltin {
		detail.Versions = handler.pluginInstaller.Versions(target.Key)
	}
	detail.TurnWaitMs = settings.TurnWaitMs
	if detail.TurnWaitMs == 0 {
		detail.TurnWaitMs = contract.DefaultPluginTurnWaitMs
	}
	detail.ExcludedClients = append([]string{}, settings.ExcludedClients...)
	detail.WorkspaceBinding, detail.AutoRefresh = settings.WorkspaceBinding, settings.AutoRefresh
	detail.LookupEnabled = settings.LookupEnabled
	detail.Tools = []contract.PluginTool{}
	language := handler.pluginPageView().Language
	for _, tool := range target.Meta.Tools {
		detail.Tools = append(detail.Tools, contract.PluginTool{Name: tool.Name, Description: tool.Description.In(language), Unbound: tool.Unbound, Writes: tool.Writes})
	}
	for _, origin := range settings.Origins {
		detail.NetworkOrigins = append(detail.NetworkOrigins, contract.PluginNetworkOrigin{Origin: origin.Origin, AllowPlaintext: origin.AllowPlaintext, Plaintext: plugin.Plaintext(origin.Origin), Setting: origin.Setting})
	}
	stored, err := handler.pluginStore.ListPluginCredentials(ctx, target.Key)
	if err != nil {
		return contract.PluginDetail{}, err
	}
	for _, slot := range target.Meta.Credentials {
		status := contract.PluginCredentialStatus{Name: slot.Name, Label: slot.Label}
		if index := slices.IndexFunc(stored, func(record storage.PluginCredentialRecord) bool { return record.Name == slot.Name }); index >= 0 {
			status.Configured, status.Hint, status.Origin = true, stored[index].Hint, stored[index].Origin
		}
		detail.Credentials = append(detail.Credentials, status)
	}
	for _, attribute := range target.Meta.Metadata {
		detail.Metadata = append(detail.Metadata, contract.PluginMetadataAttribute{Key: attribute.Key, Label: attribute.Label, Pattern: attribute.Pattern, Marker: attribute.Marker})
	}
	detail.Settings = pluginSettings(target.Meta.Settings)
	detail.Credits = []contract.PluginCredit{}
	for _, credit := range target.Meta.Credits {
		detail.Credits = append(detail.Credits, contract.PluginCredit{Name: credit.Name, Owner: credit.Owner, License: credit.License, URL: credit.URL})
	}
	detail.Storage = []contract.PluginStorageItem{}
	for _, item := range target.Meta.Storage {
		detail.Storage = append(detail.Storage, contract.PluginStorageItem{Label: item.Label, Content: item.Content, Retention: item.Retention})
	}
	if detail.HostStorage, err = handler.pluginHostStorage(ctx, target); err != nil {
		return contract.PluginDetail{}, err
	}
	return detail, nil
}

// pluginHostStorage lists what the host keeps for a plugin, with how many
// where the plugin page offers to clear them.
func (handler *Handler) pluginHostStorage(ctx context.Context, target *plugin.Plugin) ([]contract.PluginHostStorage, error) {
	kept := []contract.PluginHostStorage{}
	count := func(kind string, value int) {
		kept = append(kept, contract.PluginHostStorage{Kind: kind, Count: &value})
	}
	if len(target.Meta.Metadata) > 0 {
		kept = append(kept, contract.PluginHostStorage{Kind: contract.PluginStoredSessionBindings})
		if handler.metadataStore != nil {
			workspaces, err := handler.metadataStore.CountWorkspaceValues(ctx, pluginAttributeKeys(target))
			if err != nil {
				return nil, err
			}
			count(contract.PluginStoredWorkspaceBindings, workspaces)
		}
	}
	if len(target.Meta.Tools) > 0 && handler.pluginHost != nil {
		count(contract.PluginStoredLookupCredentials, handler.pluginHost.LookupCredentials(target.Key))
	}
	sessions, err := handler.pluginStore.CountPluginSessions(ctx, target.Key)
	if err != nil {
		return nil, err
	}
	count(contract.PluginStoredSessions, sessions)
	kept = append(kept, contract.PluginHostStorage{Kind: contract.PluginStoredRecords})
	if target.Has(plugin.HookPrepareSession) && slices.Contains(target.Meta.Capabilities, plugin.CapabilityInject) {
		kept = append(kept, contract.PluginHostStorage{Kind: contract.PluginStoredResidentBlock})
	}
	if slices.Contains(target.Meta.Capabilities, plugin.CapabilityStorage) {
		keys, err := handler.pluginStore.PluginValueKeys(ctx, target.Key, "")
		if err != nil {
			return nil, err
		}
		count(contract.PluginStoredValues, len(keys))
	}
	// The plugin's own files show once it keeps some.
	if files, bytes := target.DataFiles(); files > 0 {
		kept = append(kept, contract.PluginHostStorage{Kind: contract.PluginStoredFiles, Count: &files, Bytes: &bytes})
	}
	return kept, nil
}

func pluginSettings(settings []plugin.Setting) []contract.PluginSetting {
	converted := []contract.PluginSetting{}
	for _, setting := range settings {
		field := contract.PluginSetting{
			Key: setting.Key, Label: setting.Label, Type: setting.Type, Group: setting.Group, Row: setting.Row,
			Help: setting.Help, Default: setting.Default, Required: setting.Required, Advanced: setting.Advanced,
			Min: setting.Min, Max: setting.Max, Step: setting.Step, Unit: setting.Unit,
			Placeholder: setting.Placeholder, Credential: setting.Credential,
		}
		for _, option := range setting.Options {
			field.Options = append(field.Options, contract.PluginSettingOption{Value: option.Value, Label: option.Label})
		}
		converted = append(converted, field)
	}
	return converted
}

func (handler *Handler) updatePluginSettings(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin) {
	var update contract.PluginSettingsUpdate
	if !decodeControlJSON(writer, request, &update) {
		return
	}
	ctx := request.Context()
	handler.pluginsMu.Lock()
	defer handler.pluginsMu.Unlock()
	settings, err := handler.pluginStore.GetPluginSettings(ctx, target.Key)
	if errors.Is(err, storage.ErrNotFound) {
		settings, err = storage.NewPluginSettingsRecord(target.Key), nil
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not read the plugin's settings")
		return
	}
	invalid := func(message string) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_plugin_settings", message)
	}
	previous := settings
	previous.Capabilities = slices.Clone(settings.Capabilities)
	stored := slices.Clone(settings.Origins)
	if update.Enabled != nil {
		if *update.Enabled && !target.Loaded() {
			invalid("a plugin that did not load cannot be enabled")
			return
		}
		settings.Enabled = *update.Enabled
	}
	if update.GrantedCapabilities != nil {
		granted := []string{}
		for _, capability := range *update.GrantedCapabilities {
			if !slices.Contains(target.Meta.Capabilities, capability) {
				invalid("the plugin does not ask for the capability " + capability)
				return
			}
			if !slices.Contains(granted, capability) {
				granted = append(granted, capability)
			}
		}
		settings.Capabilities = granted
	}
	if update.NetworkOrigins != nil {
		if len(*update.NetworkOrigins) > contract.MaxPluginOrigins {
			invalid("a plugin may reach at most 16 origins")
			return
		}
		origins := []storage.PluginOrigin{}
		for _, input := range *update.NetworkOrigins {
			origin, err := plugin.NormalizeOrigin(input.Origin)
			if err != nil || len(origin) > contract.MaxPluginOriginLength {
				invalid("an origin is an http or https address with a host, such as https://memory.example.com:8443")
				return
			}
			if slices.ContainsFunc(origins, func(existing storage.PluginOrigin) bool { return existing.Origin == origin }) {
				invalid("the origin " + origin + " is listed twice")
				return
			}
			origins = append(origins, storage.PluginOrigin{Origin: origin, AllowPlaintext: input.AllowPlaintext && plugin.Plaintext(origin)})
		}
		settings.Origins = origins
	}
	if update.PrivacyTrusted != nil {
		settings.Trusted = *update.PrivacyTrusted
	}
	if update.TurnWaitMs != nil {
		if *update.TurnWaitMs < contract.MinPluginTurnWaitMs || *update.TurnWaitMs > contract.MaxPluginTurnWaitMs {
			invalid("the wait for a plugin is 1000 to 5000 ms")
			return
		}
		settings.TurnWaitMs = *update.TurnWaitMs
	}
	if update.ExcludedClients != nil {
		excluded := []string{}
		for _, client := range *update.ExcludedClients {
			if !contract.ClientType(client).Valid() {
				invalid("unknown client " + client)
				return
			}
			if !slices.Contains(excluded, client) {
				excluded = append(excluded, client)
			}
		}
		settings.ExcludedClients = excluded
	}
	if update.WorkspaceBinding != nil {
		settings.WorkspaceBinding = *update.WorkspaceBinding
	}
	if update.AutoRefresh != nil {
		settings.AutoRefresh = *update.AutoRefresh
	}
	if update.LookupEnabled != nil {
		settings.LookupEnabled = *update.LookupEnabled
	}
	if update.HookLimitsMs != nil {
		limits := maps.Clone(settings.HookLimitsMs)
		if limits == nil {
			limits = map[string]int{}
		}
		for hook, ms := range update.HookLimitsMs {
			bounds, known := contract.PluginHookLimits[hook]
			if !known {
				invalid("no extension point " + hook + " has a time limit")
				return
			}
			if ms == 0 {
				delete(limits, hook)
				continue
			}
			if ms < 1 || ms > bounds.MaxMs {
				invalid(fmt.Sprintf("the time limit of %s is 1 to %d ms", hook, bounds.MaxMs))
				return
			}
			limits[hook] = ms
		}
		settings.HookLimitsMs = limits
	}
	if update.FailClosed != nil {
		if *update.FailClosed && !target.Meta.FailClosed {
			invalid("the plugin does not ask to refuse requests it cannot decide")
			return
		}
		settings.FailClosed = *update.FailClosed
	}
	if update.UIEnabled != nil {
		settings.UIEnabled = *update.UIEnabled
	}
	if update.Config != nil {
		config := bytes.TrimSpace(update.Config)
		if len(config) > contract.MaxPluginConfigBytes || !json.Valid(config) || config[0] != '{' {
			invalid("the configuration is a JSON object of at most 64 KiB")
			return
		}
		settings.Config = json.RawMessage(config)
	}
	// A configuration fits the settings the plugin declares; its servers
	// become origins the plugin may reach, with their credentials.
	var binds map[string]string
	if target.Loaded() && (update.Config != nil || update.NetworkOrigins != nil || len(update.PlaintextConsent) > 0) {
		if update.Config != nil {
			if err := target.Meta.CheckConfig(settings.Config); err != nil {
				invalid(err.Error())
				return
			}
		}
		origins, credentials, err := serverOrigins(target.Meta, settings, stored, update.PlaintextConsent)
		var plaintext *plaintextError
		switch {
		case errors.As(err, &plaintext):
			writeError(writer, http.StatusUnprocessableEntity, "plaintext_consent_required", err.Error())
			return
		case err != nil:
			invalid(err.Error())
			return
		}
		settings.Origins, binds = origins, credentials
	}
	// What grants the plugin more takes the user's confirmation, where
	// nothing the plugin runs can click for them.
	if approval := pluginApproval(target, previous, settings); !approval.Empty() && request.Header.Get(contract.PluginApprovalHeader) == "" {
		writeJSON(writer, http.StatusConflict, approvalEnvelope{
			Error:     controlError{Code: "approval_required", Message: "this change grants the plugin more and needs the user's confirmation", Details: []errorDetail{}},
			RequestID: newRequestID(),
			Approval:  approval,
		})
		return
	}
	// Enabling registers the plugin's attributes; one whose key or marker
	// another owner holds keeps the plugin off.
	if settings.Enabled && !previous.Enabled && handler.metadata != nil {
		if err := handler.metadata.RegisterPlugin(ctx, target.Key, target.Attributes()); err != nil {
			if errors.Is(err, storage.ErrConflict) {
				writeError(writer, http.StatusConflict, "metadata_attribute_conflict", "an attribute this plugin registers has the key or marker of another attribute")
				return
			}
			writeError(writer, http.StatusInternalServerError, "metadata_unavailable", "could not register the plugin's attributes")
			return
		}
	}
	if err := handler.pluginStore.PutPluginSettings(ctx, settings); err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not save the plugin's settings")
		return
	}
	for name, origin := range binds {
		if err := handler.pluginStore.BindPluginCredential(ctx, target.Key, name, origin); err != nil && !errors.Is(err, storage.ErrNotFound) {
			writeError(writer, http.StatusInternalServerError, "credential_unavailable", "saved, but the credential could not follow its server")
			return
		}
	}
	if err := handler.plugins.Refresh(ctx); err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "saved, but the plugins could not apply the settings")
		return
	}
	// A plugin disabled, or whose lookups were taken back, voids what the
	// sessions were issued for them.
	current := handler.currentPlugin(target)
	if handler.pluginHost != nil && (!current.Enabled() || !current.Can(plugin.CapabilityTools) || !current.Settings.LookupEnabled) {
		handler.pluginHost.RevokePluginLookups(target.Key)
	}
	handler.writePluginDetail(writer, ctx, current)
}

// approvalEnvelope is the error of a change that needs the user's
// confirmation, with what it grants.
type approvalEnvelope struct {
	Error     controlError            `json:"error"`
	RequestID string                  `json:"request_id"`
	Approval  contract.PluginApproval `json:"approval"`
}

// pluginApproval describes what a change of settings grants the plugin
// beyond what it had: plain http to a server its credentials go to, the
// privacy check skipped, and refusing requests its entry hook cannot
// decide. Plugins are trusted code: what one declares the install review
// lists, and enabling it, or reaching an address, needs no confirmation.
func pluginApproval(target *plugin.Plugin, previous, next storage.PluginSettingsRecord) contract.PluginApproval {
	approval := contract.PluginApproval{Plugin: target.Key, Name: target.Meta.Name}
	if approval.Name == "" {
		approval.Name = target.Key
	}
	for _, origin := range next.Origins {
		index := slices.IndexFunc(previous.Origins, func(existing storage.PluginOrigin) bool { return existing.Origin == origin.Origin })
		if origin.AllowPlaintext && (index < 0 || !previous.Origins[index].AllowPlaintext) {
			approval.Plaintext = append(approval.Plaintext, origin.Origin)
		}
	}
	approval.Trusted = next.Trusted && !previous.Trusted
	approval.FailClosed = next.FailClosed && !previous.FailClosed
	return approval
}

// currentPlugin returns the plugin as the newest generation has it.
func (handler *Handler) currentPlugin(target *plugin.Plugin) *plugin.Plugin {
	if current := handler.plugins.Current().Plugin(target.Key); current != nil {
		return current
	}
	return target
}

func (handler *Handler) pluginCredential(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin, name string) {
	if _, declared := target.Meta.Credential(name); !declared {
		writeError(writer, http.StatusNotFound, "not_found", "the plugin declares no such credential")
		return
	}
	ctx := request.Context()
	switch request.Method {
	case http.MethodPut:
		var input contract.PluginCredentialInput
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		secret := []byte(input.Secret)
		defer clear(secret)
		if len(secret) == 0 || len(secret) > contract.MaxPluginSecretBytes || strings.ContainsAny(input.Secret, "\r\n") {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_credential", "a credential has 1 to 16384 bytes without line breaks")
			return
		}
		origin, err := plugin.NormalizeOrigin(input.Origin)
		if err != nil || !slices.ContainsFunc(target.Settings.Origins, func(allowed plugin.NetworkOrigin) bool { return allowed.Origin == origin }) {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_credential", "bind the credential to one of the origins the plugin may reach")
			return
		}
		if err := handler.pluginStore.PutPluginCredential(ctx, target.Key, name, origin, secret); err != nil {
			writeError(writer, http.StatusInternalServerError, "credential_unavailable", "could not save the credential")
			return
		}
	case http.MethodDelete:
		if err := handler.pluginStore.DeletePluginCredential(ctx, target.Key, name); err != nil && !errors.Is(err, storage.ErrNotFound) {
			writeError(writer, http.StatusInternalServerError, "credential_unavailable", "could not delete the credential")
			return
		}
	default:
		writer.Header().Set("Allow", "PUT, DELETE")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use PUT or DELETE")
		return
	}
	handler.writePluginDetail(writer, ctx, target)
}

func (handler *Handler) testPlugin(writer http.ResponseWriter, ctx context.Context, target *plugin.Plugin) {
	if !target.Has(plugin.HookTestConnection) {
		writeError(writer, http.StatusNotFound, "not_found", "the plugin has no connection test")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, pluginPageBudget)
	defer cancel()
	raw, info, err := target.Call(ctx, plugin.HookTestConnection, handler.pluginPageView(), plugin.CallOptions{NetworkBudget: pluginPageBudget})
	result := contract.PluginTestResult{ElapsedMs: int(info.Elapsed.Milliseconds()), Network: networkCount(info.Network)}
	var failure *plugin.CallError
	switch {
	case errors.As(err, &failure):
		result.Status, result.Summary, result.Reason = contract.PluginTestFailed, failure.Message, failure.Reason
	case err != nil:
		result.Status, result.Summary, result.Reason = contract.PluginTestFailed, "the test did not run", plugin.ReasonUnavailable
	default:
		var reported struct {
			Status  string          `json:"status"`
			Summary string          `json:"summary"`
			View    json.RawMessage `json:"view"`
		}
		if json.Unmarshal(raw, &reported) != nil || (reported.Status != contract.PluginTestOK && reported.Status != contract.PluginTestError) {
			result.Status, result.Summary, result.Reason = contract.PluginTestFailed, "the test returned no status of ok or error", plugin.ReasonInvalidResult
			break
		}
		result.Status = reported.Status
		result.Summary = contract.ClampRunes(cleanSummary(reported.Summary), contract.MaxPluginRecordSummaryRunes)
		if target.Can(plugin.CapabilityViews) {
			result.View, result.Truncated = view.Parse(reported.View)
		}
	}
	handler.pluginTestsMu.Lock()
	if handler.pluginTests == nil {
		handler.pluginTests = map[string]contract.PluginLastTest{}
	}
	handler.pluginTests[target.Key] = contract.PluginLastTest{PluginTestResult: result, TestedAt: time.Now().UTC()}
	handler.pluginTestsMu.Unlock()
	writeJSON(writer, http.StatusOK, result)
}

func (handler *Handler) pluginStatus(writer http.ResponseWriter, ctx context.Context, target *plugin.Plugin) {
	stats := target.Stats()
	status := contract.PluginStatus{
		Calls:        stats.Calls,
		Failures:     map[string]int{},
		MemoryLimits: stats.Failures[plugin.ReasonMemory],
		Timeouts:     stats.Failures[plugin.ReasonTimeout],
		Network:      networkCount(stats.Network),
	}
	for reason, count := range stats.Failures {
		status.Failures[reason] = count
	}
	handler.pluginTestsMu.Lock()
	if last, found := handler.pluginTests[target.Key]; found {
		status.LastTest = &last
	}
	handler.pluginTestsMu.Unlock()
	if handler.metadataStore != nil {
		for _, attribute := range target.Meta.Metadata {
			values, err := handler.metadataStore.MetadataValueStats(ctx, attribute.Key, maxPluginBindingValues)
			if err != nil {
				continue
			}
			bindings := contract.PluginBindings{Key: attribute.Key, Label: attribute.Label, Values: []contract.PluginBindingValue{}}
			for _, value := range values {
				bindings.Values = append(bindings.Values, contract.PluginBindingValue{Value: value.Value, Sessions: value.Sessions, LastUsedAt: value.LastUsedAt})
			}
			status.Bindings = append(status.Bindings, bindings)
		}
	}
	if target.Has(plugin.HookDescribeStatus) && target.Can(plugin.CapabilityViews) {
		ctx, cancel := context.WithTimeout(ctx, pluginPageBudget)
		defer cancel()
		raw, _, err := target.Call(ctx, plugin.HookDescribeStatus, handler.pluginPageView(), plugin.CallOptions{NetworkBudget: pluginPageBudget})
		var failure *plugin.CallError
		switch {
		case errors.As(err, &failure):
			status.ViewError = failure.Message
		case err != nil:
			status.ViewError = "the overview did not run"
		default:
			status.View, status.Truncated = view.Parse(raw)
		}
	}
	writeJSON(writer, http.StatusOK, status)
}

func networkCount(stats plugin.NetworkStats) contract.PluginNetworkCount {
	return contract.PluginNetworkCount{Requests: stats.Requests, Denied: stats.Denied, Errors: stats.Errors, WaitMs: int(stats.Wait.Milliseconds())}
}

// cleanSummary keeps a one-line summary from a plugin as plain text.
func cleanSummary(text string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 0x7f {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, "�"))
}

// registerEnabledPluginAttributes registers again the attributes of every
// enabled plugin, whose manifest a rescan may have changed. A conflict
// leaves that plugin's attributes as they were.
func (handler *Handler) registerEnabledPluginAttributes(ctx context.Context) {
	if handler.metadata == nil {
		return
	}
	for _, target := range handler.plugins.Current().Plugins() {
		if target.Enabled() {
			_ = handler.metadata.RegisterPlugin(ctx, target.Key, target.Attributes())
		}
	}
}

// plaintextError is a server reached over plain http without the user's
// consent.
type plaintextError struct{ setting, origin string }

func (failure *plaintextError) Error() string {
	return "the server " + failure.origin + " of " + failure.setting + " is plain http; reaching it needs the user's consent"
}

// serverOrigins returns the origins a plugin may reach: the user's own, and
// in place of what the server settings had, the origin of each address the
// configuration gives a server, marked with its setting. Plain http beyond
// this machine needs consent, given now or for the same origin before. It
// also returns, for each credential a server sends, the origin to bind it
// to.
func serverOrigins(meta plugin.Meta, settings storage.PluginSettingsRecord, stored []storage.PluginOrigin, consent []string) ([]storage.PluginOrigin, map[string]string, error) {
	origins := []storage.PluginOrigin{}
	for _, origin := range settings.Origins {
		if origin.Setting == "" {
			origins = append(origins, origin)
		}
	}
	consented := func(origin string) bool {
		for _, given := range consent {
			if normalized, err := plugin.NormalizeOrigin(given); err == nil && normalized == origin {
				return true
			}
		}
		allowed := func(existing storage.PluginOrigin) bool { return existing.Origin == origin && existing.AllowPlaintext }
		return slices.ContainsFunc(stored, allowed) || slices.ContainsFunc(settings.Origins, allowed)
	}
	servers := meta.Servers(settings.Config)
	binds := map[string]string{}
	for _, setting := range meta.Settings {
		address, found := servers[setting.Key]
		if !found {
			continue
		}
		origin, err := plugin.NormalizeOrigin(address)
		if err != nil || len(origin) > contract.MaxPluginOriginLength {
			return nil, nil, errors.New("the server of " + setting.Key + " is not an http or https address with a host")
		}
		allow := false
		if plugin.Plaintext(origin) {
			if !consented(origin) {
				return nil, nil, &plaintextError{setting: setting.Key, origin: origin}
			}
			allow = true
		}
		// The server owns its origin, also when the user listed it before.
		origins = slices.DeleteFunc(origins, func(existing storage.PluginOrigin) bool { return existing.Origin == origin })
		origins = append(origins, storage.PluginOrigin{Origin: origin, AllowPlaintext: allow, Setting: setting.Key})
		if setting.Credential != "" {
			binds[setting.Credential] = origin
		}
	}
	if len(origins) > contract.MaxPluginOrigins {
		return nil, nil, errors.New("a plugin may reach at most 16 origins")
	}
	return origins, binds, nil
}
