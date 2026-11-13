//! Codewhale reads `~/.codewhale/config.toml` and starts with the provider
//! table root `provider` names. AstrLink adds one table whose `wire` is the
//! default model's protocol; Codewhale lists the other models from the
//! gateway. A custom table reads its key only from the file.

use std::path::PathBuf;

use serde_json::Value as Json;
use toml_edit::Item;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{
            toml::{drop_empty_parent, json_of, parent_table, table_of},
            Document, Format, TomlDocument,
        },
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Codewhale);

struct Codewhale;

/// `config.toml`.
struct CodewhaleConfig;

const NAME: &str = "local-gateway";
const PROVIDER_KEY: &str = "providers.local-gateway";
const CHOICE_KEY: &str = "provider";

/// Chat Completions is what a table without `wire` speaks.
fn wire_of(protocol: &str) -> Option<&'static str> {
    match protocol {
        "openai.responses" => Some("responses"),
        "anthropic.messages" => Some("anthropic"),
        _ => None,
    }
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 6] = [
        "kind",
        "base_url",
        "api_key",
        "model",
        "wire",
        "model_context_windows",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn json_at(config: &Config, key: &str) -> Option<Json> {
    serde_json::from_str(&config.get(key).flatten()?).ok()
}

impl ClientSpec for Codewhale {
    fn id(&self) -> &'static str {
        "codewhale"
    }

    fn name(&self) -> &'static str {
        "Codewhale"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    /// `CODEWHALE_HOME` from a shell is out of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".codewhale")
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&CodewhaleConfig]
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

    /// The table holds the address and the token.
    fn holds_connection(&self, key: &str) -> bool {
        key == PROVIDER_KEY
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let model = json_at(config, PROVIDER_KEY)
            .filter(|_| config.get(CHOICE_KEY).flatten().as_deref() == Some(NAME))
            .and_then(|table| table.get("model")?.as_str().map(str::to_string))
            .and_then(clean_model);
        CurrentModels {
            model,
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let Some(model) = connection.model("model") else {
            return Vec::new();
        };
        let mut table = serde_json::json!({
            "kind": "openai-compatible",
            "base_url": self.base_url(connection.origin),
            "api_key": connection.token,
            "model": model,
        });
        if let Some(wire) = wire_of(connection.protocol(self, &model)) {
            table["wire"] = wire.into();
        }
        if let Some(context) = details
            .get(&model)
            .and_then(|details| details.context_window)
        {
            table["model_context_windows"] = serde_json::json!({ &model: context });
        }
        vec![
            (CHOICE_KEY.into(), NAME.into()),
            (PROVIDER_KEY.into(), table.to_string()),
        ]
    }

    /// Unused: `synced_keys` moves the table.
    fn base_url_key(&self) -> &'static str {
        ""
    }

    fn synced_keys(
        &self,
        config: &Config,
        _record: &ClientRecord,
        origin: &str,
    ) -> Vec<(String, String)> {
        let Some(mut table) = json_at(config, PROVIDER_KEY) else {
            return Vec::new();
        };
        table["base_url"] = self.base_url(origin).into();
        vec![(PROVIDER_KEY.into(), table.to_string())]
    }
}

impl ConfigFile for CodewhaleConfig {
    fn format(&self) -> Format {
        Format::Toml
    }

    fn secret(&self) -> bool {
        true
    }

    /// The table reads as one JSON object.
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Toml(TomlDocument { document: toml, .. }) = document else {
            return None;
        };
        if key != PROVIDER_KEY {
            return document.get(key);
        }
        let item = toml.get("providers")?.as_table_like()?.get(NAME)?;
        Some(json_of(item).map(|value| value.to_string()))
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Toml(TomlDocument { document: toml, .. }) = document else {
            return document.set(key, value);
        };
        if key != PROVIDER_KEY {
            return document.set(key, value);
        }
        let value: Json = serde_json::from_str(value).map_err(|error| error.to_string())?;
        let table =
            table_of(&value, rank).ok_or_else(|| format!("{key} cannot be written as TOML"))?;
        let providers = parent_table(toml, "providers")?;
        match providers.get_mut(NAME) {
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
                providers.insert(NAME, Item::Table(table));
            }
        }
        Ok(())
    }

    fn remove(&self, document: &mut Document, key: &str) {
        let Document::Toml(TomlDocument { document: toml, .. }) = document else {
            return;
        };
        if key != PROVIDER_KEY {
            return document.remove(key);
        }
        if let Some(providers) = toml.get_mut("providers").and_then(Item::as_table_like_mut) {
            providers.remove(NAME);
        }
        drop_empty_parent(toml, "providers");
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn file(home: &Path) -> PathBuf {
        home.join(".codewhale/config.toml")
    }

    #[test]
    fn writes_one_table_with_the_default_models_wire() {
        let home = unique_home("codewhale");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".codewhale")).unwrap();
        let models = vec![("model", "gpt-5.5".to_string())];
        let protocols = BTreeMap::from([("gpt-5.5".to_string(), "openai.responses")]);
        let chosen = Connection {
            protocols: &protocols,
            ..connection(&models)
        };
        let details = Catalog::from([(
            "gpt-5.5".to_string(),
            ModelDetails {
                context_window: Some(400_000),
                ..Default::default()
            },
        )]);
        let outcome = write_with(local, CODEWHALE, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        assert_eq!(
            fs::read_to_string(file(&home)).unwrap(),
            format!(
                r#"provider = "local-gateway"

[providers.local-gateway]
kind = "openai-compatible"
base_url = "{ORIGIN}/v1"
api_key = "{TOKEN}"
model = "gpt-5.5"
wire = "responses"
model_context_windows = {{ "gpt-5.5" = 400000 }}
"#
            )
        );
        let status = &status_of(local, [CODEWHALE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("gpt-5.5"));

        // A chat model drops the wire and the window it has no size for.
        let models = vec![("model", "deepseek-v4".to_string())];
        write(local, CODEWHALE, &connection(&models), &tokens(), false).unwrap();
        let written = fs::read_to_string(file(&home)).unwrap();
        assert!(!written.contains("wire"), "{written}");
        assert!(!written.contains("model_context_windows"), "{written}");

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = fs::read_to_string(file(&home)).unwrap();
        assert!(
            synced.contains("base_url = \"http://127.0.0.1:9000/v1\""),
            "{synced}"
        );

        remove(local, CODEWHALE).unwrap();
        assert!(!file(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = "# my whale\nprovider = \"deepseek\"\nmodel = \"deepseek-v4-pro\"\n\n[providers.deepseek]\napi_key = \"sk-deep\"\n";

    #[test]
    fn keeps_the_users_provider_and_puts_it_back() {
        let home = unique_home("codewhale-merge");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".codewhale")).unwrap();
        fs::write(file(&home), USER).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, CODEWHALE, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["provider"]), None))
        );
        write(local, CODEWHALE, &connection(&models), &tokens(), true).unwrap();
        let written = fs::read_to_string(file(&home)).unwrap();
        assert!(
            written.starts_with("# my whale\nprovider = \"local-gateway\"\n"),
            "{written}"
        );
        assert!(
            written.contains("[providers.deepseek]\napi_key = \"sk-deep\"\n"),
            "{written}"
        );
        assert!(written.contains("[providers.local-gateway]\n"), "{written}");

        remove(local, CODEWHALE).unwrap();
        assert_eq!(fs::read_to_string(file(&home)).unwrap(), USER);
        fs::remove_dir_all(home).unwrap();
    }
}
