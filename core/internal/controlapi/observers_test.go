package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func TestObserversAPI(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "observers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{ServiceStore: store, RequestRecords: store, ControlToken: testControlToken})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	handler.observers.now = func() time.Time { return now }

	read := func() ObserversResponse {
		response := accessTokenRequest(t, handler, http.MethodGet, ObserversPath, "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("observers status=%d body=%s", response.Code, response.Body.String())
		}
		var parsed ObserversResponse
		if err := json.Unmarshal(response.Body.Bytes(), &parsed); err != nil {
			t.Fatal(err)
		}
		return parsed
	}

	// The desktop shell reads the usage summary: not an observer.
	query := "?from=2026-09-21T00:00:00Z&to=2026-09-22T00:00:00Z&time_zone=UTC&bucket=day"
	if response := accessTokenRequest(t, handler, http.MethodGet, UsageSummaryPath+query, "", ""); response.Code != http.StatusOK {
		t.Fatalf("shell request status=%d", response.Code)
	}
	if got := read(); got.LastSeenAt != nil || got.Requests != 0 || got.Client != "" {
		t.Fatalf("shell request was counted as an observer: %+v", got)
	}

	// An unauthenticated request claiming to be the MCP bridge is not counted.
	unauthorized := httptest.NewRequest(http.MethodGet, UsageSummaryPath+query, nil)
	unauthorized.Header.Set("User-Agent", "astrlink-mcp/1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, unauthorized)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", recorder.Code)
	}
	if got := read(); got.Requests != 0 {
		t.Fatalf("unauthenticated observer was counted: %+v", got)
	}

	// The MCP bridge over the loopback fallback announces itself.
	viaToken := httptest.NewRequest(http.MethodGet, UsageSummaryPath+query, nil)
	viaToken.Header.Set("Authorization", "Bearer "+testControlToken)
	viaToken.Header.Set("User-Agent", "astrlink-mcp/1")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, viaToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("mcp token status=%d", recorder.Code)
	}
	got := read()
	if got.LastSeenAt == nil || !got.LastSeenAt.Equal(now) || got.Requests != 1 || got.Client != "astrlink-mcp" {
		t.Fatalf("mcp request not recorded: %+v", got)
	}

	// The production path is the local control socket, without a token.
	now = now.Add(3 * time.Second)
	viaSocket := httptest.NewRequest(http.MethodGet, UsageSummaryPath+query, nil)
	viaSocket = viaSocket.WithContext(ContextWithLocalSocketAuth(viaSocket.Context()))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, viaSocket)
	if recorder.Code != http.StatusOK {
		t.Fatalf("socket status=%d", recorder.Code)
	}
	got = read()
	if got.LastSeenAt == nil || !got.LastSeenAt.Equal(now) || got.Requests != 2 {
		t.Fatalf("socket request not recorded: %+v", got)
	}

	// Reading the observer state is never itself an observation.
	if again := read(); again.Requests != 2 {
		t.Fatalf("observers read counted itself: %+v", again)
	}
	if response := accessTokenRequest(t, handler, http.MethodPost, ObserversPath, "", ""); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", response.Code)
	}
}
