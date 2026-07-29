use std::{
    fs::{self, File, OpenOptions},
    io::Write,
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicU64, Ordering},
        Mutex, MutexGuard,
    },
};

use serde::{Deserialize, Serialize};

pub const DEFAULT_INFERENCE_PORT: u16 = 8317;
const FILE_NAME: &str = "desktop-preferences.json";
static TEMPORARY_SEQUENCE: AtomicU64 = AtomicU64::new(0);

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum CloseBehavior {
    HideToTray,
    Quit,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, deny_unknown_fields)]
pub struct Preferences {
    pub close_behavior: CloseBehavior,
    pub autostart: bool,
    pub core_auto_start: bool,
    pub core_auto_recover: bool,
    pub inference_port: u16,
}

impl Default for Preferences {
    fn default() -> Self {
        Self {
            close_behavior: CloseBehavior::HideToTray,
            autostart: false,
            core_auto_start: true,
            core_auto_recover: true,
            inference_port: DEFAULT_INFERENCE_PORT,
        }
    }
}

impl Preferences {
    pub fn validate(&self) -> Result<(), String> {
        if self.inference_port < 1024 {
            return Err("推理端口必须在 1024–65535 之间。".to_string());
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
                        Some(format!("偏好文件包含无效设置，当前使用安全默认值：{error}")),
                    ),
                },
                Err(error) => (
                    Preferences::default(),
                    Some(format!("偏好文件无法解析，当前使用安全默认值：{error}")),
                ),
            },
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                (Preferences::default(), None)
            }
            Err(error) => (
                Preferences::default(),
                Some(format!("无法读取偏好文件，当前使用安全默认值：{error}")),
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
            Some(existing) => format!("{existing}；{warning}"),
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
    let parent = path
        .parent()
        .ok_or_else(|| "偏好文件路径没有父目录。".to_string())?;
    fs::create_dir_all(parent).map_err(|error| format!("无法创建偏好目录：{error}"))?;
    let bytes = serde_json::to_vec_pretty(values)
        .map_err(|error| format!("无法序列化偏好设置：{error}"))?;
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
            .map_err(|error| format!("无法创建临时偏好文件：{error}"))?;
        file.write_all(&bytes)
            .and_then(|_| file.write_all(b"\n"))
            .and_then(|_| file.sync_all())
            .map_err(|error| format!("无法持久写入偏好文件：{error}"))?;
        atomic_replace(&temporary, path)
            .map_err(|error| format!("无法原子替换偏好文件：{error}"))?;
        #[cfg(unix)]
        File::open(parent)
            .and_then(|directory| directory.sync_all())
            .map_err(|error| format!("无法持久同步偏好目录：{error}"))?;
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
            std::thread::current().name().unwrap_or("test")
        ))
    }

    #[test]
    fn missing_file_uses_safe_defaults_without_claiming_an_error() {
        let directory = temporary_directory("missing");
        let store = PreferencesStore::load(&directory);
        let snapshot = store.snapshot();
        assert_eq!(snapshot.values, Preferences::default());
        assert_eq!(snapshot.load_warning, None);
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
            .contains("无法解析"));
        fs::write(directory.join(FILE_NAME), br#"{"inference_port":80}"#).unwrap();
        assert!(PreferencesStore::load(&directory)
            .snapshot()
            .load_warning
            .unwrap()
            .contains("无效设置"));
        let _ = fs::remove_dir_all(directory);
    }

    #[test]
    fn replacement_is_validated_and_durably_reloadable() {
        let directory = temporary_directory("replace");
        let store = PreferencesStore::load(&directory);
        let values = Preferences {
            inference_port: 9123,
            ..Preferences::default()
        };
        store.replace(values.clone()).unwrap();
        assert_eq!(PreferencesStore::load(&directory).snapshot().values, values);
        let _ = fs::remove_dir_all(directory);
    }

    #[test]
    fn unwritable_destination_is_reported_without_mutating_memory() {
        let directory = temporary_directory("unwritable");
        fs::write(&directory, b"not a directory").unwrap();
        let store = PreferencesStore::load(&directory);
        let original = store.snapshot().values;
        let mut next = original.clone();
        next.inference_port = 9124;
        assert!(store.replace(next).unwrap_err().contains("偏好目录"));
        assert_eq!(store.snapshot().values, original);
        fs::remove_file(directory).unwrap();
    }
}
