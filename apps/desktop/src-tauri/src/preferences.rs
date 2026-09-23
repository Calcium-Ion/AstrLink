use std::{
    fs::{self, OpenOptions},
    io::Write,
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicU64, Ordering},
        Mutex, MutexGuard,
    },
};

use serde::{Deserialize, Serialize};

use crate::i18n::{self, Locale};

pub const DEFAULT_INFERENCE_PORT: u16 = 8317;
pub const DEFAULT_MAX_CONCURRENT_INSPECTIONS: u16 = 16;
pub const MIN_MAX_CONCURRENT_INSPECTIONS: u16 = 4;
pub const MAX_MAX_CONCURRENT_INSPECTIONS: u16 = 128;
pub const DEFAULT_RESPONSE_START_TIMEOUT_SECONDS: u32 = 0;
pub const MAX_RESPONSE_START_TIMEOUT_SECONDS: u32 = 86400;
const FILE_NAME: &str = "desktop-preferences.json";
static TEMPORARY_SEQUENCE: AtomicU64 = AtomicU64::new(0);

fn default_max_concurrent_inspections() -> u16 {
    DEFAULT_MAX_CONCURRENT_INSPECTIONS
}

fn default_response_start_timeout_seconds() -> u32 {
    DEFAULT_RESPONSE_START_TIMEOUT_SECONDS
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum CloseBehavior {
    HideToTray,
    Quit,
}

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ThemePreference {
    #[default]
    System,
    Light,
    Dark,
}

impl ThemePreference {
    pub fn native_theme(self) -> Option<tauri::Theme> {
        match self {
            Self::System => None,
            Self::Light => Some(tauri::Theme::Light),
            Self::Dark => Some(tauri::Theme::Dark),
        }
    }
}

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum QuotaDisplayMode {
    #[default]
    Remaining,
    Used,
}

impl QuotaDisplayMode {
    pub fn percent(self, used_percent: f64) -> f64 {
        let used = if used_percent.is_finite() {
            used_percent.clamp(0.0, 100.0)
        } else {
            0.0
        };
        match self {
            Self::Remaining => 100.0 - used,
            Self::Used => used,
        }
    }
}

/// What the macOS status item shows next to the tray icon. Other platforms
/// have no title slot, so the same text goes into the tooltip instead.
#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum TrayMenubarText {
    #[default]
    None,
    Requests,
    Tokens,
    Cost,
    Subscription,
    AlertOnly,
}

/// Pages the tray popover can jump to. Overview and settings are always
/// reachable through the fixed "open" and "settings" actions, so they are not
/// listed. The popover renders the enabled subset in this declaration order.
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum TrayPage {
    Records,
    Services,
    Tokens,
    Safety,
    Routing,
    AgentTools,
}

/// Usage lines the tray menu can show. Every line is optional so the menu
/// stays short by default; the operator enables the numbers they like.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, deny_unknown_fields)]
pub struct TrayUsagePreferences {
    pub today: bool,
    pub cost: bool,
    pub subscription_windows: bool,
    pub top_model: bool,
    pub compare_yesterday: bool,
    pub cache_hit: bool,
    pub top_client: bool,
    pub last_request: bool,
    pub month_total: bool,
}

impl Default for TrayUsagePreferences {
    fn default() -> Self {
        Self {
            today: true,
            cost: true,
            subscription_windows: true,
            top_model: true,
            compare_yesterday: false,
            cache_hit: false,
            top_client: false,
            last_request: false,
            month_total: false,
        }
    }
}

impl TrayUsagePreferences {
    /// Whether any line needs a usage-summary call at all.
    pub fn needs_usage_summary(&self) -> bool {
        self.today || self.top_model || self.compare_yesterday || self.cache_hit
    }

    pub fn needs_anything(&self) -> bool {
        self.needs_usage_summary()
            || self.cost
            || self.subscription_windows
            || self.top_client
            || self.last_request
            || self.month_total
    }
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, deny_unknown_fields)]
pub struct TrayPreferences {
    pub menubar_text: TrayMenubarText,
    pub copy_address: bool,
    pub gateway_controls: bool,
    pub usage: TrayUsagePreferences,
    /// Quick-jump pages in fixed navigation order; an ordered subset of `TrayPage::ALL`.
    pub pages: Vec<TrayPage>,
}

