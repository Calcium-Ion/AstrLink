package pluginhost

import (
	"container/list"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// A session's first turns often come before it has a project: the one the
// plugin asks in tells the most about it. Their captures are held until the
// session has a value of each of the plugin's attributes, then run with it,
// the turn's text as it passed the privacy check then. A session left out
// drops them. They live in memory only, and are few: the first turns of the
// sessions held most recently, within a size.
const (
	maxHeldTurns    = 4
	maxHeldBytes    = 64 << 10
	maxHeldSessions = 64
)

type heldEntry struct {
	key   lookupKey
	turns []TurnView
	bytes int
}

// heldCaptures keeps the held turns by session and plugin.
type heldCaptures struct {
	mutex sync.Mutex
	order *list.List
	byKey map[lookupKey]*list.Element
}

func newHeldCaptures() *heldCaptures {
	return &heldCaptures{order: list.New(), byKey: map[lookupKey]*list.Element{}}
}

// heldSize is what holding a turn costs: the text it carries.
func heldSize(view TurnView) int {
	return len(view.User.Text) + len(view.User.Typed) + len(view.User.Previous)
}

// hold keeps a turn of the session for the plugin, and reports whether it
// did: the first maxHeldTurns turns within maxHeldBytes are kept, a turn
// sent again replacing itself.
func (held *heldCaptures) hold(key lookupKey, view TurnView) bool {
	size := heldSize(view)
	held.mutex.Lock()
	defer held.mutex.Unlock()
	element, found := held.byKey[key]
	if !found {
		if size > maxHeldBytes {
			return false
		}
		element = held.order.PushFront(&heldEntry{key: key})
		held.byKey[key] = element
		for held.order.Len() > maxHeldSessions {
			oldest := held.order.Back()
			held.order.Remove(oldest)
			delete(held.byKey, oldest.Value.(*heldEntry).key)
		}
	}
	held.order.MoveToFront(element)
	entry := element.Value.(*heldEntry)
	for index, kept := range entry.turns {
		if kept.Session.Turn == view.Session.Turn {
			if entry.bytes-heldSize(kept)+size > maxHeldBytes {
				return false
			}
			entry.bytes += size - heldSize(kept)
			entry.turns[index] = view
			return true
		}
	}
	if len(entry.turns) >= maxHeldTurns || entry.bytes+size > maxHeldBytes {
		return false
	}
	entry.turns = append(entry.turns, view)
	entry.bytes += size
	return true
}

// take returns the turns held for the plugin in the session, oldest first,
// and forgets them.
func (held *heldCaptures) take(key lookupKey) []TurnView {
	held.mutex.Lock()
	defer held.mutex.Unlock()
	element, found := held.byKey[key]
	if !found {
		return nil
	}
	held.order.Remove(element)
	delete(held.byKey, key)
	return element.Value.(*heldEntry).turns
}

// release runs the captures held for the plugin in the session once meta
// has a value of each of its attributes, with those values; a session left
// out drops them. They go to the queue before the request's own capture, a
// full queue counting what it drops, and their records stay with the
// request.
func (run *Run) release(current *part, meta map[string]plugin.MetaValue) {
	key := lookupKey{run.input.Session, current.plugin.Key}
	for _, attribute := range current.plugin.Meta.Metadata {
		value, found := meta[attribute.Key]
		if !found {
			return
		}
		if value.Value == contract.MetadataNone {
			run.host.held.take(key)
			return
		}
	}
	for _, view := range run.host.held.take(key) {
		view.Meta, view.Late = meta, true
		info := current.info
		ref := view.Session
		info.Meta, info.Session = meta, &ref
		run.host.observe.push(observeJob{plugin: current.plugin, view: view, info: info, deliver: run.deliverObserve})
	}
}
