//! Opens a terminal in which Claude Code or Codex reaches the gateway through
//! one API provider. The client starts with settings for that session only, so
//! the user's own client configs stay as they are and other terminals keep
//! routing as usual.

use std::{
    fs,
    io::Write,
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::Arc,
};

use serde::{Deserialize, Serialize};
use tauri::State;
use tauri_plugin_dialog::DialogExt;

use crate::{
    agent_home::Home, client_config, control_session, i18n, preferences::PreferencesStore,
    sidecar::CoreManager,
};

/// The header Core routes a request to one provider by: `ingress.ServiceHeader`.
const SERVICE_HEADER: &str = "X-AstrLink-Service";
/// Codex reads this session's connection from a provider of its own, so the
/// token `config.toml` holds for the usual `astrlink` provider cannot win.
const CODEX_PROVIDER: &str = "astrlink-terminal";
/// Codex reads the token from this variable, which keeps it off the command
/// line. Codex leaves variables named like tokens out of the commands it runs.
const CODEX_TOKEN_VARIABLE: &str = "ASTRLINK_TERMINAL_TOKEN";
/// Codex shows its own image generation and web search tools only for a
/// provider that sends this header; the gateway answers both tools.
const CODEX_TOOLS_HEADER: (&str, &str) = ("x-openai-actor-authorization", "codex-imagegen");
/// Settings that would send Claude Code elsewhere or under another login. A
/// blank value counts as unset.
const CLAUDE_CLEARED: [&str; 5] = [
    "ANTHROPIC_API_KEY",
    "CLAUDE_CODE_OAUTH_TOKEN",
    "CLAUDE_CODE_USE_BEDROCK",
    "CLAUDE_CODE_USE_VERTEX",
    "CLAUDE_CODE_USE_FOUNDRY",
];
/// Claude Code's model variables. A chosen model fills every one, so
/// background tasks stay on the provider too; blanks fall back to Claude
/// Code's own models.
const CLAUDE_MODELS: [&str; 6] = [
    "ANTHROPIC_MODEL",
    "ANTHROPIC_DEFAULT_OPUS_MODEL",
    "ANTHROPIC_DEFAULT_SONNET_MODEL",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL",
    "ANTHROPIC_DEFAULT_FABLE_MODEL",
    "ANTHROPIC_SMALL_FAST_MODEL",
];

#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum TerminalClient {
    Claude,
    Codex,
}

impl TerminalClient {
    fn config(self) -> client_config::Client {
        match self {
            Self::Claude => client_config::clients::claude::CLIENT,
            Self::Codex => client_config::clients::codex::CLIENT,
        }
    }

    fn label(self) -> &'static str {
        match self {
            Self::Claude => "Claude Code",
            Self::Codex => "Codex",
        }
    }
}

/// A terminal app a session can open in.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum TerminalApp {
    Terminal,
    Iterm2,
    Warp,
    Ghostty,
    Wezterm,
    Kitty,
    Alacritty,
    GnomeTerminal,
    Konsole,
    Xfce4Terminal,
    XTerminalEmulator,
    WindowsTerminal,
    Powershell,
}

impl TerminalApp {
    pub fn name(self) -> &'static str {
        match self {
            Self::Terminal => "Terminal",
            Self::Iterm2 => "iTerm2",
            Self::Warp => "Warp",
            Self::Ghostty => "Ghostty",
            Self::Wezterm => "WezTerm",
            Self::Kitty => "kitty",
            Self::Alacritty => "Alacritty",
            Self::GnomeTerminal => "GNOME Terminal",
            Self::Konsole => "Konsole",
            Self::Xfce4Terminal => "Xfce Terminal",
            Self::XTerminalEmulator => "x-terminal-emulator",
            Self::WindowsTerminal => "Windows Terminal",
            Self::Powershell => "PowerShell",
        }
    }
}

#[derive(Debug, Serialize, PartialEq, Eq)]
pub struct TerminalChoice {
    id: TerminalApp,
    name: &'static str,
}

/// One session's connection and the text the terminal shows around it.
pub struct Session<'a> {
    pub client: TerminalClient,
    /// The gateway's root, such as `http://127.0.0.1:18317`.
    pub origin: &'a str,
    pub token: &'a str,
    pub service_id: &'a str,
    pub model: Option<&'a str>,
    pub directory: &'a Path,
    pub banner: &'a str,
    pub exited: &'a str,
}

/// POSIX single quotes; a quote inside closes, escapes and reopens them.
#[cfg(any(unix, test))]
fn sh_quote(value: &str) -> String {
    format!("'{}'", value.replace('\'', r"'\''"))
}

/// PowerShell single quotes, which double a quote inside. PowerShell reads
/// the typographic single quotes as quotes too, such as one in "Bob’s".
#[cfg(any(windows, test))]
fn ps_quote(value: &str) -> String {
    let mut quoted = String::from("'");
    for c in value.chars() {
        if matches!(c, '\'' | '\u{2018}' | '\u{2019}' | '\u{201A}' | '\u{201B}') {
            quoted.push(c);
        }
        quoted.push(c);
    }
    quoted.push('\'');
    quoted
}

