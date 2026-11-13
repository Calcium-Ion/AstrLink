//! Qwen Code reads `~/.qwen/settings.json` (comments allowed). Its models
//! are entries in `modelProviders.<protocol>` lists, found by id and
//! address; AstrLink's are the ones whose key is `ASTRLINK_QWEN_API_KEY`,
//! from `env` in the same file. They lead each list, so a hand-written
//! entry of the same id and address cannot take their place.

use std::path::PathBuf;

use serde_json::Value as Json;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::{Config, FileError},
        format::{json, Document, Format},
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&QwenCode);

struct QwenCode;

/// `settings.json`.
struct Settings;

/// The variable every AstrLink entry reads its key from, and so what marks
/// an entry as AstrLink's. A name of its own, since Qwen reads the shell's
/// variables first.
const KEY_NAME: &str = "ASTRLINK_QWEN_API_KEY";
const KEY_KEY: &str = "env.ASTRLINK_QWEN_API_KEY";
const AUTH_KEY: &str = "security.auth.selectedType";
const MODEL_KEY: &str = "model.name";
const MODEL_URL_KEY: &str = "model.baseUrl";
const LISTS: [&str; 3] = [
    "modelProviders.openai",
    "modelProviders.anthropic",
    "modelProviders.gemini",
];

/// The list, auth type, and `wireApi` a protocol's models take.
fn route(protocol: &str) -> (&'static str, &'static str, Option<&'static str>) {
    match protocol {
        "anthropic.messages" => (LISTS[1], "anthropic", None),
        "google.generate_content" => (LISTS[2], "gemini", None),
        "openai.responses" => (LISTS[0], "openai-responses", Some("responses")),
        _ => (LISTS[0], "openai", None),
    }
}

