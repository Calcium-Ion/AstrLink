package ingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

const (
	maxQueuedRestoreBytes  = 256 << 10
	maxQueuedRestoreFrames = 128
)

type textReplacement struct {
	placeholder string
	value       string
}

type restoreLogicalFrameKind uint8

const (
	restoreFrameJSON restoreLogicalFrameKind = iota
	restoreFrameSSE
	restoreFrameRaw
)

type restoreLogicalFrame struct {
	kind     restoreLogicalFrameKind
	original []byte
	jsonData []byte
	prefix   []byte
}

func newJSONRestoreFrame(prefix, data []byte) restoreLogicalFrame {
	original := make([]byte, 0, len(prefix)+len(data))
	original = append(original, prefix...)
	original = append(original, data...)
	return restoreLogicalFrame{
		kind:     restoreFrameJSON,
		original: original,
		jsonData: append([]byte(nil), data...),
		prefix:   append([]byte(nil), prefix...),
	}
}

func newSSERestoreFrame(raw, data []byte) restoreLogicalFrame {
	return restoreLogicalFrame{
		kind:     restoreFrameSSE,
		original: append([]byte(nil), raw...),
		jsonData: append([]byte(nil), data...),
	}
}

func newRawRestoreFrame(raw []byte) restoreLogicalFrame {
	return restoreLogicalFrame{
		kind:     restoreFrameRaw,
		original: append([]byte(nil), raw...),
	}
}

func (frame restoreLogicalFrame) render(data []byte) []byte {
	switch frame.kind {
	case restoreFrameSSE:
		return renderSSEData(frame.original, data)
	case restoreFrameRaw:
		return frame.original
	default:
		out := make([]byte, 0, len(frame.prefix)+len(data))
		out = append(out, frame.prefix...)
		out = append(out, data...)
		return out
	}
}

// visibleRestoreEngine queues complete protocol frames only while a restorable
// text channel ends in a strict prefix of a current-request placeholder.
// Evaluation is repeated from the original frames so a failed parse or limit
// breach can always emit the exact queued wire bytes.
type visibleRestoreEngine struct {
	protocol contract.ProtocolID
	// replacements substitute into decoded strings that this engine re-encodes.
	replacements []textReplacement
	// jsonReplacements substitute into a string that itself holds JSON, where
	// the surrounding quoting is not ours to re-encode.
	jsonReplacements []textReplacement
	toolArguments    bool
	queue            []restoreLogicalFrame
	queueBytes       int
	restored         int
	restoredToolArgs int
	fallbacks        int
}

func newVisibleRestoreEngine(
	protocol contract.ProtocolID,
	redactions []privacy.Redaction,
	toolArguments bool,
) *visibleRestoreEngine {
	seen := make(map[string]struct{}, len(redactions))
	replacements := make([]textReplacement, 0, len(redactions))
	jsonReplacements := make([]textReplacement, 0, len(redactions))
	for _, redaction := range redactions {
		if redaction.Placeholder == "" {
			continue
		}
		if _, ok := seen[redaction.Placeholder]; ok {
			continue
		}
		seen[redaction.Placeholder] = struct{}{}
		replacements = append(replacements, textReplacement{
			placeholder: redaction.Placeholder,
			value:       redaction.Value,
		})
		if escaped, ok := jsonEscapedContent(redaction.Value); ok {
			jsonReplacements = append(jsonReplacements, textReplacement{
				placeholder: redaction.Placeholder,
				value:       escaped,
			})
		}
	}
	byLongestPlaceholder := func(list []textReplacement) {
		sort.Slice(list, func(left, right int) bool {
			return len(list[left].placeholder) > len(list[right].placeholder)
		})
	}
	byLongestPlaceholder(replacements)
	byLongestPlaceholder(jsonReplacements)
	return &visibleRestoreEngine{
		protocol:         protocol,
		replacements:     replacements,
		jsonReplacements: jsonReplacements,
		toolArguments:    toolArguments,
	}
}

// jsonEscapedContent returns the value as it must appear inside a JSON string
// literal. A value that does not survive the round trip (invalid UTF-8) is
// refused rather than silently substituted with replacement characters.
func jsonEscapedContent(value string) (string, bool) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) < 2 {
		return "", false
	}
	content := string(encoded[1 : len(encoded)-1])
	var round string
	if err := json.Unmarshal(encoded, &round); err != nil || round != value {
		return "", false
	}
	return content, true
}

