package privacymodel

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	astrLinkGuardRepoID  = "QuantumNous/astrlink-guard"
	astrLinkGuardLicense = "LICENSE"

	// astrLinkGuardFP16Revision is the commit of tag v0.1.3, the first
	// release with an fp16/ folder. While it is not a valid commit the pin
	// stays at 0.1.2, which has no FP16 build.
	//
	// TODO(v0.1.3 upload): replace "" with the 40-character commit sha of
	// tag v0.1.3 on QuantumNous/astrlink-guard.
	astrLinkGuardFP16Revision = ""

	// 0.1.3 keeps the int8/ and fp32/ files of 0.1.2 and adds fp16/.
	astrLinkGuardCPURevision = "33dfcc586f8a50529e98ce5fe2ba46836dbeab8f"
	astrLinkGuardCPUVersion  = "0.1.2"
	astrLinkGuardFP16Version = "0.1.3"

	// The Hub counts a download only for requests to the repository's root
	// config.json, which the variant folders never match. This is the first
	// commit that has one.
	astrLinkGuardCountRevision = "5fb09dd8831224273c8db11bb0ed4b0890bd42e9"
	astrLinkGuardCountTimeout  = 10 * time.Second

	astrLinkGuardDecoderContractField = "astrlink_guard_decoder_contract"
	astrLinkGuardDecoderContract      = "bio-offset-consistency-v1"
	astrLinkGuardSpanContractField    = "astrlink_guard_span_contract"
	astrLinkGuardSpanContract         = "existing-ip-and-dynamic-url-v1"

	// The FP16 build is a single ONNX file with static 1x512 inputs and
	// float32 logits, without external data. Every accelerated variant
	// installs the same files and differs only in its execution block.
	astrLinkGuardFP16Folder = "fp16"
	astrLinkGuardFP16Model  = "model_fp16_static512.onnx"
	astrLinkGuardFP16Bytes  = 75_462_203

	// The tokenizer of every release so far pads with <pad>, id 1. A release
	// with another tokenizer has its pad id read from the file instead.
	astrLinkGuardTokenizerSHA256 = "0cf3b235ef125658015a6c8026a5fe6f28088bf825101a990ec18a10200feef4"
	astrLinkGuardPadTokenID      = 1
)

// The pinned release, chosen by astrLinkGuardFP16Revision. Tests swap it.
var astrLinkGuardRevision, astrLinkGuardVersion, astrLinkGuardPinnedFP16 = astrLinkGuardPin(astrLinkGuardFP16Revision)

// astrLinkGuardPin pins 0.1.3 with its FP16 build once its commit is known,
// and 0.1.2 without one until then.
func astrLinkGuardPin(fp16Revision string) (revision, version string, fp16 bool) {
	if contract.ValidatePrivacyModelRevision(fp16Revision) != nil {
		return astrLinkGuardCPURevision, astrLinkGuardCPUVersion, false
	}
	return fp16Revision, astrLinkGuardFP16Version, true
}

// Guard labels in id2label order; each label is its own canonical kind.
var astrLinkGuardKinds = [...]contract.CanonicalKind{
	contract.CanonicalKindEmail, contract.CanonicalKindPhone,
	contract.CanonicalKindAccount, contract.CanonicalKindPaymentCard,
	contract.CanonicalKindIPAddress, contract.CanonicalKindURL,
	contract.CanonicalKindCommonSecret, contract.CanonicalKindAddress,
	contract.CanonicalKindDate, contract.CanonicalKindPerson,
}

// astrLinkGuardCatalogEntry is the pinned release as this system offers it.
func astrLinkGuardCatalogEntry() contract.PrivacyModelCatalogItem {
	item := astrLinkGuardPinnedEntry()
	item.Variants = offeredAstrLinkGuardVariants(item.Variants)
	return item
}

// astrLinkGuardPinnedEntry is the pinned release with every variant it has
// on any system. Installations are checked against it wherever they load.
func astrLinkGuardPinnedEntry() contract.PrivacyModelCatalogItem {
	return astrLinkGuardEntry(astrLinkGuardRevision, astrLinkGuardVersion, astrLinkGuardPinnedFP16)
}

func astrLinkGuardEntry(revision, version string, fp16 bool) contract.PrivacyModelCatalogItem {
	return contract.PrivacyModelCatalogItem{
		ID: CatalogAstrLinkGuard, Name: "AstrLink Guard",
		Summary:  "AstrLink's own compact Chinese and English privacy model, tuned for prompts and code. Detects personal information, credentials, IP addresses and URLs locally.",
		Source:   contract.PrivacyModelCatalogSourceOfficial,
		RepoID:   astrLinkGuardRepoID,
		Revision: revision, Version: &version,
		License: "Apache-2.0", Languages: []string{"zh", "en"},
		Adapter: contract.PrivacyModelAdapterHFToken, Recommended: true,
		Variants: astrLinkGuardVariants(fp16),
	}
}

