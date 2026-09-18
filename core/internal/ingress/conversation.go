package ingress

import (
	"context"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// conversationPolicy is the single convo configuration AstrLink uses. The
// defaults (7 day window, 32 rune fingerprint floor, entropy-checked echo
// ids) are documented in ADR 0015; change them there first.
var conversationPolicy = convo.DefaultPolicy()

// sessionFingerprintInfo is the HKDF info string that separates the session
// fingerprint key from every other use of the audit master key. Bumping it
// invalidates all stored fingerprints, which only costs text-only linking
// across the upgrade.
const sessionFingerprintInfo = "astrlink/session-fingerprint/v1"

// sessionLookupTimeout bounds the up-to-three storage queries Resolve issues
// on the request path. A slow database degrades to "new session", never to a
// slow request.
const sessionLookupTimeout = 500 * time.Millisecond

// convoProtocol maps an ingress protocol to its convo adapter. Protocols
// without replayed history (completions, model listings) return false and
// stay singleton sessions.
func convoProtocol(protocol contract.ProtocolID) (convo.Protocol, bool) {
	switch protocol {
	case contract.ProtocolOpenAIChat:
		return convo.OpenAIChat, true
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		return convo.OpenAIResponses, true
	case contract.ProtocolAnthropicMessages:
		return convo.AnthropicMessages, true
	case contract.ProtocolGoogleGenerateContent:
		return convo.GeminiGenerateContent, true
	default:
		return "", false
	}
}

// sessionFingerprints lazily derives and caches the fingerprint key from the
// audit master key. The cache only fills on success so a transient storage
// error on the first request does not disable fingerprints for the process.
type sessionFingerprints struct {
	mu          sync.Mutex
	fingerprint *convo.Fingerprinter
}

func (cache *sessionFingerprints) get(ctx context.Context, blobs AuditBlobPersister, logf func(string, ...any)) *convo.Fingerprinter {
	if cache == nil || blobs == nil {
		return nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.fingerprint != nil {
		return cache.fingerprint
	}
	master, err := blobs.GetOrCreateAuditKey(ctx)
	if err != nil {
		if logf != nil {
			logf("session fingerprint key unavailable: %v", err)
		}
		return nil
	}
	cache.fingerprint = convo.NewFingerprinter(convo.DeriveFingerprintKey(master, sessionFingerprintInfo))
	return cache.fingerprint
}

// sessionLookup adapts the record store to convo.Lookup, binding the
// principal to the local access token that authenticated this request.
func sessionLookup(store RequestRecordStore, accessTokenID *contract.AccessTokenID) convo.Lookup {
	if store == nil {
		return nil
	}
	return func(ctx context.Context, kind convo.Kind, values []string, scope convo.Scope) (convo.Match, bool, error) {
		match, ok, err := store.FindSessionLink(ctx, contract.SessionCursorKind(kind), values, storage.SessionCursorScope{
			SamePrincipal:      scope.SamePrincipal,
			LocalAccessTokenID: accessTokenID,
			NotBefore:          scope.NotBefore,
		})
		if err != nil || !ok {
			return convo.Match{}, false, err
		}
		result := convo.Match{SessionID: string(match.SessionID), Kind: kind, Value: match.Value}
		// A row that stored a turn but no comparison state (written before
		// migration v23) cannot tell the next request whether it is a new
		// turn; treat it as unknown so the count restarts instead of
		// incrementing on every call of a loop.
		if match.TurnIndex != nil && match.TurnUserMessages != nil {
			result.Turn = &convo.TurnState{
				Index:               *match.TurnIndex,
				UserMessages:        *match.TurnUserMessages,
				LastUserFingerprint: match.TurnUserFingerprint,
			}
		}
		return result, true, nil
	}
}

func contractCursors(cursors []convo.Cursor) []contract.SessionCursor {
	if len(cursors) == 0 {
		return nil
	}
	converted := make([]contract.SessionCursor, 0, len(cursors))
	for _, cursor := range cursors {
		converted = append(converted, contract.SessionCursor{
			Kind:      contract.SessionCursorKind(cursor.Kind),
			Direction: contract.SessionCursorDirection(cursor.Direction),
			Value:     cursor.Value,
		})
	}
	return converted
}

// mergeSessionCursors concatenates inbound and outbound cursors, drops exact
// duplicates, and enforces the contract bound. Explicit cursors come first so
// they survive truncation.
func mergeSessionCursors(groups ...[]contract.SessionCursor) []contract.SessionCursor {
	type key struct {
		kind      contract.SessionCursorKind
		direction contract.SessionCursorDirection
		value     string
	}
	seen := make(map[key]struct{})
	var merged []contract.SessionCursor
	for _, group := range groups {
		for _, cursor := range group {
			id := key{cursor.Kind, cursor.Direction, cursor.Value}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			merged = append(merged, cursor)
		}
	}
	if len(merged) > contract.MaxSessionCursors {
		merged = merged[:contract.MaxSessionCursors]
	}
	return merged
}
