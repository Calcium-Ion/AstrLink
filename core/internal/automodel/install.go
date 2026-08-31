package automodel

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autotaxonomy"
)

func (registry *Registry) Install(
	ctx context.Context,
	request contract.AutoClassifierInstallRequest,
) (contract.AutoClassifierInstallation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contract.ValidateAutoClassifierInstallRequest(request); err != nil {
		return contract.AutoClassifierInstallation{}, ErrInvalidConfig
	}
	directory, err := canonicalLocalDirectory(request.Path)
	if err != nil {
		return contract.AutoClassifierInstallation{}, err
	}
	entry, cached := registry.takeCachedProbe(directory)
	if !cached {
		return contract.AutoClassifierInstallation{}, ErrLocalProbeRequired
	}
	id := InstallationID(entry.response.RepoID, entry.response.Revision)
	if _, ready := registry.ReadyInstallation(id); ready {
		return contract.AutoClassifierInstallation{}, ErrAlreadyInstalled
	}
	items := registry.ListInstallations()
	if len(items) >= maxInstallations {
		return contract.AutoClassifierInstallation{}, ErrCapacity
	}
	release, err := registry.withBusy()
	if err != nil {
		return contract.AutoClassifierInstallation{}, err
	}
	defer release()

	staging := filepath.Join(registry.root, stagingDirName, string(id))
	destination := filepath.Join(registry.root, installationsDirName, string(id))
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return contract.AutoClassifierInstallation{}, err
	}
	defer func() { _ = os.RemoveAll(staging) }()

	copied := make([]installationFile, 0, len(entry.assets))
	for _, asset := range entry.assets {
		if err := ctx.Err(); err != nil {
			return contract.AutoClassifierInstallation{}, err
		}
		if err := copyLocalAsset(ctx, directory, staging, asset); err != nil {
			return contract.AutoClassifierInstallation{}, err
		}
		copied = append(copied, installationFile{
			Path:   asset.Path,
			Size:   asset.Size,
			SHA256: asset.SHA256,
		})
	}
	id2label := map[string]string{}
	for index, label := range autotaxonomy.Labels {
		id2label[itoa(index)] = label
	}
	manifest := installationManifest{
		Version:        1,
		InstallationID: string(id),
		Identity:       entry.response.RepoID + "@" + entry.response.Revision,
		TaxonomyID:     autotaxonomy.ID,
		TaxonomySHA256: autotaxonomy.SHA256,
		Preprocessing: preprocessing{
			Text:   autotaxonomy.TextPreprocessing,
			Tokens: autotaxonomy.TokenPreprocessing,
		},
		ArtifactTier:      string(entry.artifactTier),
		ReleaseMode:       entry.releaseMode,
		ModelPath:         entry.modelPath,
		TokenizerPath:     entry.tokenizer,
		ConfigPath:        entry.config,
		MaxSequenceTokens: 512,
		ContentBudget:     510,
		HeadTokens:        255,
		TailTokens:        255,
		PadTokenID:        0,
		PadMultiple:       8,
		AddSpecialTokens:  false,
		InputNames: inputNames{
			InputIDs:      "input_ids",
			AttentionMask: "attention_mask",
		},
		OutputName: "logits",
		ID2Label:   id2label,
		Files:      copied,
	}
	if err := manifest.validate(); err != nil {
		return contract.AutoClassifierInstallation{}, ErrUnsupportedModel
	}
	document, err := json.Marshal(manifest)
	if err != nil {
		return contract.AutoClassifierInstallation{}, err
	}
	if err := os.WriteFile(filepath.Join(staging, manifestName), document, 0o600); err != nil {
		return contract.AutoClassifierInstallation{}, err
	}
	if err := os.RemoveAll(destination); err != nil {
		return contract.AutoClassifierInstallation{}, err
	}
	if err := os.Rename(staging, destination); err != nil {
		return contract.AutoClassifierInstallation{}, err
	}
	installation, err := registry.GetInstallation(id)
	if err != nil {
		return contract.AutoClassifierInstallation{}, err
	}
	return installation, nil
}

func copyLocalAsset(
	ctx context.Context,
	sourceRoot string,
	destinationRoot string,
	asset Asset,
) error {
	file, before, path, err := openLocalAsset(sourceRoot, asset.Path, asset.Size)
	if err != nil {
		return err
	}
	defer file.Close()
	target := filepath.Join(destinationRoot, filepath.FromSlash(asset.Path))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(output, io.LimitReader(file, asset.Size+1))
	closeErr := output.Close()
	after, statErr := file.Stat()
	pathAfter, pathErr := os.Lstat(path)
	if copyErr != nil || closeErr != nil || statErr != nil || pathErr != nil ||
		written != asset.Size ||
		!sameLocalFileSnapshot(before, after) ||
		!sameLocalFileSnapshot(before, pathAfter) {
		_ = os.Remove(target)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrLocalSource
	}
	return nil
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
