//! npm updates on Windows that never leave a half-installed client.
//!
//! npm replaces a global package by first renaming the installed package and
//! its command shims aside and only then downloading the new one. Stopped in
//! between (the update deadline, or quitting AstrLink), it leaves no command
//! at all; when the platform binary fails to download, it finishes anyway and
//! leaves a stub. Here npm installs into a staging prefix beside the live one.
//! Only a staged client that starts and reports the new version replaces the
//! live files, by renames that are undone on any failure and, after a crash,
//! on the next check.
//!
//! Windows moves a running program like any other file but deletes it only
//! once it exits. A running client keeps working from its moved files, the
//! next start finds the new version, and a later check deletes the old files.

use std::{
    fs, io,
    path::{Path, PathBuf},
    thread,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use semver::Version;
use serde::{Deserialize, Serialize};

use super::{
    detect, manifest_name, npm_command, process, running, version_within, ClientError, ClientId,
    ClientVersion, Environment, UPDATE_TIMEOUT,
};

/// AstrLink's work directory inside an npm prefix, beside the live files so
/// that moving between them is a rename on the same volume.
const WORK: &str = ".astrlink-update";
const JOURNAL: &str = "journal.json";
/// Held while an update or repair changes a client; see [`Claim`].
const CLAIM: &str = "claim";
/// A fresh 350 MB program can take a while on its first start while
/// antivirus scans it.
const VERIFY_TIMEOUT: Duration = Duration::from_secs(60);

/// Lets one update or repair at a time change a client's files and its work
/// directory. Another AstrLink, the installed app beside a development build,
/// may update the same client. The claim ends with the process however it
/// ends, so a claim that can be taken means no update is in progress.
struct Claim {
    file: PathBuf,
    _handle: fs::File,
}

impl Claim {
    /// Takes the claim on `work`, creating it, or `None` while another
    /// update holds it.
    fn take(work: &Path) -> io::Result<Option<Self>> {
        let file = work.join(CLAIM);
        let mut attempt = 0;
        loop {
            fs::create_dir_all(work)?;
            match Self::open(&file) {
                Ok(handle) => {
                    return Ok(handle.map(|handle| Self {
                        file,
                        _handle: handle,
                    }))
                }
                // The previous holder removed the emptied work directory.
                Err(error) if error.kind() == io::ErrorKind::NotFound && attempt < 3 => {
                    attempt += 1;
                }
                Err(error) => return Err(error),
            }
        }
    }

    /// Takes the claim on `work` or fails with `busy`.
    fn hold(work: &Path) -> Result<Self, ClientError> {
        match Self::take(work) {
            Ok(Some(claim)) => Ok(claim),
            Ok(None) => Err(ClientError::new(
                "busy",
                "Another update of this client is in progress",
            )),
            Err(error) => Err(ClientError::new("command", error)),
        }
    }

    /// An open file nobody else can open until the handle closes, which
    /// deletes it.
    #[cfg(windows)]
    fn open(file: &Path) -> io::Result<Option<fs::File>> {
        use std::os::windows::fs::OpenOptionsExt;
        const DELETE: u32 = 0x0001_0000;
        const GENERIC_WRITE: u32 = 0x4000_0000;
        const FILE_FLAG_DELETE_ON_CLOSE: u32 = 0x0400_0000;
        let mut wait = 0;
        loop {
            match fs::OpenOptions::new()
                .write(true)
                .create(true)
                .access_mode(DELETE | GENERIC_WRITE)
                .share_mode(0)
                .custom_flags(FILE_FLAG_DELETE_ON_CLOSE)
                .open(file)
            {
                Ok(handle) => return Ok(Some(handle)),
                // ERROR_SHARING_VIOLATION: another update holds it.
                Err(error) if error.raw_os_error() == Some(32) => return Ok(None),
                // ERROR_ACCESS_DENIED also while the previous holder's file
                // is being deleted; a lasting one is a permission problem.
                Err(error) if error.raw_os_error() == Some(5) && wait < 10 => {
                    wait += 1;
                    thread::sleep(Duration::from_millis(50));
                }
                Err(error) => return Err(error),
            }
        }
    }

    /// Other systems update clients in place and run this only in tests. A
    /// lock would pass to every child forked while it is held, so the claim
    /// names the process holding it instead.
    #[cfg(not(windows))]
    fn open(file: &Path) -> io::Result<Option<fs::File>> {
        use std::io::Write;
        for _ in 0..2 {
            match fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(file)
            {
                Ok(mut handle) => {
                    write!(handle, "{}", std::process::id())?;
                    return Ok(Some(handle));
                }
                Err(error) if error.kind() == io::ErrorKind::AlreadyExists => {
                    let owner = fs::read_to_string(file)
                        .ok()
                        .and_then(|text| text.trim().parse::<i32>().ok());
                    // SAFETY: signal 0 only checks that the process exists.
                    let alive = owner.is_some_and(|pid| {
                        pid > 0
                            && (unsafe { libc::kill(pid, 0) } == 0
                                || io::Error::last_os_error().raw_os_error() == Some(libc::EPERM))
                    });
                    if alive {
                        return Ok(None);
                    }
                    let _ = fs::remove_file(file);
                }
                Err(error) => return Err(error),
            }
        }
        Ok(None)
    }
}

impl Drop for Claim {
    fn drop(&mut self) {
        // Windows deletes the file when the handle closes.
        if !cfg!(windows) {
            let _ = fs::remove_file(&self.file);
        }
    }
}

/// Where an npm prefix keeps a client's packages and command shims.
#[derive(Clone, Debug)]
pub(super) struct Layout {
    modules: PathBuf,
    bins: PathBuf,
    /// The shims npm writes for the command; the first is the one PATH finds.
    shims: Vec<String>,
}

impl Layout {
    pub(super) fn new(environment: &Environment, prefix: &Path, id: ClientId) -> Self {
        let name = id.name();
        if environment.windows() {
            Self {
                modules: prefix.join("node_modules"),
                bins: prefix.into(),
                shims: vec![format!("{name}.cmd"), name.into(), format!("{name}.ps1")],
            }
        } else {
            Self {
                modules: prefix.join("lib").join("node_modules"),
                bins: prefix.join("bin"),
                shims: vec![name.into()],
            }
        }
    }
    fn package(&self, package: &str) -> PathBuf {
        package
            .split('/')
            .fold(self.modules.clone(), |path, part| path.join(part))
    }
    fn shim(&self) -> PathBuf {
        self.bins.join(&self.shims[0])
    }
}

/// The npm prefix whose `bins` directory is `directory`, with links
/// resolved as detection resolves an installed client: nvm-windows, for
/// one, puts a link to the active Node.js on PATH.
fn prefix_of(environment: &Environment, directory: &Path) -> Option<PathBuf> {
    let directory = dunce::canonicalize(directory).ok()?;
    if environment.windows() {
        Some(directory)
    } else {
        directory
            .ends_with("bin")
            .then(|| directory.parent().map(Path::to_path_buf))
            .flatten()
    }
}

fn exists(path: &Path) -> bool {
    fs::symlink_metadata(path).is_ok()
}

fn remove(path: &Path) {
    match fs::symlink_metadata(path) {
        Ok(meta) if meta.is_dir() => {
            let _ = fs::remove_dir_all(path);
        }
        Ok(_) => {
            let _ = fs::remove_file(path);
        }
        Err(_) => {}
    }
}

/// Renames, retrying while antivirus or the search indexer briefly holds a
/// file that was just written.
fn rename(from: &Path, to: &Path) -> io::Result<()> {
    let mut wait = Duration::from_millis(50);
    for _ in 0..8 {
        match fs::rename(from, to) {
            // ERROR_SHARING_VIOLATION and ERROR_LOCK_VIOLATION.
            Err(error)
                if cfg!(windows)
                    && (error.kind() == io::ErrorKind::PermissionDenied
                        || matches!(error.raw_os_error(), Some(32 | 33))) =>
            {
                thread::sleep(wait);
                wait = (wait * 2).min(Duration::from_secs(1));
            }
            result => return result,
        }
    }
    fs::rename(from, to)
}

fn now() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |d| d.as_millis())
}

