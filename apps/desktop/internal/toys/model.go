// Package toys runs the desktop pet: the big girl outside the main window.
// She lives in her own transparent page window and draws her orbs with the
// overview's crew engine; this package builds and places that window,
// reads the activity for her, and does what a page cannot: which parts of
// a click-through window take the mouse, where the other apps' windows
// are, and how long the keyboard and mouse, and the AI, have been idle.
//
// macOS, Windows and Linux on X11 have the pet. A Wayland session lets no
// app place its windows or keep them on top, so there Available is false,
// nothing is built and the frontend shows no pets page.
package toys

import (
	"encoding/json"
	"math"
	"strings"
	"time"
)

// PetLabel is the pet window's label; every toy window's starts with
// labelPrefix.
const (
	PetLabel    = "toy-pet"
	labelPrefix = "toy-"
)

const (
	feedInterval   = time.Second
	tokensInterval = 20 * time.Second
	// feedWindowMinutes is the activity board's window plus its grace,
	// with a minute to spare.
	feedWindowMinutes = 7
	tick              = 33 * time.Millisecond
	// activityStale is how old an activity reading may be and still say
	// something about the AI now.
	activityStale = 60 * time.Second
	// awaySeconds without input, once the AI is quiet, make you away.
	awaySeconds = 600.0
	// awayBusyFactor: while the AI still works you may be watching it, and
	// count as away only after this many times as long.
	awayBusyFactor = 3.0
	// homesFile keeps, as a point on it, the screen the pet's window lives
	// on across runs.
	homesFile = "toy-screens.json"
)

// IsToy reports whether a window is a toy's: transparent, drawn by its
// page, never themed.
func IsToy(label string) bool { return strings.HasPrefix(label, labelPrefix) }

// rect is x, y, width and height in global top-left units: points on
// macOS, pixels on Windows and Linux.
type rect struct{ x, y, w, h float64 }

type point struct{ x, y float64 }

func (r rect) holds(p point) bool { return p.x >= r.x && p.x < r.x+r.w && p.y >= r.y && p.y < r.y+r.h }

func (r rect) middle() point { return point{r.x + r.w/2, r.y + r.h/2} }

// screen is the frame a pet window covers on a screen, the part of it the
// menu bar and the Dock leave, and how many global units make one of the
// window's points.
type screen struct {
	frame, visible rect
	scale          float64
}

// geometry is the screens in global units.
type geometry struct {
	// main is the main screen: the pet's until you carry her elsewhere.
	main rect
	// screens are every screen, the main one first.
	screens []screen
	// dock is the Dock's own frame, as the Dock reports it; macOS only.
	dock *rect
}

// homeScreen is the screen a pet window lives on: the one under its home
// point, or the main one, first in the list, once that screen is gone.
func homeScreen(screens []screen, home *point) (screen, bool) {
	if home != nil {
		for _, s := range screens {
			if s.frame.holds(*home) {
				return s, true
			}
		}
	}
	if len(screens) == 0 {
		return screen{}, false
	}
	return screens[0], true
}

// carriedTo is the screen the mouse has carried a held pet onto, if her
// window is not on it yet; none over a gap between screens.
func carriedTo(screens []screen, on rect, cursor point) (screen, bool) {
	for _, s := range screens {
		if s.frame.holds(cursor) {
			return s, s.frame != on
		}
	}
	return screen{}, false
}

// carriedShift is how far a point under the cursor moves in a pet
// window's own points when the window goes from one screen to another:
// the spot in your hand stays under the mouse whatever either screen's
// scale.
func carriedShift(cursor point, from rect, fromScale float64, to rect, toScale float64) point {
	return point{
		(cursor.x-to.x)/toScale - (cursor.x-from.x)/fromScale,
		(cursor.y-to.y)/toScale - (cursor.y-from.y)/fromScale,
	}
}

// otherWindow is another app's window on the screens: its number, its
// kind and its frame. Only macOS lists them.
type otherWindow struct {
	id    int64
	kind  string
	frame rect
}

