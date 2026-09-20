package privacymodel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/QuantumNous/astrlink/core/contract"
)

const resumePlanName = ".download-plan.json"

// Pause waits for the writer to stop before returning the checkpoint.
// If publication won the race, the completed installation is returned instead.
func (registry *Registry) PauseInstallation(ctx context.Context, id contract.PrivacyModelID) (contract.PrivacyModelInstallation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	registry.mu.Lock()
	installation, exists := registry.installations[id]
	if !exists {
		registry.mu.Unlock()
		return contract.PrivacyModelInstallation{}, ErrNotFound
	}
	if _, deleting := registry.deleting[id]; deleting {
		registry.mu.Unlock()
		return contract.PrivacyModelInstallation{}, ErrBusy
	}
	if installation.Source == contract.PrivacyModelSourceLocal {
		registry.mu.Unlock()
		return contract.PrivacyModelInstallation{}, ErrInvalidConfig
	}
	operation := registry.operations[id]
	if operation != nil {
		operation.cancel()
	}
	registry.mu.Unlock()
	if operation != nil {
		select {
		case <-operation.done:
		case <-ctx.Done():
			return contract.PrivacyModelInstallation{}, ctx.Err()
		}
	}
	return registry.GetInstallation(id)
}

func (registry *Registry) ResumeInstallation(ctx context.Context, id contract.PrivacyModelID) (contract.PrivacyModelInstallation, error) {
	installation, err := registry.GetInstallation(id)
	if err != nil {
		return contract.PrivacyModelInstallation{}, err
	}
	if installation.Source == contract.PrivacyModelSourceLocal {
		return contract.PrivacyModelInstallation{}, ErrInvalidConfig
	}
	return registry.Install(ctx, contract.PrivacyModelInstallRequest{
		RepoID: installation.RepoID, Revision: installation.Revision,
		VariantID: installation.VariantID, LabelMapping: installation.LabelMapping,
	})
}

func (registry *Registry) resumeDirectory(id contract.PrivacyModelID) string {
	return filepath.Join(registry.stagingDir, ".privacy-model-download-"+string(id)+"-resume")
}

// Bind cached bytes to the exact asset plan. A changed plan starts afresh.
func (registry *Registry) prepareResume(plan installationPlan) (int64, error) {
	directory := registry.resumeDirectory(plan.installation.ID)
	document, err := json.Marshal(plan.assets)
	if err != nil {
		return 0, ErrInvalidConfig
	}
	var existing []byte
	readErr := safeResumeTree(directory)
	if readErr == nil {
		existing, readErr = readStagedJSONAsset(directory, resumePlanName, 256<<10)
	}
	if readErr != nil || !bytes.Equal(existing, document) {
		if os.RemoveAll(directory) != nil || os.MkdirAll(directory, 0o700) != nil {
			return 0, ErrFilesystem
		}
		if os.WriteFile(filepath.Join(directory, resumePlanName), document, 0o600) != nil {
			return 0, ErrFilesystem
		}
	}
	// A pause during final verification can leave the old generated manifest.
	// It must be rebuilt, especially when the label mapping changed on retry.
	if err := os.Remove(filepath.Join(directory, InstallationManifestName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, ErrFilesystem
	}
	return stagedProgress(directory, plan.assets, plan.installation.BytesTotal), nil
}

func safeResumeTree(directory string) error {
	return filepath.WalkDir(directory, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return ErrFilesystem
		}
		return nil
	})
}

func (registry *Registry) resumeProgress(installation contract.PrivacyModelInstallation) int64 {
	directory := registry.resumeDirectory(installation.ID)
	if safeResumeTree(directory) != nil {
		return 0
	}
	document, err := readStagedJSONAsset(directory, resumePlanName, 256<<10)
	var assets []Asset
	if err != nil || json.Unmarshal(document, &assets) != nil {
		return 0
	}
	return stagedProgress(directory, assets, installation.BytesTotal)
}

func stagedProgress(directory string, assets []Asset, total int64) int64 {
	var progress int64
	seen := make(map[string]bool)
	for _, asset := range assets {
		if !safeAssetPath(asset.Path) || asset.Size <= 0 || seen[asset.Path] {
			return 0
		}
		seen[asset.Path] = true
		info, err := os.Lstat(filepath.Join(directory, filepath.FromSlash(asset.Path)))
		if err == nil && info.Mode().IsRegular() && info.Size() <= asset.Size {
			if info.Size() > total-progress {
				return 0
			}
			progress += info.Size()
		}
	}
	return progress
}

