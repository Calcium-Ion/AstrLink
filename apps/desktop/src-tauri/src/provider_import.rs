//! One-click provider import through `astrlink://v1/providers/import` links.
//!
//! A link's API key never reaches the webview: the dialog sees a hint and
//! confirms by id, and the key is attached to the create request here.

use std::collections::HashSet;
use std::fmt::Write as _;
use std::sync::{Arc, Mutex, MutexGuard};

use reqwest::Url;
use serde::Serialize;
use serde_json::Value;
use sha2::{Digest, Sha256};
use tauri::{AppHandle, Emitter, Manager, State};
use zeroize::Zeroizing;

use crate::sidecar::{CoreManager, ServiceRecordResponse};

pub const EVENT: &str = "provider-import";

// Also bounded by the Windows command line that carries the link.
const MAX_LINK_BYTES: usize = 32 * 1024;
const MAX_BASE_URL_BYTES: usize = 2_048;
const MAX_NAME_CHARS: usize = 128;
const MAX_MODELS: usize = 2_000;
const MAX_MODEL_CHARS: usize = 256;
const MAX_PROTOCOLS: usize = 32;
const MAX_HEADER_NAME_BYTES: usize = 128;
pub(crate) const MAX_SECRET_BYTES: usize = 16_384;
const AUTH_SCHEMES: [&str; 5] = [
    "none",
    "bearer",
    "anthropic_api_key",
    "google_api_key",
    "custom_header",
];
const PARAMETERS: [&str; 8] = [
    "kind",
    "name",
    "base_url",
    "api_key",
    "auth",
    "auth_header",
    "protocols",
    "models",
];

/// The link's provider as the confirmation dialog shows it. Empty protocol and
/// model lists fall back to the kind's defaults.
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct Provider {
    kind: String,
    name: Option<String>,
    base_url: String,
    auth: Option<String>,
    auth_header: Option<String>,
    protocols: Vec<String>,
    models: Vec<String>,
    has_api_key: bool,
    api_key_hint: Option<String>,
}

#[derive(Clone, Copy, Debug, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Reason {
    Malformed,
    Unsupported,
    UnknownParameter,
    DuplicateParameter,
    MissingParameter,
    InvalidParameter,
}

#[derive(Debug, PartialEq)]
struct Rejection {
    reason: Reason,
    field: Option<String>,
}

fn reject(reason: Reason, field: Option<&str>) -> Rejection {
    Rejection {
        reason,
        field: field.map(str::to_string),
    }
}

#[derive(Clone, Debug, Serialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub enum Notice {
    Pending {
        id: String,
        provider: Provider,
    },
    Invalid {
        id: String,
        reason: Reason,
        field: Option<String>,
    },
}

impl Notice {
    fn id(&self) -> &str {
        match self {
            Self::Pending { id, .. } | Self::Invalid { id, .. } => id,
        }
    }
}

struct Link {
    provider: Provider,
    api_key: Option<Zeroizing<String>>,
}

struct Entry {
    digest: [u8; 32],
    notice: Notice,
    api_key: Option<Zeroizing<String>>,
}

/// The latest link waiting for the user; a newer link replaces it.
#[derive(Default)]
pub struct ProviderImports(Mutex<Option<Entry>>);

impl ProviderImports {
    fn slot(&self) -> MutexGuard<'_, Option<Entry>> {
        self.0.lock().unwrap_or_else(|error| error.into_inner())
    }

    fn accept(&self, raw: &str) -> Result<Notice, String> {
        let digest: [u8; 32] = Sha256::digest(raw.as_bytes()).into();
        let mut slot = self.slot();
        // The same click can arrive both at launch and as an open event.
        if let Some(entry) = slot.as_ref().filter(|entry| entry.digest == digest) {
            return Ok(entry.notice.clone());
        }
        let id = new_id()?;
        let (notice, api_key) = match parse(raw) {
            Ok(link) => (
                Notice::Pending {
                    id,
                    provider: link.provider,
                },
                link.api_key,
            ),
            Err(rejection) => (
                Notice::Invalid {
                    id,
                    reason: rejection.reason,
                    field: rejection.field,
                },
                None,
            ),
        };
        *slot = Some(Entry {
            digest,
            notice: notice.clone(),
            api_key,
        });
        Ok(notice)
    }

    fn pending(&self) -> Option<Notice> {
        self.slot().as_ref().map(|entry| entry.notice.clone())
    }

    fn dismiss(&self, id: &str) {
        let mut slot = self.slot();
        if slot.as_ref().is_some_and(|entry| entry.notice.id() == id) {
            *slot = None;
        }
    }

    /// Removes the entry while it is being created so a second confirm cannot
    /// add the provider twice.
    fn take(&self, id: &str) -> Option<Entry> {
        let mut slot = self.slot();
        let matches = slot.as_ref().is_some_and(|entry| {
            entry.notice.id() == id && matches!(entry.notice, Notice::Pending { .. })
        });
        if matches {
            slot.take()
        } else {
            None
        }
    }

    fn restore(&self, entry: Entry) {
        let mut slot = self.slot();
        if slot.is_none() {
            *slot = Some(entry);
        }
    }
}

