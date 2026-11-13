// Package format edits client config files in place, so that the comments,
// layout, and keys AstrLink does not own stay as they are. Keys are dotted
// paths from the file's root; a `.env` name is one key.
package format

import "fmt"

// Invalid is a parse failure, with the 1-based line it stops at (0 when
// unknown).
type Invalid struct {
	Message string
	Line    int
}

func (e *Invalid) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s (line %d)", e.Message, e.Line)
	}
	return e.Message
}

func invalid(message string, line int) *Invalid {
	return &Invalid{Message: message, Line: line}
}

func errorf(format string, args ...any) error { return fmt.Errorf(format, args...) }
