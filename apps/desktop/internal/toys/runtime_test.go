package toys

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

type emitted struct {
	event   string
	payload any
}

type fakeWindow struct {
	label string
	mu    sync.Mutex
	// ignore is the last SetIgnoreMouseEvents, true as built.
	ignore    bool
	events    []emitted
	destroyed bool
}

func (w *fakeWindow) SetIgnoreMouseEvents(ignore bool) {
	w.mu.Lock()
	w.ignore = ignore
	w.mu.Unlock()
}

func (w *fakeWindow) Emit(event string, payload any) {
	w.mu.Lock()
	w.events = append(w.events, emitted{event, payload})
	w.mu.Unlock()
}

func (w *fakeWindow) Destroy() {
	w.mu.Lock()
	w.destroyed = true
	w.mu.Unlock()
}

func (w *fakeWindow) take(event string) []any {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []any
	rest := w.events[:0]
	for _, e := range w.events {
		if e.event == event {
			out = append(out, e.payload)
		} else {
			rest = append(rest, e)
		}
	}
	w.events = rest
	return out
}

type fakeSystem struct {
	mu       sync.Mutex
	g        geometry
	built    []*fakeWindow
	frames   map[*fakeWindow]rect
	probe    *fakeProbe
	noScreen bool
}

func (s *fakeSystem) available() bool { return true }
func (s *fakeSystem) minTop() float64 { return 0 }
func (s *fakeSystem) geometry() (geometry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.g, !s.noScreen
}

func (s *fakeSystem) build(label string, _ geometry, frame rect) (window, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := &fakeWindow{label: label, ignore: true}
	s.built = append(s.built, w)
	s.frames[w] = frame
	return w, true
}

func (s *fakeSystem) setFrame(w window, frame rect) {
	s.mu.Lock()
	s.frames[w.(*fakeWindow)] = frame
	s.mu.Unlock()
}

func (s *fakeSystem) origin(w window) (rect, float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.frames[w.(*fakeWindow)]
	return f, 1, ok
}

func (s *fakeSystem) openProbe() probe { return s.probe }

func (s *fakeSystem) frameOf(w *fakeWindow) rect {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frames[w]
}

type fakeProbe struct {
	mu     sync.Mutex
	at     point
	down   bool
	idle   float64
	closed chan struct{}
}

func (p *fakeProbe) set(at point, down bool) {
	p.mu.Lock()
	p.at, p.down = at, down
	p.mu.Unlock()
}

func (p *fakeProbe) pointer() (point, bool, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.at, true, p.down
}
func (p *fakeProbe) idleSeconds() (float64, bool) { return p.idle, true }
func (p *fakeProbe) otherWindows() []otherWindow  { return nil }
func (p *fakeProbe) close()                       { close(p.closed) }

type fixture struct {
	t     *testing.T
	sys   *fakeSystem
	r     *Runtime
	mu    sync.Mutex
	prefs Prefs
	dir   string
}

func newFixture(t *testing.T, on bool) *fixture {
	t.Helper()
	left := screen{frame: rect{0, 0, 1920, 1079}, visible: rect{0, 0, 1920, 1079}, scale: 1}
	right := screen{frame: rect{1920, 0, 1280, 719}, visible: rect{1920, 0, 1280, 719}, scale: 1}
	f := &fixture{t: t, dir: t.TempDir(), sys: &fakeSystem{
		g:      geometry{main: left.frame, screens: []screen{left, right}},
		frames: map[*fakeWindow]rect{},
		probe:  &fakeProbe{closed: make(chan struct{})},
	}}
	f.prefs = Prefs{DesktopToysPreferences: prefs.DefaultDesktopToys(), Locale: prefs.LocaleEn}
	f.prefs.Switched.Pet = &on
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f.r = newRuntime(ctx, f.sys, Deps{
		Prefs:     f.currentPrefs,
		Activity:  func(context.Context, string) (json.RawMessage, bool) { return nil, false },
		Tokens:    func(context.Context) (*int64, bool) { return nil, false },
		ConfigDir: f.dir,
	})
	return f
}

func (f *fixture) currentPrefs() Prefs {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prefs
}

