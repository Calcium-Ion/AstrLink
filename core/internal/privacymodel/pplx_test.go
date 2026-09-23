package privacymodel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func pplxTestConfig() []byte {
	return []byte(`{"model_type":"pii_masking","architectures":["PiiMaskingModel"],"num_token_labels":37,"max_seq_len":4096,"viterbi_b_bias":0,"viterbi_e_bias":0,"backbone":{"use_bidirectional_attention":true}}`)
}

func TestPPLXConfigDerivesExactTaxonomyAndRejectsCausalOrIncompleteConfig(t *testing.T) {
	config, adapter, err := parsePrivacyModelConfig(pplxTestConfig())
	if err != nil || adapter != contract.PrivacyModelAdapterPPLXBIOES || len(config.ID2Label) != 37 ||
		config.ID2Label["1"] != "B-private_person" || config.ID2Label["8"] != "S-private_email" ||
		config.ID2Label["36"] != "S-other_pii" {
		t.Fatalf("config=%#v adapter=%s err=%v", config, adapter, err)
	}
	for key, value := range map[string]any{
		"num_token_labels": 33, "max_seq_len": 8192,
		"viterbi_b_bias": nil, "viterbi_e_bias": 1e100,
		"architectures": []string{"Qwen3ForCausalLM"},
		"backbone":      map[string]any{"use_bidirectional_attention": true, "is_causal": true},
	} {
		t.Run(key, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(pplxTestConfig(), &document); err != nil {
				t.Fatal(err)
			}
			document[key] = value
			encoded, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := parsePrivacyModelConfig(encoded); err == nil {
				t.Fatal("accepted incompatible config")
			}
		})
	}
}

func TestPPLXRemoteAndLocalInstallationRetainExternalDataAndDecoder(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.requestedRevision = repository.revision
	repository.assets = map[string][]byte{
		"config.json":     pplxTestConfig(),
		"tokenizer.json":  []byte(`{"version":"1.0","model":{"type":"WordPiece"}}`),
		"model.onnx":      []byte("synthetic graph"),
		"model.onnx.data": []byte("synthetic external tensors"),
	}
	repository.lfs = map[string]bool{"model.onnx": true, "model.onnx.data": true}
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote", true: "local"}[local], func(t *testing.T) {
			store := openRegistryStore(t)
			root := filepath.Join(t.TempDir(), "registry")
			registry := newTestRegistry(t, root, repository, store)
			var probe contract.PrivacyModelProbeResponse
			var err error
			if local {
				directory := t.TempDir()
				for name, content := range repository.assets {
					if err := os.WriteFile(filepath.Join(directory, name), content, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				probe, err = registry.ProbeLocal(context.Background(), contract.PrivacyModelLocalProbeRequest{Path: directory})
			} else {
				probe, err = registry.Probe(context.Background(), contract.PrivacyModelProbeRequest{RepoID: repository.repoID, Revision: repository.revision})
			}
			if err != nil || probe.Adapter != contract.PrivacyModelAdapterPPLXBIOES ||
				len(probe.Labels) != 9 || probe.RequiresLabelMapping || len(probe.Variants) != 1 {
				t.Fatalf("probe=%#v err=%v", probe, err)
			}
			defaults := defaultPPLXLabelMapping()
			for _, label := range probe.Labels {
				want, exists := defaults[label.Label]
				if !exists || label.SuggestedIgnore != (want == nil) ||
					(want != nil && (label.SuggestedKind == nil || *label.SuggestedKind != *want)) ||
					(want == nil && label.SuggestedKind != nil) {
					t.Fatalf("probe default differs from installation default: %#v", label)
				}
			}
			started, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
				RepoID: probe.RepoID, Revision: probe.Revision, VariantID: "cpu_fp32", LabelMapping: defaultPPLXLabelMapping(),
			})
			if err != nil {
				t.Fatal(err)
			}
			installed := waitForInstallation(t, registry, started.ID)
			if installed.Status != contract.PrivacyModelStatusReady {
				t.Fatalf("installation=%#v", installed)
			}
			if err := contract.ValidatePrivacyModelInstallation(installed); err != nil {
				t.Fatal(err)
			}
			restarted := newTestRegistry(t, root, repository, store)
			if _, ok := restarted.ReadyInstallation(started.ID); !ok {
				t.Fatal("persisted PII-Tracer installation was not recovered")
			}
			ready, exists := registry.ReadyInstallation(started.ID)
			if !exists {
				t.Fatal("installation is not ready")
			}
			manifest, _, err := inspectNormalizedInstallationDocument(context.Background(), ready.Directory, started.ID)
			if err != nil || manifest.Window != 4096 || manifest.TagScheme != "bioes" ||
				manifest.Adapter != contract.PrivacyModelAdapterPPLXBIOES ||
				len(manifest.ExternalData) != 1 || manifest.ExternalData[0] != "model.onnx.data" {
				t.Fatalf("manifest=%#v err=%v", manifest, err)
			}
			manifest.Window = 4097
			if validateNormalizedManifest(manifest) == nil {
				t.Fatal("accepted window beyond model limit")
			}
		})
	}
}

