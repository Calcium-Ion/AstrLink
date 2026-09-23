package privacymodel

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
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	localProbeCacheLimit = 16
	localProbeCacheTTL   = 10 * time.Minute
	maxLocalProbeEntries = 4096
	maxLocalProbeParents = 8
	localRepoPrefix      = "local/model-"
)

type localProbeCacheEntry struct {
	directory string
	response  contract.PrivacyModelProbeResponse
	plans     map[string]customVariantPlan
	expiresAt time.Time
	timer     *time.Timer
}

type localProbeTarget struct {
	directory string
	modelPath string
}

type localFingerprint struct {
	Version  int                          `json:"version"`
	Adapter  contract.PrivacyModelAdapter `json:"adapter"`
	Variants []localFingerprintVariant    `json:"variants"`
}

type localFingerprintVariant struct {
	ID                    string                  `json:"id"`
	Name                  string                  `json:"name"`
	Quantization          string                  `json:"quantization"`
	EstimatedRAMBytes     int64                   `json:"estimated_ram_bytes"`
	ModelPath             string                  `json:"model_path"`
	ExternalData          []string                `json:"external_data_paths"`
	TokenizerPath         string                  `json:"tokenizer_path"`
	ConfigPath            string                  `json:"config_path"`
	CalibrationPath       *string                 `json:"calibration_path"`
	SecretRulesPath       *string                 `json:"secret_rules_path"`
	SecretCalibrationPath *string                 `json:"secret_calibration_path"`
	TagScheme             string                  `json:"tag_scheme"`
	Window                int                     `json:"window"`
	Stride                int                     `json:"stride"`
	MaxRequestTokens      int                     `json:"max_request_tokens"`
	InputNames            normalizedInputNames    `json:"input_names"`
	OutputName            string                  `json:"output_name"`
	Assets                []localFingerprintAsset `json:"assets"`
}

type localFingerprintAsset struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// IsLocalPrivacyModelRepoID reports whether repoID is in the namespace
// reserved for identities derived by a local path probe.
func IsLocalPrivacyModelRepoID(repoID string) bool {
	if len(repoID) != len(localRepoPrefix)+12 ||
		!strings.HasPrefix(repoID, localRepoPrefix) {
		return false
	}
	for _, character := range repoID[len(localRepoPrefix):] {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func isLocalPrivacyModelRepoID(repoID string) bool {
	return IsLocalPrivacyModelRepoID(repoID)
}

func (registry *Registry) ProbeLocal(
	ctx context.Context,
	request contract.PrivacyModelLocalProbeRequest,
) (contract.PrivacyModelProbeResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contract.ValidatePrivacyModelLocalProbeRequest(request); err != nil {
		return contract.PrivacyModelProbeResponse{}, ErrInvalidConfig
	}
	target, err := canonicalLocalProbeTarget(request.Path)
	if err != nil {
		return contract.PrivacyModelProbeResponse{}, err
	}
	registryRoot, err := filepath.EvalSymlinks(registry.rootDirectory)
	if err != nil {
		return contract.PrivacyModelProbeResponse{}, ErrInvalidConfig
	}
	if localPathsOverlap(target.directory, filepath.Clean(registryRoot)) {
		return contract.PrivacyModelProbeResponse{}, ErrInvalidConfig
	}
	var response contract.PrivacyModelProbeResponse
	var plans map[string]customVariantPlan
	if target.modelPath == "" {
		response, plans, err = inspectLocalModelDirectory(ctx, target.directory)
	} else {
		response, plans, err = inspectLocalModelFile(
			ctx,
			target.directory,
			target.modelPath,
		)
	}
	if err != nil {
		return contract.PrivacyModelProbeResponse{}, err
	}
	registry.cacheLocalProbe(target.directory, response, plans)
	return cloneProbeResponse(response), nil
}

func canonicalLocalProbeTarget(candidate string) (localProbeTarget, error) {
	candidate = filepath.Clean(candidate)
	info, err := os.Lstat(candidate)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return localProbeTarget{}, ErrLocalSource
	}
	canonical, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return localProbeTarget{}, ErrLocalSource
	}
	canonical = filepath.Clean(canonical)
	canonicalInfo, err := os.Lstat(canonical)
	if err != nil || canonicalInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(info, canonicalInfo) {
		return localProbeTarget{}, ErrLocalSource
	}
	if canonicalInfo.IsDir() {
		return localProbeTarget{directory: canonical}, nil
	}
	if !canonicalInfo.Mode().IsRegular() {
		return localProbeTarget{}, ErrLocalSource
	}
	if !strings.EqualFold(filepath.Ext(canonical), ".onnx") {
		return localProbeTarget{}, ErrUnsupportedModel
	}
	directory, err := findLocalModelPackageRoot(filepath.Dir(canonical))
	if err != nil {
		return localProbeTarget{}, err
	}
	modelPath, err := filepath.Rel(directory, canonical)
	if err != nil {
		return localProbeTarget{}, ErrLocalSource
	}
	modelPath = filepath.ToSlash(modelPath)
	if !safeAssetPath(modelPath) {
		return localProbeTarget{}, ErrUnsupportedModel
	}
	return localProbeTarget{directory: directory, modelPath: modelPath}, nil
}

