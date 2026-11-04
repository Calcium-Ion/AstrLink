//! Grok Build reads `~/.grok/config.toml`. AstrLink adds a
//! `[model."<model>"]` table for each model, each with its own address and
//! token, since a model without a key is sent the xAI login. Grok lists the
//! table names in its prompt, so they are the bare model ids, and the
//! `description` Grok keeps to itself marks AstrLink's. `[models] default`
//! picks the model Grok starts with, and `session_summary` the one that
//! names sessions, which otherwise asks AstrLink for a Grok model. The
//! community grok-cli shares `~/.grok` and reads none of this.

use std::{
    fs,
    path::{Path, PathBuf},
};

use serde_json::Value as Json;
use toml_edit::{DocumentMut, Item};

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{
            toml::{
                drop_empty_parent, json_of, parent_table, set_toml_string, table_of, toml_string,
            },
            Document, Format, TomlDocument,
        },
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&GrokBuild);

struct GrokBuild;

/// `config.toml`.
struct GrokConfig;

/// `model.<id>`: one model's table, as JSON.
const ENTRY_PREFIX: &str = "model.";
const MARK: &str = "AstrLink";
const DEFAULT_KEY: &str = "models.default";
const TITLE_KEY: &str = "models.session_summary";
/// What Grok sends as `max_tokens` to an Anthropic model whose limit it is
/// not told is 128000, more than most take.
const ANTHROPIC_OUTPUT: u64 = 32_000;

