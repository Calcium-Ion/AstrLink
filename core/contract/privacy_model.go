package contract

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type PrivacyModelID string
type PrivacyModelCatalogID string
type PrivacyModelAdapter string
type PrivacyModelSource string
type PrivacyModelCatalogSource string
type PrivacyModelStatus string
type PrivacyModelInstallationError string
type CanonicalKind string

const (
	MaxPrivacyModelByteCount int64 = 9_007_199_254_740_991

	LegacyOpenAIPrivacyFilterInstallationID PrivacyModelID = "model_de5ac42e03b4af887b31a7645d3ce111"

	PrivacyModelAdapterOpenAIBIOES   PrivacyModelAdapter = "openai_bioes_viterbi"
	PrivacyModelAdapterHFToken       PrivacyModelAdapter = "hf_token_classification"
	PrivacyModelAdapterAstrLinkGuard PrivacyModelAdapter = "astrlink_sensitive_guard"

	PrivacyModelSourceCatalog PrivacyModelSource = "catalog"
	PrivacyModelSourceCustom  PrivacyModelSource = "custom"
	PrivacyModelSourceLocal   PrivacyModelSource = "local"

	PrivacyModelCatalogSourceOfficial  PrivacyModelCatalogSource = "official"
	PrivacyModelCatalogSourceCommunity PrivacyModelCatalogSource = "community"

	PrivacyModelStatusDownloading PrivacyModelStatus = "downloading"
	PrivacyModelStatusPaused      PrivacyModelStatus = "paused"
	PrivacyModelStatusReady       PrivacyModelStatus = "ready"
	PrivacyModelStatusError       PrivacyModelStatus = "error"

	PrivacyModelErrorDownload     PrivacyModelInstallationError = "download_failed"
	PrivacyModelErrorIntegrity    PrivacyModelInstallationError = "integrity_failed"
	PrivacyModelErrorIncompatible PrivacyModelInstallationError = "incompatible_model"

	CanonicalKindEmail        CanonicalKind = "email"
	CanonicalKindPhone        CanonicalKind = "phone"
	CanonicalKindAccount      CanonicalKind = "account"
	CanonicalKindPaymentCard  CanonicalKind = "payment_card"
	CanonicalKindIPAddress    CanonicalKind = "ip_address"
	CanonicalKindURL          CanonicalKind = "url"
	CanonicalKindCommonSecret CanonicalKind = "common_secret"
	CanonicalKindAddress      CanonicalKind = "private_address"
	CanonicalKindDate         CanonicalKind = "private_date"
	CanonicalKindPerson       CanonicalKind = "private_person"
)

var (
	privacyModelIDPattern = regexp.MustCompile(`^model_[0-9a-f]{32}$`)
	catalogIDPattern      = regexp.MustCompile(`^catalog_[a-z0-9_]{3,80}$`)
	repoIDPattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}/[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)
	revisionPattern       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	requestedRevision     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
	variantIDPattern      = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
	privacyModelLabel     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	localModelRepoPattern = regexp.MustCompile(`^local/model-[0-9a-f]{12}$`)
)

func (id PrivacyModelID) Validate() error {
	if !privacyModelIDPattern.MatchString(string(id)) {
		return fmt.Errorf("privacy model installation id is invalid")
	}
	return nil
}

func (id PrivacyModelCatalogID) Validate() error {
	if !catalogIDPattern.MatchString(string(id)) {
		return fmt.Errorf("privacy model catalog id is invalid")
	}
	return nil
}

func ValidatePrivacyModelRepoID(value string) error {
	if !repoIDPattern.MatchString(value) || strings.Contains(value, "..") {
		return fmt.Errorf("repo_id is invalid")
	}
	return nil
}

func ValidatePrivacyModelRevision(value string) error {
	if !revisionPattern.MatchString(value) {
		return fmt.Errorf("revision must be a 40-character lowercase commit")
	}
	return nil
}

