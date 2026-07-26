mod sidecar;

use std::sync::Arc;

use serde::Serialize;
use sidecar::{CoreManager, CoreSnapshot, EndpointRecordResponse, PolicyRecordResponse};
use tauri::{Manager, RunEvent, State};

#[derive(Debug, Serialize)]
struct AppSnapshot {
    app_version: String,
    #[serde(flatten)]
    core: CoreSnapshot,
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
async fn list_endpoints(manager: State<'_, Arc<CoreManager>>) -> Result<serde_json::Value, String> {
    manager.list_endpoints().await
}

#[tauri::command]
async fn get_endpoint(
    endpoint_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<EndpointRecordResponse, String> {
    manager.get_endpoint(&endpoint_id).await
}

#[tauri::command]
async fn create_endpoint(
    input: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<EndpointRecordResponse, String> {
    manager.create_endpoint(input).await
}

#[tauri::command]
async fn update_endpoint(
    endpoint_id: String,
    etag: String,
    patch: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<EndpointRecordResponse, String> {
    manager.update_endpoint(&endpoint_id, &etag, patch).await
}

#[tauri::command]
async fn delete_endpoint(
    endpoint_id: String,
    etag: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<(), String> {
    manager.delete_endpoint(&endpoint_id, &etag).await
}

#[tauri::command]
async fn list_request_records(
    query: serde_json::Value,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.list_request_records(query).await
}

#[tauri::command]
async fn get_request_record(
    request_id: String,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<serde_json::Value, String> {
    manager.get_request_record(&request_id).await
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

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let manager = Arc::new(CoreManager::new());
    let setup_manager = Arc::clone(&manager);

    let app = tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .manage(manager)
        .invoke_handler(tauri::generate_handler![
            core_status,
            restart_core,
            list_endpoints,
            get_endpoint,
            create_endpoint,
            update_endpoint,
            delete_endpoint,
            list_request_records,
            get_request_record,
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
            get_privacy_model_catalog,
            probe_privacy_model,
            list_privacy_model_installations,
            install_privacy_model,
            get_privacy_model_installation,
            delete_privacy_model_installation
        ])
        .setup(move |app| {
            if let Err(error) = setup_manager.start(app.handle()) {
                eprintln!("failed to start astrlink-core: {error}");
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("failed to build AstrLink desktop app");

    app.run(|app_handle, event| {
        if let RunEvent::Exit = event {
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
            },
        };

        let value = serde_json::to_value(snapshot).expect("snapshot should serialize");
        assert_eq!(value["app_version"], "0.1.0");
        assert_eq!(value["phase"], "stopped");
        assert!(value.get("core").is_none());
    }
}
