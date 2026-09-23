use std::{collections::BTreeSet, env, ffi::OsStr, fs, io, path::Path};

#[cfg(any(target_os = "linux", target_os = "macos", test))]
use std::path::PathBuf;

use ort::{
    logging::LogLevel,
    session::{Session, SessionInputValue, builder::GraphOptimizationLevel},
    value::Tensor,
};
use tokenizers::{Encoding, Tokenizer};

use crate::{
    decoder::Decoder,
    manifest::{Adapter, ModelManifest},
    protocol::{DetectedSpan, TextInput},
    sensitive::SensitiveGuard,
};

const INTRA_OP_THREADS: usize = 2;
const INTER_OP_THREADS: usize = 1;
// CI sets this explicitly so even the production-shaped worker binary refuses
// catalog identities and assets larger than the bounded synthetic fixture.
const CI_SYNTHETIC_MODELS_ONLY_ENV: &str = "ASTRLINK_CI_SYNTHETIC_MODELS_ONLY";
#[cfg(target_os = "linux")]
const LINUX_ONNX_RUNTIME_PATH_ENV: &str = "ASTRLINK_ONNX_RUNTIME_PATH";

pub struct PrivacyEngine {
    tokenizer: Tokenizer,
    apply_post_processor: bool,
    decoder: Decoder,
    sensitive: Option<SensitiveGuard>,
    session: Session,
    model_window_tokens: usize,
    content_window_tokens: usize,
    overlap_tokens: usize,
    max_request_tokens: usize,
    input_ids_name: String,
    attention_mask_name: String,
    token_type_ids_name: Option<String>,
    output_name: String,
}

impl PrivacyEngine {
    pub fn load(model_directory: &Path) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        let manifest = ModelManifest::load(model_directory)?;
        if synthetic_models_only()? {
            manifest.validate_synthetic_fixture(model_directory)?;
        }
        manifest.validate_files(model_directory)?;
        let mut tokenizer =
            Tokenizer::from_file(manifest.resolve(model_directory, &manifest.tokenizer_path))?;
        tokenizer.with_truncation(None)?;
        tokenizer.with_padding(None);
        let config = fs::read(manifest.resolve(model_directory, &manifest.config_path))?;
        let (decoder, sensitive) = match manifest.adapter {
            Adapter::PplxBioesViterbi => (
                Decoder::from_pplx_json(&config, &manifest.label_mapping),
                None,
            ),
            Adapter::OpenaiBioesViterbi => {
                let calibration_path = manifest
                    .calibration_path
                    .as_deref()
                    .ok_or_else(|| io::Error::other("invalid_model_manifest"))?;
                let calibration = fs::read(manifest.resolve(model_directory, calibration_path))?;
                (
                    Decoder::from_openai_json(&config, &calibration, &manifest.label_mapping),
                    None,
                )
            }
            Adapter::HfTokenClassification => (
                Decoder::from_hf_json(&config, manifest.tag_scheme, &manifest.label_mapping),
                None,
            ),
            Adapter::AstrlinkSensitiveGuard => {
                let calibration_path = manifest
                    .calibration_path
                    .as_deref()
                    .ok_or_else(|| io::Error::other("invalid_model_manifest"))?;
                let rules_path = manifest
                    .secret_rules_path
                    .as_deref()
                    .ok_or_else(|| io::Error::other("invalid_model_manifest"))?;
                let secret_calibration_path = manifest
                    .secret_calibration_path
                    .as_deref()
                    .ok_or_else(|| io::Error::other("invalid_model_manifest"))?;
                let calibration = fs::read(manifest.resolve(model_directory, calibration_path))?;
                let rules = fs::read(manifest.resolve(model_directory, rules_path))?;
                let secret_calibration =
                    fs::read(manifest.resolve(model_directory, secret_calibration_path))?;
                let sensitive = SensitiveGuard::from_json(
                    &rules,
                    &secret_calibration,
                    manifest.label_mapping.get("secret").cloned().flatten(),
                )
                .map_err(io::Error::other)?;
                (
                    Decoder::from_sensitive_json(&config, &calibration, &manifest.label_mapping),
                    Some(sensitive),
                )
            }
        };
        let decoder = decoder.map_err(io::Error::other)?;
        let apply_post_processor = tokenizer.get_post_processor().is_some();
        let added_special_tokens = if apply_post_processor {
            added_special_token_count(&tokenizer)?
        } else {
            0
        };
        let content_window_tokens = manifest
            .window
            .checked_sub(added_special_tokens)
            .filter(|window| *window > manifest.stride)
            .ok_or_else(|| io::Error::other("invalid_model_window"))?;

        initialize_onnx_runtime()?;
        let session = Session::builder()?
            .with_log_level(LogLevel::Error)?
            .with_intra_threads(INTRA_OP_THREADS)?
            .with_inter_threads(INTER_OP_THREADS)?
            .with_parallel_execution(false)?
            .with_optimization_level(GraphOptimizationLevel::Level3)?
            .commit_from_file(manifest.resolve(model_directory, &manifest.model_path))?;
        validate_session(&session, &manifest)?;

