//! Writes AstrLink's gateway connection straight into a CLI client's
//! user-level config, and keeps the base URL current when the port changes.
//!
//! Only the keys listed in `~/.astrlink/client-configs.json` belong to
//! AstrLink. Each edit is made in place, keeping the rest of the file's keys,
//! comments, and layout. Verified against Claude Code 2.1.283, Codex CLI
//! 0.159.2, and Pi 0.99.2.

use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
    time::{SystemTime, UNIX_EPOCH},
};

use jsonc_parser::{
    cst::{CstInputValue, CstNode, CstObject, CstRootNode},
    JsonValue, ParseOptions,
};
use reqwest::Url;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use toml_edit::{DocumentMut, InlineTable, Item, Table, TableLike, Value};

use crate::{
    agent_home::{Home, OwnedHome},
    control_session::astrlink_home,
    host_files::{self, WriteOptions},
    sidecar::{CoreManager, CorePhase},
};

const RECORDS_VERSION: u32 = 1;
pub(crate) const CODEX_PROVIDER: &str = "astrlink";
const CODEX_PROVIDER_PREFIX: &str = "model_providers.astrlink.";
pub(crate) const PI_PROVIDER: &str = "astrlink";
const PI_PROVIDER_PREFIX: &str = "providers.astrlink.";
/// Pi's model list for the provider, written as `[{"id": <model>}]` plus
/// the model's reasoning levels when the official catalog lists them.
const PI_MODELS_KEY: &str = "providers.astrlink.models";
/// Pi's thinking levels, weakest first. Pi treats a custom model as one that
/// cannot think unless its entry says it reasons.
const PI_THINKING_LEVELS: [&str; 7] = ["off", "minimal", "low", "medium", "high", "xhigh", "max"];
/// The model Pi starts with, which its `/model` picker can save over.
const PI_STARTUP_KEYS: [&str; 2] = ["defaultProvider", "defaultModel"];
/// Codex shows its own image generation and web search tools only for a
/// provider whose `http_headers` set this header. The gateway answers both
/// tools and strips the header before forwarding; any non-empty value works.
/// AstrLink owns this one header, and other headers in the table stay the
/// user's.
const CODEX_HEADERS_KEY: &str = "model_providers.astrlink.http_headers";
const CODEX_TOOLS_HEADER: &str = "x-openai-actor-authorization";
const CODEX_TOOLS_HEADER_VALUE: &str = "codex-imagegen";
/// Claude Code's model settings, by the `Models` field each comes from.
pub(crate) const CLAUDE_MODEL_KEYS: [(&str, &str); 5] = [
    ("model", "env.ANTHROPIC_MODEL"),
    ("haikuModel", "env.ANTHROPIC_DEFAULT_HAIKU_MODEL"),
    ("sonnetModel", "env.ANTHROPIC_DEFAULT_SONNET_MODEL"),
    ("opusModel", "env.ANTHROPIC_DEFAULT_OPUS_MODEL"),
    ("fableModel", "env.ANTHROPIC_DEFAULT_FABLE_MODEL"),
];

#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Client {
    Claude,
    Codex,
    Gemini,
    Opencode,
    Openclaw,
    Pi,
}

impl Client {
    pub fn name(self) -> &'static str {
        match self {
            Self::Claude => "claude",
            Self::Codex => "codex",
            Self::Gemini => "gemini",
            Self::Opencode => "opencode",
            Self::Openclaw => "openclaw",
            Self::Pi => "pi",
        }
    }

    /// Clients AstrLink configures itself; the rest go through CC Switch.
    const WRITABLE: [Self; 3] = [Self::Claude, Self::Codex, Self::Pi];

    fn writable(self) -> Result<Self, String> {
        if Self::WRITABLE.contains(&self) {
            Ok(self)
        } else {
            Err("this client can only be configured through CC Switch".into())
        }
    }

    fn detected(self, home: Home) -> bool {
        match self {
            Self::Claude => home.claude_detected(),
            Self::Codex => home.codex_dir().is_dir(),
            Self::Pi => home.pi_detected(),
            Self::Gemini | Self::Opencode | Self::Openclaw => false,
        }
    }

    /// The files the connection is written to, main file first.
    fn files(self) -> &'static [ConfigFile] {
        match self {
            Self::Codex => &[ConfigFile::Codex],
            Self::Pi => &[ConfigFile::PiModels, ConfigFile::PiSettings],
            _ => &[ConfigFile::Claude],
        }
    }

    fn config_paths(self, home: Home) -> Vec<PathBuf> {
        self.files().iter().map(|file| file.path(home)).collect()
    }

    /// Claude Code appends its own API path; Codex and Pi need the OpenAI
    /// `/v1` root.
    fn base_url(self, origin: &str) -> String {
        match self {
            Self::Codex | Self::Pi => format!("{origin}/v1"),
            _ => origin.to_string(),
        }
    }

    fn base_url_key(self) -> &'static str {
        match self {
            Self::Codex => "model_providers.astrlink.base_url",
            Self::Pi => "providers.astrlink.baseUrl",
            _ => "env.ANTHROPIC_BASE_URL",
        }
    }

    /// Keys that decide where requests go and how they authenticate. A model
    /// the client rewrote itself, like Codex's or Pi's `/model`, is not
    /// among them.
    fn connection_keys(self) -> &'static [&'static str] {
        match self {
            Self::Codex => &[
                "model_provider",
                "model_providers.astrlink.base_url",
                "model_providers.astrlink.wire_api",
                "model_providers.astrlink.experimental_bearer_token",
            ],
            Self::Pi => &[
                "providers.astrlink.baseUrl",
                "providers.astrlink.api",
                "providers.astrlink.apiKey",
            ],
            _ => &["env.ANTHROPIC_BASE_URL", "env.ANTHROPIC_AUTH_TOKEN"],
        }
    }

    /// Keys newer versions write that configs from released builds lack. A
    /// sync adds each one once to a config nobody changed.
    fn added_keys(self) -> &'static [(&'static str, &'static str)] {
        match self {
            Self::Codex => &[(CODEX_HEADERS_KEY, CODEX_TOOLS_HEADER_VALUE)],
            _ => &[],
        }
    }
}

/// One file a client reads its connection from.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum ConfigFile {
    Claude,
    Codex,
    /// Pi's endpoints; Pi reads it with line comments and trailing commas.
    PiModels,
    /// Pi's preferences, including the model it starts with.
    PiSettings,
}

impl ConfigFile {
    fn path(self, home: Home) -> PathBuf {
        let pi = home.pi_agent_dir();
        match self {
            Self::Claude => home.claude_dir().join("settings.json"),
            Self::Codex => home.codex_dir().join("config.toml"),
            Self::PiModels => pi.join("models.json"),
            Self::PiSettings => pi.join("settings.json"),
        }
    }

    /// Whether this file of its client holds the key.
    fn holds(self, path: &str) -> bool {
        match self {
            Self::PiModels => !PI_STARTUP_KEYS.contains(&path),
            Self::PiSettings => PI_STARTUP_KEYS.contains(&path),
            Self::Claude | Self::Codex => true,
        }
    }
}

/// Why one of a client's config files cannot be edited, by the file's
/// position in `Client::files`.
#[derive(Debug)]
pub struct FileError {
    pub file: usize,
    pub message: String,
}

impl FileError {
    fn at(self, paths: &[PathBuf]) -> String {
        format!("{} {}", paths[self.file].display(), self.message)
    }
}

#[derive(Default, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Models {
    pub model: Option<String>,
    pub haiku_model: Option<String>,
    pub sonnet_model: Option<String>,
    pub opus_model: Option<String>,
    pub fable_model: Option<String>,
    /// Pi only: the context window and output limit its entry names.
    pub context_window: Option<u64>,
    pub max_tokens: Option<u64>,
}

/// The largest limit a Pi entry takes, in tokens.
const MAX_PI_LIMIT: u64 = 100_000_000;

impl Models {
    /// The context window and output limit for Pi's entry.
    fn pi_limits(&self, client: Client) -> Result<(Option<u64>, Option<u64>), String> {
        let limits = (self.context_window, self.max_tokens);
        if client != Client::Pi {
            return match limits {
                (None, None) => Ok(limits),
                _ => Err("context and output limits apply to Pi only".into()),
            };
        }
        for (name, limit) in [("contextWindow", limits.0), ("maxTokens", limits.1)] {
            if limit.is_some_and(|limit| limit == 0 || limit > MAX_PI_LIMIT) {
                return Err(format!("invalid {name}"));
            }
        }
        Ok(limits)
    }

    /// The filled-in models, trimmed and named by their camelCase field.
    /// Claude Code takes every tier; other clients take one required model.
    pub fn fields(&self, client: Client) -> Result<Vec<(&'static str, String)>, String> {
        let mut fields = vec![("model", self.model.as_deref())];
        if client == Client::Claude {
            fields.extend([
                ("haikuModel", self.haiku_model.as_deref()),
                ("sonnetModel", self.sonnet_model.as_deref()),
                ("opusModel", self.opus_model.as_deref()),
                ("fableModel", self.fable_model.as_deref()),
            ]);
        }
        let mut values = Vec::new();
        for (key, value) in fields {
            let Some(value) = value.map(str::trim).filter(|value| !value.is_empty()) else {
                continue;
            };
            if !valid_model(value) {
                return Err(format!("invalid {key}"));
            }
            values.push((key, value.to_string()));
        }
        if client != Client::Claude && values.is_empty() {
            return Err("model is required for this client".into());
        }
        Ok(values)
    }
}

/// Whether a trimmed, non-empty model name fits in a client config.
pub(crate) fn valid_model(value: &str) -> bool {
    value.chars().count() <= 256 && !value.chars().any(char::is_control)
}

/// `http://127.0.0.1:<port>` or `http://localhost:<port>` for the gateway's
/// root inference URL; Core announces localhost only while it serves `[::1]`.
pub fn local_origin(inference_url: &str) -> Result<String, String> {
    let url = Url::parse(inference_url).map_err(|_| "invalid inference URL")?;
    if url.scheme() != "http"
        || !matches!(url.host_str(), Some("127.0.0.1" | "localhost"))
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err("invalid local inference URL".into());
    }
    Ok(url.as_str().trim_end_matches('/').to_string())
}

/// Reveals a token for the gateway session the dialog was opened against,
/// so a restart in between cannot pair the token with a stale address.
pub async fn reveal_access_token(
    manager: &CoreManager,
    token_id: &str,
    inference_url: &str,
) -> Result<String, String> {
    let changed = || "gateway session changed; reopen the dialog".to_string();
    let before = manager.snapshot();
    if before.phase != CorePhase::Ready
        || before
            .ready
            .as_ref()
            .map(|ready| ready.client_inference_url.as_str())
            != Some(inference_url)
    {
        return Err(changed());
    }
    let secret = manager.reveal_access_token(token_id).await?;
    let after = manager.snapshot();
    if after.phase != CorePhase::Ready || before.pid != after.pid || before.ready != after.ready {
        return Err(changed());
    }
    secret["access_token"]
        .as_str()
        .filter(|token| !token.is_empty())
        .map(str::to_string)
        .ok_or_else(|| "access token is unavailable".to_string())
}

/// The token AstrLink last set `client` up with in `home`, whatever the
/// config holds now.
pub fn configured_token(home: Home, client: Client) -> Result<Option<String>, String> {
    let _lock = host_files::lock();
    Ok(read_records(&home)?
        .get(client)
        .map(|record| record.token_id.clone()))
}

/// A token's list entry: its display name and non-secret hint.
pub async fn token_summary(
    manager: &CoreManager,
    token_id: &str,
) -> Result<(String, String), String> {
    let page = manager.list_access_tokens().await?;
    page["items"]
        .as_array()
        .into_iter()
        .flatten()
        .find(|item| item["id"].as_str() == Some(token_id))
        .and_then(|item| Some((item["name"].as_str()?, item["hint"].as_str()?)))
        .map(|(name, hint)| (name.to_string(), hint.to_string()))
        .ok_or_else(|| "access token not found".to_string())
}

/// The ids of the gateway's access tokens. Every AstrLink on this computer
/// writes the same client configs, and one written with a token the gateway
/// lacks belongs to another AstrLink, such as the instance `make dev` runs
/// beside an installed one. Token ids are random, so no two gateways share
/// one.
#[derive(Debug)]
pub struct Tokens(BTreeSet<String>);

impl Tokens {
    pub async fn of(manager: &CoreManager) -> Result<Self, String> {
        let page = manager.list_access_tokens().await?;
        let items = page["items"]
            .as_array()
            .ok_or_else(|| "access token list is invalid".to_string())?;
        Ok(Self(
            items
                .iter()
                .filter_map(|item| item["id"].as_str())
                .map(str::to_string)
                .collect(),
        ))
    }

