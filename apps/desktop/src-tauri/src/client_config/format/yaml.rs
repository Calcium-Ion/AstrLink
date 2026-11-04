//! YAML, edited in place with yaml-edit so comments, quoting, and layout
//! stay. yaml-edit is young, so every edit is checked before it counts: the
//! result must read back, with a second parser, as exactly the data the
//! edit means, and keep every comment line, line ending, and document
//! marker. Anything else leaves the file as it was.
//!
//! What AstrLink adds is block YAML it writes itself, quoting every string
//! that YAML 1.1 and 1.2 readers could read differently.

use std::panic::{catch_unwind, AssertUnwindSafe};

use serde_json::Value as Json;
use yaml_edit::{Document as EditDocument, Mapping, Sequence, YamlFile, YamlNode};
use yaml_serde::Value;

use super::Invalid;

/// What the root of a file must be.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum YamlRoot {
    Mapping,
    Sequence,
}

/// One step of a path into a document.
#[derive(Clone, Copy, Debug)]
pub(crate) enum Step<'a> {
    Key(&'a str),
    Index(usize),
}

pub(crate) struct YamlDocument {
    text: String,
    /// What `text` holds.
    value: Value,
    root: YamlRoot,
}

/// Why an edit was refused.
const UNSAFE_EDIT: &str = "has YAML AstrLink cannot change without touching the rest of the file";

fn read(text: &str) -> Result<Value, yaml_serde::Error> {
    yaml_serde::from_str(text)
}

/// Whether `a` and `b` hold the same data; an empty root is no root.
fn same(a: &Value, b: &Value) -> bool {
    let empty = |value: &Value| match value {
        Value::Null => true,
        Value::Mapping(map) => map.is_empty(),
        _ => false,
    };
    a == b || (empty(a) && empty(b))
}

fn yaml_value(value: &Json) -> Result<Value, String> {
    yaml_serde::to_value(value).map_err(|error| error.to_string())
}

/// A string as a plain scalar when every YAML reader takes it for that
/// string, else in double quotes.
fn scalar_text(text: &str) -> String {
    const WORDS: [&str; 10] = [
        "true", "false", "yes", "no", "on", "off", "y", "n", "null", "~",
    ];
    let plain = text
        .chars()
        .next()
        .is_some_and(|first| first.is_ascii_alphabetic() || matches!(first, '_' | '/'))
        && text
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || "_-./:+=".contains(c))
        && !text.ends_with(':')
        && !WORDS.contains(&text.to_ascii_lowercase().as_str());
    if plain {
        text.to_string()
    } else {
        Json::String(text.into()).to_string()
    }
}

fn leaf_text(value: &Json) -> String {
    match value {
        Json::String(text) => scalar_text(text),
        Json::Array(_) => "[]".into(),
        Json::Object(_) => "{}".into(),
        other => other.to_string(),
    }
}

fn is_leaf(value: &Json) -> bool {
    match value {
        Json::Array(items) => items.is_empty(),
        Json::Object(map) => map.is_empty(),
        _ => true,
    }
}

/// Block YAML for `value`, indented by `unit`, with list items as compact
/// `- key: value` and properties in `rank` order.
fn block(value: &Json, unit: usize, rank: fn(&str) -> usize) -> String {
    fn emit(
        value: &Json,
        indent: usize,
        unit: usize,
        rank: fn(&str) -> usize,
        inline_first: bool,
        out: &mut String,
    ) {
        match value {
            Json::Object(map) => {
                let mut entries: Vec<_> = map.iter().collect();
                entries.sort_by_key(|(name, _)| rank(name));
                for (index, (name, value)) in entries.into_iter().enumerate() {
                    if !(inline_first && index == 0) {
                        out.push_str(&" ".repeat(indent));
                    }
                    out.push_str(&scalar_text(name));
                    out.push(':');
                    if is_leaf(value) {
                        out.push(' ');
                        out.push_str(&leaf_text(value));
                        out.push('\n');
                    } else {
                        out.push('\n');
                        emit(value, indent + unit, unit, rank, false, out);
                    }
                }
            }
            Json::Array(items) => {
                for item in items {
                    out.push_str(&" ".repeat(indent));
                    out.push_str("- ");
                    if is_leaf(item) {
                        out.push_str(&leaf_text(item));
                        out.push('\n');
                    } else {
                        emit(item, indent + 2, unit, rank, true, out);
                    }
                }
            }
            leaf => {
                out.push_str(&" ".repeat(indent));
                out.push_str(&leaf_text(leaf));
                out.push('\n');
            }
        }
    }
    let mut out = String::new();
    emit(value, 0, unit, rank, false, &mut out);
    out
}

