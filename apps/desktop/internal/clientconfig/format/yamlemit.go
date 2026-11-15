package format

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// How values are written into YAML. A collection AstrLink adds is block
// YAML it writes itself (yamlBlock); a lone scalar is written the way the
// yaml-edit crate writes one, so that edits read as the Tauri shell's.

var yamlWords = []string{"true", "false", "yes", "no", "on", "off", "y", "n", "null", "~"}

// yamlScalarText is a string in a block AstrLink writes: plain when every
// YAML reader takes it for that string, else in double quotes.
func yamlScalarText(text string) string {
	plain := text != "" && (isASCIIAlpha(text[0]) || text[0] == '_' || text[0] == '/') &&
		!strings.HasSuffix(text, ":") && !slices.Contains(yamlWords, strings.ToLower(text))
	for i := 0; plain && i < len(text); i++ {
		c := text[i]
		plain = isASCIIAlnum(c) || strings.IndexByte("_-./:+=", c) >= 0
	}
	if plain {
		return text
	}
	return Encode(text)
}

func isASCIIAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func yamlLeafText(value any) string {
	switch v := value.(type) {
	case string:
		return yamlScalarText(v)
	case []any:
		return "[]"
	case map[string]any:
		return "{}"
	}
	return Encode(value)
}

func yamlIsLeaf(value any) bool {
	switch v := value.(type) {
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return true
}

func rankedKeys(object map[string]any, rank Rank) []string {
	names := SortedKeys(object)
	slices.SortStableFunc(names, func(a, b string) int {
		ra, rb := rank(a), rank(b)
		switch {
		case ra < rb:
			return -1
		case ra > rb:
			return 1
		}
		return 0
	})
	return names
}

// yamlBlock writes value as block YAML indented by unit, list items as
// compact `- key: value`, properties in rank order.
func yamlBlock(value any, unit int, rank Rank) string {
	var b strings.Builder
	var emit func(value any, indent int, inlineFirst bool)
	emit = func(value any, indent int, inlineFirst bool) {
		switch v := value.(type) {
		case map[string]any:
			for i, name := range rankedKeys(v, rank) {
				if !(inlineFirst && i == 0) {
					b.WriteString(strings.Repeat(" ", indent))
				}
				b.WriteString(yamlScalarText(name) + ":")
				if yamlIsLeaf(v[name]) {
					b.WriteString(" " + yamlLeafText(v[name]) + "\n")
				} else {
					b.WriteByte('\n')
					emit(v[name], indent+unit, false)
				}
			}
		case []any:
			for _, item := range v {
				b.WriteString(strings.Repeat(" ", indent) + "- ")
				if yamlIsLeaf(item) {
					b.WriteString(yamlLeafText(item) + "\n")
				} else {
					emit(item, indent+2, true)
				}
			}
		default:
			b.WriteString(strings.Repeat(" ", indent) + yamlLeafText(value) + "\n")
		}
	}
	emit(value, 0, false)
	return b.String()
}

// yamlIndentUnit is the file's indent step: its smallest indentation, else
// 2.
func yamlIndentUnit(text string) int {
	unit := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent > 0 && (unit == 0 || indent < unit) {
			unit = indent
		}
	}
	if unit == 0 {
		return 2
	}
	return unit
}

// needsDoubleQuotes reports whether value has characters only a
// double-quoted scalar can hold.
func needsDoubleQuotes(value string) bool {
	for _, c := range value {
		printable := c == '\t' || c == '\n' || c >= 0x20 && c <= 0x7E || c >= 0xA0 && c <= 0xFFFD || c >= 0x10000
		if !printable || c == 0x2028 || c == 0x2029 || c == 0xFEFF {
			return true
		}
	}
	return false
}

func allDigits(text string, base int) bool {
	if text == "" {
		return false
	}
	for _, c := range text {
		if _, err := strconv.ParseUint(string(c), base, 8); err != nil {
			return false
		}
	}
	return true
}

