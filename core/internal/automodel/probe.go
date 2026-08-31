package automodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autotaxonomy"
)

const (
	maxConfigBytes        = 1 << 20
	maxOnnxManifestBytes  = 1 << 20
	maxLocalProbeParents  = 8
	requiredModelName     = "model.onnx"
	requiredTokenizerName = "tokenizer.json"
	requiredConfigName    = "config.json"
	bundleManifestName    = "onnx-manifest.json"
)

type modelConfig struct {
	Architectures []string          `json:"architectures"`
	ID2Label      map[string]string `json:"id2label"`
}

type bundleManifest struct {
	ArtifactTier string `json:"artifact_tier"`
	Source       struct {
		ArtifactTier   string `json:"artifact_tier"`
		ReleaseMode    string `json:"release_mode"`
		TaxonomySHA256 string `json:"taxonomy_sha256"`
	} `json:"source"`
	Contract struct {
		ID2Label map[string]string `json:"id2label"`
	} `json:"contract"`
	Files map[string]struct {
		SHA256    string `json:"sha256"`
		SizeBytes int64  `json:"size_bytes"`
	} `json:"files"`
}

func (registry *Registry) ProbeLocal(
	ctx context.Context,
	request contract.AutoClassifierLocalProbeRequest,
) (contract.AutoClassifierProbeResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contract.ValidateAutoClassifierLocalProbeRequest(request); err != nil {
		return contract.AutoClassifierProbeResponse{}, ErrInvalidConfig
	}
	directory, err := canonicalLocalDirectory(request.Path)
	if err != nil {
		return contract.AutoClassifierProbeResponse{}, err
	}
	root, err := filepath.EvalSymlinks(registry.root)
	if err != nil {
		return contract.AutoClassifierProbeResponse{}, ErrInvalidConfig
	}
	if localPathsOverlap(directory, filepath.Clean(root)) {
		return contract.AutoClassifierProbeResponse{}, ErrInvalidConfig
	}
	entry, err := inspectLocalClassifier(ctx, directory)
	if err != nil {
		return contract.AutoClassifierProbeResponse{}, err
	}
	registry.cacheProbe(entry)
	return entry.response, nil
}

func canonicalLocalDirectory(candidate string) (string, error) {
	candidate = filepath.Clean(candidate)
	info, err := os.Lstat(candidate)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrLocalSource
	}
	canonical, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", ErrLocalSource
	}
	canonical = filepath.Clean(canonical)
	canonicalInfo, err := os.Lstat(canonical)
	if err != nil || canonicalInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(info, canonicalInfo) {
		return "", ErrLocalSource
	}
	if canonicalInfo.IsDir() {
		return canonical, nil
	}
	if !canonicalInfo.Mode().IsRegular() ||
		!strings.EqualFold(filepath.Ext(canonical), ".onnx") {
		return "", ErrUnsupportedModel
	}
	return findPackageRoot(filepath.Dir(canonical))
}

func findPackageRoot(start string) (string, error) {
	directory := filepath.Clean(start)
	for depth := 0; depth < maxLocalProbeParents; depth++ {
		configExists, err := localPathExists(filepath.Join(directory, requiredConfigName))
		if err != nil {
			return "", err
		}
		tokenizerExists, err := localPathExists(filepath.Join(directory, requiredTokenizerName))
		if err != nil {
			return "", err
		}
		if configExists || tokenizerExists {
			if !configExists || !tokenizerExists {
				return "", ErrUnsupportedModel
			}
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", ErrUnsupportedModel
		}
		directory = parent
	}
	return "", ErrUnsupportedModel
}

func localPathExists(candidate string) (bool, error) {
	_, err := os.Lstat(candidate)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, ErrLocalSource
}