func (engine *visibleRestoreEngine) push(frame restoreLogicalFrame) []byte {
	if engine == nil {
		return frame.original
	}
	if len(frame.original) > maxQueuedRestoreBytes {
		out := engine.abort()
		out = append(out, frame.original...)
		engine.fallbacks++
		return out
	}
	engine.queue = append(engine.queue, frame)
	engine.queueBytes += len(frame.original)
	if engine.queueBytes > maxQueuedRestoreBytes ||
		len(engine.queue) > maxQueuedRestoreFrames {
		return engine.abort()
	}

	rendered, pending, restored, err := engine.evaluate()
	if err != nil {
		return engine.abort()
	}
	if pending {
		return nil
	}
	engine.queue = nil
	engine.queueBytes = 0
	engine.restored += restored.visible
	engine.restoredToolArgs += restored.toolArguments
	return rendered
}

func (engine *visibleRestoreEngine) terminal(raw []byte) []byte {
	if engine == nil || len(engine.queue) == 0 {
		return raw
	}
	out := engine.abort()
	out = append(out, raw...)
	return out
}

func (engine *visibleRestoreEngine) finish() []byte {
	if engine == nil || len(engine.queue) == 0 {
		return nil
	}
	return engine.abort()
}

func (engine *visibleRestoreEngine) abort() []byte {
	if engine == nil || len(engine.queue) == 0 {
		return nil
	}
	total := 0
	for _, frame := range engine.queue {
		total += len(frame.original)
	}
	out := make([]byte, 0, total)
	for _, frame := range engine.queue {
		out = append(out, frame.original...)
	}
	engine.fallbacks += len(engine.queue)
	engine.queue = nil
	engine.queueBytes = 0
	return out
}

type decodedRestoreFrame struct {
	frame    restoreLogicalFrame
	root     any
	modified bool
}

// restoreRefEncoding says how a replacement must be written into a reference.
type restoreRefEncoding uint8

const (
	// refEncodingPlain targets a decoded string that this engine re-encodes, so
	// the JSON encoder handles any escaping.
	refEncodingPlain restoreRefEncoding = iota
	// refEncodingJSONString targets a string that itself carries JSON, such as a
	// serialized tool-argument payload. The replacement must arrive pre-escaped
	// or it would break the inner document.
	refEncodingJSONString
)

type visibleTextRef struct {
	frame    int
	set      func(string)
	channel  string
	value    string
	encoding restoreRefEncoding
	// tool marks a reference the local agent is about to act on rather than one
	// the reader merely sees.
	tool bool
}

type restoreCounts struct {
	visible       int
	toolArguments int
}

func (engine *visibleRestoreEngine) evaluate() ([]byte, bool, restoreCounts, error) {
	decoded := make([]decodedRestoreFrame, len(engine.queue))
	byChannel := make(map[string][]*visibleTextRef)
	order := make([]string, 0, len(engine.queue))
	for index, frame := range engine.queue {
		if frame.kind == restoreFrameRaw {
			decoded[index] = decodedRestoreFrame{frame: frame}
			continue
		}
		root, err := decodeJSONValue(frame.jsonData)
		if err != nil {
			return nil, false, restoreCounts{}, err
		}
		decoded[index] = decodedRestoreFrame{frame: frame, root: root}
		refs := visibleResponseTextRefs(engine.protocol, root, index)
		if engine.toolArguments {
			refs = append(refs, toolArgumentTextRefs(engine.protocol, root, index)...)
		}
		for refIndex := range refs {
			ref := &refs[refIndex]
			if _, seen := byChannel[ref.channel]; !seen {
				order = append(order, ref.channel)
			}
			byChannel[ref.channel] = append(byChannel[ref.channel], ref)
		}
	}

	counts := restoreCounts{}
	type channelRewrite struct {
		refs  []*visibleTextRef
		value string
	}
	rewrites := make([]channelRewrite, 0, len(byChannel))
	for _, channel := range order {
		refs := byChannel[channel]
		var text strings.Builder
		for _, ref := range refs {
			text.WriteString(ref.value)
		}
		replacements := engine.replacements
		if refs[0].encoding == refEncodingJSONString {
			replacements = engine.jsonReplacements
		}
		restored, count, pending := restoreVisibleText(text.String(), replacements)
		if pending {
			return nil, true, restoreCounts{}, nil
		}
		if count > 0 {
			rewrites = append(rewrites, channelRewrite{refs: refs, value: restored})
			if refs[0].tool {
				counts.toolArguments += count
			} else {
				counts.visible += count
			}
		}
	}

	for _, rewrite := range rewrites {
		for index, ref := range rewrite.refs {
			value := ""
			if index == 0 {
				value = rewrite.value
			}
			ref.set(value)
			decoded[ref.frame].modified = true
		}
	}

	var out bytes.Buffer
	for _, item := range decoded {
		if !item.modified {
			_, _ = out.Write(item.frame.original)
			continue
		}
		encoded, err := marshalRestoreJSON(item.root)
		if err != nil {
			return nil, false, restoreCounts{}, err
		}
		_, _ = out.Write(item.frame.render(encoded))
	}
	return out.Bytes(), false, counts, nil
}

func marshalRestoreJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}

func decodeJSONValue(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values in one restore frame")
		}
		return nil, err
	}
	return root, nil
}

func restoreVisibleText(
	input string,
	replacements []textReplacement,
) (string, int, bool) {
	if input == "" || len(replacements) == 0 {
		return input, 0, false
	}
	hold := trailingRestorePrefix(input, replacements)
	stable := input
	pending := ""
	if hold > 0 {
		stable = input[:len(input)-hold]
		pending = input[len(input)-hold:]
	}

	var out strings.Builder
	cursor := 0
	count := 0
	for cursor < len(stable) {
		next := -1
		var selected *textReplacement
		for index := range replacements {
			replacement := &replacements[index]
			position := strings.Index(stable[cursor:], replacement.placeholder)
			if position < 0 {
				continue
			}
			position += cursor
			if next < 0 || position < next ||
				(position == next && len(replacement.placeholder) > len(selected.placeholder)) {
				next = position
				selected = replacement
			}
		}
		if next < 0 {
			out.WriteString(stable[cursor:])
			break
		}
		out.WriteString(stable[cursor:next])
		out.WriteString(selected.value)
		cursor = next + len(selected.placeholder)
		count++
	}
	out.WriteString(pending)
	return out.String(), count, hold > 0
}

func trailingRestorePrefix(input string, replacements []textReplacement) int {
	limit := 0
	for _, replacement := range replacements {
		if candidate := len(replacement.placeholder) - 1; candidate > limit {
			limit = candidate
		}
	}
	if len(input) < limit {
		limit = len(input)
	}
	for length := limit; length > 0; length-- {
		suffix := input[len(input)-length:]
		for _, replacement := range replacements {
			if len(replacement.placeholder) > length &&
				strings.HasPrefix(replacement.placeholder, suffix) {
				return length
			}
		}
	}
	return 0
}

func visibleResponseTextRefs(
	protocol contract.ProtocolID,
	root any,
	frame int,
) []visibleTextRef {
	object, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	refs := make([]visibleTextRef, 0, 4)
	appendErrorMessageRef(&refs, object, frame)
	// Each extractor assigns stable semantic channel IDs. A placeholder prefix
	// from one choice/item/block/candidate can therefore never consume text
	// from another channel.
	switch protocol {
	case contract.ProtocolOpenAIChat:
		appendOpenAIChatRefs(&refs, object, frame)
	case contract.ProtocolOpenAICompletions:
		appendOpenAICompletionRefs(&refs, object, frame)
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		appendOpenAIResponseRefs(&refs, object, frame)
	case contract.ProtocolAnthropicMessages:
		appendAnthropicRefs(&refs, object, frame)
	case contract.ProtocolGoogleGenerateContent:
		appendGeminiRefs(&refs, object, frame)
	}
	return refs
}

func appendErrorMessageRef(
	refs *[]visibleTextRef,
	root map[string]any,
	frame int,
) {
	errorObject, ok := root["error"].(map[string]any)
	if !ok {
		return
	}
	appendStringRef(refs, errorObject, "message", "error:message", frame)
}

