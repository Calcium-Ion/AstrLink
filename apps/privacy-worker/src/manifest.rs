use std::{
    collections::{BTreeMap, BTreeSet},
    fs, io,
    path::{Path, PathBuf},
};

use serde::Deserialize;

pub const MANIFEST_NAME: &str = "astrlink-model.json";
pub const MAX_REQUEST_TOKENS: usize = 128 * 1024;
// Token ids index the embedding table; no tokenizer comes close to this.
const MAX_PAD_TOKEN_ID: u32 = (1 << 24) - 1;
const MAX_MANIFEST_BYTES: u64 = 256 * 1024;
const MAX_SYNTHETIC_FIXTURE_BYTES: u64 = 1024 * 1024;
const SYNTHETIC_INSTALLATION_ID: &str = "model_00000000000000000000000000000000";
const SYNTHETIC_REPO_ID: &str = "astrlink/synthetic-privacy-worker-fixture";
const SYNTHETIC_REVISION: &str = "0000000000000000000000000000000000000000";
const SYNTHETIC_VARIANT_ID: &str = "synthetic_micro";
const OPENAI_ENTITY_LABELS: [&str; 8] = [
    "account_number",
    "private_address",
    "private_date",
    "private_email",
    "private_person",
    "private_phone",
    "private_url",
    "secret",
];

pub const PPLX_ENTITY_LABELS: [&str; 9] = [
    "private_person",
    "private_email",
    "private_phone",
    "private_address",
    "private_url",
    "private_date",
    "account_number",
    "secret",
    "other_pii",
];

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum Adapter {
    OpenaiBioesViterbi,
    HfTokenClassification,
    PplxBioesViterbi,
    AstrlinkSensitiveGuard,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq)]
#[serde(rename_all = "lowercase")]
pub enum TagScheme {
    Bio,
    Bioes,
}

