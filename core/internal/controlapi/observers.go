package controlapi

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

const ObserversPath = "/control/v1/observers"

// Requests arriving on the local control socket, or announcing themselves as
// the agent CLI, come from an agent reading the operator's records rather
// than from the desktop shell. The desktop surfaces that as "being watched".
const observerUserAgentPrefix = "astrlink-cli"

// ReadLevel is how much of the operator's audit an agent-side read reached.
type ReadLevel string

const (
	// ReadLevelMetadata covers records, settings, and every non-audit read.
	ReadLevelMetadata ReadLevel = "metadata"
	// ReadLevelShareable is a read of shareable audit content.
	ReadLevelShareable ReadLevel = "shareable"
	// ReadLevelRaw is a read of raw audit content through an approved grant.
	ReadLevelRaw ReadLevel = "raw"
)

// RawAccessEventKind names one step of an agent's raw access request.
type RawAccessEventKind string

const (
	RawAccessEventRequested       RawAccessEventKind = "requested"
	RawAccessEventApproved        RawAccessEventKind = "approved"
	RawAccessEventDenied          RawAccessEventKind = "denied"
	RawAccessEventPasswordInvalid RawAccessEventKind = "password_invalid"
	RawAccessEventRawRead         RawAccessEventKind = "raw_read"
	RawAccessEventRevoked         RawAccessEventKind = "revoked"
	// The raw password and key events record who changed what guards raw
	// content, so a change the operator did not make is visible.
	RawAccessEventPasswordSet     RawAccessEventKind = "raw_password_set"
	RawAccessEventPasswordChanged RawAccessEventKind = "raw_password_changed"
	RawAccessEventKeyReset        RawAccessEventKind = "raw_key_reset"
)

// maxRawAccessEvents bounds the in-memory raw access log.
const maxRawAccessEvents = 64

// RawAccessEvent is one entry of the observer log for raw access. It
// carries identifiers only, never content or proof.
type RawAccessEvent struct {
	At   time.Time          `json:"at"`
	Kind RawAccessEventKind `json:"kind"`
	// GrantID and RequestID are empty for a wrong password on the
	// desktop's own unlock or password change.
	GrantID    string `json:"grant_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	ClientName string `json:"client_name"`
	Decision   string `json:"decision,omitempty"`
}

// observerTracker remembers the most recent agent-side control request.
type observerTracker struct {
	mu          sync.Mutex
	lastSeen    time.Time
	client      string
	requests    uint64
	readLevel   ReadLevel
	lastRawRead time.Time
	events      []RawAccessEvent
	now         func() time.Time
	// pending counts raw access requests awaiting a decision.
	pending func() int
	// active counts lasting raw grants still running.
	active func() int
	// passwordRequired reports whether raw capture waits for a raw password.
	passwordRequired func(context.Context) bool
}

func newObserverTracker() *observerTracker {
	return &observerTracker{now: time.Now}
}

// classify names the agent-side client behind a request, or "" for the
// desktop shell and other first-party callers.
func classifyObserver(request *http.Request) string {
	if request == nil {
		return ""
	}
	if agent := strings.TrimSpace(request.UserAgent()); strings.HasPrefix(agent, observerUserAgentPrefix) {
		return observerUserAgentPrefix
	}
	if LocalSocketAuthenticated(request) {
		return observerUserAgentPrefix
	}
	return ""
}

// note records an agent-side request. A nil tracker (bare Handler literals in
// tests) records nothing.
func (tracker *observerTracker) note(request *http.Request) {
	if tracker == nil {
		return
	}
	client := classifyObserver(request)
	if client == "" {
		return
	}
	tracker.mu.Lock()
	tracker.lastSeen = tracker.now().UTC()
	tracker.client = client
	tracker.requests++
	tracker.readLevel = ReadLevelMetadata
	tracker.mu.Unlock()
}

// noteRead raises the level of the agent-side read just recorded. A raw
// read is recorded even from a caller that does not announce itself, since
// only an agent grant reaches raw content.
func (tracker *observerTracker) noteRead(request *http.Request, level ReadLevel) {
	if tracker == nil {
		return
	}
	if level != ReadLevelRaw && classifyObserver(request) == "" {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	now := tracker.now().UTC()
	tracker.lastSeen = now
	tracker.readLevel = level
	if level == ReadLevelRaw {
		tracker.lastRawRead = now
	}
}

// noteRawEvent appends one raw access step to the bounded log.
func (tracker *observerTracker) noteRawEvent(kind RawAccessEventKind, grant RawAccessGrant) {
	if tracker == nil {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	event := RawAccessEvent{
		At: tracker.now().UTC(), Kind: kind, GrantID: grant.GrantID,
		RequestID: string(grant.RequestID), ClientName: grant.ClientName, Decision: grant.Decision,
	}
	if len(tracker.events) == maxRawAccessEvents {
		copy(tracker.events, tracker.events[1:])
		tracker.events = tracker.events[:maxRawAccessEvents-1]
	}
	tracker.events = append(tracker.events, event)
}

// ObserversResponse is the wire shape of GET /control/v1/observers.
type ObserversResponse struct {
	// LastSeenAt is the most recent agent-side request, or null before any.
	LastSeenAt *time.Time `json:"last_seen_at"`
	Client     string     `json:"client"`
	Requests   uint64     `json:"requests"`
	// ReadLevel is the level of the most recent agent-side read, or empty
	// before any.
	ReadLevel ReadLevel `json:"read_level"`
	// LastRawReadAt is the most recent raw read through a grant.
	LastRawReadAt *time.Time `json:"last_raw_read_at"`
	// PendingRawAccess counts agent raw access requests awaiting a decision.
	PendingRawAccess int `json:"pending_raw_access"`
	// ActiveRawGrants counts lasting raw grants still running.
	ActiveRawGrants int `json:"active_raw_grants"`
	// RawAccessEvents is the recent raw access log, oldest first.
	RawAccessEvents []RawAccessEvent `json:"raw_access_events"`
	// RawPasswordRequired is true while body capture is on and no raw
	// password is set, so the desktop can point at the setup from outside the
	// main window.
	RawPasswordRequired bool `json:"raw_password_required"`
}

func (tracker *observerTracker) snapshot(ctx context.Context) ObserversResponse {
	if tracker == nil {
		return ObserversResponse{RawAccessEvents: []RawAccessEvent{}}
	}
	pending, active := 0, 0
	if tracker.pending != nil {
		pending = tracker.pending()
	}
	if tracker.active != nil {
		active = tracker.active()
	}
	passwordRequired := tracker.passwordRequired != nil && tracker.passwordRequired(ctx)
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	response := ObserversResponse{
		Client: tracker.client, Requests: tracker.requests, ReadLevel: tracker.readLevel,
		PendingRawAccess:    pending,
		ActiveRawGrants:     active,
		RawAccessEvents:     append([]RawAccessEvent{}, tracker.events...),
		RawPasswordRequired: passwordRequired,
	}
	if !tracker.lastSeen.IsZero() {
		seen := tracker.lastSeen
		response.LastSeenAt = &seen
	}
	if !tracker.lastRawRead.IsZero() {
		read := tracker.lastRawRead
		response.LastRawReadAt = &read
	}
	return response
}

func (handler *Handler) getObservers(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	writeJSON(writer, http.StatusOK, handler.observers.snapshot(request.Context()))
}
