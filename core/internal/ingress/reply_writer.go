package ingress

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/jsonpatch"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
)

// replyWriter runs the reply check of the request processing instances
// over one attempt's answer, in the client's protocol: Chat Completions,
// Responses, Claude Messages and Gemini. It reads only the assistant's text
// and rewrites it with the masks the instances applied; a stopped answer
// ends with an error. An answer it cannot read passes as it came.
type replyWriter struct {
	http.ResponseWriter
	check       *pluginhost.ReplyCheck
	protocol    contract.ProtocolID
	streaming   bool
	status      int
	wroteHeader bool
	passthrough bool
	buffer      bytes.Buffer
	carry       []byte
	// queued are the events without text that wait behind held text.
	queued [][]byte
	// templates are the last text event of each output, to carry text an
	// instance released later; sent is each output's text the client got.
	templates map[int]replyTemplate
	sent      map[int]string
	// parts numbers the Responses output_text parts as they appear.
	parts   map[[2]int]int
	stopped bool
}

type replyTemplate struct {
	event string
	data  []byte
}

// errReplyStopped ends the upstream copy of an answer an instance stopped.
var errReplyStopped = errors.New("the reply check stopped the answer")

func newReplyWriter(writer http.ResponseWriter, check *pluginhost.ReplyCheck, protocol contract.ProtocolID, streaming bool) *replyWriter {
	return &replyWriter{ResponseWriter: writer, check: check, protocol: protocol, streaming: streaming,
		templates: map[int]replyTemplate{}, sent: map[int]string{}, parts: map[[2]int]int{}}
}

// replyChecked reports whether the reply check reads answers of protocol.
func replyChecked(protocol contract.ProtocolID) bool {
	switch protocol {
	case contract.ProtocolOpenAIChat, contract.ProtocolOpenAIResponses, contract.ProtocolAnthropicMessages, contract.ProtocolGoogleGenerateContent:
		return true
	}
	return false
}

