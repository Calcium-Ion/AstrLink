use serde::Deserialize;
use std::collections::{BTreeMap, HashSet};

const MAX_MODEL_REDIRECTS: usize = 200;
const MAX_REDIRECT_MODEL_CHARS: usize = 256;
const ASTRLINK_AUTO_MODEL_ID: &str = "astrlink/auto";
const MAX_SYSTEM_PROMPT_CHARS: usize = 20000;

// Mirrors contract/model_routing.go so the host rejects a malformed group
// before Core sees it, and accepts every group Core accepts.
const MAX_MODEL_ROUTING_GROUPS: usize = 64;
const MAX_MODEL_ROUTING_GROUP_ID_RUNES: usize = 64;
const MAX_MODEL_ROUTING_MEMBERS_PER_GROUP: usize = 32;
const MAX_MODEL_ROUTING_MEMBER_ID_RUNES: usize = 64;
const MAX_MODEL_ROUTING_RULES_PER_GROUP: usize = 128;
const MAX_MODEL_ROUTING_RULE_ID_RUNES: usize = 64;
const MAX_MODEL_ROUTING_PRIORITY: i64 = 1_000_000_000;
const MAX_MODEL_ROUTING_CONDITION_DEPTH: usize = 4;
const MAX_MODEL_ROUTING_CONDITION_NODES: usize = 64;
const MAX_MODEL_ROUTING_LIST_ITEMS: usize = 16;
const MAX_MODEL_ROUTING_TASK_TYPE_LABELS: usize = MAX_MODEL_ROUTING_LIST_ITEMS;
const MAX_MODEL_ROUTING_KEYWORDS: usize = 32;
const MAX_MODEL_ROUTING_VALUE_RUNES: usize = 128;
const MODEL_ROUTING_MODEL_PREFIX: &str = "astrlink/";
const MODEL_ROUTING_RESERVED_GROUP_IDS: [&str; 2] = ["auto", "text-v1"];
const ASTRLINK_TEXT_CLASSIFICATION_V1: &str = "astrlink-text-v1";

#[derive(Deserialize)]
#[serde(rename_all = "snake_case")]
enum Action {
    Stop,
    Retry,
    Failover,
    RetryAndFailover,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct FailurePolicy {
    max_retries: u32,
    initial_delay_ms: u32,
    max_delay_ms: u32,
    response_start_timeout_seconds: Option<u32>,
    thinking_signature_recovery: Option<bool>,
    openai_reasoning_recovery: Option<bool>,
    openai_function_output_recovery: Option<bool>,
    network_error: Action,
    response_timeout: Action,
    http_status: BTreeMap<String, Action>,
}

pub(crate) fn validate_failure_policy(value: &serde_json::Value) -> Result<(), String> {
    for key in [
        "response_start_timeout_seconds",
        "thinking_signature_recovery",
        "openai_reasoning_recovery",
        "openai_function_output_recovery",
    ] {
        if value.get(key).is_some_and(serde_json::Value::is_null) {
            return Err(format!("{key} must not be null"));
        }
    }
    let policy: FailurePolicy = serde_json::from_value(value.clone())
        .map_err(|error| format!("invalid failure policy: {error}"))?;
    if policy.max_retries > 5
        || policy.initial_delay_ms > 60000
        || policy.max_delay_ms < policy.initial_delay_ms
        || policy.max_delay_ms > 60000
        || policy
            .response_start_timeout_seconds
            .is_some_and(|value| value > 86400)
    {
        return Err("failure policy count or timeout is out of range".into());
    }
    let _ = (
        policy.network_error,
        policy.response_timeout,
        policy.thinking_signature_recovery,
        policy.openai_reasoning_recovery,
        policy.openai_function_output_recovery,
    );
    for code in policy.http_status.keys() {
        if code.len() != 3
            || !code
                .parse::<u32>()
                .is_ok_and(|status| (400..=599).contains(&status))
        {
            return Err("failure policy HTTP status must be 400 through 599".into());
        }
    }
    Ok(())
}

pub(crate) fn validate_strategy(value: &serde_json::Value) -> Result<(), String> {
    match value.as_str() {
        Some("retry_first" | "failover_first" | "failover_only") => Ok(()),
        _ => Err("invalid failure handling order".into()),
    }
}

pub(crate) fn validate_attempts(value: &serde_json::Value) -> Result<(), String> {
    if value
        .as_u64()
        .is_some_and(|count| (1..=20).contains(&count))
    {
        Ok(())
    } else {
        Err("max_attempts must be 1 through 20".into())
    }
}

/// Mirrors contract.ValidateModelRedirects: sources are unique and no target is
/// another rule's source, so every redirect is a single hop.
fn validate_model_redirects(value: &serde_json::Value) -> Result<(), String> {
    let rules = value
        .as_array()
        .filter(|rules| rules.len() <= MAX_MODEL_REDIRECTS)
        .ok_or("model_redirects must be an array of at most 200 rules")?;
    let mut sources = HashSet::with_capacity(rules.len());
    let mut targets = Vec::with_capacity(rules.len());
    for rule in rules {
        let fields = rule.as_object().ok_or("model redirect must be an object")?;
        if fields.len() != 3
            || !fields
                .get("enabled")
                .is_some_and(serde_json::Value::is_boolean)
        {
            return Err("invalid model redirect fields".into());
        }
        let from = redirect_model(fields.get("from"))?;
        let to = redirect_model(fields.get("to"))?;
        if from == to || to == ASTRLINK_AUTO_MODEL_ID {
            return Err("invalid model redirect target".into());
        }
        if !sources.insert(from) {
            return Err("model redirect source is duplicated".into());
        }
        targets.push(to);
    }
    if targets.iter().any(|to| sources.contains(to)) {
        return Err("model redirect target must not be another rule's source".into());
    }
    Ok(())
}

fn redirect_model(value: Option<&serde_json::Value>) -> Result<&str, String> {
    value
        .and_then(serde_json::Value::as_str)
        .filter(|model| {
            (1..=MAX_REDIRECT_MODEL_CHARS).contains(&model.chars().count())
                && model.trim() == *model
                && !model.chars().any(|c| c < ' ' && c != '\t')
        })
        .ok_or_else(|| "invalid model redirect model".into())
}

/// Mirrors contract.ValidateModelRoutingGroups: bounded groups with unique IDs,
/// resolvable member references, unique rule IDs and priorities, and no model
/// collision with a redirect on either side. `redirects` is the sibling field
/// of the same document; a patch that omits it skips the collision check.
fn validate_model_routing_groups(
    value: &serde_json::Value,
    redirects: Option<&serde_json::Value>,
    known_dimensions: &[String],
) -> Result<(), String> {
    let groups = value
        .as_array()
        .filter(|groups| groups.len() <= MAX_MODEL_ROUTING_GROUPS)
        .ok_or("model_routing_groups must be an array of at most 64 groups")?;
    let mut ids = HashSet::with_capacity(groups.len());
    let mut occupied = HashSet::new();
    for group in groups {
        let fields = fields_of(
            group,
            &[
                "id",
                "enabled",
                "members",
                "default_member_id",
                "rules",
                "task_type_labels",
            ],
        )
        .ok_or("invalid model routing group fields")?;
        let id = string_field(fields, "id").ok_or("model routing group id must be a string")?;
        routing_slug("group id", id, MAX_MODEL_ROUTING_GROUP_ID_RUNES)?;
        if MODEL_ROUTING_RESERVED_GROUP_IDS.contains(&id) {
            return Err(format!("group id {id:?} is reserved"));
        }
        let task_type_labels = validate_task_type_labels(fields)?;
        if !ids.insert(id) {
            return Err(format!("model routing group {id:?} is duplicated"));
        }
        if !fields
            .get("enabled")
            .is_some_and(serde_json::Value::is_boolean)
        {
            return Err("model routing group enabled must be a boolean".into());
        }
        occupied.insert(format!("{MODEL_ROUTING_MODEL_PREFIX}{id}"));

        let members = fields
            .get("members")
            .and_then(serde_json::Value::as_array)
            .filter(|members| {
                (1..=MAX_MODEL_ROUTING_MEMBERS_PER_GROUP).contains(&members.len())
            })
            .ok_or_else(|| {
                format!(
                    "model routing group {id:?} must contain 1 to {MAX_MODEL_ROUTING_MEMBERS_PER_GROUP} members"
                )
            })?;
        let mut member_ids = HashSet::with_capacity(members.len());
        for (index, member) in members.iter().enumerate() {
            let fields = fields_of(member, &["id", "service_id", "model"])
                .ok_or_else(|| format!("members[{index}] has invalid fields"))?;
            let member_id = string_field(fields, "id")
                .ok_or_else(|| format!("members[{index}].id must be a string"))?;
            routing_slug(
                &format!("members[{index}].id"),
                member_id,
                MAX_MODEL_ROUTING_MEMBER_ID_RUNES,
            )?;
            if !member_ids.insert(member_id) {
                return Err(format!("model routing member {member_id:?} is duplicated"));
            }
            let service_id = string_field(fields, "service_id")
                .ok_or_else(|| format!("members[{index}].service_id must be a string"))?;
            if !valid_service_id(service_id) {
                return Err(format!("members[{index}].service_id: invalid service id"));
            }
            let model = string_field(fields, "model")
                .ok_or_else(|| format!("members[{index}].model must be a string"))?;
            routing_model(&format!("members[{index}].model"), model)?;
            occupied.insert(model.to_string());
        }

        let default_member_id = string_field(fields, "default_member_id")
            .ok_or("model routing default_member_id must be a string")?;
        if !member_ids.contains(default_member_id) {
            return Err(format!(
                "default_member_id {default_member_id:?} does not name a member"
            ));
        }

        let rules = fields
            .get("rules")
            .and_then(serde_json::Value::as_array)
            .filter(|rules| rules.len() <= MAX_MODEL_ROUTING_RULES_PER_GROUP)
            .ok_or_else(|| {
                format!(
                    "model routing group {id:?} must contain at most {MAX_MODEL_ROUTING_RULES_PER_GROUP} rules"
                )
            })?;
        let mut rule_ids = HashSet::with_capacity(rules.len());
        let mut priorities = HashSet::with_capacity(rules.len());
        for (index, rule) in rules.iter().enumerate() {
            let fields = fields_of(rule, &["id", "priority", "when", "target_member_id"])
                .ok_or_else(|| format!("rules[{index}] has invalid fields"))?;
            let rule_id = string_field(fields, "id")
                .ok_or_else(|| format!("rules[{index}].id must be a string"))?;
            routing_slug(
                &format!("rules[{index}].id"),
                rule_id,
                MAX_MODEL_ROUTING_RULE_ID_RUNES,
            )?;
            if !rule_ids.insert(rule_id) {
                return Err(format!("model routing rule {rule_id:?} is duplicated"));
            }
            let priority = fields
                .get("priority")
                .and_then(serde_json::Value::as_i64)
                .filter(|priority| (0..=MAX_MODEL_ROUTING_PRIORITY).contains(priority))
                .ok_or_else(|| {
                    format!(
                        "rules[{index}].priority must be between 0 and {MAX_MODEL_ROUTING_PRIORITY}"
                    )
                })?;
            if !priorities.insert(priority) {
                return Err(format!("routing rule priority {priority} is duplicated"));
            }
            let target = string_field(fields, "target_member_id")
                .ok_or_else(|| format!("rules[{index}].target_member_id must be a string"))?;
            if !member_ids.contains(target) {
                return Err(format!(
                    "rules[{index}].target_member_id {target:?} does not name a member"
                ));
            }
            let mut nodes = 0usize;
            validate_routing_condition(
                fields
                    .get("when")
                    .ok_or_else(|| format!("rules[{index}].when is required"))?,
                1,
                &mut nodes,
                &task_type_labels,
                known_dimensions,
            )
            .map_err(|error| format!("rules[{index}].when: {error}"))?;
        }
    }

    if let Some(redirects) = redirects.and_then(serde_json::Value::as_array) {
        for redirect in redirects {
            let Some(redirect) = redirect.as_object() else {
                continue;
            };
            for key in ["from", "to"] {
                if let Some(model) = string_field(redirect, key) {
                    if occupied.contains(model) {
                        return Err(format!("model routing redirect conflicts with {model:?}"));
                    }
                }
            }
        }
    }
    Ok(())
}

/// fields_of returns the object's entries after rejecting any key outside
/// `allowed`, mirroring decodePolicy's unknown-field refusal.
fn fields_of<'a>(
    value: &'a serde_json::Value,
    allowed: &[&str],
) -> Option<&'a serde_json::Map<String, serde_json::Value>> {
    let object = value.as_object()?;
    if object.keys().any(|key| !allowed.contains(&key.as_str())) {
        return None;
    }
    Some(object)
}

