package privacymodel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestCoreMLVariantsRequireAppleSiliconAndMacOS26(t *testing.T) {
	for _, platform := range []struct {
		goos, goarch, product, darwin string
		want                          coreMLSupport
	}{
		{"darwin", "arm64", "26.0", "25.0.0", coreMLSupported},
		{"darwin", "arm64", "27.0.1\n", "26.0.0", coreMLSupported},
		{"darwin", "arm64", "26", "", coreMLSupported},
		// A compatibility product version on a macOS 26 kernel.
		{"darwin", "arm64", "16.0", "25.1.0", coreMLSupported},
		{"darwin", "arm64", "", "25.0.0", coreMLSupported},
		{"darwin", "arm64", "15.7.1", "24.6.0", coreMLRequiresMacOS26},
		{"darwin", "arm64", "14.0", "23.0.0", coreMLRequiresMacOS26},
		{"darwin", "arm64", "", "", coreMLRequiresMacOS26},
		{"darwin", "arm64", "x26", "v25", coreMLRequiresMacOS26},
		{"darwin", "arm64", "99999.0", "", coreMLRequiresMacOS26},
		{"darwin", "amd64", "26.0", "25.0.0", coreMLUnavailable},
		{"linux", "arm64", "26.0", "25.0.0", coreMLUnavailable},
		{"windows", "arm64", "", "", coreMLUnavailable},
		{"linux", "amd64", "", "", coreMLUnavailable},
	} {
		if got := coreMLPlatformSupport(platform.goos, platform.goarch, platform.product, platform.darwin); got != platform.want {
			t.Fatalf("coreMLPlatformSupport(%s, %s, %q, %q)=%v, want %v",
				platform.goos, platform.goarch, platform.product, platform.darwin, got, platform.want)
		}
	}
}

func TestMacOSVersionIsReadOnDarwin(t *testing.T) {
	product, darwin := macOSVersion()
	if runtime.GOOS != "darwin" {
		if product != "" || darwin != "" {
			t.Fatalf("macOSVersion off macOS=%q, %q", product, darwin)
		}
		return
	}
	if majorVersion(product) < 11 || majorVersion(darwin) < 20 {
		t.Fatalf("macOSVersion=%q, %q", product, darwin)
	}
}

// An "All units" installation made before the variant was withdrawn still
// loads: neither the registry nor the worker derives variants again.
func TestInstalledAllUnitsCoreMLVariantStillLoads(t *testing.T) {
	source := writeLocalFP16Fixture(t, staticFP16Inputs(512), true)
	root := filepath.Join(t.TempDir(), "registry")
	store := openRegistryStore(t)
	setLocalCoreMLSupport(t, coreMLSupported)
	offered := coreMLVariantChoices
	t.Cleanup(func() { coreMLVariantChoices = offered })
	coreMLVariantChoices = append(slices.Clone(offered), coreMLVariantChoice{
		suffix: "all", name: "Core ML FP16 · All units",
		computeUnits: coreMLComputeUnitsAll, ramMultiplier: 20,
	})

	registry := newLocalTestRegistry(t, root, store)
	probe, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	)
	if err != nil {
		t.Fatalf("ProbeLocal: %v", err)
	}
	started, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID: probe.RepoID, Revision: probe.Revision,
			VariantID: "coreml_fp16_all", LabelMapping: emailMapping(),
		},
	)
	if err != nil {
		t.Fatalf("Install coreml_fp16_all: %v", err)
	}
	if ready := waitForInstallation(t, registry, started.ID); ready.Status != contract.PrivacyModelStatusReady {
		t.Fatalf("installation=%#v", ready)
	}

	coreMLVariantChoices = offered
	restarted := newLocalTestRegistry(t, root, store)
	installation, err := restarted.GetInstallation(started.ID)
	if err != nil || installation.VariantID != "coreml_fp16_all" ||
		installation.Status != contract.PrivacyModelStatusReady {
		t.Fatalf("installation after restart=%#v err=%v", installation, err)
	}
	if _, ok := restarted.ReadyInstallation(started.ID); !ok {
		t.Fatal("the All units installation is not ready after the variant was withdrawn")
	}
	reprobed, err := restarted.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	)
	if err != nil {
		t.Fatalf("ProbeLocal after withdrawal: %v", err)
	}
	assertVariantIDs(t, reprobed.Variants, "coreml_fp16_gpu", "coreml_fp16_ane", "cpu_int8")
	if reprobed.RepoID != probe.RepoID || reprobed.Revision != probe.Revision {
		t.Fatal("withdrawing a Core ML variant changed the local identity")
	}
}