func ValidateRequestedPrivacyModelRevision(value string) error {
	if !requestedRevision.MatchString(value) ||
		strings.Contains(value, "..") || strings.Contains(value, "//") ||
		strings.HasSuffix(value, "/") {
		return fmt.Errorf("requested revision is invalid")
	}
	return nil
}

func ValidatePrivacyModelVariantID(value string) error {
	if !variantIDPattern.MatchString(value) {
		return fmt.Errorf("variant_id is invalid")
	}
	return nil
}

func (adapter PrivacyModelAdapter) Valid() bool {
	return adapter == PrivacyModelAdapterOpenAIBIOES ||
		adapter == PrivacyModelAdapterHFToken ||
		adapter == PrivacyModelAdapterAstrLinkGuard
}

func (source PrivacyModelSource) Valid() bool {
	return source == PrivacyModelSourceCatalog ||
		source == PrivacyModelSourceCustom ||
		source == PrivacyModelSourceLocal
}

func (source PrivacyModelCatalogSource) Valid() bool {
	return source == PrivacyModelCatalogSourceOfficial ||
		source == PrivacyModelCatalogSourceCommunity
}

func (status PrivacyModelStatus) Valid() bool {
	return status == PrivacyModelStatusDownloading ||
		status == PrivacyModelStatusPaused ||
		status == PrivacyModelStatusReady ||
		status == PrivacyModelStatusError
}

func (modelError PrivacyModelInstallationError) Valid() bool {
	return modelError == PrivacyModelErrorDownload ||
		modelError == PrivacyModelErrorIntegrity ||
		modelError == PrivacyModelErrorIncompatible
}

func (kind CanonicalKind) Valid() bool {
	switch kind {
	case CanonicalKindEmail, CanonicalKindPhone, CanonicalKindAccount,
		CanonicalKindPaymentCard, CanonicalKindIPAddress, CanonicalKindURL,
		CanonicalKindCommonSecret, CanonicalKindAddress, CanonicalKindDate,
		CanonicalKindPerson:
		return true
	default:
		return false
	}
}

type PrivacyModelVariant struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Quantization      string  `json:"quantization"`
	BytesTotal        int64   `json:"bytes_total"`
	EstimatedRAMBytes int64   `json:"estimated_ram_bytes"`
	Recommended       bool    `json:"recommended"`
	Supported         bool    `json:"supported"`
	UnsupportedReason *string `json:"unsupported_reason"`
}

type PrivacyModelCatalogItem struct {
	ID        PrivacyModelCatalogID     `json:"id"`
	Name      string                    `json:"name"`
	Summary   string                    `json:"summary"`
	Source    PrivacyModelCatalogSource `json:"source"`
	RepoID    string                    `json:"repo_id"`
	Revision  string                    `json:"revision"`
	License   string                    `json:"license"`
	Languages []string                  `json:"languages"`
	Adapter   PrivacyModelAdapter       `json:"adapter"`
	Variants  []PrivacyModelVariant     `json:"variants"`
}

type PrivacyModelCatalogResponse struct {
	Items []PrivacyModelCatalogItem `json:"items"`
}

type PrivacyModelLabel struct {
	Label         string         `json:"label"`
	SuggestedKind *CanonicalKind `json:"suggested_kind"`
}

type PrivacyModelProbeRequest struct {
	RepoID   string `json:"repo_id"`
	Revision string `json:"revision"`
}

type PrivacyModelLocalProbeRequest struct {
	Path string `json:"path"`
}

type PrivacyModelProbeResponse struct {
	RepoID               string                `json:"repo_id"`
	RequestedRevision    string                `json:"requested_revision"`
	Revision             string                `json:"revision"`
	Name                 string                `json:"name"`
	License              *string               `json:"license"`
	Languages            []string              `json:"languages"`
	Adapter              PrivacyModelAdapter   `json:"adapter"`
	Variants             []PrivacyModelVariant `json:"variants"`
	Labels               []PrivacyModelLabel   `json:"labels"`
	RequiresLabelMapping bool                  `json:"requires_label_mapping"`
}

