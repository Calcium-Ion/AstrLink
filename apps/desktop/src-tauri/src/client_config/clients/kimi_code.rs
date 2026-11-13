//! Kimi Code reads `~/.kimi-code/config.toml`. AstrLink adds one provider
//! table per protocol its models use, an `astrlink/<model>` alias table for
//! each model, and picks the default with `default_model`.

use std::path::PathBuf;

use serde_json::Value as Json;
use toml_edit::Item;

use super::{ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{
            toml::{
                drop_empty_parent, json_of, parent_table, prefixed_tables, remove_prefixed,
                set_prefixed, table_of,
            },
            Document, Format, TomlDocument,
        },
        models::{clean_model, Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&KimiCode);

struct KimiCode;

/// `config.toml`.
struct KimiConfig;

/// Protocol, provider id, and provider `type`.
const PROVIDERS: [(&str, &str, &str); 4] = [
    ("openai.chat", "astrlink-chat", "openai"),
    ("anthropic.messages", "astrlink-anthropic", "anthropic"),
    ("openai.responses", "astrlink-responses", "openai_responses"),
    ("google.generate_content", "astrlink-gemini", "google-genai"),
];
const PROVIDER_PREFIX: &str = "providers.";
/// Every `astrlink/<model>` alias table, as one JSON object by alias.
const MODELS_KEY: &str = "models.\"astrlink/*\"";
const ALIAS_PREFIX: &str = "astrlink/";
const DEFAULT_KEY: &str = "default_model";
/// What Kimi takes for a model of unknown context: a size is required.
const DEFAULT_CONTEXT: u64 = 131_072;
const EFFORTS: [&str; 5] = ["low", "medium", "high", "xhigh", "max"];

fn provider_key(id: &str) -> String {
    format!("{PROVIDER_PREFIX}{id}")
}

/// Anthropic and Gemini take the bare origin; OpenAI's APIs `/v1`.
fn provider_url(id: &str, origin: &str) -> String {
    match id {
        "astrlink-anthropic" | "astrlink-gemini" => origin.to_string(),
        _ => format!("{origin}/v1"),
    }
}

fn rank(name: &str) -> usize {
    const ORDER: [&str; 10] = [
        "type",
        "base_url",
        "api_key",
        "provider",
        "model",
        "max_context_size",
        "max_output_size",
        "capabilities",
        "support_efforts",
        "off_effort",
    ];
    ORDER
        .iter()
        .position(|known| *known == name)
        .unwrap_or(usize::MAX)
}

fn alias_entry(id: &str, provider: &str, details: &Catalog) -> Json {
    let details = details.get(id);
    let mut entry = serde_json::json!({
        "provider": provider,
        "model": id,
        "max_context_size": details
            .and_then(|details| details.context_window)
            .unwrap_or(DEFAULT_CONTEXT),
    });
    let Some(details) = details else {
        return entry;
    };
    if let Some(output) = details.max_tokens {
        entry["max_output_size"] = output.into();
    }
    if let Some(levels) = details
        .reasoning
        .as_ref()
        .filter(|levels| !levels.is_empty())
    {
        entry["capabilities"] = serde_json::json!(["thinking"]);
        let efforts: Vec<&str> = EFFORTS
            .into_iter()
            .filter(|effort| levels.iter().any(|level| level == effort))
            .collect();
        if !efforts.is_empty() {
            entry["support_efforts"] = efforts.into();
        }
        if levels.iter().any(|level| level == "none") {
            entry["off_effort"] = "none".into();
        }
    }
    entry
}

impl ConfigFile for KimiConfig {
    fn format(&self) -> Format {
        Format::Toml
    }

    fn get(&self, document: &Document, key: &str) -> Option<Option<String>> {
        let Document::Toml(TomlDocument { document: toml, .. }) = document else {
            return None;
        };
        if key == MODELS_KEY {
            let aliases = prefixed_tables(toml, "models", ALIAS_PREFIX)?;
            return Some(Some(Json::Object(aliases).to_string()));
        }
        let Some(id) = key.strip_prefix(PROVIDER_PREFIX) else {
            return document.get(key);
        };
        let item = toml.get("providers")?.as_table_like()?.get(id)?;
        Some(json_of(item).map(|value| value.to_string()))
    }

    fn set(&self, document: &mut Document, key: &str, value: &str) -> Result<(), String> {
        let Document::Toml(TomlDocument { document: toml, .. }) = document else {
            return document.set(key, value);
        };
        if key != MODELS_KEY && !key.starts_with(PROVIDER_PREFIX) {
            return document.set(key, value);
        }
        let value: Json = serde_json::from_str(value).map_err(|error| error.to_string())?;
        let broken = || format!("{key} cannot be written as TOML");
        if key == MODELS_KEY {
            return set_prefixed(toml, "models", ALIAS_PREFIX, &value, rank);
        }
        let id = &key[PROVIDER_PREFIX.len()..];
        let table = table_of(&value, rank).ok_or_else(broken)?;
        let providers = parent_table(toml, "providers")?;
        match providers.get_mut(id) {
            Some(Item::Table(current)) => {
                // Values change in place, keeping the table where it is.
                let decor = current.decor().clone();
                let position = current.position();
                *current = table;
                *current.decor_mut() = decor;
                if let Some(position) = position {
                    current.set_position(position);
                }
            }
            _ => {
                providers.insert(id, Item::Table(table));
            }
        }
        Ok(())
    }

    fn remove(&self, document: &mut Document, key: &str) {
        let Document::Toml(TomlDocument { document: toml, .. }) = document else {
            return;
        };
        if key == MODELS_KEY {
            remove_prefixed(toml, "models", ALIAS_PREFIX);
        } else if let Some(id) = key.strip_prefix(PROVIDER_PREFIX) {
            if let Some(providers) = toml.get_mut("providers").and_then(Item::as_table_like_mut) {
                providers.remove(id);
            }
            drop_empty_parent(toml, "providers");
        } else {
            document.remove(key);
        }
    }
}

impl ClientSpec for KimiCode {
    fn id(&self) -> &'static str {
        "kimi_code"
    }

    fn name(&self) -> &'static str {
        "Kimi Code"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &[
            "openai.chat",
            "anthropic.messages",
            "openai.responses",
            "google.generate_content",
        ]
    }

    fn lists_models(&self) -> bool {
        true
    }

    /// `KIMI_CODE_HOME` moves it from a shell; AstrLink writes the default.
    fn dir(&self, home: Home, _target: Option<&str>) -> PathBuf {
        home.join(".kimi-code")
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&KimiConfig]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self.dir(home, target).join("config.toml")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &[]
    }

    /// Each provider table holds an address and the token.
    fn holds_connection(&self, key: &str) -> bool {
        key.starts_with(PROVIDER_PREFIX)
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        let aliases: serde_json::Map<String, Json> = config
            .get(MODELS_KEY)
            .flatten()
            .and_then(|value| serde_json::from_str(&value).ok())
            .unwrap_or_default();
        let mut ids: Vec<String> = Vec::new();
        for entry in aliases.values() {
            if let Some(id) = entry
                .get("model")
                .and_then(Json::as_str)
                .and_then(|id| clean_model(id.into()))
            {
                if !ids.contains(&id) {
                    ids.push(id);
                }
            }
        }
        let chosen = config.get(DEFAULT_KEY).flatten().and_then(|alias| {
            aliases
                .get(&alias)?
                .get("model")?
                .as_str()
                .map(str::to_string)
        });
        CurrentModels {
            model: chosen.or_else(|| ids.first().cloned()),
            models: (!ids.is_empty()).then_some(ids),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, details: &Catalog) -> Vec<(String, String)> {
        let list = connection.list();
        let mut keys = Vec::new();
        let mut aliases = serde_json::Map::new();
        for (protocol, id, kind) in PROVIDERS {
            let models: Vec<&String> = list
                .iter()
                .filter(|model| connection.protocol(self, model) == protocol)
                .collect();
            if models.is_empty() {
                continue;
            }
            let provider = serde_json::json!({
                "type": kind,
                "base_url": provider_url(id, connection.origin),
                "api_key": connection.token,
            });
            keys.push((provider_key(id), provider.to_string()));
            for model in models {
                aliases.insert(
                    format!("{ALIAS_PREFIX}{model}"),
                    alias_entry(model, id, details),
                );
            }
        }
        if !aliases.is_empty() {
            keys.push((MODELS_KEY.into(), Json::Object(aliases).to_string()));
        }
        if let Some(model) = connection.model("model") {
            keys.push((DEFAULT_KEY.into(), format!("{ALIAS_PREFIX}{model}")));
        }
        keys
    }

    /// Unused: `synced_keys` points every provider elsewhere.
    fn base_url_key(&self) -> &'static str {
        ""
    }

    fn synced_keys(
        &self,
        config: &Config,
        record: &ClientRecord,
        origin: &str,
    ) -> Vec<(String, String)> {
        record
            .keys
            .keys()
            .filter_map(|key| {
                let id = key.strip_prefix(PROVIDER_PREFIX)?;
                let mut value: Json = serde_json::from_str(&config.get(key).flatten()?).ok()?;
                value["base_url"] = provider_url(id, origin).into();
                Some((key.clone(), value.to_string()))
            })
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    const FACTORY: &str = "# ~/.kimi-code/config.toml\n# Runtime settings for Kimi Code.\n# This file starts empty so built-in defaults can apply.\n# Login will populate managed Kimi provider and model entries.\n";

    fn config(home: &Path) -> PathBuf {
        home.join(".kimi-code/config.toml")
    }

    fn toml(text: &str) -> toml_edit::DocumentMut {
        text.parse().unwrap()
    }

    #[test]
    fn writes_a_provider_per_protocol_and_an_alias_per_model() {
        let home = unique_home("kimi");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".kimi-code")).unwrap();
        fs::write(config(&home), FACTORY).unwrap();
        let models = vec![("model", "claude-sonnet-5".to_string())];
        let list = strings(&["claude-sonnet-5", "deepseek-v4-pro", "gpt-5.5"]);
        let protocols = BTreeMap::from([
            ("claude-sonnet-5".to_string(), "anthropic.messages"),
            ("gpt-5.5".to_string(), "openai.responses"),
        ]);
        let chosen = Connection {
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        let details = Catalog::from([(
            "gpt-5.5".to_string(),
            ModelDetails {
                reasoning: Some(levels(&["none", "low", "medium", "high", "xhigh"])),
                context_window: Some(400_000),
                max_tokens: Some(128_000),
            },
        )]);
        let outcome = write_with(local, KIMI_CODE, &chosen, &details, &tokens(), false);
        assert_eq!(asked(outcome.unwrap()), None);
        let text = fs::read_to_string(config(&home)).unwrap();
        assert!(text.starts_with(FACTORY), "{text}");
        let written = toml(&text);
        assert_eq!(
            written["default_model"].as_str(),
            Some("astrlink/claude-sonnet-5")
        );
        let anthropic = &written["providers"]["astrlink-anthropic"];
        assert_eq!(anthropic["type"].as_str(), Some("anthropic"));
        assert_eq!(anthropic["base_url"].as_str(), Some(ORIGIN));
        assert_eq!(anthropic["api_key"].as_str(), Some(TOKEN));
        assert_eq!(
            written["providers"]["astrlink-chat"]["base_url"].as_str(),
            Some(format!("{ORIGIN}/v1").as_str())
        );
        assert_eq!(
            written["providers"]["astrlink-responses"]["type"].as_str(),
            Some("openai_responses")
        );
        assert!(written["providers"].get("astrlink-gemini").is_none());
        let gpt = &written["models"]["astrlink/gpt-5.5"];
        assert_eq!(gpt["provider"].as_str(), Some("astrlink-responses"));
        assert_eq!(gpt["model"].as_str(), Some("gpt-5.5"));
        assert_eq!(gpt["max_context_size"].as_integer(), Some(400_000));
        assert_eq!(gpt["max_output_size"].as_integer(), Some(128_000));
        assert_eq!(gpt["off_effort"].as_str(), Some("none"));
        assert_eq!(
            gpt["support_efforts"]
                .as_array()
                .unwrap()
                .iter()
                .filter_map(|v| v.as_str())
                .collect::<Vec<_>>(),
            ["low", "medium", "high", "xhigh"]
        );
        assert_eq!(
            written["models"]["astrlink/deepseek-v4-pro"]["max_context_size"].as_integer(),
            Some(131_072)
        );
        assert!(text.contains("[models.\"astrlink/gpt-5.5\"]"), "{text}");
        let status = &status_of(local, [KIMI_CODE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("claude-sonnet-5"));
        assert_eq!(status.models.models.as_ref().map(Vec::len), Some(3));

        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        let synced = toml(&fs::read_to_string(config(&home)).unwrap());
        assert_eq!(
            synced["providers"]["astrlink-anthropic"]["base_url"].as_str(),
            Some("http://127.0.0.1:9000")
        );
        assert_eq!(
            synced["providers"]["astrlink-chat"]["base_url"].as_str(),
            Some("http://127.0.0.1:9000/v1")
        );

        remove(local, KIMI_CODE).unwrap();
        assert_eq!(fs::read_to_string(config(&home)).unwrap(), FACTORY);
        fs::remove_dir_all(home).unwrap();
    }

    const LOGGED_IN: &str = r#"default_model = "kimi-code/k3"
default_permission_mode = "manual"
telemetry = true

[providers."managed:kimi-code"]
type = "kimi"
base_url = "https://api.kimi.com/coding/v1"
api_key = ""

[models."kimi-code/k3"]
provider = "managed:kimi-code"
model = "k3"
max_context_size = 1048576
capabilities = [ "thinking", "always_thinking", "image_in", "video_in", "tool_use" ]
support_efforts = [ "low", "high", "max" ]
default_effort = "max"

[thinking]
enabled = true
effort = "high"

[[permission.rules]]
decision = "allow"
pattern = "Read"
"#;

    #[test]
    fn joins_a_logged_in_config_and_puts_it_back() {
        let home = unique_home("kimi-merge");
        let local = Home::local(&home);
        fs::create_dir_all(home.join(".kimi-code")).unwrap();
        fs::write(config(&home), LOGGED_IN).unwrap();
        let models = vec![("model", "deepseek-v4-pro".to_string())];
        assert_eq!(
            asked(write(local, KIMI_CODE, &connection(&models), &tokens(), false).unwrap()),
            Some((strings(&["default_model"]), None))
        );
        write(local, KIMI_CODE, &connection(&models), &tokens(), true).unwrap();
        let written = toml(&fs::read_to_string(config(&home)).unwrap());
        assert_eq!(
            written["default_model"].as_str(),
            Some("astrlink/deepseek-v4-pro")
        );
        assert_eq!(
            written["models"]["kimi-code/k3"]["model"].as_str(),
            Some("k3")
        );
        assert_eq!(
            written["models"]["astrlink/deepseek-v4-pro"]["provider"].as_str(),
            Some("astrlink-chat")
        );

        // `/model` picks Kimi's own model again: AstrLink's config stays.
        let picked = fs::read_to_string(config(&home)).unwrap().replace(
            "default_model = \"astrlink/deepseek-v4-pro\"",
            "default_model = \"kimi-code/k3\"",
        );
        fs::write(config(&home), &picked).unwrap();
        let status = &status_of(local, [KIMI_CODE], Some(ORIGIN), Some(&tokens())).unwrap()[0];
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("deepseek-v4-pro"));
        remove(local, KIMI_CODE).unwrap();
        assert_eq!(fs::read_to_string(config(&home)).unwrap(), LOGGED_IN);
        fs::remove_dir_all(home).unwrap();
    }
}
