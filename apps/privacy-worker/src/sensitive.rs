use std::{
    collections::{BTreeMap, BTreeSet, HashMap},
    net::{Ipv4Addr, Ipv6Addr},
};

use regex::{Regex, RegexBuilder};
use serde::Deserialize;

use crate::protocol::DetectedSpan;

const MAX_RULES: usize = 64;
const MAX_PATTERN_BYTES: usize = 16 * 1024;
const MAX_REGEX_SIZE: usize = 8 * 1024 * 1024;

#[derive(Debug)]
pub struct SensitiveGuard {
    rules: Vec<CompiledRule>,
    calibration: SecretCalibration,
    canonical_label: Option<String>,
    ipv4: Regex,
    ipv6_candidate: Regex,
}

#[derive(Debug)]
struct CompiledRule {
    definition: RuleDefinition,
    matcher: RuleMatcher,
}

#[derive(Debug)]
enum RuleMatcher {
    Regex { regex: Regex, capture_group: usize },
    AdoDsn(Regex),
    LibpqDsn(Regex),
    Bip39(Regex),
    HighEntropy(Regex),
}

#[derive(Clone, Debug, Deserialize)]
struct RuleDefinition {
    rule_id: String,
    regex: String,
    #[serde(default)]
    capture_group: usize,
    #[serde(default)]
    keywords: Vec<String>,
    #[serde(default)]
    paired_fields: Vec<String>,
    min_length: Option<usize>,
    entropy: Option<f64>,
    validator: Option<String>,
    label: Option<String>,
    #[serde(default)]
    explicit_assignment: bool,
    #[serde(default)]
    requires_support: bool,
}

#[derive(Debug, Deserialize)]
struct RuleDocument {
    schema_version: u8,
    format: String,
    rules: Vec<RuleDefinition>,
}

#[derive(Debug, Deserialize)]
struct SecretCalibration {
    schema_version: u8,
    status: String,
    mode: String,
    minimum_confidence: f64,
    platt: PlattCalibration,
    score_model: ScoreModel,
}

#[derive(Debug, Deserialize)]
struct PlattCalibration {
    slope: f64,
    intercept: f64,
}

#[derive(Debug, Deserialize)]
struct ScoreModel {
    link: String,
    clip: [f64; 2],
    entropy_offset: f64,
    entropy_cap: f64,
    intercept: f64,
    weights: BTreeMap<String, f64>,
}

#[derive(Clone, Copy, Debug)]
struct CandidateMatch {
    full_start: usize,
    start: usize,
    end: usize,
}

#[derive(Clone, Copy, Debug, Default)]
struct CandidateFeatures {
    context: f64,
    encoded: f64,
    entropy_excess: f64,
    explicit_assignment: f64,
    model_score: f64,
    negative: f64,
    pair: f64,
    pattern: f64,
    structurally_invalid: f64,
    structurally_valid: f64,
}

impl SensitiveGuard {
    pub fn from_json(
        rules: &[u8],
        calibration: &[u8],
        canonical_label: Option<String>,
    ) -> Result<Self, &'static str> {
        let document: RuleDocument =
            serde_json::from_slice(rules).map_err(|_| "invalid_secret_rules")?;
        if document.schema_version != 2
            || document.format != "JSON-compatible YAML"
            || document.rules.is_empty()
            || document.rules.len() > MAX_RULES
        {
            return Err("invalid_secret_rules");
        }
        let mut ids = BTreeSet::new();
        let mut compiled = Vec::with_capacity(document.rules.len());
        for definition in document.rules {
            validate_rule(&definition, &mut ids)?;
            let matcher = compile_matcher(&definition)?;
            compiled.push(CompiledRule {
                definition,
                matcher,
            });
        }
        if !ids.contains("password.assignment") || !ids.contains("generic.keyword_secret") {
            return Err("invalid_secret_rules");
        }

