package ingress

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

// PreviewRecovery executes the same request-local scheduler with synthetic
// outcomes. It never obtains health admission, sleeps, authenticates, or sends I/O.
func PreviewRecovery(ctx context.Context, candidates []endpoint.Resolved, input contract.RecoveryPreviewInput) (contract.RecoveryPreview, error) {
	result := contract.RecoveryPreview{Steps: []contract.RecoveryPreviewStep{}}
	if input.SuccessAt < 0 || input.SuccessAt > 20 {
		return result, fmt.Errorf("success_at must be 0–20")
	}
	if input.Error != "network_error" && input.Error != "response_timeout" {
		code, e := strconv.Atoi(input.Error)
		if e != nil || code < 400 || code > 599 {
			return result, fmt.Errorf("invalid simulated error")
		}
	}
	schedule := newRecoverySchedule(candidates, true)
	schedule.simulation = true
	result.MaxAttempts = schedule.policy.MaxAttempts
	seen := map[int]bool{}
	previous := -1
	step := func(i int, status, reason string) contract.RecoveryPreviewStep {
		candidate := candidates[i]
		model := candidate.UpstreamModel
		if model == "" {
			model = input.Model
		}
		id := ""
		if candidate.Path != nil {
			id = candidate.Path.StepID
		}
		action := "initial"
		if previous >= 0 {
			action = "failover"
			if schedule.sameTarget(previous, i) {
				action = "retry"
			}
		}
		return contract.RecoveryPreviewStep{StepID: id, ServiceID: candidate.CanonicalService().ID, Model: model, Action: action, Status: status, Reason: reason, WaitMinMS: schedule.waitBounds[i][0], WaitMaxMS: schedule.waitBounds[i][1]}
	}
	for {
		i, ok := schedule.next(ctx)
		if !ok {
			break
		}
		for j := 0; j < i; j++ {
			if reason, ok := schedule.skips[j]; ok && !seen[j] {
				result.Steps = append(result.Steps, step(j, "skipped", reason))
				seen[j] = true
			}
		}
		seen[i] = true
		if candidates[i].Unavailable != "" {
			result.Steps = append(result.Steps, step(i, "skipped", candidates[i].Unavailable))
			continue
		}
		schedule.started(i)
		if input.SuccessAt > 0 && input.SuccessAt == schedule.total {
			result.Steps = append(result.Steps, step(i, "succeeded", ""))
			schedule.stopReason = "succeeded"
			break
		}
		result.Steps = append(result.Steps, step(i, "failed", input.Error))
		previous = i
		policy := failurePolicy(candidates[i])
		action := policy.NetworkError
		if input.Error == "response_timeout" {
			action = policy.ResponseTimeout
		} else if input.Error != "network_error" {
			code, _ := strconv.Atoi(input.Error)
			action = policy.ActionForStatus(code)
		}
		if !schedule.recover(i, action, parseRetryAfter(input.RetryAfter, time.Now())) {
			break
		}
	}
	result.StopReason = schedule.stopReason
	if result.StopReason == "" {
		result.StopReason = "targets_exhausted"
	}
	for i := range candidates {
		if !seen[i] {
			reason := schedule.skips[i]
			if reason == "" {
				reason = result.StopReason
			}
			result.Steps = append(result.Steps, step(i, "skipped", reason))
		}
	}
	return result, nil
}
