//! What a write or removal would change, for the dialog: a masked diff per
//! file and the keys counted.

use std::{collections::BTreeSet, path::PathBuf};

use serde::Serialize;

use super::{
    apply::{read_files, texts, Action, Secret},
    client::{Client, Place},
    config::Config,
    format::env::closing_quote,
    gateway::{local_origin, reveal_access_token, token_summary},
    models::{catalog_for, official_details, Catalog, Models, Selection},
    plan::{plan_restore, plan_write_with, Removal},
    records::read_records,
};
use crate::{
    agent_home::{Home, Homes},
    host_files,
    sidecar::CoreManager,
};

/// Whether a key's value is a credential or otherwise private, judged by
/// its name.
pub(crate) fn secret_name(name: &str) -> bool {
    let name = name.to_ascii_lowercase();
    [
        "key",
        "token",
        "secret",
        "password",
        "passwd",
        "auth",
        "credential",
        "bearer",
        "cookie",
        "session",
    ]
    .iter()
    .any(|word| name.contains(word))
        && name != "selectedtype"
}

/// A value as a confirmation shows it: a credential by its ends only, as
/// the diff does; an address without the user, password and query that may
/// carry one; anything else as it is. `None` for a value that is not one
/// line of text, like an object, or is empty.
pub(crate) fn displayed_value(path: &str, value: &str) -> Option<String> {
    const LONGEST: usize = 200;
    let value = value.trim();
    if value.is_empty() || value.contains(['\n', '\r']) || value.starts_with(['{', '[']) {
        return None;
    }
    let name = path.rsplit('.').next().unwrap_or(path);
    if secret_name(name) {
        return Some(mask(value));
    }
    let shown = match reqwest::Url::parse(value) {
        Ok(mut url)
            if matches!(url.scheme(), "http" | "https" | "ws" | "wss")
                && (!url.username().is_empty()
                    || url.password().is_some()
                    || url.query().is_some()
                    || url.fragment().is_some()) =>
        {
            let _ = url.set_username("");
            let _ = url.set_password(None);
            url.set_query(None);
            url.set_fragment(None);
            url.to_string()
        }
        _ => value.to_string(),
    };
    Some(if shown.chars().count() > LONGEST {
        format!("{}…", shown.chars().take(LONGEST).collect::<String>())
    } else {
        shown
    })
}

/// A secret shown by its ends only.
fn mask(value: &str) -> String {
    if value.contains('…') {
        return value.to_string();
    }
    let chars = value.chars().collect::<Vec<_>>();
    match chars.len() {
        0 => String::new(),
        length if length >= 12 => format!(
            "{}…{}",
            chars[..4].iter().collect::<String>(),
            chars[length - 3..].iter().collect::<String>()
        ),
        length if length > 4 => format!("…{}", chars[length - 2..].iter().collect::<String>()),
        _ => "…".into(),
    }
}