impl Default for TrayPreferences {
    fn default() -> Self {
        Self {
            menubar_text: TrayMenubarText::None,
            copy_address: true,
            gateway_controls: true,
            usage: TrayUsagePreferences::default(),
            pages: vec![TrayPage::Records, TrayPage::Services, TrayPage::Tokens],
        }
    }
}

impl TrayPreferences {
    pub fn validate(&self, locale: Locale) -> Result<(), String> {
        let mut seen = std::collections::HashSet::new();
        for page in &self.pages {
            if !seen.insert(*page) {
                return Err(i18n::t(locale, "host.preferences.trayPagesDuplicate", &[]));
            }
        }
        Ok(())
    }
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, deny_unknown_fields)]
pub struct Preferences {
    pub close_behavior: CloseBehavior,
    pub autostart: bool,
    pub core_auto_start: bool,
    pub core_auto_recover: bool,
    pub use_system_proxy: bool,
    pub inference_port: u16,
    #[serde(default = "default_max_concurrent_inspections")]
    pub max_concurrent_inspections: u16,
    #[serde(default = "default_response_start_timeout_seconds")]
    pub response_start_timeout_seconds: u32,
    /// Maximum inference request body size in MiB; zero means unlimited.
    pub max_request_body_mib: u32,
    pub locale: Locale,
    pub theme: ThemePreference,
    pub quota_display_mode: QuotaDisplayMode,
    pub tray: TrayPreferences,
}

impl Default for Preferences {
    fn default() -> Self {
        Self {
            close_behavior: CloseBehavior::HideToTray,
            autostart: false,
            core_auto_start: true,
            core_auto_recover: true,
            use_system_proxy: true,
            inference_port: DEFAULT_INFERENCE_PORT,
            max_concurrent_inspections: DEFAULT_MAX_CONCURRENT_INSPECTIONS,
            response_start_timeout_seconds: DEFAULT_RESPONSE_START_TIMEOUT_SECONDS,
            max_request_body_mib: 0,
            locale: Locale::En,
            theme: ThemePreference::System,
            quota_display_mode: QuotaDisplayMode::Remaining,
            tray: TrayPreferences::default(),
        }
    }
}

impl Preferences {
    pub fn validate(&self) -> Result<(), String> {
        if self.inference_port < 1024 {
            return Err(i18n::t(self.locale, "host.preferences.portRange", &[]));
        }
        self.tray.validate(self.locale)?;
        if !(MIN_MAX_CONCURRENT_INSPECTIONS..=MAX_MAX_CONCURRENT_INSPECTIONS)
            .contains(&self.max_concurrent_inspections)
        {
            return Err(i18n::t(
                self.locale,
                "host.preferences.concurrencyRange",
                &[],
            ));
        }
        if self.response_start_timeout_seconds > MAX_RESPONSE_START_TIMEOUT_SECONDS {
            return Err(i18n::t(
                self.locale,
                "host.preferences.responseStartTimeoutRange",
                &[],
            ));
        }
        Ok(())
    }
}

#[derive(Clone, Debug, Serialize)]
pub struct PreferencesSnapshot {
    pub values: Preferences,
    pub load_warning: Option<String>,
}

struct StoreInner {
    values: Preferences,
    load_warning: Option<String>,
}

pub struct PreferencesStore {
    path: PathBuf,
    inner: Mutex<StoreInner>,
}

