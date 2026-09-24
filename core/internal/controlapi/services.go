package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/codingplan"
	"github.com/QuantumNous/astrlink/core/internal/servicemodel"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

type ServiceModelProber interface {
	ProbeService(context.Context, contract.Service, contract.ProtocolID) ([]string, error)
	ProbeHTTP(context.Context, contract.ServiceID, contract.ServiceKind, contract.HTTPConnection, []byte, contract.ProtocolID) ([]string, error)
}

type serviceCreateRequest struct {
	Proxy                     json.RawMessage         `json:"proxy,omitempty"`
	ResponsesWebSocketEnabled json.RawMessage         `json:"responses_websocket_enabled,omitempty"`
	FailurePolicy             *contract.FailurePolicy `json:"failure_policy,omitempty"`
	Name                      string                  `json:"name"`
	Kind                      *contract.ServiceKind   `json:"kind"`
	Enabled                   json.RawMessage         `json:"enabled,omitempty"`
	Models                    json.RawMessage         `json:"models,omitempty"`
	HTTP                      json.RawMessage         `json:"http,omitempty"`
	Capabilities              json.RawMessage         `json:"capabilities,omitempty"`
}

type serviceHTTPInput struct {
	BaseURL    string          `json:"base_url"`
	Auth       json.RawMessage `json:"auth"`
	Credential json.RawMessage `json:"credential,omitempty"`
}

type servicePageResponse struct {
	Items      []contract.Service `json:"items"`
	NextCursor *string            `json:"next_cursor"`
}

func (handler *Handler) publicService(service contract.Service) contract.Service {
	if handler.subscriptions == nil || !service.Kind.IsSubscription() || service.Subscription == nil {
		return service
	}
	connection := *service.Subscription
	connection.AuthorizationBoundary = handler.subscriptions.AuthorizationBoundary()
	service.Subscription = &connection
	return service
}

type authorizationStartRequest struct {
	Flow *contract.AuthorizationFlow `json:"flow"`
}

func (handler *Handler) registerServiceRoutes() {
	handler.mux.HandleFunc(ServicesPath, handler.authenticated(handler.serviceCollection))
	handler.mux.HandleFunc(ServicesPath+"/", handler.authenticated(handler.serviceItem))
	handler.mux.HandleFunc(ServiceModelProbesPath, handler.authenticated(handler.probeDraftServiceModels))
	handler.mux.HandleFunc(ServiceProxyProbesPath, handler.authenticated(handler.probeServiceProxy))
}

func (handler *Handler) serviceCollection(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		handler.listServices(writer, request)
	case http.MethodPost:
		handler.createService(writer, request)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and POST are allowed")
	}
}

func (handler *Handler) serviceItem(writer http.ResponseWriter, request *http.Request) {
	rest := strings.TrimPrefix(request.URL.Path, ServicesPath+"/")
	parts := strings.Split(rest, "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	id, ok := parseServiceID(writer, parts[0])
	if !ok {
		return
	}
	if len(parts) == 3 {
		if parts[1] == "usage" && parts[2] == "reset" {
			if request.Method != http.MethodPost {
				writeMethodNotAllowed(writer, http.MethodPost)
				return
			}
			handler.resetServiceUsage(writer, request, id)
			return
		}
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	if len(parts) > 2 {
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "authorization":
			handler.serviceAuthorization(writer, request, id)
		case "logout":
			if request.Method != http.MethodPost {
				writeMethodNotAllowed(writer, http.MethodPost)
				return
			}
			handler.logoutService(writer, request, id)
		case "probe-models":
			if request.Method != http.MethodPost {
				writeMethodNotAllowed(writer, http.MethodPost)
				return
			}
			handler.probeServiceModels(writer, request, id)
		case "test":
			if request.Method != http.MethodPost {
				writeMethodNotAllowed(writer, http.MethodPost)
				return
			}
			handler.testService(writer, request, id)
		case "probe-responses":
			if request.Method != http.MethodPost {
				writeMethodNotAllowed(writer, http.MethodPost)
				return
			}
			handler.probeServiceResponses(writer, request, id)
		case "usage":
			if request.Method != http.MethodGet {
				writeMethodNotAllowed(writer, http.MethodGet)
				return
			}
			handler.getServiceUsage(writer, request, id)
		default:
			writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		}
		return
	}
	switch request.Method {
	case http.MethodGet:
		handler.getService(writer, request, id)
	case http.MethodPatch:
		handler.patchService(writer, request, id)
	case http.MethodDelete:
		handler.deleteService(writer, request, id)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPatch+", "+http.MethodDelete)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET, PATCH, and DELETE are allowed")
	}
}

