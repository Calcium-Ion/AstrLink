package privacymodel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"sort"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Core ML execution for FP16 ONNX exports with a static 1×window input.
//
// The CPU worker cannot run these exports: their inputs are fixed to one
// window, and FP16 is only faster than INT8 on Apple's GPU or Neural Engine.
// On macOS arm64 a local FP16 model is therefore offered once per Core ML
// compute-unit choice instead of the unsupported gpu_f16 entry. The worker
// registers the Core ML execution provider (CPU after it for any node Core ML
// does not take), pads every window to the static length and drops the
// padded positions from the logits. Everything else (tokenization, windowing,
// decoding, span contract) is the same as the CPU path.
const (
	executionProviderCPU    = "cpu"
	executionProviderCoreML = "coreml"

	coreMLModelFormatMLProgram = "mlprogram"

	coreMLComputeUnitsCPUAndGPU          = "cpu_and_gpu"
	coreMLComputeUnitsCPUAndNeuralEngine = "cpu_and_neural_engine"
	coreMLComputeUnitsAll                = "all"

	// Token ids index the embedding table; no tokenizer comes close to this.
	maxPadTokenID = 1<<24 - 1
	// Exporters write graph inputs and outputs after the nodes and
	// initializers, so the reader streams past those (without parsing them);
	// the field bound stops a malformed file.
	maxONNXGraphFields = 1 << 20
)

// normalizedExecution is absent from every CPU manifest, which keeps those
// manifests byte-identical to the format before it existed. A worker that
// predates it rejects the unknown field instead of running a static-shape
// model without padding.
type normalizedExecution struct {
	Provider             string  `json:"provider"`
	ModelFormat          *string `json:"model_format"`
	ComputeUnits         *string `json:"compute_units"`
	StaticSequenceLength int     `json:"static_sequence_length"`
	PadTokenID           int     `json:"pad_token_id"`
}

type coreMLVariantChoice struct {
	suffix       string
	name         string
	computeUnits string
	// ramMultiplier scales the asset size into the worker's resident
	// memory. Measured with a 74 MB MiniLM export on an M5 Pro: about 5x on
	// the GPU, about 19x once the Neural Engine plan is loaded.
	ramMultiplier int64
}

// coreMLVariantChoices are offered for each eligible FP16 model. "All units"
// (compute units "all") is no longer offered: on Apple silicon it ran on the
// GPU at GPU speed and used four times the memory. Installations that already
// use it keep loading, because validateExecution and the worker still accept
// that compute unit.
var coreMLVariantChoices = []coreMLVariantChoice{
	{suffix: "gpu", name: "Core ML FP16 · GPU", computeUnits: coreMLComputeUnitsCPUAndGPU, ramMultiplier: 5},
	{suffix: "ane", name: "Core ML FP16 · Neural Engine", computeUnits: coreMLComputeUnitsCPUAndNeuralEngine, ramMultiplier: 20},
}

type coreMLSupport int

const (
	// coreMLUnavailable keeps the CPU-only FP16 entry: not Apple silicon.
	coreMLUnavailable coreMLSupport = iota
	// coreMLRequiresMacOS26 lists the variants as unsupported, with a reason.
	coreMLRequiresMacOS26
	coreMLSupported
)

// Core ML variants need macOS 26 or later. ONNX Runtime issue #32569 reports
// wrong FP16 results from the Core ML provider on macOS 15, and no release
// before 26 has been tested, so older systems are shown the variants as
// unsupported instead. The worker refuses Core ML installations there too.
const (
	coreMLMinimumMacOSMajor = 26
	// Darwin 25 is the kernel of macOS 26.
	coreMLMinimumDarwinMajor         = 25
	unsupportedReasonRequiresMacOS26 = "requires_macos_26"
)

// localCoreMLSupport reports whether this build and system can run Core ML
// variants. Tests override it to cover every outcome.
var localCoreMLSupport = func() coreMLSupport {
	product, darwin := macOSVersion()
	return coreMLPlatformSupport(runtime.GOOS, runtime.GOARCH, product, darwin)
}

// coreMLPlatformSupport checks the product version first; the Darwin release
// covers a product version that is missing or reported in a compatibility
// form. Anything unreadable counts as too old.
func coreMLPlatformSupport(goos, goarch, product, darwin string) coreMLSupport {
	if goos != "darwin" || goarch != "arm64" {
		return coreMLUnavailable
	}
	if majorVersion(product) >= coreMLMinimumMacOSMajor ||
		majorVersion(darwin) >= coreMLMinimumDarwinMajor {
		return coreMLSupported
	}
	return coreMLRequiresMacOS26
}

