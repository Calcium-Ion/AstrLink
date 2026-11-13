package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/core"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
	"github.com/QuantumNous/astrlink/desktop/internal/widget"
)

func TestOnlyLoginLaunchesWithBothPreferencesEnabledStayHidden(t *testing.T) {
	for _, autostart := range []bool{false, true} {
		for _, hidden := range []bool{false, true} {
			values := prefs.Preferences{Autostart: autostart, AutostartHidden: hidden}
			want := autostart && hidden
			if got := startHidden(values, nil, true); got != want {
				t.Fatalf("login item, %v/%v: %v", autostart, hidden, got)
			}
			// The login entries of the previous release passed --autostart.
			if got := startHidden(values, []string{LegacyAutostartArg}, false); got != want {
				t.Fatalf("legacy entry, %v/%v: %v", autostart, hidden, got)
			}
			// Each launch has its own arguments, so opening the app by hand
			// shows the window of an instance running in the tray.
			for _, args := range [][]string{nil, {"astrlink://v1/providers/import?data=x"}, {"--autostart=false"}} {
				if startHidden(values, args, false) {
					t.Fatalf("%v hid the window", args)
				}
			}
		}
	}
}

func TestTheCoreWatcherFollowsReadinessAndRawAccess(t *testing.T) {
	var w coreWatcher
	step := w.observe(core.View{Phase: core.PhaseSpawning})
	if step.openApproval || step.floating != nil || step.checkKey || step.warm || step.clear {
		t.Fatalf("starting: %+v", step)
	}
	ready := readyView()
	step = w.observe(ready)
	if !step.warm || !step.checkKey || step.clear {
		t.Fatalf("ready: %+v", step)
	}
	if step = w.observe(ready); step.warm || step.checkKey {
		t.Fatalf("still ready: %+v", step)
	}
	// A key event Core recorded is checked against the pin at once.
	ready.RawKeyEvent = &core.RawKeyEvent{Kind: core.RawKeyReset, At: "2026-11-13T08:00:00Z"}
	if step = w.observe(ready); !step.checkKey {
		t.Fatalf("key event: %+v", step)
	}
	// A request opens the window and floats it; another one opens it
	// again, without changing its level; none left lets it sink.
	ready.PendingRawAccess = 1
	if step = w.observe(ready); !step.openApproval || step.floating == nil || !*step.floating || step.checkKey {
		t.Fatalf("request: %+v", step)
	}
	ready.PendingRawAccess = 2
	if step = w.observe(ready); !step.openApproval || step.floating != nil {
		t.Fatalf("second request: %+v", step)
	}
	ready.PendingRawAccess = 0
	if step = w.observe(ready); step.openApproval || step.floating == nil || *step.floating {
		t.Fatalf("decided: %+v", step)
	}
	if step = w.observe(core.View{Phase: core.PhaseStopping}); !step.clear || step.warm {
		t.Fatalf("stopping: %+v", step)
	}
	if step = w.observe(core.View{Phase: core.PhaseStopped}); step.clear {
		t.Fatalf("stopped: %+v", step)
	}
}

func TestTheSchedulerRefreshesHiddenNumbersSlowerThanShownOnes(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	tray := &trayController{now: func() time.Time { return now }}
	if plans, ok := tray.tickPlan(true, prefs.MenubarNone); !ok || plans != ifOlderThan(planRefreshVisible) {
		t.Fatalf("visible: %+v %v", plans, ok)
	}
	// Nothing read yet: local numbers now, cached plans only.
	if plans, ok := tray.tickPlan(false, prefs.MenubarNone); !ok || plans.kind != planReuse {
		t.Fatalf("first: %+v %v", plans, ok)
	}
	tray.digestAt = now.Add(-10 * time.Second)
	if _, ok := tray.tickPlan(false, prefs.MenubarNone); ok {
		t.Fatal("fresh local numbers were read again while hidden")
	}
	tray.digestAt = now.Add(-localRefreshHidden)
	tray.subscriptionsAt = now.Add(-time.Minute)
	if plans, ok := tray.tickPlan(false, prefs.MenubarSubscription); !ok || plans.kind != planReuse {
		t.Fatalf("fresh plans: %+v %v", plans, ok)
	}
	// The menu bar shows plan figures: they follow the background cadence.
	tray.subscriptionsAt = now.Add(-planRefreshBackground)
	if plans, ok := tray.tickPlan(false, prefs.MenubarSubscription); !ok || plans != ifOlderThan(planRefreshBackground) {
		t.Fatalf("stale plans: %+v %v", plans, ok)
	}
	if plans, _ := tray.tickPlan(false, prefs.MenubarTokens); plans.kind != planReuse {
		t.Fatalf("tokens title: %+v", plans)
	}
	// A placed desktop widget shows plan figures just as the menu-bar
	// title does, so it gets the same background cadence.
	placed := &placedWidget{count: 1}
	tray.widget = widget.NewFeed(t.TempDir(), placed)
	if plans, ok := tray.tickPlan(false, prefs.MenubarNone); !ok || plans != ifOlderThan(planRefreshBackground) {
		t.Fatalf("placed widget: %+v %v", plans, ok)
	}
}

