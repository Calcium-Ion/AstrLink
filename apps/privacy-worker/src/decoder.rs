use std::collections::{BTreeMap, BTreeSet};

use serde::Deserialize;

use crate::{
    manifest::{TagScheme, canonical_kind},
    protocol::DetectedSpan,
};

const OPENAI_LABEL_COUNT: usize = 33;
const MAX_LABEL_COUNT: usize = 256;

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

#[derive(Clone, Copy, Debug, Default, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TransitionBiases {
    pub transition_bias_background_stay: f32,
    pub transition_bias_background_to_start: f32,
    pub transition_bias_end_to_background: f32,
    pub transition_bias_end_to_start: f32,
    pub transition_bias_inside_to_continue: f32,
    pub transition_bias_inside_to_end: f32,
}

#[derive(Debug, Deserialize)]
struct ModelConfig {
    id2label: BTreeMap<String, String>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
struct Entity {
    source: String,
    canonical: Option<String>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
enum Tag {
    Outside,
    Begin(Entity),
    Inside(Entity),
    End(Entity),
    Single(Entity),
}

#[derive(Clone, Debug)]
pub struct Decoder {
    labels: Vec<Tag>,
    scheme: TagScheme,
    biases: TransitionBiases,
    sensitive: Option<SensitiveCalibration>,
}

#[derive(Clone, Debug)]
struct SensitiveCalibration {
    en: LanguageCalibration,
    zh: LanguageCalibration,
}

#[derive(Clone, Debug)]
struct LanguageCalibration {
    emission_bias: Vec<f32>,
    thresholds: BTreeMap<String, f32>,
    span_confidence: BTreeMap<String, SpanCalibrator>,
}

#[derive(Clone, Copy, Debug, Deserialize)]
struct SpanCalibrator {
    slope: f64,
    intercept: f64,
    empirical_precision_lower_bound: f32,
}

#[derive(Debug, Deserialize)]
struct SensitiveCalibrationDocument {
    schema_version: u8,
    status: String,
    decoder: String,
    language_decoder_biases: BTreeMap<String, EmissionBias>,
    language_operating_points: BTreeMap<String, OperatingPoint>,
    span_confidence: BTreeMap<String, BTreeMap<String, SpanCalibrator>>,
}

#[derive(Debug, Deserialize)]
struct EmissionBias {
    emission_bias: Vec<f32>,
}

#[derive(Debug, Deserialize)]
struct OperatingPoint {
    default_threshold: f32,
    per_label: BTreeMap<String, f32>,
}

impl Decoder {
    pub fn from_openai_json(
        config: &[u8],
        calibration: &[u8],
        manifest_mapping: &BTreeMap<String, Option<String>>,
    ) -> Result<Self, &'static str> {
        if manifest_mapping.len() != OPENAI_ENTITY_LABELS.len()
            || OPENAI_ENTITY_LABELS
                .iter()
                .any(|source| !manifest_mapping.contains_key(*source))
        {
            return Err("invalid_model_mapping");
        }
        let labels = parse_labels(config, TagScheme::Bioes, manifest_mapping)?;
        if labels.len() != OPENAI_LABEL_COUNT {
            return Err("invalid_model_labels");
        }
        let expected = expected_openai_labels(manifest_mapping);
        if labels != expected {
            return Err("invalid_model_labels");
        }
        let biases = parse_calibration_biases(calibration)?;
        if [
            biases.transition_bias_background_stay,
            biases.transition_bias_background_to_start,
            biases.transition_bias_end_to_background,
            biases.transition_bias_end_to_start,
            biases.transition_bias_inside_to_continue,
            biases.transition_bias_inside_to_end,
        ]
        .iter()
        .any(|value| !value.is_finite())
        {
            return Err("invalid_calibration");
        }
        Ok(Self {
            labels,
            scheme: TagScheme::Bioes,
            biases,
            sensitive: None,
        })
    }

    pub fn from_hf_json(
        config: &[u8],
        scheme: TagScheme,
        source_mapping: &BTreeMap<String, Option<String>>,
    ) -> Result<Self, &'static str> {
        if source_mapping.is_empty() {
            return Err("invalid_model_mapping");
        }
        let labels = parse_labels(config, scheme, source_mapping)?;
        Ok(Self {
            labels,
            scheme,
            biases: TransitionBiases::default(),
            sensitive: None,
        })
    }

