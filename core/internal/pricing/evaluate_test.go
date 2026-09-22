package pricing

import (
	"github.com/QuantumNous/astrlink/core/contract"
	"testing"
	"time"
)

func ptr(v int) *int { return &v }
func TestValuationDecimalCacheAndTiers(t *testing.T) {
	tests := []struct {
		name, expr string
		usage      contract.Usage
		want       string
	}{
		{"cache partitions", `tier("standard", p * 3 + cr * 0.3 + cc * 3.75 + cc1h * 6 + c * 15)`, contract.Usage{InputTokens: 1000000, OutputTokens: 100000, CacheReadTokens: ptr(400000), CacheWriteTokens: ptr(200000), CacheWrite1hTokens: ptr(50000)}, "3.682500000"},
		{"decimal", `tier("standard", p * 0.1 + c * 0.2)`, contract.Usage{InputTokens: 3, OutputTokens: 1}, "0.000000500"},
		{"threshold", `len <= 200000 ? tier("small",p * 1) : tier("large",p * 2)`, contract.Usage{InputTokens: 200000}, "0.200000000"},
		{"over threshold", `len <= 200000 ? tier("small",p * 1) : tier("large",p * 2)`, contract.Usage{InputTokens: 200001}, "0.400002000"},
		{"explicit free cache", `tier("standard",p * 1 + cr * 0)`, contract.Usage{InputTokens: 100, CacheReadTokens: ptr(90)}, "0.000010000"},
		{"omitted cache uses input", `tier("standard",p * 1)`, contract.Usage{InputTokens: 100, CacheReadTokens: ptr(90)}, "0.000100000"},
		{"audio", `tier("standard",p * 1 + ai * 10 + c * 2 + ao * 20)`, contract.Usage{InputTokens: 100, OutputTokens: 50, InputAudioTokens: ptr(10), OutputAudioTokens: ptr(20)}, "0.000650000"},
		{"time window", `hour("UTC") >= 1 && hour("UTC") < 4 ? tier("peak",p * 2) : tier("off",p * 1)`, contract.Usage{InputTokens: 100}, "0.000200000"},
	}
	at := time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Evaluate(tt.expr, &tt.usage, at)
			if err != nil || got.AmountUSD != tt.want {
				t.Fatalf("got=%+v err=%v want=%s", got, err, tt.want)
			}
		})
	}
	for _, expr := range []string{`map([1], {#})`, `foo(1)`, `p ** 2`, `p.bar`, `tier("x", p / 0)`} {
		if _, err := Evaluate(expr, &contract.Usage{}, at); err == nil {
			t.Fatalf("unsafe expression accepted: %s", expr)
		}
	}
	if _, err := Evaluate(`tier("x",p + cc + cc1h)`, &contract.Usage{InputTokens: 10, CacheWriteTokens: ptr(3)}, at); err == nil {
		t.Fatal("missing TTL treated as zero")
	}
	if _, err := Evaluate(`tier("x",p)`, nil, at); err == nil {
		t.Fatal("missing usage treated as free")
	}
	enabled := true
	got, err := Evaluate(`param("enable_thinking") == true ? tier("thinking",c * 4) : tier("normal",c)`, &contract.Usage{OutputTokens: 100, ThinkingEnabled: &enabled}, at)
	if err != nil || got.AmountUSD != "0.000400000" {
		t.Fatalf("thinking=%+v %v", got, err)
	}
}
func TestOfficialProviderAndMonth(t *testing.T) {
	for _, kind := range []contract.ServiceKind{"kimi_coding", "glm_coding", "minimax_coding"} {
		c := DefaultConfig(kind)
		if Providers[c.Provider] == "" {
			t.Fatal(c)
		}
		c.Provider += "-coding-plan"
		if c.Validate() == nil {
			t.Fatal("accepted plan")
		}
	}
	start, end := MonthBounds(time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC), 31, "UTC")
	if start.Day() != 28 || start.Month() != time.February || end.Day() != 31 {
		t.Fatalf("%v %v", start, end)
	}
}

func TestPayAsYouGoDefaultPricingProviders(t *testing.T) {
	for kind, provider := range map[contract.ServiceKind]string{"deepseek": "deepseek", "qwen": "alibaba", "moonshot": "moonshotai", "glm": "zai", "minimax": "minimax", "xai": "xai", "doubao": ""} {
		config := DefaultConfig(kind)
		if config.Provider != provider || config.Validate() != nil {
			t.Fatalf("%s: %#v", kind, config)
		}
	}
}
