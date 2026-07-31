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
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const (
	registryDownloadError     = contract.PrivacyModelErrorDownload
	registryIntegrityError    = contract.PrivacyModelErrorIntegrity
	registryIncompatibleError = contract.PrivacyModelErrorIncompatible
	assetDownloadAttempts     = 3
)

var (
	ErrNotFound = errors.New("privacy model installation not found")
	ErrCapacity = errors.New("privacy model installation limit reached")

	errAssetIntegrity    = errors.New("privacy model asset integrity failed")
	errModelIncompatible = errors.New("privacy model is incompatible")
)

var assetRetryDelays = [...]time.Duration{
	250 * time.Millisecond,
	750 * time.Millisecond,
}

type assetDownloadFailure struct {
	reason    string
	retryable bool
	cause     error
}

func (failure *assetDownloadFailure) Error() string {
	return failure.reason
}

func (failure *assetDownloadFailure) Unwrap() error {
	return failure.cause
}

type RegistryConfig struct {
	RootDirectory        string
	MetadataBaseURL      string
	HTTPClient           HTTPClient
	Store                storage.PrivacyModelInstallationStore
	TestOnlyLoopbackMode bool
	Logf                 func(string, ...any)
}

type Registry struct {
	lifetime         context.Context
	rootDirectory    string
	installationsDir string
	stagingDir       string
	httpClient       HTTPClient
	store            storage.PrivacyModelInstallationStore
	probe            *hfProbe
	removeAll        func(string) error
	logf             func(string, ...any)

	mu            sync.Mutex
	installations map[contract.PrivacyModelID]contract.PrivacyModelInstallation
	bindings      map[contract.PrivacyModelID]storage.PrivacyModelManifestBinding
	operations    map[contract.PrivacyModelID]*registryOperation
	deleting      map[contract.PrivacyModelID]struct{}
}

type registryOperation struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type installationPlan struct {
	installation contract.PrivacyModelInstallation
	assets       []Asset
	runtime      runtimeSpec
}

func NewRegistry(
	lifetime context.Context,
	config RegistryConfig,
) (*Registry, error) {
	if lifetime == nil {
		lifetime = context.Background()
	}
	if strings.TrimSpace(config.RootDirectory) == "" {
		return nil, ErrInvalidConfig
	}
	root, err := filepath.Abs(config.RootDirectory)
	if err != nil {
		return nil, ErrFilesystem
	}
	if config.HTTPClient == nil && !config.TestOnlyLoopbackMode {
		config.HTTPClient = &http.Client{Timeout: downloadTimeout}
	}
	probe, err := newHFProbe(
		config.MetadataBaseURL,
		config.HTTPClient,
		config.TestOnlyLoopbackMode,
	)
	if err != nil {
		return nil, err
	}
	registry := &Registry{
		lifetime: lifetime, rootDirectory: root,
		installationsDir: filepath.Join(root, "installations"),
		stagingDir:       filepath.Join(root, "staging"),
		httpClient:       probe.client, store: config.Store, probe: probe,
		removeAll: os.RemoveAll, logf: config.Logf,
		installations: make(map[contract.PrivacyModelID]contract.PrivacyModelInstallation),
		bindings:      make(map[contract.PrivacyModelID]storage.PrivacyModelManifestBinding),
		operations:    make(map[contract.PrivacyModelID]*registryOperation),
		deleting:      make(map[contract.PrivacyModelID]struct{}),
	}
	for _, directory := range []string{
		registry.rootDirectory, registry.installationsDir, registry.stagingDir,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil ||
			os.Chmod(directory, 0o700) != nil {
			return nil, ErrFilesystem
		}
	}
	if err := registry.removeAbandonedStaging(); err != nil {
		return nil, err
	}
	if err := registry.loadPersisted(); err != nil {
		return nil, err
	}
	if err := registry.migrateLegacyOpenAI(); err != nil {
		return nil, err
	}
	return registry, nil
}

func (registry *Registry) Catalog() contract.PrivacyModelCatalogResponse {
	return BuiltinCatalog()
}

func (registry *Registry) Probe(
	ctx context.Context,
	request contract.PrivacyModelProbeRequest,
) (contract.PrivacyModelProbeResponse, error) {
	result, err := registry.probe.inspect(ctx, request)
	if err != nil {
		return contract.PrivacyModelProbeResponse{}, err
	}
	return result.response, nil
}

func (registry *Registry) ListInstallations() []contract.PrivacyModelInstallation {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	result := make([]contract.PrivacyModelInstallation, 0, len(registry.installations))
	for _, installation := range registry.installations {
		result = append(result, cloneInstallation(installation))
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].ID < result[right].ID
	})
	return result
}