func (handler *Handler) listServices(writer http.ResponseWriter, request *http.Request) {
	options, err := parseServiceListOptions(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", "service list query is invalid")
		return
	}
	page, err := handler.serviceStore.ListServices(request.Context(), options)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	response := servicePageResponse{Items: make([]contract.Service, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, handler.publicService(item.Service))
	}
	if page.NextCursor != "" {
		response.NextCursor = &page.NextCursor
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) createService(writer http.ResponseWriter, request *http.Request) {
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var input serviceCreateRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if input.Kind == nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_service", "kind is required")
		return
	}
	id, err := handler.newServiceID()
	if err != nil || id.Validate() != nil {
		writeError(writer, http.StatusInternalServerError, "service_id_unavailable", "could not allocate a service id")
		return
	}
	enabled := true
	if input.Enabled != nil {
		if isJSONNull(input.Enabled) || strictUnmarshal(input.Enabled, &enabled) != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_service", "enabled must be a boolean")
			return
		}
	}
	now := time.Now().UTC()
	service := contract.Service{
		ID: id, Name: input.Name, Kind: *input.Kind, Enabled: enabled, FailurePolicy: input.FailurePolicy,
		CreatedAt: now, UpdatedAt: now,
	}
	if input.ResponsesWebSocketEnabled != nil {
		var enabled bool
		if isJSONNull(input.ResponsesWebSocketEnabled) || strictUnmarshal(input.ResponsesWebSocketEnabled, &enabled) != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_service", "responses_websocket_enabled must be a boolean")
			return
		}
		service.ResponsesWebSocketEnabled = &enabled
	}
	models, err := decodeServiceModels(input.Models)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_service", "models violate the service contract")
		return
	}
	service.Models = models
	credential := storage.CredentialMutation{}
	switch {
	case input.Kind.IsSubscription():
		if handler.subscriptions == nil {
			writeError(writer, http.StatusServiceUnavailable, "subscription_unavailable", "subscription services are unavailable")
			return
		}
		if input.HTTP != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_service", "subscription services do not accept http")
			return
		}
		service.Capabilities = input.Kind.SubscriptionProvider().Capabilities()
		if input.Capabilities != nil {
			capabilities, decodeErr := decodeServiceCapabilities(input.Capabilities)
			if decodeErr != nil {
				writeError(writer, http.StatusUnprocessableEntity, "invalid_service", "capabilities violate the service contract")
				return
			}
			if err := handler.validateServiceCapabilities(*input.Kind, capabilities); err != nil {
				writeError(writer, http.StatusUnprocessableEntity, "invalid_service", err.Error())
				return
			}
			service.Capabilities = capabilities
		}
		service.Subscription = &contract.SubscriptionConnection{
			Provider:              input.Kind.SubscriptionProvider(),
			Status:                contract.SubscriptionStatusDisconnected,
			AuthorizationBoundary: handler.subscriptions.AuthorizationBoundary(),
		}
	case input.Kind.IsHTTP():
		connection, capabilities, mutation, decodeErr := decodeServiceHTTP(input.HTTP, input.Capabilities)
		if decodeErr != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_service", "http service configuration violates the contract")
			return
		}
		defer clear(mutation.Secret)
		if err := handler.validateServiceCapabilities(*input.Kind, capabilities); err != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_service", err.Error())
			return
		}
		service.HTTP = &connection
		service.Capabilities = capabilities
		credential = mutation
	default:
		writeError(writer, http.StatusUnprocessableEntity, "invalid_service", "service kind is unsupported")
		return
	}
	if err := applyServiceProxyInput(&service, &credential, input.Proxy); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_service_proxy", err.Error())
		return
	}
	record, err := handler.serviceStore.CreateService(request.Context(), service, credential)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	writer.Header().Set("Location", ServicesPath+"/"+string(record.Service.ID))
	writer.Header().Set("ETag", record.ETag)
	writeJSON(writer, http.StatusCreated, handler.publicService(record.Service))
}

func (handler *Handler) getService(writer http.ResponseWriter, request *http.Request, id contract.ServiceID) {
	record, err := handler.serviceStore.GetService(request.Context(), id)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	writer.Header().Set("ETag", record.ETag)
	writeJSON(writer, http.StatusOK, handler.publicService(record.Service))
}

