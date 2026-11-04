//! Copilot CLI reads `~/.copilot/providers.json` for providers and their
//! models, and starts with `model` from `~/.copilot/settings.json`. One
//! provider has one wire API, so AstrLink adds a provider per protocol its
//! models use, and a model entry for each model. Copilot tells the model
//! `<provider>/<id>`, so the providers are named `local-<protocol>`.

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

pub(crate) const CLIENT: Client = Client::new(&CopilotCli);

struct CopilotCli;

/// `providers.json`.
struct Providers;

/// `settings.json`.
struct Settings;

const PROVIDERS_KEY: &str = "providers";
const MODELS_KEY: &str = "models";
const MODEL_KEY: &str = "model";

/// Protocol, provider name, `type`, and `wireApi`.
const ROUTES: [(&str, &str, &str, Option<&str>); 3] = [
    ("openai.chat", "local-chat", "openai", Some("completions")),
    (
        "openai.responses",
        "local-responses",
        "openai",
        Some("responses"),
    ),
    ("anthropic.messages", "local-anthropic", "anthropic", None),
];

fn route(
    protocol: &str,
) -> (
    &'static str,
    &'static str,
    &'static str,
    Option<&'static str>,
) {
    ROUTES
        .into_iter()
        .find(|(known, ..)| *known == protocol)
        .unwrap_or(ROUTES[0])
}

fn ours(name: &str) -> bool {
    ROUTES.iter().any(|(_, known, ..)| *known == name)
}

/// Anthropic's SDK adds `/v1` itself; the OpenAI ones take it.
fn provider_url(kind: &str, origin: &str) -> String {
    if kind == "anthropic" {
        origin.to_string()
    } else {
        format!("{origin}/v1")
    }
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 10] = [
        "name",
        "id",
        "type",
        "provider",
        "wireApi",
        "baseUrl",
        "apiKey",
        "maxContextWindowTokens",
        "maxPromptTokens",
        "maxOutputTokens",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

/// Which field of an entry in `list` names its provider.
fn owner_field(list: &str) -> &'static str {
    if list == PROVIDERS_KEY {
        "name"
    } else {
        "provider"
    }
}

fn owned_in(list: &str) -> impl Fn(&Json) -> bool {
    let field = owner_field(list);
    move |entry| entry.get(field).and_then(Json::as_str).is_some_and(ours)
}

fn owned_entries(config: &Config, list: &str) -> Vec<Json> {
    config
        .get(list)
        .flatten()
        .and_then(|value| serde_json::from_str::<Json>(&value).ok())
        .and_then(|value| value.as_array().cloned())
        .unwrap_or_default()
}

