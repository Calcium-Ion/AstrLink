mod agent_install;
mod cc_switch;
mod client_config;
mod client_updates;
mod control_session;
mod data_hygiene;
#[cfg(debug_assertions)]
mod dev_reload;
mod failure_policy;
mod host_files;
mod i18n;
mod kek_store;
#[cfg(target_os = "macos")]
mod macos_app;
mod preferences;
mod provider_import;
mod proxy_check;
mod raw_access;
mod raw_approval;
mod raw_key_pin;
mod recovery_path;
mod service_proxy;
mod sidecar;
mod startup_window;
mod tray;
mod updates;

use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex,
};

use i18n::Locale;
use preferences::{
    CloseBehavior, Preferences, PreferencesSnapshot, PreferencesStore, ThemePreference,
    TrayPreferences,
};
use serde::{Deserialize, Serialize};
use sidecar::{CoreManager, CoreSnapshot, PolicyRecordResponse, ServiceRecordResponse};
use tauri::{Emitter, Manager, RunEvent, State, WebviewUrl, WebviewWindowBuilder, WindowEvent};
use tauri_plugin_autostart::ManagerExt;
use tauri_plugin_clipboard_manager::ClipboardExt;
use tauri_plugin_deep_link::DeepLinkExt;
use tauri_plugin_dialog::DialogExt;
#[cfg(not(target_os = "macos"))]
use tauri_plugin_notification::NotificationExt;

#[derive(Debug, Serialize)]
struct AppSnapshot {
    app_version: String,
    #[serde(flatten)]
    core: CoreSnapshot,
}

#[derive(Debug, Serialize)]
struct WindowChromePreferences {
    platform: &'static str,
    decoration_layout: Option<String>,
}