func (f *fixture) turn(on bool) {
	f.mu.Lock()
	f.prefs.Switched.Pet = &on
	f.mu.Unlock()
}

func (f *fixture) pet() *fakeWindow {
	f.t.Helper()
	f.sys.mu.Lock()
	defer f.sys.mu.Unlock()
	if len(f.sys.built) != 1 {
		f.t.Fatalf("%d pet windows", len(f.sys.built))
	}
	return f.sys.built[0]
}

func TestThePetComesOutAndGoesWithHerSwitch(t *testing.T) {
	f := newFixture(t, true)
	f.r.reconcile()
	pet := f.pet()
	if got := f.sys.frameOf(pet); got != (rect{0, 0, 1920, 1079}) {
		t.Fatal(got)
	}
	// Built once, whatever the number of applies.
	f.r.reconcile()
	f.pet()

	f.turn(false)
	f.r.reconcile()
	if !pet.destroyed || f.r.window(PetLabel) != nil {
		t.Fatal("the pet's window outlived her switch")
	}
}

func TestTheMouseGoesThroughButWhereThePetTakesClicks(t *testing.T) {
	f := newFixture(t, true)
	f.r.reconcile()
	pet := f.pet()
	// Her body takes clicks; her bubble only hovers.
	f.r.Hit(PetLabel, []HitRect{{X: 100, Y: 100, W: 50, H: 80, Click: true}, {X: 300, Y: 100, W: 100, H: 40}}, false, nil)

	f.r.trackPointer(point{120, 150}, false)
	if pet.ignore {
		t.Fatal("her body let the click through")
	}
	if got := pet.take(PointerEvent); !reflect.DeepEqual(got, []any{Pointer{X: 120, Y: 150, Over: true}}) {
		t.Fatal(got)
	}
	f.r.trackPointer(point{320, 110}, false)
	if !pet.ignore {
		t.Fatal("her bubble took the click")
	}
	if got := pet.take(PointerEvent); !reflect.DeepEqual(got, []any{Pointer{X: 320, Y: 110, Over: true}}) {
		t.Fatal(got)
	}
	// A press that does not move is told too; the same point again is not.
	f.r.trackPointer(point{320, 110}, true)
	f.r.trackPointer(point{320, 110}, true)
	if got := pet.take(PointerEvent); len(got) != 1 || !got[0].(Pointer).Down {
		t.Fatal(got)
	}
	// Off her window, she hears it once.
	f.r.trackPointer(point{2500, 300}, false)
	f.r.trackPointer(point{2600, 300}, false)
	if got := pet.take(PointerEvent); !reflect.DeepEqual(got, []any{nil}) {
		t.Fatal(got)
	}
	// Held, all of her window takes the mouse.
	f.r.Hit(PetLabel, nil, true, new(bool))
	f.r.trackPointer(point{900, 900}, true)
	if pet.ignore {
		t.Fatal("a held pet let the mouse through")
	}
}

func TestAHeldPetTakesHerWindowToAnotherScreenAndStaysThere(t *testing.T) {
	f := newFixture(t, true)
	f.r.reconcile()
	pet := f.pet()
	f.r.Hit(PetLabel, nil, true, nil)
	f.r.trackPointer(point{1000, 500}, true)
	f.r.trackPointer(point{2100, 300}, true)
	right := rect{1920, 0, 1280, 719}
	if got := f.sys.frameOf(pet); got != right {
		t.Fatal(got)
	}
	moves := pet.take(ScreenEvent)
	if len(moves) != 1 || !reflect.DeepEqual(moves[0], map[string]float64{"dx": -1920, "dy": 0, "w": 1280, "h": 719}) {
		t.Fatal(moves)
	}
	data, err := os.ReadFile(filepath.Join(f.dir, homesFile))
	if err != nil || string(data) != `{"toy-pet":[2560,359.5]}` {
		t.Fatal(string(data), err)
	}

	// The next run reads her screen back; unplugged, she goes to the main
	// one, and back once it returns.
	g := newFixture(t, true)
	g.dir = f.dir
	g.r.deps.ConfigDir = f.dir
	g.r.reconcile()
	again := g.pet()
	if got := g.sys.frameOf(again); got != right {
		t.Fatal(got)
	}
	g.sys.mu.Lock()
	screens := g.sys.g.screens
	g.sys.g.screens = screens[:1]
	g.sys.mu.Unlock()
	g.r.refreshGeometry()
	if got := g.sys.frameOf(again); got != (rect{0, 0, 1920, 1079}) {
		t.Fatal(got)
	}
	g.sys.mu.Lock()
	g.sys.g.screens = screens
	g.sys.mu.Unlock()
	g.r.refreshGeometry()
	if got := g.sys.frameOf(again); got != right {
		t.Fatal(got)
	}
}

