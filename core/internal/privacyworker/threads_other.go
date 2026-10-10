//go:build !darwin

package privacyworker

// topCores approximates physical cores as half the logical CPUs, which holds
// for two-way SMT.
func topCores(logicalCPUs int) int {
	return logicalCPUs / 2
}
