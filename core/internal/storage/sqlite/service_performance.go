package sqlite

import (
	"database/sql"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type servicePerformance struct {
	cacheInput, cacheRead, output, generationMs int64
	cacheSamples, speedSamples                  int64
}

func (stats *servicePerformance) observe(usage contract.Usage, streaming bool, latency, first sql.NullInt64) {
	if usage.BillingIncomplete {
		return
	}
	if usage.InputTokens > 0 && usage.CacheReadTokens != nil && *usage.CacheReadTokens <= usage.InputTokens {
		stats.cacheInput += int64(usage.InputTokens)
		stats.cacheRead += int64(*usage.CacheReadTokens)
		stats.cacheSamples++
	}
	if streaming && latency.Valid && first.Valid && first.Int64 >= 0 && latency.Int64 >= first.Int64 && usage.OutputTokens > 0 {
		stats.output += int64(usage.OutputTokens)
		stats.generationMs += max(latency.Int64-first.Int64, minGenerationMs)
		stats.speedSamples++
	}
}

func (stats *servicePerformance) summary() *storage.ServicePerformance {
	result := &storage.ServicePerformance{}
	if stats == nil {
		return result
	}
	result.CacheSamples, result.SpeedSamples = stats.cacheSamples, stats.speedSamples
	if stats.cacheInput > 0 {
		rate := float64(stats.cacheRead) / float64(stats.cacheInput)
		result.CacheHitRate = &rate
	}
	if stats.generationMs > 0 {
		rate := float64(stats.output) * 1000 / float64(stats.generationMs)
		result.OutputTokensPerSecond = &rate
	}
	return result
}
