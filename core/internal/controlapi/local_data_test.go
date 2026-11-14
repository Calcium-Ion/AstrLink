package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

type fakeLocalData struct {
	status contract.LocalDataStatus
	err    error
}

func (fake fakeLocalData) LocalDataStatus(context.Context) (contract.LocalDataStatus, error) {
	return fake.status, fake.err
}

func TestLocalDataStatusReportsCountsOnly(t *testing.T) {
	store, err := openControlTestStore(t, context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fake := &fakeLocalData{status: contract.LocalDataStatus{UnreadableCredentials: 2, UnreadableAccessTokens: 1, AuditKeyMissing: true}}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore: store, LocalData: fake, ControlToken: testControlToken, ObserverToken: testObserverToken,
	})
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	get := func(target string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer "+testObserverToken)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	response := get(LocalDataPath)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store, no-transform" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"unreadable_credentials": float64(2), "unreadable_access_tokens": float64(1), "audit_key_missing": true}
	if len(body) != len(want) {
		t.Fatalf("body = %v", body)
	}
	for key, value := range want {
		if body[key] != value {
			t.Fatalf("%s = %v, want %v", key, body[key], value)
		}
	}

	if response := get(LocalDataPath + "?verbose=1"); response.Code != http.StatusBadRequest {
		t.Fatalf("query status=%d", response.Code)
	}
	fake.err = errors.New("sqlite: disk I/O error at /private/path")
	response = get(LocalDataPath)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "/private/path") {
		t.Fatalf("storage error status=%d body=%s", response.Code, response.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, LocalDataPath, nil)
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", recorder.Code)
	}
}
