package ui

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/core"
	"github.com/QuantumNous/astrlink/desktop/internal/currency"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

func ptr[T any](v T) *T { return &v }

func readyView() core.View {
	return core.View{
		Phase:        core.PhaseReady,
		InferenceURL: ptr("http://127.0.0.1:8317"),
		CoreVersion:  ptr("0.1.0"),
	}
}

func sampleDigest() UsageDigest {
	return UsageDigest{
		Today: &UsageTotals{
			Requests: 128, Failed: 3, TotalTokens: 1_230_000, InputTokens: 1_000_000, CacheReadTokens: 410_000,
		},
		HourlyTokens:    make([]int64, 24),
		YesterdayTokens: ptr[int64](1_000_000),
		TopModel:        &Share{Name: "claude-sonnet-4", Percent: 62},
		CostToday:       &CostDigest{AmountUSD: 0.834},
		Privacy: &PrivacyDigest{
			Enabled: ptr(true), Checked: 120, Redacted: 9, Blocked: 1, Replaced: 23, Restored: 17,
			Kinds: []PrivacyKindCount{{Kind: "email", Count: 14}},
		},
		Subscriptions: []SubscriptionDigest{{
			ID: "svc_codex", Name: "Codex", Kind: "codex_subscription",
			Windows: []WindowDigest{{LimitWindowSeconds: ptr[int64](18_000), UsedPercent: 62}},
		}},
	}
}

func model(view core.View, tray prefs.TrayPreferences, digest *UsageDigest, locale prefs.Locale) TrayModel {
	return trayModel(view, tray, digest, locale, prefs.QuotaRemaining, currency.USD(), false)
}

func TestIconAndTooltipFollowTheGatewayState(t *testing.T) {
	digest := sampleDigest()
	m := model(readyView(), prefs.DefaultTrayPreferences(), &digest, prefs.LocaleZhCN)
	if m.Icon != TrayReady || m.Title != nil || m.Tooltip != "AstrLink · 网关运行中 · 127.0.0.1:8317" {
		t.Fatalf("ready = %+v", m)
	}
	stopped := readyView()
	stopped.Phase, stopped.InferenceURL = core.PhaseStopped, nil
	if m := model(stopped, prefs.DefaultTrayPreferences(), nil, prefs.LocaleEn); m.Tooltip != "AstrLink · Gateway stopped" || m.Icon != TrayIdle {
		t.Fatalf("stopped = %+v", m)
	}
	starting := readyView()
	starting.Phase = core.PhaseHandshaking
	if m := model(starting, prefs.DefaultTrayPreferences(), nil, prefs.LocaleEn); m.Tooltip != "AstrLink · Gateway starting…" || m.Icon != TrayIdle {
		t.Fatalf("starting = %+v", m)
	}
}

