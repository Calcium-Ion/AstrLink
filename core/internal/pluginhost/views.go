package pluginhost

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/tidwall/gjson"
)

// Bounds of the user text a turn view carries.
const (
	maxTurnTextRunes     = 200_000
	maxPreviousTypedSize = 4 << 10
)

// SessionView is what prepareSession reads.
type SessionView struct {
	Protocol string                      `json:"protocol"`
	Model    string                      `json:"model"`
	Client   string                      `json:"client"`
	Meta     map[string]plugin.MetaValue `json:"meta"`
	Session  plugin.SessionRef           `json:"session"`
	// Language is the interface language the session started with: what
	// the plugin injects is written in it.
	Language string `json:"language"`
	// Reason is why prepareSession runs: start, bound, config, compact,
	// manual or auto. Turn hooks have none.
	Reason string `json:"reason,omitempty"`
	// Workspace is the last element of the working directory the client
	// named, never the whole path.
	Workspace *WorkspaceView `json:"workspace,omitempty"`
	// Redacted tells prepareSession that the privacy policy replaces what
	// it finds with placeholders, so the plugin's data holds them too.
	Redacted bool `json:"redacted,omitempty"`
}

// WorkspaceView names the client's working directory by its last element.
type WorkspaceView struct {
	Name string `json:"name"`
}

// TurnView is what prepareTurn and observeTurn read: the turn as the client
// sent it, before anything was injected.
type TurnView struct {
	SessionView
	Stream bool     `json:"stream"`
	User   UserText `json:"user"`
	// Late marks a turn observeTurn sees after the fact: it came while the
	// session had no value of the plugin's attributes, and is captured now
	// that it has one, with that value.
	Late bool `json:"late,omitempty"`
}

// UserText is the turn's opening user message: as sent, without the
// client's wrappers, and the previous turn's typed text.
type UserText struct {
	Text     string `json:"text"`
	Typed    string `json:"typed"`
	Previous string `json:"previous,omitempty"`
}

var adapters = convo.DefaultRegistry()

// convoProtocols are the protocols whose turns plugins see.
var convoProtocols = map[contract.ProtocolID]convo.Protocol{
	contract.ProtocolAnthropicMessages:     convo.AnthropicMessages,
	contract.ProtocolOpenAIChat:            convo.OpenAIChat,
	contract.ProtocolOpenAIResponses:       convo.OpenAIResponses,
	contract.ProtocolGoogleGenerateContent: convo.GeminiGenerateContent,
}

// turn is what a request body holds of its conversation.
type turn struct {
	// opened is set when the body carries a user message that opens a turn;
	// user is then the newest one.
	opened bool
	user   UserText
	// own is the opening message without the reminders a client adds to it:
	// what to read a marker line the user typed from. The typed text will
	// not do: it unwraps a message that is one tag, which a marker line is.
	own string
	// history is the conversation in order, for working out a lost binding.
	history []metadata.HistoryEntry
	// first is what the history opens with: its first user message, and
	// the newest compaction a client left in it. A compacted history opens
	// with something else.
	first string
	// messages are the first user messages as sent, where clients name
	// their working directory.
	messages []string
}

// compactionItems are the Responses items a client leaves where it
// compacted its history.
var compactionItems = map[string]bool{"compaction": true, "compaction_summary": true}

