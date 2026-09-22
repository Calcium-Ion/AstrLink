//! The tray is the dashboard for a hidden window. Any click on the icon opens
//! a custom-drawn popover (its own webview, rendered by the app's React design
//! system) anchored to the icon; there is no native menu, every action lives
//! in the panel. The popover reads the Core view published by `CoreManager`,
//! the tray preferences, and a [`UsageDigest`] that this module collects on a
//! timer while the gateway is ready. The icon, tooltip and macOS status-item
//! title are derived from the same state by [`tray_model`].

use std::{
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
    time::{Duration, Instant},
};

use chrono::{DateTime, Datelike, Local, NaiveTime, SecondsFormat, TimeZone, Utc};
use serde::{Deserialize, Serialize};
use tauri::{
    menu::{IsMenuItem, Menu, MenuItem, MenuItemKind, PredefinedMenuItem},
    tray::{MouseButtonState, TrayIconBuilder, TrayIconEvent},
    AppHandle, Emitter, LogicalPosition, LogicalSize, Manager, PhysicalPosition, Rect, WebviewUrl,
    WebviewWindowBuilder, Wry,
};
use tauri_plugin_clipboard_manager::ClipboardExt;

use crate::{
    i18n::{self, Locale},
    preferences::{PreferencesStore, TrayMenubarText, TrayPreferences, TrayUsagePreferences},
    sidecar::{CoreManager, CorePhase, CoreView},
};

pub const TRAY_ID: &str = "main";
/// The popover webview. Must match `TRAY_POPOVER_LABEL` in the frontend.
pub const POPOVER_LABEL: &str = "tray-popover";
/// Emitted to the main window with the `WorkspacePage.kind` to open.
pub const NAVIGATE_EVENT: &str = "tray:navigate";
/// Emitted to the popover with a fresh [`TrayStateSnapshot`].
pub const STATE_EVENT: &str = "tray:state";

/// Two cadences. Everything except plan quotas is local (Core's SQLite over
/// loopback), so it can run often: every tick while the popover is open,
/// every `LOCAL_REFRESH_HIDDEN` otherwise, which also keeps a menu-bar
/// figure current.
const SCHEDULER_TICK: Duration = Duration::from_secs(5);
const LOCAL_REFRESH_HIDDEN: Duration = Duration::from_secs(30);
/// Plan quota windows are upstream provider calls (Codex, Claude, Kimi…)
/// and must not be polled on anyone's behalf: refreshed every two minutes
/// while the popover is open, every five while only the menu bar shows a
/// plan figure, and otherwise only when the popover opens or the operator
/// asks. Core additionally caches each lookup for 30s.
pub const PLAN_REFRESH_VISIBLE: Duration = Duration::from_secs(120);
const PLAN_REFRESH_BACKGROUND: Duration = Duration::from_secs(300);
const CLICK_REFRESH_DEBOUNCE: Duration = Duration::from_secs(10);
/// Upper bound for one digest collection; the slowest part is a plan quota
/// lookup that Core forwards upstream with its own 20s timeout.
const DIGEST_TIMEOUT: Duration = Duration::from_secs(45);
#[cfg(target_os = "macos")]
const COPIED_FLASH: Duration = Duration::from_millis(1500);
const MAX_ERROR_CHARS: usize = 80;
/// Bounds the number of upstream usage lookups a single refresh can trigger.
const MAX_SUBSCRIPTION_SERVICES: usize = 8;
/// Primary, secondary and a handful of named limits per plan.
const MAX_WINDOWS_PER_SUBSCRIPTION: usize = 6;
const MAX_WINDOW_LABEL_CHARS: usize = 40;

pub const POPOVER_WIDTH: f64 = 360.0;
const POPOVER_INITIAL_HEIGHT: f64 = 320.0;
const POPOVER_MIN_HEIGHT: f64 = 96.0;
/// Upper bound before the work area itself clamps the window; a folded
/// subscription list of ten rows plus today's numbers fits under this.
const POPOVER_MAX_HEIGHT: f64 = 960.0;
/// Vertical space between the tray icon and the window. The panel inside the
/// window adds its own few pixels, so this stays tight to hug the icon.
const POPOVER_GAP: f64 = 2.0;
/// Space kept from the work-area edges: sideways the panel must not touch
/// the screen edge; vertically it only needs to clear the menu bar/taskbar.
const POPOVER_MARGIN_X: f64 = 8.0;
const POPOVER_MARGIN_Y: f64 = 2.0;
/// How often the observer state is polled while the gateway is ready.
const OBSERVER_POLL_INTERVAL: Duration = Duration::from_secs(4);
/// Clicking the tray icon while the popover is open first blurs (and hides)
/// it, then delivers the click. A click this soon after hiding is that click.
const POPOVER_REOPEN_GUARD: Duration = Duration::from_millis(350);
/// A blur delivered this soon after showing is the show itself settling.
const POPOVER_BLUR_GUARD: Duration = Duration::from_millis(150);

const SUBSCRIPTION_KINDS: [&str; 7] = [
    "codex_subscription",
    "claude_subscription",
    "grok_subscription",
    "kimi_coding",
    "glm_coding",
    "minimax_coding",
    "opencode_go",
];

const NAVIGATION_KINDS: [&str; 8] = [
    "overview",
    "list",
    "tokens",
    "safety",
    "records",
    "routing",
    "agentTools",
    "settings",
];

/// libappindicator trays deliver no click events, only a menu. Linux therefore
/// keeps a native menu with the same actions the popover offers elsewhere.
const LINUX_FALLBACK_MENU: bool = cfg!(target_os = "linux");
const ID_SHOW: &str = "show";
const ID_SETTINGS: &str = "settings";
const ID_QUIT: &str = "quit";
const ID_CORE_START: &str = "core:start";
const ID_CORE_STOP: &str = "core:stop";
const ID_CORE_RESTART: &str = "core:restart";

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum TrayIconState {
    Ready,
    /// Anything but a running gateway: stopped, starting, stopping, failed.
    Idle,
    /// Ready, and an agent is reading records through the MCP bridge.
    Watched,
}

/// What the native tray icon itself shows: its state image, the macOS
/// status-item title and the tooltip.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct TrayModel {
    pub icon: TrayIconState,
    pub title: Option<String>,
    pub tooltip: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize)]
