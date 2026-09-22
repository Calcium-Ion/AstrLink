use std::{env, ffi::OsStr, io, path::Path};
// PathBuf only backs the Linux/macOS runtime discovery and the tests; an
// unconditional import is flagged as unused on Windows.
#[cfg(any(target_os = "linux", target_os = "macos", test))]
use std::path::PathBuf;

use ort::{
    logging::LogLevel,
    session::{Session, SessionInputValue, builder::GraphOptimizationLevel},
    value::Tensor,
};
use tokenizers::Tokenizer;

use crate::{
    manifest::{
        self, CONTENT_BUDGET, HEAD_TOKENS, LABEL_COUNT, ModelManifest, PAD_MULTIPLE, PAD_TOKEN_ID,
        TAIL_TOKENS,
    },
    normalize::{NormalizeError, normalize_current_user_text},
};

const INTRA_OP_THREADS: usize = 2;
const INTER_OP_THREADS: usize = 1;
const CI_SYNTHETIC_MODELS_ONLY_ENV: &str = "ASTRLINK_CI_SYNTHETIC_MODELS_ONLY";
#[cfg(target_os = "linux")]
const LINUX_ONNX_RUNTIME_PATH_ENV: &str = "ASTRLINK_ONNX_RUNTIME_PATH";

pub struct Classification {
    pub category: String,
    pub logits: Vec<f32>,
}

#[derive(Debug)]
pub struct ClassifyError {
    code: &'static str,
    detail: Option<String>,
}

impl ClassifyError {
    fn with_code(code: &'static str) -> Self {
        Self { code, detail: None }
    }

    pub fn code(&self) -> &'static str {
        self.code
    }
}

impl std::fmt::Display for ClassifyError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match &self.detail {
            Some(detail) => write!(formatter, "{}: {detail}", self.code),
            None => write!(formatter, "{}", self.code),
        }
    }
}

impl From<NormalizeError> for ClassifyError {
    fn from(error: NormalizeError) -> Self {
        Self::with_code(error.code())
    }
}

impl From<io::Error> for ClassifyError {
    fn from(error: io::Error) -> Self {
        Self {
            code: "inference_failed",
            detail: Some(error.to_string()),
        }
    }
}

impl From<Box<dyn std::error::Error + Send + Sync>> for ClassifyError {
    fn from(error: Box<dyn std::error::Error + Send + Sync>) -> Self {
        Self {
            code: "inference_failed",
            detail: Some(error.to_string()),
        }
    }
}

impl From<ort::Error> for ClassifyError {
    fn from(error: ort::Error) -> Self {
        Self {
            code: "inference_failed",
            detail: Some(error.to_string()),
        }
    }
}

pub struct ClassifierEngine {
    tokenizer: Tokenizer,
    session: Session,
    input_ids_name: String,
    attention_mask_name: String,
    output_name: String,
    labels: [String; LABEL_COUNT],
}

impl ClassifierEngine {
    pub fn load(model_directory: &Path) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        let manifest = ModelManifest::load(model_directory)?;
        if synthetic_models_only()? {
            manifest.validate_synthetic_fixture(model_directory)?;
        }
        manifest.validate_files(model_directory)?;
        let tokenizer =
            load_tokenizer(&manifest.resolve(model_directory, &manifest.tokenizer_path))?;
        initialize_onnx_runtime()?;
        let session = Session::builder()?
            .with_log_level(LogLevel::Error)?
            .with_intra_threads(INTRA_OP_THREADS)?
            .with_inter_threads(INTER_OP_THREADS)?
            .with_parallel_execution(false)?
            .with_optimization_level(GraphOptimizationLevel::Level3)?
            .commit_from_file(manifest.resolve(model_directory, &manifest.model_path))?;
        validate_session(&session, &manifest)?;
        let labels = manifest.labels()?;
        Ok(Self {
            tokenizer,
            session,
            input_ids_name: manifest.input_names.input_ids,
            attention_mask_name: manifest.input_names.attention_mask,
            output_name: manifest.output_name,
            labels,
        })
    }

    /// Load tokenizer.json + model.onnx from a training bundle without an
    /// install manifest. Used only by the numeric alignment gate.
    pub fn load_alignment_bundle(
        bundle: &Path,
    ) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        if synthetic_models_only()? {
            return Err(io::Error::other("non_synthetic_model_in_ci").into());
        }
        let tokenizer = load_tokenizer(&bundle.join("tokenizer.json"))?;
        initialize_onnx_runtime()?;
        let session = Session::builder()?
            .with_log_level(LogLevel::Error)?
            .with_intra_threads(INTRA_OP_THREADS)?
            .with_inter_threads(INTER_OP_THREADS)?
            .with_parallel_execution(false)?
            .with_optimization_level(GraphOptimizationLevel::Level3)?
            .commit_from_file(bundle.join("model.onnx"))?;
        Ok(Self {
            tokenizer,
            session,
            input_ids_name: "input_ids".into(),
            attention_mask_name: "attention_mask".into(),
            output_name: "logits".into(),
            labels: manifest::expected_labels().map(str::to_owned),
        })
    }

    pub fn tokenizer(&self) -> &Tokenizer {
        &self.tokenizer
    }

    pub fn classify(&mut self, text: &str) -> Result<Classification, ClassifyError> {
        let normalized = normalize_current_user_text(text)?;
        let ids = encode_head_tail(&self.tokenizer, &normalized)?;
        let (ids, mask) = pad_to_multiple(&ids, PAD_TOKEN_ID, PAD_MULTIPLE);
        infer_logits(self, &ids, &mask)
    }

    pub fn classify_ids(
        &mut self,
        input_ids: &[i64],
        attention_mask: &[i64],
    ) -> Result<Classification, ClassifyError> {
        if input_ids.len() != attention_mask.len() || input_ids.is_empty() {
            return Err(ClassifyError::with_code("invalid_text"));
        }
        infer_logits(self, input_ids, attention_mask)
    }
}