func (registry *Registry) GetInstallation(
	id contract.PrivacyModelID,
) (contract.PrivacyModelInstallation, error) {
	if err := id.Validate(); err != nil {
		return contract.PrivacyModelInstallation{}, ErrNotFound
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	installation, exists := registry.installations[id]
	if !exists {
		return contract.PrivacyModelInstallation{}, ErrNotFound
	}
	return cloneInstallation(installation), nil
}

func (registry *Registry) Install(
	ctx context.Context,
	request contract.PrivacyModelInstallRequest,
) (contract.PrivacyModelInstallation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contract.ValidatePrivacyModelInstallRequest(request); err != nil {
		return contract.PrivacyModelInstallation{}, ErrInvalidConfig
	}
	plan, err := registry.prepareInstallation(ctx, request)
	if err != nil {
		return contract.PrivacyModelInstallation{}, err
	}
	id := plan.installation.ID
	registry.mu.Lock()
	if _, deleting := registry.deleting[id]; deleting {
		registry.mu.Unlock()
		return contract.PrivacyModelInstallation{}, ErrBusy
	}
	if _, exists := registry.operations[id]; exists {
		current := cloneInstallation(registry.installations[id])
		registry.mu.Unlock()
		return current, ErrBusy
	}
	if _, exists := registry.installations[id]; !exists &&
		len(registry.installations) >= 100 {
		registry.mu.Unlock()
		return contract.PrivacyModelInstallation{}, ErrCapacity
	}
	if current, exists := registry.installations[id]; exists &&
		current.Status == contract.PrivacyModelStatusReady {
		current = cloneInstallation(current)
		registry.mu.Unlock()
		return current, ErrAlreadyInstalled
	}
	finalDirectory := registry.installationDirectory(id)
	if err := os.RemoveAll(finalDirectory); err != nil {
		registry.mu.Unlock()
		return contract.PrivacyModelInstallation{}, ErrFilesystem
	}
	operationContext, cancel := context.WithCancel(registry.lifetime)
	operation := &registryOperation{cancel: cancel, done: make(chan struct{})}
	registry.operations[id] = operation
	registry.installations[id] = cloneInstallation(plan.installation)
	delete(registry.bindings, id)
	if err := registry.persistLocked(context.Background(), plan.installation); err != nil {
		delete(registry.operations, id)
		delete(registry.installations, id)
		cancel()
		registry.mu.Unlock()
		return contract.PrivacyModelInstallation{}, err
	}
	started := cloneInstallation(plan.installation)
	registry.mu.Unlock()
	go registry.download(operationContext, operation, plan)
	return started, nil
}

func (registry *Registry) DeleteInstallation(
	ctx context.Context,
	id contract.PrivacyModelID,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := id.Validate(); err != nil {
		return ErrNotFound
	}
	registry.mu.Lock()
	if _, deleting := registry.deleting[id]; deleting {
		registry.mu.Unlock()
		return ErrBusy
	}
	operation := registry.operations[id]
	_, exists := registry.installations[id]
	if !exists {
		registry.mu.Unlock()
		return ErrNotFound
	}
	if operation != nil {
		operation.cancel()
	}
	registry.deleting[id] = struct{}{}
	registry.mu.Unlock()
	if operation != nil {
		select {
		case <-operation.done:
		case <-ctx.Done():
			go func() {
				<-operation.done
				_ = registry.completeDeletion(context.Background(), id)
			}()
			return ctx.Err()
		}
	}
	return registry.completeDeletion(ctx, id)
}

func (registry *Registry) completeDeletion(
	ctx context.Context,
	id contract.PrivacyModelID,
) error {
	clearDeleting := true
	defer func() {
		if !clearDeleting {
			return
		}
		registry.mu.Lock()
		delete(registry.deleting, id)
		registry.mu.Unlock()
	}()
	finalDirectory := registry.installationDirectory(id)
	registry.invalidateInstallationForDeletion(id)
	if err := registry.removeAll(finalDirectory); err != nil {
		registry.persistDeletionFailure(id)
		return ErrFilesystem
	}
	if err := registry.removeStagingFor(id); err != nil {
		registry.persistDeletionFailure(id)
		return err
	}
	if err := syncDirectory(registry.installationsDir); err != nil {
		registry.persistDeletionFailure(id)
		return ErrFilesystem
	}
	if registry.store != nil {
		if err := registry.store.DeletePrivacyModelInstallation(ctx, id); err != nil &&
			!errors.Is(err, storage.ErrNotFound) {
			registry.persistDeletionFailure(id)
			return err
		}
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.installations, id)
	delete(registry.bindings, id)
	delete(registry.deleting, id)
	clearDeleting = false
	return nil
}

func (registry *Registry) invalidateInstallationForDeletion(
	id contract.PrivacyModelID,
) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	installation, exists := registry.installations[id]
	if !exists {
		delete(registry.bindings, id)
		return
	}
	modelError := registryIntegrityError
	installation.Status = contract.PrivacyModelStatusError
	installation.Error = &modelError
	installation.InstalledAt = nil
	registry.installations[id] = installation
	delete(registry.bindings, id)
}