        let calibration: SecretCalibration =
            serde_json::from_slice(calibration).map_err(|_| "invalid_secret_calibration")?;
        validate_calibration(&calibration)?;
        Ok(Self {
            rules: compiled,
            calibration,
            canonical_label,
            ipv4: compile_regex(r"(?:[0-9]{1,3}\.){3}[0-9]{1,3}")?,
            ipv6_candidate: compile_regex(r"[0-9a-f:]{2,39}")?,
        })
    }

    pub fn fuse(
        &self,
        text_id: u32,
        text: &str,
        model_spans: Vec<DetectedSpan>,
        rule_confidence: f32,
    ) -> Vec<DetectedSpan> {
        let ip_spans = self.ip_spans(text_id, text);
        let Some(label) = self.canonical_label.as_deref() else {
            return append_distinct_spans(model_spans, ip_spans);
        };
        if !rule_confidence.is_finite() || !(0.0..=1.0).contains(&rule_confidence) {
            return append_distinct_spans(model_spans, ip_spans);
        }
        let lowercase = text.to_lowercase();
        let mut candidates = Vec::new();
        for rule in &self.rules {
            for matched in rule.matcher.matches(text) {
                if matched.start >= matched.end
                    || !text.is_char_boundary(matched.start)
                    || !text.is_char_boundary(matched.end)
                {
                    continue;
                }
                let value = &text[matched.start..matched.end];
                if rule.definition.rule_id == "private.pem" && value.len() > 20 * 1024 {
                    continue;
                }
                if rule
                    .definition
                    .min_length
                    .is_some_and(|minimum| value.chars().count() < minimum)
                {
                    continue;
                }
                let entropy = shannon_entropy(value.as_bytes());
                if rule
                    .definition
                    .entropy
                    .is_some_and(|minimum| entropy < minimum)
                {
                    continue;
                }
                let context = contains_any(&lowercase, &rule.definition.keywords);
                let pair = contains_any(&lowercase, &rule.definition.paired_fields);
                let model_score =
                    overlapping_model_score(&model_spans, label, matched.start, matched.end);
                if rule.definition.requires_support && !context && !pair && model_score <= 0.0 {
                    continue;
                }
                let structural =
                    validate_structure(rule.definition.validator.as_deref(), value, pair);
                let features = CandidateFeatures {
                    context: f64::from(context),
                    encoded: f64::from(looks_encoded(value)),
                    entropy_excess: ((entropy - self.calibration.score_model.entropy_offset)
                        / self.calibration.score_model.entropy_cap)
                        .clamp(0.0, 1.0),
                    explicit_assignment: f64::from(
                        rule.definition.explicit_assignment
                            && assignment_before(text, matched.full_start, matched.start),
                    ),
                    model_score,
                    negative: f64::from(obvious_placeholder(value)),
                    pair: f64::from(pair),
                    pattern: 1.0,
                    structurally_invalid: f64::from(structural == Some(false)),
                    structurally_valid: f64::from(structural == Some(true)),
                };
                let confidence = self.calibration.confidence(features);
                if confidence >= self.calibration.minimum_confidence {
                    let output_confidence = if model_score > 0.0 {
                        rule_confidence.max(model_score as f32)
                    } else {
                        rule_confidence
                    };
                    candidates.push(DetectedSpan {
                        text_id,
                        label: label.to_owned(),
                        start: matched.start,
                        end: matched.end,
                        score: output_confidence,
                    });
                }
            }
        }
        append_distinct_spans(merge_spans(model_spans, candidates), ip_spans)
    }

    fn ip_spans(&self, text_id: u32, text: &str) -> Vec<DetectedSpan> {
        let mut spans = self
            .ipv4
            .find_iter(text)
            .filter(|matched| {
                ascii_ip_boundary(text, matched.start(), matched.end())
                    && matched.as_str().parse::<Ipv4Addr>().is_ok()
            })
            .map(|matched| DetectedSpan {
                text_id,
                label: "ip_address".into(),
                start: matched.start(),
                end: matched.end(),
                score: 0.999_999,
            })
            .collect::<Vec<_>>();
        spans.extend(
            self.ipv6_candidate
                .find_iter(text)
                .filter(|matched| {
                    matched.as_str().contains(':')
                        && ascii_ip_boundary(text, matched.start(), matched.end())
                        && matched.as_str().parse::<Ipv6Addr>().is_ok()
                })
                .map(|matched| DetectedSpan {
                    text_id,
                    label: "ip_address".into(),
                    start: matched.start(),
                    end: matched.end(),
                    score: 0.999_999,
                }),
        );
        spans
    }
}