fn infer_logits(
    engine: &mut ClassifierEngine,
    input_ids: &[i64],
    attention_mask: &[i64],
) -> Result<Classification, ClassifyError> {
    let sequence_length = input_ids.len();
    if sequence_length > manifest::MAX_SEQUENCE_TOKENS {
        return Err(ClassifyError::with_code("invalid_text"));
    }
    let ids = Tensor::from_array(([1, sequence_length], input_ids.to_vec()))
        .map_err(|error| io::Error::other(error.to_string()))?;
    let mask = Tensor::from_array(([1, sequence_length], attention_mask.to_vec()))
        .map_err(|error| io::Error::other(error.to_string()))?;
    let outputs = engine
        .session
        .run(vec![
            (engine.input_ids_name.clone(), SessionInputValue::from(ids)),
            (
                engine.attention_mask_name.clone(),
                SessionInputValue::from(mask),
            ),
        ])
        .map_err(|error| io::Error::other(error.to_string()))?;
    let output = outputs
        .get(&engine.output_name)
        .ok_or_else(|| io::Error::other("missing_logits"))?;
    let (shape, logits) = output
        .try_extract_tensor::<f32>()
        .map_err(|error| io::Error::other(error.to_string()))?;
    if shape.as_ref() != [1, LABEL_COUNT as i64] {
        return Err(io::Error::other(format!("invalid_logits_shape:{shape:?}")).into());
    }
    if logits.len() != LABEL_COUNT || logits.iter().any(|value| !value.is_finite()) {
        return Err(io::Error::other("invalid_logits").into());
    }
    let category_index = logits
        .iter()
        .enumerate()
        .max_by(|left, right| left.1.total_cmp(right.1))
        .map(|(index, _)| index)
        .ok_or_else(|| io::Error::other("invalid_logits"))?;
    Ok(Classification {
        category: engine.labels[category_index].clone(),
        logits: logits.to_vec(),
    })
}

pub fn encode_head_tail(tokenizer: &Tokenizer, text: &str) -> Result<Vec<i64>, ClassifyError> {
    let encoding = tokenizer
        .encode(text, false)
        .map_err(|_| io::Error::other("invalid_tokenizer_output"))?;
    if encoding
        .get_special_tokens_mask()
        .iter()
        .any(|flag| *flag != 0)
    {
        return Err(ClassifyError::with_code("special_tokens_appended"));
    }
    let mut ids = encoding
        .get_ids()
        .iter()
        .map(|id| i64::from(*id))
        .collect::<Vec<_>>();
    if ids.len() > CONTENT_BUDGET {
        ids = ids[..HEAD_TOKENS]
            .iter()
            .copied()
            .chain(ids[ids.len() - TAIL_TOKENS..].iter().copied())
            .collect();
    }
    if ids.is_empty() {
        return Err(ClassifyError::with_code("empty_text"));
    }
    Ok(ids)
}

pub fn pad_to_multiple(ids: &[i64], pad_id: i64, multiple: usize) -> (Vec<i64>, Vec<i64>) {
    let padded_len = if ids.len().is_multiple_of(multiple) {
        ids.len()
    } else {
        ids.len() + (multiple - ids.len() % multiple)
    };
    let mut padded = Vec::with_capacity(padded_len);
    let mut mask = Vec::with_capacity(padded_len);
    padded.extend_from_slice(ids);
    mask.extend(std::iter::repeat_n(1, ids.len()));
    padded.resize(padded_len, pad_id);
    mask.resize(padded_len, 0);
    (padded, mask)
}