func (handler *Handler) patchService(writer http.ResponseWriter, request *http.Request, id contract.ServiceID) {
	if !requireMediaType(writer, request, "application/merge-patch+json") {
		return
	}
	expectedETag := request.Header.Get("If-Match")
	if expectedETag == "" {
		writeError(writer, http.StatusBadRequest, "if_match_required", "If-Match is required")
		return
	}
	current, err := handler.serviceStore.GetService(request.Context(), id)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	if current.ETag != expectedETag {
		handler.writeStoreError(writer, storage.ErrPrecondition)
		return
	}
	var patch map[string]json.RawMessage
	if !decodeControlJSON(writer, request, &patch) {
		return
	}
	service, credential, err := applyServicePatch(current.Service, patch)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_service_patch", "service patch violates the contract")
		return
	}
	if err := handler.validateServiceCapabilities(service.Kind, service.Capabilities); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_service_patch", err.Error())
		return
	}
	defer clear(credential.Secret)
	record, err := handler.serviceStore.UpdateService(request.Context(), service, credential, expectedETag)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	writer.Header().Set("ETag", record.ETag)
	writeJSON(writer, http.StatusOK, handler.publicService(record.Service))
}

func (handler *Handler) deleteService(writer http.ResponseWriter, request *http.Request, id contract.ServiceID) {
	expectedETag := request.Header.Get("If-Match")
	if expectedETag == "" {
		writeError(writer, http.StatusBadRequest, "if_match_required", "If-Match is required")
		return
	}
	current, err := handler.serviceStore.GetService(request.Context(), id)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	if current.ETag != expectedETag {
		handler.writeStoreError(writer, storage.ErrPrecondition)
		return
	}
	if current.Service.Kind.IsSubscription() {
		if handler.subscriptions == nil {
			writeError(writer, http.StatusServiceUnavailable, "subscription_unavailable", "subscription services are unavailable")
			return
		}
		if err := handler.subscriptions.CleanupCredentialsForDelete(request.Context(), id); err != nil {
			writeError(writer, http.StatusServiceUnavailable, accountauth.ErrCodeStoreUnavailable, "secure account credential store is unavailable")
			return
		}
	}
	if err := handler.serviceStore.DeleteService(request.Context(), id, expectedETag); err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) serviceAuthorization(writer http.ResponseWriter, request *http.Request, id contract.ServiceID) {
	if handler.subscriptions == nil {
		writeError(writer, http.StatusServiceUnavailable, "subscription_unavailable", "subscription services are unavailable")
		return
	}
	current, err := handler.serviceStore.GetService(request.Context(), id)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	if !current.Service.Kind.IsSubscription() {
		writeError(writer, http.StatusConflict, "service_not_subscription", "service does not support subscription authorization")
		return
	}
	switch request.Method {
	case http.MethodPost:
		flow, ok := decodeAuthorizationFlow(writer, request)
		if !ok {
			return
		}
		if !flow.SupportedBy(current.Service.Kind.SubscriptionProvider()) {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_authorization_flow", "authorization flow is unsupported by this provider")
			return
		}
		session, err := handler.subscriptions.BeginAuthorization(request.Context(), id, flow)
		if err != nil {
			switch {
			case errors.Is(err, subscription.ErrCredentialUnavailable), errors.Is(err, accountauth.ErrCredentialStoreUnavailable):
				writeError(writer, http.StatusServiceUnavailable, accountauth.ErrCodeStoreUnavailable, "secure account credential store is unavailable")
			case errors.Is(err, accountauth.ErrCallbackPortsUnavailable):
				writeError(
					writer,
					http.StatusServiceUnavailable,
					accountauth.ErrCodeCallbackPortsBusy,
					"OAuth callback ports 1455 and 1457 are unavailable, and Device Code login could not start",
				)
			case errors.Is(err, accountauth.ErrDeviceCodeUnavailable):
				writeError(
					writer,
					http.StatusUnprocessableEntity,
					accountauth.ErrCodeDeviceCodeUnavailable,
					"Device Code login is not enabled for this OpenAI account or server",
				)
			case errors.Is(err, accountauth.ErrDeviceCodeRequestFailed):
				writeError(
					writer,
					http.StatusBadGateway,
					accountauth.ErrCodeDeviceCodeRequest,
					"OpenAI Device Code login could not be started",
				)
			default:
				writeError(writer, http.StatusConflict, "authorization_busy", sanitizeSubscriptionError(err))
			}
			return
		}
		writeJSON(writer, http.StatusAccepted, session)
	case http.MethodPut:
		if !requireMediaType(writer, request, "application/json") {
			return
		}
		var input struct {
			SessionID contract.AuthorizationSessionID `json:"session_id"`
			Code      string                          `json:"code"`
		}
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		if input.SessionID.Validate() != nil || input.Code == "" || len(input.Code) > 8192 {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_authorization_code", "session_id and authorization code are required")
			return
		}
		session, err := handler.subscriptions.CompleteAuthorizationCode(request.Context(), id, input.SessionID, input.Code)
		if err != nil {
			writeError(writer, http.StatusConflict, "authorization_code_failed", "authorization code could not be accepted; check the code and session, or sign in again")
			return
		}
		writeJSON(writer, http.StatusOK, session)
	case http.MethodGet:
		session, ok := handler.subscriptions.GetAuthorization(request.Context(), id)
		if !ok {
			writeError(writer, http.StatusNotFound, "not_found", "no authorization session exists for this service")
			return
		}
		writeJSON(writer, http.StatusOK, session)
	case http.MethodDelete:
		session, err := handler.subscriptions.CancelAuthorization(request.Context(), id)
		if err != nil {
			if errors.Is(err, accountauth.ErrSessionNotFound) {
				writeError(writer, http.StatusNotFound, "not_found", "no authorization session exists for this service")
			} else {
				writeError(writer, http.StatusConflict, "authorization_not_pending", "authorization session is not pending")
			}
			return
		}
		writeJSON(writer, http.StatusOK, session)
	default:
		writer.Header().Set("Allow", http.MethodPost+", "+http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST, GET, PUT, and DELETE are allowed")
	}
}

func decodeAuthorizationFlow(
	writer http.ResponseWriter,
	request *http.Request,
) (contract.AuthorizationFlow, bool) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxControlBodyBytes)
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return "", false
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return contract.AuthorizationFlowBrowser, true
	}
	if !requireMediaType(writer, request, "application/json") {
		return "", false
	}
	if bytes.Equal(raw, []byte("null")) {
		writeError(
			writer,
			http.StatusUnprocessableEntity,
			"invalid_authorization_flow",
			"flow must be browser or device_code",
		)
		return "", false
	}
	var input authorizationStartRequest
	if err := strictUnmarshal(raw, &input); err != nil {
		writeError(
			writer,
			http.StatusUnprocessableEntity,
			"invalid_authorization_flow",
			"flow must be browser or device_code",
		)
		return "", false
	}
	if input.Flow == nil {
		writeError(
			writer,
			http.StatusUnprocessableEntity,
			"invalid_authorization_flow",
			"flow must be browser or device_code",
		)
		return "", false
	}
	flow := *input.Flow
	if !flow.Valid() {
		writeError(
			writer,
			http.StatusUnprocessableEntity,
			"invalid_authorization_flow",
			"flow must be browser or device_code",
		)
		return "", false
	}
	return flow, true
}