pub struct UsageTotals {
    pub requests: i64,
    pub failed: i64,
    pub total_tokens: i64,
    pub input_tokens: i64,
    pub cache_read_tokens: i64,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct Share {
    pub name: String,
    pub percent: u8,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct CostDigest {
    pub amount_usd: f64,
    pub unpriced: i64,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct LastRequest {
    pub started_at: DateTime<Utc>,
    pub model: Option<String>,
    pub latency_ms: Option<i64>,
    pub failed: bool,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct WindowDigest {
    /// Provider-named limit (Kimi "Monthly", Claude "Opus"); None for the
    /// primary/secondary windows, which the panel labels by their length.
    pub label: Option<String>,
    pub limit_window_seconds: Option<i64>,
    pub secondary: bool,
    pub used_percent: f64,
    pub reset_at: Option<DateTime<Utc>>,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct SubscriptionDigest {
    pub name: String,
    pub windows: Vec<WindowDigest>,
}

/// Everything the popover's usage cards need, collected in one pass. Each
/// field is independent: a failed lookup hides its card, not the whole panel.
#[derive(Clone, Debug, Default, PartialEq, Serialize)]
pub struct UsageDigest {
    pub today: Option<UsageTotals>,
    /// Tokens per local hour of today, 24 slots, empty when unknown.
    pub hourly_tokens: Vec<i64>,
    pub yesterday_tokens: Option<i64>,
    pub top_model: Option<Share>,
    pub cost_today: Option<CostDigest>,
    pub top_client: Option<Share>,
    pub last_request: Option<LastRequest>,
    pub month_tokens: Option<i64>,
    pub subscriptions: Vec<SubscriptionDigest>,
}

/// What the popover (and the settings preview) renders from.
#[derive(Clone, Debug, Serialize)]
pub struct TrayStateSnapshot {
    pub app_version: String,
    pub platform: &'static str,
    pub view: CoreView,
    pub digest: Option<UsageDigest>,
    pub digest_age_ms: Option<u64>,
    pub tray: TrayPreferences,
    /// Whether the popover hangs below its anchor (menu bar) or rises above
    /// it (taskbar); the panel keeps its shadow padding on the far side.
    pub popover_below: bool,
}

/// Actions the popover can ask the host to perform.
#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum TrayAction {
    Open,
    Navigate { page: String },
    CopyAddress,
    Core { op: CoreOp },
    Refresh,
    Quit,
}

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum CoreOp {
    Start,
    Stop,
    Restart,
}

pub fn tray_model(
    view: &CoreView,
    prefs: &TrayPreferences,
    digest: Option<&UsageDigest>,
    locale: Locale,
) -> TrayModel {
    let (mut status_text, icon) = status_line(view, locale);
    if let Some(fallback) = &view.inference_port_fallback {
        status_text.push_str(" · ");
        status_text.push_str(&i18n::t(
            locale,
            "host.tray.status.fallback",
            &[
                ("requested", &fallback.requested_port.to_string()),
                ("active", &fallback.active_port.to_string()),
            ],
        ));
    }
    if matches!(view.phase, CorePhase::Error | CorePhase::Exited) {
        if let Some(error) = view
            .last_error
            .as_deref()
            .map(|error| truncate(error, MAX_ERROR_CHARS))
            .filter(|error| !error.is_empty())
        {
            status_text.push_str(" · ");
            status_text.push_str(&error);
        }
    }

    let title = menubar_title(view, prefs.menubar_text, digest, locale);
    let tooltip_body = match &title {
        Some(title) if !cfg!(target_os = "macos") => format!("{status_text} · {title}"),
        _ => status_text,
    };
    TrayModel {
        icon,
        title,
        tooltip: i18n::t(locale, "host.tray.tooltip", &[("status", &tooltip_body)]),
    }
}

fn status_line(view: &CoreView, locale: Locale) -> (String, TrayIconState) {
    let t = |key: &str, vars: &[(&str, &str)]| i18n::t(locale, key, vars);
    match view.phase {
        CorePhase::Ready => {
            let address = view
                .inference_url
                .as_deref()
                .map(display_address)
                .unwrap_or_default();
            let mut text = t("host.tray.status.ready", &[("address", &address)]);
            if view.observer_active {
                text.push_str(" · ");
                text.push_str(&t("host.tray.status.observed", &[]));
                return (text, TrayIconState::Watched);
            }
            (text, TrayIconState::Ready)
        }
        CorePhase::Stopped => (t("host.tray.status.stopped", &[]), TrayIconState::Idle),
        CorePhase::Spawning | CorePhase::WaitingForReady | CorePhase::Handshaking => {
            (t("host.tray.status.starting", &[]), TrayIconState::Idle)
        }
        CorePhase::Stopping => (t("host.tray.status.stopping", &[]), TrayIconState::Idle),
        CorePhase::Exited | CorePhase::Error => {
            (t("host.tray.status.failed", &[]), TrayIconState::Idle)
        }
    }
}

fn menubar_title(
    view: &CoreView,
    choice: TrayMenubarText,
    digest: Option<&UsageDigest>,
    locale: Locale,
) -> Option<String> {
    if choice == TrayMenubarText::None {
        return None;
    }
    if matches!(view.phase, CorePhase::Error | CorePhase::Exited) {
        return Some(i18n::t(locale, "host.tray.menubar.alert", &[]));
    }
    if view.phase != CorePhase::Ready {
        return None;
    }
    let digest = digest?;
    match choice {
        TrayMenubarText::Requests => digest
            .today
            .as_ref()
            .map(|today| today.requests.to_string()),
        TrayMenubarText::Tokens => digest
            .today
            .as_ref()
            .map(|today| format_tokens(today.total_tokens)),
        TrayMenubarText::Cost => digest
            .cost_today
            .as_ref()
            .map(|cost| format!("${}", format_usd(cost.amount_usd))),
        TrayMenubarText::Subscription => digest
            .subscriptions
            .iter()
            .flat_map(|subscription| subscription.windows.iter())
            .map(|window| window.used_percent)
            .fold(None, |highest: Option<f64>, percent| {
                Some(highest.map_or(percent, |value| value.max(percent)))
            })
            .map(|percent| format!("{}%", percent.round().clamp(0.0, 999.0) as i64)),
        TrayMenubarText::None | TrayMenubarText::AlertOnly => None,
    }
}

// ---------------------------------------------------------------------------
// Formatting
// ---------------------------------------------------------------------------

fn display_address(url: &str) -> String {
    url.trim_start_matches("http://")
        .trim_start_matches("https://")
        .trim_end_matches('/')
        .to_string()
}

fn truncate(text: &str, max_chars: usize) -> String {
    let text = text.trim();
    if text.chars().count() <= max_chars {
        return text.to_string();
    }
    let mut out: String = text.chars().take(max_chars.saturating_sub(1)).collect();
    out.push('…');
    out
}

pub fn format_tokens(tokens: i64) -> String {
    let value = tokens.max(0) as f64;
    if value >= 1_000_000_000.0 {
        trim_decimal(value / 1_000_000_000.0, "B")
    } else if value >= 1_000_000.0 {
        trim_decimal(value / 1_000_000.0, "M")
    } else if value >= 1_000.0 {
        trim_decimal(value / 1_000.0, "K")
    } else {
        format!("{}", tokens.max(0))
    }
}

fn trim_decimal(value: f64, unit: &str) -> String {
    if value >= 100.0 {
        format!("{}{unit}", value.round() as i64)
    } else {
        let text = format!("{value:.1}");
        format!("{}{unit}", text.trim_end_matches(".0"))
    }
}

pub fn format_usd(amount: f64) -> String {
    if amount <= 0.0 {
        return "0.00".to_string();
    }
    if amount < 0.01 {
        return "<0.01".to_string();
    }
    format!("{amount:.2}")
}

fn percent_of(part: i64, whole: i64) -> u8 {
    if whole <= 0 {
        return 0;
    }
    ((part.max(0) as f64) * 100.0 / whole as f64)
        .round()
        .clamp(0.0, 100.0) as u8
}

// ---------------------------------------------------------------------------
// Usage collection
// ---------------------------------------------------------------------------

struct LocalWindows {
    time_zone: String,
    today_from: String,
    today_to: String,
    yesterday_from: String,
    month_from: String,
}

fn local_windows(now: DateTime<Local>) -> Option<LocalWindows> {
    let midnight = NaiveTime::from_hms_opt(0, 0, 0)?;
    let today = now.date_naive();
    let to_utc = |date: chrono::NaiveDate| -> Option<String> {
        Local
            .from_local_datetime(&date.and_time(midnight))
            .earliest()
            .map(|at| {
                at.with_timezone(&Utc)
                    .to_rfc3339_opts(SecondsFormat::Secs, true)
            })
    };
    Some(LocalWindows {
        time_zone: iana_time_zone::get_timezone().unwrap_or_else(|_| "UTC".to_string()),
        today_from: to_utc(today)?,
        today_to: to_utc(today.succ_opt()?)?,
        yesterday_from: to_utc(today.pred_opt()?)?,
        month_from: to_utc(today.with_day(1)?)?,
    })
}

fn totals_from(value: &serde_json::Value) -> Option<UsageTotals> {
    let totals = value.get("totals")?;
    Some(UsageTotals {
        requests: totals.get("requests")?.as_i64()?,
        failed: totals.get("failed_requests")?.as_i64()?,
        total_tokens: totals.get("total_tokens")?.as_i64()?,
        input_tokens: totals.get("input_tokens")?.as_i64()?,
        cache_read_tokens: totals.get("cache_read_tokens")?.as_i64()?,
    })
}

fn hourly_from(value: &serde_json::Value) -> Vec<i64> {
    let mut slots = vec![0i64; 24];
    let Some(buckets) = value.get("by_hour").and_then(|value| value.as_array()) else {
        return Vec::new();
    };
    for bucket in buckets {
        let (Some(hour), Some(tokens)) = (
            bucket.get("hour").and_then(|hour| hour.as_u64()),
            bucket
                .get("total_tokens")
                .and_then(|tokens| tokens.as_i64()),
        ) else {
            continue;
        };
        if let Some(slot) = slots.get_mut(hour as usize) {
            *slot += tokens;
        }
    }
    slots
}

fn top_group(value: &serde_json::Value, total_tokens: i64) -> Option<Share> {
    let group = value
        .get("by_model")?
        .as_array()?
        .iter()
        .find(|group| group.get("id").and_then(|id| id.as_str()).is_some())?;
    Some(Share {
        name: group["id"].as_str()?.to_string(),
        percent: percent_of(group.get("total_tokens")?.as_i64()?, total_tokens),
    })
}

fn cost_from(value: &serde_json::Value) -> Option<CostDigest> {
    Some(CostDigest {
        amount_usd: value.get("amount_usd")?.as_str()?.parse::<f64>().ok()?,
        unpriced: value
            .get("unpriced")
            .and_then(|count| count.as_i64())
            .unwrap_or(0),
    })
}

fn top_client_from(tokens: &serde_json::Value, usage: &serde_json::Value) -> Option<Share> {
    let names: std::collections::HashMap<&str, &str> = tokens
        .get("items")?
        .as_array()?
        .iter()
        .filter_map(|item| Some((item.get("id")?.as_str()?, item.get("name")?.as_str()?)))
        .collect();
    let items = usage.get("items")?.as_array()?;
    let mut total = 0i64;
    let mut best: Option<(&str, i64)> = None;
    for item in items {
        let id = item.get("token_id")?.as_str()?;
        let today = item.get("today_tokens")?.as_i64()?;
        total += today;
        if best.map_or(true, |(_, tokens)| today > tokens) {
            best = Some((id, today));
        }
    }
    let (id, tokens) = best.filter(|(_, tokens)| *tokens > 0)?;
    Some(Share {
        name: names
            .get(id)
            .map(|name| name.to_string())
            .unwrap_or_else(|| id.to_string()),
        percent: percent_of(tokens, total),
    })
}

fn last_request_from(value: &serde_json::Value) -> Option<LastRequest> {
    let record = value.get("items")?.as_array()?.first()?;
    let started_at = DateTime::parse_from_rfc3339(record.get("started_at")?.as_str()?)
        .ok()?
        .with_timezone(&Utc);
    Some(LastRequest {
        started_at,
        model: record
            .get("requested_model")
            .and_then(|model| model.as_str())
            .map(str::to_string),
        latency_ms: record
            .get("latency_ms")
            .and_then(|latency| latency.as_i64()),
        failed: record.get("status").and_then(|status| status.as_str()) == Some("failed"),
    })
}

fn window_from(
    value: &serde_json::Value,
    secondary: bool,
    label: Option<&str>,
) -> Option<WindowDigest> {
    let window = value.as_object()?;
    Some(WindowDigest {
        label: label
            .map(str::trim)
            .filter(|label| !label.is_empty())
            .map(|label| truncate(label, MAX_WINDOW_LABEL_CHARS)),
        limit_window_seconds: window
            .get("limit_window_seconds")
            .and_then(|value| value.as_i64()),
        secondary,
        used_percent: window.get("used_percent")?.as_f64()?,
        reset_at: window
            .get("reset_at")
            .and_then(|value| value.as_str())
            .and_then(|value| DateTime::parse_from_rfc3339(value).ok())
            .map(|value| value.with_timezone(&Utc)),
    })
}

/// Every window a plan reports: the primary/secondary pair, then any named
/// additional limits. Some plans (Kimi monthly, Claude per-model weekly caps)
/// only have the latter, so dropping them hid those plans entirely.
fn subscription_from(name: &str, usage: &serde_json::Value) -> Option<SubscriptionDigest> {
    let mut windows = Vec::new();
    if let Some(window) = usage
        .get("primary")
        .and_then(|value| window_from(value, false, None))
    {
        windows.push(window);
    }
    if let Some(window) = usage
        .get("secondary")
        .and_then(|value| window_from(value, true, None))
    {
        windows.push(window);
    }
    if let Some(limits) = usage
        .get("additional_rate_limits")
        .and_then(|value| value.as_array())
    {
        for limit in limits {
            let label = limit.get("limit_name").and_then(|value| value.as_str());
            if let Some(window) = limit
                .get("primary")
                .and_then(|value| window_from(value, false, label))
            {
                windows.push(window);
            }
            if let Some(window) = limit
                .get("secondary")
                .and_then(|value| window_from(value, true, label))
            {
                windows.push(window);
            }
        }
    }
    windows.truncate(MAX_WINDOWS_PER_SUBSCRIPTION);
    (!windows.is_empty()).then(|| SubscriptionDigest {
        name: name.to_string(),
        windows,
    })
}

fn subscription_services(services: &serde_json::Value) -> Vec<(String, String)> {
    services
        .get("items")
        .and_then(|items| items.as_array())
        .map(|items| {
            items
                .iter()
                // Disabled providers keep their plan; the quota is worth
                // watching even while the gateway is not routing to them.
                .filter(|service| {
                    service
                        .get("kind")
                        .and_then(|kind| kind.as_str())
                        .is_some_and(|kind| SUBSCRIPTION_KINDS.contains(&kind))
                })
                .filter_map(|service| {
                    Some((
                        service.get("id")?.as_str()?.to_string(),
                        service.get("name")?.as_str()?.to_string(),
                    ))
                })
                .take(MAX_SUBSCRIPTION_SERVICES)
                .collect()
        })
        .unwrap_or_default()
}

/// `fresh` bypasses Core's 30s quota snapshot: an operator pressing refresh
/// wants the provider's current numbers, not a cache.
async fn collect_subscriptions(manager: &Arc<CoreManager>, fresh: bool) -> Vec<SubscriptionDigest> {
    let Ok(services) = manager.list_services().await else {
        return Vec::new();
    };
    let mut handles = Vec::new();
    for (id, name) in subscription_services(&services) {
        let manager = Arc::clone(manager);
        handles.push(tauri::async_runtime::spawn(async move {
            match manager.get_service_usage_with(&id, fresh).await {
                Ok(usage) => subscription_from(&name, &usage),
                Err(error) => {
                    // Visible in the dev log; the panel just omits the plan
                    // until the next refresh succeeds.
                    eprintln!("tray: subscription usage for {id} unavailable: {error}");
                    None
                }
            }
        }));
    }
    let mut digests = Vec::new();
    for handle in handles {
        if let Ok(Some(digest)) = handle.await {
            digests.push(digest);
        }
    }
    digests
}

/// How a refresh treats the upstream plan quota lookups.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PlanRefresh {
    /// Reuse whatever is cached, however old; never call upstream.
    Reuse,
    /// Call upstream unless the cache is younger than `max_age`.
    IfOlderThan(Duration),
    /// Call upstream now, and make Core skip its own snapshot too.
    Force,
}

/// Pulls only what the enabled cards need. Local numbers are always
/// collected; plan windows follow `plans` against the cached `previous`.
async fn collect_digest(
    manager: &Arc<CoreManager>,
    prefs: &TrayUsagePreferences,
    previous: Option<(&Vec<SubscriptionDigest>, Instant)>,
    plans: PlanRefresh,
) -> (UsageDigest, Option<Instant>) {
    let mut digest = UsageDigest::default();
    let Some(windows) = local_windows(Local::now()) else {
        return (digest, None);
    };

    if prefs.needs_usage_summary() {
        if let Ok(summary) = manager
            .get_usage_summary(
                &windows.today_from,
                &windows.today_to,
                &windows.time_zone,
                "hour",
            )
            .await
        {
            digest.today = totals_from(&summary);
            digest.hourly_tokens = hourly_from(&summary);
            if prefs.top_model {
                let total = digest.today.as_ref().map_or(0, |today| today.total_tokens);
                digest.top_model = top_group(&summary, total);
            }
        }
        if prefs.compare_yesterday {
            digest.yesterday_tokens = manager
                .get_usage_summary(
                    &windows.yesterday_from,
                    &windows.today_from,
                    &windows.time_zone,
                    "day",
                )
                .await
                .ok()
                .and_then(|summary| totals_from(&summary))
                .map(|totals| totals.total_tokens);
        }
    }

    if prefs.cost {
        digest.cost_today = manager
            .pricing(
                "summary",
                None,
                Some(serde_json::json!({"from": windows.today_from, "to": windows.today_to})),
            )
            .await
            .ok()
            .and_then(|summary| cost_from(&summary));
    }

    if prefs.top_client {
        if let (Ok(tokens), Ok(usage)) = (
            manager.list_access_tokens().await,
            manager.list_access_token_usage(&windows.today_from).await,
        ) {
            digest.top_client = top_client_from(&tokens, &usage);
        }
    }

    if prefs.last_request {
        digest.last_request = manager
            .list_request_records(serde_json::json!({"limit": 1}))
            .await
            .ok()
            .and_then(|page| last_request_from(&page));
    }

    if prefs.month_total {
        digest.month_tokens = manager
            .get_usage_summary(
                &windows.month_from,
                &windows.today_to,
                &windows.time_zone,
                "day",
            )
            .await
            .ok()
            .and_then(|summary| totals_from(&summary))
            .map(|totals| totals.total_tokens);
    }

    let mut subscriptions_at = None;
    if prefs.subscription_windows {
        // Only a non-empty result counts as cached. An empty one usually means
        // the lookups failed (typically right after start-up, before upstream
        // auth settles) and is retried whenever plans are next allowed.
        let cached = previous.filter(|(cached, _)| !cached.is_empty());
        let reuse = match (plans, cached) {
            (PlanRefresh::Force, _) => None,
            (PlanRefresh::Reuse, cached) => cached,
            (PlanRefresh::IfOlderThan(max_age), Some((cached, at))) if at.elapsed() < max_age => {
                Some((cached, at))
            }
            (PlanRefresh::IfOlderThan(_), _) => None,
        };
        match reuse {
            Some((cached, at)) => {
                digest.subscriptions = cached.clone();
                subscriptions_at = Some(at);
            }
            None if plans == PlanRefresh::Reuse => {}
            None => {
                digest.subscriptions =
                    collect_subscriptions(manager, plans == PlanRefresh::Force).await;
                subscriptions_at = (!digest.subscriptions.is_empty()).then(Instant::now);
            }
        }
    }

    (digest, subscriptions_at)
}

// ---------------------------------------------------------------------------
// Popover placement
// ---------------------------------------------------------------------------

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct WindowBox {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}

/// Where the popover goes for a tray icon at `icon` inside the monitor work
/// area `work` (both logical). Menu-bar icons sit in the top half and get the
/// panel below them; taskbar icons sit in the bottom half and get it above.
/// The panel is centred on the icon and kept inside the work area.
pub fn popover_placement(icon: WindowBox, work: WindowBox, size: (f64, f64)) -> (f64, f64) {
    let (width, height) = size;
    let below = anchor_below(icon, work);
    let max_x = (work.x + work.width - width - POPOVER_MARGIN_X).max(work.x + POPOVER_MARGIN_X);
    let x = (icon.x + icon.width / 2.0 - width / 2.0).clamp(work.x + POPOVER_MARGIN_X, max_x);
    let preferred_y = if below {
        icon.y + icon.height + POPOVER_GAP
    } else {
        icon.y - POPOVER_GAP - height
    };
    let max_y = (work.y + work.height - height - POPOVER_MARGIN_Y).max(work.y + POPOVER_MARGIN_Y);
    let y = preferred_y.clamp(work.y + POPOVER_MARGIN_Y, max_y);
    (x, y)
}

/// Menu-bar icons sit in the top half of the work area and get the panel
/// below them; taskbar icons sit in the bottom half and get it above.
fn anchor_below(icon: WindowBox, work: WindowBox) -> bool {
    icon.y + icon.height / 2.0 < work.y + work.height / 2.0
}

#[derive(Clone, Copy, Debug)]
struct PopoverAnchor {
    icon: WindowBox,
    work: WindowBox,
}

fn anchor_from_click(
    app: &AppHandle,
    position: PhysicalPosition<f64>,
    rect: Rect,
) -> Option<PopoverAnchor> {
    let monitor = app
        .monitor_from_point(position.x, position.y)
        .ok()
        .flatten()
        .or_else(|| app.primary_monitor().ok().flatten())?;
    let scale = monitor.scale_factor();
    let icon_position = rect.position.to_logical::<f64>(scale);
    let icon_size = rect.size.to_logical::<f64>(scale);
    let work = monitor.work_area();
    let work_position = work.position.to_logical::<f64>(scale);
    let work_size = work.size.to_logical::<f64>(scale);
    Some(PopoverAnchor {
        icon: WindowBox {
            x: icon_position.x,
            y: icon_position.y,
            width: icon_size.width,
            height: icon_size.height,
        },
        work: WindowBox {
            x: work_position.x,
            y: work_position.y,
            width: work_size.width,
            height: work_size.height,
        },
    })
}

// ---------------------------------------------------------------------------
// Runtime
// ---------------------------------------------------------------------------

#[derive(Default)]
struct TrayRuntime {
    last_model: Option<TrayModel>,
    digest: Option<UsageDigest>,
    digest_at: Option<Instant>,
    subscriptions_at: Option<Instant>,
    last_click_refresh: Option<Instant>,
    refresh_in_flight: bool,
    anchor: Option<PopoverAnchor>,
    popover_height: f64,
    popover_shown_at: Option<Instant>,
    popover_hidden_at: Option<Instant>,
}

/// Managed state for the tray. Separate from `CoreManager` so the tray can be
/// re-rendered from preference and usage changes as well as Core changes.
pub struct TrayState {
    inner: Mutex<TrayRuntime>,
}

impl Default for TrayState {
    fn default() -> Self {
        Self {
            inner: Mutex::new(TrayRuntime {
                popover_height: POPOVER_INITIAL_HEIGHT,
                ..TrayRuntime::default()
            }),
        }
    }
}

impl TrayState {
    fn lock(&self) -> std::sync::MutexGuard<'_, TrayRuntime> {
        self.inner
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }
}

fn preferences_of(app: &AppHandle) -> crate::preferences::Preferences {
    app.try_state::<Arc<PreferencesStore>>()
        .map(|store| store.snapshot().values)
        .unwrap_or_default()
}

fn core_view(app: &AppHandle) -> CoreView {
    app.try_state::<Arc<CoreManager>>()
        .map(|manager| manager.view())
        .unwrap_or_default()
}

/// The snapshot the popover renders, for the stored preferences or a draft.
pub fn state_snapshot(app: &AppHandle, tray: Option<TrayPreferences>) -> TrayStateSnapshot {
    let preferences = preferences_of(app);
    let (digest, digest_age_ms, popover_below) = app
        .try_state::<TrayState>()
        .map(|state| {
            let runtime = state.lock();
            (
                runtime.digest.clone(),
                runtime
                    .digest_at
                    .map(|at| at.elapsed().as_millis().min(u64::MAX as u128) as u64),
                runtime
                    .anchor
                    .map_or(true, |anchor| anchor_below(anchor.icon, anchor.work)),
            )
        })
        .unwrap_or((None, None, true));
    TrayStateSnapshot {
        app_version: app.package_info().version.to_string(),
        platform: std::env::consts::OS,
        view: core_view(app),
        digest,
        digest_age_ms,
        tray: tray.unwrap_or(preferences.tray),
        popover_below,
    }
}

/// Re-derives the native model, re-renders it when it changed, and pushes a
/// fresh snapshot to an open popover.
pub fn refresh(app: &AppHandle) {
    let Some(state) = app.try_state::<TrayState>() else {
        return;
    };
    let preferences = preferences_of(app);
    let view = core_view(app);
    let digest = state.lock().digest.clone();
    let model = tray_model(
        &view,
        &preferences.tray,
        digest.as_ref(),
        preferences.locale,
    );
    let changed = {
        let mut runtime = state.lock();
        if runtime.last_model.as_ref() == Some(&model) {
            false
        } else {
            runtime.last_model = Some(model.clone());
            true
        }
    };
    if changed {
        if let Err(error) = render(app, &model) {
            eprintln!("unable to render AstrLink tray: {error}");
        }
    }
    if app.get_webview_window(POPOVER_LABEL).is_some() {
        if let Err(error) = app.emit_to(POPOVER_LABEL, STATE_EVENT, state_snapshot(app, None)) {
            eprintln!("unable to update the AstrLink tray popover: {error}");
        }
    }
}

/// The image for a state, and whether macOS should treat it as a template.
/// Running uses the native template glyph (the menu bar tints it, off-white
/// on a dark bar). Idle is a fixed black glyph and watched a fixed off-white
/// glyph with a red badge; neither can be a template, since templates are
/// alpha-only and would lose the black/red.
fn icon_for(state: TrayIconState) -> (tauri::image::Image<'static>, bool) {
    #[cfg(target_os = "macos")]
    {
        match state {
            TrayIconState::Ready => (
                tauri::include_image!("icons/tray/mac-ready/36x36.png"),
                true,
            ),
            TrayIconState::Idle => (
                tauri::include_image!("icons/tray/mac-idle/36x36.png"),
                false,
            ),
            TrayIconState::Watched => (
                tauri::include_image!("icons/tray/mac-watched/36x36.png"),
                false,
            ),
        }
    }
    #[cfg(not(target_os = "macos"))]
    {
        let image = match state {
            TrayIconState::Ready => tauri::include_image!("icons/tray/color-ready/32x32.png"),
            TrayIconState::Idle => tauri::include_image!("icons/tray/color-idle/32x32.png"),
            TrayIconState::Watched => tauri::include_image!("icons/tray/color-watched/32x32.png"),
        };
        (image, false)
    }
}

/// The Linux fallback menu: the tooltip text as a disabled row, gateway
/// controls for the current phase, then the fixed window actions.
fn fallback_menu(app: &AppHandle, model: &TrayModel) -> tauri::Result<Menu<Wry>> {
    let preferences = preferences_of(app);
    let locale = preferences.locale;
    let t = |key: &str| i18n::t(locale, key, &[]);
    let mut items: Vec<MenuItemKind<Wry>> = Vec::new();
    items.push(MenuItem::new(app, &model.tooltip, false, None::<&str>)?.kind());
    if preferences.tray.gateway_controls {
        items.push(PredefinedMenuItem::separator(app)?.kind());
        match core_view(app).phase {
            CorePhase::Ready => {
                items.push(
                    MenuItem::with_id(
                        app,
                        ID_CORE_RESTART,
                        t("host.tray.core.restart"),
                        true,
                        None::<&str>,
                    )?
                    .kind(),
                );
                items.push(
                    MenuItem::with_id(
                        app,
                        ID_CORE_STOP,
                        t("host.tray.core.stop"),
                        true,
                        None::<&str>,
                    )?
                    .kind(),
                );
            }
            CorePhase::Stopped | CorePhase::Exited | CorePhase::Error => {
                items.push(
                    MenuItem::with_id(
                        app,
                        ID_CORE_START,
                        t("host.tray.core.start"),
                        true,
                        None::<&str>,
                    )?
                    .kind(),
                );
            }
            _ => {}
        }
    }
    items.push(PredefinedMenuItem::separator(app)?.kind());
    items.push(MenuItem::with_id(app, ID_SHOW, t("host.tray.show"), true, None::<&str>)?.kind());
    items.push(
        MenuItem::with_id(
            app,
            ID_SETTINGS,
            t("host.tray.settings"),
            true,
            None::<&str>,
        )?
        .kind(),
    );
    items.push(MenuItem::with_id(app, ID_QUIT, t("host.tray.quit"), true, None::<&str>)?.kind());
    let refs: Vec<&dyn IsMenuItem<Wry>> = items
        .iter()
        .map(|item| item as &dyn IsMenuItem<Wry>)
        .collect();
    Menu::with_items(app, &refs)
}

fn handle_fallback_menu(app: &AppHandle, id: &str) {
    match id {
        ID_SHOW => crate::show_main_window(app),
        ID_SETTINGS => navigate(app, "settings"),
        ID_QUIT => quit(app),
        ID_CORE_START => run_core(app, CoreOp::Start),
        ID_CORE_STOP => run_core(app, CoreOp::Stop),
        ID_CORE_RESTART => run_core(app, CoreOp::Restart),
        other => eprintln!("unhandled AstrLink tray item: {other}"),
    }
}

fn render(app: &AppHandle, model: &TrayModel) -> Result<(), String> {
    let tray = app
        .tray_by_id(TRAY_ID)
        .ok_or_else(|| "tray icon is not registered".to_string())?;
    if LINUX_FALLBACK_MENU {
        let menu = fallback_menu(app, model).map_err(|error| error.to_string())?;
        tray.set_menu(Some(menu))
            .map_err(|error| error.to_string())?;
    }
    let (image, template) = icon_for(model.icon);
    tray.set_icon(Some(image))
        .map_err(|error| error.to_string())?;
    tray.set_icon_as_template(template)
        .map_err(|error| error.to_string())?;
    tray.set_tooltip(Some(&model.tooltip))
        .map_err(|error| error.to_string())?;
    #[cfg(target_os = "macos")]
    tray.set_title(model.title.as_deref())
        .map_err(|error| error.to_string())?;
    Ok(())
}

/// Builds the tray icon. Every click, whichever button, toggles the popover;
/// a double click (Windows) opens the main window directly. Linux gets the
/// native fallback menu because its tray never reports clicks.
pub fn build(app: &AppHandle) -> tauri::Result<()> {
    let preferences = preferences_of(app);
    let model = tray_model(&core_view(app), &preferences.tray, None, preferences.locale);
    // macOS gets the monochrome glyph set; the colour mark with its red badge
    // is for Windows and Linux trays.
    let (image, template) = icon_for(model.icon);
    let mut builder = TrayIconBuilder::with_id(TRAY_ID)
        .icon(image)
        .icon_as_template(template)
        .tooltip(&model.tooltip);
    if LINUX_FALLBACK_MENU {
        let menu = fallback_menu(app, &model)?;
        builder = builder
            .menu(&menu)
            .on_menu_event(|app, event| handle_fallback_menu(app, event.id().as_ref()));
    }
    builder
        .on_tray_icon_event(|tray, event| {
            let app = tray.app_handle();
            match event {
                TrayIconEvent::Click {
                    button_state: MouseButtonState::Up,
                    position,
                    rect,
                    ..
                } => toggle_popover(app, position, rect),
                TrayIconEvent::DoubleClick { .. } => {
                    hide_popover(app);
                    crate::show_main_window(app);
                }
                _ => {}
            }
        })
        .build(app)?;
    #[cfg(target_os = "macos")]
    if let Some(tray) = app.tray_by_id(TRAY_ID) {
        let _ = tray.set_title(model.title.as_deref());
    }
    if let Some(state) = app.try_state::<TrayState>() {
        state.lock().last_model = Some(model);
    }
    Ok(())
}

/// Performs an action requested by the popover.
pub fn perform(app: &AppHandle, action: TrayAction) -> Result<(), String> {
    match action {
        TrayAction::Open => {
            hide_popover(app);
            crate::show_main_window(app);
        }
        TrayAction::Navigate { page } => {
            if !NAVIGATION_KINDS.contains(&page.as_str()) {
                return Err("unknown tray navigation target".to_string());
            }
            hide_popover(app);
            navigate(app, &page);
        }
        TrayAction::CopyAddress => copy_address(app)?,
        TrayAction::Core { op } => run_core(app, op),
        // An explicit refresh means "now", plan windows included.
        TrayAction::Refresh => request_usage_refresh(app, true, PlanRefresh::Force),
        TrayAction::Quit => quit(app),
    }
    Ok(())
}

fn quit(app: &AppHandle) {
    if let Some(explicit_quit) = app.try_state::<Arc<AtomicBool>>() {
        explicit_quit.store(true, Ordering::SeqCst);
    }
    app.exit(0);
}

fn run_core(app: &AppHandle, op: CoreOp) {
    let Some(manager) = app.try_state::<Arc<CoreManager>>() else {
        return;
    };
    let manager = Arc::clone(manager.inner());
    match op {
        CoreOp::Start => {
            if let Err(error) = manager.start(app) {
                eprintln!("unable to start astrlink-core from the tray: {error}");
            }
        }
        CoreOp::Stop => {
            tauri::async_runtime::spawn(async move {
                if let Err(error) = manager.stop_and_wait().await {
                    eprintln!("unable to stop astrlink-core from the tray: {error}");
                }
            });
        }
        CoreOp::Restart => {
            let app = app.clone();
            tauri::async_runtime::spawn(async move {
                if let Err(error) = manager.restart(&app).await {
                    eprintln!("unable to restart astrlink-core from the tray: {error}");
                }
            });
        }
    }
}

fn navigate(app: &AppHandle, kind: &str) {
    crate::show_main_window(app);
    if let Err(error) = app.emit_to("main", NAVIGATE_EVENT, kind) {
        eprintln!("unable to route the AstrLink tray to {kind}: {error}");
    }
}

fn copy_address(app: &AppHandle) -> Result<(), String> {
    let address = core_view(app)
        .inference_url
        .ok_or_else(|| i18n::t(preferences_of(app).locale, "host.sidecar.notReady", &[]))?;
    app.clipboard()
        .write_text(address)
        .map_err(|error| error.to_string())?;
    // The status item is the one place that stays visible after the popover
    // closes. Other platforms stay silent rather than raise a notification.
    #[cfg(target_os = "macos")]
    {
        if let Some(tray) = app.tray_by_id(TRAY_ID) {
            let _ = tray.set_title(Some(i18n::t(
                preferences_of(app).locale,
                "host.tray.copied",
                &[],
            )));
        }
        let app = app.clone();
        tauri::async_runtime::spawn(async move {
            tokio::time::sleep(COPIED_FLASH).await;
            let title = app.try_state::<TrayState>().and_then(|state| {
                state
                    .lock()
                    .last_model
                    .as_ref()
                    .and_then(|model| model.title.clone())
            });
            if let Some(tray) = app.tray_by_id(TRAY_ID) {
                let _ = tray.set_title(title.as_deref());
            }
        });
    }
    Ok(())
}

// ---------------------------------------------------------------------------
// Popover window
// ---------------------------------------------------------------------------

fn build_popover_window(app: &AppHandle, x: f64, y: f64, height: f64) -> Result<(), String> {
    let mut builder = WebviewWindowBuilder::new(app, POPOVER_LABEL, WebviewUrl::default())
        .title("AstrLink")
        .inner_size(POPOVER_WIDTH, height)
        .position(x, y)
        .decorations(false)
        .transparent(true)
        .shadow(false)
        .always_on_top(true)
        .skip_taskbar(true)
        .resizable(false)
        .minimizable(false)
        .maximizable(false)
        .closable(false)
        .visible(false)
        .focused(false);
    #[cfg(target_os = "macos")]
    {
        builder = builder
            .visible_on_all_workspaces(true)
            .accept_first_mouse(true);
    }
    #[cfg(not(target_os = "macos"))]
    {
        builder = builder.visible_on_all_workspaces(true);
    }
    builder.build().map_err(|error| error.to_string())?;
    Ok(())
}

fn place_popover(app: &AppHandle, anchor: PopoverAnchor, height: f64) -> Option<(f64, f64)> {
    let window = app.get_webview_window(POPOVER_LABEL)?;
    // Never taller than the work area it hangs in; the panel scrolls inside.
    let height = height
        .min(anchor.work.height - 2.0 * POPOVER_MARGIN_Y)
        .max(POPOVER_MIN_HEIGHT);
    let (x, y) = popover_placement(anchor.icon, anchor.work, (POPOVER_WIDTH, height));
    let _ = window.set_size(LogicalSize::new(POPOVER_WIDTH, height));
    let _ = window.set_position(LogicalPosition::new(x, y));
    Some((x, y))
}

fn toggle_popover(app: &AppHandle, position: PhysicalPosition<f64>, rect: Rect) {
    let Some(state) = app.try_state::<TrayState>() else {
        return;
    };
    if let Some(window) = app.get_webview_window(POPOVER_LABEL) {
        if window.is_visible().unwrap_or(false) {
            hide_popover(app);
            return;
        }
    }
    let Some(anchor) = anchor_from_click(app, position, rect) else {
        eprintln!("unable to locate the monitor behind the AstrLink tray icon");
        crate::show_main_window(app);
        return;
    };
    let height = {
        let mut runtime = state.lock();
        if runtime
            .popover_hidden_at
            .is_some_and(|at| at.elapsed() < POPOVER_REOPEN_GUARD)
        {
            // The blur from this very click already closed the popover.
            return;
        }
        runtime.anchor = Some(anchor);
        runtime.popover_height
    };
    if app.get_webview_window(POPOVER_LABEL).is_some() {
        place_popover(app, anchor, height);
        present_popover(app);
        return;
    }
    // First open: the webview does not exist yet. Native window creation
    // stays off the event loop (WebView2 deadlocks when a callback creates a
    // webview) and off the async workers, exactly like the inspector windows.
    let (x, y) = popover_placement(anchor.icon, anchor.work, (POPOVER_WIDTH, height));
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        let build_app = app.clone();
        let built = tauri::async_runtime::spawn_blocking(move || {
            build_popover_window(&build_app, x, y, height)
        })
        .await
        .map_err(|error| format!("popover build task failed: {error}"))
        .and_then(|result| result);
        match built {
            Ok(()) => present_popover(&app),
            Err(error) => {
                eprintln!("unable to create the AstrLink tray popover: {error}");
                crate::show_main_window(&app);
            }
        }
    });
}