func TestAnAgentReadingRecordsSwitchesToTheWatchedIcon(t *testing.T) {
	tray := prefs.DefaultTrayPreferences()
	watched := readyView()
	watched.ObserverActive = true
	m := model(watched, tray, nil, prefs.LocaleZhCN)
	if m.Icon != TrayWatched || m.Tooltip != "AstrLink · 网关运行中 · 127.0.0.1:8317 · Agent 正在通过 CLI 读取" {
		t.Fatalf("watched = %+v", m)
	}
	watched.ObserverReadLevel = ptr(core.ReadRaw)
	watched.PendingRawAccess = 2
	if m := model(watched, tray, nil, prefs.LocaleZhCN); m.Tooltip != "AstrLink · 网关运行中 · 127.0.0.1:8317 · Agent 正在读取已批准的原文 · 2 个原文申请待批准" {
		t.Fatalf("raw = %q", m.Tooltip)
	}
	waiting := readyView()
	waiting.PendingRawAccess = 1
	if m := model(waiting, tray, nil, prefs.LocaleEn); m.Icon != TrayWatched ||
		m.Tooltip != "AstrLink · Gateway running · 127.0.0.1:8317 · Raw access requests awaiting you: 1" {
		t.Fatalf("waiting = %+v", m)
	}
	// A running timed grant keeps the watched icon until it ends.
	granted := readyView()
	granted.ActiveRawGrants = 2
	if m := model(granted, tray, nil, prefs.LocaleEn); m.Icon != TrayWatched ||
		m.Tooltip != "AstrLink · Gateway running · 127.0.0.1:8317 · Agents that may read raw content: 2" {
		t.Fatalf("granted = %+v", m)
	}
	// A missing raw password points at the main window's setup without
	// pretending an agent is reading.
	gated := readyView()
	gated.RawPasswordRequired = true
	if m := model(gated, tray, nil, prefs.LocaleZhCN); m.Icon != TrayReady ||
		m.Tooltip != "AstrLink · 网关运行中 · 127.0.0.1:8317 · 请求原文还没有保护，请打开 AstrLink 设置" {
		t.Fatalf("gated = %+v", m)
	}
	gated.PendingRawAccess = 1
	if m := model(gated, tray, nil, prefs.LocaleEn); m.Icon != TrayWatched ||
		m.Tooltip != "AstrLink · Gateway running · 127.0.0.1:8317 · Raw access requests awaiting you: 1 · Raw request content is not protected yet; open AstrLink to set it up" {
		t.Fatalf("gated and waiting = %+v", m)
	}
	// A raw key replaced outside the desktop points at the main window's
	// warning; no agent is involved either.
	replaced := readyView()
	replaced.RawKeyReplaced = true
	if m := model(replaced, tray, nil, prefs.LocaleZhCN); m.Icon != TrayReady ||
		m.Tooltip != "AstrLink · 网关运行中 · 127.0.0.1:8317 · 原文密钥在 AstrLink 之外被更换，请打开 AstrLink 查看" {
		t.Fatalf("replaced = %+v", m)
	}
	// Only a running gateway can be read; the badge drops with it.
	watched.Phase = core.PhaseError
	if m := model(watched, tray, nil, prefs.LocaleZhCN); m.Icon != TrayIdle {
		t.Fatalf("failed = %+v", m)
	}
}

func TestAFailedGatewayShowsItsErrorInTheTooltipWithTheIdleIcon(t *testing.T) {
	view := core.View{
		Phase:             core.PhaseError,
		LastError:         ptr("astrlink-core exited with status 1 after the inference port was reported busy by the operating system, twice"),
		RecoveryAttempt:   2,
		RecoveryScheduled: true,
	}
	tray := prefs.DefaultTrayPreferences()
	tray.MenubarText = prefs.MenubarTokens
	for _, mac := range []bool{false, true} {
		m := trayModel(view, tray, nil, prefs.LocaleZhCN, prefs.QuotaRemaining, currency.USD(), mac)
		if !strings.HasPrefix(m.Tooltip, "AstrLink · 网关异常退出 · astrlink-core") || !strings.Contains(m.Tooltip, "…") ||
			m.Icon != TrayIdle || m.Title == nil || *m.Title != "!" {
			t.Fatalf("model = %+v", m)
		}
		// The alert mark is the status-item title on macOS; every other
		// platform has no title slot and folds it into the tooltip.
		if mac && !strings.HasSuffix(m.Tooltip, "…") || !mac && !strings.HasSuffix(m.Tooltip, "… · !") {
			t.Fatalf("mac %v tooltip = %q", mac, m.Tooltip)
		}
	}
}

func TestAPortFallbackIsNamedInTheTooltip(t *testing.T) {
	view := readyView()
	view.InferenceURL = ptr("http://127.0.0.1:8324")
	view.InferencePortFallback = &core.InferencePortFallback{RequestedPort: 8317, ActivePort: 8324}
	if m := model(view, prefs.DefaultTrayPreferences(), nil, prefs.LocaleZhCN); m.Tooltip != "AstrLink · 网关运行中 · 127.0.0.1:8324 · 8317 被占用，已改用 8324" {
		t.Fatalf("tooltip = %q", m.Tooltip)
	}
}

func title(m TrayModel) string {
	if m.Title == nil {
		return "<nil>"
	}
	return *m.Title
}

