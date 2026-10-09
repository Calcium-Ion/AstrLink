package endpoint

import (
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// rateLimits holds the cooldowns an upstream asked for with HTTP 429. Each
// one covers only the exact service, upstream protocol, model, and plan that
// was limited; routing never withholds a provider on its own judgement.
type rateLimits struct {
	mu        sync.Mutex
	deadlines map[string]time.Time
}

// until returns when candidate's cooldown ends, or the zero time when it may
// be attempted now.
func (limits *rateLimits) until(candidate Resolved, now time.Time) time.Time {
	if limits == nil {
		return time.Time{}
	}
	limits.mu.Lock()
	defer limits.mu.Unlock()
	deadline := limits.deadlines[rateLimitKey(candidate)]
	if !now.Before(deadline) {
		return time.Time{}
	}
	return deadline
}

func (limits *rateLimits) limit(candidate Resolved, duration time.Duration, now time.Time) {
	if limits == nil || duration <= 0 {
		return
	}
	limits.mu.Lock()
	defer limits.mu.Unlock()
	if limits.deadlines == nil {
		limits.deadlines = make(map[string]time.Time)
	}
	for key, deadline := range limits.deadlines {
		if !now.Before(deadline) {
			delete(limits.deadlines, key)
		}
	}
	key := rateLimitKey(candidate)
	if deadline := now.Add(duration); deadline.After(limits.deadlines[key]) {
		limits.deadlines[key] = deadline
	}
}

func (limits *rateLimits) release(serviceID contract.ServiceID) {
	if limits == nil {
		return
	}
	limits.mu.Lock()
	defer limits.mu.Unlock()
	prefix := string(serviceID) + "\x00"
	for key := range limits.deadlines {
		if strings.HasPrefix(key, prefix) {
			delete(limits.deadlines, key)
		}
	}
}

func rateLimitKey(candidate Resolved) string {
	model := candidate.UpstreamModel
	if model == "" {
		model = candidate.RequestedModel
	}
	plan := candidate.PlanType
	if plan == "" {
		plan = contract.PlanTypeNative
		if candidate.Mode == contract.CapabilityModeDelegated {
			plan = contract.PlanTypeDelegated
		}
	}
	return string(candidate.CanonicalService().ID) + "\x00" + string(candidate.UpstreamProtocol) + "\x00" + model + "\x00" + string(plan)
}

// ReleaseRateLimits drops all route cooldowns for a service after an operator
// restores its scheduling. A later upstream 429 can establish a new cooldown.
func (resolver *StoreResolver) ReleaseRateLimits(serviceID contract.ServiceID) {
	if resolver != nil {
		resolver.limits.release(serviceID)
	}
}

func (resolver *StoreResolver) RecordRateLimit(candidate Resolved, duration time.Duration) {
	if resolver != nil {
		resolver.limits.limit(candidate, duration, resolver.now())
	}
}