impl RuleMatcher {
    fn matches(&self, text: &str) -> Vec<CandidateMatch> {
        match self {
            Self::Regex {
                regex,
                capture_group,
            } => regex_matches(regex, text, *capture_group),
            Self::HighEntropy(regex) => regex_matches(regex, text, 1)
                .into_iter()
                .filter(|matched| ascii_token_boundary(text, matched.start, matched.end))
                .collect(),
            Self::Bip39(regex) => regex_matches(regex, text, 1)
                .into_iter()
                .filter(|matched| bip39_boundary(text, matched.end))
                .collect(),
            Self::AdoDsn(value_regex) => dsn_matches(
                text,
                value_regex,
                &["server", "data source"],
                &["user id", "uid", "username", "database", "initial catalog"],
            ),
            Self::LibpqDsn(value_regex) => {
                dsn_matches(text, value_regex, &["host"], &["user", "dbname"])
            }
        }
    }
}

impl SecretCalibration {
    fn confidence(&self, features: CandidateFeatures) -> f64 {
        let weight = |name: &str| self.score_model.weights[name];
        let mut score = self.score_model.intercept
            + weight("context") * features.context
            + weight("encoded") * features.encoded
            + weight("entropy_excess") * features.entropy_excess
            + weight("explicit_assignment") * features.explicit_assignment
            + weight("model_score") * features.model_score
            + weight("negative") * features.negative
            + weight("pair") * features.pair
            + weight("pattern") * features.pattern
            + weight("structurally_invalid") * features.structurally_invalid
            + weight("structurally_valid") * features.structurally_valid;
        score = score.clamp(self.score_model.clip[0], self.score_model.clip[1]);
        let log_odds = (score / (1.0 - score)).ln();
        sigmoid(self.platt.slope * log_odds + self.platt.intercept)
    }
}

fn validate_rule(rule: &RuleDefinition, ids: &mut BTreeSet<String>) -> Result<(), &'static str> {
    let validator = rule.validator.as_deref().unwrap_or("");
    let known_validator = matches!(
        validator,
        "" | "basic_auth"
            | "bearer"
            | "bip39"
            | "cloud_pair"
            | "database_dsn"
            | "database_uri"
            | "encryption_key"
            | "jwt"
            | "key_length"
            | "pem"
            | "recovery_code"
            | "signed_url"
            | "totp_base32"
            | "wallet_key"
    );
    if rule.rule_id.is_empty()
        || rule.rule_id.len() > 128
        || rule.regex.is_empty()
        || rule.regex.len() > MAX_PATTERN_BYTES
        || rule.capture_group > 16
        || rule.label.as_deref().unwrap_or("secret") != "secret"
        || !known_validator
        || !ids.insert(rule.rule_id.clone())
        || rule
            .entropy
            .is_some_and(|value| !value.is_finite() || !(0.0..=8.0).contains(&value))
    {
        return Err("invalid_secret_rules");
    }
    Ok(())
}

