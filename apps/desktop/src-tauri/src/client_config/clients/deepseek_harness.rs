//! DeepSeek Harness (`dsh`), in its web profile (`dsh web`) and desktop
//! app. Each profile reads `~/.dsh/profiles/<profile>/cordis.patch.yml`, a
//! YAML list of patch rows: AstrLink adds its routes, one per protocol, to
//! the `llm-pi-ai` row and picks the default model in the
//! `agent-default-model` row. The token goes to the credentials file every
//! profile shares, under a name of the profile's own.
//!
//! The `deepseek-official` route stays as it is: it uploads session logs.

use std::path::PathBuf;

use serde_json::Value as Json;
use yaml_serde::Value;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{yaml::Step, Document, Format, YamlDocument, YamlRoot},
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&DeepseekHarness);

struct DeepseekHarness;

/// One route per protocol, since a route has one API: protocol, route
/// name, `api`, and the name the model picker groups it under.
const ROUTES: [(&str, &str, &str, &str); 3] = [
    (
        "openai.chat",
        "astrlink-chat",
        "openai-completions",
        "AstrLink (OpenAI Chat)",
    ),
    (
        "anthropic.messages",
        "astrlink-anthropic",
        "anthropic-messages",
        "AstrLink (Anthropic)",
    ),
    (
        "openai.responses",
        "astrlink-responses",
        "openai-responses",
        "AstrLink (Responses)",
    ),
];
const MODELS_ROW: &str = "llm-pi-ai";
const MODELS_PACKAGE: &str = "@deepseek-ai/dsh-llm-pi-ai";
const DEFAULT_ROW: &str = "agent-default-model";
const DEFAULT_PACKAGE: &str = "@deepseek-ai/dsh-agent-default-model";
const ROUTE_PREFIX: &str = "llm-pi-ai.providers.";
const DEFAULT_KEY: &str = "agent-default-model.config";
const REF_PREFIX: &str = "refs.";
/// dsh's reasoning levels, in its order; the catalog's `none` is `off`.
const EFFORTS: [&str; 7] = ["off", "minimal", "low", "medium", "high", "xhigh", "max"];

fn route_key(route: &str) -> String {
    format!("{ROUTE_PREFIX}{route}")
}

/// The credential name the place's routes read the token from.
fn reference(target: Option<&str>) -> String {
    format!(
        "ASTRLINK_{}_API_KEY",
        target.unwrap_or("web").to_ascii_uppercase()
    )
}

/// The Anthropic route takes the bare origin; the others `/v1`.
fn route_url(route: &str, origin: &str) -> String {
    if route == "astrlink-anthropic" {
        origin.to_string()
    } else {
        format!("{origin}/v1")
    }
}

/// How AstrLink's properties are placed among their siblings.
fn rank(name: &str) -> usize {
    const ORDER: [&str; 12] = [
        "id",
        "config",
        "providers",
        "displayName",
        "apiKeyEnv",
        "api",
        "baseURL",
        "models",
        "contextWindow",
        "maxTokens",
        "reasoningEfforts",
        "provider",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .or_else(|| {
            EFFORTS
                .iter()
                .position(|effort| *effort == name)
                .map(|effort| ORDER.len() + effort)
        })
        .unwrap_or(usize::MAX)
}

/// The model entry of a route, with what the catalog says of the model.
fn model_entry(id: &str, details: &Catalog) -> Json {
    let mut entry = serde_json::json!({ "id": id });
    let Some(details) = details.get(id) else {
        return entry;
    };
    if let Some(context) = details.context_window {
        entry["contextWindow"] = context.into();
    }
    if let Some(output) = details.max_tokens {
        entry["maxTokens"] = output.into();
    }
    let mut efforts = serde_json::Map::new();
    for level in details.reasoning.iter().flatten() {
        let name = if level == "none" {
            "off"
        } else {
            level.as_str()
        };
        if EFFORTS.contains(&name) {
            efforts.insert(name.into(), level.as_str().into());
        }
    }
    // dsh takes no levels, or `off` alone, as a broken route.
    if efforts.keys().any(|name| name != "off") {
        entry["reasoningEfforts"] = Json::Object(efforts);
    }
    entry
}

/// The last row dsh reads for `id`: one with no `insert`, naming no other
/// package.
fn row(value: &Value, id: &str, package: &str) -> Option<usize> {
    value.as_sequence()?.iter().rposition(|item| {
        item.as_mapping().is_some_and(|row| {
            row.get("id").and_then(Value::as_str) == Some(id)
                && !row.contains_key("insert")
                && row
                    .get("name")
                    .map_or(true, |name| name.as_str() == Some(package))
        })
    })
}

/// Where `key` is in the patch file, if its row is there.
fn path_of<'k>(document: &YamlDocument, key: &'k str) -> Option<Vec<Step<'k>>> {
    if let Some(route) = key.strip_prefix(ROUTE_PREFIX) {
        let at = row(document.value(), MODELS_ROW, MODELS_PACKAGE)?;
        return Some(vec![
            Step::Index(at),
            Step::Key("config"),
            Step::Key("providers"),
            Step::Key(route),
        ]);
    }
    (key == DEFAULT_KEY).then_some(())?;
    let at = row(document.value(), DEFAULT_ROW, DEFAULT_PACKAGE)?;
    Some(vec![Step::Index(at), Step::Key("config")])
}

