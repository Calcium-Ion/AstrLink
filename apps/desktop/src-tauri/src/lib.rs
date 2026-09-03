mod agent_install;
mod control_session;
#[cfg(debug_assertions)]
mod dev_reload;
mod i18n;
mod preferences;
mod sidecar;

use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};

use i18n::Locale;
use preferences::{CloseBehavior, Preferences, PreferencesSnapshot, PreferencesStore};
use serde::{Deserialize, Serialize};
use sidecar::{
    CoreManager, CoreSnapshot, PolicyRecordResponse, RouteRecordResponse, ServiceRecordResponse,
};
use tauri::{
    menu::{Menu, MenuItem},
    tray::TrayIconBuilder,
    Manager, RunEvent, State, WindowEvent,
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
    inference_port: u16,
    max_concurrent_inspections: u16,
    response_start_timeout_seconds: u32,
    locale: Locale,
}

impl From<PreferencesInput> for Preferences {
    fn from(input: PreferencesInput) -> Self {
        Self {
            close_behavior: input.close_behavior,
            autostart: input.autostart,
            core_auto_start: input.core_auto_start,
            core_auto_recover: input.core_auto_recover,
            inference_port: input.inference_port,
            max_concurrent_inspections: input.max_concurrent_inspections,
            response_start_timeout_seconds: input.response_start_timeout_seconds,
            locale: input.locale,
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
        values.core_auto_recover,
        locale,
    );
    if let Err(error) = rebuild_tray_menu(&app, locale) {
        eprintln!("unable to rebuild AstrLink tray menu: {error}");
    }
    Ok(settings_snapshot(&app, store.inner()))
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

#[tauri::command]
async fn list_services(manager: State<'_, Arc<CoreManager>>) -> Result<serde_json::Value, String> {
    manager.list_services().await
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
        .invoke_handler(tauri::generate_handler![
            core_status,
            window_chrome_preferences,
            get_preferences,
            update_preferences,
            start_core,
            stop_core,
            restart_core,
            list_services,
            get_service,
            create_service,
            update_service,
            delete_service,
            get_service_usage,
            reset_service_usage,
            probe_service_models,
            probe_draft_service_models,
            begin_service_authorization,
            open_authorization_url,
            save_text_file,
            get_service_authorization,
            cancel_service_authorization,
            logout_service,
            list_routes,
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
            get_audit_settings,
            update_audit_settings,
            list_access_tokens,
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
            agent_debug_status,
            install_agent_debug,
            uninstall_agent_debug
        ])
        .setup(move |app| {
            let config_directory = app
                .path()
                .app_config_dir()
                .map_err(|error| format!("unable to resolve AstrLink config directory: {error}"))?;
            let preferences = Arc::new(PreferencesStore::load(&config_directory));
            let values = preferences.snapshot().values;
            setup_manager.configure(
                values.inference_port,
                values.max_concurrent_inspections,
                values.response_start_timeout_seconds,
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
            TrayIconBuilder::with_id("main")
                .icon(
                    app.default_window_icon()
                        .cloned()
                        .ok_or("AstrLink tray icon is unavailable")?,
                )
                .menu(&menu)
                .show_menu_on_left_click(false)
                .on_tray_icon_event(|tray, event| {
                    if matches!(event, tauri::tray::TrayIconEvent::Click { .. }) {
                        show_main_window(tray.app_handle());
                    }
                })
                .on_menu_event(move |app, event| match event.id().as_ref() {
                    "show" => show_main_window(app),
                    #[cfg(debug_assertions)]
                    "reload" => dev_reload::reload_main_window(app),
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