    fn own(&self, record: &ClientRecord) -> bool {
        self.0.contains(&record.token_id)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum ClientState {
    NotConfigured,
    Configured,
    /// Still AstrLink's, pointing at a port the gateway no longer uses.
    Outdated,
    /// Another AstrLink's, pointing at its own address.
    Elsewhere,
    /// A connection key changed since AstrLink wrote it.
    Modified,
    /// Moved to CC Switch, and the connection is not the one AstrLink wrote.
    CcSwitch,
    /// The config file cannot be parsed.
    Invalid,
}

#[derive(Debug, Serialize)]
pub struct ClientStatus {
    pub client: Client,
    /// The WSL distribution the client runs in; `None` for the desktop user's.
    pub wsl: Option<String>,
    pub detected: bool,
    pub paths: Vec<String>,
    pub state: ClientState,
    pub token_id: Option<String>,
    /// The models a config AstrLink wrote holds now, by `Models` field.
    pub models: BTreeMap<&'static str, String>,
}

/// Client configs across the desktop user's home and WSL homes.
#[derive(Debug, Serialize)]
pub struct ClientOverview {
    pub clients: Vec<ClientStatus>,
    /// WSL distributions whose clients were not checked.
    pub wsl_unchecked: Vec<String>,
    /// Whether WSL clients reach the gateway at `localhost`.
    pub wsl_localhost: bool,
    /// Where CC Switch's import writes Claude Code and Codex.
    pub cc_switch_wsl: CcSwitchWsl,
}

/// The WSL distribution whose config CC Switch writes, by client; `None` for
/// the desktop user's.
#[derive(Debug, Serialize)]
pub struct CcSwitchWsl {
    pub claude: Option<String>,
    pub codex: Option<String>,
}

#[derive(Debug, PartialEq, Eq, Serialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub enum ApplyOutcome {
    Applied,
    /// Nothing was written: `keys` hold values AstrLink did not write, or
    /// the config connects to another AstrLink at the `gateway` base URL.
    NeedsConfirmation {
        keys: Vec<String>,
        gateway: Option<String>,
    },
}

/// What AstrLink wrote for one client. Values are kept only as SHA-256
/// fingerprints, so the file holds neither the token nor anything replaced.
#[derive(Clone, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ClientRecord {
    pub token_id: String,
    pub token_sha256: String,
    pub base_url: String,
    pub keys: BTreeMap<String, String>,
    pub created_file: bool,
    pub written_at_unix: u64,
}

#[derive(Debug, Default, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Records {
    version: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    claude: Option<ClientRecord>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    codex: Option<ClientRecord>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pi: Option<ClientRecord>,
    /// Clients the user moved to CC Switch since AstrLink last wrote them.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    cc_switch: Vec<Client>,
}

impl Records {
    fn is_empty(&self) -> bool {
        self.claude.is_none()
            && self.codex.is_none()
            && self.pi.is_none()
            && self.cc_switch.is_empty()
    }

    fn handed_over(&self, client: Client) -> bool {
        self.cc_switch.contains(&client)
    }

    fn set_handed_over(&mut self, client: Client, handed_over: bool) {
        self.cc_switch.retain(|item| *item != client);
        if handed_over {
            self.cc_switch.push(client);
        }
    }

    /// Once the user moved a client to CC Switch, a connection AstrLink did
    /// not write is CC Switch's, not a change to its own config. Without the
    /// gateway's `tokens`, another AstrLink's config reads as outdated.
    fn state(
        &self,
        client: Client,
        existing: &[Option<&str>],
        origin: Option<&str>,
        tokens: Option<&Tokens>,
    ) -> ClientState {
        match inspect(client, existing, self.get(client), origin) {
            ClientState::NotConfigured | ClientState::Modified if self.handed_over(client) => {
                ClientState::CcSwitch
            }
            ClientState::Outdated
                if tokens
                    .zip(self.get(client))
                    .is_some_and(|(tokens, record)| !tokens.own(record)) =>
            {
                ClientState::Elsewhere
            }
            state => state,
        }
    }

    fn slot(&mut self, client: Client) -> &mut Option<ClientRecord> {
        match client {
            Client::Codex => &mut self.codex,
            Client::Pi => &mut self.pi,
            _ => &mut self.claude,
        }
    }

    fn get(&self, client: Client) -> Option<&ClientRecord> {
        match client {
            Client::Claude => self.claude.as_ref(),
            Client::Codex => self.codex.as_ref(),
            Client::Pi => self.pi.as_ref(),
            _ => None,
        }
    }
}

fn records_path(home: &Path) -> PathBuf {
    astrlink_home(home).join("client-configs.json")
}

fn read_records(home: &Path) -> Result<Records, String> {
    let path = records_path(home);
    let Some(raw) = host_files::read_optional(&path)? else {
        return Ok(Records::default());
    };
    let records: Records = serde_json::from_str(&raw)
        .map_err(|_| format!("{} is not a valid record file", path.display()))?;
    if records.version != RECORDS_VERSION {
        return Err(format!("{} has an unknown version", path.display()));
    }
    Ok(records)
}

fn write_records(home: &Path, records: &mut Records) -> Result<(), String> {
    let path = records_path(home);
    if records.is_empty() {
        return host_files::remove_path(&path);
    }
    records.version = RECORDS_VERSION;
    let mut encoded = serde_json::to_string_pretty(records)
        .map_err(|error| format!("unable to encode client records: {error}"))?;
    encoded.push('\n');
    host_files::write_text(
        &path,
        &encoded,
        WriteOptions {
            secret: true,
            ..Default::default()
        },
    )
}

fn sha256_hex(value: &str) -> String {
    format!("{:x}", Sha256::digest(value.as_bytes()))
}

fn unix_now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs())
        .unwrap_or(0)
}

/// A client config file opened for in-place editing. Keys are dotted paths
/// from the file's root; `None` in a lookup result is a non-string value.
enum Document {
    Json {
        file: ConfigFile,
        root: CstRootNode,
        object: CstObject,
    },
    Codex {
        document: DocumentMut,
        crlf: bool,
    },
}

/// Splits a JSON key into the objects holding it and its property name:
/// `env.ANTHROPIC_MODEL`, `providers.astrlink.baseUrl`, or a root key.
fn json_key(path: &str) -> (&'static [&'static str], &str) {
    if let Some(name) = path.strip_prefix("env.") {
        (&["env"], name)
    } else if let Some(name) = path.strip_prefix(PI_PROVIDER_PREFIX) {
        (&["providers", PI_PROVIDER], name)
    } else if let Some(name) = path.strip_prefix("providers.") {
        (&["providers"], name)
    } else {
        (&[], path)
    }
}

fn json_parent(object: &CstObject, parents: &[&str]) -> Option<CstObject> {
    parents
        .iter()
        .try_fold(object.clone(), |parent, name| parent.object_value(name))
}

/// The one entry of a Pi model list AstrLink wrote: the model, and the
/// reasoning flag and thinking level map AstrLink adds. A list with more
/// entries or other fields is not one AstrLink wrote.
#[derive(Debug, PartialEq, Eq)]
struct PiModel {
    id: String,
    reasoning: Option<bool>,
    /// Pi's thinking levels in `PI_THINKING_LEVELS` order, each with the
    /// effort it sends or `None` where the model lacks the level.
    thinking: Option<Vec<(&'static str, Option<String>)>>,
    context_window: Option<u64>,
    max_tokens: Option<u64>,
}

/// What AstrLink writes into Pi's entry for the model besides its id.
#[derive(Default)]
struct PiDetails {
    /// The official reasoning levels; `None` when the catalog lists no
    /// reasoning for the model. A model with no levels to choose keeps Pi's
    /// default range.
    reasoning: Option<Vec<String>>,
    context_window: Option<u64>,
    max_tokens: Option<u64>,
}

impl PiModel {
    fn new(id: &str, details: &PiDetails) -> Self {
        let mut model = Self::from_id(id);
        model.context_window = details.context_window;
        model.max_tokens = details.max_tokens;
        let Some(levels) = &details.reasoning else {
            return model;
        };
        model.reasoning = Some(true);
        model.thinking = (!levels.is_empty()).then(|| {
            PI_THINKING_LEVELS
                .iter()
                .map(|level| {
                    let effort = if *level == "off" { "none" } else { level };
                    (
                        *level,
                        levels
                            .iter()
                            .any(|known| known == effort)
                            .then(|| effort.to_string()),
                    )
                })
                .collect()
        });
        model
    }

    fn from_id(id: &str) -> Self {
        Self {
            id: id.to_string(),
            reasoning: None,
            thinking: None,
            context_window: None,
            max_tokens: None,
        }
    }

    /// An entry with only the fields AstrLink writes.
    fn from_json(value: &serde_json::Value) -> Option<Self> {
        let entry = value.as_object()?;
        let mut model = Self::from_id(entry.get("id")?.as_str()?);
        for (key, value) in entry {
            match key.as_str() {
                "id" => {}
                "reasoning" => model.reasoning = Some(value.as_bool()?),
                "thinkingLevelMap" => {
                    let map = value.as_object()?;
                    if map
                        .keys()
                        .any(|level| !PI_THINKING_LEVELS.contains(&level.as_str()))
                    {
                        return None;
                    }
                    let mut thinking = Vec::new();
                    for level in PI_THINKING_LEVELS {
                        let Some(effort) = map.get(level) else {
                            continue;
                        };
                        let effort = match effort {
                            serde_json::Value::Null => None,
                            effort => Some(effort.as_str()?.to_string()),
                        };
                        thinking.push((level, effort));
                    }
                    model.thinking = Some(thinking);
                }
                "contextWindow" => model.context_window = Some(value.as_u64()?),
                "maxTokens" => model.max_tokens = Some(value.as_u64()?),
                _ => return None,
            }
        }
        Some(model)
    }

    /// The entry a list holding exactly one names.
    fn read(list: &CstNode) -> Option<Self> {
        let entries = list.as_array()?.elements();
        let [entry] = entries.as_slice() else {
            return None;
        };
        Self::from_json(&cst_json(entry)?)
    }

    /// Reads `value`.
    fn parse(value: &str) -> Self {
        serde_json::from_str(value)
            .ok()
            .and_then(|value| Self::from_json(&value))
            .unwrap_or_else(|| Self::from_id(value))
    }

    /// The key's value: the id alone for `[{"id": <model>}]`, the form
    /// configs from released builds were recorded with, else the entry as
    /// JSON with sorted keys, so reformatting the file changes nothing.
    fn value(&self) -> String {
        if self.reasoning.is_none()
            && self.thinking.is_none()
            && self.context_window.is_none()
            && self.max_tokens.is_none()
        {
            return self.id.clone();
        }
        let mut entry = serde_json::Map::new();
        entry.insert("id".into(), self.id.clone().into());
        if let Some(reasoning) = self.reasoning {
            entry.insert("reasoning".into(), reasoning.into());
        }
        if let Some(thinking) = &self.thinking {
            let map = thinking
                .iter()
                .map(|(level, effort)| ((*level).to_string(), effort.clone().into()))
                .collect();
            entry.insert("thinkingLevelMap".into(), serde_json::Value::Object(map));
        }
        if let Some(tokens) = self.context_window {
            entry.insert("contextWindow".into(), tokens.into());
        }
        if let Some(tokens) = self.max_tokens {
            entry.insert("maxTokens".into(), tokens.into());
        }
        serde_json::Value::Object(entry).to_string()
    }

