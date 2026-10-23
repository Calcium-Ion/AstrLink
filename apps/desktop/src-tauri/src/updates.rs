//! Application updates belong to the desktop host, never to the inference gateway.
use std::{
    fs,
    path::PathBuf,
    sync::{Arc, Mutex, MutexGuard},
    time::{Duration, Instant},
};

use base64::{engine::general_purpose::STANDARD, Engine};
use minisign_verify::{PublicKey, Signature};
use semver::Version;
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Manager, State};
use tauri_plugin_updater::{Update, UpdaterExt};
use tokio::sync::{Mutex as AsyncMutex, Notify};

use crate::{i18n, preferences::PreferencesStore, sidecar::CoreManager};

const REPOSITORY: &str = "https://github.com/Calcium-Ion/AstrLink";
const RELEASE_API: &str = "https://api.github.com/repos/Calcium-Ion/AstrLink/releases";
const LATEST_MANIFEST: &str =
    "https://github.com/Calcium-Ion/AstrLink/releases/latest/download/latest.json";
const DOWNLOAD_SITE: &str = "https://download.astrlink.com";
/// The download domain's CDN serves only `/releases/*`, so the list lives on the site.
const MIRROR_RELEASE_API: &str = "https://astrlink.com/api/releases";
const EVENT: &str = "app-update-status";
const PUBLIC_KEY: &str = env!("TAURI_UPDATER_PUBLIC_KEY");
const INTERVAL: Duration = Duration::from_secs(3 * 60 * 60);
const RETRY_INTERVALS: [Duration; 3] = [
    Duration::from_secs(60),
    Duration::from_secs(5 * 60),
    Duration::from_secs(15 * 60),
];
const MAX_PACKAGE_BYTES: u64 = 2 * 1024 * 1024 * 1024;
const DOWNLOAD_ATTEMPT_TIMEOUT: Duration = Duration::from_secs(30 * 60);

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum UpdateChannel {
    #[default]
    Stable,
    Preview,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, deny_unknown_fields)]
pub struct UpdatePreferences {
    pub auto_check: bool,
    pub auto_download: bool,
    pub channel: UpdateChannel,
    /// Check and download updates through the project download site instead of GitHub.
    pub official_mirror: bool,
}
impl Default for UpdatePreferences {
    fn default() -> Self {
        Self {
            auto_check: true,
            auto_download: true,
            channel: UpdateChannel::Stable,
            official_mirror: false,
        }
    }
}

#[derive(Clone, Debug, Serialize)]
pub struct ReleaseInfo {
    version: String,
    notes: String,
    published_at: Option<String>,
    url: String,
}

#[derive(Clone, Debug, Serialize)]
pub struct UpdateSnapshot {
    revision: u64,
    current_version: String,
    latest_version: Option<String>,
    platform: String,
    arch: String,
    install_supported: bool,
    configured: bool,
    development: bool,
    preferences: UpdatePreferences,
    phase: String,
    release: Option<ReleaseInfo>,
    downloaded_bytes: u64,
    total_bytes: Option<u64>,
    download_source: Option<String>,
    last_checked_at: Option<String>,
    error_code: Option<String>,
    error_detail: Option<String>,
    /// When GitHub accepts the next check after a rate limit.
    retry_at: Option<String>,
}

struct Inner {
    snapshot: UpdateSnapshot,
    check_schedule: CheckSchedule,
    generation: u64,
    pending: Option<Update>,
    /// Release tag (v0.2.2), not the updater's version (0.2.2).
    pending_tag: Option<String>,
    /// Wall-clock time, so a sleeping Mac ends the wait when the UI says it does.
    retry_after: Option<chrono::DateTime<chrono::Utc>>,
    /// The last `(notice, version)` announced, so periodic checks do not repeat it.
    notified: Option<(&'static str, String)>,
}

struct CheckSchedule {
    next_check: chrono::DateTime<chrono::Utc>,
    failures: usize,
}

impl CheckSchedule {
    fn new(now: chrono::DateTime<chrono::Utc>) -> Self {
        Self {
            next_check: now + chrono::TimeDelta::seconds(30),
            failures: 0,
        }
    }

    fn restart(&mut self, now: chrono::DateTime<chrono::Utc>) {
        self.next_check = now;
        self.failures = 0;
    }

    fn completed(&mut self, now: chrono::DateTime<chrono::Utc>, failed: bool) {
        let delay = if failed {
            let delay = RETRY_INTERVALS
                .get(self.failures)
                .copied()
                .unwrap_or(INTERVAL);
            self.failures = (self.failures + 1).min(RETRY_INTERVALS.len());
            delay
        } else {
            self.failures = 0;
            INTERVAL
        };
        self.next_check = now + chrono::TimeDelta::seconds(delay.as_secs() as i64);
    }

    fn delay(
        &self,
        now: chrono::DateTime<chrono::Utc>,
        retry_after: Option<chrono::DateTime<chrono::Utc>>,
    ) -> Duration {
        // A preference change may request an immediate check, but cannot bypass
        // the server's rate limit. Wall-clock deadlines also survive system sleep.
        let next = retry_after.map_or(self.next_check, |until| self.next_check.max(until));
        (next - now).to_std().unwrap_or_default()
    }
}

pub struct UpdateManager {
    inner: Mutex<Inner>,
    operation: AsyncMutex<()>,
    pub lifecycle: Arc<AsyncMutex<()>>,
    cancelled: Notify,
    schedule: Notify,
    cache: PathBuf,
}

#[derive(Clone, Debug, Deserialize)]
struct ReleaseAsset {
    name: String,
    browser_download_url: String,
}
#[derive(Clone, Debug, Deserialize)]
struct LatestManifest {
    version: String,
    notes: Option<String>,
    pub_date: Option<String>,
}
#[derive(Clone, Debug, Deserialize)]
struct GithubRelease {
    tag_name: String,
    draft: bool,
    prerelease: bool,
    body: Option<String>,
    published_at: Option<String>,
    assets: Vec<ReleaseAsset>,
}

fn version(tag: &str) -> Option<Version> {
    Version::parse(tag.strip_prefix('v').unwrap_or(tag)).ok()
}
fn select_release(releases: &[GithubRelease], channel: UpdateChannel) -> Option<&GithubRelease> {
    releases
        .iter()
        .filter(|r| !r.draft)
        .filter_map(|r| {
            let v = version(&r.tag_name)?;
            if channel == UpdateChannel::Stable && (r.prerelease || !v.pre.is_empty()) {
                return None;
            }
            Some((v, r))
        })
        .max_by(|a, b| a.0.cmp_precedence(&b.0))
        .map(|(_, r)| r)
}

/// A caller that switches only the source retries on its own; waking the
/// scheduler as well would race it for the operation lock.
fn reschedules(current: &UpdatePreferences, input: &UpdatePreferences) -> bool {
    current.official_mirror == input.official_mirror
        || UpdatePreferences {
            official_mirror: current.official_mirror,
            ..input.clone()
        } != *current
}

/// The update state worth the operator's attention. `available` only counts
/// while it waits on them; automatic downloads announce completion or failure.
fn notice(phase: &str, auto_download: bool) -> Option<&'static str> {
    match phase {
        "available" if !auto_download => Some("available"),
        "manual" => Some("manual"),
        "ready" => Some("ready"),
        "error" => Some("error"),
        _ => None,
    }
}