func (registry *Registry) persistDeletionFailure(id contract.PrivacyModelID) {
	if registry.store == nil {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	installation, exists := registry.installations[id]
	if !exists {
		return
	}
	_ = registry.persistLocked(context.Background(), installation)
}

func (registry *Registry) ReadyInstallation(
	id contract.PrivacyModelID,
) (contract.ReadyPrivacyModelInstallation, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	installation, exists := registry.installations[id]
	if !exists || installation.Status != contract.PrivacyModelStatusReady {
		return contract.ReadyPrivacyModelInstallation{}, false
	}
	binding, exists := registry.bindings[id]
	if !exists {
		return contract.ReadyPrivacyModelInstallation{}, false
	}
	return contract.ReadyPrivacyModelInstallation{
		Directory:      registry.installationDirectory(id),
		Identity:       binding.Identity,
		ManifestSHA256: binding.SHA256,
	}, true
}

func (registry *Registry) prepareInstallation(
	ctx context.Context,
	request contract.PrivacyModelInstallRequest,
) (installationPlan, error) {
	id := InstallationID(request.RepoID, request.Revision, request.VariantID)
	if catalogPlan, exists := builtinVariantPlan(
		request.RepoID, request.Revision, request.VariantID,
	); exists {
		mapping := cloneLabelMapping(request.LabelMapping)
		if catalogPlan.item.Adapter == contract.PrivacyModelAdapterOpenAIBIOES {
			fixedMapping := defaultOpenAILabelMapping()
			if len(mapping) == 0 {
				mapping = fixedMapping
			} else if !sameLabelKeys(mapping, fixedMapping) {
				return installationPlan{}, ErrInvalidConfig
			}
		} else {
			if catalogPlan.item.ID == CatalogNymPIIMultilingualSmall &&
				len(mapping) == 0 {
				mapping = defaultNymLabelMapping()
			}
			probed, err := registry.probe.inspect(
				ctx,
				contract.PrivacyModelProbeRequest{
					RepoID:   request.RepoID,
					Revision: request.Revision,
				},
			)
			if err != nil {
				return installationPlan{}, err
			}
			if probed.response.Revision != request.Revision ||
				probed.response.Adapter != catalogPlan.item.Adapter ||
				!probeContainsSupportedVariant(
					probed.response.Variants,
					request.VariantID,
				) ||
				validateLabelCoverage(
					probed.response.Labels,
					mapping,
				) != nil {
				return installationPlan{}, ErrInvalidConfig
			}
		}
		catalogID := catalogPlan.item.ID
		catalogSource := catalogPlan.item.Source
		license := catalogPlan.item.License
		return installationPlan{
			installation: contract.PrivacyModelInstallation{
				ID: id, Source: contract.PrivacyModelSourceCatalog,
				CatalogID: &catalogID, CatalogSource: &catalogSource,
				Name: catalogPlan.item.Name, License: &license,
				Languages: cloneStrings(catalogPlan.item.Languages),
				RepoID:    request.RepoID, Revision: request.Revision,
				VariantID: request.VariantID, VariantName: catalogPlan.variant.Name,
				Quantization:      catalogPlan.variant.Quantization,
				Adapter:           catalogPlan.item.Adapter,
				Status:            contract.PrivacyModelStatusDownloading,
				BytesTotal:        catalogPlan.variant.BytesTotal,
				EstimatedRAMBytes: catalogPlan.variant.EstimatedRAMBytes,
				LabelMapping:      mapping,
			},
			assets: copyAssets(catalogPlan.assets), runtime: catalogPlan.runtime,
		}, nil
	}
	probed, err := registry.probe.inspect(ctx, contract.PrivacyModelProbeRequest{
		RepoID: request.RepoID, Revision: request.Revision,
	})
	if err != nil {
		return installationPlan{}, err
	}
	if probed.response.Revision != request.Revision {
		return installationPlan{}, ErrRemoteMetadata
	}
	customPlan, exists := probed.plans[request.VariantID]
	if !exists || !customPlan.variant.Supported {
		return installationPlan{}, ErrUnsupportedModel
	}
	if err := validateLabelCoverage(probed.response.Labels, request.LabelMapping); err != nil {
		return installationPlan{}, err
	}
	return installationPlan{
		installation: contract.PrivacyModelInstallation{
			ID: id, Source: contract.PrivacyModelSourceCustom,
			Name: probed.response.Name, RepoID: request.RepoID,
			License:   cloneString(probed.response.License),
			Languages: cloneStrings(probed.response.Languages),
			Revision:  request.Revision, VariantID: request.VariantID,
			VariantName:       customPlan.variant.Name,
			Quantization:      customPlan.variant.Quantization,
			Adapter:           probed.response.Adapter,
			Status:            contract.PrivacyModelStatusDownloading,
			BytesTotal:        customPlan.variant.BytesTotal,
			EstimatedRAMBytes: customPlan.variant.EstimatedRAMBytes,
			LabelMapping:      cloneLabelMapping(request.LabelMapping),
		},
		assets: copyAssets(customPlan.assets), runtime: customPlan.runtime,
	}, nil
}

func sameLabelKeys(
	left map[string]*contract.CanonicalKind,
	right map[string]*contract.CanonicalKind,
) bool {
	if len(left) != len(right) {
		return false
	}
	for label := range left {
		if _, exists := right[label]; !exists {
			return false
		}
	}
	return true
}

func probeContainsSupportedVariant(
	variants []contract.PrivacyModelVariant,
	id string,
) bool {
	for _, variant := range variants {
		if variant.ID == id {
			return variant.Supported
		}
	}
	return false
}

func validateLabelCoverage(
	labels []contract.PrivacyModelLabel,
	mapping map[string]*contract.CanonicalKind,
) error {
	if len(labels) == 0 || len(mapping) != len(labels) {
		return ErrInvalidConfig
	}
	for _, label := range labels {
		if _, exists := mapping[label.Label]; !exists {
			return ErrInvalidConfig
		}
	}
	return nil
}

func validateStagedCompatibility(
	directory string,
	installation contract.PrivacyModelInstallation,
	runtime runtimeSpec,
) error {
	if !installation.Adapter.Valid() ||
		(runtime.tagScheme != "bio" && runtime.tagScheme != "bioes") {
		return errModelIncompatible
	}
	configDocument, err := readStagedJSONAsset(
		directory,
		runtime.configPath,
		maxConfigBytes,
	)
	if err != nil {
		return errModelIncompatible
	}
	tokenizerDocument, err := readStagedJSONAsset(
		directory,
		runtime.tokenizerPath,
		maxProbeJSONAssetBytes,
	)
	if err != nil {
		return errModelIncompatible
	}
	var tokenizerObject map[string]json.RawMessage
	if json.Unmarshal(tokenizerDocument, &tokenizerObject) != nil ||
		len(tokenizerObject) == 0 {
		return errModelIncompatible
	}
	var config hfModelConfig
	if json.Unmarshal(configDocument, &config) != nil {
		return errModelIncompatible
	}
	labels, tagScheme, _, validLabels := probeLabels(config.ID2Label)
	if !validLabels || tagScheme != runtime.tagScheme ||
		validateLabelCoverage(labels, installation.LabelMapping) != nil {
		return errModelIncompatible
	}
	switch installation.Adapter {
	case contract.PrivacyModelAdapterOpenAIBIOES:
		if !validOpenAILabelOrder(config.ID2Label) ||
			runtime.calibrationPath == nil {
			return errModelIncompatible
		}
		calibrationDocument, err := readStagedJSONAsset(
			directory,
			*runtime.calibrationPath,
			maxConfigBytes,
		)
		if err != nil ||
			validateCalibrationDocument(calibrationDocument) != nil {
			return errModelIncompatible
		}
	case contract.PrivacyModelAdapterHFToken:
		if runtime.calibrationPath != nil {
			return errModelIncompatible
		}
	}
	return nil
}

func readStagedJSONAsset(
	directory string,
	assetPath string,
	limit int64,
) ([]byte, error) {
	if !safeAssetPath(assetPath) {
		return nil, errModelIncompatible
	}
	candidate := filepath.Join(
		directory,
		filepath.FromSlash(assetPath),
	)
	info, err := os.Lstat(candidate)
	if err != nil || !info.Mode().IsRegular() ||
		info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 ||
		info.Size() > limit {
		return nil, errModelIncompatible
	}
	file, err := os.Open(candidate)
	if err != nil {
		return nil, errModelIncompatible
	}
	document, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil ||
		len(document) == 0 || int64(len(document)) > limit ||
		!json.Valid(document) {
		return nil, errModelIncompatible
	}
	return document, nil
}

func validOpenAILabelOrder(id2label map[string]string) bool {
	sources := []string{
		"account_number",
		"private_address",
		"private_date",
		"private_email",
		"private_person",
		"private_phone",
		"private_url",
		"secret",
	}
	if len(id2label) != 1+len(sources)*4 || id2label["0"] != "O" {
		return false
	}
	index := 1
	for _, source := range sources {
		for _, prefix := range []string{"B", "I", "E", "S"} {
			value, exists := id2label[strconv.Itoa(index)]
			if !exists || len(value) < 3 ||
				value[:1] != prefix ||
				(value[1] != '-' && value[1] != '_') ||
				value[2:] != source {
				return false
			}
			index++
		}
	}
	return true
}

func validateCalibrationDocument(document []byte) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(document, &root) != nil || root == nil {
		return errModelIncompatible
	}
	operatingPoints, hasOperatingPoints := root["operating_points"]
	operatingPoint, hasOperatingPoint := root["operating_point"]
	if hasOperatingPoints == hasOperatingPoint {
		return errModelIncompatible
	}

	var biasesDocument json.RawMessage
	if hasOperatingPoints {
		var points map[string]json.RawMessage
		if json.Unmarshal(operatingPoints, &points) != nil || points == nil {
			return errModelIncompatible
		}
		defaultPoint, exists := points["default"]
		if !exists {
			return errModelIncompatible
		}
		var point map[string]json.RawMessage
		if json.Unmarshal(defaultPoint, &point) != nil || point == nil {
			return errModelIncompatible
		}
		biases, exists := point["biases"]
		if !exists {
			return errModelIncompatible
		}
		biasesDocument = biases
	} else {
		var point map[string]json.RawMessage
		if json.Unmarshal(operatingPoint, &point) != nil || point == nil {
			return errModelIncompatible
		}
		transitionBiases, exists := point["transition_biases"]
		if !exists {
			return errModelIncompatible
		}
		biasesDocument = transitionBiases
	}

	var biases map[string]json.RawMessage
	if json.Unmarshal(biasesDocument, &biases) != nil || biases == nil {
		return errModelIncompatible
	}
	fields := []string{
		"transition_bias_background_stay",
		"transition_bias_background_to_start",
		"transition_bias_end_to_background",
		"transition_bias_end_to_start",
		"transition_bias_inside_to_continue",
		"transition_bias_inside_to_end",
	}
	if len(biases) != len(fields) {
		return errModelIncompatible
	}
	for _, field := range fields {
		document, exists := biases[field]
		if !exists {
			return errModelIncompatible
		}
		var value float64
		if json.Unmarshal(document, &value) != nil ||
			math.IsNaN(value) ||
			math.IsInf(value, 0) ||
			math.Abs(value) > float64(math.MaxFloat32) {
			return errModelIncompatible
		}
	}
	return nil
}

