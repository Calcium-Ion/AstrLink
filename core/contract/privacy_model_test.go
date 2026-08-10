package contract

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatePrivacyModelInstallationDisplayMetadataProvenance(t *testing.T) {
	catalogID := PrivacyModelCatalogID("catalog_test_model")
	catalogSource := PrivacyModelCatalogSourceCommunity
	license := "Apache-2.0"
	installedAt := "2026-07-24T00:00:00Z"
	email := CanonicalKindEmail
	base := PrivacyModelInstallation{
		ID:                PrivacyModelID("model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		Source:            PrivacyModelSourceCatalog,
		CatalogID:         &catalogID,
		CatalogSource:     &catalogSource,
		Name:              "Test Model",
		License:           &license,
		Languages:         []string{"en"},
		RepoID:            "acme/privacy-model",
		Revision:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		VariantID:         "q4",
		VariantName:       "CPU Q4",
		Quantization:      "q4",
		Adapter:           PrivacyModelAdapterHFToken,
		Status:            PrivacyModelStatusReady,
		BytesDownloaded:   1,
		BytesTotal:        1,
		EstimatedRAMBytes: 2,
		LabelMapping:      map[string]*CanonicalKind{"EMAIL": &email},
		InstalledAt:       &installedAt,
	}
	if err := ValidatePrivacyModelInstallation(base); err != nil {
		t.Fatalf("valid catalog installation: %v", err)
	}
	international := base
	international.Name = strings.Repeat("隐", 50)
	international.VariantName = strings.Repeat("量", 30)
	if err := ValidatePrivacyModelInstallation(international); err != nil {
		t.Fatalf("valid rune-counted installation metadata: %v", err)
	}

	custom := base
	custom.Source = PrivacyModelSourceCustom
	custom.CatalogID = nil
	custom.CatalogSource = nil
	custom.License = nil
	custom.Languages = []string{}
	if err := ValidatePrivacyModelInstallation(custom); err != nil {
		t.Fatalf("valid custom installation: %v", err)
	}
	remoteLocalOwner := custom
	remoteLocalOwner.RepoID = "local/privacy-filter"
	if err := ValidatePrivacyModelInstallation(remoteLocalOwner); err != nil {
		t.Fatalf("valid public repository owned by local: %v", err)
	}

	local := custom
	local.Source = PrivacyModelSourceLocal
	local.RepoID = "local/model-aaaaaaaaaaaa"
	if err := ValidatePrivacyModelInstallation(local); err != nil {
		t.Fatalf("valid local installation: %v", err)
	}

	for name, mutate := range map[string]func(*PrivacyModelInstallation){
		"catalog without catalog source": func(value *PrivacyModelInstallation) {
			value.CatalogSource = nil
		},
		"custom with catalog source": func(value *PrivacyModelInstallation) {
			value.Source = PrivacyModelSourceCustom
			value.CatalogID = nil
		},
		"nil languages": func(value *PrivacyModelInstallation) {
			value.Languages = nil
		},
		"duplicate languages": func(value *PrivacyModelInstallation) {
			value.Languages = []string{"en", "en"}
		},
		"trimmed name": func(value *PrivacyModelInstallation) {
			value.Name = " Test Model"
		},
		"controlled variant name": func(value *PrivacyModelInstallation) {
			value.VariantName = "CPU\nQ4"
		},
		"empty resolved mapping": func(value *PrivacyModelInstallation) {
			value.LabelMapping = map[string]*CanonicalKind{}
		},
		"local source without local identity": func(value *PrivacyModelInstallation) {
			value.Source = PrivacyModelSourceLocal
			value.CatalogID = nil
			value.CatalogSource = nil
		},
		"non-local source with local identity": func(value *PrivacyModelInstallation) {
			value.Source = PrivacyModelSourceCustom
			value.CatalogID = nil
			value.CatalogSource = nil
			value.RepoID = "local/model-aaaaaaaaaaaa"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if err := ValidatePrivacyModelInstallation(candidate); err == nil {
				t.Fatalf("invalid installation was accepted: %#v", candidate)
			}
		})
	}
}

func TestValidatePrivacyModelLocalProbeRequest(t *testing.T) {
	path := t.TempDir()
	for _, candidate := range []string{
		path,
		path + string(filepath.Separator),
		filepath.Join(path, "model.onnx"),
	} {
		if err := ValidatePrivacyModelLocalProbeRequest(
			PrivacyModelLocalProbeRequest{Path: candidate},
		); err != nil {
			t.Fatalf("valid absolute path %q: %v", candidate, err)
		}
	}
	for _, candidate := range []string{
		"relative/model",
		"smb://server/share/model",
		path + "\nmodel",
		strings.Repeat("/x", 2050),
	} {
		if err := ValidatePrivacyModelLocalProbeRequest(
			PrivacyModelLocalProbeRequest{Path: candidate},
		); err == nil {
			t.Fatalf("invalid path was accepted: %q", candidate)
		}
	}
}

func TestValidatePrivacyModelProbeResponseAllowsNullableLicense(t *testing.T) {
	response := PrivacyModelProbeResponse{
		RepoID:            "acme/privacy-model",
		RequestedRevision: "main",
		Revision:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:              "Test Model",
		License:           nil,
		Languages:         []string{},
		Adapter:           PrivacyModelAdapterHFToken,
		Variants: []PrivacyModelVariant{{
			ID: "q4", Name: "CPU Q4", Quantization: "q4",
			BytesTotal: 1, EstimatedRAMBytes: 2, Supported: true,
		}},
		Labels: []PrivacyModelLabel{{
			Label: "EMAIL", SuggestedKind: canonicalKindPointer(CanonicalKindEmail),
		}},
	}
	if err := ValidatePrivacyModelProbeResponse(response); err != nil {
		t.Fatalf("valid nullable-license probe: %v", err)
	}
}

func canonicalKindPointer(value CanonicalKind) *CanonicalKind {
	return &value
}
