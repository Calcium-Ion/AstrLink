//! Imports the providers Claude Code, Codex and Pi already use, from their
//! config files in the desktop user's home and in running WSL homes.
//!
//! Nothing is read until the user picks tools and asks to read them, and only
//! those tools' files are read. Keys stay here: the dialog sees hints and
//! confirms by id, and a key is attached only to a request for the address it
//! was configured with. Commands that would print a key never run, and source
//! files are never written.

use std::io::Read as _;
use std::net::IpAddr;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, Instant};

use reqwest::Url;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use tauri::State;
use toml_edit::{DocumentMut, Item};
use zeroize::Zeroizing;

use crate::agent_home::{Home, Homes};
use crate::client_config::{self, CODEX_PROVIDER, PI_PROVIDER};
use crate::provider_import::{
    key_hint, new_id, plain_text, redact, valid_base_url, MAX_SECRET_BYTES,
};
use crate::sidecar::{CoreManager, CoreSnapshot, ServiceRecordResponse};
use crate::{control_session, wsl};

/// Keys read for one dialog are dropped after this long without a request.
const IDLE_TIMEOUT: Duration = Duration::from_secs(10 * 60);
const MAX_FILE_BYTES: u64 = 4 * 1024 * 1024;
const MAX_NAME_CHARS: usize = 128;
const MAX_MODELS: usize = 2_000;
const OFFICIAL_ANTHROPIC: &str = "https://api.anthropic.com";
const OFFICIAL_OPENAI: &str = "https://api.openai.com/v1";
/// Sign-in methods that send an API key; others cannot carry an imported one.
const AUTH_SCHEMES: [&str; 3] = ["bearer", "anthropic_api_key", "google_api_key"];
const CLOSED: &str = "this import has closed; read the configs again";

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Tool {
    Claude,
    Codex,
    Pi,
}

impl Tool {
    const ALL: [Self; 3] = [Self::Claude, Self::Codex, Self::Pi];

    /// The files a read opens. The first one holds the providers.
    fn files(self, home: Home) -> Vec<PathBuf> {
        match self {
            Self::Claude => vec![home.claude_dir().join("settings.json")],
            Self::Codex => {
                let dir = home.codex_dir();
                vec![dir.join("config.toml"), dir.join("auth.json")]
            }
            Self::Pi => vec![home.pi_agent_dir().join("models.json")],
        }
    }

    fn detected(self, home: Home) -> bool {
        match self {
            Self::Claude => home.claude_detected(),
            Self::Codex => home.codex_dir().is_dir(),
            Self::Pi => home.pi_detected(),
        }
    }
}

/// One tool in one home, as the consent step lists it.
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct SourceView {
    id: String,
    tool: Tool,
    /// The WSL distribution, or `None` for the desktop user's home.
    wsl: Option<String>,
    /// The files a read opens, with the home shown as `~`.
    paths: Vec<String>,
}

#[derive(Clone, Debug)]
struct Source {
    view: SourceView,
    files: Vec<PathBuf>,
}

#[derive(Debug, Serialize)]
pub struct Opened {
    session: String,
    sources: Vec<SourceView>,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct SourceRef {
    tool: Tool,
    wsl: Option<String>,
}

#[derive(Clone)]
enum Key {
    Found(Zeroizing<String>),
    /// Named by an environment variable AstrLink's own environment lacks.
    Env(String),
    /// Printed by a command, which is never run.
    Command(Zeroizing<String>),
    Missing,
}

impl Key {
    fn same(&self, other: &Self) -> bool {
        match (self, other) {
            (Self::Found(a), Self::Found(b)) | (Self::Command(a), Self::Command(b)) => {
                a.as_str() == b.as_str()
            }
            (Self::Env(a), Self::Env(b)) => a == b,
            (Self::Missing, Self::Missing) => true,
            _ => false,
        }
    }

