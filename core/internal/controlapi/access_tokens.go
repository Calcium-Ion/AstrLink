package controlapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type accessTokenCreateRequest struct {
	Name string `json:"name"`
}

type accessTokenResponse struct {
	ID        contract.AccessTokenID `json:"id"`
	Name      string                 `json:"name"`
	Hint      string                 `json:"hint"`
	Source    string                 `json:"source"`
	CreatedAt time.Time              `json:"created_at"`
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
	handler.mux.HandleFunc(AccessTokensPath, handler.authenticated(handler.accessTokenCollection))
	handler.mux.HandleFunc(AccessTokensPath+"/", handler.authenticated(handler.accessTokenItem))
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
			writeError(writer, http.StatusNotFound, "not_found", "control endpoint not found")
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
		writeError(writer, http.StatusNotFound, "not_found", "control endpoint not found")
		return
	}
	id, ok := parseAccessTokenID(writer, suffix)
	if !ok {
		return
	}
	if request.Method != http.MethodDelete {
		writer.Header().Set("Allow", http.MethodDelete)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only DELETE is allowed")
		return
	}
	handler.deleteAccessToken(writer, request, id)
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
	created, err := handler.accessTokens.Create(request.Context(), input.Name)
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
	return accessTokenResponse{
		ID: token.ID, Name: token.Name, Hint: token.Hint,
		Source: string(token.Source), CreatedAt: token.CreatedAt,
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
	case errors.Is(err, accesstoken.ErrInvalidName), errors.Is(err, storage.ErrInvalidArgument):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_access_token", "access token name violates the contract")
	case errors.Is(err, storage.ErrInvalidRecord):
		writeError(writer, http.StatusInternalServerError, "persisted_state_invalid", "persisted access token state failed validation")
	default:
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "persistent access token storage is unavailable")
	}
}