// placedWidget is WidgetKit with count widgets on the desktop.
type placedWidget struct{ count int32 }

func (*placedWidget) Reload()         {}
func (*placedWidget) QueryPlaced()    {}
func (w *placedWidget) Placed() int32 { return w.count }

// fakeCore answers the control API with fixed bodies by path prefix and
// counts the plan lookups.
type fakeCore struct {
	mu      sync.Mutex
	bodies  map[string]string
	fail    map[string]bool
	fresh   []string
	lookups int
}

func (f *fakeCore) Call(_ context.Context, req control.Request) (control.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path, query, _ := strings.Cut(req.Path, "?")
	if strings.HasSuffix(path, "/usage") {
		f.lookups++
		if strings.Contains(query, "refresh=1") {
			f.fresh = append(f.fresh, path)
		}
	}
	if f.fail[path] {
		return control.Response{}, errors.New("upstream unavailable")
	}
	body, ok := f.bodies[path]
	if !ok {
		return control.Response{Status: http.StatusNotFound, Body: json.RawMessage(`{"error":{"code":"not_found"}}`)}, nil
	}
	return control.Response{Status: http.StatusOK, Body: json.RawMessage(body)}, nil
}

func newFakeCore() *fakeCore {
	return &fakeCore{
		bodies: map[string]string{
			"/control/v1/usage-summary": `{"totals": {"requests": 10, "failed_requests": 1, "input_tokens": 500,
				"output_tokens": 100, "total_tokens": 600, "cache_read_tokens": 200, "cache_write_tokens": 0},
				"by_hour": [], "by_model": [{"id": "gpt-5", "requests": 10, "total_tokens": 600}]}`,
			"/control/v1/services": `{"items": [
				{"id": "svc_codex", "name": "Codex", "kind": "codex_subscription", "enabled": true},
				{"id": "svc_claude", "name": "Claude", "kind": "claude_subscription", "enabled": true},
				{"id": "svc_api", "name": "OpenAI", "kind": "openai", "enabled": true}
			], "next_cursor": null}`,
			"/control/v1/services/svc_codex/usage": `{"service_id": "svc_codex", "fetched_at": "2026-11-13T08:00:00Z",
				"primary": {"used_percent": 62.0, "limit_window_seconds": 18000}}`,
			"/control/v1/services/svc_claude/usage": `{"service_id": "svc_claude", "fetched_at": "2026-11-13T08:00:00Z",
				"primary": {"used_percent": 30.0, "limit_window_seconds": 18000}}`,
		},
		fail: map[string]bool{},
	}
}

