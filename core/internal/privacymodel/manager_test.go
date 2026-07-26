package privacymodel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type httpClientFunc func(*http.Request) (*http.Response, error)

func (function httpClientFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestProductionManifestIsPinned(t *testing.T) {
	manifest := ProductionManifest()
	if manifest.Revision != DefaultRevision || DefaultBaseURL !=
		"https://huggingface.co/openai/privacy-filter/resolve/7ffa9a043d54d1be65afb281eddf0ffbe629385b/" {
		t.Fatalf("production revision/base URL drifted: %#v %q", manifest, DefaultBaseURL)
	}
	expected := []Asset{
		{"onnx/model_q4.onnx", 160_219, "8f7dee8b46d096f052b359375dfba5d983cc4d18c44a783bf548615c472f8dea"},
		{"onnx/model_q4.onnx_data", 917_120_144, "f30998e28c71c5374cc7e8b7de8f0f83e981592c0c2d652d2ad4928454dbb496"},
		{"tokenizer.json", 27_868_174, "0614fe83cadab421296e664e1f48f4261fa8fef6e03e63bb75c20f38e37d07d3"},
		{"config.json", 3_039, "b2b26a4a4a000639ad30b0c264adbefe365bdb567fbd7bb27303b8c438375bd1"},
		{"viterbi_calibration.json", 372, "bbc8611ef08a55ed72d64856cbbbb9a91db8dfa881f0a92e2afbad6e4bbc775a"},
	}
	if len(manifest.Assets) != len(expected) {
		t.Fatalf("asset count=%d", len(manifest.Assets))
	}
	for index := range expected {
		if manifest.Assets[index] != expected[index] {
			t.Fatalf("asset[%d]=%#v want %#v", index, manifest.Assets[index], expected[index])
		}
	}
}

func TestManagerDownloadsValidatesAndAtomicallyPublishesAssets(t *testing.T) {
	files := map[string][]byte{
		"onnx/model.onnx":  []byte("graph"),
		"onnx/model.data":  []byte("weights"),
		"tokenizer.json":   []byte(`{"tokenizer":true}`),
		"config.json":      []byte(`{"model":"test"}`),
		"calibration.json": []byte(`{"threshold":0.5}`),
	}
	manifest := manifestForFiles("test-revision", files)
	client := httpClientFunc(func(request *http.Request) (*http.Response, error) {
		name := strings.TrimPrefix(request.URL.Path, "/fixed/")
		body, ok := files[name]
		if !ok {
			t.Fatalf("unexpected download URL %q", request.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK, ContentLength: int64(len(body)),
			Body: io.NopCloser(bytes.NewReader(body)),
		}, nil
	})
	root := t.TempDir()
	manager, err := NewManager(context.Background(), Config{
		RootDirectory: root, BaseURL: "https://models.example/fixed/",
		Manifest: manifest, HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	initial := manager.Status()
	if initial.Status != StatusNotInstalled || initial.BytesDownloaded != 0 ||
		initial.BytesTotal != manifestSize(manifest) || initial.Error != nil {
		t.Fatalf("initial status=%#v", initial)
	}
	started, err := manager.Start()
	if err != nil || started.Status != StatusDownloading {
		t.Fatalf("Start=%#v, %v", started, err)
	}
	if _, err := manager.Start(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start error=%v", err)
	}
	ready := waitForStatus(t, manager, StatusReady)
	if ready.BytesDownloaded != ready.BytesTotal || ready.Error != nil {
		t.Fatalf("ready status=%#v", ready)
	}
	if directory, ok := manager.ReadyDirectory(); !ok ||
		directory != filepath.Join(root, manifest.Revision) {
		t.Fatalf("ReadyDirectory=(%q,%t)", directory, ok)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(root, manifest.Revision, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("published %s=%q, %v", name, got, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".privacy-model-download-") {
			t.Fatalf("temporary download survived publication: %s", entry.Name())
		}
	}
	if _, err := manager.Start(); !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("ready Start error=%v", err)
	}

	reopened, err := NewManager(context.Background(), Config{
		RootDirectory: root, BaseURL: "https://models.example/fixed/",
		Manifest: manifest, HTTPClient: client,
	})
	if err != nil || reopened.Status().Status != StatusReady {
		t.Fatalf("reopened status=%#v, %v", reopened.Status(), err)
	}
	if err := reopened.Delete(context.Background()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if status := reopened.Status(); status.Status != StatusNotInstalled || status.BytesDownloaded != 0 {
		t.Fatalf("status after delete=%#v", status)
	}
	if directory, ok := reopened.ReadyDirectory(); ok || directory != "" {
		t.Fatalf("ReadyDirectory after delete=(%q,%t)", directory, ok)
	}
	if _, err := os.Stat(filepath.Join(root, manifest.Revision)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published directory survived delete: %v", err)
	}
}

func TestManagerRejectsBadAssetWithoutExposingResponseBodyOrPath(t *testing.T) {
	files := map[string][]byte{"model.bin": []byte("expected")}
	manifest := manifestForFiles("test-revision", files)
	const sensitiveBody = "private upstream body /Users/alice/secret"
	manager, err := NewManager(context.Background(), Config{
		RootDirectory: t.TempDir(), BaseURL: "https://models.example/",
		Manifest: manifest,
		HTTPClient: httpClientFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader(sensitiveBody)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	failed := waitForStatus(t, manager, StatusError)
	if failed.Error == nil || *failed.Error != errorDownload {
		t.Fatalf("failure status=%#v", failed)
	}
	encoded := failed.Status.String() + " " + *failed.Error
	if strings.Contains(encoded, sensitiveBody) || strings.Contains(encoded, "/Users/") {
		t.Fatalf("sensitive response/path leaked: %s", encoded)
	}
}

func TestManagerRejectsPerFileHashMismatchBeforePublication(t *testing.T) {
	files := map[string][]byte{"model.bin": []byte("expected")}
	manifest := manifestForFiles("test-revision", files)
	root := t.TempDir()
	manager, err := NewManager(context.Background(), Config{
		RootDirectory: root, BaseURL: "https://models.example/",
		Manifest: manifest,
		HTTPClient: httpClientFunc(func(*http.Request) (*http.Response, error) {
			body := []byte("tampered")
			return &http.Response{
				StatusCode: http.StatusOK, ContentLength: int64(len(body)),
				Body: io.NopCloser(bytes.NewReader(body)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	if status := waitForStatus(t, manager, StatusError); status.Error == nil ||
		*status.Error != errorDownload {
		t.Fatalf("hash failure status=%#v", status)
	}
	if _, err := os.Stat(filepath.Join(root, manifest.Revision)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unverified model was published: %v", err)
	}
}

func TestManagerRevalidatesCompleteStagingTreeBeforePublication(t *testing.T) {
	files := map[string][]byte{
		"first.bin":  []byte("first"),
		"second.bin": []byte("second"),
	}
	manifest := manifestForFiles("test-revision", files)
	root := t.TempDir()
	requestCount := 0
	manager, err := NewManager(context.Background(), Config{
		RootDirectory: root, BaseURL: "https://models.example/",
		Manifest: manifest,
		HTTPClient: httpClientFunc(func(request *http.Request) (*http.Response, error) {
			requestCount++
			if requestCount == 2 {
				entries, readErr := os.ReadDir(root)
				if readErr != nil {
					t.Fatal(readErr)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".privacy-model-download-") {
						if removeErr := os.RemoveAll(filepath.Join(root, entry.Name())); removeErr != nil {
							t.Fatal(removeErr)
						}
					}
				}
			}
			body := files[strings.TrimPrefix(request.URL.Path, "/")]
			return &http.Response{
				StatusCode: http.StatusOK, ContentLength: int64(len(body)),
				Body: io.NopCloser(bytes.NewReader(body)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	if status := waitForStatus(t, manager, StatusError); status.Error == nil ||
		*status.Error != errorDownload {
		t.Fatalf("incomplete staging status=%#v", status)
	}
	if _, err := os.Stat(filepath.Join(root, manifest.Revision)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete staging tree was published: %v", err)
	}
}

func TestManagerRejectsSameSizePublishedAssetCorruptionOnRestart(t *testing.T) {
	files := map[string][]byte{"model.bin": []byte("expected")}
	manifest := manifestForFiles("test-revision", files)
	root := t.TempDir()
	client := httpClientFunc(func(*http.Request) (*http.Response, error) {
		body := files["model.bin"]
		return &http.Response{
			StatusCode: http.StatusOK, ContentLength: int64(len(body)),
			Body: io.NopCloser(bytes.NewReader(body)),
		}, nil
	})
	manager, err := NewManager(context.Background(), Config{
		RootDirectory: root, BaseURL: "https://models.example/",
		Manifest: manifest, HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, manager, StatusReady)
	if err := os.WriteFile(
		filepath.Join(root, manifest.Revision, "model.bin"),
		[]byte("tampered"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewManager(context.Background(), Config{
		RootDirectory: root, BaseURL: "https://models.example/",
		Manifest: manifest, HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status := reopened.Status(); status.Status != StatusError ||
		status.Error == nil || *status.Error != errorDownload {
		t.Fatalf("corrupt reopened status=%#v", status)
	}
	if directory, ok := reopened.ReadyDirectory(); ok || directory != "" {
		t.Fatalf("corrupt ReadyDirectory=(%q,%t)", directory, ok)
	}
	if started, err := reopened.Start(); err != nil || started.Status != StatusDownloading {
		t.Fatalf("retry Start=%#v, %v", started, err)
	}
	if ready := waitForStatus(t, reopened, StatusReady); ready.Error != nil {
		t.Fatalf("retry status=%#v", ready)
	}
	repaired, err := os.ReadFile(filepath.Join(root, manifest.Revision, "model.bin"))
	if err != nil || !bytes.Equal(repaired, files["model.bin"]) {
		t.Fatalf("repaired asset=%q, %v", repaired, err)
	}
}

func TestManagerDeleteCancelsDownload(t *testing.T) {
	files := map[string][]byte{"model.bin": []byte("expected")}
	manifest := manifestForFiles("test-revision", files)
	requestStarted := make(chan struct{})
	manager, err := NewManager(context.Background(), Config{
		RootDirectory: t.TempDir(), BaseURL: "https://models.example/",
		Manifest: manifest,
		HTTPClient: httpClientFunc(func(request *http.Request) (*http.Response, error) {
			close(requestStarted)
			<-request.Context().Done()
			return nil, request.Context().Err()
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	<-requestStarted
	deleteContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Delete(deleteContext); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if status := manager.Status(); status.Status != StatusNotInstalled || status.Error != nil {
		t.Fatalf("status after cancellation=%#v", status)
	}
}

func TestManagerOnlyCleansStaleAbandonedDownloads(t *testing.T) {
	root := t.TempDir()
	recent := filepath.Join(root, ".privacy-model-download-recent")
	stale := filepath.Join(root, ".privacy-model-download-stale")
	active := filepath.Join(root, ".privacy-model-download-active")
	if err := os.Mkdir(recent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(active, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "in-progress"), []byte("chunk"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-abandonedDownloadStaleAge - time.Minute)
	if err := os.Chtimes(stale, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(active, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"model.bin": []byte("expected")}
	if _, err := NewManager(context.Background(), Config{
		RootDirectory: root,
		BaseURL:       "https://models.example/",
		Manifest:      manifestForFiles("test-revision", files),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent active staging directory was removed: %v", err)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("staging with recent file progress was removed: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale staging directory survived cleanup: %v", err)
	}
}

func TestSyncStagedDirectoriesFlushesNestedParentsBeforeRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "model-root")
	var synced []string
	err := syncStagedDirectories(root, []Asset{
		{Path: "onnx/nested/model.bin"},
		{Path: "tokenizer.json"},
	}, func(directory string) error {
		synced = append(synced, directory)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(root, "onnx", "nested"),
		filepath.Join(root, "onnx"),
		root,
	}
	if len(synced) != len(want) {
		t.Fatalf("synced=%#v want %#v", synced, want)
	}
	for index := range want {
		if synced[index] != want[index] {
			t.Fatalf("synced[%d]=%q want %q; all=%#v", index, synced[index], want[index], synced)
		}
	}
}

func TestStagedTreeHashingHonorsCancellationBetweenChunks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterRead{cancel: cancel}
	if err := hashFile(ctx, sha256.New(), reader); !errors.Is(err, context.Canceled) {
		t.Fatalf("hashFile error=%v, want context cancellation", err)
	}
}

func TestManagerRejectsUnsafeManifest(t *testing.T) {
	validHash := strings.Repeat("0", 64)
	for _, manifest := range []Manifest{
		{Revision: "valid"},
		{Revision: "..", Assets: []Asset{{Path: "x", Size: 1, SHA256: validHash}}},
		{Revision: "../escape", Assets: []Asset{{Path: "x", Size: 1, SHA256: validHash}}},
		{Revision: "valid", Assets: []Asset{{Path: "../escape", Size: 1, SHA256: validHash}}},
		{Revision: "valid", Assets: []Asset{{Path: `..\escape`, Size: 1, SHA256: validHash}}},
		{Revision: "valid", Assets: []Asset{{Path: "x", Size: 0, SHA256: validHash}}},
		{Revision: "valid", Assets: []Asset{{Path: "x", Size: 1, SHA256: "bad"}}},
	} {
		if _, err := NewManager(context.Background(), Config{
			RootDirectory: t.TempDir(), BaseURL: "https://models.example/", Manifest: manifest,
		}); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("manifest %#v error=%v", manifest, err)
		}
	}
}

func manifestForFiles(revision string, files map[string][]byte) Manifest {
	manifest := Manifest{Revision: revision, Assets: make([]Asset, 0, len(files))}
	for name, body := range files {
		sum := sha256.Sum256(body)
		manifest.Assets = append(manifest.Assets, Asset{
			Path: name, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]),
		})
	}
	return manifest
}

func manifestSize(manifest Manifest) int64 {
	var total int64
	for _, asset := range manifest.Assets {
		total += asset.Size
	}
	return total
}

func waitForStatus(t *testing.T, manager *Manager, want Status) Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := manager.Status()
		if status.Status == want {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("status=%#v, want %s", manager.Status(), want)
	return Snapshot{}
}

func (status Status) String() string {
	return string(status)
}

type cancelAfterRead struct {
	cancel context.CancelFunc
	read   bool
}

func (reader *cancelAfterRead) Read(buffer []byte) (int, error) {
	if reader.read {
		return 0, io.EOF
	}
	reader.read = true
	read := copy(buffer, "first verified chunk")
	reader.cancel()
	return read, nil
}
