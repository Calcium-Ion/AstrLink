package privacy

import (
	"bytes"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/jsonedit"
)

const maxJSONDepth = 128

type jsonDocument struct {
	body          []byte
	duplicateKeys bool
}

type extractedSegment struct {
	Segment
	validateStructuredJSON bool
}

func extractDocument(
	protocol contract.ProtocolID,
	body []byte,
	options InspectionOptions,
) (jsonDocument, []extractedSegment, error) {
	roots, supported := requestRoots(protocol, options)
	if !supported || len(bytes.TrimSpace(body)) == 0 {
		return jsonDocument{}, nil, nil
	}
	if !utf8.Valid(body) {
		return jsonDocument{}, nil, ErrUnsafeInput
	}
	value, duplicateKeys, err := parsePrivacyJSON(body, true)
	if err != nil || duplicateKeys {
		return jsonDocument{}, nil, ErrUnsafeInput
	}
	root, ok := value.(map[string]any)
	if !ok || root == nil {
		return jsonDocument{}, nil, ErrUnsafeInput
	}

	document := jsonDocument{body: body, duplicateKeys: duplicateKeys}
	var extracted extraction
	extracted.segments = make([]extractedSegment, 0)
	for _, top := range roots {
		if child, exists := root[top.key]; exists {
			extracted.walk(child, "/"+escapeJSONPointer(top.key), 1, top.at, scope{})
		}
	}
	return document, extracted.segments, nil
}

// encodeJSONValue encodes only the value being changed. The parsed map is used
// for inspection, never to serialize the request, preserving other fields and
// their key order.
func encodeJSONValue(value any) ([]byte, error) {
	return jsonedit.Encode(value)
}

// stringReplacement is the new raw JSON for a string the request still holds
// as original.
type stringReplacement struct {
	original string
	raw      []byte
}

// replaceStrings rewrites the strings at the given JSON pointers in one pass
// over the body. Setting them one path at a time rescans and copies the whole
// body for each, which made a long conversation with many redacted messages
// take seconds on every turn. A string that is missing or no longer holds the
// inspected text fails the rewrite.
func (document *jsonDocument) replaceStrings(replacements map[string]stringReplacement) error {
	if len(replacements) == 0 {
		return nil
	}
	// Only containers on the way to a replaced string are walked.
	wanted := make(map[string]struct{}, 2*len(replacements))
	for pointer := range replacements {
		for index := 1; index < len(pointer); index++ {
			if pointer[index] == '/' {
				wanted[pointer[:index]] = struct{}{}
			}
		}
		wanted[pointer] = struct{}{}
	}
	trimmed := bytes.TrimLeft(document.body, " \t\r\n")
	offset := len(document.body) - len(trimmed)
	type edit struct {
		start, end int
		raw        []byte
	}
	edits := make([]edit, 0, len(replacements))
	failed := false
	var walk func(pointer string, container ojson.Result)
	walk = func(pointer string, container ojson.Result) {
		container.ForEach(func(key, child ojson.Result) bool {
			childPointer := pointer + "/"
			if key.Type == ojson.String {
				childPointer += escapeJSONPointer(key.Str)
			} else {
				childPointer += jsonIndex(int(key.Num))
			}
			if _, ok := wanted[childPointer]; !ok {
				return true
			}
			if replacement, ok := replacements[childPointer]; ok {
				if child.Type != ojson.String || child.Str != replacement.original {
					failed = true
					return false
				}
				start := offset + child.Index
				edits = append(edits, edit{start: start, end: start + len(child.Raw), raw: replacement.raw})
				return true
			}
			if child.IsObject() || child.IsArray() {
				walk(childPointer, child)
			}
			return !failed
		})
	}
	walk("", ojson.ParseBytes(trimmed))
	if failed || len(edits) != len(replacements) {
		return ErrUnsafeRewrite
	}
	sort.Slice(edits, func(left, right int) bool {
		return edits[left].start < edits[right].start
	})
	updated := make([]byte, 0, len(document.body))
	written := 0
	for _, edit := range edits {
		if edit.start < written || edit.end > len(document.body) ||
			document.body[edit.start] != '"' {
			return ErrUnsafeRewrite
		}
		updated = append(updated, document.body[written:edit.start]...)
		updated = append(updated, edit.raw...)
		written = edit.end
	}
	document.body = append(updated, document.body[written:]...)
	return nil
}