func (handler *Handler) logoutService(writer http.ResponseWriter, request *http.Request, id contract.ServiceID) {
	account, err := handler.subscriptions.Logout(request.Context(), id)
	if err != nil {
		if errors.Is(err, subscription.ErrNotFound) {
			writeError(writer, http.StatusNotFound, "not_found", "service not found")
		} else if errors.Is(err, subscription.ErrCredentialUnavailable) {
			writeError(writer, http.StatusServiceUnavailable, accountauth.ErrCodeStoreUnavailable, "secure account credential store is unavailable")
		} else {
			writeError(writer, http.StatusInternalServerError, "internal_error", "failed to logout service")
		}
		return
	}
	record, err := handler.serviceStore.GetService(request.Context(), id)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	_ = account
	writer.Header().Set("ETag", record.ETag)
	writeJSON(writer, http.StatusOK, handler.publicService(record.Service))
}

type serviceModelProbeResponse struct {
	ServiceID contract.ServiceID  `json:"service_id,omitempty"`
	Protocol  contract.ProtocolID `json:"protocol"`
	ModelIDs  []string            `json:"model_ids"`
}

type serviceModelProbeRequest struct {
	Protocol *contract.ProtocolID `json:"protocol"`
}

type draftServiceModelProbeRequest struct {
	Proxy     json.RawMessage       `json:"proxy,omitempty"`
	ServiceID *contract.ServiceID   `json:"service_id,omitempty"`
	Kind      *contract.ServiceKind `json:"kind"`
	HTTP      json.RawMessage       `json:"http"`
	Protocol  *contract.ProtocolID  `json:"protocol"`
}

type subscriptionResponsesProbeRequest struct {
	Model string `json:"model,omitempty"`
}

func (handler *Handler) probeServiceModels(writer http.ResponseWriter, request *http.Request, id contract.ServiceID) {
	if handler.serviceModels == nil {
		writeError(writer, http.StatusServiceUnavailable, "model_probe_unavailable", "model discovery is unavailable")
		return
	}
	protocol := contract.ProtocolOpenAIModels
	if request.Body != nil && request.ContentLength != 0 {
		if !requireMediaType(writer, request, "application/json") {
			return
		}
		var input serviceModelProbeRequest
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		if input.Protocol == nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_model_probe", "protocol is required")
			return
		}
		protocol = *input.Protocol
	}
	record, err := handler.serviceStore.GetService(request.Context(), id)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	ids, err := handler.serviceModels.ProbeService(request.Context(), record.Service, protocol)
	if err != nil {
		writeServiceModelProbeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, serviceModelProbeResponse{ServiceID: id, Protocol: protocol, ModelIDs: ids})
}

