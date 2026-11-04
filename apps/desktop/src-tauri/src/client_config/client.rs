//! A client by its catalog id, and the places one is written to.

use std::{cmp::Ordering, fmt, ops::Deref};

use serde::{de, Deserialize, Deserializer, Serialize, Serializer};

use super::clients::{self, ClientSpec};

/// A client AstrLink writes config files for. IPC and the record file name
/// it by its catalog id; an id no registered client has does not
/// deserialize.
#[derive(Clone, Copy)]
pub struct Client(&'static dyn ClientSpec);

impl Client {
    pub(crate) const fn new(spec: &'static dyn ClientSpec) -> Self {
        Self(spec)
    }

    /// Every registered client, in the order the dialog lists them.
    pub fn all() -> impl Iterator<Item = Self> {
        clients::REGISTERED.iter().copied()
    }

    /// The client with this id.
    pub fn find(id: &str) -> Option<Self> {
        clients::known().find(|client| client.id() == id)
    }

    /// Where the client sorts: the registry's order.
    fn rank(self) -> usize {
        clients::known()
            .position(|client| client.id() == self.id())
            .unwrap_or(usize::MAX)
    }
}

impl Deref for Client {
    type Target = dyn ClientSpec;

    fn deref(&self) -> &Self::Target {
        self.0
    }
}

impl PartialEq for Client {
    fn eq(&self, other: &Self) -> bool {
        self.id() == other.id()
    }
}

impl Eq for Client {}

impl Ord for Client {
    fn cmp(&self, other: &Self) -> Ordering {
        (self.rank(), self.id()).cmp(&(other.rank(), other.id()))
    }
}

impl PartialOrd for Client {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

impl fmt::Debug for Client {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.id())
    }
}

impl Serialize for Client {
    fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(self.id())
    }
}

impl<'de> Deserialize<'de> for Client {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let id = String::deserialize(deserializer)?;
        Self::find(&id).ok_or_else(|| de::Error::custom(format!("unknown client {id}")))
    }
}

/// Where in a home a client is written: the client, and for a client with
/// several write targets, which one. The record file keeps one record per
/// place, keyed `<client>` or `<client>:<target>`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Place {
    pub client: Client,
    pub target: Option<&'static str>,
}

impl Place {
    /// Checks a target the dialog names: a client with targets needs one of
    /// its own, and a client without takes none.
    pub fn new(client: Client, target: Option<&str>) -> Result<Self, String> {
        let targets = client.targets();
        match target {
            None if targets.is_empty() => Ok(Self {
                client,
                target: None,
            }),
            None => Err(format!("{} needs a target", client.id())),
            Some(name) => targets
                .iter()
                .find(|known| **known == name)
                .map(|target| Self {
                    client,
                    target: Some(target),
                })
                .ok_or_else(|| format!("{} has no target {name}", client.id())),
        }
    }

    /// Every place of `client`, in its targets' order.
    pub fn all(client: Client) -> Vec<Self> {
        match client.targets() {
            [] => vec![Self {
                client,
                target: None,
            }],
            targets => targets
                .iter()
                .map(|target| Self {
                    client,
                    target: Some(target),
                })
                .collect(),
        }
    }

    /// The record file's key.
    pub(crate) fn key(self) -> String {
        match self.target {
            None => self.client.id().to_string(),
            Some(target) => format!("{}:{target}", self.client.id()),
        }
    }

    pub(crate) fn from_key(key: &str) -> Option<Self> {
        let (id, target) = match key.split_once(':') {
            Some((id, target)) => (id, Some(target)),
            None => (key, None),
        };
        Self::new(Client::find(id)?, target).ok()
    }

    /// Where the target sorts: the client's targets' order.
    fn target_rank(self) -> Option<usize> {
        self.target.map(|target| {
            self.client
                .targets()
                .iter()
                .position(|known| *known == target)
                .unwrap_or(usize::MAX)
        })
    }
}

impl From<Client> for Place {
    /// The place of a client with one target.
    fn from(client: Client) -> Self {
        Self {
            client,
            target: None,
        }
    }
}

impl Ord for Place {
    fn cmp(&self, other: &Self) -> Ordering {
        (self.client, self.target_rank(), self.target).cmp(&(
            other.client,
            other.target_rank(),
            other.target,
        ))
    }
}

impl PartialOrd for Place {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

impl Serialize for Place {
    fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(&self.key())
    }
}

impl<'de> Deserialize<'de> for Place {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let key = String::deserialize(deserializer)?;
        Self::from_key(&key).ok_or_else(|| de::Error::custom(format!("unknown client {key}")))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn clients_go_by_their_ids_and_places_by_their_targets() {
        assert_eq!(serde_json::to_value(OPENCLAW).unwrap(), "openclaw");
        assert_eq!(serde_json::from_value::<Client>("pi".into()).unwrap(), PI);
        for id in ["Claude", "cursor", "trae", "claude:cn", ""] {
            let error = serde_json::from_value::<Client>(id.into()).unwrap_err();
            assert!(error.to_string().contains("unknown client"));
        }
        assert_eq!(Place::new(CLAUDE, None).unwrap(), Place::from(CLAUDE));
        for target in [None, Some("eu")] {
            assert!(Place::new(PAIR, target).is_err());
        }
        assert!(Place::new(CLAUDE, Some("cn")).is_err());
        let intl = Place::new(PAIR, Some("intl")).unwrap();
        assert_eq!(intl.key(), "pair:intl");
        assert_eq!(Place::from_key("pair:intl"), Some(intl));
        assert_eq!(Place::from_key("codex"), Some(CODEX.into()));
        for key in ["pair", "pair:eu", "claude:cn", "claude:", "cursor"] {
            assert_eq!(Place::from_key(key), None);
        }
        // Places sort as the dialog lists them: by client, then by target.
        let mut places = vec![intl, PI.into(), Place::new(PAIR, Some("cn")).unwrap()];
        places.push(CLAUDE.into());
        places.sort();
        assert_eq!(
            places.into_iter().map(Place::key).collect::<Vec<_>>(),
            ["claude", "pi", "pair:cn", "pair:intl"]
        );
        assert_eq!(Place::all(PAIR).len(), 2);
        assert_eq!(Place::all(GEMINI), [Place::from(GEMINI)]);
    }
}
