package controlapi

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/modelmeta"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
)

func TestModelMetadataFollowsRoutingAndPriceBindings(t *testing.T) {
	ctx := context.Background()
	store, err := openControlTestStore(t, ctx, filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tokens := &fakeAccessTokenManager{}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("test", "test"), Dependencies{AccessTokenManager: tokens, ServiceStore: store, RequestRecords: store, ControlToken: testControlToken, PricingStore: store, PricingManager: pricing.NewManager(store, nil)})
	if err != nil {
		t.Fatal(err)
	}
	err = store.SavePricingCatalog(ctx, pricing.Catalog{
		Version: "v1", ActivatedAt: time.Now().UTC(),
		Prices: []pricing.Price{{Provider: "moonshotai", Model: "kimi-k3", Expression: `tier("standard",p * 1 + c * 4)`}},
		Models: []modelmeta.Model{
			{Provider: "moonshotai", Model: "kimi-k3", Reasoning: true, ReasoningLevels: []string{"low", "high", "max"}, Input: []string{}},
			{Provider: "moonshotai", Model: "kimi-k2.7-code", Reasoning: true, ReasoningLevels: []string{}, Input: []string{}},
			{Provider: "openai", Model: "gpt-5.6", Reasoning: true, ReasoningLevels: []string{"none", "high"}, ContextTokens: 1050000, Input: []string{"text"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := createServiceForTest(t, handler, `{"name":"Kimi","kind":"kimi_coding","http":{"base_url":"https://api.kimi.ai/coding","auth":{"scheme":"none"}},"models":["kimi-for-coding","kimi-k3"],"capabilities":[{"protocol":"anthropic.messages","mode":"native","streaming":true}]}`)
	config := `{"provider":"moonshotai","bindings":{"kimi-for-coding":{"provider":"moonshotai","model":"kimi-k2.7-code"}},"billing_day":1,"time_zone":"UTC"}`
	if r := accessTokenRequest(t, handler, "PUT", PricingPath+"/services/"+string(service.ID), "application/json", config); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	settings, err := store.GetRoutingSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.ModelRedirects = []contract.ModelRedirect{{From: "pi-coder", To: "kimi-for-coding", Enabled: true}}
	if err = store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	for model, want := range map[string]string{
		// The service's price binding names the official model.
		"kimi-for-coding": `"model":"kimi-k2.7-code"`,
		"pi-coder":        `"model":"kimi-k2.7-code"`,
		"kimi-k3":         `"reasoning_levels":["low","high","max"]`,
		// No service serves it, so its name finds the official model.
		"gpt-5.6": `"context_tokens":1050000`,
	} {
		r := accessTokenRequest(t, handler, "GET", ModelMetadataPath+"?model="+model, "", "")
		if r.Code != 200 || !strings.Contains(r.Body.String(), want) {
			t.Fatalf("%s: %d %s", model, r.Code, r.Body)
		}
	}
	if r := accessTokenRequest(t, handler, "GET", ModelMetadataPath+"?model=gpt-4o", "", ""); r.Code != 404 {
		t.Fatalf("unknown model %d %s", r.Code, r.Body)
	}
	if r := accessTokenRequest(t, handler, "GET", ModelMetadataPath, "", ""); r.Code != 400 {
		t.Fatalf("missing model %d %s", r.Code, r.Body)
	}
	// Two services publish the same public name with different price bindings.
	other := createServiceForTest(t, handler, `{"name":"Other","kind":"openai_compatible","http":{"base_url":"https://example.com/v1","auth":{"scheme":"none"}},"models":["kimi-for-coding"],"capabilities":[{"protocol":"openai.responses","mode":"native","streaming":true}]}`)
	if r := accessTokenRequest(t, handler, "PUT", PricingPath+"/services/"+string(other.ID), "application/json", `{"provider":"openai","bindings":{"kimi-for-coding":{"provider":"openai","model":"gpt-5.6"}},"billing_day":1,"time_zone":"UTC"}`); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	for _, tc := range []struct {
		id      contract.AccessTokenID
		allowed []contract.ServiceID
		want    string
	}{
		{"token_first", []contract.ServiceID{service.ID}, "kimi-k2.7-code"},
		{"token_second", []contract.ServiceID{other.ID}, "gpt-5.6"},
		{"token_empty", []contract.ServiceID{}, ""},
	} {
		tokens.tokens = append(tokens.tokens, accesstoken.Token{ID: tc.id, Services: contract.AccessTokenServices{Scope: contract.AccessTokenServicesSelected, ServiceIDs: tc.allowed}})
		input, _ := json.Marshal(map[string]any{"token_id": tc.id, "models": []string{"pi-coder", "unknown"}})
		r := accessTokenRequest(t, handler, "POST", ModelMetadataPath+"/resolve", "application/json", string(input))
		var entries map[string]modelmeta.Model
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &entries) != nil || entries["pi-coder"].Model != tc.want {
			t.Fatalf("scoped metadata: %d %s", r.Code, r.Body)
		}
		if _, ok := entries["unknown"]; ok {
			t.Fatal("invented unknown model")
		}
	}
	if r := accessTokenRequest(t, handler, "POST", ModelMetadataPath+"/resolve", "application/json", `{"token_id":"token_missing","models":["pi-coder"]}`); r.Code != 404 {
		t.Fatalf("unknown token: %d", r.Code)
	}

}