func findLocalModelPackageRoot(start string) (string, error) {
	directory := filepath.Clean(start)
	for depth := 0; depth < maxLocalProbeParents; depth++ {
		configExists, err := localPathExists(filepath.Join(directory, "config.json"))
		if err != nil {
			return "", err
		}
		tokenizerExists, err := localPathExists(filepath.Join(directory, "tokenizer.json"))
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

func inspectLocalModelDirectory(
	ctx context.Context,
	directory string,
) (contract.PrivacyModelProbeResponse, map[string]customVariantPlan, error) {
	rootInfo, err := os.Lstat(directory)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return contract.PrivacyModelProbeResponse{}, nil, ErrLocalSource
	}
	regularFiles, modelPaths, err := scanLocalModelDirectory(ctx, directory)
	if err != nil {
		return contract.PrivacyModelProbeResponse{}, nil, err
	}
	if _, exists := regularFiles["config.json"]; !exists {
		return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
	}
	if _, exists := regularFiles["tokenizer.json"]; !exists {
		return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
	}
	if len(modelPaths) == 0 || len(modelPaths) > 32 {
		return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
	}

	required := map[string]struct{}{
		"config.json":    {},
		"tokenizer.json": {},
	}
	for _, modelPath := range modelPaths {
		required[modelPath] = struct{}{}
		for candidate := range regularFiles {
			suffix := strings.TrimPrefix(candidate, modelPath)
			if suffix != candidate && externalDataSuffixPattern.MatchString(suffix) {
				required[candidate] = struct{}{}
			}
		}
	}
	if err := addSensitiveGuardDirectoryAssets(required, regularFiles); err != nil {
		return contract.PrivacyModelProbeResponse{}, nil, err
	}
	return inspectLocalModelAssets(ctx, directory, modelPaths, required)
}

func inspectLocalModelFile(
	ctx context.Context,
	directory string,
	modelPath string,
) (contract.PrivacyModelProbeResponse, map[string]customVariantPlan, error) {
	if !safeAssetPath(modelPath) ||
		!strings.EqualFold(filepath.Ext(modelPath), ".onnx") {
		return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
	}
	required := map[string]struct{}{
		"config.json":    {},
		"tokenizer.json": {},
		modelPath:        {},
	}
	modelDirectory := filepath.Join(
		directory,
		filepath.Dir(filepath.FromSlash(modelPath)),
	)
	entries, err := os.ReadDir(modelDirectory)
	if err != nil {
		return contract.PrivacyModelProbeResponse{}, nil, ErrLocalSource
	}
	if len(entries) > maxLocalProbeEntries {
		return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return contract.PrivacyModelProbeResponse{}, nil, err
		}
		candidate := filepath.ToSlash(filepath.Join(
			filepath.Dir(filepath.FromSlash(modelPath)),
			entry.Name(),
		))
		suffix := strings.TrimPrefix(candidate, modelPath)
		if suffix == candidate || !externalDataSuffixPattern.MatchString(suffix) {
			continue
		}
		if !safeAssetPath(candidate) {
			return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
		}
		required[candidate] = struct{}{}
	}
	if err := addSensitiveGuardFileAssets(directory, required); err != nil {
		return contract.PrivacyModelProbeResponse{}, nil, err
	}
	return inspectLocalModelAssets(
		ctx,
		directory,
		[]string{modelPath},
		required,
	)
}

