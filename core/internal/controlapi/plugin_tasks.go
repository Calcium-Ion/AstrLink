package controlapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/taskplugin"
)

// PluginTasks are the tasks of task plugins; *taskplugin.Engine satisfies
// it.
type PluginTasks interface {
	Store() storage.PluginTaskStore
	Artifacts(ctx context.Context, task storage.PluginTask) ([]taskplugin.Artifact, error)
	RemoveResults(ctx context.Context, plugin string) error
}

// taskProvider is the provider a task plugin offers on the providers page,
// named in language.
func taskProvider(meta *plugin.TaskMeta, language string) contract.PluginProvider {
	auth := contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}
	if meta.Auth.Kind() == plugin.TaskAuthNone {
		auth = contract.ServiceAuth{Scheme: contract.AuthSchemeNone}
	}
	provider := contract.PluginProvider{
		ID: contract.PluginProviderTask, Name: meta.Name.In(language), BaseURL: taskplugin.DefaultBaseURL(meta), Auth: auth,
		Models: slices.Clone(meta.Models), Protocols: []contract.PluginProviderProtocol{},
	}
	for _, capability := range taskCapabilities(meta) {
		provider.Protocols = append(provider.Protocols, contract.PluginProviderProtocol{Protocol: capability.Protocol, Stream: capability.Streaming, Sync: true})
	}
	return provider
}

// taskCapabilities are what routing reads of a task plugin's provider:
// the gateway protocols the plugin claims, and its native routes and
// /v1/tasks.
func taskCapabilities(meta *plugin.TaskMeta) []contract.Capability {
	var capabilities []contract.Capability
	for _, claim := range meta.Protocols {
		switch claim.Name {
		case plugin.TaskProtocolImage:
			capabilities = append(capabilities, contract.Capability{Protocol: contract.ProtocolOpenAIImages, Mode: contract.CapabilityModeNative})
		case plugin.TaskProtocolVideo:
			capabilities = append(capabilities, contract.Capability{Protocol: contract.ProtocolOpenAIVideos, Mode: contract.CapabilityModeNative})
		case plugin.TaskProtocolResponses:
			capabilities = append(capabilities, contract.Capability{
				Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative,
				Streaming: slices.Contains(claim.Supports, plugin.TaskModeStream),
			})
		}
	}
	return append(capabilities, contract.Capability{Protocol: contract.ProtocolPluginNative, Mode: contract.CapabilityModeNative})
}

// applyTaskProvider fills in what a provider of a task plugin takes from
// the plugin: its capabilities, and its address when the user named none.
// It reports false for a provider of any other kind.
func (handler *Handler) applyTaskProvider(service *contract.Service) (bool, error) {
	if service.Kind == contract.ServiceKindPlugin && service.Plugin != nil && service.Plugin.Provider == contract.PluginProviderRelay {
		return true, handler.applyRelayProvider(service)
	}
	if service.Kind != contract.ServiceKindPlugin || service.Plugin == nil || service.Plugin.Provider != contract.PluginProviderTask {
		return false, nil
	}
	if handler.plugins == nil {
		return true, errors.New("plugins are unavailable")
	}
	target := handler.plugins.Current().Plugin(service.Plugin.Key)
	if target == nil || target.Task() == nil {
		return true, fmt.Errorf("plugin %q is not an installed task plugin", service.Plugin.Key)
	}
	meta := target.Task()
	if service.Plugin.Upstream == contract.PluginUpstreamNewAPI && !slices.Contains(meta.Upstreams, contract.PluginUpstreamNewAPI) {
		return true, fmt.Errorf("the task plugin %s cannot drive another new-api gateway", meta.Key)
	}
	service.Capabilities = taskCapabilities(meta)
	if service.HTTP != nil && service.HTTP.BaseURL == "" {
		service.HTTP.BaseURL = taskplugin.DefaultBaseURL(meta)
	}
	if meta.Auth.Kind() == plugin.TaskAuthNone && service.HTTP != nil {
		service.HTTP.Auth = contract.ServiceAuth{Scheme: contract.AuthSchemeNone}
	}
	return true, nil
}

