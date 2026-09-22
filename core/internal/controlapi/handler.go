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
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

const (
	HealthPath                   = "/control/v1/health"
	VersionPath                  = "/control/v1/version"
	CapabilitiesPath             = "/control/v1/capabilities"
	ShutdownPath                 = "/control/v1/shutdown"
	ServicesPath                 = "/control/v1/services"
	ServiceModelProbesPath       = "/control/v1/service-model-probes"
	RoutesPath                   = "/control/v1/routes"
	AccessTokensPath             = "/control/v1/access-tokens"
	PoliciesPath                 = "/control/v1/policies"
	PolicyDryRunPath             = PoliciesPath + "/" + string(contract.DefaultPrivacyPolicyID) + "/dry-run"
	PrivacyRegexBuiltinRulesPath = "/control/v1/privacy/regex-builtin-rules"
	PrivacyModelCatalogPath      = "/control/v1/privacy-model-catalog"
	PrivacyModelsPath            = "/control/v1/privacy-models"
	PrivacyModelProbePath        = PrivacyModelsPath + "/probe"
	PrivacyModelLocalProbePath   = PrivacyModelsPath + "/local/probe"
)

type Dependencies struct {
	PricingStore       PricingStore
	PricingManager     *pricing.Manager
	RecoveryResolver   *endpoint.StoreResolver
	ServiceStore       storage.ServiceStore
	RouteStore         storage.RouteStore
	AccessTokenManager AccessTokenManager
	PolicyStore        storage.PolicyStore
	PrivacyModels      PrivacyModelRegistry
	PrivacyFilter      privacy.Filter
	PolicyChanged      func(contract.Policy)
	RequestRecords     storage.RequestRecordStore
	AuditSettings      storage.AuditSettingsStore
	AuditKeys          storage.AuditKeyStore
	AuditBlobs         storage.AuditBlobStore
	Subscriptions      *subscription.Manager
	// CodingPlans reads first-party plan quotas for API-key coding plan
	// services (Kimi, GLM, MiniMax, OpenCode Go). Optional.
	CodingPlans      CodingPlanUsage
	ServiceModels    ServiceModelProber
	AutoClassifiers  AutoClassifierRegistry
	AutoClassifier   AutoClassifier
	ControlToken     string
	NewServiceID     func() (contract.ServiceID, error)
	NewRouteID       func() (contract.RouteID, error)
	ConversionEngine relaykitbridge.ConversionEngine
	Shutdown         context.CancelFunc
}

// CodingPlanUsage is satisfied by *codingplan.Fetcher.
type CodingPlanUsage interface {
	Usage(context.Context, contract.Service) (contract.SubscriptionUsage, error)
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
	ProbeLocal(context.Context, contract.PrivacyModelLocalProbeRequest) (contract.PrivacyModelProbeResponse, error)
	ListInstallations() []contract.PrivacyModelInstallation
	GetInstallation(contract.PrivacyModelID) (contract.PrivacyModelInstallation, error)
	Install(context.Context, contract.PrivacyModelInstallRequest) (contract.PrivacyModelInstallation, error)
	PauseInstallation(context.Context, contract.PrivacyModelID) (contract.PrivacyModelInstallation, error)
	ResumeInstallation(context.Context, contract.PrivacyModelID) (contract.PrivacyModelInstallation, error)
	DeleteInstallation(context.Context, contract.PrivacyModelID) error
	ReadyInstallation(contract.PrivacyModelID) (contract.ReadyPrivacyModelInstallation, bool)
}

type Handler struct {
	pricingStore      PricingStore
	pricingManager    *pricing.Manager
	recoveryPaths     storage.RecoveryPathStore
	recoveryResolver  *endpoint.StoreResolver
	routingSettings   storage.RoutingSettingsStore
	routingSettingsMu sync.Mutex
	version           contract.VersionResponse
	capabilities      contract.CapabilitiesResponse
	serviceStore      storage.ServiceStore
	routeStore        storage.RouteStore
	accessTokens      AccessTokenManager
	policyStore       storage.PolicyStore
	privacyModels     PrivacyModelRegistry
	privacyFilter     privacy.Filter
	policyChanged     func(contract.Policy)
	requestRecords    storage.RequestRecordStore
	auditSettings     storage.AuditSettingsStore
	auditKeys         storage.AuditKeyStore
	auditBlobs        storage.AuditBlobStore
	subscriptions     *subscription.Manager
	codingPlans       CodingPlanUsage
	serviceModels     ServiceModelProber
	autoClassifiers   AutoClassifierRegistry
	autoClassifier    AutoClassifier
	controlToken      []byte
	newServiceID      func() (contract.ServiceID, error)
	newRouteID        func() (contract.RouteID, error)
	mux               *http.ServeMux
	privacyMu         sync.Mutex
	shutdown          context.CancelFunc
}

