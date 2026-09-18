package convo

import (
	"context"
	"time"
)

// Policy carries every tunable limit. The zero value is not usable; start from
// DefaultPolicy and override fields.
type Policy struct {
	// Registry resolves adapters. nil means the built-in adapters.
	Registry *Registry
	// Window bounds how far back KindEchoID and KindFingerprint lookups may
	// match. Zero disables the bound.
	Window time.Duration
	// MinFingerprintRunes is the shortest normalized assistant text that
	// yields a fingerprint. Shorter replies ("OK") repeat across unrelated
	// conversations and would merge them.
	MinFingerprintRunes int
	// MaxInboundEchoIDs bounds how many echo ids a request contributes to the
	// lookup, taken from the end of the history where the newest ids live.
	MaxInboundEchoIDs int
	// MaxOutputEchoIDs bounds how many echo ids one response stores.
	MaxOutputEchoIDs int
	// MaxUserTextBytes bounds RequestSummary.LastUserText and FirstUserText.
	MaxUserTextBytes int
	// MaxResponseBytes bounds how much of a non-streaming body, or a single
	// streaming line, ResponseObserver.Write buffers before giving up.
	MaxResponseBytes int
	// EntropyCheck rejects echo ids too predictable to be safe session keys.
	// nil means HasCursorEntropy.
	EntropyCheck func(id string) bool
	// StripLeadingThink removes a leading <think>...</think> block before
	// fingerprinting, matching clients that hide reasoning embedded in
	// content.
	StripLeadingThink bool
}

// DefaultPolicy returns the recommended limits.
func DefaultPolicy() Policy {
	return Policy{
		Window:              7 * 24 * time.Hour,
		MinFingerprintRunes: 32,
		MaxInboundEchoIDs:   32,
		MaxOutputEchoIDs:    16,
		MaxUserTextBytes:    4096,
		MaxResponseBytes:    8 << 20,
		EntropyCheck:        HasCursorEntropy,
		StripLeadingThink:   true,
	}
}

func (policy Policy) entropyCheck() func(string) bool {
	if policy.EntropyCheck != nil {
		return policy.EntropyCheck
	}
	return HasCursorEntropy
}

func (policy Policy) maxInboundEchoIDs() int {
	if policy.MaxInboundEchoIDs > 0 {
		return policy.MaxInboundEchoIDs
	}
	return DefaultPolicy().MaxInboundEchoIDs
}

func (policy Policy) maxOutputEchoIDs() int {
	if policy.MaxOutputEchoIDs > 0 {
		return policy.MaxOutputEchoIDs
	}
	return DefaultPolicy().MaxOutputEchoIDs
}

func (policy Policy) maxUserTextBytes() int {
	if policy.MaxUserTextBytes > 0 {
		return policy.MaxUserTextBytes
	}
	return DefaultPolicy().MaxUserTextBytes
}

func (policy Policy) maxResponseBytes() int {
	if policy.MaxResponseBytes > 0 {
		return policy.MaxResponseBytes
	}
	return DefaultPolicy().MaxResponseBytes
}

func (policy Policy) minFingerprintRunes() int {
	if policy.MinFingerprintRunes > 0 {
		return policy.MinFingerprintRunes
	}
	return DefaultPolicy().MinFingerprintRunes
}

// Resolve decides which earlier session the request continues. Layers are
// tried most trusted first and the first hit wins:
//
//  1. KindExplicit with an unrestricted Scope;
//  2. KindEchoID restricted to the same principal and Window;
//  3. KindFingerprint with the same restriction, skipped when fp is nil or
//     the request carries no assistant digest.
//
// lookup may be nil, in which case Resolve only computes Inbound and Turn.
// Errors from lookup abort Resolve.
func (policy Policy) Resolve(
	ctx context.Context,
	summary RequestSummary,
	fp *Fingerprinter,
	lookup Lookup,
	now time.Time,
) (Decision, error) {
	decision := Decision{}
	scoped := Scope{SamePrincipal: true}
	if policy.Window > 0 {
		scoped.NotBefore = now.Add(-policy.Window)
	}
	layers := []struct {
		kind   Kind
		values []string
		scope  Scope
	}{
		{KindExplicit, summary.ExplicitCursors, Scope{}},
		{KindEchoID, summary.EchoIDs, scoped},
	}
	if fp != nil {
		if fingerprint := fp.Fingerprint(summary.AssistantDigest); fingerprint != "" {
			layers = append(layers, struct {
				kind   Kind
				values []string
				scope  Scope
			}{KindFingerprint, []string{fingerprint}, scoped})
		}
	}
	var matched *Match
	for _, layer := range layers {
		if len(layer.values) == 0 {
			continue
		}
		for _, value := range layer.values {
			decision.Inbound = append(decision.Inbound, Cursor{Kind: layer.kind, Direction: DirectionIn, Value: value})
		}
		if matched != nil || lookup == nil {
			continue
		}
		match, ok, err := lookup(ctx, layer.kind, layer.values, layer.scope)
		if err != nil {
			return decision, err
		}
		if !ok || match.SessionID == "" {
			continue
		}
		if match.Kind == "" {
			match.Kind = layer.kind
		}
		copied := match
		matched = &copied
		decision.Matched = true
		decision.Match = match
	}
	var userFingerprint string
	if fp != nil {
		userFingerprint = fp.Fingerprint(summary.LastUserDigest)
	}
	decision.Turn = policy.NextTurn(summary, matched, userFingerprint)
	return decision, nil
}

