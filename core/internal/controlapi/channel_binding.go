package controlapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func (handler *Handler) sessionChannelBinding(writer http.ResponseWriter, request *http.Request) {
	id := contract.SessionID(strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, RequestSessionsPath+"/"), "/channel-bindings"))
	if id.Validate() != nil {
		writeError(writer, http.StatusBadRequest, "invalid_session_id", "session_id must use its canonical form")
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodDelete {
		writer.Header().Set("Allow", "GET, DELETE")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and DELETE are allowed")
		return
	}
	var before int64
	if raw := request.URL.Query().Get("before"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 || len(request.URL.Query()["before"]) != 1 || request.Method != http.MethodGet {
			writeError(writer, http.StatusBadRequest, "invalid_cursor", "before must be a positive event id on GET requests")
			return
		}
		before = value
	}
	store, ok := handler.requestRecords.(storage.ChannelBindingStore)
	if !ok {
		writeError(writer, http.StatusServiceUnavailable, "channel_bindings_unavailable", "API provider bindings are unavailable")
		return
	}
	if _, err := handler.requestRecords.GetRequestSession(request.Context(), string(id)); err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	if request.Method == http.MethodDelete {
		if err := store.ReleaseChannelBindings(request.Context(), id); err != nil {
			handler.writeStoreError(writer, err)
			return
		}
	}
	audit, err := store.GetChannelBindingAudit(request.Context(), id, before)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, audit)
}