/// Text shown in the terminal. A provider name is the user's, and control
/// characters in it could drive the terminal, such as writing the clipboard.
fn printable(text: &str) -> String {
    text.chars().filter(|c| !c.is_control()).collect()
}

fn valid_service_id(id: &str) -> bool {
    let mut chars = id.chars();
    (3..=96).contains(&id.len())
        && chars.next().is_some_and(|c| c.is_ascii_lowercase())
        && chars.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '_' || c == '-')
}

fn valid_token(token: &str) -> bool {
    token.starts_with("astr_")
        && token.len() <= 128
        && token[5..]
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '-')
}

/// A model name as Codex's `-m` and Claude Code's settings take it.
fn chosen_model(model: Option<&str>) -> Result<Option<&str>, String> {
    match model.map(str::trim).filter(|model| !model.is_empty()) {
        Some(model) if model.chars().count() > 256 || model.chars().any(char::is_control) => {
            Err("invalid model name".into())
        }
        model => Ok(model),
    }
}

/// The settings file Claude Code starts with. It sits above the user's and
/// project settings, so it fixes everything that routes a request. Keys it
/// leaves out still come from the user's settings.
fn claude_settings(session: &Session) -> serde_json::Value {
    let model = session.model.unwrap_or("");
    let mut env = serde_json::Map::new();
    for (key, value) in [
        ("ANTHROPIC_BASE_URL", session.origin.to_string()),
        // AUTH_TOKEN, not API_KEY: an API key asks for approval first.
        ("ANTHROPIC_AUTH_TOKEN", session.token.to_string()),
        (
            "ANTHROPIC_CUSTOM_HEADERS",
            format!("{SERVICE_HEADER}: {}", session.service_id),
        ),
    ] {
        env.insert(key.into(), value.into());
    }
    for key in CLAUDE_CLEARED {
        env.insert(key.into(), "".into());
    }
    for key in CLAUDE_MODELS {
        env.insert(key.into(), model.into());
    }
    // Subagents then follow the models above.
    env.insert("CLAUDE_CODE_SUBAGENT_MODEL".into(), "".into());
    // A blank `model` also overrides one the user's settings name.
    serde_json::json!({ "env": env, "model": model })
}

/// Codex's flags for this session. Values stay plain words without spaces,
/// which Codex takes as text when they are not TOML, so a shell that strips
/// the quotes passes them unchanged.
fn codex_args(session: &Session) -> Vec<String> {
    let provider = format!("model_providers.{CODEX_PROVIDER}");
    let overrides = [
        format!("model_provider=\"{CODEX_PROVIDER}\""),
        // The display name is local UI.
        format!("{provider}.name=\"AstrLink\""),
        format!("{provider}.base_url=\"{}/v1\"", session.origin),
        format!("{provider}.wire_api=\"responses\""),
        format!("{provider}.env_key=\"{CODEX_TOKEN_VARIABLE}\""),
        format!(
            "{provider}.http_headers.{}=\"{}\"",
            CODEX_TOOLS_HEADER.0, CODEX_TOOLS_HEADER.1
        ),
        format!(
            "{provider}.http_headers.{SERVICE_HEADER}=\"{}\"",
            session.service_id
        ),
    ];
    let mut args: Vec<String> = overrides
        .into_iter()
        .flat_map(|value| ["-c".to_string(), value])
        .collect();
    if let Some(model) = session.model {
        args.push("-m".into());
        args.push(model.into());
    }
    args
}

/// The script that runs one session in `dir`: it starts the client in the
/// chosen directory and removes `dir` once the client exits or the window
/// closes. A Ctrl-C belongs to the client, so the script outlives it.
#[cfg(any(unix, test))]
fn session_script(session: &Session, dir: &Path, cleanup: &[PathBuf]) -> String {
    let mut script = String::from(
        "#!/bin/sh\n\
         # AstrLink opened this terminal for one session. This folder is removed\n\
         # when the client exits.\n",
    );
    script.push_str(&format!("dir={}\n", sh_quote(&dir.to_string_lossy())));
    script.push_str(
        "rm -f -- \"$0\"\n\
         trap 'rm -rf \"$dir\"' EXIT\n\
         trap 'exit 129' HUP\n\
         trap 'exit 143' TERM\n\
         trap ':' INT QUIT\n",
    );
    for path in cleanup {
        script.push_str(&format!("rm -f -- {}\n", sh_quote(&path.to_string_lossy())));
    }
    script.push_str(&format!(
        "cd {} || exit 1\nprintf '%s\\n\\n' {}\n",
        sh_quote(&session.directory.to_string_lossy()),
        sh_quote(&printable(session.banner)),
    ));
    match session.client {
        TerminalClient::Claude => {
            script.push_str("claude --settings \"$dir/settings.json\"\n");
        }
        TerminalClient::Codex => {
            let args: Vec<String> = codex_args(session)
                .iter()
                .map(|arg| sh_quote(arg))
                .collect();
            script.push_str(&format!(
                "{CODEX_TOKEN_VARIABLE}={} codex {}\n",
                sh_quote(session.token),
                args.join(" ")
            ));
        }
    }
    script.push_str(&format!(
        "printf '\\n%s\\n' {}\n",
        sh_quote(&printable(session.exited))
    ));
    script
}

