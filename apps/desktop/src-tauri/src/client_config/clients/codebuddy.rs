//! CodeBuddy, Tencent's coding agent. Its CLI, IDE, and VS Code and
//! JetBrains plugins share `~/.codebuddy/models.json`; the CLI's default
//! model is `model` in `~/.codebuddy/settings.json`. The IDE and plugins
//! post every model to the Chat API, so every entry is a Chat one.

use std::path::PathBuf;

use super::{buddy, ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::Format,
        models::{Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Codebuddy);

const MODEL_KEY: &str = "model";
/// What the CLI's default model names a `models.json` entry by, so an
/// entry sharing a built-in model's id is not taken for that model.
const ENTRY_PREFIX: &str = "custom-local:";

struct Codebuddy;

/// `settings.json`, strict JSON, of which AstrLink writes `model` only.
struct Settings;

fn route(origin: &str) -> String {
    format!("{origin}/v1/chat/completions")
}

impl ClientSpec for Codebuddy {
    fn id(&self) -> &'static str {
        "codebuddy"
    }

    fn name(&self) -> &'static str {
        "CodeBuddy"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// Only the CLI reads `CODEBUDDY_CONFIG_DIR`; the IDE and plugins keep
    /// to the default folder.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".codebuddy")
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&buddy::ModelsFile, &Settings]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        let dir = self.dir(home, target);
        vec![dir.join("models.json"), dir.join("settings.json")]
    }

    /// Each entry carries its own address and key.
    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    fn holds_connection(&self, key: &str) -> bool {
        buddy::holds_connection(key)
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let mut models = buddy::current_models(config, 0);
        let chosen = config.model(MODEL_KEY).map(|model| {
            model
                .strip_prefix(ENTRY_PREFIX)
                .map(str::to_string)
                .unwrap_or(model)
        });
        if let Some(model) = chosen.filter(|model| {
            models
                .models
                .as_ref()
                .is_some_and(|ids| ids.contains(model))
        }) {
            models.model = Some(model);
        }
        models
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let mut keys = buddy::model_keys(connection, details, |_| (route(connection.origin), None));
        if let Some(model) = connection.model("model") {
            keys.push((MODEL_KEY.into(), format!("{ENTRY_PREFIX}{model}")));
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
        record: &ClientRecord,
        origin: &str,
    ) -> Vec<(String, String)> {
        record
            .keys
            .keys()
            .filter(|key| buddy::holds_connection(key))
            .filter_map(|key| {
                let entry = buddy::rerouted(config, key, |_| route(origin))?;
                Some((key.clone(), entry))
            })
            .collect()
    }
}

impl ConfigFile for Settings {
    fn format(&self) -> Format {
        Format::Json {
            syntax: None,
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

    const GPT: &str = "gpt-5.5";
    const SONNET: &str = "claude-sonnet-5";

    fn read(home: &Path, name: &str) -> Option<serde_json::Value> {
        fs::read_to_string(home.join(".codebuddy").join(name))
            .ok()
            .map(|text| json(&text))
    }

    fn entry(id: &str, origin: &str) -> serde_json::Value {
        serde_json::json!({
            "id": id,
            "name": id,
            "vendor": "AstrLink",
            "url": format!("{origin}/v1/chat/completions"),
            "apiKey": TOKEN,
        })
    }

    #[test]
    fn writes_chat_entries_and_the_default_model() {
        let home = unique_home("codebuddy");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".codebuddy")).unwrap();
        let models = vec![("model", SONNET.to_string())];
        let list = strings(&[SONNET, GPT]);
        // The IDE posts every model to Chat, whatever was chosen.
        let protocols = BTreeMap::from([(GPT.to_string(), "openai.responses")]);
        let connection = Connection {
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        let outcome = write(local, CODEBUDDY, &connection, &tokens(), false).unwrap();
        assert_eq!(asked(outcome), None);
        assert_eq!(
            read(&home, "models.json"),
            Some(serde_json::json!({"models": [entry(SONNET, ORIGIN), entry(GPT, ORIGIN)]}))
        );
        assert_eq!(
            read(&home, "settings.json"),
            Some(serde_json::json!({"model": "custom-local:claude-sonnet-5"}))
        );
        let status = &status_of(local, [CODEBUDDY], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(
            status.models,
            CurrentModels {
                model: Some(SONNET.into()),
                models: Some(strings(&[SONNET, GPT])),
                ..Default::default()
            }
        );

        // `/model` in the CLI picks another of the entries.
        let settings = home.join(".codebuddy/settings.json");
        fs::write(&settings, "{\"model\": \"custom-local:gpt-5.5\"}\n").unwrap();
        let status = &status_of(local, [CODEBUDDY], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some(GPT));
        assert_eq!(status.models.models, Some(strings(&[GPT, SONNET])));

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert_eq!(
            read(&home, "models.json"),
            Some(serde_json::json!({"models": [
                entry(SONNET, "http://127.0.0.1:9000"),
                entry(GPT, "http://127.0.0.1:9000"),
            ]}))
        );

        remove(local, CODEBUDDY).unwrap();
        assert_eq!(read(&home, "models.json"), None);
        assert_eq!(
            read(&home, "settings.json"),
            Some(serde_json::json!({"model": "custom-local:gpt-5.5"}))
        );
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn joins_the_models_and_allowlist_already_there() {
        let home = unique_home("codebuddy-merge");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".codebuddy")).unwrap();
        let models_json = r#"{
  "models": [
    {
      "id": "deepseek-v4",
      "name": "DeepSeek V4",
      "vendor": "DeepSeek",
      "url": "https://api.deepseek.com/v1/chat/completions",
      "apiKey": "${DEEPSEEK_API_KEY}",
      "maxInputTokens": 128000,
      "maxOutputTokens": 8192
    }
  ],
  "availableModels": ["deepseek-v4", "gpt-5.4"]
}
"#;
        let settings_json = "{\"theme\":\"dark\",\"model\":\"deepseek-v4\"}";
        fs::write(home.join(".codebuddy/models.json"), models_json).unwrap();
        fs::write(home.join(".codebuddy/settings.json"), settings_json).unwrap();
        let models = vec![("model", SONNET.to_string())];
        let list = strings(&[SONNET, GPT]);
        let connection = listed(&models, &list);
        assert_eq!(
            asked(write(local, CODEBUDDY, &connection, &tokens(), false).unwrap()),
            Some((strings(&["model"]), None))
        );
        write(local, CODEBUDDY, &connection, &tokens(), true).unwrap();
        let written = read(&home, "models.json").unwrap();
        assert_eq!(written["models"][0], json(models_json)["models"][0]);
        assert_eq!(
            written["models"].as_array().unwrap()[1..],
            [entry(SONNET, ORIGIN), entry(GPT, ORIGIN)]
        );
        assert_eq!(
            written["availableModels"],
            serde_json::json!(["deepseek-v4", "gpt-5.4", SONNET, GPT])
        );
        assert_eq!(
            read(&home, "settings.json"),
            Some(serde_json::json!({"theme": "dark", "model": "custom-local:claude-sonnet-5"}))
        );

        remove(local, CODEBUDDY).unwrap();
        assert_eq!(
            fs::read_to_string(home.join(".codebuddy/models.json")).unwrap(),
            models_json
        );
        assert_eq!(
            fs::read_to_string(home.join(".codebuddy/settings.json")).unwrap(),
            settings_json
        );
        fs::remove_dir_all(home).unwrap();
    }
}
