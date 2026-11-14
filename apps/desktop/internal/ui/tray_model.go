package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/desktop/internal/core"
	"github.com/QuantumNous/astrlink/desktop/internal/currency"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// The tray is the dashboard for a hidden window: a custom-drawn popover
// (its own web page, rendered by the app's React design system) next to the
// icon. It reads the Core view, the tray preferences and a UsageDigest the
// tray collects on a timer while the gateway is ready. The icon, tooltip and
// macOS status-item title are derived from the same state by trayModel.

const (
	maxErrorChars = 80
	// maxSubscriptionServices bounds the upstream usage lookups one
	// refresh can trigger.
	maxSubscriptionServices = 8
	// maxWindowsPerSubscription is the primary, secondary and a handful of
	// named limits per plan.
	maxWindowsPerSubscription = 6
	maxWindowLabelChars       = 40
	// maxPrivacyKinds: Core names ten kinds today; the bound leaves room for
	// newer ones.
	maxPrivacyKinds = 16

	// PopoverWidth is the popover page's width in CSS pixels,
	// TRAY_POPOVER_WIDTH in the frontend.
	PopoverWidth         = 360.0
	popoverInitialHeight = 320.0
	// popoverMaxZoom: Windows text size tops out at 225%; anything beyond
	// is a bogus report.
	popoverMaxZoom   = 3.0
	popoverMinHeight = 96.0
	// popoverMaxHeight is the bound before the work area itself clamps the
	// window; a folded subscription list of ten rows plus today's numbers
	// fits under it.
	popoverMaxHeight = 960.0
)

var subscriptionKinds = []string{
	"codex_subscription",
	"claude_subscription",
	"grok_subscription",
	"antigravity_subscription",
	"copilot_subscription",
	"droid_subscription",
	"kimi_coding",
	"glm_coding",
	"minimax_coding",
	"opencode_go",
}

var navigationKinds = []string{
	"overview",
	"list",
	"tokens",
	"safety",
	"records",
	// Request records narrowed to the calls behind today's failure count.
	"failedToday",
	// The safety page opened on its privacy statistics.
	"privacyOverview",
	"routing",
	"agentTools",
	"settings",
	// The desktop pets page.
	"desktopToys",
}

// TrayIconState is the icon's state image.
type TrayIconState string

const (
	TrayReady TrayIconState = "ready"
	// TrayIdle is anything but a running gateway: stopped, starting,
	// stopping, failed.
	TrayIdle TrayIconState = "idle"
	// TrayWatched is ready, and an agent is reading records through the
	// agent CLI.
	TrayWatched TrayIconState = "watched"
)

// TrayModel is what the native tray icon itself shows: its state image, the
// macOS status-item title and the tooltip.
type TrayModel struct {
	Icon    TrayIconState
	Title   *string
	Tooltip string
}

func (m TrayModel) equal(other TrayModel) bool {
	return m.Icon == other.Icon && m.Tooltip == other.Tooltip &&
		(m.Title == nil) == (other.Title == nil) && (m.Title == nil || *m.Title == *other.Title)
}