func inspectLocalModelAssets(
	ctx context.Context,
	directory string,
	modelPaths []string,
	required map[string]struct{},
) (contract.PrivacyModelProbeResponse, map[string]customVariantPlan, error) {
	requiredPaths := make([]string, 0, len(required))
	for candidate := range required {
		requiredPaths = append(requiredPaths, candidate)
	}
	sort.Strings(requiredPaths)
	siblings := make([]hfSibling, 0, len(requiredPaths))
	for _, candidate := range requiredPaths {
		if err := ctx.Err(); err != nil {
			return contract.PrivacyModelProbeResponse{}, nil, err
		}
		asset, err := hashLocalAsset(ctx, directory, candidate, 0)
		if err != nil {
			return contract.PrivacyModelProbeResponse{}, nil, err
		}
		sibling := hfSibling{Filename: asset.Path, Size: asset.Size}
		sibling.LFS = &struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		}{SHA256: asset.SHA256, Size: asset.Size}
		siblings = append(siblings, sibling)
	}

	configDocument, err := readLocalAsset(
		ctx,
		directory,
		"config.json",
		maxConfigBytes,
	)
	if err != nil {
		return contract.PrivacyModelProbeResponse{}, nil, err
	}
	tokenizerDocument, err := readLocalAsset(
		ctx,
		directory,
		"tokenizer.json",
		maxProbeJSONAssetBytes,
	)
	if err != nil {
		return contract.PrivacyModelProbeResponse{}, nil, err
	}
	var tokenizer map[string]json.RawMessage
	if json.Unmarshal(tokenizerDocument, &tokenizer) != nil || len(tokenizer) == 0 {
		return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
	}
	config, adapter, err := parsePrivacyModelConfig(configDocument)
	if err != nil ||
		prohibitedModelConfig(config) ||
		(adapter != contract.PrivacyModelAdapterPPLXBIOES && !hasTokenClassificationArchitecture(config.Architectures)) {
		return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
	}
	selectedModels := make(map[string]struct{}, len(modelPaths))
	for _, modelPath := range modelPaths {
		selectedModels[modelPath] = struct{}{}
	}
	for _, sibling := range siblings {
		if strings.HasSuffix(strings.ToLower(sibling.Filename), ".onnx") {
			if _, selected := selectedModels[sibling.Filename]; !selected {
				return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
			}
		}
	}
	labels, tagScheme, complete, validLabels := probeModelLabels(config.ID2Label, adapter)
	if !validLabels {
		return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
	}
	variants, plans := probeVariants(
		siblings,
		tagScheme,
		usesTokenTypeIDs(config),
	)
	if len(variants) == 0 || len(plans) == 0 {
		return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
	}
	name := ""
	var license *string
	languages := []string{}
	if adapter == contract.PrivacyModelAdapterPPLXBIOES {
		decoratePPLXPlans(variants, plans)
		name = "Perplexity PII-Tracer 0.6B"
		license = optionalNonEmpty("MIT")
		languages = []string{"en", "multilingual"}
	}
	if _, sensitive := required[sensitiveGuardSecretRulesPath]; sensitive {
		viterbiDocument, readErr := readLocalAsset(
			ctx, directory, sensitiveGuardViterbiCalibrationPath, maxConfigBytes,
		)
		if readErr != nil {
			return contract.PrivacyModelProbeResponse{}, nil, readErr
		}
		rulesDocument, readErr := readLocalAsset(
			ctx, directory, sensitiveGuardSecretRulesPath, maxConfigBytes,
		)
		if readErr != nil {
			return contract.PrivacyModelProbeResponse{}, nil, readErr
		}
		secretCalibrationDocument, readErr := readLocalAsset(
			ctx, directory, sensitiveGuardSecretCalibrationPath, maxConfigBytes,
		)
		if readErr != nil || validateSensitiveGuardAssets(
			config.ID2Label,
			viterbiDocument,
			rulesDocument,
			secretCalibrationDocument,
		) != nil {
			return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
		}
		if tagScheme != "bioes" ||
			!decorateSensitiveGuardPlans(&variants, plans, siblings) {
			return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
		}
		adapter = contract.PrivacyModelAdapterAstrLinkGuard
		name = "AstrLink Sensitive Data Guard 32M"
		license = optionalNonEmpty("Apache-2.0")
		languages = []string{"zh", "en"}
	}
	revision, err := localPlansRevision(plans, adapter)
	if err != nil {
		return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
	}
	if name == "" {
		name = "Local privacy model · " + revision[:8]
	}
	repoID := localRepoID(revision)
	response := contract.PrivacyModelProbeResponse{
		RepoID: repoID, RequestedRevision: revision, Revision: revision,
		Name: name, License: license, Languages: languages,
		Adapter:  adapter,
		Variants: variants, Labels: labels,
		RequiresLabelMapping: !complete,
	}
	if contract.ValidatePrivacyModelProbeResponse(response) != nil {
		return contract.PrivacyModelProbeResponse{}, nil, ErrUnsupportedModel
	}
	return response, plans, nil
}