/// Masks the values of secret-named keys on one line of a config file:
/// `"apiKey": "…"`, `token = "…"`, or `NAME=value`; every value when `all`,
/// for a line of a file that holds credentials only.
fn mask_values(line: &str, all: bool) -> String {
    let mut out = String::with_capacity(line.len());
    let mut rest = line;
    while let Some(at) = rest.find([':', '=']) {
        let (before, after) = rest.split_at(at);
        out.push_str(before);
        out.push_str(&after[..1]);
        let after = &after[1..];
        let name = before
            .trim_end()
            .trim_end_matches(['"', '\''])
            .rsplit(|c: char| !(c.is_ascii_alphanumeric() || matches!(c, '_' | '-' | '.')))
            .next()
            .unwrap_or_default();
        let value = after.trim_start();
        out.push_str(&after[..after.len() - value.len()]);
        rest = value;
        if name.is_empty() || !(all || secret_name(name)) {
            continue;
        }
        let quote = value.chars().next().filter(|c| matches!(c, '"' | '\''));
        let (secret, tail) = match quote.map(|quote| (quote, closing_quote(value, quote))) {
            Some((quote, Some(end))) => {
                out.push(quote);
                (&value[1..end], &value[end..])
            }
            Some((_, None)) => (value, ""),
            None => {
                let end = value
                    .find(|c: char| c.is_whitespace() || matches!(c, ',' | '#' | '}' | ']'))
                    .unwrap_or(value.len());
                (&value[..end], &value[end..])
            }
        };
        // Objects and other non-text values hold no secret themselves.
        if secret.is_empty() || (quote.is_none() && secret.starts_with(['{', '['])) {
            continue;
        }
        out.push_str(&mask(secret));
        rest = tail;
    }
    out.push_str(rest);
    out
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum LineKind {
    Context,
    Add,
    Remove,
    /// Unchanged lines left out between two changes.
    Gap,
}

#[derive(Debug, PartialEq, Eq, Serialize)]
pub struct DiffLine {
    pub kind: LineKind,
    pub text: String,
}

/// The lines of `before` and `after` that differ, with two lines of context.
pub(crate) fn diff_lines(before: &str, after: &str) -> Vec<DiffLine> {
    let split = |text: &str| -> Vec<String> {
        let mut lines = text
            .split('\n')
            .map(|line| line.strip_suffix('\r').unwrap_or(line).to_string())
            .collect::<Vec<_>>();
        if lines.last().is_some_and(String::is_empty) {
            lines.pop();
        }
        lines
    };
    let (old, new) = (split(before), split(after));
    let prefix = old.iter().zip(&new).take_while(|(a, b)| a == b).count();
    let suffix = old[prefix..]
        .iter()
        .rev()
        .zip(new[prefix..].iter().rev())
        .take_while(|(a, b)| a == b)
        .count();
    let (a, b) = (
        &old[prefix..old.len() - suffix],
        &new[prefix..new.len() - suffix],
    );
    // Longest common subsequence of the changed middle; past this size the
    // middle is shown as replaced whole.
    let mut ops: Vec<(LineKind, &str)> = old[..prefix]
        .iter()
        .map(|line| (LineKind::Context, line.as_str()))
        .collect();
    if a.len().saturating_mul(b.len()) <= 4_000_000 {
        let mut table = vec![vec![0u32; b.len() + 1]; a.len() + 1];
        for i in (0..a.len()).rev() {
            for j in (0..b.len()).rev() {
                table[i][j] = if a[i] == b[j] {
                    table[i + 1][j + 1] + 1
                } else {
                    table[i + 1][j].max(table[i][j + 1])
                };
            }
        }
        let (mut i, mut j) = (0, 0);
        while i < a.len() || j < b.len() {
            if i < a.len() && j < b.len() && a[i] == b[j] {
                ops.push((LineKind::Context, a[i].as_str()));
                i += 1;
                j += 1;
            } else if i < a.len() && (j == b.len() || table[i + 1][j] >= table[i][j + 1]) {
                ops.push((LineKind::Remove, a[i].as_str()));
                i += 1;
            } else {
                ops.push((LineKind::Add, b[j].as_str()));
                j += 1;
            }
        }
    } else {
        ops.extend(a.iter().map(|line| (LineKind::Remove, line.as_str())));
        ops.extend(b.iter().map(|line| (LineKind::Add, line.as_str())));
    }
    ops.extend(
        old[old.len() - suffix..]
            .iter()
            .map(|line| (LineKind::Context, line.as_str())),
    );
    const CONTEXT: usize = 2;
    let changed = ops
        .iter()
        .enumerate()
        .filter(|(_, (kind, _))| *kind != LineKind::Context)
        .map(|(index, _)| index)
        .collect::<Vec<_>>();
    let shown = |index: usize| {
        changed
            .iter()
            .any(|at| index + CONTEXT >= *at && index <= at + CONTEXT)
    };
    let mut lines = Vec::new();
    let mut skipped = false;
    for (index, (kind, text)) in ops.iter().enumerate() {
        if !shown(index) {
            skipped = true;
            continue;
        }
        if skipped && !lines.is_empty() {
            lines.push(DiffLine {
                kind: LineKind::Gap,
                text: String::new(),
            });
        }
        skipped = false;
        lines.push(DiffLine {
            kind: *kind,
            text: (*text).to_string(),
        });
    }
    lines
}

/// What a write or removal changes in one file.
#[derive(Debug, PartialEq, Eq, Serialize)]
pub struct FileChanges {
    pub path: String,
    pub created: bool,
    pub deleted: bool,
    /// Keys whose value changes.
    pub changes: usize,
    /// What a change counts: `line` for `.env`, else `key`.
    pub unit: &'static str,
    /// The token shows as its hint; other secret values by their ends.
    pub lines: Vec<DiffLine>,
}

#[derive(Debug, PartialEq, Eq, Serialize)]
pub struct Preview {
    pub files: Vec<FileChanges>,
    /// Values AstrLink did not write that the write replaces or removes.
    pub replaced: usize,
    /// Values a removal puts back.
    pub restored: usize,
}

/// The changes from `before` to `after` per file, masked. `keys` are the
/// keys worth counting.
pub(crate) fn describe(
    client: Client,
    paths: &[PathBuf],
    before: &[Option<&str>],
    after: &[Option<&str>],
    keys: &BTreeSet<String>,
    secrets: &[(&str, &str)],
) -> Vec<FileChanges> {
    let (Ok(old), Ok(new)) = (Config::parse(client, before), Config::parse(client, after)) else {
        return Vec::new();
    };
    let mut changes = vec![0; paths.len()];
    for key in keys {
        if old.get(key) != new.get(key) {
            changes[old.index(key)] += 1;
        }
    }
    let shown = |line: &str, all: bool| {
        let mut line = line.to_string();
        for (secret, hint) in secrets {
            if !secret.is_empty() {
                line = line.replace(secret, hint);
            }
        }
        mask_values(&line, all)
    };
    paths
        .iter()
        .enumerate()
        .filter(|(index, _)| before[*index] != after[*index])
        .map(|(index, path)| FileChanges {
            path: path.display().to_string(),
            created: before[index].is_none(),
            deleted: after[index].is_none(),
            changes: changes[index],
            unit: client.files()[index].unit(),
            lines: diff_lines(
                &client.files()[index].preview(before[index].unwrap_or_default()),
                &client.files()[index].preview(after[index].unwrap_or_default()),
            )
            .into_iter()
            .map(|line| DiffLine {
                text: shown(&line.text, client.files()[index].secret()),
                ..line
            })
            .collect(),
        })
        .collect()
}

/// What writing or removing one place would change, for the dialog. The
/// token never leaves: it shows as `hint`.
#[allow(clippy::too_many_arguments)]
pub async fn preview(
    manager: &CoreManager,
    homes: Homes,
    token_id: Option<String>,
    place: Place,
    wsl: Option<String>,
    action: Action,
    models: Option<Models>,
    inference_url: String,
) -> Result<Preview, String> {
    let client = place.client;
    let origin = local_origin(&inference_url)?;
    let write = match action {
        Action::Remove => None,
        Action::Write => {
            let selection = Selection::of(&models.ok_or("models are required")?, client)?;
            let id = token_id.ok_or("access token is required")?;
            let (_, hint) = token_summary(manager, &id).await?;
            let token = reveal_access_token(manager, &id, &inference_url).await?;
            let mut known = Catalog::new();
            if client.lists_models() {
                official_details(manager, &selection.list, &mut known).await;
            }
            let details = catalog_for(&selection, &known);
            Some(PreviewWrite {
                selection,
                secret: Secret { id, token },
                hint,
                details,
            })
        }
    };
    tauri::async_runtime::spawn_blocking(move || {
        let home = homes.get(wsl.as_deref()).ok_or_else(|| {
            format!(
                "WSL distribution {} is no longer available",
                wsl.unwrap_or_default()
            )
        })?;
        preview_place(home, place, &origin, write)
    })
    .await
    .map_err(|error| error.to_string())?
}

/// A write to preview: what it writes, with the token and its hint.
pub(crate) struct PreviewWrite {
    pub(crate) selection: Selection,
    pub(crate) secret: Secret,
    pub(crate) hint: String,
    pub(crate) details: Catalog,
}

/// `preview` for one place in a home, without asking the gateway.
pub(crate) fn preview_place(
    home: Home,
    place: Place,
    origin: &str,
    write: Option<PreviewWrite>,
) -> Result<Preview, String> {
    let client = place.client;
    let _lock = host_files::lock();
    let records = read_records(&home)?;
    let record = records.get(place);
    let paths = client.config_paths(home, place.target, record);
    let existing = read_files(client, &paths)?;
    let before = texts(&existing);
    match write {
        Some(PreviewWrite {
            selection,
            secret,
            hint,
            details,
        }) => {
            let connection = selection.connection(place.target, &secret.id, &secret.token, origin);
            let plan = plan_write_with(client, &before, &connection, &details, record)
                .map_err(|error| String::from(error.at(&paths)))?;
            let after = plan
                .contents
                .iter()
                .map(|text| Some(text.as_str()))
                .collect::<Vec<_>>();
            // Files the write leaves missing stay missing, and ones it
            // empties go.
            let after = after
                .iter()
                .zip(&before)
                .map(|(after, before)| match (before, after) {
                    (None, Some("")) => None,
                    (Some(before), Some("")) if !before.is_empty() => None,
                    _ => *after,
                })
                .collect::<Vec<_>>();
            let keys = plan
                .record
                .keys
                .keys()
                .chain(&plan.conflicts)
                .chain(record.into_iter().flat_map(|record| record.keys.keys()))
                .cloned()
                .collect();
            Ok(Preview {
                files: describe(
                    client,
                    &paths,
                    &before,
                    &after,
                    &keys,
                    &[(&secret.token, &hint)],
                ),
                replaced: plan.conflicts.len(),
                restored: 0,
            })
        }
        None => {
            let Some(record) = record else {
                return Ok(Preview {
                    files: Vec::new(),
                    replaced: 0,
                    restored: 0,
                });
            };
            let (removals, restored) = plan_restore(client, &before, record)
                .map_err(|error| String::from(error.at(&paths)))?;
            let after = removals
                .iter()
                .zip(&before)
                .map(|(removal, before)| match removal {
                    Removal::Unchanged => *before,
                    Removal::Write(text) => Some(text.as_str()),
                    Removal::Delete => None,
                })
                .collect::<Vec<_>>();
            let keys = record
                .keys
                .keys()
                .chain(record.backup.keys())
                .cloned()
                .collect();
            Ok(Preview {
                files: describe(client, &paths, &before, &after, &keys, &[]),
                replaced: 0,
                restored,
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn previews_mask_secrets_and_show_changes_with_context() {
        for (line, shown) in [
            (
                "GEMINI_API_KEY=sk-1234567890abcdef",
                "GEMINI_API_KEY=sk-1…def",
            ),
            (
                "experimental_bearer_token = \"abcdefghijklmnop\"",
                "experimental_bearer_token = \"abcd…nop\"",
            ),
            (
                "    \"apiKey\": \"astr_…AAAAA0\",",
                "    \"apiKey\": \"astr_…AAAAA0\",",
            ),
            (
                "  \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:1\",",
                "  \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:1\",",
            ),
            ("  \"auth\": {", "  \"auth\": {"),
            (
                "      \"selectedType\": \"gemini-api-key\"",
                "      \"selectedType\": \"gemini-api-key\"",
            ),
            (
                "{ apiKey: 'sk-abcdefghijkl', api: 'x' }",
                "{ apiKey: 'sk-a…jkl', api: 'x' }",
            ),
        ] {
            assert_eq!(mask_values(line, false), shown);
        }

        let before = (1..=10).map(|n| format!("l{n}\n")).collect::<String>();
        let after = before.replace("l2\n", "L2\n").replace("l9\n", "L9\n");
        let kinds = diff_lines(&before, &after)
            .into_iter()
            .map(|line| match line.kind {
                LineKind::Context => format!(" {}", line.text),
                LineKind::Add => format!("+{}", line.text),
                LineKind::Remove => format!("-{}", line.text),
                LineKind::Gap => "…".into(),
            })
            .collect::<Vec<_>>();
        assert_eq!(
            kinds,
            [" l1", "-l2", "+L2", " l3", " l4", "…", " l7", " l8", "-l9", "+L9", " l10"]
        );

        let hint = "astr_…AAAAA0";
        let paths = [
            PathBuf::from("/home/ada/.gemini/.env"),
            PathBuf::from("/home/ada/.gemini/settings.json"),
        ];
        let env = "OTHER=1\nGEMINI_API_KEY=sk-other-secret-value\n";
        let before = [Some(env), None];
        let plan = plan_write(GEMINI, &before, &connection(&gemini_models()), None).unwrap();
        let after = [Some(plan.contents[0].as_str()), Some(&plan.contents[1])];
        let keys = plan.record.keys.keys().cloned().collect();
        let files = describe(GEMINI, &paths, &before, &after, &keys, &[(TOKEN, hint)]);
        assert_eq!(files.len(), 2);
        assert_eq!(
            (files[0].changes, files[0].unit, files[0].created),
            (3, "line", false)
        );
        assert_eq!(
            (files[1].changes, files[1].unit, files[1].created),
            (1, "key", true)
        );
        let text = format!("{files:?}");
        assert!(!text.contains(TOKEN) && !text.contains("sk-other-secret-value"));
        assert!(text.contains(hint));
        assert!(files[0]
            .lines
            .iter()
            .any(|line| line.kind == LineKind::Remove && line.text == "GEMINI_API_KEY=sk-o…lue"));
    }
}