func (handler *Handler) probeDraftServiceModels(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeMethodNotAllowed(writer, http.MethodPost)
		return
	}
	if handler.serviceModels == nil {
		writeError(writer, http.StatusServiceUnavailable, "model_probe_unavailable", "model discovery is unavailable")
		return
	}
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var input draftServiceModelProbeRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if input.Kind == nil || input.Protocol == nil || input.HTTP == nil || !input.Kind.IsHTTP() {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_model_probe", "kind, http, and protocol are required")
		return
	}
	var httpInput serviceHTTPInput
	if strictUnmarshal(input.HTTP, &httpInput) != nil || httpInput.Auth == nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_model_probe", "http service configuration is invalid")
		return
	}
	auth, err := decodeServiceAuth(httpInput.Auth)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_model_probe", "http authentication is invalid")
		return
	}
	serviceID := contract.ServiceID("service_model_probe")
	proxyService := contract.Service{ID: serviceID}
	connection := contract.HTTPConnection{BaseURL: httpInput.BaseURL, Auth: auth}
	var secret []byte
	if httpInput.Credential != nil {
		if isJSONNull(httpInput.Credential) {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_model_probe", "credential must be an object")
			return
		}
		var credential credentialInput
		if strictUnmarshal(httpInput.Credential, &credential) != nil || credential.Secret == "" {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_model_probe", "credential.secret must not be empty")
			return
		}
		secret = []byte(credential.Secret)
		defer clear(secret)
	}
	if input.ServiceID != nil {
		if err := input.ServiceID.Validate(); err != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_model_probe", "service_id is invalid")
			return
		}
		current, err := handler.serviceStore.GetService(request.Context(), *input.ServiceID)
		if err != nil {
			handler.writeStoreError(writer, err)
			return
		}
		if !current.Service.Kind.IsHTTP() || current.Service.HTTP == nil {
			writeError(writer, http.StatusConflict, "service_not_http", "service does not have an HTTP credential")
			return
		}
		serviceID = *input.ServiceID
		proxyService = current.Service
		if len(secret) == 0 {
			connection.CredentialRef = current.Service.HTTP.CredentialRef
		}
	}
	probeContext, err := handler.bindDraftProxy(request.Context(), proxyService, input.Proxy)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_service_proxy", err.Error())
		return
	}
	ids, err := handler.serviceModels.ProbeHTTP(
		probeContext, serviceID, *input.Kind, connection, secret, *input.Protocol,
	)
	if err != nil {
		writeServiceModelProbeError(writer, err)
		return
	}
	response := serviceModelProbeResponse{Protocol: *input.Protocol, ModelIDs: ids}
	if input.ServiceID != nil {
		response.ServiceID = *input.ServiceID
	}
	writeJSON(writer, http.StatusOK, response)
}

var modelProbeStatusPattern = regexp.MustCompile(`(?:status|HTTP) (\d{3})`)

func writeServiceModelProbeError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		writeError(writer, http.StatusGatewayTimeout, "upstream_model_discovery_timeout", "upstream model discovery timed out")
	case errors.Is(err, servicemodel.ErrUnsupported):
		writeError(writer, http.StatusUnprocessableEntity, "model_discovery_unsupported", "service does not support this model discovery protocol")
	case errors.Is(err, servicemodel.ErrCredentialUnavailable):
		writeError(writer, http.StatusConflict, "credential_unavailable", "service credential is unavailable")
	case errors.Is(err, servicemodel.ErrNotConnected):
		writeError(writer, http.StatusConflict, "service_not_connected", "subscription service is not connected")
	default:
		writeError(writer, http.StatusBadGateway, "upstream_model_discovery_failed", sanitizedModelProbeMessage(err))
	}
}

func sanitizedModelProbeMessage(err error) string {
	if err == nil {
		return "upstream model discovery failed"
	}
	text := err.Error()
	if match := modelProbeStatusPattern.FindStringSubmatch(text); len(match) == 2 {
		return "upstream model discovery failed (HTTP " + match[1] + ")"
	}
	if strings.Contains(text, "decode codex models") || strings.Contains(text, "invalid Codex") {
		return "upstream model discovery failed (invalid catalog)"
	}
	return "upstream model discovery failed"
}

func (handler *Handler) probeServiceResponses(writer http.ResponseWriter, request *http.Request, id contract.ServiceID) {
	probeSubscriptionResponses(writer, request, id, handler.subscriptions)
}