func TestLocalProbeNamesAstrLinkGuardImportsByFingerprint(t *testing.T) {
	setLocalCoreMLSupport(t, coreMLSupported)
	probeWith := func(config []byte) contract.PrivacyModelProbeResponse {
		t.Helper()
		source := writeLocalFP16Fixture(t, staticFP16Inputs(512), true)
		if err := os.WriteFile(filepath.Join(source, "config.json"), config, 0o600); err != nil {
			t.Fatal(err)
		}
		registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"), openRegistryStore(t))
		probe, err := registry.ProbeLocal(
			context.Background(),
			contract.PrivacyModelLocalProbeRequest{Path: source},
		)
		if err != nil {
			t.Fatalf("ProbeLocal: %v", err)
		}
		if probe.RepoID != localRepoID(probe.Revision) || probe.RequestedRevision != probe.Revision {
			t.Fatalf("local identity=%s@%s", probe.RepoID, probe.Revision)
		}
		return probe
	}

	guard := probeWith(guardConfigDocument(t, nil))
	if want := "AstrLink Guard · " + guard.Revision[:8]; guard.Name != want {
		t.Fatalf("name=%q, want %q", guard.Name, want)
	}
	if guard.License != nil || guard.Adapter != contract.PrivacyModelAdapterHFToken {
		t.Fatalf("Guard import claims license %v, adapter %q", guard.License, guard.Adapter)
	}
	assertVariantIDs(t, guard.Variants, "coreml_fp16_gpu", "coreml_fp16_ane", "cpu_int8")

	// The same contract values the catalog checks decide it: another label
	// order or span contract is a generic local model.
	for _, edit := range []func(map[string]any){
		func(config map[string]any) { config[astrLinkGuardSpanContractField] = "other-span-contract" },
		func(config map[string]any) {
			labels := config["id2label"].(map[string]string)
			labels["1"], labels["3"] = labels["3"], labels["1"]
		},
	} {
		other := probeWith(guardConfigDocument(t, edit))
		if want := "Local privacy model · " + other.Revision[:8]; other.Name != want {
			t.Fatalf("name=%q, want %q", other.Name, want)
		}
	}
}

