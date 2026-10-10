use std::{
    collections::{BTreeMap, BTreeSet},
    fs, io,
    path::{Path, PathBuf},
    time::{SystemTime, UNIX_EPOCH},
};

use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};

use crate::agent_home::{self, Home, ToolDirs};
use crate::control_session::{astrlink_home, session_path};
use crate::host_files::{self, read_optional, remove_path};
use crate::wsl::WslHome;

const RECEIPT_VERSION: u32 = 2;
const HOST_GUARDS_VERSION: u32 = 2;
const MANAGED_FILES_NAME: &str = ".astrlink-managed-files.json";
const CODEX_GUARD_BEGIN: &str = "<!-- astrlink-debug:begin -->";
const CODEX_GUARD_END: &str = "<!-- astrlink-debug:end -->";
const CODEX_RULES_MARKER: &str = "# astrlink-debug: managed by AstrLink";
/// Stands for the CLI in the skill text; installs write its absolute path.
const CLI_PLACEHOLDER: &str = "{{ASTRLINK_CLI}}";
/// The server name the MCP-based installer registered; only the migration
/// that removes it still looks for it.
const LEGACY_MCP_SERVER_NAME: &str = "astrlink";

struct SkillFile {
    relative: &'static str,
    contents: &'static str,
}

/// A skill AstrLink can install. Only a skill that drives the CLI brings the
/// CLI, its host access rules, and the host guards with it.
struct SkillBundle {
    name: &'static str,
    version: &'static str,
    files: &'static [SkillFile],
    needs_cli: bool,
}

const DEBUG_BUNDLE: SkillBundle = SkillBundle {
    name: "astrlink-debug",
    version: "0.3.0",
    files: &[
        SkillFile {
            relative: "SKILL.md",
            contents: include_str!("../../../../agent-bundle/astrlink-debug/SKILL.md"),
        },
        SkillFile {
            relative: "references/trajectory.md",
            contents: include_str!(
                "../../../../agent-bundle/astrlink-debug/references/trajectory.md"
            ),
        },
        SkillFile {
            relative: "manifest.json",
            contents: include_str!("../../../../agent-bundle/astrlink-debug/manifest.json"),
        },
    ],
    needs_cli: true,
};

// Hosts may send skill names and descriptions upstream with every request, so
// this bundle stays free of AstrLink branding and of the CLI placeholder.
const PLACEHOLDER_BUNDLE: SkillBundle = SkillBundle {
    name: "redaction-placeholders",
    version: "0.1.0",
    files: &[
        SkillFile {
            relative: "SKILL.md",
            contents: include_str!("../../../../agent-bundle/redaction-placeholders/SKILL.md"),
        },
        SkillFile {
            relative: "references/shapes.md",
            contents: include_str!(
                "../../../../agent-bundle/redaction-placeholders/references/shapes.md"
            ),
        },
        SkillFile {
            relative: "manifest.json",
            contents: include_str!("../../../../agent-bundle/redaction-placeholders/manifest.json"),
        },
    ],
    needs_cli: false,
};

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq, PartialOrd, Ord)]
pub enum AgentSkillId {
    #[serde(rename = "astrlink-debug")]
    AstrlinkDebug,
    #[serde(rename = "redaction-placeholders")]
    RedactionPlaceholders,
}

impl AgentSkillId {
    fn all() -> [Self; 2] {
        [Self::AstrlinkDebug, Self::RedactionPlaceholders]
    }

    fn bundle(self) -> &'static SkillBundle {
        match self {
            Self::AstrlinkDebug => &DEBUG_BUNDLE,
            Self::RedactionPlaceholders => &PLACEHOLDER_BUNDLE,
        }
    }
}

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum AgentToolId {
    Cursor,
    Claude,
    Codex,
    Grok,
    Pi,
}

/// How a host can be kept away from AstrLink's local files. Only Claude Code
/// has an enforced mechanism; the others rely on prompt text.
#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum AgentGuardKind {
    /// `permissions.deny` rules in `~/.claude/settings.json`.
    DenyRules,
    /// A marked section in the host's global instructions file.
    Instructions,
    /// No verified host mechanism; the skill text is the only guidance.
    SkillOnly,
}

/// How a host lets agents run the AstrLink CLI without asking every time.
#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum AgentCliAccessKind {
    /// `permissions.allow` rules in `~/.claude/settings.json`.
    AllowRules,
    /// A Codex rules file. Codex's sandbox blocks the control socket, so the
    /// rule also runs the CLI outside it.
    ExecPolicy,
    /// No verified host mechanism; the host asks the user on first use.
    Prompt,
    /// The host runs commands without asking, so there is nothing to allow.
    Unrestricted,
}

#[derive(Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct AgentSkillStatus {
    pub id: AgentSkillId,
    pub installed: bool,
    /// What installing this skill for the tool writes, apart from the
    /// status-wide shared paths.
    pub preview_paths: Vec<String>,
}

/// CLI access and guards come with `astrlink-debug`; a skill-only install
/// leaves them untouched.
#[derive(Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct AgentToolStatus {
    pub id: AgentToolId,
    /// The WSL distribution the tool runs in; `None` for the desktop user's.
    pub wsl: Option<String>,
    pub detected: bool,
    pub skills: Vec<AgentSkillStatus>,
    pub cli_access: AgentCliAccessKind,
    pub cli_access_installed: bool,
    pub guard: AgentGuardKind,
    pub guard_installed: bool,
}

#[derive(Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct AgentInstallStatus {
    pub cli_binary: bool,
    pub tools: Vec<AgentToolStatus>,
    pub shared_paths: Vec<String>,
    /// WSL distributions whose tools went unchecked because they were not
    /// running; checking never starts one.
    pub wsl_unchecked: Vec<String>,
}

/// A tool selected for install, in the desktop user's home or in a WSL
/// distribution's.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct AgentToolTarget {
    pub id: AgentToolId,
    #[serde(default)]
    pub wsl: Option<String>,
}

#[derive(Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct ReceiptSkill {
    pub id: AgentSkillId,
    pub version: String,
}

/// What the latest install wrote.
#[derive(Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct InstallReceipt {
    pub version: u32,
    pub skills: Vec<ReceiptSkill>,
    pub installed_at_unix: u64,
    /// `None` when no selected skill drives the CLI.
    pub cli_binary: Option<String>,
    pub files: Vec<String>,
    /// WSL distributions holding an install, so uninstall and startup sync
    /// reach them. Only the desktop user's receipt lists them.
    #[serde(default)]
    pub wsl: Vec<String>,
}

pub struct InstallContext {
    pub home: PathBuf,
    pub cli_source: PathBuf,
    /// Core's data directory, denied to hosts that support deny rules.
    pub data_directory: Option<PathBuf>,
    /// The desktop's raw key pin file, denied like the data directory. It
    /// lives in the config directory, apart from the data on Linux, and is
    /// `None` where the pins live in the keychain.
    pub raw_key_pins: Option<PathBuf>,
    /// Where the tools in `home` keep their configuration.
    pub dirs: ToolDirs,
    /// WSL distributions configured alongside `home`. Their tools run the CLI
    /// installed in `home` through WSL interop.
    pub wsl: Vec<WslHome>,
}

impl InstallContext {
    fn local(&self) -> Home<'_> {
        self.homes()
            .next()
            .expect("the desktop user's home comes first")
    }

    pub fn homes(&self) -> impl Iterator<Item = Home<'_>> {
        agent_home::homes(&self.home, &self.dirs, &self.wsl)
    }

    fn home_for(&self, distribution: Option<&str>) -> Option<Home<'_>> {
        self.homes()
            .find(|home| home.distribution() == distribution)
    }
}

/// What AstrLink wrote into host configuration outside its own files, so
/// uninstall removes exactly that and nothing the user added.
#[derive(Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
struct HostGuardRecord {
    version: u32,
    /// Claude Code deny rules; the key predates the allow rules.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    claude: Option<ClaudeRuleRecord>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    claude_allow: Option<ClaudeRuleRecord>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    codex: Option<CodexInstructionsRecord>,
    /// AstrLink wrote the Codex rules file, so startup does not re-create
    /// it after the user deletes it.
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    codex_rules: bool,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
pub struct ClaudeRuleRecord {
    /// Rule set last applied; startup only rewrites the file when it changes.
    pub rules: Vec<String>,
    /// Rules AstrLink inserted. Rules the user already had are never listed.
    pub managed: Vec<String>,
    pub created_file: bool,
    pub created_permissions: bool,
    /// AstrLink created the rule list itself (`deny` or `allow`).
    #[serde(alias = "created_deny")]
    pub created_list: bool,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
struct CodexInstructionsRecord {
    created_file: bool,
}

impl AgentToolId {
    fn all() -> [Self; 5] {
        [
            Self::Cursor,
            Self::Claude,
            Self::Codex,
            Self::Grok,
            Self::Pi,
        ]
    }

    /// Codex and Pi both discover the shared `~/.agents/skills` directory.
    fn uses_shared_skills(self) -> bool {
        matches!(self, Self::Codex | Self::Pi)
    }
}

pub fn status(context: &InstallContext) -> AgentInstallStatus {
    let tools = context
        .homes()
        .flat_map(|home| {
            AgentToolId::all()
                .into_iter()
                .map(move |id| tool_status(context, home, id))
                // A WSL distribution lists only the tools it has.
                .filter(move |tool| {
                    home.wsl.is_none() || tool.detected || tool.cli_access_installed
                })
        })
        .collect::<Vec<_>>();
    AgentInstallStatus {
        shared_paths: vec![display_path(&receipt_path(&context.home)).unwrap_or_default()],
        cli_binary: cli_binary_dest(&context.home).is_file(),
        tools,
        wsl_unchecked: Vec::new(),
    }
}

pub fn install(
    context: &InstallContext,
    skill_ids: &[AgentSkillId],
    tools: &[AgentToolTarget],
) -> Result<InstallReceipt, String> {
    let _lock = host_files::lock();
    if skill_ids.is_empty() {
        return Err("select at least one skill to install".to_string());
    }
    if tools.is_empty() {
        return Err("select at least one agent tool to install".to_string());
    }
    for tool in tools {
        let Some(home) = context.home_for(tool.wsl.as_deref()) else {
            return Err(format!(
                "WSL distribution {} is no longer available",
                tool.wsl.as_deref().unwrap_or_default()
            ));
        };
        if !tool_detected(home, tool.id) {
            return Err(format!(
                "selected agent tool {:?} is no longer detected",
                tool.id
            ));
        }
    }
    let needs_cli = skill_ids.iter().any(|id| id.bundle().needs_cli);
    if needs_cli && !context.cli_source.is_file() {
        return Err(
            "unable to locate astrlink-cli. Build desktop sidecars first (bun run sidecar:build)."
                .to_string(),
        );
    }
    let mut files = Vec::new();
    let mut cli_binary = None;
    if needs_cli {
        let cli_dest = cli_binary_dest(&context.home);
        copy_cli_binary(&context.cli_source, &cli_dest)?;
        let path = display_path(&cli_dest)?;
        files.push(path.clone());
        cli_binary = Some(path);
        remove_legacy_mcp(&context.home)?;
    }

    let skills = AgentSkillId::all()
        .into_iter()
        .filter(|id| skill_ids.contains(id))
        .collect::<Vec<_>>();
    let mut distributions = recorded_wsl(&context.home);
    for home in context.homes() {
        let tool_ids = AgentToolId::all()
            .into_iter()
            .filter(|id| {
                tools
                    .iter()
                    .any(|tool| tool.id == *id && tool.wsl.as_deref() == home.distribution())
            })
            .collect::<Vec<_>>();
        if tool_ids.is_empty() {
            continue;
        }
        let mut home_files = Vec::new();
        for id in &tool_ids {
            for skill in &skills {
                home_files.push(install_tool(home, skill.bundle(), *id)?);
            }
        }
        if needs_cli {
            home_files.extend(install_host_guards(context, home, &tool_ids)?);
        }
        // A distribution keeps its own receipt, so it syncs and uninstalls
        // like the desktop user's home.
        if let Some(distribution) = home.distribution() {
            let receipt = write_receipt(
                &home,
                &skills,
                cli_binary.clone(),
                home_files.clone(),
                Vec::new(),
            )?;
            home_files = receipt.files;
            distributions.push(distribution.to_string());
        }
        files.extend(home_files);
    }
    distributions.sort();
    distributions.dedup();
    write_receipt(&context.home, &skills, cli_binary, files, distributions)
}

/// Records what an install wrote, the receipt itself included.
fn write_receipt(
    home: &Path,
    skills: &[AgentSkillId],
    cli_binary: Option<String>,
    mut files: Vec<String>,
    wsl: Vec<String>,
) -> Result<InstallReceipt, String> {
    let path = receipt_path(home);
    deduplicate_paths(&mut files);
    files.push(display_path(&path)?);
    let receipt = InstallReceipt {
        version: RECEIPT_VERSION,
        skills: skills
            .iter()
            .map(|id| ReceiptSkill {
                id: *id,
                version: id.bundle().version.to_string(),
            })
            .collect(),
        installed_at_unix: unix_now(),
        cli_binary,
        files,
        wsl,
    };
    write_json_file(&path, &receipt)?;
    Ok(receipt)
}

/// The WSL distributions the desktop user's receipt lists.
pub fn recorded_wsl(home: &Path) -> Vec<String> {
    let Ok(Some(raw)) = read_optional(&receipt_path(home)) else {
        return Vec::new();
    };
    serde_json::from_str::<Value>(&raw)
        .ok()
        .and_then(|receipt| receipt.get("wsl").cloned())
        .and_then(|wsl| serde_json::from_value(wsl).ok())
        .unwrap_or_default()
}

/// Removes every install. WSL distributions go first, so a failure leaves
/// the desktop user's receipt naming them for a retry.
pub fn uninstall(context: &InstallContext) -> Result<(), String> {
    let _lock = host_files::lock();
    for wsl in &context.wsl {
        let home = Home::in_wsl(&context.home, wsl);
        uninstall_home(home)?;
        remove_path(&receipt_path(&home))?;
    }
    uninstall_home(context.local())?;
    remove_legacy_mcp(&context.home)?;
    remove_path(&cli_binary_dest(&context.home))?;
    remove_path(&receipt_path(&context.home))?;
    Ok(())
}

fn uninstall_home(home: Home) -> Result<(), String> {
    for skill in AgentSkillId::all() {
        for id in AgentToolId::all() {
            uninstall_tool(home, skill.bundle(), id)?;
        }
    }
    uninstall_host_guards(home)?;
    for skill in AgentSkillId::all() {
        let bundle = skill.bundle();
        let canonical = canonical_skill_dir(&home, bundle);
        if is_ours_skill(&canonical, &canonical, bundle) {
            remove_path(&canonical)?;
        }
    }
    Ok(())
}

pub fn sync_installed_skills(home: Home) -> Result<(), String> {
    let _lock = host_files::lock();
    if !receipt_path(&home).is_file() {
        return Ok(());
    }
    for skill in AgentSkillId::all() {
        let bundle = skill.bundle();
        migrate_legacy_codex_skill(home, bundle)?;
        let canonical = canonical_skill_dir(&home, bundle);
        if is_ours_skill(&canonical, &canonical, bundle) {
            write_skill_tree(home, bundle, &canonical)?;
        }
        for id in AgentToolId::all() {
            if id.uses_shared_skills() {
                continue;
            }
            let dest = tool_skill_dir(home, bundle, id);
            if is_ours_skill(&dest, &canonical, bundle) {
                write_skill_tree(home, bundle, &dest)?;
            }
        }
    }
    Ok(())
}

/// Keeps an existing install's CLI current and retires what the MCP-based
/// installer wrote. A missing sidecar (a dev build without one) only skips
/// the copy, and defers upgrading an MCP-era receipt until a CLI exists. A
/// skill-only install never gains a CLI here.
pub fn sync_installed_cli(context: &InstallContext) -> Result<(), String> {
    let _lock = host_files::lock();
    let Some(receipt) = read_optional(&receipt_path(&context.home))? else {
        return Ok(());
    };
    let migrated = remove_legacy_mcp(&context.home);
    let cli_dest = cli_binary_dest(&context.home);
    if context.cli_source.is_file() && (cli_dest.is_file() || receipt_wants_cli(&receipt)) {
        copy_cli_binary(&context.cli_source, &cli_dest)?;
    }
    migrated?;
    if cli_dest.is_file() {
        upgrade_legacy_receipt(context, &receipt)?;
    }
    Ok(())
}

/// Every MCP-era receipt came with a CLI-driven skill; a current receipt
/// names the CLI only when the install copied it.
fn receipt_wants_cli(raw: &str) -> bool {
    serde_json::from_str::<Value>(raw).is_ok_and(|receipt| {
        receipt.get("version").and_then(Value::as_u64) < Some(u64::from(RECEIPT_VERSION))
            || receipt.get("cli_binary").is_some_and(Value::is_string)
    })
}

/// Rewrites a receipt the MCP-based installer wrote. The hosts it
/// registered lost their server entries, so Claude Code and Codex get the
/// CLI access and guards a reinstall adds; without them Codex's sandbox
/// blocks the CLI.
fn upgrade_legacy_receipt(context: &InstallContext, raw: &str) -> Result<(), String> {
    let Ok(legacy) = serde_json::from_str::<Value>(raw) else {
        return Ok(());
    };
    if legacy.get("version").and_then(Value::as_u64) >= Some(u64::from(RECEIPT_VERSION)) {
        return Ok(());
    }
    let home = context.home.as_path();
    let listed = legacy
        .get("files")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(Value::as_str)
        .map(str::to_string)
        .collect::<Vec<_>>();
    let legacy_configs = AgentToolId::all()
        .into_iter()
        .filter_map(|id| Some((id, legacy_mcp_config_path(home, id)?)))
        .map(|(id, path)| Ok((id, display_path(&path)?)))
        .collect::<Result<Vec<_>, String>>()?;
    let tools = legacy_configs
        .iter()
        .filter(|(id, path)| {
            matches!(id, AgentToolId::Claude | AgentToolId::Codex)
                && tool_detected(context.local(), *id)
                && listed.contains(path)
        })
        .map(|(id, _)| *id)
        .collect::<Vec<_>>();
    let receipt_path = receipt_path(home);
    let mut retired = legacy_configs
        .into_iter()
        .map(|(_, path)| path)
        .collect::<BTreeSet<_>>();
    retired.insert(display_path(&legacy_mcp_binary_dest(home))?);
    retired.insert(display_path(&receipt_path)?);

    let cli_binary = display_path(&cli_binary_dest(home))?;
    let mut files = vec![cli_binary.clone()];
    files.extend(
        listed
            .into_iter()
            .filter(|path| !retired.contains(path) && Path::new(path).exists()),
    );
    files.extend(install_host_guards(context, Home::local(home), &tools)?);
    files.push(display_path(&receipt_path)?);
    deduplicate_paths(&mut files);
    write_json_file(
        &receipt_path,
        &InstallReceipt {
            version: RECEIPT_VERSION,
            skills: vec![ReceiptSkill {
                id: AgentSkillId::AstrlinkDebug,
                version: DEBUG_BUNDLE.version.to_string(),
            }],
            installed_at_unix: legacy
                .get("installed_at_unix")
                .and_then(Value::as_u64)
                .unwrap_or_else(unix_now),
            cli_binary: Some(cli_binary),
            files,
            wsl: Vec::new(),
        },
    )
}

pub fn resolve_sidecar_binary(name: &str) -> Result<PathBuf, String> {
    let suffix = if cfg!(windows) { ".exe" } else { "" };
    let triple = host_target_triple();
    let exe = std::env::current_exe().map_err(|error| error.to_string())?;
    let exe_dir = exe
        .parent()
        .ok_or_else(|| "AstrLink executable has no parent directory".to_string())?;
    let manifest_binaries = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("binaries");
    let candidates = [
        exe_dir.join(format!("{name}{suffix}")),
        exe_dir.join(format!("{name}-{triple}{suffix}")),
        manifest_binaries.join(format!("{name}-{triple}{suffix}")),
    ];
    for path in candidates {
        if path.is_file() {
            return Ok(path);
        }
    }
    Err(format!(
        "unable to locate {name} sidecar next to the desktop app or in src-tauri/binaries"
    ))
}

fn host_target_triple() -> &'static str {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        "aarch64-apple-darwin"
    }
    #[cfg(all(target_os = "macos", target_arch = "x86_64"))]
    {
        "x86_64-apple-darwin"
    }
    #[cfg(all(target_os = "linux", target_arch = "x86_64"))]
    {
        "x86_64-unknown-linux-gnu"
    }
    #[cfg(all(target_os = "linux", target_arch = "aarch64"))]
    {
        "aarch64-unknown-linux-gnu"
    }
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    {
        "x86_64-pc-windows-msvc"
    }
    #[cfg(all(target_os = "windows", target_arch = "aarch64"))]
    {
        "aarch64-pc-windows-msvc"
    }
}