/// False while the main window is hidden to the tray, minimized, or behind
/// other apps, where its toasts go unseen.
pub(crate) fn main_window_attended(app: &AppHandle) -> bool {
    app.get_webview_window("main").is_some_and(|window| {
        window.is_visible().unwrap_or(false)
            && window.is_focused().unwrap_or(false)
            && !window.is_minimized().unwrap_or(false)
    })
}

/// The main window shows its own toast; a native notification covers the
/// times it cannot be seen.
fn notify_unattended(app: &AppHandle, kind: &str, version: &str) {
    if main_window_attended(app) {
        return;
    }
    let locale = app
        .state::<Arc<PreferencesStore>>()
        .snapshot()
        .values
        .locale;
    crate::notify_native(
        app,
        i18n::t(locale, &format!("about.phase.{kind}"), &[]),
        i18n::t(
            locale,
            &format!("about.{kind}Notification"),
            &[("version", version)],
        ),
    );
}

fn github_asset(url: &str, tag: &str) -> bool {
    let Ok(parsed) = reqwest::Url::parse(url) else {
        return false;
    };
    parsed.scheme() == "https"
        && parsed.host_str() == Some("github.com")
        && parsed.username().is_empty()
        && parsed.password().is_none()
        && parsed.port().is_none()
        && parsed.query().is_none()
        && parsed.fragment().is_none()
        && parsed
            .path()
            .replace("%2B", "+")
            .replace("%2b", "+")
            .starts_with(&format!("/Calcium-Ion/AstrLink/releases/download/{tag}/"))
}

fn download_site_asset(url: &str, tag: &str) -> bool {
    let Ok(parsed) = reqwest::Url::parse(url) else {
        return false;
    };
    parsed.scheme() == "https"
        && parsed.host_str() == Some("download.astrlink.com")
        && parsed.username().is_empty()
        && parsed.password().is_none()
        && parsed.port().is_none()
        && parsed.query().is_none()
        && parsed.fragment().is_none()
        && parsed
            .path()
            .replace("%2B", "+")
            .replace("%2b", "+")
            .starts_with(&format!("/releases/download/{tag}/"))
}

fn official_asset(url: &str, tag: &str) -> bool {
    github_asset(url, tag) || download_site_asset(url, tag)
}

fn candidate_source(url: &reqwest::Url) -> &'static str {
    if url.host_str() == Some("download.astrlink.com") {
        "download_site"
    } else {
        "github"
    }
}

/// `Update::download` verifies the signature, so signature failures surface as
/// typed errors; map them to `verification` rather than a network `download`.
fn updater_error_code(error: &tauri_plugin_updater::Error) -> &'static str {
    use tauri_plugin_updater::Error;
    match error {
        Error::Minisign(_)
        | Error::Base64(_)
        | Error::SignatureUtf8(_)
        | Error::SignedVersionMismatch { .. }
        | Error::MissingSignedVersion => "verification",
        _ => "download",
    }
}

/// Rebuild the asset URL from constants and a validated tag/file pair so the
/// original URL's query, fragment, credentials, port or extra path cannot leak.
fn download_candidate(url: &str, tag: &str, mirror: bool) -> Option<reqwest::Url> {
    if !official_asset(url, tag) {
        return None;
    }
    let parsed = reqwest::Url::parse(url).ok()?;
    let prefix = if github_asset(url, tag) {
        "/Calcium-Ion/AstrLink/releases/download/"
    } else {
        "/releases/download/"
    };
    let path = parsed.path().replace("%2B", "+").replace("%2b", "+");
    let file = path
        .strip_prefix(prefix)?
        .strip_prefix(tag)?
        .strip_prefix('/')?;
    if file.is_empty()
        || file.contains('/')
        || file.contains('\\')
        || file.contains("%2F")
        || file.contains("%2f")
        || file == "."
        || file == ".."
        || file.contains("..")
    {
        return None;
    }
    let encoded_tag = urlencoding(tag);
    let encoded_file = urlencoding(file);
    if mirror {
        reqwest::Url::parse(&format!(
            "{DOWNLOAD_SITE}/releases/download/{encoded_tag}/{encoded_file}"
        ))
        .ok()
    } else {
        reqwest::Url::parse(&format!(
            "{REPOSITORY}/releases/download/{encoded_tag}/{encoded_file}"
        ))
        .ok()
    }
}

fn urlencoding(value: &str) -> String {
    let mut encoded = String::new();
    for byte in value.bytes() {
        match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'~' => {
                encoded.push(byte as char)
            }
            _ => encoded.push_str(&format!("%{byte:02X}")),
        }
    }
    encoded
}

pub fn verify_package(bytes: &[u8], signature: &str, key: &str) -> Result<(), String> {
    let decode = |encoded: &str| -> Result<String, String> {
        String::from_utf8(STANDARD.decode(encoded.trim()).map_err(|e| e.to_string())?)
            .map_err(|e| e.to_string())
    };
    PublicKey::decode(&decode(key)?)
        .map_err(|e| e.to_string())?
        .verify(
            bytes,
            &Signature::decode(&decode(signature)?).map_err(|e| e.to_string())?,
            true,
        )
        .map_err(|e| e.to_string())
}

/// Passes a successful response through. A 429, or a 403 carrying GitHub's
/// rate-limit signals, also reports how many seconds to wait.
async fn github_status(
    response: reqwest::Response,
    retry_after: &mut Option<u64>,
) -> Result<reqwest::Response, (&'static str, String)> {
    let status = response.status();
    if !matches!(status.as_u16(), 403 | 429) {
        return response
            .error_for_status()
            .map_err(|e| ("network", e.to_string()));
    }
    let (exhausted, wait, detail) = {
        let header = |name: &str| {
            response
                .headers()
                .get(name)
                .and_then(|v| v.to_str().ok())
                .map(str::trim)
        };
        let exhausted = header("x-ratelimit-remaining") == Some("0");
        let wait = header("retry-after")
            .and_then(|v| v.parse::<u64>().ok())
            .or_else(|| {
                let reset = header("x-ratelimit-reset")?.parse::<i64>().ok()?;
                exhausted.then(|| (reset - chrono::Utc::now().timestamp()).max(1) as u64)
            });
        let quota = header("x-ratelimit-limit")
            .zip(header("x-ratelimit-remaining"))
            .map(|(limit, left)| format!("; {left} of {limit} requests left"))
            .unwrap_or_default();
        (exhausted, wait, format!("GitHub HTTP {status}{quota}"))
    };
    // GitHub's message names the caller's IP address, so it stays out of the detail.
    let message = response.text().await.unwrap_or_default();
    if status.as_u16() == 403
        && wait.is_none()
        && !exhausted
        && !message.to_ascii_lowercase().contains("rate limit")
    {
        return Err(("network", detail));
    }
    // Without a hint, GitHub asks clients to wait at least a minute.
    *retry_after = Some(wait.unwrap_or(60).clamp(1, 86400));
    Err(("rate_limit", detail))
}

/// The tag of a `latest.json` asset URL in this repository's releases.
fn manifest_tag(url: &reqwest::Url) -> Option<String> {
    let path = url.path().replace("%2B", "+").replace("%2b", "+");
    let tag = path
        .strip_prefix("/Calcium-Ion/AstrLink/releases/download/")?
        .strip_suffix("/latest.json")?;
    (!tag.is_empty() && !tag.contains('/') && github_asset(url.as_str(), tag))
        .then(|| tag.to_owned())
}

