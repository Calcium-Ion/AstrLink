package privacymodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/QuantumNous/astrlink/core/contract"
)

const maxInstallationManifestBytes = 256 << 10

var tensorNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)

type normalizedInputNames struct {
	InputIDs      string  `json:"input_ids"`
	AttentionMask string  `json:"attention_mask"`
	TokenTypeIDs  *string `json:"token_type_ids"`
}

type normalizedFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type normalizedManifest struct {
	Version               int                                `json:"version"`
	InstallationID        contract.PrivacyModelID            `json:"installation_id"`
	Identity              string                             `json:"identity"`
	RepoID                string                             `json:"repo_id"`
	Revision              string                             `json:"revision"`
	VariantID             string                             `json:"variant_id"`
	Adapter               contract.PrivacyModelAdapter       `json:"adapter"`
	ModelPath             string                             `json:"model_path"`
	ExternalData          []string                           `json:"external_data_paths"`
	TokenizerPath         string                             `json:"tokenizer_path"`
	ConfigPath            string                             `json:"config_path"`
	CalibrationPath       *string                            `json:"calibration_path"`
	SecretRulesPath       *string                            `json:"secret_rules_path"`
	SecretCalibrationPath *string                            `json:"secret_calibration_path"`
	TagScheme             string                             `json:"tag_scheme"`
	Window                int                                `json:"window"`
	Stride                int                                `json:"stride"`
	MaxRequestTokens      int                                `json:"max_request_tokens"`
	InputNames            normalizedInputNames               `json:"input_names"`
	OutputName            string                             `json:"output_name"`
	LabelMapping          map[string]*contract.CanonicalKind `json:"label_mapping"`
	Files                 []normalizedFile                   `json:"files"`
}

func buildNormalizedManifest(
	installation contract.PrivacyModelInstallation,
	runtime runtimeSpec,
	assets []Asset,
) normalizedManifest {
	files := make([]normalizedFile, len(assets))
	for index, asset := range assets {
		files[index] = normalizedFile{
			Path: asset.Path, Size: asset.Size, SHA256: asset.SHA256,
		}
	}
	return normalizedManifest{
		Version: 1, InstallationID: installation.ID,
		Identity: installation.RepoID + "@" + installation.Revision + "#" + installation.VariantID,
		RepoID:   installation.RepoID, Revision: installation.Revision,
		VariantID: installation.VariantID,
		Adapter:   installation.Adapter, ModelPath: runtime.modelPath,
		ExternalData:  append([]string{}, runtime.externalData...),
		TokenizerPath: runtime.tokenizerPath, ConfigPath: runtime.configPath,
		CalibrationPath:       cloneString(runtime.calibrationPath),
		SecretRulesPath:       cloneString(runtime.secretRulesPath),
		SecretCalibrationPath: cloneString(runtime.secretCalibrationPath),
		TagScheme:             runtime.tagScheme, Window: runtime.window,
		Stride: runtime.stride, MaxRequestTokens: runtime.maxRequestTokens,
		InputNames: runtime.inputNames, OutputName: runtime.outputName,
		LabelMapping: cloneLabelMapping(installation.LabelMapping), Files: files,
	}
}

func writeNormalizedManifest(directory string, manifest normalizedManifest) error {
	if err := validateNormalizedManifest(manifest); err != nil {
		return err
	}
	document, err := json.Marshal(manifest)
	if err != nil || len(document) > maxInstallationManifestBytes {
		return ErrFilesystem
	}
	file, err := os.OpenFile(
		filepath.Join(directory, InstallationManifestName),
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return ErrFilesystem
	}
	if _, err := file.Write(document); err != nil {
		_ = file.Close()
		return ErrFilesystem
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return ErrFilesystem
	}
	if err := file.Close(); err != nil {
		return ErrFilesystem
	}
	return nil
}

func inspectNormalizedInstallation(
	ctx context.Context,
	directory string,
	expectedID contract.PrivacyModelID,
) (normalizedManifest, error) {
	manifest, _, err := inspectNormalizedInstallationDocument(
		ctx,
		directory,
		expectedID,
	)
	return manifest, err
}

func inspectNormalizedInstallationDocument(
	ctx context.Context,
	directory string,
	expectedID contract.PrivacyModelID,
) (normalizedManifest, []byte, error) {
	manifest, document, err := readNormalizedInstallationDocument(
		directory,
		expectedID,
	)
	if err != nil {
		return normalizedManifest{}, nil, err
	}
	for _, verified := range manifest.Files {
		if err := inspectPublishedAsset(ctx, directory, Asset{
			Path: verified.Path, Size: verified.Size, SHA256: verified.SHA256,
		}); err != nil {
			if ctx.Err() != nil {
				return normalizedManifest{}, nil, ctx.Err()
			}
			return normalizedManifest{}, nil, ErrFilesystem
		}
	}
	return manifest, document, nil
}