/// The file's indent step: its smallest indentation, else 2.
fn indent_unit(text: &str) -> usize {
    text.lines()
        .filter(|line| !line.trim().is_empty() && !line.trim_start().starts_with('#'))
        .map(|line| line.len() - line.trim_start_matches(' ').len())
        .filter(|indent| *indent > 0)
        .min()
        .unwrap_or(2)
}

fn has_marker(text: &str) -> bool {
    text.lines()
        .any(|line| line == "---" || line.starts_with("--- "))
}

fn bare_line_feed(text: &str) -> bool {
    text.match_indices('\n')
        .any(|(at, _)| at == 0 || text.as_bytes()[at - 1] != b'\r')
}

/// A value as yaml-edit takes it.
enum Piece {
    Text(String),
    Integer(i64),
    Float(f64),
    Bool(bool),
    Null,
    Mapping(Mapping),
    Sequence(Sequence),
}

impl Piece {
    fn of(value: &Json, unit: usize, rank: fn(&str) -> usize) -> Result<Self, String> {
        Ok(match value {
            Json::Null => Self::Null,
            Json::Bool(value) => Self::Bool(*value),
            Json::Number(number) => match number.as_i64() {
                Some(integer) => Self::Integer(integer),
                None => Self::Float(number.as_f64().ok_or(UNSAFE_EDIT)?),
            },
            Json::String(text) => Self::Text(text.clone()),
            Json::Object(map) if !map.is_empty() => Self::Mapping(
                fragment(value, unit, rank)?
                    .as_mapping()
                    .ok_or(UNSAFE_EDIT)?,
            ),
            Json::Array(items) if !items.is_empty() => Self::Sequence(
                fragment(value, unit, rank)?
                    .as_sequence()
                    .ok_or(UNSAFE_EDIT)?,
            ),
            Json::Object(_) => Self::Mapping(
                "{}".parse::<EditDocument>()
                    .map_err(|_| UNSAFE_EDIT)?
                    .as_mapping()
                    .ok_or(UNSAFE_EDIT)?,
            ),
            Json::Array(_) => Self::Sequence(
                "[]".parse::<EditDocument>()
                    .map_err(|_| UNSAFE_EDIT)?
                    .as_sequence()
                    .ok_or(UNSAFE_EDIT)?,
            ),
        })
    }
}

fn fragment(value: &Json, unit: usize, rank: fn(&str) -> usize) -> Result<EditDocument, String> {
    block(value, unit, rank)
        .parse::<EditDocument>()
        .map_err(|_| UNSAFE_EDIT.to_string())
}

/// Calls `$call` with the piece's value as yaml-edit takes it.
macro_rules! with_piece {
    ($piece:expr, |$value:ident| $call:expr) => {
        match $piece {
            Piece::Text(text) => {
                let $value = text.as_str();
                $call
            }
            Piece::Integer(integer) => {
                let $value = integer;
                $call
            }
            Piece::Float(float) => {
                let $value = float;
                $call
            }
            Piece::Bool(boolean) => {
                let $value = boolean;
                $call
            }
            Piece::Null => {
                let $value = None::<String>;
                $call
            }
            Piece::Mapping(mapping) => {
                let $value = &mapping;
                $call
            }
            Piece::Sequence(sequence) => {
                let $value = &sequence;
                $call
            }
        }
    };
}

