package taskplugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Bounds of a submission read as server-sent events.
const (
	maxEventBytes      = 1 << 20
	maxSnapshotBytes   = 1 << 20
	maxDeltaStateBytes = 64 << 10
	maxDeltaResult     = 1 << 20
	maxDeltaNodes      = 32768
	maxDeltaDepth      = 32
	maxDeltaChanges    = 256
	// streamIdle ends a stream that sent nothing for this long.
	streamIdle = 30 * time.Second
)

// sseEvent is the SubmitEvent a hook reads.
type sseEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	Data  string `json:"data"`
}

// readEvents reads server-sent events from body, calling handle for each
// until it says the stream is done. A stream that ends first, or sends
// nothing for streamIdle, fails.
func readEvents(ctx context.Context, cancel context.CancelFunc, body io.Reader, handle func(sseEvent) (bool, error)) error {
	idle := time.AfterFunc(streamIdle, cancel)
	defer idle.Stop()
	reader := bufio.NewReaderSize(body, 64<<10)
	var event sseEvent
	var data strings.Builder
	hasData := false
	size := 0
	dispatch := func() (bool, error) {
		if !hasData {
			event = sseEvent{}
			return false, nil
		}
		event.Data = data.String()
		if event.Event == "" {
			event.Event = "message"
		}
		done, err := handle(event)
		event, hasData, size = sseEvent{ID: event.ID}, false, 0
		data.Reset()
		return done, err
	}
	for {
		line, err := readLine(reader)
		if err != nil {
			if ctx.Err() != nil {
				return errors.New("the upstream stream sent nothing for 30 seconds")
			}
			if errors.Is(err, io.EOF) {
				return errors.New("the upstream stream ended before the plugin said it was done")
			}
			return fmt.Errorf("the upstream stream could not be read: %w", err)
		}
		idle.Reset(streamIdle)
		if size += len(line); size > maxEventBytes {
			return errors.New("an upstream event is larger than 1 MiB")
		}
		if line == "" {
			done, err := dispatch()
			if err != nil || done {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			if hasData {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			hasData = true
		case "event":
			event.Event = value
		case "id":
			if !strings.ContainsRune(value, 0) {
				event.ID = value
			}
		}
	}
}

// readLine reads one line ended by \n, \r\n or \r.
func readLine(reader *bufio.Reader) (string, error) {
	var line []byte
	for {
		char, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return string(line), nil
			}
			return "", err
		}
		switch char {
		case '\n':
			return string(line), nil
		case '\r':
			if next, err := reader.Peek(1); err == nil && next[0] == '\n' {
				_, _ = reader.ReadByte()
			}
			return string(line), nil
		}
		if len(line) > maxEventBytes {
			return "", errors.New("an upstream line is larger than 1 MiB")
		}
		line = append(line, char)
	}
}

// snapshotResult is what parseSubmitEvent returns.
type snapshotResult struct {
	State json.RawMessage `json:"state"`
	Done  *bool           `json:"done"`
}

// deltaResult is what parseSubmitEventDelta returns.
type deltaResult struct {
	Changes []jsonChange    `json:"changes"`
	State   json.RawMessage `json:"state"`
	Done    *bool           `json:"done"`
}

type jsonChange struct {
	Op    string            `json:"op"`
	Path  []json.RawMessage `json:"path"`
	Value json.RawMessage   `json:"value"`
}

// deltaNode is a node of the result parseSubmitEventDelta builds: text is
// kept as Go text, so that appending to it costs what it appends.
type deltaNode struct {
	// kind is '{', '[', 's' for text in text, or 0 for a scalar in raw.
	kind     byte
	keys     []string
	children []*deltaNode
	text     strings.Builder
	raw      json.RawMessage
}

// accumulator applies the changes of parseSubmitEventDelta and keeps count
// of the result's encoded size and values as it grows.
type accumulator struct {
	root  *deltaNode
	size  int
	nodes int
}

func toDelta(raw json.RawMessage, depth int) (*deltaNode, int, error) {
	if depth > maxDeltaDepth {
		return nil, 0, errors.New("the result is nested deeper than 32 levels")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, 0, errors.New("a change's value is not JSON")
	}
	return deltaValue(value, depth)
}

