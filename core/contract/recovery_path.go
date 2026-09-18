package contract

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type RecoveryPathID string

func (id RecoveryPathID) Validate() error { return ServiceID(id).Validate() }

type RecoveryPathNode struct {
	ID               string     `json:"id"`
	ServiceID        ServiceID  `json:"service_id"`
	UpstreamModel    string     `json:"upstream_model,omitempty"`
	UpstreamProtocol ProtocolID `json:"upstream_protocol"`
	PlanType         PlanType   `json:"plan_type"`
	MaxRetries       *int       `json:"max_retries,omitempty"`
}

func (node RecoveryPathNode) Target(priority int) RouteTarget {
	return RouteTarget{ServiceID: node.ServiceID, UpstreamModel: node.UpstreamModel, UpstreamProtocol: node.UpstreamProtocol, PlanType: node.PlanType, Priority: priority}
}
func (node RecoveryPathNode) Key() string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s", node.ServiceID, node.UpstreamModel, node.UpstreamProtocol, node.PlanType)
}

type RecoveryPath struct {
	ID            RecoveryPathID     `json:"id"`
	Name          string             `json:"name"`
	Protocol      ProtocolID         `json:"protocol"`
	Mode          string             `json:"mode"`
	Targets       []RecoveryPathNode `json:"targets,omitempty"`
	Steps         []RecoveryPathNode `json:"steps,omitempty"`
	Strategy      *FailoverStrategy  `json:"strategy,omitempty"`
	MaxAttempts   *int               `json:"max_attempts,omitempty"`
	FailurePolicy *FailurePolicy     `json:"failure_policy,omitempty"`
}

func (path RecoveryPath) Nodes() []RecoveryPathNode {
	if path.Mode == "steps" {
		return path.Steps
	}
	return path.Targets
}
func (path RecoveryPath) Validate() error {
	if err := path.ID.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(path.Name) == "" || utf8.RuneCountInString(path.Name) > 128 {
		return fmt.Errorf("path name must contain 1–128 characters")
	}
	if err := path.Protocol.Validate(); err != nil {
		return err
	}
	if !path.Protocol.AvailableInAlpha() || path.Protocol.IsModelDiscovery() {
		return fmt.Errorf("path requires an inference protocol")
	}
	if path.Mode != "automatic" && path.Mode != "steps" {
		return fmt.Errorf("mode must be automatic or steps")
	}
	if path.Mode == "steps" && (path.Targets != nil || path.Strategy != nil) || path.Mode == "automatic" && path.Steps != nil {
		return fmt.Errorf("targets and strategy belong to automatic mode; steps belong to steps mode")
	}
	limit := 200
	if path.Mode == "steps" {
		limit = 20
	}
	if len(path.Nodes()) < 1 || len(path.Nodes()) > limit {
		return fmt.Errorf("path must contain 1–%d nodes", limit)
	}
	if path.Strategy != nil && *path.Strategy != RetryFirst && *path.Strategy != FailoverFirst {
		return fmt.Errorf("invalid path strategy")
	}
	if path.MaxAttempts != nil && (*path.MaxAttempts < 1 || *path.MaxAttempts > 20) {
		return fmt.Errorf("max_attempts must be 1–20")
	}
	if path.FailurePolicy != nil {
		if err := path.FailurePolicy.Validate(); err != nil {
			return err
		}
	}
	ids, counts := map[string]bool{}, map[string]int{}
	for _, node := range path.Nodes() {
		if err := ServiceID(node.ID).Validate(); err != nil {
			return fmt.Errorf("node id: %w", err)
		}
		if ids[node.ID] {
			return fmt.Errorf("duplicate node id")
		}
		ids[node.ID] = true
		if err := node.ServiceID.Validate(); err != nil {
			return err
		}
		if !node.PlanType.Valid() {
			return fmt.Errorf("unsupported execution type")
		}
		if err := node.UpstreamProtocol.Validate(); err != nil {
			return err
		}
		if node.PlanType != PlanTypeRelayKit && node.UpstreamProtocol != path.Protocol {
			return fmt.Errorf("native/delegated targets must preserve protocol")
		}
		if utf8.RuneCountInString(node.UpstreamModel) > 256 {
			return fmt.Errorf("model exceeds 256 characters")
		}
		if node.MaxRetries != nil && (path.Mode == "steps" || *node.MaxRetries < 0 || *node.MaxRetries > 5) {
			return fmt.Errorf("max_retries is only available in automatic mode, from 0 to 5")
		}
		counts[node.Key()]++
		if path.Mode == "automatic" && counts[node.Key()] > 1 || counts[node.Key()] > 6 {
			return fmt.Errorf("duplicate automatic target or more than six attempts for one target")
		}
	}
	return nil
}

type RecoveryPathReference struct {
	RouteID    RouteID    `json:"route_id,omitempty"`
	Name       string     `json:"name"`
	CategoryID string     `json:"category_id,omitempty"`
	Protocol   ProtocolID `json:"protocol"`
	Override   bool       `json:"override"`
}
type RecoveryPathRecord struct {
	Path       RecoveryPath            `json:"path"`
	ETag       string                  `json:"etag"`
	References []RecoveryPathReference `json:"references"`
}
type RecoveryPreviewInput struct {
	Path       RecoveryPath `json:"path"`
	Model      string       `json:"model,omitempty"`
	Streaming  bool         `json:"streaming"`
	Error      string       `json:"error"`
	SuccessAt  int          `json:"success_at,omitempty"`
	RetryAfter string       `json:"retry_after,omitempty"`
	RouteID    RouteID      `json:"route_id,omitempty"`
	CategoryID string       `json:"category_id,omitempty"`
}
type RecoveryPreviewStep struct {
	StepID    string    `json:"step_id"`
	ServiceID ServiceID `json:"service_id"`
	Model     string    `json:"model"`
	Action    string    `json:"action"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	WaitMinMS int       `json:"wait_min_ms"`
	WaitMaxMS int       `json:"wait_max_ms"`
}
type RecoveryPreview struct {
	Steps       []RecoveryPreviewStep `json:"steps"`
	StopReason  string                `json:"stop_reason"`
	MaxAttempts int                   `json:"max_attempts"`
}