fn compile_matcher(rule: &RuleDefinition) -> Result<RuleMatcher, &'static str> {
    let matcher = match rule.rule_id.as_str() {
        "database.ado_dsn" => RuleMatcher::AdoDsn(compile_regex(
            r#"(?:password|pwd)\s*=\s*['\"]?([^;\s'\"]{4,256})"#,
        )?),
        "database.libpq_dsn" => RuleMatcher::LibpqDsn(compile_regex(
            r#"\bpassword\s*=\s*['\"]?([^\s'\"]{4,256})"#,
        )?),
        "wallet.bip39" => RuleMatcher::Bip39(compile_regex(
            r#"(?:mnemonic|seed[_ .-]?phrase|助记词)\s*[=:：]\s*['\"]?((?:(?:[a-z]{2,12}|[\u{3400}-\u{9fff}])\s+){11,23}(?:[a-z]{2,12}|[\u{3400}-\u{9fff}]))"#,
        )?),
        "generic.high_entropy" => {
            RuleMatcher::HighEntropy(compile_regex(r"([A-Za-z0-9+/_.~-]{32,128}={0,2})")?)
        }
        "private.pem" => RuleMatcher::Regex {
            regex: compile_regex(
                r"(-----BEGIN (?:(?:RSA|EC|DSA|OPENSSH|ENCRYPTED) PRIVATE KEY|PRIVATE KEY|PGP PRIVATE KEY BLOCK)-----[\s\S]{40,}?-----END (?:(?:RSA|EC|DSA|OPENSSH|ENCRYPTED) PRIVATE KEY|PRIVATE KEY|PGP PRIVATE KEY BLOCK)-----)",
            )?,
            capture_group: 1,
        },
        _ => RuleMatcher::Regex {
            regex: compile_regex(&rule.regex)?,
            capture_group: rule.capture_group,
        },
    };
    Ok(matcher)
}

fn compile_regex(pattern: &str) -> Result<Regex, &'static str> {
    RegexBuilder::new(pattern)
        .case_insensitive(true)
        .size_limit(MAX_REGEX_SIZE)
        .dfa_size_limit(MAX_REGEX_SIZE)
        .build()
        .map_err(|_| "invalid_secret_rules")
}

fn validate_calibration(calibration: &SecretCalibration) -> Result<(), &'static str> {
    const WEIGHTS: [&str; 10] = [
        "context",
        "encoded",
        "entropy_excess",
        "explicit_assignment",
        "model_score",
        "negative",
        "pair",
        "pattern",
        "structurally_invalid",
        "structurally_valid",
    ];
    if calibration.schema_version != 2
        || calibration.status != "fitted"
        || calibration.mode != "rules"
        || calibration.score_model.link != "linear_clip"
        || !probability(calibration.minimum_confidence)
        || !calibration.platt.slope.is_finite()
        || !calibration.platt.intercept.is_finite()
        || !calibration.score_model.intercept.is_finite()
        || !calibration.score_model.entropy_offset.is_finite()
        || !calibration.score_model.entropy_cap.is_finite()
        || calibration.score_model.entropy_cap <= 0.0
        || calibration.score_model.clip[0] < 0.0
        || calibration.score_model.clip[1] > 1.0
        || calibration.score_model.clip[0] >= calibration.score_model.clip[1]
        || calibration.score_model.weights.len() != WEIGHTS.len()
        || WEIGHTS.iter().any(|name| {
            calibration
                .score_model
                .weights
                .get(*name)
                .is_none_or(|value| !value.is_finite())
        })
    {
        return Err("invalid_secret_calibration");
    }
    Ok(())
}

fn regex_matches(regex: &Regex, text: &str, capture_group: usize) -> Vec<CandidateMatch> {
    regex
        .captures_iter(text)
        .filter_map(|captures| {
            let full = captures.get(0)?;
            let value = captures.get(capture_group)?;
            Some(CandidateMatch {
                full_start: full.start(),
                start: value.start(),
                end: value.end(),
            })
        })
        .collect()
}