fn validate_task_type_labels(
    fields: &serde_json::Map<String, serde_json::Value>,
) -> Result<Vec<String>, String> {
    let Some(value) = fields.get("task_type_labels") else {
        return Ok(["general", "research", "coding", "architect"]
            .into_iter()
            .map(str::to_owned)
            .collect());
    };
    let Some(labels) = value.as_array() else {
        return Err("task_type_labels must be an array".into());
    };
    if labels.is_empty() {
        return Ok(["general", "research", "coding", "architect"]
            .into_iter()
            .map(str::to_owned)
            .collect());
    }
    if labels.len() > MAX_MODEL_ROUTING_TASK_TYPE_LABELS {
        return Err(format!(
            "task_type_labels must contain at most {MAX_MODEL_ROUTING_TASK_TYPE_LABELS} labels"
        ));
    }
    let mut result = Vec::with_capacity(labels.len());
    let mut seen = HashSet::with_capacity(labels.len());
    for (index, label) in labels.iter().enumerate() {
        let label = label
            .as_str()
            .ok_or_else(|| format!("task_type_labels[{index}] must be a string"))?;
        routing_value(&format!("task_type_labels[{index}]"), label)?;
        if !seen.insert(label) {
            return Err(format!(
                "task_type_labels contains duplicate label {label:?}"
            ));
        }
        result.push(label.to_owned());
    }
    Ok(result)
}

fn string_field<'a>(
    fields: &'a serde_json::Map<String, serde_json::Value>,
    key: &str,
) -> Option<&'a str> {
    fields.get(key).and_then(serde_json::Value::as_str)
}

/// routing_slug mirrors validateModelRoutingSlug: lowercase ASCII letters,
/// digits and hyphens, no leading or trailing hyphen.
fn routing_slug(field: &str, value: &str, max: usize) -> Result<(), String> {
    let chars: Vec<char> = value.chars().collect();
    if chars.is_empty() || chars.len() > max {
        return Err(format!("{field} must contain 1 to {max} characters"));
    }
    for (index, value) in chars.iter().enumerate() {
        if !(value.is_ascii_lowercase() || value.is_ascii_digit() || *value == '-') {
            return Err(format!(
                "{field} must contain only lowercase ASCII letters, digits and hyphens"
            ));
        }
        if (index == 0 || index == chars.len() - 1) && *value == '-' {
            return Err(format!("{field} must not start or end with a hyphen"));
        }
    }
    Ok(())
}

/// routing_model mirrors validateModelRoutingModel: a concrete model name, not
/// a reserved group, a URL, or anything carrying credentials.
fn routing_model(field: &str, value: &str) -> Result<(), String> {
    if value.is_empty() || value.chars().count() > MAX_REDIRECT_MODEL_CHARS {
        return Err(format!(
            "{field} must contain 1 to {MAX_REDIRECT_MODEL_CHARS} characters"
        ));
    }
    if value.trim() != value || value.contains(['\r', '\n', '\t']) {
        return Err(format!(
            "{field} must not contain surrounding whitespace or controls"
        ));
    }
    if value == ASTRLINK_TEXT_CLASSIFICATION_V1
        || value == "text-v1"
        || value.starts_with(MODEL_ROUTING_MODEL_PREFIX)
        || value.contains("://")
    {
        return Err(format!(
            "{field} must name a concrete model, not a reserved group or URL"
        ));
    }
    if let Ok(url) = reqwest::Url::parse(value) {
        if !url.username().is_empty() || url.password().is_some() || url.host_str().is_some() {
            return Err(format!("{field} must not contain credentials or a URL"));
        }
    }
    Ok(())
}

fn valid_service_id(value: &str) -> bool {
    let bytes = value.as_bytes();
    (3..=96).contains(&value.len())
        && bytes[0].is_ascii_lowercase()
        && bytes.iter().all(|byte| {
            byte.is_ascii_lowercase() || byte.is_ascii_digit() || matches!(byte, b'_' | b'-')
        })
}

/// routing_field_kind mirrors modelRoutingFieldKind: the field of a routing
/// condition is resolved against two sources of very different standing.
///
/// The 21 built-in arms below are a closed, frozen vocabulary: they are the
/// fields the request itself supplies, the Go registry
/// (contract.modelRoutingFields) and the desktop field catalog both pin the
/// same set of 21, and this function must never grow, reorder, or rename one.
/// They answer "what does the request look like".
///
/// `known_dimensions` is the other source, and it is runtime context rather
/// than a vocabulary: the names of the dimensions the classifier settings
/// currently declare. A custom dimension always resolves to `"string"`, the
/// same as in Go, because its options are free-form names rather than a fixed
/// enum. That one kind is what lets a dimension accept exactly the operators
/// of any other string field (`in`, `not_in`, `equals`, `not_equals`) and
/// nothing else — a dimension is not a new field kind and gains no new
/// operators by existing.
///
/// A name in neither source is unsupported, which is what keeps a typo or a
/// retired field name out of a saved document.
fn routing_field_kind(field: &str, known_dimensions: &[String]) -> Option<&'static str> {
    Some(match field {
        "protocol" | "media_kind" | "task_type" => "string",
        "streaming" | "continuation" | "has_tools" | "has_media" | "has_image" | "has_audio"
        | "has_video" => "bool",
        "turn_count"
        | "user_message_count"
        | "assistant_message_count"
        | "tool_message_count"
        | "tool_declaration_count"
        | "tool_call_count"
        | "input_bytes"
        | "input_text_runes"
        | "estimated_input_context_tokens" => "number",
        "literal_keyword" => "keyword",
        "system_time" => "time",
        // Checked after the frozen table, never before: the built-in registry
        // resolves first in Go too, and ClassifierSettings.validateDimensions
        // refuses a dimension whose name shadows a built-in field, so the two
        // sources cannot disagree in practice.
        _ if known_dimensions.iter().any(|name| name == field) => "string",
        _ => return None,
    })
}

