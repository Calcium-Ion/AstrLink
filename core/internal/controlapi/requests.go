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
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const (
	RequestsPath        = "/control/v1/requests"
	RequestsPurgePath   = RequestsPath + "/purge"
	RequestSessionsPath = "/control/v1/request-sessions"

	maxLocalAccessTokenFilters = 100
	maxModelFilters            = 100
	maxModelFilterRunes        = 256
	maxRequestSearchRunes      = 200
)

type requestRecordPageResponse struct {
	Items      []contract.RequestRecord `json:"items"`
	NextCursor *string                  `json:"next_cursor"`
}

func (handler *Handler) registerRequestRecordRoutes() {
	handler.mux.HandleFunc(UsageSummaryPath, handler.authenticated(handler.getUsageSummary, RoleObserver))
	handler.mux.HandleFunc(PrivacySummaryPath, handler.authenticated(handler.getPrivacySummary, RoleObserver))
	handler.mux.HandleFunc(AccessTokenUsagePath, handler.authenticated(handler.listAccessTokenUsage, RoleObserver))
	handler.mux.HandleFunc(RequestsPurgePath, handler.authenticated(handler.purgeRequestRecords, RoleOperator))
	handler.mux.HandleFunc(RequestSessionsPath, handler.authenticated(handler.requestSessionCollection, RoleObserver))
	handler.mux.HandleFunc(RequestSessionsPath+"/", handler.authenticated(handler.requestSessionItem, RoleObserver))
	handler.mux.HandleFunc(RequestActivityPath, handler.authenticated(handler.getRequestActivity, RoleObserver))
	handler.mux.HandleFunc(RequestsPath, handler.authenticated(handler.requestRecordCollection, RoleObserver))
	handler.mux.HandleFunc(RequestsPath+"/", handler.authenticatedBy(handler.requestRecordItem, requestRecordItemRole))
}

// requestRecordItemRole admits observers to reads and to filing a raw
// access request; every other write stays operator-only.
func requestRecordItemRole(request *http.Request) Role {
	if isSafeMethod(request.Method) {
		return RoleObserver
	}
	if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/audit/raw-access") {
		return RoleObserver
	}
	return RoleOperator
}

type requestSessionPageResponse struct {
	Items      []contract.RequestSession `json:"items"`
	NextCursor *string                   `json:"next_cursor"`
}