/// The renames of one swap, saved before the first so that an interrupted
/// swap can be undone.
#[derive(Debug, Default, Serialize, Deserialize)]
struct Journal {
    run: PathBuf,
    /// Live entries moved aside: (live, saved).
    retired: Vec<(PathBuf, PathBuf)>,
    /// Entries moved into the live places: (from, live).
    placed: Vec<(PathBuf, PathBuf)>,
}

struct Swap {
    work: PathBuf,
    journal: Journal,
}

impl Swap {
    fn begin(work: &Path, journal: Journal) -> Result<Self, ClientError> {
        let text = serde_json::to_vec(&journal).map_err(|e| ClientError::new("command", e))?;
        // On disk before the first rename, so that power loss leaves either
        // no journal or a complete one.
        let temporary = work.join(format!("{JOURNAL}.tmp"));
        fs::File::create(&temporary)
            .and_then(|mut file| {
                use std::io::Write;
                file.write_all(&text)?;
                file.sync_all()
            })
            .and_then(|()| fs::rename(&temporary, work.join(JOURNAL)))
            .map_err(|e| ClientError::new("command", e))?;
        Ok(Self {
            work: work.into(),
            journal,
        })
    }
    fn apply(&self) -> io::Result<()> {
        for (live, saved) in &self.journal.retired {
            if exists(live) {
                if let Some(parent) = saved.parent() {
                    fs::create_dir_all(parent)?;
                }
                rename(live, saved)?;
            }
        }
        for (from, live) in &self.journal.placed {
            if let Some(parent) = live.parent() {
                fs::create_dir_all(parent)?;
            }
            rename(from, live)?;
        }
        Ok(())
    }
    /// Keeps the placed entries and deletes the retired ones. A running
    /// program among them stays until a check after it exits.
    fn finish(self) {
        let _ = fs::remove_file(self.work.join(JOURNAL));
        for (_, saved) in &self.journal.retired {
            remove(saved);
        }
    }
    /// Puts the live entries back and reports `code`, or `damaged` when that
    /// failed too.
    fn abort(self, code: &'static str, detail: impl std::fmt::Display) -> ClientError {
        match undo(&self.work, &self.journal) {
            Ok(()) => ClientError::new(code, detail),
            Err(error) => ClientError::new(
                "damaged",
                format!("{detail}\nThe previous files could not be put back: {error}"),
            ),
        }
    }
}

