package claudeofficial

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// toolPrefix is how the CLI names the tools of the MCP server that carries
// the caller's tools. The server name is neutral on purpose: the model sees
// it in every tool name.
const (
	mcpServerName = "client"
	toolPrefix    = "mcp__" + mcpServerName + "__"
	maxToolName   = 64
)

// Diagnostic is a request field the CLI cannot honour. It is reported on the
// request record; the request is still served.
type Diagnostic struct {
	Code     string
	Path     string
	Message  string
	Severity string
}

const diagnosticCode = "claude_official_unsupported"

type block = map[string]any

type message struct {
	Role   string
	Blocks []block
}

type tool struct {
	// Name is the caller's name; MCPName is the MCP tool name, which the
	// model sees with toolPrefix.
	Name        string
	MCPName     string
	Description string
	Schema      json.RawMessage
}

// request is a Claude Messages request as the CLI can serve it.
type request struct {
	Model       string
	System      []string
	Messages    []message
	Tools       []tool
	Stream      bool
	Thinking    bool
	Effort      string
	Diagnostics []Diagnostic
}

var efforts = []string{"low", "medium", "high", "xhigh", "max"}

// knownFields are the Messages fields parseRequest reads or knowingly drops.
var knownFields = []string{
	"model", "system", "messages", "tools", "tool_choice", "stream", "thinking", "output_config",
	"max_tokens", "metadata", "temperature", "top_p", "top_k", "stop_sequences", "context_management",
	"service_tier",
}

// parseRequest reads a Claude Messages body. Errors are the caller's: the
// body cannot be served at all.
func parseRequest(raw []byte) (request, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return request{}, fmt.Errorf("request body must be a JSON object")
	}
	var parsed request
	note := func(path, message, severity string) {
		parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{
			Code: diagnosticCode, Path: path, Message: message, Severity: severity,
		})
	}
	if err := json.Unmarshal(fields["model"], &parsed.Model); err != nil || strings.TrimSpace(parsed.Model) == "" {
		return request{}, fmt.Errorf("model is required")
	}
	if rawStream, ok := fields["stream"]; ok {
		_ = json.Unmarshal(rawStream, &parsed.Stream)
	}
	system, err := parseSystem(fields["system"])
	if err != nil {
		return request{}, err
	}
	parsed.System = system
	messages, err := parseMessages(fields["messages"])
	if err != nil {
		return request{}, err
	}
	parsed.Messages = messages
	tools, dropped, err := parseTools(fields["tools"])
	if err != nil {
		return request{}, err
	}
	parsed.Tools = tools
	for _, index := range dropped {
		note(fmt.Sprintf("tools[%d]", index), "Claude Code cannot offer server tools; the tool was not offered", "error")
	}
	if rawChoice, ok := fields["tool_choice"]; ok {
		var choice struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(rawChoice, &choice) == nil && choice.Type != "" && choice.Type != "auto" {
			note("tool_choice", "Claude Code always lets the model choose its tools; tool_choice "+choice.Type+" was ignored", "error")
		}
	}
	if rawThinking, ok := fields["thinking"]; ok {
		var thinking struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(rawThinking, &thinking) == nil {
			parsed.Thinking = thinking.Type == "enabled" || thinking.Type == "adaptive"
		}
	}
	if rawConfig, ok := fields["output_config"]; ok {
		var config struct {
			Effort string `json:"effort"`
		}
		if json.Unmarshal(rawConfig, &config) == nil && config.Effort != "" {
			if slices.Contains(efforts, config.Effort) {
				parsed.Effort = config.Effort
			} else {
				note("output_config.effort", "effort "+config.Effort+" is not one Claude Code accepts; its default was used", "warning")
			}
		}
	}
	for _, name := range []string{"temperature", "top_p", "top_k"} {
		if value, ok := fields[name]; ok && string(value) != "null" {
			note(name, "Claude Code sets sampling itself; "+name+" was ignored", "warning")
		}
	}
	if value, ok := fields["stop_sequences"]; ok && string(value) != "null" && string(value) != "[]" {
		note("stop_sequences", "Claude Code cannot stop on custom sequences; stop_sequences was ignored", "error")
	}
	if _, ok := fields["max_tokens"]; ok {
		note("max_tokens", "Claude Code chooses the output limit itself; max_tokens was ignored", "warning")
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !slices.Contains(knownFields, name) {
			note(name, "Claude Code cannot pass "+name+"; it was ignored", "warning")
		}
	}
	return parsed, nil
}

