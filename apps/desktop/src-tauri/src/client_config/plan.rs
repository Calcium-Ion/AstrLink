//! Planning a write, a removal, and a port sync over a client's files, and
//! reading the state they leave. The client decides the keys; the plans
//! decide what to replace, back up, put back, and leave alone.

use std::collections::BTreeMap;

use super::{
    catalog::Kind,
    client::{Client, Place},
    clients::ClientSpec,
    config::{Config, FileError},
    gateway::Tokens,
    models::{Catalog, CurrentModels},
    records::{sha256_hex, unix_now, ClientRecord, Records},
};
use serde::Serialize;

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
    /// A config file cannot be parsed.
    Invalid,
}

pub struct Connection<'a> {
    /// The place's write target, for a client that names what it writes
    /// after it; `None` for a client without.
    pub target: Option<&'a str>,
    pub token_id: &'a str,
    pub token: &'a str,
    pub origin: &'a str,
    pub models: &'a [(&'static str, String)],
    /// The models a client that switches models lists; the default model
    /// alone when empty.
    pub list: &'a [String],
    /// The gateway protocol chosen for each model; see `protocol`.
    pub protocols: &'a BTreeMap<String, &'static str>,
}

impl Connection<'_> {
    /// The model in a `Models` field, by its camelCase name.
    pub(crate) fn model(&self, field: &str) -> Option<String> {
        self.models
            .iter()
            .find(|(key, _)| *key == field)
            .map(|(_, value)| value.clone())
    }

    pub(crate) fn list(&self) -> Vec<String> {
        if self.list.is_empty() {
            self.model("model").into_iter().collect()
        } else {
            self.list.to_vec()
        }
    }

    /// The protocol `model` is written with: the one chosen for it, else
    /// the client's first.
    pub(crate) fn protocol(&self, client: &dyn ClientSpec, model: &str) -> &'static str {
        self.protocols
            .get(model)
            .copied()
            .or_else(|| client.protocols().first().copied())
            .unwrap_or_default()
    }
}

/// Whether every key deciding where `client`'s requests go still holds what
/// AstrLink wrote: its fixed connection keys, and the keys AstrLink wrote
/// that carry their own connection.
fn connection_kept(client: Client, config: &Config, record: &ClientRecord) -> bool {
    let written = record
        .keys
        .keys()
        .map(String::as_str)
        .filter(|key| client.holds_connection(key));
    client
        .connection_keys()
        .iter()
        .copied()
        .chain(written)
        .all(|path| {
            let value = config.get(path).flatten();
            written_by_us(Some(record), path, value.as_deref())
        })
}

pub(crate) fn written_by_us(
    record: Option<&ClientRecord>,
    path: &str,
    value: Option<&str>,
) -> bool {
    match (record.and_then(|record| record.keys.get(path)), value) {
        (Some(fingerprint), Some(value)) => *fingerprint == sha256_hex(value),
        _ => false,
    }
}

#[derive(Debug)]
pub struct WritePlan {
    /// New contents for each of the client's files, in `ClientSpec::files`
    /// order; a file the plan leaves alone keeps its text, and an empty one
    /// goes.
    pub contents: Vec<String>,
    /// Keys holding values AstrLink did not write, which the plan replaces
    /// or removes. Their values go only to the record's backup.
    pub conflicts: Vec<String>,
    pub record: ClientRecord,
}

/// `existing` holds each of the client's files, in `ClientSpec::files`
/// order.
#[cfg(test)]
pub(crate) fn plan_write(
    client: Client,
    existing: &[Option<&str>],
    connection: &Connection,
    previous: Option<&ClientRecord>,
) -> Result<WritePlan, FileError> {
    plan_write_with(client, existing, connection, &Catalog::new(), previous)
}

