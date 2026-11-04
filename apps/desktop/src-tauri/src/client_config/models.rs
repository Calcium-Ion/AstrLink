//! The models a client is written with, as the dialog sends them and as a
//! config holds them, and what the official catalog says about each.

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

use super::{client::Client, plan::Connection};
use crate::sidecar::CoreManager;

/// The most models one switchable list takes.
pub(crate) const MAX_LISTED_MODELS: usize = 500;
/// The largest limit a model entry takes, in tokens.
pub(crate) const MAX_PI_LIMIT: u64 = 100_000_000;

#[derive(Clone, Default, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Models {
    pub model: Option<String>,
    pub haiku_model: Option<String>,
    pub sonnet_model: Option<String>,
    pub opus_model: Option<String>,
    pub fable_model: Option<String>,
    /// Claude Code only: the model its subagents use.
    pub subagent_model: Option<String>,
    /// Codex only: one of its reasoning efforts.
    pub reasoning_effort: Option<String>,
    /// Clients that switch models: the models the client can switch among.
    pub models: Option<Vec<String>>,
    /// Pi only: the context window and output limit its entry for the
    /// default model names.
    pub context_window: Option<u64>,
    pub max_tokens: Option<u64>,
    /// The gateway protocol each model is written with, one of the client's;
    /// a model left out takes the client's first.
    pub model_protocols: Option<BTreeMap<String, String>>,
}

impl Models {
    /// A model field by its camelCase name.
    fn field(&self, name: &str) -> Option<&str> {
        match name {
            "model" => self.model.as_deref(),
            "haikuModel" => self.haiku_model.as_deref(),
            "sonnetModel" => self.sonnet_model.as_deref(),
            "opusModel" => self.opus_model.as_deref(),
            "fableModel" => self.fable_model.as_deref(),
            "subagentModel" => self.subagent_model.as_deref(),
            _ => None,
        }
    }

    /// The context window and output limit for Pi's entry.
    pub(crate) fn pi_limits(&self, client: Client) -> Result<(Option<u64>, Option<u64>), String> {
        let limits = (self.context_window, self.max_tokens);
        if !client.takes_limits() {
            return match limits {
                (None, None) => Ok(limits),
                _ => Err("context and output limits apply to Pi only".into()),
            };
        }
        for (name, limit) in [("contextWindow", limits.0), ("maxTokens", limits.1)] {
            if limit.is_some_and(|limit| limit == 0 || limit > MAX_PI_LIMIT) {
                return Err(format!("invalid {name}"));
            }
        }
        Ok(limits)
    }

    /// The filled-in models, trimmed and named by their camelCase field.
    /// Each client takes its own fields and ignores the rest.
    pub fn fields(&self, client: Client) -> Result<Vec<(&'static str, String)>, String> {
        let mut values = Vec::new();
        for key in client.model_fields() {
            let Some(value) = self
                .field(key)
                .map(str::trim)
                .filter(|value| !value.is_empty())
            else {
                continue;
            };
            if !valid_model(value) {
                return Err(format!("invalid {key}"));
            }
            values.push((*key, value.to_string()));
        }
        if client.requires_model() && values.is_empty() {
            return Err("model is required for this client".into());
        }
        let efforts = client.reasoning_efforts();
        if let Some(effort) = self
            .reasoning_effort
            .as_deref()
            .filter(|_| !efforts.is_empty())
        {
            if !efforts.contains(&effort) {
                return Err("invalid reasoningEffort".into());
            }
            values.push(("reasoningEffort", effort.to_string()));
        }
        Ok(values)
    }

    /// The models a client that switches models lists, each once, with the
    /// default model added when the list lacks it.
    fn list(&self, client: Client, model: Option<&str>) -> Result<Vec<String>, String> {
        let Some(models) = &self.models else {
            return Ok(model
                .filter(|_| client.lists_models())
                .map(str::to_string)
                .into_iter()
                .collect());
        };
        if !client.lists_models() {
            return Err("a model list applies to OpenCode, OpenClaw, and Pi only".into());
        }
        if models.len() > MAX_LISTED_MODELS {
            return Err("too many models".into());
        }
        let mut list: Vec<String> = Vec::new();
        for value in models {
            let value = value.trim();
            if value.is_empty() || !valid_model(value) {
                return Err("invalid models".into());
            }
            if !list.iter().any(|known| known == value) {
                list.push(value.to_string());
            }
        }
        if let Some(model) = model.filter(|model| !list.iter().any(|known| known == model)) {
            list.insert(0, model.to_string());
        }
        Ok(list)
    }

    /// The protocol chosen for each model, each one of the client's.
    fn protocols(&self, client: Client) -> Result<BTreeMap<String, &'static str>, String> {
        let Some(chosen) = &self.model_protocols else {
            return Ok(BTreeMap::new());
        };
        // A full list and the default model it may lack.
        if chosen.len() > MAX_LISTED_MODELS + 1 {
            return Err("too many modelProtocols".into());
        }
        chosen
            .iter()
            .map(|(model, protocol)| {
                let model = model.trim();
                let protocol = client
                    .protocols()
                    .iter()
                    .find(|known| *known == protocol)
                    .filter(|_| !model.is_empty() && valid_model(model))
                    .ok_or_else(|| "invalid modelProtocols".to_string())?;
                Ok((model.to_string(), *protocol))
            })
            .collect()
    }
}

