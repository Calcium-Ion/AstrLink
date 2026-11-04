//! The clients the dialog lists: the ones AstrLink writes, and the ones it
//! can only explain. Icons and copy stay in the frontend, keyed by id.

use serde::Serialize;

use super::{
    client::Client,
    link::{LinkClient, LINKS},
};
use crate::cc_switch;

/// How AstrLink reaches a client.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Kind {
    /// AstrLink writes its config files.
    File,
    /// Its config files, with a token the user pastes into the client once.
    Paste,
    /// The client's own import link.
    Link,
    /// What to fill in, for the user to type into the client.
    Guide,
    /// It cannot reach the local gateway.
    Unsupported,
}

#[derive(Debug, PartialEq, Eq, Serialize)]
pub struct CatalogEntry {
    pub id: &'static str,
    /// The product name, never translated.
    pub name: &'static str,
    pub kind: Kind,
    /// Write targets of a client with several; empty for one.
    pub targets: &'static [&'static str],
    /// Gateway protocols the client can be written or filled with,
    /// preferred first.
    pub protocols: &'static [&'static str],
    /// Whether the client switches among a written list of models.
    pub list: bool,
}

#[derive(Debug, Serialize)]
pub struct ClientCatalog {
    pub clients: Vec<CatalogEntry>,
}

const fn listed(
    id: &'static str,
    name: &'static str,
    kind: Kind,
    protocols: &'static [&'static str],
) -> CatalogEntry {
    CatalogEntry {
        id,
        name,
        kind,
        targets: &[],
        protocols,
        list: false,
    }
}

/// Clients set up by hand in their own settings, with the protocols they
/// take there.
const GUIDES: [CatalogEntry; 5] = [
    listed(
        "trae",
        "TRAE",
        Kind::Guide,
        &["anthropic.messages", "openai.responses", "openai.chat"],
    ),
    listed(
        "roo_code",
        "Roo Code",
        Kind::Guide,
        &[
            "anthropic.messages",
            "openai.chat",
            "google.generate_content",
        ],
    ),
    listed(
        "lobehub",
        "LobeHub",
        Kind::Guide,
        &[
            "anthropic.messages",
            "openai.chat",
            "google.generate_content",
        ],
    ),
    listed(
        "lobsterai",
        "LobsterAI",
        Kind::Guide,
        &[
            "anthropic.messages",
            "openai.responses",
            "openai.chat",
            "google.generate_content",
        ],
    ),
    listed(
        "chatwise",
        "ChatWise",
        Kind::Guide,
        &["anthropic.messages", "openai.chat"],
    ),
];

/// Clients that send requests from their own servers, or whose personal
/// editions take no address.
const UNSUPPORTED: [CatalogEntry; 6] = [
    listed("cursor", "Cursor", Kind::Unsupported, &[]),
    listed("warp", "Warp", Kind::Unsupported, &[]),
    listed("amp", "Amp", Kind::Unsupported, &[]),
    listed("trae_plugin", "TRAE Plugin", Kind::Unsupported, &[]),
    listed("comate", "文心快码 Comate", Kind::Unsupported, &[]),
    listed("qoder", "Qoder", Kind::Unsupported, &[]),
];

/// Every client the dialog lists, by kind, in rail order.
pub fn catalog() -> ClientCatalog {
    let written = Client::all().map(|client| CatalogEntry {
        id: client.id(),
        name: client.name(),
        kind: client.kind(),
        targets: client.targets(),
        protocols: client.protocols(),
        list: client.lists_models(),
    });
    let linked = LINKS.iter().map(LinkClient::entry);
    ClientCatalog {
        clients: written
            .chain(linked)
            .chain(GUIDES)
            .chain(UNSUPPORTED)
            .collect(),
    }
}

/// The link clients installed on this computer: those whose links the OS
/// opens. The dialog lists them among the installed ones.
pub fn installed() -> Vec<String> {
    LINKS
        .iter()
        .filter(|link| cc_switch::opens_links(link.scheme))
        .map(|link| link.id.to_string())
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn the_catalog_lists_each_client_once_by_a_well_formed_id() {
        let catalog = catalog();
        let ids = catalog
            .clients
            .iter()
            .map(|entry| entry.id)
            .collect::<Vec<_>>();
        // `^[a-z][a-z0-9_]{1,31}$`
        let well_formed = |id: &str| {
            id.starts_with(|first: char| first.is_ascii_lowercase())
                && (2..=32).contains(&id.len())
                && id
                    .chars()
                    .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '_')
        };
        assert!(ids.iter().all(|id| well_formed(id)), "{ids:?}");
        assert_eq!(ids.iter().collect::<BTreeSet<_>>().len(), ids.len());
        let protocols = catalog
            .clients
            .iter()
            .filter(|entry| entry.kind == Kind::File)
            .map(|entry| (entry.id, entry.protocols))
            .collect::<Vec<_>>();
        assert_eq!(
            protocols[..6],
            [
                ("claude", &["anthropic.messages"][..]),
                ("codex", &["openai.responses"]),
                ("gemini", &["google.generate_content"]),
                ("opencode", &["openai.chat"]),
                ("openclaw", &["openai.chat"]),
                ("pi", &["openai.responses"]),
            ]
        );
        const KNOWN: [&str; 4] = [
            "anthropic.messages",
            "openai.responses",
            "openai.chat",
            "google.generate_content",
        ];
        assert!(protocols.iter().all(|(_, protocols)| !protocols.is_empty()
            && protocols.iter().all(|protocol| KNOWN.contains(protocol))));
        let static_ids = catalog
            .clients
            .iter()
            .filter(|entry| matches!(entry.kind, Kind::Guide | Kind::Unsupported))
            .map(|entry| entry.id)
            .collect::<Vec<_>>();
        assert_eq!(
            static_ids,
            [
                "trae",
                "roo_code",
                "lobehub",
                "lobsterai",
                "chatwise",
                "cursor",
                "warp",
                "amp",
                "trae_plugin",
                "comate",
                "qoder"
            ]
        );
        // Only the clients AstrLink writes are ones a command can name.
        for entry in &catalog.clients {
            assert_eq!(
                Client::find(entry.id).is_some(),
                matches!(entry.kind, Kind::File | Kind::Paste)
            );
        }
        // Grouped by kind: written, linked, explained, unsupported.
        let rank = |kind| match kind {
            Kind::File | Kind::Paste => 0,
            Kind::Link => 1,
            Kind::Guide => 2,
            Kind::Unsupported => 3,
        };
        assert!(catalog
            .clients
            .windows(2)
            .all(|pair| rank(pair[0].kind) <= rank(pair[1].kind)));
        assert_eq!(
            serde_json::to_value(&catalog).unwrap()["clients"][5],
            serde_json::json!({
                "id": "pi",
                "name": "Pi",
                "kind": "file",
                "targets": [],
                "protocols": ["openai.responses"],
                "list": true,
            })
        );
        let link = catalog
            .clients
            .iter()
            .find(|entry| entry.id == "cherry_studio")
            .unwrap();
        assert_eq!(serde_json::to_value(link).unwrap()["kind"], "link");
        assert!(installed()
            .iter()
            .all(|id| LINKS.iter().any(|link| link.id == id)));
    }
}
