//! Clients AstrLink hands the connection to through their own import link.
//! The client shows what it imports and saves it itself, so AstrLink writes
//! nothing and keeps no record. The link carries the token: it is built here,
//! opened by the OS, and never logged.

use base64::{engine::general_purpose::STANDARD, Engine};
use serde::Serialize;

use super::catalog::{CatalogEntry, Kind};

/// A client that imports a connection from a link.
pub(crate) struct LinkClient {
    pub id: &'static str,
    pub name: &'static str,
    /// The links it opens, whose handler marks it installed.
    pub scheme: &'static str,
    /// The protocols a link can carry, preferred first.
    pub protocols: &'static [&'static str],
    build: fn(protocol: &str, origin: &str, token: &str) -> Option<String>,
}

impl LinkClient {
    pub(crate) fn entry(&self) -> CatalogEntry {
        CatalogEntry {
            id: self.id,
            name: self.name,
            kind: Kind::Link,
            targets: &[],
            protocols: self.protocols,
            list: false,
        }
    }
}

/// Every link client, in rail order.
pub(crate) const LINKS: [LinkClient; 2] = [
    LinkClient {
        id: "cherry_studio",
        name: "Cherry Studio",
        scheme: "cherrystudio",
        protocols: &[
            "openai.chat",
            "openai.responses",
            "anthropic.messages",
            "google.generate_content",
        ],
        build: cherry_studio,
    },
    LinkClient {
        id: "aionui",
        name: "AionUi",
        scheme: "aionui",
        protocols: &["openai.chat", "anthropic.messages"],
        build: aionui,
    },
];

/// The import link that gives `client` this gateway and token.
pub fn import_link(
    client: &str,
    protocol: &str,
    origin: &str,
    token: &str,
) -> Result<String, String> {
    let link = LINKS
        .iter()
        .find(|link| link.id == client)
        .ok_or_else(|| format!("{client} takes no import link"))?;
    if !link.protocols.contains(&protocol) {
        return Err(format!("{client} takes no {protocol} link"));
    }
    (link.build)(protocol, origin, token)
        .ok_or_else(|| format!("{client} takes no {protocol} link"))
}

/// Base64 the handlers decode, with `+`, `/`, and `=` escaped so the query
/// parser keeps them.
fn encode(json: &str) -> String {
    STANDARD
        .encode(json)
        .replace('+', "%2B")
        .replace('/', "%2F")
        .replace('=', "%3D")
}

/// A new custom provider per protocol, since Cherry Studio matches an
/// existing one by id and cannot switch its type on import. Cherry adds
/// `/v1` (`/v1beta` for Gemini) to the bare origin itself.
fn cherry_studio(protocol: &str, origin: &str, token: &str) -> Option<String> {
    #[derive(Serialize)]
    struct Provider<'a> {
        id: &'a str,
        name: &'a str,
        #[serde(rename = "type")]
        kind: &'a str,
        #[serde(rename = "baseUrl")]
        base_url: &'a str,
        #[serde(rename = "apiKey")]
        api_key: &'a str,
    }
    let (id, name, kind) = match protocol {
        "openai.chat" => ("astrlink", "AstrLink", "openai"),
        "openai.responses" => (
            "astrlink-responses",
            "AstrLink Responses",
            "openai-response",
        ),
        "anthropic.messages" => ("astrlink-anthropic", "AstrLink Anthropic", "anthropic"),
        "google.generate_content" => ("astrlink-gemini", "AstrLink Gemini", "gemini"),
        _ => return None,
    };
    let json = serde_json::to_string(&Provider {
        id,
        name,
        kind,
        base_url: origin,
        api_key: token,
    })
    .ok()?;
    Some(format!(
        "cherrystudio://providers/api-keys?v=1&data={}",
        encode(&json)
    ))
}

/// AionUi prefills its add-model form from snake_case keys. Its custom
/// platform appends `/chat/completions` to the address, and its Anthropic
/// one `/v1/messages`.
fn aionui(protocol: &str, origin: &str, token: &str) -> Option<String> {
    #[derive(Serialize)]
    struct Provider<'a> {
        platform: &'a str,
        base_url: &'a str,
        api_key: &'a str,
        name: &'a str,
    }
    let (platform, base_url) = match protocol {
        "openai.chat" => ("custom", format!("{origin}/v1")),
        "anthropic.messages" => ("Anthropic", origin.to_string()),
        _ => return None,
    };
    let json = serde_json::to_string(&Provider {
        platform,
        base_url: &base_url,
        api_key: token,
        name: "AstrLink",
    })
    .ok()?;
    Some(format!("aionui://provider/add?v=1&data={}", encode(&json)))
}

