//! Kilo Code, CLI and VS Code extension alike, reads a JSONC config from the
//! first of its global files in `~/.config/kilo`, as OpenCode does. AstrLink
//! adds one provider that takes every protocol, each model naming its SDK.
//! Kilo tells the model `<provider>/<model>`, so the provider id carries no
//! product name; its display name is local.

use std::{fs, path::PathBuf};

use serde_json::Value as Json;

use super::{get_list, model_entries, model_ids, push_present, set_list, ClientSpec, ConfigFile};
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

pub(crate) const CLIENT: Client = Client::new(&KiloCode);

struct KiloCode;

/// The global config file AstrLink writes.
struct KiloConfig;

const PROVIDER: &str = "local-gateway";
const PREFIX: &str = "provider.local-gateway.";
const MODELS_KEY: &str = "provider.local-gateway.models";
const BASE_URL_KEY: &str = "provider.local-gateway.options.baseURL";
const SCHEMA: &str = "https://app.kilo.ai/config.json";
/// The files Kilo writes, the first that exists; it merges them in the
/// reverse order of `MERGED`.
const FILES: [&str; 5] = [
    "kilo.jsonc",
    "kilo.json",
    "opencode.jsonc",
    "opencode.json",
    "config.json",
];
/// Kilo's merge order, later files winning.
const MERGED: [&str; 5] = [
    "config.json",
    "kilo.json",
    "kilo.jsonc",
    "opencode.json",
    "opencode.jsonc",
];

/// The SDK a model of `protocol` takes, when not the provider's.
fn sdk(protocol: &str) -> Option<&'static str> {
    match protocol {
        "anthropic.messages" => Some("@ai-sdk/anthropic"),
        "openai.responses" => Some("@ai-sdk/openai"),
        _ => None,
    }
}

fn config_file(home: Home, record: Option<&ClientRecord>) -> PathBuf {
    let dir = CLIENT.dir(home, None);
    record
        .and_then(|record| record.file.as_deref())
        .filter(|name| FILES.contains(name))
        .into_iter()
        .chain(FILES)
        .map(|name| dir.join(name))
        .find(|path| path.is_file())
        .unwrap_or_else(|| dir.join(FILES[0]))
}

fn jsonc() -> jsonc_parser::ParseOptions {
    jsonc_parser::ParseOptions {
        allow_comments: true,
        allow_trailing_commas: true,
        ..json::strict()
    }
}

