//! ZCode, desktop app and bundled CLI alike, reads
//! `~/.zcode/v2/provider_config.json`. A provider rule has one API type, so
//! AstrLink adds one per protocol its models use, a model rule for each
//! model, and picks the default in `defaultModelSelection`. ZCode checks the
//! file strictly and reads one that fails as empty. It tells the model
//! `<provider id>/<model>`.

use std::path::PathBuf;

use serde_json::Value as Json;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::{Config, FileError},
        format::{json, Document, Format},
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&ZCode);

struct ZCode;

/// `provider_config.json`.
struct ProviderConfig;

/// Protocol, provider id, the name ZCode shows, and `api.type`.
const ROUTES: [(&str, &str, &str, &str); 3] = [
    (
        "openai.chat",
        "local-chat",
        "AstrLink",
        "openai-chat-completions",
    ),
    (
        "openai.responses",
        "local-responses",
        "AstrLink Responses",
        "openai-responses",
    ),
    (
        "anthropic.messages",
        "local-anthropic",
        "AstrLink Anthropic",
        "anthropic-messages",
    ),
];
const PROVIDERS_KEY: &str = "config.providerConfigRules.providerRules";
const MODELS_KEY: &str = "config.modelConfigRules.providerModelRules";
const ORDER_KEY: &str = "config.providerOrder";
const DEFAULT_KEY: &str = "config.defaultModelSelection";
/// What ZCode itself starts the file with.
const EMPTY: &str = r#"{
  "schemaVersion": 1,
  "config": {
    "providerOrder": [],
    "providerConfigRules": {
      "providerRules": []
    },
    "modelConfigRules": {
      "providerModelRules": [],
      "manualProviderModelRules": []
    }
  }
}
"#;

fn ours(id: &str) -> bool {
    ROUTES.iter().any(|(_, known, ..)| *known == id)
}

fn owned(entry: &Json) -> bool {
    entry
        .get("providerId")
        .and_then(Json::as_str)
        .is_some_and(ours)
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 23] = [
        "providerId",
        "modelId",
        "templateId",
        "providerName",
        "enabled",
        "config",
        "group",
        "access",
        "type",
        "apiKey",
        "api",
        "baseUrl",
        "headers",
        "personalModelIds",
        "modelOrder",
        "visibility",
        "properties",
        "contextWindow",
        "supportsToolCall",
        "optionSpecs",
        "max",
        "options",
        "reasoningLevel",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn entries(config: &Config, key: &str) -> Vec<Json> {
    config
        .get(key)
        .flatten()
        .and_then(|value| serde_json::from_str::<Json>(&value).ok())
        .and_then(|value| value.as_array().cloned())
        .unwrap_or_default()
}

/// AstrLink's provider ids among `ids`, in its own order.
fn ours_in<'i>(ids: impl Iterator<Item = &'i str>) -> Vec<&'static str> {
    let ids: Vec<&str> = ids.collect();
    ROUTES
        .iter()
        .map(|(_, id, ..)| *id)
        .filter(|id| ids.contains(id))
        .collect()
}

