package widget

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

func at(t *testing.T, text string) *time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Fatal(err)
	}
	return &parsed
}

func plans(t *testing.T) []Plan {
	monthly := "Monthly"
	five, week := int64(18000), int64(604800)
	return []Plan{
		{ID: "svc_codex", Name: "Codex", Kind: "codex_subscription", Windows: []Window{
			// Upstream times can carry fractions; the file must not.
			{LimitWindowSeconds: &five, UsedPercent: 36, ResetAt: at(t, "2026-11-01T09:18:00.250Z")},
			{LimitWindowSeconds: &week, Secondary: true, UsedPercent: 59, ResetAt: at(t, "2026-11-04T01:00:00Z")},
		}},
		{ID: "svc_kimi", Name: "Kimi Coding", Kind: "kimi_coding", Windows: []Window{
			{Label: &monthly, UsedPercent: 21},
		}},
	}
}

func snapshot(t *testing.T) Snapshot {
	return NewSnapshot(plans(t), *at(t, "2026-11-01T07:20:00.900Z"), prefs.LocaleZhCN, prefs.QuotaRemaining)
}

func decode(t *testing.T, data []byte) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSnapshotMatchesTheExtensionFixture(t *testing.T) {
	// The Swift tests decode the same fixture (QuotaModelTests.swift).
	fixture, err := os.ReadFile(filepath.Join("..", "..", "macos-widget", "Tests", "snapshot-fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := decode(t, data), decode(t, fixture); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot\n%s\ndoes not match the fixture\n%s", data, fixture)
	}
	empty, _ := json.Marshal(NewSnapshot(nil, time.Unix(0, 0), prefs.LocaleEn, prefs.QuotaUsed))
	if string(empty) != `{"version":1,"generated_at":"1970-01-01T00:00:00Z","locale":"en","quota_display":"used","plans":[]}` {
		t.Fatalf("empty snapshot %s", empty)
	}
}

func TestSnapshotIsReplacedWhole(t *testing.T) {
	directory := t.TempDir()
	if err := writeSnapshot(directory, snapshot(t)); err != nil {
		t.Fatal(err)
	}
	next := snapshot(t)
	next.Plans = next.Plans[:1]
	if err := writeSnapshot(directory, next); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(directory, fileName))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(next)
	if !reflect.DeepEqual(decode(t, written), decode(t, want)) {
		t.Fatalf("written %s", written)
	}
	if _, err := os.Stat(filepath.Join(directory, fileName+".partial")); !os.IsNotExist(err) {
		t.Fatal("the partial file stayed")
	}
}

func TestReloadsFollowWhatTheWidgetWouldDraw(t *testing.T) {
	first := snapshot(t)
	laterRead := snapshot(t)
	laterRead.GeneratedAt = laterRead.GeneratedAt.Add(5 * time.Minute)
	changed := snapshot(t)
	changed.Plans[0].Windows[0].UsedPercent = 50
	english := snapshot(t)
	english.Locale = prefs.LocaleEn

	check := func(name string, previous *Snapshot, next Snapshot, since time.Duration, kind reloadKind, wait time.Duration) {
		t.Helper()
		if gotKind, gotWait := reloadFor(previous, next, since); gotKind != kind || gotWait != wait {
			t.Errorf("%s: %v after %v, want %v after %v", name, gotKind, gotWait, kind, wait)
		}
	}
	check("first", nil, first, -1, reloadNow, 0)
	check("changed", &first, changed, 5*time.Minute, reloadNow, 0)
	check("spaced", &first, english, 20*time.Second, reloadAfter, 40*time.Second)
	// Only the read time moved: wait until the stale mark draws near.
	check("read again", &first, laterRead, 5*time.Minute, reloadSkip, 0)
	check("nearly stale", &first, laterRead, freshnessReload, reloadNow, 0)
	// A reset time read with another fraction draws the same.
	same := snapshot(t)
	same.Plans[0].Windows[0].ResetAt = at(t, "2026-11-01T09:18:00.250Z")
	if !first.sameDisplay(same) || !first.equal(same) {
		t.Error("equal snapshots differ")
	}
}

// fakeBridge counts WidgetKit calls.
type fakeBridge struct {
	mu      sync.Mutex
	reloads int
	queries int
	placed  int32
}

func (b *fakeBridge) Reload()      { b.mu.Lock(); b.reloads++; b.mu.Unlock() }
func (b *fakeBridge) QueryPlaced() { b.mu.Lock(); b.queries++; b.mu.Unlock() }
func (b *fakeBridge) Placed() int32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.placed
}

