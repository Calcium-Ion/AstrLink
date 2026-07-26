package privacy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

const maxJSONDepth = 128
const maxExtractedSegments = 32_768

type jsonDocument struct {
	root          map[string]any
	duplicateKeys bool
}

type extractedSegment struct {
	Segment
	set                    func(string)
	validateStructuredJSON bool
}

type jsonTraversalContext uint8

const (
	jsonContentContext jsonTraversalContext = iota
	jsonSchemaContext
	jsonToolPayloadContext
)

func extractDocument(protocol contract.ProtocolID, body []byte) (jsonDocument, []extractedSegment, error) {
	roots, supported := protocolRoots(protocol)
	if !supported || len(bytes.TrimSpace(body)) == 0 {
		return jsonDocument{}, nil, nil
	}
	if !utf8.Valid(body) {
		return jsonDocument{}, nil, ErrUnsafeInput
	}
	duplicateKeys, err := inspectJSONStructure(body)
	if err != nil || duplicateKeys {
		return jsonDocument{}, nil, ErrUnsafeInput
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil || root == nil {
		return jsonDocument{}, nil, ErrUnsafeInput
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return jsonDocument{}, nil, ErrUnsafeInput
	}

	document := jsonDocument{root: root, duplicateKeys: duplicateKeys}
	extracted := make([]extractedSegment, 0)
	overflow := false
	for _, key := range roots {
		value, exists := root[key]
		if !exists || skipJSONChild(root, key, value, jsonContentContext) {
			continue
		}
		key := key
		walkJSONStrings(value, func(replacement any) {
			root[key] = replacement
		}, "/"+escapeJSONPointer(key), 1, jsonContentContext, &extracted, &overflow)
	}
	if overflow {
		return jsonDocument{}, nil, ErrUnsafeInput
	}
	return document, extracted, nil
}

func protocolRoots(protocol contract.ProtocolID) ([]string, bool) {
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		return []string{"instructions", "input", "prompt", "tools"}, true
	case contract.ProtocolOpenAIChat:
		return []string{"messages", "tools", "functions"}, true
	case contract.ProtocolOpenAICompletions:
		return []string{"prompt", "suffix"}, true
	case contract.ProtocolAnthropicMessages:
		return []string{"system", "messages", "tools"}, true
	case contract.ProtocolGoogleGenerateContent:
		return []string{"systemInstruction", "contents", "tools"}, true
	default:
		return nil, false
	}
}

func walkJSONStrings(
	value any,
	set func(any),
	path string,
	depth int,
	context jsonTraversalContext,
	extracted *[]extractedSegment,
	overflow *bool,
) {
	if *overflow || depth > maxJSONDepth {
		return
	}
	switch typed := value.(type) {
	case string:
		if len(*extracted) >= maxExtractedSegments {
			*overflow = true
			return
		}
		*extracted = append(*extracted, extractedSegment{
			Segment: Segment{Path: path, Value: typed},
			set: func(replacement string) {
				set(replacement)
			},
			validateStructuredJSON: context == jsonToolPayloadContext &&
				looksLikeStructuredJSON(typed),
		})
	case []any:
		for index, child := range typed {
			index := index
			walkJSONStrings(child, func(replacement any) {
				typed[index] = replacement
			}, path+"/"+jsonIndex(index), depth+1, context, extracted, overflow)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := typed[key]
			if skipJSONChild(typed, key, child, context) {
				continue
			}
			key := key
			childContext := nextJSONTraversalContext(typed, key, context)
			walkJSONStrings(child, func(replacement any) {
				typed[key] = replacement
			}, path+"/"+escapeJSONPointer(key), depth+1, childContext, extracted, overflow)
		}
	}
}

