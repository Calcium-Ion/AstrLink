//! WorkBuddy, Tencent's desktop agent, in its China edition (`~/.workbuddy`)
//! and international one (`~/.workbuddy-ai`). Each reads custom models from
//! `models.json` in its data folder and picks the default model in the app.

use std::path::PathBuf;

use super::{buddy, ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        models::{Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Workbuddy);

const RESPONSES: &str = "openai.responses";
/// The `api` of an entry WorkBuddy sends to the Responses API.
const RESPONSES_API: &str = "openai-responses";

struct Workbuddy;

/// The full Chat address, or the base a Responses entry appends to.
fn route(origin: &str, api: Option<&str>) -> String {
    match api {
        Some(_) => format!("{origin}/v1"),
        None => format!("{origin}/v1/chat/completions"),
    }
}

impl ClientSpec for Workbuddy {
    fn id(&self) -> &'static str {
        "workbuddy"
    }

    fn name(&self) -> &'static str {
        "WorkBuddy"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", RESPONSES]
    }

    fn targets(&self) -> &'static [&'static str] {
        &["cn", "intl"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    fn dir(&self, home: Home, target: Option<&str>) -> PathBuf {
        match target {
            Some("intl") => home.join(".workbuddy-ai"),
            _ => home.join(".workbuddy"),
        }
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&buddy::ModelsFile]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self.dir(home, target).join("models.json")]
    }

    /// Each entry carries its own address and key.
    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    fn holds_connection(&self, key: &str) -> bool {
        buddy::holds_connection(key)
    }

    /// The first of AstrLink's entries stands for the default model, which
    /// WorkBuddy keeps in its own preferences.
    fn current_models(&self, config: &Config) -> CurrentModels {
        buddy::current_models(config, 0)
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        buddy::model_keys(connection, details, |id| {
            let api = (connection.protocol(self, id) == RESPONSES).then_some(RESPONSES_API);
            (route(connection.origin, api), api)
        })
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
                let entry = buddy::rerouted(config, key, |api| route(origin, api))?;
                Some((key.clone(), entry))
            })
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    const GPT: &str = "gpt-5.5";
    const SONNET: &str = "claude-sonnet-5";

    fn models_path(home: &Path, target: &str) -> PathBuf {
        let dir = match target {
            "intl" => ".workbuddy-ai",
            _ => ".workbuddy",
        };
        home.join(dir).join("models.json")
    }

    fn read(home: &Path, target: &str) -> Option<serde_json::Value> {
        fs::read_to_string(models_path(home, target))
            .ok()
            .map(|text| json(&text))
    }

    fn gpt_details() -> Catalog {
        Catalog::from([(
            GPT.to_string(),
            ModelDetails {
                reasoning: Some(levels(&["none", "low", "medium", "high", "xhigh"])),
                context_window: Some(400_000),
                max_tokens: Some(128_000),
            },
        )])
    }

    fn chat_entry(id: &str, origin: &str) -> serde_json::Value {
        serde_json::json!({
            "id": id,
            "name": id,
            "vendor": "AstrLink",
            "url": format!("{origin}/v1/chat/completions"),
            "apiKey": TOKEN,
        })
    }

    fn responses_entry(origin: &str) -> serde_json::Value {
        serde_json::json!({
            "id": GPT,
            "name": GPT,
            "vendor": "AstrLink",
            "url": format!("{origin}/v1"),
            "api": "openai-responses",
            "apiKey": TOKEN,
            "maxInputTokens": 400_000,
            "maxOutputTokens": 128_000,
            "supportsReasoning": true,
            "reasoning": {"supportedEfforts": ["low", "medium", "high", "xhigh"]},
        })
    }

    #[test]
    fn writes_an_entry_per_model_in_each_edition() {
        let home = unique_home("workbuddy");
        let local = Home::local(&home);
        let cn = Place::new(WORKBUDDY, Some("cn")).unwrap();
        let intl = Place::new(WORKBUDDY, Some("intl")).unwrap();
        fs::create_dir_all(home.join(".workbuddy")).unwrap();
        fs::create_dir_all(home.join(".workbuddy-ai")).unwrap();
        let models = vec![("model", GPT.to_string())];
        let list = strings(&[GPT, SONNET]);
        let protocols = BTreeMap::from([(GPT.to_string(), "openai.responses")]);
        let connection = Connection {
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        let outcome = write_with(local, cn, &connection, &gpt_details(), &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        assert_eq!(
            read(&home, "cn"),
            Some(serde_json::json!({
                "models": [responses_entry(ORIGIN), chat_entry(SONNET, ORIGIN)],
            }))
        );
        let text = fs::read_to_string(models_path(&home, "cn")).unwrap();
        assert!(text.find("\"api\"").unwrap() < text.find("\"apiKey\"").unwrap());
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mode = fs::metadata(models_path(&home, "cn"))
                .unwrap()
                .permissions()
                .mode();
            assert_eq!(mode & 0o777, 0o600);
        }
        assert_eq!(read(&home, "intl"), None);
        assert_eq!(
            current_models(WORKBUDDY, &[Some(&text)]),
            CurrentModels {
                model: Some(GPT.into()),
                models: Some(strings(&[GPT, SONNET])),
                ..Default::default()
            }
        );
        let states = status_of(local, [WORKBUDDY], Some(ORIGIN), Some(&tokens()))
            .unwrap()
            .into_iter()
            .map(|status| (status.target, status.state))
            .collect::<Vec<_>>();
        assert_eq!(
            states,
            [
                (Some("cn"), ClientState::Configured),
                (Some("intl"), ClientState::NotConfigured),
            ]
        );

        // The international edition is a place of its own.
        write_with(local, intl, &connection, &gpt_details(), &tokens(), false).unwrap();
        assert_eq!(read(&home, "intl"), read(&home, "cn"));

        // A new port moves every entry, each in the form its API needs.
        let moved = "http://127.0.0.1:9000";
        sync(local, &format!("{moved}/"), &tokens()).unwrap();
        for target in ["cn", "intl"] {
            assert_eq!(
                read(&home, target),
                Some(serde_json::json!({
                    "models": [responses_entry(moved), chat_entry(SONNET, moved)],
                }))
            );
        }
        let states = status_of(local, [WORKBUDDY], Some(moved), Some(&tokens())).unwrap();
        assert!(states
            .iter()
            .all(|status| status.state == ClientState::Configured));

        // A created file goes once AstrLink's entries do.
        remove(local, cn).unwrap();
        assert!(!models_path(&home, "cn").exists());
        assert!(models_path(&home, "intl").exists());
        remove(local, intl).unwrap();
        assert!(!models_path(&home, "intl").exists());
        fs::remove_dir_all(home).unwrap();
    }

    /// What WorkBuddy's model settings leave once they saved the file.
    const SAVED: &str = r#"[
  {
    "id": "deepseek-v4",
    "name": "DeepSeek V4",
    "vendor": "DeepSeek",
    "url": "https://api.deepseek.com/v1/chat/completions",
    "apiKey": "sk-deepseek",
    "supportsToolCall": true,
    "maxInputTokens": 128000
  }
]"#;

    #[test]
    fn keeps_a_saved_list_a_list() {
        let home = unique_home("workbuddy-list");
        let local = Home::local(&home);
        let cn = Place::new(WORKBUDDY, Some("cn")).unwrap();
        fs::create_dir_all(home.join(".workbuddy")).unwrap();
        fs::write(models_path(&home, "cn"), SAVED).unwrap();
        let models = vec![("model", SONNET.to_string())];
        let outcome = write(local, cn, &connection(&models), &tokens(), false).unwrap();
        assert_eq!(asked(outcome), None);
        let mut expected = json(SAVED);
        expected
            .as_array_mut()
            .unwrap()
            .push(chat_entry(SONNET, ORIGIN));
        assert_eq!(read(&home, "cn"), Some(expected));
        let text = fs::read_to_string(models_path(&home, "cn")).unwrap();
        assert_eq!(
            current_models(WORKBUDDY, &[Some(&text)]).models,
            Some(strings(&[SONNET]))
        );

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert_eq!(
            read(&home, "cn").unwrap()[1]["url"],
            "http://127.0.0.1:9000/v1/chat/completions"
        );
        assert_eq!(read(&home, "cn").unwrap()[0], json(SAVED)[0]);

        remove(local, cn).unwrap();
        assert_eq!(fs::read_to_string(models_path(&home, "cn")).unwrap(), SAVED);
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn asks_before_replacing_an_entry_of_the_same_id() {
        let home = unique_home("workbuddy-conflict");
        let local = Home::local(&home);
        let cn = Place::new(WORKBUDDY, Some("cn")).unwrap();
        fs::create_dir_all(home.join(".workbuddy")).unwrap();
        let original = r#"{
  "models": [
    {
      "id": "claude-sonnet-5",
      "name": "Sonnet via relay",
      "vendor": "Relay",
      "url": "https://relay.example/v1/chat/completions",
      "apiKey": "sk-relay"
    }
  ],
  "availableModels": ["claude-sonnet-5", "hunyuan-turbos"]
}
"#;
        fs::write(models_path(&home, "cn"), original).unwrap();
        let models = vec![("model", GPT.to_string())];
        let list = strings(&[GPT, SONNET]);
        let connection = listed(&models, &list);
        assert_eq!(
            asked(write(local, cn, &connection, &tokens(), false).unwrap()),
            Some((strings(&["models[claude-sonnet-5]"]), None))
        );
        assert_eq!(
            fs::read_to_string(models_path(&home, "cn")).unwrap(),
            original
        );
        write(local, cn, &connection, &tokens(), true).unwrap();
        let written = read(&home, "cn").unwrap();
        assert_eq!(
            written["models"],
            serde_json::json!([chat_entry(SONNET, ORIGIN), chat_entry(GPT, ORIGIN)])
        );
        assert_eq!(
            written["availableModels"],
            serde_json::json!(["claude-sonnet-5", "hunyuan-turbos", GPT])
        );

        // The relay's entry comes back with the file AstrLink found.
        remove(local, cn).unwrap();
        assert_eq!(read(&home, "cn"), Some(json(original)));
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn leaves_a_file_with_comments_alone() {
        let home = unique_home("workbuddy-invalid");
        let local = Home::local(&home);
        let cn = Place::new(WORKBUDDY, Some("cn")).unwrap();
        fs::create_dir_all(home.join(".workbuddy")).unwrap();
        let original = "{\n  // mine\n  \"models\": []\n}\n";
        fs::write(models_path(&home, "cn"), original).unwrap();
        let models = vec![("model", GPT.to_string())];
        assert!(write(local, cn, &connection(&models), &tokens(), false).is_err());
        assert_eq!(
            fs::read_to_string(models_path(&home, "cn")).unwrap(),
            original
        );
        let state = status_of(local, [WORKBUDDY], Some(ORIGIN), Some(&tokens())).unwrap()[0].state;
        assert_eq!(state, ClientState::Invalid);
        fs::remove_dir_all(home).unwrap();
    }
}
