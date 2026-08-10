package privacymodel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestLocalSensitiveGuardProbeAndInstallBindHybridRuntimeAssets(t *testing.T) {
	source := writeLocalSensitiveGuardFixture(t)
	registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"))
	probe, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	)
	if err != nil {
		t.Fatalf("ProbeLocal sensitive guard: %v", err)
	}
	if probe.Adapter != contract.PrivacyModelAdapterAstrLinkGuard ||
		probe.Name != "AstrLink Sensitive Data Guard 32M" ||
		probe.License == nil || *probe.License != "Apache-2.0" ||
		len(probe.Languages) != 2 ||
		len(probe.Variants) != 1 || probe.Variants[0].ID != "cpu_int8" ||
		len(probe.Labels) != len(sensitiveGuardSourceLabels) ||
		probe.RequiresLabelMapping {
		t.Fatalf("sensitive guard probe=%#v", probe)
	}

	started, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID: probe.RepoID, Revision: probe.Revision,
			VariantID: probe.Variants[0].ID, LabelMapping: defaultOpenAILabelMapping(),
		},
	)
	if err != nil {
		t.Fatalf("Install sensitive guard: %v", err)
	}
	readyInstallation := waitForInstallation(t, registry, started.ID)
	if readyInstallation.Status != contract.PrivacyModelStatusReady ||
		readyInstallation.Adapter != contract.PrivacyModelAdapterAstrLinkGuard {
		t.Fatalf("sensitive guard installation=%#v", readyInstallation)
	}
	ready, exists := registry.ReadyInstallation(started.ID)
	if !exists {
		t.Fatal("sensitive guard installation was not ready")
	}
	manifest, _, err := inspectNormalizedInstallationDocument(
		context.Background(), ready.Directory, started.ID,
	)
	if err != nil || len(manifest.Files) != 7 ||
		manifest.CalibrationPath == nil ||
		*manifest.CalibrationPath != sensitiveGuardViterbiCalibrationPath ||
		manifest.SecretRulesPath == nil ||
		*manifest.SecretRulesPath != sensitiveGuardSecretRulesPath ||
		manifest.SecretCalibrationPath == nil ||
		*manifest.SecretCalibrationPath != sensitiveGuardSecretCalibrationPath {
		t.Fatalf("sensitive guard manifest=%#v err=%v", manifest, err)
	}
}

func TestLocalProbeAndInstallCopiesVerifiedAssetsIntoPrivateInstallation(t *testing.T) {
	source := writeLocalModelFixture(t)
	root := filepath.Join(t.TempDir(), "registry")
	store := openRegistryStore(t)
	registry := newLocalTestRegistry(t, root, store)

	probe, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{
			Path: source + string(filepath.Separator),
		},
	)
	if err != nil {
		t.Fatalf("ProbeLocal: %v", err)
	}
	if !strings.HasPrefix(probe.RepoID, "local/model-") ||
		len(probe.Revision) != 40 ||
		probe.RequestedRevision != probe.Revision ||
		probe.Adapter != contract.PrivacyModelAdapterHFToken ||
		len(probe.Variants) != 1 ||
		probe.Variants[0].ID != "cpu_int8" ||
		len(probe.Labels) != 1 || probe.Labels[0].Label != "EMAIL" {
		t.Fatalf("probe=%#v", probe)
	}
	encodedProbe, err := json.Marshal(probe)
	if err != nil || strings.Contains(string(encodedProbe), source) ||
		strings.Contains(string(encodedProbe), filepath.Base(source)) {
		t.Fatalf("probe leaked source path: %s, %v", encodedProbe, err)
	}

	started, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID: probe.RepoID, Revision: probe.Revision,
			VariantID:    probe.Variants[0].ID,
			LabelMapping: emailMapping(),
		},
	)
	if err != nil || started.Source != contract.PrivacyModelSourceLocal ||
		started.Status != contract.PrivacyModelStatusDownloading {
		t.Fatalf("Install=%#v, %v", started, err)
	}
	readyInstallation := waitForInstallation(t, registry, started.ID)
	if readyInstallation.Status != contract.PrivacyModelStatusReady ||
		readyInstallation.BytesDownloaded != readyInstallation.BytesTotal ||
		readyInstallation.CatalogID != nil ||
		readyInstallation.CatalogSource != nil {
		t.Fatalf("terminal installation=%#v", readyInstallation)
	}
	encodedInstallation, err := json.Marshal(readyInstallation)
	if err != nil || strings.Contains(string(encodedInstallation), filepath.Base(source)) {
		t.Fatalf("installation leaked source basename: %s, %v", encodedInstallation, err)
	}
	persisted, err := store.GetPrivacyModelInstallation(context.Background(), started.ID)
	if err != nil || persisted.Manifest == nil {
		t.Fatalf("persisted local installation=%#v err=%v", persisted, err)
	}
	persistedJSON, err := json.Marshal(persisted.Installation)
	if err != nil || strings.Contains(string(persistedJSON), filepath.Base(source)) ||
		strings.Contains(string(persisted.Manifest.JSON), filepath.Base(source)) {
		t.Fatalf("persisted data leaked source basename: installation=%s manifest=%s err=%v", persistedJSON, persisted.Manifest.JSON, err)
	}
	ready, exists := registry.ReadyInstallation(started.ID)
	if !exists || strings.Contains(ready.Directory, source) {
		t.Fatalf("ready=%#v exists=%v", ready, exists)
	}
	manifest, _, err := inspectNormalizedInstallationDocument(
		context.Background(), ready.Directory, started.ID,
	)
	if err != nil || len(manifest.Files) != 4 ||
		manifest.RepoID != probe.RepoID ||
		manifest.Revision != probe.Revision {
		t.Fatalf("manifest=%#v err=%v", manifest, err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ready.Directory, "onnx", "model_int8.onnx")); err != nil {
		t.Fatalf("private copy disappeared with source: %v", err)
	}
	restarted := newLocalTestRegistry(t, root, store)
	if restartedReady, ok := restarted.ReadyInstallation(started.ID); !ok ||
		restartedReady.Identity != ready.Identity {
		t.Fatalf("restarted local handoff=%#v ok=%v", restartedReady, ok)
	}
	if entries, err := os.ReadDir(filepath.Join(root, "staging")); err != nil || len(entries) != 0 {
		t.Fatalf("staging=%#v err=%v", entries, err)
	}
}

