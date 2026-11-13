//! Transactions over DimAgent's provider settings. Session and message tables
//! stay under DimAgent's storage implementation.

use std::{path::Path, time::Duration};

use rusqlite::{
    params, params_from_iter,
    types::{Value as SqlValue, ValueRef},
    Connection, OpenFlags, OptionalExtension, TransactionBehavior,
};
use serde_json::{json, Map, Value};

use super::{owned, ROUTES};

const JSON_COLUMNS: &[&str] = &[
    "auth",
    "credential",
    "accountUsageSettings",
    "headers",
    "retryConfig",
    "models",
    "modelConfigOverrides",
    "capabilityDefaults",
    "metadata",
];
const META_COLUMNS: &[&str] = &["providerId", "version", "createdAt", "updatedAt"];

enum Schema {
    Stable(Vec<String>),
    Native,
}

fn error(_: rusqlite::Error) -> String {
    "unable to access DimAgent settings; close DimAgent and try again".into()
}

fn columns(db: &Connection, table: &str) -> Result<Vec<String>, String> {
    let mut query = db
        .prepare(&format!("PRAGMA table_info({table})"))
        .map_err(error)?;
    let rows = query.query_map([], |row| row.get(1)).map_err(error)?;
    rows.collect::<Result<_, _>>().map_err(error)
}

fn schema(db: &Connection) -> Result<Schema, String> {
    let providers = columns(db, "providers")?;
    let selection = columns(db, "provider_selections")?;
    let has = |columns: &[String], names: &[&str]| {
        names
            .iter()
            .all(|name| columns.iter().any(|column| column == name))
    };
    if has(
        &providers,
        &[
            "providerId",
            "source",
            "displayName",
            "driverKind",
            "baseUrl",
            "auth",
            "credential",
            "headers",
            "models",
            "activeModelId",
            "modelsSource",
            "enabled",
            "createdAt",
            "updatedAt",
            "version",
        ],
    ) && has(
        &selection,
        &[
            "selectionId",
            "scope",
            "cwd",
            "providerId",
            "modelId",
            "createdAt",
            "updatedAt",
            "version",
        ],
    ) {
        return Ok(Schema::Stable(providers));
    }
    if providers == ["provider_id", "version", "record"]
        && has(&selection, &["scope", "version", "provider_id", "model_id"])
    {
        // The published Rust formats have the same provider representation.
        // A future format needs review before it can receive writes here.
        let versions = db
            .prepare("SELECT version FROM agent_store_format")
            .map_err(error)?
            .query_map([], |r| r.get::<_, u32>(0))
            .map_err(error)?
            .collect::<Result<Vec<_>, _>>()
            .map_err(error)?;
        if matches!(versions.as_slice(), [13] | [15..=34]) {
            return Ok(Schema::Native);
        }
    }
    Err("unsupported DimAgent settings format; no settings were changed".into())
}

fn open(path: &Path, write: bool) -> Result<Connection, String> {
    let flags = if write {
        OpenFlags::SQLITE_OPEN_READ_WRITE
    } else {
        OpenFlags::SQLITE_OPEN_READ_ONLY
    };
    let db = Connection::open_with_flags(path, flags | OpenFlags::SQLITE_OPEN_NO_MUTEX)
        .map_err(error)?;
    db.busy_timeout(Duration::from_secs(2)).map_err(error)?;
    Ok(db)
}

fn identifier(name: &str) -> String {
    format!("\"{}\"", name.replace('"', "\"\""))
}

