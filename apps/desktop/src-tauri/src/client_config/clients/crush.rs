//! Crush reads `~/.config/crush/crush.json`. A provider has one `type` and
//! one `base_url`, and Anthropic's takes no `/v1`, so AstrLink adds a
//! provider per protocol its models use, each listing its models, and picks
//! the large and small models. A model picked in Crush is kept in its data
//! file, which overrides this one.

use std::{fs, path::PathBuf};

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

pub(crate) const CLIENT: Client = Client::new(&Crush);

struct Crush;

/// `crush.json`.
struct CrushConfig;

/// Protocol, provider id, and `type`.
const ROUTES: [(&str, &str, &str); 3] = [
    ("openai.chat", "local-chat", "completions"),
    ("openai.responses", "local-responses", "responses"),
    ("anthropic.messages", "local-anthropic", "messages"),
];
const PROVIDER_PREFIX: &str = "providers.";
const LARGE_KEY: &str = "models.large";
const SMALL_KEY: &str = "models.small";
const SCHEMA: &str = "https://charm.land/crush.json";
/// What Crush's schema requires for a model AstrLink knows no limits of.
const DEFAULT_CONTEXT: u64 = 128_000;
const DEFAULT_OUTPUT: u64 = 16_000;

fn provider_key(id: &str) -> String {
    format!("{PROVIDER_PREFIX}{id}")
}

fn route_of(protocol: &str) -> (&'static str, &'static str, &'static str) {
    ROUTES
        .into_iter()
        .find(|(known, ..)| *known == protocol)
        .unwrap_or(ROUTES[0])
}

fn ours(id: &str) -> bool {
    ROUTES.iter().any(|(_, known, _)| *known == id)
}

/// Anthropic's SDK adds `/v1` itself; the OpenAI ones take it.
fn provider_url(kind: &str, origin: &str) -> String {
    if kind == "messages" {
        origin.to_string()
    } else {
        format!("{origin}/v1")
    }
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 9] = [
        "id",
        "name",
        "type",
        "base_url",
        "api_key",
        "context_window",
        "default_max_tokens",
        "models",
        "provider",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn json_at(config: &Config, key: &str) -> Option<Json> {
    serde_json::from_str(&config.get(key).flatten()?).ok()
}

/// Crush's data file, where a model picked in Crush is kept.
fn data_file(home: Home) -> PathBuf {
    if home.windows() {
        home.join("AppData")
            .join("Local")
            .join("crush")
            .join("crush.json")
    } else {
        home.join(".local")
            .join("share")
            .join("crush")
            .join("crush.json")
    }
}

impl ClientSpec for Crush {
    fn id(&self) -> &'static str {
        "crush"
    }

    fn name(&self) -> &'static str {
        "Crush"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `XDG_CONFIG_HOME` and `CRUSH_GLOBAL_CONFIG` from a shell are out of
    /// reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".config").join("crush")
    }

    /// A first run makes the data directory, not the config one.
    fn detected(&self, home: Home, target: Option<&str>) -> bool {
        self.dir(home, target).is_dir() || data_file(home).parent().is_some_and(|dir| dir.is_dir())
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&CrushConfig]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self.dir(home, target).join("crush.json")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// Each provider holds an address and the token.
    fn holds_connection(&self, key: &str) -> bool {
        key.strip_prefix(PROVIDER_PREFIX).is_some_and(ours)
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let mut ids: Vec<String> = Vec::new();
        for (_, provider, _) in ROUTES {
            let Some(entry) = json_at(config, &provider_key(provider)) else {
                continue;
            };
            for id in entry
                .get("models")
                .and_then(Json::as_array)
                .into_iter()
                .flatten()
                .filter_map(|model| model.get("id")?.as_str())
                .filter_map(|id| clean_model(id.into()))
            {
                if !ids.contains(&id) {
                    ids.push(id);
                }
            }
        }
        let chosen = json_at(config, LARGE_KEY)
            .filter(|large| {
                large
                    .get("provider")
                    .and_then(Json::as_str)
                    .is_some_and(ours)
            })
            .and_then(|large| large.get("model")?.as_str().map(str::to_string));
        CurrentModels {
            model: chosen.or_else(|| ids.first().cloned()),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let list = connection.list();
        let mut keys = Vec::new();
        for (protocol, id, kind) in ROUTES {
            let models: Vec<Json> = list
                .iter()
                .filter(|model| connection.protocol(self, model) == protocol)
                .map(|model| {
                    let details = details.get(model.as_str());
                    serde_json::json!({
                        "id": model,
                        "name": model,
                        "context_window": details
                            .and_then(|details| details.context_window)
                            .unwrap_or(DEFAULT_CONTEXT),
                        "default_max_tokens": details
                            .and_then(|details| details.max_tokens)
                            .unwrap_or(DEFAULT_OUTPUT),
                    })
                })
                .collect();
            if models.is_empty() {
                continue;
            }
            let provider = serde_json::json!({
                "name": "AstrLink",
                "type": kind,
                "base_url": provider_url(kind, connection.origin),
                "api_key": connection.token,
                "models": models,
            });
            keys.push((provider_key(id), provider.to_string()));
        }
        if let Some(model) = connection.model("model") {
            let (_, provider, _) = route_of(connection.protocol(self, &model));
            let picked = serde_json::json!({ "provider": provider, "model": model }).to_string();
            keys.push((LARGE_KEY.into(), picked.clone()));
            keys.push((SMALL_KEY.into(), picked));
        }
        keys
    }

    fn start_file(&self, config: &mut Config) -> Result<(), FileError> {
        config.set("$schema", SCHEMA)
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
                let kind = provider.get("type")?.as_str()?.to_string();
                provider["base_url"] = provider_url(&kind, origin).into();
                Some((key.clone(), provider.to_string()))
            })
            .collect()
    }

    /// A large model picked in Crush, other than one of AstrLink's, wins.
    fn notes(
        &self,
        home: Home,
        _target: Option<&str>,
        _config: Option<&Config>,
    ) -> Vec<&'static str> {
        let picked = fs::read_to_string(data_file(home))
            .ok()
            .and_then(|text| serde_json::from_str::<Json>(&text).ok())
            .and_then(|data| {
                data.get("models")?
                    .get("large")?
                    .get("provider")?
                    .as_str()
                    .map(str::to_string)
            });
        match picked {
            Some(provider) if !ours(&provider) => vec!["overridden"],
            _ => Vec::new(),
        }
    }
}

