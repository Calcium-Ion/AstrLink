//! DimAgent's provider rows, shared by its CLI and desktop UI. The stable
//! client uses separate columns; Rust builds keep a JSON record per provider.

mod store;
#[cfg(test)]
mod tests;

use std::path::{Path, PathBuf};

use serde_json::{json, Value};

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{Document, Format},
        models::{Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&DimAgent);
struct DimAgent;
struct Database;

const ROUTES: [(&str, &str, &str); 4] = [
    ("openai.chat", "local-chat", "openai-compatible"),
    ("openai.responses", "local-responses", "openai-responses"),
    ("anthropic.messages", "local-anthropic", "anthropic"),
    ("google.generate_content", "local-gemini", "gemini"),
];

fn owned(id: &str) -> bool {
    ROUTES.iter().any(|(_, name, _)| id == *name)
}

fn address(protocol: &str, origin: &str) -> String {
    format!(
        "{origin}/{}",
        if protocol == "google.generate_content" {
            "v1beta"
        } else {
            "v1"
        }
    )
}

fn value(config: &Config, key: &str) -> Option<Value> {
    serde_json::from_str(&config.get(key).flatten()?).ok()
}

impl ClientSpec for DimAgent {
    fn id(&self) -> &'static str {
        "dimagent"
    }
    fn name(&self) -> &'static str {
        "DimAgent"
    }
    fn protocols(&self) -> &'static [&'static str] {
        &[
            "openai.chat",
            "openai.responses",
            "anthropic.messages",
            "google.generate_content",
        ]
    }
    fn targets(&self) -> &'static [&'static str] {
        &[
            "standard",
            "rust",
            "alpha",
            "global_standard",
            "global_rust",
        ]
    }
    fn lists_models(&self) -> bool {
        true
    }
    fn dir(&self, home: Home, target: Option<&str>) -> PathBuf {
        let (base, subdir) = match target {
            Some("rust") => (".dimcode", "rust"),
            Some("alpha") => (".dimcode-alpha", "rust"),
            Some("global_standard") => (".dim", "v2"),
            Some("global_rust") => (".dim", "rust"),
            _ => (".dimcode", "v2"),
        };
        home.join(base).join(subdir)
    }
    fn detected(&self, home: Home, target: Option<&str>) -> bool {
        self.config_paths(home, target, None)[0].is_file()
    }
    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&Database]
    }
    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        let name = if target == Some("global_standard") {
            "dimglobal.sqlite"
        } else {
            "dimcode.sqlite"
        };
        vec![self.dir(home, target).join(name)]
    }
    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }
    fn holds_connection(&self, key: &str) -> bool {
        key.starts_with("providers.")
    }
    fn current_models(&self, config: &Config) -> CurrentModels {
        let selection = value(config, "selection");
        let mut models = Vec::new();
        for (_, id, _) in ROUTES {
            if let Some(provider) = value(config, &format!("providers.{id}")) {
                for model in provider["models"].as_array().into_iter().flatten() {
                    if let Some(id) = model["modelId"].as_str() {
                        if !models.iter().any(|known| known == id) {
                            models.push(id.to_string());
                        }
                    }
                }
            }
        }
        CurrentModels {
            model: selection
                .filter(|s| s["providerId"].as_str().is_some_and(owned))
                .and_then(|s| s["modelId"].as_str().map(str::to_string))
                .or_else(|| models.first().cloned()),
            models: (!models.is_empty()).then_some(models),
            ..Default::default()
        }
    }
    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let native = matches!(connection.target, Some("rust" | "alpha" | "global_rust"));
        let list = connection.list();
        let chosen = connection.model("model");
        let mut keys = Vec::new();
        for (protocol, id, driver) in ROUTES {
            let models: Vec<_> = list
                .iter()
                .filter(|m| connection.protocol(self, m) == protocol)
                .collect();
            if models.is_empty() {
                continue;
            }
            let entries: Vec<_> = models.iter().map(|model| {
                let detail = details.get(model.as_str());
                let reasoning = detail.and_then(|d| d.reasoning.as_ref());
                json!({
                    "modelId": model, "displayName": model, "status": "active",
                    "capabilities": {
                        "modalities": ["text"], "toolCalling": true, "streaming": true,
                        "structuredOutput": true, "vision": false, "audio": false,
                        "reasoning": reasoning.is_some(),
                        "contextWindow": detail.and_then(|d| d.context_window).unwrap_or(128_000),
                        "maxOutputTokens": detail.and_then(|d| d.max_tokens).unwrap_or(16_384)
                    }
                })
            }).collect();
            let mut provider = json!({
                "displayName": format!("AstrLink ({driver})"), "driverKind": driver,
                "baseUrl": address(protocol, connection.origin),
                "auth": { "type": "api_key", "headerName": "Authorization", "prefix": "Bearer " },
                "headers": { "User-Agent": "DimAgent" }, "models": entries, "enabled": true,
            });
            if native {
                provider["kind"] = "custom".into();
                provider["credential"] = connection.token.into();
            } else {
                provider["source"] = "custom".into();
                provider["credential"] = json!({ "type": "apiKey", "apiKey": connection.token });
                provider["modelsSource"] = "user".into();
                provider["activeModelId"] = chosen
                    .as_ref()
                    .filter(|m| models.contains(m))
                    .unwrap_or(models[0])
                    .clone()
                    .into();
            }
            keys.push((format!("providers.{id}"), provider.to_string()));
            if let Some(model) = chosen.as_ref().filter(|m| models.contains(m)) {
                keys.push((
                    "selection".into(),
                    json!({"providerId": id, "modelId": model}).to_string(),
                ));
            }
        }
        keys
    }
    fn base_url_key(&self) -> &'static str {
        ""
    }
    fn synced_keys(
        &self,
        config: &Config,
        record: &ClientRecord,
        origin: &str,
    ) -> Vec<(String, String)> {
        ROUTES
            .iter()
            .filter_map(|(protocol, id, _)| {
                let key = format!("providers.{id}");
                if !record.keys.contains_key(&key) {
                    return None;
                }
                let mut provider = value(config, &key)?;
                provider["baseUrl"] = address(protocol, origin).into();
                Some((key, provider.to_string()))
            })
            .collect()
    }
    fn notes(&self, _: Home, _: Option<&str>, _: Option<&Config>) -> Vec<&'static str> {
        vec!["restart"]
    }
}