        Ok(Self {
            tokenizer,
            apply_post_processor,
            decoder,
            sensitive,
            session,
            model_window_tokens: manifest.window,
            content_window_tokens,
            overlap_tokens: manifest.stride,
            max_request_tokens: manifest.max_request_tokens,
            input_ids_name: manifest.input_names.input_ids,
            attention_mask_name: manifest.input_names.attention_mask,
            token_type_ids_name: manifest.input_names.token_type_ids,
            output_name: manifest.output_name,
        })
    }

    pub fn detect(
        &mut self,
        texts: &[TextInput],
    ) -> Result<Vec<DetectedSpan>, Box<dyn std::error::Error + Send + Sync>> {
        let mut total_tokens = 0_usize;
        let mut spans = Vec::new();
        for input in texts {
            let encoding = self.tokenizer.encode(input.text.as_str(), false)?;
            total_tokens =
                checked_token_total(total_tokens, encoding.len(), self.max_request_tokens)?;
            spans.extend(self.detect_encoding(input.id, &input.text, &encoding)?);
        }
        spans.sort_by_key(|span| (span.text_id, span.start, span.end));
        Ok(spans)
    }

    fn detect_encoding(
        &mut self,
        text_id: u32,
        text: &str,
        encoding: &Encoding,
    ) -> Result<Vec<DetectedSpan>, Box<dyn std::error::Error + Send + Sync>> {
        let ids = encoding.get_ids();
        let offsets = encoding.get_offsets();
        if ids.len() != offsets.len() {
            return Err(io::Error::other("invalid_tokenizer_output").into());
        }
        if ids.is_empty() {
            return Ok(Vec::new());
        }

        let label_count = self.decoder.label_count();
        let mut scores = LogProbabilityAccumulator::new(ids.len(), label_count);
        scores.preserve_logits = self.decoder.uses_raw_logits();
        for (start, end) in
            chunk_ranges_with(ids.len(), self.content_window_tokens, self.overlap_tokens)
        {
            let window = prepare_model_window(
                &self.tokenizer,
                encoding,
                start,
                end,
                self.apply_post_processor,
            )?;
            let sequence_length = window.input_ids.len();
            if sequence_length > self.model_window_tokens {
                return Err(io::Error::other("invalid_model_window").into());
            }
            let input_ids = Tensor::from_array(([1, sequence_length], window.input_ids))?;
            let attention_mask = Tensor::from_array(([1, sequence_length], window.attention_mask))?;
            let mut inputs = vec![
                (
                    self.input_ids_name.clone(),
                    SessionInputValue::from(input_ids),
                ),
                (
                    self.attention_mask_name.clone(),
                    SessionInputValue::from(attention_mask),
                ),
            ];
            if let Some(token_type_ids_name) = &self.token_type_ids_name {
                let token_type_ids =
                    Tensor::from_array(([1, sequence_length], window.token_type_ids))?;
                inputs.push((
                    token_type_ids_name.clone(),
                    SessionInputValue::from(token_type_ids),
                ));
            }
            let outputs = self.session.run(inputs)?;
            let output = outputs
                .get(&self.output_name)
                .ok_or_else(|| io::Error::other("missing_logits"))?;
            let (shape, logits) = output.try_extract_tensor::<f32>()?;
            if shape.as_ref() != [1, sequence_length as i64, label_count as i64] {
                return Err(io::Error::other("invalid_logits_shape").into());
            }
            let mut content_logits = Vec::with_capacity((end - start) * label_count);
            for position in window.content_positions {
                content_logits.extend_from_slice(
                    &logits[position * label_count..(position + 1) * label_count],
                );
            }
            scores
                .add_window(start, &content_logits)
                .map_err(io::Error::other)?;
        }
        let scores = scores.finish().map_err(io::Error::other)?;
        let mut model_spans = if self.decoder.uses_raw_logits() {
            self.decoder.decode_pplx(text_id, &scores, offsets, text)
        } else if self.sensitive.is_some() {
            self.decoder
                .decode_sensitive(text_id, &scores, offsets, text)
        } else {
            self.decoder.decode(text_id, &scores, offsets)
        }
        .map_err(io::Error::other)?;
        if self.decoder.uses_raw_logits() {
            crate::pplx::normalize_boundaries(text, &mut model_spans);
        }
        let Some(guard) = self.sensitive.as_ref() else {
            return Ok(model_spans);
        };
        let rule_confidence = self
            .decoder
            .sensitive_precision_lower_bound(text, "secret")
            .ok_or_else(|| io::Error::other("invalid_calibration"))?;
        Ok(guard.fuse(text_id, text, model_spans, rule_confidence))
    }
}

fn synthetic_models_only() -> io::Result<bool> {
    let requested =
        parse_synthetic_models_only(env::var_os(CI_SYNTHETIC_MODELS_ONLY_ENV).as_deref())?;
    Ok(cfg!(test) || requested)
}

fn parse_synthetic_models_only(value: Option<&OsStr>) -> io::Result<bool> {
    match value {
        None => Ok(false),
        Some(value) if value == "1" => Ok(true),
        Some(_) => Err(io::Error::other("invalid_synthetic_model_guard")),
    }
}

struct ModelWindow {
    input_ids: Vec<i64>,
    attention_mask: Vec<i64>,
    token_type_ids: Vec<i64>,
    content_positions: Vec<usize>,
}

fn added_special_token_count(
    tokenizer: &Tokenizer,
) -> Result<usize, Box<dyn std::error::Error + Send + Sync>> {
    let mut empty = Encoding::default();
    empty.set_sequence_id(0);
    let processed = tokenizer.post_process(empty, None, true)?;
    if processed
        .get_sequence_ids()
        .into_iter()
        .any(|sequence| sequence.is_some())
    {
        return Err(io::Error::other("invalid_tokenizer_post_processor").into());
    }
    Ok(processed.len())
}

