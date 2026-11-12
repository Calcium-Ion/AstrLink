//! The gateway a client is written for: its local address and its tokens.

use std::collections::BTreeMap;

use reqwest::Url;

use super::records::ClientRecord;
use crate::sidecar::{CoreManager, CorePhase};

/// `http://127.0.0.1:<port>` or `http://localhost:<port>` for the gateway's
/// root inference URL; Core announces localhost only while it serves `[::1]`.
pub fn local_origin(inference_url: &str) -> Result<String, String> {
    let url = Url::parse(inference_url).map_err(|_| "invalid inference URL")?;
    if url.scheme() != "http"
        || !matches!(url.host_str(), Some("127.0.0.1" | "localhost"))
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err("invalid local inference URL".into());
    }
    Ok(url.as_str().trim_end_matches('/').to_string())
}

/// Reveals a token for the gateway session the dialog was opened against,
/// so a restart in between cannot pair the token with a stale address.
pub async fn reveal_access_token(
    manager: &CoreManager,
    token_id: &str,
    inference_url: &str,
) -> Result<String, String> {
    let changed = || "gateway session changed; reopen the dialog".to_string();
    let before = manager.snapshot();
    if before.phase != CorePhase::Ready
        || before
            .ready
            .as_ref()
            .map(|ready| ready.client_inference_url.as_str())
            != Some(inference_url)
    {
        return Err(changed());
    }
    let secret = manager.reveal_access_token(token_id).await?;
    let after = manager.snapshot();
    if after.phase != CorePhase::Ready || before.pid != after.pid || before.ready != after.ready {
        return Err(changed());
    }
    secret["access_token"]
        .as_str()
        .filter(|token| !token.is_empty())
        .map(str::to_string)
        .ok_or_else(|| "access token is unavailable".to_string())
}

/// A token's list entry: its display name and non-secret hint.
pub(crate) async fn token_summary(
    manager: &CoreManager,
    token_id: &str,
) -> Result<(String, String), String> {
    let page = manager.list_access_tokens().await?;
    page["items"]
        .as_array()
        .into_iter()
        .flatten()
        .find(|item| item["id"].as_str() == Some(token_id))
        .and_then(|item| Some((item["name"].as_str()?, item["hint"].as_str()?)))
        .map(|(name, hint)| (name.to_string(), hint.to_string()))
        .ok_or_else(|| "access token not found".to_string())
}

/// The gateway's access tokens: each id with its non-secret hint. Every
/// AstrLink on this computer writes the same client configs, and one written
/// with a token the gateway lacks belongs to another AstrLink, such as the
/// instance `make dev` runs beside an installed one. Token ids are random,
/// so no two gateways share one.
#[derive(Debug)]
pub struct Tokens(pub(crate) BTreeMap<String, String>);

impl Tokens {
    pub async fn of(manager: &CoreManager) -> Result<Self, String> {
        let page = manager.list_access_tokens().await?;
        let items = page["items"]
            .as_array()
            .ok_or_else(|| "access token list is invalid".to_string())?;
        Ok(Self(
            items
                .iter()
                .filter_map(|item| {
                    let id = item["id"].as_str()?;
                    let hint = item["hint"].as_str().unwrap_or_default();
                    Some((id.to_string(), hint.to_string()))
                })
                .collect(),
        ))
    }

    pub(crate) fn own(&self, record: &ClientRecord) -> bool {
        self.0.contains_key(&record.token_id)
    }

    /// The token whose hint ends in the last characters of `token`, when
    /// exactly one does.
    pub(crate) fn matching(&self, token: &str) -> Option<&str> {
        let hint = format!("astr_…{}", token.get(token.len().checked_sub(6)?..)?);
        let mut found = self.0.iter().filter(|(_, known)| **known == hint);
        let (id, _) = found.next()?;
        found.next().is_none().then_some(id.as_str())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn local_origin_accepts_only_the_loopback_gateway_root() {
        assert_eq!(
            local_origin("http://127.0.0.1:18317/").unwrap(),
            "http://127.0.0.1:18317"
        );
        assert_eq!(
            local_origin("http://localhost:18317").unwrap(),
            "http://localhost:18317"
        );
        for url in [
            "https://127.0.0.1:1",
            "http://[::1]:1",
            "http://localhost.example:1",
            "http://localhost:1/v1",
            "http://127.0.0.1:1/v1",
            "http://user@127.0.0.1:1",
            "http://127.0.0.1:1?x",
            "http://127.0.0.1:1#x",
        ] {
            assert!(local_origin(url).is_err());
        }
    }
}
