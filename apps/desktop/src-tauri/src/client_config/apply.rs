//! Writing and removing client configs, one place or a batch, and keeping
//! the ones the gateway wrote pointed at its current port.

use std::{
    collections::{BTreeMap, BTreeSet},
    path::PathBuf,
};

use serde::{Deserialize, Serialize};

use super::{
    client::{Client, Place},
    config::{Config, ItemError},
    gateway::{local_origin, reveal_access_token, Tokens},
    models::{catalog_for, official_details, Catalog, Models, Selection},
    plan::{
        place_state, plan_remove, plan_sync, plan_write_with, ClientState, Connection, Removal,
    },
    preview::displayed_value,
    records::{read_records, write_records, Records},
};
use crate::{
    agent_home::{Home, Homes},
    host_files,
    sidecar::CoreManager,
};

/// A value AstrLink did not write that a write replaces or removes. Both
/// values show as the change preview does: a credential by its ends only.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct ReplacedKey {
    /// The file holding it.
    pub path: String,
    pub key: String,
    /// The value now; `None` when it is not one line of text.
    pub current: Option<String>,
    /// The value the write puts there; `None` when the write removes the
    /// key or the value is not one line of text.
    pub next: Option<String>,
    /// The write removes the key.
    pub removed: bool,
}

/// What a client's write or removal asks the user to confirm.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct Confirmation {
    pub client: Client,
    pub wsl: Option<String>,
    /// One of the client's targets; `None` for a client without.
    pub target: Option<&'static str>,
    pub dir: String,
    /// AstrLink's config is removed.
    pub remove: bool,
    pub keys: Vec<ReplacedKey>,
    /// The config connects to another AstrLink at this base URL now.
    pub gateway: Option<String>,
    /// The config uses this other token of the gateway now.
    pub token_id: Option<String>,
}

impl Confirmation {
    fn needed(&self) -> bool {
        self.remove || !self.keys.is_empty() || self.gateway.is_some() || self.token_id.is_some()
    }
}

#[derive(Debug, PartialEq, Eq)]
pub enum ApplyOutcome {
    Applied,
    /// Nothing was written.
    NeedsConfirmation(Confirmation),
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Action {
    Write,
    Remove,
}

/// One place to write or remove, in the desktop user's home or a WSL one.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ApplyItem {
    pub client: Client,
    pub wsl: Option<String>,
    /// One of the client's targets; `None` for a client without.
    #[serde(default)]
    pub target: Option<String>,
    pub action: Action,
    #[serde(default)]
    pub models: Option<Models>,
}

#[derive(Debug, Serialize)]
pub struct ItemResult {
    pub client: Client,
    pub wsl: Option<String>,
    /// One of the client's targets; `None` for a client without.
    pub target: Option<&'static str>,
    pub action: Action,
    pub ok: bool,
    pub error: Option<ItemError>,
}

#[derive(Debug, Serialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub enum BatchOutcome {
    /// Nothing was written.
    NeedsConfirmation { items: Vec<Confirmation> },
    /// One result per item, in order.
    Done { results: Vec<ItemResult> },
}

/// What one write in a batch is made of.
pub(crate) struct Job<'a> {
    pub(crate) home: Option<Home<'a>>,
    pub(crate) client: Client,
    pub(crate) target: Option<&'static str>,
    pub(crate) wsl: Option<String>,
    pub(crate) action: Action,
    pub(crate) selection: Option<Selection>,
    pub(crate) details: Catalog,
}

impl Job<'_> {
    fn place(&self) -> Place {
        Place {
            client: self.client,
            target: self.target,
        }
    }
}

/// The token a batch writes, revealed once.
pub(crate) struct Secret {
    pub(crate) id: String,
    pub(crate) token: String,
}

