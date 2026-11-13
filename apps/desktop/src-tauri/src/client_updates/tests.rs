use super::*;
use std::{
    io::{Read, Write},
    net::TcpListener,
    sync::atomic::{AtomicU64, Ordering},
};

static NEXT: AtomicU64 = AtomicU64::new(0);
struct Fixture(PathBuf);
impl Fixture {
    fn new() -> Self {
        let path = std::env::temp_dir().join(format!(
            "astrlink-client-test-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::SeqCst)
        ));
        fs::create_dir_all(&path).unwrap();
        Self(path)
    }
    fn write(&self, path: &str, contents: &str) -> PathBuf {
        let path = self.0.join(path);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(&path, contents).unwrap();
        path
    }
    #[cfg(unix)]
    fn script(&self, path: &str, contents: &str) -> PathBuf {
        use std::os::unix::fs::PermissionsExt;
        let path = self.write(path, contents);
        fs::set_permissions(&path, fs::Permissions::from_mode(0o755)).unwrap();
        path
    }
    fn environment(&self) -> Environment {
        Environment {
            home: self.0.clone(),
            path: std::env::join_paths([
                self.0.join("bin"),
                PathBuf::from("/usr/bin"),
                PathBuf::from("/bin"),
            ])
            .unwrap(),
            use_system_proxy: false,
            proxy_env: None,
            wsl: None,
        }
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn version(id: ClientId, raw: &str) -> ClientVersion {
    ClientVersion::parse(id, raw).unwrap()
}

#[test]
fn reads_version_output_and_compares_semver_without_downgrading() {
    assert_eq!(
        parse_version(ClientId::Codex, "codex-cli 0.153.4\n").unwrap(),
        version(ClientId::Codex, "0.153.4")
    );
    assert_eq!(
        parse_version(ClientId::Claude, "2.1.280 (Claude Code)\n")
            .unwrap()
            .order,
        Version::new(2, 1, 280)
    );
    assert!(version(ClientId::Pi, "v2.10.0").newer_than(&version(ClientId::Pi, "2.9.9")));
    assert!(!version(ClientId::Pi, "2.9.9").newer_than(&version(ClientId::Pi, "2.9.9")));
    assert!(
        version(ClientId::Codex, "0.160.0-beta.1").newer_than(&version(ClientId::Codex, "0.159.0"))
    );
    assert!(parse_version(ClientId::Codex, "not installed").is_err());
}

#[test]
fn orders_dated_cursor_builds() {
    let current = parse_version(ClientId::Cursor, "2026.08.11-e8db854\n").unwrap();
    assert_eq!(current.to_string(), "2026.08.11-e8db854");
    let latest = version(ClientId::Cursor, "2026.09.28-64d2043");
    assert!(latest.newer_than(&current));
    assert!(!current.newer_than(&latest));
    assert!(!latest.newer_than(&latest.clone()));
    // Like Cursor's updater, another build from the same day is an update.
    assert!(version(ClientId::Cursor, "2026.09.28-1a2b3c4").newer_than(&latest));
    for invalid in ["2026.9.28-64d2043", "1.2.3", "2026.09.28-$(touch)"] {
        assert!(ClientVersion::parse(ClientId::Cursor, invalid).is_none());
    }
}

#[test]
fn identifies_npm_and_brew_without_confusing_native_installations() {
    let fixture = Fixture::new();
    let npm = fixture.write(
        "node/lib/node_modules/@openai/codex/package.json",
        r#"{"name":"@openai/codex"}"#,
    );
    let binary = npm.parent().unwrap().join("bin/codex.js");
    assert_eq!(
        installation_method(&fixture.environment(), ClientId::Codex, &binary),
        InstallMethod::Npm {
            prefix: fixture.0.join("node")
        }
    );
    let homebrew = PathBuf::from("/opt/homebrew/Caskroom/claude-code@latest/2.1.280/claude");
    assert_eq!(
        installation_method(&fixture.environment(), ClientId::Claude, &homebrew),
        InstallMethod::Brew {
            root: "/opt/homebrew".into(),
            package: "claude-code@latest".into(),
            cask: true
        }
    );
    assert!(matches!(
        installation_method(
            &fixture.environment(),
            ClientId::Codex,
            Path::new("/usr/local/Cellar/codex/1.0/bin/codex")
        ),
        InstallMethod::Brew { cask: false, .. }
    ));
    assert_eq!(
        installation_method(
            &fixture.environment(),
            ClientId::Claude,
            &fixture.0.join(".local/share/claude/versions/2.1.0")
        ),
        InstallMethod::Native
    );
    assert_eq!(
        installation_method(
            &fixture.environment(),
            ClientId::Codex,
            &fixture.0.join("random/codex")
        ),
        InstallMethod::Unknown
    );
}

#[test]
fn recognizes_a_standalone_codex_package_by_its_entrypoint() {
    let fixture = Fixture::new();
    // The Windows package names bin/codex.exe.
    fixture.write(
        "release/codex-package.json",
        r#"{"variant":"codex","layoutVersion":1,"entrypoint":"bin/codex.exe"}"#,
    );
    let binary = fixture.write("release/bin/codex.exe", "");
    let environment = fixture.environment();
    assert_eq!(
        installation_method(&environment, ClientId::Codex, &binary),
        InstallMethod::Native
    );
    let other = fixture.write("release/bin/codex", "");
    assert_eq!(
        installation_method(&environment, ClientId::Codex, &other),
        InstallMethod::Unknown
    );
}

#[cfg(windows)]
#[test]
fn reads_the_saved_windows_path() {
    let path = saved_path().unwrap();
    // The system Path holds System32, usually as %SystemRoot%\system32.
    assert!(
        std::env::split_paths(&path).any(|d| d.join("cmd.exe").is_file()),
        "{path:?}"
    );
}

#[test]
fn excludes_other_package_managers_and_project_node_modules() {
    let fixture = Fixture::new();
    let package = fixture.write(
        ".bun/install/global/node_modules/@openai/codex/package.json",
        r#"{"name":"@openai/codex"}"#,
    );
    assert_eq!(
        installation_method(
            &fixture.environment(),
            ClientId::Codex,
            &package.parent().unwrap().join("bin/codex.js")
        ),
        InstallMethod::Unknown
    );
    #[cfg(unix)]
    {
        let project = fixture.write(
            "project/node_modules/@openai/codex/package.json",
            r#"{"name":"@openai/codex"}"#,
        );
        assert_eq!(
            installation_method(
                &fixture.environment(),
                ClientId::Codex,
                &project.parent().unwrap().join("bin/codex.js")
            ),
            InstallMethod::Unknown
        );
    }
    assert!(find_programs(std::ffi::OsStr::new(".:relative"), "codex").is_empty());
}

#[test]
fn identifies_cursor_and_pi_installations() {
    let fixture = Fixture::new();
    assert_eq!(
        installation_method(
            &fixture.environment(),
            ClientId::Cursor,
            &fixture
                .0
                .join(".local/share/cursor-agent/versions/2026.09.28-64d2043/cursor-agent")
        ),
        InstallMethod::Native
    );
    assert_eq!(
        installation_method(
            &fixture.environment(),
            ClientId::Cursor,
            Path::new(
                "/opt/homebrew/Caskroom/cursor-cli/2026.09.28-64d2043/dist-package/cursor-agent"
            )
        ),
        InstallMethod::Brew {
            root: "/opt/homebrew".into(),
            package: "cursor-cli".into(),
            cask: true
        }
    );
    // The Homebrew formula wraps an npm layout inside the Cellar.
    let formula = fixture.write(
        "brew/Cellar/pi-coding-agent/0.87.1/libexec/lib/node_modules/@earendil-works/pi-coding-agent/package.json",
        r#"{"name":"@earendil-works/pi-coding-agent"}"#,
    );
    assert_eq!(
        installation_method(
            &fixture.environment(),
            ClientId::Pi,
            &formula.parent().unwrap().join("dist/cli.js")
        ),
        InstallMethod::Brew {
            root: fixture.0.join("brew"),
            package: "pi-coding-agent".into(),
            cask: false
        }
    );
    let legacy = fixture.write(
        "node/lib/node_modules/@mariozechner/pi-coding-agent/package.json",
        r#"{"name":"@mariozechner/pi-coding-agent"}"#,
    );
    assert_eq!(
        installation_method(
            &fixture.environment(),
            ClientId::Pi,
            &legacy.parent().unwrap().join("dist/cli.js")
        ),
        InstallMethod::Npm {
            prefix: fixture.0.join("node")
        }
    );
    let bun = fixture.write(
        ".bun/install/global/node_modules/@earendil-works/pi-coding-agent/package.json",
        r#"{"name":"@earendil-works/pi-coding-agent"}"#,
    );
    assert_eq!(
        installation_method(
            &fixture.environment(),
            ClientId::Pi,
            &bun.parent().unwrap().join("dist/cli.js")
        ),
        InstallMethod::Package("bun")
    );
    fixture.write(
        ".pi/agent/install/managed-install.json",
        r#"{"kind":"pi-managed-install","schemaVersion":1,"layout":"releases-v1"}"#,
    );
    assert_eq!(
        installation_method(
            &fixture.environment(),
            ClientId::Pi,
            &fixture.0.join(".pi/agent/bin/pi")
        ),
        InstallMethod::Native
    );
    // Windows PowerShell 5.1 writes the marker with a BOM.
    fixture.write(
        ".pi/agent/install/managed-install.json",
        "\u{feff}{\"kind\":\"pi-managed-install\",\"schemaVersion\":1,\"layout\":\"releases-v1\"}",
    );
    assert_eq!(
        installation_method(
            &fixture.environment(),
            ClientId::Pi,
            &fixture.0.join(".pi/agent/bin/pi-launcher.js")
        ),
        InstallMethod::Native
    );
}

#[cfg(unix)]
#[test]
fn ignores_unrelated_pi_commands() {
    let fixture = Fixture::new();
    fixture.script("bin/pi", "#!/bin/sh\necho 3.14\n");
    assert!(detect(&fixture.environment(), ClientId::Pi).is_none());
    // Standalone builds ship their package.json beside the binary.
    fixture.write(
        "bin/package.json",
        r#"{"name":"@earendil-works/pi-coding-agent"}"#,
    );
    let install = detect(&fixture.environment(), ClientId::Pi).unwrap();
    assert_eq!(install.method, InstallMethod::Unknown);
}

#[test]
fn reads_npm_cmd_shim_targets_inside_the_shim_directory() {
    let fixture = Fixture::new();
    let codex = fixture
        .0
        .join("npm/node_modules/@openai/codex/bin/codex.js");
    for shim in [
        // npm 7 and later
        r#"@ECHO off
GOTO start
:find_dp0
SET dp0=%~dp0
EXIT /b
:start
SETLOCAL
CALL :find_dp0

IF EXIST "%dp0%\node.exe" (
  SET "_prog=%dp0%\node.exe"
) ELSE (
  SET "_prog=node"
  SET PATHEXT=%PATHEXT:;.JS;=;%
)

endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%"  "%dp0%\node_modules\@openai\codex\bin\codex.js" %*
"#,
        // npm 6
        r#"@IF EXIST "%~dp0\node.exe" (
  "%~dp0\node.exe"  "%~dp0\node_modules\@openai\codex\bin\codex.js" %*
) ELSE (
  @SETLOCAL
  @SET PATHEXT=%PATHEXT:;.JS;=;%
  node  "%~dp0\node_modules\@openai\codex\bin\codex.js" %*
)
"#,
    ] {
        let path = fixture.write("npm/codex.cmd", shim);
        assert_eq!(cmd_shim_target(&path), Some(codex.clone()));
    }
    // A native binary has no interpreter, as in Claude Code 2.1.113 and later.
    let path = fixture.write(
        "npm/claude.cmd",
        r#"CALL :find_dp0
"%dp0%\node_modules\@anthropic-ai\claude-code\bin\claude.exe"   %*
"#,
    );
    assert_eq!(
        cmd_shim_target(&path),
        Some(
            fixture
                .0
                .join("npm/node_modules/@anthropic-ai/claude-code/bin/claude.exe")
        )
    );
    // Pi's installer writes `%~dp0` without a backslash, which it ends in.
    let path = fixture.write(".pi/agent/bin/pi.cmd", PI_INSTALLER_CMD);
    assert_eq!(
        cmd_shim_target(&path),
        Some(fixture.0.join(".pi/agent/bin/pi-launcher.js"))
    );
    for invalid in [
        r#""%dp0%\..\@openai\codex\bin\codex.js" %*"#,
        r#""%dp0%\C:\other\codex.js" %*"#,
        r#""%~dp0..\codex.js" %*"#,
        "@echo off\ncodex.exe %*\n",
        // The shim's own directory is not a target.
        r#"cd /d "%~dp0" %*"#,
    ] {
        let path = fixture.write("npm/codex.cmd", invalid);
        assert_eq!(cmd_shim_target(&path), None);
    }
}

/// The pi.cmd that https://pi.dev/install.ps1 writes when it installed a
/// standalone Node.js.
const PI_INSTALLER_CMD: &str = "@ECHO off\r
SETLOCAL\r
SET \"node=node\"\r
IF EXIST \"%LOCALAPPDATA%\\pi-node\\current\\node.exe\" SET \"node=%LOCALAPPDATA%\\pi-node\\current\\node.exe\"\r
\"%node%\" \"%~dp0pi-launcher.js\" %*\r
SET \"pi_exit_code=%ERRORLEVEL%\"\r
IF \"%pi_exit_code%\"==\"9009\" (\r
  IF \"%node%\"==\"node\" (\r
    \"%SystemRoot%\\System32\\where.exe\" /Q node\r
    IF ERRORLEVEL 1 GOTO node_missing\r
  ) ELSE IF NOT EXIST \"%node%\" GOTO node_missing\r
)\r
EXIT /B %pi_exit_code%\r
:node_missing\r
ECHO Pi could not find Node.js. Install Node.js 22.19.0 or newer, or rerun the installer at https://pi.dev. 1>&2\r
EXIT /B %pi_exit_code%\r
";

