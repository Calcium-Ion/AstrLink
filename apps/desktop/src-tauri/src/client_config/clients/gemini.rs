//! Gemini CLI reads its endpoint, key, and model from `~/.gemini/.env`, and
//! its login from `settings.json`.

use std::path::PathBuf;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{json, Document, Format},
        models::{Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Gemini);

const GEMINI_BASE_URL_KEY: &str = "GOOGLE_GEMINI_BASE_URL";
const GEMINI_API_KEY_KEY: &str = "GEMINI_API_KEY";
const GEMINI_MODEL_KEY: &str = "GEMINI_MODEL";
/// `.env` names that choose Gemini CLI's endpoint, credential, or model.
const GEMINI_ENV_GROUP: [&str; 3] = [GEMINI_BASE_URL_KEY, GEMINI_API_KEY_KEY, GEMINI_MODEL_KEY];
/// Without an explicit API-key login, Gemini CLI infers an auth type it
/// then refuses from the base URL alone.
const GEMINI_AUTH_KEY: &str = "security.auth.selectedType";
const GEMINI_AUTH_TYPE: &str = "gemini-api-key";
/// The gateway's automatic route, the one model name with a slash its
/// Gemini API takes.
const AUTO_MODEL: &str = "astrlink/auto";

struct Gemini;

/// `.env`, as `NAME=value` lines.
struct EnvLines;

/// `settings.json`, which picks the API-key login.
struct Settings;

impl ClientSpec for Gemini {
    fn id(&self) -> &'static str {
        "gemini"
    }

    fn name(&self) -> &'static str {
        "Gemini CLI"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["google.generate_content"]
    }

    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.gemini_dir()
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&EnvLines, &Settings]
    }

    fn config_paths(
        &self,
        home: Home,
        _target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        let dir = home.gemini_dir();
        vec![dir.join(".env"), dir.join("settings.json")]
    }

    /// Gemini CLI puts the model in the URL path, where the gateway takes
    /// no slash but its own automatic route's.
    fn check_model(&self, model: &str) -> Result<(), String> {
        if model.contains('/') && model != AUTO_MODEL {
            return Err("invalid model".into());
        }
        Ok(())
    }

    /// Gemini CLI appends its own API paths.
    fn base_url(&self, origin: &str) -> String {
        origin.to_string()
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[GEMINI_BASE_URL_KEY, GEMINI_API_KEY_KEY, GEMINI_AUTH_KEY]
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        CurrentModels {
            model: config.model(GEMINI_MODEL_KEY),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, _details: &Catalog) -> Vec<(String, String)> {
        let mut keys = vec![
            (GEMINI_BASE_URL_KEY.into(), self.base_url(connection.origin)),
            (GEMINI_API_KEY_KEY.into(), connection.token.into()),
        ];
        if let Some(model) = connection.model("model") {
            keys.push((GEMINI_MODEL_KEY.into(), model));
        }
        keys.push((GEMINI_AUTH_KEY.into(), GEMINI_AUTH_TYPE.into()));
        keys
    }

    fn base_url_key(&self) -> &'static str {
        GEMINI_BASE_URL_KEY
    }
}

impl ConfigFile for EnvLines {
    fn format(&self) -> Format {
        Format::Env {
            single_line: &GEMINI_ENV_GROUP,
        }
    }

    /// `.env` names have no dots; settings keys are dotted paths.
    fn holds(&self, key: &str) -> bool {
        !key.contains('.')
    }

    fn group(&self, document: &Document) -> Vec<(String, Option<String>)> {
        let mut keys = Vec::new();
        if let Document::Env(env) = document {
            for name in env.names() {
                if GEMINI_ENV_GROUP.contains(&name.as_str()) {
                    let value = env.get(&name);
                    keys.push((name, value));
                }
            }
        }
        keys
    }
}

impl ConfigFile for Settings {
    /// Gemini CLI strips comments, then parses strict JSON.
    fn format(&self) -> Format {
        Format::Json {
            syntax: Some(jsonc_parser::ParseOptions {
                allow_comments: true,
                ..json::strict()
            }),
            objects: &["security", "security.auth"],
            root_list: false,
        }
    }

    fn holds(&self, key: &str) -> bool {
        key.contains('.')
    }