/// Reads the stable release through GitHub's latest-release download, which is
/// served outside the API quota that shared network addresses often exhaust.
async fn fetch_latest(
    client: &reqwest::Client,
    direct: &reqwest::Client,
    endpoint: &str,
    retry_after: &mut Option<u64>,
) -> Result<Vec<GithubRelease>, (&'static str, String)> {
    let response = direct
        .get(endpoint)
        .send()
        .await
        .map_err(|e| ("network", e.to_string()))?;
    if response.status() == reqwest::StatusCode::NOT_FOUND {
        return Ok(Vec::new());
    }
    let location = response
        .headers()
        .get(reqwest::header::LOCATION)
        .and_then(|v| v.to_str().ok())
        .and_then(|v| response.url().join(v).ok());
    if !response.status().is_redirection() {
        github_status(response, retry_after).await?;
        return Err((
            "manifest",
            "GitHub did not redirect to the latest release".into(),
        ));
    }
    let (url, tag) = location
        .and_then(|url| manifest_tag(&url).map(|tag| (url, tag)))
        .ok_or((
            "manifest",
            "Latest release redirect is not a release manifest".into(),
        ))?;
    let response = client
        .get(url.clone())
        .send()
        .await
        .map_err(|e| ("network", e.to_string()))?;
    if response.status() == reqwest::StatusCode::NOT_FOUND {
        return Err((
            "missing_artifact",
            "Release has no latest.json update manifest".into(),
        ));
    }
    let manifest = github_status(response, retry_after)
        .await?
        .json()
        .await
        .map_err(|e| ("manifest", e.to_string()))?;
    latest_release(url, tag, manifest).map(|release| vec![release])
}

fn latest_release(
    url: reqwest::Url,
    tag: String,
    manifest: LatestManifest,
) -> Result<GithubRelease, (&'static str, String)> {
    if version(&manifest.version).is_none() || version(&manifest.version) != version(&tag) {
        return Err((
            "manifest",
            "Update manifest does not match the latest release".into(),
        ));
    }
    Ok(GithubRelease {
        tag_name: tag,
        draft: false,
        prerelease: false,
        body: manifest.notes,
        published_at: manifest.pub_date,
        assets: vec![ReleaseAsset {
            name: "latest.json".into(),
            browser_download_url: url.into(),
        }],
    })
}

async fn fetch_releases(
    client: &reqwest::Client,
    endpoint: &str,
    retry_after: &mut Option<u64>,
) -> Result<Vec<GithubRelease>, (&'static str, String)> {
    let mut releases = Vec::new();
    // Page through all public releases; GitHub's date ordering is not SemVer ordering.
    for page in 1..=100 {
        let response = client
            .get(endpoint)
            .query(&[("per_page", 100), ("page", page)])
            .header("Accept", "application/vnd.github+json")
            .send()
            .await
            .map_err(|e| ("network", e.to_string()))?;
        let mut batch: Vec<GithubRelease> = github_status(response, retry_after)
            .await?
            .json()
            .await
            .map_err(|e| ("manifest", e.to_string()))?;
        let done = batch.len() < 100;
        releases.append(&mut batch);
        if done {
            break;
        }
        if page == 100 {
            return Err((
                "manifest",
                "Too many releases to select a version safely".into(),
            ));
        }
    }
    Ok(releases)
}

/// The mirror lists its 20 newest releases in GitHub's shape, outside GitHub's
/// quota; its failures are never GitHub rate limits.
async fn fetch_mirror_releases(
    client: &reqwest::Client,
    endpoint: &str,
) -> Result<Vec<GithubRelease>, (&'static str, String)> {
    client
        .get(endpoint)
        .send()
        .await
        .and_then(reqwest::Response::error_for_status)
        .map_err(|e| ("network", e.to_string()))?
        .json()
        .await
        .map_err(|e| ("manifest", e.to_string()))
}