func TestLocalProbeOffersCoreMLVariantsForStaticFP16Models(t *testing.T) {
	source := writeLocalFP16Fixture(t, staticFP16Inputs(512), true)
	root := filepath.Join(t.TempDir(), "registry")
	store := openRegistryStore(t)

	setLocalCoreMLSupport(t, coreMLUnavailable)
	registry := newLocalTestRegistry(t, root, store)
	cpuOnly, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	)
	if err != nil {
		t.Fatalf("ProbeLocal without Core ML: %v", err)
	}
	assertVariantIDs(t, cpuOnly.Variants, "gpu_f16", "cpu_int8")
	if cpuOnly.Variants[0].Supported || cpuOnly.Variants[0].UnsupportedReason == nil ||
		*cpuOnly.Variants[0].UnsupportedReason != "cpu_only" {
		t.Fatalf("FP16 off Apple silicon=%#v", cpuOnly.Variants[0])
	}

	setLocalCoreMLSupport(t, coreMLSupported)
	probe, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	)
	if err != nil {
		t.Fatalf("ProbeLocal with Core ML: %v", err)
	}
	if probe.RepoID != cpuOnly.RepoID || probe.Revision != cpuOnly.Revision {
		t.Fatalf("Core ML changed local identity: %s@%s vs %s@%s",
			probe.RepoID, probe.Revision, cpuOnly.RepoID, cpuOnly.Revision)
	}
	assertVariantIDs(t, probe.Variants,
		"coreml_fp16_gpu", "coreml_fp16_ane", "cpu_int8")
	for index, variant := range probe.Variants[:2] {
		if want := coreMLEstimatedRAM(variant.BytesTotal, coreMLVariantChoices[index].ramMultiplier); variant.EstimatedRAMBytes != want {
			t.Fatalf("%s estimated RAM=%d, want %d", variant.ID, variant.EstimatedRAMBytes, want)
		}
		if !variant.Supported || variant.UnsupportedReason != nil || variant.Recommended ||
			variant.Quantization != "f16" ||
			variant.BytesTotal != cpuOnly.Variants[0].BytesTotal ||
			!strings.HasPrefix(variant.Name, "Core ML FP16 · ") {
			t.Fatalf("Core ML variant=%#v", variant)
		}
	}
	if probe.Variants[2] != cpuOnly.Variants[1] {
		t.Fatalf("INT8 variant changed: %#v vs %#v", probe.Variants[2], cpuOnly.Variants[1])
	}

	documents := map[string][]byte{}
	for _, variantID := range []string{"coreml_fp16_ane", "cpu_int8"} {
		started, err := registry.Install(
			context.Background(),
			contract.PrivacyModelInstallRequest{
				RepoID: probe.RepoID, Revision: probe.Revision,
				VariantID: variantID, LabelMapping: emailMapping(),
			},
		)
		if err != nil {
			t.Fatalf("Install %s: %v", variantID, err)
		}
		if ready := waitForInstallation(t, registry, started.ID); ready.Status != contract.PrivacyModelStatusReady {
			t.Fatalf("installation %s=%#v", variantID, ready)
		}
		installed, exists := registry.ReadyInstallation(started.ID)
		if !exists {
			t.Fatalf("%s is not ready", variantID)
		}
		document, err := os.ReadFile(filepath.Join(installed.Directory, InstallationManifestName))
		if err != nil {
			t.Fatal(err)
		}
		documents[variantID] = document
	}
	if want := `"output_name":"logits","label_mapping":{"EMAIL":"email"},"files":[`; !bytes.Contains(documents["cpu_int8"], []byte(want)) ||
		bytes.Contains(documents["cpu_int8"], []byte(`"execution"`)) {
		t.Fatalf("CPU manifest gained an execution field: %s", documents["cpu_int8"])
	}
	if want := `,"execution":{"provider":"coreml","model_format":"mlprogram","compute_units":"cpu_and_neural_engine","static_sequence_length":512,"pad_token_id":1}}`; !bytes.HasSuffix(documents["coreml_fp16_ane"], []byte(want)) {
		t.Fatalf("Core ML manifest=%s", documents["coreml_fp16_ane"])
	}
	var manifest normalizedManifest
	if err := strictDecodeJSON(documents["coreml_fp16_ane"], &manifest); err != nil ||
		manifest.ModelPath != "model_fp16_static512.onnx" ||
		len(manifest.ExternalData) != 0 || len(manifest.Files) != 3 {
		t.Fatalf("Core ML manifest=%#v err=%v", manifest, err)
	}

	restarted := newLocalTestRegistry(t, root, store)
	id := InstallationID(probe.RepoID, probe.Revision, "coreml_fp16_ane")
	if _, ok := restarted.ReadyInstallation(id); !ok {
		t.Fatal("Core ML installation is not ready after restart")
	}
}