// isRustFloat reports whether Rust's f64 parser takes text.
func isRustFloat(text string) bool {
	s := strings.TrimLeft(text, "+-")
	if len(text)-len(s) > 1 {
		return false
	}
	switch strings.ToLower(s) {
	case "inf", "infinity", "nan":
		return true
	}
	mantissa, exponent, hasExp := strings.Cut(strings.ToLower(s), "e")
	if hasExp {
		exponent = strings.TrimPrefix(strings.TrimPrefix(exponent, "+"), "-")
		if !allDigits(exponent, 10) {
			return false
		}
	}
	whole, frac, _ := strings.Cut(mantissa, ".")
	if whole == "" && frac == "" {
		return false
	}
	return (whole == "" || allDigits(whole, 10)) && (frac == "" || allDigits(frac, 10))
}

// isCoreYAMLNonString reports whether the YAML core schema reads text, as a
// plain scalar, as something other than a string.
func isCoreYAMLNonString(text string) bool {
	switch text {
	case "true", "True", "TRUE", "false", "False", "FALSE", "null", "Null", "NULL", "~", "":
		return true
	}
	unsigned := strings.TrimLeft(text, "+-")
	if len(text)-len(unsigned) <= 1 && allDigits(unsigned, 10) {
		return true
	}
	if strings.HasPrefix(text, "0o") && allDigits(text[2:], 8) || strings.HasPrefix(text, "0x") && allDigits(text[2:], 16) {
		return true
	}
	switch unsigned {
	case ".inf", ".Inf", ".INF":
		return true
	}
	switch text {
	case ".nan", ".NaN", ".NAN":
		return true
	}
	mantissa, exponent, hasExp := strings.Cut(strings.ToLower(unsigned), "e")
	if hasExp && !allDigits(strings.TrimLeft(exponent, "+-"), 10) {
		return false
	}
	whole, frac, dot := strings.Cut(mantissa, ".")
	if len(text)-len(unsigned) > 1 {
		return false
	}
	if dot {
		return whole == "" && allDigits(frac, 10) || allDigits(whole, 10) && (frac == "" || allDigits(frac, 10))
	}
	return false
}

func resolvesToNonString(value string) bool {
	if isCoreYAMLNonString(value) {
		return true
	}
	for _, word := range []string{"true", "false", "yes", "no", "on", "off", "null"} {
		if strings.EqualFold(value, word) {
			return true
		}
	}
	if value == "~" {
		return true
	}
	unsigned := strings.TrimLeft(value, "+-")
	if len(value)-len(unsigned) <= 1 && len(unsigned) > 2 && unsigned[0] == '0' && strings.ContainsRune("bBoOxX", rune(unsigned[1])) && allDigits(unsigned[2:], 16) {
		return true
	}
	return isRustFloat(value)
}

const yamlIndicators = "-?:,[]{}#&*!|>'\"%@`"

func needsQuotingAsTagged(value string) bool {
	if value != "" && (strings.ContainsRune(yamlIndicators, rune(value[0])) || value[0] == '<') {
		return true
	}
	if strings.HasPrefix(value, "...") || strings.HasPrefix(value, "---") {
		return true
	}
	runes := []rune(value)
	for i, c := range runes {
		switch {
		case strings.ContainsRune("&*!|'\"%", c):
			return true
		case c == ':' && (i+1 == len(runes) || unicode.IsSpace(runes[i+1])):
			return true
		case c == '#' && (i == 0 || unicode.IsSpace(runes[i-1])):
			return true
		}
	}
	return value != strings.TrimSpace(value)
}

func yamlNeedsQuoting(value string) bool {
	return value == "" || needsDoubleQuotes(value) || resolvesToNonString(value) || needsQuotingAsTagged(value)
}

