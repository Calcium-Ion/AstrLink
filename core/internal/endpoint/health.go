package endpoint

import (
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	defaultFailureThreshold = 3
	defaultCircuitCooldown  = 30 * time.Second
)

type circuitPhase uint8

const (
	circuitClosed circuitPhase = iota
	circuitOpen
	circuitHalfOpen
)

type circuitState struct {
	phase               circuitPhase
	consecutiveFailures int
	openedAt            time.Time
}

type circuitBreakerConfig struct {
	FailureThreshold int
	Cooldown         time.Duration
	Now              func() time.Time
}

type circuitBreaker struct {
	rateLimits       map[string]time.Time
	mu               sync.Mutex
	states           map[contract.ServiceID]circuitState
	failureThreshold int
	cooldown         time.Duration
	now              func() time.Time
}

func newCircuitBreaker(config circuitBreakerConfig) *circuitBreaker {
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = defaultFailureThreshold
	}
	if config.Cooldown <= 0 {
		config.Cooldown = defaultCircuitCooldown
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &circuitBreaker{
		states:           make(map[contract.ServiceID]circuitState),
		failureThreshold: config.FailureThreshold,
		cooldown:         config.Cooldown,
		now:              config.Now,
	}
}

// available is a non-reserving selection check. Begin performs the atomic
// half-open transition immediately before I/O, but automatic resolution can
// already exclude circuits that are still open or have a probe in flight.
func (breaker *circuitBreaker) available(candidate Resolved) bool {
	return breaker.unavailableReason(candidate) == ""
}

// unavailableReason is the available check that also says why selection must
// skip the candidate; it is empty when an attempt may begin.
func (breaker *circuitBreaker) unavailableReason(candidate Resolved) contract.RoutingSkipReason {
	if breaker == nil || candidate.CanonicalService().ID == "" {
		return contract.RoutingSkipCircuitOpen
	}
	breaker.mu.Lock()
	defer breaker.mu.Unlock()

	if deadline := breaker.rateLimits[rateLimitKey(candidate)]; breaker.now().Before(deadline) {
		return contract.RoutingSkipRateLimited
	}
	state := breaker.states[candidate.CanonicalService().ID]
	switch state.phase {
	case circuitClosed:
		return ""
	case circuitOpen:
		if breaker.now().Sub(state.openedAt) >= breaker.cooldown {
			return ""
		}
		return contract.RoutingSkipCircuitOpen
	default:
		// Half-open: the single automatic probe is already in flight.
		return contract.RoutingSkipCircuitOpen
	}
}

// begin admits ordinary closed-circuit attempts and one automatic half-open
// probe after the cooldown.
func (breaker *circuitBreaker) begin(candidate Resolved) bool {
	if breaker == nil || candidate.CanonicalService().ID == "" {
		return false
	}
	breaker.mu.Lock()
	defer breaker.mu.Unlock()

	if deadline := breaker.rateLimits[rateLimitKey(candidate)]; breaker.now().Before(deadline) {
		return false
	}
	state := breaker.states[candidate.CanonicalService().ID]
	switch state.phase {
	case circuitClosed:
		return true
	case circuitOpen:
		if breaker.now().Sub(state.openedAt) < breaker.cooldown {
			return false
		}
		state.phase = circuitHalfOpen
		breaker.states[candidate.CanonicalService().ID] = state
		return true
	default:
		// Half-open: the single automatic probe is already in flight.
		return false
	}
}

func (breaker *circuitBreaker) success(candidate Resolved) {
	if breaker == nil || candidate.CanonicalService().ID == "" {
		return
	}
	breaker.mu.Lock()
	defer breaker.mu.Unlock()

	delete(breaker.states, candidate.CanonicalService().ID)
}

func (breaker *circuitBreaker) failure(candidate Resolved) {
	if breaker == nil || candidate.CanonicalService().ID == "" {
		return
	}
	breaker.mu.Lock()
	defer breaker.mu.Unlock()

	state := breaker.states[candidate.CanonicalService().ID]
	switch state.phase {
	case circuitClosed:
		state.consecutiveFailures++
		if state.consecutiveFailures >= breaker.failureThreshold {
			state.phase = circuitOpen
			state.openedAt = breaker.now()
		}
	case circuitOpen, circuitHalfOpen:
		state.phase = circuitOpen
		state.consecutiveFailures = breaker.failureThreshold
		state.openedAt = breaker.now()
	default:
		return
	}
	breaker.states[candidate.CanonicalService().ID] = state
}

func (breaker *circuitBreaker) abandon(candidate Resolved) {
	if breaker == nil || candidate.CanonicalService().ID == "" {
		return
	}
	breaker.mu.Lock()
	defer breaker.mu.Unlock()

	state, exists := breaker.states[candidate.CanonicalService().ID]
	if !exists || state.phase != circuitHalfOpen {
		return
	}
	// Preserve the already-expired opening time so the next request may claim
	// the abandoned probe immediately.
	state.phase = circuitOpen
	breaker.states[candidate.CanonicalService().ID] = state
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
func (breaker *circuitBreaker) rateLimit(candidate Resolved, duration time.Duration) {
	if breaker == nil || duration <= 0 {
		return
	}
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	if breaker.rateLimits == nil {
		breaker.rateLimits = make(map[string]time.Time)
	}
	now := breaker.now()
	for key, deadline := range breaker.rateLimits {
		if !now.Before(deadline) {
			delete(breaker.rateLimits, key)
		}
	}
	key := rateLimitKey(candidate)
	if deadline := now.Add(duration); deadline.After(breaker.rateLimits[key]) {
		breaker.rateLimits[key] = deadline
	}
}
func (resolver *StoreResolver) RecordRateLimit(candidate Resolved, duration time.Duration) {
	if resolver != nil {
		resolver.breaker.rateLimit(candidate, duration)
	}
}
