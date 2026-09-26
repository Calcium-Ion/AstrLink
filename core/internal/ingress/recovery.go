package ingress

import (
	"context"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

// recoverySchedule is request-local. It is the only owner of retry quotas and
// next-target ordering; transports and protocol converters never retry.
type recoverySchedule struct {
	nextRepair bool
	repairing  bool
	candidates []endpoint.Resolved
	policy     contract.FailoverPolicy
	selected   []bool
	maySwitch  bool
	counts     []int // Ordinary attempts only; repairs still count toward total.
	retryable  []bool
	excluded   []bool
	readyAt    []time.Time
	total      int
	nextIndex  int
	stopReason string
	delay      time.Duration
}

func newRecoverySchedule(candidates []endpoint.Resolved, replayable bool) *recoverySchedule {
	policy := contract.DefaultFailoverPolicy()
	if len(candidates) > 0 && candidates[0].Failover != nil {
		policy = *candidates[0].Failover
	}
	if !replayable {
		policy.MaxAttempts = 1
		policy.Enabled = false
	}
	schedule := &recoverySchedule{candidates: candidates, policy: policy, selected: make([]bool, len(candidates)), maySwitch: policy.Enabled, counts: make([]int, len(candidates)), retryable: make([]bool, len(candidates)), excluded: make([]bool, len(candidates)), readyAt: make([]time.Time, len(candidates)), nextIndex: 0}
	if len(candidates) == 0 {
		schedule.nextIndex = -1
	}
	return schedule
}

func failurePolicy(candidate endpoint.Resolved) contract.FailurePolicy {
	if candidate.FailurePolicy != nil {
		return *candidate.FailurePolicy
	}
	if candidate.CanonicalService().FailurePolicy != nil {
		return *candidate.CanonicalService().FailurePolicy
	}
	return contract.DefaultFailurePolicy()
}

func (schedule *recoverySchedule) next(ctx context.Context) (int, bool) {
	index := schedule.nextIndex
	if index < 0 || schedule.total >= schedule.policy.MaxAttempts {
		return 0, false
	}
	wait := time.Until(schedule.readyAt[index])
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			schedule.stopReason = "cancelled"
			return 0, false
		case <-timer.C:
		}
	}
	if ctx.Err() != nil {
		schedule.stopReason = "cancelled"
		return 0, false
	}
	schedule.repairing = schedule.nextRepair
	schedule.nextRepair = false
	schedule.selected[index] = true
	schedule.retryable[index] = false
	schedule.nextIndex = -1
	// A refused/local candidate spends no network budget and is not revisited.
	if schedule.maySwitch {
		for next := range schedule.counts {
			if !schedule.selected[next] {
				schedule.nextIndex = next
				break
			}
		}
		if schedule.nextIndex < 0 && schedule.policy.Strategy == contract.FailoverFirst {
			for next := range schedule.counts {
				if schedule.retryable[next] {
					schedule.nextIndex = next
					break
				}
			}
		}
	}
	return index, true
}

func (schedule *recoverySchedule) started(index int) {
	schedule.total++
	if schedule.repairing {
		return
	}
	schedule.counts[index]++
}

// repair inserts an immediate same-target attempt. The caller permits only one
// repair per target. It spends the overall network budget, but not a normal
// retry.
func (schedule *recoverySchedule) repair(index int) bool {
	if schedule.total >= schedule.policy.MaxAttempts {
		return false
	}
	schedule.nextIndex = index
	schedule.nextRepair = true
	schedule.readyAt[index] = time.Time{}
	schedule.delay = 0
	schedule.stopReason = ""
	return true
}

// excludeService removes every remaining route of a paused account from this
// request, including routes for other protocols or models.
func (schedule *recoverySchedule) excludeService(id contract.ServiceID) {
	for index, candidate := range schedule.candidates {
		if candidate.CanonicalService().ID == id {
			schedule.selected[index] = true
			schedule.retryable[index] = false
			schedule.excluded[index] = true
		}
	}
}

func (schedule *recoverySchedule) recover(index int, action contract.FailureAction, retryAfter time.Duration) bool {
	schedule.nextIndex = -1
	schedule.delay = 0
	schedule.stopReason = "error_rule"
	policy := failurePolicy(schedule.candidates[index])
	schedule.retryable[index] = !schedule.excluded[index] && schedule.policy.Strategy != contract.FailoverOnly && action.AllowsRetry() && schedule.counts[index] <= policy.MaxRetries
	if schedule.total >= schedule.policy.MaxAttempts {
		schedule.stopReason = "attempt_limit"
		return false
	}
	delay := time.Duration(policy.InitialDelayMS) * time.Millisecond
	for retry := 1; retry < schedule.counts[index]; retry++ {
		delay *= 2
	}
	maximum := time.Duration(policy.MaxDelayMS) * time.Millisecond
	if delay > 0 {
		delay = time.Duration(float64(delay) * (0.8 + rand.Float64()*0.4))
	}
	if delay > maximum {
		delay = maximum
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > maximum {
		schedule.retryable[index] = false
		schedule.stopReason = "retry_after_limit"
	}
	schedule.readyAt[index] = time.Now().Add(delay)
	canSwitch := action.AllowsFailover() && schedule.policy.Enabled
	schedule.maySwitch = canSwitch
	choose := func(next int) bool {
		schedule.nextIndex = next
		schedule.stopReason = ""
		schedule.delay = max(time.Until(schedule.readyAt[next]), 0)
		return true
	}
	if schedule.retryable[index] && (!canSwitch || schedule.policy.Strategy == contract.RetryFirst) {
		return choose(index)
	}
	if canSwitch {
		for next := range schedule.counts {
			if next != index && !schedule.selected[next] {
				return choose(next)
			}
		}
		if schedule.policy.Strategy == contract.FailoverFirst {
			for next := range schedule.counts {
				if schedule.retryable[next] {
					return choose(next)
				}
			}
		}
	}
	if schedule.retryable[index] {
		return choose(index)
	}
	if schedule.stopReason != "retry_after_limit" {
		if action == contract.FailureStop {
			schedule.stopReason = "error_rule"
		} else if !schedule.policy.Enabled && action.AllowsFailover() {
			schedule.stopReason = "failover_disabled"
		} else {
			schedule.stopReason = "targets_exhausted"
		}
	}
	return false
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return max(deadline.Sub(now), 0)
	}
	return 0
}

type retryHTTPError struct {
	status int
	reason string
}

func (failure *retryHTTPError) Error() string { return "upstream HTTP " + strconv.Itoa(failure.status) }