    pub fn from_sensitive_json(
        config: &[u8],
        calibration: &[u8],
        manifest_mapping: &BTreeMap<String, Option<String>>,
    ) -> Result<Self, &'static str> {
        let mut decoder = Self::from_hf_json(config, TagScheme::Bioes, manifest_mapping)?;
        if decoder.labels.len() != OPENAI_LABEL_COUNT
            || decoder.labels != expected_openai_labels(manifest_mapping)
        {
            return Err("invalid_model_labels");
        }
        decoder.sensitive = Some(parse_sensitive_calibration(calibration)?);
        Ok(decoder)
    }

    pub fn label_count(&self) -> usize {
        self.labels.len()
    }

    pub fn sensitive_precision_lower_bound(&self, text: &str, source: &str) -> Option<f32> {
        self.language_calibration(text)?
            .span_confidence
            .get(source)
            .map(|calibrator| calibrator.empirical_precision_lower_bound)
    }

    pub fn decode(
        &self,
        text_id: u32,
        logits: &[f32],
        offsets: &[(usize, usize)],
    ) -> Result<Vec<DetectedSpan>, &'static str> {
        self.decode_internal(text_id, logits, offsets, None)
    }

    pub fn decode_sensitive(
        &self,
        text_id: u32,
        logits: &[f32],
        offsets: &[(usize, usize)],
        text: &str,
    ) -> Result<Vec<DetectedSpan>, &'static str> {
        if self.sensitive.is_none() {
            return Err("invalid_calibration");
        }
        self.decode_internal(text_id, logits, offsets, Some(text))
    }

    fn decode_internal(
        &self,
        text_id: u32,
        logits: &[f32],
        offsets: &[(usize, usize)],
        text: Option<&str>,
    ) -> Result<Vec<DetectedSpan>, &'static str> {
        let label_count = self.label_count();
        if logits.len() != offsets.len() * label_count {
            return Err("invalid_logits");
        }
        if logits.iter().any(|value| !value.is_finite()) {
            return Err("invalid_logits");
        }
        if offsets.is_empty() {
            return Ok(Vec::new());
        }

        let language = text.and_then(|value| self.language_calibration(value));
        let adjusted_logits;
        let logits = if let Some(language) = language {
            adjusted_logits = logits
                .iter()
                .enumerate()
                .map(|(index, value)| *value + language.emission_bias[index % label_count])
                .collect::<Vec<_>>();
            adjusted_logits.as_slice()
        } else {
            logits
        };
        let path = self.viterbi(logits, offsets.len())?;
        let probabilities = path
            .iter()
            .enumerate()
            .map(|(token, state)| {
                softmax_probability(
                    &logits[token * label_count..(token + 1) * label_count],
                    *state,
                )
            })
            .collect::<Vec<_>>();

        match self.scheme {
            TagScheme::Bio => decode_bio(
                text_id,
                &self.labels,
                &path,
                offsets,
                &probabilities,
                language,
            ),
            TagScheme::Bioes => decode_bioes(
                text_id,
                &self.labels,
                &path,
                offsets,
                &probabilities,
                language,
            ),
        }
    }

    fn language_calibration(&self, text: &str) -> Option<&LanguageCalibration> {
        let sensitive = self.sensitive.as_ref()?;
        if text.chars().any(is_zh_signal) {
            Some(&sensitive.zh)
        } else {
            Some(&sensitive.en)
        }
    }

    fn viterbi(&self, logits: &[f32], token_count: usize) -> Result<Vec<usize>, &'static str> {
        let label_count = self.label_count();
        let negative_infinity = f32::NEG_INFINITY;
        let mut scores = vec![negative_infinity; label_count];
        let mut backpointers = vec![vec![0_usize; label_count]; token_count];

        for (state, tag) in self.labels.iter().enumerate() {
            if valid_start(self.scheme, tag) {
                scores[state] = logits[state] + self.start_bias(tag);
            }
        }

        for token in 1..token_count {
            let mut next_scores = vec![negative_infinity; label_count];
            for (next, next_tag) in self.labels.iter().enumerate() {
                for (previous, previous_score) in scores.iter().enumerate() {
                    let Some(bias) = self.transition_bias(&self.labels[previous], next_tag) else {
                        continue;
                    };
                    let candidate = *previous_score + bias + logits[token * label_count + next];
                    if candidate > next_scores[next] {
                        next_scores[next] = candidate;
                        backpointers[token][next] = previous;
                    }
                }
            }
            scores = next_scores;
        }

        let (mut state, score) = scores
            .iter()
            .enumerate()
            .filter(|(state, _)| valid_end(self.scheme, &self.labels[*state]))
            .max_by(|(_, left), (_, right)| left.total_cmp(right))
            .ok_or("invalid_logits")?;
        if !score.is_finite() {
            return Err("invalid_logits");
        }

        let mut path = vec![0_usize; token_count];
        path[token_count - 1] = state;
        for token in (1..token_count).rev() {
            state = backpointers[token][state];
            path[token - 1] = state;
        }
        Ok(path)
    }

    fn start_bias(&self, next: &Tag) -> f32 {
        match next {
            Tag::Outside => self.biases.transition_bias_background_stay,
            Tag::Begin(_) | Tag::Single(_) => self.biases.transition_bias_background_to_start,
            Tag::Inside(_) | Tag::End(_) => 0.0,
        }
    }

    fn transition_bias(&self, previous: &Tag, next: &Tag) -> Option<f32> {
        match self.scheme {
            TagScheme::Bio => self.bio_transition_bias(previous, next),
            TagScheme::Bioes => self.bioes_transition_bias(previous, next),
        }
    }

    fn bio_transition_bias(&self, previous: &Tag, next: &Tag) -> Option<f32> {
        match (previous, next) {
            (Tag::Outside, Tag::Outside) => Some(self.biases.transition_bias_background_stay),
            (Tag::Outside, Tag::Begin(_)) => Some(self.biases.transition_bias_background_to_start),
            (Tag::Begin(left) | Tag::Inside(left), Tag::Inside(right))
                if left.source == right.source =>
            {
                Some(self.biases.transition_bias_inside_to_continue)
            }
            (Tag::Begin(_) | Tag::Inside(_), Tag::Outside) => {
                Some(self.biases.transition_bias_end_to_background)
            }
            (Tag::Begin(_) | Tag::Inside(_), Tag::Begin(_)) => {
                Some(self.biases.transition_bias_end_to_start)
            }
            _ => None,
        }
    }

    fn bioes_transition_bias(&self, previous: &Tag, next: &Tag) -> Option<f32> {
        match (previous, next) {
            (Tag::Outside, Tag::Outside) => Some(self.biases.transition_bias_background_stay),
            (Tag::Outside, Tag::Begin(_) | Tag::Single(_)) => {
                Some(self.biases.transition_bias_background_to_start)
            }
            (Tag::Begin(left) | Tag::Inside(left), Tag::Inside(right))
                if left.source == right.source =>
            {
                Some(self.biases.transition_bias_inside_to_continue)
            }
            (Tag::Begin(left) | Tag::Inside(left), Tag::End(right))
                if left.source == right.source =>
            {
                Some(self.biases.transition_bias_inside_to_end)
            }
            (Tag::End(_) | Tag::Single(_), Tag::Outside) => {
                Some(self.biases.transition_bias_end_to_background)
            }
            (Tag::End(_) | Tag::Single(_), Tag::Begin(_) | Tag::Single(_)) => {
                Some(self.biases.transition_bias_end_to_start)
            }
            _ => None,
        }
    }
}

