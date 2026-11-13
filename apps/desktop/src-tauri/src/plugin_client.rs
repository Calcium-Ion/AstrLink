//! The plugin interface protocol (plugin platform v2, §6). The main window
//! loads a plugin's interface code from
//! `astrlink-plugin://localhost/<key>/client.js`, which this host reads from
//! Core with its control token: Core serves it only while the plugin is
//! enabled with `ui.code` granted and its interface is on. The window's CSP
//! allows scripts from this scheme and nothing from the network, and only
//! the main window may load from it.

use std::sync::Arc;

use tauri::{http, Manager, Runtime, UriSchemeContext, UriSchemeResponder};

use crate::sidecar::CoreManager;

/// Must match `PLUGIN_SCHEME` in the frontend.
pub const SCHEME: &str = "astrlink-plugin";

/// The files a request may name: the code and its source map.
fn requested(path: &str) -> Option<(&str, &'static str)> {
    let (key, file) = path.trim_start_matches('/').split_once('/')?;
    let file = match file {
        "client.js" => "client.js",
        "client.js.map" => "client.js.map",
        _ => return None,
    };
    let mut chars = key.chars();
    let valid = matches!(chars.next(), Some('a'..='z'))
        && key.len() <= 32
        && chars.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-');
    valid.then_some((key, file))
}

fn respond(responder: UriSchemeResponder, status: http::StatusCode, kind: &str, body: Vec<u8>) {
    let response = http::Response::builder()
        .status(status)
        .header(http::header::CONTENT_TYPE, kind)
        .header(http::header::CACHE_CONTROL, "no-store")
        .header("X-Content-Type-Options", "nosniff")
        .body(body)
        .unwrap_or_else(|_| http::Response::new(Vec::new()));
    responder.respond(response);
}

pub fn handle<R: Runtime>(
    context: UriSchemeContext<'_, R>,
    request: http::Request<Vec<u8>>,
    responder: UriSchemeResponder,
) {
    if context.webview_label() != "main" || request.method() != http::Method::GET {
        return respond(
            responder,
            http::StatusCode::FORBIDDEN,
            "text/plain",
            Vec::new(),
        );
    }
    let Some((key, file)) = requested(request.uri().path()) else {
        return respond(
            responder,
            http::StatusCode::NOT_FOUND,
            "text/plain",
            Vec::new(),
        );
    };
    let Some(manager) = context
        .app_handle()
        .try_state::<Arc<CoreManager>>()
        .map(|state| Arc::clone(state.inner()))
    else {
        return respond(
            responder,
            http::StatusCode::SERVICE_UNAVAILABLE,
            "text/plain",
            Vec::new(),
        );
    };
    let key = key.to_string();
    tauri::async_runtime::spawn(async move {
        match manager.plugin_client(&key, file).await {
            Ok(body) => {
                let kind = if file == "client.js" {
                    "text/javascript; charset=utf-8"
                } else {
                    "application/json"
                };
                respond(responder, http::StatusCode::OK, kind, body)
            }
            Err(status) => respond(responder, status, "text/plain", Vec::new()),
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_a_plugins_code_and_map_are_served() {
        assert_eq!(requested("/notes/client.js"), Some(("notes", "client.js")));
        assert_eq!(
            requested("/notes/client.js.map"),
            Some(("notes", "client.js.map"))
        );
        assert_eq!(requested("/notes/plugin.js"), None);
        assert_eq!(requested("/../client.js"), None);
        assert_eq!(requested("/Notes/client.js"), None);
        assert_eq!(requested("/notes"), None);
    }
}
