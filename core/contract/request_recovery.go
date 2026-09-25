package contract

import "fmt"

// RequestRecovery is metadata only; it never includes request/error bodies.
type RequestRecovery struct {
	GraphRevision int64              `json:"graph_revision,omitempty"`
	GraphEntryID  string             `json:"graph_entry_id,omitempty"`
	GraphTrace    []RoutingGraphStep `json:"graph_trace,omitempty"`
	PathID        RecoveryPathID     `json:"path_id,omitempty"`
	PathName      string             `json:"path_name,omitempty"`
	PathVersion   string             `json:"path_version,omitempty"`
	StepID        string             `json:"step_id,omitempty"`
	UpstreamModel string             `json:"upstream_model,omitempty"`
	Action        string             `json:"action,omitempty"`
	Reason        string             `json:"reason,omitempty"`
	DelayMS       int                `json:"delay_ms"`
	StopReason    string             `json:"stop_reason,omitempty"`
}

func (recovery RequestRecovery) Validate() error {
	if recovery.GraphRevision < 0 || len(recovery.GraphTrace) > 2048 {
		return fmt.Errorf("invalid routing graph trace")
	}
	if recovery.GraphEntryID != "" {
		if err := validateResourceID("graph entry", recovery.GraphEntryID); err != nil {
			return err
		}
	}
	for _, step := range recovery.GraphTrace {
		if err := validateBoundedText("graph node", step.NodeID, 128, false); err != nil {
			return err
		}
		if len(step.Reason) > 512 || len(step.Port) > 128 || len(step.Model) > 1024 {
			return fmt.Errorf("graph trace text exceeds limit")
		}
	}
	if recovery.PathID != "" {
		if err := recovery.PathID.Validate(); err != nil {
			return err
		}
	}
	for name, value := range map[string]string{"path_name": recovery.PathName, "path_version": recovery.PathVersion, "step_id": recovery.StepID} {
		if err := validateBoundedText(name, value, 128, true); err != nil {
			return err
		}
	}
	if err := validateBoundedText("upstream_model", recovery.UpstreamModel, 256, true); err != nil {
		return err
	}
	if recovery.Action != "" && recovery.Action != "retry" && recovery.Action != "failover" {
		return fmt.Errorf("invalid recovery action")
	}
	if recovery.DelayMS < 0 || recovery.DelayMS > 60000 {
		return fmt.Errorf("invalid recovery delay")
	}
	if err := validateBoundedText("recovery reason", recovery.Reason, 128, true); err != nil {
		return err
	}
	return validateBoundedText("recovery stop reason", recovery.StopReason, 128, true)
}