impl UpdateManager {
    pub fn new(app: &AppHandle) -> Result<Self, String> {
        #[cfg(target_os = "linux")]
        let supported = app.env().appimage.is_some();
        #[cfg(not(target_os = "linux"))]
        let supported = cfg!(any(target_os = "macos", windows));
        Ok(Self {
            inner: Mutex::new(Inner {
                snapshot: UpdateSnapshot {
                    revision: 0,
                    current_version: app.package_info().version.to_string(),
                    latest_version: None,
                    platform: std::env::consts::OS.into(),
                    arch: std::env::consts::ARCH.into(),
                    install_supported: supported,
                    configured: !PUBLIC_KEY.is_empty(),
                    development: cfg!(debug_assertions),
                    preferences: app
                        .state::<Arc<PreferencesStore>>()
                        .snapshot()
                        .values
                        .updates,
                    phase: "idle".into(),
                    release: None,
                    downloaded_bytes: 0,
                    total_bytes: None,
                    download_source: None,
                    last_checked_at: None,
                    error_code: None,
                    error_detail: None,
                    retry_at: None,
                },
                check_schedule: CheckSchedule::new(chrono::Utc::now()),
                generation: 0,
                pending: None,
                pending_tag: None,
                retry_after: None,
                notified: None,
            }),
            operation: AsyncMutex::new(()),
            lifecycle: Arc::new(AsyncMutex::new(())),
            cancelled: Notify::new(),
            schedule: Notify::new(),
            cache: app
                .path()
                .app_cache_dir()
                .map_err(|e| e.to_string())?
                .join("updates"),
        })
    }
    fn lock(&self) -> MutexGuard<'_, Inner> {
        self.inner.lock().unwrap_or_else(|e| e.into_inner())
    }
    pub fn snapshot(&self) -> UpdateSnapshot {
        self.lock().snapshot.clone()
    }
    fn publish(&self, app: &AppHandle, change: impl FnOnce(&mut Inner)) {
        let (snapshot, announce) = {
            let mut inner = self.lock();
            change(&mut inner);
            inner.snapshot.revision += 1;
            let current = notice(
                &inner.snapshot.phase,
                inner.snapshot.preferences.auto_download,
            )
            .zip(inner.snapshot.release.as_ref().map(|r| r.version.clone()));
            let announce = current.filter(|n| inner.notified.as_ref() != Some(n));
            if announce.is_some() {
                inner.notified.clone_from(&announce);
            }
            (inner.snapshot.clone(), announce)
        };
        if let Err(error) = app.emit_to("main", EVENT, snapshot) {
            eprintln!("unable to publish update state: {error}");
        }
        if let Some((kind, version)) = announce {
            notify_unattended(app, kind, &version);
        }
    }
    fn fail(&self, app: &AppHandle, generation: u64, code: &str, detail: String) {
        self.publish(app, |i| {
            if i.generation != generation {
                return;
            }
            i.snapshot.phase = "error".into();
            i.snapshot.download_source = None;
            i.snapshot.error_code = Some(code.into());
            i.snapshot.error_detail = Some(detail);
        });
    }
    fn cache_file(&self, update: &Update) -> PathBuf {
        use sha2::{Digest, Sha256};
        let key = format!("{}:{}:{}", update.version, update.target, update.signature);
        self.cache
            .join(format!("{:x}.update", Sha256::digest(key.as_bytes())))
    }
    fn cached(&self, update: &Update) -> Result<Vec<u8>, String> {
        let path = self.cache_file(update);
        if fs::metadata(&path).map_err(|e| e.to_string())?.len() > MAX_PACKAGE_BYTES {
            return Err("Update cache is too large".into());
        }
        let bytes = fs::read(&path).map_err(|e| e.to_string())?;
        verify_package(&bytes, &update.signature, PUBLIC_KEY)?;
        Ok(bytes)
    }
    fn clear_cache(&self) {
        // This directory is exclusively owned by the updater.
        if let Ok(entries) = fs::read_dir(&self.cache) {
            for entry in entries.flatten() {
                if matches!(
                    entry.path().extension().and_then(|e| e.to_str()),
                    Some("update" | "part")
                ) {
                    let _ = fs::remove_file(entry.path());
                }
            }
        }
    }
    pub fn preferences(
        &self,
        app: &AppHandle,
        input: UpdatePreferences,
    ) -> Result<UpdateSnapshot, String> {
        let mut inner = self.lock();
        if inner.snapshot.phase == "installing" {
            return Err("An update is being installed".into());
        }
        app.state::<Arc<PreferencesStore>>()
            .replace_updates(input.clone())?;
        let current = &inner.snapshot.preferences;
        let changed_channel = current.channel != input.channel;
        let changed_mirror = current.official_mirror != input.official_mirror;
        let reschedule = reschedules(current, &input);
        inner.snapshot.preferences = input;
        if reschedule {
            inner.check_schedule.restart(chrono::Utc::now());
        }
        if changed_channel || changed_mirror {
            // Each channel and the mirror read separate sources, so a rate limit does not carry over.
            inner.retry_after = None;
            inner.snapshot.retry_at = None;
        }
        if changed_channel {
            inner.generation += 1;
            inner.pending = None;
            inner.pending_tag = None;
            inner.snapshot.release = None;
            inner.snapshot.latest_version = None;
            inner.snapshot.phase = "idle".into();
            inner.snapshot.error_code = None;
            inner.snapshot.error_detail = None;
            inner.snapshot.downloaded_bytes = 0;
            inner.snapshot.total_bytes = None;
            inner.snapshot.download_source = None;
            self.cancelled.notify_waiters();
        }
        if changed_channel {
            self.clear_cache();
        }
        drop(inner);
        self.publish(app, |_| {});
        if reschedule {
            self.schedule.notify_one();
        }
        Ok(self.snapshot())
    }
    pub fn start(self: &Arc<Self>, app: AppHandle) {
        let manager = Arc::clone(self);
        tauri::async_runtime::spawn(async move {
            loop {
                match manager.automatic_check_delay() {
                    Some(delay) if delay.is_zero() => {
                        // A manual operation may finish or preferences may change
                        // while waiting. Re-read the deadline before checking.
                        let _guard = manager.operation.lock().await;
                        if manager.automatic_check_delay().is_some_and(|d| d.is_zero()) {
                            let _ = manager.check_locked(&app).await;
                        }
                    }
                    Some(delay) => {
                        // Re-evaluate the wall-clock deadline after system sleep.
                        tokio::select! {
                            _ = tokio::time::sleep(delay.min(Duration::from_secs(60))) => {},
                            _ = manager.schedule.notified() => {},
                        }
                    }
                    None => manager.schedule.notified().await,
                }
            }
        });
    }
    fn automatic_check_delay(&self) -> Option<Duration> {
        let inner = self.lock();
        (!inner.snapshot.development && inner.snapshot.preferences.auto_check).then(|| {
            inner
                .check_schedule
                .delay(chrono::Utc::now(), inner.retry_after)
        })
    }
    fn schedule_next_check(&self, generation: u64) {
        let mut inner = self.lock();
        if inner.generation != generation {
            return;
        }
        let failed = inner.snapshot.phase == "error";
        inner.check_schedule.completed(chrono::Utc::now(), failed);
        drop(inner);
        // Manual checks and downloads also replace a pending automatic retry.
        self.schedule.notify_one();
    }
    pub async fn check(&self, app: &AppHandle) -> Result<UpdateSnapshot, String> {
        let _operation = self
            .operation
            .try_lock()
            .map_err(|_| "An update operation is already running")?;
        self.check_locked(app).await
    }
    async fn check_locked(&self, app: &AppHandle) -> Result<UpdateSnapshot, String> {
        let cancel = self.cancelled.notified();
        tokio::pin!(cancel);
        cancel.as_mut().enable();
        let generation = self.lock().generation;
        if self
            .lock()
            .retry_after
            .is_some_and(|until| until > chrono::Utc::now())
        {
            // The snapshot already reports the rate limit and when it lifts.
            return Ok(self.snapshot());
        }
        self.publish(app, |i| {
            i.pending = None;
            i.pending_tag = None;
            i.retry_after = None;
            i.snapshot.phase = "checking".into();
            i.snapshot.error_code = None;
            i.snapshot.error_detail = None;
            i.snapshot.retry_at = None;
        });
        let result = tokio::select! { result = self.check_inner(app, generation) => result, _ = &mut cancel => return Ok(self.snapshot()) };
        if let Err((code, error)) = result {
            self.fail(app, generation, code, error);
        }
        if self.snapshot().phase == "available" && self.snapshot().preferences.auto_download {
            self.download_locked(app, generation).await;
        }
        self.schedule_next_check(generation);
        Ok(self.snapshot())
    }
    async fn check_inner(
        &self,
        app: &AppHandle,
        generation: u64,
    ) -> Result<(), (&'static str, String)> {
        let prefs = app.state::<Arc<PreferencesStore>>().snapshot().values;
        let client = |redirect| {
            let mut builder = reqwest::Client::builder()
                .user_agent("tauri-updater")
                .timeout(Duration::from_secs(30))
                .redirect(redirect);
            if !prefs.use_system_proxy {
                builder = builder.no_proxy();
            }
            builder.build().map_err(|e| ("network", e.to_string()))
        };
        let follow = client(reqwest::redirect::Policy::default())?;
        let mirror = prefs.updates.official_mirror;
        let mut retry_after = None;
        let fetched = if mirror {
            fetch_mirror_releases(&follow, MIRROR_RELEASE_API).await
        } else {
            match prefs.updates.channel {
                UpdateChannel::Stable => {
                    let direct = client(reqwest::redirect::Policy::none())?;
                    fetch_latest(&follow, &direct, LATEST_MANIFEST, &mut retry_after).await
                }
                UpdateChannel::Preview => {
                    fetch_releases(&follow, RELEASE_API, &mut retry_after).await
                }
            }
        };
        let releases = match fetched {
            Ok(releases) => releases,
            Err(error) => {
                if let Some(seconds) = retry_after {
                    let until = chrono::Utc::now() + chrono::TimeDelta::seconds(seconds as i64);
                    let mut inner = self.lock();
                    if inner.generation == generation {
                        inner.retry_after = Some(until);
                        inner.snapshot.retry_at = Some(until.to_rfc3339());
                    }
                }
                return Err(error);
            }
        };
        self.publish(app, |i| {
            if i.generation == generation {
                i.snapshot.last_checked_at = Some(chrono::Utc::now().to_rfc3339());
            }
        });
        let selected = select_release(&releases, prefs.updates.channel);
        let Some(release) = selected else {
            self.publish(app, |i| {
                if i.generation == generation {
                    i.pending = None;
                    i.pending_tag = None;
                    i.snapshot.release = None;
                    i.snapshot.phase = "no_releases".into();
                    i.snapshot.latest_version = None;
                }
            });
            return Ok(());
        };
        let selected_version = version(&release.tag_name).expect("selected version is valid");
        self.publish(app, |i| {
            if i.generation == generation {
                i.snapshot.latest_version = Some(selected_version.to_string());
            }
        });
        if selected_version
            .cmp_precedence(&app.package_info().version)
            .is_le()
        {
            self.publish(app, |i| {
                if i.generation == generation {
                    i.pending = None;
                    i.pending_tag = None;
                    i.snapshot.release = None;
                    i.snapshot.phase = "up_to_date".into();
                }
            });
            return Ok(());
        }
        let info = ReleaseInfo {
            version: selected_version.to_string(),
            notes: release.body.clone().unwrap_or_default(),
            published_at: release.published_at.clone(),
            url: format!("{REPOSITORY}/releases/tag/{}", release.tag_name),
        };
        self.publish(app, |i| {
            if i.generation == generation {
                i.snapshot.release = Some(info);
            }
        });
        let snapshot = self.snapshot();
        if snapshot.development || !snapshot.install_supported || !snapshot.configured {
            self.publish(app, |i| {
                if i.generation == generation {
                    i.snapshot.phase = "manual".into();
                }
            });
            return Ok(());
        }
        let manifest = release
            .assets
            .iter()
            .find(|a| {
                a.name == "latest.json" && github_asset(&a.browser_download_url, &release.tag_name)
            })
            .ok_or((
                "missing_artifact",
                "Release has no latest.json update manifest".into(),
            ))?;
        let endpoint = if mirror {
            format!(
                "{DOWNLOAD_SITE}/releases/download/{}/latest.json",
                urlencoding(&release.tag_name)
            )
        } else {
            manifest.browser_download_url.clone()
        };
        let mut updater = app
            .updater_builder()
            .pubkey(PUBLIC_KEY)
            .timeout(Duration::from_secs(30))
            .endpoints(vec![endpoint
                .parse::<reqwest::Url>()
                .map_err(|e| ("manifest", e.to_string()))?])
            .map_err(|e| ("manifest", e.to_string()))?;
        if !prefs.use_system_proxy {
            updater = updater.no_proxy();
        }
        let mut update = updater
            .build()
            .map_err(|e| ("manifest", e.to_string()))?
            .check()
            .await
            .map_err(|e| ("manifest", e.to_string()))?
            .ok_or((
                "manifest",
                "Release and update manifest versions disagree".into(),
            ))?;
        if version(&update.version) != Some(selected_version)
            || !official_asset(update.download_url.as_str(), &release.tag_name)
        {
            return Err((
                "manifest",
                "Update manifest does not match the selected release".into(),
            ));
        }
        update.timeout = Some(Duration::from_secs(30 * 60));
        let cached = self.cached(&update).ok();
        if cached.is_none() {
            let _ = fs::remove_file(self.cache_file(&update));
        }
        self.publish(app, |i| {
            if i.generation != generation {
                return;
            }
            i.snapshot.phase = if cached.is_some() {
                "ready"
            } else {
                "available"
            }
            .into();
            i.snapshot.downloaded_bytes = cached.as_ref().map_or(0, |b| b.len() as u64);
            i.snapshot.total_bytes = cached.as_ref().map(|b| b.len() as u64);
            i.pending = Some(update);
            i.pending_tag = Some(release.tag_name.clone());
        });
        Ok(())
    }
    pub async fn download(&self, app: &AppHandle) -> Result<UpdateSnapshot, String> {
        let _operation = self
            .operation
            .try_lock()
            .map_err(|_| "An update operation is already running")?;
        let generation = self.lock().generation;
        self.download_locked(app, generation).await;
        self.schedule_next_check(generation);
        Ok(self.snapshot())
    }
    async fn download_locked(&self, app: &AppHandle, generation: u64) {
        let cancel = self.cancelled.notified();
        tokio::pin!(cancel);
        cancel.as_mut().enable();
        let (update, pending_tag) = {
            let inner = self.lock();
            (inner.pending.clone(), inner.pending_tag.clone())
        };
        let (Some(mut update), Some(pending_tag)) = (update, pending_tag) else {
            return;
        };
        let prefs = app.state::<Arc<PreferencesStore>>().snapshot().values;
        update.no_proxy = !prefs.use_system_proxy;
        if self.snapshot().phase == "ready" {
            return;
        }
        let Some(candidate) = download_candidate(
            update.download_url.as_str(),
            &pending_tag,
            prefs.updates.official_mirror,
        ) else {
            self.fail(
                app,
                generation,
                "download",
                "Update manifest does not name a supported release asset".into(),
            );
            return;
        };
        let source = candidate_source(&candidate).to_string();
        update.download_url = candidate;
        update.timeout = Some(DOWNLOAD_ATTEMPT_TIMEOUT);
        self.publish(app, |i| {
            if i.generation == generation {
                i.snapshot.phase = "downloading".into();
                i.snapshot.downloaded_bytes = 0;
                i.snapshot.total_bytes = None;
                i.snapshot.download_source = Some(source);
                i.snapshot.error_code = None;
                i.snapshot.error_detail = None;
            }
        });
        let mut bytes_downloaded = 0u64;
        let mut last_event = Instant::now();
        let task = update.download(
            move |bytes, total| {
                bytes_downloaded += bytes as u64;
                if last_event.elapsed() < Duration::from_millis(150) {
                    return;
                }
                last_event = Instant::now();
                self.publish(app, |i| {
                    if i.generation == generation {
                        i.snapshot.downloaded_bytes = bytes_downloaded;
                        i.snapshot.total_bytes = total;
                    }
                });
            },
            || {},
        );
        let result = tokio::select! {
            result = task => result,
            _ = &mut cancel => {
                self.publish(app, |i| {
                    if i.generation == generation {
                        i.snapshot.phase = "available".into();
                        i.snapshot.downloaded_bytes = 0;
                        i.snapshot.total_bytes = None;
                        i.snapshot.download_source = None;
                        i.snapshot.error_code = None;
                        i.snapshot.error_detail = None;
                    }
                });
                return;
            }
        };
        let bytes = match result {
            Ok(bytes) => bytes,
            Err(error) => {
                self.fail(
                    app,
                    generation,
                    updater_error_code(&error),
                    error.to_string(),
                );
                return;
            }
        };
        let result = (|| -> Result<(), String> {
            if bytes.len() as u64 > MAX_PACKAGE_BYTES {
                return Err("Update package is too large".into());
            }
            verify_package(&bytes, &update.signature, PUBLIC_KEY)?;
            // Serialize cache commit with channel changes, so cancelled downloads never reappear.
            let inner = self.lock();
            if inner.generation != generation {
                return Ok(());
            }
            fs::create_dir_all(&self.cache).map_err(|e| e.to_string())?;
            self.clear_cache();
            let path = self.cache_file(&update);
            fs::write(path.with_extension("part"), &bytes).map_err(|e| e.to_string())?;
            fs::rename(path.with_extension("part"), path).map_err(|e| e.to_string())?;
            Ok(())
        })();
        match result {
            Ok(()) => self.publish(app, |i| {
                if i.generation == generation {
                    i.snapshot.phase = "ready".into();
                    i.snapshot.downloaded_bytes = bytes.len() as u64;
                    i.snapshot.total_bytes = Some(bytes.len() as u64);
                }
            }),
            Err(error) => self.fail(app, generation, "verification", error),
        }
    }
    pub async fn install(&self, app: &AppHandle) -> Result<UpdateSnapshot, String> {
        let _operation = self
            .operation
            .try_lock()
            .map_err(|_| "An update operation is already running")?;
        let _lifecycle = self
            .lifecycle
            .try_lock()
            .map_err(|_| "The gateway is changing state; retry shortly")?;
        let (generation, update) = {
            let mut inner = self.lock();
            if inner.snapshot.development || inner.snapshot.phase != "ready" {
                return Err("No verified update is ready to install".into());
            }
            let update = inner.pending.clone().ok_or("No pending update")?;
            inner.snapshot.phase = "installing".into();
            (inner.generation, update)
        };
        self.publish(app, |_| {});
        let bytes = match self.cached(&update) {
            Ok(bytes) => bytes,
            Err(error) => {
                let _ = fs::remove_file(self.cache_file(&update));
                self.fail(app, generation, "verification", error);
                return Ok(self.snapshot());
            }
        };
        let core = Arc::clone(app.state::<Arc<CoreManager>>().inner());
        let (guard, was_running) = match core.begin_update() {
            Ok(value) => value,
            Err(error) => {
                self.fail(app, generation, "shutdown", error);
                return Ok(self.snapshot());
            }
        };
        if let Err(error) = core.stop_and_wait().await {
            drop(guard);
            self.fail(app, generation, "shutdown", error);
            return Ok(self.snapshot());
        }
        let result = tauri::async_runtime::spawn_blocking(move || {
            update.install(bytes).map_err(|e| e.to_string())
        })
        .await;
        match result {
            Ok(Ok(())) => {
                self.clear_cache();
                app.restart();
            }
            error => {
                drop(guard);
                let mut detail = match error {
                    Ok(Err(e)) => e,
                    Err(e) => e.to_string(),
                    _ => unreachable!(),
                };
                if was_running {
                    if let Err(e) = core.start(app) {
                        detail.push_str(&format!("; gateway recovery failed: {e}"));
                    }
                }
                self.fail(app, generation, "install", detail);
                Ok(self.snapshot())
            }
        }
    }
}