// majorVersion returns the leading number of a dotted version, or -1.
func majorVersion(version string) int {
	major, _, _ := strings.Cut(strings.TrimSpace(version), ".")
	if major == "" || len(major) > 4 {
		return -1
	}
	value := 0
	for _, digit := range []byte(major) {
		if digit < '0' || digit > '9' {
			return -1
		}
		value = value*10 + int(digit-'0')
	}
	return value
}

func cloneExecution(source *normalizedExecution) *normalizedExecution {
	if source == nil {
		return nil
	}
	result := *source
	result.ModelFormat = cloneString(source.ModelFormat)
	result.ComputeUnits = cloneString(source.ComputeUnits)
	return &result
}

func validateExecution(
	execution *normalizedExecution,
	adapter contract.PrivacyModelAdapter,
	window int,
) error {
	if execution == nil {
		return nil
	}
	if adapter != contract.PrivacyModelAdapterHFToken ||
		execution.StaticSequenceLength <= 0 ||
		execution.StaticSequenceLength != window ||
		execution.PadTokenID < 0 || execution.PadTokenID > maxPadTokenID {
		return ErrInvalidConfig
	}
	switch execution.Provider {
	case executionProviderCPU:
		if execution.ModelFormat != nil || execution.ComputeUnits != nil {
			return ErrInvalidConfig
		}
	case executionProviderCoreML:
		if execution.ModelFormat == nil ||
			*execution.ModelFormat != coreMLModelFormatMLProgram ||
			execution.ComputeUnits == nil {
			return ErrInvalidConfig
		}
		switch *execution.ComputeUnits {
		case coreMLComputeUnitsCPUAndGPU,
			coreMLComputeUnitsCPUAndNeuralEngine,
			coreMLComputeUnitsAll:
		default:
			return ErrInvalidConfig
		}
	default:
		return ErrInvalidConfig
	}
	return nil
}

// expandLocalCoreMLVariants replaces each FP16 entry that is unsupported only
// because it is not a CPU model with one variant per compute-unit choice,
// supported from macOS 26 and listed as requiring it before that. It runs
// after the local revision is computed, so the revision of a directory stays
// the same on every platform and version.
func expandLocalCoreMLVariants(
	ctx context.Context,
	directory string,
	adapter contract.PrivacyModelAdapter,
	tokenizerDocument []byte,
	variants []contract.PrivacyModelVariant,
	plans map[string]customVariantPlan,
) ([]contract.PrivacyModelVariant, map[string]customVariantPlan, error) {
	support := localCoreMLSupport()
	if support == coreMLUnavailable || adapter != contract.PrivacyModelAdapterHFToken {
		return variants, plans, nil
	}
	padTokenID, hasPad := tokenizerPadTokenID(tokenizerDocument)
	if !hasPad {
		return variants, plans, nil
	}
	expanded := make([]contract.PrivacyModelVariant, 0, len(variants)+2)
	for _, variant := range variants {
		plan, exists := plans[variant.ID]
		suffix, eligible := coreMLVariantSuffix(variant, plan)
		if exists && eligible {
			static, err := localStaticInputLength(ctx, directory, plan.runtime)
			if err != nil {
				return nil, nil, err
			}
			eligible = static == plan.runtime.window
		}
		if !exists || !eligible {
			expanded = append(expanded, variant)
			continue
		}
		delete(plans, variant.ID)
		for _, choice := range coreMLVariantChoices {
			id := "coreml_fp16_" + choice.suffix + suffix
			if _, taken := plans[id]; taken {
				return nil, nil, ErrUnsupportedModel
			}
			coreML := variant
			coreML.ID = id
			coreML.Name = choice.name
			if suffix != "" {
				coreML.Name += " " + strings.TrimPrefix(suffix, "_")
			}
			coreML.Recommended = false
			coreML.Supported = support == coreMLSupported
			coreML.UnsupportedReason = nil
			if !coreML.Supported {
				reason := unsupportedReasonRequiresMacOS26
				coreML.UnsupportedReason = &reason
			}
			coreML.EstimatedRAMBytes = coreMLEstimatedRAM(variant.BytesTotal, choice.ramMultiplier)
			runtimeSpec := plan.runtime
			runtimeSpec.externalData = append([]string(nil), plan.runtime.externalData...)
			runtimeSpec.inputNames.TokenTypeIDs = cloneString(plan.runtime.inputNames.TokenTypeIDs)
			format := coreMLModelFormatMLProgram
			units := choice.computeUnits
			runtimeSpec.execution = &normalizedExecution{
				Provider:             executionProviderCoreML,
				ModelFormat:          &format,
				ComputeUnits:         &units,
				StaticSequenceLength: plan.runtime.window,
				PadTokenID:           padTokenID,
			}
			plans[id] = customVariantPlan{
				variant: coreML, assets: copyAssets(plan.assets), runtime: runtimeSpec,
			}
			expanded = append(expanded, coreML)
		}
	}
	return expanded, plans, nil
}

