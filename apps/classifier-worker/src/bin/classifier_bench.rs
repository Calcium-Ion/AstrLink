//! Dev-only classifier harness. Never invoked from CI.
//!
//! Reads a local bundle and a JSONL holdout. If any frozen class has zero
//! samples, or the set is below the floor, this prints
//! 「评测集不足，不产出判定」and refuses to emit a pass/fail gate.

use std::{env, fs, path::PathBuf, process::ExitCode, time::Instant};

use astrlink_classifier_worker::{
    engine::ClassifierEngine,
    manifest::{LABEL_COUNT, expected_labels},
};
use serde::Deserialize;

const MIN_TOTAL: usize = 32;
const MIN_PER_CLASS: usize = 8;
const DEVELOP_MACRO_F1: f32 = 0.75;
const DEVELOP_PER_CLASS: f32 = 0.65;
const RELEASE_MACRO_F1: f32 = 0.85;
const RELEASE_RECALL: f32 = 0.90;
const RELEASE_P95_MS: f64 = 200.0;

#[derive(Deserialize)]
struct HoldoutRow {
    text: String,
    label: String,
}

fn main() -> ExitCode {
    match run() {
        Ok(code) => code,
        Err(error) => {
            eprintln!("classifier-bench: {error}");
            ExitCode::FAILURE
        }
    }
}

fn run() -> Result<ExitCode, String> {
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
        return Err(format!(
            "bundle missing model.onnx: {} (set ASTRLINK_CLASSIFIER_GOLDEN_BUNDLE)",
            bundle.display()
        ));
    }
    let holdout_path = env::var("ASTRLINK_CLASSIFIER_HOLDOUT")
        .map(PathBuf::from)
        .unwrap_or_else(|_| {
            PathBuf::from(env!("CARGO_MANIFEST_DIR"))
                .join("testdata")
                .join("holdout-insufficient.jsonl")
        });
    let rows = read_holdout(&holdout_path)?;
    let counts = class_counts(&rows);
    println!(
        "holdout {} rows from {}",
        rows.len(),
        holdout_path.display()
    );
    for (index, label) in expected_labels().into_iter().enumerate() {
        println!("  {label}: {}", counts[index]);
    }

    let insufficient = rows.len() < MIN_TOTAL || counts.iter().any(|count| *count < MIN_PER_CLASS);
    if insufficient {
        println!("评测集不足，不产出判定");
        println!(
            "need at least {MIN_TOTAL} rows and {MIN_PER_CLASS} per class; this snapshot cannot support a quality conclusion"
        );
    }

    let cold_started = Instant::now();
    let mut engine = ClassifierEngine::load_alignment_bundle(&bundle)
        .map_err(|error| format!("load engine: {error}"))?;
    let cold_ms = cold_started.elapsed().as_secs_f64() * 1000.0;
    let _ = engine
        .classify("warmup")
        .map_err(|error| format!("warmup: {error}"))?;
    let hot_started = Instant::now();
    let _ = engine
        .classify("warmup again")
        .map_err(|error| format!("hot warmup: {error}"))?;
    let hot_ms = hot_started.elapsed().as_secs_f64() * 1000.0;

    let mut confusion = [[0_u32; LABEL_COUNT]; LABEL_COUNT];
    let mut latencies = Vec::with_capacity(rows.len());
    for row in &rows {
        let Some(expected) = label_index(&row.label) else {
            return Err(format!("unknown holdout label {}", row.label));
        };
        let started = Instant::now();
        let result = engine
            .classify(&row.text)
            .map_err(|error| format!("classify: {error}"))?;
        latencies.push(started.elapsed().as_secs_f64() * 1000.0);
        let predicted = label_index(&result.category)
            .ok_or_else(|| format!("unknown prediction {}", result.category))?;
        confusion[expected][predicted] += 1;
    }

    latencies.sort_by(|left, right| left.partial_cmp(right).unwrap());
    let p50 = percentile(&latencies, 0.50);
    let p95 = percentile(&latencies, 0.95);
    println!("latency_ms p50={p50:.2} p95={p95:.2}");
    println!("startup_ms cold={cold_ms:.1} hot={hot_ms:.1}");
    if let Some(rss) = sampled_rss_bytes() {
        println!("sampled_rss_bytes={rss}");
    }

    if insufficient {
        return Ok(ExitCode::from(2));
    }

    let mut precisions = [0.0_f32; LABEL_COUNT];
    let mut recalls = [0.0_f32; LABEL_COUNT];
    let mut f1s = [0.0_f32; LABEL_COUNT];
    for class in 0..LABEL_COUNT {
        let tp = confusion[class][class] as f32;
        let predicted: f32 = (0..LABEL_COUNT)
            .map(|column| confusion[column][class] as f32)
            .sum();
        let actual: f32 = (0..LABEL_COUNT)
            .map(|column| confusion[class][column] as f32)
            .sum();
        precisions[class] = if predicted == 0.0 {
            0.0
        } else {
            tp / predicted
        };
        recalls[class] = if actual == 0.0 { 0.0 } else { tp / actual };
        f1s[class] = if precisions[class] + recalls[class] == 0.0 {
            0.0
        } else {
            2.0 * precisions[class] * recalls[class] / (precisions[class] + recalls[class])
        };
        println!(
            "class {} precision={:.3} recall={:.3} f1={:.3}",
            expected_labels()[class],
            precisions[class],
            recalls[class],
            f1s[class]
        );
    }
    let macro_f1 = f1s.iter().sum::<f32>() / LABEL_COUNT as f32;
    println!("macro_f1={macro_f1:.3}");

    let develop_ok = macro_f1 >= DEVELOP_MACRO_F1
        && precisions.iter().all(|value| *value >= DEVELOP_PER_CLASS)
        && recalls.iter().all(|value| *value >= DEVELOP_PER_CLASS);
    let release_ok = macro_f1 >= RELEASE_MACRO_F1
        && recalls.iter().all(|value| *value >= RELEASE_RECALL)
        && p95 <= RELEASE_P95_MS;
    println!(
        "develop_gate={} (macro-F1>={DEVELOP_MACRO_F1}, per-class P/R>={DEVELOP_PER_CLASS})",
        if develop_ok { "pass" } else { "fail" }
    );
    println!(
        "release_gate={} (macro-F1>={RELEASE_MACRO_F1}, all-class recall>={RELEASE_RECALL}, hot P95<={RELEASE_P95_MS}ms)",
        if release_ok { "pass" } else { "fail" }
    );
    if release_ok {
        Ok(ExitCode::SUCCESS)
    } else {
        Ok(ExitCode::from(3))
    }
}