fn root_node(document: &EditDocument) -> Option<YamlNode> {
    document
        .as_mapping()
        .map(YamlNode::Mapping)
        .or_else(|| document.as_sequence().map(YamlNode::Sequence))
}

fn node_at(document: &EditDocument, path: &[Step]) -> Option<YamlNode> {
    path.iter()
        .try_fold(root_node(document)?, |node, step| match step {
            Step::Key(key) => node.get(*key),
            Step::Index(index) => node.get_item(*index),
        })
}

fn value_at_mut<'v>(value: &'v mut Value, path: &[Step]) -> Option<&'v mut Value> {
    path.iter().try_fold(value, |node, step| match step {
        Step::Key(key) => node.as_mapping_mut()?.get_mut(*key),
        Step::Index(index) => node.as_sequence_mut()?.get_mut(*index),
    })
}

impl YamlDocument {
    pub(crate) fn parse(text: &str, root: YamlRoot) -> Result<Self, Invalid> {
        let value = read(text).map_err(|error| {
            (
                "is not valid YAML".to_string(),
                error.location().map(|location| location.line()),
            )
        })?;
        let fits = matches!(
            (&value, root),
            (Value::Null, _)
                | (Value::Mapping(_), YamlRoot::Mapping)
                | (Value::Sequence(_), YamlRoot::Sequence)
        );
        if !fits {
            let expected = match root {
                YamlRoot::Mapping => "is not a YAML mapping",
                YamlRoot::Sequence => "is not a YAML list",
            };
            return Err((expected.into(), None));
        }
        // yaml-edit must read the file as it is, or no edit can keep it.
        let edited = catch_unwind(|| text.parse::<YamlFile>().map(|file| file.to_string()));
        if !matches!(edited, Ok(Ok(ref same)) if same == text) {
            return Err((UNSAFE_EDIT.into(), None));
        }
        Ok(Self {
            text: text.to_string(),
            value,
            root,
        })
    }

    /// The data the file holds.
    pub(crate) fn value(&self) -> &Value {
        &self.value
    }

    pub(crate) fn get(&self, path: &[Step]) -> Option<&Value> {
        path.iter().try_fold(&self.value, |node, step| match step {
            Step::Key(key) => node.get(*key),
            Step::Index(index) => node.get(*index),
        })
    }

    /// The value at `path` as compact JSON with sorted keys, so reformatting
    /// the file changes nothing.
    pub(crate) fn get_json(&self, path: &[Step]) -> Option<Json> {
        serde_json::to_value(self.get(path)?).ok()
    }

    /// Runs `edit` on the text and keeps the result once it holds
    /// `expected`. `additions` also asks that every comment line stays.
    fn edit(
        &mut self,
        expected: Value,
        additions: bool,
        edit: impl FnOnce(&str) -> Result<String, String>,
    ) -> Result<(), String> {
        let out = catch_unwind(AssertUnwindSafe(|| edit(&self.text)))
            .map_err(|_| UNSAFE_EDIT.to_string())??;
        let kept = read(&out).is_ok_and(|value| same(&value, &expected))
            && !(self.text.contains("\r\n") && bare_line_feed(&out))
            && (has_marker(&self.text) || !has_marker(&out))
            && (!additions
                || self
                    .text
                    .lines()
                    .filter(|line| line.trim_start().starts_with('#'))
                    .all(|comment| out.lines().any(|line| line == comment)));
        if !kept {
            return Err(UNSAFE_EDIT.into());
        }
        self.text = out;
        self.value = expected;
        Ok(())
    }

