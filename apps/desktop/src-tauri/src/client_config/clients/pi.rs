//! Pi reads its `astrlink` provider and model list from `models.json`
//! (with line comments and trailing commas) and the model it starts with
//! from `settings.json`, both in its agent directory.

use std::path::PathBuf;

use super::{entry_rank, model_entries, model_ids, push_present, ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::{read_json, Config},
        format::{json, Document, Format},
        models::{Catalog, CurrentModels, ModelDetails, MAX_PI_LIMIT},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Pi);

pub(crate) const PI_PROVIDER: &str = "astrlink";
const PI_PROVIDER_PREFIX: &str = "providers.astrlink.";
/// Pi's model list for the provider, written as `[{"id": <model>}]` plus
/// each model's reasoning levels and limits when the official catalog lists
/// them.
const PI_MODELS_KEY: &str = "providers.astrlink.models";
/// Pi's thinking levels, weakest first. Pi treats a custom model as one that
/// cannot think unless its entry says it reasons.
pub(crate) const PI_THINKING_LEVELS: [&str; 7] =
    ["off", "minimal", "low", "medium", "high", "xhigh", "max"];
/// The model Pi starts with, which its `/model` picker can save over.
const PI_STARTUP_KEYS: [&str; 2] = ["defaultProvider", "defaultModel"];

struct Pi;

/// `models.json`: Pi's endpoints.
struct PiModels;

/// `settings.json`: Pi's preferences, including the model it starts with.
struct PiSettings;

/// A model list's value. A list of one entry keeps the form configs from
/// released builds were recorded with: the id alone for `[{"id": <model>}]`,
/// else the entry.
fn list_value(value: &serde_json::Value) -> String {
    if let Some([entry]) = value.as_array().map(Vec::as_slice) {
        return match entry.as_object() {
            Some(fields) if fields.len() == 1 => match fields.get("id") {
                Some(serde_json::Value::String(id)) => id.clone(),
                _ => entry.to_string(),
            },
            _ => entry.to_string(),
        };
    }
    value.to_string()
}

/// The JSON a model list's value stands for.
fn list_json(value: &str) -> serde_json::Value {
    let parsed = serde_json::from_str::<serde_json::Value>(value).ok();
    match parsed {
        Some(list @ serde_json::Value::Array(_)) if value.starts_with('[') => list,
        Some(entry @ serde_json::Value::Object(_)) if value.starts_with('{') => {
            serde_json::Value::Array(vec![entry])
        }
        _ => serde_json::json!([{ "id": value }]),
    }
}

/// Pi's entry for a model, its levels in Pi's order.
fn pi_entry(id: &str, details: Option<&ModelDetails>) -> serde_json::Value {
    let mut entry = serde_json::Map::new();
    entry.insert("id".into(), id.into());
    let Some(details) = details else {
        return serde_json::Value::Object(entry);
    };
    if let Some(levels) = &details.reasoning {
        entry.insert("reasoning".into(), true.into());
        if !levels.is_empty() {
            let map = PI_THINKING_LEVELS
                .iter()
                .map(|level| {
                    let effort = if *level == "off" { "none" } else { level };
                    let value = if levels.iter().any(|known| known == effort) {
                        serde_json::Value::String(effort.to_string())
                    } else {
                        serde_json::Value::Null
                    };
                    ((*level).to_string(), value)
                })
                .collect();
            entry.insert("thinkingLevelMap".into(), serde_json::Value::Object(map));
        }
    }
    if let Some(tokens) = details.context_window {
        entry.insert("contextWindow".into(), tokens.into());
    }
    if let Some(tokens) = details.max_tokens {
        entry.insert("maxTokens".into(), tokens.into());
    }
    serde_json::Value::Object(entry)
}

