use serde_json::Value;

pub(crate) fn validate_proxy(
    value: &Value,
    service_id: Option<&str>,
    input: bool,
) -> Result<(), String> {
    if input && value.is_null() {
        return Ok(());
    }
    let object = value.as_object().ok_or("invalid service proxy")?;
    let credential_key = if input {
        "credential"
    } else {
        "credential_ref"
    };
    if object
        .keys()
        .any(|key| !["mode", "url", credential_key].contains(&key.as_str()))
    {
        return Err("unexpected proxy field".into());
    }
    let mode = object
        .get("mode")
        .and_then(Value::as_str)
        .ok_or("proxy mode is required")?;
    if !["inherit", "direct", "custom"].contains(&mode) {
        return Err("invalid proxy mode".into());
    }
    if mode != "custom" {
        if object.contains_key("url")
            || object
                .get(credential_key)
                .is_some_and(|v| !input || !v.is_null())
        {
            return Err("only custom proxy may specify an address or authentication".into());
        }
        return Ok(());
    }
    let raw = object
        .get("url")
        .and_then(Value::as_str)
        .ok_or("proxy URL is required")?;
    let url = reqwest::Url::parse(raw).map_err(|_| "invalid proxy URL")?;
    if raw.len() > 2048
        || raw.trim() != raw
        || raw.ends_with(':')
        || raw.chars().any(char::is_control)
        || raw.contains(['@', '?', '#'])
        || !["http", "https", "socks5"].contains(&url.scheme())
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || (url.path() != "" && url.path() != "/")
        || url.port() == Some(0)
    {
        return Err("invalid proxy URL".into());
    }
    if let Some(auth) = object.get(credential_key) {
        if input {
            if auth.is_null() {
                return Ok(());
            }
            let auth = auth.as_object().ok_or("invalid proxy authentication")?;
            if auth.len() != 2 || !auth.contains_key("username") || !auth.contains_key("password") {
                return Err("invalid proxy authentication".into());
            }
            let username = auth["username"].as_str().ok_or("invalid proxy username")?;
            let password = auth["password"].as_str().ok_or("invalid proxy password")?;
            if username.is_empty()
                || username.len() > 255
                || password.len() > 255
                || username.contains([':', '\r', '\n', '\0'])
                || password.contains(['\r', '\n', '\0'])
            {
                return Err("invalid proxy authentication".into());
            }
        } else {
            let expected = format!(
                "local://service-proxy/{}",
                service_id.ok_or("missing service id")?
            );
            if auth.as_str() != Some(expected.as_str()) {
                return Err("invalid proxy credential reference".into());
            }
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn proxy_modes_and_secret_boundary() {
        for mode in ["inherit", "direct"] {
            assert!(validate_proxy(&json!({"mode": mode}), None, true).is_ok());
        }
        assert!(validate_proxy(&Value::Null, None, true).is_ok());
        for scheme in ["http", "https", "socks5"] {
            assert!(validate_proxy(&json!({"mode":"custom", "url":format!("{scheme}://127.0.0.1:1080"), "credential":{"username":"user", "password":"secret"}}), None, true).is_ok());
        }
        assert!(validate_proxy(&json!({"mode":"custom", "url":"http://proxy:8080", "credential_ref":"local://service-proxy/service_one"}), Some("service_one"), false).is_ok());
        for url in [
            "ftp://proxy",
            "http://user:secret@proxy",
            "http://proxy?secret",
            "http://proxy/#secret",
            "http://proxy:0",
            "http://proxy/path",
        ] {
            assert!(validate_proxy(&json!({"mode":"custom", "url":url}), None, true).is_err());
        }
        assert!(validate_proxy(&json!({"mode":"custom", "url":"http://proxy", "credential":{"username":"user", "password":"secret"}}), Some("service_one"), false).is_err());
        assert!(validate_proxy(&json!({"mode":"custom", "url":"http://proxy", "credential_ref":"local://service-proxy/service_two"}), Some("service_one"), false).is_err());
    }
}