/// Runs the session inside the user's login shell, which sets up PATH as a
/// new window would, then stays in that shell in the session's directory.
/// Shells outside the list run the session from `/bin/sh` instead.
#[cfg(any(unix, test))]
fn launch_script(shell: &Path, directory: &Path, session_path: &Path) -> String {
    format!(
        "#!/bin/sh\n\
         cd {}\n\
         SHELL={}\n\
         ASTRLINK_TERMINAL_SESSION={}\n\
         export SHELL ASTRLINK_TERMINAL_SESSION\n\
         case \"${{SHELL##*/}}\" in\n\
         bash | zsh | fish | ksh | mksh)\n\
         \texec \"$SHELL\" -l -i -c '/bin/sh \"$ASTRLINK_TERMINAL_SESSION\"; exec \"$SHELL\" -l -i'\n\
         \t;;\n\
         esac\n\
         /bin/sh \"$ASTRLINK_TERMINAL_SESSION\"\n\
         exec \"$SHELL\"\n",
        sh_quote(&directory.to_string_lossy()),
        sh_quote(&shell.to_string_lossy()),
        sh_quote(&session_path.to_string_lossy()),
    )
}

/// What a terminal types into the user's own shell, which already set up
/// the environment and stays, in the session's directory, once the client
/// exits. The leading space keeps the line out of most shells' history.
#[cfg(target_os = "macos")]
fn typed_command(directory: &Path, session_path: &Path) -> String {
    format!(
        " cd {}; /bin/sh {}",
        sh_quote(&directory.to_string_lossy()),
        sh_quote(&session_path.to_string_lossy())
    )
}

/// The Windows session: PowerShell reloads PATH, since the terminal inherits
/// the one AstrLink started with, and removes `dir` once the client exits.
#[cfg(any(windows, test))]
fn powershell_script(session: &Session, dir: &Path) -> String {
    let mut script = String::from(
        "# AstrLink opened this terminal for one session. This folder is removed\n\
         # when the client exits.\n",
    );
    script.push_str(&format!(
        "$dir = {}\n\
         Remove-Item -LiteralPath $PSCommandPath -Force -ErrorAction SilentlyContinue\n\
         $env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')\n\
         Set-Location -LiteralPath {}\n\
         Write-Host {}\n\
         Write-Host ''\n\
         try {{\n",
        ps_quote(&dir.to_string_lossy()),
        ps_quote(&session.directory.to_string_lossy()),
        ps_quote(&printable(session.banner)),
    ));
    match session.client {
        TerminalClient::Claude => {
            script.push_str("    & claude --settings (Join-Path $dir 'settings.json')\n");
        }
        TerminalClient::Codex => {
            let args: Vec<String> = codex_args(session)
                .iter()
                .map(|arg| ps_quote(arg))
                .collect();
            script.push_str(&format!(
                "    $env:{CODEX_TOKEN_VARIABLE} = {}\n    & codex {}\n",
                ps_quote(session.token),
                args.join(" ")
            ));
        }
    }
    script.push_str(&format!(
        "}} finally {{\n\
         \x20   Remove-Item Env:{CODEX_TOKEN_VARIABLE} -ErrorAction SilentlyContinue\n\
         \x20   Remove-Item -LiteralPath $dir -Recurse -Force -ErrorAction SilentlyContinue\n\
         }}\n\
         Write-Host ''\n\
         Write-Host {}\n",
        ps_quote(&printable(session.exited)),
    ));
    script
}

/// A new folder only this user can read, for one session's files.
fn private_dir() -> Result<PathBuf, String> {
    let mut random = [0u8; 12];
    getrandom::getrandom(&mut random).map_err(|error| error.to_string())?;
    let name: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
    let dir = std::env::temp_dir().join(format!("astrlink-terminal-{name}"));
    let builder = &mut fs::DirBuilder::new();
    #[cfg(unix)]
    std::os::unix::fs::DirBuilderExt::mode(builder, 0o700);
    builder
        .create(&dir)
        .map_err(|error| format!("unable to create {}: {error}", dir.display()))?;
    Ok(dir)
}

fn write_private(path: &Path, contents: &[u8], executable: bool) -> Result<(), String> {
    let mut options = fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    std::os::unix::fs::OpenOptionsExt::mode(&mut options, if executable { 0o700 } else { 0o600 });
    #[cfg(not(unix))]
    let _ = executable;
    options
        .open(path)
        .and_then(|mut file| file.write_all(contents))
        .map_err(|error| format!("unable to write {}: {error}", path.display()))
}

/// Terminal apps installed here, the platform's own first.
pub fn installed() -> Vec<TerminalChoice> {
    platform::installed()
        .into_iter()
        .map(|id| TerminalChoice {
            id,
            name: id.name(),
        })
        .collect()
}

