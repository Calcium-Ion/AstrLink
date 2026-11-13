//! Aider reads `~/.aider.conf.yml` and sends every request through
//! LiteLLM, whose OpenAI-compatible route takes a model named
//! `openai/<model>`. LiteLLM prefers `OPENAI_BASE_URL` to the address Aider
//! sets, so AstrLink also sets that variable in `set-env`, keeping one the
//! user's shell exports from taking the token elsewhere. Aider gives its
//! helper models AstrLink's model too, since its own picks may not be on
//! the gateway.

use std::path::PathBuf;

use serde_json::Value as Json;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
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

pub(crate) const CLIENT: Client = Client::new(&Aider);

struct Aider;

/// `.aider.conf.yml`.
struct AiderConfig;

const BASE_KEY: &str = "openai-api-base";
const TOKEN_KEY: &str = "openai-api-key";
const MODEL_KEYS: [&str; 3] = ["model", "weak-model", "editor-model"];
const LIST: &str = "set-env";
const VARIABLE: &str = "OPENAI_BASE_URL";
/// The `OPENAI_BASE_URL=<address>` item of `set-env`, by its value.
const VARIABLE_KEY: &str = "set-env.OPENAI_BASE_URL";
const PREFIX: &str = "openai/";

impl ClientSpec for Aider {
    fn id(&self) -> &'static str {
        "aider"
    }

    fn name(&self) -> &'static str {
        "Aider"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat"]
    }

    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.to_path_buf()
    }

    /// Aider keeps its state in `~/.aider` after a first run; uv and pipx
    /// install it as `aider-chat`.
    fn detected(&self, home: Home, _target: Option<&str>) -> bool {
        let installs = if home.windows() {
            vec![
                home.join("AppData/Roaming/uv/data/tools/aider-chat"),
                home.join("pipx/venvs/aider-chat"),
            ]
        } else {
            vec![
                home.join(".local/share/uv/tools/aider-chat"),
                home.join(".local/pipx/venvs/aider-chat"),
            ]
        };
        home.join(".aider").is_dir()
            || home.join(".aider.conf.yml").is_file()
            || installs.iter().any(|dir| dir.is_dir())
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&AiderConfig]
    }

    fn config_paths(
        &self,
        home: Home,
        _target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![home.join(".aider.conf.yml")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[BASE_KEY, TOKEN_KEY, VARIABLE_KEY]
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        CurrentModels {
            model: config
                .get(MODEL_KEYS[0])
                .flatten()
                .and_then(|model| model.strip_prefix(PREFIX).map(str::to_string))
                .and_then(clean_model),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, _details: &Catalog) -> Vec<(String, String)> {
        let base = self.base_url(connection.origin);
        let mut keys = vec![
            (BASE_KEY.to_string(), base.clone()),
            (VARIABLE_KEY.to_string(), base),
            (TOKEN_KEY.to_string(), connection.token.to_string()),
        ];
        if let Some(model) = connection.model("model") {
            for key in MODEL_KEYS {
                keys.push((key.into(), format!("{PREFIX}{model}")));
            }
        }
        keys
    }

    fn base_url_key(&self) -> &'static str {
        BASE_KEY
    }

    fn synced_keys(
        &self,
        _config: &Config,
        _record: &ClientRecord,
        origin: &str,
    ) -> Vec<(String, String)> {
        let base = self.base_url(origin);
        vec![(BASE_KEY.into(), base.clone()), (VARIABLE_KEY.into(), base)]
    }
}

/// The index and value of the `OPENAI_BASE_URL` item of `set-env`.
fn variable(yaml: &YamlDocument) -> Option<(usize, String)> {
    let items = yaml.get_json(&[Step::Key(LIST)])?;
    items
        .as_array()?
        .iter()
        .enumerate()
        .find_map(|(index, item)| {
            let (name, value) = item.as_str()?.split_once('=')?;
            (name.trim() == VARIABLE).then(|| (index, value.trim().to_string()))
        })
}

impl ConfigFile for AiderConfig {
    fn format(&self) -> Format {
        Format::Yaml {
            root: YamlRoot::Mapping,
        }
    }

    fn secret(&self) -> bool {
        true
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        match document {
            Document::Yaml(yaml) if key == VARIABLE_KEY => {
                variable(yaml).map(|(_, value)| Some(value))
            }
            _ => document.get(key),
        }
    }

