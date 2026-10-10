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
    place(dirs.pi, |dirs| &mut dirs.pi);
    (local, wsl)
}

/// The distribution a `\\wsl$` or `\\wsl.localhost` path lies in, and the
/// home it lies in: `/home/<user>` or `/root`, reached through the same share.
fn share_location(path: &Path) -> Option<(String, Option<PathBuf>)> {
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
    let parts = parts.collect::<Vec<_>>();
    if parts.contains(&"..") {
        return None;
    }
    let home = match parts.as_slice() {
        ["home", user, ..] => Some(format!(r"\\{server}\{distribution}\home\{user}")),
        ["root", ..] => Some(format!(r"\\{server}\{distribution}\root")),
        _ => None,
    };
    Some((distribution.to_string(), home.map(PathBuf::from)))
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
    let conf = PathBuf::from(format!(r"\\wsl$\{distribution}\etc\wsl.conf"));
    Ok(WslHome {
        distribution: distribution.to_string(),
        share,
        mount_root: parse_mount_root(&fs::read_to_string(conf).unwrap_or_default()),
        dirs: configured
            .map(|entry| entry.dirs.clone())
            .unwrap_or_default(),
    })
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

    /// Runs wsl.exe without a console window and returns its decoded output.
    pub fn run(args: &[&str], timeout: Duration) -> Result<String, String> {
        let system = std::env::var_os("SystemRoot")
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from(r"C:\Windows"));
        let program = system.join("System32").join("wsl.exe");
        if !program.is_file() {
            return Err("WSL is not installed".to_string());
        }
        let mut child = Command::new(program)
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
