package automodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autotaxonomy"
)

func TestProbeAndInstallLocalClassifier(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := writeClassifierFixture(t, t.TempDir(), "ModernBertForSequenceClassification", autotaxonomy.Labels, true)
	probe, err := registry.ProbeLocal(context.Background(), contract.AutoClassifierLocalProbeRequest{Path: source})
	if err != nil {
		t.Fatalf("ProbeLocal: %v", err)
	}
	if probe.ArtifactTier != contract.AutoClassifierArtifactExperimental {
		t.Fatalf("tier = %q", probe.ArtifactTier)
	}
	if !probe.EligibleForRouting {
		t.Fatal("an installed experimental classifier is routing-eligible")
	}
	if probe.TaxonomySHA256 != autotaxonomy.SHA256 {
		t.Fatal("taxonomy sha256 drifted")
	}
	installation, err := registry.Install(context.Background(), contract.AutoClassifierInstallRequest{Path: source})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if installation.ID.Validate() != nil {
		t.Fatalf("id = %q", installation.ID)
	}
	if !installation.EligibleForRouting {
		t.Fatal("installed experimental must be routing-eligible")
	}
	listed := registry.ListInstallations()
	if len(listed) != 1 || listed[0].ID != installation.ID {
		t.Fatalf("list = %+v", listed)
	}
	ready, ok := registry.FirstReady()
	if !ok || ready.ID != installation.ID || ready.Directory == "" {
		t.Fatalf("ready = %+v ok=%v", ready, ok)
	}
	if _, err := os.Lstat(filepath.Join(ready.Directory, "astrlink-classifier-model.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Install(context.Background(), contract.AutoClassifierInstallRequest{Path: source}); err != ErrAlreadyInstalled {
		t.Fatalf("second install = %v", err)
	}
}

func TestProbeRejectsWrongArchitectureAndLegacyCodeLabel(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tokenDir := writeClassifierFixture(t, t.TempDir(), "ModernBertForTokenClassification", autotaxonomy.Labels, false)
	if _, err := registry.ProbeLocal(context.Background(), contract.AutoClassifierLocalProbeRequest{Path: tokenDir}); err != ErrUnsupportedModel {
		t.Fatalf("token classification = %v", err)
	}
	legacy := []string{"general", "research", "code", "architect"}
	codeDir := writeClassifierFixture(t, t.TempDir(), "ModernBertForSequenceClassification", legacy, false)
	if _, err := registry.ProbeLocal(context.Background(), contract.AutoClassifierLocalProbeRequest{Path: codeDir}); err != ErrUnsupportedModel {
		t.Fatalf("legacy code label = %v", err)
	}
}

func TestInstallRequiresFreshProbe(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := writeClassifierFixture(t, t.TempDir(), "ModernBertForSequenceClassification", autotaxonomy.Labels, false)
	if _, err := registry.Install(context.Background(), contract.AutoClassifierInstallRequest{Path: source}); err != ErrLocalProbeRequired {
		t.Fatalf("install without probe = %v", err)
	}
}

func writeClassifierFixture(
	t *testing.T,
	directory string,
	architecture string,
	labels []string,
	withBundle bool,
) string {
	t.Helper()
	id2label := map[string]string{}
	for index, label := range labels {
		id2label[itoa(index)] = label
	}
	config, err := json.Marshal(map[string]any{
		"architectures": []string{architecture},
		"id2label":      id2label,
	})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"model.onnx":     []byte("synthetic-onnx"),
		"tokenizer.json": []byte(`{"version":"1.0"}`),
		"config.json":    config,
	}
	if withBundle {
		fileMeta := map[string]any{}
		for name, body := range files {
			digest := sha256.Sum256(body)
			fileMeta[name] = map[string]any{
				"sha256":     hex.EncodeToString(digest[:]),
				"size_bytes": len(body),
			}
		}
		bundle, marshalErr := json.Marshal(map[string]any{
			"artifact_tier": "experimental",
			"contract":      map[string]any{"id2label": id2label},
			"files":         fileMeta,
			"source": map[string]any{
				"artifact_tier":   "experimental",
				"release_mode":    "experimental",
				"taxonomy_sha256": autotaxonomy.SHA256,
			},
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		files["onnx-manifest.json"] = bundle
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(directory, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}
