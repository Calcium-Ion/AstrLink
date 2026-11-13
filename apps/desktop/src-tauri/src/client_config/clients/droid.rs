//! Droid reads `~/.factory/settings.json`. Its models are `customModels`
//! entries, each with its own address and key; AstrLink's are the ones that
//! reach a local gateway with an AstrLink token, and lead the list. The
//! default model is `sessionDefaultSettings.model`. Droid tells the model
//! its `displayName`, so that is the bare model id.

use std::{fs, path::PathBuf};

use serde_json::Value as Json;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{json, Document, Format},
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Droid);

struct Droid;

/// `settings.json`.
struct Settings;

const MODELS_KEY: &str = "customModels";
const DEFAULT_KEY: &str = "sessionDefaultSettings.model";
const ID_PREFIX: &str = "custom:";
/// Droid's overrides beside `settings.json`.
const LOCAL_FILE: &str = "settings.local.json";

/// Droid's `provider` for a gateway protocol.
fn provider(protocol: &str) -> &'static str {
    match protocol {
        "anthropic.messages" => "anthropic",
        "openai.responses" => "openai",
        _ => "generic-chat-completion-api",
    }
}

/// Anthropic's SDK adds `/v1` itself; the OpenAI ones take it.
fn entry_url(provider: &str, origin: &str) -> String {
    if provider == "anthropic" {
        origin.to_string()
    } else {
        format!("{origin}/v1")
    }
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 8] = [
        "id",
        "model",
        "displayName",
        "baseUrl",
        "apiKey",
        "provider",
        "maxContextLimit",
        "maxOutputTokens",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

/// An entry that sends an AstrLink token to a gateway on this computer.
fn owned(entry: &Json) -> bool {
    let text = |name| entry.get(name).and_then(Json::as_str).unwrap_or_default();
    text("apiKey").starts_with("astr_")
        && ["http://127.0.0.1:", "http://localhost:"]
            .iter()
            .any(|origin| text("baseUrl").starts_with(origin))
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
    let provider = provider(protocol);
    let mut entry = serde_json::json!({
        "id": format!("{ID_PREFIX}{id}"),
        "model": id,
        "displayName": id,
        "baseUrl": entry_url(provider, connection.origin),
        "apiKey": connection.token,
        "provider": provider,
    });
    if let Some(details) = details.get(id) {
        if let Some(context) = details.context_window {
            entry["maxContextLimit"] = context.into();
        }
        if let Some(output) = details.max_tokens {
            entry["maxOutputTokens"] = output.into();
        }
    }
    entry
}

impl ClientSpec for Droid {
    fn id(&self) -> &'static str {
        "droid"
    }

    fn name(&self) -> &'static str {
        "Droid"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".factory")
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&Settings]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self.dir(home, target).join("settings.json")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// Each entry carries its own address and key.
    fn holds_connection(&self, key: &str) -> bool {
        key == MODELS_KEY
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let entries = owned_entries(config);
        let mut ids: Vec<String> = Vec::new();
        for id in entries
            .iter()
            .filter_map(|entry| entry.get("model")?.as_str())
            .filter_map(|id| clean_model(id.into()))
        {
            if !ids.contains(&id) {
                ids.push(id);
            }
        }
        let chosen = config.get(DEFAULT_KEY).flatten().and_then(|chosen| {
            let entry = entries
                .iter()
                .find(|entry| entry.get("id").and_then(Json::as_str) == Some(chosen.as_str()))?;
            entry.get("model")?.as_str().map(str::to_string)
        });
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
        if !entries.is_empty() {
            keys.push((MODELS_KEY.into(), Json::Array(entries).to_string()));
        }
        if let Some(model) = connection.model("model") {
            keys.push((DEFAULT_KEY.into(), format!("{ID_PREFIX}{model}")));
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
            let provider = entry
                .get("provider")
                .and_then(Json::as_str)
                .unwrap_or_default()
                .to_string();
            entry["baseUrl"] = entry_url(&provider, origin).into();
        }
        vec![(MODELS_KEY.into(), Json::Array(entries).to_string())]
    }

    /// `settings.local.json` overrides the models or the default.
    fn notes(
        &self,
        home: Home,
        target: Option<&str>,
        _config: Option<&Config>,
    ) -> Vec<&'static str> {
        let comments = jsonc_parser::ParseOptions {
            allow_comments: true,
            ..json::strict()
        };
        let local = fs::read_to_string(self.dir(home, target).join(LOCAL_FILE))
            .ok()
            .and_then(|text| json::JsonDocument::parse(&text, Some(&comments), &[], false).ok())
            .and_then(|document| document.value());
        let overrides = local.is_some_and(|local| {
            local.get(MODELS_KEY).is_some()
                || local
                    .get("sessionDefaultSettings")
                    .and_then(|settings| settings.get("model"))
                    .is_some()
        });
        if overrides {
            vec!["overridden"]
        } else {
            Vec::new()
        }
    }
}

impl ConfigFile for Settings {
    fn format(&self) -> Format {
        Format::Json {
            syntax: Some(jsonc_parser::ParseOptions {
                allow_comments: true,
                ..json::strict()
            }),
            objects: &["sessionDefaultSettings"],
            root_list: false,
        }
    }