/// Reverses `journal`'s renames that took place, newest first, then forgets
/// the journal. Each rename either happened or not, so this also finishes
/// a swap, or an earlier undo, that stopped halfway.
fn undo(work: &Path, journal: &Journal) -> io::Result<()> {
    let mut failed = None;
    for (from, live) in journal.placed.iter().rev() {
        if !exists(from) && exists(live) {
            if let Err(error) = rename(live, from) {
                failed.get_or_insert(error);
            }
        }
    }
    // Put nothing back until every new entry has left: a later undo takes
    // what it finds in a live place for a new entry whose source is gone.
    if let Some(error) = failed {
        return Err(error);
    }
    for (index, (live, saved)) in journal.retired.iter().enumerate().rev() {
        if !exists(saved) {
            continue;
        }
        if exists(live) {
            let aside = journal.run.join(format!("discard-{index}"));
            if let Err(error) = rename(live, &aside) {
                failed.get_or_insert(error);
                continue;
            }
        }
        if let Err(error) = rename(saved, live) {
            failed.get_or_insert(error);
        }
    }
    match failed {
        Some(error) => Err(error),
        None => {
            let _ = fs::remove_file(work.join(JOURNAL));
            Ok(())
        }
    }
}

/// Undoes a swap that AstrLink did not finish, then deletes what earlier
/// runs left. The caller holds the claim on `work`.
fn recover(work: &Path) -> Result<(), ClientError> {
    if let Ok(raw) = fs::read(work.join(JOURNAL)) {
        let journal: Journal = serde_json::from_slice(&raw).map_err(|e| {
            ClientError::new("damaged", format!("{}: {e}", work.join(JOURNAL).display()))
        })?;
        undo(work, &journal).map_err(|e| {
            ClientError::new(
                "damaged",
                format!("An interrupted update could not be undone: {e}"),
            )
        })?;
    }
    let Ok(entries) = fs::read_dir(work) else {
        return Ok(());
    };
    for entry in entries.flatten() {
        let path = entry.path();
        let run = entry
            .file_name()
            .to_str()
            .is_some_and(|n| n.parse::<u128>().is_ok());
        if run && path.is_dir() {
            remove(&path);
        }
    }
    Ok(())
}

fn work_dir(prefix: &Path, id: ClientId) -> PathBuf {
    prefix.join(WORK).join(id.name())
}

/// Removes the work directories once nothing is left in them.
fn tidy(prefix: &Path, id: ClientId) {
    if fs::remove_dir(work_dir(prefix, id)).is_ok() {
        let _ = fs::remove_dir(prefix.join(WORK));
    }
}

/// Undoes interrupted swaps in every prefix on PATH. Runs before detection,
/// so that a client caught mid-swap is found as it was before the update.
pub(super) fn recover_all(environment: &Environment, id: ClientId) {
    for directory in std::env::split_paths(&environment.path) {
        let Some(prefix) = prefix_of(environment, &directory) else {
            continue;
        };
        let work = work_dir(&prefix, id);
        if !work.is_dir() {
            continue;
        }
        // An update in progress elsewhere cleans up after itself.
        let Ok(Some(claim)) = Claim::take(&work) else {
            continue;
        };
        let result = recover(&work);
        drop(claim);
        match result {
            Ok(()) => tidy(&prefix, id),
            Err(error) => eprintln!(
                "unable to undo an update of {}: {}",
                id.name(),
                error.detail
            ),
        }
    }
}

/// The client in `layout`'s prefix, if it starts there and reports
/// `expected` (when given).
fn verify(
    environment: &Environment,
    id: ClientId,
    layout: &Layout,
    expected: Option<&ClientVersion>,
) -> Result<ClientVersion, ClientError> {
    let incomplete = |detail: String| ClientError::new("incomplete", detail);
    let path = std::env::join_paths(
        std::iter::once(layout.bins.clone()).chain(std::env::split_paths(&environment.path)),
    )
    .map_err(|e| ClientError::new("command", e))?;
    let scoped = Environment {
        path,
        ..environment.clone()
    };
    let install = detect(&scoped, id)
        .filter(|i| i.executable.parent() == Some(layout.bins.as_path()))
        .ok_or_else(|| incomplete(format!("{} was not installed", layout.shim().display())))?;
    if !install.resolved.starts_with(&layout.modules) {
        return Err(incomplete(format!(
            "{} does not start the installed package",
            install.executable.display()
        )));
    }
    let version = version_within(&scoped, id, &install, VERIFY_TIMEOUT).map_err(|e| {
        incomplete(format!(
            "{} did not start: {}",
            install.executable.display(),
            e.detail
        ))
    })?;
    match expected {
        Some(expected) if version != *expected => Err(incomplete(format!(
            "{} reports {version} instead of {expected}",
            install.executable.display()
        ))),
        _ => Ok(version),
    }
}