/// Where the ONNX session runs. Absent for the CPU path, which keeps every
/// existing manifest valid unchanged.
#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum ExecutionProvider {
    Cpu,
    Coreml,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum CoreMlModelFormat {
    Mlprogram,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum CoreMlComputeUnits {
    CpuAndGpu,
    CpuAndNeuralEngine,
    All,
}

/// Runtime facts for a model exported with a static `[1, length]` input.
/// Every window is padded to `static_sequence_length` with `pad_token_id`,
/// attention mask 0 and token type 0, and the padded positions are dropped
/// from the logits.
#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Execution {
    pub provider: ExecutionProvider,
    pub model_format: Option<CoreMlModelFormat>,
    pub compute_units: Option<CoreMlComputeUnits>,
    pub static_sequence_length: usize,
    pub pad_token_id: u32,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModelManifest {
    pub version: u8,
    pub installation_id: String,
    pub identity: String,
    pub repo_id: String,
    pub revision: String,
    pub variant_id: String,
    pub adapter: Adapter,
    pub model_path: String,
    pub external_data_paths: Vec<String>,
    pub tokenizer_path: String,
    pub config_path: String,
    pub calibration_path: Option<String>,
    pub secret_rules_path: Option<String>,
    pub secret_calibration_path: Option<String>,
    pub tag_scheme: TagScheme,
    pub window: usize,
    pub stride: usize,
    pub max_request_tokens: usize,
    pub input_names: InputNames,
    pub output_name: String,
    pub label_mapping: BTreeMap<String, Option<String>>,
    pub files: Vec<ModelFile>,
    #[serde(default)]
    pub execution: Option<Execution>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct InputNames {
    pub input_ids: String,
    pub attention_mask: String,
    pub token_type_ids: Option<String>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModelFile {
    pub path: String,
    pub size: u64,
    pub sha256: String,
}

impl ModelManifest {
    pub fn load(directory: &Path) -> io::Result<Self> {
        let directory_metadata = fs::symlink_metadata(directory)
            .map_err(|_| io::Error::new(io::ErrorKind::NotFound, "model_not_installed"))?;
        if directory_metadata.file_type().is_symlink() || !directory_metadata.is_dir() {
            return Err(io::Error::other("invalid_model_installation"));
        }
        let manifest_path = directory.join(MANIFEST_NAME);
        let manifest_metadata = fs::symlink_metadata(&manifest_path)
            .map_err(|_| io::Error::new(io::ErrorKind::NotFound, "model_not_installed"))?;
        if manifest_metadata.file_type().is_symlink()
            || !manifest_metadata.is_file()
            || manifest_metadata.len() == 0
            || manifest_metadata.len() > MAX_MANIFEST_BYTES
        {
            return Err(io::Error::other("invalid_model_installation"));
        }
        let bytes = fs::read(manifest_path)
            .map_err(|_| io::Error::new(io::ErrorKind::NotFound, "model_not_installed"))?;
        if bytes.len() as u64 != manifest_metadata.len() || !manifest_fields_present(&bytes) {
            return Err(io::Error::other("invalid_model_manifest"));
        }
        let manifest: Self = serde_json::from_slice(&bytes)
            .map_err(|_| io::Error::other("invalid_model_manifest"))?;
        manifest.validate()?;
        Ok(manifest)
    }

    pub fn resolve(&self, directory: &Path, relative: &str) -> PathBuf {
        directory.join(relative)
    }

    fn validate(&self) -> io::Result<()> {
        if self.version != 1
            || !valid_installation_id(&self.installation_id)
            || !valid_repo_id(&self.repo_id)
            || !valid_lower_hex(&self.revision, 40)
            || !valid_variant_id(&self.variant_id)
            || self.identity != format!("{}@{}#{}", self.repo_id, self.revision, self.variant_id)
        {
            return Err(io::Error::other("invalid_model_manifest"));
        }
        if self.window == 0
            || self.stride >= self.window
            || self.window > self.max_request_tokens
            || self.max_request_tokens == 0
            || self.max_request_tokens > MAX_REQUEST_TOKENS
        {
            return Err(io::Error::other("invalid_model_manifest"));
        }
        if !valid_tensor_name(&self.input_names.input_ids)
            || !valid_tensor_name(&self.input_names.attention_mask)
            || self.input_names.input_ids == self.input_names.attention_mask
            || !valid_tensor_name(&self.output_name)
        {
            return Err(io::Error::other("invalid_model_manifest"));
        }
        if let Some(token_type_ids) = &self.input_names.token_type_ids
            && (!valid_tensor_name(token_type_ids)
                || token_type_ids == &self.input_names.input_ids
                || token_type_ids == &self.input_names.attention_mask)
        {
            return Err(io::Error::other("invalid_model_manifest"));
        }

        match self.adapter {
            Adapter::PplxBioesViterbi => {
                if self.tag_scheme != TagScheme::Bioes
                    || self.window > 4096
                    || self.input_names.token_type_ids.is_some()
                    || self.calibration_path.is_some()
                    || self.secret_rules_path.is_some()
                    || self.secret_calibration_path.is_some()
                    || self.label_mapping.len() != PPLX_ENTITY_LABELS.len()
                    || PPLX_ENTITY_LABELS
                        .iter()
                        .any(|label| !self.label_mapping.contains_key(*label))
                {
                    return Err(io::Error::other("invalid_model_manifest"));
                }
            }
            Adapter::OpenaiBioesViterbi => {
                if self.tag_scheme != TagScheme::Bioes
                    || self.calibration_path.is_none()
                    || self.secret_rules_path.is_some()
                    || self.secret_calibration_path.is_some()
                {
                    return Err(io::Error::other("invalid_model_manifest"));
                }
            }
            Adapter::HfTokenClassification => {
                if self.calibration_path.is_some()
                    || self.secret_rules_path.is_some()
                    || self.secret_calibration_path.is_some()
                {
                    return Err(io::Error::other("invalid_model_manifest"));
                }
            }
            Adapter::AstrlinkSensitiveGuard => {
                if self.tag_scheme != TagScheme::Bioes
                    || self.calibration_path.is_none()
                    || self.secret_rules_path.is_none()
                    || self.secret_calibration_path.is_none()
                {
                    return Err(io::Error::other("invalid_model_manifest"));
                }
            }
        }

        if let Some(execution) = &self.execution
            && !valid_execution(execution, self.adapter, self.window)
        {
            return Err(io::Error::other("invalid_model_manifest"));
        }

        if self.files.is_empty() || self.files.len() > 128 || self.label_mapping.len() > 256 {
            return Err(io::Error::other("invalid_model_manifest"));
        }
        let mut declared = BTreeSet::new();
        for file in &self.files {
            if !valid_relative_path(&file.path)
                || file.size == 0
                || !valid_sha256(&file.sha256)
                || !declared.insert(file.path.as_str())
            {
                return Err(io::Error::other("invalid_model_manifest"));
            }
            let _ = file.size;
        }
        let required = [
            Some(self.model_path.as_str()),
            Some(self.tokenizer_path.as_str()),
            Some(self.config_path.as_str()),
            self.calibration_path.as_deref(),
            self.secret_rules_path.as_deref(),
            self.secret_calibration_path.as_deref(),
        ]
        .into_iter()
        .flatten()
        .chain(self.external_data_paths.iter().map(String::as_str));
        for path in required {
            if !valid_relative_path(path) || !declared.contains(path) {
                return Err(io::Error::other("invalid_model_manifest"));
            }
        }
        for kind in self.label_mapping.values().flatten() {
            if !canonical_kind(kind) {
                return Err(io::Error::other("invalid_model_manifest"));
            }
        }
        match self.adapter {
            Adapter::OpenaiBioesViterbi | Adapter::AstrlinkSensitiveGuard
                if self.label_mapping.len() != OPENAI_ENTITY_LABELS.len()
                    || OPENAI_ENTITY_LABELS
                        .iter()
                        .any(|label| !self.label_mapping.contains_key(*label)) =>
            {
                return Err(io::Error::other("invalid_model_manifest"));
            }
            Adapter::HfTokenClassification if self.label_mapping.is_empty() => {
                return Err(io::Error::other("invalid_model_manifest"));
            }
            _ => {}
        }
        Ok(())
    }

    pub fn validate_files(&self, directory: &Path) -> io::Result<()> {
        for file in &self.files {
            let metadata = fs::symlink_metadata(self.resolve(directory, &file.path))
                .map_err(|_| io::Error::new(io::ErrorKind::NotFound, "model_not_installed"))?;
            if metadata.file_type().is_symlink()
                || !metadata.is_file()
                || metadata.len() != file.size
            {
                return Err(io::Error::other("invalid_model_installation"));
            }
        }
        Ok(())
    }

    pub fn validate_synthetic_fixture(&self, directory: &Path) -> io::Result<()> {
        let expected_identity =
            format!("{SYNTHETIC_REPO_ID}@{SYNTHETIC_REVISION}#{SYNTHETIC_VARIANT_ID}");
        let path_is_fixture = directory
            .components()
            .any(|component| component.as_os_str() == "testdata")
            || directory.file_name().is_some_and(|name| {
                name.to_string_lossy()
                    .starts_with("astrlink-privacy-worker-synthetic-fixture-")
            });
        let total_size = self.files.iter().try_fold(0_u64, |total, file| {
            if file.size > MAX_SYNTHETIC_FIXTURE_BYTES {
                return None;
            }
            total.checked_add(file.size)
        });
        if !path_is_fixture
            || self.installation_id != SYNTHETIC_INSTALLATION_ID
            || self.repo_id != SYNTHETIC_REPO_ID
            || self.revision != SYNTHETIC_REVISION
            || self.variant_id != SYNTHETIC_VARIANT_ID
            || self.identity != expected_identity
            || total_size.is_none_or(|size| size > MAX_SYNTHETIC_FIXTURE_BYTES)
        {
            return Err(io::Error::other("non_synthetic_model_in_ci"));
        }
        Ok(())
    }
}

pub fn canonical_kind(value: &str) -> bool {
    matches!(
        value,
        "email"
            | "phone"
            | "account"
            | "payment_card"
            | "ip_address"
            | "url"
            | "common_secret"
            | "private_address"
            | "private_date"
            | "private_person"
    )
}

fn manifest_fields_present(bytes: &[u8]) -> bool {
    let Ok(value) = serde_json::from_slice::<serde_json::Value>(bytes) else {
        return false;
    };
    let Some(fields) = value.as_object() else {
        return false;
    };
    for field in [
        "version",
        "installation_id",
        "identity",
        "repo_id",
        "revision",
        "variant_id",
        "adapter",
        "model_path",
        "external_data_paths",
        "tokenizer_path",
        "config_path",
        "calibration_path",
        "tag_scheme",
        "window",
        "stride",
        "max_request_tokens",
        "input_names",
        "output_name",
        "label_mapping",
        "files",
    ] {
        if !fields.contains_key(field) {
            return false;
        }
    }
    let Some(input_names) = fields
        .get("input_names")
        .and_then(|value| value.as_object())
    else {
        return false;
    };
    if ["input_ids", "attention_mask", "token_type_ids"]
        .iter()
        .any(|field| !input_names.contains_key(*field))
    {
        return false;
    }
    let Some(files) = fields.get("files").and_then(|value| value.as_array()) else {
        return false;
    };
    files.iter().all(|file| {
        file.as_object().is_some_and(|fields| {
            ["path", "size", "sha256"]
                .iter()
                .all(|field| fields.contains_key(*field))
        })
    })
}

fn valid_execution(execution: &Execution, adapter: Adapter, window: usize) -> bool {
    let provider_options = match execution.provider {
        ExecutionProvider::Cpu => {
            execution.model_format.is_none() && execution.compute_units.is_none()
        }
        ExecutionProvider::Coreml => {
            execution.model_format == Some(CoreMlModelFormat::Mlprogram)
                && execution.compute_units.is_some()
        }
    };
    provider_options
        && adapter == Adapter::HfTokenClassification
        && execution.static_sequence_length > 0
        && execution.static_sequence_length == window
        && execution.pad_token_id <= MAX_PAD_TOKEN_ID
}

fn valid_installation_id(value: &str) -> bool {
    value
        .strip_prefix("model_")
        .is_some_and(|suffix| valid_lower_hex(suffix, 32))
}

fn valid_lower_hex(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn valid_repo_id(value: &str) -> bool {
    if value.contains("..") {
        return false;
    }
    let Some((owner, repository)) = value.split_once('/') else {
        return false;
    };
    !repository.contains('/') && valid_repo_component(owner) && valid_repo_component(repository)
}

fn valid_repo_component(value: &str) -> bool {
    (1..=96).contains(&value.len())
        && value
            .bytes()
            .next()
            .is_some_and(|byte| byte.is_ascii_alphanumeric())
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
}

fn valid_variant_id(value: &str) -> bool {
    (2..=64).contains(&value.len())
        && value
            .bytes()
            .next()
            .is_some_and(|byte| byte.is_ascii_lowercase())
        && value
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'_')
}

fn valid_tensor_name(value: &str) -> bool {
    (1..=128).contains(&value.len())
        && value
            .bytes()
            .next()
            .is_some_and(|byte| byte.is_ascii_alphabetic())
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'.' | b'-'))
}

fn valid_relative_path(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 512
        && !value.starts_with('/')
        && !value.ends_with('/')
        && !value.contains('\\')
        && !value.contains(':')
        && value
            .split('/')
            .all(|component| !component.is_empty() && component != "." && component != "..")
}

fn valid_sha256(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_parent_paths_and_noncanonical_kinds() {
        assert!(!valid_relative_path("../model.onnx"));
        assert!(!valid_relative_path("onnx//model.onnx"));
        assert!(!valid_relative_path("C:model.onnx"));
        assert!(!canonical_kind("private_email"));
        assert!(canonical_kind("email"));
    }

    #[test]
    fn synthetic_fixture_guard_rejects_catalog_identity_and_large_assets() {
        let mut manifest = synthetic_manifest();
        assert!(
            manifest
                .validate_synthetic_fixture(Path::new(
                    "/tmp/astrlink-privacy-worker-synthetic-fixture-test"
                ))
                .is_ok()
        );

        manifest.repo_id = "openai/privacy-filter".into();
        manifest.revision = "7ffa9a043d54d1be65afb281eddf0ffbe629385b".into();
        manifest.variant_id = "q4".into();
        manifest.identity =
            "openai/privacy-filter@7ffa9a043d54d1be65afb281eddf0ffbe629385b#q4".into();
        assert_eq!(
            manifest
                .validate_synthetic_fixture(Path::new(
                    "/tmp/astrlink-privacy-worker-synthetic-fixture-test"
                ))
                .expect_err("catalog model must be rejected")
                .to_string(),
            "non_synthetic_model_in_ci"
        );

        let mut manifest = synthetic_manifest();
        manifest.files[0].size = MAX_SYNTHETIC_FIXTURE_BYTES + 1;
        assert_eq!(
            manifest
                .validate_synthetic_fixture(Path::new(
                    "/tmp/astrlink-privacy-worker-synthetic-fixture-test"
                ))
                .expect_err("large fixture must be rejected")
                .to_string(),
            "non_synthetic_model_in_ci"
        );
    }

    #[test]
    fn execution_is_optional_and_strictly_validated() {
        let base = synthetic_manifest_json();
        assert!(parse(&base).is_ok_and(|manifest| manifest.execution.is_none()));

        let coreml = serde_json::json!({
            "provider": "coreml",
            "model_format": "mlprogram",
            "compute_units": "cpu_and_gpu",
            "static_sequence_length": 512,
            "pad_token_id": 1,
        });
        for units in ["cpu_and_gpu", "cpu_and_neural_engine", "all"] {
            let mut execution = coreml.clone();
            execution["compute_units"] = units.into();
            let manifest = parse(&with_execution(&base, execution)).expect("valid Core ML");
            let execution = manifest.execution.expect("execution");
            assert_eq!(execution.provider, ExecutionProvider::Coreml);
            assert_eq!(execution.model_format, Some(CoreMlModelFormat::Mlprogram));
            assert_eq!(execution.static_sequence_length, 512);
            assert_eq!(execution.pad_token_id, 1);
        }
        let cpu = serde_json::json!({
            "provider": "cpu",
            "model_format": null,
            "compute_units": null,
            "static_sequence_length": 512,
            "pad_token_id": 1,
        });
        assert!(parse(&with_execution(&base, cpu.clone())).is_ok());

        let mut invalid = Vec::new();
        for (field, value) in [
            ("compute_units", serde_json::Value::Null),
            ("model_format", serde_json::Value::Null),
            ("static_sequence_length", 256.into()),
            ("static_sequence_length", 0.into()),
            ("pad_token_id", (MAX_PAD_TOKEN_ID + 1).into()),
            ("compute_units", "cpu_only".into()),
            ("model_format", "neural_network".into()),
            ("provider", "cuda".into()),
            ("pad_token_id", (-1).into()),
        ] {
            let mut execution = coreml.clone();
            execution[field] = value;
            invalid.push(execution);
        }
        let mut cpu_with_units = cpu;
        cpu_with_units["compute_units"] = "all".into();
        invalid.push(cpu_with_units);
        let mut unknown_field = coreml.clone();
        unknown_field["cache_dir"] = "/tmp".into();
        invalid.push(unknown_field);
        for execution in invalid {
            assert!(
                parse(&with_execution(&base, execution.clone())).is_err(),
                "accepted {execution}"
            );
        }

        let mut openai = with_execution(&base, coreml);
        openai["adapter"] = "openai_bioes_viterbi".into();
        openai["tag_scheme"] = "bioes".into();
        assert!(parse(&openai).is_err());
    }

    fn parse(value: &serde_json::Value) -> Result<ModelManifest, String> {
        let manifest: ModelManifest =
            serde_json::from_value(value.clone()).map_err(|error| error.to_string())?;
        manifest.validate().map_err(|error| error.to_string())?;
        Ok(manifest)
    }

    fn with_execution(base: &serde_json::Value, execution: serde_json::Value) -> serde_json::Value {
        let mut manifest = base.clone();
        manifest["execution"] = execution;
        manifest
    }

    fn synthetic_manifest_json() -> serde_json::Value {
        serde_json::json!({
            "version": 1,
            "installation_id": SYNTHETIC_INSTALLATION_ID,
            "identity": format!("{SYNTHETIC_REPO_ID}@{SYNTHETIC_REVISION}#{SYNTHETIC_VARIANT_ID}"),
            "repo_id": SYNTHETIC_REPO_ID,
            "revision": SYNTHETIC_REVISION,
            "variant_id": SYNTHETIC_VARIANT_ID,
            "adapter": "hf_token_classification",
            "model_path": "model.onnx",
            "external_data_paths": [],
            "tokenizer_path": "tokenizer.json",
            "config_path": "config.json",
            "calibration_path": null,
            "secret_rules_path": null,
            "secret_calibration_path": null,
            "tag_scheme": "bio",
            "window": 512,
            "stride": 128,
            "max_request_tokens": MAX_REQUEST_TOKENS,
            "input_names": {
                "input_ids": "input_ids",
                "attention_mask": "attention_mask",
                "token_type_ids": "token_type_ids",
            },
            "output_name": "logits",
            "label_mapping": {"EMAIL": "email"},
            "files": [
                {"path": "model.onnx", "size": 128, "sha256": "0".repeat(64)},
                {"path": "tokenizer.json", "size": 64, "sha256": "1".repeat(64)},
                {"path": "config.json", "size": 32, "sha256": "2".repeat(64)},
            ],
        })
    }

    fn synthetic_manifest() -> ModelManifest {
        ModelManifest {
            version: 1,
            installation_id: SYNTHETIC_INSTALLATION_ID.into(),
            identity: format!("{SYNTHETIC_REPO_ID}@{SYNTHETIC_REVISION}#{SYNTHETIC_VARIANT_ID}"),
            repo_id: SYNTHETIC_REPO_ID.into(),
            revision: SYNTHETIC_REVISION.into(),
            variant_id: SYNTHETIC_VARIANT_ID.into(),
            adapter: Adapter::HfTokenClassification,
            model_path: "model.onnx".into(),
            external_data_paths: Vec::new(),
            tokenizer_path: "tokenizer.json".into(),
            config_path: "config.json".into(),
            calibration_path: None,
            secret_rules_path: None,
            secret_calibration_path: None,
            tag_scheme: TagScheme::Bio,
            window: 512,
            stride: 128,
            max_request_tokens: MAX_REQUEST_TOKENS,
            input_names: InputNames {
                input_ids: "input_ids".into(),
                attention_mask: "attention_mask".into(),
                token_type_ids: None,
            },
            output_name: "logits".into(),
            label_mapping: BTreeMap::from([("EMAIL".into(), Some("email".into()))]),
            files: vec![ModelFile {
                path: "model.onnx".into(),
                size: 128,
                sha256: "0".repeat(64),
            }],
            execution: None,
        }
    }
}