/// Writes and removes each item. Without `confirmed`, nothing is written
/// while any item needs the user's confirmation. Items are written one by
/// one; one failing leaves the others written.
pub async fn apply(
    manager: &CoreManager,
    homes: Homes,
    token_id: Option<String>,
    inference_url: String,
    items: Vec<ApplyItem>,
    confirmed: bool,
) -> Result<BatchOutcome, String> {
    if items.is_empty() || items.len() > 64 {
        return Err("invalid client list".into());
    }
    let places = items
        .iter()
        .map(|item| Place::new(item.client, item.target.as_deref()))
        .collect::<Result<Vec<_>, _>>()?;
    let mut seen = BTreeSet::new();
    for (item, place) in items.iter().zip(&places) {
        if !seen.insert((
            *place,
            item.wsl.as_ref().map(|name| name.to_ascii_lowercase()),
        )) {
            return Err("a client is listed twice".into());
        }
    }
    let origin = local_origin(&inference_url)?;
    let selections = items
        .iter()
        .map(|item| match (item.action, &item.models) {
            (Action::Write, Some(models)) => Selection::of(models, item.client).map(Some),
            (Action::Write, None) => Err("models are required".to_string()),
            (Action::Remove, _) => Ok(None),
        })
        .collect::<Result<Vec<_>, _>>()?;
    let writes = selections.iter().any(Option::is_some);
    let (secret, tokens) = if writes {
        let id = token_id.ok_or("access token is required")?;
        let token = reveal_access_token(manager, &id, &inference_url).await?;
        (Some(Secret { id, token }), Some(Tokens::of(manager).await?))
    } else {
        (None, None)
    };
    let mut known = Catalog::new();
    for (item, selection) in items.iter().zip(&selections) {
        if let Some(selection) = selection.as_ref().filter(|_| item.client.lists_models()) {
            official_details(manager, &selection.list, &mut known).await;
        }
    }
    tauri::async_runtime::spawn_blocking(move || {
        let jobs = items
            .into_iter()
            .zip(places)
            .zip(selections)
            .map(|((item, place), selection)| Job {
                home: homes.get(item.wsl.as_deref()),
                details: selection
                    .as_ref()
                    .map(|selection| catalog_for(selection, &known))
                    .unwrap_or_default(),
                client: item.client,
                target: place.target,
                wsl: item.wsl,
                action: item.action,
                selection,
            })
            .collect::<Vec<_>>();
        run(&jobs, secret.as_ref(), &origin, tokens.as_ref(), confirmed)
    })
    .await
    .map_err(|error| error.to_string())
}

fn unavailable(job: &Job) -> ItemError {
    format!(
        "WSL distribution {} is no longer available",
        job.wsl.as_deref().unwrap_or_default()
    )
    .into()
}

pub(crate) fn run(
    jobs: &[Job],
    secret: Option<&Secret>,
    origin: &str,
    tokens: Option<&Tokens>,
    confirmed: bool,
) -> BatchOutcome {
    fn connection<'a>(
        job: &'a Job,
        secret: Option<&'a Secret>,
    ) -> Option<(&'a Selection, &'a Secret)> {
        Some((job.selection.as_ref()?, secret?))
    }
    if !confirmed {
        let mut confirmations = Vec::new();
        for job in jobs {
            let Some(home) = job.home else {
                continue;
            };
            let check = match (job.action, connection(job, secret)) {
                (Action::Remove, _) => removal_check(home, job.place()),
                (Action::Write, Some((selection, secret))) => check(
                    home,
                    job.place(),
                    &selection.connection(job.target, &secret.id, &secret.token, origin),
                    &job.details,
                    tokens,
                ),
                (Action::Write, None) => continue,
            };
            // A client that cannot be read reports it once written.
            if let Ok(Some(confirmation)) = check {
                confirmations.push(confirmation);
            }
        }
        if !confirmations.is_empty() {
            return BatchOutcome::NeedsConfirmation {
                items: confirmations,
            };
        }
    }
    let results = jobs
        .iter()
        .map(|job| {
            let result = match (job.home, job.action, connection(job, secret)) {
                (None, ..) => Err(unavailable(job)),
                (Some(home), Action::Remove, _) => remove(home, job.place()),
                (Some(home), Action::Write, Some((selection, secret))) => write_with(
                    home,
                    job.place(),
                    &selection.connection(job.target, &secret.id, &secret.token, origin),
                    &job.details,
                    tokens.unwrap_or(&Tokens(BTreeMap::new())),
                    confirmed,
                )
                .and_then(|outcome| match outcome {
                    ApplyOutcome::Applied => Ok(()),
                    ApplyOutcome::NeedsConfirmation(_) => {
                        Err("the config changed since it was checked; write again".into())
                    }
                }),
                (Some(_), Action::Write, None) => Err("access token is required".into()),
            };
            ItemResult {
                client: job.client,
                wsl: job.wsl.clone(),
                target: job.target,
                action: job.action,
                ok: result.is_ok(),
                error: result.err(),
            }
        })
        .collect();
    BatchOutcome::Done { results }
}