// billingHeader starts the system block a Claude client adds to identify
// itself. It is the client's metadata, not prompt text, and the CLI sends its
// own.
const billingHeader = "x-anthropic-billing-header:"

func parseSystem(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		return []string{text}, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("system must be a string or text blocks")
	}
	system := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" || strings.TrimSpace(block.Text) == "" ||
			strings.HasPrefix(strings.TrimSpace(block.Text), billingHeader) {
			continue
		}
		system = append(system, block.Text)
	}
	return system, nil
}

func parseMessages(raw json.RawMessage) ([]message, error) {
	var items []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return nil, fmt.Errorf("messages must be a non-empty array")
	}
	messages := make([]message, 0, len(items))
	for index, item := range items {
		if item.Role != "user" && item.Role != "assistant" {
			return nil, fmt.Errorf("messages[%d].role must be user or assistant", index)
		}
		blocks, err := parseContent(item.Content)
		if err != nil {
			return nil, fmt.Errorf("messages[%d].content: %w", index, err)
		}
		messages = append(messages, message{Role: item.Role, Blocks: blocks})
	}
	if messages[len(messages)-1].Role != "user" {
		return nil, fmt.Errorf("the last message must come from the user; Claude Code cannot continue a prefilled reply")
	}
	return messages, nil
}

func parseContent(raw json.RawMessage) ([]block, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []block{{"type": "text", "text": text}}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var blocks []block
	if err := decoder.Decode(&blocks); err != nil {
		return nil, fmt.Errorf("must be a string or content blocks")
	}
	for index, item := range blocks {
		if _, ok := item["type"].(string); !ok {
			return nil, fmt.Errorf("block %d has no type", index)
		}
	}
	return blocks, nil
}

func parseTools(raw json.RawMessage) ([]tool, []int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	var items []struct {
		Type        string          `json:"type"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, fmt.Errorf("tools must be an array")
	}
	tools := make([]tool, 0, len(items))
	var dropped []int
	used := map[string]bool{}
	for index, item := range items {
		if item.Type != "" && item.Type != "custom" {
			dropped = append(dropped, index)
			continue
		}
		if strings.TrimSpace(item.Name) == "" {
			return nil, nil, fmt.Errorf("tools[%d].name is required", index)
		}
		schema := item.InputSchema
		if len(schema) == 0 || string(schema) == "null" {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		name := mcpToolName(item.Name)
		for suffix := 2; used[name]; suffix++ {
			name = mcpToolName(fmt.Sprintf("%s_%d", item.Name, suffix))
		}
		used[name] = true
		tools = append(tools, tool{Name: item.Name, MCPName: name, Description: item.Description, Schema: schema})
	}
	return tools, dropped, nil
}

var validToolName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// mcpToolName keeps the caller's name when the prefixed name is valid, and
// otherwise derives a stable short one.
func mcpToolName(name string) string {
	if validToolName.MatchString(name) && len(toolPrefix)+len(name) <= maxToolName {
		return name
	}
	sanitized := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(name, "_")
	if len(sanitized) > 38 {
		sanitized = sanitized[:38]
	}
	sum := sha256.Sum256([]byte(name))
	return sanitized + "_" + hex.EncodeToString(sum[:])[:10]
}

// Unsupported lists what the CLI cannot honour in a Claude Messages body, for
// the request record. A body that cannot be served at all reports nothing;
// serving it fails instead.
func Unsupported(raw []byte) []Diagnostic {
	parsed, err := parseRequest(raw)
	if err != nil {
		return nil
	}
	return parsed.Diagnostics
}

// configKey is what a process is started with; a conversation continues only
// in a process started the same way.
func (parsed request) configKey() string {
	tools := make([]any, 0, len(parsed.Tools))
	for _, item := range parsed.Tools {
		tools = append(tools, []any{item.Name, item.MCPName, item.Description, string(item.Schema)})
	}
	raw, _ := json.Marshal([]any{parsed.Model, parsed.Effort, parsed.Thinking, parsed.System, tools})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
