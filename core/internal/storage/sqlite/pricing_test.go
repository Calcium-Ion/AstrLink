package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestBillingPinnedPricesRetryDedupAndRetention(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
	defer s.Close()
	service := pathTestService("service_billing")
	if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	price := pricing.Price{Provider: "openai", Model: "model_a", Expression: `tier("standard",p * 1 + c * 4)`}
	catalog := pricing.Catalog{Version: "v1", ActivatedAt: start.Add(-time.Hour), GeneratedAt: start, Prices: []pricing.Price{price}, Warnings: []string{}}
	if err := s.SavePricingCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	root := contract.RequestID("request_billing_root")
	model := "alias"
	r := contract.RequestRecord{ID: root, AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &model, Recovery: &contract.RequestRecovery{UpstreamModel: "model_a"}, StartedAt: start, Status: contract.RequestStatusPending, InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary()}
	if err := s.UpsertRequestRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	catalog.Version = "v2"
	catalog.ActivatedAt = start.Add(time.Second)
	catalog.Prices[0].Expression = `tier("changed",p * 10 + c * 40)`
	if err := s.SavePricingCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	r.Status = contract.RequestStatusFailed
	r.Usage = &contract.Usage{InputTokens: 1000000, OutputTokens: 100000, TotalTokens: 1100000}
	if err := s.UpsertRequestRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	child := r
	child.ID = "request_billing_child"
	child.ParentRequestID = &root
	if err := s.InsertRequestRecord(ctx, child); err != nil {
		t.Fatal(err)
	}
	r.AttemptIndex = 2
	r.Status = contract.RequestStatusSucceeded
	r.StartedAt = start.Add(2 * time.Second)
	for i := 0; i < 2; i++ {
		if err := s.UpsertRequestRecord(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		t.Helper()
		summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour))
		if err != nil || summary.AmountUSD != "15.400000000" || summary.Priced != 2 || summary.Requests != 1 {
			t.Fatalf("summary=%+v err=%v", summary, err)
		}
	}
	check()
	if _, err := s.BackfillPricing(ctx, service.ID); err != nil {
		t.Fatal(err)
	}
	check()
	if err := s.DeleteRequestRecord(ctx, root); err != nil {
		t.Fatal(err)
	}
	check()
	// Exclusive upper boundary must not include a request exactly at the end.
	summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(2*time.Second))
	if err != nil || summary.Priced != 1 || summary.AmountUSD != "1.400000000" {
		t.Fatalf("boundary=%+v %v", summary, err)
	}
}
func TestBillingUnmatchedBackfillAndAccountIsolation(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
	defer s.Close()
	service := pathTestService("service_billing")
	if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Minute)
	model := "unknown"
	r := contract.RequestRecord{ID: "request_billing_unknown", AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &model, StartedAt: start, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(), Usage: &contract.Usage{InputTokens: 1000000, TotalTokens: 1000000}}
	if err := s.InsertRequestRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour))
	if err != nil || summary.Unpriced != 1 {
		t.Fatalf("%+v %v", summary, err)
	}
	price := pricing.Price{Provider: "moonshotai", Model: "kimi-k2", Expression: `tier("standard",p * 0.9)`}
	if err := s.SavePricingCatalog(ctx, pricing.Catalog{Version: "new", ActivatedAt: time.Now().UTC(), Prices: []pricing.Price{price}}); err != nil {
		t.Fatal(err)
	}
	config := pricing.DefaultConfig(service.Kind)
	config.Bindings[model] = pricing.Binding{Provider: "moonshotai", Model: "kimi-k2"}
	if err := s.SavePricingConfig(ctx, service.ID, config); err != nil {
		t.Fatal(err)
	}
	// Log deletion does not make an unpriced ledger entry impossible to value.
	if err := s.DeleteRequestRecord(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BackfillPricing(ctx, service.ID); err != nil {
		t.Fatal(err)
	}
	summary, err = s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour))
	if err != nil || summary.AmountUSD != "0.900000000" || summary.Revalued != 1 {
		t.Fatalf("%+v %v", summary, err)
	}
	summary, err = s.BillingSummary(ctx, service.ID, "a_different_account", start, start.Add(time.Hour))
	if err != nil || summary.Requests != 0 {
		t.Fatalf("account mix: %+v %v", summary, err)
	}
	config.Provider = "zai-coding-plan"
	if s.SavePricingConfig(ctx, service.ID, config) == nil {
		t.Fatal("accepted a plan price source")
	}
}
func TestObservedPeriodsResetHistoryAndMonthlyBudget(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Second)
	s.now = func() time.Time { return now }
	service := pathTestService("service_billing")
	if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	seconds := int64(18000)
	end := now.Add(time.Hour)
	window := &contract.RateLimitWindow{UsedPercent: 40, LimitWindowSeconds: &seconds, ResetAt: &end}
	u := contract.SubscriptionUsage{ServiceID: service.ID, FetchedAt: now, Primary: window}
	for i := 0; i < 2; i++ {
		if err := s.ObserveSubscriptionUsage(ctx, service, u); err != nil {
			t.Fatal(err)
		}
	}
	c := pricing.DefaultConfig(service.Kind)
	c.MonthlyBudgetUSD = "50"
	if err := s.SavePricingConfig(ctx, service.ID, c); err != nil {
		t.Fatal(err)
	}
	if err := s.ObserveSubscriptionReset(ctx, service); err != nil {
		t.Fatal(err)
	}
	u.FetchedAt = now.Add(time.Second)
	u.Primary.UsedPercent = 0
	if err := s.ObserveSubscriptionUsage(ctx, service, u); err != nil {
		t.Fatal(err)
	}
	report, err := s.ServiceBilling(ctx, service.ID)
	if err != nil {
		t.Fatal(err)
	}
	primary := 0
	for _, p := range report.Periods {
		if p.Kind == "primary" {
			primary++
		}
		if p.Kind == "month" && p.BudgetUSD != "" && p.RemainingUSD != "50.000000000" {
			t.Fatal(p)
		}
	}
	if primary != 2 {
		t.Fatalf("periods=%+v", report.Periods)
	}
}

