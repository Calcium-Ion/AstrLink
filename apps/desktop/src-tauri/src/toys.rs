//! Desktop toys: the big pet outside the main window. She lives in her own
//! transparent webview window and draws her orbs with the overview's crew
//! engine; this module builds and places that window, reads the activity for
//! her, and does what a page cannot: which parts of a click-through window
//! take the mouse, where the other apps' windows are, and how long the
//! keyboard and mouse, and the AI, have been idle.
//!
//! Only macOS has the pet for now. Elsewhere `apply` builds nothing and the
//! commands answer with empty values, so the frontend needs no second path.

#![cfg_attr(not(target_os = "macos"), allow(dead_code))]

use std::{
    collections::HashMap,
    sync::{Arc, Mutex, MutexGuard},
    time::{Duration, Instant},
};

use chrono::{DateTime, SecondsFormat, Utc};
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Manager};

use crate::{
    i18n::Locale,
    preferences::{DesktopToysPreferences, PreferencesStore},
    sidecar::{CoreManager, CorePhase},
};

pub const PET: &str = "toy-pet";
const LABEL_PREFIX: &str = "toy-";

/// The activity and today's tokens, once a second while the pet is out.
pub const FEED_EVENT: &str = "toys:feed";
/// The pet's preferences and the language, whenever they change.
pub const PREFS_EVENT: &str = "toys:prefs";
/// Where the mouse is over the pet's window, in its own points.
pub const POINTER_EVENT: &str = "toys:pointer";
/// Other apps' windows, the Dock and the floor, for the pet.
pub const FRAMES_EVENT: &str = "toys:frames";
/// Seconds since the last key or mouse input, for the big pet's naps.
pub const IDLE_EVENT: &str = "toys:idle";
/// A pet carried onto another screen took her window along: how far its
/// points moved and its new size.
pub const SCREEN_EVENT: &str = "toys:screen";
/// The screen the pet's window lives on, as a point on it, kept across runs.
const HOMES_FILE: &str = "toy-screens.json";

const FEED_INTERVAL: Duration = Duration::from_secs(1);
const TOKENS_INTERVAL: Duration = Duration::from_secs(20);
/// The activity board's window plus its grace, with a minute to spare.
const FEED_WINDOW_MINUTES: i64 = 7;
const TICK: Duration = Duration::from_millis(33);
/// An activity reading older than this says nothing about the AI now.
const ACTIVITY_STALE_SECS: i64 = 60;
/// Seconds without input, once the AI is quiet, that make you away.
const AWAY_SECS: f64 = 600.0;
/// While the AI still works you may be watching it: you count as away only
/// after this many times as long.
const AWAY_BUSY_FACTOR: f64 = 3.0;

/// x, y, width and height in global top-left points.
type Rect = (f64, f64, f64, f64);
type Point = (f64, f64);

/// A screen's frame and the part of it the menu bar and the Dock leave.
#[derive(Clone, Copy, Debug, PartialEq)]
struct Screen {
    frame: Rect,
    visible: Rect,
}

fn holds((x, y, w, h): Rect, (px, py): Point) -> bool {
    px >= x && px < x + w && py >= y && py < y + h
}

fn middle((x, y, w, h): Rect) -> Point {
    (x + w / 2.0, y + h / 2.0)
}

/// The screen a pet window lives on: the one under its home point, or the
/// main one, first in the list, once that screen is gone.
fn home_screen(screens: &[Screen], home: Option<Point>) -> Option<Screen> {
    home.and_then(|point| screens.iter().find(|screen| holds(screen.frame, point)))
        .or_else(|| screens.first())
        .copied()
}

/// The screen the mouse has carried a held pet onto, if her window is not
/// on it yet; none over a gap between screens.
fn carried_to(screens: &[Screen], on: Rect, cursor: Point) -> Option<Screen> {
    screens
        .iter()
        .find(|screen| holds(screen.frame, cursor))
        .filter(|screen| screen.frame != on)
        .copied()
}

/// Another app's window on the screens: its number, its kind and its frame.
struct OtherWindow {
    id: i64,
    kind: &'static str,
    frame: Rect,
}

/// What a pet sees on her screen, in her window's points: the other apps'
/// windows on it front to back, the Dock if it sits at its bottom, the floor
/// and the menu bar.
fn frames_on(screen: &Screen, dock: Option<Rect>, others: &[OtherWindow]) -> serde_json::Value {
    let (mx, my, mw, mh) = screen.frame;
    let mut wins = Vec::new();
    let mut full = false;
    for other in others {
        let (x, y, w, h) = other.frame;
        if (x - mx).abs() < 1.0
            && (y - my).abs() < 1.0
            && (w - mw).abs() < 1.0
            && (h - mh).abs() < 1.0
        {
            full = true;
        }
        if w < 120.0 || h < 60.0 || x + w < mx || x > mx + mw || y + h < my || y > my + mh {
            continue;
        }
        wins.push(serde_json::json!({
            "id": other.id,
            "kind": other.kind,
            "x": x - mx,
            "y": y - my,
            "w": w,
            "h": h,
        }));
        if wins.len() >= 40 {
            break;
        }
    }
    // A Dock kept at the bottom of this screen leaves a strip under the
    // visible frame; hidden, at a side or on another screen, there is none
    // to stand on.
    let (_, vy, _, vh) = screen.visible;
    let dock_height = (my + mh) - (vy + vh);
    let dock = dock
        .filter(|&(_, y, _, h)| dock_height > 8.0 && (y + h - (my + mh)).abs() < 2.0)
        .map(|(x, _, w, _)| ((x - mx).max(0.0), (x + w - mx).min(mw)))
        .filter(|(x0, x1)| x1 - x0 >= 60.0)
        .map(|(x0, x1)| serde_json::json!({ "x0": x0, "x1": x1, "y": vy + vh - my }));
    serde_json::json!({
        "wins": wins,
        "dock": dock,
        "floor": mh - 2.0,
        "top": (vy - my).max(24.0),
        "full": full,
        "w": mw,
        "h": mh,
    })
}

