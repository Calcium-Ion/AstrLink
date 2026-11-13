package privacy

import (
	"encoding/json"
	"unicode/utf8"
)

const maxToolContextPathBytes = 512

func isToolTextBlockField(parent map[string]any, key string) bool {
	if key != "text" && key != "type" {
		return false
	}
	switch parent["type"] {
	case "text", "input_text", "output_text":
		return true
	default:
		return false
	}
}

// toolFieldContextPrefix is the exact tool-field-path-v1 training/inference
// format. JSON-encoding the path prevents a key containing quotes/newlines from
// fabricating another field marker. Retain the most local path for deep inputs,
// without silently truncating the actual value that will be inspected.
func toolFieldContextPrefix(path string) string {
	if len(path) > maxToolContextPathBytes {
		start := len(path) - maxToolContextPathBytes
		for start < len(path) && !utf8.RuneStart(path[start]) {
			start++
		}
		path = "..." + path[start:]
	}
	encoded, _ := json.Marshal(path)
	return "Field: " + string(encoded) + "\nValue: "
}