impl ConfigFile for CrushConfig {
    fn format(&self) -> Format {
        Format::Json {
            syntax: Some(jsonc_parser::ParseOptions {
                allow_comments: true,
                ..json::strict()
            }),
            objects: &["providers", "models"],
            root_list: false,
        }
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Json(json) = document else {
            return None;
        };
        if key.starts_with(PROVIDER_PREFIX) || key == LARGE_KEY || key == SMALL_KEY {
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
        if key.starts_with(PROVIDER_PREFIX) || key == LARGE_KEY || key == SMALL_KEY {
            let value: Json = serde_json::from_str(value).map_err(|error| error.to_string())?;
            return json.set_json(key, &value, rank);
        }
        json.set(key, value)
    }

    fn emptied(&self) -> &'static [&'static str] {
        &["providers", "models"]
    }

    /// The schema AstrLink named in a file it created goes with the rest.
    fn is_empty(&self, document: &Document) -> bool {
        match document {
            Document::Json(json) => json.holds_only(|name, _| name == Some("$schema")),
            _ => document.is_empty(),
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn config(home: &Path) -> PathBuf {
        home.join(".config/crush/crush.json")
    }

    fn read(home: &Path) -> serde_json::Value {
        read_json(CRUSH.files()[0], &fs::read_to_string(config(home)).unwrap()).unwrap()
    }

    #[test]
    fn writes_a_provider_per_protocol_and_the_large_and_small_models() {
        let home = unique_home("crush");
        let local = Home::local(&home);
        fs::create_dir_all(super::data_file(local).parent().unwrap()).unwrap();
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
                ..Default::default()
            },
        )]);
        let outcome = write_with(local, CRUSH, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        let picked = serde_json::json!({"provider": "local-anthropic", "model": "claude-sonnet-5"});
        assert_eq!(
            read(&home),
            serde_json::json!({
                "$schema": "https://charm.land/crush.json",
                "providers": {
                    "local-chat": {
                        "name": "AstrLink", "type": "completions", "base_url": format!("{ORIGIN}/v1"), "api_key": TOKEN,
                        "models": [{"id": "deepseek-v4", "name": "deepseek-v4", "context_window": 128_000, "default_max_tokens": 16_000}],
                    },
                    "local-anthropic": {
                        "name": "AstrLink", "type": "messages", "base_url": ORIGIN, "api_key": TOKEN,
                        "models": [{"id": "claude-sonnet-5", "name": "claude-sonnet-5", "context_window": 200_000, "default_max_tokens": 64_000}],
                    },
                },
                "models": {"large": picked, "small": picked},
            })
        );
        let status = &status_of(local, [CRUSH], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-sonnet-5"));
        assert!(status.notes.is_empty());

        fs::write(
            super::data_file(local),
            r#"{"models": {"large": {"provider": "anthropic", "model": "claude-opus-5"}}}"#,
        )
        .unwrap();
        let status = &status_of(local, [CRUSH], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.notes, ["overridden"]);

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = read(&home);
        assert_eq!(
            synced["providers"]["local-chat"]["base_url"],
            "http://127.0.0.1:9000/v1"
        );
        assert_eq!(
            synced["providers"]["local-anthropic"]["base_url"],
            "http://127.0.0.1:9000"
        );

        remove(local, CRUSH).unwrap();
        assert!(!config(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = r#"{
  "$schema": "https://charm.land/crush.json",
  "options": { "disable_provider_auto_update": true },
  "providers": {
    "deepseek": { "api_key": "sk-deep" }
  },
  "models": {
    "large": { "provider": "deepseek", "model": "deepseek-chat" }
  }
}
"#;

    #[test]
    fn keeps_the_users_providers_and_puts_their_models_back() {
        let home = unique_home("crush-merge");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".config/crush")).unwrap();
        fs::write(config(&home), USER).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, CRUSH, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["models.large"]), None))
        );
        write(local, CRUSH, &connection(&models), &tokens(), true).unwrap();
        let written = read(&home);
        assert_eq!(written["providers"]["deepseek"]["api_key"], "sk-deep");
        assert_eq!(written["models"]["large"]["provider"], "local-chat");

        remove(local, CRUSH).unwrap();
        assert_eq!(read(&home), read_json(CRUSH.files()[0], USER).unwrap());
        fs::remove_dir_all(home).unwrap();
    }
}