/// A part of a toy window, in its own points, that hovers or takes clicks.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq)]
pub struct HitRect {
    pub x: f64,
    pub y: f64,
    pub w: f64,
    pub h: f64,
    /// Takes clicks; otherwise only hover is reported.
    #[serde(default)]
    pub click: bool,
}

impl HitRect {
    fn contains(&self, x: f64, y: f64) -> bool {
        x >= self.x && x < self.x + self.w && y >= self.y && y < self.y + self.h
    }
}

#[derive(Default)]
struct Hits {
    rects: Vec<HitRect>,
    /// Takes the mouse everywhere: a pet is pressed or held.
    hold: bool,
    /// A pet is in your hand: her window follows the mouse to other screens.
    carry: bool,
    /// Top-left in global points, and its size, as last read.
    origin: Option<(f64, f64, f64, f64)>,
    origin_at: Option<Instant>,
    interactive: bool,
    pointer: Option<(f64, f64, bool)>,
    /// The button as last told, so a click that does not move is told too.
    down: bool,
}

#[derive(Default)]
struct Runtime {
    activity: Option<serde_json::Value>,
    tokens: Option<i64>,
    tokens_at: Option<Instant>,
    hits: HashMap<String, Hits>,
    feeding: bool,
    polling: bool,
    /// Per pet window: what it last heard about its screen.
    frames: HashMap<String, serde_json::Value>,
    /// Per pet window: a point on the screen it lives on; read once.
    homes: Option<HashMap<String, Point>>,
    /// Per pet window: the screen frame it was last put on.
    placed: HashMap<String, Rect>,
    #[cfg(target_os = "macos")]
    geometry: Option<mac::Geometry>,
}

#[derive(Default)]
pub struct ToysState {
    inner: Mutex<Runtime>,
}

impl ToysState {
    fn lock(&self) -> MutexGuard<'_, Runtime> {
        self.inner
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }
}

#[derive(Clone, Serialize)]
pub struct ToyPrefs {
    #[serde(flatten)]
    pub toys: DesktopToysPreferences,
    pub locale: Locale,
}

#[derive(Clone, Serialize)]
pub struct ToyFeed {
    pub activity: Option<serde_json::Value>,
    pub tokens: Option<i64>,
    /// Debug builds only: the toys play the sample conversations instead.
    pub sample: bool,
}

/// Debug builds read `toys-debug.json` in the config directory, so the pet
/// can be checked without paid traffic or minutes of idling:
/// `{"sample": true, "away_after": 20}`.
#[derive(Default, Deserialize)]
struct DebugOverrides {
    #[serde(default)]
    sample: bool,
    away_after: Option<f64>,
}

fn debug_overrides(app: &AppHandle) -> DebugOverrides {
    #[cfg(debug_assertions)]
    if let Ok(dir) = app.path().app_config_dir() {
        if let Ok(text) = std::fs::read_to_string(dir.join("toys-debug.json")) {
            return serde_json::from_str(&text).unwrap_or_default();
        }
    }
    let _ = app;
    DebugOverrides::default()
}

fn homes_path(app: &AppHandle) -> Option<std::path::PathBuf> {
    app.path()
        .app_config_dir()
        .ok()
        .map(|dir| dir.join(HOMES_FILE))
}

/// The pet windows' screens as kept on disk, read the first time.
fn homes(app: &AppHandle) -> HashMap<String, Point> {
    let Some(state) = toys_state(app) else {
        return HashMap::new();
    };
    if let Some(homes) = state.lock().homes.clone() {
        return homes;
    }
    let homes: HashMap<String, Point> = homes_path(app)
        .and_then(|path| std::fs::read_to_string(path).ok())
        .and_then(|text| serde_json::from_str(&text).ok())
        .unwrap_or_default();
    let homes = state.lock().homes.get_or_insert(homes).clone();
    homes
}

fn save_homes(app: &AppHandle, homes: &HashMap<String, Point>) {
    let Some(path) = homes_path(app) else {
        return;
    };
    let result = serde_json::to_vec(homes)
        .map_err(std::io::Error::other)
        .and_then(|bytes| {
            path.parent().map_or(Ok(()), std::fs::create_dir_all)?;
            std::fs::write(&path, bytes)
        });
    if let Err(error) = result {
        eprintln!("desktop toys: unable to keep the pets' screens: {error}");
    }
}

#[derive(Serialize)]
pub struct ToySnapshot {
    pub prefs: ToyPrefs,
    pub feed: ToyFeed,
    pub frames: Option<serde_json::Value>,
}

fn prefs_of(app: &AppHandle) -> ToyPrefs {
    let values = app
        .try_state::<Arc<PreferencesStore>>()
        .map(|store| store.snapshot().values)
        .unwrap_or_default();
    ToyPrefs {
        toys: values.desktop_toys,
        locale: values.locale,
    }
}

fn toys_state(app: &AppHandle) -> Option<tauri::State<'_, ToysState>> {
    app.try_state::<ToysState>()
}

fn toy_labels(app: &AppHandle) -> Vec<String> {
    app.webview_windows()
        .into_keys()
        .filter(|label| label.starts_with(LABEL_PREFIX))
        .collect()
}

/// Whether a window is the pet's: transparent, drawn by its page, never themed.
pub fn is_toy(label: &str) -> bool {
    label.starts_with(LABEL_PREFIX)
}

/// Sends to every toy window.
fn emit_toys<S: Serialize + Clone>(app: &AppHandle, event: &str, payload: S) {
    for label in toy_labels(app) {
        if let Err(error) = app.emit_to(label.as_str(), event, payload.clone()) {
            eprintln!("unable to update the desktop toy {label}: {error}");
        }
    }
}

