package core

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"time"
)

// ObserverActiveWindow is how long after the last agent-side control
// request the gateway still counts as being read. Long enough to bridge the
// desktop's observer polls.
const ObserverActiveWindow = 8 * time.Second

// ObserverReadLevel is how far an agent-side read reached, as Core
// classifies it.
type ObserverReadLevel string

const (
	ReadMetadata  ObserverReadLevel = "metadata"
	ReadShareable ObserverReadLevel = "shareable"
	ReadRaw       ObserverReadLevel = "raw"
)

// RawKeyEventKind is a raw password or key change, as Core's observer log
// names it.
type RawKeyEventKind string

const (
	RawPasswordSet     RawKeyEventKind = "raw_password_set"
	RawPasswordChanged RawKeyEventKind = "raw_password_changed"
	RawKeyReset        RawKeyEventKind = "raw_key_reset"
)

// RawKeyEvent is the latest raw password or key change this Core recorded,
// whoever made it.
type RawKeyEvent struct {
	Kind RawKeyEventKind `json:"kind"`
	// At is Core's RFC 3339 timestamp, rendered by the popover in local
	// time.
	At string `json:"at"`
}

// observerState is the part of /control/v1/observers the desktop renders.
type observerState struct {
	// age is how long ago an agent last read; nil before any read.
	age                 *time.Duration
	readLevel           ObserverReadLevel
	pendingRawAccess    uint32
	activeRawGrants     uint32
	rawPasswordRequired bool
	rawKeyEvent         *RawKeyEvent
}

func parseObserverState(body []byte, now time.Time) (observerState, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(body, &value); err != nil {
		return observerState{}, fmt.Errorf("observer state returned invalid JSON: %w", err)
	}
	var state observerState
	var seen string
	if json.Unmarshal(value["last_seen_at"], &seen) == nil {
		if at, err := time.Parse(time.RFC3339Nano, seen); err == nil {
			age := max(now.Sub(at), 0)
			state.age = &age
		}
	}
	var level string
	if json.Unmarshal(value["read_level"], &level) == nil {
		switch ObserverReadLevel(level) {
		case ReadMetadata, ReadShareable, ReadRaw:
			state.readLevel = ObserverReadLevel(level)
		}
	}
	state.pendingRawAccess = count(value["pending_raw_access"])
	state.activeRawGrants = count(value["active_raw_grants"])
	_ = json.Unmarshal(value["raw_password_required"], &state.rawPasswordRequired)
	var events []json.RawMessage
	if json.Unmarshal(value["raw_access_events"], &events) == nil {
		for _, event := range slices.Backward(events) {
			if parsed := rawKeyEvent(event); parsed != nil {
				state.rawKeyEvent = parsed
				break
			}
		}
	}
	return state, nil
}

// count reads a non-negative JSON integer, saturating at the uint32 range
// and reading anything else as zero.
func count(raw json.RawMessage) uint32 {
	var n uint64
	if json.Unmarshal(raw, &n) != nil {
		return 0
	}
	return uint32(min(n, math.MaxUint32))
}

func rawKeyEvent(raw json.RawMessage) *RawKeyEvent {
	var event struct {
		Kind *string `json:"kind"`
		At   *string `json:"at"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Kind == nil || event.At == nil {
		return nil
	}
	switch kind := RawKeyEventKind(*event.Kind); kind {
	case RawPasswordSet, RawPasswordChanged, RawKeyReset:
		if _, err := time.Parse(time.RFC3339Nano, *event.At); err != nil {
			return nil
		}
		return &RawKeyEvent{Kind: kind, At: *event.At}
	}
	return nil
}