func TestTheDigestReadsOnlyTheEnabledCardsAndCachesPlans(t *testing.T) {
	fake := newFakeCore()
	api := &control.API{Caller: fake}
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	usage := prefs.TrayUsagePreferences{Today: true, TopModel: true, SubscriptionWindows: true}

	digest, at := collectDigest(context.Background(), api, usage, nil, time.Time{}, ifOlderThan(planRefreshVisible), now)
	if digest.Today == nil || digest.Today.TotalTokens != 600 || digest.TopModel == nil || digest.CostToday != nil {
		t.Fatalf("local numbers: %+v", digest)
	}
	if len(digest.Subscriptions) != 2 || digest.Subscriptions[0].Name != "Codex" || digest.Subscriptions[1].Name != "Claude" ||
		digest.Subscriptions[0].ID != "svc_codex" || !at.Equal(now) {
		t.Fatalf("plans: %+v at %v", digest.Subscriptions, at)
	}
	if digest.subscriptionServices == nil || *digest.subscriptionServices != 2 || len(fake.fresh) != 0 {
		t.Fatalf("listed %v, fresh %v", digest.subscriptionServices, fake.fresh)
	}

	// Younger than the limit: the cached plans are kept.
	later := now.Add(time.Minute)
	cached, cachedAt := collectDigest(context.Background(), api, usage, digest.Subscriptions, at, ifOlderThan(planRefreshVisible), later)
	if fake.lookups != 2 || len(cached.Subscriptions) != 2 || !cachedAt.Equal(at) || cached.subscriptionServices != nil {
		t.Fatalf("cached: %d lookups, %+v at %v", fake.lookups, cached.Subscriptions, cachedAt)
	}
	// Reuse never calls upstream, even without a cache.
	if reused, reusedAt := collectDigest(context.Background(), api, usage, nil, time.Time{}, planRefresh{kind: planReuse}, later); fake.lookups != 2 ||
		len(reused.Subscriptions) != 0 || !reusedAt.IsZero() {
		t.Fatalf("reuse: %d lookups, %+v", fake.lookups, reused.Subscriptions)
	}
	// An explicit refresh asks Core to skip its snapshot; a failed plan is
	// left out until the next refresh succeeds.
	fake.fail["/control/v1/services/svc_claude/usage"] = true
	forced, _ := collectDigest(context.Background(), api, usage, digest.Subscriptions, at, planRefresh{kind: planForce}, later)
	if len(forced.Subscriptions) != 1 || forced.Subscriptions[0].Name != "Codex" || len(fake.fresh) != 2 {
		t.Fatalf("forced: %+v, fresh %v", forced.Subscriptions, fake.fresh)
	}
	// An empty read is not a cache: the next allowed read tries again.
	fake.fail["/control/v1/services/svc_codex/usage"] = true
	empty, emptyAt := collectDigest(context.Background(), api, usage, nil, time.Time{}, ifOlderThan(planRefreshVisible), later)
	if len(empty.Subscriptions) != 0 || !emptyAt.IsZero() || empty.Subscriptions == nil {
		t.Fatalf("empty: %+v at %v", empty.Subscriptions, emptyAt)
	}
}

func TestLinksWaitForTheirReceiver(t *testing.T) {
	var inbox linkInbox
	if inbox.deliver("astrlink://v1/providers/import?data=a") || inbox.deliver("astrlink://v1/providers/import?data=b") {
		t.Fatal("a link was delivered with no receiver")
	}
	// One link is confirmed at a time: the last one waits.
	if inbox.pending != "astrlink://v1/providers/import?data=b" {
		t.Fatalf("pending = %q", inbox.pending)
	}
	var got []string
	inbox.receive = func(link string) { got = append(got, link) }
	if !inbox.deliver("astrlink://v1/providers/import?data=c") || len(got) != 1 {
		t.Fatalf("delivered %v", got)
	}
}

func TestTheTrayPanelItemIsTranslated(t *testing.T) {
	for _, locale := range []prefs.Locale{prefs.LocaleEn, prefs.LocaleZhCN, prefs.LocaleJa} {
		if text := prefs.T(locale, "host.tray.panel"); text == "" || text == "host.tray.panel" {
			t.Fatalf("%s: %q", locale, text)
		}
	}
}

func TestEveryTrayIconIsEmbedded(t *testing.T) {
	for _, state := range []TrayIconState{TrayReady, TrayIdle, TrayWatched} {
		if png, _ := iconFor(state); len(png) == 0 {
			t.Fatalf("%s has no icon", state)
		}
		for _, name := range []string{"icons/mac-" + string(state) + ".png", "icons/color-" + string(state) + ".png"} {
			if data, err := icons.ReadFile(name); err != nil || len(data) == 0 {
				t.Fatalf("%s: %v", name, err)
			}
		}
	}
}