func TestLocalProbeListsCoreMLVariantsAsUnsupportedBeforeMacOS26(t *testing.T) {
	source := writeLocalFP16Fixture(t, staticFP16Inputs(512), true)
	registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"), openRegistryStore(t))

	setLocalCoreMLSupport(t, coreMLRequiresMacOS26)
	probe, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	)
	if err != nil {
		t.Fatalf("ProbeLocal before macOS 26: %v", err)
	}
	assertVariantIDs(t, probe.Variants,
		"coreml_fp16_gpu", "coreml_fp16_ane", "cpu_int8")
	for _, variant := range probe.Variants[:2] {
		if variant.Supported || variant.Recommended || variant.UnsupportedReason == nil ||
			*variant.UnsupportedReason != "requires_macos_26" {
			t.Fatalf("Core ML variant before macOS 26=%#v", variant)
		}
	}
	if !probe.Variants[2].Supported {
		t.Fatalf("INT8 variant=%#v", probe.Variants[2])
	}
	if err := contract.ValidatePrivacyModelProbeResponse(probe); err != nil {
		t.Fatalf("probe response is invalid: %v", err)
	}
	_, err = registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID: probe.RepoID, Revision: probe.Revision,
			VariantID: "coreml_fp16_gpu", LabelMapping: emailMapping(),
		},
	)
	if !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("Install before macOS 26: %v", err)
	}
}

func TestLocalProbeKeepsUnfitFP16ModelsUnsupported(t *testing.T) {
	setLocalCoreMLSupport(t, coreMLSupported)
	for name, fixture := range map[string]struct {
		inputs [][]byte
		pad    bool
	}{
		"dynamic sequence": {inputs: [][]byte{
			onnxValueInfo("input_ids", onnxElementTypeInt64, 1, "sequence"),
			onnxValueInfo("attention_mask", onnxElementTypeInt64, 1, "sequence"),
			onnxValueInfo("token_type_ids", onnxElementTypeInt64, 1, "sequence"),
		}, pad: true},
		"window mismatch":   {inputs: staticFP16Inputs(256), pad: true},
		"int32 inputs":      {inputs: staticInputsOfType(6, 512), pad: true},
		"missing input":     {inputs: staticFP16Inputs(512)[:2], pad: true},
		"no pad token":      {inputs: staticFP16Inputs(512), pad: false},
		"batch of two":      {inputs: batchInputs(2, 512), pad: true},
		"extra graph input": {inputs: append(staticFP16Inputs(512), onnxValueInfo("position_ids", onnxElementTypeInt64, 1, 512)), pad: true},
	} {
		t.Run(name, func(t *testing.T) {
			source := writeLocalFP16Fixture(t, fixture.inputs, fixture.pad)
			registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"))
			probe, err := registry.ProbeLocal(
				context.Background(),
				contract.PrivacyModelLocalProbeRequest{Path: source},
			)
			if err != nil {
				t.Fatalf("ProbeLocal: %v", err)
			}
			assertVariantIDs(t, probe.Variants, "gpu_f16", "cpu_int8")
			if probe.Variants[0].Supported {
				t.Fatalf("unfit FP16 model is supported: %#v", probe.Variants[0])
			}
		})
	}
}

func TestLocalProbeOffersCoreMLOnlyForFloatLogits(t *testing.T) {
	setLocalCoreMLSupport(t, coreMLSupported)
	const onnxElementTypeFloat16 = 10
	for name, output := range map[string][]byte{
		"float16 logits":   onnxValueInfo("logits", onnxElementTypeFloat16, 1, 512, 21),
		"no logits output": onnxValueInfo("scores", onnxElementTypeFloat, 1, 512, 21),
	} {
		t.Run(name, func(t *testing.T) {
			source := writeLocalFP16Fixture(t, staticFP16Inputs(512), true)
			model := onnxModelWithOutput(output, staticFP16Inputs(512)...)
			if err := os.WriteFile(filepath.Join(source, "model_fp16_static512.onnx"), model, 0o600); err != nil {
				t.Fatal(err)
			}
			registry := newLocalTestRegistry(t, filepath.Join(t.TempDir(), "registry"), openRegistryStore(t))
			probe, err := registry.ProbeLocal(
				context.Background(),
				contract.PrivacyModelLocalProbeRequest{Path: source},
			)
			if err != nil {
				t.Fatalf("ProbeLocal: %v", err)
			}
			assertVariantIDs(t, probe.Variants, "gpu_f16", "cpu_int8")
			if probe.Variants[0].Supported {
				t.Fatalf("FP16 model without float logits is supported: %#v", probe.Variants[0])
			}
		})
	}
}

