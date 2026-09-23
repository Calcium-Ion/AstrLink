package privacymodel

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestBuiltinCatalogOnlyAdvertisesPPLXAndRetainsLegacyProvenance(t *testing.T) {
	catalog := BuiltinCatalog()
	if len(catalog.Items) != 1 || catalog.Items[0].ID != CatalogPPLXPIITracer ||
		catalog.Items[0].Adapter != contract.PrivacyModelAdapterPPLXBIOES ||
		len(catalog.Items[0].Variants) != 1 || !catalog.Items[0].Variants[0].Recommended ||
		catalog.Items[0].Variants[0].Quantization != "int4" {
		t.Fatalf("catalog item count = %d", len(catalog.Items))
	}
	catalog.Items = append(catalog.Items, legacyCatalogEntries()...)
	expected := []struct {
		id       contract.PrivacyModelCatalogID
		repoID   string
		revision string
		variants []string
	}{
		{
			CatalogPPLXPIITracer,
			"QuantumNous/astrlink-pii-tracer-int4",
			"0f9a56fc32062ea5827f4d908e7afaa22b88c902",
			[]string{"cpu_int4"},
		},
		{
			CatalogPPLXPIITracer,
			"lemonade-sdk/pplx-pii-masking-onnx",
			"5ba4e413b78ff0f83d3c9cddee1bb5fdccbeee00",
			[]string{"cpu_fp32"},
		},
		{
			CatalogSheltronEttin32M,
			"sheltron-ai/privacy-filter-ettin-32m",
			"53d55c58fdbb5ed2ace902a374f664c1cf4914c7",
			[]string{"cpu_int8", "cpu_fp32"},
		},
		{
			CatalogNymPIIMultilingualSmall,
			"Wismut/nym-pii-multilingual-small",
			"4348999cd3c2e20c49615e9af7c6bbb45b64cd85",
			[]string{"edge_int8", "cpu_int8", "cpu_fp32"},
		},
		{
			CatalogOpenAIPrivacyFilter,
			"openai/privacy-filter",
			DefaultRevision,
			[]string{"cpu_q4", "cpu_int8", "gpu_f16", "gpu_q4f16"},
		},
	}
	for index, want := range expected {
		item := catalog.Items[index]
		if item.ID != want.id || item.RepoID != want.repoID ||
			item.Revision != want.revision ||
			contract.ValidatePrivacyModelRevision(item.Revision) != nil ||
			!item.Source.Valid() ||
			item.License == "" ||
			len(item.Languages) == 0 ||
			len(item.Variants) != len(want.variants) {
			t.Fatalf("item[%d] = %#v", index, item)
		}
		for variantIndex, variantID := range want.variants {
			variant := item.Variants[variantIndex]
			if variant.ID != variantID {
				t.Fatalf("item[%d] variant[%d]=%q, want %q", index, variantIndex, variant.ID, variantID)
			}
			if stringsHasPrefix(variant.ID, "gpu_") {
				if variant.Supported || variant.UnsupportedReason == nil ||
					*variant.UnsupportedReason != "cpu_only" {
					t.Fatalf("GPU variant was not visibly unsupported: %#v", variant)
				}
			} else if !variant.Supported || variant.UnsupportedReason != nil ||
				variant.BytesTotal <= 0 ||
				variant.EstimatedRAMBytes <= 0 {
				t.Fatalf("CPU variant is invalid: %#v", variant)
			}
		}
	}
	if InstallationID(
		"openai/privacy-filter",
		DefaultRevision,
		"cpu_q4",
	) != contract.LegacyOpenAIPrivacyFilterInstallationID {
		t.Fatal("legacy OpenAI deterministic installation id drifted")
	}
}

