//! Finds running processes started from a client's installation, to name
//! them when an update cannot move the client's files.

use std::path::{Path, PathBuf};

use super::ClientError;

/// The processes whose program, or for Node.js and Bun a loaded module, lies
/// in `roots`: files, or directories with everything below them.
#[cfg(windows)]
pub(super) fn find(roots: &[PathBuf]) -> Vec<(u32, PathBuf)> {
    use windows_sys::Win32::{
        Foundation::{CloseHandle, INVALID_HANDLE_VALUE},
        System::{
            Diagnostics::ToolHelp::{
                CreateToolhelp32Snapshot, Module32FirstW, Module32NextW, Process32FirstW,
                Process32NextW, MODULEENTRY32W, PROCESSENTRY32W, TH32CS_SNAPMODULE,
                TH32CS_SNAPPROCESS,
            },
            Threading::{
                OpenProcess, QueryFullProcessImageNameW, PROCESS_NAME_WIN32,
                PROCESS_QUERY_LIMITED_INFORMATION,
            },
        },
    };

    fn wide(raw: &[u16]) -> PathBuf {
        use std::os::windows::ffi::OsStringExt;
        let end = raw.iter().position(|&c| c == 0).unwrap_or(raw.len());
        std::ffi::OsString::from_wide(&raw[..end]).into()
    }
    fn image(pid: u32) -> Option<PathBuf> {
        // SAFETY: a null handle is checked; the buffer outlives the call and
        // `size` holds its length in characters.
        unsafe {
            let process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid);
            if process.is_null() {
                return None;
            }
            let mut buffer = vec![0u16; 32_768];
            let mut size = buffer.len() as u32;
            let ok = QueryFullProcessImageNameW(
                process,
                PROCESS_NAME_WIN32,
                buffer.as_mut_ptr(),
                &mut size,
            );
            CloseHandle(process);
            (ok != 0).then(|| wide(&buffer[..size as usize]))
        }
    }
    fn modules(pid: u32) -> Vec<PathBuf> {
        let mut found = Vec::new();
        // SAFETY: the snapshot handle is checked and closed; the entry's size
        // field is set before the first call as the API requires.
        unsafe {
            let snapshot = CreateToolhelp32Snapshot(TH32CS_SNAPMODULE, pid);
            if snapshot == INVALID_HANDLE_VALUE {
                return found;
            }
            let mut entry: MODULEENTRY32W = std::mem::zeroed();
            entry.dwSize = std::mem::size_of::<MODULEENTRY32W>() as u32;
            let mut more = Module32FirstW(snapshot, &mut entry) != 0;
            while more {
                found.push(wide(&entry.szExePath));
                more = Module32NextW(snapshot, &mut entry) != 0;
            }
            CloseHandle(snapshot);
        }
        found
    }

    let roots = Roots::new(roots);
    let mut pids = Vec::new();
    // SAFETY: as in `modules`.
    unsafe {
        let snapshot = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
        if snapshot == INVALID_HANDLE_VALUE {
            return vec![];
        }
        let mut entry: PROCESSENTRY32W = std::mem::zeroed();
        entry.dwSize = std::mem::size_of::<PROCESSENTRY32W>() as u32;
        let mut more = Process32FirstW(snapshot, &mut entry) != 0;
        while more {
            pids.push((entry.th32ProcessID, wide(&entry.szExeFile)));
            more = Process32NextW(snapshot, &mut entry) != 0;
        }
        CloseHandle(snapshot);
    }
    let own = std::process::id();
    let mut found = Vec::new();
    for (pid, name) in pids {
        if pid == own || pid == 0 {
            continue;
        }
        let Some(path) = image(pid) else {
            continue;
        };
        if roots.contains(&path) {
            found.push((pid, path));
            continue;
        }
        // Script clients run inside Node.js or Bun; their native addons
        // are loaded from the installation.
        let stem = Path::new(&name)
            .file_stem()
            .map(|s| s.to_string_lossy().to_ascii_lowercase());
        if matches!(stem.as_deref(), Some("node" | "bun"))
            && modules(pid).iter().any(|module| roots.contains(module))
        {
            found.push((pid, path));
        }
    }
    found
}

