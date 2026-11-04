//! Qoder CLI reads custom providers from `providers` in
//! `~/.qoder/settings.json` (comments allowed) and the model it starts with
//! from `model.name`, as `<provider>/<model>`. A provider has one
//! `protocol`, so AstrLink adds one per protocol its models use. Qoder uses
//! them only for an account signed in with custom models allowed.

use std::path::PathBuf;

use serde_json::Value as Json;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{json, Document, Format},
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&QoderCli);

struct QoderCli;

/// `settings.json`.
struct Settings;

/// Protocol, provider id, the name Qoder shows, `protocol`, and `authType`.
const ROUTES: [(&str, &str, &str, &str, &str); 3] = [
    ("openai.chat", "local-chat", "AstrLink", "openai", "bearer"),
    (
        "openai.responses",
        "local-responses",
        "AstrLink Responses",
        "openai-responses",
        "bearer",
    ),
    (
        "anthropic.messages",
        "local-anthropic",
        "AstrLink Anthropic",
        "anthropic",
        "api-key",
    ),
];
const PREFIX: &str = "providers.";
const MODEL_KEY: &str = "model.name";
/// The thinking levels Qoder takes.
const EFFORTS: [&str; 5] = ["low", "medium", "high", "xhigh", "max"];

fn provider_key(id: &str) -> String {
    format!("{PREFIX}{id}")
}

fn ours(id: &str) -> bool {
    ROUTES.iter().any(|(_, known, ..)| *known == id)
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 17] = [
        "type",
        "displayName",
        "protocol",
        "authType",
        "baseUrl",
        "apiKey",
        "model",
        "models",
        "contextWindow",
        "maxOutputTokens",
        "capabilities",
        "tools",
        "thinking",
        "modes",
        "preferredMode",
        "supportsEffort",
        "supportedEffortLevels",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn json_at(config: &Config, key: &str) -> Option<Json> {
    serde_json::from_str(&config.get(key).flatten()?).ok()
}

fn model_entry(id: &str, details: &Catalog) -> Json {
    let mut capabilities = serde_json::json!({ "tools": true });
    let mut entry = serde_json::json!({ "model": id });
    if let Some(details) = details.get(id) {
        let levels: Vec<&str> = details
            .reasoning
            .iter()
            .flatten()
            .map(String::as_str)
            .filter(|level| EFFORTS.contains(level))
            .collect();
        if !levels.is_empty() {
            capabilities["thinking"] = serde_json::json!({
                "modes": ["enabled"],
                "supportsEffort": true,
                "supportedEffortLevels": levels,
            });
        }
        if let Some(context) = details.context_window {
            entry["contextWindow"] = context.into();
        }
        if let Some(output) = details.max_tokens {
            entry["maxOutputTokens"] = output.into();
        }
    }
    entry["capabilities"] = capabilities;
    entry
}

impl ClientSpec for QoderCli {
    fn id(&self) -> &'static str {
        "qoder_cli"
    }

    fn name(&self) -> &'static str {
        "Qoder CLI"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `QODER_CONFIG_DIR` and `--config-dir` are out of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".qoder")
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&Settings]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self.dir(home, target).join("settings.json")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// Each provider holds an address and the token.
    fn holds_connection(&self, key: &str) -> bool {
        key.strip_prefix(PREFIX).is_some_and(ours)
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let mut ids: Vec<String> = Vec::new();
        for (_, id, ..) in ROUTES {
            let Some(provider) = json_at(config, &provider_key(id)) else {
                continue;
            };
            for model in provider
                .get("models")
                .and_then(Json::as_array)
                .into_iter()
                .flatten()
                .filter_map(|model| model.get("model")?.as_str())
                .filter_map(|model| clean_model(model.into()))
            {
                if !ids.contains(&model) {
                    ids.push(model);
                }
            }
        }
        let chosen = config
            .get(MODEL_KEY)
            .flatten()
            .and_then(|chosen| {
                let (provider, model) = chosen.split_once('/')?;
                ours(provider).then(|| model.to_string())
            })
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
        let mut keys = Vec::new();
        for (protocol, id, name, kind, auth) in ROUTES {
            let models: Vec<&String> = list
                .iter()
                .filter(|model| connection.protocol(self, model) == protocol)
                .collect();
            let Some(first) = models.first() else {
                continue;
            };
            let default = chosen
                .as_ref()
                .filter(|model| models.contains(model))
                .unwrap_or(*first);
            // Qoder adds `/messages` to an Anthropic URL ending in `/v1`.
            let provider = serde_json::json!({
                "type": "openai-compatible",
                "displayName": name,
                "protocol": kind,
                "authType": auth,
                "baseUrl": self.base_url(connection.origin),
                "apiKey": connection.token,
                "model": default,
                "models": models.iter().map(|model| model_entry(model, details)).collect::<Vec<_>>(),
            });
            keys.push((provider_key(id), provider.to_string()));
        }
        if let Some(model) = chosen {
            let protocol = connection.protocol(self, &model);
            if let Some((_, id, ..)) = ROUTES.iter().find(|(known, ..)| *known == protocol) {
                keys.push((MODEL_KEY.into(), format!("{id}/{model}")));
            }
        }
        keys
    }

    /// Unused: `synced_keys` points every provider elsewhere.
    fn base_url_key(&self) -> &'static str {
        ""
    }

    fn synced_keys(
        &self,
        config: &Config,
        record: &ClientRecord,
        origin: &str,
    ) -> Vec<(String, String)> {
        record
            .keys
            .keys()
            .filter(|key| self.holds_connection(key))
            .filter_map(|key| {
                let mut provider = json_at(config, key)?;
                provider["baseUrl"] = self.base_url(origin).into();
                Some((key.clone(), provider.to_string()))
            })
            .collect()
    }

    fn notes(
        &self,
        _home: Home,
        _target: Option<&str>,
        _config: Option<&Config>,
    ) -> Vec<&'static str> {
        vec!["account"]
    }
}

