// Package memindex is an in-memory reference implementation of the storage a
// convo host must provide: it remembers which cursors each request stored and
// answers convo.Lookup queries. It is meant for tests, examples, and small
// single-process gateways; production hosts should back Lookup with their own
// database.
package memindex

import (
	"context"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/convo"
)

// Record is one stored request.
type Record struct {
	// SessionID is the session the request was assigned to.
	SessionID string
	// Principal scopes KindEchoID and KindFingerprint matches (API key,
	// user, tenant). Empty principals only match empty principals.
	Principal string
	// Turn is the request's turn state (Decision.Turn) when known.
	Turn *convo.TurnState
	// At is when the request started; used for Scope.NotBefore and TTL.
	At time.Time
	// Cursors are the persisted inbound explicit cursors plus every output
	// cursor of the response.
	Cursors []convo.Cursor
}

// Options tunes an Index. Zero values mean no TTL and no size bound.
type Options struct {
	TTL        time.Duration
	MaxRecords int
	// Now overrides the clock, mainly for tests.
	Now func() time.Time
}

type cursorKey struct {
	kind  convo.Kind
	value string
}

// Index is safe for concurrent use.
type Index struct {
	mu      sync.Mutex
	options Options
	records []*Record
	byKey   map[cursorKey][]*Record
}

// New returns an empty index.
func New(options Options) *Index {
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Index{options: options, byKey: make(map[cursorKey][]*Record)}
}

// Put stores a record. Records are kept in insertion order; when MaxRecords is
// exceeded the oldest are evicted first, and expired records are dropped on
// every Put.
func (index *Index) Put(record Record) {
	stored := record
	stored.Cursors = append([]convo.Cursor(nil), record.Cursors...)
	if record.Turn != nil {
		turn := *record.Turn
		stored.Turn = &turn
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	index.records = append(index.records, &stored)
	for _, cursor := range stored.Cursors {
		key := cursorKey{kind: cursor.Kind, value: cursor.Value}
		index.byKey[key] = append(index.byKey[key], &stored)
	}
	index.evictLocked()
}

// Len returns how many records are stored.
func (index *Index) Len() int {
	index.mu.Lock()
	defer index.mu.Unlock()
	return len(index.records)
}

func (index *Index) evictLocked() {
	now := index.options.Now()
	drop := 0
	for drop < len(index.records) {
		record := index.records[drop]
		expired := index.options.TTL > 0 && now.Sub(record.At) > index.options.TTL
		overflow := index.options.MaxRecords > 0 && len(index.records)-drop > index.options.MaxRecords
		if !expired && !overflow {
			break
		}
		drop++
	}
	if drop == 0 {
		return
	}
	for _, record := range index.records[:drop] {
		for _, cursor := range record.Cursors {
			key := cursorKey{kind: cursor.Kind, value: cursor.Value}
			remaining := index.byKey[key][:0]
			for _, candidate := range index.byKey[key] {
				if candidate != record {
					remaining = append(remaining, candidate)
				}
			}
			if len(remaining) == 0 {
				delete(index.byKey, key)
			} else {
				index.byKey[key] = remaining
			}
		}
	}
	index.records = append(index.records[:0], index.records[drop:]...)
}

// LookupFor binds the index to a principal and returns a convo.Lookup.
func (index *Index) LookupFor(principal string) convo.Lookup {
	return func(ctx context.Context, kind convo.Kind, values []string, scope convo.Scope) (convo.Match, bool, error) {
		return index.Lookup(ctx, principal, kind, values, scope)
	}
}

// Lookup returns the most recent record storing any of values under kind.
// Records that produced a value (DirectionOut) win over records that only
// named it (DirectionIn); the latter are consulted for KindExplicit only.
func (index *Index) Lookup(_ context.Context, principal string, kind convo.Kind, values []string, scope convo.Scope) (convo.Match, bool, error) {
	index.mu.Lock()
	defer index.mu.Unlock()
	directions := []convo.Direction{convo.DirectionOut}
	if kind == convo.KindExplicit {
		directions = append(directions, convo.DirectionIn)
	}
	for _, direction := range directions {
		var (
			best      *Record
			bestValue string
		)
		for _, value := range values {
			for _, record := range index.byKey[cursorKey{kind: kind, value: value}] {
				if scope.SamePrincipal && record.Principal != principal {
					continue
				}
				if !scope.NotBefore.IsZero() && record.At.Before(scope.NotBefore) {
					continue
				}
				if !recordHasCursor(record, kind, direction, value) {
					continue
				}
				if best == nil || record.At.After(best.At) {
					best = record
					bestValue = value
				}
			}
		}
		if best != nil {
			match := convo.Match{SessionID: best.SessionID, Kind: kind, Value: bestValue}
			if best.Turn != nil {
				turn := *best.Turn
				match.Turn = &turn
			}
			return match, true, nil
		}
	}
	return convo.Match{}, false, nil
}

func recordHasCursor(record *Record, kind convo.Kind, direction convo.Direction, value string) bool {
	for _, cursor := range record.Cursors {
		if cursor.Kind == kind && cursor.Direction == direction && cursor.Value == value {
			return true
		}
	}
	return false
}
