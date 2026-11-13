package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/testutil/sqliteschema"
)

const testControlToken = "test-control-token-0123456789"

// API tests exercise store behavior on the current schema. Key/token bootstrap
// still runs per store; existing files stay intact for restart and unlock tests.
func openControlTestStore(t *testing.T, ctx context.Context, path string, options ...sqlite.Option) (*sqlite.Store, error) {
	t.Helper()
	sqliteschema.Seed(t, path)
	return sqlite.Open(ctx, path, options...)
}

func controlRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	contentType string,
	body string,
	ifMatch string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	return response
}

func newRouteHandler(t *testing.T) (*sqlite.Store, *Handler) {
	t.Helper()
	path := t.TempDir() + "/store.sqlite"
	store, err := openControlTestStore(t, context.Background(), path)
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{
			ServiceStore: store,
			ControlToken: testControlToken,
		},
	)
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	return store, handler
}

// approvedRequest sends a request as the desktop's confirmation window does,
// with the user's confirmation of what it grants a plugin.
func approvedRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	contentType string,
	body string,
	ifMatch string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	request.Header.Set(contract.PluginApprovalHeader, "confirmed")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
