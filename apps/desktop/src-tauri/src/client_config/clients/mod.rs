//! The clients AstrLink writes config files for, one module each, all
//! implementing `ClientSpec` and their files `ConfigFile`. The shared code
//! (records, confirmation, backup and restore, previews, batches, port sync)
//! reaches clients only through these two traits.
//!
//! To add a client, write `clients/<id>.rs` with a
//! `pub(crate) const CLIENT: Client` for its `ClientSpec`, and add `<id>` to
//! `registry!` below, where the order is the dialog's.

use std::path::PathBuf;

use crate::agent_home::Home;

use super::{
    catalog::Kind,
    client::Client,
    config::{Config, FileError},
    format::{Document, Format},
    models::{clean_model, Catalog, CurrentModels},
    plan::Connection,
    records::ClientRecord,
};

macro_rules! registry {
    ($($name:ident),* $(,)?) => {
        $(pub(crate) mod $name;)*

        /// Every client, in the order the dialog lists them.
        pub(crate) static REGISTERED: &[Client] = &[$($name::CLIENT),*];
    };
}

registry!(
    claude,
    codex,
    gemini,
    opencode,
    openclaw,
    pi,
    workbuddy,
    codebuddy,
    deepseek_harness,
    qwen_code,
    kimi_code,
    grok_build,
    droid,
    copilot_cli,
    vscode_copilot,
    kilo_code,
    crush,
    cline,
    zed,
    goose,
    mistral_vibe,
    codewhale,
    reasonix,
    zcode,
    minimax_code,
    step_code,
    aider,
    chatbox,
    qoder_cli,
    trae_cli,
);

/// The `models.json` WorkBuddy and CodeBuddy share.
pub(crate) mod buddy;

/// A client with two write targets, for tests of targets.
#[cfg(test)]
pub(crate) mod pair;

/// The clients an id may name: the registered ones, and in tests the
/// test-only ones too.
pub(crate) fn known() -> impl Iterator<Item = Client> {
    #[cfg(test)]
    let extra: &[Client] = &[pair::CLIENT];
    #[cfg(not(test))]
    let extra: &[Client] = &[];
    REGISTERED.iter().chain(extra).copied()
}

/// What the shared code asks of one client.
pub trait ClientSpec: Sync {
    // The catalog.

    /// The id IPC and the record file name the client by.
    fn id(&self) -> &'static str;
    /// The product name, never translated.
    fn name(&self) -> &'static str;
    fn kind(&self) -> Kind {
        Kind::File
    }
    /// The gateway protocols the client can be written with, preferred
    /// first.
    fn protocols(&self) -> &'static [&'static str];
    /// The write targets of a client written in several places, such as two
    /// editions; empty for one.
    fn targets(&self) -> &'static [&'static str] {
        &[]
    }
    /// Whether the client switches among the models AstrLink lists in it.
    fn lists_models(&self) -> bool {
        false
    }

    // Detection and paths. `target` is one of `targets`, or `None` for a
    // client without.

    /// The directory that holds the client's config, and whose absence
    /// means the client is not installed.
    fn dir(&self, home: Home, target: Option<&str>) -> PathBuf;
    fn detected(&self, home: Home, target: Option<&str>) -> bool {
        self.dir(home, target).is_dir()
    }
    /// The files the connection is written to, main file first.
    fn files(&self) -> &'static [&'static dyn ConfigFile];
    /// Where each of `files` is. `record` keeps a client that reads one of
    /// several files on the one AstrLink wrote.
    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        record: Option<&ClientRecord>,
    ) -> Vec<PathBuf>;
    /// The file name the record keeps for `config_paths`.
    fn recorded_file(&self, _paths: &[PathBuf]) -> Option<String> {
        None
    }

    // The models it takes.

    /// The `Models` fields it writes, `model` first.
    fn model_fields(&self) -> &'static [&'static str] {
        &["model"]
    }
    /// Whether a write needs a default model.
    fn requires_model(&self) -> bool {
        true
    }
    /// The reasoning efforts it takes; empty when it takes none.
    fn reasoning_efforts(&self) -> &'static [&'static str] {
        &[]
    }
    /// Whether it takes a context window and output limit for its default
    /// model.
    fn takes_limits(&self) -> bool {
        false
    }
    /// Rejects a default model the client cannot use.
    fn check_model(&self, _model: &str) -> Result<(), String> {
        Ok(())
    }

    // Reading the state.

    /// The gateway address the client is given.
    fn base_url(&self, origin: &str) -> String {
        format!("{origin}/v1")
    }
    /// Keys that decide where requests go and how they authenticate. A model
    /// the client rewrote itself, like with Codex's or Pi's `/model`, is not
    /// among them.
    fn connection_keys(&self) -> &'static [&'static str];
    /// Whether a key AstrLink wrote carries an address or token of its own,
    /// like a model entry with its own URL and key, and so counts among the
    /// connection keys.
    fn holds_connection(&self, _key: &str) -> bool {
        false
    }
    /// The models the config holds now.
    fn current_models(&self, config: &Config) -> CurrentModels;

    // The write plan.

    /// Every key AstrLink writes, with its value. `details` is what an entry
    /// for each listed model says besides its id.
    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)>;
    /// What a main file AstrLink creates starts with.
    fn start_file(&self, _config: &mut Config) -> Result<(), FileError> {
        Ok(())
    }

    // Port sync.

    /// The key holding the gateway address, which a sync rewrites.
    fn base_url_key(&self) -> &'static str;
    /// Each key a sync points at `origin`, with its new value: every place
    /// the client holds the gateway's address, in the form each needs.
    /// `config` is what the files hold now and `record` what AstrLink
    /// wrote; a sync records each value as AstrLink's.
    fn synced_keys(
        &self,
        _config: &Config,
        _record: &ClientRecord,
        origin: &str,
    ) -> Vec<(String, String)> {
        vec![(self.base_url_key().into(), self.base_url(origin))]
    }
    /// Keys newer versions write that configs from released builds lack. A
    /// sync adds each one once to a config nobody changed.
    fn added_keys(&self) -> &'static [(&'static str, &'static str)] {
        &[]
    }

    // The status.

    /// Situations the dialog explains for a place, by id, such as
    /// `running` or `overridden`. `config` is `None` when a file cannot be
    /// read.
    fn notes(
        &self,
        _home: Home,
        _target: Option<&str>,
        _config: Option<&Config>,
    ) -> Vec<&'static str> {
        Vec::new()
    }
    /// Whether the client is open and writes its own copy of the files back
    /// on exit, so AstrLink leaves them alone until it quits. The dialog
    /// shows the `running` note meanwhile.
    fn running(&self, _home: Home, _target: Option<&str>) -> bool {
        false
    }

    // A client the user pastes the token into, `Kind::Paste`.

    /// Whether it keeps the pasted token by address, so a new port needs
    /// another paste.
    fn keeps_token_by_address(&self) -> bool {
        false
    }
    /// The names the token is pasted under in the client, as `config` holds
    /// them.
    fn paste_into(&self, _config: &Config) -> Vec<String> {
        Vec::new()
    }
}

