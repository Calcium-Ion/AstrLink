//! OpenCode reads a JSONC config with an `astrlink` provider listing every
//! model, from the first of its global config files that exists.

use std::path::PathBuf;

use super::{
    get_list, model_entries, model_ids, push_present, set_list, ClientSpec, ConfigFile, PROVIDER_ID,
};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::{Config, FileError},
        format::{json, Document, Format},
        models::{Catalog, CurrentModels, ModelDetails},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Opencode);

const OPENCODE_PROVIDER_PREFIX: &str = "provider.astrlink.";
const OPENCODE_MODELS_KEY: &str = "provider.astrlink.models";
const OPENCODE_SCHEMA: &str = "https://opencode.ai/config.json";
/// OpenCode's global config files, highest priority first.
const OPENCODE_FILES: [&str; 3] = ["opencode.jsonc", "opencode.json", "config.json"];

struct Opencode;

/// The global config file AstrLink writes.
struct OpencodeConfig;

/// OpenCode merges its global files, the first of `OPENCODE_FILES` winning.
/// AstrLink writes the one it wrote before, else the first that exists, else
/// a new `opencode.jsonc`.
fn opencode_file(home: Home, record: Option<&ClientRecord>) -> PathBuf {
    let dir = home.opencode_dir();
    record
        .and_then(|record| record.file.as_deref())
        .filter(|name| OPENCODE_FILES.contains(name))
        .into_iter()
        .chain(OPENCODE_FILES)
        .map(|name| dir.join(name))
        .find(|path| path.is_file())
        .unwrap_or_else(|| dir.join(OPENCODE_FILES[0]))
}

impl ClientSpec for Opencode {
    fn id(&self) -> &'static str {
        "opencode"
    }

    fn name(&self) -> &'static str {
        "OpenCode"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.opencode_dir()
    }

    fn detected(&self, home: Home, _target: Option<&str>) -> bool {
        home.opencode_detected()
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&OpencodeConfig]
    }

    /// `record` keeps OpenCode on the file AstrLink wrote before.
    fn config_paths(
        &self,
        home: Home,
        _target: Option<&str>,
        record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![opencode_file(home, record)]
    }

    fn recorded_file(&self, paths: &[PathBuf]) -> Option<String> {
        paths[0]
            .file_name()
            .map(|name| name.to_string_lossy().into_owned())
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[
            "provider.astrlink.npm",
            "provider.astrlink.options.baseURL",
            "provider.astrlink.options.apiKey",
        ]
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let list = config
            .get(OPENCODE_MODELS_KEY)
            .flatten()
            .map(|value| model_entries(serde_json::from_str(&value).unwrap_or_default()))
            .unwrap_or_default();
        CurrentModels {
            model: config.provider_model("model"),
            models: model_ids(&list),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let base_url = self.base_url(connection.origin);
        let mut keys = Vec::new();
        if let Some(model) = connection.model("model") {
            keys.push(("model".into(), format!("{PROVIDER_ID}/{model}")));
        }
        // The display name is local UI in OpenCode's model picker.
        for (key, value) in [
            ("npm", "@ai-sdk/openai-compatible"),
            ("name", "AstrLink"),
            ("options.baseURL", base_url.as_str()),
            ("options.apiKey", connection.token),
        ] {
            keys.push((format!("{OPENCODE_PROVIDER_PREFIX}{key}"), value.into()));
        }
        // Without both limits OpenCode never compacts a long session.
        let models = connection
            .list()
            .into_iter()
            .map(|id| {
                let mut entry = serde_json::json!({ "name": id });
                if let Some(ModelDetails {
                    context_window: Some(context),
                    max_tokens: Some(output),
                    ..
                }) = details.get(&id)
                {
                    entry["limit"] = serde_json::json!({ "context": context, "output": output });
                }
                (id, entry)
            })
            .collect();
        keys.push((
            OPENCODE_MODELS_KEY.into(),
            serde_json::Value::Object(models).to_string(),
        ));
        keys
    }

    /// The schema in a file AstrLink creates.
    fn start_file(&self, config: &mut Config) -> Result<(), FileError> {
        config.set("$schema", OPENCODE_SCHEMA)
    }

    fn base_url_key(&self) -> &'static str {
        "provider.astrlink.options.baseURL"
    }
}

impl ConfigFile for OpencodeConfig {
    /// OpenCode reads JSONC.
    fn format(&self) -> Format {
        Format::Json {
            syntax: Some(jsonc_parser::ParseOptions {
                allow_comments: true,
                allow_trailing_commas: true,
                ..json::strict()
            }),
            objects: &["provider"],
            root_list: false,
        }
    }