func TestTheMenubarTitleFollowsThePreference(t *testing.T) {
	digest := sampleDigest()
	for choice, want := range map[prefs.TrayMenubarText]string{
		prefs.MenubarNone: "<nil>", prefs.MenubarRequests: "128", prefs.MenubarTokens: "1.2M",
		prefs.MenubarCost: "$0.83", prefs.MenubarSubscription: "38%", prefs.MenubarAlertOnly: "<nil>",
	} {
		tray := prefs.DefaultTrayPreferences()
		tray.MenubarText = choice
		if got := title(model(readyView(), tray, &digest, prefs.LocaleEn)); got != want {
			t.Fatalf("%s: title = %s, want %s", choice, got, want)
		}
	}
	// Without a digest there is nothing to show yet.
	tray := prefs.DefaultTrayPreferences()
	tray.MenubarText = prefs.MenubarTokens
	if got := title(model(readyView(), tray, nil, prefs.LocaleEn)); got != "<nil>" {
		t.Fatalf("title = %s", got)
	}
}

func TestTheSubscriptionTitleTracksTheMostDepletedWindowInBothModes(t *testing.T) {
	tray := prefs.DefaultTrayPreferences()
	tray.MenubarText = prefs.MenubarSubscription
	usage := sampleDigest()
	second := usage.Subscriptions[0]
	second.Windows = []WindowDigest{second.Windows[0]}
	second.Windows[0].UsedPercent = 87
	usage.Subscriptions = append(usage.Subscriptions, second)
	for mode, want := range map[prefs.QuotaDisplayMode]string{prefs.QuotaRemaining: "13%", prefs.QuotaUsed: "87%"} {
		if got := title(trayModel(readyView(), tray, &usage, prefs.LocaleEn, mode, currency.USD(), false)); got != want {
			t.Fatalf("%s: title = %s", mode, got)
		}
	}
	usage.Subscriptions[1].Windows[0].UsedPercent = 120
	if got := title(model(readyView(), tray, &usage, prefs.LocaleEn)); got != "0%" {
		t.Fatalf("title = %s", got)
	}
	if got := title(model(readyView(), tray, nil, prefs.LocaleEn)); got != "<nil>" {
		t.Fatalf("title = %s", got)
	}
}

func TestTheMenubarFigureIsCollectedEvenWithItsCardOff(t *testing.T) {
	off := prefs.DefaultTrayPreferences().Usage
	off.Today, off.Cost, off.SubscriptionWindows, off.TopModel = false, false, false, false
	collect := func(text prefs.TrayMenubarText) prefs.TrayUsagePreferences {
		tray := prefs.DefaultTrayPreferences()
		tray.MenubarText, tray.Usage = text, off
		return collectedUsage(tray, false)
	}
	if !collect(prefs.MenubarRequests).Today || !collect(prefs.MenubarTokens).Today ||
		!collect(prefs.MenubarCost).Cost || !collect(prefs.MenubarSubscription).SubscriptionWindows {
		t.Fatal("a menu bar figure was not collected")
	}
	if collect(prefs.MenubarNone) != off || collect(prefs.MenubarAlertOnly) != off {
		t.Fatal("a choice without a figure collected more")
	}
	// A desktop widget reads plan windows like the menu-bar figure does.
	tray := prefs.DefaultTrayPreferences()
	tray.MenubarText, tray.Usage = prefs.MenubarNone, off
	if widget := collectedUsage(tray, true); !widget.SubscriptionWindows || widget.Today {
		t.Fatalf("widget collected %+v", widget)
	}
}

