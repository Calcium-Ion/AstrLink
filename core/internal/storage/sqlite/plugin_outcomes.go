package sqlite

import (
	"bytes"
	"slices"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// pluginOutcome is the plugin and the status of one plugin record, as
// written in the record's JSON.
type pluginOutcome struct {
	plugin, status []byte
}

// readPluginOutcomes appends the plugin and status of each record in a
// request's plugin_records_json to into, stepping over the rest of the
// records, views included, without decoding it. It reports false for text
// that is not an array of objects; an empty column holds no records.
func readPluginOutcomes(text []byte, into []pluginOutcome) ([]pluginOutcome, bool) {
	if len(text) == 0 {
		return into, true
	}
	scan := jsonScan{text: text}
	if !scan.next('[') {
		return into, false
	}
	if scan.next(']') {
		return into, scan.done()
	}
	for {
		if !scan.next('{') {
			return into, false
		}
		var outcome pluginOutcome
		for !scan.next('}') {
			key, ok := scan.string()
			if !ok || !scan.next(':') {
				return into, false
			}
			var value []byte
			if string(key) == "plugin" || string(key) == "status" {
				value, ok = scan.string()
			}
			if value == nil && !scan.skip() {
				return into, false
			}
			switch string(key) {
			case "plugin":
				outcome.plugin = value
			case "status":
				outcome.status = value
			}
			if !scan.next(',') && !scan.peek('}') {
				return into, false
			}
		}
		into = append(into, outcome)
		if scan.next(']') {
			return into, scan.done()
		}
		if !scan.next(',') {
			return into, false
		}
	}
}

// countPluginOutcomes adds a request to the group of each plugin that acted
// on it, as failed when one of the plugin's calls failed. A plugin that only
// skipped the request leaves it out.
func countPluginOutcomes(groups map[string]*storage.UsagePluginGroup, outcomes []pluginOutcome) {
	for index, outcome := range outcomes {
		same := func(other pluginOutcome) bool { return bytes.Equal(other.plugin, outcome.plugin) }
		// A plugin counts at its first record that acted.
		if !outcome.acted() || slices.ContainsFunc(outcomes[:index], func(earlier pluginOutcome) bool {
			return same(earlier) && earlier.acted()
		}) {
			continue
		}
		failed := slices.ContainsFunc(outcomes[index:], func(record pluginOutcome) bool {
			return same(record) && string(record.status) == string(contract.PluginRecordFailed)
		})
		group := groups[string(outcome.plugin)]
		if group == nil {
			if !contract.ValidPluginKey(string(outcome.plugin)) {
				continue
			}
			group = &storage.UsagePluginGroup{Plugin: string(outcome.plugin)}
			groups[group.Plugin] = group
		}
		if failed {
			group.FailedRequests++
		} else {
			group.Requests++
		}
	}
}

// acted reports whether the record's call did something, or failed trying.
func (outcome pluginOutcome) acted() bool {
	return string(outcome.status) == string(contract.PluginRecordOK) || string(outcome.status) == string(contract.PluginRecordFailed)
}

// jsonScan steps through JSON text without decoding it.
type jsonScan struct {
	text []byte
	at   int
}

func (scan *jsonScan) space() {
	for scan.at < len(scan.text) {
		switch scan.text[scan.at] {
		case ' ', '\t', '\n', '\r':
			scan.at++
		default:
			return
		}
	}
}

// peek reports whether c comes next, after any white space.
func (scan *jsonScan) peek(c byte) bool {
	scan.space()
	return scan.at < len(scan.text) && scan.text[scan.at] == c
}

// next consumes c when it comes next.
func (scan *jsonScan) next(c byte) bool {
	if !scan.peek(c) {
		return false
	}
	scan.at++
	return true
}

// done reports whether nothing but white space is left.
func (scan *jsonScan) done() bool {
	scan.space()
	return scan.at == len(scan.text)
}

// string consumes a string when one comes next and returns its text as
// written, escapes and all.
func (scan *jsonScan) string() ([]byte, bool) {
	if !scan.peek('"') {
		return nil, false
	}
	start := scan.at + 1
	for at := start; at < len(scan.text); at++ {
		switch scan.text[at] {
		case '\\':
			at++
		case '"':
			scan.at = at + 1
			return scan.text[start:at], true
		}
	}
	return nil, false
}

// skip consumes a value of any kind: a string, an object or an array with
// everything in it, or a number or literal up to what ends it.
func (scan *jsonScan) skip() bool {
	scan.space()
	depth := 0
	for scan.at < len(scan.text) {
		switch scan.text[scan.at] {
		case '"':
			if _, ok := scan.string(); !ok {
				return false
			}
			if depth == 0 {
				return true
			}
			continue
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return true
			}
			depth--
			if depth == 0 {
				scan.at++
				return true
			}
		case ',':
			if depth == 0 {
				return true
			}
		}
		scan.at++
	}
	return false
}
