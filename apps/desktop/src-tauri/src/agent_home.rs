//! Homes whose agent tools and clients AstrLink configures: the desktop
//! user's own, and homes in WSL distributions edited through their share.
//! Tool directories follow CC Switch's settings when it moved them.

use std::{
    iter,
    ops::Deref,
    path::{Path, PathBuf},
};

use crate::{
    cc_switch,
    wsl::{self, Reach, WslHome},
};

/// Configuration directories that moved away from their default place in a
/// home, as CC Switch's directory settings record them. Cursor has none.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ToolDirs {
    pub claude: Option<PathBuf>,
    pub codex: Option<PathBuf>,
    pub grok: Option<PathBuf>,
    /// Pi's agent directory, `~/.pi/agent` by default.
    pub pi: Option<PathBuf>,
}

static DEFAULT_DIRS: ToolDirs = ToolDirs {
    claude: None,
    codex: None,
    grok: None,
    pi: None,
};

/// The desktop user's home with `dirs`, then each WSL home.
pub fn homes<'a>(
    host: &'a Path,
    dirs: &'a ToolDirs,
    wsl: &'a [WslHome],
) -> impl Iterator<Item = Home<'a>> {
    iter::once(Home {
        host,
        wsl: None,
        dirs,
    })
    .chain(wsl.iter().map(move |wsl| Home::in_wsl(host, wsl)))
}

/// The desktop user's home and the WSL homes found beside it.
pub struct Homes {
    pub host: PathBuf,
    /// Where the desktop user's tools keep their configuration.
    pub dirs: ToolDirs,
    pub wsl: Vec<WslHome>,
    /// Registered WSL distributions that were not checked.
    pub unchecked: Vec<String>,
}

impl Homes {
    /// Finds the WSL homes `reach` selects. Tool directories follow CC
    /// Switch's settings, which also locate a WSL home without asking WSL.
    pub fn find(host: PathBuf, reach: Reach) -> Result<Self, String> {
        let (dirs, wsl_dirs) = wsl::split_dirs(cc_switch::config_dirs(&host));
        let (wsl, unchecked) = wsl::homes(&wsl_dirs, reach)?;
        Ok(Self {
            host,
            dirs,
            wsl,
            unchecked,
        })
    }

    pub fn iter(&self) -> impl Iterator<Item = Home<'_>> {
        homes(&self.host, &self.dirs, &self.wsl)
    }

    /// The desktop user's home for `None`, or the named distribution's.
    pub fn take(self, distribution: Option<&str>) -> Option<OwnedHome> {
        let wsl = match distribution {
            None => None,
            Some(name) => Some(
                self.wsl
                    .into_iter()
                    .find(|wsl| wsl.distribution.eq_ignore_ascii_case(name))?,
            ),
        };
        Some(OwnedHome {
            host: self.host,
            dirs: self.dirs,
            wsl,
        })
    }
}

/// One home, owned for work that outlives the lookup.
pub struct OwnedHome {
    pub host: PathBuf,
    pub dirs: ToolDirs,
    pub wsl: Option<WslHome>,
}

impl OwnedHome {
    pub fn home(&self) -> Home<'_> {
        match &self.wsl {
            None => Home {
                host: &self.host,
                wsl: None,
                dirs: &self.dirs,
            },
            Some(wsl) => Home::in_wsl(&self.host, wsl),
        }
    }
}

/// One home. It derefs to the directory AstrLink edits.
#[derive(Clone, Copy)]
pub struct Home<'a> {
    /// The desktop user's home, which holds the CLI tools in every home run.
    pub(crate) host: &'a Path,
    pub(crate) wsl: Option<&'a WslHome>,
    pub(crate) dirs: &'a ToolDirs,
}

impl<'a> Home<'a> {
    /// The desktop user's home with every tool in its default place.
    #[cfg_attr(not(test), allow(dead_code))]
    pub fn local(host: &'a Path) -> Self {
        Self {
            host,
            wsl: None,
            dirs: &DEFAULT_DIRS,
        }
    }

    pub fn in_wsl(host: &'a Path, wsl: &'a WslHome) -> Self {
        Self {
            host,
            wsl: Some(wsl),
            dirs: &wsl.dirs,
        }
    }

    pub fn distribution(self) -> Option<&'a str> {
        self.wsl.map(|wsl| wsl.distribution.as_str())
    }

    /// A desktop path as this home's tools address it.
    pub(crate) fn seen(self, path: &Path) -> Option<String> {
        match self.wsl {
            None => path.to_str().map(str::to_string),
            Some(wsl) => wsl.linux_path(path),
        }
    }

    pub(crate) fn shown(self, path: &Path) -> String {
        self.seen(path)
            .unwrap_or_else(|| path.display().to_string())
    }

    /// Whether this home's tools take Windows paths and quoting.
    pub(crate) fn windows(self) -> bool {
        self.wsl.is_none() && cfg!(windows)
    }

    pub(crate) fn claude_dir(self) -> PathBuf {
        self.dirs
            .claude
            .clone()
            .unwrap_or_else(|| self.join(".claude"))
    }

    /// Claude Code keeps `.claude.json` beside its default directory and
    /// inside a moved one.
    pub(crate) fn claude_json(self) -> PathBuf {
        match &self.dirs.claude {
            Some(dir) if *dir != self.join(".claude") => dir.join(".claude.json"),
            _ => self.join(".claude.json"),
        }
    }

    pub(crate) fn claude_detected(self) -> bool {
        self.claude_dir().is_dir() || self.claude_json().is_file()
    }

    pub(crate) fn codex_dir(self) -> PathBuf {
        self.dirs
            .codex
            .clone()
            .unwrap_or_else(|| self.join(".codex"))
    }

    pub(crate) fn grok_dir(self) -> PathBuf {
        self.dirs.grok.clone().unwrap_or_else(|| self.join(".grok"))
    }

    pub(crate) fn pi_agent_dir(self) -> PathBuf {
        self.dirs
            .pi
            .clone()
            .unwrap_or_else(|| self.join(".pi").join("agent"))
    }

    pub(crate) fn pi_detected(self) -> bool {
        match &self.dirs.pi {
            Some(dir) => dir.is_dir(),
            None => self.join(".pi").is_dir(),
        }
    }
}

impl Deref for Home<'_> {
    type Target = Path;

    fn deref(&self) -> &Path {
        self.wsl.map_or(self.host, |wsl| wsl.share.as_path())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn claude_json_sits_beside_the_default_dir_and_inside_a_moved_one() {
        let home = Path::new("/home/ada");
        assert_eq!(Home::local(home).claude_json(), home.join(".claude.json"));
        let default = ToolDirs {
            claude: Some(home.join(".claude")),
            ..Default::default()
        };
        let moved = ToolDirs {
            claude: Some(home.join("claude")),
            ..Default::default()
        };
        let at = |dirs| Home {
            host: home,
            wsl: None,
            dirs,
        };
        assert_eq!(at(&default).claude_json(), home.join(".claude.json"));
        assert_eq!(
            at(&moved).claude_json(),
            home.join("claude").join(".claude.json")
        );
    }
}