func skipJSONChild(
	parent map[string]any,
	key string,
	child any,
	context jsonTraversalContext,
) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))

	typeName, _ := parent["type"].(string)
	typeName = strings.ToLower(strings.ReplaceAll(typeName, "_", ""))

	// Tool payload field names are controlled by the tool author and caller.
	// Names that resemble protocol media fields (for example "bytes" or
	// "file_uri") are still ordinary inspectable arguments unless the
	// surrounding value has the shape of a real non-text media block.
	if context == jsonToolPayloadContext {
		return isNonTextMediaChild(parent, normalized, child, typeName)
	}

	switch normalized {
	case "b64json", "imageurl", "inputaudio", "inlinedata", "filedata",
		"fileuri", "blob", "bytes":
		return true
	}

	switch normalized {
	case "source":
		if childMap, ok := child.(map[string]any); ok {
			sourceType, _ := childMap["type"].(string)
			sourceType = strings.ToLower(sourceType)
			if sourceType == "base64" || sourceType == "url" {
				return true
			}
		}
		if typeName == "image" || typeName == "document" || typeName == "audio" {
			return true
		}
	case "data":
		_, hasMIMEType := parent["mime_type"]
		_, hasCamelMIMEType := parent["mimeType"]
		if hasMIMEType || hasCamelMIMEType || typeName == "base64" ||
			typeName == "image" || typeName == "audio" {
			return true
		}
	case "url":
		if typeName == "image" || typeName == "imageurl" ||
			typeName == "document" || typeName == "audio" {
			return true
		}
	}

	if context == jsonSchemaContext {
		switch normalized {
		case "type", "id", "format", "required", "enum", "const", "pattern",
			"$ref", "propertyordering":
			return true
		}
	}

	switch normalized {
	case "model", "stream", "role", "type", "id", "callid", "toolcallid",
		"status", "finishreason", "stopreason", "mimetype", "format":
		return true
	case "name":
		if strings.Contains(typeName, "function") || strings.Contains(typeName, "tool") ||
			hasAnyJSONKey(parent, "arguments", "args", "parameters", "input_schema",
				"inputSchema", "response") ||
			hasAnyJSONKey(parent, "role") {
			return true
		}
	}
	return false
}

func isNonTextMediaChild(
	parent map[string]any,
	normalized string,
	child any,
	typeName string,
) bool {
	childMap, childIsMap := child.(map[string]any)
	parentHasMIME := hasAnyJSONKey(parent, "mime_type", "mimeType")
	childHasMIME := childIsMap &&
		hasAnyJSONKey(childMap, "mime_type", "mimeType")
	mediaType := isNonTextMediaType(typeName)

	switch normalized {
	case "inlinedata":
		return childIsMap && childHasMIME && hasAnyJSONKey(childMap, "data")
	case "filedata":
		return childIsMap && childHasMIME &&
			hasAnyJSONKey(childMap, "file_uri", "fileUri")
	case "inputaudio":
		return mediaType ||
			(childIsMap && hasAnyJSONKey(childMap, "data") &&
				hasAnyJSONKey(childMap, "format"))
	case "imageurl":
		return mediaType || (childIsMap && hasAnyJSONKey(childMap, "url"))
	case "b64json", "fileuri", "blob", "bytes":
		return mediaType || parentHasMIME
	case "source":
		if childIsMap {
			sourceType, _ := childMap["type"].(string)
			sourceType = strings.ToLower(strings.ReplaceAll(sourceType, "_", ""))
			if sourceType == "base64" || sourceType == "url" {
				return true
			}
		}
		return mediaType
	case "data":
		return parentHasMIME || typeName == "base64" || mediaType
	case "url":
		return mediaType
	default:
		return false
	}
}

func isNonTextMediaType(typeName string) bool {
	switch typeName {
	case "image", "inputimage", "imageurl", "document", "inputfile", "file",
		"audio", "inputaudio", "base64", "blob", "binary":
		return true
	default:
		return false
	}
}

func nextJSONTraversalContext(
	parent map[string]any,
	key string,
	context jsonTraversalContext,
) jsonTraversalContext {
	if context == jsonToolPayloadContext {
		return jsonToolPayloadContext
	}
	if isToolPayloadField(parent, key) {
		return jsonToolPayloadContext
	}
	if context == jsonSchemaContext || isJSONSchemaField(key) {
		return jsonSchemaContext
	}
	return jsonContentContext
}

func isToolPayloadField(parent map[string]any, key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
	switch normalized {
	case "arguments", "args", "response":
		return true
	}

	typeName, _ := parent["type"].(string)
	typeName = strings.ToLower(strings.ReplaceAll(typeName, "_", ""))
	switch normalized {
	case "input", "output", "content":
		return strings.Contains(typeName, "tool") ||
			strings.Contains(typeName, "function") ||
			strings.Contains(typeName, "call")
	}
	return false
}

func isJSONSchemaField(key string) bool {
	switch strings.ToLower(strings.ReplaceAll(key, "_", "")) {
	case "parameters", "inputschema", "parametersjsonschema", "responsejsonschema",
		"jsonschema", "schema":
		return true
	default:
		return false
	}
}

func hasAnyJSONKey(value map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, exists := value[key]; exists {
			return true
		}
	}
	return false
}