func TestThePollEndsWithTheSwitchAndToldTheIdleTime(t *testing.T) {
	f := newFixture(t, true)
	f.r.reconcile()
	pet := f.pet()
	f.sys.probe.idle = 42
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		f.r.poll(ticks)
		close(done)
	}()
	// The 31st tick is taken once the 30th, which reads the idle time,
	// was handled.
	for range 31 {
		select {
		case ticks <- time.Time{}:
		case <-done:
			t.Fatal("the poll ended with the pet on")
		}
	}
	// Off, the poll ends at the tick it reads the switch on: the one it
	// may be handling, or the next.
	f.turn(false)
	for taken := 0; ; taken++ {
		if taken > 1 {
			t.Fatal("the poll outlived the switch")
		}
		select {
		case ticks <- time.Time{}:
			continue
		case <-done:
		}
		break
	}
	select {
	case <-f.sys.probe.closed:
	default:
		t.Fatal("the probe stayed open")
	}
	idle := pet.take(IdleEvent)
	if len(idle) != 1 || !reflect.DeepEqual(idle[0], map[string]float64{"idle": 42, "away": 600}) {
		t.Fatal(idle)
	}
	frames := pet.take(FramesEvent)
	if len(frames) != 1 || frames[0].(Frames).W != 1920 {
		t.Fatal(frames)
	}
	// A page that loads later reads them from the snapshot.
	if s := f.r.Snapshot(PetLabel); s.Frames == nil || s.Frames.W != 1920 || s.Prefs.Locale != prefs.LocaleEn {
		t.Fatalf("%+v", s)
	}
}

func TestTheFeedReadsTokensEveryTwentySeconds(t *testing.T) {
	f := newFixture(t, true)
	f.r.reconcile()
	pet := f.pet()
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	var since []string
	tokens := 0
	f.r.deps.Now = func() time.Time { return now }
	f.r.deps.Activity = func(_ context.Context, from string) (json.RawMessage, bool) {
		since = append(since, from)
		return json.RawMessage(`{"now":"x","calls":[]}`), true
	}
	f.r.deps.Tokens = func(context.Context) (*int64, bool) {
		tokens++
		n := int64(tokens * 1000)
		return &n, true
	}
	f.r.feedOnce()
	now = now.Add(10 * time.Second)
	f.r.feedOnce()
	now = now.Add(10 * time.Second)
	f.r.feedOnce()
	if tokens != 2 || since[0] != "2026-11-11T09:53:00.000Z" {
		t.Fatal(tokens, since)
	}
	feeds := pet.take(FeedEvent)
	if len(feeds) != 3 || *feeds[1].(Feed).Tokens != 1000 || *feeds[2].(Feed).Tokens != 2000 {
		t.Fatal(feeds)
	}
	data, err := json.Marshal(feeds[0])
	if err != nil || string(data) != `{"activity":{"now":"x","calls":[]},"tokens":1000,"sample":false}` {
		t.Fatal(string(data), err)
	}
}

func TestPrefsReachThePageAsOneObject(t *testing.T) {
	p := Prefs{DesktopToysPreferences: prefs.DefaultDesktopToys(), Locale: prefs.LocaleZhCN}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["locale"]) != `"zh-CN"` || string(fields["level"]) != `"normal"` || fields["pet"] == nil || string(fields["switched"]) != "{}" || fields["on"] != nil {
		t.Fatal(string(data))
	}
	if InitializationScript(true) != "window.__ASTRLINK_DESKTOP_TOYS__ = true;" {
		t.Fatal(InitializationScript(true))
	}
}