/// Writes the session's files and opens `app` on them. The files go away if
/// the terminal cannot be opened.
pub fn open(app: TerminalApp, session: &Session) -> Result<(), String> {
    if !installed().iter().any(|choice| choice.id == app) {
        return Err(format!("{} is not installed", app.name()));
    }
    let dir = private_dir()?;
    let result = write_and_open(app, session, &dir);
    if result.is_err() {
        let _ = fs::remove_dir_all(&dir);
    }
    result
}

fn write_and_open(app: TerminalApp, session: &Session, dir: &Path) -> Result<(), String> {
    if session.client == TerminalClient::Claude {
        let settings = serde_json::to_vec_pretty(&claude_settings(session))
            .map_err(|error| error.to_string())?;
        write_private(&dir.join("settings.json"), &settings, false)?;
    }
    platform::open(app, session, dir)
}

#[cfg(unix)]
fn user_shell() -> PathBuf {
    use std::os::unix::fs::PermissionsExt;
    let usable = |path: &Path| {
        path.is_absolute()
            && fs::metadata(path)
                .is_ok_and(|meta| meta.is_file() && meta.permissions().mode() & 0o111 != 0)
    };
    if let Some(shell) = std::env::var_os("SHELL").map(PathBuf::from) {
        if usable(&shell) {
            return shell;
        }
    }
    // SAFETY: getpwuid returns the calling user's static entry or null.
    let entry = unsafe { libc::getpwuid(libc::getuid()) };
    if !entry.is_null() {
        // SAFETY: pw_shell is a NUL-terminated string owned by the entry.
        let shell = unsafe { std::ffi::CStr::from_ptr((*entry).pw_shell) };
        let shell = PathBuf::from(shell.to_string_lossy().into_owned());
        if usable(&shell) {
            return shell;
        }
    }
    PathBuf::from("/bin/sh")
}

/// Writes the POSIX scripts and returns the session script's path, and for a
/// directly started app the launcher's.
#[cfg(unix)]
fn write_posix(
    session: &Session,
    dir: &Path,
    cleanup: &[PathBuf],
) -> Result<(PathBuf, PathBuf), String> {
    let session_path = dir.join("session.sh");
    write_private(
        &session_path,
        session_script(session, dir, cleanup).as_bytes(),
        true,
    )?;
    let launch_path = dir.join("launch.sh");
    write_private(
        &launch_path,
        launch_script(&user_shell(), session.directory, &session_path).as_bytes(),
        true,
    )?;
    Ok((session_path, launch_path))
}

#[cfg(target_os = "macos")]
mod platform {
    use super::*;

    /// The user's own home, where terminal apps keep their files. An isolated
    /// AstrLink keeps its clients in a home of its own instead.
    fn real_home() -> Result<PathBuf, String> {
        std::env::var_os("HOME")
            .map(PathBuf::from)
            .ok_or_else(|| "unable to resolve the user home directory".to_string())
    }

    fn applescript_string(value: &str) -> String {
        format!("\"{}\"", value.replace('\\', "\\\\").replace('"', "\\\""))
    }

    /// Runs `command` and reports its own error output when it fails.
    fn run(command: &mut Command, what: &str) -> Result<(), String> {
        let output = command
            .stdin(Stdio::null())
            .output()
            .map_err(|error| format!("unable to start {what}: {error}"))?;
        if output.status.success() {
            return Ok(());
        }
        let detail = String::from_utf8_lossy(&output.stderr).trim().to_string();
        Err(if detail.is_empty() {
            format!("{what} failed with {}", output.status)
        } else {
            format!("{what} failed: {detail}")
        })
    }

    const APPS: [(TerminalApp, &str); 7] = [
        (TerminalApp::Terminal, "Terminal.app"),
        (TerminalApp::Iterm2, "iTerm.app"),
        (TerminalApp::Warp, "Warp.app"),
        (TerminalApp::Ghostty, "Ghostty.app"),
        (TerminalApp::Wezterm, "WezTerm.app"),
        (TerminalApp::Kitty, "kitty.app"),
        (TerminalApp::Alacritty, "Alacritty.app"),
    ];

    fn bundle(app: TerminalApp) -> Option<PathBuf> {
        let (_, name) = APPS.iter().find(|(id, _)| *id == app)?;
        let mut places = vec![
            PathBuf::from("/Applications"),
            PathBuf::from("/System/Applications/Utilities"),
            PathBuf::from("/Applications/Utilities"),
        ];
        if let Ok(home) = real_home() {
            places.push(home.join("Applications"));
        }
        places
            .into_iter()
            .map(|place| place.join(name))
            .find(|path| path.is_dir())
    }

    pub fn installed() -> Vec<TerminalApp> {
        APPS.iter()
            .map(|(app, _)| *app)
            .filter(|app| bundle(*app).is_some())
            .collect()
    }

    fn osascript(script: &str, what: &str) -> Result<(), String> {
        run(
            Command::new("/usr/bin/osascript").arg("-e").arg(script),
            what,
        )
    }

