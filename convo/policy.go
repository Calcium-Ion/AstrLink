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
// lookup may be nil, in which case Resolve only computes Inbound and
// TurnIndex. Errors from lookup abort Resolve.
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
	decision.TurnIndex = policy.NextTurnIndex(summary, matched)
	return decision, nil
}

// NextTurnIndex derives the 1-based user turn of a request.
//
// When the request continues server-side state (Stateful) and the explicit
// cursor matched a record that knows its turn, the body holds only the delta,
// so the turn is the matched turn plus one if the delta contains a user
// message. Otherwise the turn is the number of user messages in the replayed
// history. nil means the protocol exposes no user turns.
func (policy Policy) NextTurnIndex(summary RequestSummary, matched *Match) *int {
	if matched != nil && summary.Stateful && matched.Kind == KindExplicit && matched.HasTurnIndex && matched.TurnIndex >= 1 {
		next := matched.TurnIndex
		if summary.HasUserMessage {
			next++
		}
		return &next
	}
	if summary.UserTurnCount > 0 {
		next := summary.UserTurnCount
		return &next
	}
	return nil
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