impl PreferencesStore {
    pub fn load(config_directory: &Path) -> Self {
        let path = config_directory.join(FILE_NAME);
        let (values, load_warning) = match fs::read(&path) {
            Ok(bytes) => match serde_json::from_slice::<Preferences>(&bytes) {
                Ok(values) => match values.validate() {
                    Ok(()) => (values, None),
                    Err(error) => (
                        Preferences::default(),
                        Some(i18n::t(
                            values.locale,
                            "host.preferences.invalidSettings",
                            &[("error", &error)],
                        )),
                    ),
                },
                Err(error) => (
                    Preferences::default(),
                    Some(i18n::t(
                        Locale::En,
                        "host.preferences.unparseable",
                        &[("error", &error.to_string())],
                    )),
                ),
            },
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                (Preferences::default(), None)
            }
            Err(error) => (
                Preferences::default(),
                Some(i18n::t(
                    Locale::En,
                    "host.preferences.unreadable",
                    &[("error", &error.to_string())],
                )),
            ),
        };
        Self {
            path,
            inner: Mutex::new(StoreInner {
                values,
                load_warning,
            }),
        }
    }

    pub fn snapshot(&self) -> PreferencesSnapshot {
        let inner = self.lock();
        PreferencesSnapshot {
            values: inner.values.clone(),
            load_warning: inner.load_warning.clone(),
        }
    }

    pub fn replace(&self, values: Preferences) -> Result<PreferencesSnapshot, String> {
        values.validate()?;
        persist_atomic(&self.path, &values)?;
        let mut inner = self.lock();
        inner.values = values;
        inner.load_warning = None;
        Ok(PreferencesSnapshot {
            values: inner.values.clone(),
            load_warning: None,
        })
    }

    pub fn report_warning(&self, warning: String) {
        let mut inner = self.lock();
        inner.load_warning = Some(match inner.load_warning.take() {
            Some(existing) => format!("{existing}; {warning}"),
            None => warning,
        });
    }

    fn lock(&self) -> MutexGuard<'_, StoreInner> {
        self.inner
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }
}

fn persist_atomic(path: &Path, values: &Preferences) -> Result<(), String> {
    let locale = values.locale;
    let parent = path
        .parent()
        .ok_or_else(|| i18n::t(locale, "host.preferences.noParent", &[]))?;
    fs::create_dir_all(parent).map_err(|error| {
        i18n::t(
            locale,
            "host.preferences.createDirFailed",
            &[("error", &error.to_string())],
        )
    })?;
    let bytes = serde_json::to_vec_pretty(values).map_err(|error| {
        i18n::t(
            locale,
            "host.preferences.serializeFailed",
            &[("error", &error.to_string())],
        )
    })?;
    let temporary = path.with_extension(format!(
        "tmp-{}-{}",
        std::process::id(),
        TEMPORARY_SEQUENCE.fetch_add(1, Ordering::Relaxed)
    ));
    let result = (|| {
        let mut file = OpenOptions::new()
            .create_new(true)
            .write(true)
            .open(&temporary)
            .map_err(|error| {
                i18n::t(
                    locale,
                    "host.preferences.createTempFailed",
                    &[("error", &error.to_string())],
                )
            })?;
        file.write_all(&bytes)
            .and_then(|_| file.write_all(b"\n"))
            .and_then(|_| file.sync_all())
            .map_err(|error| {
                i18n::t(
                    locale,
                    "host.preferences.writeFailed",
                    &[("error", &error.to_string())],
                )
            })?;
        atomic_replace(&temporary, path).map_err(|error| {
            i18n::t(
                locale,
                "host.preferences.replaceFailed",
                &[("error", &error.to_string())],
            )
        })?;
        #[cfg(unix)]
        fs::File::open(parent)
            .and_then(|directory| directory.sync_all())
            .map_err(|error| {
                i18n::t(
                    locale,
                    "host.preferences.syncDirFailed",
                    &[("error", &error.to_string())],
                )
            })?;
        Ok(())
    })();
    if result.is_err() {
        let _ = fs::remove_file(&temporary);
    }
    result
}

#[cfg(not(windows))]
fn atomic_replace(source: &Path, destination: &Path) -> std::io::Result<()> {
    fs::rename(source, destination)
}

