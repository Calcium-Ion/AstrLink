//! What the tests of every module share: a scratch home, a connection to
//! write, and each registered client by name.

pub(crate) use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    path::{Path, PathBuf},
    sync::atomic::{AtomicU64, Ordering},
};

pub(crate) use super::{
    apply::*, client::*, config::*, format::*, gateway::*, models::*, plan::*, preview::*,
    records::*, status::*,
};
pub(crate) use crate::agent_home::Home;

pub(crate) const CLAUDE: Client = super::clients::claude::CLIENT;
pub(crate) const CODEX: Client = super::clients::codex::CLIENT;
pub(crate) const GEMINI: Client = super::clients::gemini::CLIENT;
pub(crate) const OPENCODE: Client = super::clients::opencode::CLIENT;
pub(crate) const OPENCLAW: Client = super::clients::openclaw::CLIENT;
pub(crate) const PI: Client = super::clients::pi::CLIENT;
pub(crate) const WORKBUDDY: Client = super::clients::workbuddy::CLIENT;
pub(crate) const CODEBUDDY: Client = super::clients::codebuddy::CLIENT;
pub(crate) const DEEPSEEK_HARNESS: Client = super::clients::deepseek_harness::CLIENT;
pub(crate) const QWEN_CODE: Client = super::clients::qwen_code::CLIENT;
pub(crate) const KIMI_CODE: Client = super::clients::kimi_code::CLIENT;
pub(crate) const GROK_BUILD: Client = super::clients::grok_build::CLIENT;
pub(crate) const DROID: Client = super::clients::droid::CLIENT;
pub(crate) const COPILOT_CLI: Client = super::clients::copilot_cli::CLIENT;
pub(crate) const VSCODE_COPILOT: Client = super::clients::vscode_copilot::CLIENT;
pub(crate) const KILO_CODE: Client = super::clients::kilo_code::CLIENT;
pub(crate) const CRUSH: Client = super::clients::crush::CLIENT;
pub(crate) const CLINE: Client = super::clients::cline::CLIENT;
pub(crate) const ZED: Client = super::clients::zed::CLIENT;
pub(crate) const GOOSE: Client = super::clients::goose::CLIENT;
pub(crate) const MISTRAL_VIBE: Client = super::clients::mistral_vibe::CLIENT;
pub(crate) const CODEWHALE: Client = super::clients::codewhale::CLIENT;
pub(crate) const REASONIX: Client = super::clients::reasonix::CLIENT;
pub(crate) const ZCODE: Client = super::clients::zcode::CLIENT;
pub(crate) const MINIMAX_CODE: Client = super::clients::minimax_code::CLIENT;
pub(crate) const STEP_CODE: Client = super::clients::step_code::CLIENT;
pub(crate) const AIDER: Client = super::clients::aider::CLIENT;
pub(crate) const CHATBOX: Client = super::clients::chatbox::CLIENT;
pub(crate) const QODER_CLI: Client = super::clients::qoder_cli::CLIENT;
pub(crate) const TRAE_CLI: Client = super::clients::trae_cli::CLIENT;
pub(crate) const PAIR: Client = super::clients::pair::CLIENT;

pub(crate) const TOKEN: &str = "astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0";

pub(crate) const OTHER_TOKEN: &str = "astr_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB4";

pub(crate) const ORIGIN: &str = "http://127.0.0.1:18317";

pub(crate) fn unique_home(name: &str) -> PathBuf {
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

/// No protocol chosen for any model: each takes its client's first.
pub(crate) static NO_PROTOCOLS: BTreeMap<String, &'static str> = BTreeMap::new();

pub(crate) fn connection<'a>(models: &'a [(&'static str, String)]) -> Connection<'a> {
    Connection {
        target: None,
        token_id: "token_01",
        token: TOKEN,
        origin: ORIGIN,
        models,
        list: &[],
        protocols: &NO_PROTOCOLS,
    }
}

/// The keys and other gateway a write asks about; `None` once it wrote.
pub(crate) fn asked(outcome: ApplyOutcome) -> Option<(Vec<String>, Option<String>)> {
    match outcome {
        ApplyOutcome::Applied => None,
        ApplyOutcome::NeedsConfirmation(confirmation) => Some((
            confirmation.keys.into_iter().map(|key| key.key).collect(),
            confirmation.gateway,
        )),
    }
}

/// Details for Pi's `pi-route` entry.
pub(crate) fn route(details: ModelDetails) -> Catalog {
    Catalog::from([("pi-route".to_string(), details)])
}

/// What Pi's config holds once AstrLink wrote `model` alone.
pub(crate) fn pi_current(model: &str) -> CurrentModels {
    CurrentModels {
        model: Some(model.into()),
        models: Some(vec![model.into()]),
        ..Default::default()
    }
}

/// The tokens of the gateway `connection` writes for.
pub(crate) fn tokens() -> Tokens {
    Tokens(BTreeSet::from(["token_01".to_string()]))
}

pub(crate) fn codex_models() -> Vec<(&'static str, String)> {
    vec![("model", "gpt-route".to_string())]
}

pub(crate) fn json(text: &str) -> serde_json::Value {
    serde_json::from_str(text).unwrap()
}

pub(crate) fn pi_models() -> Vec<(&'static str, String)> {
    vec![("model", "pi-route".to_string())]
}

pub(crate) fn levels(names: &[&str]) -> Vec<String> {
    names.iter().map(|name| name.to_string()).collect()
}

pub(crate) fn gemini_models() -> Vec<(&'static str, String)> {
    vec![("model", "gemini-route".to_string())]
}

pub(crate) fn listed<'a>(
    models: &'a [(&'static str, String)],
    list: &'a [String],
) -> Connection<'a> {
    Connection {
        list,
        ..connection(models)
    }
}

pub(crate) fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| value.to_string()).collect()
}

pub(crate) fn json5(text: &str) -> serde_json::Value {
    read_json(OPENCLAW.files()[0], text).unwrap()
}
