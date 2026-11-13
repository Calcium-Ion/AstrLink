//! Reasonix reads `~/.reasonix/config.toml`: `[[providers]]` with one
//! `kind` each and the `default_model` it starts with. A provider's key
//! comes only from `~/.reasonix/.env`, by the variable it names. AstrLink
//! adds a provider per protocol its models use, all reading one variable,
//! and lists them in the desktop app's provider list when the user keeps
//! one.

use std::path::PathBuf;

use serde_json::Value as Json;
use toml_edit::{Array, ArrayOfTables, DocumentMut, Item};

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

pub(crate) const CLIENT: Client = Client::new(&Reasonix);

struct Reasonix;

/// `config.toml`.
struct ReasonixConfig;

/// `.env`.
struct EnvFile;

/// Protocol, provider name, and `kind`.
const ROUTES: [(&str, &str, &str); 3] = [
    ("openai.chat", "local-chat", "openai"),
    ("openai.responses", "local-responses", "responses"),
    ("anthropic.messages", "local-anthropic", "anthropic"),
];
const PROVIDERS_KEY: &str = "providers";
const DEFAULT_KEY: &str = "default_model";
/// The desktop app's model switcher shows only the providers listed here,
/// when the list is there.
const ACCESS_KEY: &str = "desktop.provider_access";
const KEY_NAME: &str = "LOCAL_GATEWAY_API_KEY";
/// What Reasonix adds to the file on its first run.
const VERSION_KEY: &str = "config_version";

fn ours(name: &str) -> bool {
    ROUTES.iter().any(|(_, known, _)| *known == name)
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 9] = [
        "name",
        "kind",
        "base_url",
        "models",
        "default",
        "api_key_env",
        "model_overrides",
        "context_window",
        "max_output_tokens",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn owned(entry: &Json) -> bool {
    entry.get("name").and_then(Json::as_str).is_some_and(ours)
}

fn providers(config: &Config) -> Vec<Json> {
    config
        .get(PROVIDERS_KEY)
        .flatten()
        .and_then(|value| serde_json::from_str::<Json>(&value).ok())
        .and_then(|value| value.as_array().cloned())
        .unwrap_or_default()
}

/// AstrLink's provider names among `names`, in its own order.
fn ours_in<'n>(names: impl Iterator<Item = &'n str>) -> Vec<&'static str> {
    let names: Vec<&str> = names.collect();
    ROUTES
        .iter()
        .map(|(_, name, _)| *name)
        .filter(|name| names.contains(name))
        .collect()
}

impl ClientSpec for Reasonix {
    fn id(&self) -> &'static str {
        "reasonix"
    }

    fn name(&self) -> &'static str {
        "Reasonix"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `REASONIX_HOME` from a shell is out of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        if home.windows() {
            home.join("AppData").join("Roaming").join("reasonix")
        } else {
            home.join(".reasonix")
        }
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&ReasonixConfig, &EnvFile]
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
        let mut ids: Vec<String> = Vec::new();
        for id in providers(config)
            .iter()
            .filter(|entry| owned(entry))
            .filter_map(|entry| entry.get("models")?.as_array())
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
        let mut entries = Vec::new();
        let mut names = Vec::new();
        let mut default = None;
        for (protocol, name, kind) in ROUTES {
            let used: Vec<&String> = list
                .iter()
                .filter(|id| connection.protocol(self, id) == protocol)
                .collect();
            let Some(first) = used.first() else {
                continue;
            };
            let start = used
                .iter()
                .find(|id| Some(id.as_str()) == chosen.as_deref())
                .unwrap_or(first);
            if Some(start.as_str()) == chosen.as_deref() {
                default = Some(format!("{name}/{start}"));
            }
            let mut entry = serde_json::json!({
                "name": name,
                "kind": kind,
                "base_url": self.base_url(connection.origin),
                "models": used,
                "default": start,
                "api_key_env": KEY_NAME,
            });
            let mut overrides = serde_json::Map::new();
            for id in &used {
                let Some(details) = details.get(id.as_str()) else {
                    continue;
                };
                let mut limits = serde_json::Map::new();
                if let Some(context) = details.context_window {
                    limits.insert("context_window".into(), context.into());
                }
                if let Some(output) = details.max_tokens {
                    limits.insert("max_output_tokens".into(), output.into());
                }
                if !limits.is_empty() {
                    overrides.insert(id.to_string(), Json::Object(limits));
                }
            }
            if !overrides.is_empty() {
                entry["model_overrides"] = Json::Object(overrides);
            }
            entries.push(entry);
            names.push(name);
        }
        let mut keys = Vec::new();
        if let Some(default) = default {
            keys.push((DEFAULT_KEY.into(), default));
        }
        if !entries.is_empty() {
            keys.push((PROVIDERS_KEY.into(), Json::Array(entries).to_string()));
            keys.push((ACCESS_KEY.into(), serde_json::json!(names).to_string()));
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
        let mut entries = providers(config);
        if entries.is_empty() {
            return Vec::new();
        }
        for entry in &mut entries {
            entry["base_url"] = self.base_url(origin).into();
        }
        vec![(PROVIDERS_KEY.into(), Json::Array(entries).to_string())]
    }
}