func addSensitiveGuardDirectoryAssets(
	required map[string]struct{},
	files map[string]os.FileInfo,
) error {
	exists := func(path string) bool {
		_, present := files[path]
		return present
	}
	return addSensitiveGuardAssets(required, exists)
}

func addSensitiveGuardFileAssets(
	directory string,
	required map[string]struct{},
) error {
	present := make(map[string]bool)
	for _, path := range sensitiveGuardCandidateAssets() {
		exists, err := localPathExists(filepath.Join(
			directory,
			filepath.FromSlash(path),
		))
		if err != nil {
			return err
		}
		present[path] = exists
	}
	return addSensitiveGuardAssets(required, func(path string) bool {
		return present[path]
	})
}

func addSensitiveGuardAssets(
	required map[string]struct{},
	exists func(string) bool,
) error {
	marker := exists(sensitiveGuardSecretRulesPath) ||
		exists(sensitiveGuardSecretCalibrationPath)
	if !marker {
		return nil
	}
	for _, path := range []string{
		sensitiveGuardViterbiCalibrationPath,
		sensitiveGuardSecretRulesPath,
		sensitiveGuardSecretCalibrationPath,
	} {
		if !exists(path) {
			return ErrUnsupportedModel
		}
		required[path] = struct{}{}
	}
	for _, path := range []string{"NOTICE", "NOTICE-secret-rules.md"} {
		if exists(path) {
			required[path] = struct{}{}
		}
	}
	return nil
}

func sensitiveGuardCandidateAssets() []string {
	return []string{
		sensitiveGuardViterbiCalibrationPath,
		sensitiveGuardSecretRulesPath,
		sensitiveGuardSecretCalibrationPath,
		"NOTICE",
		"NOTICE-secret-rules.md",
	}
}

