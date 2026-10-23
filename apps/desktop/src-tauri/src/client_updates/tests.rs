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
    for invalid in [
        r#""%dp0%\..\@openai\codex\bin\codex.js" %*"#,
        r#""%dp0%\C:\other\codex.js" %*"#,
        "@echo off\ncodex.exe %*\n",
    ] {
        let path = fixture.write("npm/codex.cmd", invalid);
        assert_eq!(cmd_shim_target(&path), None);
    }
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