pub(crate) fn new_id() -> Result<String, String> {
    let mut random = [0_u8; 16];
    getrandom::getrandom(&mut random)
        .map_err(|error| format!("unable to generate provider import id: {error}"))?;
    let mut id = String::with_capacity(random.len() * 2);
    for byte in random {
        write!(&mut id, "{byte:02x}").expect("writing to a String cannot fail");
    }
    Ok(id)
}

/// Accepts a link the OS opened and brings the confirmation to the front.
pub fn receive(app: &AppHandle, raw: &str) {
    let notice = match app.state::<ProviderImports>().accept(raw) {
        Ok(notice) => notice,
        Err(error) => {
            eprintln!("unable to accept provider import link: {error}");
            return;
        }
    };
    crate::show_main_window(app);
    if let Err(error) = app.emit_to("main", EVENT, notice) {
        eprintln!("unable to show provider import link: {error}");
    }
}

fn parse(raw: &str) -> Result<Link, Rejection> {
    if raw.len() > MAX_LINK_BYTES {
        return Err(reject(Reason::Malformed, None));
    }
    let url = Url::parse(raw).map_err(|_| reject(Reason::Malformed, None))?;
    if url.scheme() != crate::instance::link_scheme()
        || url.host_str() != Some("v1")
        || url.path() != "/providers/import"
        || url.port().is_some()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.fragment().is_some()
    {
        return Err(reject(Reason::Unsupported, None));
    }

    let mut seen = HashSet::new();
    let mut values: [Option<Zeroizing<String>>; PARAMETERS.len()] = Default::default();
    for (key, value) in url.query_pairs() {
        let Some(index) = PARAMETERS.iter().position(|name| *name == key) else {
            // Echo only a plain name; the rest of the link is untrusted.
            let echo = (key.len() <= 64
                && key
                    .bytes()
                    .all(|byte| byte.is_ascii_alphanumeric() || b"_-.".contains(&byte)))
            .then_some(key.as_ref());
            return Err(reject(Reason::UnknownParameter, echo));
        };
        if !seen.insert(index) {
            return Err(reject(Reason::DuplicateParameter, Some(PARAMETERS[index])));
        }
        // Empty values count as absent: link builders often emit every field.
        if !value.trim().is_empty() {
            values[index] = Some(Zeroizing::new(value.into_owned()));
        }
    }
    let [kind, name, base_url, api_key, auth, auth_header, protocols, models] = values;

    let kind = kind.ok_or_else(|| reject(Reason::MissingParameter, Some("kind")))?;
    let kind = kind.trim();
    if kind.len() > 64
        || !kind.starts_with(|c: char| c.is_ascii_lowercase())
        || !kind
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'_')
    {
        return Err(reject(Reason::InvalidParameter, Some("kind")));
    }
    // Subscriptions sign in through their own flow and never take an API key.
    if kind.ends_with("_subscription") {
        return Err(reject(Reason::Unsupported, Some("kind")));
    }

    let base_url = base_url.ok_or_else(|| reject(Reason::MissingParameter, Some("base_url")))?;
    let base_url = base_url.trim();
    if !valid_base_url(base_url) {
        return Err(reject(Reason::InvalidParameter, Some("base_url")));
    }

    let name = name.map(|name| name.trim().to_string());
    if name
        .as_deref()
        .is_some_and(|name| name.chars().count() > MAX_NAME_CHARS || !plain_text(name))
    {
        return Err(reject(Reason::InvalidParameter, Some("name")));
    }

    let auth = auth.map(|auth| auth.trim().to_string());
    if auth
        .as_deref()
        .is_some_and(|auth| !AUTH_SCHEMES.contains(&auth))
    {
        return Err(reject(Reason::InvalidParameter, Some("auth")));
    }
    let custom_header = auth.as_deref() == Some("custom_header");
    let auth_header = auth_header.map(|header| header.trim().to_string());
    match auth_header.as_deref() {
        None if custom_header => {
            return Err(reject(Reason::MissingParameter, Some("auth_header")));
        }
        Some(header) if !custom_header || !valid_header_name(header) => {
            return Err(reject(Reason::InvalidParameter, Some("auth_header")));
        }
        _ => {}
    }

    if let Some(key) = &api_key {
        if key.len() > MAX_SECRET_BYTES || !plain_text(key) || auth.as_deref() == Some("none") {
            return Err(reject(Reason::InvalidParameter, Some("api_key")));
        }
    }

    let protocols = list(
        protocols.as_deref().map(String::as_str),
        MAX_PROTOCOLS,
        valid_protocol_id,
    )
    .ok_or_else(|| reject(Reason::InvalidParameter, Some("protocols")))?;
    let models = list(models.as_deref().map(String::as_str), MAX_MODELS, |model| {
        model.chars().count() <= MAX_MODEL_CHARS && plain_text(model)
    })
    .ok_or_else(|| reject(Reason::InvalidParameter, Some("models")))?;

    Ok(Link {
        provider: Provider {
            kind: kind.to_string(),
            name,
            base_url: base_url.to_string(),
            auth,
            auth_header,
            protocols,
            models,
            has_api_key: api_key.is_some(),
            api_key_hint: api_key.as_deref().and_then(|key| key_hint(key)),
        },
        api_key,
    })
}

