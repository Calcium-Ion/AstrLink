//! OpenClaw reads JSON5 with an `astrlink` provider listing every model, and
//! the model its agents start with.

use std::path::PathBuf;

use super::{
    get_list, model_entries, model_ids, push_present, set_list, ClientSpec, ConfigFile, PROVIDER_ID,
};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{Document, Format},
        models::{Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Openclaw);

const OPENCLAW_PROVIDER_PREFIX: &str = "models.providers.astrlink.";
const OPENCLAW_MODELS_KEY: &str = "models.providers.astrlink.models";
/// The model OpenClaw's agents start with; `agents.defaults.model` may also
/// be that model's name alone.
const OPENCLAW_PRIMARY_KEY: &str = "agents.defaults.model.primary";
const OPENCLAW_MODEL_KEY: &str = "agents.defaults.model";

struct Openclaw;

/// `openclaw.json`, JSON5.
struct OpenclawConfig;

/// The key `path` is written at: the model's name alone stays a name alone.
fn shorthand(document: &Document, path: &str) -> bool {
    let Document::Json(json) = document else {
        return false;
    };
    path == OPENCLAW_PRIMARY_KEY
        && json
            .property(OPENCLAW_MODEL_KEY)
            .and_then(|property| property.value())
            .is_some_and(|model| model.as_string_lit().is_some())
}

impl ClientSpec for Openclaw {
    fn id(&self) -> &'static str {
        "openclaw"
    }

    fn name(&self) -> &'static str {
        "OpenClaw"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.openclaw_dir()
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&OpenclawConfig]
    }

    fn config_paths(
        &self,
        home: Home,
        _target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![home.openclaw_dir().join("openclaw.json")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[
            "models.providers.astrlink.baseUrl",
            "models.providers.astrlink.api",
            "models.providers.astrlink.apiKey",
        ]
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let list = config
            .get(OPENCLAW_MODELS_KEY)
            .flatten()
            .map(|value| model_entries(serde_json::from_str(&value).unwrap_or_default()))
            .unwrap_or_default();
        CurrentModels {
            model: config.provider_model(OPENCLAW_PRIMARY_KEY),
            models: model_ids(&list),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let base_url = self.base_url(connection.origin);
        let mut keys = Vec::new();
        for (key, value) in [
            ("baseUrl", base_url.as_str()),
            ("apiKey", connection.token),
            ("api", "openai-completions"),
        ] {
            keys.push((format!("{OPENCLAW_PROVIDER_PREFIX}{key}"), value.into()));
        }
        // OpenClaw requires a name for every model, and caps output at
        // 8192 tokens without a limit.
        let models = connection
            .list()
            .into_iter()
            .map(|id| {
                let mut entry = serde_json::json!({ "id": id, "name": id });
                let details = details.get(&id);
                if let Some(tokens) = details.and_then(|details| details.context_window) {
                    entry["contextWindow"] = tokens.into();
                }
                if let Some(tokens) = details.and_then(|details| details.max_tokens) {
                    entry["maxTokens"] = tokens.into();
                }
                entry
            })
            .collect();
        keys.push((
            OPENCLAW_MODELS_KEY.into(),
            serde_json::Value::Array(models).to_string(),
        ));
        if let Some(model) = connection.model("model") {
            keys.push((
                OPENCLAW_PRIMARY_KEY.into(),
                format!("{PROVIDER_ID}/{model}"),
            ));
        }
        keys
    }

    fn base_url_key(&self) -> &'static str {
        "models.providers.astrlink.baseUrl"
    }
}

impl ConfigFile for OpenclawConfig {
    fn format(&self) -> Format {
        Format::Json {
            syntax: Some(jsonc_parser::ParseOptions {
                allow_missing_commas: false,
                ..jsonc_parser::ParseOptions::default()
            }),
            objects: &["models", "models.providers", "agents", "agents.defaults"],
            root_list: false,
        }
    }

    fn group(&self, document: &Document) -> Vec<(String, Option<String>)> {
        let mut keys = Vec::new();
        if let Document::Json(json) = document {
            json.object_keys(
                OPENCLAW_PROVIDER_PREFIX,
                &[],
                &|path| self.get(document, path),
                &mut keys,
            );
        }
        push_present(&mut keys, self, document, OPENCLAW_PRIMARY_KEY);
        keys
    }

