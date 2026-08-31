// Package autotext extracts classifier input using current-user-text-v3.
//
// This is intentionally not the privacy walker. Privacy scans every protocol
// root for PII. Classification only reads the last user message so the text
// matches the training distribution.
package autotext

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

const maxExtractedBytes = contract.MaxAutoClassifierPreviewBytes

// ExtractLastUserText returns the current-user-text-v3 payload.
// An empty string means the caller should skip classification.
func ExtractLastUserText(protocol contract.ProtocolID, body []byte) string {
	if len(body) == 0 || !utf8.Valid(body) {
		return ""
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return ""
	}
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		return extractFromField(root["input"])
	case contract.ProtocolOpenAIChat, contract.ProtocolAnthropicMessages:
		return extractFromField(root["messages"])
	case contract.ProtocolGoogleGenerateContent:
		return extractFromField(root["contents"])
	default:
		return ""
	}
}

func extractFromField(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return clamp(joinBlocks([]string{asString}))
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return clamp(joinBlocks(textBlocks(raw)))
	}
	for index := len(items) - 1; index >= 0; index-- {
		if !isUserItem(items[index]) {
			continue
		}
		return clamp(joinBlocks(itemTextBlocks(items[index])))
	}
	return ""
}

func isUserItem(raw json.RawMessage) bool {
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil {
		return false
	}
	role := jsonString(item["role"])
	if role == "" {
		return true
	}
	return role == "user"
}

func itemTextBlocks(raw json.RawMessage) []string {
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil {
		return textBlocks(raw)
	}
	if blocks := contentBlocks(item["content"]); len(blocks) > 0 {
		return blocks
	}
	if blocks := contentBlocks(item["parts"]); len(blocks) > 0 {
		return blocks
	}
	if text := jsonString(item["text"]); text != "" {
		return []string{text}
	}
	return nil
}

func contentBlocks(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return []string{asString}
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return textBlocks(raw)
	}
	blocks := make([]string, 0, len(items))
	for _, item := range items {
		blocks = append(blocks, textBlocks(item)...)
	}
	return blocks
}

func textBlocks(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return []string{asString}
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil
	}
	kind := jsonString(object["type"])
	if kind != "" && !isTextBlockType(kind) {
		return nil
	}
	if _, exists := object["inline_data"]; exists {
		return nil
	}
	if _, exists := object["file_data"]; exists {
		return nil
	}
	if _, exists := object["functionCall"]; exists {
		return nil
	}
	if _, exists := object["functionResponse"]; exists {
		return nil
	}
	for _, key := range []string{"text", "input_text"} {
		if text := jsonString(object[key]); text != "" || object[key] != nil && jsonString(object[key]) == "" {
			if value := jsonString(object[key]); value != "" {
				return []string{value}
			}
		}
	}
	return nil
}

func isTextBlockType(kind string) bool {
	switch kind {
	case "text", "input_text", "output_text":
		return true
	default:
		return false
	}
}

func joinBlocks(blocks []string) string {
	kept := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if strings.TrimSpace(block) == "" {
			continue
		}
		kept = append(kept, block)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n")
}

func clamp(text string) string {
	if text == "" || !utf8.ValidString(text) {
		return ""
	}
	if len(text) <= maxExtractedBytes {
		return text
	}
	end := maxExtractedBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func IsBlank(text string) bool {
	return strings.IndexFunc(text, func(value rune) bool {
		return !unicode.IsSpace(value)
	}) < 0
}
