package ui

import (
	"encoding/json"
	"math"
	"strconv"
	"sync"
)

// The trajectory inspector lives in its own window so the phase list keeps
// the full width of the main window. Docked side by side, neither pane was
// wide enough to read a service id or a captured body.
//
// Labels are numbered because a pinned inspector freezes on its phase and
// the next click has to land in a window of its own.

// InspectorLabelPrefix starts every inspector window's label
// (TRAJECTORY_INSPECTOR_LABEL_PREFIX in the frontend).
const InspectorLabelPrefix = "trajectory-inspector-"

const (
	// inspectorWidth is the opening width: the policy table keeps its
	// confidence columns. A smaller work area shrinks it.
	inspectorWidth = 640.0
	// inspectorHeight is the opening height when there is no main window
	// to match.
	inspectorHeight    = 720.0
	inspectorMinWidth  = 320.0
	inspectorMinHeight = 400.0
	// inspectorGap is the breathing room between the main window and the
	// inspector parked beside it.
	inspectorGap = 12.0
	// inspectorInset is how far the inspector stays inside the main
	// window's top and bottom edges, so it reads as the secondary window
	// rather than a second main one.
	inspectorInset = 32.0
	// inspectorCascade is the diagonal offset per extra inspector, so a
	// second window is grabbable rather than exactly beneath the first.
	inspectorCascade = 28.0
	// inspectorWideWidth is the width a widened inspector aims for: a
	// captured JSON body and its headers read without wrapping, and the
	// window still fits a 13-inch display.
	inspectorWideWidth = 1120.0
	// inspectorScreenMargin is the room an inspector leaves at the edges
	// of the work area when it opens or widens.
	inspectorScreenMargin = 16.0
)

// Box is a window frame or work area in DIPs.
type Box struct {
	X, Y, Width, Height float64
}

// narrowFrame is how a widened inspector goes back: to its earlier width,
// against the edge it grew away from, so it returns to where it was rather
// than to a default.
type narrowFrame struct {
	width       float64
	anchorRight bool
}

// inspectorEntry is one inspector window. selection is the phase it shows,
// kept here rather than only in its page so a window repopulates itself
// after a reload.
type inspectorEntry struct {
	label     string
	pinned    bool
	selection json.RawMessage
	// narrow is set while the window is widened: how to put it back.
	narrow *narrowFrame
}

// InspectorState is what an inspector window pulls on mount, instead of
// waiting for the main window to notice it exists.
type InspectorState struct {
	Selection json.RawMessage `json:"selection"`
	Pinned    bool            `json:"pinned"`
	Wide      bool            `json:"wide"`
}

// inspectorRegistry is every live inspector window, in creation order.
type inspectorRegistry struct {
	mu      sync.Mutex
	entries []*inspectorEntry
	// nextID is never reused, so a label cannot collide with a window
	// still tearing down.
	nextID uint64
}

// target is the window a new selection belongs in: the newest one that is
// not pinned. Unpinning keeps a window where it was created, so an older
// window that was just unpinned does not take over from a newer one.
func (r *inspectorRegistry) target() (string, bool) {
	for i := len(r.entries) - 1; i >= 0; i-- {
		if !r.entries[i].pinned {
			return r.entries[i].label, true
		}
	}
	return "", false
}

func (r *inspectorRegistry) insert() string {
	r.nextID++
	label := InspectorLabelPrefix + strconv.FormatUint(r.nextID, 10)
	r.entries = append(r.entries, &inspectorEntry{label: label})
	return label
}

func (r *inspectorRegistry) remove(label string) {
	kept := r.entries[:0]
	for _, entry := range r.entries {
		if entry.label != label {
			kept = append(kept, entry)
		}
	}
	r.entries = kept
}

func (r *inspectorRegistry) find(label string) *inspectorEntry {
	for _, entry := range r.entries {
		if entry.label == label {
			return entry
		}
	}
	return nil
}

func (r *inspectorRegistry) store(label string, selection json.RawMessage) {
	if entry := r.find(label); entry != nil {
		entry.selection = selection
	}
}

