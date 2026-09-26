package sqlite

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestServicePerformanceUsesEligibleWeightedSamples(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "performance.db"))
	defer store.Close()
	from := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	service := contract.ServiceID("service_one")
	tokens, err := store.ListAccessTokens(t.Context())
	if err != nil || len(tokens) == 0 {
		t.Fatalf("tokens=%v err=%v", tokens, err)
	}
	base := contract.RequestRecord{ID: "request_base", StartedAt: from, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, ServiceID: &service, Streaming: true, LocalAccessTokenID: &tokens[0].ID,
		LatencyMs: ptrInt(2000), FirstTokenMs: ptrInt(500),
		Usage: &contract.Usage{InputTokens: 100, CacheReadTokens: ptrInt(80), OutputTokens: 150, TotalTokens: 250}}
	insert := func(id string, mutate func(*contract.RequestRecord)) {
		t.Helper()
		record := base
		record.ID = contract.RequestID("request_" + id)
		usage := *base.Usage
		record.Usage = &usage
		if mutate != nil {
			mutate(&record)
		}
		if err := store.InsertRequestRecord(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	insert("first", nil)
	insert("second", func(r *contract.RequestRecord) {
		r.Usage = &contract.Usage{InputTokens: 900, CacheReadTokens: ptrInt(180), OutputTokens: 90, TotalTokens: 990}
		r.LatencyMs, r.FirstTokenMs = ptrInt(4000), ptrInt(1000)
	})
	insert("burst", func(r *contract.RequestRecord) {
		r.Usage = &contract.Usage{OutputTokens: 5, TotalTokens: 5}
		r.LatencyMs, r.FirstTokenMs = ptrInt(1000), ptrInt(999)
	})
	insert("failed", func(r *contract.RequestRecord) { r.Status = contract.RequestStatusFailed })
	insert("http_error", func(r *contract.RequestRecord) { r.HTTPStatus = ptrInt(500) })
	insert("pending", func(r *contract.RequestRecord) { r.Status = contract.RequestStatusPending })
	insert("child", func(r *contract.RequestRecord) {
		id := contract.RequestID("request_first")
		r.ParentRequestID, r.AttemptIndex = &id, 1
	})
	insert("discovery", func(r *contract.RequestRecord) { r.InputProtocol = contract.ProtocolOpenAIModels })
	insert("before", func(r *contract.RequestRecord) { r.StartedAt = from.Add(-time.Nanosecond) })
	insert("after", func(r *contract.RequestRecord) { r.StartedAt = from.AddDate(0, 0, 1) })
	insert("incomplete", func(r *contract.RequestRecord) { r.Usage.BillingIncomplete = true })
	insert("no_usage", func(r *contract.RequestRecord) { r.Usage = nil })
	// Missing cache reporting, non-streaming, missing timing and zero output must
	// not dilute either metric. Explicit zero cache reads are still a sample.
	insert("no_timing", func(r *contract.RequestRecord) { r.FirstTokenMs = nil; r.Usage.CacheReadTokens = nil })
	insert("non_streaming", func(r *contract.RequestRecord) {
		r.Streaming = false
		r.FirstTokenMs = nil
		r.Usage.CacheReadTokens = nil
	})
	insert("no_output", func(r *contract.RequestRecord) { r.Usage.OutputTokens = 0; r.Usage.CacheReadTokens = nil })
	insert("zero_cache", func(r *contract.RequestRecord) {
		id := contract.ServiceID("service_two")
		r.ServiceID = &id
		r.Usage.CacheReadTokens, r.FirstTokenMs = ptrInt(0), nil
	})
	insert("unknown", func(r *contract.RequestRecord) {
		id := contract.ServiceID("service_unknown")
		r.ServiceID = &id
		r.Usage.CacheReadTokens, r.FirstTokenMs = nil, nil
	})
	insert("only_failure", func(r *contract.RequestRecord) {
		id := contract.ServiceID("service_failed")
		r.ServiceID = &id
		r.Status = contract.RequestStatusFailed
	})
	summary, err := store.GetUsageSummary(t.Context(), storage.UsageSummaryOptions{From: from, To: from.AddDate(0, 0, 1), TimeZone: "UTC", Bucket: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.ByToken) != 1 {
		t.Fatalf("token groups=%+v", summary.ByToken)
	}
	tokenStats := summary.ByToken[0].Performance
	if tokenStats == nil || tokenStats.CacheSamples != 3 || tokenStats.SpeedSamples != 3 ||
		tokenStats.CacheHitRate == nil || math.Abs(*tokenStats.CacheHitRate-260.0/1100) > 1e-9 ||
		tokenStats.OutputTokensPerSecond == nil || *tokenStats.OutputTokensPerSecond != 49 {
		t.Fatalf("token performance across providers=%+v", tokenStats)
	}
	groups := map[string]*storage.ServicePerformance{}
	for _, group := range summary.ByService {
		groups[*group.ID] = group.Performance
	}
	stats := groups[string(service)]
	if stats == nil || stats.CacheSamples != 2 || stats.SpeedSamples != 3 || stats.CacheHitRate == nil || math.Abs(*stats.CacheHitRate-0.26) > 1e-9 || stats.OutputTokensPerSecond == nil || *stats.OutputTokensPerSecond != 49 {
		t.Fatalf("unexpected weighted performance: %+v", stats)
	}
	zero := groups["service_two"]
	if zero.CacheHitRate == nil || *zero.CacheHitRate != 0 || zero.CacheSamples != 1 || zero.OutputTokensPerSecond != nil {
		t.Fatalf("explicit zero lost: %+v", zero)
	}
	for _, id := range []string{"service_unknown", "service_failed"} {
		if s := groups[id]; s == nil || s.CacheHitRate != nil || s.OutputTokensPerSecond != nil || s.CacheSamples != 0 || s.SpeedSamples != 0 {
			t.Fatalf("unknown sample invented: %+v", s)
		}
	}
}