func TestExecutionValidationMatchesTheWorkerContract(t *testing.T) {
	format := coreMLModelFormatMLProgram
	valid := func(units string) *normalizedExecution {
		return &normalizedExecution{
			Provider: executionProviderCoreML, ModelFormat: &format,
			ComputeUnits: &units, StaticSequenceLength: 512, PadTokenID: 1,
		}
	}
	for _, units := range []string{"cpu_and_gpu", "cpu_and_neural_engine", "all"} {
		if err := validateExecution(valid(units), contract.PrivacyModelAdapterHFToken, 512); err != nil {
			t.Fatalf("%s: %v", units, err)
		}
	}
	cpu := &normalizedExecution{Provider: executionProviderCPU, StaticSequenceLength: 512}
	if err := validateExecution(cpu, contract.PrivacyModelAdapterHFToken, 512); err != nil {
		t.Fatalf("cpu: %v", err)
	}
	if err := validateExecution(nil, contract.PrivacyModelAdapterOpenAIBIOES, 512); err != nil {
		t.Fatalf("absent execution: %v", err)
	}
	neural := "neural_network"
	invalid := map[string]func(*normalizedExecution){
		"unknown units":   func(execution *normalizedExecution) { *execution.ComputeUnits = "cpu_only" },
		"missing units":   func(execution *normalizedExecution) { execution.ComputeUnits = nil },
		"missing format":  func(execution *normalizedExecution) { execution.ModelFormat = nil },
		"neural network":  func(execution *normalizedExecution) { execution.ModelFormat = &neural },
		"length":          func(execution *normalizedExecution) { execution.StaticSequenceLength = 256 },
		"zero length":     func(execution *normalizedExecution) { execution.StaticSequenceLength = 0 },
		"negative pad":    func(execution *normalizedExecution) { execution.PadTokenID = -1 },
		"large pad":       func(execution *normalizedExecution) { execution.PadTokenID = maxPadTokenID + 1 },
		"provider":        func(execution *normalizedExecution) { execution.Provider = "cuda" },
		"cpu with format": func(execution *normalizedExecution) { execution.Provider = executionProviderCPU },
	}
	for name, edit := range invalid {
		execution := valid("all")
		edit(execution)
		if validateExecution(execution, contract.PrivacyModelAdapterHFToken, 512) == nil {
			t.Fatalf("%s accepted: %#v", name, execution)
		}
	}
	for _, adapter := range []contract.PrivacyModelAdapter{
		contract.PrivacyModelAdapterOpenAIBIOES,
		contract.PrivacyModelAdapterPPLXBIOES,
		contract.PrivacyModelAdapterAstrLinkGuard,
	} {
		if validateExecution(valid("all"), adapter, 512) == nil {
			t.Fatalf("%s accepted Core ML", adapter)
		}
	}
}

