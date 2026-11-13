//! VS Code's Copilot Chat reads custom models from
//! `<user data>/User/chatLanguageModels.json`, a list of provider groups.
//! AstrLink's is the `customendpoint` group named `AstrLink`, which the
//! model picker shows beside each model; each model carries its own URL and
//! token header. VS Code has no default model to write, so the default
//! leads the group. The `settings` VS Code keeps in the group are the
//! user's.

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

pub(crate) const CLIENT: Client = Client::new(&VscodeCopilot);

struct VscodeCopilot;

/// `chatLanguageModels.json`.
struct LanguageModels;

/// AstrLink's group, without the `settings` VS Code keeps in it.
const GROUP_KEY: &str = "AstrLink";
const VENDOR: &str = "customendpoint";
/// VS Code's own defaults for a model of unknown size.
const DEFAULT_INPUT: u64 = 128_000;
const DEFAULT_OUTPUT: u64 = 16_000;

fn ours(group: &Json) -> bool {
    group.get("vendor").and_then(Json::as_str) == Some(VENDOR)
        && group.get("name").and_then(Json::as_str) == Some(GROUP_KEY)
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 15] = [
        "name",
        "vendor",
        "models",
        "id",
        "url",
        "apiType",
        "toolCalling",
        "vision",
        "maxInputTokens",
        "maxOutputTokens",
        "thinking",
        "supportsReasoningEffort",
        "statefulResponses",
        "requestHeaders",
        "settings",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

/// The path, `apiType`, and token header for a gateway protocol.
fn route(protocol: &str) -> (&'static str, &'static str, &'static str) {
    match protocol {
        "anthropic.messages" => ("/v1/messages", "messages", "x-api-key"),
        "openai.responses" => ("/v1/responses", "responses", "Authorization"),
        _ => ("/v1/chat/completions", "chat-completions", "Authorization"),
    }
}

fn model_entry(id: &str, protocol: &str, connection: &Connection, details: &Catalog) -> Json {
    let (path, api, header) = route(protocol);
    let details = details.get(id);
    let (input, output) = details
        .and_then(|details| Some((details.context_window?, details.max_tokens?)))
        .filter(|(context, output)| context > output)
        .map_or((DEFAULT_INPUT, DEFAULT_OUTPUT), |(context, output)| {
            (context - output, output)
        });
    let token = if header == "Authorization" {
        format!("Bearer {}", connection.token)
    } else {
        connection.token.to_string()
    };
    let mut entry = serde_json::json!({
        "id": id,
        "name": id,
        "url": format!("{}{path}", connection.origin),
        "apiType": api,
        "toolCalling": true,
        "vision": false,
        "maxInputTokens": input,
        "maxOutputTokens": output,
        "requestHeaders": { header: token },
    });
    let levels: Vec<&str> = details
        .and_then(|details| details.reasoning.as_ref())
        .into_iter()
        .flatten()
        .map(String::as_str)
        .filter(|level| *level != "none")
        .collect();
    if api != "messages" && !levels.is_empty() {
        entry["thinking"] = true.into();
        entry["supportsReasoningEffort"] = levels.into();
    }
    // Otherwise VS Code sends only the new turns after a response id.
    if api == "responses" {
        entry["statefulResponses"] = false.into();
    }
    entry
}

/// Points a model entry at `origin`, keeping its path.
fn moved(entry: &mut Json, origin: &str) {
    let Some(url) = entry.get("url").and_then(Json::as_str) else {
        return;
    };
    let path = ["/v1/messages", "/v1/responses", "/v1/chat/completions"]
        .into_iter()
        .find(|path| url.ends_with(path));
    if let Some(path) = path {
        entry["url"] = format!("{origin}{path}").into();
    }
}

fn group_of(config: &Config) -> Option<Json> {
    serde_json::from_str(&config.get(GROUP_KEY).flatten()?).ok()
}

impl ClientSpec for VscodeCopilot {
    fn id(&self) -> &'static str {
        "vscode_copilot"
    }

    fn name(&self) -> &'static str {
        "VS Code Copilot"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn targets(&self) -> &'static [&'static str] {
        &["stable", "insiders"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// The edition's user directory; `--user-data-dir` and portable mode
    /// are out of reach.
    fn dir(&self, home: Home, target: Option<&str>) -> PathBuf {
        let product = match target {
            Some("insiders") => "Code - Insiders",
            _ => "Code",
        };
        let data = if cfg!(target_os = "macos") {
            home.join("Library").join("Application Support")
        } else if cfg!(windows) {
            home.join("AppData").join("Roaming")
        } else {
            home.join(".config")
        };
        data.join(product).join("User")
    }

    /// VS Code's settings live on the Windows side of a WSL home.
    fn detected(&self, home: Home, target: Option<&str>) -> bool {
        home.distribution().is_none() && self.dir(home, target).is_dir()
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&LanguageModels]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self.dir(home, target).join("chatLanguageModels.json")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// Each model holds its URL and the token.
    fn holds_connection(&self, key: &str) -> bool {
        key == GROUP_KEY
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let mut ids: Vec<String> = Vec::new();
        let group = group_of(config);
        for id in group
            .iter()
            .filter_map(|group| group.get("models")?.as_array())
            .flatten()
            .filter_map(|entry| entry.get("id")?.as_str())
            .filter_map(|id| clean_model(id.into()))
        {
            if !ids.contains(&id) {
                ids.push(id);
            }
        }
        CurrentModels {
            model: ids.first().cloned(),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let mut ordered = connection.list();
        if let Some(model) = connection.model("model") {
            ordered.retain(|id| *id != model);
            ordered.insert(0, model);
        }
        let models: Vec<Json> = ordered
            .iter()
            .map(|id| model_entry(id, connection.protocol(self, id), connection, details))
            .collect();
        if models.is_empty() {
            return Vec::new();
        }
        let group = serde_json::json!({
            "name": GROUP_KEY,
            "vendor": VENDOR,
            "models": models,
        });
        vec![(GROUP_KEY.into(), group.to_string())]
    }

    /// The file is a list; a new one starts as VS Code's own, empty.
    fn start_file(&self, config: &mut Config) -> Result<(), FileError> {
        let Format::Json {
            syntax, objects, ..
        } = LanguageModels.format()
        else {
            return Ok(());
        };
        let empty = json::JsonDocument::parse("[]\n", syntax.as_ref(), objects, true).map_err(
            |(message, line)| FileError {
                file: 0,
                message,
                line,
            },
        )?;
        if let Some(document) = config.documents.first_mut() {
            *document = Document::Json(empty);
        }
        Ok(())
    }

    /// Unused: `synced_keys` moves every model's URL.
    fn base_url_key(&self) -> &'static str {
        ""
    }

    fn synced_keys(
        &self,
        config: &Config,
        _record: &ClientRecord,
        origin: &str,
    ) -> Vec<(String, String)> {
        let Some(mut group) = group_of(config) else {
            return Vec::new();
        };
        if let Some(models) = group.get_mut("models").and_then(Json::as_array_mut) {
            for entry in models {
                moved(entry, origin);
            }
        }
        vec![(GROUP_KEY.into(), group.to_string())]
    }
}

impl ConfigFile for LanguageModels {
    /// VS Code reads it with a tolerant parser.
    fn format(&self) -> Format {
        Format::Json {
            syntax: Some(jsonc_parser::ParseOptions {
                allow_comments: true,
                allow_trailing_commas: true,
                ..json::strict()
            }),
            objects: &[],
            root_list: true,
        }
    }

    fn secret(&self) -> bool {
        true
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Json(json) = document else {
            return None;
        };
        if key != GROUP_KEY || !json.is_list() {
            return None;
        }
        let mut group = json.find_item("", ours)?;
        if let Some(group) = group.as_object_mut() {
            group.remove("settings");
        }
        Some(Some(group.to_string()))
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Json(json) = document else {
            return document.set(key, value);
        };
        if !json.is_list() {
            return Err("is an object where a list is expected".into());
        }
        let mut group: Json = serde_json::from_str(value).map_err(|error| error.to_string())?;
        let settings = json
            .find_item("", ours)
            .and_then(|current| current.get("settings").cloned());
        if let Some(settings) = settings {
            group["settings"] = settings;
        }
        json.put_item("", ours, &group, rank)
    }

    fn remove(&self, document: &mut Document, key: &str) {
        if let Document::Json(json) = document {
            if key == GROUP_KEY && json.is_list() {
                json.remove_items("", ours);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn file(home: &Path) -> PathBuf {
        VSCODE_COPILOT
            .dir(Home::local(home), Some("stable"))
            .join("chatLanguageModels.json")
    }

    fn read(home: &Path) -> serde_json::Value {
        read_json(
            VSCODE_COPILOT.files()[0],
            &fs::read_to_string(file(home)).unwrap(),
        )
        .unwrap()
    }

    fn stable() -> Place {
        Place::new(VSCODE_COPILOT, Some("stable")).unwrap()
    }

    #[test]
    fn writes_one_group_with_the_default_first() {
        let home = unique_home("vscode");
        let local = Home::local(&home);
        fs::create_dir_all(VSCODE_COPILOT.dir(local, Some("stable"))).unwrap();
        assert!(!VSCODE_COPILOT.detected(local, Some("insiders")));
        let models = vec![("model", "gpt-5.5".to_string())];
        let list = strings(&["claude-sonnet-5", "gpt-5.5", "deepseek-v4"]);
        let protocols = BTreeMap::from([
            ("gpt-5.5".to_string(), "openai.responses"),
            ("claude-sonnet-5".to_string(), "anthropic.messages"),
        ]);
        let chosen = Connection {
            target: Some("stable"),
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        let details = Catalog::from([(
            "gpt-5.5".to_string(),
            ModelDetails {
                reasoning: Some(levels(&["none", "low", "medium", "high"])),
                context_window: Some(400_000),
                max_tokens: Some(128_000),
            },
        )]);
        let outcome = write_with(local, stable(), &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        let bearer = format!("Bearer {TOKEN}");
        assert_eq!(
            read(&home),
            serde_json::json!([{
                "name": "AstrLink",
                "vendor": "customendpoint",
                "models": [
                    {"id": "gpt-5.5", "name": "gpt-5.5", "url": format!("{ORIGIN}/v1/responses"),
                     "apiType": "responses", "toolCalling": true, "vision": false,
                     "maxInputTokens": 272_000, "maxOutputTokens": 128_000, "thinking": true,
                     "supportsReasoningEffort": ["low", "medium", "high"], "statefulResponses": false,
                     "requestHeaders": {"Authorization": bearer}},
                    {"id": "claude-sonnet-5", "name": "claude-sonnet-5", "url": format!("{ORIGIN}/v1/messages"),
                     "apiType": "messages", "toolCalling": true, "vision": false,
                     "maxInputTokens": 128_000, "maxOutputTokens": 16_000,
                     "requestHeaders": {"x-api-key": TOKEN}},
                    {"id": "deepseek-v4", "name": "deepseek-v4", "url": format!("{ORIGIN}/v1/chat/completions"),
                     "apiType": "chat-completions", "toolCalling": true, "vision": false,
                     "maxInputTokens": 128_000, "maxOutputTokens": 16_000,
                     "requestHeaders": {"Authorization": bearer}},
                ],
            }])
        );
        let status = status_of(local, [VSCODE_COPILOT], Some(ORIGIN), Some(&tokens())).unwrap();
        let written = status
            .iter()
            .find(|status| status.target == Some("stable"))
            .unwrap();
        assert_eq!(written.state, ClientState::Configured);
        assert_eq!(written.models.model.as_deref(), Some("gpt-5.5"));

        // VS Code keeps a per-model option in the group; it stays.
        let text = fs::read_to_string(file(&home)).unwrap();
        let mut groups = read(&home);
        groups[0]["settings"] = serde_json::json!({"gpt-5.5": {"reasoningEffort": "high"}});
        fs::write(file(&home), serde_json::to_string_pretty(&groups).unwrap()).unwrap();
        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = read(&home);
        assert_eq!(
            synced[0]["models"][1]["url"],
            "http://127.0.0.1:9000/v1/messages"
        );
        assert_eq!(synced[0]["settings"]["gpt-5.5"]["reasoningEffort"], "high");
        let status = status_of(
            local,
            [VSCODE_COPILOT],
            Some("http://127.0.0.1:9000"),
            Some(&tokens()),
        )
        .unwrap();
        assert_eq!(status[0].state, ClientState::Configured);
        assert_ne!(text, fs::read_to_string(file(&home)).unwrap());

        remove(local, stable()).unwrap();
        assert!(!file(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = "[\n\t{\n\t\t\"name\": \"My vLLM\",\n\t\t\"vendor\": \"customendpoint\",\n\t\t\"apiKey\": \"${input:chat.lm.secret.5f2a91c0}\",\n\t\t\"models\": []\n\t}\n]";

    #[test]
    fn adds_its_group_after_the_users_and_takes_it_out_again() {
        let home = unique_home("vscode-merge");
        let local = Home::local(&home);
        fs::create_dir_all(VSCODE_COPILOT.dir(local, Some("stable"))).unwrap();
        fs::write(file(&home), USER).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        let chosen = Connection {
            target: Some("stable"),
            ..connection(&models)
        };
        assert_eq!(
            asked(write(local, stable(), &chosen, &tokens(), false).unwrap()),
            None
        );
        let written = read(&home);
        assert_eq!(written[0]["name"], "My vLLM");
        assert_eq!(written[1]["name"], "AstrLink");

        remove(local, stable()).unwrap();
        assert_eq!(
            read(&home),
            read_json(VSCODE_COPILOT.files()[0], USER).unwrap()
        );
        fs::remove_dir_all(home).unwrap();
    }
}