// syncTaskProviders brings the capabilities of every provider of a task
// plugin or of a new-api plugin's provider part in line with the plugin as
// it now is, after an install or reload.
func (handler *Handler) syncTaskProviders(ctx context.Context) {
	if handler.serviceStore == nil || handler.plugins == nil {
		return
	}
	kind := contract.ServiceKindPlugin
	cursor := ""
	for {
		page, err := handler.serviceStore.ListServices(ctx, storage.ServiceListOptions{Limit: 200, Cursor: cursor, Kind: &kind})
		if err != nil {
			return
		}
		for _, record := range page.Items {
			service := record.Service
			if service.Plugin == nil || (service.Plugin.Provider != contract.PluginProviderTask && service.Plugin.Provider != contract.PluginProviderRelay) {
				continue
			}
			target := handler.plugins.Current().Plugin(service.Plugin.Key)
			var capabilities []contract.Capability
			switch {
			case service.Plugin.Provider == contract.PluginProviderRelay && target != nil && target.NewAPI() != nil:
				capabilities = handler.relayCapabilities(target.NewAPI())
			case service.Plugin.Provider == contract.PluginProviderTask && target != nil && target.Task() != nil:
				capabilities = taskCapabilities(target.Task())
			default:
				continue
			}
			if len(capabilities) == 0 {
				continue
			}
			if slices.Equal(capabilities, service.Capabilities) {
				continue
			}
			service.Capabilities = capabilities
			_, _ = handler.serviceStore.UpdateService(ctx, service, storage.CredentialMutation{}, record.ETag)
		}
		if page.NextCursor == "" {
			return
		}
		cursor = page.NextCursor
	}
}

// pluginTasks lists a task plugin's tasks, the newest first, or clears
// those that finished.
func (handler *Handler) pluginTasks(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin) {
	if handler.pluginTaskEngine == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugin tasks are unavailable")
		return
	}
	store := handler.pluginTaskEngine.Store()
	ctx := request.Context()
	switch request.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
		tasks, next, err := store.ListPluginTasks(ctx, target.Key, request.URL.Query().Get("cursor"), limit)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_cursor", "the cursor is not one this list gave")
			return
		}
		_, running, err := store.CountPluginTasks(ctx, target.Key)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "plugin_tasks_unavailable", "could not read the plugin's tasks")
			return
		}
		page := contract.PluginTaskPage{Items: []contract.PluginTaskRecord{}, NextCursor: next, Running: running}
		for _, task := range tasks {
			record := contract.PluginTaskRecord{
				ID: task.ID, PluginKey: task.PluginKey, ServiceID: task.ServiceID, Surface: task.Surface, Action: task.Action,
				Model: task.Model, Status: task.Status, Progress: task.Progress, FailReason: task.FailReason, Usage: task.Usage,
				CreatedAt: task.CreatedAt.UTC().Format(timeLayout), UpdatedAt: task.UpdatedAt.UTC().Format(timeLayout), RequestID: task.RequestID,
			}
			if !task.FinishedAt.IsZero() {
				record.FinishedAt = task.FinishedAt.UTC().Format(timeLayout)
			}
			if task.Status == contract.PluginTaskSuccess && task.Retain {
				if artifacts, err := handler.pluginTaskEngine.Artifacts(ctx, task); err == nil {
					for _, artifact := range artifacts {
						record.Artifacts = append(record.Artifacts, artifact.Type)
					}
				}
			}
			page.Items = append(page.Items, record)
		}
		writeJSON(writer, http.StatusOK, page)
	case http.MethodDelete:
		if err := handler.pluginTaskEngine.RemoveResults(ctx, target.Key); err != nil {
			writeError(writer, http.StatusInternalServerError, "plugin_tasks_unavailable", "could not clear the plugin's tasks")
			return
		}
		deleted, err := store.DeletePluginTasks(ctx, target.Key)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "plugin_tasks_unavailable", "could not clear the plugin's tasks")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]int{"deleted": deleted})
	default:
		writer.Header().Set("Allow", "GET, DELETE")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or DELETE")
	}
}

// timeLayout is how the control API writes times.
const timeLayout = "2006-01-02T15:04:05Z07:00"