func TestTheWidgetKeepsItsPlansThroughCachedAndFailedReads(t *testing.T) {
	earlier := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	now := earlier.Add(2 * time.Minute)
	listed, none := 2, 0
	read := UsageDigest{Subscriptions: []SubscriptionDigest{{ID: "svc", Name: "Codex", Kind: "codex_subscription"}}, subscriptionServices: &listed}
	if plans, ok := widgetRead(read, earlier, now); !ok || len(plans) != 1 || plans[0].ID != "svc" {
		t.Fatalf("new read: %v %v", plans, ok)
	}
	// A cached read keeps the instant it was read at.
	if _, ok := widgetRead(read, earlier, earlier); ok {
		t.Fatal("a cached read reached the widget")
	}
	// Lookups failed: plans were listed, none came back.
	if _, ok := widgetRead(UsageDigest{subscriptionServices: &listed}, earlier, time.Time{}); ok {
		t.Fatal("a failed read cleared the widget")
	}
	// Core lists no plan-backed service at all.
	if plans, ok := widgetRead(UsageDigest{subscriptionServices: &none}, earlier, time.Time{}); !ok || plans == nil || len(plans) != 0 {
		t.Fatalf("no plans: %v %v", plans, ok)
	}
	label, seconds := "Monthly", int64(18000)
	reset := now.Add(time.Hour)
	converted := widgetPlans([]SubscriptionDigest{{ID: "svc", Name: "Kimi", Kind: "kimi_coding", Windows: []WindowDigest{
		{Label: &label, LimitWindowSeconds: &seconds, Secondary: true, UsedPercent: 21, ResetAt: &reset},
	}}})
	if len(converted) != 1 || converted[0].ID != "svc" || *converted[0].Windows[0].Label != "Monthly" ||
		*converted[0].Windows[0].LimitWindowSeconds != 18000 || !converted[0].Windows[0].Secondary ||
		converted[0].Windows[0].UsedPercent != 21 || !converted[0].Windows[0].ResetAt.Equal(reset) {
		t.Fatalf("converted %+v", converted)
	}
}

func TestFormattingHelpers(t *testing.T) {
	for tokens, want := range map[int64]string{
		912: "912", 1_230: "1.2K", 348_000: "348K", 1_200_000: "1.2M", 48_000_000: "48M", 2_000_000_000: "2B", -5: "0",
	} {
		if got := formatTokens(tokens); got != want {
			t.Fatalf("formatTokens(%d) = %s, want %s", tokens, got, want)
		}
	}
	if displayAddress("http://127.0.0.1:8317") != "127.0.0.1:8317" || percentOf(41, 100) != 41 || percentOf(1, 0) != 0 ||
		truncate("abc", 3) != "abc" || truncate("abcd", 3) != "ab…" {
		t.Fatal("a helper is wrong")
	}
}

func obj(t *testing.T, text string) object {
	t.Helper()
	o, ok := objectOf(json.RawMessage(text))
	if !ok {
		t.Fatalf("not an object: %s", text)
	}
	return o
}