#[derive(Debug, Serialize)]
struct SettingsSnapshot {
    #[serde(flatten)]
    preferences: PreferencesSnapshot,
    autostart_actual: Option<bool>,
    autostart_error: Option<String>,
    data_backups: Vec<data_hygiene::DataBackupFile>,
    /// Where Core's local key lives; `None` before the first Core start.
    local_key_storage: Option<kek_store::LocalKeyStorage>,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct PreferencesInput {
    close_behavior: CloseBehavior,
    autostart: bool,
    core_auto_start: bool,
    core_auto_recover: bool,
    use_system_proxy: bool,
    inference_port: u16,
    max_concurrent_inspections: u16,
    response_start_timeout_seconds: u32,
    max_request_body_mib: u32,
    locale: Locale,
    theme: ThemePreference,
    quota_display_mode: preferences::QuotaDisplayMode,
    #[serde(default)]
    tray: TrayPreferences,
    #[serde(default)]
    updates: updates::UpdatePreferences,
}

impl From<PreferencesInput> for Preferences {
    fn from(input: PreferencesInput) -> Self {
        Self {
            close_behavior: input.close_behavior,
            autostart: input.autostart,
            core_auto_start: input.core_auto_start,
            core_auto_recover: input.core_auto_recover,
            use_system_proxy: input.use_system_proxy,
            inference_port: input.inference_port,
            max_concurrent_inspections: input.max_concurrent_inspections,
            response_start_timeout_seconds: input.response_start_timeout_seconds,
            max_request_body_mib: input.max_request_body_mib,
            locale: input.locale,
            theme: input.theme,
            quota_display_mode: input.quota_display_mode,
            tray: input.tray,
            updates: input.updates,
        }
    }
}

impl AppSnapshot {
    fn capture(app: &tauri::AppHandle, manager: &CoreManager) -> Self {
        Self {
            app_version: app.package_info().version.to_string(),
            core: manager.snapshot(),
        }
    }
}

#[tauri::command]
fn core_status(app: tauri::AppHandle, manager: State<'_, Arc<CoreManager>>) -> AppSnapshot {
    AppSnapshot::capture(&app, &manager)
}

#[cfg(target_os = "linux")]
fn linux_decoration_layout() -> Option<String> {
    use gtk::prelude::*;

    let settings = gtk::Settings::default()?;
    settings
        .property_value("gtk-decoration-layout")
        .get::<String>()
        .ok()
}

#[cfg(not(target_os = "linux"))]
fn linux_decoration_layout() -> Option<String> {
    None
}

#[tauri::command]
fn window_chrome_preferences() -> WindowChromePreferences {
    WindowChromePreferences {
        platform: std::env::consts::OS,
        decoration_layout: linux_decoration_layout(),
    }
}

#[tauri::command]
async fn restart_core(
    app: tauri::AppHandle,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<AppSnapshot, String> {
    let _lifecycle = updates::lifecycle_guard(&app)?;
    let manager = Arc::clone(manager.inner());
    manager.restart(&app).await?;
    Ok(AppSnapshot::capture(&app, &manager))
}

// Async so the start, which may wait on a keychain prompt, runs on a
// blocking worker rather than the main thread.
#[tauri::command]
async fn start_core(
    app: tauri::AppHandle,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<AppSnapshot, String> {
    let _lifecycle = updates::lifecycle_guard(&app)?;
    let manager = Arc::clone(manager.inner());
    let (starter, handle) = (Arc::clone(&manager), app.clone());
    tauri::async_runtime::spawn_blocking(move || starter.start(&handle))
        .await
        .map_err(|error| format!("unable to start astrlink-core: {error}"))??;
    Ok(AppSnapshot::capture(&app, &manager))
}

#[tauri::command]
async fn stop_core(
    app: tauri::AppHandle,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<AppSnapshot, String> {
    let _lifecycle = updates::lifecycle_guard(&app)?;
    let manager = Arc::clone(manager.inner());
    manager.stop_and_wait().await?;
    Ok(AppSnapshot::capture(&app, &manager))
}

fn settings_snapshot(app: &tauri::AppHandle, store: &PreferencesStore) -> SettingsSnapshot {
    // Rescanned on every snapshot so a backup the user deletes stops showing.
    let data_backups = app
        .path()
        .app_data_dir()
        .map(|directory| data_hygiene::scan_backup_files(&directory))
        .unwrap_or_default();
    let local_key_storage = app
        .try_state::<Arc<CoreManager>>()
        .and_then(|manager| manager.local_key_storage());
    match app.autolaunch().is_enabled() {
        Ok(actual) => SettingsSnapshot {
            preferences: store.snapshot(),
            autostart_actual: Some(actual),
            autostart_error: None,
            data_backups,
            local_key_storage,
        },
        Err(error) => SettingsSnapshot {
            preferences: store.snapshot(),
            autostart_actual: None,
            autostart_error: Some(i18n::t(
                store.snapshot().values.locale,
                "host.autostart.readFailed",
                &[("error", &error.to_string())],
            )),
            data_backups,
            local_key_storage,
        },
    }
}

#[tauri::command]
fn get_preferences(
    app: tauri::AppHandle,
    store: State<'_, Arc<PreferencesStore>>,
) -> SettingsSnapshot {
    settings_snapshot(&app, store.inner())
}

fn agent_install_context(app: &tauri::AppHandle) -> Result<agent_install::InstallContext, String> {
    Ok(agent_install::InstallContext {
        home: control_session::user_home()?,
        // Install reports a missing sidecar itself; status and uninstall do not need it.
        cli_source: agent_install::resolve_sidecar_binary("astrlink-cli").unwrap_or_default(),
        data_directory: app.path().app_data_dir().ok(),
        raw_key_pins: raw_key_pin_file(app),
    })
}

/// Keeps client configs AstrLink wrote pointed at the gateway's current
/// address, including a fallback port picked at startup.
fn start_client_config_sync(manager: &CoreManager, preferences: Arc<PreferencesStore>) {
    let mut changes = manager.subscribe();
    tauri::async_runtime::spawn(async move {
        let mut synced: Option<String> = None;
        while changes.changed().await.is_ok() {
            let inference_url = changes.borrow_and_update().inference_url.clone();
            let Some(inference_url) = inference_url.filter(|url| synced.as_ref() != Some(url))
            else {
                continue;
            };
            synced = Some(inference_url.clone());
            let result = tauri::async_runtime::spawn_blocking(move || {
                client_config::sync(&control_session::user_home()?, &inference_url)
            })
            .await
            .map_err(|error| error.to_string())
            .and_then(|result| result);
            if let Err(error) = result {
                eprintln!("failed to update client configs for the gateway address: {error}");
                preferences.report_warning(i18n::t(
                    preferences.snapshot().values.locale,
                    "host.clientConfig.syncFailed",
                    &[("error", &error)],
                ));
            }
        }
    });
}

/// The raw key pin file agent guards deny, where the pins live in one.
fn raw_key_pin_file(app: &tauri::AppHandle) -> Option<std::path::PathBuf> {
    app.try_state::<Arc<raw_key_pin::RawKeyPins>>()?
        .file()
        .map(std::path::Path::to_path_buf)
}

#[tauri::command]
fn agent_debug_status(app: tauri::AppHandle) -> Result<agent_install::AgentInstallStatus, String> {
    Ok(agent_install::status(&agent_install_context(&app)?))
}

#[tauri::command]
fn install_agent_debug(
    app: tauri::AppHandle,
    skill_ids: Vec<agent_install::AgentSkillId>,
    tool_ids: Vec<agent_install::AgentToolId>,
) -> Result<agent_install::InstallReceipt, String> {
    agent_install::install(&agent_install_context(&app)?, &skill_ids, &tool_ids)
}

#[tauri::command]
fn uninstall_agent_debug(app: tauri::AppHandle) -> Result<(), String> {
    agent_install::uninstall(&agent_install_context(&app)?)
}

#[tauri::command]
fn update_preferences(
    app: tauri::AppHandle,
    store: State<'_, Arc<PreferencesStore>>,
    manager: State<'_, Arc<CoreManager>>,
    input: PreferencesInput,
) -> Result<SettingsSnapshot, String> {
    let values = Preferences::from(input);
    values.validate()?;
    let locale = values.locale;
    let previous_tray = store.snapshot().values.tray;
    let autostart = app.autolaunch();
    let actual = autostart.is_enabled().map_err(|error| {
        i18n::t(
            locale,
            "host.autostart.readFailedUnsaved",
            &[("error", &error.to_string())],
        )
    })?;
    if values.autostart != actual {
        if values.autostart {
            autostart.enable().map_err(|error| {
                i18n::t(
                    locale,
                    "host.autostart.enableFailed",
                    &[("error", &error.to_string())],
                )
            })?;
        } else {
            autostart.disable().map_err(|error| {
                i18n::t(
                    locale,
                    "host.autostart.disableFailed",
                    &[("error", &error.to_string())],
                )
            })?;
        }
        let reconciled = autostart.is_enabled().map_err(|error| {
            i18n::t(
                locale,
                "host.autostart.verifyFailed",
                &[("error", &error.to_string())],
            )
        })?;
        if reconciled != values.autostart {
            return Err(i18n::t(locale, "host.autostart.mismatch", &[]));
        }
    }
    if let Err(persist_error) = store.replace(values.clone()) {
        if values.autostart != actual {
            let rollback = if actual {
                autostart.enable()
            } else {
                autostart.disable()
            };
            return match rollback {
                Ok(()) => Err(i18n::t(
                    locale,
                    "host.autostart.rollbackOk",
                    &[("persist_error", &persist_error)],
                )),
                Err(rollback_error) => Err(i18n::t(
                    locale,
                    "host.autostart.rollbackFailed",
                    &[
                        ("persist_error", &persist_error),
                        ("rollback_error", &rollback_error.to_string()),
                    ],
                )),
            };
        }
        return Err(persist_error);
    }
    manager.configure(&values);
    // Locale and pages re-render from stored state; a new usage line or
    // menubar figure needs numbers the last digest may not have collected.
    if values.tray.usage != previous_tray.usage
        || values.tray.menubar_text != previous_tray.menubar_text
    {
        // Operator-initiated: fresh local numbers, and plan windows unless
        // they were fetched a moment ago.
        tray::request_usage_refresh(
            &app,
            true,
            tray::PlanRefresh::IfOlderThan(tray::PLAN_REFRESH_VISIBLE),
        );
    }
    tray::refresh(&app);
    if let Err(error) = app.emit("quota-display-mode-changed", values.quota_display_mode) {
        eprintln!("unable to broadcast quota display mode: {error}");
    }
    apply_native_theme(&app, values.theme);
    if let Err(error) = app.emit("theme-preference-changed", values.theme) {
        eprintln!("unable to broadcast AstrLink theme: {error}");
    }
    Ok(settings_snapshot(&app, store.inner()))
}

fn theme_background(theme: tauri::Theme) -> tauri::window::Color {
    match theme {
        tauri::Theme::Dark => tauri::window::Color(17, 24, 39, 255),
        _ => tauri::window::Color(255, 255, 255, 255),
    }
}

fn apply_native_theme(app: &tauri::AppHandle, preference: ThemePreference) {
    app.set_theme(preference.native_theme());
    for window in app.webview_windows().values() {
        // The tray popover paints its own panel on a transparent window.
        if window.label() == tray::POPOVER_LABEL {
            continue;
        }
        let theme = preference.native_theme().or_else(|| window.theme().ok());
        if let Some(theme) = theme {
            if let Err(error) = window.set_background_color(Some(theme_background(theme))) {
                eprintln!("unable to update AstrLink window background: {error}");
            }
        }
    }
}

/// What the tray popover renders. Settings pass a draft of the tray
/// preferences to preview the panel exactly as the tray would show it.
#[tauri::command]
fn tray_state(
    app: tauri::AppHandle,
    tray: Option<TrayPreferences>,
) -> Result<tray::TrayStateSnapshot, String> {
    if let Some(tray) = &tray {
        tray.validate(
            app.state::<Arc<PreferencesStore>>()
                .snapshot()
                .values
                .locale,
        )?;
    }
    Ok(tray::state_snapshot(&app, tray))
}

#[tauri::command]
fn tray_action(app: tauri::AppHandle, action: tray::TrayAction) -> Result<(), String> {
    tray::perform(&app, action)
}

#[tauri::command]
fn tray_popover_resize(app: tauri::AppHandle, height: f64) -> Result<(), String> {
    tray::resize_popover(&app, height)
}

#[tauri::command]
fn tray_popover_hide(app: tauri::AppHandle) {
    tray::dismiss_popover(&app);
}

fn receive_deep_links(app: &tauri::AppHandle, urls: Vec<tauri::Url>) {
    // One confirmation at a time: only the last AstrLink link is kept.
    if let Some(url) = urls
        .iter()
        .rev()
        .find(|url| url.scheme() == provider_import::SCHEME)
    {
        provider_import::receive(app, url.as_str());
    }
}

pub(crate) fn show_main_window(app: &tauri::AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        // Restore the foreground app before showing/focusing its window.
        #[cfg(target_os = "macos")]
        if let Err(error) = app.set_activation_policy(tauri::ActivationPolicy::Regular) {
            eprintln!("unable to restore AstrLink in the Dock: {error}");
        }
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
    }
}

fn hide_main_window_to_tray(app: &tauri::AppHandle) {
    let Some(window) = app.get_webview_window("main") else {
        return;
    };
    if let Err(error) = window.hide() {
        eprintln!("unable to hide AstrLink to the tray: {error}");
        return;
    }
    // Hiding a window does not remove a regular macOS app from the Dock.
    // Accessory mode keeps the tray and pinned inspector windows available.
    // Use the activation policy directly: Tao's set_dock_visibility(false)
    // can ignore a close that follows a reopen by less than one second.
    #[cfg(target_os = "macos")]
    if let Err(error) = app.set_activation_policy(tauri::ActivationPolicy::Accessory) {
        eprintln!("unable to remove AstrLink from the Dock: {error}");
    }
    // A tray-parked app must not leave a following inspector on screen showing
    // a request the operator can no longer reach. Pinned ones stay.
    close_unpinned_inspectors(app);
    raw_approval::lock_on_hide(app);
    notify_hidden_to_tray(app);
}

fn notify_hidden_to_tray(app: &tauri::AppHandle) {
    let locale = app
        .state::<Arc<PreferencesStore>>()
        .snapshot()
        .values
        .locale;
    let (title_key, body_key) = if cfg!(target_os = "macos") {
        (
            "host.tray.hiddenMenuBarTitle",
            "host.tray.hiddenMenuBarBody",
        )
    } else {
        ("host.tray.hiddenTitle", "host.tray.hiddenBody")
    };
    // Use a native notification: an in-window toast is invisible after hiding.
    notify_native(
        app,
        i18n::t(locale, title_key, &[]),
        i18n::t(locale, body_key, &[]),
    );
}

/// Delivery failures are logged only; a notification must never block the
/// caller, such as the app staying in the tray.
pub(crate) fn notify_native(app: &tauri::AppHandle, title: String, body: String) {
    #[cfg(target_os = "macos")]
    {
        let _ = app;
        macos_app::notify(title, body);
    }
    #[cfg(not(target_os = "macos"))]
    if let Err(error) = app.notification().builder().title(title).body(body).show() {
        eprintln!("unable to send AstrLink notification: {error}");
    }
}

/// The trajectory inspector lives in its own window so the phase list keeps the
/// full width of the main window. Docked side by side, neither pane was wide
/// enough to read a service id or a captured body.
///
/// Labels are suffixed because a pinned inspector freezes on its phase and the
/// next click has to land in a window of its own.
const TRAJECTORY_INSPECTOR_LABEL_PREFIX: &str = "trajectory-inspector-";
const TRAJECTORY_INSPECTOR_SELECT_EVENT: &str = "trajectory-inspector:select";

const TRAJECTORY_INSPECTOR_WIDTH: f64 = 460.0;
const TRAJECTORY_INSPECTOR_HEIGHT: f64 = 680.0;
const TRAJECTORY_INSPECTOR_MIN_WIDTH: f64 = 320.0;
const TRAJECTORY_INSPECTOR_MIN_HEIGHT: f64 = 400.0;
/// Breathing room between the main window and the inspector parked beside it.
const TRAJECTORY_INSPECTOR_GAP: f64 = 12.0;
/// Diagonal offset per extra inspector, so a second window is grabbable rather
/// than exactly beneath the first.
const TRAJECTORY_INSPECTOR_CASCADE: f64 = 28.0;
/// Width a widened inspector aims for: a captured JSON body and its headers
/// read without wrapping, and the window still fits a 13-inch display.
const TRAJECTORY_INSPECTOR_WIDE_WIDTH: f64 = 1120.0;
/// Room a widened inspector leaves at the edges of the work area.
const TRAJECTORY_INSPECTOR_SCREEN_MARGIN: f64 = 16.0;
/// Long enough to read as the window stretching, short enough not to wait on.
#[cfg(target_os = "macos")]
const TRAJECTORY_INSPECTOR_RESIZE_SECONDS: f64 = 0.32;

/// One inspector window. `selection` is the phase it currently shows, kept here
/// rather than only in its React state so a window repopulates itself after the
/// dev host reloads every webview.
struct InspectorEntry {
    label: String,
    pinned: bool,
    selection: Option<serde_json::Value>,
    /// Set while the window is widened: how to put it back.
    narrow: Option<NarrowFrame>,
}

/// How a widened inspector goes back: to its earlier width, against the edge
/// it grew away from, so it returns to where it was rather than to a default.
#[derive(Clone, Copy, Debug, PartialEq)]
struct NarrowFrame {
    width: f64,
    anchor_right: bool,
}

/// Every live inspector window, in creation order.
#[derive(Default)]
struct InspectorRegistry {
    entries: Vec<InspectorEntry>,
    /// Never reused, so a label cannot collide with a window still tearing down.
    next_id: u64,
}

impl InspectorRegistry {
    /// The window a new selection belongs in: the newest one that is not
    /// pinned. Unpinning keeps a window where it was created, so an older
    /// window that was just unpinned does not take over from a newer one.
    fn target(&self) -> Option<&str> {
        self.entries
            .iter()
            .rev()
            .find(|entry| !entry.pinned)
            .map(|entry| entry.label.as_str())
    }

    fn insert(&mut self) -> String {
        self.next_id += 1;
        let label = format!("{TRAJECTORY_INSPECTOR_LABEL_PREFIX}{}", self.next_id);
        self.entries.push(InspectorEntry {
            label: label.clone(),
            pinned: false,
            selection: None,
            narrow: None,
        });
        label
    }

    fn remove(&mut self, label: &str) {
        self.entries.retain(|entry| entry.label != label);
    }

    fn find_mut(&mut self, label: &str) -> Option<&mut InspectorEntry> {
        self.entries.iter_mut().find(|entry| entry.label == label)
    }

    fn store(&mut self, label: &str, selection: serde_json::Value) {
        if let Some(entry) = self.find_mut(label) {
            entry.selection = Some(selection);
        }
    }

    fn set_pinned(&mut self, label: &str, pinned: bool) {
        if let Some(entry) = self.find_mut(label) {
            entry.pinned = pinned;
        }
    }

    fn state(&self, label: &str) -> InspectorWindowState {
        match self.entries.iter().find(|entry| entry.label == label) {
            Some(entry) => InspectorWindowState {
                selection: entry.selection.clone(),
                pinned: entry.pinned,
                wide: entry.narrow.is_some(),
            },
            None => InspectorWindowState::default(),
        }
    }

    fn unpinned_labels(&self) -> Vec<String> {
        self.entries
            .iter()
            .filter(|entry| !entry.pinned)
            .map(|entry| entry.label.clone())
            .collect()
    }

    fn remove_unpinned(&mut self) -> Vec<String> {
        let labels = self.unpinned_labels();
        self.entries.retain(|entry| entry.pinned);
        labels
    }
}

/// What an inspector window pulls on mount, instead of waiting for the main
/// window to notice it exists.
#[derive(Debug, Default, Serialize)]
struct InspectorWindowState {
    selection: Option<serde_json::Value>,
    pinned: bool,
    wide: bool,
}

#[derive(Clone, Copy, Debug, PartialEq)]
struct WindowBox {
    x: f64,
    y: f64,
    width: f64,
    height: f64,
}

/// Parks the inspector beside the main window, cascading each extra one down
/// and to the right, and folding it back over the main window's right edge when
/// the monitor has no room to its side. A window placed past the monitor edge
/// cannot be dragged back on Windows and X11, so staying on screen matters more
/// than keeping the windows disjoint.
fn inspector_placement(
    main: WindowBox,
    monitor: WindowBox,
    size: (f64, f64),
    cascade: u32,
) -> (f64, f64) {
    let (width, height) = size;
    let offset = f64::from(cascade) * TRAJECTORY_INSPECTOR_CASCADE;
    let x = (main.x + main.width + TRAJECTORY_INSPECTOR_GAP + offset)
        .min(monitor.x + monitor.width - width)
        .max(monitor.x);
    let y = (main.y + offset)
        .min(monitor.y + monitor.height - height)
        .max(monitor.y);
    (x, y)
}

/// A window's outer frame in logical pixels.
fn logical_frame(window: &tauri::WebviewWindow) -> Option<WindowBox> {
    let scale = window.scale_factor().ok()?;
    let position = window.outer_position().ok()?.to_logical::<f64>(scale);
    let size = window.outer_size().ok()?.to_logical::<f64>(scale);
    Some(WindowBox {
        x: position.x,
        y: position.y,
        width: size.width,
        height: size.height,
    })
}

/// The part of the window's monitor that the menu bar and Dock leave free.
fn logical_work_area(window: &tauri::WebviewWindow) -> Option<WindowBox> {
    let monitor = window.current_monitor().ok()??;
    let scale = monitor.scale_factor();
    let area = monitor.work_area();
    let position = area.position.to_logical::<f64>(scale);
    let size = area.size.to_logical::<f64>(scale);
    Some(WindowBox {
        x: position.x,
        y: position.y,
        width: size.width,
        height: size.height,
    })
}

fn inspector_anchor(main: &tauri::WebviewWindow, cascade: u32) -> Option<(f64, f64)> {
    let frame = logical_frame(main)?;
    let monitor = main.current_monitor().ok()??;
    let monitor_scale = monitor.scale_factor();
    let monitor_position = monitor.position().to_logical::<f64>(monitor_scale);
    let monitor_size = monitor.size().to_logical::<f64>(monitor_scale);
    Some(inspector_placement(
        frame,
        WindowBox {
            x: monitor_position.x,
            y: monitor_position.y,
            width: monitor_size.width,
            height: monitor_size.height,
        },
        (TRAJECTORY_INSPECTOR_WIDTH, TRAJECTORY_INSPECTOR_HEIGHT),
        cascade,
    ))
}

/// The frame a widened inspector takes: as wide as the work area allows, up to
/// the target, growing away from the screen edge it sits nearer. An inspector
/// parked to the right of the main window grows leftward instead of off the
/// display, and comes back to the same spot when narrowed.
fn inspector_wide_frame(current: WindowBox, work: WindowBox) -> (WindowBox, NarrowFrame) {
    let room = work.width - 2.0 * TRAJECTORY_INSPECTOR_SCREEN_MARGIN;
    let width = TRAJECTORY_INSPECTOR_WIDE_WIDTH.min(room).max(current.width);
    let anchor_right = current.x + current.width / 2.0 > work.x + work.width / 2.0;
    let frame = WindowBox {
        x: anchored_x(current, width, anchor_right, work),
        width,
        ..current
    };
    (
        frame,
        NarrowFrame {
            width: current.width,
            anchor_right,
        },
    )
}

/// The frame a widened inspector goes back to. It never grows: a window the
/// operator shrank by hand while wide keeps that width.
fn inspector_narrow_frame(current: WindowBox, work: WindowBox, narrow: NarrowFrame) -> WindowBox {
    let width = narrow.width.min(current.width);
    WindowBox {
        x: anchored_x(current, width, narrow.anchor_right, work),
        width,
        ..current
    }
}

/// Where a window of `width` starts when it keeps one edge of `current`,
/// pulled back inside the work area where it would cross it.
fn anchored_x(current: WindowBox, width: f64, anchor_right: bool, work: WindowBox) -> f64 {
    let x = if anchor_right {
        current.x + current.width - width
    } else {
        current.x
    };
    x.min(work.x + work.width - width).max(work.x)
}

fn inspector_registry<'a>(
    registry: &'a State<'_, Mutex<InspectorRegistry>>,
) -> Result<std::sync::MutexGuard<'a, InspectorRegistry>, String> {
    registry
        .lock()
        .map_err(|_| "the inspector window registry is poisoned".to_string())
}

/// Routes a selection to the newest unpinned window, opening one when every
/// inspector is pinned or none is left. Returns the label it landed in.
#[tauri::command]
async fn show_trajectory_inspector(
    app: tauri::AppHandle,
    registry: State<'_, Mutex<InspectorRegistry>>,
    selection: serde_json::Value,
) -> Result<String, String> {
    // One lock for the whole decision, so a window created alongside this call
    // cannot end up holding a phase that was routed elsewhere.
    let (label, cascade) = {
        let mut guard = inspector_registry(&registry)?;
        let existing = guard.target().map(str::to_string);
        match existing {
            Some(label) => {
                guard.store(&label, selection.clone());
                (label, None)
            }
            None => {
                let label = guard.insert();
                guard.store(&label, selection.clone());
                let cascade = u32::try_from(guard.entries.len() - 1).unwrap_or(0);
                (label, Some(cascade))
            }
        }
    };

    if let Some(cascade) = cascade {
        // WebView2 deadlocks when a synchronous IPC handler creates a webview.
        // Keep native window creation off both the event loop and async workers.
        let build_app = app.clone();
        let build_label = label.clone();
        let result = tauri::async_runtime::spawn_blocking(move || {
            build_inspector_window(&build_app, &build_label, cascade)
        })
        .await
        .map_err(|error| format!("unable to create the inspector window: {error}"))
        .and_then(|result| result);
        if let Err(error) = result {
            let mut guard = inspector_registry(&registry)?;
            guard.remove(&label);
            return Err(error);
        }

        // Leaving the conversation can cancel a window before it exists.
        // Release the registry lock before asking the event loop to close it.
        let still_open = inspector_registry(&registry)?.find_mut(&label).is_some();
        if !still_open {
            if let Some(window) = app.get_webview_window(&label) {
                window.close().map_err(|error| error.to_string())?;
            }
        }
        // The webview is not listening yet; it pulls the stored selection from
        // `trajectory_inspector_state` once it mounts.
        return Ok(label);
    }

    if let Some(window) = app.get_webview_window(&label) {
        // Reveal a hidden or minimized inspector, but leave the focus where it
        // is: the operator is clicking rows in the main window.
        let _ = window.show();
        let _ = window.unminimize();
    }
    app.emit_to(&label, TRAJECTORY_INSPECTOR_SELECT_EVENT, selection)
        .map_err(|error| error.to_string())?;
    Ok(label)
}

/// Refreshes an inspector that is already open and never creates one. A poll
/// that replaces the record must not resurrect a window the operator closed.
#[tauri::command]
fn update_trajectory_inspector(
    app: tauri::AppHandle,
    registry: State<'_, Mutex<InspectorRegistry>>,
    selection: serde_json::Value,
) -> Result<(), String> {
    let label = {
        let mut guard = inspector_registry(&registry)?;
        let Some(label) = guard.target().map(str::to_string) else {
            return Ok(());
        };
        guard.store(&label, selection.clone());
        label
    };
    app.emit_to(&label, TRAJECTORY_INSPECTOR_SELECT_EVENT, selection)
        .map_err(|error| error.to_string())
}

#[tauri::command]
fn trajectory_inspector_state(
    window: tauri::Window,
    registry: State<'_, Mutex<InspectorRegistry>>,
) -> Result<InspectorWindowState, String> {
    let guard = inspector_registry(&registry)?;
    Ok(guard.state(window.label()))
}

/// Pinning freezes the window on its phase (the router stops picking it) and
/// floats it above other windows so it can be read while the list moves on.
#[tauri::command]
fn set_trajectory_inspector_pinned(
    window: tauri::WebviewWindow,
    registry: State<'_, Mutex<InspectorRegistry>>,
    pinned: bool,
) -> Result<bool, String> {
    // The window level moves first, and the registry records only what took
    // effect. Committing first would leave the router skipping a window that
    // never floated and whose button has already snapped back.
    window
        .set_always_on_top(pinned)
        .map_err(|error| error.to_string())?;
    {
        let mut guard = inspector_registry(&registry)?;
        guard.set_pinned(window.label(), pinned);
    }
    let locale = window
        .try_state::<Arc<PreferencesStore>>()
        .map(|store| store.snapshot().values.locale)
        .unwrap_or_default();
    let _ = window.set_title(&inspector_window_title(locale, pinned));
    Ok(pinned)
}

/// Widens the inspector for long bodies, or puts it back. Only the width and
/// the horizontal position change, so the window keeps its height and stays
/// where the operator left it vertically. Returns the state that took effect.
#[tauri::command]
fn set_trajectory_inspector_wide(
    window: tauri::WebviewWindow,
    registry: State<'_, Mutex<InspectorRegistry>>,
    wide: bool,
) -> Result<bool, String> {
    if window.is_fullscreen().unwrap_or(false) {
        return Err("a full-screen inspector window cannot change its width".to_string());
    }
    let narrow = inspector_registry(&registry)?
        .find_mut(window.label())
        .and_then(|entry| entry.narrow);
    let current = logical_frame(&window).ok_or("the inspector window has no frame")?;
    let work = logical_work_area(&window).ok_or("the inspector window has no monitor")?;
    let (target, next) = match (wide, narrow) {
        (true, Some(_)) | (false, None) => return Ok(wide),
        (true, None) => {
            let (frame, narrow) = inspector_wide_frame(current, work);
            (frame, Some(narrow))
        }
        (false, Some(narrow)) => (inspector_narrow_frame(current, work, narrow), None),
    };
    move_window_frame(&window, current, target)?;
    if let Some(entry) = inspector_registry(&registry)?.find_mut(window.label()) {
        entry.narrow = next;
    }
    Ok(wide)
}

/// Moves a window to `target` with AppKit's own animation, so the window is
/// seen to stretch rather than jump. Only x and width change, and both run
/// left to right in points on either side, so the Cocoa frame needs no flip
/// from the top-left coordinates `target` is in.
#[cfg(target_os = "macos")]
fn move_window_frame(
    window: &tauri::WebviewWindow,
    current: WindowBox,
    target: WindowBox,
) -> Result<(), String> {
    use objc2_app_kit::{NSAnimatablePropertyContainer, NSAnimationContext, NSWindow};

    let handle = window.clone();
    let shift = target.x - current.x;
    let width = target.width;
    window
        .run_on_main_thread(move || {
            let Ok(pointer) = handle.ns_window() else {
                eprintln!("the inspector window has no native window to resize");
                return;
            };
            // SAFETY: tao owns this NSWindow for as long as `handle` lives, and
            // this closure runs on the main thread, where AppKit requires it.
            let ns_window = unsafe { &*pointer.cast::<NSWindow>() };
            let mut frame = ns_window.frame();
            frame.origin.x += shift;
            frame.size.width = width;
            NSAnimationContext::beginGrouping();
            NSAnimationContext::currentContext().setDuration(TRAJECTORY_INSPECTOR_RESIZE_SECONDS);
            ns_window.animator().setFrame_display(frame, true);
            NSAnimationContext::endGrouping();
        })
        .map_err(|error| error.to_string())
}

#[cfg(not(target_os = "macos"))]
fn move_window_frame(
    window: &tauri::WebviewWindow,
    _current: WindowBox,
    target: WindowBox,
) -> Result<(), String> {
    window
        .set_position(tauri::LogicalPosition::new(target.x, target.y))
        .map_err(|error| error.to_string())?;
    // Undecorated, so the inner size is the whole window.
    window
        .set_size(tauri::LogicalSize::new(target.width, target.height))
        .map_err(|error| error.to_string())
}

/// Closes the unpinned inspectors and leaves the pinned ones alone: pinning is
/// the operator saying they want to keep that phase on screen.
fn close_unpinned_inspectors(app: &tauri::AppHandle) {
    let Some(registry) = app.try_state::<Mutex<InspectorRegistry>>() else {
        return;
    };
    let labels = match registry.lock() {
        Ok(mut guard) => guard.remove_unpinned(),
        Err(_) => {
            eprintln!("inspector window registry is poisoned; unpinned windows stay open");
            return;
        }
    };
    for label in labels {
        if let Some(window) = app.get_webview_window(&label) {
            let _ = window.close();
        }
    }
}

#[tauri::command]
fn close_trajectory_inspectors(app: tauri::AppHandle) {
    close_unpinned_inspectors(&app);
}

fn inspector_window_title(locale: Locale, pinned: bool) -> String {
    let key = if pinned {
        "host.window.trajectoryInspectorPinned"
    } else {
        "host.window.trajectoryInspector"
    };
    i18n::t(locale, key, &[])
}

fn build_inspector_window(app: &tauri::AppHandle, label: &str, cascade: u32) -> Result<(), String> {
    let preferences = app
        .try_state::<Arc<PreferencesStore>>()
        .map(|store| store.snapshot().values)
        .unwrap_or_default();
    let locale = preferences.locale;
    let theme = preferences
        .theme
        .native_theme()
        .or_else(|| {
            app.get_webview_window("main")
                .and_then(|window| window.theme().ok())
        })
        .unwrap_or(tauri::Theme::Light);
    let mut builder = WebviewWindowBuilder::new(app, label, WebviewUrl::default())
        .title(inspector_window_title(locale, false))
        .background_color(theme_background(theme))
        .inner_size(TRAJECTORY_INSPECTOR_WIDTH, TRAJECTORY_INSPECTOR_HEIGHT)
        .min_inner_size(
            TRAJECTORY_INSPECTOR_MIN_WIDTH,
            TRAJECTORY_INSPECTOR_MIN_HEIGHT,
        )
        .resizable(true)
        .shadow(true);

    // The frontend draws its own title bar, so the inspector has to be
    // decorated exactly like the main window in tauri.conf.json.
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

    builder = match app
        .get_webview_window("main")
        .and_then(|main| inspector_anchor(&main, cascade))
    {
        Some((x, y)) => builder.position(x, y),
        None => builder.center(),
    };

    builder.build().map_err(|error| error.to_string())?;
    Ok(())
}

#[tauri::command]
async fn list_services(manager: State<'_, Arc<CoreManager>>) -> Result<serde_json::Value, String> {
    manager.list_services().await
}

#[tauri::command]
async fn get_service_order(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_service_order().await
}

#[tauri::command]
async fn update_service_order(
    service_ids: Vec<String>,
    etag: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.update_service_order(service_ids, &etag).await
}

#[tauri::command]
async fn get_service(
    service_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<ServiceRecordResponse, String> {
    manager.get_service(&service_id).await
}

#[tauri::command]
async fn create_service(
    input: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<ServiceRecordResponse, String> {
    manager.create_service(input).await
}

#[tauri::command]
async fn update_service(
    service_id: String,
    etag: String,
    patch: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<ServiceRecordResponse, String> {
    manager.update_service(&service_id, &etag, patch).await
}

#[tauri::command]
async fn delete_service(
    service_id: String,
    etag: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<(), String> {
    manager.delete_service(&service_id, &etag).await
}

#[tauri::command]
async fn pricing(
    operation: String,
    service_id: Option<String>,
    input: Option<serde_json::Value>,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager
        .pricing(&operation, service_id.as_deref(), input)
        .await
}

#[tauri::command]
async fn get_service_usage(
    service_id: String,
    fresh: Option<bool>,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager
        .get_service_usage_with(&service_id, fresh.unwrap_or(false))
        .await
}

#[tauri::command]
async fn get_service_reset_credits(
    service_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_service_reset_credits(&service_id).await
}

#[tauri::command]
async fn reset_service_usage(
    service_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.reset_service_usage(&service_id).await
}

#[tauri::command]
async fn test_service(
    service_id: String,
    input: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.test_service(&service_id, input).await
}

#[tauri::command]
async fn probe_service_models(
    service_id: String,
    input: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.probe_service_models(&service_id, input).await
}

#[tauri::command]
async fn probe_draft_service_models(
    input: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.probe_draft_service_models(input).await
}

#[tauri::command]
async fn probe_service_proxy(
    manager: State<'_, Arc<CoreManager>>,
    input: serde_json::Value,
) -> Result<serde_json::Value, String> {
    manager.probe_service_proxy(input).await
}

#[tauri::command]
async fn begin_service_authorization(
    service_id: String,
    flow: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager
        .begin_service_authorization(&service_id, &flow)
        .await
}

#[tauri::command]
fn open_authorization_url(url: String) -> Result<(), String> {
    sidecar::open_authorization_url(Some(&url))
}

#[tauri::command]
fn open_external_url(url: String) -> Result<(), String> {
    let parsed = reqwest::Url::parse(&url).map_err(|_| "invalid external URL".to_string())?;
    if parsed.scheme() != "https" || parsed.host_str().unwrap_or("").is_empty() {
        return Err("external URL must use https".to_string());
    }
    tauri_plugin_opener::open_url(url, None::<&str>)
        .map_err(|error| format!("unable to open URL in the system browser: {error}"))
}

#[tauri::command]
async fn complete_service_authorization(
    service_id: String,
    session_id: String,
    code: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager
        .complete_service_authorization(&service_id, &session_id, &code)
        .await
}

#[tauri::command]
async fn save_text_file(
    app: tauri::AppHandle,
    default_filename: String,
    contents: String,
) -> Result<Option<String>, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let Some(file) = app
            .dialog()
            .file()
            .set_file_name(&default_filename)
            .blocking_save_file()
        else {
            return Ok(None);
        };
        let path = file.into_path().map_err(|error| error.to_string())?;
        std::fs::write(&path, contents).map_err(|error| error.to_string())?;
        Ok(Some(path.to_string_lossy().into_owned()))
    })
    .await
    .map_err(|error| error.to_string())?
}

#[tauri::command]
async fn get_service_authorization(
    service_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_service_authorization(&service_id).await
}

#[tauri::command]
async fn cancel_service_authorization(
    service_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.cancel_service_authorization(&service_id).await
}

#[tauri::command]
async fn logout_service(
    service_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<ServiceRecordResponse, String> {
    manager.logout_service(&service_id).await
}

#[tauri::command]
async fn clear_service_risk(
    service_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<ServiceRecordResponse, String> {
    manager.clear_service_risk(&service_id).await
}

#[tauri::command]
async fn list_service_risk_events(
    service_id: String,
    limit: Option<u32>,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.list_service_risk_events(&service_id, limit).await
}

#[tauri::command]
async fn list_request_records(
    query: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.list_request_records(query).await
}

#[tauri::command]
async fn list_request_sessions(
    query: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.list_request_sessions(query).await
}

#[tauri::command]
async fn get_request_session(
    session_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_request_session(&session_id).await
}

#[tauri::command]
async fn get_session_channel_bindings(
    session_id: String,
    before: Option<i64>,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager
        .session_channel_bindings(&session_id, false, before)
        .await
}

#[tauri::command]
async fn release_session_channel_bindings(
    session_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager
        .session_channel_bindings(&session_id, true, None)
        .await
}

#[tauri::command]
async fn get_request_record(
    request_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_request_record(&request_id).await
}

#[tauri::command]
async fn list_request_record_children(
    request_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.list_request_record_children(&request_id).await
}

#[tauri::command]
async fn delete_request_record(
    request_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<(), String> {
    manager.delete_request_record(&request_id).await
}

#[tauri::command]
async fn purge_request_records(
    input: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.purge_request_records(input).await
}

#[tauri::command]
async fn get_request_audit_content(
    request_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_request_audit_content(&request_id).await
}

#[tauri::command]
async fn list_raw_access(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.list_raw_access().await
}

/// Turns a window's proof into one for Core.
fn raw_proof(arg: raw_access::ProofArg) -> raw_access::Proof {
    match arg {
        raw_access::ProofArg::Password { password } => raw_access::Proof::Password(password),
    }
}

#[tauri::command]
async fn decide_raw_access(
    grant_id: String,
    decision: String,
    proof: Option<raw_access::ProofArg>,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    // The desktop never keeps the proof: it is wiped once it has been sent.
    let proof = match proof {
        Some(arg) if decision != "deny" => Some(raw_proof(arg)),
        _ => None,
    };
    manager
        .decide_raw_access(&grant_id, &decision, proof.as_ref())
        .await
}

/// Ends a running timed agent grant before it expires.
#[tauri::command]
async fn revoke_raw_grant(
    grant_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.revoke_raw_grant(&grant_id).await
}

/// Raw sealing state plus whether the raw key was replaced outside the
/// desktop.
#[tauri::command]
async fn raw_sealing_status(
    manager: State<'_, Arc<CoreManager>>,
    pins: State<'_, Arc<raw_key_pin::RawKeyPins>>,
) -> Result<serde_json::Value, String> {
    let _pinning = pins.guard().await;
    let mut status = manager.raw_sealing_status().await?;
    note_raw_key(&pins, &manager, &mut status, raw_key_pin::PinAction::Check).await;
    Ok(status)
}

/// Adds `key_replaced` to a raw sealing status after applying `action` to
/// the pin of the running data directory, and hands the verdict to the tray.
/// Callers hold the pin guard.
async fn note_raw_key(
    pins: &Arc<raw_key_pin::RawKeyPins>,
    manager: &CoreManager,
    status: &mut serde_json::Value,
    action: raw_key_pin::PinAction,
) {
    let replaced = match manager.data_directory() {
        Some(data_directory) => {
            let account = kek_store::keychain_account(&data_directory);
            let pins = Arc::clone(pins);
            let observed = status.clone();
            tauri::async_runtime::spawn_blocking(move || pins.apply(&account, &observed, action))
                .await
                .unwrap_or(false)
        }
        None => false,
    };
    manager.note_raw_key_replaced(replaced);
    if let Some(object) = status.as_object_mut() {
        object.insert("key_replaced".to_string(), replaced.into());
    }
}

/// Checks the raw key pin without any window, when Core becomes ready or
/// records a raw key change, so the tray can warn about a key replaced while
/// the app was closed. Windows read the state again when the verdict moves.
pub(crate) async fn check_raw_key(app: tauri::AppHandle) {
    let (Some(manager), Some(pins)) = (
        app.try_state::<Arc<CoreManager>>(),
        app.try_state::<Arc<raw_key_pin::RawKeyPins>>(),
    ) else {
        return;
    };
    let (manager, pins) = (Arc::clone(&manager), Arc::clone(&pins));
    let _pinning = pins.guard().await;
    // Errors are transient (Core restarting); the next ready checks again.
    let Ok(mut status) = manager.raw_sealing_status().await else {
        return;
    };
    let before = manager.view().raw_key_replaced;
    note_raw_key(&pins, &manager, &mut status, raw_key_pin::PinAction::Check).await;
    if manager.view().raw_key_replaced != before {
        let _ = broadcast_raw_sealing_change(&app, Ok(serde_json::Value::Null));
    }
}

/// The new raw sealing state inside a successful unlock or password outcome.
fn sealing_outcome_status(outcome: &mut serde_json::Value) -> Option<&mut serde_json::Value> {
    if outcome.get("outcome")?.as_str()? != "sealing" {
        return None;
    }
    outcome.get_mut("status")
}

/// Applies `action` to the status of a successful proof outcome; refusals
/// pass through unchanged.
async fn note_raw_key_outcome(
    pins: &Arc<raw_key_pin::RawKeyPins>,
    manager: &CoreManager,
    result: Result<serde_json::Value, String>,
    action: raw_key_pin::PinAction,
) -> Result<serde_json::Value, String> {
    let mut outcome = result?;
    if let Some(status) = sealing_outcome_status(&mut outcome) {
        note_raw_key(pins, manager, status, action).await;
    }
    Ok(outcome)
}

/// Tells every window that the raw unlock or key may have changed, so one
/// showing raw parts, such as a pinned inspector, reads the state again.
const RAW_SEALING_CHANGED_EVENT: &str = "raw-sealing-changed";

fn broadcast_raw_sealing_change(
    app: &tauri::AppHandle,
    result: Result<serde_json::Value, String>,
) -> Result<serde_json::Value, String> {
    if result.is_ok() {
        if let Err(error) = app.emit(RAW_SEALING_CHANGED_EVENT, ()) {
            eprintln!("unable to broadcast a raw sealing change: {error}");
        }
    }
    result
}

#[tauri::command]
async fn unlock_raw(
    app: tauri::AppHandle,
    proof: raw_access::ProofArg,
    manager: State<'_, Arc<CoreManager>>,
    pins: State<'_, Arc<raw_key_pin::RawKeyPins>>,
) -> Result<serde_json::Value, String> {
    let proof = raw_proof(proof);
    let _pinning = pins.guard().await;
    let result = manager.unlock_raw(&proof).await;
    let result = note_raw_key_outcome(&pins, &manager, result, raw_key_pin::PinAction::Check).await;
    broadcast_raw_sealing_change(&app, result)
}

/// The operator's own lock also revokes every agent grant; locking when the
/// main window hides keeps them (`raw_approval::lock_on_hide`).
#[tauri::command]
async fn lock_raw(
    app: tauri::AppHandle,
    manager: State<'_, Arc<CoreManager>>,
    pins: State<'_, Arc<raw_key_pin::RawKeyPins>>,
) -> Result<serde_json::Value, String> {
    let _pinning = pins.guard().await;
    let result = match manager.lock_raw(false).await {
        Ok(mut status) => {
            note_raw_key(&pins, &manager, &mut status, raw_key_pin::PinAction::Check).await;
            Ok(status)
        }
        Err(error) => Err(error),
    };
    broadcast_raw_sealing_change(&app, result)
}

/// Accepts a raw key replaced outside the desktop, such as by the operator's
/// own `astrlink-core raw-password`. Only the key's own password accepts it:
/// that shows the operator chose the key. Nothing unlocks raw parts.
#[tauri::command]
async fn acknowledge_raw_key(
    app: tauri::AppHandle,
    proof: raw_access::ProofArg,
    manager: State<'_, Arc<CoreManager>>,
    pins: State<'_, Arc<raw_key_pin::RawKeyPins>>,
) -> Result<serde_json::Value, String> {
    let proof = raw_proof(proof);
    let result = acknowledge_replaced_key(&manager, &pins, &proof).await;
    broadcast_raw_sealing_change(&app, result)
}

/// The acknowledgement behind `acknowledge_raw_key`. Core's verify route
/// checks the proof without starting an unlock session, and the key its
/// answer names is pinned.
async fn acknowledge_replaced_key(
    manager: &CoreManager,
    pins: &Arc<raw_key_pin::RawKeyPins>,
    proof: &raw_access::Proof,
) -> Result<serde_json::Value, String> {
    let _pinning = pins.guard().await;
    let result = manager.verify_raw(proof).await;
    note_raw_key_outcome(pins, manager, result, raw_key_pin::PinAction::Pin).await
}

/// Sets, changes, or resets the raw password. `password` is the new one;
/// `proof` opens the existing key where Core needs it.
#[tauri::command]
async fn set_raw_password(
    app: tauri::AppHandle,
    action: String,
    password: Option<zeroize::Zeroizing<String>>,
    proof: Option<raw_access::ProofArg>,
    manager: State<'_, Arc<CoreManager>>,
    pins: State<'_, Arc<raw_key_pin::RawKeyPins>>,
) -> Result<serde_json::Value, String> {
    let proof = proof.map(raw_proof);
    let _pinning = pins.guard().await;
    let result = manager
        .change_raw_password(
            &action,
            password.as_deref().map(String::as_str),
            proof.as_ref(),
        )
        .await;
    let pin_action = raw_key_pin::PinAction::after_password(&action);
    let result = note_raw_key_outcome(&pins, &manager, result, pin_action).await;
    broadcast_raw_sealing_change(&app, result)
}

#[tauri::command]
async fn builtin_tool_action(
    kind: String,
    action: String,
    input: Option<serde_json::Value>,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.builtin_tool_action(kind, action, input).await
}

#[tauri::command]
async fn get_routing_settings(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_routing_settings().await
}

#[tauri::command]
async fn update_routing_settings(
    patch: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.update_routing_settings(patch).await
}

#[tauri::command]
async fn get_client_identities(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_client_identities().await
}

#[tauri::command]
async fn get_audit_settings(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_audit_settings().await
}

#[tauri::command]
async fn local_data_status(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.local_data_status().await
}

#[tauri::command]
async fn update_audit_settings(
    patch: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.update_audit_settings(patch).await
}

#[tauri::command]
async fn list_access_tokens(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.list_access_tokens().await
}

#[tauri::command]
async fn get_usage_summary(
    from: String,
    to: String,
    time_zone: String,
    bucket: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager
        .get_usage_summary(&from, &to, &time_zone, &bucket)
        .await
}

#[tauri::command]
async fn list_access_token_usage(
    today_from: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.list_access_token_usage(&today_from).await
}

#[tauri::command]
async fn create_access_token(
    name: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.create_access_token(&name).await
}

/// Copies an access token from Core straight to the clipboard: the token
/// never reaches the webview, and no click gesture has to outlast the
/// reveal. `false` means the clipboard refused it.
#[tauri::command]
async fn copy_access_token(
    app: tauri::AppHandle,
    token_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<bool, String> {
    let secret = manager.reveal_access_token(&token_id).await?;
    let token = secret["access_token"]
        .as_str()
        .filter(|token| !token.is_empty())
        .ok_or("access token reveal returned no token")?;
    match app.clipboard().write_text(token) {
        Ok(()) => Ok(true),
        Err(error) => {
            eprintln!("unable to copy the access token: {error}");
            Ok(false)
        }
    }
}

#[tauri::command]
async fn cc_switch_installed() -> Result<bool, String> {
    tauri::async_runtime::spawn_blocking(cc_switch::installed)
        .await
        .map_err(|error| error.to_string())
}

#[tauri::command]
async fn open_cc_switch_import(
    token_id: String,
    client: client_config::Client,
    models: client_config::Models,
    inference_url: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<(), String> {
    cc_switch::open_import(&manager, &token_id, client, &models, &inference_url).await
}

/// Read-only: reports whether the system proxy keeps Codex from reaching
/// the gateway, and never changes proxy settings.
#[tauri::command]
async fn check_client_proxy(inference_url: String) -> Result<proxy_check::ProxyCheck, String> {
    proxy_check::check(&inference_url).await
}

#[tauri::command]
async fn client_config_status(
    inference_url: Option<String>,
) -> Result<Vec<client_config::ClientStatus>, String> {
    let home = control_session::user_home()?;
    tauri::async_runtime::spawn_blocking(move || {
        client_config::status(&home, inference_url.as_deref())
    })
    .await
    .map_err(|error| error.to_string())?
}

#[tauri::command]
async fn apply_client_config(
    token_id: String,
    client: client_config::Client,
    models: client_config::Models,
    inference_url: String,
    replace: bool,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<client_config::ApplyOutcome, String> {
    let home = control_session::user_home()?;
    client_config::apply(
        &manager,
        home,
        token_id,
        client,
        &models,
        inference_url,
        replace,
    )
    .await
}

#[tauri::command]
async fn remove_client_config(client: client_config::Client) -> Result<(), String> {
    let home = control_session::user_home()?;
    tauri::async_runtime::spawn_blocking(move || client_config::remove(&home, client))
        .await
        .map_err(|error| error.to_string())?
}

/// The config `apply_client_config` would write to a fresh file, with the
/// token shown as its hint.
#[tauri::command]
async fn preview_client_config_snippet(
    token_id: String,
    client: client_config::Client,
    models: client_config::Models,
    inference_url: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<String, String> {
    let origin = client_config::local_origin(&inference_url)?;
    let models = models.fields(client)?;
    let (_, hint) = client_config::token_summary(&manager, &token_id).await?;
    client_config::snippet(
        client,
        &client_config::Connection {
            token_id: &token_id,
            token: &hint,
            origin: &origin,
            models: &models,
        },
    )
}

/// Copies the real snippet straight to the clipboard, like
/// `copy_access_token`. `false` means the clipboard refused it.
#[tauri::command]
async fn copy_client_config_snippet(
    app: tauri::AppHandle,
    token_id: String,
    client: client_config::Client,
    models: client_config::Models,
    inference_url: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<bool, String> {
    let origin = client_config::local_origin(&inference_url)?;
    let models = models.fields(client)?;
    let token = client_config::reveal_access_token(&manager, &token_id, &inference_url).await?;
    let snippet = client_config::snippet(
        client,
        &client_config::Connection {
            token_id: &token_id,
            token: &token,
            origin: &origin,
            models: &models,
        },
    )?;
    match app.clipboard().write_text(snippet) {
        Ok(()) => Ok(true),
        Err(error) => {
            eprintln!("unable to copy the client config: {error}");
            Ok(false)
        }
    }
}

#[tauri::command]
async fn delete_access_token(
    token_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<(), String> {
    manager.delete_access_token(&token_id).await
}

#[tauri::command]
async fn list_privacy_policies(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.list_privacy_policies().await
}

#[tauri::command]
async fn get_privacy_policy(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<PolicyRecordResponse, String> {
    manager.get_privacy_policy().await
}

#[tauri::command]
async fn update_privacy_policy(
    etag: String,
    patch: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<PolicyRecordResponse, String> {
    manager.update_privacy_policy(&etag, patch).await
}

#[tauri::command]
async fn dry_run_privacy_policy(
    input: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.dry_run_privacy_policy(input).await
}

#[tauri::command]
async fn get_privacy_regex_builtin_rules(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_privacy_regex_builtin_rules().await
}

#[tauri::command]
async fn get_privacy_model_catalog(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_privacy_model_catalog().await
}

#[tauri::command]
async fn get_privacy_model_releases(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_privacy_model_releases().await
}

#[tauri::command]
async fn probe_privacy_model(
    input: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.probe_privacy_model(input).await
}

async fn probe_local_privacy_model_with_manager(
    input: serde_json::Value,
    manager: &CoreManager,
) -> Result<serde_json::Value, String> {
    manager.probe_local_privacy_model(input).await
}

#[tauri::command]
async fn probe_local_privacy_model(
    input: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    probe_local_privacy_model_with_manager(input, manager.inner()).await
}

#[tauri::command]
async fn list_privacy_model_installations(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.list_privacy_model_installations().await
}

#[tauri::command]
async fn install_privacy_model(
    input: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.install_privacy_model(input).await
}

#[tauri::command]
async fn get_privacy_model_installation(
    installation_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager
        .get_privacy_model_installation(&installation_id)
        .await
}

#[tauri::command]
async fn pause_privacy_model_installation(
    installation_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager
        .pause_privacy_model_installation(&installation_id)
        .await
}

#[tauri::command]
async fn resume_privacy_model_installation(
    installation_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager
        .resume_privacy_model_installation(&installation_id)
        .await
}

#[tauri::command]
async fn delete_privacy_model_installation(
    installation_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<(), String> {
    manager
        .delete_privacy_model_installation(&installation_id)
        .await
}

fn platform_initialization_script(platform: &str) -> String {
    let encoded = serde_json::to_string(platform).expect("desktop platform should serialize");
    format!("window.__ASTRLINK_DESKTOP_PLATFORM__ = {encoded};")
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // Cocoa must see the .app before NSApplication is initialized. Replacing
    // this process preserves the PID and Tauri dev's signal/restart handling.
    #[cfg(all(target_os = "macos", dev))]
    if let Err(error) = macos_app::enter_dev_bundle() {
        eprintln!("unable to start the AstrLink development app bundle: {error}");
        std::process::exit(1);
    }

    let manager = Arc::new(CoreManager::new());
    let setup_manager = Arc::clone(&manager);
    let explicit_quit = Arc::new(AtomicBool::new(false));

    let builder = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            show_main_window(app);
        }))
        // After single-instance, which forwards a second launch's link here.
        .plugin(tauri_plugin_deep_link::init())
        .plugin(tauri_plugin_autostart::init(
            tauri_plugin_autostart::MacosLauncher::LaunchAgent,
            None,
        ))
        .append_invoke_initialization_script(platform_initialization_script(std::env::consts::OS))
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .plugin(tauri_plugin_dialog::init());
    #[cfg(not(target_os = "macos"))]
    let builder = builder.plugin(tauri_plugin_notification::init());

    let app = builder
        .manage(manager)
        .manage(explicit_quit)
        .manage(Mutex::new(InspectorRegistry::default()))
        .manage(tray::TrayState::default())
        .manage(Arc::new(client_updates::ClientUpdateManager::default()))
        .manage(provider_import::ProviderImports::default())
        .invoke_handler(tauri::generate_handler![
            client_updates::local_client_status,
            provider_import::pending_provider_import,
            provider_import::dismiss_provider_import,
            provider_import::confirm_provider_import,
            client_updates::refresh_local_clients,
            client_updates::update_local_clients,
            updates::app_update_status,
            updates::check_app_update,
            updates::download_app_update,
            updates::install_app_update,
            updates::update_update_preferences,
            core_status,
            window_chrome_preferences,
            get_preferences,
            update_preferences,
            tray_state,
            tray_action,
            tray_popover_resize,
            tray_popover_hide,
            start_core,
            stop_core,
            restart_core,
            list_services,
            get_service_order,
            update_service_order,
            get_service,
            create_service,
            update_service,
            delete_service,
            get_service_usage,
            get_service_reset_credits,
            pricing,
            reset_service_usage,
            test_service,
            probe_service_models,
            probe_draft_service_models,
            probe_service_proxy,
            begin_service_authorization,
            complete_service_authorization,
            open_authorization_url,
            open_external_url,
            save_text_file,
            get_service_authorization,
            cancel_service_authorization,
            logout_service,
            clear_service_risk,
            list_service_risk_events,
            list_request_records,
            list_request_sessions,
            get_request_session,
            get_session_channel_bindings,
            release_session_channel_bindings,
            get_request_record,
            list_request_record_children,
            delete_request_record,
            purge_request_records,
            get_request_audit_content,
            list_raw_access,
            decide_raw_access,
            revoke_raw_grant,
            raw_sealing_status,
            unlock_raw,
            lock_raw,
            acknowledge_raw_key,
            set_raw_password,
            builtin_tool_action,
            get_routing_settings,
            update_routing_settings,
            get_client_identities,
            get_audit_settings,
            update_audit_settings,
            local_data_status,
            list_access_tokens,
            list_access_token_usage,
            get_usage_summary,
            create_access_token,
            copy_access_token,
            cc_switch_installed,
            open_cc_switch_import,
            client_config_status,
            check_client_proxy,
            apply_client_config,
            remove_client_config,
            preview_client_config_snippet,
            copy_client_config_snippet,
            delete_access_token,
            list_privacy_policies,
            get_privacy_policy,
            update_privacy_policy,
            dry_run_privacy_policy,
            get_privacy_regex_builtin_rules,
            get_privacy_model_catalog,
            get_privacy_model_releases,
            probe_privacy_model,
            probe_local_privacy_model,
            list_privacy_model_installations,
            install_privacy_model,
            get_privacy_model_installation,
            delete_privacy_model_installation,
            pause_privacy_model_installation,
            resume_privacy_model_installation,
            agent_debug_status,
            install_agent_debug,
            uninstall_agent_debug,
            show_trajectory_inspector,
            update_trajectory_inspector,
            trajectory_inspector_state,
            set_trajectory_inspector_pinned,
            set_trajectory_inspector_wide,
            close_trajectory_inspectors
        ])
        .setup(move |app| {
            let config_directory = app
                .path()
                .app_config_dir()
                .map_err(|error| format!("unable to resolve AstrLink config directory: {error}"))?;
            let data_directory = app
                .path()
                .app_data_dir()
                .map_err(|error| format!("unable to resolve AstrLink data directory: {error}"))?;
            let preferences = Arc::new(PreferencesStore::load(&config_directory, &data_directory));
            let raw_key_pins = Arc::new(raw_key_pin::RawKeyPins::system(&config_directory));
            let raw_key_pin_file = raw_key_pins.file().map(std::path::Path::to_path_buf);
            app.manage(raw_key_pins);
            let values = preferences.snapshot().values;
            apply_native_theme(app.handle(), values.theme);
            if let Some(window) = app.get_webview_window("main") {
                if let Err(error) = startup_window::fit_to_monitor(&window) {
                    eprintln!("failed to size AstrLink for the current display: {error}");
                }
                window.show()?;
            }
            setup_manager.configure(&values);
            let autostart = app.autolaunch();
            let reconciliation = autostart
                .is_enabled()
                .map_err(|error| error.to_string())
                .and_then(|actual| {
                    if actual == values.autostart {
                        return Ok(());
                    }
                    if values.autostart {
                        autostart.enable().map_err(|error| error.to_string())
                    } else {
                        autostart.disable().map_err(|error| error.to_string())
                    }
                });
            if let Err(error) = reconciliation {
                preferences.report_warning(i18n::t(
                    values.locale,
                    "host.autostart.startupCheckFailed",
                    &[("error", &error)],
                ));
            }
            start_client_config_sync(&setup_manager, Arc::clone(&preferences));
            app.manage(preferences);
            let updates =
                Arc::new(updates::UpdateManager::new(app.handle()).map_err(std::io::Error::other)?);
            app.manage(Arc::clone(&updates));
            updates.start(app.handle().clone());

            tray::build(app.handle())?;
            tray::start(app.handle());

            let deep_link = app.deep_link();
            // Installers register the scheme; this repairs portable copies.
            #[cfg(any(windows, target_os = "linux"))]
            if let Err(error) = deep_link.register_all() {
                eprintln!("unable to register astrlink:// links: {error}");
            }
            let handle = app.handle().clone();
            deep_link.on_open_url(move |event| {
                receive_deep_links(&handle, event.urls());
            });
            match deep_link.get_current() {
                Ok(urls) => receive_deep_links(app.handle(), urls.unwrap_or_default()),
                Err(error) => eprintln!("unable to read the launch link: {error}"),
            }

            #[cfg(debug_assertions)]
            dev_reload::start(app.handle());

            if let Ok(home) = control_session::user_home() {
                if let Err(error) = agent_install::sync_installed_skills(&home) {
                    eprintln!("failed to sync AstrLink agent skills: {error}");
                }
                if let Err(error) = agent_install::sync_installed_host_guards(
                    &home,
                    Some(&data_directory),
                    raw_key_pin_file.as_deref(),
                ) {
                    eprintln!("failed to sync AstrLink agent host guards: {error}");
                }
                // Runs without a sidecar too, so the MCP migration still happens.
                if let Err(error) =
                    agent_install::sync_installed_cli(&agent_install::InstallContext {
                        home,
                        cli_source: agent_install::resolve_sidecar_binary("astrlink-cli")
                            .unwrap_or_default(),
                        data_directory: Some(data_directory.clone()),
                        raw_key_pins: raw_key_pin_file.clone(),
                    })
                {
                    eprintln!("failed to sync AstrLink agent CLI: {error}");
                }
            }

            if values.core_auto_start {
                // Off the main thread: resolving the local key may wait on a
                // keychain prompt.
                let handle = app.handle().clone();
                tauri::async_runtime::spawn_blocking(move || {
                    if let Err(error) = setup_manager.start(&handle) {
                        eprintln!("failed to start astrlink-core: {error}");
                    }
                });
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("failed to build AstrLink desktop app");

    app.run(|app_handle, event| {
        if let RunEvent::WindowEvent {
            label,
            event: WindowEvent::ThemeChanged(theme),
            ..
        } = &event
        {
            if label != tray::POPOVER_LABEL {
                if let Some(window) = app_handle.get_webview_window(label) {
                    if let Err(error) = window.set_background_color(Some(theme_background(*theme)))
                    {
                        eprintln!("unable to follow AstrLink window theme: {error}");
                    }
                }
            }
        }
        if let RunEvent::WindowEvent {
            label,
            event: WindowEvent::Focused(false),
            ..
        } = &event
        {
            if label == tray::POPOVER_LABEL {
                tray::on_popover_blur(app_handle);
            }
        }
        if let RunEvent::WindowEvent {
            label,
            event: WindowEvent::Focused(focused),
            ..
        } = &event
        {
            if label == "main" {
                raw_approval::on_main_focus(app_handle, *focused);
            }
        }
        if let RunEvent::WindowEvent {
            label,
            event: WindowEvent::Resized(_),
            ..
        } = &event
        {
            if label == "main" {
                raw_approval::on_main_resized(app_handle);
            }
        }
        if let RunEvent::WindowEvent {
            label,
            event: WindowEvent::Destroyed,
            ..
        } = &event
        {
            if let Some(registry) = app_handle.try_state::<Mutex<InspectorRegistry>>() {
                match registry.lock() {
                    Ok(mut guard) => guard.remove(label),
                    Err(_) => eprintln!("inspector window registry is poisoned; {label} leaked"),
                }
            }
        }
        if let RunEvent::WindowEvent {
            label,
            event: WindowEvent::CloseRequested { api, .. },
            ..
        } = &event
        {
            if label == "main" && !app_handle.state::<Arc<AtomicBool>>().load(Ordering::SeqCst) {
                let behavior = app_handle
                    .state::<Arc<PreferencesStore>>()
                    .snapshot()
                    .values
                    .close_behavior;
                if behavior == CloseBehavior::HideToTray {
                    api.prevent_close();
                    hide_main_window_to_tray(app_handle);
                } else {
                    app_handle
                        .state::<Arc<AtomicBool>>()
                        .store(true, Ordering::SeqCst);
                }
            }
        }
        #[cfg(target_os = "macos")]
        if let RunEvent::Reopen { .. } = &event {
            if !app_handle.state::<Arc<AtomicBool>>().load(Ordering::SeqCst) {
                show_main_window(app_handle);
            }
        }
        if matches!(event, RunEvent::Exit | RunEvent::ExitRequested { .. }) {
            if let Some(manager) = app_handle.try_state::<Arc<CoreManager>>() {
                let manager = Arc::clone(manager.inner());
                if let Err(error) = tauri::async_runtime::block_on(manager.stop_and_wait()) {
                    eprintln!("astrlink-core did not stop cleanly during desktop exit: {error}");
                }
            }
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;
    use sidecar::CorePhase;

    #[derive(Default)]
    struct MemoryPins(std::sync::Mutex<std::collections::BTreeMap<String, String>>);

    impl raw_key_pin::PinStore for MemoryPins {
        fn load(&self, account: &str) -> Result<Option<String>, String> {
            Ok(self.0.lock().unwrap().get(account).cloned())
        }

        fn save(&self, account: &str, pin: &str) -> Result<(), String> {
            self.0
                .lock()
                .unwrap()
                .insert(account.to_string(), pin.to_string());
            Ok(())
        }
    }

    #[test]
    fn raw_key_verdicts_reach_the_status_and_the_tray() {
        use raw_key_pin::PinAction;

        let pins = Arc::new(raw_key_pin::RawKeyPins::new(
            Box::new(MemoryPins::default()),
        ));
        let manager = CoreManager::new();
        let keyed = |fingerprint: String| serde_json::json!({ "password_set": true, "key_fingerprint": fingerprint });
        let (first, second) = ("a".repeat(64), "b".repeat(64));
        tauri::async_runtime::block_on(async {
            // Without a data directory there is nothing to compare with.
            let mut status = keyed(first.clone());
            note_raw_key(&pins, &manager, &mut status, PinAction::Check).await;
            assert_eq!(status["key_replaced"], false);

            manager.ready_for_tests(std::path::PathBuf::from("/nonexistent/astrlink-data"));
            let mut status = keyed(first.clone());
            note_raw_key(&pins, &manager, &mut status, PinAction::Check).await;
            assert_eq!(status["key_replaced"], false);
            assert!(!manager.view().raw_key_replaced);

            let mut status = keyed(second.clone());
            note_raw_key(&pins, &manager, &mut status, PinAction::Check).await;
            assert_eq!(status["key_replaced"], true);
            assert!(manager.view().raw_key_replaced);

            // A refusal leaves the verdict; accepting the key clears it.
            let refused = serde_json::json!({ "outcome": "password_invalid" });
            let refused = note_raw_key_outcome(&pins, &manager, Ok(refused), PinAction::Pin)
                .await
                .unwrap();
            assert!(refused.get("status").is_none());
            assert!(manager.view().raw_key_replaced);
            let accepted =
                serde_json::json!({ "outcome": "sealing", "status": keyed(second.clone()) });
            let accepted = note_raw_key_outcome(&pins, &manager, Ok(accepted), PinAction::Pin)
                .await
                .unwrap();
            assert_eq!(accepted["status"]["key_replaced"], false);
            assert!(!manager.view().raw_key_replaced);
            let mut status = keyed(second);
            note_raw_key(&pins, &manager, &mut status, PinAction::Check).await;
            assert_eq!(status["key_replaced"], false);
        });
    }

    /// A control API that keeps an unlock session like Core's: raw-unlock
    /// opens it, raw-lock ends it, raw-verify only checks the password. It
    /// answers every status with `fingerprint` and records each request.
    struct FakeRawCore {
        url: String,
        requests: Arc<std::sync::Mutex<Vec<String>>>,
        stop: Arc<std::sync::atomic::AtomicBool>,
        server: Option<std::thread::JoinHandle<()>>,
    }

    impl FakeRawCore {
        const PASSWORD: &'static str = "the terminal password";

        fn start(fingerprint: String) -> Self {
            use std::io::{BufRead, Read, Write};
            use std::sync::atomic::Ordering;

            let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
            listener.set_nonblocking(true).unwrap();
            let url = format!("http://{}", listener.local_addr().unwrap());
            let requests = Arc::new(std::sync::Mutex::new(Vec::new()));
            let stop = Arc::new(std::sync::atomic::AtomicBool::new(false));
            let (seen, stopping) = (Arc::clone(&requests), Arc::clone(&stop));
            let server = std::thread::spawn(move || {
                let mut unlocked = false;
                while !stopping.load(Ordering::SeqCst) {
                    let Ok((mut stream, _)) = listener.accept() else {
                        std::thread::sleep(std::time::Duration::from_millis(5));
                        continue;
                    };
                    stream.set_nonblocking(false).unwrap();
                    stream
                        .set_read_timeout(Some(std::time::Duration::from_secs(5)))
                        .unwrap();
                    let mut reader = std::io::BufReader::new(&mut stream);
                    let mut request_line = String::new();
                    reader.read_line(&mut request_line).unwrap();
                    let mut length = 0;
                    loop {
                        let mut line = String::new();
                        if reader.read_line(&mut line).unwrap() == 0 || line == "\r\n" {
                            break;
                        }
                        if let Some(value) =
                            line.to_ascii_lowercase().strip_prefix("content-length:")
                        {
                            length = value.trim().parse::<usize>().unwrap();
                        }
                    }
                    let mut body = vec![0; length];
                    reader.read_exact(&mut body).unwrap();
                    let mut words = request_line.split_whitespace();
                    let request = format!(
                        "{} {}",
                        words.next().unwrap_or_default(),
                        words.next().unwrap_or_default()
                    );
                    seen.lock().unwrap().push(request.clone());
                    let body: serde_json::Value =
                        serde_json::from_slice(&body).unwrap_or(serde_json::Value::Null);
                    let right = body["proof"]["password"] == Self::PASSWORD;
                    let (status, answer) = match request.as_str() {
                        "POST /control/v1/audit/raw-unlock" if right => {
                            unlocked = true;
                            ("200 OK", None)
                        }
                        "POST /control/v1/audit/raw-verify" if right => ("200 OK", None),
                        "POST /control/v1/audit/raw-unlock"
                        | "POST /control/v1/audit/raw-verify" => (
                            "403 Forbidden",
                            Some(
                                serde_json::json!({"error": {"code": "raw_password_invalid", "message": "wrong"}}),
                            ),
                        ),
                        "POST /control/v1/audit/raw-lock" => {
                            unlocked = false;
                            ("200 OK", None)
                        }
                        "GET /control/v1/audit/raw-sealing" => ("200 OK", None),
                        _ => (
                            "404 Not Found",
                            Some(
                                serde_json::json!({"error": {"code": "not_found", "message": "no route"}}),
                            ),
                        ),
                    };
                    let answer = answer.unwrap_or_else(|| {
                        serde_json::json!({
                            "configured": true,
                            "password_set": true,
                            "unlocked": unlocked,
                            "key_fingerprint": fingerprint,
                        })
                    });
                    let answer = answer.to_string();
                    let _ = write!(
                        stream,
                        "HTTP/1.1 {status}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{answer}",
                        answer.len()
                    );
                }
            });
            Self {
                url,
                requests,
                stop,
                server: Some(server),
            }
        }

        fn take_requests(&self) -> Vec<String> {
            std::mem::take(&mut *self.requests.lock().unwrap())
        }
    }

    impl Drop for FakeRawCore {
        fn drop(&mut self) {
            self.stop.store(true, std::sync::atomic::Ordering::SeqCst);
            if let Some(server) = self.server.take() {
                let _ = server.join();
            }
        }
    }

    /// Pins `pinned`, then lets the fake Core answer with its own key, so
    /// the desktop sees that key as replaced.
    async fn replace_raw_key(
        pins: &Arc<raw_key_pin::RawKeyPins>,
        manager: &CoreManager,
        core: &FakeRawCore,
        pinned: &str,
    ) {
        use raw_key_pin::PinAction;

        let mut status = serde_json::json!({ "password_set": true, "key_fingerprint": pinned });
        note_raw_key(pins, manager, &mut status, PinAction::Pin).await;
        let mut status = manager.raw_sealing_status().await.unwrap();
        note_raw_key(pins, manager, &mut status, PinAction::Check).await;
        assert_eq!(status["key_replaced"], true);
        core.take_requests();
    }

    #[test]
    fn acknowledging_a_replaced_key_checks_the_password_without_unlocking() {
        let (pinned, replacement) = ("a".repeat(64), "b".repeat(64));
        let core = FakeRawCore::start(replacement.clone());
        let pins = Arc::new(raw_key_pin::RawKeyPins::new(
            Box::new(MemoryPins::default()),
        ));
        let manager = CoreManager::new();
        manager.serve_control_for_tests(
            std::path::PathBuf::from("/nonexistent/astrlink-data"),
            core.url.clone(),
        );
        let password =
            |value: &str| raw_access::Proof::Password(zeroize::Zeroizing::new(value.to_string()));
        tauri::async_runtime::block_on(async {
            // Core only checks the password: no unlock session opens, and
            // the key it names is pinned.
            replace_raw_key(&pins, &manager, &core, &pinned).await;
            let outcome =
                acknowledge_replaced_key(&manager, &pins, &password(FakeRawCore::PASSWORD))
                    .await
                    .unwrap();
            assert_eq!(outcome["outcome"], "sealing");
            assert_eq!(outcome["status"]["unlocked"], false);
            assert_eq!(outcome["status"]["key_replaced"], false);
            assert!(!manager.view().raw_key_replaced);
            assert_eq!(core.take_requests(), ["POST /control/v1/audit/raw-verify"]);
            let after = manager.raw_sealing_status().await.unwrap();
            assert_eq!(
                after["unlocked"], false,
                "an acknowledgement left raw parts unlocked"
            );

            // A wrong password pins nothing.
            replace_raw_key(&pins, &manager, &core, &pinned).await;
            let refused = acknowledge_replaced_key(&manager, &pins, &password("not the password"))
                .await
                .unwrap();
            assert_eq!(refused["outcome"], "password_invalid");
            assert!(manager.view().raw_key_replaced);
            assert_eq!(core.take_requests(), ["POST /control/v1/audit/raw-verify"]);
        });
    }

    #[test]
    fn app_snapshot_serializes_as_one_flat_contract() {
        let snapshot = AppSnapshot {
            app_version: "0.1.0".to_string(),
            core: CoreSnapshot {
                phase: CorePhase::Stopped,
                pid: None,
                ready: None,
                health: None,
                version: None,
                capabilities: None,
                last_error: None,
                inference_port_fallback: None,
                recovery_attempt: 0,
                recovery_scheduled_in_ms: None,
            },
        };

        let value = serde_json::to_value(snapshot).expect("snapshot should serialize");
        assert_eq!(value["app_version"], "0.1.0");
        assert_eq!(value["phase"], "stopped");
        assert!(value.get("core").is_none());
    }

    #[test]
    fn window_chrome_preferences_keep_platform_metadata_internal() {
        let preferences = WindowChromePreferences {
            platform: "linux",
            decoration_layout: Some("close:minimize,maximize".to_string()),
        };

        let value = serde_json::to_value(preferences).expect("preferences should serialize");
        assert_eq!(value["platform"], "linux");
        assert_eq!(value["decoration_layout"], "close:minimize,maximize");
    }

    fn wide_monitor() -> WindowBox {
        WindowBox {
            x: 0.0,
            y: 0.0,
            width: 2560.0,
            height: 1440.0,
        }
    }

    fn roomy_main() -> WindowBox {
        WindowBox {
            x: 200.0,
            y: 120.0,
            width: 1120.0,
            height: 780.0,
        }
    }

    #[test]
    fn inspector_parks_beside_the_main_window() {
        assert_eq!(
            inspector_placement(
                roomy_main(),
                wide_monitor(),
                (TRAJECTORY_INSPECTOR_WIDTH, TRAJECTORY_INSPECTOR_HEIGHT),
                0
            ),
            (200.0 + 1120.0 + TRAJECTORY_INSPECTOR_GAP, 120.0)
        );
    }

    #[test]
    fn each_extra_inspector_cascades_off_the_last() {
        let (first_x, first_y) = inspector_placement(
            roomy_main(),
            wide_monitor(),
            (TRAJECTORY_INSPECTOR_WIDTH, TRAJECTORY_INSPECTOR_HEIGHT),
            0,
        );
        let (third_x, third_y) = inspector_placement(
            roomy_main(),
            wide_monitor(),
            (TRAJECTORY_INSPECTOR_WIDTH, TRAJECTORY_INSPECTOR_HEIGHT),
            2,
        );

        // Otherwise a pinned window and the one opened after it land on exactly
        // the same pixels and look like a single window.
        assert_eq!(third_x - first_x, 2.0 * TRAJECTORY_INSPECTOR_CASCADE);
        assert_eq!(third_y - first_y, 2.0 * TRAJECTORY_INSPECTOR_CASCADE);
    }

    #[test]
    fn inspector_stays_on_screen_when_the_main_window_hugs_the_edge() {
        let monitor = WindowBox {
            x: 0.0,
            y: 0.0,
            width: 1440.0,
            height: 900.0,
        };
        let main = WindowBox {
            x: 300.0,
            y: 600.0,
            width: 1120.0,
            height: 780.0,
        };

        // Even a deep cascade must not push a window past the monitor edge,
        // where it cannot be dragged back on Windows and X11.
        let (x, y) = inspector_placement(
            main,
            monitor,
            (TRAJECTORY_INSPECTOR_WIDTH, TRAJECTORY_INSPECTOR_HEIGHT),
            9,
        );

        assert_eq!(x, 1440.0 - TRAJECTORY_INSPECTOR_WIDTH);
        assert_eq!(y, 900.0 - 680.0);
    }

    #[test]
    fn inspector_never_starts_left_of_a_monitor_smaller_than_itself() {
        let monitor = WindowBox {
            x: -1920.0,
            y: 0.0,
            width: 400.0,
            height: 300.0,
        };

        assert_eq!(
            inspector_placement(
                monitor,
                monitor,
                (TRAJECTORY_INSPECTOR_WIDTH, TRAJECTORY_INSPECTOR_HEIGHT),
                0
            ),
            (-1920.0, 0.0)
        );
    }

    fn laptop_work_area() -> WindowBox {
        WindowBox {
            x: 0.0,
            y: 25.0,
            width: 1440.0,
            height: 800.0,
        }
    }

    #[test]
    fn a_wide_inspector_grows_away_from_the_nearer_screen_edge() {
        let right = WindowBox {
            x: 960.0,
            y: 80.0,
            width: TRAJECTORY_INSPECTOR_WIDTH,
            height: 680.0,
        };
        let (frame, narrow) = inspector_wide_frame(right, laptop_work_area());

        // Parked on the right, it grows leftward and keeps its right edge.
        assert_eq!(frame.width, TRAJECTORY_INSPECTOR_WIDE_WIDTH);
        assert_eq!(frame.x + frame.width, right.x + right.width);
        assert_eq!((frame.y, frame.height), (right.y, right.height));
        assert!(narrow.anchor_right);

        let left = WindowBox { x: 40.0, ..right };
        let (frame, narrow) = inspector_wide_frame(left, laptop_work_area());
        assert_eq!(frame.x, 40.0);
        assert!(!narrow.anchor_right);
    }

    #[test]
    fn a_wide_inspector_fits_a_small_display() {
        let work = WindowBox {
            x: 0.0,
            y: 25.0,
            width: 1024.0,
            height: 600.0,
        };
        let current = WindowBox {
            x: 560.0,
            y: 40.0,
            width: TRAJECTORY_INSPECTOR_WIDTH,
            height: 520.0,
        };

        let (frame, _) = inspector_wide_frame(current, work);

        assert_eq!(
            frame.width,
            1024.0 - 2.0 * TRAJECTORY_INSPECTOR_SCREEN_MARGIN
        );
        assert!(frame.x >= work.x);
        assert!(frame.x + frame.width <= work.x + work.width);
    }

    #[test]
    fn a_wide_inspector_never_shrinks_a_window_already_wider() {
        let current = WindowBox {
            x: 0.0,
            y: 25.0,
            width: 1300.0,
            height: 700.0,
        };

        let (frame, _) = inspector_wide_frame(current, laptop_work_area());

        assert_eq!(frame.width, 1300.0);
    }

    #[test]
    fn narrowing_returns_the_inspector_to_where_it_was() {
        let before = WindowBox {
            x: 960.0,
            y: 80.0,
            width: TRAJECTORY_INSPECTOR_WIDTH,
            height: 680.0,
        };
        let (wide, narrow) = inspector_wide_frame(before, laptop_work_area());
        // The operator dragged it down while it was wide.
        let moved = WindowBox { y: 120.0, ..wide };

        let after = inspector_narrow_frame(moved, laptop_work_area(), narrow);

        assert_eq!((after.x, after.width), (before.x, before.width));
        assert_eq!(after.y, 120.0);
    }

    #[test]
    fn narrowing_keeps_a_width_shrunk_by_hand() {
        let current = WindowBox {
            x: 100.0,
            y: 80.0,
            width: 380.0,
            height: 680.0,
        };
        let narrow = NarrowFrame {
            width: TRAJECTORY_INSPECTOR_WIDTH,
            anchor_right: false,
        };

        let after = inspector_narrow_frame(current, laptop_work_area(), narrow);

        assert_eq!(after.width, 380.0);
    }

    #[test]
    fn selections_route_to_the_newest_unpinned_window() {
        let mut registry = InspectorRegistry::default();
        let first = registry.insert();
        let second = registry.insert();

        assert_eq!(registry.target(), Some(second.as_str()));

        registry.set_pinned(&second, true);
        assert_eq!(registry.target(), Some(first.as_str()));

        registry.set_pinned(&first, true);
        // Every inspector is frozen, so the next click has to open a window.
        assert_eq!(registry.target(), None);
    }

    #[test]
    fn unpinning_does_not_promote_a_window_over_a_newer_one() {
        let mut registry = InspectorRegistry::default();
        let first = registry.insert();
        registry.set_pinned(&first, true);
        let second = registry.insert();

        registry.set_pinned(&first, false);

        // Both are unpinned now; the newest opened one keeps the selection.
        assert_eq!(registry.target(), Some(second.as_str()));
        assert_eq!(registry.unpinned_labels(), vec![first, second]);
    }

    #[test]
    fn labels_are_unique_after_a_window_is_destroyed() {
        let mut registry = InspectorRegistry::default();
        let first = registry.insert();
        registry.remove(&first);
        let second = registry.insert();

        assert_ne!(first, second);
        assert_eq!(registry.target(), Some(second.as_str()));
        assert!(second.starts_with(TRAJECTORY_INSPECTOR_LABEL_PREFIX));
    }

    #[test]
    fn a_destroyed_window_stops_being_a_target() {
        let mut registry = InspectorRegistry::default();
        let only = registry.insert();
        registry.remove(&only);

        assert_eq!(registry.target(), None);
        assert!(registry.unpinned_labels().is_empty());
    }

    #[test]
    fn leaving_a_conversation_cancels_inspectors_still_being_created() {
        let mut registry = InspectorRegistry::default();
        let pending = registry.insert();
        registry.store(&pending, serde_json::json!({"row": {"chip": "CLIENT"}}));

        assert_eq!(registry.remove_unpinned(), vec![pending.clone()]);
        assert!(registry.find_mut(&pending).is_none());
        assert_eq!(registry.target(), None);

        let next = registry.insert();
        // A cancelled build finishing later must not close or remove its replacement.
        registry.remove(&pending);
        assert_ne!(next, pending);
        assert_eq!(registry.target(), Some(next.as_str()));
    }

    #[test]
    fn leaving_a_conversation_preserves_pinned_inspectors_and_their_selection() {
        let mut registry = InspectorRegistry::default();
        let pinned = registry.insert();
        registry.store(&pinned, serde_json::json!({"row": {"chip": "UPSTREAM"}}));
        registry.set_pinned(&pinned, true);
        let following = registry.insert();

        assert_eq!(registry.remove_unpinned(), vec![following]);
        assert!(registry.remove_unpinned().is_empty());
        let state = registry.state(&pinned);
        assert!(state.pinned);
        assert_eq!(state.selection.unwrap()["row"]["chip"], "UPSTREAM");
    }

    #[test]
    fn an_inspector_mounts_with_the_latest_selection_received_during_creation() {
        let mut registry = InspectorRegistry::default();
        let pending = registry.insert();
        registry.store(&pending, serde_json::json!({"row": {"chip": "CLIENT"}}));

        let target = registry.target().unwrap().to_string();
        registry.store(&target, serde_json::json!({"row": {"chip": "RESULT"}}));

        assert_eq!(registry.entries.len(), 1);
        assert_eq!(
            registry.state(&pending).selection.unwrap()["row"]["chip"],
            "RESULT"
        );
    }

    #[test]
    fn a_pinned_window_keeps_replaying_the_phase_it_froze_on() {
        let mut registry = InspectorRegistry::default();
        let label = registry.insert();
        registry.store(&label, serde_json::json!({"row": {"chip": "UPSTREAM"}}));
        registry.set_pinned(&label, true);

        // A newer selection cannot reach it, so a reloaded webview pulls back
        // exactly what it was frozen on.
        registry.store("trajectory-inspector-404", serde_json::json!({"row": {}}));

        let state = registry.state(&label);
        assert!(state.pinned);
        assert_eq!(state.selection.unwrap()["row"]["chip"], "UPSTREAM");
    }

    #[test]
    fn an_unknown_window_reports_an_empty_state() {
        let registry = InspectorRegistry::default();

        let state = registry.state("trajectory-inspector-404");

        assert!(!state.pinned);
        assert!(state.selection.is_none());
    }

    #[test]
    fn inspector_title_marks_the_pinned_window() {
        assert_ne!(
            inspector_window_title(Locale::ZhCN, true),
            inspector_window_title(Locale::ZhCN, false)
        );
        assert!(inspector_window_title(Locale::En, true).contains("Pinned"));
    }

    #[test]
    fn platform_initialization_script_uses_a_quoted_literal() {
        assert_eq!(
            platform_initialization_script("macos"),
            "window.__ASTRLINK_DESKTOP_PLATFORM__ = \"macos\";"
        );
    }

    #[test]
    fn local_privacy_model_probe_command_validates_before_delegating() {
        let manager = CoreManager::new();
        let invalid = tauri::async_runtime::block_on(probe_local_privacy_model_with_manager(
            serde_json::json!({"path": "smb://ioncat.private/model-secret"}),
            &manager,
        ))
        .expect_err("URI input must be rejected before contacting Core");
        assert!(!invalid.contains("ioncat.private"));
        assert!(!invalid.contains("model-secret"));

        let path = std::env::current_dir()
            .expect("current directory")
            .to_string_lossy()
            .into_owned();
        let delegated = tauri::async_runtime::block_on(probe_local_privacy_model_with_manager(
            serde_json::json!({"path": path}),
            &manager,
        ))
        .expect_err("a valid input should reach the stopped Core manager");
        assert_eq!(delegated, "The gateway is not ready yet.");
    }
}