func manifestMatchesInstallation(
	manifest normalizedManifest,
	installation contract.PrivacyModelInstallation,
) bool {
	if manifest.InstallationID != installation.ID ||
		manifest.RepoID != installation.RepoID ||
		manifest.Revision != installation.Revision ||
		manifest.VariantID != installation.VariantID ||
		manifest.Adapter != installation.Adapter ||
		manifest.Identity != installation.RepoID+"@"+
			installation.Revision+"#"+installation.VariantID ||
		!labelMappingsEqual(
			manifest.LabelMapping,
			installation.LabelMapping,
		) {
		return false
	}
	var total int64
	for _, file := range manifest.Files {
		if file.Size <= 0 || total > installation.BytesTotal-file.Size {
			return false
		}
		total += file.Size
	}
	return total == installation.BytesTotal
}

func manifestMatchesBuiltinPlan(
	manifest normalizedManifest,
	plan variantPlan,
) bool {
	if manifest.Adapter != plan.item.Adapter ||
		manifest.ModelPath != plan.runtime.modelPath ||
		!stringSlicesEqual(manifest.ExternalData, plan.runtime.externalData) ||
		manifest.TokenizerPath != plan.runtime.tokenizerPath ||
		manifest.ConfigPath != plan.runtime.configPath ||
		!optionalStringsEqual(
			manifest.CalibrationPath,
			plan.runtime.calibrationPath,
		) ||
		manifest.TagScheme != plan.runtime.tagScheme ||
		manifest.Window != plan.runtime.window ||
		manifest.Stride != plan.runtime.stride ||
		manifest.MaxRequestTokens != plan.runtime.maxRequestTokens ||
		manifest.InputNames != plan.runtime.inputNames ||
		manifest.OutputName != plan.runtime.outputName ||
		len(manifest.Files) != len(plan.assets) {
		return false
	}
	expectedFiles := make(map[string]Asset, len(plan.assets))
	for _, asset := range plan.assets {
		expectedFiles[asset.Path] = asset
	}
	for _, file := range manifest.Files {
		expected, exists := expectedFiles[file.Path]
		if !exists || file.Size != expected.Size ||
			file.SHA256 != expected.SHA256 {
			return false
		}
	}
	return true
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func optionalStringsEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func labelMappingsEqual(
	left map[string]*contract.CanonicalKind,
	right map[string]*contract.CanonicalKind,
) bool {
	if len(left) != len(right) {
		return false
	}
	for label, leftKind := range left {
		rightKind, exists := right[label]
		if !exists || (leftKind == nil) != (rightKind == nil) {
			return false
		}
		if leftKind != nil && *leftKind != *rightKind {
			return false
		}
	}
	return true
}

func manifestBinding(
	identity string,
	document []byte,
) storage.PrivacyModelManifestBinding {
	digest := sha256.Sum256(document)
	return storage.PrivacyModelManifestBinding{
		Identity: identity,
		SHA256:   hex.EncodeToString(digest[:]),
		JSON:     append([]byte(nil), document...),
	}
}

func manifestBindingMatches(
	binding storage.PrivacyModelManifestBinding,
	identity string,
	document []byte,
) bool {
	expected := manifestBinding(identity, document)
	return binding.Identity == expected.Identity &&
		binding.SHA256 == expected.SHA256 &&
		bytes.Equal(binding.JSON, expected.JSON)
}

func cloneManifestBinding(
	binding storage.PrivacyModelManifestBinding,
) storage.PrivacyModelManifestBinding {
	binding.JSON = append([]byte(nil), binding.JSON...)
	return binding
}

func registryErrorCode(cause error) contract.PrivacyModelInstallationError {
	switch {
	case errors.Is(cause, errAssetIntegrity):
		return registryIntegrityError
	case errors.Is(cause, errModelIncompatible),
		errors.Is(cause, ErrInvalidConfig),
		errors.Is(cause, ErrUnsupportedModel):
		return registryIncompatibleError
	default:
		return registryDownloadError
	}
}

func (registry *Registry) download(
	ctx context.Context,
	operation *registryOperation,
	plan installationPlan,
) {
	id := plan.installation.ID
	if registry.logf != nil {
		registry.logf(
			"privacy model download started: model_id=%s assets=%d bytes=%d",
			id,
			len(plan.assets),
			plan.installation.BytesTotal,
		)
	}
	fail := func(stage string, cause error) {
		if stage != "asset" && registry.logf != nil {
			registry.logf(
				"privacy model download failed: model_id=%s stage=%s reason=%s",
				id,
				stage,
				assetFailureReason(cause),
			)
		}
		registry.finishDownloadError(id, cause)
	}
	defer func() {
		registry.mu.Lock()
		delete(registry.operations, id)
		close(operation.done)
		registry.mu.Unlock()
	}()
	temporary, err := os.MkdirTemp(
		registry.stagingDir,
		".privacy-model-download-"+string(id)+"-",
	)
	if err != nil {
		fail("staging", err)
		return
	}
	defer os.RemoveAll(temporary)
	if err := os.Chmod(temporary, 0o700); err != nil {
		fail("staging", err)
		return
	}
	verifiedAssets := make([]Asset, 0, len(plan.assets))
	for index, asset := range plan.assets {
		verified, err := registry.downloadAssetWithPosition(
			ctx,
			id,
			temporary,
			plan.installation,
			asset,
			index+1,
			len(plan.assets),
		)
		if err != nil {
			fail("asset", err)
			return
		}
		verifiedAssets = append(verifiedAssets, verified)
		registry.persistProgress(id)
	}
	if ctx.Err() != nil {
		fail("cancelled", ctx.Err())
		return
	}
	if err := validateStagedCompatibility(
		temporary,
		plan.installation,
		plan.runtime,
	); err != nil {
		fail("compatibility", err)
		return
	}
	manifest := buildNormalizedManifest(plan.installation, plan.runtime, verifiedAssets)
	if err := validateNormalizedManifest(manifest); err != nil {
		fail("manifest", errModelIncompatible)
		return
	}
	if err := writeNormalizedManifest(temporary, manifest); err != nil {
		fail("manifest", err)
		return
	}
	verifiedManifest, manifestDocument, err :=
		inspectNormalizedInstallationDocument(ctx, temporary, id)
	if err != nil {
		fail("verification", errAssetIntegrity)
		return
	}
	if !manifestMatchesInstallation(verifiedManifest, plan.installation) {
		fail("verification", errModelIncompatible)
		return
	}
	if err := syncStagedDirectories(temporary, verifiedAssets, syncDirectory); err != nil {
		fail("publish", err)
		return
	}
	finalDirectory := registry.installationDirectory(id)
	if _, err := os.Lstat(finalDirectory); err == nil || !errors.Is(err, os.ErrNotExist) {
		fail("publish", errAssetIntegrity)
		return
	}
	if ctx.Err() != nil {
		fail("cancelled", ctx.Err())
		return
	}
	if os.Rename(temporary, finalDirectory) != nil {
		fail("publish", ErrFilesystem)
		return
	}
	if syncDirectory(registry.installationsDir) != nil {
		fail("publish", ErrFilesystem)
		return
	}
	registry.finishDownloadReady(id, manifestBinding(
		verifiedManifest.Identity,
		manifestDocument,
	))
	if current, err := registry.GetInstallation(id); err == nil &&
		current.Status == contract.PrivacyModelStatusReady {
		if registry.logf != nil {
			registry.logf(
				"privacy model download ready: model_id=%s bytes=%d",
				id,
				current.BytesDownloaded,
			)
		}
	} else if registry.logf != nil {
		registry.logf(
			"privacy model download failed: model_id=%s stage=persist reason=filesystem",
			id,
		)
	}
}

func (registry *Registry) downloadAssetWithPosition(
	ctx context.Context,
	id contract.PrivacyModelID,
	temporary string,
	installation contract.PrivacyModelInstallation,
	asset Asset,
	position int,
	assetCount int,
) (Asset, error) {
	for attempt := 1; attempt <= assetDownloadAttempts; attempt++ {
		verified, err := registry.downloadAssetOnce(
			ctx,
			id,
			temporary,
			installation,
			asset,
		)
		if err == nil {
			return verified, nil
		}
		var failure *assetDownloadFailure
		retryable := errors.As(err, &failure) && failure.retryable
		reason := assetFailureReason(err)
		if !retryable || attempt == assetDownloadAttempts || ctx.Err() != nil {
			registry.logDownloadEvent(
				"failed",
				id,
				asset,
				position,
				assetCount,
				attempt,
				reason,
			)
			return Asset{}, err
		}
		registry.logDownloadEvent(
			"retry",
			id,
			asset,
			position,
			assetCount,
			attempt,
			reason,
		)
		delay := assetRetryDelays[attempt-1]
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return Asset{}, ctx.Err()
		case <-timer.C:
		}
	}
	return Asset{}, ErrRemoteMetadata
}

