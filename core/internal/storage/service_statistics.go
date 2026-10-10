package storage

import "time"

type StatisticsTotals struct {
	Records           int64    `json:"records"`
	Priced            int64    `json:"priced"`
	Unpriced          int64    `json:"unpriced"`
	AmountUSD         string   `json:"amount_usd"`
	PerMillionUSD     *string  `json:"per_million_usd"`
	CostSamples       int64    `json:"cost_samples"`
	InputTokens       int64    `json:"input_tokens"`
	OutputTokens      int64    `json:"output_tokens"`
	CacheSamples      int64    `json:"cache_samples"`
	CacheHits         int64    `json:"cache_hits"`
	CacheReadTokens   int64    `json:"cache_read_tokens"`
	CacheInputTokens  int64    `json:"cache_input_tokens"`
	FirstTokenMS      *float64 `json:"first_token_ms"`
	FirstTokenSamples int64    `json:"first_token_samples"`
	DurationMS        *float64 `json:"duration_ms"`
	DurationSamples   int64    `json:"duration_samples"`
	TPS               *float64 `json:"tps"`
	TPSSamples        int64    `json:"tps_samples"`
}
type StatisticsModel struct {
	Model string `json:"model"`
	StatisticsTotals
}
type ServiceStatistics struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	StatisticsTotals
	Models []StatisticsModel `json:"models"`
}