/// Installs `latest` into a staging prefix, checks it, then swaps it in.
/// Also repairs a broken installation: whatever is live is replaced.
pub(super) fn update(
    environment: &Environment,
    id: ClientId,
    prefix: &Path,
    latest: &ClientVersion,
) -> Result<ClientVersion, ClientError> {
    let work = work_dir(prefix, id);
    let claim = Claim::hold(&work)?;
    // Created after `recover`, which deletes earlier runs.
    let run = work.join(now().to_string());
    let result = (|| {
        recover(&work)?;
        let layout = Layout::new(environment, prefix, id);
        let stage = run.join("stage");
        fs::create_dir_all(&stage).map_err(|e| ClientError::new("command", e))?;
        let command = npm_command(environment, id, prefix, &stage, latest)?;
        let output = process::run(environment, &command, UPDATE_TIMEOUT)?;
        let staged = Layout::new(environment, &stage, id);
        verify(environment, id, &staged, Some(latest)).map_err(|error| {
            ClientError::new(
                "incomplete",
                format!("{}\n{}", error.detail, output.trim()).trim(),
            )
        })?;
        let mut journal = Journal {
            run: run.clone(),
            ..Journal::default()
        };
        retire_live(&layout, id, &run, &mut journal);
        for package in id.packages() {
            let from = staged.package(package);
            if exists(&from) {
                journal.placed.push((from, layout.package(package)));
            }
        }
        for shim in &staged.shims {
            let from = staged.bins.join(shim);
            if exists(&from) {
                journal.placed.push((from, layout.bins.join(shim)));
            }
        }
        swap(
            environment,
            id,
            &layout,
            &work,
            journal,
            Some(latest),
            "incomplete",
        )
    })();
    finish_run(prefix, id, &run, claim);
    result
}

/// Ends a run: deletes it unless a swap could not be undone, whose journal
/// still needs it, then lets go of the client and tidies up.
fn finish_run(prefix: &Path, id: ClientId, run: &Path, claim: Claim) {
    if !work_dir(prefix, id).join(JOURNAL).exists() {
        remove(run);
    }
    drop(claim);
    tidy(prefix, id);
}

/// Plans moving the live packages and shims into `run`.
fn retire_live(layout: &Layout, id: ClientId, run: &Path, journal: &mut Journal) {
    let saved = run.join("previous");
    for (index, package) in id.packages().iter().enumerate() {
        let live = layout.package(package);
        if exists(&live) {
            journal
                .retired
                .push((live, saved.join(format!("package-{index}"))));
        }
    }
    for shim in &layout.shims {
        let live = layout.bins.join(shim);
        if exists(&live) {
            journal.retired.push((live, saved.join(shim)));
        }
    }
}

/// Applies `journal`, checks the client, and keeps the result or undoes it.
fn swap(
    environment: &Environment,
    id: ClientId,
    layout: &Layout,
    work: &Path,
    journal: Journal,
    expected: Option<&ClientVersion>,
    unverified: &'static str,
) -> Result<ClientVersion, ClientError> {
    let swap = Swap::begin(work, journal)?;
    if let Err(error) = swap.apply() {
        // A running program moves, but not a file some program opened
        // without letting others move it.
        let moved = swap.journal.retired.iter().chain(&swap.journal.placed);
        let paths: Vec<PathBuf> = moved.flat_map(|(a, b)| [a.clone(), b.clone()]).collect();
        let (code, detail) = match running::refuse(&paths) {
            Err(running) => ("running", format!("{error}\n{}", running.detail)),
            Ok(()) if error.kind() == io::ErrorKind::PermissionDenied => {
                ("permission", error.to_string())
            }
            Ok(()) => ("command", error.to_string()),
        };
        return Err(swap.abort(code, detail));
    }
    // npm's cache of the global tree now describes the old files.
    let _ = fs::remove_file(layout.modules.join(".package-lock.json"));
    match verify(environment, id, layout, expected) {
        Ok(version) => {
            swap.finish();
            remove_npm_leftovers(layout, id);
            Ok(version)
        }
        Err(error) => Err(swap.abort(unverified, error.detail)),
    }
}

/// npm names a retired entry `.<name>-<8 letters or digits>` beside it.
fn npm_retired(entry: &str, name: &str) -> bool {
    entry
        .strip_prefix('.')
        .and_then(|rest| rest.strip_prefix(name))
        .and_then(|rest| rest.strip_prefix('-'))
        .is_some_and(|hash| hash.len() == 8 && hash.bytes().all(|b| b.is_ascii_alphanumeric()))
}

/// npm's retired copies of `path`: same directory, retired name.
fn retired_copies(path: &Path) -> Vec<PathBuf> {
    let (Some(parent), Some(name)) = (path.parent(), path.file_name().and_then(|n| n.to_str()))
    else {
        return vec![];
    };
    let Ok(entries) = fs::read_dir(parent) else {
        return vec![];
    };
    let mut found: Vec<(SystemTime, PathBuf)> = entries
        .flatten()
        .filter(|e| e.file_name().to_str().is_some_and(|n| npm_retired(n, name)))
        .map(|e| {
            let modified = e
                .metadata()
                .and_then(|m| m.modified())
                .unwrap_or(UNIX_EPOCH);
            (modified, e.path())
        })
        .collect();
    // Newest first.
    found.sort_by_key(|(modified, _)| std::cmp::Reverse(*modified));
    found.into_iter().map(|(_, path)| path).collect()
}

