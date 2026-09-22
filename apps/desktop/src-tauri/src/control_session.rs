use std::{
    fs::{self, OpenOptions},
    io::Write,
    path::{Path, PathBuf},
};

use serde::{Deserialize, Serialize};

pub const SESSION_SCHEMA_VERSION: u32 = 1;
pub const CONTROL_SOCKET_FILE_NAME: &str = "control.sock";

#[derive(Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct ControlSessionFile {
    pub schema_version: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub control_socket: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub control_url: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub control_token: Option<String>,
    /// Core process id, for readers that want to check liveness.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pid: Option<u32>,
    /// Desktop process that wrote the file. Clearing is skipped when another
    /// live desktop owns it, so a restart cannot delete its successor's file.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub desktop_pid: Option<u32>,
}

pub fn user_home() -> Result<PathBuf, String> {
    if let Ok(value) = std::env::var("ASTRLINK_AGENT_HOME") {
        if !value.is_empty() {
            return Ok(PathBuf::from(value));
        }
    }
    std::env::var("HOME")
        .or_else(|_| std::env::var("USERPROFILE"))
        .map(PathBuf::from)
        .map_err(|_| "unable to resolve the user home directory".to_string())
}

pub fn astrlink_home(home: &Path) -> PathBuf {
    home.join(".astrlink")
}

pub fn session_path(home: &Path) -> PathBuf {
    astrlink_home(home).join("control-session.json")
}

pub fn control_socket_path(data_directory: &Path) -> PathBuf {
    data_directory.join(CONTROL_SOCKET_FILE_NAME)
}

pub fn publish_control_session(
    home: &Path,
    data_directory: &Path,
    control_url: &str,
    control_token: Option<&str>,
    pid: Option<u32>,
) -> Result<(), String> {
    let session = if cfg!(windows) {
        ControlSessionFile {
            schema_version: SESSION_SCHEMA_VERSION,
            control_socket: None,
            control_url: Some(control_url.to_string()),
            control_token: control_token.map(str::to_string),
            pid,
            desktop_pid: Some(std::process::id()),
        }
    } else {
        ControlSessionFile {
            schema_version: SESSION_SCHEMA_VERSION,
            control_socket: Some(
                control_socket_path(data_directory)
                    .to_str()
                    .ok_or_else(|| "control socket path is not valid UTF-8".to_string())?
                    .to_string(),
            ),
            control_url: None,
            control_token: None,
            pid,
            desktop_pid: Some(std::process::id()),
        }
    };
    write_private_json(&session_path(home), &session)
}

/// Removes the session file unless another desktop process published it.
/// During a development restart the old instance shuts down after the new
/// one has already announced itself; deleting here would leave MCP clients
/// without a locator until the next full launch.
pub fn clear_control_session(home: &Path) -> Result<(), String> {
    clear_control_session_for(home, std::process::id())
}

fn clear_control_session_for(home: &Path, current_pid: u32) -> Result<(), String> {
    let path = session_path(home);
    let owned_by_other = fs::read(&path)
        .ok()
        .and_then(|raw| serde_json::from_slice::<ControlSessionFile>(&raw).ok())
        .and_then(|session| session.desktop_pid)
        .is_some_and(|owner| owner != current_pid);
    if owned_by_other {
        return Ok(());
    }
    match fs::remove_file(&path) {
        Ok(()) => Ok(()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(format!("unable to remove control session: {error}")),
    }
}

fn write_private_json(path: &Path, value: &impl Serialize) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)
            .map_err(|error| format!("unable to create {}: {error}", parent.display()))?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let _ = fs::set_permissions(parent, fs::Permissions::from_mode(0o700));
        }
    }
    let payload = serde_json::to_vec_pretty(value)
        .map_err(|error| format!("unable to serialize control session: {error}"))?;
    let temporary = path.with_extension("json.tmp");
    {
        let mut options = OpenOptions::new();
        options.write(true).create(true).truncate(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options
            .open(&temporary)
            .map_err(|error| format!("unable to write control session: {error}"))?;
        file.write_all(&payload)
            .map_err(|error| format!("unable to write control session: {error}"))?;
        file.write_all(b"\n").ok();
    }
    fs::rename(&temporary, path)
        .map_err(|error| format!("unable to replace control session: {error}"))?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn unix_session_omits_control_token() {
        if cfg!(windows) {
            return;
        }
        let home = tempfile_home("session-unix");
        let data = home.join("data");
        fs::create_dir_all(&data).unwrap();
        publish_control_session(
            &home,
            &data,
            "http://127.0.0.1:9",
            Some("secret-token"),
            Some(7),
        )
        .unwrap();
        let raw = fs::read_to_string(session_path(&home)).unwrap();
        assert!(!raw.contains("secret-token"));
        assert!(!raw.contains("control_token"));
        assert!(raw.contains("control.sock"));
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn clear_removes_session_file() {
        let home = tempfile_home("session-clear");
        let data = home.join("data");
        fs::create_dir_all(&data).unwrap();
        publish_control_session(
            &home,
            &data,
            "http://127.0.0.1:9",
            Some("secret-token"),
            None,
        )
        .unwrap();
        assert!(session_path(&home).is_file());
        clear_control_session(&home).unwrap();
        assert!(!session_path(&home).exists());
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn clear_leaves_a_session_published_by_another_desktop() {
        let home = tempfile_home("session-foreign");
        let data = home.join("data");
        fs::create_dir_all(&data).unwrap();
        publish_control_session(&home, &data, "http://127.0.0.1:9", None, Some(7)).unwrap();
        let raw = fs::read_to_string(session_path(&home)).unwrap();
        assert!(raw.contains("desktop_pid"));

        // A different desktop process (the successor after a dev restart)
        // must not lose its locator to our shutdown.
        clear_control_session_for(&home, std::process::id().wrapping_add(1)).unwrap();
        assert!(session_path(&home).is_file());

        // Legacy files without an owner are still cleared.
        fs::write(
            session_path(&home),
            br#"{"schema_version":1,"control_socket":"/tmp/x.sock"}"#,
        )
        .unwrap();
        clear_control_session_for(&home, std::process::id().wrapping_add(1)).unwrap();
        assert!(!session_path(&home).exists());
        let _ = fs::remove_dir_all(&home);
    }

    fn tempfile_home(name: &str) -> PathBuf {
        let path = std::env::temp_dir().join(format!(
            "astrlink-control-session-{}-{}",
            name,
            std::process::id()
        ));
        let _ = fs::remove_dir_all(&path);
        fs::create_dir_all(&path).unwrap();
        path
    }
}