impl ClientSpec for Pi {
    fn id(&self) -> &'static str {
        "pi"
    }

    fn name(&self) -> &'static str {
        "Pi"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.responses"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// Its moved agent directory, or `~/.pi`.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.pi_dir()
    }

    fn detected(&self, home: Home, _target: Option<&str>) -> bool {
        home.pi_detected()
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&PiModels, &PiSettings]
    }

    fn config_paths(
        &self,
        home: Home,
        _target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        let agent = home.pi_agent_dir();
        vec![agent.join("models.json"), agent.join("settings.json")]
    }

    fn takes_limits(&self) -> bool {
        true
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[
            "providers.astrlink.baseUrl",
            "providers.astrlink.api",
            "providers.astrlink.apiKey",
        ]
    }

    /// The model Pi starts with on AstrLink, else the first AstrLink's list
    /// offers.
    fn current_models(&self, config: &Config) -> CurrentModels {
        let entries = config
            .get(PI_MODELS_KEY)
            .flatten()
            .map(|value| model_entries(list_json(&value)))
            .unwrap_or_default();
        let starts_here = config.get("defaultProvider").flatten().as_deref() == Some(PI_PROVIDER);
        let model = starts_here
            .then(|| config.model("defaultModel"))
            .flatten()
            .or_else(|| entries.first().map(|(id, _)| id.clone()));
        let limit = |field: &str| {
            let model = model.as_deref()?;
            let (_, entry) = entries.iter().find(|(id, _)| id == model)?;
            entry
                .get(field)?
                .as_u64()
                .filter(|tokens| (1..=MAX_PI_LIMIT).contains(tokens))
        };
        CurrentModels {
            context_window: limit("contextWindow"),
            max_tokens: limit("maxTokens"),
            models: model_ids(&entries),
            model,
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let base_url = self.base_url(connection.origin);
        let mut keys = Vec::new();
        // The display name is local UI in Pi's model picker.
        for (key, value) in [
            ("name", "AstrLink"),
            ("baseUrl", base_url.as_str()),
            ("api", "openai-responses"),
            ("apiKey", connection.token),
        ] {
            keys.push((format!("{PI_PROVIDER_PREFIX}{key}"), value.into()));
        }
        if let Some(model) = connection.model("model") {
            let entries = connection
                .list()
                .iter()
                .map(|id| pi_entry(id, details.get(id)))
                .collect();
            keys.push((
                PI_MODELS_KEY.into(),
                list_value(&serde_json::Value::Array(entries)),
            ));
            keys.push(("defaultProvider".into(), PI_PROVIDER.into()));
            keys.push(("defaultModel".into(), model));
        }
        keys
    }

    fn base_url_key(&self) -> &'static str {
        "providers.astrlink.baseUrl"
    }
}

impl ConfigFile for PiModels {
    /// Pi strips line comments and trailing commas.
    fn format(&self) -> Format {
        Format::Json {
            syntax: Some(jsonc_parser::ParseOptions {
                allow_comments: true,
                allow_trailing_commas: true,
                ..json::strict()
            }),
            objects: &["providers"],
            root_list: false,
        }
    }

    fn holds(&self, key: &str) -> bool {
        !PI_STARTUP_KEYS.contains(&key)
    }

    fn group(&self, document: &Document) -> Vec<(String, Option<String>)> {
        let mut keys = Vec::new();
        if let Document::Json(json) = document {
            json.object_keys(
                PI_PROVIDER_PREFIX,
                &[],
                &|path| self.get(document, path),
                &mut keys,
            );
        }
        keys
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        match document {
            Document::Json(json) if key == PI_MODELS_KEY => json
                .get_json(key)
                .map(|value| value.map(|value| list_value(&value))),
            _ => document.get(key),
        }
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        match document {
            Document::Json(json) if key == PI_MODELS_KEY => {
                json.set_json(key, &list_json(value), entry_rank)
            }
            _ => document.set(key, value),
        }
    }

    /// Pi requires `providers`, so an emptied one stays.
    fn emptied(&self) -> &'static [&'static str] {
        &["providers.astrlink"]
    }

    /// An emptied `providers` stays until the file goes.
    fn is_empty(&self, document: &Document) -> bool {
        match document {
            Document::Json(json) => json.holds_only(|name, property| {
                name == Some("providers")
                    && property
                        .object_value()
                        .is_some_and(|providers| providers.properties().is_empty())
            }),
            _ => document.is_empty(),
        }
    }
}