impl ClientSpec for CopilotCli {
    fn id(&self) -> &'static str {
        "copilot_cli"
    }

    fn name(&self) -> &'static str {
        "Copilot CLI"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `COPILOT_HOME` moves it from a shell; AstrLink writes the default.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".copilot")
    }

    /// VS Code keeps chats in `~/.copilot` too; the CLI writes
    /// `config.json` on its first run.
    fn detected(&self, home: Home, target: Option<&str>) -> bool {
        self.dir(home, target).join("config.json").is_file()
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&Providers, &Settings]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        let dir = self.dir(home, target);
        vec![dir.join("providers.json"), dir.join("settings.json")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// Each provider holds an address and the token.
    fn holds_connection(&self, key: &str) -> bool {
        key == PROVIDERS_KEY
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let entries = owned_entries(config, MODELS_KEY);
        let name = |entry: &Json| {
            Some(format!(
                "{}/{}",
                entry.get("provider")?.as_str()?,
                entry.get("id")?.as_str()?
            ))
        };
        let mut ids: Vec<String> = Vec::new();
        for id in entries
            .iter()
            .filter_map(|entry| entry.get("id")?.as_str())
            .filter_map(|id| clean_model(id.into()))
        {
            if !ids.contains(&id) {
                ids.push(id);
            }
        }
        let chosen = config.get(MODEL_KEY).flatten().and_then(|chosen| {
            let entry = entries
                .iter()
                .find(|entry| name(entry).as_deref() == Some(chosen.as_str()))?;
            entry.get("id")?.as_str().map(str::to_string)
        });
        CurrentModels {
            model: chosen.or_else(|| ids.first().cloned()),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let list = connection.list();
        let mut providers = Vec::new();
        let mut models = Vec::new();
        for (protocol, name, kind, wire) in ROUTES {
            let used: Vec<&String> = list
                .iter()
                .filter(|id| connection.protocol(self, id) == protocol)
                .collect();
            if used.is_empty() {
                continue;
            }
            let mut provider = serde_json::json!({
                "name": name,
                "type": kind,
                "baseUrl": provider_url(kind, connection.origin),
                "apiKey": connection.token,
            });
            if let Some(wire) = wire {
                provider["wireApi"] = wire.into();
            }
            providers.push(provider);
            for id in used {
                let mut model = serde_json::json!({ "id": id, "provider": name });
                let limits = details
                    .get(id.as_str())
                    .and_then(|details| Some((details.context_window?, details.max_tokens?)));
                if let Some((context, output)) = limits.filter(|(context, output)| context > output)
                {
                    model["maxContextWindowTokens"] = context.into();
                    model["maxPromptTokens"] = (context - output).into();
                    model["maxOutputTokens"] = output.into();
                }
                models.push(model);
            }
        }
        let mut keys = Vec::new();
        if !providers.is_empty() {
            keys.push((PROVIDERS_KEY.into(), Json::Array(providers).to_string()));
            keys.push((MODELS_KEY.into(), Json::Array(models).to_string()));
        }
        if let Some(model) = connection.model("model") {
            let (_, name, ..) = route(connection.protocol(self, &model));
            keys.push((MODEL_KEY.into(), format!("{name}/{model}")));
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
        _record: &ClientRecord,
        origin: &str,
    ) -> Vec<(String, String)> {
        let mut providers = owned_entries(config, PROVIDERS_KEY);
        if providers.is_empty() {
            return Vec::new();
        }
        for provider in &mut providers {
            let kind = provider
                .get("type")
                .and_then(Json::as_str)
                .unwrap_or_default()
                .to_string();
            provider["baseUrl"] = provider_url(&kind, origin).into();
        }
        vec![(PROVIDERS_KEY.into(), Json::Array(providers).to_string())]
    }
}

impl ConfigFile for Providers {
    /// A comment fails the whole file.
    fn format(&self) -> Format {
        Format::Json {
            syntax: None,
            objects: &[],
            root_list: false,
        }
    }

    fn secret(&self) -> bool {
        true
    }

    fn holds(&self, key: &str) -> bool {
        key != MODEL_KEY
    }

    /// A list reads as AstrLink's entries in it, compact.
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Json(json) = document else {
            return None;
        };
        let owned = owned_in(key);
        let entries: Vec<Json> = json
            .get_json(key)??
            .as_array()?
            .iter()
            .filter(|entry| owned(entry))
            .cloned()
            .collect();
        (!entries.is_empty()).then(|| Some(Json::Array(entries).to_string()))
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Json(json) = document else {
            return document.set(key, value);
        };
        let entries: Vec<Json> = match serde_json::from_str(value) {
            Ok(Json::Array(entries)) => entries,
            _ => return Err(format!("{key} is not a list")),
        };
        json.remove_items(key, owned_in(key));
        json.prepend_items(key, &entries, rank)
    }

    fn remove(&self, document: &mut Document, key: &str) {
        if let Document::Json(json) = document {
            json.remove_items(key, owned_in(key));
        }
    }

    /// Emptied lists go.
    fn tidy(&self, document: &mut Document) {
        if let Document::Json(json) = document {
            for list in [PROVIDERS_KEY, MODELS_KEY] {
                if json
                    .get_json(list)
                    .flatten()
                    .is_some_and(|value| value.as_array().is_some_and(Vec::is_empty))
                {
                    json.remove(list);
                }
            }
        }
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
            objects: &[],
            root_list: false,
        }
    }

    fn holds(&self, key: &str) -> bool {
        key == MODEL_KEY
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn installed(home: &Path) {
        fs::create_dir_all(home.join(".copilot")).unwrap();
        fs::write(
            home.join(".copilot/config.json"),
            "// This file is managed automatically.\n{\"firstLaunchAt\": \"2026-11-03T00:00:00Z\"}\n",
        )
        .unwrap();
    }

    fn read(home: &Path, file: &str) -> serde_json::Value {
        let index = usize::from(file == "settings.json");
        let text = fs::read_to_string(home.join(".copilot").join(file)).unwrap();
        read_json(COPILOT_CLI.files()[index], &text).unwrap()
    }

    #[test]
    fn writes_a_provider_per_protocol_and_picks_the_default() {
        let home = unique_home("copilot");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".copilot")).unwrap();
        assert!(!COPILOT_CLI.detected(local, None));
        installed(&home);
        assert!(COPILOT_CLI.detected(local, None));
        let models = vec![("model", "gpt-5.5".to_string())];
        let list = strings(&["deepseek-v4", "gpt-5.5", "claude-sonnet-5"]);
        let protocols = BTreeMap::from([
            ("gpt-5.5".to_string(), "openai.responses"),
            ("claude-sonnet-5".to_string(), "anthropic.messages"),
        ]);
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
                ..Default::default()
            },
        )]);
        let outcome = write_with(local, COPILOT_CLI, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        let v1 = format!("{ORIGIN}/v1");
        assert_eq!(
            read(&home, "providers.json"),
            serde_json::json!({
                "providers": [
                    {"name": "local-chat", "type": "openai", "wireApi": "completions", "baseUrl": v1, "apiKey": TOKEN},
                    {"name": "local-responses", "type": "openai", "wireApi": "responses", "baseUrl": v1, "apiKey": TOKEN},
                    {"name": "local-anthropic", "type": "anthropic", "baseUrl": ORIGIN, "apiKey": TOKEN},
                ],
                "models": [
                    {"id": "deepseek-v4", "provider": "local-chat"},
                    {"id": "gpt-5.5", "provider": "local-responses"},
                    {"id": "claude-sonnet-5", "provider": "local-anthropic",
                     "maxContextWindowTokens": 200_000, "maxPromptTokens": 136_000, "maxOutputTokens": 64_000},
                ],
            })
        );
        assert_eq!(
            read(&home, "settings.json"),
            serde_json::json!({"model": "local-responses/gpt-5.5"})
        );
        let status = &status_of(local, [COPILOT_CLI], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("gpt-5.5"));
        assert_eq!(status.models.models.as_ref().map(Vec::len), Some(3));

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = read(&home, "providers.json");
        assert_eq!(
            synced["providers"][0]["baseUrl"],
            "http://127.0.0.1:9000/v1"
        );
        assert_eq!(synced["providers"][2]["baseUrl"], "http://127.0.0.1:9000");

        remove(local, COPILOT_CLI).unwrap();
        assert!(!home.join(".copilot/providers.json").exists());
        assert!(!home.join(".copilot/settings.json").exists());
        fs::remove_dir_all(home).unwrap();
    }

    const PROVIDERS: &str = r#"{
  "providers": [
    {
      "name": "ollama",
      "type": "openai",
      "baseUrl": "http://127.0.0.1:11434/v1"
    }
  ],
  "models": [
    {
      "id": "qwen3:8b",
      "provider": "ollama",
      "maxPromptTokens": 6144,
      "maxOutputTokens": 2048
    }
  ]
}
"#;
    const SETTINGS: &str =
        "{\n  // mine\n  \"banner\": \"never\",\n  \"model\": \"ollama/qwen3:8b\",\n}\n";

    #[test]
    fn keeps_the_users_providers_and_puts_their_model_back() {
        let home = unique_home("copilot-merge");
        let local = Home::local(&home);
        installed(&home);
        fs::write(home.join(".copilot/providers.json"), PROVIDERS).unwrap();
        fs::write(home.join(".copilot/settings.json"), SETTINGS).unwrap();
        let models = vec![("model", "claude-sonnet-5".to_string())];
        let protocols = BTreeMap::from([("claude-sonnet-5".to_string(), "anthropic.messages")]);
        let chosen = Connection {
            protocols: &protocols,
            ..connection(&models)
        };
        assert_eq!(
            asked(write(local, COPILOT_CLI, &chosen, &tokens(), false).unwrap()),
            Some((strings(&["model"]), None))
        );
        write(local, COPILOT_CLI, &chosen, &tokens(), true).unwrap();
        let written = read(&home, "providers.json");
        assert_eq!(written["providers"][0]["name"], "local-anthropic");
        assert_eq!(written["providers"][1]["name"], "ollama");
        assert_eq!(written["models"][1]["id"], "qwen3:8b");
        let settings = fs::read_to_string(home.join(".copilot/settings.json")).unwrap();
        assert!(settings.contains("// mine"), "{settings}");
        assert!(
            settings.contains("\"model\": \"local-anthropic/claude-sonnet-5\""),
            "{settings}"
        );

        remove(local, COPILOT_CLI).unwrap();
        assert_eq!(
            fs::read_to_string(home.join(".copilot/providers.json")).unwrap(),
            PROVIDERS
        );
        assert_eq!(
            fs::read_to_string(home.join(".copilot/settings.json")).unwrap(),
            SETTINGS
        );
        fs::remove_dir_all(home).unwrap();
    }
}
