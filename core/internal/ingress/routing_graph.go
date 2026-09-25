package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/routinggraph"
)

type graphPlanKey struct{}

func (handler *Handler) serveRoutingGraph(writer http.ResponseWriter, request *http.Request, classified Request, session *recordSession) bool {
	resolver, ok := handler.resolver.(endpoint.RoutingGraphResolver)
	if !ok || classified.Protocol.IsModelDiscovery() {
		return false
	}
	plan, err := resolver.ResolveRoutingGraph(request.Context(), endpoint.ResolveRequest{Protocol: classified.Protocol, Model: classified.Model, Streaming: classified.Streaming, Continuation: classified.PreviousResponseID != ""})
	if err != nil {
		session.captureUnreadRequestBody(request)
		handler.writeResolveError(writer, request, classified, err)
		return true
	}
	if plan == nil {
		return false
	}
	if !plan.Entry.Enabled {
		session.captureUnreadRequestBody(request)
		writeInferenceError(writer, 503, "routing_entry_paused", "this model entry is paused", false, nil)
		session.noteFailed(errorSummaryFromInference("routing_entry_paused", "this model entry is paused", false))
		return true
	}
	// Stateful continuation stays constrained by the existing protocol binding.
	// Mark other nodes unavailable; never inject the bound target into a branch.
	if classified.PreviousResponseID != "" {
		bound, err := handler.bindResponseAffinity(request.Context(), classified, plan.Candidates)
		if err != nil {
			session.captureUnreadRequestBody(request)
			writeInferenceError(writer, 409, "response_affinity_unavailable", err.Error(), false, nil)
			session.noteFailed(errorSummaryFromInference("response_affinity_unavailable", err.Error(), false))
			return true
		}
		for i, candidate := range plan.Candidates {
			if recoveryTargetKey(candidate) != recoveryTargetKey(bound[0]) {
				plan.Candidates[i].Unavailable = "protocol_binding"
			} else {
				plan.Candidates[i].Failover = bound[0].Failover
			}
		}
	}
	if turn := responsesWSTurnFromContext(request.Context()); turn != nil {
		compatible := turn.session.filterCandidates(classified.Model, classified.Model, plan.Candidates)
		allowed := map[string]bool{}
		for _, candidate := range compatible {
			allowed[candidate.GraphNodeID] = true
		}
		for i, candidate := range plan.Candidates {
			if !allowed[candidate.GraphNodeID] {
				plan.Candidates[i].Unavailable = "responses_websocket_unavailable"
				if turn.session.serviceID != "" {
					plan.Candidates[i].Unavailable = "protocol_binding"
				}
			}
		}
	}
	request = request.WithContext(context.WithValue(request.Context(), graphPlanKey{}, plan))
	handler.executeCandidates(writer, request, classified, plan.Candidates)
	return true
}

// discoverGraphWithoutUpstreams lets discovery list local graph entries when
// no upstream can contribute a listing. Storage failures still fail closed.
func (handler *Handler) discoverGraphWithoutUpstreams(ctx context.Context, classified Request, err error) bool {
	var capability *endpoint.CapabilityUnavailableError
	var unhealthy *endpoint.UnhealthyCandidatesError
	if !classified.Protocol.IsModelDiscovery() || !errors.Is(err, endpoint.ErrNoEndpoint) && !errors.As(err, &capability) && !errors.As(err, &unhealthy) {
		return false
	}
	lister, ok := handler.resolver.(endpoint.RoutingGraphResolver)
	if !ok {
		return false
	}
	models, listErr := lister.ListRoutingGraphModels(ctx)
	return listErr == nil && len(graphDiscoveryModels(classified.Protocol, models)) > 0
}

// graphDiscoveryModels drops entries that the protocol's listing cannot name.
func graphDiscoveryModels(protocol contract.ProtocolID, models []string) []string {
	if protocol != contract.ProtocolGoogleModels {
		return models
	}
	valid := make([]string, 0, len(models))
	for _, model := range models {
		if !strings.Contains(model, "/") {
			valid = append(valid, model)
		}
	}
	return valid
}

