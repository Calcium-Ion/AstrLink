package ingress

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	codexTurnMetadataHeader = "X-Codex-Turn-Metadata"
	codexImageTurnHeader    = "X-Codex-Image-Turn-Id"
)

// codexTurnRef names one Codex turn. Every Responses request of the turn,
// over HTTP or WebSocket, carries both IDs in client_metadata. An image
// request names only the turn, and a search request names both.
type codexTurnRef struct {
	turnID    string
	sessionID string
}

func (ref codexTurnRef) empty() bool { return ref.turnID == "" && ref.sessionID == "" }

// or fills the IDs ref lacks from fallback.
func (ref codexTurnRef) or(fallback codexTurnRef) codexTurnRef {
	if ref.turnID == "" {
		ref.turnID = fallback.turnID
	}
	if ref.sessionID == "" {
		ref.sessionID = fallback.sessionID
	}
	return ref
}

// codexTurnID keeps an ID only when it looks like one; it never reaches an
// upstream, but it is a map key.
func codexTurnID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if r <= 0x20 || r > 0x7e {
			return ""
		}
	}
	return value
}

func codexTurnFromClientMetadata(raw json.RawMessage) codexTurnRef {
	var metadata struct {
		TurnID    string `json:"turn_id"`
		SessionID string `json:"session_id"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &metadata) != nil {
		return codexTurnRef{}
	}
	return codexTurnRef{turnID: codexTurnID(metadata.TurnID), sessionID: codexTurnID(metadata.SessionID)}
}

// codexTurnFromHeader reads the turn metadata header Codex sends with HTTP
// Responses requests and with its search requests.
func codexTurnFromHeader(header http.Header) codexTurnRef {
	ref := codexTurnFromClientMetadata(json.RawMessage(header.Get(codexTurnMetadataHeader)))
	if ref.sessionID == "" {
		ref.sessionID = codexTurnID(header.Get("Session-Id"))
	}
	return ref
}

// codexTurnStore remembers which provider served each Codex turn and session,
// so the image and search requests Codex sends for that turn reach the same
// provider and account. It holds IDs only, keyed by access token, in memory.
type codexTurnStore struct {
	mu      sync.Mutex
	entries map[codexTurnKey]codexTurnBinding
}

type codexTurnKey struct{ principal, turnID, sessionID string }

type codexTurnBinding struct {
	service contract.ServiceID
	// model is the upstream model that served the turn.
	model string
	// official is set when the turn was forwarded with the Codex client's
	// own identity.
	official bool
	at       time.Time
}

const (
	codexTurnTTL   = 6 * time.Hour
	codexTurnLimit = 4096
)

func (store *codexTurnStore) note(principal string, ref codexTurnRef, binding codexTurnBinding) {
	if ref.empty() || binding.service == "" {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.entries == nil {
		store.entries = map[codexTurnKey]codexTurnBinding{}
	}
	if len(store.entries) >= codexTurnLimit {
		store.evict(binding.at)
	}
	if ref.turnID != "" {
		store.entries[codexTurnKey{principal: principal, turnID: ref.turnID}] = binding
	}
	if ref.sessionID != "" {
		store.entries[codexTurnKey{principal: principal, sessionID: ref.sessionID}] = binding
	}
}

// lookup prefers the turn, then the session's latest turn.
func (store *codexTurnStore) lookup(principal string, ref codexTurnRef, now time.Time) (codexTurnBinding, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, key := range []codexTurnKey{
		{principal: principal, turnID: ref.turnID},
		{principal: principal, sessionID: ref.sessionID},
	} {
		if key.turnID == "" && key.sessionID == "" {
			continue
		}
		if binding, ok := store.entries[key]; ok && now.Sub(binding.at) < codexTurnTTL {
			return binding, true
		}
	}
	return codexTurnBinding{}, false
}

// evict drops expired entries, then the oldest quarter if the store is still
// full. The caller holds store.mu.
func (store *codexTurnStore) evict(now time.Time) {
	keys := make([]codexTurnKey, 0, len(store.entries))
	for key, binding := range store.entries {
		if now.Sub(binding.at) >= codexTurnTTL {
			delete(store.entries, key)
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) < codexTurnLimit {
		return
	}
	slices.SortFunc(keys, func(left, right codexTurnKey) int {
		return store.entries[left].at.Compare(store.entries[right].at)
	})
	for _, key := range keys[:len(keys)/4] {
		delete(store.entries, key)
	}
}

// noteCodexTurn records the provider that served a Codex Responses request.
func (handler *Handler) noteCodexTurn(classified Request, session *recordSession) {
	if session == nil || classified.Protocol != contract.ProtocolOpenAIResponses || classified.codexTurn.empty() ||
		session.status != contract.RequestStatusSucceeded || session.endpointID == nil {
		return
	}
	principal := ""
	if session.accessTokenID != nil {
		principal = string(*session.accessTokenID)
	}
	model := classified.routingModel()
	if session.recovery != nil && session.recovery.UpstreamModel != "" {
		model = session.recovery.UpstreamModel
	}
	handler.codexTurns.note(principal, classified.codexTurn, codexTurnBinding{
		service: *session.endpointID, model: model, official: session.codexOfficial, at: time.Now(),
	})
}