func (registry *Registry) downloadAssetOnce(ctx context.Context, id contract.PrivacyModelID, temporary string, installation contract.PrivacyModelInstallation, asset Asset) (Asset, error) {
	if !safeAssetPath(asset.Path) || asset.Size <= 0 {
		return Asset{}, errModelIncompatible
	}
	destination := filepath.Join(temporary, filepath.FromSlash(asset.Path))
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return Asset{}, ErrFilesystem
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return Asset{}, ErrFilesystem
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return Asset{}, ErrFilesystem
	}
	offset := info.Size()
	// All reusable bytes are already included in the installation's progress.
	reset := func() error {
		if err := file.Truncate(0); err != nil {
			return ErrFilesystem
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return ErrFilesystem
		}
		registry.addProgress(id, -min(offset, asset.Size))
		offset = 0
		return nil
	}
	if offset > asset.Size {
		// Oversized files were not counted by stagedProgress.
		offset = 0
		if err := reset(); err != nil {
			return Asset{}, err
		}
	}
	hasher := sha256.New()
	if _, err := copyRegistryAsset(ctx, io.LimitReader(file, offset), hasher, func(int64) {}); err != nil {
		return Asset{}, err
	}
	if offset == asset.Size {
		digest := hex.EncodeToString(hasher.Sum(nil))
		if asset.SHA256 == "" || asset.SHA256 == digest {
			asset.SHA256 = digest
			if err := file.Close(); err != nil {
				return Asset{}, ErrFilesystem
			}
			return asset, nil
		}
		if err := reset(); err != nil {
			return Asset{}, err
		}
		hasher.Reset()
	}
	endpoint := registry.probe.endpoint(installation.RepoID, "resolve", installation.Revision, asset.Path)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Asset{}, ErrInvalidConfig
	}
	request.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := registry.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return Asset{}, ctx.Err()
		}
		return Asset{}, &assetDownloadFailure{reason: networkFailureReason(err), retryable: true, cause: ErrRemoteMetadata}
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		// Range is optional on servers. Never append a full response to a prefix.
		if offset > 0 {
			if err := reset(); err != nil {
				return Asset{}, err
			}
			hasher.Reset()
		}
	case http.StatusPartialContent:
		expected := fmt.Sprintf("bytes %d-%d/%d", offset, asset.Size-1, asset.Size)
		if response.Header.Get("Content-Range") != expected {
			return Asset{}, &assetDownloadFailure{reason: "content_range", cause: ErrRemoteMetadata}
		}
	default:
		return Asset{}, &assetDownloadFailure{reason: fmt.Sprintf("http_%d", response.StatusCode), retryable: retryableHTTPStatus(response.StatusCode), cause: ErrRemoteMetadata}
	}
	remaining := asset.Size - offset
	if response.ContentLength >= 0 && response.ContentLength != remaining {
		return Asset{}, &assetDownloadFailure{reason: "content_length", retryable: true, cause: ErrRemoteMetadata}
	}
	written, copyErr := copyRegistryAsset(ctx, io.LimitReader(response.Body, remaining+1), io.MultiWriter(file, hasher), func(delta int64) {
		reported := min(delta, asset.Size-offset)
		offset += reported
		registry.addProgress(id, reported)
	})
	bodyCloseErr := response.Body.Close()
	syncErr := file.Sync()
	if syncErr != nil || errors.Is(copyErr, io.ErrShortWrite) || written > remaining {
		if err := reset(); err != nil {
			return Asset{}, err
		}
		if written > remaining {
			return Asset{}, &assetDownloadFailure{reason: "body_read", retryable: true, cause: ErrRemoteMetadata}
		}
		return Asset{}, ErrFilesystem
	}
	if ctx.Err() != nil {
		return Asset{}, ctx.Err()
	}
	if copyErr != nil || bodyCloseErr != nil || written != remaining {
		// A transport interruption keeps the prefix for this or a future attempt.
		return Asset{}, &assetDownloadFailure{reason: "body_read", retryable: true, cause: ErrRemoteMetadata}
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if asset.SHA256 != "" && digest != asset.SHA256 {
		if err := reset(); err != nil {
			return Asset{}, err
		}
		return Asset{}, &assetDownloadFailure{reason: "sha256", cause: errAssetIntegrity}
	}
	asset.SHA256 = digest
	if err := file.Close(); err != nil {
		return Asset{}, ErrFilesystem
	}
	return asset, nil
}
