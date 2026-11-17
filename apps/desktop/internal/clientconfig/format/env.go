package format

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// envEntry is one entry of a dotenv file: a `NAME=value` assignment, or
// anything else, kept as written. A quoted value may run over several lines.
type envEntry struct {
	text       string
	assignment *envAssignment
}

type envAssignment struct {
	// prefixLen is the length of everything before the name: indentation
	// and `export `.
	prefixLen int
	name      string
	value     string
}

// EnvFile is a dotenv file edited entry by entry, read the way Gemini CLI's
// `dotenv` reads it: `NAME=value` or `NAME: value`, `#` comments, optional
// `export`, and quoted values that may span lines. Entries AstrLink does not
// own keep their text.
type EnvFile struct {
	entries []envEntry
	crlf    bool
}

func trimStart(s string) string { return strings.TrimLeftFunc(s, unicode.IsSpace) }

func trimSpace(s string) string { return strings.TrimFunc(s, unicode.IsSpace) }

func isEnvNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '.' || c == '-'
}

// envAssignmentOf returns the name's start, the name, and the raw value of a
// dotenv assignment line.
func envAssignmentOf(line string) (start int, name, value string, ok bool) {
	body := trimStart(line)
	start = len(line) - len(body)
	if rest, found := strings.CutPrefix(body, "export"); found &&
		(strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t")) {
		rest = trimStart(rest)
		start = len(line) - len(rest)
		body = rest
	}
	nameLen := 0
	for nameLen < len(body) && isEnvNameByte(body[nameLen]) {
		nameLen++
	}
	if nameLen == 0 {
		return 0, "", "", false
	}
	name, rest := body[:nameLen], body[nameLen:]
	if value, found := strings.CutPrefix(trimStart(rest), "="); found {
		return start, name, value, true
	}
	value, found := strings.CutPrefix(rest, ":")
	if !found || !(strings.HasPrefix(value, " ") || strings.HasPrefix(value, "\t")) {
		return 0, "", "", false
	}
	return start, name, value, true
}

// ClosingQuote returns the byte offset of the closing quote of a value
// opening with quote, or -1.
func ClosingQuote(text string, quote rune) int {
	escaped := false
	first := true
	for index, c := range text {
		if first {
			first = false
			continue
		}
		switch {
		case c == '\\' && !escaped:
			escaped = true
		case c == quote && !escaped:
			return index
		default:
			escaped = false
		}
	}
	return -1
}

// unquotedEnvValue is a value read up to its comment.
func unquotedEnvValue(raw string) string {
	value, _, _ := strings.Cut(raw, "#")
	return trimSpace(value)
}

