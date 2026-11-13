package controlapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// accessTokenCreateRequest omits service_scope for a token that may use
// every provider, as clients that predate scopes do.
type accessTokenCreateRequest struct {
	Name         string                            `json:"name"`
	ServiceScope *contract.AccessTokenServiceScope `json:"service_scope,omitempty"`
	ServiceIDs   []contract.ServiceID              `json:"service_ids,omitempty"`
}

// accessTokenUpdateRequest replaces the name and the provider scope together.
type accessTokenUpdateRequest struct {
	Name         string                           `json:"name"`
	ServiceScope contract.AccessTokenServiceScope `json:"service_scope"`
	ServiceIDs   []contract.ServiceID             `json:"service_ids"`
}

type accessTokenResponse struct {
	ID           contract.AccessTokenID           `json:"id"`
	Name         string                           `json:"name"`
	Hint         string                           `json:"hint"`
	Source       storage.AccessTokenSource        `json:"source"`
	CreatedAt    time.Time                        `json:"created_at"`
	ServiceScope contract.AccessTokenServiceScope `json:"service_scope"`
	ServiceIDs   []contract.ServiceID             `json:"service_ids"`
}

type accessTokenListResponse struct {
	Items      []accessTokenResponse `json:"items"`
	NextCursor *string               `json:"next_cursor"`
}

type accessTokenSecretResponse struct {
	Token       accessTokenResponse `json:"token"`
	AccessToken string              `json:"access_token"`
}

type accessTokenRevealResponse struct {
	AccessToken string `json:"access_token"`
}

func (handler *Handler) registerAccessTokenRoutes() {
	handler.mux.HandleFunc(AccessTokensPath, handler.authenticated(handler.accessTokenCollection, RoleObserver))
	handler.mux.HandleFunc(AccessTokensPath+"/", handler.authenticatedBy(handler.accessTokenItem, accessTokenItemRole))
}

// accessTokenItemRole keeps the secret reveal operator-only; listing token
// metadata stays observable.
func accessTokenItemRole(request *http.Request) Role {
	if isSafeMethod(request.Method) && !strings.HasSuffix(request.URL.Path, "/secret") {
		return RoleObserver
	}
	return RoleOperator
}

func (handler *Handler) accessTokenCollection(writer http.ResponseWriter, request *http.Request) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "access token operations do not accept query parameters")
		return
	}
	switch request.Method {
	case http.MethodGet:
		handler.listAccessTokens(writer, request)
	case http.MethodPost:
		handler.createAccessToken(writer, request)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and POST are allowed")
	}
}

func (handler *Handler) accessTokenItem(writer http.ResponseWriter, request *http.Request) {
	suffix := strings.TrimPrefix(request.URL.Path, AccessTokensPath+"/")
	if strings.HasSuffix(suffix, "/secret") {
		rawID := strings.TrimSuffix(suffix, "/secret")
		if rawID == "" || strings.Contains(rawID, "/") {
			writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
			return
		}
		id, ok := parseAccessTokenID(writer, rawID)
		if !ok {
			return
		}
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
			return
		}
		handler.revealAccessToken(writer, request, id)
		return
	}
	if suffix == "" || strings.Contains(suffix, "/") {
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	id, ok := parseAccessTokenID(writer, suffix)
	if !ok {
		return
	}
	switch request.Method {
	case http.MethodDelete:
		handler.deleteAccessToken(writer, request, id)
	case http.MethodPatch:
		handler.updateAccessToken(writer, request, id)
	default:
		writer.Header().Set("Allow", http.MethodPatch+", "+http.MethodDelete)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only PATCH and DELETE are allowed")
	}
}

func parseAccessTokenID(writer http.ResponseWriter, rawID string) (contract.AccessTokenID, bool) {
	decodedID, err := url.PathUnescape(rawID)
	if err != nil || decodedID != rawID {
		writeError(writer, http.StatusBadRequest, "invalid_access_token_id", "token_id must use its canonical form")
		return "", false
	}
	id := contract.AccessTokenID(decodedID)
	if err := id.Validate(); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_access_token_id", "token_id is invalid")
		return "", false
	}
	return id, true
}

