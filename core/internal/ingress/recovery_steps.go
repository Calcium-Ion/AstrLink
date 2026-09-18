package ingress

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

type stepTargetState struct {
	attempts int
	action   contract.FailureAction
	readyAt  time.Time
	blocked  bool
	wait     [2]int
}

func recoveryTargetKey(candidate endpoint.Resolved) string {
	model := candidate.UpstreamModel
	if model == "" {
		model = candidate.RequestedModel
	}
	plan := candidate.PlanType
	if plan == "" {
		plan = contract.PlanType(candidate.Mode)
	}
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s", candidate.CanonicalService().ID, model, candidate.UpstreamProtocol, plan)
}
func (schedule *recoverySchedule) sameTarget(a, b int) bool {
	return a >= 0 && b >= 0 && recoveryTargetKey(schedule.candidates[a]) == recoveryTargetKey(schedule.candidates[b])
}
func (schedule *recoverySchedule) stepState(index int) *stepTargetState {
	key := recoveryTargetKey(schedule.candidates[index])
	state := schedule.states[key]
	if state == nil {
		state = &stepTargetState{action: contract.FailureRetryAndFailover}
		schedule.states[key] = state
	}
	return state
}
func (schedule *recoverySchedule) chooseStep(from int) bool {
	for i := from; i < len(schedule.candidates); i++ {
		reason := ""
		state := schedule.stepState(i)
		if schedule.total >= schedule.policy.MaxAttempts {
			reason = "attempt_limit"
		} else if schedule.candidates[i].Unavailable != "" {
			reason = schedule.candidates[i].Unavailable
		} else if state.blocked {
			reason = "retry_after_limit"
		} else if state.attempts > 0 && !state.action.AllowsRetry() {
			reason = "retry_not_allowed"
		} else if !schedule.policy.Enabled && !schedule.sameTarget(0, i) {
			reason = "failover_disabled"
		} else if schedule.previous >= 0 {
			if schedule.sameTarget(schedule.previous, i) && !schedule.action.AllowsRetry() {
				reason = "retry_not_allowed"
			} else if !schedule.sameTarget(schedule.previous, i) && !schedule.action.AllowsFailover() {
				reason = "failover_not_allowed"
			}
		}
		if reason != "" {
			schedule.skips[i] = reason
			continue
		}
		schedule.nextIndex = i
		schedule.readyAt[i] = state.readyAt
		schedule.waitBounds[i] = state.wait
		schedule.delay = max(time.Until(state.readyAt), 0)
		return true
	}
	schedule.nextIndex = -1
	if schedule.total >= schedule.policy.MaxAttempts {
		schedule.stopReason = "attempt_limit"
	} else if schedule.stopReason == "" {
		schedule.stopReason = "targets_exhausted"
	}
	return false
}
func recoveryDelay(policy contract.FailurePolicy, count int, retryAfter time.Duration) (time.Duration, [2]int, bool) {
	base := time.Duration(policy.InitialDelayMS) * time.Millisecond
	for i := 1; i < count; i++ {
		base *= 2
	}
	ceiling := time.Duration(policy.MaxDelayMS) * time.Millisecond
	bounds := [2]int{int(max(min(time.Duration(float64(base)*.8), ceiling), retryAfter).Milliseconds()), int(max(min(time.Duration(float64(base)*1.2), ceiling), retryAfter).Milliseconds())}
	return base, bounds, retryAfter > ceiling
}
func (schedule *recoverySchedule) recoverStep(index int, action contract.FailureAction, retryAfter time.Duration) bool {
	schedule.previous = index
	schedule.action = action
	schedule.nextIndex = -1
	schedule.stopReason = ""
	state := schedule.stepState(index)
	state.action = action
	policy := failurePolicy(schedule.candidates[index])
	base, bounds, blocked := recoveryDelay(policy, state.attempts, retryAfter)
	state.wait = bounds
	state.blocked = blocked
	state.readyAt = time.Now().Add(max(min(time.Duration(float64(base)*(0.8+rand.Float64()*0.4)), time.Duration(policy.MaxDelayMS)*time.Millisecond), retryAfter))
	if action == contract.FailureStop {
		schedule.stopReason = "error_rule"
		return false
	}
	if blocked {
		schedule.stopReason = "retry_after_limit"
	}
	return schedule.chooseStep(index + 1)
}
