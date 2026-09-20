package controlapi

import (
	"net/http"
	"net/url"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const AccessTokenUsagePath = "/control/v1/access-token-usage"

type accessTokenUsageResponse struct {
	Items []storage.AccessTokenUsage `json:"items"`
}

func (handler *Handler) listAccessTokenUsage(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", "invalid query encoding")
		return
	}
	todayFrom, err := time.Parse(time.RFC3339Nano, query.Get("today_from"))
	if len(query) != 1 || len(query["today_from"]) != 1 || err != nil || todayFrom.IsZero() || todayFrom.Nanosecond() != 0 {
		writeError(writer, http.StatusBadRequest, "invalid_query", "today_from must be a single whole-second RFC3339 timestamp")
		return
	}
	items, err := handler.requestRecords.ListAccessTokenUsage(request.Context(), todayFrom)
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, accessTokenUsageResponse{Items: items})
}
