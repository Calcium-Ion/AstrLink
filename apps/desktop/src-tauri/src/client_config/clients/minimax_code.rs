//! MiniMax Code, CLI and desktop app alike, reads `~/.minimax/config.yaml`.
//! A custom provider has one `api`, so AstrLink adds one per protocol its
//! models use under `custom_provider`, and picks the default in
//! `defaultModel`. Changes apply on the next start.

use std::path::PathBuf;

use serde_json::Value as Json;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{
            yaml::{Step, YamlRoot},
            Document, Format,
        },
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&MiniMaxCode);

struct MiniMaxCode;

/// `config.yaml`.
struct MiniMaxConfig;

/// Protocol, provider key, the name MiniMax Code shows, and `api`.
const ROUTES: [(&str, &str, &str, &str); 3] = [
    (
        "openai.chat",
        "local-chat",
        "AstrLink",
        "openai-completions",
    ),
    (
        "openai.responses",
        "local-responses",
        "AstrLink Responses",
        "openai-responses",
    ),
    (
        "anthropic.messages",
        "local-anthropic",
        "AstrLink Anthropic",
        "anthropic-messages",
    ),
];
const PARENT: &str = "custom_provider";
const PREFIX: &str = "custom_provider.";
const DEFAULT_KEY: &str = "defaultModel";
/// What a `defaultModel` naming a custom provider starts with.
const CHOICE_PREFIX: &str = "custom_provider:";
/// What MiniMax Code assumes of a model it is told no limits of.
const DEFAULT_CONTEXT: u64 = 200_000;
const DEFAULT_OUTPUT: u64 = 16_384;

fn provider_key(id: &str) -> String {
    format!("{PREFIX}{id}")
}

fn provider_of(key: &str) -> Option<&'static str> {
    let id = key.strip_prefix(PREFIX)?;
    ROUTES
        .iter()
        .map(|(_, known, ..)| *known)
        .find(|known| *known == id)
}

