package controlapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestReadOnlyControlContract(t *testing.T) {
	version := contract.DefaultVersionResponse("0.1.0-test", "abc1234")
	handler := New(version)

	t.Run("health", func(t *testing.T) {
		response := serve(t, handler, http.MethodGet, HealthPath)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.Code)
		}
		var body contract.HealthResponse
		decode(t, response, &body)
		if body.Status != "ok" {
			t.Fatalf("health status = %q", body.Status)
		}
	})

	t.Run("version", func(t *testing.T) {
		response := serve(t, handler, http.MethodGet, VersionPath)
		var body contract.VersionResponse
		decode(t, response, &body)
		if body != version {
			t.Fatalf("version = %#v, want %#v", body, version)
		}
	})

	t.Run("capabilities", func(t *testing.T) {
		response := serve(t, handler, http.MethodGet, CapabilitiesPath)
		var body contract.CapabilitiesResponse
		decode(t, response, &body)
		if len(body.Protocols) != 8 || len(body.PlanTypes) != 3 {
			t.Fatalf("protocols=%d plans=%d, want 8 and 3", len(body.Protocols), len(body.PlanTypes))
		}
		if body.ConversionEngine.Name != "relaykit" || body.ConversionEngine.Version != nil || body.ConversionEngine.Available {
			t.Fatalf("unexpected conversion engine: %#v", body.ConversionEngine)
		}
		if body.ConversionEngine.Edges == nil || len(body.ConversionEngine.Edges) != 0 {
			t.Fatalf("edges = %#v, want []", body.ConversionEngine.Edges)
		}
	})
}

func TestControlContractRejectsMutationAndUnknownPaths(t *testing.T) {
	handler := New(contract.DefaultVersionResponse("", ""))
	response := serve(t, handler, http.MethodPost, HealthPath)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST status=%d allow=%q", response.Code, response.Header().Get("Allow"))
	}

	response = serve(t, handler, http.MethodGet, "/control/v1/unknown")
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown path status = %d", response.Code)
	}
	var envelope errorEnvelope
	decode(t, response, &envelope)
	if envelope.Error.Code != "not_found" {
		t.Fatalf("error = %#v", envelope.Error)
	}
	if envelope.RequestID == "" || envelope.Error.Retryable || envelope.Error.Details == nil {
		t.Fatalf("error envelope does not match frozen contract: %#v", envelope)
	}
}

func serve(t *testing.T, handler http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	return response
}

func decode(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v; body=%q", err, response.Body.String())
	}
}
