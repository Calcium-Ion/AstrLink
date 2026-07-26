package controlapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacymodel"
)

func (handler *Handler) registerPrivacyModelsRoutes() {
	handler.mux.HandleFunc(
		PrivacyModelCatalogPath,
		handler.authenticated(handler.privacyModelCatalog),
	)
	handler.mux.HandleFunc(
		PrivacyModelProbePath,
		handler.authenticated(handler.privacyModelProbe),
	)
	handler.mux.HandleFunc(
		PrivacyModelsPath,
		handler.authenticated(handler.privacyModelCollection),
	)
	handler.mux.HandleFunc(
		PrivacyModelsPath+"/",
		handler.authenticated(handler.privacyModelItem),
	)
}

func (handler *Handler) privacyModelCatalog(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "privacy model catalog does not accept query parameters")
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	writeJSON(writer, http.StatusOK, handler.privacyModels.Catalog())
}

func (handler *Handler) privacyModelProbe(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "privacy model probe does not accept query parameters")
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var input contract.PrivacyModelProbeRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if err := contract.ValidatePrivacyModelRepoID(input.RepoID); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_privacy_model_probe", "privacy model probe request is invalid")
		return
	}
	if err := contract.ValidateRequestedPrivacyModelRevision(input.Revision); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_privacy_model_probe", "privacy model probe request is invalid")
		return
	}
	response, err := handler.privacyModels.Probe(request.Context(), input)
	if err != nil {
		handler.writePrivacyModelRegistryError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) privacyModelCollection(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "privacy model collection does not accept query parameters")
		return
	}
	switch request.Method {
	case http.MethodGet:
		writeJSON(writer, http.StatusOK, contract.PrivacyModelInstallationList{
			Items: handler.privacyModels.ListInstallations(),
		})
	case http.MethodPost:
		handler.installPrivacyModel(writer, request)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and POST are allowed")
	}
}

func (handler *Handler) installPrivacyModel(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var input contract.PrivacyModelInstallRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if err := contract.ValidatePrivacyModelInstallRequest(input); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_privacy_model_install", "privacy model installation request is invalid")
		return
	}
	installation, err := handler.privacyModels.Install(request.Context(), input)
	if err != nil {
		handler.writePrivacyModelRegistryError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, installation)
}

func (handler *Handler) privacyModelItem(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "privacy model item does not accept query parameters")
		return
	}
	rawID := strings.TrimPrefix(request.URL.Path, PrivacyModelsPath+"/")
	if rawID == "" || strings.Contains(rawID, "/") {
		writeError(writer, http.StatusNotFound, "not_found", "control endpoint not found")
		return
	}
	decodedID, err := url.PathUnescape(rawID)
	if err != nil || decodedID != rawID {
		writeError(writer, http.StatusBadRequest, "invalid_privacy_model_id", "privacy model id must use its canonical form")
		return
	}
	id := contract.PrivacyModelID(decodedID)
	if err := id.Validate(); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_privacy_model_id", "privacy model id is invalid")
		return
	}
	switch request.Method {
	case http.MethodGet:
		installation, err := handler.privacyModels.GetInstallation(id)
		if err != nil {
			handler.writePrivacyModelRegistryError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, installation)
	case http.MethodDelete:
		handler.deletePrivacyModelInstallation(writer, request, id)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodDelete)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and DELETE are allowed")
	}
}

func (handler *Handler) deletePrivacyModelInstallation(
	writer http.ResponseWriter,
	request *http.Request,
	id contract.PrivacyModelID,
) {
	if request.ContentLength != 0 {
		writeError(writer, http.StatusBadRequest, "invalid_request", "privacy model deletion does not accept a request body")
		return
	}
	handler.privacyMu.Lock()
	defer handler.privacyMu.Unlock()
	record, err := handler.policyStore.GetPolicy(
		request.Context(),
		contract.DefaultPrivacyPolicyID,
	)
	if err != nil {
		handler.writePolicyStoreError(writer, err)
		return
	}
	if record.Policy.LocalModelID != nil && *record.Policy.LocalModelID == id {
		writeError(writer, http.StatusConflict, "privacy_model_selected", "select another detector or local model before deleting this installation")
		return
	}
	if err := handler.privacyModels.DeleteInstallation(request.Context(), id); err != nil {
		handler.writePrivacyModelRegistryError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) writePrivacyModelRegistryError(
	writer http.ResponseWriter,
	err error,
) {
	switch {
	case errors.Is(err, privacymodel.ErrNotFound):
		writeError(writer, http.StatusNotFound, "privacy_model_not_found", "privacy model installation was not found")
	case errors.Is(err, privacymodel.ErrBusy):
		writeError(writer, http.StatusConflict, "privacy_model_busy", "the privacy model installation is busy")
	case errors.Is(err, privacymodel.ErrAlreadyInstalled):
		writeError(writer, http.StatusConflict, "privacy_model_already_installed", "the privacy model installation is already ready")
	case errors.Is(err, privacymodel.ErrCapacity):
		writeError(writer, http.StatusConflict, "privacy_model_limit", "the privacy model installation limit has been reached")
	case errors.Is(err, privacymodel.ErrInvalidConfig),
		errors.Is(err, privacymodel.ErrUnsupportedModel):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_privacy_model", "privacy model metadata or label mapping is unsupported")
	case errors.Is(err, privacymodel.ErrRemoteMetadata):
		writeError(writer, http.StatusBadGateway, "privacy_model_metadata_unavailable", "public model metadata could not be verified")
	default:
		writeError(writer, http.StatusInternalServerError, "privacy_model_unavailable", "the privacy model operation failed")
	}
}