func TestMissingPricesFillAutomaticallyOncePerCatalog(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
	defer s.Close()
	service := pathTestService("service_autoprice")
	if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Minute)
	for i, model := range []string{"known", "unknown"} {
		r := contract.RequestRecord{ID: contract.RequestID([]string{"request_auto_known", "request_auto_unknown"}[i]), AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &model, StartedAt: start, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(), Usage: &contract.Usage{InputTokens: 1000000, TotalTokens: 1000000}}
		if err := s.InsertRequestRecord(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	c := pricing.Catalog{Version: "auto_v1", ActivatedAt: time.Now().UTC(), Prices: []pricing.Price{{Provider: "openai", Model: "known", Expression: `tier("standard",p * 1)`}}}
	if err := s.SavePricingCatalog(ctx, c); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PriceUnpriced(ctx); err != nil || n != 2 {
		t.Fatalf("initial fill=%d %v", n, err)
	}
	if n, err := s.PriceUnpriced(ctx); err != nil || n != 0 {
		t.Fatalf("repeated fill=%d %v", n, err)
	}
	c.Version = "auto_v2"
	c.ActivatedAt = time.Now().UTC()
	c.Prices = []pricing.Price{{Provider: "openai", Model: "known", Expression: `tier("standard",p * 9)`}, {Provider: "openai", Model: "unknown", Expression: `tier("standard",p * 2)`}}
	if err := s.SavePricingCatalog(ctx, c); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PriceUnpriced(ctx); err != nil || n != 1 {
		t.Fatalf("new price fill=%d %v", n, err)
	}
	summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour))
	if err != nil || summary.AmountUSD != "3.000000000" || summary.Unpriced != 0 || summary.Priced != 2 {
		t.Fatalf("%+v %v", summary, err)
	}
}

func TestInterruptedBillingBecomesUnpricedAndCannotBeBackfilledAsComplete(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
	defer s.Close()
	service := pathTestService("service_interrupted")
	if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Minute)
	price := pricing.Price{Provider: "openai", Model: "model_a", Expression: `tier("standard",p * 1)`}
	if err := s.SavePricingCatalog(ctx, pricing.Catalog{Version: "v1", ActivatedAt: start.Add(-time.Hour), Prices: []pricing.Price{price}}); err != nil {
		t.Fatal(err)
	}
	model := "model_a"
	r := contract.RequestRecord{ID: "request_billing_interrupted", AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &model, StartedAt: start, Status: contract.RequestStatusPending, InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(), Usage: &contract.Usage{InputTokens: 100, TotalTokens: 100}}
	if err := s.InsertRequestRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RecoverPendingRequestRecords(ctx); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := s.BackfillPricing(ctx, service.ID); err != nil {
		t.Fatal(err)
	}
	summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour))
	if err != nil || summary.Unpriced != 1 || summary.Pending != 0 || summary.Priced != 0 {
		t.Fatalf("%+v %v", summary, err)
	}
}
