//! Step Code reads its providers from `~/.stepcode/models.json`, Pi's
//! format, and the model it starts with from `~/.stepcode/config.toml`. A
//! provider has one `api`, so AstrLink adds one per protocol its models use.

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

pub(crate) const CLIENT: Client = Client::new(&StepCode);

struct StepCode;

/// `models.json`.
struct StepModels;

/// `config.toml`.
struct StepConfig;

/// Protocol, provider id, and `api`.
const ROUTES: [(&str, &str, &str); 3] = [
    ("openai.chat", "local-chat", "openai-completions"),
    ("openai.responses", "local-responses", "openai-responses"),
    (
        "anthropic.messages",
        "local-anthropic",
        "anthropic-messages",
    ),
];
const PREFIX: &str = "providers.";
const STARTUP_KEYS: [&str; 2] = ["defaultProvider", "defaultModel"];

fn provider_key(id: &str) -> String {
    format!("{PREFIX}{id}")
}

fn ours(id: &str) -> bool {
    ROUTES.iter().any(|(_, known, _)| *known == id)
}

/// Step Code adds `/v1/messages` to an Anthropic URL, and the rest to the
/// others.
fn provider_url(api: &str, origin: &str) -> String {
    if api == "anthropic-messages" {
        origin.to_string()
    } else {
        format!("{origin}/v1")
    }
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 8] = [
        "baseUrl",
        "api",
        "apiKey",
        "models",
        "id",
        "reasoning",
        "contextWindow",
        "maxTokens",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn json_at(config: &Config, key: &str) -> Option<Json> {
    serde_json::from_str(&config.get(key).flatten()?).ok()
}

impl ClientSpec for StepCode {
    fn id(&self) -> &'static str {
        "step_code"
    }

    fn name(&self) -> &'static str {
        "Step Code"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `STEPCODE_CONFIG_DIR` and `STEP_CODING_AGENT_DIR` from a shell are
    /// out of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".stepcode")
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&StepModels, &StepConfig]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        let dir = self.dir(home, target);
        vec![dir.join("models.json"), dir.join("config.toml")]
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
        for (_, id, _) in ROUTES {
            let Some(provider) = json_at(config, &provider_key(id)) else {
                continue;
            };
            for model in provider
                .get("models")
                .and_then(Json::as_array)
                .into_iter()
                .flatten()
                .filter_map(|model| model.get("id")?.as_str())
                .filter_map(|model| clean_model(model.into()))
            {
                if !ids.contains(&model) {
                    ids.push(model);
                }
            }
        }
        let chosen = config
            .get(STARTUP_KEYS[0])
            .flatten()
            .filter(|provider| ours(provider))
            .and_then(|_| config.model(STARTUP_KEYS[1]));
        CurrentModels {
            model: chosen.or_else(|| ids.first().cloned()),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let list = connection.list();
        let mut keys = Vec::new();
        for (protocol, id, api) in ROUTES {
            let models: Vec<Json> = list
                .iter()
                .filter(|model| connection.protocol(self, model) == protocol)
                .map(|model| {
                    let mut entry = serde_json::json!({ "id": model });
                    let Some(details) = details.get(model.as_str()) else {
                        return entry;
                    };
                    // Pi takes a model it is not told reasons as one that
                    // cannot.
                    if details
                        .reasoning
                        .as_ref()
                        .is_some_and(|levels| !levels.is_empty())
                    {
                        entry["reasoning"] = true.into();
                    }
                    if let Some(context) = details.context_window {
                        entry["contextWindow"] = context.into();
                    }
                    if let Some(output) = details.max_tokens {
                        entry["maxTokens"] = output.into();
                    }
                    entry
                })
                .collect();
            if models.is_empty() {
                continue;
            }
            let provider = serde_json::json!({
                "baseUrl": provider_url(api, connection.origin),
                "api": api,
                "apiKey": connection.token,
                "models": models,
            });
            keys.push((provider_key(id), provider.to_string()));
        }
        if let Some(model) = connection.model("model") {
            let protocol = connection.protocol(self, &model);
            if let Some((_, id, _)) = ROUTES.iter().find(|(known, ..)| *known == protocol) {
                keys.push((STARTUP_KEYS[0].into(), (*id).into()));
                keys.push((STARTUP_KEYS[1].into(), model));
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
                let api = provider.get("api")?.as_str()?.to_string();
                provider["baseUrl"] = provider_url(&api, origin).into();
                Some((key.clone(), provider.to_string()))
            })
            .collect()
    }
}

impl ConfigFile for StepModels {
    /// Pi strips line comments and trailing commas.
    fn format(&self) -> Format {
        Format::Json {
            syntax: Some(jsonc_parser::ParseOptions {
                allow_comments: true,
                allow_trailing_commas: true,
                ..json::strict()
            }),
            objects: &["providers"],
            root_list: false,
        }
    }