/// `cordis.patch.yml` of one profile.
struct PatchFile;

/// `~/.dsh/.credentials.yaml`, which every profile reads.
struct Credentials;

impl PatchFile {
    fn set_route(document: &mut YamlDocument, route: &str, value: &Json) -> Result<(), String> {
        let Some(at) = row(document.value(), MODELS_ROW, MODELS_PACKAGE) else {
            let row = serde_json::json!({
                "id": MODELS_ROW,
                "config": { "providers": { route: value } },
            });
            return document.push(&[], &row, rank);
        };
        let here = [Step::Index(at)];
        if document.get(&[Step::Index(at), Step::Key("disabled")]) == Some(&Value::Bool(true)) {
            return Err(format!("has its {MODELS_ROW} row turned off"));
        }
        match document.get(&[Step::Index(at), Step::Key("config")]) {
            None | Some(Value::Null) => {
                let config = serde_json::json!({ "providers": { route: value } });
                return document.set(&here, "config", &config, rank);
            }
            Some(Value::Mapping(_)) => {}
            Some(_) => return Err(format!("has a {MODELS_ROW} config that is not a mapping")),
        }
        let config = [Step::Index(at), Step::Key("config")];
        match document.get(&[Step::Index(at), Step::Key("config"), Step::Key("providers")]) {
            None | Some(Value::Null) => {
                let providers = serde_json::json!({ route: value });
                document.set(&config, "providers", &providers, rank)
            }
            Some(Value::Mapping(_)) => {
                let providers = [Step::Index(at), Step::Key("config"), Step::Key("providers")];
                document.set(&providers, route, value, rank)
            }
            Some(_) => Err(format!("has {MODELS_ROW} providers that are not a mapping")),
        }
    }

    fn set_default(document: &mut YamlDocument, value: &Json) -> Result<(), String> {
        match row(document.value(), DEFAULT_ROW, DEFAULT_PACKAGE) {
            Some(at) => document.set(&[Step::Index(at)], "config", value, rank),
            None => {
                let row = serde_json::json!({ "id": DEFAULT_ROW, "config": value });
                document.push(&[], &row, rank)
            }
        }
    }

    /// Whether a row holds nothing beyond what AstrLink leaves once its
    /// values went: its id, and an empty route list.
    fn spent(row: &Value) -> bool {
        let Some(row) = row.as_mapping() else {
            return false;
        };
        let id = row.get("id").and_then(Value::as_str);
        let rest: Vec<_> = row
            .iter()
            .filter(|(name, _)| name.as_str() != Some("id"))
            .collect();
        match (id, rest.as_slice()) {
            (Some(DEFAULT_ROW), []) => true,
            (Some(MODELS_ROW), [(name, config)]) if name.as_str() == Some("config") => {
                config.as_mapping().is_some_and(|config| {
                    config.is_empty()
                        || (config.len() == 1
                            && config
                                .get("providers")
                                .and_then(Value::as_mapping)
                                .is_some_and(|providers| providers.is_empty()))
                })
            }
            _ => false,
        }
    }
}

impl ConfigFile for PatchFile {
    fn format(&self) -> Format {
        Format::Yaml {
            root: YamlRoot::Sequence,
        }
    }

    fn holds(&self, key: &str) -> bool {
        key.starts_with(ROUTE_PREFIX) || key == DEFAULT_KEY
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Yaml(yaml) = document else {
            return None;
        };
        let value = yaml.get_json(&path_of(yaml, key)?)?;
        Some(Some(value.to_string()))
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Yaml(yaml) = document else {
            return Err("is not YAML".into());
        };
        let value: Json = serde_json::from_str(value).map_err(|error| error.to_string())?;
        match key.strip_prefix(ROUTE_PREFIX) {
            Some(route) => Self::set_route(yaml, route, &value),
            None => Self::set_default(yaml, &value),
        }
    }

