//! Writes AstrLink's gateway connection straight into a client's
//! user-level config, and keeps the base URL current when the port changes.
//!
//! Only the keys listed in `~/.astrlink/client-configs.json` belong to
//! AstrLink. Each edit is made in place, keeping the rest of the file's keys,
//! comments, and layout. Values AstrLink replaced are kept in that record
//! and come back when its config is removed. Verified against Claude Code
//! 2.1.300, Codex CLI 0.162.1, Gemini CLI 0.66.0, OpenCode 1.19.5,
//! OpenClaw 2026.10.10, and Pi 1.2.0.
//!
//! Each client is a file in `clients/`; `format/` edits the file formats;
//! the rest is shared: records, plans, confirmation, previews, batches,
//! port sync, and the catalog the dialog lists.

mod apply;
mod catalog;
mod client;
pub(crate) mod clients;
mod config;
mod format;
mod gateway;
mod link;
mod models;
mod plan;
mod preview;
mod records;
mod snippet;
mod status;
#[cfg(test)]
mod test_support;

pub use apply::{apply, remove, sync, Action, ApplyItem, BatchOutcome};
pub use catalog::{catalog, installed, ClientCatalog};
pub use client::{Client, Place};
pub use gateway::{local_origin, reveal_access_token, Tokens};
pub use link::import_link;
pub use models::Models;
pub use preview::{preview, Preview};
pub use snippet::{snippet_files, SnippetFile};
pub use status::{configured_token, status, ClientOverview};

pub(crate) use clients::{
    claude::{read_claude_settings, CLAUDE_MODEL_KEYS},
    codex::{read_codex_config, CODEX_PROVIDER},
    pi::{read_pi_models, PI_PROVIDER},
};
pub(crate) use models::valid_model;
