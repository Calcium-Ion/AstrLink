package pricing

import "fmt"

// ModelRates are USD per million tokens. The existing rational evaluator
// snapshots these expressions for each attempt, just like catalog prices.
type ModelRates struct {
	Input      string `json:"input"`
	Output     string `json:"output"`
	CacheRead  string `json:"cache_read"`
	CacheWrite string `json:"cache_write"`
}

func (r ModelRates) Price(model string) Price {
	return Price{Provider: "custom", Model: model, Name: model, Expression: fmt.Sprintf("p * %s + c * %s + cr * %s + cc * %s", r.Input, r.Output, r.CacheRead, r.CacheWrite)}
}
