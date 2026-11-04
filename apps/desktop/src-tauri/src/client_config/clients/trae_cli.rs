//! TRAE CLI reads `~/.trae/traecli.toml`. Each `[[models]]` entry is one
//! model in its `/model` list, with the address and key in a subtable named
//! for its protocol; AstrLink's are the ones that send an AstrLink token to
//! a gateway on this computer, after the user's. `model_provider = "custom"`
//! with `model` picks one.

use std::path::PathBuf;

use serde_json::Value as Json;
use toml_edit::{ArrayOfTables, DocumentMut, Item};

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

pub(crate) const CLIENT: Client = Client::new(&TraeCli);

struct TraeCli;

/// `traecli.toml`.
struct TraeConfig;

/// Protocol and the subtable that takes it.
const BACKENDS: [(&str, &str); 3] = [
    ("openai.chat", "open_ai"),
    ("openai.responses", "byted_gpt"),
    ("anthropic.messages", "claude"),
];
const MODELS_KEY: &str = "models";
const PROVIDER_KEY: &str = "model_provider";
const MODEL_KEY: &str = "model";
/// What `model_provider` picks `[[models]]` by `model` with.
const CUSTOM: &str = "custom";

fn rank(name: &str) -> usize {
    const ORDER: [&str; 6] = [
        "name",
        "context_window",
        "output_tokens_hard_limit",
        "base_url",
        "api_key",
        "max_tokens",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

/// An entry that sends an AstrLink token to a gateway on this computer.
fn owned(entry: &Json) -> bool {
    BACKENDS.iter().any(|(_, backend)| {
        let text = |name| {
            entry
                .get(backend)
                .and_then(|backend| backend.get(name))
                .and_then(Json::as_str)
                .unwrap_or_default()
        };
        text("api_key").starts_with("astr_")
            && ["http://127.0.0.1:", "http://localhost:"]
                .iter()
                .any(|origin| text("base_url").starts_with(origin))
    })
}

fn owned_entries(config: &Config) -> Vec<Json> {
    config
        .get(MODELS_KEY)
        .flatten()
        .and_then(|value| serde_json::from_str::<Json>(&value).ok())
        .and_then(|value| value.as_array().cloned())
        .unwrap_or_default()
}

fn entry(id: &str, protocol: &str, connection: &Connection, details: &Catalog) -> Json {
    let backend = BACKENDS
        .iter()
        .find(|(known, _)| *known == protocol)
        .map_or(BACKENDS[0].1, |(_, backend)| backend);
    let mut entry = serde_json::json!({ "name": id });
    // TRAE adds `/chat/completions`, `/responses`, or `/messages`.
    let mut address = serde_json::json!({
        "base_url": format!("{}/v1", connection.origin),
        "api_key": connection.token,
    });
    if let Some(details) = details.get(id) {
        if let Some(context) = details.context_window {
            entry["context_window"] = context.into();
        }
        if let Some(output) = details.max_tokens {
            entry["output_tokens_hard_limit"] = output.into();
            address["max_tokens"] = output.into();
        }
    }
    entry[backend] = address;
    entry
}

impl ClientSpec for TraeCli {
    fn id(&self) -> &'static str {
        "trae_cli"
    }

    fn name(&self) -> &'static str {
        "TRAE CLI"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `TRAE_HOME` from a shell is out of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".trae")
    }

    /// The TRAE editor keeps `~/.trae` too; the CLI keeps its state in
    /// `~/.trae/cli` after a first run.
    fn detected(&self, home: Home, target: Option<&str>) -> bool {
        let dir = self.dir(home, target);
        let installs = if home.windows() {
            vec![home.join("AppData/Local/Programs/TraeCLI/bin")]
        } else {
            vec![
                home.join(".local/bin/traecli"),
                home.join(".local/bin/traex"),
            ]
        };
        dir.join("cli").is_dir()
            || dir.join("traecli.toml").is_file()
            || installs.iter().any(|path| path.exists())
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&TraeConfig]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self.dir(home, target).join("traecli.toml")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// Each entry carries its own address and key.
    fn holds_connection(&self, key: &str) -> bool {
        key == MODELS_KEY
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let mut ids: Vec<String> = Vec::new();
        for id in owned_entries(config)
            .iter()
            .filter_map(|entry| entry.get("name")?.as_str())
            .filter_map(|id| clean_model(id.into()))
        {
            if !ids.contains(&id) {
                ids.push(id);
            }
        }
        let chosen = config
            .get(PROVIDER_KEY)
            .flatten()
            .filter(|provider| provider == CUSTOM)
            .and_then(|_| config.model(MODEL_KEY))
            .filter(|model| ids.contains(model));
        CurrentModels {
            model: chosen.or_else(|| ids.first().cloned()),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let entries: Vec<Json> = connection
            .list()
            .iter()
            .map(|id| entry(id, connection.protocol(self, id), connection, details))
            .collect();
        let mut keys = Vec::new();
        if let Some(model) = connection.model("model") {
            keys.push((MODEL_KEY.into(), model));
            keys.push((PROVIDER_KEY.into(), CUSTOM.into()));
        }
        if !entries.is_empty() {
            keys.push((MODELS_KEY.into(), Json::Array(entries).to_string()));
        }
        keys
    }

    /// Unused: `synced_keys` points every entry elsewhere.
    fn base_url_key(&self) -> &'static str {
        ""
    }

    fn synced_keys(
        &self,
        config: &Config,
        _record: &ClientRecord,
        origin: &str,
    ) -> Vec<(String, String)> {
        let mut entries = owned_entries(config);
        if entries.is_empty() {
            return Vec::new();
        }
        for entry in &mut entries {
            for (_, backend) in BACKENDS {
                if let Some(address) = entry.get_mut(backend).and_then(Json::as_object_mut) {
                    address.insert("base_url".into(), format!("{origin}/v1").into());
                }
            }
        }
        vec![(MODELS_KEY.into(), Json::Array(entries).to_string())]
    }
}

