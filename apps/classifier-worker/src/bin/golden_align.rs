//! Numeric alignment gate for the experimental P1 INT8 bundle.
//!
//! CI forbids production weights. Run with `make classifier-ort-gate`.

use std::{env, fs, io::Read, path::PathBuf, process::ExitCode};

use astrlink_classifier_worker::{
    engine::{ClassifierEngine, encode_head_tail, pad_to_multiple},
    manifest::{PAD_MULTIPLE, PAD_TOKEN_ID},
    normalize::normalize_current_user_text,
};
use serde::Deserialize;
use sha2::{Digest, Sha256};

#[derive(Deserialize)]
struct GoldenFile {
    model_onnx_sha256: String,
    absolute_tolerance: f32,
    samples: Vec<GoldenSample>,
}

#[derive(Deserialize)]
struct GoldenSample {
    text: String,
    input_ids: Vec<i64>,
    attention_mask: Vec<i64>,
    logits: Vec<f32>,
}

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("classifier-ort-gate: {error}");
            ExitCode::FAILURE
        }
    }
}

fn run() -> Result<(), String> {
    if env::var_os("ASTRLINK_CI_SYNTHETIC_MODELS_ONLY").is_some() {
        return Err(
            "refusing to load a production bundle under ASTRLINK_CI_SYNTHETIC_MODELS_ONLY".into(),
        );
    }
    let bundle = env::var("ASTRLINK_CLASSIFIER_GOLDEN_BUNDLE")
        .map(PathBuf::from)
        .map_err(|_| {
            "ASTRLINK_CLASSIFIER_GOLDEN_BUNDLE must point to the local golden bundle".to_owned()
        })?;
    if !bundle.join("model.onnx").is_file() {
        return Err(format!("bundle missing model.onnx: {}", bundle.display()));
    }

    let golden_path = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("testdata")
        .join("golden-p1-int8-ort-1.23.2.json");
    let golden: GoldenFile =
        serde_json::from_slice(&fs::read(&golden_path).map_err(|error| error.to_string())?)
            .map_err(|error| error.to_string())?;

    let model_sha = sha256_file(&bundle.join("model.onnx"))?;
    if model_sha != golden.model_onnx_sha256 {
        return Err(format!(
            "model.onnx sha256 {model_sha} does not match golden {}",
            golden.model_onnx_sha256
        ));
    }

    let mut engine = ClassifierEngine::load_alignment_bundle(&bundle)
        .map_err(|error| format!("load engine: {error}"))?;

    let mut failures = 0_usize;
    for (index, sample) in golden.samples.iter().enumerate() {
        let normalized = normalize_current_user_text(&sample.text)
            .map_err(|error| format!("sample {index} normalize: {:?}", error))?;
        let ids = encode_head_tail(engine.tokenizer(), &normalized)
            .map_err(|error| format!("sample {index} encode: {}", error.code()))?;
        let (padded, mask) = pad_to_multiple(&ids, PAD_TOKEN_ID, PAD_MULTIPLE);
        if padded != sample.input_ids || mask != sample.attention_mask {
            eprintln!(
                "sample {index} token mismatch\n  got ids  {:?}\n  want ids {:?}\n  got mask {:?}\n  want mask {:?}",
                padded, sample.input_ids, mask, sample.attention_mask
            );
            failures += 1;
            continue;
        }
        let result = engine
            .classify_ids(&sample.input_ids, &sample.attention_mask)
            .map_err(|error| format!("sample {index} infer: {}", error.code()))?;
        let ok = result
            .logits
            .iter()
            .zip(&sample.logits)
            .all(|(got, want)| (got - want).abs() <= golden.absolute_tolerance);
        if !ok {
            eprintln!(
                "sample {index} logits mismatch\n  got  {:?}\n  want {:?}",
                result.logits, sample.logits
            );
            failures += 1;
        } else {
            println!(
                "sample {index} ok category={} logits={:?}",
                result.category, result.logits
            );
        }
    }
    if failures > 0 {
        return Err(format!("{failures} golden sample(s) failed"));
    }
    println!(
        "ORT 1.23.2 golden alignment passed ({} samples)",
        golden.samples.len()
    );
    Ok(())
}

fn sha256_file(path: &std::path::Path) -> Result<String, String> {
    let mut file = fs::File::open(path).map_err(|error| error.to_string())?;
    let mut hasher = Sha256::new();
    let mut buffer = [0_u8; 64 * 1024];
    loop {
        let read = file.read(&mut buffer).map_err(|error| error.to_string())?;
        if read == 0 {
            break;
        }
        hasher.update(&buffer[..read]);
    }
    Ok(format!("{:x}", hasher.finalize()))
}
