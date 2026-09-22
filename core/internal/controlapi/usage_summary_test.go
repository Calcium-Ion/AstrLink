package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func TestUsageSummaryAPI(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{ServiceStore: store, RequestRecords: store, ControlToken: testControlToken})
	if err != nil {
		t.Fatal(err)
	}
	query := "?from=2026-09-18T15:00:00Z&to=2026-09-19T15:00:00Z&time_zone=Asia%2FTokyo&bucket=hour"
	response := accessTokenRequest(t, handler, http.MethodGet, UsageSummaryPath+query, "", "")
	var summary storage.UsageSummary
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &summary) != nil || summary.ByDay == nil || summary.ByModel == nil {
		t.Fatalf("empty status=%d body=%s", response.Code, response.Body.String())
	}
	for _, valid := range []string{
		"?from=2025-09-20T00:00:00Z&to=2026-09-20T00:00:00Z&time_zone=UTC&bucket=day",
		"?from=2026-06-22T00:00:00Z&to=2026-09-20T00:00:00Z&time_zone=Asia%2FShanghai&bucket=day",
	} {
		response := accessTokenRequest(t, handler, http.MethodGet, UsageSummaryPath+valid, "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("long range=%s status=%d body=%s", valid, response.Code, response.Body.String())
		}
	}
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, UsageSummaryPath+query, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized=%d", unauthorized.Code)
	}
	for _, bad := range []string{"", query + "&limit=1", query + "&bucket=day", "?from=%ZZ", "?from=2026-09-19T00:00:00.001Z&to=2026-09-20T00:00:00Z&time_zone=UTC&bucket=day", "?from=2026-09-19T00:00:00Z&to=2026-09-18T00:00:00Z&time_zone=UTC&bucket=day", "?from=2025-01-01T00:00:00Z&to=2026-09-20T00:00:00Z&time_zone=UTC&bucket=day", "?from=2026-09-19T00:00:00Z&to=2026-09-20T00:00:00Z&time_zone=Missing&bucket=day"} {
		response := accessTokenRequest(t, handler, http.MethodGet, UsageSummaryPath+bad, "", "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("query=%s status=%d", bad, response.Code)
		}
	}
	response = accessTokenRequest(t, handler, http.MethodPost, UsageSummaryPath+query, "", "")
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", response.Code)
	}
}