fn tables(toml: &DocumentMut) -> Option<&ArrayOfTables> {
    toml.get(MODELS_KEY)?.as_array_of_tables()
}

fn drop_ours(toml: &mut DocumentMut) {
    let Some(tables) = toml
        .get_mut(MODELS_KEY)
        .and_then(Item::as_array_of_tables_mut)
    else {
        return;
    };
    tables.retain(|table| !json_of(&Item::Table(table.clone())).is_some_and(|entry| owned(&entry)));
    if tables.is_empty() {
        toml.as_table_mut().remove(MODELS_KEY);
    }
}

impl ConfigFile for TraeConfig {
    fn format(&self) -> Format {
        Format::Toml
    }

    fn secret(&self) -> bool {
        true
    }

    /// The list reads as AstrLink's entries in it.
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Toml(toml) = document else {
            return None;
        };
        if key != MODELS_KEY {
            return document.get(key);
        }
        let entries: Vec<Json> = tables(&toml.document)?
            .iter()
            .filter_map(|table| json_of(&Item::Table(table.clone())))
            .filter(owned)
            .collect();
        (!entries.is_empty()).then(|| Some(Json::Array(entries).to_string()))
    }

    /// Each entry's address is a `[models.<backend>]` table of its own.
    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        if key != MODELS_KEY {
            return document.set(key, value);
        }
        let Document::Toml(toml) = document else {
            return Err(format!("{key} is not TOML"));
        };
        let toml = &mut toml.document;
        let Ok(Json::Array(entries)) = serde_json::from_str::<Json>(value) else {
            return Err(format!("{key} is not a list"));
        };
        drop_ours(toml);
        let root = toml.as_table_mut();
        if !root.contains_key(key) {
            root.insert(key, Item::ArrayOfTables(ArrayOfTables::new()));
        }
        let tables = root
            .get_mut(key)
            .and_then(Item::as_array_of_tables_mut)
            .ok_or_else(|| format!("has a {key} value that is not a list of tables"))?;
        for entry in &entries {
            let mut fields = entry.clone();
            let addresses: Vec<(&str, Json)> = BACKENDS
                .iter()
                .filter_map(|(_, backend)| {
                    Some((*backend, fields.as_object_mut()?.remove(*backend)?))
                })
                .collect();
            let mut table = table_of(&fields, rank)
                .ok_or_else(|| format!("{key} holds a value TOML cannot"))?;
            for (backend, address) in addresses {
                let address = table_of(&address, rank)
                    .ok_or_else(|| format!("{key} holds a value TOML cannot"))?;
                table.insert(backend, Item::Table(address));
            }
            tables.push(table);
        }
        Ok(())
    }

    fn remove(&self, document: &mut Document, key: &str) {
        match document {
            Document::Toml(toml) if key == MODELS_KEY => drop_ours(&mut toml.document),
            _ => document.remove(key),
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn file(home: &Path) -> PathBuf {
        home.join(".trae/traecli.toml")
    }

    #[test]
    fn writes_a_model_entry_per_model_and_picks_one() {
        let home = unique_home("trae");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".trae/cli")).unwrap();
        let models = vec![("model", "claude-sonnet-5".to_string())];
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
                reasoning: None,
                context_window: Some(200_000),
                max_tokens: Some(64_000),
            },
        )]);
        let outcome = write_with(local, TRAE_CLI, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        assert_eq!(
            fs::read_to_string(file(&home)).unwrap(),
            format!(
                "model = \"claude-sonnet-5\"\nmodel_provider = \"custom\"\n\n[[models]]\nname = \"deepseek-v4\"\n\n[models.open_ai]\nbase_url = \"{ORIGIN}/v1\"\napi_key = \"{TOKEN}\"\n\n[[models]]\nname = \"gpt-5.5\"\n\n[models.byted_gpt]\nbase_url = \"{ORIGIN}/v1\"\napi_key = \"{TOKEN}\"\n\n[[models]]\nname = \"claude-sonnet-5\"\ncontext_window = 200000\noutput_tokens_hard_limit = 64000\n\n[models.claude]\nbase_url = \"{ORIGIN}/v1\"\napi_key = \"{TOKEN}\"\nmax_tokens = 64000\n"
            )
        );
        let status = &status_of(local, [TRAE_CLI], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-sonnet-5"));

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = fs::read_to_string(file(&home)).unwrap();
        assert_eq!(
            synced
                .matches("base_url = \"http://127.0.0.1:9000/v1\"")
                .count(),
            3,
            "{synced}"
        );

        remove(local, TRAE_CLI).unwrap();
        assert!(!file(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = "# my trae\nmodel = \"doubao-seed\"\nmodel_provider = \"trae\"\n\n[[models]]\nname = \"my-model\"\n\n[models.open_ai]\nbase_url = \"https://api.example.com/v1\"\napi_key = \"${MY_API_KEY}\"\n";

    #[test]
    fn adds_its_models_after_the_users_and_puts_their_choice_back() {
        let home = unique_home("trae-merge");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".trae")).unwrap();
        fs::write(file(&home), USER).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, TRAE_CLI, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["model", "model_provider"]), None))
        );
        write(local, TRAE_CLI, &connection(&models), &tokens(), true).unwrap();
        let written = fs::read_to_string(file(&home)).unwrap();
        assert!(
            written
                .starts_with("# my trae\nmodel = \"deepseek-v4\"\nmodel_provider = \"custom\"\n"),
            "{written}"
        );
        let mine = written.find("name = \"my-model\"").unwrap();
        let ours = written.find("name = \"deepseek-v4\"").unwrap();
        assert!(mine < ours, "{written}");

        remove(local, TRAE_CLI).unwrap();
        assert_eq!(fs::read_to_string(file(&home)).unwrap(), USER);
        fs::remove_dir_all(home).unwrap();
    }
}
