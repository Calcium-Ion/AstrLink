package privacymodel

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	CatalogPPLXPIITracer           contract.PrivacyModelCatalogID = "catalog_pplx_pii_tracer"
	CatalogSheltronEttin32M        contract.PrivacyModelCatalogID = "catalog_sheltron_ettin_32m"
	CatalogNymPIIMultilingualSmall contract.PrivacyModelCatalogID = "catalog_nym_pii_multilingual_small"
	CatalogOpenAIPrivacyFilter     contract.PrivacyModelCatalogID = "catalog_openai_privacy_filter"
	InstallationManifestName                                      = "astrlink-model.json"
)

type runtimeSpec struct {
	modelPath             string
	externalData          []string
	tokenizerPath         string
	configPath            string
	calibrationPath       *string
	secretRulesPath       *string
	secretCalibrationPath *string
	tagScheme             string
	window                int
	stride                int
	maxRequestTokens      int
	inputNames            normalizedInputNames
	outputName            string
}

type variantPlan struct {
	item    contract.PrivacyModelCatalogItem
	variant contract.PrivacyModelVariant
	assets  []Asset
	runtime runtimeSpec
}

func BuiltinCatalog() contract.PrivacyModelCatalogResponse {
	entries := builtinCatalogEntries()
	items := make([]contract.PrivacyModelCatalogItem, len(entries))
	for index := range entries {
		items[index] = copyCatalogItem(entries[index])
	}
	return contract.PrivacyModelCatalogResponse{Items: items}
}

func builtinCatalogEntries() []contract.PrivacyModelCatalogItem {
	return []contract.PrivacyModelCatalogItem{{
		ID: CatalogPPLXPIITracer, Name: "AstrLink PII-Tracer 0.6B INT4",
		Summary:  "PII-Tracer adapted by AstrLink with INT4 weights and calibrated error correction. Detects personal information and credentials locally.",
		Source:   contract.PrivacyModelCatalogSourceCommunity,
		RepoID:   "QuantumNous/astrlink-pii-tracer-int4",
		Revision: "0f9a56fc32062ea5827f4d908e7afaa22b88c902",
		License:  "MIT", Languages: []string{"en", "multilingual"},
		Adapter: contract.PrivacyModelAdapterPPLXBIOES,
		Variants: []contract.PrivacyModelVariant{{
			ID: "cpu_int4", Name: "CPU INT4", Quantization: "int4",
			BytesTotal: 471_931_100, EstimatedRAMBytes: 2_147_483_648,
			Supported: true, Recommended: true,
		}},
	}}
}

func legacyPPLXFP32CatalogEntry() contract.PrivacyModelCatalogItem {
	return contract.PrivacyModelCatalogItem{
		ID: CatalogPPLXPIITracer, Name: "Perplexity PII-Tracer 0.6B",
		Summary:  "Multilingual privacy span detector from Perplexity, using the Lemonade CPU export. Includes personal information and credentials.",
		Source:   contract.PrivacyModelCatalogSourceCommunity,
		RepoID:   "lemonade-sdk/pplx-pii-masking-onnx",
		Revision: "5ba4e413b78ff0f83d3c9cddee1bb5fdccbeee00",
		License:  "MIT", Languages: []string{"en", "multilingual"},
		Adapter: contract.PrivacyModelAdapterPPLXBIOES,
		Variants: []contract.PrivacyModelVariant{{
			ID: "cpu_fp32", Name: "CPU FP32", Quantization: "fp32",
			BytesTotal: 2_403_057_465, EstimatedRAMBytes: 6_442_450_944,
			Supported: true, Recommended: true,
		}},
	}
}