pub(crate) fn plain_text(value: &str) -> bool {
    !value
        .chars()
        .any(|c| c.is_control() || c == char::REPLACEMENT_CHARACTER)
}

pub(crate) fn valid_base_url(value: &str) -> bool {
    if value.len() > MAX_BASE_URL_BYTES {
        return false;
    }
    let Ok(url) = Url::parse(value) else {
        return false;
    };
    matches!(url.scheme(), "http" | "https")
        && url.host_str().is_some_and(|host| !host.is_empty())
        && url.username().is_empty()
        && url.password().is_none()
        && url.query().is_none()
        && url.fragment().is_none()
}

fn valid_header_name(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= MAX_HEADER_NAME_BYTES
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"!#$%&'*+.^_`|~-".contains(&byte))
}

/// Matches Core's protocol ids such as `openai.chat` or `openai.responses.compact`.
fn valid_protocol_id(value: &str) -> bool {
    value.len() <= 64
        && value.starts_with(|c: char| c.is_ascii_lowercase())
        && value.split(['.', '_', '-']).all(|part| {
            !part.is_empty()
                && part
                    .bytes()
                    .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit())
        })
}

/// A comma-separated list; blank entries are skipped and duplicates rejected.
fn list(value: Option<&str>, limit: usize, valid: impl Fn(&str) -> bool) -> Option<Vec<String>> {
    let mut items = Vec::new();
    let mut seen = HashSet::new();
    for item in value.unwrap_or_default().split(',').map(str::trim) {
        if item.is_empty() {
            continue;
        }
        if items.len() == limit || !valid(item) || !seen.insert(item) {
            return None;
        }
        items.push(item.to_string());
    }
    Some(items)
}

/// Core's credential hint: the last four characters of a key of twelve or more.
pub(crate) fn key_hint(key: &str) -> Option<String> {
    let count = key.chars().count();
    (count >= 12).then(|| format!("…{}", key.chars().skip(count - 4).collect::<String>()))
}

