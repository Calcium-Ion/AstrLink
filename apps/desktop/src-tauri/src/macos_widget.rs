//! Feeds the macOS desktop widget (`apps/desktop/macos-widget`). The host
//! writes the tray's plan digest into the App Group container the widget
//! extension reads, then asks WidgetKit to redraw. The widget never calls Core
//! or a provider: plan quotas are read on the tray's cadence only, so the
//! widget adds no upstream request of its own.
//!
//! Only a bundled build signed with an App Group feeds a widget. An unbundled
//! development run has none and writes nothing, and the development bundle
//! names its own group, so it never overwrites the release widget's data.

// Off macOS only `publish` and `placed` are called, as no-ops; the writer and
// reload policy still build there for their tests.
#![cfg_attr(not(target_os = "macos"), allow(dead_code))]

use std::{io, path::Path, time::Duration};

use chrono::{DateTime, SecondsFormat, Utc};
use serde::{Serialize, Serializer};

use crate::{
    i18n::Locale,
    preferences::QuotaDisplayMode,
    tray::{SubscriptionDigest, WindowDigest},
};

/// `QuotaSnapshot.version` in the extension; it ignores any other version.
pub const SNAPSHOT_VERSION: u32 = 1;
/// `QuotaSnapshot.fileName` in the extension.
const FILE_NAME: &str = "quota-widget.json";
/// Reloads for changed numbers are at least this far apart. WidgetKit
/// budgets reloads, and a burst of preference edits needs only the last.
const RELOAD_MIN_INTERVAL: Duration = Duration::from_secs(60);
/// A read that changed nothing but its time reaches the widget before the
/// widget's own stale mark (ten minutes) would show.
const FRESHNESS_RELOAD: Duration = Duration::from_secs(8 * 60);
/// How often the host asks WidgetKit whether a widget is placed.
#[cfg(target_os = "macos")]
const PLACED_QUERY_INTERVAL: Duration = Duration::from_secs(60);