    pub fn open(app: TerminalApp, session: &Session, dir: &Path) -> Result<(), String> {
        // Warp runs a saved tab config, which the session removes once it runs.
        let warp_config = match app {
            TerminalApp::Warp => {
                let configs = real_home()?.join(".warp/tab_configs");
                fs::create_dir_all(&configs).map_err(|error| error.to_string())?;
                let name = format!(
                    "astrlink_terminal_{}",
                    dir.file_name()
                        .and_then(|name| name.to_str())
                        .and_then(|name| name.strip_prefix("astrlink-terminal-"))
                        .unwrap_or_default()
                );
                Some((configs.join(format!("{name}.toml")), name))
            }
            _ => None,
        };
        let cleanup: Vec<PathBuf> = warp_config.iter().map(|(path, _)| path.clone()).collect();
        let (session_path, launch_path) = write_posix(session, dir, &cleanup)?;
        let typed = typed_command(session.directory, &session_path);
        let bundle = bundle(app).ok_or_else(|| format!("{} is not installed", app.name()))?;
        let launch = launch_path.to_string_lossy().into_owned();
        match app {
            TerminalApp::Terminal => osascript(
                &format!(
                    // A cold `activate` opens a window of its own first.
                    "set command to {}\n\
                     set wasRunning to application \"Terminal\" is running\n\
                     tell application \"Terminal\"\n\
                     \tif not wasRunning then launch\n\
                     \tdo script command\n\
                     \tactivate\n\
                     end tell",
                    applescript_string(&typed)
                ),
                "Terminal",
            ),
            TerminalApp::Iterm2 => osascript(
                &format!(
                    // A cold start opens a window; the session goes there.
                    "set command to {}\n\
                     set wasRunning to application \"iTerm\" is running\n\
                     tell application \"iTerm\"\n\
                     \tactivate\n\
                     \tif wasRunning and (count of windows) > 0 then\n\
                     \t\ttell current window to create tab with default profile\n\
                     \telse\n\
                     \t\trepeat 30 times\n\
                     \t\t\tif (count of windows) > 0 then exit repeat\n\
                     \t\t\tdelay 0.1\n\
                     \t\tend repeat\n\
                     \t\tif (count of windows) = 0 then create window with default profile\n\
                     \tend if\n\
                     \ttell current session of current window to write text command\n\
                     end tell",
                    applescript_string(&typed)
                ),
                "iTerm2",
            ),
            TerminalApp::Warp => {
                let (path, name) = warp_config.expect("Warp has a tab config");
                let mut config = toml_edit::DocumentMut::new();
                config["name"] = toml_edit::value("AstrLink");
                let mut pane = toml_edit::Table::new();
                pane["id"] = toml_edit::value("session");
                pane["type"] = toml_edit::value("terminal");
                pane["commands"] = toml_edit::value(toml_edit::Array::from_iter([typed.as_str()]));
                let mut panes = toml_edit::ArrayOfTables::new();
                panes.push(pane);
                config["panes"] = toml_edit::Item::ArrayOfTables(panes);
                write_private(&path, config.to_string().as_bytes(), false)?;
                let opened = run(
                    Command::new("/usr/bin/open").arg(format!("warp://tab_config/{name}")),
                    "Warp",
                );
                if opened.is_err() {
                    let _ = fs::remove_file(&path);
                }
                opened
            }
            // Ghostty, kitty and Alacritty run the script as their program and
            // quit with the last window, so each session gets its own process.
            TerminalApp::Ghostty | TerminalApp::Alacritty => run(
                Command::new("/usr/bin/open")
                    .arg("-na")
                    .arg(&bundle)
                    .args(["--args", "-e", "/bin/sh", &launch]),
                app.name(),
            ),
            TerminalApp::Kitty => run(
                Command::new("/usr/bin/open")
                    .arg("-na")
                    .arg(&bundle)
                    .args(["--args", "/bin/sh", &launch]),
                app.name(),
            ),
            TerminalApp::Wezterm => run(
                Command::new("/usr/bin/open")
                    .arg("-na")
                    .arg(&bundle)
                    .args(["--args", "start", "--", "/bin/sh", &launch]),
                app.name(),
            ),
            _ => Err(format!("{} is not available on macOS", app.name())),
        }
    }
}

#[cfg(all(unix, not(target_os = "macos")))]
mod platform {
    use super::*;

    /// Each app with the arguments that make it run a program.
    const APPS: [(TerminalApp, &str, &[&str]); 8] = [
        (
            TerminalApp::XTerminalEmulator,
            "x-terminal-emulator",
            &["-e"],
        ),
        (TerminalApp::GnomeTerminal, "gnome-terminal", &["--"]),
        (TerminalApp::Konsole, "konsole", &["-e"]),
        (TerminalApp::Xfce4Terminal, "xfce4-terminal", &["-x"]),
        (TerminalApp::Kitty, "kitty", &[]),
        (TerminalApp::Alacritty, "alacritty", &["-e"]),
        (TerminalApp::Wezterm, "wezterm", &["start", "--"]),
        (TerminalApp::Ghostty, "ghostty", &["-e"]),
    ];

    fn executable(name: &str) -> Option<PathBuf> {
        std::env::split_paths(&std::env::var_os("PATH")?)
            .map(|dir| dir.join(name))
            .find(|path| path.is_file())
    }

