//! Chatbox keeps its settings in `config.json` in its data folder: custom
//! providers in `settings.customProviders`, each one's address and models in
//! `settings.providers`, and the chat model in `settings.defaultChatModel`.
//! A provider has one `type`, so AstrLink adds one per protocol its models
//! use. Chatbox writes its whole settings back while it runs, so AstrLink
//! edits the file only while Chatbox is closed, and only once Chatbox has
//! made it.

use std::{
    fs,
    path::{Path, PathBuf},
};

use serde_json::Value as Json;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{Document, Format},
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Chatbox);

struct Chatbox;

/// `config.json`.
struct ChatboxConfig;

/// Protocol, provider id, the name Chatbox shows, and `type`.
const ROUTES: [(&str, &str, &str, &str); 3] = [
    ("openai.chat", "local-chat", "AstrLink", "openai"),
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
        "claude",
    ),
];
const LIST_KEY: &str = "settings.customProviders";
const PREFIX: &str = "settings.providers.";
const DEFAULT_KEY: &str = "settings.defaultChatModel";

fn provider_key(id: &str) -> String {
    format!("{PREFIX}{id}")
}

fn ours(id: &str) -> bool {
    ROUTES.iter().any(|(_, known, ..)| *known == id)
}

fn owned(entry: &Json) -> bool {
    entry.get("id").and_then(Json::as_str).is_some_and(ours)
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 13] = [
        "id",
        "name",
        "type",
        "isCustom",
        "apiHost",
        "apiKey",
        "models",
        "modelId",
        "capabilities",
        "contextWindow",
        "maxOutput",
        "provider",
        "model",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn json_at(config: &Config, key: &str) -> Option<Json> {
    serde_json::from_str(&config.get(key).flatten()?).ok()
}

/// Chromium's lock in the data folder while Chatbox runs: a link to
/// `<host>-<pid>` on macOS and Linux, a file gone on exit on Windows.
fn locked(dir: &Path, windows: bool) -> bool {
    if windows {
        return dir.join("lockfile").exists();
    }
    let Ok(target) = fs::read_link(dir.join("SingletonLock")) else {
        return false;
    };
    let pid = target
        .to_str()
        .and_then(|target| target.rsplit_once('-'))
        .and_then(|(_, pid)| pid.parse::<i32>().ok());
    // A lock a crash left names a process that is gone.
    pid.map_or(true, alive)
}

#[cfg(unix)]
fn alive(pid: i32) -> bool {
    // SAFETY: signal 0 only checks that the process exists.
    let sent = unsafe { libc::kill(pid, 0) } == 0;
    sent || std::io::Error::last_os_error().raw_os_error() == Some(libc::EPERM)
}

#[cfg(not(unix))]
fn alive(_pid: i32) -> bool {
    true
}

impl ClientSpec for Chatbox {
    fn id(&self) -> &'static str {
        "chatbox"
    }

    fn name(&self) -> &'static str {
        "Chatbox"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `--user-data-dir` is out of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        let data = if cfg!(target_os = "macos") {
            home.join("Library").join("Application Support")
        } else if home.windows() {
            home.join("AppData").join("Roaming")
        } else {
            home.join(".config")
        };
        data.join("xyz.chatboxapp.app")
    }

    /// A file Chatbox has not made yet would skip its first-start setup, so
    /// Chatbox counts once it has. Its settings live on the Windows side of
    /// a WSL home.
    fn detected(&self, home: Home, target: Option<&str>) -> bool {
        home.distribution().is_none() && self.dir(home, target).join("config.json").is_file()
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&ChatboxConfig]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self.dir(home, target).join("config.json")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// Each provider's settings hold an address and the token.
    fn holds_connection(&self, key: &str) -> bool {
        key.strip_prefix(PREFIX).is_some_and(ours)
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let mut ids: Vec<String> = Vec::new();
        for (_, id, ..) in ROUTES {
            let Some(provider) = json_at(config, &provider_key(id)) else {
                continue;
            };
            for model in provider
                .get("models")
                .and_then(Json::as_array)
                .into_iter()
                .flatten()
                .filter_map(|model| model.get("modelId")?.as_str())
                .filter_map(|model| clean_model(model.into()))
            {
                if !ids.contains(&model) {
                    ids.push(model);
                }
            }
        }
        let chosen = json_at(config, DEFAULT_KEY)
            .filter(|chosen| {
                chosen
                    .get("provider")
                    .and_then(Json::as_str)
                    .is_some_and(ours)
            })
            .and_then(|chosen| chosen.get("model")?.as_str().map(str::to_string))
            .and_then(clean_model);
        CurrentModels {
            model: chosen.or_else(|| ids.first().cloned()),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let list = connection.list();
        let mut entries = Vec::new();
        let mut keys = Vec::new();
        for (protocol, id, name, kind) in ROUTES {
            let models: Vec<Json> = list
                .iter()
                .filter(|model| connection.protocol(self, model) == protocol)
                .map(|model| {
                    let details = details.get(model.as_str());
                    let mut capabilities = vec!["tool_use"];
                    if details
                        .and_then(|details| details.reasoning.as_ref())
                        .is_some_and(|levels| !levels.is_empty())
                    {
                        capabilities.push("reasoning");
                    }
                    let mut entry = serde_json::json!({
                        "modelId": model,
                        "type": "chat",
                        "capabilities": capabilities,
                    });
                    if let Some(context) = details.and_then(|details| details.context_window) {
                        entry["contextWindow"] = context.into();
                    }
                    if let Some(output) = details.and_then(|details| details.max_tokens) {
                        entry["maxOutput"] = output.into();
                    }
                    entry
                })
                .collect();
            if models.is_empty() {
                continue;
            }
            entries.push(
                serde_json::json!({ "id": id, "name": name, "type": kind, "isCustom": true }),
            );
            // Chatbox adds `/v1` to Anthropic's own address only.
            let settings = serde_json::json!({
                "apiHost": self.base_url(connection.origin),
                "apiKey": connection.token,
                "models": models,
            });
            keys.push((provider_key(id), settings.to_string()));
        }
        if !entries.is_empty() {
            keys.insert(0, (LIST_KEY.into(), Json::Array(entries).to_string()));
        }
        if let Some(model) = connection.model("model") {
            let protocol = connection.protocol(self, &model);
            if let Some((_, id, ..)) = ROUTES.iter().find(|(known, ..)| *known == protocol) {
                let chosen = serde_json::json!({ "provider": id, "model": model });
                keys.push((DEFAULT_KEY.into(), chosen.to_string()));
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
            .filter(|key| self.holds_connection(key))
            .filter_map(|key| {
                let mut settings = json_at(config, key)?;
                settings["apiHost"] = self.base_url(origin).into();
                Some((key.clone(), settings.to_string()))
            })
            .collect()
    }

    fn running(&self, home: Home, target: Option<&str>) -> bool {
        locked(&self.dir(home, target), home.windows())
    }
}

impl ConfigFile for ChatboxConfig {
    fn format(&self) -> Format {
        Format::Json {
            syntax: None,
            objects: &["settings", "settings.providers"],
            root_list: false,
        }
    }

    fn secret(&self) -> bool {
        true
    }

    /// The list reads as AstrLink's entries in it; a provider and the
    /// choice as JSON.
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Json(json) = document else {
            return None;
        };
        if key == LIST_KEY {
            let entries: Vec<Json> = json
                .get_json(key)??
                .as_array()?
                .iter()
                .filter(|entry| owned(entry))
                .cloned()
                .collect();
            return (!entries.is_empty()).then(|| Some(Json::Array(entries).to_string()));
        }
        if key.starts_with(PREFIX) || key == DEFAULT_KEY {
            return json
                .get_json(key)
                .map(|value| value.map(|value| value.to_string()));
        }
        document.get(key)
    }

    /// AstrLink's entries go after the user's.
    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Json(json) = document else {
            return document.set(key, value);
        };
        if !key.starts_with(PREFIX) && key != LIST_KEY && key != DEFAULT_KEY {
            return json.set(key, value);
        }
        let value: Json = serde_json::from_str(value).map_err(|error| error.to_string())?;
        if key != LIST_KEY {
            return json.set_json(key, &value, rank);
        }
        let Json::Array(entries) = value else {
            return Err(format!("{key} is not a list"));
        };
        json.remove_items(key, |entry| {
            owned(entry)
                && !entries
                    .iter()
                    .any(|wanted| wanted.get("id") == entry.get("id"))
        });
        json.prepend_items(key, &[], rank)?;
        for entry in &entries {
            json.put_item(
                key,
                |current| current.get("id") == entry.get("id"),
                entry,
                rank,
            )?;
        }
        Ok(())
    }

    /// An emptied list goes.
    fn remove(&self, document: &mut Document, key: &str) {
        let Document::Json(json) = document else {
            return document.remove(key);
        };
        if key != LIST_KEY {
            return json.remove(key);
        }
        json.remove_items(key, owned);
        if json
            .get_json(key)
            .flatten()
            .is_some_and(|value| value.as_array().is_some_and(Vec::is_empty))
        {
            json.remove(key);
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn file(home: &Path) -> PathBuf {
        CHATBOX.dir(Home::local(home), None).join("config.json")
    }

    fn read(home: &Path) -> serde_json::Value {
        read_json(CHATBOX.files()[0], &fs::read_to_string(file(home)).unwrap()).unwrap()
    }

    /// What Chatbox makes on its first start, in short.
    const FIRST: &str = "{\n\t\"configVersion\": 15,\n\t\"configs\": {\n\t\t\"uuid\": \"f1999b34\"\n\t},\n\t\"settings\": {\n\t\t\"showModelName\": true,\n\t\t\"providers\": {\n\t\t\t\"chatbox-ai\": {}\n\t\t},\n\t\t\"__version\": 9\n\t}\n}";

    #[test]
    fn adds_a_provider_per_protocol_to_the_file_chatbox_made() {
        let home = unique_home("chatbox");
        let local = Home::local(&home);
        let models = vec![("model", "claude-sonnet-5".to_string())];
        assert!(write(local, CHATBOX, &connection(&models), &tokens(), false).is_err());
        fs::create_dir_all(file(&home).parent().unwrap()).unwrap();
        fs::write(file(&home), FIRST).unwrap();
        let list = strings(&["claude-sonnet-5", "deepseek-v4"]);
        let protocols = BTreeMap::from([("claude-sonnet-5".to_string(), "anthropic.messages")]);
        let chosen = Connection {
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        let details = Catalog::from([(
            "claude-sonnet-5".to_string(),
            ModelDetails {
                reasoning: Some(levels(&["low", "high"])),
                context_window: Some(200_000),
                max_tokens: Some(64_000),
            },
        )]);
        let outcome = write_with(local, CHATBOX, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        let written = read(&home);
        assert_eq!(
            written["settings"]["customProviders"],
            serde_json::json!([
                {"id": "local-chat", "name": "AstrLink", "type": "openai", "isCustom": true},
                {"id": "local-anthropic", "name": "AstrLink Anthropic", "type": "claude", "isCustom": true},
            ])
        );
        assert_eq!(
            written["settings"]["providers"]["local-anthropic"],
            serde_json::json!({
                "apiHost": format!("{ORIGIN}/v1"), "apiKey": TOKEN,
                "models": [{"modelId": "claude-sonnet-5", "type": "chat", "capabilities": ["tool_use", "reasoning"],
                            "contextWindow": 200_000, "maxOutput": 64_000}],
            })
        );
        assert_eq!(
            written["settings"]["defaultChatModel"],
            serde_json::json!({"provider": "local-anthropic", "model": "claude-sonnet-5"})
        );
        assert_eq!(written["settings"]["__version"], 9);
        assert!(fs::read_to_string(file(&home))
            .unwrap()
            .contains("\n\t\t\"customProviders\": [\n"));
        let status = &status_of(local, [CHATBOX], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-sonnet-5"));
        assert!(status.notes.is_empty());

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert_eq!(
            read(&home)["settings"]["providers"]["local-chat"]["apiHost"],
            "http://127.0.0.1:9000/v1"
        );

        remove(local, CHATBOX).unwrap();
        assert_eq!(read(&home), read_json(CHATBOX.files()[0], FIRST).unwrap());
        fs::remove_dir_all(home).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn waits_while_chatbox_is_open() {
        let home = unique_home("chatbox-open");
        let local = Home::local(&home);
        let dir = CHATBOX.dir(local, None);
        fs::create_dir_all(&dir).unwrap();
        fs::write(file(&home), FIRST).unwrap();
        let lock = dir.join("SingletonLock");
        std::os::unix::fs::symlink(format!("host-{}", std::process::id()), &lock).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        let error = write(local, CHATBOX, &connection(&models), &tokens(), false).unwrap_err();
        assert_eq!(error.message, "Chatbox is open; quit it and try again");
        let status = &status_of(local, [CHATBOX], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.notes, ["running"]);
        assert_eq!(fs::read_to_string(file(&home)).unwrap(), FIRST);

        // A lock a crash left behind names no live process.
        fs::remove_file(&lock).unwrap();
        std::os::unix::fs::symlink("host-999999", &lock).unwrap();
        write(local, CHATBOX, &connection(&models), &tokens(), false).unwrap();
        assert_eq!(
            read(&home)["settings"]["defaultChatModel"]["provider"],
            "local-chat"
        );
        fs::remove_dir_all(home).unwrap();
    }
}
