package controlapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

func TestBuiltinToolConfigurationAndCredentials(t *testing.T) {
	store, handler := newRouteHandler(t)
	path := BuiltinToolsPath + "web_search/credential"
	w := controlRequest(t, handler, http.MethodPut, path, "application/json", `{"secret":"private-test-key"}`, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-test-key") {
		t.Fatal(w.Code, w.Body.String())
	}
	value, err := store.Get(context.Background(), secretstore.Ref("local://builtin-tool/web_search"))
	if err != nil || string(value) != "private-test-key" {
		t.Fatal(err)
	}
	clear(value)
	config := `{"builtin_tools":{"web_search":{"enabled":true,"backend":"external","base_url":"https://search.example"},"image_generation":{"enabled":false,"backend":"upstream"}}}`
	w = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", config, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
	if strings.Contains(w.Body.String(), "private-test-key") || !strings.Contains(w.Body.String(), "builtin_tools") {
		t.Fatal(w.Body.String())
	}
	w = controlRequest(t, handler, http.MethodDelete, path, "", "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = controlRequest(t, handler, http.MethodGet, path, "", "", "")
	if !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatal(w.Body.String())
	}
	for _, input := range []string{`{"builtin_tools":{"web_search":{"enabled":true,"backend":"external"},"image_generation":{"enabled":false,"backend":""}}}`, `{"builtin_tools":{"web_search":{"enabled":false,"backend":"upstream","secret":"no"},"image_generation":{"enabled":false,"backend":""}}}`} {
		w = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", input, "")
		if w.Code != 422 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
