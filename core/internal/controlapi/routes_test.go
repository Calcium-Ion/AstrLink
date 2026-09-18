package controlapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestLegacyRoutingControlEndpointsAreRetired(t *testing.T) {
	_, handler := newRouteHandler(t)
	for _, path := range []string{RoutesPath, RoutesPath + "/route_old", RecoveryPathsPath, RecoveryPathsPath + "/path_old", RecoveryPathsPath + "/preview", AutoClassifierPath, AutoClassifierLocalProbePath, AutoClassifierClassifyPreviewPath} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
			response := controlRequest(t, handler, method, path, "application/json", `{}`, `"old"`)
			if response.Code != http.StatusGone || !strings.Contains(response.Body.String(), "routing_feature_retired") {
				t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
			}
		}
	}
	response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", `{"default_recovery_paths":{}}`, "")
	if response.Code != http.StatusGone {
		t.Fatalf("default paths: %d", response.Code)
	}
}