fn load_tokenizer(path: &Path) -> Result<Tokenizer, Box<dyn std::error::Error + Send + Sync>> {
    let mut tokenizer = Tokenizer::from_file(path)?;
    tokenizer.with_truncation(None)?;
    tokenizer.with_padding(None);
    Ok(tokenizer)
}

fn validate_session(session: &Session, manifest: &ModelManifest) -> io::Result<()> {
    let inputs = session.inputs();
    let outputs = session.outputs();
    if !inputs
        .iter()
        .any(|input| input.name() == manifest.input_names.input_ids)
        || !inputs
            .iter()
            .any(|input| input.name() == manifest.input_names.attention_mask)
        || !outputs
            .iter()
            .any(|output| output.name() == manifest.output_name)
    {
        return Err(io::Error::other("invalid_model_io"));
    }
    Ok(())
}

fn initialize_onnx_runtime() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    #[cfg(target_os = "macos")]
    {
        const RUNTIME_NAME: &str = "libonnxruntime.1.23.2.dylib";
        let executable = env::current_exe()?;
        let runtime = runtime_library_candidates(&executable, RUNTIME_NAME)
            .into_iter()
            .find(|candidate| candidate.is_file())
            .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "onnx_runtime_not_installed"))?;
        let _ = ort::init_from(runtime)?
            .with_name("astrlink-classifier-worker")
            .commit();
    }
    #[cfg(target_os = "linux")]
    {
        const RUNTIME_NAME: &str = "libonnxruntime.so.1.23.2";
        let executable = env::current_exe()?;
        let configured = env::var_os(LINUX_ONNX_RUNTIME_PATH_ENV);
        let runtime =
            resolve_linux_runtime_library(&executable, configured.as_deref(), RUNTIME_NAME)?;
        let _ = ort::init_from(runtime)?
            .with_name("astrlink-classifier-worker")
            .commit();
    }
    #[cfg(target_os = "windows")]
    {
        let _ = ort::init().with_name("astrlink-classifier-worker").commit();
    }
    Ok(())
}

#[cfg(target_os = "linux")]
fn resolve_linux_runtime_library(
    executable: &Path,
    configured: Option<&OsStr>,
    runtime_name: &str,
) -> io::Result<PathBuf> {
    if let Some(configured) = configured {
        let configured = PathBuf::from(configured);
        if !configured.is_absolute() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "onnx_runtime_path_not_absolute",
            ));
        }
        if !configured.is_file() {
            return Err(io::Error::new(
                io::ErrorKind::NotFound,
                "onnx_runtime_not_installed",
            ));
        }
        return Ok(configured);
    }
    runtime_library_candidates(executable, runtime_name)
        .into_iter()
        .find(|candidate| candidate.is_file())
        .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "onnx_runtime_not_installed"))
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
fn runtime_library_candidates(executable: &Path, runtime_name: &str) -> Vec<PathBuf> {
    let Some(executable_directory) = executable.parent() else {
        return Vec::new();
    };
    let mut candidates = vec![executable_directory.join(runtime_name)];
    #[cfg(target_os = "macos")]
    if let Some(contents_directory) = executable_directory.parent() {
        candidates.push(contents_directory.join("Frameworks").join(runtime_name));
    }
    #[cfg(test)]
    if let Some(debug_directory) = executable_directory.parent()
        && debug_directory
            .file_name()
            .is_some_and(|name| name == "debug" || name == "deps")
    {
        let search_root = if debug_directory
            .file_name()
            .is_some_and(|name| name == "deps")
        {
            debug_directory.parent().and_then(Path::parent)
        } else {
            debug_directory.parent()
        };
        if let Some(target_directory) = search_root {
            candidates.push(target_directory.join("release").join(runtime_name));
            candidates.push(target_directory.join(runtime_name));
        }
    }
    if let Some(manifest_dir) = option_env!("CARGO_MANIFEST_DIR") {
        candidates.push(
            Path::new(manifest_dir)
                .join("target")
                .join("release")
                .join(runtime_name),
        );
    }
    candidates
}

fn synthetic_models_only() -> io::Result<bool> {
    parse_synthetic_models_only(env::var_os(CI_SYNTHETIC_MODELS_ONLY_ENV).as_deref())
}

