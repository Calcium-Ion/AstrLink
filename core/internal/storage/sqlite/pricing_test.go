package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
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
		summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
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
	summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(2*time.Second), pricing.BillingSummaryOptions{})
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
	summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
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
	summary, err = s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
	if err != nil || summary.AmountUSD != "0.900000000" || summary.Revalued != 1 {
		t.Fatalf("%+v %v", summary, err)
	}
	summary, err = s.BillingSummary(ctx, service.ID, "a_different_account", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
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
	summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
	if err != nil || summary.AmountUSD != "3.000000000" || summary.Unpriced != 0 || summary.Priced != 2 {
		t.Fatalf("%+v %v", summary, err)
	}
}

func TestChannelOverridesFillMissingPricesWithoutCatalogChanges(t *testing.T) {
	for _, catalogVersion := range []string{"", "existing_v1"} {
		t.Run("catalog="+catalogVersion, func(t *testing.T) {
			ctx := context.Background()
			s := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
			defer s.Close()
			start := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
			if catalogVersion != "" {
				catalog := pricing.Catalog{Version: catalogVersion, ActivatedAt: start.Add(-time.Hour), Prices: []pricing.Price{{Provider: "openai", Model: "known", Expression: `tier("standard",p * 1)`}}}
				if err := s.SavePricingCatalog(ctx, catalog); err != nil {
					t.Fatal(err)
				}
			}
			service, other := pathTestService("service_override"), pathTestService("service_other")
			model := `custom.model/"v1"`
			for _, channel := range []contract.Service{service, other} {
				if _, err := s.CreateService(ctx, channel, storage.CredentialMutation{}); err != nil {
					t.Fatal(err)
				}
				r := contract.RequestRecord{ID: contract.RequestID("request_" + channel.ID), AttemptIndex: 1, ServiceID: &channel.ID, RequestedModel: &model, StartedAt: start, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(), Usage: &contract.Usage{InputTokens: 1000000, TotalTokens: 1000000}}
				if err := s.InsertRequestRecord(ctx, r); err != nil {
					t.Fatal(err)
				}
				// Automatic valuation must work even after request-detail retention expires.
				if err := s.DeleteRequestRecord(ctx, r.ID); err != nil {
					t.Fatal(err)
				}
			}
			if n, err := s.PriceUnpriced(ctx); err != nil || n != 0 {
				t.Fatalf("before override fill=%d err=%v", n, err)
			}
			config := pricing.DefaultConfig(service.Kind)
			config.Overrides = map[string]pricing.ModelRates{model: {Input: "2", Output: "4", CacheRead: "2", CacheWrite: "2"}}
			if err := s.SavePricingConfig(ctx, service.ID, config); err != nil {
				t.Fatal(err)
			}
			if n, err := s.PriceUnpriced(ctx); err != nil || n != 1 {
				t.Fatalf("override fill=%d err=%v", n, err)
			}
			config.Overrides[model] = pricing.ModelRates{Input: "9", Output: "9", CacheRead: "9", CacheWrite: "9"}
			if err := s.SavePricingConfig(ctx, service.ID, config); err != nil {
				t.Fatal(err)
			}
			if n, err := s.PriceUnpriced(ctx); err != nil || n != 0 {
				t.Fatalf("repeated fill=%d err=%v", n, err)
			}
			summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
			if err != nil || summary.Priced != 1 || summary.Unpriced != 0 || summary.Revalued != 1 || summary.AmountUSD != "2.000000000" {
				t.Fatalf("override summary=%+v err=%v", summary, err)
			}
			summary, err = s.BillingSummary(ctx, other.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
			if err != nil || summary.Priced != 0 || summary.Unpriced != 1 {
				t.Fatalf("other channel summary=%+v err=%v", summary, err)
			}
		})
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
	summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
	if err != nil || summary.Unpriced != 1 || summary.Pending != 0 || summary.Priced != 0 {
		t.Fatalf("%+v %v", summary, err)
	}
}

func TestAudioPricingRepairPreservesSnapshotsAndUnknownUsage(t *testing.T) {
	for _, newerCatalog := range []bool{false, true} {
		t.Run(fmt.Sprintf("newer_catalog=%t", newerCatalog), func(t *testing.T) {
			ctx := context.Background()
			s := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
			defer s.Close()
			service := pathTestService("service_audio_repair")
			if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
				t.Fatal(err)
			}
			config := pricing.DefaultConfig(service.Kind)
			config.Provider = "google"
			if err := s.SavePricingConfig(ctx, service.ID, config); err != nil {
				t.Fatal(err)
			}
			start := time.Now().UTC().Add(-time.Minute)
			catalog := pricing.Catalog{Version: "audio_v1", ActivatedAt: start.Add(-time.Hour), Prices: []pricing.Price{
				{Provider: "google", Model: "gemini-3.7-flash", Expression: `tier("standard", p * 0.75 + cr * 0.075 + ai * 0.75 + c * 3.75)`},
				{Provider: "google", Model: "different_audio_rate", Expression: `tier("standard", p + ai * 10)`},
			}}
			if err := s.SavePricingCatalog(ctx, catalog); err != nil {
				t.Fatal(err)
			}
			// More than one page of genuinely unknown usage must not prevent the
			// nine repairable historical entries later in the ledger being reached.
			for i := 0; i < 112; i++ {
				model := "gemini-3.7-flash"
				if i < 101 {
					model = "different_audio_rate"
				}
				cache := 400000
				usage := &contract.Usage{InputTokens: 1000000, OutputTokens: 100000, TotalTokens: 1100000, CacheReadTokens: &cache}
				if i == 109 {
					audio := 200000
					usage.InputAudioTokens = &audio
				}
				if i == 111 {
					usage.BillingIncomplete = true
				}
				r := contract.RequestRecord{ID: contract.RequestID(fmt.Sprintf("request_audio_%03d", i)), AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &model, StartedAt: start, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolGoogleGenerateContent, Audit: contract.NotCapturedAuditSummary(), Usage: usage}
				if err := s.InsertRequestRecord(ctx, r); err != nil {
					t.Fatal(err)
				}
				if i >= 101 && i < 110 {
					// Recreate the pre-fix ledger state without inventing audio counts.
					reason := "missing_audio_usage"
					if i == 109 {
						reason = "missing_audio_cache_partition"
					}
					if _, err := s.db.ExecContext(ctx, `UPDATE billing_ledger SET reason=?,amount_usd='0.000000000',tier='',account_key='historical_account' WHERE root_id=?`, reason, r.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := s.DeleteRequestRecord(ctx, "request_audio_101"); err != nil {
				t.Fatal(err)
			}
			if newerCatalog {
				catalog.Version = "audio_v2"
				catalog.ActivatedAt = time.Now().UTC()
				catalog.Prices[0].Expression = `tier("new", p * 100 + ai * 100 + c * 100)`
				if err := s.SavePricingCatalog(ctx, catalog); err != nil {
					t.Fatal(err)
				}
			}
			if n, err := s.PriceUnpriced(ctx); err != nil || n != 9 {
				t.Fatalf("repair count=%d error=%v", n, err)
			}
			if n, err := s.PriceUnpriced(ctx); err != nil || n != 0 {
				t.Fatalf("repeat repair count=%d error=%v", n, err)
			}
			summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
			if err != nil || summary.AmountUSD != "8.550000000" || summary.Priced != 10 || summary.Unpriced != 102 || summary.Revalued != 9 {
				t.Fatalf("summary=%+v error=%v", summary, err)
			}
			var version, account string
			var audio any
			if err := s.db.QueryRowContext(ctx, `SELECT price_version,account_key,json_extract(usage_json,'$.input_audio_tokens') FROM billing_ledger WHERE root_id='request_audio_101'`).Scan(&version, &account, &audio); err != nil {
				t.Fatal(err)
			}
			if version != "audio_v1" || account != "historical_account" || audio != nil {
				t.Fatalf("snapshot changed: version=%s account=%s audio=%v", version, account, audio)
			}
		})
	}
}

func TestBillingSummaryTokenAmountsSortNumericallyAndUseCurrentTokens(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t, filepath.Join(t.TempDir(), "token-order.db"))
	defer s.Close()
	manager, err := accesstoken.NewManager(s)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 4)
	for i := range ids {
		token, err := manager.Create(ctx, fmt.Sprintf("Sort token %d", i))
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = string(token.Token.ID)
	}
	start := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	for i, row := range []struct {
		tokenID any
		amount  string
	}{
		{ids[0], "10"},
		{ids[1], "9"},
		{ids[2], "4"},
		{ids[2], "5"},
		{ids[3], "9.000000000"},
		{"token_deleted", "20"},
		{nil, "20"},
	} {
		_, err := s.db.ExecContext(ctx, `INSERT INTO billing_ledger
(root_id,attempt,service_id,account_key,model,started_at,terminal,amount_usd,reason,local_access_token_id)
VALUES (?,1,'service_sort','','model_sort',?,1,?,'priced',?)`,
			fmt.Sprintf("request_sort_%d", i), billingTime(start), row.amount, row.tokenID)
		if err != nil {
			t.Fatal(err)
		}
	}
	summary, err := s.BillingSummary(ctx, "", "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{IncludeTokenBreakdown: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.AmountUSD != "77.000000000" || summary.Requests != 7 {
		t.Fatalf("summary=%+v", summary)
	}
	// Numeric cost first (10 > 9), then root count, then token ID for ties.
	ties := []string{ids[1], ids[3]}
	slices.Sort(ties)
	want := []string{ids[0], ids[2], ties[0], ties[1]}
	got := make([]string, len(summary.ByToken))
	for i, group := range summary.ByToken {
		got[i] = group.TokenID
	}
	if !slices.Equal(got, want) {
		t.Fatalf("token order=%v, want=%v", got, want)
	}
	if summary.ByToken[0].AmountUSD != "10.000000000" || summary.ByToken[1].AmountUSD != "9.000000000" || summary.ByToken[1].Requests != 2 {
		t.Fatalf("token amounts=%+v", summary.ByToken)
	}
	withoutTokens, err := s.BillingSummary(ctx, "", "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
	if err != nil || withoutTokens.ByToken == nil || len(withoutTokens.ByToken) != 0 || withoutTokens.Amounts != summary.Amounts {
		t.Fatalf("without token breakdown=%+v error=%v", withoutTokens, err)
	}
}