func inspectLocalClassifier(
	ctx context.Context,
	directory string,
) (cachedProbe, error) {
	rootInfo, err := os.Lstat(directory)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return cachedProbe{}, ErrLocalSource
	}
	required := []string{requiredModelName, requiredTokenizerName, requiredConfigName}
	assets := make([]Asset, 0, 8)
	for _, relative := range required {
		if err := ctx.Err(); err != nil {
			return cachedProbe{}, err
		}
		asset, hashErr := hashLocalAsset(ctx, directory, relative, 0)
		if hashErr != nil {
			if hashErr == ErrLocalSource {
				return cachedProbe{}, ErrUnsupportedModel
			}
			return cachedProbe{}, hashErr
		}
		assets = append(assets, asset)
	}
	optional := []string{bundleManifestName, "tokenizer_config.json", "special_tokens_map.json"}
	var bundle *bundleManifest
	for _, relative := range optional {
		exists, existsErr := localPathExists(filepath.Join(directory, relative))
		if existsErr != nil {
			return cachedProbe{}, existsErr
		}
		if !exists {
			continue
		}
		asset, hashErr := hashLocalAsset(ctx, directory, relative, 0)
		if hashErr != nil {
			return cachedProbe{}, hashErr
		}
		assets = append(assets, asset)
		if relative == bundleManifestName {
			document, readErr := readLocalAsset(ctx, directory, relative, maxOnnxManifestBytes)
			if readErr != nil {
				return cachedProbe{}, readErr
			}
			parsed, parseErr := parseBundleManifest(document)
			if parseErr != nil {
				return cachedProbe{}, parseErr
			}
			bundle = &parsed
		}
	}
	configDocument, err := readLocalAsset(ctx, directory, requiredConfigName, maxConfigBytes)
	if err != nil {
		return cachedProbe{}, err
	}
	var config modelConfig
	if json.Unmarshal(configDocument, &config) != nil ||
		!hasSequenceClassificationArchitecture(config.Architectures) {
		return cachedProbe{}, ErrUnsupportedModel
	}
	if err := autotaxonomy.EqualID2Label(config.ID2Label); err != nil {
		return cachedProbe{}, ErrUnsupportedModel
	}
	artifactTier := contract.AutoClassifierArtifactExperimental
	releaseMode := "experimental"
	if bundle != nil {
		if err := crossCheckBundle(bundle, assets); err != nil {
			return cachedProbe{}, err
		}
		if bundle.Source.TaxonomySHA256 != "" &&
			bundle.Source.TaxonomySHA256 != autotaxonomy.SHA256 {
			return cachedProbe{}, ErrUnsupportedModel
		}
		if bundle.Contract.ID2Label != nil {
			if err := autotaxonomy.EqualID2Label(bundle.Contract.ID2Label); err != nil {
				return cachedProbe{}, ErrUnsupportedModel
			}
		}
		if tier := firstNonEmpty(bundle.ArtifactTier, bundle.Source.ArtifactTier); tier != "" {
			if !contract.AutoClassifierArtifactTier(tier).Valid() {
				return cachedProbe{}, ErrUnsupportedModel
			}
			artifactTier = contract.AutoClassifierArtifactTier(tier)
		}
		if bundle.Source.ReleaseMode != "" {
			releaseMode = bundle.Source.ReleaseMode
		}
	}
	sort.Slice(assets, func(left, right int) bool {
		return assets[left].Path < assets[right].Path
	})
	revision, err := localRevision(assets, artifactTier)
	if err != nil {
		return cachedProbe{}, ErrUnsupportedModel
	}
	bytesTotal := int64(0)
	for _, asset := range assets {
		bytesTotal += asset.Size
	}
	response := contract.AutoClassifierProbeResponse{
		RepoID:             localRepoID(revision),
		Revision:           revision,
		Name:               "Local classifier · " + revision[:8],
		ArtifactTier:       artifactTier,
		EligibleForRouting: contract.EligibleForRouting(artifactTier),
		TaxonomyID:         autotaxonomy.ID,
		TaxonomySHA256:     autotaxonomy.SHA256,
		BytesTotal:         bytesTotal,
		ID2Label:           append([]string(nil), autotaxonomy.Labels...),
	}
	if err := response.Validate(); err != nil {
		return cachedProbe{}, ErrUnsupportedModel
	}
	return cachedProbe{
		directory:    directory,
		response:     response,
		assets:       assets,
		modelPath:    requiredModelName,
		tokenizer:    requiredTokenizerName,
		config:       requiredConfigName,
		artifactTier: artifactTier,
		releaseMode:  releaseMode,
	}, nil
}

func parseBundleManifest(document []byte) (bundleManifest, error) {
	var manifest bundleManifest
	if json.Unmarshal(document, &manifest) != nil {
		return bundleManifest{}, ErrUnsupportedModel
	}
	return manifest, nil
}