    pub fn installed() -> Vec<TerminalApp> {
        APPS.iter()
            .filter(|(_, binary, _)| executable(binary).is_some())
            .map(|(app, _, _)| *app)
            .collect()
    }

    pub fn open(app: TerminalApp, session: &Session, dir: &Path) -> Result<(), String> {
        let (_, binary, args) = APPS
            .iter()
            .find(|(id, _, _)| *id == app)
            .ok_or_else(|| format!("{} is not available on Linux", app.name()))?;
        let program =
            executable(binary).ok_or_else(|| format!("{} is not installed", app.name()))?;
        let (_, launch) = write_posix(session, dir, &[])?;
        let mut child = Command::new(program)
            .args(*args)
            .arg("/bin/sh")
            .arg(&launch)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .map_err(|error| format!("unable to start {}: {error}", app.name()))?;
        // Some terminals stay in the foreground until their window closes.
        std::thread::spawn(move || {
            let _ = child.wait();
        });
        Ok(())
    }
}

#[cfg(windows)]
mod platform {
    use std::os::windows::process::CommandExt;

    use super::*;

    const CREATE_NO_WINDOW: u32 = 0x0800_0000;

    fn windows_terminal() -> Option<PathBuf> {
        std::env::split_paths(&std::env::var_os("PATH")?)
            .map(|dir| dir.join("wt.exe"))
            .find(|path| path.is_file())
    }

    pub fn installed() -> Vec<TerminalApp> {
        let mut apps = Vec::new();
        if windows_terminal().is_some() {
            apps.push(TerminalApp::WindowsTerminal);
        }
        apps.push(TerminalApp::Powershell);
        apps
    }

    pub fn open(app: TerminalApp, session: &Session, dir: &Path) -> Result<(), String> {
        let script = dir.join("session.ps1");
        // A byte order mark makes Windows PowerShell read the file as UTF-8.
        let mut contents = vec![0xEF, 0xBB, 0xBF];
        contents.extend_from_slice(powershell_script(session, dir).as_bytes());
        write_private(&script, &contents, false)?;
        let powershell = [
            "powershell.exe",
            "-NoLogo",
            "-NoExit",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
        ];
        match app {
            TerminalApp::WindowsTerminal => {
                let mut child =
                    Command::new(windows_terminal().ok_or("Windows Terminal is not installed")?)
                        .args(["-w", "0", "new-tab"])
                        .args(powershell)
                        .arg(&script)
                        .stdin(Stdio::null())
                        .stdout(Stdio::null())
                        .stderr(Stdio::null())
                        .spawn()
                        .map_err(|error| format!("unable to start {}: {error}", app.name()))?;
                // `wt` may stay until the tab hands off; reap it whenever it ends.
                std::thread::spawn(move || {
                    let _ = child.wait();
                });
                Ok(())
            }
            TerminalApp::Powershell => {
                // `start` gives PowerShell a console window of its own. Started
                // straight from this windowless app, it would get none to use.
                // The blank argument is the window title `start` reads first.
                let output = Command::new("cmd.exe")
                    .args(["/D", "/C", "start", ""])
                    .args(powershell)
                    .arg(&script)
                    .creation_flags(CREATE_NO_WINDOW)
                    .stdin(Stdio::null())
                    .output()
                    .map_err(|error| format!("unable to start {}: {error}", app.name()))?;
                if output.status.success() {
                    return Ok(());
                }
                Err(format!(
                    "{} failed: {}",
                    app.name(),
                    String::from_utf8_lossy(&output.stderr).trim()
                ))
            }
            _ => Err(format!("{} is not available on Windows", app.name())),
        }
    }
}

#[tauri::command]
pub fn list_terminal_apps() -> Vec<TerminalChoice> {
    installed()
}

#[tauri::command]
pub async fn pick_terminal_directory(
    app: tauri::AppHandle,
    start: Option<String>,
) -> Result<Option<String>, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let mut dialog = app.dialog().file();
        if let Some(start) = start.filter(|start| Path::new(start).is_dir()) {
            dialog = dialog.set_directory(start);
        }
        let Some(folder) = dialog.blocking_pick_folder() else {
            return Ok(None);
        };
        let path = folder.into_path().map_err(|error| error.to_string())?;
        Ok(Some(path.to_string_lossy().into_owned()))
    })
    .await
    .map_err(|error| error.to_string())?
}

#[derive(Debug, Deserialize)]
pub struct OpenServiceTerminal {
    service_id: String,
    service_name: String,
    client: TerminalClient,
    directory: String,
    model: Option<String>,
    terminal: TerminalApp,
    inference_url: String,
}