fn dsn_matches(
    text: &str,
    value_regex: &Regex,
    primary: &[&str],
    secondary: &[&str],
) -> Vec<CandidateMatch> {
    let mut results = Vec::new();
    let mut offset = 0;
    for line in text.split_inclusive(['\r', '\n']) {
        let end = offset + line.len();
        let lower_line = line.to_lowercase();
        if line.len() <= 2048
            && primary.iter().any(|marker| lower_line.contains(marker))
            && secondary.iter().any(|marker| lower_line.contains(marker))
        {
            for mut matched in regex_matches(value_regex, line, 1) {
                matched.full_start += offset;
                matched.start += offset;
                matched.end += offset;
                results.push(matched);
            }
        }
        offset = end;
    }
    results
}

fn contains_any(haystack: &str, needles: &[String]) -> bool {
    needles
        .iter()
        .any(|needle| haystack.contains(&needle.to_lowercase()))
}

fn assignment_before(text: &str, full_start: usize, value_start: usize) -> bool {
    let prefix = &text[full_start..value_start];
    prefix.contains('=')
        || prefix.contains(':')
        || prefix.contains('：')
        || prefix.to_lowercase().contains(" is ")
        || prefix.contains('是')
        || prefix.contains('为')
}

fn overlapping_model_score(spans: &[DetectedSpan], label: &str, start: usize, end: usize) -> f64 {
    spans
        .iter()
        .filter(|span| span.label == label && span.start < end && start < span.end)
        .map(|span| f64::from(span.score))
        .fold(0.0, f64::max)
}

fn validate_structure(validator: Option<&str>, value: &str, pair: bool) -> Option<bool> {
    match validator.unwrap_or("") {
        "" => None,
        "basic_auth" => Some(
            value.len() >= 8
                && value
                    .bytes()
                    .all(|byte| byte.is_ascii_alphanumeric() || b"+/=".contains(&byte)),
        ),
        "bearer" | "key_length" => Some(value.len() >= 16),
        "bip39" => Some(matches!(
            value.split_whitespace().count(),
            12 | 15 | 18 | 21 | 24
        )),
        "cloud_pair" => Some(pair || value.len() >= 30),
        "database_dsn" => Some(value.len() >= 4 && !value.chars().any(char::is_whitespace)),
        "database_uri" => Some(value.contains("://") && value.contains('@') && value.contains(':')),
        "encryption_key" => Some(matches!(value.len(), 24 | 32 | 40 | 44 | 48 | 64 | 88)),
        "jwt" => Some(value.split('.').count() == 3),
        "pem" => Some(value.starts_with("-----BEGIN ") && value.contains("-----END ")),
        "recovery_code" => Some(matches!(value.split('-').count(), 2..=4)),
        "signed_url" => Some(value.len() >= 12),
        "totp_base32" => Some(
            value.len() >= 16
                && value
                    .trim_end_matches('=')
                    .bytes()
                    .all(|byte| byte.is_ascii_uppercase() || (b'2'..=b'7').contains(&byte)),
        ),
        "wallet_key" => Some(
            value.strip_prefix("0x").unwrap_or(value).len() == 64
                || (43..=88).contains(&value.len()),
        ),
        _ => Some(false),
    }
}

fn looks_encoded(value: &str) -> bool {
    if value.len() < 16 || !value.is_ascii() {
        return false;
    }
    let mut classes = 0;
    classes += usize::from(value.bytes().any(|byte| byte.is_ascii_lowercase()));
    classes += usize::from(value.bytes().any(|byte| byte.is_ascii_uppercase()));
    classes += usize::from(value.bytes().any(|byte| byte.is_ascii_digit()));
    classes += usize::from(value.bytes().any(|byte| !byte.is_ascii_alphanumeric()));
    classes >= 2
}

fn obvious_placeholder(value: &str) -> bool {
    let lower = value.to_ascii_lowercase();
    matches!(
        lower.as_str(),
        "password" | "secret" | "token" | "changeme" | "your_api_key_here" | "replace_me"
    ) || value
        .bytes()
        .next()
        .is_some_and(|first| value.len() >= 8 && value.bytes().all(|byte| byte == first))
}