/// Builds the pet's window if the preferences turn her on, or closes it, then
/// tells her the preferences.
pub fn apply(app: &AppHandle) {
    let prefs = prefs_of(app);
    #[cfg(target_os = "macos")]
    mac::reconcile(app, &prefs.toys);
    emit_toys(app, PREFS_EVENT, prefs.clone());
    if prefs.toys.on.any() {
        start_feed(app);
        #[cfg(target_os = "macos")]
        mac::start_poll(app);
    }
}

/// The language changed: the pet says things.
pub fn prefs_changed(app: &AppHandle) {
    emit_toys(app, PREFS_EVENT, prefs_of(app));
}

fn start_feed(app: &AppHandle) {
    let Some(state) = toys_state(app) else {
        return;
    };
    {
        let mut runtime = state.lock();
        if runtime.feeding {
            return;
        }
        runtime.feeding = true;
    }
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        loop {
            if !prefs_of(&app).toys.on.any() {
                break;
            }
            feed_once(&app).await;
            tokio::time::sleep(FEED_INTERVAL).await;
        }
        if let Some(state) = toys_state(&app) {
            state.lock().feeding = false;
        }
    });
}

async fn feed_once(app: &AppHandle) {
    let sample = debug_overrides(app).sample;
    if sample {
        let feed = ToyFeed {
            activity: None,
            tokens: None,
            sample,
        };
        emit_toys(app, FEED_EVENT, feed);
        return;
    }
    let Some(manager) = app.try_state::<Arc<CoreManager>>().map(|m| Arc::clone(&m)) else {
        return;
    };
    if manager.view().phase != CorePhase::Ready {
        return;
    }
    let since = (Utc::now() - chrono::Duration::minutes(FEED_WINDOW_MINUTES))
        .to_rfc3339_opts(SecondsFormat::Millis, true);
    let activity = manager.get_request_activity(&since).await.ok();
    let tokens_due = toys_state(app).map_or(true, |state| {
        state
            .lock()
            .tokens_at
            .map_or(true, |at| at.elapsed() >= TOKENS_INTERVAL)
    });
    let mut tokens = toys_state(app).and_then(|state| state.lock().tokens);
    if tokens_due {
        if let Some(windows) = crate::tray::local_windows(chrono::Local::now()) {
            if let Ok(summary) = manager
                .get_usage_summary(
                    &windows.today_from,
                    &windows.today_to,
                    &windows.time_zone,
                    "hour",
                )
                .await
            {
                tokens = crate::tray::totals_from(&summary).map(|totals| totals.total_tokens);
            }
        }
    }
    if let Some(state) = toys_state(app) {
        let mut runtime = state.lock();
        if activity.is_some() {
            runtime.activity = activity.clone();
        }
        if tokens_due {
            runtime.tokens = tokens;
            runtime.tokens_at = Some(Instant::now());
        }
    }
    if activity.is_none() {
        return;
    }
    let feed = ToyFeed {
        activity,
        tokens,
        sample: false,
    };
    emit_toys(app, FEED_EVENT, feed);
}

/// Seconds since the AI last did anything in this activity reading: zero
/// while a call runs, infinite when nothing ran in its window or the reading
/// is stale.
fn ai_quiet_seconds(activity: Option<&serde_json::Value>, now: DateTime<Utc>) -> f64 {
    let at = |value: &serde_json::Value| {
        value
            .as_str()
            .and_then(|text| DateTime::parse_from_rfc3339(text).ok())
            .map(|time| time.with_timezone(&Utc))
    };
    let Some(activity) = activity else {
        return f64::INFINITY;
    };
    match activity.get("now").and_then(at) {
        Some(read) if (now - read).num_seconds() <= ACTIVITY_STALE_SECS => {}
        _ => return f64::INFINITY,
    }
    let calls = activity.get("calls").and_then(|calls| calls.as_array());
    let mut last: Option<DateTime<Utc>> = None;
    for call in calls.into_iter().flatten() {
        match call.get("completed_at").and_then(at) {
            Some(ended) => last = last.max(Some(ended)),
            None => return 0.0,
        }
    }
    last.map_or(f64::INFINITY, |ended| {
        (now - ended).num_milliseconds().max(0) as f64 / 1000.0
    })
}

/// The two waits before you count as away, in seconds: once the AI is quiet,
/// and while it works. A debug override sets both.
fn away_waits(debug: Option<f64>) -> (f64, f64) {
    if let Some(seconds) = debug {
        let seconds = seconds.max(5.0);
        return (seconds, seconds);
    }
    (AWAY_SECS, AWAY_SECS * AWAY_BUSY_FACTOR)
}

/// Seconds without input that make you away: the idle wait once the AI has
/// been quiet that long, the longer busy wait while it works.
fn away_after(idle_wait: f64, busy_wait: f64, ai_quiet: f64) -> f64 {
    if ai_quiet >= idle_wait {
        idle_wait
    } else {
        busy_wait.max(idle_wait)
    }
}

/// What the pet's page needs once it has loaded.
#[tauri::command]
pub fn toys_snapshot(app: AppHandle, window: tauri::WebviewWindow) -> ToySnapshot {
    let (activity, tokens, frames) = toys_state(&app)
        .map(|state| {
            let runtime = state.lock();
            (
                runtime.activity.clone(),
                runtime.tokens,
                runtime.frames.get(window.label()).cloned(),
            )
        })
        .unwrap_or((None, None, None));
    ToySnapshot {
        prefs: prefs_of(&app),
        feed: ToyFeed {
            activity,
            tokens,
            sample: debug_overrides(&app).sample,
        },
        frames,
    }
}

/// Saves the pet's preferences and applies them at once.
#[tauri::command]
pub(crate) async fn update_desktop_toys(
    app: AppHandle,
    input: DesktopToysPreferences,
) -> Result<crate::SettingsSnapshot, String> {
    let store = app.state::<Arc<PreferencesStore>>();
    store.replace_desktop_toys(input)?;
    apply(&app);
    Ok(crate::settings_snapshot(&app, store.inner()))
}