func coreMLEstimatedRAM(bytes int64, multiplier int64) int64 {
	if bytes > contract.MaxPrivacyModelByteCount/multiplier {
		return contract.MaxPrivacyModelByteCount
	}
	return max(bytes*multiplier, 256<<20)
}

// coreMLVariantSuffix accepts plain FP16 entries whose only problem is that
// they are not CPU models. q4f16 and exports named for another accelerator
// stay unsupported.
func coreMLVariantSuffix(
	variant contract.PrivacyModelVariant,
	plan customVariantPlan,
) (string, bool) {
	suffix, isF16 := strings.CutPrefix(variant.ID, "gpu_f16")
	if !isF16 || variant.Quantization != "f16" || variant.Supported ||
		(suffix != "" && !strings.HasPrefix(suffix, "_")) ||
		plan.runtime.execution != nil {
		return "", false
	}
	lower := strings.ToLower(plan.runtime.modelPath)
	for _, marker := range []string{"cuda", "tensorrt", "directml", "q4"} {
		if strings.Contains(lower, marker) {
			return "", false
		}
	}
	if len(plan.runtime.externalData) > 125 {
		return "", false
	}
	var total int64
	for _, asset := range plan.assets {
		if !validPinnedAsset(asset) ||
			asset.Size > contract.MaxPrivacyModelByteCount ||
			total > contract.MaxPrivacyModelByteCount-asset.Size {
			return "", false
		}
		total += asset.Size
	}
	return suffix, total == variant.BytesTotal
}

// tokenizerPadTokenID reads the pad id from tokenizer.json: the padding
// section when the tokenizer pads, otherwise its special pad token. The
// model config is not consulted; exported configs often carry the base
// model's pad id, which may not match the tokenizer.
func tokenizerPadTokenID(document []byte) (int, bool) {
	var tokenizer struct {
		Padding *struct {
			PadID *int `json:"pad_id"`
		} `json:"padding"`
		AddedTokens []struct {
			ID      *int   `json:"id"`
			Content string `json:"content"`
			Special bool   `json:"special"`
		} `json:"added_tokens"`
	}
	if json.Unmarshal(document, &tokenizer) != nil {
		return 0, false
	}
	if tokenizer.Padding != nil && tokenizer.Padding.PadID != nil {
		id := *tokenizer.Padding.PadID
		return id, id >= 0 && id <= maxPadTokenID
	}
	found := -1
	for _, token := range tokenizer.AddedTokens {
		if !token.Special || token.ID == nil ||
			(token.Content != "<pad>" && token.Content != "[PAD]") {
			continue
		}
		if found >= 0 && found != *token.ID {
			return 0, false
		}
		found = *token.ID
	}
	return found, found >= 0 && found <= maxPadTokenID
}

