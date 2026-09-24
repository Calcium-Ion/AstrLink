package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestUnifiedServicesSupportMultipleTargetedCodexAuthorizations(t *testing.T) {
	store, handler := newServiceHandler(t,
		"service_codex_one",
		"service_gateway_one",
		"service_codex_two",
	)

	first := createServiceForTest(t, handler, `{"name":"Codex personal","kind":"codex_subscription"}`)
	gateway := createServiceForTest(t, handler, `{
		"name":"new-api","kind":"newapi",
		"http":{
			"base_url":"https://gateway.example/v1",
			"auth":{"scheme":"bearer"},
			"credential":{"secret":"gateway-secret-value"}
		},
		"capabilities":[
			{"protocol":"openai.responses","mode":"delegated","streaming":true}
		]
	}`)
	second := createServiceForTest(t, handler, `{"name":"Codex work","kind":"codex_subscription"}`)

	if first.ID == second.ID || first.Kind != contract.ServiceKindCodexSubscription ||
		second.Kind != contract.ServiceKindCodexSubscription {
		t.Fatalf("codex services = %#v, %#v", first, second)
	}
	if gateway.Kind != contract.ServiceKindNewAPI || gateway.HTTP == nil {
		t.Fatalf("gateway service = %#v", gateway)
	}
	if strings.Contains(mustJSON(t, gateway), "gateway-secret-value") {
		t.Fatal("service response exposed HTTP credential")
	}
	secret, err := store.Get(context.Background(), secretstore.Ref(gateway.HTTP.CredentialRef))
	if err != nil || string(secret) != "gateway-secret-value" {
		t.Fatalf("stored gateway credential = %q, %v", secret, err)
	}
	clear(secret)

	firstSession := beginServiceAuthorization(t, handler, first.ID)
	secondSession := beginServiceAuthorization(t, handler, second.ID)
	t.Cleanup(func() {
		cancelServiceAuthorization(handler, first.ID)
		cancelServiceAuthorization(handler, second.ID)
	})
	if firstSession.ServiceID != first.ID || secondSession.ServiceID != second.ID {
		t.Fatalf("sessions target wrong services: %#v, %#v", firstSession, secondSession)
	}
	firstRedirect := authorizationRedirect(t, firstSession.AuthorizationURL)
	secondRedirect := authorizationRedirect(t, secondSession.AuthorizationURL)
	if firstRedirect == secondRedirect {
		t.Fatalf("parallel services reused callback listener %q", firstRedirect)
	}

	for _, session := range []contract.AuthorizationSession{firstSession, secondSession} {
		response := serviceRequestForTest(
			t, handler, http.MethodGet,
			ServicesPath+"/"+string(session.ServiceID)+"/authorization", "", "", "",
		)
		if response.Code != http.StatusOK {
			t.Fatalf("get authorization status=%d body=%s", response.Code, response.Body.String())
		}
		var current contract.AuthorizationSession
		decode(t, response, &current)
		if current.ID != session.ID || current.ServiceID != session.ServiceID {
			t.Fatalf("authorization lookup crossed services: %#v", current)
		}
	}

	response := serviceRequestForTest(t, handler, http.MethodPost,
		ServicesPath+"/"+string(gateway.ID)+"/authorization", "", "", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("HTTP service authorization status=%d body=%s", response.Code, response.Body.String())
	}

	response = serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"?limit=10", "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var page servicePageResponse
	decode(t, response, &page)
	if len(page.Items) != 3 {
		t.Fatalf("service count=%d items=%#v", len(page.Items), page.Items)
	}
}

