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
        })
    }

    pub fn label_count(&self) -> usize {
        self.labels.len()
    }

    pub fn decode(
        &self,
        text_id: u32,
        logits: &[f32],
        offsets: &[(usize, usize)],
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
            TagScheme::Bio => decode_bio(text_id, &self.labels, &path, offsets, &probabilities),
            TagScheme::Bioes => decode_bioes(text_id, &self.labels, &path, offsets, &probabilities),
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
                        canonical,
                        &offsets[start..index],
                        &probabilities[start..index],
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
                        canonical,
                        &offsets[index..=index],
                        &probabilities[index..=index],
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
                        canonical,
                        &offsets[start..index],
                        &probabilities[start..index],
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
    entity: &str,
    offsets: &[(usize, usize)],
    probabilities: &[f32],
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
    let score = probabilities.iter().copied().sum::<f32>() / probabilities.len() as f32;
    spans.push(DetectedSpan {
        text_id,
        label: entity.into(),
        start,
        end,
        score,
    });
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