func TestLocalFileProbeScopesIdentityAndInstallationToSelectedModel(t *testing.T) {
	source := writeLocalModelFixture(t)
	selectedPath := filepath.Join(source, "onnx", "model_int8.onnx")
	otherPath := filepath.Join(source, "onnx", "model_fp32.onnx")
	otherExternalPath := otherPath + "_data"
	if err := os.WriteFile(otherPath, []byte("unselected-model-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherExternalPath, []byte("unselected-data-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(otherPath, filepath.Join(source, "onnx", "unselected-link.onnx"))

	root := filepath.Join(t.TempDir(), "registry")
	store := openRegistryStore(t)
	registry := newLocalTestRegistry(t, root, store)
	first, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: selectedPath},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Variants) != 1 || first.Variants[0].ID != "cpu_int8" {
		t.Fatalf("selected-file variants=%#v", first.Variants)
	}
	encoded, err := json.Marshal(first)
	if err != nil || strings.Contains(string(encoded), source) ||
		strings.Contains(string(encoded), filepath.Base(source)) {
		t.Fatalf("selected-file probe leaked source: %s err=%v", encoded, err)
	}

	if err := os.WriteFile(otherPath, []byte("unselected-model-v2-is-different"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherExternalPath, []byte("unselected-data-v2-is-different"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: selectedPath},
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.RepoID != first.RepoID || second.Revision != first.Revision {
		t.Fatalf("unselected model changed identity: first=%#v second=%#v", first, second)
	}

	started, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID: first.RepoID, Revision: first.Revision,
			VariantID: first.Variants[0].ID, LabelMapping: emailMapping(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	readyInstallation := waitForInstallation(t, registry, started.ID)
	if readyInstallation.Status != contract.PrivacyModelStatusReady {
		t.Fatalf("selected-file installation=%#v", readyInstallation)
	}
	ready, exists := registry.ReadyInstallation(started.ID)
	if !exists {
		t.Fatal("selected-file installation was not ready")
	}
	manifest, _, err := inspectNormalizedInstallationDocument(
		context.Background(), ready.Directory, started.ID,
	)
	if err != nil || len(manifest.Files) != 4 ||
		manifest.ModelPath != "onnx/model_int8.onnx" {
		t.Fatalf("selected-file manifest=%#v err=%v", manifest, err)
	}
	for _, file := range manifest.Files {
		if strings.Contains(file.Path, "model_fp32") {
			t.Fatalf("unselected model was imported: %#v", manifest.Files)
		}
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	restarted := newLocalTestRegistry(t, root, store)
	restartedReady, exists := restarted.ReadyInstallation(started.ID)
	if !exists || restartedReady.Identity != ready.Identity {
		t.Fatalf("restarted selected-file handoff=%#v exists=%v", restartedReady, exists)
	}
}

func TestLocalFileProbeRejectsSymlinkNonONNXAndMissingPackageAssets(t *testing.T) {
	source := writeLocalModelFixture(t)
	registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"))
	modelPath := filepath.Join(source, "onnx", "model_int8.onnx")

	symlink := filepath.Join(source, "onnx", "selected-link.onnx")
	if err := os.Symlink(modelPath, symlink); err == nil {
		if _, err := registry.ProbeLocal(
			context.Background(),
			contract.PrivacyModelLocalProbeRequest{Path: symlink},
		); !errors.Is(err, ErrLocalSource) {
			t.Fatalf("selected file symlink error=%v", err)
		}
	}

	nonONNX := filepath.Join(source, "onnx", "model.bin")
	if err := os.WriteFile(nonONNX, []byte("not-onnx"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: nonONNX},
	); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("non-ONNX selected file error=%v", err)
	}

	if err := os.Remove(filepath.Join(source, "config.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: modelPath},
	); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("missing config error=%v", err)
	}
}

func TestLocalFileProbeBoundsPackageRootSearch(t *testing.T) {
	root := t.TempDir()
	writeLocalPackageMetadata(t, root)
	directory := root
	for index := 0; index < maxLocalProbeParents; index++ {
		directory = filepath.Join(directory, fmt.Sprintf("nested-%d", index))
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(directory, "model_int8.onnx")
	if err := os.WriteFile(modelPath, []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"))
	if _, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: modelPath},
	); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("distant package metadata error=%v", err)
	}
}

func TestLocalProbeRejectsNestedAssetSymlinkAndTooManyModels(t *testing.T) {
	source := writeLocalModelFixture(t)
	registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"))
	modelPath := filepath.Join(source, "onnx", "model_int8.onnx")
	if err := os.Remove(modelPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(source, "config.json"), modelPath); err == nil {
		if _, err := registry.ProbeLocal(
			context.Background(),
			contract.PrivacyModelLocalProbeRequest{Path: source},
		); !errors.Is(err, ErrLocalSource) {
			t.Fatalf("nested asset symlink error=%v", err)
		}
	}

	source = writeLocalModelFixture(t)
	for index := 0; index < 32; index++ {
		name := filepath.Join(source, "onnx", "extra_"+strconv.Itoa(index)+".onnx")
		if err := os.WriteFile(name, []byte("model"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("33-model probe error=%v", err)
	}
}

func TestLocalProbeRevisionChangesWithAssetAndCacheMissNeverUsesNetwork(t *testing.T) {
	source := writeLocalModelFixture(t)
	registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"))
	first, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	)
	if err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(source, "onnx", "model_int8.onnx")
	if err := os.WriteFile(modelPath, []byte("different-model"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision == second.Revision || first.RepoID == second.RepoID {
		t.Fatalf("content change did not change identity: first=%#v second=%#v", first, second)
	}

	_, err = registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
		RepoID:   "local/model-aaaaaaaaaaaa",
		Revision: strings.Repeat("a", 40), VariantID: "cpu_int8",
		LabelMapping: emailMapping(),
	})
	if !errors.Is(err, ErrLocalProbeRequired) {
		t.Fatalf("uncached local install error=%v", err)
	}
	_, err = registry.Probe(context.Background(), contract.PrivacyModelProbeRequest{
		RepoID: "local/model-aaaaaaaaaaaa", Revision: "main",
	})
	if !errors.Is(err, ErrLocalProbeRequired) {
		t.Fatalf("local repo reached remote probe: %v", err)
	}
}

func TestLocalInstallRejectsSourceChangedAfterProbe(t *testing.T) {
	source := writeLocalModelFixture(t)
	registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"))
	probe, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	)
	if err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(source, "onnx", "model_int8.onnx")
	original, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	replacement := []byte(strings.Repeat("x", len(original)))
	if err := os.WriteFile(modelPath, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	started, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID: probe.RepoID, Revision: probe.Revision,
			VariantID: "cpu_int8", LabelMapping: emailMapping(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForInstallation(t, registry, started.ID)
	if failed.Status != contract.PrivacyModelStatusError ||
		failed.Error == nil ||
		*failed.Error != contract.PrivacyModelErrorIntegrity {
		t.Fatalf("changed source terminal state=%#v", failed)
	}
	if _, err := os.Stat(registry.installationDirectory(started.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("changed source was published: %v", err)
	}
}

func TestLocalProbeRejectsRelativePathsSymlinksAndExpiredCache(t *testing.T) {
	registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"))
	if _, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: "relative/model"},
	); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("relative path error=%v", err)
	}

	source := writeLocalModelFixture(t)
	symlink := filepath.Join(t.TempDir(), "linked-model")
	if err := os.Symlink(source, symlink); err == nil {
		if _, err := registry.ProbeLocal(
			context.Background(),
			contract.PrivacyModelLocalProbeRequest{Path: symlink},
		); !errors.Is(err, ErrLocalSource) {
			t.Fatalf("source symlink error=%v", err)
		}
	}

	probe, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	)
	if err != nil {
		t.Fatal(err)
	}
	key := localProbeCacheKey(probe.RepoID, probe.Revision)
	registry.mu.Lock()
	entry := registry.localProbes[key]
	entry.expiresAt = time.Now().Add(-time.Second)
	registry.localProbes[key] = entry
	registry.mu.Unlock()
	_, err = registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
		RepoID: probe.RepoID, Revision: probe.Revision,
		VariantID: "cpu_int8", LabelMapping: emailMapping(),
	})
	if !errors.Is(err, ErrLocalProbeRequired) {
		t.Fatalf("expired cache install error=%v", err)
	}
}