func TestServicesAreTheOnlyServiceControlSurface(t *testing.T) {
	store, handler := newServiceHandler(t, "service_http_one")
	create := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath, "application/json", `{
		"name":"new-api primary",
		"kind":"newapi",
		"http":{
			"base_url":"https://gateway.example/v1",
			"auth":{"scheme":"bearer"},
			"credential":{"secret":"initial-secret-value"}
		},
		"capabilities":[
			{"protocol":"openai.responses","mode":"delegated","streaming":true}
		]
	}`, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	etag := create.Header().Get("ETag")
	if etag == "" ||
		create.Header().Get("Location") != ServicesPath+"/service_http_one" {
		t.Fatalf("create headers=%#v", create.Header())
	}
	if strings.Contains(create.Body.String(), "initial-secret-value") {
		t.Fatal("create response exposed credential")
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(
		unauthorized,
		httptest.NewRequest(http.MethodGet, ServicesPath, nil),
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}

	for _, retiredPath := range []string{
		"/control/v1/endpoints",
		"/control/v1/subscription-accounts",
	} {
		response := serviceRequestForTest(
			t,
			handler,
			http.MethodGet,
			retiredPath,
			"",
			"",
			"",
		)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", retiredPath, response.Code, response.Body.String())
		}
	}

	stale := serviceRequestForTest(
		t,
		handler,
		http.MethodPatch,
		ServicesPath+"/service_http_one",
		"application/merge-patch+json",
		`{"name":"new-api renamed"}`,
		`"stale"`,
	)
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale patch status=%d body=%s", stale.Code, stale.Body.String())
	}

	retained := serviceRequestForTest(
		t,
		handler,
		http.MethodPatch,
		ServicesPath+"/service_http_one",
		"application/merge-patch+json",
		`{
			"http":{
				"base_url":"https://replacement.example/v1",
				"auth":{"scheme":"google_api_key"}
			}
		}`,
		etag,
	)
	if retained.Code != http.StatusOK {
		t.Fatalf("credential-retaining patch status=%d body=%s", retained.Code, retained.Body.String())
	}
	retainedETag := retained.Header().Get("ETag")
	secret, err := store.Get(
		context.Background(),
		secretstore.Ref("local://service/service_http_one"),
	)
	if err != nil || string(secret) != "initial-secret-value" {
		t.Fatalf("retained credential=%q err=%v", secret, err)
	}
	clear(secret)

	updated := serviceRequestForTest(
		t,
		handler,
		http.MethodPatch,
		ServicesPath+"/service_http_one",
		"application/merge-patch+json",
		`{
			"name":"new-api renamed",
			"http":{"credential":{"secret":"replacement-secret-value"}}
		}`,
		retainedETag,
	)
	if updated.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", updated.Code, updated.Body.String())
	}
	updatedETag := updated.Header().Get("ETag")
	if updatedETag == "" || updatedETag == retainedETag {
		t.Fatalf("updated ETag=%q retained=%q", updatedETag, retainedETag)
	}
	if strings.Contains(updated.Body.String(), "replacement-secret-value") {
		t.Fatal("patch response exposed replacement credential")
	}
	secret, err = store.Get(
		context.Background(),
		secretstore.Ref("local://service/service_http_one"),
	)
	if err != nil || string(secret) != "replacement-secret-value" {
		t.Fatalf("replacement credential=%q err=%v", secret, err)
	}
	clear(secret)

	deleted := serviceRequestForTest(
		t,
		handler,
		http.MethodDelete,
		ServicesPath+"/service_http_one",
		"",
		"",
		updatedETag,
	)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	missing := serviceRequestForTest(
		t,
		handler,
		http.MethodGet,
		ServicesPath+"/service_http_one",
		"",
		"",
		"",
	)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("get deleted status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func newServiceHandler(t *testing.T, ids ...contract.ServiceID) (*sqlite.Store, *Handler) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatalf("sqlite.Open() = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	credentials := accountauth.NewMemoryCredentialStore()
	preferred, fallback := controlAPITestPortPair(t)
	manager, err := subscription.NewManager(
		subscription.StorageAccountStore{Store: store},
		credentials,
		accountauth.OAuthConfig{
			ClientID:      "astrlink_registered_test_client",
			Issuer:        "https://auth.example.test",
			PreferredPort: preferred,
			FallbackPort:  fallback,
		},
	)
	if err != nil {
		t.Fatalf("subscription.NewManager() = %v", err)
	}
	nextID := 0
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{
			ServiceStore:  store,
			Subscriptions: manager,
			ControlToken:  testControlToken,
			NewServiceID: func() (contract.ServiceID, error) {
				id := ids[nextID]
				nextID++
				return id, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("NewWithDependencies() = %v", err)
	}
	return store, handler
}

func TestCreateServiceRejectsLocalConversionWhileEngineUnavailable(t *testing.T) {
	_, handler := newServiceHandler(t, "service_convert")
	response := serviceRequestForTest(
		t, handler, http.MethodPost, ServicesPath, "application/json",
		`{"name":"gw","kind":"newapi","http":{"base_url":"https://gateway.example/v1","auth":{"scheme":"none"}},"capabilities":[{"protocol":"anthropic.messages","mode":"native","streaming":true,"convert_to":"openai.chat"}]}`,
		"",
	)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "local conversion is unavailable") {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestServiceRejectsDisabledModelsField(t *testing.T) {
	_, handler := newServiceHandler(t, "service_bad_models")
	response := serviceRequestForTest(
		t, handler, http.MethodPost, ServicesPath, "application/json",
		`{"name":"gw","kind":"newapi","disabled_models":["gpt-4o"],"http":{"base_url":"https://gateway.example/v1","auth":{"scheme":"none"}},"capabilities":[{"protocol":"openai.chat","mode":"native","streaming":true}]}`,
		"",
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func createServiceForTest(t *testing.T, handler http.Handler, body string) contract.Service {
	t.Helper()
	response := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath, "application/json", body, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var service contract.Service
	decode(t, response, &service)
	return service
}

func beginServiceAuthorization(
	t *testing.T,
	handler http.Handler,
	id contract.ServiceID,
) contract.AuthorizationSession {
	t.Helper()
	response := serviceRequestForTest(
		t, handler, http.MethodPost,
		ServicesPath+"/"+string(id)+"/authorization", "", "", "",
	)
	if response.Code != http.StatusAccepted {
		t.Fatalf("begin authorization status=%d body=%s", response.Code, response.Body.String())
	}
	var session contract.AuthorizationSession
	decode(t, response, &session)
	return session
}

func authorizationRedirect(t *testing.T, rawAuthorizationURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawAuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	redirect := parsed.Query().Get("redirect_uri")
	if redirect == "" {
		t.Fatalf("authorization URL omitted redirect_uri: %s", rawAuthorizationURL)
	}
	return redirect
}

func cancelServiceAuthorization(handler http.Handler, id contract.ServiceID) {
	request := httptest.NewRequest(http.MethodDelete, ServicesPath+"/"+string(id)+"/authorization", nil)
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	handler.ServeHTTP(httptest.NewRecorder(), request)
}

func serviceRequestForTest(
	t *testing.T,
	handler http.Handler,
	method, path, contentType, body, ifMatch string,
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
	return response
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func controlAPITestPortPair(t *testing.T) (int, int) {
	t.Helper()
	first := controlAPITestPort(t)
	second := controlAPITestPort(t)
	for first == second {
		second = controlAPITestPort(t)
	}
	return first, second
}

func controlAPITestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestPayAsYouGoServicesPersistAsHTTP(t *testing.T) {
	for _, kind := range []contract.ServiceKind{"deepseek", "qwen", "moonshot", "glm", "minimax", "doubao", "xai"} {
		t.Run(string(kind), func(t *testing.T) {
			store, handler := newServiceHandler(t, "service_api")
			service := createServiceForTest(t, handler, fmt.Sprintf(`{"name":"API","kind":%q,"models":["model-test"],"http":{"base_url":"https://api.example/v1","auth":{"scheme":"bearer"},"credential":{"secret":"api-secret"}},"capabilities":[{"protocol":"openai.chat","mode":"native","streaming":true}]}`, kind))
			if service.Kind != kind || service.HTTP == nil || service.Subscription != nil {
				t.Fatalf("wrong service: %#v", service)
			}
			response := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/"+string(service.ID), "", "", "")
			if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "api-secret") {
				t.Fatalf("get: %d %s", response.Code, response.Body.String())
			}
			var reloaded contract.Service
			decode(t, response, &reloaded)
			if reloaded.Kind != kind || reloaded.HTTP == nil {
				t.Fatalf("wrong saved service: %#v", reloaded)
			}
			secret, err := store.Get(context.Background(), secretstore.Ref(service.HTTP.CredentialRef))
			if err != nil || string(secret) != "api-secret" {
				t.Fatal("credential was not stored separately")
			}
			clear(secret)
		})
	}
}

func TestServiceResponsesWebSocketDefaultsAndPersistence(t *testing.T) {
	store, handler := newServiceHandler(t, "service_ws_default", "service_ws_disabled", "service_ws_http")
	codex := createServiceForTest(t, handler, `{"name":"Codex","kind":"codex_subscription"}`)
	if !codex.ResponsesWebSocket() {
		t.Fatal("Codex must default to WebSocket")
	}
	disabled := createServiceForTest(t, handler, `{"name":"Codex HTTP","kind":"codex_subscription","responses_websocket_enabled":false}`)
	if disabled.ResponsesWebSocket() {
		t.Fatal("explicit false lost")
	}
	api := createServiceForTest(t, handler, `{"name":"API","kind":"openai","http":{"base_url":"https://api.example/v1","auth":{"scheme":"none"}},"capabilities":[{"protocol":"openai.responses","mode":"native","streaming":true}]}`)
	if api.ResponsesWebSocket() {
		t.Fatal("HTTP provider should default off")
	}
	for _, service := range []contract.Service{codex, disabled, api} {
		record, err := store.GetService(context.Background(), service.ID)
		if err != nil {
			t.Fatal(err)
		}
		enabled := !service.ResponsesWebSocket()
		response := serviceRequestForTest(t, handler, http.MethodPatch, ServicesPath+"/"+string(service.ID), "application/merge-patch+json", fmt.Sprintf(`{"responses_websocket_enabled":%t}`, enabled), record.ETag)
		if response.Code != 200 {
			t.Fatalf("patch %d %s", response.Code, response.Body.String())
		}
		saved, err := store.GetService(context.Background(), service.ID)
		if err != nil || saved.Service.ResponsesWebSocket() != enabled {
			t.Fatalf("persisted switch %v %v", saved, err)
		}
		response = serviceRequestForTest(t, handler, http.MethodPatch, ServicesPath+"/"+string(service.ID), "application/merge-patch+json", `{"responses_websocket_enabled":null}`, saved.ETag)
		if response.Code != 422 {
			t.Fatalf("accepted null: %d", response.Code)
		}
	}
}

func TestSubscriptionServicesAcceptOnlyProviderEgressConversions(t *testing.T) {
	// Rejected creates still draw an ID, so reserve one per create attempt.
	store, handler := newServiceHandler(t,
		"service_codex_no_engine", "service_codex_bad_one", "service_codex_bad_two",
		"service_codex_convert", "service_codex_default",
	)
	codex := `{"protocol":"openai.responses","mode":"native","streaming":true},` +
		`{"protocol":"openai.responses.compact","mode":"native","streaming":false},` +
		`{"protocol":"openai.models","mode":"native","streaming":false}`
	chatToResponses := `{"protocol":"openai.chat","mode":"native","streaming":true,"convert_to":"openai.responses"}`
	create := func(capabilities string) *httptest.ResponseRecorder {
		return serviceRequestForTest(t, handler, http.MethodPost, ServicesPath, "application/json",
			`{"name":"Codex","kind":"codex_subscription","capabilities":[`+capabilities+`]}`, "")
	}

	if response := create(codex + "," + chatToResponses); response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), "local conversion is unavailable") {
		t.Fatalf("engine unavailable: status=%d body=%s", response.Code, response.Body.String())
	}
	handler.capabilities.ConversionEngine = relaykitbridge.Descriptor(relaykitbridge.NewEngine())
	for name, test := range map[string]struct{ capabilities, want string }{
		"wrong egress": {
			codex + `,{"protocol":"openai.chat","mode":"native","streaming":true,"convert_to":"anthropic.messages"}`,
			"can only convert to openai.responses",
		},
		"missing native": {
			`{"protocol":"openai.responses","mode":"native","streaming":true},` + chatToResponses,
			"is required",
		},
	} {
		if response := create(test.capabilities); response.Code != http.StatusUnprocessableEntity ||
			!strings.Contains(response.Body.String(), test.want) {
			t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body.String())
		}
	}

	converted := createServiceForTest(t, handler,
		`{"name":"Codex","kind":"codex_subscription","capabilities":[`+codex+","+chatToResponses+`]}`)
	if len(converted.Capabilities) != 4 || converted.Capabilities[3].ConvertTo != contract.ProtocolOpenAIResponses {
		t.Fatalf("created capabilities = %#v", converted.Capabilities)
	}
	defaulted := createServiceForTest(t, handler, `{"name":"Codex default","kind":"codex_subscription"}`)
	record, err := store.GetService(context.Background(), defaulted.ID)
	if err != nil {
		t.Fatal(err)
	}
	patch := func(capabilities, etag string) *httptest.ResponseRecorder {
		return serviceRequestForTest(t, handler, http.MethodPatch, ServicesPath+"/"+string(defaulted.ID),
			"application/merge-patch+json", `{"capabilities":[`+capabilities+`]}`, etag)
	}
	anthropicToChat := codex + `,{"protocol":"anthropic.messages","mode":"native","streaming":true,"convert_to":"openai.chat"}`
	if response := patch(anthropicToChat, record.ETag); response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), "can only convert to openai.responses") {
		t.Fatalf("patch wrong egress: status=%d body=%s", response.Code, response.Body.String())
	}
	if response := patch(codex+","+chatToResponses, record.ETag); response.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", response.Code, response.Body.String())
	}
	saved, err := store.GetService(context.Background(), defaulted.ID)
	if err != nil || len(saved.Service.Capabilities) != 4 ||
		saved.Service.Capabilities[3].Protocol != contract.ProtocolOpenAIChat {
		t.Fatalf("persisted capabilities = %#v, %v", saved.Service.Capabilities, err)
	}
}