func decorateSensitiveGuardPlans(
	variants *[]contract.PrivacyModelVariant,
	plans map[string]customVariantPlan,
	siblings []hfSibling,
) bool {
	byPath := make(map[string]hfSibling, len(siblings))
	for _, sibling := range siblings {
		byPath[sibling.Filename] = sibling
	}
	paths := []string{
		sensitiveGuardViterbiCalibrationPath,
		sensitiveGuardSecretRulesPath,
		sensitiveGuardSecretCalibrationPath,
	}
	for _, optional := range []string{"NOTICE", "NOTICE-secret-rules.md"} {
		if _, exists := byPath[optional]; exists {
			paths = append(paths, optional)
		}
	}
	assets := make([]Asset, 0, len(paths))
	var extraBytes int64
	for _, path := range paths {
		sibling, exists := byPath[path]
		if !exists {
			return false
		}
		asset := assetFromSibling(sibling)
		if !validPinnedAsset(asset) ||
			extraBytes > contract.MaxPrivacyModelByteCount-asset.Size {
			return false
		}
		extraBytes += asset.Size
		assets = append(assets, asset)
	}
	for index := range *variants {
		variant := (*variants)[index]
		plan, exists := plans[variant.ID]
		if !exists || len(plan.assets)+len(assets) > 128 ||
			variant.BytesTotal > contract.MaxPrivacyModelByteCount-extraBytes {
			return false
		}
		variant.BytesTotal += extraBytes
		plan.variant = variant
		plan.assets = append(plan.assets, assets...)
		calibration := sensitiveGuardViterbiCalibrationPath
		rules := sensitiveGuardSecretRulesPath
		secretCalibration := sensitiveGuardSecretCalibrationPath
		plan.runtime.calibrationPath = &calibration
		plan.runtime.secretRulesPath = &rules
		plan.runtime.secretCalibrationPath = &secretCalibration
		plan.runtime.tagScheme = "bioes"
		plans[variant.ID] = plan
		(*variants)[index] = variant
	}
	return true
}

