package controlapi

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

const ObserversPath = "/control/v1/observers"

// Requests arriving on the local control socket, or announcing themselves as
// the MCP bridge, come from an agent reading the operator's records rather
// than from the desktop shell. The desktop surfaces that as "being watched".
const observerUserAgentPrefix = "astrlink-mcp"

// observerTracker remembers the most recent agent-side control request.
type observerTracker struct {
	mu       sync.Mutex
	lastSeen time.Time
	client   string
	requests uint64
	now      func() time.Time
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
	tracker.mu.Unlock()
}

// ObserversResponse is the wire shape of GET /control/v1/observers.
type ObserversResponse struct {
	// LastSeenAt is the most recent agent-side request, or null before any.
	LastSeenAt *time.Time `json:"last_seen_at"`
	Client     string     `json:"client"`
	Requests   uint64     `json:"requests"`
}

func (tracker *observerTracker) snapshot() ObserversResponse {
	if tracker == nil {
		return ObserversResponse{}
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	response := ObserversResponse{Client: tracker.client, Requests: tracker.requests}
	if !tracker.lastSeen.IsZero() {
		seen := tracker.lastSeen
		response.LastSeenAt = &seen
	}
	return response
}

func (handler *Handler) getObservers(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	writeJSON(writer, http.StatusOK, handler.observers.snapshot())
}