impl ClientSpec for ZCode {
    fn id(&self) -> &'static str {
        "zcode"
    }

    fn name(&self) -> &'static str {
        "ZCode"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `ZCODE_DATA_BASE_DIR` and another data folder set in ZCode are out
    /// of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".zcode").join("v2")
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&ProviderConfig]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self.dir(home, target).join("provider_config.json")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// Each provider rule holds an address and the token.
    fn holds_connection(&self, key: &str) -> bool {
        key == PROVIDERS_KEY
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let mut ids: Vec<String> = Vec::new();
        for id in entries(config, PROVIDERS_KEY)
            .iter()
            .filter_map(|rule| rule.get("config")?.get("personalModelIds")?.as_array())
            .flatten()
            .filter_map(Json::as_str)
            .filter_map(|id| clean_model(id.into()))
        {
            if !ids.contains(&id) {
                ids.push(id);
            }
        }
        let chosen = config
            .get(DEFAULT_KEY)
            .flatten()
            .and_then(|value| serde_json::from_str::<Json>(&value).ok())
            .filter(owned)
            .and_then(|chosen| chosen.get("modelId")?.as_str().map(str::to_string))
            .and_then(clean_model);
        CurrentModels {
            model: chosen.or_else(|| ids.first().cloned()),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let list = connection.list();
        let chosen = connection.model("model");
        let mut providers = Vec::new();
        let mut models = Vec::new();
        let mut ids = Vec::new();
        let mut default = None;
        for (protocol, id, name, kind) in ROUTES {
            let used: Vec<&String> = list
                .iter()
                .filter(|model| connection.protocol(self, model) == protocol)
                .collect();
            if used.is_empty() {
                continue;
            }
            providers.push(serde_json::json!({
                "providerId": id,
                "templateId": null,
                "providerName": name,
                "enabled": true,
                "config": {
                    "group": "standard-personal",
                    "access": { "type": "api-key", "apiKey": connection.token },
                    "api": { "type": kind, "baseUrl": self.base_url(connection.origin), "headers": {} },
                    "personalModelIds": used,
                    "modelOrder": used,
                    "visibility": "visible",
                },
            }));
            for model in used {
                let details = details.get(model.as_str());
                if Some(model) == chosen.as_ref() {
                    // ZCode skips a default without a level its model offers:
                    // an effort for Claude's, on or off for the others.
                    let reasons = details
                        .and_then(|details| details.reasoning.as_ref())
                        .is_some_and(|levels| !levels.is_empty());
                    let level = match (protocol, reasons) {
                        ("anthropic.messages", _) => "high",
                        (_, true) => "enabled",
                        (_, false) => "disabled",
                    };
                    default = Some(serde_json::json!({
                        "providerId": id,
                        "modelId": model,
                        "options": { "reasoningLevel": level },
                    }));
                }
                let mut rule = serde_json::json!({
                    "providerId": id,
                    "modelId": model,
                    "config": { "enabled": true, "properties": { "supportsToolCall": true } },
                });
                if let Some(context) = details.and_then(|details| details.context_window) {
                    rule["config"]["properties"]["contextWindow"] = context.into();
                }
                if let Some(output) = details.and_then(|details| details.max_tokens) {
                    rule["config"]["optionSpecs"] =
                        serde_json::json!({ "maxOutputTokens": { "max": output } });
                }
                models.push(rule);
            }
            ids.push(id);
        }
        let mut keys = Vec::new();
        if !providers.is_empty() {
            keys.push((PROVIDERS_KEY.into(), Json::Array(providers).to_string()));
            keys.push((MODELS_KEY.into(), Json::Array(models).to_string()));
            keys.push((ORDER_KEY.into(), serde_json::json!(ids).to_string()));
        }
        if let Some(default) = default {
            keys.push((DEFAULT_KEY.into(), default.to_string()));
        }
        keys
    }

    fn start_file(&self, config: &mut Config) -> Result<(), FileError> {
        let Format::Json {
            syntax, objects, ..
        } = ProviderConfig.format()
        else {
            return Ok(());
        };
        let empty = json::JsonDocument::parse(EMPTY, syntax.as_ref(), objects, false).map_err(
            |(message, line)| FileError {
                file: 0,
                message,
                line,
            },
        )?;
        if let Some(document) = config.documents.first_mut() {
            *document = Document::Json(empty);
        }
        Ok(())
    }

    /// Unused: `synced_keys` points every provider elsewhere.
    fn base_url_key(&self) -> &'static str {
        ""
    }

    fn synced_keys(
        &self,
        config: &Config,
        _record: &ClientRecord,
        origin: &str,
    ) -> Vec<(String, String)> {
        let mut providers = entries(config, PROVIDERS_KEY);
        if providers.is_empty() {
            return Vec::new();
        }
        for rule in &mut providers {
            rule["config"]["api"]["baseUrl"] = self.base_url(origin).into();
        }
        vec![(PROVIDERS_KEY.into(), Json::Array(providers).to_string())]
    }
}

/// Whether `entry` is the rule `wanted` is: the same provider, and the same
/// model for a model rule.
fn same_rule(entry: &Json, wanted: &Json) -> bool {
    entry.get("providerId") == wanted.get("providerId")
        && entry.get("modelId") == wanted.get("modelId")
}

impl ConfigFile for ProviderConfig {
    fn format(&self) -> Format {
        Format::Json {
            syntax: None,
            objects: &[
                "config",
                "config.providerConfigRules",
                "config.modelConfigRules",
                "config.defaultModelSelection",
            ],
            root_list: false,
        }
    }

    fn secret(&self) -> bool {
        true
    }

