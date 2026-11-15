// Package widget feeds the macOS desktop widget (apps/desktop/macos-widget).
// The host writes the tray's plan digest into the App Group container the
// widget extension reads, then asks WidgetKit to redraw. The widget never
// calls Core or a provider: plan quotas are read on the tray's cadence only,
// so the widget adds no upstream request of its own.
//
// Only a bundled build signed with an App Group feeds a widget. An unbundled
// development run has none and writes nothing, and the development bundle
// names its own group, so it never overwrites the release widget's data.
package widget

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

const (
	// SnapshotVersion is QuotaSnapshot.version in the extension; it
	// ignores any other version.
	SnapshotVersion = 1
	// fileName is QuotaSnapshot.fileName in the extension.
	fileName = "quota-widget.json"
	// reloadMinInterval spaces reloads for changed numbers. WidgetKit
	// budgets reloads, and a burst of preference edits needs only the last.
	reloadMinInterval = time.Minute
	// freshnessReload brings a read that changed nothing but its time to
	// the widget before the widget's own stale mark (ten minutes) would
	// show.
	freshnessReload = 8 * time.Minute
	// placedQueryInterval is how often the host asks WidgetKit whether a
	// widget is placed.
	placedQueryInterval = time.Minute
)

// Snapshot is the file the extension reads (QuotaSnapshot in
// QuotaSnapshot.swift).
type Snapshot struct {
	Version int
	// GeneratedAt is when the host last read the plan quotas.
	GeneratedAt  time.Time
	Locale       prefs.Locale
	QuotaDisplay prefs.QuotaDisplayMode
	// Plans are in tray order.
	Plans []Plan
}

type Plan struct {
	// ID is the service id, so a configured widget keeps its plan across
	// renames.
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Windows []Window `json:"windows"`
}

type Window struct {
	Label              *string
	LimitWindowSeconds *int64
	Secondary          bool
	UsedPercent        float64
	ResetAt            *time.Time
}

// NewSnapshot is the snapshot of plans read at readAt.
func NewSnapshot(plans []Plan, readAt time.Time, locale prefs.Locale, display prefs.QuotaDisplayMode) Snapshot {
	return Snapshot{
		Version:      SnapshotVersion,
		GeneratedAt:  readAt,
		Locale:       locale,
		QuotaDisplay: display,
		Plans:        slices.Clone(plans),
	}
}

// seconds is t in whole seconds: Foundation's ISO 8601 decoder rejects
// fractions.
func seconds(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
}

func (s Snapshot) MarshalJSON() ([]byte, error) {
	plans := s.Plans
	if plans == nil {
		plans = []Plan{}
	}
	return json.Marshal(struct {
		Version      int                    `json:"version"`
		GeneratedAt  string                 `json:"generated_at"`
		Locale       prefs.Locale           `json:"locale"`
		QuotaDisplay prefs.QuotaDisplayMode `json:"quota_display"`
		Plans        []Plan                 `json:"plans"`
	}{s.Version, seconds(s.GeneratedAt), s.Locale, s.QuotaDisplay, plans})
}

func (w Window) MarshalJSON() ([]byte, error) {
	var reset *string
	if w.ResetAt != nil {
		text := seconds(*w.ResetAt)
		reset = &text
	}
	return json.Marshal(struct {
		Label              *string `json:"label"`
		LimitWindowSeconds *int64  `json:"limit_window_seconds"`
		Secondary          bool    `json:"secondary"`
		UsedPercent        float64 `json:"used_percent"`
		ResetAt            *string `json:"reset_at"`
	}{w.Label, w.LimitWindowSeconds, w.Secondary, w.UsedPercent, reset})
}

func (p Plan) MarshalJSON() ([]byte, error) {
	windows := p.Windows
	if windows == nil {
		windows = []Window{}
	}
	type plan Plan
	copied := plan(p)
	copied.Windows = windows
	return json.Marshal(copied)
}

func samePointer[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func sameWindow(a, b Window) bool {
	return samePointer(a.Label, b.Label) && samePointer(a.LimitWindowSeconds, b.LimitWindowSeconds) &&
		a.Secondary == b.Secondary && a.UsedPercent == b.UsedPercent &&
		(a.ResetAt == nil) == (b.ResetAt == nil) && (a.ResetAt == nil || a.ResetAt.Equal(*b.ResetAt))
}

func samePlan(a, b Plan) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Kind == b.Kind && slices.EqualFunc(a.Windows, b.Windows, sameWindow)
}

// sameDisplay reports whether the widget would draw s and other alike,
// whenever their numbers were read.
func (s Snapshot) sameDisplay(other Snapshot) bool {
	return s.Version == other.Version && s.Locale == other.Locale && s.QuotaDisplay == other.QuotaDisplay &&
		slices.EqualFunc(s.Plans, other.Plans, samePlan)
}