pub fn lifecycle_guard(
    app: &AppHandle,
) -> Result<Option<tokio::sync::OwnedMutexGuard<()>>, String> {
    app.try_state::<Arc<UpdateManager>>()
        .map(|manager| {
            Arc::clone(&manager.lifecycle)
                .try_lock_owned()
                .map_err(|_| "Application update or gateway operation in progress".into())
        })
        .transpose()
}

#[tauri::command]
pub fn app_update_status(manager: State<'_, Arc<UpdateManager>>) -> UpdateSnapshot {
    manager.snapshot()
}
#[tauri::command]
pub fn update_update_preferences(
    app: AppHandle,
    manager: State<'_, Arc<UpdateManager>>,
    input: UpdatePreferences,
) -> Result<UpdateSnapshot, String> {
    manager.preferences(&app, input)
}
#[tauri::command]
pub async fn check_app_update(
    app: AppHandle,
    manager: State<'_, Arc<UpdateManager>>,
) -> Result<UpdateSnapshot, String> {
    manager.check(&app).await
}
#[tauri::command]
pub async fn download_app_update(
    app: AppHandle,
    manager: State<'_, Arc<UpdateManager>>,
) -> Result<UpdateSnapshot, String> {
    manager.download(&app).await
}
#[tauri::command]
pub async fn install_app_update(
    app: AppHandle,
    manager: State<'_, Arc<UpdateManager>>,
) -> Result<UpdateSnapshot, String> {
    manager.install(&app).await
}