func (handler *Handler) getServiceUsage(writer http.ResponseWriter, request *http.Request, id contract.ServiceID) {
	record, err := handler.serviceStore.GetService(request.Context(), id)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	// ?refresh=1 is an operator asking for the provider's current numbers,
	// not the 30s snapshot; scheduled readers never send it.
	refresh := request.URL.Query().Get("refresh") == "1"
	var usage contract.SubscriptionUsage
	switch {
	case record.Service.Kind.IsSubscription():
		if handler.subscriptions == nil {
			writeError(writer, http.StatusServiceUnavailable, "subscription_unavailable", "subscription services are unavailable")
			return
		}
		if refresh {
			handler.subscriptions.ForgetUsage(id)
		}
		usage, err = handler.subscriptions.Usage(request.Context(), id)
	case handler.codingPlans != nil && codingplan.Supports(record.Service.Kind):
		if refresh {
			handler.codingPlans.ForgetUsage(id)
		}
		usage, err = handler.codingPlans.Usage(request.Context(), record.Service)
	default:
		writeError(writer, http.StatusConflict, "service_not_subscription", "service does not support subscription usage")
		return
	}
	if err != nil {
		log.Printf("control: subscription usage %s failed: %s", id, sanitizeUsageError(err))
		writeServiceUsageError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, usage)
}

func (handler *Handler) resetServiceUsage(writer http.ResponseWriter, request *http.Request, id contract.ServiceID) {
	if handler.subscriptions == nil {
		writeError(writer, http.StatusServiceUnavailable, "subscription_unavailable", "subscription services are unavailable")
		return
	}
	record, err := handler.serviceStore.GetService(request.Context(), id)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	if !record.Service.Kind.IsSubscription() {
		writeError(writer, http.StatusConflict, "service_not_subscription", "service does not support subscription usage")
		return
	}
	result, err := handler.subscriptions.ConsumeReset(request.Context(), id)
	if err != nil {
		log.Printf("control: subscription usage reset %s failed: %s", id, sanitizeUsageError(err))
		writeServiceResetError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func writeServiceResetError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscription.ErrNotConnected):
		writeError(writer, http.StatusConflict, "service_not_connected", "subscription service is not connected")
	case errors.Is(err, subscription.ErrNoResetCredit):
		writeError(writer, http.StatusConflict, "no_reset_credit", "no earned reset credits available")
	case errors.Is(err, subscription.ErrNothingToReset):
		writeError(writer, http.StatusConflict, "nothing_to_reset", "no rate-limit window is eligible for a reset")
	case errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err):
		writeError(writer, http.StatusGatewayTimeout, "subscription_usage_reset_timeout", "subscription usage reset timed out")
	default:
		writeError(writer, http.StatusBadGateway, "subscription_usage_reset_failed", sanitizeUsageError(err))
	}
}

func writeServiceUsageError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscription.ErrNotConnected):
		writeError(writer, http.StatusConflict, "service_not_connected", "subscription service is not connected")
	case errors.Is(err, codingplan.ErrUnsupported):
		writeError(writer, http.StatusConflict, "service_not_subscription", "service does not support subscription usage")
	case errors.Is(err, codingplan.ErrCredentialUnavailable):
		writeError(writer, http.StatusConflict, "service_credential_unavailable", "coding plan usage needs the service API key")
	case errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err):
		writeError(writer, http.StatusGatewayTimeout, "subscription_usage_timeout", "subscription usage lookup timed out")
	default:
		writeError(writer, http.StatusBadGateway, "subscription_usage_failed", sanitizeUsageError(err))
	}
}

func sanitizeUsageError(err error) string {
	message := sanitizeSubscriptionError(err)
	if strings.Contains(message, "@") || strings.Contains(strings.ToLower(message), "email") {
		return "subscription usage lookup failed"
	}
	return message
}

func isTimeoutError(err error) bool {
	var timeout timeoutError
	return errors.As(err, &timeout) && timeout.Timeout()
}

type timeoutError interface {
	Timeout() bool
}

// validateServiceCapabilities reports caller-correctable capability errors
// before storage validation collapses them into a generic contract failure.
func (handler *Handler) validateServiceCapabilities(kind contract.ServiceKind, capabilities []contract.Capability) error {
	if kind.IsSubscription() {
		if err := kind.SubscriptionProvider().ValidateCapabilities(capabilities); err != nil {
			return err
		}
	}
	return handler.validateLocalConversions(capabilities)
}

func (handler *Handler) validateLocalConversions(capabilities []contract.Capability) error {
	engine := handler.capabilities.ConversionEngine
	for index, capability := range capabilities {
		if capability.ConvertTo == "" {
			continue
		}
		if !engine.Available {
			return fmt.Errorf("capabilities[%d]: local conversion is unavailable", index)
		}
		matched := false
		for _, edge := range engine.Edges {
			if edge.From == capability.Protocol && edge.To == capability.ConvertTo &&
				(!capability.Streaming || edge.Streaming) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf(
				"capabilities[%d]: no conversion edge from %s to %s",
				index, capability.Protocol, capability.ConvertTo,
			)
		}
	}
	return nil
}