/// The file the extension reads (`QuotaSnapshot` in `QuotaSnapshot.swift`).
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct WidgetSnapshot {
    pub version: u32,
    /// When the host last read the plan quotas.
    #[serde(serialize_with = "seconds")]
    pub generated_at: DateTime<Utc>,
    pub locale: Locale,
    pub quota_display: QuotaDisplayMode,
    /// In tray order.
    pub plans: Vec<WidgetPlan>,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct WidgetPlan {
    /// The service id, so a configured widget keeps its plan across renames.
    pub id: String,
    pub name: String,
    pub kind: String,
    pub windows: Vec<WidgetWindow>,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct WidgetWindow {
    pub label: Option<String>,
    pub limit_window_seconds: Option<i64>,
    pub secondary: bool,
    pub used_percent: f64,
    #[serde(serialize_with = "optional_seconds")]
    pub reset_at: Option<DateTime<Utc>>,
}

/// Whole seconds: Foundation's ISO 8601 decoder rejects fractions.
fn seconds<S: Serializer>(value: &DateTime<Utc>, serializer: S) -> Result<S::Ok, S::Error> {
    serializer.serialize_str(&value.to_rfc3339_opts(SecondsFormat::Secs, true))
}

fn optional_seconds<S: Serializer>(
    value: &Option<DateTime<Utc>>,
    serializer: S,
) -> Result<S::Ok, S::Error> {
    match value {
        Some(value) => seconds(value, serializer),
        None => serializer.serialize_none(),
    }
}

impl WidgetSnapshot {
    pub fn new(
        plans: &[SubscriptionDigest],
        read_at: DateTime<Utc>,
        locale: Locale,
        quota_display: QuotaDisplayMode,
    ) -> Self {
        Self {
            version: SNAPSHOT_VERSION,
            generated_at: read_at,
            locale,
            quota_display,
            plans: plans
                .iter()
                .map(|plan| WidgetPlan {
                    id: plan.id.clone(),
                    name: plan.name.clone(),
                    kind: plan.kind.clone(),
                    windows: plan.windows.iter().map(WidgetWindow::from).collect(),
                })
                .collect(),
        }
    }

    /// Whether the widget would draw anything different, ignoring when the
    /// numbers were read.
    fn same_display(&self, other: &Self) -> bool {
        self.locale == other.locale
            && self.quota_display == other.quota_display
            && self.plans == other.plans
    }
}

impl From<&WindowDigest> for WidgetWindow {
    fn from(window: &WindowDigest) -> Self {
        Self {
            label: window.label.clone(),
            limit_window_seconds: window.limit_window_seconds,
            secondary: window.secondary,
            used_percent: window.used_percent,
            reset_at: window.reset_at,
        }
    }
}

/// Replaces the snapshot in one rename, so the extension never reads half a
/// file.
fn write_snapshot(directory: &Path, snapshot: &WidgetSnapshot) -> io::Result<()> {
    let bytes = serde_json::to_vec(snapshot).map_err(io::Error::other)?;
    let partial = directory.join(format!("{FILE_NAME}.partial"));
    std::fs::write(&partial, bytes)?;
    std::fs::rename(&partial, directory.join(FILE_NAME))
}

#[derive(Debug, PartialEq, Eq)]
enum Reload {
    Now,
    After(Duration),
    Skip,
}

/// Changed numbers redraw at once (spaced by `RELOAD_MIN_INTERVAL`); a read
/// that only moved the time redraws when the widget would otherwise soon
/// call its numbers stale.
fn reload_for(
    previous: Option<&WidgetSnapshot>,
    next: &WidgetSnapshot,
    since_reload: Option<Duration>,
) -> Reload {
    // `map_or` rather than `is_none_or` for the 1.77 MSRV.
    let changed = previous.map_or(true, |previous| !previous.same_display(next));
    match since_reload {
        None => Reload::Now,
        Some(elapsed) if changed && elapsed < RELOAD_MIN_INTERVAL => {
            Reload::After(RELOAD_MIN_INTERVAL - elapsed)
        }
        Some(_) if changed => Reload::Now,
        Some(elapsed) if elapsed >= FRESHNESS_RELOAD => Reload::Now,
        Some(_) => Reload::Skip,
    }
}

#[cfg(target_os = "macos")]
mod bridge {
    use std::{ffi::CStr, os::raw::c_char, path::PathBuf};

    extern "C" {
        fn astrlink_widget_container() -> *mut c_char;
        fn astrlink_widget_reload();
        fn astrlink_widget_query_placed();
        fn astrlink_widget_placed() -> i32;
    }

    pub fn container() -> Option<PathBuf> {
        // SAFETY: the bridge returns null or a malloc'd NUL-terminated string
        // that this function owns and frees.
        unsafe {
            let raw = astrlink_widget_container();
            if raw.is_null() {
                return None;
            }
            let path = CStr::from_ptr(raw).to_string_lossy().into_owned();
            libc::free(raw.cast());
            Some(PathBuf::from(path))
        }
    }

    pub fn reload() {
        // SAFETY: no arguments; WidgetCenter is safe to call from any thread.
        unsafe { astrlink_widget_reload() }
    }

    pub fn query_placed() {
        // SAFETY: starts an asynchronous query; no arguments.
        unsafe { astrlink_widget_query_placed() }
    }

    pub fn placed() -> i32 {
        // SAFETY: reads a lock-protected integer.
        unsafe { astrlink_widget_placed() }
    }
}

#[cfg(target_os = "macos")]
mod feed {
    use std::{
        path::PathBuf,
        sync::{Mutex, MutexGuard, OnceLock},
        time::Instant,
    };

    use super::{
        bridge, reload_for, write_snapshot, Reload, WidgetSnapshot, PLACED_QUERY_INTERVAL,
    };

    #[derive(Default)]
    struct Feed {
        written: Option<WidgetSnapshot>,
        reloaded_at: Option<Instant>,
        reload_pending: bool,
        placed_queried_at: Option<Instant>,
    }

    fn container() -> Option<&'static PathBuf> {
        static CONTAINER: OnceLock<Option<PathBuf>> = OnceLock::new();
        CONTAINER.get_or_init(bridge::container).as_ref()
    }

    fn feed() -> MutexGuard<'static, Feed> {
        static FEED: OnceLock<Mutex<Feed>> = OnceLock::new();
        FEED.get_or_init(Mutex::default)
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }

    pub fn publish(snapshot: &WidgetSnapshot) {
        let Some(directory) = container() else {
            return;
        };
        let mut feed = feed();
        if feed.written.as_ref() == Some(snapshot) {
            return;
        }
        if let Err(error) = write_snapshot(directory, snapshot) {
            eprintln!("unable to write the AstrLink widget snapshot: {error}");
            return;
        }
        let reload = reload_for(
            feed.written.as_ref(),
            snapshot,
            feed.reloaded_at.map(|at| at.elapsed()),
        );
        feed.written = Some(snapshot.clone());
        match reload {
            Reload::Now => {
                feed.reloaded_at = Some(Instant::now());
                bridge::reload();
            }
            Reload::After(wait) if !feed.reload_pending => {
                feed.reload_pending = true;
                std::thread::spawn(move || {
                    std::thread::sleep(wait);
                    let mut feed = super::feed::feed();
                    feed.reload_pending = false;
                    feed.reloaded_at = Some(Instant::now());
                    bridge::reload();
                });
            }
            Reload::After(_) | Reload::Skip => {}
        }
    }

    pub fn placed() -> bool {
        if container().is_none() {
            return false;
        }
        let mut feed = feed();
        if feed
            .placed_queried_at
            .map_or(true, |at| at.elapsed() >= PLACED_QUERY_INTERVAL)
        {
            feed.placed_queried_at = Some(Instant::now());
            bridge::query_placed();
        }
        bridge::placed() > 0
    }
}

