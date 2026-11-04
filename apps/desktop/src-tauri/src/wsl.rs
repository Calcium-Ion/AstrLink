//! WSL distributions whose agent tools the Windows desktop configures. Their
//! homes are edited through the `\\wsl$` share, and their tools run the
//! desktop's Windows CLI through WSL interop. A home comes from CC Switch's
//! directory settings when they point into the distribution, and from its
//! default user otherwise.

use std::{
    fs,
    path::{Path, PathBuf},
};

use crate::agent_home::ToolDirs;

/// One home in a WSL distribution.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct WslHome {
    /// The distribution's name, as `wsl.exe --distribution` takes it.
    pub distribution: String,
    /// The home as Windows reaches it, through the distribution's share.
    pub share: PathBuf,
    /// Where the distribution mounts Windows drives: `/mnt` by default, empty
    /// when they mount at `/`.
    pub mount_root: String,
    /// Tool directories CC Switch points into this distribution.
    pub dirs: ToolDirs,
}

impl WslHome {
    /// A Windows drive path as the distribution sees it, or `None` for a path
    /// that is not on a drive letter.
    pub fn linux_path(&self, path: &Path) -> Option<String> {
        let path = path.to_str()?;
        let path = path.strip_prefix(r"\\?\").unwrap_or(path);
        let mut chars = path.chars();
        let drive = chars.next().filter(char::is_ascii_alphabetic)?;
        if chars.next() != Some(':') {
            return None;
        }
        let rest = chars.as_str().replace('\\', "/");
        if !rest.is_empty() && !rest.starts_with('/') {
            return None;
        }
        Some(format!(
            "{}/{}{}",
            self.mount_root,
            drive.to_ascii_lowercase(),
            rest.trim_end_matches('/')
        ))
    }
}

/// Tool directories that point into one WSL distribution.
#[derive(Debug, Default, PartialEq, Eq)]
pub struct WslDirs {
    pub distribution: String,
    /// The home the first directory inside a home sits in, `None` when every
    /// directory lies outside one.
    pub home: Option<PathBuf>,
    pub dirs: ToolDirs,
}

/// Splits tool directories into the desktop user's and those inside WSL
/// distributions.
pub fn split_dirs(dirs: ToolDirs) -> (ToolDirs, Vec<WslDirs>) {
    let mut local = ToolDirs::default();
    let mut wsl: Vec<WslDirs> = Vec::new();
    let mut place = |dir: Option<PathBuf>, field: fn(&mut ToolDirs) -> &mut Option<PathBuf>| {
        let Some(dir) = dir else {
            return;
        };
        let Some((distribution, home)) = share_location(&dir) else {
            *field(&mut local) = Some(dir);
            return;
        };
        let index = match wsl
            .iter()
            .position(|entry| entry.distribution.eq_ignore_ascii_case(&distribution))
        {
            Some(index) => index,
            None => {
                wsl.push(WslDirs {
                    distribution,
                    ..Default::default()
                });
                wsl.len() - 1
            }
        };
        let entry = &mut wsl[index];
        if entry.home.is_none() {
            entry.home = home;
        }
        *field(&mut entry.dirs) = Some(dir);
    };
    place(dirs.claude, |dirs| &mut dirs.claude);
    place(dirs.codex, |dirs| &mut dirs.codex);
    place(dirs.grok, |dirs| &mut dirs.grok);
    place(dirs.gemini, |dirs| &mut dirs.gemini);
    place(dirs.opencode, |dirs| &mut dirs.opencode);
    place(dirs.openclaw, |dirs| &mut dirs.openclaw);
    place(dirs.pi, |dirs| &mut dirs.pi);
    (local, wsl)
}

/// The distribution a `\\wsl$` or `\\wsl.localhost` path lies in, and the
/// home it lies in: `/home/<user>` or `/root`, reached through the same share.
fn share_location(path: &Path) -> Option<(String, Option<PathBuf>)> {
    let (server, distribution, parts) = share_parts(path)?;
    let parts = parts.iter().map(String::as_str).collect::<Vec<_>>();
    let home = match parts.as_slice() {
        ["home", user, ..] => Some(format!(r"\\{server}\{distribution}\home\{user}")),
        ["root", ..] => Some(format!(r"\\{server}\{distribution}\root")),
        _ => None,
    };
    Some((distribution, home.map(PathBuf::from)))
}

/// The distribution a `\\wsl$` or `\\wsl.localhost` path lies in, and the
/// path as the distribution sees it.
#[cfg_attr(not(windows), allow(dead_code))]
fn inside_share(path: &Path) -> Option<(String, String)> {
    let (_, distribution, parts) = share_parts(path)?;
    Some((distribution, format!("/{}", parts.join("/"))))
}

/// A share path's server, its distribution, and its segments inside the
/// distribution.
fn share_parts(path: &Path) -> Option<(String, String, Vec<String>)> {
    let raw = path.to_str()?.replace('/', "\\");
    let raw = match raw.strip_prefix(r"\\?\UNC\") {
        Some(rest) => format!(r"\\{rest}"),
        None => raw,
    };
    let mut parts = raw
        .strip_prefix(r"\\")?
        .split('\\')
        .filter(|part| !part.is_empty() && *part != ".");
    let server = parts.next()?;
    if !server.eq_ignore_ascii_case("wsl$") && !server.eq_ignore_ascii_case("wsl.localhost") {
        return None;
    }
    let distribution = parts.next()?;
    let parts = parts.map(str::to_string).collect::<Vec<_>>();
    if parts.iter().any(|part| part == "..") {
        return None;
    }
    Some((server.to_string(), distribution.to_string(), parts))
}

/// Gives `temporary`, staged in a WSL share to replace `target`, the Linux
/// mode the write should leave, before its contents land: owner-only for a
/// secret, else the mode of the file it replaces. Windows cannot set Linux
/// modes through the share, so the distribution's default user, who owns
/// what the share creates, sets them. Returns whether the files lie in a
/// share.
pub fn settle_share_mode(temporary: &Path, target: &Path, secret: bool) -> Result<bool, String> {
    #[cfg(windows)]
    {
        let (Some((distribution, staged)), Some((_, replaced))) =
            (inside_share(temporary), inside_share(target))
        else {
            return Ok(false);
        };
        let mode = if secret {
            "600".to_string()
        } else if target.exists() {
            let output = platform::run(
                &[
                    "--distribution",
                    distribution.as_str(),
                    "--exec",
                    "stat",
                    "-c",
                    "%a",
                    replaced.as_str(),
                ],
                platform::MODE_TIMEOUT,
            )?;
            file_mode(&output)
                .ok_or_else(|| format!("{replaced} reported an unusable mode {output:?}"))?
        } else {
            // A new file keeps the distribution's default mode, as it would anywhere.
            return Ok(true);
        };
        platform::run(
            &[
                "--distribution",
                distribution.as_str(),
                "--exec",
                "chmod",
                mode.as_str(),
                staged.as_str(),
            ],
            platform::MODE_TIMEOUT,
        )?;
        Ok(true)
    }
    #[cfg(not(windows))]
    {
        // Only Windows reaches WSL homes through their share.
        let _ = (temporary, target, secret);
        Ok(false)
    }
}

/// The permission bits `stat -c %a` prints, as `chmod` takes them.
#[cfg_attr(not(windows), allow(dead_code))]
fn file_mode(output: &str) -> Option<String> {
    let mode = output.trim();
    ((1..=4).contains(&mode.len()) && mode.chars().all(|c| matches!(c, '0'..='7')))
        .then(|| mode.to_string())
}

pub struct Distribution {
    pub name: String,
    pub running: bool,
}

/// Registered distributions, apart from Docker Desktop's internal ones.
/// Listing them starts none.
pub fn distributions() -> Vec<Distribution> {
    #[cfg(windows)]
    {
        platform::distributions()
    }
    #[cfg(not(windows))]
    {
        Vec::new()
    }
}

/// Which distributions to reach.
#[derive(Clone, Copy)]
pub enum Reach<'a> {
    /// The running ones; a check never starts a distribution.
    Running,
    /// The running ones and the named ones, which start if stopped.
    RunningAnd(&'a [String]),
    /// Only the named ones, which start if stopped.
    Named(&'a [String]),
    /// The named ones that are running.
    RunningAmong(&'a [String]),
}

/// The homes of the distributions `reach` selects, and the names of the
/// registered ones left unchecked. Only a distribution named for starting
/// fails the call when its home cannot be read.
pub fn homes(dirs: &[WslDirs], reach: Reach) -> Result<(Vec<WslHome>, Vec<String>), String> {
    let mut homes = Vec::new();
    let mut unchecked = Vec::new();
    if matches!(reach, Reach::Named(names) | Reach::RunningAmong(names) if names.is_empty()) {
        return Ok((homes, unchecked));
    }
    for distribution in distributions() {
        let named = |names: &[String]| {
            names
                .iter()
                .any(|name| name.eq_ignore_ascii_case(&distribution.name))
        };
        let (reached, start) = match reach {
            Reach::Running => (distribution.running, false),
            Reach::RunningAnd(names) => {
                let start = named(names);
                (distribution.running || start, start)
            }
            Reach::Named(names) => (named(names), named(names)),
            Reach::RunningAmong(names) => (distribution.running && named(names), false),
        };
        if !reached {
            unchecked.push(distribution.name);
            continue;
        }
        let configured = dirs
            .iter()
            .find(|entry| entry.distribution.eq_ignore_ascii_case(&distribution.name));
        match home(&distribution.name, configured) {
            Ok(home) => homes.push(home),
            Err(error) if start => return Err(error),
            Err(error) => {
                eprintln!(
                    "unable to check WSL distribution {}: {error}",
                    distribution.name
                );
                unchecked.push(distribution.name);
            }
        }
    }
    Ok((homes, unchecked))
}

fn home(distribution: &str, configured: Option<&WslDirs>) -> Result<WslHome, String> {
    let share = match configured.and_then(|entry| entry.home.clone()) {
        Some(share) => share,
        None => default_home(distribution)?,
    };
    Ok(WslHome {
        distribution: distribution.to_string(),
        share,
        mount_root: mount_root(distribution),
        dirs: configured
            .map(|entry| entry.dirs.clone())
            .unwrap_or_default(),
    })
}

/// The distribution's root as Windows reaches it. Reaching it starts a
/// stopped distribution.
pub fn share_root(distribution: &str) -> PathBuf {
    PathBuf::from(format!(r"\\wsl$\{distribution}"))
}

/// Where the distribution mounts Windows drives, read from its
/// `/etc/wsl.conf`.
pub fn mount_root(distribution: &str) -> String {
    let conf = share_root(distribution).join("etc").join("wsl.conf");
    parse_mount_root(&fs::read_to_string(conf).unwrap_or_default())
}

/// wsl.exe, which runs commands in a distribution.
pub fn program() -> Result<PathBuf, String> {
    #[cfg(windows)]
    {
        platform::program()
    }
    #[cfg(not(windows))]
    {
        Err("WSL needs Windows".to_string())
    }
}

/// The default user's home, which starts the distribution if it is stopped.
fn default_home(distribution: &str) -> Result<PathBuf, String> {
    #[cfg(windows)]
    {
        let home = platform::run(
            &["--distribution", distribution, "--exec", "printenv", "HOME"],
            platform::HOME_TIMEOUT,
        )?;
        share_path(distribution, &home).ok_or_else(|| {
            format!("WSL distribution {distribution} reported an unusable home {home:?}")
        })
    }
    #[cfg(not(windows))]
    {
        Err(format!("WSL distribution {distribution} needs Windows"))
    }
}

/// The drive mount root `[automount] root` sets in `/etc/wsl.conf`, `/mnt`
/// when it is unset.
fn parse_mount_root(conf: &str) -> String {
    let mut section = String::new();
    for line in conf.lines() {
        let line = line.split('#').next().unwrap_or_default().trim();
        if let Some(name) = line
            .strip_prefix('[')
            .and_then(|rest| rest.strip_suffix(']'))
        {
            section = name.trim().to_ascii_lowercase();
            continue;
        }
        let Some((key, value)) = line.split_once('=') else {
            continue;
        };
        if section == "automount" && key.trim().eq_ignore_ascii_case("root") {
            let value = value.trim().trim_matches('"');
            if value.starts_with('/') {
                return value.trim_end_matches('/').to_string();
            }
        }
    }
    "/mnt".to_string()
}

/// Whether clients in WSL reach the desktop's `localhost`: WSL 2 shares it
/// only with mirrored networking, set in `%UserProfile%\.wslconfig`.
pub fn localhost_shared(host: &Path) -> bool {
    let conf = fs::read_to_string(host.join(".wslconfig")).unwrap_or_default();
    let mut section = String::new();
    for line in conf.lines() {
        let line = line.split('#').next().unwrap_or_default().trim();
        if let Some(name) = line
            .strip_prefix('[')
            .and_then(|rest| rest.strip_suffix(']'))
        {
            section = name.trim().to_ascii_lowercase();
            continue;
        }
        let Some((key, value)) = line.split_once('=') else {
            continue;
        };
        // Early WSL 2 releases read the setting from [experimental].
        if matches!(section.as_str(), "wsl2" | "experimental")
            && key.trim().eq_ignore_ascii_case("networkingMode")
            && value
                .trim()
                .trim_matches('"')
                .eq_ignore_ascii_case("mirrored")
        {
            return true;
        }
    }
    false
}

/// wsl.exe prints its own output in UTF-16 unless it honours `WSL_UTF8`;
/// the Linux commands it runs print UTF-8.
#[cfg_attr(not(windows), allow(dead_code))]
fn decode(bytes: &[u8]) -> String {
    let text = if bytes.contains(&0) {
        let units = bytes
            .chunks_exact(2)
            .map(|pair| u16::from_le_bytes([pair[0], pair[1]]))
            .collect::<Vec<_>>();
        String::from_utf16_lossy(&units)
    } else {
        String::from_utf8_lossy(bytes).into_owned()
    };
    text.trim_start_matches('\u{feff}').to_string()
}

/// Names from `wsl.exe --list --quiet`, skipping Docker Desktop's internal
/// distributions and any name that cannot be a share path segment.
#[cfg_attr(not(windows), allow(dead_code))]
fn parse_names(output: &str) -> Vec<String> {
    output
        .lines()
        .map(|line| line.trim_matches(|c: char| c.is_whitespace() || c == '\0'))
        .filter(|name| {
            !name.is_empty()
                && *name != "."
                && *name != ".."
                && !name.starts_with("docker-desktop")
                && !name
                    .chars()
                    .any(|c| c.is_control() || r#"\/:*?"<>|"#.contains(c))
        })
        .map(str::to_string)
        .collect()
}

#[cfg_attr(not(windows), allow(dead_code))]
fn share_path(distribution: &str, home: &str) -> Option<PathBuf> {
    let home = home.trim().trim_end_matches('/');
    if !home.starts_with('/') || home.split('/').any(|part| part == "..") {
        return None;
    }
    let mut share = format!(r"\\wsl$\{distribution}");
    for part in home.split('/').filter(|part| !part.is_empty()) {
        share.push('\\');
        share.push_str(part);
    }
    Some(PathBuf::from(share))
}

#[cfg(windows)]
mod platform {
    use std::{
        io::Read,
        os::windows::process::CommandExt,
        path::PathBuf,
        process::{Command, Stdio},
        thread,
        time::{Duration, Instant},
    };

    use super::{decode, parse_names, Distribution};

    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    const LIST_TIMEOUT: Duration = Duration::from_secs(10);
    /// Covers starting a stopped distribution, which may boot the WSL VM.
    pub const HOME_TIMEOUT: Duration = Duration::from_secs(60);
    /// The distribution already runs: the write through its share started it.
    pub const MODE_TIMEOUT: Duration = Duration::from_secs(10);

    pub fn distributions() -> Vec<Distribution> {
        let Ok(all) = run(&["--list", "--quiet"], LIST_TIMEOUT) else {
            return Vec::new();
        };
        let all = parse_names(&all);
        if all.is_empty() {
            return Vec::new();
        }
        // wsl.exe exits non-zero when none is running.
        let running = run(&["--list", "--running", "--quiet"], LIST_TIMEOUT)
            .map(|output| parse_names(&output))
            .unwrap_or_default();
        all.into_iter()
            .map(|name| Distribution {
                running: running.contains(&name),
                name,
            })
            .collect()
    }

    pub fn program() -> Result<PathBuf, String> {
        let system = std::env::var_os("SystemRoot")
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from(r"C:\Windows"));
        let program = system.join("System32").join("wsl.exe");
        if program.is_file() {
            Ok(program)
        } else {
            Err("WSL is not installed".to_string())
        }
    }

    /// Runs wsl.exe without a console window and returns its decoded output.
    pub fn run(args: &[&str], timeout: Duration) -> Result<String, String> {
        let mut child = Command::new(program()?)
            .args(args)
            .env("WSL_UTF8", "1")
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .creation_flags(CREATE_NO_WINDOW)
            .spawn()
            .map_err(|error| format!("unable to run wsl.exe: {error}"))?;
        let stdout = child.stdout.take().expect("piped stdout");
        let stderr = child.stderr.take().expect("piped stderr");
        let out = thread::spawn(move || capture(stdout));
        let err = thread::spawn(move || capture(stderr));
        let deadline = Instant::now() + timeout;
        let status = loop {
            match child.try_wait() {
                Ok(Some(status)) => break status,
                Ok(None) if Instant::now() < deadline => thread::sleep(Duration::from_millis(20)),
                Ok(None) => {
                    let _ = child.kill();
                    let _ = child.wait();
                    return Err(format!("wsl.exe {} timed out", args.join(" ")));
                }
                Err(error) => return Err(format!("unable to wait for wsl.exe: {error}")),
            }
        };
        let stdout = decode(&out.join().unwrap_or_default());
        if !status.success() {
            let stderr = decode(&err.join().unwrap_or_default());
            return Err(format!(
                "wsl.exe {} failed: {}",
                args.join(" "),
                format!("{stderr}\n{stdout}").trim()
            ));
        }
        Ok(stdout)
    }

    fn capture(stream: impl Read) -> Vec<u8> {
        let mut output = Vec::new();
        let _ = stream.take(64 * 1024).read_to_end(&mut output);
        output
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ubuntu(mount_root: &str) -> WslHome {
        WslHome {
            distribution: "Ubuntu".to_string(),
            share: PathBuf::from(r"\\wsl$\Ubuntu\home\ada"),
            mount_root: mount_root.to_string(),
            dirs: ToolDirs::default(),
        }
    }

    #[test]
    fn drive_paths_map_through_the_mount_root() {
        let home = ubuntu("/mnt");
        assert_eq!(
            home.linux_path(Path::new(
                r"C:\Users\Ada Lovelace\.astrlink\bin\astrlink.exe"
            ))
            .as_deref(),
            Some("/mnt/c/Users/Ada Lovelace/.astrlink/bin/astrlink.exe")
        );
        assert_eq!(
            home.linux_path(Path::new(r"\\?\D:\AstrLink\data\"))
                .as_deref(),
            Some("/mnt/d/AstrLink/data")
        );
        assert_eq!(
            home.linux_path(Path::new(r"C:\")).as_deref(),
            Some("/mnt/c")
        );
        assert_eq!(
            ubuntu("").linux_path(Path::new(r"C:\Users\ada")).as_deref(),
            Some("/c/Users/ada")
        );
        assert_eq!(home.linux_path(Path::new(r"\\server\share\x")), None);
        assert_eq!(home.linux_path(Path::new(r"C:relative")), None);
        assert_eq!(home.linux_path(Path::new("/home/ada")), None);
    }

    #[test]
    fn cc_switch_dirs_split_by_distribution() {
        let (local, wsl) = split_dirs(ToolDirs {
            claude: Some(PathBuf::from(
                r"\\wsl.localhost\Ubuntu-26.04\home\yayoi\.claude",
            )),
            codex: Some(PathBuf::from(r"\\wsl$\ubuntu-26.04\opt\codex")),
            grok: Some(PathBuf::from(r"D:\grok")),
            pi: Some(PathBuf::from(r"\\?\UNC\wsl$\Debian\root\.pi\agent")),
            ..Default::default()
        });
        assert_eq!(
            local,
            ToolDirs {
                grok: Some(PathBuf::from(r"D:\grok")),
                ..Default::default()
            }
        );
        assert_eq!(
            wsl,
            vec![
                WslDirs {
                    distribution: "Ubuntu-26.04".to_string(),
                    home: Some(PathBuf::from(r"\\wsl.localhost\Ubuntu-26.04\home\yayoi")),
                    dirs: ToolDirs {
                        claude: Some(PathBuf::from(
                            r"\\wsl.localhost\Ubuntu-26.04\home\yayoi\.claude"
                        )),
                        codex: Some(PathBuf::from(r"\\wsl$\ubuntu-26.04\opt\codex")),
                        ..Default::default()
                    },
                },
                WslDirs {
                    distribution: "Debian".to_string(),
                    home: Some(PathBuf::from(r"\\wsl$\Debian\root")),
                    dirs: ToolDirs {
                        pi: Some(PathBuf::from(r"\\?\UNC\wsl$\Debian\root\.pi\agent")),
                        ..Default::default()
                    },
                },
            ]
        );
    }

    #[test]
    fn share_locations_need_a_wsl_share_and_name_a_home_only_inside_one() {
        assert_eq!(share_location(Path::new(r"\\server\Ubuntu\home\a")), None);
        assert_eq!(share_location(Path::new(r"C:\Users\a\.claude")), None);
        assert_eq!(
            share_location(Path::new(r"\\wsl$\Ubuntu\home\..\etc")),
            None
        );
        assert_eq!(
            share_location(Path::new(r"\\wsl$\Ubuntu\etc\claude")),
            Some(("Ubuntu".to_string(), None))
        );
        assert_eq!(
            share_location(Path::new("//wsl.localhost/Ubuntu/home/a/.codex")),
            Some((
                "Ubuntu".to_string(),
                Some(PathBuf::from(r"\\wsl.localhost\Ubuntu\home\a"))
            ))
        );
    }

    #[test]
    fn share_writes_set_modes_on_the_paths_the_distribution_sees() {
        assert_eq!(
            inside_share(Path::new(
                r"\\wsl$\Ubuntu-24.04\home\ada\.codex\config.toml"
            )),
            Some((
                "Ubuntu-24.04".to_string(),
                "/home/ada/.codex/config.toml".to_string()
            ))
        );
        assert_eq!(
            inside_share(Path::new(r"\\?\UNC\wsl.localhost\Debian\root\.pi\agent")),
            Some(("Debian".to_string(), "/root/.pi/agent".to_string()))
        );
        assert_eq!(inside_share(Path::new(r"C:\Users\ada\.codex")), None);
        assert_eq!(
            inside_share(Path::new(r"\\wsl$\Ubuntu\home\..\etc\passwd")),
            None
        );
        assert_eq!(
            inside_share(Path::new(r"\\wsl$\Ubuntu\home\ada lovelace\.a.tmp-7-0"))
                .map(|(_, path)| path),
            Some("/home/ada lovelace/.a.tmp-7-0".to_string())
        );

        assert_eq!(file_mode("644\n").as_deref(), Some("644"));
        assert_eq!(file_mode(" 4755\r\n").as_deref(), Some("4755"));
        assert_eq!(file_mode("0").as_deref(), Some("0"));
        for output in ["", "64x", "12345", "-rw-r--r--"] {
            assert_eq!(file_mode(output), None, "{output}");
        }
    }

    #[test]
    fn mount_root_comes_from_wsl_conf() {
        assert_eq!(parse_mount_root(""), "/mnt");
        assert_eq!(
            parse_mount_root(
                "[boot]\nsystemd=true\n[automount]\nenabled = true\nroot = /windir/ # drives\n"
            ),
            "/windir"
        );
        assert_eq!(parse_mount_root("[automount]\nroot = \"/\"\n"), "");
        assert_eq!(parse_mount_root("[network]\nroot = /x/\n"), "/mnt");
        assert_eq!(parse_mount_root("[Automount]\nRoot=relative\n"), "/mnt");
    }

    #[test]
    fn mirrored_networking_shares_localhost() {
        let host = std::env::temp_dir().join(format!("astrlink-wslconfig-{}", std::process::id()));
        fs::create_dir_all(&host).unwrap();
        assert!(!localhost_shared(&host));
        for (conf, shared) in [
            ("[wsl2]\nmemory=8GB\nnetworkingMode=mirrored\n", true),
            ("[experimental]\nnetworkingMode = \"Mirrored\"\n", true),
            ("[wsl2]\nnetworkingMode=nat\n", false),
            ("[boot]\nnetworkingMode=mirrored\n", false),
            ("[wsl2]\n# networkingMode=mirrored\n", false),
        ] {
            fs::write(host.join(".wslconfig"), conf).unwrap();
            assert_eq!(localhost_shared(&host), shared, "{conf}");
        }
        fs::remove_dir_all(host).unwrap();
    }

    #[test]
    fn decodes_utf16_and_utf8_listings() {
        let utf16 = "\u{feff}Ubuntu\r\nDebian\r\n"
            .encode_utf16()
            .flat_map(u16::to_le_bytes)
            .collect::<Vec<_>>();
        assert_eq!(decode(&utf16), "Ubuntu\r\nDebian\r\n");
        assert_eq!(decode(b"Ubuntu-22.04\n"), "Ubuntu-22.04\n");
    }

    #[test]
    fn listing_skips_docker_and_unusable_names() {
        assert_eq!(
            parse_names(
                "Ubuntu\r\ndocker-desktop\r\ndocker-desktop-data\r\n\r\nArch\\x\r\nDebian\r\n"
            ),
            vec!["Ubuntu".to_string(), "Debian".to_string()]
        );
    }

    #[test]
    fn share_path_reaches_the_home_through_the_wsl_share() {
        assert_eq!(
            share_path("Ubuntu", "/home/ada\n"),
            Some(PathBuf::from(r"\\wsl$\Ubuntu\home\ada"))
        );
        assert_eq!(
            share_path("Ubuntu", "/root/"),
            Some(PathBuf::from(r"\\wsl$\Ubuntu\root"))
        );
        assert_eq!(share_path("Ubuntu", "relative"), None);
        assert_eq!(share_path("Ubuntu", "/home/../etc"), None);
    }
}