/// A removal asks first whenever AstrLink has a config to remove.
fn removal_check(home: Home, place: Place) -> Result<Option<Confirmation>, ItemError> {
    let _lock = host_files::lock();
    let records = read_records(&home)?;
    Ok(records.get(place).map(|_| Confirmation {
        client: place.client,
        wsl: home.distribution().map(str::to_string),
        target: place.target,
        dir: place.client.dir(home, place.target).display().to_string(),
        remove: true,
        keys: Vec::new(),
        gateway: None,
        token_id: None,
    }))
}

/// What writing `place` would ask to confirm, if anything.
fn check(
    home: Home,
    place: Place,
    connection: &Connection,
    details: &Catalog,
    tokens: Option<&Tokens>,
) -> Result<Option<Confirmation>, ItemError> {
    let _lock = host_files::lock();
    let records = read_records(&home)?;
    let paths = place
        .client
        .config_paths(home, place.target, records.get(place));
    let existing = read_files(place.client, &paths)?;
    let confirmation = confirmation(
        home,
        place,
        &records,
        &paths,
        &texts(&existing),
        connection,
        details,
        tokens,
    )?;
    Ok(confirmation.needed().then_some(confirmation))
}

/// Values a write replaces, and whether it takes the client from another
/// token or another AstrLink.
#[allow(clippy::too_many_arguments)]
fn confirmation(
    home: Home,
    place: Place,
    records: &Records,
    paths: &[PathBuf],
    existing: &[Option<&str>],
    connection: &Connection,
    details: &Catalog,
    tokens: Option<&Tokens>,
) -> Result<Confirmation, ItemError> {
    let client = place.client;
    let record = records.get(place);
    let plan = plan_write_with(client, existing, connection, details, record)
        .map_err(|error| error.at(paths))?;
    let config = Config::parse(client, existing).map_err(|error| error.at(paths))?;
    let desired = client.desired_keys(connection, details);
    let state = place_state(records, place, existing, Some(connection.origin), tokens);
    let gateway = record
        .filter(|_| state == ClientState::Elsewhere)
        .map(|record| record.base_url.clone());
    let token_id = record
        .filter(|record| {
            record.token_id != connection.token_id
                && matches!(state, ClientState::Configured | ClientState::Outdated)
                && tokens.is_some_and(|tokens| tokens.own(record))
        })
        .map(|record| record.token_id.clone());
    Ok(Confirmation {
        client,
        wsl: home.distribution().map(str::to_string),
        target: place.target,
        dir: client.dir(home, place.target).display().to_string(),
        remove: false,
        keys: plan
            .conflicts
            .iter()
            .map(|key| {
                let next = desired
                    .iter()
                    .find(|(path, _)| path == key)
                    .map(|(_, value)| value);
                ReplacedKey {
                    path: paths[config.index(key)].display().to_string(),
                    key: key.clone(),
                    current: config
                        .get(key)
                        .flatten()
                        .and_then(|value| displayed_value(key, &value)),
                    next: next.and_then(|value| displayed_value(key, value)),
                    removed: next.is_none(),
                }
            })
            .collect(),
        gateway,
        token_id,
    })
}

