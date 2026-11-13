//! Codex reads its provider, model, and reasoning effort from `config.toml`.
//! `auth.json`, its login, is never touched.

use std::path::PathBuf;

use toml_edit::{InlineTable, Item, Table, TableLike, Value};

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{
            toml::{set_toml_string, toml_string},
            Document, Format, TomlDocument,
        },
        models::{Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Codex);

pub(crate) const CODEX_PROVIDER: &str = "astrlink";
const CODEX_PROVIDER_PREFIX: &str = "model_providers.astrlink.";
/// Codex shows its own image generation and web search tools only for a
/// provider whose `http_headers` set this header. The gateway answers both
/// tools and strips the header before forwarding; any non-empty value works.
/// AstrLink owns this one header, and other headers in the table stay the
/// user's.
const CODEX_HEADERS_KEY: &str = "model_providers.astrlink.http_headers";
const CODEX_TOOLS_HEADER: &str = "x-openai-actor-authorization";
const CODEX_TOOLS_HEADER_VALUE: &str = "codex-imagegen";
/// How hard Codex's model reasons; left to Codex when not set.
const CODEX_REASONING_KEY: &str = "model_reasoning_effort";
const CODEX_REASONING_EFFORTS: [&str; 9] = [
    "none",
    "minimal",
    "low",
    "medium",
    "high",
    "xhigh",
    "max",
    "ultra",
    "persistent",
];

struct Codex;

/// `config.toml`.
struct CodexConfig;

/// AstrLink's entry in Codex's `http_headers`, matched case-insensitively as
/// Codex matches it.
fn codex_tools_header(headers: &dyn TableLike) -> Option<(String, &Item)> {
    headers
        .iter()
        .find(|(name, _)| name.eq_ignore_ascii_case(CODEX_TOOLS_HEADER))
        .map(|(name, item)| (name.to_string(), item))
}

/// Adds AstrLink's header to the provider's `http_headers` next to any
/// other header there. A value that is not a table is replaced.
fn set_codex_tools_header(provider: &mut dyn TableLike, value: &str) {
    if provider
        .get("http_headers")
        .and_then(Item::as_table_like)
        .is_none()
    {
        provider.insert(
            "http_headers",
            Item::Value(Value::InlineTable(InlineTable::new())),
        );
    }
    let Some(headers) = provider
        .get_mut("http_headers")
        .and_then(Item::as_table_like_mut)
    else {
        return;
    };
    let name = codex_tools_header(&*headers)
        .map(|(name, _)| name)
        .unwrap_or_else(|| CODEX_TOOLS_HEADER.to_string());
    set_toml_string(headers, &name, value);
}

impl ClientSpec for Codex {
    fn id(&self) -> &'static str {
        "codex"
    }

    fn name(&self) -> &'static str {
        "Codex"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.responses"]
    }

    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.codex_dir()
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&CodexConfig]
    }

    fn config_paths(
        &self,
        home: Home,
        _target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![home.codex_dir().join("config.toml")]
    }

    fn reasoning_efforts(&self) -> &'static [&'static str] {
        &CODEX_REASONING_EFFORTS
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[
            "model_provider",
            "model_providers.astrlink.base_url",
            "model_providers.astrlink.wire_api",
            "model_providers.astrlink.experimental_bearer_token",
        ]
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        CurrentModels {
            model: config.model("model"),
            reasoning_effort: config.model(CODEX_REASONING_KEY),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, _details: &Catalog) -> Vec<(String, String)> {
        let base_url = self.base_url(connection.origin);
        let mut keys = vec![("model_provider".into(), CODEX_PROVIDER.into())];
        if let Some(model) = connection.model("model") {
            keys.push(("model".into(), model));
        }
        if let Some(effort) = connection.model("reasoningEffort") {
            keys.push((CODEX_REASONING_KEY.into(), effort));
        }
        // The display name is local UI; a reserved ID or the name
        // "OpenAI" would make Codex treat this as its built-in provider.
        for (key, value) in [
            ("name", "AstrLink"),
            ("base_url", base_url.as_str()),
            ("wire_api", "responses"),
            ("experimental_bearer_token", connection.token),
        ] {
            keys.push((format!("{CODEX_PROVIDER_PREFIX}{key}"), value.into()));
        }
        keys.push((CODEX_HEADERS_KEY.into(), CODEX_TOOLS_HEADER_VALUE.into()));
        keys
    }

    fn base_url_key(&self) -> &'static str {
        "model_providers.astrlink.base_url"
    }

    fn added_keys(&self) -> &'static [(&'static str, &'static str)] {
        &[(CODEX_HEADERS_KEY, CODEX_TOOLS_HEADER_VALUE)]
    }
}