// Retain pinned provenance for existing installations and legacy recovery.
// These models are no longer advertised in the built-in catalog.
func legacyCatalogEntries() []contract.PrivacyModelCatalogItem {
	cpuOnly := "cpu_only"
	return []contract.PrivacyModelCatalogItem{
		legacyPPLXFP32CatalogEntry(),
		{
			ID: CatalogSheltronEttin32M, Name: "Privacy Filter Ettin 32M",
			Summary:  "Compact English privacy token classifier for local CPU inference.",
			Source:   contract.PrivacyModelCatalogSourceCommunity,
			RepoID:   "sheltron-ai/privacy-filter-ettin-32m",
			Revision: "53d55c58fdbb5ed2ace902a374f664c1cf4914c7",
			License:  "Apache-2.0", Languages: []string{"en"},
			Adapter: contract.PrivacyModelAdapterOpenAIBIOES,
			Variants: []contract.PrivacyModelVariant{
				{
					ID: "cpu_int8", Name: "CPU INT8", Quantization: "int8",
					BytesTotal: 38_883_173, EstimatedRAMBytes: 268_435_456,
					Recommended: true, Supported: true,
				},
				{
					ID: "cpu_fp32", Name: "CPU FP32", Quantization: "fp32",
					BytesTotal: 132_018_152, EstimatedRAMBytes: 536_870_912,
					Supported: true,
				},
			},
		},
		{
			ID: CatalogNymPIIMultilingualSmall, Name: "Nym PII Multilingual Small",
			Summary:  "Multilingual and CJK-capable local PII token classifier.",
			Source:   contract.PrivacyModelCatalogSourceCommunity,
			RepoID:   "Wismut/nym-pii-multilingual-small",
			Revision: "4348999cd3c2e20c49615e9af7c6bbb45b64cd85",
			License:  "MIT", Languages: []string{"multilingual", "cjk"},
			Adapter: contract.PrivacyModelAdapterHFToken,
			Variants: []contract.PrivacyModelVariant{
				{
					ID: "edge_int8", Name: "Edge INT8", Quantization: "int8",
					BytesTotal: 120_629_106, EstimatedRAMBytes: 402_653_184,
					Supported: true,
				},
				{
					ID: "cpu_int8", Name: "CPU INT8", Quantization: "int8",
					BytesTotal: 151_126_561, EstimatedRAMBytes: 536_870_912,
					Recommended: true, Supported: true,
				},
				{
					ID: "cpu_fp32", Name: "CPU FP32", Quantization: "fp32",
					BytesTotal: 441_614_904, EstimatedRAMBytes: 1_073_741_824,
					Supported: true,
				},
			},
		},
		{
			ID: CatalogOpenAIPrivacyFilter, Name: "OpenAI Privacy Filter",
			Summary:  "Official OpenAI privacy token classifier with BIOES/Viterbi decoding.",
			Source:   contract.PrivacyModelCatalogSourceOfficial,
			RepoID:   "openai/privacy-filter",
			Revision: DefaultRevision, License: "Apache-2.0",
			Languages: []string{"en"}, Adapter: contract.PrivacyModelAdapterOpenAIBIOES,
			Variants: []contract.PrivacyModelVariant{
				{
					ID: "cpu_q4", Name: "CPU Q4", Quantization: "q4",
					BytesTotal: 945_151_948, EstimatedRAMBytes: 2_147_483_648,
					Recommended: true, Supported: true,
				},
				{
					ID: "cpu_int8", Name: "CPU INT8", Quantization: "int8",
					BytesTotal: 1_646_075_888, EstimatedRAMBytes: 3_221_225_472,
					Supported: true,
				},
				{
					ID: "gpu_f16", Name: "GPU F16", Quantization: "f16",
					Supported: false, UnsupportedReason: &cpuOnly,
				},
				{
					ID: "gpu_q4f16", Name: "GPU Q4/F16", Quantization: "q4f16",
					Supported: false, UnsupportedReason: &cpuOnly,
				},
			},
		},
	}
}

func builtinVariantPlan(repoID, revision, variantID string) (variantPlan, bool) {
	for _, item := range append(builtinCatalogEntries(), legacyCatalogEntries()...) {
		if item.RepoID != repoID || item.Revision != revision {
			continue
		}
		for _, variant := range item.Variants {
			if variant.ID != variantID || !variant.Supported {
				continue
			}
			plan := variantPlan{item: copyCatalogItem(item), variant: variant}
			switch item.ID {
			case CatalogPPLXPIITracer:
				plan.assets = pplxAssets(variantID)
				plan.runtime = pplxRuntime(variantID)
			case CatalogSheltronEttin32M:
				plan.assets = sheltronAssets(variantID)
				calibration := "viterbi_calibration.json"
				plan.runtime = openAIRuntime(
					modelPathFor(variantID,
						"onnx/model_quantized.onnx",
						"onnx/model.onnx"),
					nil, &calibration,
				)
				plan.runtime.window = 512
			case CatalogNymPIIMultilingualSmall:
				plan.assets = nymAssets(variantID)
				plan.runtime = hfRuntime(modelPathFor(variantID,
					"int8/model_int8.onnx",
					"model.onnx"))
				if variantID == "edge_int8" {
					plan.runtime.modelPath = "edge-int8/model_int8.onnx"
				}
			case CatalogOpenAIPrivacyFilter:
				plan.assets = openAIAssets(variantID)
				modelPath := "onnx/model_q4.onnx"
				external := []string{"onnx/model_q4.onnx_data"}
				if variantID == "cpu_int8" {
					modelPath = "onnx/model_quantized.onnx"
					external = []string{"onnx/model_quantized.onnx_data"}
				}
				calibration := "viterbi_calibration.json"
				plan.runtime = openAIRuntime(modelPath, external, &calibration)
			}
			return plan, len(plan.assets) > 0
		}
	}
	return variantPlan{}, false
}

