package core

// Phase is where Core is in its lifecycle.
type Phase string

const (
	PhaseStopped         Phase = "stopped"
	PhaseSpawning        Phase = "spawning"
	PhaseWaitingForReady Phase = "waiting_for_ready"
	PhaseHandshaking     Phase = "handshaking"
	PhaseReady           Phase = "ready"
	PhaseStopping        Phase = "stopping"
	PhaseExited          Phase = "exited"
	PhaseError           Phase = "error"
)