fn snapshot(db: &Connection, schema: &Schema) -> Result<Value, String> {
    let mut providers = Map::new();
    for (_, id, _) in ROUTES {
        let record = match schema {
            Schema::Native => {
                let row: Option<(u32, String)> = db
                    .query_row(
                        "SELECT version,record FROM providers WHERE provider_id=?1",
                        [id],
                        |r| {
                            if r.get_ref(1)?.as_str()?.len() > 4_194_304 {
                                return Err(rusqlite::Error::InvalidQuery);
                            }
                            Ok((r.get(0)?, r.get(1)?))
                        },
                    )
                    .optional()
                    .map_err(error)?;
                row.map(|(version, raw)| -> Result<Value, String> {
                    let mut record: Value = serde_json::from_str(&raw)
                        .map_err(|_| "invalid DimAgent provider settings")?;
                    let map = record
                        .as_object_mut()
                        .ok_or("invalid DimAgent provider settings")?;
                    if map.remove("version") != Some(version.into()) {
                        return Err("DimAgent provider version mismatch".into());
                    }
                    Ok(record)
                })
                .transpose()?
            }
            Schema::Stable(columns) => {
                let sql = format!(
                    "SELECT {} FROM providers WHERE providerId=?1",
                    columns
                        .iter()
                        .map(|c| identifier(c))
                        .collect::<Vec<_>>()
                        .join(",")
                );
                db.query_row(&sql, [id], |row| {
                    let mut values = Map::new();
                    for (index, name) in columns.iter().enumerate() {
                        if META_COLUMNS.contains(&name.as_str()) {
                            continue;
                        }
                        let value = match row.get_ref(index)? {
                            ValueRef::Null => continue,
                            ValueRef::Integer(n) if name == "enabled" => Value::Bool(n != 0),
                            ValueRef::Integer(n) => n.into(),
                            ValueRef::Real(n) => json!(n),
                            ValueRef::Text(text) if text.len() <= 4_194_304 => {
                                let text = std::str::from_utf8(text)
                                    .map_err(|_| rusqlite::Error::InvalidQuery)?;
                                if JSON_COLUMNS.contains(&name.as_str()) {
                                    serde_json::from_str(text)
                                        .map_err(|_| rusqlite::Error::InvalidQuery)?
                                } else {
                                    text.into()
                                }
                            }
                            _ => return Err(rusqlite::Error::InvalidQuery),
                        };
                        values.insert(name.clone(), value);
                    }
                    Ok(Value::Object(values))
                })
                .optional()
                .map_err(error)?
            }
        };
        if let Some(record) = record {
            providers.insert(id.into(), record);
        }
    }
    let sql = match schema {
        Schema::Native => {
            "SELECT provider_id,model_id FROM provider_selections WHERE scope='global'"
        }
        Schema::Stable(_) => {
            "SELECT providerId,modelId FROM provider_selections WHERE scope='global'"
        }
    };
    let selected: Option<(String, Option<String>)> = db
        .query_row(sql, [], |r| Ok((r.get(0)?, r.get(1)?)))
        .optional()
        .map_err(error)?;
    let mut result = Map::new();
    if !providers.is_empty() {
        result.insert("providers".into(), Value::Object(providers));
    }
    if let Some((provider, model)) = selected {
        result.insert(
            "selection".into(),
            json!({"providerId":provider,"modelId":model}),
        );
    }
    Ok(Value::Object(result))
}

pub(super) fn read(path: &Path) -> Result<Option<String>, String> {
    if !path.exists() {
        return Ok(None);
    }
    let mut db = open(path, false)?;
    let tx = db.transaction().map_err(error)?;
    let schema = schema(&tx)?;
    serde_json::to_string_pretty(&snapshot(&tx, &schema)?)
        .map(Some)
        .map_err(|_| "unable to read DimAgent settings".into())
}

fn parse(text: Option<&str>) -> Result<Value, String> {
    let value: Value = serde_json::from_str(text.unwrap_or("{}"))
        .map_err(|_| "invalid DimAgent settings snapshot")?;
    let root = value
        .as_object()
        .ok_or("invalid DimAgent settings snapshot")?;
    if root.keys().any(|k| k != "providers" && k != "selection") {
        return Err("invalid DimAgent settings snapshot".into());
    }
    if let Some(providers) = root.get("providers") {
        let entries = providers.as_object().ok_or("invalid DimAgent providers")?;
        if entries
            .iter()
            .any(|(id, provider)| !owned(id) || !provider.is_object())
        {
            return Err("invalid DimAgent providers".into());
        }
    }
    if let Some(selection) = root.get("selection") {
        if !selection["providerId"].is_string()
            || !(selection["modelId"].is_string() || selection["modelId"].is_null())
        {
            return Err("invalid DimAgent selection".into());
        }
    }
    Ok(value)
}

fn write_provider(
    db: &Connection,
    schema: &Schema,
    id: &str,
    provider: &Value,
) -> Result<(), String> {
    let now = chrono::Utc::now().to_rfc3339();
    match schema {
        Schema::Native => {
            let version: Option<u32> = db
                .query_row(
                    "SELECT version FROM providers WHERE provider_id=?1",
                    [id],
                    |r| r.get(0),
                )
                .optional()
                .map_err(error)?;
            let version = version
                .unwrap_or(0)
                .checked_add(1)
                .ok_or("DimAgent provider version overflow")?;
            let mut provider = provider.clone();
            provider["version"] = version.into();
            db.execute("INSERT INTO providers(provider_id,version,record) VALUES (?1,?2,?3) ON CONFLICT(provider_id) DO UPDATE SET version=excluded.version,record=excluded.record", params![id,version,provider.to_string()]).map_err(error)?;
        }
        Schema::Stable(columns) => {
            let previous: Option<(u32, String)> = db
                .query_row(
                    "SELECT version,createdAt FROM providers WHERE providerId=?1",
                    [id],
                    |r| Ok((r.get(0)?, r.get(1)?)),
                )
                .optional()
                .map_err(error)?;
            let version = previous
                .as_ref()
                .map_or(0, |v| v.0)
                .checked_add(1)
                .ok_or("DimAgent provider version overflow")?;
            let values = columns
                .iter()
                .map(|name| match name.as_str() {
                    "providerId" => SqlValue::Text(id.into()),
                    "version" => SqlValue::Integer(version.into()),
                    "createdAt" => SqlValue::Text(previous.as_ref().map_or(&now, |v| &v.1).clone()),
                    "updatedAt" => SqlValue::Text(now.clone()),
                    name => match provider.get(name) {
                        None | Some(Value::Null) => SqlValue::Null,
                        Some(value) if JSON_COLUMNS.contains(&name) => {
                            SqlValue::Text(value.to_string())
                        }
                        Some(Value::String(value)) => SqlValue::Text(value.clone()),
                        Some(Value::Bool(value)) => SqlValue::Integer(i64::from(*value)),
                        Some(Value::Number(value)) => value
                            .as_i64()
                            .map(SqlValue::Integer)
                            .or_else(|| value.as_f64().map(SqlValue::Real))
                            .unwrap_or(SqlValue::Null),
                        Some(_) => SqlValue::Null,
                    },
                })
                .collect::<Vec<_>>();
            let names = columns.iter().map(|c| identifier(c)).collect::<Vec<_>>();
            let updates = columns
                .iter()
                .filter(|c| !["providerId", "createdAt"].contains(&c.as_str()))
                .map(|c| format!("{0}=excluded.{0}", identifier(c)))
                .collect::<Vec<_>>();
            let sql = format!(
                "INSERT INTO providers ({}) VALUES ({}) ON CONFLICT(providerId) DO UPDATE SET {}",
                names.join(","),
                vec!["?"; columns.len()].join(","),
                updates.join(",")
            );
            db.execute(&sql, params_from_iter(values)).map_err(error)?;
        }
    }
    Ok(())
}

