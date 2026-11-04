//! Manual setup: the files a write creates for a client that has none.

use std::path::Path;

use serde::Serialize;

use super::{
    client::Client,
    gateway::{local_origin, reveal_access_token, token_summary},
    models::{catalog_for, official_details, Catalog, Models, Selection},
    plan::{plan_write_with, Connection},
};
use crate::{agent_home::Home, control_session, sidecar::CoreManager};

/// A path as the user would type it from their home.
fn from_home(home: Home, path: &Path) -> String {
    let Ok(relative) = path.strip_prefix(&*home) else {
        return home.shown(path);
    };
    let parts = relative
        .components()
        .map(|part| part.as_os_str().to_string_lossy().into_owned())
        .collect::<Vec<_>>();
    if home.windows() {
        format!("%USERPROFILE%\\{}", parts.join("\\"))
    } else {
        format!("~/{}", parts.join("/"))
    }
}

/// One file of a manual setup.
#[derive(Debug, PartialEq, Eq, Serialize)]
pub struct SnippetFile {
    /// Where the file goes, from the home.
    pub path: String,
    pub contents: String,
}

/// The files `apply` writes when a client has none yet, for manual setup;
/// a client with several targets shows its first.
pub(crate) fn snippet(
    client: Client,
    home: Home,
    connection: &Connection,
    details: &Catalog,
) -> Result<Vec<SnippetFile>, String> {
    let target = client.targets().first().copied();
    let paths = client.config_paths(home, target, None);
    let none = vec![None; paths.len()];
    let plan =
        plan_write_with(client, &none, connection, details, None).map_err(|error| error.message)?;
    Ok(paths
        .iter()
        .zip(plan.contents)
        .filter(|(_, contents)| !contents.trim().is_empty())
        .map(|(path, contents)| SnippetFile {
            path: from_home(home, path),
            contents,
        })
        .collect())
}

/// The files of a manual setup in the desktop user's home, with the token
/// revealed or shown as its hint.
pub async fn snippet_files(
    manager: &CoreManager,
    token_id: &str,
    client: Client,
    models: &Models,
    inference_url: &str,
    reveal: bool,
) -> Result<Vec<SnippetFile>, String> {
    let origin = local_origin(inference_url)?;
    let selection = Selection::of(models, client)?;
    let token = if reveal {
        reveal_access_token(manager, token_id, inference_url).await?
    } else {
        token_summary(manager, token_id).await?.1
    };
    let mut known = Catalog::new();
    if client.lists_models() {
        official_details(manager, &selection.list, &mut known).await;
    }
    let home = control_session::user_home()?;
    snippet(
        client,
        Home::local(&home),
        &selection.connection(None, token_id, &token, &origin),
        &catalog_for(&selection, &known),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn snippets_match_what_apply_writes() {
        let host = Path::new("/home/ada");
        let home = Home::local(host);
        let none = Catalog::new();
        let codex = snippet(CODEX, home, &connection(&codex_models()), &none).unwrap();
        assert_eq!(codex.len(), 1);
        assert_eq!(
            codex[0].contents,
            plan_write(CODEX, &[None], &connection(&codex_models()), None)
                .unwrap()
                .contents[0]
        );
        let claude = snippet(CLAUDE, home, &connection(&[]), &none).unwrap();
        assert_eq!(
            json(&claude[0].contents),
            serde_json::json!({"env": {"ANTHROPIC_BASE_URL": ORIGIN, "ANTHROPIC_AUTH_TOKEN": TOKEN}})
        );
        // A client with two files gets one block for each.
        let gemini = snippet(GEMINI, home, &connection(&codex_models()), &none).unwrap();
        let paths = gemini
            .iter()
            .map(|file| file.path.replace('\\', "/"))
            .collect::<Vec<_>>();
        assert!(
            paths[0].ends_with("/.gemini/.env") && paths[1].ends_with("/.gemini/settings.json")
        );
        if !cfg!(windows) {
            assert_eq!(paths, ["~/.gemini/.env", "~/.gemini/settings.json"]);
        }
        assert_eq!(
            gemini[0].contents,
            format!(
                "GOOGLE_GEMINI_BASE_URL={ORIGIN}\nGEMINI_API_KEY={TOKEN}\nGEMINI_MODEL=gpt-route\n"
            )
        );
        assert_eq!(
            json(&gemini[1].contents),
            serde_json::json!({"security": {"auth": {"selectedType": "gemini-api-key"}}})
        );
        let opencode = snippet(OPENCODE, home, &connection(&codex_models()), &none).unwrap();
        assert_eq!(opencode.len(), 1);
        assert!(opencode[0]
            .path
            .replace('\\', "/")
            .ends_with("/.config/opencode/opencode.jsonc"));
        let pi = snippet(PI, home, &connection(&pi_models()), &none).unwrap();
        assert_eq!(pi.len(), 2);
    }
}