fn shannon_entropy(bytes: &[u8]) -> f64 {
    if bytes.is_empty() {
        return 0.0;
    }
    let mut counts = HashMap::new();
    for byte in bytes {
        *counts.entry(*byte).or_insert(0_usize) += 1;
    }
    counts.values().fold(0.0, |entropy, count| {
        let probability = *count as f64 / bytes.len() as f64;
        entropy - probability * probability.log2()
    })
}

fn ascii_token_boundary(text: &str, start: usize, end: usize) -> bool {
    let left_ok = text[..start]
        .chars()
        .next_back()
        .is_none_or(|character| !character.is_ascii_alphanumeric());
    let right_ok = text[end..]
        .chars()
        .next()
        .is_none_or(|character| !character.is_ascii_alphanumeric());
    left_ok && right_ok
}

fn ascii_ip_boundary(text: &str, start: usize, end: usize) -> bool {
    let invalid =
        |character: char| character.is_ascii_alphanumeric() || matches!(character, '.' | ':');
    text[..start]
        .chars()
        .next_back()
        .is_none_or(|character| !invalid(character))
        && text[end..]
            .chars()
            .next()
            .is_none_or(|character| !invalid(character))
}

fn bip39_boundary(text: &str, end: usize) -> bool {
    let suffix = &text[end..];
    let trimmed = suffix.trim_start_matches(char::is_whitespace);
    trimmed.len() == suffix.len()
        || trimmed
            .chars()
            .next()
            .is_none_or(|character| !character.is_alphabetic() && !is_han(character))
}

fn is_han(character: char) -> bool {
    matches!(character as u32, 0x3400..=0x4DBF | 0x4E00..=0x9FFF)
}

fn probability(value: f64) -> bool {
    value.is_finite() && (0.0..=1.0).contains(&value)
}

fn sigmoid(value: f64) -> f64 {
    if value >= 40.0 {
        1.0
    } else if value <= -40.0 {
        0.0
    } else {
        1.0 / (1.0 + (-value).exp())
    }
}

fn merge_spans(
    model_spans: Vec<DetectedSpan>,
    mut rule_spans: Vec<DetectedSpan>,
) -> Vec<DetectedSpan> {
    rule_spans.sort_by_key(|span| (span.start, span.end));
    let mut deduplicated: Vec<DetectedSpan> = Vec::new();
    for span in rule_spans {
        if let Some(existing) = deduplicated
            .iter_mut()
            .find(|existing| existing.start == span.start && existing.end == span.end)
        {
            existing.score = existing.score.max(span.score);
        } else {
            deduplicated.push(span);
        }
    }
    let mut result = model_spans
        .into_iter()
        .filter(|model| {
            !deduplicated.iter().any(|rule| {
                model.label == rule.label && model.start < rule.end && rule.start < model.end
            })
        })
        .collect::<Vec<_>>();
    result.extend(deduplicated);
    result.sort_by_key(|span| (span.text_id, span.start, span.end));
    result
}

fn append_distinct_spans(
    mut spans: Vec<DetectedSpan>,
    additions: Vec<DetectedSpan>,
) -> Vec<DetectedSpan> {
    for addition in additions {
        if !spans.iter().any(|span| {
            span.label == addition.label
                && span.start == addition.start
                && span.end == addition.end
                && span.text_id == addition.text_id
        }) {
            spans.push(addition);
        }
    }
    spans.sort_by_key(|span| (span.text_id, span.start, span.end));
    spans
}

#[cfg(test)]
mod tests {
    use super::*;

