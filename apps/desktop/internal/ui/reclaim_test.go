package ui

import (
	"testing"
	"time"
)

func TestAWindowShownBeforeItsTimeIsKept(t *testing.T) {
	p := newPhases()
	generation := p.hidden("main")
	if got := p.show("main", true); got != ShowExisting {
		t.Fatalf("show = %v", got)
	}
	if got := p.expire("main", generation); got != expiryStale {
		t.Fatalf("expire = %v", got)
	}
}

func TestOnlyTheLatestHideCounts(t *testing.T) {
	p := newPhases()
	first := p.hidden("main")
	p.show("main", true)
	second := p.hidden("main")
	if got := p.expire("main", first); got != expiryStale {
		t.Fatalf("first expire = %v", got)
	}
	if got := p.expire("main", second); got != expiryDestroy {
		t.Fatalf("second expire = %v", got)
	}
}

func TestAReleasedWindowIsBuiltAgain(t *testing.T) {
	p := newPhases()
	generation := p.hidden("main")
	if got := p.expire("main", generation); got != expiryDestroy {
		t.Fatalf("expire = %v", got)
	}
	if p.destroyed("main") {
		t.Fatal("a destroy nobody waited on asked to reopen")
	}
	if got := p.show("main", false); got != ShowBuild {
		t.Fatalf("show = %v", got)
	}
	// Another request while the build runs leaves it to that build.
	if got := p.show("main", false); got != ShowPending {
		t.Fatalf("second show = %v", got)
	}
	p.built("main")
	if got := p.show("main", true); got != ShowExisting {
		t.Fatalf("show after build = %v", got)
	}
}

func TestAShowDuringTheDestroyReopensOnceItIsGone(t *testing.T) {
	p := newPhases()
	generation := p.hidden("tray-popover")
	if got := p.expire("tray-popover", generation); got != expiryDestroy {
		t.Fatalf("expire = %v", got)
	}
	// The window still exists here: building now would make two.
	if got := p.show("tray-popover", true); got != ShowPending {
		t.Fatalf("show = %v", got)
	}
	if !p.destroyed("tray-popover") {
		t.Fatal("the show during the destroy was lost")
	}
	if got := p.show("tray-popover", false); got != ShowBuild {
		t.Fatalf("show after destroy = %v", got)
	}
}

func TestAHeldWindowWaitsUntilReleased(t *testing.T) {
	p := newPhases()
	p.hold("main", true)
	generation := p.hidden("main")
	if got := p.expire("main", generation); got != expiryHeld {
		t.Fatalf("expire = %v", got)
	}
	p.hold("main", false)
	if got := p.expire("main", generation); got != expiryDestroy {
		t.Fatalf("expire after release = %v", got)
	}
}

func TestWindowsAreTrackedApart(t *testing.T) {
	p := newPhases()
	main := p.hidden("main")
	p.hold("tray-popover", true)
	popover := p.hidden("tray-popover")
	if got := p.expire("main", main); got != expiryDestroy {
		t.Fatalf("main expire = %v", got)
	}
	if got := p.expire("tray-popover", popover); got != expiryHeld {
		t.Fatalf("popover expire = %v", got)
	}
	if got := p.show("tray-popover", true); got != ShowExisting {
		t.Fatalf("popover show = %v", got)
	}
	if got := p.show("main", true); got != ShowPending {
		t.Fatalf("main show = %v", got)
	}
}

func TestAWindowGoneAnotherWayIsBuiltOnTheNextShow(t *testing.T) {
	p := newPhases()
	p.hidden("main")
	if p.destroyed("main") {
		t.Fatal("a window closed while hidden asked to reopen")
	}
	if got := p.show("main", false); got != ShowBuild {
		t.Fatalf("show = %v", got)
	}
}

type fakeWindow struct {
	visible   bool
	destroyed chan struct{}
}

func (w *fakeWindow) IsVisible() bool { return w.visible }
func (w *fakeWindow) Destroy()        { close(w.destroyed) }

// TestReclaimDestroysAWindowLeftHidden runs the timers Reclaim arms by
// hand: a held window is looked at again, and destroyed once released.
func TestReclaimDestroysAWindowLeftHidden(t *testing.T) {
	w := &fakeWindow{destroyed: make(chan struct{})}
	r := NewReclaim(func(string) ReclaimedWindow { return w })
	timers := make(chan func(), 4)
	r.after = func(d time.Duration, fn func()) {
		if d != hiddenLifetime {
			t.Errorf("armed for %s", d)
		}
		timers <- fn
	}
	r.Hold("main", true)
	r.Hidden("main")
	(<-timers)()
	select {
	case <-w.destroyed:
		t.Fatal("a held window was destroyed")
	default:
	}
	r.Hold("main", false)
	// The held window armed the timer again.
	(<-timers)()
	select {
	case <-w.destroyed:
	default:
		t.Fatal("the released window was kept")
	}
	if got := r.Show("main"); got != ShowPending {
		t.Fatalf("show while destroying = %v", got)
	}
	if !r.Destroyed("main") {
		t.Fatal("the show during the destroy was lost")
	}
}

func TestReclaimKeepsAWindowBroughtBackAnotherWay(t *testing.T) {
	w := &fakeWindow{destroyed: make(chan struct{})}
	r := NewReclaim(func(string) ReclaimedWindow { return w })
	timers := make(chan func(), 1)
	r.after = func(_ time.Duration, fn func()) { timers <- fn }
	r.Hidden("main")
	w.visible = true
	(<-timers)()
	select {
	case <-w.destroyed:
		t.Fatal("a visible window was destroyed")
	default:
	}
	if len(timers) != 0 {
		t.Fatal("a window shown again kept its timer")
	}
}
