//! The plugin confirmation window (plugin platform v2, §2.4). A change that
//! grants a plugin more takes effect only from this small window: enabling
//! it, capabilities it did not have, addresses it could not reach, plain
//! HTTP, the privacy check skipped, refusing requests it cannot decide, and
//! new code: an install from source, an update, a rollback.
//! The main window's change stops at Core with `approval_required`; the host
//! keeps it here and opens this window without taking focus; only a decision
//! made in this window sends it again, confirmed. Plugin interface code runs
//! in the main window, which can neither decide here nor click for the user.

use std::sync::{Arc, Mutex};

use serde::Serialize;
use tauri::{AppHandle, Emitter, Manager, WebviewUrl, WebviewWindowBuilder};

use crate::{i18n, preferences::PreferencesStore};

/// Must match `PLUGIN_APPROVAL_LABEL` in the frontend.
pub const LABEL: &str = "plugin-approval";
/// What the main window hears once the user decided.
pub const FINISHED_EVENT: &str = "plugin-approval-finished";
const WIDTH: f64 = 460.0;
const HEIGHT: f64 = 600.0;
const MIN_WIDTH: f64 = 380.0;
const MIN_HEIGHT: f64 = 420.0;

/// What a confirmed change sends again.
#[derive(Clone, Debug)]
pub enum PendingChange {
    /// The plugin's settings.
    Settings(serde_json::Value),
    /// A plugin page action, `source_install` or `source_rollback`, with
    /// its input.
    Action {
        action: String,
        input: serde_json::Value,
    },
}

/// A change waiting for the user: the plugin, what Core said it grants,
/// and the change to send again once confirmed.
#[derive(Clone, Debug, Serialize)]
pub struct PendingApproval {
    pub id: String,
    pub key: String,
    /// Core's description of what the change grants.
    pub approval: serde_json::Value,
    #[serde(skip)]
    pub change: PendingChange,
}

/// The one change waiting for confirmation; a newer one replaces it.
#[derive(Default)]
pub struct Approvals {
    pending: Mutex<Option<PendingApproval>>,
}

impl Approvals {
    pub fn hold(&self, key: String, approval: serde_json::Value, change: PendingChange) -> String {
        let id = random_id();
        let mut pending = self
            .pending
            .lock()
            .unwrap_or_else(|error| error.into_inner());
        *pending = Some(PendingApproval {
            id: id.clone(),
            key,
            approval,
            change,
        });
        id
    }

    pub fn current(&self) -> Option<PendingApproval> {
        self.pending
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .clone()
    }

    /// Takes the change `id` names, so it is decided once.
    pub fn take(&self, id: &str) -> Option<PendingApproval> {
        let mut pending = self
            .pending
            .lock()
            .unwrap_or_else(|error| error.into_inner());
        if pending.as_ref().is_some_and(|held| held.id == id) {
            pending.take()
        } else {
            None
        }
    }
}

fn random_id() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_nanos())
        .unwrap_or_default();
    format!("approval-{nanos:x}")
}

/// What the main window hears once a change was decided.
#[derive(Clone, Debug, Serialize)]
pub struct Finished {
    pub id: String,
    pub key: String,
    pub approved: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

pub fn announce(app: &AppHandle, finished: Finished) {
    if let Err(error) = app.emit(FINISHED_EVENT, finished) {
        eprintln!("unable to announce a plugin confirmation: {error}");
    }
}

/// Keeps only the capabilities the user left ticked of those the change
/// adds: `granted_capabilities` loses the others.
pub fn narrow_grants(input: &mut serde_json::Value, added: &[String], kept: &[String]) {
    let Some(grants) = input
        .get_mut("granted_capabilities")
        .and_then(serde_json::Value::as_array_mut)
    else {
        return;
    };
    grants.retain(|grant| {
        grant.as_str().is_some_and(|name| {
            !added.iter().any(|added| added == name) || kept.iter().any(|kept| kept == name)
        })
    });
}

/// Opens the confirmation window. A window opened for a new change does not
/// take focus; one the user asks for does.
pub fn open(app: &AppHandle, focus: bool) -> Result<(), String> {
    if let Some(window) = app.get_webview_window(LABEL) {
        let _ = window.emit("plugin-approval-changed", ());
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
            "host.window.pluginApproval",
            &[],
        ))
        .background_color(crate::theme_background(theme))
        .inner_size(WIDTH, HEIGHT)
        .min_inner_size(MIN_WIDTH, MIN_HEIGHT)
        .resizable(true)
        .shadow(true)
        .focused(focus)
        .always_on_top(true)
        .center();
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

/// The error the main window gets for a change that waits in the window.
pub fn pending_error() -> String {
    r#"{"error":{"code":"approval_pending","message":"the change waits for the user's confirmation in the plugin confirmation window"}}"#.to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_change_is_decided_once() {
        let approvals = Approvals::default();
        let id = approvals.hold(
            "gate".into(),
            serde_json::json!({ "enable": true }),
            PendingChange::Settings(serde_json::json!({ "enabled": true })),
        );
        assert_eq!(
            approvals.current().map(|held| held.key),
            Some("gate".to_string())
        );
        assert!(approvals.take("other").is_none());
        assert!(approvals.take(&id).is_some());
        assert!(approvals.take(&id).is_none());
    }

    #[test]
    fn unticked_capabilities_are_left_out() {
        let mut input = serde_json::json!({
            "enabled": true,
            "granted_capabilities": ["storage", "network", "ui.code"]
        });
        narrow_grants(
            &mut input,
            &["network".to_string(), "ui.code".to_string()],
            &["network".to_string()],
        );
        assert_eq!(
            input["granted_capabilities"],
            serde_json::json!(["storage", "network"])
        );
    }
}