func appendOpenAIChatRefs(
	refs *[]visibleTextRef,
	root map[string]any,
	frame int,
) {
	choices, _ := root["choices"].([]any)
	for position, value := range choices {
		choice, ok := value.(map[string]any)
		if !ok {
			continue
		}
		index := memberID(choice, "index", position)
		if delta, ok := choice["delta"].(map[string]any); ok {
			channel := "chat:delta:" + index
			appendContentValueRefs(refs, delta, "content", channel, frame)
			appendStringRef(refs, delta, "refusal", channel+":refusal", frame)
		}
		if message, ok := choice["message"].(map[string]any); ok {
			channel := "chat:message:" + index
			appendContentValueRefs(refs, message, "content", channel, frame)
			appendStringRef(refs, message, "refusal", channel+":refusal", frame)
		}
	}
}

func appendOpenAICompletionRefs(
	refs *[]visibleTextRef,
	root map[string]any,
	frame int,
) {
	choices, _ := root["choices"].([]any)
	for position, value := range choices {
		choice, ok := value.(map[string]any)
		if !ok {
			continue
		}
		index := memberID(choice, "index", position)
		appendStringRef(refs, choice, "text", "completion:"+index, frame)
	}
}

func appendOpenAIResponseRefs(
	refs *[]visibleTextRef,
	root map[string]any,
	frame int,
) {
	eventType, _ := root["type"].(string)
	itemKey := responseEventItemKey(root)
	contentIndex := memberID(root, "content_index", 0)
	switch eventType {
	case "response.output_text.delta":
		appendStringRef(
			refs,
			root,
			"delta",
			"responses:output_text:"+itemKey+":"+contentIndex,
			frame,
		)
	case "response.refusal.delta":
		appendStringRef(
			refs,
			root,
			"delta",
			"responses:refusal:"+itemKey+":"+contentIndex,
			frame,
		)
	case "response.output_text.done":
		appendStringRef(
			refs,
			root,
			"text",
			"responses:snapshot:output_text:"+itemKey+":"+contentIndex,
			frame,
		)
	case "response.refusal.done":
		appendStringRef(
			refs,
			root,
			"refusal",
			"responses:snapshot:refusal:"+itemKey+":"+contentIndex,
			frame,
		)
	case "response.content_part.added", "response.content_part.done":
		if part, ok := root["part"].(map[string]any); ok {
			appendResponsePartRefs(
				refs,
				part,
				"responses:snapshot:part:"+itemKey+":"+contentIndex,
				frame,
			)
		}
	case "response.output_item.added", "response.output_item.done":
		if item, ok := root["item"].(map[string]any); ok {
			appendResponseItemRefs(refs, item, "responses:snapshot:item:"+itemKey, frame)
		}
	case "response.completed", "response.incomplete", "response.failed":
		if response, ok := root["response"].(map[string]any); ok {
			appendResponseObjectRefs(refs, response, "responses:snapshot:response", frame)
		}
	default:
		appendResponseObjectRefs(refs, root, "responses:response", frame)
	}
}

func appendResponseObjectRefs(
	refs *[]visibleTextRef,
	response map[string]any,
	prefix string,
	frame int,
) {
	appendStringRef(refs, response, "output_text", prefix+":output_text", frame)
	output, _ := response["output"].([]any)
	for position, value := range output {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		itemID := memberString(item, "id", strconv.Itoa(position))
		appendResponseItemRefs(refs, item, prefix+":item:"+itemID, frame)
	}
}

func appendResponseItemRefs(
	refs *[]visibleTextRef,
	item map[string]any,
	prefix string,
	frame int,
) {
	content, _ := item["content"].([]any)
	for position, value := range content {
		part, ok := value.(map[string]any)
		if !ok {
			continue
		}
		appendResponsePartRefs(refs, part, prefix+":"+strconv.Itoa(position), frame)
	}
}

func appendResponsePartRefs(
	refs *[]visibleTextRef,
	part map[string]any,
	channel string,
	frame int,
) {
	partType, _ := part["type"].(string)
	switch partType {
	case "output_text", "text":
		appendStringRef(refs, part, "text", channel+":text", frame)
	case "refusal":
		appendStringRef(refs, part, "refusal", channel+":refusal", frame)
	}
}

func responseEventItemKey(root map[string]any) string {
	if value, ok := root["item_id"].(string); ok && value != "" {
		return value
	}
	return memberID(root, "output_index", 0)
}