fn prepare_model_window(
    tokenizer: &Tokenizer,
    encoding: &Encoding,
    start: usize,
    end: usize,
    apply_post_processor: bool,
) -> Result<ModelWindow, Box<dyn std::error::Error + Send + Sync>> {
    if start >= end || end > encoding.len() {
        return Err(io::Error::other("invalid_tokenizer_output").into());
    }
    let expected = end - start;
    let processed = if apply_post_processor {
        let mut content = Encoding::new(
            encoding.get_ids()[start..end].to_vec(),
            encoding.get_type_ids()[start..end].to_vec(),
            encoding.get_tokens()[start..end].to_vec(),
            encoding.get_word_ids()[start..end].to_vec(),
            encoding.get_offsets()[start..end].to_vec(),
            encoding.get_special_tokens_mask()[start..end].to_vec(),
            encoding.get_attention_mask()[start..end].to_vec(),
            Vec::new(),
            Default::default(),
        );
        content.set_sequence_id(0);
        tokenizer.post_process(content, None, true)?
    } else {
        let mut content = Encoding::new(
            encoding.get_ids()[start..end].to_vec(),
            encoding.get_type_ids()[start..end].to_vec(),
            encoding.get_tokens()[start..end].to_vec(),
            encoding.get_word_ids()[start..end].to_vec(),
            encoding.get_offsets()[start..end].to_vec(),
            encoding.get_special_tokens_mask()[start..end].to_vec(),
            encoding.get_attention_mask()[start..end].to_vec(),
            Vec::new(),
            Default::default(),
        );
        content.set_sequence_id(0);
        content
    };
    let sequence_ids = processed.get_sequence_ids();
    let content_positions = sequence_ids
        .iter()
        .enumerate()
        .filter_map(|(position, sequence)| (*sequence == Some(0)).then_some(position))
        .collect::<Vec<_>>();
    if content_positions.len() != expected || processed.is_empty() {
        return Err(io::Error::other("invalid_tokenizer_post_processor").into());
    }
    for (local, position) in content_positions.iter().copied().enumerate() {
        if processed.get_ids()[position] != encoding.get_ids()[start + local]
            || processed.get_tokens()[position] != encoding.get_tokens()[start + local]
        {
            return Err(io::Error::other("invalid_tokenizer_post_processor").into());
        }
    }
    if processed.get_ids().len() != processed.get_attention_mask().len()
        || processed.get_ids().len() != processed.get_type_ids().len()
    {
        return Err(io::Error::other("invalid_tokenizer_output").into());
    }
    Ok(ModelWindow {
        input_ids: processed
            .get_ids()
            .iter()
            .map(|value| i64::from(*value))
            .collect(),
        attention_mask: processed
            .get_attention_mask()
            .iter()
            .map(|value| i64::from(*value))
            .collect(),
        token_type_ids: processed
            .get_type_ids()
            .iter()
            .map(|value| i64::from(*value))
            .collect(),
        content_positions,
    })
}

fn validate_session(session: &Session, manifest: &ModelManifest) -> io::Result<()> {
    let actual_inputs = session
        .inputs()
        .iter()
        .map(|input| input.name())
        .collect::<BTreeSet<_>>();
    let mut expected_inputs = BTreeSet::from([
        manifest.input_names.input_ids.as_str(),
        manifest.input_names.attention_mask.as_str(),
    ]);
    if let Some(token_type_ids) = manifest.input_names.token_type_ids.as_deref() {
        expected_inputs.insert(token_type_ids);
    }
    if actual_inputs != expected_inputs
        || !session
            .outputs()
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
            .with_name("astrlink-privacy-worker")
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
            .with_name("astrlink-privacy-worker")
            .commit();
    }
    #[cfg(target_os = "windows")]
    {
        let _ = ort::init().with_name("astrlink-privacy-worker").commit();
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
    let candidates = vec![executable_directory.join(runtime_name)];
    #[cfg(target_os = "macos")]
    let mut candidates = candidates;
    #[cfg(all(test, not(target_os = "macos")))]
    let mut candidates = candidates;
    #[cfg(target_os = "macos")]
    if let Some(contents_directory) = executable_directory.parent() {
        candidates.push(contents_directory.join("Frameworks").join(runtime_name));
    }
    #[cfg(test)]
    if let Some(debug_directory) = executable_directory.parent()
        && debug_directory
            .file_name()
            .is_some_and(|name| name == "debug")
        && let Some(target_directory) = debug_directory.parent()
    {
        candidates.push(target_directory.join("release").join(runtime_name));
    }
    candidates
}

struct LogProbabilityAccumulator {
    sums: Vec<f64>,
    coverage: Vec<u32>,
    label_count: usize,
    preserve_logits: bool,
}

impl LogProbabilityAccumulator {
    fn new(token_count: usize, label_count: usize) -> Self {
        Self {
            sums: vec![0.0; token_count * label_count],
            coverage: vec![0; token_count],
            label_count,
            preserve_logits: false,
        }
    }

    fn add_window(&mut self, start: usize, logits: &[f32]) -> Result<(), &'static str> {
        if !logits.len().is_multiple_of(self.label_count) {
            return Err("invalid_logits");
        }
        let token_count = logits.len() / self.label_count;
        let end = start
            .checked_add(token_count)
            .ok_or("invalid_logits_shape")?;
        if end > self.coverage.len() {
            return Err("invalid_logits_shape");
        }

