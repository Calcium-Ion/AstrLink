package controlapi

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/plugininstall"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// PluginInstaller installs plugins from source; *plugininstall.Installer
// satisfies it.
type PluginInstaller interface {
	Preview(ctx context.Context, link string, existing func(key string) *plugininstall.Existing) (contract.PluginInstallReview, error)
	Staged(token string) (contract.PluginInstallReview, bool)
	Install(token string) (string, error)
	Versions(key string) []contract.PluginInstalledVersion
	RollbackReview(key, id string, current *plugininstall.Existing) (contract.PluginInstallReview, error)
	Rollback(key, id string) error
	Uninstall(key string) error
	CheckUpdate(ctx context.Context, record contract.PluginInstallRecord) (contract.PluginUpdateCheck, error)
	Market(ctx context.Context, index string, installed func(key string) plugininstall.Installed) (contract.PluginMarket, error)
	FindByCode(ctx context.Context, code string, installed func(key string) plugininstall.Installed) (contract.PluginMarket, error)
	MarketDoc(ctx context.Context, index, key, version, doc string) (contract.PluginMarketDoc, error)
}

// installFailureEnvelope is the error of an install that cannot go on,
// with why.
type installFailureEnvelope struct {
	Error     controlError                  `json:"error"`
	RequestID string                        `json:"request_id"`
	Failure   contract.PluginInstallFailure `json:"failure"`
}

// writeInstallFailure answers an installer error: why the plugin cannot be
// installed, the installer being busy, or an error of Core.
func writeInstallFailure(writer http.ResponseWriter, err error) {
	var failure *plugininstall.Failure
	if !errors.As(err, &failure) {
		writeError(writer, http.StatusInternalServerError, "plugin_install_unavailable", "could not install the plugin")
		return
	}
	status, code := http.StatusUnprocessableEntity, "plugin_install_failed"
	switch failure.Reason {
	case contract.PluginInstallBusy:
		status, code = http.StatusConflict, "plugin_install_busy"
	case contract.PluginInstallExpired:
		status, code = http.StatusGone, "plugin_install_expired"
	}
	writeJSON(writer, status, installFailureEnvelope{
		Error:     controlError{Code: code, Message: failure.Error(), Details: []errorDetail{}},
		RequestID: newRequestID(),
		Failure:   failure.PluginInstallFailure,
	})
}

// existingPlugin describes the plugin key names now, for a review to
// compare with.
func (handler *Handler) existingPlugin(key string) *plugininstall.Existing {
	target := handler.plugins.Current().Plugin(key)
	if target == nil {
		return nil
	}
	existing := &plugininstall.Existing{
		Origin: string(target.Origin), Name: target.Key, Development: target.Development != nil,
		Installed: target.Installed, Client: target.Client,
	}
	if target.Loaded() {
		existing.Loaded = true
		existing.Name, existing.Version = target.Meta.Name, target.Meta.Version
		existing.Capabilities = slices.Clone(target.Meta.Capabilities)
		if target.Meta.Network != nil {
			existing.NetworkPurpose = target.Meta.Network.Purpose
		}
	}
	return existing
}

// pluginSources serves /plugins/sources/preview, which fetches, builds and
// reviews a plugin from a GitHub link, and /plugins/sources/install, which
// installs what a review staged once the user confirmed it.
func (handler *Handler) pluginSources(writer http.ResponseWriter, request *http.Request, action string) {
	if handler.pluginInstaller == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "installing plugins is unavailable")
		return
	}
	if action == "preview" {
		var input contract.PluginInstallPreviewRequest
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		review, err := handler.pluginInstaller.Preview(request.Context(), input.URL, handler.existingPlugin)
		if err != nil {
			writeInstallFailure(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, review)
		return
	}
	var input contract.PluginInstallRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	review, ok := handler.pluginInstaller.Staged(input.Token)
	if !ok {
		writeInstallFailure(writer, &plugininstall.Failure{PluginInstallFailure: contract.PluginInstallFailure{Reason: contract.PluginInstallExpired}})
		return
	}
	// New code in the plugin directory takes the user's confirmation,
	// where nothing a plugin runs can click for them.
	if request.Header.Get(contract.PluginApprovalHeader) == "" {
		writeApprovalRequired(writer, contract.PluginApproval{Plugin: review.Key, Name: review.Name, Install: &review})
		return
	}
	// A plugin new to this computer starts disabled, even where a plugin of
	// its key left settings behind; it keeps their configuration and data
	// for when the user enables it.
	if handler.plugins.Current().Plugin(review.Key) == nil {
		if err := handler.disableLeftSettings(request.Context(), review.Key); err != nil {
			writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not read the plugin's settings")
			return
		}
	}
	key, err := handler.pluginInstaller.Install(input.Token)
	if err != nil {
		writeInstallFailure(writer, err)
		return
	}
	handler.reloadInstalled(writer, request.Context(), key)
}

// disableLeftSettings turns off the settings a plugin of key left behind.
func (handler *Handler) disableLeftSettings(ctx context.Context, key string) error {
	if handler.pluginStore == nil {
		return nil
	}
	settings, err := handler.pluginStore.GetPluginSettings(ctx, key)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && !settings.Enabled) {
		return nil
	}
	if err != nil {
		return err
	}
	settings.Enabled = false
	return handler.pluginStore.PutPluginSettings(ctx, settings)
}

