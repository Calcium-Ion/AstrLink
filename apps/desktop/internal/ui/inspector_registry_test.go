package ui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func wideMonitor() Box { return Box{X: 0, Y: 0, Width: 2560, Height: 1440} }
func roomyMain() Box   { return Box{X: 200, Y: 120, Width: 1120, Height: 780} }
func laptopWorkArea() Box {
	return Box{X: 0, Y: 25, Width: 1440, Height: 800}
}

func TestInspectorParksBesideTheMainWindowInsetFromItsEdges(t *testing.T) {
	main := roomyMain()
	width, height := inspectorSize(main, wideMonitor())
	if width != inspectorWidth || height != main.Height-2*inspectorInset {
		t.Fatalf("size = %v × %v", width, height)
	}
	x, y := inspectorPlacement(main, wideMonitor(), width, height, 0)
	if x != 200+1120+inspectorGap || y != main.Y+inspectorInset {
		t.Fatalf("placement = %v, %v", x, y)
	}
}

func TestEachExtraInspectorCascadesOffTheLast(t *testing.T) {
	firstX, firstY := inspectorPlacement(roomyMain(), wideMonitor(), inspectorWidth, inspectorHeight, 0)
	thirdX, thirdY := inspectorPlacement(roomyMain(), wideMonitor(), inspectorWidth, inspectorHeight, 2)
	// Otherwise a pinned window and the one opened after it land on exactly
	// the same pixels and look like a single window.
	if thirdX-firstX != 2*inspectorCascade || thirdY-firstY != 2*inspectorCascade {
		t.Fatalf("cascade = %v, %v", thirdX-firstX, thirdY-firstY)
	}
}

func TestInspectorShrinksToASmallWorkAreaButNotBelowItsMinimum(t *testing.T) {
	// 1024 × 600 below a 25-point menu bar, beside a main window the
	// operator dragged taller than the work area.
	small := Box{X: 0, Y: 25, Width: 1024, Height: 575}
	tallMain := small
	tallMain.Height = 700
	if width, height := inspectorSize(tallMain, small); width != inspectorWidth || height != 575 {
		t.Fatalf("size = %v × %v", width, height)
	}
	tiny := Box{Width: 300, Height: 300}
	if width, height := inspectorSize(tiny, tiny); width != inspectorMinWidth || height != inspectorMinHeight {
		t.Fatalf("tiny size = %v × %v", width, height)
	}
}

func TestInspectorStaysInTheWorkAreaWhenTheMainWindowHugsTheEdge(t *testing.T) {
	// Below a 25-point menu bar and above a 75-point Dock.
	work := laptopWorkArea()
	main := Box{X: 300, Y: 600, Width: 1120, Height: 780}
	width, height := inspectorSize(main, work)
	// Even a deep cascade must not push a window past the monitor edge,
	// where it cannot be dragged back on Windows and X11, or under the
	// Dock.
	x, y := inspectorPlacement(main, work, width, height, 9)
	if x+width != work.X+work.Width || y+height != work.Y+work.Height || y < work.Y {
		t.Fatalf("placement = %v, %v", x, y)
	}
}

func TestInspectorNeverStartsLeftOfAMonitorSmallerThanItself(t *testing.T) {
	monitor := Box{X: -1920, Y: 0, Width: 400, Height: 300}
	if x, y := inspectorPlacement(monitor, monitor, inspectorWidth, inspectorHeight, 0); x != -1920 || y != 0 {
		t.Fatalf("placement = %v, %v", x, y)
	}
}

func TestAWideInspectorGrowsAwayFromTheNearerScreenEdge(t *testing.T) {
	right := Box{X: 780, Y: 80, Width: inspectorWidth, Height: 680}
	frame, narrow := inspectorWideFrame(right, laptopWorkArea())
	// Parked on the right, it grows leftward and keeps its right edge.
	if frame.Width != inspectorWideWidth || frame.X+frame.Width != right.X+right.Width ||
		frame.Y != right.Y || frame.Height != right.Height || !narrow.anchorRight {
		t.Fatalf("frame = %+v, narrow = %+v", frame, narrow)
	}
	left := right
	left.X = 40
	frame, narrow = inspectorWideFrame(left, laptopWorkArea())
	if frame.X != 40 || narrow.anchorRight {
		t.Fatalf("left frame = %+v, narrow = %+v", frame, narrow)
	}
}

func TestAWideInspectorFitsASmallDisplay(t *testing.T) {
	work := Box{X: 0, Y: 25, Width: 1024, Height: 600}
	current := Box{X: 360, Y: 40, Width: inspectorWidth, Height: 520}
	frame, _ := inspectorWideFrame(current, work)
	if frame.Width != 1024-2*inspectorScreenMargin || frame.X < work.X || frame.X+frame.Width > work.X+work.Width {
		t.Fatalf("frame = %+v", frame)
	}
}

func TestAWideInspectorNeverShrinksAWindowAlreadyWider(t *testing.T) {
	current := Box{X: 0, Y: 25, Width: 1300, Height: 700}
	if frame, _ := inspectorWideFrame(current, laptopWorkArea()); frame.Width != 1300 {
		t.Fatalf("frame = %+v", frame)
	}
}

func TestNarrowingReturnsTheInspectorToWhereItWas(t *testing.T) {
	before := Box{X: 780, Y: 80, Width: inspectorWidth, Height: 680}
	wide, narrow := inspectorWideFrame(before, laptopWorkArea())
	// The operator dragged it down while it was wide.
	moved := wide
	moved.Y = 120
	after := inspectorNarrowFrame(moved, laptopWorkArea(), narrow)
	if after.X != before.X || after.Width != before.Width || after.Y != 120 {
		t.Fatalf("after = %+v", after)
	}
}

