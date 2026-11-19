package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/pluginstats"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// PluginHostPath serves the settings of every plugin at once.
const PluginHostPath = "/control/v1/plugin-host"

// PluginHost runs the plugins' part in requests; *pluginhost.Host
// satisfies it.
type PluginHost interface {
	Settings() contract.PluginHostSettings
	UpdateSettings(context.Context, contract.PluginHostSettingsUpdate) (contract.PluginHostSettings, error)
	SessionPlugins(ctx context.Context, session string) ([]contract.SessionPlugin, error)
	PauseSession(ctx context.Context, plugin, session string, paused bool, turn int) error
	Refresh(plugin, session string)
	SessionChoices(session string) map[string][]string
	SessionWorkspace(session string) (name string, ok bool)
	WorkspaceValue(ctx context.Context, session, key string) (value string, ok bool, err error)
	BindWorkspace(ctx context.Context, session, key, value string, replace bool) (bool, error)
	Action(ctx context.Context, target *plugin.Plugin, input contract.PluginActionInput) contract.PluginActionResult
	LookupCredentials(plugin string) int
	RevokeSessionLookups(session string)
	RevokePluginLookups(plugin string) int
	// Breakers returns the extension points of plugin kept out of requests
	// after too many failures, and until when.
	Breakers(plugin string) map[string]time.Time
	// UICall answers a call of the plugin's interface code.
	UICall(ctx context.Context, target *plugin.Plugin, name string, input json.RawMessage) (json.RawMessage, error)
	// The logins plugins log providers in with, and the credentials they
	// keep.
	StartLogin(ctx context.Context, service contract.Service) (pluginhost.LoginView, error)
	Login(ctx context.Context, id contract.ServiceID) (pluginhost.LoginView, bool)
	SubmitLogin(ctx context.Context, id contract.ServiceID, input pluginhost.LoginInput) (pluginhost.LoginView, error)
	CancelLogin(id contract.ServiceID)
	PluginCredentialOf(ctx context.Context, service contract.Service) pluginhost.CredentialView
	RevokePluginCredential(ctx context.Context, service contract.Service)
	// The balance of a plugin's provider, and whether the plugin rests it.
	ProviderBalance(ctx context.Context, service contract.Service) (pluginhost.ProviderState, error)
	ProviderStateOf(id contract.ServiceID) pluginhost.ProviderState
	// DryRunRequestPlugin runs a request processing instance over a
	// request without sending it.
	DryRunRequestPlugin(ctx context.Context, instance contract.RequestPluginInstance, protocol contract.ProtocolID, body json.RawMessage, reply *string) (pluginhost.RequestDryRun, error)
}

// pluginHostRequiredRole lets observers read the settings of every plugin
// and keeps changing them to operators.
func pluginHostRequiredRole(request *http.Request) Role {
	if isSafeMethod(request.Method) {
		return RoleObserver
	}
	return RoleOperator
}

// pluginHostResource serves the pause of every plugin, the injection budget
// and the interface language the desktop tells Core.
func (handler *Handler) pluginHostResource(writer http.ResponseWriter, request *http.Request) {
	if handler.pluginHost == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
		return
	}
	switch request.Method {
	case http.MethodGet:
		writeJSON(writer, http.StatusOK, handler.pluginHost.Settings())
	case http.MethodPatch:
		var update contract.PluginHostSettingsUpdate
		if !decodeControlJSON(writer, request, &update) {
			return
		}
		merged := handler.pluginHost.Settings()
		if update.Paused != nil {
			merged.Paused = *update.Paused
		}
		if update.InjectionBudgetBytes != nil {
			merged.InjectionBudgetBytes = *update.InjectionBudgetBytes
		}
		if update.Language != nil {
			merged.Language = *update.Language
		}
		if update.Order != nil {
			merged.Order = slices.Clone(*update.Order)
		}
		if update.WebUI != nil {
			// Plugin code in the web console can do what the console
			// can, which takes the user's confirmation.
			if *update.WebUI && !merged.WebUI && request.Header.Get(contract.PluginApprovalHeader) == "" {
				writeError(writer, http.StatusConflict, "approval_required", "loading plugin interface code in the web console needs the user's confirmation")
				return
			}
			merged.WebUI = *update.WebUI
		}
		if update.NpmRegistry != nil {
			merged.NpmRegistry = strings.TrimRight(*update.NpmRegistry, "/")
		}
		if update.RequestPlugins != nil {
			instances := slices.Clone(*update.RequestPlugins)
			if err := handler.checkRequestPlugins(request.Context(), instances); err != nil {
				writeError(writer, http.StatusUnprocessableEntity, "invalid_request_plugin", err.Error())
				return
			}
			update.RequestPlugins = &instances
			merged.RequestPlugins = instances
		}
		if err := merged.Validate(); err != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_plugin_host_settings", err.Error())
			return
		}
		settings, err := handler.pluginHost.UpdateSettings(request.Context(), update)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not save the settings of the plugins")
			return
		}
		writeJSON(writer, http.StatusOK, settings)
	default:
		writer.Header().Set("Allow", "GET, PATCH")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PATCH")
	}
}