type PrivacyModelInstallRequest struct {
	RepoID       string                    `json:"repo_id"`
	Revision     string                    `json:"revision"`
	VariantID    string                    `json:"variant_id"`
	LabelMapping map[string]*CanonicalKind `json:"label_mapping"`
}

type PrivacyModelInstallation struct {
	ID                PrivacyModelID                 `json:"id"`
	Source            PrivacyModelSource             `json:"source"`
	CatalogID         *PrivacyModelCatalogID         `json:"catalog_id"`
	CatalogSource     *PrivacyModelCatalogSource     `json:"catalog_source"`
	Name              string                         `json:"name"`
	License           *string                        `json:"license"`
	Languages         []string                       `json:"languages"`
	RepoID            string                         `json:"repo_id"`
	Revision          string                         `json:"revision"`
	VariantID         string                         `json:"variant_id"`
	VariantName       string                         `json:"variant_name"`
	Quantization      string                         `json:"quantization"`
	Adapter           PrivacyModelAdapter            `json:"adapter"`
	Status            PrivacyModelStatus             `json:"status"`
	BytesDownloaded   int64                          `json:"bytes_downloaded"`
	BytesTotal        int64                          `json:"bytes_total"`
	EstimatedRAMBytes int64                          `json:"estimated_ram_bytes"`
	Error             *PrivacyModelInstallationError `json:"error"`
	LabelMapping      map[string]*CanonicalKind      `json:"label_mapping"`
	InstalledAt       *string                        `json:"installed_at"`
}

type PrivacyModelInstallationList struct {
	Items []PrivacyModelInstallation `json:"items"`
}

// ReadyPrivacyModelInstallation is an in-process-only handoff. Filesystem
// locations are deliberately not exposed by the control API.
type ReadyPrivacyModelInstallation struct {
	Directory      string
	Identity       string
	ManifestSHA256 string
}

func ValidatePrivacyModelInstallRequest(request PrivacyModelInstallRequest) error {
	if err := ValidatePrivacyModelRepoID(request.RepoID); err != nil {
		return err
	}
	if err := ValidatePrivacyModelRevision(request.Revision); err != nil {
		return err
	}
	if err := ValidatePrivacyModelVariantID(request.VariantID); err != nil {
		return err
	}
	if len(request.LabelMapping) > 256 {
		return fmt.Errorf("label_mapping contains too many entries")
	}
	for label, kind := range request.LabelMapping {
		if !privacyModelLabel.MatchString(label) {
			return fmt.Errorf("label_mapping label is invalid")
		}
		if kind != nil && !kind.Valid() {
			return fmt.Errorf("label_mapping kind is invalid")
		}
	}
	return nil
}

func ValidatePrivacyModelLocalProbeRequest(
	request PrivacyModelLocalProbeRequest,
) error {
	if request.Path == "" || len(request.Path) > 4096 ||
		!filepath.IsAbs(request.Path) ||
		strings.Contains(request.Path, "://") ||
		strings.IndexFunc(request.Path, unicode.IsControl) >= 0 {
		return fmt.Errorf("path must be an absolute native path")
	}
	return nil
}