func (b *fakeBridge) counts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reloads, b.queries
}

type clock struct{ now time.Time }

func testFeed(t *testing.T) (*Feed, *fakeBridge, *clock, *[]func()) {
	bridge := &fakeBridge{placed: -1}
	c := &clock{now: time.Date(2026, 11, 1, 7, 20, 0, 0, time.UTC)}
	var delayed []func()
	f := NewFeed(t.TempDir(), bridge)
	f.now = func() time.Time { return c.now }
	f.after = func(_ time.Duration, fn func()) { delayed = append(delayed, fn) }
	return f, bridge, c, &delayed
}

func TestFeedWritesChangesAndSpacesReloads(t *testing.T) {
	f, bridge, c, delayed := testFeed(t)
	first := snapshot(t)
	f.Publish(first)
	if reloads, _ := bridge.counts(); reloads != 1 {
		t.Fatalf("first publish reloaded %d times", reloads)
	}
	// The same snapshot again neither writes nor reloads.
	f.Publish(first)
	if reloads, _ := bridge.counts(); reloads != 1 || len(*delayed) != 0 {
		t.Fatalf("an unchanged snapshot reloaded")
	}
	// Two quick edits share one delayed reload.
	c.now = c.now.Add(20 * time.Second)
	english := snapshot(t)
	english.Locale = prefs.LocaleEn
	f.Publish(english)
	used := english
	used.QuotaDisplay = prefs.QuotaUsed
	f.Publish(used)
	if reloads, _ := bridge.counts(); reloads != 1 || len(*delayed) != 1 {
		t.Fatalf("edits reloaded %d times with %d pending", reloads, len(*delayed))
	}
	written, _ := os.ReadFile(filepath.Join(f.directory, fileName))
	want, _ := json.Marshal(used)
	if string(written) != string(want) {
		t.Fatalf("written %s", written)
	}
	c.now = c.now.Add(40 * time.Second)
	(*delayed)[0]()
	if reloads, _ := bridge.counts(); reloads != 2 {
		t.Fatal("the delayed reload did not run")
	}
	// After the delayed reload another edit waits again.
	c.now = c.now.Add(10 * time.Second)
	f.Publish(english)
	if len(*delayed) != 2 {
		t.Fatal("an edit right after a reload was not delayed")
	}
	// A new read of the same numbers waits for the freshness mark.
	c.now = c.now.Add(5 * time.Minute)
	read := english
	read.GeneratedAt = read.GeneratedAt.Add(5 * time.Minute)
	(*delayed)[1]()
	reloads, _ := bridge.counts()
	f.Publish(read)
	if after, _ := bridge.counts(); after != reloads {
		t.Fatal("a read that changed nothing reloaded")
	}
	c.now = c.now.Add(freshnessReload)
	read.GeneratedAt = read.GeneratedAt.Add(freshnessReload)
	f.Publish(read)
	if after, _ := bridge.counts(); after != reloads+1 {
		t.Fatal("a read near the stale mark did not reload")
	}
}

func TestPlacedAsksWidgetKitAtMostOnceAMinute(t *testing.T) {
	f, bridge, c, _ := testFeed(t)
	if f.Placed() {
		t.Fatal("placed before WidgetKit answered")
	}
	bridge.placed = 2
	if !f.Placed() {
		t.Fatal("not placed after WidgetKit answered")
	}
	if _, queries := bridge.counts(); queries != 1 {
		t.Fatalf("asked %d times", queries)
	}
	c.now = c.now.Add(placedQueryInterval)
	bridge.placed = 0
	if f.Placed() {
		t.Fatal("placed after the widget was removed")
	}
	if _, queries := bridge.counts(); queries != 2 {
		t.Fatalf("asked %d times", queries)
	}
}

func TestNoWidgetIsFedWithoutAnAppGroupOrBridge(t *testing.T) {
	// `go test` runs an unbundled binary with no App Group.
	if Default() != nil {
		t.Fatal("a test binary feeds a widget")
	}
	var none *Feed
	none.Publish(snapshot(t))
	if none.Placed() {
		t.Fatal("no feed reports a widget")
	}
	// Without the bridge the snapshot is still written.
	f := NewFeed(t.TempDir(), nil)
	f.Publish(snapshot(t))
	if f.Placed() {
		t.Fatal("placed without a bridge")
	}
	if _, err := os.Stat(filepath.Join(f.directory, fileName)); err != nil {
		t.Fatal(err)
	}
}
