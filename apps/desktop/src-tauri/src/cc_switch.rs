use reqwest::Url;
use serde::Deserialize;

use crate::sidecar::{CoreManager, CorePhase};

#[derive(Clone, Copy, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Client {
    Claude,
    Codex,
    Gemini,
    Opencode,
    Openclaw,
}

impl Client {
    fn name(self) -> &'static str {
        match self {
            Self::Claude => "claude",
            Self::Codex => "codex",
            Self::Gemini => "gemini",
            Self::Opencode => "opencode",
            Self::Openclaw => "openclaw",
        }
    }
}

#[derive(Default, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Models {
    pub model: Option<String>,
    pub haiku_model: Option<String>,
    pub sonnet_model: Option<String>,
    pub opus_model: Option<String>,
}

impl Models {
    fn parameters(&self, client: Client) -> Result<Vec<(&'static str, &str)>, String> {
        let mut fields = vec![("model", self.model.as_deref())];
        if matches!(client, Client::Claude) {
            fields.extend([
                ("haikuModel", self.haiku_model.as_deref()),
                ("sonnetModel", self.sonnet_model.as_deref()),
                ("opusModel", self.opus_model.as_deref()),
            ]);
        }
        let mut parameters = Vec::new();
        for (key, value) in fields {
            let Some(value) = value.map(str::trim).filter(|value| !value.is_empty()) else {
                continue;
            };
            if value.chars().count() > 256 || value.chars().any(char::is_control) {
                return Err(format!("invalid {key}"));
            }
            parameters.push((key, value));
        }
        if !matches!(client, Client::Claude) && parameters.is_empty() {
            return Err("model is required for this client".into());
        }
        Ok(parameters)
    }
}

fn import_url(
    client: Client,
    inference_url: &str,
    name: &str,
    models: &Models,
    access_token: &str,
) -> Result<Url, String> {
    let mut endpoint = Url::parse(inference_url).map_err(|_| "invalid inference URL")?;
    if endpoint.scheme() != "http"
        || endpoint.host_str() != Some("127.0.0.1")
        || !endpoint.username().is_empty()
        || endpoint.password().is_some()
        || endpoint.path() != "/"
        || endpoint.query().is_some()
        || endpoint.fragment().is_some()
    {
        return Err("invalid local inference URL".into());
    }
    let name = name.trim();
    if name.is_empty() || name.chars().count() > 128 || name.chars().any(char::is_control) {
        return Err("invalid provider name".into());
    }
    let model_parameters = models.parameters(client)?;
    if access_token.is_empty() {
        return Err("access token is unavailable".into());
    }
    // Claude and Gemini append their own API version; OpenAI clients need /v1.
    if matches!(client, Client::Codex | Client::Opencode | Client::Openclaw) {
        endpoint.set_path("/v1");
    }
    let mut url = Url::parse("ccswitch://v1/import").expect("static CC Switch URL");
    {
        let mut params = url.query_pairs_mut();
        params
            .append_pair("resource", "provider")
            .append_pair("app", client.name())
            .append_pair("name", name)
            .append_pair("endpoint", endpoint.as_str().trim_end_matches('/'))
            .append_pair("apiKey", access_token)
            .append_pair("enabled", "false");
        for (key, value) in model_parameters {
            params.append_pair(key, value);
        }
    }
    Ok(url)
}