/// `plan_write` with what a list entry for each model says besides its id.
pub(crate) fn plan_write_with(
    client: Client,
    existing: &[Option<&str>],
    connection: &Connection,
    details: &Catalog,
    previous: Option<&ClientRecord>,
) -> Result<WritePlan, FileError> {
    let mut config = Config::parse(client, existing)?;
    let desired = client.desired_keys(connection, details);
    let wanted = |path: &str| {
        desired
            .iter()
            .find(|(key, _)| key == path)
            .map(|(_, value)| value)
    };
    let mut conflicts = Vec::new();
    let mut backup = previous
        .map(|record| record.backup.clone())
        .unwrap_or_default();
    // Each value AstrLink did not write is kept until its config goes. One
    // that differs from what AstrLink writes is a conflict; one that does
    // not, like an allowlisted model, stays once AstrLink's config goes.
    let mut found = |path: &str, value: Option<String>, want: Option<&String>| {
        if written_by_us(previous, path, value.as_deref()) {
            return;
        }
        match value {
            Some(value) if want == Some(&value) => {
                backup.entry(path.to_string()).or_insert(value);
            }
            Some(value) => {
                conflicts.push(path.to_string());
                backup.insert(path.to_string(), value);
            }
            None => {
                conflicts.push(path.to_string());
                backup.remove(path);
            }
        }
    };
    let new_file = existing[0].map_or(true, |text| text.trim().is_empty());
    if new_file {
        client.start_file(&mut config)?;
    }
    let group = config.group();
    for (path, value) in &group {
        let want = wanted(path);
        found(path, value.clone(), want);
        if want.is_none() {
            config.remove(path);
        }
    }
    // Settings outside the group, like Codex's reasoning effort or Gemini
    // CLI's login.
    for (path, want) in &desired {
        if group.iter().any(|(key, _)| key == path) {
            continue;
        }
        if let Some(value) = config.get(path) {
            found(path, value, Some(want));
        }
    }
    // What AstrLink wrote before and writes no more, like a cleared effort,
    // goes back to the value it replaced. A connection key's old value
    // waits for the whole config to go.
    if let Some(previous) = previous {
        for path in previous.keys.keys() {
            if wanted(path).is_some() || group.iter().any(|(key, _)| key == path) {
                continue;
            }
            let value = config.get(path).flatten();
            if !written_by_us(Some(previous), path, value.as_deref()) {
                continue;
            }
            match backup.remove(path) {
                Some(original) => config.set(path, &original)?,
                None => config.remove(path),
            }
        }
    }
    for (path, value) in &desired {
        config.set(path, value)?;
    }
    let mut contents = config.render(existing);
    // A file AstrLink added beside the main one goes once a write empties
    // it, as a removal would take it; an empty provider file breaks Goose.
    for (index, text) in contents.iter_mut().enumerate().skip(1) {
        let created = existing[index].is_none()
            || previous.is_some_and(|record| record.created_companions.contains(&index));
        if created
            && config.changed[index]
            && config.files[index].is_empty(&config.documents[index])
        {
            text.clear();
        }
    }
    Ok(WritePlan {
        contents,
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
            created_companions: (1..existing.len())
                .filter(|index| {
                    existing[*index].is_none()
                        || previous.is_some_and(|record| record.created_companions.contains(index))
                })
                .collect(),
            written_at_unix: unix_now(),
            paste_since_unix: paste_since(client, previous, connection),
            backup,
            file: None,
        },
    })
}

#[derive(Debug, PartialEq, Eq)]
pub enum Removal {
    Unchanged,
    Write(String),
    Delete,
}

/// One removal per file, in `ClientSpec::files` order.
pub(crate) fn plan_remove(
    client: Client,
    existing: &[Option<&str>],
    record: &ClientRecord,
) -> Result<Vec<Removal>, FileError> {
    plan_restore(client, existing, record).map(|(removals, _)| removals)
}

/// `plan_remove`, and how many replaced values come back. A key goes back
/// to its old value only while it holds what AstrLink wrote, or while it is
/// still gone; a value changed since stays.
pub(crate) fn plan_restore(
    client: Client,
    existing: &[Option<&str>],
    record: &ClientRecord,
) -> Result<(Vec<Removal>, usize), FileError> {
    let mut config = Config::parse(client, existing)?;
    // A connection another tool or the user replaced is theirs now, with
    // the config around it, so removal only forgets AstrLink's record.
    if !connection_kept(client, &config, record) {
        return Ok((existing.iter().map(|_| Removal::Unchanged).collect(), 0));
    }
    let mut restored = 0;
    for path in record.keys.keys() {
        let value = config.get(path).flatten();
        if !written_by_us(Some(record), path, value.as_deref()) {
            continue;
        }
        match record.backup.get(path) {
            Some(original) if value.as_ref() == Some(original) => {}
            Some(original) => {
                config.set(path, original)?;
                restored += 1;
            }
            None => config.remove(path),
        }
    }
    for (path, original) in &record.backup {
        if !record.keys.contains_key(path) && config.get(path).is_none() {
            config.set(path, original)?;
            restored += 1;
        }
    }
    let Config {
        files,
        mut documents,
        changed,
    } = config;
    let removals = documents
        .iter_mut()
        .zip(files)
        .zip(&changed)
        .enumerate()
        .map(|(index, ((document, file), changed))| {
            if !changed {
                return Removal::Unchanged;
            }
            file.tidy(document);
            let created = match index {
                0 => record.created_file,
                _ => record.created_companions.contains(&index),
            };
            if created && file.is_empty(document) {
                Removal::Delete
            } else {
                Removal::Write(document.render())
            }
        })
        .collect();
    Ok((removals, restored))
}

