package core

import (
	"strings"
	"testing"
)

func lifecycleAt(phase Phase, pid int) lifecycle {
	return lifecycle{generation: 7, phase: phase, pid: pid}
}

func TestLifecycleTransitions(t *testing.T) {
	tests := []struct {
		name       string
		current    lifecycle
		generation uint64
		event      lifecycleEvent
		phase      Phase
		pid        int
		errorHas   string
		restart    bool
	}{
		{
			name:    "a failure whose kill succeeded is restartable",
			current: lifecycleAt(PhaseHandshaking, 42), generation: 7,
			event: lifecycleEvent{kind: eventFailureStopRequested, message: "handshake failed"},
			phase: PhaseError, errorHas: "termination request succeeded", restart: true,
		},
		{
			name:    "a failed kill keeps the PID and blocks a restart",
			current: lifecycleAt(PhaseReady, 4242), generation: 7,
			event: lifecycleEvent{kind: eventFailureStopRequestFailed, message: "control handshake failed", killError: "access denied"},
			phase: PhaseError, pid: 4242, errorHas: "PID 4242",
		},
		{
			name:    "a process error while stopping completes the stop",
			current: lifecycleAt(PhaseStopping, 52), generation: 7,
			event: lifecycleEvent{kind: eventProcessErrorWhileStopping},
			phase: PhaseStopped, restart: true,
		},
		{
			name:    "a failure without a process handle names the PID",
			current: lifecycleAt(PhaseReady, 9), generation: 7,
			event: lifecycleEvent{kind: eventFailureWithoutChild, message: "lost"},
			phase: PhaseError, pid: 9, errorHas: "manually terminate PID 9",
		},
		{
			name:    "a stop wait that times out keeps the PID",
			current: lifecycleAt(PhaseStopping, 123), generation: 7,
			event: lifecycleEvent{kind: eventStopWaitTimedOut},
			phase: PhaseError, pid: 123, errorHas: "PID 123",
		},
		{
			name:    "termination after a requested stop is a clean stop",
			current: lifecycleAt(PhaseStopping, 43), generation: 7,
			event: lifecycleEvent{kind: eventTerminated, message: "astrlink-core exited after signal 9"},
			phase: PhaseStopped, restart: true,
		},
		{
			name:    "an unrequested termination is an exit",
			current: lifecycleAt(PhaseReady, 42), generation: 7,
			event: lifecycleEvent{kind: eventTerminated, message: "astrlink-core exited with code 1"},
			phase: PhaseExited, errorHas: "code 1", restart: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := transitionLifecycle(tt.current, tt.generation, tt.event)
			if next.phase != tt.phase || next.pid != tt.pid {
				t.Fatalf("got phase %s pid %d, want %s %d", next.phase, next.pid, tt.phase, tt.pid)
			}
			if tt.errorHas == "" && next.lastError != "" {
				t.Fatalf("unexpected error %q", next.lastError)
			}
			if !strings.Contains(next.lastError, tt.errorHas) {
				t.Fatalf("error %q lacks %q", next.lastError, tt.errorHas)
			}
			if got := startAllowed(next, false); got != tt.restart {
				t.Fatalf("startAllowed = %v, want %v", got, tt.restart)
			}
		})
	}
}

func TestExpiredGenerationEventsDoNotChangeLifecycle(t *testing.T) {
	current := lifecycleAt(PhaseReady, 88)
	next := transitionLifecycle(current, 6, lifecycleEvent{kind: eventFailureStopRequested, message: "stale failure"})
	if next != current {
		t.Fatalf("got %+v, want %+v", next, current)
	}
}

func TestStartIsAllowedOnlyFromInactiveRecoverableStates(t *testing.T) {
	for _, phase := range []Phase{PhaseStopped, PhaseExited, PhaseError} {
		if !startAllowed(lifecycleAt(phase, 0), false) {
			t.Errorf("%s should allow a start", phase)
		}
	}
	for _, phase := range []Phase{PhaseSpawning, PhaseWaitingForReady, PhaseHandshaking, PhaseReady, PhaseStopping} {
		if startAllowed(lifecycleAt(phase, 0), false) {
			t.Errorf("%s should refuse a start", phase)
		}
	}
	if startAllowed(lifecycleAt(PhaseError, 44), false) {
		t.Error("a remaining PID should refuse a start")
	}
	if startAllowed(lifecycleAt(PhaseStopped, 0), true) {
		t.Error("a remaining child should refuse a start")
	}
}

// Start asks this before it resolves the keychain key, so a refused start
// never migrates local.key beside a running Core.
func TestStartRefusalNamesWhyAStartCannotProceed(t *testing.T) {
	s := defaultState()
	if err := s.startRefusal(); err != nil {
		t.Fatal(err)
	}
	s.phase = PhaseReady
	if err := s.startRefusal(); err == nil || err.Error() != "astrlink-core is already running or stopping" {
		t.Fatalf("got %v", err)
	}
	s.lastError = "previous failure"
	if err := s.startRefusal(); err == nil || err.Error() != "previous failure" {
		t.Fatalf("got %v", err)
	}
	s.phase = PhaseStopped
	s.updateInProgress = true
	if err := s.startRefusal(); err == nil || err.Error() != "Application update in progress" {
		t.Fatalf("got %v", err)
	}
}
