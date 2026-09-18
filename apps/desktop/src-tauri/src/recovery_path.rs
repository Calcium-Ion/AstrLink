use crate::failure_policy::{validate_attempts, validate_failure_policy, validate_strategy};
use serde_json::Value;
use std::collections::{HashMap, HashSet};
fn text(value: &Value, max: usize) -> Result<&str, String> {
    value
        .as_str()
        .filter(|s| s.chars().count() <= max)
        .ok_or("invalid path text".into())
}
pub(crate) fn id(value: &Value) -> Result<(), String> {
    let s = text(value, 96)?;
    if s.len() < 3
        || !s.as_bytes()[0].is_ascii_lowercase()
        || !s
            .bytes()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'_' || c == b'-')
    {
        return Err("invalid path ID".into());
    }
    Ok(())
}
pub(crate) fn protocol(value: &Value) -> Result<(), String> {
    let s = text(value, 96)?;
    if s.len() < 3
        || !s.as_bytes()[0].is_ascii_lowercase()
        || !s.bytes().all(|c| {
            c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'_' || c == b'.' || c == b'-'
        })
    {
        return Err("invalid path protocol".into());
    }
    Ok(())
}
fn keys(value: &Value, allowed: &[&str], required: &[&str]) -> Result<(), String> {
    let o = value.as_object().ok_or("path must be an object")?;
    if o.keys().any(|k| !allowed.contains(&k.as_str()))
        || required.iter().any(|k| !o.contains_key(*k))
    {
        return Err("invalid path fields".into());
    }
    Ok(())
}
pub(crate) fn validate_path(value: &Value, patch: bool, create: bool) -> Result<(), String> {
    keys(
        value,
        &[
            "id",
            "name",
            "protocol",
            "mode",
            "targets",
            "steps",
            "strategy",
            "max_attempts",
            "failure_policy",
        ],
        if patch {
            &[]
        } else {
            &["name", "protocol", "mode"]
        },
    )?;
    let o = value.as_object().ok_or("path must be an object")?;
    if !create && !patch {
        id(&value["id"])?
    } else if o.contains_key("id") {
        return Err("path ID is immutable".into());
    }
    for (key, v) in o {
        if patch
            && v.is_null()
            && [
                "targets",
                "steps",
                "strategy",
                "max_attempts",
                "failure_policy",
            ]
            .contains(&key.as_str())
        {
            continue;
        }
        match key.as_str() {
            "name" => {
                if text(v, 128)?.trim().is_empty() {
                    return Err("path name is required".into());
                }
            }
            "protocol" => protocol(v)?,
            "id" => id(v)?,
            "mode" => {
                if !matches!(v.as_str(), Some("automatic" | "steps")) {
                    return Err("invalid path mode".into());
                }
            }
            "strategy" => validate_strategy(v)?,
            "max_attempts" => validate_attempts(v)?,
            "failure_policy" => validate_failure_policy(v)?,
            "targets" | "steps" => {
                let nodes = v.as_array().ok_or("nodes must be an array")?;
                let manual = key == "steps";
                if nodes.is_empty() || nodes.len() > if manual { 20 } else { 200 } {
                    return Err("invalid node count".into());
                }
                let mut ids = HashSet::new();
                let mut counts = HashMap::new();
                for node in nodes {
                    keys(
                        node,
                        &[
                            "id",
                            "service_id",
                            "upstream_model",
                            "upstream_protocol",
                            "plan_type",
                            "max_retries",
                        ],
                        &["id", "service_id", "upstream_protocol", "plan_type"],
                    )?;
                    id(&node["id"])?;
                    id(&node["service_id"])?;
                    protocol(&node["upstream_protocol"])?;
                    if !ids.insert(node["id"].as_str()) {
                        return Err("duplicate node ID".into());
                    }
                    if !matches!(
                        node["plan_type"].as_str(),
                        Some("native" | "delegated" | "relaykit")
                    ) {
                        return Err("invalid execution type".into());
                    }
                    if let Some(model) = node.get("upstream_model") {
                        text(model, 256)?;
                    }
                    if node["plan_type"] != "relaykit"
                        && o.contains_key("protocol")
                        && node["upstream_protocol"] != value["protocol"]
                    {
                        return Err("path protocol mismatch".into());
                    }
                    if let Some(count) = node.get("max_retries") {
                        if manual || !count.as_u64().is_some_and(|n| n <= 5) {
                            return Err("invalid retry count".into());
                        }
                    }
                    let identity = (
                        node["service_id"].as_str(),
                        node["upstream_model"].as_str().unwrap_or(""),
                        node["upstream_protocol"].as_str(),
                        node["plan_type"].as_str(),
                    );
                    let count = counts.entry(identity).or_insert(0);
                    *count += 1;
                    if *count > if manual { 6 } else { 1 } {
                        return Err("duplicate target or too many repeated steps".into());
                    }
                }
            }
            _ => {}
        }
    }
    if !patch {
        match value["mode"].as_str() {
            Some("steps") => {
                if !o.contains_key("steps")
                    || o.contains_key("targets")
                    || o.contains_key("strategy")
                {
                    return Err("steps cannot have automatic settings".into());
                }
            }
            Some("automatic") => {
                if !o.contains_key("targets") || o.contains_key("steps") {
                    return Err("automatic paths require targets".into());
                }
            }
            _ => return Err("invalid mode".into()),
        }
    }
    Ok(())
}
pub(crate) fn validate_record(value: &Value) -> Result<(), String> {
    keys(
        value,
        &["path", "etag", "references"],
        &["path", "etag", "references"],
    )?;
    validate_path(&value["path"], false, false)?;
    let tag = text(&value["etag"], 80)?;
    if tag.len() != 73
        || !tag.starts_with("\"sha256:")
        || !tag.ends_with('"')
        || !tag[8..72].bytes().all(|c| c.is_ascii_hexdigit())
    {
        return Err("invalid path version".into());
    }
    for r in value["references"]
        .as_array()
        .ok_or("invalid path references")?
    {
        keys(
            r,
            &["route_id", "name", "protocol", "category_id", "override"],
            &["name", "protocol", "override"],
        )?;
        text(&r["name"], 128)?;
        protocol(&r["protocol"])?;
        if !r["override"].is_boolean() {
            return Err("invalid reference override".into());
        }
        if let Some(v) = r.get("route_id") {
            id(v)?
        }
        if let Some(v) = r.get("category_id") {
            text(v, 64)?;
        }
    }
    Ok(())
}
pub(crate) fn validate_preview_input(value: &Value) -> Result<(), String> {
    keys(
        value,
        &[
            "path",
            "model",
            "streaming",
            "error",
            "success_at",
            "retry_after",
            "route_id",
            "category_id",
        ],
        &["path", "streaming", "error"],
    )?;
    validate_path(&value["path"], false, false)?;
    if !value["streaming"].is_boolean() {
        return Err("invalid streaming flag".into());
    }
    let error = text(&value["error"], 32)?;
    if error != "network_error"
        && error != "response_timeout"
        && !error.parse::<u16>().is_ok_and(|n| (400..=599).contains(&n))
    {
        return Err("invalid simulated error".into());
    }
    if let Some(v) = value.get("success_at") {
        if !v.as_u64().is_some_and(|n| n <= 20) {
            return Err("invalid success step".into());
        }
    }
    if let Some(v) = value.get("model") {
        text(v, 256)?;
    }
    if let Some(v) = value.get("route_id") {
        id(v)?;
    }
    if let Some(v) = value.get("retry_after") {
        text(v, 128)?;
    }
    if let Some(v) = value.get("category_id") {
        text(v, 64)?;
    }
    Ok(())
}
pub(crate) fn validate_preview(value: &Value) -> Result<(), String> {
    keys(
        value,
        &["steps", "stop_reason", "max_attempts"],
        &["steps", "stop_reason", "max_attempts"],
    )?;
    validate_attempts(&value["max_attempts"])?;
    text(&value["stop_reason"], 128)?;
    for step in value["steps"].as_array().ok_or("invalid preview steps")? {
        keys(
            step,
            &[
                "step_id",
                "service_id",
                "model",
                "action",
                "status",
                "reason",
                "wait_min_ms",
                "wait_max_ms",
            ],
            &[
                "step_id",
                "service_id",
                "model",
                "action",
                "status",
                "wait_min_ms",
                "wait_max_ms",
            ],
        )?;
        id(&step["step_id"])?;
        id(&step["service_id"])?;
        text(&step["model"], 256)?;
        if !matches!(
            step["status"].as_str(),
            Some("failed" | "succeeded" | "skipped")
        ) || !matches!(
            step["action"].as_str(),
            Some("initial" | "retry" | "failover")
        ) {
            return Err("invalid preview status".into());
        }
        for key in ["wait_min_ms", "wait_max_ms"] {
            if step[key].as_u64().is_none() {
                return Err("invalid preview delay".into());
            }
        }
        if let Some(v) = step.get("reason") {
            text(v, 128)?;
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    fn path() -> Value {
        json!({"id":"path_test","name":"Shared","protocol":"openai.responses","mode":"steps","steps":[{"id":"node_a","service_id":"service_a","upstream_protocol":"openai.responses","plan_type":"native"},{"id":"node_b","service_id":"service_a","upstream_protocol":"openai.responses","plan_type":"native"}]})
    }
    #[test]
    fn repeated_steps_are_valid_but_duplicate_automatic_targets_are_not() {
        let value = path();
        validate_path(&value, false, false).unwrap();
        let mut automatic = value.clone();
        automatic["mode"] = json!("automatic");
        let steps = automatic.as_object_mut().unwrap().remove("steps").unwrap();
        automatic["targets"] = steps;
        assert!(validate_path(&automatic, false, false).is_err());
    }
    #[test]
    fn rejects_invalid_counts_and_unknown_fields() {
        let mut value = path();
        value["max_attempts"] = json!(21);
        assert!(validate_path(&value, false, false).is_err());
        let mut value = path();
        value["steps"][0]["max_retries"] = json!(1);
        assert!(validate_path(&value, false, false).is_err());
        let mut value = path();
        value["unknown"] = json!(true);
        assert!(validate_path(&value, false, false).is_err());
    }
    #[test]
    fn preview_is_bounded_and_validates_the_draft() {
        validate_preview_input(
            &json!({"path":path(),"streaming":false,"error":"429","success_at":2}),
        )
        .unwrap();
        assert!(
            validate_preview_input(&json!({"path":path(),"streaming":false,"error":"200"}))
                .is_err()
        );
    }
}