func deltaValue(value any, depth int) (*deltaNode, int, error) {
	if depth > maxDeltaDepth {
		return nil, 0, errors.New("the result is nested deeper than 32 levels")
	}
	switch typed := value.(type) {
	case map[string]any:
		node, count := &deltaNode{kind: '{'}, 1
		// Members keep the order JSON gave them only within one value;
		// sort for a stable result.
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child, children, err := deltaValue(typed[key], depth+1)
			if err != nil {
				return nil, 0, err
			}
			node.keys = append(node.keys, key)
			node.children = append(node.children, child)
			count += children
		}
		return node, count, nil
	case []any:
		node, count := &deltaNode{kind: '['}, 1
		for _, item := range typed {
			child, children, err := deltaValue(item, depth+1)
			if err != nil {
				return nil, 0, err
			}
			node.children = append(node.children, child)
			count += children
		}
		return node, count, nil
	case string:
		node := &deltaNode{kind: 's'}
		node.text.WriteString(typed)
		return node, 1, nil
	default:
		raw, err := marshalOrdered(typed)
		if err != nil {
			return nil, 0, err
		}
		return &deltaNode{raw: raw}, 1, nil
	}
}

func (node *deltaNode) write(buffer *bytes.Buffer) {
	switch node.kind {
	case '{':
		buffer.WriteByte('{')
		for index, key := range node.keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			name, _ := marshalOrdered(key)
			buffer.Write(name)
			buffer.WriteByte(':')
			node.children[index].write(buffer)
		}
		buffer.WriteByte('}')
	case '[':
		buffer.WriteByte('[')
		for index, child := range node.children {
			if index > 0 {
				buffer.WriteByte(',')
			}
			child.write(buffer)
		}
		buffer.WriteByte(']')
	case 's':
		text, _ := marshalOrdered(node.text.String())
		buffer.Write(text)
	default:
		buffer.Write(node.raw)
	}
}

func (node *deltaNode) encoded() []byte {
	var buffer bytes.Buffer
	node.write(&buffer)
	return buffer.Bytes()
}

func (node *deltaNode) count() int {
	total := 1
	for _, child := range node.children {
		total += child.count()
	}
	return total
}

// escapedLength is how many bytes text takes inside a JSON string.
func escapedLength(text string) int {
	encoded, _ := marshalOrdered(text)
	return len(encoded) - 2
}

// apply applies one change.
func (acc *accumulator) apply(change jsonChange) error {
	if len(change.Path) > maxDeltaDepth {
		return errors.New("a change's path has more than 32 segments")
	}
	if change.Op != "set" && change.Op != "append" && change.Op != "appendText" {
		return fmt.Errorf("a change's op is %q, not set, append or appendText", change.Op)
	}
	if len(change.Path) == 0 {
		if change.Op != "set" {
			return errors.New("only set changes the root")
		}
		node, count, err := toDelta(change.Value, 0)
		if err != nil {
			return err
		}
		acc.root = node
		acc.size, acc.nodes = len(node.encoded()), count
		return acc.check()
	}
	if acc.root == nil {
		return errors.New("a change addresses a member of a result that does not exist yet")
	}
	parent := acc.root
	for _, segment := range change.Path[:len(change.Path)-1] {
		child, err := parent.child(segment)
		if err != nil {
			return err
		}
		parent = child
	}
	last := change.Path[len(change.Path)-1]
	depth := len(change.Path)
	switch change.Op {
	case "set":
		node, count, err := toDelta(change.Value, depth)
		if err != nil {
			return err
		}
		added := len(node.encoded())
		var key string
		if json.Unmarshal(last, &key) == nil {
			if parent.kind != '{' {
				return errors.New("a change sets a member of what is not an object")
			}
			for index, name := range parent.keys {
				if name == key {
					old := parent.children[index]
					acc.size += added - len(old.encoded())
					acc.nodes += count - old.count()
					parent.children[index] = node
					return acc.check()
				}
			}
			parent.keys = append(parent.keys, key)
			parent.children = append(parent.children, node)
			acc.size += added + escapedLength(key) + 3
			if len(parent.keys) > 1 {
				acc.size++
			}
			acc.nodes += count
			return acc.check()
		}
		index, err := arrayIndex(last, parent)
		if err != nil {
			return err
		}
		old := parent.children[index]
		acc.size += added - len(old.encoded())
		acc.nodes += count - old.count()
		parent.children[index] = node
		return acc.check()
	}
	target, err := parent.child(last)
	if err != nil {
		return err
	}
	if change.Op == "append" {
		if target.kind != '[' {
			return errors.New("a change appends to what is not an array")
		}
		node, count, err := toDelta(change.Value, depth+1)
		if err != nil {
			return err
		}
		acc.size += len(node.encoded())
		if len(target.children) > 0 {
			acc.size++
		}
		acc.nodes += count
		target.children = append(target.children, node)
		return acc.check()
	}
	var text string
	if json.Unmarshal(change.Value, &text) != nil {
		return errors.New("appendText appends a string")
	}
	if target.kind != 's' {
		return errors.New("appendText appends to what is not a string")
	}
	target.text.WriteString(text)
	acc.size += escapedLength(text)
	return acc.check()
}