    /// The list as written, with the levels in Pi's order.
    fn input(&self) -> CstInputValue {
        let mut entry = vec![("id".to_string(), CstInputValue::String(self.id.clone()))];
        if let Some(reasoning) = self.reasoning {
            entry.push(("reasoning".into(), CstInputValue::Bool(reasoning)));
        }
        if let Some(thinking) = &self.thinking {
            let map = thinking
                .iter()
                .map(|(level, effort)| {
                    let effort = match effort {
                        Some(effort) => CstInputValue::String(effort.clone()),
                        None => CstInputValue::Null,
                    };
                    ((*level).to_string(), effort)
                })
                .collect();
            entry.push(("thinkingLevelMap".into(), CstInputValue::Object(map)));
        }
        if let Some(tokens) = self.context_window {
            entry.push((
                "contextWindow".into(),
                CstInputValue::Number(tokens.to_string()),
            ));
        }
        if let Some(tokens) = self.max_tokens {
            entry.push((
                "maxTokens".into(),
                CstInputValue::Number(tokens.to_string()),
            ));
        }
        CstInputValue::Array(vec![CstInputValue::Object(entry)])
    }
}

/// The value of a node from a file Pi reads.
fn cst_json(node: &CstNode) -> Option<serde_json::Value> {
    if let Some(object) = node.as_object() {
        let mut map = serde_json::Map::new();
        for property in object.properties() {
            map.insert(property.decoded_name()?, cst_json(&property.value()?)?);
        }
        return Some(serde_json::Value::Object(map));
    }
    if let Some(array) = node.as_array() {
        return array
            .elements()
            .iter()
            .map(cst_json)
            .collect::<Option<_>>()
            .map(serde_json::Value::Array);
    }
    if let Some(text) = node.as_string_lit() {
        return text.decoded_value().ok().map(serde_json::Value::String);
    }
    if let Some(boolean) = node.as_boolean_lit() {
        return Some(serde_json::Value::Bool(boolean.value()));
    }
    if let Some(number) = node.as_number_lit() {
        return serde_json::from_str::<serde_json::Number>(&number.to_string())
            .ok()
            .map(serde_json::Value::Number);
    }
    node.as_null_keyword().map(|_| serde_json::Value::Null)
}

/// Rejects what the client itself cannot read. Claude Code and Pi's
/// settings take strict JSON; Pi strips line comments and trailing commas
/// from models.json first.
fn validate_json(file: ConfigFile, text: &str) -> Result<(), String> {
    if file == ConfigFile::PiModels {
        let options = ParseOptions {
            allow_comments: true,
            allow_trailing_commas: true,
            allow_loose_object_property_names: false,
            allow_missing_commas: false,
            allow_single_quoted_strings: false,
            allow_hexadecimal_numbers: false,
            allow_unary_plus_numbers: false,
            allow_bare_decimal_point_numbers: false,
            allow_non_finite_numbers: false,
            allow_extended_string_escapes: false,
        };
        let value = jsonc_parser::parse_to_value(text, &options)
            .map_err(|error| format!("is not valid JSON ({error})"))?;
        let Some(JsonValue::Object(root)) = value else {
            return Err("is not a JSON object".into());
        };
        if root
            .get("providers")
            .is_some_and(|providers| !matches!(providers, JsonValue::Object(_)))
        {
            return Err("has a providers value that is not an object".into());
        }
        return Ok(());
    }
    let value: serde_json::Value = serde_json::from_str(text).map_err(|error| {
        format!(
            "is not valid JSON (line {}, column {})",
            error.line(),
            error.column()
        )
    })?;
    if !value.is_object() {
        return Err("is not a JSON object".into());
    }
    if file == ConfigFile::Claude && value.get("env").is_some_and(|env| !env.is_object()) {
        return Err("has an env value that is not an object".into());
    }
    Ok(())
}

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

fn toml_string(item: &Item) -> Option<String> {
    item.as_str().map(str::to_string)
}

/// Replaces a string in place, keeping the comment and spacing around it.
fn set_toml_string(table: &mut dyn TableLike, key: &str, value: &str) {
    match table.get_mut(key) {
        Some(Item::Value(current)) => {
            let decor = current.decor().clone();
            *current = Value::from(value);
            *current.decor_mut() = decor;
        }
        Some(item) => *item = toml_edit::value(value),
        None => {
            table.insert(key, toml_edit::value(value));
        }
    }
}

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

/// Removes a root key but keeps the comment above it, handing it to the
/// neighbouring key: a file's header note often sits above `model`.
fn remove_toml_root_key(document: &mut DocumentMut, key: &str) {
    let root = document.as_table_mut();
    let Some(index) = root.iter().position(|(name, _)| name == key) else {
        return;
    };
    let comment = root
        .key(key)
        .and_then(|key| key.leaf_decor().prefix())
        .and_then(|prefix| prefix.as_str())
        .filter(|prefix| prefix.contains('#'))
        .map(str::to_string);
    root.remove(key);
    let Some(comment) = comment else {
        return;
    };
    let values = root
        .iter()
        .enumerate()
        .filter(|(_, (_, item))| item.is_value())
        .map(|(position, (name, _))| (position, name.to_string()))
        .collect::<Vec<_>>();
    let raw = |text: Option<&toml_edit::RawString>| {
        text.and_then(|text| text.as_str())
            .unwrap_or_default()
            .to_string()
    };
    if let Some((_, next)) = values.iter().find(|(position, _)| *position >= index) {
        if let Some(mut key) = root.key_mut(next) {
            let decor = key.leaf_decor_mut();
            let rest = raw(decor.prefix());
            decor.set_prefix(format!("{comment}{rest}"));
        }
    } else if let Some((_, previous)) = values.last() {
        if let Some(value) = root.get_mut(previous).and_then(Item::as_value_mut) {
            let decor = value.decor_mut();
            let suffix = raw(decor.suffix());
            decor.set_suffix(format!("{suffix}\n{}", comment.trim_end_matches('\n')));
        }
    } else {
        let decor = root.decor_mut();
        let rest = raw(decor.prefix());
        decor.set_prefix(format!("{comment}{rest}"));
    }
}

impl Document {
    fn parse(file: ConfigFile, existing: Option<&str>) -> Result<Self, String> {
        let text = existing.unwrap_or_default();
        if file == ConfigFile::Codex {
            return Ok(Self::Codex {
                document: text.parse().map_err(|_| "is not valid TOML".to_string())?,
                crlf: text.contains("\r\n"),
            });
        }
        // The CST parser takes more than the clients do, so validate first.
        if !text.trim().is_empty() {
            validate_json(file, text)?;
        }
        let root = CstRootNode::parse(text, &ParseOptions::default())
            .map_err(|_| "is not valid JSON".to_string())?;
        let object = root.object_value_or_set();
        Ok(Self::Json { file, root, object })
    }