func (registry *Registry) downloadAssetOnce(
	ctx context.Context,
	id contract.PrivacyModelID,
	temporary string,
	installation contract.PrivacyModelInstallation,
	asset Asset,
) (_ Asset, resultErr error) {
	if !safeAssetPath(asset.Path) || asset.Size <= 0 {
		return Asset{}, errModelIncompatible
	}
	destination := filepath.Join(temporary, filepath.FromSlash(asset.Path))
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return Asset{}, ErrFilesystem
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Asset{}, ErrFilesystem
	}
	closeWithError := func() {
		_ = file.Close()
		_ = os.Remove(destination)
	}
	endpoint := registry.probe.endpoint(
		installation.RepoID, "resolve", installation.Revision, asset.Path,
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		closeWithError()
		return Asset{}, ErrFilesystem
	}
	response, err := registry.httpClient.Do(request)
	if err != nil {
		closeWithError()
		if ctx.Err() != nil {
			return Asset{}, ctx.Err()
		}
		return Asset{}, &assetDownloadFailure{
			reason:    networkFailureReason(err),
			retryable: true,
			cause:     ErrRemoteMetadata,
		}
	}
	if response.StatusCode != http.StatusOK ||
		response.ContentLength > asset.Size {
		_ = response.Body.Close()
		closeWithError()
		if response.StatusCode != http.StatusOK {
			return Asset{}, &assetDownloadFailure{
				reason:    fmt.Sprintf("http_%d", response.StatusCode),
				retryable: retryableHTTPStatus(response.StatusCode),
				cause:     ErrRemoteMetadata,
			}
		}
		return Asset{}, &assetDownloadFailure{
			reason:    "content_length",
			retryable: true,
			cause:     ErrRemoteMetadata,
		}
	}
	if response.ContentLength >= 0 && response.ContentLength != asset.Size {
		_ = response.Body.Close()
		closeWithError()
		return Asset{}, &assetDownloadFailure{
			reason:    "content_length",
			retryable: true,
			cause:     ErrRemoteMetadata,
		}
	}
	hasher := sha256.New()
	var reported int64
	written, copyErr := copyRegistryAsset(
		ctx,
		io.LimitReader(response.Body, asset.Size+1),
		io.MultiWriter(file, hasher),
		func(delta int64) {
			remaining := asset.Size - reported
			if remaining <= 0 {
				return
			}
			if delta > remaining {
				delta = remaining
			}
			reported += delta
			registry.addProgress(id, delta)
		},
	)
	defer func() {
		if resultErr != nil && reported > 0 {
			registry.addProgress(id, -reported)
		}
	}()
	bodyCloseErr := response.Body.Close()
	syncErr := file.Sync()
	fileCloseErr := file.Close()
	digest := hex.EncodeToString(hasher.Sum(nil))
	if copyErr != nil || bodyCloseErr != nil || written != asset.Size {
		_ = os.Remove(destination)
		if ctx.Err() != nil {
			return Asset{}, ctx.Err()
		}
		if errors.Is(copyErr, io.ErrShortWrite) {
			return Asset{}, &assetDownloadFailure{
				reason: "filesystem", cause: ErrFilesystem,
			}
		}
		return Asset{}, &assetDownloadFailure{
			reason:    "body_read",
			retryable: true,
			cause:     ErrRemoteMetadata,
		}
	}
	if syncErr != nil || fileCloseErr != nil {
		_ = os.Remove(destination)
		return Asset{}, &assetDownloadFailure{
			reason: "filesystem", cause: ErrFilesystem,
		}
	}
	if asset.SHA256 != "" && digest != asset.SHA256 {
		_ = os.Remove(destination)
		return Asset{}, &assetDownloadFailure{
			reason: "sha256", cause: errAssetIntegrity,
		}
	}
	asset.SHA256 = digest
	return asset, nil
}

func retryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout,
		http.StatusTooEarly,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func networkFailureReason(cause error) string {
	var dnsError *net.DNSError
	if errors.As(cause, &dnsError) {
		return "dns"
	}
	var networkError net.Error
	if errors.As(cause, &networkError) && networkError.Timeout() {
		return "network_timeout"
	}
	return "network"
}

func assetFailureReason(cause error) string {
	var failure *assetDownloadFailure
	if errors.As(cause, &failure) {
		return failure.reason
	}
	switch {
	case errors.Is(cause, context.Canceled):
		return "cancelled"
	case errors.Is(cause, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(cause, errAssetIntegrity):
		return "integrity"
	case errors.Is(cause, errModelIncompatible):
		return "incompatible"
	case errors.Is(cause, ErrFilesystem):
		return "filesystem"
	case errors.Is(cause, ErrRemoteMetadata):
		return "remote_metadata"
	default:
		return "internal"
	}
}

func (registry *Registry) logDownloadEvent(
	event string,
	id contract.PrivacyModelID,
	asset Asset,
	position int,
	assetCount int,
	attempt int,
	reason string,
) {
	if registry.logf == nil {
		return
	}
	registry.logf(
		"privacy model download %s: model_id=%s asset=%d/%d path=%q attempt=%d/%d reason=%s",
		event,
		id,
		position,
		assetCount,
		asset.Path,
		attempt,
		assetDownloadAttempts,
		reason,
	)
}

func copyRegistryAsset(
	ctx context.Context,
	source io.Reader,
	destination io.Writer,
	progress func(int64),
) (int64, error) {
	buffer := make([]byte, 64*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			written, writeErr := destination.Write(buffer[:count])
			total += int64(written)
			if written > 0 {
				progress(int64(written))
			}
			if writeErr != nil {
				return total, writeErr
			}
			if written != count {
				return total, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func (registry *Registry) addProgress(id contract.PrivacyModelID, delta int64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	installation, exists := registry.installations[id]
	if !exists || installation.Status != contract.PrivacyModelStatusDownloading {
		return
	}
	installation.BytesDownloaded += delta
	if installation.BytesDownloaded < 0 {
		installation.BytesDownloaded = 0
	}
	if installation.BytesDownloaded > installation.BytesTotal {
		installation.BytesDownloaded = installation.BytesTotal
	}
	registry.installations[id] = installation
}

func (registry *Registry) persistProgress(id contract.PrivacyModelID) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if installation, exists := registry.installations[id]; exists {
		_ = registry.persistLocked(context.Background(), installation)
	}
}

func (registry *Registry) finishDownloadReady(
	id contract.PrivacyModelID,
	binding storage.PrivacyModelManifestBinding,
) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	installation, exists := registry.installations[id]
	if !exists {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	installation.Status = contract.PrivacyModelStatusReady
	installation.BytesDownloaded = installation.BytesTotal
	installation.Error = nil
	installation.InstalledAt = &now
	registry.installations[id] = installation
	registry.bindings[id] = cloneManifestBinding(binding)
	if err := registry.persistLocked(
		context.Background(),
		installation,
	); err != nil {
		message := registryDownloadError
		installation.Status = contract.PrivacyModelStatusError
		installation.Error = &message
		installation.InstalledAt = nil
		registry.installations[id] = installation
		delete(registry.bindings, id)
		_ = registry.persistLocked(context.Background(), installation)
	}
}

func (registry *Registry) finishDownloadError(
	id contract.PrivacyModelID,
	cause error,
) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	installation, exists := registry.installations[id]
	if !exists {
		return
	}
	message := registryErrorCode(cause)
	installation.Status = contract.PrivacyModelStatusError
	installation.Error = &message
	installation.InstalledAt = nil
	registry.installations[id] = installation
	delete(registry.bindings, id)
	_ = registry.persistLocked(context.Background(), installation)
}

func (registry *Registry) loadPersisted() error {
	if registry.store == nil {
		return nil
	}
	records, err := registry.store.ListPrivacyModelInstallations(
		context.Background(),
	)
	if err != nil {
		return err
	}
	for _, record := range records {
		installation := record.Installation
		if !validInstallationProvenance(installation) {
			return fmt.Errorf(
				"%w: privacy model installation provenance is invalid",
				storage.ErrInvalidRecord,
			)
		}
		if installation.Status == contract.PrivacyModelStatusReady {
			manifest, manifestDocument, inspectErr :=
				readNormalizedInstallationDocument(
					registry.installationDirectory(installation.ID),
					installation.ID,
				)
			if inspectErr != nil ||
				record.Manifest == nil ||
				!manifestMatchesInstallation(manifest, installation) ||
				!manifestMatchesInstallationProvenance(
					manifest,
					installation,
				) ||
				!manifestBindingMatches(
					*record.Manifest,
					manifest.Identity,
					manifestDocument,
				) {
				message := registryIntegrityError
				installation.Status = contract.PrivacyModelStatusError
				installation.Error = &message
				installation.InstalledAt = nil
				record.Manifest = nil
			} else {
				registry.bindings[installation.ID] =
					cloneManifestBinding(*record.Manifest)
			}
		} else if installation.Status == contract.PrivacyModelStatusDownloading {
			message := registryDownloadError
			installation.Status = contract.PrivacyModelStatusError
			installation.Error = &message
			record.Manifest = nil
		}
		registry.installations[installation.ID] = cloneInstallation(installation)
		record.Installation = installation
		if err := registry.store.PutPrivacyModelInstallation(
			context.Background(),
			record,
		); err != nil {
			return err
		}
	}
	return nil
}

func manifestMatchesInstallationProvenance(
	manifest normalizedManifest,
	installation contract.PrivacyModelInstallation,
) bool {
	if installation.Source == contract.PrivacyModelSourceCustom {
		return true
	}
	plan, exists := builtinVariantPlan(
		installation.RepoID,
		installation.Revision,
		installation.VariantID,
	)
	return exists && manifestMatchesBuiltinPlan(manifest, plan)
}

func (registry *Registry) migrateLegacyOpenAI() error {
	id := contract.LegacyOpenAIPrivacyFilterInstallationID
	if _, exists := registry.installations[id]; exists {
		return nil
	}
	finalDirectory := registry.installationDirectory(id)
	if manifest, manifestDocument, err := inspectNormalizedInstallationDocument(
		registry.lifetime, finalDirectory, id,
	); err == nil {
		return registry.recordRecoveredInstallation(manifest, manifestDocument)
	}
	legacyDirectory := filepath.Join(registry.rootDirectory, DefaultRevision)
	info, err := os.Lstat(legacyDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	manifest := ProductionManifest()
	_, digest, err := validateManifest(manifest)
	if err != nil {
		return err
	}
	marker, err := os.ReadFile(filepath.Join(legacyDirectory, readyMarkerName))
	if err != nil || strings.TrimSpace(string(marker)) != digest {
		return nil
	}
	for _, asset := range manifest.Assets {
		if err := inspectPublishedAsset(registry.lifetime, legacyDirectory, asset); err != nil {
			return nil
		}
	}
	plan, _ := builtinVariantPlan(
		"openai/privacy-filter", DefaultRevision, "cpu_q4",
	)
	catalogID := CatalogOpenAIPrivacyFilter
	catalogSource := plan.item.Source
	license := plan.item.License
	now := time.Now().UTC().Format(time.RFC3339Nano)
	installation := contract.PrivacyModelInstallation{
		ID: id, Source: contract.PrivacyModelSourceCatalog,
		CatalogID: &catalogID, CatalogSource: &catalogSource,
		Name: plan.item.Name, License: &license,
		Languages: cloneStrings(plan.item.Languages),
		RepoID:    plan.item.RepoID, Revision: plan.item.Revision,
		VariantID: plan.variant.ID, VariantName: plan.variant.Name,
		Quantization: plan.variant.Quantization, Adapter: plan.item.Adapter,
		Status:          contract.PrivacyModelStatusReady,
		BytesDownloaded: plan.variant.BytesTotal, BytesTotal: plan.variant.BytesTotal,
		EstimatedRAMBytes: plan.variant.EstimatedRAMBytes,
		LabelMapping:      defaultOpenAILabelMapping(), InstalledAt: &now,
	}
	normalized := buildNormalizedManifest(installation, plan.runtime, plan.assets)
	if _, err := os.Lstat(filepath.Join(legacyDirectory, InstallationManifestName)); errors.Is(err, os.ErrNotExist) {
		if err := writeNormalizedManifest(legacyDirectory, normalized); err != nil {
			return err
		}
	}
	verifiedManifest, manifestDocument, err :=
		inspectNormalizedInstallationDocument(registry.lifetime, legacyDirectory, id)
	if err != nil {
		return err
	}
	if err := os.Rename(legacyDirectory, finalDirectory); err != nil ||
		syncDirectory(registry.installationsDir) != nil ||
		syncDirectory(registry.rootDirectory) != nil {
		return ErrFilesystem
	}
	registry.installations[id] = cloneInstallation(installation)
	registry.bindings[id] = manifestBinding(
		verifiedManifest.Identity,
		manifestDocument,
	)
	return registry.persistLocked(context.Background(), installation)
}

func (registry *Registry) recordRecoveredInstallation(
	manifest normalizedManifest,
	manifestDocument []byte,
) error {
	plan, exists := builtinVariantPlan(
		manifest.RepoID, manifest.Revision, manifest.VariantID,
	)
	if !exists || !manifestMatchesBuiltinPlan(manifest, plan) {
		return nil
	}
	catalogID := plan.item.ID
	catalogSource := plan.item.Source
	license := plan.item.License
	now := time.Now().UTC().Format(time.RFC3339Nano)
	installation := contract.PrivacyModelInstallation{
		ID: manifest.InstallationID, Source: contract.PrivacyModelSourceCatalog,
		CatalogID: &catalogID, CatalogSource: &catalogSource,
		Name: plan.item.Name, License: &license,
		Languages: cloneStrings(plan.item.Languages),
		RepoID:    manifest.RepoID, Revision: manifest.Revision,
		VariantID: manifest.VariantID, VariantName: plan.variant.Name,
		Quantization: plan.variant.Quantization, Adapter: manifest.Adapter,
		Status:          contract.PrivacyModelStatusReady,
		BytesDownloaded: plan.variant.BytesTotal, BytesTotal: plan.variant.BytesTotal,
		EstimatedRAMBytes: plan.variant.EstimatedRAMBytes,
		LabelMapping:      cloneLabelMapping(manifest.LabelMapping), InstalledAt: &now,
	}
	if !manifestMatchesInstallation(manifest, installation) ||
		validateStagedCompatibility(
			registry.installationDirectory(installation.ID),
			installation,
			plan.runtime,
		) != nil {
		return nil
	}
	registry.installations[installation.ID] = cloneInstallation(installation)
	registry.bindings[installation.ID] = manifestBinding(
		manifest.Identity,
		manifestDocument,
	)
	return registry.persistLocked(context.Background(), installation)
}

func (registry *Registry) persistLocked(
	ctx context.Context,
	installation contract.PrivacyModelInstallation,
) error {
	if registry.store == nil {
		return nil
	}
	if !validInstallationProvenance(installation) {
		return fmt.Errorf(
			"%w: privacy model installation provenance is invalid",
			storage.ErrInvalidArgument,
		)
	}
	record := storage.PrivacyModelInstallationRecord{
		Installation: cloneInstallation(installation),
	}
	if binding, exists := registry.bindings[installation.ID]; exists {
		cloned := cloneManifestBinding(binding)
		record.Manifest = &cloned
	}
	return registry.store.PutPrivacyModelInstallation(ctx, record)
}

func validInstallationProvenance(
	installation contract.PrivacyModelInstallation,
) bool {
	plan, builtin := builtinVariantPlan(
		installation.RepoID,
		installation.Revision,
		installation.VariantID,
	)
	if installation.Source == contract.PrivacyModelSourceCustom {
		return installation.CatalogID == nil &&
			installation.CatalogSource == nil &&
			!builtin
	}
	if installation.Source != contract.PrivacyModelSourceCatalog ||
		installation.CatalogID == nil ||
		installation.CatalogSource == nil ||
		!builtin {
		return false
	}
	return *installation.CatalogID == plan.item.ID &&
		*installation.CatalogSource == plan.item.Source &&
		installation.Name == plan.item.Name &&
		optionalStringEquals(installation.License, plan.item.License) &&
		equalStrings(installation.Languages, plan.item.Languages) &&
		installation.VariantName == plan.variant.Name &&
		installation.Quantization == plan.variant.Quantization &&
		installation.Adapter == plan.item.Adapter &&
		installation.BytesTotal == plan.variant.BytesTotal &&
		installation.EstimatedRAMBytes == plan.variant.EstimatedRAMBytes
}

func (registry *Registry) installationDirectory(
	id contract.PrivacyModelID,
) string {
	return filepath.Join(registry.installationsDir, string(id))
}

func (registry *Registry) removeAbandonedStaging() error {
	entries, err := os.ReadDir(registry.stagingDir)
	if err != nil {
		return ErrFilesystem
	}
	now := time.Now()
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".privacy-model-download-model_") {
			continue
		}
		candidate := filepath.Join(registry.stagingDir, entry.Name())
		modified, err := newestTreeModification(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return ErrFilesystem
		}
		if now.Sub(modified) >= abandonedDownloadStaleAge &&
			os.RemoveAll(candidate) != nil {
			return ErrFilesystem
		}
	}
	return nil
}

func (registry *Registry) removeStagingFor(
	id contract.PrivacyModelID,
) error {
	entries, err := os.ReadDir(registry.stagingDir)
	if err != nil {
		return ErrFilesystem
	}
	prefix := ".privacy-model-download-" + string(id) + "-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			if err := os.RemoveAll(
				filepath.Join(registry.stagingDir, entry.Name()),
			); err != nil {
				return ErrFilesystem
			}
		}
	}
	return nil
}

func cloneInstallation(
	installation contract.PrivacyModelInstallation,
) contract.PrivacyModelInstallation {
	if installation.CatalogID != nil {
		value := *installation.CatalogID
		installation.CatalogID = &value
	}
	if installation.CatalogSource != nil {
		value := *installation.CatalogSource
		installation.CatalogSource = &value
	}
	installation.License = cloneString(installation.License)
	installation.Languages = cloneStrings(installation.Languages)
	if installation.Error != nil {
		value := *installation.Error
		installation.Error = &value
	}
	installation.InstalledAt = cloneString(installation.InstalledAt)
	installation.LabelMapping = cloneLabelMapping(installation.LabelMapping)
	return installation
}

func cloneStrings(values []string) []string {
	result := make([]string, len(values))
	copy(result, values)
	return result
}

func optionalStringEquals(value *string, expected string) bool {
	return value != nil && *value == expected
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

var _ interface {
	ReadyInstallation(contract.PrivacyModelID) (contract.ReadyPrivacyModelInstallation, bool)
} = (*Registry)(nil)