fn tables(toml: &DocumentMut) -> Option<&ArrayOfTables> {
    toml.get(PROVIDERS_KEY)?.as_array_of_tables()
}

fn access(toml: &DocumentMut) -> Option<&Array> {
    toml.get("desktop")?
        .as_table_like()?
        .get("provider_access")?
        .as_array()
}

fn access_mut(toml: &mut DocumentMut) -> Option<&mut Array> {
    toml.get_mut("desktop")?
        .as_table_like_mut()?
        .get_mut("provider_access")?
        .as_array_mut()
}

fn drop_ours(toml: &mut DocumentMut) {
    let Some(tables) = toml
        .get_mut(PROVIDERS_KEY)
        .and_then(Item::as_array_of_tables_mut)
    else {
        return;
    };
    tables.retain(|table| !json_of(&Item::Table(table.clone())).is_some_and(|entry| owned(&entry)));
    if tables.is_empty() {
        toml.as_table_mut().remove(PROVIDERS_KEY);
    }
}

impl ConfigFile for ReasonixConfig {
    fn format(&self) -> Format {
        Format::Toml
    }

    fn holds(&self, key: &str) -> bool {
        key != KEY_NAME
    }

    /// The providers read as AstrLink's tables. The desktop list reads as
    /// AstrLink's names in it; without one, every provider is listed.
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Toml(toml) = document else {
            return None;
        };
        let toml = &toml.document;
        match key {
            PROVIDERS_KEY => {
                let list: Vec<Json> = tables(toml)?
                    .iter()
                    .filter_map(|table| json_of(&Item::Table(table.clone())))
                    .filter(owned)
                    .collect();
                (!list.is_empty()).then(|| Some(Json::Array(list).to_string()))
            }
            ACCESS_KEY => {
                let names = match access(toml) {
                    Some(list) => ours_in(list.iter().filter_map(|name| name.as_str())),
                    None => ours_in(
                        tables(toml)
                            .into_iter()
                            .flat_map(ArrayOfTables::iter)
                            .filter_map(|table| table.get("name")?.as_str()),
                    ),
                };
                (!names.is_empty()).then(|| Some(serde_json::json!(names).to_string()))
            }
            _ => document.get(key),
        }
    }

    /// AstrLink's tables go after the user's; its names join a desktop list
    /// the user keeps.
    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        if key != PROVIDERS_KEY && key != ACCESS_KEY {
            return document.set(key, value);
        }
        let Document::Toml(toml) = document else {
            return Err(format!("{key} is not TOML"));
        };
        let toml = &mut toml.document;
        let Ok(Json::Array(list)) = serde_json::from_str::<Json>(value) else {
            return Err(format!("{key} is not a list"));
        };
        if key == ACCESS_KEY {
            let wanted: Vec<&str> = list.iter().filter_map(Json::as_str).collect();
            if let Some(names) = access_mut(toml) {
                names.retain(|name| {
                    name.as_str()
                        .map_or(true, |name| !ours(name) || wanted.contains(&name))
                });
                for name in wanted {
                    if !names.iter().any(|known| known.as_str() == Some(name)) {
                        names.push(name);
                    }
                }
            }
            return Ok(());
        }
        drop_ours(toml);
        let root = toml.as_table_mut();
        if !root.contains_key(key) {
            root.insert(key, Item::ArrayOfTables(ArrayOfTables::new()));
        }
        let tables = root
            .get_mut(key)
            .and_then(Item::as_array_of_tables_mut)
            .ok_or_else(|| format!("has a {key} value that is not a list of tables"))?;
        for entry in &list {
            tables.push(
                table_of(entry, rank).ok_or_else(|| format!("{key} holds a value TOML cannot"))?,
            );
        }
        Ok(())
    }

    fn remove(&self, document: &mut Document, key: &str) {
        if key != PROVIDERS_KEY && key != ACCESS_KEY {
            return document.remove(key);
        }
        let Document::Toml(toml) = document else {
            return;
        };
        let toml = &mut toml.document;
        if key == PROVIDERS_KEY {
            drop_ours(toml);
        } else if let Some(names) = access_mut(toml) {
            names.retain(|name| name.as_str().map_or(true, |name| !ours(name)));
        }
    }

    /// The version Reasonix stamped on a file AstrLink created goes with
    /// the rest.
    fn is_empty(&self, document: &Document) -> bool {
        match document {
            Document::Toml(toml) => toml.document.iter().all(|(name, _)| name == VERSION_KEY),
            _ => document.is_empty(),
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
        REASONIX.dir(Home::local(home), None)
    }

    #[test]
    fn writes_a_provider_per_protocol_and_the_token_to_env() {
        let home = unique_home("reasonix");
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
        let outcome = write_with(local, REASONIX, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        assert_eq!(
            fs::read_to_string(dir(&home).join("config.toml")).unwrap(),
            format!(
                r#"default_model = "local-responses/gpt-5.5"

[[providers]]
name = "local-chat"
kind = "openai"
base_url = "{ORIGIN}/v1"
models = ["deepseek-v4"]
default = "deepseek-v4"
api_key_env = "LOCAL_GATEWAY_API_KEY"

[[providers]]
name = "local-responses"
kind = "responses"
base_url = "{ORIGIN}/v1"
models = ["gpt-5.5"]
default = "gpt-5.5"
api_key_env = "LOCAL_GATEWAY_API_KEY"
model_overrides = {{ "gpt-5.5" = {{ context_window = 400000, max_output_tokens = 128000 }} }}

[[providers]]
name = "local-anthropic"
kind = "anthropic"
base_url = "{ORIGIN}/v1"
models = ["claude-sonnet-5"]
default = "claude-sonnet-5"
api_key_env = "LOCAL_GATEWAY_API_KEY"
"#
            )
        );
        assert_eq!(
            fs::read_to_string(dir(&home).join(".env")).unwrap(),
            format!("LOCAL_GATEWAY_API_KEY={TOKEN}\n")
        );
        let status = &status_of(local, [REASONIX], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("gpt-5.5"));

        // Reasonix stamps its version on the first run.
        let path = dir(&home).join("config.toml");
        let stamped = fs::read_to_string(&path).unwrap().replacen(
            "\n\n",
            "\nconfig_version = 6   # schema marker for diagnostics; old versions may ignore it\n\n",
            1,
        );
        fs::write(&path, stamped).unwrap();
        let status = &status_of(local, [REASONIX], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = fs::read_to_string(&path).unwrap();
        assert_eq!(
            synced
                .matches("base_url = \"http://127.0.0.1:9000/v1\"")
                .count(),
            3,
            "{synced}"
        );

        remove(local, REASONIX).unwrap();
        assert!(!path.exists());
        assert!(!dir(&home).join(".env").exists());
        fs::remove_dir_all(home).unwrap();
    }

    const CONFIG: &str = "# my reasonix\ndefault_model = \"deepseek/deepseek-v4-pro\"\n\n[desktop]\nprovider_access = [\"deepseek\"]\n\n[[providers]]\nname = \"deepseek\"\nkind = \"openai\"\nbase_url = \"https://api.deepseek.com/v1\"\nmodels = [\"deepseek-v4-pro\"]\napi_key_env = \"DEEPSEEK_API_KEY\"\n";
    const ENV: &str = "DEEPSEEK_API_KEY=sk-deep\n";

    #[test]
    fn joins_the_desktop_list_and_puts_the_users_choice_back() {
        let home = unique_home("reasonix-merge");
        let local = Home::local(&home);
        fs::create_dir_all(dir(&home)).unwrap();
        fs::write(dir(&home).join("config.toml"), CONFIG).unwrap();
        fs::write(dir(&home).join(".env"), ENV).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, REASONIX, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["default_model"]), None))
        );
        write(local, REASONIX, &connection(&models), &tokens(), true).unwrap();
        let written = fs::read_to_string(dir(&home).join("config.toml")).unwrap();
        assert!(
            written.starts_with("# my reasonix\ndefault_model = \"local-chat/deepseek-v4\"\n"),
            "{written}"
        );
        assert!(
            written.contains("provider_access = [\"deepseek\", \"local-chat\"]"),
            "{written}"
        );
        assert!(written.contains("name = \"deepseek\""), "{written}");
        let status = &status_of(local, [REASONIX], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        let env = fs::read_to_string(dir(&home).join(".env")).unwrap();
        assert_eq!(env, format!("{ENV}LOCAL_GATEWAY_API_KEY={TOKEN}\n"));

        remove(local, REASONIX).unwrap();
        assert_eq!(
            fs::read_to_string(dir(&home).join("config.toml")).unwrap(),
            CONFIG
        );
        assert_eq!(fs::read_to_string(dir(&home).join(".env")).unwrap(), ENV);
        fs::remove_dir_all(home).unwrap();
    }
}