/// Deletes what an interrupted npm left of the client, once it starts again.
/// A file still in use stays until a later update.
fn remove_npm_leftovers(layout: &Layout, id: ClientId) {
    let packages = id.packages().iter().map(|p| layout.package(p));
    let shims = layout.shims.iter().map(|s| layout.bins.join(s));
    for path in packages.chain(shims) {
        for copy in retired_copies(&path) {
            remove(&copy);
        }
    }
}

/// The command a package's manifest installs for `id`.
fn bin_entry(environment: &Environment, package_dir: &Path, id: ClientId) -> Option<PathBuf> {
    let raw = environment.read(&package_dir.join("package.json"))?;
    let value: serde_json::Value = serde_json::from_str(&raw).ok()?;
    let entry = match &value["bin"] {
        serde_json::Value::String(entry) => entry.as_str(),
        bins => bins[id.name()].as_str()?,
    };
    Some(
        entry
            .split(['/', '\\'])
            .filter(|p| !p.is_empty() && *p != "." && *p != "..")
            .fold(package_dir.to_path_buf(), |path, part| path.join(part)),
    )
}

/// The package's version if its command file is in place.
fn complete(environment: &Environment, package_dir: &Path, id: ClientId) -> Result<String, String> {
    let raw = environment
        .read(&package_dir.join("package.json"))
        .ok_or_else(|| format!("{} has no package.json", package_dir.display()))?;
    let value: serde_json::Value = serde_json::from_str(&raw).map_err(|e| e.to_string())?;
    let version = value["version"].as_str().unwrap_or("?").to_string();
    let entry = bin_entry(environment, package_dir, id)
        .ok_or_else(|| format!("{} names no {} command", package_dir.display(), id.name()))?;
    if !entry.is_file() {
        return Err(format!("{} is missing", entry.display()));
    }
    // Claude Code keeps a text stub at bin/claude.exe until its native
    // binary is in place.
    if environment.windows()
        && entry
            .extension()
            .is_some_and(|e| e.eq_ignore_ascii_case("exe"))
        && !process::windows_image(&entry).unwrap_or(false)
    {
        return Err(format!("{} is not a Windows program", entry.display()));
    }
    Ok(version)
}

/// A previous copy npm left aside that can be put back.
#[derive(Clone, Debug)]
pub(super) struct Restore {
    pub version: String,
    package: &'static str,
    from: PathBuf,
    /// Missing live shims and the retired copies that replace them.
    shims: Vec<(PathBuf, PathBuf)>,
}

/// An npm installation that cannot start.
#[derive(Clone, Debug)]
pub(super) struct Damage {
    pub prefix: PathBuf,
    pub executable: PathBuf,
    pub restore: Option<Restore>,
    pub detail: String,
}

/// What is wrong with the client's npm installation in `prefix`; `None` when
/// nothing of it is there, or it looks complete and did not fail to start.
/// `failed` is how the client found there failed to report its version.
pub(super) fn damage(
    environment: &Environment,
    id: ClientId,
    prefix: &Path,
    failed: Option<&ClientError>,
) -> Option<Damage> {
    let layout = Layout::new(environment, prefix, id);
    let live = id.packages().iter().find_map(|package| {
        let dir = layout.package(package);
        (manifest_name(environment, &dir).as_deref() == Some(*package)).then_some(dir)
    });
    // The newest complete copy npm retired.
    let previous = id
        .packages()
        .iter()
        .flat_map(|package| {
            retired_copies(&layout.package(package))
                .into_iter()
                .filter(|dir| manifest_name(environment, dir).as_deref() == Some(*package))
                .filter_map(|dir| {
                    complete(environment, &dir, id)
                        .ok()
                        .map(|version| (*package, dir, version))
                })
                .collect::<Vec<_>>()
        })
        .max_by_key(|(_, _, version)| Version::parse(version).ok());
    if live.is_none() && previous.is_none() {
        return None;
    }
    // Only a start that failed for files missing from the package is
    // damage. A slow first start while antivirus scans the program, a
    // missing or too old Node.js, or a client's own error are not, and
    // installing it again would not help.
    let mut notes: Vec<String> = failed
        .filter(|e| missing_files(e))
        .map(|e| e.detail.clone())
        .into_iter()
        .collect();
    let shim = layout.shim();
    if !exists(&shim) {
        notes.push(format!("{} is missing", shim.display()));
    }
    match &live {
        Some(dir) => {
            if let Err(problem) = complete(environment, dir, id) {
                notes.push(problem);
            }
        }
        None => notes.push(format!(
            "{} is missing",
            layout.package(id.packages()[0]).display()
        )),
    }
    // A stub found by both the failed start and the file check is one note.
    let mut seen = std::collections::HashSet::new();
    notes.retain(|note| seen.insert(note.clone()));
    if notes.is_empty() {
        return None;
    }
    let restore = previous.and_then(|(package, from, version)| {
        let mut shims = Vec::new();
        for (index, name) in layout.shims.iter().enumerate() {
            let live = layout.bins.join(name);
            if exists(&live) {
                continue;
            }
            match retired_copies(&live).into_iter().next() {
                Some(copy) => shims.push((copy, live)),
                // Without the command PATH finds, putting back is no repair.
                None if index == 0 => return None,
                None => {}
            }
        }
        notes.push(format!(
            "The previous version {version} is still in {}",
            from.display()
        ));
        Some(Restore {
            version,
            package,
            from,
            shims,
        })
    });
    // A copy npm left behind once, after updating a running client, is no
    // sign of an installation when the package itself is gone: it may have
    // been uninstalled. An interrupted update leaves its shims aside too.
    if live.is_none() && restore.is_none() {
        return None;
    }
    Some(Damage {
        prefix: prefix.into(),
        executable: shim,
        restore,
        detail: notes.join("\n"),
    })
}

