package ingress

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/MoeForge/moego/ojson"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Claude Code hands the model a message the user sends while a turn runs
// inside the next call, as a system reminder beside the tool results:
// "<system-reminder>\nThe user sent a new message while you were working:\n
// <message>\n\nThis is how Claude Code surfaces messages …</system-reminder>".
// It may stand as a text block of its own or be folded into a tool result.
const (
	midTurnOpener = "The user sent a new message while you were working:\n"
	midTurnCloser = "\n\nThis is how Claude Code surfaces messages the user sends mid-turn"
	reminderClose = "</system-reminder>"
)

// claudeMidTurnMessages returns the messages the user sent while the turn
// ran that this call delivers: those in the newest message of the history
// only, so each is found on the one call that carried it and not on the
// calls that replay it.
func claudeMidTurnMessages(fields map[string]json.RawMessage) []string {
	raw, ok := fields["messages"]
	if !ok || !bytes.Contains(raw, []byte(midTurnOpener[:40])) {
		return nil
	}
	messages := ojson.ParseBytes(raw)
	if !messages.IsArray() {
		return nil
	}
	var last ojson.Result
	messages.ForEach(func(_, message ojson.Result) bool {
		last = message
		return true
	})
	if last.Get("role").Str != "user" {
		return nil
	}
	var found []string
	var visit func(content ojson.Result)
	visit = func(content ojson.Result) {
		if content.Type == ojson.String {
			found = appendMidTurn(found, content.Str)
			return
		}
		content.ForEach(func(_, block ojson.Result) bool {
			switch block.Get("type").Str {
			case "text":
				found = appendMidTurn(found, block.Get("text").Str)
			case "tool_result":
				visit(block.Get("content"))
			}
			return len(found) < contract.MaxMidTurnMessages
		})
	}
	visit(last.Get("content"))
	return found
}

func appendMidTurn(found []string, text string) []string {
	for len(found) < contract.MaxMidTurnMessages {
		start := strings.Index(text, midTurnOpener)
		if start < 0 {
			return found
		}
		text = text[start+len(midTurnOpener):]
		end := strings.Index(text, midTurnCloser)
		if close := strings.Index(text, reminderClose); end < 0 || (close >= 0 && close < end) {
			end = close
		}
		message := text
		if end >= 0 {
			message, text = text[:end], text[end:]
		} else {
			text = ""
		}
		if message = strings.TrimSpace(message); message != "" {
			found = append(found, message)
		}
	}
	return found
}

// cleanUserText makes user text fit a record: valid UTF-8, line breaks kept
// and other control characters dropped, obvious secrets cut out as the
// preview cuts them, at most contract.MaxInputTextRunes runes. cut marks
// text that was already cut short before it got here.
func cleanUserText(raw string, cut bool) string {
	text := strings.ReplaceAll(strings.ToValidUTF8(raw, ""), "\r\n", "\n")
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 32 || r == 127:
			return ' '
		}
		return r
	}, text)
	text = strings.TrimSpace(previewSecretPattern.ReplaceAllString(text, "…"))
	if text == "" {
		return ""
	}
	if cut || utf8.RuneCountInString(text) > contract.MaxInputTextRunes {
		text = contract.ClampRunes(text, contract.MaxInputTextRunes-1) + "…"
	}
	return text
}

// visibleUserText is user text a record may carry. Like the preview it
// follows the client body's exposure: as is while the body is shareable,
// masked once a redact or block decision withheld it, and absent while a
// gated body has no decision.
func (session *recordSession) visibleUserText(raw string, cut bool) string {
	switch session.privacyDecision {
	case contract.PrivacyDecisionRedact, contract.PrivacyDecisionBlock:
		raw = maskProtected(raw, session.protectedValues)
	default:
		if session.auditExposure(storage.AuditDirectionRequest) != storage.AuditExposureShareable {
			return ""
		}
	}
	return cleanUserText(raw, cut)
}

// visibleInputText is the whole message behind the record's preview; none
// when there is no preview, when the preview came from other text, or when
// the preview already says it all.
func (session *recordSession) visibleInputText() string {
	preview := session.visibleInputPreview()
	last := session.classified.Conversation.LastUserText
	if preview == "" || last == "" || sanitizePreview(last) != session.inputPreview {
		return ""
	}
	// convo cut the text at a rune boundary up to three bytes short.
	cut := len(last) > conversationPolicy.MaxUserTextBytes-utf8.UTFMax
	text := session.visibleUserText(last, cut)
	if text == preview {
		return ""
	}
	return text
}

// visibleMidTurnMessages are the messages sent while the turn ran that the
// record may carry.
func (session *recordSession) visibleMidTurnMessages() []string {
	if session.sideCall {
		return nil
	}
	var visible []string
	for _, raw := range session.classified.midTurn {
		if text := session.visibleUserText(raw, false); text != "" {
			visible = append(visible, text)
		}
	}
	return visible
}