func (acc *accumulator) check() error {
	switch {
	case acc.size > maxDeltaResult:
		return errors.New("the result is larger than 1 MiB")
	case acc.nodes > maxDeltaNodes:
		return errors.New("the result has more than 32,768 values")
	}
	return nil
}

func (node *deltaNode) child(segment json.RawMessage) (*deltaNode, error) {
	var key string
	if json.Unmarshal(segment, &key) == nil {
		if node.kind != '{' {
			return nil, errors.New("a change's path names a member of what is not an object")
		}
		for index, name := range node.keys {
			if name == key {
				return node.children[index], nil
			}
		}
		return nil, fmt.Errorf("a change's path names %q, which does not exist", key)
	}
	index, err := arrayIndex(segment, node)
	if err != nil {
		return nil, err
	}
	return node.children[index], nil
}

func arrayIndex(segment json.RawMessage, node *deltaNode) (int, error) {
	var index int
	if err := json.Unmarshal(segment, &index); err != nil || index < 0 {
		return 0, errors.New("a change's path holds what is neither a key nor an index")
	}
	if node.kind != '[' || index >= len(node.children) {
		return 0, errors.New("a change's path names an element that does not exist")
	}
	return index, nil
}

// result is the accumulated JSON, its size checked against what was
// counted.
func (acc *accumulator) result() (json.RawMessage, error) {
	if acc.root == nil {
		return json.RawMessage("null"), nil
	}
	encoded := acc.root.encoded()
	if len(encoded) > maxDeltaResult || !utf8.Valid(encoded) {
		return nil, errors.New("the result is larger than 1 MiB")
	}
	return encoded, nil
}

// streamSubmission reads a submission answered as server-sent events: the
// plugin's event hook reads each event until it says the stream is done,
// and what it built becomes the body parseSubmitResponse reads.
func (call *exchange) streamSubmission(ctx context.Context, cancel context.CancelFunc, response *http.Response, driver driverContext) (json.RawMessage, error) {
	meta := call.meta
	delta := call.hasHook("parseSubmitEventDelta") && (!call.hasHook("parseSubmitEvent") || meta.Has("submit-sse-delta@1"))
	var state json.RawMessage = json.RawMessage("null")
	acc := &accumulator{}
	var built json.RawMessage
	err := readEvents(ctx, cancel, response.Body, func(event sseEvent) (bool, error) {
		if delta {
			raw, err := call.call(ctx, "parseSubmitEventDelta", driver, event, state)
			if err != nil {
				return false, err
			}
			var result deltaResult
			if err := strictJSON(raw, &result); err != nil || result.Done == nil {
				return false, errors.New("parseSubmitEventDelta returned something other than {changes, state, done}")
			}
			if len(result.Changes) > maxDeltaChanges {
				return false, errors.New("parseSubmitEventDelta returned more than 256 changes")
			}
			for _, change := range result.Changes {
				if err := acc.apply(change); err != nil {
					return false, fmt.Errorf("parseSubmitEventDelta: %w", err)
				}
			}
			state = jsonOrNull(result.State)
			if len(state) > maxDeltaStateBytes {
				return false, errors.New("parseSubmitEventDelta returned a state larger than 64 KiB")
			}
			if *result.Done {
				built, err = acc.result()
				return true, err
			}
			return false, nil
		}
		raw, err := call.call(ctx, "parseSubmitEvent", driver, event, state)
		if err != nil {
			return false, err
		}
		var result snapshotResult
		if err := json.Unmarshal(raw, &result); err != nil || result.Done == nil {
			return false, errors.New("parseSubmitEvent returned something other than {state, done}")
		}
		state = jsonOrNull(result.State)
		if len(state) > maxSnapshotBytes {
			return false, errors.New("parseSubmitEvent returned a state larger than 1 MiB")
		}
		if *result.Done {
			built = state
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		var known *Error
		if errors.As(err, &known) {
			return nil, known
		}
		return nil, &Error{Status: http.StatusBadGateway, Code: "upstream_stream_failed", Message: err.Error(), err: err}
	}
	return built, nil
}

// strictJSON decodes raw into value refusing members value lacks.
func strictJSON(raw json.RawMessage, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
