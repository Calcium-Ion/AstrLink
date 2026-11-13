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

func TestClaudeCLICallsAreBilledOnlyOnTheirParent(t *testing.T) {
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
	model := "model_a"
	parent := contract.RequestRecord{
		ID: root, AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &model, StartedAt: start,
		Status: contract.RequestStatusPending, InputProtocol: contract.ProtocolAnthropicMessages,
		Audit: contract.NotCapturedAuditSummary(),
	}
	if err := s.UpsertRequestRecord(ctx, parent); err != nil {
		t.Fatal(err)
	}
	// The CLI's own calls finish first, with the same attempt number as the
	// parent; the parent's usage is their sum.
	for _, id := range []contract.RequestID{"request_cli_call_1", "request_cli_call_2"} {
		call := parent
		call.ID = id
		call.ParentRequestID = &root
		call.Status = contract.RequestStatusSucceeded
		call.Usage = &contract.Usage{InputTokens: 500000, OutputTokens: 50000, TotalTokens: 550000}
		call.RoutingDecision = &contract.RequestRoutingDecision{Selected: contract.RoutingSelectionClaudeCLI, Skipped: []contract.RoutingSkip{}}
		if err := s.UpsertRequestRecord(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	parent.Status = contract.RequestStatusSucceeded
	parent.Usage = &contract.Usage{InputTokens: 1000000, OutputTokens: 100000, TotalTokens: 1100000}
	if err := s.UpsertRequestRecord(ctx, parent); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		summary, err := s.BillingSummary(ctx, service.ID, "", start, start.Add(time.Hour), pricing.BillingSummaryOptions{})
		if err != nil || summary.AmountUSD != "1.400000000" || summary.Priced != 1 || summary.Requests != 1 {
			t.Fatalf("summary=%+v err=%v", summary, err)
		}
	}
	check()
	if _, err := s.BackfillPricing(ctx, service.ID); err != nil {
		t.Fatal(err)
	}
	check()
}
