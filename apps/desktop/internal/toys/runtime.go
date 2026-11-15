package toys

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// The events a toy window hears.
const (
	FeedEvent    = "toys:feed"
	PrefsEvent   = "toys:prefs"
	PointerEvent = "toys:pointer"
	FramesEvent  = "toys:frames"
	IdleEvent    = "toys:idle"
	ScreenEvent  = "toys:screen"
)

// window is a toy window as the runtime moves it and talks to its page.
type window interface {
	SetIgnoreMouseEvents(ignore bool)
	Emit(event string, payload any)
	Destroy()
}

// system is what the runtime needs from the desktop: the screens, the
// toy windows, and a probe for the mouse, idle time and other windows.
type system interface {
	available() bool
	// minTop is the menu bar's height at least, where a pet does not go.
	minTop() float64
	geometry() (geometry, bool)
	build(label string, g geometry, frame rect) (window, bool)
	setFrame(w window, frame rect)
	// origin is a window's frame in global units, and the units in one of
	// its points.
	origin(w window) (rect, float64, bool)
	openProbe() probe
}

type probe interface {
	// pointer is where the mouse is, whether that could be read, and
	// whether its main button is down.
	pointer() (point, bool, bool)
	idleSeconds() (float64, bool)
	otherWindows() []otherWindow
	close()
}

// Prefs is what a toy window hears about its preferences: the toys' own,
// and the language it speaks.
type Prefs struct {
	prefs.DesktopToysPreferences
	Locale prefs.Locale `json:"locale"`
}

func (p Prefs) MarshalJSON() ([]byte, error) {
	toys, err := json.Marshal(p.DesktopToysPreferences)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(toys, &fields); err != nil {
		return nil, err
	}
	locale, err := json.Marshal(p.Locale)
	if err != nil {
		return nil, err
	}
	fields["locale"] = locale
	return json.Marshal(fields)
}

// Feed is the activity every toy window reads.
type Feed struct {
	Activity json.RawMessage `json:"activity"`
	Tokens   *int64          `json:"tokens"`
	// Sample is debug builds only: the toys play the sample conversations
	// instead.
	Sample bool `json:"sample"`
}

// Snapshot is what a toy's page needs once it has loaded.
type Snapshot struct {
	Prefs  Prefs   `json:"prefs"`
	Feed   Feed    `json:"feed"`
	Frames *Frames `json:"frames"`
}

// pointerAt is a pet window's last pointer report, in its own points.
type pointerAt struct {
	x, y float64
	over bool
}

// Pointer is what a pet hears of the mouse over her window, in its points.
type Pointer struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Over bool    `json:"over"`
	Down bool    `json:"down"`
}

type hits struct {
	rects []HitRect
	// hold takes the mouse everywhere: a pet is pressed or held.
	hold bool
	// carry: a pet is in your hand, and her window follows the mouse to
	// other screens.
	carry bool
	// origin is the window's frame in global units, as last read.
	origin   *rect
	unit     float64
	originAt time.Time
	// interactive: the window takes the mouse.
	interactive bool
	pointer     *pointerAt
	// down is the button as last told, so a click that does not move is
	// told too.
	down bool
}

// Deps are what the runtime reads from the rest of the app.
type Deps struct {
	Prefs func() Prefs
	// Activity reads Core's request activity since an RFC 3339 time; false
	// while Core is not ready or the read failed.
	Activity func(ctx context.Context, since string) (json.RawMessage, bool)
	// Tokens reads today's tokens; false when the read failed.
	Tokens func(ctx context.Context) (*int64, bool)
	// ConfigDir keeps the pets' screens, and in development the debug
	// overrides.
	ConfigDir string
	// Debug reads toys-debug.json, so the pet can be checked without paid
	// traffic or minutes of idling.
	Debug bool
	// Preload is the script a toy window's page runs first.
	Preload func(label string) string
	Now     func() time.Time
}