func pplxRuntime(variant string) runtimeSpec {
	runtime := hfRuntime("model.onnx")
	runtime.externalData = []string{"model.onnx.data"}
	runtime.tagScheme = "bioes"
	runtime.window = 4096
	if variant == "cpu_int4" {
		runtime.modelPath = "model_int4.onnx"
		runtime.externalData = []string{"model_int4.onnx.data"}
		runtime.window = 1024
	}
	return runtime
}

func pplxAssets(variant string) []Asset {
	if variant == "cpu_int4" {
		return []Asset{
			{Path: "model_int4.onnx", Size: 6_884_480, SHA256: "8c609f9ce51ac8976910e55905fd9991d63224c0ff048a016676306209806e75"},
			{Path: "model_int4.onnx.data", Size: 453_619_712, SHA256: "adaa552808510fb37b1502d051436da9e99ebbca5cd6b278cac9d89780aa48e8"},
			{Path: "config.json", Size: 2_498, SHA256: "c57c3d8114ef302c51a35d5eb72a35e02c3e10bd17b8401bc660be593fc46dfc"},
			{Path: "tokenizer.json", Size: 11_422_936, SHA256: "cae14d1c8dda080f23792355b0692b826bf1f1da3c86ebc1b37548a391cf6526"},
			{Path: "tokenizer_config.json", Size: 398, SHA256: "aa9c1b0a1c9b48c2f70bacdf64f7dab25194be4ffea0c6a6e4da262360a91d0a"},
			{Path: "LICENSE", Size: 1_076, SHA256: "7fbf88e9c951fe53eb614a46772d0b48ada6d50b351e5e11dcb64b4dc3fb8eb2"},
		}
	}
	return []Asset{
		{Path: "model.onnx", Size: 7_235_345, SHA256: "56309eabe1e3a646718de2b0e32b097dc2a9f3fb9cf54395cac1672a1a93a630"},
		{Path: "model.onnx.data", Size: 2_384_396_288, SHA256: "65ac3f46cc6f3f5abeae2ba9bb0b35c7daebb185ff3c420a373bc9bb998a0b5e"},
		{Path: "config.json", Size: 2_498, SHA256: "c57c3d8114ef302c51a35d5eb72a35e02c3e10bd17b8401bc660be593fc46dfc"},
		{Path: "tokenizer.json", Size: 11_422_936, SHA256: "cae14d1c8dda080f23792355b0692b826bf1f1da3c86ebc1b37548a391cf6526"},
		{Path: "tokenizer_config.json", Size: 398, SHA256: "aa9c1b0a1c9b48c2f70bacdf64f7dab25194be4ffea0c6a6e4da262360a91d0a"},
	}
}

func openAIRuntime(model string, external []string, calibration *string) runtimeSpec {
	return runtimeSpec{
		modelPath: model, externalData: append([]string(nil), external...),
		tokenizerPath: "tokenizer.json", configPath: "config.json",
		calibrationPath: calibration, tagScheme: "bioes",
		window: 4096, stride: 128, maxRequestTokens: 131_072,
		inputNames: normalizedInputNames{
			InputIDs: "input_ids", AttentionMask: "attention_mask",
		},
		outputName: "logits",
	}
}

func hfRuntime(model string) runtimeSpec {
	return runtimeSpec{
		modelPath: model, tokenizerPath: "tokenizer.json",
		configPath: "config.json", tagScheme: "bio",
		window: 512, stride: 128, maxRequestTokens: 131_072,
		inputNames: normalizedInputNames{
			InputIDs: "input_ids", AttentionMask: "attention_mask",
		},
		outputName: "logits",
	}
}

func modelPathFor(variant, quantized, full string) string {
	if variant == "cpu_fp32" {
		return full
	}
	return quantized
}