func (schedule *recoverySchedule) attachGraph(plan *endpoint.RoutingGraphPlan, body []byte, records *recordSession) {
	facts := plan.Facts
	var input map[string]json.RawMessage
	if len(body) > 0 && json.Unmarshal(body, &input) == nil {
		var tools []json.RawMessage
		if raw, exists := input["tools"]; !exists || json.Unmarshal(raw, &tools) == nil {
			hasTools := len(tools) > 0
			facts.HasTools = &hasTools
		}
		// Image presence is derived from typed content blocks, never prompt text.
		var value any
		if json.Unmarshal(body, &value) == nil {
			hasImages := hasGraphImage(value)
			facts.HasImages = &hasImages
		}
	}
	schedule.graph = routinggraph.New(plan.Graph, plan.Entry.ID, facts)
	schedule.graphIndices = map[string]int{}
	for i, candidate := range schedule.candidates {
		schedule.graphIndices[candidate.GraphNodeID] = i
	}
	if schedule.replayable {
		schedule.policy.MaxAttempts = plan.MaxAttempts
	}
	schedule.graph.Observe = func(step contract.RoutingGraphStep) {
		if records == nil {
			return
		}
		if records.recovery == nil {
			records.recovery = &contract.RequestRecovery{}
		}
		records.recovery.GraphRevision = plan.Revision
		records.recovery.GraphEntryID = plan.Entry.ID
		records.recovery.GraphTrace = append([]contract.RoutingGraphStep(nil), schedule.graph.Steps...)
	}
}

func hasGraphImage(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		if kind, _ := v["type"].(string); kind == "image" || kind == "image_url" || kind == "input_image" {
			return true
		}
		if data, ok := v["inlineData"].(map[string]any); ok {
			if mime, _ := data["mimeType"].(string); strings.HasPrefix(mime, "image/") {
				return true
			}
		}
		if data, ok := v["fileData"].(map[string]any); ok {
			if mime, _ := data["mimeType"].(string); strings.HasPrefix(mime, "image/") {
				return true
			}
		}
		for _, key := range []string{"messages", "input", "contents", "content", "parts"} {
			if hasGraphImage(v[key]) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if hasGraphImage(item) {
				return true
			}
		}
	}
	return false
}

func (schedule *recoverySchedule) chooseGraph() bool {
	for {
		node, ok := schedule.graph.Next()
		if !ok {
			schedule.nextIndex = -1
			schedule.stopReason = schedule.graph.StopReason
			if schedule.graphUnbound && !schedule.graphBound {
				schedule.stopReason = "protocol_binding"
			}
			return false
		}
		index, exists := schedule.graphIndices[node.ID]
		if !exists {
			schedule.graph.Note(node.ID, "skipped", "target_unavailable", "failure")
			continue
		}
		if schedule.candidates[index].Unavailable != "protocol_binding" {
			schedule.graphBound = true
		}
		if schedule.stepState(index).attempts > 0 {
			schedule.graph.Note(node.ID, "skipped", "already_attempted", "failure")
			continue
		}
		schedule.nextIndex = index
		return true
	}
}

func (schedule *recoverySchedule) graphOutcome(index int, reason string) {
	if schedule.graph == nil {
		return
	}
	status := 0
	if strings.HasPrefix(reason, "http_") {
		status, _ = strconv.Atoi(strings.TrimPrefix(reason, "http_"))
	}
	schedule.graph.Outcome(schedule.candidates[index].GraphNodeID, status, reason)
}

func (schedule *recoverySchedule) graphSkip(index int, reason string) {
	if schedule.graph == nil {
		return
	}
	schedule.graph.Note(schedule.candidates[index].GraphNodeID, "skipped", reason, "failure")
	schedule.graph.Facts.LastStatus = nil
	schedule.graph.Facts.LastError = &reason
	// A bound continuation never sends state elsewhere, but the selected
	// failure path may still lead to the bound target.
	if reason == "protocol_binding" {
		schedule.graphUnbound = true
	}
}

func (schedule *recoverySchedule) graphResult(index int, status, reason string) {
	if schedule.graph != nil {
		schedule.graph.Note(schedule.candidates[index].GraphNodeID, status, reason, "")
	}
}

