package toys

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestYouAreAwaySoonerOnceTheAIIsQuiet(t *testing.T) {
	idle, busy := awayWaits(nil)
	if idle != 600 || busy != 1800 {
		t.Fatal(idle, busy)
	}
	// The AI quiet for the idle wait: ten minutes away is away.
	for quiet, want := range map[float64]float64{600: 600, math.Inf(1): 600, 0: 1800, 120: 1800} {
		if got := awayAfter(idle, busy, quiet); got != want {
			t.Fatal(quiet, got)
		}
	}
	// The debug override sets both, five seconds at least.
	twenty, one := 20.0, 1.0
	if a, b := awayWaits(&twenty); a != 20 || b != 20 {
		t.Fatal(a, b)
	}
	if a, b := awayWaits(&one); a != 5 || b != 5 {
		t.Fatal(a, b)
	}
}

func TestAPetLivesOnTheScreenSheWasCarriedTo(t *testing.T) {
	// A laptop under the menu bar, a big screen to its right, lower down.
	laptop := screen{frame: rect{0, 0, 1512, 982}, visible: rect{0, 33, 1512, 879}, scale: 1}
	big := screen{frame: rect{1512, -200, 3008, 1692}, visible: rect{1512, -175, 3008, 1667}, scale: 1}
	screens := []screen{laptop, big}

	// Kept as a point on the big screen: she comes back there; unplugged
	// or never moved, she lives on the main screen.
	middle := big.frame.middle()
	if s, ok := homeScreen(screens, &middle); !ok || s != big {
		t.Fatal(s)
	}
	far := point{9000, 0}
	if s, ok := homeScreen(screens, &far); !ok || s != laptop {
		t.Fatal(s)
	}
	if s, ok := homeScreen(screens, nil); !ok || s != laptop {
		t.Fatal(s)
	}
	if _, ok := homeScreen(nil, nil); ok {
		t.Fatal("a screen out of none")
	}

	// The mouse takes her across: the window follows; on the same screen
	// or over the gap below the laptop it stays put.
	if s, ok := carriedTo(screens, laptop.frame, point{1600, 400}); !ok || s != big {
		t.Fatal(s)
	}
	for _, tc := range []struct {
		on rect
		at point
	}{{laptop.frame, point{700, 400}}, {big.frame, point{2000, 0}}, {laptop.frame, point{700, 1200}}} {
		if s, ok := carriedTo(screens, tc.on, tc.at); ok {
			t.Fatal(tc, s)
		}
	}
}

func TestEachScreenShowsItsOwnWindowsAndDock(t *testing.T) {
	laptop := screen{frame: rect{0, 0, 1512, 982}, visible: rect{0, 33, 1512, 949}, scale: 1}
	big := screen{frame: rect{1512, -200, 3008, 1692}, visible: rect{1512, -175, 3008, 1595}, scale: 1}
	others := []otherWindow{
		{id: 1, kind: "term", frame: rect{100, 200, 800, 500}},
		{id: 2, kind: "code", frame: rect{2000, 100, 1200, 900}},
	}
	// The Dock sits at the bottom of the big screen.
	dock := &rect{2600, 1420, 800, 72}

	onBig := framesOn(big, dock, others, 24)
	if len(onBig.Wins) != 1 || onBig.Wins[0].ID != 2 {
		t.Fatalf("%+v", onBig.Wins)
	}
	// In the big screen's own points.
	if onBig.Wins[0].X != 488 || onBig.Wins[0].Y != 300 {
		t.Fatalf("%+v", onBig.Wins[0])
	}
	if onBig.Dock == nil || onBig.Dock.X0 != 1088 || onBig.Dock.Y != 1620 || onBig.Top != 25 || onBig.Floor != 1690 {
		t.Fatalf("%+v %+v", onBig, onBig.Dock)
	}

	onLaptop := framesOn(laptop, dock, others, 24)
	if len(onLaptop.Wins) != 1 || onLaptop.Wins[0].ID != 1 || onLaptop.Dock != nil || onLaptop.Top != 33 || onLaptop.Full {
		t.Fatalf("%+v", onLaptop)
	}
	// The page reads a missing Dock as null and every window list as a list.
	data, err := json.Marshal(framesOn(laptop, nil, nil, 0))
	if err != nil || string(data) != `{"wins":[],"dock":null,"floor":980,"top":33,"full":false,"w":1512,"h":982}` {
		t.Fatal(string(data), err)
	}
}