func readNormalizedInstallationDocument(
	directory string,
	expectedID contract.PrivacyModelID,
) (normalizedManifest, []byte, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return normalizedManifest{}, nil, ErrFilesystem
	}
	manifestPath := filepath.Join(directory, InstallationManifestName)
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() ||
		manifestInfo.Mode()&os.ModeSymlink != 0 ||
		manifestInfo.Size() <= 0 ||
		manifestInfo.Size() > maxInstallationManifestBytes {
		return normalizedManifest{}, nil, ErrFilesystem
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return normalizedManifest{}, nil, ErrFilesystem
	}
	document, readErr := io.ReadAll(io.LimitReader(
		file,
		maxInstallationManifestBytes+1,
	))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil ||
		len(document) == 0 ||
		len(document) > maxInstallationManifestBytes {
		return normalizedManifest{}, nil, ErrFilesystem
	}
	var manifest normalizedManifest
	if strictDecodeJSON(document, &manifest) != nil ||
		validateNormalizedManifest(manifest) != nil ||
		manifest.InstallationID != expectedID {
		return normalizedManifest{}, nil, ErrFilesystem
	}
	return manifest, document, nil
}

func validateNormalizedManifest(manifest normalizedManifest) error {
	if manifest.Version != 1 || manifest.InstallationID.Validate() != nil ||
		manifest.Identity == "" || len(manifest.Identity) > 512 ||
		!manifest.Adapter.Valid() || len(manifest.Files) == 0 ||
		len(manifest.Files) > 128 || manifest.ExternalData == nil {
		return ErrInvalidConfig
	}
	if contract.ValidatePrivacyModelRepoID(manifest.RepoID) != nil ||
		contract.ValidatePrivacyModelRevision(manifest.Revision) != nil ||
		contract.ValidatePrivacyModelVariantID(manifest.VariantID) != nil ||
		manifest.Identity != manifest.RepoID+"@"+manifest.Revision+"#"+manifest.VariantID {
		return ErrInvalidConfig
	}
	if manifest.TagScheme != "bio" && manifest.TagScheme != "bioes" {
		return ErrInvalidConfig
	}
	if manifest.Adapter == contract.PrivacyModelAdapterOpenAIBIOES &&
		(manifest.TagScheme != "bioes" ||
			manifest.CalibrationPath == nil ||
			manifest.SecretRulesPath != nil ||
			manifest.SecretCalibrationPath != nil) {
		return ErrInvalidConfig
	}
	if manifest.Adapter == contract.PrivacyModelAdapterHFToken &&
		(manifest.CalibrationPath != nil ||
			manifest.SecretRulesPath != nil ||
			manifest.SecretCalibrationPath != nil) {
		return ErrInvalidConfig
	}
	if manifest.Adapter == contract.PrivacyModelAdapterPPLXBIOES &&
		(manifest.TagScheme != "bioes" || manifest.Window > 4096 ||
			manifest.InputNames.TokenTypeIDs != nil || manifest.CalibrationPath != nil ||
			manifest.SecretRulesPath != nil || manifest.SecretCalibrationPath != nil ||
			!sameLabelKeys(manifest.LabelMapping, defaultPPLXLabelMapping())) {
		return ErrInvalidConfig
	}
	if manifest.Adapter == contract.PrivacyModelAdapterAstrLinkGuard &&
		(manifest.TagScheme != "bioes" ||
			manifest.CalibrationPath == nil ||
			manifest.SecretRulesPath == nil ||
			manifest.SecretCalibrationPath == nil) {
		return ErrInvalidConfig
	}
	if (manifest.Adapter == contract.PrivacyModelAdapterOpenAIBIOES ||
		manifest.Adapter == contract.PrivacyModelAdapterAstrLinkGuard) &&
		!sameLabelKeys(
			manifest.LabelMapping,
			defaultOpenAILabelMapping(),
		) {
		return ErrInvalidConfig
	}
	if manifest.Adapter == contract.PrivacyModelAdapterHFToken &&
		len(manifest.LabelMapping) == 0 {
		return ErrInvalidConfig
	}
	if manifest.Window <= 0 || manifest.Stride < 0 ||
		manifest.Stride >= manifest.Window ||
		manifest.Window > manifest.MaxRequestTokens ||
		manifest.MaxRequestTokens > 131_072 {
		return ErrInvalidConfig
	}
	if !tensorNamePattern.MatchString(manifest.InputNames.InputIDs) ||
		!tensorNamePattern.MatchString(manifest.InputNames.AttentionMask) ||
		!tensorNamePattern.MatchString(manifest.OutputName) ||
		manifest.InputNames.InputIDs == manifest.InputNames.AttentionMask ||
		manifest.OutputName == manifest.InputNames.InputIDs ||
		manifest.OutputName == manifest.InputNames.AttentionMask {
		return ErrInvalidConfig
	}
	if manifest.InputNames.TokenTypeIDs != nil &&
		(!tensorNamePattern.MatchString(*manifest.InputNames.TokenTypeIDs) ||
			*manifest.InputNames.TokenTypeIDs == manifest.InputNames.InputIDs ||
			*manifest.InputNames.TokenTypeIDs == manifest.InputNames.AttentionMask ||
			*manifest.InputNames.TokenTypeIDs == manifest.OutputName) {
		return ErrInvalidConfig
	}
	if !strings.HasSuffix(strings.ToLower(manifest.ModelPath), ".onnx") ||
		!strings.HasSuffix(strings.ToLower(manifest.TokenizerPath), ".json") ||
		!strings.HasSuffix(strings.ToLower(manifest.ConfigPath), ".json") ||
		manifest.ModelPath == InstallationManifestName ||
		manifest.TokenizerPath == InstallationManifestName ||
		manifest.ConfigPath == InstallationManifestName {
		return ErrInvalidConfig
	}
	for _, external := range manifest.ExternalData {
		suffix := strings.TrimPrefix(external, manifest.ModelPath)
		if !externalDataSuffixPattern.MatchString(suffix) {
			return ErrInvalidConfig
		}
	}
	if manifest.CalibrationPath != nil &&
		(!strings.HasSuffix(
			strings.ToLower(*manifest.CalibrationPath),
			".json",
		) ||
			*manifest.CalibrationPath == InstallationManifestName) {
		return ErrInvalidConfig
	}
	if manifest.SecretRulesPath != nil &&
		((!strings.HasSuffix(strings.ToLower(*manifest.SecretRulesPath), ".json") &&
			!strings.HasSuffix(strings.ToLower(*manifest.SecretRulesPath), ".yaml")) ||
			*manifest.SecretRulesPath == InstallationManifestName) {
		return ErrInvalidConfig
	}
	if manifest.SecretCalibrationPath != nil &&
		(!strings.HasSuffix(strings.ToLower(*manifest.SecretCalibrationPath), ".json") ||
			*manifest.SecretCalibrationPath == InstallationManifestName) {
		return ErrInvalidConfig
	}
	assets := make([]Asset, len(manifest.Files))
	paths := make(map[string]struct{}, len(manifest.Files))
	for index, verified := range manifest.Files {
		assets[index] = Asset{
			Path: verified.Path, Size: verified.Size, SHA256: verified.SHA256,
		}
		paths[verified.Path] = struct{}{}
	}
	if _, _, err := validateManifest(Manifest{
		Revision: "normalized", Assets: assets,
	}); err != nil {
		return err
	}
	required := []string{
		manifest.ModelPath, manifest.TokenizerPath, manifest.ConfigPath,
	}
	required = append(required, manifest.ExternalData...)
	if manifest.CalibrationPath != nil {
		required = append(required, *manifest.CalibrationPath)
	}
	if manifest.SecretRulesPath != nil {
		required = append(required, *manifest.SecretRulesPath)
	}
	if manifest.SecretCalibrationPath != nil {
		required = append(required, *manifest.SecretCalibrationPath)
	}
	requiredPaths := make(map[string]struct{}, len(required))
	for _, candidate := range required {
		if !safeAssetPath(candidate) {
			return ErrInvalidConfig
		}
		if _, duplicate := requiredPaths[candidate]; duplicate {
			return ErrInvalidConfig
		}
		requiredPaths[candidate] = struct{}{}
		if _, exists := paths[candidate]; !exists {
			return ErrInvalidConfig
		}
	}
	if len(manifest.LabelMapping) > 256 {
		return ErrInvalidConfig
	}
	for label, kind := range manifest.LabelMapping {
		if strings.TrimSpace(label) != label || label == "" || len(label) > 128 ||
			(kind != nil && !kind.Valid()) {
			return ErrInvalidConfig
		}
	}
	return nil
}

