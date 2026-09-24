use std::{
    collections::{BTreeMap, BTreeSet},
    fs, io,
    path::{Path, PathBuf},
    time::{SystemTime, UNIX_EPOCH},
};

use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};

use crate::control_session::astrlink_home;

pub const BUNDLE_NAME: &str = "astrlink-debug";
pub const BUNDLE_VERSION: &str = "0.1.3";
pub const MCP_SERVER_NAME: &str = "astrlink";
const RECEIPT_VERSION: u32 = 1;
const MANAGED_FILES_NAME: &str = ".astrlink-managed-files.json";

const SKILL_MD: &str = include_str!("../../../../agent-bundle/astrlink-debug/SKILL.md");
const TRAJECTORY_MD: &str =
    include_str!("../../../../agent-bundle/astrlink-debug/references/trajectory.md");
const MANIFEST_JSON: &str = include_str!("../../../../agent-bundle/astrlink-debug/manifest.json");

struct SkillFile {
    relative: &'static str,
    contents: &'static str,
}

const SKILL_FILES: &[SkillFile] = &[
    SkillFile {
        relative: "SKILL.md",
        contents: SKILL_MD,
    },
    SkillFile {
        relative: "references/trajectory.md",
        contents: TRAJECTORY_MD,
    },
    SkillFile {
        relative: "manifest.json",
        contents: MANIFEST_JSON,
    },
];

#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum AgentToolId {
    Cursor,
    Claude,
    Codex,
    Grok,
}

