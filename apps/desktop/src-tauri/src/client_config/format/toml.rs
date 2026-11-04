//! TOML files edited through toml_edit, which keeps comments and layout.

use serde_json::{Map, Value as Json};
use toml_edit::{Array, DocumentMut, Item, Table, TableLike, Value};

use super::Invalid;

pub(crate) struct TomlDocument {
    pub(crate) document: DocumentMut,
    crlf: bool,
}

pub(crate) fn toml_string(item: &Item) -> Option<String> {
    item.as_str().map(str::to_string)
}

/// Replaces a string in place, keeping the comment and spacing around it.
pub(crate) fn set_toml_string(table: &mut dyn TableLike, key: &str, value: &str) {
    match table.get_mut(key) {
        Some(Item::Value(current)) => {
            let decor = current.decor().clone();
            *current = Value::from(value);
            *current.decor_mut() = decor;
        }
        Some(item) => *item = toml_edit::value(value),
        None => {
            table.insert(key, toml_edit::value(value));
        }
    }
}

/// A value or table as JSON; `None` for what JSON cannot hold.
pub(crate) fn json_of(item: &Item) -> Option<Json> {
    fn value(item: &Value) -> Option<Json> {
        Some(match item {
            Value::String(text) => Json::String(text.value().clone()),
            Value::Integer(integer) => Json::from(*integer.value()),
            Value::Float(float) => Json::from(*float.value()),
            Value::Boolean(boolean) => Json::Bool(*boolean.value()),
            Value::Datetime(datetime) => Json::String(datetime.value().to_string()),
            Value::Array(array) => Json::Array(array.iter().map(value).collect::<Option<_>>()?),
            Value::InlineTable(table) => Json::Object(
                table
                    .iter()
                    .map(|(name, inner)| Some((name.to_string(), value(inner)?)))
                    .collect::<Option<_>>()?,
            ),
        })
    }
    match item {
        Item::Value(item) => value(item),
        Item::Table(table) => Some(Json::Object(
            table
                .iter()
                .map(|(name, item)| Some((name.to_string(), json_of(item)?)))
                .collect::<Option<_>>()?,
        )),
        _ => None,
    }
}

fn toml_value(value: &Json) -> Option<Value> {
    Some(match value {
        Json::String(text) => Value::from(text.as_str()),
        Json::Bool(boolean) => Value::from(*boolean),
        Json::Number(number) => match number.as_i64() {
            Some(integer) => Value::from(integer),
            None => Value::from(number.as_f64()?),
        },
        Json::Array(items) => {
            let mut array = Array::new();
            for item in items {
                array.push(toml_value(item)?);
            }
            Value::Array(array)
        }
        Json::Object(entries) => {
            let mut table = toml_edit::InlineTable::new();
            for (name, item) in entries {
                table.insert(name, toml_value(item)?);
            }
            Value::InlineTable(table)
        }
        Json::Null => return None,
    })
}

/// A table of `value`'s properties, in `rank` order.
pub(crate) fn table_of(value: &Json, rank: fn(&str) -> usize) -> Option<Table> {
    let mut entries: Vec<_> = value.as_object()?.iter().collect();
    entries.sort_by_key(|(name, _)| rank(name));
    let mut table = Table::new();
    for (name, value) in entries {
        table.insert(name, Item::Value(toml_value(value)?));
    }
    Some(table)
}

/// The table at `[parent]`, an implicit one made when missing.
pub(crate) fn parent_table<'d>(
    document: &'d mut DocumentMut,
    parent: &str,
) -> Result<&'d mut Table, String> {
    let root = document.as_table_mut();
    if !root.contains_key(parent) {
        let mut table = Table::new();
        table.set_implicit(true);
        root.insert(parent, Item::Table(table));
    }
    root.get_mut(parent)
        .and_then(Item::as_table_mut)
        .ok_or_else(|| format!("has a {parent} value that is not a table"))
}

/// Drops `[parent]` once a removal left it empty, unless a comment sits on
/// its header.
pub(crate) fn drop_empty_parent(document: &mut DocumentMut, parent: &str) {
    let root = document.as_table_mut();
    if root
        .get(parent)
        .and_then(Item::as_table)
        .is_some_and(|table| {
            table.is_empty()
                && (table.is_implicit()
                    || !table
                        .decor()
                        .prefix()
                        .and_then(|prefix| prefix.as_str())
                        .is_some_and(|prefix| prefix.contains('#')))
        })
    {
        root.remove(parent);
    }
}

/// The tables in `[parent]` whose names start with `prefix`, as one JSON
/// object by name; `None` when there are none.
pub(crate) fn prefixed_tables(
    document: &DocumentMut,
    parent: &str,
    prefix: &str,
) -> Option<Map<String, Json>> {
    let tables: Map<String, Json> = document
        .get(parent)?
        .as_table_like()?
        .iter()
        .filter(|(name, _)| name.starts_with(prefix))
        .filter_map(|(name, item)| Some((name.to_string(), json_of(item)?)))
        .collect();
    (!tables.is_empty()).then_some(tables)
}