// astrLinkGuardVariants lists the CPU variants and, with fp16, one variant
// per accelerated choice of any system, under the ids and names local FP16
// imports use. INT8 on the CPU stays the recommended variant.
func astrLinkGuardVariants(fp16 bool) []contract.PrivacyModelVariant {
	variants := []contract.PrivacyModelVariant{
		{
			ID: "cpu_int8", Name: "CPU INT8", Quantization: "int8",
			BytesTotal: 38_971_636, EstimatedRAMBytes: 268_435_456,
			Recommended: true, Supported: true,
		},
		{
			ID: "cpu_fp32", Name: "CPU FP32", Quantization: "fp32",
			BytesTotal: 149_179_699, EstimatedRAMBytes: 402_653_184,
			Supported: true,
		},
	}
	if !fp16 {
		return variants
	}
	for _, choice := range allAcceleratedChoices() {
		variants = append(variants, contract.PrivacyModelVariant{
			ID: choice.id, Name: choice.name, Quantization: "f16",
			BytesTotal: astrLinkGuardFP16Bytes,
			// Every release keeps the estimate of the pin, which its
			// installations are checked against.
			EstimatedRAMBytes: acceleratedEstimatedRAM(astrLinkGuardFP16Bytes, choice.ramMultiplier),
			Supported:         true,
		})
	}
	return variants
}

// offeredAstrLinkGuardVariants keeps the accelerated variants this system
// offers: Core ML on Apple silicon, listed as unsupported before macOS 26,
// and DirectML on Windows x64 from build 19041. The CPU variants stay.
func offeredAstrLinkGuardVariants(
	variants []contract.PrivacyModelVariant,
) []contract.PrivacyModelVariant {
	choices, unsupported := localAcceleratedChoices()
	offered := make(map[string]bool, len(choices))
	for _, choice := range choices {
		offered[choice.id] = true
	}
	result := make([]contract.PrivacyModelVariant, 0, len(variants))
	for _, variant := range variants {
		if _, accelerated := astrLinkGuardAccelerated(variant.ID); accelerated {
			if !offered[variant.ID] {
				continue
			}
			if unsupported != nil {
				variant.Supported = false
				variant.UnsupportedReason = cloneString(unsupported)
			}
		}
		result = append(result, variant)
	}
	return result
}

// Each variant is a self-contained folder; releases keep this layout and
// change only file contents. The accelerated variants share fp16/.
func astrLinkGuardLayout(variant string) (string, string, bool) {
	switch variant {
	case "cpu_int8":
		return "int8", "model_int8.onnx", true
	case "cpu_fp32":
		return "fp32", "model.onnx", true
	}
	if _, accelerated := astrLinkGuardAccelerated(variant); accelerated {
		return astrLinkGuardFP16Folder, astrLinkGuardFP16Model, true
	}
	return "", "", false
}

// astrLinkGuardAccelerated reports whether a variant runs the FP16 build.
func astrLinkGuardAccelerated(variant string) (acceleratedChoice, bool) {
	for _, choice := range allAcceleratedChoices() {
		if choice.id == variant {
			return choice, true
		}
	}
	return acceleratedChoice{}, false
}

// astrLinkGuardFolderFiles are the files a variant installs from its folder.
// The CPU exports keep their weights in external data; the FP16 build does
// not.
func astrLinkGuardFolderFiles(variant string) []string {
	_, model, _ := astrLinkGuardLayout(variant)
	files := []string{model}
	if _, accelerated := astrLinkGuardAccelerated(variant); !accelerated {
		files = append(files, model+"_data")
	}
	return append(files, "config.json", "tokenizer.json", "tokenizer_config.json")
}

func astrLinkGuardRuntime(variant string) runtimeSpec {
	folder, model, _ := astrLinkGuardLayout(variant)
	runtime := hfRuntime(folder + "/" + model)
	if choice, accelerated := astrLinkGuardAccelerated(variant); accelerated {
		// The execution block a local import of the FP16 build writes.
		execution := choice.execution
		execution.StaticSequenceLength = runtime.window
		execution.PadTokenID = astrLinkGuardPadTokenID
		runtime.execution = cloneExecution(&execution)
	} else {
		runtime.externalData = []string{folder + "/" + model + "_data"}
	}
	runtime.tokenizerPath = folder + "/tokenizer.json"
	runtime.configPath = folder + "/config.json"
	tokenTypeIDs := "token_type_ids"
	runtime.inputNames.TokenTypeIDs = &tokenTypeIDs
	return runtime
}