func crossCheckBundle(manifest *bundleManifest, assets []Asset) error {
	if len(manifest.Files) == 0 {
		return nil
	}
	byPath := map[string]Asset{}
	for _, asset := range assets {
		byPath[asset.Path] = asset
	}
	for _, required := range []string{requiredModelName, requiredTokenizerName, requiredConfigName} {
		declared, exists := manifest.Files[required]
		if !exists {
			continue
		}
		actual, hashed := byPath[required]
		if !hashed ||
			(declared.SHA256 != "" && !strings.EqualFold(declared.SHA256, actual.SHA256)) ||
			(declared.SizeBytes > 0 && declared.SizeBytes != actual.Size) {
			return ErrUnsupportedModel
		}
	}
	return nil
}

func localRevision(assets []Asset, tier contract.AutoClassifierArtifactTier) (string, error) {
	type fingerprint struct {
		Version int     `json:"version"`
		Tier    string  `json:"artifact_tier"`
		Assets  []Asset `json:"assets"`
	}
	document, err := json.Marshal(fingerprint{
		Version: 1,
		Tier:    string(tier),
		Assets:  assets,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:20]), nil
}

func hasSequenceClassificationArchitecture(architectures []string) bool {
	if len(architectures) == 0 {
		return false
	}
	found := false
	for _, architecture := range architectures {
		if strings.Contains(architecture, "TokenClassification") {
			return false
		}
		if strings.HasSuffix(architecture, "ForSequenceClassification") {
			found = true
		}
	}
	return found
}

func hashLocalAsset(
	ctx context.Context,
	root string,
	relative string,
	expectedSize int64,
) (Asset, error) {
	file, before, path, err := openLocalAsset(root, relative, expectedSize)
	if err != nil {
		return Asset{}, err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(hasher, io.LimitReader(file, before.Size()+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	pathAfter, pathErr := os.Lstat(path)
	if copyErr != nil {
		if ctx.Err() != nil {
			return Asset{}, ctx.Err()
		}
		return Asset{}, ErrLocalSource
	}
	if statErr != nil || closeErr != nil || pathErr != nil ||
		written != before.Size() ||
		!sameLocalFileSnapshot(before, after) ||
		!sameLocalFileSnapshot(before, pathAfter) {
		return Asset{}, ErrLocalSource
	}
	return Asset{
		Path:   relative,
		Size:   before.Size(),
		SHA256: hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func readLocalAsset(
	ctx context.Context,
	root string,
	relative string,
	limit int64,
) ([]byte, error) {
	file, before, path, err := openLocalAsset(root, relative, 0)
	if err != nil {
		return nil, err
	}
	if before.Size() > limit {
		_ = file.Close()
		return nil, ErrUnsupportedModel
	}
	document, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	pathAfter, pathErr := os.Lstat(path)
	if readErr != nil || statErr != nil || closeErr != nil || pathErr != nil ||
		len(document) == 0 || int64(len(document)) != before.Size() ||
		!sameLocalFileSnapshot(before, after) ||
		!sameLocalFileSnapshot(before, pathAfter) {
		return nil, ErrLocalSource
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return document, nil
}

func openLocalAsset(
	root string,
	relative string,
	expectedSize int64,
) (*os.File, os.FileInfo, string, error) {
	if !safeAssetPath(relative) {
		return nil, nil, "", ErrUnsupportedModel
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, "", ErrLocalSource
	}
	current := root
	segments := strings.Split(relative, "/")
	var info os.FileInfo
	for index, segment := range segments {
		current = filepath.Join(current, filepath.FromSlash(segment))
		info, err = os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, "", ErrLocalSource
		}
		if index < len(segments)-1 {
			if !info.IsDir() {
				return nil, nil, "", ErrLocalSource
			}
			continue
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 ||
			(expectedSize > 0 && info.Size() != expectedSize) {
			return nil, nil, "", ErrLocalSource
		}
	}
	file, err := os.Open(current)
	if err != nil {
		return nil, nil, "", ErrLocalSource
	}
	opened, err := file.Stat()
	if err != nil || !sameLocalFileSnapshot(info, opened) {
		_ = file.Close()
		return nil, nil, "", ErrLocalSource
	}
	return file, info, current, nil
}

func sameLocalFileSnapshot(left, right os.FileInfo) bool {
	return left != nil && right != nil &&
		left.Mode().IsRegular() && right.Mode().IsRegular() &&
		left.Mode()&os.ModeSymlink == 0 && right.Mode()&os.ModeSymlink == 0 &&
		os.SameFile(left, right) &&
		left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}

func localPathsOverlap(left, right string) bool {
	return localPathContains(left, right) || localPathContains(right, left)
}

func localPathContains(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