    /// Sets `key` of the mapping at `parent`, replacing the whole value of a
    /// key it has.
    pub(crate) fn set(
        &mut self,
        parent: &[Step],
        key: &str,
        value: &Json,
        rank: fn(&str) -> usize,
    ) -> Result<(), String> {
        let mut expected = self.value.clone();
        if parent.is_empty() && expected.is_null() {
            if self.root != YamlRoot::Mapping {
                return Err(UNSAFE_EDIT.into());
            }
            expected = Value::Mapping(Default::default());
        }
        value_at_mut(&mut expected, parent)
            .and_then(Value::as_mapping_mut)
            .ok_or(UNSAFE_EDIT)?
            .insert(Value::String(key.into()), yaml_value(value)?);
        self.edit(expected, true, |text| {
            let unit = indent_unit(text);
            let file: YamlFile = text.parse().map_err(|_| UNSAFE_EDIT)?;
            let document = file
                .documents()
                .next()
                .unwrap_or_else(|| file.ensure_document());
            let piece = Piece::of(value, unit, rank)?;
            if parent.is_empty() {
                if !with_piece!(piece, |value| document.set(key, value)) {
                    return Err(UNSAFE_EDIT.into());
                }
            } else {
                let node = node_at(&document, parent).ok_or(UNSAFE_EDIT)?;
                let mapping = node.as_mapping().ok_or(UNSAFE_EDIT)?;
                with_piece!(piece, |value| mapping.set(key, value));
            }
            Ok(file.to_string())
        })
    }

    /// Appends `value` to the list at `parent`; an empty root becomes a
    /// list of it.
    pub(crate) fn push(
        &mut self,
        parent: &[Step],
        value: &Json,
        rank: fn(&str) -> usize,
    ) -> Result<(), String> {
        let mut expected = self.value.clone();
        if parent.is_empty() && expected.is_null() {
            if self.root != YamlRoot::Sequence {
                return Err(UNSAFE_EDIT.into());
            }
            expected = Value::Sequence(Vec::new());
        }
        value_at_mut(&mut expected, parent)
            .and_then(Value::as_sequence_mut)
            .ok_or(UNSAFE_EDIT)?
            .push(yaml_value(value)?);
        let empty_root = parent.is_empty() && self.value.as_sequence().map_or(true, Vec::is_empty);
        self.edit(expected, true, |text| {
            let unit = indent_unit(text);
            let newline = if text.contains("\r\n") { "\r\n" } else { "\n" };
            let items = block(&Json::Array(vec![value.clone()]), unit, rank).replace('\n', newline);
            if empty_root {
                return Ok(Self::fill_root(text, &items, newline));
            }
            let file: YamlFile = text.parse().map_err(|_| UNSAFE_EDIT)?;
            let document = file.documents().next().ok_or(UNSAFE_EDIT)?;
            let node = node_at(&document, parent).ok_or(UNSAFE_EDIT)?;
            let sequence = node.as_sequence().ok_or(UNSAFE_EDIT)?;
            let piece = Piece::of(value, unit, rank)?;
            with_piece!(piece, |value| sequence.push(value));
            Ok(file.to_string())
        })
    }

    /// An empty root list, `[]` or nothing below the comments, as block
    /// `items`: yaml-edit would grow `[]` into one long line.
    fn fill_root(text: &str, items: &str, newline: &str) -> String {
        let flow = text
            .parse::<YamlFile>()
            .ok()
            .and_then(|file| file.documents().next())
            .and_then(|document| document.as_sequence())
            .filter(|sequence| sequence.is_flow_style() && sequence.is_empty())
            .map(|sequence| sequence.byte_range());
        match flow {
            Some(range) if text[range.start as usize..range.end as usize].trim() == "[]" => {
                let (start, end) = (range.start as usize, range.end as usize);
                let rest = &text[end..];
                let items = if rest.is_empty() {
                    items
                } else {
                    items.trim_end()
                };
                format!("{}{items}{rest}", &text[..start])
            }
            _ => {
                let mut out = text.to_string();
                if !out.is_empty() && !out.ends_with('\n') {
                    out.push_str(newline);
                }
                out.push_str(items);
                out
            }
        }
    }