    fn remove(&self, document: &mut Document, key: &str) {
        let Document::Yaml(yaml) = document else {
            return;
        };
        if let Some(path) = path_of(yaml, key) {
            let _ = yaml.remove(&path);
        }
    }

    /// Rows AstrLink added go once they hold nothing.
    fn tidy(&self, document: &mut Document) {
        let Document::Yaml(yaml) = document else {
            return;
        };
        while let Some(at) = yaml
            .value()
            .as_sequence()
            .and_then(|rows| rows.iter().rposition(Self::spent))
        {
            if yaml.remove(&[Step::Index(at)]).is_err() {
                return;
            }
        }
    }
}

impl ConfigFile for Credentials {
    fn format(&self) -> Format {
        Format::Yaml {
            root: YamlRoot::Mapping,
        }
    }

    fn secret(&self) -> bool {
        true
    }

    fn holds(&self, key: &str) -> bool {
        key.starts_with(REF_PREFIX)
    }

    /// A new file starts with the version dsh reads.
    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        if let Document::Yaml(yaml) = document {
            if yaml.value().is_null() {
                yaml.set(&[], "version", &Json::from(1), |_| 0)?;
            }
        }
        document.set(key, value)
    }

    /// An emptied ref list goes.
    fn tidy(&self, document: &mut Document) {
        if let Document::Yaml(yaml) = document {
            let refs = [Step::Key("refs")];
            if yaml
                .get(&refs)
                .and_then(Value::as_mapping)
                .is_some_and(|refs| refs.is_empty())
            {
                let _ = yaml.remove(&refs);
            }
        }
    }

    /// Nothing but the version is left.
    fn is_empty(&self, document: &Document) -> bool {
        match document {
            Document::Yaml(yaml) => yaml.value().as_mapping().map_or(true, |root| {
                root.iter()
                    .all(|(name, _)| name.as_str() == Some("version"))
            }),
            _ => document.is_empty(),
        }
    }
}

fn dsh_dir(home: Home) -> PathBuf {
    home.join(".dsh")
}

