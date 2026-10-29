package convo

import (
	"encoding/json"
	"errors"

	"github.com/QuantumNous/astrlink/convo/internal/jsonx"
)

// ErrInvalidJSON is returned by InspectBody when the body is not a JSON object.
var ErrInvalidJSON = errors.New("convo: request body is not a JSON object")

// Built-in adapters share one decoded item across request-side observations.
// The public Adapter interface stays compatible with custom protocols.
type requestItem struct {
	user, assistant, toolResult bool
	// declaresTools is set on an item that offers the model tools, such as
	// a Responses Lite additional_tools item.
	declaresTools bool
	userText      string
	echoIDs       []string
}

type requestItemInspector interface {
	inspectItem(json.RawMessage) requestItem
}

func inspectRequestItem(adapter Adapter, item json.RawMessage) requestItem {
	if inspector, ok := adapter.(requestItemInspector); ok {
		return inspector.inspectItem(item)
	}
	result := requestItem{
		user:       adapter.IsUserTurn(item),
		toolResult: adapter.IsToolResult(item),
		echoIDs:    adapter.InboundEchoIDs(item),
	}
	if result.user {
		result.userText = adapter.UserText(item)
	}
	_, result.assistant = adapter.AssistantText(item)
	return result
}

// RequestSummary is everything Policy.Resolve needs from one request, plus the
// user text a host may want for previews. It never contains assistant text.
type RequestSummary struct {
	Protocol Protocol
	// SessionCursor is an authoritative client session identifier supplied by
	// the host, such as Codex's Session_id header. A new value starts a new
	// session even when the request replays another conversation's history.
	SessionCursor string
	// ExplicitCursors are conversation identifiers the request names, most
	// trusted first. Only the first is used when SessionCursor is absent.
	ExplicitCursors []string
	// Stateful is true when the request continues server-side history, so
	// the body holds only the newest turn (Responses with previous_response_id).
	Stateful bool
	// EchoIDs are ids earlier responses produced, newest first, deduplicated,
	// entropy-checked, and bounded by Policy.MaxInboundEchoIDs.
	EchoIDs []string
	// UserTurnCount is the number of user messages in the history (in the
	// delta only, when Stateful). Tool results and messages that are only
	// dropped harness blocks do not count, nor do notes under
	// Policy.NotesFollowPrompt.
	UserTurnCount int
	// HasUserMessage is UserTurnCount > 0; kept separate for readability at
	// call sites that combine it with Stateful.
	HasUserMessage bool
	// AssistantDigest is the normalized digest of the last assistant message's
	// visible text, or nil when there is none or it is shorter than
	// Policy.MinFingerprintRunes.
	AssistantDigest []byte
	// LastUserDigest is the normalized digest of the newest user message's
	// visible text, or nil when there is none. Policy.NextTurn fingerprints
	// it to notice a new turn whose history has the same user-message count.
	LastUserDigest []byte
	// UserDigests lists, for each of the newest user messages that count
	// (at most Policy.MaxContinuationUsers), the digest LastUserDigest had
	// at that message: the newest visible user text at or before it. They
	// are oldest first and the last equals LastUserDigest, so the digest of
	// the n-th counted user message is UserDigests[n-1-(UserTurnCount-
	// len(UserDigests))]. Policy.Resolve uses them to tell which earlier
	// call of a thread the history continues. Nil when Stateful.
	UserDigests [][]byte
	// HasTools is set when the request offers the model tools. Under
	// Policy.ToolFreeHelpers a request without tools whose history
	// continues no call of its thread is a helper call, such as a title,
	// not the conversation's next call.
	HasTools bool
	// SideCall is set by the host for a request it recognizes as a helper
	// call of the conversation, such as a title request. Policy.Resolve
	// keeps it in the matched turn instead of placing it by its history.
	SideCall bool
	// LastUserText and FirstUserText are visible user texts bounded by
	// Policy.MaxUserTextBytes. They are raw: hosts must redact before display.
	LastUserText  string
	FirstUserText string
}

// InspectBody parses body and calls Policy.InspectFields with DefaultPolicy.
func InspectBody(protocol Protocol, body []byte) (RequestSummary, error) {
	return DefaultPolicy().InspectBody(protocol, body)
}

// InspectFields summarises an already-decoded request with DefaultPolicy.
func InspectFields(protocol Protocol, fields map[string]json.RawMessage) (RequestSummary, error) {
	return DefaultPolicy().InspectFields(protocol, fields)
}

// InspectBody parses body as a JSON object and summarises it.
func (policy Policy) InspectBody(protocol Protocol, body []byte) (RequestSummary, error) {
	fields, ok := jsonx.Object(body)
	if !ok {
		return RequestSummary{Protocol: protocol}, ErrInvalidJSON
	}
	return policy.InspectFields(protocol, fields)
}

