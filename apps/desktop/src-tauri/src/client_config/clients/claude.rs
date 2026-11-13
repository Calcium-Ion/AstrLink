//! Claude Code reads its gateway, token, and models from `env` in
//! `settings.json`, strict JSON.

use std::path::PathBuf;

use super::{push_present, ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::{read_json, Config},
        format::{Document, Format},
        models::{Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Claude);

/// Claude Code's model settings, by the `Models` field each comes from.
pub(crate) const CLAUDE_MODEL_KEYS: [(&str, &str); 6] = [
    ("model", "env.ANTHROPIC_MODEL"),
    ("haikuModel", "env.ANTHROPIC_DEFAULT_HAIKU_MODEL"),
    ("sonnetModel", "env.ANTHROPIC_DEFAULT_SONNET_MODEL"),
    ("opusModel", "env.ANTHROPIC_DEFAULT_OPUS_MODEL"),
    ("fableModel", "env.ANTHROPIC_DEFAULT_FABLE_MODEL"),
    ("subagentModel", "env.CLAUDE_CODE_SUBAGENT_MODEL"),
];

/// The `Models` fields Claude Code takes: every tier.
const MODEL_FIELDS: [&str; 6] = {
    let mut fields = [""; 6];
    let mut index = 0;
    while index < fields.len() {
        fields[index] = CLAUDE_MODEL_KEYS[index].0;
        index += 1;
    }
    fields
};

struct Claude;

/// `settings.json`.
struct Settings;

/// Claude Code settings keys that choose the endpoint, credential, or model.
fn claude_group_env(name: &str) -> bool {
    name.starts_with("ANTHROPIC_")
        || matches!(
            name,
            "CLAUDE_CODE_OAUTH_TOKEN"
                | "CLAUDE_CODE_SUBAGENT_MODEL"
                | "CLAUDE_CODE_USE_BEDROCK"
                | "CLAUDE_CODE_USE_VERTEX"
                | "CLAUDE_CODE_USE_FOUNDRY"
        )
}

impl ClientSpec for Claude {
    fn id(&self) -> &'static str {
        "claude"
    }

    fn name(&self) -> &'static str {
        "Claude Code"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["anthropic.messages"]
    }

    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.claude_dir()
    }

    fn detected(&self, home: Home, _target: Option<&str>) -> bool {
        home.claude_detected()
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&Settings]
    }

    fn config_paths(
        &self,
        home: Home,
        _target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![home.claude_dir().join("settings.json")]
    }

    fn model_fields(&self) -> &'static [&'static str] {
        &MODEL_FIELDS
    }

    /// Each tier may be left to Claude Code.
    fn requires_model(&self) -> bool {
        false
    }

    /// Claude Code appends its own API paths.
    fn base_url(&self, origin: &str) -> String {
        origin.to_string()
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &["env.ANTHROPIC_BASE_URL", "env.ANTHROPIC_AUTH_TOKEN"]
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let mut models = CurrentModels::default();
        for (field, path) in CLAUDE_MODEL_KEYS {
            if let Some(model) = config.model(path) {
                models.set(field, model);
            }
        }
        models
    }

    fn desired_keys(&self, connection: &Connection, _details: &Catalog) -> Vec<(String, String)> {
        let mut keys = vec![
            (
                "env.ANTHROPIC_BASE_URL".into(),
                self.base_url(connection.origin),
            ),
            // AUTH_TOKEN, not API_KEY: an API key asks for approval in the
            // interactive client and conflicts with a login.
            ("env.ANTHROPIC_AUTH_TOKEN".into(), connection.token.into()),
        ];
        for (field, path) in CLAUDE_MODEL_KEYS {
            if let Some(model) = connection.model(field) {
                keys.push((path.into(), model));
            }
        }
        keys
    }

    fn base_url_key(&self) -> &'static str {
        "env.ANTHROPIC_BASE_URL"
    }
}

impl ConfigFile for Settings {
    fn format(&self) -> Format {
        Format::Json {
            syntax: None,
            objects: &["env"],
            root_list: false,
        }
    }

    fn group(&self, document: &Document) -> Vec<(String, Option<String>)> {
        let mut keys = Vec::new();
        if let Document::Json(json) = document {
            if let Some(env) = json.object().and_then(|root| root.object_value("env")) {
                for property in env.properties() {
                    let Some(name) = property.decoded_name() else {
                        continue;
                    };
                    if claude_group_env(&name) {
                        let path = format!("env.{name}");
                        let value = self.get(document, &path).flatten();
                        keys.push((path, value));
                    }
                }
            }
        }
        push_present(&mut keys, self, document, "apiKeyHelper");
        keys
    }

    fn emptied(&self) -> &'static [&'static str] {
        &["env"]
    }
}