pub(crate) fn inspect(
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
    if !connection_kept(client, &config, record) {
        return ClientState::Modified;
    }
    match origin {
        Some(origin) if record.base_url != client.base_url(origin) => ClientState::Outdated,
        _ => ClientState::Configured,
    }
}

/// A place's state. Without the gateway's `tokens`, another AstrLink's
/// config reads as outdated.
pub(crate) fn place_state(
    records: &Records,
    place: Place,
    existing: &[Option<&str>],
    origin: Option<&str>,
    tokens: Option<&Tokens>,
) -> ClientState {
    match inspect(place.client, existing, records.get(place), origin) {
        ClientState::Outdated
            if tokens
                .zip(records.get(place))
                .is_some_and(|(tokens, record)| !tokens.own(record)) =>
        {
            ClientState::Elsewhere
        }
        state => state,
    }
}

/// The models a client's config holds now. The client may have picked them
/// itself since, like with Codex's `/model`. Names AstrLink could not write
/// back are left out.
pub(crate) fn current_models(client: Client, existing: &[Option<&str>]) -> CurrentModels {
    let Ok(config) = Config::parse(client, existing) else {
        return CurrentModels::default();
    };
    let mut models = client.current_models(&config);
    // The default model leads the list, as AstrLink wrote it.
    if let (Some(model), Some(list)) = (&models.model, &mut models.models) {
        if let Some(at) = list.iter().position(|id| id == model) {
            let id = list.remove(at);
            list.insert(0, id);
        }
    }
    models
}

/// When the token pasted into a client starts to count. It stays while the
/// token does, and for a client that keeps it by address, the address.
fn paste_since(
    client: Client,
    previous: Option<&ClientRecord>,
    connection: &Connection,
) -> Option<u64> {
    if client.kind() != Kind::Paste {
        return None;
    }
    previous
        .filter(|previous| {
            previous.token_sha256 == sha256_hex(connection.token)
                && (!client.keeps_token_by_address()
                    || previous.base_url == client.base_url(connection.origin))
        })
        .and_then(|previous| previous.paste_since_unix)
        .or_else(|| Some(unix_now()))
}

/// Points an untouched config at the gateway's new port, and adds the keys
/// a released build did not write yet. The token stays as written, so the
/// sync never needs to reveal it.
pub(crate) fn plan_sync(
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
        for (key, value) in client.synced_keys(&config, record, origin) {
            config.set(&key, &value)?;
            next.keys.insert(key, sha256_hex(&value));
        }
        next.base_url = client.base_url(origin);
        if next.paste_since_unix.is_some() && client.keeps_token_by_address() {
            next.paste_since_unix = Some(unix_now());
        }
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn new_clients_follow_the_gateway_port() {
        for client in [GEMINI, OPENCODE, OPENCLAW] {
            let models = codex_models();
            let none = vec![None; client.files().len()];
            let plan = plan_write(client, &none, &connection(&models), None).unwrap();
            let existing = plan
                .contents
                .iter()
                .map(|text| Some(text.as_str()))
                .collect::<Vec<_>>();
            assert_eq!(
                inspect(
                    client,
                    &existing,
                    Some(&plan.record),
                    Some("http://127.0.0.1:9000")
                ),
                ClientState::Outdated
            );
            let (contents, record) =
                plan_sync(client, &existing, &plan.record, "http://127.0.0.1:9000")
                    .unwrap()
                    .unwrap();
            assert!(contents[0].contains("http://127.0.0.1:9000"), "{client:?}");
            assert!(!contents[0].contains("18317"), "{client:?}");
            let synced = contents
                .iter()
                .map(|text| Some(text.as_str()))
                .collect::<Vec<_>>();
            assert_eq!(
                inspect(
                    client,
                    &synced,
                    Some(&record),
                    Some("http://127.0.0.1:9000")
                ),
                ClientState::Configured
            );
        }
    }
}