// InspectFields summarises a decoded request. It walks the history array once.
// Unknown protocols return ErrUnsupportedProtocol with an empty summary.
func (policy Policy) InspectFields(protocol Protocol, fields map[string]json.RawMessage) (RequestSummary, error) {
	summary := RequestSummary{Protocol: protocol}
	adapter, ok := policy.adapterFor(protocol)
	if !ok {
		return summary, ErrUnsupportedProtocol
	}
	if fields == nil {
		return summary, nil
	}
	summary.ExplicitCursors = adapter.ExplicitCursors(fields)
	summary.Stateful = adapter.IsStateful(fields)
	summary.HasTools = offersTools(fields)

	history := fields[adapter.HistoryField()]
	if text, ok := jsonx.String(history); ok {
		// Responses accepts input as a bare string: exactly one user turn.
		if visible := visibleText(text); visible != "" {
			summary.UserTurnCount = 1
			summary.HasUserMessage = true
			summary.LastUserText = truncateBytes(visible, policy.maxUserTextBytes())
			summary.FirstUserText = summary.LastUserText
			summary.LastUserDigest = DigestText(visible, false)
			if !summary.Stateful {
				summary.UserDigests = [][]byte{summary.LastUserDigest}
			}
		}
		return summary, nil
	}
	items, ok := jsonx.Array(history)
	if !ok {
		return summary, nil
	}

	var (
		echoIDs       []string
		lastAssistant json.RawMessage
		haveAssistant bool
		lastUserText  string
		// userTexts holds, for each of the newest counted user messages,
		// the newest user text at or before it; they are digested once the
		// walk knows which messages are the newest.
		userTexts = make([]string, 0, min(len(items), policy.maxContinuationUsers()))
		// opensTurn is whether the next user message would open a turn.
		opensTurn = true
	)
	for _, item := range items {
		inspected := inspectRequestItem(adapter, item)
		summary.HasTools = summary.HasTools || inspected.declaresTools
		user := inspected.user
		if policy.NotesFollowPrompt {
			switch {
			case inspected.toolResult:
				user, opensTurn = false, false
			case inspected.assistant:
				opensTurn = true
			case user:
				user, opensTurn = opensTurn, false
			}
		}
		if user {
			summary.UserTurnCount++
			summary.HasUserMessage = true
			if text := inspected.userText; text != "" {
				if summary.FirstUserText == "" {
					summary.FirstUserText = truncateBytes(text, policy.maxUserTextBytes())
				}
				summary.LastUserText = truncateBytes(text, policy.maxUserTextBytes())
				lastUserText = text
			}
			if lastUserText != "" {
				if len(userTexts) == cap(userTexts) {
					userTexts = append(userTexts[:0], userTexts[1:]...)
				}
				userTexts = append(userTexts, lastUserText)
			}
		}
		echoIDs = append(echoIDs, inspected.echoIDs...)
		if inspected.assistant {
			lastAssistant = item
			haveAssistant = true
		}
	}
	if lastUserText != "" {
		summary.LastUserDigest = DigestText(lastUserText, false)
	}
	if !summary.Stateful && len(userTexts) > 0 {
		summary.UserDigests = make([][]byte, len(userTexts))
		for index, text := range userTexts {
			switch {
			case index == len(userTexts)-1:
				summary.UserDigests[index] = summary.LastUserDigest
			case index > 0 && text == userTexts[index-1]:
				summary.UserDigests[index] = summary.UserDigests[index-1]
			default:
				summary.UserDigests[index] = DigestText(text, false)
			}
		}
	}
	summary.EchoIDs = policy.selectInboundEchoIDs(echoIDs)
	if haveAssistant {
		text, _ := adapter.AssistantText(lastAssistant)
		normalizer := NewTextNormalizer(policy.StripLeadingThink)
		normalizer.WriteString(text)
		if normalizer.Runes() >= policy.minFingerprintRunes() {
			summary.AssistantDigest = normalizer.Digest()
		}
	}
	return summary, nil
}

// offersTools reports whether the request body declares tools at the top
// level: tools in every protocol, and OpenAI Chat's legacy functions. An
// empty list declares none.
func offersTools(fields map[string]json.RawMessage) bool {
	for _, name := range []string{"tools", "functions"} {
		if items, ok := jsonx.Array(fields[name]); ok && len(items) > 0 {
			return true
		}
	}
	return false
}

// selectInboundEchoIDs keeps the newest ids, which sit at the end of the
// history, deduplicates them, and drops low-entropy values.
func (policy Policy) selectInboundEchoIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	limit := policy.maxInboundEchoIDs()
	check := policy.entropyCheck()
	seen := make(map[string]struct{}, min(len(ids), limit))
	selected := make([]string, 0, min(len(ids), limit))
	for index := len(ids) - 1; index >= 0 && len(selected) < limit; index-- {
		value := clampCursorValue(ids[index])
		if value == "" || !check(value) {
			continue
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		selected = append(selected, value)
	}
	if len(selected) == 0 {
		return nil
	}
	return selected
}