func ValidatePrivacyModelProbeResponse(response PrivacyModelProbeResponse) error {
	if err := ValidatePrivacyModelRepoID(response.RepoID); err != nil {
		return err
	}
	if err := ValidateRequestedPrivacyModelRevision(
		response.RequestedRevision,
	); err != nil {
		return err
	}
	if err := ValidatePrivacyModelRevision(response.Revision); err != nil {
		return err
	}
	if !validPrivacyModelText(response.Name, 128) {
		return fmt.Errorf("privacy model name is invalid")
	}
	if err := validatePrivacyModelDisplayMetadata(
		response.License,
		response.Languages,
	); err != nil {
		return err
	}
	if len(response.Variants) == 0 || len(response.Variants) > 32 ||
		len(response.Labels) == 0 || len(response.Labels) > 256 ||
		!response.Adapter.Valid() {
		return fmt.Errorf("privacy model metadata is invalid")
	}
	variants := make(map[string]struct{}, len(response.Variants))
	for _, variant := range response.Variants {
		if err := validatePrivacyModelVariant(variant); err != nil {
			return err
		}
		if _, duplicate := variants[variant.ID]; duplicate {
			return fmt.Errorf("privacy model variant id is duplicated")
		}
		variants[variant.ID] = struct{}{}
	}
	labels := make(map[string]struct{}, len(response.Labels))
	requiresMapping := false
	for _, label := range response.Labels {
		if !privacyModelLabel.MatchString(label.Label) {
			return fmt.Errorf("privacy model label is invalid")
		}
		if _, duplicate := labels[label.Label]; duplicate {
			return fmt.Errorf("privacy model label is duplicated")
		}
		labels[label.Label] = struct{}{}
		if label.SuggestedKind == nil {
			requiresMapping = true
		} else if !label.SuggestedKind.Valid() {
			return fmt.Errorf("privacy model label suggestion is invalid")
		}
	}
	if response.RequiresLabelMapping != requiresMapping {
		return fmt.Errorf("requires_label_mapping is inconsistent")
	}
	return nil
}

func validatePrivacyModelVariant(variant PrivacyModelVariant) error {
	if err := ValidatePrivacyModelVariantID(variant.ID); err != nil {
		return err
	}
	if !validPrivacyModelText(variant.Name, 64) ||
		!validPrivacyModelText(variant.Quantization, 32) ||
		variant.BytesTotal < 0 ||
		variant.BytesTotal > MaxPrivacyModelByteCount ||
		variant.EstimatedRAMBytes < 0 ||
		variant.EstimatedRAMBytes > MaxPrivacyModelByteCount {
		return fmt.Errorf("privacy model variant is invalid")
	}
	if variant.Supported {
		if variant.BytesTotal <= 0 || variant.UnsupportedReason != nil {
			return fmt.Errorf("supported privacy model variant is invalid")
		}
	} else if variant.UnsupportedReason == nil ||
		*variant.UnsupportedReason != "cpu_only" {
		return fmt.Errorf("unsupported privacy model variant is invalid")
	}
	return nil
}

func ValidatePrivacyModelInstallation(installation PrivacyModelInstallation) error {
	if err := installation.ID.Validate(); err != nil {
		return err
	}
	if !installation.Source.Valid() {
		return fmt.Errorf("privacy model source is invalid")
	}
	if installation.CatalogID != nil {
		if err := installation.CatalogID.Validate(); err != nil {
			return err
		}
	}
	if installation.CatalogSource != nil &&
		!installation.CatalogSource.Valid() {
		return fmt.Errorf("privacy model catalog_source is invalid")
	}
	isCatalog := installation.Source == PrivacyModelSourceCatalog
	if isCatalog != (installation.CatalogID != nil) ||
		isCatalog != (installation.CatalogSource != nil) {
		return fmt.Errorf("privacy model catalog provenance is inconsistent")
	}
	if !validPrivacyModelText(installation.Name, 128) {
		return fmt.Errorf("privacy model name is invalid")
	}
	if err := validatePrivacyModelDisplayMetadata(
		installation.License,
		installation.Languages,
	); err != nil {
		return err
	}
	if err := ValidatePrivacyModelRepoID(installation.RepoID); err != nil {
		return err
	}
	isLocalRepo := localModelRepoPattern.MatchString(installation.RepoID)
	if (installation.Source == PrivacyModelSourceLocal) != isLocalRepo {
		return fmt.Errorf("privacy model local provenance is inconsistent")
	}
	if err := ValidatePrivacyModelRevision(installation.Revision); err != nil {
		return err
	}
	if err := ValidatePrivacyModelVariantID(installation.VariantID); err != nil {
		return err
	}
	if !validPrivacyModelText(installation.VariantName, 64) {
		return fmt.Errorf("privacy model variant name is invalid")
	}
	if !validPrivacyModelText(installation.Quantization, 32) {
		return fmt.Errorf("privacy model quantization is invalid")
	}
	if !installation.Adapter.Valid() || !installation.Status.Valid() {
		return fmt.Errorf("privacy model adapter or status is invalid")
	}
	if installation.BytesDownloaded < 0 || installation.BytesTotal < 0 ||
		installation.BytesDownloaded > installation.BytesTotal ||
		installation.BytesTotal > MaxPrivacyModelByteCount ||
		installation.EstimatedRAMBytes < 0 ||
		installation.EstimatedRAMBytes > MaxPrivacyModelByteCount {
		return fmt.Errorf("privacy model byte counts are invalid")
	}
	if installation.Error != nil && !installation.Error.Valid() {
		return fmt.Errorf("privacy model error is invalid")
	}
	if installation.InstalledAt != nil {
		if _, err := time.Parse(time.RFC3339Nano, *installation.InstalledAt); err != nil {
			return fmt.Errorf("installed_at is invalid")
		}
	}
	switch installation.Status {
	case PrivacyModelStatusDownloading, PrivacyModelStatusPaused:
		if installation.Error != nil || installation.InstalledAt != nil {
			return fmt.Errorf("downloading installation has invalid terminal fields")
		}
	case PrivacyModelStatusReady:
		if installation.BytesDownloaded != installation.BytesTotal ||
			installation.BytesTotal <= 0 ||
			installation.Error != nil ||
			installation.InstalledAt == nil {
			return fmt.Errorf("ready installation is incomplete")
		}
	case PrivacyModelStatusError:
		if installation.Error == nil || installation.InstalledAt != nil {
			return fmt.Errorf("failed installation has invalid terminal fields")
		}
	}
	if err := ValidatePrivacyModelInstallRequest(PrivacyModelInstallRequest{
		RepoID: installation.RepoID, Revision: installation.Revision,
		VariantID: installation.VariantID, LabelMapping: installation.LabelMapping,
	}); err != nil {
		return err
	}
	return validateResolvedPrivacyModelLabelMapping(
		installation.Adapter,
		installation.LabelMapping,
	)
}