pub async fn open_import(
    manager: &CoreManager,
    token_id: &str,
    client: Client,
    name: &str,
    models: &Models,
    inference_url: &str,
) -> Result<(), String> {
    let before = manager.snapshot();
    if before.phase != CorePhase::Ready
        || before
            .ready
            .as_ref()
            .map(|ready| ready.inference_url.as_str())
            != Some(inference_url)
    {
        return Err("gateway session changed; reopen the import dialog".into());
    }
    let secret = manager.reveal_access_token(token_id).await?;
    let after = manager.snapshot();
    if after.phase != CorePhase::Ready || before.pid != after.pid || before.ready != after.ready {
        return Err("gateway session changed; reopen the import dialog".into());
    }
    let url = import_url(
        client,
        inference_url,
        name,
        models,
        secret["access_token"].as_str().unwrap_or_default(),
    )?;
    // The URL contains a credential: keep it out of frontend state and errors.
    tauri_plugin_opener::open_url(url.as_str(), None::<&str>)
        .map_err(|_| "unable to open CC Switch; check that it is installed".to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    #[test]
    fn exports_client_endpoints_and_preserves_encoded_values() {
        for (client, expected_path) in [
            (Client::Claude, ""),
            (Client::Codex, "/v1"),
            (Client::Gemini, ""),
            (Client::Opencode, "/v1"),
            (Client::Openclaw, "/v1"),
        ] {
            let url = import_url(
                client,
                "http://127.0.0.1:9324/",
                "AstrLink · 终端 & CLI",
                &Models {
                    model: Some("custom/model".into()),
                    ..Default::default()
                },
                "test+token&=?#",
            )
            .unwrap();
            assert_eq!(url.scheme(), "ccswitch");
            assert_eq!(url.host_str(), Some("v1"));
            assert_eq!(url.path(), "/import");
            let params: HashMap<_, _> = url.query_pairs().into_owned().collect();
            assert_eq!(params["app"], client.name());
            assert_eq!(params["resource"], "provider");
            assert_eq!(
                params["endpoint"],
                format!("http://127.0.0.1:9324{expected_path}")
            );
            assert_eq!(params["name"], "AstrLink · 终端 & CLI");
            assert_eq!(params["apiKey"], "test+token&=?#");
            assert_eq!(params["model"], "custom/model");
            assert_eq!(params["enabled"], "false");
            for key in ["haikuModel", "sonnetModel", "opusModel"] {
                assert!(!params.contains_key(key));
            }
        }
    }

    #[test]
    fn preserves_independent_claude_models_and_omits_empty_fields() {
        for (input, expected) in [
            (serde_json::json!({}), vec![]),
            (
                serde_json::json!({"model": " ", "haikuModel": "", "sonnetModel": "  sonnet-route  "}),
                vec![("sonnetModel", "sonnet-route")],
            ),
            (
                serde_json::json!({"model": "default-route", "haikuModel": "haiku-route", "sonnetModel": "sonnet-route", "opusModel": "opus-route"}),
                vec![
                    ("model", "default-route"),
                    ("haikuModel", "haiku-route"),
                    ("sonnetModel", "sonnet-route"),
                    ("opusModel", "opus-route"),
                ],
            ),
        ] {
            let models: Models = serde_json::from_value(input).unwrap();
            let url = import_url(
                Client::Claude,
                "http://127.0.0.1:8317",
                "AstrLink",
                &models,
                "test-token",
            )
            .unwrap();
            let params: HashMap<_, _> = url.query_pairs().into_owned().collect();
            for key in ["model", "haikuModel", "sonnetModel", "opusModel"] {
                assert_eq!(
                    params.get(key).map(String::as_str),
                    expected
                        .iter()
                        .find(|(field, _)| *field == key)
                        .map(|(_, value)| *value)
                );
            }
        }
    }

    #[test]
    fn other_clients_require_a_model_and_never_receive_claude_tiers() {
        let mut models = Models {
            opus_model: Some("opus-route".into()),
            ..Default::default()
        };
        for client in [
            Client::Codex,
            Client::Gemini,
            Client::Opencode,
            Client::Openclaw,
        ] {
            assert!(import_url(
                client,
                "http://127.0.0.1:8317",
                "AstrLink",
                &models,
                "test-token"
            )
            .is_err());
        }
        models.model = Some("main-route".into());
        let url = import_url(
            Client::Codex,
            "http://127.0.0.1:8317",
            "AstrLink",
            &models,
            "test-token",
        )
        .unwrap();
        assert!(!url.query_pairs().any(|(key, _)| key == "opusModel"));
    }

    #[test]
    fn validates_every_optional_model() {
        for key in ["model", "haikuModel", "sonnetModel", "opusModel"] {
            for value in ["x".repeat(257), "invalid\nmodel".into()] {
                let models: Models =
                    serde_json::from_value(serde_json::json!({key: value})).unwrap();
                assert!(import_url(
                    Client::Claude,
                    "http://127.0.0.1:8317",
                    "AstrLink",
                    &models,
                    "test-token"
                )
                .is_err());
            }
        }
    }

    #[test]
    fn rejects_invalid_destination_and_config_without_echoing_secrets() {
        for endpoint in [
            "https://example.com",
            "http://127.0.0.1:8317/control",
            "http://secret@127.0.0.1:8317",
            "http://127.0.0.1:8317?secret",
            "http://127.0.0.1:8317#secret",
        ] {
            let error = import_url(
                Client::Codex,
                endpoint,
                "AstrLink",
                &Models {
                    model: Some("custom/model".into()),
                    ..Default::default()
                },
                "secret",
            )
            .unwrap_err();
            assert!(!error.contains("secret"));
        }
        for (name, model, token) in [
            (" ", "auto", "key"),
            ("name", "invalid\nmodel", "key"),
            ("name", "auto", ""),
        ] {
            assert!(import_url(
                Client::Claude,
                "http://127.0.0.1:8317",
                name,
                &Models {
                    model: Some(model.into()),
                    ..Default::default()
                },
                token
            )
            .is_err());
        }
        assert!(serde_json::from_str::<Client>("\"unknown\"").is_err());
    }
}