/// Whether a client failed to start because files of its package are
/// missing: a stub left where Claude Code's native program belongs, or a
/// module or platform package npm did not install.
fn missing_files(error: &ClientError) -> bool {
    error.code == "damaged"
        || (error.code == "command"
            && [
                "native binary not installed",
                "Missing optional dependency",
                "Cannot find module",
            ]
            .iter()
            .any(|marker| error.detail.contains(marker)))
}

/// A broken npm installation in a prefix on PATH, for a client PATH no
/// longer finds. Only a prefix npm installs global commands into counts, not
/// a project folder that happens to be on PATH.
pub(super) fn find_damage(environment: &Environment, id: ClientId) -> Option<Damage> {
    let mut seen = std::collections::HashSet::new();
    std::env::split_paths(&environment.path)
        .filter(|p| p.is_absolute())
        .filter_map(|directory| prefix_of(environment, &directory))
        .filter(|prefix| seen.insert(prefix.clone()))
        .filter(|prefix| global_prefix(environment, prefix))
        .find_map(|prefix| damage(environment, id, &prefix, None))
}

/// Whether npm installs global commands into `prefix`: it holds npm itself,
/// or command shims npm wrote there, retired ones included.
fn global_prefix(environment: &Environment, prefix: &Path) -> bool {
    let layout = Layout::new(environment, prefix, ClientId::Claude);
    if layout.modules.join("npm/bin/npm-cli.js").is_file() {
        return true;
    }
    let Ok(entries) = fs::read_dir(&layout.bins) else {
        return false;
    };
    entries.flatten().any(|entry| {
        let path = entry.path();
        let target = match fs::read_link(&path) {
            Ok(target) => target.to_string_lossy().into_owned(),
            Err(_) => {
                let name = entry.file_name().to_string_lossy().to_ascii_lowercase();
                let small = entry.metadata().is_ok_and(|m| m.len() < 16_384);
                if !small || !(name.contains(".cmd") || name.contains(".ps1")) {
                    return false;
                }
                fs::read_to_string(&path).unwrap_or_default()
            }
        };
        target.contains("node_modules")
    })
}

/// Puts back the copy npm retired when it was stopped.
pub(super) fn restore(
    environment: &Environment,
    id: ClientId,
    damage: &Damage,
) -> Result<ClientVersion, ClientError> {
    let Some(restore) = &damage.restore else {
        return Err(ClientError::new("unsupported", "Nothing to put back"));
    };
    let prefix = &damage.prefix;
    let work = work_dir(prefix, id);
    let claim = Claim::hold(&work)?;
    let run = work.join(now().to_string());
    let result = (|| {
        recover(&work)?;
        put_back(environment, id, prefix, &run, restore)
    })();
    finish_run(prefix, id, &run, claim);
    result
}

