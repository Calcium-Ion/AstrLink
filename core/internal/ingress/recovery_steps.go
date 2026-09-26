package ingress

import (
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

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
