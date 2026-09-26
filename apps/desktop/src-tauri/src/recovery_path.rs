use serde_json::Value;
fn text(value: &Value, max: usize) -> Result<&str, String> {
    value
        .as_str()
        .filter(|s| s.chars().count() <= max)
        .ok_or("invalid path text".into())
}
pub(crate) fn id(value: &Value) -> Result<(), String> {
    let s = text(value, 96)?;
    if s.len() < 3
        || !s.as_bytes()[0].is_ascii_lowercase()
        || !s
            .bytes()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'_' || c == b'-')
    {
        return Err("invalid path ID".into());
    }
    Ok(())
}
pub(crate) fn protocol(value: &Value) -> Result<(), String> {
    let s = text(value, 96)?;
    if s.len() < 3
        || !s.as_bytes()[0].is_ascii_lowercase()
        || !s.bytes().all(|c| {
            c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'_' || c == b'.' || c == b'-'
        })
    {
        return Err("invalid path protocol".into());
    }
    Ok(())
}