    /// Removes the key or item at `path`. A list emptied this way stays,
    /// as `[]`.
    pub(crate) fn remove(&mut self, path: &[Step]) -> Result<(), String> {
        let Some((last, parent)) = path.split_last() else {
            return Ok(());
        };
        if self.get(path).is_none() {
            return Ok(());
        }
        // The last key of a nested mapping leaves `{}`, not a null.
        if let (Step::Key(_), Some((Step::Key(name), grandparent))) = (last, parent.split_last()) {
            if self
                .get(parent)
                .and_then(Value::as_mapping)
                .is_some_and(|map| map.len() == 1)
            {
                return self.set(grandparent, name, &Json::Object(Default::default()), |_| 0);
            }
        }
        let mut expected = self.value.clone();
        let container = value_at_mut(&mut expected, parent).ok_or(UNSAFE_EDIT)?;
        match last {
            Step::Key(key) => {
                container
                    .as_mapping_mut()
                    .ok_or(UNSAFE_EDIT)?
                    .shift_remove(*key);
            }
            Step::Index(index) => {
                container
                    .as_sequence_mut()
                    .ok_or(UNSAFE_EDIT)?
                    .remove(*index);
            }
        }
        self.edit(expected, false, |text| {
            let file: YamlFile = text.parse().map_err(|_| UNSAFE_EDIT)?;
            let document = file.documents().next().ok_or(UNSAFE_EDIT)?;
            let removed = match (last, parent.is_empty()) {
                (Step::Key(key), true) => document.remove(*key).is_some(),
                (Step::Key(key), false) => node_at(&document, parent)
                    .and_then(|node| node.as_mapping()?.remove(*key))
                    .is_some(),
                (Step::Index(index), _) => node_at(&document, parent)
                    .and_then(|node| node.as_sequence()?.remove(*index))
                    .is_some(),
            };
            if !removed {
                return Err(UNSAFE_EDIT.into());
            }
            Ok(file.to_string())
        })
    }

    pub(crate) fn is_empty(&self) -> bool {
        match &self.value {
            Value::Null => true,
            Value::Mapping(map) => map.is_empty(),
            Value::Sequence(items) => items.is_empty(),
            _ => false,
        }
    }

    pub(crate) fn render(&self) -> String {
        self.text.clone()
    }

    // Dotted keys, for a mapping file: `refs.NAME`.

    pub(crate) fn get_key(&self, key: &str) -> Option<Option<String>> {
        let path: Vec<Step> = key.split('.').map(Step::Key).collect();
        self.get(&path)
            .map(|value| value.as_str().map(str::to_string))
    }

    /// Sets a string, creating the mappings around it.
    pub(crate) fn set_key(&mut self, key: &str, value: &str) -> Result<(), String> {
        self.set_key_json(key, &Json::String(value.into()))
    }

    /// Sets any value, creating the mappings around it.
    pub(crate) fn set_key_json(&mut self, key: &str, value: &Json) -> Result<(), String> {
        let names: Vec<&str> = key.split('.').collect();
        // The deepest mapping that exists takes the rest as one block.
        let mut depth = names.len() - 1;
        while depth > 0 {
            let parent: Vec<Step> = names[..depth].iter().copied().map(Step::Key).collect();
            if self.get(&parent).is_some() {
                break;
            }
            depth -= 1;
        }
        let parent: Vec<Step> = names[..depth].iter().copied().map(Step::Key).collect();
        let nested = names[depth + 1..].iter().rev().fold(
            value.clone(),
            |inner, name| serde_json::json!({ *name: inner }),
        );
        self.set(&parent, names[depth], &nested, |_| usize::MAX)
    }