func sheltronAssets(variant string) []Asset {
	model := Asset{
		Path: "onnx/model_quantized.onnx", Size: 35_273_870,
		SHA256: "94df68db240d5d5f8c892dca3451c9e72b13398a0ed1e224aaabbcd04b249e6e",
	}
	if variant == "cpu_fp32" {
		model = Asset{
			Path: "onnx/model.onnx", Size: 128_408_849,
			SHA256: "0ed76ae3abb7a8ae1be2856433ee482f24e289b97c96552d6a0230160d9e023c",
		}
	}
	return []Asset{
		model,
		{Path: "config.json", Size: 3_092, SHA256: "535dffc4c2d897df40acc25d0688763568ca11a9852b5e2d69431790b36a191a"},
		{Path: "tokenizer.json", Size: 3_583_228, SHA256: "6c8aaa9a542084f2457eab775d4eeb51f92a70c0fd9de28d5edb0ddec3c08d30"},
		{Path: "tokenizer_config.json", Size: 20_839, SHA256: "c5a7dfd44d93c5d7ed9b2ddcd7a6018e935ec7d53f1de45910ea9868e0ff1c17"},
		{Path: "special_tokens_map.json", Size: 694, SHA256: "ea97ecdbcc73713039d8d64dbb05e3689495c96657fbd9a18f5bed381be81049"},
		{Path: "viterbi_calibration.json", Size: 1_450, SHA256: "0f577e2ac481c119d6812426f1eee21c05a3c61cc21956773423c57649d69980"},
	}
}

func nymAssets(variant string) []Asset {
	model := Asset{
		Path: "int8/model_int8.onnx", Size: 138_730_982,
		SHA256: "139006aea2cbd8e709d322f056232570de54661f624143be4893aaa387190286",
	}
	if variant == "edge_int8" {
		model = Asset{
			Path: "edge-int8/model_int8.onnx", Size: 108_233_527,
			SHA256: "e9a3a8c8cd55b3bcf329de5a9307cfae5053ce93c00330c33facd022e60daa17",
		}
	} else if variant == "cpu_fp32" {
		model = Asset{
			Path: "model.onnx", Size: 429_218_752,
			SHA256: "60c2ae1a5992e43d4022a29dcc81cc45250bdb08fd2ee472cfb4d8800bbe87aa",
		}
	}
	assets := []Asset{
		model,
		{Path: "config.json", Size: 5_688, SHA256: "3f07065571e22bb73eba28ddb1fae4509c0cf703762e3bfa7a6ab2111cf7cb88"},
		{Path: "tokenizer.json", Size: 12_389_891, SHA256: "c299144e68dfec1dc536204a7ae3712710c5c6cade9269a83f5042250d47d8de"},
	}
	if variant == "cpu_fp32" {
		assets = append(assets, Asset{
			Path: "tokenizer_config.json", Size: 573,
			SHA256: "02055b886266b1a475bc324da83e273fe96e4974002f9d970f26e94aa73e5885",
		})
	}
	return assets
}

func openAIAssets(variant string) []Asset {
	manifest := ProductionManifest()
	if variant == "cpu_q4" {
		return copyAssets(manifest.Assets)
	}
	return []Asset{
		{
			Path: "onnx/model_quantized.onnx", Size: 162_239,
			SHA256: "a325fb5341567a73c94e91ec5e49060d38d9b16111f518ad34773039a0c9c098",
		},
		{
			Path: "onnx/model_quantized.onnx_data", Size: 1_618_042_064,
			SHA256: "50f4c8c7f3c27fbc1fe16d4f74f6f7c3b74ba8f18a262e8b6911854c64c33a6d",
		},
		manifest.Assets[2], manifest.Assets[3], manifest.Assets[4],
	}
}

func InstallationID(repoID, revision, variantID string) contract.PrivacyModelID {
	digest := sha256.Sum256([]byte(repoID + "\n" + revision + "\n" + variantID))
	return contract.PrivacyModelID("model_" + hex.EncodeToString(digest[:16]))
}

func copyCatalogItem(item contract.PrivacyModelCatalogItem) contract.PrivacyModelCatalogItem {
	item.Languages = append([]string(nil), item.Languages...)
	item.Variants = append([]contract.PrivacyModelVariant(nil), item.Variants...)
	for index := range item.Variants {
		if item.Variants[index].UnsupportedReason != nil {
			reason := *item.Variants[index].UnsupportedReason
			item.Variants[index].UnsupportedReason = &reason
		}
	}
	return item
}

func copyAssets(assets []Asset) []Asset {
	return append([]Asset(nil), assets...)
}
