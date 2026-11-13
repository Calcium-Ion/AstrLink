//! Goose, desktop and CLI alike, reads `~/.config/goose`: a JSON file per
//! custom provider in `custom_providers`, and the provider and model it
//! starts with in `config.yaml`. AstrLink adds an `openai` engine provider
//! for Chat and Responses models and an `anthropic` one for Claude's. Goose
//! reads a provider's key only from the keychain, by the name in
//! `api_key_env`; both providers share one, so the user pastes the token
//! once. Goose picks Responses for a model by its name.

use std::path::PathBuf;

use serde_json::Value as Json;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        catalog::Kind,
        client::Client,
        config::Config,
        format::{
            yaml::{Step, YamlDocument, YamlRoot},
            Document, Format,
        },
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Goose);

struct Goose;

/// `config.yaml`.
struct GooseConfig;

/// A file in `custom_providers`, read as one key.
struct ProviderFile(Provider);

#[derive(Clone, Copy, PartialEq, Eq)]
struct Provider {
    /// Goose's provider name, the file's name.
    name: &'static str,
    engine: &'static str,
    /// What Goose shows.
    display: &'static str,
}

const OPENAI: Provider = Provider {
    name: "local_gateway",
    engine: "openai",
    display: "AstrLink",
};
const ANTHROPIC: Provider = Provider {
    name: "local_gateway_anthropic",
    engine: "anthropic",
    display: "AstrLink Anthropic",
};
const PROVIDERS: [Provider; 2] = [OPENAI, ANTHROPIC];
/// The keychain entry both providers read.
const KEY_NAME: &str = "LOCAL_GATEWAY_API_KEY";
const ACTIVE_KEY: &str = "active_provider";
const FILE_PREFIX: &str = "custom_providers.";
/// What Goose assumes of a model it is told no limit of.
const DEFAULT_CONTEXT: u64 = 128_000;

impl Provider {
    fn key(self) -> String {
        format!("{FILE_PREFIX}{}", self.name)
    }

    /// Goose adds `v1/messages` to an Anthropic URL, and the rest to the
    /// other.
    fn base_url(self, origin: &str) -> String {
        if self == ANTHROPIC {
            origin.to_string()
        } else {
            format!("{origin}/v1")
        }
    }

    fn of(protocol: &str) -> Self {
        if protocol == "anthropic.messages" {
            ANTHROPIC
        } else {
            OPENAI
        }
    }
}

fn provider_of(key: &str) -> Option<Provider> {
    PROVIDERS.into_iter().find(|provider| provider.key() == key)
}

/// `providers.<name>.<field>` in `config.yaml`.
fn choice_key(provider: Provider, field: &str) -> String {
    format!("providers.{}.{field}", provider.name)
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 11] = [
        "name",
        "engine",
        "display_name",
        "base_url",
        "api_key_env",
        "requires_auth",
        "supports_streaming",
        "dynamic_models",
        "models",
        "context_limit",
        "reasoning",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn json_at(config: &Config, key: &str) -> Option<Json> {
    serde_json::from_str(&config.get(key).flatten()?).ok()
}

impl ClientSpec for Goose {
    fn id(&self) -> &'static str {
        "goose"
    }

    fn name(&self) -> &'static str {
        "Goose"
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

    /// `GOOSE_PATH_ROOT` and `XDG_CONFIG_HOME` from a shell are out of
    /// reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        if home.windows() {
            home.join("AppData")
                .join("Roaming")
                .join("Block")
                .join("goose")
                .join("config")
        } else {
            home.join(".config").join("goose")
        }
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[
            &GooseConfig,
            &ProviderFile(OPENAI),
            &ProviderFile(ANTHROPIC),
        ]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        let dir = self.dir(home, target);
        let mut paths = vec![dir.join("config.yaml")];
        for provider in PROVIDERS {
            paths.push(
                dir.join("custom_providers")
                    .join(format!("{}.json", provider.name)),
            );
        }
        paths
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
        for provider in PROVIDERS {
            let Some(file) = json_at(config, &provider.key()) else {
                continue;
            };
            for id in file
                .get("models")
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
        let chosen = config
            .get(ACTIVE_KEY)
            .flatten()
            .and_then(|active| {
                PROVIDERS
                    .into_iter()
                    .find(|provider| provider.name == active)
            })
            .and_then(|provider| config.model(&choice_key(provider, "model")));
        CurrentModels {
            model: chosen.or_else(|| ids.first().cloned()),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let list = connection.list();
        let mut keys = Vec::new();
        for provider in PROVIDERS {
            let models: Vec<Json> = list
                .iter()
                .filter(|id| Provider::of(connection.protocol(self, id)) == provider)
                .map(|id| {
                    let details = details.get(id.as_str());
                    let mut model = serde_json::json!({
                        "name": id,
                        "context_limit": details
                            .and_then(|details| details.context_window)
                            .unwrap_or(DEFAULT_CONTEXT),
                    });
                    if details
                        .and_then(|details| details.reasoning.as_ref())
                        .is_some_and(|levels| !levels.is_empty())
                    {
                        model["reasoning"] = true.into();
                    }
                    model
                })
                .collect();
            if models.is_empty() {
                continue;
            }
            let file = serde_json::json!({
                "name": provider.name,
                "engine": provider.engine,
                "display_name": provider.display,
                "base_url": provider.base_url(connection.origin),
                "api_key_env": KEY_NAME,
                "requires_auth": true,
                "supports_streaming": true,
                "dynamic_models": false,
                "models": models,
            });
            keys.push((provider.key(), file.to_string()));
        }
        if let Some(model) = connection.model("model") {
            let provider = Provider::of(connection.protocol(self, &model));
            keys.push((ACTIVE_KEY.into(), provider.name.into()));
            keys.push((choice_key(provider, "enabled"), "true".into()));
            keys.push((choice_key(provider, "model"), model));
            keys.push((choice_key(provider, "configured"), "true".into()));
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
                let mut file = json_at(config, key)?;
                file["base_url"] = provider.base_url(origin).into();
                Some((key.clone(), file.to_string()))
            })
            .collect()
    }

    /// Either provider takes the one key, so it is pasted once.
    fn paste_into(&self, config: &Config) -> Vec<String> {
        PROVIDERS
            .into_iter()
            .find(|provider| json_at(config, &provider.key()).is_some())
            .map(|provider| provider.display.to_string())
            .into_iter()
            .collect()
    }
}