#[cfg(windows)]
fn atomic_replace(source: &Path, destination: &Path) -> std::io::Result<()> {
    use std::os::windows::ffi::OsStrExt;
    use windows_sys::Win32::Storage::FileSystem::{
        MoveFileExW, MOVEFILE_REPLACE_EXISTING, MOVEFILE_WRITE_THROUGH,
    };

    let source: Vec<u16> = source.as_os_str().encode_wide().chain(Some(0)).collect();
    let destination: Vec<u16> = destination
        .as_os_str()
        .encode_wide()
        .chain(Some(0))
        .collect();
    // Both paths are local, NUL-terminated buffers owned for the duration of
    // this call. REPLACE_EXISTING preserves atomic update semantics on Windows.
    let result = unsafe {
        MoveFileExW(
            source.as_ptr(),
            destination.as_ptr(),
            MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH,
        )
    };
    if result == 0 {
        Err(std::io::Error::last_os_error())
    } else {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn temporary_directory(name: &str) -> PathBuf {
        std::env::temp_dir().join(format!(
            "astrlink-preferences-{name}-{}-{}",
            std::process::id(),
            TEMPORARY_SEQUENCE.fetch_add(1, Ordering::Relaxed)
        ))
    }

    #[test]
    fn temporary_directory_names_are_unique_and_portable() {
        let first = temporary_directory("portable");
        let second = temporary_directory("portable");
        for directory in [&first, &second] {
            let name = directory.file_name().unwrap().to_str().unwrap();
            assert!(name
                .chars()
                .all(|character| character.is_ascii_alphanumeric() || "-_".contains(character)));
        }
        assert_ne!(first, second);
    }

    #[test]
    fn missing_file_uses_safe_defaults_without_claiming_an_error() {
        let directory = temporary_directory("missing");
        let store = PreferencesStore::load(&directory);
        let snapshot = store.snapshot();
        assert_eq!(snapshot.values, Preferences::default());
        assert_eq!(snapshot.values.locale, Locale::En);
        assert_eq!(snapshot.values.theme, ThemePreference::System);
        assert_eq!(
            snapshot.values.quota_display_mode,
            QuotaDisplayMode::Remaining
        );
        assert!(snapshot.values.use_system_proxy);
        assert_eq!(snapshot.values.max_request_body_mib, 0);
        assert_eq!(snapshot.load_warning, None);
    }

    #[test]
    fn missing_locale_field_defaults_to_english() {
        let directory = temporary_directory("legacy-locale");
        fs::create_dir_all(&directory).unwrap();
        fs::write(
            directory.join(FILE_NAME),
            br#"{"close_behavior":"hide_to_tray","autostart":false,"core_auto_start":true,"core_auto_recover":true,"inference_port":8317}"#,
        )
        .unwrap();
        let snapshot = PreferencesStore::load(&directory).snapshot();
        assert_eq!(snapshot.values.locale, Locale::En);
        assert_eq!(snapshot.values.theme, ThemePreference::System);
        assert_eq!(
            snapshot.values.quota_display_mode,
            QuotaDisplayMode::Remaining
        );
        assert!(snapshot.values.use_system_proxy);
        assert_eq!(
            snapshot.values.max_concurrent_inspections,
            DEFAULT_MAX_CONCURRENT_INSPECTIONS
        );
        assert_eq!(
            snapshot.values.response_start_timeout_seconds,
            DEFAULT_RESPONSE_START_TIMEOUT_SECONDS
        );
        assert_eq!(snapshot.values.max_request_body_mib, 0);
        assert_eq!(snapshot.load_warning, None);
        let _ = fs::remove_dir_all(directory);
    }

    #[test]
    fn malformed_and_invalid_files_are_reported_honestly() {
        let directory = temporary_directory("malformed");
        fs::create_dir_all(&directory).unwrap();
        fs::write(directory.join(FILE_NAME), b"{no").unwrap();
        assert!(PreferencesStore::load(&directory)
            .snapshot()
            .load_warning
            .unwrap()
            .contains("could not be parsed"));
        fs::write(directory.join(FILE_NAME), br#"{"inference_port":80}"#).unwrap();
        assert!(PreferencesStore::load(&directory)
            .snapshot()
            .load_warning
            .unwrap()
            .contains("invalid settings"));
        let _ = fs::remove_dir_all(directory);
    }

    #[test]
    fn replacement_is_validated_and_durably_reloadable() {
        let directory = temporary_directory("replace");
        let store = PreferencesStore::load(&directory);
        let values = Preferences {
            inference_port: 9123,
            max_request_body_mib: 64,
            use_system_proxy: false,
            theme: ThemePreference::Dark,
            quota_display_mode: QuotaDisplayMode::Used,
            ..Preferences::default()
        };
        store.replace(values.clone()).unwrap();
        assert_eq!(PreferencesStore::load(&directory).snapshot().values, values);
        let mut invalid = values;
        invalid.max_concurrent_inspections = 200;
        assert!(store
            .replace(invalid.clone())
            .unwrap_err()
            .contains("between 4 and 128"));
        invalid.max_concurrent_inspections = DEFAULT_MAX_CONCURRENT_INSPECTIONS;
        invalid.response_start_timeout_seconds = MAX_RESPONSE_START_TIMEOUT_SECONDS + 1;
        assert!(store
            .replace(invalid)
            .unwrap_err()
            .contains("between 0 and 86400"));
        let _ = fs::remove_dir_all(directory);
    }

    #[test]
    fn tray_preferences_default_and_reject_duplicate_pages() {
        let directory = temporary_directory("tray-legacy");
        fs::create_dir_all(&directory).unwrap();
        // A preferences file written before the tray section existed.
        fs::write(
            directory.join(FILE_NAME),
            br#"{"close_behavior":"hide_to_tray","inference_port":8317}"#,
        )
        .unwrap();
        let snapshot = PreferencesStore::load(&directory).snapshot();
        assert_eq!(snapshot.values.tray, TrayPreferences::default());
        assert_eq!(snapshot.load_warning, None);
        assert_eq!(
            snapshot.values.tray.pages,
            vec![TrayPage::Records, TrayPage::Services, TrayPage::Tokens]
        );
        let _ = fs::remove_dir_all(directory);

        let mut duplicate = Preferences::default();
        duplicate.tray.pages = vec![TrayPage::Records, TrayPage::Records];
        assert!(duplicate.validate().is_err());

        let json = serde_json::to_value(Preferences::default()).unwrap();
        assert_eq!(json["tray"]["menubar_text"], "none");
        assert_eq!(json["tray"]["pages"][0], "records");
        assert_eq!(json["tray"]["usage"]["today"], true);
        assert_eq!(json["tray"]["usage"]["compare_yesterday"], false);
        assert!(serde_json::from_str::<TrayPreferences>(r#"{"unknown":1}"#).is_err());
        // The wire names are what the frontend stores (`TRAY_PAGES`) and sends back.
        let names: Vec<String> = [
            TrayPage::Records,
            TrayPage::Services,
            TrayPage::Tokens,
            TrayPage::Safety,
            TrayPage::Routing,
            TrayPage::AgentTools,
        ]
        .iter()
        .map(|page| {
            serde_json::to_value(page)
                .unwrap()
                .as_str()
                .unwrap()
                .to_string()
        })
        .collect();
        assert_eq!(
            names,
            [
                "records",
                "services",
                "tokens",
                "safety",
                "routing",
                "agent_tools"
            ]
        );
    }

    #[test]
    fn theme_preferences_are_strict_and_round_trip() {
        for (name, theme) in [
            ("system", ThemePreference::System),
            ("light", ThemePreference::Light),
            ("dark", ThemePreference::Dark),
        ] {
            let values = Preferences {
                theme,
                ..Preferences::default()
            };
            let json = serde_json::to_value(&values).unwrap();
            assert_eq!(json["theme"], name);
            assert_eq!(serde_json::from_value::<Preferences>(json).unwrap(), values);
        }
        assert!(serde_json::from_str::<Preferences>(r#"{"theme":"auto"}"#).is_err());
        assert!(ThemePreference::System.native_theme().is_none());
        assert_eq!(
            ThemePreference::Dark.native_theme(),
            Some(tauri::Theme::Dark)
        );
    }

    #[test]
    fn unwritable_destination_is_reported_without_mutating_memory() {
        let directory = temporary_directory("unwritable");
        fs::write(&directory, b"not a directory").unwrap();
        let store = PreferencesStore::load(&directory);
        let original = store.snapshot().values;
        let mut next = original.clone();
        next.inference_port = 9124;
        assert!(store
            .replace(next)
            .unwrap_err()
            .contains("preferences directory"));
        assert_eq!(store.snapshot().values, original);
        fs::remove_file(directory).unwrap();
    }
}
