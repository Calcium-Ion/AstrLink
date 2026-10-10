use std::{
    fs,
    path::{Path, PathBuf},
};

use reqwest::Url;
use serde_json::Value;

use crate::{
    agent_home::{Homes, OwnedHome, ToolDirs},
    client_config::{self, Client, Models},
    sidecar::CoreManager,
    wsl::{self, Reach},
};

fn import_url(
    client: Client,
    inference_url: &str,
    name: &str,
    models: &Models,
    access_token: &str,
) -> Result<Url, String> {
    // CC Switch has no Pi app; AstrLink writes Pi's config itself.
    if client == Client::Pi {
        return Err("CC Switch cannot import this client".into());
    }
    let origin = client_config::local_origin(inference_url)?;
    let name = name.trim();
    if name.is_empty() || name.chars().count() > 128 || name.chars().any(char::is_control) {
        return Err("invalid provider name".into());
    }
    // CC Switch's import link has no Fable tier.
    let mut model_parameters = models.fields(client)?;
    model_parameters.retain(|(key, _)| *key != "fableModel");
    if access_token.is_empty() {
        return Err("access token is unavailable".into());
    }
    // Claude and Gemini append their own API version; OpenAI clients need /v1.
    let endpoint = if matches!(client, Client::Codex | Client::Opencode | Client::Openclaw) {
        format!("{origin}/v1")
    } else {
        origin
    };
    let mut url = Url::parse("ccswitch://v1/import").expect("static CC Switch URL");
    {
        let mut params = url.query_pairs_mut();
        params
            .append_pair("resource", "provider")
            .append_pair("app", client.name())
            .append_pair("name", name)
            .append_pair("endpoint", &endpoint)
            .append_pair("apiKey", access_token)
            .append_pair("enabled", "false");
        for (key, value) in &model_parameters {
            params.append_pair(key, value);
        }
    }
    Ok(url)
}

/// Whether the OS has an app registered for CC Switch's import links.
#[cfg(target_os = "macos")]
pub fn installed() -> bool {
    use core_foundation::{
        base::{kCFAllocatorDefault, TCFType},
        error::CFErrorRef,
        string::CFString,
        url::{CFURLCreateWithString, CFURLRef, CFURL},
    };
    use std::ptr;

    #[link(name = "CoreServices", kind = "framework")]
    extern "C" {
        fn LSCopyDefaultApplicationURLForURL(
            url: CFURLRef,
            roles: u32,
            error: *mut CFErrorRef,
        ) -> CFURLRef;
    }
    const LS_ROLES_ALL: u32 = u32::MAX;

    let link = CFString::from_static_string("ccswitch://v1/import");
    // SAFETY: `link` outlives the call; a non-null result follows the create rule.
    let url = unsafe {
        CFURLCreateWithString(kCFAllocatorDefault, link.as_concrete_TypeRef(), ptr::null())
    };
    if url.is_null() {
        return false;
    }
    let url = unsafe { CFURL::wrap_under_create_rule(url) };
    // SAFETY: `url` outlives the call; a non-null result follows the copy rule.
    let app = unsafe {
        LSCopyDefaultApplicationURLForURL(url.as_concrete_TypeRef(), LS_ROLES_ALL, ptr::null_mut())
    };
    if app.is_null() {
        return false;
    }
    let app = unsafe { CFURL::wrap_under_create_rule(app) };
    // Launch Services can keep a record briefly after the app is deleted.
    app.to_path().is_some_and(|path| path.exists())
}

#[cfg(windows)]
pub fn installed() -> bool {
    use std::ptr;
    use windows_sys::Win32::{
        Foundation::ERROR_SUCCESS,
        System::Registry::{
            RegGetValueW, HKEY_CLASSES_ROOT, RRF_NOEXPAND, RRF_RT_REG_EXPAND_SZ, RRF_RT_REG_SZ,
        },
    };

    let key: Vec<u16> = "ccswitch\\shell\\open\\command"
        .encode_utf16()
        .chain([0])
        .collect();
    let mut size = 0u32;
    // SAFETY: `key` is NUL-terminated and outlives the call; a null value name
    // reads the default value and a null data pointer only queries its size.
    let status = unsafe {
        RegGetValueW(
            HKEY_CLASSES_ROOT,
            key.as_ptr(),
            ptr::null(),
            RRF_RT_REG_SZ | RRF_RT_REG_EXPAND_SZ | RRF_NOEXPAND,
            ptr::null_mut(),
            ptr::null_mut(),
            &mut size,
        )
    };
    // An empty command is a lone UTF-16 NUL.
    status == ERROR_SUCCESS && size > 2
}

#[cfg(target_os = "linux")]
pub fn installed() -> bool {
    gtk::gio::AppInfo::default_for_uri_scheme("ccswitch").is_some()
}

#[cfg(not(any(target_os = "macos", windows, target_os = "linux")))]
pub fn installed() -> bool {
    false
}