func scanLocalModelDirectory(
	ctx context.Context,
	directory string,
) (map[string]os.FileInfo, []string, error) {
	files := make(map[string]os.FileInfo)
	models := make([]string, 0)
	entries := 0
	err := filepath.WalkDir(directory, func(
		candidate string,
		entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return ErrLocalSource
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > maxLocalProbeEntries {
			return ErrUnsupportedModel
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrLocalSource
		}
		if candidate == directory || entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(directory, candidate)
		if err != nil {
			return ErrLocalSource
		}
		relative = filepath.ToSlash(relative)
		info, err := entry.Info()
		if err != nil {
			return ErrLocalSource
		}
		if !info.Mode().IsRegular() {
			return ErrLocalSource
		}
		files[relative] = info
		if strings.HasSuffix(strings.ToLower(relative), ".onnx") {
			if !safeAssetPath(relative) {
				return ErrUnsupportedModel
			}
			models = append(models, relative)
			if len(models) > 32 {
				return ErrUnsupportedModel
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(models)
	return files, models, nil
}

func localPlansRevision(
	plans map[string]customVariantPlan,
	adapter contract.PrivacyModelAdapter,
) (string, error) {
	ids := make([]string, 0, len(plans))
	for id := range plans {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fingerprint := localFingerprint{Version: 1, Adapter: adapter}
	for _, id := range ids {
		plan := plans[id]
		assets := append([]Asset(nil), plan.assets...)
		sort.Slice(assets, func(left, right int) bool {
			return assets[left].Path < assets[right].Path
		})
		item := localFingerprintVariant{
			ID: id, Name: plan.variant.Name,
			Quantization:          plan.variant.Quantization,
			EstimatedRAMBytes:     plan.variant.EstimatedRAMBytes,
			ModelPath:             plan.runtime.modelPath,
			ExternalData:          append([]string(nil), plan.runtime.externalData...),
			TokenizerPath:         plan.runtime.tokenizerPath,
			ConfigPath:            plan.runtime.configPath,
			CalibrationPath:       cloneString(plan.runtime.calibrationPath),
			SecretRulesPath:       cloneString(plan.runtime.secretRulesPath),
			SecretCalibrationPath: cloneString(plan.runtime.secretCalibrationPath),
			TagScheme:             plan.runtime.tagScheme,
			Window:                plan.runtime.window, Stride: plan.runtime.stride,
			MaxRequestTokens: plan.runtime.maxRequestTokens,
			InputNames:       plan.runtime.inputNames,
			OutputName:       plan.runtime.outputName,
			Assets:           make([]localFingerprintAsset, len(assets)),
		}
		for index, asset := range assets {
			item.Assets[index] = localFingerprintAsset{
				Path: asset.Path, Size: asset.Size, SHA256: asset.SHA256,
			}
		}
		fingerprint.Variants = append(fingerprint.Variants, item)
	}
	document, err := json.Marshal(fingerprint)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:20]), nil
}

func localRepoID(revision string) string {
	return localRepoPrefix + revision[:12]
}

func (registry *Registry) cacheLocalProbe(
	directory string,
	response contract.PrivacyModelProbeResponse,
	plans map[string]customVariantPlan,
) {
	now := time.Now()
	key := localProbeCacheKey(response.RepoID, response.Revision)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for candidate, entry := range registry.localProbes {
		if !entry.expiresAt.After(now) {
			if entry.timer != nil {
				entry.timer.Stop()
			}
			delete(registry.localProbes, candidate)
		}
	}
	previous, replacing := registry.localProbes[key]
	if !replacing &&
		len(registry.localProbes) >= localProbeCacheLimit {
		var oldestKey string
		var oldest time.Time
		for candidate, entry := range registry.localProbes {
			if oldestKey == "" || entry.expiresAt.Before(oldest) {
				oldestKey = candidate
				oldest = entry.expiresAt
			}
		}
		if oldestEntry := registry.localProbes[oldestKey]; oldestEntry.timer != nil {
			oldestEntry.timer.Stop()
		}
		delete(registry.localProbes, oldestKey)
	}
	if replacing && previous.timer != nil {
		previous.timer.Stop()
	}
	expiresAt := now.Add(localProbeCacheTTL)
	entry := localProbeCacheEntry{
		directory: directory,
		response:  cloneProbeResponse(response),
		plans:     cloneCustomVariantPlans(plans),
		expiresAt: expiresAt,
	}
	entry.timer = time.AfterFunc(localProbeCacheTTL, func() {
		registry.expireLocalProbe(key, expiresAt)
	})
	registry.localProbes[key] = entry
}

func (registry *Registry) expireLocalProbe(key string, expiresAt time.Time) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry, exists := registry.localProbes[key]
	if exists && entry.expiresAt.Equal(expiresAt) &&
		!entry.expiresAt.After(time.Now()) {
		delete(registry.localProbes, key)
	}
}

func (registry *Registry) cachedLocalProbe(
	repoID string,
	revision string,
) (localProbeCacheEntry, bool) {
	key := localProbeCacheKey(repoID, revision)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry, exists := registry.localProbes[key]
	if !exists {
		return localProbeCacheEntry{}, false
	}
	if !entry.expiresAt.After(time.Now()) {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(registry.localProbes, key)
		return localProbeCacheEntry{}, false
	}
	entry.response = cloneProbeResponse(entry.response)
	entry.plans = cloneCustomVariantPlans(entry.plans)
	return entry, true
}

func localProbeCacheKey(repoID, revision string) string {
	return repoID + "\x00" + revision
}

func cloneProbeResponse(
	response contract.PrivacyModelProbeResponse,
) contract.PrivacyModelProbeResponse {
	response.License = cloneString(response.License)
	response.Languages = cloneStrings(response.Languages)
	response.Variants = append([]contract.PrivacyModelVariant(nil), response.Variants...)
	for index := range response.Variants {
		response.Variants[index].UnsupportedReason = cloneString(
			response.Variants[index].UnsupportedReason,
		)
	}
	response.Labels = append([]contract.PrivacyModelLabel(nil), response.Labels...)
	for index := range response.Labels {
		if response.Labels[index].SuggestedKind != nil {
			kind := *response.Labels[index].SuggestedKind
			response.Labels[index].SuggestedKind = &kind
		}
	}
	return response
}

func cloneCustomVariantPlans(
	plans map[string]customVariantPlan,
) map[string]customVariantPlan {
	result := make(map[string]customVariantPlan, len(plans))
	for id, plan := range plans {
		plan.assets = copyAssets(plan.assets)
		plan.runtime.externalData = append([]string(nil), plan.runtime.externalData...)
		plan.runtime.calibrationPath = cloneString(plan.runtime.calibrationPath)
		plan.runtime.secretRulesPath = cloneString(plan.runtime.secretRulesPath)
		plan.runtime.secretCalibrationPath = cloneString(
			plan.runtime.secretCalibrationPath,
		)
		plan.runtime.inputNames.TokenTypeIDs = cloneString(
			plan.runtime.inputNames.TokenTypeIDs,
		)
		result[id] = plan
	}
	return result
}

func (registry *Registry) prepareLocalInstallation(
	request contract.PrivacyModelInstallRequest,
) (installationPlan, error) {
	entry, exists := registry.cachedLocalProbe(request.RepoID, request.Revision)
	if !exists {
		return installationPlan{}, ErrLocalProbeRequired
	}
	selected, exists := entry.plans[request.VariantID]
	if !exists || !selected.variant.Supported {
		return installationPlan{}, ErrUnsupportedModel
	}
	if err := validateLabelCoverage(entry.response.Labels, request.LabelMapping); err != nil {
		return installationPlan{}, err
	}
	id := InstallationID(request.RepoID, request.Revision, request.VariantID)
	return installationPlan{
		installation: contract.PrivacyModelInstallation{
			ID: id, Source: contract.PrivacyModelSourceLocal,
			Name: entry.response.Name, License: cloneString(entry.response.License),
			Languages: cloneStrings(entry.response.Languages),
			RepoID:    request.RepoID, Revision: request.Revision,
			VariantID: request.VariantID, VariantName: selected.variant.Name,
			Quantization:      selected.variant.Quantization,
			Adapter:           entry.response.Adapter,
			Status:            contract.PrivacyModelStatusDownloading,
			BytesTotal:        selected.variant.BytesTotal,
			EstimatedRAMBytes: selected.variant.EstimatedRAMBytes,
			LabelMapping:      cloneLabelMapping(request.LabelMapping),
		},
		assets: copyAssets(selected.assets), runtime: selected.runtime,
		localSource: entry.directory,
	}, nil
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
	written, copyErr := copyRegistryAsset(
		ctx,
		io.LimitReader(file, before.Size()+1),
		hasher,
		func(int64) {},
	)
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
		Path: relative, Size: before.Size(),
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

func (registry *Registry) copyLocalAsset(
	ctx context.Context,
	id contract.PrivacyModelID,
	temporary string,
	sourceRoot string,
	asset Asset,
) (_ Asset, resultErr error) {
	if asset.Size <= 0 || len(asset.SHA256) != sha256.Size*2 {
		return Asset{}, errModelIncompatible
	}
	source, before, sourcePath, err := openLocalAsset(
		sourceRoot,
		asset.Path,
		asset.Size,
	)
	if err != nil {
		return Asset{}, err
	}
	defer source.Close()
	destinationPath := filepath.Join(temporary, filepath.FromSlash(asset.Path))
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
		return Asset{}, ErrFilesystem
	}
	destination, err := os.OpenFile(
		destinationPath,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return Asset{}, ErrFilesystem
	}
	var reported int64
	defer func() {
		if resultErr != nil {
			_ = destination.Close()
			_ = os.Remove(destinationPath)
			if reported > 0 {
				registry.addProgress(id, -reported)
			}
		}
	}()
	hasher := sha256.New()
	written, copyErr := copyRegistryAsset(
		ctx,
		io.LimitReader(source, asset.Size+1),
		io.MultiWriter(destination, hasher),
		func(delta int64) {
			reported += delta
			registry.addProgress(id, delta)
		},
	)
	sourceAfter, sourceStatErr := source.Stat()
	pathAfter, pathStatErr := os.Lstat(sourcePath)
	sourceCloseErr := source.Close()
	destinationSyncErr := destination.Sync()
	destinationCloseErr := destination.Close()
	if copyErr != nil {
		if ctx.Err() != nil {
			return Asset{}, ctx.Err()
		}
		return Asset{}, ErrLocalSource
	}
	if sourceStatErr != nil || pathStatErr != nil || sourceCloseErr != nil ||
		written != asset.Size ||
		!sameLocalFileSnapshot(before, sourceAfter) ||
		!sameLocalFileSnapshot(before, pathAfter) {
		return Asset{}, errAssetIntegrity
	}
	if destinationSyncErr != nil || destinationCloseErr != nil {
		return Asset{}, ErrFilesystem
	}
	if hex.EncodeToString(hasher.Sum(nil)) != asset.SHA256 {
		return Asset{}, errAssetIntegrity
	}
	return asset, nil
}