// Runtime keeps the desktop pet: her window, her feed and the poll that
// lets the mouse through her window but where she is.
type Runtime struct {
	sys  system
	deps Deps

	mu       sync.Mutex
	activity json.RawMessage
	tokens   *int64
	tokensAt time.Time
	hits     map[string]*hits
	feeding  bool
	polling  bool
	// frames is per pet window what it last heard about its screen.
	frames map[string]Frames
	// homes is per pet window a point on the screen it lives on; read
	// once.
	homes map[string]point
	// placed is per pet window the screen frame it was last put on.
	placed   map[string]rect
	geometry *geometry
	windows  map[string]window

	// turn builds or closes windows one at a time.
	turn sync.Mutex
	// stop ends the feed and the poll.
	ctx context.Context
}

// New returns a runtime on the desktop's own system. ctx ends its feed
// and poll.
func New(ctx context.Context, deps Deps) *Runtime {
	r := newRuntime(ctx, nil, deps)
	r.sys = mygoSystem{preload: deps.Preload, closed: r.Closed}
	return r
}

func newRuntime(ctx context.Context, sys system, deps Deps) *Runtime {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Runtime{
		sys: sys, deps: deps, ctx: ctx,
		hits: map[string]*hits{}, frames: map[string]Frames{}, placed: map[string]rect{}, windows: map[string]window{},
	}
}

// Available reports whether this desktop can show the pet.
func (r *Runtime) Available() bool { return r.sys.available() }

// InitializationScript tells every page whether the pet can come out, so
// the pets page shows only where she can.
func InitializationScript(available bool) string {
	if available {
		return "window.__ASTRLINK_DESKTOP_TOYS__ = true;"
	}
	return "window.__ASTRLINK_DESKTOP_TOYS__ = false;"
}

// debugOverrides are toys-debug.json's: {"sample": true, "away_after": 20}.
type debugOverrides struct {
	Sample    bool     `json:"sample"`
	AwayAfter *float64 `json:"away_after"`
}

func (r *Runtime) debugOverrides() debugOverrides {
	var d debugOverrides
	if !r.deps.Debug || r.deps.ConfigDir == "" {
		return d
	}
	if data, err := os.ReadFile(filepath.Join(r.deps.ConfigDir, "toys-debug.json")); err == nil {
		if json.Unmarshal(data, &d) != nil {
			d = debugOverrides{}
		}
	}
	return d
}

func (r *Runtime) homesPath() string {
	if r.deps.ConfigDir == "" {
		return ""
	}
	return filepath.Join(r.deps.ConfigDir, homesFile)
}

// homesLocked is the pet windows' screens as kept on disk, read the first
// time. r.mu is held.
func (r *Runtime) homesLocked() map[string]point {
	if r.homes != nil {
		return r.homes
	}
	r.homes = map[string]point{}
	if path := r.homesPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var kept map[string][2]float64
			if json.Unmarshal(data, &kept) == nil {
				for label, p := range kept {
					r.homes[label] = point{p[0], p[1]}
				}
			}
		}
	}
	return r.homes
}

func (r *Runtime) saveHomes(homes map[string]point) {
	path := r.homesPath()
	if path == "" {
		return
	}
	kept := make(map[string][2]float64, len(homes))
	for label, p := range homes {
		kept[label] = [2]float64{p.x, p.y}
	}
	data, err := json.Marshal(kept)
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			err = os.WriteFile(path, data, 0o644)
		}
	}
	if err != nil {
		log.Printf("astrlink: desktop toys: unable to keep the pets' screens: %v", err)
	}
}

// emitToys sends to every toy window.
func (r *Runtime) emitToys(event string, payload any) {
	r.mu.Lock()
	windows := make([]window, 0, len(r.windows))
	for _, w := range r.windows {
		windows = append(windows, w)
	}
	r.mu.Unlock()
	for _, w := range windows {
		w.Emit(event, payload)
	}
}

func (r *Runtime) window(label string) window {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.windows[label]
}

// Apply builds the pet's window if the preferences turn her on, or closes
// it, then tells her the preferences.
func (r *Runtime) Apply() {
	available := r.sys.available()
	if available {
		r.reconcile()
	}
	p := r.deps.Prefs()
	r.emitToys(PrefsEvent, p)
	if p.On().Any() && available {
		r.startFeed()
		r.startPoll()
	}
}

