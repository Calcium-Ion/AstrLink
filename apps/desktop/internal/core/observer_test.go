package core

import (
	"testing"
	"time"
)

func TestObserverStateReadsAgeLevelAndPendingApprovals(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 5, 0, time.UTC)
	state, err := parseObserverState([]byte(`{"last_seen_at":"2026-09-28T10:00:00Z","read_level":"raw","pending_raw_access":3,"active_raw_grants":1}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if state.age == nil || *state.age != 5*time.Second || state.readLevel != ReadRaw ||
		state.pendingRawAccess != 3 || state.activeRawGrants != 1 || state.rawKeyEvent != nil {
		t.Fatalf("parsed %+v", state)
	}

	// The setup gate and the latest raw password or key change, whoever
	// made it; agent steps after it do not hide it.
	state, err = parseObserverState([]byte(`{"raw_password_required":true,"raw_access_events":[
		{"at":"2026-09-28T09:00:00Z","kind":"raw_password_set","client_name":""},
		{"at":"2026-09-28T09:30:00Z","kind":"raw_key_reset","client_name":""},
		{"at":"2026-09-28T09:40:00Z","kind":"requested","client_name":"astrlink-cli"},
		{"at":"not a time","kind":"raw_password_changed","client_name":""}]}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if !state.rawPasswordRequired || state.rawKeyEvent == nil ||
		*state.rawKeyEvent != (RawKeyEvent{Kind: RawKeyReset, At: "2026-09-28T09:30:00Z"}) {
		t.Fatalf("parsed %+v", state)
	}

	// An unknown level and a clock ahead of the desktop.
	state, err = parseObserverState([]byte(`{"last_seen_at":"2026-09-28T10:00:09Z","read_level":"future"}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if state.age == nil || *state.age != 0 || state.readLevel != "" || state.pendingRawAccess != 0 {
		t.Fatalf("parsed %+v", state)
	}
	if state, err := parseObserverState([]byte(`{"read_level":"","pending_raw_access":-1}`), now); err != nil || state != (observerState{}) {
		t.Fatalf("parsed %+v, %v", state, err)
	}
	if _, err := parseObserverState([]byte("not json"), now); err == nil {
		t.Fatal("invalid JSON should fail")
	}
}

func TestObserverFieldsLeaveTheViewWithTheReadyPhase(t *testing.T) {
	now := time.Now()
	s := defaultState()
	s.phase = PhaseReady
	s.observerSeenAt = now
	s.observerReadLevel = ReadShareable
	s.pendingRawAccess = 2
	s.activeRawGrants = 1
	s.rawPasswordRequired = true
	s.rawKeyEvent = &RawKeyEvent{Kind: RawPasswordChanged, At: "2026-09-28T09:00:00Z"}
	s.rawKeyReplaced = true
	view := s.view(now)
	if !view.ObserverActive || !view.RawPasswordRequired || view.RawKeyEvent == nil || !view.RawKeyReplaced ||
		view.ObserverReadLevel == nil || *view.ObserverReadLevel != ReadShareable || view.PendingRawAccess != 2 || view.ActiveRawGrants != 1 {
		t.Fatalf("ready view %+v", view)
	}
	if view := s.view(now.Add(ObserverActiveWindow)); view.ObserverActive || view.ObserverReadLevel != nil || view.PendingRawAccess != 2 {
		t.Fatalf("a read older than the active window still shows: %+v", view)
	}
	s.phase = PhaseStopped
	if view := s.view(now); view.PendingRawAccess != 0 || view.ActiveRawGrants != 0 || view.RawPasswordRequired || view.RawKeyEvent != nil || view.RawKeyReplaced {
		t.Fatalf("stopped view %+v", view)
	}
	s.clearHandshake()
	if s.rawPasswordRequired || s.rawKeyEvent != nil || s.rawKeyReplaced {
		t.Fatalf("handshake state survived: %+v", s)
	}
}
