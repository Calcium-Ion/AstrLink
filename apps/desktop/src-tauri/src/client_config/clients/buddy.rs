//! The `models.json` that WorkBuddy and CodeBuddy, Tencent's agents, read
//! their own models from: entries that each carry an address and key, in
//! `{"models": [...], "availableModels": [...]}`, or the entry list alone
//! once WorkBuddy's model settings saved the file.
//!
//! An entry is one key, `models[<id>]`, holding the entry as compact JSON;
//! an allowlist place is `availableModels[<id>]`.

use super::ConfigFile;
use crate::client_config::{
    config::Config,
    format::{Document, Format},
    models::{Catalog, CurrentModels, ModelDetails},
    plan::Connection,
};

/// The group the entries show under in the clients' model pickers. Never
/// part of a model request.
const VENDOR: &str = "AstrLink";
/// The reasoning efforts an entry may list, in the clients' order.
const EFFORTS: [&str; 6] = ["minimal", "low", "medium", "high", "xhigh", "max"];
const ENTRIES: &str = "models";
const ALLOWED: &str = "availableModels";

pub(crate) fn entry_key(id: &str) -> String {
    format!("{ENTRIES}[{id}]")
}

fn allowed_key(id: &str) -> String {
    format!("{ALLOWED}[{id}]")
}

fn is_entry_key(key: &str) -> bool {
    key.starts_with(ENTRIES) && key[ENTRIES.len()..].starts_with('[')
}

fn is_allowed_key(key: &str) -> bool {
    key.starts_with(ALLOWED) && key[ALLOWED.len()..].starts_with('[')
}

/// Whether `key` is a key of `models.json`.
pub(crate) fn holds(key: &str) -> bool {
    is_entry_key(key) || is_allowed_key(key)
}

/// Whether `key` is an entry, which carries its own address and key.
pub(crate) fn holds_connection(key: &str) -> bool {
    is_entry_key(key)
}

/// How a property of an entry AstrLink writes is placed among its siblings.
fn entry_order(name: &str) -> usize {
    const ORDER: [&str; 10] = [
        "id",
        "name",
        "vendor",
        "url",
        "api",
        "apiKey",
        "maxInputTokens",
        "maxOutputTokens",
        "supportsReasoning",
        "reasoning",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

/// The entry for model `id` posting to `url`; `api` names a protocol other
/// than Chat. The name is the id: the clients put it into the system prompt
/// they send upstream.
fn entry(
    id: &str,
    url: &str,
    api: Option<&str>,
    token: &str,
    details: Option<&ModelDetails>,
) -> String {
    let mut entry = serde_json::json!({
        "id": id,
        "name": id,
        "vendor": VENDOR,
        "url": url,
        "apiKey": token,
    });
    if let Some(api) = api {
        entry["api"] = api.into();
    }
    if let Some(details) = details {
        if let Some(context) = details.context_window {
            entry["maxInputTokens"] = context.into();
        }
        if let Some(output) = details.max_tokens {
            entry["maxOutputTokens"] = output.into();
        }
        if let Some(levels) = &details.reasoning {
            entry["supportsReasoning"] = true.into();
            let efforts: Vec<&str> = EFFORTS
                .into_iter()
                .filter(|effort| levels.iter().any(|level| level == effort))
                .collect();
            if !efforts.is_empty() {
                entry["reasoning"] = serde_json::json!({ "supportedEfforts": efforts });
            }
        }
    }
    entry.to_string()
}

/// The entry and allowlist place of every listed model. `route` gives each
/// model's address and `api` value from its protocol.
pub(crate) fn model_keys(
    connection: &Connection,
    details: &Catalog,
    route: impl Fn(&str) -> (String, Option<&'static str>),
) -> Vec<(String, String)> {
    let mut keys = Vec::new();
    for id in connection.list() {
        let (url, api) = route(&id);
        keys.push((
            entry_key(&id),
            entry(&id, &url, api, connection.token, details.get(&id)),
        ));
        keys.push((allowed_key(&id), id));
    }
    keys
}

/// The entry at `key` with its address pointed elsewhere by `route`, which
/// takes the entry's `api`.
pub(crate) fn rerouted(
    config: &Config,
    key: &str,
    route: impl Fn(Option<&str>) -> String,
) -> Option<String> {
    let mut entry: serde_json::Value = serde_json::from_str(&config.get(key).flatten()?).ok()?;
    let api = entry.get("api").and_then(serde_json::Value::as_str);
    entry["url"] = route(api).into();
    Some(entry.to_string())
}

/// The models AstrLink's entries hold now, in the file's order.
pub(crate) fn current_models(config: &Config, file: usize) -> CurrentModels {
    let Some(Document::Json(json)) = config.documents.get(file) else {
        return CurrentModels::default();
    };
    let list = match json.value() {
        Some(serde_json::Value::Array(list)) => list,
        Some(serde_json::Value::Object(mut root)) => match root.remove(ENTRIES) {
            Some(serde_json::Value::Array(list)) => list,
            _ => Vec::new(),
        },
        _ => Vec::new(),
    };
    let ids: Vec<String> = list
        .iter()
        .filter(|entry| entry.get("vendor").and_then(serde_json::Value::as_str) == Some(VENDOR))
        .filter_map(|entry| entry.get("id").and_then(serde_json::Value::as_str))
        .filter_map(|id| crate::client_config::models::clean_model(id.into()))
        .collect();
    CurrentModels {
        model: ids.first().cloned(),
        models: (!ids.is_empty()).then_some(ids),
        ..Default::default()
    }
}

/// `models.json`, strict JSON: the clients drop the whole file over a
/// comment or trailing comma.
pub(crate) struct ModelsFile;

impl ModelsFile {
    /// `key` as the file is shaped: a root list holds the entries itself
    /// and has no allowlist.
    fn path(document: &Document, key: &str) -> Option<String> {
        match document {
            Document::Json(json) if json.is_list() => {
                is_entry_key(key).then(|| key[ENTRIES.len()..].to_string())
            }
            _ => Some(key.to_string()),
        }
    }

    /// Whether the file has an allowlist that leaves models out: an empty
    /// one allows every model, and a new one would hide the built-in ones.
    fn restricts(document: &Document) -> bool {
        match document {
            Document::Json(json) => json
                .get_json(ALLOWED)
                .flatten()
                .is_some_and(|list| list.as_array().is_some_and(|list| !list.is_empty())),
            _ => false,
        }
    }
}

impl ConfigFile for ModelsFile {
    fn format(&self) -> Format {
        Format::Json {
            syntax: None,
            objects: &[],
            root_list: true,
        }
    }

    fn holds(&self, key: &str) -> bool {
        holds(key)
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        document.get(&Self::path(document, key)?)
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        if is_allowed_key(key) && !Self::restricts(document) {
            return Ok(());
        }
        let Some(path) = Self::path(document, key) else {
            return Ok(());
        };
        match document {
            Document::Json(json) if is_entry_key(key) => {
                let entry = serde_json::from_str(value).map_err(|error| error.to_string())?;
                json.set_json(&path, &entry, entry_order)
            }
            _ => document.set(&path, value),
        }
    }

    fn remove(&self, document: &mut Document, key: &str) {
        if let Some(path) = Self::path(document, key) {
            document.remove(&path);
        }
    }

    /// An entry list a removal emptied goes with the entries.
    fn tidy(&self, document: &mut Document) {
        if let Document::Json(json) = document {
            if json
                .get_json(ENTRIES)
                .flatten()
                .is_some_and(|list| list.as_array().is_some_and(Vec::is_empty))
            {
                json.remove(ENTRIES);
            }
        }
    }
}