func TestNarrowingKeepsAWidthShrunkByHand(t *testing.T) {
	current := Box{X: 100, Y: 80, Width: 380, Height: 680}
	after := inspectorNarrowFrame(current, laptopWorkArea(), narrowFrame{width: inspectorWidth})
	if after.Width != 380 {
		t.Fatalf("after = %+v", after)
	}
}

func target(t *testing.T, r *inspectorRegistry) string {
	t.Helper()
	label, _ := r.target()
	return label
}

func TestSelectionsRouteToTheNewestUnpinnedWindow(t *testing.T) {
	var r inspectorRegistry
	first := r.insert()
	second := r.insert()
	if got := target(t, &r); got != second {
		t.Fatalf("target = %q", got)
	}
	r.setPinned(second, true)
	if got := target(t, &r); got != first {
		t.Fatalf("target = %q", got)
	}
	r.setPinned(first, true)
	// Every inspector is frozen, so the next click has to open a window.
	if _, ok := r.target(); ok {
		t.Fatal("a pinned window took a selection")
	}
}

func TestUnpinningDoesNotPromoteAWindowOverANewerOne(t *testing.T) {
	var r inspectorRegistry
	first := r.insert()
	r.setPinned(first, true)
	second := r.insert()
	r.setPinned(first, false)
	// Both are unpinned now; the newest opened one keeps the selection.
	if got := target(t, &r); got != second {
		t.Fatalf("target = %q", got)
	}
	if got := r.unpinnedLabels(); !reflect.DeepEqual(got, []string{first, second}) {
		t.Fatalf("unpinned = %v", got)
	}
}

func TestLabelsAreUniqueAfterAWindowIsDestroyed(t *testing.T) {
	var r inspectorRegistry
	first := r.insert()
	r.remove(first)
	second := r.insert()
	if first == second || target(t, &r) != second || !strings.HasPrefix(second, InspectorLabelPrefix) {
		t.Fatalf("first %q, second %q", first, second)
	}
}

func TestADestroyedWindowStopsBeingATarget(t *testing.T) {
	var r inspectorRegistry
	only := r.insert()
	r.remove(only)
	if _, ok := r.target(); ok || len(r.unpinnedLabels()) != 0 {
		t.Fatal("a destroyed window is still a target")
	}
}

func chip(t *testing.T, selection json.RawMessage) string {
	t.Helper()
	var v struct {
		Row struct {
			Chip string `json:"chip"`
		} `json:"row"`
	}
	if err := json.Unmarshal(selection, &v); err != nil {
		t.Fatal(err)
	}
	return v.Row.Chip
}

func TestLeavingAConversationCancelsInspectorsStillBeingCreated(t *testing.T) {
	var r inspectorRegistry
	pending := r.insert()
	r.store(pending, json.RawMessage(`{"row":{"chip":"CLIENT"}}`))
	if got := r.removeUnpinned(); !reflect.DeepEqual(got, []string{pending}) {
		t.Fatalf("removed = %v", got)
	}
	if r.find(pending) != nil {
		t.Fatal("a cancelled inspector stayed registered")
	}
	next := r.insert()
	// A cancelled build finishing later must not close or remove its
	// replacement.
	r.remove(pending)
	if next == pending || target(t, &r) != next {
		t.Fatalf("next %q, target %q", next, target(t, &r))
	}
}

func TestLeavingAConversationPreservesPinnedInspectorsAndTheirSelection(t *testing.T) {
	var r inspectorRegistry
	pinned := r.insert()
	r.store(pinned, json.RawMessage(`{"row":{"chip":"UPSTREAM"}}`))
	r.setPinned(pinned, true)
	following := r.insert()
	if got := r.removeUnpinned(); !reflect.DeepEqual(got, []string{following}) {
		t.Fatalf("removed = %v", got)
	}
	if got := r.removeUnpinned(); len(got) != 0 {
		t.Fatalf("removed again = %v", got)
	}
	state := r.state(pinned)
	if !state.Pinned || chip(t, state.Selection) != "UPSTREAM" {
		t.Fatalf("state = %+v", state)
	}
}

func TestAnInspectorMountsWithTheLatestSelectionReceivedDuringCreation(t *testing.T) {
	var r inspectorRegistry
	pending := r.insert()
	r.store(pending, json.RawMessage(`{"row":{"chip":"CLIENT"}}`))
	r.store(target(t, &r), json.RawMessage(`{"row":{"chip":"RESULT"}}`))
	if len(r.entries) != 1 || chip(t, r.state(pending).Selection) != "RESULT" {
		t.Fatalf("state = %+v", r.state(pending))
	}
}

func TestAPinnedWindowKeepsReplayingThePhaseItFrozeOn(t *testing.T) {
	var r inspectorRegistry
	label := r.insert()
	r.store(label, json.RawMessage(`{"row":{"chip":"UPSTREAM"}}`))
	r.setPinned(label, true)
	// A newer selection cannot reach it, so a reloaded page pulls back
	// exactly what it was frozen on.
	r.store("trajectory-inspector-404", json.RawMessage(`{"row":{}}`))
	state := r.state(label)
	if !state.Pinned || chip(t, state.Selection) != "UPSTREAM" {
		t.Fatalf("state = %+v", state)
	}
}

func TestAnUnknownWindowReportsAnEmptyState(t *testing.T) {
	var r inspectorRegistry
	state := r.state("trajectory-inspector-404")
	if state.Pinned || state.Selection != nil || state.Wide {
		t.Fatalf("state = %+v", state)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"selection":null,"pinned":false,"wide":false}` {
		t.Fatalf("state encodes as %s", encoded)
	}
}