// isNonTextMediaChild recognises non-text media inside a payload by its
// shape. Payload field names are chosen by the tool author and caller, so a
// field merely named "bytes" or "file_uri" is still read unless the value
// around it has the shape of a real media block.
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

func hasAnyJSONKey(value map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, exists := value[key]; exists {
			return true
		}
	}
	return false
}

func inspectJSONStructure(body []byte) (bool, error) {
	_, duplicate, err := parsePrivacyJSON(body, false)
	return duplicate, err
}

type placedFinding struct {
	Finding
	Value       string
	Placeholder string
	// quoted marks a JSON number inside a structured tool payload; the
	// placeholder is written as a string so the payload stays valid JSON.
	quoted bool
}

type rewriteOutcome struct {
	Body           []byte
	Redactions     []Redaction
	NoticeInjected bool
}

func rewriteDocument(
	document jsonDocument,
	extracted []extractedSegment,
	findings []Finding,
	allocator *placeholderAllocator,
	protocol contract.ProtocolID,
	notice string,
) (rewriteOutcome, error) {
	placed, redactions, err := assignPlaceholders(extracted, findings, allocator)
	if err != nil {
		return rewriteOutcome{}, err
	}
	grouped := make(map[int][]placedFinding)
	for _, item := range placed {
		grouped[item.Segment] = append(grouped[item.Segment], item)
	}
	replacements := make(map[string]stringReplacement, len(grouped))
	for segmentIndex, segmentFindings := range grouped {
		if segmentIndex < 0 || segmentIndex >= len(extracted) {
			return rewriteOutcome{}, ErrUnsafeRewrite
		}
		segment := extracted[segmentIndex]
		redacted, err := redactStringWithPlaceholders(segment.Value, segmentFindings)
		if err != nil {
			return rewriteOutcome{}, err
		}
		if segment.validateStructuredJSON && !validStructuredJSON(redacted) {
			return rewriteOutcome{}, ErrUnsafeRewrite
		}
		raw, err := encodeJSONValue(redacted)
		if _, duplicate := replacements[segment.Path]; err != nil || duplicate {
			return rewriteOutcome{}, ErrUnsafeRewrite
		}
		replacements[segment.Path] = stringReplacement{original: segment.Value, raw: raw}
	}
	if err := document.replaceStrings(replacements); err != nil {
		return rewriteOutcome{}, ErrUnsafeRewrite
	}
	// The note is only worth its tokens when an opaque marker actually reached
	// the wire, so it is decided after allocation rather than from policy alone.
	// An empty notice means the policy or the listed skill already covers it.
	injected := false
	if notice != "" && anyTokenPlaceholder(redactions) {
		injected, err = injectPlaceholderNotice(&document, protocol, notice)
		if err != nil {
			return rewriteOutcome{}, ErrUnsafeRewrite
		}
	}
	return rewriteOutcome{
		Body:           document.body,
		Redactions:     redactions,
		NoticeInjected: injected,
	}, nil
}

func anyTokenPlaceholder(redactions []Redaction) bool {
	for _, redaction := range redactions {
		if redaction.Style == contract.PlaceholderStyleToken {
			return true
		}
	}
	return false
}

func validStructuredJSON(value string) bool {
	duplicateKeys, err := inspectJSONStructure([]byte(value))
	return err == nil && !duplicateKeys
}