fn canonical_skill_dir(home: &Path, bundle: &SkillBundle) -> PathBuf {
    home.join(".agents").join("skills").join(bundle.name)
}

fn legacy_codex_skill_dir(home: &Path, bundle: &SkillBundle) -> PathBuf {
    home.join(".codex").join("skills").join(bundle.name)
}

// Codex discovers the shared .agents directory itself. Older installers also
// wrote a .codex copy, causing both descriptions to enter the prompt. Keep the
// shared copy active and archive the owned duplicate outside skill search roots
// so local edits and extra files remain recoverable.
fn migrate_legacy_codex_skill(home: Home, bundle: &SkillBundle) -> Result<(), String> {
    let legacy = legacy_codex_skill_dir(&home, bundle);
    let canonical = canonical_skill_dir(&home, bundle);
    if !is_ours_skill(&legacy, &canonical, bundle) {
        return Ok(());
    }
    let is_link = points_at_canonical(&legacy, &canonical);
    if !is_link {
        match canonical.symlink_metadata() {
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                fs::create_dir_all(canonical.parent().unwrap())
                    .map_err(|error| format!("unable to create shared skill directory: {error}"))?;
                return fs::rename(&legacy, &canonical)
                    .map_err(|error| format!("unable to migrate {}: {error}", legacy.display()));
            }
            Err(error) => {
                return Err(format!(
                    "unable to inspect {}: {error}",
                    canonical.display()
                ));
            }
            Ok(_) => {}
        }
    }
    // Refuse a foreign shared directory and ensure a usable replacement exists
    // before removing either a real duplicate or an old (possibly broken) link.
    write_canonical_skill(home, bundle)?;
    if is_link {
        return remove_path(&legacy);
    }

    let backups = astrlink_home(&home).join("agent-skill-backups");
    fs::create_dir_all(&backups)
        .map_err(|error| format!("unable to create {}: {error}", backups.display()))?;
    let mut index = 0_u64;
    loop {
        let backup = backups.join(format!("codex-{}-{index}", unix_now()));
        match fs::create_dir(&backup) {
            Ok(()) => {
                let dest = backup.join(bundle.name);
                fs::rename(&legacy, &dest).map_err(|error| {
                    format!(
                        "unable to archive {} to {}: {error}",
                        legacy.display(),
                        dest.display()
                    )
                })?;
                eprintln!(
                    "migrated duplicate AstrLink Codex skill to {}",
                    dest.display()
                );
                return Ok(());
            }
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => index += 1,
            Err(error) => {
                return Err(format!("unable to create {}: {error}", backup.display()));
            }
        }
    }
}

fn receipt_path(home: &Path) -> PathBuf {
    astrlink_home(home).join("agent-installs.json")
}

fn cli_binary_dest(home: &Path) -> PathBuf {
    let name = if cfg!(windows) {
        "astrlink.exe"
    } else {
        "astrlink"
    };
    astrlink_home(home).join("bin").join(name)
}

fn legacy_mcp_binary_dest(home: &Path) -> PathBuf {
    let name = if cfg!(windows) {
        "astrlink-mcp.exe"
    } else {
        "astrlink-mcp"
    };
    astrlink_home(home).join("bin").join(name)
}

fn tool_detected(home: Home, id: AgentToolId) -> bool {
    match id {
        AgentToolId::Cursor => home.join(".cursor").is_dir(),
        AgentToolId::Claude => home.claude_detected(),
        AgentToolId::Codex => home.codex_dir().is_dir(),
        AgentToolId::Grok => home.grok_dir().is_dir(),
        AgentToolId::Pi => home.pi_detected(),
    }
}

fn tool_skill_dir(home: Home, bundle: &SkillBundle, id: AgentToolId) -> PathBuf {
    match id {
        AgentToolId::Cursor => home.join(".cursor").join("skills").join(bundle.name),
        AgentToolId::Claude => home.claude_dir().join("skills").join(bundle.name),
        AgentToolId::Grok => home.grok_dir().join("skills").join(bundle.name),
        // Pi's own skill directory moves with PI_CODING_AGENT_DIR, which the
        // desktop cannot see; the shared one does not.
        AgentToolId::Codex | AgentToolId::Pi => canonical_skill_dir(&home, bundle),
    }
}

/// Where the MCP-based installer registered its server for each host. It
/// never supported Pi.
fn legacy_mcp_config_path(home: &Path, id: AgentToolId) -> Option<PathBuf> {
    match id {
        AgentToolId::Cursor => Some(home.join(".cursor").join("mcp.json")),
        AgentToolId::Claude => Some(home.join(".claude.json")),
        AgentToolId::Codex => Some(home.join(".codex").join("config.toml")),
        AgentToolId::Grok => Some(home.join(".grok").join("config.toml")),
        AgentToolId::Pi => None,
    }
}

fn tool_status(context: &InstallContext, home: Home, id: AgentToolId) -> AgentToolStatus {
    let detected = tool_detected(home, id);
    AgentToolStatus {
        id,
        wsl: home.distribution().map(str::to_string),
        detected,
        skills: AgentSkillId::all()
            .into_iter()
            .map(|skill| skill_status(home, skill, id, detected))
            .collect(),
        cli_access: tool_cli_access_kind(id),
        cli_access_installed: cli_access_present(home, id),
        guard: tool_guard_kind(id),
        guard_installed: guard_present(context, home, id),
    }
}

fn skill_status(
    home: Home,
    skill: AgentSkillId,
    id: AgentToolId,
    detected: bool,
) -> AgentSkillStatus {
    let bundle = skill.bundle();
    let dir = tool_skill_dir(home, bundle, id);
    let mut preview_paths = vec![display_path(&dir).unwrap_or_default()];
    if bundle.needs_cli {
        preview_paths.push(display_path(&cli_binary_dest(home.host)).unwrap_or_default());
        if let Some(access) = tool_cli_access_path(home, id) {
            preview_paths.push(display_path(&access).unwrap_or_default());
        }
        if let Some(guard) = tool_guard_path(home, id) {
            preview_paths.push(display_path(&guard).unwrap_or_default());
            preview_paths.push(display_path(&host_guards_path(&home)).unwrap_or_default());
        }
    }
    // The shared paths name only the desktop user's receipt.
    if home.wsl.is_some() {
        preview_paths.push(display_path(&receipt_path(&home)).unwrap_or_default());
    }
    deduplicate_paths(&mut preview_paths);
    // Codex and Pi share one directory, so a copy there only counts for a
    // tool that is actually present.
    AgentSkillStatus {
        id: skill,
        installed: detected && skill_present(&dir, &canonical_skill_dir(&home, bundle)),
        preview_paths,
    }
}

fn skill_present(path: &Path, canonical: &Path) -> bool {
    if let Ok(target) = fs::read_link(path) {
        return target == canonical;
    }
    path.join("SKILL.md").is_file()
}

fn json_command(raw: &str) -> Option<String> {
    let value: Value = serde_json::from_str(raw).ok()?;
    value
        .get("mcpServers")?
        .get(LEGACY_MCP_SERVER_NAME)?
        .get("command")?
        .as_str()
        .map(str::to_string)
}

fn toml_command(raw: &str) -> Option<String> {
    let document = raw.parse::<toml_edit::DocumentMut>().ok()?;
    document
        .get("mcp_servers")?
        .get(LEGACY_MCP_SERVER_NAME)?
        .get("command")?
        .as_str()
        .map(str::to_string)
}

fn deduplicate_paths(paths: &mut Vec<String>) {
    let mut seen = BTreeSet::new();
    paths.retain(|path| !path.is_empty() && seen.insert(path.clone()));
}

fn write_canonical_skill(home: Home, bundle: &SkillBundle) -> Result<PathBuf, String> {
    let dest = canonical_skill_dir(&home, bundle);
    write_skill_tree(home, bundle, &dest)?;
    Ok(dest)
}

fn install_tool(home: Home, bundle: &SkillBundle, id: AgentToolId) -> Result<String, String> {
    let skill = tool_skill_dir(home, bundle, id);
    // Codex and Pi discover the shared directory, so only write it when one
    // of them is selected, and never next to a duplicate Codex copy.
    if id.uses_shared_skills() {
        migrate_legacy_codex_skill(home, bundle)?;
    }
    write_skill_tree(home, bundle, &skill)?;
    display_path(&skill)
}

fn uninstall_tool(home: Home, bundle: &SkillBundle, id: AgentToolId) -> Result<(), String> {
    // The shared skill is removed once, after all tool-specific installations.
    let skill = match id {
        AgentToolId::Codex => legacy_codex_skill_dir(&home, bundle),
        AgentToolId::Pi => return Ok(()),
        _ => tool_skill_dir(home, bundle, id),
    };
    if is_ours_skill(&skill, &canonical_skill_dir(&home, bundle), bundle) {
        remove_path(&skill)?;
    }
    Ok(())
}

fn write_skill_tree(home: Home, bundle: &SkillBundle, dest: &Path) -> Result<(), String> {
    let cli = cli_command(home);
    match dest.symlink_metadata() {
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            write_skill_files(dest, bundle, None, &cli)
        }
        Err(error) => Err(format!("unable to inspect {}: {error}", dest.display())),
        Ok(metadata) if metadata.file_type().is_symlink() => {
            if !points_at_canonical(dest, &canonical_skill_dir(&home, bundle)) {
                return refuse_overwrite(bundle, dest);
            }
            remove_path(dest)?;
            write_skill_files(dest, bundle, None, &cli)
        }
        Ok(_) => match read_managed_manifest(dest) {
            Some(managed) if managed.is_ours(bundle) => {
                write_skill_files(dest, bundle, Some(&managed), &cli)
            }
            _ => refuse_overwrite(bundle, dest),
        },
    }
}

fn write_skill_files(
    dest: &Path,
    bundle: &SkillBundle,
    existing: Option<&ManagedManifest>,
    cli: &str,
) -> Result<(), String> {
    fs::create_dir_all(dest)
        .map_err(|error| format!("unable to create {}: {error}", dest.display()))?;
    let mut next_hashes = BTreeMap::new();
    for file in bundle.files {
        let path = dest.join(file.relative);
        let contents = file.contents.replace(CLI_PLACEHOLDER, cli);
        let desired_hash = sha256_hex(contents.as_bytes());
        let overwrite = match existing {
            None => true,
            Some(managed) if !managed.hashes_known() => true,
            Some(managed) => match managed.recorded_hash(file.relative) {
                None => true,
                Some(recorded) => match fs::read(&path) {
                    Err(error) if error.kind() == io::ErrorKind::NotFound => true,
                    Err(error) => {
                        return Err(format!("unable to read {}: {error}", path.display()));
                    }
                    Ok(bytes) => sha256_hex(&bytes) == recorded,
                },
            },
        };
        if overwrite {
            if let Some(parent) = path.parent() {
                fs::create_dir_all(parent)
                    .map_err(|error| format!("unable to create {}: {error}", parent.display()))?;
            }
            fs::write(&path, contents)
                .map_err(|error| format!("unable to write {}: {error}", path.display()))?;
            next_hashes.insert(file.relative.to_string(), desired_hash);
        } else if let Some(recorded) =
            existing.and_then(|managed| managed.recorded_hash(file.relative))
        {
            next_hashes.insert(file.relative.to_string(), recorded.to_string());
        }
    }
    write_managed_manifest(dest, bundle, &next_hashes)
}

fn write_managed_manifest(
    dest: &Path,
    bundle: &SkillBundle,
    files: &BTreeMap<String, String>,
) -> Result<(), String> {
    write_json_file(
        &managed_files_path(dest),
        &json!({
            "manager": "astrlink",
            "bundle": bundle.name,
            "version": bundle.version,
            "files": files,
        }),
    )
}

fn refuse_overwrite(bundle: &SkillBundle, dest: &Path) -> Result<(), String> {
    Err(format!(
        "refusing to overwrite existing {} skill at {}",
        bundle.name,
        dest.display()
    ))
}

fn is_ours_skill(path: &Path, canonical: &Path, bundle: &SkillBundle) -> bool {
    if points_at_canonical(path, canonical) {
        return true;
    }
    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.is_dir() && !metadata.file_type().is_symlink() => {
            read_managed_manifest(path).is_some_and(|managed| managed.is_ours(bundle))
        }
        _ => false,
    }
}

fn points_at_canonical(path: &Path, canonical: &Path) -> bool {
    let Ok(target) = fs::read_link(path) else {
        return false;
    };
    if target == canonical {
        return true;
    }
    let resolved = path
        .parent()
        .map(|parent| parent.join(&target))
        .unwrap_or(target);
    if resolved == canonical {
        return true;
    }
    match (fs::canonicalize(&resolved), fs::canonicalize(canonical)) {
        (Ok(left), Ok(right)) => left == right,
        _ => {
            // A relative link can still be ours when its final directory was
            // deleted. Resolve the parents without requiring the leaf to exist.
            if resolved.file_name() != canonical.file_name() {
                return false;
            }
            match (resolved.parent(), canonical.parent()) {
                (Some(left), Some(right)) => {
                    match (fs::canonicalize(left), fs::canonicalize(right)) {
                        (Ok(left), Ok(right)) => left == right,
                        _ => false,
                    }
                }
                _ => false,
            }
        }
    }
}

fn managed_files_path(dest: &Path) -> PathBuf {
    dest.join(MANAGED_FILES_NAME)
}

struct ManagedManifest {
    manager: String,
    bundle: String,
    files: ManagedFiles,
}

enum ManagedFiles {
    Hashes(BTreeMap<String, String>),
    Unknown,
}

impl ManagedManifest {
    fn is_ours(&self, bundle: &SkillBundle) -> bool {
        self.manager == "astrlink" && self.bundle == bundle.name
    }

    fn hashes_known(&self) -> bool {
        matches!(self.files, ManagedFiles::Hashes(_))
    }

    fn recorded_hash(&self, relative: &str) -> Option<&str> {
        match &self.files {
            ManagedFiles::Hashes(map) => map.get(relative).map(String::as_str),
            ManagedFiles::Unknown => None,
        }
    }
}

fn read_managed_manifest(dest: &Path) -> Option<ManagedManifest> {
    let raw = fs::read_to_string(managed_files_path(dest)).ok()?;
    let value: Value = serde_json::from_str(&raw).ok()?;
    let manager = value.get("manager")?.as_str()?.to_string();
    let bundle = value.get("bundle")?.as_str()?.to_string();
    let files = match value.get("files") {
        Some(Value::Object(map)) => {
            let mut hashes = BTreeMap::new();
            for (key, item) in map {
                if let Some(hash) = item.as_str() {
                    hashes.insert(key.clone(), hash.to_string());
                }
            }
            ManagedFiles::Hashes(hashes)
        }
        _ => ManagedFiles::Unknown,
    };
    Some(ManagedManifest {
        manager,
        bundle,
        files,
    })
}