type UsageTotals struct {
	Requests        int64 `json:"requests"`
	Failed          int64 `json:"failed"`
	TotalTokens     int64 `json:"total_tokens"`
	InputTokens     int64 `json:"input_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
}

type Share struct {
	Name    string `json:"name"`
	Percent uint8  `json:"percent"`
}

type CostDigest struct {
	AmountUSD float64 `json:"amount_usd"`
	Unpriced  int64   `json:"unpriced"`
}

type LastRequest struct {
	StartedAt time.Time `json:"started_at"`
	Model     *string   `json:"model"`
	LatencyMS *int64    `json:"latency_ms"`
	Failed    bool      `json:"failed"`
}

type WindowDigest struct {
	// Label is a provider-named limit (Kimi "Monthly", Claude "Opus"); nil
	// for the primary/secondary windows, which the panel labels by their
	// length.
	Label              *string    `json:"label"`
	LimitWindowSeconds *int64     `json:"limit_window_seconds"`
	Secondary          bool       `json:"secondary"`
	UsedPercent        float64    `json:"used_percent"`
	ResetAt            *time.Time `json:"reset_at"`
}

type SubscriptionDigest struct {
	// ID is the service id; the desktop widget remembers a chosen plan by
	// it.
	ID   string `json:"-"`
	Name string `json:"name"`
	// Kind is one of subscriptionKinds; the panel draws its logo.
	Kind    string         `json:"kind"`
	Windows []WindowDigest `json:"windows"`
}

type PrivacyKindCount struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

// PrivacyDigest is what the privacy policy did to today's calls. Values and
// placeholders never reach the host; these are counts only.
type PrivacyDigest struct {
	// Enabled is the policy switch right now; nil when it could not be
	// read.
	Enabled *bool `json:"enabled"`
	// Checked counts the calls the policy finished checking.
	Checked  int64 `json:"checked"`
	Redacted int64 `json:"redacted"`
	Warned   int64 `json:"warned"`
	Blocked  int64 `json:"blocked"`
	// Unchecked counts calls that went upstream unchecked because the check
	// failed.
	Unchecked  int64 `json:"unchecked"`
	Replaced   int64 `json:"replaced"`
	Restored   int64 `json:"restored"`
	Unrestored int64 `json:"unrestored"`
	// Kinds found, the most frequent first.
	Kinds []PrivacyKindCount `json:"kinds"`
}

// UsageDigest is everything the popover's usage cards need, collected in
// one pass. Each field is independent: a failed lookup hides its card, not
// the whole panel.
type UsageDigest struct {
	Today *UsageTotals `json:"today"`
	// HourlyTokens is tokens per local hour of today, 24 slots, empty when
	// unknown.
	HourlyTokens    []int64              `json:"hourly_tokens"`
	YesterdayTokens *int64               `json:"yesterday_tokens"`
	TopModel        *Share               `json:"top_model"`
	CostToday       *CostDigest          `json:"cost_today"`
	Privacy         *PrivacyDigest       `json:"privacy"`
	TopClient       *Share               `json:"top_client"`
	LastRequest     *LastRequest         `json:"last_request"`
	MonthTokens     *int64               `json:"month_tokens"`
	Subscriptions   []SubscriptionDigest `json:"subscriptions"`
	// subscriptionServices is how many plan-backed services Core listed
	// when plans were read just now; nil for a cached or skipped read. Zero
	// tells "no plans" apart from lookups that failed.
	subscriptionServices *int
}

func newDigest() UsageDigest {
	return UsageDigest{HourlyTokens: []int64{}, Subscriptions: []SubscriptionDigest{}}
}

// TrayStateSnapshot is what the popover (and the settings preview) renders
// from.
type TrayStateSnapshot struct {
	AppVersion  string                `json:"app_version"`
	Platform    string                `json:"platform"`
	View        core.View             `json:"view"`
	Digest      *UsageDigest          `json:"digest"`
	DigestAgeMS *uint64               `json:"digest_age_ms"`
	Tray        prefs.TrayPreferences `json:"tray"`
	// PopoverBelow is whether the popover hangs below its anchor (menu
	// bar) or rises above it (taskbar); the panel keeps its shadow padding
	// on the far side.
	PopoverBelow bool `json:"popover_below"`
}

// TrayActionKind is what the popover asks the host to do.
type TrayActionKind string

const (
	TrayOpen        TrayActionKind = "open"
	TrayNavigate    TrayActionKind = "navigate"
	TrayCopyAddress TrayActionKind = "copy_address"
	TrayCore        TrayActionKind = "core"
	TrayRefresh     TrayActionKind = "refresh"
	// TrayRawAccess opens the raw access approval window, which lists
	// pending requests and running grants.
	TrayRawAccess TrayActionKind = "raw_access"
	TrayQuit      TrayActionKind = "quit"
)

// CoreOp is a gateway control of the popover.
type CoreOp string

const (
	CoreStart   CoreOp = "start"
	CoreStop    CoreOp = "stop"
	CoreRestart CoreOp = "restart"
)

// TrayAction is an action the popover asks the host to perform:
// {"kind": "navigate", "page": "records"}, {"kind": "core", "op": "stop"}.
type TrayAction struct {
	Kind TrayActionKind `json:"kind"`
	Page string         `json:"page,omitempty"`
	Op   CoreOp         `json:"op,omitempty"`
}

// UnmarshalJSON accepts only the fields of the action's kind.
func (a *TrayAction) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var kind TrayActionKind
	if err := json.Unmarshal(fields["kind"], &kind); err != nil {
		return errors.New("tray action needs a kind")
	}
	allowed := map[TrayActionKind][]string{
		TrayOpen: {}, TrayNavigate: {"page"}, TrayCopyAddress: {}, TrayCore: {"op"},
		TrayRefresh: {}, TrayRawAccess: {}, TrayQuit: {},
	}
	names, ok := allowed[kind]
	if !ok {
		return fmt.Errorf("unknown tray action %q", kind)
	}
	for name := range fields {
		if name != "kind" && !slices.Contains(names, name) {
			return fmt.Errorf("unknown field %q of tray action %q", name, kind)
		}
	}
	action := TrayAction{Kind: kind}
	switch kind {
	case TrayNavigate:
		if err := json.Unmarshal(fields["page"], &action.Page); err != nil {
			return errors.New("tray navigation needs a page")
		}
	case TrayCore:
		if err := json.Unmarshal(fields["op"], &action.Op); err != nil {
			return errors.New("tray gateway control needs an op")
		}
		if action.Op != CoreStart && action.Op != CoreStop && action.Op != CoreRestart {
			return fmt.Errorf("unknown gateway control %q", action.Op)
		}
	}
	*a = action
	return nil
}

// trayModel derives what the icon shows. mac folds the menu bar title into
// the tooltip elsewhere, which has no title slot.
func trayModel(view core.View, tray prefs.TrayPreferences, digest *UsageDigest, locale prefs.Locale,
	quota prefs.QuotaDisplayMode, display currency.Display, mac bool) TrayModel {
	status, icon := statusLine(view, locale)
	if fallback := view.InferencePortFallback; fallback != nil {
		status += " · " + prefs.T(locale, "host.tray.status.fallback",
			"requested", strconv.Itoa(int(fallback.RequestedPort)),
			"active", strconv.Itoa(int(fallback.ActivePort)))
	}
	if view.Phase == core.PhaseError || view.Phase == core.PhaseExited {
		if view.LastError != nil {
			if text := truncate(*view.LastError, maxErrorChars); text != "" {
				status += " · " + text
			}
		}
	}
	title := menubarTitle(view, tray.MenubarText, digest, locale, quota, display)
	body := status
	if title != nil && !mac {
		body = status + " · " + *title
	}
	return TrayModel{Icon: icon, Title: title, Tooltip: prefs.T(locale, "host.tray.tooltip", "status", body)}
}

func statusLine(view core.View, locale prefs.Locale) (string, TrayIconState) {
	t := func(key string, vars ...string) string { return prefs.T(locale, key, vars...) }
	switch view.Phase {
	case core.PhaseReady:
		address := ""
		if view.InferenceURL != nil {
			address = displayAddress(*view.InferenceURL)
		}
		text := t("host.tray.status.ready", "address", address)
		if view.ObserverActive {
			key := "host.tray.status.observed"
			if view.ObserverReadLevel != nil && *view.ObserverReadLevel == core.ReadRaw {
				key = "host.tray.status.observedRaw"
			}
			text += " · " + t(key)
		}
		// An agent waiting on the operator, or one holding a timed grant,
		// is as worth noticing as one reading; all switch to the watched
		// icon.
		if view.PendingRawAccess > 0 {
			text += " · " + t("host.tray.status.rawAccessPending", "count", strconv.FormatUint(uint64(view.PendingRawAccess), 10))
		}
		if view.ActiveRawGrants > 0 {
			text += " · " + t("host.tray.status.rawGrantsActive", "count", strconv.FormatUint(uint64(view.ActiveRawGrants), 10))
		}
		// The raw password is set up only in the main window; say so where
		// a user who closed it still looks. No agent is involved, so the
		// icon keeps its meaning.
		if view.RawPasswordRequired {
			text += " · " + t("host.tray.status.rawPasswordRequired")
		}
		// The same holds for a raw key replaced outside the desktop: the
		// main window's warning is the only place that resolves it.
		if view.RawKeyReplaced {
			text += " · " + t("host.tray.status.rawKeyReplaced")
		}
		if view.ObserverActive || view.PendingRawAccess > 0 || view.ActiveRawGrants > 0 {
			return text, TrayWatched
		}
		return text, TrayReady
	case core.PhaseStopped:
		return t("host.tray.status.stopped"), TrayIdle
	case core.PhaseSpawning, core.PhaseWaitingForReady, core.PhaseHandshaking:
		return t("host.tray.status.starting"), TrayIdle
	case core.PhaseStopping:
		return t("host.tray.status.stopping"), TrayIdle
	default:
		return t("host.tray.status.failed"), TrayIdle
	}
}

func menubarTitle(view core.View, choice prefs.TrayMenubarText, digest *UsageDigest, locale prefs.Locale,
	quota prefs.QuotaDisplayMode, display currency.Display) *string {
	text := func(s string) *string { return &s }
	if choice == prefs.MenubarNone {
		return nil
	}
	if view.Phase == core.PhaseError || view.Phase == core.PhaseExited {
		return text(prefs.T(locale, "host.tray.menubar.alert"))
	}
	if view.Phase != core.PhaseReady || digest == nil {
		return nil
	}
	switch choice {
	case prefs.MenubarRequests:
		if digest.Today != nil {
			return text(strconv.FormatInt(digest.Today.Requests, 10))
		}
	case prefs.MenubarTokens:
		if digest.Today != nil {
			return text(formatTokens(digest.Today.TotalTokens))
		}
	case prefs.MenubarCost:
		if digest.CostToday != nil {
			return text(display.Format(digest.CostToday.AmountUSD))
		}
	case prefs.MenubarSubscription:
		highest, found := 0.0, false
		for _, subscription := range digest.Subscriptions {
			for _, window := range subscription.Windows {
				if !found || window.UsedPercent > highest {
					highest, found = window.UsedPercent, true
				}
			}
		}
		if found {
			return text(fmt.Sprintf("%d%%", int64(math.Round(quota.Percent(highest)))))
		}
	}
	return nil
}

func displayAddress(url string) string {
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "https://")
	return strings.TrimRight(url, "/")
}

func truncate(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= maxChars {
		return text
	}
	runes := []rune(text)
	return string(runes[:max(maxChars-1, 0)]) + "…"
}

func formatTokens(tokens int64) string {
	value := float64(max(tokens, 0))
	switch {
	case value >= 1e9:
		return trimDecimal(value/1e9, "B")
	case value >= 1e6:
		return trimDecimal(value/1e6, "M")
	case value >= 1e3:
		return trimDecimal(value/1e3, "K")
	}
	return strconv.FormatInt(max(tokens, 0), 10)
}

func trimDecimal(value float64, unit string) string {
	if value >= 100 {
		return strconv.FormatInt(int64(math.Round(value)), 10) + unit
	}
	return strings.TrimSuffix(strconv.FormatFloat(value, 'f', 1, 64), ".0") + unit
}

func percentOf(part, whole int64) uint8 {
	if whole <= 0 {
		return 0
	}
	return uint8(math.Min(math.Max(math.Round(float64(max(part, 0))*100/float64(whole)), 0), 100))
}

// checkNavigation refuses pages the main window does not know.
func checkNavigation(page string) error {
	if slices.Contains(navigationKinds, page) {
		return nil
	}
	return errors.New("unknown tray navigation target")
}

// localWindows are today's, yesterday's and this month's local bounds in
// UTC, whole seconds, with the IANA time zone Core buckets hours in.
type localWindows struct {
	timeZone      string
	todayFrom     string
	todayTo       string
	yesterdayFrom string
	monthFrom     string
}

func localWindowsAt(now time.Time, timeZone string) localWindows {
	midnight := func(year int, month time.Month, day int) string {
		return time.Date(year, month, day, 0, 0, 0, 0, now.Location()).UTC().Format(time.RFC3339)
	}
	y, m, d := now.Date()
	return localWindows{
		timeZone:      timeZone,
		todayFrom:     midnight(y, m, d),
		todayTo:       midnight(y, m, d+1),
		yesterdayFrom: midnight(y, m, d-1),
		monthFrom:     midnight(y, m, 1),
	}
}

// localTimeZone is the IANA name of the local time zone: TZ, or what
// /etc/localtime links to. Windows names its zones otherwise; it gets the
// fixed offset of now, which is right but on a daylight saving change.
func localTimeZone() string {
	if tz := strings.TrimPrefix(os.Getenv("TZ"), ":"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(filepath.ToSlash(target), "zoneinfo/"); ok {
			if _, err := time.LoadLocation(name); err == nil {
				return name
			}
		}
	}
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		if name := strings.TrimSpace(string(data)); name != "" {
			if _, err := time.LoadLocation(name); err == nil {
				return name
			}
		}
	}
	return fixedZone(time.Now())
}

// fixedZone is the Etc/GMT zone of t's offset, whose sign POSIX inverts;
// UTC for an offset of part hours.
func fixedZone(t time.Time) string {
	_, offset := t.Zone()
	if offset == 0 || offset%3600 != 0 {
		return "UTC"
	}
	return fmt.Sprintf("Etc/GMT%+d", -offset/3600)
}

// The digest parsers read Core's control API answers; a field that is
// missing or of another type leaves its card empty.

type object = map[string]json.RawMessage

func field[T any](o object, key string) (T, bool) {
	var v T
	raw, ok := o[key]
	if !ok || string(raw) == "null" {
		return v, false
	}
	return v, json.Unmarshal(raw, &v) == nil
}

func objectOf(raw json.RawMessage) (object, bool) {
	var o object
	return o, json.Unmarshal(raw, &o) == nil && o != nil
}

func totalsFrom(summary object) *UsageTotals {
	totals, ok := field[object](summary, "totals")
	if !ok {
		return nil
	}
	var t UsageTotals
	for key, target := range map[string]*int64{
		"requests": &t.Requests, "failed_requests": &t.Failed, "total_tokens": &t.TotalTokens,
		"input_tokens": &t.InputTokens, "cache_read_tokens": &t.CacheReadTokens,
	} {
		value, ok := field[int64](totals, key)
		if !ok {
			return nil
		}
		*target = value
	}
	return &t
}

func hourlyFrom(summary object) []int64 {
	buckets, ok := field[[]object](summary, "by_hour")
	if !ok {
		return []int64{}
	}
	slots := make([]int64, 24)
	for _, bucket := range buckets {
		hour, hourOK := field[uint64](bucket, "hour")
		tokens, tokensOK := field[int64](bucket, "total_tokens")
		if hourOK && tokensOK && hour < 24 {
			slots[hour] += tokens
		}
	}
	return slots
}

func topGroup(summary object, totalTokens int64) *Share {
	groups, ok := field[[]object](summary, "by_model")
	if !ok {
		return nil
	}
	for _, group := range groups {
		id, ok := field[string](group, "id")
		if !ok {
			continue
		}
		tokens, ok := field[int64](group, "total_tokens")
		if !ok {
			return nil
		}
		return &Share{Name: id, Percent: percentOf(tokens, totalTokens)}
	}
	return nil
}

func costFrom(summary object) *CostDigest {
	amount, ok := field[string](summary, "amount_usd")
	if !ok {
		return nil
	}
	value, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		return nil
	}
	unpriced, _ := field[int64](summary, "unpriced")
	return &CostDigest{AmountUSD: value, Unpriced: unpriced}
}

func privacyFrom(summary object, enabled *bool) *PrivacyDigest {
	totals, ok := field[object](summary, "totals")
	if !ok {
		return nil
	}
	counts := map[string]int64{}
	for _, key := range []string{"allowed", "ignored", "warned", "redacted", "blocked", "fail_open", "replaced", "restored", "unrestored"} {
		value, ok := field[int64](totals, key)
		if !ok {
			return nil
		}
		counts[key] = value
	}
	byKind, ok := field[[]object](summary, "by_kind")
	if !ok {
		return nil
	}
	kinds := []PrivacyKindCount{}
	for _, kind := range byKind {
		name, nameOK := field[string](kind, "kind")
		count, countOK := field[int64](kind, "count")
		if nameOK && countOK && len(kinds) < maxPrivacyKinds {
			kinds = append(kinds, PrivacyKindCount{Kind: name, Count: count})
		}
	}
	return &PrivacyDigest{
		Enabled:    enabled,
		Checked:    counts["allowed"] + counts["ignored"] + counts["warned"] + counts["redacted"] + counts["blocked"],
		Redacted:   counts["redacted"],
		Warned:     counts["warned"],
		Blocked:    counts["blocked"],
		Unchecked:  counts["fail_open"],
		Replaced:   counts["replaced"],
		Restored:   counts["restored"],
		Unrestored: counts["unrestored"],
		Kinds:      kinds,
	}
}

func topClientFrom(tokens, usage object) *Share {
	tokenItems, ok := field[[]object](tokens, "items")
	if !ok {
		return nil
	}
	names := map[string]string{}
	for _, item := range tokenItems {
		id, idOK := field[string](item, "id")
		name, nameOK := field[string](item, "name")
		if idOK && nameOK {
			names[id] = name
		}
	}
	items, ok := field[[]object](usage, "items")
	if !ok {
		return nil
	}
	var total, bestTokens int64
	bestID, found := "", false
	for _, item := range items {
		id, idOK := field[string](item, "token_id")
		today, todayOK := field[int64](item, "today_tokens")
		if !idOK || !todayOK {
			return nil
		}
		total += today
		if !found || today > bestTokens {
			bestID, bestTokens, found = id, today, true
		}
	}
	if !found || bestTokens <= 0 {
		return nil
	}
	name, ok := names[bestID]
	if !ok {
		name = bestID
	}
	return &Share{Name: name, Percent: percentOf(bestTokens, total)}
}

func lastRequestFrom(page object) *LastRequest {
	items, ok := field[[]object](page, "items")
	if !ok || len(items) == 0 {
		return nil
	}
	record := items[0]
	startedAt, ok := field[string](record, "started_at")
	if !ok {
		return nil
	}
	started, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return nil
	}
	last := &LastRequest{StartedAt: started.UTC()}
	if model, ok := field[string](record, "requested_model"); ok {
		last.Model = &model
	}
	if latency, ok := field[int64](record, "latency_ms"); ok {
		last.LatencyMS = &latency
	}
	status, _ := field[string](record, "status")
	last.Failed = status == "failed"
	return last
}

func windowFrom(raw json.RawMessage, secondary bool, label *string) *WindowDigest {
	window, ok := objectOf(raw)
	if !ok {
		return nil
	}
	used, ok := field[float64](window, "used_percent")
	if !ok {
		return nil
	}
	digest := &WindowDigest{Secondary: secondary, UsedPercent: used}
	if label != nil {
		if trimmed := strings.TrimSpace(*label); trimmed != "" {
			text := truncate(trimmed, maxWindowLabelChars)
			digest.Label = &text
		}
	}
	if seconds, ok := field[int64](window, "limit_window_seconds"); ok {
		digest.LimitWindowSeconds = &seconds
	}
	if reset, ok := field[string](window, "reset_at"); ok {
		if at, err := time.Parse(time.RFC3339, reset); err == nil {
			at = at.UTC()
			digest.ResetAt = &at
		}
	}
	return digest
}

// subscriptionFrom is every window a plan reports: the primary/secondary
// pair, then any named additional limits. Some plans (Kimi monthly, Claude
// per-model weekly caps) only have the latter, so dropping them hid those
// plans entirely.
func subscriptionFrom(name, kind string, usage object) *SubscriptionDigest {
	var windows []WindowDigest
	add := func(raw json.RawMessage, present, secondary bool, label *string) {
		if present {
			if window := windowFrom(raw, secondary, label); window != nil {
				windows = append(windows, *window)
			}
		}
	}
	primary, ok := usage["primary"]
	add(primary, ok, false, nil)
	secondary, ok := usage["secondary"]
	add(secondary, ok, true, nil)
	limits, _ := field[[]object](usage, "additional_rate_limits")
	for _, limit := range limits {
		var label *string
		if name, ok := field[string](limit, "limit_name"); ok {
			label = &name
		}
		primary, ok := limit["primary"]
		add(primary, ok, false, label)
		secondary, ok := limit["secondary"]
		add(secondary, ok, true, label)
	}
	if len(windows) == 0 {
		return nil
	}
	return &SubscriptionDigest{Name: name, Kind: kind, Windows: windows[:min(len(windows), maxWindowsPerSubscription)]}
}

type subscriptionService struct{ id, name, kind string }

// subscriptionServices are the plan-backed services. Disabled providers
// keep their plan; the quota is worth watching even while the gateway is
// not routing to them.
func subscriptionServices(items []json.RawMessage) []subscriptionService {
	var services []subscriptionService
	for _, raw := range items {
		service, ok := objectOf(raw)
		if !ok {
			continue
		}
		kind, kindOK := field[string](service, "kind")
		id, idOK := field[string](service, "id")
		name, nameOK := field[string](service, "name")
		if kindOK && idOK && nameOK && slices.Contains(subscriptionKinds, kind) && len(services) < maxSubscriptionServices {
			services = append(services, subscriptionService{id, name, kind})
		}
	}
	return services
}

// collectedUsage is the usage lines to collect: the enabled cards plus what
// the menu-bar text or a placed desktop widget reads, so neither goes blank
// when its card is off.
// The popover gates each card on the preference, not the data.
func collectedUsage(tray prefs.TrayPreferences, widgetPlaced bool) prefs.TrayUsagePreferences {
	usage := tray.Usage
	// A desktop widget reads plan windows like the menu-bar figure does.
	if widgetPlaced {
		usage.SubscriptionWindows = true
	}
	switch tray.MenubarText {
	case prefs.MenubarRequests, prefs.MenubarTokens:
		usage.Today = true
	case prefs.MenubarCost:
		usage.Cost = true
	case prefs.MenubarSubscription:
		usage.SubscriptionWindows = true
	}
	return usage
}

// popoverSize is the window size for popover content height CSS pixels
// tall, drawn at devicePixelRatio in a window of scaleFactor. Windows folds
// its Text size accessibility setting into the page's pixel ratio but not
// into the window's scale factor, so at 110% the page lays out 10% larger
// than its window and loses its right and bottom edges.
func popoverSize(height, devicePixelRatio, scaleFactor float64) (float64, float64) {
	zoom := devicePixelRatio / scaleFactor
	// A ratio read just before a move to a sharper monitor must not shrink
	// the window below its content.
	if math.IsNaN(zoom) || math.IsInf(zoom, 0) {
		zoom = 1
	}
	zoom = math.Min(math.Max(zoom, 1), popoverMaxZoom)
	return PopoverWidth * zoom, height * zoom
}

// anchorBelow: menu-bar icons sit in the top half of the work area and get
// the panel below them; taskbar icons sit in the bottom half and get it
// above.
func anchorBelow(icon, work Box) bool {
	return icon.Y+icon.Height/2 < work.Y+work.Height/2
}
