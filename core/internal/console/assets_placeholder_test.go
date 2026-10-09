package console

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

func TestConsoleCleanCheckoutPlaceholderIsOnlyAnIndexFallback(t *testing.T) {
	handler := newFixture(t, testPassword).console
	handler.assets = fstest.MapFS{
		"placeholder.html": &fstest.MapFile{Data: []byte("<!doctype html><title>AstrLink placeholder</title>")},
	}
	for _, path := range []string{"/", "/index.html"} {
		response := (call{method: http.MethodGet, path: path}).do(handler)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "AstrLink placeholder") {
			t.Fatalf("GET %s = %d %s", path, response.Code, response.Body.String())
		}
	}
	if response := (call{method: http.MethodGet, path: "/settings"}).do(handler); response.Code != http.StatusNotFound {
		t.Fatalf("missing routes must not return the placeholder: %d", response.Code)
	}
	handler.assets.(fstest.MapFS)["index.html"] = &fstest.MapFile{Data: []byte("<!doctype html><title>Built console</title>")}
	response := (call{method: http.MethodGet, path: "/"}).do(handler)
	if !strings.Contains(response.Body.String(), "Built console") {
		t.Fatal("the staged build must take precedence over the placeholder")
	}
}