/// validate_routing_condition mirrors validateModelRoutingCondition: a bounded
/// tree of exactly one logical shape, one leaf shape, and one operator per leaf.
/// `known_dimensions` rides alongside `task_type_labels` for the same reason:
/// both are values the condition cannot supply for itself. The labels come
/// from the sibling group field, the dimensions from the classifier-settings
/// singleton, which is a separate resource the routing-settings document
/// deliberately does not carry.
fn validate_routing_condition(
    value: &serde_json::Value,
    depth: usize,
    nodes: &mut usize,
    task_type_labels: &[String],
    known_dimensions: &[String],
) -> Result<(), String> {
    if depth > MAX_MODEL_ROUTING_CONDITION_DEPTH {
        return Err(format!(
            "condition depth exceeds {MAX_MODEL_ROUTING_CONDITION_DEPTH}"
        ));
    }
    *nodes += 1;
    if *nodes > MAX_MODEL_ROUTING_CONDITION_NODES {
        return Err(format!(
            "condition nodes exceed {MAX_MODEL_ROUTING_CONDITION_NODES}"
        ));
    }
    let condition = value.as_object().ok_or("condition must be an object")?;
    let branches = ["all", "any", "not"]
        .iter()
        .filter(|key| condition.get(**key).is_some_and(|v| !v.is_null()))
        .count();
    let field = string_field(condition, "field").filter(|field| !field.is_empty());
    let equals = string_field(condition, "equals").filter(|equals| !equals.is_empty());
    let not_equals =
        string_field(condition, "not_equals").filter(|not_equals| !not_equals.is_empty());
    let leaf = field.is_some()
        || present(condition, "in")
        || present(condition, "not_in")
        || equals.is_some()
        || not_equals.is_some()
        || present(condition, "equals_bool")
        || present(condition, "min")
        || present(condition, "max")
        || present(condition, "gt")
        || present(condition, "gte")
        || present(condition, "lt")
        || present(condition, "lte")
        || present(condition, "contains_any")
        || present(condition, "not_contains_any")
        || present(condition, "within");
    if branches + usize::from(leaf) != 1 {
        return Err("condition must contain exactly one logical or leaf shape".into());
    }

    if let Some(name) = ["all", "any"]
        .into_iter()
        .find(|name| condition.get(*name).is_some_and(|v| !v.is_null()))
    {
        let children = condition[name]
            .as_array()
            .filter(|children| (1..=MAX_MODEL_ROUTING_LIST_ITEMS).contains(&children.len()))
            .ok_or_else(|| {
                format!("{name} must contain 1 to {MAX_MODEL_ROUTING_LIST_ITEMS} conditions")
            })?;
        for (index, child) in children.iter().enumerate() {
            validate_routing_condition(child, depth + 1, nodes, task_type_labels, known_dimensions)
                .map_err(|error| format!("{name}[{index}]: {error}"))?;
        }
        return Ok(());
    }
    if present(condition, "not") {
        return validate_routing_condition(
            &condition["not"],
            depth + 1,
            nodes,
            task_type_labels,
            known_dimensions,
        );
    }

    let field = field.ok_or("condition must name a routing field")?;
    let kind = routing_field_kind(field, known_dimensions)
        .ok_or_else(|| format!("unsupported_routing_field: {field}"))?;

    let infix = present(condition, "in");
    let not_in_present = present(condition, "not_in");
    let equals_present = equals.is_some();
    let not_equals_present = not_equals.is_some();
    let bool_present = present(condition, "equals_bool");
    // min, max and the gt/gte/lt/lte bounds share one operator slot: a number
    // leaf is a single range built from at most one lower and one upper bound.
    let range_present = present(condition, "min")
        || present(condition, "max")
        || present(condition, "gt")
        || present(condition, "gte")
        || present(condition, "lt")
        || present(condition, "lte");
    let keywords_present = present(condition, "contains_any");
    let not_keywords_present = present(condition, "not_contains_any");
    let within_present = present(condition, "within");
    if [
        infix,
        not_in_present,
        equals_present,
        not_equals_present,
        bool_present,
        range_present,
        keywords_present,
        not_keywords_present,
        within_present,
    ]
    .iter()
    .filter(|present| **present)
    .count()
        != 1
    {
        return Err(format!(
            "field {field:?} must contain exactly one supported operator"
        ));
    }
    if within_present && kind != "time" {
        return Err(format!("field {field:?} does not support within"));
    }

    match kind {
        "string" => {
            if bool_present || range_present || keywords_present || not_keywords_present {
                return Err(format!("field {field:?} does not support that operator"));
            }
            let mut values: Vec<&str> = Vec::new();
            for (key, listed) in [("in", infix), ("not_in", not_in_present)] {
                if !listed {
                    continue;
                }
                let list = condition[key]
                    .as_array()
                    .filter(|list| (1..=MAX_MODEL_ROUTING_LIST_ITEMS).contains(&list.len()));
                let list = list.ok_or_else(|| {
                    format!(
                        "field {field:?} {key} must contain 1 to {MAX_MODEL_ROUTING_LIST_ITEMS} values"
                    )
                })?;
                for item in list {
                    let item = item
                        .as_str()
                        .ok_or_else(|| format!("{field} values must be strings"))?;
                    routing_value(field, item)?;
                    values.push(item);
                }
            }
            for scalar in [equals, not_equals].into_iter().flatten() {
                routing_value(field, scalar)?;
                values.push(scalar);
            }
            for value in values {
                match field {
                    "protocol" if !value.is_empty() && !valid_protocol_id(value) => {
                        return Err(format!("protocol {value:?} is invalid"));
                    }
                    "task_type" if !task_type_labels.iter().any(|label| label == value) => {
                        return Err(format!(
                            "task_type {value:?} is invalid; allowed labels: {}",
                            task_type_labels.join(", ")
                        ));
                    }
                    "media_kind"
                        if !matches!(
                            value,
                            "none" | "image" | "audio" | "video" | "mixed" | "unknown"
                        ) =>
                    {
                        return Err(format!("media_kind {value:?} is invalid"));
                    }
                    _ => {}
                }
            }
        }
        "bool" => {
            if !bool_present {
                return Err(format!("field {field:?} requires equals_bool"));
            }
        }
        "number" => {
            if !range_present {
                return Err(format!("field {field:?} requires a numeric bound"));
            }
            // min and gte are the inclusive lower bound, max and lte the
            // inclusive upper one; gt and lt make it exclusive. A leaf carries
            // at most one bound per side, however it is spelled.
            let lower_keys = ["min", "gte", "gt"];
            let upper_keys = ["max", "lte", "lt"];
            let mut bounds: Vec<(&str, i64)> = Vec::with_capacity(2);
            for keys in [&lower_keys[..], &upper_keys[..]] {
                let mut present_keys = keys.iter().filter(|key| present(condition, key));
                let Some(key) = present_keys.next() else {
                    continue;
                };
                if present_keys.next().is_some() {
                    return Err(format!(
                        "field {field:?} must not carry more than one {} bound",
                        if keys == lower_keys { "lower" } else { "upper" }
                    ));
                }
                let value = condition[*key]
                    .as_i64()
                    .ok_or_else(|| format!("field {field:?} bounds must be numbers"))?;
                bounds.push((key, value));
            }
            if bounds.iter().any(|(_, value)| *value < 0) {
                return Err(format!("field {field:?} bounds must be non-negative"));
            }
            let lower = bounds
                .iter()
                .find(|(key, _)| lower_keys.contains(key))
                .map(|(_, value)| *value);
            let upper = bounds
                .iter()
                .find(|(key, _)| upper_keys.contains(key))
                .map(|(_, value)| *value);
            if matches!((lower, upper), (Some(lower), Some(upper)) if lower > upper) {
                return Err(format!(
                    "field {field:?} lower bound must not exceed upper bound"
                ));
            }
        }
        "keyword" => {
            if !keywords_present && !not_keywords_present {
                return Err(format!(
                    "literal_keyword must contain 1 to {MAX_MODEL_ROUTING_KEYWORDS} values"
                ));
            }
            for key in ["contains_any", "not_contains_any"] {
                if !present(condition, key) {
                    continue;
                }
                let keywords = condition[key]
                    .as_array()
                    .filter(|list| (1..=MAX_MODEL_ROUTING_KEYWORDS).contains(&list.len()))
                    .ok_or_else(|| {
                        format!(
                            "literal_keyword {key} must contain 1 to {MAX_MODEL_ROUTING_KEYWORDS} values"
                        )
                    })?;
                for keyword in keywords {
                    let keyword = keyword
                        .as_str()
                        .ok_or_else(|| format!("{field} values must be strings"))?;
                    routing_value(field, keyword)?;
                }
            }
        }
        // A time leaf is one half-open [start, end) window: exactly two
        // HH:mm values on system_time. Exclusion composes through the not
        // leaf, not a set operator.
        "time" => {
            if !within_present {
                return Err(format!("field {field:?} requires within"));
            }
            let window = condition["within"]
                .as_array()
                .filter(|list| list.len() == 2)
                .ok_or_else(|| format!("field {field:?} within must contain exactly 2 values"))?;
            for value in window {
                let value = value
                    .as_str()
                    .ok_or_else(|| format!("field {field:?} within values must be strings"))?;
                if !valid_time_of_day(value) {
                    return Err(format!(
                        "field {field:?} within value {value:?} must be HH:mm"
                    ));
                }
            }
        }
        _ => return Err(format!("unsupported_routing_field: {field}")),
    }
    Ok(())
}

/// valid_time_of_day mirrors the Go contract's HH:mm bound: a 00-23 hour and
/// a 00-59 minute; 24:00 is not a wall-clock time.
fn valid_time_of_day(value: &str) -> bool {
    let bytes = value.as_bytes();
    if bytes.len() != 5
        || bytes[2] != b':'
        || !bytes
            .iter()
            .enumerate()
            .all(|(index, byte)| index == 2 || byte.is_ascii_digit())
    {
        return false;
    }
    let hour = (bytes[0] - b'0') * 10 + (bytes[1] - b'0');
    let minute = (bytes[3] - b'0') * 10 + (bytes[4] - b'0');
    hour <= 23 && minute <= 59
}

fn present(fields: &serde_json::Map<String, serde_json::Value>, key: &str) -> bool {
    fields.get(key).is_some_and(|value| !value.is_null())
}

/// routing_value mirrors validateModelRoutingValue's length bound.
fn routing_value(field: &str, value: &str) -> Result<(), String> {
    if value.is_empty() || value.chars().count() > MAX_MODEL_ROUTING_VALUE_RUNES {
        return Err(format!(
            "{field} values must contain 1 to {MAX_MODEL_ROUTING_VALUE_RUNES} characters"
        ));
    }
    Ok(())
}

/// valid_protocol_id mirrors ProtocolID.Valid's pattern.
fn valid_protocol_id(value: &str) -> bool {
    let mut segments = value.split(['.', '_', '-']);
    let Some(head) = segments.next() else {
        return false;
    };
    if !(3..=96).contains(&value.len()) || head.is_empty() {
        return false;
    }
    if !head
        .bytes()
        .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit())
        || !head.starts_with(|c: char| c.is_ascii_lowercase())
    {
        return false;
    }
    segments.all(|segment| {
        !segment.is_empty()
            && segment
                .bytes()
                .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit())
    })
}