#[cfg(test)]
mod tests {
    use super::*;

    const TOKEN: &str = "astr_VVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVA";
    const ORIGIN: &str = "http://127.0.0.1:8317";

    #[test]
    fn cherry_studio_links_match_its_import_handler() {
        let link = |protocol| import_link("cherry_studio", protocol, ORIGIN, TOKEN).unwrap();
        assert_eq!(
            link("openai.chat"),
            "cherrystudio://providers/api-keys?v=1&data=eyJpZCI6ImFzdHJsaW5rIiwibmFtZSI6IkFzdHJMaW5rIiwidHlwZSI6Im9wZW5haSIsImJhc2VVcmwiOiJodHRwOi8vMTI3LjAuMC4xOjgzMTciLCJhcGlLZXkiOiJhc3RyX1ZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVkEifQ%3D%3D"
        );
        assert_eq!(
            link("openai.responses"),
            "cherrystudio://providers/api-keys?v=1&data=eyJpZCI6ImFzdHJsaW5rLXJlc3BvbnNlcyIsIm5hbWUiOiJBc3RyTGluayBSZXNwb25zZXMiLCJ0eXBlIjoib3BlbmFpLXJlc3BvbnNlIiwiYmFzZVVybCI6Imh0dHA6Ly8xMjcuMC4wLjE6ODMxNyIsImFwaUtleSI6ImFzdHJfVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWQSJ9"
        );
        assert_eq!(
            link("anthropic.messages"),
            "cherrystudio://providers/api-keys?v=1&data=eyJpZCI6ImFzdHJsaW5rLWFudGhyb3BpYyIsIm5hbWUiOiJBc3RyTGluayBBbnRocm9waWMiLCJ0eXBlIjoiYW50aHJvcGljIiwiYmFzZVVybCI6Imh0dHA6Ly8xMjcuMC4wLjE6ODMxNyIsImFwaUtleSI6ImFzdHJfVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWQSJ9"
        );
        assert_eq!(
            link("google.generate_content"),
            "cherrystudio://providers/api-keys?v=1&data=eyJpZCI6ImFzdHJsaW5rLWdlbWluaSIsIm5hbWUiOiJBc3RyTGluayBHZW1pbmkiLCJ0eXBlIjoiZ2VtaW5pIiwiYmFzZVVybCI6Imh0dHA6Ly8xMjcuMC4wLjE6ODMxNyIsImFwaUtleSI6ImFzdHJfVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWQSJ9"
        );
    }

    #[test]
    fn aionui_links_match_its_deep_link_parser() {
        let link = |protocol| import_link("aionui", protocol, ORIGIN, TOKEN).unwrap();
        assert_eq!(
            link("openai.chat"),
            "aionui://provider/add?v=1&data=eyJwbGF0Zm9ybSI6ImN1c3RvbSIsImJhc2VfdXJsIjoiaHR0cDovLzEyNy4wLjAuMTo4MzE3L3YxIiwiYXBpX2tleSI6ImFzdHJfVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWQSIsIm5hbWUiOiJBc3RyTGluayJ9"
        );
        assert_eq!(
            link("anthropic.messages"),
            "aionui://provider/add?v=1&data=eyJwbGF0Zm9ybSI6IkFudGhyb3BpYyIsImJhc2VfdXJsIjoiaHR0cDovLzEyNy4wLjAuMTo4MzE3IiwiYXBpX2tleSI6ImFzdHJfVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWQSIsIm5hbWUiOiJBc3RyTGluayJ9"
        );
        assert!(import_link("aionui", "google.generate_content", ORIGIN, TOKEN).is_err());
        assert!(import_link("claude", "anthropic.messages", ORIGIN, TOKEN).is_err());
    }

    #[test]
    fn plus_slash_and_padding_are_escaped() {
        // `?>>` ends in `+`, `???` in `/`, and `??` takes padding.
        assert_eq!(encode("?>>"), "Pz4%2B");
        assert_eq!(encode("??"), "Pz8%3D");
        assert_eq!(encode("???"), "Pz8%2F");
        let link = import_link("cherry_studio", "openai.chat", ORIGIN, "astr_?>>?").unwrap();
        let data = link.split_once("data=").unwrap().1;
        assert!(!data.contains(['+', '/', '=']), "{data}");
        let decoded = STANDARD
            .decode(
                data.replace("%2B", "+")
                    .replace("%2F", "/")
                    .replace("%3D", "="),
            )
            .unwrap();
        let provider: serde_json::Value = serde_json::from_slice(&decoded).unwrap();
        assert_eq!(provider["apiKey"], "astr_?>>?");
    }
}
