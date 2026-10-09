use std::{
    ffi::OsString,
    io::Read,
    path::PathBuf,
    process::{Child, Command, Stdio},
    thread,
    time::{Duration, Instant},
};

use super::ClientError;

#[derive(Clone, Debug, PartialEq, Eq)]
pub(super) struct CommandSpec {
    pub program: PathBuf,
    pub args: Vec<OsString>,
}

impl CommandSpec {
    pub fn new(program: impl Into<PathBuf>, args: &[&str]) -> Self {
        Self {
            program: program.into(),
            args: args.iter().map(OsString::from).collect(),
        }
    }
}

#[derive(Clone)]
pub(super) struct Environment {
    pub home: PathBuf,
    pub path: OsString,
    pub use_system_proxy: bool,
    pub proxy_env: Option<Vec<(String, String)>>,
}

// Every subprocess has closed stdin, bounded output, a deadline and its own
// process group/job. A hung npm child cannot outlive a timed-out update.
struct ProcessTree {
    child: Child,
    #[cfg(windows)]
    _job: crate::sidecar::windows_job::JobObject,
}

impl Drop for ProcessTree {
    fn drop(&mut self) {
        #[cfg(unix)]
        unsafe {
            // The child was launched as the leader of a new process group.
            libc::kill(-(self.child.id() as i32), libc::SIGKILL);
        }
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

fn capture(mut stream: impl Read) -> Vec<u8> {
    let mut output = Vec::new();
    let mut chunk = [0; 4096];
    while let Ok(count) = stream.read(&mut chunk) {
        if count == 0 {
            break;
        }
        output.extend_from_slice(&chunk[..count]);
        if output.len() > 16_384 {
            output.drain(..output.len() - 16_384);
        }
    }
    output
}

/// Whether `path` holds a PE image. CreateProcess treats any other .exe as a
/// 16-bit DOS program, which 64-bit Windows refuses with a modal
/// "Unsupported 16-Bit Application" dialog on top of the error. Claude Code's
/// npm package keeps a text stub at bin/claude.exe when its postinstall did
/// not run or its platform package was not downloaded.
#[cfg(any(windows, test))]
pub(super) fn windows_image(path: &std::path::Path) -> std::io::Result<bool> {
    use std::io::{Seek, SeekFrom};
    let mut file = std::fs::File::open(path)?;
    let mut header = Vec::with_capacity(64);
    (&mut file).take(64).read_to_end(&mut header)?;
    if header.len() < 64 || !header.starts_with(b"MZ") {
        return Ok(false);
    }
    let offset = u32::from_le_bytes([header[60], header[61], header[62], header[63]]);
    file.seek(SeekFrom::Start(offset.into()))?;
    let mut signature = Vec::with_capacity(4);
    file.take(4).read_to_end(&mut signature)?;
    Ok(signature == b"PE\0\0")
}

pub(super) fn run(
    environment: &Environment,
    spec: &CommandSpec,
    deadline: Duration,
) -> Result<String, ClientError> {
    // A file that cannot be read is left to CreateProcess, which reports it
    // without a dialog.
    #[cfg(windows)]
    if matches!(windows_image(&spec.program), Ok(false)) {
        return Err(ClientError::new(
            "damaged",
            format!("{} is not a Windows program", spec.program.display()),
        ));
    }
    let mut command = Command::new(&spec.program);
    command
        .args(&spec.args)
        .current_dir(&environment.home)
        .env("PATH", &environment.path)
        .env("NO_COLOR", "1")
        .env("CI", "1")
        .env("DISABLE_AUTOUPDATER", "1")
        .env("HOMEBREW_NO_INSTALL_CLEANUP", "1")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    if !environment.use_system_proxy || environment.proxy_env.is_some() {
        for name in [
            "HTTP_PROXY",
            "HTTPS_PROXY",
            "ALL_PROXY",
            "http_proxy",
            "https_proxy",
            "all_proxy",
            "NO_PROXY",
            "no_proxy",
            "npm_config_proxy",
            "npm_config_https_proxy",
            "npm_config_noproxy",
        ] {
            command.env_remove(name);
        }
    }
    if environment.use_system_proxy {
        if let Some(proxy_env) = &environment.proxy_env {
            command.envs(proxy_env.iter().cloned());
        }
    } else {
        command.env("NO_PROXY", "*").env("no_proxy", "*");
        command
            .env("npm_config_proxy", "")
            .env("npm_config_https_proxy", "");
    }
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        command.process_group(0);
    }
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(0x08000000); // CREATE_NO_WINDOW
    }
    let mut child = command
        .spawn()
        .map_err(|e| ClientError::new("command", e))?;
    #[cfg(windows)]
    let job = match crate::sidecar::windows_job::JobObject::attach(child.id()) {
        Ok(job) => job,
        Err(error) => {
            let _ = child.kill();
            let _ = child.wait();
            return Err(ClientError::new("command", error));
        }
    };
    let stdout = child.stdout.take().expect("piped stdout");
    let stderr = child.stderr.take().expect("piped stderr");
    let out = thread::spawn(move || capture(stdout));
    let err = thread::spawn(move || capture(stderr));
    let mut process = ProcessTree {
        child,
        #[cfg(windows)]
        _job: job,
    };
    let start = Instant::now();
    let result = loop {
        match process.child.try_wait() {
            Ok(Some(status)) => break Ok(status),
            Err(error) => break Err(ClientError::new("command", error)),
            _ if start.elapsed() >= deadline => {
                break Err(ClientError::new("timeout", "Command timed out"))
            }
            _ => thread::sleep(Duration::from_millis(20)),
        }
    };
    drop(process);
    let stdout = String::from_utf8_lossy(&out.join().unwrap_or_default()).into_owned();
    let stderr = String::from_utf8_lossy(&err.join().unwrap_or_default()).into_owned();
    if !result?.success() {
        return Err(ClientError::new(
            "command",
            format!("{stderr}\n{stdout}").trim(),
        ));
    }
    // Some upstream installers use pipelines whose final process exits zero
    // even when the download failed. Keep stderr for post-update verification.
    Ok(if stderr.trim().is_empty() {
        stdout
    } else {
        format!("{stdout}\n{stderr}")
    })
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    fn environment() -> Environment {
        Environment {
            home: std::env::temp_dir(),
            path: "/usr/bin:/bin".into(),
            use_system_proxy: false,
            proxy_env: None,
        }
    }