/// Opens `terminal` in `directory` running the client through one provider.
/// The session uses the token the client is set up with here, or the first
/// token when AstrLink has not set the client up.
#[tauri::command]
pub async fn open_service_terminal(
    request: OpenServiceTerminal,
    manager: State<'_, Arc<CoreManager>>,
    preferences: State<'_, Arc<PreferencesStore>>,
) -> Result<(), String> {
    if !valid_service_id(&request.service_id) {
        return Err("invalid API provider ID".into());
    }
    let model = chosen_model(request.model.as_deref())?.map(str::to_string);
    let origin = client_config::local_origin(&request.inference_url)?;
    let directory = fs::canonicalize(&request.directory)
        .ok()
        .filter(|path| path.is_dir())
        .ok_or_else(|| format!("{} is not a folder", request.directory))?;
    // This AstrLink's own home, where it records the clients it set up.
    let home = control_session::user_home()?;
    let client = request.client;
    let configured = tauri::async_runtime::spawn_blocking(move || {
        client_config::configured_token(Home::local(&home), client.config())
    })
    .await
    .map_err(|error| error.to_string())??;
    let page = manager.list_access_tokens().await?;
    let ids: Vec<&str> = page["items"]
        .as_array()
        .into_iter()
        .flatten()
        .filter_map(|item| item["id"].as_str())
        .collect();
    let token_id = configured
        .as_deref()
        .filter(|id| ids.contains(id))
        .or(ids.first().copied())
        .ok_or("create an access token first")?
        .to_string();
    let token =
        client_config::reveal_access_token(&manager, &token_id, &request.inference_url).await?;
    if !valid_token(&token) {
        return Err("access token is unavailable".into());
    }
    let locale = preferences.snapshot().values.locale;
    let vars = [
        ("client", client.label()),
        ("service", request.service_name.as_str()),
    ];
    let banner = i18n::t(locale, "host.terminal.banner", &vars);
    let exited = i18n::t(locale, "host.terminal.exited", &vars);
    let terminal = request.terminal;
    tauri::async_runtime::spawn_blocking(move || {
        open(
            request.terminal,
            &Session {
                client,
                origin: &origin,
                token: &token,
                service_id: &request.service_id,
                model: model.as_deref(),
                directory: &directory,
                banner: &banner,
                exited: &exited,
            },
        )
    })
    .await
    .map_err(|error| error.to_string())?
    .map_err(|error| {
        // macOS refuses Apple events to an app the user has not allowed
        // AstrLink to control.
        if error.contains("(-1743)") {
            i18n::t(
                locale,
                "host.terminal.automationDenied",
                &[("terminal", terminal.name())],
            )
        } else {
            error
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn session<'a>(client: TerminalClient, model: Option<&'a str>) -> Session<'a> {
        Session {
            client,
            origin: "http://127.0.0.1:18317",
            token: "astr_secret",
            service_id: "svc_claude_two",
            model,
            directory: Path::new("/work/it's here"),
            banner: "Claude Code here goes through \u{1b}]52;c;aGk=\u{7}Claude 2",
            exited: "Claude Code exited.",
        }
    }

    #[test]
    fn claude_settings_route_through_the_provider_and_reset_models() {
        let defaults = claude_settings(&session(TerminalClient::Claude, None));
        let env = &defaults["env"];
        assert_eq!(env["ANTHROPIC_BASE_URL"], "http://127.0.0.1:18317");
        assert_eq!(env["ANTHROPIC_AUTH_TOKEN"], "astr_secret");
        assert_eq!(
            env["ANTHROPIC_CUSTOM_HEADERS"],
            "X-AstrLink-Service: svc_claude_two"
        );
        for key in CLAUDE_CLEARED.iter().chain(&CLAUDE_MODELS) {
            assert_eq!(env[*key], "", "{key}");
        }
        // A blank model overrides one the user's settings name.
        assert_eq!(defaults["model"], "");

        let chosen = claude_settings(&session(TerminalClient::Claude, Some("glm-5")));
        for key in CLAUDE_MODELS {
            assert_eq!(chosen["env"][key], "glm-5", "{key}");
        }
        assert_eq!(chosen["env"]["CLAUDE_CODE_SUBAGENT_MODEL"], "");
        assert_eq!(chosen["model"], "glm-5");
    }

    #[test]
    fn codex_args_define_a_provider_of_their_own() {
        let args = codex_args(&session(TerminalClient::Codex, Some("gpt-5.5")));
        let overrides: Vec<&str> = args
            .chunks(2)
            .filter(|pair| pair[0] == "-c")
            .map(|pair| pair[1].as_str())
            .collect();
        assert_eq!(
            overrides,
            [
                "model_provider=\"astrlink-terminal\"",
                "model_providers.astrlink-terminal.name=\"AstrLink\"",
                "model_providers.astrlink-terminal.base_url=\"http://127.0.0.1:18317/v1\"",
                "model_providers.astrlink-terminal.wire_api=\"responses\"",
                "model_providers.astrlink-terminal.env_key=\"ASTRLINK_TERMINAL_TOKEN\"",
                "model_providers.astrlink-terminal.http_headers.x-openai-actor-authorization=\"codex-imagegen\"",
                "model_providers.astrlink-terminal.http_headers.X-AstrLink-Service=\"svc_claude_two\"",
            ]
        );
        assert_eq!(&args[args.len() - 2..], ["-m", "gpt-5.5"]);
        // The token never reaches the command line.
        assert!(!args.iter().any(|arg| arg.contains("astr_secret")));
        assert!(!codex_args(&session(TerminalClient::Codex, None)).contains(&"-m".to_string()));
    }

    #[test]
    fn session_script_quotes_paths_and_strips_terminal_controls() {
        let dir = Path::new("/tmp/astrlink-terminal-ab");
        let warp = PathBuf::from("/home/u/.warp/tab_configs/astrlink_terminal_ab.toml");
        let script = session_script(&session(TerminalClient::Claude, None), dir, &[warp]);
        assert!(script.contains("dir='/tmp/astrlink-terminal-ab'\n"));
        assert!(script.contains("rm -f -- '/home/u/.warp/tab_configs/astrlink_terminal_ab.toml'\n"));
        assert!(script.contains("cd '/work/it'\\''s here' || exit 1\n"));
        assert!(script.contains("claude --settings \"$dir/settings.json\"\n"));
        // The OSC 52 sequence in the name loses its escape and bell, so the
        // terminal prints it instead of writing the clipboard.
        assert!(script
            .contains("printf '%s\\n\\n' 'Claude Code here goes through ]52;c;aGk=Claude 2'\n"));
        assert!(!script.contains('\u{1b}') && !script.contains('\u{7}'));

        let codex = session_script(&session(TerminalClient::Codex, Some("gpt-5.5")), dir, &[]);
        assert!(codex.contains(
            "ASTRLINK_TERMINAL_TOKEN='astr_secret' codex '-c' 'model_provider=\"astrlink-terminal\"'"
        ));
        assert!(codex.contains(" '-m' 'gpt-5.5'\n"));
    }

    #[test]
    fn launch_script_enters_the_login_shell() {
        let script = launch_script(
            Path::new("/bin/zsh"),
            Path::new("/work/it's here"),
            Path::new("/tmp/d/session.sh"),
        );
        // The shell left after the client exits starts in the directory too.
        assert!(script.starts_with(
            "#!/bin/sh\ncd '/work/it'\\''s here'\nSHELL='/bin/zsh'\nASTRLINK_TERMINAL_SESSION='/tmp/d/session.sh'\n"
        ));
        assert!(script.contains(
            "exec \"$SHELL\" -l -i -c '/bin/sh \"$ASTRLINK_TERMINAL_SESSION\"; exec \"$SHELL\" -l -i'"
        ));
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn typed_command_leaves_the_shell_in_the_directory() {
        assert_eq!(
            typed_command(Path::new("/work/it's here"), Path::new("/tmp/d/session.sh")),
            " cd '/work/it'\\''s here'; /bin/sh '/tmp/d/session.sh'"
        );
    }

    #[test]
    fn powershell_script_quotes_and_cleans_up() {
        let script = powershell_script(
            &session(TerminalClient::Codex, None),
            Path::new(r"C:\Temp\astrlink-terminal-ab"),
        );
        assert!(script.contains(r"$dir = 'C:\Temp\astrlink-terminal-ab'"));
        assert!(script.contains("Set-Location -LiteralPath '/work/it''s here'"));
        assert!(script.contains("$env:ASTRLINK_TERMINAL_TOKEN = 'astr_secret'"));
        assert!(script.contains("& codex '-c' 'model_provider=\"astrlink-terminal\"'"));
        assert!(script.contains("Remove-Item -LiteralPath $dir -Recurse -Force"));
        assert!(!script.contains('\u{1b}'));
    }

    #[test]
    fn powershell_quotes_hold_typographic_quotes() {
        assert_eq!(ps_quote("Bob’s ‘key’"), "'Bob’’s ‘‘key’’'");
        assert_eq!(ps_quote("it's"), "'it''s'");
    }

    #[test]
    fn inputs_are_checked() {
        assert!(valid_service_id("svc_claude-2"));
        assert!(!valid_service_id("Svc"));
        assert!(!valid_service_id("svc claude"));
        assert!(valid_token("astr_AbC-_9"));
        assert!(!valid_token("astr_a'b"));
        assert_eq!(chosen_model(Some("  ")), Ok(None));
        assert_eq!(chosen_model(Some(" glm-5 ")), Ok(Some("glm-5")));
        assert!(chosen_model(Some("a\nb")).is_err());
    }

    #[cfg(unix)]
    #[test]
    fn private_files_belong_to_the_user_alone() {
        use std::os::unix::fs::PermissionsExt;
        let dir = private_dir().unwrap();
        let mode = |path: &Path| fs::metadata(path).unwrap().permissions().mode() & 0o777;
        write_private(&dir.join("settings.json"), b"{}", false).unwrap();
        write_private(&dir.join("session.sh"), b"#!/bin/sh\n", true).unwrap();
        assert_eq!(
            (
                mode(&dir),
                mode(&dir.join("settings.json")),
                mode(&dir.join("session.sh"))
            ),
            (0o700, 0o600, 0o700)
        );
        // A second session never reuses a folder.
        let other = private_dir().unwrap();
        assert_ne!(other, dir);
        fs::remove_dir_all(other).unwrap();
        fs::remove_dir_all(dir).unwrap();
    }
}