func TestDigestsParseControlAPIPayloads(t *testing.T) {
	summary := obj(t, `{
		"totals": {"requests": 10, "failed_requests": 1, "input_tokens": 500, "output_tokens": 100,
		           "total_tokens": 600, "cache_read_tokens": 200, "cache_write_tokens": 0},
		"by_hour": [
			{"date": "2026-09-22", "hour": 9, "total_tokens": 250},
			{"date": "2026-09-22", "hour": 14, "total_tokens": 350}
		],
		"by_model": [
			{"id": null, "requests": 1, "total_tokens": 50},
			{"id": "gpt-5", "requests": 9, "total_tokens": 550}
		]
	}`)
	totals := totalsFrom(summary)
	if totals == nil || totals.Requests != 10 || totals.CacheReadTokens != 200 {
		t.Fatalf("totals = %+v", totals)
	}
	hourly := hourlyFrom(summary)
	var sum int64
	for _, tokens := range hourly {
		sum += tokens
	}
	if len(hourly) != 24 || hourly[9] != 250 || hourly[14] != 350 || sum != 600 {
		t.Fatalf("hourly = %v", hourly)
	}
	if got := hourlyFrom(obj(t, `{"totals": {}}`)); len(got) != 0 {
		t.Fatalf("hourly without buckets = %v", got)
	}
	if top := topGroup(summary, totals.TotalTokens); top == nil || top.Name != "gpt-5" || top.Percent != 92 {
		t.Fatalf("top = %+v", top)
	}

	cost := costFrom(obj(t, `{"amount_usd": "0.834000000", "unpriced": 2}`))
	if cost == nil || cost.Unpriced != 2 || math.Abs(cost.AmountUSD-0.834) > 1e-9 {
		t.Fatalf("cost = %+v", cost)
	}

	tokens := obj(t, `{"items": [{"id": "tok_a", "name": "Cursor"}, {"id": "tok_b", "name": "Codex CLI"}]}`)
	usage := obj(t, `{"items": [
		{"token_id": "tok_a", "today_tokens": 700, "total_tokens": 1000},
		{"token_id": "tok_b", "today_tokens": 300, "total_tokens": 1000}
	]}`)
	if client := topClientFrom(tokens, usage); client == nil || client.Name != "Cursor" || client.Percent != 70 {
		t.Fatalf("client = %+v", client)
	}
	if idle := topClientFrom(tokens, obj(t, `{"items": [{"token_id": "tok_a", "today_tokens": 0, "total_tokens": 1}]}`)); idle != nil {
		t.Fatalf("idle client = %+v", idle)
	}

	privacy := obj(t, `{
		"totals": {"requests": 40, "allowed": 27, "ignored": 3, "warned": 1, "redacted": 6,
		           "blocked": 2, "fail_open": 1, "replaced": 11, "restored": 7,
		           "restored_visible": 5, "restored_tool_arguments": 2, "unrestored": 1,
		           "ignored_allowlisted": 4, "ignored_low_confidence": 0},
		"by_day": [], "by_hour": [],
		"by_kind": [{"kind": "email", "count": 8}, {"kind": "phone", "count": 3}]
	}`)
	digest := privacyFrom(privacy, ptr(false))
	// Off and failed checks are calls, not checks; ignored calls were
	// checked.
	if digest == nil || *digest.Enabled || digest.Checked != 39 || digest.Unchecked != 1 ||
		digest.Replaced != 11 || digest.Restored != 7 || digest.Unrestored != 1 ||
		len(digest.Kinds) != 2 || digest.Kinds[0].Kind != "email" {
		t.Fatalf("privacy = %+v", digest)
	}
	if privacyFrom(obj(t, `{"totals": {}}`), nil) != nil {
		t.Fatal("an empty privacy summary parsed")
	}

	last := lastRequestFrom(obj(t, `{"items": [{
		"started_at": "2026-09-22T10:00:00Z", "requested_model": "gpt-5",
		"latency_ms": 2100, "status": "failed"
	}]}`))
	if last == nil || !last.Failed || *last.LatencyMS != 2100 || *last.Model != "gpt-5" {
		t.Fatalf("last = %+v", last)
	}

	services := subscriptionServices([]json.RawMessage{
		json.RawMessage(`{"id": "svc_codex", "name": "Codex", "kind": "codex_subscription", "enabled": true}`),
		json.RawMessage(`{"id": "svc_off", "name": "Off", "kind": "claude_subscription", "enabled": false}`),
		json.RawMessage(`{"id": "svc_api", "name": "OpenAI", "kind": "openai", "enabled": true}`),
	})
	if !reflect.DeepEqual(services, []subscriptionService{
		{"svc_codex", "Codex", "codex_subscription"}, {"svc_off", "Off", "claude_subscription"},
	}) {
		t.Fatalf("services = %+v", services)
	}

	plan := subscriptionFrom("Codex", "codex_subscription", obj(t, `{
		"service_id": "svc_codex", "fetched_at": "2026-09-22T10:00:00Z",
		"primary": {"used_percent": 62.0, "limit_window_seconds": 18000, "reset_at": "2026-09-22T12:13:00Z"},
		"secondary": {"used_percent": 18.0, "limit_window_seconds": 604800}
	}`))
	if plan == nil || plan.Kind != "codex_subscription" || len(plan.Windows) != 2 || !plan.Windows[1].Secondary ||
		plan.Windows[0].ResetAt == nil || plan.Windows[0].Label != nil {
		t.Fatalf("plan = %+v", plan)
	}
	if subscriptionFrom("Codex", "codex_subscription", obj(t, `{"service_id": "x"}`)) != nil {
		t.Fatal("a plan without windows parsed")
	}

	// Kimi-style plans report only a named monthly limit; Claude adds
	// per-model weekly caps next to its primary pair.
	monthly := subscriptionFrom("Kimi", "kimi_coding", obj(t, `{
		"service_id": "svc_kimi", "fetched_at": "2026-09-22T10:00:00Z",
		"additional_rate_limits": [
			{"limit_name": "Monthly", "metered_feature": "monthly",
			 "primary": {"used_percent": 41.5, "limit_window_seconds": 2592000}}
		]
	}`))
	if monthly == nil || len(monthly.Windows) != 1 || *monthly.Windows[0].Label != "Monthly" || monthly.Windows[0].UsedPercent != 41.5 {
		t.Fatalf("monthly = %+v", monthly)
	}
	claude := subscriptionFrom("Claude", "claude_subscription", obj(t, `{
		"service_id": "svc_claude", "fetched_at": "2026-09-22T10:00:00Z",
		"primary": {"used_percent": 30.0, "limit_window_seconds": 18000},
		"secondary": {"used_percent": 12.0, "limit_window_seconds": 604800},
		"additional_rate_limits": [
			{"limit_name": "Opus", "primary": {"used_percent": 70.0, "limit_window_seconds": 604800}},
			{"limit_name": "  ", "primary": {"used_percent": 1.0}},
			{"limit_name": "Extra usage", "secondary": {"used_percent": 5.0}}
		]
	}`))
	var labels []string
	for _, window := range claude.Windows {
		labels = append(labels, title(TrayModel{Title: window.Label}))
	}
	if !reflect.DeepEqual(labels, []string{"<nil>", "<nil>", "Opus", "<nil>", "Extra usage"}) {
		t.Fatalf("labels = %v", labels)
	}
}

