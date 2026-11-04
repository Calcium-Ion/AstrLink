//! Mistral Vibe reads `~/.vibe/config.toml`: `[[providers]]` with one
//! `api_style` each, `[[models]]` naming their provider, and the
//! `active_model` alias. A provider's key comes from the environment
//! variable it names, which Vibe fills from `~/.vibe/.env` before the
//! keychain, so the token goes there. AstrLink adds a provider per protocol
//! its models use, all reading one variable.

use std::path::PathBuf;

use serde_json::Value as Json;
use toml_edit::{ArrayOfTables, Item};

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{
            toml::{json_of, table_of},
            Document, Format,
        },
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&MistralVibe);

struct MistralVibe;

/// `config.toml`.
struct VibeConfig;

/// `.env`.
struct EnvFile;

/// Protocol, provider name, and `api_style`.
const ROUTES: [(&str, &str, &str); 3] = [
    ("openai.chat", "local-chat", "openai"),
    ("openai.responses", "local-responses", "openai-responses"),
    ("anthropic.messages", "local-anthropic", "anthropic"),
];
const PROVIDERS_KEY: &str = "providers";
const MODELS_KEY: &str = "models";
const ACTIVE_KEY: &str = "active_model";
const KEY_NAME: &str = "LOCAL_GATEWAY_API_KEY";

fn ours(name: &str) -> bool {
    ROUTES.iter().any(|(_, known, _)| *known == name)
}

/// Vibe adds `/v1/messages` to an Anthropic base, and the rest to the
/// others.
fn api_base(style: &str, origin: &str) -> String {
    if style == "anthropic" {
        origin.to_string()
    } else {
        format!("{origin}/v1")
    }
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 10] = [
        "name",
        "provider",
        "alias",
        "api_base",
        "api_key_env_var",
        "api_style",
        "backend",
        "max_context_length",
        "max_output_tokens",
        "thinking",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

/// Which field of an entry in `list` names AstrLink's provider.
fn owned_in(list: &str) -> impl Fn(&Json) -> bool {
    let field = if list == PROVIDERS_KEY {
        "name"
    } else {
        "provider"
    };
    move |entry| entry.get(field).and_then(Json::as_str).is_some_and(ours)
}

fn entries(config: &Config, list: &str) -> Vec<Json> {
    config
        .get(list)
        .flatten()
        .and_then(|value| serde_json::from_str::<Json>(&value).ok())
        .and_then(|value| value.as_array().cloned())
        .unwrap_or_default()
}

impl ClientSpec for MistralVibe {
    fn id(&self) -> &'static str {
        "mistral_vibe"
    }

    fn name(&self) -> &'static str {
        "Mistral Vibe"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `VIBE_HOME` from a shell is out of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".vibe")
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&VibeConfig, &EnvFile]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        let dir = self.dir(home, target);
        vec![dir.join("config.toml"), dir.join(".env")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[KEY_NAME]
    }

    /// The providers hold the addresses; `.env` the token.
    fn holds_connection(&self, key: &str) -> bool {
        key == PROVIDERS_KEY || key == KEY_NAME
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let models = entries(config, MODELS_KEY);
        let mut ids: Vec<String> = Vec::new();
        for id in models
            .iter()
            .filter_map(|entry| entry.get("name")?.as_str())
            .filter_map(|id| clean_model(id.into()))
        {
            if !ids.contains(&id) {
                ids.push(id);
            }
        }
        let chosen = config.get(ACTIVE_KEY).flatten().and_then(|alias| {
            let entry = models.iter().find(|entry| {
                entry
                    .get("alias")
                    .or_else(|| entry.get("name"))
                    .and_then(Json::as_str)
                    == Some(alias.as_str())
            })?;
            entry.get("name")?.as_str().map(str::to_string)
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
        for (protocol, name, style) in ROUTES {
            let used: Vec<&String> = list
                .iter()
                .filter(|id| connection.protocol(self, id) == protocol)
                .collect();
            if used.is_empty() {
                continue;
            }
            providers.push(serde_json::json!({
                "name": name,
                "api_base": api_base(style, connection.origin),
                "api_key_env_var": KEY_NAME,
                "api_style": style,
                "backend": "generic",
            }));
            for id in used {
                let mut model = serde_json::json!({ "name": id, "provider": name, "alias": id });
                if let Some(details) = details.get(id.as_str()) {
                    if let Some(context) = details.context_window {
                        model["max_context_length"] = context.into();
                    }
                    if let Some(output) = details.max_tokens {
                        model["max_output_tokens"] = output.into();
                    }
                }
                models.push(model);
            }
        }
        let mut keys = Vec::new();
        if let Some(model) = connection.model("model") {
            keys.push((ACTIVE_KEY.into(), model));
        }
        if !providers.is_empty() {
            keys.push((PROVIDERS_KEY.into(), Json::Array(providers).to_string()));
            keys.push((MODELS_KEY.into(), Json::Array(models).to_string()));
        }
        keys.push((KEY_NAME.into(), connection.token.into()));
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
        let mut providers = entries(config, PROVIDERS_KEY);
        if providers.is_empty() {
            return Vec::new();
        }
        for provider in &mut providers {
            let style = provider
                .get("api_style")
                .and_then(Json::as_str)
                .unwrap_or_default()
                .to_string();
            provider["api_base"] = api_base(&style, origin).into();
        }
        vec![(PROVIDERS_KEY.into(), Json::Array(providers).to_string())]
    }
}

/// The `[[list]]` tables, when `list` is one.
fn tables<'d>(document: &'d Document, list: &str) -> Option<&'d ArrayOfTables> {
    let Document::Toml(toml) = document else {
        return None;
    };
    toml.document.get(list)?.as_array_of_tables()
}

