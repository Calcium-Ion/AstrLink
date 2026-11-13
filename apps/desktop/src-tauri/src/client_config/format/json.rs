//! JSON, JSONC, and JSON5 files edited through jsonc-parser's CST, which
//! keeps comments, quoting, indentation, and the keys AstrLink does not own.

use jsonc_parser::{
    cst::{CstArray, CstInputValue, CstNode, CstObject, CstObjectProp, CstRootNode},
    JsonValue, ParseOptions,
};

use super::Invalid;

/// Strict JSON, as `ParseOptions` spell it. A client's syntax starts from
/// here and allows what the client reads.
pub(crate) fn strict() -> ParseOptions {
    ParseOptions {
        allow_comments: false,
        allow_loose_object_property_names: false,
        allow_trailing_commas: false,
        allow_missing_commas: false,
        allow_single_quoted_strings: false,
        allow_hexadecimal_numbers: false,
        allow_unary_plus_numbers: false,
        allow_bare_decimal_point_numbers: false,
        allow_non_finite_numbers: false,
        allow_extended_string_escapes: false,
    }
}

/// A JSON file whose root is an object, or a list of entries.
pub(crate) struct JsonDocument {
    root: CstRootNode,
    /// The root object; `None` when the root is a list.
    object: Option<CstObject>,
    /// Whether the text ends with a line break, as a new file does.
    final_newline: bool,
}

/// An entry of a list, as a key: `<list>[<id>]`, where `<list>` is the path
/// of the list (empty for a root list) and `<id>` the entry's `id`, or the
/// entry itself in a list of names.
fn list_entry(path: &str) -> Option<(&str, &str)> {
    let (list, id) = path.strip_suffix(']')?.split_once('[')?;
    Some((list, id))
}

/// Whether `node` is the entry `id` names: an object with that `id`, or that
/// string.
fn is_entry(node: &CstNode, id: &str) -> bool {
    match cst_json(node) {
        Some(serde_json::Value::Object(entry)) => {
            entry.get("id").and_then(serde_json::Value::as_str) == Some(id)
        }
        Some(serde_json::Value::String(name)) => name == id,
        _ => false,
    }
}

/// Splits a JSON key into the objects holding it and its property name.
/// No key AstrLink writes has a dot in a property name.
pub(crate) fn json_key(path: &str) -> (Vec<&str>, &str) {
    match path.rsplit_once('.') {
        Some((parents, name)) => (parents.split('.').collect(), name),
        None => (Vec::new(), path),
    }
}

fn json_parent(object: &CstObject, parents: &[&str]) -> Option<CstObject> {
    parents
        .iter()
        .try_fold(object.clone(), |parent, name| parent.object_value(name))
}

/// `value` as CST input, each object's properties in `rank` order, then by
/// name.
fn cst_input(value: &serde_json::Value, rank: fn(&str) -> usize) -> CstInputValue {
    match value {
        serde_json::Value::Null => CstInputValue::Null,
        serde_json::Value::Bool(value) => CstInputValue::Bool(*value),
        serde_json::Value::Number(number) => CstInputValue::Number(number.to_string()),
        serde_json::Value::String(text) => CstInputValue::String(text.clone()),
        serde_json::Value::Array(items) => {
            CstInputValue::Array(items.iter().map(|item| cst_input(item, rank)).collect())
        }
        serde_json::Value::Object(fields) => {
            let mut fields = fields.iter().collect::<Vec<_>>();
            fields.sort_by_key(|(name, _)| (rank(name), name.as_str()));
            CstInputValue::Object(
                fields
                    .into_iter()
                    .map(|(name, value)| (name.clone(), cst_input(value, rank)))
                    .collect(),
            )
        }
    }
}