impl ConfigFile for CodexConfig {
    fn format(&self) -> Format {
        Format::Toml
    }

    fn group(&self, document: &Document) -> Vec<(String, Option<String>)> {
        let mut keys = Vec::new();
        let Document::Toml(TomlDocument { document: toml, .. }) = document else {
            return keys;
        };
        for key in ["model_provider", "model"] {
            if let Some(item) = toml.get(key) {
                keys.push((key.into(), toml_string(item)));
            }
        }
        let provider = toml
            .get("model_providers")
            .and_then(Item::as_table_like)
            .and_then(|providers| providers.get(CODEX_PROVIDER));
        match provider.map(|item| item.as_table_like()) {
            Some(Some(table)) => {
                for (key, _) in table.iter() {
                    let path = format!("{CODEX_PROVIDER_PREFIX}{key}");
                    // Headers without AstrLink's own are the user's.
                    if let Some(value) = self.get(document, &path) {
                        keys.push((path, value));
                    }
                }
            }
            Some(None) => keys.push(("model_providers.astrlink".into(), None)),
            None => {}
        }
        keys
    }

    fn get(&self, document: &Document, path: &str) -> Option<Option<String>> {
        let (Document::Toml(TomlDocument { document, .. }), Some(key)) =
            (document, path.strip_prefix(CODEX_PROVIDER_PREFIX))
        else {
            return document.get(path);
        };
        let item = document
            .get("model_providers")?
            .as_table_like()?
            .get(CODEX_PROVIDER)?
            .as_table_like()?
            .get(key)?;
        if path != CODEX_HEADERS_KEY {
            return Some(toml_string(item));
        }
        match item.as_table_like() {
            Some(headers) => codex_tools_header(headers).map(|(_, item)| toml_string(item)),
            None => Some(None),
        }
    }

    fn set(&self, document: &mut Document, path: &str, value: &str) -> Result<(), String> {
        let (Document::Toml(TomlDocument { document, .. }), Some(key)) =
            (&mut *document, path.strip_prefix(CODEX_PROVIDER_PREFIX))
        else {
            return document.set(path, value);
        };
        let root = document.as_table_mut();
        if !root.contains_key("model_providers") {
            let mut providers = Table::new();
            providers.set_implicit(true);
            root.insert("model_providers", Item::Table(providers));
        }
        let providers = &mut root["model_providers"];
        let inline = providers.is_inline_table();
        let providers = providers
            .as_table_like_mut()
            .ok_or("has a model_providers value that is not a table")?;
        if providers
            .get(CODEX_PROVIDER)
            .and_then(Item::as_table_like)
            .is_none()
        {
            let table = if inline {
                Item::Value(Value::InlineTable(InlineTable::new()))
            } else {
                Item::Table(Table::new())
            };
            providers.insert(CODEX_PROVIDER, table);
        }
        let provider = providers
            .get_mut(CODEX_PROVIDER)
            .and_then(Item::as_table_like_mut)
            .ok_or("unable to create model_providers.astrlink")?;
        if path == CODEX_HEADERS_KEY {
            set_codex_tools_header(provider, value);
        } else {
            set_toml_string(provider, key, value);
        }
        Ok(())
    }

    fn remove(&self, document: &mut Document, path: &str) {
        let Document::Toml(toml) = document else {
            document.remove(path);
            return;
        };
        let providers = toml
            .document
            .get_mut("model_providers")
            .and_then(Item::as_table_like_mut);
        if path == "model_providers.astrlink" {
            if let Some(providers) = providers {
                providers.remove(CODEX_PROVIDER);
            }
        } else if let Some(key) = path.strip_prefix(CODEX_PROVIDER_PREFIX) {
            if let Some(provider) = providers
                .and_then(|providers| providers.get_mut(CODEX_PROVIDER))
                .and_then(Item::as_table_like_mut)
            {
                let headers = provider
                    .get_mut("http_headers")
                    .and_then(Item::as_table_like_mut)
                    .filter(|_| path == CODEX_HEADERS_KEY);
                match headers {
                    Some(headers) => {
                        if let Some((name, _)) = codex_tools_header(&*headers) {
                            headers.remove(&name);
                        }
                    }
                    None => {
                        provider.remove(key);
                    }
                }
            }
        } else {
            toml.remove(path);
        }
    }

