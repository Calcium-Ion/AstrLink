package controlapi

import (
	"net/http"
	"net/url"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const UsageSummaryPath = "/control/v1/usage-summary"

func (handler *Handler) getUsageSummary(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(query) != 4 || len(query["from"]) != 1 || len(query["to"]) != 1 || len(query["time_zone"]) != 1 || len(query["bucket"]) != 1 {
		writeError(writer, http.StatusBadRequest, "invalid_query", "from, to, time_zone and bucket are required exactly once")
		return
	}
	from, fromErr := time.Parse(time.RFC3339Nano, query.Get("from"))
	to, toErr := time.Parse(time.RFC3339Nano, query.Get("to"))
	options := storage.UsageSummaryOptions{From: from, To: to, TimeZone: query.Get("time_zone"), Bucket: query.Get("bucket")}
	if _, err := options.Validate(); err != nil || fromErr != nil || toErr != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", "invalid usage range, time zone or bucket")
		return
	}
	summary, err := handler.requestRecords.GetUsageSummary(request.Context(), options)
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, summary)
}