/// The value of a node from a file a client reads.
fn cst_json(node: &CstNode) -> Option<serde_json::Value> {
    if let Some(object) = node.as_object() {
        let mut map = serde_json::Map::new();
        for property in object.properties() {
            map.insert(property.decoded_name()?, cst_json(&property.value()?)?);
        }
        return Some(serde_json::Value::Object(map));
    }
    if let Some(array) = node.as_array() {
        return array
            .elements()
            .iter()
            .map(cst_json)
            .collect::<Option<_>>()
            .map(serde_json::Value::Array);
    }
    if let Some(text) = node.as_string_lit() {
        return text.decoded_value().ok().map(serde_json::Value::String);
    }
    if let Some(boolean) = node.as_boolean_lit() {
        return Some(serde_json::Value::Bool(boolean.value()));
    }
    if let Some(number) = node.as_number_lit() {
        return serde_json::from_str::<serde_json::Number>(&number.to_string())
            .ok()
            .map(serde_json::Value::Number);
    }
    node.as_null_keyword().map(|_| serde_json::Value::Null)
}

/// Rejects what the client itself cannot read: the CST parser takes more.
/// `syntax` is `None` for strict JSON; `root_list` lets the root be a list.
fn validate(syntax: Option<&ParseOptions>, text: &str, root_list: bool) -> Result<(), Invalid> {
    let root_fits = match syntax {
        Some(options) => {
            let value = jsonc_parser::parse_to_value(text, options).map_err(|error| {
                (
                    format!("is not valid JSON ({})", error.kind()),
                    Some(error.line_display()),
                )
            })?;
            match value {
                Some(JsonValue::Object(_)) => true,
                Some(JsonValue::Array(_)) => root_list,
                _ => false,
            }
        }
        None => serde_json::from_str::<serde_json::Value>(text)
            .map_err(|error| {
                (
                    format!(
                        "is not valid JSON (line {}, column {})",
                        error.line(),
                        error.column()
                    ),
                    Some(error.line()),
                )
            })
            .map(|value| value.is_object() || (root_list && value.is_array()))?,
    };
    if root_fits {
        Ok(())
    } else {
        Err(("is not a JSON object".into(), None))
    }
}

impl JsonDocument {
    /// `syntax` is what the client reads beyond strict JSON (`None` for
    /// strict); each of `objects` must be an object where it appears, and
    /// `root_list` lets the root be a list.
    pub(crate) fn parse(
        text: &str,
        syntax: Option<&ParseOptions>,
        objects: &[&str],
        root_list: bool,
    ) -> Result<Self, Invalid> {
        // The CST parser takes more than the clients do, so validate first.
        if !text.trim().is_empty() {
            validate(syntax, text, root_list)?;
        }
        let root = CstRootNode::parse(text, &ParseOptions::default())
            .map_err(|error| ("is not valid JSON".to_string(), Some(error.line_display())))?;
        // A root list stays one; only an empty file becomes an object.
        let object = root
            .array_value()
            .is_none()
            .then(|| root.object_value_or_set());
        for container in objects {
            let (parents, name) = json_key(container);
            if let Some(property) = object
                .as_ref()
                .and_then(|object| json_parent(object, &parents))
                .and_then(|parent| parent.get(name))
            {
                if property.object_value().is_none() {
                    return Err((
                        format!("has a {container} value that is not an object"),
                        None,
                    ));
                }
            }
        }
        let final_newline = text.trim().is_empty() || text.ends_with('\n');
        Ok(Self {
            root,
            object,
            final_newline,
        })
    }

    /// The root object; `None` when the root is a list.
    pub(crate) fn object(&self) -> Option<&CstObject> {
        self.object.as_ref()
    }

    /// Whether the root is a list.
    pub(crate) fn is_list(&self) -> bool {
        self.object.is_none()
    }

    /// The property at `path`.
    pub(crate) fn property(&self, path: &str) -> Option<CstObjectProp> {
        let (parents, name) = json_key(path);
        json_parent(self.object.as_ref()?, &parents)?.get(name)
    }

    /// The list at `path`: the root list for an empty path.
    fn list(&self, path: &str) -> Option<CstArray> {
        if path.is_empty() {
            self.root.array_value()
        } else {
            self.property(path)?.value()?.as_array()
        }
    }

    /// The entry a `<list>[<id>]` key names.
    fn entry(&self, path: &str) -> Option<CstNode> {
        let (list, id) = list_entry(path)?;
        self.list(list)?
            .elements()
            .into_iter()
            .find(|node| is_entry(node, id))
    }