func inspectJSONStructure(body []byte) (bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	duplicate, err := inspectJSONValue(decoder, 0)
	if err != nil {
		return false, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return false, ErrUnsafeInput
	}
	return duplicate, nil
}

func inspectJSONValue(decoder *json.Decoder, depth int) (bool, error) {
	if depth > maxJSONDepth {
		return false, ErrUnsafeInput
	}
	token, err := decoder.Token()
	if err != nil {
		return false, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return false, nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		duplicate := false
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return false, ErrUnsafeInput
			}
			if _, exists := seen[key]; exists {
				duplicate = true
			}
			seen[key] = struct{}{}
			childDuplicate, err := inspectJSONValue(decoder, depth+1)
			if err != nil {
				return false, err
			}
			duplicate = duplicate || childDuplicate
		}
		closeToken, err := decoder.Token()
		if err != nil || closeToken != json.Delim('}') {
			return false, ErrUnsafeInput
		}
		return duplicate, nil
	case '[':
		duplicate := false
		for decoder.More() {
			childDuplicate, err := inspectJSONValue(decoder, depth+1)
			if err != nil {
				return false, err
			}
			duplicate = duplicate || childDuplicate
		}
		closeToken, err := decoder.Token()
		if err != nil || closeToken != json.Delim(']') {
			return false, ErrUnsafeInput
		}
		return duplicate, nil
	default:
		return false, ErrUnsafeInput
	}
}

type placedFinding struct {
	Finding
	Value       string
	Placeholder string
}

func rewriteDocument(document jsonDocument, extracted []extractedSegment, findings []Finding) ([]byte, []Redaction, error) {
	placed, redactions, err := assignPlaceholders(extracted, findings)
	if err != nil {
		return nil, nil, err
	}
	grouped := make(map[int][]placedFinding)
	for _, item := range placed {
		grouped[item.Segment] = append(grouped[item.Segment], item)
	}
	for segmentIndex, segmentFindings := range grouped {
		if segmentIndex < 0 || segmentIndex >= len(extracted) {
			return nil, nil, ErrUnsafeRewrite
		}
		redacted, err := redactStringWithPlaceholders(extracted[segmentIndex].Value, segmentFindings)
		if err != nil {
			return nil, nil, err
		}
		if extracted[segmentIndex].validateStructuredJSON &&
			!validStructuredJSON(redacted) {
			return nil, nil, ErrUnsafeRewrite
		}
		extracted[segmentIndex].set(redacted)
	}
	var rewritten bytes.Buffer
	encoder := json.NewEncoder(&rewritten)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(document.root); err != nil {
		return nil, nil, ErrUnsafeRewrite
	}
	return bytes.TrimSuffix(rewritten.Bytes(), []byte{'\n'}), redactions, nil
}