impl ClientSpec for DeepseekHarness {
    fn id(&self) -> &'static str {
        "deepseek_harness"
    }

    fn name(&self) -> &'static str {
        "DeepSeek Harness"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "anthropic.messages", "openai.responses"]
    }

    /// The `web` profile `dsh web` runs, and the desktop app's own.
    fn targets(&self) -> &'static [&'static str] {
        &["web", "desktop"]
    }

    fn lists_models(&self) -> bool {
        true
    }

    fn dir(&self, home: Home, target: Option<&str>) -> PathBuf {
        dsh_dir(home).join("profiles").join(target.unwrap_or("web"))
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&PatchFile, &Credentials]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![
            self.dir(home, target).join("cordis.patch.yml"),
            dsh_dir(home).join(".credentials.yaml"),
        ]
    }

    /// The routes and the token carry the connection.
    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    fn holds_connection(&self, key: &str) -> bool {
        key.starts_with(ROUTE_PREFIX) || key.starts_with(REF_PREFIX)
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let Some(Document::Yaml(yaml)) = config.documents.first() else {
            return CurrentModels::default();
        };
        let mut ids = Vec::new();
        for (_, route, ..) in ROUTES {
            let key = route_key(route);
            let Some(path) = path_of(yaml, &key) else {
                continue;
            };
            let mut models = path.clone();
            models.push(Step::Key("models"));
            for entry in yaml
                .get(&models)
                .and_then(Value::as_sequence)
                .into_iter()
                .flatten()
            {
                if let Some(id) = entry.get("id").and_then(Value::as_str) {
                    if let Some(id) = clean_model(id.into()).filter(|id| !ids.contains(id)) {
                        ids.push(id);
                    }
                }
            }
        }
        let chosen = path_of(yaml, DEFAULT_KEY)
            .and_then(|path| yaml.get(&path).cloned())
            .filter(|config| {
                config
                    .get("provider")
                    .and_then(Value::as_str)
                    .is_some_and(|provider| provider.starts_with("astrlink-"))
            })
            .and_then(|config| config.get("model")?.as_str().map(str::to_string))
            .filter(|model| ids.contains(model));
        CurrentModels {
            model: chosen.or_else(|| ids.first().cloned()),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let reference = reference(connection.target);
        let list = connection.list();
        let mut keys = Vec::new();
        for (protocol, route, api, display) in ROUTES {
            let models: Vec<Json> = list
                .iter()
                .filter(|id| connection.protocol(self, id) == protocol)
                .map(|id| model_entry(id, details))
                .collect();
            if models.is_empty() {
                continue;
            }
            let value = serde_json::json!({
                "displayName": display,
                "apiKeyEnv": reference,
                "api": api,
                "baseURL": route_url(route, connection.origin),
                "models": models,
            });
            keys.push((route_key(route), value.to_string()));
        }
        if let Some(model) = connection.model("model") {
            let protocol = connection.protocol(self, &model);
            if let Some((_, route, ..)) = ROUTES.iter().find(|(known, ..)| *known == protocol) {
                let value = serde_json::json!({ "provider": route, "model": model });
                keys.push((DEFAULT_KEY.into(), value.to_string()));
            }
        }
        keys.push((format!("{REF_PREFIX}{reference}"), connection.token.into()));
        keys
    }

    /// Unused: `synced_keys` points every route elsewhere.
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
            .filter_map(|key| {
                let route = key.strip_prefix(ROUTE_PREFIX)?;
                let mut value: Json = serde_json::from_str(&config.get(key).flatten()?).ok()?;
                value["baseURL"] = route_url(route, origin).into();
                Some((key.clone(), value.to_string()))
            })
            .collect()
    }

    /// A patch in `~/.dsh` itself overrides every profile's routes or
    /// default model.
    fn notes(
        &self,
        home: Home,
        _target: Option<&str>,
        _config: Option<&Config>,
    ) -> Vec<&'static str> {
        let overridden = std::fs::read_to_string(dsh_dir(home).join("cordis.patch.yml"))
            .ok()
            .and_then(|text| yaml_serde::from_str::<Value>(&text).ok())
            .is_some_and(|value| {
                [(MODELS_ROW, MODELS_PACKAGE), (DEFAULT_ROW, DEFAULT_PACKAGE)]
                    .iter()
                    .any(|(id, package)| {
                        row(&value, id, package)
                            .and_then(|at| value.get(at)?.get("config"))
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

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    const FACTORY: &str = "# Your patch layer for this dsh profile, applied after every bundle layer:\n# a top-level YAML array of loader patch entries (id-targeted config\n# overrides, disables, and insert lists; `!!js` expressions allowed).\n[]\n";

    fn yaml(text: &str) -> yaml_serde::Value {
        yaml_serde::from_str(text).unwrap()
    }

    fn patch(home: &Path, profile: &str) -> PathBuf {
        home.join(".dsh/profiles")
            .join(profile)
            .join("cordis.patch.yml")
    }

    fn credentials(home: &Path) -> PathBuf {
        home.join(".dsh/.credentials.yaml")
    }

    fn details() -> Catalog {
        Catalog::from([
            (
                "claude-sonnet-5".to_string(),
                ModelDetails {
                    reasoning: Some(levels(&["low", "medium", "high", "max"])),
                    context_window: Some(1_000_000),
                    max_tokens: Some(64_000),
                },
            ),
            (
                "gpt-5.5".to_string(),
                ModelDetails {
                    reasoning: Some(levels(&["none", "low", "ultra"])),
                    ..Default::default()
                },
            ),
        ])
    }

    fn protocols() -> BTreeMap<String, &'static str> {
        BTreeMap::from([
            ("claude-sonnet-5".to_string(), "anthropic.messages"),
            ("gpt-5.5".to_string(), "openai.responses"),
        ])
    }

    #[test]
    fn writes_a_route_per_protocol_into_the_factory_patch() {
        let home = unique_home("dsh");
        let local = Home::local(&home);
        let web = Place::new(DEEPSEEK_HARNESS, Some("web")).unwrap();
        let desktop = Place::new(DEEPSEEK_HARNESS, Some("desktop")).unwrap();
        fs::create_dir_all(home.join(".dsh/profiles/web")).unwrap();
        fs::write(patch(&home, "web"), FACTORY).unwrap();
        let models = vec![("model", "deepseek-v4-pro".to_string())];
        let list = strings(&["deepseek-v4-pro", "claude-sonnet-5", "gpt-5.5"]);
        let protocols = protocols();
        let chosen = Connection {
            target: Some("web"),
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        let outcome = write_with(local, web, &chosen, &details(), &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        let written = fs::read_to_string(patch(&home, "web")).unwrap();
        assert!(written.starts_with("# Your patch layer"));
        assert_eq!(
            yaml(&written),
            yaml(&format!(
                "- id: llm-pi-ai
  config:
    providers:
      astrlink-chat:
        displayName: AstrLink (OpenAI Chat)
        apiKeyEnv: ASTRLINK_WEB_API_KEY
        api: openai-completions
        baseURL: {ORIGIN}/v1
        models:
          - id: deepseek-v4-pro
      astrlink-anthropic:
        displayName: AstrLink (Anthropic)
        apiKeyEnv: ASTRLINK_WEB_API_KEY
        api: anthropic-messages
        baseURL: {ORIGIN}
        models:
          - id: claude-sonnet-5
            contextWindow: 1000000
            maxTokens: 64000
            reasoningEfforts: {{low: low, medium: medium, high: high, max: max}}
      astrlink-responses:
        displayName: AstrLink (Responses)
        apiKeyEnv: ASTRLINK_WEB_API_KEY
        api: openai-responses
        baseURL: {ORIGIN}/v1
        models:
          - id: gpt-5.5
            reasoningEfforts: {{\"off\": none, low: low}}
- id: agent-default-model
  config:
    provider: astrlink-chat
    model: deepseek-v4-pro
"
            ))
        );
        assert!(written.contains("\"off\": none"), "{written}");
        assert_eq!(
            yaml(&fs::read_to_string(credentials(&home)).unwrap()),
            yaml(&format!(
                "version: 1\nrefs:\n  ASTRLINK_WEB_API_KEY: {TOKEN}\n"
            ))
        );
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mode = fs::metadata(credentials(&home))
                .unwrap()
                .permissions()
                .mode();
            assert_eq!(mode & 0o777, 0o600);
        }
        let statuses = status_of(local, [DEEPSEEK_HARNESS], Some(ORIGIN), Some(&tokens())).unwrap();
        assert_eq!(statuses[0].state, ClientState::Configured);
        assert_eq!(
            statuses[0].models,
            CurrentModels {
                model: Some("deepseek-v4-pro".into()),
                models: Some(strings(&["deepseek-v4-pro", "claude-sonnet-5", "gpt-5.5"])),
                ..Default::default()
            }
        );
        assert!(!statuses[1].detected);

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = yaml(&fs::read_to_string(patch(&home, "web")).unwrap());
        let providers = &synced[0]["config"]["providers"];
        assert_eq!(
            providers["astrlink-chat"]["baseURL"],
            "http://127.0.0.1:9000/v1"
        );
        assert_eq!(
            providers["astrlink-anthropic"]["baseURL"],
            "http://127.0.0.1:9000"
        );
        assert_eq!(
            providers["astrlink-responses"]["baseURL"],
            "http://127.0.0.1:9000/v1"
        );

        // The desktop profile names a token of its own in the same file.
        fs::create_dir_all(home.join(".dsh/profiles/desktop")).unwrap();
        let desktop_connection = Connection {
            target: Some("desktop"),
            ..connection(&models)
        };
        write(local, desktop, &desktop_connection, &tokens(), false).unwrap();
        let shared = yaml(&fs::read_to_string(credentials(&home)).unwrap());
        assert_eq!(shared["refs"]["ASTRLINK_DESKTOP_API_KEY"], TOKEN);
        assert_eq!(shared["refs"]["ASTRLINK_WEB_API_KEY"], TOKEN);

        remove(local, web).unwrap();
        assert_eq!(fs::read_to_string(patch(&home, "web")).unwrap(), FACTORY);
        let shared = yaml(&fs::read_to_string(credentials(&home)).unwrap());
        assert!(shared["refs"].get("ASTRLINK_WEB_API_KEY").is_none());
        assert_eq!(shared["refs"]["ASTRLINK_DESKTOP_API_KEY"], TOKEN);
        // The web place created the shared file; the desktop one found it.
        remove(local, desktop).unwrap();
        assert!(!patch(&home, "desktop").exists());
        assert_eq!(
            yaml(&fs::read_to_string(credentials(&home)).unwrap()),
            yaml("version: 1")
        );
        fs::remove_dir_all(home).unwrap();
    }

    const USER_PATCH: &str =
        "# Your patch layer for this dsh profile, applied after every bundle layer:
# a top-level YAML array of loader patch entries (id-targeted config
# overrides, disables, and insert lists; `!!js` expressions allowed).

# 自建网关（手写）
- id: llm-pi-ai
  config:
    providers:
      gw:                              # 路由 id 创建后不可改名
        displayName: Gateway
        apiKeyEnv: GW_API_KEY
        api: openai-completions
        baseURL: http://127.0.0.1:9/v1
        compat: {supportsDeveloperRole: false, maxTokensField: max_tokens}
        models:
          - id: deepseek-v4-pro
            name: DeepSeek V4 Pro
            input: [text]
            reasoningEfforts: {\"off\": null, high: high, max: max}
- id: agent-default-model
  config:
    provider: gw
    model: deepseek-v4-pro
- id: tools
  config:
    mode: !!js process.env.DSH_TOOLS_MODE
";

    const USER_CREDENTIALS: &str = "version: 1
# my keys
refs:
    GW_API_KEY: sk-gw
records:
    client-connection/browser-session:
        kind: grant
";

    #[test]
    fn joins_a_hand_written_patch_and_puts_it_back() {
        let home = unique_home("dsh-merge");
        let local = Home::local(&home);
        let web = Place::new(DEEPSEEK_HARNESS, Some("web")).unwrap();
        fs::create_dir_all(home.join(".dsh/profiles/web")).unwrap();
        fs::write(patch(&home, "web"), USER_PATCH).unwrap();
        fs::write(credentials(&home), USER_CREDENTIALS).unwrap();
        let models = vec![("model", "claude-opus-5".to_string())];
        let protocols = BTreeMap::from([("claude-opus-5".to_string(), "anthropic.messages")]);
        let connection = Connection {
            target: Some("web"),
            protocols: &protocols,
            ..connection(&models)
        };
        assert_eq!(
            asked(write(local, web, &connection, &tokens(), false).unwrap()),
            Some((strings(&["agent-default-model.config"]), None))
        );
        write(local, web, &connection, &tokens(), true).unwrap();
        let written = fs::read_to_string(patch(&home, "web")).unwrap();
        for kept in [
            "# 自建网关（手写）",
            "# 路由 id 创建后不可改名",
            "!!js process.env.DSH_TOOLS_MODE",
        ] {
            assert!(written.contains(kept), "{written}");
        }
        let value = yaml(&written);
        assert_eq!(value.as_sequence().unwrap().len(), 3);
        let providers = &value[0]["config"]["providers"];
        assert_eq!(
            providers["gw"],
            yaml(USER_PATCH)[0]["config"]["providers"]["gw"]
        );
        assert_eq!(providers["astrlink-anthropic"]["baseURL"], ORIGIN);
        assert_eq!(
            value[1]["config"],
            yaml("{provider: astrlink-anthropic, model: claude-opus-5}")
        );
        let shared = fs::read_to_string(credentials(&home)).unwrap();
        assert!(shared.contains("# my keys"));
        assert_eq!(yaml(&shared)["refs"]["GW_API_KEY"], "sk-gw");
        assert_eq!(yaml(&shared)["refs"]["ASTRLINK_WEB_API_KEY"], TOKEN);
        let status =
            &status_of(local, [DEEPSEEK_HARNESS], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-opus-5"));

        remove(local, web).unwrap();
        assert_eq!(fs::read_to_string(patch(&home, "web")).unwrap(), USER_PATCH);
        assert_eq!(
            fs::read_to_string(credentials(&home)).unwrap(),
            USER_CREDENTIALS
        );
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn a_patch_dsh_cannot_read_is_left_alone() {
        let home = unique_home("dsh-invalid");
        let local = Home::local(&home);
        let web = Place::new(DEEPSEEK_HARNESS, Some("web")).unwrap();
        fs::create_dir_all(home.join(".dsh/profiles/web")).unwrap();
        let models = vec![("model", "deepseek-v4-pro".to_string())];
        for original in ["providers: {}\n", "- id: llm-pi-ai\n  config: !!js x\n"] {
            fs::write(patch(&home, "web"), original).unwrap();
            assert!(write(local, web, &connection(&models), &tokens(), false).is_err());
            assert_eq!(fs::read_to_string(patch(&home, "web")).unwrap(), original);
        }
        // A patch in `~/.dsh` itself overrides every profile.
        fs::write(patch(&home, "web"), FACTORY).unwrap();
        fs::write(
            home.join(".dsh/cordis.patch.yml"),
            "- id: agent-default-model\n  config: {provider: gw, model: x}\n",
        )
        .unwrap();
        let status =
            &status_of(local, [DEEPSEEK_HARNESS], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.notes, ["overridden"]);
        fs::remove_dir_all(home).unwrap();
    }
}
