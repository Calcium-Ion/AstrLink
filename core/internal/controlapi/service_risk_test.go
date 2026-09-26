package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

func TestClearServiceRiskRestoresSchedulingAndRecordsHistory(t *testing.T) {
	store, handler := newServiceHandler(t, "service_codex_risk")
	handler.subscriptions.SetRiskEventStore(store)
	service := createServiceForTest(t, handler, `{"name":"Codex risk","kind":"codex_subscription"}`)
	connectSubscriptionForTest(t, store, accountauth.NewMemoryCredentialStore(), service.ID)
	ctx := context.Background()
	until := time.Now().Add(5 * time.Hour)
	if err := handler.subscriptions.ReportRisk(ctx, service.ID, contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskCooling, Code: contract.RiskCodeUsageLimitReached,
		Message: "The usage limit has been reached", HTTPStatus: http.StatusTooManyRequests, PausedUntil: &until,
	}); err != nil {
		t.Fatal(err)
	}
	if err := handler.subscriptions.ReportRisk(ctx, service.ID, contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskSuspended, Code: contract.RiskCodeAccountDeactivated, HTTPStatus: http.StatusPaymentRequired,
	}); err != nil {
		t.Fatal(err)
	}

	listed := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/"+string(service.ID), "", "", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"risk":{"state":"suspended","code":"account_deactivated"`) {
		t.Fatalf("service before clear status=%d body=%s", listed.Code, listed.Body.String())
	}

	cleared := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath+"/"+string(service.ID)+"/risk/clear", "", "", "")
	if cleared.Code != http.StatusOK || cleared.Header().Get("ETag") == "" {
		t.Fatalf("clear status=%d body=%s", cleared.Code, cleared.Body.String())
	}
	var restored contract.Service
	if err := json.Unmarshal(cleared.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Subscription == nil || restored.Subscription.Risk != nil ||
		restored.Subscription.Status != contract.SubscriptionStatusConnected {
		t.Fatalf("restored subscription = %+v", restored.Subscription)
	}
	if strings.Contains(cleared.Body.String(), "access-secret-token-value") {
		t.Fatalf("clear response leaked credentials: %s", cleared.Body.String())
	}

	events := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/"+string(service.ID)+"/risk-events?limit=2", "", "", "")
	if events.Code != http.StatusOK {
		t.Fatalf("risk events status=%d body=%s", events.Code, events.Body.String())
	}
	var page serviceRiskEventsResponse
	if err := json.Unmarshal(events.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Kind != contract.SubscriptionRiskEventCleared ||
		page.Items[1].Kind != contract.SubscriptionRiskEventSuspended || page.Items[1].Code != contract.RiskCodeAccountDeactivated {
		t.Fatalf("risk events = %+v", page.Items)
	}
	all := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/"+string(service.ID)+"/risk-events", "", "", "")
	if err := json.Unmarshal(all.Body.Bytes(), &page); err != nil || len(page.Items) != 3 {
		t.Fatalf("default risk events = %d %s", all.Code, all.Body.String())
	}
}

func TestServiceRiskEndpointsRejectInvalidTargets(t *testing.T) {
	_, handler := newServiceHandler(t, "service_codex_risk_empty", "service_http_risk")
	codex := createServiceForTest(t, handler, `{"name":"Codex risk","kind":"codex_subscription"}`)
	gateway := createServiceForTest(t, handler, `{
		"name":"gateway","kind":"openai_compatible",
		"http":{"base_url":"https://gateway.example/v1","auth":{"scheme":"none"}},
		"capabilities":[{"protocol":"openai.responses","mode":"delegated","streaming":true}]
	}`)

	empty := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/"+string(codex.ID)+"/risk-events", "", "", "")
	if empty.Code != http.StatusOK || strings.TrimSpace(empty.Body.String()) != `{"items":[]}` {
		t.Fatalf("empty risk events status=%d body=%s", empty.Code, empty.Body.String())
	}
	for _, query := range []string{"?limit=0", "?limit=51", "?limit=abc", "?limit=1&limit=2"} {
		invalid := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/"+string(codex.ID)+"/risk-events"+query, "", "", "")
		if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invalid_query") {
			t.Fatalf("limit %s status=%d body=%s", query, invalid.Code, invalid.Body.String())
		}
	}
	if response := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/"+string(codex.ID)+"/risk/clear", "", "", ""); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET risk/clear status=%d", response.Code)
	}
	if response := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath+"/"+string(codex.ID)+"/risk-events", "", "", ""); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST risk-events status=%d", response.Code)
	}

	for _, test := range []struct{ method, path string }{
		{http.MethodPost, "/risk/clear"},
		{http.MethodGet, "/risk-events"},
	} {
		missing := serviceRequestForTest(t, handler, test.method, ServicesPath+"/service_missing_risk"+test.path, "", "", "")
		if missing.Code != http.StatusNotFound {
			t.Fatalf("missing %s status=%d body=%s", test.path, missing.Code, missing.Body.String())
		}
		httpService := serviceRequestForTest(t, handler, test.method, ServicesPath+"/"+string(gateway.ID)+test.path, "", "", "")
		if httpService.Code != http.StatusConflict || !strings.Contains(httpService.Body.String(), "service_not_subscription") {
			t.Fatalf("http %s status=%d body=%s", test.path, httpService.Code, httpService.Body.String())
		}
	}
}