// PrefsChanged tells the toys the language changed: the pet says things.
func (r *Runtime) PrefsChanged() { r.emitToys(PrefsEvent, r.deps.Prefs()) }

// Closed forgets a toy window that closed on its own.
func (r *Runtime) Closed(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.windows, label)
	delete(r.hits, label)
	delete(r.placed, label)
	delete(r.frames, label)
}

// placement is the pet's window: her screen, the one under her home point.
func placement(g geometry, home *point) rect {
	if s, ok := homeScreen(g.screens, home); ok {
		return s.frame
	}
	return g.main
}

// reconcile builds or closes the pet's window to match the preferences,
// one at a time, reading the preferences once it runs: a quick on and off
// ends with the last word.
func (r *Runtime) reconcile() {
	r.turn.Lock()
	defer r.turn.Unlock()
	g, ok := r.sys.geometry()
	if !ok {
		log.Printf("astrlink: desktop toys: unable to read the screens")
		return
	}
	r.mu.Lock()
	r.geometry = &g
	existing := r.windows[PetLabel]
	var home *point
	if h, ok := r.homesLocked()[PetLabel]; ok {
		home = &h
	}
	r.mu.Unlock()
	on := r.deps.Prefs().On().Pet
	switch {
	case on && existing == nil:
		frame := placement(g, home)
		w, ok := r.sys.build(PetLabel, g, frame)
		if !ok {
			return
		}
		r.mu.Lock()
		r.windows[PetLabel] = w
		r.placed[PetLabel] = frame
		r.mu.Unlock()
	case !on && existing != nil:
		r.Closed(PetLabel)
		existing.Destroy()
	}
}

// Snapshot is what the page of the toy window label needs once loaded.
func (r *Runtime) Snapshot(label string) Snapshot {
	r.mu.Lock()
	s := Snapshot{Feed: Feed{Activity: r.activity, Tokens: r.tokens}}
	if f, ok := r.frames[label]; ok {
		s.Frames = &f
	}
	r.mu.Unlock()
	s.Prefs = r.deps.Prefs()
	s.Feed.Sample = r.debugOverrides().Sample
	return s
}