fn parse_calibration_biases(calibration: &[u8]) -> Result<TransitionBiases, &'static str> {
    let document: serde_json::Value =
        serde_json::from_slice(calibration).map_err(|_| "invalid_calibration")?;
    let root = document.as_object().ok_or("invalid_calibration")?;
    let openai = root.get("operating_points");
    let sheltron = root.get("operating_point");
    let biases = match (openai, sheltron) {
        (Some(points), None) => points.get("default").and_then(|point| point.get("biases")),
        (None, Some(point)) => point.get("transition_biases"),
        _ => None,
    }
    .ok_or("invalid_calibration")?;
    serde_json::from_value(biases.clone()).map_err(|_| "invalid_calibration")
}

fn parse_sensitive_calibration(calibration: &[u8]) -> Result<SensitiveCalibration, &'static str> {
    let document: SensitiveCalibrationDocument =
        serde_json::from_slice(calibration).map_err(|_| "invalid_calibration")?;
    // Schema 1 and 2 share the numeric fields this decoder consumes.
    // Schema 2 may add compatibility metadata that we ignore.
    if !matches!(document.schema_version, 1 | 2)
        || document.status != "fitted"
        || document.decoder != "bioes-constrained-viterbi"
        || document.language_decoder_biases.len() != 2
        || document.language_operating_points.len() != 2
        || document.span_confidence.len() != 2
    {
        return Err("invalid_calibration");
    }
    let parse_language = |language: &str| -> Result<LanguageCalibration, &'static str> {
        let emission_bias = document
            .language_decoder_biases
            .get(language)
            .ok_or("invalid_calibration")?
            .emission_bias
            .clone();
        let operating_point = document
            .language_operating_points
            .get(language)
            .ok_or("invalid_calibration")?;
        let span_confidence = document
            .span_confidence
            .get(language)
            .ok_or("invalid_calibration")?
            .clone();
        if emission_bias.len() != OPENAI_LABEL_COUNT
            || emission_bias.iter().any(|value| !value.is_finite())
            || !probability(operating_point.default_threshold)
            || operating_point.per_label.len() != OPENAI_ENTITY_LABELS.len()
            || span_confidence.len() != OPENAI_ENTITY_LABELS.len()
        {
            return Err("invalid_calibration");
        }
        for source in OPENAI_ENTITY_LABELS {
            if operating_point
                .per_label
                .get(source)
                .is_none_or(|value| !probability(*value))
                || span_confidence.get(source).is_none_or(|calibrator| {
                    !calibrator.slope.is_finite()
                        || !calibrator.intercept.is_finite()
                        || !probability(calibrator.empirical_precision_lower_bound)
                })
            {
                return Err("invalid_calibration");
            }
        }
        Ok(LanguageCalibration {
            emission_bias,
            thresholds: operating_point.per_label.clone(),
            span_confidence,
        })
    };
    Ok(SensitiveCalibration {
        en: parse_language("en")?,
        zh: parse_language("zh")?,
    })
}