func looksLikeStructuredJSON(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func validStructuredJSON(value string) bool {
	duplicateKeys, err := inspectJSONStructure([]byte(value))
	return err == nil && !duplicateKeys
}

func assignPlaceholders(extracted []extractedSegment, findings []Finding) ([]placedFinding, []Redaction, error) {
	grouped := make(map[int][]Finding)
	for _, finding := range findings {
		grouped[finding.Segment] = append(grouped[finding.Segment], finding)
	}
	selected := make([]placedFinding, 0, len(findings))
	for segmentIndex, segmentFindings := range grouped {
		if segmentIndex < 0 || segmentIndex >= len(extracted) {
			return nil, nil, ErrUnsafeRewrite
		}
		value := extracted[segmentIndex].Value
		chosen, err := selectNonOverlappingFindings(value, segmentFindings)
		if err != nil {
			return nil, nil, err
		}
		for _, finding := range chosen {
			selected = append(selected, placedFinding{
				Finding: finding,
				Value:   value[finding.Start:finding.End],
			})
		}
	}
	sort.Slice(selected, func(left, right int) bool {
		a, b := selected[left], selected[right]
		if a.Segment != b.Segment {
			return a.Segment < b.Segment
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End > b.End
		}
		return a.Kind < b.Kind
	})

	type kindKey struct {
		kind  Kind
		value string
	}
	distinctOrder := make(map[Kind][]string)
	seenValue := make(map[kindKey]struct{})
	for _, item := range selected {
		key := kindKey{kind: item.Kind, value: item.Value}
		if _, exists := seenValue[key]; exists {
			continue
		}
		seenValue[key] = struct{}{}
		distinctOrder[item.Kind] = append(distinctOrder[item.Kind], item.Value)
	}
	placeholderFor := make(map[kindKey]string, len(seenValue))
	for kind, values := range distinctOrder {
		base := replacementFor(kind)
		if len(values) == 1 {
			placeholderFor[kindKey{kind: kind, value: values[0]}] = base
			continue
		}
		for index, value := range values {
			placeholderFor[kindKey{kind: kind, value: value}] = indexedReplacement(base, index)
		}
	}

	redactions := make([]Redaction, 0, len(seenValue))
	seenPlaceholder := make(map[string]struct{}, len(seenValue))
	for index := range selected {
		key := kindKey{kind: selected[index].Kind, value: selected[index].Value}
		placeholder := placeholderFor[key]
		selected[index].Placeholder = placeholder
		if _, exists := seenPlaceholder[placeholder]; exists {
			continue
		}
		seenPlaceholder[placeholder] = struct{}{}
		redactions = append(redactions, Redaction{
			Placeholder: placeholder,
			Kind:        selected[index].Kind,
			Value:       selected[index].Value,
		})
	}
	sort.Slice(redactions, func(left, right int) bool {
		return redactions[left].Placeholder < redactions[right].Placeholder
	})
	return selected, redactions, nil
}

func selectNonOverlappingFindings(value string, findings []Finding) ([]Finding, error) {
	candidates := append([]Finding(nil), findings...)
	sort.Slice(candidates, func(left, right int) bool {
		a, b := candidates[left], candidates[right]
		priorityA, priorityB := kindPriority(a.Kind), kindPriority(b.Kind)
		if priorityA != priorityB {
			return priorityA > priorityB
		}
		if lengthA, lengthB := a.End-a.Start, b.End-b.Start; lengthA != lengthB {
			return lengthA > lengthB
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return a.Kind < b.Kind
	})

	selected := make([]Finding, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Start < 0 || candidate.End > len(value) || candidate.End <= candidate.Start {
			return nil, ErrUnsafeRewrite
		}
		overlap := false
		for _, existing := range selected {
			if candidate.Start < existing.End && existing.Start < candidate.End {
				overlap = true
				break
			}
		}
		if !overlap {
			selected = append(selected, candidate)
		}
	}
	return selected, nil
}

func redactStringWithPlaceholders(value string, findings []placedFinding) (string, error) {
	ordered := append([]placedFinding(nil), findings...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].Start > ordered[right].Start
	})
	for _, finding := range ordered {
		if finding.Placeholder == "" ||
			finding.Start < 0 || finding.End > len(value) || finding.End <= finding.Start {
			return "", ErrUnsafeRewrite
		}
		value = value[:finding.Start] + finding.Placeholder + value[finding.End:]
	}
	return value, nil
}

func replacementFor(kind Kind) string {
	switch kind {
	case KindEmail:
		return "<PRIVATE_EMAIL>"
	case KindPhone:
		return "<PRIVATE_PHONE>"
	case KindAccount:
		return "<PRIVATE_ACCOUNT_NUMBER>"
	case KindPaymentCard:
		return "<PRIVATE_PAYMENT_CARD>"
	case KindIPAddress:
		return "<PRIVATE_IP_ADDRESS>"
	case KindURL:
		return "<PRIVATE_URL>"
	case KindCommonSecret:
		return "<SECRET>"
	case KindAddress:
		return "<PRIVATE_ADDRESS>"
	case KindDate:
		return "<PRIVATE_DATE>"
	case KindPerson:
		return "<PRIVATE_PERSON>"
	default:
		return "<PRIVATE>"
	}
}

func indexedReplacement(base string, index int) string {
	if strings.HasPrefix(base, "<") && strings.HasSuffix(base, ">") {
		return base[:len(base)-1] + "_" + strconv.Itoa(index) + ">"
	}
	return base + "_" + strconv.Itoa(index)
}

func kindPriority(kind Kind) int {
	switch kind {
	case KindCommonSecret:
		return 700
	case KindPaymentCard:
		return 600
	case KindEmail:
		return 500
	case KindAccount:
		return 400
	case KindPhone:
		return 300
	case KindURL:
		return 200
	case KindIPAddress:
		return 100
	case KindAddress:
		return 90
	case KindDate:
		return 80
	case KindPerson:
		return 70
	default:
		return 0
	}
}

func escapeJSONPointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func jsonIndex(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = digits[value%10]
		value /= 10
	}
	return string(buffer[index:])
}