#[cfg(not(windows))]
pub(super) fn find(_roots: &[PathBuf]) -> Vec<(u32, PathBuf)> {
    vec![]
}

/// Fails with `running` when a process from `roots` is running.
pub(super) fn refuse(roots: &[PathBuf]) -> Result<(), ClientError> {
    let found = find(roots);
    if found.is_empty() {
        return Ok(());
    }
    Err(ClientError::new(
        "running",
        found
            .iter()
            .map(|(pid, path)| format!("{pid} {}", path.display()))
            .collect::<Vec<_>>()
            .join("\n"),
    ))
}

/// Paths compared as Windows compares them: case-insensitively, with either
/// separator, both as given and with links and short names resolved.
#[cfg_attr(not(any(windows, test)), allow(dead_code))]
struct Roots(Vec<String>);

#[cfg_attr(not(any(windows, test)), allow(dead_code))]
impl Roots {
    fn new(roots: &[PathBuf]) -> Self {
        let mut keys = Vec::new();
        for root in roots {
            keys.push(key(root));
            if let Ok(resolved) = dunce::canonicalize(root) {
                keys.push(key(&resolved));
            }
        }
        keys.sort();
        keys.dedup();
        Self(keys)
    }
    fn contains(&self, path: &Path) -> bool {
        let path = key(path);
        self.0.iter().any(|root| {
            path == *root
                || path
                    .strip_prefix(root.as_str())
                    .is_some_and(|rest| rest.starts_with('\\'))
        })
    }
}

#[cfg_attr(not(any(windows, test)), allow(dead_code))]
fn key(path: &Path) -> String {
    let text = dunce::simplified(path)
        .to_string_lossy()
        .replace('/', "\\")
        .to_lowercase();
    text.trim_end_matches('\\').to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn matches_files_and_directories_like_windows_paths() {
        let roots = Roots::new(&[
            PathBuf::from(r"C:\Users\A\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code"),
            PathBuf::from(r"C:\Users\A\.local\bin\claude.exe"),
        ]);
        for inside in [
            r"C:\Users\A\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code\bin\claude.exe",
            r"c:\users\a\appdata\roaming\npm\NODE_MODULES\@anthropic-ai\claude-code",
            "C:/Users/A/.local/bin/claude.exe",
        ] {
            assert!(roots.contains(Path::new(inside)), "{inside}");
        }
        for outside in [
            r"C:\Users\A\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code-old\claude.exe",
            r"C:\Users\A\AppData\Roaming\npm\node.exe",
            r"C:\Users\A\.local\bin\claude.exe.old",
        ] {
            assert!(!roots.contains(Path::new(outside)), "{outside}");
        }
    }

    #[cfg(windows)]
    #[test]
    fn refuses_while_a_program_from_the_installation_runs() {
        let dir = std::env::temp_dir().join(format!("astrlink-running-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let system = std::env::var_os("SystemRoot").unwrap();
        let ping = dir.join("ping.exe");
        std::fs::copy(Path::new(&system).join(r"System32\PING.EXE"), &ping).unwrap();
        let mut child = std::process::Command::new(&ping)
            .args(["-n", "30", "127.0.0.1"])
            .stdout(std::process::Stdio::null())
            .spawn()
            .unwrap();
        let error = refuse(std::slice::from_ref(&dir)).unwrap_err();
        let other = refuse(&[dir.join("other")]);
        let _ = child.kill();
        let _ = child.wait();
        let _ = std::fs::remove_dir_all(&dir);
        assert_eq!(error.code, "running");
        assert!(
            error.detail.starts_with(&format!("{} ", child.id())),
            "{}",
            error.detail
        );
        assert!(other.is_ok());
    }
}