func decodeServiceHTTP(
	rawHTTP json.RawMessage,
	rawCapabilities json.RawMessage,
) (contract.HTTPConnection, []contract.Capability, storage.CredentialMutation, error) {
	var connection contract.HTTPConnection
	var mutation storage.CredentialMutation
	if rawHTTP == nil || isJSONNull(rawHTTP) || rawCapabilities == nil {
		return connection, nil, mutation, fmt.Errorf("http and capabilities are required")
	}
	var input serviceHTTPInput
	if err := strictUnmarshal(rawHTTP, &input); err != nil || input.Auth == nil {
		return connection, nil, mutation, fmt.Errorf("invalid http connection")
	}
	auth, err := decodeServiceAuth(input.Auth)
	if err != nil {
		return connection, nil, mutation, err
	}
	capabilities, err := decodeServiceCapabilities(rawCapabilities)
	if err != nil {
		return connection, nil, mutation, err
	}
	connection = contract.HTTPConnection{BaseURL: input.BaseURL, Auth: auth}
	if input.Credential != nil {
		if isJSONNull(input.Credential) {
			return connection, nil, mutation, fmt.Errorf("credential must be an object")
		}
		var credential credentialInput
		if err := strictUnmarshal(input.Credential, &credential); err != nil || credential.Secret == "" {
			return connection, nil, mutation, fmt.Errorf("credential.secret must not be empty")
		}
		mutation.Present = true
		mutation.Secret = []byte(credential.Secret)
	}
	return connection, capabilities, mutation, nil
}

func applyServicePatch(
	service contract.Service,
	patch map[string]json.RawMessage,
) (contract.Service, storage.CredentialMutation, error) {
	credential := storage.CredentialMutation{}
	if len(patch) == 0 {
		return service, credential, fmt.Errorf("patch is empty")
	}
	allowed := map[string]bool{"proxy": true, "name": true, "enabled": true, "models": true, "failure_policy": true, "responses_websocket_enabled": true, "capabilities": true}
	if service.Kind.IsHTTP() {
		allowed["http"] = true
	}
	for name, raw := range patch {
		if !allowed[name] || (isJSONNull(raw) && name != "failure_policy" && name != "proxy") {
			return service, credential, fmt.Errorf("field %q cannot be patched", name)
		}
	}
	if err := applyServiceProxyInput(&service, &credential, patch["proxy"]); err != nil {
		return service, credential, err
	}
	if raw, ok := patch["failure_policy"]; ok {
		service.FailurePolicy = nil
		if !isJSONNull(raw) {
			if err := strictUnmarshal(raw, &service.FailurePolicy); err != nil {
				return service, credential, err
			}
		}
	}
	if raw, ok := patch["name"]; ok {
		if err := strictUnmarshal(raw, &service.Name); err != nil {
			return service, credential, err
		}
	}
	if raw, ok := patch["enabled"]; ok {
		if err := strictUnmarshal(raw, &service.Enabled); err != nil {
			return service, credential, err
		}
	}
	if raw, ok := patch["responses_websocket_enabled"]; ok {
		var enabled bool
		if err := strictUnmarshal(raw, &enabled); err != nil {
			return service, credential, err
		}
		service.ResponsesWebSocketEnabled = &enabled
	}
	if raw, ok := patch["models"]; ok {
		models, err := decodeServiceModels(raw)
		if err != nil {
			return service, credential, err
		}
		service.Models = models
	}
	if raw, ok := patch["capabilities"]; ok {
		capabilities, err := decodeServiceCapabilities(raw)
		if err != nil {
			return service, credential, err
		}
		service.Capabilities = capabilities
	}
	if raw, ok := patch["http"]; ok {
		var fields map[string]json.RawMessage
		if err := strictUnmarshal(raw, &fields); err != nil || service.HTTP == nil {
			return service, credential, fmt.Errorf("invalid http patch")
		}
		for name := range fields {
			if name != "base_url" && name != "auth" && name != "credential" {
				return service, credential, fmt.Errorf("unknown http field %q", name)
			}
		}
		if value, ok := fields["base_url"]; ok {
			if isJSONNull(value) || strictUnmarshal(value, &service.HTTP.BaseURL) != nil {
				return service, credential, fmt.Errorf("invalid base_url")
			}
		}
		if value, ok := fields["auth"]; ok {
			auth, err := decodeServiceAuth(value)
			if err != nil {
				return service, credential, err
			}
			service.HTTP.Auth = auth
		}
		if value, ok := fields["credential"]; ok {
			credential.Present = true
			if !isJSONNull(value) {
				var input credentialInput
				if err := strictUnmarshal(value, &input); err != nil || input.Secret == "" {
					return service, credential, fmt.Errorf("credential.secret must not be empty")
				}
				credential.Secret = []byte(input.Secret)
			}
		}
	}
	return service, credential, nil
}

