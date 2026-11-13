//! Dotenv files, edited entry by entry.

use super::Invalid;

/// One entry of a dotenv file: a `NAME=value` assignment, or anything else,
/// kept as written. A quoted value may run over several lines.
struct EnvEntry {
    text: String,
    assignment: Option<EnvAssignment>,
}

struct EnvAssignment {
    /// Everything before the name: indentation and `export `.
    prefix_len: usize,
    name: String,
    value: String,
}

/// A dotenv file edited entry by entry, read the way Gemini CLI's `dotenv`
/// reads it: `NAME=value` or `NAME: value`, `#` comments, optional
/// `export`, and quoted values that may span lines. Entries AstrLink does not
/// own keep their text.
pub(crate) struct EnvFile {
    entries: Vec<EnvEntry>,
    crlf: bool,
}

/// The name, separator end, and raw value of a dotenv assignment line.
fn env_assignment(line: &str) -> Option<(usize, &str, &str)> {
    let body = line.trim_start();
    let mut start = line.len() - body.len();
    let body = match body.strip_prefix("export") {
        Some(rest) if rest.starts_with([' ', '\t']) => {
            let rest = rest.trim_start();
            start = line.len() - rest.len();
            rest
        }
        _ => body,
    };
    let name_len = body
        .find(|c: char| !(c.is_ascii_alphanumeric() || matches!(c, '_' | '.' | '-')))
        .unwrap_or(body.len());
    if name_len == 0 {
        return None;
    }
    let (name, rest) = body.split_at(name_len);
    let trimmed = rest.trim_start();
    let value = match trimmed.strip_prefix('=') {
        Some(value) => value,
        None => rest
            .strip_prefix(':')
            .filter(|value| value.starts_with([' ', '\t']))?,
    };
    Some((start, name, value))
}

/// The closing quote of a value opening with `quote`, as a byte offset.
pub(crate) fn closing_quote(text: &str, quote: char) -> Option<usize> {
    let mut escaped = false;
    for (index, c) in text.char_indices().skip(1) {
        match c {
            '\\' if !escaped => escaped = true,
            c if c == quote && !escaped => return Some(index),
            _ => escaped = false,
        }
    }
    None
}

impl EnvFile {
    /// Values of the `single_line` names, the ones AstrLink writes, may not
    /// span lines.
    pub(crate) fn parse(text: &str, single_line: &[&str]) -> Result<Self, Invalid> {
        let crlf = text.contains("\r\n");
        let mut lines = text
            .split('\n')
            .map(|line| line.strip_suffix('\r').unwrap_or(line))
            .collect::<Vec<_>>();
        if lines.last() == Some(&"") {
            lines.pop();
        }
        let mut entries = Vec::new();
        let mut index = 0;
        while index < lines.len() {
            let line = lines[index];
            index += 1;
            let Some((prefix_len, name, raw)) = env_assignment(line) else {
                entries.push(EnvEntry {
                    text: line.to_string(),
                    assignment: None,
                });
                continue;
            };
            let raw_trimmed = raw.trim_start();
            let quote = raw_trimmed
                .chars()
                .next()
                .filter(|c| matches!(c, '"' | '\'' | '`'));
            let mut text = line.to_string();
            let value = match quote {
                Some(quote) => {
                    // A quoted value runs to its closing quote, across lines.
                    let mut joined = raw_trimmed.to_string();
                    let mut end = closing_quote(&joined, quote);
                    let mut taken = 0;
                    while end.is_none() && index + taken < lines.len() {
                        joined.push('\n');
                        joined.push_str(lines[index + taken]);
                        taken += 1;
                        end = closing_quote(&joined, quote);
                    }
                    match end {
                        Some(end) => {
                            for extra in &lines[index..index + taken] {
                                text.push('\n');
                                text.push_str(extra);
                            }
                            index += taken;
                            let inner = &joined[1..end];
                            if quote == '"' {
                                inner.replace("\\n", "\n").replace("\\r", "\r")
                            } else {
                                inner.to_string()
                            }
                        }
                        // Never closed: dotenv reads the line as unquoted.
                        None => raw.split('#').next().unwrap_or_default().trim().to_string(),
                    }
                }
                None => raw.split('#').next().unwrap_or_default().trim().to_string(),
            };
            if text.contains('\n') && single_line.contains(&name) {
                return Err((
                    format!("has a value for {name} that spans several lines"),
                    Some(index - text.matches('\n').count()),
                ));
            }
            entries.push(EnvEntry {
                assignment: Some(EnvAssignment {
                    prefix_len,
                    name: name.to_string(),
                    value,
                }),
                text,
            });
        }
        Ok(Self { entries, crlf })
    }