// localStaticInputLength returns the sequence length when every graph input
// is exactly one of the runtime's int64 inputs with shape [1, length] and the
// logits output is float32, which is what the worker reads, and 0 otherwise.
// Many FP16 exports also emit float16 logits; those are not offered.
func localStaticInputLength(
	ctx context.Context,
	directory string,
	runtimeSpec runtimeSpec,
) (int, error) {
	file, _, _, err := openLocalAsset(directory, runtimeSpec.modelPath, 0)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	inputs, outputs, err := readONNXGraphIO(bufio.NewReaderSize(file, 64<<10))
	if err != nil {
		if errors.Is(err, errONNXUnreadable) {
			return 0, nil
		}
		return 0, ErrLocalSource
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	expected := []string{runtimeSpec.inputNames.InputIDs, runtimeSpec.inputNames.AttentionMask}
	if runtimeSpec.inputNames.TokenTypeIDs != nil {
		expected = append(expected, *runtimeSpec.inputNames.TokenTypeIDs)
	}
	sort.Strings(expected)
	if len(inputs) != len(expected) {
		return 0, nil
	}
	names := make([]string, 0, len(inputs))
	length := int64(0)
	for _, input := range inputs {
		if input.elementType != onnxElementTypeInt64 || len(input.dims) != 2 ||
			input.dims[0] != 1 || input.dims[1] <= 0 || input.dims[1] > 131_072 ||
			(length != 0 && input.dims[1] != length) {
			return 0, nil
		}
		length = input.dims[1]
		names = append(names, input.name)
	}
	sort.Strings(names)
	for index := range names {
		if names[index] != expected[index] {
			return 0, nil
		}
	}
	for _, output := range outputs {
		if output.name == runtimeSpec.outputName {
			if output.elementType != onnxElementTypeFloat {
				return 0, nil
			}
			return int(length), nil
		}
	}
	return 0, nil
}

const (
	onnxElementTypeFloat = 1
	onnxElementTypeInt64 = 7
)

var errONNXUnreadable = errors.New("onnx graph inputs unreadable")

type onnxTensorInfo struct {
	name        string
	elementType int64
	// Dimensions without a fixed value are recorded as -1.
	dims []int64
}

// readONNXGraphIO walks ModelProto.graph (7) and returns every
// GraphProto.input (11) and GraphProto.output (12) entry.
func readONNXGraphIO(reader *bufio.Reader) ([]onnxTensorInfo, []onnxTensorInfo, error) {
	for fields := 0; fields < maxONNXGraphFields; fields++ {
		field, wireType, err := readProtoKey(reader)
		if err != nil {
			return nil, nil, errONNXUnreadable
		}
		if field != 7 || wireType != 2 {
			if err := skipProtoValue(reader, wireType); err != nil {
				return nil, nil, errONNXUnreadable
			}
			continue
		}
		graphLength, err := readProtoVarint(reader)
		if err != nil {
			return nil, nil, errONNXUnreadable
		}
		return readONNXGraph(reader, graphLength)
	}
	return nil, nil, errONNXUnreadable
}

func readONNXGraph(
	reader *bufio.Reader,
	length uint64,
) ([]onnxTensorInfo, []onnxTensorInfo, error) {
	limited := &countingReader{reader: reader, remaining: length}
	inputs := make([]onnxTensorInfo, 0, 3)
	outputs := make([]onnxTensorInfo, 0, 1)
	for fields := 0; fields < maxONNXGraphFields && limited.remaining > 0; fields++ {
		field, wireType, err := readProtoKey(limited)
		if err != nil {
			return nil, nil, errONNXUnreadable
		}
		if (field == 11 || field == 12) && wireType == 2 {
			message, err := readProtoBytes(limited, 1<<20)
			if err != nil {
				return nil, nil, errONNXUnreadable
			}
			info, err := parseONNXValueInfo(message)
			if err != nil {
				return nil, nil, errONNXUnreadable
			}
			if field == 11 {
				inputs = append(inputs, info)
			} else {
				outputs = append(outputs, info)
			}
			if len(inputs) > 64 || len(outputs) > 64 {
				return nil, nil, errONNXUnreadable
			}
			continue
		}
		if err := skipProtoValue(limited, wireType); err != nil {
			return nil, nil, errONNXUnreadable
		}
	}
	if len(inputs) == 0 {
		return nil, nil, errONNXUnreadable
	}
	return inputs, outputs, nil
}

func parseONNXValueInfo(message []byte) (onnxTensorInfo, error) {
	var input onnxTensorInfo
	input.elementType = -1
	err := walkProtoMessage(message, func(field uint64, value []byte, _ uint64) error {
		switch field {
		case 1:
			input.name = string(value)
		case 2:
			return walkProtoMessage(value, func(field uint64, tensor []byte, _ uint64) error {
				if field != 1 {
					return nil
				}
				return walkProtoMessage(tensor, func(field uint64, value []byte, number uint64) error {
					switch field {
					case 1:
						input.elementType = int64(number)
					case 2:
						return walkProtoMessage(value, func(field uint64, dimension []byte, _ uint64) error {
							if field != 1 {
								return nil
							}
							value := int64(-1)
							err := walkProtoMessage(dimension, func(field uint64, _ []byte, number uint64) error {
								if field == 1 {
									value = int64(number)
								}
								return nil
							})
							input.dims = append(input.dims, value)
							return err
						})
					}
					return nil
				})
			})
		}
		return nil
	})
	if err != nil || input.name == "" {
		return onnxTensorInfo{}, errONNXUnreadable
	}
	return input, nil
}

type countingReader struct {
	reader    *bufio.Reader
	remaining uint64
}

func (reader *countingReader) ReadByte() (byte, error) {
	if reader.remaining == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	value, err := reader.reader.ReadByte()
	if err == nil {
		reader.remaining--
	}
	return value, err
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if uint64(len(buffer)) > reader.remaining {
		buffer = buffer[:reader.remaining]
	}
	count, err := reader.reader.Read(buffer)
	reader.remaining -= uint64(count)
	return count, err
}

func (reader *countingReader) Discard(count int) (int, error) {
	if uint64(count) > reader.remaining {
		return 0, io.ErrUnexpectedEOF
	}
	discarded, err := reader.reader.Discard(count)
	reader.remaining -= uint64(discarded)
	return discarded, err
}

type protoReader interface {
	io.ByteReader
	io.Reader
	Discard(int) (int, error)
}

func readProtoVarint(reader io.ByteReader) (uint64, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		next, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		value |= uint64(next&0x7f) << shift
		if next < 0x80 {
			return value, nil
		}
	}
	return 0, errONNXUnreadable
}