    fn get(&self, path: &str) -> Option<Option<String>> {
        match self {
            Self::Json { object, .. } => {
                let (parents, name) = json_key(path);
                let value = json_parent(object, parents)?.get(name)?.value();
                Some(if path == PI_MODELS_KEY {
                    value
                        .as_ref()
                        .and_then(PiModel::read)
                        .map(|model| model.value())
                } else {
                    value
                        .and_then(|value| value.as_string_lit())
                        .and_then(|value| value.decoded_value().ok())
                })
            }
            Self::Codex { document, .. } => {
                let Some(key) = path.strip_prefix(CODEX_PROVIDER_PREFIX) else {
                    return document.get(path).map(toml_string);
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
        }
    }

    /// Every key in the group AstrLink takes over, with its current value.
    fn group(&self) -> Vec<(String, Option<String>)> {
        let mut keys = Vec::new();
        match self {
            Self::Json {
                file: ConfigFile::Claude,
                object,
                ..
            } => {
                if let Some(env) = object.object_value("env") {
                    for property in env.properties() {
                        let Some(name) = property.decoded_name() else {
                            continue;
                        };
                        if claude_group_env(&name) {
                            let path = format!("env.{name}");
                            let value = self.get(&path).flatten();
                            keys.push((path, value));
                        }
                    }
                }
                if object.get("apiKeyHelper").is_some() {
                    keys.push(("apiKeyHelper".into(), self.get("apiKeyHelper").flatten()));
                }
            }
            Self::Json {
                file: ConfigFile::PiModels,
                object,
                ..
            } => {
                let provider = object
                    .object_value("providers")
                    .and_then(|providers| providers.get(PI_PROVIDER));
                match provider.map(|provider| provider.object_value()) {
                    Some(Some(provider)) => {
                        for property in provider.properties() {
                            let Some(name) = property.decoded_name() else {
                                continue;
                            };
                            let path = format!("{PI_PROVIDER_PREFIX}{name}");
                            let value = self.get(&path).flatten();
                            keys.push((path, value));
                        }
                    }
                    Some(None) => keys.push(("providers.astrlink".into(), None)),
                    None => {}
                }
            }
            // Pi's settings: only the startup model is AstrLink's to take over.
            Self::Json { object, .. } => {
                for key in PI_STARTUP_KEYS {
                    if object.get(key).is_some() {
                        keys.push((key.into(), self.get(key).flatten()));
                    }
                }
            }
            Self::Codex { document, .. } => {
                for key in ["model_provider", "model"] {
                    if let Some(item) = document.get(key) {
                        keys.push((key.into(), toml_string(item)));
                    }
                }
                let provider = document
                    .get("model_providers")
                    .and_then(Item::as_table_like)
                    .and_then(|providers| providers.get(CODEX_PROVIDER));
                match provider.map(|item| item.as_table_like()) {
                    Some(Some(table)) => {
                        for (key, _) in table.iter() {
                            let path = format!("{CODEX_PROVIDER_PREFIX}{key}");
                            // Headers without AstrLink's own are the user's.
                            if let Some(value) = self.get(&path) {
                                keys.push((path, value));
                            }
                        }
                    }
                    Some(None) => keys.push(("model_providers.astrlink".into(), None)),
                    None => {}
                }
            }
        }
        keys
    }

    fn set(&mut self, path: &str, value: &str) -> Result<(), String> {
        match self {
            Self::Json { object, .. } => {
                let (parents, name) = json_key(path);
                let mut parent = object.clone();
                for segment in parents {
                    parent = parent
                        .object_value_or_create(segment)
                        .ok_or_else(|| format!("has a {segment} value that is not an object"))?;
                }
                let value = if path == PI_MODELS_KEY {
                    PiModel::parse(value).input()
                } else {
                    CstInputValue::String(value.into())
                };
                match parent.get(name) {
                    Some(property) => property.set_value(value),
                    None => {
                        parent.append(name, value);
                    }
                }
            }
            Self::Codex { document, .. } => {
                let Some(key) = path.strip_prefix(CODEX_PROVIDER_PREFIX) else {
                    set_toml_string(document.as_table_mut(), path, value);
                    return Ok(());
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
            }
        }
        Ok(())
    }

    fn remove(&mut self, path: &str) {
        match self {
            Self::Json { object, .. } => {
                let (parents, name) = json_key(path);
                if let Some(property) =
                    json_parent(object, parents).and_then(|parent| parent.get(name))
                {
                    property.remove();
                }
            }
            Self::Codex { document, .. } => {
                let providers = document
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
                    remove_toml_root_key(document, path);
                }
            }
        }
    }

    /// Drops the containers a removal left empty.
    fn tidy(&mut self) {
        match self {
            Self::Json { file, object, .. } => {
                let container = match file {
                    ConfigFile::Claude => "env",
                    ConfigFile::PiModels => "providers.astrlink",
                    _ => return,
                };
                let (parents, name) = json_key(container);
                if let Some(property) =
                    json_parent(object, parents).and_then(|parent| parent.get(name))
                {
                    if property
                        .object_value()
                        .is_some_and(|value| value.properties().is_empty())
                    {
                        property.remove();
                    }
                }
            }
            Self::Codex { document, .. } => {
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
    }

    fn is_empty(&self) -> bool {
        match self {
            // Pi requires `providers`, so an emptied one stays until the file goes.
            Self::Json { file, object, .. } => object.properties().iter().all(|property| {
                *file == ConfigFile::PiModels
                    && property.decoded_name().as_deref() == Some("providers")
                    && property
                        .object_value()
                        .is_some_and(|providers| providers.properties().is_empty())
            }),
            Self::Codex { document, .. } => document.to_string().trim().is_empty(),
        }
    }

    fn render(&self) -> String {
        match self {
            Self::Json { root, .. } => {
                let mut text = root.to_string();
                if !text.ends_with('\n') {
                    text.push('\n');
                }
                text
            }
            // toml_edit writes LF; keep a CRLF file CRLF.
            Self::Codex { document, crlf } => {
                let text = document.to_string();
                if *crlf {
                    text.replace("\r\n", "\n").replace('\n', "\r\n")
                } else {
                    text
                }
            }
        }
    }
}

/// Claude Code's settings or Pi's models.json as the client reads them, for
/// importing the providers a file configures. Nothing here writes.
fn read_json_config(file: ConfigFile, text: &str) -> Result<serde_json::Value, String> {
    if text.trim().is_empty() {
        return Ok(serde_json::Value::Object(serde_json::Map::new()));
    }
    let Document::Json { root, .. } = Document::parse(file, Some(text))? else {
        return Err("is not valid JSON".into());
    };
    root.value()
        .as_ref()
        .and_then(cst_json)
        .ok_or_else(|| "is not valid JSON".to_string())
}

pub(crate) fn read_claude_settings(text: &str) -> Result<serde_json::Value, String> {
    read_json_config(ConfigFile::Claude, text)
}

pub(crate) fn read_pi_models(text: &str) -> Result<serde_json::Value, String> {
    read_json_config(ConfigFile::PiModels, text)
}

/// Codex's config.toml as Codex reads it, for importing its providers.
pub(crate) fn read_codex_config(text: &str) -> Result<DocumentMut, String> {
    match Document::parse(ConfigFile::Codex, Some(text))? {
        Document::Codex { document, .. } => Ok(document),
        Document::Json { .. } => Err("is not valid TOML".into()),
    }
}

/// Every config file of one client, opened together. Each key lives in
/// exactly one of them.
struct Config {
    files: &'static [ConfigFile],
    documents: Vec<Document>,
    changed: Vec<bool>,
}

impl Config {
    fn parse(client: Client, existing: &[Option<&str>]) -> Result<Self, FileError> {
        let files = client.files();
        if existing.len() != files.len() {
            return Err(FileError {
                file: 0,
                message: "was read without its companion files".into(),
            });
        }
        let documents = files
            .iter()
            .zip(existing)
            .enumerate()
            .map(|(index, (file, text))| {
                Document::parse(*file, *text).map_err(|message| FileError {
                    file: index,
                    message,
                })
            })
            .collect::<Result<_, _>>()?;
        Ok(Self {
            files,
            documents,
            changed: vec![false; files.len()],
        })
    }

    fn index(&self, path: &str) -> usize {
        self.files
            .iter()
            .position(|file| file.holds(path))
            .unwrap_or(0)
    }

    fn get(&self, path: &str) -> Option<Option<String>> {
        self.documents[self.index(path)].get(path)
    }

    fn group(&self) -> Vec<(String, Option<String>)> {
        self.documents.iter().flat_map(Document::group).collect()
    }

    fn set(&mut self, path: &str, value: &str) -> Result<(), FileError> {
        let file = self.index(path);
        self.changed[file] = true;
        self.documents[file]
            .set(path, value)
            .map_err(|message| FileError { file, message })
    }

    fn remove(&mut self, path: &str) {
        let file = self.index(path);
        self.changed[file] = true;
        self.documents[file].remove(path);
    }

    /// New contents per file; a file left alone keeps its text, and a
    /// missing one stays empty.
    fn render(&self, existing: &[Option<&str>]) -> Vec<String> {
        self.documents
            .iter()
            .zip(&self.changed)
            .zip(existing)
            .map(|((document, changed), text)| {
                if *changed {
                    document.render()
                } else {
                    text.unwrap_or_default().to_string()
                }
            })
            .collect()
    }
}

pub struct Connection<'a> {
    pub token_id: &'a str,
    pub token: &'a str,
    pub origin: &'a str,
    pub models: &'a [(&'static str, String)],
}

/// `pi` is what Pi's entry for the model says besides its id.
fn desired_keys(client: Client, connection: &Connection, pi: &PiDetails) -> Vec<(String, String)> {
    let base_url = client.base_url(connection.origin);
    let model = |field: &str| {
        connection
            .models
            .iter()
            .find(|(key, _)| *key == field)
            .map(|(_, value)| value.clone())
    };
    let mut keys = Vec::new();
    match client {
        Client::Codex => {
            keys.push(("model_provider".into(), CODEX_PROVIDER.into()));
            if let Some(model) = model("model") {
                keys.push(("model".into(), model));
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
        }
        Client::Pi => {
            // The display name is local UI in Pi's model picker.
            for (key, value) in [
                ("name", "AstrLink"),
                ("baseUrl", base_url.as_str()),
                ("api", "openai-responses"),
                ("apiKey", connection.token),
            ] {
                keys.push((format!("{PI_PROVIDER_PREFIX}{key}"), value.into()));
            }
            if let Some(model) = model("model") {
                let entry = PiModel::new(&model, pi);
                keys.push((PI_MODELS_KEY.into(), entry.value()));
                keys.push(("defaultProvider".into(), PI_PROVIDER.into()));
                keys.push(("defaultModel".into(), model));
            }
        }
        _ => {
            keys.push(("env.ANTHROPIC_BASE_URL".into(), base_url));
            // AUTH_TOKEN, not API_KEY: an API key asks for approval in the
            // interactive client and conflicts with a login.
            keys.push(("env.ANTHROPIC_AUTH_TOKEN".into(), connection.token.into()));
            for (field, path) in CLAUDE_MODEL_KEYS {
                if let Some(model) = model(field) {
                    keys.push((path.into(), model));
                }
            }
        }
    }
    keys
}

fn written_by_us(record: Option<&ClientRecord>, path: &str, value: Option<&str>) -> bool {
    match (record.and_then(|record| record.keys.get(path)), value) {
        (Some(fingerprint), Some(value)) => *fingerprint == sha256_hex(value),
        _ => false,
    }
}

#[derive(Debug)]
pub struct WritePlan {
    /// New contents for each of the client's files, in `Client::files`
    /// order; a file the plan leaves alone keeps its text.
    pub contents: Vec<String>,
    /// Keys holding values AstrLink did not write, which the plan replaces
    /// or removes. Names only; values never leave the file.
    pub conflicts: Vec<String>,
    pub record: ClientRecord,
}

/// `existing` holds each of the client's files, in `Client::files` order.
pub fn plan_write(
    client: Client,
    existing: &[Option<&str>],
    connection: &Connection,
    previous: Option<&ClientRecord>,
) -> Result<WritePlan, FileError> {
    plan_write_with(
        client,
        existing,
        connection,
        &PiDetails::default(),
        previous,
    )
}

/// `plan_write` with what Pi's entry for the model says besides its id.
fn plan_write_with(
    client: Client,
    existing: &[Option<&str>],
    connection: &Connection,
    pi: &PiDetails,
    previous: Option<&ClientRecord>,
) -> Result<WritePlan, FileError> {
    let mut config = Config::parse(client, existing)?;
    let desired = desired_keys(client, connection, pi);
    let mut conflicts = Vec::new();
    for (path, value) in config.group() {
        let wanted = desired.iter().find(|(key, _)| *key == path);
        if wanted.is_some_and(|(_, wanted)| Some(wanted) == value.as_ref()) {
            continue;
        }
        if !written_by_us(previous, &path, value.as_deref()) {
            conflicts.push(path.clone());
        }
        if wanted.is_none() {
            config.remove(&path);
        }
    }
    for (path, value) in &desired {
        config.set(path, value)?;
    }
    Ok(WritePlan {
        contents: config.render(existing),
        conflicts,
        record: ClientRecord {
            token_id: connection.token_id.to_string(),
            token_sha256: sha256_hex(connection.token),
            base_url: client.base_url(connection.origin),
            keys: desired
                .iter()
                .map(|(path, value)| (path.clone(), sha256_hex(value)))
                .collect(),
            created_file: existing[0].is_none()
                || previous.is_some_and(|record| record.created_file),
            written_at_unix: unix_now(),
        },
    })
}

#[derive(Debug, PartialEq, Eq)]
pub enum Removal {
    Unchanged,
    Write(String),
    Delete,
}

/// One removal per file, in `Client::files` order.
pub fn plan_remove(
    client: Client,
    existing: &[Option<&str>],
    record: &ClientRecord,
) -> Result<Vec<Removal>, FileError> {
    let mut config = Config::parse(client, existing)?;
    for path in record.keys.keys() {
        let value = config.get(path).flatten();
        if written_by_us(Some(record), path, value.as_deref()) {
            config.remove(path);
        }
    }
    Ok(config
        .documents
        .iter_mut()
        .zip(&config.changed)
        .enumerate()
        .map(|(index, (document, changed))| {
            if !changed {
                return Removal::Unchanged;
            }
            document.tidy();
            // `created_file` is about the main file; companions stay.
            if index == 0 && record.created_file && document.is_empty() {
                Removal::Delete
            } else {
                Removal::Write(document.render())
            }
        })
        .collect())
}

pub fn inspect(
    client: Client,
    existing: &[Option<&str>],
    record: Option<&ClientRecord>,
    origin: Option<&str>,
) -> ClientState {
    let Ok(config) = Config::parse(client, existing) else {
        return ClientState::Invalid;
    };
    let Some(record) = record else {
        return ClientState::NotConfigured;
    };
    if client.connection_keys().iter().any(|path| {
        let value = config.get(path).flatten();
        !written_by_us(Some(record), path, value.as_deref())
    }) {
        return ClientState::Modified;
    }
    match origin {
        Some(origin) if record.base_url != client.base_url(origin) => ClientState::Outdated,
        _ => ClientState::Configured,
    }
}

/// The models a client's config holds now, by the `Models` field each would
/// be written from. The client may have picked them itself since, like with
/// Codex's `/model`. Names AstrLink could not write back are left out.
fn current_models(client: Client, existing: &[Option<&str>]) -> BTreeMap<&'static str, String> {
    let Ok(config) = Config::parse(client, existing) else {
        return BTreeMap::new();
    };
    let read = |path: &str| {
        config
            .get(path)
            .flatten()
            .map(|value| {
                if path == PI_MODELS_KEY {
                    PiModel::parse(&value).id
                } else {
                    value
                }
            })
            .map(|value| value.trim().to_string())
            .filter(|value| !value.is_empty() && valid_model(value))
    };
    let models = match client {
        Client::Codex => vec![("model", read("model"))],
        // The model Pi starts with on AstrLink, else the one AstrLink's list
        // offers.
        Client::Pi => {
            let starts_here =
                config.get("defaultProvider").flatten().as_deref() == Some(PI_PROVIDER);
            let startup = starts_here.then(|| read("defaultModel")).flatten();
            let entry = config
                .get(PI_MODELS_KEY)
                .flatten()
                .map(|value| PiModel::parse(&value));
            let limit = |tokens: Option<u64>| {
                tokens
                    .filter(|tokens| (1..=MAX_PI_LIMIT).contains(tokens))
                    .map(|tokens| tokens.to_string())
            };
            vec![
                ("model", startup.or_else(|| read(PI_MODELS_KEY))),
                (
                    "contextWindow",
                    entry.as_ref().and_then(|entry| limit(entry.context_window)),
                ),
                (
                    "maxTokens",
                    entry.as_ref().and_then(|entry| limit(entry.max_tokens)),
                ),
            ]
        }
        _ => CLAUDE_MODEL_KEYS
            .iter()
            .map(|(field, path)| (*field, read(path)))
            .collect(),
    };
    models
        .into_iter()
        .filter_map(|(field, model)| Some((field, model?)))
        .collect()
}

/// Points an untouched config at the gateway's new port, and adds the keys
/// a released build did not write yet. The token stays as written, so the
/// sync never needs to reveal it.
pub fn plan_sync(
    client: Client,
    existing: &[Option<&str>],
    record: &ClientRecord,
    origin: &str,
) -> Result<Option<(Vec<String>, ClientRecord)>, FileError> {
    let state = inspect(client, existing, Some(record), Some(origin));
    if !matches!(state, ClientState::Outdated | ClientState::Configured) {
        return Ok(None);
    }
    let mut config = Config::parse(client, existing)?;
    let mut next = record.clone();
    let mut changed = false;
    if state == ClientState::Outdated {
        let base_url = client.base_url(origin);
        config.set(client.base_url_key(), &base_url)?;
        next.keys
            .insert(client.base_url_key().into(), sha256_hex(&base_url));
        next.base_url = base_url;
        changed = true;
    }
    for (path, value) in client.added_keys() {
        // A key AstrLink wrote before and the user removed stays removed, and
        // a value the user set stays theirs.
        if record.keys.contains_key(*path) || config.get(path).is_some() {
            continue;
        }
        config.set(path, value)?;
        next.keys.insert((*path).into(), sha256_hex(value));
        changed = true;
    }
    if !changed {
        return Ok(None);
    }
    next.written_at_unix = unix_now();
    Ok(Some((config.render(existing), next)))
}

/// The keys `apply` writes, as a standalone file for manual setup.
pub fn snippet(client: Client, connection: &Connection) -> Result<String, String> {
    let client = client.writable()?;
    // Pi's setup spans two files, which one snippet cannot show.
    if client.files().len() != 1 {
        return Err("this client's config spans several files".into());
    }
    let mut plan = plan_write(client, &[None], connection, None).map_err(|error| error.message)?;
    Ok(plan.contents.remove(0))
}

fn read_files(paths: &[PathBuf]) -> Result<Vec<Option<String>>, String> {
    paths
        .iter()
        .map(|path| host_files::read_optional(path))
        .collect()
}

fn texts(files: &[Option<String>]) -> Vec<Option<&str>> {
    files.iter().map(Option::as_deref).collect()
}

/// Writes each file whose contents changed. When one fails, the files
/// already written get their old contents back.
fn write_files(
    paths: &[PathBuf],
    existing: &[Option<String>],
    contents: &[String],
) -> Result<(), String> {
    let mut written: Vec<(&Path, Option<&str>, &str)> = Vec::new();
    for ((path, before), after) in paths.iter().zip(existing).zip(contents) {
        let before = before.as_deref();
        if before.unwrap_or_default() == after {
            continue;
        }
        let result = host_files::write_text(
            path,
            after,
            WriteOptions {
                secret: true,
                expected: Some(before),
            },
        );
        if let Err(error) = result {
            for (path, before, after) in written.into_iter().rev() {
                let _ = match before {
                    Some(before) => host_files::write_text(
                        path,
                        before,
                        WriteOptions {
                            secret: true,
                            expected: Some(Some(after)),
                        },
                    ),
                    None if host_files::read_optional(path).ok().flatten().as_deref()
                        == Some(after) =>
                    {
                        host_files::remove_path(path)
                    }
                    None => Ok(()),
                };
            }
            return Err(error);
        }
        written.push((path, before, after));
    }
    Ok(())
}

pub fn status(
    home: Home,
    inference_url: Option<&str>,
    tokens: Option<&Tokens>,
) -> Result<Vec<ClientStatus>, String> {
    let origin = inference_url.map(local_origin).transpose()?;
    let _lock = host_files::lock();
    let records = read_records(&home)?;
    let mut statuses = Vec::new();
    for client in Client::WRITABLE {
        let paths = client.config_paths(home);
        let existing = read_files(&paths)?;
        let record = records.get(client);
        let state = records.state(client, &texts(&existing), origin.as_deref(), tokens);
        // Only a config AstrLink wrote names its token and models.
        let token_id = record
            .filter(|_| !matches!(state, ClientState::NotConfigured | ClientState::CcSwitch))
            .map(|record| record.token_id.clone());
        statuses.push(ClientStatus {
            client,
            wsl: home.distribution().map(str::to_string),
            detected: client.detected(home),
            paths: paths
                .iter()
                .map(|path| path.display().to_string())
                .collect(),
            state,
            models: if token_id.is_some() {
                current_models(client, &texts(&existing))
            } else {
                BTreeMap::new()
            },
            token_id,
        });
    }
    Ok(statuses)
}

pub async fn apply(
    manager: &CoreManager,
    target: OwnedHome,
    token_id: String,
    client: Client,
    models: &Models,
    inference_url: String,
    replace: bool,
) -> Result<ApplyOutcome, String> {
    let client = client.writable()?;
    let origin = local_origin(&inference_url)?;
    let (context_window, max_tokens) = models.pi_limits(client)?;
    let models = models.fields(client)?;
    if !client.detected(target.home()) {
        return Err("this client is not installed for the current user".into());
    }
    let token = reveal_access_token(manager, &token_id, &inference_url).await?;
    let tokens = Tokens::of(manager).await?;
    // Pi offers thinking levels only for a model its entry says reasons.
    // Without the levels the model is still written, without thinking.
    let reasoning = match models.iter().find(|(field, _)| *field == "model") {
        Some((_, model)) if client == Client::Pi => manager
            .official_reasoning(model)
            .await
            .unwrap_or_else(|error| {
                eprintln!("reasoning levels for {model} are unavailable: {error}");
                None
            }),
        _ => None,
    };
    let pi = PiDetails {
        reasoning,
        context_window,
        max_tokens,
    };
    tauri::async_runtime::spawn_blocking(move || {
        write_with(
            target.home(),
            client,
            &Connection {
                token_id: &token_id,
                token: &token,
                origin: &origin,
                models: &models,
            },
            &pi,
            &tokens,
            replace,
        )
    })
    .await
    .map_err(|error| error.to_string())?
}

/// `write_with` for a model the official catalog lists no reasoning for.
#[cfg(test)]
fn write(
    home: Home,
    client: Client,
    connection: &Connection,
    tokens: &Tokens,
    replace: bool,
) -> Result<ApplyOutcome, String> {
    write_with(
        home,
        client,
        connection,
        &PiDetails::default(),
        tokens,
        replace,
    )
}

/// `tokens` are the gateway's, so a config another AstrLink wrote moves here
/// only once the user confirms. `pi` is what Pi's entry for the model says
/// besides its id.
fn write_with(
    home: Home,
    client: Client,
    connection: &Connection,
    pi: &PiDetails,
    tokens: &Tokens,
    replace: bool,
) -> Result<ApplyOutcome, String> {
    let client = client.writable()?;
    let _lock = host_files::lock();
    let mut records = read_records(&home)?;
    let paths = client.config_paths(home);
    let existing = read_files(&paths)?;
    let plan = plan_write_with(
        client,
        &texts(&existing),
        connection,
        pi,
        records.get(client),
    )
    .map_err(|error| error.at(&paths))?;
    let gateway = records
        .get(client)
        .filter(|_| {
            records.state(
                client,
                &texts(&existing),
                Some(connection.origin),
                Some(tokens),
            ) == ClientState::Elsewhere
        })
        .map(|record| record.base_url.clone());
    if (!plan.conflicts.is_empty() || gateway.is_some()) && !replace {
        return Ok(ApplyOutcome::NeedsConfirmation {
            keys: plan.conflicts,
            gateway,
        });
    }
    let previous = records.slot(client).replace(plan.record);
    // Writing takes the client back from CC Switch.
    let handed_over = records.handed_over(client);
    records.set_handed_over(client, false);
    write_records(&home, &mut records)?;
    if let Err(error) = write_files(&paths, &existing, &plan.contents) {
        *records.slot(client) = previous;
        records.set_handed_over(client, handed_over);
        let _ = write_records(&home, &mut records);
        return Err(error);
    }
    Ok(ApplyOutcome::Applied)
}

pub fn remove(home: Home, client: Client) -> Result<(), String> {
    let client = client.writable()?;
    let _lock = host_files::lock();
    let mut records = read_records(&home)?;
    let Some(record) = records.get(client).cloned() else {
        if records.handed_over(client) {
            records.set_handed_over(client, false);
            write_records(&home, &mut records)?;
        }
        return Ok(());
    };
    let paths = client.config_paths(home);
    let existing = read_files(&paths)?;
    // CC Switch rewrites some keys with the values AstrLink wrote, so the
    // files of a client it holds are left to it.
    let removals = if records.state(client, &texts(&existing), None, None) == ClientState::CcSwitch
    {
        Vec::new()
    } else {
        plan_remove(client, &texts(&existing), &record).map_err(|error| error.at(&paths))?
    };
    for ((path, existing), removal) in paths.iter().zip(&existing).zip(removals) {
        match removal {
            Removal::Unchanged => {}
            Removal::Write(contents) => host_files::write_text(
                path,
                &contents,
                WriteOptions {
                    expected: Some(existing.as_deref()),
                    ..Default::default()
                },
            )?,
            Removal::Delete => {
                if host_files::read_optional(path)? != *existing {
                    return Err(format!(
                        "unable to remove {}: the file changed; try again",
                        path.display()
                    ));
                }
                host_files::remove_path(path)?;
            }
        }
    }
    *records.slot(client) = None;
    records.set_handed_over(client, false);
    write_records(&home, &mut records)
}

/// Records whether the user moved a client AstrLink writes to CC Switch, and
/// returns whether the record changed. Other clients have no record.
pub fn set_cc_switch(home: Home, client: Client, handed_over: bool) -> Result<bool, String> {
    if !Client::WRITABLE.contains(&client) {
        return Ok(false);
    }
    let _lock = host_files::lock();
    let mut records = read_records(&home)?;
    if records.handed_over(client) == handed_over {
        return Ok(false);
    }
    records.set_handed_over(client, handed_over);
    write_records(&home, &mut records)?;
    Ok(true)
}

/// Moves every untouched config the gateway wrote with one of its `tokens`
/// to its current address. Another AstrLink's configs stay pointed at it.
pub fn sync(home: Home, inference_url: &str, tokens: &Tokens) -> Result<(), String> {
    let origin = local_origin(inference_url)?;
    let _lock = host_files::lock();
    let mut records = read_records(&home)?;
    let mut errors = Vec::new();
    for client in Client::WRITABLE {
        let Some(record) = records
            .get(client)
            .filter(|record| tokens.own(record))
            .cloned()
        else {
            continue;
        };
        let paths = client.config_paths(home);
        let result = (|| {
            let existing = read_files(&paths)?;
            let Some((contents, next)) = plan_sync(client, &texts(&existing), &record, &origin)
                .map_err(|error| error.at(&paths))?
            else {
                return Ok(());
            };
            *records.slot(client) = Some(next);
            write_records(&home, &mut records)?;
            let written = write_files(&paths, &existing, &contents);
            if written.is_err() {
                *records.slot(client) = Some(record.clone());
                let _ = write_records(&home, &mut records);
            }
            written
        })();
        if let Err(error) = result {
            errors.push(error);
        }
    }
    if errors.is_empty() {
        Ok(())
    } else {
        Err(errors.join("; "))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{
        fs,
        sync::atomic::{AtomicU64, Ordering},
    };

    const TOKEN: &str = "astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0";
    const OTHER_TOKEN: &str = "astr_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB4";
    const ORIGIN: &str = "http://127.0.0.1:18317";

    fn unique_home(name: &str) -> PathBuf {
        static SEQUENCE: AtomicU64 = AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!(
            "astrlink-client-config-{name}-{}-{}",
            std::process::id(),
            SEQUENCE.fetch_add(1, Ordering::Relaxed)
        ));
        let _ = fs::remove_dir_all(&path);
        fs::create_dir_all(path.join(".claude")).unwrap();
        fs::create_dir_all(path.join(".codex")).unwrap();
        path
    }

    fn connection<'a>(models: &'a [(&'static str, String)]) -> Connection<'a> {
        Connection {
            token_id: "token_01",
            token: TOKEN,
            origin: ORIGIN,
            models,
        }
    }

    /// The tokens of the gateway `connection` writes for.
    fn tokens() -> Tokens {
        Tokens(BTreeSet::from(["token_01".to_string()]))
    }

    fn codex_models() -> Vec<(&'static str, String)> {
        vec![("model", "gpt-route".to_string())]
    }

    fn json(text: &str) -> serde_json::Value {
        serde_json::from_str(text).unwrap()
    }

    #[test]
    fn claude_settings_keep_other_keys_order_and_indent() {
        let existing = "{\n    \"theme\": \"dark\",\n    \"env\": {\n        \"EDITOR\": \"vim\"\n    },\n    \"permissions\": {\"allow\": []}\n}\n";
        let models = vec![
            ("model", "main-route".to_string()),
            ("fableModel", "fable-route".to_string()),
        ];
        let plan = plan_write(
            Client::Claude,
            &[Some(existing)],
            &connection(&models),
            None,
        )
        .unwrap();
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
        let removed =
            plan_remove(Client::Claude, &[Some(&plan.contents[0])], &plan.record).unwrap();
        assert_eq!(removed, [Removal::Write(existing.into())]);
    }

    #[test]
    fn claude_settings_are_created_and_removed_whole() {
        for existing in [None, Some("")] {
            let plan = plan_write(Client::Claude, &[existing], &connection(&[]), None).unwrap();
            assert_eq!(
                json(&plan.contents[0]),
                serde_json::json!({"env": {"ANTHROPIC_BASE_URL": ORIGIN, "ANTHROPIC_AUTH_TOKEN": TOKEN}})
            );
            assert!(plan.contents[0].ends_with("}\n"));
            assert_eq!(plan.record.created_file, existing.is_none());
            let removal =
                plan_remove(Client::Claude, &[Some(&plan.contents[0])], &plan.record).unwrap();
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
        let plan = plan_write(Client::Claude, &[Some(existing)], &connection(&[]), None).unwrap();
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
        let debug = format!("{plan:?}");
        for secret in ["sk-secret", "secret-helper", "other.example", TOKEN] {
            assert!(!plan.conflicts.join(",").contains(secret));
            assert!(!serde_json::to_string(&plan.record)
                .unwrap()
                .contains(secret));
        }
        assert!(debug.contains("ANTHROPIC_API_KEY"));
    }

    #[test]
    fn rewriting_replaces_our_own_values_without_asking() {
        let models = vec![("opusModel", "opus-route".to_string())];
        let first = plan_write(Client::Claude, &[None], &connection(&models), None).unwrap();
        let second = plan_write(
            Client::Claude,
            &[Some(&first.contents[0])],
            &Connection {
                token_id: "token_02",
                token: OTHER_TOKEN,
                origin: "http://127.0.0.1:9000",
                models: &[],
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
        let plan = plan_write(Client::Claude, &[None], &connection(&models), None).unwrap();
        let edited = plan.contents[0]
            .replace("main-route", "user-route")
            .replace("\"env\": {", "\"env\": {\n    \"EDITOR\": \"vim\",");
        let [Removal::Write(left)] =
            &plan_remove(Client::Claude, &[Some(&edited)], &plan.record).unwrap()[..]
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
            let error =
                plan_write(Client::Claude, &[Some(existing)], &connection(&[]), None).unwrap_err();
            assert_eq!(error.file, 0);
            assert!(!error.message.contains(TOKEN));
            assert_eq!(
                inspect(Client::Claude, &[Some(existing)], None, Some(ORIGIN)),
                ClientState::Invalid
            );
        }
    }

    #[test]
    fn codex_config_keeps_comments_and_other_tables() {
        let existing = "# user settings\nmodel = \"gpt-5\" # mine\napproval_policy = \"never\"\n\n[profiles.fast]\nmodel = \"o3\"\n\n[model_providers.other]\nname = \"Other\"\n";
        let plan = plan_write(
            Client::Codex,
            &[Some(existing)],
            &connection(&codex_models()),
            None,
        )
        .unwrap();
        assert_eq!(plan.conflicts, ["model"]);
        assert_eq!(
            plan.contents[0],
            "# user settings\nmodel = \"gpt-route\" # mine\napproval_policy = \"never\"\nmodel_provider = \"astrlink\"\n\n[profiles.fast]\nmodel = \"o3\"\n\n[model_providers.other]\nname = \"Other\"\n\n[model_providers.astrlink]\nname = \"AstrLink\"\nbase_url = \"http://127.0.0.1:18317/v1\"\nwire_api = \"responses\"\nexperimental_bearer_token = \"astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0\"\nhttp_headers = { x-openai-actor-authorization = \"codex-imagegen\" }\n"
        );
        assert_eq!(plan.record.base_url, "http://127.0.0.1:18317/v1");
        let [Removal::Write(removed)] =
            &plan_remove(Client::Codex, &[Some(&plan.contents[0])], &plan.record).unwrap()[..]
        else {
            panic!("expected a rewrite");
        };
        assert_eq!(
            removed,
            "# user settings\napproval_policy = \"never\"\n\n[profiles.fast]\nmodel = \"o3\"\n\n[model_providers.other]\nname = \"Other\"\n"
        );
    }

    #[test]
    fn codex_config_is_created_without_auth_settings() {
        let plan = plan_write(Client::Codex, &[None], &connection(&codex_models()), None).unwrap();
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
            plan_remove(Client::Codex, &[Some(&plan.contents[0])], &plan.record).unwrap(),
            [Removal::Delete]
        );
    }

    #[test]
    fn codex_tools_header_sits_beside_the_users_own_headers() {
        let existing = "[model_providers.astrlink.http_headers]\nx-team = \"blue\"\n";
        let plan = plan_write(
            Client::Codex,
            &[Some(existing)],
            &connection(&codex_models()),
            None,
        )
        .unwrap();
        assert!(plan.conflicts.is_empty(), "{:?}", plan.conflicts);
        assert!(plan.contents[0].contains("x-team = \"blue\""));
        assert!(plan.contents[0].contains("x-openai-actor-authorization = \"codex-imagegen\""));
        let [Removal::Write(removed)] =
            &plan_remove(Client::Codex, &[Some(&plan.contents[0])], &plan.record).unwrap()[..]
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
        let plan = plan_write(
            Client::Codex,
            &[Some(existing)],
            &connection(&codex_models()),
            None,
        )
        .unwrap();
        assert_eq!(plan.conflicts, [CODEX_HEADERS_KEY]);
        assert!(plan.contents[0].contains(
            "http_headers = { X-OpenAI-Actor-Authorization = \"codex-imagegen\", x-team = \"blue\" }"
        ));
    }

    #[test]
    fn sync_adds_the_codex_tools_header_to_released_configs_once() {
        let plan = plan_write(Client::Codex, &[None], &connection(&codex_models()), None).unwrap();
        // Released builds wrote no header and kept no fingerprint for it.
        let released = plan.contents[0].replace(
            "http_headers = { x-openai-actor-authorization = \"codex-imagegen\" }\n",
            "",
        );
        let mut record = plan.record.clone();
        record.keys.remove(CODEX_HEADERS_KEY);
        assert_eq!(
            inspect(
                Client::Codex,
                &[Some(&released)],
                Some(&record),
                Some(ORIGIN)
            ),
            ClientState::Configured
        );
        let (contents, next) = plan_sync(Client::Codex, &[Some(&released)], &record, ORIGIN)
            .unwrap()
            .expect("the header is added");
        assert_eq!(contents[0], plan.contents[0]);
        assert_eq!(next.keys, plan.record.keys);
        assert!(
            plan_sync(Client::Codex, &[Some(&contents[0])], &next, ORIGIN)
                .unwrap()
                .is_none()
        );

        // A header the user removed after AstrLink wrote it stays removed.
        assert!(
            plan_sync(Client::Codex, &[Some(&released)], &plan.record, ORIGIN)
                .unwrap()
                .is_none()
        );
        // A changed connection is left alone.
        let modified = released.replace("wire_api = \"responses\"", "wire_api = \"chat\"");
        assert!(
            plan_sync(Client::Codex, &[Some(&modified)], &record, ORIGIN)
                .unwrap()
                .is_none()
        );
        // A new port and the header arrive in one write.
        let (contents, next) = plan_sync(
            Client::Codex,
            &[Some(&released)],
            &record,
            "http://127.0.0.1:9000",
        )
        .unwrap()
        .expect("synced");
        assert!(contents[0].contains("base_url = \"http://127.0.0.1:9000/v1\""));
        assert!(contents[0].contains("x-openai-actor-authorization = \"codex-imagegen\""));
        assert_eq!(next.base_url, "http://127.0.0.1:9000/v1");
    }

    #[test]
    fn codex_conflicts_cover_a_foreign_provider_table() {
        let existing = "model_provider = \"other\"\n\n[model_providers.astrlink]\nbase_url = \"https://secret.example/v1\"\nenv_key = \"SECRET_KEY\"\n";
        let plan = plan_write(
            Client::Codex,
            &[Some(existing)],
            &connection(&codex_models()),
            None,
        )
        .unwrap();
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
            Client::Codex,
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
        let plan = plan_write(
            Client::Codex,
            &[Some(existing)],
            &connection(&codex_models()),
            None,
        )
        .unwrap();
        assert!(plan.contents[0].starts_with("# a\r\napproval_policy = \"never\"\r\n"));
        assert!(!plan.contents[0].replace("\r\n", "").contains('\n'));
        let [Removal::Write(removed)] =
            &plan_remove(Client::Codex, &[Some(&plan.contents[0])], &plan.record).unwrap()[..]
        else {
            panic!("expected a rewrite");
        };
        assert_eq!(removed, existing);
    }

    #[test]
    fn a_model_the_client_changed_does_not_block_sync() {
        let plan = plan_write(Client::Codex, &[None], &connection(&codex_models()), None).unwrap();
        let edited = plan.contents[0].replace("gpt-route", "picked-in-tui");
        assert_eq!(
            inspect(
                Client::Codex,
                &[Some(&edited)],
                Some(&plan.record),
                Some(ORIGIN)
            ),
            ClientState::Configured
        );
        let (synced, record) = plan_sync(
            Client::Codex,
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
                Client::Codex,
                &[Some(synced)],
                Some(&record),
                Some("http://127.0.0.1:9000")
            ),
            ClientState::Configured
        );
        // The model the user picked is theirs now and survives removal.
        let [Removal::Write(removed)] =
            &plan_remove(Client::Codex, &[Some(synced)], &record).unwrap()[..]
        else {
            panic!("expected a rewrite");
        };
        assert_eq!(removed, "model = \"picked-in-tui\"\n");
    }

    #[test]
    fn a_changed_connection_is_modified_and_never_synced() {
        let plan = plan_write(Client::Claude, &[None], &connection(&[]), None).unwrap();
        assert_eq!(
            inspect(
                Client::Claude,
                &[Some(&plan.contents[0])],
                Some(&plan.record),
                Some(ORIGIN)
            ),
            ClientState::Configured
        );
        assert_eq!(
            inspect(
                Client::Claude,
                &[Some(&plan.contents[0])],
                Some(&plan.record),
                Some("http://127.0.0.1:9000")
            ),
            ClientState::Outdated
        );
        let edited = plan.contents[0].replace(TOKEN, OTHER_TOKEN);
        assert_eq!(
            inspect(
                Client::Claude,
                &[Some(&edited)],
                Some(&plan.record),
                Some("http://127.0.0.1:9000")
            ),
            ClientState::Modified
        );
        assert_eq!(
            plan_sync(
                Client::Claude,
                &[Some(&edited)],
                &plan.record,
                "http://127.0.0.1:9000"
            )
            .unwrap(),
            None
        );
        assert_eq!(
            inspect(Client::Claude, &[None], Some(&plan.record), Some(ORIGIN)),
            ClientState::Modified
        );
        assert_eq!(
            inspect(Client::Claude, &[Some("{}")], None, Some(ORIGIN)),
            ClientState::NotConfigured
        );
    }

    #[test]
    fn a_synced_claude_config_changes_only_the_base_url() {
        let models = vec![("model", "main-route".to_string())];
        let plan = plan_write(Client::Claude, &[None], &connection(&models), None).unwrap();
        let (synced, record) = plan_sync(
            Client::Claude,
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
        assert!(plan_sync(
            Client::Claude,
            &[Some(synced)],
            &record,
            "http://127.0.0.1:9000"
        )
        .unwrap()
        .is_none());
    }

    #[test]
    fn only_claude_codex_and_pi_are_written_directly() {
        let home = unique_home("unsupported");
        for client in [Client::Gemini, Client::Opencode, Client::Openclaw] {
            let error = write(
                Home::local(&home),
                client,
                &connection(&codex_models()),
                &tokens(),
                true,
            )
            .unwrap_err();
            assert!(!error.contains(TOKEN));
            assert!(remove(Home::local(&home), client).is_err());
            assert!(snippet(client, &connection(&codex_models())).is_err());
        }
        let clients = status(
            Home::local(&home),
            Some("http://127.0.0.1:18317/"),
            Some(&tokens()),
        )
        .unwrap()
        .into_iter()
        .map(|status| status.client)
        .collect::<Vec<_>>();
        assert_eq!(clients, [Client::Claude, Client::Codex, Client::Pi]);
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn moved_dirs_and_wsl_homes_hold_their_own_configs() {
        use crate::{agent_home::ToolDirs, wsl::WslHome};

        let host = unique_home("moved-host");
        let claude = host.join("work").join("claude");
        let pi = host.join("work").join("pi-agent");
        fs::create_dir_all(&claude).unwrap();
        fs::create_dir_all(&pi).unwrap();
        let dirs = ToolDirs {
            claude: Some(claude.clone()),
            pi: Some(pi.clone()),
            ..Default::default()
        };
        let local = crate::agent_home::homes(&host, &dirs, &[]).next().unwrap();
        assert!(Client::Pi.detected(local));
        write(local, Client::Claude, &connection(&[]), &tokens(), false).unwrap();
        write(
            local,
            Client::Pi,
            &connection(&pi_models()),
            &tokens(),
            false,
        )
        .unwrap();
        assert!(claude.join("settings.json").is_file());
        assert!(pi.join("models.json").is_file() && pi.join("settings.json").is_file());
        assert!(!host.join(".claude").join("settings.json").exists());
        assert!(!host.join(".pi").exists());

        let share = unique_home("moved-share");
        let wsl = WslHome {
            distribution: "Ubuntu".to_string(),
            share: share.clone(),
            mount_root: "/mnt".to_string(),
            dirs: ToolDirs::default(),
        };
        let statuses = status(
            Home::in_wsl(&host, &wsl),
            Some("http://127.0.0.1:8317/"),
            Some(&tokens()),
        )
        .unwrap();
        assert!(statuses
            .iter()
            .all(|status| status.wsl.as_deref() == Some("Ubuntu")
                && status.state == ClientState::NotConfigured));
        let claude_status = statuses
            .iter()
            .find(|status| status.client == Client::Claude)
            .unwrap();
        assert!(claude_status.detected);
        assert_eq!(
            claude_status.paths,
            [share
                .join(".claude")
                .join("settings.json")
                .display()
                .to_string()]
        );
        // Each home keeps its own records.
        write(
            Home::in_wsl(&host, &wsl),
            Client::Claude,
            &connection(&[]),
            &tokens(),
            false,
        )
        .unwrap();
        assert!(records_path(&share).is_file());
        assert!(share.join(".claude").join("settings.json").is_file());
        remove(Home::in_wsl(&host, &wsl), Client::Claude).unwrap();
        assert!(!share.join(".claude").join("settings.json").exists());
        assert!(read_records(&host).unwrap().claude.is_some());
        assert!(claude.join("settings.json").is_file());
        fs::remove_dir_all(host).unwrap();
        fs::remove_dir_all(share).unwrap();
    }

    fn pi_models() -> Vec<(&'static str, String)> {
        vec![("model", "pi-route".to_string())]
    }

    #[test]
    fn pi_endpoints_and_startup_model_are_written_to_two_files() {
        let plan = plan_write(Client::Pi, &[None, None], &connection(&pi_models()), None).unwrap();
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
            inspect(Client::Pi, &existing, Some(&plan.record), Some(ORIGIN)),
            ClientState::Configured
        );
        // The created endpoint file goes; Pi's own settings file stays.
        assert_eq!(
            plan_remove(Client::Pi, &existing, &plan.record).unwrap(),
            [Removal::Delete, Removal::Write("{}\n".into())]
        );
        assert!(snippet(Client::Pi, &connection(&pi_models())).is_err());
    }

    #[test]
    fn pi_files_keep_comments_other_providers_and_preferences() {
        let models = "{\n  // local servers\n  \"providers\": {\n    \"ollama\": {\"baseUrl\": \"http://localhost:11434/v1\", \"apiKey\": \"sk-other\"},\n  },\n}\n";
        let settings = "{\n  \"theme\": \"dark\",\n  \"defaultProvider\": \"ollama\",\n  \"defaultModel\": \"qwen\"\n}\n";
        let plan = plan_write(
            Client::Pi,
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
        let removals = plan_remove(Client::Pi, &existing, &plan.record).unwrap();
        let [Removal::Write(models_left), Removal::Write(settings_left)] = &removals[..] else {
            panic!("expected two rewrites");
        };
        assert!(models_left.contains("// local servers") && models_left.contains("ollama"));
        assert!(!models_left.contains("astrlink"));
        // Replaced settings are not restored.
        assert_eq!(json(settings_left), serde_json::json!({"theme": "dark"}));
    }

    #[test]
    fn pi_model_choices_and_edited_model_lists_belong_to_the_user() {
        let plan = plan_write(Client::Pi, &[None, None], &connection(&pi_models()), None).unwrap();
        // Saving another model in Pi's picker is not a connection change.
        let picked = plan.contents[1].replace("astrlink", "anthropic");
        let edited = plan.contents[0].replace(
            "\"id\": \"pi-route\"",
            "\"id\": \"pi-route\", \"reasoning\": true",
        );
        assert_ne!(edited, plan.contents[0]);
        let existing = [Some(edited.as_str()), Some(&picked)];
        assert_eq!(
            inspect(Client::Pi, &existing, Some(&plan.record), Some(ORIGIN)),
            ClientState::Configured
        );
        let (synced, record) =
            plan_sync(Client::Pi, &existing, &plan.record, "http://127.0.0.1:9000")
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
            Client::Pi,
            &[Some(&synced[0]), Some(&synced[1])],
            &connection(&pi_models()),
            Some(&record),
        )
        .unwrap();
        assert_eq!(
            rewrite.conflicts,
            ["providers.astrlink.models", "defaultProvider"]
        );
        let removals =
            plan_remove(Client::Pi, &[Some(&synced[0]), Some(&synced[1])], &record).unwrap();
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

    fn levels(names: &[&str]) -> Vec<String> {
        names.iter().map(|name| name.to_string()).collect()
    }

    #[test]
    fn pi_entries_name_the_official_thinking_levels() {
        let gpt = levels(&["none", "low", "medium", "high", "xhigh", "max"]);
        let plan = plan_write_with(
            Client::Pi,
            &[None, None],
            &connection(&pi_models()),
            &PiDetails {
                reasoning: Some(gpt.clone()),
                ..Default::default()
            },
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
            inspect(Client::Pi, &existing, Some(&plan.record), Some(ORIGIN)),
            ClientState::Configured
        );
        assert_eq!(
            current_models(Client::Pi, &[existing[0], None]),
            BTreeMap::from([("model", "pi-route".to_string())])
        );
        assert_eq!(
            plan_remove(Client::Pi, &existing, &plan.record).unwrap()[0],
            Removal::Delete
        );

        // A model that cannot answer without reasoning has no off level.
        let opus = levels(&["low", "medium", "high", "xhigh", "max"]);
        let plan = plan_write_with(
            Client::Pi,
            &[None, None],
            &connection(&pi_models()),
            &PiDetails {
                reasoning: Some(opus.clone()),
                ..Default::default()
            },
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
            Client::Pi,
            &[None, None],
            &connection(&pi_models()),
            &PiDetails {
                reasoning: Some(Vec::new()),
                ..Default::default()
            },
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
        let released =
            plan_write(Client::Pi, &[None, None], &connection(&pi_models()), None).unwrap();
        let existing = [
            Some(released.contents[0].as_str()),
            Some(&released.contents[1]),
        ];
        let gpt = levels(&["none", "low", "medium", "high", "xhigh", "max"]);
        let plan = plan_write_with(
            Client::Pi,
            &existing,
            &connection(&pi_models()),
            &PiDetails {
                reasoning: Some(gpt.clone()),
                ..Default::default()
            },
            Some(&released.record),
        )
        .unwrap();
        assert!(plan.conflicts.is_empty());
        assert!(plan.contents[0].contains("\"thinkingLevelMap\""));
        // Reformatting the entry keeps it AstrLink's.
        let compact = json(&plan.contents[0]).to_string();
        assert_eq!(
            plan_remove(
                Client::Pi,
                &[Some(&compact), Some(&plan.contents[1])],
                &plan.record
            )
            .unwrap()[0],
            Removal::Delete
        );
        // A level the user changed is theirs.
        let edited = plan.contents[0].replace("\"max\": \"max\"", "\"max\": null");
        assert_ne!(edited, plan.contents[0]);
        let existing = [Some(edited.as_str()), Some(&plan.contents[1])];
        let rewrite = plan_write_with(
            Client::Pi,
            &existing,
            &connection(&pi_models()),
            &PiDetails {
                reasoning: Some(gpt.clone()),
                ..Default::default()
            },
            Some(&plan.record),
        )
        .unwrap();
        assert_eq!(rewrite.conflicts, ["providers.astrlink.models"]);
        assert_eq!(
            current_models(Client::Pi, &[existing[0], None]),
            BTreeMap::from([("model", "pi-route".to_string())])
        );
        let removals = plan_remove(Client::Pi, &existing, &plan.record).unwrap();
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
        let pi = PiDetails {
            reasoning: Some(levels(&["none", "high"])),
            context_window: Some(400_000),
            max_tokens: Some(64_000),
        };
        let plan = plan_write_with(
            Client::Pi,
            &[None, None],
            &connection(&pi_models()),
            &pi,
            None,
        )
        .unwrap();
        let entry = &json(&plan.contents[0])["providers"]["astrlink"]["models"][0];
        assert_eq!(entry["contextWindow"], 400_000);
        assert_eq!(entry["maxTokens"], 64_000);
        let existing = [Some(plan.contents[0].as_str()), Some(&plan.contents[1])];
        assert_eq!(
            current_models(Client::Pi, &existing),
            BTreeMap::from([
                ("contextWindow", "400000".to_string()),
                ("maxTokens", "64000".to_string()),
                ("model", "pi-route".to_string()),
            ])
        );
        // Writing the same entry again changes nothing and asks nothing.
        let again = plan_write_with(
            Client::Pi,
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
            Client::Pi,
            &existing,
            &connection(&pi_models()),
            &PiDetails::default(),
            Some(&plan.record),
        )
        .unwrap();
        assert!(cleared.conflicts.is_empty());
        assert_eq!(
            json(&cleared.contents[0])["providers"]["astrlink"]["models"],
            serde_json::json!([{"id": "pi-route"}])
        );
        assert_eq!(
            plan_remove(Client::Pi, &existing, &plan.record).unwrap()[0],
            Removal::Delete
        );
        let limits = |context_window, max_tokens| Models {
            model: Some("m".into()),
            context_window,
            max_tokens,
            ..Default::default()
        };
        assert_eq!(
            limits(Some(1), Some(MAX_PI_LIMIT)).pi_limits(Client::Pi),
            Ok((Some(1), Some(MAX_PI_LIMIT)))
        );
        assert!(limits(Some(0), None).pi_limits(Client::Pi).is_err());
        assert!(limits(None, Some(MAX_PI_LIMIT + 1))
            .pi_limits(Client::Pi)
            .is_err());
        assert!(limits(Some(1), None).pi_limits(Client::Codex).is_err());
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
            let error =
                plan_write(Client::Pi, &existing, &connection(&pi_models()), None).unwrap_err();
            assert_eq!(error.file, file);
            assert!(!error.message.contains(TOKEN));
            assert_eq!(
                inspect(Client::Pi, &existing, None, Some(ORIGIN)),
                ClientState::Invalid
            );
        }
        let plan = plan_write(
            Client::Pi,
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
                Client::Pi,
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
        assert_eq!(statuses[2].state, ClientState::Outdated);
        assert_eq!(
            statuses[2].paths,
            [models.display().to_string(), settings.display().to_string()]
        );
        sync(Home::local(&home), "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert!(fs::read_to_string(&models)
            .unwrap()
            .contains("http://127.0.0.1:9000/v1"));
        remove(Home::local(&home), Client::Pi).unwrap();
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
            Client::Pi,
            &connection(&pi_models()),
            &tokens(),
            false
        )
        .is_err());
        assert!(!home.join(".pi/agent/models.json").exists());
        assert!(!records_path(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn apply_asks_before_replacing_and_writes_the_record_first() {
        let home = unique_home("apply");
        let settings = home.join(".claude/settings.json");
        let foreign = "{\n  \"env\": {\n    \"ANTHROPIC_API_KEY\": \"sk-secret\"\n  }\n}\n";
        fs::write(&settings, foreign).unwrap();
        let outcome = write(
            Home::local(&home),
            Client::Claude,
            &connection(&[]),
            &tokens(),
            false,
        )
        .unwrap();
        assert_eq!(
            outcome,
            ApplyOutcome::NeedsConfirmation {
                keys: vec!["env.ANTHROPIC_API_KEY".into()],
                gateway: None,
            }
        );
        assert_eq!(fs::read_to_string(&settings).unwrap(), foreign);
        assert!(!records_path(&home).exists());

        let outcome = write(
            Home::local(&home),
            Client::Claude,
            &connection(&[]),
            &tokens(),
            true,
        )
        .unwrap();
        assert_eq!(outcome, ApplyOutcome::Applied);
        let written = fs::read_to_string(&settings).unwrap();
        assert!(!written.contains("sk-secret"));
        let records = fs::read_to_string(records_path(&home)).unwrap();
        assert!(!records.contains(TOKEN));
        assert!(!records.contains("sk-secret"));
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            for path in [settings.clone(), records_path(&home)] {
                let mode = fs::metadata(path).unwrap().permissions().mode() & 0o777;
                assert_eq!(mode, 0o600);
            }
        }
        let statuses = status(
            Home::local(&home),
            Some("http://127.0.0.1:18317/"),
            Some(&tokens()),
        )
        .unwrap();
        assert_eq!(statuses[0].state, ClientState::Configured);
        assert_eq!(statuses[0].token_id.as_deref(), Some("token_01"));
        assert_eq!(statuses[1].state, ClientState::NotConfigured);

        remove(Home::local(&home), Client::Claude).unwrap();
        assert_eq!(fs::read_to_string(&settings).unwrap(), "{}\n");
        assert!(!records_path(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn status_names_the_models_written_configs_hold_now() {
        let home = unique_home("models");
        fs::create_dir_all(home.join(".pi/agent")).unwrap();
        let settings = home.join(".claude/settings.json");
        // A model in a config AstrLink did not write is not its to show.
        fs::write(&settings, "{\"env\": {\"ANTHROPIC_MODEL\": \"own\"}}\n").unwrap();
        let models = || {
            status(Home::local(&home), None, None)
                .unwrap()
                .into_iter()
                .map(|status| status.models)
                .collect::<Vec<_>>()
        };
        assert!(models().iter().all(BTreeMap::is_empty));

        let claude = [
            ("model", "main-route".to_string()),
            ("opusModel", "opus-route".to_string()),
        ];
        for (client, models) in [
            (Client::Claude, &claude[..]),
            (Client::Codex, &codex_models()[..]),
            (Client::Pi, &pi_models()[..]),
        ] {
            write(
                Home::local(&home),
                client,
                &connection(models),
                &tokens(),
                true,
            )
            .unwrap();
        }
        assert_eq!(
            models(),
            [
                BTreeMap::from([
                    ("model", "main-route".into()),
                    ("opusModel", "opus-route".into())
                ]),
                BTreeMap::from([("model", "gpt-route".into())]),
                BTreeMap::from([("model", "pi-route".into())]),
            ]
        );

        // Models the clients picked since are the current ones, and a name
        // AstrLink could not write back is left out.
        let edit = |path: PathBuf, from: &str, to: &str| {
            let text = fs::read_to_string(&path).unwrap();
            assert!(text.contains(from));
            fs::write(&path, text.replace(from, to)).unwrap();
        };
        edit(settings, "opus-route", &"x".repeat(257));
        edit(
            home.join(".codex/config.toml"),
            "gpt-route",
            "picked-in-tui",
        );
        let pi_settings = home.join(".pi/agent/settings.json");
        edit(pi_settings.clone(), "\"pi-route\"", "\"pi-mini\"");
        assert_eq!(
            models(),
            [
                BTreeMap::from([("model", "main-route".into())]),
                BTreeMap::from([("model", "picked-in-tui".into())]),
                BTreeMap::from([("model", "pi-mini".into())]),
            ]
        );
        // Pi starting on another provider still lists AstrLink's model.
        edit(pi_settings, "\"astrlink\"", "\"anthropic\"");
        assert_eq!(models()[2], BTreeMap::from([("model", "pi-route".into())]));
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn codex_apply_never_touches_auth_json() {
        let home = unique_home("codex-auth");
        let auth = home.join(".codex/auth.json");
        let login = "{\"auth_mode\": \"chatgpt\", \"tokens\": {}}\n";
        fs::write(&auth, login).unwrap();
        write(
            Home::local(&home),
            Client::Codex,
            &connection(&codex_models()),
            &tokens(),
            false,
        )
        .unwrap();
        sync(Home::local(&home), "http://127.0.0.1:9000/", &tokens()).unwrap();
        let config = fs::read_to_string(home.join(".codex/config.toml")).unwrap();
        assert!(config.contains("base_url = \"http://127.0.0.1:9000/v1\""));
        remove(Home::local(&home), Client::Codex).unwrap();
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
    fn sync_leaves_modified_and_unreadable_configs_alone() {
        let home = unique_home("sync");
        write(
            Home::local(&home),
            Client::Claude,
            &connection(&[]),
            &tokens(),
            false,
        )
        .unwrap();
        write(
            Home::local(&home),
            Client::Codex,
            &connection(&codex_models()),
            &tokens(),
            false,
        )
        .unwrap();
        let settings = home.join(".claude/settings.json");
        let edited = fs::read_to_string(&settings)
            .unwrap()
            .replace("127.0.0.1:18317", "proxy.example");
        fs::write(&settings, &edited).unwrap();
        let broken = "token = \"astr_x\n";
        fs::write(home.join(".codex/config.toml"), broken).unwrap();
        sync(Home::local(&home), "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert_eq!(fs::read_to_string(&settings).unwrap(), edited);
        assert_eq!(
            fs::read_to_string(home.join(".codex/config.toml")).unwrap(),
            broken
        );
        let statuses = status(
            Home::local(&home),
            Some("http://127.0.0.1:9000/"),
            Some(&tokens()),
        )
        .unwrap();
        assert_eq!(statuses[0].state, ClientState::Modified);
        assert_eq!(statuses[1].state, ClientState::Invalid);
        let error = remove(Home::local(&home), Client::Codex).unwrap_err();
        assert!(error.contains("config.toml") && error.contains("TOML"));
        assert!(!error.contains("astr_x"));
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn another_astrlinks_config_moves_here_only_once_confirmed() {
        let home = unique_home("elsewhere");
        let config = home.join(".codex/config.toml");
        let models = codex_models();
        let other = Tokens(BTreeSet::from(["token_02".to_string()]));
        let theirs = Connection {
            token_id: "token_02",
            token: OTHER_TOKEN,
            origin: "http://127.0.0.1:8317",
            models: &models,
        };
        write(Home::local(&home), Client::Codex, &theirs, &other, false).unwrap();
        let written = fs::read_to_string(&config).unwrap();
        let codex = |tokens: Option<&Tokens>| {
            status(Home::local(&home), Some("http://127.0.0.1:18317/"), tokens)
                .unwrap()
                .remove(1)
        };
        let elsewhere = codex(Some(&tokens()));
        assert_eq!(elsewhere.state, ClientState::Elsewhere);
        assert_eq!(elsewhere.token_id.as_deref(), Some("token_02"));
        assert_eq!(codex(None).state, ClientState::Outdated);
        sync(Home::local(&home), "http://127.0.0.1:18317/", &tokens()).unwrap();
        assert_eq!(fs::read_to_string(&config).unwrap(), written);

        assert_eq!(
            write(
                Home::local(&home),
                Client::Codex,
                &connection(&models),
                &tokens(),
                false
            )
            .unwrap(),
            ApplyOutcome::NeedsConfirmation {
                keys: Vec::new(),
                gateway: Some("http://127.0.0.1:8317/v1".into()),
            }
        );
        assert_eq!(fs::read_to_string(&config).unwrap(), written);
        write(
            Home::local(&home),
            Client::Codex,
            &connection(&models),
            &tokens(),
            true,
        )
        .unwrap();
        let switched = fs::read_to_string(&config).unwrap();
        assert!(switched.contains(&format!("base_url = \"{ORIGIN}/v1\"")));
        assert!(switched.contains(TOKEN));
        assert_eq!(codex(Some(&tokens())).state, ClientState::Configured);
        // The other AstrLink leaves it here in turn.
        sync(Home::local(&home), "http://127.0.0.1:8317/", &other).unwrap();
        assert_eq!(fs::read_to_string(&config).unwrap(), switched);
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn a_client_moved_to_cc_switch_is_left_to_it_once_cc_switch_switches() {
        let home = unique_home("cc-switch");
        let config = home.join(".codex/config.toml");
        write(
            Home::local(&home),
            Client::Codex,
            &connection(&codex_models()),
            &tokens(),
            false,
        )
        .unwrap();
        let written = fs::read_to_string(&config).unwrap();
        // CC Switch's switch takes over the route and model keys and adds its
        // own provider table; AstrLink's table stays behind.
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

        // The move counts once CC Switch replaces AstrLink's connection.
        fs::write(&config, &written).unwrap();
        assert!(set_cc_switch(Home::local(&home), Client::Codex, true).unwrap());
        assert!(!set_cc_switch(Home::local(&home), Client::Codex, true).unwrap());
        assert_eq!(codex(&home).state, ClientState::Configured);
        fs::write(&config, &switched).unwrap();
        let moved = codex(&home);
        assert_eq!(moved.state, ClientState::CcSwitch);
        assert_eq!(moved.token_id, None);
        sync(Home::local(&home), "http://127.0.0.1:9000/", &tokens()).unwrap();
        assert_eq!(fs::read_to_string(&config).unwrap(), switched);

        // Writing takes it back, asking only about the route CC Switch chose.
        assert_eq!(
            write(
                Home::local(&home),
                Client::Codex,
                &connection(&codex_models()),
                &tokens(),
                false
            )
            .unwrap(),
            ApplyOutcome::NeedsConfirmation {
                keys: vec!["model_provider".into()],
                gateway: None,
            }
        );
        write(
            Home::local(&home),
            Client::Codex,
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
        assert!(!fs::read_to_string(records_path(&home))
            .unwrap()
            .contains("cc_switch"));
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn removing_a_client_cc_switch_holds_leaves_its_files_alone() {
        let home = unique_home("cc-switch-remove");
        let settings = home.join(".claude/settings.json");
        let models = vec![("model", "main-route".to_string())];
        write(
            Home::local(&home),
            Client::Claude,
            &connection(&models),
            &tokens(),
            false,
        )
        .unwrap();
        set_cc_switch(Home::local(&home), Client::Claude, true).unwrap();
        // A CC Switch provider with another token keeps the address and model
        // AstrLink wrote, which removal would otherwise take for its own.
        let switched = fs::read_to_string(&settings)
            .unwrap()
            .replace(TOKEN, OTHER_TOKEN);
        fs::write(&settings, &switched).unwrap();
        assert_eq!(
            status(Home::local(&home), None, None).unwrap()[0].state,
            ClientState::CcSwitch
        );
        remove(Home::local(&home), Client::Claude).unwrap();
        assert_eq!(fs::read_to_string(&settings).unwrap(), switched);
        assert!(!records_path(&home).exists());
        assert_eq!(
            status(Home::local(&home), None, None).unwrap()[0].state,
            ClientState::NotConfigured
        );
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn moves_are_noted_only_for_clients_astrlink_writes() {
        let home = unique_home("cc-switch-only");
        assert!(!set_cc_switch(Home::local(&home), Client::Gemini, true).unwrap());
        assert!(!records_path(&home).exists());
        assert!(set_cc_switch(Home::local(&home), Client::Codex, true).unwrap());
        let statuses = status(Home::local(&home), None, None).unwrap();
        assert_eq!(statuses[0].state, ClientState::NotConfigured);
        assert_eq!(statuses[1].state, ClientState::CcSwitch);
        assert_eq!(statuses[1].token_id, None);
        remove(Home::local(&home), Client::Codex).unwrap();
        assert!(!records_path(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn records_from_released_builds_still_load() {
        let home = unique_home("released-records");
        let plan = plan_write(Client::Codex, &[None], &connection(&codex_models()), None).unwrap();
        fs::write(home.join(".codex/config.toml"), &plan.contents[0]).unwrap();
        // Released builds wrote one object per client and nothing else.
        fs::create_dir_all(astrlink_home(&home)).unwrap();
        fs::write(
            records_path(&home),
            serde_json::json!({"version": 1, "codex": plan.record}).to_string(),
        )
        .unwrap();
        assert_eq!(
            status(
                Home::local(&home),
                Some("http://127.0.0.1:18317/"),
                Some(&tokens())
            )
            .unwrap()[1]
                .state,
            ClientState::Configured
        );
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn comments_above_removed_codex_keys_stay() {
        let models = codex_models();
        for (existing, expected) in [
            ("# header\nmodel = \"old\"\n", "# header\n"),
            (
                "approval_policy = \"never\"\n# pick one\nmodel = \"old\"\n\n[tui]\nx = 1\n",
                "approval_policy = \"never\"\n# pick one\n\n[tui]\nx = 1\n",
            ),
        ] {
            let plan =
                plan_write(Client::Codex, &[Some(existing)], &connection(&models), None).unwrap();
            let [Removal::Write(removed)] =
                &plan_remove(Client::Codex, &[Some(&plan.contents[0])], &plan.record).unwrap()[..]
            else {
                panic!("expected a rewrite");
            };
            assert_eq!(removed, expected);
        }
    }

    #[cfg(unix)]
    #[test]
    fn a_symlinked_settings_file_is_written_at_its_target() {
        let home = unique_home("symlink");
        let target = home.join("dotfiles-settings.json");
        fs::write(&target, "{}\n").unwrap();
        std::os::unix::fs::symlink(&target, home.join(".claude/settings.json")).unwrap();
        write(
            Home::local(&home),
            Client::Claude,
            &connection(&[]),
            &tokens(),
            false,
        )
        .unwrap();
        assert!(fs::symlink_metadata(home.join(".claude/settings.json"))
            .unwrap()
            .file_type()
            .is_symlink());
        assert!(fs::read_to_string(&target).unwrap().contains(TOKEN));
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn models_are_validated_and_fable_is_claude_only() {
        let models: Models = serde_json::from_value(serde_json::json!({
            "model": " main ", "fableModel": "fable-route"
        }))
        .unwrap();
        assert_eq!(
            models.fields(Client::Claude).unwrap(),
            [
                ("model", "main".to_string()),
                ("fableModel", "fable-route".to_string())
            ]
        );
        assert_eq!(
            models.fields(Client::Codex).unwrap(),
            [("model", "main".to_string())]
        );
        for key in [
            "model",
            "haikuModel",
            "sonnetModel",
            "opusModel",
            "fableModel",
        ] {
            for value in ["x".repeat(257), "invalid\nmodel".into()] {
                let models: Models =
                    serde_json::from_value(serde_json::json!({key: value})).unwrap();
                assert!(models.fields(Client::Claude).is_err());
            }
        }
        assert!(Models::default().fields(Client::Codex).is_err());
        assert!(serde_json::from_value::<Models>(serde_json::json!({"name": "x"})).is_err());
    }

    #[test]
    fn local_origin_accepts_only_the_loopback_gateway_root() {
        assert_eq!(
            local_origin("http://127.0.0.1:18317/").unwrap(),
            "http://127.0.0.1:18317"
        );
        assert_eq!(
            local_origin("http://localhost:18317").unwrap(),
            "http://localhost:18317"
        );
        for url in [
            "https://127.0.0.1:1",
            "http://[::1]:1",
            "http://localhost.example:1",
            "http://localhost:1/v1",
            "http://127.0.0.1:1/v1",
            "http://user@127.0.0.1:1",
            "http://127.0.0.1:1?x",
            "http://127.0.0.1:1#x",
        ] {
            assert!(local_origin(url).is_err());
        }
    }

    #[test]
    fn snippets_match_what_apply_writes() {
        let codex = snippet(Client::Codex, &connection(&codex_models())).unwrap();
        assert_eq!(
            codex,
            plan_write(Client::Codex, &[None], &connection(&codex_models()), None)
                .unwrap()
                .contents[0]
        );
        let claude = snippet(Client::Claude, &connection(&[])).unwrap();
        assert_eq!(
            json(&claude),
            serde_json::json!({"env": {"ANTHROPIC_BASE_URL": ORIGIN, "ANTHROPIC_AUTH_TOKEN": TOKEN}})
        );
    }
}
