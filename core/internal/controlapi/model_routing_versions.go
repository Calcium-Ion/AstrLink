package controlapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// ModelRoutingVersionsPath serves saved routing-group configurations at
// {path}/{group_id}/{version}. Request records name the version they were
// routed under; this is where a reader turns that number into the rules.
const ModelRoutingVersionsPath = "/control/v1/model-routing-versions"

func (handler *Handler) modelRoutingGroupVersion(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	store, ok := handler.requestRecords.(storage.ModelRoutingVersionStore)
	if !ok {
		writeError(writer, http.StatusServiceUnavailable, "model_routing_versions_unavailable", "routing group versions are unavailable")
		return
	}
	groupID, rawVersion, found := strings.Cut(strings.TrimPrefix(request.URL.Path, ModelRoutingVersionsPath+"/"), "/")
	version, err := strconv.Atoi(rawVersion)
	if !found || groupID == "" || len(groupID) > 64 || err != nil || version < 1 {
		writeError(writer, http.StatusBadRequest, "invalid_model_routing_version", "path must name a group id and a positive version")
		return
	}
	result, err := store.GetModelRoutingGroupVersion(request.Context(), groupID, version)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}
