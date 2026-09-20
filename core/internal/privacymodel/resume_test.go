package privacymodel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

type interruptedAssetBody struct {
	ctx     context.Context
	prefix  *bytes.Reader
	blocked chan struct{}
}

func (body *interruptedAssetBody) Read(buffer []byte) (int, error) {
	if body.prefix.Len() > 0 {
		return body.prefix.Read(buffer)
	}
	select {
	case <-body.blocked:
	default:
		close(body.blocked)
	}
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}
func (*interruptedAssetBody) Close() error { return nil }

func TestRegistryPauseAndResumeAcrossRestart(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprintf("shutdown=%t", shutdown), func(t *testing.T) {
			repository := newFakeHFRepository(t)
			repository.requestedRevision = repository.revision
			store := openRegistryStore(t)
			root := filepath.Join(t.TempDir(), "models")
			lifetime, cancel := context.WithCancel(context.Background())
			defer cancel()
			registry, err := NewRegistry(lifetime, RegistryConfig{RootDirectory: root, Store: store, MetadataBaseURL: repository.server.URL, HTTPClient: repository.server.Client(), TestOnlyLoopbackMode: true})
			if err != nil {
				t.Fatal(err)
			}
			document := repository.assets["model_int8.onnx"]
			prefixSize := len(document) / 2
			blocked := make(chan struct{})
			var attempts atomic.Int64
			client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if !strings.HasSuffix(request.URL.Path, "/model_int8.onnx") {
					return http.DefaultTransport.RoundTrip(request)
				}
				response := &http.Response{Header: make(http.Header), Request: request, ContentLength: int64(len(document))}
				if attempts.Add(1) == 1 {
					response.StatusCode = http.StatusOK
					response.Body = &interruptedAssetBody{ctx: request.Context(), prefix: bytes.NewReader(document[:prefixSize]), blocked: blocked}
				} else {
					if got := request.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-", prefixSize) {
						t.Errorf("Range=%q", got)
					}
					response.StatusCode = http.StatusPartialContent
					response.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", prefixSize, len(document)-1, len(document)))
					response.ContentLength = int64(len(document) - prefixSize)
					response.Body = io.NopCloser(bytes.NewReader(document[prefixSize:]))
				}
				return response, nil
			})}
			registry.httpClient = client
			started, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{RepoID: repository.repoID, Revision: repository.revision, VariantID: "cpu_int8", LabelMapping: emailMapping()})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-blocked:
			case <-time.After(3 * time.Second):
				t.Fatal("download never blocked")
			}
			if shutdown {
				cancel()
			}
			paused, err := registry.PauseInstallation(context.Background(), started.ID)
			if err != nil || paused.Status != contract.PrivacyModelStatusPaused || paused.Error != nil || paused.BytesDownloaded < int64(prefixSize) {
				t.Fatalf("paused=%#v err=%v", paused, err)
			}
			if err := contract.ValidatePrivacyModelInstallation(paused); err != nil {
				t.Fatal(err)
			}
			record, err := store.GetPrivacyModelInstallation(context.Background(), started.ID)
			if err != nil || record.Installation.Status != contract.PrivacyModelStatusPaused {
				t.Fatalf("record=%#v err=%v", record, err)
			}
			// Simulate a crash before the final progress write, and a week-old pause.
			record.Installation.Status = contract.PrivacyModelStatusDownloading
			record.Installation.BytesDownloaded = 0
			if err := store.PutPrivacyModelInstallation(context.Background(), record); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-7 * 24 * time.Hour)
			if err := filepath.Walk(registry.resumeDirectory(started.ID), func(path string, _ os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				return os.Chtimes(path, old, old)
			}); err != nil {
				t.Fatal(err)
			}
			reopened := newTestRegistry(t, root, repository, store)
			reopened.httpClient = client
			recovered, err := reopened.GetInstallation(started.ID)
			if err != nil || recovered.Status != contract.PrivacyModelStatusPaused || recovered.BytesDownloaded != paused.BytesDownloaded {
				t.Fatalf("recovered=%#v err=%v", recovered, err)
			}
			resumed, err := reopened.ResumeInstallation(context.Background(), started.ID)
			if err != nil || resumed.BytesDownloaded != paused.BytesDownloaded {
				t.Fatalf("resumed=%#v err=%v", resumed, err)
			}
			ready := waitForInstallation(t, reopened, started.ID)
			if ready.Status != contract.PrivacyModelStatusReady || attempts.Load() != 2 {
				t.Fatalf("ready=%#v attempts=%d", ready, attempts.Load())
			}
			if _, err := os.Stat(reopened.resumeDirectory(started.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("staging survived: %v", err)
			}
		})
	}
}

