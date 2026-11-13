//! Cline, CLI and editor extensions alike, reads providers from
//! `~/.cline/data/settings/providers.json` and starts with
//! `lastUsedProvider`. A built-in provider holds one model, so AstrLink
//! writes the one its default model's protocol takes. Cline rewrites the
//! file on every run, stamping `updatedAt`, so only the provider's
//! `settings` and the choice are AstrLink's.

use std::{fs, path::PathBuf};

use chrono::SecondsFormat;
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

pub(crate) const CLIENT: Client = Client::new(&Cline);

struct Cline;

/// `providers.json`.
struct Providers;

/// Protocol and Cline's provider id.
const ROUTES: [(&str, &str); 3] = [
    ("openai.chat", "openai-compatible"),
    ("openai.responses", "openai-native"),
    ("anthropic.messages", "anthropic"),
];
const PREFIX: &str = "providers.";
const SUFFIX: &str = ".settings";
const CHOICE_KEY: &str = "lastUsedProvider";
/// What Cline itself starts the file with.
const EMPTY: &str = "{\n  \"version\": 1,\n  \"modes\": {}\n}\n";
/// The editors whose extension folders hold Cline.
const EDITORS: [&str; 5] = [
    ".vscode",
    ".vscode-insiders",
    ".cursor",
    ".windsurf",
    ".vscode-oss",
];

fn settings_key(id: &str) -> String {
    format!("{PREFIX}{id}{SUFFIX}")
}

fn provider_of(key: &str) -> Option<&str> {
    let id = key.strip_prefix(PREFIX)?.strip_suffix(SUFFIX)?;
    ROUTES.iter().any(|(_, known)| *known == id).then_some(id)
}

fn route_of(protocol: &str) -> &'static str {
    ROUTES
        .into_iter()
        .find(|(known, _)| *known == protocol)
        .map_or(ROUTES[0].1, |(_, id)| id)
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 6] = [
        "provider",
        "apiKey",
        "baseUrl",
        "model",
        "contextWindow",
        "maxTokens",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn json_at(config: &Config, key: &str) -> Option<Json> {
    serde_json::from_str(&config.get(key).flatten()?).ok()
}

impl ClientSpec for Cline {
    fn id(&self) -> &'static str {
        "cline"
    }

    fn name(&self) -> &'static str {
        "Cline"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    /// `CLINE_DIR` and `CLINE_DATA_DIR` from a shell are out of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".cline")
    }

    /// An extension makes `~/.cline` on its first start.
    fn detected(&self, home: Home, target: Option<&str>) -> bool {
        self.dir(home, target).is_dir()
            || EDITORS.iter().any(|editor| {
                fs::read_dir(home.join(editor).join("extensions"))
                    .into_iter()
                    .flatten()
                    .flatten()
                    .any(|entry| {
                        entry
                            .file_name()
                            .to_string_lossy()
                            .starts_with("saoudrizwan.claude-dev-")
                    })
            })
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&Providers]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self
            .dir(home, target)
            .join("data")
            .join("settings")
            .join("providers.json")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// The provider's settings hold the address and the token.
    fn holds_connection(&self, key: &str) -> bool {
        provider_of(key).is_some()
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let chosen = config.get(CHOICE_KEY).flatten();
        let model = ROUTES
            .iter()
            .map(|(_, id)| *id)
            .filter(|id| chosen.as_deref().map_or(true, |chosen| chosen == *id))
            .find_map(|id| json_at(config, &settings_key(id)))
            .and_then(|settings| settings.get("model")?.as_str().map(str::to_string))
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
        let id = route_of(connection.protocol(self, &model));
        let mut settings = serde_json::json!({
            "provider": id,
            "apiKey": connection.token,
            "baseUrl": self.base_url(connection.origin),
            "model": model,
        });
        if let Some(details) = details.get(&model) {
            if let Some(context) = details.context_window {
                settings["contextWindow"] = context.into();
            }
            if let Some(output) = details.max_tokens {
                settings["maxTokens"] = output.into();
            }
        }
        vec![
            (settings_key(id), settings.to_string()),
            (CHOICE_KEY.into(), id.into()),
        ]
    }

    /// Without `version` Cline reads the file as empty.
    fn start_file(&self, config: &mut Config) -> Result<(), FileError> {
        let Format::Json {
            syntax, objects, ..
        } = Providers.format()
        else {
            return Ok(());
        };
        let empty = json::JsonDocument::parse(EMPTY, syntax.as_ref(), objects, false).map_err(
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

    /// Unused: `synced_keys` moves the provider's settings.
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
                settings["baseUrl"] = self.base_url(origin).into();
                Some((key.clone(), settings.to_string()))
            })
            .collect()
    }

    /// The extension's older settings choose its provider and win over
    /// this file.
    fn notes(
        &self,
        home: Home,
        target: Option<&str>,
        _config: Option<&Config>,
    ) -> Vec<&'static str> {
        let legacy =
            fs::read_to_string(self.dir(home, target).join("data").join("globalState.json"))
                .ok()
                .and_then(|text| serde_json::from_str::<Json>(&text).ok())
                .is_some_and(|state| {
                    ["actModeApiProvider", "planModeApiProvider"]
                        .iter()
                        .any(|key| state.get(key).is_some())
                });
        if legacy {
            vec!["legacy"]
        } else {
            Vec::new()
        }
    }
}

impl ConfigFile for Providers {
    /// A file that fails Cline's schema reads as signed out.
    fn format(&self) -> Format {
        Format::Json {
            syntax: None,
            objects: &["providers", "modes"],
            root_list: false,
        }
    }

    fn secret(&self) -> bool {
        true
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Json(json) = document else {
            return None;
        };
        if provider_of(key).is_some() {
            return json
                .get_json(key)
                .map(|value| value.map(|value| value.to_string()));
        }
        document.get(key)
    }