#[cfg(unix)]
#[test]
fn runs_the_node_a_shim_names() {
    let fixture = Fixture::new();
    let environment = fixture.environment();
    let shim = fixture.write(".pi/agent/bin/pi.cmd", PI_INSTALLER_CMD);
    let local = fixture.0.join("local");
    let var = |name: &str| (name == "LOCALAPPDATA").then(|| local.clone().into_os_string());
    // Without its standalone Node.js, Pi's launcher runs `node` from PATH.
    assert_eq!(shim_node(&environment, &shim, var), None);
    let node = fixture.script("local/pi-node/current/node.exe", "");
    assert_eq!(shim_node(&environment, &shim, var).unwrap().path, node);

    // npm's shims name the node.exe beside them.
    let npm = fixture.write(
        "npm/codex.cmd",
        r#"IF EXIST "%dp0%\node.exe" (
  SET "_prog=%dp0%\node.exe"
)
"#,
    );
    assert_eq!(shim_node(&environment, &npm, |_| None), None);
    let sibling = fixture.script("npm/node.exe", "");
    assert_eq!(
        shim_node(&environment, &npm, |_| None).unwrap().path,
        sibling
    );
    let script = fixture
        .0
        .join("npm/node_modules/@openai/codex/bin/codex.js");
    assert_eq!(
        shim_command(&environment, &npm, &script).unwrap().program,
        sibling
    );
}

