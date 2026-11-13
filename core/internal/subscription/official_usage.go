package subscription

import (
	"context"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const claudeRateLimitHeader = "Anthropic-Ratelimit-Unified-"

// DecodeClaudeRateLimitHeaders reads the unified subscription windows
// Anthropic returns on every Claude model call, as Claude Code does: per
// window a utilization fraction (0-1) and a reset time in Unix seconds, plus
// the overall status. ok is false when the response carries none of them.
func DecodeClaudeRateLimitHeaders(header http.Header) (contract.SubscriptionUsage, bool) {
	usage := contract.SubscriptionUsage{
		Primary:   claudeHeaderWindow(header, "5h", claudeSessionWindowSeconds),
		Secondary: claudeHeaderWindow(header, "7d", claudeWeeklyWindowSeconds),
	}
	status := strings.ToLower(strings.TrimSpace(header.Get(claudeRateLimitHeader + "Status")))
	switch status {
	case "allowed", "allowed_warning":
		allowed, reached := true, false
		usage.Allowed, usage.LimitReached = &allowed, &reached
	case "rejected":
		allowed, reached := false, true
		usage.Allowed, usage.LimitReached = &allowed, &reached
	}
	return usage, usage.Primary != nil || usage.Secondary != nil || usage.Allowed != nil
}

func claudeHeaderWindow(header http.Header, name string, seconds int64) *contract.RateLimitWindow {
	prefix := claudeRateLimitHeader + name + "-"
	var percent float64
	switch value := strings.TrimSpace(header.Get(prefix + "Utilization")); {
	case value != "":
		fraction, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(fraction) || math.IsInf(fraction, 0) || fraction > 10 {
			return nil
		}
		percent = max(fraction, 0) * 100
	case strings.EqualFold(strings.TrimSpace(header.Get(prefix+"Status")), "rejected"):
		percent = 100
	default:
		return nil
	}
	limit := seconds
	window := &contract.RateLimitWindow{UsedPercent: math.Round(percent*100) / 100, LimitWindowSeconds: &limit}
	if reset, err := strconv.ParseFloat(strings.TrimSpace(header.Get(prefix+"Reset")), 64); err == nil && reset > 0 && reset < 1e11 {
		at := time.Unix(int64(reset), 0).UTC()
		window.ResetAt = &at
	}
	return window
}

// ObserveOfficialUsage records the quota an official Claude account's model
// call reported in its rate-limit headers. Official accounts hold no token to
// read /api/oauth/usage with, so these headers, which come with requests the
// user already made, are their only quota source; nothing is ever requested
// for it. A window the response leaves out keeps its last value until it
// resets.
func (manager *Manager) ObserveOfficialUsage(ctx context.Context, id contract.ServiceID, header http.Header) {
	observed, ok := DecodeClaudeRateLimitHeaders(header)
	if !ok {
		return
	}
	account, err := manager.Get(ctx, id)
	if err != nil || !account.Official() || account.Status != contract.SubscriptionStatusConnected {
		return
	}
	now := manager.now().UTC()
	manager.mu.Lock()
	previous, known := manager.usageCache[id]
	manager.mu.Unlock()
	usage := observed
	if known && previous.usage.PlanType == account.PlanType {
		if usage.Primary == nil && !windowEnded(previous.usage.Primary, now) {
			usage.Primary = previous.usage.Primary
		}
		if usage.Secondary == nil && !windowEnded(previous.usage.Secondary, now) {
			usage.Secondary = previous.usage.Secondary
		}
	}
	usage.ServiceID = id
	usage.FetchedAt = now
	usage.PlanType = account.PlanType
	if usage.Validate() != nil {
		return
	}
	// Every model call reports the windows; a snapshot equal to the last one
	// is kept in memory and written again only after the cache period.
	persist := !known || !now.Before(previous.until) || !sameWindows(previous.usage, usage)
	if persist && manager.usageObserver != nil {
		if err := manager.usageObserver(ctx, account, usage); err != nil {
			log.Printf("subscription usage persistence: %v", err)
		}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if current, err := manager.accounts.GetAccount(ctx, id); err == nil &&
		current.Status == contract.SubscriptionStatusConnected &&
		current.ProviderAccountID == account.ProviderAccountID {
		until := now.Add(usageCacheTTL)
		if !persist {
			until = previous.until
		}
		manager.usageCache[id] = usageCacheEntry{usage: usage, until: until}
	}
}

func sameWindows(a, b contract.SubscriptionUsage) bool {
	same := func(x, y *contract.RateLimitWindow) bool {
		if x == nil || y == nil {
			return x == y
		}
		return x.UsedPercent == y.UsedPercent && (x.ResetAt == nil) == (y.ResetAt == nil) &&
			(x.ResetAt == nil || x.ResetAt.Equal(*y.ResetAt))
	}
	flag := func(x, y *bool) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return a.PlanType == b.PlanType && same(a.Primary, b.Primary) && same(a.Secondary, b.Secondary) &&
		flag(a.Allowed, b.Allowed) && flag(a.LimitReached, b.LimitReached)
}

func windowEnded(window *contract.RateLimitWindow, now time.Time) bool {
	return window == nil || window.ResetAt != nil && !now.Before(*window.ResetAt)
}