/// A toy window says which of its parts hover or take clicks; `hold` keeps
/// the whole window taking the mouse while something is pressed or dragged,
/// and `carry` (`hold` if left out) lets a dragged pet take her window to
/// another screen.
#[tauri::command]
pub fn toys_hit(
    window: tauri::WebviewWindow,
    rects: Vec<HitRect>,
    hold: bool,
    carry: Option<bool>,
) {
    let app = window.app_handle();
    let Some(state) = toys_state(app) else {
        return;
    };
    let mut runtime = state.lock();
    let hits = runtime.hits.entry(window.label().to_string()).or_default();
    hits.rects = rects.into_iter().take(64).collect();
    hits.hold = hold;
    hits.carry = hold && carry.unwrap_or(hold);
    hits.origin_at = None;
}

#[cfg(target_os = "macos")]
mod mac {
    use std::ffi::c_void;

    use core_foundation::{
        array::{CFArray, CFArrayRef},
        base::{CFType, CFTypeRef, TCFType},
        dictionary::CFDictionary,
        number::CFNumber,
        string::{CFString, CFStringRef},
    };
    use objc2::{runtime::NSObjectProtocol, MainThreadMarker};
    use objc2_app_kit::{NSScreen, NSWindow, NSWindowCollectionBehavior};
    use objc2_foundation::{NSPoint, NSRect, NSSize};
    use tauri::{WebviewUrl, WebviewWindowBuilder};

    use super::*;

    #[repr(C)]
    #[derive(Clone, Copy, Default)]
    struct CGPoint {
        x: f64,
        y: f64,
    }

    #[repr(C)]
    #[derive(Clone, Copy, Default)]
    struct CGRect {
        origin: CGPoint,
        size: CGPoint,
    }

    #[link(name = "CoreGraphics", kind = "framework")]
    extern "C" {
        fn CGEventCreate(source: *const c_void) -> *mut c_void;
        fn CGEventGetLocation(event: *mut c_void) -> CGPoint;
        fn CGEventSourceSecondsSinceLastEventType(state: i32, event_type: u32) -> f64;
        fn CGEventSourceButtonState(state: i32, button: u32) -> bool;
        fn CGWindowListCopyWindowInfo(option: u32, relative_to: u32) -> CFArrayRef;
        fn CGRectMakeWithDictionaryRepresentation(dict: CFTypeRef, rect: *mut CGRect) -> bool;
        static kCGWindowLayer: CFStringRef;
        static kCGWindowBounds: CFStringRef;
        static kCGWindowOwnerPID: CFStringRef;
        static kCGWindowOwnerName: CFStringRef;
        static kCGWindowNumber: CFStringRef;
        static kCGWindowAlpha: CFStringRef;
    }

    #[link(name = "CoreFoundation", kind = "framework")]
    extern "C" {
        fn CFRelease(cf: *const c_void);
    }

    const HID_STATE: i32 = 1;
    const COMBINED_STATE: i32 = 0;
    const ANY_INPUT: u32 = !0;
    const ON_SCREEN_ONLY: u32 = 1;
    const EXCLUDE_DESKTOP: u32 = 16;

    /// Above the Dock (20), under the menu bar (24) and its menus.
    const PET_LEVEL: isize = 21;

    /// Screens in global top-left points.
    #[derive(Clone, Debug, Default)]
    pub struct Geometry {
        /// The screen with the menu bar: the pet's until you carry her
        /// elsewhere.
        pub main: Rect,
        /// Every screen, the main one first.
        pub screens: Vec<Screen>,
        /// The Dock's own frame, as the Dock reports it.
        pub dock: Option<Rect>,
    }

    fn cocoa_rect(rect: NSRect) -> Rect {
        (
            rect.origin.x,
            rect.origin.y,
            rect.size.width,
            rect.size.height,
        )
    }

    /// Reads the screens. AppKit needs the main thread.
    pub fn geometry(mtm: MainThreadMarker) -> Option<Geometry> {
        let screens = NSScreen::screens(mtm);
        let main = screens.firstObject()?;
        let (_, _, _, main_height) = cocoa_rect(main.frame());
        let flip = |(x, y, w, h): Rect| (x, main_height - (y + h), w, h);
        let list: Vec<Screen> = screens
            .iter()
            .map(|screen| Screen {
                frame: flip(cocoa_rect(screen.frame())),
                visible: flip(cocoa_rect(screen.visibleFrame())),
            })
            .collect();
        Some(Geometry {
            main: list.first()?.frame,
            screens: list,
            dock: dock_rect(),
        })
    }

    /// The Dock's frame in global top-left points, from the Dock itself: its
    /// width follows every icon, kept or running, which no count of the
    /// Dock's preferences gets right. Private API, looked up at run time so a
    /// macOS without it only loses the Dock as a place to stand.
    fn dock_rect() -> Option<Rect> {
        type GetRect = unsafe extern "C" fn(*mut CGRect);
        static GET: std::sync::OnceLock<Option<GetRect>> = std::sync::OnceLock::new();
        let get = GET.get_or_init(|| {
            // SAFETY: a symbol lookup in the images already loaded.
            let symbol = unsafe { libc::dlsym(libc::RTLD_DEFAULT, c"CoreDockGetRect".as_ptr()) };
            if symbol.is_null() {
                eprintln!("desktop toys: no CoreDockGetRect; pets will not stand on the Dock");
                return None;
            }
            // SAFETY: CoreDockGetRect takes one CGRect out-pointer.
            Some(unsafe { std::mem::transmute::<*mut c_void, GetRect>(symbol) })
        });
        let mut rect = CGRect::default();
        // SAFETY: `rect` outlives the call, which only writes into it.
        unsafe { (*get)?(&mut rect) };
        let (x, y, w, h) = (rect.origin.x, rect.origin.y, rect.size.x, rect.size.y);
        (w > 0.0 && h > 0.0).then_some((x, y, w, h))
    }