    /// A list reads as AstrLink's rules in it. The provider order reads as
    /// AstrLink's ids in it; without one, every provider is in order.
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Json(json) = document else {
            return None;
        };
        match key {
            PROVIDERS_KEY | MODELS_KEY => {
                let rules: Vec<Json> = json
                    .get_json(key)??
                    .as_array()?
                    .iter()
                    .filter(|rule| owned(rule))
                    .cloned()
                    .collect();
                (!rules.is_empty()).then(|| Some(Json::Array(rules).to_string()))
            }
            ORDER_KEY => {
                let ids = match json.get_json(key).flatten() {
                    Some(Json::Array(order)) => ours_in(order.iter().filter_map(Json::as_str)),
                    _ => ours_in(
                        json.get_json(PROVIDERS_KEY)
                            .flatten()
                            .and_then(|rules| rules.as_array().cloned())
                            .unwrap_or_default()
                            .iter()
                            .filter_map(|rule| rule.get("providerId")?.as_str()),
                    ),
                };
                (!ids.is_empty()).then(|| Some(serde_json::json!(ids).to_string()))
            }
            DEFAULT_KEY => json
                .get_json(key)
                .map(|value| value.map(|value| value.to_string())),
            _ => document.get(key),
        }
    }

    /// AstrLink's rules go after the user's; its ids join an order the
    /// user keeps.
    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Json(json) = document else {
            return document.set(key, value);
        };
        let value: Json = serde_json::from_str(value).map_err(|error| error.to_string())?;
        match key {
            PROVIDERS_KEY | MODELS_KEY => {
                let Json::Array(rules) = value else {
                    return Err(format!("{key} is not a list"));
                };
                json.remove_items(key, |entry| {
                    owned(entry) && !rules.iter().any(|rule| same_rule(entry, rule))
                });
                json.prepend_items(key, &[], rank)?;
                for rule in &rules {
                    json.put_item(key, |entry| same_rule(entry, rule), rule, rank)?;
                }
                Ok(())
            }
            ORDER_KEY => {
                let Json::Array(ids) = value else {
                    return Err(format!("{key} is not a list"));
                };
                let Some(Json::Array(order)) = json.get_json(key).flatten() else {
                    return Ok(());
                };
                json.remove_items(key, |entry| {
                    entry.as_str().is_some_and(ours) && !ids.contains(entry)
                });
                for id in ids.iter().filter(|id| !order.contains(id)) {
                    json.put_item(key, |entry| entry == id, id, rank)?;
                }
                Ok(())
            }
            _ => json.set_json(key, &value, rank),
        }
    }

    fn remove(&self, document: &mut Document, key: &str) {
        match document {
            Document::Json(json) if key == PROVIDERS_KEY || key == MODELS_KEY => {
                json.remove_items(key, owned);
            }
            Document::Json(json) if key == ORDER_KEY => {
                json.remove_items(key, |entry| entry.as_str().is_some_and(ours));
            }
            _ => document.remove(key),
        }
    }

    /// What ZCode starts a file with goes with the rest.
    fn is_empty(&self, document: &Document) -> bool {
        let Document::Json(json) = document else {
            return document.is_empty();
        };
        let blank = |value: &Json| match value {
            Json::Array(items) => items.is_empty(),
            Json::Object(fields) => fields
                .values()
                .all(|value| value.as_array().is_some_and(Vec::is_empty)),
            _ => false,
        };
        json.value()
            .and_then(|value| value.as_object().cloned())
            .is_some_and(|file| {
                file.iter().all(|(name, value)| match name.as_str() {
                    "schemaVersion" => true,
                    "config" => value
                        .as_object()
                        .is_some_and(|config| config.values().all(blank)),
                    _ => false,
                })
            })
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn file(home: &Path) -> PathBuf {
        home.join(".zcode/v2/provider_config.json")
    }

    fn read(home: &Path) -> serde_json::Value {
        read_json(ZCODE.files()[0], &fs::read_to_string(file(home)).unwrap()).unwrap()
    }

    fn provider(id: &str, name: &str, kind: &str, models: &[&str]) -> serde_json::Value {
        serde_json::json!({
            "providerId": id, "templateId": null, "providerName": name, "enabled": true,
            "config": {
                "group": "standard-personal",
                "access": {"type": "api-key", "apiKey": TOKEN},
                "api": {"type": kind, "baseUrl": format!("{ORIGIN}/v1"), "headers": {}},
                "personalModelIds": models, "modelOrder": models, "visibility": "visible",
            },
        })
    }

    #[test]
    fn writes_a_provider_per_protocol_and_a_rule_per_model() {
        let home = unique_home("zcode");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".zcode/v2")).unwrap();
        let models = vec![("model", "claude-sonnet-5".to_string())];
        let list = strings(&["claude-sonnet-5", "deepseek-v4"]);
        let protocols = BTreeMap::from([("claude-sonnet-5".to_string(), "anthropic.messages")]);
        let chosen = Connection {
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        let details = Catalog::from([(
            "claude-sonnet-5".to_string(),
            ModelDetails {
                context_window: Some(200_000),
                max_tokens: Some(64_000),
                reasoning: Some(levels(&["low", "high"])),
            },
        )]);
        let outcome = write_with(local, ZCODE, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        assert_eq!(
            read(&home),
            serde_json::json!({
                "schemaVersion": 1,
                "config": {
                    "providerOrder": ["local-chat", "local-anthropic"],
                    "providerConfigRules": {"providerRules": [
                        provider("local-chat", "AstrLink", "openai-chat-completions", &["deepseek-v4"]),
                        provider("local-anthropic", "AstrLink Anthropic", "anthropic-messages", &["claude-sonnet-5"]),
                    ]},
                    "modelConfigRules": {
                        "providerModelRules": [
                            {"providerId": "local-chat", "modelId": "deepseek-v4",
                             "config": {"enabled": true, "properties": {"supportsToolCall": true}}},
                            {"providerId": "local-anthropic", "modelId": "claude-sonnet-5",
                             "config": {"enabled": true, "properties": {"contextWindow": 200_000, "supportsToolCall": true},
                                        "optionSpecs": {"maxOutputTokens": {"max": 64_000}}}},
                        ],
                        "manualProviderModelRules": [],
                    },
                    "defaultModelSelection": {
                        "providerId": "local-anthropic", "modelId": "claude-sonnet-5",
                        "options": {"reasoningLevel": "high"},
                    },
                },
            })
        );
        let status = &status_of(local, [ZCODE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-sonnet-5"));

        // Without the Claude model, its provider, rule and place go.
        let models = vec![("model", "deepseek-v4".to_string())];
        write(local, ZCODE, &connection(&models), &tokens(), false).unwrap();
        let written = read(&home);
        assert_eq!(
            written["config"]["providerOrder"],
            serde_json::json!(["local-chat"])
        );
        assert_eq!(
            written["config"]["providerConfigRules"]["providerRules"]
                .as_array()
                .unwrap()
                .len(),
            1
        );
        assert_eq!(
            written["config"]["modelConfigRules"]["providerModelRules"]
                .as_array()
                .unwrap()
                .len(),
            1
        );

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert_eq!(
            read(&home)["config"]["providerConfigRules"]["providerRules"][0]["config"]["api"]
                ["baseUrl"],
            "http://127.0.0.1:9000/v1"
        );

        remove(local, ZCODE).unwrap();
        assert!(!file(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = r#"{
  "schemaVersion": 1,
  "config": {
    "providerOrder": ["deepseek"],
    "providerConfigRules": {
      "providerRules": [
        {"providerId": "deepseek", "templateId": null, "providerName": "DeepSeek", "enabled": true, "config": {"group": "standard-personal"}}
      ]
    },
    "modelConfigRules": {
      "providerModelRules": [],
      "manualProviderModelRules": []
    },
    "defaultModelSelection": {"providerId": "deepseek", "modelId": "deepseek-v4-pro"}
  }
}
"#;

    #[test]
    fn adds_its_provider_after_the_users_and_puts_their_default_back() {
        let home = unique_home("zcode-merge");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".zcode/v2")).unwrap();
        fs::write(file(&home), USER).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, ZCODE, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["config.defaultModelSelection"]), None))
        );
        write(local, ZCODE, &connection(&models), &tokens(), true).unwrap();
        let written = read(&home);
        assert_eq!(
            written["config"]["providerOrder"],
            serde_json::json!(["deepseek", "local-chat"])
        );
        let rules = &written["config"]["providerConfigRules"]["providerRules"];
        assert_eq!(rules[0]["providerId"], "deepseek");
        assert_eq!(rules[1]["providerId"], "local-chat");
        assert_eq!(
            written["config"]["defaultModelSelection"],
            serde_json::json!({"providerId": "local-chat", "modelId": "deepseek-v4", "options": {"reasoningLevel": "disabled"}})
        );

        remove(local, ZCODE).unwrap();
        assert_eq!(read(&home), read_json(ZCODE.files()[0], USER).unwrap());
        fs::remove_dir_all(home).unwrap();
    }
}
