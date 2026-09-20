mod agent_install;
mod control_session;
#[cfg(debug_assertions)]
mod dev_reload;
mod failure_policy;
mod i18n;
mod preferences;
mod recovery_path;
mod sidecar;
mod startup_window;

use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex,
};

use i18n::Locale;
use preferences::{
    CloseBehavior, Preferences, PreferencesSnapshot, PreferencesStore, ThemePreference,
};
use serde::{Deserialize, Serialize};
use sidecar::{
    CoreManager, CoreSnapshot, PolicyRecordResponse, RouteRecordResponse, ServiceRecordResponse,
};
use tauri::{
    menu::{Menu, MenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    Emitter, Manager, RunEvent, State, WebviewUrl, WebviewWindowBuilder, WindowEvent,
};
use tauri_plugin_autostart::ManagerExt;
use tauri_plugin_dialog::DialogExt;

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
    locale: Locale,
    theme: ThemePreference,
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
            locale: input.locale,
            theme: input.theme,
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
    let manager = Arc::clone(manager.inner());
    manager.restart(&app).await?;
    Ok(AppSnapshot::capture(&app, &manager))
}

#[tauri::command]
fn start_core(
    app: tauri::AppHandle,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<AppSnapshot, String> {
    let manager = Arc::clone(manager.inner());
    manager.start(&app)?;
    Ok(AppSnapshot::capture(&app, &manager))
}

#[tauri::command]
async fn stop_core(
    app: tauri::AppHandle,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<AppSnapshot, String> {
    let manager = Arc::clone(manager.inner());
    manager.stop_and_wait().await?;
    Ok(AppSnapshot::capture(&app, &manager))
}

fn settings_snapshot(app: &tauri::AppHandle, store: &PreferencesStore) -> SettingsSnapshot {
    match app.autolaunch().is_enabled() {
        Ok(actual) => SettingsSnapshot {
            preferences: store.snapshot(),
            autostart_actual: Some(actual),
            autostart_error: None,
        },
        Err(error) => SettingsSnapshot {
            preferences: store.snapshot(),
            autostart_actual: None,
            autostart_error: Some(i18n::t(
                store.snapshot().values.locale,
                "host.autostart.readFailed",
                &[("error", &error.to_string())],
            )),
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

fn agent_install_context() -> Result<agent_install::InstallContext, String> {
    Ok(agent_install::InstallContext {
        home: control_session::user_home()?,
        mcp_source: agent_install::resolve_sidecar_binary("astrlink-mcp")?,
    })
}

#[tauri::command]
fn agent_debug_status() -> Result<agent_install::AgentInstallStatus, String> {
    let home = control_session::user_home()?;
    let mcp_source = agent_install::resolve_sidecar_binary("astrlink-mcp").unwrap_or_default();
    Ok(agent_install::status(&agent_install::InstallContext {
        home,
        mcp_source,
    }))
}

#[tauri::command]
fn install_agent_debug() -> Result<agent_install::InstallReceipt, String> {
    agent_install::install(&agent_install_context()?)
}

#[tauri::command]
fn uninstall_agent_debug() -> Result<(), String> {
    agent_install::uninstall(&agent_install_context()?)
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
    manager.configure(
        values.inference_port,
        values.max_concurrent_inspections,
        values.response_start_timeout_seconds,
        values.use_system_proxy,
        values.core_auto_recover,
        locale,
    );
    if let Err(error) = rebuild_tray_menu(&app, locale) {
        eprintln!("unable to rebuild AstrLink tray menu: {error}");
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
        let theme = preference.native_theme().or_else(|| window.theme().ok());
        if let Some(theme) = theme {
            if let Err(error) = window.set_background_color(Some(theme_background(theme))) {
                eprintln!("unable to update AstrLink window background: {error}");
            }
        }
    }
}

fn tray_menu(app: &tauri::AppHandle, locale: Locale) -> tauri::Result<Menu<tauri::Wry>> {
    let show = MenuItem::with_id(
        app,
        "show",
        i18n::t(locale, "host.tray.show", &[]),
        true,
        None::<&str>,
    )?;
    let quit = MenuItem::with_id(
        app,
        "quit",
        i18n::t(locale, "host.tray.quit", &[]),
        true,
        None::<&str>,
    )?;
    #[cfg(debug_assertions)]
    let reload = MenuItem::with_id(
        app,
        "reload",
        i18n::t(locale, "host.tray.reload", &[]),
        true,
        None::<&str>,
    )?;
    #[cfg(debug_assertions)]
    let menu = Menu::with_items(app, &[&show, &reload, &quit])?;
    #[cfg(not(debug_assertions))]
    let menu = Menu::with_items(app, &[&show, &quit])?;
    Ok(menu)
}

fn rebuild_tray_menu(app: &tauri::AppHandle, locale: Locale) -> Result<(), String> {
    let menu = tray_menu(app, locale).map_err(|error| error.to_string())?;
    if let Some(tray) = app.tray_by_id("main") {
        tray.set_menu(Some(menu))
            .map_err(|error| error.to_string())?;
    }
    Ok(())
}

fn show_main_window(app: &tauri::AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
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

/// One inspector window. `selection` is the phase it currently shows, kept here
/// rather than only in its React state so a window repopulates itself after the
/// dev host reloads every webview.
struct InspectorEntry {
    label: String,
    pinned: bool,
    selection: Option<serde_json::Value>,
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
}

/// What an inspector window pulls on mount, instead of waiting for the main
/// window to notice it exists.
#[derive(Debug, Default, Serialize)]
struct InspectorWindowState {
    selection: Option<serde_json::Value>,
    pinned: bool,
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

fn inspector_anchor(main: &tauri::WebviewWindow, cascade: u32) -> Option<(f64, f64)> {
    let scale = main.scale_factor().ok()?;
    let position = main.outer_position().ok()?.to_logical::<f64>(scale);
    let size = main.outer_size().ok()?.to_logical::<f64>(scale);
    let monitor = main.current_monitor().ok()??;
    let monitor_scale = monitor.scale_factor();
    let monitor_position = monitor.position().to_logical::<f64>(monitor_scale);
    let monitor_size = monitor.size().to_logical::<f64>(monitor_scale);
    Some(inspector_placement(
        WindowBox {
            x: position.x,
            y: position.y,
            width: size.width,
            height: size.height,
        },
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
fn show_trajectory_inspector(
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
        if let Err(error) = build_inspector_window(&app, &label, cascade) {
            let mut guard = inspector_registry(&registry)?;
            guard.remove(&label);
            return Err(error);
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

/// Closes the unpinned inspectors and leaves the pinned ones alone: pinning is
/// the operator saying they want to keep that phase on screen.
fn close_unpinned_inspectors(app: &tauri::AppHandle) {
    let Some(registry) = app.try_state::<Mutex<InspectorRegistry>>() else {
        return;
    };
    let labels = match registry.lock() {
        Ok(guard) => guard.unpinned_labels(),
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
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_service_usage(&service_id).await
}

#[tauri::command]
async fn reset_service_usage(
    service_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.reset_service_usage(&service_id).await
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
async fn recovery_paths(
    operation: String,
    id: Option<String>,
    etag: Option<String>,
    input: Option<serde_json::Value>,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.recovery_paths(&operation, id, etag, input).await
}

#[tauri::command]
async fn list_routes(manager: State<'_, Arc<CoreManager>>) -> Result<serde_json::Value, String> {
    manager.list_routes().await
}

#[tauri::command]
async fn get_route(
    route_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<RouteRecordResponse, String> {
    manager.get_route(&route_id).await
}

#[tauri::command]
async fn create_route(
    input: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<RouteRecordResponse, String> {
    manager.create_route(input).await
}

#[tauri::command]
async fn update_route(
    route_id: String,
    etag: String,
    patch: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<RouteRecordResponse, String> {
    manager.update_route(&route_id, &etag, patch).await
}

#[tauri::command]
async fn delete_route(
    route_id: String,
    etag: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<(), String> {
    manager.delete_route(&route_id, &etag).await
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
async fn get_audit_settings(
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_audit_settings().await
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

#[tauri::command]
async fn reveal_access_token(
    token_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.reveal_access_token(&token_id).await
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
    let manager = Arc::new(CoreManager::new());
    let setup_manager = Arc::clone(&manager);
    let explicit_quit = Arc::new(AtomicBool::new(false));
    let quit_state = Arc::clone(&explicit_quit);

    let app = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            show_main_window(app);
        }))
        .plugin(tauri_plugin_autostart::init(
            tauri_plugin_autostart::MacosLauncher::LaunchAgent,
            None,
        ))
        .append_invoke_initialization_script(platform_initialization_script(std::env::consts::OS))
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(manager)
        .manage(explicit_quit)
        .manage(Mutex::new(InspectorRegistry::default()))
        .invoke_handler(tauri::generate_handler![
            core_status,
            window_chrome_preferences,
            get_preferences,
            update_preferences,
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
            pricing,
            reset_service_usage,
            probe_service_models,
            probe_draft_service_models,
            begin_service_authorization,
            complete_service_authorization,
            open_authorization_url,
            open_external_url,
            save_text_file,
            get_service_authorization,
            cancel_service_authorization,
            logout_service,
            list_routes,
            recovery_paths,
            get_route,
            create_route,
            update_route,
            delete_route,
            list_request_records,
            list_request_sessions,
            get_request_session,
            get_request_record,
            list_request_record_children,
            delete_request_record,
            purge_request_records,
            get_request_audit_content,
            get_routing_settings,
            update_routing_settings,
            get_audit_settings,
            update_audit_settings,
            list_access_tokens,
            list_access_token_usage,
            get_usage_summary,
            create_access_token,
            reveal_access_token,
            delete_access_token,
            list_privacy_policies,
            get_privacy_policy,
            update_privacy_policy,
            dry_run_privacy_policy,
            get_privacy_regex_builtin_rules,
            get_privacy_model_catalog,
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
            close_trajectory_inspectors
        ])
        .setup(move |app| {
            let config_directory = app
                .path()
                .app_config_dir()
                .map_err(|error| format!("unable to resolve AstrLink config directory: {error}"))?;
            let preferences = Arc::new(PreferencesStore::load(&config_directory));
            let values = preferences.snapshot().values;
            apply_native_theme(app.handle(), values.theme);
            if let Some(window) = app.get_webview_window("main") {
                if let Err(error) = startup_window::fit_to_monitor(&window) {
                    eprintln!("failed to size AstrLink for the current display: {error}");
                }
                window.show()?;
            }
            setup_manager.configure(
                values.inference_port,
                values.max_concurrent_inspections,
                values.response_start_timeout_seconds,
                values.use_system_proxy,
                values.core_auto_recover,
                values.locale,
            );
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
            app.manage(preferences);

            let menu = tray_menu(app.handle(), values.locale)?;
            #[cfg(target_os = "macos")]
            let tray_icon = tauri::include_image!("icons/tray/36x36.png");
            #[cfg(not(target_os = "macos"))]
            let tray_icon = app
                .default_window_icon()
                .cloned()
                .ok_or("AstrLink tray icon is unavailable")?;
            TrayIconBuilder::with_id("main")
                .icon(tray_icon)
                .icon_as_template(cfg!(target_os = "macos"))
                .menu(&menu)
                .show_menu_on_left_click(false)
                .on_tray_icon_event(|tray, event| {
                    // Right-click belongs to the native tray menu. Focusing the
                    // main window here dismisses that menu on Windows.
                    if matches!(
                        event,
                        TrayIconEvent::Click {
                            button: MouseButton::Left,
                            button_state: MouseButtonState::Up,
                            ..
                        }
                    ) {
                        show_main_window(tray.app_handle());
                    }
                })
                .on_menu_event(move |app, event| match event.id().as_ref() {
                    "show" => show_main_window(app),
                    #[cfg(debug_assertions)]
                    "reload" => {
                        dev_reload::reload_windows(app);
                    }
                    "quit" => {
                        quit_state.store(true, Ordering::SeqCst);
                        app.exit(0);
                    }
                    _ => {}
                })
                .build(app)?;

            #[cfg(debug_assertions)]
            dev_reload::start(app.handle());

            if let Ok(home) = control_session::user_home() {
                if let Err(error) = agent_install::sync_installed_skills(&home) {
                    eprintln!("failed to sync AstrLink agent skills: {error}");
                }
                if let Ok(mcp_source) = agent_install::resolve_sidecar_binary("astrlink-mcp") {
                    if let Err(error) =
                        agent_install::sync_installed_mcp(&agent_install::InstallContext {
                            home,
                            mcp_source,
                        })
                    {
                        eprintln!("failed to sync AstrLink MCP binary: {error}");
                    }
                }
            }

            if values.core_auto_start {
                if let Err(error) = setup_manager.start(app.handle()) {
                    eprintln!("failed to start astrlink-core: {error}");
                }
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
            if let Some(window) = app_handle.get_webview_window(label) {
                if let Err(error) = window.set_background_color(Some(theme_background(*theme))) {
                    eprintln!("unable to follow AstrLink window theme: {error}");
                }
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
                    if let Some(window) = app_handle.get_webview_window("main") {
                        let _ = window.hide();
                    }
                    // A tray-parked app must not leave a following inspector on
                    // screen showing a request the operator can no longer
                    // reach. Pinned ones were kept on purpose and stay.
                    close_unpinned_inspectors(app_handle);
                } else {
                    app_handle
                        .state::<Arc<AtomicBool>>()
                        .store(true, Ordering::SeqCst);
                }
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
