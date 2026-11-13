//! Config file formats, each edited in place so that the comments, layout,
//! and keys AstrLink does not own stay as they are. Keys are dotted paths
//! from the file's root; a `.env` name is one key. `None` in a lookup result
//! is a value that is not a string. A new format, like YAML, is one more
//! module and one more variant of `Format` and `Document`.

pub(crate) mod env;
pub(crate) mod json;
pub(crate) mod toml;
pub(crate) mod yaml;

use jsonc_parser::ParseOptions;

pub(crate) use self::{
    env::EnvFile,
    json::JsonDocument,
    toml::TomlDocument,
    yaml::{YamlDocument, YamlRoot},
};

/// A parse failure, with the 1-based line it stops at.
pub(crate) type Invalid = (String, Option<usize>);

/// How a client reads one of its files.
pub(crate) enum Format {
    /// JSON with what the client reads beyond strict JSON (`None` for
    /// strict). Each of `objects` must be an object where it appears;
    /// the root is one too unless `root_list` lets it be a list.
    Json {
        syntax: Option<ParseOptions>,
        objects: &'static [&'static str],
        root_list: bool,
    },
    Toml,
    /// `NAME=value` lines. Values of the `single_line` names may not span
    /// lines.
    Env {
        single_line: &'static [&'static str],
    },
    /// YAML whose root is `root`, or nothing yet.
    Yaml {
        root: YamlRoot,
    },
}

/// A config file opened for in-place editing.
pub(crate) enum Document {
    Json(JsonDocument),
    Toml(TomlDocument),
    Env(EnvFile),
    Yaml(YamlDocument),
}

impl Document {
    pub(crate) fn parse(format: Format, existing: Option<&str>) -> Result<Self, Invalid> {
        let text = existing.unwrap_or_default();
        match format {
            Format::Json {
                syntax,
                objects,
                root_list,
            } => JsonDocument::parse(text, syntax.as_ref(), objects, root_list).map(Self::Json),
            Format::Toml => TomlDocument::parse(text).map(Self::Toml),
            Format::Env { single_line } => EnvFile::parse(text, single_line).map(Self::Env),
            Format::Yaml { root } => YamlDocument::parse(text, root).map(Self::Yaml),
        }
    }

    pub(crate) fn get(&self, key: &str) -> Option<Option<String>> {
        match self {
            Self::Json(json) => json.get(key),
            Self::Toml(toml) => toml.get(key),
            Self::Env(env) => env.get(key).map(Some),
            Self::Yaml(yaml) => yaml.get_key(key),
        }
    }

    pub(crate) fn set(&mut self, key: &str, value: &str) -> Result<(), String> {
        match self {
            Self::Json(json) => json.set(key, value),
            Self::Toml(toml) => {
                toml.set(key, value);
                Ok(())
            }
            Self::Env(env) => env.set(key, value),
            Self::Yaml(yaml) => yaml.set_key(key, value),
        }
    }

    pub(crate) fn remove(&mut self, key: &str) {
        match self {
            Self::Json(json) => json.remove(key),
            Self::Toml(toml) => toml.remove(key),
            Self::Env(env) => env.remove(key),
            Self::Yaml(yaml) => {
                // A key that cannot be removed in place stays; the check
                // after the removal reports the file.
                let _ = yaml.remove_key(key);
            }
        }
    }

    /// Drops each of the objects `containers` names, innermost first, that a
    /// removal left empty.
    pub(crate) fn drop_empty(&mut self, containers: &[&str]) {
        if let Self::Json(json) = self {
            json.drop_empty(containers);
        }
    }

    pub(crate) fn is_empty(&self) -> bool {
        match self {
            Self::Json(json) => json.is_empty(),
            Self::Toml(toml) => toml.is_empty(),
            Self::Env(env) => env.is_empty(),
            Self::Yaml(yaml) => yaml.is_empty(),
        }
    }

    pub(crate) fn render(&self) -> String {
        match self {
            Self::Json(json) => json.render(),
            Self::Toml(toml) => toml.render(),
            Self::Env(env) => env.render(),
            Self::Yaml(yaml) => yaml.render(),
        }
    }
}