// FrameWin is another app's window as a pet sees it.
type FrameWin struct {
	ID   int64   `json:"id"`
	Kind string  `json:"kind"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

type FrameDock struct {
	X0 float64 `json:"x0"`
	X1 float64 `json:"x1"`
	Y  float64 `json:"y"`
}

// Frames is what a pet sees on her screen, in her window's points.
type Frames struct {
	Wins  []FrameWin `json:"wins"`
	Dock  *FrameDock `json:"dock"`
	Floor float64    `json:"floor"`
	Top   float64    `json:"top"`
	Full  bool       `json:"full"`
	W     float64    `json:"w"`
	H     float64    `json:"h"`
}

// framesOn is what a pet sees on her screen: the other apps' windows on
// it front to back, the Dock if it sits at its bottom, the floor and the
// menu bar, whose height minTop is at least.
func framesOn(s screen, dock *rect, others []otherWindow, minTop float64) Frames {
	m, scale := s.frame, s.scale
	out := Frames{Wins: []FrameWin{}}
	for _, other := range others {
		o := other.frame
		if math.Abs(o.x-m.x) < 1 && math.Abs(o.y-m.y) < 1 && math.Abs(o.w-m.w) < 1 && math.Abs(o.h-m.h) < 1 {
			out.Full = true
		}
		if o.w/scale < 120 || o.h/scale < 60 || o.x+o.w < m.x || o.x > m.x+m.w || o.y+o.h < m.y || o.y > m.y+m.h {
			continue
		}
		out.Wins = append(out.Wins, FrameWin{ID: other.id, Kind: other.kind,
			X: (o.x - m.x) / scale, Y: (o.y - m.y) / scale, W: o.w / scale, H: o.h / scale})
		if len(out.Wins) >= 40 {
			break
		}
	}
	// A Dock kept at the bottom of this screen leaves a strip under the
	// visible frame; hidden, at a side or on another screen, there is none
	// to stand on.
	v := s.visible
	dockHeight := (m.y + m.h) - (v.y + v.h)
	if dock != nil && dockHeight > 8 && math.Abs(dock.y+dock.h-(m.y+m.h)) < 2 {
		x0, x1 := math.Max(dock.x-m.x, 0)/scale, math.Min(dock.x+dock.w-m.x, m.w)/scale
		if x1-x0 >= 60 {
			out.Dock = &FrameDock{X0: x0, X1: x1, Y: (v.y + v.h - m.y) / scale}
		}
	}
	out.Floor = m.h/scale - 2
	out.Top = math.Max((v.y-m.y)/scale, minTop)
	out.W, out.H = m.w/scale, m.h/scale
	return out
}

// petArea is the work area a pet window covers on Windows and Linux: what
// the taskbar and panels leave, a pixel short when nothing is left out, so
// neither system takes her window for a full-screen app (Windows hides the
// taskbar for one, and Mutter stops compositing it, which turns her black).
func petArea(monitor, work rect) rect {
	if work == monitor {
		work.h--
	}
	return work
}

// HitRect is a part of a toy window, in its own points, that hovers or
// takes clicks.
type HitRect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
	// Click takes clicks; otherwise only hover is reported.
	Click bool `json:"click,omitempty"`
}

func (r HitRect) contains(x, y float64) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// aiQuietSeconds is how long the AI has done nothing in an activity
// reading: zero while a call runs, infinite when nothing ran in its window
// or the reading is stale.
func aiQuietSeconds(activity json.RawMessage, now time.Time) float64 {
	if len(activity) == 0 {
		return math.Inf(1)
	}
	var reading struct {
		Now   string `json:"now"`
		Calls []struct {
			CompletedAt *string `json:"completed_at"`
		} `json:"calls"`
	}
	if json.Unmarshal(activity, &reading) != nil {
		return math.Inf(1)
	}
	read, err := time.Parse(time.RFC3339Nano, reading.Now)
	if err != nil || now.Sub(read) > activityStale+999*time.Millisecond {
		return math.Inf(1)
	}
	var last time.Time
	for _, call := range reading.Calls {
		if call.CompletedAt == nil {
			return 0
		}
		ended, err := time.Parse(time.RFC3339Nano, *call.CompletedAt)
		if err != nil {
			return 0
		}
		if ended.After(last) {
			last = ended
		}
	}
	if last.IsZero() {
		return math.Inf(1)
	}
	return math.Max(float64(now.Sub(last).Milliseconds()), 0) / 1000
}

// awayWaits are the two waits before you count as away, in seconds: once
// the AI is quiet, and while it works. A debug override sets both.
func awayWaits(debug *float64) (idle, busy float64) {
	if debug != nil {
		seconds := math.Max(*debug, 5)
		return seconds, seconds
	}
	return awaySeconds, awaySeconds * awayBusyFactor
}

// awayAfter is how many seconds without input make you away: the idle
// wait once the AI has been quiet that long, the longer busy wait while
// it works.
func awayAfter(idleWait, busyWait, aiQuiet float64) float64 {
	if aiQuiet >= idleWait {
		return idleWait
	}
	return math.Max(busyWait, idleWait)
}

// kindOf sorts another app's window by its owner's name.
func kindOf(owner string) string {
	owner = strings.ToLower(owner)
	for _, group := range []struct {
		kind  string
		names []string
	}{
		{"term", []string{"terminal", "iterm", "warp", "ghostty", "kitty", "alacritty", "wezterm", "hyper", "tabby", "rio", "termius"}},
		{"code", []string{"code", "cursor", "zed", "intellij", "goland", "pycharm", "webstorm", "rustrover", "clion", "phpstorm",
			"rider", "android studio", "sublime", "nova", "windsurf", "trae", "fleet", "bbedit", "textmate", "emacs"}},
		{"web", []string{"safari", "chrome", "chromium", "firefox", "arc", "edge", "brave", "opera", "vivaldi", "zen", "orion",
			"dia", "comet", "duckduckgo"}},
	} {
		for _, name := range group.names {
			if strings.Contains(owner, name) {
				return group.kind
			}
		}
	}
	return "other"
}