/// Checks that the dialog creates what the link described, then attaches the
/// link's key. Kind and address must match so the key only reaches that host.
fn authorize(entry: &Entry, mut input: Value) -> Result<Value, String> {
    let Notice::Pending { provider, .. } = &entry.notice else {
        return Err("this link cannot add a service".into());
    };
    let object = input.as_object_mut().ok_or("invalid service input")?;
    if object.get("kind").and_then(Value::as_str) != Some(provider.kind.as_str()) {
        return Err("the service type does not match the link".into());
    }
    let http = object
        .get_mut("http")
        .and_then(Value::as_object_mut)
        .ok_or("invalid service input")?;
    if http.get("base_url").and_then(Value::as_str) != Some(provider.base_url.as_str()) {
        return Err("the service address does not match the link".into());
    }
    let auth = http.get("auth").ok_or("invalid service input")?;
    let scheme = auth
        .get("scheme")
        .and_then(Value::as_str)
        .ok_or("invalid service input")?;
    if let Some(expected) = &provider.auth {
        if scheme != expected
            || auth.get("header_name").and_then(Value::as_str) != provider.auth_header.as_deref()
        {
            return Err("the sign-in method does not match the link".into());
        }
    }
    if let Some(key) = &entry.api_key {
        if scheme == "none" || http.contains_key("credential") {
            return Err("the link's API key cannot be replaced".into());
        }
        http.insert(
            "credential".into(),
            serde_json::json!({ "secret": key.as_str() }),
        );
    }
    Ok(input)
}

/// Keeps the link's key out of an error Core may have echoed.
pub(crate) fn redact(error: String, api_key: Option<&str>) -> String {
    match api_key {
        Some(key) if key.len() >= 8 && error.contains(key) => "unable to add the service".into(),
        _ => error,
    }
}

#[tauri::command]
pub fn pending_provider_import(imports: State<'_, ProviderImports>) -> Option<Notice> {
    imports.pending()
}

#[tauri::command]
pub fn dismiss_provider_import(id: String, imports: State<'_, ProviderImports>) {
    imports.dismiss(&id);
}