impl ClientSpec for KiloCode {
    fn id(&self) -> &'static str {
        "kilo_code"
    }

    fn name(&self) -> &'static str {
        "Kilo Code"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "openai.responses", "anthropic.messages"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// Every Kilo process makes it; `KILO_CONFIG_DIR` and
    /// `XDG_CONFIG_HOME` from a shell are out of reach.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".config").join("kilo")
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&KiloConfig]
    }

    /// `record` keeps Kilo on the file AstrLink wrote before.
    fn config_paths(
        &self,
        home: Home,
        _target: Option<&str>,
        record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![config_file(home, record)]
    }

    fn recorded_file(&self, paths: &[PathBuf]) -> Option<String> {
        paths[0]
            .file_name()
            .map(|name| name.to_string_lossy().into_owned())
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[
            "provider.local-gateway.npm",
            "provider.local-gateway.options.baseURL",
            "provider.local-gateway.options.apiKey",
        ]
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let list = config
            .get(MODELS_KEY)
            .flatten()
            .map(|value| model_entries(serde_json::from_str(&value).unwrap_or_default()))
            .unwrap_or_default();
        let model = config
            .model("model")
            .and_then(|value| {
                value
                    .strip_prefix(&format!("{PROVIDER}/"))
                    .map(str::to_string)
            })
            .and_then(clean_model);
        CurrentModels {
            model,
            models: model_ids(&list),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let mut keys = Vec::new();
        if let Some(model) = connection.model("model") {
            keys.push(("model".into(), format!("{PROVIDER}/{model}")));
        }
        let base_url = self.base_url(connection.origin);
        for (key, value) in [
            ("npm", "@ai-sdk/openai-compatible"),
            ("name", "AstrLink"),
            ("options.baseURL", base_url.as_str()),
            ("options.apiKey", connection.token),
        ] {
            keys.push((format!("{PREFIX}{key}"), value.into()));
        }
        let models: serde_json::Map<String, Json> = connection
            .list()
            .into_iter()
            .map(|id| {
                let mut entry = serde_json::json!({ "name": id });
                if let Some(npm) = sdk(connection.protocol(self, &id)) {
                    entry["provider"] = serde_json::json!({ "npm": npm });
                }
                let details = details.get(&id);
                if details
                    .and_then(|details| details.reasoning.as_ref())
                    .is_some_and(|levels| !levels.is_empty())
                {
                    entry["reasoning"] = true.into();
                }
                // Without both limits Kilo never compacts a long session.
                if let Some((context, output)) =
                    details.and_then(|details| Some((details.context_window?, details.max_tokens?)))
                {
                    entry["limit"] = serde_json::json!({ "context": context, "output": output });
                }
                (id, entry)
            })
            .collect();
        keys.push((MODELS_KEY.into(), Json::Object(models).to_string()));
        keys
    }

    /// The schema Kilo would add to the file otherwise.
    fn start_file(&self, config: &mut Config) -> Result<(), FileError> {
        config.set("$schema", SCHEMA)
    }

    fn base_url_key(&self) -> &'static str {
        BASE_URL_KEY
    }

    /// A file Kilo merges after AstrLink's sets the model or this provider.
    fn notes(
        &self,
        home: Home,
        target: Option<&str>,
        _config: Option<&Config>,
    ) -> Vec<&'static str> {
        let ours = config_file(home, None);
        let dir = self.dir(home, target);
        let Some(after) = ours
            .file_name()
            .and_then(|name| MERGED.iter().position(|known| name == *known))
        else {
            return Vec::new();
        };
        let options = jsonc();
        let overridden = MERGED[after + 1..].iter().any(|name| {
            fs::read_to_string(dir.join(name))
                .ok()
                .and_then(|text| json::JsonDocument::parse(&text, Some(&options), &[], false).ok())
                .and_then(|document| document.value())
                .is_some_and(|value| {
                    value.get("model").is_some()
                        || value
                            .get("provider")
                            .and_then(|providers| providers.get(PROVIDER))
                            .is_some()
                })
        });
        if overridden {
            vec!["overridden"]
        } else {
            Vec::new()
        }
    }
}

impl ConfigFile for KiloConfig {
    fn format(&self) -> Format {
        Format::Json {
            syntax: Some(jsonc()),
            objects: &["provider"],
            root_list: false,
        }
    }

    fn group(&self, document: &Document) -> Vec<(String, Option<String>)> {
        let mut keys = Vec::new();
        push_present(&mut keys, self, document, "model");
        if let Document::Json(json) = document {
            json.object_keys(
                PREFIX,
                &["options"],
                &|path| self.get(document, path),
                &mut keys,
            );
        }
        keys
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        if key == MODELS_KEY {
            return get_list(document, key);
        }
        document.get(key)
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        if key == MODELS_KEY {
            return set_list(document, key, value);
        }
        document.set(key, value)
    }