/// Claude Code's settings as it reads them, for importing the providers
/// they configure.
pub(crate) fn read_claude_settings(text: &str) -> Result<serde_json::Value, String> {
    read_json(&Settings, text)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn claude_settings_keep_other_keys_order_and_indent() {
        let existing = "{\n    \"theme\": \"dark\",\n    \"env\": {\n        \"EDITOR\": \"vim\"\n    },\n    \"permissions\": {\"allow\": []}\n}\n";
        let models = vec![
            ("model", "main-route".to_string()),
            ("fableModel", "fable-route".to_string()),
        ];
        let plan = plan_write(CLAUDE, &[Some(existing)], &connection(&models), None).unwrap();
        assert!(plan.conflicts.is_empty());
        assert_eq!(
            plan.contents[0],
            "{\n    \"theme\": \"dark\",\n    \"env\": {\n        \"EDITOR\": \"vim\",\n        \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:18317\",\n        \"ANTHROPIC_AUTH_TOKEN\": \"astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0\",\n        \"ANTHROPIC_MODEL\": \"main-route\",\n        \"ANTHROPIC_DEFAULT_FABLE_MODEL\": \"fable-route\"\n    },\n    \"permissions\": {\"allow\": []}\n}\n"
        );
        assert!(!plan.record.created_file);
        assert_eq!(plan.record.base_url, ORIGIN);
        assert_eq!(
            plan.record.keys.keys().collect::<Vec<_>>(),
            [
                "env.ANTHROPIC_AUTH_TOKEN",
                "env.ANTHROPIC_BASE_URL",
                "env.ANTHROPIC_DEFAULT_FABLE_MODEL",
                "env.ANTHROPIC_MODEL",
            ]
        );
        let removed = plan_remove(CLAUDE, &[Some(&plan.contents[0])], &plan.record).unwrap();
        assert_eq!(removed, [Removal::Write(existing.into())]);
    }

    #[test]
    fn claude_settings_are_created_and_removed_whole() {
        for existing in [None, Some("")] {
            let plan = plan_write(CLAUDE, &[existing], &connection(&[]), None).unwrap();
            assert_eq!(
                json(&plan.contents[0]),
                serde_json::json!({"env": {"ANTHROPIC_BASE_URL": ORIGIN, "ANTHROPIC_AUTH_TOKEN": TOKEN}})
            );
            assert!(plan.contents[0].ends_with("}\n"));
            assert_eq!(plan.record.created_file, existing.is_none());
            let removal = plan_remove(CLAUDE, &[Some(&plan.contents[0])], &plan.record).unwrap();
            if existing.is_none() {
                assert_eq!(removal, [Removal::Delete]);
            } else {
                assert_eq!(removal, [Removal::Write("{}\n".into())]);
            }
        }
    }

    #[test]
    fn claude_conflicts_are_named_and_removed_only_when_replacing() {
        let existing = r#"{"apiKeyHelper": "secret-helper", "env": {"ANTHROPIC_API_KEY": "sk-secret", "ANTHROPIC_BASE_URL": "https://other.example", "CLAUDE_CODE_USE_BEDROCK": "1", "ANTHROPIC_DEFAULT_OPUS_MODEL": "old-opus", "HTTP_PROXY": "http://proxy"}}"#;
        let plan = plan_write(CLAUDE, &[Some(existing)], &connection(&[]), None).unwrap();
        assert_eq!(
            plan.conflicts,
            [
                "env.ANTHROPIC_API_KEY",
                "env.ANTHROPIC_BASE_URL",
                "env.CLAUDE_CODE_USE_BEDROCK",
                "env.ANTHROPIC_DEFAULT_OPUS_MODEL",
                "apiKeyHelper",
            ]
        );
        let written = json(&plan.contents[0]);
        assert_eq!(
            written,
            serde_json::json!({"env": {"ANTHROPIC_BASE_URL": ORIGIN, "HTTP_PROXY": "http://proxy", "ANTHROPIC_AUTH_TOKEN": TOKEN}})
        );
        for secret in ["sk-secret", "secret-helper", "other.example", TOKEN] {
            assert!(!plan.conflicts.join(",").contains(secret));
        }
        // The record keeps what was replaced, and never the token.
        assert_eq!(
            plan.record.backup,
            BTreeMap::from(
                [
                    ("apiKeyHelper", "secret-helper"),
                    ("env.ANTHROPIC_API_KEY", "sk-secret"),
                    ("env.ANTHROPIC_BASE_URL", "https://other.example"),
                    ("env.ANTHROPIC_DEFAULT_OPUS_MODEL", "old-opus"),
                    ("env.CLAUDE_CODE_USE_BEDROCK", "1"),
                ]
                .map(|(key, value)| (key.to_string(), value.to_string()))
            )
        );
        assert!(!serde_json::to_string(&plan.record).unwrap().contains(TOKEN));
        // Removing AstrLink's config puts every replaced value back.
        let (removals, restored) =
            plan_restore(CLAUDE, &[Some(&plan.contents[0])], &plan.record).unwrap();
        assert_eq!(restored, 5);
        let [Removal::Write(text)] = removals.as_slice() else {
            panic!("expected the settings to stay");
        };
        assert_eq!(json(text), json(existing));
    }

    #[test]
    fn rewriting_replaces_our_own_values_without_asking() {
        let models = vec![("opusModel", "opus-route".to_string())];
        let first = plan_write(CLAUDE, &[None], &connection(&models), None).unwrap();
        let second = plan_write(
            CLAUDE,
            &[Some(&first.contents[0])],
            &Connection {
                token_id: "token_02",
                token: OTHER_TOKEN,
                origin: "http://127.0.0.1:9000",
                ..connection(&[])
            },
            Some(&first.record),
        )
        .unwrap();
        assert!(second.conflicts.is_empty());
        assert_eq!(
            json(&second.contents[0]),
            serde_json::json!({"env": {"ANTHROPIC_BASE_URL": "http://127.0.0.1:9000", "ANTHROPIC_AUTH_TOKEN": OTHER_TOKEN}})
        );
        assert!(second.record.created_file);
        assert_eq!(second.record.token_id, "token_02");
    }

    #[test]
    fn removal_keeps_keys_changed_or_added_after_writing() {
        let models = vec![("model", "main-route".to_string())];
        let plan = plan_write(CLAUDE, &[None], &connection(&models), None).unwrap();
        let edited = plan.contents[0]
            .replace("main-route", "user-route")
            .replace("\"env\": {", "\"env\": {\n    \"EDITOR\": \"vim\",");
        let [Removal::Write(left)] =
            &plan_remove(CLAUDE, &[Some(&edited)], &plan.record).unwrap()[..]
        else {
            panic!("expected a rewrite");
        };
        assert_eq!(
            json(left),
            serde_json::json!({"env": {"EDITOR": "vim", "ANTHROPIC_MODEL": "user-route"}})
        );
    }

    #[test]
    fn invalid_claude_settings_are_rejected() {
        for existing in [
            "{broken",
            "{/*c*/}",
            "[]",
            "{\"env\": \"x\"}",
            "{\"a\": 1,}",
        ] {
            let error = plan_write(CLAUDE, &[Some(existing)], &connection(&[]), None).unwrap_err();
            assert_eq!(error.file, 0);
            assert!(!error.message.contains(TOKEN));
            assert_eq!(
                inspect(CLAUDE, &[Some(existing)], None, Some(ORIGIN)),
                ClientState::Invalid
            );
        }
    }

    #[test]
    fn a_changed_connection_is_modified_and_never_synced() {
        let plan = plan_write(CLAUDE, &[None], &connection(&[]), None).unwrap();
        assert_eq!(
            inspect(
                CLAUDE,
                &[Some(&plan.contents[0])],
                Some(&plan.record),
                Some(ORIGIN)
            ),
            ClientState::Configured
        );
        assert_eq!(
            inspect(
                CLAUDE,
                &[Some(&plan.contents[0])],
                Some(&plan.record),
                Some("http://127.0.0.1:9000")
            ),
            ClientState::Outdated
        );
        let edited = plan.contents[0].replace(TOKEN, OTHER_TOKEN);
        assert_eq!(
            inspect(
                CLAUDE,
                &[Some(&edited)],
                Some(&plan.record),
                Some("http://127.0.0.1:9000")
            ),
            ClientState::Modified
        );
        assert_eq!(
            plan_sync(
                CLAUDE,
                &[Some(&edited)],
                &plan.record,
                "http://127.0.0.1:9000"
            )
            .unwrap(),
            None
        );
        assert_eq!(
            inspect(CLAUDE, &[None], Some(&plan.record), Some(ORIGIN)),
            ClientState::Modified
        );
        assert_eq!(
            inspect(CLAUDE, &[Some("{}")], None, Some(ORIGIN)),
            ClientState::NotConfigured
        );
    }

    #[test]
    fn a_synced_claude_config_changes_only_the_base_url() {
        let models = vec![("model", "main-route".to_string())];
        let plan = plan_write(CLAUDE, &[None], &connection(&models), None).unwrap();
        let (synced, record) = plan_sync(
            CLAUDE,
            &[Some(&plan.contents[0])],
            &plan.record,
            "http://127.0.0.1:9000",
        )
        .unwrap()
        .unwrap();
        let [synced] = &synced[..] else {
            panic!("expected one file");
        };
        assert_eq!(
            *synced,
            plan.contents[0].replace("http://127.0.0.1:18317", "http://127.0.0.1:9000")
        );
        assert_eq!(record.keys["env.ANTHROPIC_AUTH_TOKEN"], sha256_hex(TOKEN));
        assert!(
            plan_sync(CLAUDE, &[Some(synced)], &record, "http://127.0.0.1:9000")
                .unwrap()
                .is_none()
        );
    }
}
