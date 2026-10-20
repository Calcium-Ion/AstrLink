use std::{
    collections::BTreeSet,
    env,
    ffi::OsStr,
    fs, io,
    path::Path,
    sync::{Arc, OnceLock},
};

#[cfg(any(target_os = "linux", target_os = "macos", test))]
use std::path::PathBuf;

use ort::{
    environment::Environment,
    logging::LogLevel,
    session::{
        Session, SessionInputValue,
        builder::{GraphOptimizationLevel, SessionBuilder},
    },
    tensor::TensorElementType,
    value::Tensor,
};
use tokenizers::{Encoding, Tokenizer};

use crate::{
    decoder::{Decoder, offset_contract},
    manifest::{Adapter, ExecutionProvider, ModelManifest},
    priority,
    protocol::{DetectedSpan, TextInput},
    sensitive::SensitiveGuard,
    span_contract,
};

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
    span_contract: bool,
    session: Session,
    model_window_tokens: usize,
    content_window_tokens: usize,
    overlap_tokens: usize,
    max_request_tokens: usize,
    input_ids_name: String,
    attention_mask_name: String,
    token_type_ids_name: Option<String>,
    output_name: String,
    static_padding: Option<StaticPadding>,
    coreml: bool,
}

/// Compiled Core ML models live next to the installation they came from: the
/// directory is unique per installation id and is removed with it, and Core
/// already gives the worker's user exclusive access to it. Core never
/// declares a model file under this name.
const COREML_CACHE_DIRECTORY: &str = "coreml-cache";

/// A static-shape model sees every window padded to `length`; the padded
/// positions carry `pad_token_id`, attention mask 0 and token type 0.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct StaticPadding {
    length: usize,
    pad_token_id: i64,
}