func graphStopMessage(reason string) string { return fmt.Sprintf("model route stopped: %s", reason) }

func (schedule *recoverySchedule) recoverGraph(index int, action contract.FailureAction, retryAfter time.Duration) bool {
	schedule.nextIndex = -1
	schedule.stopReason = "error_rule"
	if schedule.total >= schedule.policy.MaxAttempts {
		schedule.stopReason = "attempt_limit"
		schedule.graph.Stop(schedule.stopReason)
		return false
	}
	policy := failurePolicy(schedule.candidates[index])
	_, bounds, blocked := recoveryDelay(policy, schedule.stepState(index).attempts, retryAfter)
	schedule.waitBounds[index] = bounds
	if action.AllowsRetry() && schedule.stepState(index).attempts <= policy.MaxRetries && !blocked && schedule.replayable {
		schedule.nextIndex = index
		schedule.graphPending = true
		schedule.readyAt[index] = time.Now().Add(time.Duration(bounds[0]) * time.Millisecond)
		schedule.stopReason = ""
		return true
	}
	canSwitch := action.AllowsFailover() && schedule.replayable
	if policy := schedule.candidates[index].Failover; policy != nil && !policy.Enabled {
		canSwitch = false
	}
	if canSwitch && schedule.chooseGraph() {
		schedule.graphPending = true
		schedule.stopReason = ""
		return true
	}
	if !canSwitch {
		schedule.graph.Stop(schedule.stopReason)
	}
	return false
}

func PreviewRoutingGraph(ctx context.Context, plan *endpoint.RoutingGraphPlan, input routinggraph.PreviewInput) (routinggraph.Preview, error) {
	if err := input.Graph.Validate(); err != nil {
		return routinggraph.Preview{}, err
	}
	schedule := newRecoverySchedule(plan.Candidates, true)
	schedule.simulation = true
	schedule.attachGraph(plan, nil, nil)
	if input.Facts.HasTools != nil {
		schedule.graph.Facts.HasTools = input.Facts.HasTools
	}
	if input.Facts.HasImages != nil {
		schedule.graph.Facts.HasImages = input.Facts.HasImages
	}
	for id, usage := range input.Facts.Quota {
		if err := usage.Validate(); err != nil {
			return routinggraph.Preview{}, err
		}
		if schedule.graph.Facts.Quota == nil {
			schedule.graph.Facts.Quota = map[contract.ServiceID]contract.SubscriptionUsage{}
		}
		schedule.graph.Facts.Quota[id] = usage
	}
	for {
		index, ok := schedule.next(ctx)
		if !ok {
			break
		}
		candidate := schedule.candidates[index]
		if candidate.Unavailable != "" {
			schedule.graphSkip(index, candidate.Unavailable)
			continue
		}
		outcome := input.Outcomes[candidate.GraphNodeID]
		if outcome == "unavailable" {
			schedule.graphSkip(index, "target_unavailable")
			continue
		}
		schedule.started(index)
		if outcome == "" || outcome == "success" {
			schedule.graphResult(index, "succeeded", "")
			schedule.stopReason = "succeeded"
			break
		}
		status, _ := strconv.Atoi(outcome)
		if status == 0 && outcome != "network_error" && outcome != "response_timeout" {
			return routinggraph.Preview{}, fmt.Errorf("unknown simulated outcome")
		}
		if status != 0 && (status < 400 || status > 599) {
			return routinggraph.Preview{}, fmt.Errorf("simulated HTTP status must be 400–599")
		}
		policy := failurePolicy(candidate)
		action := policy.NetworkError
		reason := outcome
		if status > 0 {
			action = policy.ActionForStatus(status)
			reason = "http_" + outcome
		} else if outcome == "response_timeout" {
			action = policy.ResponseTimeout
		}
		schedule.graphOutcome(index, reason)
		if !schedule.recover(index, action, 0) {
			break
		}
	}
	return routinggraph.Preview{Steps: schedule.graph.Steps, StopReason: schedule.stopReason, Attempts: schedule.total}, nil
}
