package controlapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/automodel"
	"github.com/QuantumNous/astrlink/core/internal/autotext"
)

const (
	AutoClassifierPath                = "/control/v1/auto-classifier"
	AutoClassifierLocalProbePath      = AutoClassifierPath + "/local/probe"
	AutoClassifierClassifyPreviewPath = AutoClassifierPath + "/classify-preview"
)

type AutoClassifierRegistry interface {
	ProbeLocal(context.Context, contract.AutoClassifierLocalProbeRequest) (contract.AutoClassifierProbeResponse, error)
	Install(context.Context, contract.AutoClassifierInstallRequest) (contract.AutoClassifierInstallation, error)
	ListInstallations() []contract.AutoClassifierInstallation
	GetInstallation(contract.AutoClassifierID) (contract.AutoClassifierInstallation, error)
	ReadyInstallation(contract.AutoClassifierID) (contract.ReadyAutoClassifierInstallation, bool)
	FirstReady() (contract.ReadyAutoClassifierInstallation, bool)
}

type AutoClassifier interface {
	Classify(context.Context, string) autoclassifier.Outcome
	ArtifactTier() contract.AutoClassifierArtifactTier
	EligibleForRouting() bool
}

func (handler *Handler) registerAutoClassifierRoutes() {
	handler.mux.HandleFunc(
		AutoClassifierLocalProbePath,
		handler.authenticated(handler.autoClassifierLocalProbe),
	)
	handler.mux.HandleFunc(
		AutoClassifierClassifyPreviewPath,
		handler.authenticated(handler.autoClassifierClassifyPreview),
	)
	handler.mux.HandleFunc(
		AutoClassifierPath,
		handler.authenticated(handler.autoClassifierCollection),
	)
}

func (handler *Handler) autoClassifierLocalProbe(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "local classifier probe does not accept query parameters")
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
	var input contract.AutoClassifierLocalProbeRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if err := contract.ValidateAutoClassifierLocalProbeRequest(input); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_auto_classifier_local_probe", "local classifier probe request is invalid")
		return
	}
	response, err := handler.autoClassifiers.ProbeLocal(request.Context(), input)
	if err != nil {
		handler.writeAutoClassifierRegistryError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) autoClassifierCollection(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "auto classifier collection does not accept query parameters")
		return
	}
	switch request.Method {
	case http.MethodGet:
		writeJSON(writer, http.StatusOK, contract.AutoClassifierInstallationList{
			Items: handler.autoClassifiers.ListInstallations(),
		})
	case http.MethodPost:
		handler.installAutoClassifier(writer, request)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and POST are allowed")
	}
}

func (handler *Handler) installAutoClassifier(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var input contract.AutoClassifierInstallRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if err := contract.ValidateAutoClassifierInstallRequest(input); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_auto_classifier_install", "classifier installation request is invalid")
		return
	}
	installation, err := handler.autoClassifiers.Install(request.Context(), input)
	if err != nil {
		handler.writeAutoClassifierRegistryError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, installation)
}

func (handler *Handler) autoClassifierClassifyPreview(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "classifier preview does not accept query parameters")
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
	var input contract.AutoClassifierClassifyPreviewRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if err := input.Validate(); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_auto_classifier_preview", "classifier preview request is invalid")
		return
	}
	text := input.Text
	if text == "" {
		text = autotext.ExtractLastUserText(input.Protocol, input.Body)
	}
	tier := handler.autoClassifier.ArtifactTier()
	if tier == "" {
		tier = contract.AutoClassifierArtifactExperimental
	}
	started := time.Now()
	response := contract.AutoClassifierClassifyPreviewResponse{
		ArtifactTier:       tier,
		EligibleForRouting: handler.autoClassifier.EligibleForRouting(),
	}
	if autotext.IsBlank(text) {
		response.FallbackReason = autoclassifier.FallbackEmptyText
		response.LatencyMS = time.Since(started).Milliseconds()
		writeJSON(writer, http.StatusOK, response)
		return
	}
	outcome := handler.autoClassifier.Classify(request.Context(), text)
	response.LatencyMS = time.Since(started).Milliseconds()
	response.Category = outcome.Category
	response.Logits = outcome.Logits
	response.FallbackReason = outcome.FallbackReason
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) writeAutoClassifierRegistryError(
	writer http.ResponseWriter,
	err error,
) {
	switch {
	case errors.Is(err, automodel.ErrNotFound):
		writeError(writer, http.StatusNotFound, "auto_classifier_not_found", "classifier installation was not found")
	case errors.Is(err, automodel.ErrBusy):
		writeError(writer, http.StatusConflict, "auto_classifier_busy", "the classifier installation is busy")
	case errors.Is(err, automodel.ErrAlreadyInstalled):
		writeError(writer, http.StatusConflict, "auto_classifier_already_installed", "the classifier installation is already ready")
	case errors.Is(err, automodel.ErrCapacity):
		writeError(writer, http.StatusConflict, "auto_classifier_limit", "the classifier installation limit has been reached")
	case errors.Is(err, automodel.ErrLocalProbeRequired):
		writeError(writer, http.StatusConflict, "auto_classifier_local_probe_required", "probe the local classifier path again before installing")
	case errors.Is(err, automodel.ErrLocalSource):
		writeError(writer, http.StatusUnprocessableEntity, "auto_classifier_local_source_unavailable", "the local classifier source is unavailable or unsafe")
	case errors.Is(err, automodel.ErrInvalidConfig),
		errors.Is(err, automodel.ErrUnsupportedModel):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_auto_classifier", "classifier metadata or taxonomy is unsupported")
	default:
		writeError(writer, http.StatusInternalServerError, "auto_classifier_unavailable", "the classifier operation failed")
	}
}
