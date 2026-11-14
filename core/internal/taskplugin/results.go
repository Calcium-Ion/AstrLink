package taskplugin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime"
	"os"
	"strings"

	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Bounds of what a task keeps of the vendor's answers.
const (
	// maxKeptData is the snapshot a task keeps whole; a larger one loses
	// its long strings, such as a video in base64.
	maxKeptData = 1 << 20
	// maxInlineString is the longest string a compacted snapshot keeps.
	maxInlineString = 64 << 10
	// maxStoredResult bounds a result the vendor returned inline.
	maxStoredResult = 512 << 20
)

// storedPrefix marks a result kept beside its task: "stored:" and its
// media type.
const storedPrefix = "stored:"

// inlineOmitted replaces a long inline value in a compacted snapshot.
const inlineOmitted = "[inline data kept by the gateway]"

// compactData is a vendor snapshot as a task keeps it: whole up to 1 MiB,
// else without its long strings, and nothing when that is still too large.
func (engine *Engine) compactData(id string, data json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if len(trimmed) <= maxKeptData {
		return trimmed
	}
	root, err := parseNode(trimmed)
	if err != nil {
		return nil
	}
	omitted, _ := json.Marshal(inlineOmitted)
	var walk func(node *jsonNode)
	walk = func(node *jsonNode) {
		if node.kind == 0 && len(node.raw) > maxInlineString && node.raw[0] == '"' {
			node.raw = omitted
		}
		for _, child := range node.children {
			walk(child)
		}
	}
	walk(root)
	compacted := root.encode()
	if len(compacted) > storage.MaxPluginTaskDataBytes {
		engine.logf("task %s: the vendor's snapshot is too large to keep", id)
		return nil
	}
	return compacted
}

// keepResult is how a task keeps the result a parse hook named: a link as
// it is, a data URL as a file beside the task.
func (engine *Engine) keepResult(id, address string) string {
	if !strings.HasPrefix(address, "data:") {
		if len(address) > storage.MaxPluginTaskResultURL {
			return ""
		}
		return address
	}
	header, payload, found := strings.Cut(strings.TrimPrefix(address, "data:"), ",")
	if !found || !strings.HasSuffix(header, ";base64") || engine.dir == "" {
		return ""
	}
	mediaType := strings.TrimSuffix(header, ";base64")
	if _, _, err := mime.ParseMediaType(mediaType); err != nil {
		mediaType = "application/octet-stream"
	}
	if base64.StdEncoding.DecodedLen(len(payload)) > maxStoredResult {
		engine.logf("task %s: the inline result is larger than %d MiB", id, maxStoredResult>>20)
		return ""
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(payload, "=")); err != nil {
			return ""
		}
	}
	if err := os.MkdirAll(engine.dir, 0o700); err != nil {
		engine.logf("task %s: keep the inline result: %v", id, err)
		return ""
	}
	if err := os.WriteFile(engine.resultPath(id), data, 0o600); err != nil {
		engine.logf("task %s: keep the inline result: %v", id, err)
		return ""
	}
	return storedPrefix + mediaType
}

// storedResult reports whether a task's result is kept beside it, and its
// media type.
func storedResult(task storage.PluginTask) (string, bool) {
	mediaType, ok := strings.CutPrefix(task.ResultURL, storedPrefix)
	return mediaType, ok
}

// RemoveResults deletes the results kept beside the finished tasks of a
// plugin, before the tasks are cleared.
func (engine *Engine) RemoveResults(ctx context.Context, plugin string) error {
	if engine.dir == "" {
		return nil
	}
	cursor := ""
	for {
		tasks, next, err := engine.store.ListPluginTasks(ctx, plugin, cursor, 200)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if _, stored := storedResult(task); stored && task.Terminal() {
				_ = os.Remove(engine.resultPath(task.ID))
			}
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
}
