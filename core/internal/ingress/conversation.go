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

// conversationPolicyFor reads a request body the way its client lays out
// user messages. DeepSeek Harness sends its runtime and clock snapshots as
// plain user messages after the prompt, so only its turn openers are typed.
// The coding agents named here offer their tools on every call, so a call of
// theirs without tools that continues none of its thread is a helper call,
// such as a title.
func conversationPolicyFor(client contract.ClientType) convo.Policy {
	policy := conversationPolicy
	policy.NotesFollowPrompt = client == contract.ClientDeepSeekHarness
	switch client {
	case contract.ClientClaudeCode, contract.ClientCodex, contract.ClientOpenCode, contract.ClientDeepSeekHarness:
		policy.ToolFreeHelpers = true
	}
	return policy
}

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

// sessionFingerprints lazily derives and caches the fingerprint keys from the
// audit master key: the session key convo links calls with, and the key of
// the link fingerprints that tie a subagent or a title request to its
// conversation. The cache only fills on success so a transient storage error
// on the first request does not disable fingerprints for the process.
type sessionFingerprints struct {
	mu          sync.Mutex
	fingerprint *convo.Fingerprinter
	link        *convo.Fingerprinter
}

func (cache *sessionFingerprints) get(ctx context.Context, blobs AuditBlobPersister, logf func(string, ...any)) (session, link *convo.Fingerprinter) {
	if cache == nil || blobs == nil {
		return nil, nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.fingerprint != nil {
		return cache.fingerprint, cache.link
	}
	master, err := blobs.GetOrCreateAuditKey(ctx)
	if err != nil {
		if logf != nil {
			logf("session fingerprint key unavailable: %v", err)
		}
		return nil, nil
	}
	cache.fingerprint = convo.NewFingerprinter(convo.DeriveFingerprintKey(master, sessionFingerprintInfo))
	cache.link = convo.NewFingerprinter(convo.DeriveFingerprintKey(master, linkFingerprintInfo))
	return cache.fingerprint, cache.link
}

// sessionLookup adapts the record store to convo.Lookup, binding the
// principal to the local access token that authenticated this request.
// learned, when not nil, learns each matched record's status and parent link
// under matchKey, since convo.Match has no room for them.
func sessionLookup(
	store RequestRecordStore,
	accessTokenID *contract.AccessTokenID,
	learned map[string]matchFacts,
) convo.Lookup {
	if store == nil {
		return nil
	}
	return func(ctx context.Context, kind convo.Kind, values []string, scope convo.Scope) (convo.Match, bool, error) {
		cursorScope := storage.SessionCursorScope{
			SamePrincipal:      scope.SamePrincipal,
			LocalAccessTokenID: accessTokenID,
			NotBefore:          scope.NotBefore,
		}
		if scope.Continues != nil {
			for _, point := range scope.Continues.Points {
				cursorScope.Continues = append(cursorScope.Continues, storage.SessionTurnPoint{
					UserMessages: point.UserMessages, Fingerprint: point.LastUserFingerprint,
				})
			}
		}
		match, ok, err := store.FindSessionLink(ctx, contract.SessionCursorKind(kind), values, cursorScope)
		if err != nil || !ok {
			return convo.Match{}, false, err
		}
		result := convo.Match{SessionID: string(match.SessionID), Kind: kind, Value: match.Value}
		if learned != nil {
			learned[matchKey(result)] = matchFacts{status: match.Status, parent: match.ParentSessionID}
		}
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

// matchFacts is what a lookup learned about a matched record beyond
// convo.Match.
type matchFacts struct {
	status contract.RequestStatus
	parent *contract.SessionID
}

// matchKey names a match by the cursor that found it.
func matchKey(match convo.Match) string {
	return string(match.Kind) + "\x00" + match.Value
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