func TestLocalProbeRejectsRegistryRootThroughAncestorSymlinkAlias(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "registry")
	registry := newLocalTestRegistry(t, root)
	source := filepath.Join(root, "source")
	writeLocalModelFixtureAt(t, source)
	alias := filepath.Join(t.TempDir(), "registry-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, candidate := range []string{
		filepath.Join(alias, "source"),
		filepath.Join(alias, "source", "onnx", "model_int8.onnx"),
	} {
		_, err := registry.ProbeLocal(
			context.Background(),
			contract.PrivacyModelLocalProbeRequest{Path: candidate},
		)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("registry alias overlap path=%q error=%v", candidate, err)
		}
	}
}

func TestLocalProbeCacheIsBounded(t *testing.T) {
	registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"))
	for index := 0; index < localProbeCacheLimit+4; index++ {
		revision := fmt.Sprintf("%040x", index+1)
		registry.cacheLocalProbe(
			filepath.Join(t.TempDir(), "source"),
			contract.PrivacyModelProbeResponse{
				RepoID:   fmt.Sprintf("local/model-%012x", index+1),
				Revision: revision, RequestedRevision: revision,
				Languages: []string{}, Variants: []contract.PrivacyModelVariant{},
				Labels: []contract.PrivacyModelLabel{},
			},
			map[string]customVariantPlan{},
		)
	}
	registry.mu.Lock()
	cacheSize := len(registry.localProbes)
	registry.mu.Unlock()
	if cacheSize != localProbeCacheLimit {
		t.Fatalf("local probe cache size=%d want=%d", cacheSize, localProbeCacheLimit)
	}
}