    fn with_ns_window(window: &tauri::WebviewWindow, f: impl FnOnce(&NSWindow) + Send + 'static) {
        let handle = window.clone();
        let _ = window.run_on_main_thread(move || {
            let Ok(pointer) = handle.ns_window() else {
                return;
            };
            // SAFETY: tao owns this NSWindow while `handle` lives, and this runs
            // on the main thread, where AppKit requires it.
            let ns_window = unsafe { &*pointer.cast::<NSWindow>() };
            f(ns_window);
        });
    }

    /// A click on the pet must not activate AstrLink: activation brings the main
    /// window forward with it. A non-activating NSPanel sets this flag; tao's
    /// window is an NSWindow, so it is set here directly.
    fn prevent_activation(ns_window: &NSWindow) {
        let selector = objc2::sel!(_setPreventsActivation:);
        if ns_window.respondsToSelector(selector) {
            // SAFETY: NSWindow answers this selector with one BOOL and no result,
            // and this runs on the main thread.
            let _: () = unsafe { objc2::msg_send![ns_window, _setPreventsActivation: true] };
        } else {
            eprintln!("desktop toys: this macOS lets a click on the pet activate AstrLink");
        }
    }

    /// Cocoa frame for a top-left global rect.
    fn to_cocoa(main_height: f64, (x, y, w, h): Rect) -> NSRect {
        NSRect::new(NSPoint::new(x, main_height - (y + h)), NSSize::new(w, h))
    }

    /// The pet's window: her whole screen, the one under her home point.
    fn placement(geometry: &Geometry, home: Option<Point>) -> Rect {
        home_screen(&geometry.screens, home).map_or(geometry.main, |s| s.frame)
    }

    fn build(app: &AppHandle, label: &'static str, geometry: &Geometry, home: Option<Point>) {
        let (x, y, w, h) = placement(geometry, home);
        let builder = WebviewWindowBuilder::new(app, label, WebviewUrl::default())
            .title("AstrLink")
            .inner_size(w, h)
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
            .focused(false)
            .focusable(false)
            .visible_on_all_workspaces(true)
            .accept_first_mouse(true)
            .background_throttling(tauri::utils::config::BackgroundThrottlingPolicy::Disabled);
        let window = match builder.build() {
            Ok(window) => window,
            Err(error) => {
                eprintln!("unable to open the desktop toy {label}: {error}");
                return;
            }
        };
        if let Some(state) = super::toys_state(app) {
            state.lock().placed.insert(label.to_string(), (x, y, w, h));
        }
        let main_height = geometry.main.3;
        let frame = to_cocoa(main_height, (x, y, w, h));
        // She floats where she was left, over full-screen apps too.
        let behavior = NSWindowCollectionBehavior::CanJoinAllSpaces
            | NSWindowCollectionBehavior::Stationary
            | NSWindowCollectionBehavior::IgnoresCycle
            | NSWindowCollectionBehavior::FullScreenAuxiliary;
        with_ns_window(&window, move |ns_window| {
            ns_window.setLevel(PET_LEVEL);
            ns_window.setCollectionBehavior(behavior);
            ns_window.setHasShadow(false);
            ns_window.setIgnoresMouseEvents(true);
            prevent_activation(ns_window);
            // A borderless window may cover the menu bar; set the frame
            // directly instead of letting AppKit keep it below.
            ns_window.setFrame_display(frame, false);
            ns_window.orderFrontRegardless();
        });
    }

    pub fn reconcile(app: &AppHandle, toys: &DesktopToysPreferences) {
        let wanted = [(PET, toys.on.pet)];
        let app = app.clone();
        let homes = super::homes(&app);
        let _ = app.clone().run_on_main_thread(move || {
            let Some(mtm) = MainThreadMarker::new() else {
                return;
            };
            let Some(geometry) = geometry(mtm) else {
                return;
            };
            if let Some(state) = super::toys_state(&app) {
                state.lock().geometry = Some(geometry.clone());
            }
            for (label, on) in wanted {
                let window = app.get_webview_window(label);
                match (on, window) {
                    (true, None) => build(&app, label, &geometry, homes.get(label).copied()),
                    (false, Some(window)) => {
                        let _ = window.destroy();
                        if let Some(state) = super::toys_state(&app) {
                            let mut runtime = state.lock();
                            runtime.hits.remove(label);
                            runtime.placed.remove(label);
                            runtime.frames.remove(label);
                        }
                    }
                    _ => {}
                }
            }
        });
    }

    fn cursor() -> CGPoint {
        // SAFETY: a null source asks for the current location; the event is
        // released after reading it.
        unsafe {
            let event = CGEventCreate(std::ptr::null());
            if event.is_null() {
                return CGPoint::default();
            }
            let point = CGEventGetLocation(event);
            CFRelease(event as *const c_void);
            point
        }
    }

    fn button_down() -> bool {
        // SAFETY: plain query of the combined session state.
        unsafe { CGEventSourceButtonState(COMBINED_STATE, 0) }
    }

    fn idle_seconds() -> f64 {
        // SAFETY: plain query; no permission is needed for the HID idle time.
        unsafe { CGEventSourceSecondsSinceLastEventType(HID_STATE, ANY_INPUT) }
    }

    fn number(dict: &CFDictionary<CFString, CFType>, key: CFStringRef) -> Option<f64> {
        let key = unsafe { CFString::wrap_under_get_rule(key) };
        dict.find(key)?.downcast::<CFNumber>()?.to_f64()
    }

