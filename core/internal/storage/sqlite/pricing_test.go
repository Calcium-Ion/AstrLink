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

func TestBillingDefaultPriceBinding(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
	defer s.Close()
	service := pathTestService("service_codex_price")
	if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	// Persisted configurations with no explicit binding must also use the alias.
	if err := s.SavePricingConfig(ctx, service.ID, pricing.DefaultConfig(service.Kind)); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Minute)
	price := pricing.Price{Provider: "openai", Model: "gpt-5.6-luna", Expression: `tier("standard", p * 0.2)`}
	if err := s.SavePricingCatalog(ctx, pricing.Catalog{Version: "luna_v1", ActivatedAt: start.Add(-time.Hour), Prices: []pricing.Price{price}}); err != nil {
		t.Fatal(err)
	}
	model := "codex-auto-review"
	r := contract.RequestRecord{ID: "request_codex_price", AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &model, StartedAt: start, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(), Usage: &contract.Usage{InputTokens: 1000000, TotalTokens: 1000000}}
	if err := s.InsertRequestRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
	if err != nil || summary.Priced != 1 || summary.Unpriced != 0 || summary.AmountUSD != "0.200000000" {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
}

func TestInterruptedBillingIsValuedFromPartialUsage(t *testing.T) {
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
	// One call was cut off after it reported input, the other before any usage.
	for i, usage := range []*contract.Usage{{InputTokens: 100, TotalTokens: 100}, nil} {
		r := contract.RequestRecord{ID: contract.RequestID(fmt.Sprintf("request_billing_interrupted_%d", i)), AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &model, StartedAt: start, Status: contract.RequestStatusPending, InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(), Usage: usage}
		if err := s.InsertRequestRecord(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.RecoverPendingRequestRecords(ctx); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if n, err := s.PriceUnpriced(ctx); err != nil || n != 2 {
		t.Fatalf("valued=%d %v", n, err)
	}
	if _, err := s.BackfillPricing(ctx, service.ID); err != nil {
		t.Fatal(err)
	}
	summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
	if err != nil || summary.AmountUSD != "0.000100000" || summary.Unpriced != 0 || summary.Pending != 0 || summary.Priced != 2 {
		t.Fatalf("%+v %v", summary, err)
	}
}

func TestAttemptsWithoutUsageCostNothingUnlessTheySucceeded(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
	defer s.Close()
	service := pathTestService("service_no_usage")
	if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Minute)
	price := pricing.Price{Provider: "openai", Model: "model_a", Expression: `tier("standard",p * 1)`}
	if err := s.SavePricingCatalog(ctx, pricing.Catalog{Version: "v1", ActivatedAt: start.Add(-time.Hour), Prices: []pricing.Price{price}}); err != nil {
		t.Fatal(err)
	}
	model := "model_a"
	record := func(id contract.RequestID, attempt int, status contract.RequestStatus, usage *contract.Usage) contract.RequestRecord {
		return contract.RequestRecord{ID: id, AttemptIndex: attempt, ServiceID: &service.ID, RequestedModel: &model, StartedAt: start, Status: status, InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(), Usage: usage}
	}
	// A root that failed over: its first attempt failed without usage.
	root := contract.RequestID("request_no_usage_root")
	child := record("request_no_usage_child", 1, contract.RequestStatusFailed, nil)
	child.ParentRequestID = &root
	for _, r := range []contract.RequestRecord{
		record(root, 2, contract.RequestStatusSucceeded, &contract.Usage{InputTokens: 1000000, TotalTokens: 1000000}),
		child,
		record("request_no_usage_failed", 1, contract.RequestStatusFailed, nil),
		record("request_no_usage_cancelled", 1, contract.RequestStatusCancelled, nil),
		// A successful call without usage was billed by the provider for an
		// amount nobody knows, so it stays unpriced.
		record("request_no_usage_succeeded", 1, contract.RequestStatusSucceeded, nil),
	} {
		if err := s.InsertRequestRecord(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		t.Helper()
		summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
		if err != nil || summary.AmountUSD != "1.000000000" || summary.Priced != 4 || summary.Unpriced != 1 || summary.Requests != 4 {
			t.Fatalf("summary=%+v err=%v", summary, err)
		}
	}
	check()
	// Entries an earlier release left unpriced are settled from their request records.
	if _, err := s.db.ExecContext(ctx, `UPDATE billing_ledger SET reason='missing_usage' WHERE usage_json='null'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PriceUnpriced(ctx); err != nil || n != 3 {
		t.Fatalf("settled=%d %v", n, err)
	}
	check()
}

func TestPricingRepairPreservesSnapshotsAndUnknownUsage(t *testing.T) {
	for _, newerCatalog := range []bool{false, true} {
		t.Run(fmt.Sprintf("newer_catalog=%t", newerCatalog), func(t *testing.T) {
			ctx := context.Background()
			s := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
			defer s.Close()
			service := pathTestService("service_usage_repair")
			if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
				t.Fatal(err)
			}
			config := pricing.DefaultConfig(service.Kind)
			config.Provider = "google"
			if err := s.SavePricingConfig(ctx, service.ID, config); err != nil {
				t.Fatal(err)
			}
			start := time.Now().UTC().Add(-time.Minute)
			catalog := pricing.Catalog{Version: "repair_v1", ActivatedAt: start.Add(-time.Hour), Prices: []pricing.Price{
				{Provider: "google", Model: "gemini-3.7-flash", Expression: `tier("standard", p * 0.75 + cr * 0.075 + ai * 0.75 + c * 3.75)`},
			}}
			if err := s.SavePricingCatalog(ctx, catalog); err != nil {
				t.Fatal(err)
			}
			// Reasons an earlier release gave for the nine repairable entries.
			reasons := []string{"incomplete_usage", "invalid_cache_partition", "missing_cache_ttl", "missing_audio_usage", "invalid_token_partition", "missing_audio_cache_partition", "missing_billing_parameter", "missing_audio_usage", "incomplete_usage"}
			model := "gemini-3.7-flash"
			for i := 0; i < 112; i++ {
				cache := 400000
				usage := &contract.Usage{InputTokens: 1000000, OutputTokens: 100000, TotalTokens: 1100000, CacheReadTokens: &cache}
				switch {
				case i < 101:
					// More than one page of interrupted calls whose logs are gone
					// stays unpriced; it must not keep the repairable entries
					// later in the ledger from being reached.
					usage = nil
				case i == 109:
					audio := 200000
					usage.InputAudioTokens = &audio
				case i == 111:
					usage.BillingIncomplete = true
				}
				r := contract.RequestRecord{ID: contract.RequestID(fmt.Sprintf("request_repair_%03d", i)), AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &model, StartedAt: start, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolGoogleGenerateContent, Audit: contract.NotCapturedAuditSummary(), Usage: usage}
				if err := s.InsertRequestRecord(ctx, r); err != nil {
					t.Fatal(err)
				}
				if i < 110 {
					// Recreate the ledger an earlier release left behind.
					reason := "incomplete_usage"
					if i > 100 {
						reason = reasons[i-101]
					}
					if _, err := s.db.ExecContext(ctx, `UPDATE billing_ledger SET reason=?,amount_usd='0.000000000',tier='',account_key='historical_account' WHERE root_id=?`, reason, r.ID); err != nil {
						t.Fatal(err)
					}
				}
				if i <= 101 {
					if err := s.DeleteRequestRecord(ctx, r.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if newerCatalog {
				catalog.Version = "repair_v2"
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
			if err != nil || summary.AmountUSD != "9.405000000" || summary.Priced != 11 || summary.Unpriced != 101 || summary.Revalued != 9 {
				t.Fatalf("summary=%+v error=%v", summary, err)
			}
			var missing int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_ledger WHERE reason='missing_usage'`).Scan(&missing); err != nil || missing != 101 {
				t.Fatalf("missing usage=%d %v", missing, err)
			}
			var version, account string
			var audio any
			if err := s.db.QueryRowContext(ctx, `SELECT price_version,account_key,json_extract(usage_json,'$.input_audio_tokens') FROM billing_ledger WHERE root_id='request_repair_101'`).Scan(&version, &account, &audio); err != nil {
				t.Fatal(err)
			}
			if version != "repair_v1" || account != "historical_account" || audio != nil {
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

func TestBillingSummaryServiceAmountsCountFailoverRootsPerService(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t, filepath.Join(t.TempDir(), "service-order.db"))
	defer s.Close()
	start := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		root    string
		attempt int
		service string
		amount  string
		reason  string
	}{
		{"request_a", 1, "service_small", "9", "priced"},
		{"request_b", 1, "service_large", "4", "priced"},
		{"request_b", 2, "service_large", "6", "priced"},
		// A failover attempt bills the service that served it.
		{"request_c", 1, "service_large", "0", "unpriced"},
		{"request_c", 2, "service_small", "0.5", "priced"},
		{"request_d", 1, "service_tie", "9.000000000", "priced"},
		{"request_e", 1, "service_pending", "0", "pending"},
	} {
		_, err := s.db.ExecContext(ctx, `INSERT INTO billing_ledger
(root_id,attempt,service_id,account_key,model,started_at,terminal,amount_usd,reason)
VALUES (?,?,?,'','model_sort',?,1,?,?)`,
			row.root, row.attempt, row.service, billingTime(start), row.amount, row.reason)
		if err != nil {
			t.Fatal(err)
		}
	}
	summary, err := s.BillingSummary(ctx, "", "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if summary.AmountUSD != "28.500000000" || summary.Requests != 5 {
		t.Fatalf("summary=%+v", summary)
	}
	// Numeric cost first (10 > 9.5 > 9), then root count, then service ID.
	want := []pricing.ServiceGroup{
		{ServiceID: "service_large", Amounts: pricing.Amounts{AmountUSD: "10.000000000", Priced: 2, Unpriced: 1, Requests: 2}},
		{ServiceID: "service_small", Amounts: pricing.Amounts{AmountUSD: "9.500000000", Priced: 2, Requests: 2}},
		{ServiceID: "service_tie", Amounts: pricing.Amounts{AmountUSD: "9.000000000", Priced: 1, Requests: 1}},
		{ServiceID: "service_pending", Amounts: pricing.Amounts{AmountUSD: "0.000000000", Pending: 1, Requests: 1}},
	}
	if !slices.Equal(summary.ByService, want) {
		t.Fatalf("services=%+v\nwant=%+v", summary.ByService, want)
	}
	scoped, err := s.BillingSummary(ctx, "service_small", "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
	if err != nil || len(scoped.ByService) != 1 || scoped.ByService[0] != want[1] {
		t.Fatalf("scoped services=%+v error=%v", scoped.ByService, err)
	}
}

func TestRunningCallCountsWithUsageSoFar(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
	defer s.Close()
	service := pathTestService("service_running")
	if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	price := pricing.Price{Provider: "openai", Model: "model_a", Expression: `tier("standard",p * 1 + c * 4)`}
	catalog := pricing.Catalog{Version: "v1", ActivatedAt: start.Add(-time.Hour), GeneratedAt: start, Prices: []pricing.Price{price}, Warnings: []string{}}
	if err := s.SavePricingCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	root := contract.RequestID("request_running_root")
	model := "model_a"
	r := contract.RequestRecord{ID: root, AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &model, StartedAt: start, Status: contract.RequestStatusPending, InputProtocol: contract.ProtocolAnthropicMessages, Audit: contract.NotCapturedAuditSummary()}
	if err := s.UpsertRequestRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	check := func(step, amount string, priced, pending int64) {
		t.Helper()
		summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
		if err != nil || summary.AmountUSD != amount || summary.Priced != priced || summary.Pending != pending || summary.Requests != 1 {
			t.Fatalf("%s: summary=%+v err=%v", step, summary.Amounts, err)
		}
		if len(summary.ByService) != 1 || summary.ByService[0].AmountUSD != amount {
			t.Fatalf("%s: by service=%+v", step, summary.ByService)
		}
	}
	check("before usage", "0.000000000", 0, 1)

	// The stream reported its input: the running call counts it at once.
	read := 200000
	live := contract.Usage{InputTokens: 1000000, OutputTokens: 1, TotalTokens: 1000001, CacheReadTokens: &read, BillingIncomplete: true}
	if err := s.UpdateLiveUsage(ctx, root, 1, live); err != nil {
		t.Fatal(err)
	}
	check("live input", "1.000004000", 0, 1)

	// A later pending snapshot carries the same usage and keeps the value.
	r.Usage = &live
	if err := s.UpsertRequestRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	check("pending snapshot", "1.000004000", 0, 1)

	r.Status = contract.RequestStatusSucceeded
	r.Usage = &contract.Usage{InputTokens: 1000000, OutputTokens: 100000, TotalTokens: 1100000, CacheReadTokens: &read}
	if err := s.UpsertRequestRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	check("settled", "1.400000000", 1, 0)

	// A read that arrives after the call settled changes nothing.
	if err := s.UpdateLiveUsage(ctx, root, 1, live); err != nil {
		t.Fatal(err)
	}
	check("late update", "1.400000000", 1, 0)
	if err := s.UpdateLiveUsage(ctx, "request_running_unknown", 1, live); err != nil {
		t.Fatalf("unknown entry: %v", err)
	}
}