fn probability(value: f32) -> bool {
    value.is_finite() && (0.0..=1.0).contains(&value)
}

fn is_zh_signal(character: char) -> bool {
    matches!(
        character as u32,
        0x3000..=0x303F
            | 0x3400..=0x4DBF
            | 0x4E00..=0x9FFF
            | 0xF900..=0xFAFF
            | 0xFF00..=0xFFEF
            | 0x20000..=0x3134F
    )
}

fn parse_labels(
    config: &[u8],
    scheme: TagScheme,
    source_mapping: &BTreeMap<String, Option<String>>,
) -> Result<Vec<Tag>, &'static str> {
    if source_mapping
        .values()
        .flatten()
        .any(|kind| !canonical_kind(kind))
    {
        return Err("invalid_model_mapping");
    }
    let config: ModelConfig = serde_json::from_slice(config).map_err(|_| "invalid_model_config")?;
    if config.id2label.is_empty() || config.id2label.len() > MAX_LABEL_COUNT {
        return Err("invalid_model_labels");
    }

    let mut labels = Vec::with_capacity(config.id2label.len());
    let mut used_sources = BTreeSet::new();
    for index in 0..config.id2label.len() {
        let label = config
            .id2label
            .get(&index.to_string())
            .ok_or("invalid_model_labels")?;
        labels.push(parse_tag(label, scheme, source_mapping, &mut used_sources)?);
    }
    if labels
        .iter()
        .filter(|label| matches!(label, Tag::Outside))
        .count()
        != 1
        || used_sources.len() != source_mapping.len()
        || source_mapping
            .keys()
            .any(|source| !used_sources.contains(source))
    {
        return Err("invalid_model_mapping");
    }
    validate_tag_set(&labels, scheme)?;
    Ok(labels)
}

