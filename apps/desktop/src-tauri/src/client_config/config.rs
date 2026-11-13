//! A client's config files, opened together, and why one cannot be edited.

use std::{fmt, path::PathBuf};

use serde::Serialize;

use super::{
    client::Client,
    clients::{ConfigFile, PROVIDER_ID},
    format::Document,
    models::clean_model,
};

/// Why one of a client's config files cannot be edited, by the file's
/// position in `ClientSpec::files`.
#[derive(Debug)]
pub struct FileError {
    pub file: usize,
    pub message: String,
    /// The 1-based line the file stops parsing at.
    pub line: Option<usize>,
}

impl FileError {
    pub(crate) fn at(self, paths: &[PathBuf]) -> ItemError {
        ItemError {
            message: format!("{} {}", paths[self.file].display(), self.message),
            path: Some(paths[self.file].display().to_string()),
            line: self.line,
        }
    }
}

/// Why writing or removing one client's config failed. Never holds a value
/// from the files.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct ItemError {
    pub message: String,
    /// The file at fault, when one is.
    pub path: Option<String>,
    pub line: Option<usize>,
}

impl From<String> for ItemError {
    fn from(message: String) -> Self {
        Self {
            message,
            path: None,
            line: None,
        }
    }
}

impl From<&str> for ItemError {
    fn from(message: &str) -> Self {
        message.to_string().into()
    }
}

impl fmt::Display for ItemError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.message)
    }
}

impl From<ItemError> for String {
    fn from(error: ItemError) -> Self {
        error.message
    }
}

/// Every config file of one client, opened together. Each key lives in
/// exactly one of them.
pub(crate) struct Config {
    pub(crate) files: &'static [&'static dyn ConfigFile],
    pub(crate) documents: Vec<Document>,
    pub(crate) changed: Vec<bool>,
}

impl Config {
    pub(crate) fn parse(client: Client, existing: &[Option<&str>]) -> Result<Self, FileError> {
        let files = client.files();
        if existing.len() != files.len() {
            return Err(FileError {
                file: 0,
                message: "was read without its companion files".into(),
                line: None,
            });
        }
        let documents = files
            .iter()
            .zip(existing)
            .enumerate()
            .map(|(index, (file, text))| {
                Document::parse(file.format(), *text).map_err(|(message, line)| FileError {
                    file: index,
                    message,
                    line,
                })
            })
            .collect::<Result<_, _>>()?;
        Ok(Self {
            files,
            documents,
            changed: vec![false; files.len()],
        })
    }

    pub(crate) fn index(&self, path: &str) -> usize {
        self.files
            .iter()
            .position(|file| file.holds(path))
            .unwrap_or(0)
    }

    pub(crate) fn get(&self, path: &str) -> Option<Option<String>> {
        let index = self.index(path);
        self.files[index].get(&self.documents[index], path)
    }

    pub(crate) fn group(&self) -> Vec<(String, Option<String>)> {
        self.files
            .iter()
            .zip(&self.documents)
            .flat_map(|(file, document)| file.group(document))
            .collect()
    }

    pub(crate) fn set(&mut self, path: &str, value: &str) -> Result<(), FileError> {
        let file = self.index(path);
        self.changed[file] = true;
        self.files[file]
            .set(&mut self.documents[file], path, value)
            .map_err(|message| FileError {
                file,
                message,
                line: None,
            })
    }

    pub(crate) fn remove(&mut self, path: &str) {
        let file = self.index(path);
        self.changed[file] = true;
        self.files[file].remove(&mut self.documents[file], path);
    }

    /// New contents per file; a file left alone keeps its text, and a
    /// missing one stays empty.
    pub(crate) fn render(&self, existing: &[Option<&str>]) -> Vec<String> {
        self.documents
            .iter()
            .zip(&self.changed)
            .zip(existing)
            .map(|((document, changed), text)| {
                if *changed {
                    document.render()
                } else {
                    text.unwrap_or_default().to_string()
                }
            })
            .collect()
    }

    /// The model at `path`, when AstrLink could write it back.
    pub(crate) fn model(&self, path: &str) -> Option<String> {
        self.get(path).flatten().and_then(clean_model)
    }

    /// AstrLink's model in a client that names the provider first.
    pub(crate) fn provider_model(&self, path: &str) -> Option<String> {
        self.model(path)
            .and_then(|value| {
                value
                    .strip_prefix(&format!("{PROVIDER_ID}/"))
                    .map(str::to_string)
            })
            .and_then(clean_model)
    }
}

/// A JSON config file as the client reads it, for importing the providers
/// it configures. Nothing here writes.
pub(crate) fn read_json(file: &dyn ConfigFile, text: &str) -> Result<serde_json::Value, String> {
    if text.trim().is_empty() {
        return Ok(serde_json::Value::Object(serde_json::Map::new()));
    }
    let Document::Json(json) =
        Document::parse(file.format(), Some(text)).map_err(|(message, _)| message)?
    else {
        return Err("is not valid JSON".into());
    };
    json.value().ok_or_else(|| "is not valid JSON".to_string())
}