fn put_back(
    environment: &Environment,
    id: ClientId,
    prefix: &Path,
    run: &Path,
    restore: &Restore,
) -> Result<ClientVersion, ClientError> {
    let work = work_dir(prefix, id);
    if !exists(&restore.from) {
        return Err(ClientError::new("changed", "Client installation changed"));
    }
    let layout = Layout::new(environment, prefix, id);
    fs::create_dir_all(run).map_err(|e| ClientError::new("command", e))?;
    let mut journal = Journal {
        run: run.to_path_buf(),
        ..Journal::default()
    };
    // Only the package moves; live shims are left as they are.
    for (index, package) in id.packages().iter().enumerate() {
        let live = layout.package(package);
        if exists(&live) {
            journal
                .retired
                .push((live, run.join("previous").join(format!("package-{index}"))));
        }
    }
    journal
        .placed
        .push((restore.from.clone(), layout.package(restore.package)));
    journal.placed.extend(restore.shims.iter().cloned());
    let expected = ClientVersion::parse(id, &restore.version);
    // Nothing was downloaded; a copy that does not start is not a network
    // problem.
    swap(
        environment,
        id,
        &layout,
        &work,
        journal,
        expected.as_ref(),
        "command",
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn recognizes_npm_retired_names() {
        assert!(npm_retired(".claude-code-1iARp2zF", "claude-code"));
        assert!(npm_retired(".claude.cmd-DH0gY7Mn", "claude.cmd"));
        for other in [
            "claude-code",
            ".claude-code-1iARp2z",
            ".claude-code-1iARp2zF9",
            ".claude.cmd-DH0gY7Mn",
            ".claude-code-1iAR+2zF",
        ] {
            assert!(!npm_retired(other, "claude-code"), "{other}");
        }
        assert!(!npm_retired(".claude.cmd-DH0gY7Mn", "claude"));
    }

    fn tree(root: &Path) -> Vec<String> {
        let mut found: Vec<String> = walk(root)
            .into_iter()
            .map(|p| {
                let text = p
                    .strip_prefix(root)
                    .unwrap()
                    .to_string_lossy()
                    .replace('\\', "/");
                let contents = fs::read_to_string(&p).unwrap_or_default();
                format!("{text}={contents}")
            })
            .collect();
        found.sort();
        found
    }
    fn walk(root: &Path) -> Vec<PathBuf> {
        let mut files = Vec::new();
        for entry in fs::read_dir(root).into_iter().flatten().flatten() {
            let path = entry.path();
            if path.is_dir() {
                files.extend(walk(&path));
            } else {
                files.push(path);
            }
        }
        files
    }

    /// A prefix with an old client, a staged new one swapped in, and the
    /// journal of that swap, as AstrLink leaves it when it stops right after.
    struct Swapped {
        root: PathBuf,
        prefix: PathBuf,
        work: PathBuf,
        run: PathBuf,
    }

    impl Swapped {
        fn new(name: &str) -> Self {
            let root = std::env::temp_dir().join(format!(
                "astrlink-{name}-{}-{}",
                std::process::id(),
                now()
            ));
            let prefix = root.join("prefix");
            let work = prefix.join(WORK).join("claude");
            let run = work.join("1");
            fs::create_dir_all(prefix.join("pkg")).unwrap();
            fs::write(prefix.join("pkg/version"), "old").unwrap();
            fs::write(prefix.join("claude"), "old shim").unwrap();
            fs::create_dir_all(run.join("stage/pkg")).unwrap();
            fs::write(run.join("stage/pkg/version"), "new").unwrap();
            fs::write(run.join("stage/claude"), "new shim").unwrap();
            let swap = Swap::begin(
                &work,
                Journal {
                    run: run.clone(),
                    retired: vec![
                        (prefix.join("pkg"), run.join("previous/package-0")),
                        (prefix.join("claude"), run.join("previous/claude")),
                    ],
                    placed: vec![
                        (run.join("stage/pkg"), prefix.join("pkg")),
                        (run.join("stage/claude"), prefix.join("claude")),
                    ],
                },
            )
            .unwrap();
            swap.apply().unwrap();
            Self {
                root,
                prefix,
                work,
                run,
            }
        }
        fn live(&self) -> Vec<String> {
            tree(&self.prefix)
                .into_iter()
                .filter(|e| !e.starts_with(WORK))
                .collect()
        }
        /// What a check runs first. Windows finds the prefix on PATH itself,
        /// other systems its `bin` folder.
        fn recover_all(&self) {
            let bins = if cfg!(windows) {
                self.prefix.clone()
            } else {
                self.prefix.join("bin")
            };
            fs::create_dir_all(&bins).unwrap();
            let environment = Environment {
                home: self.root.clone(),
                path: std::env::join_paths([bins]).unwrap(),
                use_system_proxy: false,
                proxy_env: None,
                wsl: None,
            };
            super::recover_all(&environment, ClientId::Claude);
        }
    }

    impl Drop for Swapped {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }

    #[test]
    fn leaves_a_swap_alone_while_another_update_holds_the_client() {
        let swapped = Swapped::new("claim");
        let new = ["claude=new shim", "pkg/version=new"];
        let other = Claim::hold(&swapped.work).unwrap();

        // A check, an update or a repair meanwhile changes nothing.
        swapped.recover_all();
        assert_eq!(swapped.live(), new);
        assert!(swapped.work.join(JOURNAL).exists() && swapped.run.is_dir());
        assert_eq!(Claim::hold(&swapped.work).err().unwrap().code, "busy");

        // Once that update ends, however it ends, the next check undoes the
        // swap and removes what it left.
        drop(other);
        swapped.recover_all();
        assert_eq!(swapped.live(), ["claude=old shim", "pkg/version=old"]);
        assert!(!swapped.prefix.join(WORK).exists());
    }

    #[cfg(windows)]
    fn environment(home: &Path, prefix: &Path) -> Environment {
        Environment {
            home: home.into(),
            path: std::env::join_paths([prefix]).unwrap(),
            use_system_proxy: false,
            proxy_env: None,
            wsl: None,
        }
    }

    /// A running client moves aside with its files, keeps running, and
    /// its old files go at the first check after it exits.
    #[cfg(windows)]
    #[test]
    fn swaps_a_running_client_and_deletes_its_files_once_it_exits() {
        let root = std::env::temp_dir().join(format!(
            "astrlink-running-swap-{}-{}",
            std::process::id(),
            now()
        ));
        let prefix = root.join("prefix");
        let work = work_dir(&prefix, ClientId::Claude);
        let program = prefix.join(r"pkg\bin\claude.exe");
        fs::create_dir_all(program.parent().unwrap()).unwrap();
        let system = std::env::var_os("SystemRoot").unwrap();
        fs::copy(Path::new(&system).join(r"System32\PING.EXE"), &program).unwrap();
        fs::write(prefix.join("claude.cmd"), "old shim").unwrap();
        let mut client = std::process::Command::new(&program)
            .args(["-n", "60", "127.0.0.1"])
            .stdout(std::process::Stdio::null())
            .spawn()
            .unwrap();

        let claim = Claim::hold(&work).unwrap();
        let run = work.join("1");
        fs::create_dir_all(run.join(r"stage\pkg")).unwrap();
        fs::write(run.join(r"stage\pkg\version"), "new").unwrap();
        fs::write(run.join(r"stage\claude.cmd"), "new shim").unwrap();
        let swap = Swap::begin(
            &work,
            Journal {
                run: run.clone(),
                retired: vec![
                    (prefix.join("pkg"), run.join(r"previous\package-0")),
                    (prefix.join("claude.cmd"), run.join(r"previous\claude.cmd")),
                ],
                placed: vec![
                    (run.join(r"stage\pkg"), prefix.join("pkg")),
                    (run.join(r"stage\claude.cmd"), prefix.join("claude.cmd")),
                ],
            },
        )
        .unwrap();
        swap.apply().unwrap();
        swap.finish();
        finish_run(&prefix, ClientId::Claude, &run, claim);

        let live: Vec<String> = tree(&prefix)
            .into_iter()
            .filter(|e| !e.starts_with(WORK))
            .collect();
        assert_eq!(live, ["claude.cmd=new shim", "pkg/version=new"]);
        let running = client.try_wait().unwrap().is_none();
        let moved = run.join(r"previous\package-0\bin\claude.exe");
        let kept = moved.exists();
        // A check meanwhile leaves it, and the new files, alone.
        recover_all(&environment(&root, &prefix), ClientId::Claude);
        let kept_by_check = moved.exists() && prefix.join("pkg").join("version").exists();
        let still_running = client.try_wait().unwrap().is_none();
        let _ = client.kill();
        let _ = client.wait();
        assert!(running && still_running, "the client stopped");
        assert!(kept, "the running program was not moved aside");
        assert!(
            kept_by_check,
            "a check removed the running program or the new files"
        );

        recover_all(&environment(&root, &prefix), ClientId::Claude);
        let left = prefix.join(WORK).exists();
        let _ = fs::remove_dir_all(&root);
        assert!(!left, "the old files outlived the client");
    }

    /// An undo that could not move a new entry out tries again later and
    /// never takes the restored old one for it.
    #[cfg(unix)]
    #[test]
    fn an_undo_that_stopped_halfway_can_run_again() {
        use std::os::unix::fs::PermissionsExt;
        let swapped = Swapped::new("undo");
        let stage = swapped.run.join("stage");
        fs::set_permissions(&stage, fs::Permissions::from_mode(0o555)).unwrap();
        assert_eq!(recover(&swapped.work).unwrap_err().code, "damaged");
        fs::set_permissions(&stage, fs::Permissions::from_mode(0o755)).unwrap();
        assert!(swapped.work.join(JOURNAL).exists());

        recover(&swapped.work).unwrap();
        assert_eq!(swapped.live(), ["claude=old shim", "pkg/version=old"]);
        assert!(!swapped.work.join(JOURNAL).exists());
    }

    #[test]
    fn undoes_a_swap_however_far_it_got() {
        // Each rename of a swap either happened or not when AstrLink stopped.
        for done in 0..=4 {
            let root = std::env::temp_dir().join(format!(
                "astrlink-swap-{}-{done}-{}",
                std::process::id(),
                now()
            ));
            let live = root.join("prefix");
            let work = live.join(WORK).join("claude");
            let run = work.join("1");
            fs::create_dir_all(live.join("pkg")).unwrap();
            fs::write(live.join("pkg/version"), "old").unwrap();
            fs::write(live.join("claude"), "old shim").unwrap();
            fs::create_dir_all(run.join("stage/pkg")).unwrap();
            fs::write(run.join("stage/pkg/version"), "new").unwrap();
            fs::write(run.join("stage/claude"), "new shim").unwrap();
            let journal = Journal {
                run: run.clone(),
                retired: vec![
                    (live.join("pkg"), run.join("previous/package-0")),
                    (live.join("claude"), run.join("previous/claude")),
                ],
                placed: vec![
                    (run.join("stage/pkg"), live.join("pkg")),
                    (run.join("stage/claude"), live.join("claude")),
                ],
            };
            let swap = Swap::begin(&work, journal).unwrap();
            let moves: Vec<(PathBuf, PathBuf)> = swap
                .journal
                .retired
                .iter()
                .cloned()
                .chain(swap.journal.placed.iter().cloned())
                .collect();
            for (from, to) in &moves[..done] {
                fs::create_dir_all(to.parent().unwrap()).unwrap();
                fs::rename(from, to).unwrap();
            }
            recover(&work).unwrap();
            assert!(!work.join(JOURNAL).exists(), "{done}");
            assert_eq!(fs::read_to_string(live.join("pkg/version")).unwrap(), "old");
            assert_eq!(fs::read_to_string(live.join("claude")).unwrap(), "old shim");
            // No update holds the run any more, so it goes with the new files.
            assert_eq!(
                tree(&live),
                ["claude=old shim", "pkg/version=old"],
                "{done}"
            );
            fs::remove_dir_all(&root).unwrap();
        }
    }
}