func assignPlaceholders(
	extracted []extractedSegment,
	findings []Finding,
	allocator *placeholderAllocator,
) ([]placedFinding, []Redaction, error) {
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
		// Findings in a structured payload were aligned by the engine; the
		// scan only tells a number, which needs quotes, from string contents.
		structured := extracted[segmentIndex].validateStructuredJSON
		var literals structuredJSONLiterals
		if structured {
			literals = scanStructuredJSONLiterals(value)
		}
		chosen, err := selectNonOverlappingFindings(value, segmentFindings)
		if err != nil {
			return nil, nil, err
		}
		for _, finding := range chosen {
			selected = append(selected, placedFinding{
				Finding: finding,
				Value:   value[finding.Start:finding.End],
				quoted:  structured && literals.isNumber(finding.Start, finding.End),
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
	type allocation struct {
		placeholder string
		style       contract.PlaceholderStyle
	}
	placeholderFor := make(map[kindKey]allocation, len(selected))
	for _, item := range selected {
		key := kindKey{kind: item.Kind, value: item.Value}
		if _, exists := placeholderFor[key]; exists {
			continue
		}
		placeholder, style, err := allocator.allocate(item.Kind, item.Value)
		if err != nil {
			return nil, nil, err
		}
		placeholderFor[key] = allocation{placeholder: placeholder, style: style}
	}

	redactions := make([]Redaction, 0, len(placeholderFor))
	seenPlaceholder := make(map[string]struct{}, len(placeholderFor))
	for index := range selected {
		key := kindKey{kind: selected[index].Kind, value: selected[index].Value}
		assigned := placeholderFor[key]
		selected[index].Placeholder = assigned.placeholder
		if _, exists := seenPlaceholder[assigned.placeholder]; exists {
			continue
		}
		seenPlaceholder[assigned.placeholder] = struct{}{}
		redactions = append(redactions, Redaction{
			Placeholder: assigned.placeholder,
			Kind:        selected[index].Kind,
			Value:       selected[index].Value,
			Style:       assigned.style,
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

	selector := newSpanSelector(candidates)
	selected := make([]Finding, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Start < 0 || candidate.End > len(value) || candidate.End <= candidate.Start {
			return nil, ErrUnsafeRewrite
		}
		if selector.accept(candidate) {
			selected = append(selected, candidate)
		}
	}
	return selected, nil
}

// spanSelector accepts spans of one value in the order offered and refuses
// any span that overlaps one it already accepted. A span overlaps an accepted
// one exactly when some accepted span starting before its end also ends after
// its start, so it keeps the largest accepted end per start in a Fenwick tree
// and answers in logarithmic time even for a value with many findings.
type spanSelector struct {
	starts []int
	maxEnd []int
}

func newSpanSelector(findings []Finding) *spanSelector {
	starts := make([]int, len(findings))
	for index, finding := range findings {
		starts[index] = finding.Start
	}
	slices.Sort(starts)
	starts = slices.Compact(starts)
	return &spanSelector{starts: starts, maxEnd: make([]int, len(starts))}
}

// accept takes a span from the findings the selector was built from.
func (selector *spanSelector) accept(finding Finding) bool {
	before, _ := slices.BinarySearch(selector.starts, finding.End)
	for index := before; index > 0; index -= index & -index {
		if selector.maxEnd[index-1] > finding.Start {
			return false
		}
	}
	rank, _ := slices.BinarySearch(selector.starts, finding.Start)
	for index := rank + 1; index <= len(selector.maxEnd); index += index & -index {
		selector.maxEnd[index-1] = max(selector.maxEnd[index-1], finding.End)
	}
	return true
}

// redactStringWithPlaceholders writes value once with the non-overlapping
// findings replaced, rather than copying it again for every finding.
func redactStringWithPlaceholders(value string, findings []placedFinding) (string, error) {
	ordered := append([]placedFinding(nil), findings...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].Start < ordered[right].Start
	})
	var redacted strings.Builder
	redacted.Grow(len(value))
	written := 0
	for _, finding := range ordered {
		if finding.Placeholder == "" ||
			finding.Start < written || finding.End > len(value) || finding.End <= finding.Start {
			return "", ErrUnsafeRewrite
		}
		redacted.WriteString(value[written:finding.Start])
		if finding.quoted {
			redacted.WriteString(`"` + finding.Placeholder + `"`)
		} else {
			redacted.WriteString(finding.Placeholder)
		}
		written = finding.End
	}
	redacted.WriteString(value[written:])
	return redacted.String(), nil
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