func TestTokenizerPadTokenIDPrefersPaddingThenSpecialPadToken(t *testing.T) {
	for name, test := range map[string]struct {
		document string
		want     int
		ok       bool
	}{
		"padding":      {`{"padding":{"pad_id":3},"added_tokens":[{"id":1,"content":"<pad>","special":true}]}`, 3, true},
		"xlm-r pad":    {`{"padding":null,"added_tokens":[{"id":0,"content":"<s>","special":true},{"id":1,"content":"<pad>","special":true}]}`, 1, true},
		"bert pad":     {`{"added_tokens":[{"id":0,"content":"[PAD]","special":true}]}`, 0, true},
		"not special":  {`{"added_tokens":[{"id":1,"content":"<pad>","special":false}]}`, 0, false},
		"conflicting":  {`{"added_tokens":[{"id":1,"content":"<pad>","special":true},{"id":2,"content":"[PAD]","special":true}]}`, 0, false},
		"none":         {`{"model":{"type":"WordPiece"}}`, 0, false},
		"negative":     {`{"padding":{"pad_id":-1}}`, 0, false},
		"invalid json": {`{`, 0, false},
	} {
		got, ok := tokenizerPadTokenID([]byte(test.document))
		if ok != test.ok || (ok && got != test.want) {
			t.Fatalf("%s: got %d, %v", name, got, ok)
		}
	}
}

func TestReadONNXGraphIOSkipsInitializersAndRejectsTruncation(t *testing.T) {
	model := onnxModel(staticFP16Inputs(512)...)
	inputs, outputs, err := readONNXGraphIO(bufio.NewReader(bytes.NewReader(model)))
	if err != nil || len(inputs) != 3 || len(outputs) != 1 ||
		outputs[0].name != "logits" || outputs[0].elementType != onnxElementTypeFloat {
		t.Fatalf("inputs=%#v outputs=%#v err=%v", inputs, outputs, err)
	}
	for _, input := range inputs {
		if input.elementType != onnxElementTypeInt64 ||
			len(input.dims) != 2 || input.dims[0] != 1 || input.dims[1] != 512 {
			t.Fatalf("input=%#v", input)
		}
	}
	dynamic := onnxModel(onnxValueInfo("input_ids", onnxElementTypeInt64, 1, "sequence"))
	inputs, _, err = readONNXGraphIO(bufio.NewReader(bytes.NewReader(dynamic)))
	if err != nil || len(inputs) != 1 || inputs[0].dims[1] != -1 {
		t.Fatalf("dynamic inputs=%#v err=%v", inputs, err)
	}
	for _, truncated := range [][]byte{model[:len(model)/2], model[:3], nil, []byte("not onnx")} {
		if _, _, err := readONNXGraphIO(bufio.NewReader(bytes.NewReader(truncated))); err == nil {
			t.Fatalf("accepted truncated model of %d bytes", len(truncated))
		}
	}
}

func setLocalCoreMLSupport(t *testing.T, support coreMLSupport) {
	t.Helper()
	previous := localCoreMLSupport
	localCoreMLSupport = func() coreMLSupport { return support }
	t.Cleanup(func() { localCoreMLSupport = previous })
}

func assertVariantIDs(t *testing.T, variants []contract.PrivacyModelVariant, ids ...string) {
	t.Helper()
	got := make([]string, len(variants))
	for index, variant := range variants {
		got[index] = variant.ID
	}
	if strings.Join(got, ",") != strings.Join(ids, ",") {
		t.Fatalf("variants=%v, want %v", got, ids)
	}
}

