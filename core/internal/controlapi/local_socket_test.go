package controlapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticatedAcceptsLocalSocketWithoutBearer(t *testing.T) {
	handler := &Handler{controlToken: []byte("control-token-16b")}
	protected := handler.authenticated(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/control/v1/requests", nil)
	request = request.WithContext(ContextWithLocalSocketAuth(request.Context()))
	recorder := httptest.NewRecorder()
	protected(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
}

func TestAuthenticatedRejectsHTTPWithoutBearer(t *testing.T) {
	handler := &Handler{controlToken: []byte("control-token-16b")}
	protected := handler.authenticated(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not run")
	})

	request := httptest.NewRequest(http.MethodGet, "/control/v1/requests", nil)
	recorder := httptest.NewRecorder()
	protected(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestAuthenticatedAcceptsMatchingBearer(t *testing.T) {
	token := "control-token-16b"
	handler := &Handler{controlToken: []byte(token)}
	protected := handler.authenticated(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/control/v1/requests", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	protected(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
}
