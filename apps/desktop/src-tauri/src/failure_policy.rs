use serde::Deserialize;
use std::collections::{BTreeMap, HashSet};

const MAX_MODEL_REDIRECTS: usize = 200;
const MAX_REDIRECT_MODEL_CHARS: usize = 256;
const ASTRLINK_AUTO_MODEL_ID: &str = "astrlink/auto";

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

pub(crate) fn validate_routing_settings(
    value: &serde_json::Value,
    patch: bool,
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
            "model_redirects" => validate_model_redirects(value)?,
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
                if value.is_boolean() => {}
            "claude_identity_version" | "codex_identity_version" => {
                validate_identity_version(value)?
            }
            "strategy" => validate_strategy(value)?,
            "max_attempts" => validate_attempts(value)?,
            _ => return Err("invalid routing settings field".into()),
        }
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
        assert!(validate_routing_settings(&json!({"builtin_tools":tools}), true).is_ok());
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
        assert!(validate_routing_settings(&json!({"default_failure_policy":value,"allow_unmatched_failover":false,"strategy":"retry_first","max_attempts":6}),false).is_ok());
        assert!(
            validate_routing_settings(&json!({"default_failure_policy":policy()}), true).is_ok()
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
        ] {
            for enabled in [true, false] {
                assert!(validate_routing_settings(&json!({key: enabled}), true).is_ok());
            }
            for invalid in [json!(null), json!("false"), json!(0)] {
                assert!(validate_routing_settings(&json!({key: invalid}), true).is_err());
            }
        }
        for key in ["claude_identity_version", "codex_identity_version"] {
            for version in ["", "2.1.300", "0.160.0", "2.2.0-beta.1", "1.0.0+build.5"] {
                assert_eq!(
                    validate_routing_settings(&json!({key: version}), true),
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
                    validate_routing_settings(&json!({key: invalid}), true).is_err(),
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
            "builtin_tools": {
                "web_search": {"enabled": true, "backend": "upstream", "service_id": "service_test", "model": "search-model"},
                "image_generation": {"enabled": false, "backend": "upstream"}
            }
        });
        assert_eq!(validate_routing_settings(&settings, false), Ok(()));
    }
    fn redirect(from: &str, to: &str) -> serde_json::Value {
        json!({"from":from,"to":to,"enabled":true})
    }
    fn redirects(rules: serde_json::Value) -> Result<(), String> {
        validate_routing_settings(&json!({ "model_redirects": rules }), true)
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
        assert!(validate_routing_settings(&json!({"model_redirects":[redirect("a","b")],"default_failure_policy":policy(),"allow_unmatched_failover":false,"strategy":"retry_first","max_attempts":6}),false).is_ok());
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
        assert!(validate_routing_settings(&json!({"max_attempts":21}), true).is_err());
        assert!(validate_routing_settings(&json!({"default_failure_policy":policy(),"allow_unmatched_failover":false,"strategy":"retry_first"}),false).is_err());
    }
}
