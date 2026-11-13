//! Zed reads `~/.config/zed/settings.json`. AstrLink adds an
//! `openai_compatible` provider for Chat and Responses models and an
//! `anthropic_compatible` one for Claude's, never moving Zed's own
//! providers, whose shell keys would follow. Zed reads a provider's key
//! only from the keychain, kept by `api_url`, so the user pastes the token
//! once per provider, and again after the port moves. Zed tells the model
//! only the model's name.

use std::{fs, path::PathBuf};

use serde_json::Value as Json;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        catalog::Kind,
        client::Client,
        config::Config,
        format::{json, Document, Format},
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Zed);

struct Zed;

/// `settings.json`.
struct Settings;

/// Zed's provider map and the provider id, which Zed shows.
const OPENAI: (&str, &str) = ("openai_compatible", "AstrLink");
const ANTHROPIC: (&str, &str) = ("anthropic_compatible", "AstrLink Anthropic");
const DEFAULT_KEY: &str = "agent.default_model";
/// Keys a user's settings can set `agent` and `language_models` under too.
const LAYERS: [&str; 7] = [
    "stable", "preview", "nightly", "dev", "macos", "linux", "windows",
];
/// What Zed assumes of a model it is told no limits of.
const DEFAULT_CONTEXT: u64 = 128_000;
const ANTHROPIC_CONTEXT: u64 = 200_000;
/// Zed's own Anthropic default is 4096.
const ANTHROPIC_OUTPUT: u64 = 32_000;

fn provider_key((map, id): (&str, &str)) -> String {
    format!("language_models.{map}.{id}")
}

fn providers() -> [(&'static str, &'static str); 2] {
    [OPENAI, ANTHROPIC]
}

fn provider_of(key: &str) -> Option<(&'static str, &'static str)> {
    providers()
        .into_iter()
        .find(|provider| provider_key(*provider) == key)
}