/// Shows the existing popover, seeds it with the current state and asks for
/// fresh numbers.
fn present_popover(app: &AppHandle) {
    let Some(window) = app.get_webview_window(POPOVER_LABEL) else {
        return;
    };
    if let Err(error) = window.show() {
        eprintln!("unable to show the AstrLink tray popover: {error}");
    }
    let _ = window.set_focus();
    if let Some(state) = app.try_state::<TrayState>() {
        state.lock().popover_shown_at = Some(Instant::now());
    }
    if let Err(error) = app.emit_to(POPOVER_LABEL, STATE_EVENT, state_snapshot(app, None)) {
        eprintln!("unable to seed the AstrLink tray popover: {error}");
    }
    request_usage_refresh(app, false, PlanRefresh::IfOlderThan(PLAN_REFRESH_VISIBLE));
}

pub fn hide_popover(app: &AppHandle) {
    let Some(window) = app.get_webview_window(POPOVER_LABEL) else {
        return;
    };
    if !window.is_visible().unwrap_or(false) {
        return;
    }
    let _ = window.hide();
    if let Some(state) = app.try_state::<TrayState>() {
        state.lock().popover_hidden_at = Some(Instant::now());
    }
}

/// Focus loss closes the popover, except for the blur that accompanies its
/// own appearance.
pub fn on_popover_blur(app: &AppHandle) {
    let settling = app.try_state::<TrayState>().is_some_and(|state| {
        state
            .lock()
            .popover_shown_at
            .is_some_and(|at| at.elapsed() < POPOVER_BLUR_GUARD)
    });
    if !settling {
        hide_popover(app);
    }
}