    /// The item replaces one of the same name, at the end of the list.
    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Yaml(yaml) = document else {
            return document.set(key, value);
        };
        if key != VARIABLE_KEY {
            return yaml.set_key(key, value);
        }
        let item = Json::String(format!("{VARIABLE}={value}"));
        let count = yaml
            .get_json(&[Step::Key(LIST)])
            .and_then(|items| items.as_array().map(Vec::len));
        match (variable(yaml), count) {
            // A list of the item alone is written anew, keeping its style.
            (Some(_), Some(1)) | (None, None) => {
                yaml.set(&[], LIST, &Json::Array(vec![item]), |_| 0)
            }
            (Some((index, _)), _) => {
                yaml.remove(&[Step::Key(LIST), Step::Index(index)])?;
                yaml.push(&[Step::Key(LIST)], &item, |_| 0)
            }
            (None, Some(_)) => yaml.push(&[Step::Key(LIST)], &item, |_| 0),
        }
    }

    /// An emptied `set-env` goes.
    fn remove(&self, document: &mut Document, key: &str) {
        let Document::Yaml(yaml) = document else {
            return document.remove(key);
        };
        if key != VARIABLE_KEY {
            return document.remove(key);
        }
        if let Some((index, _)) = variable(yaml) {
            let _ = yaml.remove(&[Step::Key(LIST), Step::Index(index)]);
        }
        if yaml
            .get_json(&[Step::Key(LIST)])
            .is_some_and(|items| items.as_array().is_some_and(Vec::is_empty))
        {
            let _ = yaml.remove(&[Step::Key(LIST)]);
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn file(home: &Path) -> PathBuf {
        home.join(".aider.conf.yml")
    }

    #[test]
    fn writes_the_address_twice_and_the_model_for_every_role() {
        let home = unique_home("aider");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".aider")).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        let outcome = write(local, AIDER, &connection(&models), &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        assert_eq!(
            fs::read_to_string(file(&home)).unwrap(),
            format!(
                "openai-api-base: {ORIGIN}/v1\nset-env:\n- OPENAI_BASE_URL={ORIGIN}/v1\nopenai-api-key: {TOKEN}\nmodel: openai/deepseek-v4\nweak-model: openai/deepseek-v4\neditor-model: openai/deepseek-v4\n"
            )
        );
        let status = &status_of(local, [AIDER], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("deepseek-v4"));

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = fs::read_to_string(file(&home)).unwrap();
        assert!(
            synced.contains("openai-api-base: http://127.0.0.1:9000/v1\n"),
            "{synced}"
        );
        assert!(
            synced.contains("- OPENAI_BASE_URL=http://127.0.0.1:9000/v1\n"),
            "{synced}"
        );

        remove(local, AIDER).unwrap();
        assert!(!file(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = "# my aider\nmodel: anthropic/claude-sonnet-4-5\nset-env:\n  - OPENAI_BASE_URL=https://api.openai.com/v1\n  - LITELLM_LOG=ERROR\ndark-mode: true\n";

    #[test]
    fn replaces_the_users_variable_and_puts_it_back() {
        let home = unique_home("aider-merge");
        let local = Home::local(&home);
        fs::write(file(&home), USER).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, AIDER, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["set-env.OPENAI_BASE_URL", "model"]), None))
        );
        write(local, AIDER, &connection(&models), &tokens(), true).unwrap();
        let written = fs::read_to_string(file(&home)).unwrap();
        assert!(written.contains("  - LITELLM_LOG=ERROR\n"), "{written}");
        assert!(
            written.contains(&format!("  - OPENAI_BASE_URL={ORIGIN}/v1\n")),
            "{written}"
        );
        assert!(!written.contains("api.openai.com"), "{written}");
        assert!(written.contains("dark-mode: true\n"), "{written}");

        remove(local, AIDER).unwrap();
        let restored = fs::read_to_string(file(&home)).unwrap();
        assert!(
            restored.contains("  - OPENAI_BASE_URL=https://api.openai.com/v1\n"),
            "{restored}"
        );
        assert!(
            restored.starts_with("# my aider\nmodel: anthropic/claude-sonnet-4-5\n"),
            "{restored}"
        );
        assert!(!restored.contains("openai-api-key"), "{restored}");
        fs::remove_dir_all(home).unwrap();
    }
}