/// Zed adds `/v1/messages` to an Anthropic URL, and the rest to the other.
fn api_url(provider: (&str, &str), origin: &str) -> String {
    if provider == ANTHROPIC {
        origin.to_string()
    } else {
        format!("{origin}/v1")
    }
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 15] = [
        "provider",
        "model",
        "api_url",
        "available_models",
        "name",
        "max_tokens",
        "max_output_tokens",
        "capabilities",
        "tools",
        "images",
        "parallel_tool_calls",
        "prompt_cache_key",
        "chat_completions",
        "interleaved_reasoning",
        "max_tokens_parameter",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn json_at(config: &Config, key: &str) -> Option<Json> {
    serde_json::from_str(&config.get(key).flatten()?).ok()
}

fn model_entry(id: &str, protocol: &str, details: &Catalog) -> Json {
    let details = details.get(id);
    let context = details.and_then(|details| details.context_window);
    let output = details.and_then(|details| details.max_tokens);
    if protocol == "anthropic.messages" {
        return serde_json::json!({
            "name": id,
            "max_tokens": context.unwrap_or(ANTHROPIC_CONTEXT),
            "max_output_tokens": output.unwrap_or(ANTHROPIC_OUTPUT),
            "capabilities": { "tools": true, "images": false, "prompt_caching": true },
        });
    }
    // Zed has no default for each capability, so all of them are written.
    let mut entry = serde_json::json!({
        "name": id,
        "max_tokens": context.unwrap_or(DEFAULT_CONTEXT),
        "capabilities": {
            "tools": true,
            "images": false,
            "parallel_tool_calls": false,
            "prompt_cache_key": false,
            "chat_completions": protocol != "openai.responses",
            "interleaved_reasoning": false,
            "max_tokens_parameter": false,
        },
    });
    if let Some(output) = output {
        entry["max_output_tokens"] = output.into();
    }
    entry
}

fn settings_file(home: Home) -> PathBuf {
    CLIENT.dir(home, None).join("settings.json")
}

fn jsonc() -> jsonc_parser::ParseOptions {
    jsonc_parser::ParseOptions {
        allow_comments: true,
        allow_trailing_commas: true,
        ..json::strict()
    }
}

impl ClientSpec for Zed {
    fn id(&self) -> &'static str {
        "zed"
    }

    fn name(&self) -> &'static str {
        "Zed"
    }

    fn kind(&self) -> Kind {
        Kind::Paste
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `--user-data-dir` and Flatpak's directory are out of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        if home.windows() {
            home.join("AppData").join("Roaming").join("Zed")
        } else {
            home.join(".config").join("zed")
        }
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&Settings]
    }

    fn config_paths(
        &self,
        home: Home,
        _target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![settings_file(home)]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// Each provider holds an address; the token is in the keychain.
    fn holds_connection(&self, key: &str) -> bool {
        provider_of(key).is_some()
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let mut ids: Vec<String> = Vec::new();
        for provider in providers() {
            let Some(entry) = json_at(config, &provider_key(provider)) else {
                continue;
            };
            for id in entry
                .get("available_models")
                .and_then(Json::as_array)
                .into_iter()
                .flatten()
                .filter_map(|model| model.get("name")?.as_str())
                .filter_map(|id| clean_model(id.into()))
            {
                if !ids.contains(&id) {
                    ids.push(id);
                }
            }
        }
        let chosen = json_at(config, DEFAULT_KEY)
            .filter(|chosen| {
                let provider = chosen.get("provider").and_then(Json::as_str);
                providers().iter().any(|(_, id)| Some(*id) == provider)
            })
            .and_then(|chosen| chosen.get("model")?.as_str().map(str::to_string));
        CurrentModels {
            model: chosen.or_else(|| ids.first().cloned()),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let list = connection.list();
        let mut keys = Vec::new();
        for provider in providers() {
            let models: Vec<Json> = list
                .iter()
                .filter(|id| {
                    (connection.protocol(self, id) == "anthropic.messages")
                        == (provider == ANTHROPIC)
                })
                .map(|id| model_entry(id, connection.protocol(self, id), details))
                .collect();
            if models.is_empty() {
                continue;
            }
            let entry = serde_json::json!({
                "api_url": api_url(provider, connection.origin),
                "available_models": models,
            });
            keys.push((provider_key(provider), entry.to_string()));
        }
        if let Some(model) = connection.model("model") {
            let (_, provider) = if connection.protocol(self, &model) == "anthropic.messages" {
                ANTHROPIC
            } else {
                OPENAI
            };
            let chosen = serde_json::json!({ "provider": provider, "model": model });
            keys.push((DEFAULT_KEY.into(), chosen.to_string()));
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
            .filter_map(|key| Some((key, provider_of(key)?)))
            .filter_map(|(key, provider)| {
                let mut entry = json_at(config, key)?;
                entry["api_url"] = api_url(provider, origin).into();
                Some((key.clone(), entry.to_string()))
            })
            .collect()
    }

    /// A release channel or platform section that sets the model or
    /// providers wins over the top level.
    fn notes(
        &self,
        home: Home,
        _target: Option<&str>,
        _config: Option<&Config>,
    ) -> Vec<&'static str> {
        let options = jsonc();
        let overridden = fs::read_to_string(settings_file(home))
            .ok()
            .and_then(|text| json::JsonDocument::parse(&text, Some(&options), &[], false).ok())
            .and_then(|document| document.value())
            .is_some_and(|settings| {
                LAYERS
                    .iter()
                    .filter_map(|layer| settings.get(layer))
                    .any(|layer| {
                        layer.get("language_models").is_some()
                            || layer
                                .get("agent")
                                .and_then(|agent| agent.get("default_model"))
                                .is_some()
                    })
            });
        if overridden {
            vec!["overridden"]
        } else {
            Vec::new()
        }
    }

    fn keeps_token_by_address(&self) -> bool {
        true
    }

    fn paste_into(&self, config: &Config) -> Vec<String> {
        providers()
            .into_iter()
            .filter(|provider| json_at(config, &provider_key(*provider)).is_some())
            .map(|(_, id)| id.to_string())
            .collect()
    }
}

impl ConfigFile for Settings {
    /// Zed keeps comments when it edits the file itself.
    fn format(&self) -> Format {
        Format::Json {
            syntax: Some(jsonc()),
            objects: &["language_models", "agent"],
            root_list: false,
        }
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Json(json) = document else {
            return None;
        };
        if provider_of(key).is_some() || key == DEFAULT_KEY {
            return json
                .get_json(key)
                .map(|value| value.map(|value| value.to_string()));
        }
        document.get(key)
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Json(json) = document else {
            return document.set(key, value);
        };
        if provider_of(key).is_some() || key == DEFAULT_KEY {
            let value: Json = serde_json::from_str(value).map_err(|error| error.to_string())?;
            return json.set_json(key, &value, rank);
        }
        json.set(key, value)
    }

    fn emptied(&self) -> &'static [&'static str] {
        &[
            "language_models.openai_compatible",
            "language_models.anthropic_compatible",
            "language_models",
            "agent",
        ]
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn file(home: &Path) -> PathBuf {
        ZED.dir(Home::local(home), None).join("settings.json")
    }

    fn read(home: &Path) -> serde_json::Value {
        read_json(ZED.files()[0], &fs::read_to_string(file(home)).unwrap()).unwrap()
    }

    #[test]
    fn writes_a_provider_per_kind_and_asks_for_a_paste_per_address() {
        let home = unique_home("zed");
        let local = Home::local(&home);
        fs::create_dir_all(ZED.dir(local, None)).unwrap();
        let models = vec![("model", "gpt-5.5".to_string())];
        let list = strings(&["claude-sonnet-5", "gpt-5.5", "deepseek-v4"]);
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
            "gpt-5.5".to_string(),
            ModelDetails {
                context_window: Some(400_000),
                max_tokens: Some(128_000),
                ..Default::default()
            },
        )]);
        let outcome = write_with(local, ZED, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        let capabilities = |chat: bool| {
            serde_json::json!({
                "tools": true, "images": false, "parallel_tool_calls": false, "prompt_cache_key": false,
                "chat_completions": chat, "interleaved_reasoning": false, "max_tokens_parameter": false,
            })
        };
        assert_eq!(
            read(&home),
            serde_json::json!({
                "language_models": {
                    "openai_compatible": {"AstrLink": {
                        "api_url": format!("{ORIGIN}/v1"),
                        "available_models": [
                            {"name": "gpt-5.5", "max_tokens": 400_000, "max_output_tokens": 128_000,
                             "capabilities": capabilities(false)},
                            {"name": "deepseek-v4", "max_tokens": 128_000, "capabilities": capabilities(true)},
                        ],
                    }},
                    "anthropic_compatible": {"AstrLink Anthropic": {
                        "api_url": ORIGIN,
                        "available_models": [
                            {"name": "claude-sonnet-5", "max_tokens": 200_000, "max_output_tokens": 32_000,
                             "capabilities": {"tools": true, "images": false, "prompt_caching": true}},
                        ],
                    }},
                },
                "agent": {"default_model": {"provider": "AstrLink", "model": "gpt-5.5"}},
            })
        );
        let status = &status_of(local, [ZED], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("gpt-5.5"));
        assert_eq!(status.paste_into, ["AstrLink", "AstrLink Anthropic"]);
        let since = status.paste_since_unix.unwrap();

        // Another model keeps the pasted token; another port does not.
        let models = vec![("model", "deepseek-v4".to_string())];
        let again = Connection {
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        write_with(local, ZED, &again, &details, &tokens(), false).unwrap();
        let status = &status_of(local, [ZED], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.paste_since_unix, Some(since));
        let place = Place::new(ZED, None).unwrap();
        let mut records = read_records(&home).unwrap();
        let mut record = records.get(place).unwrap().clone();
        record.paste_since_unix = Some(1);
        records.set(place, Some(record));
        write_records(&home, &mut records).unwrap();
        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = read(&home);
        assert_eq!(
            synced["language_models"]["openai_compatible"]["AstrLink"]["api_url"],
            "http://127.0.0.1:9000/v1"
        );
        assert_eq!(
            synced["language_models"]["anthropic_compatible"]["AstrLink Anthropic"]["api_url"],
            "http://127.0.0.1:9000"
        );
        let status =
            &status_of(local, [ZED], Some("http://127.0.0.1:9000"), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert!(status.paste_since_unix.is_some_and(|after| after > 1));

        remove(local, ZED).unwrap();
        assert!(!file(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = "// Zed settings\n{\n  \"theme\": \"One Dark\",\n  \"language_models\": {\n    \"openai\": { \"api_url\": \"https://api.openai.com/v1\" },\n  },\n  \"agent\": {\n    \"default_model\": { \"provider\": \"zed.dev\", \"model\": \"claude-sonnet-4\" },\n  },\n  \"stable\": { \"agent\": { \"default_model\": { \"provider\": \"openai\", \"model\": \"gpt-5\" } } },\n}\n";

    #[test]
    fn leaves_zeds_providers_and_puts_the_model_back() {
        let home = unique_home("zed-merge");
        let local = Home::local(&home);
        fs::create_dir_all(ZED.dir(local, None)).unwrap();
        fs::write(file(&home), USER).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, ZED, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["agent.default_model"]), None))
        );
        write(local, ZED, &connection(&models), &tokens(), true).unwrap();
        let written = read(&home);
        assert_eq!(
            written["language_models"]["openai"]["api_url"],
            "https://api.openai.com/v1"
        );
        assert_eq!(written["agent"]["default_model"]["provider"], "AstrLink");
        let status = &status_of(local, [ZED], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.notes, ["overridden"]);
        assert_eq!(status.paste_into, ["AstrLink"]);

        remove(local, ZED).unwrap();
        let restored = fs::read_to_string(file(&home)).unwrap();
        assert!(restored.starts_with("// Zed settings"), "{restored}");
        assert_eq!(read(&home), read_json(ZED.files()[0], USER).unwrap());
        fs::remove_dir_all(home).unwrap();
    }
}