#[derive(Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct AgentToolStatus {
    pub id: AgentToolId,
    pub detected: bool,
    pub skill_installed: bool,
    pub mcp_installed: bool,
    pub preview_paths: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct AgentInstallStatus {
    pub canonical_skill: bool,
    pub mcp_binary: bool,
    pub mcp_command: Option<String>,
    pub tools: Vec<AgentToolStatus>,
    pub shared_paths: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct InstallReceipt {
    pub version: u32,
    pub bundle: String,
    pub bundle_version: String,
    pub installed_at_unix: u64,
    pub mcp_binary: String,
    pub files: Vec<String>,
}

pub struct InstallContext {
    pub home: PathBuf,
    pub mcp_source: PathBuf,
}

impl AgentToolId {
    fn all() -> [Self; 4] {
        [Self::Cursor, Self::Claude, Self::Codex, Self::Grok]
    }
}

pub fn status(context: &InstallContext) -> AgentInstallStatus {
    let canonical = canonical_skill_dir(&context.home);
    let mcp_dest = mcp_binary_dest(&context.home);
    let mcp_command = mcp_dest.to_str().map(str::to_string);
    let tools = AgentToolId::all()
        .into_iter()
        .map(|id| tool_status(&context.home, id, mcp_command.as_deref()))
        .collect::<Vec<_>>();
    AgentInstallStatus {
        shared_paths: vec![
            display_path(&mcp_dest).unwrap_or_default(),
            display_path(&receipt_path(&context.home)).unwrap_or_default(),
        ],
        canonical_skill: canonical.join("SKILL.md").is_file(),
        mcp_binary: mcp_dest.is_file(),
        mcp_command,
        tools,
    }
}

pub fn install(
    context: &InstallContext,
    tool_ids: &[AgentToolId],
) -> Result<InstallReceipt, String> {
    if tool_ids.is_empty() {
        return Err("select at least one agent tool to install".to_string());
    }
    for id in tool_ids {
        if !tool_detected(&context.home, *id) {
            return Err(format!("selected agent tool {id:?} is no longer detected"));
        }
    }
    if !context.mcp_source.is_file() {
        return Err(
            "unable to locate astrlink-mcp. Build desktop sidecars first (bun run sidecar:build)."
                .to_string(),
        );
    }
    let mut files = Vec::new();
    let mcp_dest = mcp_binary_dest(&context.home);
    copy_mcp_binary(&context.mcp_source, &mcp_dest)?;
    files.push(display_path(&mcp_dest)?);

    let mcp_command = display_path(&mcp_dest)?;
    for id in AgentToolId::all() {
        if !tool_ids.contains(&id) {
            continue;
        }
        files.extend(install_tool(&context.home, id, &mcp_command)?);
    }
    deduplicate_paths(&mut files);

    let receipt = InstallReceipt {
        version: RECEIPT_VERSION,
        bundle: BUNDLE_NAME.to_string(),
        bundle_version: BUNDLE_VERSION.to_string(),
        installed_at_unix: unix_now(),
        mcp_binary: mcp_command,
        files: files.clone(),
    };
    let receipt_path = receipt_path(&context.home);
    write_json_file(&receipt_path, &receipt)?;
    files.push(display_path(&receipt_path)?);
    let mut receipt = receipt;
    receipt.files = files;
    write_json_file(&receipt_path, &receipt)?;
    Ok(receipt)
}

pub fn uninstall(context: &InstallContext) -> Result<(), String> {
    for id in AgentToolId::all() {
        uninstall_tool(&context.home, id)?;
    }
    let canonical = canonical_skill_dir(&context.home);
    if is_ours_skill(&canonical, &canonical) {
        remove_path(&canonical)?;
    }
    let mcp_dest = mcp_binary_dest(&context.home);
    remove_path(&mcp_dest)?;
    remove_path(&receipt_path(&context.home))?;
    Ok(())
}

pub fn sync_installed_skills(home: &Path) -> Result<(), String> {
    if !receipt_path(home).is_file() {
        return Ok(());
    }
    migrate_legacy_codex_skill(home)?;
    let canonical = canonical_skill_dir(home);
    if is_ours_skill(&canonical, &canonical) {
        write_skill_tree(&canonical, &canonical)?;
    }
    for id in AgentToolId::all() {
        if id == AgentToolId::Codex {
            continue;
        }
        let dest = tool_skill_dir(home, id);
        if is_ours_skill(&dest, &canonical) {
            write_skill_tree(&dest, &canonical)?;
        }
    }
    Ok(())
}

pub fn sync_installed_mcp(context: &InstallContext) -> Result<(), String> {
    if !receipt_path(&context.home).is_file() {
        return Ok(());
    }
    if !context.mcp_source.is_file() {
        return Ok(());
    }
    copy_mcp_binary(&context.mcp_source, &mcp_binary_dest(&context.home))
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

fn canonical_skill_dir(home: &Path) -> PathBuf {
    home.join(".agents").join("skills").join(BUNDLE_NAME)
}

fn legacy_codex_skill_dir(home: &Path) -> PathBuf {
    home.join(".codex").join("skills").join(BUNDLE_NAME)
}

// Codex discovers the shared .agents directory itself. Older installers also
// wrote a .codex copy, causing both descriptions to enter the prompt. Keep the
// shared copy active and archive the owned duplicate outside skill search roots
// so local edits and extra files remain recoverable.
fn migrate_legacy_codex_skill(home: &Path) -> Result<(), String> {
    let legacy = legacy_codex_skill_dir(home);
    let canonical = canonical_skill_dir(home);
    if !is_ours_skill(&legacy, &canonical) {
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
    write_canonical_skill(home)?;
    if is_link {
        return remove_path(&legacy);
    }

    let backups = astrlink_home(home).join("agent-skill-backups");
    fs::create_dir_all(&backups)
        .map_err(|error| format!("unable to create {}: {error}", backups.display()))?;
    let mut index = 0_u64;
    loop {
        let backup = backups.join(format!("codex-{}-{index}", unix_now()));
        match fs::create_dir(&backup) {
            Ok(()) => {
                let dest = backup.join(BUNDLE_NAME);
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

fn mcp_binary_dest(home: &Path) -> PathBuf {
    let name = if cfg!(windows) {
        "astrlink-mcp.exe"
    } else {
        "astrlink-mcp"
    };
    astrlink_home(home).join("bin").join(name)
}

fn tool_detected(home: &Path, id: AgentToolId) -> bool {
    match id {
        AgentToolId::Cursor => home.join(".cursor").is_dir(),
        AgentToolId::Claude => home.join(".claude").is_dir() || home.join(".claude.json").is_file(),
        AgentToolId::Codex => home.join(".codex").is_dir(),
        AgentToolId::Grok => home.join(".grok").is_dir(),
    }
}

fn tool_skill_dir(home: &Path, id: AgentToolId) -> PathBuf {
    match id {
        AgentToolId::Cursor => home.join(".cursor").join("skills").join(BUNDLE_NAME),
        AgentToolId::Claude => home.join(".claude").join("skills").join(BUNDLE_NAME),
        AgentToolId::Codex => canonical_skill_dir(home),
        AgentToolId::Grok => home.join(".grok").join("skills").join(BUNDLE_NAME),
    }
}

fn tool_mcp_path(home: &Path, id: AgentToolId) -> PathBuf {
    match id {
        AgentToolId::Cursor => home.join(".cursor").join("mcp.json"),
        AgentToolId::Claude => home.join(".claude.json"),
        AgentToolId::Codex => home.join(".codex").join("config.toml"),
        AgentToolId::Grok => home.join(".grok").join("config.toml"),
    }
}

fn tool_status(home: &Path, id: AgentToolId, mcp_command: Option<&str>) -> AgentToolStatus {
    let detected = tool_detected(home, id);
    let skill = tool_skill_dir(home, id);
    AgentToolStatus {
        id,
        detected,
        skill_installed: skill_present(&skill, &canonical_skill_dir(home)),
        mcp_installed: mcp_command
            .map(|command| mcp_configured(&tool_mcp_path(home, id), id, command))
            .unwrap_or(false),
        preview_paths: vec![
            display_path(&skill).unwrap_or_default(),
            display_path(&tool_mcp_path(home, id)).unwrap_or_default(),
        ],
    }
}

fn skill_present(path: &Path, canonical: &Path) -> bool {
    if let Ok(target) = fs::read_link(path) {
        return target == canonical;
    }
    path.join("SKILL.md").is_file()
}

fn mcp_configured(path: &Path, id: AgentToolId, command: &str) -> bool {
    let Ok(raw) = fs::read_to_string(path) else {
        return false;
    };
    match id {
        AgentToolId::Codex | AgentToolId::Grok => toml_command(&raw).as_deref() == Some(command),
        AgentToolId::Cursor | AgentToolId::Claude => json_command(&raw).as_deref() == Some(command),
    }
}

fn json_command(raw: &str) -> Option<String> {
    let value: Value = serde_json::from_str(raw).ok()?;
    value
        .get("mcpServers")?
        .get(MCP_SERVER_NAME)?
        .get("command")?
        .as_str()
        .map(str::to_string)
}

fn toml_command(raw: &str) -> Option<String> {
    let document = raw.parse::<toml_edit::DocumentMut>().ok()?;
    document
        .get("mcp_servers")?
        .get(MCP_SERVER_NAME)?
        .get("command")?
        .as_str()
        .map(str::to_string)
}

fn deduplicate_paths(paths: &mut Vec<String>) {
    let mut seen = BTreeSet::new();
    paths.retain(|path| !path.is_empty() && seen.insert(path.clone()));
}

fn write_canonical_skill(home: &Path) -> Result<PathBuf, String> {
    let dest = canonical_skill_dir(home);
    write_skill_tree(&dest, &dest)?;
    Ok(dest)
}

fn install_tool(home: &Path, id: AgentToolId, mcp_command: &str) -> Result<Vec<String>, String> {
    let skill = tool_skill_dir(home, id);
    // The shared directory is discovered by Codex, so only write it when selected.
    if id == AgentToolId::Codex {
        migrate_legacy_codex_skill(home)?;
    }
    write_skill_tree(&skill, &canonical_skill_dir(home))?;
    let mcp_path = tool_mcp_path(home, id);
    merge_mcp_config(&mcp_path, id, mcp_command)?;
    Ok(vec![display_path(&skill)?, display_path(&mcp_path)?])
}

fn uninstall_tool(home: &Path, id: AgentToolId) -> Result<(), String> {
    // The shared skill is removed once, after all tool-specific installations.
    let skill = if id == AgentToolId::Codex {
        legacy_codex_skill_dir(home)
    } else {
        tool_skill_dir(home, id)
    };
    if is_ours_skill(&skill, &canonical_skill_dir(home)) {
        remove_path(&skill)?;
    }
    let mcp_path = tool_mcp_path(home, id);
    if !mcp_path.is_file() {
        return Ok(());
    }
    let raw = fs::read_to_string(&mcp_path)
        .map_err(|error| format!("unable to read {}: {error}", mcp_path.display()))?;
    let next = match id {
        AgentToolId::Codex => remove_codex_mcp(&raw)?,
        AgentToolId::Grok => remove_grok_mcp(&raw)?,
        AgentToolId::Claude => remove_json_mcp(&raw)?,
        AgentToolId::Cursor => remove_json_mcp(&raw)?,
    };
    fs::write(&mcp_path, next)
        .map_err(|error| format!("unable to update {}: {error}", mcp_path.display()))?;
    Ok(())
}

fn write_skill_tree(dest: &Path, canonical: &Path) -> Result<(), String> {
    match dest.symlink_metadata() {
        Err(error) if error.kind() == io::ErrorKind::NotFound => write_skill_files(dest, None),
        Err(error) => Err(format!("unable to inspect {}: {error}", dest.display())),
        Ok(metadata) if metadata.file_type().is_symlink() => {
            if !points_at_canonical(dest, canonical) {
                return refuse_overwrite(dest);
            }
            remove_path(dest)?;
            write_skill_files(dest, None)
        }
        Ok(_) => match read_managed_manifest(dest) {
            Some(managed) if managed.is_ours() => write_skill_files(dest, Some(&managed)),
            _ => refuse_overwrite(dest),
        },
    }
}

fn write_skill_files(dest: &Path, existing: Option<&ManagedManifest>) -> Result<(), String> {
    fs::create_dir_all(dest)
        .map_err(|error| format!("unable to create {}: {error}", dest.display()))?;
    let mut next_hashes = BTreeMap::new();
    for file in SKILL_FILES {
        let path = dest.join(file.relative);
        let desired_hash = sha256_hex(file.contents.as_bytes());
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
            fs::write(&path, file.contents)
                .map_err(|error| format!("unable to write {}: {error}", path.display()))?;
            next_hashes.insert(file.relative.to_string(), desired_hash);
        } else if let Some(recorded) =
            existing.and_then(|managed| managed.recorded_hash(file.relative))
        {
            next_hashes.insert(file.relative.to_string(), recorded.to_string());
        }
    }
    write_managed_manifest(dest, &next_hashes)
}

fn write_managed_manifest(dest: &Path, files: &BTreeMap<String, String>) -> Result<(), String> {
    write_json_file(
        &managed_files_path(dest),
        &json!({
            "manager": "astrlink",
            "bundle": BUNDLE_NAME,
            "version": BUNDLE_VERSION,
            "files": files,
        }),
    )
}

fn refuse_overwrite(dest: &Path) -> Result<(), String> {
    Err(format!(
        "refusing to overwrite existing {} skill at {}",
        BUNDLE_NAME,
        dest.display()
    ))
}

fn is_ours_skill(path: &Path, canonical: &Path) -> bool {
    if points_at_canonical(path, canonical) {
        return true;
    }
    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.is_dir() && !metadata.file_type().is_symlink() => {
            read_managed_manifest(path).is_some_and(|managed| managed.is_ours())
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
    fn is_ours(&self) -> bool {
        self.manager == "astrlink" && self.bundle == BUNDLE_NAME
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

fn copy_mcp_binary(source: &Path, dest: &Path) -> Result<(), String> {
    if let Some(parent) = dest.parent() {
        fs::create_dir_all(parent)
            .map_err(|error| format!("unable to create {}: {error}", parent.display()))?;
    }
    fs::copy(source, dest).map_err(|error| format!("unable to install astrlink-mcp: {error}"))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(dest, fs::Permissions::from_mode(0o755))
            .map_err(|error| format!("unable to mark astrlink-mcp executable: {error}"))?;
    }
    Ok(())
}

fn merge_mcp_config(path: &Path, id: AgentToolId, command: &str) -> Result<(), String> {
    let existing = match fs::read_to_string(path) {
        Ok(raw) => raw,
        Err(error) if error.kind() == io::ErrorKind::NotFound => String::new(),
        Err(error) => return Err(format!("unable to read {}: {error}", path.display())),
    };
    let next = match id {
        AgentToolId::Cursor => merge_cursor_mcp(&existing, command)?,
        AgentToolId::Claude => merge_claude_mcp(&existing, command)?,
        AgentToolId::Codex => merge_codex_mcp(&existing, command)?,
        AgentToolId::Grok => merge_grok_mcp(&existing, command)?,
    };
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)
            .map_err(|error| format!("unable to create {}: {error}", parent.display()))?;
    }
    fs::write(path, next).map_err(|error| format!("unable to write {}: {error}", path.display()))
}

pub fn merge_cursor_mcp(existing: &str, command: &str) -> Result<String, String> {
    merge_json_mcp(existing, command, true)
}

pub fn merge_claude_mcp(existing: &str, command: &str) -> Result<String, String> {
    merge_json_mcp(existing, command, true)
}

fn merge_json_mcp(existing: &str, command: &str, typed: bool) -> Result<String, String> {
    let mut value = if existing.trim().is_empty() {
        json!({})
    } else {
        serde_json::from_str(existing).map_err(|error| {
            format!("MCP JSON is invalid; AstrLink will not overwrite it: {error}")
        })?
    };
    let object = value
        .as_object_mut()
        .ok_or_else(|| "MCP JSON root must be an object".to_string())?;
    let servers = object.entry("mcpServers").or_insert_with(|| json!({}));
    let servers = servers
        .as_object_mut()
        .ok_or_else(|| "mcpServers must be an object".to_string())?;
    let mut server = serde_json::Map::new();
    if typed {
        server.insert("type".into(), json!("stdio"));
    }
    server.insert("command".into(), json!(command));
    server.insert("args".into(), json!([]));
    servers.insert(MCP_SERVER_NAME.into(), Value::Object(server));
    pretty_json(&value)
}

pub fn merge_codex_mcp(existing: &str, command: &str) -> Result<String, String> {
    merge_toml_mcp(existing, command, "Codex")
}

pub fn merge_grok_mcp(existing: &str, command: &str) -> Result<String, String> {
    merge_toml_mcp(existing, command, "Grok Build")
}

fn merge_toml_mcp(existing: &str, command: &str, tool: &str) -> Result<String, String> {
    let mut document = if existing.trim().is_empty() {
        toml_edit::DocumentMut::new()
    } else {
        existing
            .parse::<toml_edit::DocumentMut>()
            .map_err(|error| {
                format!("{tool} config.toml is invalid; AstrLink will not overwrite it: {error}")
            })?
    };
    let mut server = toml_edit::Table::new();
    server["command"] = toml_edit::value(command);
    let mut args = toml_edit::Array::new();
    args.set_trailing("");
    server["args"] = toml_edit::Item::Value(toml_edit::Value::Array(args));
    let servers = document["mcp_servers"].or_insert(toml_edit::table());
    if let Some(table) = servers.as_table_mut() {
        table[MCP_SERVER_NAME] = toml_edit::Item::Table(server);
    } else {
        return Err("mcp_servers must be a table".to_string());
    }
    Ok(document.to_string())
}

pub fn remove_json_mcp(existing: &str) -> Result<String, String> {
    if existing.trim().is_empty() {
        return Ok(existing.to_string());
    }
    let mut value: Value = serde_json::from_str(existing)
        .map_err(|error| format!("MCP JSON is invalid; AstrLink will not overwrite it: {error}"))?;
    if let Some(servers) = value.get_mut("mcpServers").and_then(Value::as_object_mut) {
        servers.remove(MCP_SERVER_NAME);
    }
    pretty_json(&value)
}

pub fn remove_codex_mcp(existing: &str) -> Result<String, String> {
    remove_toml_mcp(existing, "Codex")
}

pub fn remove_grok_mcp(existing: &str) -> Result<String, String> {
    remove_toml_mcp(existing, "Grok Build")
}

fn remove_toml_mcp(existing: &str, tool: &str) -> Result<String, String> {
    if existing.trim().is_empty() {
        return Ok(existing.to_string());
    }
    let mut document = existing
        .parse::<toml_edit::DocumentMut>()
        .map_err(|error| {
            format!("{tool} config.toml is invalid; AstrLink will not overwrite it: {error}")
        })?;
    if let Some(servers) = document
        .get_mut("mcp_servers")
        .and_then(|item| item.as_table_mut())
    {
        servers.remove(MCP_SERVER_NAME);
    }
    Ok(document.to_string())
}

fn pretty_json(value: &Value) -> Result<String, String> {
    let mut encoded = serde_json::to_string_pretty(value)
        .map_err(|error| format!("unable to encode MCP JSON: {error}"))?;
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

fn remove_path(path: &Path) -> Result<(), String> {
    match fs::symlink_metadata(path) {
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(format!("unable to inspect {}: {error}", path.display())),
        Ok(metadata) if metadata.file_type().is_dir() && !metadata.file_type().is_symlink() => {
            fs::remove_dir_all(path)
                .map_err(|error| format!("unable to remove {}: {error}", path.display()))
        }
        Ok(_) => fs::remove_file(path)
            .or_else(|_| fs::remove_dir_all(path))
            .map_err(|error| format!("unable to remove {}: {error}", path.display())),
    }
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

    #[test]
    fn merge_json_keeps_other_servers_and_omits_secrets() {
        let merged = merge_cursor_mcp(
            r#"{"mcpServers":{"other":{"command":"keep-me"}}}"#,
            "/tmp/astrlink-mcp",
        )
        .unwrap();
        assert!(merged.contains("keep-me"));
        assert!(merged.contains("astrlink"));
        assert!(merged.contains("/tmp/astrlink-mcp"));
        assert!(merged.contains("\"type\": \"stdio\""));
        assert!(!merged.contains("Bearer"));
        assert!(!merged.contains("control_token"));
        let removed = remove_json_mcp(&merged).unwrap();
        assert!(removed.contains("keep-me"));
        assert!(!removed.contains("astrlink-mcp"));
    }

    #[test]
    fn merge_json_rejects_invalid_documents() {
        let error = merge_cursor_mcp("{not json", "/bin/astrlink-mcp").unwrap_err();
        assert!(error.contains("will not overwrite"));
    }

    #[test]
    fn merge_toml_keeps_other_servers() {
        let merged = merge_codex_mcp(
            "[mcp_servers.other]\ncommand = \"keep-me\"\n",
            "/tmp/astrlink-mcp",
        )
        .unwrap();
        assert!(merged.contains("keep-me"));
        assert!(merged.contains("astrlink"));
        let removed = remove_codex_mcp(&merged).unwrap();
        assert!(removed.contains("keep-me"));
        assert!(!removed.contains("/tmp/astrlink-mcp"));
    }

    #[test]
    fn merge_grok_toml_keeps_models_and_other_servers() {
        let existing = concat!(
            "[models]\n",
            "default = \"glm-5.3-flash-exl3\"\n\n",
            "[mcp_servers.outline]\n",
            "url = \"https://docs.example.test/mcp\"\n",
            "enabled = true\n\n",
            "[mcp_servers.outline.headers]\n",
            "Authorization = \"Bearer keep-me\"\n\n",
            "[model.\"glm-5.3-flash-exl3\"]\n",
            "name = \"GLM 5.3 Flash\"\n\n",
            "[[model.\"glm-5.3-flash-exl3\".reasoning_efforts]]\n",
            "value = \"high\"\n",
        );
        let merged = merge_grok_mcp(existing, "/tmp/astrlink-mcp").unwrap();
        assert!(merged.contains("[mcp_servers.astrlink]"));
        assert!(merged.contains("/tmp/astrlink-mcp"));
        assert!(merged.contains("Bearer keep-me"));
        assert!(merged.contains("default = \"glm-5.3-flash-exl3\""));
        assert!(merged.contains("[[model.\"glm-5.3-flash-exl3\".reasoning_efforts]]"));
        let document = merged.parse::<toml_edit::DocumentMut>().unwrap();
        assert_eq!(
            document["mcp_servers"]["astrlink"]["command"].as_str(),
            Some("/tmp/astrlink-mcp")
        );
        assert_eq!(
            document["mcp_servers"]["outline"]["url"].as_str(),
            Some("https://docs.example.test/mcp")
        );
        assert_eq!(
            document["models"]["default"].as_str(),
            Some("glm-5.3-flash-exl3")
        );

        let removed = remove_grok_mcp(&merged).unwrap();
        assert!(!removed.contains("astrlink"));
        assert!(removed.contains("Bearer keep-me"));
        assert!(removed.contains("[[model.\"glm-5.3-flash-exl3\".reasoning_efforts]]"));

        let error = merge_grok_mcp("[models\ndefault = 1", "/tmp/astrlink-mcp").unwrap_err();
        assert!(error.contains("Grok Build config.toml is invalid"));
    }

    #[test]
    fn install_and_uninstall_detected_tools() {
        let home = unique_temp("agent-install");
        fs::create_dir_all(home.join(".cursor")).unwrap();
        fs::create_dir_all(home.join(".claude")).unwrap();
        fs::create_dir_all(home.join(".codex")).unwrap();
        fs::create_dir_all(home.join(".grok")).unwrap();
        fs::write(
            home.join(".cursor").join("mcp.json"),
            r#"{"mcpServers":{"keep":{"command":"x"}}}"#,
        )
        .unwrap();
        fs::write(
            home.join(".grok").join("config.toml"),
            "[models]\ndefault = \"keep-model\"\n\n[mcp_servers.keep]\ncommand = \"x\"\n",
        )
        .unwrap();
        let mcp_source = home.join("src-astrlink-mcp");
        fs::write(&mcp_source, b"mcp-binary").unwrap();

        let context = InstallContext {
            home: home.clone(),
            mcp_source,
        };
        let before = status(&context);
        assert!(before
            .tools
            .iter()
            .all(|tool| tool.detected && !tool.mcp_installed));

        let receipt = install(&context, &AgentToolId::all()).unwrap();
        assert!(receipt.mcp_binary.contains("astrlink-mcp"));
        assert!(mcp_binary_dest(&home).is_file());
        assert_real_skill_copy(&canonical_skill_dir(&home));
        assert_real_skill_copy(&tool_skill_dir(&home, AgentToolId::Cursor));
        assert_real_skill_copy(&tool_skill_dir(&home, AgentToolId::Claude));
        assert_real_skill_copy(&tool_skill_dir(&home, AgentToolId::Codex));
        assert_real_skill_copy(&tool_skill_dir(&home, AgentToolId::Grok));
        assert!(!legacy_codex_skill_dir(&home).exists());
        let shared_path = display_path(&canonical_skill_dir(&home)).unwrap();
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
            .contains(&display_path(&legacy_codex_skill_dir(&home)).unwrap()));

        let after = status(&context);
        assert_eq!(
            after
                .tools
                .iter()
                .flat_map(|tool| &tool.preview_paths)
                .filter(|path| **path == shared_path)
                .count(),
            1
        );
        assert!(!after
            .tools
            .iter()
            .flat_map(|tool| &tool.preview_paths)
            .any(|path| *path == display_path(&legacy_codex_skill_dir(&home)).unwrap()));
        assert!(after.canonical_skill);
        assert!(after.mcp_binary);
        for tool in &after.tools {
            assert!(tool.detected);
            assert!(tool.skill_installed);
            assert!(tool.mcp_installed);
        }
        let cursor_mcp = fs::read_to_string(home.join(".cursor").join("mcp.json")).unwrap();
        assert!(cursor_mcp.contains("keep"));
        assert!(cursor_mcp.contains("\"type\": \"stdio\""));
        assert!(!cursor_mcp.contains("control_token"));
        let grok_mcp = fs::read_to_string(home.join(".grok").join("config.toml")).unwrap();
        assert!(grok_mcp.contains("keep-model"));
        assert!(grok_mcp.contains("[mcp_servers.keep]"));
        assert!(grok_mcp.contains("[mcp_servers.astrlink]"));
        assert!(!grok_mcp.contains("control_token"));

        uninstall(&context).unwrap();
        let gone = status(&context);
        assert!(!gone.canonical_skill);
        assert!(!gone.mcp_binary);
        for tool in &gone.tools {
            assert!(!tool.skill_installed);
            assert!(!tool.mcp_installed);
        }
        assert!(!canonical_skill_dir(&home).exists());
        assert!(!tool_skill_dir(&home, AgentToolId::Cursor).exists());
        assert!(!tool_skill_dir(&home, AgentToolId::Claude).exists());
        assert!(!tool_skill_dir(&home, AgentToolId::Codex).exists());
        assert!(!tool_skill_dir(&home, AgentToolId::Grok).exists());
        let cursor_mcp = fs::read_to_string(home.join(".cursor").join("mcp.json")).unwrap();
        assert!(cursor_mcp.contains("keep"));
        let grok_mcp = fs::read_to_string(home.join(".grok").join("config.toml")).unwrap();
        assert!(grok_mcp.contains("keep-model"));
        assert!(grok_mcp.contains("[mcp_servers.keep]"));
        assert!(!grok_mcp.contains("astrlink"));
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn install_and_startup_archive_legacy_codex_copy_without_losing_edits() {
        for reinstall in [false, true] {
            let context = installed_codex_context("codex-migrate");
            let home = &context.home;
            let canonical = canonical_skill_dir(home);
            let legacy = legacy_codex_skill_dir(home);
            write_skill_tree(&legacy, &canonical).unwrap();
            fs::write(canonical.join("SKILL.md"), "shared user edit").unwrap();
            fs::write(legacy.join("SKILL.md"), "legacy user edit").unwrap();
            fs::write(legacy.join("notes.txt"), "keep this extra file").unwrap();
            let legacy_manifest = fs::read(managed_files_path(&legacy)).unwrap();

            if reinstall {
                install(&context, &[AgentToolId::Codex]).unwrap();
            } else {
                sync_installed_skills(home).unwrap();
            }
            assert!(!legacy.exists());
            assert_eq!(
                fs::read_to_string(canonical.join("SKILL.md")).unwrap(),
                "shared user edit"
            );
            let backups = codex_backups(home);
            assert_eq!(backups.len(), 1);
            let archived = backups[0].join(BUNDLE_NAME);
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
                    .skill_installed
            );

            // Startup and a later reinstall must not recreate the duplicate.
            sync_installed_skills(home).unwrap();
            install(&context, &[AgentToolId::Codex]).unwrap();
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
        let canonical = write_canonical_skill(&home).unwrap();
        let legacy = legacy_codex_skill_dir(&home);
        write_skill_tree(&legacy, &canonical).unwrap();
        sync_installed_skills(&home).unwrap();
        assert_real_skill_copy(&legacy);
        assert!(codex_backups(&home).is_empty());
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn startup_moves_lone_legacy_codex_copy_to_shared_directory() {
        let context = installed_codex_context("codex-legacy-only");
        let home = &context.home;
        let canonical = canonical_skill_dir(home);
        let legacy = legacy_codex_skill_dir(home);
        fs::create_dir_all(legacy.parent().unwrap()).unwrap();
        fs::rename(&canonical, &legacy).unwrap();
        fs::write(legacy.join("SKILL.md"), "keep legacy customization").unwrap();
        sync_installed_skills(home).unwrap();
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
        let legacy = legacy_codex_skill_dir(home);
        fs::create_dir_all(&legacy).unwrap();
        fs::write(legacy.join("SKILL.md"), "not ours").unwrap();
        sync_installed_skills(home).unwrap();
        install(&context, &[AgentToolId::Codex]).unwrap();
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
        let canonical = canonical_skill_dir(home);
        let legacy = legacy_codex_skill_dir(home);
        write_skill_tree(&legacy, &canonical).unwrap();
        fs::remove_file(managed_files_path(&canonical)).unwrap();
        fs::write(canonical.join("SKILL.md"), "foreign shared skill").unwrap();
        assert!(sync_installed_skills(home)
            .unwrap_err()
            .contains("refusing to overwrite"));
        assert_real_skill_copy(&legacy);
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
        let canonical = canonical_skill_dir(home);
        let legacy = legacy_codex_skill_dir(home);
        write_skill_tree(&legacy, &canonical).unwrap();
        uninstall(&context).unwrap();
        assert!(!canonical.exists());
        assert!(!legacy.exists());
        assert!(
            !status(&context)
                .tools
                .iter()
                .find(|tool| tool.id == AgentToolId::Codex)
                .unwrap()
                .mcp_installed
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
                let canonical = canonical_skill_dir(home);
                let legacy = legacy_codex_skill_dir(home);
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
                sync_installed_skills(home).unwrap();
                assert!(legacy.symlink_metadata().is_err());
                assert_real_skill_copy(&canonical);
                assert!(codex_backups(home).is_empty());
                let _ = fs::remove_dir_all(home);
            }
        }
    }

    fn installed_codex_context(name: &str) -> InstallContext {
        let home = unique_temp(name);
        fs::create_dir_all(home.join(".codex")).unwrap();
        let mcp_source = home.join("src-astrlink-mcp");
        fs::write(&mcp_source, b"mcp").unwrap();
        let context = InstallContext { home, mcp_source };
        install(&context, &[AgentToolId::Codex]).unwrap();
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
        let dest = canonical_skill_dir(&home);
        write_skill_tree(&dest, &dest).unwrap();

        let skill = dest.join("SKILL.md");
        fs::write(&skill, "stale-managed").unwrap();
        let mut hashes = managed_hashes(&dest);
        hashes.insert("SKILL.md".into(), sha256_hex(b"stale-managed"));
        write_managed_manifest(&dest, &hashes).unwrap();
        write_skill_tree(&dest, &dest).unwrap();
        assert_eq!(fs::read_to_string(&skill).unwrap(), SKILL_MD);

        fs::write(&skill, "user-edit").unwrap();
        write_skill_tree(&dest, &dest).unwrap();
        assert_eq!(fs::read_to_string(&skill).unwrap(), "user-edit");
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn old_array_manifest_upgrades_to_hash_map() {
        let home = unique_temp("agent-old-manifest");
        let dest = canonical_skill_dir(&home);
        fs::create_dir_all(dest.join("references")).unwrap();
        fs::write(dest.join("SKILL.md"), "legacy").unwrap();
        write_json_file(
            &managed_files_path(&dest),
            &json!({
                "manager": "astrlink",
                "bundle": BUNDLE_NAME,
                "version": "0.0.1",
                "files": ["SKILL.md", "references/trajectory.md", "manifest.json"],
            }),
        )
        .unwrap();
        write_skill_tree(&dest, &dest).unwrap();
        assert_real_skill_copy(&dest);
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn refuses_foreign_skill_directory() {
        let home = unique_temp("agent-foreign");
        fs::create_dir_all(home.join(".cursor")).unwrap();
        let dest = tool_skill_dir(&home, AgentToolId::Cursor);
        fs::create_dir_all(&dest).unwrap();
        fs::write(dest.join("SKILL.md"), "not yours").unwrap();
        let mcp_source = home.join("src-astrlink-mcp");
        fs::write(&mcp_source, b"mcp").unwrap();
        let error = install(
            &InstallContext {
                home: home.clone(),
                mcp_source,
            },
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
        let canonical = write_canonical_skill(&home).unwrap();
        let dest = tool_skill_dir(&home, AgentToolId::Cursor);
        fs::create_dir_all(dest.parent().unwrap()).unwrap();
        std::os::unix::fs::symlink(&canonical, &dest).unwrap();
        assert!(fs::symlink_metadata(&dest)
            .unwrap()
            .file_type()
            .is_symlink());

        let mcp_source = home.join("src-astrlink-mcp");
        fs::write(&mcp_source, b"mcp").unwrap();
        install(
            &InstallContext {
                home: home.clone(),
                mcp_source,
            },
            &[AgentToolId::Cursor],
        )
        .unwrap();
        assert_real_skill_copy(&dest);
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn sync_requires_receipt_and_skips_unknown_tools() {
        let home = unique_temp("agent-sync");
        let canonical = canonical_skill_dir(&home);
        write_skill_tree(&canonical, &canonical).unwrap();
        let skill = canonical.join("SKILL.md");
        fs::write(&skill, "stale-managed").unwrap();
        let mut hashes = managed_hashes(&canonical);
        hashes.insert("SKILL.md".into(), sha256_hex(b"stale-managed"));
        write_managed_manifest(&canonical, &hashes).unwrap();

        sync_installed_skills(&home).unwrap();
        assert_eq!(fs::read_to_string(&skill).unwrap(), "stale-managed");
        assert!(!tool_skill_dir(&home, AgentToolId::Cursor).exists());

        write_json_file(
            &receipt_path(&home),
            &InstallReceipt {
                version: RECEIPT_VERSION,
                bundle: BUNDLE_NAME.to_string(),
                bundle_version: BUNDLE_VERSION.to_string(),
                installed_at_unix: 1,
                mcp_binary: "astrlink-mcp".into(),
                files: vec![],
            },
        )
        .unwrap();
        sync_installed_skills(&home).unwrap();
        assert_eq!(fs::read_to_string(&skill).unwrap(), SKILL_MD);
        assert!(!tool_skill_dir(&home, AgentToolId::Cursor).exists());
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn sync_mcp_binary_requires_receipt() {
        let home = unique_temp("agent-sync-mcp");
        let dest = mcp_binary_dest(&home);
        let stale = home.join("stale-astrlink-mcp");
        let next = home.join("next-astrlink-mcp");
        fs::write(&stale, b"stale").unwrap();
        fs::write(&next, b"next").unwrap();

        sync_installed_mcp(&InstallContext {
            home: home.clone(),
            mcp_source: next.clone(),
        })
        .unwrap();
        assert!(!dest.exists());

        write_json_file(
            &receipt_path(&home),
            &InstallReceipt {
                version: RECEIPT_VERSION,
                bundle: BUNDLE_NAME.to_string(),
                bundle_version: BUNDLE_VERSION.to_string(),
                installed_at_unix: 1,
                mcp_binary: "astrlink-mcp".into(),
                files: vec![],
            },
        )
        .unwrap();
        copy_mcp_binary(&stale, &dest).unwrap();
        sync_installed_mcp(&InstallContext {
            home: home.clone(),
            mcp_source: next,
        })
        .unwrap();
        assert_eq!(fs::read(&dest).unwrap(), b"next");
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn rejects_empty_or_undetected_selection_before_writing() {
        let home = unique_temp("agent-skip");
        let mcp_source = home.join("src-astrlink-mcp");
        fs::write(&mcp_source, b"mcp").unwrap();
        let context = InstallContext {
            home: home.clone(),
            mcp_source,
        };
        fs::create_dir_all(home.join(".grok")).unwrap();
        assert!(install(&context, &[])
            .unwrap_err()
            .contains("select at least one"));
        assert!(install(&context, &[AgentToolId::Grok, AgentToolId::Cursor])
            .unwrap_err()
            .contains("no longer detected"));
        assert!(!home.join(".cursor").exists());
        assert!(!canonical_skill_dir(&home).exists());
        assert!(!mcp_binary_dest(&home).exists());
        assert!(!receipt_path(&home).exists());
        assert!(!tool_skill_dir(&home, AgentToolId::Grok).exists());
        let _ = fs::remove_dir_all(&home);
    }

    #[test]
    fn installs_only_selected_tools_and_startup_preserves_scope() {
        for selected in [
            vec![AgentToolId::Grok],
            vec![AgentToolId::Cursor, AgentToolId::Grok],
            vec![AgentToolId::Codex],
            vec![AgentToolId::Claude, AgentToolId::Grok, AgentToolId::Grok],
        ] {
            let home = unique_temp("agent-selected");
            for dir in [".cursor", ".claude", ".codex", ".grok"] {
                fs::create_dir_all(home.join(dir)).unwrap();
            }
            let mcp_source = home.join("src-astrlink-mcp");
            fs::write(&mcp_source, b"mcp").unwrap();
            let context = InstallContext { home, mcp_source };
            let before = status(&context);
            let mut expected_paths = before.shared_paths;
            for tool in before
                .tools
                .iter()
                .filter(|tool| selected.contains(&tool.id))
            {
                expected_paths.extend(tool.preview_paths.clone());
            }
            let receipt = install(&context, &selected).unwrap();
            assert_eq!(
                receipt.files.into_iter().collect::<BTreeSet<_>>(),
                expected_paths.into_iter().collect::<BTreeSet<_>>()
            );
            sync_installed_skills(&context.home).unwrap();
            sync_installed_mcp(&context).unwrap();
            let after = status(&context);
            assert_eq!(
                after.canonical_skill,
                selected.contains(&AgentToolId::Codex)
            );
            for tool in after.tools {
                let installed = selected.contains(&tool.id);
                assert_eq!(tool.skill_installed, installed, "{:?}", tool.id);
                assert_eq!(tool.mcp_installed, installed, "{:?}", tool.id);
                assert_eq!(tool_mcp_path(&context.home, tool.id).exists(), installed);
            }
            uninstall(&context).unwrap();
            assert!(!mcp_binary_dest(&context.home).exists());
            assert!(status(&context)
                .tools
                .iter()
                .all(|tool| !tool.skill_installed && !tool.mcp_installed));
            let _ = fs::remove_dir_all(&context.home);
        }
    }

    #[test]
    fn selecting_grok_preserves_existing_unselected_installations() {
        let context = installed_codex_context("agent-unselected");
        let home = &context.home;
        fs::create_dir_all(home.join(".grok")).unwrap();
        fs::create_dir_all(home.join(".cursor")).unwrap();
        let cursor_config = tool_mcp_path(home, AgentToolId::Cursor);
        fs::write(&cursor_config, "invalid JSON must remain untouched").unwrap();
        let canonical = canonical_skill_dir(home);
        let legacy = legacy_codex_skill_dir(home);
        write_skill_tree(&legacy, &canonical).unwrap();
        let tracked = [
            canonical.join("SKILL.md"),
            managed_files_path(&canonical),
            legacy.join("SKILL.md"),
            tool_mcp_path(home, AgentToolId::Codex),
            cursor_config,
        ];
        let before = tracked
            .iter()
            .map(|path| fs::read(path).unwrap())
            .collect::<Vec<_>>();
        install(&context, &[AgentToolId::Grok]).unwrap();
        for (path, bytes) in tracked.iter().zip(before) {
            assert_eq!(fs::read(path).unwrap(), bytes);
        }
        assert!(status(&context)
            .tools
            .iter()
            .filter(|tool| [AgentToolId::Codex, AgentToolId::Grok].contains(&tool.id))
            .all(|tool| tool.skill_installed && tool.mcp_installed));
        assert!(codex_backups(home).is_empty());
        let _ = fs::remove_dir_all(home);
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

    fn assert_real_skill_copy(dir: &Path) {
        let metadata = fs::symlink_metadata(dir).unwrap();
        assert!(metadata.is_dir());
        assert!(!metadata.file_type().is_symlink());
        assert_eq!(fs::read_to_string(dir.join("SKILL.md")).unwrap(), SKILL_MD);
        let hashes = managed_hashes(dir);
        assert_eq!(
            hashes.get("SKILL.md"),
            Some(&sha256_hex(SKILL_MD.as_bytes()))
        );
        assert_eq!(
            hashes.get("references/trajectory.md"),
            Some(&sha256_hex(TRAJECTORY_MD.as_bytes()))
        );
        assert_eq!(
            hashes.get("manifest.json"),
            Some(&sha256_hex(MANIFEST_JSON.as_bytes()))
        );
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