fn sha256_hex(bytes: &[u8]) -> String {
    let digest = Sha256::digest(bytes);
    let mut out = String::with_capacity(digest.len() * 2);
    const HEX: &[u8; 16] = b"0123456789abcdef";
    for byte in digest {
        out.push(HEX[(byte >> 4) as usize] as char);
        out.push(HEX[(byte & 0x0f) as usize] as char);
    }
    out
}

/// Installs the CLI through a temporary sibling, so a CLI process that is
/// still running keeps its old file instead of seeing it truncated.
fn copy_cli_binary(source: &Path, dest: &Path) -> Result<(), String> {
    if files_equal(source, dest) {
        return Ok(());
    }
    let (Some(parent), Some(name)) = (dest.parent(), dest.file_name()) else {
        return Err(format!(
            "unable to install astrlink-cli at {}",
            dest.display()
        ));
    };
    fs::create_dir_all(parent)
        .map_err(|error| format!("unable to create {}: {error}", parent.display()))?;
    let temporary = host_files::temporary_sibling(parent, name);
    let result = (|| {
        fs::copy(source, &temporary)?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&temporary, fs::Permissions::from_mode(0o755))?;
        }
        crate::preferences::atomic_replace(&temporary, dest)
    })();
    if let Err(error) = result {
        let _ = fs::remove_file(&temporary);
        return Err(format!("unable to install astrlink-cli: {error}"));
    }
    Ok(())
}

fn files_equal(left: &Path, right: &Path) -> bool {
    match (fs::metadata(left), fs::metadata(right)) {
        (Ok(a), Ok(b)) if a.len() == b.len() => {}
        _ => return false,
    }
    matches!((fs::read(left), fs::read(right)), (Ok(a), Ok(b)) if a == b)
}

/// The CLI's absolute path as the home's tools run it. Tools in WSL run the
/// desktop's Windows CLI through interop.
fn cli_path(home: Home) -> String {
    home.shown(&cli_binary_dest(home.host))
}

/// The CLI's absolute path as one shell word. Hosts match the command text
/// literally (Codex does not expand `~`), so the skill and the allow rules
/// spell it the same way.
fn cli_command(home: Home) -> String {
    let path = cli_path(home);
    if path
        .chars()
        .all(|c| c.is_ascii_alphanumeric() || "/\\:._-".contains(c))
    {
        path
    } else if home.windows() {
        format!("\"{path}\"")
    } else {
        format!("'{}'", path.replace('\'', r"'\''"))
    }
}

/// Removes the server entries and binary the MCP-based installer wrote. An
/// entry is only removed while it still runs that binary, so a server the
/// user repointed or a config that no longer parses is left alone.
fn remove_legacy_mcp(home: &Path) -> Result<(), String> {
    let binary = legacy_mcp_binary_dest(home);
    let command = display_path(&binary)?;
    for id in AgentToolId::all() {
        let Some(path) = legacy_mcp_config_path(home, id) else {
            continue;
        };
        let Some(raw) = read_optional(&path)? else {
            continue;
        };
        let next = match id {
            AgentToolId::Cursor | AgentToolId::Claude => {
                if json_command(&raw).as_deref() != Some(command.as_str()) {
                    continue;
                }
                remove_json_mcp(&raw)?
            }
            AgentToolId::Codex | AgentToolId::Grok => {
                if toml_command(&raw).as_deref() != Some(command.as_str()) {
                    continue;
                }
                remove_toml_mcp(&raw)?
            }
            AgentToolId::Pi => continue,
        };
        write_text(&path, &next)?;
    }
    // A host may still run the old server; on Windows that locks the file, so
    // a later start retries instead of failing the install.
    if let Err(error) = remove_path(&binary) {
        eprintln!("{error}");
    }
    Ok(())
}

fn remove_json_mcp(existing: &str) -> Result<String, String> {
    let mut value: Value = serde_json::from_str(existing)
        .map_err(|error| format!("MCP JSON is invalid; AstrLink will not overwrite it: {error}"))?;
    if let Some(servers) = value.get_mut("mcpServers").and_then(Value::as_object_mut) {
        servers.remove(LEGACY_MCP_SERVER_NAME);
    }
    pretty_json(&value)
}

fn remove_toml_mcp(existing: &str) -> Result<String, String> {
    let mut document = existing
        .parse::<toml_edit::DocumentMut>()
        .map_err(|error| {
            format!("config.toml is invalid; AstrLink will not overwrite it: {error}")
        })?;
    if let Some(servers) = document
        .get_mut("mcp_servers")
        .and_then(|item| item.as_table_mut())
    {
        servers.remove(LEGACY_MCP_SERVER_NAME);
    }
    Ok(document.to_string())
}

fn tool_cli_access_kind(id: AgentToolId) -> AgentCliAccessKind {
    match id {
        AgentToolId::Claude => AgentCliAccessKind::AllowRules,
        AgentToolId::Codex => AgentCliAccessKind::ExecPolicy,
        // Cursor keeps its command allowlist in app settings, and Grok Build
        // documents no rule file, so both ask the user on first use.
        AgentToolId::Cursor | AgentToolId::Grok => AgentCliAccessKind::Prompt,
        // Pi has no permission prompts: every enabled tool runs with the
        // process's own permissions.
        AgentToolId::Pi => AgentCliAccessKind::Unrestricted,
    }
}

fn tool_cli_access_path(home: Home, id: AgentToolId) -> Option<PathBuf> {
    match tool_cli_access_kind(id) {
        AgentCliAccessKind::AllowRules => Some(claude_settings_path(home)),
        AgentCliAccessKind::ExecPolicy => Some(codex_rules_path(home)),
        AgentCliAccessKind::Prompt | AgentCliAccessKind::Unrestricted => None,
    }
}

fn cli_access_present(home: Home, id: AgentToolId) -> bool {
    match tool_cli_access_kind(id) {
        AgentCliAccessKind::AllowRules => claude_rules_present(
            &claude_settings_path(home),
            "allow",
            &claude_allow_rules(home),
        ),
        AgentCliAccessKind::ExecPolicy => {
            fs::read_to_string(codex_rules_path(home)).is_ok_and(|raw| raw == codex_rules(home))
        }
        AgentCliAccessKind::Prompt | AgentCliAccessKind::Unrestricted => false,
    }
}

/// Allow rules for Claude Code, which otherwise asks before every command.
pub fn claude_allow_rules(home: Home) -> Vec<String> {
    vec![format!("Bash({} *)", cli_command(home))]
}

fn codex_rules_path(home: Home) -> PathBuf {
    home.codex_dir().join("rules").join("astrlink.rules")
}

/// A Codex rules file allowing the CLI. Codex's sandbox blocks the local
/// control socket, and an allow rule also runs the command outside it.
fn codex_rules(home: Home) -> String {
    let pattern = serde_json::to_string(&cli_path(home)).unwrap_or_default();
    format!(
        "{CODEX_RULES_MARKER}\n\
         # Lets agents run AstrLink's read-only debugging CLI outside the sandbox,\n\
         # which blocks its local control socket. Removed when AstrLink's agent\n\
         # debugging tools are uninstalled.\n\
         prefix_rule(\n    \
             pattern = [{pattern}],\n    \
             decision = \"allow\",\n    \
             justification = \"AstrLink read-only debugging CLI\",\n\
         )\n"
    )
}

fn claude_settings_path(home: Home) -> PathBuf {
    home.claude_dir().join("settings.json")
}

fn codex_agents_path(home: Home) -> PathBuf {
    home.codex_dir().join("AGENTS.md")
}

fn host_guards_path(home: &Path) -> PathBuf {
    astrlink_home(home).join("agent-host-guards.json")
}

fn tool_guard_kind(id: AgentToolId) -> AgentGuardKind {
    match id {
        AgentToolId::Claude => AgentGuardKind::DenyRules,
        AgentToolId::Codex => AgentGuardKind::Instructions,
        // Cursor keeps its global ignore list in app settings without a
        // documented file, and Grok Build documents no global instructions
        // file, so neither is written.
        AgentToolId::Cursor | AgentToolId::Grok => AgentGuardKind::SkillOnly,
        // Pi's global AGENTS.md moves with PI_CODING_AGENT_DIR, which the
        // desktop cannot see, so it is not written either.
        AgentToolId::Pi => AgentGuardKind::SkillOnly,
    }
}

fn tool_guard_path(home: Home, id: AgentToolId) -> Option<PathBuf> {
    match tool_guard_kind(id) {
        AgentGuardKind::DenyRules => Some(claude_settings_path(home)),
        AgentGuardKind::Instructions => Some(codex_agents_path(home)),
        AgentGuardKind::SkillOnly => None,
    }
}

fn guard_present(context: &InstallContext, home: Home, id: AgentToolId) -> bool {
    let Some(path) = tool_guard_path(home, id) else {
        return false;
    };
    let Ok(raw) = fs::read_to_string(&path) else {
        return false;
    };
    match id {
        AgentToolId::Claude => claude_rules_present(
            &path,
            "deny",
            &claude_deny_rules(
                home,
                context.data_directory.as_deref(),
                context.raw_key_pins.as_deref(),
            ),
        ),
        _ => codex_guard_range(&raw).is_some(),
    }
}

fn claude_rules_present(path: &Path, list: &str, rules: &[String]) -> bool {
    let Ok(raw) = fs::read_to_string(path) else {
        return false;
    };
    let Ok(value) = serde_json::from_str::<Value>(&raw) else {
        return false;
    };
    let Some(items) = value
        .get("permissions")
        .and_then(|permissions| permissions.get(list))
        .and_then(Value::as_array)
    else {
        return false;
    };
    rules
        .iter()
        .all(|rule| items.iter().any(|item| item.as_str() == Some(rule)))
}

/// Deny rules for Claude Code. Read rules also cover the Bash file commands
/// Claude Code recognises (`cat`, `head`, `tail`, `sed`, `tee`) and
/// redirections; the two `sqlite3` forms cover `sqlite3 <file>` and
/// `sqlite3<anything>` binaries such as `sqlite3_analyzer`. In WSL the
/// desktop's files sit under the drive mounts, the session file included.
pub fn claude_deny_rules(
    home: Home,
    data_directory: Option<&Path>,
    raw_key_pins: Option<&Path>,
) -> Vec<String> {
    let pattern = |path: &Path| {
        home.seen(path)
            .and_then(|path| claude_absolute_pattern(&path, home.windows()))
    };
    let mut rules = Vec::new();
    if let Some(pattern) = data_directory.and_then(pattern) {
        rules.push(format!("Read({pattern}/**)"));
    }
    if let Some(pattern) = raw_key_pins.and_then(pattern) {
        rules.push(format!("Read({pattern})"));
    }
    let session = match home.wsl {
        None => Some("~/.astrlink/control-session.json".to_string()),
        Some(_) => pattern(&session_path(home.host)),
    };
    if let Some(session) = session {
        rules.push(format!("Read({session})"));
    }
    rules.push("Bash(sqlite3 *)".to_string());
    rules.push("Bash(sqlite3*)".to_string());
    rules
}

