package ui

import "testing"

func TestTodayFromAUsageSummary(t *testing.T) {
	summary := obj(t, `{
		"totals": {"requests": 4, "failed_requests": 0, "input_tokens": 500, "output_tokens": 100,
		           "total_tokens": 600, "cache_read_tokens": 0, "cache_write_tokens": 0},
		"by_hour": [{"date": "2026-11-16", "hour": 9, "total_tokens": 250},
		            {"date": "2026-11-16", "hour": 14, "total_tokens": 350}],
		"by_model": [{"id": "claude-opus-5-5", "requests": 3, "total_tokens": 550}],
		"by_client": [{"id": "claude_code", "requests": 3, "total_tokens": 550},
		              {"id": null, "requests": 1, "total_tokens": 50}]
	}`)
	today := todayFrom(summary)
	if today == nil || today.Tokens != 600 || len(today.Hours) != 24 || today.Hours[14] != 350 {
		t.Fatalf("today = %+v", today)
	}
	if today.Model == nil || *today.Model != "claude-opus-5-5" || today.Client == nil || *today.Client != "claude_code" {
		t.Fatalf("model %v, client %v", today.Model, today.Client)
	}
	// Unknown clients led: no client is named the busiest.
	led := obj(t, `{"totals": {"requests": 1, "failed_requests": 0, "input_tokens": 1, "output_tokens": 1,
		"total_tokens": 9, "cache_read_tokens": 0, "cache_write_tokens": 0},
		"by_client": [{"id": null, "requests": 1, "total_tokens": 8}, {"id": "codex", "requests": 1, "total_tokens": 1}]}`)
	if got := todayFrom(led); got == nil || got.Client != nil || got.Model != nil || len(got.Hours) != 0 {
		t.Fatalf("led by unknown = %+v", got)
	}
	if todayFrom(obj(t, `{}`)) != nil {
		t.Fatal("a summary without totals has no day")
	}
}