pub(crate) fn remove_prefixed(document: &mut DocumentMut, parent: &str, prefix: &str) {
    let Some(tables) = document.get_mut(parent).and_then(Item::as_table_like_mut) else {
        return;
    };
    let named: Vec<String> = tables
        .iter()
        .map(|(name, _)| name.to_string())
        .filter(|name| name.starts_with(prefix))
        .collect();
    for name in named {
        tables.remove(&name);
    }
    drop_empty_parent(document, parent);
}

/// Replaces the tables `prefixed_tables` reads with `value`'s.
pub(crate) fn set_prefixed(
    document: &mut DocumentMut,
    parent: &str,
    prefix: &str,
    value: &Json,
    rank: fn(&str) -> usize,
) -> Result<(), String> {
    let broken = || format!("{parent} tables cannot be written as TOML");
    let tables = value.as_object().ok_or_else(broken)?;
    remove_prefixed(document, parent, prefix);
    let into = parent_table(document, parent)?;
    for (name, entry) in tables {
        into.insert(name, Item::Table(table_of(entry, rank).ok_or_else(broken)?));
    }
    Ok(())
}

/// Removes a root key but keeps the comment above it, handing it to the
/// neighbouring key: a file's header note often sits above `model`.
fn remove_toml_root_key(document: &mut DocumentMut, key: &str) {
    let root = document.as_table_mut();
    let Some(index) = root.iter().position(|(name, _)| name == key) else {
        return;
    };
    let comment = root
        .key(key)
        .and_then(|key| key.leaf_decor().prefix())
        .and_then(|prefix| prefix.as_str())
        .filter(|prefix| prefix.contains('#'))
        .map(str::to_string);
    root.remove(key);
    let Some(comment) = comment else {
        return;
    };
    let values = root
        .iter()
        .enumerate()
        .filter(|(_, (_, item))| item.is_value())
        .map(|(position, (name, _))| (position, name.to_string()))
        .collect::<Vec<_>>();
    let raw = |text: Option<&toml_edit::RawString>| {
        text.and_then(|text| text.as_str())
            .unwrap_or_default()
            .to_string()
    };
    if let Some((_, next)) = values.iter().find(|(position, _)| *position >= index) {
        if let Some(mut key) = root.key_mut(next) {
            let decor = key.leaf_decor_mut();
            let rest = raw(decor.prefix());
            decor.set_prefix(format!("{comment}{rest}"));
        }
    } else if let Some((_, previous)) = values.last() {
        if let Some(value) = root.get_mut(previous).and_then(Item::as_value_mut) {
            let decor = value.decor_mut();
            let suffix = raw(decor.suffix());
            decor.set_suffix(format!("{suffix}\n{}", comment.trim_end_matches('\n')));
        }
    } else {
        let decor = root.decor_mut();
        let rest = raw(decor.prefix());
        decor.set_prefix(format!("{comment}{rest}"));
    }
}

impl TomlDocument {
    pub(crate) fn parse(text: &str) -> Result<Self, Invalid> {
        let mut document: DocumentMut = text.parse().map_err(|error: toml_edit::TomlError| {
            let line = error
                .span()
                .map(|span| text[..span.start.min(text.len())].matches('\n').count() + 1);
            ("is not valid TOML".to_string(), line)
        })?;
        // A file of comments alone keeps them as trailing text, below
        // anything added; as the root's leading text they stay on top.
        if document.as_table().is_empty() {
            let comments = document.trailing().as_str().unwrap_or_default().to_string();
            if !comments.is_empty() {
                document.set_trailing("");
                document.as_table_mut().decor_mut().set_prefix(comments);
            }
        }
        Ok(Self {
            document,
            crlf: text.contains("\r\n"),
        })
    }

    /// The string at a root key; `Some(None)` for another value.
    pub(crate) fn get(&self, key: &str) -> Option<Option<String>> {
        self.document.get(key).map(toml_string)
    }

    pub(crate) fn set(&mut self, key: &str, value: &str) {
        set_toml_string(self.document.as_table_mut(), key, value);
    }

    pub(crate) fn remove(&mut self, key: &str) {
        remove_toml_root_key(&mut self.document, key);
    }

    pub(crate) fn is_empty(&self) -> bool {
        self.document.to_string().trim().is_empty()
    }

    /// toml_edit writes LF; a CRLF file stays CRLF.
    pub(crate) fn render(&self) -> String {
        let text = self.document.to_string();
        if self.crlf {
            text.replace("\r\n", "\n").replace('\n', "\r\n")
        } else {
            text
        }
    }
}