/// Writes the snapshot for the widget if it changed, and asks WidgetKit to
/// redraw. Does nothing without a widget to feed.
pub fn publish(snapshot: &WidgetSnapshot) {
    #[cfg(target_os = "macos")]
    feed::publish(snapshot);
    #[cfg(not(target_os = "macos"))]
    let _ = snapshot;
}

/// Whether an AstrLink widget is on the desktop. A placed widget shows plan
/// figures like the menu-bar title does, so plans refresh on the same
/// background cadence; without one, nothing extra is read.
pub fn placed() -> bool {
    #[cfg(target_os = "macos")]
    return feed::placed();
    #[cfg(not(target_os = "macos"))]
    false
}

#[cfg(test)]
mod tests {
    use super::*;

    fn digest() -> Vec<SubscriptionDigest> {
        let at = |text: &str| {
            DateTime::parse_from_rfc3339(text)
                .unwrap()
                .with_timezone(&Utc)
        };
        vec![
            SubscriptionDigest {
                id: "svc_codex".to_string(),
                name: "Codex".to_string(),
                kind: "codex_subscription".to_string(),
                windows: vec![
                    WindowDigest {
                        label: None,
                        limit_window_seconds: Some(18_000),
                        secondary: false,
                        used_percent: 36.0,
                        // Upstream times can carry fractions; the file must not.
                        reset_at: Some(at("2026-11-01T09:18:00.250Z")),
                    },
                    WindowDigest {
                        label: None,
                        limit_window_seconds: Some(604_800),
                        secondary: true,
                        used_percent: 59.0,
                        reset_at: Some(at("2026-11-04T01:00:00Z")),
                    },
                ],
            },
            SubscriptionDigest {
                id: "svc_kimi".to_string(),
                name: "Kimi Coding".to_string(),
                kind: "kimi_coding".to_string(),
                windows: vec![WindowDigest {
                    label: Some("Monthly".to_string()),
                    limit_window_seconds: None,
                    secondary: false,
                    used_percent: 21.0,
                    reset_at: None,
                }],
            },
        ]
    }

    fn snapshot() -> WidgetSnapshot {
        WidgetSnapshot::new(
            &digest(),
            DateTime::parse_from_rfc3339("2026-11-01T07:20:00.900Z")
                .unwrap()
                .with_timezone(&Utc),
            Locale::ZhCN,
            QuotaDisplayMode::Remaining,
        )
    }

    #[test]
    fn snapshot_matches_the_extension_fixture() {
        // The Swift tests decode the same fixture (QuotaModelTests.swift).
        let fixture: serde_json::Value = serde_json::from_str(include_str!(
            "../../macos-widget/Tests/snapshot-fixture.json"
        ))
        .unwrap();
        assert_eq!(serde_json::to_value(snapshot()).unwrap(), fixture);
    }

    #[test]
    fn snapshot_is_replaced_whole() {
        let directory = std::env::temp_dir().join(format!(
            "astrlink-widget-test-{}-{}",
            std::process::id(),
            Utc::now().timestamp_nanos_opt().unwrap_or_default()
        ));
        std::fs::create_dir_all(&directory).unwrap();
        write_snapshot(&directory, &snapshot()).unwrap();
        let mut next = snapshot();
        next.plans.truncate(1);
        write_snapshot(&directory, &next).unwrap();
        let written: serde_json::Value =
            serde_json::from_slice(&std::fs::read(directory.join(FILE_NAME)).unwrap()).unwrap();
        assert_eq!(written, serde_json::to_value(&next).unwrap());
        assert!(!directory.join(format!("{FILE_NAME}.partial")).exists());
        std::fs::remove_dir_all(&directory).unwrap();
    }

    #[test]
    fn reloads_follow_what_the_widget_would_draw() {
        let first = snapshot();
        let mut later_read = first.clone();
        later_read.generated_at += chrono::Duration::minutes(5);
        let mut changed = first.clone();
        changed.plans[0].windows[0].used_percent = 50.0;
        let mut english = first.clone();
        english.locale = Locale::En;

        assert_eq!(reload_for(None, &first, None), Reload::Now);
        assert_eq!(
            reload_for(Some(&first), &changed, Some(Duration::from_secs(300))),
            Reload::Now
        );
        assert_eq!(
            reload_for(Some(&first), &english, Some(Duration::from_secs(20))),
            Reload::After(Duration::from_secs(40))
        );
        // Only the read time moved: wait until the stale mark draws near.
        assert_eq!(
            reload_for(Some(&first), &later_read, Some(Duration::from_secs(300))),
            Reload::Skip
        );
        assert_eq!(
            reload_for(Some(&first), &later_read, Some(FRESHNESS_RELOAD)),
            Reload::Now
        );
    }

    #[test]
    fn no_widget_is_fed_off_macos_or_without_an_app_group() {
        // `cargo test` runs an unbundled binary with no App Group.
        assert!(!placed());
        publish(&snapshot());
    }
}