#[cfg(unix)]
#[test]
fn runs_cmd_shim_targets_without_cmd_exe() {
    let fixture = Fixture::new();
    let node = fixture.script("bin/node", "#!/bin/sh\n");
    let shim = fixture.0.join("npm/codex.cmd");
    let script = fixture
        .0
        .join("npm/node_modules/@openai/codex/bin/codex.js");
    let command = shim_command(&fixture.environment(), &shim, &script).unwrap();
    assert_eq!(command.program, node);
    assert_eq!(command.args, [script.into_os_string()]);
    let binary = fixture
        .0
        .join("npm/node_modules/@anthropic-ai/claude-code/bin/claude.exe");
    assert_eq!(
        shim_command(&fixture.environment(), &shim, &binary).unwrap(),
        CommandSpec::new(&binary, &[])
    );
    // A shim whose target could not be read is never passed to cmd.exe.
    assert_eq!(
        shim_command(&fixture.environment(), &shim, &shim)
            .unwrap_err()
            .code,
        "unsupported"
    );
}

#[cfg(windows)]
#[test]
fn follows_npm_shims_on_windows_without_verbatim_paths() {
    let fixture = Fixture::new();
    // std's canonical form is \\?\C:\..., which Node cannot load as a script.
    let root = dunce::canonicalize(&fixture.0).unwrap();
    let npm = root.join("npm");
    let nodejs = root.join("nodejs");
    for (name, entry) in [
        ("@earendil-works/pi-coding-agent", "dist/bundle/cli.js"),
        ("@anthropic-ai/claude-code", "bin/claude.exe"),
    ] {
        fixture.write(
            &format!("npm/node_modules/{name}/package.json"),
            &format!(r#"{{"name":"{name}"}}"#),
        );
        fixture.write(&format!("npm/node_modules/{name}/{entry}"), "");
    }
    fixture.write(
        "npm/pi.cmd",
        r#""%_prog%"  "%dp0%\node_modules\@earendil-works\pi-coding-agent\dist\bundle\cli.js" %*"#,
    );
    // npm's sh shim for Git Bash is the same installation.
    fixture.write("npm/pi", "#!/bin/sh\n");
    fixture.write(
        "npm/claude.cmd",
        r#""%dp0%\node_modules\@anthropic-ai\claude-code\bin\claude.exe"   %*"#,
    );
    fixture.write("npm/node.exe", "");
    fixture.write("nodejs/npm.cmd", "");
    fixture.write("nodejs/node_modules/npm/bin/npm-cli.js", "");
    let environment = Environment {
        home: root.clone(),
        path: std::env::join_paths([&npm, &nodejs]).unwrap(),
        use_system_proxy: false,
        proxy_env: None,
        wsl: None,
    };

    let pi = detect(&environment, ClientId::Pi).unwrap();
    assert_eq!(
        pi.method,
        InstallMethod::Npm {
            prefix: npm.clone()
        }
    );
    assert!(pi.others.is_empty());
    let command = client_command(&environment, &pi).unwrap();
    assert_eq!(command.program, npm.join("node.exe"));
    assert_eq!(
        command.args,
        [
            npm.join(r"node_modules\@earendil-works\pi-coding-agent\dist\bundle\cli.js")
                .into_os_string()
        ]
    );

    let claude = detect(&environment, ClientId::Claude).unwrap();
    assert_eq!(
        claude.method,
        InstallMethod::Npm {
            prefix: npm.clone()
        }
    );
    assert_eq!(
        client_command(&environment, &claude).unwrap(),
        CommandSpec::new(
            npm.join(r"node_modules\@anthropic-ai\claude-code\bin\claude.exe"),
            &[]
        )
    );
    // Node's own npm.cmd runs the npm-cli.js beside it.
    let command = update_command(
        &environment,
        ClientId::Claude,
        &claude,
        &version(ClientId::Claude, "2.1.286"),
    )
    .unwrap();
    assert_eq!(
        PathBuf::from(&command.args[0]),
        nodejs.join(r"node_modules\npm\bin\npm-cli.js")
    );
    assert_eq!(command.args[4], npm.as_os_str());
}

#[cfg(windows)]
#[test]
fn follows_the_pi_installer_launcher_on_windows() {
    let fixture = Fixture::new();
    let root = dunce::canonicalize(&fixture.0).unwrap();
    let bin = root.join(r".pi\agent\bin");
    let nodejs = root.join("nodejs");
    for (name, contents) in [
        ("pi.cmd", PI_INSTALLER_CMD),
        ("pi.ps1", ""),
        ("pi", "#!/bin/sh\n"),
        ("pi-launcher.js", ""),
    ] {
        fixture.write(&format!(".pi/agent/bin/{name}"), contents);
    }
    fixture.write(
        ".pi/agent/install/managed-install.json",
        "\u{feff}{\"kind\":\"pi-managed-install\",\"schemaVersion\":1,\"layout\":\"releases-v1\"}",
    );
    fixture.write("nodejs/node.exe", "");
    let environment = Environment {
        home: root.clone(),
        path: std::env::join_paths([&bin, &nodejs]).unwrap(),
        use_system_proxy: false,
        proxy_env: None,
        wsl: None,
    };

    let pi = detect(&environment, ClientId::Pi).unwrap();
    assert_eq!(pi.method, InstallMethod::Native);
    assert_eq!(pi.executable, bin.join("pi.cmd"));
    assert_eq!(pi.resolved, bin.join("pi-launcher.js"));
    assert!(pi.others.is_empty());
    let command = update_command(
        &environment,
        ClientId::Pi,
        &pi,
        &version(ClientId::Pi, "99.0.0"),
    )
    .unwrap();
    let node = shim_node(&environment, &pi.executable, |name| std::env::var_os(name))
        .map_or(nodejs.join("node.exe"), |node| node.path);
    assert_eq!(command.program, node);
    assert_eq!(
        command.args,
        [
            bin.join("pi-launcher.js").into_os_string(),
            "update".into(),
            "--self".into()
        ]
    );
}

#[test]
fn recognizes_windows_programs_by_their_pe_header() {
    let fixture = Fixture::new();
    // Claude Code's npm stub, left when its postinstall did not run.
    let stub = fixture.write(
        "npm/node_modules/@anthropic-ai/claude-code/bin/claude.exe",
        "echo \"Error: claude native binary not installed.\" >&2\nexit 1\n",
    );
    assert!(!process::windows_image(&stub).unwrap());
    let mut image = vec![0; 0x84];
    image[..2].copy_from_slice(b"MZ");
    image[60..64].copy_from_slice(&0x80u32.to_le_bytes());
    image[0x80..].copy_from_slice(b"PE\0\0");
    let path = fixture.0.join("program.exe");
    fs::write(&path, &image).unwrap();
    assert!(process::windows_image(&path).unwrap());
    let mut dos = image.clone();
    dos[0x80..].copy_from_slice(b"NE\0\0");
    // Empty, DOS header only, cut-off signature, and a 16-bit NE image.
    for bytes in [&[][..], &image[..64], &image[..0x82], &dos[..]] {
        fs::write(&path, bytes).unwrap();
        assert!(!process::windows_image(&path).unwrap());
    }
    assert!(process::windows_image(&fixture.0.join("missing.exe")).is_err());
}

#[cfg(windows)]
#[test]
fn never_starts_a_file_that_is_not_a_windows_program() {
    let fixture = Fixture::new();
    let environment = Environment {
        home: fixture.0.clone(),
        path: std::env::var_os("PATH").unwrap_or_default(),
        use_system_proxy: false,
        proxy_env: None,
        wsl: None,
    };
    let stub = fixture.write("claude.exe", "exit 1\n");
    // CreateProcess itself would fail with "command" after showing the dialog.
    let error = process::run(
        &environment,
        &CommandSpec::new(&stub, &["--version"]),
        PROBE_TIMEOUT,
    )
    .unwrap_err();
    assert_eq!(error.code, "damaged");
    assert!(process::windows_image(&std::env::current_exe().unwrap()).unwrap());
    let shell = std::env::var_os("ComSpec").unwrap();
    let output = process::run(
        &environment,
        &CommandSpec::new(shell, &["/d", "/c", "echo ok"]),
        PROBE_TIMEOUT,
    )
    .unwrap();
    assert_eq!(output.trim(), "ok");
}

#[test]
fn parses_latest_sources_and_rejects_invalid_versions() {
    assert_eq!(
        parse_latest(
            r#"{"version":"0.158.0"}"#,
            &InstallMethod::Native,
            ClientId::Codex
        )
        .unwrap(),
        version(ClientId::Codex, "0.158.0")
    );
    assert_eq!(
        parse_latest("2.1.283\n", &InstallMethod::Native, ClientId::Claude).unwrap(),
        version(ClientId::Claude, "2.1.283")
    );
    assert_eq!(
        parse_latest(
            r#"{"url":"https://downloads.cursor.com/x.tar.gz","version":"2026.09.28-64d2043"}"#,
            &InstallMethod::Native,
            ClientId::Cursor
        )
        .unwrap()
        .to_string(),
        "2026.09.28-64d2043"
    );
    let method = InstallMethod::Brew {
        root: "/opt/homebrew".into(),
        package: "codex".into(),
        cask: false,
    };
    assert_eq!(
        parse_latest(
            r#"{"versions":{"stable":"0.158.0"}}"#,
            &method,
            ClientId::Codex
        )
        .unwrap(),
        version(ClientId::Codex, "0.158.0")
    );
    for invalid in [
        r#"{"version":"$(touch evil)"}"#,
        r#"{"version":"1.0.0-beta.1"}"#,
        "<html>error</html>",
    ] {
        assert!(parse_latest(invalid, &InstallMethod::Unknown, ClientId::Codex).is_err());
    }
}

#[test]
fn claude_version_check_respects_the_selected_native_channel() {
    let fixture = Fixture::new();
    let install = Installation {
        executable: "claude".into(),
        resolved: "claude".into(),
        method: InstallMethod::Native,
        others: vec![],
    };
    assert!(
        latest_source(&fixture.environment(), ClientId::Claude, &install)
            .url
            .ends_with("/latest")
    );
    fixture.write(
        ".claude/settings.json",
        r#"{"autoUpdatesChannel":"stable"}"#,
    );
    assert!(
        latest_source(&fixture.environment(), ClientId::Claude, &install)
            .url
            .ends_with("/stable")
    );
}

#[test]
fn cursor_and_pi_version_checks_use_their_release_services() {
    let fixture = Fixture::new();
    let install = Installation {
        executable: "cursor-agent".into(),
        resolved: "cursor-agent".into(),
        method: InstallMethod::Native,
        others: vec![],
    };
    let pi = latest_source(&fixture.environment(), ClientId::Pi, &install);
    assert_eq!((pi.url.as_str(), pi.body), (PI_LATEST_URL, None));
    let cursor = latest_source(&fixture.environment(), ClientId::Cursor, &install);
    assert_eq!(cursor.url, CURSOR_LATEST_URL);
    assert_eq!(cursor.body, Some(serde_json::json!({ "channel": "prod" })));
    fixture.write(".cursor/cli-config.json", r#"{"channel":"lab"}"#);
    assert_eq!(
        latest_source(&fixture.environment(), ClientId::Cursor, &install).body,
        Some(serde_json::json!({ "channel": "lab" }))
    );
    let cask = Installation {
        method: InstallMethod::Brew {
            root: "/opt/homebrew".into(),
            package: "cursor-cli".into(),
            cask: true,
        },
        ..install
    };
    let source = latest_source(&fixture.environment(), ClientId::Cursor, &cask);
    assert_eq!(
        (source.url.as_str(), source.body),
        ("https://formulae.brew.sh/api/cask/cursor-cli.json", None)
    );
}

#[test]
fn serializes_operations_and_rejects_unknown_client_ids() {
    let manager = ClientUpdateManager::default();
    let guard = manager.operation.try_lock().unwrap();
    assert!(manager.operation.try_lock().is_err());
    drop(guard);
    assert!(manager.operation.try_lock().is_ok());
    assert!(serde_json::from_str::<ClientId>(r#""codex; rm -rf anything""#).is_err());
    assert_eq!(manager.snapshot().clients.len(), ClientId::ALL.len());
}

#[test]
fn reports_masked_curl_failures_as_network_errors_and_keeps_the_error_tail() {
    let output = format!(
        "{}Update ran successfully!\ncurl: (35) LibreSSL SSL_connect: SSL_ERROR_SYSCALL",
        "log\n".repeat(2000)
    );
    let error = ClientError::new("unchanged", output);
    assert_eq!(error.code, "network");
    assert!(error.detail.contains("SSL_ERROR_SYSCALL"));
    assert!(error.detail.chars().count() <= 4096);
    // npm on Windows cannot clean up a half-extracted package that
    // antivirus holds; the cause is the reset download.
    let error = ClientError::new(
        "command",
        "npm warn cleanup [Error: EPERM: operation not permitted, rmdir 'C:\\stage\\node_modules']
         npm error code ECONNRESET
npm error network aborted",
    );
    assert_eq!(error.code, "network");
}

#[test]
fn reports_cache_and_installation_permission_failures_with_the_original_details() {
    for detail in [
        "wsl: Failed to mount G:\\, see dmesg for more details.\n\
         npm error code EACCES\nnpm error syscall open\n\
         npm error path /home/ada/.npm/_cacache/tmp/file\n\
         npm error Your cache folder contains root-owned files",
        "npm ERR! code EACCES\nnpm ERR! syscall rename\n\
         npm ERR! path /usr/lib/node_modules/@openai/codex",
        "npm error code EPERM\nnpm error syscall unlink",
        "Permission denied (os error 13)",
        "Access is denied. (os error 5)",
        "Operation not permitted (os error 1)",
    ] {
        for code in ["command", "unchanged"] {
            let error = ClientError::new(code, detail);
            assert_eq!(error.code, "permission", "{detail}");
            assert_eq!(error.detail, detail);
        }
    }
    assert_eq!(
        ClientError::new("command", "wsl: Failed to mount G:\\").code,
        "command"
    );
    assert_eq!(ClientError::new("version", "EACCES").code, "version");
}

#[cfg(target_os = "macos")]
#[test]
#[ignore = "Read-only live connectivity check; requires system proxy and network access"]
fn installed_system_proxy_reaches_codex_download_without_installing() {
    let environment = load_environment(crate::control_session::user_home().unwrap(), true).unwrap();
    let result = process::run(
        &environment,
        &CommandSpec::new(
            "/usr/bin/curl",
            &[
                "-fsSL",
                "--max-time",
                "20",
                "--output",
                "/dev/null",
                "--write-out",
                "%{http_code}",
                "https://chatgpt.com/codex/install.sh",
            ],
        ),
        Duration::from_secs(25),
    )
    .unwrap();
    assert_eq!(result.trim(), "200");
}

#[cfg(unix)]
#[test]
fn respects_path_order_and_deduplicates_symlinks() {
    use std::os::unix::fs::symlink;
    let fixture = Fixture::new();
    let first = fixture.script("first/codex", "#!/bin/sh\necho 'codex-cli 1.0.0'\n");
    fixture.script("second/codex", "#!/bin/sh\necho 'codex-cli 2.0.0'\n");
    fs::create_dir_all(fixture.0.join("alias")).unwrap();
    symlink(&first, fixture.0.join("alias/codex")).unwrap();
    let path = std::env::join_paths([
        fixture.0.join("first"),
        fixture.0.join("alias"),
        fixture.0.join("second"),
    ])
    .unwrap();
    let paths = find_programs(&path, "codex")
        .into_iter()
        .map(|program| program.path)
        .collect::<Vec<_>>();
    assert_eq!(paths, [first, fixture.0.join("second/codex")]);
}

#[cfg(unix)]
fn fake_codex(fixture: &Fixture, update: &str) -> Installation {
    use std::os::unix::fs::symlink;
    fixture.write(
        "package/codex-package.json",
        r#"{"variant":"codex","layoutVersion":1,"entrypoint":"bin/codex"}"#,
    );
    let script = fixture.script(
        "package/bin/codex",
        &format!("#!/bin/sh\nif [ \"$1\" = '--version' ]; then cat version; else {update}; fi\n"),
    );
    fixture.write("version", "codex-cli 1.0.0\n");
    fs::create_dir_all(fixture.0.join("bin")).unwrap();
    symlink(script, fixture.0.join("bin/codex")).unwrap();
    detect(&fixture.environment(), ClientId::Codex).unwrap()
}

/// Stands in for wsl.exe: runs the command after `--exec` from the fixture,
/// which plays the distribution user's home.
#[cfg(unix)]
fn fake_wsl(fixture: &Fixture) -> Wsl {
    let home = fixture.0.display();
    let launcher = fixture.script(
        "wsl",
        &format!(
            "#!/bin/sh\ncd '{home}' || exit 1\nwhile [ \"$1\" != --exec ]; do shift; done\nshift\n\
             exec env SHELL=/bin/sh HOME='{home}' \"$@\"\n"
        ),
    );
    Wsl {
        distribution: "Ubuntu".into(),
        launcher,
        share: "/".into(),
        mount_root: fixture.0.join("mnt").display().to_string(),
    }
}

#[cfg(unix)]
#[test]
fn reads_the_home_and_login_path_in_wsl() {
    let fixture = Fixture::new();
    let environment = wsl::environment(fake_wsl(&fixture), false).unwrap();
    assert_eq!(environment.home, fixture.0);
    let path = environment.path.to_string_lossy();
    let local_bin = fixture.0.join(".local/bin");
    assert!(path.split(':').any(|d| Path::new(d) == local_bin), "{path}");
}

#[cfg(unix)]
#[test]
fn checks_and_updates_clients_in_wsl_apart_from_windows_drives() {
    let fixture = Fixture::new();
    let local = fake_codex(&fixture, "echo 'codex-cli 1.1.0' > version");
    // This computer's npm shim, which WSL appends to PATH.
    fixture.script("mnt/c/npm/codex", "#!/bin/sh\necho 'codex-cli 9.0.0'\n");
    let cursor = fixture.script("my tools/cursor-agent", "#!/bin/sh\n");
    let environment = Environment {
        path: std::env::join_paths([
            fixture.0.join("mnt/c/npm"),
            fixture.0.join("bin"),
            fixture.0.join("my tools"),
            PathBuf::from("/usr/bin"),
            PathBuf::from("/bin"),
        ])
        .unwrap(),
        wsl: Some(fake_wsl(&fixture)),
        ..fixture.environment()
    };
    let install = detect(&environment, ClientId::Codex).unwrap();
    assert_eq!(install.executable, fixture.0.join("bin/codex"));
    assert_eq!(install.resolved, local.resolved);
    assert_eq!(install.method, InstallMethod::Native);
    assert!(install.others.is_empty());
    assert_eq!(
        environment
            .programs("cursor-agent")
            .into_iter()
            .map(|program| program.path)
            .collect::<Vec<_>>(),
        [cursor]
    );
    assert!(environment.executable(&fixture.0.join("bin/codex")));
    assert!(!environment.executable(&fixture.0.join("version")));
    assert!(environment.is_file(&fixture.0.join("version")));
    let result = perform_update(
        &environment,
        ClientId::Codex,
        &install,
        &version(ClientId::Codex, "1.1.0"),
    )
    .unwrap();
    assert_eq!(result, version(ClientId::Codex, "1.1.0"));
}

#[cfg(unix)]
#[test]
fn updates_an_isolated_native_client_and_checks_the_result() {
    let fixture = Fixture::new();
    let install = fake_codex(&fixture, "echo 'codex-cli 1.1.0' > version");
    assert_eq!(install.method, InstallMethod::Native);
    let result = perform_update(
        &fixture.environment(),
        ClientId::Codex,
        &install,
        &version(ClientId::Codex, "1.1.0"),
    )
    .unwrap();
    assert_eq!(result, version(ClientId::Codex, "1.1.0"));
    // Never downgrade even if the currently running check has stale metadata.
    let result = perform_update(
        &fixture.environment(),
        ClientId::Codex,
        &install,
        &version(ClientId::Codex, "1.0.0"),
    )
    .unwrap();
    assert_eq!(result, version(ClientId::Codex, "1.1.0"));
}

#[cfg(unix)]
#[test]
fn reports_update_failure_and_success_without_version_change() {
    for (script, code) in [
        ("echo permission-denied >&2; exit 1", "command"),
        ("echo download-failed >&2; exit 0", "unchanged"),
    ] {
        let fixture = Fixture::new();
        let install = fake_codex(&fixture, script);
        let error = perform_update(
            &fixture.environment(),
            ClientId::Codex,
            &install,
            &version(ClientId::Codex, "1.1.0"),
        )
        .unwrap_err();
        assert_eq!(error.code, code);
        if code == "unchanged" {
            assert!(error.detail.contains("download-failed"));
        }
        assert_eq!(
            current_version(&fixture.environment(), ClientId::Codex, &install).unwrap(),
            version(ClientId::Codex, "1.0.0")
        );
    }
}

#[cfg(unix)]
#[test]
fn refuses_to_update_when_the_selected_installation_changes() {
    let fixture = Fixture::new();
    let install = fake_codex(&fixture, "echo should-not-run > marker");
    fs::remove_file(&install.executable).unwrap();
    fixture.script("bin/codex", "#!/bin/sh\necho 'codex-cli 1.0.0'\n");
    assert_eq!(
        perform_update(
            &fixture.environment(),
            ClientId::Codex,
            &install,
            &version(ClientId::Codex, "1.1.0")
        )
        .unwrap_err()
        .code,
        "changed"
    );
    assert!(!fixture.0.join("marker").exists());
}

#[cfg(unix)]
#[test]
fn npm_update_targets_the_detected_prefix_and_allows_the_package_scripts() {
    let fixture = Fixture::new();
    fixture.script("node/bin/node", "#!/bin/sh\nexit 0\n");
    let npm = fixture.write("node/lib/node_modules/npm/bin/npm-cli.js", "");
    let prefix = fixture.0.join("node");
    for (id, latest, package) in [
        (ClientId::Codex, "0.158.0", "@openai/codex"),
        (ClientId::Claude, "2.1.299", "@anthropic-ai/claude-code"),
    ] {
        let install = Installation {
            executable: prefix.join("bin").join(id.name()),
            resolved: PathBuf::new(),
            method: InstallMethod::Npm {
                prefix: prefix.clone(),
            },
            others: vec![],
        };
        let command =
            update_command(&fixture.environment(), id, &install, &version(id, latest)).unwrap();
        assert_eq!(command.program, prefix.join("bin/node"));
        assert_eq!(
            command.args,
            [
                npm.clone().into_os_string(),
                "install".into(),
                "--global".into(),
                "--prefix".into(),
                prefix.clone().into_os_string(),
                "--cache".into(),
                fixture.0.join(".cache/astrlink/npm").into_os_string(),
                format!("--allow-scripts={package}").into(),
                format!("{package}@{latest}").into(),
            ]
        );
    }
    // Staged Pi gets the options its own updater passes to npm, and the
    // installation's global npmrc.
    let stage = fixture.0.join("stage");
    let command = npm_command(
        &fixture.environment(),
        ClientId::Pi,
        &prefix,
        &stage,
        &version(ClientId::Pi, "1.5.2"),
    )
    .unwrap();
    assert_eq!(
        command.args[3..],
        [
            "--prefix".into(),
            stage.into_os_string(),
            "--globalconfig".into(),
            prefix.join("etc/npmrc").into_os_string(),
            "--cache".into(),
            fixture.0.join(".cache/astrlink/npm").into_os_string(),
            "--ignore-scripts".into(),
            "--min-release-age=0".into(),
            "@earendil-works/pi-coding-agent@1.5.2".into(),
        ]
    );
}

#[cfg(unix)]
#[test]
fn npm_updates_use_a_separate_cache_locally_and_in_wsl() {
    use std::os::unix::fs::{symlink, PermissionsExt};
    for in_wsl in [false, true] {
        let fixture = Fixture::new();
        // Include spaces to exercise argument passing through the WSL launcher.
        let home = fixture.0.join("user home");
        let prefix = home.join("node");
        let cache = home.join(".npm");
        fs::create_dir_all(&cache).unwrap();
        fs::set_permissions(&cache, fs::Permissions::from_mode(0o555)).unwrap();
        fixture.write(
            "user home/node/lib/node_modules/@openai/codex/package.json",
            r#"{"name":"@openai/codex"}"#,
        );
        let codex = fixture.script(
            "user home/node/lib/node_modules/@openai/codex/bin/codex.js",
            "#!/bin/sh\ncat version\n",
        );
        fixture.write("user home/version", "codex-cli 1.0.0\n");
        fixture.script(
            "user home/node/bin/node",
            "#!/bin/sh\nexec /bin/sh \"$@\"\n",
        );
        symlink(codex, prefix.join("bin/codex")).unwrap();
        // Stand in for npm: reject the default cache, otherwise write download
        // data to the requested cache before updating the installed version.
        fixture.write(
            "user home/node/lib/node_modules/npm/bin/npm-cli.js",
            "#!/bin/sh\nset -eu\ncache=.npm\n\
             while [ $# -gt 0 ]; do\n\
               if [ \"$1\" = --cache ]; then shift; cache=$1; fi\n\
               shift\n\
             done\n\
             if [ \"$cache\" = .npm ]; then echo 'npm error code EACCES' >&2; exit 1; fi\n\
             mkdir -p \"$cache\"\n\
             echo download > \"$cache/download\"\n\
             echo 'codex-cli 1.1.0' > version\n",
        );
        let mut environment = Environment {
            home: home.clone(),
            path: std::env::join_paths([
                prefix.join("bin"),
                PathBuf::from("/usr/bin"),
                PathBuf::from("/bin"),
            ])
            .unwrap(),
            ..fixture.environment()
        };
        if in_wsl {
            let mut wsl = fake_wsl(&fixture);
            wsl.launcher = fixture.script(
                "wsl-with-home",
                &format!(
                    "#!/bin/sh\ncd '{}' || exit 1\n\
                     while [ \"$1\" != --exec ]; do shift; done\nshift\nexec \"$@\"\n",
                    home.display()
                ),
            );
            environment.wsl = Some(wsl);
        }
        let install = detect(&environment, ClientId::Codex).unwrap();
        let result = perform_update(
            &environment,
            ClientId::Codex,
            &install,
            &version(ClientId::Codex, "1.1.0"),
        );
        // Restore the fixture's permissions even if the update failed.
        let untouched = fs::read_dir(&cache).unwrap().next().is_none();
        fs::set_permissions(&cache, fs::Permissions::from_mode(0o755)).unwrap();
        assert_eq!(result.unwrap(), version(ClientId::Codex, "1.1.0"));
        assert!(untouched);
        assert_eq!(
            fs::read_to_string(home.join(".cache/astrlink/npm/download")).unwrap(),
            "download\n"
        );
    }
}

#[cfg(unix)]
#[test]
fn pi_delegates_updates_and_cursor_respects_the_static_channel() {
    let fixture = Fixture::new();
    for method in [InstallMethod::Native, InstallMethod::Package("pnpm")] {
        let install = Installation {
            executable: fixture.0.join("bin/pi"),
            resolved: PathBuf::new(),
            method,
            others: vec![],
        };
        let command = update_command(
            &fixture.environment(),
            ClientId::Pi,
            &install,
            &version(ClientId::Pi, "0.87.1"),
        )
        .unwrap();
        assert_eq!(command.program, install.executable);
        assert_eq!(command.args, ["update", "--self"]);
    }
    let cursor = Installation {
        executable: fixture.0.join("bin/cursor-agent"),
        resolved: PathBuf::new(),
        method: InstallMethod::Native,
        others: vec![],
    };
    let latest = version(ClientId::Cursor, "2026.09.28-64d2043");
    let command =
        update_command(&fixture.environment(), ClientId::Cursor, &cursor, &latest).unwrap();
    assert_eq!(command.args, ["update"]);
    fixture.write(".cursor/cli-config.json", r#"{"channel":"static"}"#);
    assert_eq!(
        update_command(&fixture.environment(), ClientId::Cursor, &cursor, &latest)
            .unwrap_err()
            .code,
        "unsupported"
    );
}

#[cfg(unix)]
#[test]
fn pi_npm_updates_run_under_node_when_bun_answers_to_node() {
    let fixture = Fixture::new();
    let bun = fixture.script(".bun/bin/bun", "#!/bin/sh\n");
    for directory in ["bin", "prefix/bin"] {
        fs::create_dir_all(fixture.0.join(directory)).unwrap();
    }
    std::os::unix::fs::symlink(&bun, fixture.0.join("bin/node")).unwrap();
    let entry = fixture.write(
        "prefix/lib/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js",
        "",
    );
    let pi = Installation {
        executable: fixture.0.join("prefix/bin/pi"),
        resolved: entry.clone(),
        method: InstallMethod::Npm {
            prefix: fixture.0.join("prefix"),
        },
        others: vec![],
    };
    let latest = version(ClientId::Pi, "1.2.0");
    let mut environment = fixture.environment();
    environment.path = fixture.0.join("bin").into();
    assert_eq!(
        update_command(&environment, ClientId::Pi, &pi, &latest)
            .unwrap_err()
            .code,
        "unsupported"
    );
    // npm itself runs under Bun when nothing else answers to `node`.
    fixture.write("prefix/lib/node_modules/npm/bin/npm-cli.js", "");
    let codex = Installation {
        executable: fixture.0.join("prefix/bin/codex"),
        resolved: PathBuf::new(),
        method: pi.method.clone(),
        others: vec![],
    };
    let command = update_command(
        &environment,
        ClientId::Codex,
        &codex,
        &version(ClientId::Codex, "0.158.0"),
    )
    .unwrap();
    assert_eq!(command.program, fixture.0.join("bin/node"));

    let node = fixture.script("node/bin/node", "#!/bin/sh\n");
    environment.path =
        std::env::join_paths([fixture.0.join("bin"), fixture.0.join("node/bin")]).unwrap();
    let command = update_command(&environment, ClientId::Pi, &pi, &latest).unwrap();
    assert_eq!(command.program, node);
    assert_eq!(
        command.args,
        [entry.into_os_string(), "update".into(), "--self".into()]
    );
    // Node.js beside the prefix comes before PATH, unless it is Bun.
    std::os::unix::fs::symlink(&bun, fixture.0.join("prefix/bin/node")).unwrap();
    let command = update_command(&environment, ClientId::Pi, &pi, &latest).unwrap();
    assert_eq!(command.program, node);
    fs::remove_file(fixture.0.join("prefix/bin/node")).unwrap();
    let sibling = fixture.script("prefix/bin/node", "#!/bin/sh\n");
    let command = update_command(&environment, ClientId::Pi, &pi, &latest).unwrap();
    assert_eq!(command.program, sibling);
}

#[tokio::test]
async fn handles_http_failure_rate_limit_and_invalid_metadata() {
    for (status, body, code) in [
        ("429 Too Many Requests", "", "rate_limit"),
        ("503 Service Unavailable", "", "network"),
        ("200 OK", "not-json", "version"),
    ] {
        let server = TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}/latest", server.local_addr().unwrap());
        let body = body.to_string();
        let worker = std::thread::spawn(move || {
            let (mut stream, _) = server.accept().unwrap();
            let mut request = [0; 2048];
            let _ = stream.read(&mut request);
            write!(
                stream,
                "HTTP/1.1 {status}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                body.len()
            )
            .unwrap();
        });
        let client = reqwest::Client::builder().no_proxy().build().unwrap();
        let install = Installation {
            executable: "codex".into(),
            resolved: "codex".into(),
            method: InstallMethod::Native,
            others: vec![],
        };
        let source = LatestSource { url, body: None };
        assert_eq!(
            fetch_latest(&client, &source, &install, ClientId::Codex)
                .await
                .unwrap_err()
                .code,
            code
        );
        worker.join().unwrap();
    }
}

#[tokio::test]
async fn posts_the_cursor_channel_to_the_release_service() {
    let server = TcpListener::bind("127.0.0.1:0").unwrap();
    let url = format!("http://{}/latest", server.local_addr().unwrap());
    let worker = std::thread::spawn(move || {
        let (mut stream, _) = server.accept().unwrap();
        let mut request = Vec::new();
        let mut chunk = [0; 2048];
        while !String::from_utf8_lossy(&request).contains(r#"{"channel":"prod"}"#) {
            let read = stream.read(&mut chunk).unwrap();
            assert_ne!(read, 0, "request ended before the body");
            request.extend_from_slice(&chunk[..read]);
        }
        let body = r#"{"url":"https://downloads.cursor.com/x","version":"2026.09.28-64d2043"}"#;
        write!(
            stream,
            "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
            body.len()
        )
        .unwrap();
        String::from_utf8_lossy(&request).to_lowercase()
    });
    let client = reqwest::Client::builder().no_proxy().build().unwrap();
    let install = Installation {
        executable: "cursor-agent".into(),
        resolved: "cursor-agent".into(),
        method: InstallMethod::Native,
        others: vec![],
    };
    let source = LatestSource {
        url,
        body: Some(serde_json::json!({ "channel": "prod" })),
    };
    let latest = fetch_latest(&client, &source, &install, ClientId::Cursor)
        .await
        .unwrap();
    assert_eq!(latest.to_string(), "2026.09.28-64d2043");
    let request = worker.join().unwrap();
    assert!(request.starts_with("post /latest "));
    assert!(request.contains("connect-protocol-version: 1\r\n"));
}

/// Fixtures whose home turns on the Windows update guard (`guarded`).
pub(super) static GUARDED: std::sync::Mutex<Vec<PathBuf>> = std::sync::Mutex::new(Vec::new());

/// An npm-installed Claude Code in `<home>/node` and a stand-in for npm that
/// works like npm's reify: it renames the live package and shim aside
/// first, then "downloads" (which `$NPM_MODE` can make fail, stop, or skip
/// the platform binary), writes the new files and finally deletes what it
/// retired.
#[cfg(unix)]
struct NpmFixture {
    fixture: Fixture,
    prefix: PathBuf,
}

#[cfg(unix)]
impl NpmFixture {
    fn new(mode: &str) -> Self {
        let fixture = Fixture::new();
        GUARDED.lock().unwrap().push(fixture.0.clone());
        let prefix = dunce::canonicalize(&fixture.0).unwrap().join("node");
        fixture.script("node/bin/node", "#!/bin/sh\nexec /bin/sh \"$@\"\n");
        let this = Self { fixture, prefix };
        this.install_live("2.1.321");
        // Arguments: npm-cli.js install --global --prefix <target> ... <pkg>@<v>
        this.fixture.write(
            "node/lib/node_modules/npm/bin/npm-cli.js",
            &format!(
                "#!/bin/sh\nset -eu\nmode='{mode}'\nshift 3\ntarget=$1\n\
                 for spec; do :; done\nversion=${{spec##*@}}\n\
                 pkg=$target/lib/node_modules/@anthropic-ai/claude-code\n\
                 shim=$target/bin/claude\n\
                 [ -e \"$pkg\" ] && mv \"$pkg\" \"$target/lib/node_modules/@anthropic-ai/.claude-code-AbCd1234\"\n\
                 {{ [ -e \"$shim\" ] || [ -L \"$shim\" ]; }} && mv \"$shim\" \"$target/bin/.claude-EfGh5678\"\n\
                 echo \"$target\" >> npm-targets\n\
                 case $mode in\n\
                   killed) kill -9 $$ ;;\n\
                   fail) echo 'npm error code ECONNRESET' >&2; exit 1 ;;\n\
                 esac\n\
                 mkdir -p \"$pkg/bin\" \"$target/bin\"\n\
                 printf '{{\"name\":\"@anthropic-ai/claude-code\",\"version\":\"%s\",\"bin\":{{\"claude\":\"bin/claude.exe\"}}}}' \"$version\" > \"$pkg/package.json\"\n\
                 if [ \"$mode\" = stub ]; then\n\
                   printf '#!/bin/sh\\necho \"Error: claude native binary not installed.\" >&2\\nexit 1\\n' > \"$pkg/bin/claude.exe\"\n\
                 else\n\
                   printf '#!/bin/sh\\necho \"%s (Claude Code)\"\\n' \"$version\" > \"$pkg/bin/claude.exe\"\n\
                 fi\n\
                 chmod +x \"$pkg/bin/claude.exe\"\n\
                 ln -s ../lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe \"$shim\"\n\
                 rm -rf \"$target/lib/node_modules/@anthropic-ai/.claude-code-AbCd1234\" \"$target/bin/.claude-EfGh5678\"\n"
            ),
        );
        this
    }
    fn install_live(&self, version: &str) {
        let package = "node/lib/node_modules/@anthropic-ai/claude-code";
        self.fixture.write(
            &format!("{package}/package.json"),
            &format!(
                r#"{{"name":"@anthropic-ai/claude-code","version":"{version}","bin":{{"claude":"bin/claude.exe"}}}}"#
            ),
        );
        let binary = self.fixture.script(
            &format!("{package}/bin/claude.exe"),
            &format!("#!/bin/sh\necho '{version} (Claude Code)'\n"),
        );
        let shim = self.prefix.join("bin/claude");
        let _ = fs::remove_file(&shim);
        std::os::unix::fs::symlink(binary, shim).unwrap();
    }
    fn environment(&self) -> Environment {
        Environment {
            path: std::env::join_paths([
                self.prefix.join("bin"),
                PathBuf::from("/usr/bin"),
                PathBuf::from("/bin"),
            ])
            .unwrap(),
            ..self.fixture.environment()
        }
    }
    fn installed(&self) -> Option<String> {
        let environment = self.environment();
        let install = detect(&environment, ClientId::Claude)?;
        current_version(&environment, ClientId::Claude, &install)
            .ok()
            .map(|v| v.to_string())
    }
    fn update(&self) -> Result<ClientVersion, ClientError> {
        let environment = self.environment();
        let install = detect(&environment, ClientId::Claude).unwrap();
        perform_update(
            &environment,
            ClientId::Claude,
            &install,
            &version(ClientId::Claude, "2.1.323"),
        )
    }
    /// What the old updater did: npm straight into the live prefix.
    fn update_in_place(&self, mode: &str) {
        let _ = mode;
        let environment = self.environment();
        let install = detect(&environment, ClientId::Claude).unwrap();
        let command = update_command(
            &environment,
            ClientId::Claude,
            &install,
            &version(ClientId::Claude, "2.1.323"),
        )
        .unwrap();
        let _ = process::run(&environment, &command, Duration::from_secs(10));
    }
    fn leftovers(&self) -> Vec<String> {
        let mut found = Vec::new();
        for directory in ["bin", "lib/node_modules/@anthropic-ai"] {
            for entry in fs::read_dir(self.prefix.join(directory)).unwrap().flatten() {
                let name = entry.file_name().to_string_lossy().into_owned();
                if name.starts_with('.') {
                    found.push(format!("{directory}/{name}"));
                }
            }
        }
        if self.prefix.join(".astrlink-update").exists() {
            found.push(".astrlink-update".into());
        }
        found
    }
}

#[cfg(unix)]
impl Drop for NpmFixture {
    fn drop(&mut self) {
        GUARDED.lock().unwrap().retain(|p| *p != self.fixture.0);
    }
}

#[cfg(unix)]
#[test]
fn a_stopped_or_failed_npm_update_keeps_the_installed_client() {
    for (mode, code) in [
        // npm stopped midway: the update deadline or quitting AstrLink.
        ("killed", "command"),
        ("fail", "network"),
        // The platform binary did not download; npm finishes anyway.
        ("stub", "incomplete"),
    ] {
        let npm = NpmFixture::new(mode);
        let error = npm.update().unwrap_err();
        assert_eq!(error.code, code, "{mode}: {}", error.detail);
        assert_eq!(npm.installed().as_deref(), Some("2.1.321"), "{mode}");
        assert_eq!(npm.leftovers(), Vec::<String>::new(), "{mode}");
        // npm never ran in the live prefix.
        let targets = fs::read_to_string(npm.fixture.0.join("npm-targets")).unwrap();
        assert!(
            targets.lines().all(|t| Path::new(t) != npm.prefix),
            "{targets}"
        );
    }
}

#[cfg(unix)]
#[test]
fn a_verified_npm_update_replaces_the_installed_client() {
    let npm = NpmFixture::new("ok");
    assert_eq!(npm.update().unwrap(), version(ClientId::Claude, "2.1.323"));
    assert_eq!(npm.installed().as_deref(), Some("2.1.323"));
    assert_eq!(npm.leftovers(), Vec::<String>::new());
    let install = detect(&npm.environment(), ClientId::Claude).unwrap();
    assert_eq!(install.executable, npm.prefix.join("bin/claude"));
    assert_eq!(
        install.method,
        InstallMethod::Npm {
            prefix: npm.prefix.clone()
        }
    );
}

/// A client for version checks that fails at once instead of reaching the
/// release services.
#[cfg(unix)]
fn offline() -> reqwest::Client {
    reqwest::Client::builder()
        .proxy(reqwest::Proxy::all("http://127.0.0.1:9").unwrap())
        .build()
        .unwrap()
}

#[cfg(unix)]
#[tokio::test]
async fn reports_and_repairs_a_client_npm_left_broken() {
    // The old updater ran npm in the live prefix: stopped midway it leaves
    // only npm's retired copy; with a missing download, only a stub.
    for (mode, repair, previous) in [
        ("killed", "restore", Some("2.1.321")),
        ("stub", "reinstall", None),
    ] {
        let npm = NpmFixture::new(mode);
        npm.update_in_place(mode);
        assert_eq!(npm.installed(), None, "{mode}");
        let environment = npm.environment();
        let (status, _) = inspect(&environment, &offline(), ClientId::Claude).await;
        assert_eq!(status.phase, "damaged", "{mode}: {status:?}");
        assert_eq!(status.repair, Some(repair), "{mode}");
        assert_eq!(status.previous_version.as_deref(), previous, "{mode}");
        assert_eq!(status.install_method, "npm");

        // The repair itself must not fail the same way.
        let npm_cli = npm.prefix.join("lib/node_modules/npm/bin/npm-cli.js");
        let script = fs::read_to_string(&npm_cli).unwrap();
        fs::write(
            &npm_cli,
            script.replacen(&format!("mode='{mode}'"), "mode='ok'", 1),
        )
        .unwrap();
        let repaired = perform_repair(
            &environment,
            ClientId::Claude,
            Some(&version(ClientId::Claude, "2.1.323")),
        )
        .unwrap();
        let expected = previous.unwrap_or("2.1.323");
        assert_eq!(repaired.to_string(), expected, "{mode}");
        assert_eq!(npm.installed().as_deref(), Some(expected), "{mode}");
        assert_eq!(npm.leftovers(), Vec::<String>::new(), "{mode}");
    }
}

#[cfg(unix)]
#[tokio::test]
async fn undoes_an_interrupted_swap_before_the_next_check() {
    let npm = NpmFixture::new("ok");
    // AstrLink quit after moving the live client aside, before moving the
    // new one in.
    let run = npm.prefix.join(".astrlink-update/claude/1");
    let saved = run.join("previous");
    fs::create_dir_all(&saved).unwrap();
    let package = npm
        .prefix
        .join("lib/node_modules/@anthropic-ai/claude-code");
    let shim = npm.prefix.join("bin/claude");
    fs::rename(&package, saved.join("package-0")).unwrap();
    fs::rename(&shim, saved.join("claude")).unwrap();
    fs::write(
        npm.prefix.join(".astrlink-update/claude/journal.json"),
        serde_json::json!({
            "run": run,
            "retired": [[package, saved.join("package-0")], [shim, saved.join("claude")]],
            "placed": [[run.join("stage/pkg"), package], [run.join("stage/claude"), shim]],
        })
        .to_string(),
    )
    .unwrap();
    assert_eq!(npm.installed(), None);
    let (status, _) = inspect(&npm.environment(), &offline(), ClientId::Claude).await;
    assert_eq!(status.current_version.as_deref(), Some("2.1.321"));
    assert_eq!(npm.installed().as_deref(), Some("2.1.321"));
    // The run AstrLink left behind goes too.
    assert_eq!(npm.leftovers(), Vec::<String>::new());
}

#[cfg(unix)]
#[test]
fn leaves_other_systems_on_the_previous_update_path() {
    let npm = NpmFixture::new("ok");
    GUARDED.lock().unwrap().retain(|p| *p != npm.fixture.0);
    npm.update().unwrap();
    // npm ran in the live prefix, as before.
    let targets = fs::read_to_string(npm.fixture.0.join("npm-targets")).unwrap();
    assert_eq!(targets.trim(), npm.prefix.to_string_lossy());
}

#[cfg(unix)]
#[test]
fn a_slow_first_start_is_not_damage() {
    let npm = NpmFixture::new("ok");
    let environment = npm.environment();
    let damage = |error: ClientError| {
        staged::damage(&environment, ClientId::Claude, &npm.prefix, Some(&error))
    };
    // Antivirus can hold a fresh program past the version probe's deadline;
    // an old Node.js fails a script client the same before and after a
    // reinstall.
    assert!(damage(ClientError::new("timeout", "Command timed out")).is_none());
    assert!(damage(ClientError::new(
        "command",
        "SyntaxError: Unexpected token '?'\n    at wrapSafe (node:internal/modules/cjs/loader:1001:15)"
    ))
    .is_none());
    let found = damage(ClientError::new(
        "command",
        "Error: claude native binary not installed.",
    ))
    .unwrap();
    assert!(found.detail.contains("not installed"), "{}", found.detail);

    // Uninstalled, with only the copy npm once could not delete because the
    // client was running: not an installation.
    let package = npm.prefix.join("lib/node_modules/@anthropic-ai");
    fs::rename(
        package.join("claude-code"),
        package.join(".claude-code-AbCd1234"),
    )
    .unwrap();
    fs::remove_file(npm.prefix.join("bin/claude")).unwrap();
    assert!(staged::find_damage(&environment, ClientId::Claude).is_none());
}

/// nvm-windows puts a link to the active Node.js folder on PATH; detection
/// resolves it, and so must a repair.
#[cfg(unix)]
#[test]
fn repairs_a_client_found_through_a_linked_folder_on_path() {
    for (mode, expected) in [("killed", "2.1.321"), ("stub", "2.1.323")] {
        let npm = NpmFixture::new(mode);
        npm.update_in_place(mode);
        let npm_cli = npm.prefix.join("lib/node_modules/npm/bin/npm-cli.js");
        let script = fs::read_to_string(&npm_cli).unwrap();
        fs::write(
            &npm_cli,
            script.replacen(&format!("mode='{mode}'"), "mode='ok'", 1),
        )
        .unwrap();
        let link = npm.fixture.0.join("current");
        std::os::unix::fs::symlink(&npm.prefix, &link).unwrap();
        let environment = Environment {
            path: std::env::join_paths([link.join("bin"), "/usr/bin".into(), "/bin".into()])
                .unwrap(),
            ..npm.environment()
        };
        let repaired = perform_repair(
            &environment,
            ClientId::Claude,
            Some(&version(ClientId::Claude, "2.1.323")),
        )
        .unwrap();
        assert_eq!(repaired.to_string(), expected, "{mode}");
        assert_eq!(npm.installed().as_deref(), Some(expected), "{mode}");
    }
}