/// The YAML path of a dotted key.
fn steps(key: &str) -> Vec<Step<'_>> {
    key.split('.').map(Step::Key).collect()
}

fn is_switch(key: &str) -> bool {
    key.starts_with("providers.") && (key.ends_with(".enabled") || key.ends_with(".configured"))
}

impl ConfigFile for GooseConfig {
    fn format(&self) -> Format {
        Format::Yaml {
            root: YamlRoot::Mapping,
        }
    }

    fn holds(&self, key: &str) -> bool {
        !key.starts_with(FILE_PREFIX)
    }

    /// A switch reads as `true` or `false`.
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        match document {
            Document::Yaml(yaml) if is_switch(key) => yaml
                .get_json(&steps(key))
                .map(|value| value.as_bool().map(|on| on.to_string())),
            _ => document.get(key),
        }
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        match document {
            Document::Yaml(yaml) if is_switch(key) => {
                yaml.set_key_json(key, &Json::Bool(value == "true"))
            }
            _ => document.set(key, value),
        }
    }

    /// A provider's emptied block goes, then the emptied `providers`.
    fn remove(&self, document: &mut Document, key: &str) {
        document.remove(key);
        let Document::Yaml(yaml) = document else {
            return;
        };
        let emptied = |yaml: &YamlDocument, path: &[Step]| {
            yaml.get_json(path)
                .is_some_and(|value| value.as_object().is_some_and(serde_json::Map::is_empty))
        };
        for provider in PROVIDERS {
            let path = [Step::Key("providers"), Step::Key(provider.name)];
            if emptied(yaml, &path) {
                let _ = yaml.remove(&path);
            }
        }
        if emptied(yaml, &[Step::Key("providers")]) {
            let _ = yaml.remove(&[Step::Key("providers")]);
        }
    }
}

impl ConfigFile for ProviderFile {
    /// A file that fails to read keeps Goose from loading every custom
    /// provider.
    fn format(&self) -> Format {
        Format::Json {
            syntax: None,
            objects: &[],
            root_list: false,
        }
    }

    fn holds(&self, key: &str) -> bool {
        key == self.0.key()
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Json(json) = document else {
            return None;
        };
        if key != self.0.key() || json.is_empty() {
            return None;
        }
        Some(json.value().map(|value| value.to_string()))
    }

