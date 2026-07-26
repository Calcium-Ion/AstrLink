package privacymodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	defaultHuggingFaceURL     = "https://huggingface.co/"
	noRemoteModelsEnvironment = "ASTRLINK_CI_NO_REMOTE_MODELS"
	maxMetadataBytes          = 4 << 20
	maxConfigBytes            = 2 << 20
	maxProbeJSONAssetBytes    = 64 << 20
)

var (
	externalDataSuffixPattern = regexp.MustCompile(`^_data(?:_[0-9]+)?$`)
	entityLabelPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

type hfProbe struct {
	baseURL *url.URL
	client  HTTPClient
}

type hfModelMetadata struct {
	ID       string          `json:"id"`
	ModelID  string          `json:"modelId"`
	SHA      string          `json:"sha"`
	CardData json.RawMessage `json:"cardData"`
	Siblings []hfSibling     `json:"siblings"`
}

type hfSibling struct {
	Filename string `json:"rfilename"`
	Size     int64  `json:"size"`
	LFS      *struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"lfs"`
}

type hfCardData struct {
	License  string          `json:"license"`
	Language json.RawMessage `json:"language"`
}

type hfModelConfig struct {
	Architectures []string          `json:"architectures"`
	ID2Label      map[string]string `json:"id2label"`
	ModelType     string            `json:"model_type"`
	TypeVocabSize *int              `json:"type_vocab_size"`
}

type sourceDescriptor struct {
	Version   int                          `json:"version"`
	Name      string                       `json:"name"`
	License   string                       `json:"license"`
	Languages []string                     `json:"languages"`
	Adapter   contract.PrivacyModelAdapter `json:"adapter"`
	Variants  []sourceDescriptorVariant    `json:"variants"`
}

type sourceDescriptorVariant struct {
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	Quantization      string               `json:"quantization"`
	EstimatedRAMBytes int64                `json:"estimated_ram_bytes"`
	Recommended       bool                 `json:"recommended"`
	ModelPath         string               `json:"model_path"`
	ExternalData      []string             `json:"external_data_paths"`
	TokenizerPath     string               `json:"tokenizer_path"`
	ConfigPath        string               `json:"config_path"`
	CalibrationPath   *string              `json:"calibration_path"`
	TagScheme         string               `json:"tag_scheme"`
	Window            int                  `json:"window"`
	Stride            int                  `json:"stride"`
	MaxRequestTokens  int                  `json:"max_request_tokens"`
	InputNames        normalizedInputNames `json:"input_names"`
	OutputName        string               `json:"output_name"`
}

type probeResult struct {
	response contract.PrivacyModelProbeResponse
	plans    map[string]customVariantPlan
}

type customVariantPlan struct {
	variant contract.PrivacyModelVariant
	assets  []Asset
	runtime runtimeSpec
}

func newHFProbe(
	baseURL string,
	client HTTPClient,
	testOnlyLoopbackMode bool,
) (*hfProbe, error) {
	remoteDisabled := os.Getenv(noRemoteModelsEnvironment) == "1"
	if testOnlyLoopbackMode {
		if baseURL == "" || client == nil {
			return nil, ErrInvalidConfig
		}
	} else {
		if remoteDisabled ||
			(baseURL != "" && baseURL != defaultHuggingFaceURL) {
			return nil, ErrInvalidConfig
		}
	}
	if baseURL == "" {
		baseURL = defaultHuggingFaceURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrInvalidConfig
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	if testOnlyLoopbackMode && !isLoopbackHostname(parsed.Hostname()) {
		return nil, ErrInvalidConfig
	}
	if client == nil {
		client = &http.Client{Timeout: downloadTimeout}
	}
	if testOnlyLoopbackMode {
		httpClient, ok := client.(*http.Client)
		if !ok {
			return nil, ErrInvalidConfig
		}
		cloned := *httpClient
		previousRedirectCheck := cloned.CheckRedirect
		cloned.CheckRedirect = func(
			request *http.Request,
			via []*http.Request,
		) error {
			if request == nil || request.URL == nil ||
				!isLoopbackHostname(request.URL.Hostname()) {
				return ErrRemoteMetadata
			}
			if previousRedirectCheck != nil {
				return previousRedirectCheck(request, via)
			}
			return nil
		}
		client = loopbackOnlyHTTPClient{delegate: &cloned}
	}
	return &hfProbe{baseURL: parsed, client: client}, nil
}

type loopbackOnlyHTTPClient struct {
	delegate HTTPClient
}

func (client loopbackOnlyHTTPClient) Do(
	request *http.Request,
) (*http.Response, error) {
	if request == nil || request.URL == nil ||
		!isLoopbackHostname(request.URL.Hostname()) {
		return nil, ErrRemoteMetadata
	}
	response, err := client.delegate.Do(request)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Request == nil ||
		response.Request.URL == nil ||
		!isLoopbackHostname(response.Request.URL.Hostname()) {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, ErrRemoteMetadata
	}
	return response, nil
}

func isLoopbackHostname(hostname string) bool {
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	address := net.ParseIP(hostname)
	return address != nil && address.IsLoopback()
}

func (probe *hfProbe) inspect(
	ctx context.Context,
	request contract.PrivacyModelProbeRequest,
) (probeResult, error) {
	if err := contract.ValidatePrivacyModelRepoID(request.RepoID); err != nil {
		return probeResult{}, ErrInvalidConfig
	}
	if err := contract.ValidateRequestedPrivacyModelRevision(request.Revision); err != nil {
		return probeResult{}, ErrInvalidConfig
	}
	metadataURL := probe.metadataEndpoint(request.RepoID, request.Revision)
	metadataURL += "?blobs=true"
	document, err := probe.getJSON(ctx, metadataURL, maxMetadataBytes)
	if err != nil {
		return probeResult{}, err
	}
	var metadata hfModelMetadata
	if err := json.Unmarshal(document, &metadata); err != nil {
		return probeResult{}, ErrRemoteMetadata
	}
	if err := contract.ValidatePrivacyModelRevision(metadata.SHA); err != nil {
		return probeResult{}, ErrRemoteMetadata
	}
	repository := metadata.ModelID
	if repository == "" {
		repository = metadata.ID
	}
	if repository != request.RepoID {
		return probeResult{}, ErrRemoteMetadata
	}
	card := hfCardData{}
	if len(metadata.CardData) > 0 && string(metadata.CardData) != "null" {
		if err := json.Unmarshal(metadata.CardData, &card); err != nil {
			return probeResult{}, ErrRemoteMetadata
		}
	}
	if card.License != "" && !validProbeMetadataText(card.License, 64) {
		card.License = ""
	}
	var descriptor *sourceDescriptor
	if siblingExists(metadata.Siblings, InstallationManifestName) {
		descriptorDocument, err := probe.getJSON(
			ctx,
			probe.endpoint(request.RepoID, "resolve", metadata.SHA, InstallationManifestName),
			maxInstallationManifestBytes,
		)
		if err != nil {
			return probeResult{}, err
		}
		var parsed sourceDescriptor
		if err := strictDecodeJSON(descriptorDocument, &parsed); err != nil ||
			validateSourceDescriptor(parsed) != nil {
			return probeResult{}, ErrUnsupportedModel
		}
		descriptor = &parsed
	}
	requiredJSON := map[string]struct{}{"config.json": {}, "tokenizer.json": {}}
	configPath := "config.json"
	if descriptor != nil {
		requiredJSON = make(map[string]struct{})
		configPath = descriptor.Variants[0].ConfigPath
		for _, variant := range descriptor.Variants {
			requiredJSON[variant.ConfigPath] = struct{}{}
			requiredJSON[variant.TokenizerPath] = struct{}{}
			if variant.CalibrationPath != nil {
				requiredJSON[*variant.CalibrationPath] = struct{}{}
			}
		}
	}
	jsonDocuments := make(map[string][]byte, len(requiredJSON))
	for filename := range requiredJSON {
		if !strings.HasSuffix(strings.ToLower(filename), ".json") ||
			!siblingExists(metadata.Siblings, filename) {
			return probeResult{}, ErrUnsupportedModel
		}
		document, err := probe.getJSON(
			ctx,
			probe.endpoint(request.RepoID, "resolve", metadata.SHA, filename),
			maxProbeJSONAssetBytes,
		)
		if err != nil {
			return probeResult{}, err
		}
		jsonDocuments[filename] = document
		if !pinSibling(&metadata, filename, document) {
			return probeResult{}, ErrRemoteMetadata
		}
	}
	configDocument := jsonDocuments[configPath]
	if len(configDocument) == 0 || len(configDocument) > maxConfigBytes {
		return probeResult{}, ErrRemoteMetadata
	}
	var modelConfig hfModelConfig
	if err := json.Unmarshal(configDocument, &modelConfig); err != nil {
		return probeResult{}, ErrRemoteMetadata
	}
	if prohibitedModelConfig(modelConfig) {
		return probeResult{}, ErrUnsupportedModel
	}
	if descriptor == nil &&
		!hasTokenClassificationArchitecture(modelConfig.Architectures) {
		return probeResult{}, ErrUnsupportedModel
	}
	labels, tagScheme, complete, validLabels := probeLabels(modelConfig.ID2Label)
	if !validLabels {
		return probeResult{}, ErrRemoteMetadata
	}
	var variants []contract.PrivacyModelVariant
	var plans map[string]customVariantPlan
	if descriptor == nil {
		variants, plans = probeVariants(
			metadata.Siblings,
			tagScheme,
			usesTokenTypeIDs(modelConfig),
		)
	} else {
		variants, plans = descriptorVariants(metadata.Siblings, *descriptor)
	}
	if len(variants) == 0 {
		return probeResult{}, ErrUnsupportedModel
	}
	name := path.Base(request.RepoID)
	response := contract.PrivacyModelProbeResponse{
		RepoID: request.RepoID, RequestedRevision: request.Revision,
		Revision: metadata.SHA, Name: name, License: optionalNonEmpty(card.License),
		Languages: parseLanguages(card.Language),
		Adapter:   contract.PrivacyModelAdapterHFToken,
		Variants:  variants, Labels: labels,
		RequiresLabelMapping: !complete,
	}
	if descriptor != nil {
		response.Name = descriptor.Name
		response.License = optionalNonEmpty(descriptor.License)
		response.Languages = cloneStrings(descriptor.Languages)
		response.Adapter = descriptor.Adapter
	}
	if contract.ValidatePrivacyModelProbeResponse(response) != nil {
		return probeResult{}, ErrRemoteMetadata
	}
	return probeResult{response: response, plans: plans}, nil
}

func optionalNonEmpty(value string) *string {
	if value == "" {
		return nil
	}
	result := value
	return &result
}

func siblingExists(siblings []hfSibling, filename string) bool {
	for _, sibling := range siblings {
		if sibling.Filename == filename {
			return true
		}
	}
	return false
}

func pinSibling(metadata *hfModelMetadata, filename string, document []byte) bool {
	digest := sha256Hex(document)
	for index := range metadata.Siblings {
		sibling := &metadata.Siblings[index]
		if sibling.Filename != filename {
			continue
		}
		if sibling.Size > 0 && sibling.Size != int64(len(document)) {
			return false
		}
		if sibling.LFS != nil && sibling.LFS.SHA256 != "" &&
			sibling.LFS.SHA256 != digest {
			return false
		}
		sibling.Size = int64(len(document))
		if sibling.LFS == nil {
			sibling.LFS = &struct {
				SHA256 string `json:"sha256"`
				Size   int64  `json:"size"`
			}{}
		}
		sibling.LFS.SHA256 = digest
		sibling.LFS.Size = int64(len(document))
		return true
	}
	return false
}

func validateSourceDescriptor(descriptor sourceDescriptor) error {
	if descriptor.Version != 1 ||
		!validProbeMetadataText(descriptor.Name, 128) ||
		(descriptor.License != "" &&
			!validProbeMetadataText(descriptor.License, 64)) ||
		!descriptor.Adapter.Valid() ||
		len(descriptor.Languages) > 32 ||
		len(descriptor.Variants) == 0 || len(descriptor.Variants) > 16 {
		return ErrUnsupportedModel
	}
	languages := make(map[string]struct{}, len(descriptor.Languages))
	for _, language := range descriptor.Languages {
		if !validProbeMetadataText(language, 64) {
			return ErrUnsupportedModel
		}
		if _, duplicate := languages[language]; duplicate {
			return ErrUnsupportedModel
		}
		languages[language] = struct{}{}
	}
	seen := make(map[string]struct{}, len(descriptor.Variants))
	sharedConfig := descriptor.Variants[0].ConfigPath
	sharedTokenizer := descriptor.Variants[0].TokenizerPath
	sharedTagScheme := descriptor.Variants[0].TagScheme
	for _, variant := range descriptor.Variants {
		if contract.ValidatePrivacyModelVariantID(variant.ID) != nil ||
			!validProbeMetadataText(variant.Name, 64) ||
			(variant.Quantization != "q4" &&
				variant.Quantization != "int8" &&
				variant.Quantization != "fp32" &&
				variant.Quantization != "f16" &&
				variant.Quantization != "q4f16") ||
			variant.EstimatedRAMBytes < 0 ||
			variant.EstimatedRAMBytes > contract.MaxPrivacyModelByteCount ||
			len(variant.ExternalData) > maxDescriptorExternalFiles(variant) ||
			!safeAssetPath(variant.ModelPath) ||
			!strings.HasSuffix(strings.ToLower(variant.ModelPath), ".onnx") ||
			!safeAssetPath(variant.TokenizerPath) ||
			!strings.HasSuffix(strings.ToLower(variant.TokenizerPath), ".json") ||
			!safeAssetPath(variant.ConfigPath) ||
			!strings.HasSuffix(strings.ToLower(variant.ConfigPath), ".json") ||
			variant.ConfigPath != sharedConfig ||
			variant.TokenizerPath != sharedTokenizer ||
			variant.TagScheme != sharedTagScheme ||
			(variant.TagScheme != "bio" && variant.TagScheme != "bioes") ||
			variant.Window <= 0 || variant.Stride < 0 ||
			variant.Stride >= variant.Window ||
			variant.Window > variant.MaxRequestTokens ||
			variant.MaxRequestTokens > 131_072 ||
			!tensorNamePattern.MatchString(variant.InputNames.InputIDs) ||
			!tensorNamePattern.MatchString(variant.InputNames.AttentionMask) ||
			!tensorNamePattern.MatchString(variant.OutputName) {
			return ErrUnsupportedModel
		}
		if variant.InputNames.InputIDs == variant.InputNames.AttentionMask ||
			variant.OutputName == variant.InputNames.InputIDs ||
			variant.OutputName == variant.InputNames.AttentionMask {
			return ErrUnsupportedModel
		}
		if variant.InputNames.TokenTypeIDs != nil &&
			(!tensorNamePattern.MatchString(*variant.InputNames.TokenTypeIDs) ||
				*variant.InputNames.TokenTypeIDs == variant.InputNames.InputIDs ||
				*variant.InputNames.TokenTypeIDs == variant.InputNames.AttentionMask ||
				*variant.InputNames.TokenTypeIDs == variant.OutputName) {
			return ErrUnsupportedModel
		}
		if descriptor.Adapter == contract.PrivacyModelAdapterOpenAIBIOES &&
			(variant.TagScheme != "bioes" || variant.CalibrationPath == nil) {
			return ErrUnsupportedModel
		}
		if descriptor.Adapter == contract.PrivacyModelAdapterHFToken &&
			variant.CalibrationPath != nil {
			return ErrUnsupportedModel
		}
		if _, exists := seen[variant.ID]; exists {
			return ErrUnsupportedModel
		}
		seen[variant.ID] = struct{}{}
		rolePaths := map[string]struct{}{
			variant.ModelPath:     {},
			variant.TokenizerPath: {},
			variant.ConfigPath:    {},
		}
		if len(rolePaths) != 3 ||
			variant.ModelPath == InstallationManifestName ||
			variant.TokenizerPath == InstallationManifestName ||
			variant.ConfigPath == InstallationManifestName {
			return ErrUnsupportedModel
		}
		for _, filename := range variant.ExternalData {
			suffix := strings.TrimPrefix(filename, variant.ModelPath)
			if !safeAssetPath(filename) ||
				!externalDataSuffixPattern.MatchString(suffix) {
				return ErrUnsupportedModel
			}
			if _, exists := rolePaths[filename]; exists {
				return ErrUnsupportedModel
			}
			rolePaths[filename] = struct{}{}
		}
		if variant.CalibrationPath != nil &&
			(!safeAssetPath(*variant.CalibrationPath) ||
				!strings.HasSuffix(
					strings.ToLower(*variant.CalibrationPath),
					".json",
				)) {
			return ErrUnsupportedModel
		}
		if variant.CalibrationPath != nil {
			if _, exists := rolePaths[*variant.CalibrationPath]; exists ||
				*variant.CalibrationPath == InstallationManifestName {
				return ErrUnsupportedModel
			}
		}
	}
	return nil
}

func maxDescriptorExternalFiles(variant sourceDescriptorVariant) int {
	if variant.CalibrationPath != nil {
		return 124
	}
	return 125
}

func hasTokenClassificationArchitecture(architectures []string) bool {
	if len(architectures) == 0 || len(architectures) > 16 {
		return false
	}
	for _, architecture := range architectures {
		if strings.HasSuffix(architecture, "ForTokenClassification") &&
			len(architecture) <= 128 {
			return true
		}
	}
	return false
}

func prohibitedModelConfig(config hfModelConfig) bool {
	if strings.Contains(strings.ToLower(config.ModelType), "gliner") {
		return true
	}
	for _, architecture := range config.Architectures {
		if strings.Contains(strings.ToLower(architecture), "gliner") {
			return true
		}
	}
	return false
}

func descriptorVariants(
	siblings []hfSibling,
	descriptor sourceDescriptor,
) ([]contract.PrivacyModelVariant, map[string]customVariantPlan) {
	byPath := make(map[string]hfSibling, len(siblings))
	for _, sibling := range siblings {
		if sibling.Size == 0 && sibling.LFS != nil {
			sibling.Size = sibling.LFS.Size
		}
		if safeAssetPath(sibling.Filename) && sibling.Size > 0 {
			byPath[sibling.Filename] = sibling
		}
	}
	variants := make([]contract.PrivacyModelVariant, 0, len(descriptor.Variants))
	plans := make(map[string]customVariantPlan, len(descriptor.Variants))
	for _, source := range descriptor.Variants {
		required := []string{
			source.ModelPath, source.TokenizerPath, source.ConfigPath,
		}
		required = append(required, source.ExternalData...)
		if source.CalibrationPath != nil {
			required = append(required, *source.CalibrationPath)
		}
		assets := make([]Asset, 0, len(required))
		var total int64
		supported := !nonCPUModelPath(source.ModelPath) &&
			!strings.Contains(strings.ToLower(source.Quantization), "f16") &&
			!strings.Contains(strings.ToLower(source.ID), "gpu")
		for _, filename := range required {
			sibling, exists := byPath[filename]
			if !exists {
				supported = false
				continue
			}
			asset := assetFromSibling(sibling)
			if !validPinnedAsset(asset) {
				supported = false
			}
			assets = append(assets, asset)
			if asset.Size > contract.MaxPrivacyModelByteCount ||
				total > contract.MaxPrivacyModelByteCount-asset.Size {
				supported = false
			} else {
				total += asset.Size
			}
		}
		var reason *string
		if !supported {
			value := "cpu_only"
			reason = &value
		}
		variant := contract.PrivacyModelVariant{
			ID: source.ID, Name: source.Name,
			Quantization: source.Quantization, BytesTotal: total,
			EstimatedRAMBytes: source.EstimatedRAMBytes,
			Recommended:       source.Recommended, Supported: supported,
			UnsupportedReason: reason,
		}
		runtime := runtimeSpec{
			modelPath:     source.ModelPath,
			externalData:  append([]string(nil), source.ExternalData...),
			tokenizerPath: source.TokenizerPath, configPath: source.ConfigPath,
			calibrationPath: cloneString(source.CalibrationPath),
			tagScheme:       source.TagScheme, window: source.Window,
			stride: source.Stride, maxRequestTokens: source.MaxRequestTokens,
			inputNames: source.InputNames, outputName: source.OutputName,
		}
		variants = append(variants, variant)
		plans[source.ID] = customVariantPlan{
			variant: variant, assets: assets, runtime: runtime,
		}
	}
	return variants, plans
}

func (probe *hfProbe) endpoint(segments ...string) string {
	parts := make([]endpointPart, len(segments))
	for index, segment := range segments {
		parts[index] = endpointPart{value: segment, slashSeparated: true}
	}
	return probe.buildEndpoint(parts...)
}

type endpointPart struct {
	value          string
	slashSeparated bool
}

func (probe *hfProbe) metadataEndpoint(repoID, revision string) string {
	return probe.buildEndpoint(
		endpointPart{value: "api"},
		endpointPart{value: "models"},
		endpointPart{value: repoID, slashSeparated: true},
		endpointPart{value: "revision"},
		endpointPart{value: revision},
	)
}

func (probe *hfProbe) buildEndpoint(parts ...endpointPart) string {
	copyOfURL := *probe.baseURL
	rawPath := strings.TrimSuffix(copyOfURL.EscapedPath(), "/")
	for _, part := range parts {
		values := []string{part.value}
		if part.slashSeparated {
			values = strings.Split(part.value, "/")
		}
		for _, value := range values {
			rawPath += "/" + url.PathEscape(value)
		}
	}
	if !strings.HasPrefix(rawPath, "/") {
		rawPath = "/" + rawPath
	}
	copyOfURL.RawPath = rawPath
	decoded, _ := url.PathUnescape(copyOfURL.RawPath)
	copyOfURL.Path = decoded
	return copyOfURL.String()
}

func (probe *hfProbe) getJSON(
	ctx context.Context,
	endpoint string,
	limit int64,
) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ErrRemoteMetadata
	}
	request.Header.Set("Accept", "application/json")
	response, err := probe.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrRemoteMetadata
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		response.ContentLength > limit {
		return nil, ErrRemoteMetadata
	}
	document, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(document)) > limit || len(document) == 0 {
		return nil, ErrRemoteMetadata
	}
	return document, nil
}

func probeVariants(
	siblings []hfSibling,
	tagScheme string,
	tokenTypeIDs bool,
) ([]contract.PrivacyModelVariant, map[string]customVariantPlan) {
	byPath := make(map[string]hfSibling, len(siblings))
	for _, sibling := range siblings {
		if !safeAssetPath(sibling.Filename) || sibling.Size < 0 {
			continue
		}
		if sibling.LFS != nil {
			if sibling.Size == 0 {
				sibling.Size = sibling.LFS.Size
			}
		}
		byPath[sibling.Filename] = sibling
	}
	config, hasConfig := byPath["config.json"]
	tokenizer, hasTokenizer := byPath["tokenizer.json"]
	if !hasConfig || !hasTokenizer {
		return nil, nil
	}
	modelPaths := make([]string, 0)
	for filename := range byPath {
		if strings.HasSuffix(strings.ToLower(filename), ".onnx") {
			modelPaths = append(modelPaths, filename)
		}
	}
	if len(modelPaths) == 0 || len(modelPaths) > 32 {
		return nil, nil
	}
	sort.Strings(modelPaths)
	variants := make([]contract.PrivacyModelVariant, 0, len(modelPaths))
	plans := make(map[string]customVariantPlan)
	usedIDs := make(map[string]int)
	for _, modelPath := range modelPaths {
		model := byPath[modelPath]
		variantID, quantization := variantIdentity(modelPath)
		cpuSupported := !nonCPUModelPath(modelPath)
		baseVariantID := variantID
		usedIDs[baseVariantID]++
		if usedIDs[baseVariantID] > 1 {
			variantID += "_" + strconv.Itoa(usedIDs[baseVariantID])
		}
		assets := []Asset{
			assetFromSibling(model),
			assetFromSibling(config),
			assetFromSibling(tokenizer),
		}
		externalPaths := externalDataPaths(byPath, modelPath)
		tooManyAssets := len(externalPaths) > 125
		for _, externalPath := range externalPaths {
			assets = append(assets, assetFromSibling(byPath[externalPath]))
		}
		var total int64
		supported := cpuSupported && !tooManyAssets
		for _, asset := range assets {
			if !validPinnedAsset(asset) {
				supported = false
			}
			if asset.Size > contract.MaxPrivacyModelByteCount ||
				total > contract.MaxPrivacyModelByteCount-asset.Size {
				supported = false
			} else {
				total += asset.Size
			}
		}
		var reason *string
		if !supported {
			value := "cpu_only"
			reason = &value
		}
		variant := contract.PrivacyModelVariant{
			ID: variantID, Name: strings.ToUpper(strings.ReplaceAll(variantID, "_", " ")),
			Quantization: quantization, BytesTotal: total,
			EstimatedRAMBytes: estimateRAM(total, quantization),
			Recommended:       quantization == "int8", Supported: supported,
			UnsupportedReason: reason,
		}
		runtime := hfRuntime(modelPath)
		runtime.tagScheme = tagScheme
		if tokenTypeIDs {
			name := "token_type_ids"
			runtime.inputNames.TokenTypeIDs = &name
		}
		runtime.externalData = append([]string(nil), externalPaths...)
		variants = append(variants, variant)
		plans[variantID] = customVariantPlan{
			variant: variant, assets: assets, runtime: runtime,
		}
	}
	return variants, plans
}

func usesTokenTypeIDs(config hfModelConfig) bool {
	return config.TypeVocabSize != nil && *config.TypeVocabSize > 1
}

func assetFromSibling(sibling hfSibling) Asset {
	digest := ""
	if sibling.LFS != nil {
		digest = sibling.LFS.SHA256
	}
	return Asset{Path: sibling.Filename, Size: sibling.Size, SHA256: digest}
}

func validPinnedAsset(asset Asset) bool {
	if asset.Size <= 0 || len(asset.SHA256) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(asset.SHA256)
	return err == nil &&
		len(decoded) == sha256.Size &&
		hex.EncodeToString(decoded) == asset.SHA256
}

func variantIdentity(modelPath string) (string, string) {
	lower := strings.ToLower(modelPath)
	switch {
	case strings.Contains(lower, "q4f16"), strings.Contains(lower, "q4_f16"):
		return "gpu_q4f16", "q4f16"
	case strings.Contains(lower, "float16"), strings.Contains(lower, "fp16"),
		strings.Contains(lower, "f16"):
		return "gpu_f16", "f16"
	case strings.Contains(lower, "q4"):
		return "cpu_q4", "q4"
	case strings.Contains(lower, "int8"), strings.Contains(lower, "quant"):
		if strings.Contains(lower, "edge") {
			return "edge_int8", "int8"
		}
		return "cpu_int8", "int8"
	default:
		return "cpu_fp32", "fp32"
	}
}

func nonCPUModelPath(modelPath string) bool {
	lower := strings.ToLower(modelPath)
	for _, marker := range []string{
		"gpu", "cuda", "tensorrt", "directml", "float16", "fp16", "f16",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func externalDataPaths(
	siblings map[string]hfSibling,
	modelPath string,
) []string {
	result := make([]string, 0)
	for filename := range siblings {
		suffix := strings.TrimPrefix(filename, modelPath)
		if suffix != filename && externalDataSuffixPattern.MatchString(suffix) {
			result = append(result, filename)
		}
	}
	sort.Strings(result)
	return result
}

func estimateRAM(bytes int64, quantization string) int64 {
	multiplier := int64(2)
	if quantization == "fp32" {
		multiplier = 3
	}
	if bytes > contract.MaxPrivacyModelByteCount/multiplier {
		return contract.MaxPrivacyModelByteCount
	}
	estimate := bytes * multiplier
	const minimum = int64(256 << 20)
	if estimate < minimum {
		return minimum
	}
	return estimate
}

func probeLabels(
	id2label map[string]string,
) ([]contract.PrivacyModelLabel, string, bool, bool) {
	if len(id2label) == 0 || len(id2label) > 256 {
		return nil, "", false, false
	}
	type prefixSet [4]bool
	unique := make(map[string]prefixSet)
	outside := 0
	tagScheme := "bio"
	for index := 0; index < len(id2label); index++ {
		raw, exists := id2label[strconv.Itoa(index)]
		if !exists {
			return nil, "", false, false
		}
		if raw == "O" {
			outside++
			continue
		}
		label, prefix, valid := strictEntityTag(raw)
		if !valid {
			return nil, "", false, false
		}
		offset := strings.IndexByte("BIES", prefix[0])
		set := unique[label]
		if offset < 0 || set[offset] {
			return nil, "", false, false
		}
		set[offset] = true
		unique[label] = set
		if prefix == "E" || prefix == "S" {
			tagScheme = "bioes"
		}
	}
	if outside != 1 || len(unique) == 0 {
		return nil, "", false, false
	}
	expected := prefixSet{true, true, false, false}
	if tagScheme == "bioes" {
		expected = prefixSet{true, true, true, true}
	}
	for _, present := range unique {
		if present != expected {
			return nil, "", false, false
		}
	}
	names := make([]string, 0, len(unique))
	for label := range unique {
		names = append(names, label)
	}
	sort.Strings(names)
	labels := make([]contract.PrivacyModelLabel, 0, len(names))
	complete := true
	for _, label := range names {
		suggested := suggestedCanonicalKind(label)
		if suggested == nil {
			complete = false
		}
		labels = append(labels, contract.PrivacyModelLabel{
			Label: label, SuggestedKind: suggested,
		})
	}
	return labels, tagScheme, complete, true
}

func baseEntityLabel(raw string) (string, string) {
	label := strings.TrimSpace(raw)
	if strings.EqualFold(label, "O") || label == "" {
		return "", ""
	}
	upper := strings.ToUpper(label)
	for _, prefix := range []string{"B-", "I-", "E-", "S-", "B_", "I_", "E_", "S_"} {
		if strings.HasPrefix(upper, prefix) {
			return strings.TrimSpace(label[2:]), prefix[:1]
		}
	}
	return label, ""
}

func strictEntityTag(raw string) (string, string, bool) {
	if len(raw) < 3 || (raw[1] != '-' && raw[1] != '_') {
		return "", "", false
	}
	prefix := raw[:1]
	if prefix != "B" && prefix != "I" &&
		prefix != "E" && prefix != "S" {
		return "", "", false
	}
	label := raw[2:]
	if !entityLabelPattern.MatchString(label) {
		return "", "", false
	}
	return label, prefix, true
}

func suggestedCanonicalKind(label string) *contract.CanonicalKind {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(label), "-", "_"))
	aliases := map[string]contract.CanonicalKind{
		"EMAIL": contract.CanonicalKindEmail, "EMAIL_ADDRESS": contract.CanonicalKindEmail,
		"PRIVATE_EMAIL": contract.CanonicalKindEmail,
		"PHONE":         contract.CanonicalKindPhone,
		"TELEPHONE":     contract.CanonicalKindPhone,
		"PRIVATE_PHONE": contract.CanonicalKindPhone,
		"MOBILE":        contract.CanonicalKindPhone,
		"ACCOUNT":       contract.CanonicalKindAccount, "ACCOUNT_NUMBER": contract.CanonicalKindAccount,
		"IBAN": contract.CanonicalKindAccount, "BANK_ACCOUNT": contract.CanonicalKindAccount,
		"CREDIT_CARD": contract.CanonicalKindPaymentCard, "CARD_NUMBER": contract.CanonicalKindPaymentCard,
		"IP": contract.CanonicalKindIPAddress, "IPV4": contract.CanonicalKindIPAddress,
		"IPV6": contract.CanonicalKindIPAddress, "URL": contract.CanonicalKindURL,
		"PRIVATE_URL": contract.CanonicalKindURL,
		"API_KEY":     contract.CanonicalKindCommonSecret, "PASSWORD": contract.CanonicalKindCommonSecret,
		"SECRET": contract.CanonicalKindCommonSecret, "TOKEN": contract.CanonicalKindCommonSecret,
		"ADDRESS": contract.CanonicalKindAddress, "STREET_ADDRESS": contract.CanonicalKindAddress,
		"PRIVATE_ADDRESS": contract.CanonicalKindAddress,
		"DATE_OF_BIRTH":   contract.CanonicalKindDate,
		"DOB":             contract.CanonicalKindDate,
		"PRIVATE_DATE":    contract.CanonicalKindDate,
		"PERSON":          contract.CanonicalKindPerson,
		"NAME":            contract.CanonicalKindPerson,
		"PRIVATE_PERSON":  contract.CanonicalKindPerson,
		"FIRST_NAME":      contract.CanonicalKindPerson, "LAST_NAME": contract.CanonicalKindPerson,
		"GIVEN_NAME": contract.CanonicalKindPerson, "SURNAME": contract.CanonicalKindPerson,
	}
	kind, exists := aliases[normalized]
	if !exists {
		return nil
	}
	result := kind
	return &result
}

func parseLanguages(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return []string{}
	}
	var single string
	if json.Unmarshal(raw, &single) == nil &&
		validProbeMetadataText(single, 64) {
		return []string{single}
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return []string{}
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		if !validProbeMetadataText(value, 64) {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == 32 {
			break
		}
	}
	sort.Strings(result)
	return result
}

func validProbeMetadataText(value string, maxRunes int) bool {
	return value != "" &&
		utf8.ValidString(value) &&
		utf8.RuneCountInString(value) <= maxRunes &&
		strings.TrimSpace(value) == value &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

var (
	ErrRemoteMetadata   = errors.New("privacy model metadata is unavailable")
	ErrUnsupportedModel = errors.New("privacy model is unsupported")
)

func sha256Hex(document []byte) string {
	sum := sha256.Sum256(document)
	return hex.EncodeToString(sum[:])
}