func astrLinkGuardAssets(variant string) []Asset {
	shared := []Asset{
		{Path: "config.json", Size: 1_804, SHA256: "2c20003590d642e273c03c4c7c82d9849fa75f64bf29a71ec0a0c9a20ba4a150"},
		{Path: "tokenizer.json", Size: 1_502_881, SHA256: astrLinkGuardTokenizerSHA256},
		{Path: "tokenizer_config.json", Size: 205, SHA256: "3a7cf4d70bb7c0376f18cc6ad23c143853de5b4478084c414d5dbbd35ea5c589"},
	}
	var assets []Asset
	switch variant {
	case "cpu_int8":
		assets = []Asset{
			{Path: "model_int8.onnx", Size: 330_781, SHA256: "0a070d3113615e33ba3b84d6bd486f663a377c04c4b056e7381732179f51832a"},
			{Path: "model_int8.onnx_data", Size: 37_124_608, SHA256: "0ba34216ceead01e0ae2dfd8e46d52b67e7aee80b04f722d9b758613a91c0067"},
		}
	case "cpu_fp32":
		assets = []Asset{
			{Path: "model.onnx", Size: 219_740, SHA256: "994d61395afadffd0b81aa94c95f9f7b6ba02e79771b6109a72eeb9b541c156d"},
			{Path: "model.onnx_data", Size: 147_443_712, SHA256: "2298b89ea747a212820abb81260027b2bc6383fa6615277e24546e535031b734"},
		}
	default:
		if _, accelerated := astrLinkGuardAccelerated(variant); !accelerated {
			return nil
		}
		assets = []Asset{
			{Path: astrLinkGuardFP16Model, Size: 73_945_956, SHA256: "ade35d3d5dbec10860d3cea5b25ad0facb27fa59c0e6547d91598afaa0012af6"},
		}
	}
	folder, _, _ := astrLinkGuardLayout(variant)
	result := make([]Asset, 0, len(assets)+len(shared)+1)
	for _, asset := range append(assets, shared...) {
		asset.Path = folder + "/" + asset.Path
		result = append(result, asset)
	}
	return append(result, Asset{
		Path: astrLinkGuardLicense, Size: 11_357,
		SHA256: "c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4",
	})
}

// countAstrLinkGuardDownload sends the HEAD the Hub counts as one download.
// It is best effort; the installation is already ready and never depends on it.
func (registry *Registry) countAstrLinkGuardDownload(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, astrLinkGuardCountTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, registry.probe.endpoint(
		astrLinkGuardRepoID, "resolve", astrLinkGuardCountRevision, "config.json",
	), nil)
	if err != nil {
		return
	}
	if response, err := registry.httpClient.Do(request); err == nil && response.Body != nil {
		_ = response.Body.Close()
	}
}

func defaultAstrLinkGuardLabelMapping() map[string]*contract.CanonicalKind {
	values := make(map[string]contract.CanonicalKind, len(astrLinkGuardKinds))
	for _, kind := range astrLinkGuardKinds {
		values[string(kind)] = kind
	}
	return resolvedLabelMapping(values)
}

// validateAstrLinkGuardConfig rejects releases whose labels or decoding
// contracts this build cannot run, so they are never offered as updates.
func validateAstrLinkGuardConfig(document []byte) error {
	config, adapter, err := parsePrivacyModelConfig(document)
	if err != nil || adapter != contract.PrivacyModelAdapterHFToken ||
		prohibitedModelConfig(config) ||
		!hasTokenClassificationArchitecture(config.Architectures) ||
		!usesTokenTypeIDs(config) ||
		len(config.ID2Label) != 1+2*len(astrLinkGuardKinds) ||
		config.ID2Label["0"] != "O" {
		return ErrUnsupportedModel
	}
	for index, kind := range astrLinkGuardKinds {
		if config.ID2Label[strconv.Itoa(1+index*2)] != "B-"+string(kind) ||
			config.ID2Label[strconv.Itoa(2+index*2)] != "I-"+string(kind) {
			return ErrUnsupportedModel
		}
	}
	var contracts map[string]json.RawMessage
	if json.Unmarshal(document, &contracts) != nil ||
		!knownGuardContract(contracts[astrLinkGuardDecoderContractField], astrLinkGuardDecoderContract) ||
		!knownGuardContract(contracts[astrLinkGuardSpanContractField], astrLinkGuardSpanContract) {
		return ErrUnsupportedModel
	}
	return nil
}

func knownGuardContract(raw json.RawMessage, known string) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && value == known
}
