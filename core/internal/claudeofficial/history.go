package claudeofficial

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// group is one turn of the caller's history: consecutive messages of one
// role. A conversation continues in a process while the hashes of the groups
// it has seen are a prefix of the caller's history.
type group struct {
	Role   string
	Blocks []block
}

func groupMessages(messages []message) []group {
	var groups []group
	for _, item := range messages {
		if len(groups) > 0 && groups[len(groups)-1].Role == item.Role {
			last := &groups[len(groups)-1]
			last.Blocks = append(last.Blocks, item.Blocks...)
			continue
		}
		groups = append(groups, group{Role: item.Role, Blocks: append([]block(nil), item.Blocks...)})
	}
	return groups
}

// hash fingerprints what a group says, ignoring what clients and converters
// rewrite freely: thinking, cache markers, citations and surrounding space.
func (item group) hash() string {
	normalized := make([]any, 0, len(item.Blocks))
	for _, content := range item.Blocks {
		if value, ok := normalizeBlock(content); ok {
			normalized = append(normalized, value)
		}
	}
	raw, _ := json.Marshal([]any{item.Role, normalized})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func hashGroups(groups []group) []string {
	hashes := make([]string, len(groups))
	for index, item := range groups {
		hashes[index] = item.hash()
	}
	return hashes
}

func normalizeBlock(content block) (any, bool) {
	kind, _ := content["type"].(string)
	switch kind {
	case "thinking", "redacted_thinking":
		return nil, false
	case "text":
		text, _ := content["text"].(string)
		return []any{"text", strings.TrimSpace(text)}, true
	case "tool_use":
		return []any{"tool_use", content["id"], content["name"], content["input"]}, true
	case "tool_result":
		return []any{"tool_result", content["tool_use_id"], normalizeToolContent(content["content"]), content["is_error"] == true}, true
	}
	copied := make(map[string]any, len(content))
	for key, value := range content {
		if key != "cache_control" && key != "citations" {
			copied[key] = value
		}
	}
	return copied, true
}

func normalizeToolContent(value any) any {
	switch typed := value.(type) {
	case string:
		return []any{[]any{"text", strings.TrimSpace(typed)}}
	case []any:
		normalized := make([]any, 0, len(typed))
		for _, item := range typed {
			if content, ok := item.(map[string]any); ok {
				if value, keep := normalizeBlock(content); keep {
					normalized = append(normalized, value)
				}
			}
		}
		return normalized
	}
	return value
}

// checkpointIntro introduces history the process has not seen. It speaks
// only of the conversation itself.
const checkpointIntro = "The following is earlier conversation history that you have not seen. " +
	"Treat it as prior conversation, not a new request."

const (
	checkpointContinueUser = "Continue with the user message after this checkpoint."
	checkpointContinue     = "Continue the conversation from where it left off."
)

// renderCheckpoint writes history as one user content list: text with the
// history's images and documents in place.
func renderCheckpoint(history []group, toolNames map[string]string, followed bool) []block {
	var blocks []block
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			blocks = append(blocks, block{"type": "text", "text": text.String()})
			text.Reset()
		}
	}
	text.WriteString("<conversation-checkpoint>\n")
	text.WriteString(checkpointIntro)
	text.WriteString(" ")
	if followed {
		text.WriteString(checkpointContinueUser)
	} else {
		text.WriteString(checkpointContinue)
	}
	text.WriteString("\n")
	for _, item := range history {
		text.WriteString("\n<" + item.Role + ">\n")
		for _, content := range item.Blocks {
			kind, _ := content["type"].(string)
			switch kind {
			case "thinking", "redacted_thinking":
			case "text":
				value, _ := content["text"].(string)
				text.WriteString(value)
				text.WriteString("\n")
			case "tool_use":
				text.WriteString("<tool-call>\n")
				text.WriteString(compactJSON(map[string]any{"id": content["id"], "name": content["name"], "input": content["input"]}))
				text.WriteString("\n</tool-call>\n")
			case "tool_result":
				id, _ := content["tool_use_id"].(string)
				result := map[string]any{"id": id, "output": toolResultText(content["content"])}
				if name := toolNames[id]; name != "" {
					result["name"] = name
				}
				if content["is_error"] == true {
					result["is_error"] = true
				}
				text.WriteString("<tool-result>\n")
				text.WriteString(compactJSON(result))
				text.WriteString("\n</tool-result>\n")
				for _, media := range toolResultMedia(content["content"]) {
					flush()
					blocks = append(blocks, media)
				}
			case "image", "document":
				flush()
				blocks = append(blocks, stripBlock(content))
			default:
				text.WriteString(compactJSON(stripBlock(content)))
				text.WriteString("\n")
			}
		}
		text.WriteString("</" + item.Role + ">\n")
	}
	text.WriteString("</conversation-checkpoint>")
	flush()
	return blocks
}

// toolUseNames maps the tool_use ids of history to their tool names, so a
// rendered result says which tool produced it.
func toolUseNames(groups []group) map[string]string {
	names := map[string]string{}
	for _, item := range groups {
		for _, content := range item.Blocks {
			if content["type"] == "tool_use" {
				id, _ := content["id"].(string)
				name, _ := content["name"].(string)
				names[id] = name
			}
		}
	}
	return names
}

func toolResultText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var parts []string
		for _, item := range typed {
			content, _ := item.(map[string]any)
			if content["type"] == "text" {
				text, _ := content["text"].(string)
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func toolResultMedia(value any) []block {
	items, _ := value.([]any)
	var media []block
	for _, item := range items {
		content, _ := item.(map[string]any)
		if content["type"] == "image" || content["type"] == "document" {
			media = append(media, stripBlock(content))
		}
	}
	return media
}

func stripBlock(content block) block {
	copied := make(block, len(content))
	for key, value := range content {
		if key != "cache_control" && key != "citations" {
			copied[key] = value
		}
	}
	return copied
}

func compactJSON(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return strings.TrimSpace(buffer.String())
}

// userInput is what a process is sent for the caller's newest turn. Tool
// results go back through MCP only while the process waits for them; any
// other result is history the process cannot pair with a call, so it is
// rendered with the rest of the unseen history.
type userInput struct {
	// History is unseen history, rendered in a checkpoint first.
	History []group
	// Blocks is the caller's newest content.
	Blocks []block
}

func (input userInput) content(toolNames map[string]string) []block {
	var newest []block
	var orphaned []block
	for _, content := range input.Blocks {
		if content["type"] == "tool_result" {
			orphaned = append(orphaned, content)
			continue
		}
		newest = append(newest, stripBlock(content))
	}
	history := input.History
	if len(orphaned) > 0 {
		history = append(append([]group(nil), history...), group{Role: "user", Blocks: orphaned})
	}
	if len(history) == 0 {
		return newest
	}
	return append(renderCheckpoint(history, toolNames, len(newest) > 0), newest...)
}
