package controlapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const (
	RequestsPath      = "/control/v1/requests"
	RequestsPurgePath = RequestsPath + "/purge"
)

type requestRecordPageResponse struct {
	Items      []contract.RequestRecord `json:"items"`
	NextCursor *string                  `json:"next_cursor"`
}

func (handler *Handler) registerRequestRecordRoutes() {
	handler.mux.HandleFunc(RequestsPurgePath, handler.authenticated(handler.purgeRequestRecords))
	handler.mux.HandleFunc(RequestsPath, handler.authenticated(handler.requestRecordCollection))
	handler.mux.HandleFunc(RequestsPath+"/", handler.authenticated(handler.requestRecordItem))
}

func (handler *Handler) requestRecordCollection(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	options, err := parseRequestRecordListOptions(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	page, err := handler.requestRecords.ListRequestRecords(request.Context(), options)
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	response := requestRecordPageResponse{Items: page.Items}
	if response.Items == nil {
		response.Items = []contract.RequestRecord{}
	}
	if page.NextCursor != "" {
		response.NextCursor = &page.NextCursor
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) requestRecordItem(writer http.ResponseWriter, request *http.Request) {
	rawID := strings.TrimPrefix(request.URL.Path, RequestsPath+"/")
	if rawID == "" {
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	if strings.HasSuffix(rawID, "/audit") {
		idPart := strings.TrimSuffix(rawID, "/audit")
		if idPart == "" || strings.Contains(idPart, "/") {
			writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
			return
		}
		handler.getRequestAuditContent(writer, request, idPart)
		return
	}
	if strings.HasSuffix(rawID, "/children") {
		idPart := strings.TrimSuffix(rawID, "/children")
		if idPart == "" || strings.Contains(idPart, "/") {
			writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
			return
		}
		handler.listRequestRecordChildren(writer, request, idPart)
		return
	}
	if strings.Contains(rawID, "/") {
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	decodedID, err := url.PathUnescape(rawID)
	if err != nil || decodedID != rawID {
		writeError(writer, http.StatusBadRequest, "invalid_request_id", "request_id must use its canonical form")
		return
	}
	id := contract.RequestID(decodedID)
	if err := id.Validate(); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request_id", "request_id is invalid")
		return
	}
	switch request.Method {
	case http.MethodGet:
		record, err := handler.requestRecords.GetRequestRecord(request.Context(), id)
		if err != nil {
			handler.writeRequestRecordStoreError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, record)
	case http.MethodDelete:
		if err := handler.requestRecords.DeleteRequestRecord(request.Context(), id); err != nil {
			handler.writeRequestRecordStoreError(writer, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodDelete)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and DELETE are allowed")
	}
}

func (handler *Handler) getRequestAuditContent(writer http.ResponseWriter, request *http.Request, rawID string) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	if handler.auditBlobs == nil {
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	decodedID, err := url.PathUnescape(rawID)
	if err != nil || decodedID != rawID {
		writeError(writer, http.StatusBadRequest, "invalid_request_id", "request_id must use its canonical form")
		return
	}
	id := contract.RequestID(decodedID)
	if err := id.Validate(); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request_id", "request_id is invalid")
		return
	}
	if _, err := handler.requestRecords.GetRequestRecord(request.Context(), id); err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	blobs, err := handler.auditBlobs.GetAuditBlobsByRequest(request.Context(), id)
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	content := contract.AuditContent{RequestID: id}
	if len(blobs) == 0 {
		writeJSON(writer, http.StatusOK, content)
		return
	}
	if handler.auditKeys == nil {
		writeError(writer, http.StatusConflict, "audit_key_missing", "audit content cannot be decrypted")
		return
	}
	key, err := handler.auditKeys.GetAuditKey(request.Context())
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(writer, http.StatusConflict, "audit_key_missing", "audit content cannot be decrypted")
			return
		}
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "audit key storage is unavailable")
		return
	}
	for _, blob := range blobs {
		switch blob.Direction {
		case storage.AuditDirectionHTTPMeta, storage.AuditDirectionUpstreamHTTPMeta:
			plaintext, err := storage.OpenAuditBlob(key, blob.Nonce, blob.Ciphertext)
			if err != nil {
				writeError(writer, http.StatusConflict, "audit_decrypt_failed", "audit content cannot be decrypted")
				return
			}
			var meta contract.AuditHTTPMeta
			if err := json.Unmarshal(plaintext, &meta); err != nil {
				// A corrupt meta payload leaves that meta field null instead of
				// failing the whole detail view.
				continue
			}
			if blob.Direction == storage.AuditDirectionHTTPMeta {
				content.HTTPMeta = &meta
			} else {
				content.UpstreamHTTPMeta = &meta
			}
		case storage.AuditDirectionRequest,
			storage.AuditDirectionResponse,
			storage.AuditDirectionUpstreamRequest,
			storage.AuditDirectionUpstreamResponse:
			part, err := decryptAuditContentPart(key, blob)
			if err != nil {
				writeError(writer, http.StatusConflict, "audit_decrypt_failed", "audit content cannot be decrypted")
				return
			}
			switch blob.Direction {
			case storage.AuditDirectionRequest:
				content.RequestBody = part
			case storage.AuditDirectionResponse:
				content.ResponseContent = part
			case storage.AuditDirectionUpstreamRequest:
				content.UpstreamRequestBody = part
			case storage.AuditDirectionUpstreamResponse:
				content.UpstreamResponseContent = part
			}
		}
	}
	writeJSON(writer, http.StatusOK, content)
}