func (handler *Handler) listAccessTokens(writer http.ResponseWriter, request *http.Request) {
	tokens, err := handler.accessTokens.List(request.Context())
	if err != nil {
		handler.writeAccessTokenError(writer, err)
		return
	}
	response := accessTokenListResponse{Items: make([]accessTokenResponse, 0, len(tokens))}
	for _, token := range tokens {
		response.Items = append(response.Items, accessTokenMetadata(token))
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) createAccessToken(writer http.ResponseWriter, request *http.Request) {
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var input accessTokenCreateRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	var services []contract.AccessTokenServices
	if input.ServiceScope != nil {
		scope := contract.AccessTokenServices{Scope: *input.ServiceScope, ServiceIDs: input.ServiceIDs}
		if !handler.checkAccessTokenServices(writer, request, scope) {
			return
		}
		services = append(services, scope)
	} else if len(input.ServiceIDs) > 0 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_access_token_services", "service_ids needs service_scope selected")
		return
	}
	created, err := handler.accessTokens.Create(request.Context(), input.Name, services...)
	if err != nil {
		handler.writeAccessTokenError(writer, err)
		return
	}
	writer.Header().Set("Location", AccessTokensPath+"/"+string(created.Token.ID))
	writeJSON(writer, http.StatusCreated, accessTokenSecretResponse{
		Token:       accessTokenMetadata(created.Token),
		AccessToken: created.Value,
	})
}

func (handler *Handler) updateAccessToken(
	writer http.ResponseWriter,
	request *http.Request,
	id contract.AccessTokenID,
) {
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var input accessTokenUpdateRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	services := contract.AccessTokenServices{Scope: input.ServiceScope, ServiceIDs: input.ServiceIDs}
	if services.ServiceIDs == nil {
		services.ServiceIDs = []contract.ServiceID{}
	}
	if !handler.checkAccessTokenServices(writer, request, services) {
		return
	}
	token, err := handler.accessTokens.Update(request.Context(), id, input.Name, services)
	if err != nil {
		handler.writeAccessTokenError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, accessTokenMetadata(token))
}

// checkAccessTokenServices rejects a scope that is malformed or names a
// provider this gateway does not have. Providers deleted later stay listed
// and match nothing.
func (handler *Handler) checkAccessTokenServices(writer http.ResponseWriter, request *http.Request, services contract.AccessTokenServices) bool {
	if err := services.Validate(); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_access_token_services", err.Error())
		return false
	}
	if handler.serviceStore == nil {
		return true
	}
	for _, id := range services.ServiceIDs {
		if _, err := handler.serviceStore.GetService(request.Context(), id); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(writer, http.StatusUnprocessableEntity, "invalid_access_token_services", fmt.Sprintf("API provider %q does not exist", id))
				return false
			}
			writeError(writer, http.StatusInternalServerError, "storage_unavailable", "persistent service storage is unavailable")
			return false
		}
	}
	return true
}

func (handler *Handler) revealAccessToken(
	writer http.ResponseWriter,
	request *http.Request,
	id contract.AccessTokenID,
) {
	secret, err := handler.accessTokens.Reveal(request.Context(), id)
	if err != nil {
		handler.writeAccessTokenError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, accessTokenRevealResponse{AccessToken: secret})
}

func (handler *Handler) deleteAccessToken(
	writer http.ResponseWriter,
	request *http.Request,
	id contract.AccessTokenID,
) {
	if err := handler.accessTokens.Delete(request.Context(), id); err != nil {
		handler.writeAccessTokenError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func accessTokenMetadata(token accesstoken.Token) accessTokenResponse {
	scope, ids := token.Services.Scope, token.Services.ServiceIDs
	if scope == "" {
		scope = contract.AccessTokenServicesAll
	}
	if ids == nil {
		ids = []contract.ServiceID{}
	}
	return accessTokenResponse{
		ID: token.ID, Name: token.Name, Hint: token.Hint, Source: token.Source,
		CreatedAt: token.CreatedAt, ServiceScope: scope, ServiceIDs: ids,
	}
}

func (handler *Handler) writeAccessTokenError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "access token not found")
	case errors.Is(err, storage.ErrConflict):
		writeError(writer, http.StatusConflict, "conflict", "access token name conflicts with current state")
	case errors.Is(err, accesstoken.ErrTokenLimit):
		writeError(writer, http.StatusConflict, "access_token_limit", "the maximum number of access tokens has been reached")
	case errors.Is(err, accesstoken.ErrInvalidServices):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_access_token_services", "access token providers violate the contract")
	case errors.Is(err, accesstoken.ErrInvalidName), errors.Is(err, storage.ErrInvalidArgument):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_access_token", "access token name violates the contract")
	case errors.Is(err, secretstore.ErrUnavailable):
		// The token still authenticates; only its saved copy is unreadable.
		writeError(writer, http.StatusConflict, "access_token_unreadable", "the saved access token does not decrypt on this device; create a new token to see its value")
	case errors.Is(err, storage.ErrInvalidRecord):
		writeError(writer, http.StatusInternalServerError, "persisted_state_invalid", "persisted access token state failed validation")
	default:
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "persistent access token storage is unavailable")
	}
}
