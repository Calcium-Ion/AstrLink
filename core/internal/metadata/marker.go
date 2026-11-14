package metadata

import (
	"regexp"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// A session marker is a line `<marker>value</marker>` that binds a session to
// a value, such as the project its memory belongs to. Only a line that holds
// nothing else counts, and only outside fenced code.

// maxMarkerLine bounds the line a MarkerScanner keeps: a marker line is a
// marker name of at most 32 characters and a value of at most 128.
const maxMarkerLine = 1024

var markerLine = regexp.MustCompile(`^<([a-z][a-z0-9-]{0,31})>([^<>\n]*)</([a-z][a-z0-9-]{0,31})>$`)

// MarkerHit is a marker line found in a reply or a user message. A hit with
// a Reason does not count: the line was in fenced code or its value does not
// suit the attribute.
type MarkerHit struct {
	Key    string
	Value  string
	Reason string
	// User marks a line the user typed, not one a reply carried.
	User bool
}

// Valid reports whether the hit binds a value.
func (hit MarkerHit) Valid() bool { return hit.Reason == "" }

// Markers maps the marker of each attribute that has one to its key.
func (registry *Registry) Markers() map[string]string {
	markers := map[string]string{}
	for _, attribute := range registry.current.Load().attributes {
		if attribute.Marker != "" {
			markers[attribute.Marker] = attribute.Key
		}
	}
	return markers
}

// matchMarker reads one line, already trimmed, as a marker line of a known
// attribute.
func (registry *Registry) matchMarker(markers map[string]string, line string) (MarkerHit, bool) {
	match := markerLine.FindStringSubmatch(line)
	if match == nil || match[1] != match[3] {
		return MarkerHit{}, false
	}
	key, known := markers[match[1]]
	if !known {
		return MarkerHit{}, false
	}
	hit := MarkerHit{Key: key, Value: strings.TrimSpace(match[2])}
	if !registry.Valid(key, hit.Value) {
		hit.Reason = contract.MetadataIgnoredInvalidValue
		hit.Value = printable(hit.Value)
	}
	return hit, true
}

// UserMarker reports a message the user typed that is one marker line and
// nothing else: the user binding or rebinding the session by hand.
func (registry *Registry) UserMarker(typed string) (MarkerHit, bool) {
	line := strings.TrimSpace(typed)
	if line == "" || strings.ContainsAny(line, "\r\n") {
		return MarkerHit{}, false
	}
	hit, ok := registry.matchMarker(registry.Markers(), line)
	hit.User = ok
	return hit, ok
}

// MarkerScanner reads the text of a reply as it streams and keeps the marker
// lines in it, joining lines across chunks. It only reads: the text goes on to
// the client as it came.
type MarkerScanner struct {
	registry *Registry
	markers  map[string]string
	line     strings.Builder
	// overflow is set while the current line is too long to be a marker.
	overflow bool
	// fence is the fence that opened the code block the text is in, if any.
	fence string
	found map[string]bool
	hits  []MarkerHit
}

// NewMarkerScanner returns a scanner for one reply, or nil when no attribute
// has a marker.
func (registry *Registry) NewMarkerScanner() *MarkerScanner {
	markers := registry.Markers()
	if len(markers) == 0 {
		return nil
	}
	return &MarkerScanner{registry: registry, markers: markers, found: map[string]bool{}}
}

// Write reads more of the reply's visible text.
func (scanner *MarkerScanner) Write(text string) {
	if scanner == nil {
		return
	}
	for text != "" {
		end := strings.IndexByte(text, '\n')
		if end < 0 {
			scanner.keep(text)
			return
		}
		scanner.keep(text[:end])
		scanner.endLine()
		text = text[end+1:]
	}
}

// Close reads the last line and returns what the reply held: for each
// attribute the first marker line that counts, and the lines that did not,
// in the order they came.
func (scanner *MarkerScanner) Close() []MarkerHit {
	if scanner == nil {
		return nil
	}
	scanner.endLine()
	return scanner.hits
}

func (scanner *MarkerScanner) keep(text string) {
	if scanner.overflow {
		return
	}
	if scanner.line.Len()+len(text) > maxMarkerLine {
		scanner.overflow = true
		scanner.line.Reset()
		return
	}
	scanner.line.WriteString(text)
}

func (scanner *MarkerScanner) endLine() {
	line := strings.TrimSpace(scanner.line.String())
	overflow := scanner.overflow
	scanner.line.Reset()
	scanner.overflow = false
	if overflow {
		return
	}
	if scanner.fenceLine(line) {
		return
	}
	hit, ok := scanner.registry.matchMarker(scanner.markers, line)
	if !ok || scanner.found[hit.Key] || len(scanner.hits) >= contract.MaxRequestMetadataEvents {
		return
	}
	if scanner.fence != "" {
		hit.Reason = contract.MetadataIgnoredInCodeBlock
	}
	if hit.Valid() {
		scanner.found[hit.Key] = true
	}
	scanner.hits = append(scanner.hits, hit)
}

// fenceLine follows fenced code: a line of three or more backticks or tildes
// opens a block, and a line of at least as many of the same closes it.
func (scanner *MarkerScanner) fenceLine(line string) bool {
	if scanner.fence != "" {
		if strings.HasPrefix(line, scanner.fence) && strings.Trim(line, scanner.fence[:1]) == "" {
			scanner.fence = ""
		}
		return false
	}
	for _, mark := range []string{"`", "~"} {
		if strings.HasPrefix(line, mark+mark+mark) {
			run := len(line) - len(strings.TrimLeft(line, mark))
			scanner.fence = strings.Repeat(mark, run)
			return true
		}
	}
	return false
}

// HistoryEntry is one message of a conversation as a client replayed it:
// the text the user typed, or the text of a reply.
type HistoryEntry struct {
	User bool
	Text string
}

// Replay works out the values a session's history binds, for a session
// nothing is stored for, as when a client lost its session: in order, a reply's
// marker binds an attribute that has no value yet, and a message that is only
// a marker binds whatever was there.
func (registry *Registry) Replay(history []HistoryEntry) map[string]MarkerHit {
	values := map[string]MarkerHit{}
	for _, entry := range history {
		if entry.User {
			if hit, ok := registry.UserMarker(entry.Text); ok && hit.Valid() {
				values[hit.Key] = hit
			}
			continue
		}
		scanner := registry.NewMarkerScanner()
		if scanner == nil {
			return values
		}
		scanner.Write(entry.Text)
		for _, hit := range scanner.Close() {
			if _, bound := values[hit.Key]; hit.Valid() && !bound {
				values[hit.Key] = hit
			}
		}
	}
	return values
}