    /// The list reads as AstrLink's entries in it, compact.
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Json(json) = document else {
            return None;
        };
        if key != MODELS_KEY {
            return document.get(key);
        }
        let entries: Vec<Json> = json
            .get_json(key)??
            .as_array()?
            .iter()
            .filter(|entry| owned(entry))
            .cloned()
            .collect();
        (!entries.is_empty()).then(|| Some(Json::Array(entries).to_string()))
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Json(json) = document else {
            return document.set(key, value);
        };
        if key != MODELS_KEY {
            return json.set(key, value);
        }
        let entries: Vec<Json> = match serde_json::from_str(value) {
            Ok(Json::Array(entries)) => entries,
            _ => return Err(format!("{key} is not a list")),
        };
        json.remove_items(key, owned);
        json.prepend_items(key, &entries, rank)
    }

    fn remove(&self, document: &mut Document, key: &str) {
        match document {
            Document::Json(json) if key == MODELS_KEY => json.remove_items(key, owned),
            _ => document.remove(key),
        }
    }

    /// An emptied list goes, then the object the default sat in.
    fn tidy(&self, document: &mut Document) {
        if let Document::Json(json) = document {
            if json
                .get_json(MODELS_KEY)
                .flatten()
                .is_some_and(|value| value.as_array().is_some_and(Vec::is_empty))
            {
                json.remove(MODELS_KEY);
            }
            json.drop_empty(&["sessionDefaultSettings"]);
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn settings(home: &Path) -> PathBuf {
        home.join(".factory/settings.json")
    }

    fn read(home: &Path) -> serde_json::Value {
        read_json(
            DROID.files()[0],
            &fs::read_to_string(settings(home)).unwrap(),
        )
        .unwrap()
    }

    #[test]
    fn writes_an_entry_per_model_with_its_provider() {
        let home = unique_home("droid");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".factory")).unwrap();
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
            "claude-sonnet-5".to_string(),
            ModelDetails {
                context_window: Some(200_000),
                max_tokens: Some(64_000),
                ..Default::default()
            },
        )]);
        let outcome = write_with(local, DROID, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        let entry = |id: &str, url: &str, provider: &str| {
            serde_json::json!({
                "id": format!("custom:{id}"),
                "model": id,
                "displayName": id,
                "baseUrl": url,
                "apiKey": TOKEN,
                "provider": provider,
            })
        };
        let mut claude = entry("claude-sonnet-5", ORIGIN, "anthropic");
        claude["maxContextLimit"] = 200_000.into();
        claude["maxOutputTokens"] = 64_000.into();
        let v1 = format!("{ORIGIN}/v1");
        assert_eq!(
            read(&home),
            serde_json::json!({
                "customModels": [
                    claude,
                    entry("gpt-5.6", &v1, "openai"),
                    entry("glm-5", &v1, "generic-chat-completion-api"),
                ],
                "sessionDefaultSettings": {"model": "custom:claude-sonnet-5"},
            })
        );
        let status = &status_of(local, [DROID], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-sonnet-5"));
        assert!(status.notes.is_empty());

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = read(&home);
        assert_eq!(
            synced["customModels"][0]["baseUrl"],
            "http://127.0.0.1:9000"
        );
        assert_eq!(
            synced["customModels"][2]["baseUrl"],
            "http://127.0.0.1:9000/v1"
        );

        fs::write(
            home.join(".factory/settings.local.json"),
            r#"{"sessionDefaultSettings": {"model": "gpt-5.5"}}"#,
        )
        .unwrap();
        let status = &status_of(
            local,
            [DROID],
            Some("http://127.0.0.1:9000"),
            Some(&tokens()),
        )
        .unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.notes, ["overridden"]);

        remove(local, DROID).unwrap();
        assert!(!settings(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = r#"{
  "customModels": [
    { "model": "kimi-k3", "displayName": "Kimi", "baseUrl": "https://api.moonshot.cn/v1",
      "apiKey": "sk-moon", "provider": "generic-chat-completion-api" }
  ],
  "sessionDefaultSettings": { "model": "custom:Kimi-0", "autonomyMode": "auto-low" },
  "cloudSessionSync": false
}
"#;

    #[test]
    fn leads_the_users_models_and_puts_their_default_back() {
        let home = unique_home("droid-merge");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".factory")).unwrap();
        fs::write(settings(&home), USER).unwrap();
        let models = vec![("model", "glm-5".to_string())];
        assert_eq!(
            asked(write(local, DROID, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["sessionDefaultSettings.model"]), None))
        );
        write(local, DROID, &connection(&models), &tokens(), true).unwrap();
        let written = read(&home);
        assert_eq!(written["customModels"][0]["id"], "custom:glm-5");
        assert_eq!(written["customModels"][1]["apiKey"], "sk-moon");
        assert_eq!(
            written["sessionDefaultSettings"]["autonomyMode"],
            "auto-low"
        );
        assert_eq!(written["cloudSessionSync"], false);

        remove(local, DROID).unwrap();
        assert_eq!(read(&home), read_json(DROID.files()[0], USER).unwrap());
        fs::remove_dir_all(home).unwrap();
    }
}