fn parse_tag(
    label: &str,
    scheme: TagScheme,
    source_mapping: &BTreeMap<String, Option<String>>,
    used_sources: &mut BTreeSet<String>,
) -> Result<Tag, &'static str> {
    if label == "O" {
        return Ok(Tag::Outside);
    }
    let bytes = label.as_bytes();
    if bytes.len() < 3 || !matches!(bytes[1], b'-' | b'_') {
        return Err("invalid_model_labels");
    }
    let prefix = &label[..1];
    let source = &label[2..];
    if source.is_empty()
        || source.len() > 128
        || source.chars().any(|character| character.is_control())
    {
        return Err("invalid_model_labels");
    }
    let canonical = source_mapping
        .get(source)
        .ok_or("invalid_model_mapping")?
        .clone();
    used_sources.insert(source.to_owned());
    let entity = Entity {
        source: source.to_owned(),
        canonical,
    };
    match (scheme, prefix) {
        (_, "B") => Ok(Tag::Begin(entity)),
        (_, "I") => Ok(Tag::Inside(entity)),
        (TagScheme::Bioes, "E") => Ok(Tag::End(entity)),
        (TagScheme::Bioes, "S") => Ok(Tag::Single(entity)),
        _ => Err("invalid_model_labels"),
    }
}

fn validate_tag_set(labels: &[Tag], scheme: TagScheme) -> Result<(), &'static str> {
    let mut tags = BTreeMap::<&str, [bool; 4]>::new();
    for tag in labels {
        let (entity, offset) = match tag {
            Tag::Outside => continue,
            Tag::Begin(entity) => (entity, 0),
            Tag::Inside(entity) => (entity, 1),
            Tag::End(entity) => (entity, 2),
            Tag::Single(entity) => (entity, 3),
        };
        let seen = tags.entry(entity.source.as_str()).or_default();
        if seen[offset] {
            return Err("invalid_model_labels");
        }
        seen[offset] = true;
    }
    let complete = match scheme {
        TagScheme::Bio => [true, true, false, false],
        TagScheme::Bioes => [true, true, true, true],
    };
    if tags.is_empty() || tags.values().any(|seen| seen != &complete) {
        return Err("invalid_model_labels");
    }
    Ok(())
}

fn expected_openai_labels(mapping: &BTreeMap<String, Option<String>>) -> Vec<Tag> {
    let mut labels = vec![Tag::Outside];
    for source in OPENAI_ENTITY_LABELS {
        let entity = Entity {
            source: source.to_owned(),
            canonical: mapping
                .get(source)
                .expect("OpenAI mapping was validated")
                .clone(),
        };
        labels.extend([
            Tag::Begin(entity.clone()),
            Tag::Inside(entity.clone()),
            Tag::End(entity.clone()),
            Tag::Single(entity),
        ]);
    }
    labels
}

fn valid_start(scheme: TagScheme, tag: &Tag) -> bool {
    match scheme {
        TagScheme::Bio => matches!(tag, Tag::Outside | Tag::Begin(_)),
        TagScheme::Bioes => matches!(tag, Tag::Outside | Tag::Begin(_) | Tag::Single(_)),
    }
}

fn valid_end(scheme: TagScheme, tag: &Tag) -> bool {
    match scheme {
        TagScheme::Bio => matches!(tag, Tag::Outside | Tag::Begin(_) | Tag::Inside(_)),
        TagScheme::Bioes => matches!(tag, Tag::Outside | Tag::End(_) | Tag::Single(_)),
    }
}

fn decode_bio(
    text_id: u32,
    labels: &[Tag],
    path: &[usize],
    offsets: &[(usize, usize)],
    probabilities: &[f32],
    calibration: Option<&LanguageCalibration>,
) -> Result<Vec<DetectedSpan>, &'static str> {
    let mut spans = Vec::new();
    let mut index = 0;
    while index < path.len() {
        match &labels[path[index]] {
            Tag::Begin(entity) => {
                let start = index;
                index += 1;
                while index < path.len()
                    && matches!(
                        &labels[path[index]],
                        Tag::Inside(next) if next.source == entity.source
                    )
                {
                    index += 1;
                }
                if let Some(canonical) = &entity.canonical {
                    push_span(
                        &mut spans,
                        text_id,
                        entity,
                        canonical,
                        &offsets[start..index],
                        &probabilities[start..index],
                        calibration,
                    );
                }
            }
            Tag::Outside => index += 1,
            _ => return Err("invalid_decoded_path"),
        }
    }
    Ok(spans)
}