/// Converts an absolute path to a Claude Code `//` pattern. Claude Code
/// normalises Windows paths to POSIX form (`C:\Users\a` becomes `/c/Users/a`)
/// before matching, and patterns use gitignore syntax, so wildcard characters
/// in the path are escaped. UNC paths have no documented form and are skipped.
pub fn claude_absolute_pattern(path: &str, windows: bool) -> Option<String> {
    let posix = if windows {
        let path = path.strip_prefix(r"\\?\").unwrap_or(path);
        let mut chars = path.chars();
        let drive = chars.next().filter(char::is_ascii_alphabetic)?;
        if chars.next() != Some(':') {
            return None;
        }
        let rest = chars.as_str().replace('\\', "/");
        if !rest.starts_with('/') {
            return None;
        }
        format!("/{}{}", drive.to_ascii_lowercase(), rest)
    } else {
        if !path.starts_with('/') {
            return None;
        }
        path.to_string()
    };
    let trimmed = posix.trim_end_matches('/');
    if trimmed.is_empty() {
        return None;
    }
    let mut pattern = String::with_capacity(trimmed.len() + 8);
    pattern.push('/');
    for character in trimmed.chars() {
        if matches!(character, '*' | '?' | '[' | ']' | '\\' | '(' | ')') {
            pattern.push('\\');
        }
        pattern.push(character);
    }
    Some(pattern)
}

/// Adds `rules` to `permissions.<list>` without touching the user's own
/// rules. `existing` is `None` when the file does not exist. Managed rules
/// from an earlier install that `rules` no longer contains are removed.
pub fn merge_claude_settings_rules(
    existing: Option<&str>,
    list: &str,
    rules: &[String],
    previous: Option<&ClaudeRuleRecord>,
) -> Result<(String, ClaudeRuleRecord), String> {
    let mut value = match existing {
        Some(raw) if !raw.trim().is_empty() => serde_json::from_str(raw).map_err(|error| {
            format!("Claude settings.json is invalid; AstrLink will not overwrite it: {error}")
        })?,
        _ => json!({}),
    };
    let root = value
        .as_object_mut()
        .ok_or_else(|| "Claude settings.json root must be an object".to_string())?;
    let previous = previous.cloned().unwrap_or_default();
    let created_permissions = previous.created_permissions || !root.contains_key("permissions");
    let permissions = root
        .entry("permissions")
        .or_insert_with(|| json!({}))
        .as_object_mut()
        .ok_or_else(|| "Claude settings.json permissions must be an object".to_string())?;
    let created_list = previous.created_list || !permissions.contains_key(list);
    let items = permissions
        .entry(list)
        .or_insert_with(|| json!([]))
        .as_array_mut()
        .ok_or_else(|| format!("Claude settings.json permissions.{list} must be an array"))?;
    for stale in previous.managed.iter().filter(|rule| !rules.contains(rule)) {
        remove_one_rule(items, stale);
    }
    let mut managed = Vec::new();
    for rule in rules {
        if !items.iter().any(|item| item.as_str() == Some(rule)) {
            items.push(json!(rule));
            managed.push(rule.clone());
        } else if previous.managed.contains(rule) {
            managed.push(rule.clone());
        }
    }
    let record = ClaudeRuleRecord {
        rules: rules.to_vec(),
        managed,
        created_file: previous.created_file || existing.is_none(),
        created_permissions,
        created_list,
    };
    Ok((pretty_json(&value)?, record))
}

/// Removes the rules AstrLink inserted. Returns `None` when the file was
/// created by AstrLink and nothing else remains in it.
pub fn remove_claude_settings_rules(
    existing: &str,
    list: &str,
    record: &ClaudeRuleRecord,
) -> Result<Option<String>, String> {
    if existing.trim().is_empty() {
        return Ok((!record.created_file).then(|| existing.to_string()));
    }
    let mut value: Value = serde_json::from_str(existing).map_err(|error| {
        format!("Claude settings.json is invalid; AstrLink will not overwrite it: {error}")
    })?;
    if let Some(root) = value.as_object_mut() {
        if let Some(permissions) = root.get_mut("permissions").and_then(Value::as_object_mut) {
            if let Some(items) = permissions.get_mut(list).and_then(Value::as_array_mut) {
                for rule in &record.managed {
                    remove_one_rule(items, rule);
                }
                if items.is_empty() && record.created_list {
                    permissions.remove(list);
                }
            }
            if permissions.is_empty() && record.created_permissions {
                root.remove("permissions");
            }
        }
        if root.is_empty() && record.created_file {
            return Ok(None);
        }
    }
    pretty_json(&value).map(Some)
}

fn remove_one_rule(items: &mut Vec<Value>, rule: &str) {
    if let Some(index) = items.iter().position(|item| item.as_str() == Some(rule)) {
        items.remove(index);
    }
}

fn codex_guard_block(
    home: Home,
    data_directory: Option<&Path>,
    raw_key_pins: Option<&Path>,
) -> String {
    let mut data = data_directory
        .map(|path| format!("AstrLink's data directory (`{}`)", home.shown(path)))
        .unwrap_or_else(|| "AstrLink's data directory".to_string());
    if let Some(path) = raw_key_pins {
        data.push_str(&format!(", its raw key pin file (`{}`)", home.shown(path)));
    }
    let session = match home.wsl {
        None => "~/.astrlink/control-session.json".to_string(),
        Some(_) => home.shown(&session_path(home.host)),
    };
    format!(
        "{CODEX_GUARD_BEGIN}\n\
         ## AstrLink local data\n\
         \n\
         AstrLink added this section with its agent debugging tools and removes it when they are uninstalled.\n\
         \n\
         - Inspect AstrLink only through the read-only CLI that the `astrlink-debug` skill describes.\n\
         - Do not read, copy, search, or open {data}, any `astrlink.db*` file, or `{session}`, and do not run `sqlite3` on them.\n\
         - The control socket and the session token only carry observer access. Do not use them to change AstrLink settings.\n\
         {CODEX_GUARD_END}"
    )
}

fn codex_guard_range(raw: &str) -> Option<(usize, usize)> {
    let start = raw.find(CODEX_GUARD_BEGIN)?;
    let end = raw[start..].find(CODEX_GUARD_END)? + start + CODEX_GUARD_END.len();
    Some((start, end))
}

/// Replaces AstrLink's marked section or appends it after the user's text.
pub fn merge_codex_agents_guard(existing: Option<&str>, block: &str) -> String {
    let existing = existing.unwrap_or_default();
    if let Some((start, end)) = codex_guard_range(existing) {
        return format!("{}{block}{}", &existing[..start], &existing[end..]);
    }
    let prefix = existing.trim_end();
    if prefix.is_empty() {
        format!("{block}\n")
    } else {
        format!("{prefix}\n\n{block}\n")
    }
}

/// Removes AstrLink's marked section and the blank line that separated it.
pub fn remove_codex_agents_guard(existing: &str) -> String {
    let Some((start, end)) = codex_guard_range(existing) else {
        return existing.to_string();
    };
    let prefix = existing[..start].trim_end();
    let suffix = existing[end..].trim_start_matches(['\r', '\n']);
    match (prefix.is_empty(), suffix.is_empty()) {
        (true, _) => suffix.to_string(),
        (false, true) => format!("{prefix}\n"),
        (false, false) => format!("{prefix}\n\n{suffix}"),
    }
}

/// Agent files keep their permissions; none of them holds a secret.
fn write_text(path: &Path, contents: &str) -> Result<(), String> {
    host_files::write_text(path, contents, Default::default())
}

fn read_host_guards(home: &Path) -> Result<HostGuardRecord, String> {
    let path = host_guards_path(home);
    match read_optional(&path)? {
        None => Ok(HostGuardRecord::default()),
        Some(raw) => serde_json::from_str(&raw)
            .map_err(|error| format!("unable to read {}: {error}", path.display())),
    }
}

fn write_host_guards(home: &Path, record: &mut HostGuardRecord) -> Result<(), String> {
    record.version = HOST_GUARDS_VERSION;
    write_json_file(&host_guards_path(home), record)
}

// The record is written before each host file so a failed write can at most
// leave a record naming rules that were never added, which removal ignores.
fn install_host_guards(
    context: &InstallContext,
    home: Home,
    tool_ids: &[AgentToolId],
) -> Result<Vec<String>, String> {
    let codex_rules_path = codex_rules_path(home);
    if tool_ids.contains(&AgentToolId::Codex) {
        if let Some(raw) = read_optional(&codex_rules_path)? {
            if !raw.starts_with(CODEX_RULES_MARKER) {
                return Err(format!(
                    "refusing to overwrite Codex rules at {}",
                    codex_rules_path.display()
                ));
            }
        }
    }
    let mut record = read_host_guards(&home)?;
    let mut files = Vec::new();
    if tool_ids.contains(&AgentToolId::Claude) {
        let path = claude_settings_path(home);
        let deny = claude_deny_rules(
            home,
            context.data_directory.as_deref(),
            context.raw_key_pins.as_deref(),
        );
        let existing = read_optional(&path)?;
        let (next, denied) = merge_claude_settings_rules(
            existing.as_deref(),
            "deny",
            &deny,
            record.claude.as_ref(),
        )?;
        let (next, allowed) = merge_claude_settings_rules(
            Some(&next),
            "allow",
            &claude_allow_rules(home),
            record.claude_allow.as_ref(),
        )?;
        record.claude = Some(denied);
        record.claude_allow = Some(allowed);
        write_host_guards(&home, &mut record)?;
        write_text(&path, &next)?;
        files.push(display_path(&path)?);
    }
    if tool_ids.contains(&AgentToolId::Codex) {
        let path = codex_agents_path(home);
        let existing = read_optional(&path)?;
        let created_file = record
            .codex
            .as_ref()
            .is_some_and(|codex| codex.created_file)
            || existing.is_none();
        record.codex = Some(CodexInstructionsRecord { created_file });
        write_host_guards(&home, &mut record)?;
        let block = codex_guard_block(
            home,
            context.data_directory.as_deref(),
            context.raw_key_pins.as_deref(),
        );
        write_text(
            &path,
            &merge_codex_agents_guard(existing.as_deref(), &block),
        )?;
        files.push(display_path(&path)?);
        record.codex_rules = true;
        write_host_guards(&home, &mut record)?;
        write_text(&codex_rules_path, &codex_rules(home))?;
        files.push(display_path(&codex_rules_path)?);
    }
    if record.claude.is_some() || record.codex.is_some() {
        files.push(display_path(&host_guards_path(&home))?);
    }
    Ok(files)
}

fn uninstall_host_guards(home: Home) -> Result<(), String> {
    let record = read_host_guards(&home)?;
    let path = claude_settings_path(home);
    if let Some(mut raw) = read_optional(&path)? {
        let mut changed = false;
        let mut emptied = false;
        // The allow rules were merged after the deny rules, so they come out first.
        for (list, rules) in [("allow", &record.claude_allow), ("deny", &record.claude)] {
            let Some(rules) = rules else {
                continue;
            };
            changed = true;
            match remove_claude_settings_rules(&raw, list, rules)? {
                Some(next) => raw = next,
                None => {
                    emptied = true;
                    break;
                }
            }
        }
        if emptied {
            remove_path(&path)?;
        } else if changed {
            write_text(&path, &raw)?;
        }
    }
    // The markers identify the section even if the record was lost.
    let path = codex_agents_path(home);
    if let Some(raw) = read_optional(&path)? {
        if codex_guard_range(&raw).is_some() {
            let next = remove_codex_agents_guard(&raw);
            let created = record
                .codex
                .as_ref()
                .is_some_and(|codex| codex.created_file);
            if next.trim().is_empty() && created {
                remove_path(&path)?;
            } else {
                write_text(&path, &next)?;
            }
        }
    }
    // The marker identifies the rules file even if the record was lost.
    let rules_path = codex_rules_path(home);
    if read_optional(&rules_path)?.is_some_and(|raw| raw.starts_with(CODEX_RULES_MARKER)) {
        remove_path(&rules_path)?;
    }
    remove_path(&host_guards_path(&home))
}

/// Keeps installed guards and CLI access current when the data directory, pin
/// file, CLI path, or rule set changes, and adds CLI access to installs made
/// before the CLI. A settings file, `AGENTS.md` section, or Codex rules file
/// the user removed by hand is not re-created; reinstalling from Settings
/// restores it. When the rules do change, every current rule missing from a
/// kept settings file is added, including one the user deleted from it.
pub fn sync_installed_host_guards(
    home: Home,
    data_directory: Option<&Path>,
    raw_key_pins: Option<&Path>,
) -> Result<(), String> {
    let _lock = host_files::lock();
    if !receipt_path(&home).is_file() || !host_guards_path(&home).is_file() {
        return Ok(());
    }
    let mut record = read_host_guards(&home)?;
    if let Some(previous) = record.claude.clone() {
        let rules = claude_deny_rules(home, data_directory, raw_key_pins);
        let path = claude_settings_path(home);
        if previous.rules != rules {
            if let Some(existing) = read_optional(&path)? {
                let (next, applied) =
                    merge_claude_settings_rules(Some(&existing), "deny", &rules, Some(&previous))?;
                record.claude = Some(applied);
                write_host_guards(&home, &mut record)?;
                write_text(&path, &next)?;
            }
        }
        let rules = claude_allow_rules(home);
        let previous = record.claude_allow.clone();
        if previous.as_ref().map(|previous| &previous.rules) != Some(&rules) {
            if let Some(existing) = read_optional(&path)? {
                let (next, applied) = merge_claude_settings_rules(
                    Some(&existing),
                    "allow",
                    &rules,
                    previous.as_ref(),
                )?;
                record.claude_allow = Some(applied);
                write_host_guards(&home, &mut record)?;
                write_text(&path, &next)?;
            }
        }
    }
    if record.codex.is_some() {
        let path = codex_agents_path(home);
        if let Some(existing) = read_optional(&path)? {
            if let Some((start, end)) = codex_guard_range(&existing) {
                let block = codex_guard_block(home, data_directory, raw_key_pins);
                if existing[start..end] != block {
                    write_text(&path, &merge_codex_agents_guard(Some(&existing), &block))?;
                }
            }
        }
        let rules_path = codex_rules_path(home);
        let desired = codex_rules(home);
        match read_optional(&rules_path)? {
            None if !record.codex_rules => {
                record.codex_rules = true;
                write_host_guards(&home, &mut record)?;
                write_text(&rules_path, &desired)?;
            }
            Some(existing) if existing.starts_with(CODEX_RULES_MARKER) && existing != desired => {
                write_text(&rules_path, &desired)?;
            }
            _ => {}
        }
    }
    Ok(())
}

fn pretty_json(value: &Value) -> Result<String, String> {
    let mut encoded = serde_json::to_string_pretty(value)
        .map_err(|error| format!("unable to encode JSON: {error}"))?;
    encoded.push('\n');
    Ok(encoded)
}

fn write_json_file(path: &Path, value: &impl Serialize) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)
            .map_err(|error| format!("unable to create {}: {error}", parent.display()))?;
    }
    let mut encoded = serde_json::to_string_pretty(value)
        .map_err(|error| format!("unable to encode {}: {error}", path.display()))?;
    encoded.push('\n');
    fs::write(path, encoded).map_err(|error| format!("unable to write {}: {error}", path.display()))
}

fn display_path(path: &Path) -> Result<String, String> {
    path.to_str()
        .map(str::to_string)
        .ok_or_else(|| format!("{} is not valid UTF-8", path.display()))
}

