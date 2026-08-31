use std::{
    collections::BTreeMap,
    fs,
    io::{self, ErrorKind},
    path::{Path, PathBuf},
};

use serde::Deserialize;

pub const MANIFEST_NAME: &str = "astrlink-classifier-model.json";
pub const TAXONOMY_ID: &str = "astrlink-text-v1";
pub const TAXONOMY_SHA256: &str =
    "83990d9d763df0feac9b06aef36ef14417a662494f53004e68c3f18248fd5a2f";
pub const TEXT_PREPROCESSING: &str = "current-user-text-v3";
pub const TOKEN_PREPROCESSING: &str = "tokenize_head_tail-v1";
pub const LABEL_COUNT: usize = 4;
pub const MAX_SEQUENCE_TOKENS: usize = 512;
pub const CONTENT_BUDGET: usize = 510;
pub const HEAD_TOKENS: usize = 255;
pub const TAIL_TOKENS: usize = 255;
pub const PAD_TOKEN_ID: i64 = 0;
pub const PAD_MULTIPLE: usize = 8;
const MAX_MANIFEST_BYTES: usize = 256 * 1024;
const MAX_FILES: usize = 128;
const SYNTHETIC_REPO_ID: &str = "astrlink/synthetic-classifier-worker-fixture";
const SYNTHETIC_VARIANT_ID: &str = "synthetic_micro";
const SYNTHETIC_DIRECTORY_PREFIX: &str = "astrlink-classifier-worker-synthetic-fixture-";

