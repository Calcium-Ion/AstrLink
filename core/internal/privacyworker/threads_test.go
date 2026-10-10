package privacyworker

import "testing"

func TestIntraThreadsFollowsDeviceCores(t *testing.T) {
	for _, test := range []struct {
		name        string
		topCores    int
		logicalCPUs int
		want        int
	}{
		{name: "M5 Pro", topCores: 6, logicalCPUs: 18, want: 4},
		{name: "M1 base", topCores: 4, logicalCPUs: 8, want: 2},
		{name: "16-core 32-thread desktop", topCores: 16, logicalCPUs: 32, want: 4},
		{name: "logical quarter binds", topCores: 8, logicalCPUs: 12, want: 3},
		{name: "top tier binds", topCores: 3, logicalCPUs: 64, want: 3},
		{name: "floor for a dual core", topCores: 1, logicalCPUs: 2, want: 2},
		{name: "floor for a single CPU", topCores: 0, logicalCPUs: 1, want: 2},
		{name: "ceiling for a large server", topCores: 64, logicalCPUs: 256, want: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := intraThreads(test.topCores, test.logicalCPUs); got != test.want {
				t.Fatalf(
					"intraThreads(%d, %d) = %d, want %d",
					test.topCores,
					test.logicalCPUs,
					got,
					test.want,
				)
			}
		})
	}
}

func TestHostIntraThreadsStaysWithinBounds(t *testing.T) {
	if threads := hostIntraThreads(); threads < minIntraThreads || threads > maxIntraThreads {
		t.Fatalf("hostIntraThreads() = %d, want %d..%d", threads, minIntraThreads, maxIntraThreads)
	}
}