func TestAScaledScreenIsToldInItsOwnPoints(t *testing.T) {
	// A 150% screen in pixels, its taskbar at the bottom, to the right of
	// a 100% one.
	plain := screen{frame: rect{0, 0, 1920, 1079}, visible: rect{0, 0, 1920, 1079}, scale: 1}
	scaled := screen{frame: rect{1920, 0, 2560, 1536}, visible: rect{1920, 0, 2560, 1536}, scale: 1.5}
	others := []otherWindow{{id: 7, kind: "web", frame: rect{2220, 150, 1500, 900}}}
	frames := framesOn(scaled, nil, others, 0)
	if frames.W != 2560/1.5 || frames.H != 1024 || frames.Floor != 1022 {
		t.Fatalf("%+v", frames)
	}
	if frames.Wins[0].X != 200 || frames.Wins[0].W != 1000 {
		t.Fatalf("%+v", frames.Wins[0])
	}

	// Carried from the plain screen onto the scaled one, the spot in your
	// hand stays under the mouse: (2100 - 1920) / 1.5 = 120 points into
	// the new window, 2100 points into the old one.
	shift := carriedShift(point{2100, 300}, plain.frame, 1, scaled.frame, 1.5)
	if shift != (point{120 - 2100, 200 - 300}) {
		t.Fatal(shift)
	}
}

func TestAPetWindowStopsAPixelShortOfABareScreen(t *testing.T) {
	monitor := rect{0, 0, 1920, 1080}
	// A taskbar leaves less: the work area as it is.
	if got := petArea(monitor, rect{0, 0, 1920, 1032}); got != (rect{0, 0, 1920, 1032}) {
		t.Fatal(got)
	}
	// An auto-hidden taskbar leaves it all: a pixel short, so she is not
	// taken for a full-screen app.
	if got := petArea(monitor, monitor); got != (rect{0, 0, 1920, 1079}) {
		t.Fatal(got)
	}
}

func TestTheAIIsQuietOnlyAfterItsLastCallEnds(t *testing.T) {
	now, err := time.Parse(time.RFC3339, "2026-11-11T10:10:00Z")
	if err != nil {
		t.Fatal(err)
	}
	reading := func(calls string) json.RawMessage {
		return json.RawMessage(`{"now":"2026-11-11T10:09:59Z","calls":` + calls + `}`)
	}
	// A call still running: the AI is busy and you are watching.
	running := reading(`[{"started_at":"2026-11-11T10:00:00Z","completed_at":"2026-11-11T10:01:00Z"},` +
		`{"started_at":"2026-11-11T10:09:00Z","completed_at":null}]`)
	if got := aiQuietSeconds(running, now); got != 0 {
		t.Fatal(got)
	}
	// Quiet since the last call ended, whatever order they come in.
	ended := reading(`[{"started_at":"2026-11-11T10:05:00Z","completed_at":"2026-11-11T10:08:00Z"},` +
		`{"started_at":"2026-11-11T10:00:00Z","completed_at":"2026-11-11T10:01:00Z"}]`)
	if got := aiQuietSeconds(ended, now); got != 120 {
		t.Fatal(got)
	}
	// Nothing in the window, no reading, or a stale one: no AI to watch.
	stale := json.RawMessage(`{"now":"2026-11-11T10:00:00Z","calls":[{"started_at":"2026-11-11T09:59:00Z","completed_at":null}]}`)
	for _, activity := range []json.RawMessage{reading(`[]`), nil, stale} {
		if got := aiQuietSeconds(activity, now); !math.IsInf(got, 1) {
			t.Fatal(string(activity), got)
		}
	}
}

func TestOtherWindowsAreSortedByTheirApp(t *testing.T) {
	for owner, kind := range map[string]string{"iTerm2": "term", "Visual Studio Code": "code", "Google Chrome": "web", "Finder": "other"} {
		if got := kindOf(owner); got != kind {
			t.Fatal(owner, got)
		}
	}
	if !IsToy(PetLabel) || IsToy("main") {
		t.Fatal("toy labels")
	}
}
