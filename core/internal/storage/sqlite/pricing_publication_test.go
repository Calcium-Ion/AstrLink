package sqlite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/modelmeta"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type sourceRedirect struct{ target *url.URL }

func (s sourceRedirect) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host, r.Host = s.target.Scheme, s.target.Host, s.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

// AstrLink 0.2.18 skipped every price that uses cc12h. After an upgrade the
// next daily sync of a new publication parses those prices and values what
// was left unpriced; an amount already valued keeps its original price.
func TestNextPublicationPricesModelsAnEarlierReleaseSkipped(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
	defer s.Close()
	service := pathTestService("service_reparse")
	if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	const astra = `len <= 272000 ? tier("0_272k", p * 10 + cr * 1 + cc * 12.5 + cc12h * 25 + c * 50) : tier("272k_plus", p * 20 + cr * 2 + cc * 25 + cc12h * 50 + c * 75)`
	activated := time.Now().UTC().Add(-25 * time.Hour)
	old := pricing.Catalog{Version: "<PRIVATE_ACCOUNT_NUMBER_bd80e8de72437a94>", ETag: `"v1"`, ActivatedAt: activated, GeneratedAt: activated,
		Prices: []pricing.Price{{Provider: "openai", Model: "gpt-5.5", Expression: `tier("standard",p * 1)`}}, Models: []modelmeta.Model{},
		Warnings: []string{"openai/gpt-6-astra: unsupported billing expression"}}
	if err := s.SavePricingCatalog(ctx, old); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Minute)
	cached := 126592
	for id, call := range map[contract.RequestID]struct {
		model string
		usage contract.Usage
	}{
		"request_reparse_astra":  {"gpt-6-astra", contract.Usage{InputTokens: 126800, OutputTokens: 4997, TotalTokens: 131797, CacheReadTokens: &cached}},
		"request_reparse_priced": {"gpt-5.5", contract.Usage{InputTokens: 1000000, TotalTokens: 1000000}},
	} {
		r := contract.RequestRecord{ID: id, AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &call.model, StartedAt: start, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(), Usage: &call.usage}
		if err := s.InsertRequestRecord(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	summary := func() pricing.Summary {
		t.Helper()
		v, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := summary(); v.AmountUSD != "1.000000000" || v.Unpriced != 1 {
		t.Fatalf("before upgrade %+v", v)
	}

	// LLM Metadata has published again since the saved catalog.
	base, _ := url.Parse(modelmeta.SourceURL)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, base.Path) {
		case "/manifest.json":
			if r.Header.Get("If-None-Match") == `"v2"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"v2"`)
			json.NewEncoder(w).Encode(map[string]any{"version": 1, "generatedAt": activated.Add(24 * time.Hour), "warnings": []string{}})
		case "/all.json":
			w.Write([]byte(`{"openai":{"models":{"gpt-6-astra":{"name":"GPT-6 Astra"},"gpt-5.5":{"name":"GPT-5.5"}}}}`))
		case "/newapi/providers/openai/ratio_config-v1-base.json":
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
				"billing_mode": map[string]string{"gpt-6-astra": "tiered_expr", "gpt-5.5": "tiered_expr"},
				"billing_expr": map[string]string{"gpt-6-astra": astra, "gpt-5.5": `tier("standard",p * 2)`},
			}})
		default:
			t.Errorf("unexpected source path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	manager := pricing.NewManager(s, &http.Client{Transport: sourceRedirect{target}})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { manager.Run(runCtx, t.Logf); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for summary().Unpriced != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if v := summary(); v.AmountUSD != "1.378522000" || v.Priced != 2 || v.Unpriced != 0 {
		t.Fatalf("after upgrade %+v", v)
	}
	c, err := s.PricingCatalog(ctx)
	if err != nil || c.Version == old.Version || len(c.Prices) != 2 || len(c.Warnings) != 0 {
		t.Fatalf("catalog %+v %v", c, err)
	}
}
