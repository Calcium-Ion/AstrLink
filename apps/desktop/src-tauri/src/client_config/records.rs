//! `~/.astrlink/client-configs.json`: what AstrLink wrote in each place of a
//! home, and the values it replaced there.

use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
    time::{SystemTime, UNIX_EPOCH},
};

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use super::client::Place;
use crate::{
    control_session::astrlink_home,
    host_files::{self, WriteOptions},
};

/// Version 2 adds Gemini CLI, OpenCode, OpenClaw, write targets, and the
/// values AstrLink replaced. Version 1 files, from released builds, read as
/// keeping none.
const RECORDS_VERSION: u32 = 2;
const RECORDS_V1: u32 = 1;
/// The places version 1 keys: released builds read a file with these and
/// nothing else.
const V1_KEYS: [&str; 3] = ["claude", "codex", "pi"];

/// What AstrLink wrote in one place. Its own values are kept only as
/// SHA-256 fingerprints, so the file never holds the token.
#[derive(Clone, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ClientRecord {
    pub token_id: String,
    pub token_sha256: String,
    pub base_url: String,
    pub keys: BTreeMap<String, String>,
    pub created_file: bool,
    /// The client's other files AstrLink created, by their place in
    /// `ClientSpec::files`; each goes once a removal empties it. Version 1
    /// has no room for them, so a version 1 file keeps such files.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub created_companions: Vec<usize>,
    pub written_at_unix: u64,
    /// The values AstrLink replaced, by key, put back when its config is
    /// removed. They may be other providers' credentials, so the record file
    /// is as private as the configs.
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub backup: BTreeMap<String, String>,
    /// The file AstrLink wrote, for a client that reads one of several.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub file: Option<String>,
    /// For a client the user pastes the token into, since when its requests
    /// show the paste worked. A new token, or a new address for a client
    /// that keeps the token by address, starts it again.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub paste_since_unix: Option<u64>,
}

#[derive(Debug, Default, Deserialize, Serialize)]
pub(crate) struct Records {
    version: u32,
    /// Keyed `<client>` or `<client>:<target>`, so an unknown name fails to
    /// read.
    #[serde(flatten)]
    places: BTreeMap<Place, ClientRecord>,
    /// Clients 0.1 noted as moved to CC Switch. Read and dropped: a config
    /// CC Switch wrote is another tool's, like any other.
    #[serde(default, rename = "cc_switch", skip_serializing)]
    _cc_switch: Option<serde::de::IgnoredAny>,
}

impl Records {
    fn is_empty(&self) -> bool {
        self.places.is_empty()
    }

    fn needs_v2(&self) -> bool {
        self.places.iter().any(|(place, record)| {
            !V1_KEYS.contains(&place.key().as_str())
                || !record.backup.is_empty()
                || record.file.is_some()
        })
    }

    pub(crate) fn get(&self, place: impl Into<Place>) -> Option<&ClientRecord> {
        self.places.get(&place.into())
    }

    /// Replaces the place's record, returning the one it had.
    pub(crate) fn set(
        &mut self,
        place: impl Into<Place>,
        record: Option<ClientRecord>,
    ) -> Option<ClientRecord> {
        let place = place.into();
        match record {
            Some(record) => self.places.insert(place, record),
            None => self.places.remove(&place),
        }
    }

    /// The places with a record, in the dialog's order.
    pub(crate) fn places(&self) -> Vec<Place> {
        self.places.keys().copied().collect()
    }
}

pub(crate) fn records_path(home: &Path) -> PathBuf {
    astrlink_home(home).join("client-configs.json")
}

pub(crate) fn read_records(home: &Path) -> Result<Records, String> {
    let path = records_path(home);
    let Some(raw) = host_files::read_optional(&path)? else {
        return Ok(Records::default());
    };
    let records: Records = serde_json::from_str(&raw)
        .map_err(|_| format!("{} is not a valid record file", path.display()))?;
    if !matches!(records.version, RECORDS_V1 | RECORDS_VERSION) {
        return Err(format!("{} has an unknown version", path.display()));
    }
    Ok(records)
}

pub(crate) fn write_records(home: &Path, records: &mut Records) -> Result<(), String> {
    let path = records_path(home);
    if records.is_empty() {
        return host_files::remove_path(&path);
    }
    // Released builds share this file and read version 1 only, so it stays
    // there until it holds something version 1 cannot.
    records.version = if records.needs_v2() {
        RECORDS_VERSION
    } else {
        for record in records.places.values_mut() {
            record.created_companions.clear();
        }
        RECORDS_V1
    };
    let mut encoded = serde_json::to_string_pretty(records)
        .map_err(|error| format!("unable to encode client records: {error}"))?;
    encoded.push('\n');
    host_files::write_text(
        &path,
        &encoded,
        WriteOptions {
            secret: true,
            ..Default::default()
        },
    )
}

pub(crate) fn sha256_hex(value: &str) -> String {
    format!("{:x}", Sha256::digest(value.as_bytes()))
}