func (s Snapshot) equal(other Snapshot) bool {
	return s.GeneratedAt.Equal(other.GeneratedAt) && s.sameDisplay(other)
}

// writeSnapshot replaces the snapshot in one rename, so the extension never
// reads half a file.
func writeSnapshot(directory string, s Snapshot) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	partial := filepath.Join(directory, fileName+".partial")
	if err := os.WriteFile(partial, data, 0o644); err != nil {
		return err
	}
	return os.Rename(partial, filepath.Join(directory, fileName))
}

type reloadKind int

const (
	reloadSkip reloadKind = iota
	reloadNow
	reloadAfter
)

// reloadFor decides when the widget redraws next: changed numbers redraw
// at once (spaced by reloadMinInterval); a read that only moved the time
// redraws when the widget would otherwise soon call its numbers stale.
// sinceReload is negative before the first reload.
func reloadFor(previous *Snapshot, next Snapshot, sinceReload time.Duration) (reloadKind, time.Duration) {
	changed := previous == nil || !previous.sameDisplay(next)
	switch {
	case sinceReload < 0:
		return reloadNow, 0
	case changed && sinceReload < reloadMinInterval:
		return reloadAfter, reloadMinInterval - sinceReload
	case changed, sinceReload >= freshnessReload:
		return reloadNow, 0
	}
	return reloadSkip, 0
}

// Bridge is the host's handle on WidgetKit, a Swift-only API.
type Bridge interface {
	// Reload asks WidgetKit to redraw AstrLink's widgets.
	Reload()
	// QueryPlaced starts a query for how many AstrLink widgets are on the
	// desktop; the answer arrives asynchronously.
	QueryPlaced()
	// Placed is the last answer to QueryPlaced; -1 before the first.
	Placed() int32
}

// Feed writes snapshots into a widget's container. A nil Feed feeds no
// widget.
type Feed struct {
	directory string
	// bridge is nil without the WidgetKit bridge: snapshots are still
	// written, for the widget's own timeline to pick up.
	bridge Bridge
	now    func() time.Time
	after  func(time.Duration, func())

	mu              sync.Mutex
	written         *Snapshot
	reloadedAt      time.Time
	reloadPending   bool
	placedQueriedAt time.Time
}

// NewFeed feeds the widget reading directory.
func NewFeed(directory string, bridge Bridge) *Feed {
	return &Feed{
		directory: directory,
		bridge:    bridge,
		now:       time.Now,
		after:     func(d time.Duration, f func()) { time.AfterFunc(d, f) },
	}
}

// Publish writes the snapshot for the widget if it changed, and asks
// WidgetKit to redraw.
func (f *Feed) Publish(s Snapshot) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.written != nil && f.written.equal(s) {
		return
	}
	if err := writeSnapshot(f.directory, s); err != nil {
		log.Printf("astrlink: unable to write the widget snapshot: %v", err)
		return
	}
	since := time.Duration(-1)
	if !f.reloadedAt.IsZero() {
		since = max(f.now().Sub(f.reloadedAt), 0)
	}
	kind, wait := reloadFor(f.written, s, since)
	written := s
	written.Plans = slices.Clone(s.Plans)
	f.written = &written
	switch {
	case f.bridge == nil:
	case kind == reloadNow:
		f.reloadedAt = f.now()
		f.bridge.Reload()
	case kind == reloadAfter && !f.reloadPending:
		f.reloadPending = true
		f.after(wait, func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.reloadPending = false
			f.reloadedAt = f.now()
			f.bridge.Reload()
		})
	}
}

// Placed reports whether an AstrLink widget is on the desktop. A placed
// widget shows plan figures like the menu-bar title does, so plans refresh
// on the same background cadence; without one, nothing extra is read.
func (f *Feed) Placed() bool {
	if f == nil || f.bridge == nil {
		return false
	}
	f.mu.Lock()
	now := f.now()
	query := f.placedQueriedAt.IsZero() || now.Sub(f.placedQueriedAt) >= placedQueryInterval
	if query {
		f.placedQueriedAt = now
	}
	f.mu.Unlock()
	if query {
		f.bridge.QueryPlaced()
	}
	return f.bridge.Placed() > 0
}

var (
	defaultOnce sync.Once
	defaultFeed *Feed
)

// Default is the feed of this build's widget, nil when the build has no App
// Group (off macOS, unbundled or unsigned).
func Default() *Feed {
	defaultOnce.Do(func() { defaultFeed = systemFeed() })
	return defaultFeed
}