// Hit takes which parts of toy window label hover or take clicks; hold
// keeps the whole window taking the mouse while something is pressed or
// dragged, and carry (hold if left out) lets a dragged pet take her
// window to another screen.
func (r *Runtime) Hit(label string, rects []HitRect, hold bool, carry *bool) {
	if len(rects) > 64 {
		rects = rects[:64]
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.hitsLocked(label)
	h.rects = append([]HitRect(nil), rects...)
	h.hold = hold
	h.carry = hold && (carry == nil || *carry)
	h.originAt = time.Time{}
}

func (r *Runtime) hitsLocked(label string) *hits {
	h, ok := r.hits[label]
	if !ok {
		h = &hits{}
		r.hits[label] = h
	}
	return h
}

func (r *Runtime) wanted() bool { return r.deps.Prefs().On().Any() }

func (r *Runtime) startFeed() {
	r.mu.Lock()
	if r.feeding {
		r.mu.Unlock()
		return
	}
	r.feeding = true
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			r.feeding = false
			r.mu.Unlock()
		}()
		ticker := time.NewTicker(feedInterval)
		defer ticker.Stop()
		for r.wanted() {
			r.feedOnce()
			select {
			case <-r.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (r *Runtime) feedOnce() {
	if r.debugOverrides().Sample {
		r.emitToys(FeedEvent, Feed{Sample: true})
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()
	now := r.deps.Now()
	since := now.Add(-feedWindowMinutes * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z07:00")
	activity, ok := r.deps.Activity(ctx, since)
	if !ok {
		return
	}
	r.mu.Lock()
	due := r.tokensAt.IsZero() || now.Sub(r.tokensAt) >= tokensInterval
	tokens := r.tokens
	r.mu.Unlock()
	if due {
		if read, ok := r.deps.Tokens(ctx); ok {
			tokens = read
		}
	}
	r.mu.Lock()
	r.activity = activity
	if due {
		r.tokens = tokens
		r.tokensAt = now
	}
	r.mu.Unlock()
	r.emitToys(FeedEvent, Feed{Activity: activity, Tokens: tokens})
}

func (r *Runtime) startPoll() {
	r.mu.Lock()
	if r.polling {
		r.mu.Unlock()
		return
	}
	r.polling = true
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			r.polling = false
			r.mu.Unlock()
		}()
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		r.poll(ticker.C)
	}()
}

// poll runs a tick at a time until the toys are off or the app quits.
func (r *Runtime) poll(ticks <-chan time.Time) {
	p := r.sys.openProbe()
	defer p.close()
	var count uint64
	last := map[string]string{}
	debugAfter := r.debugOverrides().AwayAfter
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticks:
		}
		count++
		if !r.wanted() {
			return
		}
		at, ok, down := p.pointer()
		// The Dock widens and narrows as apps open and quit.
		if count%30 == 0 {
			r.refreshGeometry()
		}
		if count%150 == 0 {
			debugAfter = r.debugOverrides().AwayAfter
		}
		if ok {
			r.trackPointer(at, down)
		}
		pet := r.window(PetLabel)
		if pet == nil || !r.deps.Prefs().On().Pet {
			continue
		}
		if count%6 == 0 || (down && count%2 == 0) {
			r.sendFrames(p, last)
		}
		if count%30 == 0 {
			idleWait, busyWait := awayWaits(debugAfter)
			r.mu.Lock()
			quiet := aiQuietSeconds(r.activity, r.deps.Now())
			r.mu.Unlock()
			away := awayAfter(idleWait, busyWait, quiet)
			// Without an idle reading you are never away.
			idle, _ := p.idleSeconds()
			pet.Emit(IdleEvent, map[string]float64{"idle": idle, "away": away})
		}
	}
}

// refreshGeometry reads the screens again, and puts the pet's window back
// on her own screen, or on the main one while that screen is unplugged.
func (r *Runtime) refreshGeometry() {
	g, ok := r.sys.geometry()
	if !ok {
		return
	}
	r.mu.Lock()
	r.geometry = &g
	homes := r.homesLocked()
	type move struct {
		w     window
		frame rect
	}
	var moves []move
	for label, w := range r.windows {
		var home *point
		if h, ok := homes[label]; ok {
			home = &h
		}
		want := placement(g, home)
		h := r.hits[label]
		// Not out of your hand; she goes once you let go.
		if (h != nil && h.hold) || r.placed[label] == want {
			continue
		}
		r.placed[label] = want
		if h != nil {
			h.originAt = time.Time{}
		}
		moves = append(moves, move{w, want})
	}
	r.mu.Unlock()
	for _, m := range moves {
		r.sys.setFrame(m.w, m.frame)
	}
}

// carry takes a held pet's window onto the screen the mouse carried her
// to; its page hears how far its points moved. The screen is kept for
// next time.
func (r *Runtime) carry(label string, w window, cursor point) {
	r.mu.Lock()
	h := r.hits[label]
	if h == nil || !h.carry || r.geometry == nil {
		r.mu.Unlock()
		return
	}
	from, ok := r.placed[label]
	if !ok {
		r.mu.Unlock()
		return
	}
	to, ok := carriedTo(r.geometry.screens, from, cursor)
	if !ok {
		r.mu.Unlock()
		return
	}
	fromScale := h.unit
	for _, s := range r.geometry.screens {
		if s.frame == from {
			fromScale = s.scale
		}
	}
	if fromScale <= 0 {
		fromScale = 1
	}
	shift := carriedShift(cursor, from, fromScale, to.frame, to.scale)
	r.placed[label] = to.frame
	homes := r.homesLocked()
	homes[label] = to.frame.middle()
	kept := make(map[string]point, len(homes))
	for k, v := range homes {
		kept[k] = v
	}
	frame := to.frame
	h.origin, h.unit, h.originAt, h.pointer = &frame, to.scale, r.deps.Now(), nil
	r.mu.Unlock()
	w.Emit(ScreenEvent, map[string]float64{"dx": shift.x, "dy": shift.y, "w": to.frame.w / to.scale, "h": to.frame.h / to.scale})
	r.sys.setFrame(w, to.frame)
	r.saveHomes(kept)
}

// trackPointer lets the mouse through everywhere but the parts a pet says
// are her own, and tells her where the mouse is.
func (r *Runtime) trackPointer(at point, down bool) {
	for _, label := range []string{PetLabel} {
		w := r.window(label)
		if w == nil {
			continue
		}
		r.carry(label, w, at)
		freshFor := 500 * time.Millisecond
		if down {
			freshFor = 60 * time.Millisecond
		}
		now := r.deps.Now()
		r.mu.Lock()
		h := r.hitsLocked(label)
		stale := h.origin == nil || h.originAt.IsZero() || now.Sub(h.originAt) >= freshFor
		r.mu.Unlock()
		if stale {
			if origin, unit, ok := r.sys.origin(w); ok {
				r.mu.Lock()
				h = r.hitsLocked(label)
				h.origin, h.unit, h.originAt = &origin, unit, now
				r.mu.Unlock()
			}
		}
		r.mu.Lock()
		h = r.hitsLocked(label)
		change, report, send := h.track(at, down)
		r.mu.Unlock()
		if change != nil {
			w.SetIgnoreMouseEvents(!*change)
		}
		if send {
			if report == nil {
				w.Emit(PointerEvent, nil)
			} else {
				w.Emit(PointerEvent, *report)
			}
		}
	}
}

// track reads the mouse at a global point against a window's parts: a
// change of whether the window takes the mouse, and whether to tell the
// page where the mouse is (nil once it left).
func (h *hits) track(at point, down bool) (change *bool, report *Pointer, send bool) {
	if h.origin == nil {
		return nil, nil, false
	}
	o := *h.origin
	unit := h.unit
	if unit <= 0 {
		unit = 1
	}
	x, y := (at.x-o.x)/unit, (at.y-o.y)/unit
	inside := x >= 0 && y >= 0 && x < o.w/unit && y < o.h/unit
	over, click := false, h.hold
	if inside {
		for _, rect := range h.rects {
			if rect.contains(x, y) {
				over = true
				click = click || rect.Click
			}
		}
	}
	if click != h.interactive {
		h.interactive = click
		change = &click
	}
	next := pointerAt{x, y, over || h.hold}
	switch {
	case inside || h.hold:
		moved := h.down != down || h.pointer == nil ||
			math.Abs(h.pointer.x-x) >= 0.5 || math.Abs(h.pointer.y-y) >= 0.5 || h.pointer.over != next.over
		h.pointer, h.down = &next, down
		if moved {
			return change, &Pointer{X: x, Y: y, Over: next.over, Down: down}, true
		}
	case h.pointer != nil:
		h.pointer = nil
		return change, nil, true
	}
	return change, nil, false
}

// sendFrames tells each pet what is on her own screen, when it changed.
func (r *Runtime) sendFrames(p probe, last map[string]string) {
	r.mu.Lock()
	if r.geometry == nil {
		r.mu.Unlock()
		return
	}
	g := *r.geometry
	type pet struct {
		label  string
		w      window
		placed *rect
	}
	var pets []pet
	for label, w := range r.windows {
		var placed *rect
		if f, ok := r.placed[label]; ok {
			placed = &f
		}
		pets = append(pets, pet{label, w, placed})
	}
	r.mu.Unlock()
	if len(pets) == 0 {
		return
	}
	others := p.otherWindows()
	for _, pet := range pets {
		var home *point
		if pet.placed != nil {
			m := pet.placed.middle()
			home = &m
		}
		s, ok := homeScreen(g.screens, home)
		if !ok {
			continue
		}
		frames := framesOn(s, g.dock, others, r.sys.minTop())
		data, err := json.Marshal(frames)
		if err != nil || last[pet.label] == string(data) {
			continue
		}
		last[pet.label] = string(data)
		r.mu.Lock()
		r.frames[pet.label] = frames
		r.mu.Unlock()
		pet.w.Emit(FramesEvent, frames)
	}
}

// Available reports whether this desktop can show the pet, before any
// runtime starts.
func Available() bool { return platformAvailable() }
