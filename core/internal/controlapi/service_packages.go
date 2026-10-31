package controlapi

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

const ServicePackagesPath = "/control/v1/service-packages"

// ServicePackageLister derives service packages the way routing groups
// services. Supply the same resolver used by ingress.
type ServicePackageLister interface {
	ServicePackages(context.Context) ([]contract.ServicePackage, error)
}

func (handler *Handler) servicePackagesResource(writer http.ResponseWriter, request *http.Request) {
	if handler.servicePackages == nil || handler.routingSettings == nil {
		writeError(writer, http.StatusServiceUnavailable, "service_packages_unavailable", "service packages are unavailable")
		return
	}
	rawID, item := strings.CutPrefix(request.URL.EscapedPath(), ServicePackagesPath+"/")
	switch {
	case !item && request.Method == http.MethodGet:
		packages, err := handler.servicePackages.ServicePackages(request.Context())
		if err != nil {
			handler.writeStoreError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, contract.ServicePackageList{Packages: packages})
	case !item:
		writeMethodNotAllowed(writer, http.MethodGet)
	case request.Method == http.MethodPatch:
		handler.updateServicePackage(writer, request, rawID)
	default:
		writeMethodNotAllowed(writer, http.MethodPatch)
	}
}

// updateServicePackage stores the strategy of one package in the routing
// settings. auto is the default, so choosing it removes the entry. Saving also
// drops the strategies of packages the services no longer form; packages are
// derived from stored plans, so a restart does not make a live one vanish.
func (handler *Handler) updateServicePackage(writer http.ResponseWriter, request *http.Request, rawID string) {
	id, err := url.PathUnescape(rawID)
	if err != nil || strings.Contains(rawID, "/") {
		writeError(writer, http.StatusNotFound, "not_found", "service package not found")
		return
	}
	if !requireMediaType(writer, request, "application/merge-patch+json") {
		return
	}
	var patch struct {
		Strategy *contract.PackageStrategy `json:"strategy"`
	}
	if !decodeControlJSON(writer, request, &patch) {
		return
	}
	if patch.Strategy == nil || !patch.Strategy.Valid() {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_package_strategy", "strategy must be auto, soonest_reset or most_remaining")
		return
	}
	handler.routingSettingsMu.Lock()
	defer handler.routingSettingsMu.Unlock()
	packages, err := handler.servicePackages.ServicePackages(request.Context())
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	index := slices.IndexFunc(packages, func(pkg contract.ServicePackage) bool { return pkg.ID == id })
	if index < 0 {
		writeError(writer, http.StatusNotFound, "not_found", "service package not found")
		return
	}
	settings, err := handler.routingSettings.GetRoutingSettings(request.Context())
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	formed := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		formed[pkg.ID] = true
	}
	strategies := maps.Clone(settings.PackageStrategies)
	maps.DeleteFunc(strategies, func(key string, _ contract.PackageStrategy) bool { return !formed[key] })
	if *patch.Strategy == contract.PackageStrategyAuto {
		delete(strategies, id)
	} else {
		if strategies == nil {
			strategies = map[string]contract.PackageStrategy{}
		}
		strategies[id] = *patch.Strategy
	}
	if len(strategies) == 0 {
		strategies = nil
	}
	settings.PackageStrategies = strategies
	if err := settings.Validate(); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_package_strategy", err.Error())
		return
	}
	if err := handler.routingSettings.UpdateRoutingSettings(request.Context(), settings); err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	pkg := packages[index]
	pkg.Strategy = *patch.Strategy
	writeJSON(writer, http.StatusOK, pkg)
}