    fn kind_of(owner: &str) -> &'static str {
        let owner = owner.to_lowercase();
        const TERM: [&str; 11] = [
            "terminal",
            "iterm",
            "warp",
            "ghostty",
            "kitty",
            "alacritty",
            "wezterm",
            "hyper",
            "tabby",
            "rio",
            "termius",
        ];
        const CODE: [&str; 20] = [
            "code",
            "cursor",
            "zed",
            "intellij",
            "goland",
            "pycharm",
            "webstorm",
            "rustrover",
            "clion",
            "phpstorm",
            "rider",
            "android studio",
            "sublime",
            "nova",
            "windsurf",
            "trae",
            "fleet",
            "bbedit",
            "textmate",
            "emacs",
        ];
        const WEB: [&str; 14] = [
            "safari",
            "chrome",
            "chromium",
            "firefox",
            "arc",
            "edge",
            "brave",
            "opera",
            "vivaldi",
            "zen",
            "orion",
            "dia",
            "comet",
            "duckduckgo",
        ];
        if TERM.iter().any(|name| owner.contains(name)) {
            "term"
        } else if CODE.iter().any(|name| owner.contains(name)) {
            "code"
        } else if WEB.iter().any(|name| owner.contains(name)) {
            "web"
        } else {
            "other"
        }
    }

    /// Other apps' windows on every screen, front to back.
    fn other_windows() -> Vec<OtherWindow> {
        let own = std::process::id() as i64;
        let mut others = Vec::new();
        // SAFETY: the list follows the copy rule and is released by `CFArray`.
        let list = unsafe { CGWindowListCopyWindowInfo(ON_SCREEN_ONLY | EXCLUDE_DESKTOP, 0) };
        if list.is_null() {
            return others;
        }
        let list: CFArray<CFDictionary<CFString, CFType>> =
            unsafe { TCFType::wrap_under_create_rule(list) };
        for dict in list.iter() {
            if number(&dict, unsafe { kCGWindowLayer }) != Some(0.0) {
                continue;
            }
            if number(&dict, unsafe { kCGWindowOwnerPID }).map(|pid| pid as i64) == Some(own) {
                continue;
            }
            if number(&dict, unsafe { kCGWindowAlpha }).unwrap_or(1.0) < 0.1 {
                continue;
            }
            let bounds_key = unsafe { CFString::wrap_under_get_rule(kCGWindowBounds) };
            let Some(bounds) = dict.find(bounds_key) else {
                continue;
            };
            let mut rect = CGRect::default();
            // SAFETY: `bounds` is the window's bounds dictionary.
            if !unsafe { CGRectMakeWithDictionaryRepresentation(bounds.as_CFTypeRef(), &mut rect) }
            {
                continue;
            }
            let owner_key = unsafe { CFString::wrap_under_get_rule(kCGWindowOwnerName) };
            let owner = dict
                .find(owner_key)
                .and_then(|value| value.downcast::<CFString>())
                .map(|name| name.to_string())
                .unwrap_or_default();
            others.push(OtherWindow {
                id: number(&dict, unsafe { kCGWindowNumber }).unwrap_or(0.0) as i64,
                kind: kind_of(&owner),
                frame: (rect.origin.x, rect.origin.y, rect.size.x, rect.size.y),
            });
        }
        others
    }

    pub fn start_poll(app: &AppHandle) {
        let Some(state) = super::toys_state(app) else {
            return;
        };
        {
            let mut runtime = state.lock();
            if runtime.polling {
                return;
            }
            runtime.polling = true;
        }
        let app = app.clone();
        std::thread::Builder::new()
            .name("astrlink-toys".into())
            .spawn(move || poll(app))
            .ok();
    }

    fn set_frame(window: &tauri::WebviewWindow, main_height: f64, rect: Rect) {
        let frame = to_cocoa(main_height, rect);
        with_ns_window(window, move |ns_window| {
            ns_window.setFrame_display(frame, false)
        });
    }

    /// Reads the screens again, and puts the pet's window back on her own
    /// screen, or on the main one while that screen is unplugged.
    fn refresh_geometry(app: &AppHandle) {
        let handle = app.clone();
        let _ = app.run_on_main_thread(move || {
            let Some(mtm) = MainThreadMarker::new() else {
                return;
            };
            let (Some(geometry), Some(state)) = (geometry(mtm), super::toys_state(&handle)) else {
                return;
            };
            state.lock().geometry = Some(geometry.clone());
            let homes = super::homes(&handle);
            for label in [PET] {
                let Some(window) = handle.get_webview_window(label) else {
                    continue;
                };
                let want = placement(&geometry, homes.get(label).copied());
                {
                    let mut runtime = state.lock();
                    // Not out of your hand; she goes once you let go.
                    let held = runtime.hits.get(label).is_some_and(|hits| hits.hold);
                    if held || runtime.placed.get(label) == Some(&want) {
                        continue;
                    }
                    runtime.placed.insert(label.to_string(), want);
                    if let Some(hits) = runtime.hits.get_mut(label) {
                        hits.origin_at = None;
                    }
                }
                set_frame(&window, geometry.main.3, want);
            }
        });
    }

    /// A held pet carried onto another screen: her window follows, and its
    /// page hears how far its points moved. The screen is kept for next time.
    fn carry(app: &AppHandle, window: &tauri::WebviewWindow, cursor: Point) {
        let Some(state) = super::toys_state(app) else {
            return;
        };
        let label = window.label();
        let (from, to, main_height, homes) = {
            let mut runtime = state.lock();
            if !runtime.hits.get(label).is_some_and(|hits| hits.carry) {
                return;
            }
            let Some(geometry) = runtime.geometry.as_ref() else {
                return;
            };
            let main_height = geometry.main.3;
            let Some(&from) = runtime.placed.get(label) else {
                return;
            };
            let Some(to) = carried_to(&geometry.screens, from, cursor) else {
                return;
            };
            runtime.placed.insert(label.to_string(), to.frame);
            let homes = runtime.homes.get_or_insert_with(HashMap::new);
            homes.insert(label.to_string(), middle(to.frame));
            let homes = homes.clone();
            if let Some(hits) = runtime.hits.get_mut(label) {
                hits.origin = Some(to.frame);
                hits.origin_at = Some(Instant::now());
                hits.pointer = None;
            }
            (from, to.frame, main_height, homes)
        };
        let _ = app.emit_to(
            label,
            SCREEN_EVENT,
            serde_json::json!({ "dx": from.0 - to.0, "dy": from.1 - to.1, "w": to.2, "h": to.3 }),
        );
        set_frame(window, main_height, to);
        super::save_homes(app, &homes);
    }

    fn poll(app: AppHandle) {
        let mut tick: u64 = 0;
        let mut last_frames: HashMap<&str, String> = HashMap::new();
        let mut debug_after = super::debug_overrides(&app).away_after;
        loop {
            std::thread::sleep(TICK);
            tick += 1;
            let toys = super::prefs_of(&app).toys;
            if !toys.on.any() {
                break;
            }
            let down = button_down();
            // The Dock widens and narrows as apps open and quit.
            if tick % 30 == 0 {
                refresh_geometry(&app);
            }
            if tick % 150 == 0 {
                debug_after = super::debug_overrides(&app).away_after;
            }
            track_pointer(&app, down);
            let pets: Vec<&str> = [(PET, toys.on.pet)]
                .into_iter()
                .filter(|&(label, on)| on && app.get_webview_window(label).is_some())
                .map(|(label, _)| label)
                .collect();
            if !pets.is_empty() && (tick % 6 == 0 || (down && tick % 2 == 0)) {
                send_frames(&app, &pets, &mut last_frames);
            }
            if pets.contains(&PET) && tick % 30 == 0 {
                let (idle_wait, busy_wait) = away_waits(debug_after);
                let quiet = super::toys_state(&app).map_or(f64::INFINITY, |state| {
                    ai_quiet_seconds(state.lock().activity.as_ref(), Utc::now())
                });
                let away = away_after(idle_wait, busy_wait, quiet);
                let _ = app.emit_to(
                    PET,
                    IDLE_EVENT,
                    serde_json::json!({ "idle": idle_seconds(), "away": away }),
                );
            }
        }
        if let Some(state) = super::toys_state(&app) {
            state.lock().polling = false;
        }
    }

    /// Tells each pet what is on her own screen, when it changed.
    fn send_frames(app: &AppHandle, pets: &[&'static str], last: &mut HashMap<&str, String>) {
        let Some(state) = super::toys_state(app) else {
            return;
        };
        let Some(geometry) = state.lock().geometry.clone() else {
            return;
        };
        let others = other_windows();
        for &label in pets {
            let placed = state.lock().placed.get(label).copied();
            let Some(screen) = home_screen(&geometry.screens, placed.map(middle)) else {
                continue;
            };
            let frames = frames_on(&screen, geometry.dock, &others);
            let text = frames.to_string();
            if last.get(label) == Some(&text) {
                continue;
            }
            last.insert(label, text);
            state
                .lock()
                .frames
                .insert(label.to_string(), frames.clone());
            let _ = app.emit_to(label, FRAMES_EVENT, frames);
        }
    }

    /// Lets the mouse through everywhere but the parts the pet says are her
    /// own, and tells her where the mouse is.
    fn track_pointer(app: &AppHandle, down: bool) {
        let Some(state) = super::toys_state(app) else {
            return;
        };
        let point = cursor();
        for label in [PET] {
            let Some(window) = app.get_webview_window(label) else {
                continue;
            };
            carry(app, &window, (point.x, point.y));
            let fresh_for = Duration::from_millis(if down { 60 } else { 500 });
            let stale = state.lock().hits.get(label).map_or(true, |hits| {
                hits.origin.is_none() || hits.origin_at.map_or(true, |at| at.elapsed() >= fresh_for)
            });
            if stale {
                let scale = window.scale_factor().unwrap_or(1.0);
                let origin = window
                    .outer_position()
                    .ok()
                    .map(|p| p.to_logical::<f64>(scale));
                let size = window.outer_size().ok().map(|s| s.to_logical::<f64>(scale));
                if let (Some(origin), Some(size)) = (origin, size) {
                    let mut runtime = state.lock();
                    let hits = runtime.hits.entry(label.to_string()).or_default();
                    hits.origin = Some((origin.x, origin.y, size.width, size.height));
                    hits.origin_at = Some(Instant::now());
                }
            }
            let (change, emit) = {
                let mut runtime = state.lock();
                let hits = runtime.hits.entry(label.to_string()).or_default();
                let Some((ox, oy, ow, oh)) = hits.origin else {
                    continue;
                };
                let x = point.x - ox;
                let y = point.y - oy;
                let inside = x >= 0.0 && y >= 0.0 && x < ow && y < oh;
                let over = inside && hits.rects.iter().any(|rect| rect.contains(x, y));
                let click = hits.hold
                    || (inside
                        && hits
                            .rects
                            .iter()
                            .any(|rect| rect.click && rect.contains(x, y)));
                let change = (click != hits.interactive).then(|| {
                    hits.interactive = click;
                    click
                });
                let next = (x, y, over || hits.hold);
                let emit = if inside || hits.hold {
                    let moved = hits.down != down
                        || hits.pointer.map_or(true, |(px, py, pover)| {
                            (px - x).abs() >= 0.5 || (py - y).abs() >= 0.5 || pover != next.2
                        });
                    hits.pointer = Some(next);
                    hits.down = down;
                    moved.then_some(Some(next))
                } else if hits.pointer.take().is_some() {
                    Some(None)
                } else {
                    None
                };
                (change, emit)
            };
            if let Some(click) = change {
                let _ = window.set_ignore_cursor_events(!click);
            }
            if let Some(next) = emit {
                let payload = match next {
                    Some((x, y, over)) => {
                        serde_json::json!({ "x": x, "y": y, "over": over, "down": down })
                    }
                    None => serde_json::Value::Null,
                };
                let _ = app.emit_to(label, POINTER_EVENT, payload);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn at(text: &str) -> DateTime<Utc> {
        DateTime::parse_from_rfc3339(text)
            .unwrap()
            .with_timezone(&Utc)
    }

    #[test]
    fn you_are_away_sooner_once_the_ai_is_quiet() {
        let (idle, busy) = away_waits(None);
        assert_eq!((idle, busy), (600.0, 1_800.0));
        // The AI quiet for the idle wait: ten minutes away is away.
        assert_eq!(away_after(idle, busy, 600.0), 600.0);
        assert_eq!(away_after(idle, busy, f64::INFINITY), 600.0);
        // A long task still running, or just done: you may be watching it.
        assert_eq!(away_after(idle, busy, 0.0), 1_800.0);
        assert_eq!(away_after(idle, busy, 120.0), 1_800.0);
        // The debug override sets both, five seconds at least.
        assert_eq!(away_waits(Some(20.0)), (20.0, 20.0));
        assert_eq!(away_waits(Some(1.0)), (5.0, 5.0));
    }

    #[test]
    fn a_pet_lives_on_the_screen_she_was_carried_to() {
        // A laptop under the menu bar, a big screen to its right, lower down.
        let laptop = Screen {
            frame: (0.0, 0.0, 1512.0, 982.0),
            visible: (0.0, 33.0, 1512.0, 879.0),
        };
        let big = Screen {
            frame: (1512.0, -200.0, 3008.0, 1692.0),
            visible: (1512.0, -175.0, 3008.0, 1667.0),
        };
        let screens = [laptop, big];

        // Kept as a point on the big screen: she comes back there; unplugged
        // or never moved, she lives on the main screen.
        assert_eq!(home_screen(&screens, Some(middle(big.frame))), Some(big));
        assert_eq!(home_screen(&screens, Some((9000.0, 0.0))), Some(laptop));
        assert_eq!(home_screen(&screens, None), Some(laptop));
        assert_eq!(home_screen(&[], None), None);

        // The mouse takes her across: the window follows; on the same screen
        // or over the gap below the laptop it stays put.
        assert_eq!(
            carried_to(&screens, laptop.frame, (1600.0, 400.0)),
            Some(big)
        );
        assert_eq!(carried_to(&screens, laptop.frame, (700.0, 400.0)), None);
        assert_eq!(carried_to(&screens, big.frame, (2000.0, 0.0)), None);
        assert_eq!(carried_to(&screens, laptop.frame, (700.0, 1200.0)), None);
    }

    #[test]
    fn each_screen_shows_its_own_windows_and_dock() {
        let laptop = Screen {
            frame: (0.0, 0.0, 1512.0, 982.0),
            visible: (0.0, 33.0, 1512.0, 949.0),
        };
        let big = Screen {
            frame: (1512.0, -200.0, 3008.0, 1692.0),
            visible: (1512.0, -175.0, 3008.0, 1595.0),
        };
        let others = [
            OtherWindow {
                id: 1,
                kind: "term",
                frame: (100.0, 200.0, 800.0, 500.0),
            },
            OtherWindow {
                id: 2,
                kind: "code",
                frame: (2000.0, 100.0, 1200.0, 900.0),
            },
        ];
        // The Dock sits at the bottom of the big screen.
        let dock = Some((2600.0, 1420.0, 800.0, 72.0));

        let on_big = frames_on(&big, dock, &others);
        let wins = on_big["wins"].as_array().unwrap();
        assert_eq!(wins.len(), 1);
        assert_eq!(wins[0]["id"], 2);
        // In the big screen's own points.
        assert_eq!(
            (wins[0]["x"].as_f64(), wins[0]["y"].as_f64()),
            (Some(488.0), Some(300.0))
        );
        assert_eq!(on_big["dock"]["x0"], 1088.0);
        assert_eq!(on_big["dock"]["y"], 1620.0);
        assert_eq!(on_big["top"], 25.0);
        assert_eq!(on_big["floor"], 1690.0);

        let on_laptop = frames_on(&laptop, dock, &others);
        let wins = on_laptop["wins"].as_array().unwrap();
        assert_eq!(wins.len(), 1);
        assert_eq!(wins[0]["id"], 1);
        assert!(on_laptop["dock"].is_null());
        assert_eq!(on_laptop["top"], 33.0);
        assert_eq!(on_laptop["full"], false);
    }

    #[test]
    fn the_ai_is_quiet_only_after_its_last_call_ends() {
        let now = at("2026-11-11T10:10:00Z");
        let reading = |calls: serde_json::Value| serde_json::json!({ "now": "2026-11-11T10:09:59Z", "calls": calls });

        // A call still running: the AI is busy and you are watching.
        let running = reading(serde_json::json!([
            { "started_at": "2026-11-11T10:00:00Z", "completed_at": "2026-11-11T10:01:00Z" },
            { "started_at": "2026-11-11T10:09:00Z", "completed_at": null },
        ]));
        assert_eq!(ai_quiet_seconds(Some(&running), now), 0.0);

        // Quiet since the last call ended, whatever order they come in.
        let ended = reading(serde_json::json!([
            { "started_at": "2026-11-11T10:05:00Z", "completed_at": "2026-11-11T10:08:00Z" },
            { "started_at": "2026-11-11T10:00:00Z", "completed_at": "2026-11-11T10:01:00Z" },
        ]));
        assert_eq!(ai_quiet_seconds(Some(&ended), now), 120.0);

        // Nothing in the window, no reading, or a stale one: no AI to watch.
        assert!(ai_quiet_seconds(Some(&reading(serde_json::json!([]))), now).is_infinite());
        assert!(ai_quiet_seconds(None, now).is_infinite());
        let stale = serde_json::json!({
            "now": "2026-11-11T10:00:00Z",
            "calls": [{ "started_at": "2026-11-11T09:59:00Z", "completed_at": null }],
        });
        assert!(ai_quiet_seconds(Some(&stale), now).is_infinite());
    }
}