fn rank(name: &str) -> usize {
    const ORDER: [&str; 8] = [
        "model",
        "name",
        "description",
        "base_url",
        "api_key",
        "api_backend",
        "context_window",
        "max_completion_tokens",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

/// `api_backend` for a gateway protocol. Every backend takes `/v1`.
fn backend(protocol: &str) -> &'static str {
    match protocol {
        "anthropic.messages" => "messages",
        "openai.responses" => "responses",
        _ => "chat_completions",
    }
}

fn entry(id: &str, protocol: &str, connection: &Connection, details: &Catalog) -> Json {
    let backend = backend(protocol);
    let mut entry = serde_json::json!({
        "model": id,
        "name": id,
        "description": MARK,
        "base_url": format!("{}/v1", connection.origin),
        "api_key": connection.token,
        "api_backend": backend,
    });
    let details = details.get(id);
    if let Some(context) = details.and_then(|details| details.context_window) {
        entry["context_window"] = context.into();
    }
    let output = details.and_then(|details| details.max_tokens);
    if let Some(output) = output.or((backend == "messages").then_some(ANTHROPIC_OUTPUT)) {
        entry["max_completion_tokens"] = output.into();
    }
    entry
}

/// `[models]` keys; `None` for another key.
fn models_name(key: &str) -> Option<&str> {
    key.strip_prefix("models.")
}

fn models_string(document: &DocumentMut, name: &str) -> Option<Option<String>> {
    document
        .get("models")?
        .as_table_like()?
        .get(name)
        .map(toml_string)
}

impl ConfigFile for GrokConfig {
    fn format(&self) -> Format {
        Format::Toml
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Toml(TomlDocument { document: toml, .. }) = document else {
            return None;
        };
        if let Some(id) = key.strip_prefix(ENTRY_PREFIX) {
            let item = toml.get("model")?.as_table_like()?.get(id)?;
            return Some(json_of(item).map(|value| value.to_string()));
        }
        match models_name(key) {
            Some(name) => models_string(toml, name),
            None => document.get(key),
        }
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Toml(TomlDocument { document: toml, .. }) = document else {
            return document.set(key, value);
        };
        if let Some(id) = key.strip_prefix(ENTRY_PREFIX) {
            let value: Json = serde_json::from_str(value).map_err(|error| error.to_string())?;
            let table =
                table_of(&value, rank).ok_or_else(|| format!("{key} cannot be written as TOML"))?;
            let models = parent_table(toml, "model")?;
            match models.get_mut(id) {
                Some(Item::Table(current)) => {
                    // Values change in place, keeping the table where it is.
                    let decor = current.decor().clone();
                    let position = current.position();
                    *current = table;
                    *current.decor_mut() = decor;
                    if let Some(position) = position {
                        current.set_position(position);
                    }
                }
                _ => {
                    models.insert(id, Item::Table(table));
                }
            }
            return Ok(());
        }
        match models_name(key) {
            Some(name) => {
                set_toml_string(parent_table(toml, "models")?, name, value);
                Ok(())
            }
            None => document.set(key, value),
        }
    }

    fn remove(&self, document: &mut Document, key: &str) {
        let Document::Toml(TomlDocument { document: toml, .. }) = document else {
            return;
        };
        let (parent, name) = match key.strip_prefix(ENTRY_PREFIX) {
            Some(id) => ("model", id),
            None => match models_name(key) {
                Some(name) => ("models", name),
                None => return document.remove(key),
            },
        };
        if let Some(table) = toml.get_mut(parent).and_then(Item::as_table_like_mut) {
            table.remove(name);
        }
        drop_empty_parent(toml, parent);
    }
}

/// AstrLink's model tables, by model id.
fn owned_entries(config: &Config) -> Vec<(String, Json)> {
    let Some(Document::Toml(TomlDocument { document, .. })) = config.documents.first() else {
        return Vec::new();
    };
    let Some(models) = document.get("model").and_then(Item::as_table_like) else {
        return Vec::new();
    };
    models
        .iter()
        .filter_map(|(id, item)| Some((id.to_string(), json_of(item)?)))
        .filter(|(_, entry)| entry.get("description").and_then(Json::as_str) == Some(MARK))
        .collect()
}

/// Whether `~/.grok` holds only the community grok-cli: its installer
/// leaves `install.json` naming its repository, and its settings file is
/// `user-settings.json`.
fn community_only(dir: &Path) -> bool {
    if dir.join("config.toml").is_file() || dir.join("downloads").is_dir() {
        return false;
    }
    fs::read_to_string(dir.join("install.json"))
        .is_ok_and(|text| text.contains("superagent-ai/grok-cli"))
        || dir.join("user-settings.json").is_file()
}

impl ClientSpec for GrokBuild {
    fn id(&self) -> &'static str {
        "grok_build"
    }

    fn name(&self) -> &'static str {
        "Grok Build"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `~/.grok`, or where CC Switch's settings move it; `GROK_HOME` from a
    /// shell is out of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.grok_dir()
    }

    fn detected(&self, home: Home, target: Option<&str>) -> bool {
        let dir = self.dir(home, target);
        dir.is_dir() && !community_only(&dir)
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&GrokConfig]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self.dir(home, target).join("config.toml")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// Each model table holds an address and the token.
    fn holds_connection(&self, key: &str) -> bool {
        key.starts_with(ENTRY_PREFIX)
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let entries = owned_entries(config);
        let mut ids: Vec<String> = Vec::new();
        for (_, entry) in &entries {
            if let Some(id) = entry
                .get("model")
                .and_then(Json::as_str)
                .and_then(|id| clean_model(id.into()))
            {
                if !ids.contains(&id) {
                    ids.push(id);
                }
            }
        }
        let chosen = config.get(DEFAULT_KEY).flatten().and_then(|name| {
            let (_, entry) = entries.iter().find(|(id, _)| *id == name)?;
            entry.get("model")?.as_str().map(str::to_string)
        });
        CurrentModels {
            model: chosen.or_else(|| ids.first().cloned()),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let mut keys: Vec<(String, String)> = connection
            .list()
            .iter()
            .map(|id| {
                let protocol = connection.protocol(self, id);
                (
                    format!("{ENTRY_PREFIX}{id}"),
                    entry(id, protocol, connection, details).to_string(),
                )
            })
            .collect();
        if let Some(model) = connection.model("model") {
            keys.push((DEFAULT_KEY.into(), model.clone()));
            keys.push((TITLE_KEY.into(), model));
        }
        keys
    }

    /// Unused: `synced_keys` points every table elsewhere.
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
            .filter(|key| key.starts_with(ENTRY_PREFIX))
            .filter_map(|key| {
                let mut entry: Json = serde_json::from_str(&config.get(key).flatten()?).ok()?;
                entry["base_url"] = format!("{origin}/v1").into();
                Some((key.clone(), entry.to_string()))
            })
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    const USER: &str = "# Grok Build settings\n[models]\ndefault = \"grok-4.6\"\n\n[model.\"glm-5\"]\nmodel = \"glm-5\"\nbase_url = \"https://open.bigmodel.cn/api/paas/v4\"\napi_key = \"zk-own\"\n\n[ui]\nscreen_mode = \"minimal\"\n";

    fn config(home: &Path) -> PathBuf {
        home.join(".grok/config.toml")
    }

    fn toml(home: &Path) -> toml_edit::DocumentMut {
        fs::read_to_string(config(home)).unwrap().parse().unwrap()
    }

    #[test]
    fn writes_a_table_per_model_with_its_own_key() {
        let home = unique_home("grok");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".grok")).unwrap();
        let models = vec![("model", "claude-sonnet-5".to_string())];
        let list = strings(&["claude-sonnet-5", "gpt-5.6", "glm-5"]);
        let protocols = BTreeMap::from([
            ("claude-sonnet-5".to_string(), "anthropic.messages"),
            ("gpt-5.6".to_string(), "openai.responses"),
        ]);
        let chosen = Connection {
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        let details = Catalog::from([(
            "gpt-5.6".to_string(),
            ModelDetails {
                context_window: Some(400_000),
                max_tokens: Some(128_000),
                ..Default::default()
            },
        )]);
        let outcome = write_with(local, GROK_BUILD, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        let written = toml(&home);
        assert_eq!(
            written["models"]["default"].as_str(),
            Some("claude-sonnet-5")
        );
        assert_eq!(
            written["models"]["session_summary"].as_str(),
            Some("claude-sonnet-5")
        );
        let claude = &written["model"]["claude-sonnet-5"];
        assert_eq!(claude["description"].as_str(), Some("AstrLink"));
        assert_eq!(claude["api_backend"].as_str(), Some("messages"));
        assert_eq!(
            claude["base_url"].as_str(),
            Some(format!("{ORIGIN}/v1").as_str())
        );
        assert_eq!(claude["api_key"].as_str(), Some(TOKEN));
        assert_eq!(claude["max_completion_tokens"].as_integer(), Some(32_000));
        let gpt = &written["model"]["gpt-5.6"];
        assert_eq!(gpt["api_backend"].as_str(), Some("responses"));
        assert_eq!(gpt["context_window"].as_integer(), Some(400_000));
        assert_eq!(gpt["max_completion_tokens"].as_integer(), Some(128_000));
        let glm = &written["model"]["glm-5"];
        assert_eq!(glm["api_backend"].as_str(), Some("chat_completions"));
        assert!(glm.get("max_completion_tokens").is_none());
        let status = &status_of(local, [GROK_BUILD], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-sonnet-5"));
        assert_eq!(status.models.models.as_ref().map(Vec::len), Some(3));

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = toml(&home);
        assert_eq!(
            synced["model"]["glm-5"]["base_url"].as_str(),
            Some("http://127.0.0.1:9000/v1")
        );
        let status = &status_of(
            local,
            [GROK_BUILD],
            Some("http://127.0.0.1:9000"),
            Some(&tokens()),
        )
        .unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);

        remove(local, GROK_BUILD).unwrap();
        assert!(
            !config(&home).exists(),
            "{:?}",
            fs::read_to_string(config(&home))
        );
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn asks_before_replacing_the_users_model_and_default_and_puts_them_back() {
        let home = unique_home("grok-merge");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".grok")).unwrap();
        fs::write(config(&home), USER).unwrap();
        let models = vec![("model", "glm-5".to_string())];
        assert_eq!(
            asked(write(local, GROK_BUILD, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["model.glm-5", "models.default"]), None))
        );
        write(local, GROK_BUILD, &connection(&models), &tokens(), true).unwrap();
        let text = fs::read_to_string(config(&home)).unwrap();
        assert!(
            text.starts_with("# Grok Build settings\n[models]\ndefault = \"glm-5\""),
            "{text}"
        );
        let written = toml(&home);
        assert_eq!(written["model"]["glm-5"]["api_key"].as_str(), Some(TOKEN));
        assert_eq!(
            written["model"]["glm-5"]["description"].as_str(),
            Some("AstrLink")
        );
        assert_eq!(toml(&home)["ui"]["screen_mode"].as_str(), Some("minimal"));

        remove(local, GROK_BUILD).unwrap();
        assert_eq!(fs::read_to_string(config(&home)).unwrap(), USER);
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn the_community_grok_cli_alone_is_not_grok_build() {
        let home = unique_home("grok-community");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".grok")).unwrap();
        fs::write(home.join(".grok/user-settings.json"), "{\"apiKey\":\"x\"}").unwrap();
        assert!(!GROK_BUILD.detected(local, None));
        fs::create_dir_all(home.join(".grok/downloads")).unwrap();
        assert!(GROK_BUILD.detected(local, None));
        fs::remove_dir_all(home).unwrap();
    }
}