func yamlDoubleQuoted(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range value {
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == 0x07:
			b.WriteString(`\a`)
		case c == 0x1B:
			b.WriteString(`\e`)
		case c == 0x0B:
			b.WriteString(`\v`)
		case c == 0:
			b.WriteString(`\0`)
		case unicode.IsControl(c) || c > 0x7F:
			switch {
			case c <= 0xFF:
				b.WriteString(`\x` + strings.ToUpper(leftPad(strconv.FormatInt(int64(c), 16), 2)))
			case c <= 0xFFFF:
				b.WriteString(`\u` + strings.ToUpper(leftPad(strconv.FormatInt(int64(c), 16), 4)))
			default:
				b.WriteString(`\U` + strings.ToUpper(leftPad(strconv.FormatInt(int64(c), 16), 8)))
			}
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// yamlString writes a string as yaml-edit does; key is set for a mapping
// key, which cannot be a block scalar. indent is the column a block
// scalar's lines go to.
func yamlString(value string, key bool, indent int) string {
	if needsDoubleQuotes(value) {
		return yamlDoubleQuoted(value)
	}
	if strings.Contains(value, "\n") {
		if key {
			return yamlDoubleQuoted(value)
		}
		for _, line := range strings.Split(value, "\n") {
			if line != "" && strings.Trim(line, " \t") == "" {
				return yamlDoubleQuoted(value)
			}
		}
		return yamlLiteral(value, indent)
	}
	if !yamlNeedsQuoting(value) {
		return value
	}
	if strings.Contains(value, "'") {
		return yamlDoubleQuoted(value)
	}
	return "'" + value + "'"
}

// yamlLiteral writes a multi-line string as a literal block scalar whose
// lines sit at indent.
func yamlLiteral(value string, indent int) string {
	body := strings.TrimRight(value, "\n")
	chomp := "-"
	switch trailing := len(value) - len(body); {
	case trailing == 1:
		chomp = ""
	case trailing > 1:
		chomp = "+"
	}
	if strings.HasPrefix(body, " ") {
		return yamlDoubleQuoted(value)
	}
	var b strings.Builder
	b.WriteString("|" + chomp)
	lines := strings.Split(body, "\n")
	for i := len(body); i < len(value)-1; i++ {
		lines = append(lines, "")
	}
	for _, line := range lines {
		b.WriteByte('\n')
		if line != "" {
			b.WriteString(strings.Repeat(" ", indent) + line)
		}
	}
	return b.String()
}

// yamlScalar writes a leaf value as yaml-edit does: strings by its rules,
// and numbers as Rust prints them.
func yamlScalar(value any, indent int) string {
	switch v := value.(type) {
	case string:
		return yamlString(v, false, indent)
	case Number:
		if n, err := strconv.ParseInt(string(v), 10, 64); err == nil {
			return strconv.FormatInt(n, 10)
		}
		f, _ := strconv.ParseFloat(string(v), 64)
		switch {
		case math.IsNaN(f):
			return ".nan"
		case math.IsInf(f, 1):
			return ".inf"
		case math.IsInf(f, -1):
			return "-.inf"
		}
		text := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.ContainsAny(text, ".eE") {
			text += ".0"
		}
		return text
	case []any:
		return "[]"
	case map[string]any:
		return "{}"
	}
	return Encode(value)
}

// yamlFlowValue writes a value AstrLink adds to a flow collection: a
// collection as its block form would read, a lone scalar as yaml-edit
// writes one there.
func yamlFlowValue(value any, rank Rank) string {
	if !yamlIsLeaf(value) {
		return yamlFlowBlock(value, rank)
	}
	if text, ok := value.(string); ok {
		return yamlFlowString(text)
	}
	return yamlScalar(value, 0)
}

func yamlFlowBlock(value any, rank Rank) string {
	switch v := value.(type) {
	case map[string]any:
		parts := make([]string, 0, len(v))
		for _, name := range rankedKeys(v, rank) {
			parts = append(parts, yamlScalarText(name)+": "+yamlFlowBlock(v[name], rank))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, yamlFlowBlock(item, rank))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return yamlLeafText(value)
}

// yamlFlowString writes a string in a flow collection, in double quotes
// where a plain or block scalar would break it.
func yamlFlowString(value string) string {
	text := yamlString(value, false, 0)
	quoted := strings.HasPrefix(text, "\"") || strings.HasPrefix(text, "'")
	if strings.HasPrefix(text, "|") || !quoted && strings.ContainsAny(text, ",[]{}") {
		return yamlDoubleQuoted(value)
	}
	return text
}
