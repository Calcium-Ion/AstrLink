package privacymodel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func TestRegistryInstallsMultipleVariantsPersistsBoundManifestAndDeletes(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.requestedRevision = repository.revision
	repository.lfs["model_fp32.onnx"] = true
	store := openRegistryStore(t)
	root := filepath.Join(t.TempDir(), "models")
	registry := newTestRegistry(t, root, repository, store)
	mapping := emailMapping()

	first, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
		RepoID: repository.repoID, Revision: repository.revision,
		VariantID: "cpu_int8", LabelMapping: mapping,
	})
	if err != nil || first.Status != contract.PrivacyModelStatusDownloading {
		t.Fatalf("Install cpu_int8 = %#v, %v", first, err)
	}
	first = waitForInstallation(t, registry, first.ID)
	if first.Status != contract.PrivacyModelStatusReady ||
		first.CatalogID != nil ||
		first.CatalogSource != nil ||
		first.License == nil ||
		*first.License != "apache-2.0" ||
		len(first.Languages) != 1 ||
		first.Languages[0] != "en" {
		t.Fatalf("cpu_int8 terminal state = %#v", first)
	}
	second, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
		RepoID: repository.repoID, Revision: repository.revision,
		VariantID: "cpu_fp32", LabelMapping: mapping,
	})
	if err != nil {
		t.Fatalf("Install cpu_fp32: %v", err)
	}
	second = waitForInstallation(t, registry, second.ID)
	if second.Status != contract.PrivacyModelStatusReady ||
		len(registry.ListInstallations()) != 2 {
		t.Fatalf("second=%#v list=%#v", second, registry.ListInstallations())
	}

	ready, exists := registry.ReadyInstallation(first.ID)
	if !exists || ready.Identity == "" ||
		len(ready.ManifestSHA256) != sha256.Size*2 {
		t.Fatalf("ready handoff = %#v, exists=%v", ready, exists)
	}
	manifest, document, err := inspectNormalizedInstallationDocument(
		context.Background(),
		ready.Directory,
		first.ID,
	)
	if err != nil || manifest.Identity != ready.Identity ||
		testSHA256(document) != ready.ManifestSHA256 ||
		manifest.InputNames.TokenTypeIDs == nil {
		t.Fatalf("manifest=%#v document_sha=%s err=%v", manifest, testSHA256(document), err)
	}
	record, err := store.GetPrivacyModelInstallation(context.Background(), first.ID)
	if err != nil || record.Manifest == nil ||
		record.Manifest.Identity != ready.Identity ||
		record.Manifest.SHA256 != ready.ManifestSHA256 ||
		string(record.Manifest.JSON) != string(document) ||
		record.Installation.License == nil ||
		*record.Installation.License != "apache-2.0" ||
		len(record.Installation.Languages) != 1 {
		t.Fatalf("persisted record=%#v err=%v", record, err)
	}
	stagingEntries, err := os.ReadDir(filepath.Join(root, "staging"))
	if err != nil || len(stagingEntries) != 0 {
		t.Fatalf("staging entries=%#v err=%v", stagingEntries, err)
	}

	if err := registry.DeleteInstallation(context.Background(), second.ID); err != nil {
		t.Fatalf("DeleteInstallation: %v", err)
	}
	if _, err := registry.GetInstallation(second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted GetInstallation error = %v", err)
	}
	if _, err := store.GetPrivacyModelInstallation(
		context.Background(),
		second.ID,
	); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted persisted record error = %v", err)
	}
}

func TestRegistryRestartUsesManifestBindingWithoutHashingAssets(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.requestedRevision = repository.revision
	store := openRegistryStore(t)
	root := filepath.Join(t.TempDir(), "models")
	registry := newTestRegistry(t, root, repository, store)
	started, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
		RepoID: repository.repoID, Revision: repository.revision,
		VariantID: "cpu_int8", LabelMapping: emailMapping(),
	})
	if err != nil {
		t.Fatal(err)
	}
	readyInstallation := waitForInstallation(t, registry, started.ID)
	if readyInstallation.Status != contract.PrivacyModelStatusReady {
		t.Fatalf("terminal state = %#v", readyInstallation)
	}
	ready, _ := registry.ReadyInstallation(started.ID)
	modelPath := filepath.Join(ready.Directory, "model_int8.onnx")
	if err := os.Chmod(modelPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(modelPath, 0o600) })

	restarted := newTestRegistry(t, root, repository, store)
	if _, exists := restarted.ReadyInstallation(started.ID); !exists {
		t.Fatal("restart rejected a bound manifest because an asset was unreadable")
	}
	if _, err := inspectNormalizedInstallation(
		context.Background(),
		ready.Directory,
		started.ID,
	); err == nil {
		t.Fatal("full use-time integrity inspection accepted unreadable asset")
	}
}