    /// The file becomes the value, property by property.
    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Json(json) = document else {
            return document.set(key, value);
        };
        let Ok(Json::Object(file)) = serde_json::from_str::<Json>(value) else {
            return Err(format!("{key} is not an object"));
        };
        let stale: Vec<String> = json
            .value()
            .and_then(|value| {
                value
                    .as_object()
                    .map(|object| object.keys().cloned().collect())
            })
            .unwrap_or_default();
        for name in stale.iter().filter(|name| !file.contains_key(*name)) {
            json.remove(name);
        }
        for (name, value) in &file {
            json.set_json(name, value, rank)?;
        }
        Ok(())
    }

    fn remove(&self, document: &mut Document, _key: &str) {
        let Document::Json(json) = document else {
            return;
        };
        let names: Vec<String> = json
            .value()
            .and_then(|value| {
                value
                    .as_object()
                    .map(|object| object.keys().cloned().collect())
            })
            .unwrap_or_default();
        for name in names {
            json.remove(&name);
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn dir(home: &Path) -> PathBuf {
        GOOSE.dir(Home::local(home), None)
    }

    fn provider(home: &Path, name: &str) -> serde_json::Value {
        let text = fs::read_to_string(
            dir(home)
                .join("custom_providers")
                .join(format!("{name}.json")),
        )
        .unwrap();
        serde_json::from_str(&text).unwrap()
    }

    #[test]
    fn writes_a_provider_per_engine_and_the_active_one() {
        let home = unique_home("goose");
        let local = Home::local(&home);
        fs::create_dir_all(dir(&home)).unwrap();
        let models = vec![("model", "claude-sonnet-5".to_string())];
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
                reasoning: Some(levels(&["low", "high"])),
                context_window: Some(400_000),
                max_tokens: Some(128_000),
            },
        )]);
        let outcome = write_with(local, GOOSE, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        let shared = |name: &str,
                      engine: &str,
                      display: &str,
                      url: String,
                      models: serde_json::Value| {
            serde_json::json!({
                "name": name, "engine": engine, "display_name": display, "base_url": url,
                "api_key_env": "LOCAL_GATEWAY_API_KEY", "requires_auth": true, "supports_streaming": true,
                "dynamic_models": false, "models": models,
            })
        };
        assert_eq!(
            provider(&home, "local_gateway"),
            shared(
                "local_gateway",
                "openai",
                "AstrLink",
                format!("{ORIGIN}/v1"),
                serde_json::json!([
                    {"name": "gpt-5.5", "context_limit": 400_000, "reasoning": true},
                    {"name": "deepseek-v4", "context_limit": 128_000},
                ]),
            )
        );
        assert_eq!(
            provider(&home, "local_gateway_anthropic"),
            shared(
                "local_gateway_anthropic",
                "anthropic",
                "AstrLink Anthropic",
                ORIGIN.to_string(),
                serde_json::json!([{"name": "claude-sonnet-5", "context_limit": 128_000}]),
            )
        );
        assert_eq!(
            fs::read_to_string(dir(&home).join("config.yaml")).unwrap(),
            "active_provider: local_gateway_anthropic\nproviders:\n  local_gateway_anthropic:\n    enabled: true\n    model: claude-sonnet-5\n    configured: true\n"
        );
        let status = &status_of(local, [GOOSE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-sonnet-5"));
        assert_eq!(status.paste_into, ["AstrLink"]);
        let since = status.paste_since_unix.unwrap();

        // The keychain entry does not name the port.
        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert_eq!(
            provider(&home, "local_gateway")["base_url"],
            "http://127.0.0.1:9000/v1"
        );
        assert_eq!(
            provider(&home, "local_gateway_anthropic")["base_url"],
            "http://127.0.0.1:9000"
        );

        // Without a Claude model, its provider file goes; an empty one
        // would keep Goose from loading any.
        let models = vec![("model", "deepseek-v4".to_string())];
        let list = strings(&["gpt-5.5", "deepseek-v4"]);
        let fewer = Connection {
            origin: "http://127.0.0.1:9000",
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        write_with(local, GOOSE, &fewer, &details, &tokens(), false).unwrap();
        assert!(!dir(&home)
            .join("custom_providers/local_gateway_anthropic.json")
            .exists());
        assert_eq!(
            fs::read_to_string(dir(&home).join("config.yaml")).unwrap(),
            "active_provider: local_gateway\nproviders:\n  local_gateway:\n    enabled: true\n    model: deepseek-v4\n    configured: true\n"
        );
        let status = &status_of(
            local,
            [GOOSE],
            Some("http://127.0.0.1:9000"),
            Some(&tokens()),
        )
        .unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.paste_since_unix, Some(since));

        remove(local, GOOSE).unwrap();
        assert!(!dir(&home).join("config.yaml").exists());
        assert!(!dir(&home)
            .join("custom_providers/local_gateway.json")
            .exists());
        assert!(!dir(&home)
            .join("custom_providers/local_gateway_anthropic.json")
            .exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = "# my goose\nactive_provider: openai\nproviders:\n  openai:\n    enabled: true\n    model: gpt-5\n    configured: true\nextensions:\n  developer:\n    enabled: true\n";

    #[test]
    fn adds_its_provider_beside_the_users_and_puts_theirs_back() {
        let home = unique_home("goose-merge");
        let local = Home::local(&home);
        fs::create_dir_all(dir(&home)).unwrap();
        fs::write(dir(&home).join("config.yaml"), USER).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, GOOSE, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["active_provider"]), None))
        );
        write(local, GOOSE, &connection(&models), &tokens(), true).unwrap();
        let written = fs::read_to_string(dir(&home).join("config.yaml")).unwrap();
        assert!(
            written.starts_with("# my goose\nactive_provider: local_gateway\n"),
            "{written}"
        );
        assert!(
            written.contains("  local_gateway:\n    enabled: true\n"),
            "{written}"
        );

        remove(local, GOOSE).unwrap();
        assert_eq!(
            fs::read_to_string(dir(&home).join("config.yaml")).unwrap(),
            USER
        );
        assert!(!dir(&home)
            .join("custom_providers/local_gateway.json")
            .exists());
        fs::remove_dir_all(home).unwrap();
    }
}