func TestTheSnapshotSerializesWithTheFrontendContract(t *testing.T) {
	digest := sampleDigest()
	snapshot := TrayStateSnapshot{
		AppVersion: "0.1.0", Platform: "macos", View: readyView(), Digest: &digest,
		DigestAgeMS: ptr[uint64](1200), Tray: prefs.DefaultTrayPreferences(), PopoverBelow: true,
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		View   map[string]any `json:"view"`
		Digest struct {
			Today         map[string]any   `json:"today"`
			Privacy       map[string]any   `json:"privacy"`
			HourlyTokens  []any            `json:"hourly_tokens"`
			Subscriptions []map[string]any `json:"subscriptions"`
		} `json:"digest"`
		Tray         map[string]any `json:"tray"`
		PopoverBelow bool           `json:"popover_below"`
	}
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	view := value.View
	if view["phase"] != "ready" || view["observer_active"] != false || view["observer_read_level"] != nil ||
		view["pending_raw_access"] != 0.0 || view["raw_password_required"] != false || view["raw_key_event"] != nil ||
		view["raw_key_replaced"] != false || view["inference_url"] != "http://127.0.0.1:8317" || !value.PopoverBelow {
		t.Fatalf("view = %v", view)
	}
	if value.Digest.Today["requests"] != 128.0 || value.Digest.Privacy["enabled"] != true || value.Digest.Privacy["replaced"] != 23.0 ||
		len(value.Digest.HourlyTokens) != 24 || value.Digest.Subscriptions[0]["kind"] != "codex_subscription" ||
		value.Tray["pages"].([]any)[0] != "records" {
		t.Fatalf("snapshot = %s", encoded)
	}
	window := value.Digest.Subscriptions[0]["windows"].([]any)[0].(map[string]any)
	if window["used_percent"] != 62.0 || strings.Contains(string(encoded), "svc_codex") {
		t.Fatalf("subscription = %v", value.Digest.Subscriptions[0])
	}
	// An empty digest still sends lists, which the page maps over.
	empty, _ := json.Marshal(newDigest())
	if !strings.Contains(string(empty), `"hourly_tokens":[]`) || !strings.Contains(string(empty), `"subscriptions":[]`) {
		t.Fatalf("empty digest = %s", empty)
	}
}