    pub(crate) fn remove_key(&mut self, key: &str) -> Result<(), String> {
        let path: Vec<Step> = key.split('.').map(Step::Key).collect();
        self.remove(&path)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn rank(name: &str) -> usize {
        ["id", "config"]
            .iter()
            .position(|known| *known == name)
            .unwrap_or(usize::MAX)
    }

    const FACTORY: &str = "# Your patch layer for this dsh profile, applied after every bundle layer:\n# a top-level YAML array of loader patch entries (id-targeted config\n# overrides, disables, and insert lists; `!!js` expressions allowed).\n[]\n";

    #[test]
    fn an_empty_root_list_grows_block_items_and_shrinks_back() {
        let mut document = YamlDocument::parse(FACTORY, YamlRoot::Sequence).unwrap();
        let row = serde_json::json!({
            "id": "agent-default-model",
            "config": {"provider": "astrlink-chat", "model": "off"},
        });
        document.push(&[], &row, rank).unwrap();
        assert_eq!(
            document.render(),
            "# Your patch layer for this dsh profile, applied after every bundle layer:\n# a top-level YAML array of loader patch entries (id-targeted config\n# overrides, disables, and insert lists; `!!js` expressions allowed).\n- id: agent-default-model\n  config:\n    model: \"off\"\n    provider: astrlink-chat\n"
        );
        document.remove(&[Step::Index(0)]).unwrap();
        assert_eq!(
            read(&document.render()).unwrap(),
            Value::Sequence(Vec::new())
        );
        assert!(document.render().starts_with("# Your patch layer"));
    }

    #[test]
    fn edits_keep_comments_tags_and_line_endings() {
        let original = "# mine\r\n- id: tools\r\n  config:\r\n    mode: !!js process.env.MODE\r\n- id: llm-pi-ai\r\n  config:\r\n    providers:\r\n      gw: {api: openai-completions}  # hand-written\r\n";
        let mut document = YamlDocument::parse(original, YamlRoot::Sequence).unwrap();
        let providers = [Step::Index(1), Step::Key("config"), Step::Key("providers")];
        let route =
            serde_json::json!({"api": "anthropic-messages", "baseURL": "http://127.0.0.1:8317"});
        document
            .set(&providers, "astrlink-anthropic", &route, rank)
            .unwrap();
        let text = document.render();
        assert!(!bare_line_feed(&text), "{text:?}");
        assert!(text.contains("# hand-written") && text.contains("!!js process.env.MODE"));
        assert_eq!(
            document.get_json(&[
                Step::Index(1),
                Step::Key("config"),
                Step::Key("providers"),
                Step::Key("astrlink-anthropic")
            ]),
            Some(route)
        );
        document
            .remove(&[
                Step::Index(1),
                Step::Key("config"),
                Step::Key("providers"),
                Step::Key("astrlink-anthropic"),
            ])
            .unwrap();
        assert_eq!(document.render(), original);
    }

    #[test]
    fn dotted_keys_create_the_mappings_around_them() {
        let original = "version: 1\n# my keys\nrecords:\n    a: {kind: grant}\n";
        let mut document = YamlDocument::parse(original, YamlRoot::Mapping).unwrap();
        document
            .set_key("refs.ASTRLINK_WEB_API_KEY", "astr_x")
            .unwrap();
        assert_eq!(
            document.get_key("refs.ASTRLINK_WEB_API_KEY"),
            Some(Some("astr_x".into()))
        );
        assert!(document.render().contains("# my keys"));
        document.remove_key("refs.ASTRLINK_WEB_API_KEY").unwrap();
        assert_eq!(document.get_key("refs.ASTRLINK_WEB_API_KEY"), None);
        assert_eq!(
            document.get(&[Step::Key("refs")]),
            Some(&Value::Mapping(Default::default()))
        );
        let mut empty = YamlDocument::parse("", YamlRoot::Mapping).unwrap();
        empty.set_key("refs.NAME", "astr_y").unwrap();
        assert_eq!(
            read(&empty.render()).unwrap(),
            read("refs:\n  NAME: astr_y\n").unwrap()
        );
    }

    #[test]
    fn rejects_what_it_cannot_edit() {
        assert!(YamlDocument::parse("a: [1\n", YamlRoot::Mapping).is_err());
        assert!(YamlDocument::parse("{}\n", YamlRoot::Sequence).is_err());
        assert!(YamlDocument::parse("a: 1\n---\nb: 2\n", YamlRoot::Mapping).is_err());
    }
}
