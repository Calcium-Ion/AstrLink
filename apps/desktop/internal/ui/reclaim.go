package ui

import (
	"sync"
	"time"
)

// A hidden window keeps its web view. On Windows the WebView2 processes
// behind it hold 100–200 MB for as long as AstrLink sits in the tray, so a
// window left hidden for hiddenLifetime is destroyed, and showing it again
// builds a new one.
//
// A show that arrives while the window is being destroyed or built is
// recorded, and the window comes up once that ends.

// hiddenLifetime is long enough that another look from the tray, or a
// window closed by mistake, comes back as it was left.
const hiddenLifetime = 5 * time.Minute

// Show is what showing a window takes.
type Show int

const (
	// ShowExisting: the window exists; show it.
	ShowExisting Show = iota
	// ShowBuild: there is no window. Build one, then report it with Built.
	ShowBuild
	// ShowPending: a destroy or build under way brings the window up when
	// it ends.
	ShowPending
)

type reclaimPhaseKind int

const (
	// phaseHidden: the timer armed with generation destroys it.
	phaseHidden reclaimPhaseKind = iota
	// phaseDestroying: destroy sent; reopen records a show that arrived
	// since.
	phaseDestroying
	// phaseBuilding: a new window is being built.
	phaseBuilding
)

type reclaimPhase struct {
	kind       reclaimPhaseKind
	generation uint64
	reopen     bool
}

type expiry int

const (
	// expiryStale: shown, destroyed or hidden again since the timer was
	// armed.
	expiryStale expiry = iota
	// expiryHeld: the window asked to be kept; look again later.
	expiryHeld
	expiryDestroy
)

// phases is the reclaim state of every window, by label.
type phases struct {
	phases     map[string]reclaimPhase
	held       map[string]bool
	generation uint64
}

func newPhases() phases {
	return phases{phases: map[string]reclaimPhase{}, held: map[string]bool{}}
}

func (p *phases) hidden(label string) uint64 {
	p.generation++
	p.phases[label] = reclaimPhase{kind: phaseHidden, generation: p.generation}
	return p.generation
}

func (p *phases) show(label string, exists bool) Show {
	phase, tracked := p.phases[label]
	switch {
	case tracked && phase.kind == phaseDestroying:
		phase.reopen = true
		p.phases[label] = phase
		return ShowPending
	case tracked && phase.kind == phaseBuilding:
		return ShowPending
	case exists:
		delete(p.phases, label)
		return ShowExisting
	default:
		p.phases[label] = reclaimPhase{kind: phaseBuilding}
		return ShowBuild
	}
}

func (p *phases) built(label string) {
	if phase, ok := p.phases[label]; ok && phase.kind == phaseBuilding {
		delete(p.phases, label)
	}
}

func (p *phases) expire(label string, generation uint64) expiry {
	phase, ok := p.phases[label]
	if !ok || phase.kind != phaseHidden || phase.generation != generation {
		return expiryStale
	}
	if p.held[label] {
		return expiryHeld
	}
	p.phases[label] = reclaimPhase{kind: phaseDestroying}
	return expiryDestroy
}

func (p *phases) destroyed(label string) bool {
	phase, ok := p.phases[label]
	if !ok {
		return false
	}
	switch phase.kind {
	case phaseDestroying:
		delete(p.phases, label)
		return phase.reopen
	case phaseHidden:
		delete(p.phases, label)
	}
	return false
}

func (p *phases) hold(label string, held bool) {
	if held {
		p.held[label] = true
	} else {
		delete(p.held, label)
	}
}

// ReclaimedWindow is what Reclaim needs of a window to release it.
type ReclaimedWindow interface {
	IsVisible() bool
	Destroy()
}

// Reclaim releases windows left hidden. Lookup finds a window by label, nil
// when there is none.
type Reclaim struct {
	Lookup func(label string) ReclaimedWindow
	// after runs fn once d passed (time.AfterFunc); tests replace it.
	after func(d time.Duration, fn func())

	mu     sync.Mutex
	phases phases
}

// NewReclaim returns a Reclaim that finds windows with lookup.
func NewReclaim(lookup func(label string) ReclaimedWindow) *Reclaim {
	return &Reclaim{
		Lookup: lookup,
		after:  func(d time.Duration, fn func()) { time.AfterFunc(d, fn) },
		phases: newPhases(),
	}
}

// Hidden starts the countdown for a window that was just hidden.
func (r *Reclaim) Hidden(label string) {
	r.mu.Lock()
	generation := r.phases.hidden(label)
	r.mu.Unlock()
	r.arm(label, generation)
}

func (r *Reclaim) arm(label string, generation uint64) {
	r.after(hiddenLifetime, func() {
		if r.expire(label, generation) == expiryHeld {
			r.arm(label, generation)
		}
	})
}

// Show tells how to bring label up. The caller acts on the answer;
// ShowBuild must be followed by Built.
func (r *Reclaim) Show(label string) Show {
	exists := r.Lookup(label) != nil
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.phases.show(label, exists)
}

// Built ends a build Show asked for, whether or not it succeeded.
func (r *Reclaim) Built(label string) {
	r.mu.Lock()
	r.phases.built(label)
	r.mu.Unlock()
}

// Destroyed records that label is gone. It reports whether a show arrived
// while it was being destroyed; the caller then shows it again.
func (r *Reclaim) Destroyed(label string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.phases.destroyed(label)
}

// Hold keeps label while it holds something it cannot rebuild, such as
// unsaved edits.
func (r *Reclaim) Hold(label string, held bool) {
	r.mu.Lock()
	r.phases.hold(label, held)
	r.mu.Unlock()
}

func (r *Reclaim) expire(label string, generation uint64) expiry {
	w := r.Lookup(label)
	if w == nil {
		return expiryStale
	}
	// A window brought back some other way stays.
	if w.IsVisible() {
		return expiryStale
	}
	r.mu.Lock()
	e := r.phases.expire(label, generation)
	r.mu.Unlock()
	if e == expiryDestroy {
		w.Destroy()
	}
	return e
}