/// The popover reports its content height; the window follows it and stays
/// anchored to the tray icon.
pub fn resize_popover(app: &AppHandle, height: f64) -> Result<(), String> {
    if !height.is_finite() {
        return Err("popover height must be finite".to_string());
    }
    let height = height.clamp(POPOVER_MIN_HEIGHT, POPOVER_MAX_HEIGHT).ceil();
    let Some(state) = app.try_state::<TrayState>() else {
        return Ok(());
    };
    let anchor = {
        let mut runtime = state.lock();
        runtime.popover_height = height;
        runtime.anchor
    };
    if let Some(anchor) = anchor {
        place_popover(app, anchor, height);
    }
    Ok(())
}

// ---------------------------------------------------------------------------
// Usage refresh scheduling
// ---------------------------------------------------------------------------

/// Collects a fresh digest and re-renders. Concurrent requests coalesce.
pub async fn refresh_usage(app: AppHandle, plans: PlanRefresh) {
    let (Some(state), Some(manager)) = (
        app.try_state::<TrayState>(),
        app.try_state::<Arc<CoreManager>>(),
    ) else {
        return;
    };
    if manager.view().phase != CorePhase::Ready {
        return;
    }
    let prefs = preferences_of(&app).tray.usage;
    let previous = {
        let mut runtime = state.lock();
        if runtime.refresh_in_flight {
            return;
        }
        runtime.refresh_in_flight = true;
        match (&runtime.digest, runtime.subscriptions_at) {
            (Some(digest), Some(at)) => Some((digest.subscriptions.clone(), at)),
            _ => None,
        }
    };
    // Whatever happens below, the next refresh must be allowed to run. A
    // collection that hung or panicked used to leave this flag set and
    // freeze the panel until the app was restarted.
    let _in_flight = InFlightGuard(state.inner());

    let collected = if prefs.needs_anything() {
        let manager = Arc::clone(manager.inner());
        match tokio::time::timeout(
            DIGEST_TIMEOUT,
            collect_digest(
                &manager,
                &prefs,
                previous
                    .as_ref()
                    .map(|(subscriptions, at)| (subscriptions, *at)),
                plans,
            ),
        )
        .await
        {
            Ok((digest, at)) => Some((Some(digest), at)),
            Err(_) => {
                eprintln!(
                    "tray: usage digest collection exceeded {}s; keeping the previous numbers",
                    DIGEST_TIMEOUT.as_secs()
                );
                None
            }
        }
    } else {
        Some((None, None))
    };
    let Some((digest, subscriptions_at)) = collected else {
        return;
    };
    #[cfg(debug_assertions)]
    eprintln!(
        "tray: usage digest refreshed (today={}, subscriptions={})",
        digest.as_ref().is_some_and(|digest| digest.today.is_some()),
        digest
            .as_ref()
            .map_or(0, |digest| digest.subscriptions.len())
    );
    {
        let mut runtime = state.lock();
        runtime.digest = digest;
        runtime.digest_at = Some(Instant::now());
        runtime.subscriptions_at = subscriptions_at;
    }
    refresh(&app);
}