func (handler *Handler) listRequestRecordChildren(
	writer http.ResponseWriter,
	request *http.Request,
	rawID string,
) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	decodedID, err := url.PathUnescape(rawID)
	if err != nil || decodedID != rawID {
		writeError(writer, http.StatusBadRequest, "invalid_request_id", "request_id must use its canonical form")
		return
	}
	id := contract.RequestID(decodedID)
	if err := id.Validate(); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request_id", "request_id is invalid")
		return
	}
	children, err := handler.requestRecords.ListRequestRecordChildren(request.Context(), id)
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	if children == nil {
		children = []contract.RequestRecord{}
	}
	writeJSON(writer, http.StatusOK, requestRecordPageResponse{Items: children})
}

func decryptAuditContentPart(key []byte, blob storage.AuditBlob) (*contract.AuditContentPart, error) {
	plaintext, err := storage.OpenAuditBlob(key, blob.Nonce, blob.Ciphertext)
	if err != nil {
		return nil, err
	}
	// Decrypted content is returned as UTF-8 when valid; otherwise base64 so
	// the JSON string stays well-formed. media_type is left unchanged either way.
	content := string(plaintext)
	if !utf8.Valid(plaintext) {
		content = base64.StdEncoding.EncodeToString(plaintext)
	}
	return &contract.AuditContentPart{
		MediaType:     blob.MediaType,
		Content:       content,
		Truncated:     blob.Truncated,
		CapturedBytes: blob.CapturedBytes,
	}, nil
}

func (handler *Handler) purgeRequestRecords(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var input contract.PurgeRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if err := input.Validate(); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_purge_request", err.Error())
		return
	}
	result, err := handler.requestRecords.PurgeRequestRecords(request.Context(), input)
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func parseRequestRecordListOptions(request *http.Request) (storage.RequestRecordListOptions, error) {
	query := request.URL.Query()
	for name := range query {
		switch name {
		case "limit", "cursor", "from", "to", "protocol", "service_id", "status":
		default:
			return storage.RequestRecordListOptions{}, fmt.Errorf("unknown query parameter")
		}
		if len(query[name]) != 1 {
			return storage.RequestRecordListOptions{}, fmt.Errorf("query parameter must occur once")
		}
	}
	options := storage.RequestRecordListOptions{Cursor: query.Get("cursor")}
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
	if value := query.Get("from"); value != "" {
		from, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			from, err = time.Parse(time.RFC3339, value)
		}
		if err != nil {
			return options, fmt.Errorf("invalid from timestamp")
		}
		from = from.UTC()
		options.From = &from
	}
	if value := query.Get("to"); value != "" {
		to, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			to, err = time.Parse(time.RFC3339, value)
		}
		if err != nil {
			return options, fmt.Errorf("invalid to timestamp")
		}
		to = to.UTC()
		options.To = &to
	}
	if value := query.Get("protocol"); value != "" {
		protocol := contract.ProtocolID(value)
		if err := protocol.Validate(); err != nil {
			return options, fmt.Errorf("invalid protocol filter")
		}
		options.Protocol = &protocol
	}
	if value := query.Get("service_id"); value != "" {
		serviceID := contract.ServiceID(value)
		if err := serviceID.Validate(); err != nil {
			return options, fmt.Errorf("invalid service_id filter")
		}
		options.ServiceID = &serviceID
	}
	if value := query.Get("status"); value != "" {
		status := contract.RequestStatus(value)
		if !status.Valid() {
			return options, fmt.Errorf("invalid status filter")
		}
		options.Status = &status
	}
	return options, nil
}

func (handler *Handler) writeRequestRecordStoreError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "request record not found")
	case errors.Is(err, storage.ErrInvalidArgument), errors.Is(err, storage.ErrInvalidCursor):
		writeError(writer, http.StatusBadRequest, "invalid_query", "request record query is invalid")
	case errors.Is(err, storage.ErrInvalidRecord):
		writeError(writer, http.StatusInternalServerError, "invalid_record", "persisted request record is invalid")
	default:
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "request record storage is unavailable")
	}
}