    #[test]
    fn bounds_output_and_reports_nonzero_exit() {
        let output = run(
            &environment(),
            &CommandSpec::new("/bin/sh", &["-c", "head -c 30000 /dev/zero"]),
            Duration::from_secs(3),
        )
        .unwrap();
        assert_eq!(output.len(), 16_384);
        let error = run(
            &environment(),
            &CommandSpec::new("/bin/sh", &["-c", "echo denied >&2; exit 1"]),
            Duration::from_secs(3),
        )
        .unwrap_err();
        assert_eq!(error.code, "command");
        assert!(error.detail.contains("denied"));
    }

    #[test]
    fn timeout_stops_descendants_and_reaps_the_process() {
        let started = Instant::now();
        let error = run(
            &environment(),
            &CommandSpec::new("/bin/sh", &["-c", "sleep 30 & wait"]),
            Duration::from_millis(100),
        )
        .unwrap_err();
        assert_eq!(error.code, "timeout");
        assert!(started.elapsed() < Duration::from_secs(3));
    }

    #[test]
    fn passes_arguments_literally_without_a_shell() {
        let output = run(
            &environment(),
            &CommandSpec::new("/usr/bin/printf", &["%s", "$(touch should-not-exist); &"]),
            Duration::from_secs(3),
        )
        .unwrap();
        assert_eq!(output, "$(touch should-not-exist); &");
    }
}