/// Anthropic and Gemini entries take the bare origin; OpenAI ones `/v1`.
fn entry_url(list: &str, origin: &str) -> String {
    if list == LISTS[0] {
        format!("{origin}/v1")
    } else {
        origin.to_string()
    }
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 9] = [
        "id",
        "name",
        "description",
        "baseUrl",
        "envKey",
        "wireApi",
        "generationConfig",
        "contextWindowSize",
        "samplingParams",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn owned(entry: &Json) -> bool {
    entry.get("envKey").and_then(Json::as_str) == Some(KEY_NAME)
}

/// AstrLink's entries in the list at `list`, in order.
fn owned_entries(config: &Config, list: &str) -> Vec<Json> {
    config
        .get(list)
        .flatten()
        .and_then(|value| serde_json::from_str::<Json>(&value).ok())
        .and_then(|value| value.as_array().cloned())
        .unwrap_or_default()
}

/// The entry for `id`, with what the catalog says of its limits. Its name
/// and description label it in Qwen's model picker only.
fn entry(id: &str, list: &str, origin: &str, wire: Option<&str>, details: &Catalog) -> Json {
    let mut entry = serde_json::json!({
        "id": id,
        "name": id,
        "description": "AstrLink",
        "baseUrl": entry_url(list, origin),
        "envKey": KEY_NAME,
    });
    if let Some(wire) = wire {
        entry["wireApi"] = wire.into();
    }
    if let Some(details) = details.get(id) {
        let mut generation = serde_json::Map::new();
        if let Some(context) = details.context_window {
            generation.insert("contextWindowSize".into(), context.into());
        }
        if let Some(output) = details.max_tokens {
            generation.insert(
                "samplingParams".into(),
                serde_json::json!({ "max_tokens": output }),
            );
        }
        if !generation.is_empty() {
            entry["generationConfig"] = Json::Object(generation);
        }
    }
    entry
}

impl ClientSpec for QwenCode {
    fn id(&self) -> &'static str {
        "qwen_code"
    }

    fn name(&self) -> &'static str {
        "Qwen Code"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &[
            "openai.chat",
            "openai.responses",
            "anthropic.messages",
            "google.generate_content",
        ]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `QWEN_HOME` moves it, from the shell or a `.env`; AstrLink writes
    /// the default one.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".qwen")
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
        &[KEY_KEY]
    }

    /// Each entry carries its own address.
    fn holds_connection(&self, key: &str) -> bool {
        LISTS.contains(&key)
    }

    /// The model Qwen starts with when it is one of AstrLink's, else the
    /// first of them.
    fn current_models(&self, config: &Config) -> CurrentModels {
        let entries: Vec<Json> = LISTS
            .iter()
            .flat_map(|list| owned_entries(config, list))
            .collect();
        let mut ids: Vec<String> = Vec::new();
        for id in entries
            .iter()
            .filter_map(|entry| entry.get("id")?.as_str())
            .filter_map(|id| clean_model(id.into()))
        {
            if !ids.contains(&id) {
                ids.push(id);
            }
        }
        let url = config.get(MODEL_URL_KEY).flatten();
        let chosen = config.model(MODEL_KEY).filter(|model| {
            entries.iter().any(|entry| {
                entry.get("id").and_then(Json::as_str) == Some(model.as_str())
                    && url.as_deref().map_or(true, |url| {
                        entry.get("baseUrl").and_then(Json::as_str) == Some(url)
                    })
            })
        });
        CurrentModels {
            model: chosen.or_else(|| ids.first().cloned()),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let default = connection.model("model");
        let mut ordered = connection.list();
        if let Some(at) = default
            .as_ref()
            .and_then(|model| ordered.iter().position(|id| id == model))
        {
            let model = ordered.remove(at);
            ordered.insert(0, model);
        }
        let mut lists: Vec<(&str, Vec<Json>)> =
            LISTS.iter().map(|list| (*list, Vec::new())).collect();
        for id in &ordered {
            let (list, _, wire) = route(connection.protocol(self, id));
            let entry = entry(id, list, connection.origin, wire, details);
            if let Some((_, entries)) = lists.iter_mut().find(|(known, _)| *known == list) {
                entries.push(entry);
            }
        }
        let mut keys: Vec<(String, String)> = lists
            .into_iter()
            .filter(|(_, entries)| !entries.is_empty())
            .map(|(list, entries)| (list.to_string(), Json::Array(entries).to_string()))
            .collect();
        keys.push((KEY_KEY.into(), connection.token.into()));
        if let Some(model) = default {
            let (list, auth, _) = route(connection.protocol(self, &model));
            keys.push((AUTH_KEY.into(), auth.into()));
            keys.push((MODEL_KEY.into(), model));
            keys.push((MODEL_URL_KEY.into(), entry_url(list, connection.origin)));
        }
        keys
    }

    fn start_file(&self, config: &mut Config) -> Result<(), FileError> {
        if let Some(Document::Json(json)) = config.documents.first_mut() {
            json.set_json("$version", &Json::from(4), rank)
                .map_err(|message| FileError {
                    file: 0,
                    message,
                    line: None,
                })?;
        }
        Ok(())
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
        let mut keys = Vec::new();
        for list in LISTS {
            if !record.keys.contains_key(list) {
                continue;
            }
            let mut entries = owned_entries(config, list);
            for entry in &mut entries {
                entry["baseUrl"] = entry_url(list, origin).into();
            }
            if !entries.is_empty() {
                keys.push((list.to_string(), Json::Array(entries).to_string()));
            }
        }
        // The default model's address follows when it is AstrLink's.
        let old = record.base_url.trim_end_matches("/v1");
        match config.get(MODEL_URL_KEY).flatten() {
            Some(url) if url == old => keys.push((MODEL_URL_KEY.into(), origin.into())),
            Some(url) if url == record.base_url => {
                keys.push((MODEL_URL_KEY.into(), format!("{origin}/v1")))
            }
            _ => {}
        }
        keys
    }
}