impl ConfigFile for VibeConfig {
    fn format(&self) -> Format {
        Format::Toml
    }

    fn holds(&self, key: &str) -> bool {
        key != KEY_NAME
    }

    /// A list reads as AstrLink's tables in it.
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        if key != PROVIDERS_KEY && key != MODELS_KEY {
            return document.get(key);
        }
        let owned = owned_in(key);
        let list: Vec<Json> = tables(document, key)?
            .iter()
            .filter_map(|table| json_of(&Item::Table(table.clone())))
            .filter(|entry| owned(entry))
            .collect();
        (!list.is_empty()).then(|| Some(Json::Array(list).to_string()))
    }

    /// AstrLink's tables go after the user's.
    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        if key != PROVIDERS_KEY && key != MODELS_KEY {
            return document.set(key, value);
        }
        let Document::Toml(toml) = document else {
            return Err(format!("{key} is not TOML"));
        };
        let Ok(Json::Array(list)) = serde_json::from_str::<Json>(value) else {
            return Err(format!("{key} is not a list"));
        };
        let root = toml.document.as_table_mut();
        if !root.contains_key(key) {
            root.insert(key, Item::ArrayOfTables(ArrayOfTables::new()));
        }
        let tables = root
            .get_mut(key)
            .and_then(Item::as_array_of_tables_mut)
            .ok_or_else(|| format!("has a {key} value that is not a list of tables"))?;
        let owned = owned_in(key);
        tables.retain(|table| {
            !json_of(&Item::Table(table.clone())).is_some_and(|entry| owned(&entry))
        });
        for entry in &list {
            tables.push(
                table_of(entry, rank).ok_or_else(|| format!("{key} holds a value TOML cannot"))?,
            );
        }
        Ok(())
    }

    fn remove(&self, document: &mut Document, key: &str) {
        if key != PROVIDERS_KEY && key != MODELS_KEY {
            return document.remove(key);
        }
        let Document::Toml(toml) = document else {
            return;
        };
        let root = toml.document.as_table_mut();
        let Some(tables) = root.get_mut(key).and_then(Item::as_array_of_tables_mut) else {
            return;
        };
        let owned = owned_in(key);
        tables.retain(|table| {
            !json_of(&Item::Table(table.clone())).is_some_and(|entry| owned(&entry))
        });
        if tables.is_empty() {
            root.remove(key);
        }
    }
}

impl ConfigFile for EnvFile {
    fn format(&self) -> Format {
        Format::Env { single_line: &[] }
    }

    fn secret(&self) -> bool {
        true
    }

