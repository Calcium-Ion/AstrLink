package controlapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	RequestActivityPath = "/control/v1/request-activity"

	// maxRequestActivityLookback bounds how far back a poll may ask, so a
	// once-a-second reader cannot turn into a history scan.
	maxRequestActivityLookback = time.Hour
)

func (handler *Handler) getRequestActivity(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	since, err := parseRequestActivitySince(request, time.Now())
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	calls, err := handler.requestRecords.ListRequestActivity(request.Context(), since)
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	// Read after the calls, so every call listed started before now.
	writeJSON(writer, http.StatusOK, contract.RequestActivity{Now: time.Now().UTC(), Calls: calls})
}

func parseRequestActivitySince(request *http.Request, now time.Time) (time.Time, error) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "since" {
			return time.Time{}, fmt.Errorf("unsupported query parameter %q", key)
		}
		if len(values) != 1 {
			return time.Time{}, fmt.Errorf("query parameter must occur once")
		}
	}
	value := query.Get("since")
	if value == "" {
		return time.Time{}, fmt.Errorf("since is required")
	}
	since, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid since timestamp")
	}
	if since.Before(now.Add(-maxRequestActivityLookback)) {
		return time.Time{}, fmt.Errorf("since must be within the last hour")
	}
	return since.UTC(), nil
}