func appendAnthropicRefs(
	refs *[]visibleTextRef,
	root map[string]any,
	frame int,
) {
	eventType, _ := root["type"].(string)
	index := memberID(root, "index", 0)
	switch eventType {
	case "content_block_delta":
		delta, _ := root["delta"].(map[string]any)
		deltaType, _ := delta["type"].(string)
		if deltaType == "text_delta" {
			appendStringRef(refs, delta, "text", "anthropic:text:"+index, frame)
		}
	case "content_block_start":
		block, _ := root["content_block"].(map[string]any)
		blockType, _ := block["type"].(string)
		if blockType == "text" {
			appendStringRef(
				refs,
				block,
				"text",
				"anthropic:snapshot:text:"+index,
				frame,
			)
		}
	case "message_start":
		if message, ok := root["message"].(map[string]any); ok {
			appendAnthropicContentRefs(refs, message, "anthropic:snapshot:message", frame)
		}
	default:
		appendAnthropicContentRefs(refs, root, "anthropic:message", frame)
		appendStringRef(refs, root, "completion", "anthropic:completion", frame)
	}
}

func appendAnthropicContentRefs(
	refs *[]visibleTextRef,
	root map[string]any,
	prefix string,
	frame int,
) {
	content, _ := root["content"].([]any)
	for position, value := range content {
		block, ok := value.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)
		if blockType == "text" {
			appendStringRef(
				refs,
				block,
				"text",
				prefix+":"+strconv.Itoa(position),
				frame,
			)
		}
	}
}

func appendGeminiRefs(
	refs *[]visibleTextRef,
	root map[string]any,
	frame int,
) {
	candidates, _ := root["candidates"].([]any)
	for candidatePosition, value := range candidates {
		candidate, ok := value.(map[string]any)
		if !ok {
			continue
		}
		candidateID := memberID(candidate, "index", candidatePosition)
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for partPosition, partValue := range parts {
			part, ok := partValue.(map[string]any)
			if !ok {
				continue
			}
			if thought, _ := part["thought"].(bool); thought {
				continue
			}
			appendStringRef(
				refs,
				part,
				"text",
				"gemini:"+candidateID+":"+strconv.Itoa(partPosition),
				frame,
			)
		}
	}
}

func appendContentValueRefs(
	refs *[]visibleTextRef,
	object map[string]any,
	key, channel string,
	frame int,
) {
	switch content := object[key].(type) {
	case string:
		*refs = append(*refs, visibleTextRef{
			frame:   frame,
			set:     func(replacement string) { object[key] = replacement },
			channel: channel,
			value:   content,
		})
	case []any:
		for position, value := range content {
			part, ok := value.(map[string]any)
			if !ok {
				continue
			}
			partType, _ := part["type"].(string)
			partChannel := channel + ":" + strconv.Itoa(position)
			switch partType {
			case "text", "output_text":
				appendStringRef(refs, part, "text", partChannel+":text", frame)
			case "refusal":
				appendStringRef(refs, part, "refusal", partChannel+":refusal", frame)
			}
		}
	}
}

func appendStringRef(
	refs *[]visibleTextRef,
	object map[string]any,
	key, channel string,
	frame int,
) {
	value, ok := object[key].(string)
	if !ok {
		return
	}
	*refs = append(*refs, visibleTextRef{
		frame:   frame,
		set:     func(replacement string) { object[key] = replacement },
		channel: channel,
		value:   value,
	})
}

// appendToolStringRef targets a string that carries a serialized tool-argument
// payload, so the replacement is escaped for the inner JSON.
func appendToolStringRef(
	refs *[]visibleTextRef,
	object map[string]any,
	key, channel string,
	frame int,
) {
	value, ok := object[key].(string)
	if !ok {
		return
	}
	*refs = append(*refs, visibleTextRef{
		frame:    frame,
		set:      func(replacement string) { object[key] = replacement },
		channel:  channel,
		value:    value,
		encoding: refEncodingJSONString,
		tool:     true,
	})
}