func (handler *Handler) requestSessionCollection(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	options, err := parseRequestSessionListOptions(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	page, err := handler.requestRecords.ListRequestSessions(request.Context(), options)
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	response := requestSessionPageResponse{Items: page.Items}
	if response.Items == nil {
		response.Items = []contract.RequestSession{}
	}
	if page.NextCursor != "" {
		response.NextCursor = &page.NextCursor
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) requestSessionItem(writer http.ResponseWriter, request *http.Request) {
	if strings.HasSuffix(request.URL.Path, "/channel-bindings") {
		handler.sessionChannelBinding(writer, request)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	rawID := strings.TrimPrefix(request.URL.Path, RequestSessionsPath+"/")
	if rawID == "" || strings.Contains(rawID, "/") {
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	decodedID, err := url.PathUnescape(rawID)
	if err != nil || decodedID != rawID {
		writeError(writer, http.StatusBadRequest, "invalid_session_id", "session_id must use its canonical form")
		return
	}
	detail, err := handler.requestRecords.GetRequestSession(request.Context(), decodedID)
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func parseRequestSessionListOptions(request *http.Request) (storage.RequestSessionListOptions, error) {
	query := request.URL.Query()
	if len(query["kind"]) > 1 {
		return storage.RequestSessionListOptions{}, fmt.Errorf("query parameter must occur once")
	}
	kind := query.Get("kind")
	if kind != "" && kind != "inference" && kind != "discovery" {
		return storage.RequestSessionListOptions{}, fmt.Errorf("kind must be inference or discovery")
	}
	query.Del("kind")
	if query.Has("q") {
		return storage.RequestSessionListOptions{}, fmt.Errorf("q is only supported on request records")
	}
	recordOptions, err := parseRequestRecordQuery(query)
	if err != nil {
		return storage.RequestSessionListOptions{}, err
	}
	return storage.RequestSessionListOptions{
		Kind:                kind,
		Limit:               recordOptions.Limit,
		Cursor:              recordOptions.Cursor,
		From:                recordOptions.From,
		To:                  recordOptions.To,
		Protocol:            recordOptions.Protocol,
		ServiceID:           recordOptions.ServiceID,
		LocalAccessTokenIDs: recordOptions.LocalAccessTokenIDs,
		Models:              recordOptions.Models,
		Status:              recordOptions.Status,
	}, nil
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
	if strings.HasSuffix(rawID, "/audit/raw-access") {
		idPart := strings.TrimSuffix(rawID, "/audit/raw-access")
		if idPart == "" || strings.Contains(idPart, "/") || handler.auditBlobs == nil {
			writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
			return
		}
		handler.requestRawAccess(writer, request, idPart)
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
	id, ok := parseRequestIDSegment(writer, rawID)
	if !ok {
		return
	}
	view := contract.AuditContentViewFull
	if values, present := request.URL.Query()["view"]; present {
		if len(values) != 1 || !contract.AuditContentView(values[0]).Valid() {
			writeValidationFailed(writer, "view is invalid", []errorDetail{{
				Field: "view", Reason: "must be shareable or full",
			}})
			return
		}
		view = contract.AuditContentView(values[0])
	}
	reader := &auditReader{handler: handler, request: request, view: view}
	grantToken := strings.TrimSpace(request.Header.Get(RawGrantHeader))
	if view == contract.AuditContentViewFull {
		switch {
		case grantToken != "":
			// Turning agent raw access off revokes every grant; checking
			// the switch here also covers a grant filed while it changed.
			enabled, err := handler.agentRawAccessEnabled(request.Context())
			if err != nil {
				handler.writeAuditSettingsStoreError(writer, err)
				return
			}
			if !enabled {
				writeError(writer, http.StatusConflict, "raw_access_disabled", "agent raw access requests are turned off")
				return
			}
			if !writeRawGrantError(writer, handler.rawGrants.check(grantToken, id)) {
				return
			}
		case requestRole(request) < RoleOperator:
			writeError(writer, http.StatusForbidden, "forbidden",
				"the full audit view needs the operator role or an approved raw grant")
			return
		}
	}
	record, err := handler.requestRecords.GetRequestRecord(request.Context(), id)
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	reader.record = record
	var blobs []storage.AuditBlob
	if view == contract.AuditContentViewShareable {
		blobs, err = handler.auditBlobs.GetShareableAuditBlobsByRequest(request.Context(), id)
	} else {
		blobs, err = handler.auditBlobs.GetAuditBlobsByRequest(request.Context(), id)
	}
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	if view == contract.AuditContentViewFull {
		if grantToken != "" {
			// Spend the read before opening anything, so a once grant
			// cannot serve two concurrent reads.
			lease, err := handler.rawGrants.claim(grantToken, id)
			if !writeRawGrantError(writer, err) {
				return
			}
			defer lease.release()
			reader.lease = lease
		} else if !reader.prepareOperator(writer) {
			return
		}
	}
	defer reader.close()
	content, err := reader.content(id, blobs)
	if err != nil {
		reader.writeOpenError(writer, err)
		return
	}
	switch {
	case reader.lease != nil:
		handler.observers.noteRead(request, ReadLevelRaw)
		handler.observers.noteRawEvent(RawAccessEventRawRead, reader.lease.grant)
	case view == contract.AuditContentViewShareable:
		handler.observers.noteRead(request, ReadLevelShareable)
	}
	writeJSON(writer, http.StatusOK, content)
}

// content opens every part one read may see into the audit view.
func (reader *auditReader) content(id contract.RequestID, blobs []storage.AuditBlob) (contract.AuditContent, error) {
	content := contract.AuditContent{
		RequestID: id, View: reader.view,
		PrivacyFindings: append([]contract.PrivacyFinding{}, reader.record.PrivacyFindings...),
	}
	for _, blob := range blobs {
		switch blob.Direction {
		case storage.AuditDirectionHTTPMeta, storage.AuditDirectionUpstreamHTTPMeta:
			plaintext, withheld, err := reader.open(blob)
			if err != nil {
				return contract.AuditContent{}, err
			}
			if withheld != "" {
				continue
			}
			var meta contract.AuditHTTPMeta
			err = json.Unmarshal(plaintext, &meta)
			clear(plaintext)
			if err != nil {
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
			part, err := reader.part(blob)
			if err != nil {
				return contract.AuditContent{}, err
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
	return content, nil
}

// writeRawGrantError maps a grant lookup failure onto its wire code.
func writeRawGrantError(writer http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, errRawGrantPending):
		writeError(writer, http.StatusConflict, "raw_access_pending", "raw access is awaiting approval on the desktop")
	case errors.Is(err, errRawGrantDenied):
		writeError(writer, http.StatusForbidden, "raw_access_denied", "raw access was denied on the desktop")
	default:
		writeError(writer, http.StatusForbidden, "raw_grant_invalid",
			"raw grant is unknown, expired, revoked, or for another request; request raw access again")
	}
	return false
}

// auditReader opens the parts one audit read may see. Shareable parts open
// with the audit key; the rest open only in the full view, through an
// approved grant or the operator's unlock session. Until a raw password is
// set, nobody reads them: parts captured before then were never kept.
type auditReader struct {
	handler *Handler
	request *http.Request
	view    contract.AuditContentView
	record  contract.RequestRecord
	lease   *rawGrantLease
	sealing RawVaultStatus
	// opener is the operator's unlock session once looked up; locked
	// withholds raw parts from an operator without one.
	sessionChecked bool
	opener         RawKeyOpener
	locked         bool
	auditKey       []byte
	keyErr         error
	keyRead        bool
	// rawAvailable caches whether an agent may ask for raw parts.
	rawAvailable *bool
}

var errAuditKeyMissing = errors.New("audit key missing")

// prepareOperator settles how an operator's full read treats raw parts.
func (reader *auditReader) prepareOperator(writer http.ResponseWriter) bool {
	status, err := reader.handler.rawVaultStatus(reader.request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "raw_vault_unavailable", "raw sealing state is unavailable")
		return false
	}
	reader.sealing = status
	return true
}

func (reader *auditReader) close() { clear(reader.auditKey) }

func (reader *auditReader) key() ([]byte, error) {
	if !reader.keyRead {
		reader.keyRead = true
		if reader.handler.auditKeys == nil {
			reader.keyErr = errAuditKeyMissing
		} else {
			reader.auditKey, reader.keyErr = reader.handler.auditKeys.GetAuditKey(reader.request.Context())
			if errors.Is(reader.keyErr, storage.ErrNotFound) {
				reader.keyErr = errAuditKeyMissing
			}
		}
	}
	return reader.auditKey, reader.keyErr
}

// open returns a part's plaintext, or the reason it is withheld.
func (reader *auditReader) open(blob storage.AuditBlob) ([]byte, contract.AuditWithheldReason, error) {
	if blob.Exposure != storage.AuditExposureShareable {
		if blob.Sealing == storage.AuditSealingNone {
			return nil, contract.AuditWithheldRawNotKept, nil
		}
		if reader.view == contract.AuditContentViewShareable {
			return nil, privacyWithheldReason(reader.record, blob), nil
		}
		if reader.lease == nil && !reader.sealing.PasswordSet {
			// No audit-key fallback: the local key alone must not read raw
			// content.
			if blob.Exposure == storage.AuditExposurePending {
				return nil, contract.AuditWithheldPrivacyPending, nil
			}
			return nil, contract.AuditWithheldRawNotKept, nil
		}
		if reader.lease == nil && !reader.sessionChecked {
			// Looked up once per read: a raw read is what keeps the
			// unlock session from idling out.
			reader.sessionChecked = true
			opener, unlocked := reader.handler.rawVault.UnlockedOpener()
			reader.opener, reader.locked = opener, !unlocked || opener == nil
		}
		if reader.lease == nil && reader.locked {
			return nil, contract.AuditWithheldRawLocked, nil
		}
		if rawSealed(blob) {
			return reader.openRawSealed(blob)
		}
	}
	key, err := reader.key()
	if err != nil {
		return nil, "", err
	}
	var plaintext []byte
	if blob.Layout == storage.AuditLayoutChunks {
		plaintext, err = storage.OpenAuditChunks(key, blob.Chunks)
	} else {
		plaintext, err = storage.OpenAuditBlob(key, blob.Nonce, blob.Ciphertext)
	}
	if errors.Is(err, storage.ErrAuditDecrypt) && reader.auditKeyOrphaned() {
		// Sealed under an audit key this device lost with its local key.
		err = errAuditKeyMissing
	}
	return plaintext, "", err
}

// auditKeyOrphaned reports whether the store set aside an audit key it can
// no longer open, so a failed decryption means the key is missing.
func (reader *auditReader) auditKeyOrphaned() bool {
	orphans, ok := reader.handler.auditKeys.(interface{ HasOrphanedAuditKey() bool })
	return ok && orphans.HasOrphanedAuditKey()
}

// openRawSealed opens a part sealed to the raw key with its own part key.
func (reader *auditReader) openRawSealed(blob storage.AuditBlob) ([]byte, contract.AuditWithheldReason, error) {
	var partKey []byte
	switch {
	case reader.lease != nil && reader.lease.opener != nil:
		key, err := reader.lease.opener.OpenBlobKey(blob)
		if err != nil {
			// Sealed to a key the grant does not hold, or the grant
			// ended during this read.
			return nil, privacyWithheldReason(reader.record, blob), nil
		}
		defer clear(key)
		partKey = key
	case reader.lease != nil:
		partKey = reader.lease.keys[blob.Direction]
		if len(partKey) == 0 {
			// Captured after the approval; the grant does not cover it.
			return nil, privacyWithheldReason(reader.record, blob), nil
		}
	case reader.opener != nil:
		key, err := reader.opener.OpenBlobKey(blob)
		if err != nil {
			return nil, "", err
		}
		defer clear(key)
		partKey = key
	default:
		// Raw sealing was reset since this part was stored.
		return nil, contract.AuditWithheldRawLocked, nil
	}
	plaintext, err := storage.OpenAuditBlob(partKey, blob.Nonce, blob.Ciphertext)
	if err != nil && reader.lease != nil {
		// Recaptured after the approval with a new part key.
		return nil, privacyWithheldReason(reader.record, blob), nil
	}
	if err != nil || blob.Layout != storage.AuditLayoutRecipe {
		return plaintext, "", err
	}
	// The part key opened a recipe; what it refers to is content the
	// session already shares, which opens with the audit key.
	defer clear(plaintext)
	key, err := reader.key()
	if err != nil {
		return nil, "", err
	}
	assembled, err := storage.AssembleAuditRecipe(plaintext, key, blob.Chunks)
	if errors.Is(err, storage.ErrAuditDecrypt) && reader.auditKeyOrphaned() {
		err = errAuditKeyMissing
	}
	return assembled, "", err
}

func (reader *auditReader) part(blob storage.AuditBlob) (*contract.AuditContentPart, error) {
	plaintext, withheld, err := reader.open(blob)
	if err != nil {
		return nil, err
	}
	part := &contract.AuditContentPart{
		MediaType:     blob.MediaType,
		Truncated:     blob.Truncated,
		CapturedBytes: blob.CapturedBytes,
	}
	if withheld != "" {
		// A part that was never kept cannot be asked for.
		available := withheld != contract.AuditWithheldRawNotKept && reader.agentRawAvailable()
		part.Withheld, part.Reason, part.RawAvailable = true, withheld, &available
		return part, nil
	}
	// Decrypted content is returned as UTF-8 when valid; otherwise base64 so
	// the JSON string stays well-formed. media_type is left unchanged either way.
	part.Content = string(plaintext)
	if !utf8.Valid(plaintext) {
		part.Content = base64.StdEncoding.EncodeToString(plaintext)
	}
	clear(plaintext)
	part.Exposure = contract.AuditPartExposureRaw
	if blob.Exposure == storage.AuditExposureShareable {
		part.Exposure = contract.AuditPartExposureShareable
	}
	return part, nil
}

// agentRawAvailable reports whether asking for the raw part can succeed:
// a raw password is set and the settings switch allows agent requests. It
// is advisory, so a state it cannot read reports false.
func (reader *auditReader) agentRawAvailable() bool {
	if reader.rawAvailable == nil {
		ctx := reader.request.Context()
		status, err := reader.handler.rawVaultStatus(ctx)
		available := err == nil && status.PasswordSet
		if available {
			enabled, err := reader.handler.agentRawAccessEnabled(ctx)
			available = err == nil && enabled
		}
		reader.rawAvailable = &available
	}
	return *reader.rawAvailable
}

func (reader *auditReader) writeOpenError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAuditKeyMissing):
		writeError(writer, http.StatusConflict, "audit_key_missing", "audit content cannot be decrypted")
	case reader.keyErr != nil && err == reader.keyErr:
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "audit key storage is unavailable")
	default:
		writeError(writer, http.StatusConflict, "audit_decrypt_failed", "audit content cannot be decrypted")
	}
}

// privacyWithheldReason explains a part withheld from the shareable view.
func privacyWithheldReason(record contract.RequestRecord, blob storage.AuditBlob) contract.AuditWithheldReason {
	if blob.Exposure == storage.AuditExposurePending {
		return contract.AuditWithheldPrivacyPending
	}
	switch blob.Direction {
	case storage.AuditDirectionResponse:
		return contract.AuditWithheldPrivacyRestored
	case storage.AuditDirectionRequest:
		if record.PrivacyDecision != nil {
			switch *record.PrivacyDecision {
			case contract.PrivacyDecisionRedact:
				return contract.AuditWithheldPrivacyRedacted
			case contract.PrivacyDecisionBlock:
				return contract.AuditWithheldPrivacyBlocked
			case contract.PrivacyDecisionFailOpen:
				return contract.AuditWithheldPrivacyFailOpen
			case contract.PrivacyDecisionTrusted:
				return contract.AuditWithheldPrivacyTrusted
			}
		}
	case storage.AuditDirectionUpstreamRequest:
		// Only a request sent to a trusted provider withholds its upstream
		// copy.
		return contract.AuditWithheldPrivacyTrusted
	}
	return contract.AuditWithheldPrivacyUnknown
}

// rawSealed reports whether a part is sealed to the raw key rather than
// the audit key.
func rawSealed(blob storage.AuditBlob) bool { return blob.Sealing == storage.AuditSealingRawV1 }

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
	return parseRequestRecordQuery(request.URL.Query())
}

func parseRequestRecordQuery(query url.Values) (storage.RequestRecordListOptions, error) {
	for name, values := range query {
		switch name {
		case "limit", "cursor", "from", "to", "protocol", "service_id", "status", "q":
			if len(values) != 1 {
				return storage.RequestRecordListOptions{}, fmt.Errorf("query parameter must occur once")
			}
		case "local_access_token_id":
			if len(values) > maxLocalAccessTokenFilters {
				return storage.RequestRecordListOptions{}, fmt.Errorf("too many local_access_token_id filters")
			}
		case "model":
			if len(values) > maxModelFilters {
				return storage.RequestRecordListOptions{}, fmt.Errorf("too many model filters")
			}
		default:
			return storage.RequestRecordListOptions{}, fmt.Errorf("unknown query parameter")
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
	if values, ok := query["local_access_token_id"]; ok {
		options.LocalAccessTokenIDs = make([]contract.AccessTokenID, 0, len(values))
		seen := make(map[contract.AccessTokenID]struct{}, len(values))
		for _, value := range values {
			tokenID := contract.AccessTokenID(value)
			if err := tokenID.Validate(); err != nil {
				return options, fmt.Errorf("invalid local_access_token_id filter")
			}
			if _, exists := seen[tokenID]; exists {
				return options, fmt.Errorf("duplicate local_access_token_id filter")
			}
			seen[tokenID] = struct{}{}
			options.LocalAccessTokenIDs = append(options.LocalAccessTokenIDs, tokenID)
		}
	}
	if values, ok := query["model"]; ok {
		options.Models = make([]string, 0, len(values))
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			if value == "" || utf8.RuneCountInString(value) > maxModelFilterRunes || strings.ContainsFunc(value, unicode.IsControl) {
				return options, fmt.Errorf("invalid model filter")
			}
			if _, exists := seen[value]; exists {
				return options, fmt.Errorf("duplicate model filter")
			}
			seen[value] = struct{}{}
			options.Models = append(options.Models, value)
		}
	}
	if value := query.Get("status"); value != "" {
		status := contract.RequestStatus(value)
		if !status.Valid() {
			return options, fmt.Errorf("invalid status filter")
		}
		options.Status = &status
	}
	if values, ok := query["q"]; ok {
		text := strings.TrimSpace(values[0])
		if text == "" || utf8.RuneCountInString(text) > maxRequestSearchRunes || strings.ContainsFunc(text, unicode.IsControl) {
			return options, fmt.Errorf("invalid q filter")
		}
		options.Query = text
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