func New(version contract.VersionResponse) *Handler {
	handler, err := newHandler(version, Dependencies{})
	if err != nil {
		panic(err)
	}
	return handler
}

func NewWithDependencies(version contract.VersionResponse, dependencies Dependencies) (*Handler, error) {
	if dependencies.ServiceStore == nil {
		return nil, fmt.Errorf("service store is required")
	}
	if len(dependencies.ControlToken) < 16 {
		return nil, fmt.Errorf("control token must contain at least 16 bytes")
	}
	return newHandler(version, dependencies)
}

func newHandler(version contract.VersionResponse, dependencies Dependencies) (*Handler, error) {
	capabilities := contract.DefaultCapabilitiesResponse()
	if dependencies.ConversionEngine != nil {
		capabilities.ConversionEngine = relaykitbridge.Descriptor(dependencies.ConversionEngine)
	}
	recoveryPaths, _ := dependencies.ServiceStore.(storage.RecoveryPathStore)
	routingSettings, _ := dependencies.ServiceStore.(storage.RoutingSettingsStore)
	handler := &Handler{
		pricingStore: dependencies.PricingStore, pricingManager: dependencies.PricingManager,
		routingSettings: routingSettings,
		recoveryPaths:   recoveryPaths, recoveryResolver: dependencies.RecoveryResolver,
		version:         version,
		capabilities:    capabilities,
		serviceStore:    dependencies.ServiceStore,
		routeStore:      dependencies.RouteStore,
		accessTokens:    dependencies.AccessTokenManager,
		policyStore:     dependencies.PolicyStore,
		privacyModels:   dependencies.PrivacyModels,
		privacyFilter:   dependencies.PrivacyFilter,
		policyChanged:   dependencies.PolicyChanged,
		requestRecords:  dependencies.RequestRecords,
		auditSettings:   dependencies.AuditSettings,
		auditKeys:       dependencies.AuditKeys,
		auditBlobs:      dependencies.AuditBlobs,
		subscriptions:   dependencies.Subscriptions,
		codingPlans:     dependencies.CodingPlans,
		serviceModels:   dependencies.ServiceModels,
		autoClassifiers: dependencies.AutoClassifiers,
		autoClassifier:  dependencies.AutoClassifier,
		controlToken:    []byte(dependencies.ControlToken),
		newServiceID:    dependencies.NewServiceID,
		newRouteID:      dependencies.NewRouteID,
		shutdown:        dependencies.Shutdown,
		mux:             http.NewServeMux(),
	}
	handler.mux.HandleFunc(PricingPath+"/", handler.authenticated(handler.pricingResource))
	handler.mux.HandleFunc(RoutingSettingsPath, handler.authenticated(handler.routingSettingsResource))
	handler.mux.HandleFunc(HealthPath, handler.getOnly(func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, contract.HealthResponse{Status: "ok"})
	}))
	handler.mux.HandleFunc(VersionPath, handler.getOnly(func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, handler.version)
	}))
	handler.mux.HandleFunc(CapabilitiesPath, handler.getOnly(func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, handler.capabilities)
	}))
	if handler.shutdown != nil {
		handler.mux.HandleFunc(ShutdownPath, handler.authenticated(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodPost {
				writer.Header().Set("Allow", http.MethodPost)
				writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
				return
			}
			writeJSON(writer, http.StatusAccepted, map[string]string{"status": "shutting_down"})
			go handler.shutdown()
		}))
	}
	if handler.serviceStore != nil {
		if handler.newServiceID == nil {
			handler.newServiceID = randomServiceID
		}
		handler.registerServiceRoutes()
	}
	handler.mux.HandleFunc(ServiceOrderPath, handler.authenticated(handler.serviceOrderResource))
	for _, path := range []string{RoutesPath, RoutesPath + "/", RecoveryPathsPath, RecoveryPathsPath + "/", AutoClassifierPath, AutoClassifierPath + "/"} {
		handler.mux.HandleFunc(path, handler.authenticated(handler.retiredRouting))
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
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
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
	ServiceID         string   `json:"service_id,omitempty"`
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