fn decode_bioes(
    text_id: u32,
    labels: &[Tag],
    path: &[usize],
    offsets: &[(usize, usize)],
    probabilities: &[f32],
    calibration: Option<&LanguageCalibration>,
) -> Result<Vec<DetectedSpan>, &'static str> {
    let mut spans = Vec::new();
    let mut index = 0;
    while index < path.len() {
        match &labels[path[index]] {
            Tag::Single(entity) => {
                if let Some(canonical) = &entity.canonical {
                    push_span(
                        &mut spans,
                        text_id,
                        entity,
                        canonical,
                        &offsets[index..=index],
                        &probabilities[index..=index],
                        calibration,
                    );
                }
                index += 1;
            }
            Tag::Begin(entity) => {
                let start = index;
                index += 1;
                while index < path.len()
                    && matches!(
                        &labels[path[index]],
                        Tag::Inside(next) if next.source == entity.source
                    )
                {
                    index += 1;
                }
                if index >= path.len()
                    || !matches!(
                        &labels[path[index]],
                        Tag::End(next) if next.source == entity.source
                    )
                {
                    return Err("invalid_decoded_path");
                }
                index += 1;
                if let Some(canonical) = &entity.canonical {
                    push_span(
                        &mut spans,
                        text_id,
                        entity,
                        canonical,
                        &offsets[start..index],
                        &probabilities[start..index],
                        calibration,
                    );
                }
            }
            Tag::Outside => index += 1,
            _ => return Err("invalid_decoded_path"),
        }
    }
    Ok(spans)
}

fn softmax_probability(logits: &[f32], state: usize) -> f32 {
    let maximum = logits.iter().copied().fold(f32::NEG_INFINITY, f32::max);
    if !maximum.is_finite() {
        return 0.0;
    }
    let denominator = logits
        .iter()
        .map(|value| (*value - maximum).exp())
        .sum::<f32>();
    if denominator == 0.0 || !denominator.is_finite() {
        return 0.0;
    }
    ((logits[state] - maximum).exp() / denominator).clamp(0.0, 1.0)
}

fn push_span(
    spans: &mut Vec<DetectedSpan>,
    text_id: u32,
    entity: &Entity,
    canonical: &str,
    offsets: &[(usize, usize)],
    probabilities: &[f32],
    calibration: Option<&LanguageCalibration>,
) {
    let Some(start) = offsets
        .iter()
        .find_map(|(start, end)| (start < end).then_some(*start))
    else {
        return;
    };
    let Some(end) = offsets
        .iter()
        .rev()
        .find_map(|(start, end)| (start < end).then_some(*end))
    else {
        return;
    };
    if start >= end {
        return;
    }
    let raw_score = probabilities.iter().copied().sum::<f32>() / probabilities.len() as f32;
    let score = calibration
        .and_then(|language| language.span_confidence.get(&entity.source))
        .map_or(raw_score, |calibrator| {
            calibrated_probability(raw_score, calibrator)
        });
    if calibration
        .and_then(|language| language.thresholds.get(&entity.source))
        .is_some_and(|threshold| score < *threshold)
    {
        return;
    }
    spans.push(DetectedSpan {
        text_id,
        label: canonical.into(),
        start,
        end,
        score,
    });
}