    fn secret(&self) -> bool {
        true
    }

    fn holds(&self, key: &str) -> bool {
        !STARTUP_KEYS.contains(&key)
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

    /// An emptied `providers` stays until the file goes.
    fn is_empty(&self, document: &Document) -> bool {
        match document {
            Document::Json(json) => json.holds_only(|name, property| {
                name == Some("providers")
                    && property
                        .object_value()
                        .is_some_and(|providers| providers.properties().is_empty())
            }),
            _ => document.is_empty(),
        }
    }
}

impl ConfigFile for StepConfig {
    fn format(&self) -> Format {
        Format::Toml
    }

    fn holds(&self, key: &str) -> bool {
        STARTUP_KEYS.contains(&key)
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn dir(home: &Path) -> PathBuf {
        home.join(".stepcode")
    }

    fn read(home: &Path) -> serde_json::Value {
        read_json(
            STEP_CODE.files()[0],
            &fs::read_to_string(dir(home).join("models.json")).unwrap(),
        )
        .unwrap()
    }

    #[test]
    fn writes_a_provider_per_protocol_and_the_startup_model() {
        let home = unique_home("step");
        let local = Home::local(&home);
        fs::create_dir_all(dir(&home)).unwrap();
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
                reasoning: Some(levels(&["low", "high"])),
                context_window: Some(200_000),
                max_tokens: Some(64_000),
            },
        )]);
        let outcome = write_with(local, STEP_CODE, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        assert_eq!(
            read(&home),
            serde_json::json!({"providers": {
                "local-chat": {
                    "baseUrl": format!("{ORIGIN}/v1"), "api": "openai-completions", "apiKey": TOKEN,
                    "models": [{"id": "deepseek-v4"}],
                },
                "local-anthropic": {
                    "baseUrl": ORIGIN, "api": "anthropic-messages", "apiKey": TOKEN,
                    "models": [{"id": "claude-sonnet-5", "reasoning": true, "contextWindow": 200_000, "maxTokens": 64_000}],
                },
            }})
        );
        assert_eq!(
            fs::read_to_string(dir(&home).join("config.toml")).unwrap(),
            "defaultProvider = \"local-anthropic\"\ndefaultModel = \"claude-sonnet-5\"\n"
        );
        let status = &status_of(local, [STEP_CODE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-sonnet-5"));

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = read(&home);
        assert_eq!(
            synced["providers"]["local-chat"]["baseUrl"],
            "http://127.0.0.1:9000/v1"
        );
        assert_eq!(
            synced["providers"]["local-anthropic"]["baseUrl"],
            "http://127.0.0.1:9000"
        );

        remove(local, STEP_CODE).unwrap();
        assert!(!dir(&home).join("models.json").exists());
        assert!(!dir(&home).join("config.toml").exists());
        fs::remove_dir_all(home).unwrap();
    }

    const MODELS: &str = "{\n  // local models\n  \"providers\": {\n    \"ollama\": {\"baseUrl\": \"http://localhost:11434/v1\", \"api\": \"openai-completions\", \"apiKey\": \"ollama\", \"models\": [{\"id\": \"llama3.1:8b\"}]},\n  },\n}\n";
    const CONFIG: &str =
        "# step preferences\ndefaultProvider = \"step\"\ndefaultModel = \"step-3.5\"\n";

    #[test]
    fn keeps_the_users_providers_and_puts_their_model_back() {
        let home = unique_home("step-merge");
        let local = Home::local(&home);
        fs::create_dir_all(dir(&home)).unwrap();
        fs::write(dir(&home).join("models.json"), MODELS).unwrap();
        fs::write(dir(&home).join("config.toml"), CONFIG).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, STEP_CODE, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["defaultProvider", "defaultModel"]), None))
        );
        write(local, STEP_CODE, &connection(&models), &tokens(), true).unwrap();
        let written = read(&home);
        assert_eq!(written["providers"]["ollama"]["apiKey"], "ollama");
        assert_eq!(written["providers"]["local-chat"]["apiKey"], TOKEN);
        assert_eq!(
            fs::read_to_string(dir(&home).join("config.toml")).unwrap(),
            "# step preferences\ndefaultProvider = \"local-chat\"\ndefaultModel = \"deepseek-v4\"\n"
        );

        remove(local, STEP_CODE).unwrap();
        assert_eq!(
            fs::read_to_string(dir(&home).join("models.json")).unwrap(),
            MODELS
        );
        assert_eq!(
            fs::read_to_string(dir(&home).join("config.toml")).unwrap(),
            CONFIG
        );
        fs::remove_dir_all(home).unwrap();
    }
}
