package core

import (
	"errors"
	"sync"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
)

// serviceTestKey scopes one provider test to the window, service and test
// ID that registered it, so a sibling window cannot cancel another
// service's test by guessing an ID.
type serviceTestKey struct {
	owner, serviceID, testID string
}

type serviceTestEntry struct {
	generation uint64
	cancel     func()
}

type serviceTests struct {
	mu       sync.Mutex
	entries  map[serviceTestKey]serviceTestEntry
	sequence uint64
}

// RegisterServiceTest registers the cancel function of one in-flight
// provider test. A duplicate key is refused rather than replacing another
// test. The returned release removes the registration once the test ends.
func (m *Manager) RegisterServiceTest(owner, serviceID, testID string, cancel func()) (release func(), err error) {
	t := &m.tests
	key := serviceTestKey{owner: owner, serviceID: serviceID, testID: testID}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.entries[key]; ok {
		return nil, errors.New(control.ServiceTestAlreadyRunning)
	}
	if t.entries == nil {
		t.entries = map[serviceTestKey]serviceTestEntry{}
	}
	t.sequence++
	generation := t.sequence
	t.entries[key] = serviceTestEntry{generation: generation, cancel: cancel}
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		// An older registration never removes a newer test that reused the
		// key after a cancellation.
		if entry, ok := t.entries[key]; ok && entry.generation == generation {
			delete(t.entries, key)
		}
	}, nil
}

// CancelServiceTest signals an in-flight provider test to stop and reports
// whether one was registered under this owner, service and ID.
func (m *Manager) CancelServiceTest(owner, serviceID, testID string) bool {
	t := &m.tests
	key := serviceTestKey{owner: owner, serviceID: serviceID, testID: testID}
	t.mu.Lock()
	entry, ok := t.entries[key]
	delete(t.entries, key)
	t.mu.Unlock()
	if ok {
		entry.cancel()
	}
	return ok
}