/// What one client is written with, checked.
#[derive(Clone, Debug, Default)]
pub struct Selection {
    pub fields: Vec<(&'static str, String)>,
    pub list: Vec<String>,
    /// Pi's limits for the default model.
    pub limits: (Option<u64>, Option<u64>),
    /// The protocol chosen for each model; see `Connection::protocol`.
    pub protocols: BTreeMap<String, &'static str>,
}

impl Selection {
    pub fn of(models: &Models, client: Client) -> Result<Self, String> {
        let limits = models.pi_limits(client)?;
        let fields = models.fields(client)?;
        let model = fields
            .iter()
            .find(|(field, _)| *field == "model")
            .map(|(_, model)| model.as_str());
        if let Some(model) = model {
            client.check_model(model)?;
        }
        let list = models.list(client, model)?;
        let protocols = models.protocols(client)?;
        Ok(Self {
            fields,
            list,
            limits,
            protocols,
        })
    }

    pub(crate) fn model(&self) -> Option<&str> {
        self.fields
            .iter()
            .find(|(field, _)| *field == "model")
            .map(|(_, model)| model.as_str())
    }

    /// The connection that writes this selection with `token`.
    pub(crate) fn connection<'a>(
        &'a self,
        target: Option<&'a str>,
        token_id: &'a str,
        token: &'a str,
        origin: &'a str,
    ) -> Connection<'a> {
        Connection {
            target,
            token_id,
            token,
            origin,
            models: &self.fields,
            list: &self.list,
            protocols: &self.protocols,
        }
    }
}

/// Whether a trimmed, non-empty model name fits in a client config.
pub(crate) fn valid_model(value: &str) -> bool {
    value.chars().count() <= 256 && !value.chars().any(char::is_control)
}

/// A model a config holds, trimmed, when AstrLink could write it back.
pub(crate) fn clean_model(value: String) -> Option<String> {
    let value = value.trim().to_string();
    (!value.is_empty() && valid_model(&value)).then_some(value)
}

/// The models a client's config holds now.
#[derive(Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CurrentModels {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub haiku_model: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sonnet_model: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub opus_model: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub fable_model: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub subagent_model: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub reasoning_effort: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub models: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub context_window: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max_tokens: Option<u64>,
}

impl CurrentModels {
    pub(crate) fn set(&mut self, field: &str, value: String) {
        let slot = match field {
            "model" => &mut self.model,
            "haikuModel" => &mut self.haiku_model,
            "sonnetModel" => &mut self.sonnet_model,
            "opusModel" => &mut self.opus_model,
            "fableModel" => &mut self.fable_model,
            "subagentModel" => &mut self.subagent_model,
            _ => &mut self.reasoning_effort,
        };
        *slot = Some(value);
    }
}

/// What AstrLink writes into a client's entry for a model besides its id.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ModelDetails {
    /// The official reasoning levels; `None` when the catalog lists no
    /// reasoning for the model. A model with no levels to choose keeps Pi's
    /// default range.
    pub reasoning: Option<Vec<String>>,
    pub context_window: Option<u64>,
    pub max_tokens: Option<u64>,
}

/// Entry details, by model.
pub type Catalog = BTreeMap<String, ModelDetails>;

/// Official reasoning levels and limits for each model, from the local
/// model catalog; a model it cannot answer for gets none.
pub(crate) async fn official_details(
    manager: &CoreManager,
    models: &[String],
    known: &mut Catalog,
) {
    for model in models {
        if known.contains_key(model) {
            continue;
        }
        let details = match manager
            .pricing("model", None, Some(serde_json::json!({ "model": model })))
            .await
        {
            Ok(metadata) => {
                let tokens = |key: &str| {
                    metadata[key]
                        .as_u64()
                        .filter(|tokens| (1..=MAX_PI_LIMIT).contains(tokens))
                };
                ModelDetails {
                    reasoning: metadata["reasoning"].as_bool().unwrap_or(false).then(|| {
                        metadata["reasoning_levels"]
                            .as_array()
                            .into_iter()
                            .flatten()
                            .filter_map(|level| level.as_str().map(str::to_string))
                            .collect()
                    }),
                    context_window: tokens("context_tokens"),
                    max_tokens: tokens("output_tokens"),
                }
            }
            Err(error) => {
                eprintln!("official details for {model} are unavailable: {error}");
                ModelDetails::default()
            }
        };
        known.insert(model.clone(), details);
    }
}