// ParseEnv reads a dotenv file. Values of the singleLine names, the ones
// AstrLink writes, may not span lines.
func ParseEnv(text string, singleLine []string) (*EnvFile, error) {
	crlf := strings.Contains(text, "\r\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var entries []envEntry
	index := 0
	for index < len(lines) {
		line := lines[index]
		index++
		prefixLen, name, raw, ok := envAssignmentOf(line)
		if !ok {
			entries = append(entries, envEntry{text: line})
			continue
		}
		rawTrimmed := trimStart(raw)
		var quote rune
		if rawTrimmed != "" && strings.ContainsRune("\"'`", rune(rawTrimmed[0])) {
			quote = rune(rawTrimmed[0])
		}
		text := line
		var value string
		if quote != 0 {
			// A quoted value runs to its closing quote, across lines.
			joined := rawTrimmed
			end := ClosingQuote(joined, quote)
			taken := 0
			for end < 0 && index+taken < len(lines) {
				joined += "\n" + lines[index+taken]
				taken++
				end = ClosingQuote(joined, quote)
			}
			if end >= 0 {
				for _, extra := range lines[index : index+taken] {
					text += "\n" + extra
				}
				index += taken
				inner := joined[1:end]
				if quote == '"' {
					inner = strings.ReplaceAll(strings.ReplaceAll(inner, `\n`, "\n"), `\r`, "\r")
				}
				value = inner
			} else {
				// Never closed: dotenv reads the line as unquoted.
				value = unquotedEnvValue(raw)
			}
		} else {
			value = unquotedEnvValue(raw)
		}
		if strings.Contains(text, "\n") && slices.Contains(singleLine, name) {
			return nil, &Invalid{
				Message: fmt.Sprintf("has a value for %s that spans several lines", name),
				Line:    index - strings.Count(text, "\n"),
			}
		}
		entries = append(entries, envEntry{
			text:       text,
			assignment: &envAssignment{prefixLen: prefixLen, name: name, value: value},
		})
	}
	return &EnvFile{entries: entries, crlf: crlf}, nil
}

func (f *EnvFile) position(name string) int {
	for i := len(f.entries) - 1; i >= 0; i-- {
		if a := f.entries[i].assignment; a != nil && a.name == name {
			return i
		}
	}
	return -1
}

// Get returns the value of name; dotenv keeps the last of repeated names.
func (f *EnvFile) Get(name string) (string, bool) {
	index := f.position(name)
	if index < 0 {
		return "", false
	}
	return f.entries[index].assignment.value, true
}

// Names lists the names the file assigns, each once, in order.
func (f *EnvFile) Names() []string {
	var names []string
	for _, entry := range f.entries {
		if entry.assignment != nil && !slices.Contains(names, entry.assignment.name) {
			names = append(names, entry.assignment.name)
		}
	}
	return names
}

func isEnvBareRune(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		strings.ContainsRune("_-./:@+,~=[]%", c)
}

// EncodeEnv writes value the way a dotenv reader reads it back.
func EncodeEnv(value string) (string, error) {
	bare := value != ""
	for _, c := range value {
		if !isEnvBareRune(c) {
			bare = false
			break
		}
	}
	switch {
	case bare:
		return value, nil
	case !strings.ContainsAny(value, "\"\\\n"):
		return `"` + value + `"`, nil
	case !strings.ContainsAny(value, "'\n"):
		return "'" + value + "'", nil
	default:
		return "", fmt.Errorf("has a value that cannot be written to .env")
	}
}

// Set rewrites the last line naming name in place and drops earlier ones,
// which it shadows; a new name goes at the end.
func (f *EnvFile) Set(name, value string) error {
	encoded, err := EncodeEnv(value)
	if err != nil {
		return err
	}
	index := f.position(name)
	if index < 0 {
		f.entries = append(f.entries, envEntry{
			text:       name + "=" + encoded,
			assignment: &envAssignment{name: name, value: value},
		})
		return nil
	}
	entry := &f.entries[index]
	prefixLen := entry.assignment.prefixLen
	entry.text = entry.text[:prefixLen] + name + "=" + encoded
	entry.assignment = &envAssignment{prefixLen: prefixLen, name: name, value: value}
	kept := f.entries[:0]
	for at, entry := range f.entries {
		if at >= index || entry.assignment == nil || entry.assignment.name != name {
			kept = append(kept, entry)
		}
	}
	f.entries = kept
	return nil
}

// Remove drops every line naming name.
func (f *EnvFile) Remove(name string) {
	f.entries = slices.DeleteFunc(f.entries, func(entry envEntry) bool {
		return entry.assignment != nil && entry.assignment.name == name
	})
}

// IsEmpty reports whether nothing but blank lines is left.
func (f *EnvFile) IsEmpty() bool {
	for _, entry := range f.entries {
		if trimSpace(entry.text) != "" {
			return false
		}
	}
	return true
}

// Render returns the text, keeping the file's line endings.
func (f *EnvFile) Render() string {
	if len(f.entries) == 0 {
		return ""
	}
	texts := make([]string, len(f.entries))
	for i, entry := range f.entries {
		texts[i] = entry.text
	}
	text := strings.Join(texts, "\n") + "\n"
	if f.crlf {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}