func validatePrivacyModelDisplayMetadata(
	license *string,
	languages []string,
) error {
	if license != nil &&
		(!utf8.ValidString(*license) ||
			*license == "" ||
			utf8.RuneCountInString(*license) > 64 ||
			strings.TrimSpace(*license) != *license ||
			strings.IndexFunc(*license, unicode.IsControl) >= 0) {
		return fmt.Errorf("privacy model license is invalid")
	}
	if languages == nil || len(languages) > 32 {
		return fmt.Errorf("privacy model languages are invalid")
	}
	seen := make(map[string]struct{}, len(languages))
	for _, language := range languages {
		if !utf8.ValidString(language) ||
			language == "" || utf8.RuneCountInString(language) > 64 ||
			strings.TrimSpace(language) != language ||
			strings.IndexFunc(language, unicode.IsControl) >= 0 {
			return fmt.Errorf("privacy model language is invalid")
		}
		if _, exists := seen[language]; exists {
			return fmt.Errorf("privacy model language is duplicated")
		}
		seen[language] = struct{}{}
	}
	return nil
}

func validPrivacyModelText(value string, maxRunes int) bool {
	return value != "" &&
		utf8.ValidString(value) &&
		utf8.RuneCountInString(value) <= maxRunes &&
		strings.TrimSpace(value) == value &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func validateResolvedPrivacyModelLabelMapping(
	adapter PrivacyModelAdapter,
	mapping map[string]*CanonicalKind,
) error {
	if adapter == PrivacyModelAdapterHFToken {
		if len(mapping) == 0 {
			return fmt.Errorf("privacy model label_mapping is empty")
		}
		return nil
	}
	required := [...]string{
		"account_number",
		"private_address",
		"private_date",
		"private_email",
		"private_person",
		"private_phone",
		"private_url",
		"secret",
	}
	if len(mapping) != len(required) {
		return fmt.Errorf("privacy model label_mapping is incomplete")
	}
	for _, label := range required {
		if _, exists := mapping[label]; !exists {
			return fmt.Errorf("privacy model label_mapping is incomplete")
		}
	}
	return nil
}
