package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestSubscriptionAuthorizationUsesOfficialClientByDefault(t *testing.T) {
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatalf("sqlite.Open() = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	preferred, fallback := controlAPITestPortPair(t)
	manager, err := subscription.NewManager(
		subscription.StorageAccountStore{Store: store},
		accountauth.NewMemoryCredentialStore(),
		accountauth.OAuthConfig{
			Now:           func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) },
			PreferredPort: preferred,
			FallbackPort:  fallback,
		},
	)
	if err != nil {
		t.Fatalf("NewManager() = %v", err)
	}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore:  store,
		Subscriptions: manager,
		ControlToken:  testControlToken,
	})
	if err != nil {
		t.Fatalf("NewWithDependencies() = %v", err)
	}

	create := httptest.NewRequest(http.MethodPost, ServicesPath, strings.NewReader(`{"name":"Codex","kind":"codex_subscription"}`))
	create.Header.Set("Authorization", "Bearer "+testControlToken)
	create.Header.Set("Content-Type", "application/json")
	createRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createRecorder, create)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createRecorder.Code, createRecorder.Body.String())
	}
	var created contract.Service
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, ServicesPath+"/"+string(created.ID)+"/authorization", nil)
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	defer cancelServiceAuthorization(handler, created.ID)
	body := recorder.Body.String()
	var session contract.AuthorizationSession
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.Flow != contract.AuthorizationFlowBrowser ||
		!strings.Contains(session.AuthorizationURL, accountauth.DefaultCodexOAuthClientID) {
		t.Fatalf("session = %#v", session)
	}
	for _, secret := range []string{"access_token", "refresh_token", "device_auth_id", "code_verifier"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaked %q: %s", secret, body)
		}
	}

	listReq := httptest.NewRequest(http.MethodGet, ServicesPath, nil)
	listReq.Header.Set("Authorization", "Bearer "+testControlToken)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRec.Code, listRec.Body.String())
	}
	var page struct {
		Items []contract.Service `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Kind != contract.ServiceKindCodexSubscription {
		t.Fatalf("items = %#v", page.Items)
	}
	if page.Items[0].Subscription == nil || page.Items[0].Subscription.AuthorizationBoundary != "" {
		t.Fatalf("unexpected authorization_boundary: %#v", page.Items[0].Subscription)
	}
	if strings.Contains(listRec.Body.String(), "Bearer ") {
		t.Fatalf("list leaked bearer material: %s", listRec.Body.String())
	}
}

func TestSubscriptionAuthorizationAcceptsExplicitFlowsAndRejectsInvalidFlow(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"device_auth_id": "control-device-secret",
				"user_code":      "CTRL-CODE",
				"interval":       "60",
			})
		case "/api/accounts/deviceauth/token":
			writer.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer issuer.Close()

	store, err := sqlite.Open(context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	preferred, fallback := controlAPITestPortPair(t)
	manager, err := subscription.NewManager(
		subscription.StorageAccountStore{Store: store},
		accountauth.NewMemoryCredentialStore(),
		accountauth.OAuthConfig{
			Issuer:        issuer.URL,
			HTTPClient:    issuer.Client(),
			PreferredPort: preferred,
			FallbackPort:  fallback,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{
			ServiceStore:  store,
			Subscriptions: manager,
			ControlToken:  testControlToken,
			NewServiceID: func() (contract.ServiceID, error) {
				return "service_device_control", nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := createServiceForTest(t, handler, `{"name":"Device account","kind":"codex_subscription"}`)

	device := serviceRequestForTest(
		t,
		handler,
		http.MethodPost,
		ServicesPath+"/"+string(service.ID)+"/authorization",
		"application/json",
		`{"flow":"device_code"}`,
		"",
	)
	if device.Code != http.StatusAccepted {
		t.Fatalf("device status=%d body=%s", device.Code, device.Body.String())
	}
	var session contract.AuthorizationSession
	decode(t, device, &session)
	if session.Flow != contract.AuthorizationFlowDeviceCode ||
		session.DeviceCode == nil ||
		session.DeviceCode.UserCode != "CTRL-CODE" {
		t.Fatalf("device session=%#v", session)
	}
	for _, secret := range []string{"control-device-secret", "device_auth_id", "code_verifier"} {
		if strings.Contains(device.Body.String(), secret) {
			t.Fatalf("device response leaked %q: %s", secret, device.Body.String())
		}
	}
	cancelServiceAuthorization(handler, service.ID)

	browser := serviceRequestForTest(
		t,
		handler,
		http.MethodPost,
		ServicesPath+"/"+string(service.ID)+"/authorization",
		"application/json",
		`{"flow":"browser"}`,
		"",
	)
	if browser.Code != http.StatusAccepted {
		t.Fatalf("browser status=%d body=%s", browser.Code, browser.Body.String())
	}
	decode(t, browser, &session)
	if session.Flow != contract.AuthorizationFlowBrowser {
		t.Fatalf("browser session=%#v", session)
	}
	cancelServiceAuthorization(handler, service.ID)

	invalid := serviceRequestForTest(
		t,
		handler,
		http.MethodPost,
		ServicesPath+"/"+string(service.ID)+"/authorization",
		"application/json",
		`{"flow":"magic"}`,
		"",
	)
	if invalid.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(invalid.Body.String(), "invalid_authorization_flow") {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	missing := serviceRequestForTest(
		t,
		handler,
		http.MethodPost,
		ServicesPath+"/"+string(service.ID)+"/authorization",
		"application/json",
		`{}`,
		"",
	)
	if missing.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(missing.Body.String(), "invalid_authorization_flow") {
		t.Fatalf("missing flow status=%d body=%s", missing.Code, missing.Body.String())
	}
}