func TestLocalRepoIdentityUsesOnlyTheDerivedSyntheticShape(t *testing.T) {
	for _, candidate := range []string{
		"local/model-0123456789ab",
		"local/model-abcdef012345",
	} {
		if !IsLocalPrivacyModelRepoID(candidate) {
			t.Fatalf("derived local identity was rejected: %q", candidate)
		}
	}
	for _, candidate := range []string{
		"local/privacy-filter",
		"local/model-0123456789AB",
		"local/model-0123456789a",
		"local/model-0123456789abc",
	} {
		if IsLocalPrivacyModelRepoID(candidate) {
			t.Fatalf("ordinary repository was reserved as local: %q", candidate)
		}
	}
}

func newLocalTestRegistry(
	t *testing.T,
	root string,
	stores ...storage.PrivacyModelInstallationStore,
) *Registry {
	t.Helper()
	var store storage.PrivacyModelInstallationStore
	if len(stores) > 0 {
		store = stores[0]
	}
	registry, err := NewRegistry(context.Background(), RegistryConfig{
		RootDirectory: root, Store: store,
		MetadataBaseURL: "http://127.0.0.1/",
		HTTPClient:      &http.Client{}, TestOnlyLoopbackMode: true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

func writeLocalModelFixture(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "local-model")
	writeLocalModelFixtureAt(t, directory)
	return directory
}

func writeLocalModelFixtureAt(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(directory, "onnx"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeLocalPackageMetadata(t, directory)
	assets := map[string][]byte{
		"onnx/model_int8.onnx":      []byte("fake-local-onnx-model"),
		"onnx/model_int8.onnx_data": []byte("fake-external-data"),
	}
	for relative, document := range assets {
		path := filepath.Join(directory, filepath.FromSlash(relative))
		if err := os.WriteFile(path, document, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func writeLocalPackageMetadata(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	assets := map[string][]byte{
		"config.json": []byte(`{
			"architectures":["TinyForTokenClassification"],
			"model_type":"bert",
			"type_vocab_size":2,
			"id2label":{"0":"O","1":"B-EMAIL","2":"I-EMAIL"}
		}`),
		"tokenizer.json": []byte(`{"version":"1.0","model":{"type":"WordPiece"}}`),
	}
	for relative, document := range assets {
		if err := os.WriteFile(
			filepath.Join(directory, relative),
			document,
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
}

func writeLocalSensitiveGuardFixture(t *testing.T) string {
	t.Helper()
	directory := writeLocalModelFixture(t)
	labels := map[string]string{"0": "O"}
	index := 1
	for _, source := range sensitiveGuardSourceLabels {
		for _, prefix := range []string{"B", "I", "E", "S"} {
			labels[strconv.Itoa(index)] = prefix + "-" + source
			index++
		}
	}
	writeJSONFixture(t, filepath.Join(directory, "config.json"), map[string]any{
		"architectures": []string{"TinyForTokenClassification"},
		"model_type":    "bert", "type_vocab_size": 2, "id2label": labels,
	})
	zeros := make([]float64, 33)
	thresholds := make(map[string]float64, len(sensitiveGuardSourceLabels))
	calibrators := make(map[string]any, len(sensitiveGuardSourceLabels))
	for _, source := range sensitiveGuardSourceLabels {
		thresholds[source] = 0
		calibrators[source] = map[string]any{
			"slope": 1.0, "intercept": 0.0,
			"empirical_precision_lower_bound": 0.9,
		}
	}
	writeJSONFixture(t, filepath.Join(directory, sensitiveGuardViterbiCalibrationPath), map[string]any{
		"schema_version": 2, "status": "fitted",
		"decoder": "bioes-constrained-viterbi",
		"compatibility": map[string]any{
			"confidence_definition": "mean_boundary_minimum_hybrid",
			"confidence_revision":   2,
			"ranking_policy":        "monotonic_platt_without_empirical_floor",
			"reporting_policy":      "maximum_of_monotonic_platt_and_empirical_precision_lower_bound",
			"floor_policy":          "reporting_only_not_ranking_or_thresholding",
		},
		"language_decoder_biases": map[string]any{
			"en": map[string]any{"emission_bias": zeros},
			"zh": map[string]any{"emission_bias": zeros},
		},
		"language_operating_points": map[string]any{
			"en": map[string]any{"default_threshold": 0.0, "per_label": thresholds},
			"zh": map[string]any{"default_threshold": 0.0, "per_label": thresholds},
		},
		"span_confidence": map[string]any{"en": calibrators, "zh": calibrators},
	})
	rules, err := json.Marshal(map[string]any{
		"schema_version": 2, "format": "JSON-compatible YAML",
		"rules": []map[string]any{
			{"rule_id": "password.assignment", "regex": "(password)", "capture_group": 1, "label": "secret"},
			{"rule_id": "generic.keyword_secret", "regex": "(api_key)", "capture_group": 1, "label": "secret"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, sensitiveGuardSecretRulesPath), rules, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	rulesDigest := sha256.Sum256(rules)
	writeJSONFixture(t, filepath.Join(directory, sensitiveGuardSecretCalibrationPath), map[string]any{
		"schema_version": 2, "status": "fitted", "mode": "rules",
		"minimum_confidence": 0.9,
		"fit_metadata":       map[string]any{"rules_sha256": fmt.Sprintf("%x", rulesDigest)},
		"platt":              map[string]any{"slope": 1.0, "intercept": 0.0},
		"score_model": map[string]any{
			"link": "linear_clip", "clip": []float64{0.01, 0.995},
			"entropy_offset": 3.0, "entropy_cap": 2.25, "intercept": 0.3,
			"weights": map[string]float64{
				"context": 0.12, "encoded": 0.03, "entropy_excess": 0.04,
				"explicit_assignment": 0.45, "model_score": 0.18,
				"negative": -0.65, "pair": 0.08, "pattern": 0.2,
				"structurally_invalid": -0.12, "structurally_valid": 0.2,
			},
		},
	})
	return directory
}

func writeJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	document, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
}
