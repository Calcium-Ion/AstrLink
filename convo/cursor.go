package convo

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

// Kind classifies how a cursor value came to exist. Kinds differ in how much a
// match can be trusted, so Policy.Resolve applies a different Scope to each.
type Kind string

const (
	// KindExplicit is an identifier the client or protocol already sends to
	// name the conversation: previous_response_id, conversation ids,
	// prompt_cache_key, Claude Code's session id, and so on. It is matched
	// globally because the client chose it on purpose.
	KindExplicit Kind = "explicit"
	// KindEchoID is an opaque id the model produced and the client must send
	// back verbatim, such as tool call ids. It is matched only within the same
	// principal and a time window, and only when the id has enough entropy.
	KindEchoID Kind = "echo_id"
	// KindFingerprint is a keyed digest of the last assistant text. It links
	// text-only conversations and is matched with the same restrictions as
	// KindEchoID plus a minimum text length.
	KindFingerprint Kind = "fingerprint"
)

// Valid reports whether kind is one of the defined kinds.
func (kind Kind) Valid() bool {
	switch kind {
	case KindExplicit, KindEchoID, KindFingerprint:
		return true
	default:
		return false
	}
}

// Direction tells whether a cursor arrived in a request or was produced by a
// response. Explicit cursors are stored in both directions so two requests
// that name the same conversation link even when neither response carried it.
type Direction string

const (
	DirectionIn  Direction = "in"
	DirectionOut Direction = "out"
)

// Valid reports whether direction is one of the defined directions.
func (direction Direction) Valid() bool {
	return direction == DirectionIn || direction == DirectionOut
}

// MaxCursorRunes bounds a cursor value. Longer values are hashed by the
// adapter that produced them or dropped.
const MaxCursorRunes = 256

// Cursor is one typed session cursor.
type Cursor struct {
	Kind      Kind      `json:"kind"`
	Direction Direction `json:"direction"`
	Value     string    `json:"value"`
}

// Validate reports whether the cursor is well-formed: known kind and
// direction, non-empty bounded value without control characters.
func (cursor Cursor) Validate() error {
	if !cursor.Kind.Valid() {
		return errInvalidCursor("unknown kind")
	}
	if !cursor.Direction.Valid() {
		return errInvalidCursor("unknown direction")
	}
	if !validCursorValue(cursor.Value) {
		return errInvalidCursor("value must be 1 to 256 printable runes")
	}
	return nil
}

type errInvalidCursor string

func (err errInvalidCursor) Error() string { return "convo: invalid cursor: " + string(err) }

// Scope constrains a Lookup. SamePrincipal requires the host to restrict the
// query to the principal (API key, user, tenant) of the current request;
// NotBefore, when non-zero, excludes records started before that instant.
type Scope struct {
	SamePrincipal bool
	NotBefore     time.Time
}

// TurnState is what a record remembers about its user turn so the next
// request in the session can be placed relative to it. Hosts store it with
// the record and return it from Lookup; Policy.NextTurn produces it.
type TurnState struct {
	// Index is the 1-based user turn within the session.
	Index int
	// UserMessages is how many user messages the request's history held
	// (cumulative for stateful chains). A later request whose history holds
	// more user messages has started a new turn.
	UserMessages int
	// LastUserFingerprint is the keyed fingerprint of the newest user text,
	// or "" when the host has no Fingerprinter. A changed fingerprint at an
	// unchanged count (a harness that compacted history, a client that edited
	// the last message) also starts a new turn.
	LastUserFingerprint string
}

// Match is a stored record that a Lookup found for one of the queried values.
type Match struct {
	SessionID string
	Kind      Kind
	Value     string
	// Turn is the matched record's stored turn state, nil when unknown.
	Turn *TurnState
}

// Lookup is the only callback from convo into the host's storage. It must
// return the most recent root record whose stored cursors of the given kind
// contain any of values, honouring scope. Records that produced a value
// (DirectionOut) must win over records that merely named it (DirectionIn):
// the producer carries the turn a continuation builds on. DirectionIn is
// searched for KindExplicit only, so client-chosen ids that no response
// emits (prompt_cache_key, Claude Code session ids) still link siblings.
type Lookup func(ctx context.Context, kind Kind, values []string, scope Scope) (Match, bool, error)

// Decision is the outcome of Policy.Resolve.
type Decision struct {
	// Matched is true when some layer found an earlier session.
	Matched bool
	// Match describes the winning record; zero when Matched is false.
	Match Match
	// Turn is the user turn this request belongs to, or nil when the protocol
	// has no user turns. Hosts store it and hand it back through Match.Turn.
	Turn *TurnState
	// Inbound lists every cursor the request carried, in layer order. Hosts
	// should persist the KindExplicit entries (see PersistentInbound); the
	// others are lookup keys only.
	Inbound []Cursor
}

// PersistentInbound returns the inbound cursors a host should store alongside
// the record so sibling requests naming the same conversation can link.
func (decision Decision) PersistentInbound() []Cursor {
	kept := make([]Cursor, 0, len(decision.Inbound))
	for _, cursor := range decision.Inbound {
		if cursor.Kind == KindExplicit {
			kept = append(kept, cursor)
		}
	}
	return kept
}

// clampCursorValue trims and bounds a cursor value; it returns "" when the
// value is unusable (empty, too long, or contains control characters).
func clampCursorValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > MaxCursorRunes {
		return ""
	}
	if !validCursorValue(value) {
		return ""
	}
	return value
}

func validCursorValue(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > MaxCursorRunes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