    fn emptied(&self) -> &'static [&'static str] {
        &[
            "provider.local-gateway.options",
            "provider.local-gateway",
            "provider",
        ]
    }

    /// The schema AstrLink named in a file it created goes with the rest.
    fn is_empty(&self, document: &Document) -> bool {
        match document {
            Document::Json(json) => json.holds_only(|name, _| name == Some("$schema")),
            _ => document.is_empty(),
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn read(path: &Path) -> serde_json::Value {
        read_json(KILO_CODE.files()[0], &fs::read_to_string(path).unwrap()).unwrap()
    }

    #[test]
    fn writes_one_provider_with_each_models_sdk() {
        let home = unique_home("kilo");
        let local = Home::local(&home);
        let dir = home.join(".config/kilo");
        fs::create_dir_all(&dir).unwrap();
        let models = vec![("model", "gpt-5.5".to_string())];
        let list = strings(&["claude-sonnet-5", "deepseek-v4", "gpt-5.5"]);
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
                reasoning: Some(levels(&["low", "medium", "high"])),
                context_window: Some(400_000),
                max_tokens: Some(128_000),
            },
        )]);
        let outcome = write_with(local, KILO_CODE, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        assert_eq!(
            read(&dir.join("kilo.jsonc")),
            serde_json::json!({
                "$schema": "https://app.kilo.ai/config.json",
                "model": "local-gateway/gpt-5.5",
                "provider": {"local-gateway": {
                    "npm": "@ai-sdk/openai-compatible",
                    "name": "AstrLink",
                    "options": {"baseURL": format!("{ORIGIN}/v1"), "apiKey": TOKEN},
                    "models": {
                        "claude-sonnet-5": {"name": "claude-sonnet-5", "provider": {"npm": "@ai-sdk/anthropic"}},
                        "deepseek-v4": {"name": "deepseek-v4"},
                        "gpt-5.5": {"name": "gpt-5.5", "provider": {"npm": "@ai-sdk/openai"}, "reasoning": true,
                                    "limit": {"context": 400_000, "output": 128_000}},
                    },
                }},
            })
        );
        let status = &status_of(local, [KILO_CODE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("gpt-5.5"));
        assert!(status.notes.is_empty());

        fs::write(
            dir.join("opencode.json"),
            r#"{"model": "kilo/kilo-auto/free"}"#,
        )
        .unwrap();
        let status = &status_of(local, [KILO_CODE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.notes, ["overridden"]);
        fs::remove_file(dir.join("opencode.json")).unwrap();

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert_eq!(
            read(&dir.join("kilo.jsonc"))["provider"]["local-gateway"]["options"]["baseURL"],
            "http://127.0.0.1:9000/v1"
        );

        remove(local, KILO_CODE).unwrap();
        assert!(!dir.join("kilo.jsonc").exists());
        fs::remove_dir_all(home).unwrap();
    }

    const USER: &str = "{\n  \"$schema\": \"https://app.kilo.ai/config.json\",\n  // My Kilo settings\n  \"model\": \"openrouter/qwen3-coder\",\n  \"permission\": {\n    \"bash\": \"allow\",\n  },\n  \"provider\": {\n    \"openrouter\": {\n      \"options\": { \"apiKey\": \"sk-or-mine\" },\n    },\n  },\n}\n";

    #[test]
    fn writes_the_file_kilo_writes_and_puts_the_model_back() {
        let home = unique_home("kilo-merge");
        let local = Home::local(&home);
        let dir = home.join(".config/kilo");
        fs::create_dir_all(&dir).unwrap();
        fs::write(dir.join("opencode.json"), USER).unwrap();
        let models = vec![("model", "deepseek-v4".to_string())];
        assert_eq!(
            asked(write(local, KILO_CODE, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["model"]), None))
        );
        write(local, KILO_CODE, &connection(&models), &tokens(), true).unwrap();
        assert!(!dir.join("kilo.jsonc").exists());
        let written = read(&dir.join("opencode.json"));
        assert_eq!(written["model"], "local-gateway/deepseek-v4");
        assert_eq!(
            written["provider"]["openrouter"]["options"]["apiKey"],
            "sk-or-mine"
        );

        remove(local, KILO_CODE).unwrap();
        let restored = fs::read_to_string(dir.join("opencode.json")).unwrap();
        assert!(restored.contains("// My Kilo settings"));
        assert_eq!(
            read(&dir.join("opencode.json")),
            read_json(KILO_CODE.files()[0], USER).unwrap()
        );
        fs::remove_dir_all(home).unwrap();
    }
}
