//! The raw access approval window (plan §5.11.6) and the auto-lock of Core's
//! unlock session. Approving an agent's request to read raw content happens
//! only in this small window: it opens on its own when a request arrives,
//! without taking focus, so no keystroke meant for another window can approve
//! anything. It floats above other windows only while a request waits for the
//! operator. Closing it decides nothing and ends no grant; a pending request
//! waits until it expires, and the tray opens the window again.

use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};
use std::time::Duration;

use tauri::{AppHandle, Manager, WebviewUrl, WebviewWindowBuilder};

use crate::{
    i18n,
    preferences::PreferencesStore,
    sidecar::{CoreManager, CorePhase},
};

/// Must match `RAW_ACCESS_APPROVAL_LABEL` in the frontend.
pub const LABEL: &str = "raw-access-approval";
const WIDTH: f64 = 440.0;
const HEIGHT: f64 = 620.0;
const MIN_WIDTH: f64 = 380.0;
const MIN_HEIGHT: f64 = 460.0;
/// macOS reports a minimize only as lost focus, and the window counts as
/// minimized once the animation ends; look for that long.
const MINIMIZE_CHECK_INTERVAL: Duration = Duration::from_millis(250);
const MINIMIZE_CHECK_ATTEMPTS: u32 = 8;

/// Opens the approval window. A window opened for a new request does not take
/// focus; the tray asks for focus because the operator clicked it.
pub fn open(app: &AppHandle, focus: bool) -> Result<(), String> {
    if let Some(window) = app.get_webview_window(LABEL) {
        // An open window already lists the new request; showing it again
        // would make it the key window.
        if focus {
            let _ = window.unminimize();
            let _ = window.show();
            let _ = window.set_focus();
        }
        return Ok(());
    }
    let preferences = app
        .try_state::<Arc<PreferencesStore>>()
        .map(|store| store.snapshot().values)
        .unwrap_or_default();
    let theme = preferences
        .theme
        .native_theme()
        .or_else(|| {
            app.get_webview_window("main")
                .and_then(|window| window.theme().ok())
        })
        .unwrap_or(tauri::Theme::Light);
    let mut builder = WebviewWindowBuilder::new(app, LABEL, WebviewUrl::default())
        .title(i18n::t(
            preferences.locale,
            "host.window.rawAccessApproval",
            &[],
        ))
        .background_color(crate::theme_background(theme))
        .inner_size(WIDTH, HEIGHT)
        .min_inner_size(MIN_WIDTH, MIN_HEIGHT)
        .resizable(true)
        .shadow(true)
        .focused(focus)
        // The main window may be hidden to the tray; a request must still be
        // seen above whatever the operator is working in.
        .always_on_top(awaiting(app))
        .center();

    // The frontend draws its own title bar, as in the inspector window.
    #[cfg(target_os = "macos")]
    {
        builder = builder
            .title_bar_style(tauri::TitleBarStyle::Overlay)
            .hidden_title(true);
    }
    #[cfg(not(target_os = "macos"))]
    {
        builder = builder.decorations(false);
    }

    builder.build().map_err(|error| error.to_string())?;
    Ok(())
}

fn awaiting(app: &AppHandle) -> bool {
    app.try_state::<Arc<CoreManager>>()
        .is_some_and(|manager| manager.view().pending_raw_access > 0)
}

/// Floats the window while a request waits for the operator, including over
/// a window left open from the tray. Once none is left, other windows cover
/// it again.
pub fn set_floating(app: &AppHandle, floating: bool) {
    let Some(window) = app.get_webview_window(LABEL) else {
        return;
    };
    if let Err(error) = window.set_always_on_top(floating) {
        eprintln!("unable to change the raw access approval window level: {error}");
    }
}

/// A native notification, since the main window may be hidden.
pub fn notify_pending(app: &AppHandle) {
    let locale = app
        .try_state::<Arc<PreferencesStore>>()
        .map(|store| store.snapshot().values.locale)
        .unwrap_or_default();
    let title = i18n::t(locale, "host.rawAccess.notificationTitle", &[]);
    let body = i18n::t(locale, "host.rawAccess.notificationBody", &[]);
    #[cfg(target_os = "macos")]
    crate::macos_app::notify(title, body);
    #[cfg(not(target_os = "macos"))]
    {
        use tauri_plugin_notification::NotificationExt;
        if let Err(error) = app.notification().builder().title(title).body(body).show() {
            eprintln!("unable to send the raw access notification: {error}");
        }
    }
}

/// Set once the main window's minimize has locked, so one minimize locks
/// once; the window taking focus again clears it.
static MINIMIZE_LOCKED: AtomicBool = AtomicBool::new(false);

/// Closes Core's unlock session once the main window leaves the screen, so
/// raw content and one-click approvals need the password again. Agent grants
/// keep running; only the manual lock revokes them.
pub fn lock_on_hide(app: &AppHandle) {
    let Some(manager) = app.try_state::<Arc<CoreManager>>() else {
        return;
    };
    if manager.view().phase != CorePhase::Ready {
        return;
    }
    let manager = Arc::clone(manager.inner());
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        let result = manager.lock_raw(true).await;
        if let Err(error) = &result {
            eprintln!("unable to lock raw content after hiding AstrLink: {error}");
        }
        let _ = crate::broadcast_raw_sealing_change(&app, result);
    });
}

fn lock_once_minimized(app: &AppHandle) {
    if !MINIMIZE_LOCKED.swap(true, Ordering::SeqCst) {
        lock_on_hide(app);
    }
}

/// Windows and Linux report a minimize as a resize.
pub fn on_main_resized(app: &AppHandle) {
    let minimized = app
        .get_webview_window("main")
        .is_some_and(|window| window.is_minimized().unwrap_or(false));
    if minimized {
        lock_once_minimized(app);
    }
}

pub fn on_main_focus(app: &AppHandle, focused: bool) {
    if focused {
        MINIMIZE_LOCKED.store(false, Ordering::SeqCst);
        return;
    }
    // macOS has no minimize event: check for one after the window loses
    // focus, until it is back or found minimized.
    if !cfg!(target_os = "macos") {
        return;
    }
    let app = app.clone();
    std::thread::spawn(move || {
        for _ in 0..MINIMIZE_CHECK_ATTEMPTS {
            std::thread::sleep(MINIMIZE_CHECK_INTERVAL);
            let Some(window) = app.get_webview_window("main") else {
                return;
            };
            if window.is_focused().unwrap_or(false) {
                return;
            }
            if window.is_minimized().unwrap_or(false) {
                lock_once_minimized(&app);
                return;
            }
        }
    });
}
