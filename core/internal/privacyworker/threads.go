package privacyworker

import "runtime"

// Four intra-op threads scan about 1.7 times as fast as two. Eight add only a
// third more speed for twice the cores, which the IDE and coding agents
// running next to AstrLink need.
const (
	minIntraThreads = 2
	maxIntraThreads = 4
)

// intraThreads keeps the worker on the fastest core tier, since every
// operator waits for its slowest thread, and leaves three quarters of the
// logical CPUs to the rest of the machine.
func intraThreads(topCores, logicalCPUs int) int {
	return min(max(min(topCores, logicalCPUs/4), minIntraThreads), maxIntraThreads)
}

func hostIntraThreads() int {
	logicalCPUs := runtime.NumCPU()
	return intraThreads(topCores(logicalCPUs), logicalCPUs)
}
