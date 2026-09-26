// Package pricing values gateway calls at first-party API list prices. These
// reference amounts are independent of a subscription provider's quota debit.
package pricing

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const SourceURL = "https://basellm.github.io/llm-metadata/api"

// Plan/region catalogs are deliberately not fallback price sources. A service
// selects one canonical first-party supplier; explicit model bindings can name
// other canonical suppliers for multi-vendor services such as OpenCode.
var Providers = map[string]string{
	"openai": "OpenAI", "anthropic": "Anthropic", "moonshotai": "Moonshot AI",
	"zai": "Z.AI", "minimax": "MiniMax", "google": "Google", "deepseek": "DeepSeek",
	"alibaba": "Alibaba", "xai": "xAI", "mistral": "Mistral", "cohere": "Cohere",
	"meta": "Meta", "perplexity": "Perplexity", "stepfun": "StepFun", "inception": "Inception",
	"longcat": "LongCat", "bailing": "Bailing", "morph": "Morph", "nvidia": "NVIDIA",
}

type Price struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Name       string `json:"name"`
	Expression string `json:"expression"`
}
type Catalog struct {
	Version     string    `json:"version"`
	GeneratedAt time.Time `json:"generated_at"`
	ActivatedAt time.Time `json:"activated_at"`
	ETag        string    `json:"etag"`
	Prices      []Price   `json:"prices"`
	Warnings    []string  `json:"warnings"`
}
type Status struct {
	Source    string     `json:"source"`
	Version   string     `json:"version"`
	UpdatedAt *time.Time `json:"updated_at"`
	CheckedAt *time.Time `json:"checked_at"`
	Error     string     `json:"error"`
	Models    int        `json:"models"`
	Warnings  []string   `json:"warnings"`
}
type Binding struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}
type Config struct {
	Overrides        map[string]ModelRates `json:"overrides,omitempty"`
	Provider         string                `json:"provider"`
	Bindings         map[string]Binding    `json:"bindings"`
	MonthlyBudgetUSD string                `json:"monthly_budget_usd"`
	BillingDay       int                   `json:"billing_day"`
	TimeZone         string                `json:"time_zone"`
}

func DefaultConfig(kind contract.ServiceKind) Config {
	provider := map[contract.ServiceKind]string{"openai": "openai", "anthropic": "anthropic", "gemini": "google",
		"codex_subscription": "openai", "claude_subscription": "anthropic", "grok_subscription": "xai", "kimi_coding": "moonshotai", "glm_coding": "zai", "minimax_coding": "minimax",
		"deepseek": "deepseek", "qwen": "alibaba", "moonshot": "moonshotai", "glm": "zai", "minimax": "minimax", "xai": "xai"}[kind]
	return Config{Provider: provider, Bindings: map[string]Binding{}, BillingDay: 1, TimeZone: "UTC"}
}

var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,9})(\.[0-9]{1,9})?$`)

func ParseUSD(s string) (*big.Rat, error) {
	if !decimalPattern.MatchString(s) {
		return nil, fmt.Errorf("invalid USD amount")
	}
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid USD amount")
	}
	return v, nil
}
func (c Config) Validate() error {
	if len(c.Overrides) > 1000 {
		return fmt.Errorf("too many model prices")
	}
	for model, rates := range c.Overrides {
		if model == "" || len(model) > 256 {
			return fmt.Errorf("invalid price model")
		}
		for _, value := range []string{rates.Input, rates.Output, rates.CacheRead, rates.CacheWrite} {
			if _, err := ParseUSD(value); err != nil {
				return fmt.Errorf("invalid model price: %s", model)
			}
		}
	}
	if c.Provider != "" && Providers[c.Provider] == "" {
		return fmt.Errorf("select a canonical official provider")
	}
	if c.BillingDay < 1 || c.BillingDay > 31 {
		return fmt.Errorf("billing_day must be 1–31")
	}
	if c.TimeZone == "" || c.TimeZone == "Local" {
		return fmt.Errorf("an IANA time zone is required")
	}
	if _, e := time.LoadLocation(c.TimeZone); e != nil {
		return fmt.Errorf("invalid time zone")
	}
	if c.MonthlyBudgetUSD != "" {
		if _, e := ParseUSD(c.MonthlyBudgetUSD); e != nil {
			return e
		}
	}
	if len(c.Bindings) > 1000 {
		return fmt.Errorf("too many bindings")
	}
	for model, b := range c.Bindings {
		if len(model) == 0 || len(model) > 256 || len(b.Model) == 0 || len(b.Model) > 256 || Providers[b.Provider] == "" {
			return fmt.Errorf("invalid official model binding")
		}
	}
	return nil
}
func (c Config) Resolve(model string, prices []Price) (Price, bool) {
	if rates, ok := c.Overrides[model]; ok {
		return rates.Price(model), true
	}
	b, ok := c.Bindings[model]
	if !ok {
		b = Binding{Provider: c.Provider, Model: model}
	}
	var match Price
	count := 0
	for _, p := range prices {
		if p.Model == b.Model && (b.Provider == p.Provider || b.Provider == "") {
			match = p
			count++
		}
	}
	return match, count == 1
}
func AccountKey(service contract.Service) string {
	id := string(service.ID)
	if service.Subscription != nil {
		id += "/" + string(service.Subscription.Provider) + "/" + service.Subscription.ProviderAccountID
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(id)))
}

type BillingSummaryOptions struct {
	IncludeTokenBreakdown bool
}

type Amounts struct {
	AmountUSD string `json:"amount_usd"`
	Priced    int64  `json:"priced"`
	Unpriced  int64  `json:"unpriced"`
	Pending   int64  `json:"pending"`
	Revalued  int64  `json:"revalued"`
	Requests  int64  `json:"requests"`
}
type Group struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Amounts
}
type TokenGroup struct {
	TokenID string `json:"token_id"`
	Amounts
}
type Summary struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	Amounts
	ByModel []Group      `json:"by_model"`
	ByToken []TokenGroup `json:"by_token"`
}
type Period struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	Start        time.Time  `json:"start"`
	End          time.Time  `json:"end"`
	ObservedAt   *time.Time `json:"observed_at"`
	UsedPercent  *float64   `json:"used_percent"`
	BudgetUSD    string     `json:"budget_usd"`
	RemainingUSD string     `json:"remaining_usd"`
	Coverage     string     `json:"coverage"`
	Summary      Summary    `json:"summary"`
}
type ServiceReport struct {
	Config  Config   `json:"config"`
	Periods []Period `json:"periods"`
}

func MonthBounds(now time.Time, day int, zone string) (time.Time, time.Time) {
	loc, _ := time.LoadLocation(zone)
	if loc == nil {
		loc = time.UTC
	}
	now = now.In(loc)
	boundary := func(y int, m time.Month) time.Time {
		last := time.Date(y, m+1, 0, 0, 0, 0, 0, loc).Day()
		return time.Date(y, m, min(day, last), 0, 0, 0, 0, loc)
	}
	start := boundary(now.Year(), now.Month())
	if start.After(now) {
		prev := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, -1, 0)
		start = boundary(prev.Year(), prev.Month())
	}
	next := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
	return start.UTC(), boundary(next.Year(), next.Month()).UTC()
}
func Digest(parts ...string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(parts, "\n"))))
}