    fn tidy(&self, document: &mut Document) {
        let Document::Toml(TomlDocument { document, .. }) = document else {
            return;
        };
        let empty = |item: Option<&Item>| {
            item.and_then(Item::as_table_like)
                .is_some_and(|table| table.is_empty())
        };
        if let Some(providers) = document
            .get_mut("model_providers")
            .and_then(Item::as_table_like_mut)
        {
            if let Some(provider) = providers
                .get_mut(CODEX_PROVIDER)
                .and_then(Item::as_table_like_mut)
            {
                if empty(provider.get("http_headers")) {
                    provider.remove("http_headers");
                }
            }
            if empty(providers.get(CODEX_PROVIDER)) {
                providers.remove(CODEX_PROVIDER);
            }
        }
        if empty(document.get("model_providers")) {
            document.remove("model_providers");
        }
    }
}

/// Codex's config.toml as Codex reads it, for importing its providers.
pub(crate) fn read_codex_config(text: &str) -> Result<toml_edit::DocumentMut, String> {
    TomlDocument::parse(text)
        .map(|toml| toml.document)
        .map_err(|(message, _)| message)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn codex_config_keeps_comments_and_other_tables() {
        let existing = "# user settings\nmodel = \"gpt-5\" # mine\napproval_policy = \"never\"\n\n[profiles.fast]\nmodel = \"o3\"\n\n[model_providers.other]\nname = \"Other\"\n";
        let plan =
            plan_write(CODEX, &[Some(existing)], &connection(&codex_models()), None).unwrap();
        assert_eq!(plan.conflicts, ["model"]);
        assert_eq!(
            plan.contents[0],
            "# user settings\nmodel = \"gpt-route\" # mine\napproval_policy = \"never\"\nmodel_provider = \"astrlink\"\n\n[profiles.fast]\nmodel = \"o3\"\n\n[model_providers.other]\nname = \"Other\"\n\n[model_providers.astrlink]\nname = \"AstrLink\"\nbase_url = \"http://127.0.0.1:18317/v1\"\nwire_api = \"responses\"\nexperimental_bearer_token = \"astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0\"\nhttp_headers = { x-openai-actor-authorization = \"codex-imagegen\" }\n"
        );
        assert_eq!(plan.record.base_url, "http://127.0.0.1:18317/v1");
        let [Removal::Write(removed)] =
            &plan_remove(CODEX, &[Some(&plan.contents[0])], &plan.record).unwrap()[..]
        else {
            panic!("expected a rewrite");
        };
        // The model AstrLink replaced comes back.
        assert_eq!(removed, existing);
    }

    #[test]
    fn codex_config_is_created_without_auth_settings() {
        let plan = plan_write(CODEX, &[None], &connection(&codex_models()), None).unwrap();
        assert_eq!(
            plan.contents[0],
            "model_provider = \"astrlink\"\nmodel = \"gpt-route\"\n\n[model_providers.astrlink]\nname = \"AstrLink\"\nbase_url = \"http://127.0.0.1:18317/v1\"\nwire_api = \"responses\"\nexperimental_bearer_token = \"astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0\"\nhttp_headers = { x-openai-actor-authorization = \"codex-imagegen\" }\n"
        );
        // The reserved provider ID or the name "OpenAI" would make Codex
        // treat this as its built-in provider.
        for forbidden in [
            "env_key",
            "requires_openai_auth",
            "\"OpenAI\"",
            "\"openai\"",
            "model_providers.openai",
        ] {
            assert!(!plan.contents[0].contains(forbidden));
        }
        assert_eq!(
            plan_remove(CODEX, &[Some(&plan.contents[0])], &plan.record).unwrap(),
            [Removal::Delete]
        );
    }

    #[test]
    fn codex_tools_header_sits_beside_the_users_own_headers() {
        let existing = "[model_providers.astrlink.http_headers]\nx-team = \"blue\"\n";
        let plan =
            plan_write(CODEX, &[Some(existing)], &connection(&codex_models()), None).unwrap();
        assert!(plan.conflicts.is_empty(), "{:?}", plan.conflicts);
        assert!(plan.contents[0].contains("x-team = \"blue\""));
        assert!(plan.contents[0].contains("x-openai-actor-authorization = \"codex-imagegen\""));
        let [Removal::Write(removed)] =
            &plan_remove(CODEX, &[Some(&plan.contents[0])], &plan.record).unwrap()[..]
        else {
            panic!("expected a rewrite");
        };
        assert!(removed.contains("x-team = \"blue\""), "{removed}");
        for gone in [
            "x-openai-actor-authorization",
            "experimental_bearer_token",
            "model_provider =",
        ] {
            assert!(!removed.contains(gone), "{removed}");
        }

        // A value the user gave the header is theirs until they confirm.
        let existing = "[model_providers.astrlink]\nhttp_headers = { X-OpenAI-Actor-Authorization = \"mine\", x-team = \"blue\" }\n";
        let plan =
            plan_write(CODEX, &[Some(existing)], &connection(&codex_models()), None).unwrap();
        assert_eq!(plan.conflicts, [CODEX_HEADERS_KEY]);
        assert!(plan.contents[0].contains(
            "http_headers = { X-OpenAI-Actor-Authorization = \"codex-imagegen\", x-team = \"blue\" }"
        ));
    }

    #[test]
    fn sync_adds_the_codex_tools_header_to_released_configs_once() {
        let plan = plan_write(CODEX, &[None], &connection(&codex_models()), None).unwrap();
        // Released builds wrote no header and kept no fingerprint for it.
        let released = plan.contents[0].replace(
            "http_headers = { x-openai-actor-authorization = \"codex-imagegen\" }\n",
            "",
        );
        let mut record = plan.record.clone();
        record.keys.remove(CODEX_HEADERS_KEY);
        assert_eq!(
            inspect(CODEX, &[Some(&released)], Some(&record), Some(ORIGIN)),
            ClientState::Configured
        );
        let (contents, next) = plan_sync(CODEX, &[Some(&released)], &record, ORIGIN)
            .unwrap()
            .expect("the header is added");
        assert_eq!(contents[0], plan.contents[0]);
        assert_eq!(next.keys, plan.record.keys);
        assert!(plan_sync(CODEX, &[Some(&contents[0])], &next, ORIGIN)
            .unwrap()
            .is_none());

        // A header the user removed after AstrLink wrote it stays removed.
        assert!(plan_sync(CODEX, &[Some(&released)], &plan.record, ORIGIN)
            .unwrap()
            .is_none());
        // A changed connection is left alone.
        let modified = released.replace("wire_api = \"responses\"", "wire_api = \"chat\"");
        assert!(plan_sync(CODEX, &[Some(&modified)], &record, ORIGIN)
            .unwrap()
            .is_none());
        // A new port and the header arrive in one write.
        let (contents, next) =
            plan_sync(CODEX, &[Some(&released)], &record, "http://127.0.0.1:9000")
                .unwrap()
                .expect("synced");
        assert!(contents[0].contains("base_url = \"http://127.0.0.1:9000/v1\""));
        assert!(contents[0].contains("x-openai-actor-authorization = \"codex-imagegen\""));
        assert_eq!(next.base_url, "http://127.0.0.1:9000/v1");
    }

    #[test]
    fn codex_conflicts_cover_a_foreign_provider_table() {
        let existing = "model_provider = \"other\"\n\n[model_providers.astrlink]\nbase_url = \"https://secret.example/v1\"\nenv_key = \"SECRET_KEY\"\n";
        let plan =
            plan_write(CODEX, &[Some(existing)], &connection(&codex_models()), None).unwrap();
        assert_eq!(
            plan.conflicts,
            [
                "model_provider",
                "model_providers.astrlink.base_url",
                "model_providers.astrlink.env_key",
            ]
        );
        assert!(!plan.contents[0].contains("env_key"));
        assert!(!plan.contents[0].contains("secret.example"));
        let plan = plan_write(
            CODEX,
            &[Some("model_providers = { astrlink = \"broken\" }\n")],
            &connection(&codex_models()),
            None,
        )
        .unwrap();
        assert_eq!(plan.conflicts, ["model_providers.astrlink"]);
        assert!(plan.contents[0].contains("experimental_bearer_token"));
    }

    #[test]
    fn codex_crlf_files_stay_crlf() {
        let existing = "# a\r\napproval_policy = \"never\"\r\n";
        let plan =
            plan_write(CODEX, &[Some(existing)], &connection(&codex_models()), None).unwrap();
        assert!(plan.contents[0].starts_with("# a\r\napproval_policy = \"never\"\r\n"));
        assert!(!plan.contents[0].replace("\r\n", "").contains('\n'));
        let [Removal::Write(removed)] =
            &plan_remove(CODEX, &[Some(&plan.contents[0])], &plan.record).unwrap()[..]
        else {
            panic!("expected a rewrite");
        };
        assert_eq!(removed, existing);
    }

    #[test]
    fn a_model_the_client_changed_does_not_block_sync() {
        let plan = plan_write(CODEX, &[None], &connection(&codex_models()), None).unwrap();
        let edited = plan.contents[0].replace("gpt-route", "picked-in-tui");
        assert_eq!(
            inspect(CODEX, &[Some(&edited)], Some(&plan.record), Some(ORIGIN)),
            ClientState::Configured
        );
        let (synced, record) = plan_sync(
            CODEX,
            &[Some(&edited)],
            &plan.record,
            "http://127.0.0.1:9000",
        )
        .unwrap()
        .unwrap();
        let [synced] = &synced[..] else {
            panic!("expected one file");
        };
        assert_eq!(*synced, edited.replace("127.0.0.1:18317", "127.0.0.1:9000"));
        assert_eq!(record.base_url, "http://127.0.0.1:9000/v1");
        assert_eq!(record.token_sha256, plan.record.token_sha256);
        assert_eq!(
            inspect(
                CODEX,
                &[Some(synced)],
                Some(&record),
                Some("http://127.0.0.1:9000")
            ),
            ClientState::Configured
        );
        // The model the user picked is theirs now and survives removal.
        let [Removal::Write(removed)] = &plan_remove(CODEX, &[Some(synced)], &record).unwrap()[..]
        else {
            panic!("expected a rewrite");
        };
        assert_eq!(removed, "model = \"picked-in-tui\"\n");
    }

    #[test]
    fn codex_apply_never_touches_auth_json() {
        let home = unique_home("codex-auth");
        let auth = home.join(".codex/auth.json");
        let login = "{\"auth_mode\": \"chatgpt\", \"tokens\": {}}\n";
        fs::write(&auth, login).unwrap();
        write(
            Home::local(&home),
            CODEX,
            &connection(&codex_models()),
            &tokens(),
            false,
        )
        .unwrap();
        sync(Home::local(&home), "http://127.0.0.1:9000/", &tokens()).unwrap();
        let config = fs::read_to_string(home.join(".codex/config.toml")).unwrap();
        assert!(config.contains("base_url = \"http://127.0.0.1:9000/v1\""));
        remove(Home::local(&home), CODEX).unwrap();
        assert!(!home.join(".codex/config.toml").exists());
        assert_eq!(fs::read_to_string(&auth).unwrap(), login);
        let entries = fs::read_dir(home.join(".codex"))
            .unwrap()
            .map(|entry| entry.unwrap().file_name())
            .collect::<Vec<_>>();
        assert_eq!(entries, ["auth.json"]);
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn a_provider_another_tool_switches_to_is_a_change_writing_takes_back() {
        let home = unique_home("switched");
        let config = home.join(".codex/config.toml");
        write(
            Home::local(&home),
            CODEX,
            &connection(&codex_models()),
            &tokens(),
            false,
        )
        .unwrap();
        let written = fs::read_to_string(&config).unwrap();
        // A tool like CC Switch takes over the route and model keys and adds
        // its own provider table; AstrLink's table stays behind.
        let switched = written
            .replace("model_provider = \"astrlink\"", "model_provider = \"custom\"")
            .replace(
                "model = \"gpt-route\"\n",
                "model = \"gpt-route\"\nmodel_reasoning_effort = \"high\"\n",
            )
            + &format!(
                "\n[model_providers.custom]\nname = \"AstrLink · VS Code\"\nbase_url = \"{ORIGIN}/v1\"\nwire_api = \"responses\"\nexperimental_bearer_token = \"{TOKEN}\"\n"
            );
        let codex = |home: &Path| {
            status(
                Home::local(home),
                Some("http://127.0.0.1:18317/"),
                Some(&tokens()),
            )
            .unwrap()
            .remove(1)
        };
        fs::write(&config, &switched).unwrap();
        assert_eq!(codex(&home).state, ClientState::Modified);
        sync(Home::local(&home), "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert_eq!(fs::read_to_string(&config).unwrap(), switched);

        // Writing takes it back, asking only about the route the tool chose.
        assert_eq!(
            asked(
                write(
                    Home::local(&home),
                    CODEX,
                    &connection(&codex_models()),
                    &tokens(),
                    false
                )
                .unwrap()
            ),
            Some((vec!["model_provider".into()], None))
        );
        write(
            Home::local(&home),
            CODEX,
            &connection(&codex_models()),
            &tokens(),
            true,
        )
        .unwrap();
        let rewritten = fs::read_to_string(&config).unwrap();
        assert!(rewritten.contains("model_provider = \"astrlink\""));
        assert!(rewritten.contains("model_reasoning_effort = \"high\""));
        assert!(rewritten.contains("[model_providers.custom]"));
        assert_eq!(codex(&home).state, ClientState::Configured);
        // Removal gives the tool its route back.
        remove(Home::local(&home), CODEX).unwrap();
        assert!(fs::read_to_string(&config)
            .unwrap()
            .contains("model_provider = \"custom\""));
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn comments_above_removed_codex_keys_stay() {
        let models = codex_models();
        for (existing, expected) in [
            ("# header\n", "# header\n"),
            ("# header\nmodel = \"old\"\n", "# header\nmodel = \"old\"\n"),
            (
                "approval_policy = \"never\"\n# pick one\nmodel = \"old\"\n\n[tui]\nx = 1\n",
                "approval_policy = \"never\"\n# pick one\nmodel = \"old\"\n\n[tui]\nx = 1\n",
            ),
        ] {
            let plan = plan_write(CODEX, &[Some(existing)], &connection(&models), None).unwrap();
            let [Removal::Write(removed)] =
                &plan_remove(CODEX, &[Some(&plan.contents[0])], &plan.record).unwrap()[..]
            else {
                panic!("expected a rewrite");
            };
            assert_eq!(removed, expected);
        }
    }

    #[test]
    fn codex_effort_and_claude_subagents_are_written_and_given_back() {
        let existing = "model_reasoning_effort = \"high\"\n";
        let models = vec![
            ("model", "gpt-route".to_string()),
            ("reasoningEffort", "low".to_string()),
        ];
        let plan = plan_write(CODEX, &[Some(existing)], &connection(&models), None).unwrap();
        assert_eq!(plan.conflicts, [CODEX_REASONING_KEY]);
        assert!(plan.contents[0].contains("model_reasoning_effort = \"low\""));
        assert_eq!(
            current_models(CODEX, &[Some(&plan.contents[0])])
                .reasoning_effort
                .as_deref(),
            Some("low")
        );
        // Clearing the effort gives back the one it replaced.
        let cleared = plan_write(
            CODEX,
            &[Some(&plan.contents[0])],
            &connection(&codex_models()),
            Some(&plan.record),
        )
        .unwrap();
        assert!(cleared.conflicts.is_empty());
        assert!(cleared.contents[0].starts_with("model_reasoning_effort = \"high\"\n"));
        assert!(!cleared.record.keys.contains_key(CODEX_REASONING_KEY));
        assert!(cleared.record.backup.is_empty());

        let models = vec![("subagentModel", "sub-route".to_string())];
        let plan = plan_write(CLAUDE, &[None], &connection(&models), None).unwrap();
        assert_eq!(
            json(&plan.contents[0])["env"]["CLAUDE_CODE_SUBAGENT_MODEL"],
            "sub-route"
        );
        assert_eq!(
            current_models(CLAUDE, &[Some(&plan.contents[0])]),
            CurrentModels {
                subagent_model: Some("sub-route".into()),
                ..Default::default()
            }
        );
    }
}