fn parse_synthetic_models_only(value: Option<&OsStr>) -> io::Result<bool> {
    match value {
        None => Ok(false),
        Some(value) if value == "1" => Ok(true),
        Some(_) => Err(io::Error::other("invalid_synthetic_model_guard")),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::manifest::{TAXONOMY_ID, TAXONOMY_SHA256};
    use std::{
        fs, process,
        time::{SystemTime, UNIX_EPOCH},
    };
    use tokenizers::{
        Tokenizer, models::wordlevel::WordLevel, pre_tokenizers::whitespace::Whitespace,
    };

    #[test]
    fn pad_to_eight_is_a_numeric_contract() {
        let (ids, mask) = pad_to_multiple(&[1, 2, 3, 4, 5, 6, 7], 0, 8);
        assert_eq!(ids, vec![1, 2, 3, 4, 5, 6, 7, 0]);
        assert_eq!(mask, vec![1, 1, 1, 1, 1, 1, 1, 0]);
        let (ids, mask) = pad_to_multiple(&[1, 2, 3, 4, 5, 6, 7, 8, 9, 10], 0, 8);
        assert_eq!(ids.len(), 16);
        assert_eq!(mask.iter().filter(|value| **value == 1).count(), 10);
    }

    #[test]
    fn synthetic_model_environment_guard_is_explicit_and_strict() {
        assert!(!parse_synthetic_models_only(None).expect("unset"));
        assert!(parse_synthetic_models_only(Some(OsStr::new("1"))).expect("enabled"));
        assert_eq!(
            parse_synthetic_models_only(Some(OsStr::new("true")))
                .unwrap_err()
                .to_string(),
            "invalid_synthetic_model_guard"
        );
    }

    #[test]
    fn synthetic_classifier_runs_the_micro_onnx_fixture() {
        let directory = micro_model_directory();
        let result = (|| {
            let mut engine = ClassifierEngine::load(&directory)?;
            engine.classify("hello world")
        })();
        let _ = fs::remove_dir_all(&directory);
        let classification = result.unwrap_or_else(|error| panic!("{error}"));
        assert_eq!(classification.category, "general");
        assert_eq!(classification.logits.len(), 4);
    }

    fn micro_model_directory() -> PathBuf {
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("system time")
            .as_nanos();
        let directory = std::env::temp_dir().join(format!(
            "astrlink-classifier-worker-synthetic-fixture-{}-{unique}",
            process::id()
        ));
        fs::create_dir_all(&directory).expect("create fixture directory");
        fs::write(directory.join("model.onnx"), MICRO_ONNX_MODEL).expect("write model");
        synthetic_word_tokenizer()
            .save(directory.join("tokenizer.json"), false)
            .expect("write tokenizer");
        fs::write(
            directory.join("config.json"),
            serde_json::to_vec(&serde_json::json!({
                "architectures": ["ModernBertForSequenceClassification"],
                "id2label": {
                    "0": "general",
                    "1": "research",
                    "2": "coding",
                    "3": "architect"
                }
            }))
            .expect("serialize config"),
        )
        .expect("write config");
        let files = ["model.onnx", "tokenizer.json", "config.json"]
            .into_iter()
            .map(|path| {
                serde_json::json!({
                    "path": path,
                    "size": fs::metadata(directory.join(path)).expect("metadata").len(),
                    "sha256": "0".repeat(64),
                })
            })
            .collect::<Vec<_>>();
        fs::write(
            directory.join(crate::manifest::MANIFEST_NAME),
            serde_json::to_vec(&serde_json::json!({
                "version": 1,
                "installation_id": "model_00000000000000000000000000000000",
                "identity": "astrlink/synthetic-classifier-worker-fixture@0000000000000000000000000000000000000000#synthetic_micro",
                "taxonomy_id": TAXONOMY_ID,
                "taxonomy_sha256": TAXONOMY_SHA256,
                "preprocessing": {
                    "text": "current-user-text-v3",
                    "tokens": "tokenize_head_tail-v1"
                },
                "artifact_tier": "experimental",
                "model_path": "model.onnx",
                "tokenizer_path": "tokenizer.json",
                "config_path": "config.json",
                "max_sequence_tokens": 512,
                "content_budget": 510,
                "head_tokens": 255,
                "tail_tokens": 255,
                "pad_token_id": 0,
                "pad_multiple": 8,
                "add_special_tokens": false,
                "input_names": {
                    "input_ids": "input_ids",
                    "attention_mask": "attention_mask"
                },
                "output_name": "logits",
                "id2label": {
                    "0": "general",
                    "1": "research",
                    "2": "coding",
                    "3": "architect"
                },
                "files": files
            }))
            .expect("serialize manifest"),
        )
        .expect("write manifest");
        directory
    }

    fn synthetic_word_tokenizer() -> Tokenizer {
        let vocabulary = [
            ("[UNK]".to_owned(), 0),
            ("hello".to_owned(), 1),
            ("world".to_owned(), 2),
        ]
        .into_iter()
        .collect();
        let model = WordLevel::builder()
            .vocab(vocabulary)
            .unk_token("[UNK]".into())
            .build()
            .expect("word-level model");
        let mut tokenizer = Tokenizer::new(model);
        tokenizer.with_pre_tokenizer(Some(Whitespace {}));
        tokenizer
    }

    const MICRO_ONNX_MODEL: &[u8] = include_bytes!("../testdata/synthetic-classifier.onnx");
}