    fn rules() -> Vec<u8> {
        serde_json::to_vec(&serde_json::json!({
            "schema_version": 2,
            "format": "JSON-compatible YAML",
            "rules": [
                {
                    "rule_id": "password.assignment",
                    "regex": "(?:password|passwd|pwd|pin|密码|口令)\\s*(?:[=:：]|(?:is|是|为)\\s*[:：]?)\\s*['\\\"]?([^\\s'\\\",;，。；]{1,256})",
                    "capture_group": 1,
                    "keywords": ["password", "passwd", "pwd", "pin", "密码", "口令"],
                    "min_length": 1,
                    "explicit_assignment": true,
                    "label": "secret"
                },
                {
                    "rule_id": "generic.keyword_secret",
                    "regex": "(?:api[_ .-]?key|access[_ .-]?token|refresh[_ .-]?token|client[_ .-]?secret|token|secret|密钥|令牌)\\s*(?:[=:：]|(?:is|是|为)\\s*[:：]?)\\s*['\\\"]?([^\\s'\\\",;，。；]{1,512})",
                    "capture_group": 1,
                    "keywords": ["api_key", "api key", "token", "secret", "密钥", "令牌"],
                    "min_length": 1,
                    "explicit_assignment": true,
                    "label": "secret"
                }
            ]
        }))
        .expect("rules")
    }

    fn calibration() -> Vec<u8> {
        serde_json::to_vec(&serde_json::json!({
            "schema_version": 2,
            "status": "fitted",
            "mode": "rules",
            "minimum_confidence": 0.9999923399166415_f64,
            "platt": {"slope": 4.62972696991034_f64, "intercept": 3.0854256167290823_f64},
            "score_model": {
                "link": "linear_clip",
                "clip": [0.01_f64, 0.995_f64],
                "entropy_offset": 3.0_f64,
                "entropy_cap": 2.25_f64,
                "intercept": 0.3_f64,
                "weights": {
                    "context": 0.12_f64,
                    "encoded": 0.03_f64,
                    "entropy_excess": 0.04_f64,
                    "explicit_assignment": 0.45_f64,
                    "model_score": 0.18_f64,
                    "negative": -0.65_f64,
                    "pair": 0.08_f64,
                    "pattern": 0.2_f64,
                    "structurally_invalid": -0.12_f64,
                    "structurally_valid": 0.2_f64
                }
            }
        }))
        .expect("calibration")
    }

    #[test]
    fn assignment_rules_recover_both_promoted_model_secrets() {
        let guard =
            SensitiveGuard::from_json(&rules(), &calibration(), Some("common_secret".into()))
                .expect("guard");
        let text = "api_key=example_test_key_1234567890，password=\"demo_password_123456\"";
        let spans = guard.fuse(7, text, Vec::new(), 0.9907427);
        let values = spans
            .iter()
            .map(|span| &text[span.start..span.end])
            .collect::<Vec<_>>();
        assert_eq!(
            values,
            ["example_test_key_1234567890", "demo_password_123456"]
        );
        assert!(spans.iter().all(|span| {
            span.text_id == 7
                && span.label == "common_secret"
                && (span.score - 0.9907427).abs() < 1.0e-6
        }));
    }

    #[test]
    fn null_secret_mapping_keeps_only_model_output() {
        let guard = SensitiveGuard::from_json(&rules(), &calibration(), None).expect("guard");
        let model = vec![DetectedSpan {
            text_id: 1,
            label: "email".into(),
            start: 0,
            end: 3,
            score: 0.9,
        }];
        assert_eq!(guard.fuse(1, "a@b", model.clone(), 0.99), model);
    }

    #[test]
    fn parses_standalone_ipv4_and_ipv6_without_accepting_invalid_octets() {
        let guard = SensitiveGuard::from_json(&rules(), &calibration(), None).expect("guard");
        let text = "hosts=192.168.1.7,[2001:db8::8]; invalid=999.1.2.3";
        let spans = guard.fuse(2, text, Vec::new(), 0.99);
        let values = spans
            .iter()
            .map(|span| &text[span.start..span.end])
            .collect::<Vec<_>>();
        assert_eq!(values, ["192.168.1.7", "2001:db8::8"]);
        assert!(spans.iter().all(|span| span.label == "ip_address"));
    }
}
