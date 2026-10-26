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
	// Without the 5-minute/1-hour split every cache write bills at the 5-minute rate.
	got, err := Evaluate(`tier("standard", p * 3 + cc * 3.75 + cc1h * 6)`, &contract.Usage{InputTokens: 1000, CacheWriteTokens: ptr(400)}, at)
	if err != nil || got.AmountUSD != "0.003300000" {
		t.Fatalf("unsplit cache write=%+v %v", got, err)
	}
	if _, err := Evaluate(`tier("x",p)`, nil, at); err == nil {
		t.Fatal("missing usage treated as free")
	}
	const thinking = `param("enable_thinking") == true ? tier("thinking",c * 4) : tier("normal",c)`
	enabled := true
	got, err = Evaluate(thinking, &contract.Usage{OutputTokens: 100, ThinkingEnabled: &enabled}, at)
	if err != nil || got.AmountUSD != "0.000400000" {
		t.Fatalf("thinking=%+v %v", got, err)
	}
	// A request that did not say whether thinking was on takes the standard tier.
	got, err = Evaluate(thinking, &contract.Usage{OutputTokens: 100}, at)
	if err != nil || got.AmountUSD != "0.000100000" || got.Tier != "normal" {
		t.Fatalf("unknown thinking=%+v %v", got, err)
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

func TestCodexAutoReviewPriceBinding(t *testing.T) {
	luna := Price{Provider: "openai", Model: "gpt-5.6-luna", Expression: `tier("standard",p * 0.2 + c * 1.2)`}
	override := Price{Provider: "anthropic", Model: "custom", Expression: `tier("standard",p)`}
	for _, tt := range []struct {
		name   string
		config Config
		prices []Price
		want   Price
		ok     bool
	}{
		{"codex default", DefaultConfig(contract.ServiceKindCodexSubscription), []Price{luna}, luna, true},
		{"openai default", DefaultConfig(contract.ServiceKindOpenAI), []Price{luna}, luna, true},
		{"unspecified provider", Config{}, []Price{luna}, luna, true},
		{"other provider", Config{Provider: "anthropic"}, []Price{luna}, Price{}, false},
		{"missing luna", Config{Provider: "openai"}, nil, Price{}, false},
		{"explicit binding", Config{Provider: "openai", Bindings: map[string]Binding{"codex-auto-review": {Provider: override.Provider, Model: override.Model}}}, []Price{luna, override}, override, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.config.Resolve("codex-auto-review", tt.prices)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("price=%+v matched=%t, want %+v matched=%t", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestIncompleteUsageIsValued(t *testing.T) {
	const geminiFlash = `tier("standard", p * 0.75 + cr * 0.075 + ai * 0.75 + c * 3.75)`
	tests := []struct {
		name, expression string
		usage            *contract.Usage
		amount, reason   string
	}{
		{"gemini totals", geminiFlash, &contract.Usage{InputTokens: 1000000, OutputTokens: 100000, CacheReadTokens: ptr(400000)}, "0.855000000", ""},
		{"gemini known audio overlaps cache", geminiFlash, &contract.Usage{InputTokens: 1000000, OutputTokens: 100000, CacheReadTokens: ptr(400000), InputAudioTokens: ptr(800000)}, "0.855000000", ""},
		{"unreported audio bills as text", `tier("standard", p * 1 + ai * 10)`, &contract.Usage{InputTokens: 100}, "0.000100000", ""},
		{"known text only with different rates", `tier("standard", p * 1 + ai * 10)`, &contract.Usage{InputTokens: 100, InputAudioTokens: ptr(0)}, "0.000100000", ""},
		{"known audio with different rates", `tier("standard", p * 1 + ai * 10)`, &contract.Usage{InputTokens: 100, InputAudioTokens: ptr(20)}, "0.000280000", ""},
		{"cache hits count as text", `tier("standard", p * 1 + cr * 0.1 + ai * 10)`, &contract.Usage{InputTokens: 100, CacheReadTokens: ptr(10), InputAudioTokens: ptr(20)}, "0.000271000", ""},
		{"audio is cut to the uncached input", `tier("standard", p * 1 + cr * 0.1 + ai * 10)`, &contract.Usage{InputTokens: 100, CacheReadTokens: ptr(90), InputAudioTokens: ptr(20)}, "0.000109000", ""},
		{"unreported output audio bills as text", `tier("standard", c * 2 + ao * 20)`, &contract.Usage{OutputTokens: 100}, "0.000200000", ""},
		{"zero tokens", `tier("standard", p + ai * 10 + c + ao * 20)`, &contract.Usage{}, "0.000000000", ""},
		{"context tiers", `len <= 200000 ? tier("small", p * 0.75 + ai * 0.75) : tier("large", p * 1.5 + ai * 1.5)`, &contract.Usage{InputTokens: 300000}, "0.450000000", ""},
		{"decimal coefficients are exact", `tier("standard", 0.1 * p + 0.2 * p + ai * 0.3)`, &contract.Usage{InputTokens: 1000000}, "0.300000000", ""},
		{"division", `tier("standard", (p + ai) / 4)`, &contract.Usage{InputTokens: 1000000}, "0.250000000", ""},
		{"audio-dependent tier", `ai > 0 ? tier("audio", p * 2) : tier("text", p)`, &contract.Usage{InputTokens: 100}, "0.000100000", ""},
		{"audio larger than input is cut", geminiFlash, &contract.Usage{InputTokens: 100, InputAudioTokens: ptr(101)}, "0.000075000", ""},
		{"cache larger than input is cut", `tier("standard", p * 1 + cr * 0.1 + cc * 2)`, &contract.Usage{InputTokens: 100, CacheReadTokens: ptr(80), CacheWriteTokens: ptr(50)}, "0.000048000", ""},
		{"interrupted stream bills partial usage", geminiFlash, &contract.Usage{InputTokens: 100, BillingIncomplete: true}, "0.000075000", ""},
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

func TestOpenAITwelveHourCacheWrites(t *testing.T) {
	// gpt-6-astra as LLM Metadata published it on 2026-10-25.
	const astra = `len <= 272000 ? tier("0_272k", p * 10 + cr * 1 + cc * 12.5 + cc12h * 25 + c * 50) : tier("272k_plus", p * 20 + cr * 2 + cc * 25 + cc12h * 50 + c * 75)`
	if err := ValidateExpression(astra); err != nil {
		t.Fatalf("published price rejected: %v", err)
	}
	tests := []struct {
		name, expression string
		usage            contract.Usage
		want             string
	}{
		// Counts of a gpt-6-astra request AstrLink 0.2.18 left unpriced.
		{"no cache writes", astra, contract.Usage{InputTokens: 126800, OutputTokens: 4997, CacheReadTokens: ptr(126592)}, "0.378522000"},
		{"ttl split", astra, contract.Usage{InputTokens: 100000, OutputTokens: 1000, CacheReadTokens: ptr(40000), CacheWriteTokens: ptr(30000), CacheWrite12hTokens: ptr(10000)}, "0.890000000"},
		{"unsplit writes bill short-lived", astra, contract.Usage{InputTokens: 100000, OutputTokens: 1000, CacheReadTokens: ptr(40000), CacheWriteTokens: ptr(30000)}, "0.765000000"},
		{"long tier", astra, contract.Usage{InputTokens: 300000, CacheWriteTokens: ptr(100000), CacheWrite12hTokens: ptr(100000)}, "9.000000000"},
		{"12h without its price bills short-lived", `tier("standard", p * 10 + cc * 12.5)`, contract.Usage{InputTokens: 1000, CacheWriteTokens: ptr(400), CacheWrite12hTokens: ptr(100)}, "0.011000000"},
		{"12h larger than writes is cut", astra, contract.Usage{InputTokens: 1000, CacheWriteTokens: ptr(100), CacheWrite12hTokens: ptr(500)}, "0.011500000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Evaluate(tt.expression, &tt.usage, time.Now())
			if err != nil || got.AmountUSD != tt.want {
				t.Fatalf("got=%+v err=%v want=%s", got, err, tt.want)
			}
		})
	}
}