    fn emptied(&self) -> &'static [&'static str] {
        &["security.auth", "security"]
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn gemini_env_lines_change_in_place_and_come_back_on_removal() {
        let env = "# keys\nexport GEMINI_API_KEY=\"old-key-123456\"\nHTTP_PROXY=http://proxy # mine\nGEMINI_MODEL=gemini-2.5-pro\n";
        let settings = "{\n  // mine\n  \"ui\": {\"theme\": \"dark\"},\n  \"security\": {\"auth\": {\"selectedType\": \"oauth-personal\"}}\n}\n";
        let existing = [Some(env), Some(settings)];
        let plan = plan_write(GEMINI, &existing, &connection(&gemini_models()), None).unwrap();
        assert_eq!(
            plan.conflicts,
            [
                "GEMINI_API_KEY",
                "GEMINI_MODEL",
                "security.auth.selectedType"
            ]
        );
        assert_eq!(
            plan.contents[0],
            format!("# keys\nexport GEMINI_API_KEY={TOKEN}\nHTTP_PROXY=http://proxy # mine\nGEMINI_MODEL=gemini-route\nGOOGLE_GEMINI_BASE_URL={ORIGIN}\n")
        );
        assert!(plan.contents[1].contains("// mine"));
        assert_eq!(
            json5(&plan.contents[1]),
            serde_json::json!({"ui": {"theme": "dark"}, "security": {"auth": {"selectedType": "gemini-api-key"}}})
        );
        assert_eq!(plan.record.base_url, ORIGIN);
        assert!(!plan.record.created_file);
        let written = [Some(plan.contents[0].as_str()), Some(&plan.contents[1])];
        assert_eq!(
            inspect(GEMINI, &written, Some(&plan.record), Some(ORIGIN)),
            ClientState::Configured
        );
        assert_eq!(
            current_models(GEMINI, &written).model.as_deref(),
            Some("gemini-route")
        );
        // Signing in with Google again is a change to the connection.
        let login = plan.contents[1].replace("gemini-api-key", "oauth-personal");
        assert_eq!(
            inspect(
                GEMINI,
                &[written[0], Some(&login)],
                Some(&plan.record),
                Some(ORIGIN)
            ),
            ClientState::Modified
        );

        let (removals, restored) = plan_restore(GEMINI, &written, &plan.record).unwrap();
        assert_eq!(restored, 3);
        let [Removal::Write(env_left), Removal::Write(settings_left)] = &removals[..] else {
            panic!("expected two rewrites");
        };
        assert_eq!(
            env_left,
            "# keys\nexport GEMINI_API_KEY=old-key-123456\nHTTP_PROXY=http://proxy # mine\nGEMINI_MODEL=gemini-2.5-pro\n"
        );
        assert_eq!(json5(settings_left), json5(settings));
    }

    #[test]
    fn gemini_env_files_keep_line_endings_and_drop_shadowed_names() {
        let mut env = EnvFile::parse(
            "A=1\r\nGEMINI_API_KEY=first\r\n  GEMINI_API_KEY: second # note\r\n",
            &GEMINI_ENV_GROUP,
        )
        .unwrap();
        assert_eq!(env.get("GEMINI_API_KEY").as_deref(), Some("second"));
        env.set("GEMINI_API_KEY", "with space").unwrap();
        env.set("GOOGLE_GEMINI_BASE_URL", "http://127.0.0.1:1")
            .unwrap();
        assert_eq!(
            env.render(),
            "A=1\r\n  GEMINI_API_KEY=\"with space\"\r\nGOOGLE_GEMINI_BASE_URL=http://127.0.0.1:1\r\n"
        );
        env.remove("A");
        env.remove("GEMINI_API_KEY");
        env.remove("GOOGLE_GEMINI_BASE_URL");
        assert!(env.is_empty());
        // A quoted value may span lines, but not one AstrLink writes.
        let other =
            EnvFile::parse("NOTE=\"one\ntwo\"\nGEMINI_MODEL='m'\n", &GEMINI_ENV_GROUP).unwrap();
        assert_eq!(other.get("NOTE").as_deref(), Some("one\ntwo"));
        assert_eq!(other.get("GEMINI_MODEL").as_deref(), Some("m"));
        assert_eq!(other.render(), "NOTE=\"one\ntwo\"\nGEMINI_MODEL='m'\n");
        let error = EnvFile::parse("# a\nGEMINI_API_KEY=\"one\ntwo\"\n", &GEMINI_ENV_GROUP)
            .err()
            .unwrap();
        assert_eq!(error.1, Some(2));
        assert!(EnvFile::encode("a\"b'\nc").is_err());
    }

    #[test]
    fn a_new_gemini_config_is_created_and_removed_whole() {
        let home = unique_home("gemini");
        fs::create_dir_all(home.join(".gemini")).unwrap();
        write(
            Home::local(&home),
            GEMINI,
            &connection(&gemini_models()),
            &tokens(),
            false,
        )
        .unwrap();
        let env = home.join(".gemini/.env");
        let settings = home.join(".gemini/settings.json");
        assert!(fs::read_to_string(&env).unwrap().contains(TOKEN));
        sync(Home::local(&home), "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert!(fs::read_to_string(&env)
            .unwrap()
            .contains("GOOGLE_GEMINI_BASE_URL=http://127.0.0.1:9000\n"));
        let statuses = status(
            Home::local(&home),
            Some("http://127.0.0.1:9000/"),
            Some(&tokens()),
        )
        .unwrap();
        assert_eq!(statuses[2].state, ClientState::Configured);
        assert_eq!(statuses[2].exists, [true, true]);
        remove(Home::local(&home), GEMINI).unwrap();
        assert!(!env.exists() && !settings.exists());
        fs::remove_dir_all(home).unwrap();
    }
}