func (r *inspectorRegistry) setPinned(label string, pinned bool) {
	if entry := r.find(label); entry != nil {
		entry.pinned = pinned
	}
}

func (r *inspectorRegistry) state(label string) InspectorState {
	entry := r.find(label)
	if entry == nil {
		return InspectorState{}
	}
	return InspectorState{Selection: entry.selection, Pinned: entry.pinned, Wide: entry.narrow != nil}
}

func (r *inspectorRegistry) unpinnedLabels() []string {
	var labels []string
	for _, entry := range r.entries {
		if !entry.pinned {
			labels = append(labels, entry.label)
		}
	}
	return labels
}

func (r *inspectorRegistry) removeUnpinned() []string {
	labels := r.unpinnedLabels()
	kept := r.entries[:0]
	for _, entry := range r.entries {
		if entry.pinned {
			kept = append(kept, entry)
		}
	}
	r.entries = kept
	return labels
}

// inspectorSize is the inspector's opening size: a little shorter than the
// main window, and the default width less a margin inside a smaller work
// area. Neither goes below the window's minimum.
func inspectorSize(main, work Box) (width, height float64) {
	width = math.Max(math.Min(inspectorWidth, work.Width-2*inspectorScreenMargin), inspectorMinWidth)
	height = math.Max(math.Min(main.Height-2*inspectorInset, work.Height), inspectorMinHeight)
	return width, height
}

// inspectorPlacement parks the inspector beside the main window and
// centered on its height, cascading each extra one down and to the right,
// and folding it back over the main window's right edge when the work area
// has no room to its side. A window placed past the monitor edge cannot be
// dragged back on Windows and X11, and one under the Dock or taskbar hides
// its bottom, so staying in the work area matters more than keeping the
// windows disjoint.
func inspectorPlacement(main, work Box, width, height float64, cascade int) (x, y float64) {
	offset := float64(cascade) * inspectorCascade
	x = math.Max(math.Min(main.X+main.Width+inspectorGap+offset, work.X+work.Width-width), work.X)
	y = math.Max(math.Min(main.Y+(main.Height-height)/2+offset, work.Y+work.Height-height), work.Y)
	return x, y
}

// inspectorOpeningFrame is where the cascade-th inspector opens beside a
// main window at main on the work area work.
func inspectorOpeningFrame(main, work Box, cascade int) Box {
	width, height := inspectorSize(main, work)
	x, y := inspectorPlacement(main, work, width, height, cascade)
	return Box{X: x, Y: y, Width: width, Height: height}
}

// inspectorWideFrame is the frame a widened inspector takes: as wide as
// the work area allows, up to the target, growing away from the screen
// edge it sits nearer. An inspector parked to the right of the main window
// grows leftward instead of off the display, and comes back to the same
// spot when narrowed.
func inspectorWideFrame(current, work Box) (Box, narrowFrame) {
	room := work.Width - 2*inspectorScreenMargin
	width := math.Max(math.Min(inspectorWideWidth, room), current.Width)
	anchorRight := current.X+current.Width/2 > work.X+work.Width/2
	frame := current
	frame.X = anchoredX(current, width, anchorRight, work)
	frame.Width = width
	return frame, narrowFrame{width: current.Width, anchorRight: anchorRight}
}

// inspectorNarrowFrame is the frame a widened inspector goes back to. It
// never grows: a window the operator shrank by hand while wide keeps that
// width.
func inspectorNarrowFrame(current, work Box, narrow narrowFrame) Box {
	width := math.Min(narrow.width, current.Width)
	frame := current
	frame.X = anchoredX(current, width, narrow.anchorRight, work)
	frame.Width = width
	return frame
}

// anchoredX is where a window of width starts when it keeps one edge of
// current, pulled back inside the work area where it would cross it.
func anchoredX(current Box, width float64, anchorRight bool, work Box) float64 {
	x := current.X
	if anchorRight {
		x = current.X + current.Width - width
	}
	return math.Max(math.Min(x, work.X+work.Width-width), work.X)
}
