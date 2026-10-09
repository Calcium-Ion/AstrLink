package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
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

func TestClearServiceRiskReleasesRouteCooldown(t *testing.T) {
	for _, test := range []struct {
		kind     contract.ServiceKind
		protocol contract.ProtocolID
		code     string
	}{
		{contract.ServiceKindCodexSubscription, contract.ProtocolOpenAIResponses, contract.RiskCodeUsageLimitReached},
		{contract.ServiceKindClaudeSubscription, contract.ProtocolAnthropicMessages, contract.RiskCodeRateLimit7d},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			store, handler := newServiceHandler(t, "service_risk_cooldown")
			service := createServiceForTest(t, handler, fmt.Sprintf(
				`{"name":"Risk cooldown","kind":%q,"models":["test-model"]}`, test.kind,
			))
			connectSubscriptionForTest(t, store, accountauth.NewMemoryCredentialStore(), service.ID)
			resolver, err := endpoint.NewStoreResolver(store)
			if err != nil {
				t.Fatal(err)
			}
			handler, err = NewWithDependencies(handler.version, Dependencies{
				ServiceStore: store, Subscriptions: handler.subscriptions, ControlToken: testControlToken,
				RateLimits: resolver,
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			request := endpoint.ResolveRequest{Protocol: test.protocol, Model: "test-model", Streaming: true}
			candidate, err := resolver.Resolve(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			until := time.Now().Add(7 * 24 * time.Hour)
			if err := handler.subscriptions.ReportRisk(ctx, service.ID, contract.SubscriptionRiskObservation{
				State: contract.SubscriptionRiskCooling, Code: test.code,
				HTTPStatus: http.StatusTooManyRequests, PausedUntil: &until,
			}); err != nil {
				t.Fatal(err)
			}
			// The second clear starts with no account risk, as on an old core
			// that already cleared the account but left its route cooldown.
			for attempt := 0; attempt < 2; attempt++ {
				resolver.RecordRateLimit(candidate, 7*24*time.Hour)
				if resolver.RateLimitedUntil(candidate).IsZero() {
					t.Fatal("route cooldown was not recorded")
				}
				cleared := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath+"/"+string(service.ID)+"/risk/clear", "", "", "")
				if cleared.Code != http.StatusOK {
					t.Fatalf("clear %d: status=%d body=%s", attempt, cleared.Code, cleared.Body.String())
				}
				var restored contract.Service
				decode(t, cleared, &restored)
				if restored.Subscription == nil || restored.Subscription.Risk != nil {
					t.Fatalf("account risk was not cleared: %+v", restored.Subscription)
				}
				resolved, err := resolver.Resolve(ctx, request)
				if err != nil || resolved.CanonicalService().ID != service.ID {
					t.Fatalf("clear %d succeeded but route is still blocked: %v", attempt, err)
				}
			}
		})
	}
}

type riskRateLimitReleaserFunc func(contract.ServiceID)

func (release riskRateLimitReleaserFunc) ReleaseRateLimits(id contract.ServiceID) { release(id) }

type failingClearRiskEventStore struct {
	subscription.RiskEventStore
}

func (failingClearRiskEventStore) AppendSubscriptionRiskEvent(context.Context, contract.SubscriptionRiskEvent) error {
	return errors.New("risk event write failed")
}

func TestClearServiceRiskFailurePreservesCooldown(t *testing.T) {
	store, handler := newServiceHandler(t, "service_risk_clear_failed")
	service := createServiceForTest(t, handler, `{"name":"Risk clear failure","kind":"codex_subscription"}`)
	connectSubscriptionForTest(t, store, accountauth.NewMemoryCredentialStore(), service.ID)
	until := time.Now().Add(time.Hour)
	if err := handler.subscriptions.ReportRisk(context.Background(), service.ID, contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskCooling, Code: contract.RiskCodeUsageLimitReached,
		HTTPStatus: http.StatusTooManyRequests, PausedUntil: &until,
	}); err != nil {
		t.Fatal(err)
	}
	handler.subscriptions.SetRiskEventStore(failingClearRiskEventStore{RiskEventStore: store})
	handler.rateLimits = riskRateLimitReleaserFunc(func(id contract.ServiceID) {
		t.Errorf("failed clear released rate limits for %s", id)
	})
	response := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath+"/"+string(service.ID)+"/risk/clear", "", "", "")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("failed clear: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestServiceRiskEndpointsRejectInvalidTargets(t *testing.T) {
	_, handler := newServiceHandler(t, "service_codex_risk_empty", "service_http_risk")
	handler.rateLimits = riskRateLimitReleaserFunc(func(id contract.ServiceID) {
		t.Errorf("invalid request released rate limits for %s", id)
	})
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