func (writer *replyWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (writer *replyWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	writer.status = status
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(writer.Header().Get("Content-Type"), ";")[0]))
	encoding := strings.ToLower(strings.TrimSpace(writer.Header().Get("Content-Encoding")))
	switch {
	case status >= http.StatusBadRequest || (encoding != "" && encoding != "identity"):
		// An error response passes and runs nothing; a compressed one
		// cannot be read.
		writer.passthrough = true
	case writer.streaming && mediaType == "text/event-stream":
		writer.Header().Del("Content-Length")
	case !writer.streaming && (mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")):
		return
	default:
		writer.passthrough = true
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *replyWriter) Write(chunk []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	if writer.stopped {
		return 0, errReplyStopped
	}
	if writer.passthrough {
		return writer.ResponseWriter.Write(chunk)
	}
	if !writer.streaming {
		if writer.buffer.Len()+len(chunk) > maxPluginRewriteBytes {
			return 0, errors.New("the answer is too large for the reply check")
		}
		writer.buffer.Write(chunk)
		return len(chunk), nil
	}
	writer.carry = append(writer.carry, chunk...)
	for {
		raw, rest, ok := cutSSEEvent(writer.carry)
		if !ok {
			break
		}
		writer.carry = rest
		if err := writer.event(raw); err != nil {
			return 0, err
		}
	}
	return len(chunk), nil
}

func (writer *replyWriter) Flush() { _ = writer.FlushError() }

func (writer *replyWriter) FlushError() error {
	if !writer.passthrough && !writer.streaming {
		return nil
	}
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

// answerText is one output's text in an event or a body: where it is, so
// it can be written again.
type answerText struct {
	index   int
	pointer string
	text    string
}

// textsOf reads the assistant's text in one stream event or whole body,
// and the outputs it ends. other reports an event that carries more than
// text, which keeps its place in the stream.
func (writer *replyWriter) textsOf(event string, document *jsonpatch.Document, whole bool) (texts []answerText, ends []int, other bool) {
	root, _ := document.Value().(*jsonpatch.Object)
	if root == nil {
		return nil, nil, true
	}
	str := func(object *jsonpatch.Object, key string) (string, bool) {
		value, _ := object.Member(key)
		text, ok := value.(string)
		return text, ok
	}
	num := func(object *jsonpatch.Object, key string) int {
		value, _ := object.Member(key)
		if number, ok := value.(json.Number); ok {
			parsed, _ := strconv.Atoi(number.String())
			return parsed
		}
		return 0
	}
	obj := func(object *jsonpatch.Object, key string) *jsonpatch.Object {
		value, _ := object.Member(key)
		child, _ := value.(*jsonpatch.Object)
		return child
	}
	arr := func(object *jsonpatch.Object, key string) []any {
		value, _ := object.Member(key)
		items, _ := value.([]any)
		return items
	}
	switch writer.protocol {
	case contract.ProtocolOpenAIChat:
		for position, item := range arr(root, "choices") {
			choice, _ := item.(*jsonpatch.Object)
			if choice == nil {
				continue
			}
			index := num(choice, "index")
			holder, member := "delta", "/delta/content"
			if whole {
				holder, member = "message", "/message/content"
			}
			if message := obj(choice, holder); message != nil {
				if text, ok := str(message, "content"); ok && (text != "" || whole) {
					texts = append(texts, answerText{index: index, pointer: "/choices/" + strconv.Itoa(position) + member, text: text})
				}
				for _, key := range []string{"tool_calls", "function_call", "refusal", "reasoning_content", "audio"} {
					if value, ok := message.Member(key); ok && value != nil {
						other = true
					}
				}
			}
			if reason, ok := choice.Member("finish_reason"); ok && reason != nil {
				ends = append(ends, index)
				other = true
			}
		}
		if usage, ok := root.Member("usage"); ok && usage != nil {
			other = true
		}
	case contract.ProtocolAnthropicMessages:
		if whole {
			for position, item := range arr(root, "content") {
				block, _ := item.(*jsonpatch.Object)
				if block == nil {
					continue
				}
				if kind, _ := str(block, "type"); kind == "text" {
					text, _ := str(block, "text")
					texts = append(texts, answerText{index: position, pointer: "/content/" + strconv.Itoa(position) + "/text", text: text})
				}
			}
			return texts, nil, false
		}
		kind, _ := str(root, "type")
		switch kind {
		case "content_block_delta":
			if delta := obj(root, "delta"); delta != nil {
				if deltaKind, _ := str(delta, "type"); deltaKind == "text_delta" {
					text, _ := str(delta, "text")
					return []answerText{{index: num(root, "index"), pointer: "/delta/text", text: text}}, nil, false
				}
			}
		case "content_block_stop":
			return nil, []int{num(root, "index")}, true
		}
		return nil, nil, true
	case contract.ProtocolOpenAIResponses:
		if whole {
			return writer.responsesOutput(root, "", obj, arr, str), nil, false
		}
		kind, _ := str(root, "type")
		key := [2]int{num(root, "output_index"), num(root, "content_index")}
		part, known := writer.parts[key]
		if !known && kind == "response.output_text.delta" {
			part = len(writer.parts)
			writer.parts[key] = part
		}
		switch kind {
		case "response.output_text.delta":
			text, _ := str(root, "delta")
			return []answerText{{index: part, pointer: "/delta", text: text}}, nil, false
		case "response.output_text.done":
			if known {
				return nil, []int{part}, true
			}
		}
		return nil, nil, true
	case contract.ProtocolGoogleGenerateContent:
		for position, item := range arr(root, "candidates") {
			candidate, _ := item.(*jsonpatch.Object)
			if candidate == nil {
				continue
			}
			index := position
			if _, ok := candidate.Member("index"); ok {
				index = num(candidate, "index")
			}
			if content := obj(candidate, "content"); content != nil {
				for partPosition, partItem := range arr(content, "parts") {
					part, _ := partItem.(*jsonpatch.Object)
					if part == nil {
						continue
					}
					if thought, _ := part.Member("thought"); thought == true {
						continue
					}
					if text, ok := str(part, "text"); ok {
						texts = append(texts, answerText{index: index, pointer: "/candidates/" + strconv.Itoa(position) + "/content/parts/" + strconv.Itoa(partPosition) + "/text", text: text})
					} else {
						other = true
					}
				}
			}
			if reason, ok := candidate.Member("finishReason"); ok && reason != nil {
				ends = append(ends, index)
				other = true
			}
		}
		if usage, ok := root.Member("usageMetadata"); ok && usage != nil {
			other = true
		}
	}
	return texts, ends, other
}

// responsesOutput reads the output_text parts of a Responses output, under
// prefix: a whole body, or the response of response.completed.
func (writer *replyWriter) responsesOutput(root *jsonpatch.Object, prefix string, obj func(*jsonpatch.Object, string) *jsonpatch.Object,
	arr func(*jsonpatch.Object, string) []any, str func(*jsonpatch.Object, string) (string, bool)) []answerText {
	var texts []answerText
	part := 0
	for itemPosition, item := range arr(root, "output") {
		message, _ := item.(*jsonpatch.Object)
		if message == nil {
			continue
		}
		for contentPosition, contentItem := range arr(message, "content") {
			content, _ := contentItem.(*jsonpatch.Object)
			if content == nil {
				continue
			}
			if kind, _ := str(content, "type"); kind == "output_text" {
				text, _ := str(content, "text")
				texts = append(texts, answerText{index: part, pointer: prefix + "/output/" + strconv.Itoa(itemPosition) + "/content/" + strconv.Itoa(contentPosition) + "/text", text: text})
				part++
			}
		}
	}
	return texts
}

// event checks the text of one event of the stream and writes what the
// check released.
func (writer *replyWriter) event(raw []byte) error {
	event, comment := parseSSEEvent(raw)
	if comment {
		_, err := writer.ResponseWriter.Write(raw)
		return err
	}
	data := bytes.TrimSpace(event.Data)
	if bytes.Equal(data, []byte("[DONE]")) {
		if err := writer.end(); err != nil {
			return err
		}
		_, err := writer.ResponseWriter.Write(raw)
		return err
	}
	if writer.check.Idle() && len(writer.queued) == 0 && !writer.masked() {
		_, err := writer.ResponseWriter.Write(raw)
		return err
	}
	document, err := jsonpatch.Parse(data)
	if err != nil {
		return writer.queue(raw)
	}
	texts, ends, other := writer.textsOf(event.Event, document, false)
	if writer.protocol == contract.ProtocolOpenAIResponses {
		if handled, err := writer.responsesDone(event, document); handled || err != nil {
			return err
		}
	}
	if writer.protocol == contract.ProtocolAnthropicMessages {
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &kind) == nil && kind.Type == "message_stop" {
			if err := writer.end(); err != nil {
				return err
			}
		}
	}
	var pieces []replyPieceAt
	for _, text := range texts {
		pieces = append(pieces, replyPieceAt{text: text})
		writer.templates[text.index] = replyTemplate{event: event.Event, data: data}
	}
	released, stop := writer.check.Feed(feedPieces(pieces, ends), other && len(texts) == 0)
	if stop != nil {
		return writer.stop(stop)
	}
	// The event's own text is what was released for its output; text
	// released for another output goes out in an event of its own first.
	own := map[int]bool{}
	for _, text := range texts {
		own[text.index] = true
	}
	for index, text := range released {
		if own[index] || text == "" {
			continue
		}
		if err := writer.synthesize(index, text); err != nil {
			return err
		}
	}
	if len(texts) > 0 {
		for _, text := range texts {
			value := released[text.index]
			released[text.index] = ""
			encoded, _ := json.Marshal(value)
			if err := document.Replace(text.pointer, encoded); err != nil {
				return err
			}
			writer.sent[text.index] += value
		}
		rewritten, err := document.Bytes()
		if err != nil {
			return err
		}
		if !other && allEmpty(texts, rewritten, document) {
			return writer.flushQueue()
		}
		if writer.check.Held() && other {
			return writer.queue(formatSSEEvent(pluginhost.StreamEvent{Event: event.Event, ID: event.ID, Data: rewritten}))
		}
		if _, err := writer.ResponseWriter.Write(formatSSEEvent(pluginhost.StreamEvent{Event: event.Event, ID: event.ID, Data: rewritten})); err != nil {
			return err
		}
		return writer.flushQueue()
	}
	if writer.check.Held() {
		return writer.queue(raw)
	}
	if err := writer.flushQueue(); err != nil {
		return err
	}
	_, err = writer.ResponseWriter.Write(raw)
	return err
}

type replyPieceAt struct{ text answerText }

func feedPieces(pieces []replyPieceAt, ends []int) []pluginhost.ReplyPiece {
	var out []pluginhost.ReplyPiece
	for _, piece := range pieces {
		out = append(out, pluginhost.ReplyPiece{Index: piece.text.index, Text: piece.text.text})
	}
	for _, index := range ends {
		out = append(out, pluginhost.ReplyPiece{Index: index, Final: true})
	}
	return out
}

// allEmpty reports whether an event whose text was all held carries nothing
// for the client.
func allEmpty(texts []answerText, _ []byte, document *jsonpatch.Document) bool {
	for _, text := range texts {
		value, _ := document.Get(text.pointer)
		if string(value) != `""` {
			return false
		}
	}
	return true
}

// masked reports whether the client got text other than the upstream's.
func (writer *replyWriter) masked() bool { return len(writer.sent) > 0 }

func (writer *replyWriter) queue(raw []byte) error {
	writer.queued = append(writer.queued, raw)
	return nil
}

func (writer *replyWriter) flushQueue() error {
	if writer.check.Held() {
		return nil
	}
	for _, raw := range writer.queued {
		if _, err := writer.ResponseWriter.Write(raw); err != nil {
			return err
		}
	}
	writer.queued = nil
	return nil
}

// synthesize writes text an instance released for an output in an event of
// the client's format, made from the output's last text event.
func (writer *replyWriter) synthesize(index int, text string) error {
	template, ok := writer.templates[index]
	if !ok {
		return nil
	}
	document, err := jsonpatch.Parse(template.data)
	if err != nil {
		return err
	}
	texts, _, _ := writer.textsOf(template.event, document, false)
	for _, found := range texts {
		value := ""
		if found.index == index {
			value = text
		}
		encoded, _ := json.Marshal(value)
		_ = document.Replace(found.pointer, encoded)
	}
	// The template's end and usage belong to its own event.
	for _, pointer := range []string{"/choices/0/finish_reason", "/usage", "/usageMetadata", "/candidates/0/finishReason"} {
		if _, exists := document.Get(pointer); exists {
			_ = document.Apply([]jsonpatch.Operation{{Op: "remove", Path: pointer}})
		}
	}
	data, err := document.Bytes()
	if err != nil {
		return err
	}
	writer.sent[index] += text
	_, err = writer.ResponseWriter.Write(formatSSEEvent(pluginhost.StreamEvent{Event: template.event, Data: data}))
	return err
}

// responsesDone rewrites the Responses events that repeat the whole text
// with the text the client got. It reports whether it wrote the event.
func (writer *replyWriter) responsesDone(event pluginhost.StreamEvent, document *jsonpatch.Document) (bool, error) {
	root, _ := document.Value().(*jsonpatch.Object)
	if root == nil {
		return false, nil
	}
	kindValue, _ := root.Member("type")
	kind, _ := kindValue.(string)
	if kind != "response.output_text.done" && kind != "response.content_part.done" && kind != "response.output_item.done" && kind != "response.completed" {
		return false, nil
	}
	if kind == "response.output_text.done" {
		// The part ends: what it held goes out first.
		released, stop := writer.check.Feed(endOf(writer, document), false)
		if stop != nil {
			return true, writer.stop(stop)
		}
		for index, text := range released {
			if text != "" {
				if err := writer.synthesize(index, text); err != nil {
					return true, err
				}
			}
		}
	}
	if kind == "response.completed" {
		if err := writer.end(); err != nil {
			return true, err
		}
	}
	if !writer.masked() {
		if err := writer.flushQueue(); err != nil {
			return true, err
		}
		_, err := writer.ResponseWriter.Write(formatSSEEvent(event))
		return true, err
	}
	switch kind {
	case "response.output_text.done":
		writer.setPart(document, root, "/text")
	case "response.content_part.done":
		writer.setPart(document, root, "/part/text")
	case "response.output_item.done":
		if item, _ := root.Member("item"); item != nil {
			if object, ok := item.(*jsonpatch.Object); ok {
				outputIndexValue, _ := root.Member("output_index")
				outputIndex, _ := strconv.Atoi(stringOf(outputIndexValue))
				contents, _ := object.Member("content")
				items, _ := contents.([]any)
				for position := range items {
					if part, known := writer.parts[[2]int{outputIndex, position}]; known {
						encoded, _ := json.Marshal(writer.sent[part])
						_ = document.Replace("/item/content/"+strconv.Itoa(position)+"/text", encoded)
					}
				}
			}
		}
	case "response.completed":
		if response, _ := root.Member("response"); response != nil {
			if object, ok := response.(*jsonpatch.Object); ok {
				obj := func(o *jsonpatch.Object, key string) *jsonpatch.Object {
					value, _ := o.Member(key)
					child, _ := value.(*jsonpatch.Object)
					return child
				}
				arr := func(o *jsonpatch.Object, key string) []any {
					value, _ := o.Member(key)
					items, _ := value.([]any)
					return items
				}
				str := func(o *jsonpatch.Object, key string) (string, bool) {
					value, _ := o.Member(key)
					text, ok := value.(string)
					return text, ok
				}
				for _, text := range writer.responsesOutput(object, "/response", obj, arr, str) {
					if sent, ok := writer.sent[text.index]; ok {
						encoded, _ := json.Marshal(sent)
						_ = document.Replace(text.pointer, encoded)
					}
				}
			}
		}
	}
	data, err := document.Bytes()
	if err != nil {
		return true, err
	}
	if err := writer.flushQueue(); err != nil {
		return true, err
	}
	_, err = writer.ResponseWriter.Write(formatSSEEvent(pluginhost.StreamEvent{Event: event.Event, ID: event.ID, Data: data}))
	return true, err
}

func endOf(writer *replyWriter, document *jsonpatch.Document) []pluginhost.ReplyPiece {
	root, _ := document.Value().(*jsonpatch.Object)
	outputIndex, _ := root.Member("output_index")
	contentIndex, _ := root.Member("content_index")
	first, _ := strconv.Atoi(stringOf(outputIndex))
	second, _ := strconv.Atoi(stringOf(contentIndex))
	if part, known := writer.parts[[2]int{first, second}]; known {
		return []pluginhost.ReplyPiece{{Index: part, Final: true}}
	}
	return nil
}

func (writer *replyWriter) setPart(document *jsonpatch.Document, root *jsonpatch.Object, pointer string) {
	outputIndex, _ := root.Member("output_index")
	contentIndex, _ := root.Member("content_index")
	first, _ := strconv.Atoi(stringOf(outputIndex))
	second, _ := strconv.Atoi(stringOf(contentIndex))
	if part, known := writer.parts[[2]int{first, second}]; known {
		encoded, _ := json.Marshal(writer.sent[part])
		_ = document.Replace(pointer, encoded)
	}
}

func stringOf(value any) string {
	if number, ok := value.(json.Number); ok {
		return number.String()
	}
	return ""
}

// end releases what every instance still holds, at the end of the stream.
func (writer *replyWriter) end() error {
	var pieces []pluginhost.ReplyPiece
	for index := range writer.templates {
		pieces = append(pieces, pluginhost.ReplyPiece{Index: index, Final: true})
	}
	released, stop := writer.check.Feed(pieces, true)
	if stop != nil {
		return writer.stop(stop)
	}
	for index, text := range released {
		if text != "" {
			if err := writer.synthesize(index, text); err != nil {
				return err
			}
		}
	}
	return writer.flushQueue()
}

// stop ends the answer an instance stopped: an error event in the
// client's format, after which the upstream is cut.
func (writer *replyWriter) stop(refusal *pluginhost.RequestRefusal) error {
	writer.stopped = true
	writer.queued = nil
	var event pluginhost.StreamEvent
	switch writer.protocol {
	case contract.ProtocolAnthropicMessages:
		data, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": refusal.Code, "message": refusal.Message}})
		event = pluginhost.StreamEvent{Event: "error", Data: data}
	case contract.ProtocolOpenAIResponses:
		data, _ := json.Marshal(map[string]any{"type": "error", "code": refusal.Code, "message": refusal.Message})
		event = pluginhost.StreamEvent{Event: "error", Data: data}
	case contract.ProtocolGoogleGenerateContent:
		data, _ := json.Marshal(map[string]any{"error": map[string]any{"code": refusal.Status, "message": refusal.Message, "status": strings.ToUpper(refusal.Code)}})
		event = pluginhost.StreamEvent{Data: data}
	default:
		data, _ := json.Marshal(map[string]any{"error": map[string]any{"message": refusal.Message, "type": refusal.Code, "code": refusal.Code}})
		event = pluginhost.StreamEvent{Data: data}
	}
	if _, err := writer.ResponseWriter.Write(formatSSEEvent(event)); err != nil {
		return err
	}
	_ = http.NewResponseController(writer.ResponseWriter).Flush()
	return errReplyStopped
}

// Finish ends the answer: a whole one is checked and goes out with its
// masks, or as the error of a stopped answer; a stream gets what the
// instances still held.
func (writer *replyWriter) Finish() error {
	defer writer.check.Finish()
	if writer.passthrough || !writer.wroteHeader {
		return nil
	}
	if writer.streaming {
		if writer.stopped {
			return nil
		}
		if len(bytes.TrimSpace(writer.carry)) > 0 {
			if err := writer.event(writer.carry); err != nil && !errors.Is(err, errReplyStopped) {
				return err
			}
		}
		writer.carry = nil
		if err := writer.end(); err != nil && !errors.Is(err, errReplyStopped) {
			return err
		}
		for _, raw := range writer.queued {
			if _, err := writer.ResponseWriter.Write(raw); err != nil {
				return err
			}
		}
		writer.queued = nil
		return http.NewResponseController(writer.ResponseWriter).Flush()
	}
	body := writer.buffer.Bytes()
	document, err := jsonpatch.Parse(body)
	if err == nil {
		texts, _, _ := writer.textsOf("", document, true)
		var pieces []pluginhost.ReplyPiece
		for _, text := range texts {
			pieces = append(pieces, pluginhost.ReplyPiece{Index: text.index, Text: text.text, Final: true})
		}
		// Gemini parts of one candidate are one output: its text goes back
		// into the first of them.
		released, stop := writer.check.Feed(mergeOutputs(pieces), false)
		if stop != nil {
			return writer.refuse(stop)
		}
		written := map[int]bool{}
		for _, text := range texts {
			value := ""
			if !written[text.index] {
				value, written[text.index] = released[text.index], true
			}
			encoded, _ := json.Marshal(value)
			_ = document.Replace(text.pointer, encoded)
		}
		if rewritten, err := document.Bytes(); err == nil && len(texts) > 0 {
			body = rewritten
		}
	}
	writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
	writer.ResponseWriter.WriteHeader(writer.status)
	_, err = writer.ResponseWriter.Write(body)
	return err
}

// mergeOutputs joins the pieces of one output.
func mergeOutputs(pieces []pluginhost.ReplyPiece) []pluginhost.ReplyPiece {
	var merged []pluginhost.ReplyPiece
	at := map[int]int{}
	for _, piece := range pieces {
		if position, ok := at[piece.Index]; ok {
			merged[position].Text += piece.Text
			continue
		}
		at[piece.Index] = len(merged)
		merged = append(merged, piece)
	}
	return merged
}

// refuse answers a whole answer an instance stopped, or failed on, with an
// error in the client's format.
func (writer *replyWriter) refuse(refusal *pluginhost.RequestRefusal) error {
	writer.Header().Del("Content-Length")
	writeInferenceError(writer.ResponseWriter, refusal.Status, refusal.Code, refusal.Message, false, nil)
	return nil
}