impl ConfigFile for Settings {
    fn format(&self) -> Format {
        Format::Json {
            syntax: Some(jsonc_parser::ParseOptions {
                allow_comments: true,
                allow_trailing_commas: true,
                ..json::strict()
            }),
            objects: &["providers", "model"],
            root_list: false,
        }
    }

    fn secret(&self) -> bool {
        true
    }

    /// A provider reads as one JSON object.
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        match document {
            Document::Json(json) if key.starts_with(PREFIX) => json
                .get_json(key)
                .map(|value| value.map(|value| value.to_string())),
            _ => document.get(key),
        }
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        match document {
            Document::Json(json) if key.starts_with(PREFIX) => {
                let value: Json = serde_json::from_str(value).map_err(|error| error.to_string())?;
                json.set_json(key, &value, rank)
            }
            _ => document.set(key, value),
        }
    }

    /// An emptied `providers` or `model` goes.
    fn remove(&self, document: &mut Document, key: &str) {
        let Document::Json(json) = document else {
            return document.remove(key);
        };
        json.remove(key);
        json.drop_empty(&["providers", "model"]);
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn file(home: &Path) -> PathBuf {
        home.join(".qoder/settings.json")
    }

    fn read(home: &Path) -> serde_json::Value {
        read_json(
            QODER_CLI.files()[0],
            &fs::read_to_string(file(home)).unwrap(),
        )
        .unwrap()
    }

    #[test]
    fn writes_a_provider_per_protocol_and_the_startup_model() {
        let home = unique_home("qoder");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".qoder")).unwrap();
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
                reasoning: Some(levels(&["none", "low", "high"])),
                context_window: Some(200_000),
                max_tokens: Some(64_000),
            },
        )]);
        let outcome = write_with(local, QODER_CLI, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        assert_eq!(
            read(&home),
            serde_json::json!({
                "providers": {
                    "local-chat": {
                        "type": "openai-compatible", "displayName": "AstrLink", "protocol": "openai",
                        "authType": "bearer", "baseUrl": format!("{ORIGIN}/v1"), "apiKey": TOKEN,
                        "model": "deepseek-v4",
                        "models": [{"model": "deepseek-v4", "capabilities": {"tools": true}}],
                    },
                    "local-anthropic": {
                        "type": "openai-compatible", "displayName": "AstrLink Anthropic", "protocol": "anthropic",
                        "authType": "api-key", "baseUrl": format!("{ORIGIN}/v1"), "apiKey": TOKEN,
                        "model": "claude-sonnet-5",
                        "models": [{
                            "model": "claude-sonnet-5", "contextWindow": 200_000, "maxOutputTokens": 64_000,
                            "capabilities": {"tools": true, "thinking": {
                                "modes": ["enabled"], "supportsEffort": true, "supportedEffortLevels": ["low", "high"],
                            }},
                        }],
                    },
                },
                "model": {"name": "local-anthropic/claude-sonnet-5"},
            })
        );
        let status = &status_of(local, [QODER_CLI], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-sonnet-5"));
        assert_eq!(status.notes, ["account"]);

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = read(&home);
        assert_eq!(
            synced["providers"]["local-chat"]["baseUrl"],
            "http://127.0.0.1:9000/v1"
        );
        assert_eq!(
            synced["providers"]["local-anthropic"]["baseUrl"],
            "http://127.0.0.1:9000/v1"
        );

        remove(local, QODER_CLI).unwrap();
        assert!(!file(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = "{\n  // my qoder\n  \"model\": {\"name\": \"auto\"},\n  \"providers\": {\n    \"mine\": {\"protocol\": \"openai\", \"baseUrl\": \"https://example.invalid/v1\", \"apiKey\": \"sk-mine\", \"models\": [{\"model\": \"glm-5\"}]},\n  },\n}\n";

    #[test]
    fn keeps_the_users_provider_and_puts_their_model_back() {
        let home = unique_home("qoder-merge");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".qoder")).unwrap();
        fs::write(file(&home), USER).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, QODER_CLI, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["model.name"]), None))
        );
        write(local, QODER_CLI, &connection(&models), &tokens(), true).unwrap();
        let written = read(&home);
        assert_eq!(written["providers"]["mine"]["apiKey"], "sk-mine");
        assert_eq!(written["providers"]["local-chat"]["apiKey"], TOKEN);
        assert_eq!(written["model"]["name"], "local-chat/deepseek-v4");

        remove(local, QODER_CLI).unwrap();
        assert_eq!(fs::read_to_string(file(&home)).unwrap(), USER);
        fs::remove_dir_all(home).unwrap();
    }
}
