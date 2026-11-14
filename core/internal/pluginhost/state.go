package pluginhost

import (
	"container/list"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// maxSessions bounds the sessions whose plugin state the host keeps. A
// session it dropped starts over: its resident block is computed again and
// its next call settles its turn again.
const maxSessions = 1024

// maxThreads bounds the threads of one session whose first message the host
// tells apart: a client's subagents share the session of the conversation
// that started them, each with a system prompt and a first message of its
// own.
const maxThreads = 16

// pluginState is what the host keeps for one plugin in one session.
type pluginState struct {
	// loaded is set once the plugin's part in the session was read from
	// storage: the turn it first took part in, 0 for none yet, and whether
	// the user paused it there.
	loaded    bool
	firstTurn int
	paused    bool

	// residentKey names what the resident block was computed from, and
	// metaKey the metadata part of it, which tells a new binding from new
	// settings.
	residentKey string
	metaKey     string
	computed    bool
	resident    string
	// record is what the plugin said when it computed the block, and
	// fresh is set until a request carried the block and reported it.
	record  residentRecord
	fresh   bool
	pending *hookCall
	// pendingKey is the key the pending call computes for, and
	// pendingTrigger why it runs.
	pendingKey     string
	pendingTrigger string

	// refresh is a refresh the user asked for, which the session's next
	// request starts; checkedAt is when the block was last computed or
	// checked for newer data, and compactedAt when a compacted history last
	// refreshed it.
	refresh     string
	checkedAt   time.Time
	compactedAt time.Time

	// choices are the values the plugin offered the session, by attribute
	// key, for the user's answer to name one.
	choices map[string][]string
	// usage is the usage of the plugin's lookups the session's last
	// request carried.
	usage string

	// settled names the turn whose block is settled, "" for none;
	// turnTitle is the title of the record that settled it, and turnBytes
	// the size of its block, which the budget counts on each call of the
	// turn.
	settled   string
	turnTitle string
	turnBytes int
	// bindingTurn names the turn a model marker bound the session in: its
	// later calls settle the turn again but capture nothing.
	bindingTurn string
}

// residentRecord is how a resident block came about.
type residentRecord struct {
	trigger  string
	at       time.Time
	language string
	// given is the record the plugin returned, when it returned one.
	given    contract.PluginRecord
	hasGiven bool
	info     callSummary
	dropped  int
}

// callSummary is what the host observed of a hook call.
type callSummary struct {
	elapsedMs int
	network   contract.PluginNetworkCount
}

type sessionState struct {
	id string
	// language is the interface language the session took when it started
	// in this process; what plugins inject keeps to it.
	language string
	// threads maps the fingerprint of each thread's system prompt to that
	// of its first message, to tell a compacted history from a thread
	// switch; order lists the system prompts oldest first.
	threads map[string]string
	order   []string
	// workspace is the workspace the session's last request named, read
	// for the system prompt and opening in workspaceFor.
	workspace    workspace
	workspaceFor string
	plugins      map[string]*pluginState
}

// sessions keeps the state of the sessions used most recently. Callers hold
// mutex while they read or change a state.
type sessions struct {
	mutex sync.Mutex
	order *list.List
	byID  map[string]*list.Element
	max   int
}

func newSessions() *sessions {
	return &sessions{order: list.New(), byID: map[string]*list.Element{}, max: maxSessions}
}

// session returns the state of session, creating it. The caller holds mutex.
func (cache *sessions) session(session string) *sessionState {
	if element, found := cache.byID[session]; found {
		cache.order.MoveToFront(element)
		return element.Value.(*sessionState)
	}
	state := &sessionState{id: session, threads: map[string]string{}, plugins: map[string]*pluginState{}}
	cache.byID[session] = cache.order.PushFront(state)
	for cache.order.Len() > cache.max {
		oldest := cache.order.Back()
		cache.order.Remove(oldest)
		delete(cache.byID, oldest.Value.(*sessionState).id)
	}
	return state
}

// lookup returns the state of session without creating or touching it. The
// caller holds mutex.
func (cache *sessions) lookup(session string) (*sessionState, bool) {
	element, found := cache.byID[session]
	if !found {
		return nil, false
	}
	return element.Value.(*sessionState), true
}

// plugin returns the state of plugin in session, creating it. The caller
// holds mutex.
func (cache *sessions) plugin(session, plugin string) *pluginState {
	state := cache.session(session)
	entry, found := state.plugins[plugin]
	if !found {
		entry = &pluginState{}
		state.plugins[plugin] = entry
	}
	return entry
}

// compacted records the first message of a thread of the session and
// reports whether it replaced the one the thread had: the client compacted
// its history, or the user went back to the start of it. The caller holds
// mutex.
func (state *sessionState) compacted(system, first string) bool {
	if first == "" {
		return false
	}
	previous, known := state.threads[system]
	if !known {
		state.order = append(state.order, system)
		for len(state.order) > maxThreads {
			delete(state.threads, state.order[0])
			state.order = state.order[1:]
		}
	}
	state.threads[system] = first
	return known && previous != first
}
