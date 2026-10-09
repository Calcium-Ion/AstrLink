//go:build !webui

package console

import (
	"net/http"
	"testing"
)

func TestConsoleWithoutTheWebuiTagServesNoAssets(t *testing.T) {
	if assets != nil {
		t.Fatal("assets are embedded without the webui tag")
	}
	handler := newFixture(t, testPassword).console
	for _, path := range []string{"/", "/index.html"} {
		response := call{method: http.MethodGet, path: path}.do(handler)
		if response.Code != http.StatusNotFound || errorCode(t, response) != "web_console_unavailable" {
			t.Fatalf("GET %s = %d %s", path, response.Code, response.Body.String())
		}
	}
}
