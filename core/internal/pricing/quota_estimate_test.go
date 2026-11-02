package pricing

import (
	"testing"
	"time"
)

func TestDistinctQuotaWindowsKeepsTheLatestReadOfEachWindow(t *testing.T) {
	at := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	window := func(start, end, observed time.Duration, used float64) QuotaSample {
		return QuotaSample{Start: at.Add(start), End: at.Add(end), ObservedAt: at.Add(observed), UsedPercent: used}
	}
	got := DistinctQuotaWindows([]QuotaSample{
		window(0, 5*time.Hour, time.Hour, 10),
		// The same window read again with a reset a few milliseconds later.
		window(time.Millisecond, 5*time.Hour+time.Millisecond, 4*time.Hour, 30),
		// A window reported before its first call, then the real one.
		window(5*time.Hour, 10*time.Hour, 5*time.Hour, 0),
		window(6*time.Hour, 11*time.Hour, 7*time.Hour, 5),
		// A window a reset credit ended early, then the one after it.
		window(12*time.Hour, 13*time.Hour, 12*time.Hour+time.Minute, 50),
		window(13*time.Hour, 18*time.Hour, 14*time.Hour, 20),
	})
	want := []float64{20, 50, 5, 30}
	if len(got) != len(want) {
		t.Fatalf("windows = %+v", got)
	}
	for i, used := range want {
		if got[i].UsedPercent != used {
			t.Fatalf("windows[%d] = %+v, want used %v", i, got[i], used)
		}
	}
}

func TestEstimateWindowTotalDividesCostByShareUsed(t *testing.T) {
	sample := func(amount, used float64, priced, unpriced int64) QuotaSample {
		return QuotaSample{AmountUSD: amount, UsedPercent: used, Priced: priced, Unpriced: unpriced}
	}
	for _, test := range []struct {
		name    string
		samples []QuotaSample
		want    string
	}{
		{"one window", []QuotaSample{sample(12, 30, 4, 0)}, "40.00"},
		{"windows pool by usage", []QuotaSample{sample(12, 30, 4, 0), sample(2, 10, 1, 0)}, "35.00"},
		{"unpriced calls leave a window out", []QuotaSample{sample(12, 30, 4, 0), sample(1, 40, 1, 1)}, "40.00"},
		{"a window used elsewhere is left out", []QuotaSample{sample(12, 30, 4, 0), sample(0, 60, 0, 0)}, "40.00"},
		{"a share reported as zero is left out", []QuotaSample{sample(12, 30, 4, 0), sample(3, 0, 2, 0)}, "40.00"},
		{"too little of a window", []QuotaSample{sample(1, 9.5, 2, 0)}, ""},
		{"nothing priced", []QuotaSample{sample(0, 50, 3, 0)}, ""},
		{"no windows", nil, ""},
	} {
		got, ok := EstimateWindowTotal(test.samples)
		if got != test.want || ok != (test.want != "") {
			t.Fatalf("%s: EstimateWindowTotal() = %q, %v; want %q", test.name, got, ok, test.want)
		}
	}
}