func TestRegistryClassifiesIntegrityAndCompatibilityFailures(t *testing.T) {
	t.Run("integrity", func(t *testing.T) {
		repository := newFakeHFRepository(t)
		repository.requestedRevision = repository.revision
		original := repository.assets["model_int8.onnx"]
		corrupt := append([]byte(nil), original...)
		corrupt[0] ^= 0xff
		repository.downloadOverride["model_int8.onnx"] = corrupt
		registry := newTestRegistry(
			t,
			filepath.Join(t.TempDir(), "models"),
			repository,
			nil,
		)
		started, err := registry.Install(
			context.Background(),
			contract.PrivacyModelInstallRequest{
				RepoID: repository.repoID, Revision: repository.revision,
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
			t.Fatalf("integrity state = %#v", failed)
		}
	})

	t.Run("incompatible", func(t *testing.T) {
		repository := newFakeHFRepository(t)
		repository.requestedRevision = repository.revision
		repository.assets["tokenizer.json"] = []byte(`[]`)
		registry := newTestRegistry(
			t,
			filepath.Join(t.TempDir(), "models"),
			repository,
			nil,
		)
		started, err := registry.Install(
			context.Background(),
			contract.PrivacyModelInstallRequest{
				RepoID: repository.repoID, Revision: repository.revision,
				VariantID: "cpu_int8", LabelMapping: emailMapping(),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		failed := waitForInstallation(t, registry, started.ID)
		if failed.Status != contract.PrivacyModelStatusError ||
			failed.Error == nil ||
			*failed.Error != contract.PrivacyModelErrorIncompatible {
			t.Fatalf("incompatible state = %#v", failed)
		}
	})
}

func TestRegistryRetriesTransientAssetFailuresAndLogsSanitizedReason(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.requestedRevision = repository.revision
	repository.transientFailures["model_int8.onnx"] = 2
	var logMu sync.Mutex
	var logs []string
	registry, err := NewRegistry(context.Background(), RegistryConfig{
		RootDirectory:        filepath.Join(t.TempDir(), "models"),
		MetadataBaseURL:      repository.server.URL,
		HTTPClient:           repository.server.Client(),
		TestOnlyLoopbackMode: true,
		Logf: func(format string, arguments ...any) {
			logMu.Lock()
			defer logMu.Unlock()
			logs = append(logs, fmt.Sprintf(format, arguments...))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID:       repository.repoID,
			Revision:     repository.revision,
			VariantID:    "cpu_int8",
			LabelMapping: emailMapping(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ready := waitForInstallation(t, registry, started.ID)
	if ready.Status != contract.PrivacyModelStatusReady ||
		ready.BytesDownloaded != ready.BytesTotal {
		t.Fatalf("terminal installation=%#v", ready)
	}
	if attempts := repository.assetAttemptCount("model_int8.onnx"); attempts != 3 {
		t.Fatalf("model attempts=%d, want 3", attempts)
	}
	logMu.Lock()
	joined := strings.Join(logs, "\n")
	logMu.Unlock()
	if strings.Count(joined, "reason=http_503") != 2 ||
		!strings.Contains(joined, "privacy model download started:") ||
		!strings.Contains(joined, "privacy model download ready:") ||
		!strings.Contains(joined, `path="model_int8.onnx"`) ||
		strings.Contains(joined, "temporary failure") ||
		strings.Contains(joined, repository.server.URL) {
		t.Fatalf("diagnostic logs=%q", joined)
	}
}

func TestDownloadAssetRetryRollsBackPartialProgress(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.requestedRevision = repository.revision
	repository.truncateFailures["model_int8.onnx"] = 1
	registry := newTestRegistry(
		t,
		filepath.Join(t.TempDir(), "models"),
		repository,
		nil,
	)
	document := repository.assets["model_int8.onnx"]
	asset := Asset{
		Path: "model_int8.onnx",
		Size: int64(len(document)),
	}
	id := InstallationID(
		repository.repoID,
		repository.revision,
		"cpu_int8",
	)
	installation := contract.PrivacyModelInstallation{
		ID:              id,
		Status:          contract.PrivacyModelStatusDownloading,
		BytesTotal:      asset.Size,
		RepoID:          repository.repoID,
		Revision:        repository.revision,
		VariantID:       "cpu_int8",
		BytesDownloaded: 0,
	}
	registry.mu.Lock()
	registry.installations[id] = installation
	registry.mu.Unlock()
	progressAtRetry := int64(-1)
	registry.logf = func(format string, arguments ...any) {
		line := fmt.Sprintf(format, arguments...)
		if !strings.Contains(line, "reason=body_read") {
			return
		}
		current, err := registry.GetInstallation(id)
		if err != nil {
			t.Errorf("GetInstallation during retry: %v", err)
			return
		}
		progressAtRetry = current.BytesDownloaded
	}
	verified, err := registry.downloadAssetWithPosition(
		context.Background(),
		id,
		t.TempDir(),
		installation,
		asset,
		1,
		1,
	)
	if err != nil {
		t.Fatalf("downloadAssetWithPosition: %v", err)
	}
	if progressAtRetry != 0 {
		t.Fatalf("partial progress at retry=%d, want 0", progressAtRetry)
	}
	current, err := registry.GetInstallation(id)
	if err != nil ||
		current.BytesDownloaded != asset.Size ||
		verified.Size != asset.Size ||
		repository.assetAttemptCount(asset.Path) != 2 {
		t.Fatalf(
			"current=%#v verified=%#v attempts=%d err=%v",
			current,
			verified,
			repository.assetAttemptCount(asset.Path),
			err,
		)
	}
}

func TestDownloadAssetRetryDoesNotRollBackUnreportedOversizeByte(t *testing.T) {
	repository := newFakeHFRepository(t)
	registry := newTestRegistry(
		t,
		filepath.Join(t.TempDir(), "models"),
		repository,
		nil,
	)
	document := repository.assets["model_int8.onnx"]
	asset := Asset{Path: "model_int8.onnx", Size: int64(len(document))}
	id := InstallationID(
		repository.repoID,
		repository.revision,
		"cpu_int8",
	)
	installation := contract.PrivacyModelInstallation{
		ID:         id,
		Status:     contract.PrivacyModelStatusDownloading,
		BytesTotal: asset.Size,
		RepoID:     repository.repoID,
		Revision:   repository.revision,
		VariantID:  "cpu_int8",
	}
	registry.mu.Lock()
	registry.installations[id] = installation
	registry.mu.Unlock()
	var attempts atomic.Int64
	registry.httpClient = &http.Client{Transport: roundTripperFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		body := append([]byte(nil), document...)
		contentLength := int64(len(body))
		if attempts.Add(1) == 1 {
			body = append(body, 0xff)
			contentLength = -1
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        make(http.Header),
			Body:          io.NopCloser(bytes.NewReader(body)),
			ContentLength: contentLength,
			Request:       request,
		}, nil
	})}
	progressAtRetry := int64(-1)
	registry.logf = func(format string, arguments ...any) {
		if !strings.Contains(
			fmt.Sprintf(format, arguments...),
			"reason=body_read",
		) {
			return
		}
		current, err := registry.GetInstallation(id)
		if err != nil {
			t.Errorf("GetInstallation during retry: %v", err)
			return
		}
		progressAtRetry = current.BytesDownloaded
	}
	if _, err := registry.downloadAssetWithPosition(
		context.Background(),
		id,
		t.TempDir(),
		installation,
		asset,
		1,
		1,
	); err != nil {
		t.Fatalf("downloadAssetWithPosition: %v", err)
	}
	current, err := registry.GetInstallation(id)
	if err != nil ||
		progressAtRetry != 0 ||
		current.BytesDownloaded != asset.Size ||
		attempts.Load() != 2 {
		t.Fatalf(
			"progress_at_retry=%d current=%#v attempts=%d err=%v",
			progressAtRetry,
			current,
			attempts.Load(),
			err,
		)
	}
}

func TestNetworkFailureReasonClassifiesWrappedDNSFailure(t *testing.T) {
	cause := fmt.Errorf(
		"request failed: %w",
		&net.DNSError{Name: "huggingface.invalid", Err: "no such host"},
	)
	if reason := networkFailureReason(cause); reason != "dns" {
		t.Fatalf("reason=%q, want dns", reason)
	}
}

func TestValidateCalibrationDocumentRequiresExactFiniteBiases(t *testing.T) {
	for name, valid := range map[string][]byte{
		"openai": []byte(`{
			"operating_points":{
				"default":{
					"biases":{
						"transition_bias_background_stay":0.1,
						"transition_bias_background_to_start":0.2,
						"transition_bias_end_to_background":0.3,
						"transition_bias_end_to_start":0.4,
						"transition_bias_inside_to_continue":0.5,
						"transition_bias_inside_to_end":0.6
					}
				}
			}
		}`),
		"sheltron": []byte(`{
			"schema_version":"sheltron_privacy_filter_viterbi_calibration.v1",
			"decoder":"constrained_bioes_viterbi",
			"operating_point":{
				"status":"default_zero_bias_not_fitted",
				"transition_biases":{
					"transition_bias_background_stay":0.0,
					"transition_bias_background_to_start":0.0,
					"transition_bias_inside_to_continue":0.0,
					"transition_bias_inside_to_end":0.0,
					"transition_bias_end_to_background":0.0,
					"transition_bias_end_to_start":0.0
				}
			}
		}`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCalibrationDocument(valid); err != nil {
				t.Fatalf("valid calibration: %v", err)
			}
		})
	}
	for name, document := range map[string][]byte{
		"missing": []byte(`{
			"operating_points":{"default":{"biases":{
				"transition_bias_background_stay":0.1,
				"transition_bias_background_to_start":0.2,
				"transition_bias_end_to_background":0.3,
				"transition_bias_end_to_start":0.4,
				"transition_bias_inside_to_continue":0.5
			}}}
		}`),
		"extra": []byte(`{
			"operating_points":{"default":{"biases":{
				"transition_bias_background_stay":0.1,
				"transition_bias_background_to_start":0.2,
				"transition_bias_end_to_background":0.3,
				"transition_bias_end_to_start":0.4,
				"transition_bias_inside_to_continue":0.5,
				"transition_bias_inside_to_end":0.6,
				"unexpected":0.7
			}}}
		}`),
		"non_finite": []byte(`{
			"operating_points":{"default":{"biases":{
				"transition_bias_background_stay":1e309,
				"transition_bias_background_to_start":0.2,
				"transition_bias_end_to_background":0.3,
				"transition_bias_end_to_start":0.4,
				"transition_bias_inside_to_continue":0.5,
				"transition_bias_inside_to_end":0.6
			}}}
		}`),
		"float32_overflow": []byte(`{
			"operating_points":{"default":{"biases":{
				"transition_bias_background_stay":3.5e38,
				"transition_bias_background_to_start":0.2,
				"transition_bias_end_to_background":0.3,
				"transition_bias_end_to_start":0.4,
				"transition_bias_inside_to_continue":0.5,
				"transition_bias_inside_to_end":0.6
			}}}
		}`),
		"case_variant_root": []byte(`{
			"Operating_Point":{"transition_biases":{
				"transition_bias_background_stay":0.1,
				"transition_bias_background_to_start":0.2,
				"transition_bias_end_to_background":0.3,
				"transition_bias_end_to_start":0.4,
				"transition_bias_inside_to_continue":0.5,
				"transition_bias_inside_to_end":0.6
			}}
		}`),
		"case_variant_nested": []byte(`{
			"operating_point":{"Transition_Biases":{
				"transition_bias_background_stay":0.1,
				"transition_bias_background_to_start":0.2,
				"transition_bias_end_to_background":0.3,
				"transition_bias_end_to_start":0.4,
				"transition_bias_inside_to_continue":0.5,
				"transition_bias_inside_to_end":0.6
			}}
		}`),
		"case_variant_bias": []byte(`{
			"operating_point":{"transition_biases":{
				"Transition_bias_background_stay":0.1,
				"transition_bias_background_to_start":0.2,
				"transition_bias_end_to_background":0.3,
				"transition_bias_end_to_start":0.4,
				"transition_bias_inside_to_continue":0.5,
				"transition_bias_inside_to_end":0.6
			}}
		}`),
		"ambiguous": []byte(`{
			"operating_points":{"default":{"biases":{
				"transition_bias_background_stay":0.1,
				"transition_bias_background_to_start":0.2,
				"transition_bias_end_to_background":0.3,
				"transition_bias_end_to_start":0.4,
				"transition_bias_inside_to_continue":0.5,
				"transition_bias_inside_to_end":0.6
			}}},
			"operating_point":{"transition_biases":{
				"transition_bias_background_stay":0.1,
				"transition_bias_background_to_start":0.2,
				"transition_bias_end_to_background":0.3,
				"transition_bias_end_to_start":0.4,
				"transition_bias_inside_to_continue":0.5,
				"transition_bias_inside_to_end":0.6
			}}
		}`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCalibrationDocument(document); !errors.Is(
				err,
				errModelIncompatible,
			) {
				t.Fatalf("invalid calibration error=%v", err)
			}
		})
	}
}

func TestValidateStagedCompatibilityAcceptsSheltronCalibrationSchema(t *testing.T) {
	plan, exists := builtinVariantPlan(
		"sheltron-ai/privacy-filter-ettin-32m",
		"53d55c58fdbb5ed2ace902a374f664c1cf4914c7",
		"cpu_int8",
	)
	if !exists {
		t.Fatal("missing Sheltron built-in plan")
	}
	id2label := map[string]string{"0": "O"}
	index := 1
	for _, source := range []string{
		"account_number",
		"private_address",
		"private_date",
		"private_email",
		"private_person",
		"private_phone",
		"private_url",
		"secret",
	} {
		for _, prefix := range []string{"B", "I", "E", "S"} {
			id2label[strconv.Itoa(index)] = prefix + "-" + source
			index++
		}
	}
	config, err := json.Marshal(map[string]any{"id2label": id2label})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	for path, document := range map[string][]byte{
		"config.json":    config,
		"tokenizer.json": []byte(`{"version":"1.0"}`),
		"viterbi_calibration.json": []byte(`{
			"schema_version":"sheltron_privacy_filter_viterbi_calibration.v1",
			"decoder":"constrained_bioes_viterbi",
			"operating_point":{
				"status":"default_zero_bias_not_fitted",
				"transition_biases":{
					"transition_bias_background_stay":0.0,
					"transition_bias_background_to_start":0.0,
					"transition_bias_inside_to_continue":0.0,
					"transition_bias_inside_to_end":0.0,
					"transition_bias_end_to_background":0.0,
					"transition_bias_end_to_start":0.0
				}
			}
		}`),
	} {
		if err := os.WriteFile(
			filepath.Join(directory, path),
			document,
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateStagedCompatibility(
		directory,
		contract.PrivacyModelInstallation{
			Adapter:      plan.item.Adapter,
			LabelMapping: defaultOpenAILabelMapping(),
		},
		plan.runtime,
	); err != nil {
		t.Fatalf("Sheltron staged compatibility: %v", err)
	}
}

func TestRegistryDeleteTombstoneBlocksConcurrentRetry(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.requestedRevision = repository.revision
	repository.blockAsset = "model_int8.onnx"
	repository.blockStarted = make(chan struct{})
	sqliteStore := openRegistryStore(t)
	blockingStore := &deleteBlockingInstallationStore{
		PrivacyModelInstallationStore: sqliteStore,
		deleteStarted:                 make(chan struct{}),
		releaseDelete:                 make(chan struct{}),
	}
	registry := newTestRegistry(
		t,
		filepath.Join(t.TempDir(), "models"),
		repository,
		blockingStore,
	)
	request := contract.PrivacyModelInstallRequest{
		RepoID: repository.repoID, Revision: repository.revision,
		VariantID: "cpu_int8", LabelMapping: emailMapping(),
	}
	started, err := registry.Install(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-repository.blockStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("download did not reach blocked model asset")
	}
	deleteResult := make(chan error, 1)
	go func() {
		deleteResult <- registry.DeleteInstallation(
			context.Background(),
			started.ID,
		)
	}()
	select {
	case <-blockingStore.deleteStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("delete did not reach persistent tombstone window")
	}
	if _, err := registry.Install(
		context.Background(),
		request,
	); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent retry error = %v, want ErrBusy", err)
	}
	close(blockingStore.releaseDelete)
	select {
	case err := <-deleteResult:
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delete did not finish")
	}
	if _, err := registry.GetInstallation(started.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("installation survived delete: %v", err)
	}
}

func TestRegistryDeleteFailureImmediatelyInvalidatesReadyInstallation(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.requestedRevision = repository.revision
	sqliteStore := openRegistryStore(t)
	deleteErr := errors.New("injected persistent delete failure")
	failingStore := &deleteFailOnceInstallationStore{
		PrivacyModelInstallationStore: sqliteStore,
		err:                           deleteErr,
	}
	registry := newTestRegistry(
		t,
		filepath.Join(t.TempDir(), "models"),
		repository,
		failingStore,
	)
	started, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID:       repository.repoID,
			Revision:     repository.revision,
			VariantID:    "cpu_int8",
			LabelMapping: emailMapping(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ready := waitForInstallation(t, registry, started.ID)
	if ready.Status != contract.PrivacyModelStatusReady {
		t.Fatalf("installation did not become ready: %#v", ready)
	}
	if err := registry.DeleteInstallation(
		context.Background(),
		ready.ID,
	); !errors.Is(err, deleteErr) {
		t.Fatalf("first delete error=%v", err)
	}
	failed, err := registry.GetInstallation(ready.ID)
	if err != nil ||
		failed.Status != contract.PrivacyModelStatusError ||
		failed.Error == nil ||
		*failed.Error != contract.PrivacyModelErrorIntegrity {
		t.Fatalf("invalidated installation=%#v err=%v", failed, err)
	}
	if _, exists := registry.ReadyInstallation(ready.ID); exists {
		t.Fatal("ReadyInstallation remained true after files were removed")
	}
	persisted, err := sqliteStore.GetPrivacyModelInstallation(
		context.Background(),
		ready.ID,
	)
	if err != nil ||
		persisted.Installation.Status != contract.PrivacyModelStatusError ||
		persisted.Manifest != nil {
		t.Fatalf("persisted invalidated installation=%#v err=%v", persisted, err)
	}
	if err := registry.DeleteInstallation(
		context.Background(),
		ready.ID,
	); err != nil {
		t.Fatalf("retry delete: %v", err)
	}
	if _, err := registry.GetInstallation(
		ready.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("installation survived retry delete: %v", err)
	}
}

func TestRegistryPartialFileDeleteFailureCannotRemainReady(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.requestedRevision = repository.revision
	store := openRegistryStore(t)
	registry := newTestRegistry(
		t,
		filepath.Join(t.TempDir(), "models"),
		repository,
		store,
	)
	started, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID:       repository.repoID,
			Revision:     repository.revision,
			VariantID:    "cpu_int8",
			LabelMapping: emailMapping(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ready := waitForInstallation(t, registry, started.ID)
	if ready.Status != contract.PrivacyModelStatusReady {
		t.Fatalf("installation did not become ready: %#v", ready)
	}
	deleteErr := errors.New("injected partial filesystem delete failure")
	registry.removeAll = func(directory string) error {
		if err := os.Remove(
			filepath.Join(directory, InstallationManifestName),
		); err != nil {
			t.Fatalf("remove manifest in injected delete: %v", err)
		}
		return deleteErr
	}
	if err := registry.DeleteInstallation(
		context.Background(),
		ready.ID,
	); !errors.Is(err, ErrFilesystem) {
		t.Fatalf("partial delete error=%v", err)
	}
	failed, err := registry.GetInstallation(ready.ID)
	if err != nil ||
		failed.Status != contract.PrivacyModelStatusError ||
		failed.Error == nil ||
		*failed.Error != contract.PrivacyModelErrorIntegrity {
		t.Fatalf("invalidated installation=%#v err=%v", failed, err)
	}
	if _, exists := registry.ReadyInstallation(ready.ID); exists {
		t.Fatal("partial filesystem deletion remained ready")
	}
	registry.removeAll = os.RemoveAll
	if err := registry.DeleteInstallation(
		context.Background(),
		ready.ID,
	); err != nil {
		t.Fatalf("retry delete: %v", err)
	}
}

type deleteBlockingInstallationStore struct {
	storage.PrivacyModelInstallationStore
	deleteStarted chan struct{}
	releaseDelete chan struct{}
}

func (store *deleteBlockingInstallationStore) DeletePrivacyModelInstallation(
	ctx context.Context,
	id contract.PrivacyModelID,
) error {
	select {
	case <-store.deleteStarted:
	default:
		close(store.deleteStarted)
	}
	select {
	case <-store.releaseDelete:
	case <-ctx.Done():
		return ctx.Err()
	}
	return store.PrivacyModelInstallationStore.
		DeletePrivacyModelInstallation(ctx, id)
}

type deleteFailOnceInstallationStore struct {
	storage.PrivacyModelInstallationStore
	failed atomic.Bool
	err    error
}

func (store *deleteFailOnceInstallationStore) DeletePrivacyModelInstallation(
	ctx context.Context,
	id contract.PrivacyModelID,
) error {
	if !store.failed.Swap(true) {
		return store.err
	}
	return store.PrivacyModelInstallationStore.
		DeletePrivacyModelInstallation(ctx, id)
}

func openRegistryStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(
		context.Background(),
		filepath.Join(t.TempDir(), "astrlink.db"),
	)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newTestRegistry(
	t *testing.T,
	root string,
	repository *fakeHFRepository,
	store storage.PrivacyModelInstallationStore,
) *Registry {
	t.Helper()
	registry, err := NewRegistry(context.Background(), RegistryConfig{
		RootDirectory:        root,
		MetadataBaseURL:      repository.server.URL,
		HTTPClient:           repository.server.Client(),
		Store:                store,
		TestOnlyLoopbackMode: true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

func waitForInstallation(
	t *testing.T,
	registry *Registry,
	id contract.PrivacyModelID,
) contract.PrivacyModelInstallation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		installation, err := registry.GetInstallation(id)
		if err != nil {
			t.Fatalf("GetInstallation: %v", err)
		}
		if installation.Status != contract.PrivacyModelStatusDownloading {
			return installation
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("installation %s did not finish", id)
	return contract.PrivacyModelInstallation{}
}

func emailMapping() map[string]*contract.CanonicalKind {
	kind := contract.CanonicalKindEmail
	return map[string]*contract.CanonicalKind{"EMAIL": &kind}
}

func nymConfigDocument(t *testing.T) []byte {
	t.Helper()
	mapping := defaultNymLabelMapping()
	labels := make([]string, 0, len(mapping))
	for label := range mapping {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	id2label := map[string]string{"0": "O"}
	for index, label := range labels {
		id2label[strconv.Itoa(index*2+1)] = "B-" + label
		id2label[strconv.Itoa(index*2+2)] = "I-" + label
	}
	document, err := json.Marshal(map[string]any{
		"architectures": []string{"TinyForTokenClassification"},
		"id2label":      id2label,
	})
	if err != nil {
		t.Fatalf("marshal Nym config: %v", err)
	}
	return document
}

func TestRegistryNeverUsesProductionHuggingFaceHostInTestMode(t *testing.T) {
	t.Setenv(noRemoteModelsEnvironment, "1")
	_, err := NewRegistry(context.Background(), RegistryConfig{
		RootDirectory: filepath.Join(t.TempDir(), "models"),
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewRegistry error = %v", err)
	}
}

func TestRegistryWeightRedirectCannotEscapeLoopbackGuard(t *testing.T) {
	t.Setenv(noRemoteModelsEnvironment, "1")
	repository := newFakeHFRepository(t)
	repository.requestedRevision = repository.revision
	repository.redirectAsset = "model_int8.onnx"
	repository.redirectURL = "https://huggingface.co/acme/model/weights"
	var remoteRoundTrips atomic.Int64
	baseClient := repository.server.Client()
	client := &http.Client{Transport: roundTripperFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		if !isLoopbackHostname(request.URL.Hostname()) {
			remoteRoundTrips.Add(1)
		}
		return baseClient.Transport.RoundTrip(request)
	})}
	registry, err := NewRegistry(context.Background(), RegistryConfig{
		RootDirectory:        filepath.Join(t.TempDir(), "models"),
		MetadataBaseURL:      repository.server.URL,
		HTTPClient:           client,
		TestOnlyLoopbackMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID:       repository.repoID,
			Revision:     repository.revision,
			VariantID:    "cpu_int8",
			LabelMapping: emailMapping(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForInstallation(t, registry, started.ID)
	if failed.Status != contract.PrivacyModelStatusError ||
		failed.Error == nil ||
		*failed.Error != contract.PrivacyModelErrorDownload {
		t.Fatalf("redirect terminal state=%#v", failed)
	}
	if remoteRoundTrips.Load() != 0 {
		t.Fatalf("remote weight round trips=%d", remoteRoundTrips.Load())
	}
}

func TestRegistryRejectsPersistedCatalogProvenanceTampering(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.requestedRevision = repository.revision
	store := openRegistryStore(t)
	root := filepath.Join(t.TempDir(), "models")
	registry := newTestRegistry(t, root, repository, store)
	started, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID:       repository.repoID,
			Revision:     repository.revision,
			VariantID:    "cpu_int8",
			LabelMapping: emailMapping(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ready := waitForInstallation(t, registry, started.ID)
	record, err := store.GetPrivacyModelInstallation(
		context.Background(),
		ready.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	fakeCatalogID := CatalogOpenAIPrivacyFilter
	record.Installation.Source = contract.PrivacyModelSourceCatalog
	record.Installation.CatalogID = &fakeCatalogID
	corruptStore := &listOverrideInstallationStore{
		PrivacyModelInstallationStore: store,
		records:                       []storage.PrivacyModelInstallationRecord{record},
	}
	_, err = NewRegistry(context.Background(), RegistryConfig{
		RootDirectory:        root,
		MetadataBaseURL:      repository.server.URL,
		HTTPClient:           repository.server.Client(),
		Store:                corruptStore,
		TestOnlyLoopbackMode: true,
	})
	if !errors.Is(err, storage.ErrInvalidRecord) {
		t.Fatalf("provenance tampering error=%v", err)
	}
}

func TestRegistryRejectsCatalogManifestDriftFromBuiltinPlan(t *testing.T) {
	for name, mutate := range map[string]func(*normalizedManifest){
		"runtime": func(manifest *normalizedManifest) {
			manifest.Window /= 2
		},
		"files": func(manifest *normalizedManifest) {
			manifest.Files[0].SHA256 = strings.Repeat("b", sha256.Size*2)
		},
	} {
		t.Run(name, func(t *testing.T) {
			repository := newFakeHFRepository(t)
			store := openRegistryStore(t)
			root := filepath.Join(t.TempDir(), "models")
			plan, exists := builtinVariantPlan(
				"openai/privacy-filter",
				DefaultRevision,
				"cpu_q4",
			)
			if !exists {
				t.Fatal("missing OpenAI built-in plan")
			}
			id := InstallationID(
				plan.item.RepoID,
				plan.item.Revision,
				plan.variant.ID,
			)
			catalogID := plan.item.ID
			catalogSource := plan.item.Source
			license := plan.item.License
			installedAt := "2026-07-24T00:00:00Z"
			installation := contract.PrivacyModelInstallation{
				ID: id, Source: contract.PrivacyModelSourceCatalog,
				CatalogID: &catalogID, CatalogSource: &catalogSource,
				Name: plan.item.Name, License: &license,
				Languages: cloneStrings(plan.item.Languages),
				RepoID:    plan.item.RepoID, Revision: plan.item.Revision,
				VariantID: plan.variant.ID, VariantName: plan.variant.Name,
				Quantization:      plan.variant.Quantization,
				Adapter:           plan.item.Adapter,
				Status:            contract.PrivacyModelStatusReady,
				BytesDownloaded:   plan.variant.BytesTotal,
				BytesTotal:        plan.variant.BytesTotal,
				EstimatedRAMBytes: plan.variant.EstimatedRAMBytes,
				LabelMapping:      defaultOpenAILabelMapping(),
				InstalledAt:       &installedAt,
			}
			manifest := buildNormalizedManifest(
				installation,
				plan.runtime,
				plan.assets,
			)
			mutate(&manifest)
			directory := filepath.Join(
				root,
				"installations",
				string(id),
			)
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := writeNormalizedManifest(directory, manifest); err != nil {
				t.Fatalf("write tampered manifest: %v", err)
			}
			document, err := os.ReadFile(
				filepath.Join(directory, InstallationManifestName),
			)
			if err != nil {
				t.Fatal(err)
			}
			binding := manifestBinding(manifest.Identity, document)
			if err := store.PutPrivacyModelInstallation(
				context.Background(),
				storage.PrivacyModelInstallationRecord{
					Installation: installation,
					Manifest:     &binding,
				},
			); err != nil {
				t.Fatalf("seed persisted installation: %v", err)
			}
			registry := newTestRegistry(t, root, repository, store)
			loaded, err := registry.GetInstallation(id)
			if err != nil ||
				loaded.Status != contract.PrivacyModelStatusError ||
				loaded.Error == nil ||
				*loaded.Error != contract.PrivacyModelErrorIntegrity {
				t.Fatalf("loaded=%#v err=%v", loaded, err)
			}
			if _, ready := registry.ReadyInstallation(id); ready {
				t.Fatal("drifted catalog manifest remained ready")
			}
		})
	}
}

type listOverrideInstallationStore struct {
	storage.PrivacyModelInstallationStore
	records []storage.PrivacyModelInstallationRecord
}

func (store *listOverrideInstallationStore) ListPrivacyModelInstallations(
	context.Context,
) ([]storage.PrivacyModelInstallationRecord, error) {
	return store.records, nil
}

func TestOpenAICatalogMappingAllowsCompleteRemapAndRejectsMissingKeys(t *testing.T) {
	repository := newFakeHFRepository(t)
	registry := newTestRegistry(
		t,
		filepath.Join(t.TempDir(), "models"),
		repository,
		nil,
	)
	remapped := defaultOpenAILabelMapping()
	phone := contract.CanonicalKindPhone
	remapped["private_email"] = &phone
	remapped["secret"] = nil
	plan, err := registry.prepareInstallation(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID:       "openai/privacy-filter",
			Revision:     DefaultRevision,
			VariantID:    "cpu_q4",
			LabelMapping: remapped,
		},
	)
	if err != nil ||
		!labelMappingsEqual(plan.installation.LabelMapping, remapped) {
		t.Fatalf("complete remap plan=%#v err=%v", plan, err)
	}
	delete(remapped, "private_email")
	if _, err := registry.prepareInstallation(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID:       "openai/privacy-filter",
			Revision:     DefaultRevision,
			VariantID:    "cpu_q4",
			LabelMapping: remapped,
		},
	); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("incomplete remap error=%v", err)
	}
	defaultPlan, err := registry.prepareInstallation(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID:       "openai/privacy-filter",
			Revision:     DefaultRevision,
			VariantID:    "cpu_q4",
			LabelMapping: map[string]*contract.CanonicalKind{},
		},
	)
	if err != nil ||
		!labelMappingsEqual(
			defaultPlan.installation.LabelMapping,
			defaultOpenAILabelMapping(),
		) ||
		defaultPlan.installation.CatalogSource == nil ||
		*defaultPlan.installation.CatalogSource !=
			contract.PrivacyModelCatalogSourceOfficial ||
		defaultPlan.installation.License == nil ||
		*defaultPlan.installation.License != "Apache-2.0" ||
		len(defaultPlan.installation.Languages) != 1 ||
		defaultPlan.installation.Languages[0] != "en" {
		t.Fatalf("default mapping plan=%#v err=%v", defaultPlan, err)
	}
	if !validInstallationProvenance(defaultPlan.installation) {
		t.Fatal("catalog plan failed its own provenance check")
	}
	tampered := defaultPlan.installation
	tamperedLicense := "MIT"
	tampered.License = &tamperedLicense
	if validInstallationProvenance(tampered) {
		t.Fatal("catalog license tampering passed provenance validation")
	}
}

func TestNymCatalogUsesCompleteDefaultMapping(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.repoID = "Wismut/nym-pii-multilingual-small"
	repository.revision = "4348999cd3c2e20c49615e9af7c6bbb45b64cd85"
	repository.requestedRevision = repository.revision
	repository.assets = map[string][]byte{
		"config.json":               nymConfigDocument(t),
		"tokenizer.json":            []byte(`{"version":"1.0","model":{"type":"WordPiece"}}`),
		"edge-int8/model_int8.onnx": []byte("metadata-only fake model"),
	}
	repository.lfs = map[string]bool{
		"edge-int8/model_int8.onnx": true,
	}
	registry := newTestRegistry(
		t,
		filepath.Join(t.TempDir(), "models"),
		repository,
		nil,
	)
	plan, err := registry.prepareInstallation(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID:    repository.repoID,
			Revision:  repository.revision,
			VariantID: "edge_int8",
		},
	)
	if err != nil ||
		!labelMappingsEqual(
			plan.installation.LabelMapping,
			defaultNymLabelMapping(),
		) {
		t.Fatalf("default Nym mapping plan=%#v err=%v", plan, err)
	}
	if plan.installation.CatalogID == nil ||
		*plan.installation.CatalogID != CatalogNymPIIMultilingualSmall {
		t.Fatalf("Nym catalog provenance=%#v", plan.installation)
	}
	if repository.assetRequests.Load() != 2 {
		t.Fatalf(
			"asset requests=%d, want config+tokenizer only",
			repository.assetRequests.Load(),
		)
	}
}

func TestNymCatalogRejectsIncompleteMappingBeforeWeightDownload(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.repoID = "Wismut/nym-pii-multilingual-small"
	repository.revision = "4348999cd3c2e20c49615e9af7c6bbb45b64cd85"
	repository.requestedRevision = repository.revision
	repository.assets = map[string][]byte{
		"config.json": []byte(`{
			"architectures":["TinyForTokenClassification"],
			"id2label":{
				"0":"O",
				"1":"B-EMAIL","2":"I-EMAIL",
				"3":"B-PHONE","4":"I-PHONE"
			}
		}`),
		"tokenizer.json":            []byte(`{"version":"1.0","model":{"type":"WordPiece"}}`),
		"edge-int8/model_int8.onnx": []byte("metadata-only fake model"),
	}
	repository.lfs = map[string]bool{
		"edge-int8/model_int8.onnx": true,
	}
	registry := newTestRegistry(
		t,
		filepath.Join(t.TempDir(), "models"),
		repository,
		nil,
	)
	_, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID:       repository.repoID,
			Revision:     repository.revision,
			VariantID:    "edge_int8",
			LabelMapping: emailMapping(),
		},
	)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("incomplete mapping error=%v", err)
	}
	if repository.assetRequests.Load() != 2 {
		t.Fatalf("asset requests=%d, want config+tokenizer only", repository.assetRequests.Load())
	}
	if len(registry.ListInstallations()) != 0 {
		t.Fatalf("rejected mapping created installation: %#v", registry.ListInstallations())
	}
}

func TestInstallationDirectoryContainsOnlyNormalizedManifestName(t *testing.T) {
	if strings.Contains(InstallationManifestName, "/") ||
		InstallationManifestName != "astrlink-model.json" {
		t.Fatalf("manifest name drifted: %q", InstallationManifestName)
	}
}
