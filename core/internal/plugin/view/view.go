// Package view turns what a plugin returns for the user to see into views
// the host can render. It keeps the blocks it knows, cleans every string to
// plain text, and cuts what passes the limits, marking that it did. A plugin
// cannot put markup, scripts or links in front of the user: the desktop
// renders each string as text in its own components.
package view

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Parse reads a view: a JSON array of blocks. It never fails. Blocks it does
// not know or cannot use are dropped, oversized parts are cut, and truncated
// tells whether either happened.
func Parse(raw json.RawMessage) (view []contract.PluginViewBlock, truncated bool) {
	value, ok := decode(raw)
	if !ok {
		return nil, len(bytes.TrimSpace(raw)) > 0
	}
	if value == nil {
		return nil, false
	}
	blocks, isArray := value.([]any)
	if !isArray {
		return nil, true
	}
	return parseBlocks(blocks)
}

// ParseRecord reads the record a hook attaches to its result. ok is false
// when it is not an object with a title and a known status; the host then
// records the hook's outcome on its own.
func ParseRecord(raw json.RawMessage) (record contract.PluginRecord, ok bool) {
	value, decoded := decode(raw)
	object, isObject := value.(map[string]any)
	if !decoded || !isObject {
		return contract.PluginRecord{}, false
	}
	return recordFrom(object)
}

// RecordFrom is ParseRecord for a record already decoded with
// json.Decoder.UseNumber.
func RecordFrom(object map[string]any) (contract.PluginRecord, bool) { return recordFrom(object) }

// BlocksFrom is Parse for blocks already decoded with json.Decoder.UseNumber.
func BlocksFrom(value any) (view []contract.PluginViewBlock, truncated bool) {
	if value == nil {
		return nil, false
	}
	blocks, isArray := value.([]any)
	if !isArray {
		return nil, true
	}
	return parseBlocks(blocks)
}

func recordFrom(object map[string]any) (contract.PluginRecord, bool) {
	title, cut := text(object["title"], contract.MaxPluginRecordTitleRunes, false)
	status := contract.PluginRecordStatus(stringOf(object["status"]))
	if title == "" || !status.Valid() {
		return contract.PluginRecord{}, false
	}
	summary, summaryCut := text(object["summary"], contract.MaxPluginRecordSummaryRunes, false)
	record := contract.PluginRecord{Title: title, Status: status, Summary: summary}
	view, viewCut := BlocksFrom(object["view"])
	record.View = view
	actions, actionsCut := actionsFrom(object["actions"])
	record.Actions = actions
	record.Truncated = cut || summaryCut || viewCut || actionsCut
	if record.Validate() != nil {
		return contract.PluginRecord{}, false
	}
	return record, true
}

// actionsFrom reads a record's buttons. One that is not valid, repeats an id
// or comes past the limit is dropped, and the record marked truncated.
func actionsFrom(value any) ([]contract.PluginRecordAction, bool) {
	if value == nil {
		return nil, false
	}
	values, isArray := value.([]any)
	if !isArray {
		return nil, true
	}
	truncated := false
	var actions []contract.PluginRecordAction
	for _, entry := range values {
		object, isObject := entry.(map[string]any)
		if !isObject || len(actions) == contract.MaxPluginRecordActions {
			truncated = true
			continue
		}
		label, labelCut := text(object["label"], contract.MaxPluginActionLabelRunes, false)
		confirm, confirmCut := text(object["confirm"], contract.MaxPluginActionConfirmRunes, false)
		action := contract.PluginRecordAction{ID: stringOf(object["id"]), Label: label, Confirm: confirm}
		repeated := slices.ContainsFunc(actions, func(other contract.PluginRecordAction) bool { return other.ID == action.ID })
		if action.Validate() != nil || repeated {
			truncated = true
			continue
		}
		truncated = truncated || labelCut || confirmCut
		actions = append(actions, action)
	}
	return actions, truncated
}

func decode(raw json.RawMessage) (any, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	return value, true
}

func parseBlocks(values []any) ([]contract.PluginViewBlock, bool) {
	truncated := false
	if len(values) > contract.MaxPluginViewBlocks {
		values, truncated = values[:contract.MaxPluginViewBlocks], true
	}
	view := make([]contract.PluginViewBlock, 0, len(values))
	for _, value := range values {
		object, _ := value.(map[string]any)
		block, cut, ok := parseBlock(object)
		truncated = truncated || cut || !ok
		if ok {
			view = append(view, block)
		}
	}
	// Past the size limit, the last blocks go first.
	for len(view) > 0 {
		encoded, err := json.Marshal(view)
		if err == nil && len(encoded) <= contract.MaxPluginViewBytes {
			break
		}
		view, truncated = view[:len(view)-1], true
	}
	if contract.ValidatePluginView(view) != nil {
		return nil, true
	}
	if len(view) == 0 {
		view = nil
	}
	return view, truncated
}