    /// A provider AstrLink adds gets the stamp Cline gives its own.
    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Json(json) = document else {
            return document.set(key, value);
        };
        let Some(id) = provider_of(key) else {
            return json.set(key, value);
        };
        let settings: Json = serde_json::from_str(value).map_err(|error| error.to_string())?;
        json.set_json(key, &settings, rank)?;
        let entry = format!("{PREFIX}{id}");
        if json.get(&format!("{entry}.updatedAt")).is_none() {
            let now = chrono::Utc::now().to_rfc3339_opts(SecondsFormat::Millis, true);
            json.set(&format!("{entry}.updatedAt"), &now)?;
        }
        if json.get(&format!("{entry}.tokenSource")).is_none() {
            json.set(&format!("{entry}.tokenSource"), "manual")?;
        }
        Ok(())
    }

    /// A provider left with only Cline's stamps goes.
    fn remove(&self, document: &mut Document, key: &str) {
        let Document::Json(json) = document else {
            return document.remove(key);
        };
        json.remove(key);
        let Some(id) = provider_of(key) else {
            return;
        };
        let entry = format!("{PREFIX}{id}");
        let stamps_only = json.get_json(&entry).flatten().is_some_and(|value| {
            value.as_object().is_some_and(|object| {
                object
                    .keys()
                    .all(|name| name == "updatedAt" || name == "tokenSource")
            })
        });
        if stamps_only {
            json.remove(&entry);
        }
    }

    fn emptied(&self) -> &'static [&'static str] {
        &["providers"]
    }

    /// What Cline starts a file with goes with the rest.
    fn is_empty(&self, document: &Document) -> bool {
        let Document::Json(json) = document else {
            return document.is_empty();
        };
        json.value()
            .and_then(|value| value.as_object().cloned())
            .is_some_and(|object| {
                object.iter().all(|(name, value)| match name.as_str() {
                    "version" => true,
                    "modes" => value.as_object().is_some_and(serde_json::Map::is_empty),
                    _ => false,
                })
            })
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn file(home: &Path) -> PathBuf {
        home.join(".cline/data/settings/providers.json")
    }

    fn read(home: &Path) -> serde_json::Value {
        read_json(CLINE.files()[0], &fs::read_to_string(file(home)).unwrap()).unwrap()
    }

    #[test]
    fn writes_the_provider_the_default_model_takes() {
        let home = unique_home("cline");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".cline/data")).unwrap();
        let models = vec![("model", "claude-sonnet-5".to_string())];
        let protocols = BTreeMap::from([("claude-sonnet-5".to_string(), "anthropic.messages")]);
        let chosen = Connection {
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
        let outcome = write_with(local, CLINE, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        let mut written = read(&home);
        let stamp = written["providers"]["anthropic"]["updatedAt"].take();
        assert!(
            stamp.as_str().is_some_and(|stamp| stamp.ends_with('Z')),
            "{stamp}"
        );
        assert_eq!(
            written,
            serde_json::json!({
                "version": 1,
                "modes": {},
                "providers": {"anthropic": {
                    "settings": {
                        "provider": "anthropic", "apiKey": TOKEN, "baseUrl": format!("{ORIGIN}/v1"),
                        "model": "claude-sonnet-5", "contextWindow": 200_000, "maxTokens": 64_000,
                    },
                    "updatedAt": null,
                    "tokenSource": "manual",
                }},
                "lastUsedProvider": "anthropic",
            })
        );
        let status = &status_of(local, [CLINE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-sonnet-5"));
        assert!(status.notes.is_empty());

        // A chat model moves the choice to the compatible provider.
        let models = vec![("model", "deepseek-v4".to_string())];
        write(local, CLINE, &connection(&models), &tokens(), false).unwrap();
        let written = read(&home);
        assert!(written["providers"].get("anthropic").is_none());
        assert_eq!(written["lastUsedProvider"], "openai-compatible");

        fs::write(
            home.join(".cline/data/globalState.json"),
            r#"{"actModeApiProvider": "openrouter"}"#,
        )
        .unwrap();
        let status = &status_of(local, [CLINE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.notes, ["legacy"]);

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert_eq!(
            read(&home)["providers"]["openai-compatible"]["settings"]["baseUrl"],
            "http://127.0.0.1:9000/v1"
        );

        remove(local, CLINE).unwrap();
        assert!(!file(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = r#"{
  "version": 1,
  "lastUsedProvider": "openai-compatible",
  "modes": {},
  "providers": {
    "openai-compatible": {
      "settings": {
        "provider": "openai-compatible",
        "apiKey": "sk-mine",
        "model": "qwen3-coder",
        "baseUrl": "http://192.168.1.9:8000/v1"
      },
      "updatedAt": "2026-10-30T08:00:00.000Z",
      "tokenSource": "manual"
    }
  }
}
"#;

    #[test]
    fn puts_the_users_provider_back() {
        let home = unique_home("cline-merge");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".cline/data/settings")).unwrap();
        fs::write(file(&home), USER).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, CLINE, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["providers.openai-compatible.settings"]), None))
        );
        write(local, CLINE, &connection(&models), &tokens(), true).unwrap();
        let written = read(&home);
        assert_eq!(
            written["providers"]["openai-compatible"]["settings"]["apiKey"],
            TOKEN
        );
        assert_eq!(
            written["providers"]["openai-compatible"]["updatedAt"],
            "2026-10-30T08:00:00.000Z"
        );

        remove(local, CLINE).unwrap();
        assert_eq!(read(&home), read_json(CLINE.files()[0], USER).unwrap());
        fs::remove_dir_all(home).unwrap();
    }
}