func parseServiceListOptions(request *http.Request) (storage.ServiceListOptions, error) {
	query := request.URL.Query()
	for name := range query {
		if name != "limit" && name != "cursor" && name != "enabled" && name != "kind" {
			return storage.ServiceListOptions{}, fmt.Errorf("unknown query parameter")
		}
		if len(query[name]) != 1 {
			return storage.ServiceListOptions{}, fmt.Errorf("query parameter must occur once")
		}
	}
	options := storage.ServiceListOptions{Cursor: query.Get("cursor")}
	if len(options.Cursor) > 512 {
		return options, fmt.Errorf("cursor too long")
	}
	if value := query.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 200 {
			return options, fmt.Errorf("invalid limit")
		}
		options.Limit = limit
	}
	if value := query.Get("enabled"); value != "" {
		if value != "true" && value != "false" {
			return options, fmt.Errorf("invalid enabled filter")
		}
		enabled := value == "true"
		options.Enabled = &enabled
	}
	if value := query.Get("kind"); value != "" {
		kind := contract.ServiceKind(value)
		if !kind.Valid() {
			return options, fmt.Errorf("invalid kind filter")
		}
		options.Kind = &kind
	}
	return options, nil
}

func parseServiceID(writer http.ResponseWriter, rawID string) (contract.ServiceID, bool) {
	decodedID, err := url.PathUnescape(rawID)
	if err != nil || decodedID != rawID {
		writeError(writer, http.StatusBadRequest, "invalid_service_id", "service_id must use its canonical form")
		return "", false
	}
	id := contract.ServiceID(decodedID)
	if err := id.Validate(); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_service_id", "service_id is invalid")
		return "", false
	}
	return id, true
}

func writeMethodNotAllowed(writer http.ResponseWriter, allowed string) {
	writer.Header().Set("Allow", allowed)
	writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
}

func probeSubscriptionResponses(
	writer http.ResponseWriter,
	request *http.Request,
	id contract.ServiceID,
	manager *subscription.Manager,
) {
	account, err := manager.Get(request.Context(), id)
	if err != nil || account.Provider != contract.SubscriptionProviderOpenAICodex {
		writeError(writer, http.StatusUnprocessableEntity, "subscription_probe_unsupported", "Responses probe requires a Codex subscription")
		return
	}
	var body subscriptionResponsesProbeRequest
	if request.Body != nil && request.ContentLength != 0 {
		if !decodeControlJSON(writer, request, &body) {
			return
		}
	}
	proxyContext, proxyErr := manager.ProxyContext(request.Context(), id)
	if proxyErr != nil {
		writeError(writer, http.StatusBadGateway, "instance_proxy_unavailable", "instance proxy configuration unavailable")
		return
	}
	request = request.WithContext(proxyContext)
	tokens, err := manager.AccessToken(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusConflict, "service_not_connected", "subscription service is not connected")
		return
	}
	raw, status, err := manager.Provider().ProbeNonStreamingResponse(request.Context(), tokens, body.Model)
	if err != nil {
		writeError(writer, http.StatusBadGateway, "subscription_probe_failed", "responses probe through subscription failed")
		return
	}
	responseID := ""
	var parsed map[string]any
	if json.Unmarshal(raw, &parsed) == nil {
		responseID, _ = parsed["id"].(string)
	}
	writeJSON(writer, http.StatusOK, struct {
		ServiceID  contract.ServiceID `json:"service_id"`
		StatusCode int                `json:"status_code"`
		OK         bool               `json:"ok"`
		ResponseID string             `json:"response_id,omitempty"`
	}{ServiceID: id, StatusCode: status, OK: status >= 200 && status < 300, ResponseID: responseID})
}

func sanitizeSubscriptionError(err error) string {
	message := err.Error()
	lower := strings.ToLower(message)
	if strings.Contains(lower, "bearer ") ||
		strings.Contains(lower, "access_token") ||
		strings.Contains(lower, "refresh_token") ||
		strings.Contains(lower, "id_token") ||
		strings.Contains(lower, "device_auth_id") ||
		strings.Contains(lower, "code_verifier") ||
		strings.Contains(lower, "authorization_code") {
		return "authorization request failed"
	}
	if len(message) > 180 {
		return message[:180]
	}
	return message
}