    /// The string at `path`; `Some(None)` for another value. A list entry
    /// reads as its name, or as compact JSON with sorted keys.
    pub(crate) fn get(&self, path: &str) -> Option<Option<String>> {
        if list_entry(path).is_some() {
            return self.entry(path).map(|node| match cst_json(&node) {
                Some(serde_json::Value::String(name)) => Some(name),
                Some(value) => Some(value.to_string()),
                None => None,
            });
        }
        let value = self.property(path)?.value();
        Some(
            value
                .and_then(|value| value.as_string_lit())
                .and_then(|value| value.decoded_value().ok()),
        )
    }

    /// The value at `path`, whatever it is; `Some(None)` when it does not
    /// read as JSON.
    pub(crate) fn get_json(&self, path: &str) -> Option<Option<serde_json::Value>> {
        let value = self.property(path)?.value();
        Some(value.as_ref().and_then(cst_json))
    }

    fn set_input(&mut self, path: &str, value: CstInputValue) -> Result<(), String> {
        if let Some((list, _)) = list_entry(path) {
            return self.set_entry(path, list, value);
        }
        let (parents, name) = json_key(path);
        let mut parent = self
            .object
            .clone()
            .ok_or_else(|| "is a list where an object is expected".to_string())?;
        for segment in parents {
            parent = parent
                .object_value_or_create(segment)
                .ok_or_else(|| format!("has a {segment} value that is not an object"))?;
        }
        match parent.get(name) {
            Some(property) => property.set_value(value),
            None => {
                parent.append(name, value);
            }
        }
        Ok(())
    }

    /// Replaces the entry a `<list>[<id>]` key names, or appends it, creating
    /// the list when the root is an object.
    fn set_entry(&mut self, path: &str, list: &str, value: CstInputValue) -> Result<(), String> {
        let id = list_entry(path).map(|(_, id)| id).unwrap_or_default();
        if let Some(array) = self.list(list) {
            let elements = array.elements();
            if let Some(index) = elements.iter().position(|node| is_entry(node, id)) {
                elements[index].clone().remove();
                array.insert(index, value);
                return Ok(());
            }
        }
        let array = match self.list(list) {
            Some(array) => array,
            None if list.is_empty() => {
                return Err("is an object where a list is expected".into());
            }
            None => {
                if self.property(list).is_some() {
                    return Err(format!("has a {list} value that is not a list"));
                }
                self.set_input(list, CstInputValue::Array(Vec::new()))?;
                self.list(list).expect("the list just set")
            }
        };
        // A one-line list of names stays on one line.
        if matches!(value, CstInputValue::Object(_)) {
            array.ensure_multiline();
        }
        array.append(value);
        Ok(())
    }

    /// Sets a string, creating the objects around it. A list entry's value
    /// is its name, or its JSON when it starts with `{`.
    pub(crate) fn set(&mut self, path: &str, value: &str) -> Result<(), String> {
        if list_entry(path).is_some() && value.starts_with('{') {
            let entry = serde_json::from_str(value).map_err(|error| error.to_string())?;
            return self.set_json(path, &entry, |_| usize::MAX);
        }
        self.set_input(path, CstInputValue::String(value.into()))
    }

    /// Sets any value; `rank` orders the properties of its objects.
    pub(crate) fn set_json(
        &mut self,
        path: &str,
        value: &serde_json::Value,
        rank: fn(&str) -> usize,
    ) -> Result<(), String> {
        self.set_input(path, cst_input(value, rank))
    }

    pub(crate) fn remove(&mut self, path: &str) {
        if list_entry(path).is_some() {
            if let Some(node) = self.entry(path) {
                node.remove();
            }
        } else if let Some(property) = self.property(path) {
            property.remove();
        }
    }

    /// Removes the items of the list at `path` that `drop` picks.
    pub(crate) fn remove_items(&mut self, path: &str, drop: impl Fn(&serde_json::Value) -> bool) {
        let Some(list) = self.list(path) else {
            return;
        };
        for item in list.elements() {
            if cst_json(&item).is_some_and(|value| drop(&value)) {
                item.remove();
            }
        }
    }

    /// The first item of the list at `path` that `pick` picks.
    pub(crate) fn find_item(
        &self,
        path: &str,
        pick: impl Fn(&serde_json::Value) -> bool,
    ) -> Option<serde_json::Value> {
        self.list(path)?
            .elements()
            .iter()
            .filter_map(cst_json)
            .find(|value| pick(value))
    }