pub async fn open_import(
    manager: &CoreManager,
    home: PathBuf,
    token_id: &str,
    client: Client,
    models: &Models,
    inference_url: &str,
) -> Result<(), String> {
    let (token_name, _) = client_config::token_summary(manager, token_id).await?;
    let token = client_config::reveal_access_token(manager, token_id, inference_url).await?;
    let url = import_url(
        client,
        inference_url,
        &format!("AstrLink · {token_name}"),
        models,
        &token,
    )?;
    // Note the move before CC Switch can act on it, so the config CC Switch
    // writes is not reported as a change. The import goes ahead without it.
    let marked = set_cc_switch(home.clone(), client, true)
        .await
        .unwrap_or_else(|error| {
            eprintln!("unable to note the move to CC Switch: {error}");
            false
        });
    // The URL contains a credential: keep it out of frontend state and errors.
    if tauri_plugin_opener::open_url(url.as_str(), None::<&str>).is_err() {
        if marked {
            if let Err(error) = set_cc_switch(home, client, false).await {
                eprintln!("unable to undo the move to CC Switch: {error}");
            }
        }
        return Err("unable to open CC Switch; check that it is installed".into());
    }
    Ok(())
}

async fn set_cc_switch(home: PathBuf, client: Client, handed_over: bool) -> Result<bool, String> {
    tauri::async_runtime::spawn_blocking(move || match managed_home(home, client)? {
        Some(target) => client_config::set_cc_switch(target.home(), client, handed_over),
        None => Ok(false),
    })
    .await
    .map_err(|error| error.to_string())?
}

/// The home whose config CC Switch writes for `client`: the WSL home its
/// directory setting points into, or else the desktop user's. `None` when
/// that distribution is no longer registered.
fn managed_home(home: PathBuf, client: Client) -> Result<Option<OwnedHome>, String> {
    let (_, wsl_dirs) = wsl::split_dirs(config_dirs(&home));
    let distribution = wsl_dirs
        .into_iter()
        .find(|entry| match client {
            Client::Claude => entry.dirs.claude.is_some(),
            Client::Codex => entry.dirs.codex.is_some(),
            _ => false,
        })
        .map(|entry| entry.distribution);
    let names = distribution.iter().cloned().collect::<Vec<_>>();
    Ok(Homes::find(home, Reach::Named(&names))?.take(distribution.as_deref()))
}

/// The configuration directories CC Switch's settings move each app to, so
/// agent installs reach the same Claude Code or Codex, inside WSL included.
/// Missing or unreadable settings move nothing.
pub fn config_dirs(home: &Path) -> ToolDirs {
    let settings = fs::read_to_string(home.join(".cc-switch").join("settings.json"))
        .ok()
        .and_then(|raw| serde_json::from_str::<Value>(&raw).ok())
        .unwrap_or_default();
    let dir = |key: &str| {
        settings
            .get(key)
            .and_then(Value::as_str)
            .and_then(|raw| resolve_dir(home, raw))
    };
    ToolDirs {
        claude: dir("claudeConfigDir"),
        codex: dir("codexConfigDir"),
        grok: dir("grokConfigDir"),
        pi: dir("piConfigDir"),
    }
}

/// Resolves a directory setting the way CC Switch does: `~` is the home, and
/// anything still relative is ignored.
fn resolve_dir(home: &Path, raw: &str) -> Option<PathBuf> {
    let raw = raw.trim();
    let path = if raw == "~" {
        home.to_path_buf()
    } else if let Some(rest) = raw.strip_prefix("~/").or_else(|| raw.strip_prefix("~\\")) {
        rest.split(['/', '\\'])
            .filter(|part| !part.is_empty())
            .fold(home.to_path_buf(), |path, part| path.join(part))
    } else {
        PathBuf::from(raw)
    };
    path.is_absolute().then_some(path)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    #[test]
    fn config_dirs_follow_cc_switch_settings() {
        let home = std::env::temp_dir().join(format!("astrlink-ccs-dirs-{}", std::process::id()));
        let _ = fs::remove_dir_all(&home);
        assert_eq!(config_dirs(&home), ToolDirs::default());
        fs::create_dir_all(home.join(".cc-switch")).unwrap();
        fs::write(
            home.join(".cc-switch").join("settings.json"),
            r#"{"claudeConfigDir":"~/work/.claude","codexConfigDir":"  ","grokConfigDir":"relative/grok","piConfigDir":"/opt/pi/agent","showInTray":true}"#,
        )
        .unwrap();
        let absolute = if cfg!(windows) {
            None
        } else {
            Some(PathBuf::from("/opt/pi/agent"))
        };
        assert_eq!(
            config_dirs(&home),
            ToolDirs {
                claude: Some(home.join("work").join(".claude")),
                codex: None,
                grok: None,
                pi: absolute,
            }
        );
        fs::write(home.join(".cc-switch").join("settings.json"), "not json").unwrap();
        assert_eq!(config_dirs(&home), ToolDirs::default());
        fs::remove_dir_all(&home).unwrap();
    }

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
            for key in ["haikuModel", "sonnetModel", "opusModel", "fableModel"] {
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
    fn pi_is_never_sent_to_cc_switch() {
        let models = Models {
            model: Some("pi-route".into()),
            ..Default::default()
        };
        let error = import_url(
            Client::Pi,
            "http://127.0.0.1:8317",
            "AstrLink",
            &models,
            "test-token",
        )
        .unwrap_err();
        assert!(!error.contains("test-token"));
    }

    #[test]
    fn the_fable_tier_is_not_sent_to_cc_switch() {
        let models: Models =
            serde_json::from_value(serde_json::json!({"fableModel": "fable-route"})).unwrap();
        let url = import_url(
            Client::Claude,
            "http://127.0.0.1:8317",
            "AstrLink",
            &models,
            "test-token",
        )
        .unwrap();
        assert!(!url.query_pairs().any(|(_, value)| value == "fable-route"));
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
            "http://[::1]:8317",
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