    fn holds(&self, key: &str) -> bool {
        key == KEY_NAME
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn dir(home: &Path) -> PathBuf {
        home.join(".vibe")
    }

    #[test]
    fn writes_a_provider_per_protocol_and_the_token_to_env() {
        let home = unique_home("vibe");
        let local = Home::local(&home);
        fs::create_dir_all(dir(&home)).unwrap();
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
        let outcome = write_with(local, MISTRAL_VIBE, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        assert_eq!(
            fs::read_to_string(dir(&home).join("config.toml")).unwrap(),
            format!(
                r#"active_model = "gpt-5.5"

[[providers]]
name = "local-chat"
api_base = "{ORIGIN}/v1"
api_key_env_var = "LOCAL_GATEWAY_API_KEY"
api_style = "openai"
backend = "generic"

[[providers]]
name = "local-responses"
api_base = "{ORIGIN}/v1"
api_key_env_var = "LOCAL_GATEWAY_API_KEY"
api_style = "openai-responses"
backend = "generic"

[[providers]]
name = "local-anthropic"
api_base = "{ORIGIN}"
api_key_env_var = "LOCAL_GATEWAY_API_KEY"
api_style = "anthropic"
backend = "generic"

[[models]]
name = "deepseek-v4"
provider = "local-chat"
alias = "deepseek-v4"

[[models]]
name = "gpt-5.5"
provider = "local-responses"
alias = "gpt-5.5"
max_context_length = 400000
max_output_tokens = 128000

[[models]]
name = "claude-sonnet-5"
provider = "local-anthropic"
alias = "claude-sonnet-5"
"#
            )
        );
        assert_eq!(
            fs::read_to_string(dir(&home).join(".env")).unwrap(),
            format!("LOCAL_GATEWAY_API_KEY={TOKEN}\n")
        );
        let status = &status_of(local, [MISTRAL_VIBE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("gpt-5.5"));

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = fs::read_to_string(dir(&home).join("config.toml")).unwrap();
        assert!(
            synced.contains("api_base = \"http://127.0.0.1:9000/v1\""),
            "{synced}"
        );
        assert!(
            synced.contains("api_base = \"http://127.0.0.1:9000\"\n"),
            "{synced}"
        );

        remove(local, MISTRAL_VIBE).unwrap();
        assert!(!dir(&home).join("config.toml").exists());
        assert!(!dir(&home).join(".env").exists());
        fs::remove_dir_all(home).unwrap();
    }

    const CONFIG: &str = "# my vibe\nactive_model = \"devstral\"\n\n[[providers]]\nname = \"mistral\"\napi_base = \"https://api.mistral.ai/v1\"\napi_key_env_var = \"MISTRAL_API_KEY\"\n\n[[models]]\nname = \"devstral-2\"\nprovider = \"mistral\"\nalias = \"devstral\"\n";
    const ENV: &str = "MISTRAL_API_KEY=sk-mistral\n";

    #[test]
    fn keeps_the_users_providers_and_puts_their_model_back() {
        let home = unique_home("vibe-merge");
        let local = Home::local(&home);
        fs::create_dir_all(dir(&home)).unwrap();
        fs::write(dir(&home).join("config.toml"), CONFIG).unwrap();
        fs::write(dir(&home).join(".env"), ENV).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, MISTRAL_VIBE, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["active_model"]), None))
        );
        write(local, MISTRAL_VIBE, &connection(&models), &tokens(), true).unwrap();
        let written = fs::read_to_string(dir(&home).join("config.toml")).unwrap();
        assert!(
            written.starts_with("# my vibe\nactive_model = \"deepseek-v4\"\n"),
            "{written}"
        );
        assert!(written.contains("name = \"mistral\""), "{written}");
        assert!(written.contains("name = \"local-chat\""), "{written}");
        let env = fs::read_to_string(dir(&home).join(".env")).unwrap();
        assert_eq!(env, format!("{ENV}LOCAL_GATEWAY_API_KEY={TOKEN}\n"));

        remove(local, MISTRAL_VIBE).unwrap();
        assert_eq!(
            fs::read_to_string(dir(&home).join("config.toml")).unwrap(),
            CONFIG
        );
        assert_eq!(fs::read_to_string(dir(&home).join(".env")).unwrap(), ENV);
        fs::remove_dir_all(home).unwrap();
    }
}