    fn view(&self) -> KeyView {
        match self {
            Self::Found(key) => KeyView::Found {
                hint: key_hint(key),
            },
            Self::Env(name) => KeyView::Env { name: name.clone() },
            Self::Command(_) => KeyView::Command,
            Self::Missing => KeyView::Missing,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Serialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub enum KeyView {
    Found { hint: Option<String> },
    Env { name: String },
    Command,
    Missing,
}

/// A provider as one config file sets it up.
struct Entry {
    name: Option<String>,
    address: String,
    protocol: Option<&'static str>,
    models: Vec<String>,
    key: Key,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum SkipReason {
    /// Codex signed in with ChatGPT; a subscription account covers it.
    ChatgptLogin,
    /// The entry points at AstrLink itself.
    Astrlink,
    Missing,
    Unreadable,
    Invalid,
    InvalidAddress,
    Unsupported,
    SignIn,
}

/// Something a read found but does not offer, and why.
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct Skipped {
    reason: SkipReason,
    tool: Tool,
    wsl: Option<String>,
    name: Option<String>,
    path: Option<String>,
    detail: Option<String>,
}

struct Skip {
    reason: SkipReason,
    name: Option<String>,
    path: Option<String>,
    detail: Option<String>,
}

impl Skip {
    fn new(reason: SkipReason) -> Self {
        Self {
            reason,
            name: None,
            path: None,
            detail: None,
        }
    }

    fn named(reason: SkipReason, name: Option<&str>) -> Self {
        Self {
            name: name.and_then(shown_name),
            ..Self::new(reason)
        }
    }

    fn file(reason: SkipReason, path: &str, detail: Option<String>) -> Self {
        Self {
            path: Some(path.to_string()),
            detail,
            ..Self::new(reason)
        }
    }

    fn at(self, place: &SourceRef) -> Skipped {
        Skipped {
            reason: self.reason,
            tool: place.tool,
            wsl: place.wsl.clone(),
            name: self.name,
            path: self.path,
            detail: self.detail,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Progress {
    Ready,
    Busy,
    Imported,
}

/// A provider the read found; entries with the same address and key merge.
struct Found {
    id: String,
    name: Option<String>,
    address: String,
    origin: String,
    normalized: String,
    protocols: Vec<&'static str>,
    models: Vec<String>,
    key: Key,
    sources: Vec<SourceRef>,
    progress: Progress,
    probed: bool,
}

impl Found {
    fn view(&self) -> Candidate {
        Candidate {
            id: self.id.clone(),
            name: self.name.clone(),
            address: self.address.clone(),
            protocols: self.protocols.clone(),
            models: self.models.clone(),
            key: self.key.view(),
            sources: self.sources.clone(),
        }
    }
}

/// A found provider as the dialog shows it; never carries a key.
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct Candidate {
    id: String,
    name: Option<String>,
    address: String,
    /// Formats the tools speak to it, as Core protocol ids.
    protocols: Vec<&'static str>,
    models: Vec<String>,
    key: KeyView,
    sources: Vec<SourceRef>,
}

#[derive(Debug, Serialize)]
pub struct ReadResult {
    candidates: Vec<Candidate>,
    skipped: Vec<Skipped>,
}

struct Collected {
    found: Vec<Found>,
    skipped: Vec<Skipped>,
}

struct Session {
    id: String,
    sources: Vec<Source>,
    found: Vec<Found>,
    touched: Instant,
    watched: bool,
}

impl Session {
    fn found(&mut self, candidate: &str) -> Result<&mut Found, String> {
        self.found
            .iter_mut()
            .find(|found| found.id == candidate)
            .ok_or_else(|| "this provider is not in the import".to_string())
    }
}

/// The open import dialog's sources and, after a read, what it found. Opening
/// the dialog again replaces it.
pub struct LocalImports {
    slot: Arc<Mutex<Option<Session>>>,
    idle: Duration,
}

impl Default for LocalImports {
    fn default() -> Self {
        Self::with_idle(IDLE_TIMEOUT)
    }
}

impl LocalImports {
    fn with_idle(idle: Duration) -> Self {
        Self {
            slot: Arc::default(),
            idle,
        }
    }

    fn lock(&self) -> MutexGuard<'_, Option<Session>> {
        self.slot.lock().unwrap_or_else(|error| error.into_inner())
    }

    fn open(&self, sources: Vec<Source>) -> Result<Opened, String> {
        let id = new_id()?;
        let opened = Opened {
            session: id.clone(),
            sources: sources.iter().map(|source| source.view.clone()).collect(),
        };
        *self.lock() = Some(Session {
            id,
            sources,
            found: Vec::new(),
            touched: Instant::now(),
            watched: false,
        });
        Ok(opened)
    }

    /// Runs `task` on the session named `id` unless it closed, or sat idle
    /// while holding what a read found. Before a read it holds no keys.
    fn with<T>(
        &self,
        id: &str,
        task: impl FnOnce(&mut Session) -> Result<T, String>,
    ) -> Result<T, String> {
        let mut slot = self.lock();
        if slot.as_ref().is_some_and(|session| {
            !session.found.is_empty() && session.touched.elapsed() >= self.idle
        }) {
            *slot = None;
        }
        let session = slot
            .as_mut()
            .filter(|session| session.id == id)
            .ok_or(CLOSED)?;
        session.touched = Instant::now();
        task(session)
    }

    /// The sources the user checked, in the order the dialog listed them.
    fn chosen(&self, id: &str, chosen: &[String]) -> Result<Vec<Source>, String> {
        self.with(id, |session| {
            if chosen.is_empty() {
                return Err("choose a tool to read".into());
            }
            if let Some(unknown) = chosen
                .iter()
                .find(|id| !session.sources.iter().any(|source| source.view.id == **id))
            {
                return Err(format!("unknown source {unknown}"));
            }
            Ok(session
                .sources
                .iter()
                .filter(|source| chosen.contains(&source.view.id))
                .cloned()
                .collect())
        })
    }

    fn store(&self, id: &str, collected: Collected) -> Result<ReadResult, String> {
        self.with(id, |session| {
            session.found = collected.found;
            Ok(ReadResult {
                candidates: session.found.iter().map(Found::view).collect(),
                skipped: collected.skipped,
            })
        })
    }

    /// Attaches the provider's key to the one model-list request it gets.
    fn begin_probe(
        &self,
        id: &str,
        candidate: &str,
        input: Value,
    ) -> Result<(Value, Zeroizing<String>), String> {
        self.with(id, |session| {
            let found = session.found(candidate)?;
            if found.progress != Progress::Ready {
                return Err("this provider was already imported".into());
            }
            if found.probed {
                return Err("the model list was already requested".into());
            }
            let authorized = authorize(found, input, &["kind", "http", "protocol"])?;
            found.probed = true;
            Ok(authorized)
        })
    }

    /// Holds the provider while it is created so a second confirm cannot
    /// add it twice.
    fn begin_confirm(
        &self,
        id: &str,
        candidate: &str,
        input: Value,
    ) -> Result<(Value, Zeroizing<String>), String> {
        self.with(id, |session| {
            let found = session.found(candidate)?;
            match found.progress {
                Progress::Busy => Err("this provider is being imported".into()),
                Progress::Imported => Err("this provider was already imported".into()),
                Progress::Ready => {
                    let authorized = authorize(
                        found,
                        input,
                        &["name", "kind", "enabled", "models", "http", "capabilities"],
                    )?;
                    found.progress = Progress::Busy;
                    Ok(authorized)
                }
            }
        })
    }

    /// Records the outcome; an imported provider's key is dropped at once.
    fn finish_confirm(&self, id: &str, candidate: &str, imported: bool) {
        let mut slot = self.lock();
        let Some(found) = slot
            .as_mut()
            .filter(|session| session.id == id)
            .and_then(|session| session.found(candidate).ok())
        else {
            return;
        };
        if imported {
            found.progress = Progress::Imported;
            found.key = Key::Missing;
        } else {
            found.progress = Progress::Ready;
        }
    }

    fn dismiss(&self, id: &str) {
        let mut slot = self.lock();
        if slot.as_ref().is_some_and(|session| session.id == id) {
            *slot = None;
        }
    }

    /// Drops the session's keys once it sits idle, even while the dialog
    /// stays open without another request.
    fn watch(&self, id: &str) {
        {
            let mut slot = self.lock();
            match slot.as_mut().filter(|session| session.id == id) {
                Some(session) if !session.watched => session.watched = true,
                _ => return,
            }
        }
        let slot = Arc::clone(&self.slot);
        let idle = self.idle;
        let id = id.to_string();
        tauri::async_runtime::spawn(async move {
            loop {
                let wait = {
                    let mut slot = slot.lock().unwrap_or_else(|error| error.into_inner());
                    let Some(session) = slot.as_ref().filter(|session| session.id == id) else {
                        return;
                    };
                    let elapsed = session.touched.elapsed();
                    if elapsed >= idle {
                        *slot = None;
                        return;
                    }
                    idle - elapsed
                };
                tokio::time::sleep(wait).await;
            }
        });
    }
}

/// Every tool in the desktop user's home, then the tools found in each WSL
/// home. Only directories are checked; no file is opened.
fn locate(homes: &Homes) -> Vec<Source> {
    let mut sources = Vec::new();
    for home in homes.iter() {
        for tool in Tool::ALL {
            if home.distribution().is_some() && !tool.detected(home) {
                continue;
            }
            let files = tool.files(home);
            sources.push(Source {
                view: SourceView {
                    id: format!("s{}", sources.len()),
                    tool,
                    wsl: home.distribution().map(str::to_string),
                    paths: files.iter().map(|file| display_path(home, file)).collect(),
                },
                files,
            });
        }
    }
    sources
}

fn display_path(home: Home, path: &Path) -> String {
    let Ok(rest) = path.strip_prefix(&*home) else {
        return home.shown(path);
    };
    let separator = if home.windows() { "\\" } else { "/" };
    let parts: Vec<_> = rest.iter().map(|part| part.to_string_lossy()).collect();
    if parts.is_empty() {
        "~".into()
    } else {
        format!("~{separator}{}", parts.join(separator))
    }
}

/// A file's text, `None` when it does not exist, or why it cannot be read.
type FileText = Result<Option<Zeroizing<String>>, &'static str>;

fn read_file(path: &Path) -> FileText {
    let file = match std::fs::File::open(path) {
        Ok(file) => file,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(error) if error.kind() == std::io::ErrorKind::PermissionDenied => {
            return Err("permission")
        }
        Err(_) => return Err("io"),
    };
    if !file.metadata().map_err(|_| "io")?.is_file() {
        return Err("io");
    }
    let mut bytes = Zeroizing::new(Vec::new());
    file.take(MAX_FILE_BYTES + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| "io")?;
    if bytes.len() as u64 > MAX_FILE_BYTES {
        return Err("too_large");
    }
    let text = std::str::from_utf8(&bytes).map_err(|_| "not_text")?;
    let text = text.strip_prefix('\u{feff}').unwrap_or(text);
    Ok(Some(Zeroizing::new(text.to_string())))
}

/// An environment variable of AstrLink's own process; no shell is asked.
fn process_env(name: &str) -> Option<Zeroizing<String>> {
    std::env::var(name)
        .ok()
        .filter(|value| !value.trim().is_empty())
        .map(Zeroizing::new)
}

type Env<'a> = dyn Fn(&str) -> Option<Zeroizing<String>> + 'a;

/// The ports Core serves inference on; loopback addresses there are AstrLink.
fn gateway_ports(snapshot: &CoreSnapshot) -> Vec<u16> {
    snapshot
        .ready
        .iter()
        .flat_map(|ready| [&ready.inference_url, &ready.client_inference_url])
        .filter_map(|url| Url::parse(url).ok()?.port_or_known_default())
        .collect()
}

fn collect(
    sources: &[Source],
    read: &mut dyn FnMut(&Path) -> FileText,
    env: &Env,
    gateway_ports: &[u16],
) -> Collected {
    let mut found: Vec<Found> = Vec::new();
    let mut skipped: Vec<Skipped> = Vec::new();
    for source in sources {
        let place = SourceRef {
            tool: source.view.tool,
            wsl: source.view.wsl.clone(),
        };
        let (entries, skips) = read_source(source, read, env);
        let mut skips: Vec<_> = skips.into_iter().map(|skip| skip.at(&place)).collect();
        for entry in entries {
            match admit(entry, gateway_ports) {
                Ok((entry, url)) => merge(&mut found, entry, &url, &place),
                Err(skip) => skips.push(skip.at(&place)),
            }
        }
        for skip in skips {
            if !skipped.contains(&skip) {
                skipped.push(skip);
            }
        }
    }
    Collected { found, skipped }
}

fn read_source(
    source: &Source,
    read: &mut dyn FnMut(&Path) -> FileText,
    env: &Env,
) -> (Vec<Entry>, Vec<Skip>) {
    let mut skipped = Vec::new();
    let mut load = |index: usize, skipped: &mut Vec<Skip>| -> Option<Zeroizing<String>> {
        let shown = &source.view.paths[index];
        match read(&source.files[index]) {
            Ok(text) => {
                // Codex's auth.json is optional; the provider file is not.
                if text.is_none() && index == 0 {
                    skipped.push(Skip::file(SkipReason::Missing, shown, None));
                }
                text
            }
            Err(code) => {
                skipped.push(Skip::file(SkipReason::Unreadable, shown, Some(code.into())));
                None
            }
        }
    };
    let invalid = |index: usize, error: String| {
        Skip::file(SkipReason::Invalid, &source.view.paths[index], Some(error))
    };
    let entries = match source.view.tool {
        Tool::Claude => match load(0, &mut skipped).map(|text| claude_entries(&text)) {
            Some(Ok((entries, skips))) => {
                skipped.extend(skips);
                entries
            }
            Some(Err(error)) => {
                skipped.push(invalid(0, error));
                Vec::new()
            }
            None => Vec::new(),
        },
        Tool::Codex => {
            let config =
                match load(0, &mut skipped).map(|text| client_config::read_codex_config(&text)) {
                    Some(Ok(config)) => Some(config),
                    Some(Err(error)) => {
                        skipped.push(invalid(0, error));
                        None
                    }
                    None => None,
                };
            let auth = match load(1, &mut skipped).map(|text| read_codex_auth(&text)) {
                Some(Ok(auth)) => Some(auth),
                Some(Err(error)) => {
                    skipped.push(invalid(1, error));
                    None
                }
                None => None,
            };
            let (entries, skips) = codex_entries(config.as_ref(), auth.as_ref(), env);
            skipped.extend(skips);
            entries
        }
        Tool::Pi => match load(0, &mut skipped).map(|text| pi_entries(&text, env)) {
            Some(Ok((entries, skips))) => {
                skipped.extend(skips);
                entries
            }
            Some(Err(error)) => {
                skipped.push(invalid(0, error));
                Vec::new()
            }
            None => Vec::new(),
        },
    };
    (entries, skipped)
}

/// Rejects entries that point at AstrLink or have no usable address.
fn admit(entry: Entry, gateway_ports: &[u16]) -> Result<(Entry, Url), Skip> {
    let address = entry.address.trim().trim_end_matches('/');
    let url = Url::parse(address).ok().filter(|_| valid_base_url(address));
    let label = entry.name.clone().or_else(|| url.as_ref().map(authority));
    if matches!(&entry.key, Key::Found(key) if key.trim_start().starts_with("astr_")) {
        return Err(Skip::named(SkipReason::Astrlink, label.as_deref()));
    }
    let Some(url) = url else {
        return Err(Skip::named(SkipReason::InvalidAddress, label.as_deref()));
    };
    if loopback(&url)
        && url
            .port_or_known_default()
            .is_some_and(|port| gateway_ports.contains(&port))
    {
        return Err(Skip::named(SkipReason::Astrlink, label.as_deref()));
    }
    let address = address.to_string();
    Ok((Entry { address, ..entry }, url))
}

fn authority(url: &Url) -> String {
    let host = url.host_str().unwrap_or_default();
    match url.port() {
        Some(port) => format!("{host}:{port}"),
        None => host.to_string(),
    }
}

fn loopback(url: &Url) -> bool {
    let Some(host) = url.host_str() else {
        return false;
    };
    let host = host.trim_start_matches('[').trim_end_matches(']');
    match host.parse::<IpAddr>() {
        Ok(IpAddr::V4(ip)) => ip.is_loopback() || ip.is_unspecified(),
        Ok(IpAddr::V6(ip)) => {
            ip.is_loopback()
                || ip.is_unspecified()
                || ip.to_ipv4_mapped().is_some_and(|ip| ip.is_loopback())
        }
        Err(_) => host == "localhost" || host.ends_with(".localhost"),
    }
}

/// Scheme, host, port and path, ignoring a trailing slash and `/v1`, which
/// clients append differently for the same endpoint.
fn normalized(url: &Url) -> String {
    let path = url.path().trim_end_matches('/');
    let path = path
        .strip_suffix("/v1")
        .unwrap_or(path)
        .trim_end_matches('/');
    format!("{}{path}", url.origin().ascii_serialization())
}

fn merge(found: &mut Vec<Found>, entry: Entry, url: &Url, place: &SourceRef) {
    let normalized = normalized(url);
    let index = match found
        .iter()
        .position(|found| found.normalized == normalized && found.key.same(&entry.key))
    {
        Some(index) => index,
        None => {
            found.push(Found {
                id: format!("c{}", found.len()),
                name: None,
                address: entry.address,
                origin: url.origin().ascii_serialization(),
                normalized,
                protocols: Vec::new(),
                models: Vec::new(),
                key: entry.key,
                sources: Vec::new(),
                progress: Progress::Ready,
                probed: false,
            });
            found.len() - 1
        }
    };
    let found = &mut found[index];
    if found.name.is_none() {
        found.name = entry.name.as_deref().and_then(shown_name);
    }
    if let Some(protocol) = entry.protocol {
        if !found.protocols.contains(&protocol) {
            found.protocols.push(protocol);
        }
    }
    for model in entry.models {
        let model = model.trim();
        if found.models.len() < MAX_MODELS
            && !model.is_empty()
            && client_config::valid_model(model)
            && !found.models.iter().any(|known| known == model)
        {
            found.models.push(model.to_string());
        }
    }
    if !found.sources.contains(place) {
        found.sources.push(place.clone());
    }
}

/// A name from a config file, if it is short plain text worth showing.
fn shown_name(name: &str) -> Option<String> {
    let name = name.trim();
    (!name.is_empty() && name.chars().count() <= MAX_NAME_CHARS && plain_text(name))
        .then(|| name.to_string())
}

fn env_name(value: &str) -> bool {
    value.len() <= 256
        && value.starts_with(|c: char| c.is_ascii_alphabetic() || c == '_')
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'_')
}

fn env_key(name: &str, env: &Env) -> Key {
    if !env_name(name) {
        return Key::Missing;
    }
    env(name).map_or_else(|| Key::Env(name.to_string()), Key::Found)
}

fn trimmed(value: Option<&str>) -> Option<&str> {
    value.map(str::trim).filter(|value| !value.is_empty())
}

fn json_str(value: Option<&Value>) -> Option<&str> {
    trimmed(value.and_then(Value::as_str))
}

fn toml_str(item: Option<&Item>) -> Option<&str> {
    trimmed(item.and_then(Item::as_str))
}

/// Claude Code's `env` sets one provider: its own address or Anthropic's.
fn claude_entries(text: &str) -> Result<(Vec<Entry>, Vec<Skip>), String> {
    let settings = client_config::read_claude_settings(text)?;
    let vars = settings.get("env").and_then(Value::as_object);
    let var = |name: &str| json_str(vars.and_then(|vars| vars.get(name)));
    for (flag, platform) in [
        ("CLAUDE_CODE_USE_BEDROCK", "Amazon Bedrock"),
        ("CLAUDE_CODE_USE_VERTEX", "Google Vertex AI"),
        ("CLAUDE_CODE_USE_FOUNDRY", "Microsoft Foundry"),
    ] {
        if var(flag).is_some_and(|value| {
            ["1", "true", "yes", "on"].contains(&value.to_ascii_lowercase().as_str())
        }) {
            return Ok((
                Vec::new(),
                vec![Skip::named(SkipReason::Unsupported, Some(platform))],
            ));
        }
    }
    // Claude Code's order: the bearer token, the API key, then the helper.
    let key = if let Some(token) = var("ANTHROPIC_AUTH_TOKEN") {
        Key::Found(Zeroizing::new(token.to_string()))
    } else if let Some(key) = var("ANTHROPIC_API_KEY") {
        Key::Found(Zeroizing::new(key.to_string()))
    } else if let Some(helper) = json_str(settings.get("apiKeyHelper")) {
        Key::Command(Zeroizing::new(helper.to_string()))
    } else {
        Key::Missing
    };
    let address = var("ANTHROPIC_BASE_URL");
    if address.is_none() && matches!(key, Key::Missing) {
        // Signed in to Claude, or nothing set up here.
        return Ok((Vec::new(), Vec::new()));
    }
    let models = client_config::CLAUDE_MODEL_KEYS
        .iter()
        .filter_map(|(_, path)| path.strip_prefix("env."))
        .chain(["ANTHROPIC_SMALL_FAST_MODEL"])
        .filter_map(var)
        // `[1m]` selects Claude Code's long context; it is not in the model id.
        .map(|model| model.strip_suffix("[1m]").unwrap_or(model).to_string())
        .collect();
    Ok((
        vec![Entry {
            name: None,
            address: address.unwrap_or(OFFICIAL_ANTHROPIC).to_string(),
            protocol: Some("anthropic.messages"),
            models,
            key,
        }],
        Vec::new(),
    ))
}

/// The parts of Codex's auth.json an import may use.
#[derive(Deserialize)]
struct CodexAuth {
    #[serde(rename = "OPENAI_API_KEY", default)]
    api_key: Option<Zeroizing<String>>,
    /// ChatGPT sign-in tokens; never read beyond whether they exist.
    #[serde(default)]
    tokens: Option<serde::de::IgnoredAny>,
}

fn read_codex_auth(text: &str) -> Result<CodexAuth, String> {
    serde_json::from_str(text).map_err(|error| {
        // serde's messages may quote the value; keep only the position.
        format!(
            "is not valid JSON (line {}, column {})",
            error.line(),
            error.column()
        )
    })
}

/// Each provider in Codex's config.toml, and OpenAI when auth.json holds an
/// API key. A ChatGPT sign-in is left to subscription accounts.
fn codex_entries(
    config: Option<&DocumentMut>,
    auth: Option<&CodexAuth>,
    env: &Env,
) -> (Vec<Entry>, Vec<Skip>) {
    let mut entries = Vec::new();
    let mut skipped = Vec::new();
    let chatgpt = auth.is_some_and(|auth| auth.tokens.is_some());
    if chatgpt {
        skipped.push(Skip::new(SkipReason::ChatgptLogin));
    }
    let api_key = auth
        .filter(|_| !chatgpt)
        .and_then(|auth| trimmed(auth.api_key.as_deref().map(String::as_str)))
        .map(|key| Zeroizing::new(key.to_string()));

    let models = config.map(codex_models).unwrap_or_default();
    let models_for = |provider: &str| {
        models
            .iter()
            .filter(|(id, _)| id == provider)
            .map(|(_, model)| model.clone())
            .collect::<Vec<_>>()
    };
    let openai_address = config
        .and_then(|config| toml_str(config.get("openai_base_url")))
        .unwrap_or(OFFICIAL_OPENAI);

    let mut configured_openai = false;
    let providers = config
        .and_then(|config| config.get("model_providers"))
        .and_then(Item::as_table_like);
    for (id, item) in providers.into_iter().flat_map(|providers| providers.iter()) {
        let Some(provider) = item.as_table_like() else {
            continue;
        };
        let name = toml_str(provider.get("name")).unwrap_or(id);
        if id == CODEX_PROVIDER {
            skipped.push(Skip::named(SkipReason::Astrlink, Some(id)));
            continue;
        }
        if provider.contains_key("aws") {
            skipped.push(Skip::named(SkipReason::Unsupported, Some(name)));
            continue;
        }
        configured_openai |= id == "openai";
        let command = provider
            .get("auth")
            .and_then(Item::as_table_like)
            .and_then(|auth| toml_str(auth.get("command")));
        let key = if let Some(token) = toml_str(provider.get("experimental_bearer_token")) {
            Key::Found(Zeroizing::new(token.to_string()))
        } else if let Some(variable) = toml_str(provider.get("env_key")) {
            env_key(variable, env)
        } else if let Some(command) = command {
            Key::Command(Zeroizing::new(command.to_string()))
        } else if id == "openai"
            || provider.get("requires_openai_auth").and_then(Item::as_bool) == Some(true)
        {
            api_key.clone().map_or(Key::Missing, Key::Found)
        } else {
            Key::Missing
        };
        let fallback = if id == "openai" {
            openai_address
        } else {
            OFFICIAL_OPENAI
        };
        entries.push(Entry {
            name: Some(name.to_string()),
            address: toml_str(provider.get("base_url"))
                .unwrap_or(fallback)
                .to_string(),
            protocol: Some(match toml_str(provider.get("wire_api")) {
                Some("chat") => "openai.chat",
                _ => "openai.responses",
            }),
            models: models_for(id),
            key,
        });
    }
    if let (false, Some(key)) = (configured_openai, api_key) {
        entries.push(Entry {
            name: Some("OpenAI".into()),
            address: openai_address.to_string(),
            protocol: Some("openai.responses"),
            models: models_for("openai"),
            key: Key::Found(key),
        });
    }
    (entries, skipped)
}

/// `(provider, model)` for the default model and each profile's.
fn codex_models(config: &DocumentMut) -> Vec<(String, String)> {
    let default_provider = toml_str(config.get("model_provider")).unwrap_or("openai");
    let mut models = Vec::new();
    if let Some(model) = toml_str(config.get("model")) {
        models.push((default_provider.to_string(), model.to_string()));
    }
    let profiles = config.get("profiles").and_then(Item::as_table_like);
    for (_, profile) in profiles.into_iter().flat_map(|profiles| profiles.iter()) {
        let Some(profile) = profile.as_table_like() else {
            continue;
        };
        if let Some(model) = toml_str(profile.get("model")) {
            let provider = toml_str(profile.get("model_provider")).unwrap_or(default_provider);
            models.push((provider.to_string(), model.to_string()));
        }
    }
    models
}

/// Pi's API formats AstrLink can serve.
fn pi_protocol(api: &str) -> Option<&'static str> {
    match api {
        "openai-completions" => Some("openai.chat"),
        "openai-responses" => Some("openai.responses"),
        "anthropic-messages" => Some("anthropic.messages"),
        "google-generative-ai" => Some("google.generate_content"),
        _ => None,
    }
}

/// Each provider in Pi's models.json; models with their own address or
/// format become entries of their own.
fn pi_entries(text: &str, env: &Env) -> Result<(Vec<Entry>, Vec<Skip>), String> {
    let config = client_config::read_pi_models(text)?;
    let mut entries = Vec::new();
    let mut skipped = Vec::new();
    let providers = config.get("providers").and_then(Value::as_object);
    for (id, provider) in providers.into_iter().flatten() {
        let Some(provider) = provider.as_object() else {
            continue;
        };
        let name = json_str(provider.get("name")).unwrap_or(id);
        if id == PI_PROVIDER {
            skipped.push(Skip::named(SkipReason::Astrlink, Some(id.as_str())));
            continue;
        }
        if provider.contains_key("oauth") {
            skipped.push(Skip::named(SkipReason::SignIn, Some(name)));
            continue;
        }
        let key = json_str(provider.get("apiKey")).map_or(Key::Missing, |value| pi_key(value, env));
        let address = json_str(provider.get("baseUrl"));
        let api = json_str(provider.get("api"));
        let mut groups: Vec<(Option<&str>, Option<&str>, Vec<String>)> = Vec::new();
        let models = provider.get("models").and_then(Value::as_array);
        for model in models.into_iter().flatten() {
            let Some(model_id) = json_str(model.get("id")) else {
                continue;
            };
            let at = json_str(model.get("baseUrl")).or(address);
            let speaks = json_str(model.get("api")).or(api);
            match groups
                .iter_mut()
                .find(|(group_at, group_api, _)| *group_at == at && *group_api == speaks)
            {
                Some((_, _, ids)) => ids.push(model_id.to_string()),
                None => groups.push((at, speaks, vec![model_id.to_string()])),
            }
        }
        if groups.is_empty() {
            groups.push((address, api, Vec::new()));
        }
        for (at, speaks, models) in groups {
            // Without an address it only adjusts one of Pi's own providers.
            let Some(at) = at else {
                continue;
            };
            let protocol = match speaks {
                None => None,
                Some(speaks) => match pi_protocol(speaks) {
                    Some(protocol) => Some(protocol),
                    None => {
                        skipped.push(Skip {
                            detail: (speaks.len() <= 64 && env_name(&speaks.replace('-', "_")))
                                .then(|| speaks.to_string()),
                            ..Skip::named(SkipReason::Unsupported, Some(name))
                        });
                        continue;
                    }
                },
            };
            entries.push(Entry {
                name: Some(name.to_string()),
                address: at.to_string(),
                protocol,
                models,
                key: key.clone(),
            });
        }
    }
    Ok((entries, skipped))
}

/// Resolves Pi's `apiKey` as Pi does, without running anything: `!` runs a
/// command; `$NAME`, `${NAME}` read the environment; `$$` and `$!` escape.
/// Before Pi 0.77 a bare variable name also read the environment.
fn pi_key(value: &str, env: &Env) -> Key {
    if value.starts_with('!') {
        return Key::Command(Zeroizing::new(value.to_string()));
    }
    let bare_name = value.starts_with(|c: char| c.is_ascii_uppercase())
        && value.contains('_')
        && !value.ends_with('_')
        && !value.contains("__")
        && value
            .bytes()
            .all(|byte| byte.is_ascii_uppercase() || byte.is_ascii_digit() || byte == b'_');
    if bare_name {
        return env_key(value, env);
    }
    let mut resolved = Zeroizing::new(String::new());
    let mut missing: Option<&str> = None;
    let mut lookup = |name, resolved: &mut String| match env(name) {
        Some(found) => resolved.push_str(&found),
        None => {
            missing.get_or_insert(name);
        }
    };
    let mut index = 0;
    while index < value.len() {
        let Some(offset) = value[index..].find('$') else {
            resolved.push_str(&value[index..]);
            break;
        };
        let dollar = index + offset;
        resolved.push_str(&value[index..dollar]);
        let rest = &value[dollar + 1..];
        if rest.starts_with(['$', '!']) {
            resolved.push_str(&rest[..1]);
            index = dollar + 2;
        } else if let Some(braced) = rest.strip_prefix('{') {
            match braced.find('}') {
                Some(end) if env_name(&braced[..end]) => {
                    lookup(&braced[..end], &mut resolved);
                    index = dollar + end + 3;
                }
                Some(end) => {
                    resolved.push_str(&value[dollar..dollar + end + 3]);
                    index = dollar + end + 3;
                }
                None => {
                    resolved.push('$');
                    index = dollar + 1;
                }
            }
        } else {
            let length = rest
                .char_indices()
                .find(|&(at, c)| {
                    !(c.is_ascii_alphabetic() || c == '_' || (at > 0 && c.is_ascii_digit()))
                })
                .map_or(rest.len(), |(at, _)| at);
            if length > 0 {
                lookup(&rest[..length], &mut resolved);
                index = dollar + 1 + length;
            } else {
                resolved.push('$');
                index = dollar + 1;
            }
        }
    }
    match missing {
        Some(name) => Key::Env(name.to_string()),
        None if resolved.trim().is_empty() => Key::Missing,
        None => Key::Found(Zeroizing::new(resolved.trim().to_string())),
    }
}

/// Checks that a request goes to the provider's own origin with an API-key
/// sign-in, then attaches the key it was configured with. A provider without
/// a readable key takes the one the user pasted; one with a key takes no
/// other. Returns the request and the key it carries.
fn authorize(
    found: &Found,
    mut input: Value,
    allowed: &[&str],
) -> Result<(Value, Zeroizing<String>), String> {
    let invalid = || "invalid service input".to_string();
    let object = input.as_object_mut().ok_or_else(invalid)?;
    if object.keys().any(|key| !allowed.contains(&key.as_str())) {
        return Err(invalid());
    }
    let kind = object
        .get("kind")
        .and_then(Value::as_str)
        .ok_or_else(invalid)?;
    if kind.ends_with("_subscription") || kind == "vertex" {
        return Err("this service type cannot use an imported key".into());
    }
    let http = object
        .get_mut("http")
        .and_then(Value::as_object_mut)
        .ok_or_else(invalid)?;
    if http
        .keys()
        .any(|key| !["base_url", "auth", "credential"].contains(&key.as_str()))
    {
        return Err(invalid());
    }
    let base_url = http
        .get("base_url")
        .and_then(Value::as_str)
        .ok_or_else(invalid)?;
    let same_origin = valid_base_url(base_url)
        && Url::parse(base_url).is_ok_and(|url| url.origin().ascii_serialization() == found.origin);
    if !same_origin {
        return Err("the service address does not match the imported provider".into());
    }
    let auth = http
        .get("auth")
        .and_then(Value::as_object)
        .ok_or_else(invalid)?;
    let scheme = auth
        .get("scheme")
        .and_then(Value::as_str)
        .ok_or_else(invalid)?;
    if auth.len() != 1 || !AUTH_SCHEMES.contains(&scheme) {
        return Err("this sign-in method cannot use an imported key".into());
    }
    let secret = match &found.key {
        Key::Found(key) => {
            if http.contains_key("credential") {
                return Err("the imported key cannot be replaced".into());
            }
            http.insert("credential".into(), json!({ "secret": key.as_str() }));
            key.clone()
        }
        Key::Env(_) | Key::Command(_) | Key::Missing => {
            let credential = http
                .get("credential")
                .and_then(Value::as_object)
                .ok_or("paste the key first")?;
            let secret = credential
                .get("secret")
                .and_then(Value::as_str)
                .filter(|secret| !secret.trim().is_empty())
                .ok_or("paste the key first")?;
            if credential.len() != 1 || secret.len() > MAX_SECRET_BYTES || !plain_text(secret) {
                return Err("invalid key".into());
            }
            Zeroizing::new(secret.to_string())
        }
    };
    Ok((input, secret))
}

/// Locates the tools' config files without opening any of them, and starts a
/// new import that replaces any earlier one.
#[tauri::command]
pub async fn local_import_sources(imports: State<'_, LocalImports>) -> Result<Opened, String> {
    let home = control_session::user_home()?;
    let sources = tauri::async_runtime::spawn_blocking(move || {
        Homes::find(home, wsl::Reach::Running).map(|homes| locate(&homes))
    })
    .await
    .map_err(|error| format!("unable to find the configs: {error}"))??;
    imports.open(sources)
}

/// Reads the checked sources. The result names keys only by hint.
#[tauri::command]
pub async fn read_local_import(
    session: String,
    sources: Vec<String>,
    imports: State<'_, LocalImports>,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<ReadResult, String> {
    let chosen = imports.chosen(&session, &sources)?;
    let ports = gateway_ports(&manager.snapshot());
    let collected = tauri::async_runtime::spawn_blocking(move || {
        collect(&chosen, &mut read_file, &process_env, &ports)
    })
    .await
    .map_err(|error| format!("unable to read the configs: {error}"))?;
    let result = imports.store(&session, collected)?;
    imports.watch(&session);
    Ok(result)
}

/// The one model-list request a found provider gets before it is created.
#[tauri::command]
pub async fn probe_local_import_models(
    session: String,
    candidate: String,
    input: Value,
    imports: State<'_, LocalImports>,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<Value, String> {
    let (input, secret) = imports.begin_probe(&session, &candidate, input)?;
    manager
        .probe_draft_service_models(input)
        .await
        .map_err(|error| redact(error, Some(secret.as_str())))
}

#[tauri::command]
pub async fn confirm_local_import(
    session: String,
    candidate: String,
    input: Value,
    imports: State<'_, LocalImports>,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<ServiceRecordResponse, String> {
    let (input, secret) = imports.begin_confirm(&session, &candidate, input)?;
    let result = manager.create_service(input).await;
    imports.finish_confirm(&session, &candidate, result.is_ok());
    result.map_err(|error| redact(error, Some(secret.as_str())))
}

/// Ends the import and drops every key it read.
#[tauri::command]
pub fn dismiss_local_import(session: String, imports: State<'_, LocalImports>) {
    imports.dismiss(&session);
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;
    use std::fs;
    use std::sync::atomic::{AtomicU64, Ordering};

    use crate::agent_home::ToolDirs;

    static SEQUENCE: AtomicU64 = AtomicU64::new(0);

    fn temp_home(name: &str) -> PathBuf {
        let home = std::env::temp_dir().join(format!(
            "astrlink-local-import-{name}-{}-{}",
            std::process::id(),
            SEQUENCE.fetch_add(1, Ordering::Relaxed)
        ));
        let _ = fs::remove_dir_all(&home);
        fs::create_dir_all(&home).unwrap();
        home
    }

    fn write(path: &Path, text: &str) {
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, text).unwrap();
    }

    fn homes(host: &Path) -> Homes {
        Homes {
            host: host.to_path_buf(),
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
            unchecked: Vec::new(),
        }
    }

    fn env_of(vars: &[(&str, &str)]) -> impl Fn(&str) -> Option<Zeroizing<String>> {
        let vars: HashMap<String, String> = vars
            .iter()
            .map(|(name, value)| (name.to_string(), value.to_string()))
            .collect();
        move |name| vars.get(name).cloned().map(Zeroizing::new)
    }

    fn no_env(_: &str) -> Option<Zeroizing<String>> {
        None
    }

    fn sources(host: &Path, tools: &[Tool]) -> Vec<Source> {
        locate(&homes(host))
            .into_iter()
            .filter(|source| tools.contains(&source.view.tool))
            .collect()
    }

    fn read_all(host: &Path, tools: &[Tool], env: &Env, ports: &[u16]) -> Collected {
        collect(&sources(host, tools), &mut read_file, env, ports)
    }

    fn view(collected: &Collected) -> ReadResult {
        ReadResult {
            candidates: collected.found.iter().map(Found::view).collect(),
            skipped: collected.skipped.clone(),
        }
    }

    fn found(address: &str, key: Key) -> Found {
        let url = Url::parse(address).unwrap();
        Found {
            id: "c0".into(),
            name: None,
            address: address.into(),
            origin: url.origin().ascii_serialization(),
            normalized: normalized(&url),
            protocols: Vec::new(),
            models: Vec::new(),
            key,
            sources: Vec::new(),
            progress: Progress::Ready,
            probed: false,
        }
    }

    fn secret(key: &str) -> Key {
        Key::Found(Zeroizing::new(key.into()))
    }

    const CLAUDE_KEY: &str = "fake-claude-key-0c7d";
    const RELAY_KEY: &str = "fake-relay-key-k2Qe";
    const OPENAI_KEY: &str = "fake-openai-key-T3bA";
    const PI_KEY: &str = "fake-pi-key-x81P";

    fn fixture_home(name: &str) -> PathBuf {
        let home = temp_home(name);
        write(
            &home.join(".claude").join("settings.json"),
            &format!(
                r#"{{"env": {{
                    "ANTHROPIC_BASE_URL": "https://open.bigmodel.cn/api/anthropic/",
                    "ANTHROPIC_AUTH_TOKEN": "{CLAUDE_KEY}",
                    "ANTHROPIC_MODEL": "glm-5.3[1m]",
                    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-5.3-flash",
                    "ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.3"
                }}}}"#
            ),
        );
        write(
            &home.join(".codex").join("config.toml"),
            &format!(
                r#"model = "relay-large"
model_provider = "relay"

[profiles.fast]
model = "relay-small"

[profiles.openai]
model = "gpt-5.5"
model_provider = "openai"

[model_providers.relay]
name = "Relay Hub"
base_url = "https://api.relayhub.example/v1"
experimental_bearer_token = "{RELAY_KEY}"

[model_providers.astrlink]
name = "AstrLink"
base_url = "http://localhost:8317/v1"
"#
            ),
        );
        write(
            &home.join(".codex").join("auth.json"),
            &format!(r#"{{"OPENAI_API_KEY": "{OPENAI_KEY}", "tokens": null}}"#),
        );
        write(
            &home.join(".pi").join("agent").join("models.json"),
            &format!(
                r#"{{
                  // Pi takes comments and trailing commas.
                  "providers": {{
                    "relay": {{
                      "baseUrl": "https://api.relayhub.example",
                      "api": "openai-completions",
                      "apiKey": "{RELAY_KEY}",
                      "models": [{{"id": "relay-large"}}, {{"id": "relay-chat"}}],
                    }},
                    "deepseek": {{
                      "baseUrl": "https://api.deepseek.com/v1",
                      "api": "openai-completions",
                      "apiKey": "$DEEPSEEK_API_KEY",
                      "models": [{{"id": "deepseek-chat"}}, {{"id": "deepseek-reasoner"}}],
                    }},
                    "kimi": {{
                      "baseUrl": "https://api.kimi.com/coding",
                      "api": "anthropic-messages",
                      "apiKey": "{PI_KEY}",
                    }},
                    "astrlink": {{
                      "baseUrl": "http://127.0.0.1:8317/v1",
                      "apiKey": "astr_fake_token_value",
                    }},
                  }},
                }}"#
            ),
        );
        home
    }

    #[test]
    fn locate_lists_each_tool_with_home_relative_paths() {
        let home = temp_home("locate");
        let opened = LocalImports::default().open(locate(&homes(&home))).unwrap();
        let separator = if cfg!(windows) { "\\" } else { "/" };
        let shown = |parts: &[&str]| format!("~{separator}{}", parts.join(separator));
        assert_eq!(
            opened
                .sources
                .iter()
                .map(|source| (source.id.as_str(), source.tool, source.paths.clone()))
                .collect::<Vec<_>>(),
            vec![
                (
                    "s0",
                    Tool::Claude,
                    vec![shown(&[".claude", "settings.json"])]
                ),
                (
                    "s1",
                    Tool::Codex,
                    vec![
                        shown(&[".codex", "config.toml"]),
                        shown(&[".codex", "auth.json"]),
                    ]
                ),
                (
                    "s2",
                    Tool::Pi,
                    vec![shown(&[".pi", "agent", "models.json"])]
                ),
            ]
        );
        assert!(opened.sources.iter().all(|source| source.wsl.is_none()));
        assert_eq!(opened.session.len(), 32);
    }

    #[test]
    fn reads_only_the_checked_tools() {
        let home = fixture_home("checked");
        let imports = LocalImports::default();
        let opened = imports.open(locate(&homes(&home))).unwrap();
        let codex = opened
            .sources
            .iter()
            .find(|source| source.tool == Tool::Codex)
            .unwrap()
            .id
            .clone();
        assert!(imports.chosen(&opened.session, &[]).is_err());
        assert!(imports
            .chosen(&opened.session, &["s9".to_string()])
            .is_err());
        let chosen = imports.chosen(&opened.session, &[codex]).unwrap();

        let mut opened_files = Vec::new();
        let collected = collect(
            &chosen,
            &mut |path: &Path| {
                opened_files.push(path.to_path_buf());
                read_file(path)
            },
            &no_env,
            &[],
        );
        let codex_dir = home.join(".codex");
        assert_eq!(
            opened_files,
            vec![codex_dir.join("config.toml"), codex_dir.join("auth.json")]
        );
        assert!(collected.found.iter().all(|found| found.sources
            == [SourceRef {
                tool: Tool::Codex,
                wsl: None
            }]));
    }

    #[test]
    fn extracts_every_tool_and_merges_the_same_provider() {
        let home = fixture_home("extract");
        let collected = read_all(
            &home,
            &Tool::ALL,
            &env_of(&[("DEEPSEEK_API_KEY", "fake-deepseek-key-2041")]),
            &[8317],
        );
        let result = view(&collected);
        let summary: Vec<_> = result
            .candidates
            .iter()
            .map(|candidate| {
                (
                    candidate.name.as_deref(),
                    candidate.address.as_str(),
                    candidate.protocols.clone(),
                    candidate.models.clone(),
                    candidate.key.clone(),
                    candidate
                        .sources
                        .iter()
                        .map(|source| source.tool)
                        .collect::<Vec<_>>(),
                )
            })
            .collect();
        let hint = |key: &str| KeyView::Found {
            hint: key_hint(key),
        };
        assert_eq!(
            summary,
            vec![
                (
                    None,
                    "https://open.bigmodel.cn/api/anthropic",
                    vec!["anthropic.messages"],
                    vec!["glm-5.3".to_string(), "glm-5.3-flash".to_string()],
                    hint(CLAUDE_KEY),
                    vec![Tool::Claude],
                ),
                (
                    Some("Relay Hub"),
                    "https://api.relayhub.example/v1",
                    vec!["openai.responses", "openai.chat"],
                    vec![
                        "relay-large".to_string(),
                        "relay-small".to_string(),
                        "relay-chat".to_string(),
                    ],
                    hint(RELAY_KEY),
                    vec![Tool::Codex, Tool::Pi],
                ),
                (
                    Some("OpenAI"),
                    OFFICIAL_OPENAI,
                    vec!["openai.responses"],
                    vec!["gpt-5.5".to_string()],
                    hint(OPENAI_KEY),
                    vec![Tool::Codex],
                ),
                (
                    Some("deepseek"),
                    "https://api.deepseek.com/v1",
                    vec!["openai.chat"],
                    vec!["deepseek-chat".to_string(), "deepseek-reasoner".to_string()],
                    hint("fake-deepseek-key-2041"),
                    vec![Tool::Pi],
                ),
                (
                    Some("kimi"),
                    "https://api.kimi.com/coding",
                    vec!["anthropic.messages"],
                    Vec::new(),
                    hint(PI_KEY),
                    vec![Tool::Pi],
                ),
            ]
        );
        assert_eq!(
            result
                .skipped
                .iter()
                .map(|skip| (skip.reason, skip.tool, skip.name.as_deref()))
                .collect::<Vec<_>>(),
            vec![
                (SkipReason::Astrlink, Tool::Codex, Some("astrlink")),
                (SkipReason::Astrlink, Tool::Pi, Some("astrlink")),
            ]
        );

        // Nothing the dialog receives carries a key.
        let serialized = serde_json::to_string(&result).unwrap();
        for key in [
            CLAUDE_KEY,
            RELAY_KEY,
            OPENAI_KEY,
            PI_KEY,
            "fake-deepseek-key-2041",
        ] {
            assert!(!serialized.contains(key), "{key}");
        }
        assert!(!serialized.contains("astr_fake"));
    }

    #[test]
    fn source_files_stay_byte_identical() {
        let home = fixture_home("untouched");
        let files = [
            home.join(".claude").join("settings.json"),
            home.join(".codex").join("config.toml"),
            home.join(".codex").join("auth.json"),
            home.join(".pi").join("agent").join("models.json"),
        ];
        let before: Vec<_> = files.iter().map(|file| fs::read(file).unwrap()).collect();
        let modified: Vec<_> = files
            .iter()
            .map(|file| fs::metadata(file).unwrap().modified().unwrap())
            .collect();
        read_all(&home, &Tool::ALL, &no_env, &[]);
        let after: Vec<_> = files.iter().map(|file| fs::read(file).unwrap()).collect();
        assert_eq!(before, after);
        for (file, modified) in files.iter().zip(modified) {
            assert_eq!(fs::metadata(file).unwrap().modified().unwrap(), modified);
        }
        let mut names: Vec<_> = fs::read_dir(home.join(".codex"))
            .unwrap()
            .map(|entry| entry.unwrap().file_name())
            .collect();
        names.sort();
        assert_eq!(names, ["auth.json", "config.toml"]);
    }

    #[test]
    fn keys_behind_variables_and_commands_are_never_run_or_guessed() {
        let home = temp_home("indirect");
        let ran = home.join("ran");
        let command = format!("touch {}", ran.display());
        write(
            &home.join(".claude").join("settings.json"),
            &serde_json::to_string(&json!({
                "apiKeyHelper": command,
                "env": {"ANTHROPIC_BASE_URL": "https://relay.example.com"},
            }))
            .unwrap(),
        );
        write(
            &home.join(".codex").join("config.toml"),
            &format!(
                r#"[model_providers.env]
base_url = "https://env.example.com/v1"
env_key = "RELAY_KEY"

[model_providers.set]
base_url = "https://set.example.com/v1"
env_key = "SET_KEY"

[model_providers.command]
base_url = "https://command.example.com/v1"
auth = {{ command = "touch", args = ["{}"] }}

[model_providers.bare]
base_url = "https://bare.example.com/v1"
"#,
                ran.display().to_string().replace('\\', "\\\\")
            ),
        );
        write(
            &home.join(".pi").join("agent").join("models.json"),
            &serde_json::to_string(&json!({"providers": {
                "a-command": {"baseUrl": "https://a.example.com", "apiKey": format!("!{command}")},
                "b-template": {"baseUrl": "https://b.example.com", "apiKey": "sk-${PART_ONE}-$PART_TWO"},
                "c-missing": {"baseUrl": "https://c.example.com", "apiKey": "${NOT_SET}"},
                "d-bare": {"baseUrl": "https://d.example.com", "apiKey": "OLD_STYLE_KEY"},
                "e-escaped": {"baseUrl": "https://e.example.com", "apiKey": "$$literal$!key-1234"},
            }}))
            .unwrap(),
        );
        let env = env_of(&[
            ("SET_KEY", "fake-set-key-9999"),
            ("PART_ONE", "first"),
            ("PART_TWO", "second-5678"),
        ]);
        let collected = read_all(&home, &Tool::ALL, &env, &[]);
        assert!(!ran.exists(), "a key command ran");
        let keys: Vec<_> = collected
            .found
            .iter()
            .map(|found| (found.address.as_str(), found.key.view()))
            .collect();
        assert_eq!(
            keys,
            vec![
                ("https://relay.example.com", KeyView::Command),
                (
                    "https://env.example.com/v1",
                    KeyView::Env {
                        name: "RELAY_KEY".into()
                    }
                ),
                (
                    "https://set.example.com/v1",
                    KeyView::Found {
                        hint: Some("…9999".into())
                    }
                ),
                ("https://command.example.com/v1", KeyView::Command),
                ("https://bare.example.com/v1", KeyView::Missing),
                ("https://a.example.com", KeyView::Command),
                (
                    "https://b.example.com",
                    KeyView::Found {
                        hint: Some("…5678".into())
                    }
                ),
                (
                    "https://c.example.com",
                    KeyView::Env {
                        name: "NOT_SET".into()
                    }
                ),
                (
                    "https://d.example.com",
                    KeyView::Env {
                        name: "OLD_STYLE_KEY".into()
                    }
                ),
                (
                    "https://e.example.com",
                    KeyView::Found {
                        hint: Some("…1234".into())
                    }
                ),
            ]
        );
        let Key::Found(template) = &collected.found[6].key else {
            panic!("template key");
        };
        assert_eq!(template.as_str(), "sk-first-second-5678");
        let Key::Found(escaped) = &collected.found[9].key else {
            panic!("escaped key");
        };
        assert_eq!(escaped.as_str(), "$literal!key-1234");
    }

    #[test]
    fn skips_astrlink_itself_and_chatgpt_sign_in() {
        let home = temp_home("astrlink");
        write(
            &home.join(".claude").join("settings.json"),
            r#"{"env": {"ANTHROPIC_BASE_URL": "http://localhost:8317", "ANTHROPIC_AUTH_TOKEN": "astr_fake_token_value"}}"#,
        );
        write(
            &home.join(".codex").join("config.toml"),
            r#"[model_providers.gateway]
base_url = "http://127.0.0.1:8317/v1"
experimental_bearer_token = "fake-not-astr-key-1"

[model_providers.local]
base_url = "http://127.0.0.1:3425/v1"
experimental_bearer_token = "fake-local-key-2222"
"#,
        );
        write(
            &home.join(".codex").join("auth.json"),
            &format!(
                r#"{{"OPENAI_API_KEY": "{OPENAI_KEY}", "tokens": {{"id_token": "fake", "access_token": "fake"}}}}"#
            ),
        );
        let collected = read_all(&home, &Tool::ALL, &no_env, &[8317]);
        assert_eq!(
            collected
                .found
                .iter()
                .map(|found| found.address.as_str())
                .collect::<Vec<_>>(),
            ["http://127.0.0.1:3425/v1"]
        );
        assert_eq!(
            collected
                .skipped
                .iter()
                .map(|skip| (skip.reason, skip.tool, skip.name.as_deref()))
                .collect::<Vec<_>>(),
            vec![
                (SkipReason::Astrlink, Tool::Claude, Some("localhost:8317")),
                (SkipReason::ChatgptLogin, Tool::Codex, None),
                (SkipReason::Astrlink, Tool::Codex, Some("gateway")),
                (SkipReason::Missing, Tool::Pi, None),
            ]
        );
        // The ChatGPT sign-in's API key is not offered as an OpenAI key.
        assert!(!serde_json::to_string(&view(&collected))
            .unwrap()
            .contains(OPENAI_KEY));
    }

    #[test]
    fn reports_missing_unreadable_and_invalid_files() {
        let home = temp_home("broken");
        write(&home.join(".claude").join("settings.json"), "{\"env\": ");
        write(&home.join(".codex").join("config.toml"), "model = [");
        fs::write(home.join(".codex").join("auth.json"), [0xff, 0xfe, 0x00]).unwrap();
        write(
            &home.join(".pi").join("agent").join("models.json"),
            &format!(
                r#"{{"providers": {{
                    "a-bad": {{"baseUrl": "https://user:{PI_KEY}@relay.example.com", "apiKey": "x"}},
                    "b-vertex": {{"baseUrl": "https://v.example.com", "api": "google-vertex"}},
                    "c-radius": {{"baseUrl": "https://r.example.com", "oauth": "radius"}},
                }}}}"#
            ),
        );
        let collected = read_all(&home, &Tool::ALL, &no_env, &[]);
        assert!(collected.found.is_empty());
        let separator = if cfg!(windows) { "\\" } else { "/" };
        let shown = |parts: &[&str]| format!("~{separator}{}", parts.join(separator));
        assert_eq!(
            collected
                .skipped
                .iter()
                .map(|skip| (
                    skip.reason,
                    skip.name.as_deref(),
                    skip.path.clone(),
                    skip.detail.clone()
                ))
                .collect::<Vec<_>>(),
            vec![
                (
                    SkipReason::Invalid,
                    None,
                    Some(shown(&[".claude", "settings.json"])),
                    Some("is not valid JSON (line 1, column 8)".into())
                ),
                (
                    SkipReason::Invalid,
                    None,
                    Some(shown(&[".codex", "config.toml"])),
                    Some("is not valid TOML".into())
                ),
                (
                    SkipReason::Unreadable,
                    None,
                    Some(shown(&[".codex", "auth.json"])),
                    Some("not_text".into())
                ),
                (
                    SkipReason::Unsupported,
                    Some("b-vertex"),
                    None,
                    Some("google-vertex".into())
                ),
                (SkipReason::SignIn, Some("c-radius"), None, None),
                (SkipReason::InvalidAddress, Some("a-bad"), None, None),
            ]
        );
        assert!(!serde_json::to_string(&collected.skipped)
            .unwrap()
            .contains(PI_KEY));

        let empty = temp_home("empty");
        let missing = read_all(&empty, &Tool::ALL, &no_env, &[]);
        assert_eq!(
            missing
                .skipped
                .iter()
                .map(|skip| (skip.reason, skip.path.clone()))
                .collect::<Vec<_>>(),
            vec![
                (
                    SkipReason::Missing,
                    Some(shown(&[".claude", "settings.json"]))
                ),
                (SkipReason::Missing, Some(shown(&[".codex", "config.toml"]))),
                (
                    SkipReason::Missing,
                    Some(shown(&[".pi", "agent", "models.json"]))
                ),
            ]
        );
    }

    #[test]
    fn merges_only_the_same_address_and_key() {
        let mut found = Vec::new();
        let place = SourceRef {
            tool: Tool::Claude,
            wsl: None,
        };
        let wsl = SourceRef {
            tool: Tool::Codex,
            wsl: Some("Ubuntu".into()),
        };
        for (address, key, place) in [
            ("https://Relay.example.com", secret("fake-key-aaaa"), &place),
            (
                "https://relay.example.com/v1/",
                secret("fake-key-aaaa"),
                &wsl,
            ),
            (
                "https://relay.example.com/v1",
                secret("fake-key-bbbb"),
                &wsl,
            ),
            (
                "https://relay.example.com:8443/v1",
                secret("fake-key-aaaa"),
                &wsl,
            ),
            ("http://relay.example.com/v1", secret("fake-key-aaaa"), &wsl),
            (
                "https://relay.example.com/claude",
                secret("fake-key-aaaa"),
                &wsl,
            ),
        ] {
            let entry = Entry {
                name: None,
                address: address.trim_end_matches('/').into(),
                protocol: None,
                models: Vec::new(),
                key,
            };
            let url = Url::parse(address).unwrap();
            merge(&mut found, entry, &url, place);
        }
        assert_eq!(
            found
                .iter()
                .map(|found| (found.normalized.as_str(), found.sources.len()))
                .collect::<Vec<_>>(),
            vec![
                ("https://relay.example.com", 2),
                ("https://relay.example.com", 1),
                ("https://relay.example.com:8443", 1),
                ("http://relay.example.com", 1),
                ("https://relay.example.com/claude", 1),
            ]
        );
    }

    #[test]
    fn attaches_the_key_only_to_the_same_origin() {
        let provider = found("https://open.bigmodel.cn/api/anthropic", secret(CLAUDE_KEY));
        let input = json!({
            "name": "GLM",
            "kind": "glm_coding",
            "enabled": true,
            "models": ["glm-5.3"],
            "http": {
                "base_url": "https://open.bigmodel.cn/api/coding/paas/v4",
                "auth": {"scheme": "bearer"},
            },
            "capabilities": [],
        });
        let create = ["name", "kind", "enabled", "models", "http", "capabilities"];
        let (authorized, key) = authorize(&provider, input.clone(), &create).unwrap();
        assert_eq!(
            authorized["http"]["credential"],
            json!({"secret": CLAUDE_KEY})
        );
        assert_eq!(key.as_str(), CLAUDE_KEY);

        for (pointer, value) in [
            ("/http/base_url", json!("https://evil.example.com/api")),
            ("/http/base_url", json!("http://open.bigmodel.cn/api")),
            ("/http/base_url", json!("https://open.bigmodel.cn:444/api")),
            ("/http/base_url", json!("https://user@open.bigmodel.cn/api")),
            ("/http/auth", json!({"scheme": "none"})),
            (
                "/http/auth",
                json!({"scheme": "custom_header", "header_name": "X-Key"}),
            ),
            ("/kind", json!("codex_subscription")),
            ("/kind", json!("vertex")),
            ("/http/credential", json!({"secret": "other"})),
            ("/http/headers", json!({"X-Echo": "1"})),
            ("/proxy", json!({"mode": "none"})),
        ] {
            let mut changed = input.clone();
            match changed.pointer_mut(pointer) {
                Some(slot) => *slot = value,
                None => {
                    let (parent, name) = pointer.rsplit_once('/').unwrap();
                    let parent = if parent.is_empty() {
                        &mut changed
                    } else {
                        changed.pointer_mut(parent).unwrap()
                    };
                    parent[name] = value;
                }
            }
            let error = authorize(&provider, changed, &create).unwrap_err();
            assert!(!error.contains(CLAUDE_KEY), "{pointer}");
        }

        // A provider whose key AstrLink cannot read takes the pasted one.
        let pending = found(
            "https://api.deepseek.com/v1",
            Key::Env("DEEPSEEK_API_KEY".into()),
        );
        let mut pasted = input.clone();
        pasted["kind"] = json!("deepseek");
        pasted["http"]["base_url"] = json!("https://api.deepseek.com/v1");
        assert_eq!(
            authorize(&pending, pasted.clone(), &create).unwrap_err(),
            "paste the key first"
        );
        pasted["http"]["credential"] = json!({"secret": "fake-pasted-key-7777"});
        let (_, key) = authorize(&pending, pasted.clone(), &create).unwrap();
        assert_eq!(key.as_str(), "fake-pasted-key-7777");
        pasted["http"]["credential"] = json!({"secret": "a\nb"});
        assert!(authorize(&pending, pasted, &create).is_err());

        // The model-list request takes no other fields.
        let probe = json!({
            "kind": "glm_coding",
            "protocol": "openai.models",
            "http": {"base_url": "https://open.bigmodel.cn/api/coding/paas/v4", "auth": {"scheme": "bearer"}},
        });
        assert!(authorize(&provider, probe.clone(), &["kind", "http", "protocol"]).is_ok());
        let mut with_service = probe;
        with_service["service_id"] = json!("svc_other");
        assert!(authorize(&provider, with_service, &["kind", "http", "protocol"]).is_err());
    }

    #[test]
    fn redacts_a_key_core_echoes() {
        let error = format!("upstream said: bad key {RELAY_KEY}");
        assert_eq!(redact(error, Some(RELAY_KEY)), "unable to add the service");
    }

    fn session_with(imports: &LocalImports, provider: Found) -> String {
        let opened = imports.open(Vec::new()).unwrap();
        imports
            .store(
                &opened.session,
                Collected {
                    found: vec![provider],
                    skipped: Vec::new(),
                },
            )
            .unwrap();
        opened.session
    }

    #[test]
    fn creates_each_provider_once_and_probes_it_once() {
        let imports = LocalImports::default();
        let session = session_with(
            &imports,
            found("https://api.relayhub.example/v1", secret(RELAY_KEY)),
        );
        let input = json!({
            "name": "Relay",
            "kind": "newapi",
            "models": ["relay-large"],
            "http": {"base_url": "https://api.relayhub.example/v1", "auth": {"scheme": "bearer"}},
            "capabilities": [],
        });
        let probe = json!({
            "kind": "newapi",
            "protocol": "openai.models",
            "http": {"base_url": "https://api.relayhub.example/v1", "auth": {"scheme": "bearer"}},
        });
        assert!(imports.begin_probe(&session, "c0", probe.clone()).is_ok());
        assert_eq!(
            imports
                .begin_probe(&session, "c0", probe.clone())
                .unwrap_err(),
            "the model list was already requested"
        );
        assert!(imports
            .begin_confirm(&session, "c9", input.clone())
            .is_err());
        assert!(imports.begin_confirm(&session, "c0", input.clone()).is_ok());
        assert_eq!(
            imports
                .begin_confirm(&session, "c0", input.clone())
                .unwrap_err(),
            "this provider is being imported"
        );
        imports.finish_confirm(&session, "c0", false);
        assert!(imports.begin_confirm(&session, "c0", input.clone()).is_ok());
        imports.finish_confirm(&session, "c0", true);
        assert_eq!(
            imports.begin_confirm(&session, "c0", input).unwrap_err(),
            "this provider was already imported"
        );
        let slot = imports.lock();
        assert!(matches!(slot.as_ref().unwrap().found[0].key, Key::Missing));
    }

    #[test]
    fn dismissing_or_idling_drops_the_keys() {
        let imports = LocalImports::default();
        let session = session_with(
            &imports,
            found("https://api.relayhub.example/v1", secret(RELAY_KEY)),
        );
        imports.dismiss("someone-else");
        assert!(imports.lock().is_some());
        imports.dismiss(&session);
        assert!(imports.lock().is_none());
        assert_eq!(
            imports.chosen(&session, &["s0".into()]).unwrap_err(),
            CLOSED
        );

        // Before a read the session holds no keys and never times out.
        let idle = LocalImports::with_idle(Duration::ZERO);
        let opened = idle.open(Vec::new()).unwrap();
        assert!(idle
            .chosen(&opened.session, &[])
            .is_err_and(|error| error != CLOSED));
        idle.store(
            &opened.session,
            Collected {
                found: vec![found("https://a.example.com", secret(RELAY_KEY))],
                skipped: Vec::new(),
            },
        )
        .unwrap();
        assert_eq!(
            idle.begin_probe(&opened.session, "c0", json!({}))
                .unwrap_err(),
            CLOSED
        );
        assert!(idle.lock().is_none());

        // A newer dialog replaces the older one and its keys.
        let replaced = session_with(&imports, found("https://a.example.com", secret(RELAY_KEY)));
        imports.open(Vec::new()).unwrap();
        assert_eq!(
            imports.begin_probe(&replaced, "c0", json!({})).unwrap_err(),
            CLOSED
        );
    }

    #[test]
    fn gateway_ports_come_from_the_ready_announcement() {
        let snapshot = CoreSnapshot {
            phase: crate::sidecar::CorePhase::Ready,
            pid: None,
            ready: Some(crate::sidecar::ReadyAnnouncement {
                event: "ready".into(),
                core_version: "0".into(),
                control_api_version: "v1".into(),
                protocol_contract_version: "v1".into(),
                inference_url: "http://127.0.0.1:18318".into(),
                client_inference_url: "http://localhost:18318".into(),
                control_url: "http://127.0.0.1:1".into(),
            }),
            health: None,
            version: None,
            capabilities: None,
            last_error: None,
            inference_port_fallback: None,
            inference_listen_active: None,
            restart_pending: false,
            recovery_attempt: 0,
            recovery_scheduled_in_ms: None,
        };
        assert_eq!(gateway_ports(&snapshot), [18318, 18318]);
        for (address, local) in [
            ("http://localhost:1", true),
            ("http://app.localhost:1", true),
            ("http://127.1.2.3:1", true),
            ("http://[::1]:1", true),
            ("http://0.0.0.0:1", true),
            ("http://192.168.1.2:1", false),
            ("https://localhost.example.com", false),
        ] {
            assert_eq!(loopback(&Url::parse(address).unwrap()), local, "{address}");
        }
    }
}
