// Package controlapi implements the bootstrap surface and authenticated local
// configuration operations. Bootstrap reads remain unauthenticated; state and
// credential operations are registered only when explicit dependencies and a
// per-start control token are supplied.
package controlapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const (
	HealthPath              = "/control/v1/health"
	VersionPath             = "/control/v1/version"
	CapabilitiesPath        = "/control/v1/capabilities"
	EndpointsPath           = "/control/v1/endpoints"
	AccessTokensPath        = "/control/v1/access-tokens"
	PoliciesPath            = "/control/v1/policies"
	PolicyDryRunPath        = PoliciesPath + "/" + string(contract.DefaultPrivacyPolicyID) + "/dry-run"
	PrivacyModelCatalogPath = "/control/v1/privacy-model-catalog"
	PrivacyModelsPath       = "/control/v1/privacy-models"
	PrivacyModelProbePath   = PrivacyModelsPath + "/probe"
)

type Dependencies struct {
	EndpointStore      storage.EndpointStore
	AccessTokenManager AccessTokenManager
	PolicyStore        storage.PolicyStore
	PrivacyModels      PrivacyModelRegistry
	PrivacyFilter      privacy.Filter
	PolicyChanged      func(contract.Policy)
	RequestRecords     storage.RequestRecordStore
	AuditSettings      storage.AuditSettingsStore
	AuditKeys          storage.AuditKeyStore
	AuditBlobs         storage.AuditBlobStore
	ControlToken       string
	NewEndpointID      func() (contract.EndpointID, error)
}

type AccessTokenManager interface {
	List(context.Context) ([]accesstoken.Token, error)
	Create(context.Context, string) (accesstoken.CreatedToken, error)
	Reveal(context.Context, contract.AccessTokenID) (string, error)
	Delete(context.Context, contract.AccessTokenID) error
}

type PrivacyModelRegistry interface {
	Catalog() contract.PrivacyModelCatalogResponse
	Probe(context.Context, contract.PrivacyModelProbeRequest) (contract.PrivacyModelProbeResponse, error)
	ListInstallations() []contract.PrivacyModelInstallation
	GetInstallation(contract.PrivacyModelID) (contract.PrivacyModelInstallation, error)
	Install(context.Context, contract.PrivacyModelInstallRequest) (contract.PrivacyModelInstallation, error)
	DeleteInstallation(context.Context, contract.PrivacyModelID) error
	ReadyInstallation(contract.PrivacyModelID) (contract.ReadyPrivacyModelInstallation, bool)
}

type Handler struct {
	version        contract.VersionResponse
	capabilities   contract.CapabilitiesResponse
	endpointStore  storage.EndpointStore
	accessTokens   AccessTokenManager
	policyStore    storage.PolicyStore
	privacyModels  PrivacyModelRegistry
	privacyFilter  privacy.Filter
	policyChanged  func(contract.Policy)
	requestRecords storage.RequestRecordStore
	auditSettings  storage.AuditSettingsStore
	auditKeys      storage.AuditKeyStore
	auditBlobs     storage.AuditBlobStore
	controlToken   []byte
	newEndpointID  func() (contract.EndpointID, error)
	mux            *http.ServeMux
	privacyMu      sync.Mutex
}

func New(version contract.VersionResponse) *Handler {
	handler, err := newHandler(version, Dependencies{})
	if err != nil {
		panic(err)
	}
	return handler
}

func NewWithDependencies(version contract.VersionResponse, dependencies Dependencies) (*Handler, error) {
	if dependencies.EndpointStore == nil {
		return nil, fmt.Errorf("endpoint store is required")
	}
	if len(dependencies.ControlToken) < 16 {
		return nil, fmt.Errorf("control token must contain at least 16 bytes")
	}
	return newHandler(version, dependencies)
}

func newHandler(version contract.VersionResponse, dependencies Dependencies) (*Handler, error) {
	handler := &Handler{
		version:        version,
		capabilities:   contract.DefaultCapabilitiesResponse(),
		endpointStore:  dependencies.EndpointStore,
		accessTokens:   dependencies.AccessTokenManager,
		policyStore:    dependencies.PolicyStore,
		privacyModels:  dependencies.PrivacyModels,
		privacyFilter:  dependencies.PrivacyFilter,
		policyChanged:  dependencies.PolicyChanged,
		requestRecords: dependencies.RequestRecords,
		auditSettings:  dependencies.AuditSettings,
		auditKeys:      dependencies.AuditKeys,
		auditBlobs:     dependencies.AuditBlobs,
		controlToken:   []byte(dependencies.ControlToken),
		newEndpointID:  dependencies.NewEndpointID,
		mux:            http.NewServeMux(),
	}
	handler.mux.HandleFunc(HealthPath, handler.getOnly(func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, contract.HealthResponse{Status: "ok"})
	}))
	handler.mux.HandleFunc(VersionPath, handler.getOnly(func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, handler.version)
	}))
	handler.mux.HandleFunc(CapabilitiesPath, handler.getOnly(func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, handler.capabilities)
	}))
	if handler.endpointStore != nil {
		if handler.newEndpointID == nil {
			handler.newEndpointID = randomEndpointID
		}
		handler.registerEndpointRoutes()
	}
	if handler.accessTokens != nil {
		handler.registerAccessTokenRoutes()
	}
	if handler.policyStore != nil {
		handler.registerPolicyRoutes()
	}
	if handler.policyStore != nil && handler.privacyModels != nil {
		handler.registerPrivacyModelsRoutes()
	}
	if handler.requestRecords != nil {
		handler.registerRequestRecordRoutes()
	}
	if handler.auditSettings != nil {
		handler.registerAuditSettingsRoutes()
	}
	handler.mux.HandleFunc("/", func(writer http.ResponseWriter, _ *http.Request) {
		writeError(writer, http.StatusNotFound, "not_found", "control endpoint not found")
	})
	return handler, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	setHeaders(writer.Header())
	handler.mux.ServeHTTP(writer, request)
}

func (handler *Handler) getOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
			return
		}
		next(writer, request)
	}
}

func setHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "application/json")
	header.Set("X-Content-Type-Options", "nosniff")
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

type errorEnvelope struct {
	Error     controlError `json:"error"`
	RequestID string       `json:"request_id"`
}

type controlError struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Retryable bool          `json:"retryable"`
	Details   []errorDetail `json:"details"`
}

type errorDetail struct {
	Field             string   `json:"field,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	Protocol          string   `json:"protocol,omitempty"`
	EndpointID        string   `json:"endpoint_id,omitempty"`
	RequiredPlanTypes []string `json:"required_plan_types,omitempty"`
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeErrorDetails(writer, status, code, message, nil)
}

func writeValidationFailed(writer http.ResponseWriter, message string, details []errorDetail) {
	writeErrorDetails(writer, http.StatusBadRequest, "validation_failed", message, details)
}

func writeErrorDetails(writer http.ResponseWriter, status int, code, message string, details []errorDetail) {
	if details == nil {
		details = []errorDetail{}
	}
	writeJSON(writer, status, errorEnvelope{
		Error: controlError{
			Code: code, Message: message, Retryable: false, Details: details,
		},
		RequestID: newRequestID(),
	})
}

func newRequestID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "req_unavailable"
	}
	return "req_" + hex.EncodeToString(value[:])
}