/// classifier_dimension_names reads the declared dimension names out of a
/// classifier-settings document, so a caller that has already fetched that
/// document can hand them to `validate_routing_settings`.
///
/// It is deliberately a pure, total extraction over untrusted shape: anything
/// that is not a `dimensions` array of objects with a non-empty string `name`
/// contributes nothing, and a malformed document yields a shorter list rather
/// than an error. The alternative — refusing to read a document the host only
/// half-validates — would let a classifier-settings problem reject a routing
/// document, which is the one outcome this path must never produce.
pub(crate) fn classifier_dimension_names(value: &serde_json::Value) -> Vec<String> {
    value
        .get("dimensions")
        .and_then(serde_json::Value::as_array)
        .map(|dimensions| {
            dimensions
                .iter()
                .filter_map(|dimension| {
                    let name = dimension.get("name")?.as_str()?;
                    (!name.is_empty()).then(|| name.to_string())
                })
                .collect()
        })
        .unwrap_or_default()
}

/// validate_routing_settings validates the routing-settings document.
///
/// `known_dimensions` is the one input this document cannot supply for itself:
/// the user-defined classifier dimensions live in the classifier-settings
/// singleton (see core/contract/classifier_settings.go), a deliberately
/// separate resource, because the routing-settings document crosses a strict
/// Go/Rust field whitelist that must not grow for this feature. The caller is
/// therefore responsible for fetching that resource and passing the names in.
///
/// An empty slice is the correct and safe value whenever the names are
/// unavailable: it restores exactly the built-in-only table this validator
/// used before dimensions existed, so a missing dimension list can only make
/// the validator behave as it always has, never stricter than Core.
pub(crate) fn validate_routing_settings(
    value: &serde_json::Value,
    patch: bool,
    known_dimensions: &[String],
) -> Result<(), String> {
    let object = value
        .as_object()
        .ok_or("routing settings must be an object")?;
    if object.is_empty()
        || (!patch
            && [
                "default_failure_policy",
                "allow_unmatched_failover",
                "strategy",
                "max_attempts",
            ]
            .iter()
            .any(|key| !object.contains_key(*key)))
    {
        return Err("missing routing settings fields".into());
    }
    for (key, value) in object {
        match key.as_str() {
            "default_recovery_paths" => {
                for (protocol, id) in value.as_object().ok_or("invalid default paths")? {
                    crate::recovery_path::protocol(&serde_json::Value::String(protocol.clone()))?;
                    crate::recovery_path::id(id)?;
                }
            }
            "channel_stickiness" => {
                let settings = value
                    .as_object()
                    .ok_or("invalid API provider reuse settings")?;
                if settings.len() != 2
                    || !settings
                        .get("enabled")
                        .is_some_and(serde_json::Value::is_boolean)
                    || !settings
                        .get("ttl_seconds")
                        .and_then(serde_json::Value::as_u64)
                        .is_some_and(|ttl| (60..=86400).contains(&ttl))
                {
                    return Err("invalid API provider reuse settings".into());
                }
            }
            "builtin_tools" => validate_builtin_tools(value)?,
            "image_compression" => validate_image_compression(value)?,
            "model_redirects" => validate_model_redirects(value)?,
            "model_routing_groups" => validate_model_routing_groups(
                value,
                object.get("model_redirects"),
                known_dimensions,
            )?,
            "default_failure_policy" => validate_failure_policy(value)?,
            "allow_unmatched_failover"
            | "codex_identity_enforcement"
            | "claude_identity_enforcement"
            | "grok_identity_enforcement"
            | "official_client_passthrough"
            | "subscription_risk_protection"
            | "codex_request_normalization"
            | "claude_request_normalization"
            | "subscription_session_isolation"
            | "claude_identity_auto_learn"
            | "codex_identity_auto_learn"
            | "grok_identity_auto_learn"
            | "system_prompt_enabled"
                if value.is_boolean() => {}
            "claude_identity_version" | "codex_identity_version" | "grok_identity_version" => {
                validate_identity_version(value)?
            }
            "system_prompt_prefix" | "system_prompt_suffix" => validate_system_prompt(value)?,
            "reasoning_mode" => validate_reasoning_mode(value)?,
            "reasoning_level" | "adaptive_min" | "adaptive_max" => validate_reasoning_level(value)?,
            "adaptive_preset" => validate_adaptive_preset(value)?,
            "strategy" => validate_strategy(value)?,
            "max_attempts" => validate_attempts(value)?,
            _ => return Err("invalid routing settings field".into()),
        }
    }
    validate_adaptive_range(object)
}

/// Mirrors contract.ImageCompression: every field is required.
fn validate_image_compression(value: &serde_json::Value) -> Result<(), String> {
    let settings = value
        .as_object()
        .ok_or("invalid image compression settings")?;
    if settings.len() != 3
        || !settings
            .get("enabled")
            .is_some_and(serde_json::Value::is_boolean)
        || !matches!(
            settings.get("mode").and_then(serde_json::Value::as_str),
            Some("lossless" | "lossy")
        )
        || !settings
            .get("quality")
            .and_then(serde_json::Value::as_u64)
            .is_some_and(|quality| (1..=100).contains(&quality))
    {
        return Err("invalid image compression settings".into());
    }
    Ok(())
}

// validate_identity_version checks the shape of a client version override; an
// empty string clears it. The core applies the full version rules.
fn validate_identity_version(value: &serde_json::Value) -> Result<(), String> {
    let version = value.as_str().ok_or("invalid client identity version")?;
    if version.is_empty() {
        return Ok(());
    }
    if version.len() > 64
        || !version.starts_with(|c: char| c.is_ascii_digit())
        || !version
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-' | '+'))
    {
        return Err("invalid client identity version".into());
    }
    Ok(())
}

// validate_system_prompt mirrors the core's limit on the global prompt prefix
// and suffix, counted in code points like Go runes.
fn validate_system_prompt(value: &serde_json::Value) -> Result<(), String> {
    match value.as_str() {
        Some(text) if text.chars().count() <= MAX_SYSTEM_PROMPT_CHARS => Ok(()),
        _ => Err("global prompt text must be at most 20000 characters".into()),
    }
}

// validate_reasoning_mode accepts how the core sets thinking: passthrough
// leaves it to each client, fixed sends every request at the level, and
// adaptive moves inside the preset's range as requests grow.
fn validate_reasoning_mode(value: &serde_json::Value) -> Result<(), String> {
    match value.as_str() {
        Some("passthrough" | "fixed" | "adaptive") => Ok(()),
        _ => Err("invalid reasoning mode".into()),
    }
}

const REASONING_LEVELS: [&str; 5] = ["off", "low", "medium", "high", "max"];

// validate_reasoning_level accepts the thinking levels the fixed mode sends
// and the custom adaptive range takes for its ends.
fn validate_reasoning_level(value: &serde_json::Value) -> Result<(), String> {
    match value.as_str() {
        Some(level) if REASONING_LEVELS.contains(&level) => Ok(()),
        _ => Err("invalid reasoning level".into()),
    }
}

// validate_adaptive_preset accepts the adaptive ranges: everyday is low through
// max, research medium through max, and custom adaptive_min through
// adaptive_max.
fn validate_adaptive_preset(value: &serde_json::Value) -> Result<(), String> {
    match value.as_str() {
        Some("everyday" | "research" | "custom") => Ok(()),
        _ => Err("invalid adaptive preset".into()),
    }
}

// validate_adaptive_range checks the custom range when the object carries both
// ends. A patch with one end is checked by the core against the stored other
// end.
fn validate_adaptive_range(
    object: &serde_json::Map<String, serde_json::Value>,
) -> Result<(), String> {
    let rank = |key: &str| {
        let level = object.get(key)?.as_str()?;
        REASONING_LEVELS.iter().position(|known| *known == level)
    };
    match (rank("adaptive_min"), rank("adaptive_max")) {
        (Some(low), Some(high)) if low > high => {
            Err("the adaptive range must start at or below its upper end".into())
        }
        _ => Ok(()),
    }
}

pub(crate) fn validate_builtin_tools(value: &serde_json::Value) -> Result<(), String> {
    let tools = value.as_object().ok_or("invalid builtin tools")?;
    if tools.len() != 2
        || !tools.contains_key("web_search")
        || !tools.contains_key("image_generation")
    {
        return Err("invalid builtin tool fields".into());
    }
    for (kind, value) in tools {
        validate_builtin_tool(kind, value)?;
    }
    Ok(())
}