pub(crate) fn unix_now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs())
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn records_noting_cc_switch_load_and_drop_the_note() {
        let home = unique_home("cc-switch-note");
        let plan = plan_write(CODEX, &[None], &connection(&codex_models()), None).unwrap();
        fs::write(home.join(".codex/config.toml"), &plan.contents[0]).unwrap();
        fs::create_dir_all(astrlink_home(&home)).unwrap();
        // 0.1 noted clients moved to CC Switch; later builds ignore the note.
        for version in [1, 2] {
            fs::write(
                records_path(&home),
                serde_json::json!({
                    "version": version,
                    "codex": plan.record,
                    "cc_switch": ["claude", "codex"],
                })
                .to_string(),
            )
            .unwrap();
            let statuses = status(
                Home::local(&home),
                Some("http://127.0.0.1:18317/"),
                Some(&tokens()),
            )
            .unwrap();
            assert_eq!(statuses[0].state, ClientState::NotConfigured);
            assert_eq!(statuses[1].state, ClientState::Configured);
            assert_eq!(statuses[1].token_id.as_deref(), Some("token_01"));
        }
        write(
            Home::local(&home),
            CLAUDE,
            &connection(&[("model", "main-route".to_string())]),
            &tokens(),
            false,
        )
        .unwrap();
        let records = fs::read_to_string(records_path(&home)).unwrap();
        assert!(!records.contains("cc_switch"));
        assert!(records.contains("\"codex\""));
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn records_from_released_builds_still_load() {
        let home = unique_home("released-records");
        let plan = plan_write(CODEX, &[None], &connection(&codex_models()), None).unwrap();
        fs::write(home.join(".codex/config.toml"), &plan.contents[0]).unwrap();
        // Released builds wrote one object per client and nothing else.
        fs::create_dir_all(astrlink_home(&home)).unwrap();
        fs::write(
            records_path(&home),
            serde_json::json!({"version": 1, "codex": plan.record}).to_string(),
        )
        .unwrap();
        assert_eq!(
            status(
                Home::local(&home),
                Some("http://127.0.0.1:18317/"),
                Some(&tokens())
            )
            .unwrap()[1]
                .state,
            ClientState::Configured
        );
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn created_companions_need_version_2() {
        let home = unique_home("created-companions");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".pi/agent")).unwrap();
        fs::create_dir_all(home.join(".codebuddy")).unwrap();
        let read = |home: &Path| json(&fs::read_to_string(records_path(home)).unwrap());
        // Pi creates its settings file too, which version 1 cannot note.
        write(local, PI, &connection(&pi_models()), &tokens(), false).unwrap();
        assert_eq!(read(&home)["version"], 1);
        assert!(read(&home)["pi"].get("created_companions").is_none());
        let models = vec![("model", "cb-route".to_string())];
        write(local, CODEBUDDY, &connection(&models), &tokens(), false).unwrap();
        assert_eq!(read(&home)["version"], 2);
        assert_eq!(
            read(&home)["codebuddy"]["created_companions"],
            serde_json::json!([1])
        );
        remove(local, CODEBUDDY).unwrap();
        assert!(!home.join(".codebuddy/models.json").exists());
        assert!(!home.join(".codebuddy/settings.json").exists());
        assert_eq!(read(&home)["version"], 1);
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn version_1_records_read_as_keeping_no_replaced_values() {
        let home = unique_home("records-v1");
        let models = codex_models();
        let plan = plan_write(CODEX, &[None], &connection(&models), None).unwrap();
        let config = home.join(".codex/config.toml");
        fs::write(&config, &plan.contents[0]).unwrap();
        let mut record = serde_json::to_value(&plan.record).unwrap();
        record.as_object_mut().unwrap().remove("backup");
        fs::create_dir_all(records_path(&home).parent().unwrap()).unwrap();
        fs::write(
            records_path(&home),
            serde_json::json!({"version": 1, "codex": record}).to_string(),
        )
        .unwrap();
        let statuses = status(
            Home::local(&home),
            Some("http://127.0.0.1:18317/"),
            Some(&tokens()),
        )
        .unwrap();
        assert_eq!(statuses[1].state, ClientState::Configured);
        let version = || {
            let written: serde_json::Value =
                serde_json::from_str(&fs::read_to_string(records_path(&home)).unwrap()).unwrap();
            assert_eq!(written["codex"]["token_id"], "token_01");
            written["version"].clone()
        };
        // Released builds read version 1, so it stays while it can.
        let models = vec![("model", "main-route".to_string())];
        write(
            Home::local(&home),
            CLAUDE,
            &connection(&models),
            &tokens(),
            false,
        )
        .unwrap();
        assert_eq!(version(), 1);
        fs::create_dir_all(home.join(".gemini")).unwrap();
        write(
            Home::local(&home),
            GEMINI,
            &connection(&models),
            &tokens(),
            false,
        )
        .unwrap();
        assert_eq!(version(), 2);
        fs::write(records_path(&home), "{\"version\": 3}").unwrap();
        assert!(status(Home::local(&home), None, None).is_err());
        fs::remove_dir_all(home).unwrap();
    }
}
