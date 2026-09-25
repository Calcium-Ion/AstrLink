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
	"github.com/QuantumNous/astrlink/core/internal/routinggraph"
)

// recoverySchedule is request-local. It is the only owner of retry quotas and
// next-target ordering; transports and protocol converters never retry.
type recoverySchedule struct {
	graph              *routinggraph.Run
	graphIndices       map[string]int
	graphPending       bool
	graphUnbound       bool // A stateful binding excluded at least one visited node.
	graphBound         bool // The graph reached a node compatible with that binding.
	replayable         bool
	manual, simulation bool
	nextRepair         bool
	repairing          bool
	previous           int
	action             contract.FailureAction
	states             map[string]*stepTargetState
	skips              map[int]string
	waitBounds         [][2]int
	candidates         []endpoint.Resolved
	policy             contract.FailoverPolicy
	selected           []bool
	maySwitch          bool
	counts             []int // Ordinary attempts only; repairs still count toward total.
	retryable          []bool
	readyAt            []time.Time
	total              int
	nextIndex          int
	stopReason         string
	delay              time.Duration
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
	schedule := &recoverySchedule{replayable: replayable, candidates: candidates, policy: policy, selected: make([]bool, len(candidates)), maySwitch: policy.Enabled, counts: make([]int, len(candidates)), retryable: make([]bool, len(candidates)), readyAt: make([]time.Time, len(candidates)), nextIndex: 0}
	schedule.previous = -1
	schedule.states = map[string]*stepTargetState{}
	schedule.skips = map[int]string{}
	schedule.waitBounds = make([][2]int, len(candidates))
	schedule.manual = len(candidates) > 0 && candidates[0].Path != nil && candidates[0].Path.Mode == "steps"
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
	if schedule.graph != nil && !schedule.graphPending && !schedule.nextRepair {
		if !schedule.chooseGraph() {
			return 0, false
		}
	}
	if schedule.manual && !schedule.nextRepair && schedule.nextIndex >= 0 && !schedule.chooseStep(schedule.nextIndex) {
		return 0, false
	}
	index := schedule.nextIndex
	if index < 0 || schedule.total >= schedule.policy.MaxAttempts {
		if schedule.total >= schedule.policy.MaxAttempts {
			schedule.stopReason = "attempt_limit"
		}
		return 0, false
	}
	wait := time.Until(schedule.readyAt[index])
	if wait > 0 && !schedule.simulation {
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
	if schedule.graph != nil {
		schedule.graphPending = false
		schedule.nextIndex = -1
		schedule.selected[index] = true
		return index, true
	}
	if schedule.manual {
		schedule.selected[index] = true
		schedule.nextIndex = index + 1
		return index, true
	}
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
	if schedule.graph != nil {
		schedule.graph.Facts.Attempts = schedule.total
		schedule.graphResult(index, "attempted", "")
	}
	if schedule.repairing {
		return
	}
	schedule.counts[index]++
	if schedule.manual || schedule.graph != nil {
		schedule.stepState(index).attempts++
	}
}

// repair inserts an immediate same-target attempt, including within a manual
// step. The caller permits only one repair per target. It spends the overall
// network budget, but neither a normal retry nor a manual path step.
func (schedule *recoverySchedule) repair(index int) bool {
	if schedule.total >= schedule.policy.MaxAttempts {
		return false
	}
	schedule.nextIndex = index
	schedule.nextRepair = true
	schedule.readyAt[index] = time.Time{}
	schedule.waitBounds[index] = [2]int{}
	schedule.delay = 0
	schedule.stopReason = ""
	return true
}

func (schedule *recoverySchedule) recover(index int, action contract.FailureAction, retryAfter time.Duration) bool {
	if schedule.graph != nil {
		return schedule.recoverGraph(index, action, retryAfter)
	}
	if schedule.manual {
		return schedule.recoverStep(index, action, retryAfter)
	}
	schedule.nextIndex = -1
	schedule.delay = 0
	schedule.stopReason = "error_rule"
	policy := failurePolicy(schedule.candidates[index])
	schedule.retryable[index] = schedule.policy.Strategy != contract.FailoverOnly && action.AllowsRetry() && schedule.counts[index] <= policy.MaxRetries
	if schedule.total >= schedule.policy.MaxAttempts {
		schedule.stopReason = "attempt_limit"
		return false
	}
	_, bounds, _ := recoveryDelay(policy, schedule.counts[index], retryAfter)
	schedule.waitBounds[index] = bounds
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