impl PrivacyEngine {
    pub fn load(
        model_directory: &Path,
        intra_threads: usize,
    ) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        match Self::load_once(model_directory, intra_threads) {
            // Core ML compiles on first use; a prediction at startup moves
            // that cost out of the first request and proves the cache works.
            Ok(mut engine) if engine.coreml => {
                if engine.warm_up().is_ok() {
                    return Ok(engine);
                }
            }
            Err(error) if error.to_string() == "coreml_session_failed" => {}
            // CPU models, and every other load error, are returned as they are.
            other => return other,
        }
        // A compiled model left by a worker killed while Core ML was
        // compiling, or by another runtime build, can fail at session
        // creation or at the first prediction. Rebuild it from the ONNX file
        // once; a second failure is reported, never hidden.
        coreml::clear_cache(&model_directory.join(COREML_CACHE_DIRECTORY));
        let mut engine = Self::load_once(model_directory, intra_threads)
            .map_err(|_| io::Error::other("coreml_session_failed"))?;
        engine
            .warm_up()
            .map_err(|_| io::Error::other("coreml_session_failed"))?;
        Ok(engine)
    }

    fn warm_up(&mut self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        self.detect(&[TextInput {
            id: 0,
            text: "AstrLink warm-up".into(),
        }])
        .map(|_| ())
    }

    fn load_once(
        model_directory: &Path,
        intra_threads: usize,
    ) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
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
        // Opt-in AstrLink Guard contracts. Absent or null fields keep the
        // legacy decoder and spans; they are defined only for generic HF
        // token-classification models.
        let offset_consistency = offset_contract::configured(&config).map_err(io::Error::other)?;
        let span_contract = span_contract::configured(&config).map_err(io::Error::other)?;
        if manifest.adapter != Adapter::HfTokenClassification {
            if offset_consistency {
                return Err(io::Error::other("invalid_decoder_contract_adapter").into());
            }
            if span_contract {
                return Err(io::Error::other("invalid_span_contract_adapter").into());
            }
        }
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
                Decoder::from_hf_json(&config, manifest.tag_scheme, &manifest.label_mapping)
                    .and_then(|decoder| {
                        if offset_consistency {
                            decoder.with_offset_consistency()
                        } else {
                            Ok(decoder)
                        }
                    }),
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

        let static_padding = match &manifest.execution {
            Some(execution) => {
                if tokenizer.id_to_token(execution.pad_token_id).is_none() {
                    return Err(io::Error::other("invalid_model_manifest").into());
                }
                Some(StaticPadding {
                    length: execution.static_sequence_length,
                    pad_token_id: i64::from(execution.pad_token_id),
                })
            }
            None => None,
        };

        let coreml = manifest
            .execution
            .as_ref()
            .is_some_and(|execution| execution.provider == ExecutionProvider::Coreml);
        if coreml {
            // Checked before the Core ML runtime is loaded, so it is never
            // loaded on a system the gate refuses.
            coreml::check_platform()?;
        }
        initialize_onnx_runtime(coreml)?;
        let model_path = manifest.resolve(model_directory, &manifest.model_path);
        let session = if coreml {
            coreml::commit_session(&manifest, model_directory, &model_path, intra_threads)?
        } else {
            priority::lower_pool_threads(session_builder(intra_threads)?)?
                .commit_from_file(model_path)?
        };
        validate_session(&session, &manifest)?;

        Ok(Self {
            tokenizer,
            apply_post_processor,
            decoder,
            sensitive,
            span_contract,
            session,
            model_window_tokens: manifest.window,
            content_window_tokens,
            overlap_tokens: manifest.stride,
            max_request_tokens: manifest.max_request_tokens,
            input_ids_name: manifest.input_names.input_ids,
            attention_mask_name: manifest.input_names.attention_mask,
            token_type_ids_name: manifest.input_names.token_type_ids,
            output_name: manifest.output_name,
            static_padding,
            coreml,
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
        let mut scores = StitchedWindowScores::new(ids.len(), label_count);
        scores.preserve_logits = self.decoder.uses_raw_logits();
        for (start, end) in
            chunk_ranges_with(ids.len(), self.content_window_tokens, self.overlap_tokens)
        {
            let mut window = prepare_model_window(
                &self.tokenizer,
                encoding,
                start,
                end,
                self.apply_post_processor,
            )?;
            let mut sequence_length = window.input_ids.len();
            if sequence_length > self.model_window_tokens {
                return Err(io::Error::other("invalid_model_window").into());
            }
            if let Some(padding) = self.static_padding {
                // content_positions all index the unpadded prefix, so the
                // padded tail never reaches the stitched scores.
                pad_model_window(&mut window, padding)?;
                sequence_length = padding.length;
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
            if self.span_contract {
                return Ok(span_contract::refine(text, model_spans).map_err(io::Error::other)?);
            }
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

// Without spinning, idle pool threads sleep between operators instead of
// holding their cores while the main thread tokenizes and decodes.
fn session_builder(intra_threads: usize) -> ort::Result<SessionBuilder> {
    Session::builder()?
        .with_log_level(LogLevel::Error)?
        .with_intra_threads(intra_threads)?
        .with_intra_op_spinning(false)?
        .with_inter_threads(INTER_OP_THREADS)?
        .with_parallel_execution(false)?
        .with_optimization_level(GraphOptimizationLevel::Level3)
}

fn pad_model_window(window: &mut ModelWindow, padding: StaticPadding) -> io::Result<()> {
    let length = window.input_ids.len();
    if length == 0
        || length > padding.length
        || window.attention_mask.len() != length
        || window.token_type_ids.len() != length
        || window
            .content_positions
            .last()
            .is_some_and(|position| *position >= length)
    {
        return Err(io::Error::other("invalid_model_window"));
    }
    window
        .input_ids
        .resize(padding.length, padding.pad_token_id);
    window.attention_mask.resize(padding.length, 0);
    window.token_type_ids.resize(padding.length, 0);
    Ok(())
}

#[cfg(target_os = "macos")]
mod coreml {
    use std::{
        ffi::CStr,
        fs,
        io::{self, ErrorKind},
        os::unix::fs::DirBuilderExt,
        path::{Path, PathBuf},
        ptr,
    };

    use ort::{
        ep::{self, ExecutionProvider},
        session::Session,
    };

    use super::{COREML_CACHE_DIRECTORY as CACHE_DIRECTORY, session_builder};
    use crate::{
        manifest::{CoreMlComputeUnits, ModelManifest},
        priority,
    };

    /// Core ML models run only on Apple silicon with macOS 26 or later.
    /// ONNX Runtime issue #32569 reports wrong FP16 results from the Core ML
    /// provider on macOS 15, and no release before 26 has been tested, so
    /// older systems are refused instead of trusted. Core applies the same
    /// gate before it offers the variants.
    const MINIMUM_MACOS_MAJOR: u32 = 26;
    /// Darwin 25 is the kernel of macOS 26.
    const MINIMUM_DARWIN_MAJOR: u32 = 25;

    pub fn check_platform() -> io::Result<()> {
        if !cfg!(target_arch = "aarch64") {
            return Err(io::Error::other("unsupported_execution_provider"));
        }
        let product = sysctl_string(c"kern.osproductversion");
        let darwin = sysctl_string(c"kern.osrelease");
        if supported_macos(product.as_deref(), darwin.as_deref()) {
            Ok(())
        } else {
            Err(io::Error::other("coreml_requires_macos_26"))
        }
    }

    /// The product version ("26.0.1") is checked first; the Darwin release
    /// ("25.0.0") covers a product version that is missing or reported in a
    /// compatibility form. Anything unreadable counts as too old.
    fn supported_macos(product: Option<&str>, darwin: Option<&str>) -> bool {
        major_version(product).is_some_and(|major| major >= MINIMUM_MACOS_MAJOR)
            || major_version(darwin).is_some_and(|major| major >= MINIMUM_DARWIN_MAJOR)
    }

    fn major_version(version: Option<&str>) -> Option<u32> {
        let major = version?.trim().split('.').next()?;
        if major.is_empty() || major.len() > 4 || !major.bytes().all(|byte| byte.is_ascii_digit()) {
            return None;
        }
        major.parse().ok()
    }

    fn sysctl_string(name: &CStr) -> Option<String> {
        let mut buffer = [0_u8; 64];
        let mut size: libc::size_t = buffer.len();
        // SAFETY: the name is NUL-terminated, the buffer is writable for
        // `size` bytes, and no new value is set.
        let result = unsafe {
            libc::sysctlbyname(
                name.as_ptr(),
                buffer.as_mut_ptr().cast(),
                &mut size,
                ptr::null_mut(),
                0,
            )
        };
        if result != 0 || size == 0 || size > buffer.len() {
            return None;
        }
        CStr::from_bytes_until_nul(&buffer[..size])
            .ok()?
            .to_str()
            .ok()
            .map(str::to_owned)
    }

    /// ONNX Runtime keys its Core ML cache by the model alone, so a cache
    /// written by another runtime build or with other options would be
    /// loaded as it is and fail at the first prediction. Each runtime build
    /// and option set therefore gets its own subdirectory of the cache, and
    /// the others are removed.
    pub fn commit_session(
        manifest: &ModelManifest,
        model_directory: &Path,
        model_path: &Path,
        intra_threads: usize,
    ) -> Result<Session, Box<dyn std::error::Error + Send + Sync>> {
        let execution = manifest
            .execution
            .as_ref()
            .ok_or_else(|| io::Error::other("invalid_model_manifest"))?;
        let units = execution
            .compute_units
            .ok_or_else(|| io::Error::other("invalid_model_manifest"))?;
        if manifest
            .files
            .iter()
            .any(|file| file.path.split('/').next() == Some(CACHE_DIRECTORY))
        {
            return Err(io::Error::other("invalid_model_manifest").into());
        }
        let key = cache_key(ort::info(), units, execution.static_sequence_length);
        let cache = prepare_cache(model_directory, &key)?;
        let cache_path = cache
            .to_str()
            .ok_or_else(|| io::Error::other("invalid_model_installation"))?;
        let provider = ep::CoreML::default()
            .with_model_format(ep::coreml::ModelFormat::MLProgram)
            .with_compute_units(compute_units(units))
            .with_static_input_shapes(true)
            .with_model_cache_dir(cache_path);
        if !provider
            .is_available()
            .map_err(|_| io::Error::other("coreml_unavailable"))?
        {
            return Err(io::Error::other("coreml_unavailable").into());
        }
        // Registration errors are fatal rather than silently leaving the
        // whole graph on the CPU. CPU comes second and takes only the nodes
        // Core ML does not claim.
        let builder = session_builder(intra_threads)
            .and_then(|builder| {
                builder.with_execution_providers([
                    provider.build().error_on_failure(),
                    ep::CPU::default().build().error_on_failure(),
                ])
            })
            .map_err(|_| io::Error::other("coreml_unavailable"))?;
        Ok(priority::lower_pool_threads(builder)
            .and_then(|builder| builder.commit_from_file(model_path))
            .map_err(|_| io::Error::other("coreml_session_failed"))?)
    }

    pub fn clear_cache(cache: &Path) {
        // Best effort: a cache that cannot be removed fails the rebuild.
        let _ = fs::remove_dir_all(cache);
    }

    fn compute_units(units: CoreMlComputeUnits) -> ep::coreml::ComputeUnits {
        match units {
            CoreMlComputeUnits::CpuAndGpu => ep::coreml::ComputeUnits::CPUAndGPU,
            CoreMlComputeUnits::CpuAndNeuralEngine => ep::coreml::ComputeUnits::CPUAndNeuralEngine,
            CoreMlComputeUnits::All => ep::coreml::ComputeUnits::All,
        }
    }

    /// FNV-1a over the runtime build string (it names the release branch
    /// and commit) and the options that shape the compiled model.
    pub fn cache_key(
        runtime_build: &str,
        units: CoreMlComputeUnits,
        static_sequence_length: usize,
    ) -> String {
        let units = match units {
            CoreMlComputeUnits::CpuAndGpu => "cpu_and_gpu",
            CoreMlComputeUnits::CpuAndNeuralEngine => "cpu_and_neural_engine",
            CoreMlComputeUnits::All => "all",
        };
        let mut hash = 0xcbf2_9ce4_8422_2325_u64;
        for byte in [
            runtime_build.as_bytes(),
            b"\0mlprogram\0static\0",
            units.as_bytes(),
            b"\0",
            static_sequence_length.to_string().as_bytes(),
        ]
        .concat()
        {
            hash ^= u64::from(byte);
            hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
        }
        format!("{hash:016x}")
    }

    fn prepare_cache(model_directory: &Path, key: &str) -> io::Result<PathBuf> {
        let root = model_directory.join(CACHE_DIRECTORY);
        create_private_directory(&root)?;
        if let Ok(entries) = fs::read_dir(&root) {
            for entry in entries.flatten() {
                if entry.file_name() != key {
                    let path = entry.path();
                    let _ = match entry.file_type() {
                        Ok(kind) if kind.is_dir() => fs::remove_dir_all(path),
                        _ => fs::remove_file(path),
                    };
                }
            }
        }
        let cache = root.join(key);
        create_private_directory(&cache)?;
        Ok(cache)
    }

    fn create_private_directory(path: &Path) -> io::Result<()> {
        match fs::symlink_metadata(path) {
            Ok(metadata) if metadata.file_type().is_symlink() || !metadata.is_dir() => {
                Err(io::Error::other("invalid_model_installation"))
            }
            Ok(_) => Ok(()),
            Err(error) if error.kind() == ErrorKind::NotFound => fs::DirBuilder::new()
                .mode(0o700)
                .create(path)
                .map_err(|_| io::Error::other("invalid_model_installation")),
            Err(_) => Err(io::Error::other("invalid_model_installation")),
        }
    }

    #[cfg(test)]
    mod tests {
        use super::*;

        #[test]
        fn core_ml_requires_macos_26() {
            for (product, darwin, expected) in [
                (Some("26.0"), Some("25.0.0"), true),
                (Some("26"), None, true),
                (Some("27.0.1\n"), Some("26.0.0"), true),
                (Some("15.7.1"), Some("24.6.0"), false),
                (Some("14.0"), Some("23.0.0"), false),
                // A compatibility product version on a macOS 26 kernel.
                (Some("16.0"), Some("25.1.0"), true),
                (None, Some("25.0.0"), true),
                (None, Some("24.6.0"), false),
                (Some(""), Some(""), false),
                (Some("x26"), Some("v25"), false),
                (Some("99999.0"), None, false),
                (None, None, false),
            ] {
                assert_eq!(
                    supported_macos(product, darwin),
                    expected,
                    "{product:?} / {darwin:?}"
                );
            }
        }

        #[test]
        fn reads_the_running_macos_version() {
            let product = sysctl_string(c"kern.osproductversion");
            let darwin = sysctl_string(c"kern.osrelease");
            assert!(major_version(product.as_deref()).is_some(), "{product:?}");
            assert!(major_version(darwin.as_deref()).is_some(), "{darwin:?}");
            assert_eq!(
                check_platform().is_ok(),
                cfg!(target_arch = "aarch64")
                    && supported_macos(product.as_deref(), darwin.as_deref())
            );
        }

        #[test]
        fn cache_key_changes_with_runtime_build_and_options() {
            let build = "ORT Build Info: git-branch=rel-1.24.4, git-commit-id=abc";
            let key = cache_key(build, CoreMlComputeUnits::CpuAndGpu, 512);
            assert_eq!(key.len(), 16);
            assert_eq!(key, cache_key(build, CoreMlComputeUnits::CpuAndGpu, 512));
            for other in [
                cache_key(
                    "ORT Build Info: git-branch=rel-1.31.0, git-commit-id=def",
                    CoreMlComputeUnits::CpuAndGpu,
                    512,
                ),
                cache_key(build, CoreMlComputeUnits::CpuAndNeuralEngine, 512),
                cache_key(build, CoreMlComputeUnits::All, 512),
                cache_key(build, CoreMlComputeUnits::CpuAndGpu, 256),
            ] {
                assert_ne!(key, other);
            }
        }

        #[test]
        fn cache_preparation_keeps_only_the_current_key() {
            let directory = std::env::temp_dir().join(format!(
                "astrlink-privacy-worker-coreml-cache-{}",
                std::process::id()
            ));
            let _ = fs::remove_dir_all(&directory);
            fs::create_dir_all(directory.join(CACHE_DIRECTORY).join("stale")).expect("stale");
            fs::write(directory.join(CACHE_DIRECTORY).join("loose"), b"x").expect("loose");
            let cache = prepare_cache(&directory, "0123456789abcdef").expect("prepare");
            let names = fs::read_dir(directory.join(CACHE_DIRECTORY))
                .expect("list cache")
                .map(|entry| entry.expect("entry").file_name())
                .collect::<Vec<_>>();
            assert_eq!(names, ["0123456789abcdef"]);
            assert!(cache.is_dir());
            let _ = fs::remove_dir_all(&directory);

            fs::create_dir_all(&directory).expect("fixture");
            fs::write(directory.join(CACHE_DIRECTORY), b"not a directory").expect("file");
            assert_eq!(
                prepare_cache(&directory, "0123456789abcdef")
                    .expect_err("a file in place of the cache is refused")
                    .to_string(),
                "invalid_model_installation"
            );
            let _ = fs::remove_dir_all(&directory);
        }
    }
}

#[cfg(not(target_os = "macos"))]
mod coreml {
    use std::{io, path::Path};

    use ort::session::Session;

    use crate::manifest::ModelManifest;

    pub fn check_platform() -> io::Result<()> {
        Err(io::Error::other("unsupported_execution_provider"))
    }

    pub fn commit_session(
        _manifest: &ModelManifest,
        _model_directory: &Path,
        _model_path: &Path,
        _intra_threads: usize,
    ) -> Result<Session, Box<dyn std::error::Error + Send + Sync>> {
        Err(io::Error::other("unsupported_execution_provider").into())
    }

    pub fn clear_cache(_cache: &Path) {}
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
    if let Some(execution) = &manifest.execution {
        // Static padding is only correct for a graph that declares exactly
        // that shape; a dynamic graph would also keep Core ML off every node.
        // The logits are read as float32; FP16 exports that also emit
        // float16 logits are refused here, before the first prediction.
        let expected = [1, execution.static_sequence_length as i64];
        if session.inputs().iter().any(|input| {
            input.dtype().tensor_type() != Some(TensorElementType::Int64)
                || input.dtype().tensor_shape().map(|shape| &shape[..]) != Some(&expected[..])
        }) || session.outputs().iter().any(|output| {
            output.name() == manifest.output_name
                && output.dtype().tensor_type() != Some(TensorElementType::Float32)
        }) {
            return Err(io::Error::other("invalid_model_io"));
        }
    }
    Ok(())
}

// CPU models keep the runtime they have always used, so their output stays
// byte-identical. Core ML needs 1.24.1 or later: before it the Core ML
// provider refused every FP16 node on macOS and ran them on the CPU.
#[cfg(target_os = "macos")]
const MACOS_RUNTIME_NAME: &str = "libonnxruntime.1.23.2.dylib";
#[cfg(target_os = "macos")]
const MACOS_COREML_RUNTIME_NAME: &str = "libonnxruntime.1.24.4.dylib";

#[cfg(target_os = "macos")]
fn macos_runtime_name(coreml: bool) -> &'static str {
    if coreml {
        MACOS_COREML_RUNTIME_NAME
    } else {
        MACOS_RUNTIME_NAME
    }
}

fn initialize_onnx_runtime(
    #[cfg_attr(not(target_os = "macos"), allow(unused_variables))] coreml: bool,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    #[cfg(target_os = "macos")]
    {
        let executable = env::current_exe()?;
        let runtime = runtime_library_candidates(&executable, macos_runtime_name(coreml))
            .into_iter()
            .find(|candidate| candidate.is_file())
            .ok_or_else(|| {
                if coreml {
                    io::Error::new(io::ErrorKind::NotFound, "coreml_unavailable")
                } else {
                    io::Error::new(io::ErrorKind::NotFound, "onnx_runtime_not_installed")
                }
            })?;
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
    // ort keeps only a weak reference. Retain the environment between sessions
    // so a new default logger cannot race the previous environment's teardown.
    static ENVIRONMENT: OnceLock<Arc<Environment>> = OnceLock::new();
    if ENVIRONMENT.get().is_none() {
        let environment = ort::environment::get_environment()?;
        let _ = ENVIRONMENT.set(environment);
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

/// Stitches overlapping windows by keeping, for each token, the row from the
/// window where it sits farthest from a cut edge. A window that starts or ends
/// inside the text cannot see past that edge; averaging its row in let a value
/// cut at a window start lose its cue words and fall out of the span.
struct StitchedWindowScores {
    scores: Vec<f64>,
    // Distance to the nearest cut edge of the window each row came from.
    context: Vec<Option<usize>>,
    label_count: usize,
    preserve_logits: bool,
}

impl StitchedWindowScores {
    fn new(token_count: usize, label_count: usize) -> Self {
        Self {
            scores: vec![0.0; token_count * label_count],
            context: vec![None; token_count],
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
        if end > self.context.len() {
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
            // The text's own start and end hide no context.
            let before = if start == 0 { usize::MAX } else { local_token };
            let after = if end == self.context.len() {
                usize::MAX
            } else {
                token_count - 1 - local_token
            };
            let context = before.min(after);
            // Ties keep the earlier window.
            if self.context[global_token].is_some_and(|kept| kept >= context) {
                continue;
            }
            let destination = &mut self.scores
                [global_token * self.label_count..(global_token + 1) * self.label_count];
            for (score, value) in destination.iter_mut().zip(row) {
                *score = f64::from(*value) - normalizer;
            }
            self.context[global_token] = Some(context);
        }
        Ok(())
    }

    fn finish(self) -> Result<Vec<f32>, &'static str> {
        if self.context.contains(&None) {
            return Err("incomplete_logits_coverage");
        }
        Ok(self.scores.into_iter().map(|value| value as f32).collect())
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
    const TEST_INTRA_THREADS: usize = 2;

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

    // The micro fixture above with both inputs and the output fixed to a
    // sequence of 4, like an FP16 export for Core ML.
    const STATIC_MICRO_ONNX_MODEL: &[u8] = &[
        8, 9, 18, 21, 97, 115, 116, 114, 108, 105, 110, 107, 45, 116, 101, 115, 116, 45, 102, 105,
        120, 116, 117, 114, 101, 58, 162, 4, 10, 31, 10, 9, 105, 110, 112, 117, 116, 95, 105, 100,
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
        95, 112, 114, 105, 118, 97, 99, 121, 95, 102, 105, 108, 116, 101, 114, 90, 27, 10, 9, 105,
        110, 112, 117, 116, 95, 105, 100, 115, 18, 14, 10, 12, 8, 7, 18, 8, 10, 2, 8, 1, 10, 2, 8,
        4, 90, 32, 10, 14, 97, 116, 116, 101, 110, 116, 105, 111, 110, 95, 109, 97, 115, 107, 18,
        14, 10, 12, 8, 7, 18, 8, 10, 2, 8, 1, 10, 2, 8, 4, 98, 28, 10, 6, 108, 111, 103, 105, 116,
        115, 18, 18, 10, 16, 8, 1, 18, 12, 10, 2, 8, 1, 10, 2, 8, 4, 10, 2, 8, 33, 66, 4, 10, 0,
        16, 13,
    ];

    // The micro fixture above with the 21 Guard BIO labels: every token
    // prefers I-ip_address, then B-ip_address.
    const GUARD_CONTRACT_ONNX_MODEL: &[u8] = &[
        8, 9, 18, 21, 97, 115, 116, 114, 108, 105, 110, 107, 45, 116, 101, 115, 116, 45, 102, 105,
        120, 116, 117, 114, 101, 58, 135, 4, 10, 31, 10, 9, 105, 110, 112, 117, 116, 95, 105, 100,
        115, 18, 11, 105, 110, 112, 117, 116, 95, 115, 104, 97, 112, 101, 34, 5, 83, 104, 97, 112,
        101, 10, 63, 18, 17, 108, 97, 98, 101, 108, 95, 119, 105, 100, 116, 104, 95, 118, 97, 108,
        117, 101, 34, 8, 67, 111, 110, 115, 116, 97, 110, 116, 42, 32, 10, 5, 118, 97, 108, 117,
        101, 42, 20, 8, 1, 16, 7, 58, 1, 21, 66, 11, 108, 97, 98, 101, 108, 95, 119, 105, 100, 116,
        104, 160, 1, 4, 10, 67, 10, 11, 105, 110, 112, 117, 116, 95, 115, 104, 97, 112, 101, 10,
        17, 108, 97, 98, 101, 108, 95, 119, 105, 100, 116, 104, 95, 118, 97, 108, 117, 101, 18, 12,
        111, 117, 116, 112, 117, 116, 95, 115, 104, 97, 112, 101, 34, 6, 67, 111, 110, 99, 97, 116,
        42, 11, 10, 4, 97, 120, 105, 115, 24, 0, 160, 1, 2, 10, 150, 1, 18, 17, 98, 97, 115, 101,
        95, 108, 111, 103, 105, 116, 115, 95, 118, 97, 108, 117, 101, 34, 8, 67, 111, 110, 115,
        116, 97, 110, 116, 42, 119, 10, 5, 118, 97, 108, 117, 101, 42, 107, 8, 1, 8, 1, 8, 21, 16,
        1, 34, 84, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0,
        32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 160, 64, 0, 0, 32, 65, 0, 0,
        32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0,
        32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 0, 0, 32, 193, 66, 11, 98, 97, 115, 101, 95, 108,
        111, 103, 105, 116, 115, 160, 1, 4, 10, 49, 10, 17, 98, 97, 115, 101, 95, 108, 111, 103,
        105, 116, 115, 95, 118, 97, 108, 117, 101, 10, 12, 111, 117, 116, 112, 117, 116, 95, 115,
        104, 97, 112, 101, 18, 6, 108, 111, 103, 105, 116, 115, 34, 6, 69, 120, 112, 97, 110, 100,
        18, 29, 97, 115, 116, 114, 108, 105, 110, 107, 95, 109, 105, 99, 114, 111, 95, 112, 114,
        105, 118, 97, 99, 121, 95, 102, 105, 108, 116, 101, 114, 90, 35, 10, 9, 105, 110, 112, 117,
        116, 95, 105, 100, 115, 18, 22, 10, 20, 8, 7, 18, 16, 10, 2, 8, 1, 10, 10, 18, 8, 115, 101,
        113, 117, 101, 110, 99, 101, 90, 40, 10, 14, 97, 116, 116, 101, 110, 116, 105, 111, 110,
        95, 109, 97, 115, 107, 18, 22, 10, 20, 8, 7, 18, 16, 10, 2, 8, 1, 10, 10, 18, 8, 115, 101,
        113, 117, 101, 110, 99, 101, 98, 36, 10, 6, 108, 111, 103, 105, 116, 115, 18, 26, 10, 24,
        8, 1, 18, 20, 10, 2, 8, 1, 10, 10, 18, 8, 115, 101, 113, 117, 101, 110, 99, 101, 10, 2, 8,
        21, 66, 4, 10, 0, 16, 13,
    ];
    const GUARD_KINDS: [&str; 10] = [
        "email",
        "phone",
        "account",
        "payment_card",
        "ip_address",
        "url",
        "common_secret",
        "private_address",
        "private_date",
        "private_person",
    ];

    #[test]
    fn preserves_raw_logits_for_pplx_confidence_across_windows() {
        let mut scores = StitchedWindowScores::new(4, 2);
        scores.preserve_logits = true;
        scores
            .add_window(0, &[1.0, 3.0, 4.0, 6.0, 5.0, 7.0])
            .expect("first");
        scores
            .add_window(1, &[2.0, 4.0, 8.0, 9.0, 7.0, 9.0])
            .expect("second");
        assert_eq!(
            scores.finish().expect("scores"),
            vec![1.0, 3.0, 4.0, 6.0, 8.0, 9.0, 7.0, 9.0]
        );
    }

    #[test]
    fn keeps_each_token_from_the_window_farthest_from_a_cut_edge() {
        let mut scores = StitchedWindowScores::new(6, TEST_LABEL_COUNT);
        let first = rows(&[(0, 0.0), (0, 0.0), (1, 2.0), (1, 3.0)]);
        let second = rows(&[(2, 4.0), (2, 5.0), (0, 0.0), (0, 0.0)]);
        assert_eq!(chunk_ranges_with(6, 4, 2), vec![(0, 4), (2, 6)]);
        scores.add_window(0, &first).expect("first window");
        scores.add_window(2, &second).expect("second window");

        let stitched = scores.finish().expect("complete coverage");
        let assert_row = |token: usize, expected: Vec<f32>| {
            let row = &stitched[token * TEST_LABEL_COUNT..(token + 1) * TEST_LABEL_COUNT];
            for (actual, expected) in row.iter().zip(expected) {
                assert!((actual - expected).abs() < 1e-6);
            }
        };
        // Token 2 is one token from the first window's cut end but at the
        // second window's cut start; token 3 is the other way round.
        assert_row(
            2,
            log_softmax(&first[2 * TEST_LABEL_COUNT..3 * TEST_LABEL_COUNT]),
        );
        assert_row(
            3,
            log_softmax(&second[TEST_LABEL_COUNT..2 * TEST_LABEL_COUNT]),
        );
    }

    #[test]
    fn value_cut_at_a_window_start_keeps_its_full_span() {
        let config = serde_json::to_vec(&serde_json::json!({
            "id2label": { "0": "O", "1": "B-account", "2": "I-account" }
        }))
        .expect("config");
        let mapping =
            std::collections::BTreeMap::from([("account".into(), Some("account".into()))]);
        let decoder = Decoder::from_hf_json(&config, crate::manifest::TagScheme::Bio, &mapping)
            .expect("decoder");
        let label_count = 3;
        let token_count = 8;
        let offsets = (0..token_count)
            .map(|token| (token, token + 1))
            .collect::<Vec<_>>();
        let ranges = chunk_ranges_with(token_count, 6, 4);
        assert_eq!(ranges, vec![(0, 6), (2, 8)]);
        let mut scores = StitchedWindowScores::new(token_count, label_count);
        for (start, end) in ranges {
            let mut logits = vec![-20.0; (end - start) * label_count];
            for global_token in start..end {
                let row = &mut logits[(global_token - start) * label_count
                    ..(global_token - start + 1) * label_count];
                // The first window sees the cue before the value at tokens
                // 1..=3. The second starts inside the value and reads it as
                // background, as a window without the cue words would.
                match (start, global_token) {
                    (0, 1) => row[1] = 2.0,
                    (0, 2 | 3) => row[2] = 2.0,
                    _ => row[0] = 5.0,
                }
            }
            scores.add_window(start, &logits).expect("add window");
        }

        let scores = scores.finish().expect("complete coverage");
        let spans = decoder.decode(7, &scores, &offsets).expect("decode");
        assert_eq!(spans.len(), 1);
        assert_eq!(spans[0].label, "account");
        assert_eq!((spans[0].start, spans[0].end), (1, 4));
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
        let mut scores = StitchedWindowScores::new(token_count, TEST_LABEL_COUNT);

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
            scores.add_window(start, &logits).expect("aggregate window");
        }

        let scores = scores.finish().expect("complete coverage");
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
    fn macos_core_ml_models_load_their_own_runtime() {
        assert_eq!(macos_runtime_name(false), "libonnxruntime.1.23.2.dylib");
        assert_eq!(macos_runtime_name(true), "libonnxruntime.1.24.4.dylib");
    }

    #[test]
    fn onnx_environment_outlives_concurrent_sessions() {
        const CHILD_ENV: &str = "ASTRLINK_TEST_ORT_LIFETIME_CHILD";
        if env::var_os(CHILD_ENV).is_none() {
            // Other tests can keep ORT alive, hiding the last-session race.
            let output = std::process::Command::new(env::current_exe().expect("test executable"))
                .args([
                    "--exact",
                    "engine::tests::onnx_environment_outlives_concurrent_sessions",
                    "--nocapture",
                ])
                .env(CHILD_ENV, "1")
                .output()
                .expect("run isolated ORT lifetime test");
            assert!(
                output.status.success(),
                "isolated ORT lifetime test failed:\n{}\n{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
            return;
        }

        initialize_onnx_runtime(false).expect("initialize runtime");
        let environment = ort::environment::get_environment().expect("get environment");
        let weak = std::sync::Arc::downgrade(&environment);
        drop(environment);
        assert!(
            weak.upgrade().is_some(),
            "environment dropped before sessions"
        );

        let start = std::sync::Barrier::new(8);
        std::thread::scope(|scope| {
            for _ in 0..8 {
                scope.spawn(|| {
                    start.wait();
                    for _ in 0..256 {
                        initialize_onnx_runtime(false).expect("reuse runtime");
                        drop(Session::builder().expect("create concurrent session builder"));
                    }
                });
            }
        });
        assert!(
            weak.upgrade().is_some(),
            "environment dropped after sessions"
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
            let mut engine = PrivacyEngine::load(&directory, TEST_INTRA_THREADS)?;
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
            let mut engine = PrivacyEngine::load(&directory, TEST_INTRA_THREADS)?;
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
            let mut engine = PrivacyEngine::load(&directory, TEST_INTRA_THREADS)?;
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

    #[test]
    fn static_padding_fills_the_tail_and_keeps_content_positions() {
        let padding = StaticPadding {
            length: 6,
            pad_token_id: 1,
        };
        let mut window = ModelWindow {
            input_ids: vec![0, 7, 8, 2],
            attention_mask: vec![1, 1, 1, 1],
            token_type_ids: vec![0, 0, 0, 0],
            content_positions: vec![1, 2],
        };
        pad_model_window(&mut window, padding).expect("pad window");
        assert_eq!(window.input_ids, [0, 7, 8, 2, 1, 1]);
        assert_eq!(window.attention_mask, [1, 1, 1, 1, 0, 0]);
        assert_eq!(window.token_type_ids, [0, 0, 0, 0, 0, 0]);
        assert_eq!(window.content_positions, [1, 2]);

        // A full window is left as it is.
        pad_model_window(&mut window, padding).expect("pad full window");
        assert_eq!(window.input_ids.len(), 6);

        for mut invalid in [
            ModelWindow {
                input_ids: vec![0; 7],
                attention_mask: vec![1; 7],
                token_type_ids: vec![0; 7],
                content_positions: vec![1],
            },
            ModelWindow {
                input_ids: vec![0, 1],
                attention_mask: vec![1],
                token_type_ids: vec![0, 0],
                content_positions: vec![0],
            },
            ModelWindow {
                input_ids: vec![0, 1],
                attention_mask: vec![1, 1],
                token_type_ids: vec![0, 0],
                content_positions: vec![2],
            },
            ModelWindow {
                input_ids: Vec::new(),
                attention_mask: Vec::new(),
                token_type_ids: Vec::new(),
                content_positions: Vec::new(),
            },
        ] {
            assert_eq!(
                pad_model_window(&mut invalid, padding)
                    .expect_err("invalid window must be rejected")
                    .to_string(),
                "invalid_model_window"
            );
        }
    }

    #[test]
    fn static_execution_pads_windows_and_matches_the_dynamic_model() {
        let text = "你好 secret 你好 secret 你好";
        let dynamic = static_fixture_directory(MICRO_ONNX_MODEL, None);
        let expected = (|| {
            PrivacyEngine::load(&dynamic, TEST_INTRA_THREADS)?.detect(&[TextInput {
                id: 5,
                text: text.into(),
            }])
        })();
        let _ = fs::remove_dir_all(&dynamic);
        let expected = expected.expect("run dynamic fixture");

        let padded = static_fixture_directory(
            STATIC_MICRO_ONNX_MODEL,
            Some(serde_json::json!({
                "provider": "cpu",
                "model_format": null,
                "compute_units": null,
                "static_sequence_length": 4,
                "pad_token_id": 0,
            })),
        );
        let actual = (|| {
            let mut engine = PrivacyEngine::load(&padded, TEST_INTRA_THREADS)?;
            assert_eq!(
                engine.static_padding,
                Some(StaticPadding {
                    length: 4,
                    pad_token_id: 0
                })
            );
            engine.detect(&[TextInput {
                id: 5,
                text: text.into(),
            }])
        })();
        let _ = fs::remove_dir_all(&padded);
        let actual = actual.expect("run static fixture");
        assert!(!expected.is_empty());
        assert_eq!(
            serde_json::to_value(&actual).expect("serialize static spans"),
            serde_json::to_value(&expected).expect("serialize dynamic spans")
        );
    }

    #[test]
    fn static_execution_rejects_a_dynamic_graph_and_an_unknown_pad_token() {
        let cpu = serde_json::json!({
            "provider": "cpu",
            "model_format": null,
            "compute_units": null,
            "static_sequence_length": 4,
            "pad_token_id": 0,
        });
        let directory = static_fixture_directory(MICRO_ONNX_MODEL, Some(cpu.clone()));
        assert_eq!(load_error(&directory), "invalid_model_io");

        let mut unknown_pad = cpu;
        unknown_pad["pad_token_id"] = 4096.into();
        let directory = static_fixture_directory(STATIC_MICRO_ONNX_MODEL, Some(unknown_pad));
        assert_eq!(load_error(&directory), "invalid_model_manifest");
    }

    #[cfg(not(target_os = "macos"))]
    #[test]
    fn coreml_execution_is_refused_off_macos() {
        let directory = static_fixture_directory(
            STATIC_MICRO_ONNX_MODEL,
            Some(serde_json::json!({
                "provider": "coreml",
                "model_format": "mlprogram",
                "compute_units": "cpu_and_gpu",
                "static_sequence_length": 4,
                "pad_token_id": 0,
            })),
        );
        assert_eq!(load_error(&directory), "unsupported_execution_provider");
    }

    /// The generic HF fixture with 4-token windows, the given model and an
    /// optional execution block.
    fn static_fixture_directory(model: &[u8], execution: Option<serde_json::Value>) -> PathBuf {
        let directory = micro_model_directory();
        fs::write(directory.join("onnx/model_q4.onnx"), model).expect("write fixture model");
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
        for file in manifest["files"]
            .as_array_mut()
            .expect("fixture file descriptors")
        {
            if file["path"] == "onnx/model_q4.onnx" {
                file["size"] = model.len().into();
            }
        }
        if let Some(execution) = execution {
            manifest["execution"] = execution;
        }
        fs::write(
            &manifest_path,
            serde_json::to_vec(&manifest).expect("serialize fixture manifest"),
        )
        .expect("write fixture manifest");
        directory
    }

    #[test]
    fn guard_contracts_are_opt_in_and_refine_model_spans() {
        let text = "203.0.113.9:8080";
        let detect = |fields: serde_json::Value| {
            let directory = guard_contract_model_directory();
            edit_fixture(&directory, |config, _| {
                for (key, value) in fields.as_object().expect("contract fields") {
                    config[key] = value.clone();
                }
            });
            let result = (|| {
                PrivacyEngine::load(&directory, TEST_INTRA_THREADS)?.detect(&[TextInput {
                    id: 29,
                    text: text.into(),
                }])
            })();
            let _ = fs::remove_dir_all(&directory);
            result.expect("run Guard contract fixture")
        };
        let decoder_field = offset_contract::CONFIG_FIELD;
        let span_field = span_contract::CONFIG_FIELD;

        let legacy = detect(serde_json::json!({}));
        assert_eq!(
            legacy
                .iter()
                .map(|span| (span.text_id, span.label.as_str(), span.start, span.end))
                .collect::<Vec<_>>(),
            vec![(29, "ip_address", 0, text.len())]
        );
        assert_eq!(
            detect(serde_json::json!({ decoder_field: null, span_field: null })),
            legacy
        );
        // No token shares a character here, so the decoder contract agrees.
        assert_eq!(
            detect(serde_json::json!({ decoder_field: offset_contract::CONTRACT })),
            legacy
        );

        let refined = detect(serde_json::json!({ span_field: span_contract::CONTRACT }));
        assert_eq!(
            refined
                .iter()
                .map(|span| (span.text_id, span.label.as_str(), span.start, span.end))
                .collect::<Vec<_>>(),
            vec![(29, "ip_address", 0, "203.0.113.9".len())]
        );
        assert_eq!(refined[0].score, legacy[0].score);
        assert_eq!(
            detect(serde_json::json!({
                decoder_field: offset_contract::CONTRACT,
                span_field: span_contract::CONTRACT,
            })),
            refined
        );
    }

    #[test]
    fn guard_contract_values_are_strict_at_load() {
        for (field, value, error) in [
            (
                offset_contract::CONFIG_FIELD,
                serde_json::json!("bio-offset-consistency-v2"),
                "invalid_decoder_contract",
            ),
            (
                offset_contract::CONFIG_FIELD,
                serde_json::json!(true),
                "invalid_decoder_contract",
            ),
            (
                span_contract::CONFIG_FIELD,
                serde_json::json!("existing-ip-and-dynamic-url-v0"),
                "invalid_span_contract",
            ),
            (
                span_contract::CONFIG_FIELD,
                serde_json::json!(["existing-ip-and-dynamic-url-v1"]),
                "invalid_span_contract",
            ),
        ] {
            let directory = guard_contract_model_directory();
            edit_fixture(&directory, |config, _| config[field] = value.clone());
            assert_eq!(load_error(&directory), error, "{field}={value}");
        }
    }

    #[test]
    fn guard_contracts_require_the_hf_adapter_and_guard_labels() {
        for (field, value, error) in [
            (
                offset_contract::CONFIG_FIELD,
                offset_contract::CONTRACT,
                "invalid_decoder_contract_adapter",
            ),
            (
                span_contract::CONFIG_FIELD,
                span_contract::CONTRACT,
                "invalid_span_contract_adapter",
            ),
        ] {
            // The micro fixture uses the OpenAI BIOES adapter.
            let directory = micro_model_directory();
            edit_fixture(&directory, |config, _| config[field] = value.into());
            assert_eq!(load_error(&directory), error, "{field}");
        }

        // A generic HF model without the 21 Guard BIO labels cannot use the
        // decoder contract.
        let directory = micro_model_directory();
        edit_fixture(&directory, |config, manifest| {
            config[offset_contract::CONFIG_FIELD] = offset_contract::CONTRACT.into();
            manifest["adapter"] = "hf_token_classification".into();
            manifest["calibration_path"] = serde_json::Value::Null;
        });
        assert_eq!(load_error(&directory), "invalid_decoder_contract");
    }

    /// Rewrites the fixture `config.json` and manifest, keeping the manifest
    /// file sizes of the config and model valid.
    fn edit_fixture(
        directory: &Path,
        edit: impl FnOnce(&mut serde_json::Value, &mut serde_json::Value),
    ) {
        let manifest_path = directory.join(crate::manifest::MANIFEST_NAME);
        let config_path = directory.join("config.json");
        let mut manifest: serde_json::Value =
            serde_json::from_slice(&fs::read(&manifest_path).expect("read fixture manifest"))
                .expect("parse fixture manifest");
        let mut config: serde_json::Value =
            serde_json::from_slice(&fs::read(&config_path).expect("read fixture config"))
                .expect("parse fixture config");
        edit(&mut config, &mut manifest);
        let config = serde_json::to_vec(&config).expect("serialize fixture config");
        fs::write(&config_path, &config).expect("write fixture config");
        let model_size = fs::metadata(directory.join("onnx/model_q4.onnx"))
            .expect("fixture model metadata")
            .len();
        for file in manifest["files"]
            .as_array_mut()
            .expect("fixture file descriptors")
        {
            if file["path"] == "config.json" {
                file["size"] = config.len().into();
            } else if file["path"] == "onnx/model_q4.onnx" {
                file["size"] = model_size.into();
            }
        }
        fs::write(
            &manifest_path,
            serde_json::to_vec(&manifest).expect("serialize fixture manifest"),
        )
        .expect("write fixture manifest");
    }

    fn guard_contract_model_directory() -> PathBuf {
        let directory = micro_model_directory();
        fs::write(
            directory.join("onnx/model_q4.onnx"),
            GUARD_CONTRACT_ONNX_MODEL,
        )
        .expect("write Guard contract model");
        edit_fixture(&directory, |config, manifest| {
            let mut labels = serde_json::Map::new();
            labels.insert("0".into(), "O".into());
            for (index, kind) in GUARD_KINDS.iter().enumerate() {
                labels.insert((1 + index * 2).to_string(), format!("B-{kind}").into());
                labels.insert((2 + index * 2).to_string(), format!("I-{kind}").into());
            }
            *config = serde_json::json!({ "id2label": labels });
            manifest["adapter"] = "hf_token_classification".into();
            manifest["tag_scheme"] = "bio".into();
            manifest["calibration_path"] = serde_json::Value::Null;
            manifest["label_mapping"] = GUARD_KINDS
                .iter()
                .map(|kind| ((*kind).to_owned(), serde_json::Value::from(*kind)))
                .collect::<serde_json::Map<_, _>>()
                .into();
        });
        directory
    }

    /// Loads a fixture that must fail and removes it.
    fn load_error(directory: &Path) -> String {
        let error = PrivacyEngine::load(directory, TEST_INTRA_THREADS)
            .err()
            .map(|error| error.to_string());
        let _ = fs::remove_dir_all(directory);
        error.expect("fixture must fail to load")
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
