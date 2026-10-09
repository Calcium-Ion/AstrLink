//go:build webui

package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleServesTheEmbeddedPageAndRoutesToIt(t *testing.T) {
	handler := newFixture(t, testPassword).console
	for _, path := range []string{"/", "/index.html"} {
		response := call{method: http.MethodGet, path: path}.do(handler)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "AstrLink") ||
			!strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("GET %s = %d %q %s", path, response.Code, response.Header().Get("Content-Type"), response.Body.String())
		}
		if response.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("GET %s has no CSP", path)
		}
	}
	// No catch-all page route: the listener is shared with inference.
	for _, path := range []string{"/missing.js", "/../../go.mod", "/settings/routing"} {
		if response := (call{method: http.MethodGet, path: path}).do(handler); response.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d", path, response.Code)
		}
	}
	if !handler.Owns(httptest.NewRequest(http.MethodGet, "/index.html", nil)) {
		t.Fatal("the embedded page is not the console's")
	}
	if response := (call{method: http.MethodPost, path: "/", page: true}).do(handler); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / = %d", response.Code)
	}
}