// writeLocalFP16Fixture writes an FP16 export next to an INT8 one. The pad
// token is the XLM-R one, as in AstrLink Guard.
func writeLocalFP16Fixture(t *testing.T, inputs [][]byte, pad bool) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "local-fp16-model")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	tokenizer := map[string]any{"version": "1.0", "model": map[string]any{"type": "Unigram"}}
	if pad {
		tokenizer["added_tokens"] = []map[string]any{
			{"id": 0, "content": "<s>", "special": true},
			{"id": 1, "content": "<pad>", "special": true},
		}
	}
	document, err := json.Marshal(tokenizer)
	if err != nil {
		t.Fatal(err)
	}
	assets := map[string][]byte{
		"config.json": []byte(`{
			"architectures":["BertForTokenClassification"],
			"model_type":"bert",
			"type_vocab_size":2,
			"pad_token_id":0,
			"id2label":{"0":"O","1":"B-EMAIL","2":"I-EMAIL"}
		}`),
		"tokenizer.json":            document,
		"model_fp16_static512.onnx": onnxModel(inputs...),
		"model_int8.onnx":           []byte("fake-local-int8-model"),
	}
	for relative, document := range assets {
		if err := os.WriteFile(filepath.Join(directory, relative), document, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func staticFP16Inputs(length int) [][]byte {
	return staticInputsOfType(onnxElementTypeInt64, length)
}

func staticInputsOfType(elementType int, length int) [][]byte {
	return [][]byte{
		onnxValueInfo("input_ids", elementType, 1, length),
		onnxValueInfo("attention_mask", elementType, 1, length),
		onnxValueInfo("token_type_ids", elementType, 1, length),
	}
}

func batchInputs(batch int, length int) [][]byte {
	return [][]byte{
		onnxValueInfo("input_ids", onnxElementTypeInt64, batch, length),
		onnxValueInfo("attention_mask", onnxElementTypeInt64, batch, length),
		onnxValueInfo("token_type_ids", onnxElementTypeInt64, batch, length),
	}
}

// onnxModel serializes a ModelProto in field order, as exporters do: nodes
// and a large initializer precede the graph inputs and the float logits.
func onnxModel(inputs ...[]byte) []byte {
	return onnxModelWithOutput(onnxValueInfo("logits", onnxElementTypeFloat, 1, 512, 21), inputs...)
}

func onnxModelWithOutput(output []byte, inputs ...[]byte) []byte {
	var graph []byte
	graph = protoBytesField(graph, 1, protoBytesField(nil, 4, []byte("Identity")))
	graph = protoBytesField(graph, 2, []byte("graph"))
	initializer := protoBytesField(nil, 8, []byte("weight"))
	initializer = protoVarintField(initializer, 2, 10)
	initializer = protoBytesField(initializer, 9, bytes.Repeat([]byte{0x3c}, 1<<17))
	graph = protoBytesField(graph, 5, initializer)
	for _, input := range inputs {
		graph = protoBytesField(graph, 11, input)
	}
	graph = protoBytesField(graph, 12, output)
	model := protoVarintField(nil, 1, 9)
	model = protoBytesField(model, 2, []byte("test"))
	model = protoBytesField(model, 7, graph)
	return protoBytesField(model, 8, protoVarintField(nil, 2, 17))
}

func onnxValueInfo(name string, elementType int, dims ...any) []byte {
	var shape []byte
	for _, dimension := range dims {
		var encoded []byte
		switch value := dimension.(type) {
		case int:
			encoded = protoVarintField(nil, 1, uint64(value))
		case string:
			encoded = protoBytesField(nil, 2, []byte(value))
		}
		shape = protoBytesField(shape, 1, encoded)
	}
	tensor := protoVarintField(nil, 1, uint64(elementType))
	tensor = protoBytesField(tensor, 2, shape)
	info := protoBytesField(nil, 1, []byte(name))
	return protoBytesField(info, 2, protoBytesField(nil, 1, tensor))
}

func protoVarintField(buffer []byte, field int, value uint64) []byte {
	buffer = appendProtoVarint(buffer, uint64(field)<<3)
	return appendProtoVarint(buffer, value)
}

func protoBytesField(buffer []byte, field int, value []byte) []byte {
	buffer = appendProtoVarint(buffer, uint64(field)<<3|2)
	buffer = appendProtoVarint(buffer, uint64(len(value)))
	return append(buffer, value...)
}

func appendProtoVarint(buffer []byte, value uint64) []byte {
	for value >= 0x80 {
		buffer = append(buffer, byte(value)|0x80)
		value >>= 7
	}
	return append(buffer, byte(value))
}