        for (local_token, row) in logits.chunks_exact(self.label_count).enumerate() {
            let global_token = start + local_token;
            if row.iter().any(|value| !value.is_finite()) {
                return Err("invalid_logits");
            }
            let maximum = row
                .iter()
                .copied()
                .map(f64::from)
                .fold(f64::NEG_INFINITY, f64::max);
            let denominator = row
                .iter()
                .map(|value| (f64::from(*value) - maximum).exp())
                .sum::<f64>();
            if denominator == 0.0 || !denominator.is_finite() {
                return Err("invalid_logits");
            }
            let log_denominator = denominator.ln();
            let normalizer = if self.preserve_logits {
                0.0
            } else {
                maximum + log_denominator
            };
            let destination = &mut self.sums
                [global_token * self.label_count..(global_token + 1) * self.label_count];
            for (sum, value) in destination.iter_mut().zip(row) {
                *sum += f64::from(*value) - normalizer;
            }
            self.coverage[global_token] = self.coverage[global_token]
                .checked_add(1)
                .ok_or("invalid_logits")?;
        }
        Ok(())
    }

    fn finish(self) -> Result<Vec<f32>, &'static str> {
        if self.coverage.contains(&0) {
            return Err("incomplete_logits_coverage");
        }
        let mut averaged = self.sums;
        for (token, count) in self.coverage.into_iter().enumerate() {
            let divisor = f64::from(count);
            for value in &mut averaged[token * self.label_count..(token + 1) * self.label_count] {
                *value /= divisor;
            }
        }
        Ok(averaged.into_iter().map(|value| value as f32).collect())
    }

    #[cfg(test)]
    fn coverage(&self) -> &[u32] {
        &self.coverage
    }
}

fn checked_token_total(current: usize, additional: usize, maximum: usize) -> io::Result<usize> {
    let total = current
        .checked_add(additional)
        .ok_or_else(|| io::Error::other("token_limit_exceeded"))?;
    if total > maximum {
        return Err(io::Error::other("token_limit_exceeded"));
    }
    Ok(total)
}