// readTurn reads the conversation of a request body in the client's
// protocol.
func readTurn(protocol contract.ProtocolID, body []byte) turn {
	var result turn
	adapter, ok := adapters.Lookup(convoProtocols[protocol])
	if !ok {
		return result
	}
	history := gjson.GetBytes(body, adapter.HistoryField())
	var items []json.RawMessage
	switch {
	case history.Type == gjson.String:
		// Responses takes input as a bare string: one user message.
		items = []json.RawMessage{json.RawMessage(`{"role":"user","content":` + history.Raw + `}`)}
	case history.IsArray():
		for _, element := range history.Array() {
			items = append(items, json.RawMessage(element.Raw))
		}
	}
	previous, opening, compaction := "", "", ""
	for _, item := range items {
		if protocol == contract.ProtocolOpenAIResponses && compactionItems[gjson.GetBytes(item, "type").Str] {
			compaction = string(item)
			continue
		}
		if adapter.IsUserTurn(item) {
			if result.opened {
				previous = result.user.Typed
			}
			text := rawText(protocol, item)
			if !result.opened {
				opening = text
			}
			if len(result.messages) < workspaceMessages {
				result.messages = append(result.messages, text)
			}
			result.opened = true
			result.user = UserText{Text: text, Typed: adapter.UserText(item)}
			result.own = withoutReminders(text)
			result.history = append(result.history, metadata.HistoryEntry{User: true, Text: result.own})
			continue
		}
		if text, ok := adapter.AssistantText(item); ok {
			result.history = append(result.history, metadata.HistoryEntry{Text: text})
		}
	}
	result.user.Text = clampRunes(result.user.Text, maxTurnTextRunes)
	result.user.Typed = clampRunes(result.user.Typed, maxTurnTextRunes)
	result.user.Previous = clampBytes(previous, maxPreviousTypedSize)
	if opening != "" || compaction != "" {
		result.first = opening + "\x00" + compaction
	}
	return result
}

// rawText is the text of a user message as sent, wrappers included: its
// text parts joined by line breaks. Tool results and attachments are not
// text.
func rawText(protocol contract.ProtocolID, item json.RawMessage) string {
	message := gjson.ParseBytes(item)
	member := "content"
	if protocol == contract.ProtocolGoogleGenerateContent {
		member = "parts"
	}
	content := message.Get(member)
	if content.Type == gjson.String {
		return content.Str
	}
	var texts []string
	for _, part := range content.Array() {
		if part.Type == gjson.String {
			texts = append(texts, part.Str)
			continue
		}
		text := part.Get("text")
		if text.Type != gjson.String {
			continue
		}
		switch kind := part.Get("type").Str; {
		case protocol == contract.ProtocolGoogleGenerateContent:
			if part.Get("thought").Bool() {
				continue
			}
		case kind != "" && kind != "text" && kind != "input_text":
			continue
		}
		texts = append(texts, text.Str)
	}
	return strings.Join(texts, "\n")
}

// Claude Code wraps notes it adds to a user's message in this tag; what is
// inside is never the user's own.
const (
	reminderOpen  = "<system-reminder>"
	reminderClose = "</system-reminder>"
)

// withoutReminders drops the reminders at either end of text.
func withoutReminders(text string) string {
	for {
		text = strings.TrimSpace(text)
		switch {
		case strings.HasPrefix(text, reminderOpen):
			end := strings.Index(text, reminderClose)
			if end < 0 {
				return text
			}
			text = text[end+len(reminderClose):]
		case strings.HasSuffix(text, reminderClose):
			start := strings.LastIndex(text, reminderOpen)
			if start < 0 {
				return text
			}
			text = text[:start]
		default:
			return text
		}
	}
}

// typedOf is the typed text of a user message whose text is text, as the
// protocol's adapter reads it.
func typedOf(protocol contract.ProtocolID, text string) string {
	adapter, ok := adapters.Lookup(convoProtocols[protocol])
	if !ok {
		return text
	}
	encoded, err := json.Marshal(text)
	if err != nil {
		return text
	}
	if protocol == contract.ProtocolGoogleGenerateContent {
		return adapter.UserText(json.RawMessage(`{"role":"user","parts":[{"text":` + string(encoded) + `}]}`))
	}
	return adapter.UserText(json.RawMessage(`{"role":"user","content":` + string(encoded) + `}`))
}

func clampRunes(text string, max int) string {
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	count := 0
	for index := range text {
		if count == max {
			return text[:index]
		}
		count++
	}
	return text
}

func clampBytes(text string, max int) string {
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