    /// Replaces the first item of the list at `path` that `pick` picks with
    /// `item`, or appends `item`; `rank` orders its properties.
    pub(crate) fn put_item(
        &mut self,
        path: &str,
        pick: impl Fn(&serde_json::Value) -> bool,
        item: &serde_json::Value,
        rank: fn(&str) -> usize,
    ) -> Result<(), String> {
        let list = self
            .list(path)
            .ok_or_else(|| format!("has a {path} value that is not a list"))?;
        let elements = list.elements();
        let value = cst_input(item, rank);
        match elements
            .iter()
            .position(|node| cst_json(node).is_some_and(|current| pick(&current)))
        {
            Some(index) => {
                elements[index].clone().remove();
                list.insert(index, value);
            }
            None => {
                list.ensure_multiline();
                list.append(value);
            }
        }
        Ok(())
    }

    /// Puts `items` at the start of the list at `path`, creating the list;
    /// `rank` orders the properties of their objects.
    pub(crate) fn prepend_items(
        &mut self,
        path: &str,
        items: &[serde_json::Value],
        rank: fn(&str) -> usize,
    ) -> Result<(), String> {
        let list = match self.list(path) {
            Some(list) => list,
            None if self.property(path).is_some() => {
                return Err(format!("has a {path} value that is not a list"));
            }
            None => {
                self.set_input(path, CstInputValue::Array(Vec::new()))?;
                self.list(path).expect("the list just set")
            }
        };
        if items.is_empty() {
            return Ok(());
        }
        list.ensure_multiline();
        for (index, item) in items.iter().enumerate() {
            list.insert(index, cst_input(item, rank));
        }
        Ok(())
    }

    /// Drops each of `containers`, innermost first, that is an empty object.
    pub(crate) fn drop_empty(&mut self, containers: &[&str]) {
        for container in containers {
            if let Some(property) = self.property(container) {
                if property
                    .object_value()
                    .is_some_and(|value| value.properties().is_empty())
                {
                    property.remove();
                }
            }
        }
    }

    /// Whether every property of the root is one `kept` passes, by its name;
    /// true for an empty object.
    /// A root list holds only what it is empty of.
    pub(crate) fn holds_only(&self, kept: impl Fn(Option<&str>, &CstObjectProp) -> bool) -> bool {
        match &self.object {
            Some(object) => object
                .properties()
                .iter()
                .all(|property| kept(property.decoded_name().as_deref(), property)),
            None => self
                .root
                .array_value()
                .map_or(true, |list| list.elements().is_empty()),
        }
    }

    pub(crate) fn is_empty(&self) -> bool {
        self.holds_only(|_, _| false)
    }

    /// The properties of the object at `prefix` (ending in `.`) as keys, or
    /// the object itself when it is something else. Properties named in
    /// `expand` that hold objects list their own properties. `get` reads
    /// each value as the file's client reads it.
    pub(crate) fn object_keys(
        &self,
        prefix: &str,
        expand: &[&str],
        get: &dyn Fn(&str) -> Option<Option<String>>,
        keys: &mut Vec<(String, Option<String>)>,
    ) {
        let path = prefix.trim_end_matches('.');
        let Some(property) = self.property(path) else {
            return;
        };
        let Some(provider) = property.object_value() else {
            keys.push((path.into(), None));
            return;
        };
        for property in provider.properties() {
            let Some(name) = property.decoded_name() else {
                continue;
            };
            let path = format!("{prefix}{name}");
            if expand.contains(&name.as_str()) && property.object_value().is_some() {
                self.object_keys(&format!("{path}."), &[], get, keys);
            } else {
                let value = get(&path).flatten();
                keys.push((path, value));
            }
        }
    }

    /// The whole file as JSON.
    pub(crate) fn value(&self) -> Option<serde_json::Value> {
        self.root.value().as_ref().and_then(cst_json)
    }

    /// The text, ending as the file did: some clients save without a final
    /// line break.
    pub(crate) fn render(&self) -> String {
        let mut text = self.root.to_string();
        if self.final_newline && !text.ends_with('\n') {
            text.push('\n');
        }
        text
    }
}