// sessionPlugins serves the plugins' part in a session under
// /control/v1/request-sessions/{id}/plugins: the resident block each keeps
// in memory, the pause of one, and the refresh that makes the session's next
// request compute its block again.
func (handler *Handler) sessionPlugins(writer http.ResponseWriter, request *http.Request) {
	if handler.pluginHost == nil || handler.plugins == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
		return
	}
	rest := strings.TrimPrefix(request.URL.Path, RequestSessionsPath+"/")
	rawID, path, _ := strings.Cut(rest, "/plugins")
	id := contract.SessionID(rawID)
	key, action, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if id.Validate() != nil || (path != "" && key == "") || (action != "" && action != "refresh") {
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	ctx := request.Context()
	detail, err := handler.requestRecords.GetRequestSession(ctx, string(id))
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	if key == "" {
		if allowMethods(writer, request, http.MethodGet) {
			handler.writeSessionPlugins(writer, ctx, id)
		}
		return
	}
	target := handler.plugins.Current().Plugin(key)
	if !contract.ValidPluginKey(key) || target == nil {
		writeError(writer, http.StatusNotFound, "not_found", "plugin not found")
		return
	}
	switch {
	case action == "refresh":
		if !allowMethods(writer, request, http.MethodPost) {
			return
		}
		handler.pluginHost.Refresh(key, string(id))
	default:
		if !allowMethods(writer, request, http.MethodPut) {
			return
		}
		var input contract.SessionPluginInput
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		turn := detail.TurnCount
		for _, record := range detail.Turns {
			if record.TurnIndex != nil && *record.TurnIndex > turn {
				turn = *record.TurnIndex
			}
		}
		if err := handler.pluginHost.PauseSession(ctx, key, string(id), input.Paused, turn); err != nil {
			writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not save the plugin's part in the session")
			return
		}
	}
	handler.writeSessionPlugins(writer, ctx, id)
}

func (handler *Handler) writeSessionPlugins(writer http.ResponseWriter, ctx context.Context, id contract.SessionID) {
	parts, err := handler.pluginHost.SessionPlugins(ctx, string(id))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not read the plugins' part in the session")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"plugins": parts})
}

// pluginAction runs a plugin's onAction hook for a click on a button of one
// of its records.
func (handler *Handler) pluginAction(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin) {
	if handler.pluginHost == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
		return
	}
	if !target.Enabled() || !target.Has(plugin.HookOnAction) {
		writeError(writer, http.StatusNotFound, "not_found", "the plugin takes no actions")
		return
	}
	var input contract.PluginActionInput
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if !contract.ValidPluginActionID(input.Action) || contract.SessionID(input.Session).Validate() != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_plugin_action", "an action names one of the record's buttons and its session")
		return
	}
	if _, err := handler.requestRecords.GetRequestSession(request.Context(), input.Session); err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, handler.pluginHost.Action(request.Context(), target, input))
}

// clearPluginData removes what the host keeps for a plugin: the workspaces
// bound to its attributes' values, its own values, the files of its own
// directory, or the credentials of its lookups, which each session's next
// request issues anew.
func (handler *Handler) clearPluginData(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin, what string) {
	if !allowMethods(writer, request, http.MethodDelete) {
		return
	}
	ctx := request.Context()
	removed := 0
	var err error
	switch what {
	case "workspaces":
		if handler.metadataStore == nil {
			writeError(writer, http.StatusServiceUnavailable, "unavailable", "metadata is unavailable")
			return
		}
		removed, err = handler.metadataStore.DeleteWorkspaceValues(ctx, pluginAttributeKeys(target))
	case "values":
		removed, err = handler.pluginStore.DeletePluginValues(ctx, target.Key)
	case "files":
		removed, err = target.ClearData()
	case "lookup-credentials":
		if handler.pluginHost == nil {
			writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
			return
		}
		removed = handler.pluginHost.RevokePluginLookups(target.Key)
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not clear the plugin's data")
		return
	}
	writeJSON(writer, http.StatusOK, contract.PluginClearResult{Removed: removed})
}

// pluginStats sums the plugin's account over its last days, by the
// gateway's calendar, from this computer's request records: counts, sizes,
// durations and reasons only.
func (handler *Handler) pluginStats(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin) {
	days := contract.DefaultPluginStatsDays
	if raw := request.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > contract.MaxPluginStatsDays {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_plugin_stats", "days is 1 to 30")
			return
		}
		days = parsed
	}
	ctx := request.Context()
	account := pluginstats.New(target.Key, pluginAttributeKeys(target), time.Local, time.Now(), days)
	if err := handler.pluginStore.ScanPluginActivity(ctx, account.Since(), func(activity storage.PluginActivity) error {
		account.Add(activity)
		return nil
	}); err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not read the plugin's activity")
		return
	}
	// Whether a session asked is still without a value, and whether one
	// bound by its workspace moved, is a matter of its binding now.
	current := map[string]map[string]storage.MetadataValueRecord{}
	if handler.metadataStore != nil && len(target.Meta.Metadata) > 0 {
		values, err := handler.metadataStore.ListMetadataValues(ctx, storage.MetadataTargetSession)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "metadata_unavailable", "could not read the sessions' bindings")
			return
		}
		for _, value := range values {
			if current[value.TargetID] == nil {
				current[value.TargetID] = map[string]storage.MetadataValueRecord{}
			}
			current[value.TargetID][value.Key] = value
		}
	}
	writeJSON(writer, http.StatusOK, account.Result(func(session, key string) (storage.MetadataValueRecord, bool) {
		value, found := current[session][key]
		return value, found
	}))
}

func pluginAttributeKeys(target *plugin.Plugin) []string {
	keys := make([]string, 0, len(target.Meta.Metadata))
	for _, attribute := range target.Meta.Metadata {
		keys = append(keys, attribute.Key)
	}
	return keys
}

// pluginPageView is what describeStatus and testConnection read: the
// interface language, for the plugin to write its views in.
type pluginPageView struct {
	Language string `json:"language"`
}

func (handler *Handler) pluginPageView() pluginPageView {
	language := contract.DefaultPluginLanguage
	if handler.pluginHost != nil {
		language = handler.pluginHost.Settings().Language
	}
	return pluginPageView{Language: language}
}
