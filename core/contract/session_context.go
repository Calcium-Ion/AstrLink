package contract

import (
	"fmt"
	"math"
	"regexp"
)

// SessionUsage sums the tokens of every call of a conversation, retries
// included, in the Usage accounting: input counts cache reads and writes.
type SessionUsage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	// CacheHitRatio is cache reads over the input of the calls that reported
	// their cache use; nil when none did.
	CacheHitRatio *float64 `json:"cache_hit_ratio,omitempty"`
}

func (usage SessionUsage) Validate() error {
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheReadTokens < 0 || usage.CacheWriteTokens < 0 {
		return fmt.Errorf("session usage token counts must be non-negative")
	}
	if ratio := usage.CacheHitRatio; ratio != nil && (math.IsNaN(*ratio) || *ratio < 0 || *ratio > 1) {
		return fmt.Errorf("cache_hit_ratio must be between 0 and 1")
	}
	return nil
}

// usdAmountPattern is a non-negative decimal amount with up to nine
// fraction digits, the precision the billing ledger keeps.
var usdAmountPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,9})?$`)

func validateUSDAmount(field, value string) error {
	if !usdAmountPattern.MatchString(value) {
		return fmt.Errorf("%s must be a non-negative decimal amount", field)
	}
	return nil
}

// SessionCost values a conversation's calls at official API prices, as the
// billing ledger records them. AmountUSD sums the priced attempts; the counts
// say how many attempts were priced, could not be priced, or still run.
type SessionCost struct {
	AmountUSD string `json:"amount_usd"`
	Priced    int    `json:"priced"`
	Unpriced  int    `json:"unpriced"`
	Pending   int    `json:"pending"`
}

func (cost SessionCost) Validate() error {
	if err := validateUSDAmount("amount_usd", cost.AmountUSD); err != nil {
		return err
	}
	if cost.Priced < 0 || cost.Unpriced < 0 || cost.Pending < 0 {
		return fmt.Errorf("session cost counts must be non-negative")
	}
	return nil
}

// SessionContext follows what a conversation sent the model call by call:
// the context each main call carried, where the client compacted it, and
// where a provider's prompt cache missed. It is derived when the detail is
// read and never stored.
type SessionContext struct {
	// Calls are the main calls with usage, oldest first, each with the
	// context it sent. Helper calls such as a title are left out.
	Calls []ContextCall `json:"calls"`
	// Compactions are where the context was compacted, oldest first.
	Compactions []ContextCompaction `json:"compactions"`
	// CacheMisses are main calls that rewrote a prompt cache the previous
	// main call had built, oldest first. The rebuild after a compaction is
	// expected and not listed.
	CacheMisses []CacheMiss `json:"cache_misses"`
}

// ContextCall is the context one main call sent: its input tokens, cache
// reads and writes included.
type ContextCall struct {
	RequestID RequestID `json:"request_id"`
	Tokens    int       `json:"tokens"`
}

// ContextCompaction is one compaction of a conversation's context.
type ContextCompaction struct {
	// RequestID is the first main call after the compaction, the one whose
	// context dropped.
	RequestID RequestID `json:"request_id"`
	// SummaryRequestID is the call that wrote the summary, when the client
	// marked one.
	SummaryRequestID *RequestID `json:"summary_request_id,omitempty"`
	// BeforeTokens is the context of the main call before the compaction,
	// AfterTokens that of RequestID.
	BeforeTokens int `json:"before_tokens"`
	AfterTokens  int `json:"after_tokens"`
	// Marked is set when the client marked the compaction in what it sent;
	// otherwise the drop in context showed it.
	Marked bool `json:"marked"`
}

// CacheMissReason is the first explanation that holds for a cache miss.
type CacheMissReason string

const (
	// CacheMissProviderChanged: the call went to another provider or
	// subscription account, which keeps its own cache.
	CacheMissProviderChanged CacheMissReason = "provider_changed"
	// CacheMissModelChanged: the call asked another model.
	CacheMissModelChanged CacheMissReason = "model_changed"
	// CacheMissExpired: the gap since the previous call outlasted the cache.
	CacheMissExpired CacheMissReason = "expired"
	// CacheMissPrefixChanged: the system prompt, the tool definitions or a
	// rules or memory file changed since the previous call.
	CacheMissPrefixChanged CacheMissReason = "prefix_changed"
	// CacheMissUnknown: none of the above holds.
	CacheMissUnknown CacheMissReason = "unknown"
)

func (reason CacheMissReason) Valid() bool {
	switch reason {
	case CacheMissProviderChanged, CacheMissModelChanged, CacheMissExpired, CacheMissPrefixChanged, CacheMissUnknown:
		return true
	default:
		return false
	}
}

// CacheMiss is a main call that read less than half of the prompt cache the
// previous main call left.
type CacheMiss struct {
	RequestID RequestID       `json:"request_id"`
	Reason    CacheMissReason `json:"reason"`
	// Changed names what changed for prefix_changed: "system", "tools", or
	// the rules or memory file, such as AGENTS.md.
	Changed string `json:"changed,omitempty"`
	// IdleMs is the gap before the call and TTLMs the cache lifetime it
	// outlasted, for expired.
	IdleMs *int64 `json:"idle_ms,omitempty"`
	TTLMs  *int64 `json:"ttl_ms,omitempty"`
	// RewrittenTokens is the cached prefix the call had to send again.
	RewrittenTokens int `json:"rewritten_tokens"`
	// ExtraUSD is what the rewrite cost over a cache hit at official prices;
	// nil when the call has no price.
	ExtraUSD *string `json:"extra_usd,omitempty"`
}

// MaxContextChangedRunes bounds CacheMiss.Changed, a file name or a part.
const MaxContextChangedRunes = 128

func (miss CacheMiss) Validate() error {
	if err := miss.RequestID.Validate(); err != nil {
		return err
	}
	if !miss.Reason.Valid() {
		return fmt.Errorf("unknown cache miss reason %q", miss.Reason)
	}
	if err := validateBoundedText("changed", miss.Changed, MaxContextChangedRunes, true); err != nil {
		return err
	}
	if (miss.IdleMs != nil && *miss.IdleMs < 0) || (miss.TTLMs != nil && *miss.TTLMs < 0) {
		return fmt.Errorf("cache miss durations must be non-negative")
	}
	if miss.RewrittenTokens < 0 {
		return fmt.Errorf("rewritten_tokens must be non-negative")
	}
	if miss.ExtraUSD != nil {
		return validateUSDAmount("extra_usd", *miss.ExtraUSD)
	}
	return nil
}

func (context SessionContext) Validate() error {
	for index, call := range context.Calls {
		if err := call.RequestID.Validate(); err != nil {
			return fmt.Errorf("calls[%d]: %w", index, err)
		}
		if call.Tokens < 0 {
			return fmt.Errorf("calls[%d]: tokens must be non-negative", index)
		}
	}
	for index, compaction := range context.Compactions {
		if err := compaction.RequestID.Validate(); err != nil {
			return fmt.Errorf("compactions[%d]: %w", index, err)
		}
		if compaction.SummaryRequestID != nil {
			if err := compaction.SummaryRequestID.Validate(); err != nil {
				return fmt.Errorf("compactions[%d]: %w", index, err)
			}
		}
		if compaction.BeforeTokens < 0 || compaction.AfterTokens < 0 {
			return fmt.Errorf("compactions[%d]: tokens must be non-negative", index)
		}
	}
	for index, miss := range context.CacheMisses {
		if err := miss.Validate(); err != nil {
			return fmt.Errorf("cache_misses[%d]: %w", index, err)
		}
	}
	return nil
}