// writeApprovalRequired answers a change that waits for the user's
// confirmation, with what it grants.
func writeApprovalRequired(writer http.ResponseWriter, approval contract.PluginApproval) {
	writeJSON(writer, http.StatusConflict, approvalEnvelope{
		Error:     controlError{Code: "approval_required", Message: "this change needs the user's confirmation", Details: []errorDetail{}},
		RequestID: newRequestID(),
		Approval:  approval,
	})
}

// reloadInstalled loads what an install or a rollback put in place and
// answers with the plugin, as the registry now has it.
func (handler *Handler) reloadInstalled(writer http.ResponseWriter, ctx context.Context, key string) {
	if err := handler.plugins.Reload(ctx); err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_reload_failed", "installed, but the plugins could not be reloaded")
		return
	}
	// An enabled plugin's new manifest may register other attributes, and
	// a task plugin's claim other protocols.
	handler.registerEnabledPluginAttributes(ctx)
	handler.syncTaskProviders(ctx)
	target := handler.plugins.Current().Plugin(key)
	if target == nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "installed, but the plugin is not among the plugins")
		return
	}
	handler.writePluginDetail(writer, ctx, target)
}

// pluginSource serves a plugin installed from source:
// POST source/check looks for an update, POST source/rollback puts back a
// version an install replaced, and DELETE source uninstalls it.
func (handler *Handler) pluginSource(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin, action string) {
	if handler.pluginInstaller == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "installing plugins is unavailable")
		return
	}
	switch action {
	case "":
		if !allowMethods(writer, request, http.MethodDelete) {
			return
		}
		if err := handler.pluginInstaller.Uninstall(target.Key); err != nil {
			if errors.Is(err, plugininstall.ErrNotInstalled) {
				writeError(writer, http.StatusConflict, "plugin_not_installed", "the plugin was not installed from source")
				return
			}
			writeError(writer, http.StatusInternalServerError, "plugin_install_unavailable", "could not uninstall the plugin")
			return
		}
		if err := handler.plugins.Reload(request.Context()); err != nil {
			writeError(writer, http.StatusInternalServerError, "plugin_reload_failed", "uninstalled, but the plugins could not be reloaded")
			return
		}
		handler.writePluginList(writer)
	case "check":
		if !allowMethods(writer, request, http.MethodPost) {
			return
		}
		if target.Installed == nil {
			writeError(writer, http.StatusConflict, "plugin_not_installed", "the plugin was not installed from source")
			return
		}
		check, err := handler.pluginInstaller.CheckUpdate(request.Context(), *target.Installed)
		if err != nil {
			writeInstallFailure(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, check)
	case "rollback":
		if !allowMethods(writer, request, http.MethodPost) {
			return
		}
		var input contract.PluginRollbackRequest
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		review, err := handler.pluginInstaller.RollbackReview(target.Key, input.Version, handler.existingPlugin(target.Key))
		if err != nil {
			var failure *plugininstall.Failure
			if errors.As(err, &failure) {
				writeInstallFailure(writer, err)
				return
			}
			writeError(writer, http.StatusNotFound, "plugin_version_not_found", "the plugin has no such version kept")
			return
		}
		if request.Header.Get(contract.PluginApprovalHeader) == "" {
			writeApprovalRequired(writer, contract.PluginApproval{Plugin: target.Key, Name: review.Name, Install: &review, Rollback: true})
			return
		}
		if err := handler.pluginInstaller.Rollback(target.Key, input.Version); err != nil {
			writeError(writer, http.StatusInternalServerError, "plugin_install_unavailable", "could not put the version back")
			return
		}
		handler.reloadInstalled(writer, request.Context(), target.Key)
	default:
		writeError(writer, http.StatusNotFound, "not_found", "plugin endpoint not found")
	}
}

// pluginMarket lists what a plugin marketplace offers, the official one
// unless index names another; with code, the one plugin of that code.
func (handler *Handler) pluginMarket(writer http.ResponseWriter, request *http.Request) {
	if handler.pluginInstaller == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "installing plugins is unavailable")
		return
	}
	installed := func(key string) plugininstall.Installed {
		target := handler.plugins.Current().Plugin(key)
		if target == nil || target.Task() == nil {
			return plugininstall.Installed{}
		}
		now := plugininstall.Installed{Version: target.Task().Version}
		if record := target.Installed; record != nil && record.Source.Kind == contract.PluginSourceMarket {
			now.SHA256 = record.Source.Commit
		}
		return now
	}
	query := request.URL.Query()
	var market contract.PluginMarket
	var err error
	if code := query.Get("code"); code != "" {
		market, err = handler.pluginInstaller.FindByCode(request.Context(), code, installed)
	} else {
		market, err = handler.pluginInstaller.Market(request.Context(), query.Get("index"), installed)
	}
	if err != nil {
		writeInstallFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, market)
}

// pluginMarketDoc answers a marketplace version's usage guide or release
// notes.
func (handler *Handler) pluginMarketDoc(writer http.ResponseWriter, request *http.Request) {
	if handler.pluginInstaller == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "installing plugins is unavailable")
		return
	}
	query := request.URL.Query()
	key, doc := query.Get("key"), query.Get("doc")
	if !contract.ValidPluginKey(key) || (doc != contract.PluginMarketReadme && doc != contract.PluginMarketChangelog) {
		writeError(writer, http.StatusBadRequest, "invalid_request", "key and doc (readme or changelog) are required")
		return
	}
	found, err := handler.pluginInstaller.MarketDoc(request.Context(), query.Get("index"), key, query.Get("version"), doc)
	if err != nil {
		writeInstallFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, found)
}