    fn get(&self, document: &Document, path: &str) -> Option<Option<String>> {
        if let (Document::Json(json), OPENCLAW_PRIMARY_KEY) = (document, path) {
            // `model` may be the primary model's name alone.
            let model = json.property(OPENCLAW_MODEL_KEY)?.value()?;
            if let Some(text) = model.as_string_lit() {
                return Some(text.decoded_value().ok());
            }
        }
        if path == OPENCLAW_MODELS_KEY {
            return get_list(document, path);
        }
        document.get(path)
    }

    fn set(&self, document: &mut Document, path: &str, value: &str) -> Result<(), String> {
        let path = if shorthand(document, path) {
            OPENCLAW_MODEL_KEY
        } else {
            path
        };
        if path == OPENCLAW_MODELS_KEY {
            return set_list(document, path, value);
        }
        document.set(path, value)
    }

    fn remove(&self, document: &mut Document, path: &str) {
        let path = if shorthand(document, path) {
            OPENCLAW_MODEL_KEY
        } else {
            path
        };
        document.remove(path);
    }

    fn emptied(&self) -> &'static [&'static str] {
        &[
            "models.providers.astrlink",
            "models.providers",
            "models",
            OPENCLAW_MODEL_KEY,
            "agents.defaults",
            "agents",
        ]
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn openclaw_json5_keeps_its_style_and_the_model_shorthand() {
        let existing = "// OpenClaw\n{\n  agents: { defaults: { model: 'anthropic/claude-opus', workspace: '~/claw' } },\n  models: { providers: { other: { baseUrl: 'http://x', models: [{ id: 'a', name: 'A' }], }, }, },\n}\n";
        let list = strings(&["oc-route"]);
        let models = vec![("model", "oc-route".to_string())];
        let details = Catalog::from([(
            "oc-route".to_string(),
            ModelDetails {
                context_window: Some(200_000),
                max_tokens: Some(32_000),
                ..Default::default()
            },
        )]);
        let plan = plan_write_with(
            OPENCLAW,
            &[Some(existing)],
            &listed(&models, &list),
            &details,
            None,
        )
        .unwrap();
        assert_eq!(plan.conflicts, [OPENCLAW_PRIMARY_KEY]);
        assert_eq!(
            plan.record.backup[OPENCLAW_PRIMARY_KEY],
            "anthropic/claude-opus"
        );
        let text = &plan.contents[0];
        assert!(text.starts_with("// OpenClaw\n{\n  agents: { defaults: { model: "));
        assert!(
            text.contains("workspace: '~/claw'") && text.contains("other: { baseUrl: 'http://x'")
        );
        let value = json5(text);
        assert_eq!(value["agents"]["defaults"]["model"], "astrlink/oc-route");
        assert_eq!(
            value["models"]["providers"]["astrlink"],
            serde_json::json!({
                "baseUrl": "http://127.0.0.1:18317/v1",
                "apiKey": TOKEN,
                "api": "openai-completions",
                "models": [{"id": "oc-route", "name": "oc-route", "contextWindow": 200_000, "maxTokens": 32_000}],
            })
        );
        let written = [Some(text.as_str())];
        assert_eq!(
            inspect(OPENCLAW, &written, Some(&plan.record), Some(ORIGIN)),
            ClientState::Configured
        );
        assert_eq!(
            current_models(OPENCLAW, &written).model.as_deref(),
            Some("oc-route")
        );
        let (removals, restored) = plan_restore(OPENCLAW, &written, &plan.record).unwrap();
        assert_eq!(restored, 1);
        let [Removal::Write(left)] = &removals[..] else {
            panic!("expected a rewrite");
        };
        assert_eq!(json5(left), json5(existing));
        assert!(left.starts_with("// OpenClaw\n"));

        // An object model keeps its fallbacks.
        let object = "{\"agents\": {\"defaults\": {\"model\": {\"primary\": \"a/b\", \"fallbacks\": [\"c/d\"]}}}}";
        let plan = plan_write(OPENCLAW, &[Some(object)], &connection(&models), None).unwrap();
        assert_eq!(
            json5(&plan.contents[0])["agents"]["defaults"]["model"],
            serde_json::json!({"primary": "astrlink/oc-route", "fallbacks": ["c/d"]})
        );
        for broken in ["{models: []}", "{a: 1 b: 2}"] {
            assert!(plan_write(OPENCLAW, &[Some(broken)], &connection(&models), None).is_err());
        }
    }
}
