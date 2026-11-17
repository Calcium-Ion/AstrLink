package core

import "fmt"

// lifecycle is the part of Core's state every process event may change.
// Events carry the generation of the start they belong to, so an event from
// an earlier Core never changes a later one.
type lifecycle struct {
	generation uint64
	phase      Phase
	pid        int // 0 when there is no process
	lastError  string
}

type lifecycleEventKind int

const (
	eventStopRequested lifecycleEventKind = iota
	eventStopRequestFailed
	eventFailureStopRequested
	eventFailureStopRequestFailed
	eventFailureWithoutChild
	eventProcessErrorWhileStopping
	eventStopWaitTimedOut
	eventTerminated
)

type lifecycleEvent struct {
	kind lifecycleEventKind
	// message is the failure, the kill error of a failed stop request, or
	// the termination, as the kind says.
	message   string
	killError string
}

func transitionLifecycle(current lifecycle, eventGeneration uint64, event lifecycleEvent) lifecycle {
	if current.generation != eventGeneration {
		return current
	}
	next := current
	switch event.kind {
	case eventStopRequested:
		next.phase = PhaseStopping
		next.lastError = ""
	case eventStopRequestFailed:
		next.phase = PhaseError
		next.lastError = manualTerminationMessage("unable to send astrlink-core termination request", event.message, next.pid)
	case eventFailureStopRequested:
		next.phase = PhaseError
		next.pid = 0
		next.lastError = event.message + "; astrlink-core termination request succeeded"
	case eventFailureStopRequestFailed:
		next.phase = PhaseError
		next.lastError = manualTerminationMessage(event.message, event.killError, next.pid)
	case eventFailureWithoutChild:
		next.phase = PhaseError
		if next.pid != 0 {
			next.lastError = fmt.Sprintf("%s; process handle is unavailable; manually terminate PID %d before retrying", event.message, next.pid)
		} else {
			next.lastError = event.message
		}
	case eventProcessErrorWhileStopping:
		if current.phase == PhaseStopping {
			next.phase = PhaseStopped
			next.pid = 0
			next.lastError = ""
		}
	case eventStopWaitTimedOut:
		if current.phase == PhaseStopping {
			next.phase = PhaseError
			if next.pid != 0 {
				next.lastError = fmt.Sprintf("timed out waiting for astrlink-core to terminate; manually verify or terminate PID %d before retrying", next.pid)
			} else {
				next.lastError = "timed out waiting for astrlink-core to terminate"
			}
		}
	case eventTerminated:
		next.pid = 0
		if current.phase == PhaseStopping {
			next.phase = PhaseStopped
			next.lastError = ""
		} else {
			next.phase = PhaseExited
			if current.lastError != "" {
				next.lastError = current.lastError + "; " + event.message
			} else {
				next.lastError = event.message
			}
		}
	}
	return next
}

func manualTerminationMessage(prefix, err string, pid int) string {
	if pid != 0 {
		return fmt.Sprintf("%s: %s; manually terminate PID %d before retrying", prefix, err, pid)
	}
	return prefix + ": " + err
}

func startAllowed(state lifecycle, hasChild bool) bool {
	return !hasChild && state.pid == 0 &&
		(state.phase == PhaseStopped || state.phase == PhaseExited || state.phase == PhaseError)
}