fn chunk_ranges_with(
    token_count: usize,
    window_tokens: usize,
    overlap_tokens: usize,
) -> Vec<(usize, usize)> {
    debug_assert!(overlap_tokens < window_tokens);
    let mut ranges = Vec::new();
    let mut start = 0_usize;
    while start < token_count {
        let end = (start + window_tokens).min(token_count);
        ranges.push((start, end));
        if end == token_count {
            break;
        }
        start = end - overlap_tokens;
    }
    ranges
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{
        process,
        sync::atomic::{AtomicU64, Ordering},
        time::{SystemTime, UNIX_EPOCH},
    };
    use tokenizers::{
        models::wordlevel::WordLevel, pre_tokenizers::whitespace::Whitespace,
        processors::template::TemplateProcessing,
    };

    const TEST_LABEL_COUNT: usize = 33;
    const TEST_WINDOW_TOKENS: usize = 4096;
    const TEST_OVERLAP_TOKENS: usize = 128;
    const TEST_MAX_REQUEST_TOKENS: usize = 128 * 1024;

    const MICRO_ONNX_MODEL: &[u8] = &[
        8, 9, 18, 21, 97, 115, 116, 114, 108, 105, 110, 107, 45, 116, 101, 115, 116, 45, 102, 105,
        120, 116, 117, 114, 101, 58, 186, 4, 10, 31, 10, 9, 105, 110, 112, 117, 116, 95, 105, 100,
        115, 18, 11, 105, 110, 112, 117, 116, 95, 115, 104, 97, 112, 101, 34, 5, 83, 104, 97, 112,
        101, 10, 63, 18, 17, 108, 97, 98, 101, 108, 95, 119, 105, 100, 116, 104, 95, 118, 97, 108,
        117, 101, 34, 8, 67, 111, 110, 115, 116, 97, 110, 116, 42, 32, 10, 5, 118, 97, 108, 117,
        101, 42, 20, 8, 1, 16, 7, 58, 1, 33, 66, 11, 108, 97, 98, 101, 108, 95, 119, 105, 100, 116,
        104, 160, 1, 4, 10, 67, 10, 11, 105, 110, 112, 117, 116, 95, 115, 104, 97, 112, 101, 10,
        17, 108, 97, 98, 101, 108, 95, 119, 105, 100, 116, 104, 95, 118, 97, 108, 117, 101, 18, 12,
        111, 117, 116, 112, 117, 116, 95, 115, 104, 97, 112, 101, 34, 6, 67, 111, 110, 99, 97, 116,
        42, 11, 10, 4, 97, 120, 105, 115, 24, 0, 160, 1, 2, 10, 201, 1, 18, 17, 98, 97, 115, 101,
        95, 108, 111, 103, 105, 116, 115, 95, 118, 97, 108, 117, 101, 34, 8, 67, 111, 110, 115,
        116, 97, 110, 116, 42, 169, 1, 10, 5, 118, 97, 108, 117, 101, 42, 156, 1, 8, 1, 8, 1, 8,
        33, 16, 1, 34, 132, 1, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0,
        32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0,
        32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0,
        32, 65, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0,
        32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0,
        32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 66, 11, 98, 97, 115,
        101, 95, 108, 111, 103, 105, 116, 115, 160, 1, 4, 10, 49, 10, 17, 98, 97, 115, 101, 95,
        108, 111, 103, 105, 116, 115, 95, 118, 97, 108, 117, 101, 10, 12, 111, 117, 116, 112, 117,
        116, 95, 115, 104, 97, 112, 101, 18, 6, 108, 111, 103, 105, 116, 115, 34, 6, 69, 120, 112,
        97, 110, 100, 18, 29, 97, 115, 116, 114, 108, 105, 110, 107, 95, 109, 105, 99, 114, 111,
        95, 112, 114, 105, 118, 97, 99, 121, 95, 102, 105, 108, 116, 101, 114, 90, 35, 10, 9, 105,
        110, 112, 117, 116, 95, 105, 100, 115, 18, 22, 10, 20, 8, 7, 18, 16, 10, 2, 8, 1, 10, 10,
        18, 8, 115, 101, 113, 117, 101, 110, 99, 101, 90, 40, 10, 14, 97, 116, 116, 101, 110, 116,
        105, 111, 110, 95, 109, 97, 115, 107, 18, 22, 10, 20, 8, 7, 18, 16, 10, 2, 8, 1, 10, 10,
        18, 8, 115, 101, 113, 117, 101, 110, 99, 101, 98, 36, 10, 6, 108, 111, 103, 105, 116, 115,
        18, 26, 10, 24, 8, 1, 18, 20, 10, 2, 8, 1, 10, 10, 18, 8, 115, 101, 113, 117, 101, 110, 99,
        101, 10, 2, 8, 33, 66, 4, 10, 0, 16, 13,
    ];

    #[test]
    fn preserves_raw_logits_for_pplx_confidence_across_windows() {
        let mut accumulator = LogProbabilityAccumulator::new(3, 2);
        accumulator.preserve_logits = true;
        accumulator
            .add_window(0, &[1.0, 3.0, 4.0, 6.0])
            .expect("first");
        accumulator
            .add_window(1, &[2.0, 4.0, 7.0, 9.0])
            .expect("second");
        assert_eq!(
            accumulator.finish().expect("scores"),
            vec![1.0, 3.0, 3.0, 5.0, 7.0, 9.0]
        );
    }

    #[test]
    fn averages_log_softmax_scores_for_overlapping_tokens() {
        let mut accumulator = LogProbabilityAccumulator::new(4, TEST_LABEL_COUNT);
        let first = rows(&[(0, 0.0), (0, 0.0), (1, 2.0)]);
        let second = rows(&[(1, 4.0), (0, 0.0)]);
        accumulator.add_window(0, &first).expect("first window");
        accumulator.add_window(2, &second).expect("second window");
        assert_eq!(accumulator.coverage(), &[1, 1, 2, 1]);

        let first_overlap = log_softmax(&first[2 * TEST_LABEL_COUNT..3 * TEST_LABEL_COUNT]);
        let second_overlap = log_softmax(&second[..TEST_LABEL_COUNT]);
        let averaged = accumulator.finish().expect("complete coverage");
        for label in 0..TEST_LABEL_COUNT {
            let expected = (first_overlap[label] + second_overlap[label]) / 2.0;
            assert!((averaged[2 * TEST_LABEL_COUNT + label] - expected).abs() < 1e-6);
        }
    }

    #[test]
    fn global_viterbi_joins_bioes_entity_across_window_boundary() {
        let decoder = test_decoder();
        let window_tokens = 8;
        let overlap_tokens = 3;
        let token_count = window_tokens + 2;
        let entity_start = window_tokens - overlap_tokens - 2;
        let entity_end = window_tokens;
        let offsets = (0..token_count)
            .map(|token| (token, token + 1))
            .collect::<Vec<_>>();
        let mut accumulator = LogProbabilityAccumulator::new(token_count, TEST_LABEL_COUNT);

        for (start, end) in chunk_ranges_with(token_count, window_tokens, overlap_tokens) {
            let mut logits = vec![-20.0; (end - start) * TEST_LABEL_COUNT];
            for global_token in start..end {
                let row = &mut logits[(global_token - start) * TEST_LABEL_COUNT
                    ..(global_token - start + 1) * TEST_LABEL_COUNT];
                row[0] = 0.0;
                if global_token == entity_start {
                    row[1] = 2.0;
                } else if global_token == entity_end {
                    row[3] = 2.0;
                } else if (entity_start + 1..entity_end).contains(&global_token) {
                    row[2] = 0.01;
                }
            }
            accumulator
                .add_window(start, &logits)
                .expect("aggregate window");
        }

        let scores = accumulator.finish().expect("complete coverage");
        let spans = decoder.decode(7, &scores, &offsets).expect("global decode");
        assert_eq!(spans.len(), 1);
        assert_eq!(spans[0].label, "account");
        assert_eq!(
            (spans[0].start, spans[0].end),
            (entity_start, entity_end + 1)
        );
    }

    #[test]
    fn chunks_with_exact_overlap_and_full_coverage() {
        let ranges = chunk_ranges_with(8_200, TEST_WINDOW_TOKENS, TEST_OVERLAP_TOKENS);
        assert_eq!(ranges, vec![(0, 4096), (3968, 8064), (7936, 8200)]);
        for pair in ranges.windows(2) {
            assert_eq!(pair[0].1 - pair[1].0, TEST_OVERLAP_TOKENS);
        }
        assert_eq!(chunk_ranges_with(5, 2, 0), vec![(0, 2), (2, 4), (4, 5)]);
    }

    #[test]
    fn hf_post_processor_wraps_every_content_window_without_polluting_offsets() {
        let tokenizer = synthetic_template_tokenizer();
        let encoding = tokenizer
            .encode("你好 secret 你好 secret", false)
            .expect("encode content without special tokens");
        assert_eq!(
            encoding.get_offsets(),
            &[(0, "你好".len()), (7, 13), (14, 20), (21, 27)]
        );
        assert_eq!(
            added_special_token_count(&tokenizer).expect("special count"),
            2
        );
        let ranges = chunk_ranges_with(encoding.len(), 2, 1);
        assert_eq!(ranges, vec![(0, 2), (1, 3), (2, 4)]);

        for (start, end) in ranges {
            let window = prepare_model_window(&tokenizer, &encoding, start, end, true)
                .expect("prepare model window");
            assert_eq!(window.input_ids.first(), Some(&3));
            assert_eq!(window.input_ids.last(), Some(&4));
            assert_eq!(window.input_ids.len(), 4);
            assert_eq!(window.content_positions, vec![1, 2]);
            for (local, position) in window.content_positions.iter().copied().enumerate() {
                assert_eq!(
                    window.input_ids[position],
                    i64::from(encoding.get_ids()[start + local])
                );
            }
        }
    }

    #[test]
    fn enforces_request_token_limit_across_segments() {
        assert_eq!(
            checked_token_total(TEST_MAX_REQUEST_TOKENS - 1, 1, TEST_MAX_REQUEST_TOKENS)
                .expect("at boundary"),
            TEST_MAX_REQUEST_TOKENS
        );
        assert_eq!(
            checked_token_total(TEST_MAX_REQUEST_TOKENS, 1, TEST_MAX_REQUEST_TOKENS)
                .expect_err("over limit")
                .to_string(),
            "token_limit_exceeded"
        );
    }

    #[test]
    fn synthetic_model_environment_guard_is_explicit_and_strict() {
        assert!(!parse_synthetic_models_only(None).expect("unset"));
        assert!(parse_synthetic_models_only(Some(OsStr::new("1"))).expect("enabled"));
        assert_eq!(
            parse_synthetic_models_only(Some(OsStr::new("true")))
                .expect_err("ambiguous value must fail closed")
                .to_string(),
            "invalid_synthetic_model_guard"
        );
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn locates_development_and_bundle_runtime_candidates() {
        let candidates = runtime_library_candidates(
            Path::new("/Applications/AstrLink.app/Contents/MacOS/astrlink-privacy-worker"),
            "libonnxruntime.1.23.2.dylib",
        );
        assert_eq!(
            candidates,
            vec![
                PathBuf::from(
                    "/Applications/AstrLink.app/Contents/MacOS/libonnxruntime.1.23.2.dylib"
                ),
                PathBuf::from(
                    "/Applications/AstrLink.app/Contents/Frameworks/libonnxruntime.1.23.2.dylib"
                ),
            ]
        );
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn resolves_only_absolute_existing_configured_linux_runtime() {
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("system time")
            .as_nanos();
        let directory = std::env::temp_dir().join(format!(
            "astrlink-privacy-worker-runtime-fixture-{}-{unique}",
            process::id()
        ));
        fs::create_dir_all(&directory).expect("create runtime fixture directory");
        let runtime = directory.join("libonnxruntime.so.1.23.2");
        fs::write(&runtime, b"fixture").expect("write runtime fixture");

        assert_eq!(
            resolve_linux_runtime_library(
                Path::new("/opt/AstrLink/astrlink-privacy-worker"),
                Some(runtime.as_os_str()),
                "libonnxruntime.so.1.23.2",
            )
            .expect("absolute existing runtime"),
            runtime
        );
        assert_eq!(
            resolve_linux_runtime_library(
                Path::new("/opt/AstrLink/astrlink-privacy-worker"),
                Some(OsStr::new("libonnxruntime.so.1.23.2")),
                "libonnxruntime.so.1.23.2",
            )
            .expect_err("relative runtime must fail")
            .kind(),
            io::ErrorKind::InvalidInput
        );
        assert_eq!(
            resolve_linux_runtime_library(
                Path::new("/opt/AstrLink/astrlink-privacy-worker"),
                Some(directory.join("missing.so").as_os_str()),
                "libonnxruntime.so.1.23.2",
            )
            .expect_err("missing runtime must fail")
            .kind(),
            io::ErrorKind::NotFound
        );

        fs::remove_dir_all(directory).expect("remove runtime fixture directory");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn locates_sibling_and_release_linux_runtime_candidates() {
        assert_eq!(
            runtime_library_candidates(
                Path::new("/workspace/apps/privacy-worker/target/debug/deps/privacy_worker_tests"),
                "libonnxruntime.so.1.23.2",
            ),
            vec![
                PathBuf::from(
                    "/workspace/apps/privacy-worker/target/debug/deps/libonnxruntime.so.1.23.2"
                ),
                PathBuf::from(
                    "/workspace/apps/privacy-worker/target/release/libonnxruntime.so.1.23.2"
                ),
            ]
        );
    }

    #[test]
    fn micro_onnx_fixture_runs_end_to_end_with_utf8_offsets() {
        let directory = micro_model_directory();
        let result = (|| {
            let mut engine = PrivacyEngine::load(&directory)?;
            engine.detect(&[TextInput {
                id: 17,
                text: "你好 secret".into(),
            }])
        })();
        let _ = fs::remove_dir_all(&directory);

        let spans = result.expect("load and run micro ONNX fixture");
        assert_eq!(spans.len(), 2);
        assert_eq!(
            (
                spans[0].text_id,
                spans[0].label.as_str(),
                spans[0].start,
                spans[0].end
            ),
            (17, "email", 0, "你好".len())
        );
        assert_eq!(
            (
                spans[1].text_id,
                spans[1].label.as_str(),
                spans[1].start,
                spans[1].end
            ),
            (17, "email", "你好 ".len(), "你好 secret".len())
        );
    }

    #[test]
    fn openai_adapter_uses_sheltron_style_post_processor_for_every_window() {
        let directory = micro_model_directory();
        let manifest_path = directory.join(crate::manifest::MANIFEST_NAME);
        let mut manifest: serde_json::Value =
            serde_json::from_slice(&fs::read(&manifest_path).expect("read fixture manifest"))
                .expect("parse fixture manifest");
        configure_template_windows(&directory, &mut manifest);
        fs::write(
            &manifest_path,
            serde_json::to_vec(&manifest).expect("serialize OpenAI manifest"),
        )
        .expect("write OpenAI manifest");

        let result = (|| {
            let mut engine = PrivacyEngine::load(&directory)?;
            engine.detect(&[TextInput {
                id: 19,
                text: "你好 secret 你好 secret".into(),
            }])
        })();
        let _ = fs::remove_dir_all(&directory);

        let spans = result.expect("run OpenAI adapter with template post-processor");
        assert_eq!(spans.len(), 4);
        assert!(spans.iter().all(|span| span.label == "email"));
    }

    #[test]
    fn generic_hf_adapter_runs_the_synthetic_onnx_fixture() {
        let directory = micro_model_directory();
        let manifest_path = directory.join(crate::manifest::MANIFEST_NAME);
        let mut manifest: serde_json::Value =
            serde_json::from_slice(&fs::read(&manifest_path).expect("read fixture manifest"))
                .expect("parse fixture manifest");
        configure_template_windows(&directory, &mut manifest);
        manifest["adapter"] = "hf_token_classification".into();
        manifest["calibration_path"] = serde_json::Value::Null;
        manifest["label_mapping"] = serde_json::json!({
            "account_number": "account",
            "private_address": "private_address",
            "private_date": "private_date",
            "private_email": "email",
            "private_person": "private_person",
            "private_phone": "phone",
            "private_url": "url",
            "secret": "common_secret",
        });
        fs::write(
            &manifest_path,
            serde_json::to_vec(&manifest).expect("serialize generic manifest"),
        )
        .expect("write generic manifest");

        let result = (|| {
            let mut engine = PrivacyEngine::load(&directory)?;
            engine.detect(&[TextInput {
                id: 23,
                text: "你好 secret 你好 secret".into(),
            }])
        })();
        let _ = fs::remove_dir_all(&directory);

        let spans = result.expect("run generic adapter fixture");
        assert_eq!(spans.len(), 4);
        assert!(spans.iter().all(|span| span.label == "email"));
        assert_eq!((spans[0].start, spans[0].end), (0, "你好".len()));
        assert_eq!(
            (spans[3].start, spans[3].end),
            ("你好 secret 你好 ".len(), "你好 secret 你好 secret".len())
        );
    }

    fn configure_template_windows(directory: &Path, manifest: &mut serde_json::Value) {
        synthetic_template_tokenizer()
            .save(directory.join("tokenizer.json"), false)
            .expect("write template tokenizer");
        manifest["window"] = 4.into();
        manifest["stride"] = 1.into();
        let tokenizer_size = fs::metadata(directory.join("tokenizer.json"))
            .expect("template tokenizer metadata")
            .len();
        for file in manifest["files"]
            .as_array_mut()
            .expect("fixture file descriptors")
        {
            if file["path"] == "tokenizer.json" {
                file["size"] = tokenizer_size.into();
            }
        }
    }

    fn synthetic_template_tokenizer() -> Tokenizer {
        let vocabulary = [
            ("[UNK]".to_owned(), 0),
            ("你好".to_owned(), 1),
            ("secret".to_owned(), 2),
            ("[CLS]".to_owned(), 3),
            ("[SEP]".to_owned(), 4),
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
        tokenizer.with_post_processor(Some(
            TemplateProcessing::builder()
                .try_single("[CLS] $A [SEP]")
                .expect("single template")
                .try_pair("[CLS] $A [SEP] $B:1 [SEP]:1")
                .expect("pair template")
                .special_tokens(vec![("[CLS]", 3), ("[SEP]", 4)])
                .build()
                .expect("template processor"),
        ));
        tokenizer
    }

    fn micro_model_directory() -> PathBuf {
        static NEXT_FIXTURE_ID: AtomicU64 = AtomicU64::new(0);
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("system time")
            .as_nanos();
        let directory = std::env::temp_dir().join(format!(
            "astrlink-privacy-worker-synthetic-fixture-{}-{unique}-{}",
            process::id(),
            NEXT_FIXTURE_ID.fetch_add(1, Ordering::Relaxed)
        ));
        let onnx_directory = directory.join("onnx");
        fs::create_dir_all(&onnx_directory).expect("create fixture directory");
        fs::write(onnx_directory.join("model_q4.onnx"), MICRO_ONNX_MODEL).expect("write model");
        fs::write(onnx_directory.join("model_q4.onnx_data"), b"external")
            .expect("write external data");
        fs::write(
            directory.join("tokenizer.json"),
            r#"{
                "version":"1.0",
                "truncation":null,
                "padding":null,
                "added_tokens":[],
                "normalizer":null,
                "pre_tokenizer":{"type":"Whitespace"},
                "post_processor":null,
                "decoder":null,
                "model":{
                    "type":"WordLevel",
                    "vocab":{"[UNK]":0,"你好":1,"secret":2},
                    "unk_token":"[UNK]"
                }
            }"#,
        )
        .expect("write tokenizer");

        let mut labels = serde_json::Map::new();
        labels.insert("0".into(), serde_json::Value::String("O".into()));
        let mut index = 1;
        for entity in [
            "account_number",
            "private_address",
            "private_date",
            "private_email",
            "private_person",
            "private_phone",
            "private_url",
            "secret",
        ] {
            for prefix in ["B", "I", "E", "S"] {
                labels.insert(
                    index.to_string(),
                    serde_json::Value::String(format!("{prefix}-{entity}")),
                );
                index += 1;
            }
        }
        fs::write(
            directory.join("config.json"),
            serde_json::to_vec(&serde_json::json!({"id2label": labels})).expect("serialize config"),
        )
        .expect("write config");
        fs::write(
            directory.join("viterbi_calibration.json"),
            br#"{
                "operating_points":{
                    "default":{
                        "biases":{
                            "transition_bias_background_stay":0.0,
                            "transition_bias_background_to_start":0.0,
                            "transition_bias_end_to_background":0.0,
                            "transition_bias_end_to_start":0.0,
                            "transition_bias_inside_to_continue":0.0,
                            "transition_bias_inside_to_end":0.0
                        }
                    }
                }
            }"#,
        )
        .expect("write calibration");
        let fixture_files = [
            "onnx/model_q4.onnx",
            "onnx/model_q4.onnx_data",
            "tokenizer.json",
            "config.json",
            "viterbi_calibration.json",
        ]
        .into_iter()
        .map(|path| {
            serde_json::json!({
                "path": path,
                "size": fs::metadata(directory.join(path))
                    .expect("fixture metadata")
                    .len(),
                "sha256": "0".repeat(64),
            })
        })
        .collect::<Vec<_>>();
        fs::write(
            directory.join(crate::manifest::MANIFEST_NAME),
            serde_json::to_vec(&serde_json::json!({
                "version": 1,
                "installation_id": "model_00000000000000000000000000000000",
                "identity": "astrlink/synthetic-privacy-worker-fixture@0000000000000000000000000000000000000000#synthetic_micro",
                "repo_id": "astrlink/synthetic-privacy-worker-fixture",
                "revision": "0000000000000000000000000000000000000000",
                "variant_id": "synthetic_micro",
                "adapter": "openai_bioes_viterbi",
                "model_path": "onnx/model_q4.onnx",
                "external_data_paths": ["onnx/model_q4.onnx_data"],
                "tokenizer_path": "tokenizer.json",
                "config_path": "config.json",
                "calibration_path": "viterbi_calibration.json",
                "tag_scheme": "bioes",
                "window": TEST_WINDOW_TOKENS,
                "stride": TEST_OVERLAP_TOKENS,
                "max_request_tokens": TEST_MAX_REQUEST_TOKENS,
                "input_names": {
                    "input_ids": "input_ids",
                    "attention_mask": "attention_mask",
                    "token_type_ids": null,
                },
                "output_name": "logits",
                "label_mapping": {
                    "account_number": "account",
                    "private_address": "private_address",
                    "private_date": "private_date",
                    "private_email": "email",
                    "private_person": "private_person",
                    "private_phone": "phone",
                    "private_url": "url",
                    "secret": "common_secret",
                },
                "files": fixture_files,
            }))
            .expect("serialize manifest"),
        )
        .expect("write manifest");
        directory
    }

    fn rows(preferred: &[(usize, f32)]) -> Vec<f32> {
        let mut logits = vec![0.0; preferred.len() * TEST_LABEL_COUNT];
        for (token, (label, score)) in preferred.iter().copied().enumerate() {
            logits[token * TEST_LABEL_COUNT + label] = score;
        }
        logits
    }

    fn log_softmax(row: &[f32]) -> Vec<f32> {
        let maximum = row
            .iter()
            .copied()
            .map(f64::from)
            .fold(f64::NEG_INFINITY, f64::max);
        let denominator = row
            .iter()
            .map(|value| (f64::from(*value) - maximum).exp())
            .sum::<f64>();
        row.iter()
            .map(|value| (f64::from(*value) - maximum - denominator.ln()) as f32)
            .collect()
    }

    fn test_decoder() -> Decoder {
        let mut id2label = serde_json::Map::new();
        id2label.insert("0".into(), "O".into());
        let entities = [
            "account_number",
            "private_address",
            "private_date",
            "private_email",
            "private_person",
            "private_phone",
            "private_url",
            "secret",
        ];
        let mut index = 1;
        for entity in entities {
            for prefix in ["B", "I", "E", "S"] {
                id2label.insert(index.to_string(), format!("{prefix}-{entity}").into());
                index += 1;
            }
        }
        let config =
            serde_json::to_vec(&serde_json::json!({ "id2label": id2label })).expect("config");
        let calibration = serde_json::to_vec(&serde_json::json!({
            "operating_points": {
                "default": {
                    "biases": {
                        "transition_bias_background_stay": 0.0,
                        "transition_bias_background_to_start": 0.0,
                        "transition_bias_end_to_background": 0.0,
                        "transition_bias_end_to_start": 0.0,
                        "transition_bias_inside_to_continue": 0.0,
                        "transition_bias_inside_to_end": 0.0
                    }
                }
            }
        }))
        .expect("calibration");
        Decoder::from_openai_json(&config, &calibration, &openai_label_mapping()).expect("decoder")
    }

    fn openai_label_mapping() -> std::collections::BTreeMap<String, Option<String>> {
        std::collections::BTreeMap::from([
            ("account_number".into(), Some("account".into())),
            ("private_address".into(), Some("private_address".into())),
            ("private_date".into(), Some("private_date".into())),
            ("private_email".into(), Some("email".into())),
            ("private_person".into(), Some("private_person".into())),
            ("private_phone".into(), Some("phone".into())),
            ("private_url".into(), Some("url".into())),
            ("secret".into(), Some("common_secret".into())),
        ])
    }
}
