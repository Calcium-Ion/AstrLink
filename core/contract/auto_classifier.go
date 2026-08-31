package contract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"unicode/utf8"
)

type AutoClassifierID string
type AutoClassifierArtifactTier string
type AutoClassifierStatus string

const (
	AutoClassifierArtifactExperimental AutoClassifierArtifactTier = "experimental"
	AutoClassifierArtifactReleased     AutoClassifierArtifactTier = "released"

	AutoClassifierStatusReady AutoClassifierStatus = "ready"

	MaxAutoClassifierPreviewBytes = 1 << 20
)

var autoClassifierIDPattern = regexp.MustCompile(`^classifier_[0-9a-f]{32}$`)

func (id AutoClassifierID) Validate() error {
	if !autoClassifierIDPattern.MatchString(string(id)) {
		return fmt.Errorf("auto classifier installation id is invalid")
	}
	return nil
}

func (tier AutoClassifierArtifactTier) Valid() bool {
	return tier == AutoClassifierArtifactExperimental ||
		tier == AutoClassifierArtifactReleased
}

func EligibleForRouting(tier AutoClassifierArtifactTier) bool {
	return tier.Valid()
}

type AutoClassifierLocalProbeRequest struct {
	Path string `json:"path"`
}

func ValidateAutoClassifierLocalProbeRequest(request AutoClassifierLocalProbeRequest) error {
	if request.Path == "" || !utf8.ValidString(request.Path) {
		return fmt.Errorf("local classifier path is required")
	}
	if len(request.Path) > 4096 {
		return fmt.Errorf("local classifier path is too long")
	}
	return nil
}

type AutoClassifierProbeResponse struct {
	RepoID             string                     `json:"repo_id"`
	Revision           string                     `json:"revision"`
	Name               string                     `json:"name"`
	ArtifactTier       AutoClassifierArtifactTier `json:"artifact_tier"`
	EligibleForRouting bool                       `json:"eligible_for_routing"`
	TaxonomyID         string                     `json:"taxonomy_id"`
	TaxonomySHA256     string                     `json:"taxonomy_sha256"`
	BytesTotal         int64                      `json:"bytes_total"`
	ID2Label           []string                   `json:"id2label"`
}

func (response AutoClassifierProbeResponse) Validate() error {
	if response.RepoID == "" || response.Revision == "" || response.Name == "" {
		return fmt.Errorf("classifier probe response is incomplete")
	}
	if !response.ArtifactTier.Valid() {
		return fmt.Errorf("classifier artifact tier is invalid")
	}
	if response.EligibleForRouting != EligibleForRouting(response.ArtifactTier) {
		return fmt.Errorf("classifier routing eligibility does not match artifact tier")
	}
	if response.TaxonomyID != AstrLinkTextClassificationV1 {
		return fmt.Errorf("classifier taxonomy id is invalid")
	}
	if response.BytesTotal <= 0 {
		return fmt.Errorf("classifier byte count is invalid")
	}
	if len(response.ID2Label) != 4 {
		return fmt.Errorf("classifier id2label is invalid")
	}
	return nil
}

type AutoClassifierInstallRequest struct {
	Path string `json:"path"`
}

func ValidateAutoClassifierInstallRequest(request AutoClassifierInstallRequest) error {
	return ValidateAutoClassifierLocalProbeRequest(AutoClassifierLocalProbeRequest(request))
}

type AutoClassifierInstallation struct {
	ID                 AutoClassifierID           `json:"id"`
	Identity           string                     `json:"identity"`
	Name               string                     `json:"name"`
	ArtifactTier       AutoClassifierArtifactTier `json:"artifact_tier"`
	EligibleForRouting bool                       `json:"eligible_for_routing"`
	TaxonomyID         string                     `json:"taxonomy_id"`
	Status             AutoClassifierStatus       `json:"status"`
	BytesTotal         int64                      `json:"bytes_total"`
}

func (installation AutoClassifierInstallation) Validate() error {
	if err := installation.ID.Validate(); err != nil {
		return err
	}
	if installation.Identity == "" || installation.Name == "" {
		return fmt.Errorf("classifier installation is incomplete")
	}
	if !installation.ArtifactTier.Valid() {
		return fmt.Errorf("classifier artifact tier is invalid")
	}
	if installation.EligibleForRouting != EligibleForRouting(installation.ArtifactTier) {
		return fmt.Errorf("classifier routing eligibility does not match artifact tier")
	}
	if installation.TaxonomyID != AstrLinkTextClassificationV1 {
		return fmt.Errorf("classifier taxonomy id is invalid")
	}
	if installation.Status != AutoClassifierStatusReady {
		return fmt.Errorf("classifier installation status is invalid")
	}
	if installation.BytesTotal <= 0 {
		return fmt.Errorf("classifier byte count is invalid")
	}
	return nil
}

type AutoClassifierInstallationList struct {
	Items []AutoClassifierInstallation `json:"items"`
}

type ReadyAutoClassifierInstallation struct {
	ID             AutoClassifierID
	Directory      string
	Identity       string
	ManifestSHA256 string
	ArtifactTier   AutoClassifierArtifactTier
}

type AutoClassifierClassifyPreviewRequest struct {
	Text     string          `json:"text,omitempty"`
	Protocol ProtocolID      `json:"protocol,omitempty"`
	Body     json.RawMessage `json:"body,omitempty"`
}

func (request AutoClassifierClassifyPreviewRequest) Validate() error {
	hasText := request.Text != ""
	hasBody := len(request.Body) > 0
	if hasText == hasBody {
		return fmt.Errorf("classify preview requires exactly one of text or body")
	}
	if hasText && (!utf8.ValidString(request.Text) || len(request.Text) > MaxAutoClassifierPreviewBytes) {
		return fmt.Errorf("classify preview text is invalid")
	}
	if hasBody {
		if request.Protocol == "" || !request.Protocol.Valid() {
			return fmt.Errorf("classify preview body requires a protocol")
		}
		if len(request.Body) > MaxAutoClassifierPreviewBytes {
			return fmt.Errorf("classify preview body is too large")
		}
	}
	return nil
}

type AutoClassifierClassifyPreviewResponse struct {
	Category           string                     `json:"category,omitempty"`
	Logits             []float32                  `json:"logits,omitempty"`
	LatencyMS          int64                      `json:"latency_ms"`
	ArtifactTier       AutoClassifierArtifactTier `json:"artifact_tier"`
	EligibleForRouting bool                       `json:"eligible_for_routing"`
	FallbackReason     string                     `json:"fallback_reason,omitempty"`
}