impl ConfigFile for Database {
    fn preview(&self, text: &str) -> String {
        let Ok(mut value) = serde_json::from_str::<Value>(text) else {
            return String::new();
        };
        if let Some(providers) = value["providers"].as_object_mut() {
            for provider in providers.values_mut() {
                // Custom headers and proxy URLs may carry credentials under
                // names that the general preview cannot recognize.
                if let Some(headers) = provider["headers"].as_object_mut() {
                    for value in headers.values_mut() {
                        *value = "…".into();
                    }
                }
                for key in ["proxy", "network", "accountUsageSettings", "metadata"] {
                    if provider.get(key).is_some() {
                        provider[key] = "…".into();
                    }
                }
            }
        }
        serde_json::to_string_pretty(&value).unwrap_or_default()
    }
    fn format(&self) -> Format {
        Format::Json {
            syntax: None,
            objects: &["providers"],
            root_list: false,
        }
    }
    fn read(&self, path: &Path) -> Result<Option<String>, String> {
        store::read(path)
    }
    fn write(
        &self,
        path: &Path,
        before: Option<&str>,
        after: Option<&str>,
        _: bool,
    ) -> Result<(), String> {
        store::write(path, before, after)
    }
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Json(json) = document else {
            return None;
        };
        json.get_json(key).map(|v| v.map(|v| v.to_string()))
    }
    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Json(json) = document else {
            return Err("expected provider settings".into());
        };
        let value: Value = serde_json::from_str(value).map_err(|_| "invalid provider settings")?;
        json.set_json(key, &value, |_| usize::MAX)
    }
    fn emptied(&self) -> &'static [&'static str] {
        &["providers"]
    }
}
