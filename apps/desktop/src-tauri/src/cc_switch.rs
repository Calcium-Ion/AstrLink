use std::{
    fs,
    path::{Path, PathBuf},
};

use serde_json::Value;

use crate::agent_home::ToolDirs;

/// Whether the OS has an app registered for CC Switch's links, so a write
/// can warn that switching providers there overwrites it.
pub fn installed() -> bool {
    opens_links("ccswitch")
}

/// Whether the OS has an app registered for `scheme:` links.
#[cfg(target_os = "macos")]
pub fn opens_links(scheme: &str) -> bool {
    use core_foundation::{
        base::{kCFAllocatorDefault, TCFType},
        error::CFErrorRef,
        string::CFString,
        url::{CFURLCreateWithString, CFURLRef, CFURL},
    };
    use std::ptr;

    #[link(name = "CoreServices", kind = "framework")]
    extern "C" {
        fn LSCopyDefaultApplicationURLForURL(
            url: CFURLRef,
            roles: u32,
            error: *mut CFErrorRef,
        ) -> CFURLRef;
    }
    const LS_ROLES_ALL: u32 = u32::MAX;

    let link = CFString::new(&format!("{scheme}://open"));
    // SAFETY: `link` outlives the call; a non-null result follows the create rule.
    let url = unsafe {
        CFURLCreateWithString(kCFAllocatorDefault, link.as_concrete_TypeRef(), ptr::null())
    };
    if url.is_null() {
        return false;
    }
    let url = unsafe { CFURL::wrap_under_create_rule(url) };
    // SAFETY: `url` outlives the call; a non-null result follows the copy rule.
    let app = unsafe {
        LSCopyDefaultApplicationURLForURL(url.as_concrete_TypeRef(), LS_ROLES_ALL, ptr::null_mut())
    };
    if app.is_null() {
        return false;
    }
    let app = unsafe { CFURL::wrap_under_create_rule(app) };
    // Launch Services can keep a record briefly after the app is deleted.
    app.to_path().is_some_and(|path| path.exists())
}

#[cfg(windows)]
pub fn opens_links(scheme: &str) -> bool {
    use std::ptr;
    use windows_sys::Win32::{
        Foundation::ERROR_SUCCESS,
        System::Registry::{
            RegGetValueW, HKEY_CLASSES_ROOT, RRF_NOEXPAND, RRF_RT_REG_EXPAND_SZ, RRF_RT_REG_SZ,
        },
    };

    let key: Vec<u16> = format!("{scheme}\\shell\\open\\command")
        .encode_utf16()
        .chain([0])
        .collect();
    let mut size = 0u32;
    // SAFETY: `key` is NUL-terminated and outlives the call; a null value name
    // reads the default value and a null data pointer only queries its size.
    let status = unsafe {
        RegGetValueW(
            HKEY_CLASSES_ROOT,
            key.as_ptr(),
            ptr::null(),
            RRF_RT_REG_SZ | RRF_RT_REG_EXPAND_SZ | RRF_NOEXPAND,
            ptr::null_mut(),
            ptr::null_mut(),
            &mut size,
        )
    };
    // An empty command is a lone UTF-16 NUL.
    status == ERROR_SUCCESS && size > 2
}

#[cfg(target_os = "linux")]
pub fn opens_links(scheme: &str) -> bool {
    gtk::gio::AppInfo::default_for_uri_scheme(scheme).is_some()
}

#[cfg(not(any(target_os = "macos", windows, target_os = "linux")))]
pub fn opens_links(_scheme: &str) -> bool {
    false
}

/// The configuration directories CC Switch's settings move each app to, so
/// agent installs and client configs reach the same directories, inside WSL
/// included. Missing or unreadable settings move nothing.
pub fn config_dirs(home: &Path) -> ToolDirs {
    let settings = fs::read_to_string(home.join(".cc-switch").join("settings.json"))
        .ok()
        .and_then(|raw| serde_json::from_str::<Value>(&raw).ok())
        .unwrap_or_default();
    let dir = |key: &str| {
        settings
            .get(key)
            .and_then(Value::as_str)
            .and_then(|raw| resolve_dir(home, raw))
    };
    ToolDirs {
        claude: dir("claudeConfigDir"),
        codex: dir("codexConfigDir"),
        grok: dir("grokConfigDir"),
        gemini: dir("geminiConfigDir"),
        opencode: dir("opencodeConfigDir"),
        openclaw: dir("openclawConfigDir"),
        pi: dir("piConfigDir"),
    }
}

/// Resolves a directory setting the way CC Switch does: `~` is the home, and
/// anything still relative is ignored.
fn resolve_dir(home: &Path, raw: &str) -> Option<PathBuf> {
    let raw = raw.trim();
    let path = if raw == "~" {
        home.to_path_buf()
    } else if let Some(rest) = raw.strip_prefix("~/").or_else(|| raw.strip_prefix("~\\")) {
        rest.split(['/', '\\'])
            .filter(|part| !part.is_empty())
            .fold(home.to_path_buf(), |path, part| path.join(part))
    } else {
        PathBuf::from(raw)
    };
    path.is_absolute().then_some(path)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn config_dirs_follow_cc_switch_settings() {
        let home = std::env::temp_dir().join(format!("astrlink-ccs-dirs-{}", std::process::id()));
        let _ = fs::remove_dir_all(&home);
        assert_eq!(config_dirs(&home), ToolDirs::default());
        fs::create_dir_all(home.join(".cc-switch")).unwrap();
        fs::write(
            home.join(".cc-switch").join("settings.json"),
            r#"{"claudeConfigDir":"~/work/.claude","codexConfigDir":"  ","grokConfigDir":"relative/grok","piConfigDir":"/opt/pi/agent","showInTray":true}"#,
        )
        .unwrap();
        let absolute = if cfg!(windows) {
            None
        } else {
            Some(PathBuf::from("/opt/pi/agent"))
        };
        assert_eq!(
            config_dirs(&home),
            ToolDirs {
                claude: Some(home.join("work").join(".claude")),
                codex: None,
                grok: None,
                gemini: None,
                opencode: None,
                openclaw: None,
                pi: absolute,
            }
        );
        fs::write(home.join(".cc-switch").join("settings.json"), "not json").unwrap();
        assert_eq!(config_dirs(&home), ToolDirs::default());
        fs::remove_dir_all(&home).unwrap();
    }
}
