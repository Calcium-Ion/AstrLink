package contract

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// OutputKind is the part of a response a model is producing.
type OutputKind string

const (
	// OutputKindReasoning is thinking the provider exposes before the answer.
	OutputKindReasoning OutputKind = "reasoning"
	// OutputKindText is an answer, a refusal or other content for the caller.
	OutputKindText OutputKind = "text"
	// OutputKindTool is a tool call the client is asked to run.
	OutputKindTool OutputKind = "tool"
)

func (kind OutputKind) Valid() bool {
	switch kind {
	case OutputKindReasoning, OutputKindText, OutputKindTool:
		return true
	default:
		return false
	}
}

const (
	// MaxOutputTools bounds the tool calls one response names.
	MaxOutputTools = 8
	// MaxOutputToolNameRunes bounds one tool name.
	MaxOutputToolNameRunes = 64
)

// OutputActivity is what a call's response produces. While the call streams,
// Kind is the part arriving now; once it ends, the last part. Tools names the
// tool calls in the order they were made, so after a call that ended on a
// tool call the client is running Tools.
type OutputActivity struct {
	Kind  OutputKind `json:"kind"`
	Tools []string   `json:"tools,omitempty"`
}

func (output OutputActivity) Validate() error {
	if !output.Kind.Valid() {
		return fmt.Errorf("unknown output kind %q", output.Kind)
	}
	if len(output.Tools) > MaxOutputTools {
		return fmt.Errorf("output names at most %d tools", MaxOutputTools)
	}
	for _, name := range output.Tools {
		if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > MaxOutputToolNameRunes {
			return fmt.Errorf("output tool names must contain 1 to %d characters", MaxOutputToolNameRunes)
		}
	}
	return nil
}

// AddTool appends a tool call's name, made fit for Tools, unless Tools is
// full or nothing printable is left. It reports whether the name was added.
func (output *OutputActivity) AddTool(name string) bool {
	name = OutputToolName(name)
	if name == "" || len(output.Tools) >= MaxOutputTools {
		return false
	}
	output.Tools = append(output.Tools, name)
	return true
}

// OutputToolName is name made fit for OutputActivity.Tools: control
// characters become spaces, the ends are trimmed and the length is bounded.
// It returns "" when nothing printable is left.
func OutputToolName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, name)
	return ClampRunes(strings.TrimSpace(name), MaxOutputToolNameRunes)
}