func TestResumeAssetValidatesRangesAndHashes(t *testing.T) {
	for _, scenario := range []string{"range", "ignored", "wrong-start", "wrong-total", "short-body", "corrupt", "complete", "corrupt-complete"} {
		t.Run(scenario, func(t *testing.T) {
			repository := newFakeHFRepository(t)
			registry := newTestRegistry(t, filepath.Join(t.TempDir(), "models"), repository, nil)
			document := []byte("0123456789abcdefghij")
			asset := Asset{Path: "model.onnx", Size: int64(len(document)), SHA256: testSHA256(document)}
			prefix := document[:7]
			if scenario == "complete" || scenario == "corrupt-complete" {
				prefix = append([]byte(nil), document...)
			}
			if scenario == "corrupt-complete" {
				prefix[0] = 'X'
			}
			id := InstallationID(repository.repoID, repository.revision, "cpu_int8")
			installation := contract.PrivacyModelInstallation{ID: id, RepoID: repository.repoID, Revision: repository.revision, Status: contract.PrivacyModelStatusDownloading, BytesDownloaded: int64(len(prefix)), BytesTotal: asset.Size}
			registry.installations[id] = installation
			directory := t.TempDir()
			destination := filepath.Join(directory, asset.Path)
			if err := os.WriteFile(destination, prefix, 0o600); err != nil {
				t.Fatal(err)
			}
			registry.httpClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if scenario == "complete" {
					t.Error("completed asset downloaded again")
				}
				offset := len(prefix)
				if scenario == "corrupt-complete" {
					offset = 0
				}
				wantRange := fmt.Sprintf("bytes=%d-", offset)
				if offset == 0 {
					wantRange = ""
				}
				if request.Header.Get("Range") != wantRange {
					t.Errorf("Range=%q want %q", request.Header.Get("Range"), wantRange)
				}
				response := &http.Response{StatusCode: http.StatusPartialContent, Header: make(http.Header)}
				response.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(document)-1, len(document)))
				body := append([]byte(nil), document[offset:]...)
				switch scenario {
				case "ignored", "corrupt-complete":
					response.StatusCode = http.StatusOK
					body = document
				case "wrong-start":
					response.Header.Set("Content-Range", "bytes 0-19/20")
				case "wrong-total":
					response.Header.Set("Content-Range", "bytes 7-19/21")
				case "corrupt":
					body[0] = 'X'
				}
				response.ContentLength = int64(len(body))
				if scenario == "short-body" {
					body = body[:3]
				}
				response.Body = io.NopCloser(bytes.NewReader(body))
				return response, nil
			})}
			_, err := registry.downloadAssetOnce(context.Background(), id, directory, installation, asset)
			data, readErr := os.ReadFile(destination)
			if readErr != nil {
				t.Fatal(readErr)
			}
			current, _ := registry.GetInstallation(id)
			switch scenario {
			case "wrong-start", "wrong-total":
				if err == nil || !bytes.Equal(data, prefix) || current.BytesDownloaded != int64(len(prefix)) {
					t.Fatalf("err=%v data=%q progress=%d", err, data, current.BytesDownloaded)
				}
			case "short-body":
				if err == nil || len(data) != 10 || current.BytesDownloaded != 10 {
					t.Fatalf("err=%v data=%q progress=%d", err, data, current.BytesDownloaded)
				}
			case "corrupt":
				if !errors.Is(err, errAssetIntegrity) || len(data) != 0 || current.BytesDownloaded != 0 {
					t.Fatalf("err=%v data=%q progress=%d", err, data, current.BytesDownloaded)
				}
			default:
				if err != nil || !bytes.Equal(data, document) || current.BytesDownloaded != asset.Size {
					t.Fatalf("err=%v data=%q progress=%d", err, data, current.BytesDownloaded)
				}
			}
		})
	}
}

func TestDeletePausedInstallationRemovesCheckpoint(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.requestedRevision = repository.revision
	repository.blockAsset = "model_int8.onnx"
	repository.blockStarted = make(chan struct{})
	registry := newTestRegistry(t, filepath.Join(t.TempDir(), "models"), repository, openRegistryStore(t))
	started, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{RepoID: repository.repoID, Revision: repository.revision, VariantID: "cpu_int8", LabelMapping: emailMapping()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-repository.blockStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	if _, err := registry.PauseInstallation(context.Background(), started.ID); err != nil {
		t.Fatal(err)
	}
	if err := registry.DeleteInstallation(context.Background(), started.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(registry.resumeDirectory(started.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkpoint survived: %v", err)
	}
	if _, err := registry.GetInstallation(started.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("record survived: %v", err)
	}
}

func TestResumeRebuildsManifestAfterInterruptedVerification(t *testing.T) {
	repository := newFakeHFRepository(t)
	repository.requestedRevision = repository.revision
	registry := newTestRegistry(t, filepath.Join(t.TempDir(), "models"), repository, nil)
	input := contract.PrivacyModelInstallRequest{RepoID: repository.repoID, Revision: repository.revision, VariantID: "cpu_int8", LabelMapping: emailMapping()}
	plan, err := registry.prepareInstallation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.prepareResume(plan); err != nil {
		t.Fatal(err)
	}
	directory := registry.resumeDirectory(plan.installation.ID)
	for _, asset := range plan.assets {
		destination := filepath.Join(directory, asset.Path)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, repository.assets[asset.Path], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, InstallationManifestName), []byte(`{"incomplete":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	started, err := registry.Install(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if started.BytesDownloaded != started.BytesTotal {
		t.Fatalf("progress lost: %#v", started)
	}
	if ready := waitForInstallation(t, registry, started.ID); ready.Status != contract.PrivacyModelStatusReady {
		t.Fatalf("ready=%#v", ready)
	}
}