func parseBlock(object map[string]any) (contract.PluginViewBlock, bool, bool) {
	block := contract.PluginViewBlock{Type: contract.PluginViewBlockType(stringOf(object["type"]))}
	truncated := false
	switch block.Type {
	case contract.PluginViewKV:
		for _, value := range list(object["items"], contract.MaxPluginViewItems, &truncated) {
			item, _ := value.(map[string]any)
			label, labelCut := text(item["label"], contract.MaxPluginViewTextRunes, false)
			itemValue, valueCut := text(item["value"], contract.MaxPluginViewTextRunes, false)
			truncated = truncated || labelCut || valueCut
			if label == "" {
				truncated = true
				continue
			}
			mono, _ := item["mono"].(bool)
			block.Items = append(block.Items, contract.PluginViewItem{Label: label, Value: itemValue, Mono: mono})
		}
		return block, truncated, len(block.Items) > 0
	case contract.PluginViewBadges:
		for _, value := range list(object["items"], contract.MaxPluginViewItems, &truncated) {
			item, _ := value.(map[string]any)
			badge, cut := text(item["text"], contract.MaxPluginViewTextRunes, false)
			tone := contract.PluginViewTone(stringOf(item["tone"]))
			if tone != contract.PluginViewToneOK && tone != contract.PluginViewToneWarn && tone != contract.PluginViewToneError {
				tone = ""
			}
			truncated = truncated || cut
			if badge == "" {
				truncated = true
				continue
			}
			block.Items = append(block.Items, contract.PluginViewItem{Text: badge, Tone: tone})
		}
		return block, truncated, len(block.Items) > 0
	case contract.PluginViewTable:
		for _, value := range list(object["columns"], contract.MaxPluginTableColumns, &truncated) {
			column, cut := text(value, contract.MaxPluginViewTextRunes, false)
			truncated = truncated || cut
			if column == "" {
				column = " "
			}
			block.Columns = append(block.Columns, column)
		}
		if len(block.Columns) == 0 {
			return block, truncated, false
		}
		for _, value := range list(object["rows"], contract.MaxPluginTableRows, &truncated) {
			cells, isArray := value.([]any)
			if !isArray {
				truncated = true
				continue
			}
			if len(cells) > len(block.Columns) {
				cells, truncated = cells[:len(block.Columns)], true
			}
			row := make([]contract.PluginViewCell, len(block.Columns))
			for index, cellValue := range cells {
				var cut bool
				row[index], cut = cell(cellValue)
				truncated = truncated || cut
			}
			block.Rows = append(block.Rows, row)
		}
		return block, truncated, true
	case contract.PluginViewText:
		body, cut := text(object["text"], contract.MaxPluginViewTextRunes, true)
		block.Text = body
		return block, cut, body != ""
	}
	return block, false, false
}

// list returns the elements of an array value, at most max of them; a cut
// sets truncated.
func list(value any, max int, truncated *bool) []any {
	values, _ := value.([]any)
	if len(values) > max {
		values, *truncated = values[:max], true
	}
	return values
}

func cell(value any) (contract.PluginViewCell, bool) {
	if number, ok := value.(json.Number); ok {
		if parsed, err := number.Float64(); err == nil {
			return contract.PluginViewCell{Number: &parsed}, false
		}
	}
	content, cut := text(value, contract.MaxPluginViewTextRunes, false)
	return contract.PluginViewCell{Text: content}, cut
}

// text returns a value as clean text: strings as they are, numbers and
// booleans as JSON writes them, a missing value as nothing. cut reports that
// something was dropped: characters past max, or a value that is not text.
func text(value any, max int, multiline bool) (string, bool) {
	var raw string
	switch value := value.(type) {
	case string:
		raw = value
	case json.Number:
		raw = value.String()
	case bool:
		if value {
			raw = "true"
		} else {
			raw = "false"
		}
	default:
		return "", value != nil
	}
	return clean(raw, max, multiline)
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}

// clean drops control characters, other than tabs and, in multiline text,
// line breaks, and the characters that reorder text for display; then it
// cuts the text to max characters.
func clean(text string, max int, multiline bool) (string, bool) {
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	var builder strings.Builder
	count := 0
	for _, r := range text {
		if r == '\r' && multiline {
			continue
		}
		if (unicode.IsControl(r) && r != '\t' && !(multiline && r == '\n')) || bidiControl(r) {
			continue
		}
		if count == max {
			return builder.String(), true
		}
		builder.WriteRune(r)
		count++
	}
	return builder.String(), false
}

func bidiControl(r rune) bool {
	return (r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩') || r == '‎' || r == '‏' || r == '؜'
}