#[tauri::command]
pub async fn confirm_provider_import(
    id: String,
    input: Value,
    imports: State<'_, ProviderImports>,
    manager: State<'_, Arc<CoreManager>>,
) -> Result<ServiceRecordResponse, String> {
    let entry = imports
        .take(&id)
        .ok_or("this link is no longer waiting; open it again")?;
    let result = match authorize(&entry, input) {
        Ok(input) => manager.create_service(input).await,
        Err(error) => Err(error),
    };
    result.map_err(|error| {
        let error = redact(error, entry.api_key.as_deref().map(String::as_str));
        imports.restore(entry);
        error
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    const BASE: &str = "astrlink://v1/providers/import";

    fn url_for(query: &str) -> String {
        format!("{BASE}?{query}")
    }

    fn rejection(query: &str) -> Rejection {
        match parse(&url_for(query)) {
            Ok(_) => panic!("{query} should be rejected"),
            Err(rejection) => rejection,
        }
    }

    fn entry(query: &str) -> Entry {
        let link = parse(&url_for(query)).unwrap();
        Entry {
            digest: [0; 32],
            notice: Notice::Pending {
                id: "id".into(),
                provider: link.provider,
            },
            api_key: link.api_key,
        }
    }

    #[test]
    fn parses_encoded_values_and_keeps_the_key_out_of_the_notice() {
        let link = parse(&url_for(
            "kind=openai_compatible&name=%E4%B8%AD%E8%BD%AC%20%26%20Co&base_url=https%3A%2F%2Fapi.example.com%2Fv1\
             &api_key=sk-test%2Bkey%26%3D1234&auth=custom_header&auth_header=X-Api-Key\
             &protocols=openai.chat,%20openai.models,&models=gpt-5,team%2Fmodel%20a",
        ))
        .unwrap();
        assert_eq!(
            link.provider,
            Provider {
                kind: "openai_compatible".into(),
                name: Some("中转 & Co".into()),
                base_url: "https://api.example.com/v1".into(),
                auth: Some("custom_header".into()),
                auth_header: Some("X-Api-Key".into()),
                protocols: vec!["openai.chat".into(), "openai.models".into()],
                models: vec!["gpt-5".into(), "team/model a".into()],
                has_api_key: true,
                api_key_hint: Some("…1234".into()),
            }
        );
        assert_eq!(link.api_key.as_deref().unwrap(), "sk-test+key&=1234");
        let notice = serde_json::to_string(&Notice::Pending {
            id: "id".into(),
            provider: link.provider,
        })
        .unwrap();
        assert!(!notice.contains("sk-test"));
    }

    #[test]
    fn optional_fields_fall_back_and_blank_values_count_as_absent() {
        let link = parse(&url_for(
            "kind=deepseek&base_url=http://192.168.1.2:3000&name=&api_key=%20&models=,",
        ))
        .unwrap();
        assert_eq!(link.provider.name, None);
        assert_eq!(link.provider.base_url, "http://192.168.1.2:3000");
        assert_eq!(link.provider.auth, None);
        assert!(link.provider.protocols.is_empty());
        assert!(link.provider.models.is_empty());
        assert!(!link.provider.has_api_key);
        assert!(link.api_key.is_none());
        // Short keys get no hint, as in Core.
        let short = parse(&url_for(
            "kind=deepseek&base_url=https://a.example&api_key=short",
        ))
        .unwrap();
        assert!(short.provider.has_api_key);
        assert_eq!(short.provider.api_key_hint, None);
    }

    #[test]
    fn rejects_other_links_and_parameters() {
        for raw in [
            "astrlink://v1/providers/import/?kind=openai&base_url=https://a.example",
            "astrlink://v2/providers/import?kind=openai&base_url=https://a.example",
            "astrlink://v1/providers/export?kind=openai&base_url=https://a.example",
            "astrlink://v1/providers/import?kind=openai&base_url=https://a.example#x",
            "ccswitch://v1/providers/import?kind=openai&base_url=https://a.example",
        ] {
            assert_eq!(
                parse(raw).err(),
                Some(reject(Reason::Unsupported, None)),
                "{raw}"
            );
        }
        let long = url_for(&format!(
            "kind=openai&base_url=https://a.example&name={}",
            "a".repeat(MAX_LINK_BYTES)
        ));
        assert_eq!(parse(&long).err(), Some(reject(Reason::Malformed, None)));
        assert_eq!(
            rejection("kind=openai&base_url=https://a.example&apiKey=x"),
            reject(Reason::UnknownParameter, Some("apiKey"))
        );
        assert_eq!(
            rejection("kind=openai&base_url=https://a.example&%3Cb%3E=x"),
            reject(Reason::UnknownParameter, None)
        );
        assert_eq!(
            rejection("kind=openai&base_url=https://a.example&base_url=https://b.example"),
            reject(Reason::DuplicateParameter, Some("base_url"))
        );
        assert_eq!(
            rejection("base_url=https://a.example"),
            reject(Reason::MissingParameter, Some("kind"))
        );
        assert_eq!(
            rejection("kind=openai&base_url="),
            reject(Reason::MissingParameter, Some("base_url"))
        );
        assert_eq!(
            rejection("kind=codex_subscription&base_url=https://a.example"),
            reject(Reason::Unsupported, Some("kind"))
        );
    }

    #[test]
    fn rejects_invalid_values() {
        let base = "kind=openai&base_url=https://a.example";
        let long_model = "m".repeat(MAX_MODEL_CHARS + 1);
        let many_models = (0..=MAX_MODELS)
            .map(|index| format!("m{index}"))
            .collect::<Vec<_>>()
            .join(",");
        for (query, field) in [
            ("kind=OpenAI&base_url=https://a.example".to_string(), "kind"),
            ("kind=openai&base_url=ftp://a.example".into(), "base_url"),
            ("kind=openai&base_url=/v1".into(), "base_url"),
            (
                "kind=openai&base_url=https://user:pass@a.example".into(),
                "base_url",
            ),
            (
                "kind=openai&base_url=https://a.example/v1%3Fkey%3D1".into(),
                "base_url",
            ),
            (
                "kind=openai&base_url=https://a.example/v1%23top".into(),
                "base_url",
            ),
            (format!("{base}&name=a%0Ab"), "name"),
            (
                format!("{base}&name={}", "n".repeat(MAX_NAME_CHARS + 1)),
                "name",
            ),
            (format!("{base}&auth=basic"), "auth"),
            (
                format!("{base}&auth=bearer&auth_header=X-Key"),
                "auth_header",
            ),
            (format!("{base}&auth_header=X-Key"), "auth_header"),
            (
                format!("{base}&auth=custom_header&auth_header=X%20Key"),
                "auth_header",
            ),
            (format!("{base}&auth=none&api_key=sk-test"), "api_key"),
            (format!("{base}&api_key=sk%0Dtest"), "api_key"),
            (format!("{base}&protocols=OpenAI.Chat"), "protocols"),
            (format!("{base}&protocols=openai..chat"), "protocols"),
            (format!("{base}&models=a,a"), "models"),
            (format!("{base}&models={long_model}"), "models"),
            (format!("{base}&models={many_models}"), "models"),
        ] {
            assert_eq!(
                rejection(&query),
                reject(Reason::InvalidParameter, Some(field)),
                "{query}"
            );
        }
        assert_eq!(
            rejection(&format!("{base}&auth=custom_header")),
            reject(Reason::MissingParameter, Some("auth_header"))
        );
    }

    #[test]
    fn a_repeated_link_keeps_its_id_and_a_new_one_replaces_it() {
        let imports = ProviderImports::default();
        let first = url_for("kind=openai&base_url=https://a.example&api_key=sk-test");
        let id = imports.accept(&first).unwrap().id().to_string();
        assert_eq!(imports.accept(&first).unwrap().id(), id);

        let invalid = imports.accept(&url_for("kind=openai")).unwrap();
        assert!(matches!(
            invalid,
            Notice::Invalid {
                reason: Reason::MissingParameter,
                ..
            }
        ));
        assert_ne!(invalid.id(), id);
        assert_eq!(imports.pending().unwrap().id(), invalid.id());
        assert!(imports.take(invalid.id()).is_none());

        imports.dismiss(invalid.id());
        assert!(imports.pending().is_none());
        let again = imports.accept(&first).unwrap();
        assert_ne!(again.id(), id);
        let taken = imports.take(again.id()).unwrap();
        assert!(imports.take(again.id()).is_none());
        imports.restore(taken);
        assert_eq!(imports.pending().unwrap().id(), again.id());
    }

    #[test]
    fn attaches_the_key_only_to_the_linked_service() {
        let input = json!({
            "name": "Relay",
            "kind": "openai_compatible",
            "http": {"base_url": "https://a.example/v1", "auth": {"scheme": "bearer"}},
            "capabilities": [],
        });
        let linked = entry("kind=openai_compatible&base_url=https://a.example/v1&api_key=sk-test");
        let authorized = authorize(&linked, input.clone()).unwrap();
        assert_eq!(
            authorized["http"]["credential"],
            json!({"secret": "sk-test"})
        );
        assert_eq!(authorized["name"], "Relay");

        for (pointer, value) in [
            ("/kind", json!("openai")),
            ("/http/base_url", json!("https://evil.example/v1")),
            ("/http/auth", json!({"scheme": "none"})),
            ("/http/credential", json!({"secret": "other"})),
        ] {
            let mut changed = input.clone();
            match changed.pointer_mut(pointer) {
                Some(slot) => *slot = value,
                None => changed["http"]["credential"] = value,
            }
            assert!(authorize(&linked, changed).is_err(), "{pointer}");
        }

        let pinned = entry(
            "kind=openai_compatible&base_url=https://a.example/v1&api_key=sk-test&auth=custom_header&auth_header=X-Key",
        );
        let mut custom = input.clone();
        custom["http"]["auth"] = json!({"scheme": "custom_header", "header_name": "X-Other"});
        assert!(authorize(&pinned, custom.clone()).is_err());
        custom["http"]["auth"]["header_name"] = json!("X-Key");
        assert!(authorize(&pinned, custom).is_ok());

        // Without a key in the link, the user may enter their own.
        let keyless = entry("kind=openai_compatible&base_url=https://a.example/v1");
        let mut typed = input;
        typed["http"]["credential"] = json!({"secret": "typed"});
        assert_eq!(
            authorize(&keyless, typed).unwrap()["http"]["credential"]["secret"],
            "typed"
        );
    }

    #[test]
    fn redacts_an_echoed_key() {
        assert_eq!(
            redact("bad key sk-test-123".into(), Some("sk-test-123")),
            "unable to add the service"
        );
        assert_eq!(
            redact("name taken".into(), Some("sk-test-123")),
            "name taken"
        );
        assert_eq!(redact("a bad a".into(), Some("a")), "a bad a");
    }
}