#[cfg(test)]
mod tests {
    use super::*;
    fn release(tag: &str, prerelease: bool) -> GithubRelease {
        GithubRelease {
            tag_name: tag.into(),
            draft: false,
            prerelease,
            body: None,
            published_at: None,
            assets: vec![],
        }
    }
    #[test]
    fn notices_skip_states_that_resolve_on_their_own() {
        assert_eq!(notice("available", false), Some("available"));
        assert_eq!(notice("available", true), None);
        assert_eq!(notice("manual", true), Some("manual"));
        assert_eq!(notice("ready", true), Some("ready"));
        assert_eq!(notice("error", true), Some("error"));
        for phase in ["idle", "checking", "up_to_date", "downloading"] {
            assert_eq!(notice(phase, false), None, "{phase}");
        }
        // Native notifications read the same catalogs; a missing key would
        // surface raw, and another language would silently fall back to English.
        for kind in ["available", "manual", "ready", "error"] {
            let title = format!("about.phase.{kind}");
            let body = format!("about.{kind}Notification");
            let vars = [("version", "1.1.0")];
            let en = i18n::t(i18n::Locale::En, &body, &vars);
            let ja = i18n::t(i18n::Locale::Ja, &body, &vars);
            let zh = i18n::t(i18n::Locale::ZhCN, &body, &vars);
            assert!(
                en.contains("1.1.0") && ja.contains("1.1.0") && zh.contains("1.1.0"),
                "{body}"
            );
            assert_ne!(en, zh, "{body}");
            assert_ne!(en, ja, "{body}");
            assert_ne!(
                i18n::t(i18n::Locale::En, &title, &[]),
                i18n::t(i18n::Locale::ZhCN, &title, &[]),
                "{title}"
            );
            assert_ne!(
                i18n::t(i18n::Locale::En, &title, &[]),
                i18n::t(i18n::Locale::Ja, &title, &[]),
                "{title}"
            );
        }
    }

    #[test]
    fn failed_checks_retry_soon_then_back_off_and_reset_after_success() {
        let now = chrono::DateTime::parse_from_rfc3339("2026-10-21T04:00:00Z")
            .unwrap()
            .with_timezone(&chrono::Utc);
        let mut schedule = CheckSchedule::new(now);
        assert_eq!(schedule.delay(now, None), Duration::from_secs(30));
        for seconds in [60, 300, 900, 10800, 10800] {
            schedule.completed(now, true);
            assert_eq!(schedule.delay(now, None), Duration::from_secs(seconds));
        }
        schedule.completed(now, false);
        assert_eq!(schedule.delay(now, None), INTERVAL);
        schedule.completed(now, true);
        assert_eq!(schedule.delay(now, None), Duration::from_secs(60));
        // Manual success cancels an outstanding short retry.
        schedule.completed(now, false);
        assert_eq!(schedule.delay(now, None), INTERVAL);
    }

    #[test]
    fn automatic_retries_respect_rate_limits_and_expired_wall_clock_deadlines() {
        let now = chrono::DateTime::parse_from_rfc3339("2026-10-21T04:00:00Z")
            .unwrap()
            .with_timezone(&chrono::Utc);
        let mut schedule = CheckSchedule::new(now);
        schedule.completed(now, true);
        let retry_after = Some(now + chrono::TimeDelta::minutes(20));
        assert_eq!(schedule.delay(now, retry_after), Duration::from_secs(1200));
        // Changing preferences must not let a check bypass the server's wait.
        schedule.restart(now);
        assert_eq!(schedule.delay(now, retry_after), Duration::from_secs(1200));
        assert_eq!(schedule.delay(now, None), Duration::ZERO);
        assert_eq!(
            schedule.delay(now + chrono::TimeDelta::minutes(21), retry_after),
            Duration::ZERO
        );
        // A new configuration gets the short first retry again.
        schedule.completed(now, true);
        assert_eq!(schedule.delay(now, None), Duration::from_secs(60));
    }
    #[test]
    fn cached_artifacts_are_verified_before_installation() {
        let fixture: serde_json::Value =
            serde_json::from_str(include_str!("../testdata/update-signature.json")).unwrap();
        let message = fixture["message"].as_str().unwrap().as_bytes();
        let signature = fixture["signature"].as_str().unwrap();
        let key = fixture["key"].as_str().unwrap();
        verify_package(message, signature, key)
            .expect("Node-signed Minisign fixture verifies independently in Rust");
        let mut corrupted = message.to_vec();
        corrupted[0] ^= 1;
        assert!(verify_package(&corrupted, signature, key).is_err());
        let decoded = String::from_utf8(STANDARD.decode(signature).unwrap())
            .unwrap()
            .replace("timestamp:1", "timestamp:2");
        assert!(verify_package(message, &STANDARD.encode(decoded), key).is_err());
    }