    fn position(&self, name: &str) -> Option<usize> {
        self.entries.iter().rposition(|entry| {
            entry
                .assignment
                .as_ref()
                .is_some_and(|assignment| assignment.name == name)
        })
    }

    /// dotenv keeps the last of repeated names.
    pub(crate) fn get(&self, name: &str) -> Option<String> {
        let index = self.position(name)?;
        self.entries[index]
            .assignment
            .as_ref()
            .map(|assignment| assignment.value.clone())
    }

    pub(crate) fn names(&self) -> Vec<String> {
        let mut names = Vec::new();
        for entry in &self.entries {
            if let Some(assignment) = &entry.assignment {
                if !names.contains(&assignment.name) {
                    names.push(assignment.name.clone());
                }
            }
        }
        names
    }

    pub(crate) fn encode(value: &str) -> Result<String, String> {
        if !value.is_empty()
            && value
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || "_-./:@+,~=[]%".contains(c))
        {
            Ok(value.to_string())
        } else if !value.contains(['"', '\\', '\n']) {
            Ok(format!("\"{value}\""))
        } else if !value.contains(['\'', '\n']) {
            Ok(format!("'{value}'"))
        } else {
            Err("has a value that cannot be written to .env".into())
        }
    }

    /// Rewrites the last line naming `name` in place and drops earlier ones,
    /// which it shadows; a new name goes at the end.
    pub(crate) fn set(&mut self, name: &str, value: &str) -> Result<(), String> {
        let encoded = Self::encode(value)?;
        let Some(index) = self.position(name) else {
            self.entries.push(EnvEntry {
                text: format!("{name}={encoded}"),
                assignment: Some(EnvAssignment {
                    prefix_len: 0,
                    name: name.to_string(),
                    value: value.to_string(),
                }),
            });
            return Ok(());
        };
        let entry = &mut self.entries[index];
        let prefix_len = entry.assignment.as_ref().map_or(0, |a| a.prefix_len);
        entry.text = format!("{}{name}={encoded}", &entry.text[..prefix_len]);
        entry.assignment = Some(EnvAssignment {
            prefix_len,
            name: name.to_string(),
            value: value.to_string(),
        });
        let mut at = 0;
        self.entries.retain(|entry| {
            let keep = at >= index
                || entry
                    .assignment
                    .as_ref()
                    .map_or(true, |assignment| assignment.name != name);
            at += 1;
            keep
        });
        Ok(())
    }

    pub(crate) fn remove(&mut self, name: &str) {
        self.entries.retain(|entry| {
            entry
                .assignment
                .as_ref()
                .map_or(true, |assignment| assignment.name != name)
        });
    }

    pub(crate) fn is_empty(&self) -> bool {
        self.entries
            .iter()
            .all(|entry| entry.text.trim().is_empty())
    }

    pub(crate) fn render(&self) -> String {
        if self.entries.is_empty() {
            return String::new();
        }
        let mut text = self
            .entries
            .iter()
            .map(|entry| entry.text.as_str())
            .collect::<Vec<_>>()
            .join("\n");
        text.push('\n');
        if self.crlf {
            text = text.replace('\n', "\r\n");
        }
        text
    }
}
