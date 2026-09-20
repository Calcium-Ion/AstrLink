package controlapi

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPricingAPIAuthAndConfig(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "pricing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("test", "test"), Dependencies{ServiceStore: store, RequestRecords: store, ControlToken: testControlToken, PricingStore: store, PricingManager: pricing.NewManager(store, nil)})
	if err != nil {
		t.Fatal(err)
	}
	service := createServiceForTest(t, handler, `{"name":"Kimi","kind":"kimi_coding","http":{"base_url":"https://api.kimi.ai/coding","auth":{"scheme":"none"}},"models":["kimi-for-coding"],"capabilities":[{"protocol":"anthropic.messages","mode":"native","streaming":true}]}`)
	path := PricingPath + "/services/" + string(service.ID)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest("GET", PricingPath+"/catalog", nil))
	if unauthorized.Code != 401 {
		t.Fatal(unauthorized.Code)
	}
	r := accessTokenRequest(t, handler, "GET", path, "", "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"provider":"moonshotai"`) {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	config := `{"provider":"moonshotai","bindings":{"kimi-for-coding":{"provider":"moonshotai","model":"kimi-k2.7-code"}},"monthly_budget_usd":"50","billing_day":15,"time_zone":"Asia/Shanghai"}`
	r = accessTokenRequest(t, handler, "PUT", path, "application/json", config)
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	r = accessTokenRequest(t, handler, "PUT", path, "application/json", strings.Replace(config, "moonshotai", "zai-coding-plan", 1))
	if r.Code != 400 {
		t.Fatalf("plan price accepted %d", r.Code)
	}
	for _, p := range []string{"status", "catalog", "summary?from=2026-09-01T00:00:00Z&to=2026-10-01T00:00:00Z"} {
		r = accessTokenRequest(t, handler, http.MethodGet, PricingPath+"/"+p, "", "")
		if r.Code != 200 {
			t.Fatalf("%s %d %s", p, r.Code, r.Body)
		}
	}
}
