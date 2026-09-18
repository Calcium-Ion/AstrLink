package convo

import (
	"encoding/json"
	"errors"

	"github.com/QuantumNous/astrlink/convo/internal/jsonx"
)

// ErrInvalidJSON is returned by InspectBody when the body is not a JSON object.
var ErrInvalidJSON = errors.New("convo: request body is not a JSON object")

// RequestSummary is everything Policy.Resolve needs from one request, plus the
// user text a host may want for previews. It never contains assistant text.
type RequestSummary struct {
	Protocol Protocol
	// ExplicitCursors are conversation identifiers the request names, most
	// trusted first.
	ExplicitCursors []string
	// Stateful is true when the request continues server-side history, so
	// the body holds only the newest turn (Responses with previous_response_id).
	Stateful bool
	// EchoIDs are ids earlier responses produced, newest first, deduplicated,
	// entropy-checked, and bounded by Policy.MaxInboundEchoIDs.
	EchoIDs []string
	// UserTurnCount is the number of user messages in the history (in the
	// delta only, when Stateful). Tool results and messages that are only
	// dropped harness blocks do not count.
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

	history := fields[adapter.HistoryField()]
	if text, ok := jsonx.String(history); ok {
		// Responses accepts input as a bare string: exactly one user turn.
		if visible := visibleText(text); visible != "" {
			summary.UserTurnCount = 1
			summary.HasUserMessage = true
			summary.LastUserText = truncateBytes(visible, policy.maxUserTextBytes())
			summary.FirstUserText = summary.LastUserText
			summary.LastUserDigest = DigestText(visible, false)
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
	)
	for _, item := range items {
		if adapter.IsUserTurn(item) {
			summary.UserTurnCount++
			summary.HasUserMessage = true
			if text := adapter.UserText(item); text != "" {
				if summary.FirstUserText == "" {
					summary.FirstUserText = truncateBytes(text, policy.maxUserTextBytes())
				}
				summary.LastUserText = truncateBytes(text, policy.maxUserTextBytes())
				summary.LastUserDigest = DigestText(text, false)
			}
		}
		echoIDs = append(echoIDs, adapter.InboundEchoIDs(item)...)
		if _, isAssistant := adapter.AssistantText(item); isAssistant {
			lastAssistant = item
			haveAssistant = true
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