fn read_holdout(path: &PathBuf) -> Result<Vec<HoldoutRow>, String> {
    let document = fs::read_to_string(path).map_err(|error| error.to_string())?;
    let mut rows = Vec::new();
    for (index, line) in document.lines().enumerate() {
        if line.trim().is_empty() {
            continue;
        }
        rows.push(
            serde_json::from_str(line).map_err(|error| format!("line {}: {error}", index + 1))?,
        );
    }
    if rows.is_empty() {
        return Err("holdout is empty".into());
    }
    Ok(rows)
}

fn class_counts(rows: &[HoldoutRow]) -> [usize; LABEL_COUNT] {
    let mut counts = [0_usize; LABEL_COUNT];
    for row in rows {
        if let Some(index) = label_index(&row.label) {
            counts[index] += 1;
        }
    }
    counts
}

fn label_index(label: &str) -> Option<usize> {
    expected_labels()
        .into_iter()
        .position(|candidate| candidate == label)
}

fn percentile(sorted: &[f64], fraction: f64) -> f64 {
    if sorted.is_empty() {
        return 0.0;
    }
    let index = ((sorted.len() as f64 - 1.0) * fraction).round() as usize;
    sorted[index.min(sorted.len() - 1)]
}

fn sampled_rss_bytes() -> Option<u64> {
    let output = std::process::Command::new("ps")
        .args(["-o", "rss=", "-p", &std::process::id().to_string()])
        .output()
        .ok()?;
    let kilobytes: u64 = String::from_utf8_lossy(&output.stdout)
        .trim()
        .parse()
        .ok()?;
    Some(kilobytes.saturating_mul(1024))
}
