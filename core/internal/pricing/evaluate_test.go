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

func TestAudioBreakdownRequiredOnlyWhenItChangesCost(t *testing.T) {
	const geminiFlash = `tier("standard", p * 0.75 + cr * 0.075 + ai * 0.75 + c * 3.75)`
	tests := []struct {
		name, expression string
		usage            *contract.Usage
		amount, reason   string
	}{
		{"gemini historical totals", geminiFlash, &contract.Usage{InputTokens: 1000000, OutputTokens: 100000, CacheReadTokens: ptr(400000)}, "0.855000000", ""},
		{"gemini known audio overlaps cache", geminiFlash, &contract.Usage{InputTokens: 1000000, OutputTokens: 100000, CacheReadTokens: ptr(400000), InputAudioTokens: ptr(800000)}, "0.855000000", ""},
		{"different audio rate stays unknown", `tier("standard", p * 1 + ai * 10)`, &contract.Usage{InputTokens: 100}, "", "missing_audio_usage"},
		{"known text only with different rates", `tier("standard", p * 1 + ai * 10)`, &contract.Usage{InputTokens: 100, InputAudioTokens: ptr(0)}, "0.000100000", ""},
		{"known audio with different rates", `tier("standard", p * 1 + ai * 10)`, &contract.Usage{InputTokens: 100, InputAudioTokens: ptr(20)}, "0.000280000", ""},
		{"unknown cache audio intersection", `tier("standard", p * 1 + cr * 0.1 + ai * 10)`, &contract.Usage{InputTokens: 100, CacheReadTokens: ptr(10), InputAudioTokens: ptr(20)}, "", "missing_audio_cache_partition"},
		{"equal output audio rates", `tier("standard", c * 2 + ao * 2)`, &contract.Usage{OutputTokens: 100}, "0.000200000", ""},
		{"different output audio rates", `tier("standard", c * 2 + ao * 20)`, &contract.Usage{OutputTokens: 100}, "", "missing_audio_usage"},
		{"zero tokens prove zero audio", `tier("standard", p + ai * 10 + c + ao * 20)`, &contract.Usage{}, "0.000000000", ""},
		{"context tiers", `len <= 200000 ? tier("small", p * 0.75 + ai * 0.75) : tier("large", p * 1.5 + ai * 1.5)`, &contract.Usage{InputTokens: 300000}, "0.450000000", ""},
		{"decimal coefficients are exact", `tier("standard", 0.1 * p + 0.2 * p + ai * 0.3)`, &contract.Usage{InputTokens: 1000000}, "0.300000000", ""},
		{"division", `tier("standard", (p + ai) / 4)`, &contract.Usage{InputTokens: 1000000}, "0.250000000", ""},
		{"audio-dependent tier", `ai > 0 ? tier("audio", p + ai) : tier("text", p + ai)`, &contract.Usage{InputTokens: 100}, "", "missing_audio_usage"},
		{"audio-dependent tier label", `tier(ai > 0 ? "audio" : "text", p + ai)`, &contract.Usage{InputTokens: 100}, "", "missing_audio_usage"},
		{"text-dependent tier", `p > 50 ? tier("large", p + ai) : tier("small", p + ai)`, &contract.Usage{InputTokens: 100}, "", "missing_audio_usage"},
		{"nonlinear surcharge", `tier("standard", p + ai + p * ai)`, &contract.Usage{InputTokens: 100}, "", "missing_audio_usage"},
		{"invalid audio count", geminiFlash, &contract.Usage{InputTokens: 100, InputAudioTokens: ptr(101)}, "", "invalid_token_partition"},
		{"incomplete stream", geminiFlash, &contract.Usage{InputTokens: 100, BillingIncomplete: true}, "", "incomplete_usage"},
		{"missing usage", geminiFlash, nil, "", "missing_usage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Evaluate(tt.expression, tt.usage, time.Now())
			if tt.reason != "" {
				if err == nil || err.Error() != tt.reason {
					t.Fatalf("got=%+v error=%v want error=%s", got, err, tt.reason)
				}
			} else if err != nil || got.AmountUSD != tt.amount {
				t.Fatalf("got=%+v error=%v want=%s", got, err, tt.amount)
			}
		})
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