    fn response_server(
        status: &str,
        headers: &str,
        body: &str,
    ) -> (String, std::thread::JoinHandle<()>) {
        use std::io::{Read, Write};
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        let response = format!("HTTP/1.1 {status}\r\nContent-Length: {}\r\nContent-Type: application/json\r\nConnection: close\r\n{headers}\r\n{body}", body.len());
        let handle = std::thread::spawn(move || {
            let (mut socket, _) = listener.accept().unwrap();
            socket
                .set_read_timeout(Some(Duration::from_secs(3)))
                .unwrap();
            let mut request = [0; 4096];
            let _ = socket.read(&mut request).unwrap();
            socket.write_all(response.as_bytes()).unwrap();
        });
        (url, handle)
    }

    #[tokio::test]
    async fn github_failures_are_distinct_from_no_releases_and_rate_limits_back_off() {
        let client = reqwest::Client::builder()
            .no_proxy()
            .timeout(Duration::from_secs(3))
            .build()
            .unwrap();
        let reset = chrono::Utc::now().timestamp() + 600;
        let exhausted = format!(
            "X-RateLimit-Limit: 60\r\nX-RateLimit-Remaining: 0\r\nX-RateLimit-Reset: {reset}\r\n"
        );
        for (status, headers, body, expected) in [
            ("200 OK", "", "[]", None),
            (
                "500 Internal Server Error",
                "",
                "{}",
                Some(("network", None)),
            ),
            ("200 OK", "", "invalid", Some(("manifest", None))),
            (
                "429 Too Many Requests",
                "Retry-After: 60\r\n",
                "{}",
                Some(("rate_limit", Some(60..=60))),
            ),
            (
                "403 Forbidden",
                exhausted.as_str(),
                "{}",
                Some(("rate_limit", Some(590..=600))),
            ),
            (
                "403 Forbidden",
                "",
                r#"{"message":"You have exceeded a secondary rate limit"}"#,
                Some(("rate_limit", Some(60..=60))),
            ),
            // A blocked network or a revoked repository is not worth waiting on.
            (
                "403 Forbidden",
                "X-RateLimit-Remaining: 59\r\nX-RateLimit-Reset: 1\r\n",
                r#"{"message":"Forbidden"}"#,
                Some(("network", None)),
            ),
        ] {
            let (url, server) = response_server(status, headers, body);
            let mut retry = None;
            let result = fetch_releases(&client, &url, &mut retry).await;
            server.join().unwrap();
            match expected {
                Some((code, wait)) => {
                    assert_eq!(result.unwrap_err().0, code, "{status} {body}");
                    assert_eq!(retry.is_some(), wait.is_some(), "{status} {body}");
                    if let Some(range) = wait {
                        assert!(range.contains(&retry.unwrap()), "{retry:?}");
                    }
                }
                None => assert!(result.unwrap().is_empty()),
            }
        }
        let mut retry = None;
        assert_eq!(
            fetch_releases(&client, "http://127.0.0.1:0", &mut retry)
                .await
                .unwrap_err()
                .0,
            "network"
        );
        assert!(retry.is_none());
    }