func safeAssetPath(candidate string) bool {
	cleaned := path.Clean(candidate)
	return len(candidate) <= 512 &&
		strings.IndexFunc(candidate, unicode.IsControl) < 0 &&
		cleaned == candidate && cleaned != "." &&
		!strings.HasPrefix(cleaned, "/") &&
		!strings.HasPrefix(cleaned, "../") &&
		!strings.ContainsAny(candidate, `\:`)
}

func cloneLabelMapping(
	source map[string]*contract.CanonicalKind,
) map[string]*contract.CanonicalKind {
	result := make(map[string]*contract.CanonicalKind, len(source))
	for label, kind := range source {
		if kind == nil {
			result[label] = nil
			continue
		}
		value := *kind
		result[label] = &value
	}
	return result
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func defaultOpenAILabelMapping() map[string]*contract.CanonicalKind {
	values := map[string]contract.CanonicalKind{
		"account_number":  contract.CanonicalKindAccount,
		"private_address": contract.CanonicalKindAddress,
		"private_date":    contract.CanonicalKindDate,
		"private_email":   contract.CanonicalKindEmail,
		"private_person":  contract.CanonicalKindPerson,
		"private_phone":   contract.CanonicalKindPhone,
		"private_url":     contract.CanonicalKindURL,
		"secret":          contract.CanonicalKindCommonSecret,
	}
	return resolvedLabelMapping(values)
}

func defaultNymLabelMapping() map[string]*contract.CanonicalKind {
	return resolvedLabelMapping(nymLabelKinds())
}

func nymLabelKinds() map[string]contract.CanonicalKind {
	return map[string]contract.CanonicalKind{
		"ACCOUNT_NUMBER":        contract.CanonicalKindAccount,
		"AGE":                   contract.CanonicalKindDate,
		"API_KEY":               contract.CanonicalKindCommonSecret,
		"BUILDING_NUMBER":       contract.CanonicalKindAddress,
		"CITY":                  contract.CanonicalKindAddress,
		"COMPANY_NAME":          contract.CanonicalKindPerson,
		"COUNTRY":               contract.CanonicalKindAddress,
		"CREDIT_DEBIT_CARD":     contract.CanonicalKindPaymentCard,
		"CUSTOMER_ID":           contract.CanonicalKindAccount,
		"CVV":                   contract.CanonicalKindPaymentCard,
		"DATE":                  contract.CanonicalKindDate,
		"DATE_OF_BIRTH":         contract.CanonicalKindDate,
		"DRIVERS_LICENSE":       contract.CanonicalKindAccount,
		"EMAIL":                 contract.CanonicalKindEmail,
		"EMPLOYEE_ID":           contract.CanonicalKindAccount,
		"FAX_NUMBER":            contract.CanonicalKindPhone,
		"GENDER":                contract.CanonicalKindPerson,
		"GIVEN_NAME":            contract.CanonicalKindPerson,
		"GOVERNMENT_ID":         contract.CanonicalKindAccount,
		"IBAN":                  contract.CanonicalKindAccount,
		"LICENSE_PLATE":         contract.CanonicalKindAccount,
		"MAC_ADDRESS":           contract.CanonicalKindIPAddress,
		"MEDICAL_RECORD_NUMBER": contract.CanonicalKindAccount,
		"PASSPORT":              contract.CanonicalKindAccount,
		"PASSWORD":              contract.CanonicalKindCommonSecret,
		"PHONE":                 contract.CanonicalKindPhone,
		"PIN":                   contract.CanonicalKindCommonSecret,
		"ROUTING_NUMBER":        contract.CanonicalKindAccount,
		"SECONDARY_ADDRESS":     contract.CanonicalKindAddress,
		"SSN":                   contract.CanonicalKindAccount,
		"STATE":                 contract.CanonicalKindAddress,
		"STREET_ADDRESS":        contract.CanonicalKindAddress,
		"STREET_NAME":           contract.CanonicalKindAddress,
		"SURNAME":               contract.CanonicalKindPerson,
		"SWIFT_BIC":             contract.CanonicalKindAccount,
		"TAX_ID":                contract.CanonicalKindAccount,
		"TIME":                  contract.CanonicalKindDate,
		"URL":                   contract.CanonicalKindURL,
		"USERNAME":              contract.CanonicalKindAccount,
		"ZIP_CODE":              contract.CanonicalKindAddress,
	}
}

func resolvedLabelMapping(
	values map[string]contract.CanonicalKind,
) map[string]*contract.CanonicalKind {
	result := make(map[string]*contract.CanonicalKind, len(values))
	for label, kind := range values {
		value := kind
		result[label] = &value
	}
	return result
}

func strictDecodeJSON(document []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