func TestTrayActionsParseOnlyTheirOwnFields(t *testing.T) {
	for text, want := range map[string]TrayAction{
		`{"kind": "core", "op": "restart"}`:           {Kind: TrayCore, Op: CoreRestart},
		`{"kind": "navigate", "page": "records"}`:     {Kind: TrayNavigate, Page: "records"},
		`{"kind": "navigate", "page": "failedToday"}`: {Kind: TrayNavigate, Page: "failedToday"},
		`{"kind": "raw_access"}`:                      {Kind: TrayRawAccess},
		`{"kind": "copy_address"}`:                    {Kind: TrayCopyAddress},
	} {
		var action TrayAction
		if err := json.Unmarshal([]byte(text), &action); err != nil || action != want {
			t.Fatalf("%s = %+v, %v", text, action, err)
		}
	}
	for _, text := range []string{
		`{"kind": "explode"}`, `{"kind": "open", "page": "records"}`, `{"kind": "core", "op": "explode"}`,
		`{"kind": "navigate"}`, `{}`, `[]`,
	} {
		var action TrayAction
		if err := json.Unmarshal([]byte(text), &action); err == nil {
			t.Fatalf("%s parsed", text)
		}
	}
}

func TestNavigationAcceptsOnlyKnownPages(t *testing.T) {
	for _, page := range []string{"records", "failedToday", "privacyOverview", "settings", "agentTools", "desktopToys"} {
		if err := checkNavigation(page); err != nil {
			t.Fatalf("%s: %v", page, err)
		}
	}
	for _, page := range []string{"", "explode", "Records", "records?status=failed"} {
		if checkNavigation(page) == nil {
			t.Fatalf("%q was accepted", page)
		}
	}
}

func TestLocalWindowsAreWholeSecondsInUTC(t *testing.T) {
	zone := time.FixedZone("test", 8*3600)
	windows := localWindowsAt(time.Date(2026, 3, 1, 0, 30, 0, 5, zone), "Asia/Shanghai")
	if windows.todayFrom != "2026-02-28T16:00:00Z" || windows.todayTo != "2026-03-01T16:00:00Z" ||
		windows.yesterdayFrom != "2026-02-27T16:00:00Z" || windows.monthFrom != "2026-02-28T16:00:00Z" ||
		windows.timeZone != "Asia/Shanghai" {
		t.Fatalf("windows = %+v", windows)
	}
	if fixedZone(time.Date(2026, 1, 1, 0, 0, 0, 0, zone)) != "Etc/GMT-8" ||
		fixedZone(time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("x", -5*3600))) != "Etc/GMT+5" ||
		fixedZone(time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("x", 5*3600+1800))) != "UTC" {
		t.Fatal("fixed zones are wrong")
	}
	if _, err := time.LoadLocation(localTimeZone()); err != nil {
		t.Fatal(err)
	}
}

func TestThePopoverGrowsWithTheWindowsTextSize(t *testing.T) {
	// 150% display scale and 110% text size: WebView2 draws at 1.65.
	if width, height := popoverSize(400, 1.65, 1.5); math.Abs(width-PopoverWidth*1.1) > 1e-9 || math.Abs(height-440) > 1e-9 {
		t.Fatalf("size = %v × %v", width, height)
	}
	// Elsewhere CSS pixels are the window's DIPs.
	for _, c := range [][2]float64{{2, 2}, {1, 2}, {math.NaN(), 1}, {1, 0}} {
		if width, height := popoverSize(400, c[0], c[1]); width != PopoverWidth || height != 400 {
			t.Fatalf("%v: size = %v × %v", c, width, height)
		}
	}
	if width, height := popoverSize(400, 10, 1); width != PopoverWidth*popoverMaxZoom || height != 400*popoverMaxZoom {
		t.Fatalf("size = %v × %v", width, height)
	}
}

func TestThePopoverHangsBelowAMenuBarIconAndRisesAboveATaskbarOne(t *testing.T) {
	menuBar := Box{X: 0, Y: 37, Width: 1440, Height: 863}
	if !anchorBelow(Box{X: 1200, Y: 0, Width: 30, Height: 37}, menuBar) {
		t.Fatal("a menu bar icon got the panel above it")
	}
	taskbar := Box{X: 0, Y: 0, Width: 1920, Height: 1032}
	if anchorBelow(Box{X: 1700, Y: 1040, Width: 24, Height: 24}, taskbar) {
		t.Fatal("a taskbar icon got the panel below it")
	}
}