fn calibrated_probability(score: f32, calibrator: &SpanCalibrator) -> f32 {
    let score = f64::from(score).clamp(1.0e-7, 1.0 - 1.0e-7);
    let log_odds = (score / (1.0 - score)).ln();
    let value = calibrator.slope * log_odds + calibrator.intercept;
    if value >= 40.0 {
        1.0
    } else if value <= -40.0 {
        0.0
    } else {
        (1.0 / (1.0 + (-value).exp())) as f32
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn config(labels: &[&str]) -> Vec<u8> {
        let id2label = labels
            .iter()
            .enumerate()
            .map(|(index, label)| (index.to_string(), *label))
            .collect::<BTreeMap<_, _>>();
        serde_json::to_vec(&serde_json::json!({ "id2label": id2label })).expect("config")
    }

    fn generic_decoder(scheme: TagScheme, labels: &[&str]) -> Decoder {
        let mapping = BTreeMap::from([
            ("EMAIL".into(), Some("email".into())),
            ("IGNORED".into(), None),
        ]);
        Decoder::from_hf_json(&config(labels), scheme, &mapping).expect("decoder")
    }

    #[test]
    fn generic_bio_maps_source_labels_to_canonical_kinds() {
        let decoder = generic_decoder(
            TagScheme::Bio,
            &["O", "B_EMAIL", "I_EMAIL", "B_IGNORED", "I_IGNORED"],
        );
        let count = decoder.label_count();
        let mut logits = vec![-10.0; 3 * count];
        logits[1] = 10.0;
        logits[count + 2] = 10.0;
        logits[2 * count] = 10.0;

        let spans = decoder
            .decode(4, &logits, &[(0, 2), (2, 4), (4, 6)])
            .expect("decode");
        assert_eq!(spans.len(), 1);
        assert_eq!(spans[0].label, "email");
        assert_eq!((spans[0].start, spans[0].end), (0, 4));
    }

    #[test]
    fn generic_bioes_ignores_null_mapped_entity() {
        let decoder = generic_decoder(
            TagScheme::Bioes,
            &[
                "O",
                "B-EMAIL",
                "I-EMAIL",
                "E-EMAIL",
                "S-EMAIL",
                "B-IGNORED",
                "I-IGNORED",
                "E-IGNORED",
                "S-IGNORED",
            ],
        );
        let count = decoder.label_count();
        let mut logits = vec![-10.0; count];
        logits[8] = 10.0;
        assert!(decoder.decode(1, &logits, &[(0, 4)]).unwrap().is_empty());
    }

    #[test]
    fn rejects_incomplete_mapping_and_scheme_mismatch() {
        let mapping = BTreeMap::from([("EMAIL".into(), Some("email".into()))]);
        assert_eq!(
            Decoder::from_hf_json(
                &config(&["O", "B-EMAIL", "I-EMAIL", "B-PHONE", "I-PHONE"]),
                TagScheme::Bio,
                &mapping,
            )
            .unwrap_err(),
            "invalid_model_mapping"
        );
        assert_eq!(
            Decoder::from_hf_json(
                &config(&["O", "B-EMAIL", "I-EMAIL", "E-EMAIL", "S-EMAIL"]),
                TagScheme::Bio,
                &mapping,
            )
            .unwrap_err(),
            "invalid_model_labels"
        );
    }

    #[test]
    fn openai_adapter_requires_exact_label_order_and_canonicalizes_output() {
        let mut labels = vec!["O".to_owned()];
        for source in OPENAI_ENTITY_LABELS {
            for prefix in ["B", "I", "E", "S"] {
                labels.push(format!("{prefix}-{source}"));
            }
        }
        let labels = labels.iter().map(String::as_str).collect::<Vec<_>>();
        let calibration = br#"{
            "operating_points":{"default":{"biases":{
                "transition_bias_background_stay":0.0,
                "transition_bias_background_to_start":0.0,
                "transition_bias_end_to_background":0.0,
                "transition_bias_end_to_start":0.0,
                "transition_bias_inside_to_continue":0.0,
                "transition_bias_inside_to_end":0.0
            }}}
        }"#;
        let mut mapping = default_openai_mapping();
        mapping.insert("private_email".into(), Some("phone".into()));
        mapping.insert("secret".into(), None);
        let decoder =
            Decoder::from_openai_json(&config(&labels), calibration, &mapping).expect("decoder");
        let count = decoder.label_count();
        let mut logits = vec![-10.0; 2 * count];
        logits[16] = 10.0;
        logits[count + 32] = 10.0;
        let spans = decoder
            .decode(9, &logits, &[(3, 8), (9, 15)])
            .expect("decode");
        assert_eq!(spans.len(), 1);
        assert_eq!(spans[0].label, "phone");
        assert_eq!((spans[0].start, spans[0].end), (3, 8));

        mapping.remove("secret");
        assert_eq!(
            Decoder::from_openai_json(&config(&labels), calibration, &mapping).unwrap_err(),
            "invalid_model_mapping"
        );
    }

    #[test]
    fn openai_compatible_adapter_accepts_sheltron_calibration_schema() {
        let mut labels = vec!["O".to_owned()];
        for source in OPENAI_ENTITY_LABELS {
            for prefix in ["B", "I", "E", "S"] {
                labels.push(format!("{prefix}-{source}"));
            }
        }
        let labels = labels.iter().map(String::as_str).collect::<Vec<_>>();
        let calibration = br#"{
            "schema_version":"sheltron_privacy_filter_viterbi_calibration.v1",
            "decoder":"constrained_bioes_viterbi",
            "operating_point":{
                "status":"default_zero_bias_not_fitted",
                "transition_biases":{
                    "transition_bias_background_stay":0.0,
                    "transition_bias_background_to_start":0.0,
                    "transition_bias_inside_to_continue":0.0,
                    "transition_bias_inside_to_end":0.0,
                    "transition_bias_end_to_background":0.0,
                    "transition_bias_end_to_start":0.0
                }
            }
        }"#;
        Decoder::from_openai_json(&config(&labels), calibration, &default_openai_mapping())
            .expect("Sheltron calibration");

        let ambiguous = br#"{
            "operating_points":{"default":{"biases":{}}},
            "operating_point":{"transition_biases":{}}
        }"#;
        assert_eq!(
            Decoder::from_openai_json(&config(&labels), ambiguous, &default_openai_mapping())
                .unwrap_err(),
            "invalid_calibration"
        );

        for invalid in [
            br#"{
                "Operating_Point":{"transition_biases":{
                    "transition_bias_background_stay":0.0,
                    "transition_bias_background_to_start":0.0,
                    "transition_bias_inside_to_continue":0.0,
                    "transition_bias_inside_to_end":0.0,
                    "transition_bias_end_to_background":0.0,
                    "transition_bias_end_to_start":0.0
                }}
            }"#
            .as_slice(),
            br#"{
                "operating_point":{"transition_biases":{
                    "transition_bias_background_stay":3.5e38,
                    "transition_bias_background_to_start":0.0,
                    "transition_bias_inside_to_continue":0.0,
                    "transition_bias_inside_to_end":0.0,
                    "transition_bias_end_to_background":0.0,
                    "transition_bias_end_to_start":0.0
                }}
            }"#
            .as_slice(),
        ] {
            assert_eq!(
                Decoder::from_openai_json(&config(&labels), invalid, &default_openai_mapping())
                    .unwrap_err(),
                "invalid_calibration"
            );
        }
    }

    #[test]
    fn sensitive_adapter_accepts_viterbi_schema_one_and_two() {
        let mut labels = vec!["O".to_owned()];
        for source in OPENAI_ENTITY_LABELS {
            for prefix in ["B", "I", "E", "S"] {
                labels.push(format!("{prefix}-{source}"));
            }
        }
        let labels = labels.iter().map(String::as_str).collect::<Vec<_>>();
        let mapping = default_openai_mapping();
        Decoder::from_sensitive_json(&config(&labels), &sensitive_calibration(1, false), &mapping)
            .expect("schema 1");
        Decoder::from_sensitive_json(&config(&labels), &sensitive_calibration(2, true), &mapping)
            .expect("schema 2 with compatibility metadata");
        assert_eq!(
            Decoder::from_sensitive_json(
                &config(&labels),
                &sensitive_calibration(3, false),
                &mapping,
            )
            .unwrap_err(),
            "invalid_calibration"
        );
    }

    fn sensitive_calibration(schema_version: u8, with_compatibility: bool) -> Vec<u8> {
        let zeros = vec![0.0; OPENAI_LABEL_COUNT];
        let mut thresholds = serde_json::Map::new();
        let mut calibrators = serde_json::Map::new();
        for source in OPENAI_ENTITY_LABELS {
            thresholds.insert((*source).into(), serde_json::json!(0.0));
            calibrators.insert(
                (*source).into(),
                serde_json::json!({
                    "slope": 1.0,
                    "intercept": 0.0,
                    "empirical_precision_lower_bound": 0.9
                }),
            );
        }
        let mut root = serde_json::json!({
            "schema_version": schema_version,
            "status": "fitted",
            "decoder": "bioes-constrained-viterbi",
            "language_decoder_biases": {
                "en": {"emission_bias": zeros.clone()},
                "zh": {"emission_bias": zeros}
            },
            "language_operating_points": {
                "en": {"default_threshold": 0.0, "per_label": thresholds.clone()},
                "zh": {"default_threshold": 0.0, "per_label": thresholds}
            },
            "span_confidence": {
                "en": calibrators.clone(),
                "zh": calibrators
            }
        });
        if with_compatibility {
            root.as_object_mut().unwrap().insert(
                "compatibility".into(),
                serde_json::json!({
                    "confidence_revision": 2,
                    "ranking_policy": "monotonic_platt_without_empirical_floor"
                }),
            );
        }
        serde_json::to_vec(&root).expect("calibration")
    }

    fn default_openai_mapping() -> BTreeMap<String, Option<String>> {
        BTreeMap::from([
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