func TestPPLXINT4ManifestIncludesLicenseAndDoesNotReplaceFP32Provenance(t *testing.T) {
	item := BuiltinCatalog().Items[0]
	plan, exists := builtinVariantPlan(item.RepoID, item.Revision, "cpu_int4")
	if !exists {
		t.Fatal("INT4 plan missing")
	}
	manifest := buildNormalizedManifest(contract.PrivacyModelInstallation{
		ID:     InstallationID(item.RepoID, item.Revision, "cpu_int4"),
		RepoID: item.RepoID, Revision: item.Revision, VariantID: "cpu_int4",
		Adapter: item.Adapter, LabelMapping: defaultPPLXLabelMapping(),
	}, plan.runtime, plan.assets)
	if validateNormalizedManifest(manifest) != nil || !manifestMatchesBuiltinPlan(manifest, plan) {
		t.Fatalf("INT4 manifest invalid: %#v", manifest)
	}
	licenseFound := false
	for _, file := range manifest.Files {
		licenseFound = licenseFound || file.Path == "LICENSE"
	}
	if !licenseFound {
		t.Fatal("INT4 installation lost its upstream license")
	}
	legacy, exists := builtinVariantPlan(
		"lemonade-sdk/pplx-pii-masking-onnx",
		"5ba4e413b78ff0f83d3c9cddee1bb5fdccbeee00", "cpu_fp32",
	)
	if !exists || legacy.item.Name != "Perplexity PII-Tracer 0.6B" ||
		legacy.variant.BytesTotal != 2_403_057_465 || manifestMatchesBuiltinPlan(manifest, legacy) {
		t.Fatal("legacy FP32 provenance was replaced by the INT4 release")
	}
}

func TestBuiltinPlansMatchCatalogTotalsAndModelWindows(t *testing.T) {
	for _, item := range append(BuiltinCatalog().Items, legacyCatalogEntries()...) {
		for _, variant := range item.Variants {
			if !variant.Supported {
				continue
			}
			plan, exists := builtinVariantPlan(
				item.RepoID,
				item.Revision,
				variant.ID,
			)
			if !exists {
				t.Fatalf("missing plan for %s/%s", item.ID, variant.ID)
			}
			var total int64
			for _, asset := range plan.assets {
				if !validPinnedAsset(asset) {
					t.Fatalf("unpinned built-in asset: %#v", asset)
				}
				total += asset.Size
			}
			if total != variant.BytesTotal {
				t.Fatalf("%s/%s total=%d want=%d", item.ID, variant.ID, total, variant.BytesTotal)
			}
			if item.ID == CatalogSheltronEttin32M &&
				(plan.runtime.window != 512 || plan.runtime.stride != 128) {
				t.Fatalf("Sheltron runtime=%#v", plan.runtime)
			}
			if item.ID == CatalogPPLXPIITracer {
				window, modelPath := 4096, "model.onnx"
				if variant.ID == "cpu_int4" {
					window, modelPath = 1024, "model_int4.onnx"
				}
				if plan.runtime.window != window || plan.runtime.stride != 128 ||
					plan.runtime.tagScheme != "bioes" || plan.runtime.modelPath != modelPath ||
					len(plan.runtime.externalData) != 1 || plan.runtime.externalData[0] != modelPath+".data" {
					t.Fatalf("PII-Tracer runtime=%#v", plan.runtime)
				}
			}
		}
	}
}

func TestNormalizedManifestAlwaysEmitsExternalDataAsArray(t *testing.T) {
	plan, exists := builtinVariantPlan(
		"Wismut/nym-pii-multilingual-small",
		"4348999cd3c2e20c49615e9af7c6bbb45b64cd85",
		"edge_int8",
	)
	if !exists || len(plan.runtime.externalData) != 0 {
		t.Fatalf("unexpected Nym plan=%#v exists=%t", plan, exists)
	}
	email := contract.CanonicalKindEmail
	installation := contract.PrivacyModelInstallation{
		ID: InstallationID(
			plan.item.RepoID,
			plan.item.Revision,
			plan.variant.ID,
		),
		RepoID:       plan.item.RepoID,
		Revision:     plan.item.Revision,
		VariantID:    plan.variant.ID,
		Adapter:      plan.item.Adapter,
		LabelMapping: map[string]*contract.CanonicalKind{"EMAIL": &email},
	}
	manifest := buildNormalizedManifest(
		installation,
		plan.runtime,
		plan.assets,
	)
	if manifest.ExternalData == nil ||
		validateNormalizedManifest(manifest) != nil {
		t.Fatalf("normalized manifest=%#v", manifest)
	}
	document, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(document, []byte(`"external_data_paths":[]`)) {
		t.Fatalf("external_data_paths was not an array: %s", document)
	}
	manifest.ExternalData = nil
	if validateNormalizedManifest(manifest) == nil {
		t.Fatal("normalized manifest accepted null external_data_paths")
	}
}

func stringsHasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