impl ConfigFile for PiSettings {
    fn format(&self) -> Format {
        Format::Json {
            syntax: None,
            objects: &[],
            root_list: false,
        }
    }

    fn holds(&self, key: &str) -> bool {
        PI_STARTUP_KEYS.contains(&key)
    }

    /// Only the startup model is AstrLink's to take over.
    fn group(&self, document: &Document) -> Vec<(String, Option<String>)> {
        let mut keys = Vec::new();
        for key in PI_STARTUP_KEYS {
            push_present(&mut keys, self, document, key);
        }
        keys
    }
}

/// Pi's models.json as Pi reads it, for importing the providers it
/// configures.
pub(crate) fn read_pi_models(text: &str) -> Result<serde_json::Value, String> {
    read_json(&PiModels, text)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn pi_endpoints_and_startup_model_are_written_to_two_files() {
        let plan = plan_write(PI, &[None, None], &connection(&pi_models()), None).unwrap();
        assert!(plan.conflicts.is_empty());
        assert_eq!(
            json(&plan.contents[0]),
            serde_json::json!({"providers": {"astrlink": {
                "name": "AstrLink",
                "baseUrl": "http://127.0.0.1:18317/v1",
                "api": "openai-responses",
                "apiKey": TOKEN,
                "models": [{"id": "pi-route"}],
            }}})
        );
        assert_eq!(
            json(&plan.contents[1]),
            serde_json::json!({"defaultProvider": "astrlink", "defaultModel": "pi-route"})
        );
        assert!(!plan.contents[1].contains(TOKEN));
        assert!(plan.record.created_file);
        assert_eq!(plan.record.base_url, "http://127.0.0.1:18317/v1");
        let existing = [Some(plan.contents[0].as_str()), Some(&plan.contents[1])];
        assert_eq!(
            inspect(PI, &existing, Some(&plan.record), Some(ORIGIN)),
            ClientState::Configured
        );
        // Both created files go.
        assert_eq!(
            plan_remove(PI, &existing, &plan.record).unwrap(),
            [Removal::Delete, Removal::Delete]
        );
    }

    #[test]
    fn pi_files_keep_comments_other_providers_and_preferences() {
        let models = "{\n  // local servers\n  \"providers\": {\n    \"ollama\": {\"baseUrl\": \"http://localhost:11434/v1\", \"apiKey\": \"sk-other\"},\n  },\n}\n";
        let settings = "{\n  \"theme\": \"dark\",\n  \"defaultProvider\": \"ollama\",\n  \"defaultModel\": \"qwen\"\n}\n";
        let plan = plan_write(
            PI,
            &[Some(models), Some(settings)],
            &connection(&pi_models()),
            None,
        )
        .unwrap();
        assert_eq!(plan.conflicts, ["defaultProvider", "defaultModel"]);
        assert!(plan.contents[0].starts_with("{\n  // local servers\n"));
        assert!(plan.contents[0].contains("\"apiKey\": \"sk-other\""));
        assert_eq!(
            json(&plan.contents[1]),
            serde_json::json!({"theme": "dark", "defaultProvider": "astrlink", "defaultModel": "pi-route"})
        );
        assert!(!plan.record.created_file);
        let existing = [Some(plan.contents[0].as_str()), Some(&plan.contents[1])];
        let removals = plan_remove(PI, &existing, &plan.record).unwrap();
        let [Removal::Write(models_left), Removal::Write(settings_left)] = &removals[..] else {
            panic!("expected two rewrites");
        };
        assert!(models_left.contains("// local servers") && models_left.contains("ollama"));
        assert!(!models_left.contains("astrlink"));
        // The replaced settings come back.
        assert_eq!(json(settings_left), json(settings));
    }

    #[test]
    fn pi_model_choices_and_edited_model_lists_belong_to_the_user() {
        let plan = plan_write(PI, &[None, None], &connection(&pi_models()), None).unwrap();
        // Saving another model in Pi's picker is not a connection change.
        let picked = plan.contents[1].replace("astrlink", "anthropic");
        let edited = plan.contents[0].replace(
            "\"id\": \"pi-route\"",
            "\"id\": \"pi-route\", \"reasoning\": true",
        );
        assert_ne!(edited, plan.contents[0]);
        let existing = [Some(edited.as_str()), Some(&picked)];
        assert_eq!(
            inspect(PI, &existing, Some(&plan.record), Some(ORIGIN)),
            ClientState::Configured
        );
        let (synced, record) = plan_sync(PI, &existing, &plan.record, "http://127.0.0.1:9000")
            .unwrap()
            .unwrap();
        assert_eq!(
            synced,
            [
                edited.replace("127.0.0.1:18317", "127.0.0.1:9000"),
                picked.clone()
            ]
        );
        assert_eq!(record.base_url, "http://127.0.0.1:9000/v1");
        let rewrite = plan_write(
            PI,
            &[Some(&synced[0]), Some(&synced[1])],
            &connection(&pi_models()),
            Some(&record),
        )
        .unwrap();
        assert_eq!(
            rewrite.conflicts,
            ["providers.astrlink.models", "defaultProvider"]
        );
        let removals = plan_remove(PI, &[Some(&synced[0]), Some(&synced[1])], &record).unwrap();
        let [Removal::Write(models_left), Removal::Write(settings_left)] = &removals[..] else {
            panic!("expected two rewrites");
        };
        assert_eq!(
            json(models_left),
            serde_json::json!({"providers": {"astrlink": {"models": [{"id": "pi-route", "reasoning": true}]}}})
        );
        assert_eq!(
            json(settings_left),
            serde_json::json!({"defaultProvider": "anthropic"})
        );
    }

    #[test]
    fn pi_entries_name_the_official_thinking_levels() {
        let gpt = levels(&["none", "low", "medium", "high", "xhigh", "max"]);
        let plan = plan_write_with(
            PI,
            &[None, None],
            &connection(&pi_models()),
            &route(ModelDetails {
                reasoning: Some(gpt.clone()),
                ..Default::default()
            }),
            None,
        )
        .unwrap();
        assert!(plan.conflicts.is_empty());
        assert_eq!(
            json(&plan.contents[0])["providers"]["astrlink"]["models"],
            serde_json::json!([{"id": "pi-route", "reasoning": true, "thinkingLevelMap": {
                "off": "none", "minimal": null, "low": "low", "medium": "medium",
                "high": "high", "xhigh": "xhigh", "max": "max",
            }}])
        );
        // Written in Pi's order, recorded with sorted keys.
        let text = &plan.contents[0];
        let at = |needle: &str| text.find(needle).unwrap();
        assert!(at("\"reasoning\"") < at("\"thinkingLevelMap\"") && at("\"off\"") < at("\"max\""));
        assert_eq!(
            plan.record.keys[PI_MODELS_KEY],
            sha256_hex(
                r#"{"id":"pi-route","reasoning":true,"thinkingLevelMap":{"high":"high","low":"low","max":"max","medium":"medium","minimal":null,"off":"none","xhigh":"xhigh"}}"#
            )
        );
        let existing = [Some(plan.contents[0].as_str()), Some(&plan.contents[1])];
        assert_eq!(
            inspect(PI, &existing, Some(&plan.record), Some(ORIGIN)),
            ClientState::Configured
        );
        assert_eq!(
            current_models(PI, &[existing[0], None]),
            pi_current("pi-route")
        );
        assert_eq!(
            plan_remove(PI, &existing, &plan.record).unwrap()[0],
            Removal::Delete
        );

        // A model that cannot answer without reasoning has no off level.
        let opus = levels(&["low", "medium", "high", "xhigh", "max"]);
        let plan = plan_write_with(
            PI,
            &[None, None],
            &connection(&pi_models()),
            &route(ModelDetails {
                reasoning: Some(opus.clone()),
                ..Default::default()
            }),
            None,
        )
        .unwrap();
        assert_eq!(
            json(&plan.contents[0])["providers"]["astrlink"]["models"][0]["thinkingLevelMap"],
            serde_json::json!({
                "off": null, "minimal": null, "low": "low", "medium": "medium",
                "high": "high", "xhigh": "xhigh", "max": "max",
            })
        );
        // One that reasons with no strength to choose keeps Pi's range.
        let plan = plan_write_with(
            PI,
            &[None, None],
            &connection(&pi_models()),
            &route(ModelDetails {
                reasoning: Some(Vec::new()),
                ..Default::default()
            }),
            None,
        )
        .unwrap();
        assert_eq!(
            json(&plan.contents[0])["providers"]["astrlink"]["models"],
            serde_json::json!([{"id": "pi-route", "reasoning": true}])
        );
    }

    #[test]
    fn pi_thinking_levels_reach_released_configs_and_edits_stay_the_users() {
        let released = plan_write(PI, &[None, None], &connection(&pi_models()), None).unwrap();
        let existing = [
            Some(released.contents[0].as_str()),
            Some(&released.contents[1]),
        ];
        let gpt = levels(&["none", "low", "medium", "high", "xhigh", "max"]);
        let plan = plan_write_with(
            PI,
            &existing,
            &connection(&pi_models()),
            &route(ModelDetails {
                reasoning: Some(gpt.clone()),
                ..Default::default()
            }),
            Some(&released.record),
        )
        .unwrap();
        assert!(plan.conflicts.is_empty());
        assert!(plan.contents[0].contains("\"thinkingLevelMap\""));
        // Reformatting the entry keeps it AstrLink's.
        let compact = json(&plan.contents[0]).to_string();
        assert_eq!(
            plan_remove(PI, &[Some(&compact), Some(&plan.contents[1])], &plan.record).unwrap()[0],
            Removal::Delete
        );
        // A level the user changed is theirs.
        let edited = plan.contents[0].replace("\"max\": \"max\"", "\"max\": null");
        assert_ne!(edited, plan.contents[0]);
        let existing = [Some(edited.as_str()), Some(&plan.contents[1])];
        let rewrite = plan_write_with(
            PI,
            &existing,
            &connection(&pi_models()),
            &route(ModelDetails {
                reasoning: Some(gpt.clone()),
                ..Default::default()
            }),
            Some(&plan.record),
        )
        .unwrap();
        assert_eq!(rewrite.conflicts, ["providers.astrlink.models"]);
        assert_eq!(
            current_models(PI, &[existing[0], None]),
            pi_current("pi-route")
        );
        let removals = plan_remove(PI, &existing, &plan.record).unwrap();
        let Removal::Write(left) = &removals[0] else {
            panic!("expected the edited list to stay");
        };
        assert_eq!(
            json(left)["providers"]["astrlink"]["models"][0]["thinkingLevelMap"]["max"],
            serde_json::Value::Null
        );
    }

    #[test]
    fn pi_entries_carry_the_limits_the_user_set() {
        let pi = route(ModelDetails {
            reasoning: Some(levels(&["none", "high"])),
            context_window: Some(400_000),
            max_tokens: Some(64_000),
        });
        let plan =
            plan_write_with(PI, &[None, None], &connection(&pi_models()), &pi, None).unwrap();
        let entry = &json(&plan.contents[0])["providers"]["astrlink"]["models"][0];
        assert_eq!(entry["contextWindow"], 400_000);
        assert_eq!(entry["maxTokens"], 64_000);
        let existing = [Some(plan.contents[0].as_str()), Some(&plan.contents[1])];
        assert_eq!(
            current_models(PI, &existing),
            CurrentModels {
                context_window: Some(400_000),
                max_tokens: Some(64_000),
                ..pi_current("pi-route")
            }
        );
        // Writing the same entry again changes nothing and asks nothing.
        let again = plan_write_with(
            PI,
            &existing,
            &connection(&pi_models()),
            &pi,
            Some(&plan.record),
        )
        .unwrap();
        assert!(again.conflicts.is_empty());
        assert_eq!(again.contents, plan.contents);
        // Clearing the limits gives Pi its defaults back.
        let cleared = plan_write_with(
            PI,
            &existing,
            &connection(&pi_models()),
            &route(ModelDetails::default()),
            Some(&plan.record),
        )
        .unwrap();
        assert!(cleared.conflicts.is_empty());
        assert_eq!(
            json(&cleared.contents[0])["providers"]["astrlink"]["models"],
            serde_json::json!([{"id": "pi-route"}])
        );
        assert_eq!(
            plan_remove(PI, &existing, &plan.record).unwrap()[0],
            Removal::Delete
        );
        let limits = |context_window, max_tokens| Models {
            model: Some("m".into()),
            context_window,
            max_tokens,
            ..Default::default()
        };
        assert_eq!(
            limits(Some(1), Some(MAX_PI_LIMIT)).pi_limits(PI),
            Ok((Some(1), Some(MAX_PI_LIMIT)))
        );
        assert!(limits(Some(0), None).pi_limits(PI).is_err());
        assert!(limits(None, Some(MAX_PI_LIMIT + 1)).pi_limits(PI).is_err());
        assert!(limits(Some(1), None).pi_limits(CODEX).is_err());
    }

    #[test]
    fn pi_files_pi_cannot_read_are_left_alone() {
        for (models, settings, file) in [
            ("{\"providers\": []}", "{}", 0),
            ("{providers: {}}", "{}", 0),
            ("[]", "{}", 0),
            ("{}", "{\n  // note\n}", 1),
            ("{}", "{\"a\": 1,}", 1),
        ] {
            let existing = [Some(models), Some(settings)];
            let error = plan_write(PI, &existing, &connection(&pi_models()), None).unwrap_err();
            assert_eq!(error.file, file);
            assert!(!error.message.contains(TOKEN));
            assert_eq!(
                inspect(PI, &existing, None, Some(ORIGIN)),
                ClientState::Invalid
            );
        }
        let plan = plan_write(
            PI,
            &[Some("{\"providers\": {\"astrlink\": \"broken\"}}"), None],
            &connection(&pi_models()),
            None,
        )
        .unwrap();
        assert_eq!(plan.conflicts, ["providers.astrlink"]);
        assert!(plan.contents[0].contains(TOKEN));
    }

    #[test]
    fn pi_apply_writes_both_files_and_removes_only_what_it_wrote() {
        let home = unique_home("pi");
        fs::create_dir_all(home.join(".pi/agent")).unwrap();
        let agent = home.join(".pi").join("agent");
        let settings = agent.join("settings.json");
        fs::write(&settings, "{\n  \"theme\": \"dark\"\n}\n").unwrap();
        assert_eq!(
            write(
                Home::local(&home),
                PI,
                &connection(&pi_models()),
                &tokens(),
                false
            )
            .unwrap(),
            ApplyOutcome::Applied
        );
        let models = agent.join("models.json");
        assert!(fs::read_to_string(&models).unwrap().contains(TOKEN));
        assert!(fs::read_to_string(&settings)
            .unwrap()
            .contains("\"defaultModel\": \"pi-route\""));
        let statuses = status(
            Home::local(&home),
            Some("http://127.0.0.1:9000/"),
            Some(&tokens()),
        )
        .unwrap();
        assert_eq!(statuses[5].state, ClientState::Outdated);
        assert_eq!(
            statuses[5].paths,
            [models.display().to_string(), settings.display().to_string()]
        );
        sync(Home::local(&home), "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert!(fs::read_to_string(&models)
            .unwrap()
            .contains("http://127.0.0.1:9000/v1"));
        remove(Home::local(&home), PI).unwrap();
        assert!(!models.exists());
        assert_eq!(
            json(&fs::read_to_string(&settings).unwrap()),
            serde_json::json!({"theme": "dark"})
        );
        assert!(!records_path(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn a_failed_pi_write_puts_back_the_files_already_written() {
        let home = unique_home("pi-rollback");
        fs::create_dir_all(home.join(".pi/agent")).unwrap();
        // A dangling link reads as missing but cannot be written.
        std::os::unix::fs::symlink(
            home.join("missing/settings.json"),
            home.join(".pi/agent/settings.json"),
        )
        .unwrap();
        assert!(write(
            Home::local(&home),
            PI,
            &connection(&pi_models()),
            &tokens(),
            false
        )
        .is_err());
        assert!(!home.join(".pi/agent/models.json").exists());
        assert!(!records_path(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }
}