/// One file a client reads its connection from.
pub(crate) trait ConfigFile: Sync {
    /// How the client reads it.
    fn format(&self) -> Format;
    /// Whether the file holds credentials and nothing a preview may show.
    /// Every file a write changes becomes owner-only, since most hold the
    /// token; a secret file also stays owner-only when a removal rewrites
    /// it, and its preview masks every value, whatever the key's name.
    fn secret(&self) -> bool {
        false
    }
    /// Whether this file of its client holds the key.
    fn holds(&self, _key: &str) -> bool {
        true
    }
    /// What a change counts in a preview: `line` for `.env`, else `key`.
    fn unit(&self) -> &'static str {
        match self.format() {
            Format::Env { .. } => "line",
            _ => "key",
        }
    }
    /// Every key in the group AstrLink takes over, with its current value.
    fn group(&self, _document: &Document) -> Vec<(String, Option<String>)> {
        Vec::new()
    }
    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        document.get(key)
    }
    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        document.set(key, value)
    }
    fn remove(&self, document: &mut Document, key: &str) {
        document.remove(key);
    }
    /// Objects a removal drops once they are empty, innermost first.
    fn emptied(&self) -> &'static [&'static str] {
        &[]
    }
    /// Drops the containers a removal left empty.
    fn tidy(&self, document: &mut Document) {
        document.drop_empty(self.emptied());
    }
    /// Whether nothing is left, so a file AstrLink created can go.
    fn is_empty(&self, document: &Document) -> bool {
        document.is_empty()
    }
}

/// The provider AstrLink adds to OpenCode and OpenClaw; local config only.
pub(crate) const PROVIDER_ID: &str = "astrlink";

/// Adds `key` with its value when the file has it.
pub(crate) fn push_present(
    keys: &mut Vec<(String, Option<String>)>,
    file: &dyn ConfigFile,
    document: &Document,
    key: &str,
) {
    if let Some(value) = file.get(document, key) {
        keys.push((key.into(), value));
    }
}

/// Where a property goes among its siblings when AstrLink writes a model
/// entry: Pi's and OpenClaw's model entries read best in this order.
pub(crate) fn entry_rank(name: &str) -> usize {
    const ORDER: [&str; 9] = [
        "id",
        "name",
        "reasoning",
        "thinkingLevelMap",
        "contextWindow",
        "maxTokens",
        "limit",
        "context",
        "output",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .or_else(|| {
            pi::PI_THINKING_LEVELS
                .iter()
                .position(|level| *level == name)
                .map(|level| ORDER.len() + level)
        })
        .unwrap_or(usize::MAX)
}

/// A whole model list, kept as JSON with sorted keys so reformatting the
/// file changes nothing.
pub(crate) fn get_list(document: &Document, key: &str) -> Option<Option<String>> {
    match document {
        Document::Json(json) => json
            .get_json(key)
            .map(|value| value.map(|value| value.to_string())),
        _ => document.get(key),
    }
}

pub(crate) fn set_list(document: &mut Document, key: &str, value: &str) -> Result<(), String> {
    match document {
        Document::Json(json) => json.set_json(
            key,
            &serde_json::from_str(value).unwrap_or(serde_json::Value::Null),
            entry_rank,
        ),
        _ => document.set(key, value),
    }
}

/// The models a list holds, each once, by id: the keys of an object, or the
/// `id` of each entry of an array. Names AstrLink could not write back are
/// left out.
pub(crate) fn model_entries(list: serde_json::Value) -> Vec<(String, serde_json::Value)> {
    let mut entries = Vec::new();
    match list {
        serde_json::Value::Object(map) => {
            entries.extend(map);
        }
        serde_json::Value::Array(items) => {
            for entry in items {
                if let Some(id) = entry.get("id").and_then(serde_json::Value::as_str) {
                    entries.push((id.to_string(), entry.clone()));
                }
            }
        }
        _ => {}
    }
    let mut seen = std::collections::BTreeSet::new();
    entries
        .into_iter()
        .filter_map(|(id, entry)| Some((clean_model(id)?, entry)))
        .filter(|(id, _)| seen.insert(id.clone()))
        .collect()
}

/// The ids of a list's entries; `None` for none.
pub(crate) fn model_ids(entries: &[(String, serde_json::Value)]) -> Option<Vec<String>> {
    (!entries.is_empty()).then(|| entries.iter().map(|(id, _)| id.clone()).collect())
}