// NextTurn places a request relative to the record it continues.
//
// The first request of a session is turn 1 whatever its history holds: a
// client may replay a conversation that started elsewhere, and a harness may
// replay notes, skill text, or a compaction summary as user messages. None of
// that is knowable from one body, so the turn is never the absolute count of
// user messages. Instead a request starts a new turn when, compared with the
// matched record, its history holds more user messages or its newest user
// text changed (userFingerprint is the keyed fingerprint of
// summary.LastUserDigest, "" when the host has none). A request that changes
// neither — the next call of an agent loop, which only appends assistant
// and tool items — shares the matched turn.
//
// Stateful requests (Responses with previous_response_id) carry only the
// delta, so "more user messages" means the delta contains one; the stored
// count accumulates so a later full replay still compares correctly.
//
// A match whose record stored no turn cannot be compared and restarts the
// count at 1. nil means the request has no user turns at all (completions,
// a bare tool result on a stateful chain without a comparable match).
func (policy Policy) NextTurn(summary RequestSummary, matched *Match, userFingerprint string) *TurnState {
	if matched == nil || matched.Turn == nil || matched.Turn.Index < 1 {
		if !summary.HasUserMessage {
			return nil
		}
		return &TurnState{Index: 1, UserMessages: summary.UserTurnCount, LastUserFingerprint: userFingerprint}
	}
	previous := *matched.Turn
	next := TurnState{Index: previous.Index, UserMessages: previous.UserMessages, LastUserFingerprint: previous.LastUserFingerprint}
	if summary.Stateful {
		if summary.HasUserMessage {
			next.Index++
			next.UserMessages += summary.UserTurnCount
			if userFingerprint != "" {
				next.LastUserFingerprint = userFingerprint
			}
		}
		return &next
	}
	if !summary.HasUserMessage {
		return &next
	}
	progressed := summary.UserTurnCount > previous.UserMessages ||
		(userFingerprint != "" && previous.LastUserFingerprint != "" && userFingerprint != previous.LastUserFingerprint)
	if progressed {
		next.Index++
	}
	next.UserMessages = summary.UserTurnCount
	if userFingerprint != "" {
		next.LastUserFingerprint = userFingerprint
	}
	return &next
}

// OutputCursors converts what a response produced into cursors the host
// should store under the request's session so later requests can link to it.
func (policy Policy) OutputCursors(summary ResponseSummary, fp *Fingerprinter) []Cursor {
	cursors := make([]Cursor, 0, 2+len(summary.EchoIDs))
	if value := clampCursorValue(summary.OutputID); value != "" {
		cursors = append(cursors, Cursor{Kind: KindExplicit, Direction: DirectionOut, Value: value})
	}
	limit := policy.maxOutputEchoIDs()
	for _, id := range summary.EchoIDs {
		if len(cursors) >= limit+1 {
			break
		}
		if value := clampCursorValue(id); value != "" {
			cursors = append(cursors, Cursor{Kind: KindEchoID, Direction: DirectionOut, Value: value})
		}
	}
	if fp != nil {
		if fingerprint := fp.Fingerprint(summary.AssistantDigest); fingerprint != "" {
			cursors = append(cursors, Cursor{Kind: KindFingerprint, Direction: DirectionOut, Value: fingerprint})
		}
	}
	return cursors
}