fn selection(db: &Connection, schema: &Schema, value: Option<&Value>) -> Result<(), String> {
    let Some(value) = value else {
        db.execute("DELETE FROM provider_selections WHERE scope='global'", [])
            .map_err(error)?;
        return Ok(());
    };
    let provider = value["providerId"]
        .as_str()
        .ok_or("invalid DimAgent selection")?;
    let model = value["modelId"].as_str();
    match schema {
        Schema::Native => {
            db.execute("INSERT INTO provider_selections(scope,version,provider_id,model_id) VALUES ('global',1,?1,?2) ON CONFLICT(scope) DO UPDATE SET version=version+1,provider_id=excluded.provider_id,model_id=excluded.model_id", params![provider, model]).map_err(error)?;
        }
        Schema::Stable(_) => {
            let now = chrono::Utc::now().to_rfc3339();
            db.execute("INSERT INTO provider_selections(selectionId,scope,providerId,modelId,createdAt,updatedAt,version) VALUES ('global','global',?1,?2,?3,?3,1) ON CONFLICT(selectionId) DO UPDATE SET providerId=excluded.providerId,modelId=excluded.modelId,updatedAt=excluded.updatedAt,version=version+1", params![provider,model,now]).map_err(error)?;
        }
    }
    Ok(())
}

pub(super) fn write(path: &Path, before: Option<&str>, after: Option<&str>) -> Result<(), String> {
    if !path.is_file() {
        return Err("open DimAgent once before writing its settings".into());
    }
    let resolved = std::fs::canonicalize(path).map_err(|_| "unable to locate DimAgent settings")?;
    let path = resolved.as_path();
    let expected = parse(before)?;
    let next = parse(after)?;
    if let Some(id) = next["selection"]["providerId"]
        .as_str()
        .filter(|id| owned(id))
    {
        if next["providers"].get(id).is_none() {
            return Err(
                "DimAgent still selects this provider; switch its model before removing it".into(),
            );
        }
    }
    let mut db = open(path, true)?;
    let tx = db
        .transaction_with_behavior(TransactionBehavior::Immediate)
        .map_err(error)?;
    let schema = schema(&tx)?;
    let current = snapshot(&tx, &schema)?;
    if current != expected {
        return Err("DimAgent settings changed while being edited; try again".into());
    }
    secure(path)?;
    for (_, id, _) in ROUTES {
        let old = current["providers"].get(id);
        let new = next["providers"].get(id);
        if old == new {
            continue;
        }
        if let Some(provider) = new {
            write_provider(&tx, &schema, id, provider)?;
        } else {
            let column = match schema {
                Schema::Native => "provider_id",
                Schema::Stable(_) => "providerId",
            };
            let referenced: bool = tx.query_row(
                &format!("SELECT EXISTS(SELECT 1 FROM provider_selections WHERE {column}=?1 AND scope<>'global')"),
                [id], |r| r.get(0),
            ).map_err(error)?;
            if referenced {
                return Err("a DimAgent workspace still selects this provider; switch that workspace before removing it".into());
            }
            tx.execute(&format!("DELETE FROM providers WHERE {column}=?1"), [id])
                .map_err(error)?;
        }
    }
    if current.get("selection") != next.get("selection") {
        selection(&tx, &schema, next.get("selection"))?;
    }
    tx.commit().map_err(error)
}

fn secure(path: &Path) -> Result<(), String> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        for suffix in ["", "-wal", "-shm", "-journal"] {
            let mut name = path.as_os_str().to_os_string();
            name.push(suffix);
            let file = Path::new(&name);
            if file.exists() {
                std::fs::set_permissions(file, std::fs::Permissions::from_mode(0o600))
                    .map_err(|_| "unable to protect DimAgent credentials")?;
            }
        }
    }
    #[cfg(not(unix))]
    let _ = path;
    Ok(())
}
