package controlapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestServiceStatisticsErrorResponses(t *testing.T) {
	store, handler := newServiceHandler(t, "service_statistics")
	service := createServiceForTest(t, handler, `{"name":"statistics","kind":"newapi","http":{"base_url":"https://gateway.example/v1","auth":{"scheme":"none"}},"capabilities":[{"protocol":"openai.chat","mode":"native","streaming":true}]}`)
	path := ServicesPath + "/" + string(service.ID) + "/statistics"
	validRange := "?from=2026-09-01T00:00:00Z&to=2026-09-25T00:00:00Z"
	for _, tc := range []struct {
		name, path, code string
		status           int
	}{
		{"valid", path + validRange, "", http.StatusOK},
		{"missing service", ServicesPath + "/service_missing/statistics" + validRange, "not_found", http.StatusNotFound},
		{"malformed", path + "?from=bad&to=bad", "invalid_range", http.StatusBadRequest},
		{"too long", path + "?from=2026-08-01T00:00:00Z&to=2026-09-25T00:00:00Z", "invalid_range", http.StatusBadRequest},
		{"reversed", path + "?from=2026-09-25T00:00:00Z&to=2026-09-01T00:00:00Z", "invalid_range", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := serviceRequestForTest(t, handler, http.MethodGet, tc.path, "", "", "")
			if response.Code != tc.status || (tc.code != "" && !strings.Contains(response.Body.String(), `"code":"`+tc.code+`"`)) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	response := serviceRequestForTest(t, handler, http.MethodGet, path+validRange, "", "", "")
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"storage_unavailable"`) || strings.Contains(response.Body.String(), "database is closed") {
		t.Fatalf("storage failure status=%d body=%s", response.Code, response.Body.String())
	}
}
