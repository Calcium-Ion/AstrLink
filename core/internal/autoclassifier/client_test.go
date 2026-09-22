package autoclassifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autotaxonomy"
)

const helperEnvironment = "ASTRLINK_CLASSIFIER_WORKER_TEST_HELPER"

func TestClassifyFailOpenAndSuccess(t *testing.T) {
	installation := writeReadyInstallation(t)
	provider := ReadyInstallationProviderFunc(func() (contract.ReadyAutoClassifierInstallation, bool) {
		return installation, true
	})

	okClient := newTestClient(t, provider, "ok", time.Second)
	outcome := okClient.Classify(context.Background(), "hello")
	if !outcome.OK() || outcome.Category != "general" || len(outcome.Logits) != 4 {
		t.Fatalf("success outcome = %+v", outcome)
	}
	if !okClient.EligibleForRouting() {
		t.Fatal("a ready installation is routing-eligible")
	}
	process := okClient.process
	okClient.Close()
	okClient.Close()
	if okClient.process != nil || process.running() {
		t.Fatal("worker remained active after Close")
	}

	missing := ReadyInstallationProviderFunc(func() (contract.ReadyAutoClassifierInstallation, bool) {
		return contract.ReadyAutoClassifierInstallation{}, false
	})
	emptyClient, err := New(Config{ExecutablePath: os.Args[0], Model: missing})
	if err != nil {
		t.Fatal(err)
	}
	if got := emptyClient.Classify(context.Background(), "hello"); got.FallbackReason != FallbackUnavailable {
		t.Fatalf("missing install = %+v", got)
	}
	if got := emptyClient.Classify(context.Background(), "   "); got.FallbackReason != FallbackEmptyText {
		t.Fatalf("blank text = %+v", got)
	}

	hang := newTestClient(t, provider, "hang", 150*time.Millisecond)
	if got := hang.Classify(context.Background(), "hello"); got.FallbackReason != FallbackTimeout {
		t.Fatalf("timeout = %+v", got)
	}
}

func TestSiblingExecutablePathUsesCoreSuffix(t *testing.T) {
	path, err := SiblingExecutablePath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) == "" {
		t.Fatal("empty sibling path")
	}
}

func newTestClient(
	t *testing.T,
	model ReadyInstallationProvider,
	mode string,
	timeout time.Duration,
) *Client {
	t.Helper()
	client, err := New(Config{
		ExecutablePath: os.Args[0],
		Model:          model,
		Timeout:        timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	client.command = func(_ string, arguments ...string) *exec.Cmd {
		helperArguments := []string{"-test.run=^TestClassifierWorkerHelper$", "--"}
		helperArguments = append(helperArguments, arguments...)
		command := exec.Command(os.Args[0], helperArguments...)
		command.Env = append(os.Environ(),
			helperEnvironment+"=1",
			"ASTRLINK_CLASSIFIER_WORKER_TEST_MODE="+mode,
		)
		return command
	}
	t.Cleanup(client.Close)
	return client
}

func writeReadyInstallation(t *testing.T) contract.ReadyAutoClassifierInstallation {
	t.Helper()
	directory := t.TempDir()
	id := contract.AutoClassifierID("classifier_" + "ab0123456789abcdef0123456789abcd")
	identity := "local/model-aaaaaaaaaaaa@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	files := []struct {
		name string
		body []byte
	}{
		{name: "model.onnx", body: []byte("synthetic-onnx")},
		{name: "tokenizer.json", body: []byte(`{"version":"1.0"}`)},
		{name: "config.json", body: []byte(`{"architectures":["ModernBertForSequenceClassification"]}`)},
	}
	declared := make([]map[string]any, 0, len(files))
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(directory, file.name), file.body, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(file.body)
		declared = append(declared, map[string]any{
			"path":   file.name,
			"size":   len(file.body),
			"sha256": hex.EncodeToString(digest[:]),
		})
	}
	manifest := map[string]any{
		"version":             1,
		"installation_id":     string(id),
		"identity":            identity,
		"taxonomy_id":         autotaxonomy.ID,
		"taxonomy_sha256":     autotaxonomy.SHA256,
		"preprocessing":       map[string]string{"text": autotaxonomy.TextPreprocessing, "tokens": autotaxonomy.TokenPreprocessing},
		"artifact_tier":       "experimental",
		"model_path":          "model.onnx",
		"tokenizer_path":      "tokenizer.json",
		"config_path":         "config.json",
		"max_sequence_tokens": 512,
		"content_budget":      510,
		"head_tokens":         255,
		"tail_tokens":         255,
		"pad_token_id":        0,
		"pad_multiple":        8,
		"add_special_tokens":  false,
		"input_names":         map[string]string{"input_ids": "input_ids", "attention_mask": "attention_mask"},
		"output_name":         "logits",
		"id2label": map[string]string{
			"0": "general", "1": "research", "2": "coding", "3": "architect",
		},
		"files": declared,
	}
	document, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, installationManifestName), document, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(document)
	return contract.ReadyAutoClassifierInstallation{
		ID:             id,
		Directory:      directory,
		Identity:       identity,
		ManifestSHA256: hex.EncodeToString(digest[:]),
		ArtifactTier:   contract.AutoClassifierArtifactExperimental,
	}
}

func TestClassifierWorkerHelper(t *testing.T) {
	if os.Getenv(helperEnvironment) != "1" {
		return
	}
	mode := os.Getenv("ASTRLINK_CLASSIFIER_WORKER_TEST_MODE")
	ready, err := json.Marshal(workerReady{Version: protocolVersion, Ready: true})
	if err != nil || writeFrame(os.Stdout, ready) != nil {
		os.Exit(4)
	}
	for {
		payload, err := readFrame(os.Stdin)
		if err != nil {
			os.Exit(0)
		}
		if mode == "hang" {
			time.Sleep(time.Second)
			os.Exit(0)
		}
		var request classifyRequest
		if json.Unmarshal(payload, &request) != nil {
			os.Exit(2)
		}
		category := "general"
		response := classifyResponse{
			Version:  protocolVersion,
			ID:       request.ID,
			Category: &category,
			Logits:   []float32{1, 0, 0, 0},
		}
		encoded, err := json.Marshal(response)
		if err != nil || writeFrame(os.Stdout, encoded) != nil {
			os.Exit(3)
		}
	}
}