    fn group(&self, document: &Document) -> Vec<(String, Option<String>)> {
        let mut keys = Vec::new();
        push_present(&mut keys, self, document, "model");
        if let Document::Json(json) = document {
            json.object_keys(
                OPENCODE_PROVIDER_PREFIX,
                &["options"],
                &|path| self.get(document, path),
                &mut keys,
            );
        }
        keys
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        if key == OPENCODE_MODELS_KEY {
            return get_list(document, key);
        }
        document.get(key)
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        if key == OPENCODE_MODELS_KEY {
            return set_list(document, key, value);
        }
        document.set(key, value)
    }

    fn emptied(&self) -> &'static [&'static str] {
        &["provider.astrlink.options", "provider.astrlink", "provider"]
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
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn opencode_gets_a_provider_with_every_listed_model() {
        let list = strings(&["oc-route", "oc-mini"]);
        let models = vec![("model", "oc-route".to_string())];
        let details = Catalog::from([(
            "oc-route".to_string(),
            ModelDetails {
                context_window: Some(400_000),
                max_tokens: Some(64_000),
                ..Default::default()
            },
        )]);
        let plan =
            plan_write_with(OPENCODE, &[None], &listed(&models, &list), &details, None).unwrap();
        assert_eq!(
            json5(&plan.contents[0]),
            serde_json::json!({
                "$schema": "https://opencode.ai/config.json",
                "model": "astrlink/oc-route",
                "provider": {"astrlink": {
                    "npm": "@ai-sdk/openai-compatible",
                    "name": "AstrLink",
                    "options": {"baseURL": "http://127.0.0.1:18317/v1", "apiKey": TOKEN},
                    "models": {
                        "oc-route": {"name": "oc-route", "limit": {"context": 400_000, "output": 64_000}},
                        "oc-mini": {"name": "oc-mini"},
                    },
                }},
            })
        );
        assert!(!plan.record.keys.contains_key("$schema"));
        let written = [Some(plan.contents[0].as_str())];
        assert_eq!(
            current_models(OPENCODE, &written),
            CurrentModels {
                model: Some("oc-route".into()),
                models: Some(list.clone()),
                ..Default::default()
            }
        );
        // The created file holds nothing else and goes.
        assert_eq!(
            plan_remove(OPENCODE, &written, &plan.record).unwrap(),
            [Removal::Delete]
        );

        let existing = "{\n  // mine\n  \"model\": \"anthropic/claude-sonnet\",\n  \"provider\": {\"other\": {\"options\": {\"apiKey\": \"sk-other\"}}},\n}\n";
        let plan = plan_write(OPENCODE, &[Some(existing)], &connection(&models), None).unwrap();
        assert_eq!(plan.conflicts, ["model"]);
        assert!(!plan.contents[0].contains("$schema"));
        let [Removal::Write(left)] =
            &plan_remove(OPENCODE, &[Some(&plan.contents[0])], &plan.record).unwrap()[..]
        else {
            panic!("expected a rewrite");
        };
        assert!(left.contains("// mine"));
        assert_eq!(json5(left), json5(existing));
    }

    #[test]
    fn opencode_writes_the_file_it_reads_first_and_keeps_to_it() {
        let home = unique_home("opencode");
        let dir = home.join(".config").join("opencode");
        fs::create_dir_all(&dir).unwrap();
        let local = Home::local(&home);
        assert_eq!(opencode_file(local, None), dir.join("opencode.jsonc"));
        fs::write(dir.join("config.json"), "{}").unwrap();
        assert_eq!(opencode_file(local, None), dir.join("config.json"));
        fs::write(dir.join("opencode.json"), "{}").unwrap();
        assert_eq!(opencode_file(local, None), dir.join("opencode.json"));
        write(
            local,
            OPENCODE,
            &connection(&codex_models()),
            &tokens(),
            false,
        )
        .unwrap();
        assert!(fs::read_to_string(dir.join("opencode.json"))
            .unwrap()
            .contains(TOKEN));
        // A file made later does not move AstrLink's config.
        fs::write(dir.join("opencode.jsonc"), "{}").unwrap();
        let statuses = status(local, Some("http://127.0.0.1:18317/"), Some(&tokens())).unwrap();
        assert_eq!(
            statuses[3].paths,
            [dir.join("opencode.json").display().to_string()]
        );
        assert_eq!(statuses[3].state, ClientState::Configured);
        remove(local, OPENCODE).unwrap();
        assert_eq!(
            json5(&fs::read_to_string(dir.join("opencode.json")).unwrap()),
            serde_json::json!({})
        );
        fs::remove_dir_all(home).unwrap();
    }
}