/// MiniMax Code adds `/v1/messages` to an Anthropic URL, and the rest to the
/// others.
fn provider_url(api: &str, origin: &str) -> String {
    if api == "anthropic-messages" {
        origin.to_string()
    } else {
        format!("{origin}/v1")
    }
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 14] = [
        "name",
        "kind",
        "enabled",
        "api",
        "options",
        "baseURL",
        "apiKey",
        "models",
        "reasoning",
        "thinking",
        "effortOptions",
        "limit",
        "context",
        "output",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn json_at(config: &Config, key: &str) -> Option<Json> {
    serde_json::from_str(&config.get(key).flatten()?).ok()
}

fn model_entry(id: &str, details: &Catalog) -> Json {
    let mut entry = serde_json::json!({ "name": id, "enabled": true });
    let Some(details) = details.get(id) else {
        return entry;
    };
    if let Some(levels) = details
        .reasoning
        .as_ref()
        .filter(|levels| !levels.is_empty())
    {
        entry["reasoning"] = true.into();
        entry["thinking"] = serde_json::json!({ "effortOptions": levels });
    }
    if details.context_window.is_some() || details.max_tokens.is_some() {
        entry["limit"] = serde_json::json!({
            "context": details.context_window.unwrap_or(DEFAULT_CONTEXT),
            "output": details.max_tokens.unwrap_or(DEFAULT_OUTPUT),
        });
    }
    entry
}

impl ClientSpec for MiniMaxCode {
    fn id(&self) -> &'static str {
        "minimax_code"
    }

    fn name(&self) -> &'static str {
        "MiniMax Code"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `MINIMAX_DATA_DIR` and a profile's own folder are out of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".minimax")
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&MiniMaxConfig]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self.dir(home, target).join("config.yaml")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// Each provider holds an address and the token.
    fn holds_connection(&self, key: &str) -> bool {
        provider_of(key).is_some()
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let mut ids: Vec<String> = Vec::new();
        for (_, id, ..) in ROUTES {
            let Some(provider) = json_at(config, &provider_key(id)) else {
                continue;
            };
            for model in provider
                .get("models")
                .and_then(Json::as_object)
                .into_iter()
                .flat_map(|models| models.keys())
                .filter_map(|model| clean_model(model.clone()))
            {
                if !ids.contains(&model) {
                    ids.push(model);
                }
            }
        }
        let chosen = config
            .get(DEFAULT_KEY)
            .flatten()
            .and_then(|chosen| {
                let (provider, model) = chosen.strip_prefix(CHOICE_PREFIX)?.split_once('/')?;
                provider_of(&provider_key(provider)).map(|_| model.to_string())
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
        let mut keys = Vec::new();
        for (protocol, id, name, api) in ROUTES {
            let models: serde_json::Map<String, Json> = list
                .iter()
                .filter(|model| connection.protocol(self, model) == protocol)
                .map(|model| (model.clone(), model_entry(model, details)))
                .collect();
            if models.is_empty() {
                continue;
            }
            let provider = serde_json::json!({
                "name": name,
                "kind": "custom",
                "enabled": true,
                "api": api,
                "options": { "baseURL": provider_url(api, connection.origin), "apiKey": connection.token },
                "models": models,
            });
            keys.push((provider_key(id), provider.to_string()));
        }
        if let Some(model) = connection.model("model") {
            let protocol = connection.protocol(self, &model);
            if let Some((_, id, ..)) = ROUTES.iter().find(|(known, ..)| *known == protocol) {
                keys.push((DEFAULT_KEY.into(), format!("{CHOICE_PREFIX}{id}/{model}")));
            }
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
            .filter(|key| provider_of(key).is_some())
            .filter_map(|key| {
                let mut provider = json_at(config, key)?;
                let api = provider.get("api")?.as_str()?.to_string();
                provider["options"]["baseURL"] = provider_url(&api, origin).into();
                Some((key.clone(), provider.to_string()))
            })
            .collect()
    }
}

impl ConfigFile for MiniMaxConfig {
    fn format(&self) -> Format {
        Format::Yaml {
            root: YamlRoot::Mapping,
        }
    }

    /// MiniMax Code keeps the file to its owner.
    fn secret(&self) -> bool {
        true
    }

    /// A provider reads as one JSON object.
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        match (document, provider_of(key)) {
            (Document::Yaml(yaml), Some(id)) => yaml
                .get_json(&[Step::Key(PARENT), Step::Key(id)])
                .map(|value| Some(value.to_string())),
            _ => document.get(key),
        }
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let (Document::Yaml(yaml), Some(id)) = (&mut *document, provider_of(key)) else {
            return document.set(key, value);
        };
        let value: Json = serde_json::from_str(value).map_err(|error| error.to_string())?;
        if yaml.get(&[Step::Key(PARENT)]).is_some() {
            yaml.set(&[Step::Key(PARENT)], id, &value, rank)
        } else {
            yaml.set(&[], PARENT, &serde_json::json!({ id: value }), rank)
        }
    }

    /// An emptied `custom_provider` goes.
    fn remove(&self, document: &mut Document, key: &str) {
        let (Document::Yaml(yaml), Some(id)) = (&mut *document, provider_of(key)) else {
            return document.remove(key);
        };
        let _ = yaml.remove(&[Step::Key(PARENT), Step::Key(id)]);
        if yaml
            .get_json(&[Step::Key(PARENT)])
            .is_some_and(|value| value.as_object().is_some_and(serde_json::Map::is_empty))
        {
            let _ = yaml.remove(&[Step::Key(PARENT)]);
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn file(home: &Path) -> PathBuf {
        home.join(".minimax/config.yaml")
    }

    #[test]
    fn writes_a_provider_per_protocol_and_the_default() {
        let home = unique_home("minimax");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".minimax")).unwrap();
        let models = vec![("model", "gpt-5.5".to_string())];
        let list = strings(&["gpt-5.5", "claude-sonnet-5", "deepseek-v4"]);
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
                max_tokens: None,
            },
        )]);
        let outcome = write_with(local, MINIMAX_CODE, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        assert_eq!(
            fs::read_to_string(file(&home)).unwrap(),
            format!(
                r#"custom_provider:
  local-chat:
    name: AstrLink
    kind: custom
    enabled: true
    api: openai-completions
    options:
      baseURL: {ORIGIN}/v1
      apiKey: {TOKEN}
    models:
      deepseek-v4:
        name: deepseek-v4
        enabled: true
  local-responses:
    name: "AstrLink Responses"
    kind: custom
    enabled: true
    api: openai-responses
    options:
      baseURL: {ORIGIN}/v1
      apiKey: {TOKEN}
    models:
      gpt-5.5:
        name: gpt-5.5
        enabled: true
        reasoning: true
        thinking:
          effortOptions:
            - low
            - high
        limit:
          context: 400000
          output: 16384
  local-anthropic:
    name: "AstrLink Anthropic"
    kind: custom
    enabled: true
    api: anthropic-messages
    options:
      baseURL: {ORIGIN}
      apiKey: {TOKEN}
    models:
      claude-sonnet-5:
        name: claude-sonnet-5
        enabled: true
defaultModel: custom_provider:local-responses/gpt-5.5
"#
            )
        );
        let status = &status_of(local, [MINIMAX_CODE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("gpt-5.5"));

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = fs::read_to_string(file(&home)).unwrap();
        assert_eq!(
            synced
                .matches("baseURL: http://127.0.0.1:9000/v1\n")
                .count(),
            2,
            "{synced}"
        );
        assert!(
            synced.contains("baseURL: http://127.0.0.1:9000\n"),
            "{synced}"
        );

        remove(local, MINIMAX_CODE).unwrap();
        assert!(!file(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = "# my mcode\ndefaultModel: minimax/MiniMax-M3\ncustom_provider:\n  my-gateway:\n    name: My Gateway\n    kind: custom\n    api: openai-completions\n    options:\n      baseURL: https://example.invalid/v1\n      apiKey: sk-mine\n    models:\n      glm-5:\n        name: GLM 5\n";

    #[test]
    fn adds_its_provider_beside_the_users_and_puts_their_default_back() {
        let home = unique_home("minimax-merge");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".minimax")).unwrap();
        fs::write(file(&home), USER).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, MINIMAX_CODE, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["defaultModel"]), None))
        );
        write(local, MINIMAX_CODE, &connection(&models), &tokens(), true).unwrap();
        let written = fs::read_to_string(file(&home)).unwrap();
        assert!(
            written
                .starts_with("# my mcode\ndefaultModel: custom_provider:local-chat/deepseek-v4\n"),
            "{written}"
        );
        assert!(written.contains("  my-gateway:\n"), "{written}");
        assert!(written.contains("  local-chat:\n"), "{written}");

        remove(local, MINIMAX_CODE).unwrap();
        assert_eq!(fs::read_to_string(file(&home)).unwrap(), USER);
        fs::remove_dir_all(home).unwrap();
    }
}