/// Clears `refresh_in_flight` when the refresh ends, however it ends.
struct InFlightGuard<'a>(&'a TrayState);

impl Drop for InFlightGuard<'_> {
    fn drop(&mut self) {
        self.0.lock().refresh_in_flight = false;
    }
}

/// What a scheduler tick should do: `None` skips the tick, otherwise the
/// local numbers are collected and plans follow the returned policy.
fn tick_plan(app: &AppHandle) -> Option<PlanRefresh> {
    let visible = popover_visible(app);
    let (digest_at, subscriptions_at) = app
        .try_state::<TrayState>()
        .map(|state| {
            let runtime = state.lock();
            (runtime.digest_at, runtime.subscriptions_at)
        })
        .unwrap_or((None, None));
    if visible {
        return Some(PlanRefresh::IfOlderThan(PLAN_REFRESH_VISIBLE));
    }
    let local_due = digest_at.is_none_or_older(LOCAL_REFRESH_HIDDEN);
    if !local_due {
        return None;
    }
    let plans = if preferences_of(app).tray.menubar_text == TrayMenubarText::Subscription
        && subscriptions_at.is_none_or_older(PLAN_REFRESH_BACKGROUND)
    {
        PlanRefresh::IfOlderThan(PLAN_REFRESH_BACKGROUND)
    } else {
        PlanRefresh::Reuse
    };
    Some(plans)
}