    #[tokio::test]
    async fn mirror_checks_read_github_shaped_releases_and_never_back_off() {
        let client = reqwest::Client::builder()
            .no_proxy()
            .timeout(Duration::from_secs(3))
            .build()
            .unwrap();
        let listed = r#"[{"tag_name":"v1.1.0-beta.1","draft":false,"prerelease":true,
            "body":"notes","body_html":"<p>notes</p>","html_url":"https://github.com/x",
            "published_at":"2026-10-13T05:08:04Z","assets":[{"name":"latest.json",
            "browser_download_url":"https://github.com/Calcium-Ion/AstrLink/releases/download/v1.1.0-beta.1/latest.json",
            "download_url":"https://download.astrlink.com/releases/download/v1.1.0-beta.1/latest.json",
            "digest":"sha256:00"}]}]"#;
        let (url, server) = response_server("200 OK", "", listed);
        let releases = fetch_mirror_releases(&client, &url).await.unwrap();
        server.join().unwrap();
        assert_eq!(
            select_release(&releases, UpdateChannel::Preview)
                .unwrap()
                .tag_name,
            "v1.1.0-beta.1"
        );
        assert!(select_release(&releases, UpdateChannel::Stable).is_none());
        for (status, headers, body, code) in [
            ("503 Service Unavailable", "", "{}", "network"),
            (
                "429 Too Many Requests",
                "Retry-After: 60\r\n",
                "{}",
                "network",
            ),
            ("200 OK", "", "invalid", "manifest"),
        ] {
            let (url, server) = response_server(status, headers, body);
            let result = fetch_mirror_releases(&client, &url).await;
            server.join().unwrap();
            assert_eq!(result.unwrap_err().0, code, "{status} {body}");
        }
    }

    #[test]
    fn switching_only_the_mirror_leaves_the_retry_to_its_caller() {
        let current = UpdatePreferences::default();
        let mirror = UpdatePreferences {
            official_mirror: true,
            ..current.clone()
        };
        assert!(!reschedules(&current, &mirror));
        assert!(reschedules(&current, &current));
        assert!(reschedules(
            &current,
            &UpdatePreferences {
                channel: UpdateChannel::Preview,
                ..mirror.clone()
            }
        ));
        assert!(reschedules(
            &current,
            &UpdatePreferences {
                auto_download: false,
                ..current.clone()
            }
        ));
    }

    #[tokio::test]
    async fn stable_checks_follow_only_the_latest_release_manifest_redirect() {
        let client = reqwest::Client::builder()
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .timeout(Duration::from_secs(3))
            .build()
            .unwrap();
        let elsewhere = "Location: https://example.org/latest.json\r\n";
        let other_repository =
            "Location: https://github.com/someone/AstrLink/releases/download/v1.0.0/latest.json\r\n";
        for (status, headers, expected) in [
            ("404 Not Found", "", None),
            ("302 Found", elsewhere, Some("manifest")),
            ("302 Found", other_repository, Some("manifest")),
            ("200 OK", "", Some("manifest")),
            (
                "429 Too Many Requests",
                "Retry-After: 30\r\n",
                Some("rate_limit"),
            ),
        ] {
            let (url, server) = response_server(status, headers, "{}");
            let mut retry = None;
            let result = fetch_latest(&client, &client, &url, &mut retry).await;
            server.join().unwrap();
            match expected {
                Some(code) => assert_eq!(result.unwrap_err().0, code, "{status} {headers}"),
                None => assert!(result.unwrap().is_empty()),
            }
            assert_eq!(retry.is_some(), expected == Some("rate_limit"));
        }
    }

    #[test]
    fn latest_manifests_must_name_the_release_they_were_published_in() {
        let url = |tag: &str| {
            reqwest::Url::parse(&format!(
                "https://github.com/Calcium-Ion/AstrLink/releases/download/{tag}/latest.json"
            ))
            .unwrap()
        };
        assert_eq!(manifest_tag(&url("v0.1.8")).as_deref(), Some("v0.1.8"));
        assert_eq!(
            manifest_tag(&url("v1.0.0%2Bbuild.1")).as_deref(),
            Some("v1.0.0+build.1")
        );
        for wrong in [
            "https://github.com/Calcium-Ion/AstrLink/releases/download/v0.1.8/a.json",
            "https://github.com/Calcium-Ion/AstrLink/releases/download/a/b/latest.json",
            "https://github.com/Calcium-Ion/AstrLink/releases/download//latest.json",
            "https://github.com.evil/Calcium-Ion/AstrLink/releases/download/v0.1.8/latest.json",
            "http://github.com/Calcium-Ion/AstrLink/releases/download/v0.1.8/latest.json",
        ] {
            assert_eq!(manifest_tag(&reqwest::Url::parse(wrong).unwrap()), None);
        }
        let manifest = |version: &str| LatestManifest {
            version: version.into(),
            notes: Some("notes".into()),
            pub_date: None,
        };
        let release = latest_release(url("v0.1.8"), "v0.1.8".into(), manifest("0.1.8")).unwrap();
        assert_eq!(release.tag_name, "v0.1.8");
        assert_eq!(release.body.as_deref(), Some("notes"));
        assert!(
            release.assets[0].name == "latest.json"
                && github_asset(&release.assets[0].browser_download_url, "v0.1.8")
        );
        assert_eq!(
            select_release(&[release], UpdateChannel::Stable)
                .unwrap()
                .tag_name,
            "v0.1.8"
        );
        for wrong in ["0.1.7", "invalid"] {
            assert_eq!(
                latest_release(url("v0.1.8"), "v0.1.8".into(), manifest(wrong))
                    .unwrap_err()
                    .0,
                "manifest"
            );
        }
    }

    #[test]
    fn channels_select_by_semver_not_dates_and_never_accept_drafts() {
        let mut draft = release("9.0.0", false);
        draft.draft = true;
        let releases = vec![
            release("v1.9.0", false),
            release("1.10.0", false),
            release("2.0.0-beta.2", false),
            release("2.0.0-beta.10", true),
            release("invalid", false),
            draft,
        ];
        assert_eq!(
            select_release(&releases, UpdateChannel::Stable)
                .unwrap()
                .tag_name,
            "1.10.0"
        );
        assert_eq!(
            select_release(&releases, UpdateChannel::Preview)
                .unwrap()
                .tag_name,
            "2.0.0-beta.10"
        );
        assert!(version("v1.10.0")
            .unwrap()
            .cmp_precedence(&version("2.0.0").unwrap())
            .is_lt());
        assert!(select_release(&[], UpdateChannel::Stable).is_none());
    }
    #[test]
    fn asset_urls_must_belong_to_selected_release() {
        assert!(github_asset(
            "https://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0/latest.json",
            "v1.0.0"
        ));
        assert!(download_site_asset(
            "https://download.astrlink.com/releases/download/v1.0.0/AstrLink.exe",
            "v1.0.0"
        ));
        for url in [
            "http://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0/a",
            "https://example.org/a",
            "https://github.com/Calcium-Ion/AstrLink/releases/download/v0.1.0/a",
            "https://github.com.evil/Calcium-Ion/AstrLink/releases/download/v1.0.0/a",
            "http://download.astrlink.com/releases/download/v1.0.0/a",
            "https://download.astrlink.com.evil/releases/download/v1.0.0/a",
            "https://download.astrlink.com/releases/download/v0.1.0/a",
            "https://download.astrlink.com/releases/download/v1.0.0/a?x=1",
        ] {
            assert!(!official_asset(url, "v1.0.0"));
        }
    }

    #[test]
    fn download_candidate_follows_the_official_mirror_preference() {
        let github =
            "https://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0/AstrLink.exe";
        let site = "https://download.astrlink.com/releases/download/v1.0.0/AstrLink.exe";
        for source in [github, site] {
            assert_eq!(
                download_candidate(source, "v1.0.0", false)
                    .unwrap()
                    .as_str(),
                github
            );
            assert_eq!(
                download_candidate(source, "v1.0.0", true).unwrap().as_str(),
                site
            );
        }
    }

    #[test]
    fn download_candidate_percent_encodes_plus_tags() {
        let github =
            "https://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0+build.1/AstrLink.exe";
        assert_eq!(
            download_candidate(github, "v1.0.0+build.1", false).unwrap().as_str(),
            "https://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0%2Bbuild.1/AstrLink.exe"
        );
        assert_eq!(
            download_candidate(github, "v1.0.0+build.1", true)
                .unwrap()
                .as_str(),
            "https://download.astrlink.com/releases/download/v1.0.0%2Bbuild.1/AstrLink.exe"
        );
    }

    #[test]
    fn download_candidate_rejects_untrusted_or_malformed_assets() {
        for source in [
            "https://example.org/releases/download/v1.0.0/AstrLink.exe",
            "https://github.com/Calcium-Ion/AstrLink/releases/download/v0.1.0/AstrLink.exe",
            "https://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0/dir/AstrLink.exe",
            "https://download.astrlink.com/releases/download/v1.0.0/a?x=1",
            "https://download.astrlink.com/releases/download/v1.0.0/a#x",
            "https://download.astrlink.com/releases/download/v1.0.0/dir%2FAstrLink.exe",
        ] {
            assert!(
                download_candidate(source, "v1.0.0", false).is_none(),
                "{source}"
            );
        }
    }
    #[test]
    fn updater_errors_classify_signature_failures_as_verification() {
        use tauri_plugin_updater::Error;
        assert_eq!(
            updater_error_code(&Error::Minisign(minisign_verify::Error::InvalidSignature)),
            "verification"
        );
        assert_eq!(
            updater_error_code(&Error::SignatureUtf8("bad".into())),
            "verification"
        );
        assert_eq!(
            updater_error_code(&Error::SignedVersionMismatch {
                signed: "1.0.0".into(),
                announced: "1.1.0".into(),
            }),
            "verification"
        );
        assert_eq!(
            updater_error_code(&Error::MissingSignedVersion),
            "verification"
        );
        assert_eq!(
            updater_error_code(&Error::Network(
                "Download request failed with status: 404 Not Found".into()
            )),
            "download"
        );
        assert_eq!(updater_error_code(&Error::ReleaseNotFound), "download");
    }

    #[test]
    fn invalid_signatures_and_keys_fail_closed() {
        assert!(verify_package(b"tampered", "invalid", "invalid").is_err());
        assert!(verify_package(b"", "", "").is_err());
    }
    #[test]
    fn preferences_migrate_defaults_and_reject_unknown_channels() {
        assert_eq!(
            serde_json::from_str::<UpdatePreferences>("{}").unwrap(),
            UpdatePreferences::default()
        );
        assert!(serde_json::from_str::<UpdatePreferences>(r#"{"channel":"nightly"}"#).is_err());
    }
}