func TestPPLXINT4LocalImportRetainsQuantizationAndExternalWeights(t *testing.T) {
	directory := t.TempDir()
	for name, content := range map[string][]byte{
		"config.json":          pplxTestConfig(),
		"tokenizer.json":       []byte(`{"version":"1.0","model":{"type":"WordPiece"}}`),
		"model_int4.onnx":      []byte("synthetic INT4 graph"),
		"model_int4.onnx.data": []byte("synthetic INT4 tensors"),
	} {
		if err := os.WriteFile(filepath.Join(directory, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"), openRegistryStore(t))
	probe, err := registry.ProbeLocal(context.Background(), contract.PrivacyModelLocalProbeRequest{Path: directory})
	if err != nil || len(probe.Variants) != 1 || probe.Variants[0].ID != "cpu_int4" ||
		probe.Variants[0].Quantization != "int4" || !probe.Variants[0].Supported {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
	started, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
		RepoID: probe.RepoID, Revision: probe.Revision, VariantID: "cpu_int4", LabelMapping: defaultPPLXLabelMapping(),
	})
	if err != nil {
		t.Fatal(err)
	}
	installed := waitForInstallation(t, registry, started.ID)
	if installed.Status != contract.PrivacyModelStatusReady {
		t.Fatalf("installation=%#v", installed)
	}
	ready, exists := registry.ReadyInstallation(started.ID)
	if !exists {
		t.Fatal("INT4 installation is not ready")
	}
	manifest, _, err := inspectNormalizedInstallationDocument(context.Background(), ready.Directory, started.ID)
	if err != nil || manifest.VariantID != "cpu_int4" || manifest.ModelPath != "model_int4.onnx" ||
		len(manifest.ExternalData) != 1 || manifest.ExternalData[0] != "model_int4.onnx.data" ||
		manifest.Adapter != contract.PrivacyModelAdapterPPLXBIOES || manifest.Window != 1024 ||
		probe.Variants[0].EstimatedRAMBytes < 2_147_483_648 {
		t.Fatalf("manifest=%#v err=%v", manifest, err)
	}
}

func TestPPLXINT4RepositoryDescriptorCanBeProbed(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.assets = map[string][]byte{
		"config.json":          pplxTestConfig(),
		"tokenizer.json":       []byte(`{"version":"1.0","model":{"type":"WordPiece"}}`),
		"model_int4.onnx":      []byte("synthetic INT4 graph"),
		"model_int4.onnx.data": []byte("synthetic INT4 tensors"),
		InstallationManifestName: []byte(`{
			"version":1,"name":"AstrLink PII-Tracer 0.6B INT4",
			"license":"MIT","languages":["en","multilingual"],
			"adapter":"pplx_bioes_viterbi","variants":[{
				"id":"cpu_int4","name":"CPU INT4","quantization":"int4",
				"estimated_ram_bytes":2147483648,"recommended":true,
				"model_path":"model_int4.onnx","external_data_paths":["model_int4.onnx.data"],
				"tokenizer_path":"tokenizer.json","config_path":"config.json",
				"tag_scheme":"bioes","window":1024,"stride":128,"max_request_tokens":131072,
				"input_names":{"input_ids":"input_ids","attention_mask":"attention_mask"},
				"output_name":"logits"
			}]
		}`),
	}
	repository.lfs = map[string]bool{"model_int4.onnx": true, "model_int4.onnx.data": true}
	registry := newTestRegistry(t, filepath.Join(t.TempDir(), "registry"), repository, openRegistryStore(t))
	probe, err := registry.Probe(context.Background(), contract.PrivacyModelProbeRequest{
		RepoID: repository.repoID, Revision: repository.requestedRevision,
	})
	if err != nil || probe.Name != "AstrLink PII-Tracer 0.6B INT4" ||
		probe.Adapter != contract.PrivacyModelAdapterPPLXBIOES || len(probe.Variants) != 1 ||
		probe.Variants[0].Quantization != "int4" || !probe.Variants[0].Recommended ||
		!probe.Variants[0].Supported {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
}