/// `Option<Instant>` helper spelled out for the 1.77 MSRV.
trait InstantAge {
    fn is_none_or_older(&self, age: Duration) -> bool;
}

impl InstantAge for Option<Instant> {
    fn is_none_or_older(&self, age: Duration) -> bool {
        match self {
            Some(at) => at.elapsed() >= age,
            None => true,
        }
    }
}

fn popover_visible(app: &AppHandle) -> bool {
    app.get_webview_window(POPOVER_LABEL)
        .and_then(|window| window.is_visible().ok())
        .unwrap_or(false)
}

/// Schedules a usage refresh. Clicks are debounced; state transitions are not.
pub fn request_usage_refresh(app: &AppHandle, immediate: bool, plans: PlanRefresh) {
    if let Some(state) = app.try_state::<TrayState>() {
        if !immediate {
            let mut runtime = state.lock();
            if runtime
                .last_click_refresh
                .is_some_and(|at| at.elapsed() < CLICK_REFRESH_DEBOUNCE)
            {
                return;
            }
            runtime.last_click_refresh = Some(Instant::now());
        }
    }
    let app = app.clone();
    tauri::async_runtime::spawn(refresh_usage(app, plans));
}

/// Starts the watcher that follows Core state and the usage ticker.
pub fn start(app: &AppHandle) {
    let Some(manager) = app.try_state::<Arc<CoreManager>>() else {
        return;
    };
    let mut changes = manager.subscribe();
    let watcher = app.clone();
    tauri::async_runtime::spawn(async move {
        let mut was_ready = false;
        while changes.changed().await.is_ok() {
            let ready = changes.borrow_and_update().phase == CorePhase::Ready;
            if ready && !was_ready {
                // Warm the panel once so the first open is not blank.
                request_usage_refresh(
                    &watcher,
                    true,
                    PlanRefresh::IfOlderThan(PLAN_REFRESH_VISIBLE),
                );
            } else if !ready && was_ready {
                // Numbers from a gateway that is gone would be stale on return.
                if let Some(state) = watcher.try_state::<TrayState>() {
                    let mut runtime = state.lock();
                    runtime.digest = None;
                    runtime.digest_at = None;
                    runtime.subscriptions_at = None;
                }
            }
            was_ready = ready;
            refresh(&watcher);
        }
    });

    let ticker = app.clone();
    tauri::async_runtime::spawn(async move {
        loop {
            tokio::time::sleep(SCHEDULER_TICK).await;
            if let Some(plans) = tick_plan(&ticker) {
                refresh_usage(ticker.clone(), plans).await;
            }
        }
    });

    // Agent-side reads are short bursts; a tight local poll keeps the
    // "being watched" icon honest without any push channel from Core.
    let observer_manager = Arc::clone(manager.inner());
    tauri::async_runtime::spawn(async move {
        loop {
            tokio::time::sleep(OBSERVER_POLL_INTERVAL).await;
            if observer_manager.view().phase != CorePhase::Ready {
                continue;
            }
            // Errors are transient (Core restarting); the next tick retries.
            let _ = observer_manager.poll_observers().await;
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::sidecar::InferencePortFallback;

    fn ready_view() -> CoreView {
        CoreView {
            phase: CorePhase::Ready,
            inference_url: Some("http://127.0.0.1:8317".to_string()),
            core_version: Some("0.1.0".to_string()),
            inference_port_fallback: None,
            last_error: None,
            recovery_attempt: 0,
            recovery_scheduled: false,
            observer_active: false,
        }
    }

    fn digest() -> UsageDigest {
        UsageDigest {
            today: Some(UsageTotals {
                requests: 128,
                failed: 3,
                total_tokens: 1_230_000,
                input_tokens: 1_000_000,
                cache_read_tokens: 410_000,
            }),
            hourly_tokens: vec![0; 24],
            yesterday_tokens: Some(1_000_000),
            top_model: Some(Share {
                name: "claude-sonnet-4".to_string(),
                percent: 62,
            }),
            cost_today: Some(CostDigest {
                amount_usd: 0.834,
                unpriced: 0,
            }),
            top_client: None,
            last_request: None,
            month_tokens: None,
            subscriptions: vec![SubscriptionDigest {
                name: "Codex".to_string(),
                windows: vec![WindowDigest {
                    label: None,
                    limit_window_seconds: Some(18_000),
                    secondary: false,
                    used_percent: 62.0,
                    reset_at: None,
                }],
            }],
        }
    }

    #[test]
    fn icon_and_tooltip_follow_the_gateway_state() {
        let model = tray_model(
            &ready_view(),
            &TrayPreferences::default(),
            Some(&digest()),
            Locale::ZhCN,
        );
        assert_eq!(model.icon, TrayIconState::Ready);
        assert_eq!(model.title, None);
        assert_eq!(model.tooltip, "AstrLink · 网关运行中 · 127.0.0.1:8317");

        let mut stopped = ready_view();
        stopped.phase = CorePhase::Stopped;
        stopped.inference_url = None;
        let model = tray_model(&stopped, &TrayPreferences::default(), None, Locale::En);
        assert_eq!(model.tooltip, "AstrLink · Gateway stopped");
        assert_eq!(model.icon, TrayIconState::Idle);

        let mut starting = ready_view();
        starting.phase = CorePhase::Handshaking;
        let model = tray_model(&starting, &TrayPreferences::default(), None, Locale::En);
        assert_eq!(model.tooltip, "AstrLink · Gateway starting…");
        assert_eq!(model.icon, TrayIconState::Idle);
    }

    #[test]
    fn an_agent_reading_records_switches_to_the_watched_icon() {
        let mut watched = ready_view();
        watched.observer_active = true;
        let model = tray_model(&watched, &TrayPreferences::default(), None, Locale::ZhCN);
        assert_eq!(model.icon, TrayIconState::Watched);
        assert_eq!(
            model.tooltip,
            "AstrLink · 网关运行中 · 127.0.0.1:8317 · Agent 正在通过 MCP 读取"
        );
        // Only a running gateway can be read; the badge drops with it.
        watched.phase = CorePhase::Error;
        let model = tray_model(&watched, &TrayPreferences::default(), None, Locale::ZhCN);
        assert_eq!(model.icon, TrayIconState::Idle);
    }

    #[test]
    fn failed_gateway_shows_error_in_the_tooltip_with_the_idle_icon() {
        let view = CoreView {
            phase: CorePhase::Error,
            inference_url: None,
            core_version: None,
            inference_port_fallback: None,
            last_error: Some("astrlink-core exited with status 1 after the inference port was reported busy by the operating system, twice".to_string()),
            recovery_attempt: 2,
            recovery_scheduled: true,
            observer_active: false,
        };
        let prefs = TrayPreferences {
            menubar_text: TrayMenubarText::Tokens,
            ..TrayPreferences::default()
        };
        let model = tray_model(&view, &prefs, None, Locale::ZhCN);
        assert!(model
            .tooltip
            .starts_with("AstrLink · 网关异常退出 · astrlink-core"));
        assert!(model.tooltip.ends_with('…'));
        assert_eq!(model.icon, TrayIconState::Idle);
        assert_eq!(model.title.as_deref(), Some("!"));
    }

    #[test]
    fn port_fallback_is_named_in_the_tooltip() {
        let mut view = ready_view();
        view.inference_url = Some("http://127.0.0.1:8324".to_string());
        view.inference_port_fallback = Some(InferencePortFallback {
            requested_port: 8317,
            active_port: 8324,
        });
        let model = tray_model(&view, &TrayPreferences::default(), None, Locale::ZhCN);
        assert_eq!(
            model.tooltip,
            "AstrLink · 网关运行中 · 127.0.0.1:8324 · 8317 被占用，已改用 8324"
        );
    }

    #[test]
    fn menubar_title_follows_the_preference() {
        let cases = [
            (TrayMenubarText::None, None),
            (TrayMenubarText::Requests, Some("128")),
            (TrayMenubarText::Tokens, Some("1.2M")),
            (TrayMenubarText::Cost, Some("$0.83")),
            (TrayMenubarText::Subscription, Some("62%")),
            (TrayMenubarText::AlertOnly, None),
        ];
        for (choice, expected) in cases {
            let prefs = TrayPreferences {
                menubar_text: choice,
                ..TrayPreferences::default()
            };
            let model = tray_model(&ready_view(), &prefs, Some(&digest()), Locale::En);
            assert_eq!(model.title.as_deref(), expected, "{choice:?}");
        }
        // Without a digest there is nothing to show yet.
        let prefs = TrayPreferences {
            menubar_text: TrayMenubarText::Tokens,
            ..TrayPreferences::default()
        };
        assert_eq!(
            tray_model(&ready_view(), &prefs, None, Locale::En).title,
            None
        );
    }

    #[test]
    fn formatting_helpers() {
        assert_eq!(format_tokens(912), "912");
        assert_eq!(format_tokens(1_230), "1.2K");
        assert_eq!(format_tokens(348_000), "348K");
        assert_eq!(format_tokens(1_200_000), "1.2M");
        assert_eq!(format_tokens(48_000_000), "48M");
        assert_eq!(format_tokens(2_000_000_000), "2B");
        assert_eq!(format_usd(0.0), "0.00");
        assert_eq!(format_usd(0.004), "<0.01");
        assert_eq!(format_usd(0.834), "0.83");
        assert_eq!(display_address("http://127.0.0.1:8317"), "127.0.0.1:8317");
        assert_eq!(percent_of(41, 100), 41);
        assert_eq!(percent_of(1, 0), 0);
        assert_eq!(truncate("abc", 3), "abc");
        assert_eq!(truncate("abcd", 3), "ab…");
    }

    #[test]
    fn digests_parse_control_api_payloads() {
        let summary = serde_json::json!({
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
        });
        let totals = totals_from(&summary).unwrap();
        assert_eq!(totals.requests, 10);
        assert_eq!(totals.cache_read_tokens, 200);
        let hourly = hourly_from(&summary);
        assert_eq!(hourly.len(), 24);
        assert_eq!(hourly[9], 250);
        assert_eq!(hourly[14], 350);
        assert_eq!(hourly.iter().sum::<i64>(), 600);
        assert!(hourly_from(&serde_json::json!({"totals": {}})).is_empty());
        let top = top_group(&summary, totals.total_tokens).unwrap();
        assert_eq!(top.name, "gpt-5");
        assert_eq!(top.percent, 92);

        let cost =
            cost_from(&serde_json::json!({"amount_usd": "0.834000000", "unpriced": 2})).unwrap();
        assert_eq!(cost.unpriced, 2);
        assert!((cost.amount_usd - 0.834).abs() < 1e-9);

        let tokens = serde_json::json!({"items": [{"id": "tok_a", "name": "Cursor"}, {"id": "tok_b", "name": "Codex CLI"}]});
        let usage = serde_json::json!({"items": [
            {"token_id": "tok_a", "today_tokens": 700, "total_tokens": 1000},
            {"token_id": "tok_b", "today_tokens": 300, "total_tokens": 1000}
        ]});
        let client = top_client_from(&tokens, &usage).unwrap();
        assert_eq!(client.name, "Cursor");
        assert_eq!(client.percent, 70);
        let idle = serde_json::json!({"items": [{"token_id": "tok_a", "today_tokens": 0, "total_tokens": 1}]});
        assert!(top_client_from(&tokens, &idle).is_none());

        let records = serde_json::json!({"items": [{
            "started_at": "2026-09-22T10:00:00Z", "requested_model": "gpt-5",
            "latency_ms": 2100, "status": "failed"
        }]});
        let last = last_request_from(&records).unwrap();
        assert!(last.failed);
        assert_eq!(last.latency_ms, Some(2100));

        let services = serde_json::json!({"items": [
            {"id": "svc_codex", "name": "Codex", "kind": "codex_subscription", "enabled": true},
            {"id": "svc_off", "name": "Off", "kind": "claude_subscription", "enabled": false},
            {"id": "svc_api", "name": "OpenAI", "kind": "openai", "enabled": true}
        ]});
        assert_eq!(
            subscription_services(&services),
            vec![
                ("svc_codex".to_string(), "Codex".to_string()),
                ("svc_off".to_string(), "Off".to_string())
            ]
        );

        let usage = serde_json::json!({
            "service_id": "svc_codex", "fetched_at": "2026-09-22T10:00:00Z",
            "primary": {"used_percent": 62.0, "limit_window_seconds": 18000, "reset_at": "2026-09-22T12:13:00Z"},
            "secondary": {"used_percent": 18.0, "limit_window_seconds": 604800}
        });
        let digest = subscription_from("Codex", &usage).unwrap();
        assert_eq!(digest.windows.len(), 2);
        assert!(digest.windows[1].secondary);
        assert!(digest.windows[0].reset_at.is_some());
        assert!(digest.windows[0].label.is_none());
        assert!(subscription_from("Codex", &serde_json::json!({"service_id": "x"})).is_none());

        // Kimi-style plans report only a named monthly limit; Claude adds
        // per-model weekly caps next to its primary pair.
        let monthly_only = serde_json::json!({
            "service_id": "svc_kimi", "fetched_at": "2026-09-22T10:00:00Z",
            "additional_rate_limits": [
                {"limit_name": "Monthly", "metered_feature": "monthly",
                 "primary": {"used_percent": 41.5, "limit_window_seconds": 2592000}}
            ]
        });
        let digest = subscription_from("Kimi", &monthly_only).unwrap();
        assert_eq!(digest.windows.len(), 1);
        assert_eq!(digest.windows[0].label.as_deref(), Some("Monthly"));
        assert_eq!(digest.windows[0].used_percent, 41.5);

        let claude = serde_json::json!({
            "service_id": "svc_claude", "fetched_at": "2026-09-22T10:00:00Z",
            "primary": {"used_percent": 30.0, "limit_window_seconds": 18000},
            "secondary": {"used_percent": 12.0, "limit_window_seconds": 604800},
            "additional_rate_limits": [
                {"limit_name": "Opus", "primary": {"used_percent": 70.0, "limit_window_seconds": 604800}},
                {"limit_name": "  ", "primary": {"used_percent": 1.0}},
                {"limit_name": "Extra usage", "secondary": {"used_percent": 5.0}}
            ]
        });
        let digest = subscription_from("Claude", &claude).unwrap();
        let labels: Vec<Option<&str>> = digest
            .windows
            .iter()
            .map(|window| window.label.as_deref())
            .collect();
        assert_eq!(
            labels,
            vec![None, None, Some("Opus"), None, Some("Extra usage")]
        );
    }

    #[test]
    fn snapshot_serializes_with_the_frontend_contract() {
        let snapshot = TrayStateSnapshot {
            app_version: "0.1.0".to_string(),
            platform: "macos",
            view: ready_view(),
            digest: Some(digest()),
            digest_age_ms: Some(1200),
            tray: TrayPreferences::default(),
            popover_below: true,
        };
        let value = serde_json::to_value(snapshot).unwrap();
        assert_eq!(value["view"]["phase"], "ready");
        assert_eq!(value["view"]["observer_active"], false);
        assert_eq!(value["popover_below"], true);
        assert_eq!(value["view"]["inference_url"], "http://127.0.0.1:8317");
        assert_eq!(value["digest"]["today"]["requests"], 128);
        assert_eq!(
            value["digest"]["hourly_tokens"].as_array().unwrap().len(),
            24
        );
        assert_eq!(
            value["digest"]["subscriptions"][0]["windows"][0]["used_percent"],
            62.0
        );
        assert_eq!(value["tray"]["pages"][0], "records");

        let action: TrayAction =
            serde_json::from_value(serde_json::json!({"kind": "core", "op": "restart"})).unwrap();
        assert_eq!(
            action,
            TrayAction::Core {
                op: CoreOp::Restart
            }
        );
        let action: TrayAction =
            serde_json::from_value(serde_json::json!({"kind": "navigate", "page": "records"}))
                .unwrap();
        assert_eq!(
            action,
            TrayAction::Navigate {
                page: "records".to_string()
            }
        );
        assert!(
            serde_json::from_value::<TrayAction>(serde_json::json!({"kind": "explode"})).is_err()
        );
    }

    #[test]
    fn local_windows_are_whole_seconds_in_utc() {
        let windows = local_windows(Local::now()).unwrap();
        for value in [
            &windows.today_from,
            &windows.today_to,
            &windows.yesterday_from,
            &windows.month_from,
        ] {
            let parsed = DateTime::parse_from_rfc3339(value).unwrap();
            assert_eq!(parsed.timestamp_subsec_nanos(), 0);
            assert!(value.ends_with('Z'));
        }
        assert!(windows.today_from < windows.today_to);
        assert!(windows.yesterday_from < windows.today_from);
        assert!(windows.month_from <= windows.today_from);
        assert!(!windows.time_zone.is_empty());
    }

    fn work_area() -> WindowBox {
        // A 1440×900 display below a 37-point menu bar.
        WindowBox {
            x: 0.0,
            y: 37.0,
            width: 1440.0,
            height: 863.0,
        }
    }

    #[test]
    fn popover_hangs_below_a_menu_bar_icon_centred_on_it() {
        let icon = WindowBox {
            x: 1200.0,
            y: 0.0,
            width: 30.0,
            height: 37.0,
        };
        let (x, y) = popover_placement(icon, work_area(), (POPOVER_WIDTH, 400.0));
        assert_eq!(x, 1200.0 + 15.0 - POPOVER_WIDTH / 2.0);
        // Hugs the menu bar: the icon's bottom edge plus the gap.
        assert_eq!(y, 37.0 + POPOVER_GAP);
        assert!(anchor_below(icon, work_area()));
    }

    #[test]
    fn popover_stays_inside_the_work_area_near_the_right_edge() {
        let icon = WindowBox {
            x: 1400.0,
            y: 0.0,
            width: 30.0,
            height: 37.0,
        };
        let (x, _) = popover_placement(icon, work_area(), (POPOVER_WIDTH, 400.0));
        assert_eq!(x, 1440.0 - POPOVER_WIDTH - POPOVER_MARGIN_X);
    }

    #[test]
    fn popover_rises_above_a_taskbar_icon() {
        // Windows: taskbar at the bottom, work area ends above it.
        let work = WindowBox {
            x: 0.0,
            y: 0.0,
            width: 1920.0,
            height: 1032.0,
        };
        let icon = WindowBox {
            x: 1700.0,
            y: 1040.0,
            width: 24.0,
            height: 24.0,
        };
        let (x, y) = popover_placement(icon, work, (POPOVER_WIDTH, 480.0));
        assert_eq!(y, 1032.0 - 480.0 - POPOVER_MARGIN_Y);
        assert_eq!(x, 1700.0 + 12.0 - POPOVER_WIDTH / 2.0);
        assert!(!anchor_below(icon, work));
    }

    #[test]
    fn popover_never_leaves_a_monitor_smaller_than_itself() {
        let work = WindowBox {
            x: -400.0,
            y: 0.0,
            width: 300.0,
            height: 200.0,
        };
        let icon = WindowBox {
            x: -200.0,
            y: 0.0,
            width: 20.0,
            height: 20.0,
        };
        let (x, y) = popover_placement(icon, work, (POPOVER_WIDTH, 480.0));
        assert_eq!(x, -400.0 + POPOVER_MARGIN_X);
        assert_eq!(y, POPOVER_MARGIN_Y);
    }
}