// appendStructuredToolRefs walks an already-parsed tool argument object. Each
// leaf string is its own channel: a parsed structure arrives whole, so no
// placeholder can straddle two of them, and keeping them separate stops a
// prefix in one field from consuming text from the next.
func appendStructuredToolRefs(
	refs *[]visibleTextRef,
	value any,
	assign func(string),
	channel string,
	frame int,
	depth int,
) {
	if depth > maxStructuredToolDepth {
		return
	}
	switch typed := value.(type) {
	case string:
		if assign == nil {
			return
		}
		*refs = append(*refs, visibleTextRef{
			frame:   frame,
			set:     assign,
			channel: channel,
			value:   typed,
			tool:    true,
		})
	case []any:
		for index := range typed {
			index := index
			appendStructuredToolRefs(
				refs,
				typed[index],
				func(replacement string) { typed[index] = replacement },
				channel+"/"+strconv.Itoa(index),
				frame,
				depth+1,
			)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			key := key
			appendStructuredToolRefs(
				refs,
				typed[key],
				func(replacement string) { typed[key] = replacement },
				channel+"/"+key,
				frame,
				depth+1,
			)
		}
	}
}

const maxStructuredToolDepth = 32

func memberID(object map[string]any, key string, fallback int) string {
	switch value := object[key].(type) {
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case string:
		if value != "" {
			return value
		}
	}
	return strconv.Itoa(fallback)
}

func memberString(object map[string]any, key, fallback string) string {
	if value, ok := object[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func parseSSEData(frame []byte) []byte {
	normalized := normalizeSSELines(frame)
	lines := bytes.Split(normalized, []byte{'\n'})
	data := make([][]byte, 0, 1)
	for _, line := range lines {
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		value := line[len("data:"):]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		data = append(data, value)
	}
	if len(data) == 0 {
		return nil
	}
	return bytes.Join(data, []byte{'\n'})
}

func renderSSEData(frame, data []byte) []byte {
	newline := []byte{'\n'}
	if bytes.Contains(frame, []byte("\r\n")) {
		newline = []byte("\r\n")
	}
	normalized := normalizeSSELines(frame)
	lines := bytes.Split(normalized, []byte{'\n'})
	var out bytes.Buffer
	inserted := false
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			if inserted {
				continue
			}
			_, _ = out.Write([]byte("data: "))
			_, _ = out.Write(data)
			_, _ = out.Write(newline)
			inserted = true
			continue
		}
		_, _ = out.Write(line)
		_, _ = out.Write(newline)
	}
	_, _ = out.Write(newline)
	return out.Bytes()
}

func normalizeSSELines(value []byte) []byte {
	normalized := bytes.ReplaceAll(value, []byte("\r\n"), []byte{'\n'})
	return bytes.ReplaceAll(normalized, []byte{'\r'}, []byte{'\n'})
}

func nextSSEFrameEnd(buffer []byte) int {
	lineStart := 0
	for lineStart < len(buffer) {
		position := lineStart
		for position < len(buffer) && buffer[position] != '\n' && buffer[position] != '\r' {
			position++
		}
		if position == len(buffer) {
			return 0
		}
		lineEnd := position
		if buffer[position] == '\r' && position+1 < len(buffer) && buffer[position+1] == '\n' {
			position += 2
		} else {
			position++
		}
		if lineEnd == lineStart {
			return position
		}
		lineStart = position
	}
	return 0
}

func completeJSONValueEnd(value []byte, start int) (int, bool, bool) {
	if start >= len(value) {
		return 0, false, false
	}
	switch value[start] {
	case '{', '[':
		depth := 0
		inString := false
		escaped := false
		for index := start; index < len(value); index++ {
			current := value[index]
			if inString {
				if escaped {
					escaped = false
					continue
				}
				switch current {
				case '\\':
					escaped = true
				case '"':
					inString = false
				}
				continue
			}
			switch current {
			case '"':
				inString = true
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth < 0 {
					return 0, false, true
				}
				if depth == 0 {
					return index + 1, true, false
				}
			}
		}
		return 0, false, false
	case '"':
		escaped := false
		for index := start + 1; index < len(value); index++ {
			current := value[index]
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == '"' {
				return index + 1, true, false
			}
		}
		return 0, false, false
	default:
		for index := start; index < len(value); index++ {
			switch value[index] {
			case ' ', '\t', '\r', '\n', ',', ']':
				if index == start {
					return 0, false, true
				}
				return index, true, false
			}
		}
		return 0, false, false
	}
}

func firstNonSpace(value []byte) int {
	for index, current := range value {
		switch current {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return index
		}
	}
	return -1
}
