package sqlite

import (
	"context"
	"errors"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceStatisticsRatesCacheMissingAndPinnedHistory(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "stats.db"))
	defer s.Close()
	service := pathTestService("service_stats")
	if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	config := pricing.DefaultConfig(service.Kind)
	config.Overrides = map[string]pricing.ModelRates{"model_a": {Input: "1", Output: "4", CacheRead: "0.1", CacheWrite: "1.25"}}
	if err := s.SavePricingConfig(ctx, service.ID, config); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	model := "model_a"
	read, zero := 400000, 0
	duration := 1000
	first := 0
	for i, cache := range []*int{&read, &zero, nil} {
		r := contract.RequestRecord{ID: contract.RequestID("request_stats_" + string(rune('a'+i))), AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &model, StartedAt: start.Add(time.Duration(i) * time.Second), Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIChat, Audit: contract.NotCapturedAuditSummary(), Usage: &contract.Usage{InputTokens: 1000000, OutputTokens: 100000, TotalTokens: 1100000, CacheReadTokens: cache}, LatencyMs: &duration}
		if i == 0 {
			r.FirstTokenMs = &first
			r.Streaming = true
		}
		if err := s.UpsertRequestRecord(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	model = "unknown"
	read = 100
	r := contract.RequestRecord{ID: "request_stats_unknown", AttemptIndex: 1, ServiceID: &service.ID, RequestedModel: &model, StartedAt: start.Add(3 * time.Second), Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIChat, Audit: contract.NotCapturedAuditSummary(), Usage: &contract.Usage{InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100, CacheReadTokens: &read}, LatencyMs: &duration}
	if err := s.UpsertRequestRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	assert := func() {
		t.Helper()
		v, err := s.ServiceStatistics(ctx, service.ID, start, start.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if v.Records != 4 || v.Priced != 3 || v.Unpriced != 1 || v.AmountUSD != "3.840000000" || v.InputTokens != 3000000 || v.OutputTokens != 300000 || v.CostSamples != 3 || v.PerMillionUSD == nil || *v.PerMillionUSD != "1.163636364" || v.CacheSamples != 3 || v.CacheHits != 2 || v.CacheReadTokens != 400100 || v.CacheInputTokens != 2001000 || v.FirstTokenSamples != 1 || v.FirstTokenMS == nil || *v.FirstTokenMS != 0 || len(v.Models) != 2 {
			t.Fatalf("statistics=%+v", v)
		}
	}
	assert()
	config.Overrides["model_a"] = pricing.ModelRates{Input: "10", Output: "40", CacheRead: "1", CacheWrite: "12.5"}
	if err := s.SavePricingConfig(ctx, service.ID, config); err != nil {
		t.Fatal(err)
	}
	assert()
	if _, err := s.ServiceStatistics(ctx, service.ID, start, start.Add(32*24*time.Hour)); !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("invalid range error=%v", err)
	}
}

func TestServiceStatisticsRetryUsesActualChannelOnce(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "retry.db"))
	defer s.Close()
	a, b := pathTestService("service_stats_a"), pathTestService("service_stats_b")
	for _, service := range []contract.Service{a, b} {
		if _, err := s.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now().UTC().Truncate(time.Second)
	model := "x"
	r := contract.RequestRecord{ID: "request_stats_root", AttemptIndex: 1, ServiceID: &a.ID, RequestedModel: &model, StartedAt: start, Status: contract.RequestStatusFailed, InputProtocol: contract.ProtocolOpenAIChat, Audit: contract.NotCapturedAuditSummary()}
	if err := s.UpsertRequestRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	child := r
	child.ID = "request_stats_child"
	child.ParentRequestID = &r.ID
	if err := s.InsertRequestRecord(ctx, child); err != nil {
		t.Fatal(err)
	}
	r.AttemptIndex = 2
	r.ServiceID = &b.ID
	r.Status = contract.RequestStatusSucceeded
	if err := s.UpsertRequestRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	for _, id := range []contract.ServiceID{a.ID, b.ID} {
		v, err := s.ServiceStatistics(ctx, id, start, start.Add(time.Minute))
		if err != nil || v.Records != 1 {
			t.Fatalf("channel=%s stats=%+v err=%v", id, v, err)
		}
	}
}