/// The catalog's details for each listed model, with the limits the user
/// set for Pi's default one.
pub(crate) fn catalog_for(selection: &Selection, known: &Catalog) -> Catalog {
    let mut catalog = selection
        .list
        .iter()
        .map(|model| (model.clone(), known.get(model).cloned().unwrap_or_default()))
        .collect::<Catalog>();
    if let Some(model) = selection
        .model()
        .filter(|_| selection.limits != (None, None))
    {
        let entry = catalog.entry(model.to_string()).or_default();
        (entry.context_window, entry.max_tokens) = selection.limits;
    }
    catalog
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn models_are_validated_and_fable_is_claude_only() {
        let models: Models = serde_json::from_value(serde_json::json!({
            "model": " main ", "fableModel": "fable-route"
        }))
        .unwrap();
        assert_eq!(
            models.fields(CLAUDE).unwrap(),
            [
                ("model", "main".to_string()),
                ("fableModel", "fable-route".to_string())
            ]
        );
        assert_eq!(
            models.fields(CODEX).unwrap(),
            [("model", "main".to_string())]
        );
        for key in [
            "model",
            "haikuModel",
            "sonnetModel",
            "opusModel",
            "fableModel",
        ] {
            for value in ["x".repeat(257), "invalid\nmodel".into()] {
                let models: Models =
                    serde_json::from_value(serde_json::json!({key: value})).unwrap();
                assert!(models.fields(CLAUDE).is_err());
            }
        }
        assert!(Models::default().fields(CODEX).is_err());
        assert!(serde_json::from_value::<Models>(serde_json::json!({"name": "x"})).is_err());
    }

    #[test]
    fn selections_check_each_clients_models() {
        let of = |client, value: serde_json::Value| {
            Selection::of(&serde_json::from_value(value).unwrap(), client)
        };
        assert!(of(GEMINI, serde_json::json!({"model": "a/b"})).is_err());
        assert!(of(GEMINI, serde_json::json!({"model": "astrlink/auto"})).is_ok());
        let pi = of(
            PI,
            serde_json::json!({"model": "b", "models": [" a ", "a", "c"], "contextWindow": 1000}),
        )
        .unwrap();
        assert_eq!(pi.list, ["b", "a", "c"]);
        assert_eq!(pi.limits, (Some(1000), None));
        assert!(of(CLAUDE, serde_json::json!({"models": ["a"]})).is_err());
        assert!(of(
            OPENCODE,
            serde_json::json!({"model": "a", "models": vec!["m"; 501]})
        )
        .is_err());
        assert!(of(
            CODEX,
            serde_json::json!({"model": "a", "reasoningEffort": "huge"})
        )
        .is_err());
        assert!(of(CODEX, serde_json::json!({"model": "a", "contextWindow": 1})).is_err());
        let codex = of(
            CODEX,
            serde_json::json!({"model": "a", "reasoningEffort": "xhigh"}),
        )
        .unwrap();
        assert_eq!(codex.fields[1], ("reasoningEffort", "xhigh".to_string()));
        // Fields one client does not take are left out.
        let gemini = of(
            GEMINI,
            serde_json::json!({"model": "a", "opusModel": "o", "reasoningEffort": "low"}),
        )
        .unwrap();
        assert_eq!(gemini.fields, [("model", "a".to_string())]);
    }

    #[test]
    fn model_protocols_name_one_of_the_clients_protocols() {
        let of = |client, value: serde_json::Value| {
            Selection::of(&serde_json::from_value(value).unwrap(), client)
        };
        let pair = of(
            PAIR,
            serde_json::json!({"model": "a", "modelProtocols": {" a ": "anthropic.messages"}}),
        )
        .unwrap();
        let connection = pair.connection(None, "token_01", TOKEN, ORIGIN);
        assert_eq!(connection.protocol(&*PAIR, "a"), "anthropic.messages");
        // A model left out takes the client's first.
        assert_eq!(connection.protocol(&*PAIR, "b"), "openai.chat");
        let opencode = of(
            OPENCODE,
            serde_json::json!({"model": "a", "models": ["b"], "modelProtocols": {"b": "openai.chat"}}),
        )
        .unwrap();
        assert_eq!(
            opencode
                .connection(None, "token_01", TOKEN, ORIGIN)
                .protocol(&*OPENCODE, "b"),
            "openai.chat"
        );
        for protocols in [
            serde_json::json!({"a": "anthropic.messages"}),
            serde_json::json!({"a": "gemini.generate"}),
            serde_json::json!({" ": "openai.chat"}),
            serde_json::json!({"x".repeat(257): "openai.chat"}),
        ] {
            assert!(of(
                OPENCODE,
                serde_json::json!({"model": "a", "modelProtocols": protocols})
            )
            .is_err());
        }
        assert!(of(
            CLAUDE,
            serde_json::json!({"modelProtocols": {"a": "openai.responses"}})
        )
        .is_err());
        let many = |count: usize| {
            (0..count)
                .map(|index| (format!("m{index}"), "openai.chat".to_string()))
                .collect::<BTreeMap<_, _>>()
        };
        assert!(of(
            OPENCODE,
            serde_json::json!({"model": "a", "modelProtocols": many(MAX_LISTED_MODELS + 1)})
        )
        .is_ok());
        assert!(of(
            OPENCODE,
            serde_json::json!({"model": "a", "modelProtocols": many(MAX_LISTED_MODELS + 2)})
        )
        .is_err());
    }
}