pub(crate) fn validate_builtin_tool(kind: &str, value: &serde_json::Value) -> Result<(), String> {
    if !matches!(kind, "web_search" | "image_generation") {
        return Err("invalid builtin tool kind".into());
    }
    let config = value.as_object().ok_or("invalid builtin tool")?;
    if !config
        .get("enabled")
        .is_some_and(serde_json::Value::is_boolean)
    {
        return Err("enabled must be a boolean".into());
    }
    for (key, value) in config {
        if key == "enabled" {
            continue;
        }
        if !matches!(
            key.as_str(),
            "backend" | "service_id" | "model" | "base_url"
        ) || !value.is_string()
        {
            return Err("invalid builtin tool field".into());
        }
    }
    let text = |key: &str| {
        config
            .get(key)
            .and_then(serde_json::Value::as_str)
            .unwrap_or("")
    };
    if !config.contains_key("backend") {
        return Err("backend is required".into());
    }
    if !text("service_id").is_empty() {
        crate::recovery_path::id(&serde_json::Value::String(text("service_id").into()))?;
    }
    let backend = text("backend");
    if !matches!(backend, "" | "upstream" | "external" | "service_images")
        || (backend == "service_images" && kind != "image_generation")
    {
        return Err("invalid builtin backend".into());
    }
    if text("model").chars().count() > 256 || text("model").chars().any(char::is_control) {
        return Err("invalid tool model".into());
    }
    if !text("base_url").is_empty() {
        let url = reqwest::Url::parse(text("base_url")).map_err(|_| "invalid tool API URL")?;
        if text("base_url").len() > 2048
            || !matches!(url.scheme(), "http" | "https")
            || url.host_str().is_none()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.query().is_some()
            || url.fragment().is_some()
        {
            return Err("invalid tool API URL".into());
        }
    }
    if config.get("enabled").and_then(serde_json::Value::as_bool) == Some(true)
        && (backend.is_empty()
            || (matches!(backend, "upstream" | "service_images")
                && (text("service_id").is_empty() || text("model").is_empty()))
            || (backend == "external"
                && (text("base_url").is_empty()
                    || (kind == "image_generation" && text("model").is_empty()))))
    {
        return Err("tool configuration is incomplete".into());
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    fn policy() -> serde_json::Value {
        json!({"max_retries":1,"initial_delay_ms":500,"max_delay_ms":5000,"network_error":"retry_and_failover","response_timeout":"retry_and_failover","http_status":{"401":"failover","429":"retry_and_failover","418":"retry"}})
    }
    #[test]
    fn validates_builtin_tool_settings_without_credentials() {
        let disabled = json!({"enabled":false,"backend":"upstream"});
        let tools = json!({"web_search":{"enabled":true,"backend":"external","base_url":"https://search.example"},"image_generation":disabled});
        assert!(validate_routing_settings(&json!({"builtin_tools":tools}), true, &[]).is_ok());
        for tool in [
            json!({"enabled":false}),
            json!({"enabled":true,"backend":"external"}),
            json!({"enabled":false,"backend":"upstream","secret":"private"}),
            json!({"enabled":false,"backend":"service_images"}),
        ] {
            assert!(validate_builtin_tool("web_search", &tool).is_err());
        }
        let provider_images = json!({"enabled":true,"backend":"service_images","service_id":"newapi_main","model":"gpt-image-1"});
        assert!(validate_builtin_tool("image_generation", &provider_images).is_ok());
        assert!(validate_builtin_tool(
            "image_generation",
            &json!({"enabled":true,"backend":"service_images","service_id":"newapi_main"})
        )
        .is_err());
    }

    #[test]
    fn accepts_complete_global_defaults_and_optional_timeout() {
        let mut value = policy();
        assert!(validate_failure_policy(&value).is_ok());
        value["response_start_timeout_seconds"] = json!(0);
        assert!(validate_failure_policy(&value).is_ok());
        value["thinking_signature_recovery"] = json!(false);
        assert!(validate_failure_policy(&value).is_ok());
        value["openai_reasoning_recovery"] = json!(true);
        assert!(validate_failure_policy(&value).is_ok());
        value["openai_function_output_recovery"] = json!(true);
        assert!(validate_failure_policy(&value).is_ok());
        assert!(validate_routing_settings(&json!({"default_failure_policy":value,"allow_unmatched_failover":false,"strategy":"retry_first","max_attempts":6}),false, &[]).is_ok());
        assert!(
            validate_routing_settings(&json!({"default_failure_policy":policy()}), true, &[])
                .is_ok()
        );
    }
    #[test]
    fn validates_subscription_identity_settings() {
        for key in [
            "codex_identity_enforcement",
            "claude_identity_enforcement",
            "grok_identity_enforcement",
            "official_client_passthrough",
            "subscription_risk_protection",
            "codex_request_normalization",
            "claude_request_normalization",
            "subscription_session_isolation",
            "claude_identity_auto_learn",
            "codex_identity_auto_learn",
            "grok_identity_auto_learn",
        ] {
            for enabled in [true, false] {
                assert!(validate_routing_settings(&json!({key: enabled}), true, &[]).is_ok());
            }
            for invalid in [json!(null), json!("false"), json!(0)] {
                assert!(validate_routing_settings(&json!({key: invalid}), true, &[]).is_err());
            }
        }
        for key in [
            "claude_identity_version",
            "codex_identity_version",
            "grok_identity_version",
        ] {
            for version in ["", "2.1.300", "0.160.0", "2.2.0-beta.1", "1.0.0+build.5"] {
                assert_eq!(
                    validate_routing_settings(&json!({key: version}), true, &[]),
                    Ok(()),
                    "{key} {version}"
                );
            }
            for invalid in [
                json!(null),
                json!(2),
                json!("v2.1.300"),
                json!("claude-cli/2.1.300"),
                json!("2.1.300 (external, cli)"),
                json!("2.1.300\n"),
                json!(format!("1.0.0-{}", "a".repeat(64))),
            ] {
                assert!(
                    validate_routing_settings(&json!({key: invalid}), true, &[]).is_err(),
                    "{key} {invalid}"
                );
            }
        }
    }

    #[test]
    fn loads_complete_routing_settings_with_builtin_tools_and_identity_controls() {
        let settings = json!({
            "default_failure_policy": policy(),
            "allow_unmatched_failover": true,
            "strategy": "failover_only",
            "max_attempts": 6,
            "model_redirects": [],
            "channel_stickiness": {"enabled": true, "ttl_seconds": 3600},
            "codex_identity_enforcement": true,
            "claude_identity_enforcement": true,
            "grok_identity_enforcement": true,
            "official_client_passthrough": false,
            "subscription_risk_protection": true,
            "codex_request_normalization": true,
            "claude_request_normalization": true,
            "subscription_session_isolation": true,
            "claude_identity_auto_learn": true,
            "codex_identity_auto_learn": false,
            "claude_identity_version": "2.1.300",
            "codex_identity_version": "0.160.0",
            "system_prompt_enabled": true,
            "system_prompt_prefix": "Answer in English.\n",
            "system_prompt_suffix": "",
            "reasoning_mode": "adaptive",
            "reasoning_level": "max",
            "image_compression": {"enabled": true, "mode": "lossy", "quality": 80},
            "builtin_tools": {
                "web_search": {"enabled": true, "backend": "upstream", "service_id": "service_test", "model": "search-model"},
                "image_generation": {"enabled": false, "backend": "upstream"}
            }
        });
        assert_eq!(validate_routing_settings(&settings, false, &[]), Ok(()));
    }
    #[test]
    fn validates_global_prompt_and_thinking_settings() {
        let longest = "提".repeat(20000);
        let mut valid = vec![
            json!({"system_prompt_enabled": true}),
            json!({"system_prompt_enabled": false}),
            json!({"system_prompt_prefix": ""}),
            json!({"system_prompt_prefix": "  Answer in English.\n\nBe brief.  "}),
            json!({"system_prompt_suffix": longest}),
        ];
        let modes = ["passthrough", "fixed", "adaptive"];
        let levels = ["off", "low", "medium", "high", "max"];
        valid.extend(modes.map(|mode| json!({ "reasoning_mode": mode })));
        valid.extend(levels.map(|level| json!({ "reasoning_level": level })));
        for mode in modes {
            for level in levels {
                valid.push(json!({"reasoning_mode": mode, "reasoning_level": level}));
            }
        }
        valid.extend(
            ["everyday", "research", "custom"].map(|preset| json!({ "adaptive_preset": preset })),
        );
        for (index, low) in levels.iter().enumerate() {
            // One end alone is checked by the core against the stored other end.
            valid.push(json!({ "adaptive_min": low }));
            valid.push(json!({ "adaptive_max": low }));
            for high in &levels[index..] {
                valid.push(json!({"adaptive_min": low, "adaptive_max": high}));
            }
        }
        for patch in valid {
            assert_eq!(
                validate_routing_settings(&patch, true, &[]),
                Ok(()),
                "{patch}"
            );
        }
        for invalid in [
            json!({"system_prompt_enabled": null}),
            json!({"system_prompt_enabled": "true"}),
            json!({"system_prompt_enabled": 1}),
            json!({"system_prompt_prefix": null}),
            json!({"system_prompt_prefix": 1}),
            json!({"system_prompt_prefix": ["text"]}),
            json!({"system_prompt_suffix": "提".repeat(20001)}),
            json!({"reasoning_mode": ""}),
            json!({"reasoning_mode": null}),
            json!({"reasoning_mode": "Fixed"}),
            json!({"reasoning_mode": "manual"}),
            // The modes were renamed before release.
            json!({"reasoning_mode": "follow"}),
            json!({"reasoning_mode": "auto"}),
            json!({"reasoning_mode": 1}),
            json!({"reasoning_mode": ["adaptive"]}),
            json!({"reasoning_level": ""}),
            json!({"reasoning_level": null}),
            json!({"reasoning_level": "High"}),
            json!({"reasoning_level": "xhigh"}),
            json!({"reasoning_level": "minimal"}),
            json!({"reasoning_level": "none"}),
            json!({"reasoning_level": 3}),
            json!({"reasoning_level": false}),
            json!({"reasoning_mode": "fixed", "reasoning_level": "auto"}),
            // The single override key was replaced by the mode and level.
            json!({"reasoning_override": ""}),
            json!({"reasoning_override": "high"}),
            json!({"adaptive_preset": ""}),
            json!({"adaptive_preset": null}),
            json!({"adaptive_preset": "daily"}),
            json!({"adaptive_preset": "Everyday"}),
            json!({"adaptive_preset": 1}),
            json!({"adaptive_min": "xhigh"}),
            json!({"adaptive_min": null}),
            json!({"adaptive_max": "none"}),
            json!({"adaptive_max": ""}),
            json!({"adaptive_min": "low", "adaptive_max": "off"}),
            json!({"adaptive_min": "max", "adaptive_max": "high"}),
            json!({"adaptive_preset": "everyday", "adaptive_min": "high", "adaptive_max": "medium"}),
        ] {
            assert!(
                validate_routing_settings(&invalid, true, &[]).is_err(),
                "{invalid}"
            );
        }
        let mut settings = json!({
            "default_failure_policy": policy(),
            "allow_unmatched_failover": false,
            "strategy": "retry_first",
            "max_attempts": 6,
        });
        // Every key above is optional in a full document too.
        assert_eq!(validate_routing_settings(&settings, false, &[]), Ok(()));
        settings["reasoning_mode"] = json!("fixed");
        settings["reasoning_level"] = json!("off");
        assert_eq!(validate_routing_settings(&settings, false, &[]), Ok(()));
        settings["adaptive_preset"] = json!("custom");
        settings["adaptive_min"] = json!("medium");
        settings["adaptive_max"] = json!("medium");
        assert_eq!(validate_routing_settings(&settings, false, &[]), Ok(()));
        settings["adaptive_min"] = json!("high");
        assert!(validate_routing_settings(&settings, false, &[]).is_err());
        settings["adaptive_min"] = json!("low");
        settings["reasoning_override"] = json!("");
        assert!(validate_routing_settings(&settings, false, &[]).is_err());
        settings
            .as_object_mut()
            .unwrap()
            .remove("reasoning_override");
        settings["system_prompt_prefix"] = json!(format!("{}x", "提".repeat(20000)));
        assert!(validate_routing_settings(&settings, false, &[]).is_err());
    }
    #[test]
    fn validates_image_compression_settings() {
        for valid in [
            json!({"enabled": false, "mode": "lossless", "quality": 85}),
            json!({"enabled": true, "mode": "lossy", "quality": 1}),
            json!({"enabled": true, "mode": "lossy", "quality": 100}),
        ] {
            assert_eq!(
                validate_routing_settings(&json!({ "image_compression": valid }), true, &[]),
                Ok(()),
                "{valid}"
            );
        }
        for invalid in [
            json!(null),
            json!(true),
            json!({"enabled": true}),
            json!({"enabled": true, "mode": "lossless"}),
            json!({"enabled": "true", "mode": "lossless", "quality": 85}),
            json!({"enabled": true, "mode": "webp", "quality": 85}),
            json!({"enabled": true, "mode": "lossy", "quality": 0}),
            json!({"enabled": true, "mode": "lossy", "quality": 101}),
            json!({"enabled": true, "mode": "lossy", "quality": 80.5}),
            json!({"enabled": true, "mode": "lossy", "quality": 80, "max_side": 2048}),
        ] {
            assert!(
                validate_routing_settings(&json!({ "image_compression": invalid }), true, &[])
                    .is_err(),
                "{invalid}"
            );
        }
    }
    fn redirect(from: &str, to: &str) -> serde_json::Value {
        json!({"from":from,"to":to,"enabled":true})
    }
    fn redirects(rules: serde_json::Value) -> Result<(), String> {
        validate_routing_settings(&json!({ "model_redirects": rules }), true, &[])
    }
    #[test]
    fn accepts_valid_model_redirects() {
        let long = "模".repeat(256);
        let many: Vec<_> = (0..200)
            .map(|index| redirect(&format!("client-{index}"), &format!("upstream-{index}")))
            .collect();
        for rules in [
            json!([]),
            json!([redirect("gpt-4o", "gpt-5")]),
            json!([
                redirect("astrlink/auto", "gpt-5"),
                {"from":"Claude","to":"claude-sonnet","enabled":false},
                redirect("claude", "claude-sonnet"),
                redirect("tab\tinside", &long),
            ]),
            json!(many),
        ] {
            assert!(redirects(rules.clone()).is_ok(), "{rules}");
        }
        assert!(validate_routing_settings(&json!({"model_redirects":[redirect("a","b")],"default_failure_policy":policy(),"allow_unmatched_failover":false,"strategy":"retry_first","max_attempts":6}),false, &[]).is_ok());
    }
    #[test]
    fn rejects_invalid_model_redirects() {
        let many: Vec<_> = (0..201)
            .map(|index| redirect(&format!("client-{index}"), &format!("upstream-{index}")))
            .collect();
        let long = "m".repeat(257);
        for rules in [
            json!(null),
            json!({}),
            json!("gpt-4o"),
            json!(many),
            json!(["gpt-4o"]),
            json!([null]),
            json!([{"from":"a","to":"b"}]),
            json!([{"from":"a","enabled":true}]),
            json!([{"from":"a","to":"b","enabled":true,"note":"x"}]),
            json!([{"from":"a","to":"b","enabled":"true"}]),
            json!([{"from":"a","to":"b","enabled":null}]),
            json!([{"from":1,"to":"b","enabled":true}]),
            json!([{"from":"a","to":null,"enabled":true}]),
            json!([redirect("", "b")]),
            json!([redirect("a", "")]),
            json!([redirect(&long, "b")]),
            json!([redirect("a", &long)]),
            json!([redirect(" a", "b")]),
            json!([redirect("a", "b\t")]),
            json!([redirect("a", "b\u{3000}")]),
            json!([redirect("a\nb", "c")]),
            json!([redirect("a", "b\u{0}c")]),
            json!([redirect("a", "a")]),
            json!([redirect("a", "astrlink/auto")]),
            json!([redirect("a", "b"), redirect("a", "c")]),
            json!([redirect("a", "b"), redirect("b", "c")]),
            json!([redirect("b", "c"), {"from":"a","to":"b","enabled":false}]),
        ] {
            assert!(redirects(rules.clone()).is_err(), "{rules}");
        }
    }
    #[test]
    fn rejects_incomplete_null_and_out_of_range_policies() {
        for (key, invalid) in [
            ("max_retries", json!(6)),
            ("initial_delay_ms", json!(-1)),
            ("max_delay_ms", json!(100)),
            ("response_start_timeout_seconds", json!(null)),
            ("thinking_signature_recovery", json!(null)),
            ("thinking_signature_recovery", json!("true")),
            ("openai_reasoning_recovery", json!(null)),
            ("openai_reasoning_recovery", json!("true")),
            ("openai_function_output_recovery", json!(null)),
            ("openai_function_output_recovery", json!("true")),
            ("network_error", json!("ignore")),
            ("http_status", json!({"200":"retry"})),
        ] {
            let mut value = policy();
            value[key] = invalid;
            assert!(validate_failure_policy(&value).is_err(), "{key}");
        }
        for key in [
            "max_retries",
            "initial_delay_ms",
            "max_delay_ms",
            "network_error",
            "response_timeout",
            "http_status",
        ] {
            let mut value = policy();
            value.as_object_mut().unwrap().remove(key);
            assert!(validate_failure_policy(&value).is_err(), "{key}");
        }
        assert!(validate_routing_settings(&json!({"max_attempts":21}), true, &[]).is_err());
        assert!(validate_routing_settings(&json!({"default_failure_policy":policy(),"allow_unmatched_failover":false,"strategy":"retry_first"}),false, &[]).is_err());
    }

    fn groups(value: serde_json::Value) -> Result<(), String> {
        groups_with_dimensions(value, &[])
    }

    /// groups_with_dimensions drives the same document through the same public
    /// entry, with the classifier dimension list the validator cannot read out
    /// of the routing-settings document itself.
    fn groups_with_dimensions(
        value: serde_json::Value,
        known_dimensions: &[String],
    ) -> Result<(), String> {
        validate_routing_settings(
            &json!({ "model_routing_groups": value }),
            true,
            known_dimensions,
        )
    }

    fn group(members: serde_json::Value, default_member_id: &str) -> serde_json::Value {
        json!({
            "id": "coding",
            "enabled": true,
            "members": members,
            "default_member_id": default_member_id,
            "rules": [],
        })
    }

    fn member(id: &str, service_id: &str, model: &str) -> serde_json::Value {
        json!({ "id": id, "service_id": service_id, "model": model })
    }

    /// Core always emits model_routing_groups, including the empty default, so
    /// the host must accept every shape the contract produces.
    #[test]
    fn accepts_model_routing_groups_core_emits() {
        assert_eq!(groups(json!([])), Ok(()));
        assert_eq!(
            validate_routing_settings(
                &json!({
                    "model_redirects": [redirect("claude-3-5-sonnet", "claude-sonnet-5")],
                    "model_routing_groups": [
                        group(
                            json!([
                                member("fast", "service_openai", "gpt-5.6-luna"),
                                member("deep", "service_anthropic", "claude-opus-5-5"),
                            ]),
                            "deep",
                        )
                    ],
                }),
                true,
                &[],
            ),
            Ok(())
        );
    }

    #[test]
    fn attributes_a_routing_group_failure_to_its_field() {
        // A group shape the contract refuses must fail the whole document; the
        // host must not silently drop it and let Core reject the write later.
        let one_member = || json!([member("fast", "service_openai", "gpt-5.6-luna")]);

        let mut reserved_id = group(one_member(), "fast");
        reserved_id["id"] = json!("auto");

        let mut unknown_group_field = group(one_member(), "fast");
        unknown_group_field["unexpected"] = json!(true);

        let with_rule = |when: serde_json::Value, target: &str| {
            let mut value = group(one_member(), "fast");
            value["rules"] = json!([{
                "id": "r",
                "priority": 0,
                "when": when,
                "target_member_id": target,
            }]);
            value
        };

        for invalid in [
            // default_member_id names no member.
            json!([group(one_member(), "missing")]),
            // member model is a reserved AstrLink ID.
            json!([group(
                json!([member("fast", "service_openai", "astrlink/auto")]),
                "fast"
            )]),
            // service_id is too short to be a resource ID.
            json!([group(json!([member("fast", "sv", "m")]), "fast")]),
            // member id breaks the slug rule.
            json!([group(
                json!([member("Fast", "service_openai", "m")]),
                "Fast"
            )]),
            // group id is reserved.
            json!([reserved_id]),
            // member ids repeat within a group.
            json!([group(
                json!([
                    member("fast", "service_openai", "m"),
                    member("fast", "service_openai", "n")
                ]),
                "fast",
            )]),
            // two groups share one id.
            json!([
                group(one_member(), "fast"),
                group(json!([member("fast", "service_openai", "n")]), "fast"),
            ]),
            // a field the contract does not route on.
            json!([with_rule(json!({"field": "intent", "equals": "x"}), "fast")]),
            // a rule targeting a member that does not exist.
            json!([with_rule(
                json!({"field": "protocol", "equals": "x"}),
                "gone"
            )]),
            // an unknown key inside a group.
            json!([unknown_group_field]),
        ] {
            assert!(groups(invalid.clone()).is_err(), "{invalid}");
        }
    }

    /// rules builds one group whose single rule carries `when`, so a condition
    /// can be validated through the same path Core and the host share.
    fn rules(when: serde_json::Value) -> serde_json::Value {
        let mut value = group(
            json!([member("fast", "service_openai", "gpt-5.6-luna")]),
            "fast",
        );
        value["rules"] = json!([{
            "id": "r",
            "priority": 0,
            "when": when,
            "target_member_id": "fast",
        }]);
        value
    }

    fn condition(when: serde_json::Value) -> Result<(), String> {
        groups(json!([rules(when)]))
    }

    /// condition_with_dimensions is `condition` against a declared dimension
    /// set, so a rule may name a custom dimension.
    fn condition_with_dimensions(
        when: serde_json::Value,
        known_dimensions: &[String],
    ) -> Result<(), String> {
        groups_with_dimensions(json!([rules(when)]), known_dimensions)
    }

    #[test]
    fn accepts_extended_routing_condition_operators() {
        for when in [
            // String kind: in / not_in / equals / not_equals.
            json!({"field": "protocol", "not_in": ["openai_chat", "anthropic_messages"]}),
            json!({"field": "protocol", "not_equals": "openai_chat"}),
            json!({"field": "task_type", "not_in": ["research", "architect"]}),
            json!({"field": "task_type", "not_equals": "general"}),
            json!({"field": "media_kind", "not_equals": "none"}),
            // Number kind: single bounds, paired bounds, and cross-family pairs.
            json!({"field": "turn_count", "gt": 3}),
            json!({"field": "turn_count", "gte": 3}),
            json!({"field": "turn_count", "lt": 3}),
            json!({"field": "turn_count", "lte": 3}),
            json!({"field": "input_bytes", "gt": 0, "lt": 1024}),
            json!({"field": "input_bytes", "gte": 5, "lte": 10}),
            json!({"field": "input_bytes", "gte": 5, "lt": 10}),
            json!({"field": "input_bytes", "gt": 5, "lte": 10}),
            // Legacy bounds keep working, alone and beside a new bound.
            json!({"field": "turn_count", "min": 2}),
            json!({"field": "turn_count", "max": 2}),
            json!({"field": "turn_count", "min": 2, "max": 4}),
            json!({"field": "turn_count", "min": 2, "lte": 4}),
            json!({"field": "turn_count", "gte": 2, "max": 4}),
            // Keyword kind: contains_any / not_contains_any.
            json!({"field": "literal_keyword", "contains_any": ["x"]}),
            json!({"field": "literal_keyword", "not_contains_any": ["x", "y"]}),
            // The additions compose inside a tree.
            json!({"all": [
                {"field": "protocol", "not_equals": "openai_chat"},
                {"field": "turn_count", "gte": 2, "lte": 8},
            ]}),
        ] {
            assert!(condition(when.clone()).is_ok(), "{when}");
        }
    }

    #[test]
    fn rejects_a_routing_operator_for_the_wrong_field_kind() {
        for when in [
            json!({"field": "protocol", "gt": 1}),
            json!({"field": "protocol", "lte": 1}),
            json!({"field": "protocol", "not_contains_any": ["x"]}),
            json!({"field": "streaming", "not_in": ["true"]}),
            json!({"field": "streaming", "not_equals": "true"}),
            json!({"field": "streaming", "gt": 1}),
            json!({"field": "streaming", "not_contains_any": ["x"]}),
            json!({"field": "turn_count", "not_in": ["1"]}),
            json!({"field": "turn_count", "not_equals": "1"}),
            json!({"field": "turn_count", "not_contains_any": ["x"]}),
            json!({"field": "literal_keyword", "equals": "x"}),
            json!({"field": "literal_keyword", "not_equals": "x"}),
            json!({"field": "literal_keyword", "not_in": ["x"]}),
            json!({"field": "literal_keyword", "gte": 1}),
            // Two operators of the same kind on one leaf.
            json!({"field": "protocol", "in": ["a"], "not_in": ["b"]}),
            json!({"field": "protocol", "equals": "a", "not_equals": "b"}),
            json!({"field": "literal_keyword", "contains_any": ["a"], "not_contains_any": ["b"]}),
        ] {
            assert!(condition(when.clone()).is_err(), "{when}");
        }
    }

    /// The time leaf mirrors the Go contract: exactly two HH:mm values on
    /// system_time only, with exclusion through the not leaf.
    #[test]
    fn accepts_and_bounds_the_time_leaf() {
        for when in [
            json!({"field": "system_time", "within": ["09:00", "12:00"]}),
            json!({"field": "system_time", "within": ["00:00", "23:59"]}),
            // Equal endpoints name a single instant; a start above the end
            // crosses midnight.
            json!({"field": "system_time", "within": ["09:00", "09:00"]}),
            json!({"field": "system_time", "within": ["22:00", "06:00"]}),
            json!({"not": {"field": "system_time", "within": ["09:00", "12:00"]}}),
            json!({"all": [
                {"field": "system_time", "within": ["09:00", "12:00"]},
                {"field": "task_type", "in": ["coding"]},
            ]}),
        ] {
            assert!(condition(when.clone()).is_ok(), "{when}");
        }
        for when in [
            // Arity: the window is exactly two values.
            json!({"field": "system_time", "within": ["09:00"]}),
            json!({"field": "system_time", "within": ["09:00", "12:00", "18:00"]}),
            json!({"field": "system_time", "within": []}),
            // Format and range.
            json!({"field": "system_time", "within": ["9:00", "12:00"]}),
            json!({"field": "system_time", "within": ["09:00", "12:0"]}),
            json!({"field": "system_time", "within": ["09-00", "12:00"]}),
            json!({"field": "system_time", "within": ["0900", "12:00"]}),
            json!({"field": "system_time", "within": ["24:00", "12:00"]}),
            json!({"field": "system_time", "within": ["09:60", "12:00"]}),
            // within belongs to system_time only.
            json!({"field": "protocol", "within": ["09:00", "12:00"]}),
            json!({"field": "turn_count", "within": ["09:00", "12:00"]}),
            json!({"field": "literal_keyword", "within": ["09:00", "12:00"]}),
            // system_time takes no other operator, alone or combined.
            json!({"field": "system_time"}),
            json!({"field": "system_time", "equals": "09:00"}),
            json!({"field": "system_time", "in": ["09:00"]}),
            json!({"field": "system_time", "within": ["09:00", "12:00"], "equals": "09:00"}),
        ] {
            assert!(condition(when.clone()).is_err(), "{when}");
        }
    }

    #[test]
    fn rejects_malformed_routing_bounds() {
        for when in [
            json!({"field": "turn_count", "gt": -1}),
            json!({"field": "turn_count", "gte": -1}),
            json!({"field": "turn_count", "lt": -1}),
            json!({"field": "turn_count", "lte": -1}),
            json!({"field": "turn_count", "min": -1}),
            json!({"field": "turn_count", "max": -1}),
            // More than one bound per side, however it is spelled.
            json!({"field": "turn_count", "gt": 1, "gte": 2}),
            json!({"field": "turn_count", "lt": 1, "lte": 2}),
            json!({"field": "turn_count", "min": 1, "gte": 2}),
            json!({"field": "turn_count", "max": 1, "lt": 2}),
            // A lower bound above its upper bound, on one side or across families.
            json!({"field": "turn_count", "min": 10, "max": 5}),
            json!({"field": "turn_count", "gte": 10, "lte": 5}),
            json!({"field": "turn_count", "gt": 10, "lt": 5}),
            json!({"field": "turn_count", "gte": 6, "lt": 5}),
            json!({"field": "turn_count", "min": 10, "lte": 5}),
            json!({"field": "turn_count", "gte": 10, "max": 5}),
            // No bound at all, or a non-integer bound.
            json!({"field": "turn_count"}),
            json!({"field": "turn_count", "gte": "5"}),
        ] {
            assert!(condition(when.clone()).is_err(), "{when}");
        }
    }

    #[test]
    fn bounds_the_negated_keyword_list() {
        let keywords: Vec<_> = (0..MAX_MODEL_ROUTING_KEYWORDS)
            .map(|index| format!("k{index}"))
            .collect();
        assert!(
            condition(json!({"field": "literal_keyword", "not_contains_any": keywords})).is_ok()
        );
        let overlong: Vec<_> = (0..=MAX_MODEL_ROUTING_KEYWORDS)
            .map(|index| format!("k{index}"))
            .collect();
        for when in [
            json!({"field": "literal_keyword", "not_contains_any": overlong}),
            json!({"field": "literal_keyword", "not_contains_any": []}),
            json!({"field": "literal_keyword", "not_contains_any": [""]}),
            json!({"field": "protocol", "not_in": []}),
            json!({"field": "task_type", "not_equals": "storyboard"}),
            json!({"field": "media_kind", "not_in": ["hologram"]}),
        ] {
            assert!(condition(when.clone()).is_err(), "{when}");
        }
    }

    #[test]
    fn rejects_group_level_classifier_fields() {
        // The group-scoped classifier keys were removed from the contract; the
        // whitelist in `fields_of` now refuses them like any other unknown key.
        let with_field = |key: &str, value: serde_json::Value| {
            let mut group = group(json!([member("fast", "service_openai", "m")]), "fast");
            group[key] = value;
            groups(json!([group]))
        };
        for (key, value) in [
            ("classifier_prompt", json!("classify the request")),
            ("classifier_output_fmt", json!("keyword_match")),
            ("classifier_json_path", json!("$.category")),
            ("classifier_regex", json!("^code")),
            ("classifier_fallback", json!("general")),
            ("classifier_timeout_ms", json!(2500)),
        ] {
            assert!(with_field(key, value).is_err(), "{key}");
        }
    }

    #[test]
    fn validates_group_scoped_task_type_labels() {
        let mut custom = rules(json!({"field": "task_type", "in": ["custom-review"]}));
        custom["task_type_labels"] = json!(["general", "custom-review"]);
        assert_eq!(groups(json!([custom])), Ok(()));

        let mut invalid = rules(json!({"field": "task_type", "in": ["coding"]}));
        invalid["task_type_labels"] = json!(["custom-review"]);
        let error = groups(json!([invalid])).expect_err("coding must be rejected");
        assert!(error.contains("allowed labels"), "{error}");

        let mut legacy = rules(json!({"field": "task_type", "in": ["coding"]}));
        legacy["task_type_labels"] = json!([]);
        assert_eq!(groups(json!([legacy])), Ok(()));

        let mut duplicate = group(
            json!([member("fast", "service_openai", "gpt-5.6-luna")]),
            "fast",
        );
        duplicate["task_type_labels"] = json!(["custom-review", "custom-review"]);
        assert!(groups(json!([duplicate])).is_err());
    }

    /// A redirect and a group must not both claim one model name.
    #[test]
    fn rejects_a_redirect_that_collides_with_a_routing_group() {
        let settings = |redirect_to: &str| {
            json!({
                "model_redirects": [redirect("source-model", redirect_to)],
                "model_routing_groups": [group(json!([member("fast", "service_openai", "gpt-5.6-luna")]), "fast")],
            })
        };
        assert_eq!(
            validate_routing_settings(&settings("other-model"), true, &[]),
            Ok(())
        );
        for colliding in ["gpt-5.6-luna", "astrlink/coding"] {
            assert!(
                validate_routing_settings(&settings(colliding), true, &[]).is_err(),
                "{colliding}"
            );
        }
    }

    /// The 21 built-in field names are a closed frozen table, and every one of
    /// them must keep resolving exactly as it did before dimensions existed:
    /// with no dimensions declared, and with a dimension list that does not
    /// mention them. This is the assertion that would fail if a custom
    /// dimension were ever allowed to shadow, rename or displace a built-in.
    #[test]
    fn every_builtin_routing_field_still_resolves() {
        for field in [
            "protocol",
            "media_kind",
            "task_type",
            "streaming",
            "continuation",
            "has_tools",
            "has_media",
            "has_image",
            "has_audio",
            "has_video",
            "turn_count",
            "user_message_count",
            "assistant_message_count",
            "tool_message_count",
            "tool_declaration_count",
            "tool_call_count",
            "input_bytes",
            "input_text_runes",
            "estimated_input_context_tokens",
            "literal_keyword",
            "system_time",
        ] {
            let kind = routing_field_kind(field, &[]).unwrap_or_else(|| {
                panic!("built-in field {field:?} no longer resolves");
            });
            // The table is frozen at 21 entries across 5 kinds.
            assert!(
                matches!(kind, "string" | "bool" | "number" | "keyword" | "time"),
                "{field}: unexpected kind {kind}"
            );
            // An unrelated dimension list must not change any of them, and in
            // particular must not turn one into a custom string field.
            let dimensions = vec!["brand".to_string()];
            assert_eq!(
                routing_field_kind(field, &dimensions),
                Some(kind),
                "{field}"
            );
            // Every built-in is a real condition field: a leaf naming it is
            // accepted whenever its operator matches its kind. The value is
            // chosen per field because the string kind carries per-field value
            // rules (protocol IDs, media kinds, the group's task_type label
            // domain) that this test is not about.
            let value = match field {
                "protocol" => "openai_chat",
                "media_kind" => "none",
                "task_type" => "coding",
                _ => "x",
            };
            let when = match kind {
                "string" => json!({"field": field, "equals": value}),
                "bool" => json!({"field": field, "equals_bool": true}),
                "number" => json!({"field": field, "gte": 1}),
                "keyword" => json!({"field": field, "contains_any": ["x"]}),
                _ => json!({"field": field, "within": ["09:00", "12:00"]}),
            };
            assert!(condition(when.clone()).is_ok(), "{when}");
            assert!(
                condition_with_dimensions(when.clone(), &dimensions).is_ok(),
                "{when}"
            );
        }
        // The table is exactly the 21 names the whitelist above enumerates.
        let names = [
            "protocol",
            "media_kind",
            "task_type",
            "streaming",
            "continuation",
            "has_tools",
            "has_media",
            "has_image",
            "has_audio",
            "has_video",
            "turn_count",
            "user_message_count",
            "assistant_message_count",
            "tool_message_count",
            "tool_declaration_count",
            "tool_call_count",
            "input_bytes",
            "input_text_runes",
            "estimated_input_context_tokens",
            "literal_keyword",
            "system_time",
        ];
        assert_eq!(names.len(), 21);
        for name in names {
            assert!(routing_field_kind(name, &[]).is_some(), "{name}");
        }
    }

    /// A declared custom dimension becomes a routable string field, and only
    /// while it is declared: the same condition is refused when the dimension
    /// list is empty. That asymmetry is the whole point — it pins dimensions
    /// as runtime context read from the classifier-settings singleton, not as
    /// new built-in names baked into the frozen table.
    #[test]
    fn accepts_a_routing_condition_on_a_declared_dimension() {
        let dimensions = vec!["brand".to_string(), "品牌".to_string()];
        for when in [
            json!({"field": "brand", "equals": "acme"}),
            json!({"field": "brand", "not_equals": "acme"}),
            json!({"field": "brand", "in": ["acme", "other"]}),
            json!({"field": "brand", "not_in": ["acme"]}),
            // A dimension name is free-form: the contract deliberately imposes
            // no character-set rule, so a Chinese name is addressable too.
            json!({"field": "品牌", "equals": "acme"}),
            // A dimension leaf composes inside a tree like any other field.
            json!({"all": [
                {"field": "brand", "equals": "acme"},
                {"field": "task_type", "in": ["coding"]},
            ]}),
            json!({"not": {"field": "brand", "equals": "acme"}}),
        ] {
            assert!(
                condition_with_dimensions(when.clone(), &dimensions).is_ok(),
                "{when}"
            );
            // Without the declaration it is an unsupported field, exactly as
            // it was before this feature existed.
            let error = condition(when.clone()).expect_err("undeclared dimension must be refused");
            assert!(error.contains("unsupported_routing_field"), "{error}");
        }
    }

    /// A custom dimension is string kind and nothing more: it accepts the
    /// string operators and refuses every other kind's operators. It is not a
    /// new field kind that happens to be spelled like a built-in.
    #[test]
    fn a_dimension_takes_only_the_string_operators() {
        let dimensions = vec!["brand".to_string()];
        for when in [
            json!({"field": "brand", "equals_bool": true}),
            json!({"field": "brand", "gte": 1}),
            json!({"field": "brand", "contains_any": ["a"]}),
            json!({"field": "brand", "not_contains_any": ["a"]}),
            json!({"field": "brand", "within": ["09:00", "12:00"]}),
            // Two operators of the same kind, as for any string field.
            json!({"field": "brand", "in": ["a"], "not_in": ["b"]}),
            json!({"field": "brand"}),
        ] {
            assert!(
                condition_with_dimensions(when.clone(), &dimensions).is_err(),
                "{when}"
            );
        }
    }

    /// classifier_dimension_names reads only well-formed names, and treats a
    /// malformed classifier document as "no dimensions" rather than an error:
    /// the routing validator must never reject a valid routing document
    /// because the sibling classifier resource was unreadable.
    #[test]
    fn reads_dimension_names_without_ever_failing() {
        assert_eq!(
            classifier_dimension_names(&json!({
                "dimensions": [
                    {"name": "brand", "options": [{"name": "acme"}]},
                    {"name": "品牌"},
                ],
            })),
            vec!["brand".to_string(), "品牌".to_string()]
        );
        for malformed in [
            json!({}),
            json!({"dimensions": null}),
            json!({"dimensions": "brand"}),
            json!({"dimensions": [{"name": ""}, {"name": 7}, null, "brand"]}),
        ] {
            assert!(
                classifier_dimension_names(&malformed).is_empty(),
                "{malformed}"
            );
        }
    }
    /// 内建字段表的冻结断言。期望值是 Go 侧
    /// `core/contract/model_routing_test.go:TestModelRoutingFieldSetFrozen` 与 TS 侧
    /// `model-routing-group-model.test.ts` 同一份字面量文本的拷贝：三侧任一侧漏改
    /// ⇒ 至少一侧变红。`routing_field_kind` 在本次改动前从未被任何测试触碰过，
    /// 这条测试是它在测试里的第一个调用点。
    #[test]
    fn routing_field_kind_frozen() {
        let frozen: [(&str, &str); 21] = [
            ("protocol", "string"),
            ("media_kind", "string"),
            ("task_type", "string"),
            ("streaming", "bool"),
            ("continuation", "bool"),
            ("has_tools", "bool"),
            ("has_media", "bool"),
            ("has_image", "bool"),
            ("has_audio", "bool"),
            ("has_video", "bool"),
            ("turn_count", "number"),
            ("user_message_count", "number"),
            ("assistant_message_count", "number"),
            ("tool_message_count", "number"),
            ("tool_declaration_count", "number"),
            ("tool_call_count", "number"),
            ("input_bytes", "number"),
            ("input_text_runes", "number"),
            ("estimated_input_context_tokens", "number"),
            ("literal_keyword", "keyword"),
            ("system_time", "time"),
        ];
        let none: [String; 0] = [];
        for (field, kind) in frozen {
            assert_eq!(
                routing_field_kind(field, &none),
                Some(kind),
                "built-in field {field} drifted; update all three mirrors together"
            );
        }
        // Custom dimensions resolve after the frozen table and never enter it.
        let dimensions = vec!["topic".to_string()];
        assert_eq!(routing_field_kind("topic", &dimensions), Some("string"));
        assert_eq!(routing_field_kind("topic", &none), None);
        assert_eq!(routing_field_kind("ghost", &dimensions), None);
        // A dimension cannot shadow a built-in: the table wins first.
        let shadow = vec!["task_type".to_string()];
        assert_eq!(routing_field_kind("task_type", &shadow), Some("string"));
    }
}
