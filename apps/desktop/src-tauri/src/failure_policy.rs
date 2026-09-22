use serde::Deserialize;
use std::collections::BTreeMap;

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

pub(crate) fn validate_failover(value: &serde_json::Value) -> Result<(), String> {
    let object = value.as_object().ok_or("failover must be an object")?;
    if object.len() != 3 || !value["enabled"].is_boolean() {
        return Err("invalid failover fields".into());
    }
    validate_strategy(&value["strategy"])?;
    validate_attempts(&value["max_attempts"])
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
            "default_failure_policy" => validate_failure_policy(value)?,
            "allow_unmatched_failover" if value.is_boolean() => {}
            "strategy" => validate_strategy(value)?,
            "max_attempts" => validate_attempts(value)?,
            _ => return Err("invalid routing settings field".into()),
        }
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
        assert!(validate_failover(
            &json!({"enabled":true,"strategy":"retry_first","max_attempts":21})
        )
        .is_err());
        assert!(validate_routing_settings(&json!({"default_failure_policy":policy(),"allow_unmatched_failover":false,"strategy":"retry_first"}),false).is_err());
    }
}