#[derive(Clone, Debug, Deserialize)]
pub struct ModelManifest {
    pub version: u32,
    pub installation_id: String,
    pub identity: String,
    pub taxonomy_id: String,
    pub taxonomy_sha256: String,
    pub preprocessing: Preprocessing,
    pub artifact_tier: String,
    pub model_path: String,
    pub tokenizer_path: String,
    pub config_path: String,
    pub max_sequence_tokens: usize,
    pub content_budget: usize,
    pub head_tokens: usize,
    pub tail_tokens: usize,
    pub pad_token_id: i64,
    pub pad_multiple: usize,
    pub add_special_tokens: bool,
    pub input_names: InputNames,
    pub output_name: String,
    pub id2label: BTreeMap<String, String>,
    pub files: Vec<ManifestFile>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Preprocessing {
    pub text: String,
    pub tokens: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct InputNames {
    pub input_ids: String,
    pub attention_mask: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct ManifestFile {
    pub path: String,
    pub size: u64,
    pub sha256: String,
}

impl ModelManifest {
    pub fn load(directory: &Path) -> io::Result<Self> {
        let path = directory.join(MANIFEST_NAME);
        let bytes = fs::read(&path)?;
        if bytes.len() > MAX_MANIFEST_BYTES {
            return Err(io::Error::other("invalid_model_manifest"));
        }
        let manifest: Self = serde_json::from_slice(&bytes)
            .map_err(|_| io::Error::new(ErrorKind::InvalidData, "invalid_model_manifest"))?;
        manifest.validate()?;
        Ok(manifest)
    }

    pub fn validate(&self) -> io::Result<()> {
        if self.version != 1
            || self.installation_id.is_empty()
            || self.identity.is_empty()
            || self.taxonomy_id != TAXONOMY_ID
            || self.taxonomy_sha256 != TAXONOMY_SHA256
            || self.preprocessing.text != TEXT_PREPROCESSING
            || self.preprocessing.tokens != TOKEN_PREPROCESSING
            || self.max_sequence_tokens != MAX_SEQUENCE_TOKENS
            || self.content_budget != CONTENT_BUDGET
            || self.head_tokens != HEAD_TOKENS
            || self.tail_tokens != TAIL_TOKENS
            || self.pad_token_id != PAD_TOKEN_ID
            || self.pad_multiple != PAD_MULTIPLE
            || self.add_special_tokens
            || self.output_name != "logits"
            || self.input_names.input_ids.is_empty()
            || self.input_names.attention_mask.is_empty()
            || self.files.is_empty()
            || self.files.len() > MAX_FILES
        {
            return Err(io::Error::other("invalid_model_manifest"));
        }
        if !matches!(self.artifact_tier.as_str(), "experimental" | "released") {
            return Err(io::Error::other("invalid_model_manifest"));
        }
        validate_id2label(&self.id2label)?;
        for file in &self.files {
            if !safe_asset_path(&file.path) || file.size == 0 || file.sha256.len() != 64 {
                return Err(io::Error::other("invalid_model_manifest"));
            }
        }
        Ok(())
    }

    pub fn resolve(&self, directory: &Path, relative: &str) -> PathBuf {
        directory.join(relative)
    }

    pub fn validate_files(&self, directory: &Path) -> io::Result<()> {
        for file in &self.files {
            let path = self.resolve(directory, &file.path);
            let metadata = fs::symlink_metadata(&path)?;
            if metadata.file_type().is_symlink() || !metadata.is_file() {
                return Err(io::Error::other("invalid_model_file"));
            }
            if metadata.len() != file.size {
                return Err(io::Error::other("invalid_model_file"));
            }
        }
        Ok(())
    }

    pub fn validate_synthetic_fixture(&self, directory: &Path) -> io::Result<()> {
        let in_testdata = directory.components().any(|component| {
            component.as_os_str() == "testdata"
                || directory.file_name().is_some_and(|name| {
                    name.to_string_lossy()
                        .starts_with(SYNTHETIC_DIRECTORY_PREFIX)
                })
        });
        if !in_testdata {
            return Err(io::Error::other("non_synthetic_model_in_ci"));
        }
        if !self.identity.contains(SYNTHETIC_REPO_ID)
            || !self.identity.contains(SYNTHETIC_VARIANT_ID)
        {
            return Err(io::Error::other("non_synthetic_model_in_ci"));
        }
        let model = self
            .files
            .iter()
            .find(|file| file.path.ends_with(".onnx"))
            .ok_or_else(|| io::Error::other("non_synthetic_model_in_ci"))?;
        if model.size > 1024 * 1024 {
            return Err(io::Error::other("non_synthetic_model_in_ci"));
        }
        Ok(())
    }

    pub fn labels(&self) -> io::Result<[String; LABEL_COUNT]> {
        let mut labels = [(); LABEL_COUNT].map(|_| String::new());
        for (index, expected) in expected_labels().into_iter().enumerate() {
            let actual = self
                .id2label
                .get(&index.to_string())
                .ok_or_else(|| io::Error::other("invalid_model_manifest"))?;
            if actual != expected {
                return Err(io::Error::other("invalid_model_manifest"));
            }
            labels[index] = expected.to_owned();
        }
        Ok(labels)
    }
}

pub fn expected_labels() -> [&'static str; LABEL_COUNT] {
    ["general", "research", "coding", "architect"]
}

fn validate_id2label(id2label: &BTreeMap<String, String>) -> io::Result<()> {
    if id2label.len() != LABEL_COUNT {
        return Err(io::Error::other("invalid_model_manifest"));
    }
    for (index, expected) in expected_labels().into_iter().enumerate() {
        match id2label.get(&index.to_string()) {
            Some(actual) if actual == expected => {}
            _ => return Err(io::Error::other("invalid_model_manifest")),
        }
    }
    Ok(())
}

fn safe_asset_path(path: &str) -> bool {
    !path.is_empty()
        && !path.starts_with('/')
        && !path.contains('\\')
        && !path.split('/').any(|segment| {
            segment.is_empty() || segment == "." || segment == ".." || segment.contains('\0')
        })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_unknown_preprocessing_and_wrong_labels() {
        let mut manifest = synthetic_manifest();
        manifest.preprocessing.tokens = "tokenize_head_tail-v0".into();
        assert!(manifest.validate().is_err());

        let mut manifest = synthetic_manifest();
        manifest.id2label.insert("2".into(), "code".into());
        assert!(manifest.validate().is_err());
    }

    #[test]
    fn synthetic_fixture_guard_rejects_catalog_identity() {
        let manifest = synthetic_manifest();
        assert!(
            manifest
                .validate_synthetic_fixture(Path::new(
                    "/tmp/astrlink-classifier-worker-synthetic-fixture-test"
                ))
                .is_ok()
        );
        let mut production = synthetic_manifest();
        production.identity = "jhu-clsp/mmbert-small@deadbeef#p1-onnx-int8-dynamic-v1".into();
        assert_eq!(
            production
                .validate_synthetic_fixture(Path::new(
                    "/tmp/astrlink-classifier-worker-synthetic-fixture-test"
                ))
                .unwrap_err()
                .to_string(),
            "non_synthetic_model_in_ci"
        );
    }

    fn synthetic_manifest() -> ModelManifest {
        ModelManifest {
            version: 1,
            installation_id: "model_00000000000000000000000000000000".into(),
            identity: format!(
                "{SYNTHETIC_REPO_ID}@0000000000000000000000000000000000000000#{SYNTHETIC_VARIANT_ID}"
            ),
            taxonomy_id: TAXONOMY_ID.into(),
            taxonomy_sha256: TAXONOMY_SHA256.into(),
            preprocessing: Preprocessing {
                text: TEXT_PREPROCESSING.into(),
                tokens: TOKEN_PREPROCESSING.into(),
            },
            artifact_tier: "experimental".into(),
            model_path: "model.onnx".into(),
            tokenizer_path: "tokenizer.json".into(),
            config_path: "config.json".into(),
            max_sequence_tokens: MAX_SEQUENCE_TOKENS,
            content_budget: CONTENT_BUDGET,
            head_tokens: HEAD_TOKENS,
            tail_tokens: TAIL_TOKENS,
            pad_token_id: PAD_TOKEN_ID,
            pad_multiple: PAD_MULTIPLE,
            add_special_tokens: false,
            input_names: InputNames {
                input_ids: "input_ids".into(),
                attention_mask: "attention_mask".into(),
            },
            output_name: "logits".into(),
            id2label: BTreeMap::from([
                ("0".into(), "general".into()),
                ("1".into(), "research".into()),
                ("2".into(), "coding".into()),
                ("3".into(), "architect".into()),
            ]),
            files: vec![ManifestFile {
                path: "model.onnx".into(),
                size: 128,
                sha256: "0".repeat(64),
            }],
        }
    }
}