func readProtoKey(reader io.ByteReader) (uint64, uint64, error) {
	key, err := readProtoVarint(reader)
	if err != nil {
		return 0, 0, err
	}
	if key>>3 == 0 {
		return 0, 0, errONNXUnreadable
	}
	return key >> 3, key & 7, nil
}

func readProtoBytes(reader protoReader, limit uint64) ([]byte, error) {
	length, err := readProtoVarint(reader)
	if err != nil || length > limit {
		return nil, errONNXUnreadable
	}
	value := make([]byte, length)
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, err
	}
	return value, nil
}

func skipProtoValue(reader protoReader, wireType uint64) error {
	switch wireType {
	case 0:
		_, err := readProtoVarint(reader)
		return err
	case 1:
		_, err := reader.Discard(8)
		return err
	case 2:
		length, err := readProtoVarint(reader)
		if err != nil || length > uint64(contract.MaxPrivacyModelByteCount) {
			return errONNXUnreadable
		}
		for length > 0 {
			step := min(length, 1<<30)
			if _, err := reader.Discard(int(step)); err != nil {
				return err
			}
			length -= step
		}
		return nil
	case 5:
		_, err := reader.Discard(4)
		return err
	default:
		return errONNXUnreadable
	}
}

// walkProtoMessage calls visit for every field of an in-memory message:
// length-delimited values arrive as bytes, varints as number.
func walkProtoMessage(
	message []byte,
	visit func(field uint64, value []byte, number uint64) error,
) error {
	for len(message) > 0 {
		key, size := decodeProtoVarint(message)
		if size == 0 || key>>3 == 0 {
			return errONNXUnreadable
		}
		message = message[size:]
		field, wireType := key>>3, key&7
		switch wireType {
		case 0:
			number, size := decodeProtoVarint(message)
			if size == 0 {
				return errONNXUnreadable
			}
			message = message[size:]
			if err := visit(field, nil, number); err != nil {
				return err
			}
		case 1:
			if len(message) < 8 {
				return errONNXUnreadable
			}
			message = message[8:]
		case 2:
			length, size := decodeProtoVarint(message)
			if size == 0 || length > uint64(len(message)-size) {
				return errONNXUnreadable
			}
			value := message[size : size+int(length)]
			message = message[size+int(length):]
			if err := visit(field, value, 0); err != nil {
				return err
			}
		case 5:
			if len(message) < 4 {
				return errONNXUnreadable
			}
			message = message[4:]
		default:
			return errONNXUnreadable
		}
	}
	return nil
}

func decodeProtoVarint(buffer []byte) (uint64, int) {
	var value uint64
	for index := 0; index < len(buffer) && index < 10; index++ {
		value |= uint64(buffer[index]&0x7f) << (7 * uint(index))
		if buffer[index] < 0x80 {
			return value, index + 1
		}
	}
	return 0, 0
}