impl ConfigFile for Settings {
    /// Qwen strips comments before a strict parse.
    fn format(&self) -> Format {
        Format::Json {
            syntax: Some(jsonc_parser::ParseOptions {
                allow_comments: true,
                ..json::strict()
            }),
            objects: &[
                "modelProviders",
                "env",
                "security",
                "security.auth",
                "model",
            ],
            root_list: false,
        }
    }

    /// A list key reads as AstrLink's entries in it, compact.
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Json(json) = document else {
            return None;
        };
        if !LISTS.contains(&key) {
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
        if !LISTS.contains(&key) {
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
            Document::Json(json) if LISTS.contains(&key) => json.remove_items(key, owned),
            _ => document.remove(key),
        }
    }

    /// Emptied lists go, then the objects around them.
    fn tidy(&self, document: &mut Document) {
        if let Document::Json(json) = document {
            for list in LISTS {
                if json
                    .get_json(list)
                    .flatten()
                    .is_some_and(|value| value.as_array().is_some_and(Vec::is_empty))
                {
                    json.remove(list);
                }
            }
            json.drop_empty(&[
                "modelProviders",
                "env",
                "security.auth",
                "security",
                "model",
            ]);
        }
    }

    /// `$version` alone is left.
    fn is_empty(&self, document: &Document) -> bool {
        match document {
            Document::Json(json) => json.holds_only(|name, _| name == Some("$version")),
            _ => document.is_empty(),
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn settings(home: &Path) -> PathBuf {
        home.join(".qwen/settings.json")
    }

    fn read(home: &Path) -> Option<serde_json::Value> {
        fs::read_to_string(settings(home))
            .ok()
            .map(|text| read_json(QWEN_CODE.files()[0], &text).unwrap())
    }

    #[test]
    fn writes_each_model_into_its_protocols_list() {
        let home = unique_home("qwen");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".qwen")).unwrap();
        let models = vec![("model", "claude-sonnet-5".to_string())];
        let list = strings(&["gpt-5.6-sol", "glm-5", "claude-sonnet-5", "gemini-3.6-pro"]);
        let protocols = BTreeMap::from([
            ("gpt-5.6-sol".to_string(), "openai.responses"),
            ("claude-sonnet-5".to_string(), "anthropic.messages"),
            ("gemini-3.6-pro".to_string(), "google.generate_content"),
        ]);
        let chosen = Connection {
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        let details = Catalog::from([(
            "glm-5".to_string(),
            ModelDetails {
                context_window: Some(200_000),
                max_tokens: Some(32_000),
                ..Default::default()
            },
        )]);
        let outcome = write_with(local, QWEN_CODE, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        let entry = |id: &str, url: &str| {
            serde_json::json!({
                "id": id,
                "name": id,
                "description": "AstrLink",
                "baseUrl": url,
                "envKey": "ASTRLINK_QWEN_API_KEY",
            })
        };
        let mut responses = entry("gpt-5.6-sol", &format!("{ORIGIN}/v1"));
        responses["wireApi"] = "responses".into();
        let mut chat = entry("glm-5", &format!("{ORIGIN}/v1"));
        chat["generationConfig"] = serde_json::json!({"contextWindowSize": 200_000, "samplingParams": {"max_tokens": 32_000}});
        assert_eq!(
            read(&home),
            Some(serde_json::json!({
                "$version": 4,
                "modelProviders": {
                    "openai": [responses, chat],
                    "anthropic": [entry("claude-sonnet-5", ORIGIN)],
                    "gemini": [entry("gemini-3.6-pro", ORIGIN)],
                },
                "env": {"ASTRLINK_QWEN_API_KEY": TOKEN},
                "security": {"auth": {"selectedType": "anthropic"}},
                "model": {"name": "claude-sonnet-5", "baseUrl": ORIGIN},
            }))
        );
        let status = &status_of(local, [QWEN_CODE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-sonnet-5"));
        assert_eq!(
            status.models.models,
            Some(strings(&[
                "claude-sonnet-5",
                "gpt-5.6-sol",
                "glm-5",
                "gemini-3.6-pro"
            ]))
        );

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = read(&home).unwrap();
        assert_eq!(
            synced["modelProviders"]["openai"][0]["baseUrl"],
            "http://127.0.0.1:9000/v1"
        );
        assert_eq!(
            synced["modelProviders"]["anthropic"][0]["baseUrl"],
            "http://127.0.0.1:9000"
        );
        assert_eq!(synced["model"]["baseUrl"], "http://127.0.0.1:9000");
        let status = &status_of(
            local,
            [QWEN_CODE],
            Some("http://127.0.0.1:9000"),
            Some(&tokens()),
        )
        .unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);

        remove(local, QWEN_CODE).unwrap();
        assert!(!settings(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = r#"{
  "$version": 4,
  // my own DashScope setup
  "general": { "vimMode": true },
  "modelProviders": {
    "openai": [
      { "id": "qwen3-coder-plus", "name": "qwen3-coder-plus",
        "baseUrl": "https://dashscope.aliyuncs.com/compatible-mode/v1",
        "description": "Qwen3-Coder via Dashscope", "envKey": "DASHSCOPE_API_KEY" }
    ]
  },
  "env": { "DASHSCOPE_API_KEY": "sk-dash" },
  "security": { "auth": { "selectedType": "qwen-oauth" } },
  "model": { "name": "coder-model" }
}
"#;

    #[test]
    fn leads_the_users_lists_and_puts_their_choice_back() {
        let home = unique_home("qwen-merge");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".qwen")).unwrap();
        fs::write(settings(&home), USER).unwrap();
        let models = vec![("model", "claude-sonnet-5".to_string())];
        let list = strings(&["claude-sonnet-5", "glm-5"]);
        let protocols = BTreeMap::from([("claude-sonnet-5".to_string(), "anthropic.messages")]);
        let chosen = Connection {
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        assert_eq!(
            asked(write(local, QWEN_CODE, &chosen, &tokens(), false).unwrap()),
            Some((strings(&["security.auth.selectedType", "model.name"]), None))
        );
        write(local, QWEN_CODE, &chosen, &tokens(), true).unwrap();
        let text = fs::read_to_string(settings(&home)).unwrap();
        assert!(text.contains("// my own DashScope setup"));
        let written = read(&home).unwrap();
        let openai = written["modelProviders"]["openai"].as_array().unwrap();
        assert_eq!(openai[0]["id"], "glm-5");
        assert_eq!(openai[1]["id"], "qwen3-coder-plus");
        assert_eq!(written["env"]["DASHSCOPE_API_KEY"], "sk-dash");
        assert_eq!(written["security"]["auth"]["selectedType"], "anthropic");
        assert_eq!(
            written["model"],
            serde_json::json!({"name": "claude-sonnet-5", "baseUrl": ORIGIN})
        );

        // `/model` picks another of AstrLink's models.
        let picked = text.replace(
            "\"name\": \"claude-sonnet-5\",\n    \"baseUrl\": \"http://127.0.0.1:18317\"",
            "\"name\": \"glm-5\",\n    \"baseUrl\": \"http://127.0.0.1:18317/v1\"",
        );
        assert_ne!(picked, text);
        fs::write(settings(&home), &picked).unwrap();
        let status = &status_of(local, [QWEN_CODE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("glm-5"));
        fs::write(settings(&home), &text).unwrap();

        // The values come back; a one-line object AstrLink added to stays
        // on several lines.
        remove(local, QWEN_CODE).unwrap();
        let restored = fs::read_to_string(settings(&home)).unwrap();
        assert!(restored.contains("// my own DashScope setup"));
        assert_eq!(
            read(&home),
            Some(read_json(QWEN_CODE.files()[0], USER).unwrap())
        );
        fs::remove_dir_all(home).unwrap();
    }
}
