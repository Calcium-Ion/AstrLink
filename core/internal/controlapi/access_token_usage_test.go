package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func TestAccessTokenUsageAPI(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore: store, RequestRecords: store, ControlToken: testControlToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := AccessTokenUsagePath + "?today_from=2026-09-19T00:00:00%2B08:00"
	response := accessTokenRequest(t, handler, http.MethodGet, path, "", "")
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"items":[]}` {
		t.Fatalf("empty status=%d body=%s", response.Code, response.Body.String())
	}
	started := time.Date(2026, 9, 18, 16, 0, 0, 1, time.UTC)
	completed := started.Add(time.Second)
	token := contract.AccessTokenID("token_usage")
	latency, first, cache := 1000, 500, 20
	if err := store.InsertRequestRecord(context.Background(), contract.RequestRecord{
		ID: "request_usage", StartedAt: started, CompletedAt: &completed,
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
		LocalAccessTokenID: &token, Usage: &contract.Usage{InputTokens: 40, CacheReadTokens: &cache, OutputTokens: 2, TotalTokens: 42}, Audit: contract.NotCapturedAuditSummary(),
		Streaming: true, LatencyMs: &latency, FirstTokenMs: &first,
	}); err != nil {
		t.Fatal(err)
	}
	response = accessTokenRequest(t, handler, http.MethodGet, path, "", "")
	var result accessTokenUsageResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(result.Items) != 1 || result.Items[0].TokenID != token || result.Items[0].TodayTokens != 42 || result.Items[0].TotalTokens != 42 || result.Items[0].TodayBilling.AmountUSD != "0.000000000" || result.Items[0].TotalBilling.AmountUSD != "0.000000000" {
		t.Fatalf("usage status=%d body=%s", response.Code, response.Body.String())
	}
	if stats := result.Items[0].TodayPerformance; stats.CacheHitRate == nil || *stats.CacheHitRate != 0.5 || stats.OutputTokensPerSecond == nil || *stats.OutputTokensPerSecond != 4 || stats.SpeedSamples != 1 || stats.CacheSamples != 1 {
		t.Fatalf("performance missing from response: %s", response.Body.String())
	}
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, path, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized=%d", unauthorized.Code)
	}
	for _, query := range []string{"", "?today_from=invalid", "?today_from=2026-09-19T00:00:00Z&limit=1", "?today_from=2026-09-19T00:00:00Z&today_from=2026-09-19T00:00:00Z", "?today_from=2026-09-19T00:00:00.001Z", "?today_from=%ZZ"} {
		response := accessTokenRequest(t, handler, http.MethodGet, AccessTokenUsagePath+query, "", "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("query=%q status=%d body=%s", query, response.Code, response.Body.String())
		}
	}
	response = accessTokenRequest(t, handler, http.MethodPost, path, "", "")
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method status=%d", response.Code)
	}
}