pub(crate) fn read_files(client: Client, paths: &[PathBuf]) -> Result<Vec<Option<String>>, String> {
    paths
        .iter()
        .zip(client.files())
        .map(|(path, file)| file.read(path))
        .collect()
}

pub(crate) fn texts(files: &[Option<String>]) -> Vec<Option<&str>> {
    files.iter().map(Option::as_deref).collect()
}

/// Writes each file whose contents changed. When one fails, the files
/// already written get their old contents back.
fn write_files(
    client: Client,
    paths: &[PathBuf],
    existing: &[Option<String>],
    contents: &[String],
) -> Result<(), String> {
    let mut written: Vec<usize> = Vec::new();
    for (index, ((path, before), after)) in paths.iter().zip(existing).zip(contents).enumerate() {
        let before = before.as_deref();
        if before.unwrap_or_default() == after {
            continue;
        }
        let file = client.files()[index];
        let after = (!after.is_empty()).then_some(after.as_str());
        if let Err(error) = file.write(path, before, after, true) {
            for index in written.into_iter().rev() {
                let after = &contents[index];
                let _ = client.files()[index].write(
                    &paths[index],
                    (!after.is_empty()).then_some(after.as_str()),
                    existing[index].as_deref(),
                    true,
                );
            }
            return Err(error);
        }
        written.push(index);
    }
    Ok(())
}

/// `write_with` for models the official catalog lists nothing for.
#[cfg(test)]
pub(crate) fn write(
    home: Home,
    place: impl Into<Place>,
    connection: &Connection,
    tokens: &Tokens,
    replace: bool,
) -> Result<ApplyOutcome, ItemError> {
    write_with(home, place, connection, &Catalog::new(), tokens, replace)
}

/// `tokens` are the gateway's, so a config another AstrLink or another
/// token wrote moves here only once the user confirms. `details` is what a
/// list entry for each model says besides its id.
pub(crate) fn write_with(
    home: Home,
    place: impl Into<Place>,
    connection: &Connection,
    details: &Catalog,
    tokens: &Tokens,
    replace: bool,
) -> Result<ApplyOutcome, ItemError> {
    let place = place.into();
    let client = place.client;
    if !client.detected(home, place.target) {
        return Err("this client is not installed for the current user".into());
    }
    if client.running(home, place.target) {
        return Err(open_error(client));
    }
    let _lock = host_files::lock();
    let mut records = read_records(&home)?;
    let paths = client.config_paths(home, place.target, records.get(place));
    let existing = read_files(place.client, &paths)?;
    let confirmation = confirmation(
        home,
        place,
        &records,
        &paths,
        &texts(&existing),
        connection,
        details,
        Some(tokens),
    )?;
    if confirmation.needed() && !replace {
        return Ok(ApplyOutcome::NeedsConfirmation(confirmation));
    }
    let mut plan = plan_write_with(
        client,
        &texts(&existing),
        connection,
        details,
        records.get(place),
    )
    .map_err(|error| error.at(&paths))?;
    plan.record.file = client.recorded_file(&paths);
    let previous = records.set(place, Some(plan.record));
    write_records(&home, &mut records)?;
    if let Err(error) = write_files(client, &paths, &existing, &plan.contents) {
        records.set(place, previous);
        let _ = write_records(&home, &mut records);
        return Err(error.into());
    }
    Ok(ApplyOutcome::Applied)
}

fn open_error(client: Client) -> ItemError {
    format!("{} is open; quit it and try again", client.name()).into()
}