fn unix_now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs())
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;

    const DEBUG: &SkillBundle = &DEBUG_BUNDLE;
    const DEBUG_ONLY: &[AgentSkillId] = &[AgentSkillId::AstrlinkDebug];

    impl AgentToolStatus {
        fn skill(&self, id: AgentSkillId) -> &AgentSkillStatus {
            self.skills.iter().find(|skill| skill.id == id).unwrap()
        }
    }

    fn install_local(
        context: &InstallContext,
        skill_ids: &[AgentSkillId],
        tool_ids: &[AgentToolId],
    ) -> Result<InstallReceipt, String> {
        let tools = tool_ids
            .iter()
            .map(|id| AgentToolTarget { id: *id, wsl: None })
            .collect::<Vec<_>>();
        install(context, skill_ids, &tools)
    }

    /// Any desktop home; local rules do not depend on it.
    fn desktop() -> Home<'static> {
        Home::local(Path::new("/home/user"))
    }

    #[test]
    fn install_and_uninstall_detected_tools() {
        let home = unique_temp("agent-install");
        for dir in [".cursor", ".claude", ".codex", ".grok", ".pi"] {
            fs::create_dir_all(home.join(dir)).unwrap();
        }
        let cursor_mcp = r#"{"mcpServers":{"keep":{"command":"x"}}}"#;
        fs::write(home.join(".cursor").join("mcp.json"), cursor_mcp).unwrap();
        let grok_config =
            "[models]\ndefault = \"keep-model\"\n\n[mcp_servers.keep]\ncommand = \"x\"\n";
        fs::write(home.join(".grok").join("config.toml"), grok_config).unwrap();
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();

        let context = InstallContext {
            home: home.clone(),
            cli_source,
            data_directory: None,
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };
        let before = status(&context);
        assert!(before.tools.iter().all(|tool| tool.detected
            && tool.skills.iter().all(|skill| !skill.installed)
            && !tool.cli_access_installed));

        let receipt = install_local(&context, &AgentSkillId::all(), &AgentToolId::all()).unwrap();
        assert_eq!(
            receipt.skills,
            AgentSkillId::all().map(|id| ReceiptSkill {
                id,
                version: id.bundle().version.to_string(),
            })
        );
        assert_eq!(
            receipt.cli_binary,
            Some(display_path(&cli_binary_dest(&home)).unwrap())
        );
        assert_eq!(fs::read(cli_binary_dest(&home)).unwrap(), b"cli");
        for skill in AgentSkillId::all() {
            let bundle = skill.bundle();
            for id in AgentToolId::all() {
                assert_real_skill_copy(
                    &home,
                    bundle,
                    &tool_skill_dir(Home::local(&home), bundle, id),
                );
            }
            assert!(!legacy_codex_skill_dir(&home, bundle).exists());
            let shared_path = display_path(&canonical_skill_dir(&home, bundle)).unwrap();
            assert_eq!(
                receipt
                    .files
                    .iter()
                    .filter(|path| **path == shared_path)
                    .count(),
                1
            );
            assert!(!receipt
                .files
                .contains(&display_path(&legacy_codex_skill_dir(&home, bundle)).unwrap()));
        }

        let after = status(&context);
        let shared_path = display_path(&canonical_skill_dir(&home, DEBUG)).unwrap();
        // Codex and Pi both read the one shared copy.
        assert_eq!(
            after
                .tools
                .iter()
                .filter(|tool| tool
                    .skill(AgentSkillId::AstrlinkDebug)
                    .preview_paths
                    .contains(&shared_path))
                .map(|tool| tool.id)
                .collect::<Vec<_>>(),
            [AgentToolId::Codex, AgentToolId::Pi]
        );
        assert!(!after
            .tools
            .iter()
            .flat_map(|tool| &tool.skills)
            .flat_map(|skill| &skill.preview_paths)
            .any(|path| *path == display_path(&legacy_codex_skill_dir(&home, DEBUG)).unwrap()));
        assert!(after.cli_binary);
        for tool in &after.tools {
            let access = match tool.id {
                AgentToolId::Claude => AgentCliAccessKind::AllowRules,
                AgentToolId::Codex => AgentCliAccessKind::ExecPolicy,
                AgentToolId::Pi => AgentCliAccessKind::Unrestricted,
                AgentToolId::Cursor | AgentToolId::Grok => AgentCliAccessKind::Prompt,
            };
            assert!(
                tool.detected && tool.skills.iter().all(|skill| skill.installed),
                "{:?}",
                tool.id
            );
            assert_eq!(tool.cli_access, access, "{:?}", tool.id);
            assert_eq!(
                tool.cli_access_installed,
                matches!(
                    access,
                    AgentCliAccessKind::AllowRules | AgentCliAccessKind::ExecPolicy
                ),
                "{:?}",
                tool.id
            );
        }
        // The CLI needs no host MCP entry, so other servers stay untouched.
        assert_eq!(
            fs::read_to_string(home.join(".cursor").join("mcp.json")).unwrap(),
            cursor_mcp
        );
        assert_eq!(
            fs::read_to_string(home.join(".grok").join("config.toml")).unwrap(),
            grok_config
        );

        uninstall(&context).unwrap();
        let gone = status(&context);
        assert!(!gone.cli_binary);
        for tool in &gone.tools {
            assert!(tool.skills.iter().all(|skill| !skill.installed));
            assert!(!tool.cli_access_installed);
        }
        for skill in AgentSkillId::all() {
            let bundle = skill.bundle();
            for id in AgentToolId::all() {
                assert!(
                    !tool_skill_dir(Home::local(&home), bundle, id).exists(),
                    "{id:?}"
                );
            }
        }
        assert!(!codex_rules_path(Home::local(&home)).exists());
        assert!(!claude_settings_path(Home::local(&home)).exists());
        assert!(!receipt_path(&home).exists());
        assert_eq!(
            fs::read_to_string(home.join(".cursor").join("mcp.json")).unwrap(),
            cursor_mcp
        );
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn the_skill_names_the_cli_by_its_absolute_path() {
        // Nothing is written, and the temporary directory can't stand in for a
        // plain home: Windows runners use a short name such as `RUNNER~1`.
        let home = PathBuf::from(if cfg!(windows) {
            r"C:\Users\agent"
        } else {
            "/home/agent"
        });
        let cli = cli_command(Home::local(&home));
        assert_eq!(cli, display_path(&cli_binary_dest(&home)).unwrap());
        assert!(bundle_file(DEBUG, "SKILL.md").contains(CLI_PLACEHOLDER));
        let rendered = rendered_skill(&home);
        assert!(!rendered.contains(CLI_PLACEHOLDER));
        assert!(rendered.contains(&format!("{cli} sessions")));
        assert_eq!(
            claude_allow_rules(Home::local(&home)),
            [format!("Bash({cli} *)")]
        );
        let rules = codex_rules(Home::local(&home));
        assert!(rules.starts_with(CODEX_RULES_MARKER));
        assert!(rules.contains(&format!(
            "pattern = [{}]",
            serde_json::to_string(&cli).unwrap()
        )));
        assert!(!rules.contains('~'));

        // A path a shell would split is quoted as one word.
        #[cfg(unix)]
        {
            let spaced = home.join("John Doe");
            assert_eq!(
                cli_command(Home::local(&spaced)),
                format!("'{}'", cli_binary_dest(&spaced).display())
            );
        }
    }

    #[test]
    fn install_and_startup_retire_the_legacy_mcp_server() {
        for startup in [false, true] {
            let home = unique_temp("agent-legacy-mcp");
            for dir in [".cursor", ".claude", ".codex", ".grok"] {
                fs::create_dir_all(home.join(dir)).unwrap();
            }
            let legacy = legacy_mcp_binary_dest(&home);
            fs::create_dir_all(legacy.parent().unwrap()).unwrap();
            fs::write(&legacy, b"mcp").unwrap();
            let command = display_path(&legacy).unwrap();
            let json_config = json!({
                "mcpServers": {
                    "astrlink": {"type": "stdio", "command": command, "args": []},
                    "keep": {"command": "x"},
                },
                "theme": "dark",
            })
            .to_string();
            let toml_config = format!(
                "model = \"keep-model\"\n\n[mcp_servers.keep]\ncommand = \"x\"\n\n\
                 [mcp_servers.astrlink]\ncommand = {}\nargs = []\n",
                serde_json::to_string(&command).unwrap()
            );
            for id in AgentToolId::all() {
                let config = match id {
                    AgentToolId::Cursor | AgentToolId::Claude => &json_config,
                    AgentToolId::Codex | AgentToolId::Grok => &toml_config,
                    AgentToolId::Pi => continue,
                };
                fs::write(legacy_mcp_config_path(&home, id).unwrap(), config).unwrap();
            }
            let cli_source = home.join("src-astrlink-cli");
            fs::write(&cli_source, b"cli").unwrap();
            let context = InstallContext {
                home: home.clone(),
                cli_source,
                data_directory: None,
                raw_key_pins: None,
                dirs: ToolDirs::default(),
                wsl: Vec::new(),
            };
            if startup {
                // Startup migrates only installs AstrLink made.
                sync_installed_cli(&context).unwrap();
                assert!(legacy.is_file());
                write_json_file(
                    &receipt_path(&home),
                    &json!({"version": 1, "bundle": DEBUG.name, "mcp_binary": command}),
                )
                .unwrap();
                sync_installed_cli(&context).unwrap();
            } else {
                install_local(&context, DEBUG_ONLY, &[AgentToolId::Cursor]).unwrap();
            }

            assert!(!legacy.exists());
            assert_eq!(fs::read(cli_binary_dest(&home)).unwrap(), b"cli");
            for id in AgentToolId::all() {
                let Some(path) = legacy_mcp_config_path(&home, id) else {
                    continue;
                };
                let raw = fs::read_to_string(path).unwrap();
                assert!(!raw.contains("astrlink"), "{id:?}: {raw}");
                assert!(raw.contains("keep"), "{id:?}");
            }
            let cursor: Value = serde_json::from_str(
                &fs::read_to_string(legacy_mcp_config_path(&home, AgentToolId::Cursor).unwrap())
                    .unwrap(),
            )
            .unwrap();
            assert_eq!(cursor["theme"], "dark");
            let grok =
                fs::read_to_string(legacy_mcp_config_path(&home, AgentToolId::Grok).unwrap())
                    .unwrap()
                    .parse::<toml_edit::DocumentMut>()
                    .unwrap();
            assert_eq!(grok["model"].as_str(), Some("keep-model"));
            let _ = fs::remove_dir_all(&home);
        }
    }

    #[test]
    fn startup_grants_cli_access_to_hosts_an_mcp_install_registered() {
        let home = unique_temp("agent-legacy-receipt");
        for dir in [".cursor", ".claude", ".codex", ".grok"] {
            fs::create_dir_all(home.join(dir)).unwrap();
        }
        fs::write(
            claude_settings_path(Home::local(&home)),
            r#"{"permissions":{"allow":["Bash(ls *)"]}}"#,
        )
        .unwrap();
        let legacy = legacy_mcp_binary_dest(&home);
        let receipt_file = receipt_path(&home);
        let path = |path: &Path| display_path(path).unwrap();
        let mut listed = vec![path(&legacy), path(&receipt_file)];
        // Grok was installed too; its host has no rule file to write.
        for id in [AgentToolId::Claude, AgentToolId::Codex, AgentToolId::Grok] {
            fs::write(legacy_mcp_config_path(&home, id).unwrap(), "").unwrap();
            listed.push(path(&legacy_mcp_config_path(&home, id).unwrap()));
        }
        let skill = tool_skill_dir(Home::local(&home), DEBUG, AgentToolId::Claude);
        fs::create_dir_all(&skill).unwrap();
        listed.push(path(&skill));
        listed.push(path(&home.join(".codex/skills").join(DEBUG.name)));
        write_json_file(
            &receipt_file,
            &json!({
                "version": 1,
                "bundle": DEBUG.name,
                "bundle_version": "0.1.1",
                "installed_at_unix": 7,
                "mcp_binary": path(&legacy),
                "files": listed,
            }),
        )
        .unwrap();
        let context = InstallContext {
            home: home.clone(),
            cli_source: home.join("missing"),
            data_directory: Some(home.join("data")),
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };

        // Without a CLI to allow, the receipt waits for a build that has one.
        sync_installed_cli(&context).unwrap();
        assert!(!codex_rules_path(Home::local(&home)).exists());
        let receipt: Value =
            serde_json::from_str(&fs::read_to_string(&receipt_file).unwrap()).unwrap();
        assert_eq!(receipt["version"], 1);

        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            cli_source,
            ..context
        };
        sync_installed_cli(&context).unwrap();
        let settings = fs::read_to_string(claude_settings_path(Home::local(&home))).unwrap();
        let after = status(&context);
        for tool in &after.tools {
            let expected = matches!(tool.id, AgentToolId::Claude | AgentToolId::Codex);
            assert_eq!(tool.cli_access_installed, expected, "{:?}", tool.id);
            assert_eq!(tool.guard_installed, expected, "{:?}", tool.id);
        }
        assert!(settings.contains("Bash(ls *)"));
        let receipt: InstallReceipt =
            serde_json::from_str(&fs::read_to_string(&receipt_file).unwrap()).unwrap();
        assert_eq!(receipt.version, RECEIPT_VERSION);
        assert_eq!(receipt.installed_at_unix, 7);
        assert_eq!(receipt.cli_binary, Some(path(&cli_binary_dest(&home))));
        assert_eq!(
            receipt.skills,
            [ReceiptSkill {
                id: AgentSkillId::AstrlinkDebug,
                version: DEBUG.version.to_string(),
            }]
        );
        assert_eq!(
            receipt.files,
            [
                path(&cli_binary_dest(&home)),
                path(&skill),
                path(&claude_settings_path(Home::local(&home))),
                path(&codex_agents_path(Home::local(&home))),
                path(&codex_rules_path(Home::local(&home))),
                path(&host_guards_path(&home)),
                path(&receipt_file),
            ]
        );

        // The upgraded receipt is not migrated again.
        fs::remove_file(codex_rules_path(Home::local(&home))).unwrap();
        sync_installed_cli(&context).unwrap();
        sync_installed_host_guards(Home::local(&home), context.data_directory.as_deref(), None)
            .unwrap();
        assert!(!codex_rules_path(Home::local(&home)).exists());
        assert_eq!(
            fs::read_to_string(claude_settings_path(Home::local(&home))).unwrap(),
            settings
        );

        uninstall(&context).unwrap();
        let settings: Value = serde_json::from_str(
            &fs::read_to_string(claude_settings_path(Home::local(&home))).unwrap(),
        )
        .unwrap();
        assert_eq!(settings, json!({"permissions": {"allow": ["Bash(ls *)"]}}));
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn legacy_mcp_removal_keeps_repointed_and_unreadable_entries() {
        let home = unique_temp("agent-legacy-mcp-kept");
        for dir in [".cursor", ".claude", ".codex", ".grok"] {
            fs::create_dir_all(home.join(dir)).unwrap();
        }
        let repointed = r#"{"mcpServers":{"astrlink":{"command":"/custom/astrlink-mcp"}}}"#;
        let kept = [
            (AgentToolId::Cursor, repointed.to_string()),
            (AgentToolId::Claude, "{".to_string()),
            (
                AgentToolId::Codex,
                "[mcp_servers.astrlink]\ncommand = \"/custom/astrlink-mcp\"\n".to_string(),
            ),
            (AgentToolId::Grok, "[models\n".to_string()),
        ];
        for (id, raw) in &kept {
            fs::write(legacy_mcp_config_path(&home, *id).unwrap(), raw).unwrap();
        }
        remove_legacy_mcp(&home).unwrap();
        for (id, raw) in &kept {
            assert_eq!(
                &fs::read_to_string(legacy_mcp_config_path(&home, *id).unwrap()).unwrap(),
                raw,
                "{id:?}"
            );
        }
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn install_and_startup_archive_legacy_codex_copy_without_losing_edits() {
        for reinstall in [false, true] {
            let context = installed_codex_context("codex-migrate");
            let home = &context.home;
            let canonical = canonical_skill_dir(home, DEBUG);
            let legacy = legacy_codex_skill_dir(home, DEBUG);
            write_skill_tree(Home::local(home), DEBUG, &legacy).unwrap();
            fs::write(canonical.join("SKILL.md"), "shared user edit").unwrap();
            fs::write(legacy.join("SKILL.md"), "legacy user edit").unwrap();
            fs::write(legacy.join("notes.txt"), "keep this extra file").unwrap();
            let legacy_manifest = fs::read(managed_files_path(&legacy)).unwrap();

            if reinstall {
                install_local(&context, DEBUG_ONLY, &[AgentToolId::Codex]).unwrap();
            } else {
                sync_installed_skills(Home::local(home)).unwrap();
            }
            assert!(!legacy.exists());
            assert_eq!(
                fs::read_to_string(canonical.join("SKILL.md")).unwrap(),
                "shared user edit"
            );
            let backups = codex_backups(home);
            assert_eq!(backups.len(), 1);
            let archived = backups[0].join(DEBUG.name);
            assert_eq!(
                fs::read_to_string(archived.join("SKILL.md")).unwrap(),
                "legacy user edit"
            );
            assert_eq!(
                fs::read_to_string(archived.join("notes.txt")).unwrap(),
                "keep this extra file"
            );
            assert_eq!(
                fs::read(managed_files_path(&archived)).unwrap(),
                legacy_manifest
            );
            assert!(
                status(&context)
                    .tools
                    .iter()
                    .find(|tool| tool.id == AgentToolId::Codex)
                    .unwrap()
                    .skill(AgentSkillId::AstrlinkDebug)
                    .installed
            );

            // Startup and a later reinstall must not recreate the duplicate.
            sync_installed_skills(Home::local(home)).unwrap();
            install_local(&context, DEBUG_ONLY, &[AgentToolId::Codex]).unwrap();
            assert!(!legacy.exists());
            assert_eq!(codex_backups(home), backups);
            uninstall(&context).unwrap();
            assert!(!canonical.exists());
            assert!(archived.join("notes.txt").is_file());
            let _ = fs::remove_dir_all(home);
        }
    }

    #[test]
    fn startup_migration_requires_receipt() {
        let home = unique_temp("codex-no-receipt");
        write_canonical_skill(Home::local(&home), DEBUG).unwrap();
        let legacy = legacy_codex_skill_dir(&home, DEBUG);
        write_skill_tree(Home::local(&home), DEBUG, &legacy).unwrap();
        sync_installed_skills(Home::local(&home)).unwrap();
        assert_real_skill_copy(&home, DEBUG, &legacy);
        assert!(codex_backups(&home).is_empty());
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn startup_moves_lone_legacy_codex_copy_to_shared_directory() {
        let context = installed_codex_context("codex-legacy-only");
        let home = &context.home;
        let canonical = canonical_skill_dir(home, DEBUG);
        let legacy = legacy_codex_skill_dir(home, DEBUG);
        fs::create_dir_all(legacy.parent().unwrap()).unwrap();
        fs::rename(&canonical, &legacy).unwrap();
        fs::write(legacy.join("SKILL.md"), "keep legacy customization").unwrap();
        sync_installed_skills(Home::local(home)).unwrap();
        assert!(!legacy.exists());
        assert_eq!(
            fs::read_to_string(canonical.join("SKILL.md")).unwrap(),
            "keep legacy customization"
        );
        assert!(codex_backups(home).is_empty());
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn migration_and_uninstall_preserve_foreign_codex_directory() {
        let context = installed_codex_context("codex-foreign");
        let home = &context.home;
        let legacy = legacy_codex_skill_dir(home, DEBUG);
        fs::create_dir_all(&legacy).unwrap();
        fs::write(legacy.join("SKILL.md"), "not ours").unwrap();
        sync_installed_skills(Home::local(home)).unwrap();
        install_local(&context, DEBUG_ONLY, &[AgentToolId::Codex]).unwrap();
        uninstall(&context).unwrap();
        assert_eq!(
            fs::read_to_string(legacy.join("SKILL.md")).unwrap(),
            "not ours"
        );
        assert!(codex_backups(home).is_empty());
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn migration_refuses_foreign_shared_directory_and_preserves_legacy_copy() {
        let context = installed_codex_context("codex-foreign-shared");
        let home = &context.home;
        let canonical = canonical_skill_dir(home, DEBUG);
        let legacy = legacy_codex_skill_dir(home, DEBUG);
        write_skill_tree(Home::local(home), DEBUG, &legacy).unwrap();
        fs::remove_file(managed_files_path(&canonical)).unwrap();
        fs::write(canonical.join("SKILL.md"), "foreign shared skill").unwrap();
        assert!(sync_installed_skills(Home::local(home))
            .unwrap_err()
            .contains("refusing to overwrite"));
        assert_real_skill_copy(home, DEBUG, &legacy);
        assert!(codex_backups(home).is_empty());
        uninstall(&context).unwrap();
        assert!(!legacy.exists());
        assert_eq!(
            fs::read_to_string(canonical.join("SKILL.md")).unwrap(),
            "foreign shared skill"
        );
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn uninstall_removes_legacy_codex_installation_before_startup_migration() {
        let context = installed_codex_context("codex-uninstall-legacy");
        let home = &context.home;
        let canonical = canonical_skill_dir(home, DEBUG);
        let legacy = legacy_codex_skill_dir(home, DEBUG);
        write_skill_tree(Home::local(home), DEBUG, &legacy).unwrap();
        uninstall(&context).unwrap();
        assert!(!canonical.exists());
        assert!(!legacy.exists());
        assert!(
            !status(&context)
                .tools
                .iter()
                .find(|tool| tool.id == AgentToolId::Codex)
                .unwrap()
                .skill(AgentSkillId::AstrlinkDebug)
                .installed
        );
        let _ = fs::remove_dir_all(home);
    }

    #[cfg(unix)]
    #[test]
    fn startup_removes_legacy_codex_links_and_repairs_missing_shared_skill() {
        for relative in [false, true] {
            for missing_shared in [false, true] {
                let context = installed_codex_context("codex-link");
                let home = &context.home;
                let canonical = canonical_skill_dir(home, DEBUG);
                let legacy = legacy_codex_skill_dir(home, DEBUG);
                fs::create_dir_all(legacy.parent().unwrap()).unwrap();
                let target = if relative {
                    PathBuf::from("../../.agents/skills/astrlink-debug")
                } else {
                    canonical.clone()
                };
                std::os::unix::fs::symlink(target, &legacy).unwrap();
                if missing_shared {
                    fs::remove_dir_all(&canonical).unwrap();
                }
                sync_installed_skills(Home::local(home)).unwrap();
                assert!(legacy.symlink_metadata().is_err());
                assert_real_skill_copy(home, DEBUG, &canonical);
                assert!(codex_backups(home).is_empty());
                let _ = fs::remove_dir_all(home);
            }
        }
    }

    fn installed_codex_context(name: &str) -> InstallContext {
        let home = unique_temp(name);
        fs::create_dir_all(home.join(".codex")).unwrap();
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            home,
            cli_source,
            data_directory: None,
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };
        install_local(&context, DEBUG_ONLY, &[AgentToolId::Codex]).unwrap();
        context
    }

    fn codex_backups(home: &Path) -> Vec<PathBuf> {
        let root = astrlink_home(home).join("agent-skill-backups");
        if !root.exists() {
            return vec![];
        }
        let mut backups = fs::read_dir(root)
            .unwrap()
            .map(|entry| entry.unwrap().path())
            .collect::<Vec<_>>();
        backups.sort();
        backups
    }

    #[test]
    fn hash_gate_overwrites_unchanged_files_and_keeps_edits() {
        let home = unique_temp("agent-hash-gate");
        let dest = canonical_skill_dir(&home, DEBUG);
        write_skill_tree(Home::local(&home), DEBUG, &dest).unwrap();

        let skill = dest.join("SKILL.md");
        fs::write(&skill, "stale-managed").unwrap();
        let mut hashes = managed_hashes(&dest);
        hashes.insert("SKILL.md".into(), sha256_hex(b"stale-managed"));
        write_managed_manifest(&dest, DEBUG, &hashes).unwrap();
        write_skill_tree(Home::local(&home), DEBUG, &dest).unwrap();
        assert_eq!(fs::read_to_string(&skill).unwrap(), rendered_skill(&home));

        fs::write(&skill, "user-edit").unwrap();
        write_skill_tree(Home::local(&home), DEBUG, &dest).unwrap();
        assert_eq!(fs::read_to_string(&skill).unwrap(), "user-edit");
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn old_array_manifest_upgrades_to_hash_map() {
        let home = unique_temp("agent-old-manifest");
        let dest = canonical_skill_dir(&home, DEBUG);
        fs::create_dir_all(dest.join("references")).unwrap();
        fs::write(dest.join("SKILL.md"), "legacy").unwrap();
        write_json_file(
            &managed_files_path(&dest),
            &json!({
                "manager": "astrlink",
                "bundle": DEBUG.name,
                "version": "0.0.1",
                "files": ["SKILL.md", "references/trajectory.md", "manifest.json"],
            }),
        )
        .unwrap();
        write_skill_tree(Home::local(&home), DEBUG, &dest).unwrap();
        assert_real_skill_copy(&home, DEBUG, &dest);
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn refuses_foreign_skill_directory() {
        let home = unique_temp("agent-foreign");
        fs::create_dir_all(home.join(".cursor")).unwrap();
        let dest = tool_skill_dir(Home::local(&home), DEBUG, AgentToolId::Cursor);
        fs::create_dir_all(&dest).unwrap();
        fs::write(dest.join("SKILL.md"), "not yours").unwrap();
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let error = install_local(
            &InstallContext {
                home: home.clone(),
                cli_source,
                data_directory: None,
                raw_key_pins: None,
                dirs: ToolDirs::default(),
                wsl: Vec::new(),
            },
            DEBUG_ONLY,
            &[AgentToolId::Cursor],
        )
        .unwrap_err();
        assert!(error.contains("refusing to overwrite"));
        assert_eq!(
            fs::read_to_string(dest.join("SKILL.md")).unwrap(),
            "not yours"
        );
        let _ = fs::remove_dir_all(&home);
    }

    #[cfg(unix)]
    #[test]
    fn replaces_legacy_symlink_with_real_copy() {
        let home = unique_temp("agent-symlink");
        fs::create_dir_all(home.join(".cursor")).unwrap();
        let canonical = write_canonical_skill(Home::local(&home), DEBUG).unwrap();
        let dest = tool_skill_dir(Home::local(&home), DEBUG, AgentToolId::Cursor);
        fs::create_dir_all(dest.parent().unwrap()).unwrap();
        std::os::unix::fs::symlink(&canonical, &dest).unwrap();
        assert!(fs::symlink_metadata(&dest)
            .unwrap()
            .file_type()
            .is_symlink());

        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        install_local(
            &InstallContext {
                home: home.clone(),
                cli_source,
                data_directory: None,
                raw_key_pins: None,
                dirs: ToolDirs::default(),
                wsl: Vec::new(),
            },
            DEBUG_ONLY,
            &[AgentToolId::Cursor],
        )
        .unwrap();
        assert_real_skill_copy(&home, DEBUG, &dest);
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn sync_requires_receipt_and_skips_unknown_tools() {
        let home = unique_temp("agent-sync");
        let canonical = canonical_skill_dir(&home, DEBUG);
        write_skill_tree(Home::local(&home), DEBUG, &canonical).unwrap();
        let skill = canonical.join("SKILL.md");
        fs::write(&skill, "stale-managed").unwrap();
        let mut hashes = managed_hashes(&canonical);
        hashes.insert("SKILL.md".into(), sha256_hex(b"stale-managed"));
        write_managed_manifest(&canonical, DEBUG, &hashes).unwrap();

        sync_installed_skills(Home::local(&home)).unwrap();
        assert_eq!(fs::read_to_string(&skill).unwrap(), "stale-managed");
        assert!(!tool_skill_dir(Home::local(&home), DEBUG, AgentToolId::Cursor).exists());

        write_json_file(
            &receipt_path(&home),
            &InstallReceipt {
                version: RECEIPT_VERSION,
                skills: vec![ReceiptSkill {
                    id: AgentSkillId::AstrlinkDebug,
                    version: DEBUG.version.to_string(),
                }],
                installed_at_unix: 1,
                cli_binary: Some("astrlink".into()),
                files: vec![],
                wsl: Vec::new(),
            },
        )
        .unwrap();
        sync_installed_skills(Home::local(&home)).unwrap();
        assert_eq!(fs::read_to_string(&skill).unwrap(), rendered_skill(&home));
        assert!(!tool_skill_dir(Home::local(&home), DEBUG, AgentToolId::Cursor).exists());
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn sync_cli_binary_requires_receipt() {
        let home = unique_temp("agent-sync-cli");
        let dest = cli_binary_dest(&home);
        let stale = home.join("stale-astrlink-cli");
        let next = home.join("next-astrlink-cli");
        fs::write(&stale, b"stale").unwrap();
        fs::write(&next, b"next").unwrap();
        let context = InstallContext {
            home: home.clone(),
            cli_source: next,
            data_directory: None,
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };

        sync_installed_cli(&context).unwrap();
        assert!(!dest.exists());

        write_json_file(
            &receipt_path(&home),
            &InstallReceipt {
                version: RECEIPT_VERSION,
                skills: vec![ReceiptSkill {
                    id: AgentSkillId::AstrlinkDebug,
                    version: DEBUG.version.to_string(),
                }],
                installed_at_unix: 1,
                cli_binary: Some("astrlink".into()),
                files: vec![],
                wsl: Vec::new(),
            },
        )
        .unwrap();
        copy_cli_binary(&stale, &dest).unwrap();
        sync_installed_cli(&context).unwrap();
        assert_eq!(fs::read(&dest).unwrap(), b"next");
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                fs::metadata(&dest).unwrap().permissions().mode() & 0o777,
                0o755
            );
        }

        // A dev build without a sidecar keeps the installed CLI.
        sync_installed_cli(&InstallContext {
            cli_source: home.join("missing"),
            ..context
        })
        .unwrap();
        assert_eq!(fs::read(&dest).unwrap(), b"next");
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn rejects_empty_or_undetected_selection_before_writing() {
        let home = unique_temp("agent-skip");
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            home: home.clone(),
            cli_source,
            data_directory: None,
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };
        fs::create_dir_all(home.join(".grok")).unwrap();
        assert!(install_local(&context, &[], &[AgentToolId::Grok])
            .unwrap_err()
            .contains("select at least one skill"));
        assert!(install_local(&context, DEBUG_ONLY, &[])
            .unwrap_err()
            .contains("select at least one agent tool"));
        assert!(install_local(
            &context,
            DEBUG_ONLY,
            &[AgentToolId::Grok, AgentToolId::Cursor]
        )
        .unwrap_err()
        .contains("no longer detected"));
        assert!(!home.join(".cursor").exists());
        assert!(!canonical_skill_dir(&home, DEBUG).exists());
        assert!(!cli_binary_dest(&home).exists());
        assert!(!receipt_path(&home).exists());
        assert!(!tool_skill_dir(Home::local(&home), DEBUG, AgentToolId::Grok).exists());
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn installs_only_selected_tools_and_startup_preserves_scope() {
        let both = AgentSkillId::all();
        let placeholder_only = [AgentSkillId::RedactionPlaceholders];
        for (skills, selected) in [
            (DEBUG_ONLY, vec![AgentToolId::Grok]),
            (DEBUG_ONLY, vec![AgentToolId::Cursor, AgentToolId::Grok]),
            (DEBUG_ONLY, vec![AgentToolId::Codex]),
            (
                DEBUG_ONLY,
                vec![AgentToolId::Claude, AgentToolId::Grok, AgentToolId::Grok],
            ),
            (DEBUG_ONLY, vec![AgentToolId::Pi]),
            (&both[..], vec![AgentToolId::Claude, AgentToolId::Pi]),
            (
                &placeholder_only[..],
                vec![AgentToolId::Claude, AgentToolId::Codex],
            ),
        ] {
            let home = unique_temp("agent-selected");
            for dir in [".cursor", ".claude", ".codex", ".grok", ".pi"] {
                fs::create_dir_all(home.join(dir)).unwrap();
            }
            let cli_source = home.join("src-astrlink-cli");
            fs::write(&cli_source, b"cli").unwrap();
            let context = InstallContext {
                home,
                cli_source,
                data_directory: None,
                raw_key_pins: None,
                dirs: ToolDirs::default(),
                wsl: Vec::new(),
            };
            let before = status(&context);
            let mut expected_paths = before.shared_paths;
            for tool in before
                .tools
                .iter()
                .filter(|tool| selected.contains(&tool.id))
            {
                for skill in tool
                    .skills
                    .iter()
                    .filter(|skill| skills.contains(&skill.id))
                {
                    expected_paths.extend(skill.preview_paths.clone());
                }
            }
            let receipt = install_local(&context, skills, &selected).unwrap();
            assert_eq!(
                receipt.files.into_iter().collect::<BTreeSet<_>>(),
                expected_paths.into_iter().collect::<BTreeSet<_>>()
            );
            sync_installed_skills(Home::local(&context.home)).unwrap();
            sync_installed_cli(&context).unwrap();
            sync_installed_host_guards(Home::local(&context.home), None, None).unwrap();
            let after = status(&context);
            let needs_cli = skills.contains(&AgentSkillId::AstrlinkDebug);
            let shared = selected.iter().any(|id| id.uses_shared_skills());
            for tool in after.tools {
                let chosen = selected.contains(&tool.id);
                for skill in &tool.skills {
                    // Codex and Pi share a directory, so choosing either one
                    // installs the skill for both.
                    let installed = skills.contains(&skill.id)
                        && (chosen || tool.id.uses_shared_skills() && shared);
                    assert_eq!(skill.installed, installed, "{:?} {:?}", tool.id, skill.id);
                }
                assert_eq!(
                    tool.cli_access_installed,
                    needs_cli
                        && chosen
                        && matches!(
                            tool.cli_access,
                            AgentCliAccessKind::AllowRules | AgentCliAccessKind::ExecPolicy
                        ),
                    "{:?}",
                    tool.id
                );
                assert_eq!(
                    tool.guard_installed,
                    needs_cli && chosen && tool.guard != AgentGuardKind::SkillOnly,
                    "{:?}",
                    tool.id
                );
            }
            assert_eq!(cli_binary_dest(&context.home).exists(), needs_cli);
            assert_eq!(
                codex_rules_path(Home::local(&context.home)).exists(),
                needs_cli && selected.contains(&AgentToolId::Codex)
            );
            uninstall(&context).unwrap();
            assert!(!cli_binary_dest(&context.home).exists());
            assert!(status(&context).tools.iter().all(|tool| {
                tool.skills.iter().all(|skill| !skill.installed) && !tool.cli_access_installed
            }));
            let _ = fs::remove_dir_all(&context.home);
        }
    }

    #[test]
    fn selecting_grok_preserves_existing_unselected_installations() {
        let context = installed_codex_context("agent-unselected");
        let home = &context.home;
        fs::create_dir_all(home.join(".grok")).unwrap();
        fs::create_dir_all(home.join(".cursor")).unwrap();
        let cursor_config = legacy_mcp_config_path(home, AgentToolId::Cursor).unwrap();
        fs::write(&cursor_config, "invalid JSON must remain untouched").unwrap();
        let canonical = canonical_skill_dir(home, DEBUG);
        let legacy = legacy_codex_skill_dir(home, DEBUG);
        write_skill_tree(Home::local(home), DEBUG, &legacy).unwrap();
        let tracked = [
            canonical.join("SKILL.md"),
            managed_files_path(&canonical),
            legacy.join("SKILL.md"),
            codex_rules_path(Home::local(home)),
            codex_agents_path(Home::local(home)),
            cursor_config,
        ];
        let before = tracked
            .iter()
            .map(|path| fs::read(path).unwrap())
            .collect::<Vec<_>>();
        install_local(&context, DEBUG_ONLY, &[AgentToolId::Grok]).unwrap();
        for (path, bytes) in tracked.iter().zip(before) {
            assert_eq!(fs::read(path).unwrap(), bytes);
        }
        assert!(status(&context)
            .tools
            .iter()
            .filter(|tool| [AgentToolId::Codex, AgentToolId::Grok].contains(&tool.id))
            .all(|tool| tool.skill(AgentSkillId::AstrlinkDebug).installed));
        assert!(codex_backups(home).is_empty());
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn bundles_match_their_manifests_and_the_placeholder_skill_stays_neutral() {
        for skill in AgentSkillId::all() {
            let bundle = skill.bundle();
            let manifest: Value =
                serde_json::from_str(bundle_file(bundle, "manifest.json")).unwrap();
            assert_eq!(manifest["name"], bundle.name);
            assert_eq!(manifest["version"], bundle.version);
            assert_eq!(
                serde_json::from_value::<Vec<AgentToolId>>(manifest["adapters"].clone()).unwrap(),
                AgentToolId::all(),
                "{}",
                bundle.name
            );
            assert_eq!(serde_json::to_value(skill).unwrap(), bundle.name);
            assert!(bundle_file(bundle, "SKILL.md")
                .starts_with(&format!("---\nname: {}\n", bundle.name)));
            assert_eq!(
                bundle.needs_cli,
                bundle
                    .files
                    .iter()
                    .any(|file| file.contents.contains(CLI_PLACEHOLDER)),
                "{}",
                bundle.name
            );
        }
        // Hosts may send the skill upstream, so it must not name the gateway.
        for file in PLACEHOLDER_BUNDLE.files {
            assert!(
                !file.contents.to_lowercase().contains("astrlink"),
                "{}",
                file.relative
            );
        }
    }

    #[test]
    fn placeholder_skill_alone_brings_no_cli_rules_or_guards() {
        let home = unique_temp("agent-placeholder-only");
        for dir in [".cursor", ".claude", ".codex", ".grok", ".pi"] {
            fs::create_dir_all(home.join(dir)).unwrap();
        }
        let settings = claude_settings_path(Home::local(&home));
        let user_settings = r#"{"permissions":{"allow":["Bash(ls *)"]}}"#;
        fs::write(&settings, user_settings).unwrap();
        let placeholder = &PLACEHOLDER_BUNDLE;
        // No sidecar is needed for a skill that never runs the CLI.
        let context = InstallContext {
            home: home.clone(),
            cli_source: home.join("missing"),
            data_directory: Some(home.join("data")),
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };
        let receipt = install_local(
            &context,
            &[AgentSkillId::RedactionPlaceholders],
            &AgentToolId::all(),
        )
        .unwrap();
        assert_eq!(receipt.cli_binary, None);
        assert_eq!(
            receipt.skills,
            [ReceiptSkill {
                id: AgentSkillId::RedactionPlaceholders,
                version: placeholder.version.to_string(),
            }]
        );
        let after = status(&context);
        let mut expected_paths = after.shared_paths.clone();
        for tool in &after.tools {
            let skill = tool.skill(AgentSkillId::RedactionPlaceholders);
            assert!(skill.installed, "{:?}", tool.id);
            assert!(!tool.skill(AgentSkillId::AstrlinkDebug).installed);
            assert!(!tool.cli_access_installed && !tool.guard_installed);
            expected_paths.extend(skill.preview_paths.clone());
            assert_real_skill_copy(
                &home,
                placeholder,
                &tool_skill_dir(Home::local(&home), placeholder, tool.id),
            );
            assert!(!tool_skill_dir(Home::local(&home), DEBUG, tool.id).exists());
        }
        assert_eq!(
            receipt.files.iter().cloned().collect::<BTreeSet<_>>(),
            expected_paths.into_iter().collect::<BTreeSet<_>>()
        );
        let untouched = |home: &Path| {
            assert!(!cli_binary_dest(home).exists());
            assert!(!codex_rules_path(Home::local(home)).exists());
            assert!(!codex_agents_path(Home::local(home)).exists());
            assert!(!host_guards_path(home).exists());
            assert_eq!(
                fs::read_to_string(claude_settings_path(Home::local(home))).unwrap(),
                user_settings
            );
        };
        untouched(&home);

        // Startup keeps the install skill-only once a sidecar exists.
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            cli_source,
            ..context
        };
        sync_installed_skills(Home::local(&home)).unwrap();
        sync_installed_cli(&context).unwrap();
        sync_installed_host_guards(Home::local(&home), context.data_directory.as_deref(), None)
            .unwrap();
        untouched(&home);

        uninstall(&context).unwrap();
        for id in AgentToolId::all() {
            assert!(
                !tool_skill_dir(Home::local(&home), placeholder, id).exists(),
                "{id:?}"
            );
        }
        assert_eq!(fs::read_to_string(&settings).unwrap(), user_settings);
        assert!(!receipt_path(&home).exists());
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn pi_uses_the_shared_directory_and_leaves_other_hosts_alone() {
        let home = unique_temp("agent-pi");
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            home: home.clone(),
            cli_source,
            data_directory: None,
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };
        let pi = |context: &InstallContext| {
            status(context)
                .tools
                .into_iter()
                .find(|tool| tool.id == AgentToolId::Pi)
                .unwrap()
        };
        assert!(!pi(&context).detected);
        assert!(install_local(&context, DEBUG_ONLY, &[AgentToolId::Pi])
            .unwrap_err()
            .contains("no longer detected"));

        for dir in [".pi", ".cursor", ".claude", ".codex", ".grok"] {
            fs::create_dir_all(home.join(dir)).unwrap();
        }
        let settings = claude_settings_path(Home::local(&home));
        fs::write(&settings, "{}").unwrap();
        let receipt = install_local(&context, &AgentSkillId::all(), &[AgentToolId::Pi]).unwrap();
        for skill in AgentSkillId::all() {
            let bundle = skill.bundle();
            let shared = home.join(".agents").join("skills").join(bundle.name);
            assert_eq!(
                tool_skill_dir(Home::local(&home), bundle, AgentToolId::Pi),
                shared
            );
            assert_real_skill_copy(&home, bundle, &shared);
            assert!(receipt.files.contains(&display_path(&shared).unwrap()));
            for id in [AgentToolId::Cursor, AgentToolId::Claude, AgentToolId::Grok] {
                assert!(
                    !tool_skill_dir(Home::local(&home), bundle, id).exists(),
                    "{id:?}"
                );
            }
            assert!(!legacy_codex_skill_dir(&home, bundle).exists());
            assert!(!home.join(".pi").join("agent").exists());
        }
        // Pi runs commands without asking, so it needs the CLI but no rules,
        // and the Claude Code and Codex files stay as they were.
        assert!(cli_binary_dest(&home).is_file());
        assert!(!codex_rules_path(Home::local(&home)).exists());
        assert!(!codex_agents_path(Home::local(&home)).exists());
        assert!(!host_guards_path(&home).exists());
        assert_eq!(fs::read_to_string(&settings).unwrap(), "{}");
        let status = pi(&context);
        assert!(status.detected && status.skills.iter().all(|skill| skill.installed));
        assert_eq!(
            (status.cli_access, status.cli_access_installed),
            (AgentCliAccessKind::Unrestricted, false)
        );
        assert_eq!(
            (status.guard, status.guard_installed),
            (AgentGuardKind::SkillOnly, false)
        );

        uninstall(&context).unwrap();
        for skill in AgentSkillId::all() {
            assert!(!canonical_skill_dir(&home, skill.bundle()).exists());
        }
        assert!(pi(&context).skills.iter().all(|skill| !skill.installed));
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn a_shared_skill_counts_only_for_a_detected_tool() {
        let context = installed_codex_context("agent-shared-undetected");
        let home = context.home.clone();
        let tool = |id: AgentToolId| {
            status(&context)
                .tools
                .into_iter()
                .find(|tool| tool.id == id)
                .unwrap()
        };
        // Codex put the skill in ~/.agents/skills, which Pi also reads, but
        // Pi is not installed.
        assert!(canonical_skill_dir(&home, DEBUG).join("SKILL.md").is_file());
        assert!(
            tool(AgentToolId::Codex)
                .skill(AgentSkillId::AstrlinkDebug)
                .installed
        );
        let pi = tool(AgentToolId::Pi);
        assert!(!pi.detected);
        assert!(pi.skills.iter().all(|skill| !skill.installed));

        fs::create_dir_all(home.join(".pi")).unwrap();
        assert!(
            tool(AgentToolId::Pi)
                .skill(AgentSkillId::AstrlinkDebug)
                .installed
        );
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn a_later_skill_only_install_keeps_the_earlier_cli_install() {
        let context = installed_codex_context("agent-later-placeholder");
        let home = context.home.clone();
        fs::create_dir_all(home.join(".pi")).unwrap();
        let canonical = canonical_skill_dir(&home, DEBUG);
        let tracked = [
            canonical.join("SKILL.md"),
            managed_files_path(&canonical),
            codex_rules_path(Home::local(&home)),
            codex_agents_path(Home::local(&home)),
            host_guards_path(&home),
            cli_binary_dest(&home),
        ];
        let before = tracked
            .iter()
            .map(|path| fs::read(path).unwrap())
            .collect::<Vec<_>>();
        let receipt = install_local(
            &context,
            &[AgentSkillId::RedactionPlaceholders],
            &[AgentToolId::Pi],
        )
        .unwrap();
        assert_eq!(receipt.cli_binary, None);
        for (path, bytes) in tracked.iter().zip(before) {
            assert_eq!(fs::read(path).unwrap(), bytes, "{path:?}");
        }
        let codex = status(&context)
            .tools
            .into_iter()
            .find(|tool| tool.id == AgentToolId::Codex)
            .unwrap();
        assert!(codex.skills.iter().all(|skill| skill.installed));
        assert!(codex.cli_access_installed && codex.guard_installed);

        // Startup still keeps the CLI the earlier install copied current.
        let next = home.join("next-astrlink-cli");
        fs::write(&next, b"next").unwrap();
        sync_installed_cli(&InstallContext {
            cli_source: next,
            ..context
        })
        .unwrap();
        assert_eq!(fs::read(cli_binary_dest(&home)).unwrap(), b"next");
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn startup_refreshes_every_bundle_and_keeps_user_edits() {
        let home = unique_temp("agent-sync-bundles");
        for dir in [".claude", ".codex"] {
            fs::create_dir_all(home.join(dir)).unwrap();
        }
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            home: home.clone(),
            cli_source,
            data_directory: None,
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };
        install_local(
            &context,
            &AgentSkillId::all(),
            &[AgentToolId::Claude, AgentToolId::Codex],
        )
        .unwrap();
        let dirs = |bundle| {
            [
                tool_skill_dir(Home::local(&home), bundle, AgentToolId::Claude),
                canonical_skill_dir(&home, bundle),
            ]
        };
        for skill in AgentSkillId::all() {
            let bundle = skill.bundle();
            for dir in dirs(bundle) {
                fs::write(dir.join("manifest.json"), "stale-managed").unwrap();
                let mut hashes = managed_hashes(&dir);
                hashes.insert("manifest.json".into(), sha256_hex(b"stale-managed"));
                write_managed_manifest(&dir, bundle, &hashes).unwrap();
                fs::write(dir.join("SKILL.md"), "user edit").unwrap();
            }
        }
        sync_installed_skills(Home::local(&home)).unwrap();
        for skill in AgentSkillId::all() {
            let bundle = skill.bundle();
            for dir in dirs(bundle) {
                assert_eq!(
                    fs::read_to_string(dir.join("manifest.json")).unwrap(),
                    bundle_file(bundle, "manifest.json"),
                    "{dir:?}"
                );
                assert_eq!(
                    fs::read_to_string(dir.join("SKILL.md")).unwrap(),
                    "user edit",
                    "{dir:?}"
                );
            }
        }
        // A reinstall keeps the edits too.
        install_local(&context, &AgentSkillId::all(), &[AgentToolId::Claude]).unwrap();
        for skill in AgentSkillId::all() {
            let dir = tool_skill_dir(Home::local(&home), skill.bundle(), AgentToolId::Claude);
            assert_eq!(
                fs::read_to_string(dir.join("SKILL.md")).unwrap(),
                "user edit"
            );
        }
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn claude_patterns_follow_the_documented_path_forms() {
        assert_eq!(
            claude_absolute_pattern("/Users/a/Library/Application Support/com.x", false).as_deref(),
            Some("//Users/a/Library/Application Support/com.x")
        );
        assert_eq!(
            claude_absolute_pattern(r"C:\Users\a\AppData\Roaming\com.x", true).as_deref(),
            Some("//c/Users/a/AppData/Roaming/com.x")
        );
        assert_eq!(
            claude_absolute_pattern(r"\\?\D:\data\", true).as_deref(),
            Some("//d/data")
        );
        assert_eq!(
            claude_absolute_pattern("/srv/a[1]*?(x)", false).as_deref(),
            Some(r"//srv/a\[1\]\*\?\(x\)")
        );
        assert_eq!(claude_absolute_pattern(r"\\server\share\x", true), None);
        assert_eq!(claude_absolute_pattern("relative/path", false), None);
        assert_eq!(claude_absolute_pattern("/", false), None);

        let rules = claude_deny_rules(desktop(), Some(Path::new("/data/astrlink")), None);
        if cfg!(windows) {
            assert_eq!(rules.len(), 3);
        } else {
            assert_eq!(rules[0], "Read(//data/astrlink/**)");
        }
        assert!(rules.contains(&"Read(~/.astrlink/control-session.json)".to_string()));
        assert!(rules.contains(&"Bash(sqlite3 *)".to_string()));
        assert!(rules.contains(&"Bash(sqlite3*)".to_string()));
        assert_eq!(claude_deny_rules(desktop(), None, None).len(), 3);

        // The pin file sits apart from the data on Linux and gets the same
        // kind of rule as the data directory, and only that kind.
        let pins = Path::new("/config/astrlink/raw-key-pins.json");
        let with_pins = claude_deny_rules(desktop(), Some(Path::new("/data/astrlink")), Some(pins));
        let pin_rules = with_pins
            .iter()
            .filter(|rule| rule.contains("raw-key-pins.json"))
            .collect::<Vec<_>>();
        if cfg!(windows) {
            assert!(pin_rules.is_empty());
        } else {
            assert_eq!(pin_rules, ["Read(//config/astrlink/raw-key-pins.json)"]);
            assert_eq!(with_pins.len(), rules.len() + 1);
        }
        assert_eq!(
            claude_deny_rules(desktop(), None, Some(pins))[1..],
            rules[1..]
        );
    }

    #[test]
    fn claude_deny_merge_keeps_user_rules_and_removes_only_its_own() {
        let existing = r#"{"model":"opus","permissions":{"allow":["Bash(ls *)"],"deny":["Read(./.env)","Bash(sqlite3 *)"]}}"#;
        let rules = vec![
            "Read(//data/**)".to_string(),
            "Bash(sqlite3 *)".to_string(),
            "Bash(sqlite3*)".to_string(),
        ];
        let (merged, record) =
            merge_claude_settings_rules(Some(existing), "deny", &rules, None).unwrap();
        let value: Value = serde_json::from_str(&merged).unwrap();
        let deny = value["permissions"]["deny"].as_array().unwrap();
        assert_eq!(
            deny.iter().filter_map(Value::as_str).collect::<Vec<_>>(),
            [
                "Read(./.env)",
                "Bash(sqlite3 *)",
                "Read(//data/**)",
                "Bash(sqlite3*)"
            ]
        );
        // The user already had `Bash(sqlite3 *)`, so uninstall must keep it.
        assert_eq!(record.managed, ["Read(//data/**)", "Bash(sqlite3*)"]);
        assert!(!record.created_file && !record.created_permissions && !record.created_list);

        let (again, same) =
            merge_claude_settings_rules(Some(&merged), "deny", &rules, Some(&record)).unwrap();
        assert_eq!(again, merged);
        assert_eq!(same, record);

        let restored = remove_claude_settings_rules(&merged, "deny", &record)
            .unwrap()
            .unwrap();
        assert_eq!(
            serde_json::from_str::<Value>(&restored).unwrap(),
            serde_json::from_str::<Value>(existing).unwrap()
        );
    }

    #[test]
    fn claude_deny_merge_replaces_stale_rules_and_cleans_up_what_it_created() {
        let first = vec!["Read(//old/**)".to_string(), "Bash(sqlite3*)".to_string()];
        let (merged, record) = merge_claude_settings_rules(None, "deny", &first, None).unwrap();
        assert!(record.created_file && record.created_permissions && record.created_list);
        let next = vec!["Read(//new/**)".to_string(), "Bash(sqlite3*)".to_string()];
        let (moved, record) =
            merge_claude_settings_rules(Some(&merged), "deny", &next, Some(&record)).unwrap();
        assert!(!moved.contains("//old/"));
        assert!(moved.contains("//new/"));
        assert_eq!(record.managed, next);
        assert!(record.created_file);
        assert_eq!(
            remove_claude_settings_rules(&moved, "deny", &record).unwrap(),
            None
        );

        // A key the user added later keeps the file alive.
        let mut value: Value = serde_json::from_str(&moved).unwrap();
        value["theme"] = json!("dark");
        let kept = remove_claude_settings_rules(&value.to_string(), "deny", &record)
            .unwrap()
            .unwrap();
        assert_eq!(
            serde_json::from_str::<Value>(&kept).unwrap(),
            json!({"theme":"dark"})
        );

        for invalid in [
            "{not json",
            "[]",
            r#"{"permissions":[]}"#,
            r#"{"permissions":{"deny":{}}}"#,
        ] {
            assert!(
                merge_claude_settings_rules(Some(invalid), "deny", &next, None).is_err(),
                "{invalid}"
            );
        }
    }

    #[test]
    fn codex_guard_block_round_trips_and_replaces_itself() {
        let user = "# My rules\n\nBe terse.\n";
        let block = codex_guard_block(desktop(), Some(Path::new("/data/astrlink")), None);
        assert!(block.contains("/data/astrlink"));
        assert!(block.contains("observer access"));
        assert!(!block.contains("raw key pin file"));
        let pinned = codex_guard_block(
            desktop(),
            Some(Path::new("/data/astrlink")),
            Some(Path::new("/config/astrlink/raw-key-pins.json")),
        );
        assert!(pinned.contains(
            "Do not read, copy, search, or open AstrLink's data directory (`/data/astrlink`), \
             its raw key pin file (`/config/astrlink/raw-key-pins.json`), any `astrlink.db*` file"
        ));
        let merged = merge_codex_agents_guard(Some(user), &block);
        assert!(merged.starts_with(user));
        assert_eq!(remove_codex_agents_guard(&merged), user);

        let other = codex_guard_block(desktop(), Some(Path::new("/elsewhere")), None);
        let replaced = merge_codex_agents_guard(Some(&merged), &other);
        assert_eq!(replaced.matches(CODEX_GUARD_BEGIN).count(), 1);
        assert!(replaced.contains("/elsewhere") && !replaced.contains("/data/astrlink"));

        let fresh = merge_codex_agents_guard(None, &block);
        assert_eq!(remove_codex_agents_guard(&fresh), "");
        let middle = format!("before\n\n{block}\n\nafter\n");
        assert_eq!(remove_codex_agents_guard(&middle), "before\n\nafter\n");
    }

    #[test]
    fn install_writes_host_guards_and_uninstall_restores_user_files() {
        let home = unique_temp("agent-guards");
        for dir in [".cursor", ".claude", ".codex", ".grok", ".pi"] {
            fs::create_dir_all(home.join(dir)).unwrap();
        }
        let settings = claude_settings_path(Home::local(&home));
        let user_settings = r#"{"permissions":{"deny":["Read(~/.ssh/**)"]},"env":{"A":"1"}}"#;
        fs::write(&settings, user_settings).unwrap();
        let agents = codex_agents_path(Home::local(&home));
        let user_agents = "Always run tests.\n";
        fs::write(&agents, user_agents).unwrap();
        let data = home.join("data");
        let pins = home.join("config").join("raw-key-pins.json");
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            home: home.clone(),
            cli_source,
            data_directory: Some(data.clone()),
            raw_key_pins: Some(pins.clone()),
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };

        let before = status(&context);
        assert!(before.tools.iter().all(|tool| !tool.guard_installed));
        let receipt = install_local(&context, DEBUG_ONLY, &AgentToolId::all()).unwrap();
        let codex_rules_file = codex_rules_path(Home::local(&home));
        for path in [
            &settings,
            &agents,
            &codex_rules_file,
            &host_guards_path(&home),
        ] {
            assert!(
                receipt.files.contains(&display_path(path).unwrap()),
                "{path:?}"
            );
        }
        let after = status(&context);
        for tool in &after.tools {
            let expected = match tool.id {
                AgentToolId::Claude => (AgentGuardKind::DenyRules, true),
                AgentToolId::Codex => (AgentGuardKind::Instructions, true),
                _ => (AgentGuardKind::SkillOnly, false),
            };
            assert_eq!(
                (tool.guard, tool.guard_installed),
                expected,
                "{:?}",
                tool.id
            );
        }
        let merged: Value = serde_json::from_str(&fs::read_to_string(&settings).unwrap()).unwrap();
        let deny = merged["permissions"]["deny"].as_array().unwrap();
        assert_eq!(deny[0], "Read(~/.ssh/**)");
        let rules = claude_deny_rules(desktop(), Some(&data), Some(&pins));
        assert!(rules.iter().any(|rule| rule.contains("raw-key-pins.json")));
        for rule in rules {
            assert!(
                deny.iter().any(|item| item.as_str() == Some(&rule)),
                "{rule}"
            );
        }
        let allow = merged["permissions"]["allow"].as_array().unwrap();
        assert_eq!(
            allow.iter().filter_map(Value::as_str).collect::<Vec<_>>(),
            claude_allow_rules(Home::local(&home))
        );
        assert_eq!(
            fs::read_to_string(&codex_rules_file).unwrap(),
            codex_rules(Home::local(&home))
        );
        let guarded = fs::read_to_string(&agents).unwrap();
        assert!(guarded.contains(CODEX_GUARD_BEGIN));
        assert!(guarded.contains(&format!("(`{}`)", pins.display())));
        // No token or secret is ever written into host configuration.
        for path in [&settings, &agents, &codex_rules_file] {
            let raw = fs::read_to_string(path).unwrap();
            assert!(!raw.contains("control_token") && !raw.contains("Bearer"));
        }

        // Moving the data directory updates only the managed rule.
        let moved = home.join("moved-data");
        sync_installed_host_guards(Home::local(&home), Some(&moved), Some(&pins)).unwrap();
        let synced = fs::read_to_string(&settings).unwrap();
        let synced: Value = serde_json::from_str(&synced).unwrap();
        let synced_deny = synced["permissions"]["deny"].as_array().unwrap();
        let has = |rule: &str| synced_deny.iter().any(|item| item.as_str() == Some(rule));
        assert!(!has(&claude_deny_rules(desktop(), Some(&data), None)[0]));
        assert!(has(&claude_deny_rules(desktop(), Some(&moved), None)[0]));
        assert!(has(&claude_deny_rules(desktop(), None, Some(&pins))[0]));
        assert!(has("Read(~/.ssh/**)"));
        assert!(fs::read_to_string(&agents).unwrap().contains("moved-data"));

        uninstall(&context).unwrap();
        assert_eq!(
            serde_json::from_str::<Value>(&fs::read_to_string(&settings).unwrap()).unwrap(),
            serde_json::from_str::<Value>(user_settings).unwrap()
        );
        assert_eq!(fs::read_to_string(&agents).unwrap(), user_agents);
        assert!(!codex_rules_file.exists());
        assert!(!host_guards_path(&home).exists());
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn host_guards_created_by_install_are_removed_and_sync_respects_manual_removal() {
        let home = unique_temp("agent-guards-fresh");
        fs::write(home.join(".claude.json"), "{}").unwrap();
        fs::create_dir_all(home.join(".codex")).unwrap();
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            home: home.clone(),
            cli_source,
            data_directory: Some(home.join("data")),
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };
        install_local(
            &context,
            DEBUG_ONLY,
            &[AgentToolId::Claude, AgentToolId::Codex],
        )
        .unwrap();
        assert!(claude_settings_path(Home::local(&home)).is_file());
        assert!(codex_agents_path(Home::local(&home)).is_file());
        assert!(codex_rules_path(Home::local(&home)).is_file());

        // A guard or rules file the user deleted by hand stays deleted
        // across restarts.
        fs::write(codex_agents_path(Home::local(&home)), "mine\n").unwrap();
        fs::remove_file(codex_rules_path(Home::local(&home))).unwrap();
        sync_installed_host_guards(Home::local(&home), Some(&home.join("other")), None).unwrap();
        assert_eq!(
            fs::read_to_string(codex_agents_path(Home::local(&home))).unwrap(),
            "mine\n"
        );
        assert!(!codex_rules_path(Home::local(&home)).exists());
        fs::remove_file(codex_agents_path(Home::local(&home))).unwrap();

        uninstall(&context).unwrap();
        assert!(!claude_settings_path(Home::local(&home)).exists());
        assert!(!codex_agents_path(Home::local(&home)).exists());
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn sync_adds_the_raw_key_pin_file_to_guards_installed_before_it() {
        let home = unique_temp("agent-guards-pins");
        fs::create_dir_all(home.join(".claude")).unwrap();
        fs::create_dir_all(home.join(".codex")).unwrap();
        let settings = claude_settings_path(Home::local(&home));
        fs::write(&settings, r#"{"permissions":{"deny":["Read(~/.ssh/**)"]}}"#).unwrap();
        let data = home.join("data");
        let pins = home.join("config").join("raw-key-pins.json");
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let earlier = InstallContext {
            home: home.clone(),
            cli_source: cli_source.clone(),
            data_directory: Some(data.clone()),
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };
        install_local(
            &earlier,
            DEBUG_ONLY,
            &[AgentToolId::Claude, AgentToolId::Codex],
        )
        .unwrap();
        let current = InstallContext {
            raw_key_pins: Some(pins.clone()),
            ..earlier
        };
        let pin_rule = claude_deny_rules(desktop(), None, Some(&pins))[0].clone();
        assert!(pin_rule.starts_with("Read(") && pin_rule.contains("raw-key-pins.json"));
        let claude_guarded = |context: &InstallContext| {
            status(context)
                .tools
                .into_iter()
                .find(|tool| tool.id == AgentToolId::Claude)
                .unwrap()
                .guard_installed
        };
        // The earlier rules no longer count as the whole guard.
        assert!(!claude_guarded(&current));

        sync_installed_host_guards(Home::local(&home), Some(&data), Some(&pins)).unwrap();
        let synced: Value = serde_json::from_str(&fs::read_to_string(&settings).unwrap()).unwrap();
        let deny = synced["permissions"]["deny"]
            .as_array()
            .unwrap()
            .iter()
            .filter_map(Value::as_str)
            .collect::<Vec<_>>();
        assert!(deny.contains(&pin_rule.as_str()));
        assert!(deny.contains(&"Read(~/.ssh/**)"));
        assert!(read_host_guards(&home)
            .unwrap()
            .claude
            .unwrap()
            .managed
            .contains(&pin_rule));
        assert!(claude_guarded(&current));
        assert!(fs::read_to_string(codex_agents_path(Home::local(&home)))
            .unwrap()
            .contains(&format!("its raw key pin file (`{}`)", pins.display())));

        // Uninstall removes the pin rule with the rest of AstrLink's rules.
        uninstall(&current).unwrap();
        let restored: Value =
            serde_json::from_str(&fs::read_to_string(&settings).unwrap()).unwrap();
        assert_eq!(
            restored,
            json!({"permissions":{"deny":["Read(~/.ssh/**)"]}})
        );
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn sync_adds_cli_access_to_guards_installed_before_the_cli() {
        let home = unique_temp("agent-guards-cli");
        fs::create_dir_all(home.join(".claude")).unwrap();
        fs::create_dir_all(home.join(".codex")).unwrap();
        let data = home.join("data");
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            home: home.clone(),
            cli_source,
            data_directory: Some(data.clone()),
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };
        install_local(
            &context,
            DEBUG_ONLY,
            &[AgentToolId::Claude, AgentToolId::Codex],
        )
        .unwrap();
        let installed = fs::read_to_string(claude_settings_path(Home::local(&home))).unwrap();

        // Recreate what the MCP-based installer left: deny rules only, and a
        // record from before the allow list was renamed.
        let settings = claude_settings_path(Home::local(&home));
        let mut value: Value = serde_json::from_str(&installed).unwrap();
        value["permissions"]
            .as_object_mut()
            .unwrap()
            .remove("allow");
        fs::write(&settings, value.to_string()).unwrap();
        fs::remove_file(codex_rules_path(Home::local(&home))).unwrap();
        let record = read_host_guards(&home).unwrap();
        let mut old = serde_json::to_value(&record).unwrap();
        let claude = old["claude"].as_object_mut().unwrap();
        let created = claude.remove("created_list").unwrap();
        claude.insert("created_deny".into(), created);
        let old = old.as_object_mut().unwrap();
        old.remove("claude_allow");
        old.remove("codex_rules");
        old.insert("version".into(), json!(1));
        fs::write(
            host_guards_path(&home),
            Value::Object(old.clone()).to_string(),
        )
        .unwrap();
        let legacy = read_host_guards(&home).unwrap();
        assert_eq!(legacy.claude, record.claude);
        assert!(legacy.claude_allow.is_none() && !legacy.codex_rules);
        let access = |context: &InstallContext| {
            status(context)
                .tools
                .into_iter()
                .filter(|tool| tool.cli_access_installed)
                .map(|tool| tool.id)
                .collect::<Vec<_>>()
        };
        assert!(access(&context).is_empty());

        sync_installed_host_guards(Home::local(&home), Some(&data), None).unwrap();
        assert_eq!(access(&context), [AgentToolId::Claude, AgentToolId::Codex]);
        assert_eq!(
            serde_json::from_str::<Value>(&fs::read_to_string(&settings).unwrap()).unwrap(),
            serde_json::from_str::<Value>(&installed).unwrap()
        );
        let synced = read_host_guards(&home).unwrap();
        assert!(synced.codex_rules);
        // Sync created the allow list, so uninstall removes it whole.
        assert!(synced.claude_allow.unwrap().created_list);

        uninstall(&context).unwrap();
        assert!(!settings.exists());
        assert!(!codex_rules_path(Home::local(&home)).exists());
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn a_foreign_codex_rules_file_blocks_install_before_writing() {
        let home = unique_temp("agent-codex-rules-foreign");
        fs::create_dir_all(home.join(".codex")).unwrap();
        let rules = codex_rules_path(Home::local(&home));
        let foreign = "prefix_rule(pattern = [\"git\"], decision = \"allow\")\n";
        write_text(&rules, foreign).unwrap();
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            home: home.clone(),
            cli_source,
            data_directory: None,
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };
        let error = install_local(&context, DEBUG_ONLY, &[AgentToolId::Codex]).unwrap_err();
        assert!(
            error.contains("refusing to overwrite Codex rules"),
            "{error}"
        );
        assert_eq!(fs::read_to_string(&rules).unwrap(), foreign);
        assert!(!codex_agents_path(Home::local(&home)).exists());
        assert!(!host_guards_path(&home).exists());
        assert!(!receipt_path(&home).exists());

        // Uninstall and startup sync leave it alone too.
        uninstall(&context).unwrap();
        sync_installed_host_guards(Home::local(&home), None, None).unwrap();
        assert_eq!(fs::read_to_string(&rules).unwrap(), foreign);
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn invalid_claude_settings_block_install_without_rewriting_them() {
        let home = unique_temp("agent-guards-invalid");
        fs::create_dir_all(home.join(".claude")).unwrap();
        fs::write(claude_settings_path(Home::local(&home)), "{broken").unwrap();
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            home: home.clone(),
            cli_source,
            data_directory: None,
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: Vec::new(),
        };
        let error = install_local(&context, DEBUG_ONLY, &[AgentToolId::Claude]).unwrap_err();
        assert!(error.contains("will not overwrite"), "{error}");
        assert_eq!(
            fs::read_to_string(claude_settings_path(Home::local(&home))).unwrap(),
            "{broken"
        );
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn host_files_are_replaced_whole_and_keep_their_mode() {
        let home = unique_temp("agent-guards-atomic");
        let path = home.join(".claude").join("settings.json");
        write_text(&path, "{\"first\": true}\n").unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&path, fs::Permissions::from_mode(0o640)).unwrap();
        }
        write_text(&path, "{\"second\": true}\n").unwrap();
        assert_eq!(fs::read_to_string(&path).unwrap(), "{\"second\": true}\n");
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mode = fs::metadata(&path).unwrap().permissions().mode() & 0o777;
            assert_eq!(mode, 0o640);
        }
        let names: Vec<_> = fs::read_dir(path.parent().unwrap())
            .unwrap()
            .map(|entry| entry.unwrap().file_name())
            .collect();
        assert_eq!(names, ["settings.json"], "a temporary file was left behind");
        let _ = fs::remove_dir_all(&home);
    }

    // Dotfile managers keep settings behind a symlink; the link survives.
    #[cfg(unix)]
    #[test]
    fn a_symlinked_host_file_is_written_at_its_target() {
        let home = unique_temp("agent-guards-symlink");
        let target = home.join("dotfiles").join("AGENTS.md");
        fs::create_dir_all(target.parent().unwrap()).unwrap();
        fs::write(&target, "mine\n").unwrap();
        let link = home.join(".codex").join("AGENTS.md");
        fs::create_dir_all(link.parent().unwrap()).unwrap();
        std::os::unix::fs::symlink(&target, &link).unwrap();
        write_text(&link, "mine\n\nguard\n").unwrap();
        assert!(fs::symlink_metadata(&link).unwrap().is_symlink());
        assert_eq!(fs::read_to_string(&target).unwrap(), "mine\n\nguard\n");
        assert_eq!(fs::read_dir(link.parent().unwrap()).unwrap().count(), 1);
        assert_eq!(fs::read_dir(target.parent().unwrap()).unwrap().count(), 1);
        let _ = fs::remove_dir_all(&home);
    }

    fn wsl_home(share: PathBuf, dirs: ToolDirs) -> WslHome {
        WslHome {
            distribution: "Ubuntu".to_string(),
            share,
            mount_root: "/mnt".to_string(),
            dirs,
        }
    }

    #[test]
    fn wsl_tools_install_into_their_home_and_run_the_desktop_cli() {
        let host = unique_temp("agent-install-wsl-host");
        let share = unique_temp("agent-install-wsl-share");
        fs::create_dir_all(host.join(".claude")).unwrap();
        for dir in [".claude", ".codex"] {
            fs::create_dir_all(share.join(dir)).unwrap();
        }
        let cli_source = host.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            home: host.clone(),
            cli_source,
            data_directory: None,
            raw_key_pins: None,
            dirs: ToolDirs::default(),
            wsl: vec![wsl_home(share.clone(), ToolDirs::default())],
        };
        let target = Home::in_wsl(&host, &context.wsl[0]);

        let before = status(&context);
        let listed = before
            .tools
            .iter()
            .filter(|tool| tool.wsl.is_some())
            .map(|tool| (tool.id, tool.wsl.as_deref().unwrap()))
            .collect::<Vec<_>>();
        assert_eq!(
            listed,
            [
                (AgentToolId::Claude, "Ubuntu"),
                (AgentToolId::Codex, "Ubuntu")
            ]
        );
        assert_eq!(
            before
                .tools
                .iter()
                .filter(|tool| tool.wsl.is_none())
                .count(),
            AgentToolId::all().len()
        );

        let selected = [AgentToolId::Claude, AgentToolId::Codex].map(|id| AgentToolTarget {
            id,
            wsl: Some("Ubuntu".to_string()),
        });
        let receipt = install(&context, &AgentSkillId::all(), &selected).unwrap();
        assert_eq!(receipt.wsl, ["Ubuntu"]);
        assert_eq!(recorded_wsl(&host), ["Ubuntu"]);
        // The CLI stays in the desktop user's home; WSL runs it from there.
        assert!(cli_binary_dest(&host).is_file());
        assert!(!cli_binary_dest(&share).exists());
        assert!(!tool_skill_dir(Home::local(&host), DEBUG, AgentToolId::Claude).exists());
        for id in [AgentToolId::Claude, AgentToolId::Codex] {
            let dir = tool_skill_dir(target, DEBUG, id);
            assert!(dir.starts_with(&share));
            assert_eq!(
                fs::read_to_string(dir.join("SKILL.md")).unwrap(),
                bundle_file(DEBUG, "SKILL.md").replace(CLI_PLACEHOLDER, &cli_command(target))
            );
        }
        assert!(receipt_path(&share).is_file());
        assert!(claude_rules_present(
            &claude_settings_path(target),
            "allow",
            &claude_allow_rules(target)
        ));
        assert_eq!(
            fs::read_to_string(codex_rules_path(target)).unwrap(),
            codex_rules(target)
        );

        let after = status(&context);
        for tool in after.tools.iter().filter(|tool| tool.wsl.is_some()) {
            assert!(
                tool.skills.iter().all(|skill| skill.installed),
                "{:?}",
                tool.id
            );
            assert!(
                tool.cli_access_installed && tool.guard_installed,
                "{:?}",
                tool.id
            );
            assert!(tool
                .skill(AgentSkillId::AstrlinkDebug)
                .preview_paths
                .contains(&display_path(&receipt_path(&share)).unwrap()));
        }
        let host_claude = after
            .tools
            .iter()
            .find(|tool| tool.wsl.is_none() && tool.id == AgentToolId::Claude)
            .unwrap();
        assert!(host_claude.detected && !host_claude.skills.iter().any(|skill| skill.installed));

        // A distribution that went away fails the install instead of being skipped.
        let gone = InstallContext {
            wsl: Vec::new(),
            ..context
        };
        assert!(install(&gone, DEBUG_ONLY, &selected[..1])
            .unwrap_err()
            .contains("no longer available"));
        let context = InstallContext {
            wsl: vec![wsl_home(share.clone(), ToolDirs::default())],
            ..gone
        };

        uninstall(&context).unwrap();
        let target = Home::in_wsl(&host, &context.wsl[0]);
        assert!(!tool_skill_dir(target, DEBUG, AgentToolId::Claude).exists());
        assert!(!canonical_skill_dir(&share, DEBUG).exists());
        assert!(!codex_rules_path(target).exists());
        assert!(!claude_settings_path(target).exists());
        assert!(!receipt_path(&share).exists());
        assert!(!receipt_path(&host).exists());
        assert!(!cli_binary_dest(&host).exists());
        assert!(share.join(".claude").is_dir() && share.join(".codex").is_dir());
        fs::remove_dir_all(host).unwrap();
        fs::remove_dir_all(share).unwrap();
    }

    #[test]
    fn wsl_rules_reach_desktop_files_through_the_drive_mounts() {
        let wsl = wsl_home(
            PathBuf::from(r"\\wsl$\Ubuntu\home\ada"),
            ToolDirs::default(),
        );
        let target = Home::in_wsl(Path::new(r"C:\Users\Ada Lovelace"), &wsl);
        let exe = if cfg!(windows) { ".exe" } else { "" };
        let cli = format!("/mnt/c/Users/Ada Lovelace/.astrlink/bin/astrlink{exe}");
        assert_eq!(cli_path(target), cli);
        assert_eq!(cli_command(target), format!("'{cli}'"));
        assert_eq!(claude_allow_rules(target), [format!("Bash('{cli}' *)")]);
        assert!(codex_rules(target).contains(&serde_json::to_string(&cli).unwrap()));

        let data = Path::new(r"C:\Users\Ada Lovelace\AppData\Roaming\AstrLink");
        let rules = claude_deny_rules(target, Some(data), None);
        assert_eq!(
            rules[0],
            "Read(//mnt/c/Users/Ada Lovelace/AppData/Roaming/AstrLink/**)"
        );
        assert_eq!(
            rules[1],
            "Read(//mnt/c/Users/Ada Lovelace/.astrlink/control-session.json)"
        );
        let block = codex_guard_block(target, Some(data), None);
        assert!(block.contains("`/mnt/c/Users/Ada Lovelace/AppData/Roaming/AstrLink`"));
        assert!(block.contains("`/mnt/c/Users/Ada Lovelace/.astrlink/control-session.json`"));
        assert!(!block.contains("~/.astrlink"));
    }

    #[test]
    fn moved_tool_dirs_take_skills_and_rules() {
        let home = unique_temp("agent-install-dirs");
        let claude = home.join("work").join("claude");
        let codex = home.join("work").join("codex");
        let pi = home.join("work").join("pi-agent");
        for dir in [&claude, &codex, &pi] {
            fs::create_dir_all(dir).unwrap();
        }
        // The default directories exist too, and stay untouched.
        fs::create_dir_all(home.join(".claude")).unwrap();
        let cli_source = home.join("src-astrlink-cli");
        fs::write(&cli_source, b"cli").unwrap();
        let context = InstallContext {
            home: home.clone(),
            cli_source,
            data_directory: None,
            raw_key_pins: None,
            dirs: ToolDirs {
                claude: Some(claude.clone()),
                codex: Some(codex.clone()),
                grok: Some(home.join("missing-grok")),
                pi: Some(pi),
            },
            wsl: Vec::new(),
        };
        let detected = status(&context)
            .tools
            .into_iter()
            .filter(|tool| tool.detected)
            .map(|tool| tool.id)
            .collect::<Vec<_>>();
        assert_eq!(
            detected,
            [AgentToolId::Claude, AgentToolId::Codex, AgentToolId::Pi]
        );

        install_local(&context, DEBUG_ONLY, &detected).unwrap();
        assert!(claude
            .join("skills")
            .join(DEBUG.name)
            .join("SKILL.md")
            .is_file());
        assert!(claude.join("settings.json").is_file());
        assert!(codex.join("rules").join("astrlink.rules").is_file());
        assert!(codex.join("AGENTS.md").is_file());
        assert!(canonical_skill_dir(&home, DEBUG).join("SKILL.md").is_file());
        assert!(!home.join(".claude").join("skills").exists());
        assert!(!home.join(".claude").join("settings.json").exists());
        assert!(!home.join(".codex").exists());
        let installed = status(&context);
        for tool in installed.tools.iter().filter(|tool| tool.detected) {
            assert!(
                tool.skill(AgentSkillId::AstrlinkDebug).installed,
                "{:?}",
                tool.id
            );
        }

        uninstall(&context).unwrap();
        assert!(!claude.join("skills").join(DEBUG.name).exists());
        assert!(!claude.join("settings.json").exists());
        assert!(!codex.join("rules").join("astrlink.rules").exists());
        assert!(!codex.join("AGENTS.md").exists());
        fs::remove_dir_all(home).unwrap();
    }

    fn unique_temp(name: &str) -> PathBuf {
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|duration| duration.as_nanos())
            .unwrap_or(0);
        let path = std::env::temp_dir().join(format!(
            "astrlink-agent-install-{}-{}-{}",
            name,
            std::process::id(),
            nanos
        ));
        let _ = fs::remove_dir_all(&path);
        fs::create_dir_all(&path).unwrap();
        path
    }

    fn bundle_file(bundle: &SkillBundle, relative: &str) -> &'static str {
        bundle
            .files
            .iter()
            .find(|file| file.relative == relative)
            .unwrap()
            .contents
    }

    fn rendered_skill(home: &Path) -> String {
        bundle_file(DEBUG, "SKILL.md").replace(CLI_PLACEHOLDER, &cli_command(Home::local(home)))
    }

    fn assert_real_skill_copy(home: &Path, bundle: &SkillBundle, dir: &Path) {
        let metadata = fs::symlink_metadata(dir).unwrap();
        assert!(metadata.is_dir());
        assert!(!metadata.file_type().is_symlink());
        let hashes = managed_hashes(dir);
        for file in bundle.files {
            let rendered = file
                .contents
                .replace(CLI_PLACEHOLDER, &cli_command(Home::local(home)));
            assert_eq!(
                fs::read_to_string(dir.join(file.relative)).unwrap(),
                rendered,
                "{}",
                file.relative
            );
            assert_eq!(
                hashes.get(file.relative),
                Some(&sha256_hex(rendered.as_bytes())),
                "{}",
                file.relative
            );
        }
    }

    fn managed_hashes(dir: &Path) -> BTreeMap<String, String> {
        let value: Value =
            serde_json::from_str(&fs::read_to_string(managed_files_path(dir)).unwrap()).unwrap();
        value["files"]
            .as_object()
            .unwrap()
            .iter()
            .map(|(key, item)| (key.clone(), item.as_str().unwrap().to_string()))
            .collect()
    }
}