/// Removes what AstrLink wrote and puts back the values it replaced.
pub fn remove(home: Home, place: impl Into<Place>) -> Result<(), ItemError> {
    let place = place.into();
    let _lock = host_files::lock();
    let mut records = read_records(&home)?;
    let Some(record) = records.get(place).cloned() else {
        return Ok(());
    };
    if place.client.running(home, place.target) {
        return Err(open_error(place.client));
    }
    let paths = place.client.config_paths(home, place.target, Some(&record));
    let existing = read_files(place.client, &paths)?;
    let removals =
        plan_remove(place.client, &texts(&existing), &record).map_err(|error| error.at(&paths))?;
    let files = place.client.files();
    for (((path, existing), removal), file) in paths.iter().zip(&existing).zip(removals).zip(files)
    {
        match removal {
            Removal::Unchanged => {}
            Removal::Write(contents) => {
                file.write(path, existing.as_deref(), Some(&contents), file.secret())?
            }
            Removal::Delete => file.write(path, existing.as_deref(), None, file.secret())?,
        }
    }
    records.set(place, None);
    Ok(write_records(&home, &mut records)?)
}

/// Moves every untouched config the gateway wrote with one of its `tokens`
/// to its current address. Another AstrLink's configs stay pointed at it.
pub fn sync(home: Home, inference_url: &str, tokens: &Tokens) -> Result<(), String> {
    let origin = local_origin(inference_url)?;
    let _lock = host_files::lock();
    let mut records = read_records(&home)?;
    let mut errors = Vec::new();
    for place in records.places() {
        let Some(record) = records
            .get(place)
            .filter(|record| tokens.own(record))
            .cloned()
        else {
            continue;
        };
        let client = place.client;
        if client.running(home, place.target) {
            errors.push(open_error(client).message);
            continue;
        }
        let paths = client.config_paths(home, place.target, Some(&record));
        let result = (|| {
            let existing = read_files(place.client, &paths)?;
            let Some((contents, next)) = plan_sync(client, &texts(&existing), &record, &origin)
                .map_err(|error| String::from(error.at(&paths)))?
            else {
                return Ok(());
            };
            records.set(place, Some(next));
            write_records(&home, &mut records)?;
            let written = write_files(client, &paths, &existing, &contents);
            if written.is_err() {
                records.set(place, Some(record.clone()));
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
    use crate::client_config::test_support::*;

    #[test]
    fn apply_asks_before_replacing_and_writes_the_record_first() {
        let home = unique_home("apply");
        let settings = home.join(".claude/settings.json");
        let foreign = "{\n  \"env\": {\n    \"ANTHROPIC_API_KEY\": \"sk-secret\"\n  }\n}\n";
        fs::write(&settings, foreign).unwrap();
        let outcome = write(
            Home::local(&home),
            CLAUDE,
            &connection(&[]),
            &tokens(),
            false,
        )
        .unwrap();
        assert_eq!(
            asked(outcome),
            Some((vec!["env.ANTHROPIC_API_KEY".into()], None))
        );
        assert_eq!(fs::read_to_string(&settings).unwrap(), foreign);
        assert!(!records_path(&home).exists());

        let outcome = write(
            Home::local(&home),
            CLAUDE,
            &connection(&[]),
            &tokens(),
            true,
        )
        .unwrap();
        assert_eq!(outcome, ApplyOutcome::Applied);
        let written = fs::read_to_string(&settings).unwrap();
        assert!(!written.contains("sk-secret"));
        // The replaced key waits in the private record until removal.
        let records = fs::read_to_string(records_path(&home)).unwrap();
        assert!(!records.contains(TOKEN));
        assert!(records.contains("sk-secret"));
        assert!(records.contains("\"version\": 2"));
        assert!(records.contains("\"backup\""));
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

        remove(Home::local(&home), CLAUDE).unwrap();
        assert_eq!(json(&fs::read_to_string(&settings).unwrap()), json(foreign));
        assert!(!records_path(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn sync_leaves_modified_and_unreadable_configs_alone() {
        let home = unique_home("sync");
        write(
            Home::local(&home),
            CLAUDE,
            &connection(&[]),
            &tokens(),
            false,
        )
        .unwrap();
        write(
            Home::local(&home),
            CODEX,
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
        let error = remove(Home::local(&home), CODEX).unwrap_err();
        assert!(error.message.contains("config.toml") && error.message.contains("TOML"));
        assert!(!error.message.contains("astr_x"));
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn another_astrlinks_config_moves_here_only_once_confirmed() {
        let home = unique_home("elsewhere");
        let config = home.join(".codex/config.toml");
        let models = codex_models();
        let other = Tokens(BTreeMap::from([(
            "token_02".to_string(),
            hint(OTHER_TOKEN),
        )]));
        let theirs = Connection {
            token_id: "token_02",
            token: OTHER_TOKEN,
            origin: "http://127.0.0.1:8317",
            ..connection(&models)
        };
        write(Home::local(&home), CODEX, &theirs, &other, false).unwrap();
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
            asked(
                write(
                    Home::local(&home),
                    CODEX,
                    &connection(&models),
                    &tokens(),
                    false
                )
                .unwrap()
            ),
            Some((Vec::new(), Some("http://127.0.0.1:8317/v1".into())))
        );
        assert_eq!(fs::read_to_string(&config).unwrap(), written);
        write(
            Home::local(&home),
            CODEX,
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
    fn removal_leaves_a_connection_someone_else_replaced() {
        let home = unique_home("replaced-remove");
        let settings = home.join(".claude/settings.json");
        let models = vec![("model", "main-route".to_string())];
        write(
            Home::local(&home),
            CLAUDE,
            &connection(&models),
            &tokens(),
            false,
        )
        .unwrap();
        // A provider with another token keeps the address and model AstrLink
        // wrote, which removal would otherwise take for its own.
        let switched = fs::read_to_string(&settings)
            .unwrap()
            .replace(TOKEN, OTHER_TOKEN);
        fs::write(&settings, &switched).unwrap();
        assert_eq!(
            status(Home::local(&home), None, None).unwrap()[0].state,
            ClientState::Modified
        );
        remove(Home::local(&home), CLAUDE).unwrap();
        assert_eq!(fs::read_to_string(&settings).unwrap(), switched);
        assert!(!records_path(&home).exists());
        assert_eq!(
            status(Home::local(&home), None, None).unwrap()[0].state,
            ClientState::NotConfigured
        );
        fs::remove_dir_all(home).unwrap();
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
            CLAUDE,
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
    fn a_confirmation_shows_each_replaced_value_before_and_after() {
        let home = unique_home("before-after");
        fs::write(
            home.join(".claude/settings.json"),
            r#"{"env": {
                "ANTHROPIC_BASE_URL": "https://me:pw@relay.example/v1?key=abc",
                "ANTHROPIC_AUTH_TOKEN": "sk-relay-0123456789",
                "ANTHROPIC_MODEL": "relay-opus",
                "ANTHROPIC_DEFAULT_HAIKU_MODEL": "relay-haiku",
                "ANTHROPIC_CUSTOM_HEADERS": "{\"x\": 1}"
            }}"#,
        )
        .unwrap();
        let models = [("model", "main-route".to_string())];
        let ApplyOutcome::NeedsConfirmation(confirmation) = write(
            Home::local(&home),
            CLAUDE,
            &connection(&models),
            &tokens(),
            false,
        )
        .unwrap() else {
            panic!("expected a confirmation");
        };
        let shown = confirmation
            .keys
            .iter()
            .map(|key| {
                (
                    key.key.as_str(),
                    key.current.as_deref(),
                    key.next.as_deref(),
                    key.removed,
                )
            })
            .collect::<Vec<_>>();
        let token = TOKEN.chars().collect::<Vec<_>>();
        let token = format!(
            "{}…{}",
            token[..4].iter().collect::<String>(),
            token[token.len() - 3..].iter().collect::<String>()
        );
        let base_url = CLAUDE.base_url(ORIGIN);
        assert_eq!(
            shown,
            [
                // An address drops what may carry a credential.
                (
                    "env.ANTHROPIC_BASE_URL",
                    Some("https://relay.example/v1"),
                    Some(base_url.as_str()),
                    false
                ),
                (
                    "env.ANTHROPIC_AUTH_TOKEN",
                    Some("sk-r…789"),
                    Some(token.as_str()),
                    false
                ),
                (
                    "env.ANTHROPIC_MODEL",
                    Some("relay-opus"),
                    Some("main-route"),
                    false
                ),
                // A tier the write leaves empty goes; an object shows as
                // nothing.
                (
                    "env.ANTHROPIC_DEFAULT_HAIKU_MODEL",
                    Some("relay-haiku"),
                    None,
                    true
                ),
                ("env.ANTHROPIC_CUSTOM_HEADERS", None, None, true),
            ]
        );
        let all = format!("{:?}", confirmation.keys);
        assert!(!all.contains("sk-relay-0123456789") && !all.contains(TOKEN));
        assert!(!all.contains("pw") && !all.contains("abc"));
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn a_batch_asks_once_then_writes_each_client_on_its_own() {
        let home = unique_home("batch");
        fs::write(
            home.join(".claude/settings.json"),
            "{\"env\": {\"ANTHROPIC_API_KEY\": \"sk-secret\"}}",
        )
        .unwrap();
        let secret = Secret {
            id: "token_01".into(),
            token: TOKEN.into(),
        };
        let job = |client: Client, action: Action, model: Option<&str>| Job {
            home: Some(Home::local(&home)),
            client,
            target: None,
            wsl: None,
            action,
            selection: model.map(|model| {
                Selection::of(
                    &Models {
                        model: Some(model.into()),
                        ..Default::default()
                    },
                    client,
                )
                .unwrap()
            }),
            details: Catalog::new(),
        };
        let jobs = [
            job(CLAUDE, Action::Write, Some("main-route")),
            job(CODEX, Action::Write, Some("gpt-route")),
        ];
        let BatchOutcome::NeedsConfirmation { items } =
            run(&jobs, Some(&secret), ORIGIN, Some(&tokens()), false)
        else {
            panic!("expected a confirmation");
        };
        assert_eq!(items.len(), 1);
        assert_eq!(items[0].client, CLAUDE);
        assert_eq!(items[0].keys[0].key, "env.ANTHROPIC_API_KEY");
        // The key shows by its ends, as the diff does; AstrLink writes its
        // token as ANTHROPIC_AUTH_TOKEN, so the API key goes.
        assert_eq!(items[0].keys[0].current.as_deref(), Some("…et"));
        assert_eq!(items[0].keys[0].next, None);
        assert!(items[0].keys[0].removed);
        assert!(!records_path(&home).exists());
        assert!(!home.join(".codex/config.toml").exists());

        let BatchOutcome::Done { results } =
            run(&jobs, Some(&secret), ORIGIN, Some(&tokens()), true)
        else {
            panic!("expected results");
        };
        assert!(results.iter().all(|result| result.ok));
        assert!(fs::read_to_string(home.join(".codex/config.toml"))
            .unwrap()
            .contains(TOKEN));

        // A client that cannot be written leaves the others written; a
        // removal always asks first.
        let jobs = [
            job(CODEX, Action::Remove, None),
            job(OPENCLAW, Action::Write, Some("oc-route")),
        ];
        let BatchOutcome::NeedsConfirmation { items } =
            run(&jobs, Some(&secret), ORIGIN, Some(&tokens()), false)
        else {
            panic!("expected a confirmation");
        };
        assert!(items[0].remove);
        let BatchOutcome::Done { results } =
            run(&jobs, Some(&secret), ORIGIN, Some(&tokens()), true)
        else {
            panic!("expected results");
        };
        assert!(results[0].ok);
        assert!(!results[1].ok);
        assert!(results[1]
            .error
            .as_ref()
            .is_some_and(|error| !error.message.contains(TOKEN)));
        assert!(!home.join(".codex/config.toml").exists());
        fs::remove_dir_all(home).unwrap();
    }
}
